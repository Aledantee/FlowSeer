package vswitch_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func TestNormalizeInheritedPVSTPrioritiesFollowBridge(t *testing.T) {
	t.Parallel()

	cfg := vswitch.Config{
		Ports: mustTable(t, port.NewBuilder().Add(port.Port{Name: "1/1/1", Kind: port.Physical})),
		STP: &stp.Config{
			Ports: map[string]stp.Port{"1/1/1": {}},
			PVST:  &stp.PVST{Trees: map[vlan.ID]stp.Tree{10: {Ports: map[string]stp.InstancePort{"1/1/1": {PathCost: 200_000}}}}},
		},
	}
	before := cfg.Normalize()
	initial := before.STP.PVST.Trees[10]
	if initial.Priority != 32768 || initial.PriorityPresent || initial.Ports["1/1/1"].Priority != 128 || initial.Ports["1/1/1"].PriorityPresent {
		t.Errorf("initial tree = %+v, want inherited bridge priority 32768 and port priority 128", initial)
	}
	changed := before.Clone()
	changed.STP.Priority = 4096
	changed.STP.Ports["1/1/1"] = stp.Port{Priority: 64, PriorityPresent: true}
	after := changed.Normalize()
	got := after.STP.PVST.Trees[10]
	if got.Priority != 4096 || got.PriorityPresent {
		t.Errorf("tree = %+v, want inherited priority 4096", got)
	}
	if got := got.Ports["1/1/1"]; got.Priority != 64 || got.PriorityPresent || got.PathCost != 200_000 {
		t.Errorf("tree port = %+v, want inherited priority 64 and cost 200000", got)
	}
}
