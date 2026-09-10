package lanehost_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/lanehost"
)

// beaterFake answers or fails on demand, and blocks when a test wants an
// attempt that never returns.
type beaterFake struct {
	mu      sync.Mutex
	results []error
	times   []time.Time
	calls   int
	block   chan struct{}
}

func (b *beaterFake) Heartbeat(
	ctx context.Context, _ *connect.Request[edgev1.HeartbeatRequest],
) (*connect.Response[edgev1.HeartbeatResponse], error) {
	b.mu.Lock()
	gate := b.block
	var result error
	if b.calls < len(b.results) {
		result = b.results[b.calls]
	}
	call := b.calls
	b.calls++
	b.mu.Unlock()

	if gate != nil {
		// A call that never answers on its own. It ends when the attempt's
		// own deadline does, which is the property under test.
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if result != nil {
		return nil, result
	}
	response := &edgev1.HeartbeatResponse{}
	if call < len(b.times) {
		response.SetServerTime(timestamppb.New(b.times[call]))
	}
	return connect.NewResponse(response), nil
}

func (b *beaterFake) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

// laneSpy records what the loop did to the lane. Freezing is the whole
// observable: a lane that was never frozen and a lane that was frozen and
// unfrozen both end up admitting work, and "nothing was refused" cannot tell
// them apart.
type laneSpy struct {
	mu      sync.Mutex
	freezes int
	thaws   int
	results []error
}

func (l *laneSpy) Freeze(context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	var result error
	if l.freezes < len(l.results) {
		result = l.results[l.freezes]
	}
	l.freezes++
	return result
}

func (l *laneSpy) Unfreeze(context.Context) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.thaws++
}

func (l *laneSpy) counts() (freezes, thaws int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.freezes, l.thaws
}

type freezeDeadlineSpy struct {
	observed time.Time
	deadline time.Time
	ok       bool
}

func (l *freezeDeadlineSpy) Freeze(ctx context.Context) error {
	l.observed = time.Now()
	l.deadline, l.ok = ctx.Deadline()
	return nil
}

func (*freezeDeadlineSpy) Unfreeze(context.Context) {}

// runLoop drives the loop for exactly n intervals and returns once it has
// stopped, so a test asserts against a finished run rather than polling.
func runLoop(t *testing.T, cfg lanehost.HeartbeatConfig, intervals int) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	served := 0
	cfg.Wait = func(ctx context.Context, _ time.Duration) bool {
		served++
		if served >= intervals {
			return false
		}
		return ctx.Err() == nil
	}
	done := make(chan error, 1)
	go func() { done <- lanehost.RunHeartbeat(ctx, cfg) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunHeartbeat: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the heartbeat loop never stopped")
	}
}

// TestTwoMissesFreezeAndTheNextSuccessUnfreezes is the mechanism's own test.
// It asserts the freeze happened and when, not that nothing else did: a lane
// that never froze and one that froze and thawed both admit work at the end.
func TestTwoMissesFreezeAndTheNextSuccessUnfreezes(t *testing.T) {
	lane := &laneSpy{}
	client := &beaterFake{results: []error{
		errors.New("unreachable"),
		errors.New("unreachable"),
		nil,
	}}

	runLoop(t, lanehost.HeartbeatConfig{
		Client:       client,
		Lane:         lane,
		AgentVersion: "test",
		Interval:     time.Millisecond,
	}, 3)

	freezes, thaws := lane.counts()
	if freezes != 1 {
		t.Errorf("froze %d times, want exactly 1", freezes)
	}
	if thaws != 1 {
		t.Errorf("unfroze %d times, want exactly 1", thaws)
	}
}

func TestAHeartbeatPublishesCentralServerTime(t *testing.T) {
	serverNow := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	var adopted time.Time

	runLoop(t, lanehost.HeartbeatConfig{
		Client: &beaterFake{times: []time.Time{serverNow}},
		Lane:   &laneSpy{},
		AdoptServerTime: func(_ context.Context, got time.Time) {
			adopted = got
		},
		Interval: time.Millisecond,
	}, 1)
	if !adopted.Equal(serverNow) {
		t.Errorf("adopted server time = %v, want %v", adopted, serverNow)
	}
}

// TestOneMissDoesNotFreeze is the partner that makes the test above evidence.
// Without it, "froze once after two misses" is also satisfied by a loop that
// freezes on the first — and a single failed heartbeat against a central
// that is merely restarting would stop every device this edge serves.
func TestOneMissDoesNotFreeze(t *testing.T) {
	lane := &laneSpy{}
	client := &beaterFake{results: []error{errors.New("unreachable"), nil, nil}}

	runLoop(t, lanehost.HeartbeatConfig{
		Client: client, Lane: lane, AgentVersion: "test", Interval: time.Millisecond,
	}, 3)

	if freezes, _ := lane.counts(); freezes != 0 {
		t.Errorf("froze %d times after a single miss, want 0", freezes)
	}
}

// TestMissesAreConsecutiveNotCumulative pins which counter this is. Failures
// separated by a success must not add up: an edge on a flaky link that
// succeeds every other attempt is in contact, and a cumulative count would
// freeze it permanently after two bad minutes in a day.
func TestMissesAreConsecutiveNotCumulative(t *testing.T) {
	lane := &laneSpy{}
	client := &beaterFake{results: []error{
		errors.New("unreachable"), nil, errors.New("unreachable"), nil,
	}}

	runLoop(t, lanehost.HeartbeatConfig{
		Client: client, Lane: lane, AgentVersion: "test", Interval: time.Millisecond,
	}, 4)

	if freezes, _ := lane.counts(); freezes != 0 {
		t.Errorf("froze %d times on alternating results, want 0", freezes)
	}
}

// TestAHungCallCountsAsAMiss is why each attempt has its own deadline. A call
// that never answers is contact lost by any useful definition, but it is not
// a failure — it never returns to be counted — so a loop without a deadline
// stops counting exactly when it matters, and the lane stays unfrozen because
// nothing ever told it otherwise.
func TestAHungCallCountsAsAMiss(t *testing.T) {
	lane := &laneSpy{}
	client := &beaterFake{block: make(chan struct{})}
	defer close(client.block)

	runLoop(t, lanehost.HeartbeatConfig{
		Client:       client,
		Lane:         lane,
		AgentVersion: "test",
		// Short enough that two attempts time out inside the test, which is
		// what makes them misses rather than a hang.
		Interval: 20 * time.Millisecond,
	}, 2)

	if freezes, _ := lane.counts(); freezes != 1 {
		t.Errorf("froze %d times after two hung calls, want 1: an attempt that never returns is a miss", freezes)
	}
	if client.count() < 2 {
		t.Errorf("made %d attempts, want at least 2: each must end on its own deadline", client.count())
	}
}

func TestAFailedFreezeIsRetriedUntilItsRecordsLand(t *testing.T) {
	lane := &laneSpy{results: []error{errors.New("audit delivery unavailable"), nil}}
	client := &beaterFake{results: []error{
		errors.New("unreachable"), errors.New("unreachable"),
		errors.New("unreachable"),
	}}

	runLoop(t, lanehost.HeartbeatConfig{
		Client: client, Lane: lane, AgentVersion: "test", Interval: time.Millisecond,
	}, 3)

	if freezes, _ := lane.counts(); freezes != 2 {
		t.Errorf("froze %d times, want 2: the failed record must be retried before the loop marks the lane frozen", freezes)
	}
}

func TestFreezeIsBoundedByTheHeartbeatInterval(t *testing.T) {
	lane := &freezeDeadlineSpy{}
	interval := 20 * time.Millisecond
	runLoop(t, lanehost.HeartbeatConfig{
		Client:             &beaterFake{results: []error{errors.New("unreachable")}},
		Lane:               lane,
		Interval:           interval,
		MissesBeforeFreeze: 1,
	}, 1)

	if !lane.ok {
		t.Fatal("Freeze context had no deadline")
	}
	if remaining := lane.deadline.Sub(lane.observed); remaining <= 0 || remaining > interval {
		t.Errorf("Freeze deadline is %v away, want a positive duration no longer than %v", remaining, interval)
	}
}

// TestALaneIsRequiredBeforeTheLoopStarts covers the collaborator that is only
// reached two missed heartbeats into a contact outage. Left to fail there, a
// missing lane is a panic in this goroutine at the least recoverable moment
// the agent has; refused at construction it is a startup error naming what is
// absent.
func TestALaneIsRequiredBeforeTheLoopStarts(t *testing.T) {
	err := lanehost.RunHeartbeat(context.Background(), lanehost.HeartbeatConfig{
		Client:       &beaterFake{},
		AgentVersion: "test",
		Interval:     time.Millisecond,
	})
	if err == nil {
		t.Fatal("RunHeartbeat accepted a configuration with no lane; the first freeze would panic")
	}
}

// testRefusalCode stands for a central that answers and refuses this agent,
// as against one that cannot be reached. Registered once: errs codes are
// process-global and a repeat registration panics.
var testRefusalCode = errs.NewCode("lanehost-test/refused")

// TestTheFreezeRecordNamesWhyContactWasLost keeps the two causes apart. A
// central that refuses this agent and a central that cannot be reached both
// freeze the lane here and have opposite remedies; a record carrying only the
// miss count tells an operator neither.
func TestTheFreezeRecordNamesWhyContactWasLost(t *testing.T) {
	logs := &recordingLogs{}
	lane := &laneSpy{}
	client := &beaterFake{results: []error{
		errs.New().Code(testRefusalCode).Msg("assertion outside the window"),
		errs.New().Code(testRefusalCode).Msg("assertion outside the window"),
		nil,
	}}

	runLoop(t, lanehost.HeartbeatConfig{
		Client:       client,
		Lane:         lane,
		AgentVersion: "test",
		Interval:     time.Millisecond,
		Logger:       slog.New(logs),
	}, 3)

	attrs, ok := logs.event("flowseer.edge.contact.lost")
	if !ok {
		t.Fatal("no flowseer.edge.contact.lost record was emitted")
	}
	if got := attrs["error.type"]; got != string(testRefusalCode) {
		t.Errorf("error.type = %q, want %q", got, string(testRefusalCode))
	}
}
