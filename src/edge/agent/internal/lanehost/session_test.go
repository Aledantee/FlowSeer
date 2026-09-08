package lanehost_test

import (
	"context"
	"strings"
	"testing"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	credentialv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/credential/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/lanehost"
)

func credentialWith(material *credentialv1.CredentialMaterial) *edgev1.DeviceCredential {
	cred := &edgev1.DeviceCredential{}
	cred.SetTypedMaterial(material)
	return cred
}

func shellMaterial(username, password string) *credentialv1.CredentialMaterial {
	shell := &credentialv1.ShellCredential{}
	shell.SetUsername(username)
	shell.SetPassword(password)
	material := &credentialv1.CredentialMaterial{}
	material.SetShell(shell)
	return material
}

func snmpMaterial(auth credentialv1.SnmpAuthProtocol, priv credentialv1.SnmpPrivProtocol) *credentialv1.CredentialMaterial {
	usm := &credentialv1.SnmpV3Credential{}
	usm.SetUser("monitor")
	usm.SetAuthProtocol(auth)
	usm.SetAuthPassphrase("auth-passphrase")
	usm.SetPrivProtocol(priv)
	usm.SetPrivPassphrase("priv-passphrase")
	material := &credentialv1.CredentialMaterial{}
	material.SetSnmpV3(usm)
	return material
}

// TestAnEmptyHostKeyDigestIsRefused is the second place in this agent where
// an empty set has to mean "trust nothing" rather than "trust anything", and
// it is tested here rather than assumed from the TLS anchors having been
// careful about it.
//
// An unpinned SSH session to a network device is one where anything that can
// answer on the address receives the credential this call is about to send.
// The dial must not happen at all, which is why the assertion is on the error
// naming the digest rather than on the dial failing — a dial to a dead
// address fails too.
func TestAnEmptyHostKeyDigestIsRefused(t *testing.T) {
	open := lanehost.OpenShell(lanehost.Endpoint{Address: "192.0.2.1"})

	_, err := open(context.Background(), credentialWith(shellMaterial("admin", "secret")), "")
	if err == nil {
		t.Fatal("OpenShell() error = nil, want an unpinned session refused")
	}
	if code, _ := errs.CodeOf(err); code != lanehost.ErrCodeSession {
		t.Errorf("code = %v, want %v", code, lanehost.ErrCodeSession)
	}
	if !strings.Contains(err.Error(), "host key") {
		t.Errorf("error = %v, want it to name the missing host key rather than the dial", err)
	}
}

// TestShellMaterialIsRequired covers the credential central failed to
// populate. Falling through with an empty username would offer the device an
// anonymous login, which some answer.
func TestShellMaterialIsRequired(t *testing.T) {
	open := lanehost.OpenShell(lanehost.Endpoint{Address: "192.0.2.1"})

	_, err := open(context.Background(), credentialWith(&credentialv1.CredentialMaterial{}), "SHA256:abc")
	if err == nil {
		t.Fatal("OpenShell() error = nil, want a credential with no shell material refused")
	}
}

// TestAnUnspecifiedSNMPProtocolIsRefused pins the mapping's failure
// direction. The zero value of an enum is what an unset field looks like, and
// mapping it to noAuth would turn a credential central failed to populate
// into an unauthenticated read of a device — which succeeds against anything
// configured permissively and is invisible afterwards.
func TestAnUnspecifiedSNMPProtocolIsRefused(t *testing.T) {
	open := lanehost.OpenSNMP(lanehost.Endpoint{Address: "192.0.2.1"})

	for _, tc := range []struct {
		name     string
		material *credentialv1.CredentialMaterial
		// names is what the refusal must say. Asserting only that an error
		// came back does not test this mapping: USMConfig.Validate refuses
		// an auth passphrase with no auth protocol too, so a mapping that
		// silently returned noAuth would still produce an error, from
		// somewhere else, and the test would pass.
		names string
	}{
		{
			"no auth protocol", snmpMaterial(
				credentialv1.SnmpAuthProtocol_SNMP_AUTH_PROTOCOL_UNSPECIFIED,
				credentialv1.SnmpPrivProtocol_SNMP_PRIV_PROTOCOL_AES128),
			"no SNMPv3 authentication protocol",
		},
		{
			"no privacy protocol", snmpMaterial(
				credentialv1.SnmpAuthProtocol_SNMP_AUTH_PROTOCOL_SHA256,
				credentialv1.SnmpPrivProtocol_SNMP_PRIV_PROTOCOL_UNSPECIFIED),
			"below authPriv",
		},
		{
			"no snmp material at all", &credentialv1.CredentialMaterial{},
			"carries no SNMPv3 material",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := open(context.Background(), credentialWith(tc.material))
			if err == nil {
				t.Fatal("OpenSNMP() error = nil, want the incomplete credential refused")
			}
			if !strings.Contains(err.Error(), tc.names) {
				t.Fatalf("error = %v, want it to name %q: another check refusing this is not this check refusing it", err, tc.names)
			}
		})
	}
}

// TestACompleteSNMPCredentialIsAccepted is the partner that keeps the test
// above honest: without it, "refuses an unspecified protocol" is also
// satisfied by a factory that refuses everything.
//
// It reaches a real dial, which fails against a reserved address — that is
// expected and is not what is asserted. What is asserted is that the failure
// is the transport's rather than the credential mapping's.
func TestACompleteSNMPCredentialIsAccepted(t *testing.T) {
	open := lanehost.OpenSNMP(lanehost.Endpoint{Address: "192.0.2.1", SNMPPort: 1})
	material := snmpMaterial(
		credentialv1.SnmpAuthProtocol_SNMP_AUTH_PROTOCOL_SHA256,
		credentialv1.SnmpPrivProtocol_SNMP_PRIV_PROTOCOL_AES128)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // no packets leave this test

	_, err := open(ctx, credentialWith(material))
	if err == nil {
		return // a session against a canceled context is fine to have got that far
	}
	if strings.Contains(err.Error(), "SNMPv3") || strings.Contains(err.Error(), "usable") {
		t.Errorf("a complete credential was rejected by the mapping: %v", err)
	}
}
