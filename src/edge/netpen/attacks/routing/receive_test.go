package routing_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/routing"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/testtest"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

type receiveFailureLeg struct {
	*testtest.Leg
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

func TestRoutingReceiveFailureStopsSequence(t *testing.T) {
	for _, name := range []string{"ospf", "eigrp"} {
		for _, tc := range []struct {
			name   string
			rxErr  error
			cancel bool
			want   error
		}{
			{name: "terminal error", rxErr: io.ErrUnexpectedEOF, want: io.ErrUnexpectedEOF},
			{name: "closed receiver", want: io.EOF},
			{name: "canceled receive", cancel: true, want: context.Canceled},
		} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				leg := &receiveFailureLeg{Leg: testtest.New(), err: tc.rxErr}
				t.Cleanup(func() { _ = leg.Close() })
				var behaviorErr error
				behavior := routing.Behaviors()[name]
				recs, err := runOne(t, runner.Options{
					AttackLeg:      leg,
					Attacks:        []runner.AttackRef{{Name: name}},
					TeardownBudget: time.Second,
					Behaviors: map[string]runner.Behavior{
						name: func(ctx context.Context, deps runner.Deps) error {
							ctx, cancel := context.WithCancel(ctx)
							defer cancel()
							if tc.cancel {
								leg.cancel = cancel
							}
							behaviorErr = behavior(ctx, deps)
							return behaviorErr
						},
					},
				})
				if err != nil {
					t.Fatalf("run: %v", err)
				}
				if !errors.Is(behaviorErr, tc.want) {
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
				if got := leg.SendCount(); got != wantSends {
					t.Errorf("send count: got %d, want %d", got, wantSends)
				}
			})
		}
	}
}
