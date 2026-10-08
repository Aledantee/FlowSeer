package vswitch

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func TestLinkChangeRejectsAbsentPortWithoutMutation(t *testing.T) {
	ports, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).Build()
	if err != nil {
		t.Fatal(err)
	}
	sw, err := New(Config{Ports: ports, Bridge: &bridge.Config{}})
	if err != nil {
		t.Fatal(err)
	}
	before := sw.ports.Ports()
	beforeRelay := sw.bridge.Clone()
	_, err = sw.LinkChange(time.Time{}, "absent", port.Down, PointToPointTrue, 0)
	if err == nil {
		t.Fatal("LinkChange(absent, Down) accepted absent port")
	}
	if got := errs.Attributes(err)["field"]; got != "ports.absent" {
		t.Errorf("field = %v, want ports.absent", got)
	}
	if got := sw.ports.Ports(); !slices.Equal(got, before) {
		t.Errorf("ports = %+v, want unchanged %+v", got, before)
	}
	if !reflect.DeepEqual(sw.bridge.Clone(), beforeRelay) {
		t.Error("absent-port update changed relay")
	}
}

func TestLinkChangeRejectsAbsentPortWithoutRelay(t *testing.T) {
	ports, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).Build()
	if err != nil {
		t.Fatal(err)
	}
	sw, err := New(Config{Ports: ports})
	if err != nil {
		t.Fatalf("New without Bridge: %v", err)
	}
	if sw.bridge != nil {
		t.Fatal("New without Bridge constructed a relay, want nil")
	}
	before := sw.ports.Clone()
	_, err = sw.LinkChange(time.Time{}, "absent", port.Down, PointToPointTrue, 0)
	if err == nil {
		t.Error("LinkChange(absent, Down) error = nil, want error")
	} else if got := errs.Attributes(err)["field"]; got != "ports.absent" {
		t.Errorf("field = %v, want ports.absent", got)
	}
	if !reflect.DeepEqual(sw.ports, before) {
		t.Errorf("ports = %+v, want unchanged %+v", sw.ports, before)
	}
}
