package vswitch

import (
	"fmt"
	"net/netip"
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

// TestStateConvertersCopyEveryField fills every exported field of a layer row with a distinct
// nonzero value and compares what each hand-written converter returns with it field by field,
// so a converter that leaves a field at its zero value or copies it from the wrong source fails.
func TestStateConvertersCopyEveryField(t *testing.T) {
	cases := []struct {
		name    string
		layer   any
		convert func(any) any
	}{
		{"bridge.Entry", bridge.Entry{}, func(v any) any { return stateEntry(v.(bridge.Entry)) }},
		{"mcast.Entry", mcast.Entry{}, func(v any) any { return stateGroup(v.(mcast.Entry)) }},
		{"mcast.RouterPort", mcast.RouterPort{}, func(v any) any { return stateRouterPort(v.(mcast.RouterPort)) }},
		{"routing.NeighborEntry", routing.NeighborEntry{}, func(v any) any { return stateNeighbor(v.(routing.NeighborEntry)) }},
		{"stp.PortInfo", stp.PortInfo{}, func(v any) any { return statePortInfo(v.(stp.PortInfo)) }},
		{"phy.Allocation", phy.Allocation{}, func(v any) any { return stateAllocation(v.(phy.Allocation)) }},
	}
	reached := 0
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := reflect.New(reflect.TypeOf(tc.layer)).Elem()
			next := 0
			fillDistinct(t, in, &next)
			compareConverted(t, tc.name, in, reflect.ValueOf(tc.convert(in.Interface())))
			reached++
		})
	}
	if reached != 6 {
		t.Fatalf("converters reaching expectations = %d, want 6", reached)
	}
}

// fillDistinct sets every exported field under v to a nonzero value that no other field under
// the same root shares, descending into structs, slices, and maps.
func fillDistinct(t *testing.T, v reflect.Value, next *int) {
	t.Helper()
	*next++
	n := *next
	switch v.Type() {
	case reflect.TypeFor[time.Time]():
		v.Set(reflect.ValueOf(time.Unix(int64(n)*1000, 0).UTC()))
		return
	case reflect.TypeFor[netip.Addr]():
		v.Set(reflect.ValueOf(netip.AddrFrom4([4]byte{10, 0, 0, byte(n)})))
		return
	}
	switch v.Kind() {
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(int64(n))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(uint64(n))
	case reflect.String:
		v.SetString(fmt.Sprintf("value-%d", n))
	case reflect.Array:
		for i := range v.Len() {
			fillDistinct(t, v.Index(i), next)
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				fillDistinct(t, v.Field(i), next)
			}
		}
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		fillDistinct(t, v.Index(0), next)
	case reflect.Map:
		v.Set(reflect.MakeMap(v.Type()))
		key := reflect.New(v.Type().Key()).Elem()
		fillDistinct(t, key, next)
		elem := reflect.New(v.Type().Elem()).Elem()
		fillDistinct(t, elem, next)
		v.SetMapIndex(key, elem)
	default:
		t.Fatalf("cannot fill a %s", v.Type())
	}
}

// compareConverted checks that every exported field of layer reaches the same-named field of
// seam with the same value.
func compareConverted(t *testing.T, path string, layer, seam reflect.Value) {
	t.Helper()
	if layer.Type() == seam.Type() {
		if !reflect.DeepEqual(layer.Interface(), seam.Interface()) {
			t.Errorf("%s = %v, want %v", path, seam.Interface(), layer.Interface())
		}
		return
	}
	switch layer.Kind() {
	case reflect.Struct:
		for i := range layer.NumField() {
			field := layer.Type().Field(i)
			if !field.IsExported() {
				continue
			}
			target := seam.FieldByName(field.Name)
			if !target.IsValid() {
				t.Errorf("%s.%s has no field in %s", path, field.Name, seam.Type())
				continue
			}
			compareConverted(t, path+"."+field.Name, layer.Field(i), target)
		}
	case reflect.Slice:
		if layer.Len() != seam.Len() {
			t.Errorf("%s has %d elements, want %d", path, seam.Len(), layer.Len())
			return
		}
		for i := range layer.Len() {
			compareConverted(t, fmt.Sprintf("%s[%d]", path, i), layer.Index(i), seam.Index(i))
		}
	case reflect.Map:
		if layer.Len() != seam.Len() {
			t.Errorf("%s has %d entries, want %d", path, seam.Len(), layer.Len())
			return
		}
		for _, key := range layer.MapKeys() {
			got := seam.MapIndex(key.Convert(seam.Type().Key()))
			if !got.IsValid() {
				t.Errorf("%s has no entry %v", path, key)
				continue
			}
			compareConverted(t, fmt.Sprintf("%s[%v]", path, key), layer.MapIndex(key), got)
		}
	case reflect.String:
		if layer.String() != seam.String() {
			t.Errorf("%s = %q, want %q", path, seam.String(), layer.String())
		}
	case reflect.Bool:
		if layer.Bool() != seam.Bool() {
			t.Errorf("%s = %v, want %v", path, seam.Bool(), layer.Bool())
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if layer.Int() != seam.Int() {
			t.Errorf("%s = %d, want %d", path, seam.Int(), layer.Int())
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if layer.Uint() != seam.Uint() {
			t.Errorf("%s = %d, want %d", path, seam.Uint(), layer.Uint())
		}
	default:
		t.Errorf("%s: cannot compare a %s with a %s", path, layer.Type(), seam.Type())
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
