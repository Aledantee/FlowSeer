package integration

import (
	"context"
	"testing"

	"go.aledante.io/FlowSeer/generated/go/mib/ifmib"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// vbFixture is one OID+VarBind pair fed to fakeSession's pump.
type vbFixture struct {
	oid snmp.OID
	vb  snmp.VarBind
}

// fakeSession answers lexicographic GetBulk/GetNext requests from fixtures and
// retains the replay walk path used by watcher tests.
type fakeSession struct {
	vbs []vbFixture
}

func (s *fakeSession) Get(context.Context, []snmp.OID, ...snmp.CallOption) ([]snmp.VarBind, error) {
	return nil, nil
}

func (s *fakeSession) GetNext(ctx context.Context, oids []snmp.OID, opts ...snmp.CallOption) ([]snmp.VarBind, error) {
	return s.GetBulk(ctx, 0, 1, oids, opts...)
}

func (s *fakeSession) GetBulk(ctx context.Context, _ uint8, reps uint8, oids []snmp.OID, _ ...snmp.CallOption) ([]snmp.VarBind, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cur := append([]snmp.OID(nil), oids...)
	var out []snmp.VarBind
	for range reps {
		for i, o := range cur {
			var next *vbFixture
			for j := range s.vbs {
				f := &s.vbs[j]
				if f.oid.Compare(o) > 0 && (next == nil || f.oid.Compare(next.oid) < 0) {
					next = f
				}
			}
			if next == nil {
				out = append(out, snmp.EndOfMibViewVar{Header: snmp.Header{OID: o, Kind: snmp.KindEndOfMibView}})
			} else {
				out = append(out, next.vb)
				cur[i] = next.oid
			}
		}
	}
	return out, nil
}

func (s *fakeSession) Set(context.Context, []snmp.VarBind, ...snmp.CallOption) ([]snmp.VarBind, error) {
	return nil, nil
}
func (s *fakeSession) Close() error { return nil }
func (s *fakeSession) Walk(ctx context.Context, _ snmp.OID, _ ...snmp.CallOption) *snmp.Walker {
	return s.pump(ctx)
}

func (s *fakeSession) BulkWalk(ctx context.Context, _ snmp.OID, _ ...snmp.CallOption) *snmp.Walker {
	return s.pump(ctx)
}

func (s *fakeSession) BulkWalkRaw(ctx context.Context, root snmp.OID, opts ...snmp.CallOption) *snmp.RawWalker {
	return snmp.RawWalkerFromWalker(ctx, s.BulkWalk(ctx, root, opts...))
}

func (s *fakeSession) pump(ctx context.Context) *snmp.Walker {
	w := snmp.NewWalker(ctx, 64)
	w.Pump(func(_ context.Context) {
		for _, f := range s.vbs {
			if !w.Send(f.oid, f.vb) {
				return
			}
		}
	})
	return w
}

// ifEntry is the ifTable.entry prefix.
var ifEntry = snmp.MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1)

// ifRowFixtures produces VarBinds for one ifTable row populating:
//   - ifIndex (col 1)
//   - ifDescr (col 2)
//   - ifType (col 3)
//   - ifAdminStatus (col 7)
//   - ifOperStatus (col 8)
//   - ifOutOctets (col 16) — non-sentinel; used to verify the helper
//     ignores unrequested counter columns rather than blowing up.
func ifRowFixtures(idx uint32, descr string, oper ifmib.IfOperStatusValue) []vbFixture {
	return []vbFixture{
		{
			oid: ifEntry.Append(1, idx),
			vb:  snmp.Integer32Var{Header: snmp.Header{OID: ifEntry.Append(1, idx), Kind: snmp.KindInteger32}, Value: int32(idx)},
		},
		{
			oid: ifEntry.Append(2, idx),
			vb:  snmp.OctetStringVar{Header: snmp.Header{OID: ifEntry.Append(2, idx), Kind: snmp.KindOctetString}, Value: []byte(descr)},
		},
		{
			oid: ifEntry.Append(3, idx),
			vb:  snmp.Integer32Var{Header: snmp.Header{OID: ifEntry.Append(3, idx), Kind: snmp.KindInteger32}, Value: 6}, // ethernetCsmacd
		},
		{
			oid: ifEntry.Append(7, idx),
			vb:  snmp.Integer32Var{Header: snmp.Header{OID: ifEntry.Append(7, idx), Kind: snmp.KindInteger32}, Value: int32(ifmib.IfAdminStatusValueUp)},
		},
		{
			oid: ifEntry.Append(8, idx),
			vb:  snmp.Integer32Var{Header: snmp.Header{OID: ifEntry.Append(8, idx), Kind: snmp.KindInteger32}, Value: int32(oper)},
		},
		{
			oid: ifEntry.Append(16, idx),
			vb:  snmp.Counter32Var{Header: snmp.Header{OID: ifEntry.Append(16, idx), Kind: snmp.KindCounter32}, Value: 4242},
		},
	}
}

// fakeIfTableSession builds a fakeSession populated with three healthy
// ifTable rows. Every fixture columns above is supplied; the helper
// chooses which to request via the AnyColumn variadic, and the codegen
// filters accordingly.
func fakeIfTableSession() *fakeSession {
	var vbs []vbFixture
	vbs = append(vbs, ifRowFixtures(1, "eth0", ifmib.IfOperStatusValueUp)...)
	vbs = append(vbs, ifRowFixtures(2, "eth1", ifmib.IfOperStatusValueDown)...)
	vbs = append(vbs, ifRowFixtures(3, "lo", ifmib.IfOperStatusValueUp)...)
	return &fakeSession{vbs: vbs}
}

// TestAssertIfTableDenseRows_Happy_TwoColumns is the canonical happy
// path: IfDescr + IfOperStatus requested; helper passes.
func TestAssertIfTableDenseRows_Happy_TwoColumns(t *testing.T) {
	AssertIfTableDenseRows(t, fakeIfTableSession(), ifmib.IfDescr, ifmib.IfOperStatus)
}

// TestAssertIfTableDenseRows_Happy_SingleSentinel exercises the
// "single requested column" boundary — proves the helper does not
// require multiple columns to validate the contract.
func TestAssertIfTableDenseRows_Happy_SingleSentinel(t *testing.T) {
	AssertIfTableDenseRows(t, fakeIfTableSession(), ifmib.IfDescr)
}

// TestAssertIfTableDenseRows_Happy_AllSentinels exercises the
// "all sentinel columns requested" path. In this configuration every
// sentinel must surface as non-zero in at least one row; no sentinel
// gets the "unrequested → zero" check. Together with the two-column
// test this covers both halves of the sentinel decision tree.
func TestAssertIfTableDenseRows_Happy_AllSentinels(t *testing.T) {
	AssertIfTableDenseRows(t, fakeIfTableSession(),
		ifmib.IfIndex,
		ifmib.IfDescr,
		ifmib.IfType,
		ifmib.IfAdminStatus,
		ifmib.IfOperStatus,
	)
}

// TestAssertIfTableDenseRows_Happy_NonSentinelRequested confirms the
// helper accepts a requested column that is not in the sentinel set
// (here, IfOutOctets, a counter). The dense-row contract still holds:
// IfOutOctets's fixture value of 4242 is non-zero and the codegen
// decodes it, but the helper does not assert anything about non-
// sentinel columns directly — coverage of those is a per-tier
// concern.
func TestAssertIfTableDenseRows_Happy_NonSentinelRequested(t *testing.T) {
	AssertIfTableDenseRows(t, fakeIfTableSession(), ifmib.IfDescr, ifmib.IfOutOctets)
}
