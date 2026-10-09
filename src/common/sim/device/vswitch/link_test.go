package vswitch

import (
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func TestPointToPointDecidedBySwitch(t *testing.T) {
	ports, err := port.NewBuilder().Add(port.Port{Name: "p", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).Build()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		report   device.Link
		override stp.PointToPointMode
		want     pointToPoint
	}{
		{"unset duplex", device.Link{}, stp.PointToPointAuto, pointToPointUnknown},
		{"unknown duplex", device.Link{Duplex: device.DuplexUnknown}, stp.PointToPointAuto, pointToPointUnknown},
		{"half duplex", device.Link{Duplex: device.DuplexHalf, PeerRelays: true}, stp.PointToPointAuto, pointToPointFalse},
		{"host", device.Link{Duplex: device.DuplexFull, PeerRelays: true}, stp.PointToPointAuto, pointToPointTrue},
		{"reflector", device.Link{Duplex: device.DuplexFull, PeerRelays: true}, stp.PointToPointAuto, pointToPointTrue},
		{"relay", device.Link{Duplex: device.DuplexFull, PeerRelays: true}, stp.PointToPointAuto, pointToPointTrue},
		{"repeater", device.Link{Duplex: device.DuplexFull}, stp.PointToPointAuto, pointToPointFalse},
		{"force true", device.Link{Duplex: device.DuplexUnknown}, stp.PointToPointForceTrue, pointToPointTrue},
		{"force false", device.Link{Duplex: device.DuplexFull, PeerRelays: true}, stp.PointToPointForceFalse, pointToPointFalse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sw, err := New(Config{Ports: ports, Bridge: &bridge.Config{}, STP: &stp.Config{Ports: map[string]stp.Port{"p": {PointToPoint: tc.override}}}})
			if err != nil {
				t.Fatal(err)
			}
			tc.report.State = port.Up
			tc.report.SpeedBPS = 1_000_000_000
			if _, err := sw.LinkChange(time.Unix(1000, 0), "p", tc.report); err != nil {
				t.Fatal(err)
			}
			if got := sw.portP2P["p"]; got != tc.want {
				t.Errorf("point-to-point = %s, want %s", got, tc.want)
			}
		})
	}
}

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

			_, err = sw.LinkChange(time.Time{}, portName, device.Link{State: port.LinkState("invalid"), Duplex: device.DuplexFull, PeerRelays: true, SpeedBPS: 0})
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
			_, err = sw.LinkChange(time.Time{}, tc.name, device.Link{State: tc.state, Duplex: device.DuplexFull, PeerRelays: true, SpeedBPS: 1_000_000_000})
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
