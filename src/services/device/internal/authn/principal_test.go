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
		Tenants:  []string{"tenant-1", "tenant-2"},
		Platform: true,
	}
	ctx = authn.NewContext(ctx, p)

	got, ok := authn.FromContext(ctx)
	if !ok {
		t.Fatal("FromContext returned ok=false, want true")
	}
	if got.ID != p.ID || !slices.Equal(got.Tenants, p.Tenants) || got.Platform != p.Platform {
		t.Fatalf("got %+v, want %+v", got, p)
	}
}
