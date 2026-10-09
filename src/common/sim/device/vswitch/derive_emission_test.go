package vswitch_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func spanningTreeDeriveSwitch(t *testing.T, priority uint16) *vswitch.Switch {
	t.Helper()
	return mustSwitch(t, vswitch.Config{
		MAC: macRouter,
		Ports: mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})),
		Bridge: &bridge.Config{},
		STP:    &stp.Config{Address: macRouter, Priority: priority, Ports: map[string]stp.Port{"1/1/1": {}, "1/1/2": {}}},
	})
}

func TestDeriveDropsProtocolEmissionsOfReplacedLayer(t *testing.T) {
	sw := spanningTreeDeriveSwitch(t, 32768)
	sw.Start(fixedTime)
	target := sw.Spec()
	target.Config.STP.Priority = 4096
	next, out, err := vswitch.Derive(sw, target, fixedTime.Add(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if next.Retention().STP.Kept {
		t.Fatal("spanning tree kept across a priority change, want rebuilt")
	}
	for _, em := range out.Emissions {
		if !em.Protocol || em.Hold != 0 {
			continue
		}
		if b, err := bpdu.Decode(em.Frame); err == nil {
			t.Errorf("derive output holds a BPDU of the replaced configuration (bridge priority %d) on %s", b.BridgeID.Priority, em.Port)
		}
	}
}

func TestDeriveKeepsProtocolEmissionsOfKeptLayer(t *testing.T) {
	sw := spanningTreeDeriveSwitch(t, 32768)
	sw.Start(fixedTime)
	aged := sw.Fork().(*vswitch.Switch).Advance(fixedTime.Add(10 * time.Second))
	if len(aged.Emissions) == 0 {
		t.Fatal("source emits nothing over 10s, want hello BPDUs")
	}
	target := sw.Spec()
	next, out, err := vswitch.Derive(sw, target, fixedTime.Add(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !next.Retention().STP.Kept {
		t.Fatal("spanning tree not kept across an identical spec")
	}
	if len(out.Emissions) != len(aged.Emissions) {
		t.Errorf("derive emissions = %d, want the %d the kept layer owes", len(out.Emissions), len(aged.Emissions))
	}
}
