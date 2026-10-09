package assembly_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/assembly"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
)

func TestDiffScenariosPairsConfigureByID(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	config := vswitch.Config{MAC: netaddr.MAC{0, 0, 0, 0, 0, 1}}
	a := fabric.Scenario{Name: "configure", Actions: []fabric.Action{
		{ID: "target", At: at, Kind: fabric.ActionConfigure, Configure: new(assembly.ConfigureAction("sw1", config))},
		{ID: "other", At: at, Kind: fabric.ActionConfigure, Configure: new(assembly.ConfigureAction("sw2", config))},
	}}
	b := a.Clone()
	config.MAC[5] = 2
	b.Actions[0].Configure = new(assembly.ConfigureAction("sw1", config))
	b.Actions[0], b.Actions[1] = b.Actions[1], b.Actions[0]
	changes, err := fabric.DiffScenarios(a, b)
	if err != nil || len(changes) != 1 || changes[0].Subject.Kind != "scenario.action" || changes[0].Subject.Key != "target/device%2F" || changes[0].Field != "mac" || changes[0].From.Canonical() != "00:00:00:00:00:01" || changes[0].To.Canonical() != "00:00:00:00:00:02" {
		t.Fatalf("configure changes = %+v, error %v, want one MAC change", changes, err)
	}
}
