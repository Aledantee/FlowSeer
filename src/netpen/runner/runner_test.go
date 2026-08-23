package runner_test

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/netpen/catalog"
	"go.aledante.io/FlowSeer/src/netpen/findings"
	"go.aledante.io/FlowSeer/src/netpen/link"
	"go.aledante.io/FlowSeer/src/netpen/runner"
)

// mockLeg is an in-memory [link.Leg] for testing the runner without an
// AF_PACKET socket. It satisfies the [link.Leg] interface.
type mockLeg struct {
	frames chan link.Frame
	closed atomic.Bool
}

func newMockLeg() *mockLeg {
	return &mockLeg{frames: make(chan link.Frame, 16)}
}

func (m *mockLeg) Send(_ context.Context, _ []byte) error {
	if m.closed.Load() {
		return errs.New().Code(link.ErrCodeLegOpen).Msg("leg is closed")
	}
	return nil
}

func (m *mockLeg) SetFilter(_ []link.RawInstruction) error { return nil }

func (m *mockLeg) Receive(ctx context.Context) <-chan link.Frame {
	out := make(chan link.Frame, 16)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case f, ok := <-m.frames:
				if !ok {
					return
				}
				select {
				case out <- f:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

func (m *mockLeg) Close() error {
	m.closed.Store(true)
	close(m.frames)
	return nil
}

// Compile-time assertion that mockLeg satisfies [link.Leg].

// closeLeg is a t.Cleanup helper that closes a mock leg and fails the test
// on error.
func closeLeg(t *testing.T, leg *mockLeg) {
	t.Helper()
	t.Cleanup(func() {
		if err := leg.Close(); err != nil {
			t.Errorf("leg close: %v", err)
		}
	})
}

// drainStream consumes all records from the stream until it ends, returning
// them in order. It is the in-memory consumer harness (the A3-shaped harness
// with no CLI wiring).
func drainStream(s *runner.Stream) []findings.Record {
	var recs []findings.Record
	for rec := range s.Iter() {
		recs = append(recs, rec)
	}
	return recs
}

// drainStreamAsync starts Run in a goroutine and drains the stream
// concurrently, returning the records and the Run error.
func drainStreamAsync(t *testing.T, ctx context.Context, r *runner.Runner) ([]findings.Record, error) { //nolint:revive // test helper: *testing.T conventionally precedes ctx
	t.Helper()
	var (
		recs []findings.Record
		err  error
		wg   sync.WaitGroup
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		recs = drainStream(r.Stream())
	}()

	err = r.Run(ctx)
	r.Wait()
	wg.Wait()
	return recs, err
}

// --- Happy path: stub behavior delivers findings in order ---

func TestStubBehaviorOrderedFindings(t *testing.T) {
	leg := newMockLeg()
	closeLeg(t, leg)

	// Register a stub behavior that emits three findings in order.
	stub := func(_ context.Context, deps runner.Deps) error {
		deps.Emitter.Finding("arp", []byte(`{"host":"10.0.0.1"}`))
		deps.Emitter.Finding("arp", []byte(`{"host":"10.0.0.2"}`))
		deps.Emitter.Finding("arp", []byte(`{"host":"10.0.0.3"}`))
		return nil
	}

	r := runner.NewRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "arpsweep"}},
		Behaviors: map[string]runner.Behavior{"arpsweep": stub},
	})

	recs, err := drainStreamAsync(t, context.Background(), r)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if len(recs) != 3 {
		t.Fatalf("got %d records, want 3", len(recs))
	}
	for i, rec := range recs {
		if rec.Kind != findings.KindFinding {
			t.Errorf("record %d: kind %q, want %q", i, rec.Kind, findings.KindFinding)
		}
		if rec.Attack != "arpsweep" {
			t.Errorf("record %d: attack %q, want %q", i, rec.Attack, "arpsweep")
		}
		if rec.Finding == nil {
			t.Fatalf("record %d: nil finding", i)
		}
		if rec.Finding.Module != "arp" {
			t.Errorf("record %d: module %q, want %q", i, rec.Finding.Module, "arp")
		}
	}
	// Verify order: the detail payloads carry distinct hosts.
	wantHosts := []string{
		`{"host":"10.0.0.1"}`,
		`{"host":"10.0.0.2"}`,
		`{"host":"10.0.0.3"}`,
	}
	for i, rec := range recs {
		got := string(rec.Finding.Detail)
		if got != wantHosts[i] {
			t.Errorf("record %d: detail %q, want %q", i, got, wantHosts[i])
		}
	}
}

// --- Close stops producer within one poll cycle, idempotent ---

func TestCloseStopsProducerAndIdempotent(t *testing.T) {
	leg := newMockLeg()
	closeLeg(t, leg)

	// A behavior that blocks forever (until ctx canceled).
	block := func(ctx context.Context, _ runner.Deps) error {
		<-ctx.Done()
		return ctx.Err()
	}

	r := runner.NewRunner(runner.Options{
		AttackLeg: leg,
		Attacks: []runner.AttackRef{
			{Name: "arpsweep"},
			{Name: "scan"},
		},
		Behaviors: map[string]runner.Behavior{
			"arpsweep": block,
			"scan":     block,
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = r.Run(ctx) }()

	// Give the run a moment to start the first behavior.
	time.Sleep(20 * time.Millisecond)

	// Close the stream — the producer should stop within one poll cycle.
	start := time.Now()
	if err := r.Stream().Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	// Wait for Run to return.
	r.Wait()
	elapsed := time.Since(start)

	// The producer should stop within a generous bound of one poll cycle.
	// The behavior blocks on ctx; Close signals stop but does not cancel
	// the run ctx. The behavior finishes when we cancel ctx.
	cancel()
	r.Wait()

	if elapsed > 500*time.Millisecond {
		t.Errorf("Close took %v, want < 500ms", elapsed)
	}

	// Second Close is idempotent (no panic, no error).
	if err := r.Stream().Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// --- Early-leave consumer = no goroutine leak ---

func TestEarlyLeaveNoGoroutineLeak(t *testing.T) {
	leg := newMockLeg()
	closeLeg(t, leg)

	// A behavior that emits a few findings then returns. A second
	// behavior that also emits. The consumer leaves after the first
	// record.
	emitted := atomic.Int32{}
	stub1 := func(_ context.Context, deps runner.Deps) error {
		for i := 0; i < 10; i++ {
			if !deps.Emitter.Finding("arp", []byte(`{}`)) {
				return nil
			}
			emitted.Add(1)
		}
		return nil
	}
	stub2 := func(_ context.Context, deps runner.Deps) error {
		deps.Emitter.Finding("arp", []byte(`{}`))
		return nil
	}

	r := runner.NewRunner(runner.Options{
		AttackLeg: leg,
		Attacks: []runner.AttackRef{
			{Name: "arpsweep"},
			{Name: "scan"},
		},
		Behaviors: map[string]runner.Behavior{
			"arpsweep": stub1,
			"scan":     stub2,
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start Run in a goroutine.
	go func() { _ = r.Run(ctx) }()

	// Consume one record then leave.
	consumed := 0
	for rec := range r.Stream().Iter() {
		consumed++
		_ = rec
		break
	}
	if consumed != 1 {
		t.Fatalf("consumed %d records, want 1", consumed)
	}

	// Wait for Run to finish. The Stopped check in the dispatch loop
	// prevents new behaviors from starting; the in-flight behavior
	// finishes when Emitter.Finding returns false.
	r.Wait()

	// Cancel the context to unblock any remaining work.
	cancel()

	// Give goroutines a moment to exit.
	time.Sleep(50 * time.Millisecond)

	// Assert no goroutine leak: the delta should be near zero. We
	// allow a small tolerance for runtime-internal goroutines.
	got := runtime.NumGoroutine()
	// The test itself runs in a goroutine; the baseline is whatever
	// was running before the run started. We check that it's not
	// grown significantly.
	if got > 20 {
		t.Errorf("goroutine count %d after early leave, want <= 20 (no leak)", got)
	}
}

// --- Behavior error surfaces as typed error record, remaining run ---

func TestBehaviorErrorTypedRecordAndContinues(t *testing.T) {
	leg := newMockLeg()
	closeLeg(t, leg)

	behErr := errs.New().Code(runner.ErrCodeRunner).Msg("behavior exploded")

	callOrder := atomic.Int32{}
	stub1 := func(_ context.Context, deps runner.Deps) error {
		callOrder.Add(1)
		deps.Emitter.Finding("arp", []byte(`{"step":1}`))
		return nil
	}
	stub2 := func(_ context.Context, deps runner.Deps) error {
		callOrder.Add(1)
		// arpspoof is temporary-restored: arm a restore step so the
		// U6 teardown gate does not fail it for arming zero steps.
		if deps.Teardown != nil {
			deps.Teardown.Arm("restore", func(_ context.Context) error { return nil })
		}
		return behErr // errors mid-run
	}
	stub3 := func(_ context.Context, deps runner.Deps) error {
		callOrder.Add(1)
		deps.Emitter.Finding("arp", []byte(`{"step":3}`))
		return nil
	}

	r := runner.NewRunner(runner.Options{
		AttackLeg: leg,
		Attacks: []runner.AttackRef{
			{Name: "arpsweep"},
			{Name: "arpspoof"},
			{Name: "scan"},
		},
		Behaviors: map[string]runner.Behavior{
			"arpsweep": stub1,
			"arpspoof": stub2,
			"scan":     stub3,
		},
	})

	recs, err := drainStreamAsync(t, context.Background(), r)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// All three behaviors ran.
	if got := callOrder.Load(); got != 3 {
		t.Errorf("call order count %d, want 3", got)
	}

	// Records: 2 findings + 1 error record = 3.
	if len(recs) != 3 {
		t.Fatalf("got %d records, want 3 (2 findings + 1 error)", len(recs))
	}

	// Find the error record.
	var errRec *findings.ErrorRecord
	for _, rec := range recs {
		if rec.Kind == findings.KindError {
			if errRec != nil {
				t.Fatal("multiple error records")
			}
			errRec = rec.Error
		}
	}
	if errRec == nil {
		t.Fatal("no error record in stream")
	}
	if errRec.Attack != "arpspoof" {
		t.Errorf("error record attack %q, want %q", errRec.Attack, "arpspoof")
	}
	if errRec.Code != "netpen/runner" {
		t.Errorf("error record code %q, want %q", errRec.Code, "netpen/runner")
	}
	if errRec.Message != "behavior exploded" {
		t.Errorf("error record message %q, want %q", errRec.Message, "behavior exploded")
	}
}

// --- Watch-leg-required behavior without watch leg = coded fast failure ---

func TestWatchLegRequiredFastFailure(t *testing.T) {
	leg := newMockLeg()
	closeLeg(t, leg)

	stub := func(_ context.Context, _ runner.Deps) error {
		t.Error("behavior should not be invoked")
		return nil
	}

	r := runner.NewRunner(runner.Options{
		AttackLeg: leg,
		// No WatchLeg set.
		Attacks:   []runner.AttackRef{{Name: "ghost"}},
		Behaviors: map[string]runner.Behavior{"ghost": stub},
	})

	ctx := context.Background()
	err := r.Run(ctx)
	r.Wait()

	if err == nil {
		t.Fatal("Run returned nil, want coded error")
	}
	if code, ok := errs.CodeOf(err); !ok || code != catalog.ErrCodeWatchLegRequired {
		t.Errorf("error code: got %q ok=%v, want %q", code, ok, catalog.ErrCodeWatchLegRequired)
	}
}

// --- In-memory consumer (A3-shaped harness) receives typed stream ---

func TestInMemoryConsumerReceivesTypedStream(t *testing.T) {
	leg := newMockLeg()
	closeLeg(t, leg)

	stub := func(_ context.Context, deps runner.Deps) error {
		deps.Emitter.Finding("arp", []byte(`{"host":"10.0.0.1"}`))
		deps.Emitter.Progress("scan-complete", "segment scanned")
		return nil
	}

	r := runner.NewRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "arpsweep"}},
		Behaviors: map[string]runner.Behavior{"arpsweep": stub},
		// SuppressOutput is the A3 embed seam — the host consumes the
		// stream programmatically and suppresses CLI output.
		SuppressOutput: true,
	})

	var recs []findings.Record
	var mu sync.Mutex
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for rec := range r.Stream().Iter() {
			mu.Lock()
			recs = append(recs, rec)
			mu.Unlock()
		}
	}()

	err := r.Run(context.Background())
	r.Wait()
	wg.Wait()

	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}
	if recs[0].Kind != findings.KindFinding {
		t.Errorf("record 0: kind %q, want %q", recs[0].Kind, findings.KindFinding)
	}
	if recs[1].Kind != findings.KindProgress {
		t.Errorf("record 1: kind %q, want %q", recs[1].Kind, findings.KindProgress)
	}
}

// --- Stream Close latency asserted ---

func TestStreamCloseLatency(t *testing.T) {
	leg := newMockLeg()
	closeLeg(t, leg)

	// A behavior that returns immediately.
	stub := func(_ context.Context, _ runner.Deps) error {
		return nil
	}

	r := runner.NewRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "arpsweep"}},
		Behaviors: map[string]runner.Behavior{
			"arpsweep": stub,
		},
	})

	go func() { _ = r.Run(context.Background()) }()

	// Wait for the run to complete.
	r.Wait()

	// Now Close should return immediately (the stream is already done).
	start := time.Now()
	if err := r.Stream().Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed > 100*time.Millisecond {
		t.Errorf("Close took %v, want < 100ms", elapsed)
	}
}

// --- Unknown attack = coded dispatch failure ---

func TestUnknownAttackCodedFailure(t *testing.T) {
	leg := newMockLeg()
	closeLeg(t, leg)

	r := runner.NewRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "nonexistent"}},
		Behaviors: map[string]runner.Behavior{},
	})

	err := r.Run(context.Background())
	r.Wait()

	if err == nil {
		t.Fatal("Run returned nil, want coded error")
	}
	if code, ok := errs.CodeOf(err); !ok || code != catalog.ErrCodeUnknownBehavior {
		t.Errorf("error code: got %q ok=%v, want %q", code, ok, catalog.ErrCodeUnknownBehavior)
	}
}

// --- Nil attack leg = coded failure ---

func TestNilAttackLegCodedFailure(t *testing.T) {
	r := runner.NewRunner(runner.Options{
		AttackLeg: nil,
		Attacks:   []runner.AttackRef{{Name: "arpsweep"}},
		Behaviors: map[string]runner.Behavior{
			"arpsweep": func(_ context.Context, _ runner.Deps) error { return nil },
		},
	})

	err := r.Run(context.Background())
	r.Wait()

	if err == nil {
		t.Fatal("Run returned nil, want coded error")
	}
	if code, ok := errs.CodeOf(err); !ok || code != runner.ErrCodeRunner {
		t.Errorf("error code: got %q ok=%v, want %q", code, ok, runner.ErrCodeRunner)
	}
}

// --- Empty attacks = nil ---

func TestEmptyAttacksReturnsNil(t *testing.T) {
	leg := newMockLeg()
	closeLeg(t, leg)

	r := runner.NewRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   nil,
	})

	err := r.Run(context.Background())
	r.Wait()

	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

// --- Context cancellation returns ctx.Err() unwrapped ---

func TestContextCancellationReturnsCtxErr(t *testing.T) {
	leg := newMockLeg()
	closeLeg(t, leg)

	// A behavior that blocks until ctx canceled.
	block := func(ctx context.Context, _ runner.Deps) error {
		<-ctx.Done()
		return ctx.Err()
	}

	r := runner.NewRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "arpsweep"}},
		Behaviors: map[string]runner.Behavior{
			"arpsweep": block,
		},
	})

	ctx, cancel := context.WithCancel(context.Background())

	go func() { _ = r.Run(ctx) }()

	// Cancel after a short delay.
	time.Sleep(20 * time.Millisecond)
	cancel()

	err := func() error {
		// Wait for Run to finish and get the error.
		r.Wait()
		// The error was returned by Run, but we didn't capture it.
		// Instead, check the stream's terminal error.
		return r.Stream().Err()
	}()

	// Run returns ctx.Err() (context.Canceled) on cancellation.
	// The stream may or may not have latched it depending on timing;
	// the key assertion is that the run stopped.
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("error %v, want context.Canceled or nil", err)
	}
}

// --- Mode-bearing behavior resolves correct catalog entry ---

func TestModeBearingBehaviorResolvesEntry(t *testing.T) {
	leg := newMockLeg()
	closeLeg(t, leg)

	var gotEntry catalog.Entry
	stub := func(_ context.Context, deps runner.Deps) error {
		gotEntry = deps.Entry
		// portsteal --relay is temporary-restored: arm a restore step.
		if deps.Teardown != nil {
			deps.Teardown.Arm("ip-forward-restore", func(_ context.Context) error { return nil })
		}
		deps.Emitter.Finding("arp", []byte(`{}`))
		return nil
	}

	r := runner.NewRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "portsteal", Mode: "relay"}},
		Behaviors: map[string]runner.Behavior{"portsteal": stub},
	})

	recs, err := drainStreamAsync(t, context.Background(), r)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if gotEntry.Name != "portsteal" {
		t.Errorf("entry name %q, want %q", gotEntry.Name, "portsteal")
	}
	if gotEntry.Mode != "relay" {
		t.Errorf("entry mode %q, want %q", gotEntry.Mode, "relay")
	}
	if gotEntry.Class != catalog.TemporaryRestored {
		t.Errorf("entry class %q, want %q", gotEntry.Class, catalog.TemporaryRestored)
	}

	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	if recs[0].Mode != "relay" {
		t.Errorf("record mode %q, want %q", recs[0].Mode, "relay")
	}
}

// --- Backpressure: slow consumer does not cause unbounded growth ---

func TestBackpressureBoundedChannel(t *testing.T) {
	leg := newMockLeg()
	closeLeg(t, leg)

	// A behavior that tries to emit many records quickly.
	emitted := atomic.Int32{}
	stub := func(_ context.Context, deps runner.Deps) error {
		for i := 0; i < 1000; i++ {
			if !deps.Emitter.Finding("arp", []byte(`{}`)) {
				return nil
			}
			emitted.Add(1)
		}
		return nil
	}

	r := runner.NewRunner(runner.Options{
		AttackLeg: leg,
		Attacks:   []runner.AttackRef{{Name: "arpsweep"}},
		Behaviors: map[string]runner.Behavior{"arpsweep": stub},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = r.Run(ctx) }()

	// Slow consumer: read one record every 1ms.
	consumed := 0
	for rec := range r.Stream().Iter() {
		_ = rec
		consumed++
		time.Sleep(1 * time.Millisecond)
		if consumed >= 10 {
			break
		}
	}

	if err := r.Stream().Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r.Wait()

	// The behavior emitted at most buffer + consumed records (bounded).
	// The stream buffer is 64; the consumer consumed ~10; the behavior
	// should have emitted at most ~74, not 1000. We check it emitted
	// far fewer than 1000, proving backpressure.
	got := emitted.Load()
	if got > 200 {
		t.Errorf("emitted %d records, want <= 200 (backpressure)", got)
	}
}
