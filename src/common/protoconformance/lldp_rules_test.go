package protoconformance

import (
	"testing"

	"google.golang.org/protobuf/proto"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	lldpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/lldp/v1"
)

func chassisID() *lldpv1.ChassisId {
	return lldpv1.ChassisId_builder{
		Subtype: lldpv1.ChassisIdSubtype_CHASSIS_ID_SUBTYPE_MAC_ADDRESS.Enum(),
		Value:   []byte{0x00, 0x1b, 0x21, 0x3c, 0x4d, 0x5e},
	}.Build()
}

func portID() *lldpv1.PortId {
	return lldpv1.PortId_builder{
		Subtype: lldpv1.PortIdSubtype_PORT_ID_SUBTYPE_INTERFACE_NAME.Enum(),
		Value:   []byte("GigabitEthernet1/0/24"),
	}.Build()
}

func TestLldpNeighborRules(t *testing.T) {
	tests := []validationCase{
		{
			name: "complete neighbor row",
			message: lldpv1.Neighbor_builder{
				LocalInterfaceName: proto.String("GigabitEthernet1/0/1"),
				ChassisId:          chassisID(),
				PortId:             portID(),
				SystemName:         proto.String("access-sw-02"),
				SystemDescription:  proto.String("24-port switch"),
				CapabilitiesEnabled: []lldpv1.SystemCapability{
					lldpv1.SystemCapability_SYSTEM_CAPABILITY_BRIDGE,
				},
				ManagementAddresses: []*lldpv1.ManagementAddress{
					lldpv1.ManagementAddress_builder{
						Ip: addrv1.IpAddress_builder{
							V4: addrv1.Ipv4Address_builder{Octets: []byte{10, 0, 0, 2}}.Build(),
						}.Build(),
					}.Build(),
				},
			}.Build(),
			wantValid: true,
		},
		{
			name: "neighbor without a chassis identifier",
			message: lldpv1.Neighbor_builder{
				LocalInterfaceName: proto.String("GigabitEthernet1/0/1"),
				PortId:             portID(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "neighbor without a port identifier",
			message: lldpv1.Neighbor_builder{
				LocalInterfaceName: proto.String("GigabitEthernet1/0/1"),
				ChassisId:          chassisID(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "neighbor without a local interface name",
			message: lldpv1.Neighbor_builder{
				ChassisId: chassisID(),
				PortId:    portID(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "neighbor local interface name empty",
			message: lldpv1.Neighbor_builder{
				LocalInterfaceName: proto.String(""),
				ChassisId:          chassisID(),
				PortId:             portID(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "chassis identifier without octets",
			message: lldpv1.ChassisId_builder{
				Subtype: lldpv1.ChassisIdSubtype_CHASSIS_ID_SUBTYPE_LOCAL.Enum(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "chassis identifier with empty octets",
			message: lldpv1.ChassisId_builder{
				Subtype: lldpv1.ChassisIdSubtype_CHASSIS_ID_SUBTYPE_LOCAL.Enum(),
				Value:   []byte{},
			}.Build(),
			wantValid: false,
		},
		{
			name:      "chassis identifier without a subtype",
			message:   lldpv1.ChassisId_builder{Value: []byte("core-1")}.Build(),
			wantValid: false,
		},
		{
			name: "chassis identifier with the reserved subtype",
			message: lldpv1.ChassisId_builder{
				Subtype: lldpv1.ChassisIdSubtype_CHASSIS_ID_SUBTYPE_RESERVED.Enum(),
				Value:   []byte("core-1"),
			}.Build(),
			wantValid: false,
		},
		{
			name: "unknown nonzero chassis id subtype remains valid",
			message: lldpv1.ChassisId_builder{
				Subtype: lldpv1.ChassisIdSubtype(99).Enum(),
				Value:   []byte("core-1"),
			}.Build(),
			wantValid: true,
		},
		{
			name: "unknown nonzero port id subtype remains valid",
			message: lldpv1.PortId_builder{
				Subtype: lldpv1.PortIdSubtype(99).Enum(),
				Value:   []byte("port-1"),
			}.Build(),
			wantValid: true,
		},
		{
			name: "port identifier with the reserved subtype",
			message: lldpv1.PortId_builder{
				Subtype: lldpv1.PortIdSubtype_PORT_ID_SUBTYPE_RESERVED.Enum(),
				Value:   []byte("port-1"),
			}.Build(),
			wantValid: false,
		},
	}

	runValidationCases(t, tests)
}

func TestLldpManagementAddressRules(t *testing.T) {
	tests := []validationCase{
		{
			name: "IPv6 management address",
			message: lldpv1.ManagementAddress_builder{
				Ip: addrv1.IpAddress_builder{
					V6: addrv1.Ipv6Address_builder{
						Octets: []byte{
							0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0,
							0, 0, 0, 0, 0, 0, 0, 0x01,
						},
					}.Build(),
				}.Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name:      "management address with no arm set",
			message:   lldpv1.ManagementAddress_builder{}.Build(),
			wantValid: false,
		},
		{
			name: "management address whose IP payload is empty",
			message: lldpv1.ManagementAddress_builder{
				Ip: addrv1.IpAddress_builder{}.Build(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "non-IP management address carries its address family",
			message: lldpv1.ManagementAddress_builder{
				Other: lldpv1.OtherManagementAddress_builder{
					AddressFamily: proto.Uint32(6),
					Value:         []byte{0x49, 0x00, 0x01},
				}.Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "non-IP management address without octets",
			message: lldpv1.ManagementAddress_builder{
				Other: lldpv1.OtherManagementAddress_builder{
					AddressFamily: proto.Uint32(6),
				}.Build(),
			}.Build(),
			wantValid: false,
		},
	}

	runValidationCases(t, tests)
}

func TestLldpLocalSystemRules(t *testing.T) {
	tests := []validationCase{
		{
			name: "local system block",
			message: lldpv1.LocalSystem_builder{
				ChassisId:         chassisID(),
				SystemName:        proto.String("core-1"),
				SystemDescription: proto.String("core switch"),
				CapabilitiesSupported: []lldpv1.SystemCapability{
					lldpv1.SystemCapability_SYSTEM_CAPABILITY_BRIDGE,
					lldpv1.SystemCapability_SYSTEM_CAPABILITY_ROUTER,
				},
				CapabilitiesEnabled: []lldpv1.SystemCapability{
					lldpv1.SystemCapability_SYSTEM_CAPABILITY_BRIDGE,
				},
			}.Build(),
			wantValid: true,
		},
		{
			name:      "empty local system block",
			message:   lldpv1.LocalSystem_builder{}.Build(),
			wantValid: true,
		},
		{
			name: "unknown capabilities remain valid",
			message: lldpv1.LocalSystem_builder{
				CapabilitiesSupported: []lldpv1.SystemCapability{
					lldpv1.SystemCapability(42),
				},
			}.Build(),
			wantValid: true,
		},
		{
			name: "the other capability remains valid at zero",
			message: lldpv1.LocalSystem_builder{
				CapabilitiesSupported: []lldpv1.SystemCapability{
					lldpv1.SystemCapability_SYSTEM_CAPABILITY_OTHER,
				},
			}.Build(),
			wantValid: true,
		},
		{
			name: "repeated capability",
			message: lldpv1.LocalSystem_builder{
				CapabilitiesSupported: []lldpv1.SystemCapability{
					lldpv1.SystemCapability_SYSTEM_CAPABILITY_ROUTER,
					lldpv1.SystemCapability_SYSTEM_CAPABILITY_ROUTER,
				},
			}.Build(),
			wantValid: false,
		},
	}

	runValidationCases(t, tests)
}

func TestLldpPortSettingsRules(t *testing.T) {
	tests := []validationCase{
		{
			name: "port settings with transmitted TLVs",
			message: lldpv1.PortSettings_builder{
				InterfaceName:   proto.String("GigabitEthernet1/0/1"),
				AdminStatus:     lldpv1.PortAdminStatus_PORT_ADMIN_STATUS_TX_AND_RX.Enum(),
				PortId:          portID(),
				PortDescription: proto.String("uplink"),
				TransmittedTlvs: []lldpv1.TlvType{
					lldpv1.TlvType_TLV_TYPE_SYSTEM_NAME,
					lldpv1.TlvType_TLV_TYPE_MANAGEMENT_ADDRESS,
				},
			}.Build(),
			wantValid: true,
		},
		{
			name:      "port settings without an interface name",
			message:   lldpv1.PortSettings_builder{}.Build(),
			wantValid: false,
		},
		{
			name: "unknown nonzero admin status remains valid",
			message: lldpv1.PortSettings_builder{
				InterfaceName: proto.String("GigabitEthernet1/0/1"),
				AdminStatus:   lldpv1.PortAdminStatus(99).Enum(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "the end-of-LLDPDU marker is not a transmittable TLV",
			message: lldpv1.PortSettings_builder{
				InterfaceName:   proto.String("GigabitEthernet1/0/1"),
				TransmittedTlvs: []lldpv1.TlvType{lldpv1.TlvType_TLV_TYPE_END_OF_LLDPDU},
			}.Build(),
			wantValid: false,
		},
	}

	runValidationCases(t, tests)
}

func TestLldpNeighborKeepsUnnamedCapability(t *testing.T) {
	neighbor := lldpv1.Neighbor_builder{
		LocalInterfaceName: proto.String("GigabitEthernet1/0/1"),
		ChassisId:          chassisID(),
		PortId:             portID(),
		CapabilitiesSupported: []lldpv1.SystemCapability{
			lldpv1.SystemCapability_SYSTEM_CAPABILITY_BRIDGE,
			lldpv1.SystemCapability(42),
		},
	}.Build()

	runValidationCases(t, []validationCase{
		{name: "neighbor reporting an unnamed capability", message: neighbor, wantValid: true},
	})

	wire, err := proto.Marshal(neighbor)
	if err != nil {
		t.Fatalf("marshaling neighbor: %v", err)
	}

	decoded := &lldpv1.Neighbor{}
	if err := proto.Unmarshal(wire, decoded); err != nil {
		t.Fatalf("unmarshaling neighbor: %v", err)
	}

	caps := decoded.GetCapabilitiesSupported()
	if len(caps) != 2 {
		t.Fatalf("got %d capabilities, want 2", len(caps))
	}
	if caps[1] != lldpv1.SystemCapability(42) {
		t.Errorf("got capability %v, want the unnamed value 42", caps[1])
	}
}
