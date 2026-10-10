package netmodel

import (
	"net/netip"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	packetv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/packet/v1"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/filter"
)

// Issue codes used by netmodel.
const (
	IssueInvalidMAC analysis.IssueCode = "netmodel.address.invalid_mac"
)

func parseMAC(mac *addrv1.MacAddress) (netaddr.MAC, bool) {
	if mac == nil || mac.GetEui48() == nil {
		return netaddr.MAC{}, false
	}
	return parseEUI48(mac.GetEui48())
}

func parseEUI48(eui *addrv1.Eui48Address) (netaddr.MAC, bool) {
	if eui == nil {
		return netaddr.MAC{}, false
	}
	octets := eui.GetOctets()
	if len(octets) != 6 {
		return netaddr.MAC{}, false
	}
	var mac netaddr.MAC
	copy(mac[:], octets)
	return mac, true
}

func parseIP(ipAddr *addrv1.IpAddress) (netip.Addr, bool) {
	if ipAddr == nil {
		return netip.Addr{}, false
	}
	if v4 := ipAddr.GetV4(); v4 != nil {
		octets := v4.GetOctets()
		if len(octets) != 4 {
			return netip.Addr{}, false
		}
		return netip.AddrFrom4([4]byte(octets)), true
	}
	if v6 := ipAddr.GetV6(); v6 != nil {
		octets := v6.GetOctets()
		if len(octets) != 16 {
			return netip.Addr{}, false
		}
		addr := netip.AddrFrom16([16]byte(octets))
		if addr.Is4In6() {
			// Routing validation refuses an IPv4-mapped address outright
			// (a decoded IPv4 address is 4 bytes and never matches it), so
			// treating it as invalid here turns a device report that
			// carries one into the same recorded issue a parse failure
			// gets, instead of an unconstructible load.
			return netip.Addr{}, false
		}
		return addr, true
	}
	return netip.Addr{}, false
}

func parsePrefix(p *addrv1.IpPrefix, address netip.Addr) (int, bool) {
	if p == nil || !address.IsValid() {
		return 0, false
	}
	if address.Is4() {
		v4 := p.GetV4()
		if v4 == nil || !v4.HasAddress() || !v4.HasLength() || len(v4.GetAddress().GetOctets()) != 4 || v4.GetLength() > 32 {
			return 0, false
		}
		prefixAddress := netip.AddrFrom4([4]byte(v4.GetAddress().GetOctets()))
		prefix := netip.PrefixFrom(prefixAddress, int(v4.GetLength()))
		if prefix != prefix.Masked() || !prefix.Contains(address) {
			return 0, false
		}
		return int(v4.GetLength()), true
	}
	if address.Is6() {
		v6 := p.GetV6()
		if v6 == nil || !v6.HasAddress() || !v6.HasLength() || len(v6.GetAddress().GetOctets()) != 16 || v6.GetLength() > 128 {
			return 0, false
		}
		prefixAddress := netip.AddrFrom16([16]byte(v6.GetAddress().GetOctets()))
		prefix := netip.PrefixFrom(prefixAddress, int(v6.GetLength()))
		if prefix != prefix.Masked() || !prefix.Contains(address) {
			return 0, false
		}
		return int(v6.GetLength()), true
	}
	return 0, false
}

func parseIPPrefix(p *addrv1.IpPrefix) (netip.Prefix, bool) {
	if p == nil {
		return netip.Prefix{}, false
	}
	if v4 := p.GetV4(); v4 != nil {
		addr := v4.GetAddress()
		if addr == nil || len(addr.GetOctets()) != 4 || v4.GetLength() > 32 {
			return netip.Prefix{}, false
		}
		ip := netip.AddrFrom4([4]byte(addr.GetOctets()))
		prefix := netip.PrefixFrom(ip, int(v4.GetLength()))
		if prefix != prefix.Masked() {
			return netip.Prefix{}, false
		}
		return prefix, true
	}
	if v6 := p.GetV6(); v6 != nil {
		addr := v6.GetAddress()
		if addr == nil || len(addr.GetOctets()) != 16 || v6.GetLength() > 128 {
			return netip.Prefix{}, false
		}
		ip := netip.AddrFrom16([16]byte(addr.GetOctets()))
		prefix := netip.PrefixFrom(ip, int(v6.GetLength()))
		if prefix != prefix.Masked() {
			return netip.Prefix{}, false
		}
		return prefix, true
	}
	return netip.Prefix{}, false
}

func parsePortMatch(m *packetv1.TransportPortMatch) (filter.PortRange, bool) {
	if m == nil {
		return filter.PortRange{}, false
	}
	if r := m.GetRange(); r != nil {
		if r.GetStart() > 65535 || r.GetEnd() > 65535 || r.GetStart() > r.GetEnd() {
			return filter.PortRange{}, false
		}
		return filter.PortRange{
			Start: uint16(r.GetStart()),
			End:   uint16(r.GetEnd()),
		}, true
	}
	if m.HasExact() {
		exact := m.GetExact()
		if exact > 65535 {
			return filter.PortRange{}, false
		}
		return filter.PortRange{
			Start: uint16(exact),
			End:   uint16(exact),
		}, true
	}
	return filter.PortRange{}, false
}
