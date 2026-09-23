package fabric_test

import (
	"net/netip"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/arp"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
)

func flowFrame(src, dst [6]byte) ethernet.Frame {
	return ethernet.Frame{
		Src:     src,
		Dst:     dst,
		Payload: make([]byte, 46),
	}
}

// TestAggregateFlowFoldsOfferedDeliveriesAndLatency covers the fold of a
// unicast aggregate stream: every injected frame is offered once, delivered
// once to the destination, and its per-delivery latency folded, with the
// minimum equal to the path's single-frame latency.
func TestAggregateFlowFoldsOfferedDeliveriesAndLatency(t *testing.T) {
	base := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	const frames = 1000

	single, src, dst := newTwoSwitchTopology(t, fabric.Fault{})
	frame := flowFrame(src, dst)
	if _, err := single.Inject(fabric.Injection{At: base, Origin: fabric.Endpoint{Node: "h1"}, Frame: frame}); err != nil {
		t.Fatalf("single Inject: %v", err)
	}
	single.Run(1000)
	delivery := single.Report()[0].Deliveries
	if len(delivery) != 1 {
		t.Fatalf("single-frame deliveries = %+v, want one", delivery)
	}
	singleLatency := delivery[0].At.Sub(base)

	fab, src, dst := newTwoSwitchTopology(t, fabric.Fault{})
	frame = flowFrame(src, dst)
	for range frames {
		if _, err := fab.Inject(fabric.Injection{
			At:        base,
			Origin:    fabric.Endpoint{Node: "h1"},
			Frame:     frame,
			Retention: fabric.RetainAggregate,
			Flow:      7,
		}); err != nil {
			t.Fatalf("Inject: %v", err)
		}
	}
	if res := fab.Run(1_000_000); res.Stop != fabric.StopQueueDrained {
		t.Fatalf("Run stop = %s, want %s", res.Stop, fabric.StopQueueDrained)
	}

	stats := fab.Flows()[7]
	if stats.Offered != frames {
		t.Errorf("Offered = %d, want %d", stats.Offered, frames)
	}
	if stats.Delivered["h2"] != frames {
		t.Errorf("Delivered[h2] = %d, want %d", stats.Delivered["h2"], frames)
	}
	if len(stats.Drops) != 0 {
		t.Errorf("Drops = %+v, want none", stats.Drops)
	}
	if stats.Latency.Count != frames {
		t.Errorf("latency count = %d, want %d", stats.Latency.Count, frames)
	}
	if stats.Latency.Min != singleLatency {
		t.Errorf("latency minimum = %s, want the single-frame latency %s", stats.Latency.Min, singleLatency)
	}
	if reported := fab.Report(); len(reported) != 0 {
		t.Errorf("Report held %d journeys for an aggregate flow, want none", len(reported))
	}
}

// TestAggregateWithoutADestinationFoldsItsDrop covers an injection whose host
// link is Down: with no step run, the frame folds its offered count and its
// drop under the link's reason, and leaves no journey behind.
func TestAggregateWithoutADestinationFoldsItsDrop(t *testing.T) {
	base := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	fab, src, dst := newTwoSwitchTopology(t, fabric.Fault{})
	if err := fab.SetFault(fabric.Endpoint{Node: "h1"}, fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, fabric.Fault{Kind: fabric.FaultCut}); err != nil {
		t.Fatalf("SetFault: %v", err)
	}

	const frames = 10
	frame := flowFrame(src, dst)
	for range frames {
		if _, err := fab.Inject(fabric.Injection{
			At:        base,
			Origin:    fabric.Endpoint{Node: "h1"},
			Frame:     frame,
			Retention: fabric.RetainAggregate,
			Flow:      4,
		}); err != nil {
			t.Fatalf("Inject: %v", err)
		}
	}

	if reported := fab.Report(); len(reported) != 0 {
		t.Errorf("Report held %d journeys before any step, want none", len(reported))
	}
	stats := fab.Flows()[4]
	if stats.Offered != frames {
		t.Errorf("Offered = %d, want %d", stats.Offered, frames)
	}
	if got := stats.Drops[fabric.ReasonCut]; got != frames {
		t.Errorf("Drops[%s] = %d, want %d", fabric.ReasonCut, got, frames)
	}
}

// TestPolicedFlowDropsAndDeliveriesMatchAdmissions covers an ingress policer
// that admits five of ten 84-wire-octet frames and refills nothing
// measurable, and that across a thousand frames drops plus deliveries equal
// what the flow offered.
func TestPolicedFlowDropsAndDeliveriesMatchAdmissions(t *testing.T) {
	base := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	policer := &traffic.Config{Policers: map[string]traffic.Policer{
		"1/1/1": {RateBPS: 1, BurstOctets: 420},
	}}

	fab, macs := newTrafficTopology(t, policer)
	frame := flowFrame(macs["h1"], macs["h2"])
	for range 10 {
		if _, err := fab.Inject(fabric.Injection{
			At:        base,
			Origin:    fabric.Endpoint{Node: "h1"},
			Frame:     frame,
			Retention: fabric.RetainAggregate,
			Flow:      7,
		}); err != nil {
			t.Fatalf("Inject: %v", err)
		}
	}
	if res := fab.Run(1_000_000); res.Stop != fabric.StopQueueDrained {
		t.Fatalf("Run stop = %s, want %s", res.Stop, fabric.StopQueueDrained)
	}

	stats := fab.Flows()[7]
	if got := stats.Drops[traffic.ReasonPoliced]; got != 5 {
		t.Errorf("Drops[%s] = %d, want 5", traffic.ReasonPoliced, got)
	}
	if got := stats.Delivered["h2"]; got != 5 {
		t.Errorf("Delivered[h2] = %d, want 5", got)
	}

	const many = 1000
	bulk, macs := newTrafficTopology(t, policer)
	frame = flowFrame(macs["h1"], macs["h2"])
	for range many {
		if _, err := bulk.Inject(fabric.Injection{
			At:        base,
			Origin:    fabric.Endpoint{Node: "h1"},
			Frame:     frame,
			Retention: fabric.RetainAggregate,
			Flow:      8,
		}); err != nil {
			t.Fatalf("bulk Inject: %v", err)
		}
	}
	if res := bulk.Run(1_000_000); res.Stop != fabric.StopQueueDrained {
		t.Fatalf("bulk Run stop = %s, want %s", res.Stop, fabric.StopQueueDrained)
	}

	bulkStats := bulk.Flows()[8]
	var dropped uint64
	for _, n := range bulkStats.Drops {
		dropped += n
	}
	if got := dropped + bulkStats.Delivered["h2"]; got != bulkStats.Offered {
		t.Errorf("drops+deliveries = %d, want Offered %d", got, bulkStats.Offered)
	}
}

// TestFlowMetadataCarriesTheJourneyTrust covers folding a journey's trust
// metadata into its flow: a unicast that crosses a link with no medium and a
// length carries propagation-unknown.
func TestFlowMetadataCarriesTheJourneyTrust(t *testing.T) {
	fab := journeyFabric(t, true)
	if _, err := fab.Inject(fabric.Injection{
		At:        fab.Snapshot().Clock,
		Origin:    fabric.Endpoint{Node: "h1"},
		Frame:     ethernet.Frame{Dst: journeyH2, Src: journeyH1, EtherType: ethernet.EtherTypeIPv4},
		Retention: fabric.RetainAggregate,
		Flow:      7,
	}); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	fab.Run(10)

	stats := fab.Flows()[7]
	if !hasIssue(stats.Metadata.Issues(), fabric.IssuePropagationUnknown) {
		t.Errorf("flow metadata issues = %+v, want propagation-unknown", stats.Metadata.Issues())
	}
}

// TestAggregateHeldFrameReleaseKeepsItsProvenance covers the journey of an
// aggregate frame a switch holds for neighbor resolution: the hold settles the
// frame's own journey and folds Held once, but the journey is not freed until
// the switch releases it, so the released frame links back through
// OriginRelease instead of appearing as a fresh injection.
func TestAggregateHeldFrameReleaseKeepsItsProvenance(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := b.Build()
	if err != nil {
		t.Fatalf("build switch ports: %v", err)
	}

	vid10 := vlan.ID(10)
	vid20 := vlan.ID(20)
	swMAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x77}
	addrH2 := netip.MustParseAddr("10.0.20.7")

	cfg := fabric.Config{
		Start: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		Switches: map[string]vswitch.Config{"sw1": {
			MAC:   swMAC,
			Ports: ports,
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
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
							"vlan10": {VLAN: 10, MAC: swMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
							"vlan20": {VLAN: 20, MAC: swMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
						},
					},
				},
			},
		}},
		Hosts: map[string]fabric.Host{
			"h1": {
				Address: macH1,
				IP: &fabric.HostIP{
					Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.7/24")},
					Gateway:   netip.MustParseAddr("10.0.10.1"),
					Neighbors: map[netip.Addr]netaddr.MAC{netip.MustParseAddr("10.0.10.1"): swMAC},
				},
			},
			"h2": {Address: macH2},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, LengthMeters: 5},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "h2"}, LengthMeters: 5},
		},
	}
	fab, err := fabric.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}

	heldID, err := fab.Inject(fabric.Injection{
		At:        cfg.Start,
		Origin:    fabric.Endpoint{Node: "h1"},
		Packet:    &fabric.Packet{To: addrH2, Protocol: 17, Payload: []byte("ping")},
		Retention: fabric.RetainAggregate,
		Flow:      9,
	})
	if err != nil {
		t.Fatalf("Inject packet: %v", err)
	}

	replyFrame, err := arp.Encode(arp.Message{
		HardwareType: 1,
		ProtocolType: 0x0800,
		Operation:    arp.Reply,
		SenderMAC:    macH2,
		SenderAddr:   addrH2,
		TargetMAC:    swMAC,
		TargetAddr:   netip.MustParseAddr("10.0.20.1"),
	}, swMAC)
	if err != nil {
		t.Fatalf("encode ARP reply: %v", err)
	}
	replyID, err := fab.Inject(fabric.Injection{
		At:     cfg.Start.Add(time.Second),
		Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
		Frame:  replyFrame,
	})
	if err != nil {
		t.Fatalf("Inject ARP reply: %v", err)
	}

	fab.Run(20)

	if got := fab.Flows()[9].Held; got != 1 {
		t.Fatalf("Held = %d, want the held frame counted once", got)
	}

	var released *fabric.Journey
	for _, j := range fab.Report() {
		if j.FrameID == heldID || j.FrameID == replyID {
			continue
		}
		if len(j.Deliveries) == 1 && j.Deliveries[0].Host == "h2" {
			jj := j
			released = &jj
			break
		}
	}
	if released == nil {
		t.Fatalf("no released journey delivered to h2 among: %+v", fab.Report())
	}
	if released.Origin.Kind != fabric.OriginRelease {
		t.Errorf("released origin kind = %s, want %s", released.Origin.Kind, fabric.OriginRelease)
	}
	if released.Origin.Of != heldID {
		t.Errorf("released origin parent = %d, want the held frame %d", released.Origin.Of, heldID)
	}
}

// TestInjectRefusesUnknownRetention covers the validation: a retention value
// above RetainAggregate is refused rather than silently treated as
// RetainJourney.
func TestInjectRefusesUnknownRetention(t *testing.T) {
	fab, src, dst := newTwoSwitchTopology(t, fabric.Fault{})
	_, err := fab.Inject(fabric.Injection{
		At:        time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC),
		Origin:    fabric.Endpoint{Node: "h1"},
		Frame:     flowFrame(src, dst),
		Retention: fabric.Retention(2),
		Flow:      1,
	})
	if err == nil {
		t.Fatal("Inject with an unknown retention returned no error")
	}
}

// TestAggregateRequiresAFlow covers the refusal: RetainAggregate with a zero
// flow is an error from Inject.
func TestAggregateRequiresAFlow(t *testing.T) {
	fab, src, dst := newTwoSwitchTopology(t, fabric.Fault{})
	_, err := fab.Inject(fabric.Injection{
		At:        time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC),
		Origin:    fabric.Endpoint{Node: "h1"},
		Frame:     flowFrame(src, dst),
		Retention: fabric.RetainAggregate,
		Flow:      0,
	})
	if err == nil {
		t.Fatal("Inject with RetainAggregate and a zero flow returned no error")
	}
}
