package authn_test

import (
	"context"
	"slices"
	"testing"

	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
)

func TestPrincipalContextRoundTrip(t *testing.T) {
	ctx := context.Background()
	if _, ok := authn.FromContext(ctx); ok {
		t.Fatal("FromContext on empty context returned ok=true, want false")
	}

	p := authn.Principal{
		ID:       "user-42",
		Issuer:   "https://issuer.example.com",
		Subject:  "u1",
		Tenants:  []string{"tenant-1", "tenant-2"},
		Platform: true,
	}
	ctx = authn.NewContext(ctx, p)

	got, ok := authn.FromContext(ctx)
	if !ok {
		t.Fatal("FromContext returned ok=false, want true")
	}
	if got.ID != p.ID || got.Issuer != p.Issuer || got.Subject != p.Subject || !slices.Equal(got.Tenants, p.Tenants) || got.Platform != p.Platform {
		t.Fatalf("got %+v, want %+v", got, p)
	}
}

func TestPrincipalIDDiffersForCollidingPrefixes(t *testing.T) {
	id1 := authn.ComputePrincipalID("https://a/b", "c")
	id2 := authn.ComputePrincipalID("https://a/", "bc")

	if id1 == id2 {
		t.Fatalf("ComputePrincipalID returned identical ID %q for distinct (issuer, subject) pairs", id1)
	}
	if len(id1) != 64 || len(id2) != 64 {
		t.Fatalf("expected 64 hex characters, got len(id1)=%d, len(id2)=%d", len(id1), len(id2))
	}
}
