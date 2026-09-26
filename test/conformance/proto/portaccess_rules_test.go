package conformance

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	portaccessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/portaccess/v1"
)

func validPortAccessMac() *addrv1.MacAddress {
	return addrv1.MacAddress_builder{
		Eui48: addrv1.Eui48Address_builder{
			Octets: []byte{0x00, 0x50, 0x56, 0xaa, 0xbb, 0xcc},
		}.Build(),
	}.Build()
}

func validSession() portaccessv1.Session_builder {
	return portaccessv1.Session_builder{
		InterfaceName:  proto.String("ethernet1/1"),
		Mac:            validPortAccessMac(),
		Method:         portaccessv1.PortAccessMethod_PORT_ACCESS_METHOD_DOT1X.Enum(),
		AuthState:      portaccessv1.PortAccessAuthState_PORT_ACCESS_AUTH_STATE_AUTHORIZED.Enum(),
		AssignedVlanId: proto.Uint32(20),
		RoleName:       proto.String("employee"),
		UserName:       proto.String("alice@example.com"),
	}
}

func TestSessionRequiredKeyFields(t *testing.T) {
	noInterface := validSession()
	noInterface.InterfaceName = nil

	noMac := validSession()
	noMac.Mac = nil

	emptyInterface := validSession()
	emptyInterface.InterfaceName = proto.String("")

	toolongInterface := validSession()
	toolongInterface.InterfaceName = proto.String(strings.Repeat("e", 256))

	runValidationCases(t, []validationCase{
		{name: "valid session passes", message: validSession().Build(), wantValid: true},
		{name: "missing interface_name fails", message: noInterface.Build(), wantValid: false},
		{name: "missing mac fails", message: noMac.Build(), wantValid: false},
		{name: "empty interface_name fails", message: emptyInterface.Build(), wantValid: false},
		{name: "interface_name 256 chars fails", message: toolongInterface.Build(), wantValid: false},
	})
}

func TestSessionVlanAndOptionalFields(t *testing.T) {
	vlan1 := validSession()
	vlan1.AssignedVlanId = proto.Uint32(1)

	vlan4094 := validSession()
	vlan4094.AssignedVlanId = proto.Uint32(4094)

	vlan0 := validSession()
	vlan0.AssignedVlanId = proto.Uint32(0)

	vlan4095 := validSession()
	vlan4095.AssignedVlanId = proto.Uint32(4095)

	role256 := validSession()
	role256.RoleName = proto.String(strings.Repeat("r", 256))

	role257 := validSession()
	role257.RoleName = proto.String(strings.Repeat("r", 257))

	user256 := validSession()
	user256.UserName = proto.String(strings.Repeat("u", 256))

	user257 := validSession()
	user257.UserName = proto.String(strings.Repeat("u", 257))

	runValidationCases(t, []validationCase{
		{name: "assigned_vlan_id 1 passes", message: vlan1.Build(), wantValid: true},
		{name: "assigned_vlan_id 4094 passes", message: vlan4094.Build(), wantValid: true},
		{name: "assigned_vlan_id 0 fails", message: vlan0.Build(), wantValid: false},
		{name: "assigned_vlan_id 4095 fails", message: vlan4095.Build(), wantValid: false},
		{name: "role_name 256 chars passes", message: role256.Build(), wantValid: true},
		{name: "role_name 257 chars fails", message: role257.Build(), wantValid: false},
		{name: "user_name 256 chars passes", message: user256.Build(), wantValid: true},
		{name: "user_name 257 chars fails", message: user257.Build(), wantValid: false},
	})
}

func TestSessionEnumValidation(t *testing.T) {
	badMethod := validSession()
	badMethod.Method = portaccessv1.PortAccessMethod(99).Enum()

	badAuthState := validSession()
	badAuthState.AuthState = portaccessv1.PortAccessAuthState(99).Enum()

	unspecifiedMethod := validSession()
	unspecifiedMethod.Method = portaccessv1.PortAccessMethod_PORT_ACCESS_METHOD_UNSPECIFIED.Enum()

	unspecifiedAuthState := validSession()
	unspecifiedAuthState.AuthState = portaccessv1.PortAccessAuthState_PORT_ACCESS_AUTH_STATE_UNSPECIFIED.Enum()

	mabMethod := validSession()
	mabMethod.Method = portaccessv1.PortAccessMethod_PORT_ACCESS_METHOD_MAB.Enum()

	webAuthMethod := validSession()
	webAuthMethod.Method = portaccessv1.PortAccessMethod_PORT_ACCESS_METHOD_WEB_AUTH.Enum()

	unauthAuthState := validSession()
	unauthAuthState.AuthState = portaccessv1.PortAccessAuthState_PORT_ACCESS_AUTH_STATE_UNAUTHORIZED.Enum()

	runValidationCases(t, []validationCase{
		{name: "undefined method fails defined_only", message: badMethod.Build(), wantValid: false},
		{name: "undefined auth_state fails defined_only", message: badAuthState.Build(), wantValid: false},
		{name: "unspecified method passes", message: unspecifiedMethod.Build(), wantValid: true},
		{name: "unspecified auth_state passes", message: unspecifiedAuthState.Build(), wantValid: true},
		{name: "MAB method passes", message: mabMethod.Build(), wantValid: true},
		{name: "web auth method passes", message: webAuthMethod.Build(), wantValid: true},
		{name: "unauthorized auth_state passes", message: unauthAuthState.Build(), wantValid: true},
	})
}
