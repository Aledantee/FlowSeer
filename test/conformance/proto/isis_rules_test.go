package conformance

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	isisv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/isis/v1"
)

func TestIsisAdjacencyStateValues(t *testing.T) {
	// IsisAdjacencyState names exactly 1 to 4 beside _UNSPECIFIED.
	values := isisv1.IsisAdjacencyState(0).Descriptor().Values()
	require.Equal(t, 5, values.Len())
	require.NotNil(t, values.ByNumber(0))
	require.NotNil(t, values.ByNumber(1))
	require.NotNil(t, values.ByNumber(2))
	require.NotNil(t, values.ByNumber(3))
	require.NotNil(t, values.ByNumber(4))
	require.Nil(t, values.ByNumber(5))
}

func validIsisInstance() *isisv1.IsisInstance_builder {
	return &isisv1.IsisInstance_builder{
		NetworkInstance:  proto.String("default"),
		ProtocolInstance: proto.String("default"),
		SystemId:         []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
	}
}

func TestIsisInstanceRules(t *testing.T) {
	withoutNetwork := validIsisInstance()
	withoutNetwork.NetworkInstance = nil

	withoutProtocol := validIsisInstance()
	withoutProtocol.ProtocolInstance = nil

	systemID5 := validIsisInstance()
	systemID5.SystemId = []byte{1, 2, 3, 4, 5}

	systemID7 := validIsisInstance()
	systemID7.SystemId = []byte{1, 2, 3, 4, 5, 6, 7}

	areaAddr0 := validIsisInstance()
	areaAddr0.AreaAddresses = [][]byte{{}}

	areaAddr21 := validIsisInstance()
	areaAddr21.AreaAddresses = [][]byte{make([]byte, 21)}

	areaAddrDup := validIsisInstance()
	areaAddrDup.AreaAddresses = [][]byte{
		{0x49, 0x00, 0x01},
		{0x49, 0x00, 0x01},
	}

	areaAddrValid := validIsisInstance()
	areaAddrValid.AreaAddresses = [][]byte{
		{0x49},
		make([]byte, 20),
	}

	runFieldCases(t, []fieldCase{
		{name: "valid instance", message: validIsisInstance().Build()},
		{name: "network_instance absent", message: withoutNetwork.Build(), wantField: "network_instance", wantText: "value is required"},
		{name: "protocol_instance absent", message: withoutProtocol.Build(), wantField: "protocol_instance", wantText: "value is required"},
		{name: "system_id 5 octets", message: systemID5.Build(), wantField: "system_id", wantText: "must be 6 bytes"},
		{name: "system_id 7 octets", message: systemID7.Build(), wantField: "system_id", wantText: "must be 6 bytes"},
		{name: "area_address 0 octets", message: areaAddr0.Build(), wantField: "area_addresses[0]", wantText: "must be at least 1 bytes"},
		{name: "area_address 21 octets", message: areaAddr21.Build(), wantField: "area_addresses[0]", wantText: "must be at most 20 bytes"},
		{name: "area_addresses duplicate", message: areaAddrDup.Build(), wantField: "area_addresses", wantText: "repeated value must contain unique items"},
		{name: "area_addresses valid bounds", message: areaAddrValid.Build()},
	})
}

func validIsisAdjacency() *isisv1.IsisAdjacency_builder {
	return &isisv1.IsisAdjacency_builder{
		InterfaceName:    proto.String("eth0"),
		ProtocolInstance: proto.String("default"),
		NeighborSystemId: []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
		Usage:            isisv1.IsisLevel_ISIS_LEVEL_LEVEL1_AND_LEVEL2.Enum(),
	}
}

func TestIsisAdjacencyRules(t *testing.T) {
	require.Nil(t, (*isisv1.IsisAdjacency)(nil).ProtoReflect().Descriptor().Fields().ByName("network_instance"),
		"IsisAdjacency must not carry a network_instance field; network instance is inherited from the interface")

	withoutIf := validIsisAdjacency()
	withoutIf.InterfaceName = nil

	withoutProtocol := validIsisAdjacency()
	withoutProtocol.ProtocolInstance = nil

	withoutNeighbor := validIsisAdjacency()
	withoutNeighbor.NeighborSystemId = nil

	neighbor5 := validIsisAdjacency()
	neighbor5.NeighborSystemId = []byte{1, 2, 3, 4, 5}

	withoutUsage := validIsisAdjacency()
	withoutUsage.Usage = nil

	usageUnspecified := validIsisAdjacency()
	usageUnspecified.Usage = isisv1.IsisLevel_ISIS_LEVEL_UNSPECIFIED.Enum()

	usageLevel1 := validIsisAdjacency()
	usageLevel1.Usage = isisv1.IsisLevel_ISIS_LEVEL_LEVEL1.Enum()

	usageLevel2 := validIsisAdjacency()
	usageLevel2.Usage = isisv1.IsisLevel_ISIS_LEVEL_LEVEL2.Enum()

	usageLevel1And2 := validIsisAdjacency()
	usageLevel1And2.Usage = isisv1.IsisLevel_ISIS_LEVEL_LEVEL1_AND_LEVEL2.Enum()

	holdTime65536 := validIsisAdjacency()
	holdTime65536.HoldTime = &durationpb.Duration{Seconds: 65536}

	priority128 := validIsisAdjacency()
	priority128.Priority = proto.Uint32(128)

	priority127 := validIsisAdjacency()
	priority127.Priority = proto.Uint32(127)

	runFieldCases(t, []fieldCase{
		{name: "valid adjacency", message: validIsisAdjacency().Build()},
		{name: "interface_name absent", message: withoutIf.Build(), wantField: "interface_name", wantText: "value is required"},
		{name: "protocol_instance absent", message: withoutProtocol.Build(), wantField: "protocol_instance", wantText: "value is required"},
		{name: "neighbor_system_id absent", message: withoutNeighbor.Build(), wantField: "neighbor_system_id", wantText: "value is required"},
		{name: "neighbor_system_id 5 octets", message: neighbor5.Build(), wantField: "neighbor_system_id", wantText: "must be 6 bytes"},
		{name: "usage absent", message: withoutUsage.Build(), wantField: "usage", wantText: "value is required"},
		{name: "usage unspecified", message: usageUnspecified.Build(), wantField: "usage", wantText: "must not be in list"},
		{name: "usage level 1", message: usageLevel1.Build()},
		{name: "usage level 2", message: usageLevel2.Build()},
		{name: "usage level 1 and 2", message: usageLevel1And2.Build()},
		{name: "hold_time 65536s", message: holdTime65536.Build(), wantField: "hold_time", wantText: "less than or equal to 65535s"},
		{name: "priority 128", message: priority128.Build(), wantField: "priority", wantText: "less than or equal to 127"},
		{name: "priority 127", message: priority127.Build()},
	})
}
