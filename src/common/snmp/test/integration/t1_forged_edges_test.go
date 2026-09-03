//go:build snmp_integration_t1

package integration

import (
	"context"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/snmp"
)

// Forged-edge OID subtrees registered via snmpd.conf's pass directives.
// Each script under testdata/snmpd/pass-scripts/ emits the wire shape
// the library is expected to surface to the caller.
var (
	forgedNoSuchInstance     = snmp.MustOID(1, 3, 6, 1, 4, 1, 99999, 1, 0)
	forgedCounter32Boundary  = snmp.MustOID(1, 3, 6, 1, 4, 1, 99999, 3, 0)
	forgedMalformedDateTime  = snmp.MustOID(1, 3, 6, 1, 4, 1, 99999, 4, 0)
	forgedOversizedOctet     = snmp.MustOID(1, 3, 6, 1, 4, 1, 99999, 5, 0)
	forgedMidTableTruncation = snmp.MustOID(1, 3, 6, 1, 4, 1, 99999, 6)

	// forgedUnregisteredOID sits in the FlowSeer experimental subtree
	// but at an instance no pass directive registers; snmpd surfaces
	// NoSuchObject for an unregistered MIB subtree.
	forgedUnregisteredOID = snmp.MustOID(1, 3, 6, 1, 4, 1, 99999, 99, 0)
)

// TestT1_Forged_NoSuchInstance pins the NoSuchInstance variant: a
// GET on an OID inside a pass-registered subtree where the script
// emits no value surfaces as KindNoSuchInstance to the caller.
func TestT1_Forged_NoSuchInstance(t *testing.T) {
	sess := t1DialV2c(t)
	vbs, err := sess.Get(context.Background(), []snmp.OID{forgedNoSuchInstance})
	if err != nil {
		t.Fatalf("Get forgedNoSuchInstance: %v", err)
	}
	if len(vbs) != 1 {
		t.Fatalf("got %d varbinds, want 1", len(vbs))
	}
	if k := vbs[0].GetHeader().Kind; k != snmp.KindNoSuchInstance {
		t.Errorf("kind = %s, want KindNoSuchInstance", k)
	}
}

// TestT1_Forged_NoSuchObject pins the NoSuchObject variant: a GET
// on an OID outside any registered MIB subtree surfaces as
// KindNoSuchObject. The OID sits inside the FlowSeer enterprise
// space at an instance no pass directive registers.
func TestT1_Forged_NoSuchObject(t *testing.T) {
	sess := t1DialV2c(t)
	vbs, err := sess.Get(context.Background(), []snmp.OID{forgedUnregisteredOID})
	if err != nil {
		t.Fatalf("Get forgedUnregisteredOID: %v", err)
	}
	if len(vbs) != 1 {
		t.Fatalf("got %d varbinds, want 1", len(vbs))
	}
	if k := vbs[0].GetHeader().Kind; k != snmp.KindNoSuchObject {
		t.Errorf("kind = %s, want KindNoSuchObject", k)
	}
}

// TestT1_Forged_EndOfMibView pins the EndOfMibView variant.
// A GetBulk past every registered MIB subtree forces snmpd to fill
// the response PDU with EndOfMibView exception varbinds, which the
// library must surface as KindEndOfMibView.
//
// The start OID must sit past .1.3.6.1.6.* (SNMP framework MIBs are
// registered there even when not explicitly loaded), past .1.3.6.1.4.*
// (enterprise MIBs including net-snmp's own .8072 and our .99999),
// and past every other arc snmpd implements. Picking .1.3.6.1.999
// puts us well past .1.3.6.1.6.* and snmpd has nothing under .7+ in
// the default module set.
func TestT1_Forged_EndOfMibView(t *testing.T) {
	sess := t1DialV2c(t)
	start := snmp.MustOID(1, 3, 6, 1, 999)
	vbs, err := sess.GetBulk(context.Background(), 0, 5, []snmp.OID{start})
	if err != nil {
		t.Fatalf("GetBulk past MIB view: %v", err)
	}
	seenEOM := false
	for _, vb := range vbs {
		if vb.GetHeader().Kind == snmp.KindEndOfMibView {
			seenEOM = true
			break
		}
	}
	if !seenEOM {
		var kinds []string
		for _, vb := range vbs {
			kinds = append(kinds, vb.GetHeader().Kind.String())
		}
		t.Errorf("no KindEndOfMibView in %d-varbind response; kinds=%v", len(vbs), kinds)
	}
}

// TestT1_Forged_Counter32Boundary pins the Counter32 boundary
// decode. The pass-script returns Counter32 value 2^32-1 (the wire-
// level upper bound); the library's decoder must surface the typed
// value as Counter32Var with Value=4294967295 — not error out, not
// silently wrap.
//
// This scenario tests boundary-value decode, not wrap-detection
// semantics. Wrap detection is a property
// of an external observer comparing two successive reads; pass-
// scripts are stateless and cannot model it.
func TestT1_Forged_Counter32Boundary(t *testing.T) {
	sess := t1DialV2c(t)
	vbs, err := sess.Get(context.Background(), []snmp.OID{forgedCounter32Boundary})
	if err != nil {
		t.Fatalf("Get forgedCounter32Boundary: %v", err)
	}
	if len(vbs) != 1 {
		t.Fatalf("got %d varbinds, want 1", len(vbs))
	}
	c, ok := vbs[0].(snmp.Counter32Var)
	if !ok {
		t.Fatalf("varbind type = %T, want Counter32Var", vbs[0])
	}
	const wantBoundary = uint32(4294967295)
	if c.Value != wantBoundary {
		t.Errorf("Counter32 value = %d, want %d (boundary 2^32-1)", c.Value, wantBoundary)
	}
}

// TestT1_Forged_MalformedDateAndTime pins the malformed-tc decode.
// The pass-script returns an 8-octet OCTET STRING whose hour field
// is 99 — a RFC 2579 field-range violation. The library's
// DecodeDateAndTime (fixed by commit affa176 to enforce field ranges)
// must surface a typed error rather than a nonsense time.Time.
//
// The error message is checked for the RFC reference rather than via
// errors.Is because DecodeDateAndTime builds the typed error with a
// static message (the RFC citation is preserved verbatim, the offending
// value moves to a structured attribute) and there is no per-field
// sentinel.
func TestT1_Forged_MalformedDateAndTime(t *testing.T) {
	sess := t1DialV2c(t)
	vbs, err := sess.Get(context.Background(), []snmp.OID{forgedMalformedDateTime})
	if err != nil {
		t.Fatalf("Get forgedMalformedDateTime: %v", err)
	}
	if len(vbs) != 1 {
		t.Fatalf("got %d varbinds, want 1", len(vbs))
	}
	_, derr := snmp.DecodeDateAndTime(vbs[0])
	if derr == nil {
		t.Fatal("DecodeDateAndTime succeeded; expected RFC 2579 field-range error")
	}
	if !strings.Contains(derr.Error(), "RFC 2579") {
		t.Errorf("decode error %q does not reference RFC 2579", derr)
	}
}

// TestT1_Forged_OversizedOctetString pins the oversized-string
// decode. The pass-script returns an OCTET STRING >1500 bytes; the
// library must accept the full payload and surface its length to
// the caller without UDP-level truncation. snmpd's default UDP
// transport supports payloads up to the agent's maxMessageSize
// (typically 8192).
func TestT1_Forged_OversizedOctetString(t *testing.T) {
	sess := t1DialV2c(t)
	vbs, err := sess.Get(context.Background(), []snmp.OID{forgedOversizedOctet})
	if err != nil {
		t.Fatalf("Get forgedOversizedOctet: %v", err)
	}
	if len(vbs) != 1 {
		t.Fatalf("got %d varbinds, want 1", len(vbs))
	}
	os, ok := vbs[0].(snmp.OctetStringVar)
	if !ok {
		t.Fatalf("varbind type = %T, want OctetStringVar", vbs[0])
	}
	if len(os.Value) <= 1500 {
		t.Errorf("OCTET STRING length = %d, want > 1500", len(os.Value))
	}
}

// TestT1_Forged_MidTableTruncation pins the walk-with-typed-error
// behavior. The pass-script registered at .99999.6 returns three
// rows as OCTET STRINGs followed by a fourth row with a wire type
// mismatching the column's declared kind (Counter32 where the
// caller expects OctetString). The Walker yields the first three
// rows, then surfaces a non-nil Walker.Err() — a silent truncated
// walk is the failure mode this test protects against.
//
// The library's walker does not pre-validate column types; the
// type-mismatch surfaces during decode at the consumer. For this
// integration test we treat any walk that does not yield exactly
// rows 1..3 as a regression: the agent SHOULD emit at least three
// well-formed OCTET STRINGs before the typed-mismatch row.
func TestT1_Forged_MidTableTruncation(t *testing.T) {
	sess := t1DialV2c(t)
	w := sess.BulkWalk(context.Background(), forgedMidTableTruncation)
	var stringRows int
	var mismatch bool
	for _, vb := range w.Iter() {
		switch vb.GetHeader().Kind {
		case snmp.KindOctetString:
			stringRows++
		case snmp.KindCounter32:
			mismatch = true
		}
	}
	if err := w.Err(); err != nil {
		t.Logf("Walker.Err() = %v (acceptable: terminal mismatch surfaced)", err)
	}
	if stringRows < 3 {
		t.Errorf("got %d OCTET STRING rows, want >= 3 before mismatch", stringRows)
	}
	if !mismatch {
		// The pass-script is deterministic — .99999.6.4 always
		// returns a Counter32. A walker that drops/reorders rows
		// silently makes this `mismatch` flag false; a future
		// regression must not pass with just a log note.
		t.Errorf("no Counter32 row observed; walker may have silently dropped or reordered the mismatched row")
	}
}
