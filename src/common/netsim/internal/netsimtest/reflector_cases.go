package netsimtest

import (
	"fmt"
	"net/netip"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/udp"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/mcast"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// reflectorGroupMAC and reflectorGroupAddr are the IPv4 mDNS group's
// Ethernet and IP addresses (RFC 6762 section 3), shared by both cases in
// this file so their injected queries and reflected copies agree on what
// they are chasing.
var (
	reflectorGroupMAC  = netaddr.MAC{0x01, 0x00, 0x5e, 0x00, 0x00, 0xfb}
	reflectorGroupAddr = netip.MustParseAddr("224.0.0.251")
)

// mdnsQueryFrame builds the Ethernet frame an mDNS querier sends: a UDP
// datagram from port 5353 to port 5353, addressed to the IPv4 mDNS group,
// carrying payload as its body. The frame is untagged; a host origin's own
// VLAN tags it on injection, and a reflector attachment's own tag form
// applies once it crosses a cable.
func mdnsQueryFrame(src netaddr.MAC, srcAddr netip.Addr, payload []byte) (ethernet.Frame, error) {
	udpBytes, err := udp.Encode(udp.Header{SrcPort: 5353, DstPort: 5353}, payload, srcAddr, reflectorGroupAddr)
	if err != nil {
		return ethernet.Frame{}, err
	}
	ipBytes, err := ip.Header{Src: srcAddr, Dst: reflectorGroupAddr, HopLimit: 1, Protocol: 17, V4: &ip.V4{}}.Encode(udpBytes)
	if err != nil {
		return ethernet.Frame{}, err
	}

	return ethernet.Frame{Dst: reflectorGroupMAC, Src: src, EtherType: ethernet.EtherTypeIPv4, Payload: ipBytes}, nil
}

var (
	reflectedQueryH1MAC = netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x04, 0x01}
	reflectedQueryH2MAC = netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x04, 0x02}
	reflectedQueryR1MAC = netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x04, 0xaa}
)

// reflectedQueryConfig cables h1 on VLAN 10 and h2 on VLAN 20 to sw1, and
// sw1's third port to r1's single trunk port, carrying both VLANs tagged.
// sw1 snoops both VLANs with FloodUnregistered off, so the case also proves
// the reflector's query still crosses it: 224.0.0.251 sits in the
// 224.0.0.0/24 range RFC 4541 section 2.1.2 exempts from snooping admission,
// so the switch floods it exactly as it would with snooping off, per
// [CaseTroubleshootingMDNSIPv4FloodsUnderSnooping]'s sibling proof at the
// switch layer alone. r1's two attachments carry the only addresses of h2's
// family, so accepting on one produces exactly one copy.
func reflectedQueryConfig() (fabric.Config, error) {
	vid10, vid20 := vlan.ID(10), vlan.ID(20)
	gigabit := gigabitAuto()
	flood := false

	tbl, err := port.NewBuilder().
		Add(port.Port{Name: "p1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "p2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "p9", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
	if err != nil {
		return fabric.Config{}, err
	}

	return fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports: tbl,
				Bridge: &bridge.Config{VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "ten", 20: "twenty"},
					Switchports: map[string]bridge.Switchport{
						"p1": {PVID: &vid10, Untagged: []vlan.ID{10}},
						"p2": {PVID: &vid20, Untagged: []vlan.ID{20}},
						"p9": {Tagged: []vlan.ID{10, 20}},
					},
				}},
				Mcast: &mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
					10: {FloodUnregistered: &flood},
					20: {FloodUnregistered: &flood},
				}},
				Phy: &phy.Config{Ethernet: map[string]phy.Ethernet{"p1": gigabit, "p2": gigabit, "p9": gigabit}},
			},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: reflectedQueryH1MAC, Ethernet: gigabit},
			"h2": {
				Address: reflectedQueryH2MAC, Ethernet: gigabit,
				IP:     &fabric.HostIP{Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.20.5/24")}},
				Accept: fabric.HostAccept{Multicast: []netaddr.MAC{reflectorGroupMAC}},
			},
		},
		Reflectors: map[string]fabric.Reflector{
			"r1": {
				Address: reflectedQueryR1MAC,
				Ports:   map[string]phy.Ethernet{"trunk": gigabit},
				Attachments: map[string]fabric.Attachment{
					"v10": {Port: "trunk", VLAN: &vid10, Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.9/24")}},
					"v20": {Port: "trunk", VLAN: &vid20, Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.20.9/24")}},
				},
			},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "p1"}, Medium: fabric.TwistedPair},
			{A: fabric.Endpoint{Node: "h2"}, B: fabric.Endpoint{Node: "sw1", Port: "p2"}, Medium: fabric.TwistedPair},
			{A: fabric.Endpoint{Node: "sw1", Port: "p9"}, B: fabric.Endpoint{Node: "r1", Port: "trunk"}, Medium: fabric.TwistedPair},
		},
	}, nil
}

// CaseTroubleshootingMDNSReflectedAcrossVLANs returns the case whose journey
// is the reflected copy r1 originates onto VLAN 20: h1 queries mDNS on
// VLAN 10, sw1 floods it to r1's trunk despite snooping being locked down,
// r1 accepts and originates one copy addressed from its VLAN 20 attachment,
// and h2 decodes it and takes delivery. It pins the copy's journey, not the
// injected query's: [ExecutionResult.Journey] is a single journey, and the
// query's own journey ends at r1's acceptance with no delivery to assert.
func CaseTroubleshootingMDNSReflectedAcrossVLANs() Case {
	arrivingFrame := expectedFact("bridge.frame",
		`src="02:00:00:00:04:aa";dst="01:00:5e:00:00:fb";ether_type=2048;tags=[{tpid=33024;pcp=0;dei=false;vid=20}];payload_len=38`)
	strippedFrame := expectedFact("bridge.frame",
		`src="02:00:00:00:04:aa";dst="01:00:5e:00:00:fb";ether_type=2048;tags=[];payload_len=38`)
	groupDestination := expectedFact("bridge.egress_decision", `port="";member="";fid=20;eligible=true;reason="group-destination"`)
	floodToH2 := expectedFact("bridge.egress_decision", `port="p2";member="";fid=20;eligible=true;reason=""`)
	macFact := expectedFact("fabric.mac", "01:00:5e:00:00:fb")
	addrFact := expectedFact("routing.addr", "224.0.0.251")

	steps := []StepExpectation{
		expectedStep("vlan", trace.OpClassify, "vlan-classify", trace.Subject{Kind: "vlan", Key: "20"},
			[]FactExpectation{arrivingFrame},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="p9";fid=20;pcp=0;dei=false;form="tagged"`)}),
		expectedStep("relay", trace.OpLearn, "learn", trace.Subject{Kind: "mac", Key: "02:00:00:00:04:aa"}, nil,
			[]FactExpectation{expectedFact("bridge.fdb_decision", `fid=20;mac="02:00:00:00:04:aa";present=true;port="p9";static=false`)}),
		expectedStep("relay", trace.OpLookup, "group-destination", trace.Subject{Kind: "mac", Key: "01:00:5e:00:00:fb"},
			[]FactExpectation{arrivingFrame}, []FactExpectation{groupDestination}),
		expectedStep("relay", trace.OpReplicate, "flood", trace.Subject{Kind: "vlan", Key: "20"},
			[]FactExpectation{arrivingFrame}, []FactExpectation{floodToH2}),
		expectedStep("vlan", trace.OpRewrite, "vlan-tag-form", trace.Subject{Kind: "port", Key: "p2"},
			[]FactExpectation{arrivingFrame},
			[]FactExpectation{strippedFrame, expectedFact("bridge.vlan_decision", `port="p2";fid=20;pcp=0;dei=false;form="egress"`)}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "p2"},
			[]FactExpectation{strippedFrame}, []FactExpectation{floodToH2}),
		expectedStep("host", trace.OpFilter, "host.ip.group", trace.Subject{Kind: "host", Key: "h2"},
			[]FactExpectation{expectedFact("fabric.vlan_tags", "[]"), macFact, addrFact}, nil),
	}

	return Case{
		ID:      "troubleshooting/mdns-reflected-across-vlans",
		UseCase: UseCaseTroubleshooting,
		Question: "When a reflector sits on a trunk carrying VLAN 10 and VLAN 20, and the " +
			"switch snoops both VLANs with unregistered flooding off, does an mDNS query from " +
			"VLAN 10 still reach a VLAN 20 host through the reflector's copy?",
		FalseAnswer: "Assuming a switch locked down against unregistered multicast also blocks " +
			"the reflected copy, when 224.0.0.251 sits in the reserved range snooping always floods",
		CurrentResult: "sw1 floods h1's query to r1 despite FloodUnregistered being off; r1 " +
			"originates one copy from its VLAN 20 attachment, sw1 floods it to h2, and h2 decodes " +
			"and delivers it under host.ip.group",
		ExpectedMetadata: &MetadataExpectation{Status: analysis.Complete, Scope: analysis.WholeScope()},
		ExpectedOutcome:  trace.Flooded,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("group-destination"),
			trace.RuleID("host.ip.group"),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "mac", Key: "01:00:5e:00:00:fb"},
			{Kind: "host", Key: "h2"},
		},
		ExpectedFacts: []FactExpectation{groupDestination, macFact, addrFact},
		ExpectedSteps: steps,
		Execute: func() (ExecutionResult, error) {
			cfg, err := reflectedQueryConfig()
			if err != nil {
				return ExecutionResult{}, err
			}
			fab, err := fabric.New(cfg)
			if err != nil {
				return ExecutionResult{}, err
			}

			frame, err := mdnsQueryFrame(reflectedQueryH1MAC, netip.MustParseAddr("10.0.10.5"), []byte("mdns-query"))
			if err != nil {
				return ExecutionResult{}, err
			}
			queryID, err := fab.Inject(fabric.Injection{
				At:     time.Unix(1700000000, 0),
				Origin: fabric.Endpoint{Node: "h1"},
				Frame:  frame,
			})
			if err != nil {
				return ExecutionResult{}, err
			}
			fab.Run(10)

			var copyJourney fabric.Journey
			matches := 0
			for _, j := range fab.Report() {
				if j.Parent == queryID {
					copyJourney = j
					matches++
				}
			}
			if matches != 1 {
				return ExecutionResult{}, fmt.Errorf("%d journeys have query %d as their parent, want exactly one", matches, queryID)
			}

			return ExecutionResult{
				Outcome:  journeyHopOutcome(copyJourney),
				Reason:   journeyHopReason(copyJourney),
				Steps:    journeySteps(copyJourney),
				Metadata: copyJourney.Metadata,
				Journey:  &copyJourney,
			}, nil
		},
	}
}

var (
	reflectorLoopH1MAC = netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x05, 0x01}
	reflectorLoopR1MAC = netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x05, 0xaa}
	reflectorLoopR2MAC = netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x05, 0xbb}
)

// reflectorLoopConfig cables h1 directly to r1's own attachment on VLAN 10,
// and r1 to r2 over a direct trunk carrying VLAN 10 and VLAN 20, with no
// switch between them: the far end of that cable is a reflector, so the
// Decisions require it enqueue an arrival rather than call arrive inline,
// and this is the configuration that exercises that branch. r1 answers on
// three attachments (its own VLAN 10 port and both VLANs on the trunk) and
// r2 on two (both VLANs on the trunk). A copy's entered set is cloned from
// its parent's at origination, so the first copy to cross the trunk and
// r2's own reflection of it back across the trunk each visit a trunk port
// their ancestry has not yet entered; only the third copy, r1's reflection
// of that second one back onto the trunk a second time, finds the trunk
// port already there and closes the loop.
func reflectorLoopConfig() fabric.Config {
	vid10, vid20 := vlan.ID(10), vlan.ID(20)
	gigabit := gigabitAuto()

	return fabric.Config{
		Hosts: map[string]fabric.Host{
			"h1": {Address: reflectorLoopH1MAC, VLAN: &vid10, Ethernet: gigabit},
		},
		Reflectors: map[string]fabric.Reflector{
			"r1": {
				Address: reflectorLoopR1MAC,
				Ports:   map[string]phy.Ethernet{"up": gigabit, "trunk": gigabit},
				Attachments: map[string]fabric.Attachment{
					"up":  {Port: "up", VLAN: &vid10, Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.9/24")}},
					"t10": {Port: "trunk", VLAN: &vid10, Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.10/24")}},
					"t20": {Port: "trunk", VLAN: &vid20, Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.20.10/24")}},
				},
			},
			"r2": {
				Address: reflectorLoopR2MAC,
				Ports:   map[string]phy.Ethernet{"trunk": gigabit},
				Attachments: map[string]fabric.Attachment{
					"t10": {Port: "trunk", VLAN: &vid10, Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.20/24")}},
					"t20": {Port: "trunk", VLAN: &vid20, Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.20.20/24")}},
				},
			},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "r1", Port: "up"}, Medium: fabric.TwistedPair},
			{A: fabric.Endpoint{Node: "r1", Port: "trunk"}, B: fabric.Endpoint{Node: "r2", Port: "trunk"}, Medium: fabric.TwistedPair},
		},
	}
}

// reflectorLoopBudget is the step budget the case runs with. It is large
// enough for the bounce between r1 and r2 to land its first EntryLoop, and
// small enough that Run still has arrivals queued when it stops: the run
// halts on the budget rather than on the loop itself.
const reflectorLoopBudget = 40

// CaseTroubleshootingMDNSTwoReflectorsLoop returns the case whose journey
// carries an EntryLoop entry: r1 and r2 share VLAN 10 and VLAN 20 over
// one trunk, and a copy bouncing between them eventually re-enters an
// endpoint one of its ancestors already entered. That arrival is also where
// the reflector's own acceptance step is recorded, since re-entry does not
// stop the frame from being decided; the arriving copy is taken for
// reflection immediately after the loop is marked, which is what keeps the
// bounce (and the run) going until the budget stops it. It pins that
// journey rather than the injected query's, since the query enters each
// reflector once and never re-enters.
func CaseTroubleshootingMDNSTwoReflectorsLoop() Case {
	vlanTags := expectedFact("fabric.vlan_tags", "[{tpid=0x8100;pcp=0;dei=false;vid=10}]")
	macFact := expectedFact("fabric.mac", "01:00:5e:00:00:fb")
	addrFact := expectedFact("routing.addr", "224.0.0.251")
	udpPorts := expectedFact("fabric.udp_ports", "src=5353;dst=5353")

	steps := []StepExpectation{
		expectedStep(fabric.ReflectorLayer, trace.OpFilter, "reflector.udp.port", trace.Subject{Kind: "reflector", Key: "r2"},
			[]FactExpectation{vlanTags, macFact, addrFact, udpPorts}, nil),
	}

	return Case{
		ID:      "troubleshooting/mdns-two-reflectors-loop",
		UseCase: UseCaseTroubleshooting,
		Question: "When two reflectors share VLAN 10 and VLAN 20 over one trunk, does a copy " +
			"bouncing between them surface as a recorded loop and stop the run on its step " +
			"budget, rather than circulating forever or overflowing the call stack?",
		FalseAnswer: "Assuming re-entry detection keyed on the injected query's own frame would " +
			"catch this, when the query itself never re-enters anything; only a reflected copy does",
		CurrentResult: "r1 and r2 keep reflecting each other's copies over their shared trunk; " +
			"one copy's arrival re-enters an endpoint its ancestry already carries, recording an " +
			"EntryLoop, and Run(40) still returns 40 with work left queued",
		ExpectedMetadata: &MetadataExpectation{Status: analysis.Complete, Scope: analysis.WholeScope()},
		ExpectedOutcome:  trace.Consumed,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("reflector.udp.port"),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "reflector", Key: "r2"},
		},
		ExpectedFacts: []FactExpectation{macFact, addrFact, udpPorts},
		ExpectedSteps: steps,
		Execute: func() (ExecutionResult, error) {
			fab, err := fabric.New(reflectorLoopConfig())
			if err != nil {
				return ExecutionResult{}, err
			}

			frame, err := mdnsQueryFrame(reflectorLoopH1MAC, netip.MustParseAddr("10.0.10.5"), []byte("mdns-query"))
			if err != nil {
				return ExecutionResult{}, err
			}
			if _, err := fab.Inject(fabric.Injection{
				At:     time.Unix(1700000000, 0),
				Origin: fabric.Endpoint{Node: "h1"},
				Frame:  frame,
			}); err != nil {
				return ExecutionResult{}, err
			}
			steps := fab.Run(reflectorLoopBudget)
			if steps != reflectorLoopBudget || len(fab.Snapshot().Queue) == 0 {
				return ExecutionResult{}, fmt.Errorf(
					"fabric.Run(%d) = %d steps with %d arrivals queued, want the budget exhausted with work still queued",
					reflectorLoopBudget, steps, len(fab.Snapshot().Queue))
			}

			var loopJourney fabric.Journey
			for _, j := range fab.Report() {
				if journeyHasKind(j.Entries, fabric.EntryLoop) {
					loopJourney = j
					break
				}
			}
			if loopJourney.FrameID == 0 {
				return ExecutionResult{}, fmt.Errorf("no journey carries an EntryLoop entry")
			}

			return ExecutionResult{
				Outcome:  journeyLastEntryOutcome(loopJourney),
				Reason:   journeyLastEntryReason(loopJourney),
				Steps:    journeySteps(loopJourney),
				Metadata: loopJourney.Metadata,
				Journey:  &loopJourney,
			}, nil
		},
	}
}

// journeyHasKind reports whether entries carries a kind entry.
func journeyHasKind(entries []fabric.Entry, kind fabric.EntryKind) bool {
	for _, e := range entries {
		if e.Kind == kind {
			return true
		}
	}

	return false
}

// journeyLastEntryOutcome returns the domain outcome of j's own last recorded
// entry: [trace.Consumed] for a reflector or host taking the frame for
// itself, [trace.Dropped] for one rejecting, discarding, or failing to
// decide it. Unlike journeyHopOutcome, which reads the last switch-layer
// [vswitch.ForwardResult], a reflector's own arrival carries no such
// result, only the Step its acceptance or refusal recorded.
func journeyLastEntryOutcome(j fabric.Journey) trace.Outcome {
	if len(j.Entries) == 0 {
		return ""
	}
	switch j.Entries[len(j.Entries)-1].Kind {
	case fabric.EntryReflection, fabric.EntryDelivery:
		return trace.Consumed
	case fabric.EntryRejection, fabric.EntryDrop, fabric.EntryUnresolved:
		return trace.Dropped
	default:
		return ""
	}
}

// journeyLastEntryReason returns the reason paired with [journeyLastEntryOutcome].
func journeyLastEntryReason(j fabric.Journey) trace.Reason {
	if len(j.Entries) == 0 {
		return ""
	}

	return j.Entries[len(j.Entries)-1].Reason
}

// RegisterReflectorCases populates registry with the cases covering the
// mDNS reflector's own use of a query it accepts: the copy it originates
// onto another VLAN, and the loop two reflectors sharing VLANs produce.
func RegisterReflectorCases(registry *Registry) {
	registry.MustRegister(CaseTroubleshootingMDNSReflectedAcrossVLANs())
	registry.MustRegister(CaseTroubleshootingMDNSTwoReflectorsLoop())
}
