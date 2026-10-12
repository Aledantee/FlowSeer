// Package routing holds netpen's routing-injection and rogue-WPAD attack
// behaviors: OSPF LSA injection, EIGRP route injection, and the rogue WPAD
// proxy. [Behaviors] lists them for the shipped catalog, and the runner supplies
// the legs, the target, the emitter, the teardown handle, and the listener.
// Each behavior reads the router identity or route it injects from the
// target and its own addresses from the attack leg, and a value that only has
// to be well-formed, such as an area, a sequence number, or an autonomous
// system number, stays a constant in the behavior.
//
// OSPF uses the gopacket fork's OSPF types for decode where they exist,
// raw craft for the PDU types the fork does not serialize.
// EIGRP uses the owned [nl.EIGRP] layer. WPAD combines the owned
// [nl.NBNS] and [nl.LLMNR] layers with a minimal embedded TCP proxy.
package routing

import (
	"net"
)

// Well-known multicast destinations.
var (
	// ospfAllSPFRouters is the OSPF AllSPFRouters multicast: 224.0.0.5.
	ospfAllSPFRouters = net.IPv4(224, 0, 0, 5)
	// eigrpMulticast is the EIGRP multicast: 224.0.0.10.
	eigrpMulticast = net.IPv4(224, 0, 0, 10)
	// ospfMulticastMAC is the L2 multicast for 224.0.0.5.
	ospfMulticastMAC = net.HardwareAddr{0x01, 0x00, 0x5e, 0x00, 0x00, 0x05}
	// eigrpMulticastMAC is the L2 multicast for 224.0.0.10.
	eigrpMulticastMAC = net.HardwareAddr{0x01, 0x00, 0x5e, 0x00, 0x00, 0x0a}
	// broadcastMAC is ff:ff:ff:ff:ff:ff (NBT-NS broadcast).
	broadcastMAC = net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
)
