package conformance

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	ospfv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/ospf/v1"
)

func TestOspfInterfaceTypeKeepsTheGap(t *testing.T) {
	// Value 4 is unassigned in OSPF-MIB and OSPFV3-MIB and must stay unassigned.
	require.Nil(t, ospfv1.OspfInterfaceType(0).Descriptor().Values().ByNumber(4))
	require.NotNil(t, ospfv1.OspfInterfaceType(0).Descriptor().Values().ByNumber(3))
	require.NotNil(t, ospfv1.OspfInterfaceType(0).Descriptor().Values().ByNumber(5))
}

func validOspfArea() *ospfv1.OspfArea_builder {
	return &ospfv1.OspfArea_builder{
		NetworkInstance:  proto.String("default"),
		Version:          proto.Uint32(2),
		ProtocolInstance: proto.String("default"),
		AreaId:           proto.Uint32(0),
	}
}

func TestOspfAreaRules(t *testing.T) {
	withoutNetwork := validOspfArea()
	withoutNetwork.NetworkInstance = nil

	withoutVersion := validOspfArea()
	withoutVersion.Version = nil

	version1 := validOspfArea()
	version1.Version = proto.Uint32(1)

	version4 := validOspfArea()
	version4.Version = proto.Uint32(4)

	withoutProtocol := validOspfArea()
	withoutProtocol.ProtocolInstance = nil

	withoutArea := validOspfArea()
	withoutArea.AreaId = nil

	runFieldCases(t, []fieldCase{
		{name: "valid area (backbone)", message: validOspfArea().Build()},
		{name: "network_instance absent", message: withoutNetwork.Build(), wantField: "network_instance", wantText: "value is required"},
		{name: "version absent", message: withoutVersion.Build(), wantField: "version", wantText: "value is required"},
		{name: "version 1", message: version1.Build(), wantField: "version", wantText: "greater than or equal to 2"},
		{name: "version 4", message: version4.Build(), wantField: "version", wantText: "less than or equal to 3"},
		{name: "protocol_instance absent", message: withoutProtocol.Build(), wantField: "protocol_instance", wantText: "value is required"},
		{name: "area_id absent", message: withoutArea.Build(), wantField: "area_id", wantText: "value is required"},
	})
}

func validOspfInterface() *ospfv1.OspfInterface_builder {
	return &ospfv1.OspfInterface_builder{
		InterfaceName:    proto.String("eth0"),
		Version:          proto.Uint32(2),
		ProtocolInstance: proto.String("default"),
		AreaId:           proto.Uint32(0),
	}
}

func TestOspfInterfaceRules(t *testing.T) {
	require.Nil(t, (*ospfv1.OspfInterface)(nil).ProtoReflect().Descriptor().Fields().ByName("network_instance"),
		"OspfInterface must not carry a network_instance field; network instance is inherited from the interface")

	withoutIf := validOspfInterface()
	withoutIf.InterfaceName = nil

	withoutVersion := validOspfInterface()
	withoutVersion.Version = nil

	version4 := validOspfInterface()
	version4.Version = proto.Uint32(4)

	withoutProtocol := validOspfInterface()
	withoutProtocol.ProtocolInstance = nil

	withoutArea := validOspfInterface()
	withoutArea.AreaId = nil

	v2WithInstanceID := validOspfInterface()
	v2WithInstanceID.InstanceId = proto.Uint32(1)

	v3WithInstanceID := validOspfInterface()
	v3WithInstanceID.Version = proto.Uint32(3)
	v3WithInstanceID.InstanceId = proto.Uint32(1)

	instanceID256 := validOspfInterface()
	instanceID256.Version = proto.Uint32(3)
	instanceID256.InstanceId = proto.Uint32(256)

	priority256 := validOspfInterface()
	priority256.Priority = proto.Uint32(256)

	cost65536 := validOspfInterface()
	cost65536.Cost = proto.Uint32(65536)

	helloNeg := validOspfInterface()
	helloNeg.HelloInterval = &durationpb.Duration{Seconds: -1}

	runFieldCases(t, []fieldCase{
		{name: "valid interface", message: validOspfInterface().Build()},
		{name: "interface_name absent", message: withoutIf.Build(), wantField: "interface_name", wantText: "value is required"},
		{name: "version absent", message: withoutVersion.Build(), wantField: "version", wantText: "value is required"},
		{name: "version 4", message: version4.Build(), wantField: "version", wantText: "less than or equal to 3"},
		{name: "protocol_instance absent", message: withoutProtocol.Build(), wantField: "protocol_instance", wantText: "value is required"},
		{name: "area_id absent", message: withoutArea.Build(), wantField: "area_id", wantText: "value is required"},
		{name: "instance_id on version 2", message: v2WithInstanceID.Build(), wantRule: "ospf_interface.instance_id_is_v3"},
		{name: "instance_id on version 3", message: v3WithInstanceID.Build()},
		{name: "instance_id 256", message: instanceID256.Build(), wantField: "instance_id", wantText: "less than or equal to 255"},
		{name: "priority 256", message: priority256.Build(), wantField: "priority", wantText: "less than or equal to 255"},
		{name: "cost 65536", message: cost65536.Build(), wantField: "cost", wantText: "less than or equal to 65535"},
		{name: "hello_interval negative", message: helloNeg.Build(), wantField: "hello_interval", wantText: "greater than or equal to 0s"},
	})
}

func validOspfNeighbor(t *testing.T) *ospfv1.OspfNeighbor_builder {
	return &ospfv1.OspfNeighbor_builder{
		InterfaceName:    proto.String("eth0"),
		Version:          proto.Uint32(2),
		ProtocolInstance: proto.String("default"),
		NeighborRouterId: proto.Uint32(1),
		NeighborAddress:  ipAddress(t, "192.0.2.1"),
	}
}

func TestOspfNeighborRules(t *testing.T) {
	require.Nil(t, (*ospfv1.OspfNeighbor)(nil).ProtoReflect().Descriptor().Fields().ByName("network_instance"),
		"OspfNeighbor must not carry a network_instance field; network instance is inherited from the interface")

	withoutIf := validOspfNeighbor(t)
	withoutIf.InterfaceName = nil

	withoutVersion := validOspfNeighbor(t)
	withoutVersion.Version = nil

	withoutProtocol := validOspfNeighbor(t)
	withoutProtocol.ProtocolInstance = nil

	withoutRouterID := validOspfNeighbor(t)
	withoutRouterID.NeighborRouterId = nil

	v2WithV6Addr := validOspfNeighbor(t)
	v2WithV6Addr.NeighborAddress = ipAddress(t, "2001:db8::1")

	v3WithV4Addr := validOspfNeighbor(t)
	v3WithV4Addr.Version = proto.Uint32(3)
	v3WithV4Addr.NeighborAddress = ipAddress(t, "192.0.2.1")

	v3WithV6Addr := validOspfNeighbor(t)
	v3WithV6Addr.Version = proto.Uint32(3)
	v3WithV6Addr.NeighborAddress = ipAddress(t, "2001:db8::1")

	v2WithInstanceID := validOspfNeighbor(t)
	v2WithInstanceID.InstanceId = proto.Uint32(1)

	v3WithInstanceID := validOspfNeighbor(t)
	v3WithInstanceID.Version = proto.Uint32(3)
	v3WithInstanceID.NeighborAddress = ipAddress(t, "2001:db8::1")
	v3WithInstanceID.InstanceId = proto.Uint32(1)

	priority256 := validOspfNeighbor(t)
	priority256.Priority = proto.Uint32(256)

	runFieldCases(t, []fieldCase{
		{name: "valid neighbor", message: validOspfNeighbor(t).Build()},
		{name: "interface_name absent", message: withoutIf.Build(), wantField: "interface_name", wantText: "value is required"},
		{name: "version absent", message: withoutVersion.Build(), wantField: "version", wantText: "value is required"},
		{name: "protocol_instance absent", message: withoutProtocol.Build(), wantField: "protocol_instance", wantText: "value is required"},
		{name: "neighbor_router_id absent", message: withoutRouterID.Build(), wantField: "neighbor_router_id", wantText: "value is required"},
		{name: "version 2 with IPv6 address", message: v2WithV6Addr.Build(), wantRule: "ospf_neighbor.address_family"},
		{name: "version 3 with IPv4 address", message: v3WithV4Addr.Build(), wantRule: "ospf_neighbor.address_family"},
		{name: "version 3 with IPv6 address", message: v3WithV6Addr.Build()},
		{name: "instance_id on version 2", message: v2WithInstanceID.Build(), wantRule: "ospf_neighbor.instance_id_is_v3"},
		{name: "instance_id on version 3", message: v3WithInstanceID.Build()},
		{name: "priority 256", message: priority256.Build(), wantField: "priority", wantText: "less than or equal to 255"},
	})
}
