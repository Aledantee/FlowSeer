package search

import (
	"strings"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
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

	initialCmp := runCompare(current, candidate, cand, budget)
	if initialCmp.Disposition != analysis.Different {
		return cand, Minimal
	}
	targetObservable := initialCmp.Difference.Observable

	reduced := cand.Clone()
	trials := 0

	for {
		changed := false

		// 1. Reduce injections in scenario in fixed index order
		idx := 0
		for idx < len(reduced.Scenario) {
			if len(reduced.Scenario) <= 1 && len(reduced.Faults) == 0 {
				idx++
				continue
			}

			if trials >= maxTrials {
				reduced.Tuple = computeCandidateTuple(reduced)
				return reduced, LimitReached
			}

			trial := copyCandidateWithoutScenarioIndex(reduced, idx)
			trials++

			cmp := runCompare(current, candidate, trial, budget)
			if cmp.Disposition == analysis.Different && cmp.Difference.Observable == targetObservable {
				reduced = trial
				changed = true
			} else {
				idx++
			}
		}

		// 2. Reduce faults in fixed index order
		fIdx := 0
		for fIdx < len(reduced.Faults) {
			if len(reduced.Scenario) == 0 && len(reduced.Faults) <= 1 {
				fIdx++
				continue
			}

			if trials >= maxTrials {
				reduced.Tuple = computeCandidateTuple(reduced)
				return reduced, LimitReached
			}

			trial := copyCandidateWithoutFaultIndex(reduced, fIdx)
			trials++

			cmp := runCompare(current, candidate, trial, budget)
			if cmp.Disposition == analysis.Different && cmp.Difference.Observable == targetObservable {
				reduced = trial
				changed = true
			} else {
				fIdx++
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
	parts := make([]string, 0, len(c.Scenario)+len(c.Faults))
	for _, inj := range c.Scenario {
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
	for _, f := range c.Faults {
		parts = append(parts, string(formatFaultTuple(FaultSpec{A: f.A, B: f.B, Fault: f.Fault}, f.At)))
	}
	if len(parts) == 0 {
		return Tuple("")
	}
	if len(parts) == 1 {
		return Tuple(parts[0])
	}
	return Tuple(strings.Join(parts, "; "))
}

func copyCandidateWithoutScenarioIndex(c Candidate, removeIdx int) Candidate {
	cp := c.Clone()
	cp.Scenario = append(cp.Scenario[:removeIdx], cp.Scenario[removeIdx+1:]...)
	return cp
}

func copyCandidateWithoutFaultIndex(c Candidate, removeIdx int) Candidate {
	cp := c.Clone()
	cp.Faults = append(cp.Faults[:removeIdx], cp.Faults[removeIdx+1:]...)
	return cp
}

func runCompare(current, candidate *fabric.Fabric, cand Candidate, budget int) fabric.Comparison {
	return fabric.Compare(current, candidate, cand.ToScenario(budget), budget)
}
