package vswitch_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
)

// TestCloneMSTRegionIsIndependent proves that cloning a Config carrying an
// MST region yields a clone whose region, instances, VLAN lists, and
// per-instance port maps do not alias the original's. Config.Clone once
// hand-rolled the STP copy and stopped before MST, so two configs Clone
// reported as independent shared one *stp.MST underneath.
func TestCloneMSTRegionIsIndependent(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "a", Kind: port.Physical}).
		Add(port.Port{Name: "b", Kind: port.Physical}))

	original := vswitch.Config{
		Ports: ports,
		STP: &stp.Config{
			Ports: map[string]stp.Port{"a": {}},
			MST: &stp.MST{
				Name: "region-1",
				Instances: map[stp.MSTID]stp.Instance{
					1: {
						VLANs: []vlan.ID{10, 20},
						Ports: map[string]stp.InstancePort{"a": {}},
					},
				},
			},
		},
	}

	clone := original.Clone()

	clone.STP.MST.Instances[1].VLANs[0] = 999
	clone.STP.MST.Instances[1].Ports["b"] = stp.InstancePort{Priority: 64, PriorityPresent: true}

	originalInstance := original.STP.MST.Instances[1]
	if originalInstance.VLANs[0] != 10 {
		t.Errorf("original instance VLANs[0] = %d after mutating the clone, want 10 (unchanged)", originalInstance.VLANs[0])
	}
	if _, ok := originalInstance.Ports["b"]; ok {
		t.Errorf("original instance Ports has key %q after the clone added it, want it absent", "b")
	}
}
