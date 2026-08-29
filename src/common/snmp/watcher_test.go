package snmp

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- Fakes shared across watcher tests --------------------------------

// watcherFakeSession is a fake [Session]. It supports a
// scripted sequence of BulkWalk responses; each call dequeues the
// next scripted response. Get is supported for the interleaved-Get
// parity tests. GetNext / GetBulk / Walk / Set return zero/empty
// values; these tests' Watchers do not consume them.
//
// The session is safe for concurrent use by the Watcher's tick
// goroutine and a test goroutine that issues parallel Get calls.
type watcherFakeSession struct {
	mu sync.Mutex

	// bulkWalkScripts is the FIFO queue of responses for BulkWalk.
	// Each script either returns a slice of VarBinds OR an error
	// (mutually exclusive). When the queue empties, BulkWalk returns
	// a Walker carrying ErrSessionClosed (terminal — every well-formed
	// test scenario knows in advance how many BulkWalk calls it will
	// make).
	bulkWalkScripts []bulkWalkScript

	bulkWalkCalls atomic.Uint64
	getCalls      atomic.Uint64

	// closeBehavior controls what BulkWalk/Get do once Close has
	// been called externally.
	closed atomic.Bool

	// pumpDelay is added between each VarBind the pumped Walker
	// sends. Useful for tests that want to interrupt mid-walk.
	pumpDelay time.Duration
}

type bulkWalkScript struct {
	vbs []VarBind
	err error
}

func (s *watcherFakeSession) Get(_ context.Context, oids []OID, _ ...CallOption) ([]VarBind, error) {
	s.getCalls.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed.Load() {
		return nil, ErrSessionClosed
	}
	out := make([]VarBind, len(oids))
	for i, o := range oids {
		out[i] = Counter32Var{
			Header: Header{OID: o, Kind: KindCounter32},
			Value:  uint32(7),
		}
	}
	return out, nil
}

func (s *watcherFakeSession) GetNext(context.Context, []OID, ...CallOption) ([]VarBind, error) {
	return nil, nil
}

func (s *watcherFakeSession) GetBulk(context.Context, uint8, uint8, []OID, ...CallOption) ([]VarBind, error) {
	return nil, nil
}

func (s *watcherFakeSession) Set(context.Context, []VarBind, ...CallOption) ([]VarBind, error) {
	return nil, nil
}

func (s *watcherFakeSession) Walk(ctx context.Context, _ OID, _ ...CallOption) *Walker {
	return s.BulkWalk(ctx, OID{})
}

func (s *watcherFakeSession) BulkWalk(ctx context.Context, _ OID, _ ...CallOption) *Walker {
	s.bulkWalkCalls.Add(1)

	s.mu.Lock()
	var script bulkWalkScript
	hasScript := len(s.bulkWalkScripts) > 0
	if hasScript {
		script = s.bulkWalkScripts[0]
		s.bulkWalkScripts = s.bulkWalkScripts[1:]
	}
	closed := s.closed.Load()
	delay := s.pumpDelay
	s.mu.Unlock()

	w := NewWalker(ctx, 16)
	w.Pump(func(pctx context.Context) {
		if closed {
			w.Fail(ErrSessionClosed)
			return
		}
		if !hasScript {
			// No script queued → empty walk (no VarBinds, no error).
			// Matches real SNMP behavior for a sparse table.
			return
		}
		if script.err != nil {
			w.Fail(script.err)
			return
		}
		for _, vb := range script.vbs {
			if delay > 0 {
				select {
				case <-pctx.Done():
					return
				case <-time.After(delay):
				}
			}
			if !w.Send(vb.GetHeader().OID, vb) {
				return
			}
		}
	})
	return w
}

func (s *watcherFakeSession) BulkWalkRaw(ctx context.Context, root OID, opts ...CallOption) *RawWalker {
	return RawWalkerFromWalker(ctx, s.BulkWalk(ctx, root, opts...))
}

func (s *watcherFakeSession) Close() error {
	s.closed.Store(true)
	return nil
}

func (s *watcherFakeSession) pushScript(vbs []VarBind) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bulkWalkScripts = append(s.bulkWalkScripts, bulkWalkScript{vbs: vbs})
}

func (s *watcherFakeSession) pushScriptErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bulkWalkScripts = append(s.bulkWalkScripts, bulkWalkScript{err: err})
}

// --- Test fixtures: a tiny "interface table" row -----------------------

// ifEntry mirrors the SMIv2 conceptual ifTable.ifEntry OID
// (1.3.6.1.2.1.2.2.1) but lives here so tests do not import the
// generated mib packages.
var (
	ifTableRoot  = MustOID(1, 3, 6, 1, 2, 1, 2, 2)
	ifIndexOID   = MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 1)
	ifDescrOID   = MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 2)
	ifLastChange = MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 9)
)

// testIfRow is a tiny row type sufficient to exercise the Watcher's
// decode + diff contract without depending on generated code.
type testIfRow struct {
	IfIndex  uint32
	IfDescr  string
	IfLastCh uint32
}

func testIfDecode(idx OID, vbs []VarBind) (testIfRow, error) {
	var r testIfRow
	if idx.Len() != 1 {
		return r, fmt.Errorf("expected single-component ifIndex, got %s", idx)
	}
	r.IfIndex = idx.At(0)
	for _, vb := range vbs {
		oid := vb.GetHeader().OID
		// Strip the row index to identify the column.
		colOID, _ := oid.Parent() // drops the trailing index sub-id
		switch {
		case colOID.Equal(ifDescrOID):
			if s, ok := vb.(OctetStringVar); ok {
				r.IfDescr = string(s.Value)
			}
		case colOID.Equal(ifLastChange):
			if t, ok := vb.(TimeTicksVar); ok {
				r.IfLastCh = t.Value
			}
		}
	}
	return r, nil
}

func testIfEqual(a, b testIfRow) bool {
	return a.IfIndex == b.IfIndex &&
		a.IfDescr == b.IfDescr &&
		a.IfLastCh == b.IfLastCh
}

// testIfMerge mirrors mibgen's emitted merge<TableName>Row: for each
// VarBind, identify which column it belongs to and update only that
// field on dst. Fields whose columns are not in vbs stay at their
// prior value — the load-bearing property under partial-column
// (Counter/Static) fetches.
func testIfMerge(dst *testIfRow, vbs []VarBind) {
	for _, vb := range vbs {
		oid := vb.GetHeader().OID
		colOID, _ := oid.Parent()
		switch {
		case colOID.Equal(ifDescrOID):
			if s, ok := vb.(OctetStringVar); ok {
				dst.IfDescr = string(s.Value)
			}
		case colOID.Equal(ifLastChange):
			if t, ok := vb.(TimeTicksVar); ok {
				dst.IfLastCh = t.Value
			}
		}
	}
}

// makeIfRowVarBinds builds the VarBinds a real BulkWalk would return
// for n synthetic ifTable rows: each row contributes one ifDescr and
// one ifLastChange VarBind, with the ifDescr column emitted first
// across all rows, then the ifLastChange column across all rows
// (column-major order, matching real BulkWalk).
func makeIfRowVarBinds(n int) []VarBind {
	vbs := make([]VarBind, 0, 2*n)
	for i := 1; i <= n; i++ {
		oid := ifDescrOID.Append(uint32(i))
		vbs = append(vbs, OctetStringVar{
			Header: Header{OID: oid, Kind: KindOctetString},
			Value:  []byte(fmt.Sprintf("eth%d", i)),
		})
	}
	for i := 1; i <= n; i++ {
		oid := ifLastChange.Append(uint32(i))
		vbs = append(vbs, TimeTicksVar{
			Header: Header{OID: oid, Kind: KindTimeTicks},
			Value:  uint32(100 * i),
		})
	}
	return vbs
}

// makeIfIndicator constructs the per-row IfLastChange indicator for
// the synthetic ifTable.
func makeIfIndicator(t *testing.T) ChangeIndicator {
	t.Helper()
	col := fakeColumn{oid: ifLastChange, kind: KindTimeTicks}
	ci, err := NewPerRowIndicator(col, ifTableRoot)
	if err != nil {
		t.Fatalf("NewPerRowIndicator: %v", err)
	}
	return ci
}

// --- NewWatcher validation tests --------------------------------------

func TestNewWatcher_RejectsNilSession(t *testing.T) {
	w, err := NewWatcher[testIfRow](
		context.Background(),
		nil,
		makeIfIndicator(t),
		nil,
		testIfDecode,
		testIfEqual,
		testIfMerge,
		WithCadenceBounds(1*time.Second, 30*time.Second),
	)
	if err == nil {
		t.Fatal("NewWatcher(nil session) = nil err, want error")
	}
	// Non-nil-with-latched-error contract: the returned Watcher is
	// usable as a zombie; its Err() returns the validation error
	// and Iter() terminates immediately.
	if w == nil {
		t.Fatal("Watcher = nil; want non-nil with latched Err()")
	}
	if got := w.Err(); got == nil {
		t.Errorf("Err() = nil; want the validation error")
	}
}

func TestNewWatcher_RejectsZeroIndicator(t *testing.T) {
	s := &watcherFakeSession{}
	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		ChangeIndicator{},
		nil,
		testIfDecode,
		testIfEqual,
		testIfMerge,
		WithCadenceBounds(1*time.Second, 30*time.Second),
	)
	if err == nil {
		t.Fatal("NewWatcher(zero indicator) = nil err, want error")
	}
	if w == nil {
		t.Fatal("Watcher = nil; want non-nil with latched Err()")
	}
	if w.Err() == nil {
		t.Error("Err() = nil; want the validation error")
	}
}

func TestNewWatcher_RejectsNilDecode(t *testing.T) {
	s := &watcherFakeSession{}
	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		nil,
		nil,
		testIfEqual,
		testIfMerge,
		WithCadenceBounds(1*time.Second, 30*time.Second),
	)
	if err == nil {
		t.Fatalf("expected err, got nil; w=%v", w)
	}
	if w == nil || w.Err() == nil {
		t.Errorf("expected non-nil Watcher with latched Err(); got w=%v err=%v", w, err)
	}
}

func TestNewWatcher_RejectsNilEqual(t *testing.T) {
	s := &watcherFakeSession{}
	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		nil,
		testIfDecode,
		nil,
		testIfMerge,
		WithCadenceBounds(1*time.Second, 30*time.Second),
	)
	if err == nil {
		t.Fatalf("expected err, got nil; w=%v", w)
	}
	if w == nil || w.Err() == nil {
		t.Errorf("expected non-nil Watcher with latched Err(); got w=%v err=%v", w, err)
	}
}

func TestNewWatcher_RejectsNilMerge(t *testing.T) {
	s := &watcherFakeSession{}
	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		nil,
		testIfDecode,
		testIfEqual,
		nil,
		WithCadenceBounds(1*time.Second, 30*time.Second),
	)
	if err == nil {
		t.Fatalf("expected err, got nil; w=%v", w)
	}
	if w == nil || w.Err() == nil {
		t.Errorf("expected non-nil Watcher with latched Err(); got w=%v err=%v", w, err)
	}
}

func TestNewWatcher_RejectsMissingCadenceBounds(t *testing.T) {
	s := &watcherFakeSession{}
	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		nil,
		testIfDecode,
		testIfEqual,
		testIfMerge,
	)
	if err == nil {
		t.Fatalf("expected err, got nil; w=%v", w)
	}
	if w == nil || w.Err() == nil {
		t.Errorf("expected non-nil Watcher with latched Err(); got w=%v err=%v", w, err)
	}
}

func TestNewWatcher_RejectsInvalidConfig(t *testing.T) {
	baseline := runtime.NumGoroutine()
	s := &watcherFakeSession{}
	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		nil,
		testIfDecode,
		testIfEqual,
		testIfMerge,
		WithCadenceBounds(1*time.Minute, 10*time.Second), // min > max
	)
	if err == nil {
		t.Fatalf("expected err, got nil; w=%v", w)
	}
	if w == nil || w.Err() == nil {
		t.Errorf("expected non-nil Watcher with latched Err(); got w=%v err=%v", w, err)
	}
	// No goroutine should have been spawned for the invalid config.
	if n := waitForPumpExit(t, baseline, 200*time.Millisecond); n > baseline {
		t.Errorf("goroutine count = %d, baseline = %d (invalid NewWatcher leaked a goroutine)",
			n, baseline)
	}
}

func TestNewWatcher_RejectsIndicatorColumnOverride(t *testing.T) {
	s := &watcherFakeSession{}
	indicatorCol := fakeColumn{oid: ifLastChange, kind: KindTimeTicks}
	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		[]AnyColumn{fakeColumn{oid: ifDescrOID, kind: KindOctetString}},
		testIfDecode,
		testIfEqual,
		testIfMerge,
		WithCadenceBounds(1*time.Second, 30*time.Second),
		WithColumnTier(indicatorCol, TierStatic),
	)
	if err == nil {
		t.Fatal("expected indicator-override rejection, got nil err")
	}
	if w == nil || w.Err() == nil {
		t.Errorf("expected non-nil Watcher with latched Err(); got w=%v err=%v", w, err)
	}
}

// --- Cold-start lifecycle tests ---------------------------------------

func TestWatcher_ColdStartEmitsAddedForEveryRow(t *testing.T) {
	s := &watcherFakeSession{}
	s.pushScript(makeIfRowVarBinds(3))

	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		[]AnyColumn{
			fakeColumn{oid: ifDescrOID, kind: KindOctetString},
		},
		testIfDecode,
		testIfEqual,
		testIfMerge,
		WithCadenceBounds(50*time.Millisecond, 1*time.Second),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	got := make(map[uint32]testIfRow)
	timeout := time.After(2 * time.Second)
loop:
	for {
		select {
		case <-timeout:
			t.Fatalf("timed out waiting for cold-start events; got=%d", len(got))
		default:
		}
		select {
		case ev, ok := <-w.pump.Data():
			if !ok {
				break loop
			}
			if ev.Kind != ChangeKindAdded {
				t.Errorf("event kind = %v, want Added", ev.Kind)
			}
			got[ev.Row.IfIndex] = ev.Row
			if len(got) == 3 {
				break loop
			}
		case <-timeout:
			t.Fatalf("timed out; got=%d", len(got))
		}
	}

	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3", len(got))
	}
	for i := uint32(1); i <= 3; i++ {
		r, ok := got[i]
		if !ok {
			t.Errorf("row %d missing", i)
			continue
		}
		want := fmt.Sprintf("eth%d", i)
		if r.IfDescr != want {
			t.Errorf("row %d IfDescr = %q, want %q", i, r.IfDescr, want)
		}
		if r.IfLastCh != 100*i {
			t.Errorf("row %d IfLastCh = %d, want %d", i, r.IfLastCh, 100*i)
		}
	}

	// Steady state: after cold-start no further events should arrive
	// within a short window (the indicator never advances).
	select {
	case ev, ok := <-w.pump.Data():
		if ok {
			t.Errorf("unexpected post-cold-start event: %+v", ev)
		}
	case <-time.After(150 * time.Millisecond):
		// expected: no events
	}

	if err := w.Err(); err != nil {
		t.Errorf("Err = %v, want nil", err)
	}
	if w.Fallback() {
		t.Error("Fallback = true on healthy cold-start")
	}
	if w.LastTickErr() != nil {
		t.Errorf("LastTickErr = %v, want nil", w.LastTickErr())
	}

	// Snapshot was populated. Close the Watcher first so the tick
	// goroutine has exited before we read the goroutine-owned
	// snapshot map (otherwise -race flags a data race; #17). Drain
	// any in-flight events the close path may emit.
	_ = w.Close()
	for {
		ev, ok := <-w.pump.Data()
		if !ok {
			break
		}
		_ = ev
	}
	if len(w.snapshot) != 3 {
		t.Errorf("snapshot has %d entries, want 3", len(w.snapshot))
	}
}

func TestWatcher_EmptyColdStart(t *testing.T) {
	s := &watcherFakeSession{}
	s.pushScript(nil) // zero VarBinds → no rows

	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		nil,
		testIfDecode,
		testIfEqual,
		testIfMerge,
		WithCadenceBounds(50*time.Millisecond, 1*time.Second),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	// Expect no events; the steady-state loop is alive.
	select {
	case ev, ok := <-w.pump.Data():
		if ok {
			t.Errorf("unexpected event after empty cold-start: %+v", ev)
		}
	case <-time.After(150 * time.Millisecond):
		// expected
	}

	if err := w.Err(); err != nil {
		t.Errorf("Err = %v, want nil", err)
	}
	// Close before reading the goroutine-owned snapshot to avoid a
	// data race with the tick loop. Drain any in-flight events.
	_ = w.Close()
	for {
		ev, ok := <-w.pump.Data()
		if !ok {
			break
		}
		_ = ev
	}
	if len(w.snapshot) != 0 {
		t.Errorf("snapshot has %d entries, want 0", len(w.snapshot))
	}
}

func TestWatcher_ColdStartBulkWalkErrorIsTerminal(t *testing.T) {
	s := &watcherFakeSession{}
	sentinel := errors.New("upstream-died")
	s.pushScriptErr(sentinel)

	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		nil,
		testIfDecode,
		testIfEqual,
		testIfMerge,
		WithCadenceBounds(50*time.Millisecond, 1*time.Second),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}

	// Iter should return immediately without events.
	count := 0
	for range w.Iter() {
		count++
	}
	if count != 0 {
		t.Errorf("Iter yielded %d events after cold-start fail, want 0", count)
	}
	if err := w.Err(); !errors.Is(err, sentinel) {
		t.Errorf("Err = %v, want %v", err, sentinel)
	}
}

func TestWatcher_ColdStartSessionClosedIsTerminal(t *testing.T) {
	s := &watcherFakeSession{}
	s.closed.Store(true) // BulkWalk will return ErrSessionClosed via Walker.Err

	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		nil,
		testIfDecode,
		testIfEqual,
		testIfMerge,
		WithCadenceBounds(50*time.Millisecond, 1*time.Second),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}

	count := 0
	for range w.Iter() {
		count++
	}
	if count != 0 {
		t.Errorf("Iter yielded %d events, want 0", count)
	}
	if err := w.Err(); !errors.Is(err, ErrSessionClosed) {
		t.Errorf("Err = %v, want ErrSessionClosed", err)
	}
	if w.Fallback() {
		t.Error("Fallback = true on terminal close (should stay false)")
	}
}

// --- Close & lifecycle ------------------------------------------------

func TestWatcher_CloseIsIdempotent(t *testing.T) {
	s := &watcherFakeSession{}
	s.pushScript(makeIfRowVarBinds(1))

	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		nil,
		testIfDecode,
		testIfEqual,
		testIfMerge,
		WithCadenceBounds(50*time.Millisecond, 1*time.Second),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}

	// Drain cold-start before closing.
	<-w.pump.Data()

	if err := w.Close(); err != nil {
		t.Errorf("Close #1: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("Close #2: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("Close #3: %v", err)
	}
}

func TestWatcher_CloseIsNonBlocking(t *testing.T) {
	s := &watcherFakeSession{}
	s.pushScript(nil) // empty cold-start
	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		nil,
		testIfDecode,
		testIfEqual,
		testIfMerge,
		WithCadenceBounds(1*time.Second, 30*time.Second),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	// Let the cold-start complete and the steady-state loop enter its
	// long sleep.
	time.Sleep(20 * time.Millisecond)

	start := time.Now()
	if err := w.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("Close took %v, want < 50ms (must not block on goroutine exit)", elapsed)
	}
}

func TestWatcher_RangeBreakDrainsAndExits(t *testing.T) {
	baseline := runtime.NumGoroutine()
	s := &watcherFakeSession{}
	s.pushScript(makeIfRowVarBinds(10))

	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		nil,
		testIfDecode,
		testIfEqual,
		testIfMerge,
		WithCadenceBounds(50*time.Millisecond, 1*time.Second),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}

	count := 0
	for range w.Iter() {
		count++
		if count == 3 {
			break
		}
	}
	if count != 3 {
		t.Errorf("range consumed %d events before break, want 3", count)
	}

	// Close after the break so the watcher's steady-state loop exits.
	_ = w.Close()

	if n := waitForPumpExit(t, baseline, 500*time.Millisecond); n > baseline+1 {
		t.Errorf("goroutine count after break = %d, baseline = %d", n, baseline)
	}
}

func TestWatcher_ContextCancelMidColdStart(t *testing.T) {
	baseline := runtime.NumGoroutine()
	s := &watcherFakeSession{}
	// Many rows + a per-VarBind delay so the cold-start is slow and
	// cancellation has time to fire mid-walk.
	s.pushScript(makeIfRowVarBinds(50))
	s.pumpDelay = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())

	w, err := NewWatcher[testIfRow](
		ctx,
		s,
		makeIfIndicator(t),
		nil,
		testIfDecode,
		testIfEqual,
		testIfMerge,
		WithCadenceBounds(50*time.Millisecond, 1*time.Second),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}

	// Consume a couple of events to confirm the cold-start started.
	gotFirst := false
	go func() {
		for range w.Iter() {
			gotFirst = true
		}
	}()
	time.Sleep(15 * time.Millisecond)
	cancel()

	if n := waitForPumpExit(t, baseline, 1*time.Second); n > baseline+1 {
		t.Errorf("goroutine count after cancel = %d, baseline = %d", n, baseline)
	}
	if !gotFirst {
		// Not a hard failure: with very fast cancellation the
		// consumer may never see an event. Log for diagnosis.
		t.Log("note: no events observed before cancel — timing-sensitive")
	}
	_ = w
}

// --- Goroutine hygiene: nested-Walker close discipline ----------------

func TestWatcher_NestedWalkerNoLeakAcrossCycles(t *testing.T) {
	baseline := runtime.NumGoroutine()

	for cycle := 0; cycle < 5; cycle++ {
		s := &watcherFakeSession{pumpDelay: 1 * time.Millisecond}
		s.pushScript(makeIfRowVarBinds(20))

		w, err := NewWatcher[testIfRow](
			context.Background(),
			s,
			makeIfIndicator(t),
			nil,
			testIfDecode,
			testIfEqual,
			testIfMerge,
			WithCadenceBounds(50*time.Millisecond, 1*time.Second),
		)
		if err != nil {
			t.Fatalf("cycle %d NewWatcher: %v", cycle, err)
		}
		// Drain a single event then Close (mid-cold-start).
		<-w.pump.Data()
		_ = w.Close()

		// Each cycle must return goroutine count to baseline within
		// the budget; otherwise nested Walkers are leaking.
		if n := waitForPumpExit(t, baseline, 500*time.Millisecond); n > baseline+1 {
			t.Errorf("cycle %d: goroutines = %d, baseline = %d",
				cycle, n, baseline)
		}
	}
}

// TestWatcher_InterleavedGetDoesNotDeadlock verifies that a
// consumer goroutine can issue Get calls on the shared session while
// the tick goroutine runs, without deadlocking.
func TestWatcher_InterleavedGetDoesNotDeadlock(t *testing.T) {
	s := &watcherFakeSession{}
	s.pushScript(makeIfRowVarBinds(10))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	w, err := NewWatcher[testIfRow](
		ctx,
		s,
		makeIfIndicator(t),
		nil,
		testIfDecode,
		testIfEqual,
		testIfMerge,
		WithCadenceBounds(50*time.Millisecond, 1*time.Second),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	consumed := 0
	for _, ev := range w.Iter() {
		_ = ev
		// Issue a Get from inside the loop body. If the watcher held
		// the session mutex for the entire cold-start, this would
		// deadlock.
		got, err := s.Get(ctx, []OID{ifIndexOID})
		if err != nil {
			t.Fatalf("Get from loop body: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("Get returned %d vbs, want 1", len(got))
		}
		consumed++
		if consumed == 10 {
			break
		}
	}
	if consumed != 10 {
		t.Errorf("consumed %d, want 10", consumed)
	}
	if got := s.getCalls.Load(); got != 10 {
		t.Errorf("getCalls = %d, want 10", got)
	}
}

// --- rowIndex helper unit ---------------------------------------------

// rawOID constructs an OID directly from sub-ids without running
// SMIv2 root validation. Used here only to express expected row-index
// suffixes (e.g., [5] for ifIndex=5) which legitimately violate the
// SMIv2 0|1|2 first-sub-id rule because they are partial OIDs.
func rawOID(subs ...uint32) OID {
	cp := make([]uint32, len(subs))
	copy(cp, subs)
	return OID{subs: cp}
}

func TestRowIndex_StandardCase(t *testing.T) {
	full := MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 2, 5) // ifDescr for ifIndex=5
	root := ifTableRoot
	got := rowIndex(full, root)
	want := rawOID(5)
	if !got.Equal(want) {
		t.Errorf("rowIndex = %s, want %s", got, want)
	}
}

func TestRowIndex_MultiComponentIndex(t *testing.T) {
	// e.g., ifStackTable: 1.3.6.1.2.1.31.1.2.1.1.<high>.<low>
	root := MustOID(1, 3, 6, 1, 2, 1, 31, 1, 2)
	full := MustOID(1, 3, 6, 1, 2, 1, 31, 1, 2, 1, 1, 7, 42) // stackStatus[7,42]
	got := rowIndex(full, root)
	want := rawOID(7, 42)
	if !got.Equal(want) {
		t.Errorf("rowIndex = %s, want %s", got, want)
	}
}

func TestRowIndex_NotUnderRoot(t *testing.T) {
	full := MustOID(1, 3, 6, 1, 2, 1, 4, 4, 1, 2, 5)
	root := ifTableRoot
	got := rowIndex(full, root)
	if got.Len() != 0 {
		t.Errorf("rowIndex = %s, want empty", got)
	}
}

func TestRowIndex_TooShort(t *testing.T) {
	full := MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1)
	root := ifTableRoot
	got := rowIndex(full, root)
	if got.Len() != 0 {
		t.Errorf("rowIndex = %s, want empty", got)
	}
}

// --- deriveTableRoot -------------------------------------------------

func TestDeriveTableRoot_PerRow(t *testing.T) {
	col := fakeColumn{oid: ifLastChange, kind: KindTimeTicks}
	ci := MustChangeIndicator(NewPerRowIndicator(col, ifTableRoot))
	got, err := deriveTableRoot(ci, nil)
	if err != nil {
		t.Fatalf("deriveTableRoot: %v", err)
	}
	if !got.Equal(ifTableRoot) {
		t.Errorf("got %s, want %s", got, ifTableRoot)
	}
}

func TestDeriveTableRoot_SingleCoverageScalar(t *testing.T) {
	scalarOID := MustOID(1, 3, 6, 1, 2, 1, 1, 8)
	roots := []OID{MustOID(1, 3, 6, 1, 2, 1, 1, 9)}
	ci := MustChangeIndicator(NewScalarIndicator(scalarOID, KindTimeTicks, roots))
	got, err := deriveTableRoot(ci, nil)
	if err != nil {
		t.Fatalf("deriveTableRoot: %v", err)
	}
	if !got.Equal(roots[0]) {
		t.Errorf("got %s, want %s", got, roots[0])
	}
}

func TestDeriveTableRoot_MultiCoverageNeedsCols(t *testing.T) {
	scalarOID := MustOID(1, 3, 6, 1, 2, 1, 47, 1, 4, 1)
	roots := []OID{
		MustOID(1, 3, 6, 1, 2, 1, 47, 1, 1, 1),
		MustOID(1, 3, 6, 1, 2, 1, 47, 1, 2, 1),
	}
	ci := MustChangeIndicator(NewScalarIndicator(scalarOID, KindUinteger32, roots))

	if _, err := deriveTableRoot(ci, nil); err == nil {
		t.Error("expected error for multi-coverage scalar with no cols")
	}

	col := fakeColumn{oid: MustOID(1, 3, 6, 1, 2, 1, 47, 1, 2, 1, 1, 2)}
	got, err := deriveTableRoot(ci, []AnyColumn{col})
	if err != nil {
		t.Fatalf("deriveTableRoot: %v", err)
	}
	if !got.Equal(roots[1]) {
		t.Errorf("got %s, want %s", got, roots[1])
	}
}

func TestDeriveTableRoot_MultiCoverageSpansMultipleRejected(t *testing.T) {
	scalarOID := MustOID(1, 3, 6, 1, 2, 1, 47, 1, 4, 1)
	roots := []OID{
		MustOID(1, 3, 6, 1, 2, 1, 47, 1, 1, 1),
		MustOID(1, 3, 6, 1, 2, 1, 47, 1, 2, 1),
	}
	ci := MustChangeIndicator(NewScalarIndicator(scalarOID, KindUinteger32, roots))

	colA := fakeColumn{oid: MustOID(1, 3, 6, 1, 2, 1, 47, 1, 1, 1, 1, 2)}
	colB := fakeColumn{oid: MustOID(1, 3, 6, 1, 2, 1, 47, 1, 2, 1, 1, 2)}
	if _, err := deriveTableRoot(ci, []AnyColumn{colA, colB}); err == nil {
		t.Error("expected error for cols spanning multiple coverage tables")
	}
}

func TestDeriveTableRoot_ColNotUnderAnyCoverageRejected(t *testing.T) {
	scalarOID := MustOID(1, 3, 6, 1, 2, 1, 47, 1, 4, 1)
	roots := []OID{
		MustOID(1, 3, 6, 1, 2, 1, 47, 1, 1, 1),
		MustOID(1, 3, 6, 1, 2, 1, 47, 1, 2, 1),
	}
	ci := MustChangeIndicator(NewScalarIndicator(scalarOID, KindUinteger32, roots))
	col := fakeColumn{oid: ifDescrOID} // ifTable, not under ENTITY-MIB
	if _, err := deriveTableRoot(ci, []AnyColumn{col}); err == nil {
		t.Error("expected error for col outside coverage")
	}
}
