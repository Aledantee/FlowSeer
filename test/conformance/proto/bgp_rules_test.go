package conformance

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	bgpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/bgp/v1"
)

func validBgpInstance() *bgpv1.BgpInstance_builder {
	return &bgpv1.BgpInstance_builder{
		NetworkInstance:  proto.String("default"),
		ProtocolInstance: proto.String("default"),
		Asn:              proto.Uint32(65001),
		RouterId:         proto.Uint32(1),
	}
}

func TestBgpInstanceRules(t *testing.T) {
	withoutNetwork := validBgpInstance()
	withoutNetwork.NetworkInstance = nil

	withoutProtocol := validBgpInstance()
	withoutProtocol.ProtocolInstance = nil

	withoutAsn := validBgpInstance()
	withoutAsn.Asn = nil

	routerIDZero := validBgpInstance()
	routerIDZero.RouterId = proto.Uint32(0)

	runFieldCases(t, []fieldCase{
		{name: "valid instance", message: validBgpInstance().Build()},
		{name: "network_instance absent", message: withoutNetwork.Build(), wantField: "network_instance", wantText: "value is required"},
		{name: "protocol_instance absent", message: withoutProtocol.Build(), wantField: "protocol_instance", wantText: "value is required"},
		{name: "asn absent", message: withoutAsn.Build(), wantField: "asn", wantText: "value is required"},
		{name: "router_id zero", message: routerIDZero.Build(), wantField: "router_id", wantText: "greater than or equal to 1"},
	})
}

func validBgpPeer(t *testing.T) *bgpv1.BgpPeer_builder {
	return &bgpv1.BgpPeer_builder{
		NetworkInstance:  proto.String("default"),
		ProtocolInstance: proto.String("default"),
		RemoteAddress:    ipAddress(t, "192.0.2.1"),
		RemoteAsn:        proto.Uint32(4200000000),
	}
}

func TestBgpPeerRules(t *testing.T) {
	withoutNetwork := validBgpPeer(t)
	withoutNetwork.NetworkInstance = nil

	withoutProtocol := validBgpPeer(t)
	withoutProtocol.ProtocolInstance = nil

	withoutRemoteAddr := validBgpPeer(t)
	withoutRemoteAddr.RemoteAddress = nil

	linkLocalWithoutIf := validBgpPeer(t)
	linkLocalWithoutIf.RemoteAddress = ipAddress(t, "fe80::1")

	linkLocalWithIf := validBgpPeer(t)
	linkLocalWithIf.RemoteAddress = ipAddress(t, "fe80::1")
	linkLocalWithIf.InterfaceName = proto.String("eth0")

	holdTime0 := validBgpPeer(t)
	holdTime0.NegotiatedHoldTime = durationpb.New(0)

	holdTime3 := validBgpPeer(t)
	holdTime3.NegotiatedHoldTime = &durationpb.Duration{Seconds: 3}

	holdTime2 := validBgpPeer(t)
	holdTime2.NegotiatedHoldTime = &durationpb.Duration{Seconds: 2}

	holdTimeFractional := validBgpPeer(t)
	holdTimeFractional.NegotiatedHoldTime = &durationpb.Duration{Seconds: 90, Nanos: 500000000}

	keepalive21845 := validBgpPeer(t)
	keepalive21845.NegotiatedKeepalive = &durationpb.Duration{Seconds: 21845}

	keepalive21846 := validBgpPeer(t)
	keepalive21846.NegotiatedKeepalive = &durationpb.Duration{Seconds: 21846}

	dupFamilies := validBgpPeer(t)
	dupFamilies.AddressFamilies = []*bgpv1.BgpPeerAddressFamily{
		bgpv1.BgpPeerAddressFamily_builder{Afi: bgpv1.BgpAfi_BGP_AFI_IPV4.Enum(), Safi: bgpv1.BgpSafi_BGP_SAFI_UNICAST.Enum()}.Build(),
		bgpv1.BgpPeerAddressFamily_builder{Afi: bgpv1.BgpAfi_BGP_AFI_IPV4.Enum(), Safi: bgpv1.BgpSafi_BGP_SAFI_UNICAST.Enum()}.Build(),
	}

	distinctFamilies := validBgpPeer(t)
	distinctFamilies.AddressFamilies = []*bgpv1.BgpPeerAddressFamily{
		bgpv1.BgpPeerAddressFamily_builder{Afi: bgpv1.BgpAfi_BGP_AFI_IPV4.Enum(), Safi: bgpv1.BgpSafi_BGP_SAFI_UNICAST.Enum()}.Build(),
		bgpv1.BgpPeerAddressFamily_builder{Afi: bgpv1.BgpAfi_BGP_AFI_L2VPN.Enum(), Safi: bgpv1.BgpSafi_BGP_SAFI_EVPN.Enum()}.Build(),
	}

	runFieldCases(t, []fieldCase{
		{name: "valid peer with 4-octet ASN", message: validBgpPeer(t).Build()},
		{name: "network_instance absent", message: withoutNetwork.Build(), wantField: "network_instance", wantText: "value is required"},
		{name: "protocol_instance absent", message: withoutProtocol.Build(), wantField: "protocol_instance", wantText: "value is required"},
		{name: "remote_address absent", message: withoutRemoteAddr.Build(), wantField: "remote_address", wantText: "value is required"},
		{name: "link-local without interface_name", message: linkLocalWithoutIf.Build(), wantRule: "bgp_peer.link_local_interface"},
		{name: "link-local with interface_name", message: linkLocalWithIf.Build()},
		{name: "hold time 0s passes", message: holdTime0.Build()},
		{name: "hold time 3s passes", message: holdTime3.Build()},
		{name: "hold time 2s fails", message: holdTime2.Build(), wantRule: "bgp_peer.hold_time"},
		{name: "hold time 90.5s fails", message: holdTimeFractional.Build(), wantRule: "bgp_peer.hold_time"},
		{name: "keepalive 21845s passes", message: keepalive21845.Build()},
		{name: "keepalive 21846s fails", message: keepalive21846.Build(), wantRule: "bgp_peer.keepalive"},
		{name: "duplicate address families fail", message: dupFamilies.Build(), wantRule: "bgp_peer.address_families_unique"},
		{name: "distinct address families pass", message: distinctFamilies.Build()},
	})
}

func TestBgpPeerAddressFamilyRules(t *testing.T) {
	valid := func() *bgpv1.BgpPeerAddressFamily_builder {
		return &bgpv1.BgpPeerAddressFamily_builder{
			Afi:  bgpv1.BgpAfi_BGP_AFI_IPV4.Enum(),
			Safi: bgpv1.BgpSafi_BGP_SAFI_UNICAST.Enum(),
		}
	}

	withoutAfi := valid()
	withoutAfi.Afi = nil

	afiZero := valid()
	afiZero.Afi = bgpv1.BgpAfi_BGP_AFI_UNSPECIFIED.Enum()

	afi65536 := valid()
	afi65536.Afi = bgpv1.BgpAfi(65536).Enum()

	withoutSafi := valid()
	withoutSafi.Safi = nil

	safiZero := valid()
	safiZero.Safi = bgpv1.BgpSafi_BGP_SAFI_UNSPECIFIED.Enum()

	safi256 := valid()
	safi256.Safi = bgpv1.BgpSafi(256).Enum()

	safi200 := valid()
	safi200.Safi = bgpv1.BgpSafi(200).Enum()

	l2vpnEvpn := valid()
	l2vpnEvpn.Afi = bgpv1.BgpAfi_BGP_AFI_L2VPN.Enum()
	l2vpnEvpn.Safi = bgpv1.BgpSafi_BGP_SAFI_EVPN.Enum()

	runFieldCases(t, []fieldCase{
		{name: "valid family", message: valid().Build()},
		{name: "afi absent", message: withoutAfi.Build(), wantField: "afi", wantText: "value is required"},
		{name: "afi zero fails", message: afiZero.Build(), wantField: "afi", wantText: "must not be in list"},
		{name: "afi 65536 fails", message: afi65536.Build(), wantField: "afi", wantText: "16-bit"},
		{name: "safi absent", message: withoutSafi.Build(), wantField: "safi", wantText: "value is required"},
		{name: "safi zero fails", message: safiZero.Build(), wantField: "safi", wantText: "must not be in list"},
		{name: "safi 256 fails", message: safi256.Build(), wantField: "safi", wantText: "8-bit"},
		{name: "safi 200 unnamed passes", message: safi200.Build()},
		{name: "L2VPN EVPN passes", message: l2vpnEvpn.Build()},
	})
}

func TestBgpCommunityRules(t *testing.T) {
	empty := bgpv1.BgpCommunity_builder{}

	standard := bgpv1.BgpCommunity_builder{
		Standard: proto.Uint32(0xFFFFFF01),
	}

	large := bgpv1.BgpCommunity_builder{
		Large: bgpv1.BgpLargeCommunity_builder{
			GlobalAdministrator: proto.Uint32(4200000000),
			LocalDataPart1:      proto.Uint32(1),
			LocalDataPart2:      proto.Uint32(2),
		}.Build(),
	}

	largeWithoutAdmin := bgpv1.BgpLargeCommunity_builder{
		LocalDataPart1: proto.Uint32(1),
		LocalDataPart2: proto.Uint32(2),
	}

	largeWithoutPart1 := bgpv1.BgpLargeCommunity_builder{
		GlobalAdministrator: proto.Uint32(4200000000),
		LocalDataPart2:      proto.Uint32(2),
	}

	largeWithoutPart2 := bgpv1.BgpLargeCommunity_builder{
		GlobalAdministrator: proto.Uint32(4200000000),
		LocalDataPart1:      proto.Uint32(1),
	}

	runFieldCases(t, []fieldCase{
		{name: "standard community passes", message: standard.Build()},
		{name: "large community passes", message: large.Build()},
		{name: "empty community fails", message: empty.Build(), wantField: "kind", wantText: "exactly one field is required"},
		{name: "large without global_administrator fails", message: largeWithoutAdmin.Build(), wantField: "global_administrator", wantText: "value is required"},
		{name: "large without local_data_part1 fails", message: largeWithoutPart1.Build(), wantField: "local_data_part1", wantText: "value is required"},
		{name: "large without local_data_part2 fails", message: largeWithoutPart2.Build(), wantField: "local_data_part2", wantText: "value is required"},
	})
}

func TestBgpPathRules(t *testing.T) {
	ipv4Prefix := func(octets []byte, length uint32) *addrv1.IpPrefix {
		return addrv1.IpPrefix_builder{
			V4: addrv1.Ipv4Prefix_builder{
				Address: addrv1.Ipv4Address_builder{Octets: octets}.Build(),
				Length:  proto.Uint32(length),
			}.Build(),
		}.Build()
	}

	segment := func(asns ...uint32) *bgpv1.BgpAsPathSegment_builder {
		return &bgpv1.BgpAsPathSegment_builder{
			Type: bgpv1.BgpAsPathSegmentType_BGP_AS_PATH_SEGMENT_TYPE_AS_SEQUENCE.Enum(),
			Asns: asns,
		}
	}

	validPath := func() *bgpv1.BgpPath_builder {
		return &bgpv1.BgpPath_builder{
			NetworkInstance:  proto.String("default"),
			ProtocolInstance: proto.String("default"),
			Prefix:           ipv4Prefix([]byte{192, 0, 2, 0}, 24),
			Origin:           bgpv1.BgpOrigin_BGP_ORIGIN_IGP.Enum(),
			AsPath: []*bgpv1.BgpAsPathSegment{
				segment(65001).Build(),
			},
			NextHop: ipAddress(t, "192.0.2.1"),
		}
	}

	withoutNetwork := validPath()
	withoutNetwork.NetworkInstance = nil

	withoutProtocol := validPath()
	withoutProtocol.ProtocolInstance = nil

	withoutPrefix := validPath()
	withoutPrefix.Prefix = nil

	segmentNoAsns := segment()
	segmentWithoutType := segment(65001)
	segmentWithoutType.Type = nil
	segmentZeroType := segment(65001)
	segmentZeroType.Type = bgpv1.BgpAsPathSegmentType_BGP_AS_PATH_SEGMENT_TYPE_UNSPECIFIED.Enum()

	runFieldCases(t, []fieldCase{
		{name: "valid path passes", message: validPath().Build()},
		{name: "network_instance absent fails", message: withoutNetwork.Build(), wantField: "network_instance", wantText: "value is required"},
		{name: "protocol_instance absent fails", message: withoutProtocol.Build(), wantField: "protocol_instance", wantText: "value is required"},
		{name: "prefix absent fails", message: withoutPrefix.Build(), wantField: "prefix", wantText: "value is required"},
		{name: "as_path segment without ASNs fails", message: segmentNoAsns.Build(), wantField: "asns", wantText: "at least 1"},
		{name: "as_path segment without type fails", message: segmentWithoutType.Build(), wantField: "type", wantText: "value is required"},
		{name: "as_path segment with unspecified type fails", message: segmentZeroType.Build(), wantField: "type", wantText: "must not be in list"},
	})
}
