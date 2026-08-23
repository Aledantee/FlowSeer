// Package testtest is netpen's shared in-memory [link.Leg] harness for
// behavior tests (U8–U11). A [Leg] is fed from pcap fixtures on RX and
// records every Send call on TX, so a behavior's frame sequence is asserted
// byte-for-byte or by field-set without a socket.
//
// The harness is intentionally boring and general: it wraps a channel of
// pre-loaded frames for Receive and a slice of recorded sends for TX
// assertions. No filtering logic — SetFilter is a no-op — because behavior
// tests drive the decode path themselves; the leg is a wire, not a parser.
package testtest

import (
	"context"
	"sync"
	"sync/atomic"

	"go.aledante.io/FlowSeer/src/netpen/link"
)

// Leg is an in-memory [link.Leg] for behavior tests. RX frames are fed from
// a pre-loaded queue (pushed via [Leg.PushRX]); TX frames are recorded in
// order for post-run assertion. The leg is safe for one reader and one
// writer (the behavior's goroutine), matching the [link.Leg] contract.
type Leg struct {
	mu       sync.Mutex
	rx       []link.Frame // pending frames not yet delivered
	rxCh     chan link.Frame
	closed   atomic.Bool
	closedCh chan struct{}

	// TX recording.
	txMu  sync.Mutex
	sends [][]byte // recorded Send payloads, in order
	txErr error    // if non-nil, Send returns this instead of recording
}

// Compile-time assertion.
var _ link.Leg = (*Leg)(nil)

// New returns a fresh in-memory leg with no queued RX frames.
func New() *Leg {
	return &Leg{
		rxCh:     make(chan link.Frame, 64),
		closedCh: make(chan struct{}),
	}
}

// PushRX queues a frame for delivery on the next [Leg.Receive] channel read.
// Call before the behavior starts (or from a test goroutine that races with
// the behavior — the channel is buffered). Pushing after [Leg.Close] is a
// no-op.
func (l *Leg) PushRX(data []byte) {
	if l.closed.Load() {
		return
	}
	l.mu.Lock()
	l.rx = append(l.rx, link.Frame{Data: append([]byte(nil), data...)})
	l.mu.Unlock()
}

// PushRXFrame queues a pre-built [link.Frame] (including an error).
func (l *Leg) PushRXFrame(f link.Frame) {
	if l.closed.Load() {
		return
	}
	l.mu.Lock()
	l.rx = append(l.rx, f)
	l.mu.Unlock()
}

// Send records pkt on the TX list and returns nil (or the planted error).
func (l *Leg) Send(_ context.Context, pkt []byte) error {
	if l.closed.Load() {
		return nil
	}
	l.txMu.Lock()
	defer l.txMu.Unlock()
	if l.txErr != nil {
		return l.txErr
	}
	l.sends = append(l.sends, append([]byte(nil), pkt...))
	return nil
}

// SetFilter is a no-op; the test harness does not filter.
func (l *Leg) SetFilter(_ []link.RawInstruction) error { return nil }

// Receive returns a channel that delivers queued RX frames until the
// context is canceled or the leg is closed. Pending frames are drained
// first; the channel is then closed.
func (l *Leg) Receive(ctx context.Context) <-chan link.Frame {
	out := make(chan link.Frame, 64)
	go func() {
		defer close(out)
		// Drain pending frames.
		for {
			l.mu.Lock()
			if len(l.rx) == 0 {
				l.mu.Unlock()
				break
			}
			f := l.rx[0]
			l.rx = l.rx[1:]
			l.mu.Unlock()
			select {
			case out <- f:
			case <-ctx.Done():
				return
			case <-l.closedCh:
				return
			}
		}
		// Wait for cancellation or close.
		select {
		case <-ctx.Done():
		case <-l.closedCh:
		}
	}()
	return out
}

// Close marks the leg closed and unblocks any pending Receive. Idempotent.
func (l *Leg) Close() error {
	if l.closed.Swap(true) {
		return nil
	}
	close(l.closedCh)
	return nil
}

// TX returns a copy of the recorded Send payloads in send order.
func (l *Leg) TX() [][]byte {
	l.txMu.Lock()
	defer l.txMu.Unlock()
	out := make([][]byte, len(l.sends))
	for i, s := range l.sends {
		out[i] = append([]byte(nil), s...)
	}
	return out
}

// SendCount returns the number of recorded Send calls.
func (l *Leg) SendCount() int {
	l.txMu.Lock()
	defer l.txMu.Unlock()
	return len(l.sends)
}

// SetTXError plants an error for subsequent Send calls (pass nil to clear).
// Useful for testing error paths.
func (l *Leg) SetTXError(err error) {
	l.txMu.Lock()
	l.txErr = err
	l.txMu.Unlock()
}

// ResetTX clears the recorded TX history.
func (l *Leg) ResetTX() {
	l.txMu.Lock()
	l.sends = l.sends[:0]
	l.txMu.Unlock()
}
