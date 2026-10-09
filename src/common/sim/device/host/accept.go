package host

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

const (
	// ReasonVLANNotAccepted records a host refusing a frame whose tag form its VLAN does not accept.
	ReasonVLANNotAccepted trace.Reason = "host-vlan-not-accepted"

	// ReasonUnicastNotAddressed records a host refusing a unicast frame addressed to another MAC.
	ReasonUnicastNotAddressed trace.Reason = "host-unicast-not-addressed"

	// ReasonMulticastNotAccepted records a host refusing a frame to a group MAC it does not accept.
	ReasonMulticastNotAccepted trace.Reason = "host-multicast-not-accepted"

	// ReasonIPNotAddressed records a host refusing an IP packet addressed to none of its addresses,
	// broadcasts, or accepted groups.
	ReasonIPNotAddressed trace.Reason = "host-ip-not-addressed"

	// ReasonIPHeaderUndecodable records a host that cannot decide on a frame because its IP header
	// does not decode.
	ReasonIPHeaderUndecodable trace.Reason = "host-ip-header-undecodable"
)

// Layer is the trace layer of a host's acceptance decisions.
const Layer trace.Layer = "host"

// Host acceptance rules. An accepting rule names the clause that matched, so a
// step says why a frame was taken as well as why one was refused.
const (
	ruleHostVLANForm                trace.RuleID = "host.vlan.form"
	ruleHostMACOwn                  trace.RuleID = "host.mac.own"
	ruleHostMACBroadcast            trace.RuleID = "host.mac.broadcast"
	ruleHostMACPromiscuous          trace.RuleID = "host.mac.promiscuous"
	ruleHostMACAllMulticast         trace.RuleID = "host.mac.all_multicast"
	ruleHostMACListedMulticast      trace.RuleID = "host.mac.listed_multicast"
	ruleHostMACIPv4AllHosts         trace.RuleID = "host.mac.ipv4_all_hosts"
	ruleHostMACIPv6AllNodes         trace.RuleID = "host.mac.ipv6_all_nodes"
	ruleHostMACSolicitedNode        trace.RuleID = "host.mac.solicited_node"
	ruleHostMACUnicastNotAddressed  trace.RuleID = "host.mac.unicast_not_addressed"
	ruleHostMACMulticastNotAccepted trace.RuleID = "host.mac.multicast_not_accepted"
	ruleHostIPOwn                   trace.RuleID = "host.ip.own"
	ruleHostIPLimitedBroadcast      trace.RuleID = "host.ip.limited_broadcast"
	ruleHostIPDirectedBroadcast     trace.RuleID = "host.ip.directed_broadcast"
	ruleHostIPGroup                 trace.RuleID = "host.ip.group"
	ruleHostIPNotAddressed          trace.RuleID = "host.ip.not_addressed"
	ruleHostIPUndecodable           trace.RuleID = "host.ip.undecodable"
)

var (
	// ipv4AllHostsMAC is the MAC of 224.0.0.1, which every IPv4 host joins
	// (RFC 1112 §7.2, mapped per §6.4).
	ipv4AllHostsMAC = netaddr.MAC{0x01, 0x00, 0x5e, 0x00, 0x00, 0x01}

	// ipv6AllNodesMAC is the MAC of ff02::1 (RFC 4291 §2.7.1), which every IPv6
	// host recognizes (§2.8), mapped per RFC 2464 §7.
	ipv6AllNodesMAC = netaddr.MAC{0x33, 0x33, 0x00, 0x00, 0x00, 0x01}

	limitedBroadcast = netip.AddrFrom4([4]byte{255, 255, 255, 255})
)

var broadcastMAC = netaddr.MAC{255, 255, 255, 255, 255, 255}

type tagsFact string

func (f tagsFact) TypeID() string    { return "fabric.vlan_tags" }
func (f tagsFact) Canonical() string { return string(f) }

// vlanTagsFact is the tag stack a host's VLAN check reads, outermost first.
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

type macFact netaddr.MAC

func (f macFact) TypeID() string    { return "fabric.mac" }
func (f macFact) Canonical() string { return netaddr.MAC(f).String() }

// accept runs a host's checks on an arrived frame in order: its VLAN form, its
// destination MAC, and for a host with an IP stack its IP destination. It
// returns trace.Consumed or trace.Dropped, with the step of
// the deciding rule over the facts read up to it.
func (h *Host) accept(frame ethernet.Frame) device.Result {
	name, host := h.name, h.cfg
	inputs := []trace.Fact{vlanTagsFact(frame.Tags)}
	decide := func(outcome trace.Outcome, rule trace.RuleID, reason trace.Reason) device.Result {
		step := trace.Step{
			Layer:   Layer,
			Op:      trace.OpFilter,
			RuleID:  rule,
			Subject: trace.Subject{Kind: "host", Key: name},
			Inputs:  inputs,
		}
		res := device.Result{Acceptance: device.DeliveryAcceptance, Trace: trace.Trace{Outcome: outcome, Reason: reason, Steps: []trace.Step{step}}}
		if reason == ReasonIPHeaderUndecodable {
			scope := analysis.NodeScope(name)
			res.Metadata = analysis.NewMetadata(scope, []analysis.Issue{{Code: analysis.IssueCode(reason), Status: analysis.Incomplete, Scope: scope, Message: "host IP header does not decode"}}, analysis.EvidenceCatalog{}, nil)
		}
		return res
	}

	if !host.acceptsTags(frame.Tags) {
		return decide(trace.Dropped, ruleHostVLANForm, ReasonVLANNotAccepted)
	}

	inputs = append(inputs, macFact(frame.Dst))
	if host.Accept.Promiscuous {
		return decide(trace.Consumed, ruleHostMACPromiscuous, "")
	}
	macRule, ok := host.macRule(frame.Dst)
	switch {
	case !ok && frame.Dst.IsGroup():
		return decide(trace.Dropped, ruleHostMACMulticastNotAccepted, ReasonMulticastNotAccepted)
	case !ok:
		return decide(trace.Dropped, ruleHostMACUnicastNotAddressed, ReasonUnicastNotAddressed)
	}

	var version int
	switch frame.EtherType {
	case ethernet.EtherTypeIPv4:
		version = 4
	case ethernet.EtherTypeIPv6:
		version = 6
	}
	if host.IP == nil || version == 0 {
		return decide(trace.Consumed, macRule, "")
	}
	header, _, err := ip.Decode(frame.Payload)
	if err != nil || header.Version() != version {
		return decide(trace.Dropped, ruleHostIPUndecodable, ReasonIPHeaderUndecodable)
	}

	inputs = append(inputs, routing.AddrFact(header.Dst))
	ipRule, ok := host.ipRule(header.Dst)
	if !ok {
		return decide(trace.Dropped, ruleHostIPNotAddressed, ReasonIPNotAddressed)
	}

	return decide(trace.Consumed, ipRule, "")
}

// acceptsTags reports whether the tag stack is a form the host receives: with
// no VLAN, untagged or one VID 0 priority C-TAG; with a VLAN, one C-TAG with
// that VID.
func (h Config) acceptsTags(tags []vlan.Tag) bool {
	if len(tags) > 1 {
		return false
	}
	if len(tags) == 1 && tags[0].TPID != 0 && tags[0].TPID != uint16(ethernet.EtherTypeDot1Q) {
		return false
	}
	if h.VLAN == nil {
		return len(tags) == 0 || tags[0].VID == 0
	}

	return len(tags) == 1 && tags[0].VID == *h.VLAN
}

// macRule returns the rule under which the host accepts a destination MAC.
func (h Config) macRule(dst netaddr.MAC) (trace.RuleID, bool) {
	switch {
	case dst == h.Address:
		return ruleHostMACOwn, true
	case dst == broadcastMAC:
		return ruleHostMACBroadcast, true
	case !dst.IsGroup():
		return "", false
	case h.Accept.AllMulticast:
		return ruleHostMACAllMulticast, true
	case slices.Contains(h.Accept.Multicast, dst):
		return ruleHostMACListedMulticast, true
	case h.IP == nil:
		return "", false
	}

	for _, prefix := range h.IP.Addresses {
		addr := prefix.Addr()
		switch {
		case addr.Is4() && dst == ipv4AllHostsMAC:
			return ruleHostMACIPv4AllHosts, true
		case addr.Is6() && dst == ipv6AllNodesMAC:
			return ruleHostMACIPv6AllNodes, true
		case addr.Is6() && dst == solicitedNodeMAC(addr):
			return ruleHostMACSolicitedNode, true
		}
	}

	return "", false
}

// ipRule returns the rule under which a host with an IP stack accepts a packet
// to dst: an own address, the limited or directed broadcast of an own IPv4
// prefix, or a group whose MAC the host accepts.
func (h Config) ipRule(dst netip.Addr) (trace.RuleID, bool) {
	if dst.IsMulticast() {
		if _, ok := h.macRule(groupMAC(dst)); ok {
			return ruleHostIPGroup, true
		}

		return "", false
	}

	for _, prefix := range h.IP.Addresses {
		addr := prefix.Addr()
		switch {
		case addr == dst:
			return ruleHostIPOwn, true
		case addr.Is4() && dst == limitedBroadcast:
			return ruleHostIPLimitedBroadcast, true
		// A 31-bit prefix has no directed broadcast (RFC 3021 §2.2), and a
		// 32-bit one names only the host itself.
		case addr.Is4() && dst.Is4() && prefix.Bits() < 31 && dst == directedBroadcast(prefix):
			return ruleHostIPDirectedBroadcast, true
		}
	}

	return "", false
}

// solicitedNodeMAC is the MAC of the solicited-node group of an IPv6 address:
// ff02::1:ff00:0/104 with the address's low 24 bits (RFC 4291 §2.7.1), which a
// host recognizes for each of its addresses (§2.8), mapped per RFC 2464 §7.
func solicitedNodeMAC(addr netip.Addr) netaddr.MAC {
	a := addr.As16()

	return netaddr.MAC{0x33, 0x33, 0xff, a[13], a[14], a[15]}
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

func directedBroadcast(prefix netip.Prefix) netip.Addr {
	network := prefix.Masked().Addr().As4()
	hostBits := uint32(1)<<(32-prefix.Bits()) - 1
	var out [4]byte
	binary.BigEndian.PutUint32(out[:], binary.BigEndian.Uint32(network[:])|hostBits)

	return netip.AddrFrom4(out)
}
