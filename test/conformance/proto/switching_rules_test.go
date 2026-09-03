package conformance

import (
	"testing"

	"google.golang.org/protobuf/proto"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	packetv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/packet/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
)

func TestSwitchingPrimitiveRules(t *testing.T) {
	validTag := func(vlanID uint32) *switchingv1.VlanTag {
		return switchingv1.VlanTag_builder{
			Tpid:   packetv1.EtherType_ETHER_TYPE_DOT1Q.Enum(),
			VlanId: proto.Uint32(vlanID),
			Pcp:    proto.Uint32(0),
			Dei:    proto.Bool(false),
		}.Build()
	}

	tests := []validationCase{
		{
			name:      "VLAN identifier absent",
			message:   switchingv1.Vlan_builder{}.Build(),
			wantValid: false,
		},
		{
			name:      "VLAN identifier zero",
			message:   switchingv1.Vlan_builder{Id: proto.Uint32(0)}.Build(),
			wantValid: false,
		},
		{
			name:      "lowest usable VLAN identifier",
			message:   switchingv1.Vlan_builder{Id: proto.Uint32(1)}.Build(),
			wantValid: true,
		},
		{
			name:      "highest usable VLAN identifier",
			message:   switchingv1.Vlan_builder{Id: proto.Uint32(4094)}.Build(),
			wantValid: true,
		},
		{
			name:      "reserved VLAN identifier",
			message:   switchingv1.Vlan_builder{Id: proto.Uint32(4095)}.Build(),
			wantValid: false,
		},
		{
			name:      "priority tag VID zero",
			message:   validTag(0),
			wantValid: true,
		},
		{
			name:      "reserved tag VID",
			message:   validTag(4095),
			wantValid: false,
		},
		{
			name:      "exact VLAN tag requires every wire field",
			message:   switchingv1.VlanTag_builder{VlanId: proto.Uint32(100)}.Build(),
			wantValid: false,
		},
		{
			name: "VLAN tag rejects IEEE 802.3 length values",
			message: switchingv1.VlanTag_builder{
				Tpid:   packetv1.EtherType(1500).Enum(),
				VlanId: proto.Uint32(100),
				Pcp:    proto.Uint32(0),
				Dei:    proto.Bool(false),
			}.Build(),
			wantValid: false,
		},
		{
			name:      "VLAN tag stack requires a tag",
			message:   switchingv1.VlanTagStack_builder{}.Build(),
			wantValid: false,
		},
		{
			name: "ordered QinQ stack",
			message: switchingv1.VlanTagStack_builder{
				Tags: []*switchingv1.VlanTag{
					switchingv1.VlanTag_builder{
						Tpid:   packetv1.EtherType_ETHER_TYPE_PROVIDER_BRIDGING.Enum(),
						VlanId: proto.Uint32(200),
						Pcp:    proto.Uint32(5),
						Dei:    proto.Bool(false),
					}.Build(),
					validTag(100),
				},
			}.Build(),
			wantValid: true,
		},
		{
			name: "switchport membership rejects duplicates",
			message: switchingv1.SwitchportFacet_builder{
				TaggedVlanIds: []uint32{10, 10},
			}.Build(),
			wantValid: false,
		},
		{
			name: "switchport PVID rejects zero",
			message: switchingv1.SwitchportFacet_builder{
				Pvid: proto.Uint32(0),
			}.Build(),
			wantValid: false,
		},
		{
			name: "switchport tagged and untagged memberships cannot overlap",
			message: switchingv1.SwitchportFacet_builder{
				TaggedVlanIds:   []uint32{10, 20},
				UntaggedVlanIds: []uint32{20, 30},
			}.Build(),
			wantValid: false,
		},
		{
			name: "switchport exact memberships",
			message: switchingv1.SwitchportFacet_builder{
				Pvid:            proto.Uint32(10),
				TaggedVlanIds:   []uint32{20, 30},
				UntaggedVlanIds: []uint32{10},
			}.Build(),
			wantValid: true,
		},
		{
			name:      "aggregation thresholds absent",
			message:   switchingv1.AggregationFacet_builder{}.Build(),
			wantValid: true,
		},
		{
			name: "aggregation minimum active links rejects zero",
			message: switchingv1.AggregationFacet_builder{
				MinimumActiveLinks: proto.Uint32(0),
			}.Build(),
			wantValid: false,
		},
		{
			name: "aggregation minimum active links accepts a positive value",
			message: switchingv1.AggregationFacet_builder{
				MinimumActiveLinks: proto.Uint32(1),
			}.Build(),
			wantValid: true,
		},
		{
			name: "aggregation effective speed rejects zero",
			message: switchingv1.AggregationFacet_builder{
				EffectiveSpeedBps: proto.Uint64(0),
			}.Build(),
			wantValid: false,
		},
		{
			name: "aggregation effective speed accepts a positive value",
			message: switchingv1.AggregationFacet_builder{
				EffectiveSpeedBps: proto.Uint64(10_000_000_000),
			}.Build(),
			wantValid: true,
		},
		{
			name:      "FDB entry requires VLAN and MAC",
			message:   switchingv1.FdbEntry_builder{}.Build(),
			wantValid: false,
		},
		{
			name: "unicast FDB entry",
			message: switchingv1.FdbEntry_builder{
				VlanId: proto.Uint32(10),
				Mac: addrv1.Eui48Address_builder{
					Octets: []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
				}.Build(),
				InterfaceName: proto.String("ethernet1/1"),
			}.Build(),
			wantValid: true,
		},
		{
			name: "FDB entry rejects a multicast address",
			message: switchingv1.FdbEntry_builder{
				VlanId: proto.Uint32(10),
				Mac: addrv1.Eui48Address_builder{
					Octets: []byte{0x01, 0x00, 0x5e, 0x00, 0x00, 0x01},
				}.Build(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "FDB entry accepts a high-bit unicast address",
			message: switchingv1.FdbEntry_builder{
				VlanId: proto.Uint32(10),
				Mac: addrv1.Eui48Address_builder{
					Octets: []byte{0x82, 0x00, 0x00, 0x00, 0x00, 0x01},
				}.Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "FDB entry rejects the broadcast address",
			message: switchingv1.FdbEntry_builder{
				VlanId: proto.Uint32(10),
				Mac: addrv1.Eui48Address_builder{
					Octets: []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
				}.Build(),
			}.Build(),
			wantValid: false,
		},
	}

	runValidationCases(t, tests)
}
