package access_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	eventv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/device/v1"
	"go.aledante.io/FlowSeer/src/modules/localnet/access"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
)

// blockingRecordDeliverer takes every audit record except the one naming a
// kind it is told to refuse, which it fails the way a central that has gone
// away fails it.
type blockingRecordDeliverer struct {
	mu      sync.Mutex
	refuse  func(*eventv1.DeviceOperationEvent) bool
	refused int
}

func (d *blockingRecordDeliverer) Emit(_ context.Context, event *eventv1.DeviceOperationEvent) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.refuse != nil && d.refuse(event) {
		d.refused++
		return errors.New("central did not take the audit record")
	}
	return nil
}

func (d *blockingRecordDeliverer) refusals() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.refused
}

// An audit record central will not take, delivered while a mutation is
// entering recovery, leaves that mutation with no recovery poll at all.
//
// This is the mechanism behind a mutation that rests at POSSIBLY_APPLIED and
// INDETERMINATE forever with nothing ever looking at the device again. It is
// not a deadlock and nothing is blocked: enterRecovery returns early when
// EnterRecovering fails, and its own comment says why — "no poll will run and
// nothing else will answer this caller". The comment is right about the
// caller. What neither it nor anything else accounts for is the mutation,
// which is left blocked by a write that block() performs before its audit
// attempt precisely so the block survives a failed delivery.
//
// So the two halves are individually deliberate and their combination is that
// a transient audit outage strands a mutation permanently. The device may
// have been written to; nothing will ever look to find out; and only an
// operator can end it.
//
// The lane's own audit deliverer blocks until central answers and central
// answers only once the stream has acked, which is what makes a central
// restart able to produce this. The refusal here stands in for that: what
// matters is that the delivery does not succeed, not how it fails.
//
// One weakness, stated rather than left to be discovered. Accepting the
// record instead does not exercise the read count: the mutation then resolves
// and Submit succeeds, so the reversal fails at the assertion above this one.
// The contrast is real — record taken, the mutation ends; record refused, it
// strands and the device is never read again — but the read count itself is
// only ever asserted in the failing direction, and a reversal that reached it
// would need a submit path that does not acknowledge. The load-bearing half
// is the read count; the half with no reversal behind it is the claim that a
// passing delivery would have kept the poll alive.
func TestAnAuditOutageWhileEnteringRecoveryLeavesNoPoll(t *testing.T) {
	reporter := &recordingReporter{}
	deliverer := &blockingRecordDeliverer{
		refuse: func(e *eventv1.DeviceOperationEvent) bool { return e.HasLaneBlocked() },
	}
	l := laneWithReporter(t, reporter, deliverer)

	// A device whose read never matches what the mutation asked for, so the
	// mutation cannot verify and must enter recovery — the ordinary route to
	// the state this test is about. Its reads are counted, because a recovery
	// poll observes and observing reads: the count is the only thing that
	// distinguishes a poll that is running from one that was never started.
	//
	// The horizon is short so the derived poll interval lands on its
	// two-second floor and a poll that did start would read within the wait
	// below. A minute-long horizon would give a ten-second interval and the
	// wait would have to be longer than the failure deserves.
	var reads atomic.Int64
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

	// Submit fails, and that is not the defect — a mutation whose effect
	// cannot be established is supposed to fail its caller. The defect is
	// what is left behind.
	if _, err := runMutation(t, l, mutationRequest(1)); err == nil {
		t.Fatal("Submit() succeeded; with the lane-blocked record refused, entering recovery cannot have worked")
	}
	if deliverer.refusals() == 0 {
		t.Fatal("no lane-blocked record was refused; the mutation did not take the path this test is about")
	}

	// The device is never read again. A recovery poll observes on every
	// attempt, at a two-second interval here, so several would have run
	// inside this wait if one had been started at all.
	before := reads.Load()
	time.Sleep(7 * time.Second)
	if got := reads.Load(); got != before {
		t.Errorf("the device was read %d more times after the mutation failed; a recovery poll is running after all",
			got-before)
	}
}
