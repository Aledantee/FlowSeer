package simtest

import (
	"net/netip"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/assembly"

	hostmodel "go.aledante.io/FlowSeer/src/common/sim/device/host"

	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/arp"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func routedECMPPorts() (port.Table, error) {
	return port.NewBuilder().
		Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "out-a", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "out-b", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
}

// casePlanningECMPCandidatesRecorded returns the case evaluating whether a lookup
// over two equal-cost static routes records the path the packet did not take.
func casePlanningECMPCandidatesRecorded() Case {
	routerMAC := netaddr.MAC{0x02, 0, 0, 0, 0, 0x01}
	hostMAC := netaddr.MAC{0x02, 0, 0, 0, 0, 0x51}
	nextHopMACA := netaddr.MAC{0x02, 0, 0, 0, 0, 0xa1}
	nextHopMACB := netaddr.MAC{0x02, 0, 0, 0, 0, 0xb1}
	nextHopA := netip.MustParseAddr("10.0.20.7")
	nextHopB := netip.MustParseAddr("10.0.30.7")
	source := netip.MustParseAddr("10.0.10.7")
	destination := netip.MustParseAddr("10.0.99.5")

	packetIn := expectedFact("routing.packet_decision",
		`interface="in";ether_type=2048;src="10.0.10.7";dst="10.0.99.5";hop_limit=64;valid=true;reason=""`)
	lookup := expectedFact("routing.lookup_decision",
		`vrf="default";destination="10.0.99.5";matched=true;prefix="10.0.99.0/24";next_hop="10.0.20.7";`+
			`interface="out-a";kind="static";hash_src="10.0.10.7";hash_dst="10.0.99.5";hash_flow_label=0;`+
			`hash=2072557066;chosen=0;candidates=[10.0.20.7|out-a|10.0.20.7,10.0.30.7|out-b|10.0.30.7]`)
	neighbor := expectedFact("routing.neighbor_decision",
		`interface="out-a";address="10.0.20.7";state="reachable";mac="02:00:00:00:00:a1";origin="configured"`)
	packetOut := expectedFact("routing.packet_decision",
		`interface="out-a";ether_type=2048;src="10.0.10.7";dst="10.0.99.5";hop_limit=63;valid=true;reason=""`)

	return Case{
		ID:      "planning/ecmp-candidates-recorded",
		UseCase: UseCasePlanning,
		Question: "Two equal-cost static routes reach one prefix. Does the trace record both, " +
			"so a reader can tell which path the flow would move to if one failed?",
		FalseAnswer: "One route wins and the alternatives are invisible",
		CurrentResult: "The lookup fact names both next hops in canonical order and the index of " +
			"the one the flow hash chose, so the unused path is visible beside the used one",
		ExpectedMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		ExpectedOutcome: trace.Forwarded,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("classify"),
			trace.RuleID("static"),
			trace.RuleID("decrement-ttl"),
			trace.RuleID("routing.transmit"),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "interface", Key: "in"},
			{Kind: "prefix", Key: "10.0.99.0/24"},
			{Kind: "interface", Key: "out-a"},
			{Kind: "port", Key: "out-a"},
		},
		ExpectedFacts: []FactExpectation{lookup},
		ExpectedSteps: []StepExpectation{
			expectedStep(routing.LayerName, trace.OpClassify, "classify", trace.Subject{Kind: "interface", Key: "in"},
				[]FactExpectation{packetIn},
				[]FactExpectation{expectedFact("routing.route.interface", "in")}),
			expectedStep(routing.LayerName, trace.OpLookup, "static", trace.Subject{Kind: "prefix", Key: "10.0.99.0/24"},
				[]FactExpectation{packetIn}, []FactExpectation{lookup}),
			expectedStep(routing.LayerName, trace.OpRewrite, "decrement-ttl", trace.Subject{Kind: "interface", Key: "out-a"},
				[]FactExpectation{neighbor, packetIn}, []FactExpectation{packetOut}),
			expectedStep(routing.LayerName, trace.OpTransmit, "routing.transmit", trace.Subject{Kind: "port", Key: "out-a"},
				nil, []FactExpectation{expectedFact("routing.egress_decision", `interface="out-a";port="out-a";member="";reason=""`)}),
		},
		ExpectedForwardMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		Execute: func() (ExecutionResult, error) {
			ports, err := routedECMPPorts()
			if err != nil {
				return ExecutionResult{}, err
			}

			spec := vswitch.ConstructionSpec{
				Config: vswitch.Config{
					Ports: ports,
					Routing: &routing.Config{VRFs: map[string]routing.VRF{
						routing.DefaultVRF: {
							Interfaces: map[string]routing.Interface{
								"in":    {Port: "in", MAC: routerMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
								"out-a": {Port: "out-a", MAC: routerMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
								"out-b": {Port: "out-b", MAC: routerMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.30.1/24")}},
							},
							Routes: []routing.Route{
								{Prefix: netip.MustParsePrefix("10.0.99.0/24"), NextHop: nextHopA, Preference: 1, Metric: 10},
								{Prefix: netip.MustParsePrefix("10.0.99.0/24"), NextHop: nextHopB, Preference: 1, Metric: 10},
							},
							Neighbors: []routing.Neighbor{
								{Interface: "out-a", Addr: nextHopA, MAC: nextHopMACA},
								{Interface: "out-b", Addr: nextHopB, MAC: nextHopMACB},
							},
						},
					}},
				},
			}

			sw, err := vswitch.NewWithSpec(spec)
			if err != nil {
				return ExecutionResult{}, err
			}

			hdr := ip.Header{Src: source, Dst: destination, HopLimit: 64, Protocol: 17, V4: &ip.V4{}}
			pkt, err := hdr.Encode([]byte("flow"))
			if err != nil {
				return ExecutionResult{}, err
			}

			fwd := sw.Receive(time.Unix(1700000000, 0), "in", ethernet.Frame{
				Dst:       routerMAC,
				Src:       hostMAC,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   pkt,
			})
			if err := validateReceiveResult(fwd.Result); err != nil {
				return ExecutionResult{}, err
			}

			return ExecutionResult{
				Outcome:  fwd.Outcome,
				Reason:   fwd.Reason,
				Steps:    fwd.Steps,
				Metadata: fwd.Metadata,
				Switch:   sw,
				Forward:  &fwd.Result,
			}, nil
		},
	}
}

// caseTroubleshootingNeighborResolutionPending returns the case evaluating what a
// routed forward does when its next hop has no configured or observed neighbor entry:
// whether it is indistinguishable from a definite drop, or a hold netsim can still release.
func caseTroubleshootingNeighborResolutionPending() Case {
	routerMAC := netaddr.MAC{0x02, 0, 0, 0, 0, 0x01}
	hostMAC := netaddr.MAC{0x02, 0, 0, 0, 0, 0x51}
	learnedMAC := netaddr.MAC{0x02, 0, 0, 0, 0, 0xa1}
	source := netip.MustParseAddr("10.0.10.7")
	destination := netip.MustParseAddr("10.0.20.77")

	packetIn := expectedFact("routing.packet_decision",
		`interface="in";ether_type=2048;src="10.0.10.7";dst="10.0.20.77";hop_limit=64;valid=true;reason=""`)
	lookup := expectedFact("routing.lookup_decision",
		`vrf="default";destination="10.0.20.77";matched=true;prefix="10.0.20.0/24";next_hop="invalid IP";`+
			`interface="out";kind="connected";hash_src="10.0.10.7";hash_dst="10.0.20.77";hash_flow_label=0;`+
			`hash=336306069;chosen=0;candidates=[invalid IP|out|invalid IP]`)
	neighborPending := expectedFact("routing.neighbor_decision",
		`interface="out";address="10.0.20.77";state="incomplete";mac="00:00:00:00:00:00";origin="observed"`)

	neighborScope := routing.NeighborLookupScope("", "default", "out", destination)
	var cat analysis.EvidenceCatalog
	neighborUnresolvedEvidence := analysis.Evidence{
		Kind:   "vswitch.runtime",
		Origin: "forward",
		Context: `code="neighbor-unresolved",status="incomplete",` +
			`scope="node[\"\"]/protocol[\"routing\",\"default\"]/field[\"interfaces\",\"out\",\"neighbors\",\"10.0.20.77\"]"`,
	}
	var refNeighborUnresolved trace.EvidenceRef
	cat, refNeighborUnresolved = cat.Add(neighborUnresolvedEvidence)
	expectedIssue := IssueExpectation{
		Code:     vswitch.IssueNeighborUnresolved,
		Status:   analysis.Incomplete,
		Scope:    neighborScope,
		Evidence: []trace.EvidenceRef{refNeighborUnresolved},
	}
	resultMetadata := MetadataExpectation{
		Status:   analysis.Incomplete,
		Scope:    analysis.NodeScope(""),
		Issues:   []IssueExpectation{expectedIssue},
		Evidence: cat.Entries(),
	}.Canonical()

	expectedSteps := []StepExpectation{
		expectedStep(routing.LayerName, trace.OpClassify, "classify", trace.Subject{Kind: "interface", Key: "in"},
			[]FactExpectation{packetIn},
			[]FactExpectation{expectedFact("routing.route.interface", "in")}),
		expectedStep(routing.LayerName, trace.OpLookup, "connected", trace.Subject{Kind: "prefix", Key: "10.0.20.0/24"},
			[]FactExpectation{packetIn}, []FactExpectation{lookup}),
		expectedStep(routing.LayerName, trace.OpLookup, trace.RuleID(routing.ReasonNeighborPending), trace.Subject{Kind: "ip", Key: "10.0.20.77"},
			[]FactExpectation{lookup}, []FactExpectation{neighborPending}),
	}

	return Case{
		ID:      "troubleshooting/neighbor-resolution-pending",
		UseCase: UseCaseTroubleshooting,
		Question: "A routed forward's next hop has no configured neighbor and none has been " +
			"observed yet. Does the frame drop, or does netsim hold it as an open question?",
		FalseAnswer: "An unresolved next hop is a definite drop, so a plan reads a missing " +
			"neighbor as a misconfiguration rather than as something netsim has not observed",
		CurrentResult: "The frame holds with outcome Held and reason neighbor-pending, and the " +
			"forward's metadata carries IssueNeighborUnresolved at Incomplete scoped to the " +
			"neighbor lookup, rather than a Complete drop",
		ExpectedMetadata: &resultMetadata,
		ExpectedOutcome:  trace.Held,
		ExpectedReason:   routing.ReasonNeighborPending,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("classify"),
			trace.RuleID("connected"),
			trace.RuleID(routing.ReasonNeighborPending),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "interface", Key: "in"},
			{Kind: "prefix", Key: "10.0.20.0/24"},
			{Kind: "ip", Key: "10.0.20.77"},
		},
		ExpectedFacts:           []FactExpectation{lookup, neighborPending},
		ExpectedSteps:           expectedSteps,
		ExpectedForwardMetadata: &resultMetadata,
		Execute: func() (ExecutionResult, error) {
			return executeTroubleshootingNeighborResolutionPending(routerMAC, hostMAC, learnedMAC, source, destination)
		},
	}
}

func executeTroubleshootingNeighborResolutionPending(routerMAC, hostMAC, learnedMAC netaddr.MAC, source, destination netip.Addr) (ExecutionResult, error) {
	ports, err := port.NewBuilder().
		Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "out", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
	if err != nil {
		return ExecutionResult{}, err
	}

	spec := vswitch.ConstructionSpec{
		Config: vswitch.Config{
			Ports: ports,
			Routing: &routing.Config{VRFs: map[string]routing.VRF{
				routing.DefaultVRF: {
					Interfaces: map[string]routing.Interface{
						"in":  {Port: "in", MAC: routerMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"out": {Port: "out", MAC: routerMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
					},
					// No neighbors configured: the next hop is on-link but unresolved.
				},
			}},
		},
	}

	sw, err := vswitch.NewWithSpec(spec)
	if err != nil {
		return ExecutionResult{}, err
	}

	now := time.Unix(1700000000, 0)

	hdr := ip.Header{Src: source, Dst: destination, HopLimit: 64, Protocol: 17, V4: &ip.V4{}}
	pkt, err := hdr.Encode([]byte("hello"))
	if err != nil {
		return ExecutionResult{}, err
	}

	fwd := sw.Receive(now, "in", ethernet.Frame{
		Dst:       routerMAC,
		Src:       hostMAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	})
	if err := validateReceiveResult(fwd.Result); err != nil {
		return ExecutionResult{}, err
	}

	// The reply arrives on the retransmit deadline, after the second solicitation.
	replyMsg := arp.Message{
		HardwareType: 1,
		ProtocolType: 0x0800,
		Operation:    arp.Reply,
		SenderMAC:    learnedMAC,
		SenderAddr:   destination,
		TargetMAC:    routerMAC,
		TargetAddr:   netip.MustParseAddr("10.0.20.1"),
	}
	reply, err := arp.Encode(replyMsg, routerMAC)
	if err != nil {
		return ExecutionResult{}, err
	}
	received := sw.Receive(now.Add(time.Second), "out", reply)
	if err := validateReceiveResult(received.Result); err != nil {
		return ExecutionResult{}, err
	}
	advanced := sw.Advance(now.Add(time.Second))

	var emissions []device.Emission
	solicitations := 0
	for _, emission := range append(append(fwd.Emissions, received.Emissions...), advanced.Emissions...) {
		if !emission.Protocol {
			emissions = append(emissions, emission)
			continue
		}
		msg, err := arp.Decode(emission.Frame)
		if err != nil || msg.Operation != arp.Request || msg.TargetAddr != destination || emission.Port != "out" || emission.PCP != 0 {
			return ExecutionResult{}, errs.Msgf("solicitation = %+v, message = %+v, error = %v", emission, msg, err)
		}
		solicitations++
	}
	if solicitations != 2 {
		return ExecutionResult{}, errs.Msgf("solicitations = %d, want 2", solicitations)
	}
	if len(emissions) != 1 {
		return ExecutionResult{}, errs.Msgf("released emissions = %d, want 1", len(emissions))
	}
	if emissions[0].Frame.Dst != learnedMAC {
		return ExecutionResult{}, errs.Msgf("released frame dst = %v, want %v", emissions[0].Frame.Dst, learnedMAC)
	}
	if emissions[0].Port != "out" || emissions[0].Protocol {
		return ExecutionResult{}, errs.Msgf("released emission = {Port: %v, Protocol: %v}, want {Port: out, Protocol: false}", emissions[0].Port, emissions[0].Protocol)
	}
	if failures := append(append(fwd.Drops, received.Drops...), advanced.Drops...); len(failures) != 0 {
		return ExecutionResult{}, errs.Msgf("neighbor failures = %d, want 0: %+v", len(failures), failures)
	}

	// The same unresolved forward must finish under its original fabric identity.
	fabricSwitch := spec.Clone()
	fabricSwitch.NodeID = "router"
	gigabit := gigabitAuto()
	fabricSwitch.Config.Phy = &phy.Config{Ethernet: map[string]phy.Ethernet{"in": gigabit, "out": gigabit}}
	fab, err := assembly.NewWithSpec(assembly.ConstructionSpec{
		Start:    now,
		Switches: map[string]vswitch.ConstructionSpec{"router": fabricSwitch},
		Hosts: map[string]hostmodel.Config{
			"source": {Address: hostMAC, Ethernet: gigabit},
			"target": {Address: learnedMAC, Ethernet: gigabit},
		},
		Cables: []fabric.Cable{
			{Cable: cablemodel.Cable{Medium: cablemodel.TwistedPair}, A: fabric.Endpoint{Node: "source"}, B: fabric.Endpoint{Node: "router", Port: "in"}},
			{Cable: cablemodel.Cable{Medium: cablemodel.TwistedPair}, A: fabric.Endpoint{Node: "target"}, B: fabric.Endpoint{Node: "router", Port: "out"}},
		},
	})
	if err != nil {
		return ExecutionResult{}, err
	}
	fid, err := fab.Inject(fabric.Injection{
		At: now, Origin: fabric.Endpoint{Node: "router", Port: "in"}, Flow: 1,
		Frame: ethernet.Frame{Dst: routerMAC, Src: hostMAC, EtherType: ethernet.EtherTypeIPv4, Payload: pkt},
	})
	if err != nil {
		return ExecutionResult{}, err
	}
	if _, err := fab.Inject(fabric.Injection{At: now.Add(time.Second), Origin: fabric.Endpoint{Node: "router", Port: "out"}, Frame: reply}); err != nil {
		return ExecutionResult{}, err
	}
	run := fab.Run(fabric.RunOptions{Budget: 1000, Window: 0})
	if err := validateReceiveReport(fab.Report()); err != nil {
		return ExecutionResult{}, err
	}
	journey, err := reportJourney(fab, fid)
	if err != nil {
		return ExecutionResult{}, err
	}
	if run.Err != nil || run.Stop != fabric.StopQueueDrained || journey.State != fabric.JourneyDelivered || len(journey.Deliveries) != 1 || journey.Deliveries[0].Host != "target" {
		return ExecutionResult{}, errs.Msgf("held journey %d = %+v, stop = %v, want one delivery to target and a drained queue", fid, journey, run.Stop)
	}
	stats := fab.Flows()
	if len(stats) != 1 || stats[1].Offered != 1 || stats[1].Delivered["target"] != 1 || stats[1].Held != 0 {
		return ExecutionResult{}, errs.Msgf("released flow = %+v, want one offered, one target delivery, no holds", stats)
	}

	return ExecutionResult{
		Outcome:  fwd.Outcome,
		Reason:   fwd.Reason,
		Steps:    fwd.Steps,
		Metadata: fwd.Metadata,
		Switch:   sw,
		Forward:  &fwd.Result,
	}, nil
}

// caseTroubleshootingRecursiveRouteNotInstalled returns the case evaluating what a
// static route whose next hop resolves to nothing does to the packets it would match.
func caseTroubleshootingRecursiveRouteNotInstalled() Case {
	routerMAC := netaddr.MAC{0x02, 0, 0, 0, 0, 0x01}
	hostMAC := netaddr.MAC{0x02, 0, 0, 0, 0, 0x51}
	nextHopMAC := netaddr.MAC{0x02, 0, 0, 0, 0, 0xa1}
	nextHop := netip.MustParseAddr("10.0.20.7")
	unresolvable := netip.MustParseAddr("192.0.2.1")
	source := netip.MustParseAddr("10.0.10.7")
	destination := netip.MustParseAddr("10.0.99.5")

	packetIn := expectedFact("routing.packet_decision",
		`interface="in";ether_type=2048;src="10.0.10.7";dst="10.0.99.5";hop_limit=64;valid=true;reason=""`)
	lookup := expectedFact("routing.lookup_decision",
		`vrf="default";destination="10.0.99.5";matched=true;prefix="10.0.0.0/8";next_hop="10.0.20.7";`+
			`interface="out";kind="static";hash_src="10.0.10.7";hash_dst="10.0.99.5";hash_flow_label=0;`+
			`hash=2072557066;chosen=0;candidates=[10.0.20.7|out|10.0.20.7]`)
	neighbor := expectedFact("routing.neighbor_decision",
		`interface="out";address="10.0.20.7";state="reachable";mac="02:00:00:00:00:a1";origin="configured"`)
	packetOut := expectedFact("routing.packet_decision",
		`interface="out";ether_type=2048;src="10.0.10.7";dst="10.0.99.5";hop_limit=63;valid=true;reason=""`)

	return Case{
		ID:      "troubleshooting/recursive-route-not-installed",
		UseCase: UseCaseTroubleshooting,
		Question: "A more specific static route names a next hop that no other route reaches. " +
			"What carries a packet the route would have matched?",
		FalseAnswer: "A broken recursive route silently forwards, or fails construction, " +
			"instead of leaving the table",
		CurrentResult: "The device constructs, the unresolvable /24 is withdrawn rather than " +
			"installed, and the packet takes the less specific /8 with a Complete result",
		ExpectedMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		ExpectedOutcome: trace.Forwarded,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("classify"),
			trace.RuleID("static"),
			trace.RuleID("decrement-ttl"),
			trace.RuleID("routing.transmit"),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "interface", Key: "in"},
			{Kind: "prefix", Key: "10.0.0.0/8"},
			{Kind: "interface", Key: "out"},
			{Kind: "port", Key: "out"},
		},
		ExpectedFacts: []FactExpectation{lookup},
		ExpectedSteps: []StepExpectation{
			expectedStep(routing.LayerName, trace.OpClassify, "classify", trace.Subject{Kind: "interface", Key: "in"},
				[]FactExpectation{packetIn},
				[]FactExpectation{expectedFact("routing.route.interface", "in")}),
			expectedStep(routing.LayerName, trace.OpLookup, "static", trace.Subject{Kind: "prefix", Key: "10.0.0.0/8"},
				[]FactExpectation{packetIn}, []FactExpectation{lookup}),
			expectedStep(routing.LayerName, trace.OpRewrite, "decrement-ttl", trace.Subject{Kind: "interface", Key: "out"},
				[]FactExpectation{neighbor, packetIn}, []FactExpectation{packetOut}),
			expectedStep(routing.LayerName, trace.OpTransmit, "routing.transmit", trace.Subject{Kind: "port", Key: "out"},
				nil, []FactExpectation{expectedFact("routing.egress_decision", `interface="out";port="out";member="";reason=""`)}),
		},
		ExpectedForwardMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		Execute: func() (ExecutionResult, error) {
			ports, err := port.NewBuilder().
				Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
				Add(port.Port{Name: "out", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
				Build()
			if err != nil {
				return ExecutionResult{}, err
			}

			spec := vswitch.ConstructionSpec{
				Config: vswitch.Config{
					Ports: ports,
					Routing: &routing.Config{VRFs: map[string]routing.VRF{
						routing.DefaultVRF: {
							Interfaces: map[string]routing.Interface{
								"in":  {Port: "in", MAC: routerMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
								"out": {Port: "out", MAC: routerMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
							},
							Routes: []routing.Route{
								{Prefix: netip.MustParsePrefix("10.0.99.0/24"), NextHop: unresolvable},
								{Prefix: netip.MustParsePrefix("10.0.0.0/8"), NextHop: nextHop},
							},
							Neighbors: []routing.Neighbor{
								{Interface: "out", Addr: nextHop, MAC: nextHopMAC},
							},
						},
					}},
				},
			}

			sw, err := vswitch.NewWithSpec(spec)
			if err != nil {
				return ExecutionResult{}, err
			}

			hdr := ip.Header{Src: source, Dst: destination, HopLimit: 64, Protocol: 17, V4: &ip.V4{}}
			pkt, err := hdr.Encode([]byte("flow"))
			if err != nil {
				return ExecutionResult{}, err
			}

			fwd := sw.Receive(time.Unix(1700000000, 0), "in", ethernet.Frame{
				Dst:       routerMAC,
				Src:       hostMAC,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   pkt,
			})
			if err := validateReceiveResult(fwd.Result); err != nil {
				return ExecutionResult{}, err
			}

			return ExecutionResult{
				Outcome:  fwd.Outcome,
				Reason:   fwd.Reason,
				Steps:    fwd.Steps,
				Metadata: fwd.Metadata,
				Switch:   sw,
				Forward:  &fwd.Result,
			}, nil
		},
	}
}
