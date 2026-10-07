package search

import (
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
)

// Search evaluates a finite candidate domain against current and candidate fabrics,
// bounded by lim.
//
// Neither current nor candidate fabric is stepped, injected, or consumed across the search.
// Search returns coverage progress, the exact slice of untested tuples when halted early
// by candidate budget, and full replay specifications only for retained differences up to
// lim.MaxDifferences. Discarded candidates contribute to summary counters only.
// Invalid limits return an error before enumeration. Candidate failures increment
// Result.Errors and retain only the first wrapped error with its tuple in Result.Err.
func Search(current, candidate *fabric.Fabric, dom Domain, lim Limits) (Result, error) {
	if err := lim.Validate(); err != nil {
		return Result{}, err
	}
	total := dom.Size()
	if total == 0 {
		return Result{
			Coverage: Coverage{Tested: 0, Total: 0},
		}, nil
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

		sc := cand.ToScenario(lim.Budget)
		cand.Actions = sc.Actions
		var comparisonErr error
		var cmp fabric.Comparison
		if comparisonErr = cand.Validate(); comparisonErr == nil {
			cmp = fabric.Compare(current, candidate, sc, lim.Budget)
			comparisonErr = cmp.Err
		}
		if comparisonErr != nil {
			res.Errors++
			if res.Err == nil {
				res.Err = errs.Wrapf(comparisonErr, "candidate %q", cand.Tuple)
			}
			return true
		}
		switch cmp.Disposition {
		case analysis.Different:
			res.TotalDifferences++
			if lim.MaxDifferences == -1 || len(res.Differences) < lim.MaxDifferences {
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

	return res, nil
}
