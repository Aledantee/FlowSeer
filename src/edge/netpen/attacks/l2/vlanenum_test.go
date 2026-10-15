package l2_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/l2"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/testtest"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
)

// recordingEmitter keeps the findings a direct call to a behavior emits.
type recordingEmitter struct {
	findings []json.RawMessage
}

func (e *recordingEmitter) Emit(findings.Record) bool { return true }

func (e *recordingEmitter) Finding(_ string, detail json.RawMessage) bool {
	e.findings = append(e.findings, detail)
	return true
}

func (e *recordingEmitter) Progress(_, _ string) bool { return true }

func TestVlanEnumReturnsCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leg := testtest.New()
		t.Cleanup(func() { _ = leg.Close() })
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			_, err := l2.RunVlanEnum(ctx, check.Deps{AttackLeg: leg, Observe: time.Second})
			done <- err
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
		_, err := l2.RunVlanEnum(ctx, check.Deps{AttackLeg: leg, Observe: time.Second})
		if !errors.Is(err, errReceive) {
			t.Errorf("RunVlanEnum() error = %v, want %v", err, errReceive)
		}
		if elapsed := time.Since(started); elapsed != 0 {
			t.Errorf("RunVlanEnum() delayed receive failure by %v, want 0", elapsed)
		}
	})
}

// TestVlanEnumIgnoresTruncatedHeaders pins that a frame without a readable
// 802.1Q header is skipped: the window runs out and the verdict is pending
// with the window in its detail, with no error and no finding.
func TestVlanEnumIgnoresTruncatedHeaders(t *testing.T) {
	frame := fixturePackets(t, "vlanenum.pcap")[0]
	for _, n := range []int{0, 13, 14, 17} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := testtest.New()
				t.Cleanup(func() { _ = leg.Close() })
				leg.PushRX(frame[:n])
				em := &recordingEmitter{}
				v, err := l2.RunVlanEnum(context.Background(), check.Deps{AttackLeg: leg, Observe: time.Second, Emitter: em})
				if err != nil {
					t.Fatalf("RunVlanEnum() with %d header bytes returned %v, want nil", n, err)
				}
				if v.Kind != findings.VerdictPending || v.Detail != "no tagged frames in 1s" {
					t.Errorf("verdict = %+v, want pending with detail %q", v, "no tagged frames in 1s")
				}
				if len(em.findings) != 0 {
					t.Errorf("findings = %d, want 0", len(em.findings))
				}
			})
		})
	}
}

// TestVlanEnumQuietLegIsPending pins that a leg with no tagged frame runs
// the window out and the verdict is pending naming the window.
func TestVlanEnumQuietLegIsPending(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		leg := testtest.New()
		t.Cleanup(func() { _ = leg.Close() })
		start := time.Now()
		v, err := l2.RunVlanEnum(context.Background(), check.Deps{AttackLeg: leg, Observe: time.Second})
		if err != nil {
			t.Fatalf("RunVlanEnum() error = %v, want nil", err)
		}
		if v.Kind != findings.VerdictPending || v.Detail != "no tagged frames in 1s" {
			t.Errorf("verdict = %+v, want pending with detail %q", v, "no tagged frames in 1s")
		}
		if elapsed := time.Since(start); elapsed != time.Second {
			t.Errorf("returned after %v, want the 1s window", elapsed)
		}
	})
}

// TestVlanEnumBusyLegReturnsAtTheWindow pins that a leg delivering a tagged
// frame every 100 ms for 5 s does not keep the observation open: the
// verdict is confirmed at the 1 s window with the ids seen by then.
func TestVlanEnumBusyLegReturnsAtTheWindow(t *testing.T) {
	frames := fixturePackets(t, "vlanenum.pcap")
	synctest.Test(t, func(t *testing.T) {
		leg := testtest.New()
		t.Cleanup(func() { _ = leg.Close() })
		stop := make(chan struct{})
		go func() {
			for i := range 50 {
				select {
				case <-stop:
					return
				case <-time.After(100 * time.Millisecond):
					leg.PushRX(frames[i%len(frames)])
				}
			}
		}()
		em := &recordingEmitter{}
		start := time.Now()
		v, err := l2.RunVlanEnum(context.Background(), check.Deps{AttackLeg: leg, Observe: time.Second, Emitter: em})
		close(stop)
		if err != nil {
			t.Fatalf("RunVlanEnum() error = %v, want nil", err)
		}
		if elapsed := time.Since(start); elapsed != time.Second {
			t.Errorf("returned after %v, want the 1s window", elapsed)
		}
		if v.Kind != findings.VerdictConfirmed || v.Detail != "VLANs 10, 20, 30, 40" || v.Observed != time.Second {
			t.Errorf("verdict = %+v, want confirmed with the four ids seen in 1s", v)
		}
		if len(em.findings) != 1 {
			t.Errorf("findings = %d, want 1", len(em.findings))
		}
	})
}
