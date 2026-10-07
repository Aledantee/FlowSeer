package search

import (
	"strings"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
)

// Minimality indicates whether scenario reduction reached a minimal fixed point
// or halted due to an evaluation limit.
type Minimality string

const (
	// Minimal indicates that no further scenario elements could be removed without
	// altering or losing the target divergence observable.
	Minimal Minimality = "Minimal"

	// LimitReached indicates that reduction halted because the maximum number of
	// reduction attempts was reached before achieving a fixed point.
	LimitReached Minimality = "LimitReached"
)

// DefaultMinimizationLimit is the default maximum number of reduction trials
// evaluated during counterexample minimization.
const DefaultMinimizationLimit = 64

// Minimize reduces a divergence counterexample within the domain by iteratively
// removing scenario elements (injections, then faults) in a fixed order.
//
// A removal is preserved only if re-running fabric.Compare against the candidate
// still reproduces the exact same Difference.Observable. Minimization returns either
// Minimal when no further elements can be pruned, or LimitReached if the evaluation
// limit is reached.
func Minimize(current, candidate *fabric.Fabric, cand Candidate, budget int) (Candidate, Minimality) {
	return MinimizeWithLimit(current, candidate, cand, budget, DefaultMinimizationLimit)
}

// MinimizeWithLimit reduces a counterexample up to a caller-specified maximum trial limit.
func MinimizeWithLimit(current, candidate *fabric.Fabric, cand Candidate, budget int, maxTrials int) (Candidate, Minimality) {
	if maxTrials <= 0 {
		maxTrials = DefaultMinimizationLimit
	}

	cand.Actions = cand.ToScenario(budget).Actions
	initialCmp := runCompare(current, candidate, cand, budget)
	if initialCmp.Disposition != analysis.Different {
		return cand, Minimal
	}
	targetObservable := initialCmp.Difference.Observable

	reduced := cand.Clone()
	trials := 0

	for {
		changed := false

		for _, kind := range []fabric.ActionKind{fabric.ActionInject, fabric.ActionFault} {
			idx := 0
			for idx < len(reduced.Actions) {
				if reduced.Actions[idx].Kind != kind || len(reduced.Actions) <= 1 {
					idx++
					continue
				}
				if trials >= maxTrials {
					reduced.Tuple = computeCandidateTuple(reduced)
					return reduced, LimitReached
				}
				trial := copyCandidateWithoutActionIndex(reduced, idx)
				trials++
				cmp := runCompare(current, candidate, trial, budget)
				if cmp.Disposition == analysis.Different && cmp.Difference.Observable == targetObservable {
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
	return reduced, Minimal
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
	return fabric.Compare(current, candidate, cand.ToScenario(budget), budget)
}
