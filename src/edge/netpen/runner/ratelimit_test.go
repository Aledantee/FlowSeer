package runner

import (
	"context"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/edge/netpen/link"
)

type countingLeg struct {
	link.Leg
	sends int
}

func (l *countingLeg) Send(context.Context, []byte) error {
	l.sends++
	return nil
}

// fakeClock stands in for the real clock: now advances only through sleep,
// which records every wait the pacer asked for.
type fakeClock struct {
	now   time.Time
	waits []time.Duration
}

func (c *fakeClock) sleep(_ context.Context, d time.Duration) error {
	c.waits = append(c.waits, d)
	c.now = c.now.Add(d)
	return nil
}

// TestPacedLegSpacesSendsByRate pins the pacer on a fake clock: at 4 per
// second the first send goes out at once and each later send waits for
// the remainder of its 250 ms slot, so ten sends ask for nine waits that
// sum to 2.25 s.
func TestPacedLegSpacesSendsByRate(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	inner := &countingLeg{}
	leg := newPacedLeg(inner, 4)
	leg.now = func() time.Time { return clock.now }
	leg.sleep = clock.sleep

	for range 10 {
		if err := leg.Send(context.Background(), []byte{1}); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}
	if inner.sends != 10 {
		t.Errorf("inner sends = %d, want 10", inner.sends)
	}
	if len(clock.waits) != 9 {
		t.Fatalf("waits = %v, want 9 of them", clock.waits)
	}
	var total time.Duration
	for i, w := range clock.waits {
		if w != 250*time.Millisecond {
			t.Errorf("wait %d = %v, want 250ms", i, w)
		}
		total += w
	}
	if total != 2250*time.Millisecond {
		t.Errorf("total wait = %v, want 2.25s", total)
	}
}

// TestPacedLegDoesNotWaitAfterAnIdleGap pins that a slot already in the
// past costs nothing: a send after a long pause goes out at once.
func TestPacedLegDoesNotWaitAfterAnIdleGap(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	leg := newPacedLeg(&countingLeg{}, 4)
	leg.now = func() time.Time { return clock.now }
	leg.sleep = clock.sleep

	if err := leg.Send(context.Background(), []byte{1}); err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(time.Second)
	if err := leg.Send(context.Background(), []byte{1}); err != nil {
		t.Fatal(err)
	}
	if len(clock.waits) != 0 {
		t.Errorf("waits = %v, want none after an idle second", clock.waits)
	}
}

// TestPacedLegCanceledWhileWaiting pins that a canceled context ends the
// wait without sending.
func TestPacedLegCanceledWhileWaiting(t *testing.T) {
	inner := &countingLeg{}
	leg := newPacedLeg(inner, 1)
	ctx, cancel := context.WithCancel(context.Background())
	if err := leg.Send(ctx, []byte{1}); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := leg.Send(ctx, []byte{1}); err != context.Canceled {
		t.Errorf("Send after cancel = %v, want context.Canceled", err)
	}
	if inner.sends != 1 {
		t.Errorf("inner sends = %d, want 1", inner.sends)
	}
}
