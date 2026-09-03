package snmp

import (
	"context"
	"net"
	"sort"
	"sync/atomic"
	"testing"
	"time"
)

// Misbehaving-responder seam (SNMP conformance hardening).
//
// startScriptedGetNext (session_guards_test.go) drives a precise, hand-authored
// sequence of GetNext responses — ideal for cycle/non-increasing cases. This
// seam complements it with a *canned-MIB* responder that serves GetNext/GetBulk
// lexicographically (the shape the walker actually issues: a single requested
// OID, nonRepeaters=0) and layers deterministic misbehaviors on top: tooBig
// above a max-repetitions threshold (MikroTik), endOfMibView emerging at the
// table end, rows mutating between PDUs (WLAN), a duplicate datagram, and a
// mismatched response request-id.
//
// It reuses the in-package mockAgent, so it is connected-socket honest (it
// replies from its own socket to the requester) and decodes/encodes with the
// production codec — unlike bench/responder_test.go, which hand-rolls BER for
// benchmark fidelity behind the bench module + no_gosnmp exemption.
//
// Behavioral walk/transport gaps that a well-behaved snmpd cannot express
// deterministically (tooBig fallback, EOMV/mutate, dup-response) are
// driven through this seam in fast, race-clean unit tests.

// kv is one canned-MIB entry: an OID and the varbind served at it.
type kv struct {
	oid OID
	vb  VarBind
}

// misbehaviorCfg configures the misbehaviors layered atop the well-behaved
// canned-MIB responder. The zero value is a correct, well-behaved agent.
type misbehaviorCfg struct {
	// tooBigAboveReps, when >0, makes a GetBulk whose maxRepetitions exceeds
	// it reply errorStatus=TooBig with no varbinds (MikroTik ">50" behavior).
	tooBigAboveReps int
	// bulkAlwaysTooBig makes every GetBulk reply TooBig regardless of
	// max-repetitions (an agent that cannot do GetBulk at all), forcing the
	// walker to halve to the floor and fall back to GetNext. GetNext is served
	// normally.
	bulkAlwaysTooBig bool
	// duplicate sends each response datagram twice (retransmit/dup test).
	duplicate bool
	// mutateRequestID replies with requestID+1, an id that will not match the
	// in-flight request (used to drive demux-drop and mismatched-id paths).
	mutateRequestID bool
	// mutatePerCall, when set, rebuilds the served MIB for the 0-based call
	// index, letting a test add or drop rows between successive PDUs. The
	// returned slice is copied and re-sorted, so callers need not pre-sort.
	mutatePerCall func(call int, base []kv) []kv
	// maxPerPDU, when >0, caps the number of varbinds a GetBulk response
	// returns regardless of max-repetitions (a real agent returning fewer than
	// requested), forcing a multi-PDU walk so mutate-mid-walk can be exercised.
	maxPerPDU int
}

// startMisbehavingResponder starts a v2c mock agent serving mib over
// GetNext/GetBulk with cfg's misbehaviors. See the file comment for rationale.
func startMisbehavingResponder(t *testing.T, mib []kv, cfg misbehaviorCfg) *mockAgent {
	t.Helper()
	base := sortedCopy(mib)
	var calls atomic.Int32
	return startMockAgent(t, func(req *message, _ *net.UDPAddr, send func(*message)) {
		call := int(calls.Add(1)) - 1
		served := base
		if cfg.mutatePerCall != nil {
			served = sortedCopy(cfg.mutatePerCall(call, base))
		}

		resp := &message{
			version:   req.version,
			community: req.community,
			pdu:       pdu{typ: pduGetResponse, requestID: req.pdu.requestID},
		}
		if cfg.mutateRequestID {
			resp.pdu.requestID = req.pdu.requestID + 1
		}

		switch req.pdu.typ {
		case pduGetBulkRequest:
			if cfg.bulkAlwaysTooBig ||
				(cfg.tooBigAboveReps > 0 && req.pdu.maxRepetitions > cfg.tooBigAboveReps) {
				resp.pdu.errorStatus = TooBig
				emitResp(send, resp, cfg.duplicate)
				return
			}
			start := req.pdu.varbinds[0].GetHeader().OID
			reps := req.pdu.maxRepetitions
			if reps <= 0 {
				reps = 1
			}
			if cfg.maxPerPDU > 0 && cfg.maxPerPDU < reps {
				reps = cfg.maxPerPDU
			}
			resp.pdu.varbinds = bulkFrom(served, start, reps)
		case pduGetNextRequest:
			start := req.pdu.varbinds[0].GetHeader().OID
			resp.pdu.varbinds = bulkFrom(served, start, 1)
		default: // GetRequest and friends: exact-match get
			resp.pdu.varbinds = getExact(served, req.pdu.varbinds)
		}
		emitResp(send, resp, cfg.duplicate)
	})
}

// bulkFrom returns up to reps lexicographic successors of start from mib. When
// the table is exhausted before reps are filled, it appends a single
// EndOfMibView varbind (on the last served OID, or start if none) — the
// terminator a real agent returns, which drives the walk's EOMV handling.
func bulkFrom(mib []kv, start OID, reps int) []VarBind {
	var out []VarBind
	for _, e := range mib {
		if e.oid.Compare(start) > 0 {
			out = append(out, e.vb)
			if len(out) >= reps {
				return out
			}
		}
	}
	last := start
	if len(out) > 0 {
		last = out[len(out)-1].GetHeader().OID
	}
	return append(out, EndOfMibViewVar{Header: Header{OID: last, Kind: KindEndOfMibView}})
}

// getExact answers a GetRequest: each requested varbind's OID is matched
// exactly, returning NoSuchInstance when absent.
func getExact(mib []kv, reqs []VarBind) []VarBind {
	out := make([]VarBind, 0, len(reqs))
	for _, rq := range reqs {
		oid := rq.GetHeader().OID
		var found VarBind
		for _, e := range mib {
			if e.oid.Equal(oid) {
				found = e.vb
				break
			}
		}
		if found == nil {
			found = NoSuchInstanceVar{Header: Header{OID: oid, Kind: KindNoSuchInstance}}
		}
		out = append(out, found)
	}
	return out
}

// sortedCopy returns a lexicographically-sorted copy of mib.
func sortedCopy(mib []kv) []kv {
	cp := append([]kv(nil), mib...)
	sort.Slice(cp, func(i, j int) bool { return cp[i].oid.Compare(cp[j].oid) < 0 })
	return cp
}

// emitResp sends resp once, or twice when duplicate is set.
func emitResp(send func(*message), resp *message, duplicate bool) {
	send(resp)
	if duplicate {
		send(resp)
	}
}

// bulkReq builds a v2c GetBulkRequest for a single OID with the given
// max-repetitions, matching the shape the walker issues (nonRepeaters=0).
func bulkReq(oid OID, maxReps int) *message {
	return &message{
		version:   V2c,
		community: "public",
		pdu: pdu{
			typ:            pduGetBulkRequest,
			nonRepeaters:   0,
			maxRepetitions: maxReps,
			varbinds:       []VarBind{NullVar{Header: Header{OID: oid, Kind: KindNull}}},
		},
	}
}

func mibRoot() OID { return MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 2) }

func threeRowMIB() []kv {
	r := mibRoot()
	return []kv{
		{r.Append(1), octet(r.Append(1), "eth1")},
		{r.Append(2), octet(r.Append(2), "eth2")},
		{r.Append(3), octet(r.Append(3), "eth3")},
	}
}

func TestMisbehavingResponder_TooBigThreshold(t *testing.T) {
	agent := startMisbehavingResponder(t, threeRowMIB(), misbehaviorCfg{tooBigAboveReps: 50})
	r := newTestReactor(t, agent.addr, tcfg{})

	// Above the threshold -> tooBig.
	resp, err := r.do(context.Background(), bulkReq(mibRoot(), 100))
	if err != nil {
		t.Fatalf("bulk(100) err = %v", err)
	}
	if resp.pdu.errorStatus != TooBig {
		t.Fatalf("bulk(100) errorStatus = %v, want TooBig", resp.pdu.errorStatus)
	}

	// At/below the threshold -> a normal table response.
	resp, err = r.do(context.Background(), bulkReq(mibRoot(), 10))
	if err != nil {
		t.Fatalf("bulk(10) err = %v", err)
	}
	if resp.pdu.errorStatus != NoError || len(resp.pdu.varbinds) == 0 {
		t.Fatalf("bulk(10) = status %v, %d varbinds; want NoError with rows",
			resp.pdu.errorStatus, len(resp.pdu.varbinds))
	}
}

func TestMisbehavingResponder_EndOfMibViewAtTableEnd(t *testing.T) {
	agent := startMisbehavingResponder(t, threeRowMIB(), misbehaviorCfg{})
	r := newTestReactor(t, agent.addr, tcfg{})

	// maxReps exceeds the table size: 3 rows then a single EndOfMibView.
	resp, err := r.do(context.Background(), bulkReq(mibRoot(), 10))
	if err != nil {
		t.Fatalf("bulk err = %v", err)
	}
	vbs := resp.pdu.varbinds
	if len(vbs) != 4 {
		t.Fatalf("got %d varbinds, want 4 (3 rows + EndOfMibView)", len(vbs))
	}
	if vbs[len(vbs)-1].GetHeader().Kind != KindEndOfMibView {
		t.Fatalf("last varbind kind = %v, want EndOfMibView", vbs[len(vbs)-1].GetHeader().Kind)
	}
}

func TestMisbehavingResponder_MismatchedRequestIDDoesNotResolve(t *testing.T) {
	agent := startMisbehavingResponder(t, threeRowMIB(), misbehaviorCfg{mutateRequestID: true})
	// Short timeout, no retries: a reply with a mismatched id must not resolve
	// the in-flight request, so the round-trip times out.
	r := newTestReactor(t, agent.addr, tcfg{timeout: 150 * time.Millisecond})

	_, err := r.do(context.Background(), bulkReq(mibRoot(), 5))
	if err == nil {
		t.Fatal("round-trip resolved despite mismatched response request-id")
	}
}

func TestMisbehavingResponder_MutatePerCallChangesRows(t *testing.T) {
	// Call 0 sees the full table; call 1 drops the middle row. Proves the seam
	// can mutate the served MIB between successive PDUs (mutate-mid-walk basis).
	r0 := mibRoot()
	cfg := misbehaviorCfg{
		mutatePerCall: func(call int, base []kv) []kv {
			if call == 0 {
				return base
			}
			out := make([]kv, 0, len(base))
			for _, e := range base {
				if e.oid.Equal(r0.Append(2)) {
					continue // drop eth2 on later calls
				}
				out = append(out, e)
			}
			return out
		},
	}
	agent := startMisbehavingResponder(t, threeRowMIB(), cfg)
	r := newTestReactor(t, agent.addr, tcfg{})

	resp0, err := r.do(context.Background(), bulkReq(mibRoot(), 1))
	if err != nil {
		t.Fatalf("call 0 err = %v", err)
	}
	if got := resp0.pdu.varbinds[0].GetHeader().OID; !got.Equal(r0.Append(1)) {
		t.Fatalf("call 0 first oid = %v, want eth1", got)
	}
	// Second call after eth1: with eth2 dropped, the next successor is eth3.
	resp1, err := r.do(context.Background(), bulkReq(r0.Append(1), 1))
	if err != nil {
		t.Fatalf("call 1 err = %v", err)
	}
	if got := resp1.pdu.varbinds[0].GetHeader().OID; !got.Equal(r0.Append(3)) {
		t.Fatalf("call 1 first oid = %v, want eth3 (eth2 dropped mid-walk)", got)
	}
}

func TestMisbehavingResponder_DuplicateDatagramTolerated(t *testing.T) {
	// The responder sends every reply twice; the reactor's request-id demux
	// drops the duplicate, so the round-trip still resolves cleanly. (The
	// dedicated dup-drop assertion lives in walk_transport_pins_test.go.)
	agent := startMisbehavingResponder(t, threeRowMIB(), misbehaviorCfg{duplicate: true})
	r := newTestReactor(t, agent.addr, tcfg{})

	resp, err := r.do(context.Background(), bulkReq(mibRoot(), 2))
	if err != nil {
		t.Fatalf("round-trip with duplicate reply err = %v", err)
	}
	if resp.pdu.errorStatus != NoError {
		t.Fatalf("errorStatus = %v, want NoError", resp.pdu.errorStatus)
	}
	// A second round-trip must still work — the stale duplicate from the first
	// exchange must not poison the demux table.
	if _, err := r.do(context.Background(), bulkReq(mibRoot(), 2)); err != nil {
		t.Fatalf("second round-trip err = %v (duplicate poisoned demux?)", err)
	}
}
