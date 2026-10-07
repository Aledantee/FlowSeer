package vswitch_test

import (
	"slices"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func TestSetOperStatusRejectsInvalidStateWithoutMutation(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	for _, portName := range []string{"1/1/2", "missing"} {
		t.Run(portName, func(t *testing.T) {
			sw := mustSwitch(t, vswitch.Config{Ports: ports, Bridge: &bridge.Config{}})
			before := sw.Ports().Ports()

			err := sw.SetOperStatus(portName, port.LinkState("invalid"))
			if err == nil {
				t.Fatal("SetOperStatus with invalid state succeeded, want error")
			}
			attrs := errs.Attributes(err)
			wantField := "ports." + portName + ".oper_status"
			var wantState any = port.LinkState("invalid")
			if portName == "missing" {
				wantField = "ports." + portName
				wantState = nil
			}
			if got := attrs["field"]; got != wantField {
				t.Errorf("field attribute = %v, want %s", got, wantField)
			}
			if got := attrs["oper_status"]; got != wantState {
				t.Errorf("oper_status attribute = %v, want %v", got, wantState)
			}
			if got := attrs["name"]; got != portName {
				t.Errorf("name attribute = %v, want %s", got, portName)
			}
			if after := sw.Ports().Ports(); !slices.Equal(after, before) {
				t.Errorf("ports after invalid update = %+v, want unchanged %+v", after, before)
			}
		})
	}
}
