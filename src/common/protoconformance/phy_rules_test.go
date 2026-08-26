package protoconformance

import (
	"testing"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"

	phyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/phy/v1"
)

func TestPhysicalPrimitiveRules(t *testing.T) {
	tests := []struct {
		name      string
		message   proto.Message
		wantValid bool
	}{
		{
			name:      "Ethernet facts may be absent",
			message:   phyv1.EthernetFacet_builder{}.Build(),
			wantValid: true,
		},
		{
			name:      "requested Ethernet speed rejects zero",
			message:   phyv1.EthernetSettings_builder{SpeedBps: proto.Uint64(0)}.Build(),
			wantValid: false,
		},
		{
			name:      "active Ethernet speed rejects zero",
			message:   phyv1.EthernetFacet_builder{SpeedBps: proto.Uint64(0)}.Build(),
			wantValid: false,
		},
		{
			name: "supported Ethernet speeds are unique",
			message: phyv1.EthernetCapabilities_builder{
				SupportedSpeedsBps: []uint64{1_000_000_000, 1_000_000_000},
			}.Build(),
			wantValid: false,
		},
		{
			name: "auto-negotiation enabled without support",
			message: phyv1.EthernetFacet_builder{
				AutoNegotiation: phyv1.AutoNegotiationFacet_builder{Enabled: proto.Bool(true)}.Build(),
				Capabilities: phyv1.EthernetCapabilities_builder{
					AutoNegotiationSupported: proto.Bool(false),
				}.Build(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "auto-negotiation enabled with support",
			message: phyv1.EthernetFacet_builder{
				AutoNegotiation: phyv1.AutoNegotiationFacet_builder{Enabled: proto.Bool(true)}.Build(),
				Capabilities: phyv1.EthernetCapabilities_builder{
					AutoNegotiationSupported: proto.Bool(true),
				}.Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "PoE delivery requires support",
			message: phyv1.PoeFacet_builder{
				Supported: proto.Bool(false),
				Status:    pointerTo(phyv1.PoeStatus_POE_STATUS_DELIVERING_POWER),
			}.Build(),
			wantValid: false,
		},
		{
			name: "PoE class zero is a real value",
			message: phyv1.PoeFacet_builder{
				Supported:  proto.Bool(true),
				PowerClass: proto.Uint32(0),
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
			name: "present transceiver with identity",
			message: phyv1.TransceiverFacet_builder{
				Present:    proto.Bool(true),
				PartNumber: proto.String("SFP-10G-SR"),
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
