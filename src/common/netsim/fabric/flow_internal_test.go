package fabric

import (
	"net/netip"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/arp"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
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
				NeighborPolicy: routing.NeighborPolicy{ResolutionTimeout: time.Hour},
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
func heldPropertyFixture(t *testing.T) (*Fabric, netaddr.MAC, netaddr.MAC) {
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

	cfg := Config{
		Start:         time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC),
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
			"h1": {Address: macH1, IP: &HostIP{
				Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.7/24")},
				Gateway:   netip.MustParseAddr("10.0.10.1"),
				Neighbors: map[netip.Addr]netaddr.MAC{netip.MustParseAddr("10.0.10.1"): swMAC},
			}},
			"h2": {Address: macH2},
		},
		Cables: []Cable{
			{A: Endpoint{Node: "h1"}, B: Endpoint{Node: "sw1", Port: "1/1/1"}, LengthMeters: 5},
			{A: Endpoint{Node: "sw1", Port: "1/1/2"}, B: Endpoint{Node: "h2"}, LengthMeters: 5},
		},
	}

	fab, err := New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	return fab, swMAC, macH1
}

// heldPropertyRun is what one retention of the property fixture produced: the
// fabric, the mapping from each release journey's FrameID to the FrameID it
// names as its holder, and the FrameIDs of the held flow frames this run
// injected.
type heldPropertyRun struct {
	fab       *Fabric
	releaseOf map[FrameID]FrameID
	heldIDs   []FrameID
}

// runHeldProperty drives the fixture with the flow frames either retained or
// aggregated. The fixture holds three frames for three unresolved addresses on
// one device at once: two flow frames and one frame with no flow. Each address
// is resolved in turn, while the later ones are still held, so every release
// happens beside a held frame another retention kept. Four more frames for a
// fourth address exceed the default neighbor HoldDepth of three, so the oldest
// is evicted; that address is never resolved, and the default three-second
// resolution timeout expires the rest. The replies land before that timeout.
func runHeldProperty(t *testing.T, aggregated bool) heldPropertyRun {
	t.Helper()

	fab, swMAC, macH1 := heldPropertyFixture(t)
	t0 := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	saddr := netip.MustParseAddr("10.0.10.7")

	flowRetention := RetainJourney
	if aggregated {
		flowRetention = RetainAggregate
	}

	injectFrame := func(at time.Time, daddr netip.Addr, payload []byte, flow FlowID, retention Retention) FrameID {
		fid, err := fab.Inject(Injection{
			At:        at,
			Origin:    Endpoint{Node: "sw1", Port: "1/1/1"},
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
		if _, err := fab.Inject(Injection{
			At:     at,
			Origin: Endpoint{Node: "sw1", Port: "1/1/2"},
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

	heldIDs := []FrameID{
		injectFrame(t0.Add(1*time.Millisecond), addrA, []byte("holder-a"), 11, flowRetention),
		injectFrame(t0.Add(2*time.Millisecond), addrB, []byte("holder-b"), 0, RetainJourney),
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

	fab.Run(100)

	releaseOf := make(map[FrameID]FrameID)
	for _, j := range fab.Report() {
		if j.Origin.Kind == OriginRelease {
			releaseOf[j.FrameID] = j.Origin.Of
		}
	}

	return heldPropertyRun{fab: fab, releaseOf: releaseOf, heldIDs: heldIDs}
}

// TestAggregateRetentionPreservesReleaseAttribution is the property this
// change must hold: for one fixture, retention does not change which frame a
// release names. The
// same fixture runs twice, once with the flow frames retained and once with
// them aggregated; the two release->holder mappings must match, and the
// aggregate run must leave no settled aggregate journey or re-entry set behind
// and no pending journey for the aggregated frames, evicted or timed out holds
// included. In the retained run the held frames that resolution abandoned stay
// pending, as they do without aggregation.
func TestAggregateRetentionPreservesReleaseAttribution(t *testing.T) {
	retained := runHeldProperty(t, false)
	aggregated := runHeldProperty(t, true)

	if len(retained.releaseOf) == 0 {
		t.Fatal("the retained run produced no release journey; the property held vacuously")
	}
	if len(retained.releaseOf) != len(aggregated.releaseOf) {
		t.Fatalf("release counts differ: retained %d, aggregated %d", len(retained.releaseOf), len(aggregated.releaseOf))
	}
	for fid, of := range retained.releaseOf {
		if got, ok := aggregated.releaseOf[fid]; !ok || got != of {
			t.Errorf("release %d names %d when retained, got %d (present %v) when aggregated", fid, of, got, ok)
		}
	}

	if got := len(aggregated.fab.journeys); got == 0 {
		t.Errorf("aggregated run kept no protocol or non-flow journey; the fixture lost its non-aggregate frames")
	}
	for fid, j := range aggregated.fab.journeys {
		if j.Injection.Retention == RetainAggregate {
			t.Errorf("journey %d survived with RetainAggregate retention", fid)
		}
	}
	for fid := range aggregated.fab.entered {
		if j := aggregated.fab.journeys[fid]; j == nil {
			t.Errorf("entered holds %d with no journey", fid)
		}
	}
	if aggregated.fab.hasPendingJourneys() {
		t.Errorf("aggregated run still has a pending journey: %+v", aggregated.fab.Report())
	}

	releasedHolders := make(map[FrameID]bool, len(aggregated.releaseOf))
	for _, of := range aggregated.releaseOf {
		releasedHolders[of] = true
	}
	var want []FrameID
	for _, fid := range aggregated.heldIDs {
		if !releasedHolders[fid] {
			want = append(want, fid)
		}
	}
	if got := aggregated.fab.heldAggregates["sw1"]; !slices.Equal(got, want) {
		t.Errorf("heldAggregates[sw1] = %v, want only the abandoned holds %v: a release claims the holder's placeholder", got, want)
	}
	if got := retained.fab.heldAggregates["sw1"]; len(got) != 0 {
		t.Errorf("retained run kept placeholders %v, want none: no journey is freed there", got)
	}

	if !retained.fab.hasPendingJourneys() {
		t.Errorf("retained run has no pending journey, want the held frames resolution abandoned to stay pending")
	}
	for _, j := range retained.fab.Report() {
		if j.State == JourneyPending && !isJourneyHeld(&j) {
			t.Errorf("retained run has a pending journey %d that no hold explains", j.FrameID)
		}
	}
}
