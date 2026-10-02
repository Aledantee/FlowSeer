package fabric

import (
	"testing"
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
	if !first.Equal(fab.Metadata()) {
		t.Error("two Metadata calls without a SetFault returned different values")
	}

	a := Endpoint{Node: "h1"}
	b := Endpoint{Node: "sw1", Port: "1/1/1"}
	if err := fab.SetFault(a, b, Fault{Kind: FaultCut}); err != nil {
		t.Fatalf("SetFault: %v", err)
	}

	if first.Equal(fab.Metadata()) {
		t.Error("Metadata is unchanged after SetFault rewrote the link")
	}
}
