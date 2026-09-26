package conformance

import (
	"testing"

	"google.golang.org/protobuf/proto"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	stpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/stp/v1"
)

// fieldCase is a validationCase that also names where an invalid message
// fails, so a message rejected by some other rule does not pass for the one
// under test: wantField is the violated field path and wantText a fragment of
// its message, or, for a message-level rule, which has no field path,
// wantRule is the rule id. With neither set the message must be valid.
type fieldCase struct {
	name      string
	message   proto.Message
	wantField string
	wantText  string
	wantRule  string
}

func runFieldCases(t *testing.T, cases []fieldCase) {
	t.Helper()

	var valid []validationCase
	for _, tt := range cases {
		switch {
		case tt.wantRule != "":
			t.Run(tt.name, func(t *testing.T) {
				errsOn(t, tt.message, tt.wantRule)
			})
		case tt.wantField != "":
			t.Run(tt.name, func(t *testing.T) {
				errsAtField(t, tt.message, tt.wantField, tt.wantText)
			})
		default:
			valid = append(valid, validationCase{name: tt.name, message: tt.message, wantValid: true})
		}
	}
	runValidationCases(t, valid)
}

func stpBridgeID(priority uint32) *stpv1.BridgeId {
	return stpv1.BridgeId_builder{
		Priority: proto.Uint32(priority),
		Address:  addrv1.Eui48Address_builder{Octets: []byte{0x00, 0x1b, 0x21, 0x0a, 0x0b, 0x0c}}.Build(),
	}.Build()
}

func validMstInstance(mstID uint32) *stpv1.MstInstance_builder {
	return &stpv1.MstInstance_builder{
		NetworkInstance: proto.String("default"),
		MstId:           proto.Uint32(mstID),
		BridgeId:        stpBridgeID(32768),
	}
}

// TestMstInstanceRules holds an MSTI row to MSTIDs 1 to 4094, with the CIST
// (0) and the SPVID marker (4095) outside it, and to a bridge identifier
// whose priority is masked to its settable four bits.
func TestMstInstanceRules(t *testing.T) {
	withPriority := func(priority uint32) *stpv1.MstInstance {
		b := validMstInstance(1)
		b.BridgeId = stpBridgeID(priority)
		return b.Build()
	}
	withoutInstance := validMstInstance(1)
	withoutInstance.NetworkInstance = nil
	withoutBridge := validMstInstance(1)
	withoutBridge.BridgeId = nil

	runFieldCases(t, []fieldCase{
		{name: "lowest MSTID", message: validMstInstance(1).Build()},
		{name: "highest MSTID", message: validMstInstance(4094).Build()},
		{name: "CIST is not an MSTI row", message: validMstInstance(0).Build(), wantField: "mst_id", wantText: "greater than or equal to 1"},
		{name: "SPVID marker", message: validMstInstance(4095).Build(), wantField: "mst_id", wantText: "less than or equal to 4094"},
		{name: "masked priority", message: withPriority(32768)},
		{name: "system ID extension left in the priority", message: withPriority(32769), wantField: "bridge_id.priority", wantText: "multiple of 4096"},
		{name: "network instance absent", message: withoutInstance.Build(), wantField: "network_instance", wantText: "value is required"},
		{name: "bridge identifier absent", message: withoutBridge.Build(), wantField: "bridge_id", wantText: "value is required"},
	})
}

// TestMstPortRules holds an MSTI port row to the same MSTID range and to an
// interface name.
func TestMstPortRules(t *testing.T) {
	port := func(mstID uint32) *stpv1.MstPort_builder {
		return &stpv1.MstPort_builder{
			MstId:         proto.Uint32(mstID),
			InterfaceName: proto.String("ethernet1/1/5"),
			Role:          stpv1.PortRole_PORT_ROLE_MASTER.Enum(),
			State:         stpv1.ForwardingState_FORWARDING_STATE_FORWARDING.Enum(),
			Priority:      proto.Uint32(128),
			PathCost:      proto.Uint32(20000),
		}
	}
	withoutName := port(1)
	withoutName.InterfaceName = nil
	withPriority := port(1)
	withPriority.Priority = proto.Uint32(241)

	runFieldCases(t, []fieldCase{
		{name: "lowest MSTID", message: port(1).Build()},
		{name: "highest MSTID", message: port(4094).Build()},
		{name: "CIST is not an MSTI row", message: port(0).Build(), wantField: "mst_id", wantText: "greater than or equal to 1"},
		{name: "SPVID marker", message: port(4095).Build(), wantField: "mst_id", wantText: "less than or equal to 4094"},
		{name: "interface name absent", message: withoutName.Build(), wantField: "interface_name", wantText: "value is required"},
		{name: "priority above 240", message: withPriority.Build(), wantField: "priority", wantText: "less than or equal to 240"},
	})
}

// TestMstVlanMapRules holds the VLAN-to-MSTI map to MSTIDs 0 to 4094, where 0
// is the CIST, and to a set of usable VLAN identifiers.
func TestMstVlanMapRules(t *testing.T) {
	vlanMap := func(mstID uint32, vlanIDs ...uint32) *stpv1.MstVlanMap_builder {
		return &stpv1.MstVlanMap_builder{
			NetworkInstance: proto.String("default"),
			MstId:           proto.Uint32(mstID),
			VlanIds:         vlanIDs,
		}
	}
	withoutInstance := vlanMap(1, 10)
	withoutInstance.NetworkInstance = nil
	withoutMstID := vlanMap(1, 10)
	withoutMstID.MstId = nil

	runFieldCases(t, []fieldCase{
		{name: "CIST keeps VLAN 1", message: vlanMap(0, 1).Build()},
		{name: "MSTI with VLANs", message: vlanMap(1, 10, 20, 4094).Build()},
		{name: "MSTI with no VLANs allocated", message: vlanMap(4094).Build()},
		{name: "repeated VLAN", message: vlanMap(1, 10, 10).Build(), wantField: "vlan_ids", wantText: "unique"},
		{name: "reserved VLAN", message: vlanMap(1, 4095).Build(), wantField: "vlan_ids[0]", wantText: "VLAN"},
		{name: "SPVID marker", message: vlanMap(4095, 10).Build(), wantField: "mst_id", wantText: "less than or equal to 4094"},
		{name: "network instance absent", message: withoutInstance.Build(), wantField: "network_instance", wantText: "value is required"},
		{name: "MSTID absent", message: withoutMstID.Build(), wantField: "mst_id", wantText: "value is required"},
	})
}

// TestBridgeStateRequiresNetworkInstance holds the CIST row to the key the
// MSTI rows beside it carry, and the MSTP-only values to their MIB ranges.
func TestBridgeStateRequiresNetworkInstance(t *testing.T) {
	bridge := func() *stpv1.BridgeState_builder {
		return &stpv1.BridgeState_builder{
			NetworkInstance: proto.String("default"),
			ProtocolVersion: stpv1.ProtocolVersion_PROTOCOL_VERSION_MSTP.Enum(),
			BridgeId:        stpBridgeID(32768),
			MstConfigId: stpv1.MstConfigId_builder{
				Name:          proto.String("region1"),
				RevisionLevel: proto.Uint32(1),
				Digest:        make([]byte, 16),
			}.Build(),
			CistRegionalRoot:         stpBridgeID(4096),
			CistInternalRootPathCost: proto.Uint32(20000),
			MaxHops:                  proto.Uint32(20),
		}
	}
	withoutInstance := bridge()
	withoutInstance.NetworkInstance = nil
	withMaxHops := bridge()
	withMaxHops.MaxHops = proto.Uint32(5)
	withDigest := bridge()
	withDigest.MstConfigId = stpv1.MstConfigId_builder{Digest: make([]byte, 15)}.Build()
	withName := bridge()
	withName.MstConfigId = stpv1.MstConfigId_builder{Name: proto.String("abcdefghijklmnopqrstuvwxyz0123456")}.Build()

	runFieldCases(t, []fieldCase{
		{name: "MSTP bridge", message: bridge().Build()},
		{name: "network instance absent", message: withoutInstance.Build(), wantField: "network_instance", wantText: "value is required"},
		{name: "max hops below 6", message: withMaxHops.Build(), wantField: "max_hops", wantText: "greater than or equal to 6"},
		{name: "digest of 15 octets", message: withDigest.Build(), wantField: "mst_config_id.digest", wantText: "16 bytes"},
		{name: "configuration name of 33 octets", message: withName.Build(), wantField: "mst_config_id.name", wantText: "32 bytes"},
	})
}
