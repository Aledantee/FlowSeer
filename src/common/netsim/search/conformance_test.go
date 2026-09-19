package search_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/search"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

func buildSearchConformanceFabric(t *testing.T, vid2 uint16) (*fabric.Fabric, netaddr.MAC) {
	t.Helper()
	h1MAC := netaddr.MAC{0x02, 0, 0, 0, 0, 1}
	h2MAC := netaddr.MAC{0x02, 0, 0, 0, 0, 2}

	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	pTable, err := b.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	v10 := vlan.ID(10)
	v2 := vlan.ID(vid2)
	swCfg := vswitch.Config{
		Ports: pTable,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{
					10: "VLAN10",
					20: "VLAN20",
				},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &v10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &v2, Untagged: []vlan.ID{v2}},
				},
			},
		},
	}

	cfg := fabric.Config{
		PhyAssumption: &fabric.PhyAssumption{
			Medium: fabric.TwistedPair,
			Ethernet: phy.Ethernet{
				SupportedSpeedsBPS:       []uint64{10_000_000, 100_000_000, 1_000_000_000},
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
		},
		Switches: map[string]vswitch.Config{"sw1": swCfg},
		Hosts: map[string]fabric.Host{
			"h1": {Address: h1MAC},
			"h2": {Address: h2MAC},
		},
		Cables: []fabric.Cable{
			{
				A:            fabric.Endpoint{Node: "h1"},
				B:            fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
				LengthMeters: 5,
				Medium:       fabric.TwistedPair,
			},
			{
				A:            fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
				B:            fabric.Endpoint{Node: "h2"},
				LengthMeters: 5,
				Medium:       fabric.TwistedPair,
			},
		},
	}

	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}
	return fab, h2MAC
}

// Conformance group: search-coverage-accounting
func TestConformanceSearchCoverageAccounting(t *testing.T) {
	t.Parallel()

	cur, h2MAC := buildSearchConformanceFabric(t, 10)
	cand, _ := buildSearchConformanceFabric(t, 20)

	domain := search.NewL2TrafficDomain(search.L2TrafficDomainConfig{
		Sources:      []fabric.Endpoint{{Node: "h1"}},
		Destinations: []netaddr.MAC{h2MAC},
		VLANs:        []vlan.ID{10, 20, 30},
		Shapes: []search.FrameShape{
			{EtherType: ethernet.EtherTypeIPv4, Payload: []byte("frame-1")},
			{EtherType: ethernet.EtherTypeIPv4, Payload: []byte("frame-2")},
		},
	})

	total := domain.Size()
	if total != 6 {
		t.Fatalf("domain.Size() = %d, want 6", total)
	}

	limits := search.Limits{
		MaxCandidates: 4,
		Budget:        10,
	}

	// 1. Current-first execution
	resCurFirst := search.Search(cur, cand, domain, limits)

	if resCurFirst.Coverage.Total != total {
		t.Errorf("Coverage.Total = %d, want %d", resCurFirst.Coverage.Total, total)
	}
	if resCurFirst.Coverage.Tested != 4 {
		t.Errorf("Coverage.Tested = %d, want 4", resCurFirst.Coverage.Tested)
	}
	if len(resCurFirst.Remainder) != 2 {
		t.Errorf("len(Remainder) = %d, want 2 (total - tested)", len(resCurFirst.Remainder))
	}

	// 2. Candidate-first execution
	resCandFirst := search.Search(cand, cur, domain, limits)

	if resCandFirst.Coverage.Total != resCurFirst.Coverage.Total {
		t.Errorf("Coverage.Total mismatch across evaluations: %d vs %d",
			resCandFirst.Coverage.Total, resCurFirst.Coverage.Total)
	}
	if resCandFirst.Coverage.Tested != resCurFirst.Coverage.Tested {
		t.Errorf("Coverage.Tested mismatch across evaluations: %d vs %d",
			resCandFirst.Coverage.Tested, resCurFirst.Coverage.Tested)
	}
	if len(resCandFirst.Remainder) != len(resCurFirst.Remainder) {
		t.Errorf("len(Remainder) mismatch across evaluations: %d vs %d",
			len(resCandFirst.Remainder), len(resCurFirst.Remainder))
	}
	if len(resCurFirst.Differences) != len(resCandFirst.Differences) {
		t.Errorf("Differences count mismatch: %d vs %d",
			len(resCurFirst.Differences), len(resCandFirst.Differences))
	}
}
