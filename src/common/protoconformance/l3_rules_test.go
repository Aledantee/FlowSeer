package protoconformance

import (
	"testing"

	"buf.build/go/protovalidate"
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

	tests := []struct {
		name      string
		message   proto.Message
		wantValid bool
	}{
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
			name: "IPv4 neighbor rejects router flag",
			message: l3v1.NeighborEntry_builder{
				InterfaceName: proto.String("ethernet1/1"),
				Ip:            v4Address(192, 0, 2, 2),
				IsRouter:      proto.Bool(true),
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := protovalidate.Validate(tt.message)
			gotValid := err == nil
			if gotValid != tt.wantValid {
				t.Errorf("got valid=%t, want %t: %v", gotValid, tt.wantValid, err)
			}
		})
	}
}
