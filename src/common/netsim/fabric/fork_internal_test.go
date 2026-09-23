package fabric

import (
	"net/netip"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

func defaultPhyAssumption() *PhyAssumption {
	return &PhyAssumption{
		Medium: TwistedPair,
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
)

func (c forkClass) String() string {
	switch c {
	case classImmutableShared:
		return "immutableShared"
	case classDeepCopied:
		return "deepCopied"
	case classResetOnFork:
		return "resetOnFork"
	default:
		return "unknown"
	}
}

var fabricFieldClasses = map[string]forkClass{
	"cfg":            classDeepCopied,
	"evidence":       classImmutableShared,
	"links":          classDeepCopied,
	"linkTrust":      classDeepCopied,
	"uncabled":       classDeepCopied,
	"switches":       classDeepCopied,
	"hostStacks":     classDeepCopied,
	"byEnd":          classDeepCopied,
	"clock":          classImmutableShared,
	"stepped":        classImmutableShared,
	"queue":          classDeepCopied,
	"wakes":          classDeepCopied,
	"dequeueItems":   classDeepCopied,
	"wakeItems":      classDeepCopied,
	"nextFrameID":    classImmutableShared,
	"nextSeq":        classImmutableShared,
	"journeys":       classDeepCopied,
	"entered":        classDeepCopied,
	"cableCrossings": classDeepCopied,
	"busyUntil":      classDeepCopied,
	"egress":         classDeepCopied,
	"counters":       classDeepCopied,
	"metadataCache":  classImmutableShared,
	"err":            classImmutableShared,
}

func TestFabricFieldsAreClassifiedAndChecked(t *testing.T) {
	typ := reflect.TypeOf(Fabric{})
	fieldNames := make(map[string]bool, typ.NumField())

	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		fieldNames[f.Name] = true
		class, ok := fabricFieldClasses[f.Name]
		if !ok {
			t.Errorf("field %q on fabric.Fabric has no fork classification", f.Name)
			continue
		}
		if class == classDeepCopied {
			if _, hasProbe := fabricDeepCopiedProbes[f.Name]; !hasProbe {
				t.Errorf("field %q is classified deepCopied but has no probe in fabricDeepCopiedProbes", f.Name)
			}
		}
	}

	for name := range fabricFieldClasses {
		if !fieldNames[name] {
			t.Errorf("fabricFieldClasses names field %q which does not exist on fabric.Fabric", name)
		}
	}

	fab := newTestFabricForFork(t)
	fork := fab.Fork()

	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		class := fabricFieldClasses[f.Name]
		vSrc := reflect.ValueOf(fab).Elem().Field(i)
		vDst := reflect.ValueOf(fork).Elem().Field(i)

		switch class {
		case classImmutableShared:
			assertPointerOrValueIdentical(t, "Fabric."+f.Name, vSrc, vDst)
		case classResetOnFork:
			if !vDst.IsZero() {
				t.Errorf("field %q is classified resetOnFork but has non-zero value %+v on fork", f.Name, vDst.Interface())
			}
		case classDeepCopied:
			// Verified by running each probe below.
		}
	}

	for name, probe := range fabricDeepCopiedProbes {
		t.Run("probe_"+name, probe)
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

func newTestFabricForFork(t *testing.T) *Fabric {
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

	cfg := Config{
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
		Hosts: map[string]Host{
			"h1": {
				Address: macH1,
				IP: &HostIP{
					Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.7/24")},
				},
			},
			"h2": {Address: macH2},
		},
		Cables: []Cable{
			{
				A: Endpoint{Node: "h1"},
				B: Endpoint{Node: "sw1", Port: "1/1/1"},
			},
			{
				A: Endpoint{Node: "h2"},
				B: Endpoint{Node: "sw2", Port: "1/1/1"},
			},
			{
				A:            Endpoint{Node: "sw1", Port: "1/1/24"},
				B:            Endpoint{Node: "sw2", Port: "1/1/24"},
				LengthMeters: 300,
			},
		},
		Uncabled: []Uncabled{
			{Endpoint: Endpoint{Node: "sw1", Port: "1/1/2"}},
		},
		PhyAssumption: defaultPhyAssumption(),
	}

	fab, err := New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	// Populate runtime maps and slices for non-empty pointer checks
	fab.clock = time.Unix(1500, 0)
	fab.stepped = true
	fab.nextFrameID = 10
	fab.nextSeq = 20

	fab.queue = []Arrival{
		{At: fab.clock.Add(time.Millisecond), Device: "sw1", Port: "1/1/1", Seq: 1},
	}
	fab.wakes = map[string]time.Time{
		"sw1": fab.clock.Add(time.Second),
	}
	fab.journeys = map[FrameID]*Journey{
		1: {FrameID: 1, Injection: Injection{At: fab.clock}},
	}
	fab.entered = map[FrameID]map[Endpoint]bool{
		1: {{Node: "sw1", Port: "1/1/1"}: true},
	}
	fab.cableCrossings = map[Endpoint]uint{
		{Node: "sw1", Port: "1/1/1"}: 1,
	}
	fab.busyUntil = map[Endpoint]time.Time{
		{Node: "sw1", Port: "1/1/1"}: fab.clock.Add(10 * time.Millisecond),
	}
	fab.egress = map[Endpoint]*egressQueue{
		{Node: "sw1", Port: "1/1/1"}: {
			pending: [8][]queued{
				{{seq: 1, fid: 1, enqueued: fab.clock}},
			},
		},
	}
	fab.counters = map[Endpoint]*Counters{
		{Node: "sw1", Port: "1/1/1"}: {InOctets: 100},
	}

	return fab
}
