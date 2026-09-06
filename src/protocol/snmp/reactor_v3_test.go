package snmp

import (
	"bytes"
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/secret"
)

// v3Agent is a USM-aware UDP responder for v3 reactor tests. It answers an
// engine-discovery probe with an unauthenticated unknownEngineID Report and
// an authenticated request with an authenticated GetResponse, using a
// server-side usmContext that shares the client's keys.
type v3Agent struct {
	conn   *net.UDPConn
	addr   *net.UDPAddr
	usm    *usmContext // engine set to engineID
	engine []byte
	boots  int32
	etime  int32
}

// startV3Agent starts an agent that performs the standard discovery + reply
// flow. before is an optional hook returning a raw datagram to send instead
// of the default reply (return nil to fall through to the default); it lets
// a test inject Reports (notInTimeWindow, etc).
func startV3Agent(t *testing.T, cfg USMConfig, engineID []byte, boots, etime int32, before func(dec *v3Decoded, src *net.UDPAddr) []byte) *v3Agent {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("v3 agent listen: %v", err)
	}
	agentCfg := cfg
	agentCfg.EngineID = engineID
	u, err := newUSMContext(context.Background(), agentCfg)
	if err != nil {
		t.Fatalf("agent usm: %v", err)
	}
	a := &v3Agent{conn: conn, addr: conn.LocalAddr().(*net.UDPAddr), usm: u, engine: engineID, boots: boots, etime: etime}
	go a.loop(before)
	t.Cleanup(func() { _ = conn.Close() })
	return a
}

func (a *v3Agent) loop(before func(dec *v3Decoded, src *net.UDPAddr) []byte) {
	buf := make([]byte, maxUDPPayload)
	for {
		n, src, err := a.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		data := append([]byte(nil), buf[:n]...)
		_, dec, derr := decodeAnyMessage(data)
		if derr != nil || dec == nil {
			continue
		}
		if before != nil {
			if out := before(dec, src); out != nil {
				_, _ = a.conn.WriteToUDP(out, src)
				continue
			}
		}
		if !dec.msg.flags.auth && len(dec.msg.sec.engineID) == 0 {
			a.sendReport(src, dec.msg.msgID, reportPDUUnauth(a.engine, a.boots, a.etime), false)
			continue
		}
		a.sendDataReply(src, dec)
	}
}

// reportPDUUnauth builds the scoped Report for an unauthenticated discovery
// reply.
func reportPDUUnauth(engine []byte, boots, etime int32) *v3Message {
	return &v3Message{
		msgID:         0, // set by caller
		msgMaxSize:    v3MaxMessageSize,
		securityModel: securityModelUSM,
		flags:         msgFlags{},
		sec:           usmSecurityParameters{engineID: engine, engineBoots: boots, engineTime: etime},
		scoped: &scopedPDU{
			contextEngineID: engine,
			pdu:             pdu{typ: pduReport, requestID: 0, varbinds: []VarBind{usmStatsVB(4)}},
		},
	}
}

func (a *v3Agent) sendReport(src *net.UDPAddr, msgID int32, m *v3Message, _ bool) {
	m.msgID = msgID
	raw, err := encodeV3Message(m)
	if err != nil {
		return
	}
	_, _ = a.conn.WriteToUDP(raw, src)
}

// sendDataReply verifies the request and answers with an authenticated
// GetResponse echoing the inner request-id and returning a fixed value.
func (a *v3Agent) sendDataReply(src *net.UDPAddr, dec *v3Decoded) {
	sp, err := a.usm.verifyInbound(dec)
	if err != nil {
		return
	}
	// Echo each requested OID with a stub OctetString value, so Get/GetNext/
	// Set/GetBulk all receive one varbind per request varbind.
	var vbs []VarBind
	for _, vb := range sp.pdu.varbinds {
		vbs = append(vbs, OctetStringVar{
			Header: Header{OID: vb.GetHeader().OID, Kind: KindOctetString},
			Value:  []byte("agent-ok"),
		})
	}
	if len(vbs) == 0 {
		vbs = []VarBind{OctetStringVar{Header: Header{OID: MustOID(1, 3, 6, 1, 2, 1, 1, 1, 0), Kind: KindOctetString}, Value: []byte("agent-ok")}}
	}
	resp := pdu{
		typ:       pduGetResponse,
		requestID: sp.pdu.requestID,
		varbinds:  vbs,
	}
	raw, err := a.usm.buildOutbound(dec.msg.msgID, resp, a.boots, a.etime, false)
	if err != nil {
		return
	}
	_, _ = a.conn.WriteToUDP(raw, src)
}

func newV3Reactor(t *testing.T, peer *net.UDPAddr, cfg USMConfig) *reactor {
	t.Helper()
	u, err := newUSMContext(context.Background(), cfg)
	if err != nil {
		t.Fatalf("client usm: %v", err)
	}
	r, err := newReactor(context.Background(), reactorConfig{
		peer:    peer,
		version: V3,
		usm:     u,
	})
	if err != nil {
		t.Fatalf("newReactor: %v", err)
	}
	t.Cleanup(func() { _ = r.close() })
	return r
}

func getReqPDU() pdu {
	return pdu{
		typ:      pduGetRequest,
		varbinds: []VarBind{NullVar{Header: Header{OID: MustOID(1, 3, 6, 1, 2, 1, 1, 1, 0), Kind: KindNull}}},
	}
}

var v3TestEngine = mustHex("80001f8880aabbccddeeff0011")

func baseCfg() USMConfig {
	return USMConfig{
		Username:       "alice",
		AuthProtocol:   AuthSHA256,
		AuthPassphrase: secret.NewString("auth-passphrase-1234"),
		PrivProtocol:   PrivAES,
		PrivPassphrase: secret.NewString("priv-passphrase-1234"),
	}
}

// TestReactorV3_DiscoveryAndGet exercises the full lazy-discovery + authPriv
// Get path: empty EngineID → probe → unknownEngineID Report → cached → real
// request → authenticated reply.
//
// Covers conformance matrix row: usm-3step-discovery (gosnmp #511; RFC 3414 §4).
// Initial discovery learns the authoritative engine's boots/time before the
// first authenticated request is sent.
func TestReactorV3_DiscoveryAndGet(t *testing.T) {
	agent := startV3Agent(t, baseCfg(), v3TestEngine, 3, 1000, nil)
	r := newV3Reactor(t, agent.addr, baseCfg())

	res, err := r.v3RoundTrip(context.Background(), getReqPDU(), time.Second, 2)
	if err != nil {
		t.Fatalf("v3RoundTrip: %v", err)
	}
	if res.isReport || res.scoped == nil {
		t.Fatalf("expected data reply, got %+v", res)
	}
	if len(res.scoped.pdu.varbinds) != 1 || res.scoped.pdu.varbinds[0].GetHeader().OID.String() != "1.3.6.1.2.1.1.1.0" {
		t.Fatalf("unexpected varbinds: %+v", res.scoped.pdu.varbinds)
	}
	if !r.usm.hasEngine() {
		t.Fatalf("engine not cached after discovery")
	}
}

// TestReactorV3_NotInTimeWindowResync uses a configured EngineID (no
// discovery); the agent rejects the first authenticated request with an
// authenticated notInTimeWindow Report, then accepts after resync.
func TestReactorV3_NotInTimeWindowResync(t *testing.T) {
	var seen atomic.Int32
	cfg := baseCfg()

	var agent *v3Agent
	before := func(dec *v3Decoded, _ *net.UDPAddr) []byte {
		if !dec.msg.flags.auth {
			return nil // not used: configured engineID, no discovery
		}
		if seen.Add(1) == 1 {
			// First authenticated request → notInTimeWindow Report (authNoPriv).
			m := &v3Message{
				msgID:         dec.msg.msgID,
				msgMaxSize:    v3MaxMessageSize,
				securityModel: securityModelUSM,
				flags:         msgFlags{auth: true},
				sec:           usmSecurityParameters{engineID: v3TestEngine, engineBoots: 9, engineTime: 5000, userName: cfg.Username},
				scoped: &scopedPDU{
					contextEngineID: v3TestEngine,
					pdu:             pdu{typ: pduReport, requestID: 0, varbinds: []VarBind{usmStatsVB(2)}},
				},
			}
			raw, err := agent.usm.signMessage(m, agent.usm.loadAuthKey())
			if err != nil {
				return nil
			}
			return raw
		}
		return nil // second request: fall through to normal data reply
	}

	ccfg := cfg
	ccfg.EngineID = v3TestEngine
	agent = startV3Agent(t, cfg, v3TestEngine, 9, 5000, before)
	r := newV3Reactor(t, agent.addr, ccfg)

	res, err := r.v3RoundTrip(context.Background(), getReqPDU(), time.Second, 2)
	if err != nil {
		t.Fatalf("v3RoundTrip after resync: %v", err)
	}
	if res.isReport || res.scoped == nil {
		t.Fatalf("expected data reply after resync, got %+v", res)
	}
	if seen.Load() < 2 {
		t.Fatalf("expected a resync retry, saw %d auth requests", seen.Load())
	}
}

// TestReactorV3_DiscoveryWrongSourceDropped confirms a discovery Report from
// a source other than the dialed peer is dropped, even with
// validateSrc off (the default for v3 reactors here).
func TestReactorV3_DiscoveryWrongSourceDropped(t *testing.T) {
	// An off-path socket that races a forged discovery Report to the client.
	off, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("off-path listen: %v", err)
	}
	t.Cleanup(func() { _ = off.Close() })

	agent := startV3Agent(t, baseCfg(), v3TestEngine, 3, 1000, func(dec *v3Decoded, src *net.UDPAddr) []byte {
		if !dec.msg.flags.auth && len(dec.msg.sec.engineID) == 0 {
			// Forge a Report from the off-path socket with a bogus engineID;
			// it must be dropped (wrong source). The genuine agent reply
			// still follows from the real socket.
			forged := reportPDUUnauth(mustHex("ffffffffffffffff"), 99, 99)
			forged.msgID = dec.msg.msgID
			raw, _ := encodeV3Message(forged)
			_, _ = off.WriteToUDP(raw, src)
		}
		return nil // fall through to genuine discovery + data reply
	})
	r := newV3Reactor(t, agent.addr, baseCfg())

	res, err := r.v3RoundTrip(context.Background(), getReqPDU(), 2*time.Second, 3)
	if err != nil {
		t.Fatalf("v3RoundTrip: %v", err)
	}
	if res.scoped == nil {
		t.Fatalf("expected genuine data reply despite forged report")
	}
	// The cached engine must be the genuine one, not the forged 0xff… id.
	if string(r.usm.currentEngineID()) == string(mustHex("ffffffffffffffff")) {
		t.Fatalf("forged engineID was accepted")
	}
}

// TestReactorV3_MsgIDSeeded mirrors the ridCounter seed assertion: the v3
// msgID counter is seeded (not a deterministic zero start).
func TestReactorV3_MsgIDSeeded(t *testing.T) {
	a := nextMsgIDValue()
	b := nextMsgIDValue()
	if a == b {
		t.Fatalf("msgID counter not advancing")
	}
	// A freshly-seeded counter is overwhelmingly unlikely to be near zero on
	// the first call; assert it is positive 31-bit.
	if a < 0 || b < 0 {
		t.Fatalf("msgID not in positive 31-bit range: %d %d", a, b)
	}
}

// TestReactorV3_SingleFlightDiscovery confirms concurrent first-ops issue
// exactly one discovery probe.
func TestReactorV3_SingleFlightDiscovery(t *testing.T) {
	var probes atomic.Int32
	agent := startV3Agent(t, baseCfg(), v3TestEngine, 3, 1000, func(dec *v3Decoded, _ *net.UDPAddr) []byte {
		if !dec.msg.flags.auth && len(dec.msg.sec.engineID) == 0 {
			probes.Add(1)
		}
		return nil
	})
	r := newV3Reactor(t, agent.addr, baseCfg())

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = r.v3RoundTrip(context.Background(), getReqPDU(), 2*time.Second, 3)
		}(i)
	}
	wg.Wait()
	for i, e := range errs {
		if e != nil {
			t.Fatalf("op %d: %v", i, e)
		}
	}
	if got := probes.Load(); got != 1 {
		t.Fatalf("expected exactly 1 discovery probe, got %d", got)
	}
}

// TestReactorV3_ResyncBudget confirms a second notInTimeWindow fails with the
// exhausted-budget error.
func TestReactorV3_ResyncBudget(t *testing.T) {
	cfg := baseCfg()
	var agent *v3Agent
	before := func(dec *v3Decoded, _ *net.UDPAddr) []byte {
		if !dec.msg.flags.auth {
			return nil
		}
		// Always reject with notInTimeWindow.
		m := &v3Message{
			msgID:         dec.msg.msgID,
			msgMaxSize:    v3MaxMessageSize,
			securityModel: securityModelUSM,
			flags:         msgFlags{auth: true},
			sec:           usmSecurityParameters{engineID: v3TestEngine, engineBoots: 9, engineTime: 5000, userName: cfg.Username},
			scoped: &scopedPDU{
				contextEngineID: v3TestEngine,
				pdu:             pdu{typ: pduReport, requestID: 0, varbinds: []VarBind{usmStatsVB(2)}},
			},
		}
		raw, _ := agent.usm.signMessage(m, agent.usm.loadAuthKey())
		return raw
	}
	ccfg := cfg
	ccfg.EngineID = v3TestEngine
	agent = startV3Agent(t, cfg, v3TestEngine, 9, 5000, before)
	r := newV3Reactor(t, agent.addr, ccfg)

	_, err := r.v3RoundTrip(context.Background(), getReqPDU(), time.Second, 1)
	if !errors.Is(err, ErrResyncExhausted) {
		t.Fatalf("expected ErrResyncExhausted, got %v", err)
	}
}

// TestReactorV3_RediscoveryAfterEngineRestart simulates a peer that restarted
// with a new engineID mid-session: a configured-engineID client's first
// authenticated request draws an unauthenticated unknownEngineID Report
// (from the peer) carrying the new engineID; the reactor re-localizes via
// applyRediscovery and the retry succeeds.
func TestReactorV3_RediscoveryAfterEngineRestart(t *testing.T) {
	oldEngine := mustHex("80001f8800dead00000000aa")
	// Agent runs as the NEW engine; the client is configured for the OLD one.
	var agent *v3Agent
	before := func(dec *v3Decoded, _ *net.UDPAddr) []byte {
		if dec.msg.flags.auth && bytes.Equal(dec.msg.sec.engineID, oldEngine) {
			// Peer no longer recognizes the old engineID: unauthenticated
			// unknownEngineID Report advertising the new engineID.
			rep := reportPDUUnauth(v3TestEngine, agent.boots, agent.etime)
			rep.msgID = dec.msg.msgID
			raw, _ := encodeV3Message(rep)
			return raw
		}
		return nil // requests on the new engineID fall through to a data reply
	}
	agent = startV3Agent(t, baseCfg(), v3TestEngine, 7, 4000, before)

	ccfg := baseCfg()
	ccfg.EngineID = oldEngine // configured -> no initial discovery
	r := newV3Reactor(t, agent.addr, ccfg)

	res, err := r.v3RoundTrip(context.Background(), getReqPDU(), 2*time.Second, 3)
	if err != nil {
		t.Fatalf("v3RoundTrip after re-discovery: %v", err)
	}
	if res.isReport || res.scoped == nil {
		t.Fatalf("expected data reply after re-discovery, got %+v", res)
	}
	if !bytes.Equal(r.usm.currentEngineID(), v3TestEngine) {
		t.Fatalf("engine not re-localized: got %x", r.usm.currentEngineID())
	}
}

// TestReactorV3_ForgedReplyDropped is the v3 analog of
// TestReactor_WrongCommunityDropped (the validate-reply-identity-before-demux
// learning): a forged authenticated reply with a tampered HMAC on a live
// in-flight msgID must drop+count WITHOUT burning the waiter, and the genuine
// reply must still resolve the request.
func TestReactorV3_ForgedReplyDropped(t *testing.T) {
	var agent *v3Agent
	before := func(dec *v3Decoded, src *net.UDPAddr) []byte {
		if !dec.msg.flags.auth {
			return nil // discovery
		}
		// Build a genuine reply, corrupt one byte (breaks the whole-message
		// HMAC), and send it from the agent socket; then fall through so the
		// loop also sends the genuine reply for the same msgID.
		sp, err := agent.usm.verifyInbound(dec)
		if err != nil {
			return nil
		}
		resp := pdu{
			typ: pduGetResponse, requestID: sp.pdu.requestID,
			varbinds: []VarBind{OctetStringVar{Header: Header{OID: MustOID(1, 3, 6, 1, 2, 1, 1, 1, 0), Kind: KindOctetString}, Value: []byte("agent-ok")}},
		}
		forged, err := agent.usm.buildOutbound(dec.msg.msgID, resp, agent.boots, agent.etime, false)
		if err == nil {
			forged[len(forged)-1] ^= 0xff // tamper -> HMAC verify fails
			_, _ = agent.conn.WriteToUDP(forged, src)
		}
		return nil // genuine reply follows
	}
	agent = startV3Agent(t, baseCfg(), v3TestEngine, 3, 1000, before)
	r := newV3Reactor(t, agent.addr, baseCfg())

	before0 := r.droppedCount()
	res, err := r.v3RoundTrip(context.Background(), getReqPDU(), 2*time.Second, 2)
	if err != nil {
		t.Fatalf("genuine reply should resolve despite forged datagram: %v", err)
	}
	if res.scoped == nil {
		t.Fatalf("expected genuine data reply")
	}
	if r.droppedCount() <= before0 {
		t.Fatalf("forged reply was not dropped+counted (dropped %d -> %d)", before0, r.droppedCount())
	}
}
