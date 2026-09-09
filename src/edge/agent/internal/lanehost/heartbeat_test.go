package lanehost_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	connect "connectrpc.com/connect"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/lanehost"
)

// beaterFake answers or fails on demand, and blocks when a test wants an
// attempt that never returns.
type beaterFake struct {
	mu      sync.Mutex
	results []error
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
	return connect.NewResponse(&edgev1.HeartbeatResponse{}), nil
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
	err     error
}

func (l *laneSpy) Freeze(context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.freezes++
	return l.err
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

// TestTheLaneIsFrozenEvenWhenItsRecordsFail: Freeze returns the joined
// delivery errors with the gate already frozen, so a failed audit delivery
// must not be read as "not frozen". Reading it that way would retry the
// freeze every interval and re-emit records for a lane already stopped.
func TestTheLaneIsFrozenEvenWhenItsRecordsFail(t *testing.T) {
	lane := &laneSpy{err: errors.New("audit delivery unavailable")}
	client := &beaterFake{results: []error{
		errors.New("unreachable"), errors.New("unreachable"),
		errors.New("unreachable"), errors.New("unreachable"),
	}}

	runLoop(t, lanehost.HeartbeatConfig{
		Client: client, Lane: lane, AgentVersion: "test", Interval: time.Millisecond,
	}, 4)

	if freezes, _ := lane.counts(); freezes != 1 {
		t.Errorf("froze %d times, want exactly 1: a failed record does not mean the gate is open", freezes)
	}
}
