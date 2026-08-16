package snmp

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

// mockAgent is a UDP responder for reactor tests. Each decoded request is
// passed to handle along with a send callback (which replies from the
// agent's own socket, i.e. from the peer address) and the requester's
// address, so a handler can also reply from an alternate socket to
// exercise source validation.
type mockAgent struct {
	conn *net.UDPConn
	addr *net.UDPAddr
}

func startMockAgent(t *testing.T, handle func(req *message, src *net.UDPAddr, send func(*message))) *mockAgent {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("mock agent listen: %v", err)
	}
	a := &mockAgent{conn: conn, addr: conn.LocalAddr().(*net.UDPAddr)}
	go func() {
		buf := make([]byte, maxUDPPayload)
		for {
			n, src, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			data := append([]byte(nil), buf[:n]...)
			req, derr := decodeMessage(data)
			if derr != nil {
				continue
			}
			send := func(resp *message) {
				out, err := encodeMessage(resp)
				if err != nil {
					return
				}
				_, _ = conn.WriteToUDP(out, src)
			}
			handle(req, src, send)
		}
	}()
	t.Cleanup(func() { _ = conn.Close() })
	return a
}

// respondEcho replies with a GetResponse echoing the request's id and
// varbinds, so a test can verify which request a reply answers.
func respondEcho(req *message, _ *net.UDPAddr, send func(*message)) {
	send(&message{
		version:   req.version,
		community: req.community,
		pdu: pdu{
			typ:       pduGetResponse,
			requestID: req.pdu.requestID,
			varbinds:  req.pdu.varbinds,
		},
	})
}

func getReq(oid OID) *message {
	return &message{
		version:   V2c,
		community: "public",
		pdu: pdu{
			typ:      pduGetRequest,
			varbinds: []VarBind{NullVar{Header: Header{OID: oid, Kind: KindNull}}},
		},
	}
}

// tcfg bundles the reactor config with the per-call timeout/retries the
// test wants roundTrip to use (those are no longer reactor fields).
type tcfg struct {
	timeout     time.Duration
	retries     int
	validateSrc bool
	maxInFlight int
	multiHomed  bool
}

// testReactor wraps a reactor with the test's default timeout/retries so
// call sites can use the convenience do() method.
type testReactor struct {
	*reactor
	timeout time.Duration
	retries int
}

func newTestReactor(t *testing.T, peer *net.UDPAddr, c tcfg) *testReactor {
	t.Helper()
	if c.timeout == 0 {
		c.timeout = time.Second
	}
	r, err := newReactor(context.Background(), reactorConfig{
		peer:        peer,
		validateSrc: c.validateSrc,
		multiHomed:  c.multiHomed,
		maxInFlight: c.maxInFlight,
		// getReq builds V2c/"public" requests, and the mock agent echoes
		// those, so the reactor must expect the same to accept replies.
		version:   V2c,
		community: "public",
	})
	if err != nil {
		t.Fatalf("newReactor: %v", err)
	}
	t.Cleanup(func() { _ = r.close() })
	return &testReactor{reactor: r, timeout: c.timeout, retries: c.retries}
}

// do runs a round-trip with the test reactor's default timeout/retries.
func (tr *testReactor) do(ctx context.Context, req *message) (*message, error) {
	return tr.roundTrip(ctx, req, tr.timeout, tr.retries)
}

func TestReactor_HappyPath(t *testing.T) {
	agent := startMockAgent(t, respondEcho)
	r := newTestReactor(t, agent.addr, tcfg{})

	oid := MustOID(1, 3, 6, 1, 2, 1, 1, 1, 0)
	resp, err := r.do(context.Background(), getReq(oid))
	if err != nil {
		t.Fatalf("roundTrip: %v", err)
	}
	if len(resp.pdu.varbinds) != 1 || !resp.pdu.varbinds[0].GetHeader().OID.Equal(oid) {
		t.Fatalf("unexpected response: %+v", resp.pdu)
	}
	if r.inFlight() != 0 {
		t.Errorf("registry not empty after success: %d", r.inFlight())
	}
}

// TestReactor_ConcurrentOutOfOrder verifies the demux contract: two
// concurrent requests whose replies arrive out of order each receive
// their own reply.
func TestReactor_ConcurrentOutOfOrder(t *testing.T) {
	// Delay the reply to OID ...1 so the reply to ...2 comes back first.
	oid1 := MustOID(1, 3, 6, 1, 2, 1, 1, 1)
	oid2 := MustOID(1, 3, 6, 1, 2, 1, 1, 2)
	agent := startMockAgent(t, func(req *message, src *net.UDPAddr, send func(*message)) {
		last := req.pdu.varbinds[0].GetHeader().OID
		delay := 5 * time.Millisecond
		if last.Equal(oid1) {
			delay = 60 * time.Millisecond
		}
		go func() {
			time.Sleep(delay)
			respondEcho(req, src, send)
		}()
	})
	r := newTestReactor(t, agent.addr, tcfg{timeout: 2 * time.Second})

	var wg sync.WaitGroup
	check := func(oid OID) {
		defer wg.Done()
		resp, err := r.do(context.Background(), getReq(oid))
		if err != nil {
			t.Errorf("roundTrip %s: %v", oid, err)
			return
		}
		if !resp.pdu.varbinds[0].GetHeader().OID.Equal(oid) {
			t.Errorf("request %s got reply for %s — misrouted", oid, resp.pdu.varbinds[0].GetHeader().OID)
		}
	}
	wg.Add(2)
	go check(oid1)
	go check(oid2)
	wg.Wait()
}

// TestReactor_HighConcurrencyFanout runs many concurrent requests through
// one reactor; every one must resolve to its own reply.
func TestReactor_HighConcurrencyFanout(t *testing.T) {
	agent := startMockAgent(t, respondEcho)
	r := newTestReactor(t, agent.addr, tcfg{timeout: 2 * time.Second})

	const n = 64
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			oid := MustOID(1, 3, 6, 1, 4, 1, uint32(i+1))
			resp, err := r.do(context.Background(), getReq(oid))
			if err != nil {
				t.Errorf("req %d: %v", i, err)
				return
			}
			if !resp.pdu.varbinds[0].GetHeader().OID.Equal(oid) {
				t.Errorf("req %d misrouted", i)
			}
		}(i)
	}
	wg.Wait()
	if r.inFlight() != 0 {
		t.Errorf("registry not empty: %d", r.inFlight())
	}
}

// TestReactor_LateDuplicateDropped verifies a duplicate reply for an
// already-delivered id is dropped and counted, not delivered.
//
// Covers conformance matrix row: txp-dup-response (gosnmp #417). A
// duplicate/retransmitted response for an already-resolved request-id is
// dropped by the demux; a later genuine reply still resolves.
func TestReactor_LateDuplicateDropped(t *testing.T) {
	agent := startMockAgent(t, func(req *message, src *net.UDPAddr, send func(*message)) {
		respondEcho(req, src, send)
		// Late duplicate of the same reply.
		go func() {
			time.Sleep(30 * time.Millisecond)
			respondEcho(req, src, send)
		}()
	})
	r := newTestReactor(t, agent.addr, tcfg{timeout: 2 * time.Second})

	oid := MustOID(1, 3, 6, 1, 2, 1, 1, 1, 0)
	if _, err := r.do(context.Background(), getReq(oid)); err != nil {
		t.Fatalf("roundTrip: %v", err)
	}
	// Wait for the duplicate to arrive and be dropped.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && r.droppedCount() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if r.droppedCount() == 0 {
		t.Error("late duplicate was not counted as dropped")
	}
	if r.inFlight() != 0 {
		t.Errorf("registry not empty: %d", r.inFlight())
	}
}

// TestReactor_UnknownIDDropped verifies a reply with an unregistered id is
// dropped, so the request times out rather than resolving on a stray
// reply.
func TestReactor_UnknownIDDropped(t *testing.T) {
	agent := startMockAgent(t, func(req *message, src *net.UDPAddr, send func(*message)) {
		// Reply with a deliberately wrong request-id.
		bad := *req
		bad.pdu.requestID = req.pdu.requestID ^ 0x1
		respondEcho(&bad, src, send)
	})
	r := newTestReactor(t, agent.addr, tcfg{timeout: 100 * time.Millisecond, retries: 0})

	oid := MustOID(1, 3, 6, 1, 2, 1, 1, 1, 0)
	_, err := r.do(context.Background(), getReq(oid))
	if !errors.Is(err, errTimeout) {
		t.Fatalf("err = %v, want Is errTimeout", err)
	}
	if r.droppedCount() == 0 {
		t.Error("stray reply was not counted as dropped")
	}
}

// TestReactor_WrongCommunityDropped verifies a datagram carrying a matching
// request-id but a community that differs from the session's is dropped in
// the read-loop before delivery, so a forged reply cannot
// satisfy or fail the in-flight request, and the genuine reply still
// resolves it.
func TestReactor_WrongCommunityDropped(t *testing.T) {
	oid := MustOID(1, 3, 6, 1, 2, 1, 1, 1, 0)
	agent := startMockAgent(t, func(req *message, src *net.UDPAddr, send func(*message)) {
		// A forged reply on the matching id, but with a different community.
		send(&message{
			version:   req.version,
			community: "wrong",
			pdu:       pdu{typ: pduGetResponse, requestID: req.pdu.requestID, varbinds: req.pdu.varbinds},
		})
		// The genuine reply (community "public") must still win.
		respondEcho(req, src, send)
	})
	r := newTestReactor(t, agent.addr, tcfg{timeout: 2 * time.Second})

	resp, err := r.do(context.Background(), getReq(oid))
	if err != nil {
		t.Fatalf("roundTrip: %v", err)
	}
	if resp.community != "public" {
		t.Fatalf("request resolved on a non-genuine reply: community=%q", resp.community)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && r.droppedCount() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if r.droppedCount() == 0 {
		t.Error("wrong-community datagram was not counted as dropped")
	}
}

// TestReactor_ReadErrorUnblocks verifies that an unexpected (non-close)
// read error marks the reactor closed and fails parked callers fast with
// ErrSessionClosed, rather than hanging until their timeout.
func TestReactor_ReadErrorUnblocks(t *testing.T) {
	agent := startMockAgent(t, func(*message, *net.UDPAddr, func(*message)) {})
	r := newTestReactor(t, agent.addr, tcfg{timeout: 10 * time.Second, retries: 5})

	errc := make(chan error, 1)
	go func() {
		_, err := r.do(context.Background(), getReq(MustOID(1, 3, 6, 1)))
		errc <- err
	}()
	time.Sleep(30 * time.Millisecond)

	// Inject a read error by closing the socket directly, bypassing the
	// orchestrated close() path so the read-loop sees an unexpected error.
	if err := r.conn.Close(); err != nil {
		t.Fatalf("inject read error: %v", err)
	}

	select {
	case err := <-errc:
		if !errors.Is(err, ErrSessionClosed) {
			t.Fatalf("parked roundTrip err = %v, want Is ErrSessionClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("read error did not unblock parked roundTrip")
	}
	// A new caller must also fail fast.
	if _, _, err := r.register(false); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("register after read error err = %v, want Is ErrSessionClosed", err)
	}
}

// TestReactor_ContextCancel verifies a canceled context aborts promptly
// and returns the unwrapped context error, leaving the registry empty.
func TestReactor_ContextCancel(t *testing.T) {
	// Silent agent: never replies.
	agent := startMockAgent(t, func(*message, *net.UDPAddr, func(*message)) {})
	r := newTestReactor(t, agent.addr, tcfg{timeout: 10 * time.Second, retries: 5})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(40 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err := r.do(ctx, getReq(MustOID(1, 3, 6, 1)))
	if err != context.Canceled {
		t.Fatalf("err = %v, want exactly context.Canceled (unwrapped)", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("cancellation took %v, expected prompt return", elapsed)
	}
	if r.inFlight() != 0 {
		t.Errorf("registry not empty after cancel: %d", r.inFlight())
	}
}

// TestReactor_DeadlineExceeded verifies a context deadline surfaces as the
// unwrapped context.DeadlineExceeded, not a generic wire timeout.
func TestReactor_DeadlineExceeded(t *testing.T) {
	agent := startMockAgent(t, func(*message, *net.UDPAddr, func(*message)) {})
	r := newTestReactor(t, agent.addr, tcfg{timeout: 10 * time.Second, retries: 5})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := r.do(ctx, getReq(MustOID(1, 3, 6, 1)))
	if err != context.DeadlineExceeded {
		t.Fatalf("err = %v, want exactly context.DeadlineExceeded", err)
	}
}

// TestReactor_RetriesThenTimeout verifies that when no reply ever arrives
// and no context deadline fires, retries are exhausted and the wire-level
// errTimeout is returned.
func TestReactor_RetriesThenTimeout(t *testing.T) {
	agent := startMockAgent(t, func(*message, *net.UDPAddr, func(*message)) {})
	r := newTestReactor(t, agent.addr, tcfg{timeout: 30 * time.Millisecond, retries: 2})

	_, err := r.do(context.Background(), getReq(MustOID(1, 3, 6, 1)))
	if !errors.Is(err, errTimeout) {
		t.Fatalf("err = %v, want Is errTimeout", err)
	}
}

// TestReactor_RegistryBound verifies WithMaxInFlight caps the registry: a
// send past the cap fails with errAtCapacity, and the registry returns to
// usable once an id is freed.
func TestReactor_RegistryBound(t *testing.T) {
	agent := startMockAgent(t, respondEcho)
	r := newTestReactor(t, agent.addr, tcfg{maxInFlight: 1})

	id, _, err := r.register(false)
	if err != nil {
		t.Fatalf("first register: %v", err)
	}
	if _, _, err := r.register(false); !errors.Is(err, errAtCapacity) {
		t.Fatalf("second register err = %v, want Is errAtCapacity", err)
	}
	r.deregister(id)
	if _, _, err := r.register(false); err != nil {
		t.Fatalf("register after free: %v", err)
	}
}

// TestReactor_SourceValidation verifies the source-acceptance model: the
// connected default drops a reply from a foreign source at the kernel; the
// multi-homed (unconnected) opt-out accepts it by request-id; and
// multi-homed + validateSrc rejects it in user space and counts the drop. The
// agent replies from an alternate socket so the source differs from the
// dialed peer.
func TestReactor_SourceValidation(t *testing.T) {
	replyFromAlternate := func(req *message, src *net.UDPAddr, _ func(*message)) {
		alt, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
		if err != nil {
			return
		}
		defer func() { _ = alt.Close() }()
		out, err := encodeMessage(&message{
			version:   req.version,
			community: req.community,
			pdu:       pdu{typ: pduGetResponse, requestID: req.pdu.requestID, varbinds: req.pdu.varbinds},
		})
		if err != nil {
			return
		}
		_, _ = alt.WriteToUDP(out, src)
	}

	t.Run("connected default drops foreign source", func(t *testing.T) {
		// The connected socket only receives from the dialed peer, so a reply
		// from an alternate source never reaches the read-loop — the request
		// times out.
		agent := startMockAgent(t, replyFromAlternate)
		r := newTestReactor(t, agent.addr, tcfg{timeout: 150 * time.Millisecond, retries: 0})
		if _, err := r.do(context.Background(), getReq(MustOID(1, 3, 6, 1))); !errors.Is(err, errTimeout) {
			t.Fatalf("connected default should not see a foreign-source reply (err=%v)", err)
		}
	})

	t.Run("multi-homed accepts foreign source", func(t *testing.T) {
		agent := startMockAgent(t, replyFromAlternate)
		r := newTestReactor(t, agent.addr, tcfg{timeout: time.Second, multiHomed: true})
		if _, err := r.do(context.Background(), getReq(MustOID(1, 3, 6, 1))); err != nil {
			t.Fatalf("multi-homed mode should accept foreign source: %v", err)
		}
	})

	t.Run("multi-homed + strict rejects foreign source", func(t *testing.T) {
		agent := startMockAgent(t, replyFromAlternate)
		r := newTestReactor(t, agent.addr, tcfg{timeout: 150 * time.Millisecond, retries: 0, multiHomed: true, validateSrc: true})
		_, err := r.do(context.Background(), getReq(MustOID(1, 3, 6, 1)))
		if !errors.Is(err, errTimeout) {
			t.Fatalf("strict mode should drop foreign source (err=%v)", err)
		}
		if r.droppedCount() == 0 {
			t.Error("foreign-source reply not counted as dropped")
		}
	})
}

// TestRequestID_PositiveAcrossWrap verifies request-ids stay in the
// positive 31-bit range even across a counter wrap.
func TestRequestID_PositiveAcrossWrap(t *testing.T) {
	ridCounter.Store(0x7ffffffe)
	for i := 0; i < 8; i++ {
		if id := nextRequestIDValue(); id < 0 {
			t.Fatalf("nextRequestIDValue returned negative id %d", id)
		}
	}
}

func TestReactor_CloseUnblocksAndRejects(t *testing.T) {
	agent := startMockAgent(t, func(*message, *net.UDPAddr, func(*message)) {})
	r := newTestReactor(t, agent.addr, tcfg{timeout: 10 * time.Second, retries: 5})

	errc := make(chan error, 1)
	go func() {
		_, err := r.do(context.Background(), getReq(MustOID(1, 3, 6, 1)))
		errc <- err
	}()
	time.Sleep(30 * time.Millisecond)
	if err := r.close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	select {
	case err := <-errc:
		if !errors.Is(err, ErrSessionClosed) {
			t.Fatalf("in-flight roundTrip err = %v, want Is ErrSessionClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not unblock in-flight roundTrip")
	}
	if _, _, err := r.register(false); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("register after close err = %v, want Is ErrSessionClosed", err)
	}
}
