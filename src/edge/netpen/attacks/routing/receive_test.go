package routing_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/routing"
	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

type receiveFailureLeg struct {
	link.Leg
	err    error
	cancel context.CancelFunc
}

func (l *receiveFailureLeg) Receive(context.Context) <-chan link.Frame {
	ch := make(chan link.Frame, 1)
	if l.cancel != nil {
		l.cancel()
	}
	if l.err != nil {
		ch <- link.Frame{Err: l.err}
	}
	close(ch)
	return ch
}

// TestRoutingReceiveFailureStopsSequence pins what the sequence does with
// the leg's receive outcomes: a terminal frame error, a receiver that
// closes with nothing to deliver, and a cancellation each stop it after the
// hello, since a leg that stops before the window has observed nothing.
func TestRoutingReceiveFailureStopsSequence(t *testing.T) {
	for _, name := range []string{"ospf", "eigrp"} {
		for _, tc := range []struct {
			name     string
			rxErr    error
			cancel   bool
			want     error
			wantText string
		}{
			{name: "terminal error", rxErr: io.ErrUnexpectedEOF, want: io.ErrUnexpectedEOF},
			{name: "closed receiver", wantText: "leg closed before the window"},
			{name: "canceled receive", cancel: true, want: context.Canceled},
		} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				inner := fixtureLeg(t)
				leg := &receiveFailureLeg{Leg: inner, err: tc.rxErr}
				var behaviorErr error
				bs := routing.Behaviors()
				for i := range bs {
					if bs[i].Name == name {
						run := bs[i].Run
						bs[i].Run = func(ctx context.Context, deps check.Deps) (check.Verdict, error) {
							ctx, cancel := context.WithCancel(ctx)
							defer cancel()
							if tc.cancel {
								leg.cancel = cancel
							}
							var v check.Verdict
							v, behaviorErr = run(ctx, deps)
							return v, behaviorErr
						}
					}
				}
				recs, err := runOne(t, runner.Options{
					Catalog:        catalog.MustNew(bs...),
					AttackLeg:      leg,
					Attacks:        []runner.AttackRef{{Name: name}},
					Target:         fixtureTarget(),
					TeardownBudget: time.Second,
				})
				if err != nil {
					t.Fatalf("run: %v", err)
				}
				switch {
				case tc.wantText != "":
					if behaviorErr == nil || !strings.Contains(behaviorErr.Error(), tc.wantText) {
						t.Errorf("behavior error: got %v, want one naming %q", behaviorErr, tc.wantText)
					}
				case !errors.Is(behaviorErr, tc.want):
					t.Errorf("behavior error: got %v, want %v", behaviorErr, tc.want)
				}
				if tc.cancel && behaviorErr != context.Canceled {
					t.Errorf("cancellation: got %v, want unwrapped context.Canceled", behaviorErr)
				}
				if f := findFinding(t, recs); f != nil {
					t.Errorf("finding after receive failure: %+v", f)
				}
				wantSends := 2 // Initial hello and the armed goodbye.
				if name == "ospf" {
					wantSends++ // OSPF also arms an LSA flush.
				}
				if got := inner.SendCount(); got != wantSends {
					t.Errorf("send count: got %d, want %d", got, wantSends)
				}
			})
		}
	}
}
