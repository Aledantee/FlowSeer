package fabric

import (
	"maps"
	"net/netip"
	"reflect"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
)

var fabricDeepCopiedProbes = map[string]func(t *testing.T){
	"cfg":            probeFabricCfg,
	"links":          probeFabricLinks,
	"linkTrust":      probeFabricLinkTrust,
	"uncabled":       probeFabricUncabled,
	"switches":       probeFabricSwitches,
	"hostStacks":     probeFabricHostStacks,
	"byEnd":          probeFabricByEnd,
	"queue":          probeFabricQueue,
	"wakes":          probeFabricWakes,
	"dequeueItems":   probeFabricDequeueItems,
	"wakeItems":      probeFabricWakeItems,
	"journeys":       probeFabricJourneys,
	"entered":        probeFabricEntered,
	"cableCrossings": probeFabricCableCrossings,
	"busyUntil":      probeFabricBusyUntil,
	"egress":         probeFabricEgress,
	"counters":       probeFabricCounters,
	"unstatedBacked": probeFabricUnstatedBacked,
	"inflight":       probeFabricInflight,
	"heldAggregates": probeFabricHeldAggregates,
	"flows":          probeFabricFlows,
}

func probeFabricUnstatedBacked(t *testing.T) {
	fab := newTestFabricForFork(t)
	ep := Endpoint{Node: "sw1", Port: "1/1/1"}
	fab.unstatedBacked = map[Endpoint]struct{}{ep: {}}
	fork := fab.Fork()

	forkEP := Endpoint{Node: "sw1", Port: "1/1/2"}
	fork.unstatedBacked[forkEP] = struct{}{}
	if _, ok := fab.unstatedBacked[forkEP]; ok {
		t.Errorf("source unstatedBacked gained entry added to fork")
	}
	delete(fork.unstatedBacked, ep)
	if _, ok := fab.unstatedBacked[ep]; !ok {
		t.Errorf("source unstatedBacked lost entry deleted from fork")
	}
}

func probeFabricCfg(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()

	fab.cfg.Start = time.Unix(9999, 0)
	if fork.cfg.Start.Unix() == 9999 {
		t.Errorf("fork cfg.Start changed when source mutated")
	}

	fork.cfg.Start = time.Unix(8888, 0)
	if fab.cfg.Start.Unix() == 8888 {
		t.Errorf("source cfg.Start changed when fork mutated")
	}
}

func probeFabricLinks(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()

	fab.links[0].A.Oper = port.Down
	if fork.links[0].A.Oper == port.Down {
		t.Errorf("fork links[0].A.Oper changed when source mutated")
	}

	fork.links[0].A.Oper = port.Down
	fab.links[0].A.Oper = port.Up
	if fork.links[0].A.Oper != port.Down {
		t.Errorf("source links mutation affected fork")
	}
}

func probeFabricLinkTrust(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()

	fab.linkTrust = append(fab.linkTrust, linkTrust{issues: []analysis.Issue{{Code: "source-issue"}}})
	if len(fork.linkTrust) == len(fab.linkTrust) {
		t.Errorf("fork linkTrust slice grew when source mutated")
	}

	fork.linkTrust = append(fork.linkTrust, linkTrust{issues: []analysis.Issue{{Code: "fork-issue"}}})
	if fork.linkTrust[len(fork.linkTrust)-1].issues[0].Code != "fork-issue" {
		t.Errorf("fork linkTrust entry missing")
	}
}

func probeFabricUncabled(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()

	ep := Endpoint{Node: "sw1", Port: "1/1/99"}
	fab.uncabled[ep] = Uncabled{Endpoint: ep}
	if _, ok := fork.uncabled[ep]; ok {
		t.Errorf("fork uncabled map gained entry added to source")
	}

	forkEP := Endpoint{Node: "sw1", Port: "1/1/98"}
	fork.uncabled[forkEP] = Uncabled{Endpoint: forkEP}
	if _, ok := fab.uncabled[forkEP]; ok {
		t.Errorf("source uncabled map gained entry added to fork")
	}
}

func probeFabricSwitches(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x88}
	if err := fab.switches["sw1"].Learn([]bridge.Seed{{FID: 10, MAC: mac, Port: "1/1/1", Lifetime: bridge.Aging}}); err != nil {
		t.Fatalf("fab learn: %v", err)
	}
	if fork.switches["sw1"].Forget(10, mac) {
		t.Errorf("fork switch learned entry learned on source")
	}

	forkMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x77}
	if err := fork.switches["sw1"].Learn([]bridge.Seed{{FID: 10, MAC: forkMAC, Port: "1/1/1", Lifetime: bridge.Aging}}); err != nil {
		t.Fatalf("fork learn: %v", err)
	}
	if fab.switches["sw1"].Forget(10, forkMAC) {
		t.Errorf("source switch learned entry learned on fork")
	}
}

func probeFabricHostStacks(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()

	if fab.hostStacks["h1"] == fork.hostStacks["h1"] {
		t.Errorf("hostStacks pointers aliased across fork")
	}
}

func probeFabricByEnd(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()

	ep := Endpoint{Node: "h1"}
	refSrc, okSrc := fab.byEnd[ep]
	refDst, okDst := fork.byEnd[ep]
	if !okSrc || !okDst {
		t.Fatalf("byEnd missing endpoint")
	}
	if refSrc.link == refDst.link {
		t.Errorf("byEnd.link pointers aliased across fork")
	}
	if refSrc.end == refDst.end {
		t.Errorf("byEnd.end pointers aliased across fork")
	}
}

func probeFabricQueue(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()

	fab.queue = append(fab.queue, Arrival{Seq: 9999})
	if len(fork.queue) == len(fab.queue) {
		t.Errorf("fork queue grew when source queue appended")
	}

	fork.queue = append(fork.queue, Arrival{Seq: 8888})
	if fab.queue[len(fab.queue)-1].Seq == 8888 {
		t.Errorf("source queue saw fork arrival")
	}
}

func probeFabricInflight(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()

	fab.inflight[1] = 1
	if _, ok := fork.inflight[1]; ok {
		t.Errorf("fork inflight saw entry added to source")
	}

	fork.inflight[2] = 1
	if _, ok := fab.inflight[2]; ok {
		t.Errorf("source inflight saw entry added to fork")
	}
}

func probeFabricHeldAggregates(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()

	fab.heldAggregates["sw2"] = []FrameID{1}
	if _, ok := fork.heldAggregates["sw2"]; ok {
		t.Errorf("fork heldAggregates saw entry added to source")
	}

	fork.heldAggregates["sw3"] = []FrameID{2}
	if _, ok := fab.heldAggregates["sw3"]; ok {
		t.Errorf("source heldAggregates saw entry added to fork")
	}

	fork.heldAggregates["sw1"][0] = 3
	if fab.heldAggregates["sw1"][0] == 3 {
		t.Errorf("source heldAggregates slice changed when fork's changed")
	}
}

func probeFabricFlows(t *testing.T) {
	fab := newTestFabricForFork(t)
	fab.flows = make(map[FlowID]*FlowStats)
	fork := fab.Fork()

	fab.flows[1] = &FlowStats{Offered: 1}
	if _, ok := fork.flows[1]; ok {
		t.Errorf("fork flows saw entry added to source")
	}

	fork.flows[2] = &FlowStats{Offered: 2}
	if _, ok := fab.flows[2]; ok {
		t.Errorf("source flows saw entry added to fork")
	}
}

func probeFabricDequeueItems(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()

	fab.dequeueItems[Endpoint{Node: "src"}] = 1
	if _, ok := fork.dequeueItems[Endpoint{Node: "src"}]; ok {
		t.Errorf("fork dequeueItems saw entry added to source")
	}

	fork.dequeueItems[Endpoint{Node: "dst"}] = 2
	if _, ok := fab.dequeueItems[Endpoint{Node: "dst"}]; ok {
		t.Errorf("source dequeueItems saw entry added to fork")
	}
}

func probeFabricWakeItems(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()

	fab.wakeItems["src"] = 1
	if _, ok := fork.wakeItems["src"]; ok {
		t.Errorf("fork wakeItems saw entry added to source")
	}

	fork.wakeItems["dst"] = 2
	if _, ok := fab.wakeItems["dst"]; ok {
		t.Errorf("source wakeItems saw entry added to fork")
	}
}

func probeFabricWakes(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()

	fab.wakes["extra-src"] = time.Unix(2000, 0)
	if _, ok := fork.wakes["extra-src"]; ok {
		t.Errorf("fork wakes saw entry added to source")
	}

	fork.wakes["extra-fork"] = time.Unix(3000, 0)
	if _, ok := fab.wakes["extra-fork"]; ok {
		t.Errorf("source wakes saw entry added to fork")
	}
}

func probeFabricJourneys(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()

	fab.journeys[999] = &Journey{FrameID: 999}
	if _, ok := fork.journeys[999]; ok {
		t.Errorf("fork journeys saw entry added to source")
	}

	fork.journeys[888] = &Journey{FrameID: 888}
	if _, ok := fab.journeys[888]; ok {
		t.Errorf("source journeys saw entry added to fork")
	}
}

func probeFabricEntered(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()

	ep := Endpoint{Node: "sw2", Port: "1/1/24"}
	fab.entered[1][ep] = true
	if fork.entered[1][ep] {
		t.Errorf("fork entered map mutated when source mutated")
	}

	forkEP := Endpoint{Node: "sw2", Port: "1/1/1"}
	fork.entered[1][forkEP] = true
	if fab.entered[1][forkEP] {
		t.Errorf("source entered map mutated when fork mutated")
	}
}

func probeFabricCableCrossings(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()

	ep := Endpoint{Node: "sw1", Port: "1/1/1"}
	fab.cableCrossings[ep] = 50
	if fork.cableCrossings[ep] == 50 {
		t.Errorf("fork cableCrossings changed when source mutated")
	}

	fork.cableCrossings[ep] = 70
	if fab.cableCrossings[ep] == 70 {
		t.Errorf("source cableCrossings changed when fork mutated")
	}
}

func probeFabricBusyUntil(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()

	ep := Endpoint{Node: "sw1", Port: "1/1/1"}
	fab.busyUntil[ep] = time.Unix(5000, 0)
	if fork.busyUntil[ep].Unix() == 5000 {
		t.Errorf("fork busyUntil changed when source mutated")
	}

	fork.busyUntil[ep] = time.Unix(6000, 0)
	if fab.busyUntil[ep].Unix() == 6000 {
		t.Errorf("source busyUntil changed when fork mutated")
	}
}

func probeFabricEgress(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()

	ep := Endpoint{Node: "sw1", Port: "1/1/1"}
	fab.egress[ep].pending[0] = append(fab.egress[ep].pending[0], queued{seq: 999})
	if len(fork.egress[ep].pending[0]) == len(fab.egress[ep].pending[0]) {
		t.Errorf("fork egress pending queue grew when source mutated")
	}

	fork.egress[ep].pending[0] = append(fork.egress[ep].pending[0], queued{seq: 888})
	if fab.egress[ep].pending[0][len(fab.egress[ep].pending[0])-1].seq == 888 {
		t.Errorf("source egress pending queue saw fork mutation")
	}
}

func probeFabricCounters(t *testing.T) {
	fab := newTestFabricForFork(t)
	fork := fab.Fork()

	ep := Endpoint{Node: "sw1", Port: "1/1/1"}
	fab.counters[ep].InOctets = 9999
	if fork.counters[ep].InOctets == 9999 {
		t.Errorf("fork counters changed when source mutated")
	}

	fork.counters[ep].InOctets = 8888
	if fab.counters[ep].InOctets == 8888 {
		t.Errorf("source counters changed when fork mutated")
	}
}

func buildSteppableTwinFabrics(t *testing.T) (*Fabric, *Fabric) {
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

	cfg1 := Config{
		Start: t0,
		Switches: map[string]vswitch.Config{
			"sw1": {Ports: pTable1, Bridge: newBridgeCfg()},
			"sw2": {Ports: pTable2, Bridge: newBridgeCfg()},
		},
		Hosts: map[string]Host{
			"h1": {Address: macH1},
			"h2": {Address: macH2},
		},
		Cables: []Cable{
			{A: Endpoint{Node: "h1"}, B: Endpoint{Node: "sw1", Port: "1/1/1"}, LengthMeters: 5},
			{A: Endpoint{Node: "sw1", Port: "1/1/24"}, B: Endpoint{Node: "sw2", Port: "1/1/24"}, LengthMeters: 50},
			{A: Endpoint{Node: "sw2", Port: "1/1/1"}, B: Endpoint{Node: "h2"}, LengthMeters: 5},
		},
		PhyAssumption: defaultPhyAssumption(),
	}
	cfg2 := cfg1.Clone()

	fab1, err := New(cfg1)
	if err != nil {
		t.Fatalf("New fab1: %v", err)
	}
	fab2, err := New(cfg2)
	if err != nil {
		t.Fatalf("New fab2: %v", err)
	}

	frame := ethernet.Frame{
		Src:       macH1,
		Dst:       macH2,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("test-payload-bytes"),
	}
	if _, err := fab1.Inject(Injection{At: t0.Add(time.Millisecond), Origin: Endpoint{Node: "h1"}, Frame: frame}); err != nil {
		t.Fatalf("fab1 Inject: %v", err)
	}
	if _, err := fab2.Inject(Injection{At: t0.Add(time.Millisecond), Origin: Endpoint{Node: "h1"}, Frame: frame}); err != nil {
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
	initialQueue := make([]Arrival, len(snap.Queue))
	for i, arr := range snap.Queue {
		initialQueue[i] = arr
		initialQueue[i].Frame.Payload = slices.Clone(arr.Frame.Payload)
	}
	initialQueued := maps.Clone(snap.Queued)
	initialLinksCount := len(snap.Links)
	initialDevicesLen := len(snap.Devices)
	initialSw1Entries := slices.Clone(snap.Devices["sw1"].Entries)
	initialBusy := maps.Clone(snap.Busy)
	initialFabricQueueLen := len(fab.queue)

	// Step until queue length, counters, and sw1 forwarding database have all moved.
	steps := 0
	for steps < 50 {
		_, ok := fab.Step()
		if !ok {
			break
		}
		steps++

		queueMoved := len(fab.queue) != initialFabricQueueLen
		fdbMoved := len(fab.switches["sw1"].Entries()) > len(initialSw1Entries)
		countersMoved := false
		for _, c := range fab.counters {
			if c.InOctets > 0 || c.OutOctets > 0 || c.InDiscards > 0 || c.OutDiscards > 0 {
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
	if len(fab.queue) == initialFabricQueueLen {
		t.Fatalf("fabric queue length did not move")
	}
	hasCounterMoved := false
	for _, c := range fab.counters {
		if c.InOctets > 0 || c.OutOctets > 0 || c.InDiscards > 0 || c.OutDiscards > 0 {
			hasCounterMoved = true
			break
		}
	}
	if !hasCounterMoved {
		t.Fatalf("fabric counters did not move")
	}
	if len(fab.switches["sw1"].Entries()) <= len(initialSw1Entries) {
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

	cfg := Config{
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
		Hosts: map[string]Host{
			"h1": {
				Address: macH1,
				IP: &HostIP{
					Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.7/24")},
					Gateway:   netip.MustParseAddr("10.0.10.1"),
					Neighbors: map[netip.Addr]netaddr.MAC{
						netip.MustParseAddr("10.0.10.1"): swMAC,
					},
				},
			},
			"h2": {Address: macH2},
		},
		Cables: []Cable{
			{A: Endpoint{Node: "sw1", Port: "1/1/1"}, B: Endpoint{Node: "h1"}, LengthMeters: 5},
			{A: Endpoint{Node: "sw1", Port: "1/1/2"}, B: Endpoint{Node: "h2"}, LengthMeters: 5},
		},
		PhyAssumption: defaultPhyAssumption(),
	}

	fab, err := New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	// Inject packet to 10.0.20.99 (subnet vlan20, unconfigured neighbor)
	unresolvedIP := netip.MustParseAddr("10.0.20.99")
	_, err = fab.Inject(Injection{
		At:     t0.Add(time.Millisecond),
		Origin: Endpoint{Node: "h1"},
		Packet: &Packet{
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
		if entry.Device == "sw1" && entry.Kind == EntryHop && entry.Result != nil && entry.Result.Reason == routing.ReasonNeighborPending {
			break
		}
	}

	snap := fab.Snapshot()
	sw1Dev, ok := snap.Devices["sw1"]
	if !ok {
		t.Fatalf("Devices[sw1] missing from snapshot")
	}

	var found *routing.NeighborEntry
	for i := range sw1Dev.Neighbors {
		if sw1Dev.Neighbors[i].Addr == unresolvedIP {
			found = &sw1Dev.Neighbors[i]
			break
		}
	}

	if found == nil {
		t.Fatalf("snapshot Devices[sw1] has no neighbor for %s; neighbors: %+v", unresolvedIP, sw1Dev.Neighbors)
	}

	if found.State != routing.NeighborIncomplete {
		t.Errorf("neighbor state = %v, want %v", found.State, routing.NeighborIncomplete)
	}
	if found.HoldDepth != 1 {
		t.Errorf("neighbor hold depth = %d, want 1", found.HoldDepth)
	}
}
