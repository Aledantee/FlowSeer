package fabric_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	hostmodel "go.aledante.io/FlowSeer/src/common/sim/device/host"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func testPhyAssumption() *cablemodel.PhyAssumption {
	return &cablemodel.PhyAssumption{
		Medium: cablemodel.TwistedPair,
		Ethernet: phy.Ethernet{
			SupportedSpeedsBPS:       []uint64{10_000_000, 100_000_000, 1_000_000_000},
			AutoNegotiationSupported: phy.CapabilitySupported,
			Setting:                  &phy.Setting{AutoNegotiation: true},
		},
	}
}

func TestStopReasonTableNoEmptyConstant(t *testing.T) {
	reasons := []fabric.StopReason{fabric.StopNotRun, fabric.StopQueueDrained, fabric.StopBudget, fabric.StopConverged, fabric.StopOscillating, fabric.StopFault}

	seen := make(map[fabric.StopReason]bool)
	for _, r := range reasons {
		if r == "" {
			t.Errorf("StopReason constant is empty string")
		}
		if seen[r] {
			t.Errorf("duplicate StopReason constant %q", r)
		}
		seen[r] = true
	}
	if len(reasons) != 6 {
		t.Errorf("got %d StopReason constants, want 6", len(reasons))
	}
}

func TestStopReasonsCoverage(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)

	// StopNotRun
	fab, err := fabric.New(fabric.Config{Start: t0, PhyAssumption: testPhyAssumption()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if res := fab.Run(0); res.Stop != fabric.StopNotRun || res.Steps != 0 {
		t.Errorf("Run(0) stop = %v, steps = %d, want StopNotRun, 0", res.Stop, res.Steps)
	}
	if res := fab.Run(-5); res.Stop != fabric.StopNotRun || res.Steps != 0 {
		t.Errorf("Run(-5) stop = %v, steps = %d, want StopNotRun, 0", res.Stop, res.Steps)
	}

	// StopQueueDrained
	fabQueue := newTestHostSwitchFabric(t, t0)
	inj := fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Src:     netaddr.MAC{0, 0, 0, 0, 0, 1},
			Dst:     netaddr.MAC{0, 0, 0, 0, 0, 2},
			Payload: []byte("ping"),
		},
	}
	if _, err := fabQueue.Inject(inj); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	resQueue := fabQueue.Run(50)
	if resQueue.Stop != fabric.StopQueueDrained {
		t.Errorf("resQueue.Stop = %v, want StopQueueDrained", resQueue.Stop)
	}
	if resQueue.Steps != 2 {
		t.Errorf("resQueue.Steps = %d, want 2", resQueue.Steps)
	}

	// StopBudget
	fabSTP := newTestSTPFabric(t, t0, 4096, 8192)
	resBudget := fabSTP.Run(2)
	if resBudget.Stop != fabric.StopBudget {
		t.Errorf("resBudget.Stop = %v, want StopBudget", resBudget.Stop)
	}
	if resBudget.Steps != 2 {
		t.Errorf("resBudget.Steps = %d, want 2", resBudget.Steps)
	}
	if !hasIssueCode(resBudget.Issues, fabric.IssueBudgetExhausted) {
		t.Errorf("resBudget.Issues missing IssueBudgetExhausted: %+v", resBudget.Issues)
	}

	// StopFault
	ep := fabric.Endpoint{Node: "sw1", Port: "1/1/1"}
	fabFault := fabric.MakeTestFabricPointer(fabric.ExportFabricFields{
		Clock:   t0,
		Stepped: true,
		Egress:  map[fabric.Endpoint]*fabric.ExportEgressQueue{ep: fabric.MakeTestEgressQueuePointer(fabric.ExportEgressQueueFields{})},
	})
	fabric.ExportFabricScheduleDequeue(fabFault, ep, t0.Add(-time.Second))
	resFault := fabFault.Run(10)
	if resFault.Stop != fabric.StopFault {
		t.Errorf("resFault.Stop = %v, want StopFault", resFault.Stop)
	}
	if resFault.Err == nil {
		t.Errorf("resFault.Err = nil, want fault error")
	}
	if !hasIssueCode(resFault.Issues, fabric.IssueSchedulingFault) {
		t.Errorf("resFault.Issues missing IssueSchedulingFault: %+v", resFault.Issues)
	}
}

func TestBudgetAndUnsupportedBothSurvive(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	fab := newSingleSwitchSTPFabric(t, t0)

	unsupportedIssue := analysis.Issue{
		Code:    "test.unsupported",
		Status:  analysis.Unsupported,
		Scope:   analysis.WholeScope(),
		Message: "unsupported hardware feature",
	}
	(*fabric.ExportFabricLinkTrust(fab)) = append((*fabric.ExportFabricLinkTrust(fab)), fabric.MakeTestLinkTrust(fabric.ExportLinkTrustFields{
		Issues: []analysis.Issue{unsupportedIssue},
	}))
	(*fabric.
		// A direct linkTrust write bypasses SetFault, the one writer whose cache
		// invalidation a real fabric sees.
		ExportFabricMetadataCache(fab)) = nil

	res := fab.Run(5)
	if res.Stop != fabric.StopBudget {
		t.Fatalf("res.Stop = %v, want StopBudget", res.Stop)
	}
	if res.Steps != 5 {
		t.Fatalf("res.Steps = %d, want 5", res.Steps)
	}
	if res.Pending.Wakes != 1 {
		t.Errorf("res.Pending.Wakes = %d, want 1", res.Pending.Wakes)
	}
	if res.Pending.Arrivals < 1 {
		t.Errorf("res.Pending.Arrivals = %d, want >= 1", res.Pending.Arrivals)
	}

	hasExhausted := hasIssueCode(res.Issues, fabric.IssueBudgetExhausted)
	hasUnsup := hasIssueCode(res.Issues, "test.unsupported")
	if !hasExhausted {
		t.Errorf("res.Issues missing IssueBudgetExhausted: %+v", res.Issues)
	}
	if !hasUnsup {
		t.Errorf("res.Issues missing test.unsupported: %+v", res.Issues)
	}
	if res.Status != analysis.Unsupported {
		t.Errorf("res.Status = %v, want Unsupported", res.Status)
	}
}

func TestPendingWorkHandBuiltQueue(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	f := fabric.MakeTestFabricPointer(fabric.ExportFabricFields{
		Queue: []fabric.Arrival{fabric.MakeTestArrival(fabric.ExportArrivalFields{Kind: fabric.ArrivalFrame, At: t0}), fabric.MakeTestArrival(fabric.ExportArrivalFields{Kind: fabric.ArrivalFrame, At: t0.Add(time.Microsecond)}), fabric.MakeTestArrival(fabric.ExportArrivalFields{Kind: fabric.ArrivalWake, At: t0.Add(2 * time.Microsecond)}), fabric.MakeTestArrival(fabric.ExportArrivalFields{Kind: fabric.ArrivalWake, At: t0.Add(3 * time.Microsecond)}), fabric.MakeTestArrival(fabric.ExportArrivalFields{Kind: fabric.ArrivalDequeue, At: t0.Add(4 * time.Microsecond)})},
		Egress: map[fabric.Endpoint]*fabric.ExportEgressQueue{
			{Node: "sw1", Port: "1/1/1"}: fabric.MakeTestEgressQueuePointer(fabric.ExportEgressQueueFields{
				Pending: [8][]fabric.ExportQueued{
					0: {fabric.MakeTestQueued(fabric.ExportQueuedFields{Frame: ethernet.Frame{Payload: []byte("a")}})},
					1: {fabric.MakeTestQueued(fabric.ExportQueuedFields{Frame: ethernet.Frame{Payload: []byte("b")}}), fabric.MakeTestQueued(fabric.ExportQueuedFields{Frame: ethernet.Frame{Payload: []byte("c")}})},
				},
			}),
		},
		Journeys: map[fabric.FrameID]*fabric.Journey{
			1: fabric.MakeTestJourneyPointer(fabric.ExportJourneyFields{
				FrameID: 1,
				Entries: []fabric.Entry{
					{
						Kind:   fabric.EntryHop,
						Device: "sw1",
						Result: &device.Result{
							Trace: trace.Trace{Outcome: trace.Held},
						},
					},
				},
			}),
		},
	})

	pw := fabric.ExportFabricPendingWork(f)
	if pw.Arrivals != 2 {
		t.Errorf("pw.Arrivals = %d, want 2", pw.Arrivals)
	}
	if pw.Wakes != 2 {
		t.Errorf("pw.Wakes = %d, want 2", pw.Wakes)
	}
	if pw.Egress != 3 {
		t.Errorf("pw.Egress = %d, want 3", pw.Egress)
	}
	if pw.Journeys != 1 {
		t.Errorf("pw.Journeys = %d, want 1", pw.Journeys)
	}
}

func TestConvergenceSpanningTreeWithHellosStillFiring(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	fab := newTestSTPFabric(t, t0, 4096, 8192)

	res := fabric.ExportFabricRun(fab, 100, 3)
	if res.Stop != fabric.StopConverged {
		t.Fatalf("res.Stop = %v, want StopConverged", res.Stop)
	}
	if res.Steps >= 100 {
		t.Fatalf("res.Steps = %d, want < 100", res.Steps)
	}
	if res.Pending.Wakes < 1 {
		t.Errorf("res.Pending.Wakes = %d, want >= 1", res.Pending.Wakes)
	}
	if len(res.Fingerprints) != res.Steps {
		t.Errorf("len(res.Fingerprints) = %d, want %d", len(res.Fingerprints), res.Steps)
	}
}

func TestConvergenceBlockedByPendingJourney(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	fab := newTestSTPFabric(t, t0, 4096, 8192)

	fid := (*fabric.ExportFabricNextFrameID(fab))
	(*fabric.ExportFabricNextFrameID(fab))++
	(*fabric.ExportFabricJourneys(fab))[fid] = fabric.MakeTestJourneyPointer(fabric.ExportJourneyFields{
		FrameID: fid,
		Origin:  fabric.JourneyOrigin{Kind: fabric.OriginInjection},
		Entries: []fabric.Entry{
			{
				Kind:   fabric.EntryHop,
				Device: "sw1",
				Result: &device.Result{
					Trace: trace.Trace{Outcome: trace.Held},
				},
			},
		},
	})

	res := fabric.ExportFabricRun(fab, 21, 3)
	if res.Stop == fabric.StopConverged {
		t.Errorf("res.Stop = StopConverged, want convergence blocked by pending journey")
	}
	if res.Pending.Journeys != 2 {
		t.Errorf("res.Pending.Journeys = %d, want 2", res.Pending.Journeys)
	}
}

func TestOscillatingRootCycle(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	sw1MAC := netaddr.MAC{0, 0, 0, 0, 1, 1}
	peerMAC := netaddr.MAC{0, 0, 0, 0, 2, 2}

	ports := func() port.Table {
		tbl, _ := port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Build()
		return tbl
	}

	cfg := fabric.Config{
		Start:         t0,
		PhyAssumption: testPhyAssumption(),
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  ports(),
				Bridge: &bridge.Config{},
				STP: &stp.Config{
					Priority: 32768,
					Address:  sw1MAC,
					Ports:    map[string]stp.Port{"1/1/1": {}},
				},
			},
		},
		Hosts: map[string]hostmodel.Config{
			"peer": {Address: peerMAC},
		},
		Cables: []fabric.Cable{
			{
				A: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
				B: fabric.Endpoint{Node: "peer"},
			},
		},
	}

	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	peerBridge := bpdu.BridgeID{Priority: 8192, Address: peerMAC}
	rootA := bpdu.BridgeID{Priority: 4096, Address: netaddr.MAC{0, 0, 0, 0, 0, 1}}
	rootB := bpdu.BridgeID{Priority: 8192, Address: netaddr.MAC{0, 0, 0, 0, 0, 2}}

	makeBPDU := func(root bpdu.BridgeID) ethernet.Frame {
		b := bpdu.BPDU{
			Version:      0,
			Type:         bpdu.TypeConfiguration,
			RootID:       root,
			BridgeID:     peerBridge,
			PortID:       0x8001,
			RootPathCost: 0,
			HelloTime:    2 * time.Second,
			MaxAge:       20 * time.Second,
			ForwardDelay: 15 * time.Second,
		}
		b.SetRole(bpdu.RoleDesignated)
		frame, err := bpdu.Encode(b, peerMAC)
		if err != nil {
			t.Fatalf("stp.Encode: %v", err)
		}
		return frame
	}

	frameA := makeBPDU(rootA)
	frameB := makeBPDU(rootB)

	// Enqueue 4 alternating BPDU arrivals
	roots := []ethernet.Frame{frameA, frameB, frameA, frameB}
	for i, f := range roots {
		at := t0.Add(time.Duration(i+1) * time.Millisecond)
		fid := (*fabric.ExportFabricNextFrameID(fab))
		(*fabric.ExportFabricNextFrameID(fab))++
		(*fabric.ExportFabricJourneys(fab))[fid] = fabric.MakeTestJourneyPointer(fabric.ExportJourneyFields{
			FrameID: fid,
			Origin:  fabric.JourneyOrigin{Kind: fabric.OriginInjection},
		})
		fabric.ExportFabricEnqueue(fab, fabric.MakeTestArrival(fabric.ExportArrivalFields{
			At:      at,
			Kind:    fabric.ArrivalFrame,
			Seq:     uint64(i + 1),
			Device:  "sw1",
			Port:    "1/1/1",
			FrameID: fid,
			Frame:   f,
		}))
	}

	res := fabric.ExportFabricRun(fab, 10, 3)
	if res.Stop != fabric.StopOscillating {
		t.Fatalf("res.Stop = %v, want StopOscillating", res.Stop)
	}
	if res.Status != analysis.Unstable {
		t.Errorf("res.Status = %v, want Unstable", res.Status)
	}
	if len(res.Cycle) != 2 {
		t.Fatalf("len(res.Cycle) = %d, want 2", len(res.Cycle))
	}
	if res.Cycle[0] == res.Cycle[1] {
		t.Errorf("cycle elements are identical: %v", res.Cycle)
	}
}

func TestFingerprintStableStatusIncompleteWhenUnknownAdjacencyConsulted(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	macH1 := netaddr.MAC{0, 0, 0, 0, 1, 1}
	macUnknown := netaddr.MAC{0, 0, 0, 0, 9, 9}

	tbl, _ := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()

	cfg := fabric.Config{
		Start:         t0,
		PhyAssumption: testPhyAssumption(),
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  tbl,
				Bridge: &bridge.Config{},
			},
		},
		Hosts: map[string]hostmodel.Config{
			"h1": {Address: macH1},
		},
		Cables: []fabric.Cable{
			{
				A: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
				B: fabric.Endpoint{Node: "h1"},
			},
		},
		// Port 1/1/2 is neither cabled nor in Uncabled -> ReasonAdjacencyUnresolved
	}

	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := fab.Switch("sw1").Learn([]bridge.Seed{
		{MAC: macUnknown, Port: "1/1/2", Lifetime: bridge.Static},
	}); err != nil {
		t.Fatalf("Learn: %v", err)
	}

	inj := fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Src:     macH1,
			Dst:     macUnknown,
			Payload: []byte("query"),
		},
	}
	if _, err := fab.Inject(inj); err != nil {
		t.Fatalf("Inject: %v", err)
	}

	res := fab.Run(10)
	if res.Stop != fabric.StopQueueDrained {
		t.Errorf("res.Stop = %v, want StopQueueDrained", res.Stop)
	}
	if res.Status != analysis.Incomplete {
		t.Errorf("res.Status = %v, want Incomplete", res.Status)
	}
	if !hasIssueCode(res.Issues, analysis.IssueCode(fabric.ReasonAdjacencyUnresolved)) {
		t.Errorf("res.Issues missing ReasonAdjacencyUnresolved: %+v", res.Issues)
	}
}

func newTestHostSwitchFabric(t *testing.T, t0 time.Time) *fabric.Fabric {
	t.Helper()
	macH1 := netaddr.MAC{0, 0, 0, 0, 1, 1}
	macH2 := netaddr.MAC{0, 0, 0, 0, 1, 2}

	tbl, _ := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()

	cfg := fabric.Config{
		Start:         t0,
		PhyAssumption: testPhyAssumption(),
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  tbl,
				Bridge: &bridge.Config{},
			},
		},
		Hosts: map[string]hostmodel.Config{
			"h1": {Address: macH1},
			"h2": {Address: macH2},
		},
		Cables: []fabric.Cable{
			{
				A: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
				B: fabric.Endpoint{Node: "h1"},
			},
			{
				A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
				B: fabric.Endpoint{Node: "h2"},
			},
		},
	}

	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := fab.Switch("sw1").Learn([]bridge.Seed{
		{MAC: macH2, Port: "1/1/2", Lifetime: bridge.Static},
	}); err != nil {
		t.Fatalf("Learn: %v", err)
	}
	return fab
}

func newTestSTPFabric(t *testing.T, t0 time.Time, prio1, prio2 uint16) *fabric.Fabric {
	t.Helper()
	mac1 := netaddr.MAC{0, 0, 0, 0, 1, 1}
	mac2 := netaddr.MAC{0, 0, 0, 0, 1, 2}

	ports := func() port.Table {
		tbl, _ := port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Build()
		return tbl
	}

	cfg := fabric.Config{
		Start:         t0,
		PhyAssumption: testPhyAssumption(),
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  ports(),
				Bridge: &bridge.Config{},
				STP: &stp.Config{
					Priority: prio1,
					Address:  mac1,
					Ports:    map[string]stp.Port{"1/1/1": {}},
				},
			},
			"sw2": {
				Ports:  ports(),
				Bridge: &bridge.Config{},
				STP: &stp.Config{
					Priority: prio2,
					Address:  mac2,
					Ports:    map[string]stp.Port{"1/1/1": {}},
				},
			},
		},
		Cables: []fabric.Cable{
			{
				A: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
				B: fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
			},
		},
	}

	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("New STP fabric: %v", err)
	}
	return fab
}

func newSingleSwitchSTPFabric(t *testing.T, t0 time.Time) *fabric.Fabric {
	t.Helper()
	mac1 := netaddr.MAC{0, 0, 0, 0, 1, 1}

	ports := func() port.Table {
		tbl, _ := port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Build()
		return tbl
	}

	cfg := fabric.Config{
		Start:         t0,
		PhyAssumption: testPhyAssumption(),
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  ports(),
				Bridge: &bridge.Config{},
				STP: &stp.Config{
					Priority: 4096,
					Address:  mac1,
					Ports:    map[string]stp.Port{"1/1/1": {}},
				},
			},
			"sw2": {
				Ports:  ports(),
				Bridge: &bridge.Config{},
			},
		},
		Cables: []fabric.Cable{
			{
				Cable: cablemodel.Cable{Delay: func() *time.Duration { d := 3 * time.Second; return &d }()},
				A:     fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
				B:     fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
			},
		},
	}

	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("New single switch STP fabric: %v", err)
	}
	return fab
}

func hasIssueCode(issues []analysis.Issue, code analysis.IssueCode) bool {
	return slices.ContainsFunc(issues, func(iss analysis.Issue) bool {
		return iss.Code == code
	})
}

func TestPlainRunLeavesReplayZero(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	fab := newTestHostSwitchFabric(t, t0)
	res := fab.Run(10)
	if res.Replay.Checkpoint() != nil || res.Replay.Scenario.Name != "" {
		t.Errorf("plain Run returned non-zero Replay: %+v", res.Replay)
	}
}

func TestScenarioReplayHasCheckpoint(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	fab := newTestTwoSwitchSTPFabric(t, t0)

	sc := fabric.Scenario{
		Name:   "test-checkpoint",
		Budget: 10,
		Window: 1,
	}

	res, err := fab.RunScenario(sc)
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if res.Replay.Checkpoint() == nil {
		t.Error("RunScenario replay has no checkpoint")
	}
}

func TestRunScenarioBudgetExhaustionReportsDroppedActions(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	fab := newTestSTPFabric(t, t0, 8192, 16384)

	frame := ethernet.Frame{
		Src:     netaddr.MAC{0, 0, 0, 0, 0, 1},
		Dst:     netaddr.MAC{0, 0, 0, 0, 0, 2},
		Payload: []byte("ping"),
	}

	sc := fabric.Scenario{
		Name:   "budget-drops-actions",
		Budget: 1,
		Window: 0,
		Actions: []fabric.Action{
			{
				At:   t0.Add(time.Second),
				Kind: fabric.ActionInject,
				Inject: &fabric.Injection{
					Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
					Frame:  frame,
				},
			},
			{
				At:   t0.Add(2 * time.Second),
				Kind: fabric.ActionInject,
				Inject: &fabric.Injection{
					Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
					Frame:  frame,
				},
			},
		},
	}

	res, err := fab.RunScenario(sc)
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if res.Stop != fabric.StopBudget {
		t.Fatalf("res.Stop = %v, want StopBudget", res.Stop)
	}
	if res.DroppedActions != 2 {
		t.Errorf("res.DroppedActions = %d, want 2", res.DroppedActions)
	}
	var found bool
	for _, iss := range res.Issues {
		if iss.Code == fabric.IssueActionsDropped {
			found = true
			if !strings.Contains(iss.Message, "2 timed actions did not fire before the run stopped") {
				t.Errorf("issue message = %q, want naming 2 timed actions did not fire before the run stopped", iss.Message)
			}
		}
	}
	if !found {
		t.Errorf("res.Issues missing IssueActionsDropped (%q): %+v", fabric.IssueActionsDropped, res.Issues)
	}
}
