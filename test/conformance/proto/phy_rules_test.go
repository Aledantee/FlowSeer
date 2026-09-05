package conformance

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
	}

	runValidationCases(t, tests)
}

func TestEthernetTransportRules(t *testing.T) {
	tests := []validationCase{
		{
			name: "copper arm carries PoE detail",
			message: phyv1.EthernetFacet_builder{
				Copper: phyv1.CopperFacet_builder{
					Poe: phyv1.PoeFacet_builder{Supported: proto.Bool(true)}.Build(),
					PoeDetail: phyv1.PoePortDetail_builder{
						PseGroup: proto.Uint32(1),
						PsePort:  proto.Uint32(3),
					}.Build(),
				}.Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "bare fiber arm validates",
			message: phyv1.EthernetFacet_builder{
				Fiber: phyv1.FiberFacet_builder{}.Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "bare backplane arm validates",
			message: phyv1.EthernetFacet_builder{
				Backplane: phyv1.BackplaneFacet_builder{}.Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "bare other arm validates",
			message: phyv1.EthernetFacet_builder{
				Other: phyv1.OtherTransport_builder{}.Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "MAU type by IANA registration number",
			message: phyv1.EthernetFacet_builder{
				MauType: phyv1.MauType_builder{Iana: proto.Uint32(200)}.Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name:      "MAU type by raw object identifier",
			message:   phyv1.MauType_builder{Oid: proto.String("1.3.6.1.4.1.9.99")}.Build(),
			wantValid: true,
		},
		{
			name:      "empty MAU type fails the required oneof",
			message:   phyv1.MauType_builder{}.Build(),
			wantValid: false,
		},
		{
			name:      "MAU registration number zero is invalid",
			message:   phyv1.MauType_builder{Iana: proto.Uint32(0)}.Build(),
			wantValid: false,
		},
		{
			name:      "MAU object identifier must be dotted arcs",
			message:   phyv1.MauType_builder{Oid: proto.String("dot3MauType")}.Build(),
			wantValid: false,
		},
		{
			name: "unnamed link mode remains valid",
			message: phyv1.EthernetFacet_builder{
				AdvertisedLinkModes: []phyv1.MauLinkMode{phyv1.MauLinkMode(99)},
			}.Build(),
			wantValid: true,
		},
		{
			name: "link modes are unique",
			message: phyv1.EthernetFacet_builder{
				ReceivedLinkModes: []phyv1.MauLinkMode{
					phyv1.MauLinkMode_MAU_LINK_MODE_BASE_T_GBPS1_FULL,
					phyv1.MauLinkMode_MAU_LINK_MODE_BASE_T_GBPS1_FULL,
				},
			}.Build(),
			wantValid: false,
		},
		{
			name: "PoE port detail rejects group zero",
			message: phyv1.PoePortDetail_builder{
				PseGroup: proto.Uint32(0),
				PsePort:  proto.Uint32(1),
			}.Build(),
			wantValid: false,
		},
		{
			name: "PoE port detail requires its key",
			message: phyv1.PoePortDetail_builder{
				OverloadCount: proto.Uint64(2),
			}.Build(),
			wantValid: false,
		},
		{
			name: "PoE port detail with key and no counters",
			message: phyv1.PoePortDetail_builder{
				PseGroup: proto.Uint32(2),
				PsePort:  proto.Uint32(7),
			}.Build(),
			wantValid: true,
		},
		{
			name: "PSE budget with distinct group",
			message: phyv1.PseBudget_builder{
				PseGroup:              proto.Uint32(2),
				PowerMilliwatts:       proto.Uint32(370_000),
				ConsumptionMilliwatts: proto.Uint32(0),
				UsageThresholdPercent: proto.Uint32(80),
				OperStatus:            phyv1.PseOperStatus_PSE_OPER_STATUS_ON.Enum(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "PSE budget usage threshold above 99 percent",
			message: phyv1.PseBudget_builder{
				PseGroup:              proto.Uint32(1),
				UsageThresholdPercent: proto.Uint32(100),
			}.Build(),
			wantValid: false,
		},
		{
			name:      "PSE budget requires its group",
			message:   phyv1.PseBudget_builder{PowerMilliwatts: proto.Uint32(1000)}.Build(),
			wantValid: false,
		},
	}

	runValidationCases(t, tests)
}

func TestPluggableModuleRules(t *testing.T) {
	lane := func(index uint32) *phyv1.ModuleLane {
		return phyv1.ModuleLane_builder{
			Index: proto.Uint32(index),
			TxPower: phyv1.OpticalPower_builder{
				ValueNanowatts: proto.Uint32(500_000),
			}.Build(),
		}.Build()
	}

	tests := []validationCase{
		{
			name: "facet without transport or module validates",
			message: phyv1.EthernetFacet_builder{
				ActiveSpeedBps: proto.Uint64(1_000_000_000),
			}.Build(),
			wantValid: true,
		},
		{
			name: "empty cage with a vendor name",
			message: phyv1.PluggableModule_builder{
				Present: proto.Bool(false),
				Vendor:  proto.String("FiberCo"),
			}.Build(),
			wantValid: false,
		},
		{
			name:      "empty cage with nothing else",
			message:   phyv1.PluggableModule_builder{Present: proto.Bool(false)}.Build(),
			wantValid: true,
		},
		{
			name: "empty cage with a lane measurement",
			message: phyv1.PluggableModule_builder{
				Present: proto.Bool(false),
				Lanes:   []*phyv1.ModuleLane{lane(1)},
			}.Build(),
			wantValid: false,
		},
		{
			name:      "module presence is required",
			message:   phyv1.PluggableModule_builder{Vendor: proto.String("FiberCo")}.Build(),
			wantValid: false,
		},
		{
			name: "direct-attach cable on a copper arm carries identity",
			message: phyv1.EthernetFacet_builder{
				Copper: phyv1.CopperFacet_builder{}.Build(),
				Module: phyv1.PluggableModule_builder{
					Present:      proto.Bool(true),
					Vendor:       proto.String("CableCo"),
					SerialNumber: proto.String("DAC-0001"),
					Connector:    phyv1.ModuleConnector_MODULE_CONNECTOR_NONE.Enum(),
				}.Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "four lanes with distinct indexes",
			message: phyv1.PluggableModule_builder{
				Present: proto.Bool(true),
				Lanes:   []*phyv1.ModuleLane{lane(1), lane(2), lane(3), lane(4)},
			}.Build(),
			wantValid: true,
		},
		{
			name: "two lanes sharing an index",
			message: phyv1.PluggableModule_builder{
				Present: proto.Bool(true),
				Lanes:   []*phyv1.ModuleLane{lane(1), lane(2), lane(2)},
			}.Build(),
			wantValid: false,
		},
		{
			name:      "lane index zero is invalid",
			message:   lane(0),
			wantValid: false,
		},
		{
			name: "high alarm below high warning",
			message: phyv1.OpticalPower_builder{
				HighWarningNanowatts: proto.Uint32(900_000),
				HighAlarmNanowatts:   proto.Uint32(800_000),
			}.Build(),
			wantValid: false,
		},
		{
			name: "two of four thresholds in order",
			message: phyv1.BiasCurrent_builder{
				LowAlarmMicroamperes:   proto.Uint32(2_000),
				LowWarningMicroamperes: proto.Uint32(3_000),
			}.Build(),
			wantValid: true,
		},
		{
			name: "temperature thresholds out of order",
			message: phyv1.ModuleTemperature_builder{
				LowAlarmMillidegrees:   proto.Int32(-5_000),
				LowWarningMillidegrees: proto.Int32(-10_000),
			}.Build(),
			wantValid: false,
		},
		{
			name: "empty vendor string",
			message: phyv1.PluggableModule_builder{
				Present: proto.Bool(true),
				Vendor:  proto.String(""),
			}.Build(),
			wantValid: false,
		},
		{
			name: "unnamed form factor remains valid",
			message: phyv1.PluggableModule_builder{
				Present:    proto.Bool(true),
				FormFactor: phyv1.ModuleFormFactor(200).Enum(),
			}.Build(),
			wantValid: true,
		},
		{
			name:      "present module with no lanes",
			message:   phyv1.PluggableModule_builder{Present: proto.Bool(true)}.Build(),
			wantValid: true,
		},
		{
			name: "encoding code above the registry width",
			message: phyv1.PluggableModule_builder{
				Present:      proto.Bool(true),
				EncodingCode: proto.Uint32(256),
			}.Build(),
			wantValid: false,
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

func TestMauLinkModeWireValues(t *testing.T) {
	// Values are IANA-MAU-MIB bit positions; readers depend on them.
	if got := int32(phyv1.MauLinkMode_MAU_LINK_MODE_OTHER); got != 0 {
		t.Fatalf("MAU_LINK_MODE_OTHER = %d, want 0", got)
	}
	if got := int32(phyv1.MauLinkMode_MAU_LINK_MODE_BASE_T_GBPS1_FULL); got != 15 {
		t.Fatalf("MAU_LINK_MODE_BASE_T_GBPS1_FULL = %d, want 15", got)
	}
}
