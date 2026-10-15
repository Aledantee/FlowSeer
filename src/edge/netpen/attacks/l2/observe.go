// observe.go holds the predicates the L2 checks read the wire with and the
// echo probe the cache-poisoning check shares with the first-hop family.
// Every predicate decodes with gopacket or the owned layer and returns
// false for a frame it cannot read, since the frames come from the
// segment.

package l2

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

// echoID is the ICMP echo identifier the probe carries, so a reply to the
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

// fromAnother returns the frame's Ethernet header when its source is a
// MAC other than leg, the attribution every peer predicate rests on.
func fromAnother(pkt gopacket.Packet, leg net.HardwareAddr) *layers.Ethernet {
	eth := ethernetOf(pkt)
	if eth == nil || bytes.Equal(eth.SrcMAC, leg) {
		return nil
	}
	return eth
}

// isBPDUFrom returns the configuration BPDU in data when it comes from a
// MAC other than leg.
func isBPDUFrom(data []byte, leg net.HardwareAddr) (*layers.STP, bool) {
	pkt := decodeFrame(data)
	if fromAnother(pkt, leg) == nil {
		return nil, false
	}
	bpdu, _ := pkt.Layer(layers.LayerTypeSTP).(*layers.STP)
	if bpdu == nil || bpdu.Type != 0 {
		return nil, false
	}
	return bpdu, true
}

// rootIsLeg reports whether the BPDU names a root identifier with priority
// 0 and the attack leg's MAC, the identity the probe claims.
func rootIsLeg(bpdu *layers.STP, leg net.HardwareAddr) bool {
	return bpdu.RouteID.Priority == 0 && bytes.Equal(bpdu.RouteID.HwAddr, leg)
}

// rootString renders a root identifier as the priority word and MAC.
func rootString(id layers.STPSwitchID) string {
	return fmt.Sprintf("%d/%s", id.Priority|id.SysID, id.HwAddr)
}

// isDTPFrom returns the DTP message in data when it comes from a MAC other
// than leg.
func isDTPFrom(data []byte, leg net.HardwareAddr) (*nl.DTP, bool) {
	pkt := decodeFrame(data)
	if fromAnother(pkt, leg) == nil {
		return nil, false
	}
	msg, _ := pkt.Layer(nl.LayerTypeDTP).(*nl.DTP)
	if msg == nil {
		return nil, false
	}
	return msg, true
}

// dtpTrunking reports whether a trunk status byte has the bit the owned
// layer's trunk value 0x81 carries beyond the access, desirable, and auto
// values, read as the port trunking. Cisco's format is not vendored, so
// the reading rests on that constant alone.
func dtpTrunking(status uint8) bool {
	return status&0x80 != 0
}

// dtpStatusName names a trunk status byte for a detail.
func dtpStatusName(status uint8) string {
	switch status {
	case nl.DTPStatusAccess:
		return "access (0x02)"
	case nl.DTPStatusDesirable:
		return "desirable (0x03)"
	case nl.DTPStatusAuto:
		return "auto (0x04)"
	}
	if dtpTrunking(status) {
		return fmt.Sprintf("trunk (0x%02x)", status)
	}
	return fmt.Sprintf("unknown (0x%02x)", status)
}

// isVTPSummaryFrom returns the VTP summary advertisement in data when it
// comes from a MAC other than leg and names domain.
func isVTPSummaryFrom(data []byte, leg net.HardwareAddr, domain string) (*nl.VTP, bool) {
	pkt := decodeFrame(data)
	if fromAnother(pkt, leg) == nil {
		return nil, false
	}
	msg, _ := pkt.Layer(nl.LayerTypeVTP).(*nl.VTP)
	if msg == nil || msg.Code != nl.VTPCodeSummary || msg.Domain != domain {
		return nil, false
	}
	return msg, true
}

// isLACPFrom returns the LACPDU in data when it comes from a MAC other
// than leg.
func isLACPFrom(data []byte, leg net.HardwareAddr) (*nl.LACP, bool) {
	pkt := decodeFrame(data)
	if fromAnother(pkt, leg) == nil {
		return nil, false
	}
	msg, _ := pkt.Layer(nl.LayerTypeLACP).(*nl.LACP)
	if msg == nil {
		return nil, false
	}
	return msg, true
}

// isPAgPFrom returns the PAgP message in data when it comes from a MAC
// other than leg.
func isPAgPFrom(data []byte, leg net.HardwareAddr) (*nl.PAgP, bool) {
	pkt := decodeFrame(data)
	if fromAnother(pkt, leg) == nil {
		return nil, false
	}
	msg, _ := pkt.Layer(nl.LayerTypePAgP).(*nl.PAgP)
	if msg == nil {
		return nil, false
	}
	return msg, true
}

// isMVRPJoinFor reports whether data is an MVRP frame from a MAC other
// than leg that registers one of vids with a JoinIn event, and returns
// that id. The owned layer defines no plain In event, so JoinIn is the one
// registration event the predicate reads.
func isMVRPJoinFor(data []byte, leg net.HardwareAddr, vids []uint16) (uint16, bool) {
	pkt := decodeFrame(data)
	if fromAnother(pkt, leg) == nil {
		return 0, false
	}
	msg, _ := pkt.Layer(nl.LayerTypeMVRP).(*nl.MVRP)
	if msg == nil {
		return 0, false
	}
	for _, m := range msg.Messages {
		if m.AttributeType != nl.MVRPAttrTypeVID {
			continue
		}
		for i, ev := range m.Events {
			if ev != nl.MVRPEventJoinIn {
				continue
			}
			vid := m.FirstValue + uint16(i)
			for _, want := range vids {
				if vid == want {
					return vid, true
				}
			}
		}
	}
	return 0, false
}

// isMEDPolicyFor reports whether data is an LLDPDU from a MAC other than
// leg carrying an LLDP-MED network policy TLV whose VLAN id is vlan, read
// through gopacket's LLDP-MED decoder.
func isMEDPolicyFor(data []byte, leg net.HardwareAddr, vlan uint16) bool {
	pkt := decodeFrame(data)
	if fromAnother(pkt, leg) == nil {
		return false
	}
	info, _ := pkt.Layer(layers.LayerTypeLinkLayerDiscoveryInfo).(*layers.LinkLayerDiscoveryInfo)
	if info == nil {
		return false
	}
	media, err := info.DecodeMedia()
	if err != nil {
		return false
	}
	return media.NetworkPolicy.VLANId == vlan
}

// isHopEcho reports whether data is the double-tagged check's echo request
// as it leaves the trunk: from the attack leg's MAC, IPv4 10.0.0.1 to
// 10.0.0.2, an ICMP echo request, tagged with the inner VLAN or untagged.
func isHopEcho(data []byte, leg net.HardwareAddr, inner uint16) bool {
	pkt := decodeFrame(data)
	eth := ethernetOf(pkt)
	if eth == nil || !bytes.Equal(eth.SrcMAC, leg) {
		return false
	}
	if tag, ok := pkt.Layer(layers.LayerTypeDot1Q).(*layers.Dot1Q); ok && tag.VLANIdentifier != inner {
		return false
	}
	ip, _ := pkt.Layer(layers.LayerTypeIPv4).(*layers.IPv4)
	icmp, _ := pkt.Layer(layers.LayerTypeICMPv4).(*layers.ICMPv4)
	if ip == nil || icmp == nil {
		return false
	}
	return ip.SrcIP.Equal(hopSrcIP) && ip.DstIP.Equal(hopDstIP) && icmp.TypeCode.Type() == layers.ICMPv4TypeEchoRequest
}

// isFloodProbe reports whether data is the CAM flood's unicast probe: from
// the attack leg's MAC to the victim's MAC with the sentinel payload. A
// switch that forwards it to the watch leg's port, which does not own the
// victim's address, has lost the address to the flood.
func isFloodProbe(data []byte, leg, victim net.HardwareAddr) bool {
	pkt := decodeFrame(data)
	eth := ethernetOf(pkt)
	if eth == nil || !bytes.Equal(eth.SrcMAC, leg) || !bytes.Equal(eth.DstMAC, victim) {
		return false
	}
	return bytes.Contains(data, camFloodSentinel)
}

// isEchoReplyTo reports whether data is an ICMP echo reply from the peer's
// MAC, carrying the probe's identifier, whose IPv4 destination is the
// spoofed address, and returns the Ethernet destination the peer sent it
// to.
func isEchoReplyTo(data []byte, peer net.HardwareAddr, spoofed net.IP) (net.HardwareAddr, bool) {
	pkt := decodeFrame(data)
	eth := ethernetOf(pkt)
	ip, _ := pkt.Layer(layers.LayerTypeIPv4).(*layers.IPv4)
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
// MAC asking who has ip.
func isARPRequestFor(data []byte, peer net.HardwareAddr, ip net.IP) bool {
	pkt := decodeFrame(data)
	eth := ethernetOf(pkt)
	arp, _ := pkt.Layer(layers.LayerTypeARP).(*layers.ARP)
	if eth == nil || arp == nil || !bytes.Equal(eth.SrcMAC, peer) {
		return false
	}
	return arp.Operation == layers.ARPRequest && net.IP(arp.DstProtAddress).Equal(ip)
}

// craftEchoRequest builds an ICMP echo request from srcIP to dstIP, sent
// to dstMAC from the attack leg's MAC.
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

// observeCacheProbe sends an echo request to the gateway from the spoofed
// address and reads the attack leg for the gateway's answer within
// deps.Observe. A reply addressed to the attack leg's MAC is confirmed with
// detail; a reply to another MAC or an ARP request for the spoofed address
// is resisted; silence is pending.
func observeCacheProbe(ctx context.Context, deps check.Deps, gateway check.Host, spoofed net.IP, detail string) (check.Verdict, error) {
	leg := deps.AttackLeg.Addr().MAC
	probe, err := craftEchoRequest(leg, gateway.MAC, spoofed, gateway.IP)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "craft echo probe")
	}
	if err := deps.AttackLeg.Send(ctx, probe); err != nil {
		return check.Verdict{}, errs.Wrap(err, "send echo probe")
	}
	var repliedTo net.HardwareAddr
	asked := false
	found, obs, err := check.First(ctx, deps.AttackLeg, deps.Observe, func(data []byte) bool {
		if to, ok := isEchoReplyTo(data, gateway.MAC, spoofed); ok {
			repliedTo = to
			return true
		}
		if isARPRequestFor(data, gateway.MAC, spoofed) {
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
		return check.Pending(fmt.Sprintf("no reply from the gateway in %s", obs.Elapsed.Round(time.Millisecond))), nil
	case asked:
		return check.Resisted(fmt.Sprintf("the gateway asked who-has %s", spoofed), obs.Elapsed), nil
	case bytes.Equal(repliedTo, leg):
		return check.Confirmed(detail, obs.Elapsed), nil
	}
	return check.Resisted(fmt.Sprintf("the gateway sent its reply for %s to %s", spoofed, repliedTo), obs.Elapsed), nil
}

// observeWatch reads the watch leg for the frame match accepts within
// deps.Observe and returns confirmed with confirmedDetail, resisted with
// resistedDetail, or pending when the run has no watch leg, since the
// observable lives on the other side of the switch.
func observeWatch(ctx context.Context, deps check.Deps, match func([]byte) bool, confirmedDetail, resistedDetail string) (check.Verdict, error) {
	if deps.WatchLeg == nil {
		return check.Pending("no watch leg: the observable is on the other side of the switch, pass -w <iface>"), nil
	}
	found, obs, err := check.First(ctx, deps.WatchLeg, deps.Observe, match)
	if err != nil {
		if ctx.Err() != nil {
			return check.Verdict{}, ctx.Err()
		}
		return check.Verdict{}, errs.Wrap(err, "receive watch frame")
	}
	if found != nil {
		return check.Confirmed(confirmedDetail, obs.Elapsed), nil
	}
	return check.Resisted(fmt.Sprintf("%s in %s", resistedDetail, obs.Elapsed.Round(time.Millisecond)), obs.Elapsed), nil
}
