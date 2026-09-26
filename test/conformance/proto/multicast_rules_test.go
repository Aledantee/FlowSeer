package conformance

import (
	"net/netip"
	"testing"

	"google.golang.org/protobuf/proto"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	multicastv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/multicast/v1"
)

func ipAddress(t *testing.T, s string) *addrv1.IpAddress {
	t.Helper()

	addr := netip.MustParseAddr(s)
	if addr.Is4() {
		octets := addr.As4()
		return addrv1.IpAddress_builder{V4: addrv1.Ipv4Address_builder{Octets: octets[:]}.Build()}.Build()
	}
	octets := addr.As16()
	return addrv1.IpAddress_builder{V6: addrv1.Ipv6Address_builder{Octets: octets[:]}.Build()}.Build()
}

// TestGroupMembershipRules holds a snooping membership row to a multicast
// group of either family, to sources and reporters of the group's family,
// to the MLD versions for an IPv6 group, and to its key.
func TestGroupMembershipRules(t *testing.T) {
	membership := func(group string) *multicastv1.GroupMembership_builder {
		return &multicastv1.GroupMembership_builder{
			NetworkInstance: proto.String("default"),
			VlanId:          proto.Uint32(10),
			Group:           ipAddress(t, group),
			InterfaceName:   proto.String("1/1/5"),
		}
	}
	withSource := func(group, source string) *multicastv1.GroupMembership_builder {
		b := membership(group)
		b.Source = ipAddress(t, source)
		return b
	}
	withVersion := func(group string, version uint32) *multicastv1.GroupMembership_builder {
		b := membership(group)
		b.CompatibilityVersion = proto.Uint32(version)
		return b
	}
	withReporter := func(group, reporter string) *multicastv1.GroupMembership_builder {
		b := membership(group)
		b.LastReporter = ipAddress(t, reporter)
		return b
	}
	withoutInstance := membership("239.1.1.1")
	withoutInstance.NetworkInstance = nil
	withoutInterface := membership("239.1.1.1")
	withoutInterface.InterfaceName = nil
	withoutGroup := membership("239.1.1.1")
	withoutGroup.Group = nil

	runFieldCases(t, []fieldCase{
		{name: "IPv4 group", message: membership("239.1.1.1").Build()},
		{name: "lowest IPv4 multicast address", message: membership("224.0.0.0").Build()},
		{name: "highest IPv4 multicast address", message: membership("239.255.255.255").Build()},
		{name: "IPv6 group", message: membership("ff02::1:3").Build()},
		{name: "IGMPv3 source-specific entry", message: withSource("232.1.1.1", "10.0.0.5").Build()},
		{name: "MLDv2 source-specific entry", message: withSource("ff3e::8000:1", "2001:db8::5").Build()},
		{name: "IGMPv3 compatibility mode", message: withVersion("239.1.1.1", 3).Build()},
		{name: "MLDv2 compatibility mode", message: withVersion("ff02::1:3", 2).Build()},
		{name: "IPv4 reporter for an IPv4 group", message: withReporter("239.1.1.1", "10.0.0.7").Build()},
		{name: "unicast IPv4 group", message: membership("10.0.0.1").Build(), wantRule: "group_membership.group_is_multicast"},
		{name: "IPv4 address just above 224.0.0.0/4", message: membership("240.0.0.1").Build(), wantRule: "group_membership.group_is_multicast"},
		{name: "unicast IPv6 group", message: membership("2001:db8::1").Build(), wantRule: "group_membership.group_is_multicast"},
		{name: "IPv6 source for an IPv4 group", message: withSource("239.1.1.1", "2001:db8::5").Build(), wantRule: "group_membership.source_family"},
		{name: "IPv6 reporter for an IPv4 group", message: withReporter("239.1.1.1", "2001:db8::7").Build(), wantRule: "group_membership.last_reporter_family"},
		{name: "IGMPv3 mode for an IPv6 group", message: withVersion("ff02::1:3", 3).Build(), wantRule: "group_membership.mld_compatibility_version"},
		{name: "compatibility version 4", message: withVersion("239.1.1.1", 4).Build(), wantField: "compatibility_version", wantText: "less than or equal to 3"},
		{name: "network instance absent", message: withoutInstance.Build(), wantField: "network_instance", wantText: "value is required"},
		{name: "interface name absent", message: withoutInterface.Build(), wantField: "interface_name", wantText: "value is required"},
		{name: "group absent", message: withoutGroup.Build(), wantField: "group", wantText: "value is required"},
	})
}
