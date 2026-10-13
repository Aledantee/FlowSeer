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

func TestValidateTenant(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tenant  string
		wantErr bool
	}{
		{name: "default tenant", tenant: "default", wantErr: false},
		{name: "valid uuid", tenant: "018f6c42-2b28-7654-a321-0123456789ab", wantErr: false},
		{name: "empty", tenant: "", wantErr: true},
		{name: "dotted tenant", tenant: "acme.prod", wantErr: true},
		{name: "wildcard tenant", tenant: "*", wantErr: true},
		{name: "path traversal tenant", tenant: "../foo", wantErr: true},
		{name: "upper case uuid", tenant: "018F6C42-2B28-7654-A321-0123456789AB", wantErr: true},
		{name: "braced uuid", tenant: "{018f6c42-2b28-7654-a321-0123456789ab}", wantErr: true},
		{name: "urn uuid", tenant: "urn:uuid:018f6c42-2b28-7654-a321-0123456789ab", wantErr: true},
		{name: "dashless hex uuid", tenant: "018f6c422b287654a3210123456789ab", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tenant.Validate(tc.tenant)
			if tc.wantErr && err == nil {
				t.Fatalf("Validate(%q) = nil, want error", tc.tenant)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Validate(%q) = %v, want nil", tc.tenant, err)
			}
			if tc.wantErr {
				if code, ok := errs.CodeOf(err); !ok || code != tenant.ErrCodeInvalidTenant {
					t.Fatalf("got code %v, want %v", code, tenant.ErrCodeInvalidTenant)
				}
			}
		})
	}
}

func TestSetFromReturnsTheAdmittedSetAndFromContextFindsNoSingleTenant(t *testing.T) {
	const first, second = "018f6c42-2b28-7654-a321-0123456789ab", "018f6c42-2b28-7654-a321-0123456789ac"
	ctx := tenant.WithSet(context.Background(), []string{first, second})

	got, err := tenant.SetFrom(ctx)
	if err != nil {
		t.Fatalf("SetFrom: %v", err)
	}
	if len(got) != 2 || got[0] != first || got[1] != second {
		t.Errorf("SetFrom = %v, want [%s %s]", got, first, second)
	}
	if _, err := tenant.FromContext(ctx); err == nil {
		t.Error("FromContext found a single tenant on a context that holds a set, want ErrCodeNoTenant")
	}
}

func TestSetFromTreatsASingleTenantAsASetOfOne(t *testing.T) {
	const only = "018f6c42-2b28-7654-a321-0123456789ab"

	got, err := tenant.SetFrom(tenant.WithTenant(context.Background(), only))

	if err != nil || len(got) != 1 || got[0] != only {
		t.Errorf("SetFrom = %v, %v, want [%s]", got, err, only)
	}
}

func TestSetFromRefusesAContextWithoutTenants(t *testing.T) {
	for name, ctx := range map[string]context.Context{
		"no tenant":     context.Background(),
		"an empty set":  tenant.WithSet(context.Background(), nil),
		"an empty list": tenant.WithSet(context.Background(), []string{}),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := tenant.SetFrom(ctx)

			code, ok := errs.CodeOf(err)
			if !ok || code != tenant.ErrCodeNoTenant {
				t.Errorf("SetFrom code = %v (err %v), want %v", code, err, tenant.ErrCodeNoTenant)
			}
		})
	}
}

func TestTheSetIsCopiedOnTheWayInAndOut(t *testing.T) {
	const first, second = "018f6c42-2b28-7654-a321-0123456789ab", "018f6c42-2b28-7654-a321-0123456789ac"
	input := []string{first, second}
	ctx := tenant.WithSet(context.Background(), input)
	input[0] = "changed-after-store"

	got, err := tenant.SetFrom(ctx)
	if err != nil || got[0] != first {
		t.Fatalf("SetFrom = %v, %v, want the first tenant unchanged", got, err)
	}
	got[1] = "changed-after-read"
	again, _ := tenant.SetFrom(ctx)

	if again[1] != second {
		t.Errorf("a caller changed the stored set to %v", again)
	}
}
