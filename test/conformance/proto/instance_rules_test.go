package conformance

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	instancev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/instance/v1"
)

func TestNetworkInstanceRules(t *testing.T) {
	validRD := instancev1.RouteDistinguisher_builder{
		As2: instancev1.As2RouteDistinguisher_builder{
			Asn:            proto.Uint32(65000),
			AssignedNumber: proto.Uint32(100),
		}.Build(),
	}.Build()

	tests := []validationCase{
		{
			name: "valid default instance",
			message: instancev1.NetworkInstance_builder{
				Name: proto.String("default"),
				Kind: instancev1.NetworkInstanceKind_NETWORK_INSTANCE_KIND_DEFAULT.Enum(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "valid VRF with route distinguisher and description",
			message: instancev1.NetworkInstance_builder{
				Name:               proto.String("vrf-red"),
				Kind:               instancev1.NetworkInstanceKind_NETWORK_INSTANCE_KIND_L3VRF.Enum(),
				RouteDistinguisher: validRD,
				Description:        proto.String("Customer red VRF"),
			}.Build(),
			wantValid: true,
		},
		{
			name: "name absent",
			message: instancev1.NetworkInstance_builder{
				Kind: instancev1.NetworkInstanceKind_NETWORK_INSTANCE_KIND_DEFAULT.Enum(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "name empty",
			message: instancev1.NetworkInstance_builder{
				Name: proto.String(""),
				Kind: instancev1.NetworkInstanceKind_NETWORK_INSTANCE_KIND_DEFAULT.Enum(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "name exceeds 255 characters",
			message: instancev1.NetworkInstance_builder{
				Name: proto.String(strings.Repeat("a", 256)),
				Kind: instancev1.NetworkInstanceKind_NETWORK_INSTANCE_KIND_DEFAULT.Enum(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "name at 255 characters accepted",
			message: instancev1.NetworkInstance_builder{
				Name: proto.String(strings.Repeat("a", 255)),
				Kind: instancev1.NetworkInstanceKind_NETWORK_INSTANCE_KIND_DEFAULT.Enum(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "kind absent",
			message: instancev1.NetworkInstance_builder{
				Name: proto.String("default"),
			}.Build(),
			wantValid: false,
		},
		{
			name: "kind unspecified",
			message: instancev1.NetworkInstance_builder{
				Name: proto.String("default"),
				Kind: instancev1.NetworkInstanceKind_NETWORK_INSTANCE_KIND_UNSPECIFIED.Enum(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "kind undefined",
			message: instancev1.NetworkInstance_builder{
				Name: proto.String("default"),
				Kind: instancev1.NetworkInstanceKind(99).Enum(),
			}.Build(),
			wantValid: false,
		},
	}

	runValidationCases(t, tests)
}

func TestRouteDistinguisherRules(t *testing.T) {
	validIpv4 := addrv1.Ipv4Address_builder{
		Octets: []byte{192, 0, 2, 1},
	}.Build()

	tests := []validationCase{
		{
			name: "As2 format valid",
			message: instancev1.RouteDistinguisher_builder{
				As2: instancev1.As2RouteDistinguisher_builder{
					Asn:            proto.Uint32(65535),
					AssignedNumber: proto.Uint32(100),
				}.Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "As2 format ASN exceeds 65535",
			message: instancev1.RouteDistinguisher_builder{
				As2: instancev1.As2RouteDistinguisher_builder{
					Asn:            proto.Uint32(65536),
					AssignedNumber: proto.Uint32(100),
				}.Build(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "As2 format missing ASN",
			message: instancev1.RouteDistinguisher_builder{
				As2: instancev1.As2RouteDistinguisher_builder{
					AssignedNumber: proto.Uint32(100),
				}.Build(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "As2 format missing assigned number",
			message: instancev1.RouteDistinguisher_builder{
				As2: instancev1.As2RouteDistinguisher_builder{
					Asn: proto.Uint32(65000),
				}.Build(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "Ipv4 format valid",
			message: instancev1.RouteDistinguisher_builder{
				Ipv4: instancev1.Ipv4RouteDistinguisher_builder{
					Ip:             validIpv4,
					AssignedNumber: proto.Uint32(65535),
				}.Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "Ipv4 format assigned number exceeds 65535",
			message: instancev1.RouteDistinguisher_builder{
				Ipv4: instancev1.Ipv4RouteDistinguisher_builder{
					Ip:             validIpv4,
					AssignedNumber: proto.Uint32(65536),
				}.Build(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "Ipv4 format missing IP",
			message: instancev1.RouteDistinguisher_builder{
				Ipv4: instancev1.Ipv4RouteDistinguisher_builder{
					AssignedNumber: proto.Uint32(100),
				}.Build(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "Ipv4 format missing assigned number",
			message: instancev1.RouteDistinguisher_builder{
				Ipv4: instancev1.Ipv4RouteDistinguisher_builder{
					Ip: validIpv4,
				}.Build(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "As4 format valid with 4-octet ASN",
			message: instancev1.RouteDistinguisher_builder{
				As4: instancev1.As4RouteDistinguisher_builder{
					Asn:            proto.Uint32(131072),
					AssignedNumber: proto.Uint32(65535),
				}.Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "As4 format assigned number exceeds 65535",
			message: instancev1.RouteDistinguisher_builder{
				As4: instancev1.As4RouteDistinguisher_builder{
					Asn:            proto.Uint32(65000),
					AssignedNumber: proto.Uint32(65536),
				}.Build(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "As4 format missing ASN",
			message: instancev1.RouteDistinguisher_builder{
				As4: instancev1.As4RouteDistinguisher_builder{
					AssignedNumber: proto.Uint32(100),
				}.Build(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "As4 format missing assigned number",
			message: instancev1.RouteDistinguisher_builder{
				As4: instancev1.As4RouteDistinguisher_builder{
					Asn: proto.Uint32(65000),
				}.Build(),
			}.Build(),
			wantValid: false,
		},
		{
			name:      "RouteDistinguisher without arm set",
			message:   instancev1.RouteDistinguisher_builder{}.Build(),
			wantValid: false,
		},
	}

	runValidationCases(t, tests)
}
