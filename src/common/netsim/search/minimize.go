package search

import (
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

	// 1. Reduce injections in scenario in fixed index order
	idx := 0
	for idx < len(reduced.Scenario) {
		if len(reduced.Scenario) <= 1 && len(reduced.Faults) == 0 {
			idx++
			continue
		}

		if trials >= maxTrials {
			return reduced, LimitReached
		}

		trial := copyCandidateWithoutScenarioIndex(reduced, idx)
		trials++

		cmp := runCompare(current, candidate, trial, budget)
		if cmp.Disposition == analysis.Different && cmp.Difference.Observable == targetObservable {
			reduced = trial
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
			return reduced, LimitReached
		}

		trial := copyCandidateWithoutFaultIndex(reduced, fIdx)
		trials++

		cmp := runCompare(current, candidate, trial, budget)
		if cmp.Disposition == analysis.Different && cmp.Difference.Observable == targetObservable {
			reduced = trial
		} else {
			fIdx++
		}
	}

	updateReducedTuple(&reduced)

	return reduced, Minimal
}

func updateReducedTuple(c *Candidate) {
	if len(c.Scenario) == 1 && len(c.Faults) == 0 {
		inj := c.Scenario[0]
		var vid vlan.ID
		if len(inj.Frame.Tags) > 0 {
			vid = inj.Frame.Tags[0].VID
		}
		c.Tuple = formatL2Tuple(inj.Origin, inj.Frame.Dst, vid, inj.Frame.EtherType, inj.Frame.Payload)
	} else if len(c.Faults) == 1 && len(c.Scenario) == 0 {
		f := c.Faults[0]
		c.Tuple = formatFaultTuple(FaultSpec{A: f.A, B: f.B, Fault: f.Fault}, f.At)
	}
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
	cur := current
	candFab := candidate
	if len(cand.Faults) > 0 {
		cur = current.Fork()
		candFab = candidate.Fork()
		for _, f := range cand.Faults {
			_ = cur.SetFault(f.A, f.B, f.Fault)
			_ = candFab.SetFault(f.A, f.B, f.Fault)
		}
	}
	return fabric.Compare(cur, candFab, cand.Scenario, budget)
}
