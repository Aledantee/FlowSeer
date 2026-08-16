package snmp

import (
	"bytes"
	"context"
	"sync"
	"testing"
)

func engCfg(engineID []byte, user, authPass string) USMConfig {
	return USMConfig{
		Username:       user,
		EngineID:       engineID,
		AuthProtocol:   AuthSHA256,
		AuthPassphrase: authPass,
		PrivProtocol:   PrivAES,
		PrivPassphrase: "priv-passphrase-1234",
	}
}

var (
	engA = mustHex("80001f8800aaaaaaaaaaaa01")
	engB = mustHex("80001f8800bbbbbbbbbbbb02")
)

// TestEngineTable_DirectLookup confirms a registered (engineID, userName)
// resolves and a miss is a clean "no engine" (never a trial-decrypt
// fallback).
func TestEngineTable_DirectLookup(t *testing.T) {
	tbl := newEngineTable(context.Background())
	if err := tbl.register(engCfg(engA, "alice", "auth-passphrase-1234")); err != nil {
		t.Fatalf("register: %v", err)
	}
	u, base, ok := tbl.lookup(engA, "alice")
	if !ok || u == nil || base == nil {
		t.Fatalf("lookup miss for registered engine")
	}
	if !u.hasEngine() {
		t.Fatalf("credential has no engine keys")
	}
	if _, _, ok := tbl.lookup(engA, "bob"); ok {
		t.Fatalf("unknown user should miss")
	}
	if _, _, ok := tbl.lookup(engB, "alice"); ok {
		t.Fatalf("unknown engine should miss")
	}
}

// TestEngineTable_ReplacePreservesBaseline confirms re-registering the same
// (engineID, userName) with a rotated passphrase replaces the keys but
// preserves the §3.2 baseline.
func TestEngineTable_ReplacePreservesBaseline(t *testing.T) {
	tbl := newEngineTable(context.Background())
	if err := tbl.register(engCfg(engA, "alice", "auth-passphrase-1234")); err != nil {
		t.Fatalf("register: %v", err)
	}
	_, base1, _ := tbl.lookup(engA, "alice")
	// Learn a baseline.
	if !base1.checkAndUpdate(5, 1000) {
		t.Fatalf("first contact should be accepted")
	}

	u1, _, _ := tbl.lookup(engA, "alice")
	if err := tbl.register(engCfg(engA, "alice", "rotated-passphrase-9999")); err != nil {
		t.Fatalf("re-register: %v", err)
	}
	u2, base2, _ := tbl.lookup(engA, "alice")
	if bytes.Equal(u1.loadAuthKey(), u2.loadAuthKey()) {
		t.Fatalf("rotated passphrase should change the key")
	}
	if base1 != base2 {
		t.Fatalf("baseline should persist across credential replace")
	}
	// A replayed (older) message is still rejected by the preserved baseline.
	if base2.checkAndUpdate(4, 1000) {
		t.Fatalf("regressed boots should be rejected by preserved baseline")
	}
}

// TestEngineTable_MultiUserCoexist confirms the same EngineID with two
// different userNames coexist (composite-key granularity), and two engines
// with the same userName resolve independently.
func TestEngineTable_MultiUserCoexist(t *testing.T) {
	tbl := newEngineTable(context.Background())
	for _, c := range []USMConfig{
		engCfg(engA, "alice", "auth-passphrase-1234"),
		engCfg(engA, "bob", "auth-passphrase-5678"),
		engCfg(engB, "alice", "auth-passphrase-abcd"),
	} {
		if err := tbl.register(c); err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	uAalice, _, _ := tbl.lookup(engA, "alice")
	uAbob, _, okBob := tbl.lookup(engA, "bob")
	uBalice, _, okB := tbl.lookup(engB, "alice")
	if !okBob || !okB {
		t.Fatalf("expected all three to resolve")
	}
	if bytes.Equal(uAalice.loadAuthKey(), uAbob.loadAuthKey()) {
		t.Fatalf("different users on one engine should have different keys")
	}
	if bytes.Equal(uAalice.loadAuthKey(), uBalice.loadAuthKey()) {
		t.Fatalf("same user on different engines should have different keys")
	}
}

// TestEngineTable_RequiresEngineID rejects a registration without an
// EngineID.
func TestEngineTable_RequiresEngineID(t *testing.T) {
	tbl := newEngineTable(context.Background())
	cfg := engCfg(nil, "alice", "auth-passphrase-1234")
	if err := tbl.register(cfg); err == nil {
		t.Fatalf("registration without EngineID should error")
	}
}

// TestEngineTable_ConcurrentRegisterLookup exercises the RWMutex: lookups
// during concurrent registers see a consistent snapshot (run under -race).
func TestEngineTable_ConcurrentRegisterLookup(_ *testing.T) {
	tbl := newEngineTable(context.Background())
	_ = tbl.register(engCfg(engA, "alice", "auth-passphrase-1234"))
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				_ = tbl.register(engCfg(engA, "alice", "auth-passphrase-1234"))
			} else {
				tbl.lookup(engA, "alice")
			}
		}(i)
	}
	wg.Wait()
}

// TestRecvBaseline_TimeWindow exercises the §3.2 non-authoritative checks.
func TestRecvBaseline_TimeWindow(t *testing.T) {
	b := &recvBaseline{}
	if !b.checkAndUpdate(5, 1000) {
		t.Fatalf("first contact accept-and-learn")
	}
	if b.checkAndUpdate(4, 1000) {
		t.Fatalf("regressed boots must reject")
	}
	if b.checkAndUpdate(5, 800) {
		t.Fatalf("time >150s behind latest must reject")
	}
	if !b.checkAndUpdate(5, 1100) {
		t.Fatalf("future time must be accepted")
	}
	if !b.checkAndUpdate(6, 10) {
		t.Fatalf("boots increase (reboot) must reset+accept")
	}
}
