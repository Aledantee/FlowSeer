package netconf_test

import (
	"fmt"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/secret"
	"go.aledante.io/FlowSeer/src/protocol/netconf"
)

// TestOptions_NoCredentialLeak asserts that formatting Options, which a
// dial error or a debug log routinely does, exposes neither the password
// nor the private key.
func TestOptions_NoCredentialLeak(t *testing.T) {
	const pass, key = "hunter2-password", "-----BEGIN OPENSSH PRIVATE KEY-----"
	opts := netconf.Options{
		Username:      "admin",
		Password:      secret.NewString(pass),
		PrivateKeyPEM: secret.NewString(key),
		HostKeySHA256: "AAAA",
	}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		rendered := fmt.Sprintf(verb, opts)
		if strings.Contains(rendered, pass) || strings.Contains(rendered, key) {
			t.Errorf("verb %s leaked a credential: %s", verb, rendered)
		}
		if !strings.Contains(rendered, secret.Redacted) {
			t.Errorf("verb %s is not redacted: %s", verb, rendered)
		}
		if !strings.Contains(rendered, "AAAA") {
			t.Errorf("verb %s dropped the host-key pin, which is public: %s", verb, rendered)
		}
	}
}
