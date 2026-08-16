package integration

import (
	"context"
	"time"

	"go.aledante.io/ae"

	"go.aledante.io/FlowSeer/src/common/snmp"
)

// ErrTrapWaitTimeout is returned by [WaitForTrap] when the deadline
// elapses before a matching trap arrives.
var ErrTrapWaitTimeout = ae.Msg("timeout before matching trap arrived")

// ErrTrapStreamClosed is returned by [WaitForTrap] when the
// underlying [snmp.TrapStream] finishes iteration before any matching
// trap arrives — e.g., the listener was closed or its pump failed.
var ErrTrapStreamClosed = ae.Msg("stream closed before matching trap arrived")

// WaitForTrap consumes traps from ts via the Scanner-style
// [snmp.TrapStream.Next] / [snmp.TrapStream.Current] surface until
// match returns true, ctx expires, the timeout deadline elapses, or
// the stream itself ends — whichever happens first.
//
// On success returns the first matching trap and a nil error. On any
// terminal condition before a match the returned trap is the zero
// value and the error indicates which condition fired:
//
//   - [ErrTrapWaitTimeout] for the deadline.
//   - [ErrTrapStreamClosed] when the stream's iteration finishes
//     without yielding a match.
//   - The context's error when ctx fires before the others.
//
// Non-matching traps are silently discarded.
//
// # Multi-call safety
//
// WaitForTrap MUST use the Scanner-style [snmp.TrapStream.Next]
// surface rather than [snmp.TrapStream.Iter]. The Iter contract
// signals the producer to stop and drain when the consumer breaks
// out of the range loop (trap.go's Iter calls signalStop() + drains
// the channel as soon as yield returns false), which would
// permanently kill the stream after the first match. Callers
// frequently invoke WaitForTrap multiple times against a single
// long-lived stream (e.g., linkDown then linkUp from the same SR
// Linux trap-receiver), so an early-return consumer cannot afford to
// take down the producer.
//
// The Scanner-style triple [snmp.TrapStream.Next] /
// [snmp.TrapStream.Current] / [snmp.TrapStream.Err] does not signal
// stop on consumer exit, so the stream survives across calls.
//
// # Goroutine lifecycle
//
// The implementation spawns a background goroutine that blocks on
// [snmp.TrapStream.Next] until a matching trap arrives, the stream
// closes, or the test process exits. On timeout / ctx cancellation
// the goroutine remains blocked on the next Next() call until one of
// those terminal conditions fires — by design, since the typical
// pattern is "wait for one trap, then continue, then wait for
// another", and a leak-on-timeout goroutine consumes one extra trap
// at most (which is then matched or discarded by the next caller).
// Test process lifetime bounds this; t.Cleanup-registered Close()
// at end-of-test unblocks any lingering goroutine.
//
// The implementation reaches only through TrapStream's public
// Scanner surface; the waiter therefore survives a Backend swap
// unchanged.
func WaitForTrap(ctx context.Context, ts *snmp.TrapStream, match func(snmp.Trap) bool, timeout time.Duration) (snmp.Trap, error) {
	if ts == nil {
		return snmp.Trap{}, ae.Msg("nil TrapStream")
	}
	if match == nil {
		return snmp.Trap{}, ae.Msg("nil match function")
	}

	hits := make(chan snmp.Trap, 1)
	done := make(chan struct{})

	go func() {
		defer close(done)
		for ts.Next() {
			trap := ts.Current()
			if !match(trap) {
				continue
			}
			select {
			case hits <- trap:
			default:
			}
			return
		}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case trap := <-hits:
		return trap, nil
	case <-timer.C:
		// Drain hits one last time — Go's select is non-deterministic
		// when multiple cases are ready, so a match landing in the
		// buffer the same scheduler tick the timer fires would
		// otherwise be silently discarded.
		select {
		case trap := <-hits:
			return trap, nil
		default:
		}
		return snmp.Trap{}, ErrTrapWaitTimeout
	case <-done:
		select {
		case trap := <-hits:
			return trap, nil
		default:
		}
		return snmp.Trap{}, ErrTrapStreamClosed
	case <-ctx.Done():
		select {
		case trap := <-hits:
			return trap, nil
		default:
		}
		return snmp.Trap{}, ctx.Err()
	}
}
