package bridge

import (
	"reflect"
	"testing"
	"time"
	"unsafe"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
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

var bridgeFieldClasses = map[string]forkClass{
	"cfg":           classImmutableShared,
	"ports":         classImmutableShared,
	"agingTime":     classImmutableShared,
	"fdbScope":      classImmutableShared,
	"selectorScope": classImmutableShared,
	"resolverScope": classImmutableShared,
	"fdb":           classDeepCopied,
	"gates":         classDeepCopied,
	"counters":      classDeepCopied,
	"dynamic":       classDeepCopied,
	"selector":      classResetOnFork,
	"resolver":      classResetOnFork,
}

var bridgeDeepCopiedProbes = map[string]func(t *testing.T){
	"fdb":      probeBridgeFDB,
	"gates":    probeBridgeGates,
	"counters": probeBridgeCounters,
	"dynamic":  probeBridgeDynamic,
}

func TestBridgeFieldsAreClassifiedAndChecked(t *testing.T) {
	typ := reflect.TypeOf(Bridge{})
	seen := make(map[string]bool, typ.NumField())

	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		seen[f.Name] = true
		class, ok := bridgeFieldClasses[f.Name]
		if !ok {
			t.Errorf("field %q has no classification in bridgeFieldClasses", f.Name)
			continue
		}
		if class == classDeepCopied {
			if _, hasProbe := bridgeDeepCopiedProbes[f.Name]; !hasProbe {
				t.Errorf("deepCopied field %q has no probe in bridgeDeepCopiedProbes", f.Name)
			}
		}
	}

	for name := range bridgeFieldClasses {
		if !seen[name] {
			t.Errorf("bridgeFieldClasses names %q, which Bridge no longer has", name)
		}
	}

	// Now check classes on an actual instance and its clone.
	b := newTestBridgeForFork(t)
	fork := b.Clone()

	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		class := bridgeFieldClasses[f.Name]
		vSrc := reflect.ValueOf(b).Elem().Field(i)
		vDst := reflect.ValueOf(fork).Elem().Field(i)

		switch class {
		case classImmutableShared:
			assertPointerOrValueIdentical(t, "Bridge."+f.Name, vSrc, vDst)
		case classResetOnFork:
			if !vDst.IsZero() {
				t.Errorf("field %q is classified resetOnFork but has non-zero value %+v on clone", f.Name, vDst.Interface())
			}
		case classDeepCopied:
			// Verified below by running the probe.
		}
	}

	// Run every deepCopied probe.
	for name, probe := range bridgeDeepCopiedProbes {
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
			t.Errorf("%s: pointer not identical across clone: got %v and %v", name, src.Pointer(), dst.Pointer())
		}
	case reflect.Map, reflect.Slice:
		if src.Pointer() != dst.Pointer() {
			t.Errorf("%s: backing pointer not identical across clone: got %v and %v", name, src.Pointer(), dst.Pointer())
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
					t.Errorf("%s.%s: pointer not identical across clone: got %v and %v", name, f.Name, fSrc.Pointer(), fDst.Pointer())
				}
			}
		}
		if !hasPointers {
			if !reflect.DeepEqual(src.Interface(), dst.Interface()) {
				t.Errorf("%s: struct value differs across clone: got %+v, want %+v", name, dst.Interface(), src.Interface())
			}
		}
	default:
		if !reflect.DeepEqual(src.Interface(), dst.Interface()) {
			t.Errorf("%s: value differs across clone: got %+v, want %+v", name, dst.Interface(), src.Interface())
		}
	}
}

type testGate struct {
	learns   bool
	forwards bool
}

func (g testGate) Learns(string, vlan.ID) bool   { return g.learns }
func (g testGate) Forwards(string, vlan.ID) bool { return g.forwards }

type testSelector struct{}

func (testSelector) Select(time.Time, string, bool, ethernet.Frame, vlan.ID) Selection {
	return Selection{Member: "1/1/1", Cause: "primary"}
}

type testResolver struct{}

func (r testResolver) Resolve(time.Time, vlan.ID, ethernet.Frame) ([]string, bool) {
	return nil, false
}

func newTestBridgeForFork(t *testing.T) *Bridge {
	t.Helper()
	p1 := port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}
	p2 := port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}
	b := port.NewBuilder()
	b.Add(p1)
	b.Add(p2)
	tbl, err := b.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	cfg := Config{
		AgingTime: 300 * time.Second,
		VLAN: &VLAN{
			Table: map[vlan.ID]string{10: "vlan10"},
			Switchports: map[string]Switchport{
				"1/1/1": {Untagged: []vlan.ID{10}},
				"1/1/2": {Untagged: []vlan.ID{10}},
			},
		},
	}
	br, err := New(cfg, tbl)
	if err != nil {
		t.Fatalf("New bridge: %v", err)
	}
	br.SetFDBScope(analysis.ProtocolScope("sw1", string(port.LayerRelay), "0"))
	br.SetSelector(testSelector{}, analysis.ProtocolScope("sw1", string(port.LayerLag), "0"))
	br.SetGroupResolver(testResolver{}, analysis.ProtocolScope("sw1", string(port.LayerMcast), "0"))
	br.SetGate(testGate{learns: true, forwards: true}, analysis.ProtocolScope("sw1", string(port.LayerStp), "0"))

	// Learn an entry so dynamic and fdb are non-empty.
	frame := ethernet.Frame{Src: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}, Dst: netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}}
	br.Ingress(time.Unix(1000, 0), "1/1/1", frame, true, true)

	return br
}

func probeBridgeFDB(t *testing.T) {
	b := newTestBridgeForFork(t)
	fork := b.Clone()

	macSrc := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}
	macFork := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	// Mutate source through exported Ingress
	fSrc := ethernet.Frame{Src: macSrc, Dst: netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}}
	b.Ingress(time.Unix(1010, 0), "1/1/1", fSrc, true, true)

	if _, ok := fork.fdb[fdbKey{mac: macSrc}]; ok {
		t.Errorf("mutation on source FDB visible on fork")
	}

	// Mutate fork through exported Ingress
	fFork := ethernet.Frame{Src: macFork, Dst: netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}}
	fork.Ingress(time.Unix(1010, 0), "1/1/2", fFork, true, true)

	if _, ok := b.fdb[fdbKey{mac: macFork}]; ok {
		t.Errorf("mutation on fork FDB visible on source")
	}
}

func probeBridgeGates(t *testing.T) {
	b := newTestBridgeForFork(t)
	fork := b.Clone()

	scopeSrc := analysis.ProtocolScope("sw1", "test-src", "0")
	scopeFork := analysis.ProtocolScope("sw1", "test-fork", "0")

	b.SetGate(testGate{learns: false, forwards: false}, scopeSrc)
	for _, g := range fork.gates {
		if g.scope.Compare(scopeSrc) == 0 {
			t.Errorf("gate added on source visible on fork")
		}
	}

	fork.SetGate(testGate{learns: false, forwards: false}, scopeFork)
	for _, g := range b.gates {
		if g.scope.Compare(scopeFork) == 0 {
			t.Errorf("gate added on fork visible on source")
		}
	}
}

func probeBridgeCounters(t *testing.T) {
	b := newTestBridgeForFork(t)
	fork := b.Clone()

	cSrcBefore := b.Counters()
	cForkBefore := fork.Counters()

	// Mutate source by learning a new seed
	seedSrc := Seed{FID: 10, MAC: netaddr.MAC{0x00, 0x55, 0x66, 0x77, 0x88, 0x01}, Port: "1/1/1", Lifetime: Aging}
	if err := b.Learn([]Seed{seedSrc}); err != nil {
		t.Fatalf("b.Learn: %v", err)
	}

	if fork.Counters() != cForkBefore {
		t.Errorf("source learning changed fork counters")
	}
	if b.Counters() == cSrcBefore {
		t.Errorf("source learning did not increment source counters")
	}

	// Mutate fork by learning a new seed
	seedFork := Seed{FID: 10, MAC: netaddr.MAC{0x00, 0x55, 0x66, 0x77, 0x88, 0x02}, Port: "1/1/2", Lifetime: Aging}
	if err := fork.Learn([]Seed{seedFork}); err != nil {
		t.Fatalf("fork.Learn: %v", err)
	}

	if b.Counters().Learned != cSrcBefore.Learned+1 {
		t.Errorf("fork learning changed source counters: got %d, want %d", b.Counters().Learned, cSrcBefore.Learned+1)
	}
	if fork.Counters().Learned != cForkBefore.Learned+1 {
		t.Errorf("fork learning did not increment fork counters")
	}
}

func probeBridgeDynamic(t *testing.T) {
	b := newTestBridgeForFork(t)
	fork := b.Clone()

	dynSrcBefore := b.dynamic
	dynForkBefore := fork.dynamic

	// Mutate source: learn new seed
	seedSrc := Seed{FID: 10, MAC: netaddr.MAC{0x00, 0x99, 0x88, 0x77, 0x66, 0x01}, Port: "1/1/1", Lifetime: Aging}
	if err := b.Learn([]Seed{seedSrc}); err != nil {
		t.Fatalf("b.Learn: %v", err)
	}

	if fork.dynamic != dynForkBefore {
		t.Errorf("source learning changed fork dynamic count")
	}
	if b.dynamic == dynSrcBefore {
		t.Errorf("source learning did not change source dynamic count")
	}

	// Mutate fork: learn new seed
	seedFork := Seed{FID: 10, MAC: netaddr.MAC{0x00, 0x99, 0x88, 0x77, 0x66, 0x02}, Port: "1/1/2", Lifetime: Aging}
	if err := fork.Learn([]Seed{seedFork}); err != nil {
		t.Fatalf("fork.Learn: %v", err)
	}

	if b.dynamic != dynSrcBefore+1 {
		t.Errorf("fork learning changed source dynamic count")
	}
	if fork.dynamic != dynForkBefore+1 {
		t.Errorf("fork learning did not change fork dynamic count")
	}
}
