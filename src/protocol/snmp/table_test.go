package snmp

import (
	"context"
	"errors"
	"fmt"
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

type localTableRow struct {
	Index    OID
	Key      int32
	keyValid bool
	Descr    string
	observed [1]uint64
}

type localTableWalker struct {
	TableWalker[localTableRow]
}

func TestTable_NamedWrapperPromotedReturnType(t *testing.T) {
	entry := MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1)
	colDescr := NewTableColumn[string](entry.Append(2), KindOctetString, func(vb VarBind) (string, error) {
		if os, ok := vb.(OctetStringVar); ok {
			return string(os.Value), nil
		}
		return "", ErrTypeMismatch
	}, 0)

	tbl := NewTable[localTableRow, *localTableWalker](
		"localTable",
		[]AnyColumn{colDescr},
		func(idx OID, r *localTableRow) {
			r.Index = idx
			if idx.Len() == 1 {
				r.Key = int32(idx.At(0))
				r.keyValid = true
			}
		},
		func(r *localTableRow, ordinal int, rv RawVarBind) error {
			if ordinal == 0 {
				return DecodeColumn(rv, colDescr, &r.Descr, r.observed[:])
			}
			return nil
		},
		func(tw TableWalker[localTableRow]) *localTableWalker {
			return &localTableWalker{TableWalker: tw}
		},
	)

	var sess Session = columnScript{call: func(_ context.Context, _ []OID, _ int, _ bool) ([]VarBind, error) {
		return nil, nil
	}}

	takeNamedWalker := func(w *localTableWalker) *localTableWalker { return w }
	w := takeNamedWalker(tbl.Walk(context.Background(), sess, colDescr))
	if w == nil {
		t.Fatal("tbl.Walk returned nil")
	}

	_ = w.Iter()
	if err := w.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
	w.Close()
}

func TestTable_LazyDeduplicatedSelection(t *testing.T) {
	entry := MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1)
	col1 := NewTableColumn[int32](entry.Append(1), KindInteger32, nil, 0)
	col2 := NewTableColumn[string](entry.Append(2), KindOctetString, nil, 1)

	tbl := NewTable[localTableRow, *localTableWalker](
		"localTable",
		[]AnyColumn{col1, col2},
		func(idx OID, r *localTableRow) { r.Index = idx },
		func(_ *localTableRow, _ int, _ RawVarBind) error { return nil },
		func(tw TableWalker[localTableRow]) *localTableWalker {
			return &localTableWalker{TableWalker: tw}
		},
	)

	calls := 0
	var requestedRoots []OID
	sess := columnScript{call: func(_ context.Context, oids []OID, _ int, _ bool) ([]VarBind, error) {
		calls++
		requestedRoots = append(requestedRoots, oids...)
		var out []VarBind
		for _, o := range oids {
			out = append(out, EndOfMibViewVar{Header: Header{OID: o, Kind: KindEndOfMibView}})
		}
		return out, nil
	}}

	w := tbl.Walk(context.Background(), sess, col1, col2, col1)

	if calls != 0 {
		t.Fatalf("calls before Iter = %d, want 0 (lazy)", calls)
	}

	for range w.Iter() {
	}

	if calls == 0 {
		t.Fatal("no calls made during Iter")
	}
	if len(requestedRoots) != 2 {
		t.Fatalf("requestedRoots len = %d, want 2 (deduplicated): %v", len(requestedRoots), requestedRoots)
	}
	if !requestedRoots[0].Equal(col1.OID()) || !requestedRoots[1].Equal(col2.OID()) {
		t.Fatalf("requestedRoots = %v, want [%v, %v]", requestedRoots, col1.OID(), col2.OID())
	}
}

func TestTable_PreIOForeignFailure(t *testing.T) {
	entry := MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1)
	col1 := NewTableColumn[int32](entry.Append(1), KindInteger32, nil, 0)
	foreignCol := NewTableColumn[int32](MustOID(1, 3, 6, 1, 4, 1, 9, 9, 1), KindInteger32, nil, 0)

	tbl := NewTable[localTableRow, *localTableWalker](
		"localTable",
		[]AnyColumn{col1},
		func(idx OID, r *localTableRow) { r.Index = idx },
		func(_ *localTableRow, _ int, _ RawVarBind) error { return nil },
		func(tw TableWalker[localTableRow]) *localTableWalker {
			return &localTableWalker{TableWalker: tw}
		},
	)

	calls := 0
	sess := columnScript{call: func(_ context.Context, _ []OID, _ int, _ bool) ([]VarBind, error) {
		calls++
		return nil, nil
	}}

	w := tbl.Walk(context.Background(), sess, foreignCol)

	if calls != 0 {
		t.Fatalf("calls = %d, want 0 (pre-I/O rejection)", calls)
	}

	for range w.Iter() {
		t.Fatal("Iter yielded rows for foreign column walk")
	}

	if calls != 0 {
		t.Fatalf("calls after Iter = %d, want 0", calls)
	}
	if err := w.Err(); !errors.Is(err, ErrForeignColumn) {
		t.Fatalf("Err() = %v, want ErrForeignColumn", err)
	}
}

func TestTable_EarlyBreakClosesRetrieval(t *testing.T) {
	entry := MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1)
	col1 := NewTableColumn[string](entry.Append(2), KindOctetString, func(vb VarBind) (string, error) {
		if os, ok := vb.(OctetStringVar); ok {
			return string(os.Value), nil
		}
		return "", ErrTypeMismatch
	}, 0)

	tbl := NewTable[localTableRow, *localTableWalker](
		"localTable",
		[]AnyColumn{col1},
		func(idx OID, r *localTableRow) { r.Index = idx },
		func(r *localTableRow, _ int, rv RawVarBind) error {
			return DecodeColumn(rv, col1, &r.Descr, r.observed[:])
		},
		func(tw TableWalker[localTableRow]) *localTableWalker {
			return &localTableWalker{TableWalker: tw}
		},
	)

	sess := columnScript{call: func(_ context.Context, oids []OID, _ int, _ bool) ([]VarBind, error) {
		cur := oids[0]
		var out []VarBind
		for i := 1; i <= 5; i++ {
			next := col1.OID().Child(uint32(i))
			if next.Compare(cur) > 0 {
				out = append(out, octet(next, "eth"+string(rune('0'+i))))
			}
		}
		return out, nil
	}}

	w := tbl.Walk(context.Background(), sess, col1)
	yielded := 0
	for range w.Iter() {
		yielded++
		break
	}

	if yielded != 1 {
		t.Fatalf("yielded = %d, want 1", yielded)
	}
	if err := w.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil on clean early break", err)
	}
}

func TestTable_MalformedKeysRemainRows(t *testing.T) {
	entry := MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1)
	col1 := NewTableColumn[string](entry.Append(2), KindOctetString, func(vb VarBind) (string, error) {
		if os, ok := vb.(OctetStringVar); ok {
			return string(os.Value), nil
		}
		return "", ErrTypeMismatch
	}, 0)

	tbl := NewTable[localTableRow, *localTableWalker](
		"localTable",
		[]AnyColumn{col1},
		func(idx OID, r *localTableRow) {
			r.Index = idx
			if idx.Len() == 1 {
				r.Key = int32(idx.At(0))
				r.keyValid = true
			} else {
				r.Key = 0
				r.keyValid = false
			}
		},
		func(r *localTableRow, _ int, rv RawVarBind) error {
			return DecodeColumn(rv, col1, &r.Descr, r.observed[:])
		},
		func(tw TableWalker[localTableRow]) *localTableWalker {
			return &localTableWalker{TableWalker: tw}
		},
	)

	items := []VarBind{
		octet(col1.OID().Append(7, 99), "badKey"),
		octet(col1.OID().Append(8), "goodKey"),
	}
	sess := columnScript{call: func(_ context.Context, oids []OID, _ int, _ bool) ([]VarBind, error) {
		cur := oids[0]
		var out []VarBind
		for _, item := range items {
			if item.GetHeader().OID.Compare(cur) > 0 {
				out = append(out, item)
			}
		}
		if len(out) == 0 {
			out = append(out, EndOfMibViewVar{Header: Header{OID: cur, Kind: KindEndOfMibView}})
		}
		return out, nil
	}}

	w := tbl.Walk(context.Background(), sess, col1)
	var rows []localTableRow
	for _, row := range w.Iter() {
		rows = append(rows, row)
	}
	if err := w.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2 (malformed index must remain a row)", len(rows))
	}
	if rows[0].keyValid || rows[0].Key != 0 || rows[0].Descr != "badKey" {
		t.Errorf("row[0] malformed key mismatch: %+v", rows[0])
	}
	if !rows[1].keyValid || rows[1].Key != 8 || rows[1].Descr != "goodKey" {
		t.Errorf("row[1] valid key mismatch: %+v", rows[1])
	}
}

func TestTable_DecodeFailureRetainsDeliveredPrefix(t *testing.T) {
	entry := MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1)
	col1 := NewTableColumn[string](entry.Append(2), KindOctetString, func(vb VarBind) (string, error) {
		if os, ok := vb.(OctetStringVar); ok {
			if string(os.Value) == "FAIL" {
				return "", errors.New("decode failure")
			}
			return string(os.Value), nil
		}
		return "", ErrTypeMismatch
	}, 0)

	tbl := NewTable[localTableRow, *localTableWalker](
		"localTable",
		[]AnyColumn{col1},
		func(idx OID, r *localTableRow) { r.Index = idx },
		func(r *localTableRow, _ int, rv RawVarBind) error {
			return DecodeColumn(rv, col1, &r.Descr, r.observed[:])
		},
		func(tw TableWalker[localTableRow]) *localTableWalker {
			return &localTableWalker{TableWalker: tw}
		},
	)

	sess := columnScript{call: func(_ context.Context, _ []OID, _ int, _ bool) ([]VarBind, error) {
		return []VarBind{
			octet(col1.OID().Child(1), "eth1"),
			octet(col1.OID().Child(2), "FAIL"),
			octet(col1.OID().Child(3), "eth3"),
		}, nil
	}}

	w := tbl.Walk(context.Background(), sess, col1)
	var rows []localTableRow
	for _, row := range w.Iter() {
		rows = append(rows, row)
	}

	if len(rows) != 1 {
		t.Fatalf("delivered rows = %d, want 1 (only delivered prefix before error)", len(rows))
	}
	if rows[0].Descr != "eth1" {
		t.Errorf("row[0].Descr = %q, want eth1", rows[0].Descr)
	}
	if w.Err() == nil {
		t.Fatal("w.Err() = nil, want decode error")
	}
}

type sparseTableRow struct {
	Index    OID
	A, B     string
	observed [1]uint64
}

type sparseTableWalker struct {
	TableWalker[sparseTableRow]
}

func TestTable_RetainedRowsSurviveLaterRows(t *testing.T) {
	entry := MustOID(1, 3, 6, 1, 4, 1, 999, 1)
	decodeString := func(vb VarBind) (string, error) {
		if os, ok := vb.(OctetStringVar); ok {
			return string(os.Value), nil
		}
		return "", ErrTypeMismatch
	}
	colA := NewTableColumn[string](entry.Child(1), KindOctetString, decodeString, 0)
	colB := NewTableColumn[string](entry.Child(2), KindOctetString, decodeString, 1)
	cols := []AnyColumn{colA, colB}
	tbl := NewTable[sparseTableRow, *sparseTableWalker](
		"sparseTable",
		cols,
		func(idx OID, r *sparseTableRow) { r.Index = idx },
		func(r *sparseTableRow, ordinal int, rv RawVarBind) error {
			switch ordinal {
			case 0:
				return DecodeColumn(rv, colA, &r.A, r.observed[:])
			case 1:
				return DecodeColumn(rv, colB, &r.B, r.observed[:])
			}
			return nil
		},
		func(tw TableWalker[sparseTableRow]) *sparseTableWalker {
			return &sparseTableWalker{TableWalker: tw}
		},
	)

	// Row 1 answers both columns and row 2 only A, so a row buffer that
	// carried state between rows would leak row 1's B into row 2.
	roots := []OID{colA.OID(), colB.OID()}
	data := [][]uint32{{1, 2}, {1}}
	sess := columnScript{call: func(_ context.Context, oids []OID, reps int, _ bool) ([]VarBind, error) {
		cur := append([]OID(nil), oids...)
		var out []VarBind
		for range reps {
			for i, o := range cur {
				var vb VarBind = EndOfMibViewVar{Header: Header{OID: o, Kind: KindEndOfMibView}}
				for col, root := range roots {
					if !o.HasPrefix(root) {
						continue
					}
					for _, index := range data[col] {
						if next := root.Child(index); next.Compare(o) > 0 {
							vb = octet(next, fmt.Sprintf("c%d.%d", col, index))
							break
						}
					}
				}
				cur[i] = vb.GetHeader().OID
				out = append(out, vb)
			}
		}
		return out, nil
	}}

	snapshot := func(r sparseTableRow) string {
		return fmt.Sprintf("%s %q %q %v", r.Index, r.A, r.B, r.observed)
	}
	w := tbl.Walk(context.Background(), sess, colA, colB)
	var rows []sparseTableRow
	var atYield []string
	for _, row := range w.Iter() {
		rows = append(rows, row)
		atYield = append(atYield, snapshot(row))
	}
	if err := w.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(rows))
	}
	for i, row := range rows {
		if got := snapshot(row); got != atYield[i] {
			t.Errorf("row %d changed after later rows: got %s, yielded %s", i, got, atYield[i])
		}
	}
	if rows[0].B != "c1.1" || !ColumnObserved(rows[0].observed[:], cols, colB) {
		t.Errorf("row 1 B = %q, observed %v; want c1.1 observed", rows[0].B, ColumnObserved(rows[0].observed[:], cols, colB))
	}
	if rows[1].B != "" || ColumnObserved(rows[1].observed[:], cols, colB) {
		t.Errorf("row 2 B = %q, observed %v; want empty and unobserved", rows[1].B, ColumnObserved(rows[1].observed[:], cols, colB))
	}
}
