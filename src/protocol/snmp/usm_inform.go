package snmp

import (
	"net"
	"time"
)

// usm_inform.go is the authoritative SNMPv3 role: answer engine
// discovery as the authoritative engine, verify/decrypt informs against
// FlowSeer's own keys, apply the §3.2 authoritative time window plus the
// post-restart quarantine, and return a Response-PDU ack (or a Report on
// rejection). It adds the listener's write path. Every failure is a typed
// drop, never a panic.

// authoritativeBoots is FlowSeer's snmpEngineBoots in the authoritative
// role, pinned to 2^31-1. With boots constant across restarts the
// §3.2 boot-sequence check is permanently disabled and the time window is
// the sole gate; the post-restart quarantine reduces (does not close) the
// cross-restart replay surface — full closure needs persistent boots.
const authoritativeBoots int32 = 2147483647

// quarantineSeconds is the post-restart window during which all informs are
// rejected: the time window resets to ±150s around snmpEngineTime
// after every restart, so blacking out the first 150s closes the immediate
// post-restart replay window.
const quarantineSeconds = 150

// snmpEngineTime returns FlowSeer's authoritative snmpEngineTime: seconds
// since listener start, read lock-free from the monotonic base.
func (l *listener) snmpEngineTime() int32 {
	start := time.Unix(0, l.startedNanos.Load())
	s := int64(time.Since(start).Seconds())
	if s < 0 {
		return 0
	}
	if s > 2147483647 {
		return 2147483647
	}
	return int32(s)
}

// sendTo writes a datagram back to remote on the listener's socket. The
// listenLoop is single-goroutine, so writes serialize with reads without new
// locking. A short write deadline keeps the send genuinely non-blocking
// under flood: if the kernel send buffer is full,
// the write fails fast instead of stalling reception of other senders'
// notifications. The deadline is independent of the read path, so it does
// not affect ReadFromUDP. A send failure is best-effort-dropped; the sender
// retransmits (or resyncs) on its own.
func (l *listener) sendTo(remote *net.UDPAddr, datagram []byte) {
	if remote == nil || len(datagram) == 0 {
		return
	}
	_ = l.conn.SetWriteDeadline(time.Now().Add(sendWriteTimeout))
	_, _ = l.conn.WriteToUDP(datagram, remote)
	_ = l.conn.SetWriteDeadline(time.Time{})
}

// sendWriteTimeout bounds a single outbound ack/Report write so a congested
// socket cannot stall the receive loop.
const sendWriteTimeout = 100 * time.Millisecond

// respondDiscovery answers an engine-discovery probe with an unauthenticated
// Report carrying our authoritative engineID, boots (2^31-1), and current
// snmpEngineTime, echoing the probe's msgID so the sender's discovery waiter
// matches (the discovery carve-out on the sender side).
func (l *listener) respondDiscovery(dec *v3Decoded, remote *net.UDPAddr) {
	var innerRID int32
	if dec.msg.scoped != nil {
		innerRID = dec.msg.scoped.pdu.requestID
	}
	report := &v3Message{
		msgID:         dec.msg.msgID,
		msgMaxSize:    v3MaxMessageSize,
		securityModel: securityModelUSM,
		flags:         msgFlags{},
		sec: usmSecurityParameters{
			engineID:    l.ownEngineID,
			engineBoots: authoritativeBoots,
			engineTime:  l.snmpEngineTime(),
			userName:    dec.msg.sec.userName,
		},
		scoped: &scopedPDU{
			contextEngineID: l.ownEngineID,
			pdu:             pdu{typ: pduReport, requestID: innerRID, varbinds: []VarBind{usmStatsVB(4)}},
		},
	}
	raw, err := encodeV3Message(report)
	if err != nil {
		l.ts.recordDropped()
		return
	}
	l.sendTo(remote, raw)
}

// handleAuthoritative is the authoritative-role branch: an
// unauthenticated reportable probe is answered as discovery; an
// authenticated inform is verified against our own keys, time-checked, and
// acked (or Report-rejected).
func (l *listener) handleAuthoritative(dec *v3Decoded, remote *net.UDPAddr) {
	m := dec.msg

	// An unauthenticated reportable message to ourselves that already knows
	// our engineID is a discovery probe (or a re-probe); answer it.
	if !m.flags.auth {
		if m.flags.reportable {
			l.respondDiscovery(dec, remote)
		} else {
			l.ts.recordDropped()
		}
		return
	}

	// Authenticated inform: our own keys are keyed by (ownEngineID, userName).
	u, _, ok := l.engines.lookup(l.ownEngineID, m.sec.userName)
	if !ok {
		l.ts.recordDropped()
		return
	}

	// Inbound security-level floor (mirror of the trap path, security #5).
	if m.flags.level() < u.level {
		l.ts.recordDropped()
		return
	}

	sp, isReport, err := u.inboundGate(dec)
	if err != nil {
		l.ts.recordDropped()
		return
	}
	if isReport {
		l.ts.recordDropped()
		return
	}

	// Role/PDU-type check: the authoritative branch must decrypt to
	// an InformRequest, not a trap.
	if sp.pdu.typ != pduInformRequest {
		l.ts.recordDropped()
		return
	}

	// Refined against live Net-SNMP: the authentication anchor is
	// msgAuthoritativeEngineID == ownEngineID (the dual-role routing gate) plus
	// the HMAC keyed to our engineID — both already enforced above. The
	// scoped contextEngineID is NOT validated against ownEngineID: per RFC
	// 3412 it identifies the data's context (the *sender's* engine for a
	// notification), and real senders (net-snmp) set it to their own
	// engineID, not the receiver's. It is echoed verbatim in the ack and is
	// never trusted as a routing/authorization key — same discipline as
	// contextName.

	// §3.2 authoritative window + post-restart quarantine. On rejection,
	// Report back so the sender resyncs rather than retransmitting blindly.
	if !l.authoritativeTimeOK(m.sec.engineBoots, m.sec.engineTime) {
		l.sendNotInTimeWindow(remote, u, m.msgID)
		return
	}

	// Accept: ack so the sender stops retransmitting, then surface the inform.
	l.sendInformAck(remote, u, m.msgID, sp)

	// Surface the inform with the SENDER's engine identity, not ours. For an
	// inform, msgAuthoritativeEngineID is FlowSeer's own engineID (we are the
	// authoritative engine for the time window), so it cannot identify the
	// originating device. The sender's engine is carried in the scoped
	// contextEngineID (RFC 3412); fall back to userName-only attribution when
	// it is empty.
	trap := translateV3Trap(m, sp, remote)
	if len(sp.contextEngineID) != 0 {
		trap.EngineID = append([]byte(nil), sp.contextEngineID...)
	}
	l.ts.Push(trap)
}

// authoritativeTimeOK applies the §3.2 authoritative window (symmetric ±150s
// around the live snmpEngineTime, boots pinned) plus the post-restart
// quarantine (reject everything for the first quarantineSeconds).
func (l *listener) authoritativeTimeOK(boots, etime int32) bool {
	return informTimeWindowOK(l.snmpEngineTime(), boots, etime)
}

// informTimeWindowOK is the pure §3.2 authoritative-window predicate, split out
// so it is unit-testable with an injected now. The time difference is computed
// in int64 so a forged boundary engineTime (e.g. math.MinInt32) cannot wrap an
// int32 subtraction into a spurious in-window accept (usm-inform-timewindow).
func informTimeWindowOK(now, boots, etime int32) bool {
	if now < quarantineSeconds {
		return false
	}
	if boots != authoritativeBoots {
		return false
	}
	d := int64(now) - int64(etime)
	if d < 0 {
		d = -d
	}
	return d <= 150
}

// sendInformAck sends the Response-PDU ack: echoed request-id and varbinds,
// contextName echoed verbatim, at the inform's security level. A fresh msgID
// and (under authPriv) a fresh salt from the engine's priv context keep the
// ack's IV unique.
func (l *listener) sendInformAck(remote *net.UDPAddr, u *usmContext, msgID int32, inform *scopedPDU) {
	ackSP := &scopedPDU{
		contextEngineID: inform.contextEngineID, // echoed verbatim
		contextName:     inform.contextName,     // echoed verbatim, not trusted
		pdu: pdu{
			typ:       pduGetResponse,
			requestID: inform.pdu.requestID,
			varbinds:  inform.pdu.varbinds,
		},
	}
	raw, err := u.buildOutboundScoped(msgID, ackSP, authoritativeBoots, l.snmpEngineTime(), false)
	if err != nil {
		l.ts.recordDropped()
		return
	}
	l.sendTo(remote, raw)
}

// sendNotInTimeWindow sends an authenticated usmStatsNotInTimeWindows Report
// (authNoPriv, carrying our boots/time) so the sender resyncs,
// instead of a silent drop that would trigger indefinite retransmission.
func (l *listener) sendNotInTimeWindow(remote *net.UDPAddr, u *usmContext, msgID int32) {
	if u.authProto == AuthProtocolNone {
		return // cannot authenticate a Report without keys
	}
	m := &v3Message{
		msgID:         msgID,
		msgMaxSize:    v3MaxMessageSize,
		securityModel: securityModelUSM,
		flags:         msgFlags{auth: true},
		sec: usmSecurityParameters{
			engineID:    l.ownEngineID,
			engineBoots: authoritativeBoots,
			engineTime:  l.snmpEngineTime(),
			userName:    u.userName,
		},
		scoped: &scopedPDU{
			contextEngineID: l.ownEngineID,
			pdu:             pdu{typ: pduReport, requestID: 0, varbinds: []VarBind{usmStatsVB(2)}},
		},
	}
	raw, err := u.signMessage(m, u.loadAuthKey())
	if err != nil {
		return
	}
	l.sendTo(remote, raw)
}

// usmStatsVB builds a usmStats counter varbind for a Report (counter index
// per RFC 3414 §5).
func usmStatsVB(counter uint32) VarBind {
	return Counter32Var{
		Header: Header{OID: MustOID(1, 3, 6, 1, 6, 3, 15, 1, 1, counter, 0), Kind: KindCounter32},
		Value:  1,
	}
}
