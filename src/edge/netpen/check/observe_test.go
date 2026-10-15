package check_test

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/testtest"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
)

var (
	noise  = []byte("noise")
	wanted = []byte("wanted")
)

func isWanted(data []byte) bool { return bytes.Equal(data, wanted) }

// exitLeg wraps the test leg so a test can see that the receiver the
// helper started has exited by the time the helper returns, which the
// [link.Leg] contract needs before the next Receive.
type exitLeg struct {
	*testtest.Leg
	exited atomic.Bool
}

func (l *exitLeg) Receive(ctx context.Context) <-chan link.Frame {
	in := l.Leg.Receive(ctx)
	out := make(chan link.Frame)
	go func() {
		defer close(out)
		for f := range in {
			out <- f
		}
		l.exited.Store(true)
	}()
	return out
}

func newExitLeg(t *testing.T) *exitLeg {
	t.Helper()
	leg := &exitLeg{Leg: testtest.New()}
	t.Cleanup(func() { _ = leg.Close() })
	return leg
}

// TestFirstReturnsTheMatchBeforeTheWindow pins that a match ends the
// observation at once, with the frames before it counted as skipped and
// none of the window spent.
func TestFirstReturnsTheMatchBeforeTheWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leg := newExitLeg(t)
		for range 3 {
			leg.PushRX(noise)
		}
		leg.PushRX(wanted)
		got, obs, err := check.First(context.Background(), leg, 3*time.Second, isWanted)
		if err != nil {
			t.Fatalf("First() error = %v, want nil", err)
		}
		if !bytes.Equal(got, wanted) {
			t.Errorf("First() = %q, want %q", got, wanted)
		}
		if obs.Skipped != 3 {
			t.Errorf("Skipped = %d, want 3", obs.Skipped)
		}
		if obs.Elapsed >= 3*time.Second {
			t.Errorf("Elapsed = %v, want less than the 3s window", obs.Elapsed)
		}
		if !leg.exited.Load() {
			t.Error("the receiver was still running when First returned")
		}
	})
}

// TestFirstReturnsNoFrameAtTheWindow pins that skipped frames alone run the
// window out: no frame, no error, and the elapsed time is the window.
func TestFirstReturnsNoFrameAtTheWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leg := newExitLeg(t)
		for range 3 {
			leg.PushRX(noise)
		}
		got, obs, err := check.First(context.Background(), leg, 2*time.Second, isWanted)
		if err != nil {
			t.Fatalf("First() error = %v, want nil", err)
		}
		if got != nil {
			t.Errorf("First() = %q, want nil", got)
		}
		if obs.Skipped != 3 {
			t.Errorf("Skipped = %d, want 3", obs.Skipped)
		}
		if obs.Elapsed != 2*time.Second {
			t.Errorf("Elapsed = %v, want 2s", obs.Elapsed)
		}
		if !leg.exited.Load() {
			t.Error("the receiver was still running when First returned")
		}
	})
}

// TestCollectKeepsEveryMatchUntilTheWindow pins that Collect does not stop
// on a match: frames pushed during the window are read, the matches are
// kept in order, and the observation ends at the window.
func TestCollectKeepsEveryMatchUntilTheWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leg := newExitLeg(t)
		leg.PushRX(wanted)
		leg.PushRX(noise)
		go func() {
			time.Sleep(500 * time.Millisecond)
			leg.PushRX(wanted)
		}()
		got, obs, err := check.Collect(context.Background(), leg, time.Second, isWanted)
		if err != nil {
			t.Fatalf("Collect() error = %v, want nil", err)
		}
		if len(got) != 2 {
			t.Errorf("Collect() kept %d frames, want 2", len(got))
		}
		if obs.Skipped != 1 {
			t.Errorf("Skipped = %d, want 1", obs.Skipped)
		}
		if obs.Elapsed != time.Second {
			t.Errorf("Elapsed = %v, want 1s", obs.Elapsed)
		}
		if !leg.exited.Load() {
			t.Error("the receiver was still running when Collect returned")
		}
	})
}

// TestFirstReturnsAReceiveErrorAtOnce pins that a terminal frame error is
// the helper's error, returned without waiting for the window.
func TestFirstReturnsAReceiveErrorAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leg := newExitLeg(t)
		errReceive := errors.New("capture failed")
		leg.PushRX(noise)
		leg.PushRXFrame(link.Frame{Err: errReceive})
		_, obs, err := check.First(context.Background(), leg, 3*time.Second, isWanted)
		if !errors.Is(err, errReceive) {
			t.Errorf("First() error = %v, want %v", err, errReceive)
		}
		if obs.Elapsed != 0 {
			t.Errorf("Elapsed = %v, want 0", obs.Elapsed)
		}
	})
}

// TestFirstEndsEarlyOnAClosedChannel pins that a leg closed under the
// helper ends the window with no frame and no error, since the leg has
// nothing more to deliver.
func TestFirstEndsEarlyOnAClosedChannel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leg := newExitLeg(t)
		leg.PushRX(noise)
		go func() {
			time.Sleep(time.Second)
			_ = leg.Close()
		}()
		got, obs, err := check.First(context.Background(), leg, 5*time.Second, isWanted)
		if err != nil {
			t.Errorf("First() error = %v, want nil", err)
		}
		if got != nil {
			t.Errorf("First() = %q, want nil", got)
		}
		if obs.Elapsed != time.Second {
			t.Errorf("Elapsed = %v, want 1s, the close time", obs.Elapsed)
		}
	})
}

// TestFirstCancellationWinsOverAClosedChannel pins that a canceled context
// is reported as ctx.Err() even though cancellation also closes the leg's
// channel, so a caller never mistakes its own stop for a quiet leg.
func TestFirstCancellationWinsOverAClosedChannel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leg := newExitLeg(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, err := check.First(ctx, leg, 5*time.Second, isWanted)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("First() error = %v, want %v", err, context.Canceled)
		}
		if !leg.exited.Load() {
			t.Error("the receiver was still running when First returned")
		}
	})
}

// TestObserveRefusesAZeroWindow pins that a zero window is a caller error
// rather than an observation that ends at once, since the runner always
// supplies one.
func TestObserveRefusesAZeroWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leg := newExitLeg(t)
		leg.PushRX(wanted)
		if _, _, err := check.First(context.Background(), leg, 0, isWanted); err == nil {
			t.Error("First() with a zero window returned nil, want an error")
		}
		if _, _, err := check.Collect(context.Background(), leg, -time.Second, isWanted); err == nil {
			t.Error("Collect() with a negative window returned nil, want an error")
		}
	})
}
