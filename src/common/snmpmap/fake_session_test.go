package snmpmap_test

import (
	"context"
	"slices"

	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// vbFixture is one OID+VarBind pair fed to fakeSession's pump.
type vbFixture struct {
	oid snmp.OID
	vb  snmp.VarBind
}

// fakeSession is an snmp.Session whose walks replay caller-supplied
// VarBinds, filtered to the walked subtree so one fixture set can serve
// the ifTable, ifXTable, and ifStackTable walks of a single mapper call.
// Get, GetNext, and GetBulk answer from the same fixtures so generated walks
// exercise their real request and merge path.
//
// The pattern is re-declared here rather than imported from
// src/protocol/snmp/test/integration, where the same fake lives in a _test.go
// file that no other package can reach.
type fakeSession struct {
	vbs []vbFixture
}

// Get answers from the same fixtures the walks replay, matched on the
// exact instance OID. A scalar with no fixture returns noSuchObject, as
// an agent that does not implement it would.
func (s *fakeSession) Get(_ context.Context, oids []snmp.OID, _ ...snmp.CallOption) ([]snmp.VarBind, error) {
	out := make([]snmp.VarBind, 0, len(oids))

	for _, o := range oids {
		var vb snmp.VarBind = snmp.NoSuchObjectVar{
			Header: snmp.Header{OID: o, Kind: snmp.KindNoSuchObject},
		}
		for _, f := range s.vbs {
			if f.oid.Compare(o) == 0 {
				vb = f.vb

				break
			}
		}
		out = append(out, vb)
	}

	return out, nil
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

// integerAt builds an INTEGER varbind for the instance of col at idx.
// The column carries its own OID, so a fixture needs no entry constant.
func integerAt(col snmp.AnyColumn, value int32, idx ...uint32) vbFixture {
	oid := col.OID().Append(idx...)

	return vbFixture{oid: oid, vb: snmp.Integer32Var{
		Header: snmp.Header{OID: oid, Kind: snmp.KindInteger32},
		Value:  value,
	}}
}

// stringAt builds an OCTET STRING varbind for the instance of col at idx.
func stringAt(col snmp.AnyColumn, value []byte, idx ...uint32) vbFixture {
	oid := col.OID().Append(idx...)

	return vbFixture{oid: oid, vb: snmp.OctetStringVar{
		Header: snmp.Header{OID: oid, Kind: snmp.KindOctetString},
		Value:  value,
	}}
}

// counter32At builds a Counter32 varbind for the instance of col at idx.
func counter32At(col snmp.AnyColumn, value uint32, idx ...uint32) vbFixture {
	oid := col.OID().Append(idx...)

	return vbFixture{oid: oid, vb: snmp.Counter32Var{
		Header: snmp.Header{OID: oid, Kind: snmp.KindCounter32},
		Value:  value,
	}}
}

// counter64At builds a Counter64 varbind for the instance of col at idx.
func counter64At(col snmp.AnyColumn, value uint64, idx ...uint32) vbFixture {
	oid := col.OID().Append(idx...)

	return vbFixture{oid: oid, vb: snmp.Counter64Var{
		Header: snmp.Header{OID: oid, Kind: snmp.KindCounter64},
		Value:  value,
	}}
}

// gauge32At builds a Gauge32 varbind for the instance of col at idx.
func gauge32At(col snmp.AnyColumn, value uint32, idx ...uint32) vbFixture {
	oid := col.OID().Append(idx...)

	return vbFixture{oid: oid, vb: snmp.Gauge32Var{
		Header: snmp.Header{OID: oid, Kind: snmp.KindGauge32},
		Value:  value,
	}}
}

// objectIDAt builds an OBJECT IDENTIFIER varbind for the instance of col
// at idx.
func objectIDAt(col snmp.AnyColumn, value snmp.OID, idx ...uint32) vbFixture {
	oid := col.OID().Append(idx...)

	return vbFixture{oid: oid, vb: snmp.ObjectIDVar{
		Header: snmp.Header{OID: oid, Kind: snmp.KindObjectID},
		Value:  value,
	}}
}

// bitsAt builds the OCTET STRING varbind a BITS column travels in, with
// the given positions set: position p is bit 7-(p%8) of octet p/8, the
// order RFC 2578 defines.
func bitsAt(col snmp.AnyColumn, positions []uint32, idx ...uint32) vbFixture {
	var octets []byte

	for _, p := range positions {
		for int(p/8) >= len(octets) {
			octets = append(octets, 0)
		}

		octets[p/8] |= 0x80 >> (p % 8)
	}

	return stringAt(col, octets, idx...)
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
