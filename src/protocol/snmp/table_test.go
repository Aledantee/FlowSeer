package snmp

import (
	"context"
	"errors"
	"testing"
)

// probeSession answers every GetNext with one scripted reply.
type probeSession struct {
	Session
	reply []VarBind
	err   error
	asked []OID
}

func (s *probeSession) GetNext(_ context.Context, oids []OID, _ ...CallOption) ([]VarBind, error) {
	s.asked = append(s.asked, oids...)
	return s.reply, s.err
}

func TestTableDescriptor_Present(t *testing.T) {
	root := MustOID(1, 3, 6, 1, 2, 1, 2, 2)
	desc := TableDescriptor{Root: root, KeyType: "IfIndex"}

	tests := []struct {
		name  string
		reply VarBind
		want  bool
	}{
		{"first instance under root", Integer32Var{Header: Header{OID: root.Append(1, 1, 1), Kind: KindInteger32}, Value: 1}, true},
		{"reply outside root", Integer32Var{Header: Header{OID: MustOID(1, 3, 6, 1, 2, 1, 3, 1), Kind: KindInteger32}, Value: 1}, false},
		{"end of MIB", EndOfMibViewVar{Header: Header{OID: root, Kind: KindEndOfMibView}}, false},
		{"root itself is not an instance", Integer32Var{Header: Header{OID: root, Kind: KindInteger32}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sess := &probeSession{reply: []VarBind{tc.reply}}
			got, err := desc.Present(context.Background(), sess)
			if err != nil {
				t.Fatalf("Present error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("Present = %v, want %v", got, tc.want)
			}
			if len(sess.asked) != 1 || !sess.asked[0].Equal(root) {
				t.Fatalf("GetNext asked %v, want exactly the root", sess.asked)
			}
		})
	}
}

func TestTableDescriptor_PresentErrors(t *testing.T) {
	desc := TableDescriptor{Root: MustOID(1, 3, 6, 1, 2, 1, 2, 2)}
	boom := errors.New("boom")
	if _, err := desc.Present(context.Background(), &probeSession{err: boom}); !errors.Is(err, boom) {
		t.Fatalf("transport error not propagated: %v", err)
	}
	if _, err := desc.Present(context.Background(), &probeSession{}); err == nil {
		t.Fatal("an empty reply must be an error, not an absent table")
	}
}

func TestTableDescriptor_Indicator(t *testing.T) {
	root := MustOID(1, 3, 6, 1, 2, 1, 2, 2)
	if (TableDescriptor{Root: root}).HasIndicator() {
		t.Fatal("zero indicator reported as present")
	}
	ind, err := NewScalarIndicator(MustOID(1, 3, 6, 1, 2, 1, 2, 1), KindTimeTicks, []OID{root})
	if err != nil {
		t.Fatal(err)
	}
	if !(TableDescriptor{Root: root, Indicator: ind}).HasIndicator() {
		t.Fatal("scalar indicator not reported")
	}
}

// presentTables is the consumer shape the descriptor exists for: a
// collector probing tables from many generated packages through one slice
// without naming any row type.
func presentTables(ctx context.Context, sess Session, descs []TableDescriptor) ([]bool, error) {
	out := make([]bool, len(descs))
	for i, d := range descs {
		present, err := d.Present(ctx, sess)
		if err != nil {
			return nil, err
		}
		out[i] = present
	}
	return out, nil
}

func TestTableDescriptor_ProbeLoop(t *testing.T) {
	a := MustOID(1, 3, 6, 1, 2, 1, 2, 2)
	b := MustOID(1, 3, 6, 1, 4, 1, 9, 9, 46, 1, 3, 1)
	sess := &probeSession{reply: []VarBind{Integer32Var{Header: Header{OID: a.Append(1, 1, 1), Kind: KindInteger32}}}}
	got, err := presentTables(context.Background(), sess, []TableDescriptor{{Root: a, KeyType: "IfIndex"}, {Root: b, KeyType: "VlanIndex"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[0] || got[1] {
		t.Fatalf("presentTables = %v", got)
	}
}
