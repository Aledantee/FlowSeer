//go:build authz_integration

package integration_test

import (
	"context"
	"testing"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestOpenFGAMemberRequiresClaimedAndEnrolled checks that a stored
// tenant:T#enrolled@user:u relationship alone does not make tenant:T#member
// true, and that the contextual tenant:T#claimed@user:u completes it.
func TestOpenFGAMemberRequiresClaimedAndEnrolled(t *testing.T) {
	env := startOpenFGAEnv(t, nil)
	ctx := context.Background()

	tenant := "tenant:t1"
	user := "user:u1"

	if err := env.WriteTuple(ctx, user, "enrolled", tenant); err != nil {
		t.Fatalf("write enrolled: %v", err)
	}

	// One relationship away: the stored enrollment stands, the claim is absent.
	allowed, err := env.Check(ctx, user, "member", tenant)
	if err != nil {
		t.Fatalf("check member without claimed: %v", err)
	}
	if allowed {
		t.Fatal("expected member to be false without contextual claimed tuple")
	}

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

	// One relationship away: a claim for a user with no stored enrollment.
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

// TestOpenFGAPartnerAdminAndCrossTenantCapture checks that a partner admin is
// a member of the customer only with the home claim, and holds capture on its
// edge only while tenant:C#capturer@tenant:M#active_admin is stored.
func TestOpenFGAPartnerAdminAndCrossTenantCapture(t *testing.T) {
	env := startOpenFGAEnv(t, nil)
	ctx := context.Background()

	custTenant := "tenant:customer"
	partnerTenant := "tenant:partner"
	custEdge := "edge:cust-edge-1"
	partnerUser := "user:partner-admin"

	if err := env.WriteTuple(ctx, custTenant, "tenant", custEdge); err != nil {
		t.Fatalf("write edge tenant: %v", err)
	}
	if err := env.WriteTuple(ctx, partnerTenant, "partner", custTenant); err != nil {
		t.Fatalf("write partner: %v", err)
	}
	if err := env.WriteTuple(ctx, partnerUser, "enrolled", partnerTenant); err != nil {
		t.Fatalf("write partner enrolled: %v", err)
	}
	if err := env.WriteTuple(ctx, partnerUser, "admin", partnerTenant); err != nil {
		t.Fatalf("write partner admin: %v", err)
	}
	if err := env.WriteTuple(ctx, partnerTenant+"#active_admin", "capturer", custTenant); err != nil {
		t.Fatalf("write cross-tenant capturer: %v", err)
	}

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

	// A member of the partner tenant without active_admin reaches nothing in
	// the customer. The partner edge carries active_admin, not member.
	memberOnly := "user:partner-member"
	if err := env.WriteTuple(ctx, memberOnly, "enrolled", partnerTenant); err != nil {
		t.Fatalf("write partner member enrolled: %v", err)
	}
	memberClaim := &openfgav1.TupleKey{
		User:     memberOnly,
		Relation: "claimed",
		Object:   partnerTenant,
	}
	allowed, err = env.Check(ctx, memberOnly, "member", partnerTenant, memberClaim)
	if err != nil {
		t.Fatalf("check partner member in home tenant: %v", err)
	}
	if !allowed {
		t.Fatal("expected partner member to be a member of its home tenant")
	}
	allowed, err = env.Check(ctx, memberOnly, "member", custTenant, memberClaim)
	if err != nil {
		t.Fatalf("check partner member in customer tenant: %v", err)
	}
	if allowed {
		t.Fatal("expected member of the partner tenant without active_admin to be denied member of the customer")
	}
}

// TestOpenFGAPlatformAdmin checks that a platform admin with its claim is
// admin of every tenant and lacks full_payload.
func TestOpenFGAPlatformAdmin(t *testing.T) {
	env := startOpenFGAEnv(t, nil)
	ctx := context.Background()

	platformObj := "platform:flowseer"
	tenant := "tenant:t1"
	platUser := "user:platform-admin"

	if err := env.WriteTuple(ctx, platformObj, "platform", tenant); err != nil {
		t.Fatalf("write tenant platform: %v", err)
	}
	if err := env.WriteTuple(ctx, platUser, "enrolled", platformObj); err != nil {
		t.Fatalf("write platform enrolled: %v", err)
	}

	platClaim := &openfgav1.TupleKey{
		User:     platUser,
		Relation: "claimed",
		Object:   platformObj,
	}

	allowed, err := env.Check(ctx, platUser, "admin", tenant)
	if err != nil {
		t.Fatalf("check admin without platform claim: %v", err)
	}
	if allowed {
		t.Fatal("expected admin to be false without platform claim")
	}

	allowed, err = env.Check(ctx, platUser, "admin", tenant, platClaim)
	if err != nil {
		t.Fatalf("check admin with platform claim: %v", err)
	}
	if !allowed {
		t.Fatal("expected admin to be true for platform admin with claim")
	}

	allowed, err = env.Check(ctx, platUser, "full_payload", tenant, platClaim)
	if err != nil {
		t.Fatalf("check full_payload for platform admin: %v", err)
	}
	if allowed {
		t.Fatal("expected platform admin to lack full_payload on tenant")
	}
}

// TestOpenFGAEdgeGrantsDirectAndTenantIsolation checks that
// edge:E1#capture@user:u grants nothing on E2, and that edge#tenant is false
// for another tenant.
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

	allowed, err := env.Check(ctx, user, "capture", edge1)
	if err != nil {
		t.Fatalf("check edge1 capture: %v", err)
	}
	if !allowed {
		t.Fatal("expected capture to be allowed on edge1")
	}

	allowed, err = env.Check(ctx, user, "capture", edge2)
	if err != nil {
		t.Fatalf("check edge2 capture: %v", err)
	}
	if allowed {
		t.Fatal("expected edge1 grant to give nothing on edge2")
	}

	allowed, err = env.Check(ctx, tenant2, "tenant", edge1)
	if err != nil {
		t.Fatalf("check edge1 tenant for t2: %v", err)
	}
	if allowed {
		t.Fatal("expected edge#tenant to be false for tenant2")
	}

	allowed, err = env.Check(ctx, tenant1, "tenant", edge1)
	if err != nil {
		t.Fatalf("check edge1 tenant for t1: %v", err)
	}
	if !allowed {
		t.Fatal("expected edge#tenant to be true for tenant1")
	}
}

// TestOpenFGARuleRelationsBranchEvaluation checks that every relation a rule
// may name has, for each branch of its definition, one allowed case and one
// denial produced by removing a single relationship from it.
func TestOpenFGARuleRelationsBranchEvaluation(t *testing.T) {
	env := startOpenFGAEnv(t, nil)
	ctx := context.Background()

	const (
		tenant  = "tenant:eval-t"
		partner = "tenant:eval-partner"
		edge    = "edge:eval-e"
		device  = "device:eval-d"
		session = "capture_session:eval-s"
		role    = "role:eval-r"
		plat    = "platform:eval-p"
		user    = "user:eval-u"
	)

	// The tenant and edge links every tuple-to-userset branch reads. Each
	// case adds the relationship that names its branch and removes it again.
	for _, base := range []branchTuple{
		{tenant, "tenant", edge},
		{tenant, "tenant", device},
		{tenant, "tenant", session},
		{edge, "edge", session},
	} {
		if err := env.WriteTuple(ctx, base.user, base.relation, base.object); err != nil {
			t.Fatalf("write base relation %s %s %s: %v", base.user, base.relation, base.object, err)
		}
	}

	roleAssignee := role + "#assignee"
	partnerActiveAdmin := partner + "#active_admin"

	cases := []branchCase{
		// platform#admin: claimed and enrolled.
		{
			name: "platform admin requires claimed", target: plat, relation: "admin", user: user,
			setup: []branchTuple{{user, "enrolled", plat}, {user, "claimed", plat}},
			away:  branchTuple{user, "claimed", plat},
		},
		{
			name: "platform admin requires enrolled", target: plat, relation: "admin", user: user,
			setup: []branchTuple{{user, "enrolled", plat}, {user, "claimed", plat}},
			away:  branchTuple{user, "enrolled", plat},
		},

		// tenant#admin: [user, role#assignee] or admin from platform.
		{
			name: "tenant admin direct user", target: tenant, relation: "admin", user: user,
			setup: []branchTuple{{user, "admin", tenant}},
			away:  branchTuple{user, "admin", tenant},
		},
		{
			name: "tenant admin role assignee", target: tenant, relation: "admin", user: user,
			setup: []branchTuple{{roleAssignee, "admin", tenant}, {user, "assignee", role}},
			away:  branchTuple{roleAssignee, "admin", tenant},
		},
		{
			name: "tenant admin from platform admin", target: tenant, relation: "admin", user: user,
			setup: []branchTuple{{plat, "platform", tenant}, {user, "enrolled", plat}, {user, "claimed", plat}},
			away:  branchTuple{plat, "platform", tenant},
		},

		// edge#administer: [user, role#assignee] or admin from tenant.
		{
			name: "edge administer direct user", target: edge, relation: "administer", user: user,
			setup: []branchTuple{{user, "administer", edge}},
			away:  branchTuple{user, "administer", edge},
		},
		{
			name: "edge administer role assignee", target: edge, relation: "administer", user: user,
			setup: []branchTuple{{roleAssignee, "administer", edge}, {user, "assignee", role}},
			away:  branchTuple{roleAssignee, "administer", edge},
		},
		{
			name: "edge administer from tenant admin", target: edge, relation: "administer", user: user,
			setup: []branchTuple{{user, "admin", tenant}},
			away:  branchTuple{user, "admin", tenant},
		},

		// edge#capture: [user, role#assignee] or capturer from tenant.
		{
			name: "edge capture direct user", target: edge, relation: "capture", user: user,
			setup: []branchTuple{{user, "capture", edge}},
			away:  branchTuple{user, "capture", edge},
		},
		{
			name: "edge capture role assignee", target: edge, relation: "capture", user: user,
			setup: []branchTuple{{roleAssignee, "capture", edge}, {user, "assignee", role}},
			away:  branchTuple{roleAssignee, "capture", edge},
		},
		{
			name: "edge capture from tenant capturer", target: edge, relation: "capture", user: user,
			setup: []branchTuple{{user, "capturer", tenant}},
			away:  branchTuple{user, "capturer", tenant},
		},

		// edge#operate: [user, role#assignee] or operator from tenant. The
		// operate term of edge#view reaches these branches.
		{
			name: "edge operate direct user", target: edge, relation: "operate", user: user,
			setup: []branchTuple{{user, "operate", edge}},
			away:  branchTuple{user, "operate", edge},
		},
		{
			name: "edge operate role assignee", target: edge, relation: "operate", user: user,
			setup: []branchTuple{{roleAssignee, "operate", edge}, {user, "assignee", role}},
			away:  branchTuple{roleAssignee, "operate", edge},
		},
		{
			name: "edge operate from tenant operator", target: edge, relation: "operate", user: user,
			setup: []branchTuple{{user, "operator", tenant}},
			away:  branchTuple{user, "operator", tenant},
		},

		// edge#view: [user, role#assignee] or administer or operate or capture
		// or viewer from tenant.
		{
			name: "edge view direct user", target: edge, relation: "view", user: user,
			setup: []branchTuple{{user, "view", edge}},
			away:  branchTuple{user, "view", edge},
		},
		{
			name: "edge view role assignee", target: edge, relation: "view", user: user,
			setup: []branchTuple{{roleAssignee, "view", edge}, {user, "assignee", role}},
			away:  branchTuple{roleAssignee, "view", edge},
		},
		{
			name: "edge view via administer", target: edge, relation: "view", user: user,
			setup: []branchTuple{{user, "administer", edge}},
			away:  branchTuple{user, "administer", edge},
		},
		{
			name: "edge view via operate", target: edge, relation: "view", user: user,
			setup: []branchTuple{{user, "operate", edge}},
			away:  branchTuple{user, "operate", edge},
		},
		{
			name: "edge view via capture", target: edge, relation: "view", user: user,
			setup: []branchTuple{{user, "capture", edge}},
			away:  branchTuple{user, "capture", edge},
		},
		{
			name: "edge view from tenant viewer", target: edge, relation: "view", user: user,
			setup: []branchTuple{{user, "viewer", tenant}},
			away:  branchTuple{user, "viewer", tenant},
		},

		// device#operate: [user, role#assignee] or operator from tenant.
		{
			name: "device operate direct user", target: device, relation: "operate", user: user,
			setup: []branchTuple{{user, "operate", device}},
			away:  branchTuple{user, "operate", device},
		},
		{
			name: "device operate role assignee", target: device, relation: "operate", user: user,
			setup: []branchTuple{{roleAssignee, "operate", device}, {user, "assignee", role}},
			away:  branchTuple{roleAssignee, "operate", device},
		},
		{
			name: "device operate from tenant operator", target: device, relation: "operate", user: user,
			setup: []branchTuple{{user, "operator", tenant}},
			away:  branchTuple{user, "operator", tenant},
		},

		// device#view: [user, role#assignee] or operate or viewer from tenant.
		{
			name: "device view direct user", target: device, relation: "view", user: user,
			setup: []branchTuple{{user, "view", device}},
			away:  branchTuple{user, "view", device},
		},
		{
			name: "device view role assignee", target: device, relation: "view", user: user,
			setup: []branchTuple{{roleAssignee, "view", device}, {user, "assignee", role}},
			away:  branchTuple{roleAssignee, "view", device},
		},
		{
			name: "device view via operate", target: device, relation: "view", user: user,
			setup: []branchTuple{{user, "operate", device}},
			away:  branchTuple{user, "operate", device},
		},
		{
			name: "device view from tenant viewer", target: device, relation: "view", user: user,
			setup: []branchTuple{{user, "viewer", tenant}},
			away:  branchTuple{user, "viewer", tenant},
		},

		// capture_session#manage: capture from edge.
		{
			name: "capture_session manage via edge capture", target: session, relation: "manage", user: user,
			setup: []branchTuple{{user, "capture", edge}},
			away:  branchTuple{user, "capture", edge},
		},

		// capture_session#download: requester or capture from edge.
		{
			name: "capture_session download requester", target: session, relation: "download", user: user,
			setup: []branchTuple{{user, "requester", session}},
			away:  branchTuple{user, "requester", session},
		},
		{
			name: "capture_session download via edge capture", target: session, relation: "download", user: user,
			setup: []branchTuple{{user, "capture", edge}},
			away:  branchTuple{user, "capture", edge},
		},

		// tenant#operator: [user, role#assignee, tenant#active_admin] or admin.
		// edge#operate and device#operate reach this through operator from
		// tenant, so its branches are on the path a rule may name.
		{
			name: "tenant operator direct user", target: tenant, relation: "operator", user: user,
			setup: []branchTuple{{user, "operator", tenant}},
			away:  branchTuple{user, "operator", tenant},
		},
		{
			name: "tenant operator role assignee", target: tenant, relation: "operator", user: user,
			setup: []branchTuple{{roleAssignee, "operator", tenant}, {user, "assignee", role}},
			away:  branchTuple{roleAssignee, "operator", tenant},
		},
		{
			name: "tenant operator from active admin userset", target: tenant, relation: "operator", user: user,
			setup: []branchTuple{{partnerActiveAdmin, "operator", tenant}, {user, "admin", partner}, {user, "claimed", partner}, {user, "enrolled", partner}},
			away:  branchTuple{partnerActiveAdmin, "operator", tenant},
		},
		{
			name: "tenant operator from admin", target: tenant, relation: "operator", user: user,
			setup: []branchTuple{{user, "admin", tenant}},
			away:  branchTuple{user, "admin", tenant},
		},

		// tenant#capturer: [user, role#assignee, tenant#active_admin] or admin.
		{
			name: "tenant capturer direct user", target: tenant, relation: "capturer", user: user,
			setup: []branchTuple{{user, "capturer", tenant}},
			away:  branchTuple{user, "capturer", tenant},
		},
		{
			name: "tenant capturer role assignee", target: tenant, relation: "capturer", user: user,
			setup: []branchTuple{{roleAssignee, "capturer", tenant}, {user, "assignee", role}},
			away:  branchTuple{roleAssignee, "capturer", tenant},
		},
		{
			name: "tenant capturer from active admin userset", target: tenant, relation: "capturer", user: user,
			setup: []branchTuple{{partnerActiveAdmin, "capturer", tenant}, {user, "admin", partner}, {user, "claimed", partner}, {user, "enrolled", partner}},
			away:  branchTuple{partnerActiveAdmin, "capturer", tenant},
		},
		{
			name: "tenant capturer from admin", target: tenant, relation: "capturer", user: user,
			setup: []branchTuple{{user, "admin", tenant}},
			away:  branchTuple{user, "admin", tenant},
		},

		// tenant#viewer: [user, role#assignee, tenant#active_admin] or
		// operator or capturer.
		{
			name: "tenant viewer direct user", target: tenant, relation: "viewer", user: user,
			setup: []branchTuple{{user, "viewer", tenant}},
			away:  branchTuple{user, "viewer", tenant},
		},
		{
			name: "tenant viewer role assignee", target: tenant, relation: "viewer", user: user,
			setup: []branchTuple{{roleAssignee, "viewer", tenant}, {user, "assignee", role}},
			away:  branchTuple{roleAssignee, "viewer", tenant},
		},
		{
			name: "tenant viewer from active admin userset", target: tenant, relation: "viewer", user: user,
			setup: []branchTuple{{partnerActiveAdmin, "viewer", tenant}, {user, "admin", partner}, {user, "claimed", partner}, {user, "enrolled", partner}},
			away:  branchTuple{partnerActiveAdmin, "viewer", tenant},
		},
		{
			name: "tenant viewer from operator", target: tenant, relation: "viewer", user: user,
			setup: []branchTuple{{user, "operator", tenant}},
			away:  branchTuple{user, "operator", tenant},
		},
		{
			name: "tenant viewer from capturer", target: tenant, relation: "viewer", user: user,
			setup: []branchTuple{{user, "capturer", tenant}},
			away:  branchTuple{user, "capturer", tenant},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, st := range tc.setup {
				if err := env.WriteTuple(ctx, st.user, st.relation, st.object); err != nil {
					t.Fatalf("setup write %s %s %s: %v", st.user, st.relation, st.object, err)
				}
			}

			allowed, err := env.Check(ctx, tc.user, tc.relation, tc.target)
			if err != nil {
				t.Fatalf("check %s on %s before removal: %v", tc.relation, tc.target, err)
			}
			if !allowed {
				t.Fatalf("expected %s on %s for %s to be allowed", tc.relation, tc.target, tc.user)
			}

			if err := env.Write(ctx, nil, []*openfgav1.TupleKeyWithoutCondition{{
				User:     tc.away.user,
				Relation: tc.away.relation,
				Object:   tc.away.object,
			}}); err != nil {
				t.Fatalf("remove %s %s %s: %v", tc.away.user, tc.away.relation, tc.away.object, err)
			}

			allowed, err = env.Check(ctx, tc.user, tc.relation, tc.target)
			if err != nil {
				t.Fatalf("check %s on %s after removing one relationship: %v", tc.relation, tc.target, err)
			}
			if allowed {
				t.Fatalf("expected %s on %s for %s to be denied after removing %s %s %s", tc.relation, tc.target, tc.user, tc.away.user, tc.away.relation, tc.away.object)
			}

			var rest []*openfgav1.TupleKeyWithoutCondition
			for _, st := range tc.setup {
				if st == tc.away {
					continue
				}
				rest = append(rest, &openfgav1.TupleKeyWithoutCondition{
					User:     st.user,
					Relation: st.relation,
					Object:   st.object,
				})
			}
			if len(rest) > 0 {
				if err := env.Write(ctx, nil, rest); err != nil {
					t.Fatalf("teardown write: %v", err)
				}
			}
		})
	}
}

type branchTuple struct {
	user     string
	relation string
	object   string
}

type branchCase struct {
	name     string
	target   string
	relation string
	user     string
	setup    []branchTuple
	away     branchTuple
}

// TestOpenFGASiteWriteRefused checks that a write of site:s#viewer@user:u is
// refused with OpenFGA's validation code, on a store that accepts a valid
// write through the same path.
func TestOpenFGASiteWriteRefused(t *testing.T) {
	env := startOpenFGAEnv(t, nil)
	ctx := context.Background()

	if err := env.WriteTuple(ctx, "tenant:s", "tenant", "edge:s"); err != nil {
		t.Fatalf("accepted write through the same path failed: %v", err)
	}

	err := env.WriteTuple(ctx, "user:u", "viewer", "site:s")
	if err == nil {
		t.Fatal("expected write of site:s#viewer@user:u to be refused with error")
	}
	if code := status.Code(err); code != codes.Code(2000) {
		t.Fatalf("site write status code = %d (%v), want 2000", code, err)
	}
}
