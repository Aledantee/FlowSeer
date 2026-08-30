package snmpmap_test

import (
	"context"
	"slices"

	"go.aledante.io/FlowSeer/src/common/snmp"
)

// vbFixture is one OID+VarBind pair fed to fakeSession's pump.
type vbFixture struct {
	oid snmp.OID
	vb  snmp.VarBind
}

// fakeSession is an snmp.Session whose walks replay caller-supplied
// VarBinds, filtered to the walked subtree so one fixture set can serve
// the ifTable, ifXTable, and ifStackTable walks of a single mapper call.
// Only the walk paths are populated; the mapper issues no other Session
// call.
//
// The pattern is re-declared here rather than imported from
// src/common/snmp/integration, where the same fake lives in a _test.go
// file that no other package can reach.
type fakeSession struct {
	vbs []vbFixture
}

// Get answers from the same fixtures the walks replay, matched on the
// exact instance OID. A scalar with no fixture is simply left out of the
// response, which is how an agent that does not implement it reads to
// the generated getter.
func (s *fakeSession) Get(_ context.Context, oids []snmp.OID, _ ...snmp.CallOption) ([]snmp.VarBind, error) {
	out := make([]snmp.VarBind, 0, len(oids))

	for _, o := range oids {
		for _, f := range s.vbs {
			if f.oid.Compare(o) == 0 {
				out = append(out, f.vb)

				break
			}
		}
	}

	return out, nil
}

func (s *fakeSession) GetNext(context.Context, []snmp.OID, ...snmp.CallOption) ([]snmp.VarBind, error) {
	return nil, nil
}

func (s *fakeSession) GetBulk(context.Context, uint8, uint8, []snmp.OID, ...snmp.CallOption) ([]snmp.VarBind, error) {
	return nil, nil
}

func (s *fakeSession) Set(context.Context, []snmp.VarBind, ...snmp.CallOption) ([]snmp.VarBind, error) {
	return nil, nil
}

func (s *fakeSession) Close() error { return nil }

func (s *fakeSession) Walk(ctx context.Context, root snmp.OID, _ ...snmp.CallOption) *snmp.Walker {
	return s.pump(ctx, root)
}

func (s *fakeSession) BulkWalk(ctx context.Context, root snmp.OID, _ ...snmp.CallOption) *snmp.Walker {
	return s.pump(ctx, root)
}

func (s *fakeSession) BulkWalkRaw(ctx context.Context, root snmp.OID, opts ...snmp.CallOption) *snmp.RawWalker {
	return snmp.RawWalkerFromWalker(ctx, s.BulkWalk(ctx, root, opts...))
}

// pump replays the fixtures of the walked subtree in OID order, the
// order a real agent answers a walk in and the one the generated walkers
// rely on to detect the end of a subtree.
func (s *fakeSession) pump(ctx context.Context, root snmp.OID) *snmp.Walker {
	subtree := s.subtree(root)

	w := snmp.NewWalker(ctx, 64)
	w.Pump(func(_ context.Context) {
		for _, f := range subtree {
			if !w.Send(f.oid, f.vb) {
				return
			}
		}
	})

	return w
}

// subtree is the fixtures under root in OID order, the order a real agent
// answers a walk in and the one the generated walkers rely on to detect
// the end of a subtree.
func (s *fakeSession) subtree(root snmp.OID) []vbFixture {
	out := make([]vbFixture, 0, len(s.vbs))

	for _, f := range s.vbs {
		if f.oid.HasPrefix(root) {
			out = append(out, f)
		}
	}

	slices.SortFunc(out, func(a, b vbFixture) int { return a.oid.Compare(b.oid) })

	return out
}

var (
	ifEntry      = snmp.MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1)
	ifXEntry     = snmp.MustOID(1, 3, 6, 1, 2, 1, 31, 1, 1, 1)
	ifStackEntry = snmp.MustOID(1, 3, 6, 1, 2, 1, 31, 1, 2, 1)
)

// integerVar builds an INTEGER varbind for column col of row idx under entry.
func integerVar(entry snmp.OID, col, idx uint32, value int32) vbFixture {
	oid := entry.Append(col, idx)

	return vbFixture{oid: oid, vb: snmp.Integer32Var{
		Header: snmp.Header{OID: oid, Kind: snmp.KindInteger32},
		Value:  value,
	}}
}

// stringVar builds an OCTET STRING varbind for column col of row idx under entry.
func stringVar(entry snmp.OID, col, idx uint32, value []byte) vbFixture {
	oid := entry.Append(col, idx)

	return vbFixture{oid: oid, vb: snmp.OctetStringVar{
		Header: snmp.Header{OID: oid, Kind: snmp.KindOctetString},
		Value:  value,
	}}
}

// counter32Var builds a Counter32 varbind for column col of row idx under entry.
func counter32Var(entry snmp.OID, col, idx uint32, value uint32) vbFixture {
	oid := entry.Append(col, idx)

	return vbFixture{oid: oid, vb: snmp.Counter32Var{
		Header: snmp.Header{OID: oid, Kind: snmp.KindCounter32},
		Value:  value,
	}}
}

// counter64Var builds a Counter64 varbind for column col of row idx under entry.
func counter64Var(entry snmp.OID, col, idx uint32, value uint64) vbFixture {
	oid := entry.Append(col, idx)

	return vbFixture{oid: oid, vb: snmp.Counter64Var{
		Header: snmp.Header{OID: oid, Kind: snmp.KindCounter64},
		Value:  value,
	}}
}

// stackVar builds the ifStackStatus varbind declaring that interface
// higher runs over interface lower.
func stackVar(higher, lower uint32) vbFixture {
	oid := ifStackEntry.Append(3, higher, lower)

	return vbFixture{oid: oid, vb: snmp.Integer32Var{
		Header: snmp.Header{OID: oid, Kind: snmp.KindInteger32},
		Value:  int32(snmp.RowStatusActive),
	}}
}
