package fh_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/fh"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/testtest"
	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

// ghostWatchLeg hands the behavior a channel the test controls. Under the
// [link.Leg] contract the channel closes once the context ends, which
// closeOnDone does for a channel the test did not close itself.
type ghostWatchLeg struct {
	*testtest.Leg
	frames    chan link.Frame
	onReceive func()
	closeOnce sync.Once
}

func (l *ghostWatchLeg) Receive(ctx context.Context) <-chan link.Frame {
	if l.onReceive != nil {
		l.onReceive()
	}
	context.AfterFunc(ctx, func() { l.closeOnce.Do(func() { close(l.frames) }) })
	return l.frames
}

// closeFrames closes the channel the test owns, once.
func (l *ghostWatchLeg) closeFrames() {
	l.closeOnce.Do(func() { close(l.frames) })
}

// ghostCatalog is the family catalog with ghost wrapped so a test can
// observe the behavior's own error and context.
func ghostCatalog(wrap func(ctx context.Context, deps check.Deps) (check.Verdict, error)) catalog.Catalog {
	bs := fh.Behaviors()
	for i := range bs {
		if bs[i].Name == "ghost" {
			bs[i].Run = wrap
		}
	}
	return catalog.MustNew(bs...)
}

func runGhost(t *testing.T, opts runner.Options) ([]findings.Record, error) {
	t.Helper()
	if opts.MaxImpact == "" {
		opts.MaxImpact = check.ImpactTemporary
	}
	r := runner.NewRunner(opts)
	var recs []findings.Record
	done := make(chan struct{})
	go func() {
		for rec := range r.Stream().Iter() {
			recs = append(recs, rec)
		}
		close(done)
	}()
	err := r.Run(context.Background())
	r.Wait()
	<-done
	return recs, err
}

func TestGhostObservationCanceled(t *testing.T) {
	for _, closed := range []bool{false, true} {
		name := "open receive channel"
		if closed {
			name = "closed receive channel"
		}
		t.Run(name, func(t *testing.T) {
			leg := fixtureLeg(t, net.IPv4(10, 0, 0, 2))
			watch := &ghostWatchLeg{Leg: leg, frames: make(chan link.Frame)}
			if closed {
				watch.closeFrames()
			}

			var behaviorErr error
			recs, err := runGhost(t, runner.Options{
				Catalog: ghostCatalog(func(ctx context.Context, deps check.Deps) (check.Verdict, error) {
					observeCtx, cancel := context.WithCancel(ctx)
					defer cancel()
					watch.onReceive = cancel
					var v check.Verdict
					v, behaviorErr = fh.RunGhost(observeCtx, deps)
					return v, behaviorErr
				}),
				AttackLeg: leg,
				WatchLeg:  watch,
				Attacks:   []runner.AttackRef{{Name: "ghost"}},
			})
			if err != nil {
				t.Fatalf("run ghost: %v", err)
			}
			if behaviorErr != context.Canceled {
				t.Errorf("behavior error: got %v, want %v", behaviorErr, context.Canceled)
			}
			if finding := findFinding(t, recs); finding != nil {
				t.Errorf("finding after cancellation: got %s, want none", finding.Detail)
			}
		})
	}
}

func TestGhostReceiveError(t *testing.T) {
	leg := fixtureLeg(t, net.IPv4(10, 0, 0, 2))
	wantErr := errors.New("capture stopped")
	watch := &ghostWatchLeg{Leg: leg, frames: make(chan link.Frame, 1)}
	watch.frames <- link.Frame{Err: wantErr}
	watch.closeFrames()

	var behaviorErr error
	recs, err := runGhost(t, runner.Options{
		Catalog: ghostCatalog(func(ctx context.Context, deps check.Deps) (check.Verdict, error) {
			var v check.Verdict
			v, behaviorErr = fh.RunGhost(ctx, deps)
			return v, behaviorErr
		}),
		AttackLeg: leg,
		WatchLeg:  watch,
		Attacks:   []runner.AttackRef{{Name: "ghost"}},
	})
	if err != nil {
		t.Fatalf("run ghost: %v", err)
	}
	if !errors.Is(behaviorErr, wantErr) {
		t.Errorf("behavior error: got %v, want %v", behaviorErr, wantErr)
	}
	if finding := findFinding(t, recs); finding != nil {
		t.Errorf("finding after receive error: got %s, want none", finding.Detail)
	}
	if v := verdictOf(recs); v == nil || v.Kind != findings.VerdictPending {
		t.Errorf("verdict = %+v, want pending with the receive error", v)
	}
}

func TestGhostTruncatedFrames(t *testing.T) {
	leg := fixtureLeg(t, net.IPv4(10, 0, 0, 2))
	watch := &ghostWatchLeg{Leg: leg, frames: make(chan link.Frame, 3)}
	fixture := fixturePackets(t, "ghost.pcap")[0]
	for _, length := range []int{0, 12, 13} {
		watch.frames <- link.Frame{Data: fixture[:length]}
	}
	watch.closeFrames()

	recs, err := runOne(t, runner.Options{
		AttackLeg: leg,
		WatchLeg:  watch,
		Attacks:   []runner.AttackRef{{Name: "ghost"}},
	})
	if err != nil {
		t.Fatalf("run ghost: %v", err)
	}
	finding := findFinding(t, recs)
	if finding == nil {
		t.Fatal("no finding emitted")
	}
	var detail struct {
		Traversal string `json:"traversal"`
	}
	if err := json.Unmarshal(finding.Detail, &detail); err != nil {
		t.Fatalf("unmarshal finding: %v", err)
	}
	if detail.Traversal != "not-forwarded" {
		t.Errorf("traversal for truncated frames: got %q, want %q", detail.Traversal, "not-forwarded")
	}
}

// TestGhostEmptyWindowIsResisted pins the observation window: a watch leg
// that delivers nothing yields a resisted verdict once the window ends,
// with the observed time at or above the window and the run back within
// a generous bound of it. The bound is the timing under test.
func TestGhostEmptyWindowIsResisted(t *testing.T) {
	leg := fixtureLeg(t, net.IPv4(10, 0, 0, 2))
	watch := &ghostWatchLeg{Leg: leg, frames: make(chan link.Frame)}
	const window = 50 * time.Millisecond

	start := time.Now()
	recs, err := runOne(t, runner.Options{
		AttackLeg: leg,
		WatchLeg:  watch,
		Attacks:   []runner.AttackRef{{Name: "ghost"}},
		Observe:   window,
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("run ghost: %v", err)
	}
	v := verdictOf(recs)
	if v == nil || v.Kind != findings.VerdictResisted {
		t.Fatalf("verdict = %+v, want resisted on an empty window", v)
	}
	if v.ObservedMS < window.Milliseconds() {
		t.Errorf("observed_ms = %d, want at least the %d ms window", v.ObservedMS, window.Milliseconds())
	}
	if elapsed > 20*window {
		t.Errorf("run took %v, want it bounded by the %v window", elapsed, window)
	}
}

// ghostEmitter keeps the findings a direct call to the behavior emits.
type ghostEmitter struct {
	findings []json.RawMessage
}

func (e *ghostEmitter) Emit(findings.Record) bool { return true }

func (e *ghostEmitter) Finding(_ string, detail json.RawMessage) bool {
	e.findings = append(e.findings, detail)
	return true
}

func (e *ghostEmitter) Progress(_, _ string) bool { return true }

// ghostTraversal returns the traversal field of the one finding em holds.
func ghostTraversal(t *testing.T, em *ghostEmitter) string {
	t.Helper()
	if len(em.findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(em.findings))
	}
	var detail struct {
		Traversal string `json:"traversal"`
	}
	if err := json.Unmarshal(em.findings[0], &detail); err != nil {
		t.Fatalf("unmarshal finding: %v", err)
	}
	return detail.Traversal
}

// TestGhostAmbientOnlyIsResisted pins that the ambient frames alone, with
// no ghost frame among them, leave the verdict resisted at the window
// rather than confirmed on a count, with the finding reading not-forwarded.
func TestGhostAmbientOnlyIsResisted(t *testing.T) {
	ambient := fixturePackets(t, "ghost_ambient.pcap")
	synctest.Test(t, func(t *testing.T) {
		attack := fixtureLeg(t, net.IPv4(10, 0, 0, 2))
		watch := fixtureLeg(t, net.IPv4(10, 0, 0, 2))
		for range 3 {
			for _, f := range ambient {
				watch.PushRX(f)
			}
		}
		em := &ghostEmitter{}
		start := time.Now()
		v, err := fh.RunGhost(context.Background(), check.Deps{AttackLeg: attack, WatchLeg: watch, Observe: 2 * time.Second, Emitter: em})
		if err != nil {
			t.Fatalf("RunGhost() error = %v, want nil", err)
		}
		if v.Kind != findings.VerdictResisted {
			t.Errorf("verdict = %+v, want resisted with only ambient frames", v)
		}
		if elapsed := time.Since(start); elapsed != 2*time.Second {
			t.Errorf("returned after %v, want the 2s window", elapsed)
		}
		if got := ghostTraversal(t, em); got != "not-forwarded" {
			t.Errorf("traversal = %q, want not-forwarded", got)
		}
	})
}

// TestGhostAmbientThenGhostIsConfirmed pins that three unrelated frames
// ahead of the ghost frames do not end the observation: the ghost frame
// behind them is read and the verdict is confirmed within the window.
func TestGhostAmbientThenGhostIsConfirmed(t *testing.T) {
	ambient := fixturePackets(t, "ghost_ambient.pcap")
	ghost := fixturePackets(t, "ghost.pcap")
	synctest.Test(t, func(t *testing.T) {
		attack := fixtureLeg(t, net.IPv4(10, 0, 0, 2))
		watch := fixtureLeg(t, net.IPv4(10, 0, 0, 2))
		for range 3 {
			watch.PushRX(ambient[0])
		}
		for _, f := range ghost {
			watch.PushRX(f)
		}
		em := &ghostEmitter{}
		v, err := fh.RunGhost(context.Background(), check.Deps{AttackLeg: attack, WatchLeg: watch, Observe: 2 * time.Second, Emitter: em})
		if err != nil {
			t.Fatalf("RunGhost() error = %v, want nil", err)
		}
		if v.Kind != findings.VerdictConfirmed || v.Observed >= 2*time.Second {
			t.Errorf("verdict = %+v, want confirmed within the window", v)
		}
		if got := ghostTraversal(t, em); got != "forwarded" {
			t.Errorf("traversal = %q, want forwarded", got)
		}
	})
}
