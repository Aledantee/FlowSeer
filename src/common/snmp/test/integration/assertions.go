package integration

import (
	"context"
	"testing"

	"go.aledante.io/FlowSeer/generated/go/mib/ifmib"
	"go.aledante.io/FlowSeer/src/common/snmp"
)

// ifTableSentinel describes one ifTable column whose value is RFC-2863-
// mandated non-zero on every legitimate row. Sentinels back the dense-
// row assertion: requested → at least one row non-zero; unrequested →
// every row zero.
type ifTableSentinel struct {
	name   string
	col    snmp.AnyColumn
	isZero func(ifmib.IfTableRow) bool
}

// ifTableSentinels enumerates the columns [AssertIfTableDenseRows]
// uses to verify the dense-row contract.
//
// Each entry is a column whose presence on every real ifTable row is
// mandated by RFC 2863, so the "non-zero in at least one row when
// requested, zero in every row when unrequested" check is well-defined.
// Counter-shaped columns (IfInOctets, IfOutOctets, etc.) are
// intentionally excluded: a healthy interface that has not yet forwarded
// traffic legitimately reports zero, making the "non-zero when
// requested" half of the contract unverifiable for them.
var ifTableSentinels = []ifTableSentinel{
	{"IfIndex", ifmib.IfIndex, func(r ifmib.IfTableRow) bool { return r.IfIndex == 0 }},
	{"IfDescr", ifmib.IfDescr, func(r ifmib.IfTableRow) bool { return r.IfDescr == "" }},
	{"IfType", ifmib.IfType, func(r ifmib.IfTableRow) bool { return r.IfType == 0 }},
	{"IfAdminStatus", ifmib.IfAdminStatus, func(r ifmib.IfTableRow) bool { return r.IfAdminStatus == 0 }},
	{"IfOperStatus", ifmib.IfOperStatus, func(r ifmib.IfTableRow) bool { return r.IfOperStatus == 0 }},
}

// AssertIfTableDenseRows walks IfTable via sess with the supplied
// requested columns and asserts the dense-row contract: only requested
// columns reach the row struct.
//
// The contract is checked at two levels:
//
//   - Walker termination: [ifmib.IfTableWalker.Err] returns nil and at
//     least one row is yielded. A walk that fails terminally or
//     returns zero rows fails the helper with a diagnostic;
//     "vacuously passing" is not an outcome.
//
//   - Per sentinel column ([ifTableSentinels]):
//
//   - If the column is in `requested`: at least one yielded row has
//     a non-zero value for it. This is the necessary-but-not-
//     sufficient signal that the column reached the row struct at all.
//
//   - If the column is NOT in `requested`: every yielded row has a
//     zero value for it. This is the dense-row contract proper — a
//     codegen regression that decoded VarBinds outside `byCol` would
//     surface here.
//
// Counter-shaped columns where zero is a legitimate reading
// ([ifmib.IfInOctets], [ifmib.IfOutOctets], …) are not in the sentinel
// set; callers wanting to assert against counters supply their own
// per-test predicates.
//
// To assert the dense-row contract on IfXTable columns
// ([ifmib.IfHCInOctets] and the rest of the 64-bit HC counter family),
// make a separate [ifmib.IfXTable.Walk] call. The two tables share an
// Index space but live in distinct OID subtrees, so a single Walk
// cannot satisfy both.
func AssertIfTableDenseRows(t *testing.T, sess snmp.Session, requested ...snmp.AnyColumn) {
	t.Helper()
	if sess == nil {
		t.Fatal("AssertIfTableDenseRows: sess is nil")
	}
	if len(requested) == 0 {
		t.Fatal("AssertIfTableDenseRows: no columns requested; the assertion is meaningless with an empty column set")
	}

	requestedSet := make(map[uint32]struct{}, len(requested))
	for _, c := range requested {
		o := c.OID()
		if o.Len() == 0 {
			t.Fatalf("AssertIfTableDenseRows: requested column has empty OID: %T", c)
		}
		requestedSet[o.At(o.Len()-1)] = struct{}{}
	}

	tw := ifmib.IfTable.Walk(context.Background(), sess, requested...)

	var rows []ifmib.IfTableRow
	for _, row := range tw.Iter() {
		rows = append(rows, row)
	}
	if err := tw.Err(); err != nil {
		t.Fatalf("AssertIfTableDenseRows: Walker.Err() = %v, want nil", err)
	}
	if len(rows) == 0 {
		t.Fatal("AssertIfTableDenseRows: walk yielded zero rows; agent has no ifTable entries or the walk did not reach the wire")
	}

	for _, s := range ifTableSentinels {
		colOID := s.col.OID()
		colNum := colOID.At(colOID.Len() - 1)
		_, isRequested := requestedSet[colNum]
		if isRequested {
			anyNonZero := false
			for _, r := range rows {
				if !s.isZero(r) {
					anyNonZero = true
					break
				}
			}
			if !anyNonZero {
				t.Errorf("AssertIfTableDenseRows: %s is in requested set but every row has it at zero (%d rows checked)", s.name, len(rows))
			}
			continue
		}
		for i, r := range rows {
			if !s.isZero(r) {
				t.Errorf("AssertIfTableDenseRows: row %d has %s populated, but it was not in the requested column set (dense-row contract violated)", i, s.name)
			}
		}
	}
}
