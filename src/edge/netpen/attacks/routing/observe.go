// observe.go holds the predicates the routing checks read the wire with.
// Every predicate decodes with gopacket or the owned layer and returns
// false for a frame it cannot read, since the frames come from the
// segment.

package routing

import (
	"bytes"
	"net"
	"strings"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	nl "go.aledante.io/FlowSeer/src/edge/netpen/layers"
)

// decodeFrame parses one Ethernet frame. The result's layers are nil for
// what the frame does not carry or carries malformed.
func decodeFrame(data []byte) gopacket.Packet {
	return gopacket.NewPacket(data, layers.LayerTypeEthernet, gopacket.Default)
}

// fromAnother returns the frame's Ethernet header when its source is a
// MAC other than leg.
func fromAnother(pkt gopacket.Packet, leg net.HardwareAddr) *layers.Ethernet {
	eth, _ := pkt.Layer(layers.LayerTypeEthernet).(*layers.Ethernet)
	if eth == nil || bytes.Equal(eth.SrcMAC, leg) {
		return nil
	}
	return eth
}

// ospfHello is what the OSPF check reads off a hello: the sender's router
// ID and the neighbors it lists.
type ospfHello struct {
	routerID  uint32
	neighbors []uint32
}

// isOSPFHelloFrom returns the OSPFv2 hello in data when it comes from a
// MAC other than leg, for the backbone area, with the router ID and
// neighbor list the hello carries.
func isOSPFHelloFrom(data []byte, leg net.HardwareAddr) (ospfHello, bool) {
	pkt := decodeFrame(data)
	if fromAnother(pkt, leg) == nil {
		return ospfHello{}, false
	}
	ospf, _ := pkt.Layer(layers.LayerTypeOSPF).(*layers.OSPFv2)
	if ospf == nil || ospf.Type != layers.OSPFHello || ospf.AreaID != 0 {
		return ospfHello{}, false
	}
	hello, ok := ospf.Content.(layers.HelloPkgV2)
	if !ok {
		return ospfHello{}, false
	}
	return ospfHello{routerID: ospf.RouterID, neighbors: hello.NeighborID}, true
}

// isOSPFHelloListing reports whether data is an OSPFv2 hello from the
// router with routerID, from a MAC other than leg, whose neighbor list
// carries listed: the 2-WayReceived event of RFC 2328 section 10.5.
func isOSPFHelloListing(data []byte, leg net.HardwareAddr, routerID, listed uint32) bool {
	hello, ok := isOSPFHelloFrom(data, leg)
	if !ok || hello.routerID != routerID {
		return false
	}
	for _, n := range hello.neighbors {
		if n == listed {
			return true
		}
	}
	return false
}

// eigrpFrom returns the EIGRP message in data, with its IPv4 header, when
// it comes from a MAC other than leg and belongs to the autonomous
// system.
func eigrpFrom(data []byte, leg net.HardwareAddr, as uint16) (*nl.EIGRP, *layers.IPv4, bool) {
	pkt := decodeFrame(data)
	if fromAnother(pkt, leg) == nil {
		return nil, nil, false
	}
	ip, _ := pkt.Layer(layers.LayerTypeIPv4).(*layers.IPv4)
	payload, err := extractIPPayload(data, 88)
	if ip == nil || err != nil {
		return nil, nil, false
	}
	eigrp := &nl.EIGRP{}
	if err := eigrp.DecodeFromBytes(payload, gopacket.NilDecodeFeedback); err != nil || eigrp.AutonomousSystem != as {
		return nil, nil, false
	}
	return eigrp, ip, true
}

// isEIGRPHelloInAS reports whether data is an EIGRP hello in the
// autonomous system from a MAC other than leg.
func isEIGRPHelloInAS(data []byte, leg net.HardwareAddr, as uint16) bool {
	eigrp, _, ok := eigrpFrom(data, leg, as)
	return ok && eigrp.Opcode == nl.EIGRPOpcodeHello
}

// isEIGRPGoodbyeInAS reports whether data is an EIGRP hello in the
// autonomous system from a MAC other than leg that ends the adjacency: it
// carries the Peer Termination TLV (RFC 7868 section 6.7.7), or the older
// form, a Parameters TLV whose K1 to K5 are all 255.
func isEIGRPGoodbyeInAS(data []byte, leg net.HardwareAddr, as uint16) bool {
	eigrp, _, ok := eigrpFrom(data, leg, as)
	if !ok || eigrp.Opcode != nl.EIGRPOpcodeHello {
		return false
	}
	for _, tlv := range eigrp.TLVs {
		switch tlv.Type {
		case eigrpTLVPeerTermination:
			return true
		case eigrpTLVParameters:
			if len(tlv.Value) >= 5 && bytes.Equal(tlv.Value[:5], []byte{255, 255, 255, 255, 255}) {
				return true
			}
		}
	}
	return false
}

// isEIGRPAckTo reports whether data is the target's acknowledgment of the
// null update: an EIGRP hello in the autonomous system from a MAC other
// than leg, sent unicast to legIP, with a non-zero Ack number (RFC 7868
// section 5.2).
func isEIGRPAckTo(data []byte, leg net.HardwareAddr, legIP net.IP, as uint16) bool {
	eigrp, ip, ok := eigrpFrom(data, leg, as)
	return ok && eigrp.Opcode == nl.EIGRPOpcodeHello && eigrp.AckNumber != 0 && ip.DstIP.Equal(legIP)
}

// wpadQuery is what the answer to a name query copies from it: the
// sender's addresses and port, the transaction id, and the protocol the
// query spoke.
type wpadQuery struct {
	srcMAC  net.HardwareAddr
	srcIP   net.IP
	srcPort layers.UDPPort
	id      uint16
	llmnr   bool
}

// isWPADQuery returns the NBT-NS or LLMNR query for the name wpad in data
// when it comes from a MAC other than leg with the QR bit clear and one
// question.
func isWPADQuery(data []byte, leg net.HardwareAddr) (wpadQuery, bool) {
	pkt := decodeFrame(data)
	eth := fromAnother(pkt, leg)
	ip, _ := pkt.Layer(layers.LayerTypeIPv4).(*layers.IPv4)
	udp, _ := pkt.Layer(layers.LayerTypeUDP).(*layers.UDP)
	if eth == nil || ip == nil || udp == nil {
		return wpadQuery{}, false
	}
	q := wpadQuery{srcMAC: eth.SrcMAC, srcIP: ip.SrcIP, srcPort: udp.SrcPort}
	if nbns, ok := pkt.Layer(nl.LayerTypeNBTNS).(*nl.NBNS); ok && udp.DstPort == 137 {
		if nbns.Flags&0x8000 != 0 || len(nbns.Questions) != 1 || !strings.EqualFold(nbns.Questions[0].Name, "wpad") {
			return wpadQuery{}, false
		}
		q.id = nbns.ID
		return q, true
	}
	if llmnr, ok := pkt.Layer(nl.LayerTypeLLMNR).(*nl.LLMNR); ok && udp.DstPort == 5355 {
		if llmnr.QR() || len(llmnr.Questions) != 1 || !strings.EqualFold(llmnr.Questions[0].Name, "wpad") {
			return wpadQuery{}, false
		}
		q.id = llmnr.ID
		q.llmnr = true
		return q, true
	}
	return wpadQuery{}, false
}
