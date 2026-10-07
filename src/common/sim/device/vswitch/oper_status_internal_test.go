package vswitch

import (
	"reflect"
	"slices"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func TestSetOperStatusRejectsAbsentPortWithoutMutation(t *testing.T) {
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
	err = sw.SetOperStatus("absent", port.Down)
	if err == nil {
		t.Fatal("SetOperStatus(absent, Down) accepted absent port")
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

func TestSetOperStatusRejectsAbsentPortWithoutRelay(t *testing.T) {
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
	err = sw.SetOperStatus("absent", port.Down)
	if err == nil {
		t.Error("SetOperStatus(absent, Down) error = nil, want error")
	} else if got := errs.Attributes(err)["field"]; got != "ports.absent" {
		t.Errorf("field = %v, want ports.absent", got)
	}
	if !reflect.DeepEqual(sw.ports, before) {
		t.Errorf("ports = %+v, want unchanged %+v", sw.ports, before)
	}
}
