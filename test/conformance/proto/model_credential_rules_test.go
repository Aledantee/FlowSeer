package conformance

import (
	"testing"

	"google.golang.org/protobuf/proto"

	credentialv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/credential/v1"
)

func TestCredentialMaterialRules(t *testing.T) {
	shortAuth := credentialv1.SnmpV3Credential_builder{
		User:           proto.String("flowseer-ro"),
		AuthProtocol:   credentialv1.SnmpAuthProtocol_SNMP_AUTH_PROTOCOL_SHA256.Enum(),
		AuthPassphrase: proto.String("short"),
		PrivProtocol:   credentialv1.SnmpPrivProtocol_SNMP_PRIV_PROTOCOL_AES128.Enum(),
		PrivPassphrase: proto.String("priv-passphrase"),
	}.Build()

	noPriv := credentialv1.SnmpV3Credential_builder{
		User:           proto.String("flowseer-ro"),
		AuthProtocol:   credentialv1.SnmpAuthProtocol_SNMP_AUTH_PROTOCOL_SHA256.Enum(),
		AuthPassphrase: proto.String("auth-passphrase"),
		PrivPassphrase: proto.String("priv-passphrase"),
	}.Build()

	runValidationCases(t, []validationCase{
		{name: "snmpv3 material is valid", message: snmpMaterial(), wantValid: true},
		{name: "shell material is valid", message: shellMaterial(), wantValid: true},
		{name: "material without an arm is rejected", message: credentialv1.CredentialMaterial_builder{}.Build()},
		{name: "an authentication passphrase under 8 characters is rejected", message: shortAuth},
		{name: "snmpv3 without a privacy protocol is rejected", message: noPriv},
		{
			name: "shell login with neither password nor key is rejected",
			message: credentialv1.ShellCredential_builder{
				Username: proto.String("flowseer"),
			}.Build(),
		},
		{
			name: "shell login with a private key alone is valid",
			message: credentialv1.ShellCredential_builder{
				Username:   proto.String("flowseer"),
				PrivateKey: []byte("-----BEGIN OPENSSH PRIVATE KEY-----\n"),
			}.Build(),
			wantValid: true,
		},
		{
			name: "a key passphrase without a key is rejected",
			message: credentialv1.ShellCredential_builder{
				Username:             proto.String("flowseer"),
				Password:             proto.String("shell-password"),
				PrivateKeyPassphrase: proto.String("secret"),
			}.Build(),
		},
		{
			name: "shell login with an enable password is valid",
			message: credentialv1.ShellCredential_builder{
				Username:       proto.String("flowseer"),
				Password:       proto.String("shell-password"),
				EnablePassword: proto.String("enable"),
			}.Build(),
			wantValid: true,
		},
	})
}
