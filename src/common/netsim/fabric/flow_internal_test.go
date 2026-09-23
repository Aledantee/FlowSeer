package fabric

import (
	"net/netip"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
)

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
	if _, err := fab.Inject(Injection{
		At:        t0.Add(time.Millisecond),
		Origin:    Endpoint{Node: "h1"},
		Frame:     broadcast,
		Retention: RetainAggregate,
		Flow:      3,
	}); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if res := fab.Run(100); res.Stop != StopQueueDrained {
		t.Fatalf("Run stop = %s, want %s", res.Stop, StopQueueDrained)
	}

	if len(fab.inflight) != 0 {
		t.Errorf("inflight = %+v, want empty after the run drained", fab.inflight)
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
		if _, err := fab.Inject(Injection{
			At:        t0.Add(time.Millisecond),
			Origin:    Endpoint{Node: "h1"},
			Frame:     frame,
			Retention: RetainAggregate,
			Flow:      7,
		}); err != nil {
			t.Fatalf("Inject: %v", err)
		}
	}
	if res := fab.Run(1_000_000); res.Stop != StopQueueDrained {
		t.Fatalf("Run stop = %s, want %s", res.Stop, StopQueueDrained)
	}

	if len(fab.journeys) != 0 {
		t.Errorf("journeys = %d, want only protocol journeys (none here)", len(fab.journeys))
	}
	if len(fab.entered) != 0 {
		t.Errorf("entered = %d, want only protocol frames (none here)", len(fab.entered))
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
	fid, err := fab.Inject(Injection{
		At:     t0.Add(time.Millisecond),
		Origin: Endpoint{Node: "h1"},
		Frame:  broadcast,
		Flow:   3,
	})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if res := fab.Run(100); res.Stop != StopQueueDrained {
		t.Fatalf("Run stop = %s, want %s", res.Stop, StopQueueDrained)
	}

	if got := fab.Flows()[3].Offered; got != 1 {
		t.Fatalf("Offered = %d, want 1", got)
	}
	fab.settle(fid)
	fab.settle(fid)
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
		fab  *Fabric
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

func driveAndCheckInflight(t *testing.T, fab *Fabric, t0 time.Time) {
	t.Helper()
	sawInFlight := 0
	for _, dst := range []netaddr.MAC{
		{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		{0x00, 0x11, 0x22, 0x33, 0x44, 0x02},
	} {
		if _, err := fab.Inject(Injection{
			At:     t0.Add(time.Millisecond),
			Origin: Endpoint{Node: "h1"},
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

func checkInflightMatchesContainers(t *testing.T, fab *Fabric) int {
	t.Helper()
	want := make(map[FrameID]int)
	for _, arr := range fab.queue {
		if arr.Kind == ArrivalFrame {
			want[arr.FrameID]++
		}
	}
	for _, q := range fab.egress {
		for pcp := range 8 {
			for _, item := range q.pending[pcp] {
				want[item.fid]++
			}
		}
	}

	for fid, n := range want {
		if got := fab.inflight[fid]; got != n {
			t.Fatalf("inflight[%d] = %d, want %d held in the queue and egress queues", fid, got, n)
		}
	}
	for fid, got := range fab.inflight {
		if want[fid] != got {
			t.Fatalf("inflight[%d] = %d with %d in the containers", fid, got, want[fid])
		}
	}

	return len(want)
}

// invariantFabric builds a two-switch chain with the named traffic: a cut
// trunk for the cable-loss case, a second parallel trunk for a loop, a mirror
// to an analyzer, or a reflector hung off a spare port.
func invariantFabric(t *testing.T, variant string, t0 time.Time) *Fabric {
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
	cables := []Cable{
		{A: Endpoint{Node: "h1"}, B: Endpoint{Node: "sw1", Port: "1/1/1"}, LengthMeters: 5},
		{A: Endpoint{Node: "sw1", Port: "1/1/24"}, B: Endpoint{Node: "sw2", Port: "1/1/24"}, LengthMeters: 5, Medium: MultimodeFiber},
		{A: Endpoint{Node: "sw2", Port: "1/1/1"}, B: Endpoint{Node: "h2"}, LengthMeters: 5},
	}

	hosts := map[string]Host{
		"h1": {Address: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}},
		"h2": {Address: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}},
	}

	switch variant {
	case "cable-loss":
		cables[1].Fault = Fault{Kind: FaultLoseEveryNth, N: 1}
	case "loop":
		cables = append(cables, Cable{
			A: Endpoint{Node: "sw1", Port: "1/1/2"}, B: Endpoint{Node: "sw2", Port: "1/1/2"}, LengthMeters: 5, Medium: MultimodeFiber,
		})
	case "mirror":
		hosts["an"] = Host{Address: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x09}, Accept: HostAccept{Promiscuous: true}}
		cables = append(cables, Cable{A: Endpoint{Node: "sw1", Port: "1/1/3"}, B: Endpoint{Node: "an"}, LengthMeters: 5})
		sw1.Traffic = &traffic.Config{Mirrors: []traffic.Mirror{{Name: "analyzer", SelectAll: true, OutputPort: "1/1/3"}}}
	case "mirror-reject":
		hosts["an"] = Host{Address: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x09}}
		cables = append(cables, Cable{A: Endpoint{Node: "sw1", Port: "1/1/3"}, B: Endpoint{Node: "an"}, LengthMeters: 5})
		sw1.Traffic = &traffic.Config{Mirrors: []traffic.Mirror{{Name: "analyzer", SelectAll: true, OutputPort: "1/1/3"}}}
	case "reflector":
		reflec := Reflector{
			Address: netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x01, 0xaa},
			Ports:   map[string]phy.Ethernet{"rp1": {}},
			Attachments: map[string]Attachment{
				"a": {Port: "rp1", VLAN: &vid10, Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.9/24")}},
			},
		}
		cables = append(cables, Cable{A: Endpoint{Node: "sw1", Port: "1/1/3"}, B: Endpoint{Node: "reflector", Port: "rp1"}, LengthMeters: 5})
		cfg := Config{
			Start:         t0,
			PhyAssumption: testPhyAssumption(),
			Switches:      map[string]vswitch.Config{"sw1": sw1, "sw2": sw2},
			Hosts:         hosts,
			Reflectors:    map[string]Reflector{"reflector": reflec},
			Cables:        cables,
		}
		return newInvariantFabric(t, cfg)
	}

	cfg := Config{
		Start:         t0,
		PhyAssumption: testPhyAssumption(),
		Switches:      map[string]vswitch.Config{"sw1": sw1, "sw2": sw2},
		Hosts:         hosts,
		Cables:        cables,
	}
	return newInvariantFabric(t, cfg)
}

func newInvariantFabric(t *testing.T, cfg Config) *Fabric {
	t.Helper()
	fab, err := New(cfg)
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
	if _, err := fab.Inject(Injection{
		At:        t0.Add(time.Millisecond),
		Origin:    Endpoint{Node: "h1"},
		Frame:     frame,
		Retention: RetainAggregate,
		Flow:      5,
	}); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if res := fab.Run(100); res.Stop != StopQueueDrained {
		t.Fatalf("Run stop = %s, want %s", res.Stop, StopQueueDrained)
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
		if _, err := fab.Inject(Injection{
			At:        t0.Add(time.Millisecond),
			Origin:    Endpoint{Node: "h1"},
			Frame:     frame,
			Retention: RetainAggregate,
			Flow:      5,
		}); err != nil {
			t.Fatalf("Inject: %v", err)
		}
		if res := fab.Run(100); res.Stop != StopQueueDrained {
			t.Fatalf("Run stop = %s, want %s", res.Stop, StopQueueDrained)
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
		if _, err := fab.Inject(Injection{
			At:        t0.Add(time.Millisecond),
			Origin:    Endpoint{Node: "h1"},
			Frame:     frame,
			Retention: RetainAggregate,
			Flow:      6,
		}); err != nil {
			t.Fatalf("Inject: %v", err)
		}
		if res := fab.Run(100); res.Stop != StopQueueDrained {
			t.Fatalf("Run stop = %s, want %s", res.Stop, StopQueueDrained)
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

	cfg := Config{
		Start:         t0,
		PhyAssumption: testPhyAssumption(),
		Switches: map[string]vswitch.Config{"sw1": {
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
		Hosts: map[string]Host{
			"h1": {
				Address: macH1,
				IP: &HostIP{
					Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.7/24")},
					Gateway:   netip.MustParseAddr("10.0.10.1"),
					Neighbors: map[netip.Addr]netaddr.MAC{netip.MustParseAddr("10.0.10.1"): swMAC},
				},
			},
			"h2": {Address: macH2},
		},
		Cables: []Cable{
			{A: Endpoint{Node: "h1"}, B: Endpoint{Node: "sw1", Port: "1/1/1"}, LengthMeters: 5},
			{A: Endpoint{Node: "sw1", Port: "1/1/2"}, B: Endpoint{Node: "h2"}, LengthMeters: 5},
		},
	}
	fab := newInvariantFabric(t, cfg)

	if _, err := fab.Inject(Injection{
		At:        t0,
		Origin:    Endpoint{Node: "h1"},
		Packet:    &Packet{To: addrH2, Protocol: 17, Payload: []byte("ping")},
		Retention: RetainAggregate,
		Flow:      9,
	}); err != nil {
		t.Fatalf("Inject packet: %v", err)
	}
	fab.Run(20)

	stats := fab.Flows()[9]
	if stats.Held != 1 {
		t.Errorf("Held = %d, want 1", stats.Held)
	}
}
