package snmp

import (
	"context"
	"iter"
	"sync"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/pump"
	"go.aledante.io/FlowSeer/src/common/spawn"
)

// ErrSessionClosed is the canonical "session is closed" sentinel,
// returned by every [Session] method (Get / GetNext / GetBulk / Set)
// invoked after [Session.Close] has succeeded, and surfaced via
// [Walker.Err] when the underlying Session is closed mid-walk. Backends
// either return it directly from single-PDU methods or pass it to
// [Walker.Fail], so consumers can distinguish a clean shutdown from a
// transport error via a single [errors.Is] check.
//
// The wire layer returns this exact value from
// their session implementations; no separate Backend-private sentinel
// exists, which keeps gosnmp identifiers from leaking into the public
// API.
var ErrSessionClosed = errs.Msg("session is closed")

// ErrOIDNotIncreasing is the terminal error a Walk/BulkWalk reports via
// [Walker.Err] when the agent returns an OID that is not strictly greater
// than the previous one, unless the session opted into skip mode via
// [WithIgnoreNonIncreasing]. A repeated *exact* OID is always treated as a
// cycle and aborts even in skip mode, since no forward progress is
// possible. Backends without walk guards never produce it.
var ErrOIDNotIncreasing = errs.Msg("walk OID not increasing")

// ErrMaxWalkVars is the terminal error a Walk/BulkWalk reports via
// [Walker.Err] when the number of yielded varbinds exceeds the configured
// [WithMaxWalkVars] budget — the bound that stops a runaway walk against a
// misbehaving agent.
var ErrMaxWalkVars = errs.Msg("walk exceeded max varbind budget")

// defaultRowBuffer is the buffer size [NewWalker] uses when the caller
// passes a non-positive bufferSize. It matches the value documented in
// [WithRowBuffer]: large enough to absorb a single GetBulk PDU's worth of
// VarBinds (gosnmp's default max-repetitions is 50) with headroom, small
// enough that a stalled consumer does not pin large amounts of memory.
const defaultRowBuffer = 64

// Walker yields (OID, VarBind) pairs from a Walk or BulkWalk operation.
//
// Walker exposes two iteration shapes that share a single state machine:
//
//   - [Walker.Iter] returns an [iter.Seq2][OID, VarBind] suitable for
//     range-over-func loops. This is the idiomatic choice for almost
//     every caller.
//   - [Walker.Next] / [Walker.Current] / [Walker.Err] form a
//     [bufio.Scanner]-style triple for callers that need explicit
//     control — passing the iterator across an API boundary, draining
//     conditionally, or interleaving with bookkeeping logic that is
//     awkward to express inside a range loop.
//
// Mixing the two shapes within a single loop is documented as undefined
// behavior but is safe at runtime: both forms draw from the same
// channel, so one consumer simply "steals" values from the other and
// the totals come up short. There is no panic and no data corruption.
//
// After either iteration shape exits, callers SHOULD check
// [Walker.Err]: a nil result means the walk completed naturally
// (subtree exhausted or EndOfMibView seen). The [iter.Seq2][V, error]
// silent-drop footgun is intentionally avoided.
//
// Backends are responsible for spawning the pump and feeding values via
// [Walker.Send] / [Walker.Fail] / [Walker.Done] (typically through
// [Walker.Pump], which installs a deferred [Walker.Done] so the
// channel is closed exactly once even on panic). Calling
// [Walker.Close] tells the pump to terminate early; range-over-func
// loops invoke Close automatically when they exit via break.
//
// The zero value is not usable; construct a Walker via [NewWalker].
//
// # Synchronization model
//
// Walker wraps the shared [pump.Pump] of [walkerItem], which owns the
// shared channel-pump state machine (data channel, stop signal,
// idempotent close guards, send/close RWMutex, derived context, and
// first-terminal-error latch). Walker-specific state — the "current"
// latch for the Scanner shape — lives on Walker itself under its own
// mutex.
type Walker struct {
	pump *pump.Pump[walkerItem]

	// currentMu guards current. Distinct from the wrapped pump's
	// terminal-error mutex so a Current/Next call cannot block on a
	// concurrent Fail/Err read.
	currentMu sync.Mutex
	current   walkerItem // latched by Next for Current
}

// walkerItem is one entry yielded by the pump. The pair (Index, Value)
// is what either iteration shape surfaces to the consumer.
type walkerItem struct {
	Index OID
	Value VarBind
}

// NewWalker constructs a Walker tied to ctx with the given buffer size.
// bufferSize <= 0 defaults to [defaultRowBuffer]. The returned Walker is
// empty; backends populate it via [Walker.Pump].
//
// The returned Walker derives a cancellable child context from ctx; the
// caller's ctx is not stored on the Session and may be released after
// NewWalker returns.
func NewWalker(ctx context.Context, bufferSize int) *Walker {
	if bufferSize <= 0 {
		bufferSize = defaultRowBuffer
	}
	return &Walker{
		pump: pump.New[walkerItem](ctx, bufferSize),
	}
}

// Pump runs fn in a new goroutine. fn is responsible for producing
// items via [Walker.Send] and respecting ctx cancellation. Pump returns
// immediately; the goroutine self-terminates when fn returns.
//
// fn must:
//   - call [Walker.Send] for each (OID, VarBind) pair, and stop
//     promptly when Send returns false (the consumer has signaled
//     termination).
//   - call [Walker.Fail] on any terminal error (transport, decode,
//     closed session). Fail records the error and closes the data
//     channel.
//   - return normally when the walk reaches its natural end
//     (EndOfMibView, root subtree exited). Pump installs a deferred
//     [Walker.Done] so the channel is always closed exactly once,
//     even when fn panics.
//
// Pump may only be called once per Walker; calling it twice produces
// two pump goroutines racing on the same channel, which is a
// programming error.
//
// A panic in fn is recovered by [spawn.Go] and reported through
// [spawn.ReportTo](w.Fail): without that sink, a panicking fn would
// close the channel via the deferred Done below with Err() still
// nil, which a consumer cannot tell from a walk that finished.
func (w *Walker) Pump(fn func(ctx context.Context)) {
	spawn.Go(w.pump.Context(), "Walker.Pump", func() {
		// Close the data channel exactly once when the pump returns,
		// regardless of whether it returned normally, was canceled,
		// or panicked. Done is idempotent with Fail.
		defer w.Done()
		fn(w.pump.Context())
	}, spawn.ReportTo(w.Fail))
}

// Send delivers one item to the consumer. Returns false when the
// consumer has signaled termination ([Walker.Close], [Walker.Fail],
// [Walker.Done], or context cancellation); the pump should return
// promptly in that case so its goroutine does not leak.
//
// Send blocks until either the buffered channel has room, the consumer
// reads, stop is closed, or the Walker's context is canceled.
//
// Send is safe to call concurrently with [Walker.Fail] or [Walker.Done]:
// the read lock on sendMu serializes against the channel close so a
// closing call cannot race an in-flight send.
func (w *Walker) Send(idx OID, vb VarBind) bool {
	return w.pump.Send(walkerItem{Index: idx, Value: vb})
}

// Fail records err as the terminal error, signals the pump to stop,
// and closes the data channel. Safe to call multiple times; only the
// first non-nil error sticks. Backends call this on transport
// failures, decode errors, or Session.Close-during-walk. External code
// (e.g. [BindRow] on a bind error) calls it to abort iteration with a
// surfaced cause.
//
// Fail acquires the sendMu write lock before closing ch so a
// concurrent [Walker.Send] from the pump cannot panic on a closed
// channel. Send observes the closed stop on its next invocation and
// returns false.
func (w *Walker) Fail(err error) { w.pump.Fail(err) }

// Done signals normal completion of the walk: closes the data channel
// so consumers exit cleanly, and closes the stop signal so the pump
// returns promptly if it is still in a Send. Idempotent and safe to
// call after [Walker.Fail]; subsequent calls are no-ops.
//
// [Walker.Pump] installs Done as a deferred call so it runs even when
// the pump function panics. Backends that drive the Walker without
// Pump must call Done themselves on natural completion.
func (w *Walker) Done() { w.pump.Done() }

// Err returns the first terminal error recorded via [Walker.Fail], or
// nil if the walk completed naturally or is still running.
func (w *Walker) Err() error { return w.pump.Err() }

// Iter returns the range-over-function form of the walk. The returned
// [iter.Seq2] yields (OID, VarBind) pairs until the walk ends; the
// caller should check [Walker.Err] after the loop to distinguish
// natural completion from a terminal error.
//
// Breaking out of the loop tells the pump to terminate; Iter drains
// any in-flight buffered items so the pump goroutine does not block on
// a full channel. The pump exits within a bounded time (one PDU
// round-trip).
func (w *Walker) Iter() iter.Seq2[OID, VarBind] {
	return func(yield func(OID, VarBind) bool) {
		for item := range w.pump.Data() {
			if !yield(item.Index, item.Value) {
				// Consumer broke out. Signal the pump and drain so the
				// pump can exit even if it was blocked on send.
				w.pump.SignalStop()
				for range w.pump.Data() {
				}
				return
			}
		}
	}
}

// Next advances the Scanner-shape state machine to the next item.
// Returns false when the walk is done (consumer should check
// [Walker.Err] for the terminal cause). Companion to [Walker.Current].
func (w *Walker) Next() bool {
	item, ok := w.pump.Recv()
	if ok {
		w.currentMu.Lock()
		w.current = item
		w.currentMu.Unlock()
	}
	return ok
}

// Current returns the most recent (OID, VarBind) yielded by
// [Walker.Next]. Calling Current before Next or after Next returned
// false yields the zero (OID, VarBind) pair; callers should check
// Next's return first to avoid the ambiguity.
func (w *Walker) Current() (OID, VarBind) {
	w.currentMu.Lock()
	defer w.currentMu.Unlock()
	return w.current.Index, w.current.Value
}

// Close signals the pump to terminate early and returns nil. After
// Close, iteration shapes report zero remaining items beyond what was
// already buffered; [Walker.Err] reports whichever terminal cause
// arrives first (nil if Close races the pump's natural completion).
//
// Close does NOT block waiting for the pump goroutine to exit — the
// pump shuts down asynchronously within a bounded time (one PDU
// round-trip). Iteration over a closed Walker
// drains the channel and returns.
//
// Close is idempotent. Buffered items remain available to a consumer
// that has not yet drained them — the data channel is not closed by
// Close itself, only by the pump's deferred [Walker.Done] once the
// producer goroutine returns. This is the deliberate asymmetry with
// [TrapStream.Close]; see that method's godoc.
func (w *Walker) Close() error {
	w.pump.SignalStop()
	w.pump.Cancel()
	return nil
}
