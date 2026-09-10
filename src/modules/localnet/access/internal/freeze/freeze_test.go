package freeze_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/freeze"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/telemetry"
)

func mustFreeze(t *testing.T, g *freeze.Gate) {
	t.Helper()
	if err := g.Freeze(context.Background()); err != nil {
		t.Fatalf("Freeze() error: %v", err)
	}
}

// eventCounter counts flowseer.device.lane.frozen and .released events,
// safe for concurrent Freeze/Unfreeze calls from multiple goroutines.
type eventCounter struct {
	mu            sync.Mutex
	frozenCount   int
	releasedCount int
}

func (c *eventCounter) Enabled(context.Context, slog.Level) bool { return true }

func (c *eventCounter) Handle(_ context.Context, r slog.Record) error {
	r.Attrs(func(a slog.Attr) bool {
		if a.Key != "otel.event.name" {
			return true
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		switch a.Value.String() {
		case "flowseer.device.lane.frozen":
			c.frozenCount++
		case "flowseer.device.lane.released":
			c.releasedCount++
		}
		return false
	})
	return nil
}

func (c *eventCounter) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *eventCounter) WithGroup(string) slog.Handler      { return c }

func (c *eventCounter) counts() (frozen, released int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.frozenCount, c.releasedCount
}

func TestAwaitSideEffectReturnsImmediatelyWhenNeverFrozen(t *testing.T) {
	g := freeze.New(nil)

	done := make(chan error, 1)
	go func() { done <- g.AwaitSideEffect(context.Background()) }()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("AwaitSideEffect() error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("AwaitSideEffect blocked with no freeze in effect")
	}
}

func TestAwaitSideEffectBlocksWhileFrozenAndReturnsOnUnfreeze(t *testing.T) {
	g := freeze.New(nil)
	ctx := context.Background()
	mustFreeze(t, g)

	if g.AllowSideEffect() {
		t.Fatal("AllowSideEffect() = true while frozen")
	}

	done := make(chan error, 1)
	go func() { done <- g.AwaitSideEffect(context.Background()) }()

	select {
	case <-done:
		t.Fatal("AwaitSideEffect returned before Unfreeze")
	case <-time.After(50 * time.Millisecond):
	}

	g.Unfreeze(ctx)

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("AwaitSideEffect() error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("AwaitSideEffect did not return after Unfreeze")
	}

	if !g.AllowSideEffect() {
		t.Error("AllowSideEffect() = false after Unfreeze")
	}
}

func TestAwaitSideEffectHonorsContextCancellation(t *testing.T) {
	g := freeze.New(nil)
	mustFreeze(t, g)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.AwaitSideEffect(ctx) }()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != ctx.Err() {
			t.Errorf("AwaitSideEffect() error = %v, want %v", err, ctx.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("AwaitSideEffect did not return after context cancellation")
	}
}

func TestDoubleFreezeAndDoubleUnfreezeAreNoOps(t *testing.T) {
	g := freeze.New(nil)
	ctx := context.Background()

	mustFreeze(t, g)
	mustFreeze(t, g)
	if g.AllowSideEffect() {
		t.Fatal("AllowSideEffect() = true after double Freeze")
	}

	g.Unfreeze(ctx)
	if !g.AllowSideEffect() {
		t.Fatal("AllowSideEffect() = false after Unfreeze")
	}
	g.Unfreeze(ctx)
	if !g.AllowSideEffect() {
		t.Fatal("AllowSideEffect() = false after double Unfreeze")
	}
}

func TestFreezeDoesNotReturnWhileASideEffectIsInFlight(t *testing.T) {
	g := freeze.New(nil)
	ctx := context.Background()

	leave, err := g.Enter(ctx)
	if err != nil {
		t.Fatalf("Enter() error: %v", err)
	}

	freezeDone := make(chan error, 1)
	go func() {
		freezeDone <- g.Freeze(context.Background())
	}()

	select {
	case <-freezeDone:
		t.Fatal("Freeze() returned while a side effect entered through Enter had not left")
	case <-time.After(50 * time.Millisecond):
	}

	leave()

	select {
	case err := <-freezeDone:
		if err != nil {
			t.Errorf("Freeze() error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Freeze() did not return after the in-flight side effect left")
	}
}

func TestEnterBlocksAndRetriesAcrossAFreezeRace(t *testing.T) {
	g := freeze.New(nil)
	mustFreeze(t, g)

	done := make(chan error, 1)
	go func() {
		leave, err := g.Enter(context.Background())
		if err == nil {
			leave()
		}
		done <- err
	}()

	select {
	case <-done:
		t.Fatal("Enter() returned while frozen")
	case <-time.After(50 * time.Millisecond):
	}

	g.Unfreeze(context.Background())

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Enter() error = %v, want nil after Unfreeze", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Enter() did not return after Unfreeze")
	}
}

// TestFreezeHonorsContextCancellationWhileWaitingForAnInFlightSideEffect
// proves Freeze does not hang forever behind a write that never finishes:
// a control plane fencing an edge precisely because it has gone
// unresponsive must be able to give up waiting on that same edge's own
// stuck write, rather than blocking indefinitely with no way to cancel.
func TestFreezeHonorsContextCancellationWhileWaitingForAnInFlightSideEffect(t *testing.T) {
	g := freeze.New(nil)

	leave, err := g.Enter(context.Background())
	if err != nil {
		t.Fatalf("Enter() error: %v", err)
	}
	t.Cleanup(leave)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	err = g.Freeze(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Freeze() error = nil, want ctx's deadline exceeded error")
	}
	if elapsed > time.Second {
		t.Fatalf("Freeze() took %v, want it to return at roughly its own 100ms deadline", elapsed)
	}
	// New side effects must still be stopped even though this call gave
	// up waiting for the in-flight one.
	if g.AllowSideEffect() {
		t.Error("AllowSideEffect() = true after Freeze gave up waiting; frozen must stay set")
	}
}

// TestFreezeWaitsEveryCallEvenWhenAlreadyFrozen proves the barrier wait is
// not skipped for a Freeze call that finds the gate already frozen: only
// the durable frozen flag and the LaneFrozen telemetry event are
// idempotent, not the drain proof itself, or a second concurrent Freeze
// call would return immediately while a side effect the first call is
// still waiting on remains in flight.
func TestFreezeWaitsEveryCallEvenWhenAlreadyFrozen(t *testing.T) {
	g := freeze.New(nil)

	leave, err := g.Enter(context.Background())
	if err != nil {
		t.Fatalf("Enter() error: %v", err)
	}

	firstDone := make(chan error, 1)
	go func() { firstDone <- g.Freeze(context.Background()) }()
	time.Sleep(50 * time.Millisecond) // let the first Freeze call start waiting

	secondDone := make(chan error, 1)
	go func() { secondDone <- g.Freeze(context.Background()) }()

	select {
	case <-secondDone:
		t.Fatal("second Freeze() returned while the side effect entered before either call was still in flight")
	case <-time.After(50 * time.Millisecond):
	}

	leave()

	for _, done := range []chan error{firstDone, secondDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Freeze() error = %v, want nil", err)
			}
		case <-time.After(time.Second):
			t.Fatal("a Freeze() call did not return after the in-flight side effect left")
		}
	}
}

// TestFreezeStillAnnouncesAfterAnEarlierAttemptsCtxExpired proves the
// LaneFrozen event is not lost forever when the first Freeze call to
// notice a drained gate never happens: an earlier call whose ctx expired
// before the drain finished sets frozen but must not consume the
// announcement, or every later Freeze call finding frozen already true
// would skip emitting it, leaving Unfreeze's LaneReleased with no matching
// LaneFrozen in the telemetry stream.
func TestFreezeStillAnnouncesAfterAnEarlierAttemptsCtxExpired(t *testing.T) {
	counter := &eventCounter{}
	view, err := telemetry.NewView(telemetry.ViewConfig{Logger: slog.New(counter)})
	if err != nil {
		t.Fatalf("NewView() error: %v", err)
	}
	g := freeze.New(view)

	leave, err := g.Enter(context.Background())
	if err != nil {
		t.Fatalf("Enter() error: %v", err)
	}

	// First attempt: ctx expires before the drain finishes.
	shortCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := g.Freeze(shortCtx); err == nil {
		t.Fatal("Freeze() error = nil, want the short ctx to expire first")
	}
	if frozen, _ := counter.counts(); frozen != 0 {
		t.Fatalf("frozen event count = %d after the expired attempt, want 0", frozen)
	}

	// Second attempt, with time for the drain to finish: this is the call
	// that must announce, since the first one gave up without seeing the
	// drain complete.
	longDone := make(chan error, 1)
	go func() { longDone <- g.Freeze(context.Background()) }()

	time.Sleep(50 * time.Millisecond)
	leave()

	select {
	case err := <-longDone:
		if err != nil {
			t.Fatalf("Freeze() error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second Freeze() did not return after the side effect left")
	}

	if frozen, _ := counter.counts(); frozen != 1 {
		t.Errorf("frozen event count = %d after the drain completed, want exactly 1", frozen)
	}

	g.Unfreeze(context.Background())
	if _, released := counter.counts(); released != 1 {
		t.Errorf("released event count = %d after Unfreeze, want exactly 1 (matching the one frozen event)", released)
	}
}

// TestUnfreezeEmitsNothingIfFreezeNeverAnnounced proves Unfreeze does not
// emit LaneReleased when no Freeze call on this Gate ever finished its
// drain — a release with no matching freeze would be as misleading in the
// telemetry stream as the reverse.
func TestUnfreezeEmitsNothingIfFreezeNeverAnnounced(t *testing.T) {
	counter := &eventCounter{}
	view, err := telemetry.NewView(telemetry.ViewConfig{Logger: slog.New(counter)})
	if err != nil {
		t.Fatalf("NewView() error: %v", err)
	}
	g := freeze.New(view)

	leave, err := g.Enter(context.Background())
	if err != nil {
		t.Fatalf("Enter() error: %v", err)
	}
	defer leave()

	shortCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := g.Freeze(shortCtx); err == nil {
		t.Fatal("Freeze() error = nil, want the short ctx to expire first")
	}

	g.Unfreeze(context.Background())

	frozen, released := counter.counts()
	if frozen != 0 || released != 0 {
		t.Errorf("event counts = (frozen: %d, released: %d), want (0, 0) since Freeze never announced", frozen, released)
	}
}
