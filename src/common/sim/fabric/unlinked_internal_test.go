package fabric

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func testConstructionSpec(cfg Config) ConstructionSpec {
	switches := make(map[string]vswitch.ConstructionSpec, len(cfg.Switches))
	for name, swCfg := range cfg.Switches {
		switches[name] = vswitch.ConstructionSpec{Config: swCfg, NodeID: name}
	}

	return ConstructionSpec{
		Start:      cfg.Start,
		Switches:   switches,
		Hosts:      cfg.Hosts,
		Reflectors: cfg.Reflectors,
		Cables:     cfg.Cables,
		Uncabled:   cfg.Uncabled,
	}
}

func TestFabricUnlinkedInternal(t *testing.T) {
	t.Parallel()

	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	behind3 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x03}
	behind4 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x04}
	gigabit := phy.Ethernet{SupportedSpeedsBPS: []uint64{1_000_000_000}}
	catalog, uncabledRef := analysis.EvidenceCatalog{}.Add(analysis.Evidence{Kind: "survey", Origin: "rack-walk", Context: "sw1 port 3 empty"})

	b := port.NewBuilder()
	b.Add(port.Port{Name: "1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Range("%d", 2, 4, port.Port{Kind: port.Physical, AdminStatus: port.Up})
	ports, err := b.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	cfg := Config{
		Switches: map[string]vswitch.Config{
			"sw1": {Ports: ports, Bridge: &bridge.Config{}, Phy: &phy.Config{Ethernet: map[string]phy.Ethernet{"1": gigabit}}},
		},
		Hosts:    map[string]Host{"h1": {Address: macH1, Ethernet: gigabit}},
		Cables:   []Cable{{A: Endpoint{Node: "h1"}, B: Endpoint{Node: "sw1", Port: "1"}, Medium: TwistedPair}},
		Uncabled: []Uncabled{{Endpoint: Endpoint{Node: "sw1", Port: "3"}, Evidence: []trace.EvidenceRef{uncabledRef}}},
	}
	spec := testConstructionSpec(cfg)
	spec.Evidence = catalog
	sw1 := spec.Switches["sw1"]
	sw1.Seeds = []bridge.Seed{
		{MAC: behind3, Port: "3", Lifetime: bridge.Static},
		{MAC: behind4, Port: "4", Lifetime: bridge.Static},
	}
	spec.Switches["sw1"] = sw1

	fab, err := NewWithSpec(spec)
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}

	wantUnlinked := []LinkEnd{
		{Endpoint: Endpoint{Node: "sw1", Port: "2"}, Oper: port.Unknown, Reason: ReasonAdjacencyUnresolved},
		{Endpoint: Endpoint{Node: "sw1", Port: "3"}, Oper: port.Down, Reason: ReasonNoCable},
		{Endpoint: Endpoint{Node: "sw1", Port: "4"}, Oper: port.Unknown, Reason: ReasonAdjacencyUnresolved},
	}
	got := fab.unlinked("sw1")
	if len(got) != len(wantUnlinked) {
		t.Fatalf("unlinked(sw1) = %+v, want %+v", got, wantUnlinked)
	}
	for i := range wantUnlinked {
		if got[i].Endpoint != wantUnlinked[i].Endpoint || got[i].Oper != wantUnlinked[i].Oper || got[i].Reason != wantUnlinked[i].Reason {
			t.Errorf("unlinked(sw1)[%d] = %+v, want %+v", i, got[i], wantUnlinked[i])
		}
	}
}

func TestFabricUnlinkedOmittedPortConfiguredUp(t *testing.T) {
	t.Parallel()

	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := b.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	cfg := Config{
		Switches: map[string]vswitch.Config{"sw1": {Ports: ports, Bridge: &bridge.Config{}}},
		Hosts: map[string]Host{
			"h1": {Address: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}},
			"h2": {Address: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}},
		},
		Cables: []Cable{
			{A: Endpoint{Node: "h1"}, B: Endpoint{Node: "sw1", Port: "1/1/1"}},
			{A: Endpoint{Node: "h2"}, B: Endpoint{Node: "sw1", Port: "1/1/2"}},
		},
		PhyAssumption: &PhyAssumption{
			Medium: TwistedPair,
			Ethernet: phy.Ethernet{
				SupportedSpeedsBPS:       []uint64{10_000_000, 100_000_000, 1_000_000_000},
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
		},
	}
	fab, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got := fab.unlinked("sw1")
	if len(got) != 1 || got[0].Port != "1/1/3" || got[0].Oper != port.Unknown || got[0].Reason != ReasonAdjacencyUnresolved {
		t.Errorf("unlinked(sw1) = %+v, want 1/1/3 Unknown with reason adjacency-unresolved", got)
	}
}
