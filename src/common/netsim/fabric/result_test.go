package fabric

import (
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
)

func testPhyAssumption() *PhyAssumption {
	return &PhyAssumption{
		Medium: TwistedPair,
		Ethernet: phy.Ethernet{
			SupportedSpeedsBPS:       []uint64{10_000_000, 100_000_000, 1_000_000_000},
			AutoNegotiationSupported: phy.CapabilitySupported,
			Setting:                  &phy.Setting{AutoNegotiation: true},
		},
	}
}

func TestStopReasonTableNoEmptyConstant(t *testing.T) {
	reasons := []StopReason{
		StopNotRun,
		StopQueueDrained,
		StopBudget,
		StopConverged,
		StopOscillating,
		StopFault,
	}

	seen := make(map[StopReason]bool)
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
		t.Errorf("expected 6 StopReason constants, got %d", len(reasons))
	}
}

func TestStopReasonsCoverage(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)

	// StopNotRun
	fab, err := New(Config{Start: t0, PhyAssumption: testPhyAssumption()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if res := fab.Run(0); res.Stop != StopNotRun || res.Steps != 0 {
		t.Errorf("Run(0) stop = %v, steps = %d, want StopNotRun, 0", res.Stop, res.Steps)
	}
	if res := fab.Run(-5); res.Stop != StopNotRun || res.Steps != 0 {
		t.Errorf("Run(-5) stop = %v, steps = %d, want StopNotRun, 0", res.Stop, res.Steps)
	}

	// StopQueueDrained
	fabQueue := newTestHostSwitchFabric(t, t0)
	inj := Injection{
		At:     t0,
		Origin: Endpoint{Node: "h1"},
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
	if resQueue.Stop != StopQueueDrained {
		t.Errorf("resQueue.Stop = %v, want StopQueueDrained", resQueue.Stop)
	}
	if resQueue.Steps != 1 {
		t.Errorf("resQueue.Steps = %d, want 1", resQueue.Steps)
	}

	// StopBudget
	fabSTP := newTestSTPFabric(t, t0, 4096, 8192)
	resBudget := fabSTP.Run(2)
	if resBudget.Stop != StopBudget {
		t.Errorf("resBudget.Stop = %v, want StopBudget", resBudget.Stop)
	}
	if resBudget.Steps != 2 {
		t.Errorf("resBudget.Steps = %d, want 2", resBudget.Steps)
	}
	if !hasIssueCode(resBudget.Issues, IssueBudgetExhausted) {
		t.Errorf("resBudget.Issues missing IssueBudgetExhausted: %+v", resBudget.Issues)
	}

	// StopFault
	ep := Endpoint{Node: "sw1", Port: "1/1/1"}
	fabFault := &Fabric{
		clock:   t0,
		stepped: true,
		egress:  map[Endpoint]*egressQueue{ep: {}},
	}
	fabFault.scheduleDequeue(ep, t0.Add(-time.Second))
	resFault := fabFault.Run(10)
	if resFault.Stop != StopFault {
		t.Errorf("resFault.Stop = %v, want StopFault", resFault.Stop)
	}
	if resFault.Err == nil {
		t.Errorf("resFault.Err = nil, want fault error")
	}
	if !hasIssueCode(resFault.Issues, IssueSchedulingFault) {
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
	fab.linkTrust = append(fab.linkTrust, linkTrust{
		issues: []analysis.Issue{unsupportedIssue},
	})

	res := fab.Run(5)
	if res.Stop != StopBudget {
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

	hasExhausted := hasIssueCode(res.Issues, IssueBudgetExhausted)
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
	f := &Fabric{
		queue: []Arrival{
			{Kind: ArrivalFrame, At: t0},
			{Kind: ArrivalFrame, At: t0.Add(time.Microsecond)},
			{Kind: ArrivalWake, At: t0.Add(2 * time.Microsecond)},
			{Kind: ArrivalWake, At: t0.Add(3 * time.Microsecond)},
			{Kind: ArrivalDequeue, At: t0.Add(4 * time.Microsecond)},
		},
		egress: map[Endpoint]*egressQueue{
			{Node: "sw1", Port: "1/1/1"}: {
				pending: [8][]queued{
					0: {{frame: ethernet.Frame{Payload: []byte("a")}}},
					1: {{frame: ethernet.Frame{Payload: []byte("b")}}, {frame: ethernet.Frame{Payload: []byte("c")}}},
				},
			},
		},
		journeys: map[FrameID]*Journey{
			1: {
				FrameID: 1,
				Entries: []Entry{
					{
						Kind:   EntryHop,
						Device: "sw1",
						Result: &vswitch.ForwardResult{
							Trace: trace.Trace{Outcome: trace.Held},
						},
					},
				},
			},
		},
	}

	pw := f.pendingWork()
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

	res := fab.run(100, 3)
	if res.Stop != StopConverged {
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

	fid := fab.nextFrameID
	fab.nextFrameID++
	fab.journeys[fid] = &Journey{
		FrameID: fid,
		Origin:  JourneyOrigin{Kind: OriginInjection},
		Entries: []Entry{
			{
				Kind:   EntryHop,
				Device: "sw1",
				Result: &vswitch.ForwardResult{
					Trace: trace.Trace{Outcome: trace.Held},
				},
			},
		},
	}

	res := fab.run(20, 3)
	if res.Stop == StopConverged {
		t.Errorf("res.Stop = StopConverged, want convergence blocked by pending journey")
	}
	if res.Pending.Journeys != 1 {
		t.Errorf("res.Pending.Journeys = %d, want 1", res.Pending.Journeys)
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

	cfg := Config{
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
		Hosts: map[string]Host{
			"peer": {Address: peerMAC},
		},
		Cables: []Cable{
			{
				A: Endpoint{Node: "sw1", Port: "1/1/1"},
				B: Endpoint{Node: "peer"},
			},
		},
	}

	fab, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	peerBridge := stp.BridgeID{Priority: 8192, Address: peerMAC}
	rootA := stp.BridgeID{Priority: 4096, Address: netaddr.MAC{0, 0, 0, 0, 0, 1}}
	rootB := stp.BridgeID{Priority: 8192, Address: netaddr.MAC{0, 0, 0, 0, 0, 2}}

	makeBPDU := func(root stp.BridgeID) ethernet.Frame {
		b := stp.BPDU{
			Version:      0,
			Type:         stp.BPDUTypeConfiguration,
			RootID:       root,
			BridgeID:     peerBridge,
			PortID:       0x8001,
			RootPathCost: 0,
			HelloTime:    2 * time.Second,
			MaxAge:       20 * time.Second,
			ForwardDelay: 15 * time.Second,
		}
		b.SetRole(stp.RoleDesignated)
		frame, err := stp.Encode(b, peerMAC)
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
		fid := fab.nextFrameID
		fab.nextFrameID++
		fab.journeys[fid] = &Journey{
			FrameID: fid,
			Origin:  JourneyOrigin{Kind: OriginInjection},
		}
		fab.enqueue(Arrival{
			At:      at,
			Kind:    ArrivalFrame,
			Seq:     uint64(i + 1),
			Device:  "sw1",
			Port:    "1/1/1",
			FrameID: fid,
			Frame:   f,
		})
	}

	res := fab.run(10, 3)
	if res.Stop != StopOscillating {
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

	cfg := Config{
		Start:         t0,
		PhyAssumption: testPhyAssumption(),
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  tbl,
				Bridge: &bridge.Config{},
			},
		},
		Hosts: map[string]Host{
			"h1": {Address: macH1},
		},
		Cables: []Cable{
			{
				A: Endpoint{Node: "sw1", Port: "1/1/1"},
				B: Endpoint{Node: "h1"},
			},
		},
		// Port 1/1/2 is neither cabled nor in Uncabled -> ReasonAdjacencyUnresolved
	}

	fab, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := fab.Switch("sw1").Learn([]bridge.Seed{
		{MAC: macUnknown, Port: "1/1/2", Lifetime: bridge.Static},
	}); err != nil {
		t.Fatalf("Learn: %v", err)
	}

	inj := Injection{
		At:     t0,
		Origin: Endpoint{Node: "h1"},
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
	if res.Stop != StopQueueDrained {
		t.Errorf("res.Stop = %v, want StopQueueDrained", res.Stop)
	}
	if res.Status != analysis.Incomplete {
		t.Errorf("res.Status = %v, want Incomplete", res.Status)
	}
	if !hasIssueCode(res.Issues, analysis.IssueCode(ReasonAdjacencyUnresolved)) {
		t.Errorf("res.Issues missing ReasonAdjacencyUnresolved: %+v", res.Issues)
	}
}

func newTestHostSwitchFabric(t *testing.T, t0 time.Time) *Fabric {
	t.Helper()
	macH1 := netaddr.MAC{0, 0, 0, 0, 1, 1}
	macH2 := netaddr.MAC{0, 0, 0, 0, 1, 2}

	tbl, _ := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()

	cfg := Config{
		Start:         t0,
		PhyAssumption: testPhyAssumption(),
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  tbl,
				Bridge: &bridge.Config{},
			},
		},
		Hosts: map[string]Host{
			"h1": {Address: macH1},
			"h2": {Address: macH2},
		},
		Cables: []Cable{
			{
				A: Endpoint{Node: "sw1", Port: "1/1/1"},
				B: Endpoint{Node: "h1"},
			},
			{
				A: Endpoint{Node: "sw1", Port: "1/1/2"},
				B: Endpoint{Node: "h2"},
			},
		},
	}

	fab, err := New(cfg)
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

func newTestSTPFabric(t *testing.T, t0 time.Time, prio1, prio2 uint16) *Fabric {
	t.Helper()
	mac1 := netaddr.MAC{0, 0, 0, 0, 1, 1}
	mac2 := netaddr.MAC{0, 0, 0, 0, 1, 2}

	ports := func() port.Table {
		tbl, _ := port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Build()
		return tbl
	}

	cfg := Config{
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
		Cables: []Cable{
			{
				A: Endpoint{Node: "sw1", Port: "1/1/1"},
				B: Endpoint{Node: "sw2", Port: "1/1/1"},
			},
		},
	}

	fab, err := New(cfg)
	if err != nil {
		t.Fatalf("New STP fabric: %v", err)
	}
	return fab
}

func newSingleSwitchSTPFabric(t *testing.T, t0 time.Time) *Fabric {
	t.Helper()
	mac1 := netaddr.MAC{0, 0, 0, 0, 1, 1}

	ports := func() port.Table {
		tbl, _ := port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Build()
		return tbl
	}

	cfg := Config{
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
		Cables: []Cable{
			{
				A:     Endpoint{Node: "sw1", Port: "1/1/1"},
				B:     Endpoint{Node: "sw2", Port: "1/1/1"},
				Delay: func() *time.Duration { d := 3 * time.Second; return &d }(),
			},
		},
	}

	fab, err := New(cfg)
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
