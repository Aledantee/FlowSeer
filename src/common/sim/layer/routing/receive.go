package routing

import (
	"net/netip"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/arp"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/ndp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

// Receive processes ARP and Neighbor Discovery addressed to iface, ignoring
// malformed frames and frames for another receiver. Replies are returned as
// emissions. Resolved held frames leave through Advance and DrainExits.
// Receive also queues timed-out and evicted exits for DrainExits when it
// retires an exhausted entry.
// NeighborDisabled answers requests without changing the neighbor table.
// Receive is not safe for concurrent use.
func (l *Layer) Receive(now time.Time, ifaceName string, frame ethernet.Frame) layer.Effects {
	vrf, ok := l.ifaceVRF[ifaceName]
	if !ok {
		return layer.Effects{}
	}
	iface := l.ifaces[ifaceName]
	vs := l.vrfs[vrf]
	switch frame.EtherType {
	case ethernet.EtherTypeARP:
		if frame.Dst != iface.MAC && frame.Dst != (netaddr.MAC{255, 255, 255, 255, 255, 255}) {
			return layer.Effects{}
		}
		msg, err := arp.Decode(frame)
		if err != nil {
			return layer.Effects{}
		}
		local := interfaceHolds(iface, msg.TargetAddr)
		if vs.policy.Mode != NeighborDisabled && !msg.SenderAddr.IsUnspecified() {
			key := neighborKey{iface: ifaceName, addr: msg.SenderAddr}
			l.retireNeighbor(vs, now, key, true)
			if _, exists := vs.neighbors[key]; !exists && local {
				vs.neighbors[key] = &neighborEntry{mac: msg.SenderMAC, state: NeighborStale, origin: observed}
			} else {
				l.observe(now, advertisement{
					Interface: ifaceName, Addr: msg.SenderAddr, MAC: msg.SenderMAC,
					HasMAC: true, Solicited: msg.Operation == arp.Reply, Override: true,
				})
			}
		}
		if !local || msg.Operation != arp.Request {
			return layer.Effects{}
		}
		reply, err := arp.Encode(arp.Message{
			HardwareType: 1, ProtocolType: 0x0800,
			Operation: arp.Reply, SenderMAC: iface.MAC, SenderAddr: msg.TargetAddr,
			TargetMAC: msg.SenderMAC, TargetAddr: msg.SenderAddr,
		}, msg.SenderMAC)
		if err != nil {
			return layer.Effects{}
		}
		return layer.Effects{Emissions: []layer.Emission{{Port: iface.Port, VID: iface.VLAN, Frame: reply}}}
	case ethernet.EtherTypeIPv6:
		if !ndpReceiver(iface, frame.Dst) {
			return layer.Effects{}
		}
		hdr, body, err := ip.Decode(frame.Payload)
		if err != nil {
			return layer.Effects{}
		}
		msg, err := ndp.Decode(hdr, body)
		if err != nil {
			return layer.Effects{}
		}
		if msg.Type == ndp.NeighborAdvertisement {
			if hdr.Dst.IsMulticast() && msg.Solicited {
				return layer.Effects{}
			}
			if vs.policy.Mode != NeighborDisabled {
				l.observe(now, advertisement{
					Interface: ifaceName, Addr: msg.Target, MAC: msg.LinkLayerAddr,
					HasMAC: msg.HasLinkLayerAddr, Solicited: msg.Solicited, Override: msg.Override, Router: msg.Router,
				})
			}
			return layer.Effects{}
		}
		if hdr.Src.IsUnspecified() && !isSolicitedNode(hdr.Dst) {
			return layer.Effects{}
		}
		// RFC 4861 section 7.2.3 discards a non-local target before learning.
		if !interfaceHolds(iface, msg.Target) {
			return layer.Effects{}
		}
		key := neighborKey{iface: ifaceName, addr: hdr.Src}
		if vs.policy.Mode != NeighborDisabled && !hdr.Src.IsUnspecified() {
			l.retireNeighbor(vs, now, key, true)
		}
		if vs.policy.Mode != NeighborDisabled && !hdr.Src.IsUnspecified() && msg.HasLinkLayerAddr {
			entry, exists := vs.neighbors[key]
			if !exists {
				vs.neighbors[key] = &neighborEntry{mac: msg.LinkLayerAddr, state: NeighborStale, origin: observed}
			} else if entry.origin != configured && (entry.state == NeighborIncomplete || entry.mac != msg.LinkLayerAddr) {
				entry.mac = msg.LinkLayerAddr
				entry.state = NeighborStale
				entry.expiry = time.Time{}
			}
		}
		dst, dstMAC := hdr.Src, msg.LinkLayerAddr
		if hdr.Src.IsUnspecified() {
			dst = netip.MustParseAddr("ff02::1")
			dstMAC = netaddr.MAC{0x33, 0x33, 0, 0, 0, 1}
		} else if !msg.HasLinkLayerAddr {
			entry, exists := vs.neighbors[key]
			if !exists || entry.state == NeighborIncomplete {
				return layer.Effects{}
			}
			dstMAC = entry.mac
		}
		answerHeader := ip.Header{Src: msg.Target, Dst: dst, Protocol: 58, HopLimit: 255, V6: &ip.V6{}}
		answer, err := ndp.Encode(answerHeader, ndp.Message{
			Type: ndp.NeighborAdvertisement, Target: msg.Target,
			Router: true, Solicited: !hdr.Src.IsUnspecified(), Override: true, HasLinkLayerAddr: true, LinkLayerAddr: iface.MAC,
		})
		if err != nil {
			return layer.Effects{}
		}
		payload, err := answerHeader.Encode(answer)
		if err != nil {
			return layer.Effects{}
		}
		reply := ethernet.Frame{Src: iface.MAC, Dst: dstMAC, EtherType: ethernet.EtherTypeIPv6, Payload: payload}
		return layer.Effects{Emissions: []layer.Emission{{Port: iface.Port, VID: iface.VLAN, Frame: reply}}}
	default:
		return layer.Effects{}
	}
}

func interfaceHolds(iface Interface, addr netip.Addr) bool {
	for _, prefix := range iface.Prefixes {
		if prefix.Addr() == addr {
			return true
		}
	}
	return false
}

func ndpReceiver(iface Interface, mac netaddr.MAC) bool {
	if mac == iface.MAC || mac == (netaddr.MAC{0x33, 0x33, 0, 0, 0, 1}) {
		return true
	}
	for _, prefix := range iface.Prefixes {
		if prefix.Addr().Is6() {
			a := prefix.Addr().As16()
			if mac == (netaddr.MAC{0x33, 0x33, 0xff, a[13], a[14], a[15]}) {
				return true
			}
		}
	}
	return false
}

func isSolicitedNode(addr netip.Addr) bool {
	return netip.MustParsePrefix("ff02::1:ff00:0/104").Contains(addr)
}
