package snmp

import (
	"context"
	"sync"
)

// pump is the package-private generic state machine shared by [Walker]
// and [TrapStream]. Both types are producer/consumer pipelines that
// follow the same shape: a buffered data channel, a stop signal, a
// pair of sync.Once guards for idempotent closes, an RWMutex that
// serializes in-flight sends against the channel close, a derived
// cancellable context, and a separate mutex guarding the first
// terminal error.
//
// pump owns only the cross-cutting plumbing. Type-specific state
// (e.g. Walker's "current" latch, TrapStream's atomic drop counter)
// lives on the outer struct.
//
// # Synchronization model
//
//   - ch — buffered data channel. Closed exactly once by [pump.fail]
//     or [pump.done] under the sendMu write lock.
//   - stop — unbuffered signal channel. Closed via stopOnce by
//     [pump.signalStop] (used by [pump.fail], [pump.done], and the
//     outer-type Close paths).
//   - sendMu — read/write lock. [pump.send] and [pump.trySendDropOldest]
//     acquire the read lock for the entire send attempt; close paths
//     acquire the write lock before closing ch, so a closing call can
//     never race an in-flight send and panic.
//
// The zero value is not usable; construct via [newPump].
type pump[T any] struct {
	// ch is the buffered data channel the producer writes into. Closed
	// by [pump.fail] or [pump.done] under sendMu's write lock.
	ch chan T
	// stop signals the producer to terminate. Closed via stopOnce.
	stop     chan struct{}
	stopOnce sync.Once
	// chOnce guards the ch close so fail / done are idempotent.
	chOnce sync.Once
	// sendMu serializes send (read lock) against close-of-ch
	// (write lock) so a closing close-of-channel cannot race an
	// in-flight send.
	sendMu sync.RWMutex

	// ctx is the derived cancellable context.
	ctx context.Context
	// cancel terminates the derived context.
	cancel context.CancelFunc

	mu  sync.Mutex
	err error // first terminal error
}

// newPump constructs a pump with a buffered data channel of size buf,
// a derived cancellable context from ctx, and freshly-zeroed Once
// guards. Buf must be >= 0; callers are expected to apply their own
// default before invoking newPump (see [defaultRowBuffer],
// [defaultTrapBuffer]).
//
// If ctx is nil, [context.Background] is used.
//
//nolint:contextcheck // documented fallback: a nil ctx parameter is a caller bug we recover from rather than panic on; the derived context still inherits from Background.
func newPump[T any](ctx context.Context, buf int) *pump[T] {
	if ctx == nil {
		ctx = context.Background()
	}
	pctx, cancel := context.WithCancel(ctx)
	return &pump[T]{
		ch:     make(chan T, buf),
		stop:   make(chan struct{}),
		ctx:    pctx,
		cancel: cancel,
	}
}

// signalStop closes the stop channel exactly once. Safe to call from
// any goroutine.
func (p *pump[T]) signalStop() {
	p.stopOnce.Do(func() { close(p.stop) })
}

// closeData closes the data channel exactly once under the sendMu
// write lock so a concurrent in-flight send (which holds the read
// lock) finishes before the channel is closed, eliminating the
// send-on-closed panic.
func (p *pump[T]) closeData() {
	p.chOnce.Do(func() {
		p.sendMu.Lock()
		close(p.ch)
		p.sendMu.Unlock()
	})
}

// send delivers v to the consumer, blocking until the buffered
// channel has room, the consumer reads, stop is closed, or the
// pump's context is canceled. Returns false when the consumer has
// signaled termination so the caller can return promptly without
// leaking its goroutine.
//
// send is safe to call concurrently with [pump.fail] or [pump.done]:
// the read lock on sendMu serializes against the channel close so a
// closing call cannot race an in-flight send.
func (p *pump[T]) send(v T) bool {
	// Hold the read lock for the entire send. fail/done acquire the
	// write lock before closing ch, so this prevents a send-on-closed
	// panic.
	p.sendMu.RLock()
	defer p.sendMu.RUnlock()

	// First check stop / ctx without blocking: a closed stop should
	// short-circuit send even if the channel still has buffer space.
	// Go's select picks randomly among ready cases, so without this
	// pre-check the producer could keep sending values after Close.
	select {
	case <-p.stop:
		return false
	case <-p.ctx.Done():
		p.signalStop()
		return false
	default:
	}
	select {
	case <-p.stop:
		return false
	case <-p.ctx.Done():
		p.signalStop()
		return false
	case p.ch <- v:
		return true
	}
}

// trySendDropOldest performs the drop-oldest non-blocking send pattern
// used by [TrapStream.Push]. The entire operation runs under a single
// sendMu read lock so a concurrent close cannot race the drop+retry
// sequence and trigger a send-on-closed panic.
//
// Return semantics:
//
//   - (delivered=true,  dropped=0): sent on the first attempt with
//     no drop.
//   - (delivered=true,  dropped=1): buffer was full; the oldest
//     buffered item was discarded and v was sent on the retry.
//   - (delivered=false, dropped=1): buffer was full, the retry also
//     failed (extremely rare race with the consumer), and v itself
//     was dropped.
//   - (delivered=false, dropped=0): pump is stopped; v was silently
//     dropped without counting (clean-shutdown, not a loss event —
//     matches existing [TrapStream.Push] semantics).
//
// The pump itself maintains no drop counter; callers that want one
// should add to their own [sync/atomic.Uint64] based on the returned
// dropped count.
func (p *pump[T]) trySendDropOldest(v T) (delivered bool, dropped int) {
	p.sendMu.RLock()
	defer p.sendMu.RUnlock()

	// If the pump is already terminating, silently drop. We do not
	// count this: a clean shutdown is not a loss event.
	select {
	case <-p.stop:
		return false, 0
	default:
	}

	// First non-blocking attempt.
	select {
	case p.ch <- v:
		return true, 0
	default:
		// Buffer full — drop the oldest, count it, then retry.
		drainedOne := false
		select {
		case <-p.ch:
			drainedOne = true
		default:
			// Race: the consumer drained between our two selects.
			// Fall through to the retry without bumping the counter;
			// if the retry still cannot send we'll count that loss
			// instead.
		}
		select {
		case p.ch <- v:
			if drainedOne {
				return true, 1
			}
			return true, 0
		default:
			// Still full (rare) — drop the one we tried to push.
			// If we also drained one earlier, that one counts too.
			if drainedOne {
				return false, 2
			}
			return false, 1
		}
	}
}

// fail records err as the first terminal error (subsequent non-nil
// errors are ignored), signals the producer to stop, and closes the
// data channel. Safe to call multiple times and from any goroutine.
func (p *pump[T]) fail(err error) {
	p.mu.Lock()
	if p.err == nil && err != nil {
		p.err = err
	}
	p.mu.Unlock()
	p.signalStop()
	p.closeData()
}

// done signals normal completion: closes the data channel so consumers
// exit cleanly, and closes the stop signal so the producer returns
// promptly. Idempotent with [pump.fail]; subsequent calls are no-ops.
func (p *pump[T]) done() {
	p.signalStop()
	p.closeData()
}

// Err returns the first terminal error recorded via [pump.fail], or
// nil if the pump completed normally or is still running.
func (p *pump[T]) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

// recv pulls the next item from the data channel. Returns the zero
// value and false when the channel is closed.
func (p *pump[T]) recv() (T, bool) {
	v, ok := <-p.ch
	return v, ok
}
