package protoconformance

import (
	"testing"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"

	l2v1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/l2/v1"
	phyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/phy/v1"
)

func TestNetworkPrimitiveRules(t *testing.T) {
	tests := []struct {
		name      string
		message   proto.Message
		wantValid bool
	}{
		{
			name:      "VLAN identifier absent",
			message:   l2v1.Vlan_builder{}.Build(),
			wantValid: false,
		},
		{
			name:      "VLAN identifier zero",
			message:   l2v1.Vlan_builder{Id: proto.Uint32(0)}.Build(),
			wantValid: false,
		},
		{
			name:      "lowest usable VLAN identifier",
			message:   l2v1.Vlan_builder{Id: proto.Uint32(1)}.Build(),
			wantValid: true,
		},
		{
			name:      "highest usable VLAN identifier",
			message:   l2v1.Vlan_builder{Id: proto.Uint32(4094)}.Build(),
			wantValid: true,
		},
		{
			name:      "reserved VLAN identifier",
			message:   l2v1.Vlan_builder{Id: proto.Uint32(4095)}.Build(),
			wantValid: false,
		},
		{
			name:      "Ethernet attributes absent",
			message:   phyv1.EthernetFacet_builder{}.Build(),
			wantValid: true,
		},
		{
			name:      "zero Ethernet speed",
			message:   phyv1.EthernetFacet_builder{SpeedBps: proto.Uint64(0)}.Build(),
			wantValid: false,
		},
		{
			name:      "positive Ethernet speed",
			message:   phyv1.EthernetFacet_builder{SpeedBps: proto.Uint64(1)}.Build(),
			wantValid: true,
		},
		{
			name: "auto-negotiation enabled without support",
			message: phyv1.AutoNegotiation_builder{
				Supported: proto.Bool(false),
				Enabled:   proto.Bool(true),
			}.Build(),
			wantValid: false,
		},
		{
			name: "auto-negotiation enabled with support",
			message: phyv1.AutoNegotiation_builder{
				Supported: proto.Bool(true),
				Enabled:   proto.Bool(true),
			}.Build(),
			wantValid: true,
		},
		{
			name: "PoE enabled without support",
			message: phyv1.PoeFacet_builder{
				Supported: proto.Bool(false),
				Enabled:   proto.Bool(true),
			}.Build(),
			wantValid: false,
		},
		{
			name: "PoE enabled with support",
			message: phyv1.PoeFacet_builder{
				Supported: proto.Bool(true),
				Enabled:   proto.Bool(true),
			}.Build(),
			wantValid: true,
		},
		{
			name: "empty transceiver form factor",
			message: phyv1.TransceiverFacet_builder{
				FormFactor: proto.String(""),
			}.Build(),
			wantValid: false,
		},
		{
			name: "transceiver identity in an empty cage",
			message: phyv1.TransceiverFacet_builder{
				Present:    proto.Bool(false),
				PartNumber: proto.String("SFP-10G-SR"),
			}.Build(),
			wantValid: false,
		},
		{
			name: "empty transceiver cage",
			message: phyv1.TransceiverFacet_builder{
				Present: proto.Bool(false),
			}.Build(),
			wantValid: true,
		},
		{
			name: "present transceiver with identity",
			message: phyv1.TransceiverFacet_builder{
				Present:    proto.Bool(true),
				PartNumber: proto.String("SFP-10G-SR"),
			}.Build(),
			wantValid: true,
		},
		{
			name: "zero transceiver wavelength",
			message: phyv1.TransceiverFacet_builder{
				WavelengthNanometers: proto.Uint32(0),
			}.Build(),
			wantValid: false,
		},
		{
			name: "positive transceiver wavelength",
			message: phyv1.TransceiverFacet_builder{
				WavelengthNanometers: proto.Uint32(850),
			}.Build(),
			wantValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := protovalidate.Validate(tt.message)
			gotValid := err == nil
			if gotValid != tt.wantValid {
				t.Errorf("got valid=%t, want %t: %v", gotValid, tt.wantValid, err)
			}
		})
	}
}
