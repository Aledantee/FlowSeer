package protoconformance

import (
	"testing"

	"google.golang.org/protobuf/proto"

	packetv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/packet/v1"
)

func TestPacketPrimitiveRules(t *testing.T) {
	tests := []validationCase{
		{
			name:      "transport port range is ordered",
			message:   packetv1.TransportPortRange_builder{Start: proto.Uint32(443), End: proto.Uint32(80)}.Build(),
			wantValid: false,
		},
		{
			name:      "transport port zero is structurally valid",
			message:   packetv1.TransportPortRange_builder{Start: proto.Uint32(0), End: proto.Uint32(0)}.Build(),
			wantValid: true,
		},
		{
			name:      "transport port exceeds wire width",
			message:   packetv1.TransportPortRange_builder{Start: proto.Uint32(0), End: proto.Uint32(65536)}.Build(),
			wantValid: false,
		},
		{
			name:      "transport port match requires a selection",
			message:   packetv1.TransportPortMatch_builder{}.Build(),
			wantValid: false,
		},
		{
			name:      "exact transport port match",
			message:   packetv1.TransportPortMatch_builder{Exact: proto.Uint32(53)}.Build(),
			wantValid: true,
		},
		{
			name:      "TCP flag match requires a predicate",
			message:   packetv1.TcpFlagsMatch_builder{}.Build(),
			wantValid: false,
		},
		{
			name: "TCP flag predicates must be disjoint",
			message: packetv1.TcpFlagsMatch_builder{
				RequiredSet:   []packetv1.TcpFlag{packetv1.TcpFlag_TCP_FLAG_SYN},
				RequiredClear: []packetv1.TcpFlag{packetv1.TcpFlag_TCP_FLAG_SYN},
			}.Build(),
			wantValid: false,
		},
		{
			name: "disjoint TCP flag predicate",
			message: packetv1.TcpFlagsMatch_builder{
				RequiredSet:   []packetv1.TcpFlag{packetv1.TcpFlag_TCP_FLAG_SYN},
				RequiredClear: []packetv1.TcpFlag{packetv1.TcpFlag_TCP_FLAG_ACK},
			}.Build(),
			wantValid: true,
		},
		{
			name:      "ICMP fields require a family",
			message:   packetv1.IcmpFields_builder{}.Build(),
			wantValid: false,
		},
		{
			name:      "ICMPv4 type is required",
			message:   packetv1.IcmpFields_builder{V4: packetv1.Icmpv4Fields_builder{}.Build()}.Build(),
			wantValid: false,
		},
		{
			name: "ICMPv4 echo request",
			message: packetv1.IcmpFields_builder{
				V4: packetv1.Icmpv4Fields_builder{Type: proto.Uint32(8), Code: proto.Uint32(0)}.Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "ICMP type exceeds wire width",
			message: packetv1.IcmpFields_builder{
				V6: packetv1.Icmpv6Fields_builder{Type: proto.Uint32(256)}.Build(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "ICMP matcher requires a predicate",
			message: packetv1.IcmpMatch_builder{
				V4: packetv1.Icmpv4Match_builder{}.Build(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "ICMP matcher accepts a type set",
			message: packetv1.IcmpMatch_builder{
				V6: packetv1.Icmpv6Match_builder{Types: []uint32{128, 129}}.Build(),
			}.Build(),
			wantValid: true,
		},
	}

	runValidationCases(t, tests)
}
