package access_test

import (
	"context"
	"testing"
	"time"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	integrationv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/localnet/access"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/lane"
)

// heldAgainst reports whether the device's lane refuses a further mutation,
// which is what an engaged recovery hold looks like from outside.
//
// Asked by submitting rather than by reading a flag: the hold has no
// accessor, and what matters about it is exactly this — whether the next
// mutation is admitted or refused.
func heldAgainst(t *testing.T, l *access.Lane, sequence uint64) (bool, error) {
	t.Helper()
	// Bounded, because a lane that is *not* held admits this mutation and
	// then waits for a checkpoint nobody sends. The deadline is what makes
	// the unheld case answerable instead of hanging.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := l.Submit(ctx, access.SubmitOptions{
		DeviceKey: "dev-1", Request: mutationRequest(sequence), Priority: lane.PriorityNormal,
	})
	code, _ := errs.CodeOf(err)
	return code == access.ErrCodeDesynchronized, err
}

// A mutation that provably sent nothing leaves the device admitting the next
// one.
//
// The hold exists for one thing: a mutation whose effect nobody can
// establish. A checkpoint wait that reaches the submitter's deadline
// establishes the opposite — the command was never sent, `submitted` is
// false, and central disposes it REJECTED on exactly that flag. Holding the
// device there takes it out of service for a timeout, and nothing brings it
// back: endMutation resolves a hold only for a mutation that verified, this
// one cannot, and the terminal acknowledgement that would carry an operator's
// decision is refused with no-pending-wait once the mutation has closed.
//
// The lane already states the rule in the neighboring branch. A mutation
// blocked on a firmware-epoch change is the same case — the command provably
// never left — and epochBlocked says "there is nothing to recover and nothing
// to hold" and engages none. Two branches of one function disagreed about the
// same fact.
func TestAMutationThatSentNothingDoesNotHoldTheDevice(t *testing.T) {
	l := laneWithDeliverer(t, noopDeliverer{})
	addDevice(t, l)

	// No checkpoint is delivered, so the wait ends at the submitter's own
	// deadline with the command unsent — the transient failure this is
	// about.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := l.Submit(ctx, access.SubmitOptions{
		DeviceKey: "dev-1", Request: mutationRequest(1), Priority: lane.PriorityNormal,
	}); err == nil {
		t.Fatal("Submit() succeeded; this test needs the checkpoint wait to fail")
	}

	if held, err := heldAgainst(t, l, 2); held {
		t.Errorf("the device's lane is held after a mutation that provably sent nothing (%v); a transient timeout took it out of service", err)
	}
}

// An abandonment whose lane-blocked record central will not take still holds
// the device.
//
// Machine.Abandon writes the phase, sets the disposition and closes done
// before it delivers the record, so by the time the delivery fails the
// mutation is durably abandoned — its effect unknown, forever, since nothing
// will look at it again. The lane engaged the hold on Acknowledge's return
// value instead of on that durable state, so the one abandonment that fails
// to deliver is the one that leaves the device open, and the next mutation is
// admitted over a device whose last one may or may not have applied.
//
// It is permanent rather than transient: central re-sending the same
// acknowledgement gets already-terminal, which returns at the same line and
// never reaches the engage.
//
// THIS TEST CURRENTLY FAILS, roughly one run in three here and five in six on
// another machine, and the failure is a second defect rather than a flaky
// assertion. Its diagnostic names it: the abandoned mutation ends with "enter
// recovery: EnterRecovering is not valid from OPERATION_PHASE_VERIFIED", so it
// had reached VERIFIED when the acknowledgement arrived. Machine.Abandon emits
// its PhaseTransitioned record *before* taking the lock to write the phase and
// the disposition, and in that window the door's `abandoning` mark is set while
// Phase() still reads VERIFIED. A submitting goroutine that ends the mutation
// inside it reaches endMutation, sees Verified() true, and calls
// hold.Resolve() — clearing the hold the acknowledging goroutine engages.
// endMutation's own comment argues this is safe "by the time this runs,
// Acknowledge has already moved the phase to ABANDONED", which is exactly the
// ordering that is not guaranteed.
//
// The fix is to consult the mark rather than the phase: `abandoning` is set
// under the same lock that guards the phase and before any delivery, so
// endMutation should resolve only when Verified() and not abandoning, and the
// engage should fire on the mark as well as on the disposition. Left unapplied
// deliberately — it is a fix nobody has watched work yet.
//
// The rule this asserts is the one recovery.Runner.Attempt already states for
// itself — a state change belongs to the machine's own durable phase, not to
// whether the call announcing it returned an error.
func TestAnAbandonmentHoldsTheDeviceEvenWhenItsRecordIsRefused(t *testing.T) {
	deliverer := &refusingDeliverer{refuse: isLaneBlocked}
	l := laneWithDeliverer(t, deliverer)
	addDevice(t, l)

	req := mutationRequest(1)
	done := make(chan struct{})
	var firstResult *integrationv1.ExecuteResult
	var firstErr error
	go func() {
		defer close(done)
		firstResult, firstErr = l.Submit(context.Background(), access.SubmitOptions{
			DeviceKey: "dev-1", Request: req, Priority: lane.PriorityNormal,
		})
	}()
	deliverCheckpoint(t, l, req.GetSequence())

	err := deliverAck(t, l, terminalAck(req.GetSequence(), accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED))
	if err == nil {
		t.Fatal("the acknowledgement succeeded; this test needs its lane-blocked record refused")
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Submit() never returned after the abandonment")
	}

	if held, submitErr := heldAgainst(t, l, 2); !held {
		t.Errorf("the device admitted another mutation after an abandonment whose record was refused; "+
			"the previous mutation's effect is unknown and nothing is holding the lane\n"+
			"  acknowledgement error: %v\n  the next mutation was answered with: %v\n"+
			"  the abandoned mutation ended phase=%v err=%v",
			err, submitErr, firstResult.GetPhaseReached(), firstErr)
	}
}

// laneWithDeliverer is newTestLane with an audit deliverer a test controls.
func laneWithDeliverer(t *testing.T, deliverer auditDeliverer) *access.Lane {
	t.Helper()
	return laneWithReporter(t, &recordingReporter{}, deliverer)
}
