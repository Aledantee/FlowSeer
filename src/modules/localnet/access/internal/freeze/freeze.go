package freeze

import (
	"context"
	"sync"

	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/telemetry"
)

// Gate is the control-plane freeze state for every device one access.Lane
// serves — a single Gate is shared across the whole Lane, not scoped to one
// device, per decision 8's control-plane-wide freeze. The zero value is not
// usable; construct with [New].
type Gate struct {
	view *telemetry.View

	mu       sync.Mutex
	frozen   bool
	released chan struct{}

	// barrier is the counted barrier proving Freeze has actually stopped
	// every in-flight side effect, not merely new ones: each entered side
	// effect holds a read lock for its duration, and Freeze takes the
	// write lock before reporting the freeze in effect, so it cannot
	// return while one is still running. frozen alone only stops a side
	// effect from starting; it says nothing about one already in progress.
	barrier sync.RWMutex
}

// New constructs an unfrozen Gate. view may be nil; nil is treated as a
// no-op view.
func New(view *telemetry.View) *Gate {
	return &Gate{view: view, released: closedChan()}
}

func closedChan() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

// AllowSideEffect reports whether a new device-facing side effect may
// proceed right now. It does not block; a caller that wants to wait for an
// unfreeze uses [Gate.AwaitSideEffect].
func (g *Gate) AllowSideEffect() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return !g.frozen
}

// AllowAcknowledgement always reports true: the acknowledgement barrier
// (decision 4's central journal) is never gated by a freeze, so an
// already-verified mutation's terminal acknowledgement completes
// regardless of freeze state.
func (g *Gate) AllowAcknowledgement() bool { return true }

// AwaitSideEffect blocks until a side effect is allowed, ctx ends, or the
// gate is unfrozen — whichever comes first. It returns immediately, with a
// nil error, when the gate is not frozen. Freezing never fails or disposes
// the caller's work; it only delays the moment this call returns.
func (g *Gate) AwaitSideEffect(ctx context.Context) error {
	for {
		g.mu.Lock()
		frozen := g.frozen
		released := g.released
		g.mu.Unlock()

		if !frozen {
			return nil
		}

		select {
		case <-released:
			continue
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Enter blocks until a side effect may proceed — same as AwaitSideEffect —
// then returns a leave func the caller must call exactly once when its own
// side effect ends. Freeze cannot return while any Enter's leave has not
// yet run, so a caller observing Freeze return also knows no write is in
// flight, not only that none can start. AwaitSideEffect alone proves only
// the latter: it says nothing about a side effect that was already
// admitted and is still running when Freeze is called.
func (g *Gate) Enter(ctx context.Context) (leave func(), err error) {
	for {
		if err := g.AwaitSideEffect(ctx); err != nil {
			return nil, err
		}
		g.barrier.RLock()
		if g.AllowSideEffect() {
			return g.barrier.RUnlock, nil
		}
		// Froze between AwaitSideEffect returning and RLock succeeding;
		// back off and wait again rather than proceeding into a freeze
		// that has not yet drained.
		g.barrier.RUnlock()
	}
}

// Freeze stops new side effects from proceeding immediately (before this
// call returns, every goroutine's next AllowSideEffect/AwaitSideEffect
// check sees it), and waits for every side effect already admitted through
// [Gate.Enter] to finish before returning nil — decision 8's positive
// fencing depends on Freeze itself proving no write is in flight, not
// merely on the frozen flag other callers observe. That wait honors ctx: a
// control plane fencing an edge precisely because it has gone unresponsive
// must not be able to hang forever behind a write to that same
// unresponsive device, so a canceled or expired ctx returns ctx.Err()
// instead of blocking indefinitely — frozen stays set regardless, so new
// side effects remain stopped even though this call gave up waiting for
// the one already in flight. The wait itself runs unconditionally on
// every call, concurrent or repeated: only the durable frozen flag and the
// LaneFrozen telemetry event are idempotent, since a caller that gets
// ctx.Err() from one Freeze call and immediately calls Freeze again with a
// longer deadline must still be able to wait for the same drain, not have
// a second call return early because some other call already flipped the
// flag.
func (g *Gate) Freeze(ctx context.Context) error {
	g.mu.Lock()
	alreadyFrozen := g.frozen
	if !alreadyFrozen {
		g.frozen = true
		g.released = make(chan struct{})
	}
	g.mu.Unlock()

	// sync.RWMutex has no cancellable Lock, so the acquisition itself runs
	// on its own goroutine and this call races that against ctx. Emitted
	// while still holding the write lock, not after releasing it: that is
	// the one point that has actually confirmed every entered side effect
	// has left, so it is the accurate moment to say the freeze is in
	// effect, not merely requested. Telemetry.LaneFrozen never blocks past
	// a bounded local call (View's own doc), so holding the lock here is
	// safe.
	acquired := make(chan struct{})
	go func() {
		g.barrier.Lock()
		close(acquired)
	}()

	select {
	case <-acquired:
		if !alreadyFrozen {
			g.view.LaneFrozen(ctx)
		}
		g.barrier.Unlock()
		return nil
	case <-ctx.Done():
		// The background goroutine may still be waiting for the write
		// lock, or may acquire it after this call has already returned;
		// release it whenever that happens so a later Freeze or Enter on
		// this Gate never deadlocks on this abandoned attempt.
		go func() {
			<-acquired
			g.barrier.Unlock()
		}()
		return ctx.Err()
	}
}

// Unfreeze allows side effects to proceed again. Idempotent: unfreezing an
// already-unfrozen gate emits nothing further.
func (g *Gate) Unfreeze(ctx context.Context) {
	g.mu.Lock()
	wasFrozen := g.frozen
	var toClose chan struct{}
	if wasFrozen {
		g.frozen = false
		toClose = g.released
		g.released = closedChan()
	}
	g.mu.Unlock()

	if wasFrozen {
		close(toClose)
		g.view.LaneReleased(ctx)
	}
}
