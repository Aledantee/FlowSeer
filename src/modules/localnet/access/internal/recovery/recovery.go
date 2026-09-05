package recovery

import (
	"context"
	"time"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/mutation"
)

// Fenced reports whether a device-native fence now proves a retry safe —
// decision 8's positive-fencing input. A nil Fenced is treated as always
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
	default:
		return "unspecified"
	}
}

const minCorroboratingObservations = 2

// Runner drives one mutation's recovery: it must be constructed fresh per
// mutation, since it tracks that mutation's corroboration state across
// calls. Not safe for concurrent use; a caller drives one Attempt at a
// time, per decision 3's rule that recovery for a device is part of its
// single ordered lane.
type Runner struct {
	machine *mutation.Machine
	fenced  Fenced
	horizon interfaces.DelayedEffect
	minGap  time.Duration
	clock   func() time.Time

	corroborations    int
	lastCorroboration time.Time
}

// New constructs a Runner for machine, which must already be in phase
// RECOVERING (a caller transitions it there with
// [mutation.Machine.EnterRecovering] before constructing a Runner). fenced
// may be nil. minGap is the minimum spacing decision 5's "repeated fresh
// observations" requires between two observations that both count as
// corroborating; clock lets a test control elapsed time.
func New(machine *mutation.Machine, fenced Fenced, horizon interfaces.DelayedEffect, minGap time.Duration, clock func() time.Time) *Runner {
	return &Runner{machine: machine, fenced: fenced, horizon: horizon, minGap: minGap, clock: clock}
}

// Attempt observes the mutation's affected state — always, before any
// retry decision — and reports whether a retry is now authorized. preMutation
// is the complete observation of the device's state before the mutation was
// submitted, the baseline "unchanged" corroboration compares against.
// since is when the mutation was submitted, the horizon's start.
func (r *Runner) Attempt(ctx context.Context, since time.Time, preMutation *accessv1.InterfaceObservation) (Outcome, *accessv1.InterfaceObservation, error) {
	obs, err := r.machine.Observe(ctx)
	if err != nil {
		return 0, nil, err
	}

	if r.fenced != nil {
		ok, err := r.fenced(ctx)
		if err != nil {
			return 0, obs, err
		}
		if ok {
			return OutcomeRetry, obs, nil
		}
	}

	now := r.clock()

	if observationsMatch(preMutation, obs) {
		if r.corroborations == 0 || now.Sub(r.lastCorroboration) >= r.minGap {
			r.corroborations++
			r.lastCorroboration = now
		}
	} else {
		r.corroborations = 0
	}

	if r.corroborations >= minCorroboratingObservations && r.horizon.WithinHorizon(since, now) {
		return OutcomeRetry, obs, nil
	}

	if !r.horizon.WithinHorizon(since, now) {
		if err := r.machine.Abandon(ctx); err != nil {
			return 0, obs, err
		}
		return OutcomeAbandoned, obs, nil
	}

	return OutcomeContinueObserving, obs, nil
}

// observationsMatch reports whether a and b are both complete and agree on
// every compared field. Either being anything but COMPLETE, or the two
// disagreeing, means they cannot corroborate one another.
func observationsMatch(a, b *accessv1.InterfaceObservation) bool {
	if a.GetCompleteness() != accessv1.Completeness_COMPLETENESS_COMPLETE ||
		b.GetCompleteness() != accessv1.Completeness_COMPLETENESS_COMPLETE {
		return false
	}
	conflict, _ := interfaces.ConflictingReads(a, b)
	return !conflict
}
