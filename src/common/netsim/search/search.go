package search

import (
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
)

// Search evaluates a finite candidate domain against current and candidate fabrics,
// bounded by lim.
//
// Neither current nor candidate fabric is stepped, injected, or consumed across the search.
// Search returns coverage progress, the exact slice of untested tuples when halted early
// by candidate budget, and full replay specifications only for retained differences up to
// lim.MaxDifferences. Discarded candidates contribute to summary counters only.
func Search(current, candidate *fabric.Fabric, dom Domain, lim Limits) Result {
	total := dom.Size()
	if total == 0 {
		return Result{
			Coverage: Coverage{Tested: 0, Total: 0},
		}
	}

	res := Result{
		Coverage: Coverage{Total: total},
	}

	dom.Enumerate(func(cand Candidate) bool {
		if lim.MaxCandidates > 0 && res.Coverage.Tested >= lim.MaxCandidates {
			res.Remainder = append(res.Remainder, cand.Tuple)
			return true
		}

		res.Coverage.Tested++

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

		cmp := fabric.Compare(cur, candFab, cand.Scenario, lim.Budget)
		switch cmp.Disposition {
		case analysis.Different:
			res.TotalDifferences++
			if lim.MaxDifferences <= 0 || len(res.Differences) < lim.MaxDifferences {
				res.Differences = append(res.Differences, RetainedDifference{
					Tuple:      cand.Tuple,
					Candidate:  cand.Clone(),
					Replay:     cmp.Replay,
					Difference: cmp.Difference,
				})
			}
		case analysis.Equivalent:
			res.EquivalentCount++
		case analysis.Inconclusive:
			res.InconclusiveCount++
		}

		return true
	})

	return res
}
