package freeze

import (
	"context"
	"sync"

	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/telemetry"
)

// Gate is one device's control-plane freeze state. The zero value is not
// usable; construct with [New].
type Gate struct {
	view *telemetry.View

	mu       sync.Mutex
	frozen   bool
	released chan struct{}
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

// Freeze stops new side effects from proceeding until [Gate.Unfreeze] is
// called. Idempotent: freezing an already-frozen gate emits nothing further.
func (g *Gate) Freeze(ctx context.Context) {
	g.mu.Lock()
	alreadyFrozen := g.frozen
	if !alreadyFrozen {
		g.frozen = true
		g.released = make(chan struct{})
	}
	g.mu.Unlock()

	if !alreadyFrozen {
		g.view.LaneFrozen(ctx)
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
