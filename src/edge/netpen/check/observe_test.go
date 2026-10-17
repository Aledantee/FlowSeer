package check_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
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

// TestObserveClosedLegIsAnError pins that a leg closed under a live
// context is an error naming the close, not a quiet window: a leg that
// stops delivering has not observed the window, and a quiet verdict from
// it would be the leg's failure read as the segment's silence.
func TestObserveClosedLegIsAnError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leg := newExitLeg(t)
		leg.PushRX(noise)
		go func() {
			time.Sleep(time.Second)
			_ = leg.Close()
		}()
		got, obs, err := check.First(context.Background(), leg, 5*time.Second, isWanted)
		if err == nil || !strings.Contains(err.Error(), "leg closed before the window") {
			t.Errorf("First() error = %v, want the closed-leg error", err)
		}
		if got != nil {
			t.Errorf("First() = %q, want nil", got)
		}
		if obs.Elapsed != time.Second {
			t.Errorf("Elapsed = %v, want 1s, the close time", obs.Elapsed)
		}
		if obs.Skipped != 1 {
			t.Errorf("Skipped = %d, want 1", obs.Skipped)
		}
	})
}

// TestScanKeepsNoFrame pins that Scan hands every frame to the callback,
// counts each as skipped, and ends at the window.
func TestScanKeepsNoFrame(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leg := newExitLeg(t)
		leg.PushRX(wanted)
		leg.PushRX(noise)
		go func() {
			time.Sleep(500 * time.Millisecond)
			leg.PushRX(wanted)
		}()
		var seen int
		obs, err := check.Scan(context.Background(), leg, time.Second, func(data []byte) {
			if isWanted(data) {
				seen++
			}
		})
		if err != nil {
			t.Fatalf("Scan() error = %v, want nil", err)
		}
		if seen != 2 {
			t.Errorf("the callback saw %d wanted frames, want 2", seen)
		}
		if obs.Skipped != 3 {
			t.Errorf("Skipped = %d, want 3, every frame", obs.Skipped)
		}
		if obs.Elapsed != time.Second {
			t.Errorf("Elapsed = %v, want 1s", obs.Elapsed)
		}
		if !leg.exited.Load() {
			t.Error("the receiver was still running when Scan returned")
		}
	})
}

// TestSustainSendsOncePerInterval pins the alternation: one send, one read
// of the interval, repeated until the window is spent, so a 9 s window at
// 3 s sends three times and the elapsed time is the window.
func TestSustainSendsOncePerInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leg := newExitLeg(t)
		var sends []time.Duration
		start := time.Now()
		got, obs, err := check.Sustain(context.Background(), leg, 9*time.Second, 3*time.Second,
			func(context.Context) error {
				sends = append(sends, time.Since(start))
				return nil
			}, isWanted)
		if err != nil {
			t.Fatalf("Sustain() error = %v, want nil", err)
		}
		if got != nil {
			t.Errorf("Sustain() = %q, want nil at the window", got)
		}
		want := []time.Duration{0, 3 * time.Second, 6 * time.Second}
		if len(sends) != len(want) {
			t.Fatalf("sends at %v, want %v", sends, want)
		}
		for i := range want {
			if sends[i] != want[i] {
				t.Errorf("send %d at %v, want %v", i, sends[i], want[i])
			}
		}
		if obs.Elapsed != 9*time.Second {
			t.Errorf("Elapsed = %v, want 9s", obs.Elapsed)
		}
	})
}

// TestSustainStopsOnMatch pins that a match during the second read ends the
// call with that frame, no further send, and the elapsed time at the match.
func TestSustainStopsOnMatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leg := newExitLeg(t)
		sends := 0
		go func() {
			time.Sleep(4 * time.Second)
			leg.PushRX(noise)
			leg.PushRX(wanted)
		}()
		got, obs, err := check.Sustain(context.Background(), leg, 9*time.Second, 3*time.Second,
			func(context.Context) error {
				sends++
				return nil
			}, isWanted)
		if err != nil {
			t.Fatalf("Sustain() error = %v, want nil", err)
		}
		if !bytes.Equal(got, wanted) {
			t.Errorf("Sustain() = %q, want %q", got, wanted)
		}
		if sends != 2 {
			t.Errorf("sends = %d, want 2", sends)
		}
		if obs.Skipped != 1 {
			t.Errorf("Skipped = %d, want 1", obs.Skipped)
		}
		if obs.Elapsed != 4*time.Second {
			t.Errorf("Elapsed = %v, want 4s, the match time", obs.Elapsed)
		}
	})
}

// TestSustainReturnsAtTheWindow pins that a window that is not a multiple
// of the interval ends at the window, with the last read shortened, and
// that the match closure's last state is the caller's to read.
func TestSustainReturnsAtTheWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leg := newExitLeg(t)
		go func() {
			time.Sleep(3500 * time.Millisecond)
			leg.PushRX(noise)
		}()
		var last []byte
		got, obs, err := check.Sustain(context.Background(), leg, 5*time.Second, 3*time.Second,
			func(context.Context) error { return nil },
			func(data []byte) bool {
				last = data
				return false
			})
		if err != nil {
			t.Fatalf("Sustain() error = %v, want nil", err)
		}
		if got != nil {
			t.Errorf("Sustain() = %q, want nil at the window", got)
		}
		if !bytes.Equal(last, noise) {
			t.Errorf("last frame the closure saw = %q, want %q", last, noise)
		}
		if obs.Elapsed != 5*time.Second {
			t.Errorf("Elapsed = %v, want 5s", obs.Elapsed)
		}
		if _, _, err := check.Sustain(context.Background(), leg, 5*time.Second, 0,
			func(context.Context) error { return nil }, isWanted); err == nil {
			t.Error("Sustain() with a zero interval returned nil, want an error")
		}
	})
}

// closedLeg is a leg whose Receive hands back a channel that is already
// closed, so a select between the context and the channel has both ready.
type closedLeg struct {
	*testtest.Leg
}

func (l closedLeg) Receive(context.Context) <-chan link.Frame {
	ch := make(chan link.Frame)
	close(ch)
	return ch
}

// TestFirstCancellationWinsOverAClosedChannel pins that a canceled context
// is reported as ctx.Err() even when the leg's channel is already closed,
// so a caller never mistakes its own stop for a quiet leg. The select
// picks among ready cases at random, so the call is repeated until both
// branches have been taken.
func TestFirstCancellationWinsOverAClosedChannel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leg := closedLeg{Leg: testtest.New()}
		t.Cleanup(func() { _ = leg.Close() })
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		for range 50 {
			_, _, err := check.First(ctx, leg, 5*time.Second, isWanted)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("First() error = %v, want %v", err, context.Canceled)
			}
		}
		exit := newExitLeg(t)
		if _, _, err := check.First(ctx, exit, 5*time.Second, isWanted); !errors.Is(err, context.Canceled) {
			t.Errorf("First() error = %v, want %v", err, context.Canceled)
		}
		if !exit.exited.Load() {
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
