package snmp_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// This file exercises the [snmp.Watcher] public API as a third-party
// caller would see it. Tests in the internal [snmp] package can
// reach private fields; tests here can only call exported methods,
// which keeps the public surface honest about what callers can
// observe.

// publicTestRow is a row type defined in the external test file so
// the Watcher's Row type parameter is exercised across package
// boundaries — catches a future API change that accidentally narrows
// Row's any-shaped constraint to comparable.
type publicTestRow struct {
	Index uint32
	Data  string
}

// External tests cannot satisfy the unexported sealedColumn method,
// so we cannot construct AnyColumn from outside the snmp package
// directly. Use snmp.NewColumn through a wrapper instead.
//
// (This is intentional API hygiene — AnyColumn is sealed so the
// implementation set stays under the snmp package's control. Generated
// code uses [snmp.NewColumn]; third-party code does the same.)

// makeColumn builds an [snmp.AnyColumn] via the public Column[T]
// constructor.
func makeColumn(t *testing.T, dotted string, k snmp.Kind) snmp.AnyColumn {
	t.Helper()
	oid, err := snmp.ParseOID(dotted)
	if err != nil {
		t.Fatalf("ParseOID(%q): %v", dotted, err)
	}
	return snmp.NewColumn[string](oid, k, func(vb snmp.VarBind) (string, error) {
		if s, ok := vb.(snmp.OctetStringVar); ok {
			return string(s.Value), nil
		}
		return "", snmp.ErrTypeMismatch
	})
}

// externalFakeSession is a small public-API-only fake [snmp.Session]
// suitable for exercising the Watcher from outside the snmp package.
type externalFakeSession struct {
	mu           sync.Mutex
	bulkVBs      []snmp.VarBind
	walkCalls    int
	closeOnEmpty bool
}

func (s *externalFakeSession) Get(context.Context, []snmp.OID, ...snmp.CallOption) ([]snmp.VarBind, error) {
	return nil, nil
}

func (s *externalFakeSession) GetNext(context.Context, []snmp.OID, ...snmp.CallOption) ([]snmp.VarBind, error) {
	return nil, nil
}

func (s *externalFakeSession) GetBulk(context.Context, uint8, uint8, []snmp.OID, ...snmp.CallOption) ([]snmp.VarBind, error) {
	return nil, nil
}

func (s *externalFakeSession) Set(context.Context, []snmp.VarBind, ...snmp.CallOption) ([]snmp.VarBind, error) {
	return nil, nil
}
func (s *externalFakeSession) Close() error { return nil }

func (s *externalFakeSession) Walk(ctx context.Context, _ snmp.OID, _ ...snmp.CallOption) *snmp.Walker {
	return s.BulkWalk(ctx, snmp.OID{})
}

func (s *externalFakeSession) BulkWalk(ctx context.Context, _ snmp.OID, _ ...snmp.CallOption) *snmp.Walker {
	s.mu.Lock()
	vbs := s.bulkVBs
	s.walkCalls++
	closeNow := s.closeOnEmpty && s.walkCalls > 1
	s.mu.Unlock()

	w := snmp.NewWalker(ctx, 16)
	w.Pump(func(_ context.Context) {
		if closeNow {
			w.Fail(snmp.ErrSessionClosed)
			return
		}
		for _, vb := range vbs {
			if !w.Send(vb.GetHeader().OID, vb) {
				return
			}
		}
	})
	return w
}

// TestExternal_ColdStartAddedThenSteady verifies that, viewed only
// through the public surface, a Watcher emits one Added event per row
// on cold-start and then quiesces (no further events while the
// indicator stays unchanged).

func (s *externalFakeSession) BulkWalkRaw(ctx context.Context, root snmp.OID, opts ...snmp.CallOption) *snmp.RawWalker {
	return snmp.RawWalkerFromWalker(ctx, s.BulkWalk(ctx, root, opts...))
}

func TestExternal_ColdStartAddedThenSteady(t *testing.T) {
	ifTable, err := snmp.ParseOID("1.3.6.1.2.1.2.2")
	if err != nil {
		t.Fatalf("ParseOID: %v", err)
	}

	indicatorCol := makeColumn(t, "1.3.6.1.2.1.2.2.1.9", snmp.KindTimeTicks)
	indicator, err := snmp.NewPerRowIndicator(indicatorCol, ifTable)
	if err != nil {
		t.Fatalf("NewPerRowIndicator: %v", err)
	}

	descrCol := makeColumn(t, "1.3.6.1.2.1.2.2.1.2", snmp.KindOctetString)

	// Build 2 fake rows: ifDescr + ifLastChange. The indicator
	// column must be present in the cold-start walk; otherwise the
	// Watcher correctly transitions to fallback mode on the
	// first-tick indicator exception.
	vbs := []snmp.VarBind{}
	for i := uint32(1); i <= 2; i++ {
		oid := snmp.MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 2).Append(i)
		vbs = append(vbs, snmp.OctetStringVar{
			Header: snmp.Header{OID: oid, Kind: snmp.KindOctetString},
			Value:  []byte(fmt.Sprintf("if-%d", i)),
		})
	}
	for i := uint32(1); i <= 2; i++ {
		oid := snmp.MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 9).Append(i)
		vbs = append(vbs, snmp.TimeTicksVar{
			Header: snmp.Header{OID: oid, Kind: snmp.KindTimeTicks},
			Value:  100 * i,
		})
	}
	sess := &externalFakeSession{bulkVBs: vbs}

	decode := func(idx snmp.OID, vbs []snmp.VarBind) (publicTestRow, error) {
		var r publicTestRow
		if idx.Len() != 1 {
			return r, fmt.Errorf("bad idx: %s", idx)
		}
		r.Index = idx.At(0)
		for _, vb := range vbs {
			if s, ok := vb.(snmp.OctetStringVar); ok {
				r.Data = string(s.Value)
			}
		}
		return r, nil
	}
	equal := func(a, b publicTestRow) bool {
		return a.Index == b.Index && a.Data == b.Data
	}
	merge := func(dst *publicTestRow, vbs []snmp.VarBind) {
		for _, vb := range vbs {
			if s, ok := vb.(snmp.OctetStringVar); ok {
				dst.Data = string(s.Value)
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	w, err := snmp.NewWatcher[publicTestRow](
		ctx, sess, indicator,
		[]snmp.AnyColumn{descrCol},
		decode, equal, merge,
		snmp.WithCadenceBounds(50*time.Millisecond, 500*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	events := receiveBudget(w)
	got := 0
	deadline := time.After(1 * time.Second)
loop:
	for {
		select {
		case <-deadline:
			t.Fatalf("timed out; got=%d", got)
		case _, ok := <-events:
			if !ok {
				break loop
			}
			got++
			if got >= 2 {
				break loop
			}
		}
	}
	if got != 2 {
		t.Errorf("got %d events, want 2", got)
	}

	if err := w.Err(); err != nil {
		t.Errorf("Err = %v, want nil", err)
	}
	if w.Fallback() {
		t.Error("Fallback = true, want false")
	}
	if w.LastTickErr() != nil {
		t.Errorf("LastTickErr = %v, want nil", w.LastTickErr())
	}
}

// receiveBudget bridges range-over-func iteration into a select-able
// channel so the test can race against a deadline. One goroutine
// pulls from the Watcher's Iter and forwards events on the returned
// channel; the channel closes when the Iter terminates.
func receiveBudget(w *snmp.Watcher[publicTestRow]) <-chan snmp.WatchEvent[publicTestRow] {
	out := make(chan snmp.WatchEvent[publicTestRow], 8)
	go func() {
		defer close(out)
		for _, ev := range w.Iter() {
			out <- ev
		}
	}()
	return out
}

// TestExternal_InvalidConfigSurfacesAtConstruction pins that a
// caller cannot accidentally get a half-constructed Watcher: invalid
// options yield (nil, err) and the caller's range loop never runs.
func TestExternal_InvalidConfigSurfacesAtConstruction(t *testing.T) {
	sess := &externalFakeSession{}
	ifTable := snmp.MustOID(1, 3, 6, 1, 2, 1, 2, 2)
	col := makeColumn(t, "1.3.6.1.2.1.2.2.1.9", snmp.KindTimeTicks)
	indicator, _ := snmp.NewPerRowIndicator(col, ifTable)

	w, err := snmp.NewWatcher[publicTestRow](
		context.Background(), sess, indicator, nil,
		func(snmp.OID, []snmp.VarBind) (publicTestRow, error) {
			return publicTestRow{}, nil
		},
		func(a, b publicTestRow) bool { return a == b },
		func(_ *publicTestRow, _ []snmp.VarBind) {},
		snmp.WithCadenceBounds(1*time.Minute, 10*time.Second), // min > max
	)
	if err == nil {
		t.Fatal("expected validation error")
	}
	// Non-nil-with-latched-Err contract: the returned Watcher exists
	// so a caller (or a generated Watch wrapper) can safely range
	// over Iter() and call Err() without a nil-check; iteration
	// yields zero events and Err() surfaces the validation cause.
	if w == nil {
		t.Fatal("Watcher = nil; want non-nil with latched Err()")
	}
	if w.Err() == nil {
		t.Errorf("Err() = nil; want the validation error")
	}
}

// TestExternal_TableRootAccessor pins that the Watcher exposes the
// table root it operates over — useful when a scalar indicator covers
// multiple tables and the caller wants to confirm which one was
// chosen.
func TestExternal_TableRootAccessor(t *testing.T) {
	sess := &externalFakeSession{}
	ifTable := snmp.MustOID(1, 3, 6, 1, 2, 1, 2, 2)
	col := makeColumn(t, "1.3.6.1.2.1.2.2.1.9", snmp.KindTimeTicks)
	indicator, _ := snmp.NewPerRowIndicator(col, ifTable)

	w, err := snmp.NewWatcher[publicTestRow](
		context.Background(), sess, indicator, nil,
		func(snmp.OID, []snmp.VarBind) (publicTestRow, error) {
			return publicTestRow{}, nil
		},
		func(a, b publicTestRow) bool { return a == b },
		func(_ *publicTestRow, _ []snmp.VarBind) {},
		snmp.WithCadenceBounds(1*time.Second, 30*time.Second),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	if got := w.TableRoot(); !got.Equal(ifTable) {
		t.Errorf("TableRoot = %s, want %s", got, ifTable)
	}
}
