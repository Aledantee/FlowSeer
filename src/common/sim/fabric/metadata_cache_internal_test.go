package fabric

import (
	"slices"
	"testing"

	"go.aledante.io/FlowSeer/src/common/sim/analysis"
)

// TestFabricMetadataCachesUntilSetFault pins the two halves of the cache:
// repeated calls with no link change return the same value, and a SetFault
// that rewrites a link changes what the next call returns.
func TestFabricMetadataCachesUntilSetFault(t *testing.T) {
	fab := newTestFabricForFork(t)

	first := fab.Metadata()
	if fab.metadataCache == nil {
		t.Fatal("Metadata did not cache its result")
	}
	if !sameMetadata(first, fab.Metadata()) {
		t.Error("two Metadata calls without a SetFault returned different values")
	}

	a := Endpoint{Node: "h1"}
	b := Endpoint{Node: "sw1", Port: "1/1/1"}
	if err := fab.SetFault(a, b, Fault{Kind: FaultCut}); err != nil {
		t.Fatalf("SetFault: %v", err)
	}

	if sameMetadata(first, fab.Metadata()) {
		t.Error("Metadata is unchanged after SetFault rewrote the link")
	}
}

// sameMetadata reports whether a and b hold the same issues in order, with
// their messages, and the same assumptions in order. [analysis.Metadata.Equal]
// ignores issue Message and order, which would hide a cache that returns a
// stale rendering.
func sameMetadata(a, b analysis.Metadata) bool {
	aIssues, bIssues := a.Issues(), b.Issues()
	if len(aIssues) != len(bIssues) {
		return false
	}
	for i := range aIssues {
		if !analysis.SameIssue(aIssues[i], bIssues[i]) {
			return false
		}
	}

	aAssumptions, bAssumptions := a.Assumptions(), b.Assumptions()
	if len(aAssumptions) != len(bAssumptions) {
		return false
	}
	for i := range aAssumptions {
		x, y := aAssumptions[i].Canonical(), bAssumptions[i].Canonical()
		if x.Scope.Compare(y.Scope) != 0 || x.Statement != y.Statement || !slices.Equal(x.Evidence, y.Evidence) {
			return false
		}
	}

	return true
}
