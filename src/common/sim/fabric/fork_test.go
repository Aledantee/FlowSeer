package fabric_test

import (
	"maps"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/assembly"
	"go.aledante.io/FlowSeer/src/common/sim/flow"

	hostmodel "go.aledante.io/FlowSeer/src/common/sim/device/host"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

var fabricForkChecks = map[string]struct {
	class forkClass
	probe func(*testing.T)
}{
	"topology":        {classDeepCopied, probeFabricTopology},
	"evidence":        {classImmutableShared, nil},
	"runtimeEvidence": {classCopied, nil},
	"links":           {classDeepCopied, probeFabricLinks},
	"linkTrust":       {classDeepCopied, probeFabricLinkTrust},
	"uncabled":        {classDeepCopied, probeFabricUncabled},
	"byEnd":           {classDeepCopied, probeFabricByEnd},
	"clock":           {classCopied, nil},
	"stepped":         {classCopied, nil},
	"arrivalQueue":    {classDeepCopied, probeFabricArrivalQueue},
	"nextFrameID":     {classCopied, nil},
	"nextSeq":         {classCopied, nil},
	"journeys":        {classDeepCopied, probeFabricJourneys},
	"entered":         {classDeepCopied, probeFabricEntered},
	"cableCrossings":  {classDeepCopied, probeFabricCableCrossings},
	"busyUntil":       {classDeepCopied, probeFabricBusyUntil},
	"egress":          {classDeepCopied, probeFabricEgress},
	"counters":        {classDeepCopied, probeFabricCounters},
	"unstatedBacked":  {classDeepCopied, probeFabricUnstatedBacked},
	"metadataCache":   {classImmutableShared, nil},
	"inflight":        {classDeepCopied, probeFabricInflight},
	"held":            {classDeepCopied, probeFabricHeld},
	"flows":           {classDeepCopied, probeFabricFlows},
	"protocolIssues":  {classDeepCopied, probeFabricProtocolIssues},
	"touched":         {classResetOnFork, nil},
	"err":             {classCopied, nil},
	"attachments":     {classDeepCopied, probeFabricAttachments},
}

func probeFabricUnstatedBacked(t *testing.T) {
	fab := newTestFabricForFork(t)
	ep := fabric.Endpoint{Node: "sw1", Port: "1/1/1"}
	*fabric.ExportFabricUnstatedBacked(fab) = map[fabric.Endpoint]trace.EvidenceRef{ep: "source-ref"}
	fork := fab.Fork()
	(*fabric.ExportFabricUnstatedBacked(fork))[ep] = "fork-ref"
	if (*fabric.ExportFabricUnstatedBacked(fab))[ep] != "source-ref" {
		t.Error("source evidence reference changed through fork")
	}
}

func probeFabricTopology(t *testing.T) {
	probeFabricSwitches(t)
	probeFabricDevices(t)
	fab := newTestFabricForFork(t)
	cables := fabric.ExportTopologyCables(fabric.ExportFabricTopology(fab))
	if len(*cables) > 0 {
		(*cables)[0].Fault = cablemodel.Fault{
			Kind:     cablemodel.FaultLoseSequence,
			Sequence: []uint{1, 2, 3},
		}
	}
	fork := fab.Fork()
	(*fabric.ExportTopologyStart(fabric.ExportFabricTopology(fab))) = time.Unix(9999, 0)
	if fabric.ExportTopologyStart(fabric.ExportFabricTopology(fork)).Unix() == 9999 {
		t.Errorf("fork cfg.Start changed when source mutated")
	}
	(*fabric.ExportTopologyStart(fabric.ExportFabricTopology(fork))) = time.Unix(8888, 0)
	if fabric.ExportTopologyStart(fabric.ExportFabricTopology(fab)).Unix() == 8888 {
		t.Errorf("source cfg.Start changed when fork mutated")
	}
	forkCables := fabric.ExportTopologyCables(fabric.ExportFabricTopology(fork))
	if len(*forkCables) > 0 && len((*forkCables)[0].Fault.Sequence) > 0 {
		(*forkCables)[0].Fault.Sequence[0] = 99
		if (*cables)[0].Fault.Sequence[0] == 99 {
			t.Error("source cable fault changed through fork")
		}
	}
}

func probeFabricLinks(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()
	(*fabric.ExportFabricLinks(fab))[0].A.Oper = port.Down
	if (*fabric.ExportFabricLinks(fork))[0].A.Oper == port.Down {
		t.Errorf("fork links[0].A.Oper changed when source mutated")
	}
	(*fabric.ExportFabricLinks(fork))[0].A.Oper = port.Down
	(*fabric.ExportFabricLinks(fab))[0].A.Oper = port.Up
	if (*fabric.ExportFabricLinks(fork))[0].A.Oper != port.Down {
		t.Errorf("source links mutation affected fork")
	}
}

func probeFabricLinkTrust(t *testing.T) {
	fab := newTestFabricForFork(t)
	(*fabric.ExportFabricLinkTrust(fab))[0] = fabric.MakeTestLinkTrust(fabric.ExportLinkTrustFields{Issues: []analysis.Issue{{Code: "source"}}})
	fork := fab.Fork()
	(*fabric.ExportLinkTrustIssues(&(*fabric.ExportFabricLinkTrust(fork))[0]))[0].Code = "fork"
	if (*fabric.ExportLinkTrustIssues(&(*fabric.ExportFabricLinkTrust(fab))[0]))[0].Code != "source" {
		t.Error("source link issue changed through fork")
	}
}

func probeFabricUncabled(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()
	keys := slices.SortedFunc(maps.Keys(*fabric.ExportFabricUncabled(fab)), func(a, b fabric.Endpoint) int { return strings.Compare(a.Canonical(), b.Canonical()) })
	if len(keys) == 0 {
		t.Fatal("fixture has no uncabled entry")
	}
	ep := keys[0]
	u := (*fabric.ExportFabricUncabled(fork))[ep]
	u.Endpoint.Port = "changed"
	(*fabric.ExportFabricUncabled(fork))[ep] = u
	if (*fabric.ExportFabricUncabled(fab))[ep].Endpoint.Port == "changed" {
		t.Error("source uncabled entry changed through fork")
	}
}

func probeFabricSwitches(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x88}
	if err := fabric.ExportFabricNodeDevice(fab, "sw1").(*vswitch.Switch).Learn([]bridge.Seed{{FID: 10, MAC: mac, Port: "1/1/1", Lifetime: bridge.Aging}}); err != nil {
		t.Fatalf("fab learn: %v", err)
	}
	if fabric.ExportFabricNodeDevice(fork, "sw1").(*vswitch.Switch).Forget(10, mac) {
		t.Errorf("fork switch learned entry learned on source")
	}

	forkMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x77}
	if err := fabric.ExportFabricNodeDevice(fork, "sw1").(*vswitch.Switch).Learn([]bridge.Seed{{FID: 10, MAC: forkMAC, Port: "1/1/1", Lifetime: bridge.Aging}}); err != nil {
		t.Fatalf("fork learn: %v", err)
	}
	if fabric.ExportFabricNodeDevice(fab, "sw1").(*vswitch.Switch).Forget(10, forkMAC) {
		t.Errorf("source switch learned entry learned on fork")
	}
}

func probeFabricDevices(t *testing.T) {
	h := newTestFabricForFork(t)
	forkHost := h.Fork()
	if fabric.ExportFabricNodeDevice(h, "h1") == fabric.ExportFabricNodeDevice(forkHost, "h1") {
		t.Error("host device aliased across fork")
	}
	if _, err := fabric.ExportFabricNodeDevice(h, "h1").LinkChange((*fabric.ExportFabricClock(h)), "", device.Link{State: port.Down}); err != nil {
		t.Fatal(err)
	}
	if source, forkState := fabric.ExportFabricNodeDevice(h, "h1").State((*fabric.ExportFabricClock(h))), fabric.ExportFabricNodeDevice(forkHost, "h1").State((*fabric.ExportFabricClock(h))); source.Ports[0].OperStatus != port.Down || forkState.Ports[0].OperStatus != port.Up {
		t.Errorf("host link states = %s/%s, want Down/Up", source.Ports[0].OperStatus, forkState.Ports[0].OperStatus)
	}
	initial := assembly.Spec(newTestFabricForFork(t)).Config()
	initial.Cables[2].LengthMeters = 1
	fab, err := assembly.New(initial)
	if err != nil {
		t.Fatal(err)
	}
	fork := fab.Fork()
	cfg := assembly.Spec(fork).Config().Switches["sw1"]
	cfg.Bridge = nil
	if err := assembly.Configure(fork, "sw1", cfg); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		fabric *fabric.Fabric
		fid    vlan.ID
	}{
		{"source", fab, 10},
		{"fork", fork, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.fabric.Inject(fabric.Injection{
				At:     tc.fabric.Clock(),
				Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
				Frame:  ethernet.Frame{Src: netaddr.MAC{0, 1, 2, 3, 4, 5}, Dst: netaddr.MAC{255, 255, 255, 255, 255, 255}},
			})
			if err != nil {
				t.Fatal(err)
			}
			entry, ok := tc.fabric.Step()
			if !ok || entry.Result == nil {
				t.Fatalf("step = %+v, ok %t, want forwarding result", entry, ok)
			}
			if entry.Result.Outcome != trace.Flooded || entry.Result.FID != tc.fid {
				t.Fatalf("outcome/FID = %s/%d, want Flooded/%d", entry.Result.Outcome, entry.Result.FID, tc.fid)
			}
		})
	}
}

func probeFabricByEnd(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()

	ep := fabric.Endpoint{Node: "h1"}
	refSrc, okSrc := (*fabric.ExportFabricByEnd(fab))[ep]
	refDst, okDst := (*fabric.ExportFabricByEnd(fork))[ep]
	if !okSrc || !okDst {
		t.Fatalf("byEnd missing endpoint")
	}
	if (*fabric.ExportLinkEndRefLink(&refSrc)) == (*fabric.ExportLinkEndRefLink(&refDst)) {
		t.Errorf("byEnd.link pointers aliased across fork")
	}
	if (*fabric.ExportLinkEndRefEnd(&refSrc)) == (*fabric.ExportLinkEndRefEnd(&refDst)) {
		t.Errorf("byEnd.end pointers aliased across fork")
	}
	refDstEnd := fabric.ExportLinkEndRefEnd(&refDst)
	(*refDstEnd).Oper = port.Down
	if (*fabric.ExportLinkEndRefEnd(&refSrc)).Oper != port.Up {
		t.Error("source link end changed through fork")
	}
}

func probeFabricQueue(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()
	(*fabric.ExportFabricQueue(fork))[0].Seq = 8888
	if (*fabric.ExportFabricQueue(fab))[0].Seq == 8888 {
		t.Error("source queue element changed through fork")
	}
}

func probeFabricInflight(t *testing.T) {
	fab := newTestFabricForFork(t)
	key := fabric.FrameID(1)
	(*fabric.ExportFabricInflight(fab))[key] = 1
	fork := fab.Fork()
	(*fabric.ExportFabricInflight(fork))[key] = 9
	if (*fabric.ExportFabricInflight(fab))[key] != 1 {
		t.Error("source Inflight entry changed through fork")
	}
}

func probeFabricHeld(t *testing.T) {
	fab := newTestFabricForFork(t)
	key := fabric.MakeTestHoldKey("sw1", 1)
	(*fabric.ExportFabricHeld(fab))[key] = 1
	fork := fab.Fork()
	(*fabric.ExportFabricHeld(fork))[key] = 9
	if (*fabric.ExportFabricHeld(fab))[key] != 1 {
		t.Error("source Held entry changed through fork")
	}
}

func probeFabricFlows(t *testing.T) {
	fab := newTestFabricForFork(t)
	*fabric.ExportFabricFlows(fab) = map[flow.FlowID]*flow.FlowStats{
		1: {Offered: 1, Drops: map[trace.Reason]uint64{cablemodel.ReasonBadFrame: 1}},
	}
	fork := fab.Fork()
	(*fabric.ExportFabricFlows(fork))[1].Offered = 9
	if (*fabric.ExportFabricFlows(fab))[1].Offered != 1 {
		t.Error("source flow changed through fork")
	}
	(*fabric.ExportFabricFlows(fork))[1].Drops[cablemodel.ReasonBadFrame] = 99
	if (*fabric.ExportFabricFlows(fab))[1].Drops[cablemodel.ReasonBadFrame] != 1 {
		t.Error("source flow map changed through fork")
	}
}

func probeFabricProtocolIssues(t *testing.T) {
	fab := newTestFabricForFork(t)
	*fabric.ExportFabricProtocolIssues(fab) = []analysis.Issue{{Code: "source-code"}}
	fork := fab.Fork()
	(*fabric.ExportFabricProtocolIssues(fork))[0].Code = "fork-code"
	*fabric.ExportFabricProtocolIssues(fork) = append(*fabric.ExportFabricProtocolIssues(fork), analysis.Issue{Code: "fork-only"})
	if got := *fabric.ExportFabricProtocolIssues(fab); len(got) != 1 || got[0].Code != "source-code" {
		t.Errorf("source protocol issues = %+v, changed through fork", got)
	}
}

func probeFabricDequeueItems(t *testing.T) {
	fab := newTestFabricForFork(t)
	key := fabric.Endpoint{Node: "existing"}
	(*fabric.ExportFabricDequeueItems(fab))[key] = 1
	fork := fab.Fork()
	(*fabric.ExportFabricDequeueItems(fork))[key] = 9
	if (*fabric.ExportFabricDequeueItems(fab))[key] != 1 {
		t.Error("source DequeueItems entry changed through fork")
	}
}

func probeFabricWakeItems(t *testing.T) {
	fab := newTestFabricForFork(t)
	key := "existing"
	(*fabric.ExportFabricWakeItems(fab))[key] = 1
	fork := fab.Fork()
	(*fabric.ExportFabricWakeItems(fork))[key] = 9
	if (*fabric.ExportFabricWakeItems(fab))[key] != 1 {
		t.Error("source WakeItems entry changed through fork")
	}
}

func probeFabricWakes(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()
	before := (*fabric.ExportFabricWakes(fab))["sw1"]
	(*fabric.ExportFabricWakes(fork))["sw1"] = before.Add(time.Second)
	if (*fabric.ExportFabricWakes(fab))["sw1"] != before {
		t.Error("source wake changed through fork")
	}
}

func probeFabricJourneys(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()
	(*fabric.ExportFabricJourneys(fork))[1].FrameID = 9
	if (*fabric.ExportFabricJourneys(fab))[1].FrameID != 1 {
		t.Error("source journey changed through fork")
	}
	(*fabric.ExportFabricJourneys(fork))[1].Entries[0].Device = "sw2"
	if (*fabric.ExportFabricJourneys(fab))[1].Entries[0].Device != "sw1" {
		t.Error("source journey entry changed through fork")
	}
}

func probeFabricEntered(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()
	ep := fabric.Endpoint{Node: "sw1", Port: "1/1/1"}
	(*fabric.ExportFabricEntered(fork))[1][ep] = false
	if !(*fabric.ExportFabricEntered(fab))[1][ep] {
		t.Error("source entered element changed through fork")
	}
}

func probeFabricCableCrossings(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()

	ep := fabric.Endpoint{Node: "sw1", Port: "1/1/1"}
	(*fabric.ExportFabricCableCrossings(fab))[ep] = 50
	if (*fabric.ExportFabricCableCrossings(fork))[ep] == 50 {
		t.Errorf("fork cableCrossings changed when source mutated")
	}
	(*fabric.ExportFabricCableCrossings(fork))[ep] = 70
	if (*fabric.ExportFabricCableCrossings(fab))[ep] == 70 {
		t.Errorf("source cableCrossings changed when fork mutated")
	}
}

func probeFabricBusyUntil(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()

	ep := fabric.Endpoint{Node: "sw1", Port: "1/1/1"}
	(*fabric.ExportFabricBusyUntil(fab))[ep] = time.Unix(5000, 0)
	if (*fabric.ExportFabricBusyUntil(fork))[ep].Unix() == 5000 {
		t.Errorf("fork busyUntil changed when source mutated")
	}
	(*fabric.ExportFabricBusyUntil(fork))[ep] = time.Unix(6000, 0)
	if (*fabric.ExportFabricBusyUntil(fab))[ep].Unix() == 6000 {
		t.Errorf("source busyUntil changed when fork mutated")
	}
}

func probeFabricEgress(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()
	ep := fabric.Endpoint{Node: "sw1", Port: "1/1/1"}
	forkQ := &fabric.ExportEgressQueuePending((*fabric.ExportFabricEgress(fork))[ep])[0][0]
	srcQ := &fabric.ExportEgressQueuePending((*fabric.ExportFabricEgress(fab))[ep])[0][0]
	*fabric.ExportQueuedSeq(forkQ) = 9
	if *fabric.ExportQueuedSeq(srcQ) != 1 {
		t.Error("source queued element changed through fork")
	}
	forkJourney := *fabric.ExportQueuedJourney(forkQ)
	srcJourney := *fabric.ExportQueuedJourney(srcQ)
	if forkJourney != nil && srcJourney != nil {
		forkJourney.FrameID = 99
		if srcJourney.FrameID == 99 {
			t.Error("source queued journey changed through fork")
		}
	}
}

func probeFabricCounters(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()

	ep := fabric.Endpoint{Node: "sw1", Port: "1/1/1"}
	(*fabric.ExportFabricCounters(fab))[ep].InBytes = 9999
	if (*fabric.ExportFabricCounters(fork))[ep].InBytes == 9999 {
		t.Errorf("fork counters changed when source mutated")
	}
	(*fabric.ExportFabricCounters(fork))[ep].InBytes = 8888
	if (*fabric.ExportFabricCounters(fab))[ep].InBytes == 8888 {
		t.Errorf("source counters changed when fork mutated")
	}
	(*fabric.ExportFabricCounters(fork))[ep].Discards[cablemodel.ReasonBadFrame] = 99
	if (*fabric.ExportFabricCounters(fab))[ep].Discards[cablemodel.ReasonBadFrame] != 5 {
		t.Error("source counter map changed through fork")
	}
}

func buildSteppableTwinFabrics(t *testing.T) (*fabric.Fabric, *fabric.Fabric) {
	t.Helper()

	b1 := port.NewBuilder()
	b1.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b1.Add(port.Port{Name: "1/1/24", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	pTable1, err := b1.Build()
	if err != nil {
		t.Fatalf("build sw1 ports: %v", err)
	}

	b2 := port.NewBuilder()
	b2.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b2.Add(port.Port{Name: "1/1/24", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	pTable2, err := b2.Build()
	if err != nil {
		t.Fatalf("build sw2 ports: %v", err)
	}

	vid10 := vlan.ID(10)
	newBridgeCfg := func() *bridge.Config {
		return &bridge.Config{
			AgingTime: 300 * time.Second,
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "VLAN10"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1":  {PVID: &vid10, Untagged: []vlan.ID{10}},
					"1/1/24": {Tagged: []vlan.ID{10}},
				},
			},
		}
	}

	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}
	t0 := time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)

	cfg1 := assembly.Config{
		Start: t0,
		Switches: map[string]vswitch.Config{
			"sw1": {Ports: pTable1, Bridge: newBridgeCfg()},
			"sw2": {Ports: pTable2, Bridge: newBridgeCfg()},
		},
		Hosts: map[string]hostmodel.Config{
			"h1": {Address: macH1},
			"h2": {Address: macH2},
		},
		Cables: []fabric.Cable{
			{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{Cable: cablemodel.Cable{LengthMeters: 50}, A: fabric.Endpoint{Node: "sw1", Port: "1/1/24"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/24"}},
			{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}, B: fabric.Endpoint{Node: "h2"}},
		},
		PhyAssumption: defaultPhyAssumption(),
	}
	cfg2 := cfg1.Clone()

	fab1, err := assembly.New(cfg1)
	if err != nil {
		t.Fatalf("New fab1: %v", err)
	}
	fab2, err := assembly.New(cfg2)
	if err != nil {
		t.Fatalf("New fab2: %v", err)
	}

	frame := ethernet.Frame{
		Src:       macH1,
		Dst:       macH2,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("test-payload-bytes"),
	}
	if _, err := fab1.Inject(fabric.Injection{At: t0.Add(time.Millisecond), Origin: fabric.Endpoint{Node: "h1"}, Frame: frame}); err != nil {
		t.Fatalf("fab1 Inject: %v", err)
	}
	if _, err := fab2.Inject(fabric.Injection{At: t0.Add(time.Millisecond), Origin: fabric.Endpoint{Node: "h1"}, Frame: frame}); err != nil {
		t.Fatalf("fab2 Inject: %v", err)
	}

	return fab1, fab2
}

func TestSameNextArrival(t *testing.T) {
	// Running a fork to quiescence does not perturb the source switch's next arrival relative to an unforked twin.
	fab, twin := buildSteppableTwinFabrics(t)

	// Step both 2 times mid-run
	for i := 0; i < 2; i++ {
		e1, ok1 := fab.Step()
		e2, ok2 := twin.Step()
		if !ok1 || !ok2 {
			t.Fatalf("step %d failed: ok1=%v, ok2=%v", i, ok1, ok2)
		}
		if e1.Kind != e2.Kind || e1.Device != e2.Device || e1.Port != e2.Port {
			t.Fatalf("step %d diverged: e1=%+v, e2=%+v", i, e1, e2)
		}
	}

	// Fork mid-run
	fork := fab.Fork()

	// Run the fork to quiescence
	for {
		_, ok := fork.Step()
		if !ok {
			break
		}
	}

	// Step the source once
	fabEntry, fabOk := fab.Step()
	twinEntry, twinOk := twin.Step()

	if fabOk != twinOk {
		t.Fatalf("fabOk=%v, twinOk=%v", fabOk, twinOk)
	}
	if fabEntry.Kind != twinEntry.Kind {
		t.Errorf("entry Kind = %v, want %v", fabEntry.Kind, twinEntry.Kind)
	}
	if fabEntry.Device != twinEntry.Device {
		t.Errorf("entry Device = %v, want %v", fabEntry.Device, twinEntry.Device)
	}
	if fabEntry.Port != twinEntry.Port {
		t.Errorf("entry Port = %v, want %v", fabEntry.Port, twinEntry.Port)
	}
	if !fabEntry.At.Equal(twinEntry.At) {
		t.Errorf("entry At = %v, want %v", fabEntry.At, twinEntry.At)
	}
}

func TestSiblingJourneys(t *testing.T) {
	// A fork continuing execution produces journey records identical to an unforked twin run from the same point.
	fab, twin := buildSteppableTwinFabrics(t)

	// Step both 2 times mid-run
	for i := 0; i < 2; i++ {
		fab.Step()
		twin.Step()
	}

	fork := fab.Fork()

	// Run fork to quiescence
	for {
		_, ok := fork.Step()
		if !ok {
			break
		}
	}

	// Run twin to quiescence
	for {
		_, ok := twin.Step()
		if !ok {
			break
		}
	}

	forkReports := fork.Report()
	twinReports := twin.Report()

	if len(forkReports) != len(twinReports) {
		t.Fatalf("report lengths differ: got %d, want %d", len(forkReports), len(twinReports))
	}

	for i := range forkReports {
		fj := forkReports[i]
		tj := twinReports[i]
		if fj.FrameID != tj.FrameID {
			t.Errorf("journey %d FrameID = %v, want %v", i, fj.FrameID, tj.FrameID)
		}
		if len(fj.Entries) != len(tj.Entries) {
			t.Errorf("journey %d entries len = %d, want %d", i, len(fj.Entries), len(tj.Entries))
			continue
		}
		for k := range fj.Entries {
			if fj.Entries[k].Kind != tj.Entries[k].Kind || fj.Entries[k].Device != tj.Entries[k].Device {
				t.Errorf("journey %d entry %d differs: got %+v, want %+v", i, k, fj.Entries[k], tj.Entries[k])
			}
		}
		if len(fj.Deliveries) != len(tj.Deliveries) {
			t.Errorf("journey %d deliveries len = %d, want %d", i, len(fj.Deliveries), len(tj.Deliveries))
		}
	}
}

func TestSnapshotImmutability(t *testing.T) {
	// A captured snapshot remains unaffected when the running fabric advances queue, counter, and FDB state.
	fab, _ := buildSteppableTwinFabrics(t)

	snap := fab.Snapshot()

	// Capture snapshot state
	initialClock := snap.Clock
	initialQueue := make([]fabric.Arrival, len(snap.Queue))
	for i, arr := range snap.Queue {
		initialQueue[i] = arr
		initialQueue[i].Frame.Payload = slices.Clone(arr.Frame.Payload)
	}
	initialQueued := maps.Clone(snap.Queued)
	initialLinksCount := len(snap.Links)
	initialDevicesLen := len(snap.Devices)
	initialSw1Entries := slices.Clone(snap.Devices["sw1"].Entries)
	initialBusy := maps.Clone(snap.Busy)
	initialFabricQueueLen := len((*fabric.ExportFabricQueue(fab)))

	// Step until queue length, counters, and sw1 forwarding database have all moved.
	steps := 0
	for steps < 50 {
		_, ok := fab.Step()
		if !ok {
			break
		}
		steps++

		queueMoved := len((*fabric.ExportFabricQueue(fab))) != initialFabricQueueLen
		fdbMoved := len(fabric.ExportFabricNodeDevice(fab, "sw1").(*vswitch.Switch).Entries()) > len(initialSw1Entries)
		countersMoved := false
		for _, c := range *fabric.ExportFabricCounters(fab) {
			if c.InBytes > 0 || c.OutBytes > 0 || c.InDiscards > 0 || c.OutDiscards > 0 {
				countersMoved = true
				break
			}
		}

		if queueMoved && countersMoved && fdbMoved {
			break
		}
	}

	if steps == 0 {
		t.Fatalf("fabric did not step")
	}
	if len((*fabric.ExportFabricQueue(fab))) == initialFabricQueueLen {
		t.Fatalf("fabric queue length did not move")
	}
	hasCounterMoved := false
	for _, c := range *fabric.ExportFabricCounters(fab) {
		if c.InBytes > 0 || c.OutBytes > 0 || c.InDiscards > 0 || c.OutDiscards > 0 {
			hasCounterMoved = true
			break
		}
	}
	if !hasCounterMoved {
		t.Fatalf("fabric counters did not move")
	}
	if len(fabric.ExportFabricNodeDevice(fab, "sw1").(*vswitch.Switch).Entries()) <= len(initialSw1Entries) {
		t.Fatalf("fabric sw1 entries did not move")
	}

	// Assert every snapshot field unchanged
	if !snap.Clock.Equal(initialClock) {
		t.Errorf("snapshot Clock changed: got %v, want %v", snap.Clock, initialClock)
	}
	if !reflect.DeepEqual(snap.Queue, initialQueue) {
		t.Errorf("snapshot Queue contents changed: got %+v, want %+v", snap.Queue, initialQueue)
	}
	if !reflect.DeepEqual(snap.Queued, initialQueued) {
		t.Errorf("snapshot Queued changed: got %+v, want %+v", snap.Queued, initialQueued)
	}
	if len(snap.Links) != initialLinksCount {
		t.Errorf("snapshot Links count changed: got %d, want %d", len(snap.Links), initialLinksCount)
	}
	if len(snap.Devices) != initialDevicesLen {
		t.Errorf("snapshot Devices len changed: got %d, want %d", len(snap.Devices), initialDevicesLen)
	}
	if !reflect.DeepEqual(snap.Devices["sw1"].Entries, initialSw1Entries) {
		t.Errorf("snapshot Devices[sw1] Entries contents changed: got %+v, want %+v", snap.Devices["sw1"].Entries, initialSw1Entries)
	}
	if !reflect.DeepEqual(snap.Busy, initialBusy) {
		t.Errorf("snapshot Busy contents changed: got %+v, want %+v", snap.Busy, initialBusy)
	}
}

func TestSnapshotPhase4b(t *testing.T) {
	// The snapshot exposes incomplete neighbor state and hold-queue depth for switch routing interfaces.
	b1 := port.NewBuilder()
	b1.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b1.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	pTable1, err := b1.Build()
	if err != nil {
		t.Fatalf("build sw1 ports: %v", err)
	}

	vid10 := vlan.ID(10)
	vid20 := vlan.ID(20)
	swMAC := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}
	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}
	t0 := time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)

	cfg := assembly.Config{
		Start: t0,
		Switches: map[string]vswitch.Config{
			"sw1": {
				MAC:   swMAC,
				Ports: pTable1,
				Bridge: &bridge.Config{
					AgingTime: 300 * time.Second,
					VLAN: &bridge.VLAN{
						Table: map[vlan.ID]string{10: "VLAN10", 20: "VLAN20"},
						Switchports: map[string]bridge.Switchport{
							"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
							"1/1/2": {PVID: &vid20, Untagged: []vlan.ID{20}},
						},
					},
				},
				Routing: &routing.Config{
					VRFs: map[string]routing.VRF{
						routing.DefaultVRF: {
							Interfaces: map[string]routing.Interface{
								"vlan10": {
									VLAN:     10,
									MAC:      swMAC,
									Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")},
								},
								"vlan20": {
									VLAN:     20,
									MAC:      swMAC,
									Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")},
								},
							},
						},
					},
				},
			},
		},
		Hosts: map[string]hostmodel.Config{
			"h1": {
				Address: macH1,
				IP: &hostmodel.IP{
					Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.7/24")},
					Gateway:   netip.MustParseAddr("10.0.10.1"),
					Neighbors: map[netip.Addr]netaddr.MAC{
						netip.MustParseAddr("10.0.10.1"): swMAC,
					},
				},
			},
			"h2": {Address: macH2},
		},
		Cables: []fabric.Cable{
			{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, B: fabric.Endpoint{Node: "h1"}},
			{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "h2"}},
		},
		PhyAssumption: defaultPhyAssumption(),
	}

	fab, err := assembly.New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	// Inject packet to 10.0.20.99 (subnet vlan20, unconfigured neighbor)
	unresolvedIP := netip.MustParseAddr("10.0.20.99")
	_, err = fab.Inject(fabric.Injection{
		At:     t0.Add(time.Millisecond),
		Origin: fabric.Endpoint{Node: "h1"},
		Packet: &fabric.Packet{
			To:       unresolvedIP,
			Protocol: 17,
			Payload:  []byte("probe"),
		},
	})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}

	// Step until the packet reaches sw1 and is held
	for i := 0; i < 10; i++ {
		entry, ok := fab.Step()
		if !ok {
			break
		}
		if entry.Device == "sw1" && entry.Kind == fabric.EntryHop && entry.Result != nil && entry.Result.Reason == routing.ReasonNeighborPending {
			break
		}
	}

	snap := fab.Snapshot()
	sw1Dev, ok := snap.Devices["sw1"]
	if !ok {
		t.Fatalf("Devices[sw1] missing from snapshot")
	}

	var found *device.NeighborEntry
	for i := range sw1Dev.Neighbors {
		if sw1Dev.Neighbors[i].Addr == unresolvedIP {
			found = &sw1Dev.Neighbors[i]
			break
		}
	}

	if found == nil {
		t.Fatalf("snapshot Devices[sw1] has no neighbor for %s; neighbors: %+v", unresolvedIP, sw1Dev.Neighbors)
	}

	if found.State != device.NeighborIncomplete {
		t.Errorf("neighbor state = %v, want %v", found.State, device.NeighborIncomplete)
	}
	if found.HoldDepth != 1 {
		t.Errorf("neighbor hold depth = %d, want 1", found.HoldDepth)
	}
}

func probeFabricArrivalQueue(t *testing.T) {
	probes := map[string]func(*testing.T){
		"queue": probeFabricQueue, "wakes": probeFabricWakes,
		"dequeueItems": probeFabricDequeueItems, "wakeItems": probeFabricWakeItems,
	}
	field, ok := reflect.TypeOf(fabric.Fabric{}).FieldByName("arrivalQueue")
	if !ok {
		t.Fatal("fabric has no arrivalQueue")
	}
	if field.Type.NumField() != len(probes) {
		t.Fatalf("queue fields = %d, want %d probes", field.Type.NumField(), len(probes))
	}
	for i := range field.Type.NumField() {
		name := field.Type.Field(i).Name
		probe, ok := probes[name]
		if !ok {
			t.Fatalf("queue field %q has no isolation probe", name)
		}
		probe(t)
	}
}
