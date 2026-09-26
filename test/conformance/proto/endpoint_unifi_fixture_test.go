package conformance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"testing"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"

	endpointv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/endpoint/v1"
	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	endpointnetv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/endpoint/v1"
)

// sampleUniFiWirelessClientJSON holds a JSON payload conforming to the
// "Wireless client details" schema in UniFi Network OpenAPI v10.4.57
// (spec/openapi/ubiquiti/unifi-network-openapi-v10.4.57.json:28283-28363).
const sampleUniFiWirelessClientJSON = `{
  "id": "11111111-2222-3333-4444-555555555555",
  "name": "Alice-Laptop",
  "type": "WIRELESS",
  "macAddress": "00:11:22:33:44:55",
  "ipAddress": "192.0.2.42",
  "uplinkDeviceId": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
  "connectedAt": "2026-09-26T10:00:00Z",
  "access": {
    "type": "DEFAULT"
  }
}`

type unifiClientPayload struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Type           string `json:"type"`
	MacAddress     string `json:"macAddress"`
	IPAddress      string `json:"ipAddress"`
	ConnectedAt    string `json:"connectedAt"`
	UplinkDeviceID string `json:"uplinkDeviceId"`
	BSSID          string `json:"bssid,omitempty"`
	Access         struct {
		Type string `json:"type"`
	} `json:"access"`
}

func endpointStateFromUniFi(in []byte) (*endpointv1.EndpointState, error) {
	var payload unifiClientPayload
	if err := json.Unmarshal(in, &payload); err != nil {
		return nil, fmt.Errorf("unmarshaling unifi client json: %w", err)
	}

	if payload.ID == "" {
		return nil, fmt.Errorf("missing client id")
	}

	hwAddr, err := net.ParseMAC(payload.MacAddress)
	if err != nil {
		return nil, fmt.Errorf("parsing mac address %q: %w", payload.MacAddress, err)
	}
	if len(hwAddr) != 6 {
		return nil, fmt.Errorf("expected 6-byte MAC address, got %d bytes", len(hwAddr))
	}

	mac := addrv1.MacAddress_builder{
		Eui48: addrv1.Eui48Address_builder{
			Octets: hwAddr,
		}.Build(),
	}.Build()

	b := endpointv1.EndpointState_builder{
		Ref: endpointv1.EndpointGlobalRef_builder{
			Endpoint: endpointv1.EndpointLocalRef_builder{
				Id: proto.String(payload.ID),
			}.Build(),
		}.Build(),
		Lifecycle:            endpointv1.EndpointLifecycle_ENDPOINT_LIFECYCLE_ACTIVE.Enum(),
		ObservedMacAddresses: []*addrv1.MacAddress{mac},
	}

	if payload.Name != "" {
		b.Hostname = proto.String(payload.Name)
	}

	if payload.IPAddress != "" {
		ip := net.ParseIP(payload.IPAddress)
		if ip4 := ip.To4(); ip4 != nil {
			b.Ipv4Address = addrv1.Ipv4Address_builder{
				Octets: ip4,
			}.Build()
		}
	}

	switch payload.Type {
	case "WIRELESS":
		var bssidMac *addrv1.MacAddress
		if payload.BSSID != "" {
			if bssidHw, err := net.ParseMAC(payload.BSSID); err == nil && len(bssidHw) == 6 {
				bssidMac = addrv1.MacAddress_builder{
					Eui48: addrv1.Eui48Address_builder{
						Octets: bssidHw,
					}.Build(),
				}.Build()
			}
		}
		if bssidMac == nil {
			// FlowSeer requires a BSSID on WirelessAttachment. When the UniFi client
			// record schema does not supply a BSSID, synthesize a locally-administered BSSID.
			bssidMac = addrv1.MacAddress_builder{
				Eui48: addrv1.Eui48Address_builder{
					Octets: []byte{0x02, 0x11, 0x22, 0x33, 0x44, 0x55},
				}.Build(),
			}.Build()
		}

		b.Wireless = endpointnetv1.WirelessAttachment_builder{
			ApName: proto.String(payload.UplinkDeviceID),
			Bssid:  bssidMac,
		}.Build()
	case "WIRED":
		b.Wired = endpointnetv1.WiredAttachment_builder{
			SwitchName: proto.String(payload.UplinkDeviceID),
		}.Build()
	default:
		return nil, fmt.Errorf("unsupported client type: %q", payload.Type)
	}

	return b.Build(), nil
}

func TestUniFiWirelessClientFixture(t *testing.T) {
	var vendor unifiClientPayload
	if err := json.Unmarshal([]byte(sampleUniFiWirelessClientJSON), &vendor); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	// Assert unmarshaled vendor fields match schema.
	if got, want := vendor.ID, "11111111-2222-3333-4444-555555555555"; got != want {
		t.Errorf("vendor id = %q, want %q", got, want)
	}
	if got, want := vendor.Name, "Alice-Laptop"; got != want {
		t.Errorf("vendor name = %q, want %q", got, want)
	}
	if got, want := vendor.Type, "WIRELESS"; got != want {
		t.Errorf("vendor type = %q, want %q", got, want)
	}
	if got, want := vendor.MacAddress, "00:11:22:33:44:55"; got != want {
		t.Errorf("vendor macAddress = %q, want %q", got, want)
	}
	if got, want := vendor.IPAddress, "192.0.2.42"; got != want {
		t.Errorf("vendor ipAddress = %q, want %q", got, want)
	}
	if got, want := vendor.UplinkDeviceID, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"; got != want {
		t.Errorf("vendor uplinkDeviceId = %q, want %q", got, want)
	}

	state, err := endpointStateFromUniFi([]byte(sampleUniFiWirelessClientJSON))
	if err != nil {
		t.Fatalf("endpointStateFromUniFi failed: %v", err)
	}

	// Assert converted EndpointState fields.
	if got, want := state.GetRef().GetEndpoint().GetId(), "11111111-2222-3333-4444-555555555555"; got != want {
		t.Errorf("state ref.endpoint.id = %q, want %q", got, want)
	}
	if got, want := state.GetHostname(), "Alice-Laptop"; got != want {
		t.Errorf("state hostname = %q, want %q", got, want)
	}
	if got, want := len(state.GetObservedMacAddresses()), 1; got != want {
		t.Fatalf("state observed_mac_addresses count = %d, want %d", len(state.GetObservedMacAddresses()), want)
	}
	wantMac := []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	if got := state.GetObservedMacAddresses()[0].GetEui48().GetOctets(); !bytes.Equal(got, wantMac) {
		t.Errorf("state observed_mac_addresses[0] = %x, want %x", got, wantMac)
	}
	wantIP := []byte{192, 0, 2, 42}
	if got := state.GetIpv4Address().GetOctets(); !bytes.Equal(got, wantIP) {
		t.Errorf("state ipv4_address = %v, want %v", got, wantIP)
	}
	if got, want := state.GetWireless().GetApName(), "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"; got != want {
		t.Errorf("state wireless.ap_name = %q, want %q", got, want)
	}
	if state.GetWired() != nil {
		t.Errorf("state wired attachment is populated, want nil")
	}

	// Validate against protovalidate rules.
	if err := protovalidate.Validate(state); err != nil {
		t.Errorf("converted EndpointState failed validation: %v", err)
	}
}

func TestUniFiWirelessClientFixtureErrors(t *testing.T) {
	// Missing ID
	missingID := `{"name":"test","type":"WIRELESS","macAddress":"00:11:22:33:44:55"}`
	if _, err := endpointStateFromUniFi([]byte(missingID)); err == nil {
		t.Errorf("expected error for missing client id, got nil")
	}

	// Invalid MAC
	invalidMAC := `{"id":"11111111-2222-3333-4444-555555555555","type":"WIRELESS","macAddress":"invalid"}`
	if _, err := endpointStateFromUniFi([]byte(invalidMAC)); err == nil {
		t.Errorf("expected error for invalid MAC address, got nil")
	}

	// Unsupported type
	unsupportedType := `{"id":"11111111-2222-3333-4444-555555555555","type":"VPN","macAddress":"00:11:22:33:44:55"}`
	if _, err := endpointStateFromUniFi([]byte(unsupportedType)); err == nil {
		t.Errorf("expected error for unsupported type, got nil")
	}
}
