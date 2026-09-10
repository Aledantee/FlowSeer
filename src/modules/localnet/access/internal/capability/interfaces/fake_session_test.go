package interfaces_test

import (
	"context"
	"slices"

	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// vbFixture is one OID+VarBind pair fed to fakeSession's pump. The type
// and fakeSession below are the same pattern
// src/modules/localnet/snmpmap/fake_session_test.go uses, re-declared here
// because that file is package snmpmap_test and this package cannot import
// a _test.go file from another package.
type vbFixture struct {
	oid snmp.OID
	vb  snmp.VarBind
}

type fakeSession struct {
	vbs []vbFixture
}

func (s *fakeSession) Get(_ context.Context, oids []snmp.OID, _ ...snmp.CallOption) ([]snmp.VarBind, error) {
	out := make([]snmp.VarBind, 0, len(oids))

	for _, o := range oids {
		var vb snmp.VarBind = snmp.NoSuchObjectVar{Header: snmp.Header{OID: o, Kind: snmp.KindNoSuchObject}}

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

var ifXEntry = snmp.MustOID(1, 3, 6, 1, 2, 1, 31, 1, 1, 1)

func integerVar(entry snmp.OID, col, idx uint32, value int32) vbFixture {
	oid := entry.Append(col, idx)

	return vbFixture{oid: oid, vb: snmp.Integer32Var{Header: snmp.Header{OID: oid, Kind: snmp.KindInteger32}, Value: value}}
}

func stringVar(entry snmp.OID, col, idx uint32, value []byte) vbFixture {
	oid := entry.Append(col, idx)

	return vbFixture{oid: oid, vb: snmp.OctetStringVar{Header: snmp.Header{OID: oid, Kind: snmp.KindOctetString}, Value: value}}
}
