package conformance

import (
	"bytes"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	wlanv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/wlan/v1"
)

func validWlanMac() *addrv1.MacAddress {
	return addrv1.MacAddress_builder{
		Eui48: addrv1.Eui48Address_builder{
			Octets: []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		}.Build(),
	}.Build()
}

func TestBssSsidRules(t *testing.T) {
	errsOn(t, wlanv1.Bss_builder{
		Bssid: validWlanMac(),
		Ssid:  bytes.Repeat([]byte{'a'}, 33),
	}.Build(), "bytes.max_len")

	runValidationCases(t, []validationCase{
		{
			name: "SSID of 32 octets passes",
			message: wlanv1.Bss_builder{
				Bssid: validWlanMac(),
				Ssid:  bytes.Repeat([]byte{'a'}, 32),
			}.Build(),
			wantValid: true,
		},
		{
			name: "non-UTF-8 SSID passes",
			message: wlanv1.Bss_builder{
				Bssid: validWlanMac(),
				Ssid:  []byte{0xff, 0xfe},
			}.Build(),
			wantValid: true,
		},
		{
			name: "empty SSID passes for hidden network",
			message: wlanv1.Bss_builder{
				Bssid: validWlanMac(),
				Ssid:  []byte{},
			}.Build(),
			wantValid: true,
		},
	})
}

func TestWifiChannelWidthRules(t *testing.T) {
	errsOn(t, wlanv1.RadioFacet_builder{
		ChannelWidthMhz: proto.Uint32(60),
	}.Build(), "uint32.wifi_channel_width_mhz")
	errsOn(t, wlanv1.RadioFacet_builder{
		ChannelWidthMhz: proto.Uint32(0),
	}.Build(), "uint32.wifi_channel_width_mhz")

	runValidationCases(t, []validationCase{
		{
			name: "channel width 20 MHz passes",
			message: wlanv1.RadioFacet_builder{
				ChannelWidthMhz: proto.Uint32(20),
			}.Build(),
			wantValid: true,
		},
		{
			name: "channel width 320 MHz passes",
			message: wlanv1.RadioFacet_builder{
				ChannelWidthMhz: proto.Uint32(320),
			}.Build(),
			wantValid: true,
		},
		{
			name: "channel width 2160 MHz passes",
			message: wlanv1.RadioFacet_builder{
				ChannelWidthMhz: proto.Uint32(2160),
			}.Build(),
			wantValid: true,
		},
	})
}

func TestWifiChannelRules(t *testing.T) {
	errsOn(t, wlanv1.RadioFacet_builder{
		Band:           wlanv1.WifiBand_WIFI_BAND_GHZ2P4.Enum(),
		PrimaryChannel: proto.Uint32(0),
	}.Build(), "uint32.wifi_channel")
	errsOn(t, wlanv1.RadioFacet_builder{
		Band:           wlanv1.WifiBand_WIFI_BAND_GHZ6.Enum(),
		PrimaryChannel: proto.Uint32(234),
	}.Build(), "uint32.wifi_channel")

	runValidationCases(t, []validationCase{
		{
			name: "channel 1 passes",
			message: wlanv1.RadioFacet_builder{
				Band:           wlanv1.WifiBand_WIFI_BAND_GHZ2P4.Enum(),
				PrimaryChannel: proto.Uint32(1),
			}.Build(),
			wantValid: true,
		},
		{
			name: "channel 233 passes",
			message: wlanv1.RadioFacet_builder{
				Band:           wlanv1.WifiBand_WIFI_BAND_GHZ6.Enum(),
				PrimaryChannel: proto.Uint32(233),
			}.Build(),
			wantValid: true,
		},
	})
}

func TestRadioFacetBandRules(t *testing.T) {
	errsOn(t, wlanv1.RadioFacet_builder{
		PrimaryChannel: proto.Uint32(6),
	}.Build(), "radio_facet.channel_needs_band")
	errsOn(t, wlanv1.RadioFacet_builder{
		Band:           wlanv1.WifiBand_WIFI_BAND_UNSPECIFIED.Enum(),
		PrimaryChannel: proto.Uint32(6),
	}.Build(), "radio_facet.channel_needs_band")

	runValidationCases(t, []validationCase{
		{
			name: "channel with valid band passes",
			message: wlanv1.RadioFacet_builder{
				Band:           wlanv1.WifiBand_WIFI_BAND_GHZ6.Enum(),
				PrimaryChannel: proto.Uint32(6),
			}.Build(),
			wantValid: true,
		},
	})
}

func TestRadioFacetSecondaryChannelRules(t *testing.T) {
	errsOn(t, wlanv1.RadioFacet_builder{
		Band:             wlanv1.WifiBand_WIFI_BAND_GHZ5.Enum(),
		PrimaryChannel:   proto.Uint32(36),
		ChannelWidthMhz:  proto.Uint32(80),
		SecondaryChannel: proto.Uint32(155),
	}.Build(), "radio_facet.secondary_needs_160")

	runValidationCases(t, []validationCase{
		{
			name: "secondary channel at 160 MHz passes",
			message: wlanv1.RadioFacet_builder{
				Band:             wlanv1.WifiBand_WIFI_BAND_GHZ5.Enum(),
				PrimaryChannel:   proto.Uint32(36),
				ChannelWidthMhz:  proto.Uint32(160),
				SecondaryChannel: proto.Uint32(100),
			}.Build(),
			wantValid: true,
		},
	})
}

func TestRadioFacetCountryEnvironmentRules(t *testing.T) {
	errsOn(t, wlanv1.RadioFacet_builder{
		CountryEnvironment: wlanv1.CountryEnvironment_COUNTRY_ENVIRONMENT_NON_COUNTRY_ENTITY.Enum(),
		CountryCode:        proto.String("US"),
	}.Build(), "radio_facet.non_country_entity_code")
	errsOn(t, wlanv1.RadioFacet_builder{
		CountryCode: proto.String("us"),
	}.Build(), "string.pattern")
	errsOn(t, wlanv1.RadioFacet_builder{
		CountryEnvironment: wlanv1.CountryEnvironment(256).Enum(),
	}.Build(), "radio_facet.country_environment_octet")

	runValidationCases(t, []validationCase{
		{
			name: "XX with non-country-entity passes",
			message: wlanv1.RadioFacet_builder{
				CountryEnvironment: wlanv1.CountryEnvironment_COUNTRY_ENVIRONMENT_NON_COUNTRY_ENTITY.Enum(),
				CountryCode:        proto.String("XX"),
			}.Build(),
			wantValid: true,
		},
		{
			name: "indoor country environment passes",
			message: wlanv1.RadioFacet_builder{
				CountryEnvironment: wlanv1.CountryEnvironment_COUNTRY_ENVIRONMENT_INDOOR.Enum(),
				CountryCode:        proto.String("US"),
			}.Build(),
			wantValid: true,
		},
	})
}

func TestBssRules(t *testing.T) {
	errsOn(t, wlanv1.Bss_builder{
		Bssid:          validWlanMac(),
		BeaconInterval: durationpb.New(1023 * time.Microsecond),
	}.Build(), "duration.gte_lte")
	errsOn(t, wlanv1.Bss_builder{
		Bssid:      validWlanMac(),
		DtimPeriod: proto.Uint32(0),
	}.Build(), "uint32.gte_lte")

	runValidationCases(t, []validationCase{
		{
			name: "valid beacon interval passes",
			message: wlanv1.Bss_builder{
				Bssid:          validWlanMac(),
				BeaconInterval: durationpb.New(102400 * time.Microsecond),
			}.Build(),
			wantValid: true,
		},
		{
			name: "Bss security of 0 passes",
			message: wlanv1.Bss_builder{
				Bssid:    validWlanMac(),
				Security: wlanv1.WlanSecurity_WLAN_SECURITY_UNSPECIFIED.Enum(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "Bss security of undeclared value 99 passes",
			message: wlanv1.Bss_builder{
				Bssid:    validWlanMac(),
				Security: wlanv1.WlanSecurity(99).Enum(),
			}.Build(),
			wantValid: true,
		},
	})
}

func TestChannelUtilizationRules(t *testing.T) {
	errsOn(t, wlanv1.ChannelUtilization_builder{
		TotalAvgBasisPoints: proto.Uint32(10001),
	}.Build(), "uint32.basis_points")

	runValidationCases(t, []validationCase{
		{
			name: "valid utilization basis points passes",
			message: wlanv1.ChannelUtilization_builder{
				TotalAvgBasisPoints: proto.Uint32(3700),
			}.Build(),
			wantValid: true,
		},
	})
}

func TestNeighborBssRules(t *testing.T) {
	errsOn(t, wlanv1.NeighborBss_builder{}.Build(), "required")
	errsOn(t, wlanv1.NeighborBss_builder{
		Bssid:          validWlanMac(),
		PrimaryChannel: proto.Uint32(36),
	}.Build(), "neighbor_bss.channel_needs_band")

	runValidationCases(t, []validationCase{
		{
			name: "valid neighbor BSS passes",
			message: wlanv1.NeighborBss_builder{
				Bssid:          validWlanMac(),
				Ssid:           []byte("Corp-Guest"),
				Band:           wlanv1.WifiBand_WIFI_BAND_GHZ5.Enum(),
				PrimaryChannel: proto.Uint32(36),
			}.Build(),
			wantValid: true,
		},
	})
}
