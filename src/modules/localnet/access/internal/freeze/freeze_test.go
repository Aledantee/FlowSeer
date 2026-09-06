package freeze_test

import (
	"context"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/freeze"
)

func mustFreeze(t *testing.T, g *freeze.Gate) {
	t.Helper()
	if err := g.Freeze(context.Background()); err != nil {
		t.Fatalf("Freeze() error: %v", err)
	}
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

func TestAllowAcknowledgementIsAlwaysTrue(t *testing.T) {
	g := freeze.New(nil)

	if !g.AllowAcknowledgement() {
		t.Fatal("AllowAcknowledgement() = false while unfrozen")
	}

	mustFreeze(t, g)
	if !g.AllowAcknowledgement() {
		t.Fatal("AllowAcknowledgement() = false while frozen; the acknowledgement barrier must never be gated")
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
