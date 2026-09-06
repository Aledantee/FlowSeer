package snmp

import (
	"bytes"
	"context"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/secret"
)

// buildV3Trap builds an authenticated (and optionally encrypted) SNMPv3
// SNMPv2-Trap-PDU as the authoritative sender engineID would, for delivery
// to a listener that has the (engineID, userName) registered.
func buildV3Trap(t *testing.T, cfg USMConfig, engineID []byte, boots, etime int32, vbs []VarBind) []byte {
	t.Helper()
	c := cfg
	c.EngineID = engineID
	u, err := newUSMContext(context.Background(), c)
	if err != nil {
		t.Fatalf("sender usm: %v", err)
	}
	p := pdu{typ: pduV2Trap, requestID: 1, varbinds: vbs}
	raw, err := u.buildOutbound(nextMsgIDValue(), p, boots, etime, false)
	if err != nil {
		t.Fatalf("buildOutbound trap: %v", err)
	}
	return raw
}

var trapSender = mustHex("80001f8800c0ffee1234567890")

func trapCfg() USMConfig {
	return USMConfig{
		Username:       "alice",
		AuthProtocol:   AuthSHA256,
		AuthPassphrase: secret.NewString("trap-auth-passphrase"),
		PrivProtocol:   PrivAES,
		PrivPassphrase: secret.NewString("trap-priv-passphrase"),
	}
}

// TestV3Trap_AuthPrivReceive confirms a valid authPriv trap from a
// registered engine is emitted with EngineID/UserName and canonical
// varbinds.
func TestV3Trap_AuthPrivReceive(t *testing.T) {
	cfg := trapCfg()
	reg := cfg
	reg.EngineID = trapSender
	ts, addr := startTrapListener(t, WithUSMTable([]USMConfig{reg}))

	vbs := snmpTrapVarBinds()
	sendDatagram(t, addr, buildV3Trap(t, cfg, trapSender, 5, 1000, vbs))

	tr, ok := nextTrap(ts, 2*time.Second)
	if !ok {
		t.Fatal("v3 trap not received")
	}
	if tr.Version != V3 {
		t.Fatalf("version = %s, want v3", tr.Version)
	}
	if !bytes.Equal(tr.EngineID, trapSender) || tr.UserName != "alice" {
		t.Fatalf("trap identity: engineID=%x user=%q", tr.EngineID, tr.UserName)
	}
	if len(tr.VarBinds) != 2 {
		t.Fatalf("got %d varbinds, want 2", len(tr.VarBinds))
	}
}

// TestV3Trap_ReplayRejected confirms a trap with regressed boots, or equal
// boots and a far-behind time, is dropped after the baseline is learned.
func TestV3Trap_ReplayRejected(t *testing.T) {
	cfg := trapCfg()
	reg := cfg
	reg.EngineID = trapSender
	ts, addr := startTrapListener(t, WithUSMTable([]USMConfig{reg}))

	// First contact learns boots=5, time=1000.
	sendDatagram(t, addr, buildV3Trap(t, cfg, trapSender, 5, 1000, snmpTrapVarBinds()))
	if _, ok := nextTrap(ts, 2*time.Second); !ok {
		t.Fatal("first trap not received")
	}
	before := ts.Dropped()

	// Regressed boots → dropped.
	sendDatagram(t, addr, buildV3Trap(t, cfg, trapSender, 4, 1000, snmpTrapVarBinds()))
	// Equal boots, time >150s behind → dropped.
	sendDatagram(t, addr, buildV3Trap(t, cfg, trapSender, 5, 800, snmpTrapVarBinds()))

	if _, ok := nextTrap(ts, 300*time.Millisecond); ok {
		t.Fatal("replayed trap should not be surfaced")
	}
	if ts.Dropped() < before+2 {
		t.Fatalf("replays not counted: before=%d after=%d", before, ts.Dropped())
	}
}

// TestV3Trap_FutureTimeAccepted confirms a trap with time ahead of the
// stored baseline is accepted.
func TestV3Trap_FutureTimeAccepted(t *testing.T) {
	cfg := trapCfg()
	reg := cfg
	reg.EngineID = trapSender
	ts, addr := startTrapListener(t, WithUSMTable([]USMConfig{reg}))

	sendDatagram(t, addr, buildV3Trap(t, cfg, trapSender, 5, 1000, snmpTrapVarBinds()))
	if _, ok := nextTrap(ts, 2*time.Second); !ok {
		t.Fatal("first trap not received")
	}
	sendDatagram(t, addr, buildV3Trap(t, cfg, trapSender, 5, 5000, snmpTrapVarBinds()))
	if _, ok := nextTrap(ts, 2*time.Second); !ok {
		t.Fatal("future-time trap should be accepted")
	}
}

// TestV3Trap_WrongDigestDropped confirms a tampered MAC is dropped+counted.
func TestV3Trap_WrongDigestDropped(t *testing.T) {
	cfg := trapCfg()
	reg := cfg
	reg.EngineID = trapSender
	ts, addr := startTrapListener(t, WithUSMTable([]USMConfig{reg}))

	raw := buildV3Trap(t, cfg, trapSender, 5, 1000, snmpTrapVarBinds())
	// Corrupt a byte near the end (inside the ciphertext / mac region).
	raw[len(raw)-1] ^= 0xff
	before := ts.Dropped()
	sendDatagram(t, addr, raw)
	if _, ok := nextTrap(ts, 300*time.Millisecond); ok {
		t.Fatal("tampered trap should not be surfaced")
	}
	if ts.Dropped() <= before {
		t.Fatalf("tampered trap not counted as dropped")
	}
}

// TestV3Trap_SecurityLevelFloor confirms a sender registered at authPriv
// that sends an authNoPriv trap is rejected (security review #5).
func TestV3Trap_SecurityLevelFloor(t *testing.T) {
	reg := trapCfg() // authPriv
	reg.EngineID = trapSender
	ts, addr := startTrapListener(t, WithUSMTable([]USMConfig{reg}))

	// authNoPriv sender (same engineID + userName + auth passphrase).
	weak := USMConfig{Username: "alice", AuthProtocol: AuthSHA256, AuthPassphrase: secret.NewString("trap-auth-passphrase")}
	before := ts.Dropped()
	sendDatagram(t, addr, buildV3Trap(t, weak, trapSender, 5, 1000, snmpTrapVarBinds()))
	if _, ok := nextTrap(ts, 300*time.Millisecond); ok {
		t.Fatal("authNoPriv trap from authPriv-registered engine should be rejected")
	}
	if ts.Dropped() <= before {
		t.Fatalf("downgraded trap not counted")
	}
}

// TestV3Trap_UnregisteredEngineDropped confirms an unknown engine drops
// cleanly (no trial-decryption).
func TestV3Trap_UnregisteredEngineDropped(t *testing.T) {
	ts, addr := startTrapListener(t) // empty USM table
	cfg := trapCfg()
	before := ts.Dropped()
	sendDatagram(t, addr, buildV3Trap(t, cfg, trapSender, 5, 1000, snmpTrapVarBinds()))
	if _, ok := nextTrap(ts, 300*time.Millisecond); ok {
		t.Fatal("trap from unregistered engine should be dropped")
	}
	if ts.Dropped() <= before {
		t.Fatalf("unregistered-engine trap not counted")
	}
}

// TestV3Trap_MultiEngine confirms two engines with the same userName resolve
// and time-check independently.
func TestV3Trap_MultiEngine(t *testing.T) {
	engX := mustHex("80001f8800aaaa0000000001")
	engY := mustHex("80001f8800bbbb0000000002")
	cfg := trapCfg()
	regX, regY := cfg, cfg
	regX.EngineID = engX
	regY.EngineID = engY
	ts, addr := startTrapListener(t, WithUSMTable([]USMConfig{regX, regY}))

	sendDatagram(t, addr, buildV3Trap(t, cfg, engX, 5, 1000, snmpTrapVarBinds()))
	sendDatagram(t, addr, buildV3Trap(t, cfg, engY, 9, 2000, snmpTrapVarBinds()))

	got := map[string]bool{}
	for range 2 {
		tr, ok := nextTrap(ts, 2*time.Second)
		if !ok {
			t.Fatal("expected two traps")
		}
		got[string(tr.EngineID)] = true
	}
	if !got[string(engX)] || !got[string(engY)] {
		t.Fatalf("both engines should resolve independently: %v", got)
	}
}
