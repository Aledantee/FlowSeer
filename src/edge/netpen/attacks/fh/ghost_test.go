package fh_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/fh"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/testtest"
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

func TestGhostObservationCanceled(t *testing.T) {
	for _, closed := range []bool{false, true} {
		name := "open receive channel"
		if closed {
			name = "closed receive channel"
		}
		t.Run(name, func(t *testing.T) {
			leg := testtest.New()
			t.Cleanup(func() { _ = leg.Close() })
			watch := &ghostWatchLeg{Leg: leg, frames: make(chan link.Frame)}
			if closed {
				close(watch.frames)
			}

			var behaviorErr error
			recs, err := runOne(t, runner.Options{
				AttackLeg: leg,
				WatchLeg:  watch,
				Attacks:   []runner.AttackRef{{Name: "ghost"}},
				Behaviors: map[string]runner.Behavior{
					"ghost": func(ctx context.Context, deps runner.Deps) error {
						observeCtx, cancel := context.WithCancel(ctx)
						defer cancel()
						watch.onReceive = cancel
						behaviorErr = fh.RunGhost(observeCtx, deps)
						return behaviorErr
					},
				},
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
	leg := testtest.New()
	t.Cleanup(func() { _ = leg.Close() })
	wantErr := errors.New("capture stopped")
	watch := &ghostWatchLeg{Leg: leg, frames: make(chan link.Frame, 1)}
	watch.frames <- link.Frame{Err: wantErr}
	close(watch.frames)

	var behaviorErr error
	recs, err := runOne(t, runner.Options{
		AttackLeg: leg,
		WatchLeg:  watch,
		Attacks:   []runner.AttackRef{{Name: "ghost"}},
		Behaviors: map[string]runner.Behavior{
			"ghost": func(ctx context.Context, deps runner.Deps) error {
				behaviorErr = fh.RunGhost(ctx, deps)
				return behaviorErr
			},
		},
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
}

func TestGhostTruncatedFrames(t *testing.T) {
	leg := testtest.New()
	t.Cleanup(func() { _ = leg.Close() })
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
