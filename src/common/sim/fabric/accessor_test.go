package fabric_test

import (
	"reflect"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func TestDeviceAccessorIsACopy(t *testing.T) {
	fab := newTestFabricForFork(t)
	before := fab.Snapshot()
	d := fab.Device("sw1")
	if _, err := d.LinkChange(before.Clock, "1/1/1", device.Link{State: port.Down}); err != nil {
		t.Fatal(err)
	}
	if after := fab.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatal("link change on returned device changed fabric snapshot")
	}
}

func TestStepKeepsAggregateHopCompact(t *testing.T) {
	fab, src, dst := newTwoSwitchTopology(t, cablemodel.Fault{})
	_, err := fab.Inject(fabric.Injection{
		At: fab.Clock(), Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
		Frame: ethernet.Frame{Src: src, Dst: dst}, Flow: 1, Retention: fabric.RetainAggregate,
	})
	if err != nil {
		t.Fatal(err)
	}
	step, ok := fab.Step()
	if !ok || step.Kind != fabric.EntryHop || step.Result != nil {
		t.Fatalf("aggregate step = %+v, %t, want hop without result storage", step, ok)
	}
}

func TestRuntimeJourneyHasNoState(t *testing.T) {
	field, ok := reflect.TypeOf(fabric.Fabric{}).FieldByName("journeys")
	if !ok {
		t.Fatal("fabric has no journey storage")
	}
	if _, stored := field.Type.Elem().Elem().FieldByName("State"); stored {
		t.Fatal("execution journey stores a derived State")
	}
}
