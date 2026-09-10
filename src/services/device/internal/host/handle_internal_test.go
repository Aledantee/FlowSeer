package host

import (
	"context"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// A module that needs the hub waits for it rather than assuming it is up,
// because the runtime starts children concurrently and declaration order is
// not startup order.
func TestAWaiterGetsTheResourcesWhoeverPublishesThemFirst(t *testing.T) {
	handle := newHubHandle()
	want := &busResources{}

	got := make(chan *busResources, 1)
	go func() {
		resources, err := handle.await(context.Background())
		if err != nil {
			t.Errorf("await: %v", err)
		}
		got <- resources
	}()

	handle.publish(want)
	select {
	case resources := <-got:
		if resources != want {
			t.Fatalf("await returned %p, want the published %p", resources, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a waiter did not see the published resources")
	}
}

// A hub that never starts must end its dependents rather than park them. The
// wait is bounded by the module's own attempt context, which the supervisor
// cancels on shutdown.
func TestAWaitEndsWithTheModuleThatIsWaiting(t *testing.T) {
	handle := newHubHandle()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := handle.await(ctx); err == nil {
		t.Fatal("a wait outlived the module doing it")
	} else if code, _ := errs.CodeOf(err); code != ErrCodeHubGone {
		t.Fatalf("error = %v, want code %v", err, ErrCodeHubGone)
	}
}

// The generation property, and the reason the strategy above these modules is
// RestForOne rather than the default: a dependent that starts while the hub is
// down must wait for the hub that is coming, not take the one that has closed.
// A stale handle's connections are shut, and every write through it fails as
// though the bucket were broken.
//
// Withdrawing arms the next wait, so the waiter blocks rather than failing at
// once. Both are safe — a failed Setup is retried by the supervisor — but
// blocking is what makes a hub restart invisible to the modules behind it
// instead of a burst of failed attempts.
func TestAWaiterAfterAWithdrawalWaitsForTheNextHub(t *testing.T) {
	handle := newHubHandle()
	handle.publish(&busResources{})
	handle.withdraw()

	type result struct {
		resources *busResources
		err       error
	}
	got := make(chan result, 1)
	go func() {
		resources, err := handle.await(context.Background())
		got <- result{resources, err}
	}()

	select {
	case r := <-got:
		t.Fatalf("a waiter returned (%p, %v) while no hub was up", r.resources, r.err)
	case <-time.After(100 * time.Millisecond):
	}

	next := &busResources{}
	handle.publish(next)
	select {
	case r := <-got:
		if r.err != nil {
			t.Fatalf("await after the next hub started: %v", r.err)
		}
		if r.resources != next {
			t.Fatalf("await returned %p, want the new hub's %p", r.resources, next)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a waiter did not see the next hub")
	}
}

// The race the wait's second read exists for: a waiter is woken by a publish,
// and the hub is withdrawn before the waiter reads the resources. It must be
// refused rather than handed a hub on its way down — the state is built here
// directly, because provoking the interleaving is not something a test can
// schedule.
func TestAWaiterWokenByAPublishThatWasWithdrawnIsRefused(t *testing.T) {
	woken := make(chan struct{})
	close(woken)
	handle := &hubHandle{ready: woken, current: nil}

	if _, err := handle.await(context.Background()); err == nil {
		t.Fatal("a withdrawn hub was handed to a waiter")
	} else if code, _ := errs.CodeOf(err); code != ErrCodeHubGone {
		t.Fatalf("error = %v, want code %v", err, ErrCodeHubGone)
	}
}
