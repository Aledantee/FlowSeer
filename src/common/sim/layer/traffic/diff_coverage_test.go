package traffic_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/internal/simtest"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
)

// TestDiffCoversEveryConfigField verifies that every exported traffic.Config field reaches
// traffic.Diff.
func TestDiffCoversEveryConfigField(t *testing.T) {
	simtest.AssertMappedDiffCoverage(t, "traffic", simtest.TrafficDiffAllowance())
}

// TestDiffIgnoresMirrorSelectorOrder verifies traffic.Diff's self-normalization: two
// configurations whose mirror selectors list the same elements in a different
// order are not a change, because Diff normalizes both sides through
// Config.Normalize before comparing.
func TestDiffIgnoresMirrorSelectorOrder(t *testing.T) {
	a := traffic.Config{
		Mirrors: []traffic.Mirror{
			{
				Name:           "m1",
				SelectSrcPorts: []string{"1/1/1", "1/1/2"},
				SelectVLANs:    []vlan.ID{20, 10},
			},
		},
	}
	b := traffic.Config{
		Mirrors: []traffic.Mirror{
			{
				Name:           "m1",
				SelectSrcPorts: []string{"1/1/2", "1/1/1"},
				SelectVLANs:    []vlan.ID{10, 20},
			},
		},
	}

	if changes := traffic.Diff(a, b); len(changes) != 0 {
		t.Errorf("Diff(a, b) = %+v, want none: the two configs differ only in selector order", changes)
	}
}
