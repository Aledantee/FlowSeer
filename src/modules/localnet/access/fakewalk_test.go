package access_test

import (
	"context"
	"slices"

	"go.aledante.io/FlowSeer/generated/go/mib/ifmib"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// walkingSession is an SNMP device that answers the identity probe and an
// interface walk, which is what a real read needs and what
// fakeIdentitySession — whose walks return nothing — cannot give.
//
// The pump is the same pattern
// src/modules/localnet/access/internal/capability/interfaces/fake_session_test.go
// uses, re-declared here because a _test.go file in another package cannot
// be imported. Get is not: it answers positionally the way the identity
// probe's fixture does, so a device built here has the fingerprint every
// other fixture device has.
type walkingSession struct {
	vbs []vbFixture
}

// vbFixture is one OID and the VarBind served at it.
type vbFixture struct {
	oid snmp.OID
	vb  snmp.VarBind
}

var (
	ifEntry  = snmp.MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1)
	ifXEntry = snmp.MustOID(1, 3, 6, 1, 2, 1, 31, 1, 1, 1)
)

// interfaceRows is a device serving one ethernet interface, up in both
// senses. alias is its ifAlias, which is what an InterfaceObservation's
// description is read from; unaliased leaves that column unobserved, which
// is the whole difference between a COMPLETE SNMP read and a PARTIAL one.
func interfaceRows(name, alias string, aliased bool) []vbFixture {
	rows := []vbFixture{
		integerVar(ifEntry, 1, 1),
		stringVar(ifEntry, 2, []byte(name)),
		integerVar(ifEntry, 3, 6),
		integerVar(ifEntry, 7, int32(ifmib.IfAdminStatusValueUp)),
		integerVar(ifEntry, 8, int32(ifmib.IfOperStatusValueUp)),
	}
	if aliased {
		rows = append(rows, stringVar(ifXEntry, 18, []byte(alias)))
	}
	return rows
}

// ifIndex is the one interface every device built here serves. A second row
// would need the walk to order them, which nothing this file tests needs.
const ifIndex uint32 = 1

func integerVar(entry snmp.OID, col uint32, value int32) vbFixture {
	oid := entry.Append(col, ifIndex)
	return vbFixture{oid: oid, vb: snmp.Integer32Var{Header: snmp.Header{OID: oid, Kind: snmp.KindInteger32}, Value: value}}
}

func stringVar(entry snmp.OID, col uint32, value []byte) vbFixture {
	oid := entry.Append(col, ifIndex)
	return vbFixture{oid: oid, vb: snmp.OctetStringVar{Header: snmp.Header{OID: oid, Kind: snmp.KindOctetString}, Value: value}}
}

// Get answers the identity probe exactly as fakeIdentitySession does.
func (s *walkingSession) Get(ctx context.Context, oids []snmp.OID, opts ...snmp.CallOption) ([]snmp.VarBind, error) {
	return fakeIdentitySession{onGet: func() {}}.Get(ctx, oids, opts...)
}

func (s *walkingSession) GetNext(ctx context.Context, oids []snmp.OID, opts ...snmp.CallOption) ([]snmp.VarBind, error) {
	return s.GetBulk(ctx, 0, 1, oids, opts...)
}

func (s *walkingSession) GetBulk(ctx context.Context, _ uint8, reps uint8, oids []snmp.OID, _ ...snmp.CallOption) ([]snmp.VarBind, error) {
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
				continue
			}
			out = append(out, next.vb)
			cur[i] = next.oid
		}
	}
	return out, nil
}

func (s *walkingSession) Set(context.Context, []snmp.VarBind, ...snmp.CallOption) ([]snmp.VarBind, error) {
	return nil, nil
}

func (s *walkingSession) Close() error { return nil }

func (s *walkingSession) Walk(ctx context.Context, root snmp.OID, _ ...snmp.CallOption) *snmp.Walker {
	return s.pump(ctx, root)
}

func (s *walkingSession) BulkWalk(ctx context.Context, root snmp.OID, _ ...snmp.CallOption) *snmp.Walker {
	return s.pump(ctx, root)
}

func (s *walkingSession) BulkWalkRaw(ctx context.Context, root snmp.OID, opts ...snmp.CallOption) *snmp.RawWalker {
	return snmp.RawWalkerFromWalker(ctx, s.BulkWalk(ctx, root, opts...))
}

func (s *walkingSession) pump(ctx context.Context, root snmp.OID) *snmp.Walker {
	subtree := make([]vbFixture, 0, len(s.vbs))
	for _, f := range s.vbs {
		if f.oid.HasPrefix(root) {
			subtree = append(subtree, f)
		}
	}
	slices.SortFunc(subtree, func(a, b vbFixture) int { return a.oid.Compare(b.oid) })

	w := snmp.NewWalker(ctx, 64)
	w.Pump(func(context.Context) {
		for _, f := range subtree {
			if !w.Send(f.oid, f.vb) {
				return
			}
		}
	})
	return w
}
