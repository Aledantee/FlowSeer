package snmp

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
)

// startScriptedGetNext returns each script entry in turn as the varbinds
// of successive GetNext responses; once the script is exhausted it returns
// EndOfMibView for the requested OID. It lets a test drive the walk guards
// with a precise, non-increasing or cyclic OID sequence the well-behaved
// MIB agent would never produce.
func startScriptedGetNext(t *testing.T, script [][]VarBind) *mockAgent {
	t.Helper()
	var n atomic.Int32
	return startMockAgent(t, func(req *message, _ *net.UDPAddr, send func(*message)) {
		resp := &message{
			version:   req.version,
			community: req.community,
			pdu:       pdu{typ: pduGetResponse, requestID: req.pdu.requestID},
		}
		idx := int(n.Add(1)) - 1
		if idx < len(script) {
			resp.pdu.varbinds = script[idx]
		} else {
			oid := req.pdu.varbinds[0].GetHeader().OID
			resp.pdu.varbinds = []VarBind{
				EndOfMibViewVar{Header: Header{OID: oid, Kind: KindEndOfMibView}},
			}
		}
		send(resp)
	})
}

func ifDescrRoot() OID { return MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 2) }

func dialWalk(t *testing.T, agent *mockAgent, opts ...Option) Session {
	t.Helper()
	base := []Option{
		WithCommunity("public"),
		WithMinSecurity(MinSecurityNoAuth),
	}
	sess, err := NewSession(context.Background(), agent.addr.String(), V2c, append(base, opts...)...)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func TestWalkGuard_NonIncreasingAborts(t *testing.T) {
	root := ifDescrRoot()
	agent := startScriptedGetNext(t, [][]VarBind{
		{octet(root.Append(2), "eth2")},
		{octet(root.Append(1), "eth1")}, // < previous → non-increasing
	})
	sess := dialWalk(t, agent)

	w := sess.Walk(context.Background(), root)
	for range w.Iter() {
	}
	if !errors.Is(w.Err(), ErrOIDNotIncreasing) {
		t.Fatalf("walk err = %v, want Is ErrOIDNotIncreasing", w.Err())
	}
}

func TestWalkGuard_IgnoreNonIncreasingSkips(t *testing.T) {
	root := ifDescrRoot()
	agent := startScriptedGetNext(t, [][]VarBind{
		{octet(root.Append(2), "eth2")},
		{octet(root.Append(1), "eth1")}, // backward — skipped, not yielded
	})
	sess := dialWalk(t, agent, WithIgnoreNonIncreasing(true))

	count := 0
	w := sess.Walk(context.Background(), root)
	for _, vb := range w.Iter() {
		if !IsException(vb) {
			count++
		}
	}
	if err := w.Err(); err != nil {
		t.Fatalf("walk err with ignore = %v, want nil", err)
	}
	// The backward eth1 is dropped (not yielded) and, with no in-order
	// varbind in that PDU to advance the cursor, the walk terminates rather
	// than regressing — so only eth2 is yielded.
	if count != 1 {
		t.Fatalf("walked %d data rows, want 1 (backward OID skipped)", count)
	}
}

// TestWalkGuard_IgnoreNonIncreasingForwardCursor drives a PDU that mixes an
// in-order, a non-increasing, and a further in-order varbind under skip
// mode: the offending OID is dropped (no duplicate yield) while the walk
// keeps advancing forward from the highest in-order OID seen.
func TestWalkGuard_IgnoreNonIncreasingForwardCursor(t *testing.T) {
	root := ifDescrRoot()
	eth1 := root.Append(1)
	ethBack := root.Append(0) // < eth1: non-increasing, must be skipped
	eth3 := root.Append(3)
	agent := startScriptedGetNext(t, [][]VarBind{
		{octet(eth1, "eth1"), octet(ethBack, "eth0"), octet(eth3, "eth3")},
	})
	sess := dialWalk(t, agent, WithIgnoreNonIncreasing(true))

	var yielded []OID
	w := sess.Walk(context.Background(), root)
	for oid, vb := range w.Iter() {
		if !IsException(vb) {
			yielded = append(yielded, oid)
		}
	}
	if err := w.Err(); err != nil {
		t.Fatalf("walk err with ignore = %v, want nil", err)
	}
	if len(yielded) != 2 || !yielded[0].Equal(eth1) || !yielded[1].Equal(eth3) {
		t.Fatalf("yielded %v, want [eth1 eth3] (ethBack skipped, forward-only)", yielded)
	}
}

// Covers conformance matrix row: walk-cycling-oid (gosnmp #401 Juniper; Cisco
// CSCuf16921). An exact-repeat OID is an unambiguous cycle and aborts even in
// skip mode; the forward-cursor and non-increasing tests above cover the
// distinct single-regress (bounded skip-forward) case.
func TestWalkGuard_ExactRepeatIsCycle(t *testing.T) {
	root := ifDescrRoot()
	// Even with IgnoreNonIncreasing, an exact-repeat OID is an
	// unambiguous cycle and must abort.
	agent := startScriptedGetNext(t, [][]VarBind{
		{octet(root.Append(1), "eth1")},
		{octet(root.Append(1), "eth1")}, // exact repeat
	})
	sess := dialWalk(t, agent, WithIgnoreNonIncreasing(true))

	w := sess.Walk(context.Background(), root)
	for range w.Iter() {
	}
	if !errors.Is(w.Err(), ErrOIDNotIncreasing) {
		t.Fatalf("cycle err = %v, want Is ErrOIDNotIncreasing", w.Err())
	}
}

func TestWalkGuard_MaxWalkVarsBudget(t *testing.T) {
	root := ifDescrRoot()
	// An endlessly increasing agent: GetNext(oid) → oid with last sub-id
	// incremented, always in-subtree. The budget must stop it.
	agent := startMockAgent(t, func(req *message, _ *net.UDPAddr, send func(*message)) {
		reqOID := req.pdu.varbinds[0].GetHeader().OID
		var nextOID OID
		if reqOID.Equal(root) {
			nextOID = root.Append(1)
		} else {
			last := reqOID.At(reqOID.Len() - 1)
			parent, _ := reqOID.Parent()
			nextOID = parent.Append(last + 1)
		}
		send(&message{
			version:   req.version,
			community: req.community,
			pdu: pdu{
				typ:       pduGetResponse,
				requestID: req.pdu.requestID,
				varbinds:  []VarBind{octet(nextOID, "eth")},
			},
		})
	})
	sess := dialWalk(t, agent, WithMaxWalkVars(3))

	count := 0
	w := sess.Walk(context.Background(), root)
	for range w.Iter() {
		count++
	}
	if !errors.Is(w.Err(), ErrMaxWalkVars) {
		t.Fatalf("walk err = %v, want Is ErrMaxWalkVars", w.Err())
	}
	if count != 3 {
		t.Fatalf("yielded %d rows before budget abort, want 3", count)
	}
}
