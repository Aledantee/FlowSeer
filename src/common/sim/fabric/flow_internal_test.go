package fabric_test

import (
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/internal/simtest"

	"go.aledante.io/FlowSeer/src/common/sim/assembly"

	reflectormodel "go.aledante.io/FlowSeer/src/common/sim/device/reflector"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"

	hostmodel "go.aledante.io/FlowSeer/src/common/sim/device/host"

	"go.aledante.io/FlowSeer/src/common/net/arp"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/filter"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func injectHeldPair(t *testing.T, retention fabric.JourneyRetention) (*fabric.Fabric, [2]fabric.FrameID, netaddr.MAC) {
	t.Helper()
	fab, swMAC, srcMAC := heldPropertyFixture(t)
	var ids [2]fabric.FrameID
	for i, addr := range []string{"10.0.20.7", "10.0.20.8"} {
		fid, err := fab.Inject(fabric.Injection{
			At: fab.Clock(), Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			Frame: heldPropertyFrame(t, srcMAC, swMAC, netip.MustParseAddr("10.0.10.7"), netip.MustParseAddr(addr), []byte("same payload")),
			Flow:  21, Retention: retention,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = fid
	}
	for range 2 {
		if _, ok := fab.Step(); !ok {
			t.Fatal("held frame was not stepped")
		}
	}
	return fab, ids, swMAC
}

func releaseHeldAddress(t *testing.T, fab *fabric.Fabric, swMAC netaddr.MAC, addr string) {
	t.Helper()
	_, err := fab.Inject(fabric.Injection{
		At: fab.Clock().Add(time.Millisecond), Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
		Frame: heldPropertyARPReply(t, netaddr.MAC{0, 0x11, 0x22, 0x33, 0x44, 0x77}, netip.MustParseAddr(addr), swMAC),
	})
	if err != nil {
		t.Fatal(err)
	}
	until := fab.Clock().Add(10 * time.Millisecond)
	for len(fab.Snapshot().Queue) > 0 && !fab.Snapshot().Queue[0].At.After(until) {
		if _, ok := fab.Step(); !ok {
			t.Fatal("release did not advance")
		}
	}
}

func TestHeldIsAGauge(t *testing.T) {
	fab, _, swMAC := injectHeldPair(t, fabric.RetainAggregate)
	if got := fab.Flows()[21].Held; got != 2 {
		t.Fatalf("Held = %d, want 2", got)
	}
	releaseHeldAddress(t, fab, swMAC, "10.0.20.8")
	stats := fab.Flows()[21]
	if stats.Held != 1 || stats.Delivered["h2"] != 1 {
		t.Fatalf("Held/Delivered = %d/%d, want 1/1", stats.Held, stats.Delivered["h2"])
	}
}

func TestReleaseOutOfOrder(t *testing.T) {
	fab, ids, swMAC := injectHeldPair(t, fabric.RetainJourney)
	releaseHeldAddress(t, fab, swMAC, "10.0.20.8")
	var checked int
	for _, j := range fab.Report() {
		if j.FrameID == ids[0] {
			if j.State != fabric.JourneyPending || len(j.Deliveries) != 0 {
				t.Fatalf("first holder = %+v, want pending without deliveries", j)
			}
			checked++
		}
		if j.FrameID == ids[1] {
			if j.State != fabric.JourneyDelivered || len(j.Deliveries) != 1 || j.Deliveries[0].Host != "h2" {
				t.Fatalf("second holder state/deliveries = %s/%+v, want Delivered/h2", j.State, j.Deliveries)
			}
			checked++
		}
	}
	if checked != 2 {
		t.Fatalf("checked holders = %d, want 2", checked)
	}
}

// TestAggregateBroadcastFoldsOnce covers a broadcast aggregate injection on a
// three-host VLAN: after the run drains, no frame is in flight and the flow
// holds one offered frame, folded exactly once.
func TestAggregateBroadcastFoldsOnce(t *testing.T) {
	t0 := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	fab, cfg := newTestThreeHostFabric(t, t0)

	broadcast := ethernet.Frame{
		Src:     cfg.Hosts["h1"].Address,
		Dst:     netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		Payload: []byte("broadcast"),
	}
	if _, err := fab.Inject(fabric.Injection{
		At:        t0.Add(time.Millisecond),
		Origin:    fabric.Endpoint{Node: "h1"},
		Frame:     broadcast,
		Retention: fabric.RetainAggregate,
		Flow:      3,
	}); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if res := fab.Run(fabric.RunOptions{Budget: 100, Window: 0}); res.Stop != fabric.StopQueueDrained {
		t.Fatalf("Run stop = %s, want %s", res.Stop, fabric.StopQueueDrained)
	}

	if len((*fabric.ExportFabricInflight(fab))) != 0 {
		t.Errorf("inflight = %+v, want empty after the run drained", (*fabric.ExportFabricInflight(fab)))
	}
	stats := fab.Flows()[3]
	if stats.Offered != 1 {
		t.Errorf("Offered = %d, want 1", stats.Offered)
	}
	if stats.Delivered["h2"] != 1 || stats.Delivered["h3"] != 1 {
		t.Errorf("Delivered = %+v, want one each at h2 and h3", stats.Delivered)
	}
}

// TestAggregateFreesJourneys covers the scale promise: a thousand aggregated
// frames leave no per-frame journey or re-entry set behind once they settle.
func TestAggregateFreesJourneys(t *testing.T) {
	t0 := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	fab, cfg := newTestThreeHostFabric(t, t0)

	frame := ethernet.Frame{
		Src:     cfg.Hosts["h1"].Address,
		Dst:     cfg.Hosts["h2"].Address,
		Payload: make([]byte, 46),
	}
	for range 1000 {
		if _, err := fab.Inject(fabric.Injection{
			At:        t0.Add(time.Millisecond),
			Origin:    fabric.Endpoint{Node: "h1"},
			Frame:     frame,
			Retention: fabric.RetainAggregate,
			Flow:      7,
		}); err != nil {
			t.Fatalf("Inject: %v", err)
		}
	}
	if res := fab.Run(fabric.RunOptions{Budget: 1_000_000, Window: 0}); res.Stop != fabric.StopQueueDrained {
		t.Fatalf("Run stop = %s, want %s", res.Stop, fabric.StopQueueDrained)
	}

	if len((*fabric.ExportFabricJourneys(fab))) != 0 {
		t.Errorf("journeys = %d, want only protocol journeys (none here)", len((*fabric.ExportFabricJourneys(fab))))
	}
	if len((*fabric.ExportFabricEntered(fab))) != 0 {
		t.Errorf("entered = %d, want only protocol frames (none here)", len((*fabric.ExportFabricEntered(fab))))
	}
	if reported := fab.Report(); len(reported) != 0 {
		t.Errorf("Report held %d journeys, want none", len(reported))
	}
}

// TestFloodedFlowFoldsOnceNotTwice drives a retained flooded flow through its
// settle and then settles the same frame again: a settled journey folds once.
func TestFloodedFlowFoldsOnceNotTwice(t *testing.T) {
	t0 := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	fab, cfg := newTestThreeHostFabric(t, t0)

	broadcast := ethernet.Frame{
		Src:     cfg.Hosts["h1"].Address,
		Dst:     netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		Payload: []byte("broadcast"),
	}
	fid, err := fab.Inject(fabric.Injection{
		At:     t0.Add(time.Millisecond),
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  broadcast,
		Flow:   3,
	})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if res := fab.Run(fabric.RunOptions{Budget: 100, Window: 0}); res.Stop != fabric.StopQueueDrained {
		t.Fatalf("Run stop = %s, want %s", res.Stop, fabric.StopQueueDrained)
	}

	if got := fab.Flows()[3].Offered; got != 1 {
		t.Fatalf("Offered = %d, want 1", got)
	}
	fabric.ExportFabricSettle(fab, fid)
	fabric.ExportFabricSettle(fab, fid)
	if got := fab.Flows()[3].Offered; got != 1 {
		t.Errorf("Offered = %d after two more settles, want 1", got)
	}
}

// TestInflightMatchesContainersAcrossFixtures checks, after every step, that
// each frame's in-flight count equals the arrivals it holds in the queue and
// the egress queues, across loop, mirror, reflector, and cable-loss traffic.
func TestInflightMatchesContainersAcrossFixtures(t *testing.T) {
	t0 := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		fab  *fabric.Fabric
	}{
		{"cable-loss", invariantFabric(t, "cable-loss", t0)},
		{"loop", invariantFabric(t, "loop", t0)},
		{"mirror", invariantFabric(t, "mirror", t0)},
		{"reflector", invariantFabric(t, "reflector", t0)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			driveAndCheckInflight(t, tc.fab, t0)
		})
	}
}

func driveAndCheckInflight(t *testing.T, fab *fabric.Fabric, t0 time.Time) {
	t.Helper()
	sawInFlight := 0
	for _, dst := range []netaddr.MAC{
		{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		{0x00, 0x11, 0x22, 0x33, 0x44, 0x02},
	} {
		if _, err := fab.Inject(fabric.Injection{
			At:     t0.Add(time.Millisecond),
			Origin: fabric.Endpoint{Node: "h1"},
			Frame:  ethernet.Frame{Src: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}, Dst: dst, Payload: []byte("x")},
		}); err != nil {
			t.Fatalf("Inject: %v", err)
		}
		sawInFlight += checkInflightMatchesContainers(t, fab)
	}

	for range 300 {
		if _, ok := fab.Step(); !ok {
			break
		}
		sawInFlight += checkInflightMatchesContainers(t, fab)
	}

	if sawInFlight == 0 {
		t.Fatal("no frame was ever observed in flight; the invariant held vacuously")
	}
}

func checkInflightMatchesContainers(t *testing.T, fab *fabric.Fabric) int {
	t.Helper()
	want := make(map[fabric.FrameID]int)
	for _, arr := range *fabric.ExportFabricQueue(fab) {
		if arr.Kind == fabric.ArrivalFrame || arr.Kind == fabric.ArrivalHost {
			want[arr.FrameID]++
		}
	}
	for _, q := range *fabric.ExportFabricEgress(fab) {
		for pcp := range 8 {
			for _, item := range fabric.ExportEgressQueuePending(q)[pcp] {
				want[(*fabric.ExportQueuedFid(&item))]++
			}
		}
	}

	for fid, n := range want {
		if got := (*fabric.ExportFabricInflight(fab))[fid]; got != n {
			t.Fatalf("inflight[%d] = %d, want %d held in the queue and egress queues", fid, got, n)
		}
	}
	for fid, got := range *fabric.ExportFabricInflight(fab) {
		if want[fid] != got {
			t.Fatalf("inflight[%d] = %d with %d in the containers", fid, got, want[fid])
		}
	}

	return len(want)
}

// invariantFabric builds a two-switch chain with the named traffic: a cut
// trunk for the cable-loss case, a second parallel trunk for a loop, a mirror
// to an analyzer, or a reflector hung off a spare port.
func invariantFabric(t *testing.T, variant string, t0 time.Time) *fabric.Fabric {
	t.Helper()

	sw1Ports, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/24", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
	if err != nil {
		t.Fatalf("build sw1 ports: %v", err)
	}
	sw2Ports, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/24", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
	if err != nil {
		t.Fatalf("build sw2 ports: %v", err)
	}

	vid10 := vlan.ID(10)
	bridgeCfg := func(names ...string) *bridge.Config {
		ports := make(map[string]bridge.Switchport, len(names))
		for _, name := range names {
			ports[name] = bridge.Switchport{PVID: &vid10, Untagged: []vlan.ID{10}}
		}

		return &bridge.Config{VLAN: &bridge.VLAN{Table: map[vlan.ID]string{10: "vlan10"}, Switchports: ports}}
	}

	sw1 := vswitch.Config{Ports: sw1Ports, Bridge: bridgeCfg("1/1/1", "1/1/2", "1/1/3", "1/1/24")}
	sw2 := vswitch.Config{Ports: sw2Ports, Bridge: bridgeCfg("1/1/1", "1/1/2", "1/1/24")}
	cables := []fabric.Cable{
		{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
		{Cable: cablemodel.Cable{LengthMeters: 5, Medium: cablemodel.MultimodeFiber}, A: fabric.Endpoint{Node: "sw1", Port: "1/1/24"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/24"}},
		{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}, B: fabric.Endpoint{Node: "h2"}},
	}

	hosts := map[string]hostmodel.Config{
		"h1": {Address: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}},
		"h2": {Address: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}},
	}

	switch variant {
	case "cable-loss":
		cables[1].Fault = cablemodel.Fault{Kind: cablemodel.FaultLoseEveryNth, N: 1}
	case "loop":
		cables = append(cables, fabric.Cable{
			Cable: cablemodel.Cable{LengthMeters: 5, Medium: cablemodel.MultimodeFiber},
			A:     fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/2"},
		})
	case "mirror":
		hosts["an"] = hostmodel.Config{Address: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x09}, Accept: hostmodel.Accept{Promiscuous: true}}
		cables = append(cables, fabric.Cable{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "sw1", Port: "1/1/3"}, B: fabric.Endpoint{Node: "an"}})
		sw1.Traffic = &traffic.Config{Mirrors: []traffic.Mirror{{Name: "analyzer", SelectAll: true, OutputPort: "1/1/3"}}}
	case "mirror-reject":
		hosts["an"] = hostmodel.Config{Address: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x09}}
		cables = append(cables, fabric.Cable{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "sw1", Port: "1/1/3"}, B: fabric.Endpoint{Node: "an"}})
		sw1.Traffic = &traffic.Config{Mirrors: []traffic.Mirror{{Name: "analyzer", SelectAll: true, OutputPort: "1/1/3"}}}
	case "reflector":
		reflec := reflectormodel.Config{
			Address: netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x01, 0xaa},
			Ports:   map[string]phy.Ethernet{"rp1": {}},
			Attachments: map[string]reflectormodel.Attachment{
				"a": {Port: "rp1", VLAN: &vid10, Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.9/24")}},
			},
		}
		cables = append(cables, fabric.Cable{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "sw1", Port: "1/1/3"}, B: fabric.Endpoint{Node: "reflector", Port: "rp1"}})
		cfg := assembly.Config{
			Start:         t0,
			PhyAssumption: testPhyAssumption(),
			Switches:      map[string]vswitch.Config{"sw1": sw1, "sw2": sw2},
			Hosts:         hosts,
			Reflectors:    map[string]reflectormodel.Config{"reflector": reflec},
			Cables:        cables,
		}
		return newInvariantFabric(t, cfg)
	}

	cfg := assembly.Config{
		Start:         t0,
		PhyAssumption: testPhyAssumption(),
		Switches:      map[string]vswitch.Config{"sw1": sw1, "sw2": sw2},
		Hosts:         hosts,
		Cables:        cables,
	}
	return newInvariantFabric(t, cfg)
}

func newInvariantFabric(t *testing.T, cfg assembly.Config) *fabric.Fabric {
	t.Helper()
	fab, err := assembly.New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	return fab
}

// TestMirrorCopyFoldsIntoCopies covers a flow's mirror copies: they fold
// under the mirror's name rather than under the stream's deliveries.
func TestMirrorCopyFoldsIntoCopies(t *testing.T) {
	t0 := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	fab := invariantFabric(t, "mirror", t0)

	frame := ethernet.Frame{
		Src:     netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01},
		Dst:     netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02},
		Payload: []byte("mirrored"),
	}
	if _, err := fab.Inject(fabric.Injection{
		At:        t0.Add(time.Millisecond),
		Origin:    fabric.Endpoint{Node: "h1"},
		Frame:     frame,
		Retention: fabric.RetainAggregate,
		Flow:      5,
	}); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if res := fab.Run(fabric.RunOptions{Budget: 100, Window: 0}); res.Stop != fabric.StopQueueDrained {
		t.Fatalf("Run stop = %s, want %s", res.Stop, fabric.StopQueueDrained)
	}

	stats := fab.Flows()[5]
	if stats.Copies["analyzer"] == 0 {
		t.Errorf("Copies = %+v, want the analyzer mirror's copy", stats.Copies)
	}
}

// TestMirrorCopyStaysOutOfTheStreamCounters covers a flow whose frame is
// mirrored: the copy folds under Copies alone, so the stream's latency and
// outcome counters describe the stream, not the copy. A promiscuous analyzer
// accepts the copy; a non-promiscuous one refuses it. Either way the copy
// contributes no latency and no rejection to the stream that was delivered.
func TestMirrorCopyStaysOutOfTheStreamCounters(t *testing.T) {
	t0 := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	frame := ethernet.Frame{
		Src:     netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01},
		Dst:     netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02},
		Payload: []byte("mirrored"),
	}

	t.Run("accepted copy", func(t *testing.T) {
		fab := invariantFabric(t, "mirror", t0)
		if _, err := fab.Inject(fabric.Injection{
			At:        t0.Add(time.Millisecond),
			Origin:    fabric.Endpoint{Node: "h1"},
			Frame:     frame,
			Retention: fabric.RetainAggregate,
			Flow:      5,
		}); err != nil {
			t.Fatalf("Inject: %v", err)
		}
		if res := fab.Run(fabric.RunOptions{Budget: 100, Window: 0}); res.Stop != fabric.StopQueueDrained {
			t.Fatalf("Run stop = %s, want %s", res.Stop, fabric.StopQueueDrained)
		}

		stats := fab.Flows()[5]
		if stats.Copies["analyzer"] != 1 {
			t.Errorf("Copies = %+v, want one analyzer copy", stats.Copies)
		}
		if stats.Latency.Count != 1 {
			t.Errorf("Latency.Count = %d, want 1: only the stream's own delivery", stats.Latency.Count)
		}
		if stats.Delivered["h2"] != 1 {
			t.Errorf("Delivered = %+v, want one to h2", stats.Delivered)
		}
	})

	t.Run("rejected copy", func(t *testing.T) {
		fab := invariantFabric(t, "mirror-reject", t0)
		if _, err := fab.Inject(fabric.Injection{
			At:        t0.Add(time.Millisecond),
			Origin:    fabric.Endpoint{Node: "h1"},
			Frame:     frame,
			Retention: fabric.RetainAggregate,
			Flow:      6,
		}); err != nil {
			t.Fatalf("Inject: %v", err)
		}
		if res := fab.Run(fabric.RunOptions{Budget: 100, Window: 0}); res.Stop != fabric.StopQueueDrained {
			t.Fatalf("Run stop = %s, want %s", res.Stop, fabric.StopQueueDrained)
		}

		stats := fab.Flows()[6]
		if stats.Rejected != 0 {
			t.Errorf("Rejected = %d, want 0: the analyzer refused the copy, not the stream", stats.Rejected)
		}
		if stats.Delivered["h2"] != 1 {
			t.Errorf("Delivered = %+v, want one to h2", stats.Delivered)
		}
	})
}

// TestHeldFlowCountsHeld routes an aggregated packet through a neighbor hold
// and checks the flow counted it under Held.
func TestHeldFlowCountsHeld(t *testing.T) {
	t0 := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	ports, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
	if err != nil {
		t.Fatalf("build switch ports: %v", err)
	}

	vid10, vid20 := vlan.ID(10), vlan.ID(20)
	swMAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x77}
	addrH2 := netip.MustParseAddr("10.0.20.7")

	cfg := simtest.RoutedFixture(
		t0,
		map[string]vswitch.Config{"sw1": {
			MAC:   swMAC,
			Ports: ports,
			Bridge: &bridge.Config{VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &vid20, Untagged: []vlan.ID{20}},
				},
			}},
			Routing: &routing.Config{VRFs: map[string]routing.VRF{routing.DefaultVRF: {
				NeighborPolicy: routing.NeighborPolicy{RetransTimer: time.Hour},
				Interfaces: map[string]routing.Interface{
					"vlan10": {VLAN: 10, MAC: swMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
					"vlan20": {VLAN: 20, MAC: swMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
				},
			}}},
		}},
		map[string]hostmodel.Config{
			"h1": {
				Address: macH1,
				IP: &hostmodel.IP{
					Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.7/24")},
					Gateway:   netip.MustParseAddr("10.0.10.1"),
					Neighbors: map[netip.Addr]netaddr.MAC{netip.MustParseAddr("10.0.10.1"): swMAC},
				},
			},
			"h2": {Address: macH2},
		},
		[]fabric.Cable{
			{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "h2"}},
		},
	)
	cfg.PhyAssumption = testPhyAssumption()
	fab := newInvariantFabric(t, cfg)

	if _, err := fab.Inject(fabric.Injection{
		At:        t0,
		Origin:    fabric.Endpoint{Node: "h1"},
		Packet:    &fabric.Packet{To: addrH2, Protocol: 17, Payload: []byte("ping")},
		Retention: fabric.RetainAggregate,
		Flow:      9,
	}); err != nil {
		t.Fatalf("Inject packet: %v", err)
	}
	fab.Run(fabric.RunOptions{Budget: 5, Window: 0})

	stats := fab.Flows()[9]
	if stats.Held != 1 {
		t.Errorf("Held = %d, want 1", stats.Held)
	}
}

// heldPropertyFrame builds an IPv4 frame a switch with a vlan10 interface
// routes: the caller injects it tagged at the interface's port, and the switch
// holds it when the destination's neighbor is unresolved.
func heldPropertyFrame(t *testing.T, src, dst netaddr.MAC, saddr, daddr netip.Addr, payload []byte) ethernet.Frame {
	t.Helper()
	pkt, err := ip.Header{Src: saddr, Dst: daddr, HopLimit: 64, Protocol: 17, V4: &ip.V4{}}.Encode(payload)
	if err != nil {
		t.Fatalf("encode IPv4 packet: %v", err)
	}

	return ethernet.Frame{
		Src:       src,
		Dst:       dst,
		EtherType: ethernet.EtherTypeIPv4,
		Tags:      []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10}},
		Payload:   pkt,
	}
}

func heldPropertyARPReply(t *testing.T, senderMAC netaddr.MAC, senderAddr netip.Addr, switchMAC netaddr.MAC) ethernet.Frame {
	t.Helper()
	frame, err := arp.Encode(arp.Message{
		HardwareType: 1,
		ProtocolType: 0x0800,
		Operation:    arp.Reply,
		SenderMAC:    senderMAC,
		SenderAddr:   senderAddr,
		TargetMAC:    switchMAC,
		TargetAddr:   netip.MustParseAddr("10.0.20.1"),
	}, switchMAC)
	if err != nil {
		t.Fatalf("encode ARP reply: %v", err)
	}

	return frame
}

// heldPropertyFixture builds the switch the property runs on: one routed port
// per VLAN, a host behind each, and nothing configured for the 10.0.20.0/24
// neighbors, so a frame routed to a 10.0.20.x address is held until an ARP
// reply observes it.
func heldPropertyFixture(t *testing.T) (*fabric.Fabric, netaddr.MAC, netaddr.MAC) {
	t.Helper()

	ports, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
	if err != nil {
		t.Fatalf("build switch ports: %v", err)
	}

	vid10, vid20 := vlan.ID(10), vlan.ID(20)
	swMAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x77}

	cfg := simtest.RoutedFixture(
		time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC),
		map[string]vswitch.Config{"sw1": {
			MAC:   swMAC,
			Ports: ports,
			Bridge: &bridge.Config{VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &vid20, Untagged: []vlan.ID{20}},
				},
			}},
			Routing: &routing.Config{VRFs: map[string]routing.VRF{routing.DefaultVRF: {
				Interfaces: map[string]routing.Interface{
					"vlan10": {VLAN: 10, MAC: swMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
					"vlan20": {VLAN: 20, MAC: swMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
				},
			}}},
		}},
		map[string]hostmodel.Config{
			"h1": {Address: macH1, IP: &hostmodel.IP{
				Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.7/24")},
				Gateway:   netip.MustParseAddr("10.0.10.1"),
				Neighbors: map[netip.Addr]netaddr.MAC{netip.MustParseAddr("10.0.10.1"): swMAC},
			}},
			"h2": {Address: macH2},
		},
		[]fabric.Cable{
			{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "h2"}},
		},
	)
	cfg.PhyAssumption = testPhyAssumption()

	fab, err := assembly.New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	return fab, swMAC, macH1
}

// heldPropertyRun records the continued holders that survived retention.
type heldPropertyRun struct {
	fab       *fabric.Fabric
	releaseOf map[fabric.FrameID]fabric.FrameID
	heldIDs   []fabric.FrameID
}

// runHeldProperty drives the fixture with the flow frames either retained or
// aggregated. The fixture holds three frames for three unresolved addresses on
// one device at once: two flow frames and one frame with no flow. Each address
// is resolved in turn, while the later ones are still held, so every release
// happens beside a held frame another retention kept. Four more frames for a
// fourth address exceed the default neighbor HoldDepth of three, so the oldest
// is evicted; that address is never resolved, and the three unanswered
// solicitations fail the rest after three seconds. The replies land before that timeout.
func runHeldProperty(t *testing.T, aggregated bool) heldPropertyRun {
	t.Helper()

	fab, swMAC, macH1 := heldPropertyFixture(t)
	t0 := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	saddr := netip.MustParseAddr("10.0.10.7")

	flowRetention := fabric.RetainJourney
	if aggregated {
		flowRetention = fabric.RetainAggregate
	}

	injectFrame := func(at time.Time, daddr netip.Addr, payload []byte, flow fabric.FlowID, retention fabric.JourneyRetention) fabric.FrameID {
		fid, err := fab.Inject(fabric.Injection{
			At:        at,
			Origin:    fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			Frame:     heldPropertyFrame(t, macH1, swMAC, saddr, daddr, payload),
			Retention: retention,
			Flow:      flow,
		})
		if err != nil {
			t.Fatalf("Inject frame to %s: %v", daddr, err)
		}

		return fid
	}
	injectReply := func(at time.Time, senderMAC netaddr.MAC, senderAddr netip.Addr) {
		if _, err := fab.Inject(fabric.Injection{
			At:     at,
			Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
			Frame:  heldPropertyARPReply(t, senderMAC, senderAddr, swMAC),
		}); err != nil {
			t.Fatalf("Inject ARP reply for %s: %v", senderAddr, err)
		}
	}

	addrA := netip.MustParseAddr("10.0.20.7")
	addrB := netip.MustParseAddr("10.0.20.8")
	addrC := netip.MustParseAddr("10.0.20.9")
	addrD := netip.MustParseAddr("10.0.20.10")
	macB := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x88}

	heldIDs := []fabric.FrameID{
		injectFrame(t0.Add(1*time.Millisecond), addrA, []byte("holder-a"), 11, flowRetention),
		injectFrame(t0.Add(2*time.Millisecond), addrB, []byte("holder-b"), 0, fabric.RetainJourney),
		injectFrame(t0.Add(3*time.Millisecond), addrD, []byte("holder-d"), 13, flowRetention),
	}
	injectReply(t0.Add(4*time.Millisecond), macB, addrA)
	injectReply(t0.Add(5*time.Millisecond), macB, addrB)
	injectReply(t0.Add(6*time.Millisecond), macB, addrD)
	for i := range 4 {
		heldIDs = append(heldIDs, injectFrame(
			t0.Add(time.Duration(7+i)*time.Millisecond), addrC, []byte("evicted-c"), 12, flowRetention,
		))
	}

	fab.Run(fabric.RunOptions{Budget: 100, Window: 0})

	releaseOf := make(map[fabric.FrameID]fabric.FrameID)
	for _, fid := range heldIDs[:3] {
		if aggregated && fid != heldIDs[1] {
			continue
		}
		for _, j := range fab.Report() {
			if j.FrameID == fid && j.State == fabric.JourneyRejected {
				releaseOf[fid] = fid
			}
		}
	}

	return heldPropertyRun{fab: fab, releaseOf: releaseOf, heldIDs: heldIDs}
}

func TestAggregateRetentionPreservesReleaseAttribution(t *testing.T) {
	retained := runHeldProperty(t, false)
	aggregated := runHeldProperty(t, true)
	if len(retained.releaseOf) != 3 || len(aggregated.releaseOf) != 1 || aggregated.releaseOf[aggregated.heldIDs[1]] != aggregated.heldIDs[1] {
		t.Fatalf("continued holders = %v/%v, want three retained and one non-flow holder", retained.releaseOf, aggregated.releaseOf)
	}
	for _, run := range []heldPropertyRun{retained, aggregated} {
		if len(*fabric.ExportFabricHeld(run.fab)) != 0 || fabric.ExportFabricHasPendingJourneys(run.fab) {
			t.Fatalf("hold exits left keys or pending journeys")
		}
		for fid := range *fabric.ExportFabricEntered(run.fab) {
			if (*fabric.ExportFabricJourneys(run.fab))[fid] == nil {
				t.Fatalf("entered frame %d has no journey", fid)
			}
		}
	}
	for fid, j := range *fabric.ExportFabricJourneys(aggregated.fab) {
		if j.Injection.Retention == fabric.RetainAggregate || j.Protocol {
			t.Fatalf("unretained journey %d survived settle", fid)
		}
	}
	for _, flow := range []fabric.FlowID{11, 13} {
		if stats := aggregated.fab.Flows()[flow]; stats.Rejected != 1 || stats.Held != 0 {
			t.Fatalf("flow %d = %+v, want one rejection and no hold", flow, stats)
		}
	}
	stats := aggregated.fab.Flows()[12]
	if stats.Drops[routing.ReasonNeighborHoldOverflow] != 1 || stats.Drops[routing.ReasonNeighborMiss] != 3 || stats.Held != 0 {
		t.Fatalf("evicted/timed out flow = %+v, want one overflow and three misses", stats)
	}
}

func TestHoldExits(t *testing.T) {
	var cases int
	for _, exit := range []string{"released", "timed out", "overflow", "filtered", "reconfigured"} {
		for _, retention := range []fabric.JourneyRetention{fabric.RetainJourney, fabric.RetainAggregate} {
			for _, reverse := range []bool{false, true} {
				name := fmt.Sprintf("%s/retention=%d/reverse=%t", exit, retention, reverse)
				t.Run(name, func(t *testing.T) {
					checkHoldExit(t, exit, retention, reverse)
					cases++
				})
			}
		}
	}
	if cases != 20 {
		t.Fatalf("checked cases = %d, want 20", cases)
	}
}

func checkHoldExit(t *testing.T, exit string, retention fabric.JourneyRetention, reverse bool) {
	t.Helper()
	base, swMAC, srcMAC := heldPropertyFixture(t)
	cfg := assembly.Spec(base).Config()
	sw := cfg.Switches["sw1"]
	sw.STP = &stp.Config{Ports: map[string]stp.Port{"1/1/1": {AdminEdge: true}, "1/1/2": {AdminEdge: true}}}
	vrf := sw.Routing.VRFs[routing.DefaultVRF]
	vrf.NeighborPolicy.HoldDepth = 1
	sw.Routing.VRFs[routing.DefaultVRF] = vrf
	if exit == "filtered" {
		sw.Filter = &filter.Config{
			Sets:     map[string]filter.RuleSet{"refuse": {Default: filter.Drop}},
			Bindings: []filter.Binding{{Interface: "vlan20", Direction: filter.Out, Set: "refuse"}},
		}
	}
	cfg.Switches["sw1"] = sw
	if exit == "reconfigured" || exit == "timed out" {
		second := sw.Clone()
		second.MAC[5]++
		secondVRF := second.Routing.VRFs[routing.DefaultVRF]
		for name, iface := range secondVRF.Interfaces {
			iface.MAC = second.MAC
			secondVRF.Interfaces[name] = iface
		}
		if exit == "timed out" {
			if reverse {
				vrf.NeighborPolicy.RetransTimer = 2 * time.Second
				sw.Routing.VRFs[routing.DefaultVRF] = vrf
				cfg.Switches["sw1"] = sw
				secondVRF.NeighborPolicy.RetransTimer = time.Second
			} else {
				secondVRF.NeighborPolicy.RetransTimer = 2 * time.Second
			}
		}
		second.Routing.VRFs[routing.DefaultVRF] = secondVRF
		cfg.Switches["sw2"] = second
		cfg.Hosts["h3"] = hostmodel.Config{Address: netaddr.MAC{2, 0, 0, 0, 0, 3}}
		cfg.Hosts["h4"] = hostmodel.Config{Address: netaddr.MAC{2, 0, 0, 0, 0, 4}}
		cfg.Cables = append(cfg.Cables,
			fabric.Cable{A: fabric.Endpoint{Node: "h3"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}},
			fabric.Cable{A: fabric.Endpoint{Node: "sw2", Port: "1/1/2"}, B: fabric.Endpoint{Node: "h4"}})
	}
	fab, err := assembly.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	addresses := [2]string{"10.0.20.7", "10.0.20.8"}
	if reverse && exit != "timed out" && exit != "reconfigured" {
		addresses[0], addresses[1] = addresses[1], addresses[0]
	}
	var ids [2]fabric.FrameID
	for i, addr := range addresses {
		offset := i + 1
		node := "sw1"
		if (exit == "reconfigured" || exit == "timed out") && i == 1 {
			node = "sw2"
		}
		routerMAC := cfg.Switches[node].MAC
		ids[i], err = fab.Inject(fabric.Injection{
			At:        cfg.Start.Add(time.Duration(offset) * time.Millisecond),
			Origin:    fabric.Endpoint{Node: node, Port: "1/1/1"},
			Frame:     heldPropertyFrame(t, srcMAC, routerMAC, netip.MustParseAddr("10.0.10.7"), netip.MustParseAddr(addr), []byte(addr)),
			Retention: retention, Flow: fabric.FlowID(i + 1),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	var holds int
	for steps := 0; holds < 2 && steps < 1000; steps++ {
		_, ok := fab.Step()
		if !ok {
			t.Fatal("ended before both holds")
		}
		holds = len(*fabric.ExportFabricHeld(fab))
	}
	if holds != 2 {
		t.Fatalf("held frames = %d, want 2 within 1000 steps", holds)
	}
	if len(*fabric.ExportFabricHeld(fab)) != 2 {
		t.Fatalf("held keys = %d, want 2", len(*fabric.ExportFabricHeld(fab)))
	}
	var exitOrder []fabric.FrameID
	ended := make(map[fabric.FrameID]bool)
	exitTimes := make(map[fabric.FrameID]time.Time)
	observeExit := func() {
		for _, fid := range ids {
			present := false
			for _, held := range *fabric.ExportFabricHeld(fab) {
				if held == fid {
					present = true
				}
			}
			if !present && !ended[fid] {
				ended[fid] = true
				exitOrder = append(exitOrder, fid)
				exitTimes[fid] = fab.Clock()
			}
		}
	}
	wantReason := trace.Reason("")
	switch exit {
	case "released", "filtered":
		for i, addr := range []string{"10.0.20.7", "10.0.20.8"} {
			_, err = fab.Inject(fabric.Injection{
				At:     cfg.Start.Add(time.Duration(10+i) * time.Millisecond),
				Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
				Frame:  heldPropertyARPReply(t, netaddr.MAC{0, 0x11, 0x22, 0x33, 0x44, 0x77}, netip.MustParseAddr(addr), swMAC),
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		if exit == "filtered" {
			wantReason = filter.ReasonFilterDrop
		}
	case "overflow":
		wantReason = routing.ReasonNeighborHoldOverflow
		for i, addr := range []string{"10.0.20.7", "10.0.20.8"} {
			_, err = fab.Inject(fabric.Injection{
				At:     cfg.Start.Add(time.Duration(10+i) * time.Millisecond),
				Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
				Frame:  heldPropertyFrame(t, srcMAC, swMAC, netip.MustParseAddr("10.0.10.7"), netip.MustParseAddr(addr), []byte("replacement")),
			})
			if err != nil {
				t.Fatal(err)
			}
		}
	case "timed out":
		wantReason = routing.ReasonNeighborMiss
	case "reconfigured":
		wantReason = routing.ReasonNeighborMiss
		nodes := []string{"sw1", "sw2"}
		if reverse {
			slices.Reverse(nodes)
		}
		cfgClock := fab.Clock()
		for _, node := range nodes {
			sw = assembly.Spec(fab).Config().Switches[node]
			vrf = sw.Routing.VRFs[routing.DefaultVRF]
			vrf.Routes = append(vrf.Routes, routing.Route{Prefix: netip.MustParsePrefix("10.99.0.0/16"), Interface: "vlan20"})
			sw.Routing.VRFs[routing.DefaultVRF] = vrf
			if err := assembly.Configure(fab, node, sw); err != nil {
				t.Fatal(err)
			}
			observeExit()
		}
		if len(exitOrder) != 2 {
			t.Fatalf("reconfigured exit count before step loop = %d, want 2", len(exitOrder))
		}
		for _, fid := range ids {
			if !exitTimes[fid].Equal(cfgClock) {
				t.Fatalf("reconfigured exit time for frame %d = %v, want configure clock %v", fid, exitTimes[fid], cfgClock)
			}
		}
	}
	for steps := 0; len(exitOrder) < 2 && steps < 1000; steps++ {
		if _, ok := fab.Step(); !ok {
			t.Fatal("ended before both hold exits")
		}
		observeExit()
	}
	wantOrder := []fabric.FrameID{ids[0], ids[1]}
	if reverse {
		slices.Reverse(wantOrder)
	}
	if !slices.Equal(exitOrder, wantOrder) {
		t.Fatalf("hold exit order = %v, want %v", exitOrder, wantOrder)
	}

	result, err := fab.RunScenario(fabric.Scenario{Name: "hold exits"}, fabric.RunOptions{Budget: 1000, Window: 3})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stop != fabric.StopConverged {
		t.Fatalf("stop = %s, want Converged", result.Stop)
	}
	if len(*fabric.ExportFabricHeld(fab)) != 0 {
		t.Fatalf("held keys remain: %v", *fabric.ExportFabricHeld(fab))
	}
	for i, fid := range ids {
		stats := fab.Flows()[fabric.FlowID(i+1)]
		if stats.Held != 0 || stats.Offered != 1 {
			t.Fatalf("flow %d Held/Offered = %d/%d, want 0/1", i+1, stats.Held, stats.Offered)
		}
		if wantReason == "" {
			if stats.Delivered["h2"] != 1 || stats.Latency.Count != 1 || len(stats.Drops) != 0 {
				t.Fatalf("flow %d = %+v, want one delivery/sample and no drops", i+1, stats)
			}
			if stats.Latency.Min < 8*time.Millisecond {
				t.Fatalf("flow %d Latency.Min = %v, want at least 8ms hold duration", i+1, stats.Latency.Min)
			}
		} else if stats.Drops[wantReason] != 1 || len(stats.Delivered) != 0 || stats.Latency.Count != 0 {
			t.Fatalf("flow %d = %+v, want exactly one %s drop and no delivery/sample", i+1, stats, wantReason)
		}
		var found bool
		for _, j := range fab.Report() {
			if j.FrameID != fid {
				continue
			}
			found = true
			if retention == fabric.RetainAggregate {
				t.Fatalf("aggregate holder %d survived", fid)
			}
			hdr, _, err := ip.Decode(j.Injection.Frame.Payload)
			if err != nil || hdr.Dst != netip.MustParseAddr(addresses[i]) {
				t.Fatalf("holder %d destination = %s/%v, want %s", fid, hdr.Dst, err, addresses[i])
			}
			if wantReason == "" {
				if j.State != fabric.JourneyDelivered || len(j.Deliveries) != 1 || j.Deliveries[0].Host != "h2" {
					t.Fatalf("holder %d = %s/%+v, want Delivered/h2", fid, j.State, j.Deliveries)
				}
			} else {
				var drops int
				for _, e := range j.Entries {
					if e.Kind == fabric.EntryDrop && e.Reason == wantReason {
						drops++
					}
				}
				if j.State != fabric.JourneyDropped || drops != 1 {
					t.Fatalf("holder %d state/drops = %s/%d, want Dropped/1", fid, j.State, drops)
				}
			}
		}
		if retention == fabric.RetainJourney && !found {
			t.Fatalf("holder %d disappeared", fid)
		}
	}
}

// observeJourneys samples live reports before settlement and returns raw step results.
func observeJourneys(t *testing.T, fab *fabric.Fabric, budget int) ([]fabric.Journey, []fabric.Entry) {
	t.Helper()
	seen := make(map[fabric.FrameID]fabric.Journey)
	var entries []fabric.Entry
	for i := 0; i <= budget; i++ {
		for _, j := range fab.Report() {
			seen[j.FrameID] = j
		}
		if i == budget {
			break
		}
		e, ok := fab.Step()
		if !ok {
			break
		}
		entries = append(entries, e.Entry)
	}
	if err := fab.Err(); err != nil {
		t.Fatal(err)
	}
	var journeys []fabric.Journey
	for _, fid := range slices.Sorted(maps.Keys(seen)) {
		journeys = append(journeys, seen[fid])
	}
	return journeys, entries
}

func TestReleaseWithMirrorUnderAggregate(t *testing.T) {
	base, swMAC, srcMAC := heldPropertyFixture(t)
	cfg := assembly.Spec(base).Config()
	sw := cfg.Switches["sw1"]
	builder := port.NewBuilder()
	for _, p := range sw.Ports.Ports() {
		builder.Add(p)
	}
	builder.Add(port.Port{Name: "mirror", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	var err error
	sw.Ports, err = builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	sw.Traffic = &traffic.Config{Mirrors: []traffic.Mirror{{Name: "span", SelectDstPorts: []string{"1/1/2"}, OutputPort: "mirror"}}}
	cfg.Switches["sw1"] = sw
	cfg.Hosts["analyzer"] = hostmodel.Config{Address: netaddr.MAC{2, 0, 0, 0, 0, 9}, Accept: hostmodel.Accept{Promiscuous: true}}
	cfg.Cables = append(cfg.Cables, fabric.Cable{A: fabric.Endpoint{Node: "sw1", Port: "mirror"}, B: fabric.Endpoint{Node: "analyzer"}})
	fab, err := assembly.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fab.Inject(fabric.Injection{
		At: cfg.Start, Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
		Frame:     heldPropertyFrame(t, srcMAC, swMAC, netip.MustParseAddr("10.0.10.7"), netip.MustParseAddr("10.0.20.7"), []byte("mirror")),
		Retention: fabric.RetainAggregate, Flow: 21,
	}); err != nil {
		t.Fatal(err)
	}
	releaseHeldAddress(t, fab, swMAC, "10.0.20.7")
	stats := fab.Flows()[21]
	if stats.Delivered["h2"] != 1 || stats.Copies["span"] != 1 || stats.Latency.Count != 1 || stats.Held != 0 || len(*fabric.ExportFabricHeld(fab)) != 0 {
		t.Fatalf("released mirror flow = %+v, held=%v, want Delivered=1, Copies=1, Latency.Count=1 and no hold", stats, *fabric.ExportFabricHeld(fab))
	}
}

func TestProtocolJourneyFreedAtSettle(t *testing.T) {
	fab, _, _ := injectHeldPair(t, fabric.RetainJourney)
	var protocols []fabric.FrameID
	for _, j := range fab.Report() {
		if j.Protocol {
			protocols = append(protocols, j.FrameID)
		}
	}
	if len(protocols) != 2 {
		t.Fatalf("live protocol journeys = %d, want 2 solicitations", len(protocols))
	}
	fab.Run(fabric.RunOptions{Budget: 100, Window: 0})
	for _, fid := range protocols {
		if (*fabric.ExportFabricJourneys(fab))[fid] != nil || (*fabric.ExportFabricEntered(fab))[fid] != nil {
			t.Fatalf("settled protocol %d retained journey or entered set", fid)
		}
	}
	for _, j := range fab.Report() {
		if j.Protocol {
			t.Fatalf("protocol journey %d survived settle", j.FrameID)
		}
	}
}
