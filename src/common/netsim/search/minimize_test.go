package search

import (
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

func makeMinimizeFabrics(t *testing.T) (cur, cand *fabric.Fabric, h1MAC, h2MAC, h3MAC netaddr.MAC) {
	t.Helper()
	h1MAC = netaddr.MAC{0x02, 0, 0, 0, 0, 1}
	h2MAC = netaddr.MAC{0x02, 0, 0, 0, 0, 2}
	h3MAC = netaddr.MAC{0x02, 0, 0, 0, 0, 3}

	buildFabric := func(vid2 uint16) *fabric.Fabric {
		b := port.NewBuilder()
		b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
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
						"1/1/3": {
							PVID:     &v10,
							Untagged: []vlan.ID{10},
						},
					},
				},
			},
		}

		v99 := vlan.ID(99)
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
				"h3": {Address: h3MAC, VLAN: &v99},
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
				{
					A:            fabric.Endpoint{Node: "sw1", Port: "1/1/3"},
					B:            fabric.Endpoint{Node: "h3"},
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
	cand = buildFabric(20)
	return
}

func TestMinimizePaddedCounterexampleReducesToMinimal(t *testing.T) {
	t.Parallel()

	cur, cand, h1MAC, h2MAC, h3MAC := makeMinimizeFabrics(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Injections:
	// inj0: h3 (VLAN 99) -> h1 (irrelevant: dropped at sw1:1/1/3 on both fabrics)
	// inj1: h1 -> h2 (critical: delivered on cur, dropped on cand due to VLAN mismatch)
	// inj2: h3 (VLAN 99) -> h2 (irrelevant: dropped at sw1:1/1/3 on both fabrics)
	// inj3: h3 (VLAN 99) -> h1 (irrelevant: dropped at sw1:1/1/3 on both fabrics)
	inj0 := fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h3"},
		Frame: ethernet.Frame{
			Dst:       h1MAC,
			Src:       h3MAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("pad-0"),
		},
	}
	inj1 := fabric.Injection{
		At:     t0.Add(time.Millisecond),
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Dst:       h2MAC,
			Src:       h1MAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("critical"),
		},
	}
	inj2 := fabric.Injection{
		At:     t0.Add(2 * time.Millisecond),
		Origin: fabric.Endpoint{Node: "h3"},
		Frame: ethernet.Frame{
			Dst:       h2MAC,
			Src:       h3MAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("pad-2"),
		},
	}
	inj3 := fabric.Injection{
		At:     t0.Add(3 * time.Millisecond),
		Origin: fabric.Endpoint{Node: "h3"},
		Frame: ethernet.Frame{
			Dst:       h1MAC,
			Src:       h3MAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("pad-3"),
		},
	}

	paddedCandidate := Candidate{
		Tuple:    "padded-candidate",
		Scenario: []fabric.Injection{inj0, inj1, inj2, inj3},
	}

	initialCmp := runCompare(cur, cand, paddedCandidate, 10)
	if initialCmp.Disposition != analysis.Different {
		t.Fatalf("paddedCandidate disposition = %v, want Different", initialCmp.Disposition)
	}

	reduced, minimality := Minimize(cur, cand, paddedCandidate, 10)

	if minimality != Minimal {
		t.Fatalf("minimality = %v, want %v", minimality, Minimal)
	}
	if len(reduced.Scenario) != 1 {
		t.Fatalf("len(reduced.Scenario) = %d, want 1 (reduced to single critical injection)", len(reduced.Scenario))
	}
	if string(reduced.Scenario[0].Frame.Payload) != "critical" {
		t.Errorf("retained injection payload = %q, want 'critical'", string(reduced.Scenario[0].Frame.Payload))
	}

	// Verify reduced candidate still exhibits the exact same observable
	reducedCmp := runCompare(cur, cand, reduced, 10)
	if reducedCmp.Disposition != analysis.Different {
		t.Errorf("reduced candidate disposition = %v, want Different", reducedCmp.Disposition)
	}
	if reducedCmp.Difference.Observable != initialCmp.Difference.Observable {
		t.Errorf("reduced candidate observable = %q, want %q", reducedCmp.Difference.Observable, initialCmp.Difference.Observable)
	}
}

func TestMinimizeLimitReachedReportedWithPartialReduction(t *testing.T) {
	t.Parallel()

	cur, cand, h1MAC, h2MAC, h3MAC := makeMinimizeFabrics(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	makeInj := func(offset int, payload string, node string, dst netaddr.MAC) fabric.Injection {
		src := h3MAC
		if node == "h1" {
			src = h1MAC
		}
		return fabric.Injection{
			At:     t0.Add(time.Duration(offset) * time.Millisecond),
			Origin: fabric.Endpoint{Node: node},
			Frame: ethernet.Frame{
				Dst:       dst,
				Src:       src,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte(payload),
			},
		}
	}

	// 5 injections, only offset 2 (from h1 to h2) is critical
	candInjs := []fabric.Injection{
		makeInj(0, "pad-0", "h3", h1MAC),
		makeInj(1, "pad-1", "h3", h1MAC),
		makeInj(2, "critical", "h1", h2MAC),
		makeInj(3, "pad-3", "h3", h1MAC),
		makeInj(4, "pad-4", "h3", h1MAC),
	}

	paddedCandidate := Candidate{
		Tuple:    "padded-candidate",
		Scenario: candInjs,
	}

	// Max 1 trial: will only attempt 1 removal and halt
	reduced, minimality := MinimizeWithLimit(cur, cand, paddedCandidate, 10, 1)

	if minimality != LimitReached {
		t.Fatalf("minimality = %v, want %v", minimality, LimitReached)
	}
	if len(reduced.Scenario) >= len(candInjs) || len(reduced.Scenario) == 1 {
		t.Fatalf("len(reduced.Scenario) = %d, want partial reduction (between 2 and %d)", len(reduced.Scenario), len(candInjs)-1)
	}
}

func TestMinimizeRejectsReductionThatChangesObservable(t *testing.T) {
	t.Parallel()

	cur, cand, h1MAC, h2MAC, _ := makeMinimizeFabrics(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Only 1 critical injection
	cand1 := Candidate{
		Tuple: "only-critical",
		Scenario: []fabric.Injection{
			{
				At:     t0,
				Origin: fabric.Endpoint{Node: "h1"},
				Frame: ethernet.Frame{
					Dst:       h2MAC,
					Src:       h1MAC,
					EtherType: ethernet.EtherTypeIPv4,
					Payload:   []byte("critical"),
				},
			},
		},
	}

	reduced, minimality := Minimize(cur, cand, cand1, 10)
	if minimality != Minimal {
		t.Errorf("minimality = %v, want %v", minimality, Minimal)
	}
	if len(reduced.Scenario) != 1 {
		t.Errorf("critical element was removed: %d elements remain", len(reduced.Scenario))
	}
}
