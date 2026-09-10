package recovery_test

import (
	"context"
	"errors"
	"testing"
	"time"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	eventv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/device/v1"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/recovery"
)

// countingDeliverer counts the records it was handed, by kind.
type countingDeliverer struct {
	phaseTransitions []string
	other            int
}

func (d *countingDeliverer) Emit(_ context.Context, event *eventv1.DeviceOperationEvent) error {
	if transition := event.GetPhaseTransitioned(); transition != nil {
		d.phaseTransitions = append(d.phaseTransitions, transition.GetTo().String())
		return nil
	}
	d.other++
	return nil
}

// TestAVerifyingObservationOnTheCrossingPollVerifies is the ordering this
// slice exists for. The poll that crosses the horizon is also the poll
// whose observation shows the mutation applied; abandoning it would record
// a device as indeterminate while holding the evidence that it is not.
//
// The observation is the intent's own description, so Compare reports
// VERIFIED, and `since` is set far enough back that the horizon has
// certainly elapsed — so the only thing that can produce OutcomeVerified
// here is deciding verification before the horizon.
func TestAVerifyingObservationOnTheCrossingPollVerifies(t *testing.T) {
	m := recoveringMachine(t, observation("uplink to core"))
	horizon := interfaces.DelayedEffect{Horizon: time.Minute}
	runner := recovery.New(m, nil, horizon, 0, time.Now, nil)

	since := time.Now().Add(-time.Hour)
	outcome, obs, err := runner.Attempt(context.Background(), since, observation("old description"))
	if err != nil {
		t.Fatalf("Attempt() error: %v", err)
	}
	if outcome != recovery.OutcomeVerified {
		t.Fatalf("Attempt() outcome = %v, want %v: the horizon must not beat an observation that answers the question", outcome, recovery.OutcomeVerified)
	}
	if obs.GetDescription() != "uplink to core" {
		t.Errorf("observation description = %q, want the applied value", obs.GetDescription())
	}
	if got := m.Phase(); got == accessv1.OperationPhase_OPERATION_PHASE_ABANDONED {
		t.Error("the mutation was abandoned despite an observation proving it applied")
	}
}

// TestAnUnchangedObservationPastTheHorizonStillAbandons is the other half,
// and the reason the test above is evidence rather than a tautology: the
// same horizon, the same crossing poll, an observation that does not verify
// — and it abandons. Without this, "verification wins" could be satisfied
// by a Runner that had simply stopped checking the horizon at all.
func TestAnUnchangedObservationPastTheHorizonStillAbandons(t *testing.T) {
	m := recoveringMachine(t, observation("old description"))
	horizon := interfaces.DelayedEffect{Horizon: time.Minute}
	runner := recovery.New(m, nil, horizon, 0, time.Now, nil)

	since := time.Now().Add(-time.Hour)
	outcome, _, err := runner.Attempt(context.Background(), since, observation("old description"))
	if err != nil {
		t.Fatalf("Attempt() error: %v", err)
	}
	if outcome != recovery.OutcomeAbandoned {
		t.Fatalf("Attempt() outcome = %v, want %v", outcome, recovery.OutcomeAbandoned)
	}
}

// TestRecoveryPollsAreRecordFreeAndARetryIsNot pins what the durable audit
// stream holds for a recovery that runs for a while. Polls are silent; the
// one thing recorded is the retry, because resending a command to a device
// is a real event and the observations around it are not.
//
// Counting the records is the assertion, not "fewer than before": a stream
// whose readers are asking what happened to a device is unusable if a
// half-hour recovery buries RecoveryStarted and the terminal record under
// hundreds of transitions nobody asked about.
func TestRecoveryPollsAreRecordFreeAndARetryIsNot(t *testing.T) {
	deliverer := &countingDeliverer{}
	m := recoveringMachineWithDeliverer(t, deliverer, observation("old description"))

	// Everything up to here — Checkpoint's transition, EnterRecovering's
	// own transition and block — is setup, so start counting now.
	deliverer.phaseTransitions = nil
	deliverer.other = 0

	// An explicit minGap and a clock the test advances, because the derived
	// gap for an hour-long horizon is half an hour: two polls a microsecond
	// apart on the wall clock would leave the corroboration count at one
	// and never authorize the retry this test goes on to make.
	horizon := interfaces.DelayedEffect{Horizon: time.Hour}
	now := time.Now()
	clock := func() time.Time { return now }
	runner := recovery.New(m, nil, horizon, time.Second, clock, nil)
	since := now

	// Two polls a corroborating gap apart, both observing the unchanged
	// pre-mutation state, which corroborate and authorize one retry.
	for i := range 2 {
		outcome, _, err := runner.Attempt(context.Background(), since, observation("old description"))
		if err != nil {
			t.Fatalf("Attempt() %d error: %v", i, err)
		}
		if want := []recovery.Outcome{recovery.OutcomeContinueObserving, recovery.OutcomeRetry}[i]; outcome != want {
			t.Fatalf("Attempt() %d outcome = %v, want %v", i, outcome, want)
		}
		now = now.Add(2 * time.Second)
	}
	if len(deliverer.phaseTransitions) != 0 || deliverer.other != 0 {
		t.Fatalf("two polls emitted %v and %d other records, want none", deliverer.phaseTransitions, deliverer.other)
	}

	if err := m.Retry(context.Background()); err != nil {
		t.Fatalf("Retry() error: %v", err)
	}
	if len(deliverer.phaseTransitions) != 1 ||
		deliverer.phaseTransitions[0] != accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED.String() {
		t.Fatalf("Retry emitted %v, want exactly one transition to POSSIBLY_APPLIED", deliverer.phaseTransitions)
	}

	// And the poll after the retry is silent again, even though the phase
	// is now POSSIBLY_APPLIED rather than RECOVERING — which is where a
	// phase-based check would start recording again.
	deliverer.phaseTransitions = nil
	if _, _, err := runner.Attempt(context.Background(), since, observation("old description")); err != nil {
		t.Fatalf("Attempt() after retry error: %v", err)
	}
	if len(deliverer.phaseTransitions) != 0 {
		t.Fatalf("the poll after a retry emitted %v, want none", deliverer.phaseTransitions)
	}
}

// TestVerificationIsMarkedByAttemptNotLeftToTheCaller is the obligation
// test. Attempt returns OutcomeVerified with the machine already at
// VERIFIED, so there is no window between deciding and marking for a caller
// to be canceled in.
//
// The reversal to picture is a caller that returns between the two: with
// the mark left to the caller, the machine rests at RECOVERING with the
// poll gone and nothing anywhere that notices. Asserting the phase right
// after Attempt returns — with no caller action in between at all — is what
// makes that window's absence observable.
func TestVerificationIsMarkedByAttemptNotLeftToTheCaller(t *testing.T) {
	m := recoveringMachine(t, observation("uplink to core"))
	runner := recovery.New(m, nil, interfaces.DelayedEffect{Horizon: time.Hour}, 0, time.Now, nil)

	outcome, _, err := runner.Attempt(context.Background(), time.Now(), observation("old description"))
	if err != nil {
		t.Fatalf("Attempt() error: %v", err)
	}
	if outcome != recovery.OutcomeVerified {
		t.Fatalf("Attempt() outcome = %v, want %v", outcome, recovery.OutcomeVerified)
	}
	if got := m.Phase(); got != accessv1.OperationPhase_OPERATION_PHASE_VERIFIED {
		t.Fatalf("Phase() = %v immediately after Attempt returned, want VERIFIED: the mark must not be an obligation left to the caller", got)
	}
}

// TestAFailedVerificationRecordSurfacesAsAnAttemptError proves the failure
// path leaves the poll something to retry rather than a half-applied
// decision: MarkVerified's audit delivery fails, Attempt returns the error,
// and the machine has not moved. The next tick runs the same step.
func TestAFailedVerificationRecordSurfacesAsAnAttemptError(t *testing.T) {
	deliverer := &failVerifiedTransition{}
	m := recoveringMachineWithDeliverer(t, deliverer, observation("uplink to core"))
	runner := recovery.New(m, nil, interfaces.DelayedEffect{Horizon: time.Hour}, 0, time.Now, nil)

	deliverer.arm()
	outcome, _, err := runner.Attempt(context.Background(), time.Now(), observation("old description"))
	if err == nil {
		t.Fatalf("Attempt() error = nil (outcome %v), want the failed record surfaced", outcome)
	}
	if got := m.Phase(); got != accessv1.OperationPhase_OPERATION_PHASE_RECOVERING {
		t.Errorf("Phase() = %v, want the mutation left at RECOVERING for the next poll", got)
	}

	// The next tick, with the audit path working, completes the step.
	outcome, _, err = runner.Attempt(context.Background(), time.Now(), observation("old description"))
	if err != nil {
		t.Fatalf("the retried Attempt() error: %v", err)
	}
	if outcome != recovery.OutcomeVerified {
		t.Fatalf("the retried Attempt() outcome = %v, want %v", outcome, recovery.OutcomeVerified)
	}
}

// failVerifiedTransition fails exactly one PhaseTransitioned to VERIFIED,
// once armed.
type failVerifiedTransition struct{ armed bool }

func (d *failVerifiedTransition) arm() { d.armed = true }

func (d *failVerifiedTransition) Emit(_ context.Context, event *eventv1.DeviceOperationEvent) error {
	if d.armed && event.GetPhaseTransitioned().GetTo() == accessv1.OperationPhase_OPERATION_PHASE_VERIFIED {
		d.armed = false
		return errors.New("audit delivery unavailable")
	}
	return nil
}
