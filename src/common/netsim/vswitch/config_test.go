package vswitch_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/loopprotect"
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

// TestValidateRefusesLoopProtectWithoutBridge proves that loop protection,
// like spanning tree, requires a bridge to gate: with no bridge relay there
// is nothing for the layer's Block or NoLearn action to deny.
func TestValidateRefusesLoopProtectWithoutBridge(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "a", Kind: port.Physical}))

	cfg := vswitch.Config{
		Ports: tbl,
		LoopProtect: &loopprotect.Config{
			Ports: map[string]loopprotect.Port{
				"a": {Action: loopprotect.Block},
			},
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() = nil, want error when loop protection is configured without bridge")
	}
}

// TestValidateRefusesLoopProtectVLANNotAdmitted proves that a probe VLAN a
// loop-protection port names must be one its switchport actually carries: a
// VID the switchport admits neither tagged nor untagged nor as its PVID
// would never see the port's own probe return, silently disabling detection.
func TestValidateRefusesLoopProtectVLANNotAdmitted(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "a", Kind: port.Physical}))

	cfg := vswitch.Config{
		Ports: tbl,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10"},
				Switchports: map[string]bridge.Switchport{
					"a": {Untagged: []vlan.ID{10}},
				},
			},
		},
		LoopProtect: &loopprotect.Config{
			Ports: map[string]loopprotect.Port{
				"a": {Action: loopprotect.Block, VLANs: []vlan.ID{20}},
			},
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() = nil, want error when a loop protection port names a VLAN its switchport does not admit")
	}
}
