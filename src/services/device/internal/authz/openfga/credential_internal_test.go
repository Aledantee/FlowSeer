package openfga

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/secret"
)

const credentialTestKey = "preshared-key-material"

func TestPresharedKeyCredentialSendsTheKeyAsABearerToken(t *testing.T) {
	cred := presharedKeyCredential{Key: secret.NewString(credentialTestKey)}

	md, err := cred.GetRequestMetadata(context.Background())
	if err != nil {
		t.Fatalf("GetRequestMetadata: %v", err)
	}
	if got, want := md["authorization"], "Bearer "+credentialTestKey; got != want {
		t.Errorf("authorization = %q, want %q", got, want)
	}
	if len(md) != 1 {
		t.Errorf("metadata = %v, want only authorization", md)
	}
}

// The bearer token must never travel in the clear, so gRPC has to refuse to
// attach it to a connection without transport security.
func TestPresharedKeyCredentialRequiresTransportSecurity(t *testing.T) {
	if !(presharedKeyCredential{Key: secret.NewString(credentialTestKey)}).RequireTransportSecurity() {
		t.Error("RequireTransportSecurity = false, want true")
	}
}

// A struct is printed field by field, and fmt consults a secret.Value's
// redaction only for an exported field.
func TestPresharedKeyCredentialPrintsWithoutTheKey(t *testing.T) {
	cred := presharedKeyCredential{Key: secret.NewString(credentialTestKey)}

	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		if got := fmt.Sprintf(verb, cred); strings.Contains(got, credentialTestKey) {
			t.Errorf("Sprintf(%q) = %s, want no key material", verb, got)
		}
		if got := fmt.Sprintf(verb, &cred); strings.Contains(got, credentialTestKey) {
			t.Errorf("Sprintf(%q) of a pointer = %s, want no key material", verb, got)
		}
	}
}
