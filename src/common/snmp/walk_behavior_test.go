package snmp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
)

// Walk behavioral tests (SNMP conformance hardening) driven through the
// misbehaving-responder seam.

// mibN builds an n-row OctetString table rooted at mibRoot().
func mibN(n int) []kv {
	r := mibRoot()
	out := make([]kv, 0, n)
	for i := 1; i <= n; i++ {
		oid := r.Append(uint32(i))
		out = append(out, kv{oid, octet(oid, fmt.Sprintf("eth%d", i))})
	}
	return out
}

// collectBulkWalk runs a BulkWalk over root and returns the non-exception OIDs
// yielded, failing on a walk error.
func collectBulkWalk(t *testing.T, sess Session, root OID) []OID {
	t.Helper()
	var got []OID
	w := sess.BulkWalk(context.Background(), root)
	for oid, vb := range w.Iter() {
		if !IsException(vb) {
			got = append(got, oid)
		}
	}
	if err := w.Err(); err != nil {
		t.Fatalf("walk err = %v, want nil", err)
	}
	return got
}

// Covers conformance matrix row: walk-toobig-fallback (RFC 3416; MikroTik >50,
// Cisco/Nokia/F5): an agent that returns tooBig above a max-repetitions
// threshold must not abort the walk — the walker halves max-repetitions until
// the agent accepts the request and completes the full table.
func TestWalk_TooBigHalvesAndCompletes(t *testing.T) {
	const n = 8
	// Threshold 10: the walk's initial reps (50) is rejected; halving
	// 50->25->12->6 lands under the threshold and the table completes via bulk.
	agent := startMisbehavingResponder(t, mibN(n), misbehaviorCfg{tooBigAboveReps: 10})
	sess := dialWalk(t, agent)

	got := collectBulkWalk(t, sess, mibRoot())
	if len(got) != n {
		t.Fatalf("walked %d rows, want %d (tooBig halving must complete the table)", len(got), n)
	}
}

// Covers conformance matrix row: walk-toobig-fallback (RFC 3416), floor
// case: an agent that rejects GetBulk at every max-repetitions (including 1)
// must drive the walker all the way to a GetNext-per-OID fallback rather than
// aborting — the full table still completes.
func TestWalk_TooBigFallsBackToGetNext(t *testing.T) {
	const n = 5
	agent := startMisbehavingResponder(t, mibN(n), misbehaviorCfg{bulkAlwaysTooBig: true})
	sess := dialWalk(t, agent)

	got := collectBulkWalk(t, sess, mibRoot())
	if len(got) != n {
		t.Fatalf("walked %d rows, want %d (GetNext fallback must complete the table)", len(got), n)
	}
	// Spot-check ordering: first and last row OIDs.
	if !got[0].Equal(mibRoot().Append(1)) || !got[n-1].Equal(mibRoot().Append(uint32(n))) {
		t.Fatalf("walked OIDs %v, want eth1..eth%d in order", got, n)
	}
}

// noSuchAt returns a NoSuchInstanceVar at oid (a canned-MIB hole).
func noSuchInstAt(oid OID) VarBind {
	return NoSuchInstanceVar{Header: Header{OID: oid, Kind: KindNoSuchInstance}}
}

func noSuchObjAt(oid OID) VarBind {
	return NoSuchObjectVar{Header: Header{OID: oid, Kind: KindNoSuchObject}}
}

// Covers conformance matrix row: walk-nosuch-semantics (RFC 3416). A
// noSuchInstance mid-table is skipped (not yielded) and the walk continues past
// it; a noSuchObject terminates the subtree walk. Exception OIDs are classified
// before the cycle guard, so they never trip a false cycle abort.
func TestWalk_NoSuchInstanceSkips(t *testing.T) {
	r := mibRoot()
	mib := []kv{
		{r.Append(1), octet(r.Append(1), "eth1")},
		{r.Append(2), noSuchInstAt(r.Append(2))}, // hole: skip & continue
		{r.Append(3), octet(r.Append(3), "eth3")},
	}
	agent := startMisbehavingResponder(t, mib, misbehaviorCfg{})
	sess := dialWalk(t, agent)

	got := collectBulkWalk(t, sess, r)
	if len(got) != 2 || !got[0].Equal(r.Append(1)) || !got[1].Equal(r.Append(3)) {
		t.Fatalf("yielded %v, want [eth1 eth3] (noSuchInstance eth2 skipped)", got)
	}
}

func TestWalk_NoSuchObjectTerminatesSubtree(t *testing.T) {
	r := mibRoot()
	mib := []kv{
		{r.Append(1), octet(r.Append(1), "eth1")},
		{r.Append(2), noSuchObjAt(r.Append(2))}, // object absent: terminate
		{r.Append(3), octet(r.Append(3), "eth3")},
	}
	agent := startMisbehavingResponder(t, mib, misbehaviorCfg{})
	sess := dialWalk(t, agent)

	got := collectBulkWalk(t, sess, r)
	if len(got) != 1 || !got[0].Equal(r.Append(1)) {
		t.Fatalf("yielded %v, want [eth1] (noSuchObject terminates the subtree)", got)
	}
}

// Covers conformance matrix row: walk-mid-pdu-eomv (RFC 3416). In the
// single-OID GETBULK chain this codec issues, all value varbinds preceding an
// endOfMibView in the same PDU are yielded before the walk terminates at the
// endOfMibView marker — no data is lost. (Per-column EOMV across a multi-OID
// GETBULK table walk is out of scope; the walk shape is single-chain.)
func TestWalk_MidPduEndOfMibView(t *testing.T) {
	const n = 4
	agent := startMisbehavingResponder(t, mibN(n), misbehaviorCfg{})
	sess := dialWalk(t, agent)

	// One PDU carries all n rows then the EndOfMibView terminator; every value
	// before it must be yielded.
	got := collectBulkWalk(t, sess, mibRoot())
	if len(got) != n {
		t.Fatalf("yielded %d values before EndOfMibView, want %d (no data loss)", len(got), n)
	}
}

// Covers conformance matrix row: walk-mutate-midwalk (RFC 3416; WLAN). A
// table that gains/loses rows between successive PDUs yields a self-consistent
// snapshot — strictly increasing OIDs, no duplicates, no loop, no error.
func TestWalk_MutateMidWalk(t *testing.T) {
	r := mibRoot()
	base := mibN(6)
	cfg := misbehaviorCfg{
		maxPerPDU: 2, // force multiple PDUs so mutation lands mid-walk
		mutatePerCall: func(call int, b []kv) []kv {
			// From the 2nd PDU on, drop eth3 and add eth7 — rows shifting under
			// the walk. The walk must tolerate the gap and the late addition.
			if call == 0 {
				return b
			}
			out := make([]kv, 0, len(b)+1)
			for _, e := range b {
				if e.oid.Equal(r.Append(3)) {
					continue
				}
				out = append(out, e)
			}
			out = append(out, kv{r.Append(7), octet(r.Append(7), "eth7")})
			return out
		},
	}
	agent := startMisbehavingResponder(t, base, cfg)
	sess := dialWalk(t, agent)

	got := collectBulkWalk(t, sess, r) // fails on any walk error
	// Self-consistency: strictly increasing, no duplicates.
	for i := 1; i < len(got); i++ {
		if got[i].Compare(got[i-1]) <= 0 {
			t.Fatalf("walk yielded non-increasing/duplicate OIDs: %v", got)
		}
	}
	if len(got) == 0 {
		t.Fatal("mutating walk yielded nothing, want a self-consistent snapshot")
	}
}

// Covers conformance matrix row: walk-nosuch-semantics (RFC 3416) — watcher
// result-set contract. The change-stream consumer (watcher.go collectTableWalk /
// extractIndicatorVB) diffs the walk's yielded varbinds; extractIndicatorVB
// already filters IsException varbinds, so routing noSuchInstance to skip (rather
// than yielding it) produces the SAME post-filter row set the watcher sees — no
// spurious add/remove churn. This pins that the walk output handed to the
// watcher contains only real value rows when holes are present.
func TestWalk_NoSuchRoutingWatcherContract(t *testing.T) {
	r := mibRoot()
	mib := []kv{
		{r.Append(1), octet(r.Append(1), "eth1")},
		{r.Append(2), noSuchInstAt(r.Append(2))},
		{r.Append(3), octet(r.Append(3), "eth3")},
		{r.Append(4), noSuchInstAt(r.Append(4))},
		{r.Append(5), octet(r.Append(5), "eth5")},
	}
	agent := startMisbehavingResponder(t, mib, misbehaviorCfg{})
	sess := dialWalk(t, agent)

	w := sess.BulkWalk(context.Background(), r)
	var values, noSuch int
	for _, vb := range w.Iter() {
		switch vb.(type) {
		case NoSuchInstanceVar, NoSuchObjectVar:
			noSuch++
		case EndOfMibViewVar:
			// The end-of-walk terminator is legitimately yielded and
			// filtered by the watcher's extractIndicatorVB; not churn.
		default:
			values++
		}
	}
	if err := w.Err(); err != nil {
		t.Fatalf("walk err = %v", err)
	}
	if values != 3 {
		t.Fatalf("yielded %d value rows, want 3 (eth1/eth3/eth5)", values)
	}
	if noSuch != 0 {
		t.Fatalf("yielded %d noSuch varbinds, want 0 (holes must be skipped, not handed to the watcher)", noSuch)
	}
}

// Covers conformance matrix row: walk-nosuch-semantics (RFC 3416) — budget
// bound. An agent that streams endless strictly-increasing in-subtree
// noSuchInstance varbinds must not loop forever: skipped instances charge the
// walk budget, so the walk aborts with ErrMaxWalkVars rather than hanging.
func TestWalk_EndlessNoSuchInstanceBounded(t *testing.T) {
	root := mibRoot()
	var n atomic.Int32
	agent := startMockAgent(t, func(req *message, _ *net.UDPAddr, send func(*message)) {
		oid := root.Append(uint32(n.Add(1))) // strictly increasing, in-subtree
		send(&message{
			version:   req.version,
			community: req.community,
			pdu: pdu{
				typ:       pduGetResponse,
				requestID: req.pdu.requestID,
				varbinds:  []VarBind{NoSuchInstanceVar{Header: Header{OID: oid, Kind: KindNoSuchInstance}}},
			},
		})
	})
	sess := dialWalk(t, agent, WithMaxWalkVars(5))

	w := sess.BulkWalk(context.Background(), root)
	for range w.Iter() {
	}
	if !errors.Is(w.Err(), ErrMaxWalkVars) {
		t.Fatalf("endless noSuchInstance walk err = %v, want ErrMaxWalkVars (budget must bound skips)", w.Err())
	}
}

// TestGetBulk_TooBigFloorStillErrors regression-pins the GetBulk single-shot
// path: unlike BulkWalk, GetBulk surfaces the PDUError at the floor (it does
// not fall back to GetNext). Guards the shared nextBulkReps extraction.
func TestGetBulk_TooBigFloorStillErrors(t *testing.T) {
	agent := startMisbehavingResponder(t, mibN(3), misbehaviorCfg{bulkAlwaysTooBig: true})
	sess := dialWalk(t, agent)

	_, err := sess.GetBulk(context.Background(), 0, 10, []OID{mibRoot()})
	if err == nil {
		t.Fatal("GetBulk against always-tooBig agent returned nil error, want PDUError")
	}
	var pe *PDUError
	if !errors.As(err, &pe) || pe.Status != TooBig {
		t.Fatalf("GetBulk err = %v, want a TooBig PDUError", err)
	}
}
