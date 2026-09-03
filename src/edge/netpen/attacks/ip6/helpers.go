// Package ip6 provides DHCP and IPv6 first-hop behaviors for netpen's runner.
// Behaviors use fixed fixture addresses, including [FixtureSrcMAC], and send
// through the supplied legs. DHCPv4 shares this package with DHCPv6 because
// both use the same fixture harness and runner contracts.
//
// Register [Behaviors] in the runner's behavior map. Callers must supply the
// initialized dependencies promised by runner.Deps and must not mutate fixture
// addresses during a run. Flood behaviors send five frames. [RunRogueDHCPv6]
// requires a context deadline to bound an idle receive.
package ip6

import (
	"net"

	"github.com/gopacket/gopacket"
)

// FixtureSrcMAC is the source MAC used by the behaviors and their fixture
// generator. Behaviors do not derive it from the attack leg. Callers must not
// mutate it concurrently with a behavior run.
var FixtureSrcMAC = net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}

// Well-known multicast destinations.
var (
	broadcastMAC = net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	// IPv6 all-nodes multicast (ff02::1).
	allNodesIPv6 = net.ParseIP("ff02::1")
	// IPv6 all-nodes Ethernet multicast MAC (33:33:00:00:00:01).
	allNodesMAC = net.HardwareAddr{0x33, 0x33, 0x00, 0x00, 0x00, 0x01}
	// Solicited-node Ethernet multicast MAC prefix (33:33:ff:XX:XX:XX).
	solicitedMACPrefix = []byte{0x33, 0x33, 0xff}
)

// Fixture target addresses (matching the harvest script's constants).
var (
	victimMAC6 = net.HardwareAddr{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}
	victimIP6  = net.ParseIP("fd00::10")
	serverIP6  = net.ParseIP("fd00::1")
	clientIP6  = net.ParseIP("fd00::100")

	// DHCPv4 targets.
	dhcpClientMAC        = net.HardwareAddr{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}
	dhcpClientIP         = net.IPv4(172, 16, 0, 10)
	dhcpOfferIP          = net.IPv4(172, 16, 0, 100)
	dhcpXid       uint32 = 0x12345678
)

func srcMAC() net.HardwareAddr {
	return FixtureSrcMAC
}

// craftDefault serializes layers with the default craft path:
// SerializeLayers + ComputeChecksums + FixLengths. Used by non-flood
// behaviors.
func craftDefault(serializable ...gopacket.SerializableLayer) ([]byte, error) {
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		ComputeChecksums: true,
		FixLengths:       true,
	}
	if err := gopacket.SerializeLayers(buf, opts, serializable...); err != nil {
		return nil, err
	}
	return append([]byte(nil), buf.Bytes()...), nil
}

// solicitedNodeMAC computes the solicited-node multicast MAC for the
// given IPv6 address: 33:33:ff:<last-3-bytes-of-addr>.
func solicitedNodeMAC(ip net.IP) net.HardwareAddr {
	ip16 := ip.To16()
	return net.HardwareAddr{
		solicitedMACPrefix[0], solicitedMACPrefix[1], solicitedMACPrefix[2],
		ip16[13], ip16[14], ip16[15],
	}
}

// solicitedNodeIP computes the solicited-node multicast IPv6 address
// for the given target: ff02::1:ff<last-3-bytes>.
func solicitedNodeIP(ip net.IP) net.IP {
	ip16 := ip.To16()
	addr := make(net.IP, 16)
	addr[0] = 0xff
	addr[1] = 0x02
	addr[11] = 0x01
	addr[12] = 0xff
	addr[13] = ip16[13]
	addr[14] = ip16[14]
	addr[15] = ip16[15]
	return addr
}

// multicastMACForIPv6 computes the Ethernet multicast MAC for an IPv6
// multicast address: 33:33:<last-4-bytes>.
func multicastMACForIPv6(ip net.IP) net.HardwareAddr {
	ip16 := ip.To16()
	return net.HardwareAddr{
		0x33, 0x33,
		ip16[12], ip16[13], ip16[14], ip16[15],
	}
}

// linkLocalFromMAC derives an IPv6 link-local address from a MAC using
// the EUI-64 format: fe80::XXff:feYY:ZZZZ.
func linkLocalFromMAC(mac net.HardwareAddr) net.IP {
	addr := make(net.IP, 16)
	addr[0] = 0xfe
	addr[1] = 0x80
	// EUI-64: flip U/L bit in first byte, insert ff:fe in the middle.
	addr[8] = mac[0] ^ 0x02
	addr[9] = mac[1]
	addr[10] = mac[2]
	addr[11] = 0xff
	addr[12] = 0xfe
	addr[13] = mac[3]
	addr[14] = mac[4]
	addr[15] = mac[5]
	return addr
}
