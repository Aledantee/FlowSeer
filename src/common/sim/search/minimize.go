package search

import (
	"reflect"
	"strings"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
)

// Minimality indicates whether scenario reduction reached a minimal fixed point
// or halted due to an evaluation limit. Values are safe for concurrent reads.
type Minimality string

const (
	// Minimal indicates that no further scenario elements could be removed without
	// altering or losing the target location and exact difference.
	Minimal Minimality = "Minimal"

	// LimitReached indicates that reduction halted because the maximum number of
	// reduction attempts was reached before achieving a fixed point.
	LimitReached Minimality = "LimitReached"
)

// DefaultMinimizationLimit is the default maximum number of reduction trials
// evaluated during counterexample minimization.
const DefaultMinimizationLimit = 64

// Minimization retains the original target and exact displayed difference, even
// when reduction reaches its trial limit. Candidate owns independent action data.
// Treat its pointers and slices as immutable during concurrent reads.
type Minimization struct {
	Candidate  Candidate
	Minimality Minimality
	Target     fabric.DifferenceLocation
	Difference analysis.Difference
}

// Minimize removes injections, then faults, while retaining the initial location
// and all Difference values. It does not consume either fabric or the input.
func Minimize(current, candidate *fabric.Fabric, cand Candidate, budget int) (Minimization, error) {
	return MinimizeWithLimit(current, candidate, cand, budget, DefaultMinimizationLimit)
}

// MinimizeWithLimit returns an error for invalid inputs, comparison errors, or an
// initial non-difference. Zero maxTrials uses the default. Errors never report Minimal.
func MinimizeWithLimit(current, candidate *fabric.Fabric, cand Candidate, budget int, maxTrials int) (Minimization, error) {
	if budget <= 0 {
		return Minimization{}, errs.Msg("minimization budget must be positive")
	}
	if maxTrials < 0 {
		return Minimization{}, errs.Msg("minimization trial limit must be nonnegative")
	}
	if maxTrials == 0 {
		maxTrials = DefaultMinimizationLimit
	}
	scenario, err := cand.ToScenario().Normalize()
	if err != nil {
		return Minimization{}, errs.Wrap(err, "minimization scenario")
	}
	cand.Actions = scenario.Actions
	if err := cand.Validate(); err != nil {
		return Minimization{}, errs.Wrap(err, "minimization candidate")
	}

	initialCmp := fabric.Compare(current, candidate, scenario, fabric.RunOptions{Budget: budget, Window: 0})
	if initialCmp.Err != nil {
		return Minimization{}, errs.Wrap(initialCmp.Err, "initial minimization comparison")
	}
	if initialCmp.Disposition != analysis.Different {
		return Minimization{}, errs.Msgf("initial minimization comparison is %s, want Different", initialCmp.Disposition)
	}
	result := Minimization{Target: initialCmp.Location, Difference: initialCmp.Difference}
	reduced := cand.Clone()
	trials := 0
	for {
		changed := false
		for _, kind := range []fabric.ActionKind{fabric.ActionInject, fabric.ActionFault} {
			idx := 0
			for idx < len(reduced.Actions) {
				if reduced.Actions[idx].Kind != kind {
					idx++
					continue
				}
				if trials >= maxTrials {
					reduced.Tuple = computeCandidateTuple(reduced)
					result.Candidate = reduced
					result.Minimality = LimitReached
					return result, nil
				}
				trial := copyCandidateWithoutActionIndex(reduced, idx)
				trials++
				comparison := runCompare(current, candidate, trial, budget)
				if comparison.Err != nil {
					reduced.Tuple = computeCandidateTuple(reduced)
					result.Candidate = reduced
					return result, errs.Wrap(comparison.Err, "minimization trial comparison")
				}
				if comparison.Disposition == analysis.Different && comparison.Difference == result.Difference && reflect.DeepEqual(comparison.Location, result.Target) {
					reduced = trial
					changed = true
				} else {
					idx++
				}
			}
		}
		if !changed {
			break
		}
	}
	reduced.Tuple = computeCandidateTuple(reduced)
	result.Candidate = reduced
	result.Minimality = Minimal
	return result, nil
}

func computeCandidateTuple(c Candidate) Tuple {
	parts := make([]string, 0, len(c.Actions))
	for _, kind := range []fabric.ActionKind{fabric.ActionInject, fabric.ActionFault} {
		for _, action := range c.Actions {
			if action.Kind != kind {
				continue
			}
			if kind == fabric.ActionFault {
				parts = append(parts, string(formatFaultTuple(*action.Fault, action.At)))
				continue
			}
			inj := action.Inject
			var vid vlan.ID
			if len(inj.Frame.Tags) > 0 {
				vid = inj.Frame.Tags[0].VID
			}
			etype := inj.Frame.EtherType
			if etype == 0 {
				etype = ethernet.EtherTypeIPv4
			}
			parts = append(parts, string(formatL2Tuple(inj.Origin, inj.Frame.Dst, vid, etype, inj.Frame.Payload)))
		}
	}
	return Tuple(strings.Join(parts, "; "))
}

func copyCandidateWithoutActionIndex(c Candidate, removeIdx int) Candidate {
	cp := c.Clone()
	cp.Actions = append(cp.Actions[:removeIdx], cp.Actions[removeIdx+1:]...)
	return cp
}

func runCompare(current, candidate *fabric.Fabric, cand Candidate, budget int) fabric.Comparison {
	return fabric.Compare(current, candidate, cand.ToScenario(), fabric.RunOptions{Budget: budget, Window: 0})
}
