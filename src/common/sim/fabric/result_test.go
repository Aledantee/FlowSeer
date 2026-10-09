package fabric_test

import (
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/assembly"

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
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestRunResultAcrossRetentions(t *testing.T) {
	cases := []string{"delivered", "dropped", "lost", "released", "timed out", "unresolved", "rejected", "mirrored"}
	checked := 0
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			var results [2]fabric.RunResult
			for i, retention := range []fabric.JourneyRetention{fabric.RetainJourney, fabric.RetainAggregate} {
				f, inj, actions := retentionResultFixture(t, name)
				inj.Flow, inj.Retention = 71, retention
				fid, err := f.Inject(inj)
				if err != nil {
					t.Fatal(err)
				}
				// Seed a carried issue independently of forwarding so every exit
				// proves the run reads metadata after aggregate settlement.
				j := (*fabric.ExportFabricJourneys(f))[fid]
				issue := analysis.Issue{Code: "fixture-observation", Status: analysis.Incomplete, Scope: analysis.WholeScope(), Message: name}
				j.Metadata = j.Metadata.Merge(analysis.NewMetadata(analysis.WholeScope(), []analysis.Issue{issue}, analysis.EvidenceCatalog{}, nil))
				if j.Metadata.Status() != analysis.Incomplete {
					t.Fatal("fixture issue was not carried")
				}
				results[i], err = f.RunScenario(fabric.Scenario{Name: name, Actions: actions}, fabric.RunOptions{Budget: 1000, Window: 0})
				if err != nil {
					t.Fatal(err)
				}
				stats := f.Flows()[71]
				if stats.Offered != 1 || stats.Held != 0 || results[i].Stop != fabric.StopQueueDrained {
					t.Fatalf("%s retention %d: stats %+v, run %+v, want one settled offered frame", name, retention, stats, results[i])
				}
				switch name {
				case "delivered", "released", "mirrored":
					if stats.Delivered["h2"] != 1 || stats.Latency.Count != 1 {
						t.Fatalf("%s stats = %+v, want one h2 delivery and latency", name, stats)
					}
				case "dropped":
					if stats.Drops[cablemodel.ReasonBadFrame] != 1 {
						t.Fatalf("drop stats = %+v, want one bad-frame drop", stats)
					}
				case "lost":
					if stats.Lost != 1 {
						t.Fatalf("loss stats = %+v, want one loss", stats)
					}
				case "timed out":
					if stats.Drops[routing.ReasonNeighborMiss] != 1 {
						t.Fatalf("timeout stats = %+v, want one neighbor-miss drop", stats)
					}
				case "unresolved":
					if stats.Unresolved != 1 {
						t.Fatalf("unresolved stats = %+v, want one unresolved frame", stats)
					}
				case "rejected":
					if stats.Rejected != 1 {
						t.Fatalf("rejection stats = %+v, want one rejected frame", stats)
					}
				}
				if name == "mirrored" && stats.Copies["analyzer"] != 1 {
					t.Fatalf("mirror stats = %+v, want one analyzer copy", stats)
				}
				if results[i].Status != analysis.Incomplete || !hasIssueCode(results[i].Issues, "fixture-observation") {
					t.Fatalf("%s retention %d: result %+v, want Incomplete with carried issue", name, retention, results[i])
				}
				checked++
			}
			if results[0].Status != results[1].Status || !analysis.SameIssues(results[0].Issues, results[1].Issues) {
				t.Errorf("retentions differ: %+v / %+v", results[0], results[1])
			}
		})
	}
	if checked != 16 {
		t.Errorf("checked retention fixtures = %d, want 16", checked)
	}
}

func retentionResultFixture(t *testing.T, name string) (*fabric.Fabric, fabric.Injection, []fabric.Action) {
	t.Helper()
	at := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	if name == "released" || name == "timed out" {
		f, swMAC, src := heldPropertyFixture(t)
		inj := fabric.Injection{At: at, Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, Frame: heldPropertyFrame(t, src, swMAC, netip.MustParseAddr("10.0.10.7"), netip.MustParseAddr("10.0.20.7"), []byte("held"))}
		var actions []fabric.Action
		if name == "released" {
			arpAt := at.Add(time.Millisecond)
			arp := fabric.Injection{At: arpAt, Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, Frame: heldPropertyARPReply(t, netaddr.MAC{0, 0x11, 0x22, 0x33, 0x44, 0x77}, netip.MustParseAddr("10.0.20.7"), swMAC)}
			actions = []fabric.Action{{ID: "result_test-1", At: arpAt, Kind: fabric.ActionInject, Inject: &arp}}
		}
		return f, inj, actions
	}
	variant := "ordinary"
	if name == "lost" {
		variant = "cable-loss"
	}
	if name == "mirrored" {
		variant = "mirror"
	}
	f := invariantFabric(t, variant, at)
	cfg := assembly.Spec(f).Config()
	if name == "dropped" {
		cfg.Cables[1].Fault = cablemodel.Fault{Kind: cablemodel.FaultCorruptEveryNth, N: 1}
	}
	if name == "unresolved" {
		cfg.PhyAssumption = nil
	}
	var err error
	f, err = assembly.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	frame := ethernet.Frame{Src: cfg.Hosts["h1"].Address, Dst: cfg.Hosts["h2"].Address, Payload: []byte("retention")}
	if name == "rejected" {
		frame.Dst = netaddr.MAC{2, 0, 0, 0, 0, 99}
	}
	return f, fabric.Injection{At: at, Origin: fabric.Endpoint{Node: "h1"}, Frame: frame}, nil
}

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

func TestStopReasonsCoverage(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)

	// StopNotRun
	fab, err := assembly.New(assembly.Config{Start: t0, PhyAssumption: testPhyAssumption()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if res := fab.Run(fabric.RunOptions{Budget: 0, Window: 0}); res.Stop != fabric.StopNotRun || res.Steps != 0 {
		t.Errorf("Run(0) stop = %v, steps = %d, want StopNotRun, 0", res.Stop, res.Steps)
	}
	if res := fab.Run(fabric.RunOptions{Budget: -5, Window: 0}); res.Stop != fabric.StopNotRun || res.Steps != 0 {
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
	resQueue := fabQueue.Run(fabric.RunOptions{Budget: 50, Window: 0})
	if resQueue.Stop != fabric.StopQueueDrained {
		t.Errorf("resQueue.Stop = %v, want StopQueueDrained", resQueue.Stop)
	}
	if resQueue.Steps != 3 {
		t.Errorf("resQueue.Steps = %d, want 3", resQueue.Steps)
	}

	// StopBudget
	fabSTP := newTestSTPFabric(t, t0, 4096, 8192)
	resBudget := fabSTP.Run(fabric.RunOptions{Budget: 2, Window: 0})
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
	fabFault := newEmptyFabric(t, t0)
	*fabric.ExportFabricStepped(fabFault) = true
	*fabric.ExportFabricEgress(fabFault) = map[fabric.Endpoint]*fabric.ExportEgressQueue{ep: fabric.MakeTestEgressQueuePointer(fabric.ExportEgressQueueFields{})}
	fabric.ExportFabricScheduleDequeue(fabFault, ep, t0.Add(-time.Second))
	resFault := fabFault.Run(fabric.RunOptions{Budget: 10, Window: 0})
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

	res := fab.Run(fabric.RunOptions{Budget: 5, Window: 0})
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
	f := newEmptyFabric(t, time.Time{})
	*fabric.ExportFabricQueue(f) = []fabric.Arrival{fabric.MakeTestArrival(fabric.ExportArrivalFields{Kind: fabric.ArrivalFrame, At: t0, FrameID: 1}), fabric.MakeTestArrival(fabric.ExportArrivalFields{Kind: fabric.ArrivalFrame, At: t0.Add(time.Microsecond), FrameID: 1}), fabric.MakeTestArrival(fabric.ExportArrivalFields{Kind: fabric.ArrivalWake, At: t0.Add(2 * time.Microsecond)}), fabric.MakeTestArrival(fabric.ExportArrivalFields{Kind: fabric.ArrivalWake, At: t0.Add(3 * time.Microsecond)}), fabric.MakeTestArrival(fabric.ExportArrivalFields{Kind: fabric.ArrivalDequeue, At: t0.Add(4 * time.Microsecond)})}
	*fabric.ExportFabricEgress(f) = map[fabric.Endpoint]*fabric.ExportEgressQueue{
		{Node: "sw1", Port: "1/1/1"}: fabric.MakeTestEgressQueuePointer(fabric.ExportEgressQueueFields{
			Pending: [8][]fabric.ExportQueued{
				0: {fabric.MakeTestQueued(fabric.ExportQueuedFields{Fid: 1, Frame: ethernet.Frame{Payload: []byte("a")}})},
				1: {fabric.MakeTestQueued(fabric.ExportQueuedFields{Fid: 1, Frame: ethernet.Frame{Payload: []byte("b")}}), fabric.MakeTestQueued(fabric.ExportQueuedFields{Fid: 1, Frame: ethernet.Frame{Payload: []byte("c")}})},
			},
		}),
	}
	*fabric.ExportFabricJourneys(f) = map[fabric.FrameID]*fabric.ExportJourney{
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
	}
	*fabric.ExportFabricInflight(f) = map[fabric.FrameID]int{1: 5}

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

	withoutWork := fab.Fork()
	fabric.ExportFabricEnqueue(fab, fabric.Arrival{Kind: fabric.ArrivalFrame, At: t0.Add(24 * time.Hour), FrameID: fid, Device: "sw1", Port: "1/1/1"})
	res := fabric.ExportFabricRun(fab, 21, 3)
	if res.Stop == fabric.StopConverged {
		t.Errorf("res.Stop = StopConverged, want convergence blocked by pending journey")
	}
	if res.Pending.Journeys != 2 {
		t.Errorf("res.Pending.Journeys = %d, want 2", res.Pending.Journeys)
	}
	res = withoutWork.Run(fabric.RunOptions{Budget: 21, Window: 3})
	if res.Stop != fabric.StopConverged || res.Pending.Journeys != 0 {
		t.Errorf("record without live work = %+v, want Converged with zero pending journeys", res)
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

	cfg := assembly.Config{
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

	fab, err := assembly.New(cfg)
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

	cfg := assembly.Config{
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

	fab, err := assembly.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := fab.Device("sw1").(*vswitch.Switch).Learn([]bridge.Seed{
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

	res := fab.Run(fabric.RunOptions{Budget: 10, Window: 0})
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

	cfg := assembly.Config{
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

	fab, err := assembly.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := fab.Device("sw1").(*vswitch.Switch).Learn([]bridge.Seed{
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

	cfg := assembly.Config{
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

	fab, err := assembly.New(cfg)
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

	cfg := assembly.Config{
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

	fab, err := assembly.New(cfg)
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
	res := fab.Run(fabric.RunOptions{Budget: 10, Window: 0})
	if res.Replay.Checkpoint() != nil || res.Replay.Scenario.Name != "" {
		t.Errorf("plain Run returned non-zero Replay: %+v", res.Replay)
	}
}

func TestScenarioReplayHasCheckpoint(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	fab := newTestTwoSwitchSTPFabric(t, t0)

	sc := fabric.Scenario{
		Name: "test-checkpoint",
	}

	res, err := fab.RunScenario(sc, fabric.RunOptions{Budget: 10, Window: 1})
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
		Name: "budget-drops-actions",
		Actions: []fabric.Action{
			{
				ID:   "result_test-2",
				At:   t0.Add(time.Second),
				Kind: fabric.ActionInject,
				Inject: &fabric.Injection{
					Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
					Frame:  frame,
				},
			},
			{
				ID:   "result_test-3",
				At:   t0.Add(2 * time.Second),
				Kind: fabric.ActionInject,
				Inject: &fabric.Injection{
					Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
					Frame:  frame,
				},
			},
		},
	}

	res, err := fab.RunScenario(sc, fabric.RunOptions{Budget: 1, Window: 0})
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

func TestPendingInputsCounted(t *testing.T) {
	fab, src, dst := newTwoSwitchTopology(t, cablemodel.Fault{})
	at := fab.Clock().Add(time.Second)
	frame := flowFrame(src, dst)
	if _, err := fab.Inject(fabric.Injection{At: fab.Clock(), Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, Frame: frame}); err != nil {
		t.Fatal(err)
	}
	if _, err := fab.Inject(fabric.Injection{At: at, Origin: fabric.Endpoint{Node: "h1"}, Frame: frame}); err != nil {
		t.Fatal(err)
	}
	if err := fab.AttachStream(fabric.StreamAttachment{Origin: fabric.Endpoint{Node: "h1"}, Source: streamSource(t, frame, 1, 1), Start: 2 * time.Second, Flow: 1}); err != nil {
		t.Fatal(err)
	}
	result, err := fab.RunScenario(fabric.Scenario{Name: "pending inputs", Actions: []fabric.Action{{ID: "result_test-4", At: at, Kind: fabric.ActionInject, Inject: &fabric.Injection{Origin: fabric.Endpoint{Node: "h2"}, Frame: frame}}}}, fabric.RunOptions{Budget: 1, Window: 0})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stop != fabric.StopBudget || result.Pending.Inputs != 3 {
		t.Fatalf("result = %+v, want Budget with three pending inputs", result)
	}
}
