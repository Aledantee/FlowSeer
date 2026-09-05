// Package fh provides first-hop and identity behaviors with fixed fixture
// addresses and bounded send counts. Use [Behaviors] with runner.Options so
// the runner supplies the required legs, emitter, and teardown handles and
// applies catalog safety gates. Direct Run calls require those same handles.
// The behaviors do not configure host IP forwarding or discover live targets.
//
// ARP-class behaviors (arpsweep, arpspoof, gratarp) are L2-only frames
// (Ethernet → ARP). L3 behaviors (hsrp, vrrp, icmpredirect, llmnr, glbp)
// use the default craft path (SerializeLayers+ComputeChecksums+FixLengths)
// with SetNetworkLayerForChecksum. Ghost uses crafted L2 frames
// with reserved group MACs.
package fh

import (
	"net"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/internal/craft"
)

// Well-known multicast destinations.
var (
	broadcastMAC = net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	vrrpDstMAC   = net.HardwareAddr{0x01, 0x00, 0x5e, 0x00, 0x00, 0x12}
	hsrpDstMAC   = net.HardwareAddr{0x01, 0x00, 0x5e, 0x00, 0x00, 0x02}
	glbpDstMAC   = net.HardwareAddr{0x01, 0x00, 0x5e, 0x00, 0x00, 0x66}
	lldpDstMAC   = net.HardwareAddr{0x01, 0x80, 0xc2, 0x00, 0x00, 0x0e} // LLDP nearest bridge
	stpDstMAC    = net.HardwareAddr{0x01, 0x80, 0xc2, 0x00, 0x00, 0x00}
)

// GhostSA is the reserved group MAC used as the source address in ghost
// frames — an 802.3 §3.2.6 violation (reserved group MAC as source) that
// conformant silicon should filter but non-conformant silicon forwards.
// Callers must preserve its six-byte length and must not mutate it while
// a behavior is running.
var GhostSA = net.HardwareAddr{0x01, 0x80, 0xc2, 0x00, 0x00, 0x01}

// Fixture target addresses (matching the harvest script's constants).
var (
	victimMAC = net.HardwareAddr{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}
	gwMAC     = net.HardwareAddr{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xfe}
	victimIP  = net.IPv4(172, 16, 0, 10)
	gwIP      = net.IPv4(172, 16, 0, 1)
)

// craftL3Payload builds Ethernet → IPv4 → payload (no transport layer)
// with proper checksums.
func craftL3Payload(eth *layers.Ethernet, ip *layers.IPv4, payload []byte) ([]byte, error) {
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		ComputeChecksums: true,
		FixLengths:       true,
	}
	if err := gopacket.SerializeLayers(buf, opts, eth, ip, gopacket.Payload(payload)); err != nil {
		return nil, err
	}
	return append([]byte(nil), buf.Bytes()...), nil
}

// craftUDPLayer builds Ethernet → IPv4 → UDP → layer with proper checksums.
// The UDP layer's SetNetworkLayerForChecksum is called so the UDP checksum
// includes the IP pseudo-header. The final layer must implement
// gopacket.SerializableLayer (e.g. HSRP, GLBP).
func craftUDPLayer(eth *layers.Ethernet, ip *layers.IPv4, udp *layers.UDP, layer gopacket.SerializableLayer) ([]byte, error) {
	_ = udp.SetNetworkLayerForChecksum(ip)
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		ComputeChecksums: true,
		FixLengths:       true,
	}
	if err := gopacket.SerializeLayers(buf, opts, eth, ip, udp, layer); err != nil {
		return nil, err
	}
	return append([]byte(nil), buf.Bytes()...), nil
}

func srcMAC() net.HardwareAddr {
	return craft.FixtureSrcMAC
}
