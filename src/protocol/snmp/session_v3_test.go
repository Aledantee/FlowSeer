package snmp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"testing"

	"go.aledante.io/FlowSeer/src/common/secret"
)

// dialV3 dials a native v3 session at the agent's address with the given USM
// config and options.
func dialV3(t *testing.T, agent *v3Agent, cfg USMConfig, extra ...Option) (Session, error) {
	t.Helper()
	target := fmt.Sprintf("127.0.0.1:%d", agent.addr.Port)
	opts := append([]Option{WithUSM(cfg)}, extra...)
	return NewSession(context.Background(), target, V3, opts...)
}

// TestV3Session_DialAndGet confirms NewSession(V3+USM) returns a working session
// (no ErrV3Unsupported) and a Get succeeds over discovery + authPriv.
func TestV3Session_DialAndGet(t *testing.T) {
	agent := startV3Agent(t, baseCfg(), v3TestEngine, 3, 1000, nil)
	sess, err := dialV3(t, agent, baseCfg(), WithMinSecurity(MinSecurityAuthPriv))
	if err != nil {
		t.Fatalf("Dial v3: %v", err)
	}
	defer func() { _ = sess.Close() }()

	oid := MustOID(1, 3, 6, 1, 2, 1, 1, 1, 0)
	vbs, err := sess.Get(context.Background(), []OID{oid})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(vbs) != 1 || !vbs[0].GetHeader().OID.Equal(oid) {
		t.Fatalf("unexpected varbinds: %+v", vbs)
	}
}

// TestV3Session_SimpleOps runs Get, GetNext, and Set over v3 and checks each
// returns the expected per-OID varbinds (same shape as v1/v2c).
func TestV3Session_SimpleOps(t *testing.T) {
	agent := startV3Agent(t, baseCfg(), v3TestEngine, 3, 1000, nil)
	sess, err := dialV3(t, agent, baseCfg())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = sess.Close() }()
	ctx := context.Background()
	oid := MustOID(1, 3, 6, 1, 2, 1, 1, 5, 0)

	if vbs, err := sess.Get(ctx, []OID{oid}); err != nil || len(vbs) != 1 {
		t.Fatalf("Get: vbs=%v err=%v", vbs, err)
	}
	if vbs, err := sess.GetNext(ctx, []OID{oid}); err != nil || len(vbs) != 1 {
		t.Fatalf("GetNext: vbs=%v err=%v", vbs, err)
	}
	setVB := OctetStringVar{Header: Header{OID: oid, Kind: KindOctetString}, Value: []byte("x")}
	if vbs, err := sess.Set(ctx, []VarBind{setVB}); err != nil || len(vbs) != 1 {
		t.Fatalf("Set: vbs=%v err=%v", vbs, err)
	}
}

// TestV3Session_GetBulkHalving confirms a tooBig response halves and retries
// over the v3 path, just as it does for v2c.
func TestV3Session_GetBulkHalving(t *testing.T) {
	var first atomic.Bool
	first.Store(true)
	var agent *v3Agent
	before := func(dec *v3Decoded, _ *net.UDPAddr) []byte {
		if !dec.msg.flags.auth {
			return nil // discovery
		}
		sp, err := agent.usm.verifyInbound(dec)
		if err != nil {
			return nil
		}
		if sp.pdu.typ == pduGetBulkRequest && first.Load() {
			first.Store(false)
			// Reply tooBig so the session halves maxRepetitions and retries.
			resp := pdu{typ: pduGetResponse, requestID: sp.pdu.requestID, errorStatus: TooBig}
			raw, _ := agent.usm.buildOutbound(dec.msg.msgID, resp, agent.boots, agent.etime, false)
			return raw
		}
		return nil // second bulk → normal echo reply
	}
	agent = startV3Agent(t, baseCfg(), v3TestEngine, 3, 1000, before)
	sess, err := dialV3(t, agent, baseCfg())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = sess.Close() }()

	oid := MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 1)
	vbs, err := sess.GetBulk(context.Background(), 0, 10, []OID{oid})
	if err != nil {
		t.Fatalf("GetBulk: %v", err)
	}
	if len(vbs) == 0 {
		t.Fatalf("expected varbinds after halve-retry")
	}
	if first.Load() {
		t.Fatalf("tooBig path not exercised")
	}
}

// TestV3Session_MinSecurityFloor rejects an authNoPriv config under a
// WithMinSecurity(authPriv) floor.
func TestV3Session_MinSecurityFloor(t *testing.T) {
	cfg := USMConfig{Username: "alice", AuthProtocol: AuthSHA256, AuthPassphrase: secret.NewString("auth-passphrase-1234")}
	_, err := NewSession(context.Background(), "127.0.0.1:16100", V3, WithUSM(cfg), WithMinSecurity(MinSecurityAuthPriv))
	if !errors.Is(err, ErrSecurityPolicy) {
		t.Fatalf("expected ErrSecurityPolicy, got %v", err)
	}
}

// TestV3Session_InvalidUSMRejected confirms a structurally invalid USM
// config (priv without auth) is rejected at Dial via the existing
// Validate, without a new idiom.
func TestV3Session_InvalidUSMRejected(t *testing.T) {
	cfg := USMConfig{Username: "alice", PrivProtocol: PrivAES, PrivPassphrase: secret.NewString("p")}
	_, err := NewSession(context.Background(), "127.0.0.1:16100", V3, WithUSM(cfg))
	if err == nil {
		t.Fatalf("priv-without-auth config should be rejected at Dial")
	}
}
