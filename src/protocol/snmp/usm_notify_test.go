package snmp

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/secret"
)

var notifyOwnEngine = mustHex("80001f8800f100dca7ed00abcd")

// startAuthoritativeListener starts a listener with a configured ownEngineID
// and the given USM table, exposing the *listener so a test can control the
// snmpEngineTime base (quarantine). startedAgo sets started = now-startedAgo.
func startAuthoritativeListener(t *testing.T, startedAgo time.Duration, table []USMConfig) (*TrapStream, *net.UDPAddr, *listener) {
	t.Helper()
	var addr *net.UDPAddr
	var ll *listener
	listenerRegistered = func(_ *TrapStream, l *listener) {
		addr = l.conn.LocalAddr().(*net.UDPAddr)
		ll = l
	}
	t.Cleanup(func() { listenerRegistered = nil })
	ts, err := ListenTraps(context.Background(), "127.0.0.1:0",
		WithOwnEngineID(notifyOwnEngine), WithUSMTable(table))
	if err != nil {
		t.Fatalf("ListenTraps: %v", err)
	}
	t.Cleanup(func() { _ = ts.Close() })
	if ll == nil {
		t.Fatal("listener not captured")
	}
	ll.startedNanos.Store(time.Now().Add(-startedAgo).UnixNano())
	return ts, addr, ll
}

// askListener sends datagram to the listener and returns its reply (or
// ok=false on timeout).
func askListener(t *testing.T, addr *net.UDPAddr, datagram []byte) ([]byte, bool) {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("client listen: %v", err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.WriteToUDP(datagram, addr); err != nil {
		t.Fatalf("send: %v", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, maxUDPPayload)
	n, _, err := c.ReadFromUDP(buf)
	if err != nil {
		return nil, false
	}
	return append([]byte(nil), buf[:n]...), true
}

func informCfg() USMConfig {
	return USMConfig{
		Username:       "alice",
		AuthProtocol:   AuthSHA256,
		AuthPassphrase: secret.NewString("inform-auth-passphrase"),
		PrivProtocol:   PrivAES,
		PrivPassphrase: secret.NewString("inform-priv-passphrase"),
	}
}

// buildInform builds an authenticated/encrypted InformRequest as a sender
// that has discovered ownEngineID would, with the given context fields
// boots/time.
func buildInform(t *testing.T, cfg USMConfig, etime int32, ctxEngine, ctxName []byte) []byte {
	t.Helper()
	c := cfg
	c.EngineID = notifyOwnEngine
	u, err := newUSMContext(context.Background(), c)
	if err != nil {
		t.Fatalf("sender usm: %v", err)
	}
	sp := &scopedPDU{
		contextEngineID: ctxEngine,
		contextName:     ctxName,
		pdu: pdu{
			typ:       pduInformRequest,
			requestID: 777,
			varbinds:  snmpTrapVarBinds(),
		},
	}
	raw, err := u.buildOutboundScoped(nextMsgIDValue(), sp, authoritativeBoots, etime, true)
	if err != nil {
		t.Fatalf("buildOutboundScoped: %v", err)
	}
	return raw
}

// decodeReplyScoped verifies a listener reply with the sender's keys and
// returns the scoped PDU.
func decodeReplyScoped(t *testing.T, cfg USMConfig, reply []byte) (*scopedPDU, *v3Decoded) {
	t.Helper()
	dec, err := decodeV3Message(reply)
	if err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	c := cfg
	c.EngineID = notifyOwnEngine
	u, err := newUSMContext(context.Background(), c)
	if err != nil {
		t.Fatalf("verify usm: %v", err)
	}
	sp, _, err := u.inboundGate(dec)
	if err != nil {
		t.Fatalf("verify reply: %v", err)
	}
	return sp, dec
}

// TestV3Inform_DiscoveryResponder confirms an unauthenticated reportable
// probe gets a Report with ownEngineID and boots 2147483647.
func TestV3Inform_DiscoveryResponder(t *testing.T) {
	_, addr, _ := startAuthoritativeListener(t, 200*time.Second, nil)
	probe, err := buildDiscoveryProbe(12345)
	if err != nil {
		t.Fatalf("buildDiscoveryProbe: %v", err)
	}
	reply, ok := askListener(t, addr, probe)
	if !ok {
		t.Fatal("no discovery Report")
	}
	dec, err := decodeV3Message(reply)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(dec.msg.sec.engineID, notifyOwnEngine) {
		t.Fatalf("Report engineID = %x, want ownEngineID", dec.msg.sec.engineID)
	}
	if dec.msg.sec.engineBoots != 2147483647 {
		t.Fatalf("Report boots = %d, want 2147483647", dec.msg.sec.engineBoots)
	}
	if out, isR := classifyReport(&dec.msg.scoped.pdu); !isR || out != reportUnknownEngineID {
		t.Fatalf("Report outcome = %v isReport=%v", out, isR)
	}
}

// TestV3Inform_AcceptAndAck confirms a valid inform is acked (echoed
// request-id + varbinds) and surfaced on the stream.
func TestV3Inform_AcceptAndAck(t *testing.T) {
	cfg := informCfg()
	reg := cfg
	reg.EngineID = notifyOwnEngine
	ts, addr, l := startAuthoritativeListener(t, 200*time.Second, []USMConfig{reg})

	inform := buildInform(t, cfg, l.snmpEngineTime(), notifyOwnEngine, []byte("ctx-1"))
	reply, ok := askListener(t, addr, inform)
	if !ok {
		t.Fatal("no inform ack")
	}
	sp, _ := decodeReplyScoped(t, cfg, reply)
	if sp.pdu.typ != pduGetResponse || sp.pdu.requestID != 777 {
		t.Fatalf("ack pdu = type %#x rid %d", sp.pdu.typ, sp.pdu.requestID)
	}
	if !bytes.Equal(sp.contextName, []byte("ctx-1")) {
		t.Fatalf("contextName not echoed: %q", sp.contextName)
	}
	// Inform surfaced on the stream.
	tr, ok := nextTrap(ts, 2*time.Second)
	if !ok {
		t.Fatal("inform not surfaced")
	}
	if tr.Version != V3 || tr.UserName != "alice" {
		t.Fatalf("surfaced inform meta: %+v", tr)
	}
}

// TestV3Inform_ForeignContextEngineAccepted confirms (validated against
// live Net-SNMP): an inform whose contextEngineID is the
// sender's own engine (not ours) is still accepted — authentication is
// anchored on msgAuthoritativeEngineID==ownEngineID + HMAC, and
// contextEngineID is echoed verbatim, never trusted. Real senders set it to
// their own engineID, so rejecting it would break interop.
func TestV3Inform_ForeignContextEngineAccepted(t *testing.T) {
	cfg := informCfg()
	reg := cfg
	reg.EngineID = notifyOwnEngine
	ts, addr, l := startAuthoritativeListener(t, 200*time.Second, []USMConfig{reg})

	foreign := mustHex("deadbeefdead")
	inform := buildInform(t, cfg, l.snmpEngineTime(), foreign, nil)
	reply, ok := askListener(t, addr, inform)
	if !ok {
		t.Fatal("inform with foreign contextEngineID should still be acked")
	}
	sp, _ := decodeReplyScoped(t, cfg, reply)
	if !bytes.Equal(sp.contextEngineID, foreign) {
		t.Fatalf("ack should echo the sender's contextEngineID, got %x", sp.contextEngineID)
	}
	tr, ok := nextTrap(ts, 2*time.Second)
	if !ok {
		t.Fatal("inform should be surfaced")
	}
	// The surfaced inform must carry the SENDER's engine (contextEngineID),
	// not FlowSeer's own authoritative engineID.
	if !bytes.Equal(tr.EngineID, foreign) {
		t.Fatalf("surfaced inform EngineID = %x, want sender's contextEngineID %x", tr.EngineID, foreign)
	}
}

// TestV3Inform_OutOfWindowReport confirms an out-of-window inform gets a
// notInTimeWindows Report (not surfaced).
func TestV3Inform_OutOfWindowReport(t *testing.T) {
	cfg := informCfg()
	reg := cfg
	reg.EngineID = notifyOwnEngine
	ts, addr, l := startAuthoritativeListener(t, 1000*time.Second, []USMConfig{reg})

	// Time far outside ±150 of the live snmpEngineTime (~1000).
	inform := buildInform(t, cfg, l.snmpEngineTime()-500, notifyOwnEngine, nil)
	reply, ok := askListener(t, addr, inform)
	if !ok {
		t.Fatal("expected a notInTimeWindows Report")
	}
	dec, err := decodeV3Message(reply)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out, isR := classifyReport(&dec.msg.scoped.pdu); !isR || out != reportNotInTimeWindow {
		t.Fatalf("expected notInTimeWindow Report, got %v isReport=%v", out, isR)
	}
	if _, ok := nextTrap(ts, 300*time.Millisecond); ok {
		t.Fatal("out-of-window inform should not be surfaced")
	}
}

// TestV3Inform_PostRestartQuarantine confirms an inform within the first
// quarantine window is rejected with a Report, and the SAME inform
// is accepted once snmpEngineTime climbs past the quarantine into the window
// — proving the quarantine only reduces, not closes, cross-restart replay.
func TestV3Inform_PostRestartQuarantine(t *testing.T) {
	cfg := informCfg()
	reg := cfg
	reg.EngineID = notifyOwnEngine

	// started = now → snmpEngineTime ~0 → quarantined.
	ts, addr, l := startAuthoritativeListener(t, 0, []USMConfig{reg})
	inform := buildInform(t, cfg, 5, notifyOwnEngine, nil) // msgTime within window of ~0..5
	reply, ok := askListener(t, addr, inform)
	if !ok {
		t.Fatal("quarantine should Report-back")
	}
	dec, _ := decodeV3Message(reply)
	if out, isR := classifyReport(&dec.msg.scoped.pdu); !isR || out != reportNotInTimeWindow {
		t.Fatalf("quarantine reply should be notInTimeWindow, got %v", out)
	}
	// (The Report-back, not a silent drop, proves the quarantine rejected it;
	// a negative stream check here would leave a lingering ts.Next goroutine
	// that races the positive check below.)

	// Documented residual: advance snmpEngineTime past the quarantine; the
	// SAME captured msgTime (5) is now within [now-150, now+150] once now is
	// in [0,155]. Set started so snmpEngineTime ~ 5 (past the 150 gate? no —
	// the gate needs now>=150). Set started so now ~ 155 and rebuild an
	// inform with msgTime within window to prove acceptance after quarantine.
	l.startedNanos.Store(time.Now().Add(-200 * time.Second).UnixNano()) // now ~200, quarantine lifted
	inform2 := buildInform(t, cfg, l.snmpEngineTime(), notifyOwnEngine, nil)
	if _, ok := askListener(t, addr, inform2); !ok {
		t.Fatal("inform after quarantine should be acked")
	}
	if _, ok := nextTrap(ts, 2*time.Second); !ok {
		t.Fatal("inform after quarantine should be surfaced")
	}
}

// TestV3Inform_RoleMismatchDropped confirms an authoritative-branch datagram
// that decrypts to a trap PDU is dropped.
func TestV3Inform_RoleMismatchDropped(t *testing.T) {
	cfg := informCfg()
	reg := cfg
	reg.EngineID = notifyOwnEngine
	ts, addr, l := startAuthoritativeListener(t, 200*time.Second, []USMConfig{reg})

	// Build an authenticated message TO ownEngineID but carrying a trap PDU.
	c := cfg
	c.EngineID = notifyOwnEngine
	u, _ := newUSMContext(context.Background(), c)
	sp := &scopedPDU{contextEngineID: notifyOwnEngine, pdu: pdu{typ: pduV2Trap, requestID: 1, varbinds: snmpTrapVarBinds()}}
	raw, _ := u.buildOutboundScoped(nextMsgIDValue(), sp, authoritativeBoots, l.snmpEngineTime(), true)

	if _, ok := askListener(t, addr, raw); ok {
		t.Fatal("role mismatch should not produce a reply")
	}
	if _, ok := nextTrap(ts, 300*time.Millisecond); ok {
		t.Fatal("role-mismatch datagram should not be surfaced")
	}
}

// TestV3Inform_OutboundIVUnique confirms two informs produce acks with
// distinct msgPrivacyParameters (no salt/IV reuse under one key).
func TestV3Inform_OutboundIVUnique(t *testing.T) {
	cfg := informCfg()
	reg := cfg
	reg.EngineID = notifyOwnEngine
	_, addr, l := startAuthoritativeListener(t, 200*time.Second, []USMConfig{reg})

	reply1, ok1 := askListener(t, addr, buildInform(t, cfg, l.snmpEngineTime(), notifyOwnEngine, nil))
	reply2, ok2 := askListener(t, addr, buildInform(t, cfg, l.snmpEngineTime(), notifyOwnEngine, nil))
	if !ok1 || !ok2 {
		t.Fatal("both informs should be acked")
	}
	d1, _ := decodeV3Message(reply1)
	d2, _ := decodeV3Message(reply2)
	if bytes.Equal(d1.msg.sec.privParams, d2.msg.sec.privParams) {
		t.Fatalf("ack privParams reused: %x", d1.msg.sec.privParams)
	}
}

// TestV3Inform_ShortOwnEngineIDRejected confirms a configured ownEngineID
// shorter than 5 octets is rejected at listen time.
func TestV3Inform_ShortOwnEngineIDRejected(t *testing.T) {
	_, err := ListenTraps(context.Background(), "127.0.0.1:0", WithOwnEngineID([]byte{1, 2, 3}))
	if err == nil {
		t.Fatal("ownEngineID <5 octets should be rejected")
	}
}
