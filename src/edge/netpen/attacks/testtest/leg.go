// Package testtest is netpen's shared in-memory [link.Leg] harness for
// behavior tests. A [Leg] is fed from pcap fixtures on RX and
// records every Send call on TX, so a behavior's frame sequence is asserted
// byte-for-byte or by field-set without a socket.
//
// SetFilter is a no-op because behavior tests supply their own RX inputs.
package testtest

import (
	"context"
	"sync"
	"sync/atomic"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/spawn"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
)

// Leg is an in-memory [link.Leg] for behavior tests. RX frames are fed from
// a pre-loaded queue (pushed via [Leg.PushRX]); TX frames are recorded in
// order for post-run assertion. The leg is safe for one reader and one
// writer (the behavior's goroutine), matching the [link.Leg] contract.
// Use [New] to initialize a Leg; its zero value is not usable.
type Leg struct {
	mu       sync.Mutex   // guards rx
	rx       []link.Frame // pending frames not yet delivered
	rxReady  chan struct{}
	closed   atomic.Bool
	closedCh chan struct{}

	txMu  sync.Mutex // guards sends, txErr, and Send against Close
	sends [][]byte   // recorded Send payloads, in order
	txErr error      // if non-nil, Send returns this instead of recording
}

var _ link.Leg = (*Leg)(nil)

// New returns a fresh in-memory leg with no queued RX frames.
func New() *Leg {
	return &Leg{
		rxReady:  make(chan struct{}, 1),
		closedCh: make(chan struct{}),
	}
}

// PushRX queues a frame for delivery on the next [Leg.Receive] channel read.
// It copies data and wakes an active receiver. Pushing after [Leg.Close]
// is a no-op.
func (l *Leg) PushRX(data []byte) {
	l.PushRXFrame(link.Frame{Data: data})
}

// PushRXFrame copies and queues a [link.Frame], including any terminal error.
// Pushing after [Leg.Close] is a no-op.
func (l *Leg) PushRXFrame(f link.Frame) {
	l.mu.Lock()
	if l.closed.Load() {
		l.mu.Unlock()
		return
	}
	f.Data = append([]byte(nil), f.Data...)
	l.rx = append(l.rx, f)
	l.mu.Unlock()
	select {
	case l.rxReady <- struct{}{}:
	default:
	}
}

// Send copies pkt into the TX list. A canceled context returns ctx.Err();
// a closed leg returns an error carrying [link.ErrCodeLegOpen]. A planted
// error takes effect before recording.
func (l *Leg) Send(ctx context.Context, pkt []byte) error {
	l.txMu.Lock()
	defer l.txMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if l.closed.Load() {
		return errs.New().Code(link.ErrCodeLegOpen).Msg("send on closed test leg")
	}
	if l.txErr != nil {
		return l.txErr
	}
	l.sends = append(l.sends, append([]byte(nil), pkt...))
	return nil
}

// SetFilter is a no-op; the test harness does not filter.
func (l *Leg) SetFilter(_ []link.RawInstruction) error { return nil }

// Receive returns a channel that delivers queued RX frames until the
// context is canceled, a terminal error is delivered, or the leg is closed.
// It waits for future pushes after the pending queue drains. Drain the
// returned channel before starting another Receive call.
func (l *Leg) Receive(ctx context.Context) <-chan link.Frame {
	out := make(chan link.Frame, 64)
	// close(out) is fn's own deferred call, so it still runs on a panic
	// unwind and a consumer draining out until close never hangs.
	spawn.Go(ctx, "netpen testtest leg receive", func() {
		defer close(out)
		for {
			if ctx.Err() != nil || l.closed.Load() {
				return
			}
			l.mu.Lock()
			if len(l.rx) == 0 {
				l.mu.Unlock()
				select {
				case <-l.rxReady:
					continue
				case <-ctx.Done():
					return
				case <-l.closedCh:
					return
				}
			}
			f := l.rx[0]
			l.rx[0] = link.Frame{}
			l.rx = l.rx[1:]
			l.mu.Unlock()
			select {
			case out <- f:
			case <-ctx.Done():
				return
			case <-l.closedCh:
				return
			}
			if f.Err != nil {
				return
			}
		}
	})
	return out
}

// Close marks the leg closed and unblocks any pending Receive. Idempotent.
func (l *Leg) Close() error {
	l.txMu.Lock()
	defer l.txMu.Unlock()
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
