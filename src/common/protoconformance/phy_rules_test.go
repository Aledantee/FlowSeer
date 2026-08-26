package protoconformance

import (
	"testing"

	"google.golang.org/protobuf/proto"

	phyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/phy/v1"
)

func TestPhysicalPrimitiveRules(t *testing.T) {
	tests := []validationCase{
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
			name: "requested Ethernet duplex rejects explicit unspecified",
			message: phyv1.EthernetSettings_builder{
				Duplex: phyv1.EthernetDuplex_ETHERNET_DUPLEX_UNSPECIFIED.Enum(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "requested Ethernet FEC rejects explicit unspecified",
			message: phyv1.EthernetSettings_builder{
				FecMode: phyv1.EthernetFecMode_ETHERNET_FEC_MODE_UNSPECIFIED.Enum(),
			}.Build(),
			wantValid: false,
		},
		{
			name:      "active Ethernet speed rejects zero",
			message:   phyv1.EthernetFacet_builder{ActiveSpeedBps: proto.Uint64(0)}.Build(),
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
				AppliedAutoNegotiation: phyv1.AutoNegotiationFacet_builder{Enabled: proto.Bool(true)}.Build(),
				Capabilities: phyv1.EthernetCapabilities_builder{
					AutoNegotiationSupported: proto.Bool(false),
				}.Build(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "auto-negotiation enabled with support",
			message: phyv1.EthernetFacet_builder{
				AppliedAutoNegotiation: phyv1.AutoNegotiationFacet_builder{Enabled: proto.Bool(true)}.Build(),
				Capabilities: phyv1.EthernetCapabilities_builder{
					AutoNegotiationSupported: proto.Bool(true),
				}.Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name:      "PoE settings may omit a power limit",
			message:   phyv1.PoeSettings_builder{}.Build(),
			wantValid: true,
		},
		{
			name: "PoE settings accept an explicit zero power limit",
			message: phyv1.PoeSettings_builder{
				PowerLimitMilliwatts: proto.Uint32(0),
			}.Build(),
			wantValid: true,
		},
		{
			name: "PoE settings reject explicit unspecified priority",
			message: phyv1.PoeSettings_builder{
				Priority: phyv1.PoePriority_POE_PRIORITY_UNSPECIFIED.Enum(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "PoE delivery requires support",
			message: phyv1.PoeFacet_builder{
				Supported: proto.Bool(false),
				Status:    phyv1.PoeStatus_POE_STATUS_DELIVERING_POWER.Enum(),
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

	runValidationCases(t, tests)
}

func TestPoeSettingsPowerLimitPresence(t *testing.T) {
	absent := phyv1.PoeSettings_builder{}.Build()
	explicitZero := phyv1.PoeSettings_builder{PowerLimitMilliwatts: proto.Uint32(0)}.Build()

	if absent.HasPowerLimitMilliwatts() {
		t.Fatal("omitted power limit is present")
	}
	if !explicitZero.HasPowerLimitMilliwatts() {
		t.Fatal("explicit zero power limit is absent")
	}
	if got := explicitZero.GetPowerLimitMilliwatts(); got != 0 {
		t.Fatalf("explicit zero power limit = %d, want 0", got)
	}
}

func TestEthernetMediumRenumberedValues(t *testing.T) {
	// The pre-release reserved-tombstone collapse renumbered the value that
	// followed the removed direct-attach slot; pin the highest surviving value
	// so an accidental re-renumber cannot land silently.
	if got := int32(phyv1.EthernetMedium_ETHERNET_MEDIUM_OTHER); got != 4 {
		t.Fatalf("ETHERNET_MEDIUM_OTHER = %d, want 4", got)
	}
}
