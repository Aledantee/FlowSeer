package stp_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/internal/netsimtest"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
)

// TestDiffCoversEveryConfigField is R9's gate: every exported stp.Config field reaches
// stp.Diff. MST and PVST are mutually exclusive at construction (Config.Validate), which
// this fixture never calls; Diff and Normalize take the raw struct, so seeding both
// together reaches every leaf of each in one pass.
func TestDiffCoversEveryConfigField(t *testing.T) {
	seed := stp.Config{
		// Priority is 0 at every level below, paired with PriorityPresent: true: an
		// explicitly configured zero reads differently from an absent one only when
		// Priority is already 0 (effectivePriority and its siblings default only
		// p == 0 && !present), so this is the one value that makes PriorityPresent's
		// leaf observable through Diff at all; any nonzero seed value would make
		// PriorityPresent unobservable regardless of the flag, which is a property of
		// the value chosen, not of whether Diff covers the field.
		Priority:        0,
		PriorityPresent: true,
		Address:         netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x01},
		HelloTime:       2 * time.Second,
		MaxAge:          20 * time.Second,
		ForwardDelay:    15 * time.Second,
		TxHoldCount:     6,
		Ports: map[string]stp.Port{
			"1/1/1": {
				Priority:        0,
				PriorityPresent: true,
				PathCost:        20000,
				AdminEdge:       true,
				AutoEdge:        true,
				PointToPoint:    stp.PointToPointForceTrue,
				BPDUGuard:       true,
				RestrictedRole:  true,
				RestrictedTCN:   true,
				LoopGuard:       true,
			},
		},
		MST: &stp.MST{
			Name:     "region1",
			Revision: 1,
			MaxHops:  20,
			Instances: map[stp.MSTID]stp.Instance{
				1: {
					Priority:        0,
					PriorityPresent: true,
					VLANs:           []vlan.ID{10},
					Ports: map[string]stp.InstancePort{
						"1/1/1": {Priority: 128, PriorityPresent: true, PathCost: 20000},
					},
				},
			},
		},
		PVST: &stp.PVST{
			Trees: map[vlan.ID]stp.Tree{
				// Priority is nonzero here on purpose, unlike the bridge and MST levels
				// above: PVST.Normalize overrides an absent tree's Priority to the
				// bridge's own (0 in this fixture) whenever PriorityPresent is false,
				// with no p == 0 guard, so any Priority other than the bridge's own
				// makes toggling PriorityPresent observable.
				10: {
					Priority:        8192,
					PriorityPresent: true,
					Ports: map[string]stp.InstancePort{
						"1/1/1": {Priority: 128, PriorityPresent: true, PathCost: 20000},
					},
				},
			},
		},
	}

	netsimtest.AssertDiffCoversConfig(t, seed, stp.Config.Normalize, stp.Diff, nil)
}
