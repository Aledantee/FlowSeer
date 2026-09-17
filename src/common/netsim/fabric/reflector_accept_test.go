package fabric_test

import (
	"net/netip"
	"strconv"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/udp"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

var (
	reflectorOwnAddress = netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0xaa}
	reflectorSenderMAC  = netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x01}

	// reflectorGroupMAC is the Ethernet group MAC of reflectorGroupAddr (RFC
	// 1112 §6.4): 01:00:5e plus the low 23 bits of 224.0.0.251.
	reflectorGroupMAC  = netaddr.MAC{0x01, 0x00, 0x5e, 0x00, 0x00, 0xfb}
	reflectorGroupAddr = netip.AddrFrom4([4]byte{224, 0, 0, 251})
)

// reflectorAcceptConfig builds a fabric with one switch of two ports, a host
// h1 cabled to the first, and a reflector r1 attached to the second, on VLAN
// 10. Injecting at the port facing h1 floods to the port facing r1, the only
// other port, so the frame's fate at r1 is unambiguous: no other device
// shares the journey.
func reflectorAcceptConfig(t *testing.T) fabric.Config {
	t.Helper()
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})

	vid10 := vlan.ID(10)

	return fabric.Config{
		Switches: map[string]vswitch.Config{"sw1": {Ports: mustTable(t, b)}},
		Hosts: map[string]fabric.Host{
			"h1": {Address: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}},
		},
		Reflectors: map[string]fabric.Reflector{
			"r1": {
				Address: reflectorOwnAddress,
				Ports:   map[string]phy.Ethernet{"p1": {}},
				Attachments: map[string]fabric.Attachment{
					"a": {
						Port:      "p1",
						VLAN:      &vid10,
						Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.9/24")},
					},
				},
			},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, LengthMeters: 5},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "r1", Port: "p1"}, LengthMeters: 5},
		},
	}
}

// reflectorArrival injects frame at the port facing away from r1 and returns
// the journey once the run has settled.
func reflectorArrival(t *testing.T, cfg fabric.Config, frame ethernet.Frame) fabric.Journey {
	t.Helper()
	fab, err := fabric.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}
	fid, err := fab.Inject(fabric.Injection{
		At:     fixedTime,
		Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
		Frame:  frame,
	})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	fab.Run(10)

	for _, j := range fab.Report() {
		if j.FrameID == fid {
			return j
		}
	}
	t.Fatalf("journey %d not found", fid)

	return fabric.Journey{}
}

// reflectorDecision returns the last entry recorded at r1, which is its
// acceptance decision (or, for a corrupt arrival, its drop).
func reflectorDecision(t *testing.T, journey fabric.Journey) fabric.Entry {
	t.Helper()
	var decision *fabric.Entry
	for i := range journey.Entries {
		if journey.Entries[i].Device == "r1" {
			decision = &journey.Entries[i]
		}
	}
	if decision == nil {
		t.Fatalf("entries = %+v, want one at r1", journey.Entries)
	}

	return *decision
}

// mdnsFrame builds an Ethernet frame carrying an IPv4 UDP datagram, letting
// each case override exactly one field of an otherwise fully accepted mDNS
// query: source MAC, tags, destination MAC, IP destination, IP protocol, or
// UDP destination port.
func mdnsFrame(t *testing.T, src netaddr.MAC, tags []vlan.Tag, dstMAC netaddr.MAC, dstIP netip.Addr, protocol uint8, dstPort uint16) ethernet.Frame {
	t.Helper()
	srcIP := netip.MustParseAddr("10.0.10.7")
	udpDatagram, err := udp.Encode(udp.Header{SrcPort: 5353, DstPort: dstPort}, []byte("query"), srcIP, dstIP)
	if err != nil {
		t.Fatalf("encode UDP datagram: %v", err)
	}
	ipHeader := ip.Header{Src: srcIP, Dst: dstIP, HopLimit: 255, Protocol: protocol, V4: &ip.V4{}}
	ipPacket, err := ipHeader.Encode(udpDatagram)
	if err != nil {
		t.Fatalf("encode IP header: %v", err)
	}

	return ethernet.Frame{Src: src, Dst: dstMAC, Tags: tags, EtherType: ethernet.EtherTypeIPv4, Payload: ipPacket}
}

func acceptedVID10() []vlan.Tag {
	return []vlan.Tag{{TPID: 0x8100, VID: 10}}
}

// mustUndecodableUDPFrame builds an otherwise fully accepted mDNS query whose
// IPv4 header carries no payload: a total length equal to its header length,
// which ip.Decode accepts but leaves udp.Decode nothing to read.
func mustUndecodableUDPFrame(t *testing.T) ethernet.Frame {
	t.Helper()
	ipHeader := ip.Header{
		Src: netip.MustParseAddr("10.0.10.7"), Dst: reflectorGroupAddr,
		HopLimit: 255, Protocol: 17, V4: &ip.V4{},
	}
	ipPacket, err := ipHeader.Encode(nil)
	if err != nil {
		t.Fatalf("encode IP header: %v", err)
	}

	return ethernet.Frame{
		Src: reflectorSenderMAC, Dst: reflectorGroupMAC, Tags: acceptedVID10(),
		EtherType: ethernet.EtherTypeIPv4, Payload: ipPacket,
	}
}

func TestReflectorAcceptanceFollowsItsCheckOrder(t *testing.T) {
	other := netip.AddrFrom4([4]byte{10, 0, 10, 50})
	otherMAC := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x09}
	mismatchedVID := []vlan.Tag{{TPID: 0x8100, VID: 20}}

	tests := []struct {
		name       string
		frame      ethernet.Frame
		wantKind   fabric.EntryKind
		wantRule   trace.RuleID
		wantReason trace.Reason
	}{
		{
			name:     "an mDNS query is accepted",
			frame:    mdnsFrame(t, reflectorSenderMAC, acceptedVID10(), reflectorGroupMAC, reflectorGroupAddr, 17, 5353),
			wantKind: fabric.EntryReflection, wantRule: "reflector.accepted",
		},
		{
			name:       "a frame sourced from the reflector's own MAC is refused",
			frame:      mdnsFrame(t, reflectorOwnAddress, acceptedVID10(), reflectorGroupMAC, reflectorGroupAddr, 17, 5353),
			wantKind:   fabric.EntryRejection,
			wantRule:   "reflector.mac.own_source",
			wantReason: fabric.ReasonReflectorOwnSource,
		},
		{
			name:       "a tag form matching no attachment on the arrival port is refused",
			frame:      mdnsFrame(t, reflectorSenderMAC, mismatchedVID, reflectorGroupMAC, reflectorGroupAddr, 17, 5353),
			wantKind:   fabric.EntryRejection,
			wantRule:   "reflector.vlan.form",
			wantReason: fabric.ReasonReflectorTagFormNotAccepted,
		},
		{
			name:       "a destination MAC that is not the mDNS group MAC is refused",
			frame:      mdnsFrame(t, reflectorSenderMAC, acceptedVID10(), otherMAC, reflectorGroupAddr, 17, 5353),
			wantKind:   fabric.EntryRejection,
			wantRule:   "reflector.mac.group",
			wantReason: fabric.ReasonReflectorMACNotGroup,
		},
		{
			name:       "an IP destination that is not the mDNS group is refused",
			frame:      mdnsFrame(t, reflectorSenderMAC, acceptedVID10(), reflectorGroupMAC, other, 17, 5353),
			wantKind:   fabric.EntryRejection,
			wantRule:   "reflector.ip.group",
			wantReason: fabric.ReasonReflectorIPNotGroup,
		},
		{
			name:       "a protocol that is not UDP is refused",
			frame:      mdnsFrame(t, reflectorSenderMAC, acceptedVID10(), reflectorGroupMAC, reflectorGroupAddr, 6, 5353),
			wantKind:   fabric.EntryRejection,
			wantRule:   "reflector.udp.protocol",
			wantReason: fabric.ReasonReflectorProtocolNotUDP,
		},
		{
			name:       "a UDP destination port that is not 5353 is refused",
			frame:      mdnsFrame(t, reflectorSenderMAC, acceptedVID10(), reflectorGroupMAC, reflectorGroupAddr, 17, 5354),
			wantKind:   fabric.EntryRejection,
			wantRule:   "reflector.udp.port",
			wantReason: fabric.ReasonReflectorUDPPortNotMDNS,
		},
		{
			name: "an undecodable IP header leaves acceptance unknown",
			frame: ethernet.Frame{
				Src: reflectorSenderMAC, Dst: reflectorGroupMAC, Tags: acceptedVID10(),
				EtherType: ethernet.EtherTypeIPv4, Payload: []byte{0x45, 0x00, 0x00},
			},
			wantKind:   fabric.EntryUnresolved,
			wantRule:   "reflector.ip.undecodable",
			wantReason: fabric.ReasonReflectorIPHeaderUndecodable,
		},
		{
			// A well-formed IPv4 header whose total length equals its header
			// length decodes with an empty payload, which udp.Decode refuses
			// as shorter than the eight-octet UDP header. Nothing else about
			// the header is wrong, so the undecodable UDP header is the only
			// thing that can leave acceptance unknown here.
			name:       "an undecodable UDP header leaves acceptance unknown",
			frame:      mustUndecodableUDPFrame(t),
			wantKind:   fabric.EntryUnresolved,
			wantRule:   "reflector.udp.undecodable",
			wantReason: fabric.ReasonReflectorUDPHeaderUndecodable,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			journey := reflectorArrival(t, reflectorAcceptConfig(t), tc.frame)
			decision := reflectorDecision(t, journey)

			if decision.Kind != tc.wantKind || decision.Reason != tc.wantReason || decision.Device != "r1" {
				t.Errorf("decision = %s %q at %q, want %s %q at r1", decision.Kind, decision.Reason, decision.Device, tc.wantKind, tc.wantReason)
			}
			step := decision.Step
			if step == nil {
				t.Fatalf("decision %+v carries no step", decision)
			}
			if step.Layer != fabric.ReflectorLayer || step.Op != trace.OpFilter || step.RuleID != tc.wantRule ||
				step.Subject != (trace.Subject{Kind: "reflector", Key: "r1"}) {
				t.Errorf("step = %s %s %s %+v, want reflector filter %s on reflector r1", step.Layer, step.Op, step.RuleID, step.Subject, tc.wantRule)
			}
			if len(step.Inputs) == 0 {
				t.Error("step carries no facts")
			}

			if len(journey.Deliveries) != 0 {
				t.Errorf("deliveries = %+v, want none: a reflector acceptance is not a host delivery", journey.Deliveries)
			}

			undecided := false
			for _, issue := range journey.Metadata.Issues() {
				if issue.Code == analysis.IssueCode(tc.wantReason) &&
					issue.Status == analysis.Incomplete &&
					issue.Scope.Compare(analysis.JourneyScope(strconv.FormatUint(uint64(journey.FrameID), 10))) == 0 {
					undecided = true
				}
			}
			if want := tc.wantKind == fabric.EntryUnresolved; undecided != want {
				t.Errorf("journey issues = %+v, undecodable issue present %t, want %t", journey.Metadata.Issues(), undecided, want)
			}
		})
	}
}

// TestReflectorDropsACorruptArrivalBeforeAnyClauseRuns proves that a corrupt
// arrival is discarded before acceptReflector ever runs, over an otherwise
// fully accepted mDNS query: only the corruption can explain the drop.
func TestReflectorDropsACorruptArrivalBeforeAnyClauseRuns(t *testing.T) {
	cfg := reflectorAcceptConfig(t)
	cfg.Cables[1].Fault = fabric.Fault{Kind: fabric.FaultCorruptEveryNth, N: 1}

	frame := mdnsFrame(t, reflectorSenderMAC, acceptedVID10(), reflectorGroupMAC, reflectorGroupAddr, 17, 5353)
	journey := reflectorArrival(t, cfg, frame)
	decision := reflectorDecision(t, journey)

	if decision.Kind != fabric.EntryDrop || decision.Reason != fabric.ReasonBadFrame {
		t.Errorf("decision = %s %q, want Drop %q", decision.Kind, decision.Reason, fabric.ReasonBadFrame)
	}
	if decision.Step != nil {
		t.Errorf("decision step = %+v, want none: a corrupt arrival never reaches a clause", decision.Step)
	}
	if len(journey.Deliveries) != 0 {
		t.Errorf("deliveries = %+v, want none", journey.Deliveries)
	}
}
