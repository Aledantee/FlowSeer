package fabric_test

import (
	"maps"
	"net/netip"
	"reflect"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/assembly"

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

var fabricDeepCopiedProbes = map[string]func(t *testing.T){
	"arrivalQueue":   probeFabricArrivalQueue,
	"topology":       probeFabricTopology,
	"links":          probeFabricLinks,
	"linkTrust":      probeFabricLinkTrust,
	"uncabled":       probeFabricUncabled,
	"byEnd":          probeFabricByEnd,
	"journeys":       probeFabricJourneys,
	"entered":        probeFabricEntered,
	"cableCrossings": probeFabricCableCrossings,
	"busyUntil":      probeFabricBusyUntil,
	"egress":         probeFabricEgress,
	"counters":       probeFabricCounters,
	"unstatedBacked": probeFabricUnstatedBacked,
	"inflight":       probeFabricInflight,
	"held":           probeFabricHeld,
	"flows":          probeFabricFlows,
}

func probeFabricUnstatedBacked(t *testing.T) {
	fab := newTestFabricForFork(t)
	ep := fabric.Endpoint{Node: "sw1", Port: "1/1/1"}
	(*fabric.ExportFabricUnstatedBacked(fab)) = map[fabric.Endpoint]trace.EvidenceRef{ep: "source-ref"}
	fork := fab.Fork()

	forkEP := fabric.Endpoint{Node: "sw1", Port: "1/1/2"}
	(*fabric.ExportFabricUnstatedBacked(fork))[forkEP] = "fork-ref"
	if _, ok := (*fabric.ExportFabricUnstatedBacked(fab))[forkEP]; ok {
		t.Errorf("source unstatedBacked gained entry added to fork")
	}
	delete((*fabric.ExportFabricUnstatedBacked(fork)), ep)
	if _, ok := (*fabric.ExportFabricUnstatedBacked(fab))[ep]; !ok {
		t.Errorf("source unstatedBacked lost entry deleted from fork")
	}
	if got, want := (*fabric.ExportFabricUnstatedBacked(fab))[ep], trace.EvidenceRef("source-ref"); got != want {
		t.Errorf("source evidence ref = %q, want %q", got, want)
	}
}

func probeFabricTopology(t *testing.T) {
	probeFabricSwitches(t)
	probeFabricDevices(t)
	fab := newTestFabricForFork(t)
	fork := fab.Fork()
	(*fabric.ExportTopologyStart(fabric.ExportFabricTopology(fab))) = time.Unix(9999, 0)
	if fabric.ExportTopologyStart(fabric.ExportFabricTopology(fork)).Unix() == 9999 {
		t.Errorf("fork cfg.Start changed when source mutated")
	}
	(*fabric.ExportTopologyStart(fabric.ExportFabricTopology(fork))) = time.Unix(8888, 0)
	if fabric.ExportTopologyStart(fabric.ExportFabricTopology(fab)).Unix() == 8888 {
		t.Errorf("source cfg.Start changed when fork mutated")
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
	fork := fab.Fork()
	(*fabric.ExportFabricLinkTrust(fab)) = append((*fabric.ExportFabricLinkTrust(fab)), fabric.MakeTestLinkTrust(fabric.ExportLinkTrustFields{Issues: []analysis.Issue{{Code: "source-issue"}}}))
	if len((*fabric.ExportFabricLinkTrust(fork))) == len((*fabric.ExportFabricLinkTrust(fab))) {
		t.Errorf("fork linkTrust slice grew when source mutated")
	}
	(*fabric.ExportFabricLinkTrust(fork)) = append((*fabric.ExportFabricLinkTrust(fork)), fabric.MakeTestLinkTrust(fabric.ExportLinkTrustFields{Issues: []analysis.Issue{{Code: "fork-issue"}}}))
	if (*fabric.ExportLinkTrustIssues(&(*fabric.ExportFabricLinkTrust(fork))[len((*fabric.ExportFabricLinkTrust(fork)))-1]))[0].Code != "fork-issue" {
		t.Errorf("fork linkTrust entry missing")
	}
}

func probeFabricUncabled(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()

	ep := fabric.Endpoint{Node: "sw1", Port: "1/1/99"}
	(*fabric.ExportFabricUncabled(fab))[ep] = fabric.Uncabled{Endpoint: ep}
	if _, ok := (*fabric.ExportFabricUncabled(fork))[ep]; ok {
		t.Errorf("fork uncabled map gained entry added to source")
	}

	forkEP := fabric.Endpoint{Node: "sw1", Port: "1/1/98"}
	(*fabric.ExportFabricUncabled(fork))[forkEP] = fabric.Uncabled{Endpoint: forkEP}
	if _, ok := (*fabric.ExportFabricUncabled(fab))[forkEP]; ok {
		t.Errorf("source uncabled map gained entry added to fork")
	}
}

func probeFabricSwitches(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x88}
	if err := fab.Device("sw1").(*vswitch.Switch).Learn([]bridge.Seed{{FID: 10, MAC: mac, Port: "1/1/1", Lifetime: bridge.Aging}}); err != nil {
		t.Fatalf("fab learn: %v", err)
	}
	if fork.Device("sw1").(*vswitch.Switch).Forget(10, mac) {
		t.Errorf("fork switch learned entry learned on source")
	}

	forkMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x77}
	if err := fork.Device("sw1").(*vswitch.Switch).Learn([]bridge.Seed{{FID: 10, MAC: forkMAC, Port: "1/1/1", Lifetime: bridge.Aging}}); err != nil {
		t.Fatalf("fork learn: %v", err)
	}
	if fab.Device("sw1").(*vswitch.Switch).Forget(10, forkMAC) {
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
				At:     tc.fabric.Snapshot().Clock,
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
}

func probeFabricQueue(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()
	(*fabric.ExportFabricQueue(fab)) = append((*fabric.ExportFabricQueue(fab)), fabric.MakeTestArrival(fabric.ExportArrivalFields{Seq: 9999}))
	if len((*fabric.ExportFabricQueue(fork))) == len((*fabric.ExportFabricQueue(fab))) {
		t.Errorf("fork queue grew when source queue appended")
	}
	(*fabric.ExportFabricQueue(fork)) = append((*fabric.ExportFabricQueue(fork)), fabric.MakeTestArrival(fabric.ExportArrivalFields{Seq: 8888}))
	if (*fabric.ExportFabricQueue(fab))[len((*fabric.ExportFabricQueue(fab)))-1].Seq == 8888 {
		t.Errorf("source queue saw fork arrival")
	}
	(*fabric.ExportFabricQueue(fab)) = []fabric.Arrival{{Kind: fabric.ArrivalHost, Frame: ethernet.Frame{Payload: []byte{1}}}}
	fork = fab.Fork()
	(*fabric.ExportFabricQueue(fork))[0].Frame.Payload[0] = 2
	if got := (*fabric.ExportFabricQueue(fab))[0].Frame.Payload[0]; got != 1 {
		t.Errorf("source queued host payload = %d, want 1", got)
	}
}

func probeFabricInflight(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()
	(*fabric.ExportFabricInflight(fab))[1] = 1
	if _, ok := (*fabric.ExportFabricInflight(fork))[1]; ok {
		t.Errorf("fork inflight saw entry added to source")
	}
	(*fabric.ExportFabricInflight(fork))[2] = 1
	if _, ok := (*fabric.ExportFabricInflight(fab))[2]; ok {
		t.Errorf("source inflight saw entry added to fork")
	}
}

func probeFabricHeld(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()
	(*fabric.ExportFabricHeld(fab))[fabric.MakeTestHoldKey("sw2", 1)] = 1
	if _, ok := (*fabric.ExportFabricHeld(fork))[fabric.MakeTestHoldKey("sw2", 1)]; ok {
		t.Errorf("fork held saw entry added to source")
	}
	(*fabric.ExportFabricHeld(fork))[fabric.MakeTestHoldKey("sw3", 1)] = 2
	if _, ok := (*fabric.ExportFabricHeld(fab))[fabric.MakeTestHoldKey("sw3", 1)]; ok {
		t.Errorf("source held saw entry added to fork")
	}
	(*fabric.ExportFabricHeld(fork))[fabric.MakeTestHoldKey("sw1", 1)] = 3
	if (*fabric.ExportFabricHeld(fab))[fabric.MakeTestHoldKey("sw1", 1)] == 3 {
		t.Errorf("source held mapping changed when fork's changed")
	}
}

func probeFabricFlows(t *testing.T) {
	fab := newTestFabricForFork(t)
	(*fabric.ExportFabricFlows(fab)) = make(map[fabric.FlowID]*fabric.FlowStats)
	fork := fab.Fork()
	(*fabric.ExportFabricFlows(fab))[1] = &fabric.FlowStats{Offered: 1}
	if _, ok := (*fabric.ExportFabricFlows(fork))[1]; ok {
		t.Errorf("fork flows saw entry added to source")
	}
	(*fabric.ExportFabricFlows(fork))[2] = &fabric.FlowStats{Offered: 2}
	if _, ok := (*fabric.ExportFabricFlows(fab))[2]; ok {
		t.Errorf("source flows saw entry added to fork")
	}
}

func probeFabricDequeueItems(t *testing.T) {
	fab := newTestFabricForFork(t)
	(*fabric.ExportFabricDequeueItems(fab))[fabric.Endpoint{Node: "existing"}] = 0
	fork := fab.Fork()
	(*fabric.ExportFabricDequeueItems(fab))[fabric.Endpoint{Node: "src"}] = 1
	if _, ok := (*fabric.ExportFabricDequeueItems(fork))[fabric.Endpoint{Node: "src"}]; ok {
		t.Errorf("fork dequeueItems saw entry added to source")
	}
	(*fabric.ExportFabricDequeueItems(fork))[fabric.Endpoint{Node: "dst"}] = 2
	if _, ok := (*fabric.ExportFabricDequeueItems(fab))[fabric.Endpoint{Node: "dst"}]; ok {
		t.Errorf("source dequeueItems saw entry added to fork")
	}
}

func probeFabricWakeItems(t *testing.T) {
	fab := newTestFabricForFork(t)
	(*fabric.ExportFabricWakeItems(fab))["existing"] = 0
	fork := fab.Fork()
	(*fabric.ExportFabricWakeItems(fab))["src"] = 1
	if _, ok := (*fabric.ExportFabricWakeItems(fork))["src"]; ok {
		t.Errorf("fork wakeItems saw entry added to source")
	}
	(*fabric.ExportFabricWakeItems(fork))["dst"] = 2
	if _, ok := (*fabric.ExportFabricWakeItems(fab))["dst"]; ok {
		t.Errorf("source wakeItems saw entry added to fork")
	}
}

func probeFabricWakes(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()
	(*fabric.ExportFabricWakes(fab))["extra-src"] = time.Unix(2000, 0)
	if _, ok := (*fabric.ExportFabricWakes(fork))["extra-src"]; ok {
		t.Errorf("fork wakes saw entry added to source")
	}
	(*fabric.ExportFabricWakes(fork))["extra-fork"] = time.Unix(3000, 0)
	if _, ok := (*fabric.ExportFabricWakes(fab))["extra-fork"]; ok {
		t.Errorf("source wakes saw entry added to fork")
	}
}

func probeFabricJourneys(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()
	(*fabric.ExportFabricJourneys(fab))[999] = fabric.MakeTestJourneyPointer(fabric.ExportJourneyFields{FrameID: 999})
	if _, ok := (*fabric.ExportFabricJourneys(fork))[999]; ok {
		t.Errorf("fork journeys saw entry added to source")
	}
	(*fabric.ExportFabricJourneys(fork))[888] = fabric.MakeTestJourneyPointer(fabric.ExportJourneyFields{FrameID: 888})
	if _, ok := (*fabric.ExportFabricJourneys(fab))[888]; ok {
		t.Errorf("source journeys saw entry added to fork")
	}
}

func probeFabricEntered(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()

	ep := fabric.Endpoint{Node: "sw2", Port: "1/1/24"}
	(*fabric.ExportFabricEntered(fab))[1][ep] = true
	if (*fabric.ExportFabricEntered(fork))[1][ep] {
		t.Errorf("fork entered map mutated when source mutated")
	}

	forkEP := fabric.Endpoint{Node: "sw2", Port: "1/1/1"}
	(*fabric.ExportFabricEntered(fork))[1][forkEP] = true
	if (*fabric.ExportFabricEntered(fab))[1][forkEP] {
		t.Errorf("source entered map mutated when fork mutated")
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
	fabric.ExportEgressQueuePending((*fabric.ExportFabricEgress(fab))[ep])[0] = append(fabric.ExportEgressQueuePending((*fabric.ExportFabricEgress(fab))[ep])[0], fabric.MakeTestQueued(fabric.ExportQueuedFields{Seq: 999}))
	if len(fabric.ExportEgressQueuePending((*fabric.ExportFabricEgress(fork))[ep])[0]) == len(fabric.ExportEgressQueuePending((*fabric.ExportFabricEgress(fab))[ep])[0]) {
		t.Errorf("fork egress pending queue grew when source mutated")
	}
	fabric.ExportEgressQueuePending((*fabric.ExportFabricEgress(fork))[ep])[0] = append(fabric.ExportEgressQueuePending((*fabric.ExportFabricEgress(fork))[ep])[0], fabric.MakeTestQueued(fabric.ExportQueuedFields{Seq: 888}))
	if (*fabric.ExportQueuedSeq(&fabric.ExportEgressQueuePending((*fabric.ExportFabricEgress(fab))[ep])[0][len(fabric.ExportEgressQueuePending((*fabric.ExportFabricEgress(fab))[ep])[0])-1])) == 888 {
		t.Errorf("source egress pending queue saw fork mutation")
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
		fdbMoved := len(fab.Device("sw1").(*vswitch.Switch).Entries()) > len(initialSw1Entries)
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
	if len(fab.Device("sw1").(*vswitch.Switch).Entries()) <= len(initialSw1Entries) {
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
	field, ok := reflect.TypeOf(fabric.MakeTestFabric(fabric.ExportFabricFields{})).FieldByName("arrivalQueue")
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
