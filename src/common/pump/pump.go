// Package pump provides the generic producer/consumer channel pump
// shared by FlowSeer's protocol-library collection primitives (SNMP's
// Walker, Watcher, TrapStream, and RawWalker, and the corresponding
// primitives of the YANG protocol libraries).
//
// A [Pump] is the cross-cutting state machine of a streaming pipeline:
// a buffered data channel, a stop signal, a pair of sync.Once guards
// for idempotent closes, an RWMutex that serializes in-flight sends
// against the channel close, a derived cancellable context, and a
// separate mutex guarding the first terminal error. Type-specific
// state (e.g. a Walker's "current" latch, a TrapStream's atomic drop
// counter) lives on the outer type that wraps the Pump.
//
// # Synchronization model
//
//   - the data channel is optionally buffered. [Pump.CloseData] closes it
//     exactly once under the sendMu write lock.
//   - the stop channel — unbuffered signal. Closed via stopOnce by
//     [Pump.SignalStop] (used by [Pump.CloseData], which [Pump.Fail] and
//     [Pump.Done] go through, and by the outer-type Close paths).
//   - sendMu — read/write lock. [Pump.Send] and
//     [Pump.TrySendDropOldest] acquire the read lock for the entire
//     send attempt; close paths acquire the write lock before closing
//     the data channel, so a closing call can never race an in-flight
//     send and panic.
package pump

import (
	"context"
	"sync"
)

// Pump transfers values between producers and consumers. Its methods are safe
// for concurrent use; concurrent consumers divide the available values. The
// zero value is not usable; construct via [New] and do not copy it. The owner
// must call [Pump.Cancel] when finished to release the derived context.
type Pump[T any] struct {
	// ch is closed under sendMu's write lock.
	ch chan T
	// stop signals the producer to terminate. Closed via stopOnce.
	stop     chan struct{}
	stopOnce sync.Once
	// chOnce guards the ch close so Fail / Done are idempotent.
	chOnce sync.Once
	// sendMu serializes Send (read lock) against close-of-ch
	// (write lock) so a closing close-of-channel cannot race an
	// in-flight send.
	sendMu sync.RWMutex

	// ctx belongs to the pump's lifetime, independently of any send or receive.
	ctx    context.Context
	cancel context.CancelFunc

	mu  sync.Mutex // guards err
	err error      // first terminal error
}

// New constructs a Pump with capacity buf and a cancellable context derived
// from ctx. A zero buf creates an unbuffered channel; a negative buf panics.
// Callers choose their own default capacity.
//
// If ctx is nil, [context.Background] is used.
//
//nolint:contextcheck // documented fallback: a nil ctx parameter is a caller bug we recover from rather than panic on; the derived context still inherits from Background.
func New[T any](ctx context.Context, buf int) *Pump[T] {
	if ctx == nil {
		ctx = context.Background()
	}
	pctx, cancel := context.WithCancel(ctx)
	return &Pump[T]{
		ch:     make(chan T, buf),
		stop:   make(chan struct{}),
		ctx:    pctx,
		cancel: cancel,
	}
}

// Context returns the pump's derived cancellable context. Producer
// goroutines run under it; [Pump.Cancel] terminates it.
func (p *Pump[T]) Context() context.Context { return p.ctx }

// Cancel terminates the pump's derived context. Outer-type Close paths
// call it after [Pump.SignalStop] so any producer blocked on I/O under
// [Pump.Context] observes cancellation promptly.
func (p *Pump[T]) Cancel() { p.cancel() }

// Data returns the data channel for consumer-side range loops. [Pump.CloseData],
// [Pump.Fail], and [Pump.Done] close it while leaving buffered values readable.
func (p *Pump[T]) Data() <-chan T { return p.ch }

// Stopped returns the stop signal. Producers select on it to know when to
// release their resources. Context cancellation alone does not close it;
// [Pump.Send] propagates cancellation when it observes it.
func (p *Pump[T]) Stopped() <-chan struct{} { return p.stop }

// SignalStop closes the stop channel exactly once. Safe to call from
// any goroutine.
func (p *Pump[T]) SignalStop() {
	p.stopOnce.Do(func() { close(p.stop) })
}

// CloseData signals stop, unblocks pending sends, and closes the data channel
// exactly once. Buffered values remain readable. It does not cancel the
// derived context or record an error. It is safe to call concurrently with
// either send method.
func (p *Pump[T]) CloseData() {
	p.SignalStop()
	p.chOnce.Do(func() {
		p.sendMu.Lock()
		close(p.ch)
		p.sendMu.Unlock()
	})
}

// Send delivers v to the consumer, blocking until the buffered
// channel has room, the consumer reads, stop is closed, or the
// pump's context is canceled. Returns false when the consumer has
// signaled termination so the caller can return promptly without
// leaking its goroutine.
//
// Send is safe to call concurrently with [Pump.Fail] or [Pump.Done]:
// the read lock on sendMu serializes against the channel close so a
// closing call cannot race an in-flight send.
func (p *Pump[T]) Send(v T) bool {
	// Hold the read lock for the entire send. Fail/Done acquire the
	// write lock before closing ch, so this prevents a send-on-closed
	// panic.
	p.sendMu.RLock()
	defer p.sendMu.RUnlock()

	// First check stop / ctx without blocking: a closed stop should
	// short-circuit Send even if the channel still has buffer space.
	// Go's select picks randomly among ready cases, so without this
	// pre-check the producer could keep sending values after Close.
	select {
	case <-p.stop:
		return false
	case <-p.ctx.Done():
		p.SignalStop()
		return false
	default:
	}
	select {
	case <-p.stop:
		return false
	case <-p.ctx.Done():
		p.SignalStop()
		return false
	case p.ch <- v:
		return true
	}
}

// TrySendDropOldest performs the drop-oldest non-blocking send pattern
// used by realtime streams (e.g. a trap listener's Push). The entire
// operation runs under a single sendMu read lock so a concurrent close
// cannot race the drop+retry sequence and trigger a send-on-closed
// panic.
//
// Return semantics:
//
//   - (delivered=true,  dropped=0): sent without discarding a buffered item.
//   - (delivered=true,  dropped=1): buffer was full; the oldest
//     buffered item was discarded and v was sent on the retry.
//   - (delivered=false, dropped=1): neither attempt sent v and no
//     buffered item was discarded. Only v was dropped.
//   - (delivered=false, dropped=2): an old item was discarded, but
//     another producer filled the space before the retry; v was also dropped.
//   - (delivered=false, dropped=0): pump is stopped; v was silently
//     dropped without counting (clean-shutdown, not a loss event).
//
// The pump itself maintains no drop counter; callers that want one
// should add to their own [sync/atomic.Uint64] based on the returned
// dropped count.
func (p *Pump[T]) TrySendDropOldest(v T) (delivered bool, dropped int) {
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

// Fail records err as the first terminal error (subsequent non-nil
// errors are ignored), signals the producer to stop, and closes the
// data channel. Safe to call multiple times and from any goroutine.
func (p *Pump[T]) Fail(err error) {
	p.mu.Lock()
	if p.err == nil && err != nil {
		p.err = err
	}
	p.mu.Unlock()
	p.CloseData()
}

// Done signals normal completion by stopping sends and closing the data channel.
// Buffered values remain readable. Repeated calls are harmless; a later
// [Pump.Fail] can still record an error. Done does not cancel the derived context.
func (p *Pump[T]) Done() {
	p.CloseData()
}

// Err returns the first terminal error recorded via [Pump.Fail], or
// nil if the pump completed normally or is still running.
func (p *Pump[T]) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

// Recv blocks until a value is available or the data channel is closed and
// drained. It returns the zero value and false only after buffered values have
// been consumed. Stopping or canceling alone does not unblock Recv.
func (p *Pump[T]) Recv() (T, bool) {
	v, ok := <-p.ch
	return v, ok
}
