package snmp

import (
	"strings"
	"testing"
)

// Walk/transport verify-row pins (SNMP conformance hardening). These pin
// already-correct behaviors via the misbehaving-responder seam.

// Covers conformance matrix row: walk-leaf-start (gosnmp #170). A walk rooted at
// a scalar leaf node must return that node's instance (.0), not nothing: GetNext
// from the leaf yields the in-subtree successor.
func TestWalk_LeafStart(t *testing.T) {
	leaf := MustOID(1, 3, 6, 1, 2, 1, 1, 1) // sysDescr (no instance)
	inst := leaf.Append(0)                  // sysDescr.0
	agent := startMisbehavingResponder(t, []kv{{inst, octet(inst, "desc")}}, misbehaviorCfg{})
	sess := dialWalk(t, agent)

	got := collectBulkWalk(t, sess, leaf)
	if len(got) != 1 || !got[0].Equal(inst) {
		t.Fatalf("leaf-rooted walk yielded %v, want [%s] (the .0 instance)", got, inst)
	}
}

// Covers conformance matrix row: walk-large-value-hang (gosnmp #408). A BulkWalk
// over a varbind carrying a >1KB OctetString value must complete, not hang.
func TestWalk_LargeValueNoHang(t *testing.T) {
	r := mibRoot()
	big := strings.Repeat("A", 4096) // > 1KB
	mib := []kv{
		{r.Append(1), octet(r.Append(1), "eth1")},
		{r.Append(2), octet(r.Append(2), big)},
		{r.Append(3), octet(r.Append(3), "eth3")},
	}
	agent := startMisbehavingResponder(t, mib, misbehaviorCfg{})
	sess := dialWalk(t, agent)

	got := collectBulkWalk(t, sess, r)
	if len(got) != 3 {
		t.Fatalf("walked %d rows, want 3 (large value must not stall the walk)", len(got))
	}
}

// Covers conformance matrix row: txp-subtree-exit (RFC 3416). When the agent
// returns an OID outside the requested subtree prefix, the walk terminates
// normally (no error) rather than yielding the out-of-subtree row.
func TestWalk_SubtreeExit(t *testing.T) {
	root := MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 2) // ifDescr column
	inSub := root.Append(1)
	outside := MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 3, 1) // ifType column — outside ifDescr
	mib := []kv{
		{inSub, octet(inSub, "eth1")},
		{outside, octet(outside, "ethType")},
	}
	agent := startMisbehavingResponder(t, mib, misbehaviorCfg{})
	sess := dialWalk(t, agent)

	got := collectBulkWalk(t, sess, root) // fails on any walk error
	if len(got) != 1 || !got[0].Equal(inSub) {
		t.Fatalf("walked %v, want [%s] (must stop at the subtree boundary, no error)", got, inSub)
	}
}
