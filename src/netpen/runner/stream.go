package runner

import (
	"context"
	"iter"
	"sync"

	"go.aledante.io/FlowSeer/src/common/pump"
	"go.aledante.io/FlowSeer/src/netpen/findings"
)

// defaultStreamBuffer is the bounded channel size between the runner's
// producer (the dispatch loop) and the consumer (the host). Large enough to
// absorb a burst of findings from a fast behavior without forcing the
// producer to block on a slow consumer; small enough that a stalled consumer
// does not pin large amounts of memory.
const defaultStreamBuffer = 64

// Stream is the findings stream a [Runner] delivers. It wraps the shared
// [pump.Pump] of [findings.Record], which owns the channel-pump state machine
// (bounded data channel, stop signal, idempotent close guards, send/close
// RWMutex, derived cancellable context, and first-terminal-error latch).
//
// The lifecycle follows the Collection Primitives contract:
//
//   - The stream is created at [NewRunner] construction, so a consumer can
//     start iterating before [Runner.Run] is called.
//   - Iteration (Iter or Next/Current) is the idiomatic consumer surface.
//   - Close is idempotent and terminates the producer within one leg poll
//     cycle (~100ms) by canceling the derived context.
//   - A terminal error latches once and surfaces through Err.
//
// # Backpressure
//
// The stream uses a bounded channel. When the consumer stalls, the producer
// (the dispatch loop) blocks on send — respecting the run's context — rather
// than growing memory unboundedly. This is the boring correct thing: a
// bounded buffer with producer-side backpressure. The alternative (drop-
// oldest, used by the snmp TrapStream for realtime traps) is wrong here
// because findings are not realtime events — a dropped finding is a silent
// audit gap, which defeats the tool's purpose.
//
// The zero value is not usable; construct a Stream via [NewRunner].
//
// Stream is safe for concurrent use by one consumer (iteration) and the
// single producer (the runner's dispatch loop).
type Stream struct {
	pump *pump.Pump[findings.Record]

	// closeHook is called by Close to cancel the run's context, so a
	// consumer leaving iteration early aborts the dispatch loop and any
	// in-flight behavior. Set by [Runner.Run] before the dispatch loop
	// starts; nil when Run has not been called (Close is then a no-op
	// beyond signaling the pump).
	closeHook func()

	// attackMu guards attack and mode, the currently-running attack
	// identity used to stamp emitted records. Distinct from the pump's
	// terminal-error mutex so an Emitter.Finding call cannot block on
	// a concurrent fail/Err read.
	attackMu sync.Mutex
	attack   string
	mode     string

	// currentMu guards current, the latch for the Scanner-shape
	// (Next/Current). Distinct from the pump's mutex for the same
	// reason as attackMu.
	currentMu sync.Mutex
	current   findings.Record
}

// newStream constructs a Stream tied to ctx with a bounded channel of
// defaultStreamBuffer. The pump derives a cancellable child context from ctx;
// Close cancels it so a behavior blocked in leg I/O unblocks within one poll
// cycle.
func newStream(ctx context.Context) *Stream {
	return &Stream{
		pump: pump.New[findings.Record](ctx, defaultStreamBuffer),
	}
}

// send delivers one record to the consumer. It blocks until the bounded
// channel has room, the consumer reads, stop is closed, or the stream's
// context is canceled. Returns false when the stream is terminating so the
// dispatch loop can return promptly without leaking.
func (s *Stream) send(r findings.Record) bool {
	return s.pump.Send(r)
}

// fail records err as the first terminal error (subsequent non-nil errors
// are ignored), signals the producer to stop, and closes the data channel.
func (s *Stream) fail(err error) {
	s.pump.Fail(err)
}

// done signals normal completion: closes the data channel so consumers exit
// cleanly, and closes the stop signal so the producer returns promptly.
func (s *Stream) done() {
	s.pump.Done()
}

// setAttack records the currently-running attack identity (name and mode)
// so emitted records can be stamped. Called by the runner's dispatch loop
// before each behavior.
func (s *Stream) setAttack(name, mode string) {
	s.attackMu.Lock()
	s.attack = name
	s.mode = mode
	s.attackMu.Unlock()
}

// attackName returns the currently-running attack name.
func (s *Stream) attackName() string {
	s.attackMu.Lock()
	defer s.attackMu.Unlock()
	return s.attack
}

// attackMode returns the currently-running attack mode.
func (s *Stream) attackMode() string {
	s.attackMu.Lock()
	defer s.attackMu.Unlock()
	return s.mode
}

// Err returns the first terminal error recorded via fail, or nil if the
// stream completed normally or is still running.
func (s *Stream) Err() error {
	return s.pump.Err()
}

// Iter returns the range-over-function form of the stream. The returned
// [iter.Seq] yields each [findings.Record] in order until the stream ends;
// the caller should check [Stream.Err] after the loop to distinguish natural
// completion from a terminal error.
//
// Breaking out of the loop signals the producer to terminate; Iter drains
// any in-flight buffered records so the producer goroutine does not block on
// a full channel. The producer exits within one leg poll cycle (~100ms).
func (s *Stream) Iter() iter.Seq[findings.Record] {
	return func(yield func(findings.Record) bool) {
		for rec := range s.pump.Data() {
			if !yield(rec) {
				_ = s.Close()
				// Drain remaining buffered records so the
				// producer's blocked send unblocks.
				for range s.pump.Data() {
				}
				return
			}
		}
	}
}

// Next advances the Scanner-shape state machine to the next record. Returns
// false when the stream is done; the caller should check [Stream.Err] for
// the terminal cause. Companion to [Stream.Current].
func (s *Stream) Next() bool {
	// Select on the data channel and the stop signal so a closed
	// stream does not block when the channel is still open (Close
	// signals stop but the producer's deferred done closes the
	// channel). When stop fires, drain any remaining buffered records
	// so the consumer does not lose them.
	select {
	case rec, ok := <-s.pump.Data():
		if !ok {
			return false
		}
		s.currentMu.Lock()
		s.current = rec
		s.currentMu.Unlock()
		return true
	case <-s.pump.Stopped():
		// The stream is terminating. Drain remaining buffered records.
		select {
		case rec, ok := <-s.pump.Data():
			if !ok {
				return false
			}
			s.currentMu.Lock()
			s.current = rec
			s.currentMu.Unlock()
			return true
		default:
			return false
		}
	}
}

// Current returns the most recent record yielded by [Stream.Next]. Calling
// Current before Next has returned true at least once yields the zero
// [findings.Record]. The Scanner-shape is the explicit-control counterpart
// to [Stream.Iter]; callers should use Iter for the idiomatic range loop.
func (s *Stream) Current() findings.Record {
	s.currentMu.Lock()
	defer s.currentMu.Unlock()
	return s.current
}

// Close signals the producer to terminate and cancels the stream's context.
// It is idempotent. After Close, iteration drains any remaining buffered
// records and returns; the producer exits within one leg poll cycle
// (~100ms).
//
// Close does not block waiting for the producer goroutine to exit — the
// producer shuts down asynchronously within the bounded time. The bounded
// shutdown is asserted in tests.
func (s *Stream) Close() error {
	s.pump.SignalStop()
	s.pump.Cancel()
	// Cancel the run's context so the dispatch loop and any in-flight
	// behavior observe cancellation and exit promptly. closeHook is
	// set by [Runner.Run]; when Run has not been called it is nil and
	// Close is a stream-only teardown.
	if s.closeHook != nil {
		s.closeHook()
	}
	return nil
}
