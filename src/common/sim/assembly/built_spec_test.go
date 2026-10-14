package assembly_test

import (
	"net/netip"
	"reflect"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/assembly"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	hostmodel "go.aledante.io/FlowSeer/src/common/sim/device/host"
	reflectormodel "go.aledante.io/FlowSeer/src/common/sim/device/reflector"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func builtSpecFixture(t *testing.T) assembly.ConstructionSpec {
	t.Helper()
	ports, err := port.NewBuilder().
		Add(port.Port{Name: "h", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Down}).
		Add(port.Port{Name: "r", Kind: port.Physical, AdminStatus: port.Up}).
		Add(port.Port{Name: "empty", Kind: port.Physical, AdminStatus: port.Up}).Build()
	if err != nil {
		t.Fatal(err)
	}
	vid := vlan.ID(10)
	catalog, _ := analysis.EvidenceCatalog{}.Add(analysis.Evidence{Kind: "inventory", Origin: "unused inventory observation"})
	return assembly.ConstructionSpec{
		Start: time.Unix(1700000000, 123).UTC(),
		Switches: map[string]vswitch.ConstructionSpec{"sw": {
			NodeID: "sw", Config: vswitch.Config{Ports: ports, Bridge: &bridge.Config{}},
			Seeds: []bridge.Seed{{MAC: netaddr.MAC{2, 0, 0, 0, 0, 9}, Port: "r", Lifetime: bridge.Static}},
		}},
		Hosts: map[string]hostmodel.Config{"h": {Address: netaddr.MAC{2, 0, 0, 0, 0, 1}, VLAN: &vid}},
		Reflectors: map[string]reflectormodel.Config{"r": {
			Ports:       map[string]phy.Ethernet{"p": {}},
			Attachments: map[string]reflectormodel.Attachment{"a": {Port: "p", Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.0.9/24")}}},
		}},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "sw", Port: "h"}, B: fabric.Endpoint{Node: "h"}},
			{A: fabric.Endpoint{Node: "sw", Port: "r"}, B: fabric.Endpoint{Node: "r", Port: "p"}},
		},
		Uncabled:      []fabric.Uncabled{{Endpoint: fabric.Endpoint{Node: "sw", Port: "empty"}}},
		PhyAssumption: &cablemodel.PhyAssumption{Medium: cablemodel.TwistedPair, Ethernet: phy.Ethernet{SupportedSpeedsBPS: []uint64{1_000_000_000}, Setting: &phy.Setting{SpeedBPS: 1_000_000_000, Duplex: phy.Full}}}, Evidence: catalog,
	}
}

func TestSpecRoundTrip(t *testing.T) {
	first, err := assembly.NewWithSpec(builtSpecFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	spec := assembly.Spec(first)
	second, err := assembly.NewWithSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(assembly.Spec(second), spec) {
		t.Fatal("rebuilt Spec differs from the first Spec")
	}
	if len(spec.Switches["sw"].Seeds) != 1 || len(spec.Hosts) != 1 || len(spec.Reflectors) != 1 {
		t.Fatal("Spec lost a seeded switch, tagged host, or reflector")
	}
}

func TestConfigureWithoutChangeKeepsSpec(t *testing.T) {
	f, err := assembly.NewWithSpec(builtSpecFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	before := assembly.Spec(f)
	if err := assembly.Configure(f, "sw", assembly.Spec(f).Config().Switches["sw"]); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(assembly.Spec(f), before) {
		t.Fatal("no-change Configure changed Spec")
	}
	if got := operStatusConflictCount(f); got != 1 {
		t.Fatalf("conflicts after Configure = %d, want 1", got)
	}
}

func TestSpecKeepsConfiguredState(t *testing.T) {
	input, err := builtSpecFixture(t).Normalize()
	if err != nil {
		t.Fatal(err)
	}
	f, err := assembly.NewWithSpec(input)
	if err != nil {
		t.Fatal(err)
	}
	runtimePort, _ := f.Device("sw").(*vswitch.Switch).Ports().Port("h")
	if runtimePort.OperStatus != port.Up {
		t.Fatalf("runtime port = %s, want Up", runtimePort.OperStatus)
	}
	spec := assembly.Spec(f)
	configured, _ := spec.Switches["sw"].Config.Ports.Port("h")
	if configured.OperStatus != port.Down {
		t.Fatalf("configured port = %s, want Down", configured.OperStatus)
	}
	if got := operStatusConflictCount(f); got != 1 {
		t.Fatalf("conflicts = %d, want 1", got)
	}
	if spec.Start != input.Start || !reflect.DeepEqual(spec.PhyAssumption, input.PhyAssumption) || !reflect.DeepEqual(spec.Uncabled, input.Uncabled) || !reflect.DeepEqual(spec.Evidence.Entries(), input.Evidence.Entries()) {
		t.Fatal("Spec lost start, assumption, uncabled entry, or unreferenced evidence")
	}
	fault := cablemodel.Fault{Kind: cablemodel.FaultCut}
	cable := spec.Cables[0]
	if err := f.SetFault(cable.A, cable.B, fault); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(assembly.Spec(f).Cables[0].Fault, fault) {
		t.Fatal("Spec did not report present cable fault")
	}
}
