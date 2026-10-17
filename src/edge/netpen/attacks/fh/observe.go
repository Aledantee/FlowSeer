// observe.go holds the predicates the first-hop checks read the wire with
// and the echo probe four cache-poisoning checks share. Every predicate
// decodes with gopacket or the owned layer and returns false for a frame it
// cannot read, since the frames come from the segment.

package fh

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/internal/craft"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	nl "go.aledante.io/FlowSeer/src/edge/netpen/layers"
)

// echoID is the ICMP echo identifier the probes carry, so a reply to a
// probe is told from a reply to the host's own ping.
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

// ipv4Of returns the frame's IPv4 header, or nil when it has none.
func ipv4Of(pkt gopacket.Packet) *layers.IPv4 {
	ip, _ := pkt.Layer(layers.LayerTypeIPv4).(*layers.IPv4)
	return ip
}

// isEchoReplyTo reports whether data is an ICMP echo reply from the peer's
// MAC, carrying the probe's identifier, whose IPv4 destination is the
// spoofed address, and returns the Ethernet destination the peer sent it
// to. The destination is the verdict: the attack leg's MAC means the
// peer's cache resolves the spoofed address to the attacker.
func isEchoReplyTo(data []byte, peer net.HardwareAddr, spoofed net.IP) (net.HardwareAddr, bool) {
	pkt := decodeFrame(data)
	eth, ip := ethernetOf(pkt), ipv4Of(pkt)
	if eth == nil || ip == nil || !bytes.Equal(eth.SrcMAC, peer) {
		return nil, false
	}
	icmp, _ := pkt.Layer(layers.LayerTypeICMPv4).(*layers.ICMPv4)
	if icmp == nil || icmp.TypeCode.Type() != layers.ICMPv4TypeEchoReply || icmp.Id != echoID {
		return nil, false
	}
	if !ip.DstIP.Equal(spoofed) {
		return nil, false
	}
	return eth.DstMAC, true
}

// isARPRequestFor reports whether data is an ARP request from the peer's
// MAC asking who has ip, the question a peer asks once it distrusts or has
// lost its entry for the address.
func isARPRequestFor(data []byte, peer net.HardwareAddr, ip net.IP) bool {
	pkt := decodeFrame(data)
	eth := ethernetOf(pkt)
	arp, _ := pkt.Layer(layers.LayerTypeARP).(*layers.ARP)
	if eth == nil || arp == nil || !bytes.Equal(eth.SrcMAC, peer) {
		return false
	}
	return arp.Operation == layers.ARPRequest && net.IP(arp.DstProtAddress).Equal(ip)
}

// isHSRPFor returns the HSRP message in data when it is for group and comes
// from a MAC other than leg, with that sender. Any opcode qualifies, since
// a Resign is the evidence a coup wants.
func isHSRPFor(data []byte, group uint8, leg net.HardwareAddr) (*nl.HSRP, net.HardwareAddr, bool) {
	pkt := decodeFrame(data)
	eth := ethernetOf(pkt)
	msg, _ := pkt.Layer(nl.LayerTypeHSRP).(*nl.HSRP)
	if eth == nil || msg == nil || bytes.Equal(eth.SrcMAC, leg) || msg.Group != group {
		return nil, nil, false
	}
	return msg, eth.SrcMAC, true
}

// isGLBPHelloFor returns the hello TLV of the GLBP message in data when it
// is for group and comes from a MAC other than leg, with that sender.
func isGLBPHelloFor(data []byte, group uint16, leg net.HardwareAddr) (*nl.GLBPHello, net.HardwareAddr, bool) {
	pkt := decodeFrame(data)
	eth := ethernetOf(pkt)
	msg, _ := pkt.Layer(nl.LayerTypeGLBP).(*nl.GLBP)
	if eth == nil || msg == nil || bytes.Equal(eth.SrcMAC, leg) || msg.Group != group {
		return nil, nil, false
	}
	for _, tlv := range msg.TLVs {
		if tlv.Hello != nil {
			return tlv.Hello, eth.SrcMAC, true
		}
	}
	return nil, nil, false
}

// isVRRPAdvertFor returns the VRRPv2 advertisement in data when it is for
// vrid and comes from a MAC other than leg, with that sender.
func isVRRPAdvertFor(data []byte, vrid uint8, leg net.HardwareAddr) (*layers.VRRPv2, net.HardwareAddr, bool) {
	pkt := decodeFrame(data)
	eth := ethernetOf(pkt)
	adv, _ := pkt.Layer(layers.LayerTypeVRRP).(*layers.VRRPv2)
	if eth == nil || adv == nil || bytes.Equal(eth.SrcMAC, leg) || adv.VirtualRtrID != vrid {
		return nil, nil, false
	}
	return adv, eth.SrcMAC, true
}

// llmnrQuery is what the answer to an LLMNR query copies from it: the
// sender's addresses and port, the transaction id, and the queried name.
type llmnrQuery struct {
	srcMAC  net.HardwareAddr
	srcIP   net.IP
	srcPort layers.UDPPort
	id      uint16
	name    string
}

// isLLMNRQueryFrom returns the LLMNR query in data when it comes from the
// victim's MAC to the LLMNR group on port 5355 with the QR bit clear and
// one question. RFC 4795 section 2.1.1 defines the header.
func isLLMNRQueryFrom(data []byte, victim net.HardwareAddr) (llmnrQuery, bool) {
	pkt := decodeFrame(data)
	eth, ip := ethernetOf(pkt), ipv4Of(pkt)
	udp, _ := pkt.Layer(layers.LayerTypeUDP).(*layers.UDP)
	msg, _ := pkt.Layer(nl.LayerTypeLLMNR).(*nl.LLMNR)
	if eth == nil || ip == nil || udp == nil || msg == nil {
		return llmnrQuery{}, false
	}
	if !bytes.Equal(eth.SrcMAC, victim) || !ip.DstIP.Equal(llmnrGroupIP) || udp.DstPort != 5355 {
		return llmnrQuery{}, false
	}
	if msg.QR() || len(msg.Questions) != 1 {
		return llmnrQuery{}, false
	}
	return llmnrQuery{
		srcMAC:  eth.SrcMAC,
		srcIP:   ip.SrcIP,
		srcPort: udp.SrcPort,
		id:      msg.ID,
		name:    msg.Questions[0].Name,
	}, true
}

// craftEchoRequest builds an ICMP echo request from srcIP to dstIP, sent
// to dstMAC from the attack leg's MAC. The addresses are the probe: the
// reply's Ethernet destination says which MAC the peer's cache holds for
// srcIP.
func craftEchoRequest(leg, dstMAC net.HardwareAddr, srcIP, dstIP net.IP) ([]byte, error) {
	eth := &layers.Ethernet{
		DstMAC:       dstMAC,
		SrcMAC:       leg,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    srcIP.To4(),
		DstIP:    dstIP.To4(),
	}
	icmp := &layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
		Id:       echoID,
		Seq:      1,
	}
	return craft.Default(eth, ip, icmp, gopacket.Payload([]byte("netpen")))
}

// cacheProbe is one echo probe of a poisoned cache: the peer whose cache
// the check poisoned, the spoofed address the probe is sent from, and the
// peer's name for the details.
type cacheProbe struct {
	peer     check.Host
	peerName string
	spoofed  net.IP
}

// observeCacheProbe sends the echo request and reads the attack leg for
// the peer's answer within deps.Observe. A reply addressed to the attack
// leg's MAC is confirmed with detail; a reply to another MAC or an ARP
// request for the spoofed address is resisted; silence is pending.
func observeCacheProbe(ctx context.Context, deps check.Deps, p cacheProbe, detail string) (check.Verdict, error) {
	leg := deps.AttackLeg.Addr().MAC
	probe, err := craftEchoRequest(leg, p.peer.MAC, p.spoofed, p.peer.IP)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "craft echo probe")
	}
	if err := deps.AttackLeg.Send(ctx, probe); err != nil {
		return check.Verdict{}, errs.Wrap(err, "send echo probe")
	}
	var repliedTo net.HardwareAddr
	asked := false
	found, obs, err := check.First(ctx, deps.AttackLeg, deps.Observe, func(data []byte) bool {
		if to, ok := isEchoReplyTo(data, p.peer.MAC, p.spoofed); ok {
			repliedTo = to
			return true
		}
		if isARPRequestFor(data, p.peer.MAC, p.spoofed) {
			asked = true
			return true
		}
		return false
	})
	if err != nil {
		if ctx.Err() != nil {
			return check.Verdict{}, ctx.Err()
		}
		return check.Verdict{}, errs.Wrap(err, "receive echo reply")
	}
	switch {
	case found == nil:
		return check.Pending(fmt.Sprintf("no reply from the %s in %s", p.peerName, obs.Elapsed.Round(time.Millisecond))), nil
	case asked:
		return check.Resisted(fmt.Sprintf("the %s asked who-has %s", p.peerName, p.spoofed), obs.Elapsed), nil
	case bytes.Equal(repliedTo, leg):
		return check.Confirmed(detail, obs.Elapsed), nil
	}
	return check.Resisted(fmt.Sprintf("the %s sent its reply for %s to %s", p.peerName, p.spoofed, repliedTo), obs.Elapsed), nil
}
