package search

import (
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

// Align identifies the first entry index in paired traversal journeys where causal
// trace facts diverge.
//
// Align returns (index, true) naming the first differing entry index, or (0, false)
// if comparison is Equivalent or no trace facts diverge.
func Align(cmp fabric.Comparison) (int, bool) {
	if cmp.Disposition == analysis.Equivalent {
		return 0, false
	}

	for j := 0; j < len(cmp.Current) && j < len(cmp.Expected); j++ {
		curJ := cmp.Current[j]
		expJ := cmp.Expected[j]

		minLen := min(len(curJ.Entries), len(expJ.Entries))
		for k := 0; k < minLen; k++ {
			if !sameEntryTraceFacts(curJ.Entries[k], expJ.Entries[k]) {
				return k, true
			}
		}

		if len(curJ.Entries) != len(expJ.Entries) {
			return minLen, true
		}
	}

	return 0, false
}

func sameEntryTraceFacts(a, b fabric.Entry) bool {
	if a.Kind != b.Kind || a.Device != b.Device || a.Port != b.Port || a.Reason != b.Reason {
		return false
	}

	stepsA := collectEntrySteps(a)
	stepsB := collectEntrySteps(b)

	if len(stepsA) != len(stepsB) {
		return false
	}

	for i := range stepsA {
		if !trace.EqualStep(stepsA[i], stepsB[i]) {
			return false
		}
	}

	return true
}

func collectEntrySteps(e fabric.Entry) []trace.Step {
	var out []trace.Step
	if e.Step != nil {
		out = append(out, *e.Step)
	}
	if e.Result != nil {
		out = append(out, e.Result.Steps...)
	}
	return out
}
