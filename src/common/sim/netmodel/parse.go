package netmodel

import (
	"net/netip"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	packetv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/packet/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/filter"
)

// Issue codes used by netmodel.
const (
	IssueInvalidMAC analysis.IssueCode = "netmodel.address.invalid_mac"
)

func parseMAC(mac *addrv1.MacAddress) (netaddr.MAC, error) {
	if mac == nil {
		return netaddr.MAC{}, errs.Msg("mac is absent")
	}
	if mac.GetEui64() != nil {
		return netaddr.MAC{}, errs.Msg("mac kind EUI-64 is unsupported")
	}
	if mac.GetEui48() == nil {
		return netaddr.MAC{}, errs.Msg("mac kind is absent")
	}
	return parseEUI48(mac.GetEui48())
}

func parseEUI48(eui *addrv1.Eui48Address) (netaddr.MAC, error) {
	if eui == nil {
		return netaddr.MAC{}, errs.Msg("eui48 is absent")
	}
	octets := eui.GetOctets()
	if len(octets) != 6 {
		return netaddr.MAC{}, errs.Msgf("eui48 octets length %d, want 6", len(octets))
	}
	var mac netaddr.MAC
	copy(mac[:], octets)
	return mac, nil
}

func parseIP(ipAddr *addrv1.IpAddress) (netip.Addr, error) {
	if ipAddr == nil {
		return netip.Addr{}, errs.Msg("ip address is absent")
	}
	if v4 := ipAddr.GetV4(); v4 != nil {
		octets := v4.GetOctets()
		if len(octets) != 4 {
			return netip.Addr{}, errs.Msgf("ipv4 octets length %d, want 4", len(octets))
		}
		return netip.AddrFrom4([4]byte(octets)), nil
	}
	if v6 := ipAddr.GetV6(); v6 != nil {
		octets := v6.GetOctets()
		if len(octets) != 16 {
			return netip.Addr{}, errs.Msgf("ipv6 octets length %d, want 16", len(octets))
		}
		addr := netip.AddrFrom16([16]byte(octets))
		// Routing refuses mapped addresses, which cannot match a decoded IPv4 address.
		if addr.Is4In6() {
			return netip.Addr{}, errs.Msg("ipv6 address is IPv4-mapped")
		}
		return addr, nil
	}
	return netip.Addr{}, errs.Msg("ip address family is absent")
}

func parsePrefix(p *addrv1.IpPrefix, address netip.Addr) (int, error) {
	if p == nil {
		return 0, errs.Msg("prefix is absent")
	}
	if !address.IsValid() {
		return 0, errs.Msg("supplied address is invalid")
	}
	if address.Is4() {
		v4 := p.GetV4()
		if v4 == nil {
			return 0, errs.Msg("prefix family differs from address")
		}
		if !v4.HasLength() {
			return 0, errs.Msg("ipv4 prefix length is absent")
		}
	} else {
		v6 := p.GetV6()
		if v6 == nil {
			return 0, errs.Msg("prefix family differs from address")
		}
		if !v6.HasLength() {
			return 0, errs.Msg("ipv6 prefix length is absent")
		}
	}
	prefix, err := parseIPPrefix(p)
	if err != nil {
		return 0, err
	}
	if !prefix.Contains(address) {
		return 0, errs.Msg("prefix does not contain address")
	}
	return prefix.Bits(), nil
}

func parseIPPrefix(p *addrv1.IpPrefix) (netip.Prefix, error) {
	if p == nil {
		return netip.Prefix{}, errs.Msg("prefix is absent")
	}
	if v4 := p.GetV4(); v4 != nil {
		if !v4.HasLength() {
			return netip.Prefix{}, errs.Msg("ipv4 prefix length is absent")
		}
		addr := v4.GetAddress()
		if addr == nil {
			return netip.Prefix{}, errs.Msg("ipv4 prefix address is absent")
		}
		if len(addr.GetOctets()) != 4 {
			return netip.Prefix{}, errs.Msgf("ipv4 prefix address octets length %d, want 4", len(addr.GetOctets()))
		}
		if v4.GetLength() > 32 {
			return netip.Prefix{}, errs.Msgf("ipv4 prefix length %d exceeds 32", v4.GetLength())
		}
		ip := netip.AddrFrom4([4]byte(addr.GetOctets()))
		prefix := netip.PrefixFrom(ip, int(v4.GetLength()))
		if prefix != prefix.Masked() {
			return netip.Prefix{}, errs.Msg("ipv4 prefix has nonzero host bits")
		}
		return prefix, nil
	}
	if v6 := p.GetV6(); v6 != nil {
		if !v6.HasLength() {
			return netip.Prefix{}, errs.Msg("ipv6 prefix length is absent")
		}
		addr := v6.GetAddress()
		if addr == nil {
			return netip.Prefix{}, errs.Msg("ipv6 prefix address is absent")
		}
		if len(addr.GetOctets()) != 16 {
			return netip.Prefix{}, errs.Msgf("ipv6 prefix address octets length %d, want 16", len(addr.GetOctets()))
		}
		if v6.GetLength() > 128 {
			return netip.Prefix{}, errs.Msgf("ipv6 prefix length %d exceeds 128", v6.GetLength())
		}
		ip := netip.AddrFrom16([16]byte(addr.GetOctets()))
		prefix := netip.PrefixFrom(ip, int(v6.GetLength()))
		if prefix != prefix.Masked() {
			return netip.Prefix{}, errs.Msg("ipv6 prefix has nonzero host bits")
		}
		return prefix, nil
	}
	return netip.Prefix{}, errs.Msg("prefix family is absent")
}

func parsePortMatch(m *packetv1.TransportPortMatch) (filter.PortRange, error) {
	if m == nil {
		return filter.PortRange{}, errs.Msg("port match is absent")
	}
	if r := m.GetRange(); r != nil {
		if !r.HasStart() {
			return filter.PortRange{}, errs.Msg("port range start is absent")
		}
		if !r.HasEnd() {
			return filter.PortRange{}, errs.Msg("port range end is absent")
		}
		if r.GetStart() > 65535 {
			return filter.PortRange{}, errs.Msgf("port range start %d exceeds 65535", r.GetStart())
		}
		if r.GetEnd() > 65535 {
			return filter.PortRange{}, errs.Msgf("port range end %d exceeds 65535", r.GetEnd())
		}
		if r.GetStart() > r.GetEnd() {
			return filter.PortRange{}, errs.Msgf("port range start %d exceeds end %d", r.GetStart(), r.GetEnd())
		}
		return filter.PortRange{Start: uint16(r.GetStart()), End: uint16(r.GetEnd())}, nil
	}
	if m.HasExact() {
		exact := m.GetExact()
		if exact > 65535 {
			return filter.PortRange{}, errs.Msgf("exact port %d exceeds 65535", exact)
		}
		return filter.PortRange{Start: uint16(exact), End: uint16(exact)}, nil
	}
	return filter.PortRange{}, errs.Msg("port match selection is absent")
}
