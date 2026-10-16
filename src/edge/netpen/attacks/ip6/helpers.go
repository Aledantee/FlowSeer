// Package ip6 provides DHCP and IPv6 first-hop behaviors for netpen's runner.
// [Behaviors] lists them for the shipped catalog. The runner supplies the
// legs, the target, and the emitter. Each behavior reads the victim, server,
// prefix, and offered address it acts on from the target and its own
// addresses from the attack leg. A value that only has to be well-formed,
// such as a flood frame's stamped MAC or a lease address, stays a constant
// in the behavior. DHCPv4 shares this package with DHCPv6 because both use
// the same fixture harness and runner contracts.
//
// Each behavior reads the wire for the effect its probe should have, with
// the predicates in observe.go, and returns confirmed or resisted from what
// it saw, or pending with the reason the leg cannot carry the observable.
// The rogue servers answer the query they observe within the window with
// its transaction id, since a client matches an answer to its own query by
// that id. Flood behaviors send five frames.
package ip6

import (
	"fmt"
	"net"

	"go.aledante.io/FlowSeer/src/common/errs"

	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

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
// the EUI-64 format: fe80::XXff:feYY:ZZZZ. A MAC that is not six octets
// is an error carrying [check.ErrCodeTargetInvalid], since the attack leg's
// address is what the behavior frames from and nothing was sent.
func linkLocalFromMAC(mac net.HardwareAddr) (net.IP, error) {
	if len(mac) != 6 {
		text := fmt.Sprintf("the attack leg's MAC %q is not six octets, so no link-local address derives from it", mac)
		return nil, errs.New().
			Code(check.ErrCodeTargetInvalid).
			ExitCode(2).
			UserMsg(text).
			Hint("pass -i <iface> naming an Ethernet interface").
			Msg(text)
	}
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
	return addr, nil
}
