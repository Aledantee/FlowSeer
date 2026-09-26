package conformance

import (
	"testing"

	"google.golang.org/protobuf/proto"

	wirelessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/wireless/v1"
	wlanv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/wlan/v1"
)

const validWlanID = "12345678-1234-4234-8234-123456789abc"

func validWlanRef() *wirelessv1.WlanGlobalRef {
	return wirelessv1.WlanGlobalRef_builder{
		Wlan: wirelessv1.WlanLocalRef_builder{
			Id: proto.String(validWlanID),
		}.Build(),
	}.Build()
}

func validWlanBroadcast() *wirelessv1.WlanBroadcast {
	return wirelessv1.WlanBroadcast_builder{
		Radio: componentRef(topologyDevA, "wifi0"),
		Bssid: validWlanMac(),
	}.Build()
}

func validWlanConfig() wirelessv1.WlanConfig_builder {
	return wirelessv1.WlanConfig_builder{
		Ref:      validWlanRef(),
		Name:     proto.String("Corporate-Secure"),
		Ssid:     []byte("Corporate-Secure"),
		Security: wlanv1.WlanSecurity_WLAN_SECURITY_WPA3_PERSONAL.Enum(),
		Pmf:      wlanv1.PmfMode_PMF_MODE_REQUIRED.Enum(),
		VlanId:   proto.Uint32(100),
		Enabled:  proto.Bool(true),
		Bands: []wlanv1.WifiBand{
			wlanv1.WifiBand_WIFI_BAND_GHZ5,
			wlanv1.WifiBand_WIFI_BAND_GHZ6,
		},
	}
}

func validWlanState() wirelessv1.WlanState_builder {
	return wirelessv1.WlanState_builder{
		Ref:        validWlanRef(),
		Status:     wirelessv1.WlanStatus_WLAN_STATUS_BROADCASTING.Enum(),
		Ssid:       []byte("Corporate-Secure"),
		Security:   wlanv1.WlanSecurity_WLAN_SECURITY_WPA3_PERSONAL.Enum(),
		Pmf:        wlanv1.PmfMode_PMF_MODE_REQUIRED.Enum(),
		VlanId:     proto.Uint32(100),
		Enabled:    proto.Bool(true),
		Broadcasts: []*wirelessv1.WlanBroadcast{validWlanBroadcast()},
	}
}

func TestWlanConfigPmfSecurityRules(t *testing.T) {
	badPmf := validWlanConfig()
	badPmf.Security = wlanv1.WlanSecurity_WLAN_SECURITY_WPA3_PERSONAL.Enum()
	badPmf.Pmf = wlanv1.PmfMode_PMF_MODE_OPTIONAL.Enum()
	errsOn(t, badPmf.Build(), "wlan_config.pmf_matches_security")

	badEnterprisePmf := validWlanConfig()
	badEnterprisePmf.Security = wlanv1.WlanSecurity_WLAN_SECURITY_WPA3_ENTERPRISE.Enum()
	badEnterprisePmf.Pmf = wlanv1.PmfMode_PMF_MODE_OPTIONAL.Enum()
	errsOn(t, badEnterprisePmf.Build(), "wlan_config.pmf_matches_security")

	badEnterprise192Pmf := validWlanConfig()
	badEnterprise192Pmf.Security = wlanv1.WlanSecurity_WLAN_SECURITY_WPA3_ENTERPRISE192.Enum()
	badEnterprise192Pmf.Pmf = wlanv1.PmfMode_PMF_MODE_OPTIONAL.Enum()
	errsOn(t, badEnterprise192Pmf.Build(), "wlan_config.pmf_matches_security")

	goodPersonal := validWlanConfig()
	goodPersonal.Security = wlanv1.WlanSecurity_WLAN_SECURITY_WPA3_PERSONAL.Enum()
	goodPersonal.Pmf = wlanv1.PmfMode_PMF_MODE_REQUIRED.Enum()

	goodTransition := validWlanConfig()
	goodTransition.Security = wlanv1.WlanSecurity_WLAN_SECURITY_WPA3_PERSONAL_TRANSITION.Enum()
	goodTransition.Pmf = wlanv1.PmfMode_PMF_MODE_OPTIONAL.Enum()

	goodEnterpriseTransition := validWlanConfig()
	goodEnterpriseTransition.Security = wlanv1.WlanSecurity_WLAN_SECURITY_WPA3_ENTERPRISE_TRANSITION.Enum()
	goodEnterpriseTransition.Pmf = wlanv1.PmfMode_PMF_MODE_OPTIONAL.Enum()

	pmfAbsent := validWlanConfig()
	pmfAbsent.Security = wlanv1.WlanSecurity_WLAN_SECURITY_WPA3_PERSONAL.Enum()
	pmfAbsent.Pmf = nil

	pmfZero := validWlanConfig()
	pmfZero.Pmf = wlanv1.PmfMode_PMF_MODE_UNSPECIFIED.Enum()

	pmfUndeclared := validWlanConfig()
	pmfUndeclared.Pmf = wlanv1.PmfMode(99).Enum()

	runValidationCases(t, []validationCase{
		{name: "WPA3_PERSONAL with REQUIRED passes", message: goodPersonal.Build(), wantValid: true},
		{name: "WPA3_PERSONAL_TRANSITION with OPTIONAL passes", message: goodTransition.Build(), wantValid: true},
		{name: "WPA3_ENTERPRISE_TRANSITION with OPTIONAL passes", message: goodEnterpriseTransition.Build(), wantValid: true},
		{name: "WPA3_PERSONAL with PMF absent passes", message: pmfAbsent.Build(), wantValid: true},
		{name: "PMF of zero fails", message: pmfZero.Build()},
		{name: "PMF of 99 fails", message: pmfUndeclared.Build()},
	})
}

func TestWlanConfigRules(t *testing.T) {
	valid := validWlanConfig()

	noSsid := validWlanConfig()
	noSsid.Ssid = nil

	emptySsid := validWlanConfig()
	emptySsid.Ssid = []byte{}

	unspecifiedSecurity := validWlanConfig()
	unspecifiedSecurity.Security = wlanv1.WlanSecurity_WLAN_SECURITY_UNSPECIFIED.Enum()

	duplicateBand := validWlanConfig()
	duplicateBand.Bands = []wlanv1.WifiBand{
		wlanv1.WifiBand_WIFI_BAND_GHZ2P4,
		wlanv1.WifiBand_WIFI_BAND_GHZ2P4,
	}

	runValidationCases(t, []validationCase{
		{name: "valid config passes", message: valid.Build(), wantValid: true},
		{name: "config without SSID fails", message: noSsid.Build()},
		{name: "config with empty SSID fails", message: emptySsid.Build()},
		{name: "config with unspecified security fails", message: unspecifiedSecurity.Build()},
		{name: "config with duplicate band fails", message: duplicateBand.Build()},
	})
}

func TestWlanStateStatusBroadcastRules(t *testing.T) {
	broadcastingNoBroadcasts := validWlanState()
	broadcastingNoBroadcasts.Status = wirelessv1.WlanStatus_WLAN_STATUS_BROADCASTING.Enum()
	broadcastingNoBroadcasts.Broadcasts = nil
	errsOn(t, broadcastingNoBroadcasts.Build(), "wlan_state.status_matches_broadcasts")

	notBroadcastingWithBroadcast := validWlanState()
	notBroadcastingWithBroadcast.Status = wirelessv1.WlanStatus_WLAN_STATUS_NOT_BROADCASTING.Enum()
	notBroadcastingWithBroadcast.Broadcasts = []*wirelessv1.WlanBroadcast{validWlanBroadcast()}
	errsOn(t, notBroadcastingWithBroadcast.Build(), "wlan_state.status_matches_broadcasts")

	missingWithBroadcast := validWlanState()
	missingWithBroadcast.Status = wirelessv1.WlanStatus_WLAN_STATUS_MISSING.Enum()
	missingWithBroadcast.Broadcasts = []*wirelessv1.WlanBroadcast{validWlanBroadcast()}
	errsOn(t, missingWithBroadcast.Build(), "wlan_state.status_matches_broadcasts")

	broadcastingWithBroadcast := validWlanState()
	broadcastingWithBroadcast.Status = wirelessv1.WlanStatus_WLAN_STATUS_BROADCASTING.Enum()
	broadcastingWithBroadcast.Broadcasts = []*wirelessv1.WlanBroadcast{validWlanBroadcast()}

	notBroadcastingEmpty := validWlanState()
	notBroadcastingEmpty.Status = wirelessv1.WlanStatus_WLAN_STATUS_NOT_BROADCASTING.Enum()
	notBroadcastingEmpty.Broadcasts = nil

	missingEmpty := validWlanState()
	missingEmpty.Status = wirelessv1.WlanStatus_WLAN_STATUS_MISSING.Enum()
	missingEmpty.Broadcasts = nil

	runValidationCases(t, []validationCase{
		{name: "BROADCASTING with one broadcast passes", message: broadcastingWithBroadcast.Build(), wantValid: true},
		{name: "NOT_BROADCASTING with no broadcasts passes", message: notBroadcastingEmpty.Build(), wantValid: true},
		{name: "MISSING with no broadcasts passes", message: missingEmpty.Build(), wantValid: true},
	})
}

func TestWlanEventRules(t *testing.T) {
	sameStatus := wirelessv1.WlanEvent_builder{
		Ref:  validWlanRef(),
		From: wirelessv1.WlanStatus_WLAN_STATUS_NOT_BROADCASTING.Enum(),
		To:   wirelessv1.WlanStatus_WLAN_STATUS_NOT_BROADCASTING.Enum(),
	}
	errsOn(t, sameStatus.Build(), "wlan_event.status_changes")

	toAbsent := wirelessv1.WlanEvent_builder{
		Ref:  validWlanRef(),
		From: wirelessv1.WlanStatus_WLAN_STATUS_NOT_BROADCASTING.Enum(),
	}

	validTransition := wirelessv1.WlanEvent_builder{
		Ref:  validWlanRef(),
		From: wirelessv1.WlanStatus_WLAN_STATUS_NOT_BROADCASTING.Enum(),
		To:   wirelessv1.WlanStatus_WLAN_STATUS_BROADCASTING.Enum(),
	}

	appearing := wirelessv1.WlanEvent_builder{
		Ref: validWlanRef(),
		To:  wirelessv1.WlanStatus_WLAN_STATUS_BROADCASTING.Enum(),
	}

	runValidationCases(t, []validationCase{
		{name: "event with to absent fails", message: toAbsent.Build()},
		{name: "event with valid transition passes", message: validTransition.Build(), wantValid: true},
		{name: "event for appearing WLAN passes", message: appearing.Build(), wantValid: true},
	})
}

func TestWlanBroadcastRules(t *testing.T) {
	valid := validWlanBroadcast()

	noBssid := wirelessv1.WlanBroadcast_builder{
		Radio: componentRef(topologyDevA, "wifi0"),
	}

	noRadio := wirelessv1.WlanBroadcast_builder{
		Bssid: validWlanMac(),
	}

	runValidationCases(t, []validationCase{
		{name: "valid broadcast passes", message: valid, wantValid: true},
		{name: "broadcast without bssid fails", message: noBssid.Build()},
		{name: "broadcast without radio fails", message: noRadio.Build()},
	})
}
