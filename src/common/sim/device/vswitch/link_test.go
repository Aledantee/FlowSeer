package vswitch

import (
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"

	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func TestLinkChangeRejectsInvalidStateWithoutMutation(t *testing.T) {
	ports, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).Build()
	if err != nil {
		t.Fatal(err)
	}

	for _, portName := range []string{"1/1/2", "missing"} {
		t.Run(portName, func(t *testing.T) {
			sw, err := New(Config{Ports: ports, Bridge: &bridge.Config{}})
			if err != nil {
				t.Fatal(err)
			}
			before := sw.Ports().Ports()

			_, err = sw.LinkChange(time.Time{}, portName, port.LinkState("invalid"), PointToPointTrue, 0)
			if err == nil {
				t.Fatal("LinkChange with invalid state succeeded, want error")
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

func TestLinkChangeRefusesAnUnknownPort(t *testing.T) {
	ports, err := port.NewBuilder().Add(port.Port{Name: "known", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).Build()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		state port.LinkState
	}{{"missing", port.Up}, {"known", port.LinkState("invalid")}} {
		t.Run(tc.name, func(t *testing.T) {
			sw, err := New(Config{Ports: ports, Bridge: &bridge.Config{}})
			if err != nil {
				t.Fatal(err)
			}
			before := sw.Ports().Ports()
			_, err = sw.LinkChange(time.Time{}, tc.name, tc.state, PointToPointTrue, 1_000_000_000)
			if err == nil {
				t.Error("LinkChange did not return an error")
			}
			if !slices.Equal(before, sw.Ports().Ports()) {
				t.Error("LinkChange changed ports")
			}
			if _, ok := sw.portP2P[tc.name]; ok {
				t.Error("LinkChange stored point-to-point for refused input")
			}
			if _, ok := sw.portSpeed[tc.name]; ok {
				t.Error("LinkChange stored speed for refused input")
			}
		})
	}
}
