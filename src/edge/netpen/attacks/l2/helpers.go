// helpers.go holds shared constants and frame-craft helpers used across
// L2 behaviors: well-known multicast MACs, and the LLC/SNAP envelope builder
// for Cisco-proprietary protocols (DTP, VTP, PAgP). The fixture source MAC
// lives in the shared [craft] package.

package l2

import (
	"net"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/internal/craft"
)

// Well-known L2 multicast destinations.
var (
	dtpDstMAC    = net.HardwareAddr{0x01, 0x00, 0x0c, 0xcc, 0xcc, 0xcc}
	vtpDstMAC    = net.HardwareAddr{0x01, 0x00, 0x0c, 0xcc, 0xcc, 0xcc}
	pagpDstMAC   = net.HardwareAddr{0x01, 0x00, 0x0c, 0xcc, 0xcc, 0xcc}
	mvrpDstMAC   = net.HardwareAddr{0x01, 0x80, 0xc2, 0x00, 0x00, 0x21}
	lacpDstMAC   = net.HardwareAddr{0x01, 0x80, 0xc2, 0x00, 0x00, 0x02}
	stpDstMAC    = net.HardwareAddr{0x01, 0x80, 0xc2, 0x00, 0x00, 0x00}
	broadcastMAC = net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	ciscoOUI     = []byte{0x00, 0x00, 0x0C}
	partnerMAC   = net.HardwareAddr{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee}
)

// SNAP PID constants for Cisco-proprietary protocols.
const (
	snapPIDDTP  uint16 = 0x2004
	snapPIDVTP  uint16 = 0x2003
	snapPIDPAgP uint16 = 0x0104
)

// EtherType constants for directly-Ethernet-encapsulated protocols.
const (
	ethertypeLACP uint16 = 0x8809
	ethertypeMVRP uint16 = 0x88F5
)

// craftDot3SNAP builds a full 802.3/LLC/SNAP frame for Cisco-proprietary
// protocols (DTP, VTP, PAgP). The Ethernet Length field is set correctly
// by SerializeLayers with FixLengths.
func craftDot3SNAP(dst, src net.HardwareAddr, snapPID uint16, protoLayer gopacket.SerializableLayer) ([]byte, error) {
	eth := &layers.Ethernet{
		DstMAC:       dst,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeLLC,
	}
	llc := &layers.LLC{
		DSAP:    0xAA,
		SSAP:    0xAA,
		Control: 3,
	}
	snap := &layers.SNAP{
		OrganizationalCode: ciscoOUI,
		Type:               layers.EthernetType(snapPID),
	}
	return craft.Default(eth, llc, snap, protoLayer)
}

// craftEtherType builds a full Ethernet frame with the given EtherType.
func craftEtherType(dst, src net.HardwareAddr, etherType uint16, protoLayer gopacket.SerializableLayer) ([]byte, error) {
	eth := &layers.Ethernet{
		DstMAC:       dst,
		SrcMAC:       src,
		EthernetType: layers.EthernetType(etherType),
	}
	return craft.Default(eth, protoLayer)
}
