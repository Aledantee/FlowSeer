// observe.go holds the predicates the DHCP and IPv6 checks read the wire
// with. Every predicate decodes with gopacket and returns false for a
// frame it cannot read, since the frames come from the segment.

package ip6

import (
	"bytes"
	"net"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
)

// echoID is the ICMPv6 echo identifier the ndpspoof probe carries, so a
// reply to the probe is told from a reply to the host's own ping.
const echoID = 0x4e50

// decodeFrame parses one Ethernet frame. The result's layers are nil for
// what the frame does not carry or carries malformed.
func decodeFrame(data []byte) gopacket.Packet {
	return gopacket.NewPacket(data, layers.LayerTypeEthernet, gopacket.Default)
}

// ethernetOf returns the frame's Ethernet header, or nil when it has none.
func ethernetOf(pkt gopacket.Packet) *layers.Ethernet {
	eth, _ := pkt.Layer(layers.LayerTypeEthernet).(*layers.Ethernet)
	return eth
}

// solicitationOf returns the frame's Ethernet, IPv6, and Neighbor
// Solicitation layers, or nils when it is not one.
func solicitationOf(data []byte) (*layers.Ethernet, *layers.IPv6, *layers.ICMPv6NeighborSolicitation) {
	pkt := decodeFrame(data)
	eth := ethernetOf(pkt)
	ip, _ := pkt.Layer(layers.LayerTypeIPv6).(*layers.IPv6)
	ns, _ := pkt.Layer(layers.LayerTypeICMPv6NeighborSolicitation).(*layers.ICMPv6NeighborSolicitation)
	if eth == nil || ip == nil || ns == nil {
		return nil, nil, nil
	}
	return eth, ip, ns
}

// isDADSolicitIn returns the target of a Neighbor Solicitation in data
// from a MAC other than leg whose source is unspecified and whose target
// lies within prefix: the duplicate address detection a host runs on an
// address it formed from the prefix (RFC 4862 sections 5.4 and 5.5.3).
func isDADSolicitIn(data []byte, leg net.HardwareAddr, prefix *net.IPNet) (net.IP, bool) {
	eth, ip, ns := solicitationOf(data)
	if eth == nil || bytes.Equal(eth.SrcMAC, leg) {
		return nil, false
	}
	if !ip.SrcIP.IsUnspecified() || !prefix.Contains(ns.TargetAddress) {
		return nil, false
	}
	return ns.TargetAddress, true
}

// isSolicitForAny returns the target of a Neighbor Solicitation in data
// when it is one of targets, the probe a host sends a router it has
// traffic for (RFC 4861 section 7.3.3).
func isSolicitForAny(data []byte, targets []net.IP) (net.IP, bool) {
	_, _, ns := solicitationOf(data)
	if ns == nil {
		return nil, false
	}
	for _, t := range targets {
		if ns.TargetAddress.Equal(t) {
			return ns.TargetAddress, true
		}
	}
	return nil, false
}

// isSolicitFromFor reports whether data is a Neighbor Solicitation from
// the victim's MAC for target, the question a victim asks once it has no
// entry it trusts for the address.
func isSolicitFromFor(data []byte, victim net.HardwareAddr, target net.IP) bool {
	eth, _, ns := solicitationOf(data)
	return eth != nil && bytes.Equal(eth.SrcMAC, victim) && ns.TargetAddress.Equal(target)
}

// isEchoReplyTo reports whether data is an ICMPv6 echo reply from the
// victim's MAC carrying the probe's identifier whose IPv6 destination is
// the spoofed address, and returns the Ethernet destination the victim
// sent it to. The destination is the verdict: the attack leg's MAC means
// the victim's neighbor cache resolves the spoofed address to the attacker.
func isEchoReplyTo(data []byte, victim net.HardwareAddr, spoofed net.IP) (net.HardwareAddr, bool) {
	pkt := decodeFrame(data)
	eth := ethernetOf(pkt)
	ip, _ := pkt.Layer(layers.LayerTypeIPv6).(*layers.IPv6)
	icmp, _ := pkt.Layer(layers.LayerTypeICMPv6).(*layers.ICMPv6)
	echo, _ := pkt.Layer(layers.LayerTypeICMPv6Echo).(*layers.ICMPv6Echo)
	if eth == nil || ip == nil || icmp == nil || echo == nil || !bytes.Equal(eth.SrcMAC, victim) {
		return nil, false
	}
	if icmp.TypeCode.Type() != layers.ICMPv6TypeEchoReply || echo.Identifier != echoID || !ip.DstIP.Equal(spoofed) {
		return nil, false
	}
	return eth.DstMAC, true
}

// dhcpv4Of returns the frame's Ethernet and DHCPv4 layers, or nils when it
// is not a DHCPv4 message.
func dhcpv4Of(data []byte) (*layers.Ethernet, *layers.DHCPv4) {
	pkt := decodeFrame(data)
	eth := ethernetOf(pkt)
	dhcp, _ := pkt.Layer(layers.LayerTypeDHCPv4).(*layers.DHCPv4)
	if eth == nil || dhcp == nil {
		return nil, nil
	}
	return eth, dhcp
}

// dhcpv4MessageType returns the DHCPv4 message type option, or 0 when the
// message carries none.
func dhcpv4MessageType(dhcp *layers.DHCPv4) layers.DHCPMsgType {
	for _, opt := range dhcp.Options {
		if opt.Type == layers.DHCPOptMessageType && len(opt.Data) == 1 {
			return layers.DHCPMsgType(opt.Data[0])
		}
	}
	return 0
}

// dhcpv4ServerID returns the DHCPv4 server identifier option, or nil when
// the message carries none.
func dhcpv4ServerID(dhcp *layers.DHCPv4) net.IP {
	for _, opt := range dhcp.Options {
		if opt.Type == layers.DHCPOptServerID && len(opt.Data) == 4 {
			return net.IP(opt.Data)
		}
	}
	return nil
}

// isOfferFor returns the index into chaddrs of the client hardware address
// a DHCPv4 OFFER in data from a MAC other than leg is addressed to (RFC
// 2131 section 3.1 step 2).
func isOfferFor(data []byte, leg net.HardwareAddr, chaddrs []net.HardwareAddr) (int, bool) {
	eth, dhcp := dhcpv4Of(data)
	if eth == nil || bytes.Equal(eth.SrcMAC, leg) {
		return 0, false
	}
	if dhcp.Operation != layers.DHCPOpReply || dhcpv4MessageType(dhcp) != layers.DHCPMsgTypeOffer {
		return 0, false
	}
	for i, c := range chaddrs {
		if bytes.Equal(dhcp.ClientHWAddr, c) {
			return i, true
		}
	}
	return 0, false
}

// isDiscoverFrom returns the DHCPv4 DISCOVER in data when it comes from
// the victim's MAC, the message an OFFER answers by its xid (RFC 2131
// section 4.4.1).
func isDiscoverFrom(data []byte, victim net.HardwareAddr) (*layers.DHCPv4, bool) {
	eth, dhcp := dhcpv4Of(data)
	if eth == nil || !bytes.Equal(eth.SrcMAC, victim) {
		return nil, false
	}
	if dhcp.Operation != layers.DHCPOpRequest || dhcpv4MessageType(dhcp) != layers.DHCPMsgTypeDiscover {
		return nil, false
	}
	return dhcp, true
}

// isRequestNaming returns the DHCPv4 REQUEST in data from the victim's MAC
// with the server identifier it names, the server the client selected
// among the offers (RFC 2131 section 4.4.1).
func isRequestNaming(data []byte, victim net.HardwareAddr) (*layers.DHCPv4, net.IP, bool) {
	eth, dhcp := dhcpv4Of(data)
	if eth == nil || !bytes.Equal(eth.SrcMAC, victim) {
		return nil, nil, false
	}
	if dhcp.Operation != layers.DHCPOpRequest || dhcpv4MessageType(dhcp) != layers.DHCPMsgTypeRequest {
		return nil, nil, false
	}
	return dhcp, dhcpv4ServerID(dhcp), true
}

// isRequest6Naming returns the DHCPv6 REQUEST in data from the victim's
// MAC with the server identifier (DUID) it names, the server whose
// ADVERTISE the client chose (RFC 8415 sections 16.4 and 18.2.2).
func isRequest6Naming(data []byte, victim net.HardwareAddr) (*layers.DHCPv6, []byte, bool) {
	pkt := decodeFrame(data)
	eth := ethernetOf(pkt)
	dhcp, _ := pkt.Layer(layers.LayerTypeDHCPv6).(*layers.DHCPv6)
	if eth == nil || dhcp == nil || !bytes.Equal(eth.SrcMAC, victim) || dhcp.MsgType != layers.DHCPv6MsgTypeRequest {
		return nil, nil, false
	}
	for _, opt := range dhcp.Options {
		if opt.Code == layers.DHCPv6OptServerID {
			return dhcp, opt.Data, true
		}
	}
	return dhcp, nil, true
}

// isGroupTraffic reports whether data is an IPv6 packet to group from a
// MAC other than leg.
func isGroupTraffic(data []byte, leg net.HardwareAddr, group net.IP) bool {
	pkt := decodeFrame(data)
	eth := ethernetOf(pkt)
	ip, _ := pkt.Layer(layers.LayerTypeIPv6).(*layers.IPv6)
	return eth != nil && ip != nil && !bytes.Equal(eth.SrcMAC, leg) && ip.DstIP.Equal(group)
}

// isAdvertisementFor reports whether data is a Neighbor Advertisement
// whose target is addr, the defense a host that holds the address sends
// (RFC 4862 section 5.4.4).
func isAdvertisementFor(data []byte, addr net.IP) bool {
	pkt := decodeFrame(data)
	na, _ := pkt.Layer(layers.LayerTypeICMPv6NeighborAdvertisement).(*layers.ICMPv6NeighborAdvertisement)
	return na != nil && na.TargetAddress.Equal(addr)
}
