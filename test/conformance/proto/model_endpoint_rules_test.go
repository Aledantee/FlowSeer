package conformance

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	endpointv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/endpoint/v1"
	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	endpointnetv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/endpoint/v1"
	wlanv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/wlan/v1"
)

const validEndpointID = "11111111-2222-3333-4444-555555555555"

func validModelEndpointRef() *endpointv1.EndpointGlobalRef {
	return endpointv1.EndpointGlobalRef_builder{
		Endpoint: endpointv1.EndpointLocalRef_builder{
			Id: proto.String(validEndpointID),
		}.Build(),
	}.Build()
}

func validModelEndpointMac() *addrv1.MacAddress {
	return addrv1.MacAddress_builder{
		Eui48: addrv1.Eui48Address_builder{
			Octets: []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		}.Build(),
	}.Build()
}

func validModelEndpointMac2() *addrv1.MacAddress {
	return addrv1.MacAddress_builder{
		Eui48: addrv1.Eui48Address_builder{
			Octets: []byte{0x02, 0x11, 0x22, 0x33, 0x44, 0x55},
		}.Build(),
	}.Build()
}

func validModelWiredAttachment() *endpointnetv1.WiredAttachment {
	return endpointnetv1.WiredAttachment_builder{
		SwitchName:    proto.String("sw-access-01"),
		InterfaceName: proto.String("GigabitEthernet1/0/1"),
		VlanId:        proto.Uint32(10),
	}.Build()
}

func validModelWirelessAttachment() *endpointnetv1.WirelessAttachment {
	return endpointnetv1.WirelessAttachment_builder{
		ApName:            proto.String("ap-hallway-02"),
		Bssid:             validModelEndpointMac(),
		Ssid:              []byte("CorpNet"),
		Band:              wlanv1.WifiBand_WIFI_BAND_GHZ5.Enum(),
		Channel:           proto.Uint32(40),
		RssiMillidbm:      proto.Int32(-60000),
		SnrMillidb:        proto.Int32(35000),
		NegotiatedRateBps: proto.Uint64(600000000),
		Mcs:               proto.Uint32(7),
		Nss:               proto.Uint32(2),
		GuardInterval:     durationpb.New(800 * time.Nanosecond),
	}.Build()
}

func validEndpointState() endpointv1.EndpointState_builder {
	return endpointv1.EndpointState_builder{
		Ref:                  validModelEndpointRef(),
		Lifecycle:            endpointv1.EndpointLifecycle_ENDPOINT_LIFECYCLE_ACTIVE.Enum(),
		ObservedMacAddresses: []*addrv1.MacAddress{validModelEndpointMac()},
		Wired:                validModelWiredAttachment(),
	}
}

func TestEndpointStateAttachmentOneofRules(t *testing.T) {
	// EndpointState with no attachment arm set fails validation.
	noAttachment := validEndpointState()
	noAttachment.Wired = nil
	noAttachment.Wireless = nil

	// EndpointState with only wired set passes.
	wiredOnly := validEndpointState()
	wiredOnly.Wired = validModelWiredAttachment()
	wiredOnly.Wireless = nil

	// EndpointState with only wireless set passes.
	wirelessOnly := validEndpointState()
	wirelessOnly.Wired = nil
	wirelessOnly.Wireless = validModelWirelessAttachment()

	runValidationCases(t, []validationCase{
		{name: "no attachment arm fails oneof required", message: noAttachment.Build(), wantValid: false},
		{name: "only wired attachment passes", message: wiredOnly.Build(), wantValid: true},
		{name: "only wireless attachment passes", message: wirelessOnly.Build(), wantValid: true},
	})
}

func TestEndpointStateAddressAndMacRules(t *testing.T) {
	// Empty observed MACs fails validation.
	emptyMacs := validEndpointState()
	emptyMacs.ObservedMacAddresses = nil

	// Multiple observed MACs pass validation.
	multiMacs := validEndpointState()
	multiMacs.ObservedMacAddresses = []*addrv1.MacAddress{
		validModelEndpointMac(),
		validModelEndpointMac2(),
	}

	// IPv4 address populated passes.
	withIpv4 := validEndpointState()
	withIpv4.Ipv4Address = addrv1.Ipv4Address_builder{
		Octets: []byte{192, 0, 2, 42},
	}.Build()

	// Multiple IPv6 addresses pass.
	withIpv6 := validEndpointState()
	withIpv6.Ipv6Addresses = []*addrv1.Ipv6Address{
		addrv1.Ipv6Address_builder{Octets: []byte{0xfe, 0x80, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}}.Build(),
		addrv1.Ipv6Address_builder{Octets: []byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}}.Build(),
	}

	// Hostname bounds.
	host255 := validEndpointState()
	host255.Hostname = proto.String(strings.Repeat("h", 255))

	host256 := validEndpointState()
	host256.Hostname = proto.String(strings.Repeat("h", 256))

	// Unspecified lifecycle fails.
	unspecLifecycle := validEndpointState()
	unspecLifecycle.Lifecycle = endpointv1.EndpointLifecycle_ENDPOINT_LIFECYCLE_UNSPECIFIED.Enum()

	runValidationCases(t, []validationCase{
		{name: "empty observed macs fails min_items", message: emptyMacs.Build(), wantValid: false},
		{name: "multiple observed macs pass", message: multiMacs.Build(), wantValid: true},
		{name: "ipv4 address passes", message: withIpv4.Build(), wantValid: true},
		{name: "multiple ipv6 addresses pass", message: withIpv6.Build(), wantValid: true},
		{name: "hostname 255 chars passes", message: host255.Build(), wantValid: true},
		{name: "hostname 256 chars fails", message: host256.Build(), wantValid: false},
		{name: "unspecified lifecycle fails not_in 0", message: unspecLifecycle.Build(), wantValid: false},
	})
}

func TestEndpointEventTransitionRules(t *testing.T) {
	// Lifecycle transition with from == to fails CEL rule.
	sameState := endpointv1.EndpointLifecycleTransition_builder{
		From: endpointv1.EndpointLifecycle_ENDPOINT_LIFECYCLE_ACTIVE.Enum(),
		To:   endpointv1.EndpointLifecycle_ENDPOINT_LIFECYCLE_ACTIVE.Enum(),
	}.Build()
	errsOn(t, sameState, "endpoint_lifecycle_transition.changes")

	// Lifecycle transition with from != to passes.
	validTransition := endpointv1.EndpointLifecycleTransition_builder{
		From: endpointv1.EndpointLifecycle_ENDPOINT_LIFECYCLE_ACTIVE.Enum(),
		To:   endpointv1.EndpointLifecycle_ENDPOINT_LIFECYCLE_STALE.Enum(),
	}.Build()

	// Initial appearance (from = unset) passes.
	initialAppearance := endpointv1.EndpointLifecycleTransition_builder{
		To: endpointv1.EndpointLifecycle_ENDPOINT_LIFECYCLE_ACTIVE.Enum(),
	}.Build()

	// To state unspecified fails.
	toUnspec := endpointv1.EndpointLifecycleTransition_builder{
		From: endpointv1.EndpointLifecycle_ENDPOINT_LIFECYCLE_ACTIVE.Enum(),
		To:   endpointv1.EndpointLifecycle_ENDPOINT_LIFECYCLE_UNSPECIFIED.Enum(),
	}.Build()

	// Roam without to_attachment fails.
	badRoam := endpointv1.EndpointRoam_builder{
		FromAttachment: validModelWirelessAttachment(),
		Reason:         endpointnetv1.RoamReason_ROAM_REASON_FAST_BSS_TRANSITION.Enum(),
	}.Build()

	// Roam with to_attachment passes.
	goodRoam := endpointv1.EndpointRoam_builder{
		FromAttachment: validModelWirelessAttachment(),
		ToAttachment:   validModelWirelessAttachment(),
		Reason:         endpointnetv1.RoamReason_ROAM_REASON_FAST_BSS_TRANSITION.Enum(),
	}.Build()

	// Roam with undefined reason fails defined_only.
	badRoamReason := endpointv1.EndpointRoam_builder{
		ToAttachment: validModelWirelessAttachment(),
		Reason:       endpointnetv1.RoamReason(99).Enum(),
	}.Build()

	// Connection failure without stage (stage = UNSPECIFIED / 0) fails.
	badFailure := endpointnetv1.ConnectionFailureStage_CONNECTION_FAILURE_STAGE_UNSPECIFIED
	badConnFailure := endpointv1.EndpointConnectionFailure_builder{
		Stage:  &badFailure,
		Detail: proto.String("auth timeout"),
	}.Build()

	// Connection failure with stage passes.
	goodFailure := endpointnetv1.ConnectionFailureStage_CONNECTION_FAILURE_STAGE_AUTHENTICATION
	goodConnFailure := endpointv1.EndpointConnectionFailure_builder{
		Stage:    &goodFailure,
		Detail:   proto.String("EAP failure"),
		Wireless: validModelWirelessAttachment(),
	}.Build()

	// EndpointEvent wrappers.
	eventLifecycle := endpointv1.EndpointEvent_builder{
		Ref:       validModelEndpointRef(),
		Lifecycle: validTransition,
	}.Build()

	eventRoam := endpointv1.EndpointEvent_builder{
		Ref:  validModelEndpointRef(),
		Roam: goodRoam,
	}.Build()

	eventFailure := endpointv1.EndpointEvent_builder{
		Ref:               validModelEndpointRef(),
		ConnectionFailure: goodConnFailure,
	}.Build()

	eventNoRef := endpointv1.EndpointEvent_builder{
		Lifecycle: validTransition,
	}.Build()

	eventNoArm := endpointv1.EndpointEvent_builder{
		Ref: validModelEndpointRef(),
	}.Build()

	runValidationCases(t, []validationCase{
		{name: "different lifecycle transition passes", message: validTransition, wantValid: true},
		{name: "initial appearance passes", message: initialAppearance, wantValid: true},
		{name: "to unspecified fails not_in 0", message: toUnspec, wantValid: false},
		{name: "roam without to_attachment fails", message: badRoam, wantValid: false},
		{name: "roam with to_attachment passes", message: goodRoam, wantValid: true},
		{name: "roam with undefined reason fails", message: badRoamReason, wantValid: false},
		{name: "connection failure without stage fails", message: badConnFailure, wantValid: false},
		{name: "connection failure with stage passes", message: goodConnFailure, wantValid: true},
		{name: "event lifecycle arm passes", message: eventLifecycle, wantValid: true},
		{name: "event roam arm passes", message: eventRoam, wantValid: true},
		{name: "event connection failure arm passes", message: eventFailure, wantValid: true},
		{name: "event without ref fails", message: eventNoRef, wantValid: false},
		{name: "event without arm fails", message: eventNoArm, wantValid: false},
	})
}
