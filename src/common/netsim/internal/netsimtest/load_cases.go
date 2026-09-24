package netsimtest

import (
	"fmt"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/stream"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
)

var (
	loadH1 = netaddr.MAC{0x02, 0, 0, 0, 0x13, 1}
	loadH2 = netaddr.MAC{0x02, 0, 0, 0, 0x13, 2}
)

func loadCaseSpec(buffer *uint64, policed bool) (fabric.ConstructionSpec, error) {
	ports, err := port.NewBuilder().Range("1/1/%d", 1, 2,
		port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).Build()
	if err != nil {
		return fabric.ConstructionSpec{}, err
	}
	vid10 := vlan.ID(10)
	physical := func(speed uint64) phy.Ethernet {
		return phy.Ethernet{
			SupportedSpeedsBPS:       []uint64{speed},
			AutoNegotiationSupported: phy.CapabilitySupported,
			Setting:                  &phy.Setting{AutoNegotiation: true},
		}
	}
	trafficConfig := &traffic.Config{}
	if buffer != nil {
		trafficConfig.Queues = map[string]traffic.PortQueues{
			"1/1/2": {BufferOctets: map[vlan.PCP]uint64{0: *buffer}},
		}
	}
	if policed {
		trafficConfig.Policers = map[string]traffic.Policer{
			"1/1/1": {RateBPS: 1_000_000, BurstOctets: 1518},
		}
	}
	return fabric.ConstructionSpec{
		Start: time.Unix(0, 0).UTC(),
		Switches: map[string]vswitch.ConstructionSpec{
			"sw1": {
				NodeID: "sw1",
				Config: vswitch.Config{
					Ports: ports,
					Bridge: &bridge.Config{VLAN: &bridge.VLAN{
						Table: map[vlan.ID]string{10: "load"},
						Switchports: map[string]bridge.Switchport{
							"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
							"1/1/2": {Tagged: []vlan.ID{10}},
						},
					}},
					Phy: &phy.Config{Ethernet: map[string]phy.Ethernet{
						"1/1/1": physical(1_000_000_000),
						"1/1/2": physical(10_000_000),
					}},
					Traffic: trafficConfig,
				},
				Seeds: []bridge.Seed{{FID: 10, MAC: loadH2, Port: "1/1/2", Lifetime: bridge.Static}},
			},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: loadH1, Ethernet: physical(1_000_000_000)},
			"h2": {Address: loadH2, VLAN: &vid10, Ethernet: physical(10_000_000)},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, Medium: fabric.TwistedPair, TopSpeedBPS: 1_000_000_000},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "h2"}, Medium: fabric.TwistedPair, TopSpeedBPS: 10_000_000},
		},
	}, nil
}

func executeLoadCase(buffer *uint64, policed bool, decisive trace.RuleID) (ExecutionResult, error) {
	spec, err := loadCaseSpec(buffer, policed)
	if err != nil {
		return ExecutionResult{}, err
	}
	fab, err := fabric.NewWithSpec(spec)
	if err != nil {
		return ExecutionResult{}, err
	}
	source, err := (stream.Spec{
		Frame: ethernet.Frame{Src: loadH1, Dst: loadH2, EtherType: ethernet.EtherTypeIPv4, Payload: make([]byte, 1000)},
		Rate:  stream.Rate{FramesPerSecond: 10_000}, Count: 32,
	}).Source()
	if err != nil {
		return ExecutionResult{}, err
	}
	const flow = fabric.FlowID(13)
	if err := fab.AttachStream(fabric.StreamAttachment{
		Origin: fabric.Endpoint{Node: "h1"}, Source: source, Flow: flow, Retention: fabric.RetainJourney,
	}); err != nil {
		return ExecutionResult{}, err
	}
	if run := fab.Run(10_000); run.Err != nil || run.Pending.Journeys != 0 {
		return ExecutionResult{}, fmt.Errorf("load run did not settle: %+v", run)
	}
	stats, ok := fab.Flows()[flow]
	if !ok || stats.Offered != 32 {
		return ExecutionResult{}, fmt.Errorf("flow %d offered %d frames, found %t; want 32", flow, stats.Offered, ok)
	}
	if decisive == traffic.RuleQueueBufferUnstated && len(stats.Drops) != 0 {
		return ExecutionResult{}, fmt.Errorf("unstated queue dropped flow %d frames: %v", flow, stats.Drops)
	}
	for _, j := range fab.Report() {
		if j.Injection.Flow != flow {
			continue
		}
		for _, step := range journeySteps(j) {
			if step.RuleID != decisive {
				continue
			}
			fabricMetadata := fab.Metadata()
			result := ExecutionResult{
				Steps: journeySteps(j), Metadata: j.Metadata, Journey: &j,
				FabricMetadata: &fabricMetadata,
			}
			switch j.State {
			case fabric.JourneyDelivered:
				result.Outcome = trace.Forwarded
			case fabric.JourneyDropped:
				result.Outcome = trace.Dropped
			default:
				return ExecutionResult{}, fmt.Errorf("selected frame %d state %s", j.FrameID, j.State)
			}
			for _, entry := range j.Entries {
				if entry.Kind == fabric.EntryDrop {
					result.Reason = entry.Reason
				}
				if entry.Result != nil && entry.Result.Outcome == trace.Dropped {
					result.Reason = entry.Result.Reason
				}
			}
			return result, nil
		}
	}
	return ExecutionResult{}, fmt.Errorf("no flow %d frame records rule %s", flow, decisive)
}

func loadForwardSteps() []StepExpectation {
	frame := expectedFact("bridge.frame", `src="02:00:00:00:13:01";dst="02:00:00:00:13:02";ether_type=2048;tags=[];payload_len=1000`)
	tagged := expectedFact("bridge.frame", `src="02:00:00:00:13:01";dst="02:00:00:00:13:02";ether_type=2048;tags=[{tpid=33024;pcp=0;dei=false;vid=10}];payload_len=1000`)
	learn := expectedFact("bridge.fdb_decision", `fid=10;mac="02:00:00:00:13:01";present=true;port="1/1/1";static=false`)
	return []StepExpectation{
		expectedStep("vlan", trace.OpClassify, "vlan-classify", trace.Subject{Kind: "vlan", Key: "10"},
			[]FactExpectation{frame}, []FactExpectation{expectedFact("bridge.vlan_decision", `port="1/1/1";fid=10;pcp=0;dei=false;form="untagged"`)}),
		expectedStep("relay", trace.OpLearn, "learn", trace.Subject{Kind: "mac", Key: "02:00:00:00:13:01"},
			[]FactExpectation{learn}, []FactExpectation{learn}),
		expectedStep("relay", trace.OpLookup, "unicast-hit", trace.Subject{Kind: "mac", Key: "02:00:00:00:13:02"},
			[]FactExpectation{frame}, []FactExpectation{expectedFact("bridge.fdb_decision", `fid=10;mac="02:00:00:00:13:02";present=true;port="1/1/2";static=true`)}),
		expectedStep("vlan", trace.OpRewrite, "vlan-tag-form", trace.Subject{Kind: "port", Key: "1/1/2"},
			[]FactExpectation{frame}, []FactExpectation{tagged, expectedFact("bridge.vlan_decision", `port="1/1/2";fid=10;pcp=0;dei=false;form="egress"`)}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "1/1/2"},
			[]FactExpectation{tagged}, []FactExpectation{expectedFact("bridge.egress_decision", `port="1/1/2";member="";fid=10;eligible=true;reason=""`)}),
	}
}

// CasePlanningOversubscribedTrunkStatedBuffer proves a 1518-octet egress
// buffer tail-drops a frame offered faster than the tagged trunk can serve it.
func CasePlanningOversubscribedTrunkStatedBuffer() Case {
	buffer := uint64(1518)
	queueFact := NewFactExpectation(traffic.QueueDropFact(1018, buffer, 1018))
	steps := append(loadForwardSteps(), expectedStep(traffic.Layer, trace.OpDrop, traffic.RuleQueueDrop,
		trace.Subject{Kind: "port", Key: "1/1/2/0"}, []FactExpectation{queueFact}, nil))
	return Case{
		ID: "planning/oversubscribed-trunk-stated-buffer", UseCase: UseCasePlanning,
		Question:         "Which frame is lost when a 10,000 frame/s stream feeds a 10 Mbit/s tagged trunk with a 1518-octet buffer?",
		FalseAnswer:      "Every offered frame is delivered because queue occupancy is ignored",
		CurrentResult:    "The third frame is tail-dropped when it would raise the egress queue from 1018 to 2036 encoded octets",
		ExpectedMetadata: &MetadataExpectation{Status: analysis.Complete, Scope: analysis.WholeScope()},
		ExpectedOutcome:  trace.Dropped, ExpectedReason: traffic.ReasonQueueFull,
		ExpectedRules:    []trace.RuleID{traffic.RuleQueueDrop},
		ExpectedSubjects: []trace.Subject{{Kind: "port", Key: "1/1/2/0"}},
		ExpectedFacts:    []FactExpectation{queueFact}, ExpectedSteps: steps,
		Execute: func() (ExecutionResult, error) { return executeLoadCase(&buffer, false, traffic.RuleQueueDrop) },
	}
}

// CasePlanningOversubscribedTrunkUnstatedBuffer proves a delivered frame
// carries the first queue crossing and its runtime evidence despite no drop.
func CasePlanningOversubscribedTrunkUnstatedBuffer() Case {
	context := `rule="traffic.queue.buffer-unstated";physical_node="sw1";physical_port="1/1/2";egress_port="1/1/2";pcp=0;frame_id=3;at="1970-01-01T00:00:00.000208304Z";depth_before_octets=1018;frame_octets=1018;threshold_octets=1518`
	catalog, ref := analysis.EvidenceCatalog{}.Add(analysis.Evidence{Kind: "fabric.runtime", Origin: "egress-queue", Context: context})
	queueFact := NewFactExpectation(traffic.QueueThresholdFact(1018, 1018, 1518))
	queueStep := expectedStep(traffic.Layer, trace.OpQueue, traffic.RuleQueueBufferUnstated,
		trace.Subject{Kind: "port", Key: "1/1/2/0"}, []FactExpectation{queueFact}, nil)
	queueStep.Evidence = []trace.EvidenceRef{ref}
	steps := append(loadForwardSteps(), queueStep,
		expectedStep("host", trace.OpFilter, "host.mac.own", trace.Subject{Kind: "host", Key: "h2"},
			[]FactExpectation{
				expectedFact("fabric.mac", "02:00:00:00:13:02"),
				expectedFact("fabric.vlan_tags", "[{tpid=0x8100;pcp=0;dei=false;vid=10}]"),
			}, nil))
	return Case{
		ID: "planning/oversubscribed-trunk-unstated-buffer", UseCase: UseCasePlanning,
		Question:      "What evidence limits a loss estimate when the oversubscribed trunk states no queue buffer size?",
		FalseAnswer:   "A complete zero-loss estimate from an unbounded queue with no measured buffer",
		CurrentResult: "The third frame crosses 1518 encoded octets, retains runtime evidence, and reaches h2 while the queue issue marks readiness Incomplete",
		ExpectedMetadata: &MetadataExpectation{
			Status: analysis.Incomplete, Scope: analysis.WholeScope(),
			Issues: []IssueExpectation{{
				Code: fabric.IssueQueueBufferUnstated, Status: analysis.Incomplete,
				Scope: analysis.PortScope("sw1", "1/1/2"), Evidence: []trace.EvidenceRef{ref},
			}},
			Evidence: catalog.Entries(),
		},
		ExpectedOutcome:  trace.Forwarded,
		ExpectedRules:    []trace.RuleID{traffic.RuleQueueBufferUnstated, "host.mac.own"},
		ExpectedSubjects: []trace.Subject{{Kind: "port", Key: "1/1/2/0"}, {Kind: "host", Key: "h2"}},
		ExpectedFacts:    []FactExpectation{queueFact}, ExpectedSteps: steps,
		Execute: func() (ExecutionResult, error) { return executeLoadCase(nil, false, traffic.RuleQueueBufferUnstated) },
	}
}

// CasePlanningPolicedStream proves the ingress token bucket refuses the
// second frame even though the first frame has enough burst tokens.
func CasePlanningPolicedStream() Case {
	policerFact := NewFactExpectation(traffic.PolicerDecisionFact(1_000_000, 1518, 1038, false))
	return Case{
		ID: "planning/policed-stream", UseCase: UseCasePlanning,
		Question:         "Does a 1 Mbit/s ingress policer with a 1518-octet burst refuse a 10,000 frame/s stream?",
		FalseAnswer:      "The offered stream is admitted without consulting the ingress token bucket",
		CurrentResult:    "The second frame is refused at ingress after the first frame consumes the initial burst",
		ExpectedMetadata: &MetadataExpectation{Status: analysis.Complete, Scope: analysis.WholeScope()},
		ExpectedOutcome:  trace.Dropped, ExpectedReason: "policed",
		ExpectedRules:    []trace.RuleID{traffic.RulePolicerRefuse},
		ExpectedSubjects: []trace.Subject{{Kind: "port", Key: "1/1/1"}},
		ExpectedFacts:    []FactExpectation{policerFact},
		ExpectedSteps: []StepExpectation{expectedStep(traffic.Layer, trace.OpDrop, traffic.RulePolicerRefuse,
			trace.Subject{Kind: "port", Key: "1/1/1"}, nil, []FactExpectation{policerFact})},
		Execute: func() (ExecutionResult, error) { return executeLoadCase(nil, true, traffic.RulePolicerRefuse) },
	}
}
