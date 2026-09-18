package host

import (
	"context"
	"sync"

	"github.com/nats-io/nats.go/jetstream"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/captureapi"
	"go.aledante.io/FlowSeer/src/services/device/internal/dispatchapi"
	"go.aledante.io/FlowSeer/src/services/device/internal/edgestore"
	"go.aledante.io/FlowSeer/src/services/device/internal/journal"
)

// ErrCodeHubGone is a module that needed the hub and stopped before one was
// available. It is the shutdown path, not a failure of the module.
var ErrCodeHubGone = errs.NewCode("host/hub-gone")

// busResources is everything that exists only while the hub does: the
// connections into its accounts, the buckets the journal and the edge store
// write through, and the relay built over them. They are built once per hub
// attempt and thrown away with it.
type busResources struct {
	hub         *edgebus.Hub
	lanes       jetstream.KeyValue
	journal     *journal.Journal
	edges       *edgestore.Store
	dispatch    *dispatchapi.Service
	captures    *captureapi.Store
	broadcaster *captureapi.Broadcaster
}

// hubHandle carries the hub's resources from the module that owns them to the
// four that need them.
//
// It exists because the service runtime starts a supervisor's children
// concurrently: runWithStarted schedules every child and returns before any
// Setup has begun, so declaration order is not startup order and a module
// cannot assume the hub is up because it is listed first. Each dependent waits
// here instead, bounded by its own attempt context, so a hub that never starts
// ends its dependents rather than parking them.
//
// The generation is the part that is easy to get wrong. A dependent reads
// these resources once, at Setup, and uses them for the whole attempt — a fact
// established at startup and relied on long afterwards. If the hub attempt
// ended and restarted underneath it, that dependent would be holding a closed
// connection and every write through it would fail in a way that reads like a
// broken bucket. What prevents it is not this type: it is the root
// supervisor's RestForOne strategy with the hub declared first, which ends and
// rebuilds every module after the hub whenever the hub itself is rebuilt. The
// strategy is load-bearing, not a default, and withdraw below is what makes
// the next generation's waiters block for the new hub rather than take the old
// one.
//
// What makes the swap safe today is narrower than it looks, and an earlier
// version of this comment gave the wrong reason — that a failed Setup is
// retried. It is not that. A hub attempt that has published can only end
// through supervisor cancellation: its runner returns solely on ctx.Done, and
// the service declares no message bus, so there is no delivery goroutine that
// could end the attempt on its own. Cancellation reaches the dependents first,
// because RestForOne quiesces every module after the hub before the hub's own
// replacement starts. So no dependent is ever running against a withdrawn
// generation.
//
// The distinction matters to whoever changes the runtime next. "A failed Setup
// is retried" would still be true of a runtime where a module could end its
// own attempt, and that runtime would break this — a hub whose runner returned
// for its own reasons would withdraw while its dependents were mid-write. A
// true conclusion from a false reason is worse than no comment, because the
// person checking whether their change is safe checks the reason.
type hubHandle struct {
	mu      sync.Mutex
	ready   chan struct{}
	current *busResources
}

func newHubHandle() *hubHandle {
	return &hubHandle{ready: make(chan struct{})}
}

// publish makes the resources available to the waiters of this generation.
func (h *hubHandle) publish(resources *busResources) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.current = resources
	select {
	case <-h.ready:
		// Already closed: a publish without an intervening withdraw. Nothing
		// to signal.
	default:
		close(h.ready)
	}
}

// withdraw retires this generation's resources and arms the next wait, so a
// dependent that starts while the hub is down blocks for the hub that is
// coming rather than taking the one that has closed.
func (h *hubHandle) withdraw() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.current = nil
	select {
	case <-h.ready:
		h.ready = make(chan struct{})
	default:
		// Never published this generation; the existing channel is still the
		// one waiters are blocked on.
	}
}

// await blocks until the hub publishes its resources or ctx ends.
func (h *hubHandle) await(ctx context.Context) (*busResources, error) {
	h.mu.Lock()
	ready, current := h.ready, h.current
	h.mu.Unlock()

	if current != nil {
		return current, nil
	}

	select {
	case <-ready:
	case <-ctx.Done():
		return nil, errs.From(ctx.Err()).Code(ErrCodeHubGone).Msg("stopped before the bus was available")
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.current == nil {
		// Published and withdrawn between the wait and this read: the hub
		// attempt ended, and RestForOne is ending this module too.
		return nil, errs.New().Code(ErrCodeHubGone).Msg("the bus went away before this module could use it")
	}
	return h.current, nil
}
