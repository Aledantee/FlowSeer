package conformance

import (
	"encoding/hex"
	"testing"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"

	wlanv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/wlan/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/ruckus/ap"
)

// ruckusAPStatusRadioSampleHex holds 37 bytes of a Ruckus APStatusRadio message:
//
//	08 01                            tag 1  (radioId) = 1
//	10 24                            tag 2  (channel) = 36
//	22 02 35 47                      tag 4  (band) = "5G"
//	48 a1 ff ff ff ff ff ff ff ff 01 tag 9  (noiseFloor) = -95
//	b0 01 25                         tag 22 (total) = 37
//	d0 01 50                         tag 26 (channelWidth) = 80
//	c0 02 01                         tag 40 (isRadioEnabled) = true
//	d0 02 14                         tag 42 (eirp) = 20
//	a8 03 11                         tag 53 (actualTxPower) = 17
//	b8 03 17                         tag 55 (maxTxPower) = 23
const ruckusAPStatusRadioSampleHex = "080110242202354748a1ffffffffffffffff01b00125d00150c00201d00214a80311b80317"

func radioFacetFromRuckus(in *ap.APStatusRadio) *wlanv1.RadioFacet {
	b := wlanv1.RadioFacet_builder{}

	if in.HasRadioId() {
		b.RadioIndex = proto.Uint32(uint32(in.GetRadioId()))
	}

	switch in.GetBand() {
	case "2.4G":
		b.Band = wlanv1.WifiBand_WIFI_BAND_GHZ2P4.Enum()
	case "5G":
		b.Band = wlanv1.WifiBand_WIFI_BAND_GHZ5.Enum()
	case "6G":
		b.Band = wlanv1.WifiBand_WIFI_BAND_GHZ6.Enum()
	default:
		// Any other band string leaves band and channels unpopulated.
	}

	if b.Band != nil {
		if in.HasChannel() && in.GetChannel() > 0 {
			b.PrimaryChannel = proto.Uint32(uint32(in.GetChannel()))
		}
		if in.HasSecondaryChannel() && in.GetSecondaryChannel() > 0 {
			b.SecondaryChannel = proto.Uint32(in.GetSecondaryChannel())
		}
	}

	if in.HasChannelWidth() && in.GetChannelWidth() > 0 {
		b.ChannelWidthMhz = proto.Uint32(in.GetChannelWidth())
	}

	if in.HasIsRadioEnabled() {
		if in.GetIsRadioEnabled() {
			b.OperStatus = wlanv1.RadioOperStatus_RADIO_OPER_STATUS_UP.Enum()
		} else {
			b.OperStatus = wlanv1.RadioOperStatus_RADIO_OPER_STATUS_DOWN.Enum()
		}
	}

	if in.HasActualTxPower() {
		b.TxPowerMillidbm = proto.Int32(in.GetActualTxPower() * 1000)
	}
	if in.HasMaxTxPower() {
		b.MaxTxPowerMillidbm = proto.Int32(in.GetMaxTxPower() * 1000)
	}
	if in.HasEirp() {
		b.EirpMillidbm = proto.Int32(in.GetEirp() * 1000)
	}
	if in.HasNoiseFloor() {
		b.NoiseFloorMillidbm = proto.Int32(in.GetNoiseFloor() * 1000)
	}

	if in.HasTotal() {
		b.ChannelUtilization = wlanv1.ChannelUtilization_builder{
			TotalAvgBasisPoints: proto.Uint32(in.GetTotal() * 100),
		}.Build()
	}

	return b.Build()
}

func TestRuckusAPStatusRadioFixture(t *testing.T) {
	wireBytes, err := hex.DecodeString(ruckusAPStatusRadioSampleHex)
	if err != nil {
		t.Fatalf("hex.DecodeString: %v", err)
	}
	if len(wireBytes) != 37 {
		t.Fatalf("got wire bytes length %d, want 37", len(wireBytes))
	}

	var vendor ap.APStatusRadio
	if err := proto.Unmarshal(wireBytes, &vendor); err != nil {
		t.Fatalf("proto.Unmarshal: %v", err)
	}

	// Assert decoded vendor fields match Requirement 3 specification.
	if got, want := vendor.GetRadioId(), int32(1); got != want {
		t.Errorf("vendor radioId = %d, want %d", got, want)
	}
	if got, want := vendor.GetChannel(), int32(36); got != want {
		t.Errorf("vendor channel = %d, want %d", got, want)
	}
	if got, want := vendor.GetBand(), "5G"; got != want {
		t.Errorf("vendor band = %q, want %q", got, want)
	}
	if got, want := vendor.GetNoiseFloor(), int32(-95); got != want {
		t.Errorf("vendor noiseFloor = %d, want %d", got, want)
	}
	if got, want := vendor.GetTotal(), uint32(37); got != want {
		t.Errorf("vendor total = %d, want %d", got, want)
	}
	if got, want := vendor.GetChannelWidth(), uint32(80); got != want {
		t.Errorf("vendor channelWidth = %d, want %d", got, want)
	}
	if got, want := vendor.GetIsRadioEnabled(), true; got != want {
		t.Errorf("vendor isRadioEnabled = %t, want %t", got, want)
	}
	if got, want := vendor.GetEirp(), int32(20); got != want {
		t.Errorf("vendor eirp = %d, want %d", got, want)
	}
	if got, want := vendor.GetActualTxPower(), int32(17); got != want {
		t.Errorf("vendor actualTxPower = %d, want %d", got, want)
	}
	if got, want := vendor.GetMaxTxPower(), int32(23); got != want {
		t.Errorf("vendor maxTxPower = %d, want %d", got, want)
	}

	facet := radioFacetFromRuckus(&vendor)

	// Assert converted facet fields.
	if got, want := facet.GetRadioIndex(), uint32(1); got != want {
		t.Errorf("facet radio_index = %d, want %d", got, want)
	}
	if got, want := facet.GetBand(), wlanv1.WifiBand_WIFI_BAND_GHZ5; got != want {
		t.Errorf("facet band = %v, want %v", got, want)
	}
	if got, want := facet.GetPrimaryChannel(), uint32(36); got != want {
		t.Errorf("facet primary_channel = %d, want %d", got, want)
	}
	if got, want := facet.GetChannelWidthMhz(), uint32(80); got != want {
		t.Errorf("facet channel_width_mhz = %d, want %d", got, want)
	}
	if got, want := facet.GetOperStatus(), wlanv1.RadioOperStatus_RADIO_OPER_STATUS_UP; got != want {
		t.Errorf("facet oper_status = %v, want %v", got, want)
	}
	if got, want := facet.GetTxPowerMillidbm(), int32(17000); got != want {
		t.Errorf("facet tx_power_millidbm = %d, want %d", got, want)
	}
	if got, want := facet.GetMaxTxPowerMillidbm(), int32(23000); got != want {
		t.Errorf("facet max_tx_power_millidbm = %d, want %d", got, want)
	}
	if got, want := facet.GetEirpMillidbm(), int32(20000); got != want {
		t.Errorf("facet eirp_millidbm = %d, want %d", got, want)
	}
	if got, want := facet.GetNoiseFloorMillidbm(), int32(-95000); got != want {
		t.Errorf("facet noise_floor_millidbm = %d, want %d", got, want)
	}
	if facet.GetChannelUtilization() == nil || facet.GetChannelUtilization().GetTotalAvgBasisPoints() != 3700 {
		t.Errorf("facet channel_utilization.total_avg_basis_points = %v, want 3700", facet.GetChannelUtilization())
	}

	if err := protovalidate.Validate(facet); err != nil {
		t.Errorf("converted RadioFacet failed validation: %v", err)
	}
}

func TestRuckusAPStatusRadioUnrecognizedBand(t *testing.T) {
	vendor := ap.APStatusRadio_builder{
		RadioId:        proto.Int32(1),
		Channel:        proto.Int32(36),
		Band:           proto.String("60G"),
		ChannelWidth:   proto.Uint32(80),
		IsRadioEnabled: proto.Bool(true),
		ActualTxPower:  proto.Int32(17),
	}.Build()

	facet := radioFacetFromRuckus(vendor)

	if facet.HasBand() {
		t.Errorf("facet has band %v, want unpopulated", facet.GetBand())
	}
	if facet.HasPrimaryChannel() {
		t.Errorf("facet has primary_channel %d, want unpopulated", facet.GetPrimaryChannel())
	}

	if err := protovalidate.Validate(facet); err != nil {
		t.Errorf("facet with unrecognized band failed validation: %v", err)
	}
}
