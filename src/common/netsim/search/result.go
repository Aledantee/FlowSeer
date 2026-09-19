package search

import (
	"fmt"

	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
)

// Coverage records the count of evaluated tuples against total domain candidate size.
type Coverage struct {
	Tested int
	Total  int
}

// Complete reports whether every candidate in the domain was evaluated.
// Complete requires a non-empty domain (Total > 0) and Tested == Total;
// an empty domain is not complete even though Ratio returns 1.0.
func (c Coverage) Complete() bool {
	return c.Total > 0 && c.Tested == c.Total
}

// Ratio returns tested / total, or 1.0 if domain size is zero.
func (c Coverage) Ratio() float64 {
	if c.Total == 0 {
		return 1.0
	}
	return float64(c.Tested) / float64(c.Total)
}

// String returns a human-readable representation of coverage progress.
func (c Coverage) String() string {
	return fmt.Sprintf("%d/%d", c.Tested, c.Total)
}

// RetainedDifference preserves complete causal and replay artifacts for a behavioral
// divergence under the search resource contract.
type RetainedDifference struct {
	Tuple      Tuple
	Candidate  Candidate
	Replay     [2]fabric.ReplaySpec
	Difference fabric.Difference
}

// Result summarizes the differential search outcome across an explored domain.
type Result struct {
	Coverage          Coverage
	Remainder         []Tuple
	Differences       []RetainedDifference
	TotalDifferences  int
	EquivalentCount   int
	InconclusiveCount int
}

// DiscardedDifferences returns the count of divergence candidates whose full traces
// were discarded due to the retained difference limit.
func (r Result) DiscardedDifferences() int {
	if r.TotalDifferences > len(r.Differences) {
		return r.TotalDifferences - len(r.Differences)
	}
	return 0
}
