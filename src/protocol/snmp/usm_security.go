package snmp

import (
	"context"
	"sync/atomic"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/secret"
)

// usm_security.go is the single composition point for the USM crypto units
// (key derivation, auth, priv, envelope, report). A
// usmContext holds the localized keys for one (user, engine) pair and turns
// a scoped PDU into an authenticated/encrypted v3 datagram (buildOutbound)
// and an inbound datagram back into a scoped PDU after the ordered crypto
// gate (verifyInbound). Key material lives only here and never crosses into
// spans, logs, or errors.

// USM security sentinels.
var (
	// ErrUSMDowngrade is returned when an inbound reply's actual protection
	// is weaker than the session requested: an authPriv context
	// receiving an unencrypted reply, or an authNoPriv context receiving an
	// unauthenticated one.
	ErrUSMDowngrade = errs.Msg("reply security level weaker than requested")
	// ErrUSMNoKeys is returned when an authenticated operation is attempted
	// before the authoritative engineID is known (discovery has not run).
	ErrUSMNoKeys = errs.Msg("USM keys not yet derived")
	// errUSMScopedMissing is returned when a verified message carries no
	// scoped PDU.
	errUSMScopedMissing = errs.Msg("USM message has no scoped PDU")
)

// usmContext is the per-session (or per-engine) USM processor. It is built
// from a [USMConfig]; the localized keys are derived once the
// authoritative engineID is known (immediately when configured, or after
// discovery, via setEngine).
type usmContext struct {
	userName  string
	level     SecurityLevel
	authProto AuthProtocol
	privProto PrivProtocol

	authPass secret.Value
	privPass secret.Value

	logCtx context.Context

	// engine is the atomically-published immutable key snapshot. It is nil
	// until setEngine runs (discovery on the polling path; construction on the
	// trap path). An atomic snapshot lets the many concurrent polling ops read
	// the keys lock-free while a lazy discovery (or re-discovery) publishes a
	// new snapshot — no torn read of engineID-vs-keys.
	engine atomic.Pointer[engineKeys]
}

// engineKeys is the immutable derived-key snapshot for one engineID.
type engineKeys struct {
	engineID []byte
	authKey  []byte // nil for noAuth
	priv     *privContext
}

// newUSMContext builds a USM processor from cfg. The config is assumed
// already validated (Dial calls cfg.Validate via USMValidationError). When
// cfg.EngineID is set the keys are derived immediately; otherwise the
// caller runs discovery and then setEngine.
func newUSMContext(ctx context.Context, cfg USMConfig) (*usmContext, error) {
	u := &usmContext{
		userName:  cfg.Username,
		level:     cfg.Level(),
		authProto: cfg.AuthProtocol,
		privProto: cfg.PrivProtocol,
		authPass:  cfg.AuthPassphrase,
		privPass:  cfg.PrivPassphrase,
		logCtx:    context.WithoutCancel(ctx),
	}
	if u.level == SecurityLevelUnknown {
		return nil, errs.Msg("USMConfig produces no valid security level")
	}
	if len(cfg.EngineID) != 0 {
		if err := u.setEngine(cfg.EngineID); err != nil {
			return nil, err
		}
	}
	return u, nil
}

// setEngine binds the authoritative engineID and derives the localized auth
// and priv keys for it (idempotent re-derivation on a re-discovery). It is
// the only place keys are computed.
func (u *usmContext) setEngine(engineID []byte) error {
	if len(engineID) == 0 {
		return errs.Msg("setEngine requires a non-empty engineID")
	}
	eid := cloneBytes(engineID)

	var authKey []byte
	var priv *privContext
	if u.authProto != AuthProtocolNone {
		k, err := localizedAuthKey(u.authProto, u.authPass, eid)
		if err != nil {
			return err
		}
		authKey = k
	}
	if u.privProto != PrivProtocolNone {
		k, err := localizedPrivKey(u.authProto, u.privProto, u.privPass, eid)
		if err != nil {
			return err
		}
		pc, err := newPrivContext(u.logCtx, u.privProto, k)
		if err != nil {
			return err
		}
		priv = pc
	}
	u.engine.Store(&engineKeys{engineID: eid, authKey: authKey, priv: priv})
	return nil
}

// hasEngine reports whether the authoritative engineID and keys are set.
func (u *usmContext) hasEngine() bool { return u.engine.Load() != nil }

// currentEngineID returns the published engineID (nil before discovery).
func (u *usmContext) currentEngineID() []byte {
	if ek := u.engine.Load(); ek != nil {
		return ek.engineID
	}
	return nil
}

// loadAuthKey returns the published localized auth key (nil if unset/noAuth).
func (u *usmContext) loadAuthKey() []byte {
	if ek := u.engine.Load(); ek != nil {
		return ek.authKey
	}
	return nil
}

// buildOutbound assembles a v3 datagram carrying p as the scoped PDU,
// authenticated and encrypted per the context's security level. boots/time
// are the (atomically snapshotted) authoritative engine baseline; they bind
// the privacy IV/salt for this exact encode (the IV-reuse guard). reportable
// sets the msgFlags reportable bit.
func (u *usmContext) buildOutbound(msgID int32, p pdu, boots, etime int32, reportable bool) ([]byte, error) {
	ek := u.engine.Load()
	if ek == nil {
		return nil, ErrUSMNoKeys
	}
	return u.buildOutboundScoped(msgID, &scopedPDU{contextEngineID: ek.engineID, pdu: p}, boots, etime, reportable)
}

// buildOutboundScoped is buildOutbound with a caller-supplied scoped PDU, so
// the authoritative inform ack can echo the inform's contextName/contextEngineID
// verbatim rather than the default empty contextName.
func (u *usmContext) buildOutboundScoped(msgID int32, sp *scopedPDU, boots, etime int32, reportable bool) ([]byte, error) {
	ek := u.engine.Load()
	if ek == nil {
		return nil, ErrUSMNoKeys
	}
	m := &v3Message{
		msgID:         msgID,
		msgMaxSize:    v3MaxMessageSize,
		securityModel: securityModelUSM,
		flags: msgFlags{
			reportable: reportable,
			auth:       u.level >= SecurityLevelAuthNoPriv,
			priv:       u.level == SecurityLevelAuthPriv,
		},
		sec: usmSecurityParameters{
			engineID:    ek.engineID,
			engineBoots: boots,
			engineTime:  etime,
			userName:    u.userName,
		},
	}

	if u.level == SecurityLevelAuthPriv {
		plaintext, err := encodeScopedPDU(sp)
		if err != nil {
			return nil, err
		}
		ct, privParams, err := ek.priv.encrypt(uint32(boots), uint32(etime), plaintext)
		if err != nil {
			return nil, err
		}
		m.ciphertext = ct
		m.sec.privParams = privParams
	} else {
		m.scoped = sp
	}

	if u.level >= SecurityLevelAuthNoPriv {
		return u.signMessage(m, ek.authKey)
	}
	return encodeV3Message(m)
}

// signMessage zero-fills msgAuthenticationParameters to the truncated
// length, encodes, computes the HMAC over those bytes, then re-encodes with
// the digest in place (the two encodings differ only in that field's
// content, so the digest matches what the peer recomputes after zeroing).
func (u *usmContext) signMessage(m *v3Message, authKey []byte) ([]byte, error) {
	trunc, err := authParamLen(u.authProto)
	if err != nil {
		return nil, err
	}
	m.sec.authParams = make([]byte, trunc)
	raw0, err := encodeV3Message(m)
	if err != nil {
		return nil, err
	}
	mac, err := authMAC(u.authProto, authKey, raw0)
	if err != nil {
		return nil, err
	}
	m.sec.authParams = mac
	return encodeV3Message(m)
}

// verifyInbound runs the ordered crypto gate on a decoded inbound message:
// HMAC verify → security-level/downgrade check → decrypt → return
// the scoped PDU. Every failure is a typed reject (mapping to
// usmStatsWrongDigests / DecryptionErrors / a downgrade error), never a
// panic. The inner request-id check is the reactor's job (it needs
// the waiter's expected id), so it is not done here.
func (u *usmContext) verifyInbound(dec *v3Decoded) (*scopedPDU, error) {
	m := dec.msg
	ek := u.engine.Load()
	if ek == nil {
		return nil, ErrUSMNoKeys
	}

	// HMAC verify first, streaming over the received bytes with the
	// msgAuthenticationParameters window zero-treated (no datagram copy).
	if u.level >= SecurityLevelAuthNoPriv {
		if !m.flags.auth {
			return nil, ErrUSMDowngrade
		}
		if err := authVerifyOverZeroed(u.authProto, ek.authKey, dec.wholeMsg, dec.authStart, dec.authEnd, m.sec.authParams); err != nil {
			return nil, err
		}
	}

	// Downgrade check: a reply must be at least as protected as requested.
	if m.flags.level() < u.level {
		return nil, ErrUSMDowngrade
	}

	if u.level == SecurityLevelAuthPriv {
		if !dec.encrypted {
			return nil, ErrUSMDowngrade
		}
		plaintext, err := ek.priv.decrypt(uint32(m.sec.engineBoots), uint32(m.sec.engineTime), m.sec.privParams, m.ciphertext)
		if err != nil {
			return nil, err
		}
		sp, err := decodeScopedPDU(plaintext)
		if err != nil {
			// A wrong key yields BER garbage here → DecryptionErrors, no panic.
			return nil, errs.New().Cause(ErrPrivDecrypt).Msg("scoped PDU BER parse failed after decrypt")
		}
		return sp, nil
	}

	if m.scoped == nil {
		return nil, errUSMScopedMissing
	}
	return m.scoped, nil
}

// inboundGate is the read-loop variant of verifyInbound. It runs the
// crypto checks but, unlike verifyInbound, tolerates an authenticated Report
// sent at a lower security level than the session (RFC 3414 §3.2 time-sync
// Reports are authNoPriv even for an authPriv request) — Reports bypass the
// downgrade and inner-request-id checks because they carry no scoped data
// the session asked for. A real (non-Report) data reply still must meet the
// requested level. The unauthenticated discovery Report is handled by the
// reactor's discovery carve-out, not here.
func (u *usmContext) inboundGate(dec *v3Decoded) (sp *scopedPDU, isReport bool, err error) {
	m := dec.msg
	ek := u.engine.Load()
	if ek == nil {
		return nil, false, ErrUSMNoKeys
	}

	if u.level >= SecurityLevelAuthNoPriv {
		if !m.flags.auth {
			return nil, false, ErrUSMDowngrade
		}
		if err := authVerifyOverZeroed(u.authProto, ek.authKey, dec.wholeMsg, dec.authStart, dec.authEnd, m.sec.authParams); err != nil {
			return nil, false, err
		}
	}

	// Recover the scoped PDU. A Report may be sent authNoPriv even on an
	// authPriv session, so decrypt only when this message is itself
	// encrypted, not merely because the session level is authPriv.
	if m.flags.priv {
		if ek.priv == nil {
			return nil, false, ErrUSMDowngrade
		}
		plaintext, derr := ek.priv.decrypt(uint32(m.sec.engineBoots), uint32(m.sec.engineTime), m.sec.privParams, m.ciphertext)
		if derr != nil {
			return nil, false, derr
		}
		decoded, derr := decodeScopedPDU(plaintext)
		if derr != nil {
			return nil, false, errs.New().Cause(ErrPrivDecrypt).Msg("scoped PDU BER parse failed after decrypt")
		}
		sp = decoded
	} else {
		if m.scoped == nil {
			return nil, false, errUSMScopedMissing
		}
		sp = m.scoped
	}

	if _, isR := classifyReport(&sp.pdu); isR {
		return sp, true, nil
	}

	// A real data reply must be at least as protected as requested.
	if m.flags.level() < u.level {
		return nil, false, ErrUSMDowngrade
	}
	return sp, false, nil
}
