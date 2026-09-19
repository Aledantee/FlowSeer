package fabric_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/internal/netsimtest"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

func buildConformanceFabric(t *testing.T, vid2 uint16) (*fabric.Fabric, netaddr.MAC, netaddr.MAC) {
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
	return fab, h1MAC, h2MAC
}

// Conformance group: clone-isolation
func TestConformanceCloneIsolation(t *testing.T) {
	t.Parallel()

	base, h1MAC, h2MAC := buildConformanceFabric(t, 10)
	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	clone := base.Fork()

	// Mutate clone by injecting frames and stepping
	_, err := clone.Inject(fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Dst:       h2MAC,
			Src:       h1MAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("clone-only"),
		},
	})
	if err != nil {
		t.Fatalf("clone.Inject: %v", err)
	}

	for i := 0; i < 10; i++ {
		clone.Step()
	}

	if len(clone.Report()) == 0 {
		t.Fatal("clone has no journeys after stepping")
	}

	// Base must remain completely pristine and unstepped
	if len(base.Report()) != 0 {
		t.Errorf("base has %d journeys, want 0 (clone isolation violated)", len(base.Report()))
	}
}

// Conformance group: run-lifecycle
func TestConformanceRunLifecycle(t *testing.T) {
	t.Parallel()

	fab, h1MAC, h2MAC := buildConformanceFabric(t, 10)
	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	// 1. Not run if budget is 0
	resNotRun := fab.Run(0)
	if resNotRun.Stop != fabric.StopNotRun {
		t.Errorf("Run(0) StopReason = %v, want %v", resNotRun.Stop, fabric.StopNotRun)
	}

	// 2. Schedule injections with shuffled insertion order
	rawOffsets := []int{3, 0, 2, 1}
	shuffled := netsimtest.PermuteOrder(rawOffsets, 7)

	for _, offset := range shuffled {
		_, err := fab.Inject(fabric.Injection{
			At:     t0.Add(time.Duration(offset) * time.Millisecond),
			Origin: fabric.Endpoint{Node: "h1"},
			Frame: ethernet.Frame{
				Dst:       h2MAC,
				Src:       h1MAC,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("lifecycle-frame"),
			},
		})
		if err != nil {
			t.Fatalf("fab.Inject: %v", err)
		}
	}

	// 3. StopBudget when budget is limited
	resBudget := fab.Run(1)
	if resBudget.Stop != fabric.StopBudget {
		t.Errorf("Run(1) StopReason = %v, want %v", resBudget.Stop, fabric.StopBudget)
	}

	// 4. Drain queue with sufficient budget
	resDrained := fab.Run(100)
	if resDrained.Stop != fabric.StopQueueDrained {
		t.Errorf("Run(100) StopReason = %v, want %v", resDrained.Stop, fabric.StopQueueDrained)
	}

	journeys := fab.Report()
	if len(journeys) != 4 {
		t.Fatalf("journeys count = %d, want 4", len(journeys))
	}
}

// Conformance group: exact-behavioral-comparison
func TestConformanceExactBehavioralComparison(t *testing.T) {
	t.Parallel()

	cur, h1MAC, h2MAC := buildConformanceFabric(t, 10)
	cand, _, _ := buildConformanceFabric(t, 20)
	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	scenario := []fabric.Injection{
		{
			At:     t0,
			Origin: fabric.Endpoint{Node: "h1"},
			Frame: ethernet.Frame{
				Dst:       h2MAC,
				Src:       h1MAC,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("probe"),
			},
		},
	}

	// Current-first comparison
	cmpCurFirst := fabric.Compare(cur, cand, scenario, 10)
	if cmpCurFirst.Disposition != analysis.Different {
		t.Fatalf("cur-first comparison = %v, want Different", cmpCurFirst.Disposition)
	}

	// Candidate-first comparison
	cmpCandFirst := fabric.Compare(cand, cur, scenario, 10)
	if cmpCandFirst.Disposition != analysis.Different {
		t.Fatalf("cand-first comparison = %v, want Different", cmpCandFirst.Disposition)
	}

	// Observable should remain identical, with Current and Expected swapped
	if cmpCurFirst.Difference.Observable != cmpCandFirst.Difference.Observable {
		t.Errorf("observable mismatch: %q vs %q", cmpCurFirst.Difference.Observable, cmpCandFirst.Difference.Observable)
	}
	if cmpCurFirst.Difference.Current != cmpCandFirst.Difference.Expected ||
		cmpCurFirst.Difference.Expected != cmpCandFirst.Difference.Current {
		t.Errorf("symmetric swap violated: curFirst=%+v, candFirst=%+v",
			cmpCurFirst.Difference, cmpCandFirst.Difference)
	}
}

// Conformance group: diagnostic-trace-equality
func TestConformanceDiagnosticTraceEquality(t *testing.T) {
	t.Parallel()

	cur, h1MAC, h2MAC := buildConformanceFabric(t, 10)
	cand, _, _ := buildConformanceFabric(t, 10)
	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	scenario := []fabric.Injection{
		{
			At:     t0,
			Origin: fabric.Endpoint{Node: "h1"},
			Frame: ethernet.Frame{
				Dst:       h2MAC,
				Src:       h1MAC,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("probe"),
			},
		},
	}

	cmpAB := fabric.Compare(cur, cand, scenario, 10)
	if cmpAB.Disposition != analysis.Equivalent {
		t.Errorf("cur vs cand disposition = %v, want Equivalent", cmpAB.Disposition)
	}

	cmpBA := fabric.Compare(cand, cur, scenario, 10)
	if cmpBA.Disposition != analysis.Equivalent {
		t.Errorf("cand vs cur disposition = %v, want Equivalent", cmpBA.Disposition)
	}
}
