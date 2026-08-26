package protoconformance

import (
	"testing"

	"google.golang.org/protobuf/proto"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	l3v1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/l3/v1"
)

func TestLayer3PrimitiveRules(t *testing.T) {
	v4Address := func(octets ...byte) *addrv1.IpAddress {
		return addrv1.IpAddress_builder{
			V4: addrv1.Ipv4Address_builder{Octets: octets}.Build(),
		}.Build()
	}
	v6Address := func(octets ...byte) *addrv1.IpAddress {
		return addrv1.IpAddress_builder{
			V6: addrv1.Ipv6Address_builder{Octets: octets}.Build(),
		}.Build()
	}
	v4Prefix := func(octets []byte, length uint32) *addrv1.IpPrefix {
		return addrv1.IpPrefix_builder{
			V4: addrv1.Ipv4Prefix_builder{
				Address: addrv1.Ipv4Address_builder{Octets: octets}.Build(),
				Length:  proto.Uint32(length),
			}.Build(),
		}.Build()
	}
	v6Prefix := func(octets []byte, length uint32) *addrv1.IpPrefix {
		return addrv1.IpPrefix_builder{
			V6: addrv1.Ipv6Prefix_builder{
				Address: addrv1.Ipv6Address_builder{Octets: octets}.Build(),
				Length:  proto.Uint32(length),
			}.Build(),
		}.Build()
	}

	tests := []validationCase{
		{
			name:      "IPv4 MTU below minimum",
			message:   l3v1.Ipv4Facet_builder{Mtu: proto.Uint32(67)}.Build(),
			wantValid: false,
		},
		{
			name:      "minimum IPv4 MTU",
			message:   l3v1.Ipv4Facet_builder{Mtu: proto.Uint32(68)}.Build(),
			wantValid: true,
		},
		{
			name:      "IPv6 MTU below minimum",
			message:   l3v1.Ipv6Facet_builder{Mtu: proto.Uint32(1279)}.Build(),
			wantValid: false,
		},
		{
			name:      "minimum IPv6 MTU",
			message:   l3v1.Ipv6Facet_builder{Mtu: proto.Uint32(1280)}.Build(),
			wantValid: true,
		},
		{
			name: "interface address rejects mixed families",
			message: l3v1.InterfaceAddress_builder{
				InterfaceName: proto.String("ethernet1/1"),
				Address:       v4Address(192, 0, 2, 1),
				Prefix:        v6Prefix(make([]byte, 16), 64),
			}.Build(),
			wantValid: false,
		},
		{
			name: "valid IPv4 interface address",
			message: l3v1.InterfaceAddress_builder{
				InterfaceName: proto.String("ethernet1/1"),
				Address:       v4Address(192, 0, 2, 1),
				Prefix:        v4Prefix([]byte{192, 0, 2, 0}, 24),
			}.Build(),
			wantValid: true,
		},
		{
			name: "IPv4 interface address outside prefix",
			message: l3v1.InterfaceAddress_builder{
				InterfaceName: proto.String("ethernet1/1"),
				Address:       v4Address(192, 0, 2, 127),
				Prefix:        v4Prefix([]byte{192, 0, 2, 128}, 25),
			}.Build(),
			wantValid: false,
		},
		{
			name: "IPv4 interface address at non-nibble prefix boundary",
			message: l3v1.InterfaceAddress_builder{
				InterfaceName: proto.String("ethernet1/1"),
				Address:       v4Address(192, 0, 2, 255),
				Prefix:        v4Prefix([]byte{192, 0, 2, 128}, 25),
			}.Build(),
			wantValid: true,
		},
		{
			name: "IPv4 interface address at two-bit prefix boundary",
			message: l3v1.InterfaceAddress_builder{
				InterfaceName: proto.String("ethernet1/1"),
				Address:       v4Address(192, 0, 2, 255),
				Prefix:        v4Prefix([]byte{192, 0, 2, 192}, 26),
			}.Build(),
			wantValid: true,
		},
		{
			name: "IPv4 interface address at three-bit prefix boundary",
			message: l3v1.InterfaceAddress_builder{
				InterfaceName: proto.String("ethernet1/1"),
				Address:       v4Address(192, 0, 2, 255),
				Prefix:        v4Prefix([]byte{192, 0, 2, 224}, 27),
			}.Build(),
			wantValid: true,
		},
		{
			name: "IPv6 interface address outside prefix",
			message: l3v1.InterfaceAddress_builder{
				InterfaceName: proto.String("ethernet1/1"),
				Address: v6Address(
					0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0,
					0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
				),
				Prefix: v6Prefix([]byte{
					0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0,
					0x80, 0, 0, 0, 0, 0, 0, 0,
				}, 65),
			}.Build(),
			wantValid: false,
		},
		{
			name: "IPv6 interface address at non-nibble prefix boundary",
			message: l3v1.InterfaceAddress_builder{
				InterfaceName: proto.String("ethernet1/1"),
				Address: v6Address(
					0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0,
					0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
				),
				Prefix: v6Prefix([]byte{
					0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0,
					0x80, 0, 0, 0, 0, 0, 0, 0,
				}, 65),
			}.Build(),
			wantValid: true,
		},
		{
			name: "IPv6 interface address at two-bit prefix boundary",
			message: l3v1.InterfaceAddress_builder{
				InterfaceName: proto.String("ethernet1/1"),
				Address: v6Address(
					0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0,
					0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
				),
				Prefix: v6Prefix([]byte{
					0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0,
					0xc0, 0, 0, 0, 0, 0, 0, 0,
				}, 66),
			}.Build(),
			wantValid: true,
		},
		{
			name: "IPv6 interface address at three-bit prefix boundary",
			message: l3v1.InterfaceAddress_builder{
				InterfaceName: proto.String("ethernet1/1"),
				Address: v6Address(
					0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0,
					0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
				),
				Prefix: v6Prefix([]byte{
					0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0,
					0xe0, 0, 0, 0, 0, 0, 0, 0,
				}, 67),
			}.Build(),
			wantValid: true,
		},
		{
			name: "IPv4 neighbor rejects router flag",
			message: l3v1.NeighborEntry_builder{
				InterfaceName: proto.String("ethernet1/1"),
				Ip:            v4Address(192, 0, 2, 2),
				IsRouter:      proto.Bool(true),
			}.Build(),
			wantValid: false,
		},
		{
			name: "neighbor with unresolved link-layer address",
			message: l3v1.NeighborEntry_builder{
				InterfaceName: proto.String("ethernet1/1"),
				Ip:            v4Address(192, 0, 2, 2),
			}.Build(),
			wantValid: true,
		},
		{
			name: "neighbor with a resolved EUI-48 address",
			message: l3v1.NeighborEntry_builder{
				InterfaceName: proto.String("ethernet1/1"),
				Ip:            v4Address(192, 0, 2, 2),
				Mac: addrv1.EuiAddress_builder{
					Eui48: addrv1.Eui48Address_builder{
						Octets: []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
					}.Build(),
				}.Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "neighbor with a resolved EUI-64 address",
			message: l3v1.NeighborEntry_builder{
				InterfaceName: proto.String("ethernet1/1"),
				Ip:            v4Address(192, 0, 2, 2),
				Mac: addrv1.EuiAddress_builder{
					Eui64: addrv1.Eui64Address_builder{
						Octets: []byte{0x00, 0x11, 0x22, 0xff, 0xfe, 0x33, 0x44, 0x55},
					}.Build(),
				}.Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "neighbor rejects an empty link-layer wrapper",
			message: l3v1.NeighborEntry_builder{
				InterfaceName: proto.String("ethernet1/1"),
				Ip:            v4Address(192, 0, 2, 2),
				Mac:           addrv1.EuiAddress_builder{}.Build(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "IPv6 router neighbor",
			message: l3v1.NeighborEntry_builder{
				InterfaceName: proto.String("ethernet1/1"),
				Ip: v6Address(
					0xfe, 0x80, 0, 0, 0, 0, 0, 0,
					0, 0, 0, 0, 0, 0, 0, 1,
				),
				IsRouter: proto.Bool(true),
			}.Build(),
			wantValid: true,
		},
	}

	runValidationCases(t, tests)
}

func TestAddressOriginRenumberedValues(t *testing.T) {
	// The pre-release reserved-tombstone collapse renumbered the values that
	// followed the removed SLAAC slot; pin the highest surviving value so an
	// accidental re-renumber cannot land silently.
	if got := int32(l3v1.AddressOrigin_ADDRESS_ORIGIN_LINK_LAYER); got != 4 {
		t.Fatalf("ADDRESS_ORIGIN_LINK_LAYER = %d, want 4", got)
	}
	if got := int32(l3v1.AddressOrigin_ADDRESS_ORIGIN_RANDOM); got != 5 {
		t.Fatalf("ADDRESS_ORIGIN_RANDOM = %d, want 5", got)
	}
}
