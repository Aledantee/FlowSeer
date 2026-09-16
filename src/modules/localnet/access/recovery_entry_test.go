package access_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	eventv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/device/v1"
	integrationv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1"
	"go.aledante.io/FlowSeer/src/modules/localnet/access"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/lane"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/telemetry"
)

// refusingDeliverer stands in for a central that has gone away: it refuses
// the records a test names, remembers their ids, and keeps every record it
// took so a test can ask what the account ended up holding.
type refusingDeliverer struct {
	mu       sync.Mutex
	refuse   func(*eventv1.DeviceOperationEvent) bool
	refused  []string
	accepted []*eventv1.DeviceOperationEvent
}

func (d *refusingDeliverer) Emit(_ context.Context, event *eventv1.DeviceOperationEvent) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.refuse != nil && d.refuse(event) {
		d.refused = append(d.refused, event.GetEventId())
		return errors.New("central did not take the audit record")
	}
	d.accepted = append(d.accepted, event)
	return nil
}

func (d *refusingDeliverer) refusedIDs() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.refused...)
}

// acceptedOfKind is the ids of the records the deliverer took that satisfy
// match, in the order it took them.
func (d *refusingDeliverer) acceptedOfKind(match func(*eventv1.DeviceOperationEvent) bool) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var ids []string
	for _, e := range d.accepted {
		if match(e) {
			ids = append(ids, e.GetEventId())
		}
	}
	return ids
}

// safeBuilder is a strings.Builder a test goroutine can read while the lane's
// own goroutines are writing to it.
type safeBuilder struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *safeBuilder) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuilder) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// laneWithDelivererAndLog is a lane whose telemetry log is captured, so a test
// can assert on an event the lane emits rather than on state it happens to
// leave behind.
func laneWithDelivererAndLog(t *testing.T, deliverer auditDeliverer, log *safeBuilder) *access.Lane {
	t.Helper()
	view, err := telemetry.NewView(telemetry.ViewConfig{
		Logger: slog.New(slog.NewTextHandler(log, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	if err != nil {
		t.Fatalf("NewView() error: %v", err)
	}
	return access.NewLane(access.Config{
		QueueCapacity:    4,
		Audit:            deliverer,
		Telemetry:        view,
		Clock:            time.Now,
		OperationTimeout: 2 * time.Second,
	})
}

// deviceWhoseChangeNeverShows registers a device whose read never matches what
// a mutation asked for, so the mutation cannot verify and must enter recovery.
//
// Its reads are counted, because a recovery poll observes and observing reads:
// the count is what distinguishes a poll that is running from one that was
// never started. The horizon is short so the derived poll interval lands on
// its two-second floor and a poll that did start reads within a few seconds.
func deviceWhoseChangeNeverShows(t *testing.T, l *access.Lane, reads *atomic.Int64) {
	t.Helper()
	if err := l.AddDevice(context.Background(), "dev-1", access.DeviceSession{
		DelayedEffect: interfaces.DelayedEffect{Horizon: 12 * time.Second},
		OpenSNMP:      probeFactory(),
		ReadOverride: func(context.Context, string) (*accessv1.InterfaceObservation, error) {
			reads.Add(1)
			return completeObservation("something else entirely"), nil
		},
		SubmitOverride: func(context.Context, *accessv1.InterfaceDescriptionChange) error { return nil },
	}); err != nil {
		t.Fatalf("AddDevice() error: %v", err)
	}
}

func isLaneBlocked(e *eventv1.DeviceOperationEvent) bool { return e.HasLaneBlocked() }

// A mutation whose lane-blocked record central will not take still gets a
// recovery poll, and the record is delivered once the stream takes it.
//
// The failure this rules out stranded a mutation permanently. Entering
// recovery moves the phase, writes the block, and then delivers a record; when
// that delivery failed the whole call returned an error and the poll was never
// started — so the mutation rested POSSIBLY_APPLIED and INDETERMINATE on a
// device that may have been written to, with nothing scheduled ever to look at
// it again and only an operator able to end it.
//
// Both halves were deliberate. The block is written before its record
// precisely so the block survives a failed delivery, and the caller is failed
// because Submit cannot return an outcome it does not have. Their combination
// was that a transient audit outage cost the mutation its recovery.
//
// The poll now follows the state rather than the delivery, and the record
// stays owed. Two things are asserted because they are the two halves of that:
// the device is read again, and the account is not short a record.
//
// The redelivered record must carry the id the refused one carried. Every
// constructor mints a fresh event_id and the stream deduplicates on it, so a
// retry that rebuilds the record is a second record rather than a duplicate —
// and an account with duplicates is as wrong as one with holes while looking
// healthier.
func TestARefusedRecordCostsTheMutationNeitherItsPollNorItsAccount(t *testing.T) {
	var refuseBlocked atomic.Bool
	refuseBlocked.Store(true)
	deliverer := &refusingDeliverer{
		refuse: func(e *eventv1.DeviceOperationEvent) bool { return isLaneBlocked(e) && refuseBlocked.Load() },
	}

	log := &safeBuilder{}
	l := laneWithDelivererAndLog(t, deliverer, log)
	var reads atomic.Int64
	deviceWhoseChangeNeverShows(t, l, &reads)

	// Driven on its own goroutine: this mutation cannot verify, so Submit
	// does not return until recovery has run its course, and everything this
	// test asserts happens while it is still in flight.
	go func() { _, _ = runMutation(t, l, mutationRequest(1)) }()

	waitFor(t, 30*time.Second, "a lane-blocked record to be refused",
		func() bool { return len(deliverer.refusedIDs()) > 0 })
	refused := deliverer.refusedIDs()

	// The poll runs. The first attempt is an interval away, so this is
	// waited for rather than sampled once.
	before := reads.Load()
	waitFor(t, 30*time.Second, "the device to be read again by a recovery poll",
		func() bool { return reads.Load() > before })

	// And the account catches up once the stream will take the record.
	refuseBlocked.Store(false)
	waitFor(t, 30*time.Second, "the refused record to be redelivered",
		func() bool { return len(deliverer.acceptedOfKind(isLaneBlocked)) > 0 })

	accepted := deliverer.acceptedOfKind(isLaneBlocked)
	if len(accepted) != 1 {
		t.Errorf("the stream took %d lane-blocked records for one block (%v); a retry that rebuilds the record is a second record",
			len(accepted), accepted)
	}
	if accepted[0] != refused[0] {
		t.Errorf("the redelivered record carries id %q, want the refused record's %q — the stream deduplicates on that id",
			accepted[0], refused[0])
	}
}

// A poll that ends still holding a record says so.
//
// The hole is not avoidable here: the records live in memory, the poll is the
// only thing retrying them, and when its budget ends nothing is left holding
// the obligation. What is avoidable is the hole being silent. An account with
// a gap and an account of a device nothing happened to read identically, and
// the mutation this befalls is exactly the one an operator will later have to
// resolve.
//
// Asserted on the emitted event, because that is the operator's only view of
// it.
func TestAPollThatEndsStillOwingRecordsReportsTheGap(t *testing.T) {
	deliverer := &refusingDeliverer{refuse: isLaneBlocked}

	log := &safeBuilder{}
	l := laneWithDelivererAndLog(t, deliverer, log)
	var reads atomic.Int64
	deviceWhoseChangeNeverShows(t, l, &reads)

	// The checkpoint is answered but the terminal acknowledgement is not, so
	// nothing ends this mutation early and recovery runs to the end of its
	// budget — a twelve-second horizon and a two-second interval. Using the
	// shared helper would acknowledge it VERIFIED, which ends the mutation
	// through a different door — a door that would let this test pass while
	// asserting nothing about the recovery budget at all.
	req := mutationRequest(1)
	go func() {
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			checkpoint := &integrationv1.CheckpointRequest{}
			checkpoint.SetSequence(req.GetSequence())
			if err := l.HandleCheckpoint("dev-1", checkpoint); err == nil {
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	// The outcome is not asserted, and that is deliberate. Recovery running
	// out of budget abandons the mutation, and an abandoned mutation ends
	// with a result rather than an error — so "Submit failed" is not the
	// property here and asserting it cost this test a run. What matters is
	// that the mutation ended at all, whichever way, while a record was
	// still owed.
	if _, err := l.Submit(context.Background(), access.SubmitOptions{
		DeviceKey: "dev-1", Request: req, Priority: lane.PriorityNormal,
	}); err != nil {
		t.Logf("Submit() ended with %v", err)
	}

	waitFor(t, 60*time.Second, "the undelivered record to be reported as a gap",
		func() bool { return strings.Contains(log.String(), "flowseer.device.audit.gap") })

	if refused := deliverer.refusedIDs(); len(refused) > 0 && !strings.Contains(log.String(), refused[0]) {
		t.Errorf("the gap was reported without naming the missing record %q; the id is what makes it findable", refused[0])
	}
}

// waitFor polls until done reports true, and fails naming what it was waiting
// for rather than that a boolean stayed false.
func waitFor(t *testing.T, budget time.Duration, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(budget)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("waited %v for %s and it never happened", budget, what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// isRecoveringTransition matches the PhaseTransitioned record EnterRecovering
// delivers as its first step, before the block and the RecoveryStarted.
func isRecoveringTransition(e *eventv1.DeviceOperationEvent) bool {
	return e.GetPhaseTransitioned().GetTo() == accessv1.OperationPhase_OPERATION_PHASE_RECOVERING
}

// A mutation whose recovery transition central will not take still ends up
// with something scheduled to look at it.
//
// This is the half TestARefusedRecordCostsTheMutationNeitherItsPollNorItsAccount
// does not reach. That test refuses the lane-blocked record, which
// EnterRecovering delivers after the phase has already moved. The phase
// transition itself is delivered first, and it used to be strict: a refused
// transition left the phase where it was, so the mutation was not in recovery
// and enterRecovery returned without starting a poll. Nothing came back to it
// afterwards, and a refusal that lasted a moment cost the mutation its
// recovery permanently — it rested INDETERMINATE on a device that may have
// been written to, and only an operator could end it.
//
// The transition now retains its record and moves the phase, on the same
// terms as the two records after it. So the two waits below are the two
// halves of that: a poll runs despite the refusal, and the account is not
// short the record once the stream takes it again.
func TestARefusedRecoveryTransitionDoesNotStrandTheMutation(t *testing.T) {
	var refuseTransition atomic.Bool
	refuseTransition.Store(true)
	deliverer := &refusingDeliverer{
		refuse: func(e *eventv1.DeviceOperationEvent) bool {
			return isRecoveringTransition(e) && refuseTransition.Load()
		},
	}

	log := &safeBuilder{}
	l := laneWithDelivererAndLog(t, deliverer, log)
	var reads atomic.Int64
	deviceWhoseChangeNeverShows(t, l, &reads)

	go func() { _, _ = runMutation(t, l, mutationRequest(1)) }()

	waitFor(t, 30*time.Second, "the recovery transition to be refused",
		func() bool { return len(deliverer.refusedIDs()) > 0 })
	refused := deliverer.refusedIDs()

	// The poll is what proves the mutation entered recovery: only this
	// device's reads move the count, and the mutation's own observation is
	// already behind it, so the next read can only be a poll's.
	before := reads.Load()
	waitFor(t, 60*time.Second, "the device to be read again by a recovery poll",
		func() bool { return reads.Load() > before })

	// And the account catches up once the stream will take the record.
	refuseTransition.Store(false)
	waitFor(t, 60*time.Second, "the refused transition to be redelivered",
		func() bool { return len(deliverer.acceptedOfKind(isRecoveringTransition)) > 0 })

	accepted := deliverer.acceptedOfKind(isRecoveringTransition)
	if len(accepted) != 1 {
		t.Errorf("the stream took %d recovery transitions for one move (%v); a retry that rebuilds the record is a second record",
			len(accepted), accepted)
	}
	if accepted[0] != refused[0] {
		t.Errorf("the redelivered record carries id %q, want the refused record's %q — the stream deduplicates on that id",
			accepted[0], refused[0])
	}
}
