package l2_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/l2"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/testtest"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

func TestVlanEnumReturnsCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leg := testtest.New()
		t.Cleanup(func() { _ = leg.Close() })
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			done <- l2.RunVlanEnum(ctx, runner.Deps{AttackLeg: leg})
		}()
		synctest.Wait()
		cancel()
		if err := <-done; err != context.Canceled {
			t.Errorf("RunVlanEnum() error = %v, want %v", err, context.Canceled)
		}
	})
}

func TestVlanEnumReturnsReceiveError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leg := testtest.New()
		t.Cleanup(func() { _ = leg.Close() })
		errReceive := errors.New("capture failed")
		leg.PushRXFrame(link.Frame{Err: errReceive})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		started := time.Now()
		err := l2.RunVlanEnum(ctx, runner.Deps{AttackLeg: leg})
		if !errors.Is(err, errReceive) {
			t.Errorf("RunVlanEnum() error = %v, want %v", err, errReceive)
		}
		if elapsed := time.Since(started); elapsed != 0 {
			t.Errorf("RunVlanEnum() delayed receive failure by %v, want 0", elapsed)
		}
	})
}

func TestVlanEnumIgnoresTruncatedHeaders(t *testing.T) {
	frame := fixturePackets(t, "vlanenum.pcap")[0]
	for _, n := range []int{0, 13, 14, 17} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := testtest.New()
				t.Cleanup(func() { _ = leg.Close() })
				leg.PushRX(frame[:n])
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if err := l2.RunVlanEnum(ctx, runner.Deps{AttackLeg: leg}); err == nil {
					t.Errorf("RunVlanEnum() with %d header bytes returned nil, want error", n)
				}
			})
		})
	}
}
