package conformance

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	packetv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/packet/v1"
	phyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/phy/v1"
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

func neighborWithTimeToLive(ttl *durationpb.Duration) *lldpv1.Neighbor {
	return lldpv1.Neighbor_builder{
		LocalInterfaceName: proto.String("GigabitEthernet1/0/1"),
		ChassisId:          chassisID(),
		PortId:             portID(),
		TimeToLive:         ttl,
	}.Build()
}

func TestLldpNeighborRules(t *testing.T) {
	tests := []validationCase{
		{
			name:      "neighbor with the default 120 second time to live",
			message:   neighborWithTimeToLive(durationpb.New(120 * time.Second)),
			wantValid: true,
		},
		{
			name:      "neighbor asking for its information to be discarded",
			message:   neighborWithTimeToLive(durationpb.New(0)),
			wantValid: true,
		},
		{
			name:      "neighbor time to live at the 16-bit maximum",
			message:   neighborWithTimeToLive(durationpb.New(65535 * time.Second)),
			wantValid: true,
		},
		{
			name:      "neighbor time to live beyond 16 bits",
			message:   neighborWithTimeToLive(durationpb.New(65536 * time.Second)),
			wantValid: false,
		},
		{
			name:      "neighbor with a negative time to live",
			message:   neighborWithTimeToLive(durationpb.New(-time.Second)),
			wantValid: false,
		},
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

// detailedIeee8023 holds the IEEE 802.3 TLVs of the Wireshark sample capture
// lldp.detailed.pcap (sha256
// 480e6123969cc1084b7d41a54ff0947e0bffe72a4ce025664d5fb872998809dc), decoded
// with tcpdump: MAC/PHY 03 6c00 0010, Power via MDI 07 01 00, maximum frame
// size 05f2. The advertised bitmap 0x6c00 reads with bit 0 as the most
// significant bit of the first octet, and the class octet 0 leaves
// power_class absent.
func detailedIeee8023() *lldpv1.Ieee8023Extension_builder {
	return &lldpv1.Ieee8023Extension_builder{
		AutoNegotiationSupported: proto.Bool(true),
		AutoNegotiationEnabled:   proto.Bool(true),
		AdvertisedLinkModes: []phyv1.MauLinkMode{
			phyv1.MauLinkMode_MAU_LINK_MODE_BASE_T_MBPS10,
			phyv1.MauLinkMode_MAU_LINK_MODE_BASE_T_MBPS10_FULL,
			phyv1.MauLinkMode_MAU_LINK_MODE_BASE_TX_MBPS100,
			phyv1.MauLinkMode_MAU_LINK_MODE_BASE_TX_MBPS100_FULL,
		},
		OperationalMauType: phyv1.MauType_builder{Iana: proto.Uint32(16)}.Build(),
		PowerViaMdi: lldpv1.PowerViaMdi_builder{
			PortClass:          phyv1.PoeRole_POE_ROLE_PSE.Enum(),
			Supported:          proto.Bool(true),
			Enabled:            proto.Bool(true),
			PairControlCapable: proto.Bool(false),
			Pairs:              lldpv1.PowerPairs_POWER_PAIRS_SIGNAL.Enum(),
		}.Build(),
		MaxFrameSizeBytes: proto.Uint64(1522),
	}
}

func neighborWithIeee8023(ext *lldpv1.Ieee8023Extension) *lldpv1.Neighbor {
	return lldpv1.Neighbor_builder{
		LocalInterfaceName: proto.String("GigabitEthernet1/0/1"),
		ChassisId:          chassisID(),
		PortId:             portID(),
		Ieee8023:           ext,
	}.Build()
}

// TestLldpIeee8023ExtensionRules holds a neighbor's IEEE 802.3 TLVs, as a
// published capture carries them, to the MIB's ranges.
func TestLldpIeee8023ExtensionRules(t *testing.T) {
	tooLargeFrame := detailedIeee8023()
	tooLargeFrame.MaxFrameSizeBytes = proto.Uint64(65536)
	classFive := detailedIeee8023()
	classFive.PowerViaMdi = lldpv1.PowerViaMdi_builder{PowerClass: proto.Uint32(5)}.Build()
	classFour := detailedIeee8023()
	classFour.PowerViaMdi = lldpv1.PowerViaMdi_builder{PowerClass: proto.Uint32(4)}.Build()
	repeatedMode := detailedIeee8023()
	repeatedMode.AdvertisedLinkModes = []phyv1.MauLinkMode{
		phyv1.MauLinkMode_MAU_LINK_MODE_BASE_T_MBPS10,
		phyv1.MauLinkMode_MAU_LINK_MODE_BASE_T_MBPS10,
	}

	runFieldCases(t, []fieldCase{
		{name: "lldp.detailed.pcap IEEE 802.3 TLVs", message: neighborWithIeee8023(detailedIeee8023().Build())},
		{name: "power class 4", message: neighborWithIeee8023(classFour.Build())},
		{name: "maximum frame size beyond 16 bits", message: neighborWithIeee8023(tooLargeFrame.Build()), wantField: "ieee8023.max_frame_size_bytes", wantText: "less than or equal to 65535"},
		{name: "power class 5", message: neighborWithIeee8023(classFive.Build()), wantField: "ieee8023.power_via_mdi.power_class", wantText: "less than or equal to 4"},
		{name: "repeated link mode", message: neighborWithIeee8023(repeatedMode.Build()), wantField: "ieee8023.advertised_link_modes", wantText: "unique"},
	})
}

// civicLocation is the location data of the Wireshark sample capture
// lldpmed_civicloc.pcap after the format octet 02: 41 octets of civic
// address LCI, country "US", state "CA", city "Roseville".
var civicLocation = []byte{
	0x28, 0x02, 0x55, 0x53, 0x01, 0x02, 0x43, 0x41, 0x03, 0x09, 0x52, 0x6f,
	0x73, 0x65, 0x76, 0x69, 0x6c, 0x6c, 0x65, 0x06, 0x09, 0x46, 0x6f, 0x6f,
	0x74, 0x68, 0x69, 0x6c, 0x6c, 0x73, 0x13, 0x04, 0x38, 0x30, 0x30, 0x30,
	0x1a, 0x03, 0x52, 0x33, 0x4c,
}

func voicePolicy(vlanID uint32) *lldpv1.MedNetworkPolicy {
	return lldpv1.MedNetworkPolicy_builder{
		ApplicationType: lldpv1.MedApplicationType_MED_APPLICATION_TYPE_VOICE.Enum(),
		Tagged:          proto.Bool(true),
		Unknown:         proto.Bool(false),
		VlanId:          proto.Uint32(vlanID),
		Priority:        proto.Uint32(6),
		Dscp:            packetv1.IpDscp_IP_DSCP_EF.Enum(),
	}.Build()
}

// civicLocMed holds the LLDP-MED TLVs of the Wireshark sample capture
// lldpmed_civicloc.pcap (sha256
// d24a236b6e4ff04d29fb90f8b1448f5bff0007c6d3eafd3958c71a77c2d9869e), decoded
// with tcpdump: capabilities 000f 04, network policy 01 40 65 ae, location
// format 02, extended power 03 00 41. The power octet 0x03 reads as a PSE
// with low priority when the type is masked from bits 7-6, as Wireshark's
// dissector does; 0x41 is 65 tenths of a watt.
func civicLocMed() *lldpv1.MedExtension_builder {
	return &lldpv1.MedExtension_builder{
		CapabilitiesSupported: []lldpv1.MedCapability{
			lldpv1.MedCapability_MED_CAPABILITY_CAPABILITIES,
			lldpv1.MedCapability_MED_CAPABILITY_NETWORK_POLICY,
			lldpv1.MedCapability_MED_CAPABILITY_LOCATION,
			lldpv1.MedCapability_MED_CAPABILITY_EXTENDED_PSE,
		},
		DeviceClass:     lldpv1.MedDeviceClass_MED_DEVICE_CLASS_NETWORK_CONNECTIVITY.Enum(),
		NetworkPolicies: []*lldpv1.MedNetworkPolicy{voicePolicy(50)},
		Locations: []*lldpv1.MedLocation{lldpv1.MedLocation_builder{
			Format: lldpv1.MedLocationFormat_MED_LOCATION_FORMAT_CIVIC_ADDRESS.Enum(),
			Info:   civicLocation,
		}.Build()},
		Power: lldpv1.MedPower_builder{
			Role:           phyv1.PoeRole_POE_ROLE_PSE.Enum(),
			Priority:       phyv1.PoePriority_POE_PRIORITY_LOW.Enum(),
			PowerNanowatts: proto.Uint64(6_500_000_000),
		}.Build(),
	}
}

func neighborWithMed(ext *lldpv1.MedExtension) *lldpv1.Neighbor {
	return lldpv1.Neighbor_builder{
		LocalInterfaceName: proto.String("1"),
		ChassisId:          chassisID(),
		PortId:             portID(),
		Med:                ext,
	}.Build()
}

// TestLldpMedExtensionRules holds a neighbor's LLDP-MED TLVs, as a published
// capture carries them, to the MIB's ranges and to one policy per
// application type.
func TestLldpMedExtensionRules(t *testing.T) {
	twoVoicePolicies := civicLocMed()
	twoVoicePolicies.NetworkPolicies = []*lldpv1.MedNetworkPolicy{voicePolicy(50), voicePolicy(60)}
	reservedVlan := civicLocMed()
	reservedVlan.NetworkPolicies = []*lldpv1.MedNetworkPolicy{voicePolicy(4095)}
	outOfRangeVlan := civicLocMed()
	outOfRangeVlan.NetworkPolicies = []*lldpv1.MedNetworkPolicy{voicePolicy(4096)}
	tooMuchPower := civicLocMed()
	tooMuchPower.Power = lldpv1.MedPower_builder{PowerNanowatts: proto.Uint64(102_300_000_001)}.Build()
	mostPower := civicLocMed()
	mostPower.Power = lldpv1.MedPower_builder{PowerNanowatts: proto.Uint64(102_300_000_000)}.Build()
	longLocation := civicLocMed()
	longLocation.Locations = []*lldpv1.MedLocation{lldpv1.MedLocation_builder{
		Format: lldpv1.MedLocationFormat_MED_LOCATION_FORMAT_ELIN.Enum(),
		Info:   make([]byte, 257),
	}.Build()}
	twoCivicLocations := civicLocMed()
	twoCivicLocations.Locations = append(twoCivicLocations.Locations, lldpv1.MedLocation_builder{
		Format: lldpv1.MedLocationFormat_MED_LOCATION_FORMAT_CIVIC_ADDRESS.Enum(),
		Info:   civicLocation[:4],
	}.Build())
	longSerial := civicLocMed()
	longSerial.Inventory = lldpv1.MedInventory_builder{SerialNumber: proto.String("ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456")}.Build()
	fullSerial := civicLocMed()
	fullSerial.Inventory = lldpv1.MedInventory_builder{SerialNumber: proto.String("ABCDEFGHIJKLMNOPQRSTUVWXYZ012345")}.Build()

	runFieldCases(t, []fieldCase{
		{name: "lldpmed_civicloc.pcap LLDP-MED TLVs", message: neighborWithMed(civicLocMed().Build())},
		{name: "policy VLAN reserved for implementation use", message: neighborWithMed(reservedVlan.Build())},
		{name: "power at the 102.3 W maximum", message: neighborWithMed(mostPower.Build())},
		{name: "serial number of 32 octets", message: neighborWithMed(fullSerial.Build())},
		{name: "two policies for one application", message: neighborWithMed(twoVoicePolicies.Build()), wantField: "med", wantText: "application_type"},
		{name: "policy VLAN beyond 12 bits", message: neighborWithMed(outOfRangeVlan.Build()), wantField: "med.network_policies[0].vlan_id", wantText: "less than or equal to 4095"},
		{name: "power beyond 102.3 W", message: neighborWithMed(tooMuchPower.Build()), wantField: "med.power.power_nanowatts", wantText: "less than or equal to 102300000000"},
		{name: "location of 257 octets", message: neighborWithMed(longLocation.Build()), wantField: "med.locations[0].info", wantText: "256 bytes"},
		{name: "two locations in one format", message: neighborWithMed(twoCivicLocations.Build()), wantField: "med", wantText: "format"},
		{name: "serial number of 33 octets", message: neighborWithMed(longSerial.Build()), wantField: "med.inventory.serial_number", wantText: "32 bytes"},
	})
}
