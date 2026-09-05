package snmp

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/service"
)

// fallbackLog captures the fallback warnings the Watcher emits through the
// service logger on its context, so tests can assert the one-log-per-
// transition contract.
//
// Write runs on the Watcher's tick goroutine while calls/lastMessage run on
// the test goroutine, so the recorded slice is mutex-guarded (`go test -race`
// catches the unguarded version).
type fallbackLog struct {
	mu       sync.Mutex
	messages []string
}

// fallbackLogMessage is the stable message body enterFallback logs.
const fallbackLogMessage = "watcher entering fallback mode"

func (l *fallbackLog) Write(p []byte) (int, error) {
	if line := string(p); strings.Contains(line, fallbackLogMessage) {
		l.mu.Lock()
		l.messages = append(l.messages, line)
		l.mu.Unlock()
	}
	return len(p), nil
}

func (l *fallbackLog) calls() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return int64(len(l.messages))
}

func (l *fallbackLog) lastMessage() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.messages) == 0 {
		return ""
	}
	return l.messages[len(l.messages)-1]
}

// runWithFallbackLog runs body inside a service attempt whose logger writes
// into logs, so code under test reaches it through service.Logger(ctx).
func runWithFallbackLog(t *testing.T, logs *fallbackLog, body func(ctx context.Context)) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	err := service.Run(context.Background(), service.Config{
		Identity: service.Identity{Name: "snmp_watcher_fallback_test", Namespace: "flowseer", Version: "test"},
		Logger:   logger,
		Setup: func(ctx context.Context) (service.Attempt, error) {
			body(ctx)
			return service.Attempt{Runner: func(context.Context) error { return nil }}, nil
		},
	})
	if err != nil {
		t.Fatalf("running service attempt: %v", err)
	}
}

// TestWatcher_Scalar_NoSuchObjectFallback verifies that a scalar
// indicator returning NoSuchObject on cold-start triggers fallback
// and the one-time log fires exactly once.
func TestWatcher_Scalar_NoSuchObjectFallback(t *testing.T) {
	scalarOID := MustOID(1, 3, 6, 1, 2, 1, 1, 8)
	tableRoot := MustOID(1, 3, 6, 1, 2, 1, 1, 9)

	indicator := MustChangeIndicator(NewScalarIndicator(
		scalarOID, KindTimeTicks, []OID{tableRoot},
	))

	s := newScriptedSession()
	// Cold-start walk: 1 row with a single column.
	descrCol := tableRoot.Append(1, 2)
	s.pushWalkAt(tableRoot, []VarBind{
		OctetStringVar{
			Header: Header{OID: descrCol.Append(1), Kind: KindOctetString},
			Value:  []byte("a"),
		},
	})
	// Scalar Get returns NoSuchObject → fallback.
	s.pushGet(map[string]VarBind{
		scalarOID.String(): NoSuchObjectVar{
			Header: Header{OID: scalarOID, Kind: KindNoSuchObject},
		},
	})

	type row struct {
		idx   uint32
		descr string
	}
	decode := func(idx OID, vbs []VarBind) (row, error) {
		var r row
		if idx.Len() > 0 {
			r.idx = idx.At(0)
		}
		for _, vb := range vbs {
			if s, ok := vb.(OctetStringVar); ok {
				r.descr = string(s.Value)
			}
		}
		return r, nil
	}
	equal := func(a, b row) bool { return a == b }
	// merge mirrors the per-VB decode logic so partial-fetch ticks
	// update only the fields present in vbs (this test never exercises
	// such a tick, but the contract is required by NewWatcher).
	merge := func(dst *row, vbs []VarBind) {
		for _, vb := range vbs {
			if s, ok := vb.(OctetStringVar); ok {
				dst.descr = string(s.Value)
			}
		}
	}

	logs := &fallbackLog{}
	runWithFallbackLog(t, logs, func(ctx context.Context) {
		w, err := NewWatcher[row](
			ctx,
			s,
			indicator,
			[]AnyColumn{fakeColumn{oid: descrCol, kind: KindOctetString}},
			decode, equal, merge,
			WithCadenceBounds(20*time.Millisecond, 100*time.Millisecond),
			WithForcedWalkInterval(10*time.Hour),
		)
		if err != nil {
			t.Fatalf("NewWatcher: %v", err)
		}
		defer func() { _ = w.Close() }()

		// Drain cold-start (1 Added).
		select {
		case <-w.pump.Data():
		case <-time.After(500 * time.Millisecond):
			t.Fatal("timed out waiting for cold-start event")
		}

		// Fallback should be true immediately after cold-start
		// (the Get happened before Added events emit).
		if !w.Fallback() {
			t.Error("Fallback() = false after NoSuchObject scalar probe")
		}
		if logs.calls() != 1 {
			t.Errorf("logger calls = %d, want 1", logs.calls())
		}
		if !strings.Contains(logs.lastMessage(), "fallback") {
			t.Errorf("log message %q does not mention fallback", logs.lastMessage())
		}
		if w.Err() != nil {
			t.Errorf("Err = %v, want nil (indicator-exception fallback is not terminal)", w.Err())
		}
	})
}

func TestWatcher_Scalar_NoSuchInstanceFallback(t *testing.T) {
	scalarOID := MustOID(1, 3, 6, 1, 2, 1, 1, 8)
	tableRoot := MustOID(1, 3, 6, 1, 2, 1, 1, 9)
	indicator := MustChangeIndicator(NewScalarIndicator(
		scalarOID, KindTimeTicks, []OID{tableRoot},
	))

	s := newScriptedSession()
	s.pushWalkAt(tableRoot, nil)
	s.pushGet(map[string]VarBind{
		scalarOID.String(): NoSuchInstanceVar{
			Header: Header{OID: scalarOID, Kind: KindNoSuchInstance},
		},
	})

	type row struct{}
	decode := func(_ OID, _ []VarBind) (row, error) { return row{}, nil }
	equal := func(_, _ row) bool { return true }
	merge := func(_ *row, _ []VarBind) {}

	w, err := NewWatcher[row](
		context.Background(),
		s, indicator, nil, decode, equal, merge,
		WithCadenceBounds(20*time.Millisecond, 100*time.Millisecond),
		WithForcedWalkInterval(10*time.Hour),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	deadline := time.After(1 * time.Second)
	for !w.Fallback() {
		select {
		case <-deadline:
			t.Fatal("NoSuchInstance scalar probe did not trigger fallback within deadline")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestWatcher_Scalar_EndOfMibViewFallback(t *testing.T) {
	scalarOID := MustOID(1, 3, 6, 1, 2, 1, 1, 8)
	tableRoot := MustOID(1, 3, 6, 1, 2, 1, 1, 9)
	indicator := MustChangeIndicator(NewScalarIndicator(
		scalarOID, KindTimeTicks, []OID{tableRoot},
	))

	s := newScriptedSession()
	s.pushWalkAt(tableRoot, nil)
	s.pushGet(map[string]VarBind{
		scalarOID.String(): EndOfMibViewVar{
			Header: Header{OID: scalarOID, Kind: KindEndOfMibView},
		},
	})

	type row struct{}
	decode := func(_ OID, _ []VarBind) (row, error) { return row{}, nil }
	equal := func(_, _ row) bool { return true }
	merge := func(_ *row, _ []VarBind) {}

	w, err := NewWatcher[row](
		context.Background(),
		s, indicator, nil, decode, equal, merge,
		WithCadenceBounds(20*time.Millisecond, 100*time.Millisecond),
		WithForcedWalkInterval(10*time.Hour),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	deadline := time.After(1 * time.Second)
	for !w.Fallback() {
		select {
		case <-deadline:
			t.Fatal("EndOfMibView scalar probe did not trigger fallback within deadline")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestWatcher_PerRow_NoIndicatorObserved verifies that a per-row
// indicator column missing from the cold-start walk (despite the
// table having rows) triggers fallback.
func TestWatcher_PerRow_NoIndicatorObserved(t *testing.T) {
	s := newScriptedSession()
	// Cold-start: rows present but NO ifLastChange VBs.
	s.pushTableWalk([]VarBind{
		OctetStringVar{
			Header: Header{OID: ifDescrOID.Append(1), Kind: KindOctetString},
			Value:  []byte("eth1"),
		},
	})

	logs := &fallbackLog{}
	runWithFallbackLog(t, logs, func(ctx context.Context) {
		w, err := NewWatcher[testIfRow](
			ctx,
			s,
			makeIfIndicator(t),
			[]AnyColumn{fakeColumn{oid: ifDescrOID, kind: KindOctetString}},
			testIfDecode,
			testIfEqual,
			testIfMerge,
			WithCadenceBounds(20*time.Millisecond, 100*time.Millisecond),
			WithForcedWalkInterval(10*time.Hour),
		)
		if err != nil {
			t.Fatalf("NewWatcher: %v", err)
		}
		defer func() { _ = w.Close() }()

		<-w.pump.Data() // cold-start Added

		if !w.Fallback() {
			t.Error("per-row indicator missing from cold-start did not trigger fallback")
		}
		if logs.calls() != 1 {
			t.Errorf("logger calls = %d, want 1", logs.calls())
		}
	})
}

// TestWatcher_PerRow_EmptyTableNoFallback pins that an empty
// table at cold-start does NOT trigger fallback — we have no
// evidence either way.
func TestWatcher_PerRow_EmptyTableNoFallback(t *testing.T) {
	s := newScriptedSession()
	s.pushTableWalk(nil)

	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		nil,
		testIfDecode,
		testIfEqual,
		testIfMerge,
		WithCadenceBounds(20*time.Millisecond, 100*time.Millisecond),
		WithForcedWalkInterval(10*time.Hour),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()
	time.Sleep(50 * time.Millisecond)
	if w.Fallback() {
		t.Error("empty cold-start incorrectly triggered fallback")
	}
}

// TestWatcher_Fallback_IsMonotonic verifies that once Fallback() is
// true, it stays true. First ship: no recovery from fallback mode
// without constructing a new Watcher.
func TestWatcher_Fallback_IsMonotonic(t *testing.T) {
	s := newScriptedSession()
	s.pushTableWalk(nil)
	s.pushGet(map[string]VarBind{}) // trigger fallback via empty Get response on first probe? Not quite.

	// Easier route: directly call enterFallback on a constructed
	// Watcher to mimic transition.
	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		nil,
		testIfDecode,
		testIfEqual,
		testIfMerge,
		WithCadenceBounds(20*time.Millisecond, 100*time.Millisecond),
		WithForcedWalkInterval(10*time.Hour),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	w.enterFallback("test-trigger-1")
	if !w.Fallback() {
		t.Fatal("enterFallback did not set Fallback() = true")
	}
	// Second call should be a no-op.
	w.enterFallback("test-trigger-2")
	if !w.Fallback() {
		t.Error("Fallback() reverted to false")
	}
}

// TestWatcher_Fallback_EnterLogsOnce verifies the edge-triggered
// "one log per transition" contract.
func TestWatcher_Fallback_EnterLogsOnce(t *testing.T) {
	s := newScriptedSession()
	s.pushTableWalk(nil)

	logs := &fallbackLog{}
	runWithFallbackLog(t, logs, func(ctx context.Context) {
		w, err := NewWatcher[testIfRow](
			ctx,
			s,
			makeIfIndicator(t),
			nil,
			testIfDecode,
			testIfEqual,
			testIfMerge,
			WithCadenceBounds(20*time.Millisecond, 100*time.Millisecond),
			WithForcedWalkInterval(10*time.Hour),
		)
		if err != nil {
			t.Fatalf("NewWatcher: %v", err)
		}
		defer func() { _ = w.Close() }()

		w.enterFallback("first")
		w.enterFallback("second")
		w.enterFallback("third")
		if logs.calls() != 1 {
			t.Errorf("logger calls = %d, want 1 (edge-triggered)", logs.calls())
		}
		if !strings.Contains(logs.lastMessage(), "first") {
			t.Errorf("log captured wrong reason: %q", logs.lastMessage())
		}
	})
}

// TestWatcher_TransientGetErrorSurfaceLastTickErrNotEvents
// verifies that a Get failure during the per-row tick's targeted
// fetch surfaces via LastTickErr without injecting synthetic events
// into the stream.
func TestWatcher_TransientGetErrorSurfaceLastTickErrNotEvents(t *testing.T) {
	// Use a session that fails the Get on every advance tick. By
	// pushing many advancing indicator walks the targeted Get
	// fails repeatedly, keeping LastTickErr populated against the
	// per-tick reset.
	s := &flakySession{
		scripted: newScriptedSession(),
		getErr:   errors.New("transient-timeout"),
	}
	s.scripted.pushTableWalk(buildIfTableRows(t,
		ifRow{idx: 1, descr: "eth1", lastCh: 100},
	))
	for i := uint32(2); i < 50; i++ {
		s.scripted.pushIndicatorWalk(buildIndicatorVBs(idxValue{1, i * 100}))
	}

	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		[]AnyColumn{fakeColumn{oid: ifDescrOID, kind: KindOctetString}},
		testIfDecode,
		testIfEqual,
		testIfMerge,
		WithCadenceBounds(10*time.Millisecond, 10*time.Millisecond), // tight + no step
		WithForcedWalkInterval(10*time.Hour),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	// Drain cold-start Added.
	select {
	case <-w.pump.Data():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out on cold-start")
	}

	// Poll LastTickErr — by design the value can be nil mid-tick
	// when reset before re-set; loop until we see it populated.
	deadline := time.After(1 * time.Second)
	var seenErr error
	for seenErr == nil {
		select {
		case <-deadline:
			t.Fatalf("LastTickErr never populated; getCount=%d", s.getCount.Load())
		case <-time.After(5 * time.Millisecond):
			seenErr = w.LastTickErr()
		}
	}
	if !strings.Contains(seenErr.Error(), "transient-timeout") {
		t.Errorf("LastTickErr = %v, want transient-timeout", seenErr)
	}
	if w.Err() != nil {
		t.Errorf("Err = %v, want nil (transient errors are not terminal)", w.Err())
	}
}

// flakySession decorates scriptedSession so Get returns a configured
// error after the cold-start completes.
type flakySession struct {
	scripted *scriptedSession
	getErr   error
	getCount atomic.Int64
}

func (f *flakySession) Get(ctx context.Context, oids []OID, opts ...CallOption) ([]VarBind, error) {
	f.getCount.Add(1)
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.scripted.Get(ctx, oids, opts...)
}

func (f *flakySession) BulkWalkRaw(ctx context.Context, root OID, opts ...CallOption) *RawWalker {
	return f.scripted.BulkWalkRaw(ctx, root, opts...)
}

func (f *flakySession) GetNext(ctx context.Context, oids []OID, opts ...CallOption) ([]VarBind, error) {
	return f.scripted.GetNext(ctx, oids, opts...)
}

func (f *flakySession) GetBulk(ctx context.Context, nr, mr uint8, oids []OID, opts ...CallOption) ([]VarBind, error) {
	return f.scripted.GetBulk(ctx, nr, mr, oids, opts...)
}

func (f *flakySession) Walk(ctx context.Context, root OID, opts ...CallOption) *Walker {
	return f.scripted.Walk(ctx, root, opts...)
}

func (f *flakySession) BulkWalk(ctx context.Context, root OID, opts ...CallOption) *Walker {
	return f.scripted.BulkWalk(ctx, root, opts...)
}

func (f *flakySession) Set(ctx context.Context, vbs []VarBind, opts ...CallOption) ([]VarBind, error) {
	return f.scripted.Set(ctx, vbs, opts...)
}
func (f *flakySession) Close() error { return f.scripted.Close() }

var _ Session = (*flakySession)(nil)

// TestWatcher_StuckZeroFallbackOnCounterMovement verifies that
// when the indicator stays unchanged for the probe-window count of
// consecutive ticks but Counter-tier columns advance (proving the
// device is alive), the Watcher transitions to fallback.
func TestWatcher_StuckZeroFallbackOnCounterMovement(t *testing.T) {
	counterCol := fakeColumn{
		oid:  MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 10),
		kind: KindCounter32,
	}

	s := newScriptedSession()
	s.pushTableWalk([]VarBind{
		OctetStringVar{
			Header: Header{OID: ifDescrOID.Append(1), Kind: KindOctetString},
			Value:  []byte("eth1"),
		},
		TimeTicksVar{
			Header: Header{OID: ifLastChange.Append(1), Kind: KindTimeTicks},
			Value:  100,
		},
		Counter32Var{
			Header: Header{OID: counterCol.OID().Append(1), Kind: KindCounter32},
			Value:  1000,
		},
	})
	// Many quiet indicator walks.
	for i := 0; i < 30; i++ {
		s.pushIndicatorWalk(buildIndicatorVBs(idxValue{1, 100}))
	}
	// Counter values change each Get → counter emits register as
	// movement → fuels the probe.
	for v := uint32(2000); v <= 20000; v += 1000 {
		s.pushGet(map[string]VarBind{
			counterCol.OID().Append(1).String(): Counter32Var{
				Header: Header{OID: counterCol.OID().Append(1), Kind: KindCounter32},
				Value:  v,
			},
		})
	}

	type counterRow struct {
		Idx   uint32
		Bytes uint32
	}
	decode := func(idx OID, vbs []VarBind) (counterRow, error) {
		var r counterRow
		if idx.Len() > 0 {
			r.Idx = idx.At(0)
		}
		for _, vb := range vbs {
			if c, ok := vb.(Counter32Var); ok {
				r.Bytes = c.Value
			}
		}
		return r, nil
	}
	equal := func(a, b counterRow) bool { return a == b }
	// merge updates only Bytes from the partial Counter-tier fetch.
	// Idx is preserved from prev because the Counter-tier Get does
	// not re-walk the index column.
	merge := func(dst *counterRow, vbs []VarBind) {
		for _, vb := range vbs {
			if c, ok := vb.(Counter32Var); ok {
				dst.Bytes = c.Value
			}
		}
	}

	logs := &fallbackLog{}
	runWithFallbackLog(t, logs, func(ctx context.Context) {
		w, err := NewWatcher[counterRow](
			ctx,
			s,
			makeIfIndicator(t),
			[]AnyColumn{counterCol},
			decode, equal, merge,
			// Very short cadences so the probe can fire in test time.
			// CadenceMin = CadenceMax = 10ms (no stepping → probe stays
			// at min cadence; the probe window resets on cadence steps).
			WithCadenceBounds(10*time.Millisecond, 10*time.Millisecond),
			WithCounterCadence(counterCol, 10*time.Millisecond),
			WithProbeWindow(3),
			WithForcedWalkInterval(10*time.Hour),
		)
		if err != nil {
			t.Fatalf("NewWatcher: %v", err)
		}
		defer func() { _ = w.Close() }()

		// Wait long enough for several ticks to occur.
		deadline := time.After(2 * time.Second)
		for !w.Fallback() {
			select {
			case <-deadline:
				t.Fatalf("fallback never engaged within deadline; counterEmits=%d",
					w.counterEventsSinceLastState.Load())
			case <-time.After(20 * time.Millisecond):
			}
		}
		// Fallback() flips before enterFallback writes its warning, so
		// wait for the log rather than sampling it the moment the flag
		// is observed.
		logDeadline := time.After(time.Second)
		for logs.calls() < 1 {
			select {
			case <-logDeadline:
				t.Fatal("fallback log never recorded")
			case <-time.After(10 * time.Millisecond):
			}
		}
		if !strings.Contains(logs.lastMessage(), "probe window") {
			t.Errorf("log %q does not mention probe window", logs.lastMessage())
		}
	})
}

// TestWatcher_ProbeWindowDisabledByZero verifies that
// WithProbeWindow(0) disables the stuck-zero probe; only the
// indicator-exception path can trigger fallback in that mode.
func TestWatcher_ProbeWindowDisabledByZero(t *testing.T) {
	counterCol := fakeColumn{
		oid:  MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 10),
		kind: KindCounter32,
	}

	s := newScriptedSession()
	s.pushTableWalk([]VarBind{
		OctetStringVar{
			Header: Header{OID: ifDescrOID.Append(1), Kind: KindOctetString},
			Value:  []byte("eth1"),
		},
		TimeTicksVar{
			Header: Header{OID: ifLastChange.Append(1), Kind: KindTimeTicks},
			Value:  100,
		},
	})
	for i := 0; i < 30; i++ {
		s.pushIndicatorWalk(buildIndicatorVBs(idxValue{1, 100}))
	}
	for v := uint32(2000); v <= 10000; v += 1000 {
		s.pushGet(map[string]VarBind{
			counterCol.OID().Append(1).String(): Counter32Var{
				Header: Header{OID: counterCol.OID().Append(1), Kind: KindCounter32},
				Value:  v,
			},
		})
	}

	type counterRow struct {
		Idx   uint32
		Bytes uint32
	}
	decode := func(idx OID, vbs []VarBind) (counterRow, error) {
		var r counterRow
		if idx.Len() > 0 {
			r.Idx = idx.At(0)
		}
		for _, vb := range vbs {
			if c, ok := vb.(Counter32Var); ok {
				r.Bytes = c.Value
			}
		}
		return r, nil
	}
	equal := func(a, b counterRow) bool { return a == b }
	merge := func(dst *counterRow, vbs []VarBind) {
		for _, vb := range vbs {
			if c, ok := vb.(Counter32Var); ok {
				dst.Bytes = c.Value
			}
		}
	}

	w, err := NewWatcher[counterRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		[]AnyColumn{counterCol},
		decode, equal, merge,
		WithCadenceBounds(10*time.Millisecond, 10*time.Millisecond),
		WithCounterCadence(counterCol, 10*time.Millisecond),
		WithProbeWindow(0), // disabled
		WithForcedWalkInterval(10*time.Hour),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	time.Sleep(500 * time.Millisecond)
	if w.Fallback() {
		t.Error("ProbeWindow(0) but Watcher entered fallback")
	}
}

// TestWatcher_NoCounterColumnsProbeNeverFires pins the
// documented limitation: the stuck-zero probe requires at least one
// Counter-tier column. Without one, the probe can never fire.
func TestWatcher_NoCounterColumnsProbeNeverFires(t *testing.T) {
	s := newScriptedSession()
	s.pushTableWalk(buildIfTableRows(t,
		ifRow{idx: 1, descr: "eth1", lastCh: 100},
	))
	for i := 0; i < 30; i++ {
		s.pushIndicatorWalk(buildIndicatorVBs(idxValue{1, 100}))
	}

	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		// No Counter cols.
		[]AnyColumn{fakeColumn{oid: ifDescrOID, kind: KindOctetString}},
		testIfDecode,
		testIfEqual,
		testIfMerge,
		WithCadenceBounds(10*time.Millisecond, 10*time.Millisecond),
		WithProbeWindow(3),
		WithForcedWalkInterval(10*time.Hour),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	time.Sleep(200 * time.Millisecond)
	if w.Fallback() {
		t.Error("no Counter cols but stuck-zero probe fired")
	}
}

// TestWatcher_FallbackTick_UnconditionalFullWalk verifies that once
// in fallback mode, every tick performs a full table walk and emits
// diff events without consulting the indicator.
func TestWatcher_FallbackTick_UnconditionalFullWalk(t *testing.T) {
	s := newScriptedSession()
	// Cold-start: 1 row.
	s.pushTableWalk(buildIfTableRows(t,
		ifRow{idx: 1, descr: "eth1", lastCh: 100},
	))
	// Forcing fallback via an indicator exception is not possible
	// here: the scalar path needs NoSuchObject, but we have a
	// per-row indicator, and the per-row cold-start walk has the
	// indicator VBs. Trigger via enterFallback directly, then push
	// a tick-2 full walk.
	s.pushTableWalk(buildIfTableRows(t,
		ifRow{idx: 1, descr: "eth1-tick2", lastCh: 100},
	))

	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		nil,
		testIfDecode,
		testIfEqual,
		testIfMerge,
		WithCadenceBounds(20*time.Millisecond, 20*time.Millisecond),
		WithForcedWalkInterval(10*time.Hour),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	<-w.pump.Data() // cold-start Added

	// Force into fallback.
	w.enterFallback("forced by test")

	// The next tick fires a full walk → should emit Modified for
	// the changed row.
	select {
	case ev := <-w.pump.Data():
		if ev.Kind != ChangeKindModified {
			t.Errorf("fallback tick event kind = %v, want Modified", ev.Kind)
		}
		if ev.Row.IfDescr != "eth1-tick2" {
			t.Errorf("fallback tick row IfDescr = %q, want eth1-tick2", ev.Row.IfDescr)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("no fallback tick event within deadline")
	}
}

// TestWatcher_FallbackDoesNotTerminate_ErrStaysNil pins that
// fallback is *degraded but operational* — Err() stays nil.
func TestWatcher_FallbackDoesNotTerminate_ErrStaysNil(t *testing.T) {
	scalarOID := MustOID(1, 3, 6, 1, 2, 1, 1, 8)
	tableRoot := MustOID(1, 3, 6, 1, 2, 1, 1, 9)
	indicator := MustChangeIndicator(NewScalarIndicator(
		scalarOID, KindTimeTicks, []OID{tableRoot},
	))
	s := newScriptedSession()
	s.pushWalkAt(tableRoot, nil)
	s.pushGet(map[string]VarBind{
		scalarOID.String(): NoSuchObjectVar{
			Header: Header{OID: scalarOID, Kind: KindNoSuchObject},
		},
	})

	type row struct{}
	decode := func(_ OID, _ []VarBind) (row, error) { return row{}, nil }
	equal := func(_, _ row) bool { return true }
	merge := func(_ *row, _ []VarBind) {}

	w, err := NewWatcher[row](
		context.Background(),
		s, indicator, nil, decode, equal, merge,
		WithCadenceBounds(20*time.Millisecond, 100*time.Millisecond),
		WithForcedWalkInterval(10*time.Hour),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	time.Sleep(80 * time.Millisecond)
	if w.Err() != nil {
		t.Errorf("Err = %v, want nil (fallback is not terminal)", w.Err())
	}
	if !w.Fallback() {
		t.Error("Fallback() = false but we expected true")
	}
}
