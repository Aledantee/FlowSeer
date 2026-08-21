// Package proto_test holds gate tests for the schemas under spec/proto/.
//
// They live here rather than under src/ because their subject is this
// directory: one walks these .proto sources to enforce import layering, the
// other runs the protovalidate rules the sources declare. buf lint compiles
// those rules but never executes them, so without this package the
// edition-2024 trap — a rule without `required` is skipped on an absent
// field — would pass every gate while enforcing nothing.
package proto_test

import (
	"errors"
	"net"
	"net/netip"
	"slices"
	"testing"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
)

func TestAddressRulesReject(t *testing.T) {
	cases := []struct {
		name string
		msg  proto.Message
		want string // the rule id the violation must carry
	}{
		{"eui48 with seven octets", addrv1.Eui48Address_builder{Octets: make([]byte, 7)}.Build(), "bytes.len"},
		{"eui48 with five octets", addrv1.Eui48Address_builder{Octets: make([]byte, 5)}.Build(), "bytes.len"},
		{"eui48 with no octets", addrv1.Eui48Address_builder{}.Build(), "required"},
		{"eui64 with six octets", addrv1.Eui64Address_builder{Octets: make([]byte, 6)}.Build(), "bytes.len"},
		{"eui64 with no octets", addrv1.Eui64Address_builder{}.Build(), "required"},
		{"mac with no arm", addrv1.MacAddress_builder{}.Build(), "required"},

		{"oui with two octets", addrv1.Oui_builder{Octets: make([]byte, 2)}.Build(), "bytes.len"},
		{"oui with four octets", addrv1.Oui_builder{Octets: make([]byte, 4)}.Build(), "bytes.len"},

		{"ipv4 with five octets", addrv1.Ipv4Address_builder{Octets: make([]byte, 5)}.Build(), "bytes.len"},
		{"ipv4 with sixteen octets", addrv1.Ipv4Address_builder{Octets: make([]byte, 16)}.Build(), "bytes.len"},
		{"ipv6 with four octets", addrv1.Ipv6Address_builder{Octets: make([]byte, 4)}.Build(), "bytes.len"},
		{"ip address with no arm", addrv1.IpAddress_builder{}.Build(), "required"},

		{"ipv4 prefix longer than 32", ipv4Prefix(33), "uint32.lte"},
		{"ipv6 prefix longer than 128", ipv6Prefix(129), "uint32.lte"},
		{
			"ipv4 prefix with no length",
			addrv1.Ipv4Prefix_builder{Address: ipv4(make([]byte, 4))}.Build(),
			"required",
		},
		{
			"ipv4 prefix with no address",
			addrv1.Ipv4Prefix_builder{Length: proto.Uint32(24)}.Build(),
			"required",
		},
		{"ip prefix with no arm", addrv1.IpPrefix_builder{}.Build(), "required"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ruleIDs(t, protovalidate.Validate(tc.msg))
			if !slices.Contains(got, tc.want) {
				t.Errorf("got rule ids %v, want one of them to be %q", got, tc.want)
			}
		})
	}
}

func TestAddressRulesAccept(t *testing.T) {
	cases := []struct {
		name string
		msg  proto.Message
	}{
		{"eui48 with six octets", addrv1.Eui48Address_builder{Octets: make([]byte, 6)}.Build()},
		{"eui64 with eight octets", addrv1.Eui64Address_builder{Octets: make([]byte, 8)}.Build()},
		{"oui with three octets", addrv1.Oui_builder{Octets: make([]byte, 3)}.Build()},
		{"ipv4 with four octets", addrv1.Ipv4Address_builder{Octets: make([]byte, 4)}.Build()},
		{"ipv6 with sixteen octets", addrv1.Ipv6Address_builder{Octets: make([]byte, 16)}.Build()},

		{"mac with the eui48 arm", mac48(make([]byte, 6))},
		{
			"mac with the eui64 arm",
			addrv1.MacAddress_builder{
				Eui64: addrv1.Eui64Address_builder{Octets: make([]byte, 8)}.Build(),
			}.Build(),
		},
		{"ip address with the v4 arm", ipAddr4(make([]byte, 4))},
		{
			"ip address with the v6 arm",
			addrv1.IpAddress_builder{V6: ipv6(make([]byte, 16))}.Build(),
		},

		// Length 0 is the default route, not an absent length.
		{"ipv4 prefix of length zero", ipv4Prefix(0)},
		{"ipv4 prefix of length 32", ipv4Prefix(32)},
		{"ipv6 prefix of length zero", ipv6Prefix(0)},
		{"ipv6 prefix of length 128", ipv6Prefix(128)},

		{"ip prefix with the v4 arm", addrv1.IpPrefix_builder{V4: ipv4Prefix(24)}.Build()},
		{"ip prefix with the v6 arm", addrv1.IpPrefix_builder{V6: ipv6Prefix(64)}.Build()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := protovalidate.Validate(tc.msg); err != nil {
				t.Errorf("got %v, want valid", err)
			}
		})
	}
}

func TestOneofArmIsReported(t *testing.T) {
	m := mac48(make([]byte, 6))
	if got, want := m.WhichKind(), addrv1.MacAddress_Eui48_case; got != want {
		t.Errorf("got %v, want %v", got, want)
	}

	a := ipAddr4(make([]byte, 4))
	if got, want := a.WhichFamily(), addrv1.IpAddress_V4_case; got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestUnsetAddressFieldFiresNoRule is the other half of the required-vs-rule
// contract: a MacAddress that is *set but empty* is invalid, while a field
// holding one that is simply unset is fine. There is no production message
// with such a field yet, so the wrapper is built as a descriptor at runtime.
func TestUnsetAddressFieldFiresNoRule(t *testing.T) {
	md := wrapperDescriptor(t)
	if err := protovalidate.Validate(dynamicpb.NewMessage(md)); err != nil {
		t.Errorf("got %v, want valid", err)
	}
}

// TestBytesRoundTripThroughStdlib exercises the canonical-bytes choice once:
// the payloads have to hand straight to Go's own address types with no
// parsing step, which is the reason they are bytes and not strings.
func TestBytesRoundTripThroughStdlib(t *testing.T) {
	t.Run("eui48 through net.HardwareAddr", func(t *testing.T) {
		const want = "aa:bb:cc:dd:ee:ff"
		hw, err := net.ParseMAC(want)
		if err != nil {
			t.Fatalf("parsing %q: %v", want, err)
		}
		m := mac48(hw)
		if got := net.HardwareAddr(m.GetEui48().GetOctets()).String(); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("ipv4 through netip.Addr", func(t *testing.T) {
		want := netip.MustParseAddr("192.0.2.1")
		a := ipAddr4(want.AsSlice())
		got, ok := netip.AddrFromSlice(a.GetV4().GetOctets())
		if !ok {
			t.Fatalf("got !ok from AddrFromSlice, want an address")
		}
		if got != want {
			t.Errorf("got %v, want %v", got, want)
		}
		if !want.Is4() || a.WhichFamily() != addrv1.IpAddress_V4_case {
			t.Errorf("got arm %v for %v, want the v4 arm", a.WhichFamily(), want)
		}
	})

	t.Run("ipv6 through netip.Addr", func(t *testing.T) {
		want := netip.MustParseAddr("2001:db8::1")
		v6 := ipv6(want.AsSlice())
		a := addrv1.IpAddress_builder{V6: v6}.Build()
		got, ok := netip.AddrFromSlice(a.GetV6().GetOctets())
		if !ok {
			t.Fatalf("got !ok from AddrFromSlice, want an address")
		}
		if got != want {
			t.Errorf("got %v, want %v", got, want)
		}
		if want.Is4() || a.WhichFamily() != addrv1.IpAddress_V6_case {
			t.Errorf("got arm %v for %v, want the v6 arm", a.WhichFamily(), want)
		}
	})

	t.Run("ipv4 prefix through netip.Prefix", func(t *testing.T) {
		want := netip.MustParsePrefix("198.51.100.0/24")
		p := addrv1.Ipv4Prefix_builder{
			Address: ipv4(want.Addr().AsSlice()),
			Length:  proto.Uint32(uint32(want.Bits())),
		}.Build()
		addr, ok := netip.AddrFromSlice(p.GetAddress().GetOctets())
		if !ok {
			t.Fatalf("got !ok from AddrFromSlice, want an address")
		}
		if got := netip.PrefixFrom(addr, int(p.GetLength())); got != want {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

func ipv4(octets []byte) *addrv1.Ipv4Address {
	return addrv1.Ipv4Address_builder{Octets: octets}.Build()
}

func ipv6(octets []byte) *addrv1.Ipv6Address {
	return addrv1.Ipv6Address_builder{Octets: octets}.Build()
}

func mac48(octets []byte) *addrv1.MacAddress {
	return addrv1.MacAddress_builder{
		Eui48: addrv1.Eui48Address_builder{Octets: octets}.Build(),
	}.Build()
}

func ipAddr4(octets []byte) *addrv1.IpAddress {
	return addrv1.IpAddress_builder{V4: ipv4(octets)}.Build()
}

func ipv4Prefix(length uint32) *addrv1.Ipv4Prefix {
	return addrv1.Ipv4Prefix_builder{
		Address: ipv4(make([]byte, 4)),
		Length:  proto.Uint32(length),
	}.Build()
}

func ipv6Prefix(length uint32) *addrv1.Ipv6Prefix {
	return addrv1.Ipv6Prefix_builder{
		Address: ipv6(make([]byte, 16)),
		Length:  proto.Uint32(length),
	}.Build()
}

// wrapperDescriptor builds a one-field message holding an unset, non-required
// MacAddress, resolving the field's type against the generated descriptors.
func wrapperDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()
	fdp := &descriptorpb.FileDescriptorProto{
		Name:       proto.String("flowseer/proto_test/v1/wrapper.proto"),
		Package:    proto.String("flowseer.proto_test.v1"),
		Syntax:     proto.String("editions"),
		Edition:    descriptorpb.Edition_EDITION_2024.Enum(),
		Dependency: []string{"flowseer/net/addr/v1/mac_address.proto"},
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("Wrapper"),
			Field: []*descriptorpb.FieldDescriptorProto{{
				Name:     proto.String("mac"),
				Number:   proto.Int32(1),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
				TypeName: proto.String(".flowseer.net.addr.v1.MacAddress"),
				JsonName: proto.String("mac"),
			}},
		}},
	}
	fd, err := protodesc.NewFile(fdp, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatalf("building the wrapper descriptor: %v", err)
	}
	return fd.Messages().Get(0)
}

func ruleIDs(t *testing.T, err error) []string {
	t.Helper()
	if err == nil {
		t.Fatal("got valid, want a violation")
	}
	var verr *protovalidate.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("got %T (%v), want *protovalidate.ValidationError", err, err)
	}
	ids := make([]string, 0, len(verr.Violations))
	for _, v := range verr.Violations {
		ids = append(ids, v.Proto.GetRuleId())
	}
	return ids
}
