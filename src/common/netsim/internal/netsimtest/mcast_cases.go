package netsimtest

import (
	"net/netip"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/mcast"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// mdnsIPv4Datagram is a minimal mDNS PTR query for _services._dns-sd._udp.local
// (RFC 6762), port 5353 to port 5353, as raw UDP datagram bytes.
var mdnsIPv4Datagram = []byte{
	0x14, 0xe9, 0x14, 0xe9, 0x00, 0x36, 0xaa, 0x94, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x09, 0x5f, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x07, 0x5f,
	0x64, 0x6e, 0x73, 0x2d, 0x73, 0x64, 0x04, 0x5f, 0x75, 0x64, 0x70, 0x05, 0x6c, 0x6f, 0x63, 0x61,
	0x6c, 0x00, 0x00, 0x0c, 0x00, 0x01,
}

// mdnsIPv6Datagram is the same mDNS PTR query as [mdnsIPv4Datagram], carrying
// the UDP checksum recomputed over an IPv6 pseudo-header (RFC 8200 section 8.1).
var mdnsIPv6Datagram = []byte{
	0x14, 0xe9, 0x14, 0xe9, 0x00, 0x36, 0xa1, 0x17, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x09, 0x5f, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x73, 0x07, 0x5f,
	0x64, 0x6e, 0x73, 0x2d, 0x73, 0x64, 0x04, 0x5f, 0x75, 0x64, 0x70, 0x05, 0x6c, 0x6f, 0x63, 0x61,
	0x6c, 0x00, 0x00, 0x0c, 0x00, 0x01,
}

// mdnsCaseSwitchPorts builds the nine administratively up physical ports
// (p1..p9) shared by the two mDNS conformance cases. VLAN membership and the
// multicast router port are configured separately, in
// [mdnsCaseVLANSwitchports] and each case's own multicast config.
func mdnsCaseSwitchPorts() (port.Table, error) {
	builder := port.NewBuilder()
	for _, name := range []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8", "p9"} {
		builder.Add(port.Port{Name: name, Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	}

	return builder.Build()
}

func mdnsCaseVLANSwitchports() map[string]bridge.Switchport {
	pvid := vlan.ID(10)
	switchports := make(map[string]bridge.Switchport, 9)
	for _, name := range []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8", "p9"} {
		switchports[name] = bridge.Switchport{PVID: &pvid, Untagged: []vlan.ID{10}}
	}

	return switchports
}

// CaseTroubleshootingMDNSIPv4FloodsUnderSnooping returns the case evaluating
// whether a snooping switch treats IPv4 mDNS, addressed to a group in
// 224.0.0.0/24, as an unregistered multicast group to drop, rather than a
// reserved local-network control range that always floods (RFC 4541
// section 2.1.2).
func CaseTroubleshootingMDNSIPv4FloodsUnderSnooping() Case {
	hostMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	groupMAC := netaddr.MAC{0x01, 0x00, 0x5e, 0x00, 0x00, 0xfb}
	frame := expectedFact("bridge.frame", `src="00:11:22:33:44:01";dst="01:00:5e:00:00:fb";ether_type=2048;tags=[];payload_len=74`)
	fdbLearned := expectedFact("bridge.fdb_decision", `fid=10;mac="00:11:22:33:44:01";present=true;port="p1";static=false`)
	groupDestination := expectedFact("bridge.egress_decision", `port="";member="";fid=10;eligible=true;reason="group-destination"`)

	expectedSteps := []StepExpectation{
		expectedStep("vlan", trace.OpClassify, "vlan-classify", trace.Subject{Kind: "vlan", Key: "10"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="p1";fid=10;pcp=0;dei=false;form="untagged"`)}),
		expectedStep("relay", trace.OpLearn, "learn", trace.Subject{Kind: "mac", Key: "00:11:22:33:44:01"},
			nil, []FactExpectation{fdbLearned}),
		expectedStep("relay", trace.OpLookup, "group-destination", trace.Subject{Kind: "mac", Key: "01:00:5e:00:00:fb"},
			[]FactExpectation{frame}, []FactExpectation{groupDestination}),
		expectedStep("relay", trace.OpReplicate, "flood", trace.Subject{Kind: "vlan", Key: "10"},
			[]FactExpectation{frame},
			[]FactExpectation{
				expectedFact("bridge.egress_decision", `port="p2";member="";fid=10;eligible=true;reason=""`),
				expectedFact("bridge.egress_decision", `port="p3";member="";fid=10;eligible=true;reason=""`),
				expectedFact("bridge.egress_decision", `port="p4";member="";fid=10;eligible=true;reason=""`),
				expectedFact("bridge.egress_decision", `port="p5";member="";fid=10;eligible=true;reason=""`),
				expectedFact("bridge.egress_decision", `port="p6";member="";fid=10;eligible=true;reason=""`),
				expectedFact("bridge.egress_decision", `port="p7";member="";fid=10;eligible=true;reason=""`),
				expectedFact("bridge.egress_decision", `port="p8";member="";fid=10;eligible=true;reason=""`),
				expectedFact("bridge.egress_decision", `port="p9";member="";fid=10;eligible=true;reason=""`),
			}),
		expectedStep("vlan", trace.OpRewrite, "vlan-tag-form", trace.Subject{Kind: "port", Key: "p2"},
			[]FactExpectation{frame},
			[]FactExpectation{frame, expectedFact("bridge.vlan_decision", `port="p2";fid=10;pcp=0;dei=false;form="egress"`)}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "p2"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="p2";member="";fid=10;eligible=true;reason=""`)}),
		expectedStep("vlan", trace.OpRewrite, "vlan-tag-form", trace.Subject{Kind: "port", Key: "p3"},
			[]FactExpectation{frame},
			[]FactExpectation{frame, expectedFact("bridge.vlan_decision", `port="p3";fid=10;pcp=0;dei=false;form="egress"`)}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "p3"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="p3";member="";fid=10;eligible=true;reason=""`)}),
		expectedStep("vlan", trace.OpRewrite, "vlan-tag-form", trace.Subject{Kind: "port", Key: "p4"},
			[]FactExpectation{frame},
			[]FactExpectation{frame, expectedFact("bridge.vlan_decision", `port="p4";fid=10;pcp=0;dei=false;form="egress"`)}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "p4"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="p4";member="";fid=10;eligible=true;reason=""`)}),
		expectedStep("vlan", trace.OpRewrite, "vlan-tag-form", trace.Subject{Kind: "port", Key: "p5"},
			[]FactExpectation{frame},
			[]FactExpectation{frame, expectedFact("bridge.vlan_decision", `port="p5";fid=10;pcp=0;dei=false;form="egress"`)}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "p5"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="p5";member="";fid=10;eligible=true;reason=""`)}),
		expectedStep("vlan", trace.OpRewrite, "vlan-tag-form", trace.Subject{Kind: "port", Key: "p6"},
			[]FactExpectation{frame},
			[]FactExpectation{frame, expectedFact("bridge.vlan_decision", `port="p6";fid=10;pcp=0;dei=false;form="egress"`)}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "p6"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="p6";member="";fid=10;eligible=true;reason=""`)}),
		expectedStep("vlan", trace.OpRewrite, "vlan-tag-form", trace.Subject{Kind: "port", Key: "p7"},
			[]FactExpectation{frame},
			[]FactExpectation{frame, expectedFact("bridge.vlan_decision", `port="p7";fid=10;pcp=0;dei=false;form="egress"`)}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "p7"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="p7";member="";fid=10;eligible=true;reason=""`)}),
		expectedStep("vlan", trace.OpRewrite, "vlan-tag-form", trace.Subject{Kind: "port", Key: "p8"},
			[]FactExpectation{frame},
			[]FactExpectation{frame, expectedFact("bridge.vlan_decision", `port="p8";fid=10;pcp=0;dei=false;form="egress"`)}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "p8"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="p8";member="";fid=10;eligible=true;reason=""`)}),
		expectedStep("vlan", trace.OpRewrite, "vlan-tag-form", trace.Subject{Kind: "port", Key: "p9"},
			[]FactExpectation{frame},
			[]FactExpectation{frame, expectedFact("bridge.vlan_decision", `port="p9";fid=10;pcp=0;dei=false;form="egress"`)}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "p9"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="p9";member="";fid=10;eligible=true;reason=""`)}),
	}

	return Case{
		ID:            "troubleshooting/mdns-ipv4-floods-under-snooping",
		UseCase:       UseCaseTroubleshooting,
		Question:      "Does IGMP snooping drop an mDNS query addressed to 224.0.0.251 because no port has joined it?",
		FalseAnswer:   "snooping drops unregistered mDNS",
		CurrentResult: "the frame's destination sits in 224.0.0.0/24, which Resolve exempts from admission entirely, so it floods every port but the ingress port regardless of membership",
		ExpectedMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		ExpectedOutcome: trace.Flooded,
		ExpectedReason:  "",
		ExpectedRules: []trace.RuleID{
			trace.RuleID("vlan-classify"),
			trace.RuleID("learn"),
			trace.RuleID("group-destination"),
			trace.RuleID("flood"),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "vlan", Key: "10"},
			{Kind: "mac", Key: "00:11:22:33:44:01"},
			{Kind: "mac", Key: "01:00:5e:00:00:fb"},
		},
		ExpectedFacts: []FactExpectation{
			groupDestination,
		},
		ExpectedSteps: expectedSteps,
		ExpectedForwardMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		Execute: func() (ExecutionResult, error) {
			hdr := ip.Header{
				Src:      netip.MustParseAddr("10.0.10.7"),
				Dst:      netip.MustParseAddr("224.0.0.251"),
				HopLimit: 255,
				Protocol: 17,
				V4:       &ip.V4{},
			}

			return mdnsCaseExecute(hostMAC, groupMAC, ethernet.EtherTypeIPv4, hdr, mdnsIPv4Datagram)
		},
	}
}

// CaseTroubleshootingMDNSIPv6UnregisteredRouterPorts returns the case
// evaluating whether a snooping switch floods an IPv6 mDNS query addressed
// to a link-scope group with no member, rather than restricting it to the
// configured router ports (RFC 4541 section 3).
func CaseTroubleshootingMDNSIPv6UnregisteredRouterPorts() Case {
	hostMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}
	groupMAC := netaddr.MAC{0x33, 0x33, 0x00, 0x00, 0x00, 0xfb}
	frame := expectedFact("bridge.frame", `src="00:11:22:33:44:02";dst="33:33:00:00:00:fb";ether_type=34525;tags=[];payload_len=94`)
	fdbLearned := expectedFact("bridge.fdb_decision", `fid=10;mac="00:11:22:33:44:02";present=true;port="p1";static=false`)
	groupDestination := expectedFact("bridge.egress_decision", `port="";member="";fid=10;eligible=true;reason="group-destination"`)
	membership := expectedFact("vswitch.mcast_membership", `fid=10;group="ff02::fb";source="fe80::1";registered=false;decided=true;ports=["p9"]`)

	expectedSteps := []StepExpectation{
		expectedStep("vlan", trace.OpClassify, "vlan-classify", trace.Subject{Kind: "vlan", Key: "10"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="p1";fid=10;pcp=0;dei=false;form="untagged"`)}),
		expectedStep("relay", trace.OpLearn, "learn", trace.Subject{Kind: "mac", Key: "00:11:22:33:44:02"},
			nil, []FactExpectation{fdbLearned}),
		expectedStep("relay", trace.OpLookup, "group-destination", trace.Subject{Kind: "mac", Key: "33:33:00:00:00:fb"},
			[]FactExpectation{frame}, []FactExpectation{groupDestination}),
		expectedStep("mcast", trace.OpReplicate, "group-members", trace.Subject{Kind: "vlan", Key: "10"},
			[]FactExpectation{frame, membership},
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="p9";member="";fid=10;eligible=true;reason=""`)}),
		expectedStep("vlan", trace.OpRewrite, "vlan-tag-form", trace.Subject{Kind: "port", Key: "p9"},
			[]FactExpectation{frame},
			[]FactExpectation{frame, expectedFact("bridge.vlan_decision", `port="p9";fid=10;pcp=0;dei=false;form="egress"`)}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "p9"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="p9";member="";fid=10;eligible=true;reason=""`)}),
	}

	return Case{
		ID:            "troubleshooting/mdns-ipv6-unregistered-router-ports",
		UseCase:       UseCaseTroubleshooting,
		Question:      "Does IPv6 mDNS with no member registered on ff02::fb flood every port like the IPv4 case, or reach only the router ports?",
		FalseAnswer:   "the switch floods link-scope groups like IPv4",
		CurrentResult: "no port has joined ff02::fb, so Resolve admits only the static router port p9 and the frame reaches p9 alone",
		ExpectedMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		ExpectedOutcome: trace.Flooded,
		ExpectedReason:  "",
		ExpectedRules: []trace.RuleID{
			trace.RuleID("vlan-classify"),
			trace.RuleID("learn"),
			trace.RuleID("group-destination"),
			trace.RuleID("group-members"),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "vlan", Key: "10"},
			{Kind: "mac", Key: "00:11:22:33:44:02"},
			{Kind: "mac", Key: "33:33:00:00:00:fb"},
		},
		ExpectedFacts: []FactExpectation{
			membership,
		},
		ExpectedSteps: expectedSteps,
		ExpectedForwardMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		Execute: func() (ExecutionResult, error) {
			hdr := ip.Header{
				Src:      netip.MustParseAddr("fe80::1"),
				Dst:      netip.MustParseAddr("ff02::fb"),
				HopLimit: 255,
				Protocol: 17,
				V6:       &ip.V6{},
			}

			return mdnsCaseExecute(hostMAC, groupMAC, ethernet.EtherTypeIPv6, hdr, mdnsIPv6Datagram)
		},
	}
}

// mdnsCaseExecute builds the shared nine-port switch and forwards a single
// frame carrying an mDNS datagram from p1, for the mDNS conformance cases.
func mdnsCaseExecute(hostMAC, groupMAC netaddr.MAC, etherType ethernet.EtherType, hdr ip.Header, datagram []byte) (ExecutionResult, error) {
	ports, err := mdnsCaseSwitchPorts()
	if err != nil {
		return ExecutionResult{}, err
	}

	flood := false
	spec := vswitch.ConstructionSpec{
		Config: vswitch.Config{
			Ports: ports,
			Bridge: &bridge.Config{VLAN: &bridge.VLAN{
				Table:       map[vlan.ID]string{10: "ten"},
				Switchports: mdnsCaseVLANSwitchports(),
			}},
			Mcast: &mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
				10: {FloodUnregistered: &flood, RouterPorts: []string{"p9"}},
			}},
		},
	}

	sw, err := vswitch.NewWithSpec(spec)
	if err != nil {
		return ExecutionResult{}, err
	}

	pkt, err := hdr.Encode(datagram)
	if err != nil {
		return ExecutionResult{}, err
	}

	mdnsFrame := ethernet.Frame{Dst: groupMAC, Src: hostMAC, EtherType: etherType, Payload: pkt}

	now := time.Unix(1700000000, 0)
	fwd := sw.Forward(now, "p1", mdnsFrame)

	return ExecutionResult{
		Outcome:  fwd.Outcome,
		Reason:   fwd.Reason,
		Steps:    fwd.Steps,
		Metadata: fwd.Metadata,
		Switch:   sw,
		Forward:  &fwd,
	}, nil
}

// RegisterMDNSCases populates registry with the cases covering how IGMP and
// MLD snooping treat mDNS's reserved IPv4 control range and its unregistered
// IPv6 link-scope group.
func RegisterMDNSCases(registry *Registry) {
	registry.MustRegister(CaseTroubleshootingMDNSIPv4FloodsUnderSnooping())
	registry.MustRegister(CaseTroubleshootingMDNSIPv6UnregisteredRouterPorts())
}
