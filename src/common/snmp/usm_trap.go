package snmp

import (
	"bytes"
	crand "crypto/rand"
	"net"
	"time"

	"go.aledante.io/ae"
)

// usm_trap.go is the non-authoritative SNMPv3 trap reception path
// plus the dual-role router that the listener's handlePacket forks v3
// datagrams into. Every failure on the receive path is a silent
// drop+RecordDropped, never a panic.

// resolveOwnEngineID validates a configured engineID or derives a local one.
// The derivation is an RFC 3411 §5.2 format-5 (octets) engineID
// with a crypto/rand tail — a dev/single-host fallback. It is ephemeral
// across restarts, which invalidates registered inform
// senders' keys on restart; production should configure a durable engineID
// via [WithOwnEngineID].
func resolveOwnEngineID(configured []byte) ([]byte, error) {
	if len(configured) != 0 {
		if n := len(configured); n < 5 || n > 32 {
			return nil, ae.New().Attr("length", n).Msg("ownEngineID length outside RFC 3411 range [5,32]")
		}
		return append([]byte(nil), configured...), nil
	}
	// 0x80|enterprise(4) with format octet 5 ("octets"), then 8 random bytes.
	eid := []byte{0x80, 0x00, 0x00, 0x00, 0x05}
	tail := make([]byte, 8)
	if _, err := crand.Read(tail); err != nil {
		// crypto/rand failure: fall back to a fixed but valid tail rather than
		// failing listener start.
		tail = []byte{1, 2, 3, 4, 5, 6, 7, 8}
	}
	return append(eid, tail...), nil
}

// handleV3Notification routes a decoded v3 datagram by role: a datagram
// whose msgAuthoritativeEngineID equals our ownEngineID is for the
// authoritative role (inform / discovery-to-us); anything else is a
// non-authoritative trap from a sender that is its own authoritative engine.
func (l *listener) handleV3Notification(dec *v3Decoded, remote *net.UDPAddr) {
	m := dec.msg
	if bytes.Equal(m.sec.engineID, l.ownEngineID) {
		l.handleAuthoritative(dec, remote)
		return
	}
	// An engine-discovery probe directed at us carries an empty engineID (the
	// sender does not know ours yet); it is unauthenticated and reportable.
	// A trap always carries the sender's own non-empty authoritative
	// engineID, so this cannot be mistaken for one.
	if len(m.sec.engineID) == 0 && m.flags.reportable && !m.flags.auth {
		l.respondDiscovery(dec, remote)
		return
	}
	l.handleNonAuthTrap(dec, remote)
}

// handleNonAuthTrap verifies and decrypts a v3 trap against the sender's
// engine-table entry, applies the §3.2 non-authoritative time check, and
// emits a [Trap]. Resolution is by direct (engineID, userName) lookup,
// never trial-decryption.
func (l *listener) handleNonAuthTrap(dec *v3Decoded, remote *net.UDPAddr) {
	m := dec.msg
	u, base, ok := l.engines.lookup(m.sec.engineID, m.sec.userName)
	if !ok {
		l.ts.recordDropped() // no registered engine: clean drop, never trial-decrypt
		return
	}

	// Inbound security-level floor (security review #5): a trap weaker than
	// the level the engine is registered at is rejected. inboundGate enforces
	// the same via its downgrade check, but rejecting here avoids the crypto
	// on an obviously-too-weak datagram.
	if m.flags.level() < u.level {
		l.ts.recordDropped()
		return
	}

	sp, isReport, err := u.inboundGate(dec)
	if err != nil {
		l.ts.recordDropped() // wrong digest / decryption error / downgrade
		return
	}
	if isReport {
		l.ts.recordDropped() // a Report is never a trap; never surfaced as data
		return
	}

	// Role/PDU-type check: the non-authoritative branch must decrypt
	// to a notification PDU, not an inform.
	if sp.pdu.typ != pduV2Trap {
		l.ts.recordDropped()
		return
	}

	// §3.2 non-authoritative time check, baseline updated only after the full
	// verify+decrypt succeeded (baseline-after-success, TOCTOU-safe).
	if !base.checkAndUpdate(m.sec.engineBoots, m.sec.engineTime) {
		l.ts.recordDropped()
		return
	}

	l.ts.Push(translateV3Trap(m, sp, remote))
}

// translateV3Trap builds a [Trap] from a verified v3 notification. The
// SNMPv2-Trap-PDU varbinds are already canonical (sysUpTime.0 /
// snmpTrapOID.0-led), so they pass through unchanged; EngineID and UserName
// are populated from the security parameters.
func translateV3Trap(m *v3Message, sp *scopedPDU, remote *net.UDPAddr) Trap {
	t := Trap{
		Received: time.Now(),
		Version:  V3,
		EngineID: append([]byte(nil), m.sec.engineID...),
		UserName: m.sec.userName,
		VarBinds: sp.pdu.varbinds,
	}
	if remote != nil {
		if v4 := remote.IP.To4(); v4 != nil {
			t.Source = v4
		} else {
			t.Source = remote.IP
		}
	}
	return t
}
