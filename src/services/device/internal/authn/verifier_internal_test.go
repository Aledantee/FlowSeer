package authn

import (
	"testing"
	"time"
)

func TestDefaultClientTimeout(t *testing.T) {
	v, err := NewVerifier(Options{
		Issuers: []IssuerConfig{{Issuer: "https://issuer.example.com", Audience: "flowseer-device"}},
	})
	if err != nil {
		t.Fatalf("NewVerifier default: %v", err)
	}
	if v.client.Timeout != 10*time.Second {
		t.Fatalf("got default client timeout %v, want 10s", v.client.Timeout)
	}
}
