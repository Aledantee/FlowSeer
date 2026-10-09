package reflector

import (
	"fmt"
	"net/netip"
	"strings"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/udp"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// Layer names reflector acceptance decisions.
const Layer trace.Layer = "reflector"

// Reflector acceptance rules. Each rejection names the clause that failed,
// since a reflector's clauses have exactly one way to be satisfied, unlike a
// host's several MAC and IP rules. The acceptance names itself rather than
// reusing the last clause it passed, so a step alone says which of the two
// outcomes a rule ID shared with a rejection would otherwise leave to entry
// kind.
const (
	ruleReflectorMACOwnSource   trace.RuleID = "reflector.mac.own_source"
	ruleReflectorVLANForm       trace.RuleID = "reflector.vlan.form"
	ruleReflectorMACGroup       trace.RuleID = "reflector.mac.group"
	ruleReflectorIPGroup        trace.RuleID = "reflector.ip.group"
	ruleReflectorIPUndecodable  trace.RuleID = "reflector.ip.undecodable"
	ruleReflectorUDPProtocol    trace.RuleID = "reflector.udp.protocol"
	ruleReflectorUDPUndecodable trace.RuleID = "reflector.udp.undecodable"
	ruleReflectorUDPPort        trace.RuleID = "reflector.udp.port"
	ruleReflectorAccepted       trace.RuleID = "reflector.accepted"
)

// Reflector acceptance reasons, one per rule above that can refuse or leave a
// frame undecided.
const (
	// ReasonReflectorOwnSource records a reflector refusing a frame whose
	// Ethernet source is its own MAC: a copy it sent flooding back to it.
	ReasonReflectorOwnSource trace.Reason = "reflector-own-source"

	// ReasonReflectorTagFormNotAccepted records a reflector refusing a frame
	// whose tag form matches no attachment on the arrival port.
	ReasonReflectorTagFormNotAccepted trace.Reason = "reflector-tag-form-not-accepted"

	// ReasonReflectorMACNotGroup records a reflector refusing a frame whose
	// destination MAC is not the IPv4 or IPv6 mDNS group MAC.
	ReasonReflectorMACNotGroup trace.Reason = "reflector-mac-not-group"

	// ReasonReflectorIPHeaderUndecodable records a reflector that cannot
	// decide on a frame because its IP header does not decode.
	ReasonReflectorIPHeaderUndecodable trace.Reason = "reflector-ip-header-undecodable"

	// ReasonReflectorIPNotGroup records a reflector refusing a frame whose IP
	// destination is not the mDNS group its destination MAC named.
	ReasonReflectorIPNotGroup trace.Reason = "reflector-ip-not-group"

	// ReasonReflectorProtocolNotUDP records a reflector refusing a frame
	// whose IP protocol is not UDP.
	ReasonReflectorProtocolNotUDP trace.Reason = "reflector-protocol-not-udp"

	// ReasonReflectorUDPHeaderUndecodable records a reflector that cannot
	// decide on a frame because its UDP header does not decode.
	ReasonReflectorUDPHeaderUndecodable trace.Reason = "reflector-udp-header-undecodable"

	// ReasonReflectorUDPPortNotMDNS records a reflector refusing a frame
	// whose UDP destination port is not 5353.
	ReasonReflectorUDPPortNotMDNS trace.Reason = "reflector-udp-port-not-mdns"

	// ReasonReflectorNoAddress records a reflector dropping a copy it did not
	// originate: the target attachment names no address of the accepted
	// datagram's family.
	ReasonReflectorNoAddress trace.Reason = "reflector-no-address"
)

var (
	// mdnsIPv4Group is 224.0.0.251, the IPv4 mDNS group (RFC 6762 §3).
	mdnsIPv4Group = netip.AddrFrom4([4]byte{224, 0, 0, 251})

	// mdnsIPv6Group is ff02::fb, the IPv6 mDNS group (RFC 6762 §3).
	mdnsIPv6Group = netip.MustParseAddr("ff02::fb")

	// mdnsIPv4MAC is the Ethernet group MAC of mdnsIPv4Group (RFC 1112 §6.4).
	mdnsIPv4MAC = netaddr.MAC{0x01, 0x00, 0x5e, 0x00, 0x00, 0xfb}

	// mdnsIPv6MAC is the Ethernet group MAC of mdnsIPv6Group (RFC 2464 §7).
	mdnsIPv6MAC = netaddr.MAC{0x33, 0x33, 0x00, 0x00, 0x00, 0xfb}
)

// mdnsUDPPort is the UDP port an mDNS querier and responder both use
// (RFC 6762 §5.2).
const mdnsUDPPort = 5353

// udpProtocolNumber is the IANA protocol number for UDP, carried in the IP
// header's Protocol field (RFC 768).
const udpProtocolNumber uint8 = 17

type tagsFact string

func (f tagsFact) TypeID() string    { return "fabric.vlan_tags" }
func (f tagsFact) Canonical() string { return string(f) }

// vlanTagsFact is the tag stack a reflector's VLAN check reads, outermost first.
// A zero TPID is written as the C-TAG it encodes as.
func vlanTagsFact(tags []vlan.Tag) trace.Fact {
	var out strings.Builder
	out.WriteByte('[')
	for i, tag := range tags {
		if i > 0 {
			out.WriteByte(',')
		}
		tpid := tag.TPID
		if tpid == 0 {
			tpid = uint16(ethernet.EtherTypeDot1Q)
		}
		fmt.Fprintf(&out, "{tpid=%#04x;pcp=%d;dei=%t;vid=%d}", tpid, tag.PCP, tag.DEI, tag.VID)
	}
	out.WriteByte(']')

	return tagsFact(out.String())
}

type udpPortsFact string

func (f udpPortsFact) TypeID() string    { return "fabric.udp_ports" }
func (f udpPortsFact) Canonical() string { return string(f) }

// udpPortsFactFor is the UDP clause's fact: the datagram's source and
// destination ports, canonicalized as "src=<n>;dst=<n>".
func udpPortsFactFor(header udp.Header) trace.Fact {
	return udpPortsFact(fmt.Sprintf("src=%d;dst=%d", header.SrcPort, header.DstPort))
}

func accept(name string, refl Config, arrivalPort string, frame ethernet.Frame) (trace.Outcome, trace.Step, trace.Reason) {
	inputs := []trace.Fact{vlanTagsFact(frame.Tags)}
	decide := func(kind trace.Outcome, rule trace.RuleID, reason trace.Reason) (trace.Outcome, trace.Step, trace.Reason) {
		return kind, trace.Step{
			Layer:   Layer,
			Op:      trace.OpFilter,
			RuleID:  rule,
			Subject: trace.Subject{Kind: "reflector", Key: name},
			Inputs:  inputs,
		}, reason
	}

	if frame.Src == refl.Address {
		return decide(trace.Dropped, ruleReflectorMACOwnSource, ReasonReflectorOwnSource)
	}
	if !acceptsReflectorTags(refl, arrivalPort, frame.Tags) {
		return decide(trace.Dropped, ruleReflectorVLANForm, ReasonReflectorTagFormNotAccepted)
	}

	inputs = append(inputs, macFact(frame.Dst))
	if frame.Dst != mdnsIPv4MAC && frame.Dst != mdnsIPv6MAC {
		return decide(trace.Dropped, ruleReflectorMACGroup, ReasonReflectorMACNotGroup)
	}

	var version int
	switch frame.EtherType {
	case ethernet.EtherTypeIPv4:
		version = 4
	case ethernet.EtherTypeIPv6:
		version = 6
	}
	header, payload, err := ip.Decode(frame.Payload)
	if version == 0 || err != nil || header.Version() != version {
		return decide(trace.Dropped, ruleReflectorIPUndecodable, ReasonReflectorIPHeaderUndecodable)
	}

	inputs = append(inputs, routing.AddrFact(header.Dst))
	if (frame.Dst == mdnsIPv4MAC && header.Dst != mdnsIPv4Group) || (frame.Dst == mdnsIPv6MAC && header.Dst != mdnsIPv6Group) {
		return decide(trace.Dropped, ruleReflectorIPGroup, ReasonReflectorIPNotGroup)
	}

	if header.Protocol != udpProtocolNumber {
		return decide(trace.Dropped, ruleReflectorUDPProtocol, ReasonReflectorProtocolNotUDP)
	}

	udpHeader, _, err := udp.Decode(payload)
	if err != nil {
		return decide(trace.Dropped, ruleReflectorUDPUndecodable, ReasonReflectorUDPHeaderUndecodable)
	}

	inputs = append(inputs, udpPortsFactFor(udpHeader))
	if udpHeader.DstPort != mdnsUDPPort {
		return decide(trace.Dropped, ruleReflectorUDPPort, ReasonReflectorUDPPortNotMDNS)
	}

	return decide(trace.Consumed, ruleReflectorAccepted, "")
}

func acceptsReflectorTags(r Config, port string, tags []vlan.Tag) bool {
	_, ok := arrivalAttachment(r, port, tags)

	return ok
}

type macFact netaddr.MAC

func (f macFact) TypeID() string    { return "fabric.mac" }
func (f macFact) Canonical() string { return netaddr.MAC(f).String() }
