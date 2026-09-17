package recovery

import (
	"context"
	"time"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/access/v1"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/mutation"
)

// Fenced reports whether a device-native fence now proves a retry safe —
// the positive-fencing input. A nil Fenced is treated as always
// false: recovery falls through to the repeated-observation path alone.
type Fenced func(ctx context.Context) (bool, error)

// Outcome is what one [Runner.Attempt] call decided.
type Outcome int

const (
	// OutcomeContinueObserving means neither a fence nor two corroborating
	// observations authorized a retry yet, and the horizon has not
	// elapsed: the caller should observe again after its own poll
	// interval.
	OutcomeContinueObserving Outcome = iota + 1
	// OutcomeRetry means the caller may resubmit the command.
	OutcomeRetry
	// OutcomeAbandoned means the Machine has been transitioned to
	// ABANDONED; no further attempt should be made.
	OutcomeAbandoned
	// OutcomeVerified means this poll's observation shows the mutation
	// applied after all. The caller marks it verified and waits for
	// central's acknowledgement; no further attempt should be made.
	OutcomeVerified
)

// String returns the outcome's name for logging and diagnostics.
func (o Outcome) String() string {
	switch o {
	case OutcomeContinueObserving:
		return "continue_observing"
	case OutcomeRetry:
		return "retry"
	case OutcomeAbandoned:
		return "abandoned"
	case OutcomeVerified:
		return "verified"
	default:
		return "unspecified"
	}
}

const minCorroboratingObservations = 2

// Runner drives one mutation's recovery: it must be constructed fresh per
// mutation, since it tracks that mutation's corroboration state across
// calls. Not safe for concurrent use; a caller drives one Attempt at a
// time, per the rule that recovery for a device is part of its
// single ordered lane.
type Runner struct {
	machine *mutation.Machine
	fenced  Fenced
	horizon interfaces.DelayedEffect
	minGap  time.Duration
	clock   func() time.Time
	hold    *Hold

	corroborations    int
	lastCorroboration time.Time
}

// New constructs a Runner for machine, which must already be in phase
// RECOVERING (a caller transitions it there with
// [mutation.Machine.EnterRecovering] before constructing a Runner). fenced
// may be nil. minGap is the minimum spacing between two observations that
// both count as corroborating; zero derives it from the horizon, below.
// clock lets a test control elapsed time. hold is the
// device's own Hold, engaged when Attempt abandons — a caller must pass the
// same Hold [Lane.Submit] checks before admitting the device's next
// mutation, or an abandonment leaves no trace blocking further admission.
func New(machine *mutation.Machine, fenced Fenced, horizon interfaces.DelayedEffect, minGap time.Duration, clock func() time.Time, hold *Hold) *Runner {
	if minGap <= 0 {
		// Derived from the horizon rather than left at zero, because zero
		// makes the spacing check vacuous and a retry then needs only two
		// consecutive polls. The corroboration a retry rests on is that the
		// change is still absent across the window in which it could still
		// appear, and the horizon is that window: two polls a few seconds
		// apart on a device with a ten-minute horizon prove nothing except
		// that it has not applied yet, and resending the command is the one
		// thing that must not follow from it.
		minGap = horizon.Horizon / minCorroboratingObservations
	}
	return &Runner{machine: machine, fenced: fenced, horizon: horizon, minGap: minGap, clock: clock, hold: hold}
}

// Attempt observes the mutation's affected state — always, before any
// retry decision, and even when that observation fails, since a device
// that stays unreachable is exactly the case recovery exists for — and
// reports whether a retry is now authorized. preMutation is the complete
// observation of the device's state before the mutation was submitted, the
// baseline "unchanged" corroboration compares against. since is when the
// mutation was submitted, the horizon's start.
//
// Attempt owns every state change it decides. OutcomeAbandoned is returned
// with the Machine already ABANDONED, and OutcomeVerified with it already
// VERIFIED; neither is an instruction to the caller. Only OutcomeRetry
// leaves work for one, and that work is resending the command, which this
// package has no way to do.
//
// Verification is decided before the horizon, and the horizon before the
// fence. The first ordering is the one that matters: a poll whose
// observation shows the mutation applied has answered the question recovery
// was asking, and abandoning it because that poll happened to be the one
// that crossed the horizon would record a device as indeterminate while
// holding the evidence that it is not. The horizon bounds how long we go on
// not knowing, not what we do when we find out.
//
// The horizon is checked before the fence so that once it has elapsed,
// Attempt abandons regardless of a fence's answer: a Fenced implementation
// that stays true forever cannot keep authorizing retries past the horizon
// with no bound at all. A fence-authorized or corroboration-authorized
// retry resets the corroboration counter, so "two corroborating
// observations" authorizes exactly one retry, never an unbounded stream of
// them for the remaining horizon.
func (r *Runner) Attempt(ctx context.Context, since time.Time, preMutation *accessv1.InterfaceObservation) (Outcome, *accessv1.InterfaceObservation, error) {
	obs, observeErr := r.machine.Observe(ctx)
	now := r.clock()

	switch {
	case observeErr != nil:
		// A failed observation cannot corroborate anything; it is not
		// this call's own failure to report — an unreachable device is
		// the ordinary case this loop keeps polling through.
		r.corroborations = 0
	case observationsMatch(preMutation, obs):
		if r.corroborations == 0 || now.Sub(r.lastCorroboration) >= r.minGap {
			r.corroborations++
			r.lastCorroboration = now
		}
	default:
		r.corroborations = 0
	}

	if observeErr == nil {
		disposition, compareErr := r.machine.Compare(ctx, nil)
		if compareErr != nil {
			return 0, obs, compareErr
		}
		if disposition == accessv1.Disposition_DISPOSITION_VERIFIED {
			// Marked here, not by the caller, for the same reason
			// abandonment is: the decision and the state change are one
			// step. A caller told "this verified" and trusted to mark it
			// holds an obligation created somewhere else, and a caller
			// that returns, errors, or is canceled before discharging it
			// leaves the mutation resting at RECOVERING with nothing left
			// to move it and nothing anywhere that notices.
			//
			// A failed audit delivery inside MarkVerified surfaces as this
			// call's error, and the poll retries the same step on its next
			// tick — the same contract Abandon's own failure has.
			if err := r.machine.MarkVerified(ctx); err != nil {
				return 0, obs, err
			}
			return OutcomeVerified, obs, nil
		}
	}

	if !r.horizon.WithinHorizon(since, now) {
		abandonErr := r.machine.Abandon(ctx)
		// Engage on the machine's own durable phase, not on Abandon's
		// return value: Abandon can also fail before any state changes at
		// all (requireAnyPhase, e.g. the machine was already abandoned by
		// another path), in which case Phase() correctly is not ABANDONED
		// here and nothing is (re-)engaged unnecessarily. But when Abandon
		// fails only on its trailing LaneBlocked delivery, the phase and
		// block reason are already durably ABANDONED by then — engaging
		// here regardless keeps the hold in step with the state that
		// actually exists, rather than depending on a notification that
		// may never arrive.
		if r.hold != nil && r.machine.Phase() == accessv1.OperationPhase_OPERATION_PHASE_ABANDONED {
			r.hold.Engage(r.machine.Sequence())
		}
		if abandonErr != nil {
			return 0, obs, abandonErr
		}
		return OutcomeAbandoned, obs, nil
	}

	if r.fenced != nil {
		ok, err := r.fenced(ctx)
		if err != nil {
			return 0, obs, err
		}
		if ok {
			r.corroborations = 0
			return OutcomeRetry, obs, nil
		}
	}

	if r.corroborations >= minCorroboratingObservations {
		r.corroborations = 0
		return OutcomeRetry, obs, nil
	}

	return OutcomeContinueObserving, obs, nil
}

// observationsMatch reports whether a and b are both complete and agree on
// every compared field. Either being anything but COMPLETE, or the two
// disagreeing, means they cannot corroborate one another.
func observationsMatch(a, b *accessv1.InterfaceObservation) bool {
	// interfaces.ConflictingReads deliberately reports no conflict for two
	// observations naming different interfaces ("they have nothing to
	// conflict about") — the right answer for its own purpose, but wrong
	// here: two reads of different interfaces cannot corroborate one
	// another either, so this check must come first rather than reading
	// ConflictingReads' "no conflict" as "unchanged."
	if a.GetInterfaceName() != b.GetInterfaceName() {
		return false
	}
	if a.GetCompleteness() != accessv1.Completeness_COMPLETENESS_COMPLETE ||
		b.GetCompleteness() != accessv1.Completeness_COMPLETENESS_COMPLETE {
		return false
	}
	conflict, _ := interfaces.ConflictingReads(a, b)
	return !conflict
}
