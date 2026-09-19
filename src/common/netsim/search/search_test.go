package search

import (
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

func makeTestFabrics(t *testing.T) (cur, cand, curTwin, candTwin *fabric.Fabric, h1MAC, h2MAC netaddr.MAC) {
	t.Helper()
	h1MAC = netaddr.MAC{0x02, 0, 0, 0, 0, 1}
	h2MAC = netaddr.MAC{0x02, 0, 0, 0, 0, 2}

	buildFabric := func(vid2 uint16) *fabric.Fabric {
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
						"1/1/1": {
							PVID:     &v10,
							Untagged: []vlan.ID{10},
						},
						"1/1/2": {
							PVID:     &v2,
							Untagged: []vlan.ID{v2},
						},
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
			Switches: map[string]vswitch.Config{
				"sw1": swCfg,
			},
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
		return fab
	}

	cur = buildFabric(10)
	curTwin = buildFabric(10)
	cand = buildFabric(20)
	candTwin = buildFabric(20)
	return
}

func make12TupleDomain(h1MAC, h2MAC netaddr.MAC) *L2TrafficDomain {
	return NewL2TrafficDomain(L2TrafficDomainConfig{
		Sources: []fabric.Endpoint{
			{Node: "h1"},
			{Node: "h2"},
		},
		Destinations: []netaddr.MAC{
			h1MAC,
			h2MAC,
		},
		VLANs: []vlan.ID{10, 20, 30},
		Shapes: []FrameShape{
			{EtherType: ethernet.EtherTypeIPv4, Payload: []byte("pkt")},
		},
	})
}

func TestSearchFullCoverage(t *testing.T) {
	t.Parallel()

	cur, _, curTwin, _, h1MAC, h2MAC := makeTestFabrics(t)
	dom := make12TupleDomain(h1MAC, h2MAC)

	res := Search(cur, curTwin, dom, Limits{Budget: 10})

	if res.Coverage.Tested != dom.Size() {
		t.Errorf("res.Coverage.Tested = %d, want %d", res.Coverage.Tested, dom.Size())
	}
	if !res.Coverage.Complete() {
		t.Errorf("res.Coverage.Complete() = false, want true")
	}
	if len(res.Remainder) != 0 {
		t.Errorf("len(res.Remainder) = %d, want 0", len(res.Remainder))
	}
	if len(res.Differences) != 0 {
		t.Errorf("len(res.Differences) = %d, want 0", len(res.Differences))
	}
	if res.EquivalentCount != dom.Size() {
		t.Errorf("res.EquivalentCount = %d, want %d", res.EquivalentCount, dom.Size())
	}
}

func TestSearchBudgetCutExactRemainder(t *testing.T) {
	t.Parallel()

	cur, _, curTwin, _, h1MAC, h2MAC := makeTestFabrics(t)
	dom := make12TupleDomain(h1MAC, h2MAC)

	var allTuples []Tuple
	dom.Enumerate(func(c Candidate) bool {
		allTuples = append(allTuples, c.Tuple)
		return true
	})

	lim := Limits{
		MaxCandidates: 8,
		Budget:        10,
	}
	res := Search(cur, curTwin, dom, lim)

	if res.Coverage.Tested != 8 {
		t.Fatalf("res.Coverage.Tested = %d, want 8", res.Coverage.Tested)
	}
	if res.Coverage.Total != 12 {
		t.Fatalf("res.Coverage.Total = %d, want 12", res.Coverage.Total)
	}
	if res.Coverage.Complete() {
		t.Errorf("res.Coverage.Complete() = true, want false")
	}

	expectedRemainder := allTuples[8:]
	if !slices.Equal(res.Remainder, expectedRemainder) {
		t.Fatalf("res.Remainder = %v, want %v", res.Remainder, expectedRemainder)
	}
}

func TestSearchDetectsDifferenceAndCapturesReplay(t *testing.T) {
	t.Parallel()

	cur, cand, _, _, h1MAC, h2MAC := makeTestFabrics(t)
	dom := make12TupleDomain(h1MAC, h2MAC)

	lim := Limits{
		MaxDifferences: 5,
		Budget:         10,
	}
	res := Search(cur, cand, dom, lim)

	if res.TotalDifferences == 0 {
		t.Fatalf("res.TotalDifferences = 0, want >= 1")
	}
	if len(res.Differences) == 0 {
		t.Fatalf("len(res.Differences) = 0, want >= 1")
	}

	diff := res.Differences[0]
	if diff.Tuple == "" {
		t.Errorf("retained difference tuple is empty")
	}
	if diff.Difference.Observable == "" {
		t.Errorf("retained difference observable is empty")
	}
	if diff.Replay[0].Contract != fabric.ReplayContract {
		t.Errorf("retained difference replay[0] contract = %q, want %q", diff.Replay[0].Contract, fabric.ReplayContract)
	}
	if diff.Replay[1].Contract != fabric.ReplayContract {
		t.Errorf("retained difference replay[1] contract = %q, want %q", diff.Replay[1].Contract, fabric.ReplayContract)
	}
}

func TestSearchRetainedDifferenceWallSummaryOverflow(t *testing.T) {
	t.Parallel()

	cur, cand, _, _, h1MAC, h2MAC := makeTestFabrics(t)
	dom := make12TupleDomain(h1MAC, h2MAC)

	lim := Limits{
		MaxDifferences: 1,
		Budget:         10,
	}
	res := Search(cur, cand, dom, lim)

	if res.TotalDifferences <= 1 {
		t.Fatalf("res.TotalDifferences = %d, want > 1 for overflow test", res.TotalDifferences)
	}
	if len(res.Differences) != 1 {
		t.Fatalf("len(res.Differences) = %d, want 1 (wall enforced)", len(res.Differences))
	}
	if res.DiscardedDifferences() != res.TotalDifferences-1 {
		t.Errorf("res.DiscardedDifferences() = %d, want %d", res.DiscardedDifferences(), res.TotalDifferences-1)
	}
}

func TestSearchFabricsUnchangedAfterSearch(t *testing.T) {
	t.Parallel()

	cur, cand, curTwin, candTwin, h1MAC, h2MAC := makeTestFabrics(t)
	dom := make12TupleDomain(h1MAC, h2MAC)

	// Run Search
	_ = Search(cur, cand, dom, Limits{Budget: 10})

	// Verify cur matches curTwin
	scenario := []fabric.Injection{
		{
			At:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Origin: fabric.Endpoint{Node: "h1"},
			Frame: ethernet.Frame{
				Dst:       h2MAC,
				Src:       h1MAC,
				Tags:      []vlan.Tag{{VID: 10}},
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("verify"),
			},
		},
	}

	cmpCur := fabric.Compare(cur, curTwin, scenario, 10)
	if cmpCur.Disposition != analysis.Equivalent {
		t.Errorf("cur diverged from curTwin after Search: %v (%v)", cmpCur.Disposition, cmpCur.Difference)
	}

	cmpCand := fabric.Compare(cand, candTwin, scenario, 10)
	if cmpCand.Disposition != analysis.Equivalent {
		t.Errorf("cand diverged from candTwin after Search: %v (%v)", cmpCand.Disposition, cmpCand.Difference)
	}
}

func TestSearchEmptyDomain(t *testing.T) {
	t.Parallel()

	cur, cand, _, _, _, _ := makeTestFabrics(t)
	emptyDom := NewL2TrafficDomain(L2TrafficDomainConfig{})

	res := Search(cur, cand, emptyDom, Limits{Budget: 10})

	if res.Coverage.Tested != 0 || res.Coverage.Total != 0 {
		t.Errorf("res.Coverage = %v, want 0/0", res.Coverage)
	}
	if len(res.Remainder) != 0 {
		t.Errorf("len(res.Remainder) = %d, want 0", len(res.Remainder))
	}
	if len(res.Differences) != 0 {
		t.Errorf("len(res.Differences) = %d, want 0", len(res.Differences))
	}
}
