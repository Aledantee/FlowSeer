package fabric_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func TestFabricSeedDefaultsCompareEqual(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	ports := mustFabricPortTable(t, port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	base, err := fabric.NewConstructionSpec(statedPhysical(fabric.Config{
		Start:    now,
		Switches: map[string]vswitch.Config{"sw1": {Ports: ports, Bridge: &bridge.Config{}}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	omitted := base.Clone()
	sw := omitted.Switches["sw1"]
	sw.Seeds = []bridge.Seed{{MAC: netaddr.MAC{2, 0, 0, 0, 0, 1}, Port: "1/1/1", LearnedAt: now}}
	omitted.Switches["sw1"] = sw
	explicit := omitted.Clone()
	sw = explicit.Switches["sw1"]
	sw.Seeds[0].Origin = "configured"
	sw.Seeds[0].Lifetime = "aging"
	explicit.Switches["sw1"] = sw
	a, err := fabric.NewWithSpec(omitted)
	if err != nil {
		t.Fatal(err)
	}
	b, err := fabric.NewWithSpec(explicit)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Spec().Equal(b.Spec()) {
		t.Error("constructed specs differ for omitted and explicit seed defaults")
	}
	changes, err := fabric.DiffSpecs(omitted, explicit)
	if err != nil || len(changes) != 0 {
		t.Errorf("DiffSpecs = %+v, %v, want empty", changes, err)
	}
	if a.Snapshot().Fingerprint() != b.Snapshot().Fingerprint() {
		t.Error("snapshots differ for omitted and explicit seed defaults")
	}
	comparison := fabric.Compare(a, b, fabric.Scenario{Name: "seed-defaults", Spec: a.Spec()}, 10)
	if comparison.Err != nil || comparison.Disposition != analysis.Equivalent {
		t.Errorf("Compare = %+v, want Equivalent without error", comparison)
	}
}
