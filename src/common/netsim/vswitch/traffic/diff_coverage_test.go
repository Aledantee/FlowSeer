package traffic_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/internal/netsimtest"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
)

// TestDiffCoversEveryConfigField is R9's gate: every exported traffic.Config field
// reaches traffic.Diff.
func TestDiffCoversEveryConfigField(t *testing.T) {
	outputVLAN := vlan.ID(20)
	seed := traffic.Config{
		Mirrors: []traffic.Mirror{
			{
				Name:           "m1",
				SelectAll:      true,
				SelectSrcPorts: []string{"1/1/1"},
				SelectDstPorts: []string{"1/1/2"},
				SelectVLANs:    []vlan.ID{10},
				OutputPort:     "1/1/3",
				OutputVLAN:     &outputVLAN,
				SnapLen:        128,
			},
		},
		Policers: map[string]traffic.Policer{
			"1/1/1": {RateBPS: 1_000_000, BurstOctets: 1500},
		},
		Queues: map[string]traffic.PortQueues{
			"1/1/1": {MaxRateBPS: map[vlan.PCP]uint64{0: 1_000_000}, BufferOctets: map[vlan.PCP]uint64{0: 4096}},
		},
	}

	netsimtest.AssertDiffCoversConfig(t, seed, traffic.Config.Normalize, traffic.Diff, nil)
}

// TestDiffIgnoresMirrorSelectorOrder is U3's functional case for traffic.Diff's new
// self-normalization: two configurations whose mirror selectors list the same elements
// in a different order are not a change, because Diff now normalizes both sides through
// Config.Normalize before comparing instead of re-normalizing each selector by hand.
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
