package snmp

import (
	"bytes"
	"math"
	"testing"
)

// SNMPv3/USM crypto + lifecycle pins.

// Covers conformance matrix row: usm-inform-timewindow (RFC 3414 §3.2). The
// authoritative-role time window accepts an inform within ±150s of the live
// engine time (boots pinned), rejects outside it, rejects during the
// post-restart quarantine, and — critically — computes the difference in int64
// so a forged boundary engineTime cannot wrap an int32 subtraction into a
// spurious accept.
func TestInformTimeWindow(t *testing.T) {
	const now int32 = 10000 // well past the quarantine

	// In window.
	for _, etime := range []int32{now, now - 150, now + 150, now - 1, now + 1} {
		if !informTimeWindowOK(now, authoritativeBoots, etime) {
			t.Errorf("etime %d at now %d: rejected, want accepted (within ±150s)", etime, now)
		}
	}
	// Out of window.
	for _, etime := range []int32{now - 151, now + 151, 0} {
		if informTimeWindowOK(now, authoritativeBoots, etime) {
			t.Errorf("etime %d at now %d: accepted, want rejected (outside ±150s)", etime, now)
		}
	}
	// Wrong boots.
	if informTimeWindowOK(now, authoritativeBoots-1, now) {
		t.Error("non-authoritative boots accepted, want rejected")
	}
	// Post-restart quarantine.
	if informTimeWindowOK(quarantineSeconds-1, authoritativeBoots, quarantineSeconds-1) {
		t.Error("inform during quarantine accepted, want rejected")
	}
	// int32-boundary forged engineTime must NOT wrap into a spurious accept.
	// (The pre-fix int32 subtraction overflowed and wrongly accepted these.)
	for _, etime := range []int32{math.MinInt32, math.MaxInt32, math.MinInt32 + 100} {
		if informTimeWindowOK(now, authoritativeBoots, etime) {
			t.Errorf("forged boundary etime %d accepted — int32 overflow leaked", etime)
		}
	}
}

// Covers conformance matrix row: usm-authoritative-boots-pinned (RFC 3414 §3.2).
// ACCEPTED-RISK: the listener pins snmpEngineBoots to 2^31-1, so the §3.2
// boots-sequence check is permanently disabled for the authoritative role and
// the time window is the sole gate. This pin grounds the ledger entry by
// asserting the documented surface actually exists.
func TestUSM_AuthoritativeBootsPinned(t *testing.T) {
	if authoritativeBoots != math.MaxInt32 {
		t.Fatalf("authoritativeBoots = %d, want %d (2^31-1)", authoritativeBoots, int32(math.MaxInt32))
	}
	if quarantineSeconds != 150 {
		t.Fatalf("quarantineSeconds = %d, want 150 (the post-restart replay window)", quarantineSeconds)
	}
}

// Covers conformance matrix row: usm-keycache-passphrase (gosnmp #424).
// ACCEPTED-RISK: there is no passphrase-keyed key cache — keys are derived per
// (engineID, user) from the passphrase material. This pin grounds the ledger
// entry by confirming that two contexts differing ONLY in the priv passphrase
// (same auth passphrase, same engine) derive different priv keys, so a shared
// cache keyed on the auth passphrase alone cannot return a stale key.
func TestUSM_KeyDerivationPerPrivPassphrase(t *testing.T) {
	const auth = AuthSHA256
	const priv = PrivAES256
	engine := mustHex("8000000001020304050607")

	k1, err := localizedPrivKey(auth, priv, "priv-passphrase-AAAA", engine)
	if err != nil {
		t.Fatalf("localizedPrivKey 1: %v", err)
	}
	k2, err := localizedPrivKey(auth, priv, "priv-passphrase-BBBB", engine)
	if err != nil {
		t.Fatalf("localizedPrivKey 2: %v", err)
	}
	if bytes.Equal(k1, k2) {
		t.Fatal("different priv passphrases derived the same priv key — a passphrase-blind cache would be unsafe")
	}
}

// Covers conformance matrix row: usm-trap-reportable (gosnmp #391; RFC 3412
// §6.4). A v3 trap is fire-and-forget: its msgFlags reportable bit is clear, so
// a receiver never owes a Report. An inform is reportable (the sender expects an
// ack). This pins the flag the receive path keys its trap-vs-inform handling on.
func TestV3_TrapVsInformReportableFlag(t *testing.T) {
	cfg := trapCfg()

	trapRaw := buildV3Trap(t, cfg, trapSender, 5, 1000, snmpTrapVarBinds())
	tdec, err := decodeV3Message(trapRaw)
	if err != nil {
		t.Fatalf("decode trap: %v", err)
	}
	if tdec.msg.flags.reportable {
		t.Error("v3 trap has reportable flag set, want clear (traps are not reportable)")
	}

	icfg := informCfg()
	informRaw := buildInform(t, icfg, 1000, notifyOwnEngine, []byte("ctx-1"))
	idec, err := decodeV3Message(informRaw)
	if err != nil {
		t.Fatalf("decode inform: %v", err)
	}
	if !idec.msg.flags.reportable {
		t.Error("v3 inform has reportable flag clear, want set (informs are reportable)")
	}
}
