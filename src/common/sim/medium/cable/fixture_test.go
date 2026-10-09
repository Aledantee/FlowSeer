package cable_test

import (
	"maps"
	"slices"
	"testing"

	"go.aledante.io/FlowSeer/src/common/sim/assembly"

	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func mustTable(t *testing.T, b *port.Builder) port.Table {
	t.Helper()
	tbl, err := b.Build()
	if err != nil {
		t.Fatalf("build port table: %v", err)
	}

	return tbl
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
