package conformance

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	vrrpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/vrrp/v1"
)

func validVrrpGroup() *vrrpv1.VrrpGroup_builder {
	return &vrrpv1.VrrpGroup_builder{
		InterfaceName: proto.String("eth0"),
		AddressFamily: addrv1.IpVersion_IP_VERSION_V4.Enum(),
		Vrid:          proto.Uint32(1),
		Version:       proto.Uint32(2),
	}
}

func TestVrrpGroupRules(t *testing.T) {
	withoutIf := validVrrpGroup()
	withoutIf.InterfaceName = nil

	withoutAf := validVrrpGroup()
	withoutAf.AddressFamily = nil

	afUnspecified := validVrrpGroup()
	afUnspecified.AddressFamily = addrv1.IpVersion_IP_VERSION_UNSPECIFIED.Enum()

	withoutVrid := validVrrpGroup()
	withoutVrid.Vrid = nil

	vrid0 := validVrrpGroup()
	vrid0.Vrid = proto.Uint32(0)

	vrid256 := validVrrpGroup()
	vrid256.Vrid = proto.Uint32(256)

	vrid255 := validVrrpGroup()
	vrid255.Vrid = proto.Uint32(255)

	priority256 := validVrrpGroup()
	priority256.Priority = proto.Uint32(256)

	priority0 := validVrrpGroup()
	priority0.Priority = proto.Uint32(0)

	priority255 := validVrrpGroup()
	priority255.Priority = proto.Uint32(255)

	v2WithV6 := validVrrpGroup()
	v2WithV6.AddressFamily = addrv1.IpVersion_IP_VERSION_V6.Enum()

	v3WithV6 := validVrrpGroup()
	v3WithV6.Version = proto.Uint32(3)
	v3WithV6.AddressFamily = addrv1.IpVersion_IP_VERSION_V6.Enum()

	interval1005ms := validVrrpGroup()
	interval1005ms.AdvertisementInterval = &durationpb.Duration{Seconds: 1, Nanos: 5000000}

	v3Interval1s := validVrrpGroup()
	v3Interval1s.Version = proto.Uint32(3)
	v3Interval1s.AdvertisementInterval = &durationpb.Duration{Seconds: 1}

	v3Interval41s := validVrrpGroup()
	v3Interval41s.Version = proto.Uint32(3)
	v3Interval41s.AdvertisementInterval = &durationpb.Duration{Seconds: 41}

	v3Interval40950ms := validVrrpGroup()
	v3Interval40950ms.Version = proto.Uint32(3)
	v3Interval40950ms.AdvertisementInterval = &durationpb.Duration{Seconds: 40, Nanos: 950000000}

	v2Interval1500ms := validVrrpGroup()
	v2Interval1500ms.AdvertisementInterval = &durationpb.Duration{Seconds: 1, Nanos: 500000000}

	v2Interval255s := validVrrpGroup()
	v2Interval255s.AdvertisementInterval = &durationpb.Duration{Seconds: 255}

	v4GroupWithV6Virtual := validVrrpGroup()
	v4GroupWithV6Virtual.VirtualAddresses = []*addrv1.IpAddress{ipAddress(t, "2001:db8::1")}

	v4GroupWithV4Virtual := validVrrpGroup()
	v4GroupWithV4Virtual.VirtualAddresses = []*addrv1.IpAddress{ipAddress(t, "192.0.2.1")}

	runFieldCases(t, []fieldCase{
		{name: "valid group", message: validVrrpGroup().Build()},
		{name: "interface_name absent", message: withoutIf.Build(), wantField: "interface_name", wantText: "value is required"},
		{name: "address_family absent", message: withoutAf.Build(), wantField: "address_family", wantText: "value is required"},
		{name: "address_family unspecified", message: afUnspecified.Build(), wantField: "address_family", wantText: "must not be in list"},
		{name: "vrid absent", message: withoutVrid.Build(), wantField: "vrid", wantText: "value is required"},
		{name: "vrid 0", message: vrid0.Build(), wantField: "vrid", wantText: "greater than or equal to 1"},
		{name: "vrid 256", message: vrid256.Build(), wantField: "vrid", wantText: "less than or equal to 255"},
		{name: "vrid 255 passes", message: vrid255.Build()},
		{name: "priority 256", message: priority256.Build(), wantField: "priority", wantText: "less than or equal to 255"},
		{name: "priority 0 passes", message: priority0.Build()},
		{name: "priority 255 passes", message: priority255.Build()},
		{name: "version 2 with IPv6 fails", message: v2WithV6.Build(), wantRule: "vrrp_group.v2_is_ipv4"},
		{name: "version 3 with IPv6 passes", message: v3WithV6.Build()},
		{name: "advertisement_interval 1005ms not centisecond", message: interval1005ms.Build(), wantRule: "vrrp_group.advertisement_interval_centiseconds"},
		{name: "version 3 interval 1s passes", message: v3Interval1s.Build()},
		{name: "version 3 interval 41s exceeds 40.95s", message: v3Interval41s.Build(), wantRule: "vrrp_group.v3_interval"},
		{name: "version 3 interval 40.95s passes", message: v3Interval40950ms.Build()},
		{name: "version 2 interval 1.5s not whole second", message: v2Interval1500ms.Build(), wantRule: "vrrp_group.v2_interval"},
		{name: "version 2 interval 255s passes", message: v2Interval255s.Build()},
		{name: "IPv6 virtual address in IPv4 group", message: v4GroupWithV6Virtual.Build(), wantRule: "vrrp_group.address_family"},
		{name: "IPv4 virtual address in IPv4 group passes", message: v4GroupWithV4Virtual.Build()},
	})
}
