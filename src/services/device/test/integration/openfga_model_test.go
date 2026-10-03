//go:build authz_integration

package integration_test

import (
	"context"
	"testing"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
)

// TestOpenFGAMemberRequiresClaimedAndEnrolled verifies requirement 2:
// With tenant:T#enrolled@user:u stored, tenant:T#member is true only with
// the contextual tenant:T#claimed@user:u.
func TestOpenFGAMemberRequiresClaimedAndEnrolled(t *testing.T) {
	env := startOpenFGAEnv(t, nil)
	ctx := context.Background()

	tenant := "tenant:t1"
	user := "user:u1"

	// Store enrolled
	if err := env.WriteTuple(ctx, user, "enrolled", tenant); err != nil {
		t.Fatalf("write enrolled: %v", err)
	}

	// 1 relationship away: no claimed tuple provided.
	allowed, err := env.Check(ctx, user, "member", tenant)
	if err != nil {
		t.Fatalf("check member without claimed: %v", err)
	}
	if allowed {
		t.Fatal("expected member to be false without contextual claimed tuple")
	}

	// Allowed case: both enrolled and contextual claimed present.
	claimedTuple := &openfgav1.TupleKey{
		User:     user,
		Relation: "claimed",
		Object:   tenant,
	}
	allowed, err = env.Check(ctx, user, "member", tenant, claimedTuple)
	if err != nil {
		t.Fatalf("check member with claimed: %v", err)
	}
	if !allowed {
		t.Fatal("expected member to be true with enrolled + contextual claimed")
	}

	// 1 relationship away: claimed for different user not enrolled.
	otherUser := "user:u2"
	otherClaimed := &openfgav1.TupleKey{
		User:     otherUser,
		Relation: "claimed",
		Object:   tenant,
	}
	allowed, err = env.Check(ctx, otherUser, "member", tenant, otherClaimed)
	if err != nil {
		t.Fatalf("check member for unenrolled user: %v", err)
	}
	if allowed {
		t.Fatal("expected member to be false for unenrolled user despite claimed tuple")
	}
}

// TestOpenFGAPartnerAdminAndCrossTenantCapture verifies requirement 2:
// A partner admin is a member of the customer only with the home claim, and holds
// capture on its edge only while tenant:C#capturer@tenant:M#active_admin is stored.
func TestOpenFGAPartnerAdminAndCrossTenantCapture(t *testing.T) {
	env := startOpenFGAEnv(t, nil)
	ctx := context.Background()

	custTenant := "tenant:customer"
	partnerTenant := "tenant:partner"
	custEdge := "edge:cust-edge-1"
	partnerUser := "user:partner-admin"

	// edge belongs to customer tenant
	if err := env.WriteTuple(ctx, custTenant, "tenant", custEdge); err != nil {
		t.Fatalf("write edge tenant: %v", err)
	}
	// customer has partner
	if err := env.WriteTuple(ctx, partnerTenant, "partner", custTenant); err != nil {
		t.Fatalf("write partner: %v", err)
	}
	// partner user is enrolled and admin in partner tenant
	if err := env.WriteTuple(ctx, partnerUser, "enrolled", partnerTenant); err != nil {
		t.Fatalf("write partner enrolled: %v", err)
	}
	if err := env.WriteTuple(ctx, partnerUser, "admin", partnerTenant); err != nil {
		t.Fatalf("write partner admin: %v", err)
	}
	// cross-tenant grant: customer allows partner active_admin as capturer
	if err := env.WriteTuple(ctx, partnerTenant+"#active_admin", "capturer", custTenant); err != nil {
		t.Fatalf("write cross-tenant capturer: %v", err)
	}

	// Without home claim: partner admin is not member of customer and has no capture on edge.
	allowed, err := env.Check(ctx, partnerUser, "member", custTenant)
	if err != nil {
		t.Fatalf("check member without home claim: %v", err)
	}
	if allowed {
		t.Fatal("expected member to be false without partner home claim")
	}

	allowed, err = env.Check(ctx, partnerUser, "capture", custEdge)
	if err != nil {
		t.Fatalf("check capture without home claim: %v", err)
	}
	if allowed {
		t.Fatal("expected capture on edge to be false without partner home claim")
	}

	// With partner home claim: partner admin is member of customer and has capture on edge.
	homeClaim := &openfgav1.TupleKey{
		User:     partnerUser,
		Relation: "claimed",
		Object:   partnerTenant,
	}
	allowed, err = env.Check(ctx, partnerUser, "member", custTenant, homeClaim)
	if err != nil {
		t.Fatalf("check member with home claim: %v", err)
	}
	if !allowed {
		t.Fatal("expected member to be true with partner home claim")
	}

	allowed, err = env.Check(ctx, partnerUser, "capture", custEdge, homeClaim)
	if err != nil {
		t.Fatalf("check capture with home claim: %v", err)
	}
	if !allowed {
		t.Fatal("expected capture on edge to be true with partner home claim and cross-tenant grant")
	}

	// Delete cross-tenant capturer grant: edge capture is lost despite active partner admin.
	delTuple := &openfgav1.TupleKeyWithoutCondition{
		User:     partnerTenant + "#active_admin",
		Relation: "capturer",
		Object:   custTenant,
	}
	if err := env.Write(ctx, nil, []*openfgav1.TupleKeyWithoutCondition{delTuple}); err != nil {
		t.Fatalf("delete cross-tenant capturer: %v", err)
	}

	allowed, err = env.Check(ctx, partnerUser, "capture", custEdge, homeClaim)
	if err != nil {
		t.Fatalf("check capture after cross-tenant grant removed: %v", err)
	}
	if allowed {
		t.Fatal("expected capture on edge to be false after cross-tenant grant removed")
	}
}

// TestOpenFGAPlatformAdmin verifies requirement 2:
// A platform admin with its claim is admin of every tenant and lacks full_payload.
func TestOpenFGAPlatformAdmin(t *testing.T) {
	env := startOpenFGAEnv(t, nil)
	ctx := context.Background()

	platformObj := "platform:flowseer"
	tenant := "tenant:t1"
	platUser := "user:platform-admin"

	// Associate tenant with platform
	if err := env.WriteTuple(ctx, platformObj, "platform", tenant); err != nil {
		t.Fatalf("write tenant platform: %v", err)
	}
	// Platform admin is enrolled in platform
	if err := env.WriteTuple(ctx, platUser, "enrolled", platformObj); err != nil {
		t.Fatalf("write platform enrolled: %v", err)
	}

	platClaim := &openfgav1.TupleKey{
		User:     platUser,
		Relation: "claimed",
		Object:   platformObj,
	}

	// Without platform claim: not admin of tenant
	allowed, err := env.Check(ctx, platUser, "admin", tenant)
	if err != nil {
		t.Fatalf("check admin without platform claim: %v", err)
	}
	if allowed {
		t.Fatal("expected admin to be false without platform claim")
	}

	// With platform claim: admin of tenant
	allowed, err = env.Check(ctx, platUser, "admin", tenant, platClaim)
	if err != nil {
		t.Fatalf("check admin with platform claim: %v", err)
	}
	if !allowed {
		t.Fatal("expected admin to be true for platform admin with claim")
	}

	// Lacks full_payload
	allowed, err = env.Check(ctx, platUser, "full_payload", tenant, platClaim)
	if err != nil {
		t.Fatalf("check full_payload for platform admin: %v", err)
	}
	if allowed {
		t.Fatal("expected platform admin to lack full_payload on tenant")
	}
}

// TestOpenFGAEdgeGrantsDirectAndTenantIsolation verifies requirement 2:
// edge:E1#capture@user:u grants nothing on E2, and edge#tenant is false for another tenant.
func TestOpenFGAEdgeGrantsDirectAndTenantIsolation(t *testing.T) {
	env := startOpenFGAEnv(t, nil)
	ctx := context.Background()

	edge1 := "edge:e1"
	edge2 := "edge:e2"
	tenant1 := "tenant:t1"
	tenant2 := "tenant:t2"
	user := "user:u1"

	if err := env.WriteTuple(ctx, tenant1, "tenant", edge1); err != nil {
		t.Fatalf("write edge1 tenant: %v", err)
	}
	if err := env.WriteTuple(ctx, tenant1, "tenant", edge2); err != nil {
		t.Fatalf("write edge2 tenant: %v", err)
	}
	if err := env.WriteTuple(ctx, user, "capture", edge1); err != nil {
		t.Fatalf("write edge1 capture: %v", err)
	}

	// Allowed on edge1
	allowed, err := env.Check(ctx, user, "capture", edge1)
	if err != nil {
		t.Fatalf("check edge1 capture: %v", err)
	}
	if !allowed {
		t.Fatal("expected capture to be allowed on edge1")
	}

	// Denied on edge2 (grants nothing on E2)
	allowed, err = env.Check(ctx, user, "capture", edge2)
	if err != nil {
		t.Fatalf("check edge2 capture: %v", err)
	}
	if allowed {
		t.Fatal("expected edge1 grant to give nothing on edge2")
	}

	// edge#tenant is false for another tenant
	allowed, err = env.Check(ctx, tenant2, "tenant", edge1)
	if err != nil {
		t.Fatalf("check edge1 tenant for t2: %v", err)
	}
	if allowed {
		t.Fatal("expected edge#tenant to be false for tenant2")
	}
}

// TestOpenFGARuleRelationsBranchEvaluation verifies that for every relation a rule may name,
// each branch of its definition has one allowed case and one denial a single relationship away.
func TestOpenFGARuleRelationsBranchEvaluation(t *testing.T) {
	env := startOpenFGAEnv(t, nil)
	ctx := context.Background()

	tenant := "tenant:eval-t"
	edge := "edge:eval-e"
	device := "device:eval-d"
	session := "capture_session:eval-s"
	role := "role:eval-r"
	platformObj := "platform:eval-p"
	user := "user:eval-u"

	// Base relationships
	for _, rel := range []struct{ u, r, o string }{
		{tenant, "tenant", edge},
		{tenant, "tenant", device},
		{tenant, "tenant", session},
		{edge, "edge", session},
	} {
		if err := env.WriteTuple(ctx, rel.u, rel.r, rel.o); err != nil {
			t.Fatalf("write base relation %s %s %s: %v", rel.u, rel.r, rel.o, err)
		}
	}

	type branchTest struct {
		name     string
		target   string
		relation string
		user     string
		setup    []struct{ u, r, o string }
		teardown []struct{ u, r, o string }
	}

	tests := []branchTest{
		// platform#admin
		{
			name:     "platform admin direct enrolled+claimed",
			target:   platformObj,
			relation: "admin",
			user:     user,
			setup:    []struct{ u, r, o string }{{user, "enrolled", platformObj}, {user, "claimed", platformObj}},
			teardown: []struct{ u, r, o string }{{user, "enrolled", platformObj}, {user, "claimed", platformObj}},
		},
		// tenant#admin - branch 1: direct user
		{
			name:     "tenant admin direct user",
			target:   tenant,
			relation: "admin",
			user:     user,
			setup:    []struct{ u, r, o string }{{user, "admin", tenant}},
			teardown: []struct{ u, r, o string }{{user, "admin", tenant}},
		},
		// tenant#admin - branch 2: role assignee
		{
			name:     "tenant admin via role assignee",
			target:   tenant,
			relation: "admin",
			user:     user,
			setup:    []struct{ u, r, o string }{{role + "#assignee", "admin", tenant}, {user, "assignee", role}},
			teardown: []struct{ u, r, o string }{{role + "#assignee", "admin", tenant}, {user, "assignee", role}},
		},
		// edge#administer - branch 1: direct user
		{
			name:     "edge administer direct user",
			target:   edge,
			relation: "administer",
			user:     user,
			setup:    []struct{ u, r, o string }{{user, "administer", edge}},
			teardown: []struct{ u, r, o string }{{user, "administer", edge}},
		},
		// edge#administer - branch 2: admin from tenant
		{
			name:     "edge administer from tenant admin",
			target:   edge,
			relation: "administer",
			user:     user,
			setup:    []struct{ u, r, o string }{{user, "admin", tenant}},
			teardown: []struct{ u, r, o string }{{user, "admin", tenant}},
		},
		// edge#capture - branch 1: direct user
		{
			name:     "edge capture direct user",
			target:   edge,
			relation: "capture",
			user:     user,
			setup:    []struct{ u, r, o string }{{user, "capture", edge}},
			teardown: []struct{ u, r, o string }{{user, "capture", edge}},
		},
		// edge#capture - branch 2: role assignee
		{
			name:     "edge capture role assignee",
			target:   edge,
			relation: "capture",
			user:     user,
			setup:    []struct{ u, r, o string }{{role + "#assignee", "capture", edge}, {user, "assignee", role}},
			teardown: []struct{ u, r, o string }{{role + "#assignee", "capture", edge}, {user, "assignee", role}},
		},
		// edge#capture - branch 3: capturer from tenant
		{
			name:     "edge capture from tenant capturer",
			target:   edge,
			relation: "capture",
			user:     user,
			setup:    []struct{ u, r, o string }{{user, "capturer", tenant}},
			teardown: []struct{ u, r, o string }{{user, "capturer", tenant}},
		},
		// edge#operate - branch 1: direct user
		{
			name:     "edge operate direct user",
			target:   edge,
			relation: "operate",
			user:     user,
			setup:    []struct{ u, r, o string }{{user, "operate", edge}},
			teardown: []struct{ u, r, o string }{{user, "operate", edge}},
		},
		// edge#operate - branch 2: operator from tenant
		{
			name:     "edge operate from tenant operator",
			target:   edge,
			relation: "operate",
			user:     user,
			setup:    []struct{ u, r, o string }{{user, "operator", tenant}},
			teardown: []struct{ u, r, o string }{{user, "operator", tenant}},
		},
		// edge#view - branch 1: direct user
		{
			name:     "edge view direct user",
			target:   edge,
			relation: "view",
			user:     user,
			setup:    []struct{ u, r, o string }{{user, "view", edge}},
			teardown: []struct{ u, r, o string }{{user, "view", edge}},
		},
		// edge#view - branch 2: viewer from tenant
		{
			name:     "edge view from tenant viewer",
			target:   edge,
			relation: "view",
			user:     user,
			setup:    []struct{ u, r, o string }{{user, "viewer", tenant}},
			teardown: []struct{ u, r, o string }{{user, "viewer", tenant}},
		},
		// device#operate - branch 1: direct user
		{
			name:     "device operate direct user",
			target:   device,
			relation: "operate",
			user:     user,
			setup:    []struct{ u, r, o string }{{user, "operate", device}},
			teardown: []struct{ u, r, o string }{{user, "operate", device}},
		},
		// device#operate - branch 2: operator from tenant
		{
			name:     "device operate from tenant operator",
			target:   device,
			relation: "operate",
			user:     user,
			setup:    []struct{ u, r, o string }{{user, "operator", tenant}},
			teardown: []struct{ u, r, o string }{{user, "operator", tenant}},
		},
		// device#view - branch 1: direct user
		{
			name:     "device view direct user",
			target:   device,
			relation: "view",
			user:     user,
			setup:    []struct{ u, r, o string }{{user, "view", device}},
			teardown: []struct{ u, r, o string }{{user, "view", device}},
		},
		// device#view - branch 2: viewer from tenant
		{
			name:     "device view from tenant viewer",
			target:   device,
			relation: "view",
			user:     user,
			setup:    []struct{ u, r, o string }{{user, "viewer", tenant}},
			teardown: []struct{ u, r, o string }{{user, "viewer", tenant}},
		},
		// capture_session#manage - capture from edge
		{
			name:     "capture_session manage via edge capture",
			target:   session,
			relation: "manage",
			user:     user,
			setup:    []struct{ u, r, o string }{{user, "capture", edge}},
			teardown: []struct{ u, r, o string }{{user, "capture", edge}},
		},
		// capture_session#download - branch 1: requester
		{
			name:     "capture_session download via requester",
			target:   session,
			relation: "download",
			user:     user,
			setup:    []struct{ u, r, o string }{{user, "requester", session}},
			teardown: []struct{ u, r, o string }{{user, "requester", session}},
		},
		// capture_session#download - branch 2: capture from edge
		{
			name:     "capture_session download via edge capture",
			target:   session,
			relation: "download",
			user:     user,
			setup:    []struct{ u, r, o string }{{user, "capture", edge}},
			teardown: []struct{ u, r, o string }{{user, "capture", edge}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 1 relationship away: without setup tuples, check should be denied
			allowed, err := env.Check(ctx, tt.user, tt.relation, tt.target)
			if err != nil {
				t.Fatalf("check before setup: %v", err)
			}
			if allowed {
				t.Fatalf("expected %s on %s for %s to be denied before setup", tt.relation, tt.target, tt.user)
			}

			// Apply setup
			for _, st := range tt.setup {
				if err := env.WriteTuple(ctx, st.u, st.r, st.o); err != nil {
					t.Fatalf("setup write %s %s %s: %v", st.u, st.r, st.o, err)
				}
			}

			// Now allowed
			allowed, err = env.Check(ctx, tt.user, tt.relation, tt.target)
			if err != nil {
				t.Fatalf("check after setup: %v", err)
			}
			if !allowed {
				t.Fatalf("expected %s on %s for %s to be allowed after setup", tt.relation, tt.target, tt.user)
			}

			// Clean up setup
			var dels []*openfgav1.TupleKeyWithoutCondition
			for _, st := range tt.teardown {
				dels = append(dels, &openfgav1.TupleKeyWithoutCondition{
					User:     st.u,
					Relation: st.r,
					Object:   st.o,
				})
			}
			if err := env.Write(ctx, nil, dels); err != nil {
				t.Fatalf("teardown write: %v", err)
			}

			// Back to denied
			allowed, err = env.Check(ctx, tt.user, tt.relation, tt.target)
			if err != nil {
				t.Fatalf("check after teardown: %v", err)
			}
			if allowed {
				t.Fatalf("expected %s on %s for %s to be denied after teardown", tt.relation, tt.target, tt.user)
			}
		})
	}
}

// TestOpenFGASiteWriteRefused verifies requirement 2:
// A write of site:s#viewer@user:u is refused with error.
func TestOpenFGASiteWriteRefused(t *testing.T) {
	env := startOpenFGAEnv(t, nil)
	ctx := context.Background()

	err := env.WriteTuple(ctx, "user:u", "viewer", "site:s")
	if err == nil {
		t.Fatal("expected write of site:s#viewer@user:u to be refused with error")
	}
	t.Logf("site write refused as expected: %v", err)
}
