package fabric_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
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
