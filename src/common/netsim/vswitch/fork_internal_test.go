package vswitch

import (
	"net/netip"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/lag"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/loopprotect"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/mcast"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
)

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

var switchFieldClasses = map[string]forkClass{
	"cfg":                      classImmutableShared,
	"ports":                    classImmutableShared,
	"bridge":                   classDeepCopied,
	"speeds":                   classImmutableShared,
	"power":                    classImmutableShared,
	"stp":                      classDeepCopied,
	"loopprotect":              classDeepCopied,
	"lag":                      classDeepCopied,
	"mcast":                    classDeepCopied,
	"routing":                  classDeepCopied,
	"traffic":                  classImmutableShared,
	"buckets":                  classDeepCopied,
	"copies":                   classDeepCopied,
	"emissions":                classDeepCopied,
	"portP2P":                  classDeepCopied,
	"portSpeed":                classDeepCopied,
	"protocolIssues":           classDeepCopied,
	"seeds":                    classDeepCopied,
	"nodeID":                   classImmutableShared,
	"metadata":                 classImmutableShared,
	"missingSTP":               classImmutableShared,
	"operErr":                  classDeepCopied,
	"lagRebalanceHits":         classResetOnFork,
	"mcastQueryUnobservedHits": classResetOnFork,
	"neighborUnresolvedHits":   classResetOnFork,
	"pvstBoundaryHits":         classResetOnFork,
	"neighborFailures":         classDeepCopied,
}

func TestSwitchFieldsAreClassifiedAndChecked(t *testing.T) {
	typ := reflect.TypeOf(Switch{})
	seen := make(map[string]bool, typ.NumField())

	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		seen[f.Name] = true
		class, ok := switchFieldClasses[f.Name]
		if !ok {
			t.Errorf("field %q has no classification in switchFieldClasses", f.Name)
			continue
		}
		if class == classDeepCopied {
			if _, hasProbe := switchDeepCopiedProbes[f.Name]; !hasProbe {
				t.Errorf("deepCopied field %q has no probe in switchDeepCopiedProbes", f.Name)
			}
		}
	}

	for name := range switchFieldClasses {
		if !seen[name] {
			t.Errorf("switchFieldClasses names %q, which Switch no longer has", name)
		}
	}

	// Now check classes on an actual fully-populated Switch instance and its Fork.
	sw := newTestSwitchForFork(t)
	fork := sw.Fork()

	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		class := switchFieldClasses[f.Name]
		vSrc := reflect.ValueOf(sw).Elem().Field(i)
		vDst := reflect.ValueOf(fork).Elem().Field(i)

		switch class {
		case classImmutableShared:
			assertPointerOrValueIdentical(t, "Switch."+f.Name, vSrc, vDst)
		case classResetOnFork:
			if !vDst.IsZero() {
				t.Errorf("field %q is classified resetOnFork but has non-zero value %+v on fork", f.Name, vDst.Interface())
			}
		case classDeepCopied:
			// Verified by running each probe below.
		}
	}

	for name, probe := range switchDeepCopiedProbes {
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

func newTestSwitchForFork(t *testing.T) *Switch {
	t.Helper()

	p1 := port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}
	p2 := port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}
	lagPort := port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}
	lagM1 := port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"}
	lagM2 := port.Port{Name: "1/1/4", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"}

	b := port.NewBuilder()
	b.Add(p1)
	b.Add(p2)
	b.Add(lagPort)
	b.Add(lagM1)
	b.Add(lagM2)
	tbl, err := b.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	vid10 := vlan.ID(10)
	spec := ConstructionSpec{
		NodeID: "sw1",
		Config: Config{
			MAC:   mac,
			Ports: tbl,
			Bridge: &bridge.Config{
				AgingTime: 300 * time.Second,
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
						"1/1/2": {PVID: &vid10, Untagged: []vlan.ID{10}},
						"lag1":  {PVID: &vid10, Untagged: []vlan.ID{10}},
					},
				},
			},
			STP: &stp.Config{Priority: 32768, Address: mac},
			LoopProtect: &loopprotect.Config{
				Interval: 5 * time.Second,
				Ports: map[string]loopprotect.Port{
					"1/1/1": {Action: loopprotect.Block, VLANs: []vlan.ID{10}},
				},
			},
			LAG: &lag.Config{
				LAGs: map[string]lag.LAG{
					"lag1": {Members: map[string]lag.Member{"1/1/3": {}, "1/1/4": {}}},
				},
			},
			Mcast: &mcast.Config{
				VLANs: map[vlan.ID]mcast.VLANSnooping{
					10: {},
				},
			},
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					routing.DefaultVRF: {
						Interfaces: map[string]routing.Interface{
							"vlan10": {VLAN: 10, MAC: mac, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						},
						Neighbors: []routing.Neighbor{
							{Interface: "vlan10", Addr: netip.MustParseAddr("10.0.10.2"), MAC: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}},
						},
					},
				},
			},
			Traffic: &traffic.Config{
				Policers: map[string]traffic.Policer{
					"1/1/1": {RateBPS: 1000, BurstOctets: 500},
				},
			},
			Phy: &phy.Config{
				Ethernet: map[string]phy.Ethernet{
					"1/1/1": {
						SupportedSpeedsBPS: []uint64{1_000_000_000},
						Setting:            &phy.Setting{SpeedBPS: 1_000_000_000, AutoNegotiation: false, Duplex: phy.Full},
					},
				},
			},
		},
		Seeds: []bridge.Seed{
			{FID: 10, MAC: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x99}, Port: "lag1", Lifetime: bridge.Static},
		},
		Metadata: analysis.NewMetadata(analysis.NodeScope("sw1"), nil, analysis.EvidenceCatalog{}, nil),
	}

	sw, err := NewWithSpec(spec)
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}

	// Populate runtime maps so they are non-empty for the pointer check.
	sw.portP2P = map[string]PointToPoint{"1/1/1": PointToPointTrue}
	sw.portSpeed = map[string]uint64{"1/1/1": 1_000_000_000}
	sw.protocolIssues = map[string]analysis.Issue{"1/1/1": {Code: "test-issue"}}
	sw.copies = []traffic.Copy{{Port: "1/1/2"}}
	sw.emissions = []Emission{{Port: "1/1/1"}}
	sw.neighborFailures = []NeighborDrop{{Port: "1/1/1", Reason: "held-test"}}

	// Populate transient hit sets on sw to verify resetOnFork clears them.
	sw.lagRebalanceHits = map[string]lag.Selection{"lag1": {Member: "1/1/3"}}
	sw.mcastQueryUnobservedHits = []mcastQueryUnobservedHit{{vid: 10, group: netip.MustParseAddr("224.0.0.1")}}
	sw.neighborUnresolvedHits = []neighborUnresolvedHit{{iface: "vlan10", addr: netip.MustParseAddr("10.0.10.99")}}
	sw.pvstBoundaryHits = map[pvstBoundaryHit]struct{}{{port: "1/1/1", vid: 10}: {}}

	return sw
}
