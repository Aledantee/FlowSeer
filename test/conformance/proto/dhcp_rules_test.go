package conformance

import (
	"bytes"
	"net/netip"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	dhcpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/dhcp/v1"
)

func validDhcpv4Lease() *dhcpv1.Dhcpv4Lease_builder {
	return &dhcpv1.Dhcpv4Lease_builder{
		NetworkInstance: proto.String("default"),
		Address: addrv1.Ipv4Address_builder{
			Octets: []byte{192, 0, 2, 10},
		}.Build(),
	}
}

func TestDhcpv4LeaseRules(t *testing.T) {
	withoutNetInst := validDhcpv4Lease()
	withoutNetInst.NetworkInstance = nil

	emptyNetInst := validDhcpv4Lease()
	emptyNetInst.NetworkInstance = proto.String("")

	netInst256 := validDhcpv4Lease()
	netInst256.NetworkInstance = proto.String(strings.Repeat("a", 256))

	withoutAddr := validDhcpv4Lease()
	withoutAddr.Address = nil

	cid1 := validDhcpv4Lease()
	cid1.ClientIdentifier = []byte{1}

	cid2 := validDhcpv4Lease()
	cid2.ClientIdentifier = []byte{1, 2}

	cid255 := validDhcpv4Lease()
	cid255.ClientIdentifier = bytes.Repeat([]byte{1}, 255)

	cid256 := validDhcpv4Lease()
	cid256.ClientIdentifier = bytes.Repeat([]byte{1}, 256)

	withExpiresAt := validDhcpv4Lease()
	withExpiresAt.ExpiresAt = &timestamppb.Timestamp{Seconds: 1000}

	withInfiniteTrue := validDhcpv4Lease()
	withInfiniteTrue.Infinite = proto.Bool(true)

	withInfiniteFalse := validDhcpv4Lease()
	withInfiniteFalse.Infinite = proto.Bool(false)

	poolName256 := validDhcpv4Lease()
	poolName256.PoolName = proto.String(strings.Repeat("p", 256))

	hostName256 := validDhcpv4Lease()
	hostName256.HostName = proto.String(strings.Repeat("h", 256))

	runFieldCases(t, []fieldCase{
		{name: "valid lease with no expiry arm passes", message: validDhcpv4Lease().Build()},
		{name: "valid lease with expires_at passes", message: withExpiresAt.Build()},
		{name: "valid lease with infinite true passes", message: withInfiniteTrue.Build()},
		{name: "lease without network_instance fails", message: withoutNetInst.Build(), wantField: "network_instance", wantText: "value is required"},
		{name: "lease with empty network_instance fails", message: emptyNetInst.Build(), wantField: "network_instance", wantText: "network instance name"},
		{name: "lease with 256 char network_instance fails", message: netInst256.Build(), wantField: "network_instance", wantText: "network instance name"},
		{name: "lease without address fails", message: withoutAddr.Build(), wantField: "address", wantText: "value is required"},
		{name: "client_identifier 1 octet fails", message: cid1.Build(), wantField: "client_identifier", wantText: "at least 2"},
		{name: "client_identifier 2 octets passes", message: cid2.Build()},
		{name: "client_identifier 255 octets passes", message: cid255.Build()},
		{name: "client_identifier 256 octets fails", message: cid256.Build(), wantField: "client_identifier", wantText: "at most 255"},
		{name: "infinite false fails", message: withInfiniteFalse.Build(), wantField: "infinite", wantText: "must equal true"},
		{name: "pool_name 256 chars fails", message: poolName256.Build(), wantField: "pool_name", wantText: "at most 255"},
		{name: "host_name 256 chars fails", message: hostName256.Build(), wantField: "host_name", wantText: "at most 255"},
	})
}

func validDhcpv4Pool() *dhcpv1.Dhcpv4Pool_builder {
	return &dhcpv1.Dhcpv4Pool_builder{
		NetworkInstance: proto.String("default"),
		Name:            proto.String("pool1"),
	}
}

func TestDhcpv4PoolRules(t *testing.T) {
	withoutNetInst := validDhcpv4Pool()
	withoutNetInst.NetworkInstance = nil

	withoutName := validDhcpv4Pool()
	withoutName.Name = nil

	emptyName := validDhcpv4Pool()
	emptyName.Name = proto.String("")

	name256 := validDhcpv4Pool()
	name256.Name = proto.String(strings.Repeat("p", 256))

	leasedLeTotal := validDhcpv4Pool()
	leasedLeTotal.TotalAddresses = proto.Uint32(100)
	leasedLeTotal.LeasedAddresses = proto.Uint32(50)

	leasedGtTotal := validDhcpv4Pool()
	leasedGtTotal.TotalAddresses = proto.Uint32(10)
	leasedGtTotal.LeasedAddresses = proto.Uint32(20)

	domain253 := validDhcpv4Pool()
	domain253.DomainName = proto.String(strings.Repeat("d", 253))

	domain254 := validDhcpv4Pool()
	domain254.DomainName = proto.String(strings.Repeat("d", 254))

	leaseDuration0 := validDhcpv4Pool()
	leaseDuration0.LeaseDuration = &durationpb.Duration{Seconds: 0}

	leaseDuration1h := validDhcpv4Pool()
	leaseDuration1h.LeaseDuration = &durationpb.Duration{Seconds: 3600}

	infiniteTrue := validDhcpv4Pool()
	infiniteTrue.Infinite = proto.Bool(true)

	infiniteFalse := validDhcpv4Pool()
	infiniteFalse.Infinite = proto.Bool(false)

	runFieldCases(t, []fieldCase{
		{name: "valid pool passes", message: validDhcpv4Pool().Build()},
		{name: "without network_instance fails", message: withoutNetInst.Build(), wantField: "network_instance", wantText: "value is required"},
		{name: "without name fails", message: withoutName.Build(), wantField: "name", wantText: "value is required"},
		{name: "empty name fails", message: emptyName.Build(), wantField: "name", wantText: "at least 1"},
		{name: "name 256 chars fails", message: name256.Build(), wantField: "name", wantText: "at most 255"},
		{name: "leased <= total passes", message: leasedLeTotal.Build()},
		{name: "leased > total fails", message: leasedGtTotal.Build(), wantRule: "dhcpv4_pool.leased_le_total"},
		{name: "domain_name 253 chars passes", message: domain253.Build()},
		{name: "domain_name 254 chars fails", message: domain254.Build(), wantField: "domain_name", wantText: "at most 253"},
		{name: "lease_duration 0s fails", message: leaseDuration0.Build(), wantField: "lease_duration", wantText: "greater than 0s"},
		{name: "lease_duration 1h passes", message: leaseDuration1h.Build()},
		{name: "infinite true passes", message: infiniteTrue.Build()},
		{name: "infinite false fails", message: infiniteFalse.Build(), wantField: "infinite", wantText: "must equal true"},
	})
}

func validDhcpv6Binding() *dhcpv1.Dhcpv6Binding_builder {
	v6Addr := netip.MustParseAddr("2001:db8::1").As16()
	return &dhcpv1.Dhcpv6Binding_builder{
		NetworkInstance: proto.String("default"),
		Duid:            []byte{1, 2, 3},
		IaNa: []*dhcpv1.Dhcpv6IaNa{
			dhcpv1.Dhcpv6IaNa_builder{
				Iaid: proto.Uint32(1),
				Addresses: []*dhcpv1.Dhcpv6IaAddress{
					dhcpv1.Dhcpv6IaAddress_builder{
						Address: addrv1.Ipv6Address_builder{Octets: v6Addr[:]}.Build(),
					}.Build(),
				},
			}.Build(),
		},
	}
}

func TestDhcpv6BindingRules(t *testing.T) {
	v6PrefixAddr := netip.MustParseAddr("2001:db8::").As16()
	v6PrefixWithHostBits := netip.MustParseAddr("2001:db8::1").As16()

	withoutNetInst := validDhcpv6Binding()
	withoutNetInst.NetworkInstance = nil

	withoutDuid := validDhcpv6Binding()
	withoutDuid.Duid = nil

	duid2 := validDhcpv6Binding()
	duid2.Duid = []byte{1, 2}

	duid3 := validDhcpv6Binding()
	duid3.Duid = []byte{1, 2, 3}

	duid130 := validDhcpv6Binding()
	duid130.Duid = bytes.Repeat([]byte{1}, 130)

	duid131 := validDhcpv6Binding()
	duid131.Duid = bytes.Repeat([]byte{1}, 131)

	noIa := validDhcpv6Binding()
	noIa.IaNa = nil
	noIa.IaPd = nil

	withIaPdOnly := &dhcpv1.Dhcpv6Binding_builder{
		NetworkInstance: proto.String("default"),
		Duid:            []byte{1, 2, 3},
		IaPd: []*dhcpv1.Dhcpv6IaPd{
			dhcpv1.Dhcpv6IaPd_builder{
				Iaid: proto.Uint32(1),
				Prefixes: []*dhcpv1.Dhcpv6IaPrefix{
					dhcpv1.Dhcpv6IaPrefix_builder{
						Prefix: addrv1.Ipv6Prefix_builder{
							Address: addrv1.Ipv6Address_builder{Octets: v6PrefixAddr[:]}.Build(),
							Length:  proto.Uint32(48),
						}.Build(),
					}.Build(),
				},
			}.Build(),
		},
	}

	dupIaNa := validDhcpv6Binding()
	dupIaNa.IaNa = []*dhcpv1.Dhcpv6IaNa{
		dhcpv1.Dhcpv6IaNa_builder{Iaid: proto.Uint32(1)}.Build(),
		dhcpv1.Dhcpv6IaNa_builder{Iaid: proto.Uint32(1)}.Build(),
	}

	uniqueIaNa := validDhcpv6Binding()
	uniqueIaNa.IaNa = []*dhcpv1.Dhcpv6IaNa{
		dhcpv1.Dhcpv6IaNa_builder{Iaid: proto.Uint32(1)}.Build(),
		dhcpv1.Dhcpv6IaNa_builder{Iaid: proto.Uint32(2)}.Build(),
	}

	dupIaPd := validDhcpv6Binding()
	dupIaPd.IaPd = []*dhcpv1.Dhcpv6IaPd{
		dhcpv1.Dhcpv6IaPd_builder{Iaid: proto.Uint32(1)}.Build(),
		dhcpv1.Dhcpv6IaPd_builder{Iaid: proto.Uint32(1)}.Build(),
	}

	prefixWithHostBits := &dhcpv1.Dhcpv6Binding_builder{
		NetworkInstance: proto.String("default"),
		Duid:            []byte{1, 2, 3},
		IaPd: []*dhcpv1.Dhcpv6IaPd{
			dhcpv1.Dhcpv6IaPd_builder{
				Iaid: proto.Uint32(1),
				Prefixes: []*dhcpv1.Dhcpv6IaPrefix{
					dhcpv1.Dhcpv6IaPrefix_builder{
						Prefix: addrv1.Ipv6Prefix_builder{
							Address: addrv1.Ipv6Address_builder{Octets: v6PrefixWithHostBits[:]}.Build(),
							Length:  proto.Uint32(48),
						}.Build(),
					}.Build(),
				},
			}.Build(),
		},
	}

	runFieldCases(t, []fieldCase{
		{name: "valid binding with ia_na passes", message: validDhcpv6Binding().Build()},
		{name: "valid binding with ia_pd passes", message: withIaPdOnly.Build()},
		{name: "without network_instance fails", message: withoutNetInst.Build(), wantField: "network_instance", wantText: "value is required"},
		{name: "without duid fails", message: withoutDuid.Build(), wantField: "duid", wantText: "value is required"},
		{name: "duid 2 octets fails", message: duid2.Build(), wantField: "duid", wantText: "at least 3"},
		{name: "duid 3 octets passes", message: duid3.Build()},
		{name: "duid 130 octets passes", message: duid130.Build()},
		{name: "duid 131 octets fails", message: duid131.Build(), wantField: "duid", wantText: "at most 130"},
		{name: "binding with neither ia_na nor ia_pd fails", message: noIa.Build(), wantRule: "dhcpv6_binding.has_ia"},
		{name: "duplicate ia_na iaid fails", message: dupIaNa.Build(), wantRule: "dhcpv6_binding.ia_na_iaid_unique"},
		{name: "unique ia_na iaids pass", message: uniqueIaNa.Build()},
		{name: "duplicate ia_pd iaid fails", message: dupIaPd.Build(), wantRule: "dhcpv6_binding.ia_pd_iaid_unique"},
		{name: "prefix with host bits set fails", message: prefixWithHostBits.Build(), wantRule: "ipv6_prefix.masked"},
	})
}

func TestDhcpServerCountersRules(t *testing.T) {
	validV4 := &dhcpv1.Dhcpv4ServerCounters_builder{
		Messages: []*dhcpv1.Dhcpv4MessageCount{
			dhcpv1.Dhcpv4MessageCount_builder{
				MessageType:      dhcpv1.Dhcpv4MessageType_DHCPV4_MESSAGE_TYPE_DISCOVER.Enum(),
				ReceivedMessages: proto.Uint64(10),
			}.Build(),
			dhcpv1.Dhcpv4MessageCount_builder{
				MessageType:  dhcpv1.Dhcpv4MessageType_DHCPV4_MESSAGE_TYPE_OFFER.Enum(),
				SentMessages: proto.Uint64(10),
			}.Build(),
		},
	}

	dupV4 := &dhcpv1.Dhcpv4ServerCounters_builder{
		Messages: []*dhcpv1.Dhcpv4MessageCount{
			dhcpv1.Dhcpv4MessageCount_builder{
				MessageType:      dhcpv1.Dhcpv4MessageType_DHCPV4_MESSAGE_TYPE_REQUEST.Enum(),
				ReceivedMessages: proto.Uint64(5),
			}.Build(),
			dhcpv1.Dhcpv4MessageCount_builder{
				MessageType:      dhcpv1.Dhcpv4MessageType_DHCPV4_MESSAGE_TYPE_REQUEST.Enum(),
				ReceivedMessages: proto.Uint64(10),
			}.Build(),
		},
	}

	v4Type0 := &dhcpv1.Dhcpv4ServerCounters_builder{
		Messages: []*dhcpv1.Dhcpv4MessageCount{
			dhcpv1.Dhcpv4MessageCount_builder{
				MessageType: dhcpv1.Dhcpv4MessageType_DHCPV4_MESSAGE_TYPE_UNSPECIFIED.Enum(),
			}.Build(),
		},
	}

	v4Type256 := &dhcpv1.Dhcpv4ServerCounters_builder{
		Messages: []*dhcpv1.Dhcpv4MessageCount{
			dhcpv1.Dhcpv4MessageCount_builder{
				MessageType: dhcpv1.Dhcpv4MessageType(256).Enum(),
			}.Build(),
		},
	}

	validV6 := &dhcpv1.Dhcpv6ServerCounters_builder{
		Messages: []*dhcpv1.Dhcpv6MessageCount{
			dhcpv1.Dhcpv6MessageCount_builder{
				MessageType:      dhcpv1.Dhcpv6MessageType_DHCPV6_MESSAGE_TYPE_SOLICIT.Enum(),
				ReceivedMessages: proto.Uint64(20),
			}.Build(),
			dhcpv1.Dhcpv6MessageCount_builder{
				MessageType:  dhcpv1.Dhcpv6MessageType_DHCPV6_MESSAGE_TYPE_ADVERTISE.Enum(),
				SentMessages: proto.Uint64(20),
			}.Build(),
		},
	}

	dupV6 := &dhcpv1.Dhcpv6ServerCounters_builder{
		Messages: []*dhcpv1.Dhcpv6MessageCount{
			dhcpv1.Dhcpv6MessageCount_builder{
				MessageType:      dhcpv1.Dhcpv6MessageType_DHCPV6_MESSAGE_TYPE_REQUEST.Enum(),
				ReceivedMessages: proto.Uint64(5),
			}.Build(),
			dhcpv1.Dhcpv6MessageCount_builder{
				MessageType:      dhcpv1.Dhcpv6MessageType_DHCPV6_MESSAGE_TYPE_REQUEST.Enum(),
				ReceivedMessages: proto.Uint64(10),
			}.Build(),
		},
	}

	v6Type0 := &dhcpv1.Dhcpv6ServerCounters_builder{
		Messages: []*dhcpv1.Dhcpv6MessageCount{
			dhcpv1.Dhcpv6MessageCount_builder{
				MessageType: dhcpv1.Dhcpv6MessageType_DHCPV6_MESSAGE_TYPE_UNSPECIFIED.Enum(),
			}.Build(),
		},
	}

	v6Type256 := &dhcpv1.Dhcpv6ServerCounters_builder{
		Messages: []*dhcpv1.Dhcpv6MessageCount{
			dhcpv1.Dhcpv6MessageCount_builder{
				MessageType: dhcpv1.Dhcpv6MessageType(256).Enum(),
			}.Build(),
		},
	}

	runFieldCases(t, []fieldCase{
		{name: "valid v4 server counters pass", message: validV4.Build()},
		{name: "duplicate v4 message_type fails", message: dupV4.Build(), wantRule: "dhcpv4_server_counters.message_type_unique"},
		{name: "v4 message_type 0 fails", message: v4Type0.Build(), wantField: "messages[0].message_type", wantText: "must not be in list"},
		{name: "v4 message_type 256 fails", message: v4Type256.Build(), wantRule: "dhcpv4_message_count.message_type"},
		{name: "valid v6 server counters pass", message: validV6.Build()},
		{name: "duplicate v6 message_type fails", message: dupV6.Build(), wantRule: "dhcpv6_server_counters.message_type_unique"},
		{name: "v6 message_type 0 fails", message: v6Type0.Build(), wantField: "messages[0].message_type", wantText: "must not be in list"},
		{name: "v6 message_type 256 fails", message: v6Type256.Build(), wantRule: "dhcpv6_message_count.message_type"},
	})
}

func validDhcpSnoopingBinding(t *testing.T) *dhcpv1.DhcpSnoopingBinding_builder {
	t.Helper()
	return &dhcpv1.DhcpSnoopingBinding_builder{
		NetworkInstance: proto.String("default"),
		VlanId:          proto.Uint32(10),
		Mac: addrv1.MacAddress_builder{
			Eui48: addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4, 5}}.Build(),
		}.Build(),
		Address:       ipAddress(t, "192.0.2.1"),
		InterfaceName: proto.String("eth0"),
	}
}

func TestDhcpSnoopingBindingRules(t *testing.T) {
	withoutNetInst := validDhcpSnoopingBinding(t)
	withoutNetInst.NetworkInstance = nil

	withoutVlan := validDhcpSnoopingBinding(t)
	withoutVlan.VlanId = nil

	vlan0 := validDhcpSnoopingBinding(t)
	vlan0.VlanId = proto.Uint32(0)

	vlan4095 := validDhcpSnoopingBinding(t)
	vlan4095.VlanId = proto.Uint32(4095)

	vlan1 := validDhcpSnoopingBinding(t)
	vlan1.VlanId = proto.Uint32(1)

	vlan4094 := validDhcpSnoopingBinding(t)
	vlan4094.VlanId = proto.Uint32(4094)

	withoutMac := validDhcpSnoopingBinding(t)
	withoutMac.Mac = nil

	withoutAddr := validDhcpSnoopingBinding(t)
	withoutAddr.Address = nil

	withoutIf := validDhcpSnoopingBinding(t)
	withoutIf.InterfaceName = nil

	expiresInNegative := validDhcpSnoopingBinding(t)
	expiresInNegative.ExpiresIn = &durationpb.Duration{Seconds: -1}

	expiresIn0 := validDhcpSnoopingBinding(t)
	expiresIn0.ExpiresIn = &durationpb.Duration{Seconds: 0}

	expiresIn300 := validDhcpSnoopingBinding(t)
	expiresIn300.ExpiresIn = &durationpb.Duration{Seconds: 300}

	runFieldCases(t, []fieldCase{
		{name: "valid snooping binding passes", message: validDhcpSnoopingBinding(t).Build()},
		{name: "without network_instance fails", message: withoutNetInst.Build(), wantField: "network_instance", wantText: "value is required"},
		{name: "without vlan_id fails", message: withoutVlan.Build(), wantField: "vlan_id", wantText: "value is required"},
		{name: "vlan_id 0 fails", message: vlan0.Build(), wantField: "vlan_id", wantText: "usable IEEE 802.1Q VLAN identifier"},
		{name: "vlan_id 4095 fails", message: vlan4095.Build(), wantField: "vlan_id", wantText: "usable IEEE 802.1Q VLAN identifier"},
		{name: "vlan_id 1 passes", message: vlan1.Build()},
		{name: "vlan_id 4094 passes", message: vlan4094.Build()},
		{name: "without mac fails", message: withoutMac.Build(), wantField: "mac", wantText: "value is required"},
		{name: "without address fails", message: withoutAddr.Build(), wantField: "address", wantText: "value is required"},
		{name: "without interface_name fails", message: withoutIf.Build(), wantField: "interface_name", wantText: "value is required"},
		{name: "negative expires_in fails", message: expiresInNegative.Build(), wantField: "expires_in", wantText: "greater than or equal to 0s"},
		{name: "expires_in 0s passes", message: expiresIn0.Build()},
		{name: "expires_in 300s passes", message: expiresIn300.Build()},
	})
}
