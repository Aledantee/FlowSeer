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

// makeOID is a brevity helper for the table tests.
func makeOID(t *testing.T, s string) OID {
	t.Helper()
	o, err := ParseOID(s)
	if err != nil {
		t.Fatalf("ParseOID(%q): %v", s, err)
	}
	return o
}

// makeVarBinds builds n Counter32 VarBinds rooted under 1.3.6.1.2.1.X
// for use as fixed pump inputs in the table tests.
func makeVarBinds(t *testing.T, n int) []VarBind {
	t.Helper()
	out := make([]VarBind, n)
	for i := 0; i < n; i++ {
		oid := makeOID(t, fmt.Sprintf("1.3.6.1.2.1.42.%d", i+1))
		out[i] = Counter32Var{
			Header: Header{OID: oid, Kind: KindCounter32},
			Value:  uint32(100 + i),
		}
	}
	return out
}

// waitForPumpExit polls runtime.NumGoroutine until the value drops to
// the supplied baseline (or below) or the timeout fires. It returns
// the observed goroutine count for diagnostic purposes.
func waitForPumpExit(t *testing.T, baseline int, d time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		// runtime.Gosched and a short sleep give the pump goroutine a
		// chance to run before we sample again.
		runtime.Gosched()
		n := runtime.NumGoroutine()
		if n <= baseline {
			return n
		}
		if time.Now().After(deadline) {
			return n
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestWalker_IterHappyPath: pump produces 5 items, range loop visits
// all 5 in order, Err() nil after.
func TestWalker_IterHappyPath(t *testing.T) {
	ctx := context.Background()
	w := NewWalker(ctx, 0) // default buffer
	vbs := makeVarBinds(t, 5)
	w.Pump(func(_ context.Context) {
		for _, vb := range vbs {
			if !w.Send(vb.GetHeader().OID, vb) {
				return
			}
		}
	})

	got := 0
	for idx, vb := range w.Iter() {
		want := vbs[got]
		if !idx.Equal(want.GetHeader().OID) {
			t.Errorf("idx[%d] = %s, want %s", got, idx, want.GetHeader().OID)
		}
		if vb.GetHeader().OID.Equal(want.GetHeader().OID) != true {
			t.Errorf("vb[%d] oid mismatch", got)
		}
		got++
	}
	if got != 5 {
		t.Errorf("yielded %d items, want 5", got)
	}
	if err := w.Err(); err != nil {
		t.Errorf("Err = %v, want nil", err)
	}
}

// TestWalker_NextHappyPath: same source consumed via Next/Current;
// same totals.
func TestWalker_NextHappyPath(t *testing.T) {
	ctx := context.Background()
	w := NewWalker(ctx, 4)
	vbs := makeVarBinds(t, 5)
	w.Pump(func(_ context.Context) {
		for _, vb := range vbs {
			if !w.Send(vb.GetHeader().OID, vb) {
				return
			}
		}
	})

	got := 0
	for w.Next() {
		idx, vb := w.Current()
		want := vbs[got]
		if !idx.Equal(want.GetHeader().OID) {
			t.Errorf("Current[%d] idx = %s, want %s", got, idx, want.GetHeader().OID)
		}
		if vb == nil {
			t.Errorf("Current[%d] vb = nil", got)
		}
		got++
	}
	if got != 5 {
		t.Errorf("Next yielded %d items, want 5", got)
	}
	if err := w.Err(); err != nil {
		t.Errorf("Err = %v, want nil", err)
	}
}

// TestWalker_EmptyWalk: pump returns immediately; both shapes loop
// zero times; Err() nil.
func TestWalker_EmptyWalk(t *testing.T) {
	t.Run("Iter", func(t *testing.T) {
		w := NewWalker(context.Background(), 0)
		w.Pump(func(_ context.Context) {}) // produces nothing, returns -> Done

		count := 0
		for range w.Iter() {
			count++
		}
		if count != 0 {
			t.Errorf("Iter yielded %d items, want 0", count)
		}
		if err := w.Err(); err != nil {
			t.Errorf("Err = %v, want nil", err)
		}
	})
	t.Run("Next", func(t *testing.T) {
		w := NewWalker(context.Background(), 0)
		w.Pump(func(_ context.Context) {})

		count := 0
		for w.Next() {
			count++
		}
		if count != 0 {
			t.Errorf("Next yielded %d items, want 0", count)
		}
		if err := w.Err(); err != nil {
			t.Errorf("Err = %v, want nil", err)
		}
	})
}

// TestWalker_TerminalError: pump calls Fail with a sentinel; Iter
// exits, Err() returns the sentinel.
func TestWalker_TerminalError(t *testing.T) {
	sentinel := errors.New("pump-go-boom")
	w := NewWalker(context.Background(), 0)
	w.Pump(func(_ context.Context) {
		w.Fail(sentinel)
	})

	count := 0
	for range w.Iter() {
		count++
	}
	if count != 0 {
		t.Errorf("yielded %d items after Fail, want 0", count)
	}
	if err := w.Err(); !errors.Is(err, sentinel) {
		t.Errorf("Err = %v, want %v", err, sentinel)
	}
}

// TestWalker_PumpPanicLeavesErrNonNil is evidence for the spawn.Go
// conversion of [Walker.Pump]: a panicking fn must not silently present as a
// completed walk. Err() is read once, the instant the iteration ends, because
// Walker holds the same ordering guarantee as Watcher — the panic path closes
// the channel through Fail, which records the error before closing. Polling
// here would also pass against a Pump whose deferred Done closed the channel
// first and latched the error afterwards, which is the silent completion this
// asserts against.
func TestWalker_PumpPanicLeavesErrNonNil(t *testing.T) {
	w := NewWalker(context.Background(), 0)
	w.Pump(func(_ context.Context) {
		panic("pump exploded")
	})

	count := 0
	for range w.Iter() {
		count++
	}
	if count != 0 {
		t.Errorf("yielded %d items after panic, want 0", count)
	}

	if err := w.Err(); err == nil {
		t.Error("Err() is nil when the data channel closed after a panicking Pump fn, want non-nil")
	}
}

// TestWalker_ContextCancel: caller cancels ctx; pump's next Send
// returns false; pump exits within a bounded time.
func TestWalker_ContextCancel(t *testing.T) {
	baseline := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := NewWalker(ctx, 1)

	// The pump produces values forever until Send returns false. With
	// a buffer of 1 and a single consumed value, the next Send will
	// block on the channel until cancel arrives.
	pumpExited := make(chan struct{})
	w.Pump(func(_ context.Context) {
		defer close(pumpExited)
		i := 0
		for {
			oid := makeOID(t, fmt.Sprintf("1.3.6.1.4.1.99.%d", i))
			vb := Counter32Var{Header: Header{OID: oid, Kind: KindCounter32}, Value: uint32(i)}
			if !w.Send(oid, vb) {
				return
			}
			i++
		}
	})

	// Consume one value to let the pump start, then cancel.
	gotFirst := false
	for idx, vb := range w.Iter() {
		_ = idx
		_ = vb
		gotFirst = true
		cancel()
		break
	}
	if !gotFirst {
		t.Fatal("never consumed first value")
	}

	select {
	case <-pumpExited:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("pump did not exit within 500ms after context cancel")
	}

	n := waitForPumpExit(t, baseline, 500*time.Millisecond)
	if n > baseline+1 {
		t.Errorf("goroutine count after pump exit = %d, baseline = %d", n, baseline)
	}
}

// TestWalker_BreakLoop: range loop with early break causes pump to
// terminate; goroutine exits.
func TestWalker_BreakLoop(t *testing.T) {
	baseline := runtime.NumGoroutine()
	w := NewWalker(context.Background(), 1)

	pumpExited := make(chan struct{})
	w.Pump(func(_ context.Context) {
		defer close(pumpExited)
		i := 0
		for {
			oid := makeOID(t, fmt.Sprintf("1.3.6.1.4.1.99.%d", i))
			vb := Counter32Var{Header: Header{OID: oid, Kind: KindCounter32}, Value: uint32(i)}
			if !w.Send(oid, vb) {
				return
			}
			i++
		}
	})

	count := 0
	for range w.Iter() {
		count++
		if count == 3 {
			break
		}
	}
	if count != 3 {
		t.Errorf("loop visited %d, want 3", count)
	}

	select {
	case <-pumpExited:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("pump did not exit within 500ms after break")
	}
	if n := waitForPumpExit(t, baseline, 500*time.Millisecond); n > baseline+1 {
		t.Errorf("goroutine count after break = %d, baseline = %d", n, baseline)
	}
}

// TestWalker_MixingShapesIsSafe: alternate Iter and Next within the
// same source. The exact distribution is undefined but the test
// verifies no panic, no deadlock, and the total never exceeds the
// pump's input.
func TestWalker_MixingShapesIsSafe(t *testing.T) {
	w := NewWalker(context.Background(), 0)
	vbs := makeVarBinds(t, 8)
	w.Pump(func(_ context.Context) {
		for _, vb := range vbs {
			if !w.Send(vb.GetHeader().OID, vb) {
				return
			}
		}
	})

	totalSeen := 0
	// Alternate one Iter step with one Next call.
	iterDone := false
	for idx, vb := range w.Iter() {
		_ = idx
		_ = vb
		totalSeen++
		if !w.Next() {
			iterDone = true
			break
		}
		_, _ = w.Current()
		totalSeen++
	}
	_ = iterDone

	if totalSeen > len(vbs) {
		t.Errorf("totalSeen = %d, exceeds pump input %d", totalSeen, len(vbs))
	}
	if err := w.Err(); err != nil {
		t.Errorf("Err = %v, want nil", err)
	}
}

// TestWalker_Close: calling Close terminates iteration within a
// bounded time and the pump goroutine exits.
//
// The consumer is allowed to drain in-flight buffered items before
// reaching the end of the iteration (the contract guarantees prompt
// termination, not zero post-Close items). We bound the total observed
// items relative to wall time: the pump is producing values in a tight
// loop, but once Close fires the cancel case in the Send select wins
// within a small number of select rounds.
func TestWalker_Close(t *testing.T) {
	baseline := runtime.NumGoroutine()
	w := NewWalker(context.Background(), 1)

	pumpExited := make(chan struct{})
	w.Pump(func(_ context.Context) {
		defer close(pumpExited)
		i := 0
		for {
			oid := makeOID(t, fmt.Sprintf("1.3.6.1.4.1.99.%d", i))
			vb := Counter32Var{Header: Header{OID: oid, Kind: KindCounter32}, Value: uint32(i)}
			if !w.Send(oid, vb) {
				return
			}
			i++
		}
	})

	// Drain a few, then Close from outside the iter loop. Once Close
	// fires, the pump's Send select picks the ctx.Done() branch within
	// a bounded number of iterations.
	consumed := 0
	closeStarted := make(chan struct{})
	closeFired := make(chan struct{})
	go func() {
		<-closeStarted
		_ = w.Close()
		close(closeFired)
	}()
	done := make(chan struct{})
	go func() {
		for range w.Iter() {
			consumed++
			if consumed == 5 {
				close(closeStarted)
			}
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("consumer did not terminate within 2s of Close")
	}
	<-closeFired // ensure Close has been called

	select {
	case <-pumpExited:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("pump did not exit within 500ms after Close")
	}
	if n := waitForPumpExit(t, baseline, 500*time.Millisecond); n > baseline+2 {
		t.Errorf("goroutine count after Close = %d, baseline = %d", n, baseline)
	}

	// Close is idempotent.
	if err := w.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// TestWalker_BindRow: BindRow over a Walker with 3 VarBinds with a
// bind closure that fills a struct's field; yields 3 (idx, row) pairs.
func TestWalker_BindRow(t *testing.T) {
	w := NewWalker(context.Background(), 0)
	vbs := makeVarBinds(t, 3)
	w.Pump(func(_ context.Context) {
		for _, vb := range vbs {
			if !w.Send(vb.GetHeader().OID, vb) {
				return
			}
		}
	})

	type row struct {
		value uint32
	}
	bind := func(_ OID, vb VarBind, r *row) error {
		c, ok := vb.(Counter32Var)
		if !ok {
			return ErrTypeMismatch
		}
		r.value = c.Value
		return nil
	}

	count := 0
	for idx, r := range BindRow[row](w, bind) {
		want := vbs[count].(Counter32Var)
		if !idx.Equal(want.GetHeader().OID) {
			t.Errorf("row[%d] idx = %s, want %s", count, idx, want.GetHeader().OID)
		}
		if r.value != want.Value {
			t.Errorf("row[%d] value = %d, want %d", count, r.value, want.Value)
		}
		count++
	}
	if count != 3 {
		t.Errorf("BindRow yielded %d rows, want 3", count)
	}
	if err := w.Err(); err != nil {
		t.Errorf("Err = %v, want nil", err)
	}
}

// TestWalker_BindRow_BindErrorSurfaces: bind closure returns an error;
// BindRow stops yielding and Walker.Err() reports it.
func TestWalker_BindRow_BindErrorSurfaces(t *testing.T) {
	baseline := runtime.NumGoroutine()
	w := NewWalker(context.Background(), 1)
	pumpExited := make(chan struct{})
	w.Pump(func(_ context.Context) {
		defer close(pumpExited)
		i := 0
		for {
			oid := makeOID(t, fmt.Sprintf("1.3.6.1.4.1.99.%d", i))
			vb := Counter32Var{Header: Header{OID: oid, Kind: KindCounter32}, Value: uint32(i)}
			if !w.Send(oid, vb) {
				return
			}
			i++
		}
	})

	bindErr := errors.New("bind-go-boom")
	type row struct{ v uint32 }
	bind := func(_ OID, vb VarBind, r *row) error {
		c := vb.(Counter32Var)
		if c.Value == 2 {
			return bindErr
		}
		r.v = c.Value
		return nil
	}

	count := 0
	for range BindRow[row](w, bind) {
		count++
		if count > 100 {
			t.Fatal("BindRow did not terminate on bind error")
		}
	}
	// We yielded up to 2 successful rows (values 0 and 1) before the
	// error fires; the exact count depends on buffering. Bound the
	// upper end and assert the error.
	if count > 2 {
		t.Errorf("BindRow yielded %d before error, want <= 2", count)
	}
	if err := w.Err(); !errors.Is(err, bindErr) {
		t.Errorf("Err = %v, want %v", err, bindErr)
	}

	select {
	case <-pumpExited:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("pump did not exit within 500ms after BindRow error")
	}
	if n := waitForPumpExit(t, baseline, 500*time.Millisecond); n > baseline+1 {
		t.Errorf("goroutine count after BindRow error = %d, baseline = %d", n, baseline)
	}
}

// TestWalker_FailIdempotent: calling Fail multiple times records the
// first non-nil error and is safe under concurrent invocation.
func TestWalker_FailIdempotent(t *testing.T) {
	first := errors.New("first")
	second := errors.New("second")
	w := NewWalker(context.Background(), 0)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); w.Fail(first) }()
	go func() { defer wg.Done(); w.Fail(second) }()
	wg.Wait()

	got := w.Err()
	if !errors.Is(got, first) && !errors.Is(got, second) {
		t.Errorf("Err = %v, want first or second", got)
	}

	// Iter over a Fail-closed channel must return immediately.
	count := 0
	for range w.Iter() {
		count++
	}
	if count != 0 {
		t.Errorf("Iter yielded %d items after Fail, want 0", count)
	}
}

// TestWalker_DoneAfterFailIsNoop: Done after Fail keeps the original
// error and does not panic on a second channel close.
func TestWalker_DoneAfterFailIsNoop(t *testing.T) {
	sentinel := errors.New("first-error")
	w := NewWalker(context.Background(), 0)
	w.Fail(sentinel)
	w.Done() // must not panic
	if err := w.Err(); !errors.Is(err, sentinel) {
		t.Errorf("Err = %v, want %v", err, sentinel)
	}
}

// ktdFakeSession is an in-memory Session used to exercise the
// concurrency invariant: a caller ranging over walker.Iter() can issue
// sess.Get from the loop body without deadlocking, because the pump
// holds the (fake) lock only per-PDU.
type ktdFakeSession struct {
	mu       sync.Mutex
	getCalls atomic.Uint64

	// pdus is the queue of (OID, VarBind) batches the fake pump
	// returns one PDU at a time.
	pdus [][]VarBind
}

func (s *ktdFakeSession) Get(_ context.Context, oids []OID, _ ...CallOption) ([]VarBind, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getCalls.Add(1)
	out := make([]VarBind, len(oids))
	for i, o := range oids {
		out[i] = Counter32Var{Header: Header{OID: o, Kind: KindCounter32}, Value: 7}
	}
	return out, nil
}

func (s *ktdFakeSession) walk(ctx context.Context) *Walker {
	w := NewWalker(ctx, 4)
	w.Pump(func(_ context.Context) {
		for _, batch := range s.pdus {
			// Per-PDU mutex acquisition: release between PDUs
			// so a caller in the consumer goroutine can interleave Get.
			s.mu.Lock()
			pduCopy := make([]VarBind, len(batch))
			copy(pduCopy, batch)
			s.mu.Unlock()

			for _, vb := range pduCopy {
				if !w.Send(vb.GetHeader().OID, vb) {
					return
				}
			}
		}
	})
	return w
}

// TestWalker_InterleavedGetDoesNotDeadlock: ranging over a Walker
// while issuing Get from the loop body must not deadlock. The fake
// Session releases its mutex between PDUs; the Get inside the loop
// body acquires the mutex briefly each time.
func TestWalker_InterleavedGetDoesNotDeadlock(t *testing.T) {
	const rows = 10
	vbs := makeVarBinds(t, rows)
	// Split into 5 PDUs of 2 VarBinds each so the loop crosses
	// PDU boundaries multiple times.
	var pdus [][]VarBind
	for i := 0; i < len(vbs); i += 2 {
		pdus = append(pdus, vbs[i:i+2])
	}
	s := &ktdFakeSession{pdus: pdus}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	w := s.walk(ctx)

	consumed := 0
	for idx, vb := range w.Iter() {
		_ = vb
		// Issue a Get from inside the loop body. If the pump held the
		// session mutex for the whole walk, this would deadlock.
		got, err := s.Get(ctx, []OID{idx})
		if err != nil {
			t.Fatalf("Get from loop body: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("Get returned %d vbs, want 1", len(got))
		}
		consumed++
	}
	if consumed != rows {
		t.Errorf("consumed %d rows, want %d", consumed, rows)
	}
	if s.getCalls.Load() != uint64(rows) {
		t.Errorf("Get calls = %d, want %d", s.getCalls.Load(), rows)
	}
	if err := w.Err(); err != nil {
		t.Errorf("Err = %v, want nil", err)
	}
}

// TestWalker_NilOrEmptyOpts: a Walker constructed with bufferSize <= 0
// uses the default buffer and works normally.
func TestWalker_DefaultBufferSize(t *testing.T) {
	w := NewWalker(context.Background(), -1)
	if cap(w.pump.Data()) != defaultRowBuffer {
		t.Errorf("buffer cap = %d, want %d", cap(w.pump.Data()), defaultRowBuffer)
	}
	w.Done()
}
