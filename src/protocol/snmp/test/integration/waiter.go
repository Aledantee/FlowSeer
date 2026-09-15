package integration

import (
	"context"
	"errors"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/spawn"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// ErrTrapWaitTimeout is returned by [WaitForTrap] when the deadline
// elapses before a matching trap arrives.
var ErrTrapWaitTimeout = errs.Msg("timeout before matching trap arrived")

// ErrTrapStreamClosed is returned by [WaitForTrap] when the
// underlying [snmp.TrapStream] finishes iteration before any matching
// trap arrives — e.g., the listener was closed or its pump failed.
var ErrTrapStreamClosed = errs.Msg("stream closed before matching trap arrived")

// WaitForTrap consumes and discards nonmatching traps until match succeeds,
// timeout expires, ctx is canceled, or ts ends. It returns the matched trap on
// success and leaves ts open for another sequential call. ts and match must be
// non-nil; match must return promptly. The caller must not consume ts concurrently.
//
// On timeout or cancellation it closes ts and joins its reader before returning
// a zero trap and [ErrTrapWaitTimeout] or ctx.Err(). TrapStream.Next cannot be
// interrupted independently, so closing prevents a leftover reader from stealing
// subsequent traps. The caller must create a new stream after these errors.
// Stream termination returns [ErrTrapStreamClosed], joined with ts.Err() when
// present. A match already buffered when cancellation fires takes precedence.
func WaitForTrap(ctx context.Context, ts *snmp.TrapStream, match func(snmp.Trap) bool, timeout time.Duration) (snmp.Trap, error) {
	if ts == nil {
		return snmp.Trap{}, errs.Msg("nil TrapStream")
	}
	if match == nil {
		return snmp.Trap{}, errs.Msg("nil match function")
	}

	hits := make(chan snmp.Trap, 1)
	done := make(chan struct{})

	spawn.Go(ctx, "WaitForTrap.match", func() {
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
	})
	defer func() { <-done }()

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
		return snmp.Trap{}, errors.Join(ErrTrapWaitTimeout, ts.Close())
	case <-done:
		select {
		case trap := <-hits:
			return trap, nil
		default:
		}
		return snmp.Trap{}, errors.Join(ErrTrapStreamClosed, ts.Err())
	case <-ctx.Done():
		select {
		case trap := <-hits:
			return trap, nil
		default:
		}
		return snmp.Trap{}, errors.Join(ctx.Err(), ts.Close())
	}
}
