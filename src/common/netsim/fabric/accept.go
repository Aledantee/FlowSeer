package fabric

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
)

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

// arrive records a frame reaching a host over cable and the host's decision on
// it, delivering the frame only when the host accepts it.
func (f *Fabric) arrive(j *Journey, host string, cable Cable, frame ethernet.Frame, at time.Time) {
	arrivalCable := cable.Clone()
	f.record(j, Entry{At: at, Kind: EntryArrival, Device: host, Cable: &arrivalCable})

	kind, step, reason := f.accept(host, frame)
	var raised []analysis.Issue
	switch kind {
	case EntryDelivery:
		j.Deliveries = append(j.Deliveries, Delivery{Host: host, At: at, Frame: cloneFrame(frame)})
	case EntryUnresolved:
		raised = append(raised, analysis.Issue{
			Code:    analysis.IssueCode(reason),
			Status:  analysis.Incomplete,
			Scope:   analysis.JourneyScope(strconv.FormatUint(uint64(j.FrameID), 10)),
			Message: fmt.Sprintf("host %q cannot decide on frame %d: its IP header does not decode", host, j.FrameID),
		})
	}
	decisionCable := cable.Clone()
	f.record(j, Entry{At: at, Kind: kind, Device: host, Cable: &decisionCable, Step: &step, Reason: reason}, raised...)
}

// accept runs a host's checks on an arrived frame in order: its VLAN form, its
// destination MAC, and for a host with an IP stack its IP destination. It
// returns EntryDelivery, EntryRejection, or EntryUnresolved, with the step of
// the deciding rule over the facts read up to it.
func (f *Fabric) accept(name string, frame ethernet.Frame) (EntryKind, trace.Step, trace.Reason) {
	host := f.cfg.Hosts[name]
	inputs := []trace.Fact{vlanTagsFact(frame.Tags)}
	decide := func(kind EntryKind, rule trace.RuleID, reason trace.Reason) (EntryKind, trace.Step, trace.Reason) {
		return kind, trace.Step{
			Layer:   HostLayer,
			Op:      trace.OpFilter,
			RuleID:  rule,
			Subject: trace.Subject{Kind: "host", Key: name},
			Inputs:  inputs,
		}, reason
	}

	if !host.acceptsTags(frame.Tags) {
		return decide(EntryRejection, ruleHostVLANForm, ReasonHostVLANNotAccepted)
	}

	inputs = append(inputs, MACFact(frame.Dst))
	if host.Accept.Promiscuous {
		return decide(EntryDelivery, ruleHostMACPromiscuous, "")
	}
	macRule, ok := host.macRule(frame.Dst)
	switch {
	case !ok && frame.Dst.IsGroup():
		return decide(EntryRejection, ruleHostMACMulticastNotAccepted, ReasonHostMulticastNotAccepted)
	case !ok:
		return decide(EntryRejection, ruleHostMACUnicastNotAddressed, ReasonHostUnicastNotAddressed)
	}

	var version int
	switch frame.EtherType {
	case ethernet.EtherTypeIPv4:
		version = 4
	case ethernet.EtherTypeIPv6:
		version = 6
	}
	if host.IP == nil || version == 0 {
		return decide(EntryDelivery, macRule, "")
	}
	header, _, err := ip.Decode(frame.Payload)
	if err != nil || header.Version() != version {
		return decide(EntryUnresolved, ruleHostIPUndecodable, ReasonHostIPHeaderUndecodable)
	}

	inputs = append(inputs, routing.AddrFact(header.Dst))
	ipRule, ok := host.ipRule(header.Dst)
	if !ok {
		return decide(EntryRejection, ruleHostIPNotAddressed, ReasonHostIPNotAddressed)
	}

	return decide(EntryDelivery, ipRule, "")
}

// acceptsTags reports whether the tag stack is a form the host receives: with
// no VLAN, untagged or one VID 0 priority C-TAG; with a VLAN, one C-TAG with
// that VID.
func (h Host) acceptsTags(tags []vlan.Tag) bool {
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
func (h Host) macRule(dst netaddr.MAC) (trace.RuleID, bool) {
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
func (h Host) ipRule(dst netip.Addr) (trace.RuleID, bool) {
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
