package freeze_test

import (
	"context"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/freeze"
)

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
	g.Freeze(ctx)

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
	g.Freeze(context.Background())

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

	g.Freeze(ctx)
	g.Freeze(ctx)
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
	ctx := context.Background()

	if !g.AllowAcknowledgement() {
		t.Fatal("AllowAcknowledgement() = false while unfrozen")
	}

	g.Freeze(ctx)
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

	freezeDone := make(chan struct{})
	go func() {
		g.Freeze(context.Background())
		close(freezeDone)
	}()

	select {
	case <-freezeDone:
		t.Fatal("Freeze() returned while a side effect entered through Enter had not left")
	case <-time.After(50 * time.Millisecond):
	}

	leave()

	select {
	case <-freezeDone:
	case <-time.After(time.Second):
		t.Fatal("Freeze() did not return after the in-flight side effect left")
	}
}

func TestEnterBlocksAndRetriesAcrossAFreezeRace(t *testing.T) {
	g := freeze.New(nil)
	g.Freeze(context.Background())

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
