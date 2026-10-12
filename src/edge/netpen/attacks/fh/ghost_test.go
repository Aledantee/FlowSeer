package fh_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/fh"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/testtest"
	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

type ghostWatchLeg struct {
	*testtest.Leg
	frames    chan link.Frame
	onReceive func()
}

func (l *ghostWatchLeg) Receive(context.Context) <-chan link.Frame {
	if l.onReceive != nil {
		l.onReceive()
	}
	return l.frames
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
				close(watch.frames)
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
	close(watch.frames)

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
	close(watch.frames)

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
