package fabric

import (
	"fmt"
	reflectormodel "go.aledante.io/FlowSeer/src/common/sim/device/reflector"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/udp"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// reflectormodel.Config acceptance rules. Each rejection names the clause that failed,
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

// reflectormodel.Config acceptance reasons, one per rule above that can refuse or leave a
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
	mdnsIPv4MAC = groupMAC(mdnsIPv4Group)

	// mdnsIPv6MAC is the Ethernet group MAC of mdnsIPv6Group (RFC 2464 §7).
	mdnsIPv6MAC = groupMAC(mdnsIPv6Group)
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

// arriveReflector records a reflector's decision on an arrived frame,
// journaled on the arriving frame's own journey rather than on any copy: a
// copy does not exist yet at this point, only the frame that reached the
// reflector. Unlike arriveHost, it never appends to Deliveries, since an
// EntryReflection is not a host taking delivery of a frame, and Compare pairs
// journeys on Deliveries alone.
func (f *Fabric) arriveReflector(j *Journey, name string, refl reflectormodel.Config, arrivalPort string, frame ethernet.Frame, at time.Time) Entry {
	kind, step, reason := acceptReflector(name, refl, arrivalPort, frame)
	var raised []analysis.Issue
	if kind == EntryUnresolved {
		raised = append(raised, analysis.Issue{
			Code:    analysis.IssueCode(reason),
			Status:  analysis.Incomplete,
			Scope:   analysis.JourneyScope(strconv.FormatUint(uint64(j.FrameID), 10)),
			Message: fmt.Sprintf("reflector %q cannot decide on frame %d: its header does not decode", name, j.FrameID),
		})
	}
	entry := Entry{At: at, Kind: kind, Device: name, Port: arrivalPort, Step: &step, Reason: reason}
	f.record(j, entry, raised...)

	return entry
}

// acceptReflector runs a reflector's checks on an arrived frame in order: that
// its Ethernet source is not the reflector's own MAC, that its tag form
// matches an attachment on the arrival port, that its destination MAC is the
// IPv4 or IPv6 mDNS group MAC, that its IP destination is the corresponding
// mDNS group, that its protocol is UDP, and that its UDP destination is port
// 5353. It returns EntryReflection, EntryRejection, or EntryUnresolved, with
// the step of the deciding rule over the facts read up to it.
func acceptReflector(name string, refl reflectormodel.Config, arrivalPort string, frame ethernet.Frame) (EntryKind, trace.Step, trace.Reason) {
	inputs := []trace.Fact{vlanTagsFact(frame.Tags)}
	decide := func(kind EntryKind, rule trace.RuleID, reason trace.Reason) (EntryKind, trace.Step, trace.Reason) {
		return kind, trace.Step{
			Layer:   ReflectorLayer,
			Op:      trace.OpFilter,
			RuleID:  rule,
			Subject: trace.Subject{Kind: "reflector", Key: name},
			Inputs:  inputs,
		}, reason
	}

	if frame.Src == refl.Address {
		return decide(EntryRejection, ruleReflectorMACOwnSource, ReasonReflectorOwnSource)
	}
	if !acceptsReflectorTags(refl, arrivalPort, frame.Tags) {
		return decide(EntryRejection, ruleReflectorVLANForm, ReasonReflectorTagFormNotAccepted)
	}

	inputs = append(inputs, macFact(frame.Dst))
	if frame.Dst != mdnsIPv4MAC && frame.Dst != mdnsIPv6MAC {
		return decide(EntryRejection, ruleReflectorMACGroup, ReasonReflectorMACNotGroup)
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
		return decide(EntryUnresolved, ruleReflectorIPUndecodable, ReasonReflectorIPHeaderUndecodable)
	}

	inputs = append(inputs, routing.AddrFact(header.Dst))
	if header.Dst != mdnsIPv4Group && header.Dst != mdnsIPv6Group {
		return decide(EntryRejection, ruleReflectorIPGroup, ReasonReflectorIPNotGroup)
	}

	if header.Protocol != udpProtocolNumber {
		return decide(EntryRejection, ruleReflectorUDPProtocol, ReasonReflectorProtocolNotUDP)
	}

	udpHeader, _, err := udp.Decode(payload)
	if err != nil {
		return decide(EntryUnresolved, ruleReflectorUDPUndecodable, ReasonReflectorUDPHeaderUndecodable)
	}

	inputs = append(inputs, udpPortsFactFor(udpHeader))
	if udpHeader.DstPort != mdnsUDPPort {
		return decide(EntryRejection, ruleReflectorUDPPort, ReasonReflectorUDPPortNotMDNS)
	}

	return decide(EntryReflection, ruleReflectorAccepted, "")
}

// acceptsTags reports whether tags is the tag form of one of the reflector's
// attachments on port: with no VLAN, untagged or one VID 0 priority C-TAG;
// with a VLAN, one C-TAG with that VID. Mirrors host acceptance, but over
// the several attachments a port may carry rather than one host VLAN. It
// answers the same question as [reflectorArrivalAttachment], over the same
// attachments, so the two are defined in terms of each other rather than
// carrying the tag-form guards twice.
func acceptsReflectorTags(r reflectormodel.Config, port string, tags []vlan.Tag) bool {
	_, ok := reflectorArrivalAttachment(r, port, tags)

	return ok
}

// arriveHost records the queued arrival and the host's acceptance decision.
func (f *Fabric) arriveHost(j *Journey, name string, frame ethernet.Frame, at time.Time, res device.Result) Entry {
	cable := f.portCable(name, "")
	f.record(j, Entry{At: at, Kind: EntryArrival, Device: name, Cable: cable})
	kind := EntryRejection
	var raised []analysis.Issue
	if res.Outcome == trace.Consumed {
		kind = EntryDelivery
		j.Deliveries = append(j.Deliveries, Delivery{Host: name, At: at, Frame: cloneFrame(frame)})
	} else if res.Metadata.Status() == analysis.Incomplete {
		kind = EntryUnresolved
		raised = append(raised, analysis.Issue{Code: analysis.IssueCode(res.Reason), Status: analysis.Incomplete, Scope: analysis.JourneyScope(strconv.FormatUint(uint64(j.FrameID), 10)), Message: fmt.Sprintf("host %q cannot decide on frame %d: its IP header does not decode", name, j.FrameID)})
	}
	step := res.Steps[len(res.Steps)-1]
	entry := Entry{At: at, Kind: kind, Device: name, Cable: cable, Step: &step, Reason: res.Reason}
	f.record(j, entry, raised...)
	return entry
}

// groupMAC maps an IP group address to its Ethernet group MAC: the low 23 bits
// of an IPv4 group under 01:00:5e (RFC 1112 §6.4), or the low 32 bits of an
// IPv6 group under 33:33 (RFC 2464 §7).
func groupMAC(group netip.Addr) netaddr.MAC {
	if group.Is4() {
		a := group.As4()

		return netaddr.MAC{0x01, 0x00, 0x5e, a[1] & 0x7f, a[2], a[3]}
	}
	a := group.As16()

	return netaddr.MAC{0x33, 0x33, a[12], a[13], a[14], a[15]}
}
