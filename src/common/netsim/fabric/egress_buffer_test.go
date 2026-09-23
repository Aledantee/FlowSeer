package fabric_test

import (
	"fmt"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
)

// egressForcedEthernet states one forced full-duplex link speed with no
// autonegotiation.
func egressForcedEthernet(speedBPS uint64) phy.Ethernet {
	return phy.Ethernet{
		SupportedSpeedsBPS: []uint64{speedBPS},
		Setting:            &phy.Setting{SpeedBPS: speedBPS, Duplex: phy.Full},
	}
}

// egressBufferTopology builds one untagged switch joining h1, h2, and h3,
// with an optional buffer in encoded frame octets stated on the port toward
// h3. A nil buffer leaves that queue unstated.
func egressBufferTopology(t *testing.T, buffer *uint64) (*fabric.Fabric, map[string]netaddr.MAC) {
	t.Helper()

	builder := port.NewBuilder()
	for _, name := range []string{"1/1/1", "1/1/2", "1/1/3"} {
		builder.Add(port.Port{Name: name, Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	}
	ports, err := builder.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	vid10 := vlan.ID(10)
	var queues map[string]traffic.PortQueues
	if buffer != nil {
		queues = map[string]traffic.PortQueues{
			"1/1/3": {BufferOctets: map[vlan.PCP]uint64{0: *buffer}},
		}
	}

	macs := map[string]netaddr.MAC{
		"h1": {0x02, 0, 0, 0, 0, 0x01},
		"h2": {0x02, 0, 0, 0, 0, 0x02},
		"h3": {0x02, 0, 0, 0, 0, 0x03},
	}

	fab, err := fabric.New(statedPhysical(fabric.Config{
		Start: time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC),
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports: ports,
				Bridge: &bridge.Config{VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "VLAN10"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
						"1/1/2": {PVID: &vid10, Untagged: []vlan.ID{10}},
						"1/1/3": {PVID: &vid10, Untagged: []vlan.ID{10}},
					},
				}},
				// The two sources run at 100 Mbit/s against the gigabit egress
				// toward h3, so the offered load fits the egress line and every
				// frame the buffer admits still drains before the next pair.
				Phy: &phy.Config{Ethernet: map[string]phy.Ethernet{
					"1/1/1": egressForcedEthernet(100_000_000),
					"1/1/2": egressForcedEthernet(100_000_000),
					"1/1/3": egressForcedEthernet(1_000_000_000),
				}},
				Traffic: &traffic.Config{Queues: queues},
			},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: macs["h1"], Ethernet: egressForcedEthernet(100_000_000)},
			"h2": {Address: macs["h2"], Ethernet: egressForcedEthernet(100_000_000)},
			"h3": {Address: macs["h3"], Ethernet: egressForcedEthernet(1_000_000_000)},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{A: fabric.Endpoint{Node: "h2"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/3"}, B: fabric.Endpoint{Node: "h3"}},
		},
	}))
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}

	return fab, macs
}

// primeEgressLearning sends one frame from h3 to h1 so the switch learns h3's
// address on the port toward h3, making the later h1 and h2 frames known
// unicast rather than a flood.
func primeEgressLearning(t *testing.T, fab *fabric.Fabric, macs map[string]netaddr.MAC) {
	t.Helper()
	if _, err := fab.Inject(fabric.Injection{
		At:     fab.Snapshot().Clock,
		Origin: fabric.Endpoint{Node: "h3"},
		Frame:  ethernet.Frame{Src: macs["h3"], Dst: macs["h1"]},
	}); err != nil {
		t.Fatalf("prime Inject: %v", err)
	}
	fab.Run(1000)
}

func egressBufferFrame(src, dst netaddr.MAC) ethernet.Frame {
	return ethernet.Frame{Src: src, Dst: dst, Payload: make([]byte, 1000)}
}

func injectEgressFlow(t *testing.T, fab *fabric.Fabric, macs map[string]netaddr.MAC, host string, flow fabric.FlowID, retention fabric.Retention) {
	t.Helper()
	at := fab.Snapshot().Clock
	for range 20 {
		if _, err := fab.Inject(fabric.Injection{
			At:        at,
			Origin:    fabric.Endpoint{Node: host},
			Frame:     egressBufferFrame(macs[host], macs["h3"]),
			Retention: retention,
			Flow:      flow,
		}); err != nil {
			t.Fatalf("Inject %s: %v", host, err)
		}
	}
}

// TestEgressStatedBufferTailDrop asserts a 2000-octet stated buffer drops the
// frames that would overflow it and counts only those as discards: OutDiscards
// and Discards["queue-full"] move together, the transmitted frame counters
// match what h3 received, the drops fold into their flows, and no peak exceeds
// the buffer.
func TestEgressStatedBufferTailDrop(t *testing.T) {
	buffer := uint64(2000)
	fab, macs := egressBufferTopology(t, &buffer)
	primeEgressLearning(t, fab, macs)
	injectEgressFlow(t, fab, macs, "h1", 1, fabric.RetainAggregate)
	injectEgressFlow(t, fab, macs, "h2", 2, fabric.RetainAggregate)
	if res := fab.Run(1_000_000); res.Stop != fabric.StopQueueDrained {
		t.Fatalf("Run stop = %s, want %s", res.Stop, fabric.StopQueueDrained)
	}

	counters := fab.Snapshot().Devices["sw1"].Counters["1/1/3"]
	if counters.OutDiscards == 0 {
		t.Fatalf("OutDiscards = 0, want a nonzero queue-full count")
	}
	if counters.OutDiscards != counters.Discards[traffic.ReasonQueueFull] {
		t.Errorf("OutDiscards = %d, Discards[queue-full] = %d; want equal", counters.OutDiscards, counters.Discards[traffic.ReasonQueueFull])
	}

	var (
		flowDrops uint64
		delivered uint64
	)
	for _, flow := range []fabric.FlowID{1, 2} {
		stats := fab.Flows()[flow]
		drops := stats.Drops[traffic.ReasonQueueFull]
		flowDrops += drops
		delivered += stats.Delivered["h3"]
		if got := stats.Delivered["h3"] + drops; got != stats.Offered {
			t.Errorf("flow %d Delivered[h3]+Drops[queue-full] = %d, want Offered %d", flow, got, stats.Offered)
		}
	}
	if flowDrops != counters.Discards[traffic.ReasonQueueFull] {
		t.Errorf("flow drops = %d, want the port's queue-full count %d", flowDrops, counters.Discards[traffic.ReasonQueueFull])
	}

	// A tail-dropped frame never left the port, so only the delivered frames
	// count as transmitted, in frames and in the encoded octets they carry.
	if counters.OutUnicast != delivered {
		t.Errorf("OutUnicast = %d, want the delivered frame count %d", counters.OutUnicast, delivered)
	}
	if counters.OutOctets != delivered*1014 {
		t.Errorf("OutOctets = %d, want %d encoded octets for %d delivered frames", counters.OutOctets, delivered*1014, delivered)
	}

	peak := fab.Snapshot().EgressDepths[fabric.Endpoint{Node: "sw1", Port: "1/1/3"}][0].Peak
	if peak > 2000 {
		t.Errorf("peak = %d, want at most the stated buffer 2000", peak)
	}
}

// TestEgressStatedBufferUnderCapacity asserts the same traffic under an
// 8000-octet buffer delivers every frame, leaves Drops empty, and reports a
// peak below the buffer.
func TestEgressStatedBufferUnderCapacity(t *testing.T) {
	buffer := uint64(8000)
	fab, macs := egressBufferTopology(t, &buffer)
	primeEgressLearning(t, fab, macs)
	injectEgressFlow(t, fab, macs, "h1", 1, fabric.RetainAggregate)
	injectEgressFlow(t, fab, macs, "h2", 2, fabric.RetainAggregate)
	if res := fab.Run(1_000_000); res.Stop != fabric.StopQueueDrained {
		t.Fatalf("Run stop = %s, want %s", res.Stop, fabric.StopQueueDrained)
	}

	for _, flow := range []fabric.FlowID{1, 2} {
		stats := fab.Flows()[flow]
		if got := stats.Delivered["h3"]; got != 20 {
			t.Errorf("flow %d Delivered[h3] = %d, want 20", flow, got)
		}
		if len(stats.Drops) != 0 {
			t.Errorf("flow %d Drops = %+v, want none", flow, stats.Drops)
		}
	}

	peak := fab.Snapshot().EgressDepths[fabric.Endpoint{Node: "sw1", Port: "1/1/3"}][0].Peak
	if peak == 0 || peak >= 8000 {
		t.Errorf("peak = %d, want above zero and below the stated buffer 8000", peak)
	}
}

// TestEgressBufferCountsEncodedFrameOctets asserts two 1000-octet-payload
// untagged frames are 1014 encoded octets each, so a 2028-octet buffer admits
// the second and a 2027-octet buffer refuses it. A wire-octet count (1038
// each) would refuse at 2028, separating the two units.
func TestEgressBufferCountsEncodedFrameOctets(t *testing.T) {
	for _, tc := range []struct {
		buffer    uint64
		delivered uint64
	}{
		{buffer: 2028, delivered: 2},
		{buffer: 2027, delivered: 1},
	} {
		t.Run(fmt.Sprintf("buffer_%d", tc.buffer), func(t *testing.T) {
			buffer := tc.buffer
			fab, macs := egressBufferTopology(t, &buffer)
			primeEgressLearning(t, fab, macs)

			at := fab.Snapshot().Clock
			for _, host := range []string{"h1", "h2"} {
				if _, err := fab.Inject(fabric.Injection{
					At:        at,
					Origin:    fabric.Endpoint{Node: host},
					Frame:     egressBufferFrame(macs[host], macs["h3"]),
					Retention: fabric.RetainAggregate,
					Flow:      1,
				}); err != nil {
					t.Fatalf("Inject %s: %v", host, err)
				}
			}
			fab.Run(1_000_000)

			if got := fab.Flows()[1].Delivered["h3"]; got != tc.delivered {
				t.Errorf("buffer %d: Delivered[h3] = %d, want %d", tc.buffer, got, tc.delivered)
			}
		})
	}
}

// TestEgressUnstatedBufferNeverDrops asserts the same fabric with no Queues
// entry delivers every frame and drops none.
func TestEgressUnstatedBufferNeverDrops(t *testing.T) {
	fab, macs := egressBufferTopology(t, nil)
	primeEgressLearning(t, fab, macs)
	injectEgressFlow(t, fab, macs, "h1", 1, fabric.RetainAggregate)
	injectEgressFlow(t, fab, macs, "h2", 2, fabric.RetainAggregate)
	fab.Run(1_000_000)

	for _, flow := range []fabric.FlowID{1, 2} {
		stats := fab.Flows()[flow]
		if got := stats.Delivered["h3"]; got != 20 {
			t.Errorf("flow %d Delivered[h3] = %d, want 20", flow, got)
		}
		if len(stats.Drops) != 0 {
			t.Errorf("flow %d Drops = %+v, want none", flow, stats.Drops)
		}
	}
	if got := fab.Snapshot().Devices["sw1"].Counters["1/1/3"].OutDiscards; got != 0 {
		t.Errorf("OutDiscards = %d, want none on an unstated queue", got)
	}
}

// TestEgressUnstatedBufferReportsPeak asserts ten 1014-octet frames enqueued
// on one endpoint at a single instant make a peak within one frame of ten
// frames' octets and equal to the greatest depth seen; after the drain the
// depth is zero and the peak is unchanged.
func TestEgressUnstatedBufferReportsPeak(t *testing.T) {
	fab, macs := egressBufferTopology(t, nil)
	primeEgressLearning(t, fab, macs)

	at := fab.Snapshot().Clock
	for range 10 {
		if _, err := fab.Inject(fabric.Injection{
			At:     at,
			Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
			Frame:  egressBufferFrame(macs["h2"], macs["h3"]),
		}); err != nil {
			t.Fatalf("Inject: %v", err)
		}
	}

	ep := fabric.Endpoint{Node: "sw1", Port: "1/1/3"}
	var greatestDepth uint64
	for {
		if depth := fab.Snapshot().EgressDepths[ep][0].Depth; depth > greatestDepth {
			greatestDepth = depth
		}
		if _, ok := fab.Step(); !ok {
			break
		}
	}

	if greatestDepth < 9*1014 || greatestDepth > 10*1014 {
		t.Errorf("greatest depth = %d, want between %d and %d", greatestDepth, 9*1014, 10*1014)
	}
	depth := fab.Snapshot().EgressDepths[ep][0]
	if depth.Depth != 0 {
		t.Errorf("depth after the drain = %d, want 0", depth.Depth)
	}
	if depth.Peak != greatestDepth {
		t.Errorf("peak = %d, want the greatest depth seen %d", depth.Peak, greatestDepth)
	}
	if depth.Peak < 9*1014 || depth.Peak > 10*1014 {
		t.Errorf("peak = %d, want between %d and %d", depth.Peak, 9*1014, 10*1014)
	}
}

// TestEgressTailDropEntryCarriesItsFact covers the drop entry's shape: its
// reason, its step's rule and subject, and its typed fact.
func TestEgressTailDropEntryCarriesItsFact(t *testing.T) {
	buffer := uint64(2000)
	fab, macs := egressBufferTopology(t, &buffer)
	primeEgressLearning(t, fab, macs)
	injectEgressFlow(t, fab, macs, "h1", 0, fabric.RetainJourney)
	injectEgressFlow(t, fab, macs, "h2", 0, fabric.RetainJourney)
	if res := fab.Run(1_000_000); res.Stop != fabric.StopQueueDrained {
		t.Fatalf("Run stop = %s, want %s", res.Stop, fabric.StopQueueDrained)
	}

	wantFact := traffic.QueueDropFact(1014, 2000, 1014)
	found := false
	for _, journey := range fab.Report() {
		for _, entry := range journey.Entries {
			if entry.Kind != fabric.EntryDrop || entry.Reason != traffic.ReasonQueueFull {
				continue
			}
			found = true
			if entry.Device != "sw1" || entry.Port != "1/1/3" {
				t.Errorf("drop entry device/port = %s/%s, want sw1/1/1/3", entry.Device, entry.Port)
			}
			if entry.Step == nil {
				t.Fatal("queue-full drop entry has no step")
			}
			if entry.Step.Layer != traffic.Layer || entry.Step.Op != trace.OpDrop || entry.Step.RuleID != traffic.RuleQueueDrop {
				t.Errorf("drop step = %+v, want traffic layer, drop op, rule %s", entry.Step, traffic.RuleQueueDrop)
			}
			if entry.Step.Subject != (trace.Subject{Kind: "port", Key: "1/1/3/0"}) {
				t.Errorf("drop step subject = %+v, want port 1/1/3/0", entry.Step.Subject)
			}
			if len(entry.Step.Inputs) != 1 || trace.CompareFact(entry.Step.Inputs[0], wantFact) != 0 {
				t.Errorf("drop step inputs = %+v, want %+v", entry.Step.Inputs, wantFact)
			}
		}
	}
	if !found {
		t.Fatalf("no queue-full drop entry among: %+v", fab.Report())
	}
}
