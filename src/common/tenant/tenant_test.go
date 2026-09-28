package tenant_test

import (
	"context"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/tenant"
)

func TestWithTenantRoundTrip(t *testing.T) {
	ctx := context.Background()
	const wantTenant = "018f6c42-2b28-7654-a321-0123456789ab"

	ctxWithTenant := tenant.WithTenant(ctx, wantTenant)
	gotTenant, err := tenant.FromContext(ctxWithTenant)
	if err != nil {
		t.Fatalf("FromContext: %v", err)
	}
	if gotTenant != wantTenant {
		t.Fatalf("got tenant %q, want %q", gotTenant, wantTenant)
	}
}

func TestFromContextAbsentTenant(t *testing.T) {
	ctx := context.Background()
	gotTenant, err := tenant.FromContext(ctx)
	if err == nil {
		t.Fatalf("FromContext returned nil error and tenant %q, want error", gotTenant)
	}
	code, ok := errs.CodeOf(err)
	if !ok || code != tenant.ErrCodeNoTenant {
		t.Fatalf("got code %v, want %v", code, tenant.ErrCodeNoTenant)
	}
}

func TestFromContextEmptyTenant(t *testing.T) {
	ctx := tenant.WithTenant(context.Background(), "")
	gotTenant, err := tenant.FromContext(ctx)
	if err == nil {
		t.Fatalf("FromContext returned nil error and tenant %q, want error", gotTenant)
	}
	code, ok := errs.CodeOf(err)
	if !ok || code != tenant.ErrCodeNoTenant {
		t.Fatalf("got code %v, want %v", code, tenant.ErrCodeNoTenant)
	}
}

func TestFromContextNilContext(t *testing.T) {
	//nolint:staticcheck // intentionally testing nil context behavior
	gotTenant, err := tenant.FromContext(nil)
	if err == nil {
		t.Fatalf("FromContext(nil) returned nil error and tenant %q, want error", gotTenant)
	}
	code, ok := errs.CodeOf(err)
	if !ok || code != tenant.ErrCodeNoTenant {
		t.Fatalf("got code %v, want %v", code, tenant.ErrCodeNoTenant)
	}
}
