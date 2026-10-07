package routing

import (
	"net/netip"

	"go.aledante.io/FlowSeer/src/common/net/arp"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/ndp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

func solicitation(iface Interface, target netip.Addr, cached netaddr.MAC, probe bool) (layer.Emission, bool) {
	var source, first netip.Addr
	bestBits := -1
	for _, prefix := range iface.Prefixes {
		if prefix.Addr().Is4() != target.Is4() {
			continue
		}
		if !first.IsValid() {
			first = prefix.Addr()
		}
		if prefix.Contains(target) && prefix.Bits() > bestBits {
			source = prefix.Addr()
			bestBits = prefix.Bits()
		}
	}
	if !source.IsValid() {
		source = first
	}
	if !source.IsValid() {
		return layer.Emission{}, false
	}

	dst := netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	if probe {
		dst = cached
	}
	var frame ethernet.Frame
	if target.Is4() {
		var err error
		frame, err = arp.Encode(arp.Message{HardwareType: 1, ProtocolType: 0x0800, Operation: arp.Request, SenderMAC: iface.MAC, SenderAddr: source, TargetAddr: target}, dst)
		if err != nil {
			return layer.Emission{}, false
		}
	} else {
		destination := target
		if !probe {
			t := target.As16()
			// RFC 4291 section 2.7.1 uses the target's low 24 bits.
			destination = netip.AddrFrom16([16]byte{0xff, 0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0xff, t[13], t[14], t[15]})
			// RFC 2464 section 7 maps the group's last four octets.
			dst = netaddr.MAC{0x33, 0x33, 0xff, t[13], t[14], t[15]}
		}
		hdr := ip.Header{Src: source, Dst: destination, Protocol: 58, HopLimit: 255, V6: &ip.V6{}}
		body, err := ndp.Encode(hdr, ndp.Message{Type: ndp.NeighborSolicitation, Target: target, LinkLayerAddr: iface.MAC, HasLinkLayerAddr: true})
		if err != nil {
			return layer.Emission{}, false
		}
		payload, err := hdr.Encode(body)
		if err != nil {
			return layer.Emission{}, false
		}
		frame = ethernet.Frame{Src: iface.MAC, Dst: dst, EtherType: ethernet.EtherTypeIPv6, Payload: payload}
	}
	return layer.Emission{Port: iface.Port, VID: iface.VLAN, Frame: frame}, true
}
