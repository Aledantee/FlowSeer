package identityapi_test

import (
	"testing"

	"connectrpc.com/connect"

	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	"go.aledante.io/FlowSeer/src/services/device/internal/tenantstore"
)

func TestCreateTenantKeepsItsRetentionClass(t *testing.T) {
	f := newFixture(t)
	ctx := admitted(t, tenantA)
	req := createRequest()
	req.SetRetentionClass(identityv1.RetentionClass_RETENTION_CLASS_SHORT)

	resp, err := f.tenant.CreateTenant(ctx, request(t, req))
	if err != nil {
		t.Fatal(err)
	}
	valid(t, resp.Msg.GetTenant())
	if got := resp.Msg.GetTenant().GetConfig().GetRetentionClass(); got != identityv1.RetentionClass_RETENTION_CLASS_SHORT {
		t.Fatalf("created retention class = %v, want SHORT", got)
	}
	stored, err := f.tenants.LookupByOrg(ctx, issuer, "acme")
	if err != nil || stored == nil {
		t.Fatalf("stored tenant = %v, %v", stored, err)
	}
	if got := stored.GetConfig().GetRetentionClass(); got != identityv1.RetentionClass_RETENTION_CLASS_SHORT {
		t.Fatalf("stored retention class = %v, want SHORT", got)
	}

	again := createRequest()
	again.SetRetentionClass(identityv1.RetentionClass_RETENTION_CLASS_SHORT)
	if _, err := f.tenant.CreateTenant(ctx, request(t, again)); err != nil {
		t.Fatalf("retry with the same class: %v", err)
	}
	other := createRequest()
	other.SetRetentionClass(identityv1.RetentionClass_RETENTION_CLASS_LONG)
	_, err = f.tenant.CreateTenant(ctx, request(t, other))
	wantError(t, err, connect.CodeAlreadyExists, string(tenantstore.ErrCodeAlreadyExists))
}

func TestCreateTenantLeavesAnOmittedRetentionClassUnset(t *testing.T) {
	f := newFixture(t)
	ctx := admitted(t, tenantA)

	if _, err := f.tenant.CreateTenant(ctx, request(t, createRequest())); err != nil {
		t.Fatal(err)
	}
	stored, err := f.tenants.LookupByOrg(ctx, issuer, "acme")
	if err != nil || stored == nil {
		t.Fatalf("stored tenant = %v, %v", stored, err)
	}
	valid(t, stored)
	if stored.GetConfig().HasRetentionClass() {
		t.Fatalf("stored retention class = %v, want unset", stored.GetConfig().GetRetentionClass())
	}
}
