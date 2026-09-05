// Package routing holds netpen's routing-injection and rogue-WPAD attack
// behaviors: OSPF LSA injection, EIGRP route injection, and the rogue WPAD
// proxy. Each behavior is a thin [runner.Behavior] over the protocol
// toolkit — leg sends, decoder reads, findings emission — with durability
// duties sourced from the catalog, not per-command code.
//
// OSPF uses the gopacket fork's OSPF types for decode where they exist,
// raw craft for the PDU types the fork does not serialize.
// EIGRP uses the owned [nl.EIGRP] layer. WPAD combines the owned
// [nl.NBNS] and [nl.LLMNR] layers with a minimal embedded TCP proxy.
package routing

import (
	"net"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/internal/craft"
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

// Fixture target addresses (matching the harvest script's constants).
var (
	// attackerRouterID is the OSPF router ID we inject as.
	attackerRouterID uint32 = 0x0a000099 // 10.0.0.99
	// attackerIP is the source IP for L3 frames.
	attackerIP = net.IPv4(10, 0, 0, 99)
)

// srcMAC returns the source MAC for the behavior: the fixture MAC.
// In-memory behaviors have no real interface to derive a MAC from.
func srcMAC() net.HardwareAddr {
	return craft.FixtureSrcMAC
}
