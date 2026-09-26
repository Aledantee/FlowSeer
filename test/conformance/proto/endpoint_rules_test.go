package conformance

import (
	"strings"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	endpointv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/endpoint/v1"
	wlanv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/wlan/v1"
)

func validEndpointMac() *addrv1.MacAddress {
	return addrv1.MacAddress_builder{
		Eui48: addrv1.Eui48Address_builder{
			Octets: []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		}.Build(),
	}.Build()
}

func validWirelessAttachment() endpointv1.WirelessAttachment_builder {
	return endpointv1.WirelessAttachment_builder{
		ApName:            proto.String("ap-floor2-north"),
		Bssid:             validEndpointMac(),
		Ssid:              []byte("Staff-Secure"),
		Band:              wlanv1.WifiBand_WIFI_BAND_GHZ5.Enum(),
		Channel:           proto.Uint32(36),
		RssiMillidbm:      proto.Int32(-65000),
		SnrMillidb:        proto.Int32(30000),
		NegotiatedRateBps: proto.Uint64(866700000),
		Mcs:               proto.Uint32(9),
		Nss:               proto.Uint32(2),
		GuardInterval:     durationpb.New(400 * time.Nanosecond),
	}
}

func TestWirelessAttachmentBandAndChannelRules(t *testing.T) {
	// Channel set without band fails CEL rule.
	badChannelNoBand := validWirelessAttachment()
	badChannelNoBand.Band = nil
	badChannelNoBand.Channel = proto.Uint32(6)
	errsOn(t, badChannelNoBand.Build(), "wireless_attachment.channel_needs_band")

	// Channel set with unspecified band (0) fails CEL rule.
	badChannelZeroBand := validWirelessAttachment()
	badChannelZeroBand.Band = wlanv1.WifiBand_WIFI_BAND_UNSPECIFIED.Enum()
	badChannelZeroBand.Channel = proto.Uint32(6)
	errsOn(t, badChannelZeroBand.Build(), "wireless_attachment.channel_needs_band")

	// Channel set with 2.4 GHz band passes.
	goodChannel24 := validWirelessAttachment()
	goodChannel24.Band = wlanv1.WifiBand_WIFI_BAND_GHZ2P4.Enum()
	goodChannel24.Channel = proto.Uint32(6)

	// Channel set with 5 GHz band and channel 1 passes.
	goodChannel1 := validWirelessAttachment()
	goodChannel1.Band = wlanv1.WifiBand_WIFI_BAND_GHZ2P4.Enum()
	goodChannel1.Channel = proto.Uint32(1)

	// Channel set with 6 GHz band and channel 233 passes.
	goodChannel233 := validWirelessAttachment()
	goodChannel233.Band = wlanv1.WifiBand_WIFI_BAND_GHZ6.Enum()
	goodChannel233.Channel = proto.Uint32(233)

	runValidationCases(t, []validationCase{
		{name: "channel 6 on 2.4GHz passes", message: goodChannel24.Build(), wantValid: true},
		{name: "channel 1 passes", message: goodChannel1.Build(), wantValid: true},
		{name: "channel 233 passes", message: goodChannel233.Build(), wantValid: true},
		{name: "channel 0 fails wifi_channel", message: endpointv1.WirelessAttachment_builder{
			Bssid:   validEndpointMac(),
			Band:    wlanv1.WifiBand_WIFI_BAND_GHZ2P4.Enum(),
			Channel: proto.Uint32(0),
		}.Build(), wantValid: false},
		{name: "channel 234 fails wifi_channel", message: endpointv1.WirelessAttachment_builder{
			Bssid:   validEndpointMac(),
			Band:    wlanv1.WifiBand_WIFI_BAND_GHZ6.Enum(),
			Channel: proto.Uint32(234),
		}.Build(), wantValid: false},
		{name: "no channel and no band passes", message: endpointv1.WirelessAttachment_builder{
			Bssid: validEndpointMac(),
		}.Build(), wantValid: true},
	})
}

func TestWirelessAttachmentBoundsAndParameters(t *testing.T) {
	// BSSID is required.
	noBssid := validWirelessAttachment()
	noBssid.Bssid = nil

	// SSID bounds: 32 bytes max, non-UTF-8 permitted.
	ssid32 := validWirelessAttachment()
	ssid32.Ssid = []byte("12345678901234567890123456789012")

	ssidNonUTF8 := validWirelessAttachment()
	ssidNonUTF8.Ssid = []byte{0xff, 0xfe, 0x00, 0x01, 0x80}

	ssid33 := validWirelessAttachment()
	ssid33.Ssid = []byte("123456789012345678901234567890123")

	// MCS bounds: 0..31 passes, 32 fails.
	mcs31 := validWirelessAttachment()
	mcs31.Mcs = proto.Uint32(31)

	mcs32 := validWirelessAttachment()
	mcs32.Mcs = proto.Uint32(32)

	// NSS bounds: 1..8 passes, 0 and 9 fail.
	nss1 := validWirelessAttachment()
	nss1.Nss = proto.Uint32(1)

	nss8 := validWirelessAttachment()
	nss8.Nss = proto.Uint32(8)

	nss0 := validWirelessAttachment()
	nss0.Nss = proto.Uint32(0)

	nss9 := validWirelessAttachment()
	nss9.Nss = proto.Uint32(9)

	// AP name bounds: 1..256.
	apNameEmpty := validWirelessAttachment()
	apNameEmpty.ApName = proto.String("")

	apName256 := validWirelessAttachment()
	apName256.ApName = proto.String(strings.Repeat("a", 256))

	apName257 := validWirelessAttachment()
	apName257.ApName = proto.String(strings.Repeat("a", 257))

	// Guard interval duration 400ns.
	gi400 := validWirelessAttachment()
	gi400.GuardInterval = durationpb.New(400 * time.Nanosecond)

	runValidationCases(t, []validationCase{
		{name: "valid wireless attachment passes", message: validWirelessAttachment().Build(), wantValid: true},
		{name: "missing bssid fails", message: noBssid.Build(), wantValid: false},
		{name: "32-byte ssid passes", message: ssid32.Build(), wantValid: true},
		{name: "non-UTF-8 ssid passes", message: ssidNonUTF8.Build(), wantValid: true},
		{name: "33-byte ssid fails", message: ssid33.Build(), wantValid: false},
		{name: "mcs 31 passes", message: mcs31.Build(), wantValid: true},
		{name: "mcs 32 fails", message: mcs32.Build(), wantValid: false},
		{name: "nss 1 passes", message: nss1.Build(), wantValid: true},
		{name: "nss 8 passes", message: nss8.Build(), wantValid: true},
		{name: "nss 0 fails", message: nss0.Build(), wantValid: false},
		{name: "nss 9 fails", message: nss9.Build(), wantValid: false},
		{name: "ap_name empty string fails", message: apNameEmpty.Build(), wantValid: false},
		{name: "ap_name 256 chars passes", message: apName256.Build(), wantValid: true},
		{name: "ap_name 257 chars fails", message: apName257.Build(), wantValid: false},
		{name: "guard interval 400ns passes", message: gi400.Build(), wantValid: true},
	})
}

func TestDhcpOptionCodeRules(t *testing.T) {
	runValidationCases(t, []validationCase{
		{name: "option code 55 passes", message: endpointv1.EndpointFingerprint_builder{
			DhcpParameterRequestList: []uint32{55},
		}.Build(), wantValid: true},
		{name: "option codes 1 and 254 pass", message: endpointv1.EndpointFingerprint_builder{
			DhcpParameterRequestList: []uint32{1, 254},
		}.Build(), wantValid: true},
		{name: "option code 0 fails dhcp_option_code", message: endpointv1.EndpointFingerprint_builder{
			DhcpParameterRequestList: []uint32{0},
		}.Build(), wantValid: false},
		{name: "option code 255 fails dhcp_option_code", message: endpointv1.EndpointFingerprint_builder{
			DhcpParameterRequestList: []uint32{255},
		}.Build(), wantValid: false},
		{name: "vendor class 255 bytes passes", message: endpointv1.EndpointFingerprint_builder{
			DhcpVendorClass: proto.String(strings.Repeat("v", 255)),
		}.Build(), wantValid: true},
		{name: "vendor class 256 bytes fails", message: endpointv1.EndpointFingerprint_builder{
			DhcpVendorClass: proto.String(strings.Repeat("v", 256)),
		}.Build(), wantValid: false},
		{name: "user agent 1024 chars passes", message: endpointv1.EndpointFingerprint_builder{
			HttpUserAgent: proto.String(strings.Repeat("u", 1024)),
		}.Build(), wantValid: true},
		{name: "user agent 1025 chars fails", message: endpointv1.EndpointFingerprint_builder{
			HttpUserAgent: proto.String(strings.Repeat("u", 1025)),
		}.Build(), wantValid: false},
	})
}

func TestEndpointCountersRules(t *testing.T) {
	now := timestamppb.Now()
	counters := endpointv1.EndpointCounters_builder{
		InBytes:           proto.Uint64(1024),
		OutBytes:          proto.Uint64(2048),
		InFrames:          proto.Uint64(10),
		OutFrames:         proto.Uint64(20),
		TxRetryFrames:     proto.Uint64(2),
		LastDiscontinuity: now,
	}.Build()

	if err := protovalidate.Validate(counters); err != nil {
		t.Fatalf("EndpointCounters validation failed: %v", err)
	}
	if !counters.HasLastDiscontinuity() {
		t.Errorf("counters.HasLastDiscontinuity() = false, want true")
	}
	if got := counters.GetInBytes(); got != 1024 {
		t.Errorf("counters.GetInBytes() = %d, want 1024", got)
	}

	emptyCounters := endpointv1.EndpointCounters_builder{}.Build()
	if err := protovalidate.Validate(emptyCounters); err != nil {
		t.Fatalf("empty EndpointCounters validation failed: %v", err)
	}
}

func TestWiredAttachmentRules(t *testing.T) {
	runValidationCases(t, []validationCase{
		{name: "valid wired attachment passes", message: endpointv1.WiredAttachment_builder{
			SwitchName:    proto.String("sw-core-01"),
			InterfaceName: proto.String("GigabitEthernet1/0/24"),
			VlanId:        proto.Uint32(100),
		}.Build(), wantValid: true},
		{name: "vlan_id 1 passes", message: endpointv1.WiredAttachment_builder{
			VlanId: proto.Uint32(1),
		}.Build(), wantValid: true},
		{name: "vlan_id 4094 passes", message: endpointv1.WiredAttachment_builder{
			VlanId: proto.Uint32(4094),
		}.Build(), wantValid: true},
		{name: "vlan_id 0 fails", message: endpointv1.WiredAttachment_builder{
			VlanId: proto.Uint32(0),
		}.Build(), wantValid: false},
		{name: "vlan_id 4095 fails", message: endpointv1.WiredAttachment_builder{
			VlanId: proto.Uint32(4095),
		}.Build(), wantValid: false},
		{name: "switch_name 256 chars passes", message: endpointv1.WiredAttachment_builder{
			SwitchName: proto.String(strings.Repeat("s", 256)),
		}.Build(), wantValid: true},
		{name: "switch_name 257 chars fails", message: endpointv1.WiredAttachment_builder{
			SwitchName: proto.String(strings.Repeat("s", 257)),
		}.Build(), wantValid: false},
		{name: "interface_name empty fails", message: endpointv1.WiredAttachment_builder{
			InterfaceName: proto.String(""),
		}.Build(), wantValid: false},
	})
}
