package conformance

import (
	"testing"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	ipv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/ip/v1"
)

func TestIpPrimitiveRules(t *testing.T) {
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
			message:   ipv1.Ipv4Facet_builder{Mtu: proto.Uint32(67)}.Build(),
			wantValid: false,
		},
		{
			name:      "minimum IPv4 MTU",
			message:   ipv1.Ipv4Facet_builder{Mtu: proto.Uint32(68)}.Build(),
			wantValid: true,
		},
		{
			name:      "IPv6 MTU below minimum",
			message:   ipv1.Ipv6Facet_builder{Mtu: proto.Uint32(1279)}.Build(),
			wantValid: false,
		},
		{
			name:      "minimum IPv6 MTU",
			message:   ipv1.Ipv6Facet_builder{Mtu: proto.Uint32(1280)}.Build(),
			wantValid: true,
		},
		{
			name: "interface address rejects mixed families",
			message: ipv1.InterfaceAddress_builder{
				InterfaceName: proto.String("ethernet1/1"),
				Address:       v4Address(192, 0, 2, 1),
				Prefix:        v6Prefix(make([]byte, 16), 64),
			}.Build(),
			wantValid: false,
		},
		{
			name: "valid IPv4 interface address",
			message: ipv1.InterfaceAddress_builder{
				InterfaceName: proto.String("ethernet1/1"),
				Address:       v4Address(192, 0, 2, 1),
				Prefix:        v4Prefix([]byte{192, 0, 2, 0}, 24),
			}.Build(),
			wantValid: true,
		},
		{
			name: "IPv4 link-local interface address",
			message: ipv1.InterfaceAddress_builder{
				InterfaceName: proto.String("ethernet1/1"),
				Address:       v4Address(169, 254, 7, 9),
				Prefix:        v4Prefix([]byte{169, 254, 0, 0}, 16),
				Origin:        ipv1.AddressOrigin_ADDRESS_ORIGIN_LINK_LOCAL.Enum(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "IPv6 temporary SLAAC interface address",
			message: ipv1.InterfaceAddress_builder{
				InterfaceName: proto.String("ethernet1/1"),
				Address: v6Address(
					0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0,
					0x3c, 0x5e, 0x91, 0x0a, 0x77, 0x14, 0xd2, 0x08,
				),
				Prefix:    v6Prefix([]byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, 64),
				Origin:    ipv1.AddressOrigin_ADDRESS_ORIGIN_SLAAC.Enum(),
				IidMethod: ipv1.InterfaceIdentifierMethod_INTERFACE_IDENTIFIER_METHOD_TEMPORARY.Enum(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "IPv4 interface address outside prefix",
			message: ipv1.InterfaceAddress_builder{
				InterfaceName: proto.String("ethernet1/1"),
				Address:       v4Address(192, 0, 2, 127),
				Prefix:        v4Prefix([]byte{192, 0, 2, 128}, 25),
			}.Build(),
			wantValid: false,
		},
		{
			name: "IPv4 interface address at non-nibble prefix boundary",
			message: ipv1.InterfaceAddress_builder{
				InterfaceName: proto.String("ethernet1/1"),
				Address:       v4Address(192, 0, 2, 255),
				Prefix:        v4Prefix([]byte{192, 0, 2, 128}, 25),
			}.Build(),
			wantValid: true,
		},
		{
			name: "IPv4 interface address at two-bit prefix boundary",
			message: ipv1.InterfaceAddress_builder{
				InterfaceName: proto.String("ethernet1/1"),
				Address:       v4Address(192, 0, 2, 255),
				Prefix:        v4Prefix([]byte{192, 0, 2, 192}, 26),
			}.Build(),
			wantValid: true,
		},
		{
			name: "IPv4 interface address at three-bit prefix boundary",
			message: ipv1.InterfaceAddress_builder{
				InterfaceName: proto.String("ethernet1/1"),
				Address:       v4Address(192, 0, 2, 255),
				Prefix:        v4Prefix([]byte{192, 0, 2, 224}, 27),
			}.Build(),
			wantValid: true,
		},
		{
			name: "IPv6 interface address outside prefix",
			message: ipv1.InterfaceAddress_builder{
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
			message: ipv1.InterfaceAddress_builder{
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
			message: ipv1.InterfaceAddress_builder{
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
			message: ipv1.InterfaceAddress_builder{
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
			message: ipv1.NeighborEntry_builder{
				InterfaceName: proto.String("ethernet1/1"),
				Ip:            v4Address(192, 0, 2, 2),
				IsRouter:      proto.Bool(true),
			}.Build(),
			wantValid: false,
		},
		{
			name: "neighbor with unresolved link-layer address",
			message: ipv1.NeighborEntry_builder{
				InterfaceName: proto.String("ethernet1/1"),
				Ip:            v4Address(192, 0, 2, 2),
			}.Build(),
			wantValid: true,
		},
		{
			name: "neighbor with a resolved EUI-48 address",
			message: ipv1.NeighborEntry_builder{
				InterfaceName: proto.String("ethernet1/1"),
				Ip:            v4Address(192, 0, 2, 2),
				Mac: addrv1.MacAddress_builder{
					Eui48: addrv1.Eui48Address_builder{
						Octets: []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
					}.Build(),
				}.Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "neighbor with a resolved EUI-64 address",
			message: ipv1.NeighborEntry_builder{
				InterfaceName: proto.String("ethernet1/1"),
				Ip:            v4Address(192, 0, 2, 2),
				Mac: addrv1.MacAddress_builder{
					Eui64: addrv1.Eui64Address_builder{
						Octets: []byte{0x00, 0x11, 0x22, 0xff, 0xfe, 0x33, 0x44, 0x55},
					}.Build(),
				}.Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "neighbor rejects an empty link-layer wrapper",
			message: ipv1.NeighborEntry_builder{
				InterfaceName: proto.String("ethernet1/1"),
				Ip:            v4Address(192, 0, 2, 2),
				Mac:           addrv1.MacAddress_builder{}.Build(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "IPv6 router neighbor",
			message: ipv1.NeighborEntry_builder{
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

func TestAddressOriginWireValues(t *testing.T) {
	// Readers depend on these wire numbers retaining their meanings. Numbers 4
	// and 5 once meant identifier generation and stay reserved.
	origins := map[ipv1.AddressOrigin]int32{
		ipv1.AddressOrigin_ADDRESS_ORIGIN_STATIC:     2,
		ipv1.AddressOrigin_ADDRESS_ORIGIN_DHCP:       3,
		ipv1.AddressOrigin_ADDRESS_ORIGIN_SLAAC:      6,
		ipv1.AddressOrigin_ADDRESS_ORIGIN_LINK_LOCAL: 7,
	}
	for origin, want := range origins {
		if got := int32(origin); got != want {
			t.Errorf("%s = %d, want %d", origin, got, want)
		}
	}
	values := ipv1.AddressOrigin(0).Descriptor().Values()
	for _, number := range []protoreflect.EnumNumber{4, 5} {
		if value := values.ByNumber(number); value != nil {
			t.Errorf("AddressOrigin number %d is reused by %s", number, value.Name())
		}
	}
}

func TestInterfaceIdentifierMethodIsIpv6Only(t *testing.T) {
	v6 := ipv1.InterfaceAddress_builder{
		InterfaceName: proto.String("ethernet1/1"),
		Address: addrv1.IpAddress_builder{
			V6: addrv1.Ipv6Address_builder{Octets: []byte{
				0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0,
				0, 0, 0, 0, 0, 0, 0, 1,
			}}.Build(),
		}.Build(),
		Prefix: addrv1.IpPrefix_builder{
			V6: addrv1.Ipv6Prefix_builder{
				Address: addrv1.Ipv6Address_builder{Octets: []byte{
					0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0,
					0, 0, 0, 0, 0, 0, 0, 0,
				}}.Build(),
				Length: proto.Uint32(64),
			}.Build(),
		}.Build(),
		Origin:    ipv1.AddressOrigin_ADDRESS_ORIGIN_SLAAC.Enum(),
		IidMethod: ipv1.InterfaceIdentifierMethod_INTERFACE_IDENTIFIER_METHOD_MODIFIED_EUI64.Enum(),
	}.Build()
	if err := protovalidate.Validate(v6); err != nil {
		t.Errorf("IPv6 address with an interface identifier method: %v", err)
	}

	v4 := ipv1.InterfaceAddress_builder{
		InterfaceName: proto.String("ethernet1/1"),
		Address: addrv1.IpAddress_builder{
			V4: addrv1.Ipv4Address_builder{Octets: []byte{192, 0, 2, 1}}.Build(),
		}.Build(),
		Prefix: addrv1.IpPrefix_builder{
			V4: addrv1.Ipv4Prefix_builder{
				Address: addrv1.Ipv4Address_builder{Octets: []byte{192, 0, 2, 0}}.Build(),
				Length:  proto.Uint32(24),
			}.Build(),
		}.Build(),
		IidMethod: ipv1.InterfaceIdentifierMethod_INTERFACE_IDENTIFIER_METHOD_MODIFIED_EUI64.Enum(),
	}.Build()
	errsOn(t, v4, "interface_address.iid_method_is_ipv6_only")
}
