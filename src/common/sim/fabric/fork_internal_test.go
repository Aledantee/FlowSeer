package fabric_test

import (
	"errors"
	"maps"
	"net/netip"
	"reflect"
	"slices"
	"testing"
	"time"
	"unsafe"

	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/assembly"

	hostmodel "go.aledante.io/FlowSeer/src/common/sim/device/host"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func defaultPhyAssumption() *cablemodel.PhyAssumption {
	return &cablemodel.PhyAssumption{
		Medium: cablemodel.TwistedPair,
		Ethernet: phy.Ethernet{
			SupportedSpeedsBPS:       []uint64{10_000_000, 100_000_000, 1_000_000_000},
			AutoNegotiationSupported: phy.CapabilitySupported,
			Setting:                  &phy.Setting{AutoNegotiation: true},
		},
	}
}

type forkClass int

const (
	classImmutableShared forkClass = iota
	classDeepCopied
	classResetOnFork
	classCopied
)

func (c forkClass) String() string {
	switch c {
	case classImmutableShared:
		return "immutableShared"
	case classDeepCopied:
		return "deepCopied"
	case classCopied:
		return "copied"
	case classResetOnFork:
		return "resetOnFork"
	default:
		return "unknown"
	}
}

func TestFabricFieldsAreClassifiedAndChecked(t *testing.T) {
	typ := reflect.TypeOf(fabric.Fabric{})
	fieldNames := make(map[string]bool, typ.NumField())

	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		fieldNames[f.Name] = true
		check, ok := fabricForkChecks[f.Name]
		class := check.class
		if !ok {
			t.Errorf("field %q on fabric.Fabric has no fork classification", f.Name)
			continue
		}
		if class == classDeepCopied {
			if check.probe == nil {
				t.Errorf("field %q is classified deepCopied but has no probe in fabricForkChecks", f.Name)
			}
		}
	}

	for name := range fabricForkChecks {
		if !fieldNames[name] {
			t.Errorf("fabricForkChecks names field %q which does not exist on fabric.Fabric", name)
		}
	}

	fab := newTestFabricForFork(t)
	*fabric.ExportFabricRuntimeEvidence(fab), _ = fabric.ExportFabricRuntimeEvidence(fab).Add(analysis.Evidence{Kind: "runtime", Context: "copy proof"})
	exportValue(reflect.ValueOf(fab).Elem().FieldByName("err")).Set(reflect.ValueOf(errors.New("sticky fault")))
	exportValue(reflect.ValueOf(fab).Elem().FieldByName("touched")).Set(reflect.ValueOf([]fabric.FrameID{1}))
	fork := fab.Fork()

	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		class := fabricForkChecks[f.Name].class
		vSrc := reflect.ValueOf(fab).Elem().Field(i)
		vDst := reflect.ValueOf(fork).Elem().Field(i)

		switch class {
		case classImmutableShared, classCopied:
			assertPointerOrValueIdentical(t, "Fabric."+f.Name, vSrc, vDst)
		case classResetOnFork:
			if !vDst.IsZero() {
				t.Errorf("field %q is classified resetOnFork but has non-zero value %+v on fork", f.Name, vDst.Interface())
			}
		case classDeepCopied:
			// Verified by running each probe below.
		}
	}

	checked := 0
	for _, name := range slices.Sorted(maps.Keys(fabricForkChecks)) {
		if probe := fabricForkChecks[name].probe; probe != nil {
			t.Run("probe_"+name, probe)
			checked++
		}
	}
	if checked != 18 {
		t.Fatalf("checked probes = %d, want 18", checked)
	}
}

func exportValue(v reflect.Value) reflect.Value {
	if !v.CanAddr() {
		return v
	}
	return reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem()
}

func assertPointerOrValueIdentical(t *testing.T, name string, src, dst reflect.Value) {
	t.Helper()
	src = exportValue(src)
	dst = exportValue(dst)
	switch src.Kind() {
	case reflect.Pointer, reflect.UnsafePointer:
		if src.Pointer() != dst.Pointer() {
			t.Errorf("%s: pointer not identical across fork: got %v and %v", name, src.Pointer(), dst.Pointer())
		}
	case reflect.Map, reflect.Slice:
		if src.Pointer() != dst.Pointer() {
			t.Errorf("%s: backing pointer not identical across fork: got %v and %v", name, src.Pointer(), dst.Pointer())
		}
	case reflect.Struct:
		typ := src.Type()
		hasPointers := false
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			fSrc := exportValue(src.Field(i))
			fDst := exportValue(dst.Field(i))
			if fSrc.Kind() == reflect.Map || fSrc.Kind() == reflect.Slice || fSrc.Kind() == reflect.Pointer {
				hasPointers = true
				if fSrc.Pointer() != fDst.Pointer() {
					t.Errorf("%s.%s: pointer not identical across fork: got %v and %v", name, f.Name, fSrc.Pointer(), fDst.Pointer())
				}
			}
		}
		if !hasPointers {
			if !reflect.DeepEqual(src.Interface(), dst.Interface()) {
				t.Errorf("%s: struct value differs across fork: got %+v, want %+v", name, dst.Interface(), src.Interface())
			}
		}
	default:
		if !reflect.DeepEqual(src.Interface(), dst.Interface()) {
			t.Errorf("%s: value differs across fork: got %+v, want %+v", name, dst.Interface(), src.Interface())
		}
	}
}

func newTestFabricForFork(t *testing.T) *fabric.Fabric {
	t.Helper()

	b1 := port.NewBuilder()
	b1.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b1.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b1.Add(port.Port{Name: "1/1/24", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	pTable1, err := b1.Build()
	if err != nil {
		t.Fatalf("build sw1 ports: %v", err)
	}

	b2 := port.NewBuilder()
	b2.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b2.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b2.Add(port.Port{Name: "1/1/24", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	pTable2, err := b2.Build()
	if err != nil {
		t.Fatalf("build sw2 ports: %v", err)
	}

	vid10 := vlan.ID(10)
	newBridgeCfg := func() *bridge.Config {
		return &bridge.Config{
			AgingTime: 300 * time.Second,
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "VLAN10"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1":  {PVID: &vid10, Untagged: []vlan.ID{10}},
					"1/1/2":  {PVID: &vid10, Untagged: []vlan.ID{10}},
					"1/1/24": {Tagged: []vlan.ID{10}},
				},
			},
		}
	}

	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}

	cfg := assembly.Config{
		Start: time.Unix(1000, 0),
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  pTable1,
				Bridge: newBridgeCfg(),
			},
			"sw2": {
				Ports:  pTable2,
				Bridge: newBridgeCfg(),
			},
		},
		Hosts: map[string]hostmodel.Config{
			"h1": {
				Address: macH1,
				IP: &hostmodel.IP{
					Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.7/24")},
				},
			},
			"h2": {Address: macH2},
		},
		Cables: []fabric.Cable{
			{
				A: fabric.Endpoint{Node: "h1"},
				B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			},
			{
				A: fabric.Endpoint{Node: "h2"},
				B: fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
			},
			{
				Cable: cablemodel.Cable{LengthMeters: 300},
				A:     fabric.Endpoint{Node: "sw1", Port: "1/1/24"},
				B:     fabric.Endpoint{Node: "sw2", Port: "1/1/24"},
			},
		},
		Uncabled: []fabric.Uncabled{
			{Endpoint: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}},
		},
		PhyAssumption: defaultPhyAssumption(),
	}

	fab, err := assembly.New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}
	(*fabric.

		// Populate runtime maps and slices for non-empty pointer checks
		ExportFabricClock(fab)) = time.Unix(1500, 0)
	(*fabric.ExportFabricStepped(fab)) = true
	(*fabric.ExportFabricNextFrameID(fab)) = 10
	(*fabric.ExportFabricNextSeq(fab)) = 20
	(*fabric.ExportFabricQueue(fab)) = []fabric.Arrival{fabric.MakeTestArrival(fabric.ExportArrivalFields{At: fabric.ExportFabricClock(fab).Add(time.Millisecond), Device: "sw1", Port: "1/1/1", Seq: 1})}
	(*fabric.ExportFabricWakes(fab)) = map[string]time.Time{
		"sw1": fabric.ExportFabricClock(fab).Add(time.Second),
	}
	(*fabric.ExportFabricJourneys(fab)) = map[fabric.FrameID]*fabric.ExportJourney{
		1: fabric.MakeTestJourneyPointer(fabric.ExportJourneyFields{FrameID: 1, Injection: fabric.Injection{At: (*fabric.ExportFabricClock(fab))}}),
	}
	(*fabric.ExportFabricEntered(fab)) = map[fabric.FrameID]map[fabric.Endpoint]bool{
		1: {{Node: "sw1", Port: "1/1/1"}: true},
	}
	(*fabric.ExportFabricCableCrossings(fab)) = map[fabric.Endpoint]uint{
		{Node: "sw1", Port: "1/1/1"}: 1,
	}
	(*fabric.ExportFabricHeld(fab)) = map[fabric.ExportHoldKey]fabric.FrameID{
		fabric.MakeTestHoldKey("sw1", 1): 5,
	}
	(*fabric.ExportFabricBusyUntil(fab)) = map[fabric.Endpoint]time.Time{
		{Node: "sw1", Port: "1/1/1"}: fabric.ExportFabricClock(fab).Add(10 * time.Millisecond),
	}
	(*fabric.ExportFabricEgress(fab)) = map[fabric.Endpoint]*fabric.ExportEgressQueue{
		{Node: "sw1", Port: "1/1/1"}: fabric.MakeTestEgressQueuePointer(fabric.ExportEgressQueueFields{
			Pending: [8][]fabric.ExportQueued{
				{fabric.MakeTestQueued(fabric.ExportQueuedFields{Seq: 1, Fid: 1, Enqueued: (*fabric.ExportFabricClock(fab))})},
			},
		}),
	}
	(*fabric.ExportFabricCounters(fab)) = map[fabric.Endpoint]*fabric.Counters{
		{Node: "sw1", Port: "1/1/1"}: {InBytes: 100},
	}

	return fab
}
