package netmodel

import (
	"net/netip"
	"reflect"
	"testing"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	packetv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/packet/v1"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/layer/filter"
)

func parserResult[T any](value T, err error) (any, string) {
	if err != nil {
		return value, err.Error()
	}
	return value, ""
}

func TestParserReasons(t *testing.T) {
	eui := func(b []byte) *addrv1.Eui48Address { return addrv1.Eui48Address_builder{Octets: b}.Build() }
	mac := func(b []byte) *addrv1.MacAddress { return addrv1.MacAddress_builder{Eui48: eui(b)}.Build() }
	ip4 := func(b []byte) *addrv1.IpAddress {
		return addrv1.IpAddress_builder{V4: addrv1.Ipv4Address_builder{Octets: b}.Build()}.Build()
	}
	ip6 := func(b []byte) *addrv1.IpAddress {
		return addrv1.IpAddress_builder{V6: addrv1.Ipv6Address_builder{Octets: b}.Build()}.Build()
	}
	prefix4 := func(b []byte, length *uint32) *addrv1.IpPrefix {
		return addrv1.IpPrefix_builder{V4: addrv1.Ipv4Prefix_builder{Address: addrv1.Ipv4Address_builder{Octets: b}.Build(), Length: length}.Build()}.Build()
	}
	prefix6 := func(b []byte, length *uint32) *addrv1.IpPrefix {
		return addrv1.IpPrefix_builder{V6: addrv1.Ipv6Prefix_builder{Address: addrv1.Ipv6Address_builder{Octets: b}.Build(), Length: length}.Build()}.Build()
	}
	exact := func(n uint32) *packetv1.TransportPortMatch {
		return packetv1.TransportPortMatch_builder{Exact: new(n)}.Build()
	}
	ports := func(start, end uint32) *packetv1.TransportPortMatch {
		return packetv1.TransportPortMatch_builder{Range: packetv1.TransportPortRange_builder{Start: new(start), End: new(end)}.Build()}.Build()
	}
	address := netip.MustParseAddr("10.0.0.1")
	rows := []struct {
		name   string
		run    func() (any, string)
		want   any
		reason string
	}{
		{"MAC/nil", func() (any, string) { return parserResult(parseMAC(nil)) }, netaddr.MAC{}, "mac is absent"},
		{"MAC/missing_arm", func() (any, string) { return parserResult(parseMAC(addrv1.MacAddress_builder{}.Build())) }, netaddr.MAC{}, "mac kind is absent"},
		{"MAC/eui64", func() (any, string) {
			return parserResult(parseMAC(addrv1.MacAddress_builder{Eui64: addrv1.Eui64Address_builder{Octets: make([]byte, 8)}.Build()}.Build()))
		}, netaddr.MAC{}, "mac kind EUI-64 is unsupported"},
		{"MAC/length5", func() (any, string) { return parserResult(parseMAC(mac(make([]byte, 5)))) }, netaddr.MAC{}, "eui48 octets length 5, want 6"},
		{"MAC/length7", func() (any, string) { return parserResult(parseMAC(mac(make([]byte, 7)))) }, netaddr.MAC{}, "eui48 octets length 7, want 6"},
		{"MAC/zero", func() (any, string) { return parserResult(parseMAC(mac(make([]byte, 6)))) }, netaddr.MAC{}, ""},
		{"MAC/group", func() (any, string) { return parserResult(parseMAC(mac([]byte{1, 2, 3, 4, 5, 6}))) }, netaddr.MAC{1, 2, 3, 4, 5, 6}, ""},
		{"MAC/unicast", func() (any, string) { return parserResult(parseMAC(mac([]byte{2, 3, 4, 5, 6, 7}))) }, netaddr.MAC{2, 3, 4, 5, 6, 7}, ""},
		{"EUI48/nil", func() (any, string) { return parserResult(parseEUI48(nil)) }, netaddr.MAC{}, "eui48 is absent"},
		{"EUI48/length0", func() (any, string) { return parserResult(parseEUI48(eui([]byte{}))) }, netaddr.MAC{}, "eui48 octets length 0, want 6"},
		{"EUI48/length5", func() (any, string) { return parserResult(parseEUI48(eui(make([]byte, 5)))) }, netaddr.MAC{}, "eui48 octets length 5, want 6"},
		{"EUI48/length7", func() (any, string) { return parserResult(parseEUI48(eui(make([]byte, 7)))) }, netaddr.MAC{}, "eui48 octets length 7, want 6"},
		{"EUI48/zero", func() (any, string) { return parserResult(parseEUI48(eui(make([]byte, 6)))) }, netaddr.MAC{}, ""},
		{"EUI48/group", func() (any, string) { return parserResult(parseEUI48(eui([]byte{1, 2, 3, 4, 5, 6}))) }, netaddr.MAC{1, 2, 3, 4, 5, 6}, ""},
		{"EUI48/unicast", func() (any, string) { return parserResult(parseEUI48(eui([]byte{2, 3, 4, 5, 6, 7}))) }, netaddr.MAC{2, 3, 4, 5, 6, 7}, ""},
		{"EUI48/broadcast", func() (any, string) { return parserResult(parseEUI48(eui([]byte{255, 255, 255, 255, 255, 255}))) }, netaddr.MAC{255, 255, 255, 255, 255, 255}, ""},
		{"IP/nil", func() (any, string) { return parserResult(parseIP(nil)) }, netip.Addr{}, "ip address is absent"},
		{"IP/missing_arm", func() (any, string) { return parserResult(parseIP(addrv1.IpAddress_builder{}.Build())) }, netip.Addr{}, "ip address family is absent"},
		{"IP/v4_width", func() (any, string) { return parserResult(parseIP(ip4(make([]byte, 3)))) }, netip.Addr{}, "ipv4 octets length 3, want 4"},
		{"IP/v6_width", func() (any, string) { return parserResult(parseIP(ip6(make([]byte, 15)))) }, netip.Addr{}, "ipv6 octets length 15, want 16"},
		{"IP/v4", func() (any, string) { return parserResult(parseIP(ip4([]byte{10, 0, 0, 1}))) }, address, ""},
		{"IP/v6", func() (any, string) { return parserResult(parseIP(ip6(netip.MustParseAddr("2001:db8::1").AsSlice()))) }, netip.MustParseAddr("2001:db8::1"), ""},
		{"IP/mapped", func() (any, string) {
			return parserResult(parseIP(ip6(netip.MustParseAddr("::ffff:10.0.0.1").AsSlice())))
		}, netip.Addr{}, "ipv6 address is IPv4-mapped"},
		{"IP/unspecified", func() (any, string) { return parserResult(parseIP(ip4(make([]byte, 4)))) }, netip.MustParseAddr("0.0.0.0"), ""},
		{"Prefix/nil", func() (any, string) { return parserResult(parsePrefix(nil, address)) }, 0, "prefix is absent"},
		{"Prefix/invalid_address", func() (any, string) {
			return parserResult(parsePrefix(prefix4([]byte{10, 0, 0, 0}, new(uint32(24))), netip.Addr{}))
		}, 0, "supplied address is invalid"},
		{"Prefix/wrong_family", func() (any, string) {
			return parserResult(parsePrefix(prefix6(make([]byte, 16), new(uint32(0))), address))
		}, 0, "prefix family differs from address"},
		{"Prefix/absent_length", func() (any, string) { return parserResult(parsePrefix(prefix4(make([]byte, 4), nil), address)) }, 0, "ipv4 prefix length is absent"},
		{"Prefix/excessive_length", func() (any, string) {
			return parserResult(parsePrefix(prefix4(make([]byte, 4), new(uint32(33))), address))
		}, 0, "ipv4 prefix length 33 exceeds 32"},
		{"Prefix/host_bits", func() (any, string) {
			return parserResult(parsePrefix(prefix4([]byte{10, 0, 0, 1}, new(uint32(24))), address))
		}, 0, "ipv4 prefix has nonzero host bits"},
		{"Prefix/noncontaining", func() (any, string) {
			return parserResult(parsePrefix(prefix4([]byte{11, 0, 0, 0}, new(uint32(24))), address))
		}, 0, "prefix does not contain address"},
		{"Prefix/canonical", func() (any, string) {
			return parserResult(parsePrefix(prefix4([]byte{10, 0, 0, 0}, new(uint32(24))), address))
		}, 24, ""},
		{"IPPrefix/nil", func() (any, string) { return parserResult(parseIPPrefix(nil)) }, netip.Prefix{}, "prefix is absent"},
		{"IPPrefix/missing_arm", func() (any, string) { return parserResult(parseIPPrefix(addrv1.IpPrefix_builder{}.Build())) }, netip.Prefix{}, "prefix family is absent"},
		{"IPPrefix/v4_width", func() (any, string) { return parserResult(parseIPPrefix(prefix4(make([]byte, 3), new(uint32(24))))) }, netip.Prefix{}, "ipv4 prefix address octets length 3, want 4"},
		{"IPPrefix/v6_width", func() (any, string) { return parserResult(parseIPPrefix(prefix6(make([]byte, 15), new(uint32(64))))) }, netip.Prefix{}, "ipv6 prefix address octets length 15, want 16"},
		{"IPPrefix/excessive_length", func() (any, string) { return parserResult(parseIPPrefix(prefix6(make([]byte, 16), new(uint32(129))))) }, netip.Prefix{}, "ipv6 prefix length 129 exceeds 128"},
		{"IPPrefix/host_bits", func() (any, string) {
			return parserResult(parseIPPrefix(prefix4([]byte{10, 0, 0, 1}, new(uint32(24)))))
		}, netip.Prefix{}, "ipv4 prefix has nonzero host bits"},
		{"IPPrefix/v4", func() (any, string) {
			return parserResult(parseIPPrefix(prefix4([]byte{10, 0, 0, 0}, new(uint32(24)))))
		}, netip.MustParsePrefix("10.0.0.0/24"), ""},
		{"IPPrefix/v6", func() (any, string) {
			return parserResult(parseIPPrefix(prefix6(netip.MustParseAddr("2001:db8::").AsSlice(), new(uint32(32)))))
		}, netip.MustParsePrefix("2001:db8::/32"), ""},
		{"PortMatch/nil", func() (any, string) { return parserResult(parsePortMatch(nil)) }, filter.PortRange{}, "port match is absent"},
		{"PortMatch/missing_arm", func() (any, string) {
			return parserResult(parsePortMatch(packetv1.TransportPortMatch_builder{}.Build()))
		}, filter.PortRange{}, "port match selection is absent"},
		{"PortMatch/exact0", func() (any, string) { return parserResult(parsePortMatch(exact(0))) }, filter.PortRange{}, ""},
		{"PortMatch/exact65535", func() (any, string) { return parserResult(parsePortMatch(exact(65535))) }, filter.PortRange{Start: 65535, End: 65535}, ""},
		{"PortMatch/exact65536", func() (any, string) { return parserResult(parsePortMatch(exact(65536))) }, filter.PortRange{}, "exact port 65536 exceeds 65535"},
		{"PortMatch/ordered", func() (any, string) { return parserResult(parsePortMatch(ports(10, 20))) }, filter.PortRange{Start: 10, End: 20}, ""},
		{"PortMatch/inverted", func() (any, string) { return parserResult(parsePortMatch(ports(20, 10))) }, filter.PortRange{}, "port range start 20 exceeds end 10"},
		{"PortMatch/oversized", func() (any, string) { return parserResult(parsePortMatch(ports(10, 65536))) }, filter.PortRange{}, "port range end 65536 exceeds 65535"},
	}
	reached := 0
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			got, reason := row.run()
			if !reflect.DeepEqual(got, row.want) {
				t.Errorf("value = %v, want %v", got, row.want)
			}
			if reason != row.reason {
				t.Errorf("reason = %q, want %q", reason, row.reason)
			}
			reached++
		})
	}
	if reached != 48 {
		t.Fatalf("reached %d concrete parser cases, want 48", reached)
	}
}
