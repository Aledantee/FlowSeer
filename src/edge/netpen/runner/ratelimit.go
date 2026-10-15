package runner

import (
	"context"
	"sync"
	"time"

	"go.aledante.io/FlowSeer/src/edge/netpen/link"
)

// pacedLeg is the [link.Leg] the runner hands a behavior when
// [Options.Rate] is positive. It spaces sends by one interval, so one
// enforcement point covers every behavior and none paces itself. A token
// bucket would be more than a packet pacer needs. Receive, SetFilter, Addr,
// and Close pass through to the wrapped leg.
//
// A pacedLeg is safe for concurrent Send calls: the next slot is guarded by
// a mutex, and a sender that must wait holds it so later senders queue
// behind it in order.
type pacedLeg struct {
	link.Leg
	interval time.Duration
	now      func() time.Time
	sleep    func(ctx context.Context, d time.Duration) error

	mu   sync.Mutex // guards next
	next time.Time
}

// newPacedLeg wraps leg so sends are at most rate per second on the real
// clock.
func newPacedLeg(leg link.Leg, rate int) *pacedLeg {
	return &pacedLeg{
		Leg:      leg,
		interval: time.Second / time.Duration(rate),
		now:      time.Now,
		sleep:    sleepContext,
	}
}

// Send waits until the next slot, claims the one after it, and sends. A
// context canceled while waiting returns ctx.Err() without sending.
func (p *pacedLeg) Send(ctx context.Context, pkt []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if wait := p.next.Sub(p.now()); wait > 0 {
		if err := p.sleep(ctx, wait); err != nil {
			return err
		}
	}
	p.next = p.now().Add(p.interval)
	return p.Leg.Send(ctx, pkt)
}

// sleepContext waits d on the real clock or until ctx is done.
func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
