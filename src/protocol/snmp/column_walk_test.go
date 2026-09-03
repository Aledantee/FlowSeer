package snmp

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

type columnScript struct {
	Session
	call func(context.Context, []OID, int, bool) ([]VarBind, error)
}

func (s columnScript) GetBulk(ctx context.Context, _ uint8, reps uint8, oids []OID, _ ...CallOption) ([]VarBind, error) {
	return s.call(ctx, oids, int(reps), false)
}

func (s columnScript) GetNext(ctx context.Context, oids []OID, _ ...CallOption) ([]VarBind, error) {
	return s.call(ctx, oids, 1, true)
}

func TestColumnWalkSparseAndStop(t *testing.T) {
	roots := []OID{MustOID(1, 3, 6, 1, 4, 1, 999, 1), MustOID(1, 3, 6, 1, 4, 1, 999, 2)}
	data := [][]uint32{{1, 3, 7}, {1, 2, 7}}
	for _, stop := range []bool{false, true} {
		calls := 0
		sess := columnScript{call: func(_ context.Context, oids []OID, reps int, _ bool) ([]VarBind, error) {
			calls++
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
								vb = octet(next, next.String())
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
		w := WalkColumns(context.Background(), sess, roots, TableWalkOptions{MaxRepetitions: 2})
		if calls != 0 {
			t.Fatal("eager request")
		}
		var indexes []string
		var columns [][]int
		for idx, cells := range w.Iter() {
			indexes = append(indexes, idx.String())
			var cols []int
			for _, c := range cells {
				cols = append(cols, c.Column)
			}
			columns = append(columns, cols)
			if stop {
				break
			}
		}
		if w.Err() != nil {
			t.Fatal(w.Err())
		}
		if stop {
			if calls != 1 {
				t.Fatalf("early calls = %d", calls)
			}
			continue
		}
		if !reflect.DeepEqual(indexes, []string{"1", "2", "3", "7"}) || !reflect.DeepEqual(columns, [][]int{{0, 1}, {1}, {0}, {0, 1}}) {
			t.Fatalf("rows %v %v", indexes, columns)
		}
	}
}

func TestColumnWalkNoProgress(t *testing.T) {
	calls := 0
	sess := columnScript{call: func(_ context.Context, _ []OID, _ int, next bool) ([]VarBind, error) {
		calls++
		if next != (calls == 2) {
			t.Fatalf("request %d GETNEXT=%v", calls, next)
		}
		return nil, nil
	}}
	w := WalkColumns(context.Background(), sess, []OID{MustOID(1, 3, 6)}, TableWalkOptions{})
	for range w.Iter() {
		t.Fatal("unexpected row")
	}
	if !errors.Is(w.Err(), ErrWalkNoProgress) || calls != 2 {
		t.Fatalf("err=%v calls=%d", w.Err(), calls)
	}
}

func TestColumnWalkProtocol(t *testing.T) {
	root := MustOID(1, 3, 6, 1, 4, 1, 999, 1)
	for _, tc := range []struct {
		name  string
		batch []VarBind
		opts  []CallOption
		want  error
		rows  int
	}{
		{"duplicate", []VarBind{octet(root.Child(1), "a"), octet(root.Child(1), "b")}, nil, ErrOIDNotIncreasing, 0},
		{"decrease", []VarBind{octet(root.Child(2), "a"), octet(root.Child(1), "b")}, nil, ErrOIDNotIncreasing, 0},
		{"skip-decrease", []VarBind{octet(root.Child(2), "a"), octet(root.Child(1), "b")}, []CallOption{WithCallIgnoreNonIncreasing(true)}, nil, 1},
		{"budget", []VarBind{NoSuchInstanceVar{Header: Header{OID: root.Child(1), Kind: KindNoSuchInstance}}, octet(root.Child(2), "b")}, []CallOption{WithCallMaxWalkVars(1)}, ErrMaxWalkVars, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			sess := columnScript{call: func(_ context.Context, oids []OID, _ int, _ bool) ([]VarBind, error) {
				calls++
				if calls == 1 {
					return tc.batch, nil
				}
				return []VarBind{EndOfMibViewVar{Header: Header{OID: oids[0], Kind: KindEndOfMibView}}}, nil
			}}
			w := WalkColumns(context.Background(), sess, []OID{root}, TableWalkOptions{CallOptions: tc.opts})
			n := 0
			for range w.Iter() {
				n++
			}
			if !errors.Is(w.Err(), tc.want) || n != tc.rows {
				t.Fatalf("rows=%d err=%v", n, w.Err())
			}
		})
	}
	for _, batch := range [][]VarBind{{octet(root, "empty-index")}, {octet(root.Child(1), "a"), octet(root.Child(2), "b")}} {
		w := WalkColumns(context.Background(), columnScript{call: func(context.Context, []OID, int, bool) ([]VarBind, error) { return batch, nil }}, []OID{root}, TableWalkOptions{MaxRepetitions: 1})
		for range w.Iter() {
			t.Fatal("malformed response yielded")
		}
		if w.Err() == nil {
			t.Fatal("malformed response succeeded")
		}
	}
}

func TestColumnWalkTooBig(t *testing.T) {
	roots := []OID{MustOID(1, 3, 6, 1, 4, 1, 999, 1), MustOID(1, 3, 6, 1, 4, 1, 999, 2)}
	var trace []string
	sess := columnScript{call: func(_ context.Context, oids []OID, reps int, next bool) ([]VarBind, error) {
		trace = append(trace, fmt.Sprintf("%d/%d/%v", len(oids), reps, next))
		if !next {
			return nil, &PDUError{Status: TooBig}
		}
		return []VarBind{octet(oids[0].Child(1), "a")}, nil
	}}
	w := WalkColumns(context.Background(), sess, roots, TableWalkOptions{MaxRepetitions: 4})
	for range w.Iter() {
		break
	}
	want := []string{"2/4/false", "2/2/false", "2/1/false", "1/1/false", "1/1/true", "1/1/false", "1/1/true"}
	if !reflect.DeepEqual(trace, want) || w.Err() != nil {
		t.Fatalf("trace=%v err=%v", trace, w.Err())
	}
}

func TestColumnWalkTruncatedFairness(t *testing.T) {
	roots := []OID{MustOID(1, 3, 6, 1, 4, 1, 999, 1), MustOID(1, 3, 6, 1, 4, 1, 999, 2)}
	calls := 0
	sess := columnScript{call: func(_ context.Context, oids []OID, _ int, _ bool) ([]VarBind, error) {
		calls++
		if calls > 4 {
			t.Fatal("starved second cursor")
		}
		o := oids[0]
		if o.Len() == roots[0].Len() {
			return []VarBind{octet(o.Child(1), "a")}, nil
		}
		return []VarBind{EndOfMibViewVar{Header: Header{OID: o, Kind: KindEndOfMibView}}}, nil
	}}
	w := WalkColumns(context.Background(), sess, roots, TableWalkOptions{})
	n := 0
	for _, cells := range w.Iter() {
		n++
		if len(cells) != 2 {
			t.Fatal(cells)
		}
	}
	if n != 1 || w.Err() != nil {
		t.Fatalf("rows=%d err=%v", n, w.Err())
	}
}

func TestColumnWalkCancellation(t *testing.T) {
	root := MustOID(1, 3, 6, 1, 4, 1, 999, 1)
	for _, parentCancel := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		entered := make(chan struct{})
		sess := columnScript{call: func(ctx context.Context, _ []OID, _ int, _ bool) ([]VarBind, error) {
			close(entered)
			<-ctx.Done()
			return nil, ctx.Err()
		}}
		w := WalkColumns(ctx, sess, []OID{root}, TableWalkOptions{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			for range w.Iter() {
			}
		}()
		<-entered
		if parentCancel {
			cancel()
		} else {
			w.Close()
			w.Close()
		}
		<-done
		want := error(nil)
		if parentCancel {
			want = context.Canceled
		}
		if !errors.Is(w.Err(), want) {
			t.Fatalf("error=%v want=%v", w.Err(), want)
		}
		cancel()
	}
	w := WalkColumns(context.Background(), columnScript{call: func(context.Context, []OID, int, bool) ([]VarBind, error) {
		t.Fatal("request after Close")
		return nil, nil
	}}, []OID{root}, TableWalkOptions{})
	w.Close()
	for range w.Iter() {
		t.Fatal("row after Close")
	}
	if w.Err() != nil {
		t.Fatal(w.Err())
	}
}

func TestColumnWalkScaleBound(t *testing.T) {
	for _, n := range []int{1000, 100000} {
		roots := []OID{MustOID(1, 3, 6, 1, 4, 1, 999, 1), MustOID(1, 3, 6, 1, 4, 1, 999, 2), MustOID(1, 3, 6, 1, 4, 1, 999, 3)}
		calls := 0
		sess := columnScript{call: func(_ context.Context, oids []OID, reps int, _ bool) ([]VarBind, error) {
			calls++
			var out []VarBind
			cur := append([]OID(nil), oids...)
			for range reps {
				for i, o := range cur {
					col := int(o.At(7))
					index := uint32((col-1)*n + 1)
					if o.Len() > 8 {
						index = o.At(8) + 1
					}
					var vb VarBind = EndOfMibViewVar{Header: Header{OID: o, Kind: KindEndOfMibView}}
					if col < 3 && int(index) <= col*n {
						next := roots[col-1].Child(index)
						vb = octet(next, "payload")
						cur[i] = next
					}
					out = append(out, vb)
				}
			}
			return out, nil
		}}
		w := WalkColumns(context.Background(), sess, roots, TableWalkOptions{MaxRepetitions: 7})
		m := columnMerge{w: w, columns: make([]columnCursor, len(roots))}
		for i, r := range roots {
			m.columns[i] = columnCursor{root: r.WireBytes(), cursor: r.WireBytes()}
		}
		rows, highWater := 0, 0
		var held RawVarBind
		err := m.run(func(idx OID, cells []ColumnCell) bool {
			rows++
			if int(idx.At(0)) != rows {
				t.Fatalf("index=%v row=%d", idx, rows)
			}
			queued := 0
			for _, c := range m.columns {
				queued += len(c.queue)
				if len(c.queue) == 0 && c.queue != nil {
					t.Fatal("drained queue retains backing array")
				}
			}
			highWater = max(highWater, queued+len(cells))
			if queued > len(roots)*7 {
				t.Fatalf("queued %d", queued)
			}
			if rows == 1 {
				held = cells[0].Value
			}
			return true
		})
		vb, e := held.Decode()
		if err != nil || e != nil || rows != 2*n || string(vb.(OctetStringVar).Value) != "payload" {
			t.Fatalf("rows=%d calls=%d err=%v/%v", rows, calls, err, e)
		}
		t.Logf("rows=%d queued-cell-high-water=%d bound=%d", rows, highWater, len(roots)*7)
		w.Close()
	}
}

func TestColumnWalkIndexOrder(t *testing.T) {
	root := MustOID(1, 3, 6, 1, 4, 1, 999, 1)
	suffixes := [][]uint32{{1}, {1, 2}, {127}, {128}, {192, 168, 0, 2}, {192, 168, 0, 10}, {16383}, {16384}}
	var batch []VarBind
	var want []string
	for _, s := range suffixes {
		batch = append(batch, octet(root.Append(s...), "v"))
		want = append(want, OID{}.Append(s...).String())
	}
	batch = append(batch, EndOfMibViewVar{Header: Header{OID: root, Kind: KindEndOfMibView}})
	w := WalkColumns(context.Background(), columnScript{call: func(context.Context, []OID, int, bool) ([]VarBind, error) { return batch, nil }}, []OID{root}, TableWalkOptions{})
	var got []string
	for idx := range w.Iter() {
		got = append(got, idx.String())
	}
	if !reflect.DeepEqual(got, want) || w.Err() != nil {
		t.Fatalf("got=%v err=%v", got, w.Err())
	}
}

func TestColumnWalkNativeInterleavedGet(t *testing.T) {
	root := MustOID(1, 3, 6, 1, 4, 1, 999, 1)
	var entries []mibEntry
	for i := uint32(1); i <= 4; i++ {
		o := root.Child(i)
		entries = append(entries, mibEntry{o, octet(o, fmt.Sprintf("row-%d", i))})
	}
	sess := dialNative(t, startMIBAgent(t, entries, mibBehavior{}), V2c)
	w := WalkColumns(context.Background(), sess, []OID{root}, TableWalkOptions{MaxRepetitions: 2})
	var held RawVarBind
	for idx, cells := range w.Iter() {
		if idx.At(0) == 1 {
			held = cells[0].Value
		}
		if _, err := sess.Get(context.Background(), []OID{root.Child(1)}); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()
	vb, err := held.Decode()
	if err != nil || w.Err() != nil || string(vb.(OctetStringVar).Value) != "row-1" {
		t.Fatalf("retained=%v err=%v/%v", vb, err, w.Err())
	}
}

func TestColumnWalkLateFailurePrefix(t *testing.T) {
	root := MustOID(1, 3, 6, 1, 4, 1, 999, 1)
	terminal := &PDUError{Status: GenErr, Index: 1, OID: root.Child(1)}
	sess := columnScript{call: func(_ context.Context, oids []OID, _ int, _ bool) ([]VarBind, error) {
		if oids[0].Equal(root) {
			return []VarBind{octet(root.Child(1), "first")}, nil
		}
		return nil, terminal
	}}
	w := WalkColumns(context.Background(), sess, []OID{root}, TableWalkOptions{MaxRepetitions: 1})
	n := 0
	for range w.Iter() {
		n++
	}
	if n != 1 || !errors.Is(w.Err(), terminal) {
		t.Fatalf("rows=%d err=%v", n, w.Err())
	}
}

func TestColumnWalkConsumerPanicReleasesContext(t *testing.T) {
	root := MustOID(1, 3, 6, 1, 4, 1, 999, 1)
	w := WalkColumns(context.Background(), columnScript{call: func(context.Context, []OID, int, bool) ([]VarBind, error) {
		return []VarBind{octet(root.Child(1), "first")}, nil
	}}, []OID{root}, TableWalkOptions{})
	func() {
		defer func() {
			if recover() != "consumer" {
				t.Error("consumer panic changed")
			}
		}()
		for range w.Iter() {
			panic("consumer")
		}
	}()
	if w.ctx.Err() == nil {
		t.Fatal("consumer panic left request context open")
	}
}
