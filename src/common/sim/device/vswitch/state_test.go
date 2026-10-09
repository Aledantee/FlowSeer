package vswitch

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/mcast"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func TestStateCarriesEveryLayerField(t *testing.T) {
	rows := []struct{ layer, seam any }{
		{bridge.Entry{}, device.Entry{}},
		{bridge.Counters{}, device.RelayCounters{}},
		{mcast.Entry{}, device.GroupEntry{}},
		{mcast.RouterPort{}, device.RouterPort{}},
		{stp.PortInfo{}, device.PortInfo{}},
		{routing.NeighborEntry{}, device.NeighborEntry{}},
		{phy.Allocation{}, device.Allocation{}},
		{phy.PortAllocation{}, device.PortAllocation{}},
		{phy.GroupAllocation{}, device.GroupAllocation{}},
	}
	reached := 0
	for _, row := range rows {
		t.Run(reflect.TypeOf(row.layer).PkgPath()+"/"+reflect.TypeOf(row.layer).Name(), func(t *testing.T) {
			checkStateFields(t, reflect.TypeOf(row.layer), reflect.TypeOf(row.seam))
			reached++
		})
	}
	if reached != 9 {
		t.Fatalf("layer roots reaching expectations = %d, want 9", reached)
	}
	cases := []struct {
		name   string
		values []any
		want   []string
	}{
		{"FDB origin", []any{stateEntry(bridge.Entry{Origin: bridge.Configured}).Origin, stateEntry(bridge.Entry{Origin: bridge.Observed}).Origin}, []string{"configured", "observed"}},
		{"FDB lifetime", []any{stateEntry(bridge.Entry{Lifetime: bridge.Aging}).Lifetime, stateEntry(bridge.Entry{Lifetime: bridge.Static}).Lifetime}, []string{"aging", "static"}},
		{"router origin", []any{stateRouterPort(mcast.RouterPort{Origin: mcast.Configured}).Origin, stateRouterPort(mcast.RouterPort{Origin: mcast.Observed}).Origin}, []string{"configured", "observed"}},
		{"router lifetime", []any{stateRouterPort(mcast.RouterPort{Lifetime: mcast.Aging}).Lifetime, stateRouterPort(mcast.RouterPort{Lifetime: mcast.Static}).Lifetime}, []string{"aging", "static"}},
		{"group mode", []any{stateGroup(mcast.Entry{Mode: mcast.Include}).Mode, stateGroup(mcast.Entry{Mode: mcast.Exclude}).Mode}, []string{"INCLUDE", "EXCLUDE"}},
		{"block reason", []any{statePortInfo(stp.PortInfo{BlockReason: stp.BlockReasonBPDUGuard}).BlockReason, statePortInfo(stp.PortInfo{BlockReason: stp.BlockReasonPVIDInconsistent}).BlockReason, statePortInfo(stp.PortInfo{BlockReason: stp.BlockReasonLoopInconsistent}).BlockReason}, []string{"bpdu-guard", "pvid-inconsistent", "loop-inconsistent"}},
		{"tree kind", []any{statePortInfo(stp.PortInfo{Tree: stp.TreeRef{Kind: stp.TreeCIST}}).Tree.Kind, statePortInfo(stp.PortInfo{Tree: stp.TreeRef{Kind: stp.TreeMSTI}}).Tree.Kind, statePortInfo(stp.PortInfo{Tree: stp.TreeRef{Kind: stp.TreeVLAN}}).Tree.Kind}, []string{"CIST", "MSTI", "VLAN"}},
		{"port state", []any{statePortInfo(stp.PortInfo{State: stp.StateDiscarding}).State, statePortInfo(stp.PortInfo{State: stp.StateLearning}).State, statePortInfo(stp.PortInfo{State: stp.StateForwarding}).State}, []string{"Discarding", "Learning", "Forwarding"}},
		{"neighbor state", []any{stateNeighbor(routing.NeighborEntry{State: routing.NeighborUnobserved}).State, stateNeighbor(routing.NeighborEntry{State: routing.NeighborIncomplete}).State, stateNeighbor(routing.NeighborEntry{State: routing.NeighborReachable}).State, stateNeighbor(routing.NeighborEntry{State: routing.NeighborStale}).State, stateNeighbor(routing.NeighborEntry{State: routing.NeighborDelay}).State, stateNeighbor(routing.NeighborEntry{State: routing.NeighborProbe}).State}, []string{"", "incomplete", "reachable", "stale", "delay", "probe"}},
		{"power state", []any{stateAllocation(phy.Allocation{Ports: map[string]phy.PortAllocation{"p": {State: phy.PowerUnknown}}}).Ports["p"].State, stateAllocation(phy.Allocation{Ports: map[string]phy.PortAllocation{"p": {State: phy.PowerNoDevice}}}).Ports["p"].State, stateAllocation(phy.Allocation{Ports: map[string]phy.PortAllocation{"p": {State: phy.PowerDenied}}}).Ports["p"].State, stateAllocation(phy.Allocation{Ports: map[string]phy.PortAllocation{"p": {State: phy.PowerDelivered}}}).Ports["p"].State}, []string{"Unknown", "NoDevice", "Denied", "Delivered"}},
	}
	constants := 0
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seen := map[string]bool{}
			for i, value := range tc.values {
				got := fmt.Sprint(value)
				if got != tc.want[i] {
					t.Errorf("constant %d = %q, want %q", i, got, tc.want[i])
				}
				if seen[got] {
					t.Errorf("two layer constants convert to %q", got)
				}
				seen[got] = true
				constants++
			}
		})
	}
	if constants != 29 {
		t.Fatalf("constants reaching expectations = %d, want 29", constants)
	}
}

func checkStateFields(t *testing.T, layer, seam reflect.Type) {
	t.Helper()
	if layer.Kind() != seam.Kind() {
		t.Errorf("%s kind = %s, want %s", seam, seam.Kind(), layer.Kind())
		return
	}
	switch layer.Kind() {
	case reflect.Struct:
		for field := range layer.Fields() {
			if !field.IsExported() {
				continue
			}
			target, ok := seam.FieldByName(field.Name)
			if !ok {
				t.Errorf("%s.%s has no field in %s", layer.Name(), field.Name, seam.Name())
				continue
			}
			checkStateFields(t, field.Type, target.Type)
		}
	case reflect.Map, reflect.Slice:
		checkStateFields(t, layer.Elem(), seam.Elem())
	}
}

func TestStateKeepsCISTWhenVLANOneIsAssignedToMSTI(t *testing.T) {
	ports, err := port.NewBuilder().Add(port.Port{Name: "p", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).Build()
	if err != nil {
		t.Fatal(err)
	}
	sw, err := New(Config{Ports: ports, Bridge: &bridge.Config{}, STP: &stp.Config{Address: netaddr.MAC{2, 0, 0, 0, 0, 1}, Ports: map[string]stp.Port{"p": {}}, MST: &stp.MST{Instances: map[bpdu.MSTID]stp.Instance{1: {VLANs: []vlan.ID{1}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if row := sw.TreeRoles()[1]["p"]; row.Tree.Kind != stp.TreeMSTI || row.Tree.ID != 1 {
		t.Fatalf("VLAN 1 tree = %+v, want MSTI 1", row.Tree)
	}
	state := sw.State(time.Unix(0, 0))
	if len(state.TreeRoles) != 2 {
		t.Fatalf("trees = %+v, want exactly CIST 0 and MSTI 1", state.TreeRoles)
	}
	for _, tree := range []device.TreeRef{{Kind: device.TreeCIST, ID: 0}, {Kind: device.TreeMSTI, ID: 1}} {
		row, ok := state.TreeRoles[tree]["p"]
		if !ok || row.Tree != tree {
			t.Errorf("tree %+v port p = %+v, present %t, want matching tree", tree, row, ok)
		}
	}
}
