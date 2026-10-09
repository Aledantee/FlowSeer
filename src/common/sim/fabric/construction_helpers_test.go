package fabric_test

import (
	"maps"
	"net/netip"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/assembly"

	hostmodel "go.aledante.io/FlowSeer/src/common/sim/device/host"
	reflectormodel "go.aledante.io/FlowSeer/src/common/sim/device/reflector"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

var fixedTime = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

func mustTable(t *testing.T, b *port.Builder) port.Table {
	t.Helper()
	tbl, err := b.Build()
	if err != nil {
		t.Fatalf("build port table: %v", err)
	}

	return tbl
}

func constructionSpec(cfg assembly.Config) assembly.ConstructionSpec {
	switches := make(map[string]vswitch.ConstructionSpec, len(cfg.Switches))
	for name, swCfg := range cfg.Switches {
		switches[name] = vswitch.ConstructionSpec{Config: swCfg, NodeID: name}
	}

	return assembly.ConstructionSpec{
		Start:         cfg.Start,
		Switches:      switches,
		Hosts:         cfg.Hosts,
		Reflectors:    cfg.Reflectors,
		Cables:        cfg.Cables,
		Uncabled:      cfg.Uncabled,
		PhyAssumption: cfg.PhyAssumption,
	}
}

// gigabitCopper is the profile a fixture assumes when its test is not about
// negotiation: auto-negotiating 10 Mb/s to 1 Gb/s ends over twisted pair, so two
// ends with no facts of their own resolve 1 Gb/s full duplex.
func gigabitCopper() *cablemodel.PhyAssumption {
	return &cablemodel.PhyAssumption{
		Medium: cablemodel.TwistedPair,
		Ethernet: phy.Ethernet{
			SupportedSpeedsBPS:       []uint64{10_000_000, 100_000_000, 1_000_000_000},
			AutoNegotiationSupported: phy.CapabilitySupported,
			Setting:                  &phy.Setting{AutoNegotiation: true},
		},
	}
}

// statedPhysical returns a copy of cfg that states what its fixture leaves
// unreported: the gigabit copper profile for every missing physical fact, and
// an Uncabled entry for every non-LAG switch port no cable names. A fixture
// written before unreported facts became unknown keeps the links it was
// written against. A test of unknown facts builds its configuration without it.
//
// An added Uncabled port's configured operational status is cleared, since the
// fixture observed nothing about it; left Up, it would conflict with the Down
// its entry derives and put that conflict into every journey consulting it.
func statedPhysical(cfg assembly.Config) assembly.Config {
	cfg = cfg.Clone()
	if cfg.PhyAssumption == nil {
		cfg.PhyAssumption = gigabitCopper()
	}

	named := make(map[fabric.Endpoint]bool)
	for _, cable := range cfg.Cables {
		named[cable.A] = true
		named[cable.B] = true
	}
	for _, entry := range cfg.Uncabled {
		named[entry.Endpoint] = true
	}
	switchNames := slices.Sorted(maps.Keys(cfg.Switches))
	for _, name := range switchNames {
		swCfg := cfg.Switches[name]
		b := port.NewBuilder()
		for _, p := range swCfg.Ports.Ports() {
			ep := fabric.Endpoint{Node: name, Port: p.Name}
			if p.Kind != port.LAG && !named[ep] {
				cfg.Uncabled = append(cfg.Uncabled, fabric.Uncabled{Endpoint: ep})
				p.OperStatus = ""
			}
			b.Add(p)
		}
		// The table already built from these ports, so it builds again.
		swCfg.Ports, _ = b.Build()
		cfg.Switches[name] = swCfg
	}

	return cfg
}

func twoSwitchBaseConfig(t *testing.T) assembly.Config {
	t.Helper()
	b1 := port.NewBuilder()
	b1.Range("1/1/%d", 1, 2, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b1.Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up})

	b2 := port.NewBuilder()
	b2.Range("1/1/%d", 1, 2, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b2.Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up})

	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}

	return assembly.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {Ports: mustTable(t, b1)},
			"sw2": {Ports: mustTable(t, b2)},
		},
		Hosts: map[string]hostmodel.Config{
			"h1": {Address: macH1},
			"h2": {Address: macH2},
		},
		Cables: []fabric.Cable{
			{
				Cable: cablemodel.Cable{LengthMeters: 5},
				A:     fabric.Endpoint{Node: "h1"},
				B:     fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			},
			{
				Cable: cablemodel.Cable{LengthMeters: 20},
				A:     fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
				B:     fabric.Endpoint{Node: "sw2", Port: "1/1/2"},
			},
			{
				Cable: cablemodel.Cable{LengthMeters: 5},
				A:     fabric.Endpoint{Node: "h2"},
				B:     fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
			},
		},
	}
}

func reflectorBaseConfig(t *testing.T) assembly.Config {
	t.Helper()
	cfg := twoSwitchBaseConfig(t)

	b := port.NewBuilder()
	for _, p := range cfg.Switches["sw1"].Ports.Ports() {
		b.Add(p)
	}
	b.Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	sw1 := cfg.Switches["sw1"]
	sw1.Ports = mustTable(t, b)
	cfg.Switches["sw1"] = sw1

	vid10 := vlan.ID(10)
	cfg.Reflectors = map[string]reflectormodel.Config{
		"r1": {
			Ports: map[string]phy.Ethernet{"p1": {}},
			Attachments: map[string]reflectormodel.Attachment{
				"a": {
					Port:      "p1",
					VLAN:      &vid10,
					Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.9/24")},
				},
			},
		},
	}
	cfg.Cables = append(cfg.Cables, fabric.Cable{
		Cable: cablemodel.Cable{LengthMeters: 5},
		A:     fabric.Endpoint{Node: "sw1", Port: "1/1/3"},
		B:     fabric.Endpoint{Node: "r1", Port: "p1"},
	})

	return cfg
}
