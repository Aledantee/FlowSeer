package netsimtest

import (
	"encoding/binary"
	"net/netip"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/tcp"
	"go.aledante.io/FlowSeer/src/common/net/udp"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/filter"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
)

// RegisterFilterCases populates registry with the packet filter cases covering
// first-match drops, stateful counterpart acceptance, and planning rule diffs.
func RegisterFilterCases(registry *Registry) {
	registry.MustRegister(CasePlanningFilterRuleChange())
	registry.MustRegister(CaseTroubleshootingFilterDropsMDNSUnicastProbe())
	registry.MustRegister(CaseTroubleshootingStatefulReplyAllowed())
}

var (
	filterCaseMacRouter = netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0xfe}
	filterCaseMacClient = netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x01}
	filterCaseMacServer = netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x02}

	filterCaseIPClient = netip.MustParseAddr("10.0.10.7")
	filterCaseIPServer = netip.MustParseAddr("10.0.20.5")
)

func filterCasePorts() (port.Table, error) {
	return port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
}

func filterCaseBaseConfig() (vswitch.Config, error) {
	ports, err := filterCasePorts()
	if err != nil {
		return vswitch.Config{}, err
	}

	vid10 := vlan.ID(10)
	vid20 := vlan.ID(20)

	return vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{
					10: "lan",
					20: "srv",
				},
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
						"vlan10": {VLAN: 10, MAC: filterCaseMacRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"vlan20": {VLAN: 20, MAC: filterCaseMacRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
					},
					Neighbors: []routing.Neighbor{
						{Interface: "vlan10", Addr: filterCaseIPClient, MAC: filterCaseMacClient},
						{Interface: "vlan20", Addr: filterCaseIPServer, MAC: filterCaseMacServer},
					},
				},
			},
		},
	}, nil
}

func mdnsUnicastProbeFrame() (ethernet.Frame, error) {
	udpBytes, err := udp.Encode(udp.Header{SrcPort: 40000, DstPort: 5353}, []byte("mdns-probe"), filterCaseIPClient, filterCaseIPServer)
	if err != nil {
		return ethernet.Frame{}, err
	}
	ipBytes, err := ip.Header{
		Src:      filterCaseIPClient,
		Dst:      filterCaseIPServer,
		HopLimit: 64,
		Protocol: 17,
		V4:       &ip.V4{},
	}.Encode(udpBytes)
	if err != nil {
		return ethernet.Frame{}, err
	}
	return ethernet.Frame{
		Dst:       filterCaseMacRouter,
		Src:       filterCaseMacClient,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   ipBytes,
	}, nil
}

func statefulReplyFrame() (ethernet.Frame, error) {
	tcpPayload := make([]byte, 20+len("https-response"))
	binary.BigEndian.PutUint16(tcpPayload[0:2], 443)
	binary.BigEndian.PutUint16(tcpPayload[2:4], 40000)
	tcpPayload[12] = 0x50
	tcpPayload[13] = byte(tcp.ACK)
	copy(tcpPayload[20:], "https-response")

	ipBytes, err := ip.Header{
		Src:      filterCaseIPServer,
		Dst:      filterCaseIPClient,
		HopLimit: 64,
		Protocol: 6,
		V4:       &ip.V4{},
	}.Encode(tcpPayload)
	if err != nil {
		return ethernet.Frame{}, err
	}
	return ethernet.Frame{
		Dst:       filterCaseMacRouter,
		Src:       filterCaseMacServer,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   ipBytes,
	}, nil
}

// CaseTroubleshootingFilterDropsMDNSUnicastProbe tests that an interface-bound filter rule
// matches packet criteria and drops matching traffic with ReasonFilterDrop and Complete status.
func CaseTroubleshootingFilterDropsMDNSUnicastProbe() Case {
	arrivingFrame := expectedFact("bridge.frame",
		`src="02:00:00:00:00:01";dst="02:00:00:00:00:fe";ether_type=2048;tags=[];payload_len=38`)
	vlanDec := expectedFact("bridge.vlan_decision",
		`port="1/1/1";fid=10;pcp=0;dei=false;form="untagged"`)
	matchFact := expectedFact("filter.match",
		`proto=17;src="10.0.10.7";dst="10.0.20.5";src_port=40000;dst_port=5353`)
	ruleDec := expectedFact("filter.rule_decision",
		`set="lan-in";rule="deny-mdns-unicast";action="drop";direction="in";interface="vlan10"`)

	steps := []StepExpectation{
		expectedStep("vlan", trace.OpClassify, "vlan-classify", trace.Subject{Kind: "vlan", Key: "10"},
			[]FactExpectation{arrivingFrame}, []FactExpectation{vlanDec}),
		expectedStep("filter", trace.OpDrop, "filter.drop", trace.Subject{Kind: "interface", Key: "vlan10"},
			[]FactExpectation{matchFact}, []FactExpectation{ruleDec}),
	}

	return Case{
		ID:      "troubleshooting/filter-drops-mdns-unicast-probe",
		UseCase: UseCaseTroubleshooting,
		Question: "When a filter set bound to an ingress interface denies UDP traffic to port 5353, " +
			"does a unicast mDNS probe drop with a definitive filter-drop reason and complete readiness?",
		FalseAnswer: "Silently forwarding the probe, dropping it with an uninformative generic reason, or degrading model readiness",
		CurrentResult: "The frame drops at the filter layer with ReasonFilterDrop and analysis.Complete readiness",
		ExpectedMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		ExpectedOutcome: trace.Dropped,
		ExpectedReason:  filter.ReasonFilterDrop,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("vlan-classify"),
			trace.RuleID("filter.drop"),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "vlan", Key: "10"},
			{Kind: "interface", Key: "vlan10"},
		},
		ExpectedFacts: []FactExpectation{vlanDec, ruleDec},
		ExpectedSteps: steps,
		ExpectedForwardMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		Execute: func() (ExecutionResult, error) {
			cfg, err := filterCaseBaseConfig()
			if err != nil {
				return ExecutionResult{}, err
			}

			protoUDP := uint8(17)
			cfg.Filter = &filter.Config{
				Sets: map[string]filter.RuleSet{
					"lan-in": {
						Stateful: false,
						Default:  filter.Accept,
						Rules: []filter.Rule{
							{
								Name:   "deny-mdns-unicast",
								Action: filter.Drop,
								Match: filter.Match{
									Protocol: &protoUDP,
									Dst:      []netip.Prefix{netip.MustParsePrefix("10.0.20.0/24")},
									DstPorts: []filter.PortRange{{Start: 5353, End: 5353}},
								},
							},
						},
					},
				},
				Bindings: []filter.Binding{
					{Interface: "vlan10", Direction: filter.In, Set: "lan-in"},
				},
			}

			spec := vswitch.ConstructionSpec{
				Config: cfg,
				Seeds: []bridge.Seed{
					{FID: 10, MAC: filterCaseMacClient, Port: "1/1/1", Lifetime: bridge.Static},
					{FID: 20, MAC: filterCaseMacServer, Port: "1/1/2", Lifetime: bridge.Static},
				},
			}

			sw, err := vswitch.NewWithSpec(spec)
			if err != nil {
				return ExecutionResult{}, err
			}

			frame, err := mdnsUnicastProbeFrame()
			if err != nil {
				return ExecutionResult{}, err
			}

			now := time.Unix(1700000000, 0)
			fwd := sw.Forward(now, "1/1/1", frame)

			return ExecutionResult{
				Outcome:  fwd.Outcome,
				Reason:   fwd.Reason,
				Steps:    fwd.Steps,
				Metadata: fwd.Metadata,
				Switch:   sw,
				Forward:  &fwd,
			}, nil
		},
	}
}

// CaseTroubleshootingStatefulReplyAllowed tests that an interface filter with a default-drop policy
// allows return traffic whose reversed 5-tuple was accepted by a stateful counterpart set.
func CaseTroubleshootingStatefulReplyAllowed() Case {
	arrivingFrame := expectedFact("bridge.frame",
		`src="02:00:00:00:00:02";dst="02:00:00:00:00:fe";ether_type=2048;tags=[];payload_len=54`)
	vlanDec := expectedFact("bridge.vlan_decision",
		`port="1/1/2";fid=20;pcp=0;dei=false;form="untagged"`)
	matchFact := expectedFact("filter.match",
		`proto=6;src="10.0.20.5";dst="10.0.10.7";src_port=443;dst_port=40000`)
	fwdDecFact := expectedFact("filter.rule_decision",
		`set="lan-in";rule="allow-https";action="accept";direction="in";interface="vlan10"`)
	stateDecFact := expectedFact("filter.rule_decision",
		`set="srv-in";rule="state";action="accept";direction="in";interface="vlan20"`)

	pktIn := expectedFact("routing.packet_decision",
		`interface="vlan20";ether_type=2048;src="10.0.20.5";dst="10.0.10.7";hop_limit=64;valid=true;reason=""`)
	routeIface := expectedFact("routing.route.interface", "vlan20")
	lookupFact := expectedFact("routing.lookup_decision",
		`vrf="default";destination="10.0.10.7";matched=true;prefix="10.0.10.0/24";next_hop="invalid IP";interface="vlan10";kind="connected";hash_src="10.0.20.5";hash_dst="10.0.10.7";hash_flow_label=0;hash=2387496097;chosen=0;candidates=[invalid IP|vlan10|invalid IP]`)
	neighborFact := expectedFact("routing.neighbor_decision",
		`interface="vlan10";address="10.0.10.7";state="reachable";mac="02:00:00:00:00:01";origin="configured"`)
	pktOut := expectedFact("routing.packet_decision",
		`interface="vlan10";ether_type=2048;src="10.0.20.5";dst="10.0.10.7";hop_limit=63;valid=true;reason=""`)
	routedFrame := expectedFact("bridge.frame",
		`src="02:00:00:00:00:fe";dst="02:00:00:00:00:01";ether_type=2048;tags=[];payload_len=54`)
	fdbDec := expectedFact("bridge.fdb_decision",
		`fid=10;mac="02:00:00:00:00:01";present=true;port="1/1/1";static=true`)
	egressVlanDec := expectedFact("bridge.vlan_decision",
		`port="1/1/1";fid=10;pcp=0;dei=false;form="egress"`)
	egressTransmit := expectedFact("bridge.egress_decision",
		`port="1/1/1";member="";fid=10;eligible=true;reason=""`)

	steps := []StepExpectation{
		expectedStep("vlan", trace.OpClassify, "vlan-classify", trace.Subject{Kind: "vlan", Key: "20"},
			[]FactExpectation{arrivingFrame}, []FactExpectation{vlanDec}),
		expectedStep("filter", trace.OpFilter, "filter.state", trace.Subject{Kind: "interface", Key: "vlan20"},
			[]FactExpectation{matchFact, fwdDecFact}, []FactExpectation{stateDecFact}),
		expectedStep(port.LayerRouting, trace.OpClassify, "classify", trace.Subject{Kind: "interface", Key: "vlan20"},
			[]FactExpectation{pktIn}, []FactExpectation{routeIface}),
		expectedStep(port.LayerRouting, trace.OpLookup, "connected", trace.Subject{Kind: "prefix", Key: "10.0.10.0/24"},
			[]FactExpectation{pktIn}, []FactExpectation{lookupFact}),
		expectedStep(port.LayerRouting, trace.OpRewrite, "decrement-ttl", trace.Subject{Kind: "interface", Key: "vlan10"},
			[]FactExpectation{neighborFact, pktIn}, []FactExpectation{pktOut}),
		expectedStep("relay", trace.OpLookup, "unicast-hit", trace.Subject{Kind: "mac", Key: "02:00:00:00:00:01"},
			[]FactExpectation{routedFrame}, []FactExpectation{fdbDec}),
		expectedStep("vlan", trace.OpRewrite, "vlan-tag-form", trace.Subject{Kind: "port", Key: "1/1/1"},
			[]FactExpectation{routedFrame}, []FactExpectation{routedFrame, egressVlanDec}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "1/1/1"},
			[]FactExpectation{routedFrame}, []FactExpectation{egressTransmit}),
	}

	return Case{
		ID:      "troubleshooting/stateful-reply-allowed",
		UseCase: UseCaseTroubleshooting,
		Question: "When an ingress interface filter set default-drops traffic, does it forward a " +
			"return packet whose reversed 5-tuple was accepted by a stateful counterpart set?",
		FalseAnswer: "Dropping the return packet because the local interface set has no matching accept rule, or requiring an explicit return rule",
		CurrentResult: "The return frame forwards with a filter.state step referencing the counterpart rule",
		ExpectedMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		ExpectedOutcome: trace.Forwarded,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("vlan-classify"),
			trace.RuleID("filter.state"),
			trace.RuleID("classify"),
			trace.RuleID("connected"),
			trace.RuleID("decrement-ttl"),
			trace.RuleID("unicast-hit"),
			trace.RuleID("vlan-tag-form"),
			trace.RuleID("transmit"),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "vlan", Key: "20"},
			{Kind: "interface", Key: "vlan20"},
			{Kind: "prefix", Key: "10.0.10.0/24"},
			{Kind: "interface", Key: "vlan10"},
			{Kind: "mac", Key: "02:00:00:00:00:01"},
			{Kind: "port", Key: "1/1/1"},
		},
		ExpectedFacts: []FactExpectation{vlanDec, stateDecFact, lookupFact, fdbDec},
		ExpectedSteps: steps,
		ExpectedForwardMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		Execute: func() (ExecutionResult, error) {
			cfg, err := filterCaseBaseConfig()
			if err != nil {
				return ExecutionResult{}, err
			}

			protoTCP := uint8(6)
			cfg.Filter = &filter.Config{
				Sets: map[string]filter.RuleSet{
					"lan-in": {
						Stateful: true,
						Default:  filter.Drop,
						Rules: []filter.Rule{
							{
								Name:   "allow-https",
								Action: filter.Accept,
								Match: filter.Match{
									Protocol: &protoTCP,
									Src:      []netip.Prefix{netip.MustParsePrefix("10.0.10.0/24")},
									Dst:      []netip.Prefix{netip.MustParsePrefix("10.0.20.5/32")},
									DstPorts: []filter.PortRange{{Start: 443, End: 443}},
								},
							},
						},
					},
					"srv-in": {
						Stateful: true,
						Default:  filter.Drop,
						Rules:    nil,
					},
				},
				Bindings: []filter.Binding{
					{Interface: "vlan10", Direction: filter.In, Set: "lan-in"},
					{Interface: "vlan20", Direction: filter.In, Set: "srv-in"},
				},
			}

			spec := vswitch.ConstructionSpec{
				Config: cfg,
				Seeds: []bridge.Seed{
					{FID: 10, MAC: filterCaseMacClient, Port: "1/1/1", Lifetime: bridge.Static},
					{FID: 20, MAC: filterCaseMacServer, Port: "1/1/2", Lifetime: bridge.Static},
				},
			}

			sw, err := vswitch.NewWithSpec(spec)
			if err != nil {
				return ExecutionResult{}, err
			}

			frame, err := statefulReplyFrame()
			if err != nil {
				return ExecutionResult{}, err
			}

			now := time.Unix(1700000000, 0)
			fwd := sw.Forward(now, "1/1/2", frame)

			return ExecutionResult{
				Outcome:  fwd.Outcome,
				Reason:   fwd.Reason,
				Steps:    fwd.Steps,
				Metadata: fwd.Metadata,
				Switch:   sw,
				Forward:  &fwd,
			}, nil
		},
	}
}

// CasePlanningFilterRuleChange evaluates a prospective filter rule addition to an ingress
// interface, asserting typed diff changes and a flipped forwarding outcome under Compare.
func CasePlanningFilterRuleChange() Case {
	arrivingFrame := expectedFact("bridge.frame",
		`src="02:00:00:00:00:01";dst="02:00:00:00:00:fe";ether_type=2048;tags=[];payload_len=38`)
	vlanDec := expectedFact("bridge.vlan_decision",
		`port="1/1/1";fid=10;pcp=0;dei=false;form="untagged"`)
	matchFact := expectedFact("filter.match",
		`proto=17;src="10.0.10.7";dst="10.0.20.5";src_port=40000;dst_port=5353`)
	defaultDec := expectedFact("filter.rule_decision",
		`set="lan-in";rule="default";action="accept";direction="in";interface="vlan10"`)
	denyRuleDec := expectedFact("filter.rule_decision",
		`set="lan-in";rule="deny-mdns-unicast";action="drop";direction="in";interface="vlan10"`)

	pktIn := expectedFact("routing.packet_decision",
		`interface="vlan10";ether_type=2048;src="10.0.10.7";dst="10.0.20.5";hop_limit=64;valid=true;reason=""`)
	routeIface := expectedFact("routing.route.interface", "vlan10")
	lookupFact := expectedFact("routing.lookup_decision",
		`vrf="default";destination="10.0.20.5";matched=true;prefix="10.0.20.0/24";next_hop="invalid IP";interface="vlan20";kind="connected";hash_src="10.0.10.7";hash_dst="10.0.20.5";hash_flow_label=0;hash=1544294637;chosen=0;candidates=[invalid IP|vlan20|invalid IP]`)
	neighborFact := expectedFact("routing.neighbor_decision",
		`interface="vlan20";address="10.0.20.5";state="reachable";mac="02:00:00:00:00:02";origin="configured"`)
	pktOut := expectedFact("routing.packet_decision",
		`interface="vlan20";ether_type=2048;src="10.0.10.7";dst="10.0.20.5";hop_limit=63;valid=true;reason=""`)
	routedFrame := expectedFact("bridge.frame",
		`src="02:00:00:00:00:fe";dst="02:00:00:00:00:02";ether_type=2048;tags=[];payload_len=38`)
	egressVlanDec := expectedFact("bridge.vlan_decision",
		`port="1/1/2";fid=20;pcp=0;dei=false;form="egress"`)
	egressTransmit := expectedFact("bridge.egress_decision",
		`port="1/1/2";member="";fid=20;eligible=true;reason=""`)

	currentSteps := []StepExpectation{
		expectedStep("vlan", trace.OpClassify, "vlan-classify", trace.Subject{Kind: "vlan", Key: "10"},
			[]FactExpectation{arrivingFrame}, []FactExpectation{vlanDec}),
		expectedStep("filter", trace.OpFilter, "filter.default", trace.Subject{Kind: "interface", Key: "vlan10"},
			[]FactExpectation{matchFact}, []FactExpectation{defaultDec}),
		expectedStep(port.LayerRouting, trace.OpClassify, "classify", trace.Subject{Kind: "interface", Key: "vlan10"},
			[]FactExpectation{pktIn}, []FactExpectation{routeIface}),
		expectedStep(port.LayerRouting, trace.OpLookup, "connected", trace.Subject{Kind: "prefix", Key: "10.0.20.0/24"},
			[]FactExpectation{pktIn}, []FactExpectation{lookupFact}),
		expectedStep(port.LayerRouting, trace.OpRewrite, "decrement-ttl", trace.Subject{Kind: "interface", Key: "vlan20"},
			[]FactExpectation{neighborFact, pktIn}, []FactExpectation{pktOut}),
		expectedStep("relay", trace.OpLookup, "unicast-hit", trace.Subject{Kind: "mac", Key: "02:00:00:00:00:02"},
			[]FactExpectation{routedFrame}, []FactExpectation{expectedFact("bridge.fdb_decision", `fid=20;mac="02:00:00:00:00:02";present=true;port="1/1/2";static=true`)}),
		expectedStep("vlan", trace.OpRewrite, "vlan-tag-form", trace.Subject{Kind: "port", Key: "1/1/2"},
			[]FactExpectation{routedFrame}, []FactExpectation{routedFrame, egressVlanDec}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "1/1/2"},
			[]FactExpectation{routedFrame}, []FactExpectation{egressTransmit}),
	}

	expectedSteps := []StepExpectation{
		expectedStep("vlan", trace.OpClassify, "vlan-classify", trace.Subject{Kind: "vlan", Key: "10"},
			[]FactExpectation{arrivingFrame}, []FactExpectation{vlanDec}),
		expectedStep("filter", trace.OpDrop, "filter.drop", trace.Subject{Kind: "interface", Key: "vlan10"},
			[]FactExpectation{matchFact}, []FactExpectation{denyRuleDec}),
	}

	protoUDP := uint8(17)
	denyRule := filter.Rule{
		Name:   "deny-mdns-unicast",
		Action: filter.Drop,
		Match: filter.Match{
			Protocol: &protoUDP,
			Dst:      []netip.Prefix{netip.MustParsePrefix("10.0.20.0/24")},
			DstPorts: []filter.PortRange{{Start: 5353, End: 5353}},
		},
	}
	ruleSnap := NewFactExpectation(filter.SnapshotRule(denyRule))
	expectedChanges := []ChangeExpectation{
		expectedChange("filter", trace.Subject{Kind: "rule", Key: "lan-in/deny-mdns-unicast"}, "", nil, &ruleSnap),
	}

	return Case{
		ID:      "planning/filter-rule-change",
		UseCase: UseCasePlanning,
		Question: "Does adding a filter rule that drops unicast mDNS alter forwarding behavior " +
			"from forwarded to dropped and emit a typed filter rule diff change?",
		FalseAnswer: "Silently ignoring the rule addition, failing to emit typed configuration diff facts, or reporting equivalent forwarding",
		CurrentResult: "vswitch.Diff produces a typed filter.rule change fact and vswitch.Compare detects forwarding divergence from forwarded to dropped",
		ExpectedMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		ExpectedOutcome: trace.Dropped,
		ExpectedReason:  filter.ReasonFilterDrop,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("vlan-classify"),
			trace.RuleID("filter.drop"),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "vlan", Key: "10"},
			{Kind: "interface", Key: "vlan10"},
		},
		ExpectedFacts: []FactExpectation{
			vlanDec,
			denyRuleDec,
			ruleSnap,
		},
		ExpectedSteps:   expectedSteps,
		ExpectedChanges: expectedChanges,
		ExpectedComparison: &ComparisonExpectation{
			Current: ForwardExpectation{
				Outcome: trace.Forwarded,
				Metadata: MetadataExpectation{
					Status: analysis.Complete,
					Scope:  analysis.NodeScope(""),
				},
				Steps: currentSteps,
			},
			Expected: ForwardExpectation{
				Outcome: trace.Dropped,
				Reason:  filter.ReasonFilterDrop,
				Metadata: MetadataExpectation{
					Status: analysis.Complete,
					Scope:  analysis.NodeScope(""),
				},
				Steps: cloneStepExpectations(expectedSteps),
			},
			Same: false,
		},
		Execute: func() (ExecutionResult, error) {
			cfgCur, err := filterCaseBaseConfig()
			if err != nil {
				return ExecutionResult{}, err
			}

			cfgCur.Filter = &filter.Config{
				Sets: map[string]filter.RuleSet{
					"lan-in": {
						Stateful: false,
						Default:  filter.Accept,
						Rules:    nil,
					},
				},
				Bindings: []filter.Binding{
					{Interface: "vlan10", Direction: filter.In, Set: "lan-in"},
				},
			}

			specCur := vswitch.ConstructionSpec{
				Config: cfgCur,
				Seeds: []bridge.Seed{
					{FID: 10, MAC: filterCaseMacClient, Port: "1/1/1", Lifetime: bridge.Static},
					{FID: 20, MAC: filterCaseMacServer, Port: "1/1/2", Lifetime: bridge.Static},
				},
			}

			swCur, err := vswitch.NewWithSpec(specCur)
			if err != nil {
				return ExecutionResult{}, err
			}

			cfgNext := cfgCur.Clone()
			set := cfgNext.Filter.Sets["lan-in"]
			set.Rules = []filter.Rule{denyRule}
			cfgNext.Filter.Sets["lan-in"] = set

			changes := vswitch.Diff(cfgCur, cfgNext)

			swNext, err := vswitch.Derive(swCur, vswitch.ConstructionSpec{Config: cfgNext})
			if err != nil {
				return ExecutionResult{}, err
			}

			frame, err := mdnsUnicastProbeFrame()
			if err != nil {
				return ExecutionResult{}, err
			}

			now := time.Unix(1700000000, 0)
			cmp := vswitch.Compare(swCur, swNext, now, "1/1/1", frame)

			return ExecutionResult{
				Outcome:    cmp.Expected.Outcome,
				Reason:     cmp.Expected.Reason,
				Steps:      cmp.Expected.Steps,
				Changes:    changes,
				Metadata:   cmp.Expected.Metadata,
				Switch:     swNext,
				Comparison: &cmp,
			}, nil
		},
	}
}
