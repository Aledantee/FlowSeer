package snmp

import (
	"context"
	"math"
	"sync"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// usm_discovery.go drives the RFC 3414 §5 engine discovery and §3.2
// time-window resync for a v3 polling session, layered around the reactor's
// msgID demux. Two budgets are composed explicitly, never merged:
// the lazy single-flight discovery (its own timeout+retransmit) and
// the per-op resync (at most one notInTimeWindow resync + one re-discovery,
// then a typed failure).

// Discovery / resync sentinels.
var (
	ErrResyncExhausted = errs.Msg("USM time-window resync budget exhausted")
	ErrDiscoveryFailed = errs.Msg("USM engine discovery failed")

	ErrReportUnsupportedSecLevel = errs.Msg("USM peer reported unsupported security level")
	ErrReportUnknownUserName     = errs.Msg("USM peer reported unknown user name")
	ErrReportWrongDigest         = errs.Msg("USM peer reported wrong digest")
	ErrReportDecryptionError     = errs.Msg("USM peer reported decryption error")
	ErrReportUnexpected          = errs.Msg("USM peer returned an unexpected report")
)

// reportError maps a Report outcome that terminates an operation to a typed
// error (the notInTimeWindow and unknownEngineID outcomes are handled by the
// resync/re-discovery loop and never reach here).
func reportError(o reportOutcome) error {
	switch o {
	case reportUnsupportedSecLevel:
		return ErrReportUnsupportedSecLevel
	case reportUnknownUserName:
		return ErrReportUnknownUserName
	case reportWrongDigest:
		return ErrReportWrongDigest
	case reportDecryptionError:
		return ErrReportDecryptionError
	default:
		return ErrReportUnexpected
	}
}

// engineBaseline tracks the authoritative engine's (boots, time) for a v3
// polling session. snapshot advances time by the wall-clock elapsed since
// the baseline was learned; update resets it from a discovery or resync
// Report. The lock keeps the (boots, time) pair consistent so a concurrent
// resync cannot tear one encode's snapshot (the IV-reuse guard pairs a
// snapshot with the salt allocated inside that same buildOutbound).
type engineBaseline struct {
	mu      sync.Mutex
	boots   int32
	etime   int32
	learned time.Time
	known   bool
}

// learn establishes the baseline from an initial discovery or a re-discovery
// (a new authoritative engineID). It is unconditional: a (re)discovery is a
// deliberate fresh learning of the engine's authoritative (boots, time).
func (b *engineBaseline) learn(boots, etime int32) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.boots, b.etime, b.learned, b.known = boots, etime, time.Now(), true
}

// update applies a mid-session RFC 3414 §3.2 resync from a notInTimeWindow
// Report. It enforces §2.2.3 monotonicity: the authoritative engine's boots is
// non-decreasing, and at the same boots its time must not move backward. A
// rollback — a replayed or forged older Report — is rejected so a stale
// baseline cannot poison the outbound time window into a self-inflicted DoS
// (usm-timewindow-rollback). It reports whether the update was applied.
func (b *engineBaseline) update(boots, etime int32) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.known && (boots < b.boots || (boots == b.boots && etime < b.etime)) {
		return false
	}
	b.boots, b.etime, b.learned, b.known = boots, etime, time.Now(), true
	return true
}

// snapshot returns the current (boots, time) estimate, advancing time by the
// elapsed wall-clock. ok is false until the baseline has been learned.
func (b *engineBaseline) snapshot() (boots, etime int32, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.known {
		return 0, 0, false
	}
	elapsed := int64(time.Since(b.learned).Seconds())
	t := int64(b.etime) + elapsed
	if t > math.MaxInt32 {
		t = math.MaxInt32
	}
	return b.boots, int32(t), true
}

// buildDiscoveryProbe builds the RFC 3414 §4 engine-discovery probe: a
// noAuthNoPriv, reportable GetRequest with an empty engineID, empty
// userName, and empty contextEngineID. The agent answers with a Report
// carrying its authoritative engineID/boots/time.
func buildDiscoveryProbe(msgID int32) ([]byte, error) {
	m := &v3Message{
		msgID:         msgID,
		msgMaxSize:    v3MaxMessageSize,
		securityModel: securityModelUSM,
		flags:         msgFlags{reportable: true},
		sec:           usmSecurityParameters{},
		scoped: &scopedPDU{
			pdu: pdu{typ: pduGetRequest, requestID: 0},
		},
	}
	return encodeV3Message(m)
}

// v3RoundTrip is the full v3 polling exchange: discover the engine if needed
// (lazy, single-flighted, own budget), then run the op with at most one
// §3.2 resync and one re-discovery before a typed failure.
func (r *reactor) v3RoundTrip(ctx context.Context, p pdu, timeout time.Duration, retries int) (*v3Result, error) {
	if err := r.ensureDiscovered(ctx, timeout, retries); err != nil {
		return nil, err
	}

	resynced := false
	rediscovered := false
	for {
		boots, etime, _ := r.baseline.snapshot()
		res, err := r.v3Attempts(ctx, p, boots, etime, timeout, retries)
		if err != nil {
			return nil, err
		}
		if !res.isReport {
			return res, nil
		}
		switch res.report {
		case reportNotInTimeWindow:
			if resynced {
				return nil, ErrResyncExhausted
			}
			if !r.baseline.update(res.reportBoots, res.reportTime) {
				// Rollback attempt (boots decreased or time moved backward): a
				// replayed/forged older Report. Leave the baseline intact and
				// fail rather than self-DoS with a rolled-back time window.
				return nil, ErrResyncExhausted
			}
			resynced = true
		case reportUnknownEngineID:
			if rediscovered {
				return nil, ErrDiscoveryFailed
			}
			if err := r.applyRediscovery(res); err != nil {
				return nil, err
			}
			rediscovered = true
		default:
			return nil, reportError(res.report)
		}
	}
}

// ensureDiscovered runs lazy single-flight discovery before the first
// authenticated op. A session with a configured engineID already has keys,
// so discovery is skipped (its baseline stays unknown until the first resync
// Report). The mutex makes concurrent first-ops issue exactly one probe; it
// is retryable (release-on-completion, not a latching sync.Once) so a failed
// probe does not brick the session.
func (r *reactor) ensureDiscovered(ctx context.Context, timeout time.Duration, retries int) error {
	if r.usm.hasEngine() {
		return nil
	}
	r.discoMu.Lock()
	defer r.discoMu.Unlock()
	if r.usm.hasEngine() {
		return nil
	}
	eid, boots, etime, err := r.runDiscoveryProbe(ctx, timeout, retries)
	if err != nil {
		return err
	}
	if err := r.usm.setEngine(eid); err != nil {
		return err
	}
	r.baseline.learn(boots, etime)
	return nil
}

// applyRediscovery handles a persistent unknownEngineID during ops: the
// Report carries the engine's (new) authoritative engineID, so the keys are
// re-localized to it and the baseline reset — no extra probe needed. Guarded
// by discoMu so it composes with the first-op single-flight.
func (r *reactor) applyRediscovery(res *v3Result) error {
	if len(res.reportEngineID) < 5 {
		return ErrDiscoveryFailed
	}
	r.discoMu.Lock()
	defer r.discoMu.Unlock()
	if err := r.usm.setEngine(res.reportEngineID); err != nil {
		return err
	}
	r.baseline.learn(res.reportBoots, res.reportTime)
	return nil
}

// runDiscoveryProbe sends the discovery probe with its own timeout +
// retransmit budget (distinct from the per-op resync budget) and
// returns the authoritative engineID/boots/time from the Report. The
// unauthenticated Report is accepted under the reactor's discovery carve-out
// (live discovery msgID, probe window, peer source).
func (r *reactor) runDiscoveryProbe(ctx context.Context, timeout time.Duration, retries int) (engineID []byte, boots, etime int32, err error) {
	attempts := retries + 1
	if attempts < 1 {
		attempts = 1
	}
	lastErr := ErrDiscoveryFailed
	for attempt := 0; attempt < attempts; attempt++ {
		if cerr := ctx.Err(); cerr != nil {
			return nil, 0, 0, cerr
		}
		msgID, w, rerr := r.registerV3(0, SecurityLevelNoAuthNoPriv, true)
		if rerr != nil {
			return nil, 0, 0, rerr
		}
		probe, berr := buildDiscoveryProbe(msgID)
		if berr != nil {
			r.deregisterV3(msgID)
			return nil, 0, 0, berr
		}
		res, retry, serr := r.sendWaitV3(ctx, probe, w, timeout)
		r.deregisterV3(msgID)
		if serr == nil {
			if res.isReport && len(res.reportEngineID) >= 5 {
				return res.reportEngineID, res.reportBoots, res.reportTime, nil
			}
			lastErr = ErrDiscoveryFailed
			continue
		}
		if !retry {
			return nil, 0, 0, serr
		}
		lastErr = serr
	}
	return nil, 0, 0, lastErr
}
