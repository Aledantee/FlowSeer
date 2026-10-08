package vswitch

import (
	"net/netip"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"go.aledante.io/FlowSeer/src/common/net/arp"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/filter"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/loopprotect"
	"go.aledante.io/FlowSeer/src/common/sim/layer/mcast"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
	"go.aledante.io/FlowSeer/src/common/sim/port"
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

type forkFieldClass struct {
	class      forkClass
	zeroReason string
}

var switchFieldClasses = map[string]forkFieldClass{
	"call":               {class: classResetOnFork, zeroReason: "The active call is nil between public entries."},
	"cfg":                {class: classImmutableShared},
	"ports":              {class: classImmutableShared},
	"bridge":             {class: classDeepCopied},
	"speeds":             {class: classImmutableShared},
	"power":              {class: classImmutableShared},
	"stp":                {class: classDeepCopied},
	"loopprotect":        {class: classDeepCopied},
	"lag":                {class: classDeepCopied},
	"mcast":              {class: classDeepCopied},
	"routing":            {class: classDeepCopied},
	"filter":             {class: classDeepCopied},
	"traffic":            {class: classDeepCopied},
	"trafficSwitchports": {class: classImmutableShared},
	"portP2P":            {class: classDeepCopied},
	"portSpeed":          {class: classDeepCopied},
	"protocolIssues":     {class: classDeepCopied},
	"seeds":              {class: classDeepCopied},
	"nodeID":             {class: classImmutableShared},
	"metadata":           {class: classImmutableShared},
	"missingSTP":         {class: classImmutableShared},
	"retention":          {class: classDeepCopied},
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
		if class.class == classDeepCopied {
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

	sw := newTestSwitchForFork(t)
	spec := sw.Spec()
	spec.Config.STP = nil
	spec.Metadata = analysis.NewMetadata(analysis.NodeScope("sw1"), []analysis.Issue{{Code: "missing-stp", Scope: protocolScope("sw1", stp.LayerName), Status: analysis.Incomplete}}, analysis.EvidenceCatalog{}, nil)
	missingSTP, err := NewWithSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	fixtures := []*Switch{sw, missingSTP}
	populated := make(map[string]bool)

	for _, source := range fixtures {
		fork := source.Fork()
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			class := switchFieldClasses[f.Name].class
			vSrc := reflect.ValueOf(source).Elem().Field(i)
			vDst := reflect.ValueOf(fork).Elem().Field(i)
			populated[f.Name] = populated[f.Name] || !vSrc.IsZero()

			switch class {
			case classImmutableShared:
				assertPointerOrValueIdentical(t, "Switch."+f.Name, vSrc, vDst)
			case classResetOnFork:
				if !vDst.IsZero() {
					t.Errorf("field %q is classified resetOnFork but has non-zero value %+v on fork", f.Name, exportValue(vDst).Interface())
				}
			case classDeepCopied:
				if vSrc.Kind() == reflect.Pointer && !vSrc.IsNil() && (vDst.IsNil() || vSrc.Pointer() == vDst.Pointer()) {
					t.Errorf("field %q must have its own non-nil clone", f.Name)
				}
			}
		}
	}
	for name := range switchFieldClasses {
		if !populated[name] && switchFieldClasses[name].zeroReason == "" {
			t.Errorf("field %q is zero in every fixture", name)
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
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if !f.IsExported() {
				continue
			}
			fSrc, fDst := src.Field(i), dst.Field(i)
			if fSrc.Kind() == reflect.Map || fSrc.Kind() == reflect.Slice || fSrc.Kind() == reflect.Pointer {
				if fSrc.Pointer() != fDst.Pointer() {
					t.Errorf("%s.%s: pointer differs across fork", name, f.Name)
				}
			}
		}
		if !reflect.DeepEqual(src.Interface(), dst.Interface()) {
			t.Errorf("%s: struct value differs across fork: got %+v, want %+v", name, dst.Interface(), src.Interface())
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
	lagPort := port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}
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
			STP: &stp.Config{
				Priority: 32768,
				Address:  mac,
				Ports: map[string]stp.Port{
					"1/1/1": {AdminEdge: true},
					"1/1/2": {AdminEdge: true},
				},
			},
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
			Filter: &filter.Config{
				Sets: map[string]filter.RuleSet{"ingress": {
					Default: filter.Accept,
					Rules: []filter.Rule{{
						Name: "blocked-source", Action: filter.Drop,
						Match: filter.Match{Src: []netip.Prefix{netip.MustParsePrefix("10.0.10.51/32")}},
					}},
				}},
				Bindings: []filter.Binding{{Interface: "vlan10", Direction: filter.In, Set: "ingress"}},
			},
			Phy: &phy.Config{
				PoE: &phy.PoE{
					Groups: map[string]phy.Group{"pse": {PowerNanowatts: 30_000_000_000}},
					Ports:  map[string]phy.PsePort{"1/1/1": {Group: "pse", MaxClass: 4, Enabled: true, PD: phy.PDAttached, PDClass: phy.Class(1)}},
				},
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
	if sw.filter == nil {
		t.Fatal("newTestSwitchForFork produced nil filter")
	}
	if _, err := sw.LinkChange(time.Unix(1000, 0), "1/1/1", port.Up, PointToPointFalse, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := sw.LinkChange(time.Unix(1000, 0), "1/1/2", port.Up, PointToPointFalse, 0); err !=

		// Populate runtime maps so they are non-empty for the pointer check.
		nil {
		t.Fatal(err)
	}

	sw.portP2P = map[string]PointToPoint{"1/1/1": PointToPointTrue}
	sw.portSpeed = map[string]uint64{"1/1/1": 1_000_000_000}
	sw.protocolIssues = map[string]analysis.Issue{"1/1/1": {Code: "test-issue"}}
	sw.retention = allKeptRetention()

	frame, err := arp.Encode(arp.Message{
		HardwareType: 1, ProtocolType: 0x0800, Operation: arp.Reply,
		SenderMAC: netaddr.MAC{0, 0xaa, 0xbb, 0xcc, 0xdd, 0xef}, SenderAddr: netip.MustParseAddr("10.0.10.98"),
		TargetMAC: mac, TargetAddr: netip.MustParseAddr("10.0.10.1"),
	}, mac)
	if err != nil {
		t.Fatal(err)
	}
	sw.Receive(time.Unix(1000, 0), "1/1/1", frame)
	found := false
	for _, n := range sw.Neighbors() {
		found = found || n.Addr == netip.MustParseAddr("10.0.10.98")
	}
	if !found {
		t.Fatal("source did not learn runtime neighbor")
	}
	spent := ethernet.Frame{Payload: make([]byte, 400-38)}
	sw.Receive(time.Unix(1000, 0), "1/1/1", spent)
	if got := sw.Receive(time.Unix(1000, 0), "1/1/1", spent); got.Reason != traffic.ReasonPoliced {
		t.Fatalf("spent source bucket reason = %v, want %v", got.Reason, traffic.ReasonPoliced)
	}

	return sw
}
