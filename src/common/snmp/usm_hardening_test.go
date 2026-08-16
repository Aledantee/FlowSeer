package snmp

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// SNMPv3/USM security + reliability hardening tests.

// Covers conformance matrix row: usm-timewindow-rollback (RFC 3414 §2.2.3). A
// mid-session resync must not roll the authoritative baseline backward: a
// replayed/forged Report with lower engineBoots, or backward engineTime at the
// same boots, is rejected so a stale window cannot self-DoS the session. A
// genuine reboot (higher boots, time reset) is accepted.
func TestEngineBaseline_RollbackRejected(t *testing.T) {
	b := &engineBaseline{}
	b.learn(5, 1000) // initial discovery

	if !b.update(5, 1200) {
		t.Fatal("forward time at same boots was rejected, want accepted")
	}
	if b.update(5, 1100) {
		t.Fatal("backward time at same boots was accepted (rollback)")
	}
	if b.update(4, 9999) {
		t.Fatal("decreasing boots was accepted (rollback)")
	}
	if !b.update(6, 1) {
		t.Fatal("genuine reboot (higher boots, low time) was rejected")
	}
	gotBoots, _, ok := b.snapshot()
	if !ok || gotBoots != 6 {
		t.Fatalf("baseline boots = %d (ok=%v), want 6 — only the accepted updates apply", gotBoots, ok)
	}
}

// Covers conformance matrix row: usm-msgid-predictability (RFC 3412). v3
// msgIDs are drawn fresh from crypto/rand, not a +1 counter, so observing one
// does not predict the next during the unauthenticated discovery window. A
// sequential generator would make nearly every id == previous+1; CSPRNG makes
// that essentially never. Also pins the 31-bit non-negative range.
func TestMsgID_FreshNotSequential(t *testing.T) {
	const n = 128
	consecutive := 0
	prev := nextMsgIDValue()
	if prev < 0 {
		t.Fatalf("msgID negative: %d", prev)
	}
	for i := 1; i < n; i++ {
		cur := nextMsgIDValue()
		if cur < 0 {
			t.Fatalf("msgID negative: %d", cur)
		}
		if cur == prev+1 {
			consecutive++
		}
		prev = cur
	}
	// A +1 counter yields n-1 consecutive pairs; CSPRNG should yield ~0. Allow a
	// tiny slack for the astronomically unlikely random adjacency.
	if consecutive > 2 {
		t.Fatalf("%d/%d msgIDs were previous+1 — generator looks sequential, not CSPRNG", consecutive, n-1)
	}
}

// Covers conformance matrix row: usm-authbit-bypass (gosnmp #496). The third
// downgrade-matrix cell: a reply with the Auth bit cleared arriving at an
// authPriv session is rejected before any HMAC/decrypt work — the cleared flag
// cannot bypass verification. (The auth-cleared-at-authNoPriv and
// priv-cleared-at-authPriv cells are pinned by TestUSM_DowngradeUnauthenticated
// and TestUSM_DowngradePlaintext.)
func TestUSM_DowngradeAuthClearedAtAuthPriv(t *testing.T) {
	send := mustContext(t, AuthProtocolNone, PrivProtocolNone) // no auth bit set
	recv := mustContext(t, AuthSHA256, PrivAES256)             // authPriv expected
	raw, err := send.buildOutbound(7, sampleResponsePDU(), 3, 600, false)
	if err != nil {
		t.Fatalf("buildOutbound: %v", err)
	}
	dec, err := decodeV3Message(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, err := recv.verifyInbound(dec); !errors.Is(err, ErrUSMDowngrade) {
		t.Fatalf("auth-cleared reply at authPriv: err = %v, want ErrUSMDowngrade", err)
	}
}

// Covers conformance matrix row: usm-authbit-bypass (RFC 3414 §3.2) — allowance
// arm. The inverse of the downgrade matrix: an AUTHENTICATED authNoPriv Report
// is legitimately accepted on an authPriv session (time-sync Reports are
// authNoPriv even for an authPriv request). Pinning this prevents the
// downgrade/Report-disposition work from accidentally tightening the rule and
// breaking resync.
func TestUSM_InboundGateAllowsAuthNoPrivReport(t *testing.T) {
	send := mustContext(t, AuthSHA256, PrivProtocolNone) // authNoPriv sender
	recv := mustContext(t, AuthSHA256, PrivAES256)       // authPriv session
	rep := pdu{typ: pduReport, requestID: 0, varbinds: []VarBind{usmStatsVB(2)}}
	raw, err := send.buildOutbound(7, rep, 3, 600, false)
	if err != nil {
		t.Fatalf("buildOutbound: %v", err)
	}
	dec, err := decodeV3Message(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	sp, isReport, err := recv.inboundGate(dec)
	if err != nil {
		t.Fatalf("inboundGate rejected an authenticated authNoPriv Report on an authPriv session: %v", err)
	}
	if !isReport || sp == nil {
		t.Fatalf("expected isReport=true with a scoped PDU, got isReport=%v sp=%v", isReport, sp)
	}
}

// Covers conformance matrix row: usm-report-randomid (gosnmp #139). An
// unsolicited Report bearing a msgID that matches no in-flight request is
// dropped by the demux (drop-and-count) — it must not abort the in-flight op or
// hang it. Here the agent injects a mismatched-msgID Report just before the
// genuine reply; the round-trip still resolves cleanly.
func TestReactorV3_MismatchedReportDropped(t *testing.T) {
	cfg := baseCfg()
	ccfg := cfg
	ccfg.EngineID = v3TestEngine // skip discovery: configured engine

	var agent *v3Agent
	injected := false
	before := func(dec *v3Decoded, src *net.UDPAddr) []byte {
		if !dec.msg.flags.auth {
			return nil
		}
		if !injected {
			injected = true
			// Unsolicited authNoPriv Report with a msgID that is NOT the
			// in-flight request's — must be dropped, not delivered.
			bogus := &v3Message{
				msgID:         dec.msg.msgID ^ 0x004d2d2d, // differs from the live msgID
				msgMaxSize:    v3MaxMessageSize,
				securityModel: securityModelUSM,
				flags:         msgFlags{auth: true},
				sec:           usmSecurityParameters{engineID: v3TestEngine, engineBoots: 9, engineTime: 5000, userName: cfg.Username},
				scoped: &scopedPDU{
					contextEngineID: v3TestEngine,
					pdu:             pdu{typ: pduReport, requestID: 0, varbinds: []VarBind{usmStatsVB(2)}},
				},
			}
			if raw, err := agent.usm.signMessage(bogus, agent.usm.loadAuthKey()); err == nil {
				_, _ = agent.conn.WriteToUDP(raw, src)
			}
		}
		return nil // fall through to the genuine data reply
	}

	agent = startV3Agent(t, cfg, v3TestEngine, 9, 5000, before)
	r := newV3Reactor(t, agent.addr, ccfg)

	res, err := r.v3RoundTrip(context.Background(), getReqPDU(), time.Second, 2)
	if err != nil {
		t.Fatalf("v3RoundTrip err = %v — a mismatched-msgID Report must not abort the op", err)
	}
	if res.isReport || res.scoped == nil {
		t.Fatalf("expected the genuine data reply, got %+v", res)
	}
}
