package conformance

import (
	"errors"
	"strings"
	"testing"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	routingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/routing/v1"
)

func gatewayNextHop(octets ...byte) *routingv1.NextHop {
	return routingv1.NextHop_builder{
		Forwarding: routingv1.ForwardingNextHop_builder{
			Address: addrv1.IpAddress_builder{
				V4: addrv1.Ipv4Address_builder{Octets: octets}.Build(),
			}.Build(),
		}.Build(),
	}.Build()
}

func defaultPrefix() *addrv1.IpPrefix {
	return addrv1.IpPrefix_builder{
		V4: addrv1.Ipv4Prefix_builder{
			Address: addrv1.Ipv4Address_builder{Octets: []byte{0, 0, 0, 0}}.Build(),
			Length:  proto.Uint32(0),
		}.Build(),
	}.Build()
}

// errsAtField asserts msg fails validation with a violation on the field path
// whose message contains text, so a failure caused by some other rule does
// not pass for the one under test.
func errsAtField(t *testing.T, msg proto.Message, field, text string) {
	t.Helper()

	err := protovalidate.Validate(msg)
	if err == nil {
		t.Fatalf("message was valid, want a violation at %s", field)
	}
	var validationErr *protovalidate.ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("got %T, want *protovalidate.ValidationError: %v", err, err)
	}
	for _, violation := range validationErr.Violations {
		if protovalidate.FieldPathString(violation.Proto.GetField()) == field &&
			strings.Contains(violation.Proto.GetMessage(), text) {
			return
		}
	}
	t.Errorf("no violation at %s containing %q; got %v", field, text, err)
}

// TestStaticDefaultRouteRoundTrip holds a static default route in the default
// instance through validation and the wire, keeping the static source
// protocol's registry value.
func TestStaticDefaultRouteRoundTrip(t *testing.T) {
	route := routingv1.Route_builder{
		NetworkInstance:   proto.String("default"),
		DestinationPrefix: defaultPrefix(),
		SourceProtocol:    routingv1.RouteSourceProtocol_ROUTE_SOURCE_PROTOCOL_NETMGMT.Enum(),
		NextHopGroup: routingv1.NextHopGroup_builder{
			NextHops: []*routingv1.NextHop{gatewayNextHop(192, 0, 2, 1)},
		}.Build(),
	}.Build()
	if err := protovalidate.Validate(route); err != nil {
		t.Fatalf("static default route is invalid: %v", err)
	}

	wire, err := proto.Marshal(route)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := &routingv1.Route{}
	if err := proto.Unmarshal(wire, got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !proto.Equal(got, route) {
		t.Errorf("round trip changed the route: got %v, want %v", got, route)
	}
	if !got.HasSourceProtocol() || got.GetSourceProtocol() != routingv1.RouteSourceProtocol_ROUTE_SOURCE_PROTOCOL_NETMGMT {
		t.Errorf("source protocol: got %v, want NETMGMT", got.GetSourceProtocol())
	}
	if n := int32(got.GetSourceProtocol()); n != 3 {
		t.Errorf("NETMGMT encodes as %d, want the IANAipRouteProtocol value 3", n)
	}
}

func TestRouteRules(t *testing.T) {
	nextHops := routingv1.NextHopGroup_builder{
		NextHops: []*routingv1.NextHop{gatewayNextHop(192, 0, 2, 1)},
	}.Build()

	t.Run("network instance absent", func(t *testing.T) {
		errsAtField(t, routingv1.Route_builder{
			DestinationPrefix: defaultPrefix(),
			NextHopGroup:      nextHops,
		}.Build(), "network_instance", "value is required")
	})
	t.Run("network instance empty", func(t *testing.T) {
		errsOn(t, routingv1.Route_builder{
			NetworkInstance:   proto.String(""),
			DestinationPrefix: defaultPrefix(),
		}.Build(), "string.network_instance_name")
	})
	t.Run("destination prefix absent", func(t *testing.T) {
		errsAtField(t, routingv1.Route_builder{
			NetworkInstance: proto.String("default"),
			NextHopGroup:    nextHops,
		}.Build(), "destination_prefix", "value is required")
	})

	runValidationCases(t, []validationCase{
		{
			name: "every field set",
			message: routingv1.Route_builder{
				NetworkInstance:   proto.String("vrf-red"),
				DestinationPrefix: defaultPrefix(),
				SourceProtocol:    routingv1.RouteSourceProtocol_ROUTE_SOURCE_PROTOCOL_OSPF.Enum(),
				Preference:        proto.Uint32(110),
				Metric:            proto.Uint32(20),
				NextHopGroup:      nextHops,
				Active:            proto.Bool(true),
				TableType:         routingv1.RouteTableType_ROUTE_TABLE_TYPE_RIB.Enum(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "only the key set",
			message: routingv1.Route_builder{
				NetworkInstance:   proto.String("default"),
				DestinationPrefix: defaultPrefix(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "invalid next hop inside the group",
			message: routingv1.Route_builder{
				NetworkInstance:   proto.String("default"),
				DestinationPrefix: defaultPrefix(),
				NextHopGroup: routingv1.NextHopGroup_builder{
					NextHops: []*routingv1.NextHop{routingv1.NextHop_builder{}.Build()},
				}.Build(),
			}.Build(),
			wantValid: false,
		},
	})
}

// TestNextHopTarget holds the required-oneof contract: no arm fails naming
// the oneof, and exactly one arm of either kind passes.
func TestNextHopTarget(t *testing.T) {
	t.Run("no arm fails naming the oneof", func(t *testing.T) {
		errsAtField(t, routingv1.NextHop_builder{}.Build(), "target", "exactly one field is required")
	})

	runValidationCases(t, []validationCase{
		{name: "only forwarding set", message: gatewayNextHop(192, 0, 2, 1), wantValid: true},
		{
			name: "only special set",
			message: routingv1.NextHop_builder{
				Special: routingv1.SpecialNextHop_SPECIAL_NEXT_HOP_BLACKHOLE.Enum(),
			}.Build(),
			wantValid: true,
		},
	})

	// Wire bytes carrying both arms decode to the last one: a next hop that
	// forwards and discards at once cannot reach a consumer.
	t.Run("both arms on the wire decode to the last", func(t *testing.T) {
		forwarding, err := proto.Marshal(gatewayNextHop(192, 0, 2, 1))
		if err != nil {
			t.Fatalf("marshal forwarding: %v", err)
		}
		special, err := proto.Marshal(routingv1.NextHop_builder{
			Special: routingv1.SpecialNextHop_SPECIAL_NEXT_HOP_BLACKHOLE.Enum(),
		}.Build())
		if err != nil {
			t.Fatalf("marshal special: %v", err)
		}
		got := &routingv1.NextHop{}
		if err := proto.Unmarshal(append(forwarding, special...), got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !got.HasSpecial() || got.HasForwarding() {
			t.Errorf("got special=%t forwarding=%t, want only special", got.HasSpecial(), got.HasForwarding())
		}
	})
}

func TestForwardingNextHopRules(t *testing.T) {
	t.Run("neither interface nor address", func(t *testing.T) {
		errsOn(t, routingv1.ForwardingNextHop_builder{}.Build(), "forwarding_next_hop.target_present")
	})
	t.Run("empty interface name", func(t *testing.T) {
		errsOn(t, routingv1.ForwardingNextHop_builder{
			InterfaceName: proto.String(""),
		}.Build(), "string.interface_name")
	})

	gateway := addrv1.IpAddress_builder{
		V6: addrv1.Ipv6Address_builder{Octets: []byte{0x20, 0x01, 0x0d, 0xb8, 15: 1}}.Build(),
	}.Build()
	runValidationCases(t, []validationCase{
		{
			name:      "interface only",
			message:   routingv1.ForwardingNextHop_builder{InterfaceName: proto.String("ethernet1/1")}.Build(),
			wantValid: true,
		},
		{
			name:      "address only",
			message:   routingv1.ForwardingNextHop_builder{Address: gateway}.Build(),
			wantValid: true,
		},
		{
			name: "interface and address",
			message: routingv1.ForwardingNextHop_builder{
				InterfaceName: proto.String("ethernet1/1"),
				Address:       gateway,
			}.Build(),
			wantValid: true,
		},
		{
			name: "malformed address",
			message: routingv1.ForwardingNextHop_builder{
				Address: addrv1.IpAddress_builder{}.Build(),
			}.Build(),
			wantValid: false,
		},
	})
}

func TestSpecialNextHopRules(t *testing.T) {
	runValidationCases(t, []validationCase{
		{
			name: "unspecified",
			message: routingv1.NextHop_builder{
				Special: routingv1.SpecialNextHop_SPECIAL_NEXT_HOP_UNSPECIFIED.Enum(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "undefined",
			message: routingv1.NextHop_builder{
				Special: routingv1.SpecialNextHop(99).Enum(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "receive",
			message: routingv1.NextHop_builder{
				Special: routingv1.SpecialNextHop_SPECIAL_NEXT_HOP_RECEIVE.Enum(),
			}.Build(),
			wantValid: true,
		},
	})
}

func TestNextHopGroupRules(t *testing.T) {
	runValidationCases(t, []validationCase{
		{name: "empty", message: routingv1.NextHopGroup_builder{}.Build(), wantValid: false},
		{
			name: "equal-cost pair",
			message: routingv1.NextHopGroup_builder{
				NextHops: []*routingv1.NextHop{gatewayNextHop(192, 0, 2, 1), gatewayNextHop(192, 0, 2, 2)},
			}.Build(),
			wantValid: true,
		},
	})
}
