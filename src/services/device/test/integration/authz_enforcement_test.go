//go:build authz_integration

package integration_test

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	apicapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1/capturev1connect"
	apiedgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1/edgev1connect"
	apiidentityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/identity/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/identity/v1/identityv1connect"
	modelcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn/authntest"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz/openfga"
	"go.aledante.io/FlowSeer/src/services/device/internal/tenantstore"
)

func TestEnforcementAgainstTheRealEngine(t *testing.T) {
	env := startOpenFGAEnv(t, nil)
	iss := authntest.New(t)
	keyFile := writeTempKeyFile(t, testPresharedKey)
	dir := t.TempDir()
	writeCredentials(t, filepath.Join(dir, "credentials"))
	regDir := writeRegistry(t, filepath.Join(dir, "registry.textproto"), "0192e6a0-0000-7000-8000-00000000dead", 0)

	token := func(subject, organization string) string {
		return iss.Sign(map[string]any{
			"iss": iss.URL(), "sub": subject, "aud": "flowseer-real-engine",
			"org_id": organization, "exp": time.Now().Add(time.Hour).Unix(),
		})
	}
	platformToken := token("platform-admin", "flowseer-platform")
	c := &central{
		t: t, dir: dir, configDir: dir, registry: regDir,
		apiPort: freePort(t), busPort: freePort(t), issuer: iss,
		audience: "flowseer-real-engine", token: platformToken,
		platformAdmin: storev1.PlatformAdmin_builder{
			Issuer: new(iss.URL()), Organization: new("flowseer-platform"),
			Subjects: []string{"platform-admin"}, OrganizationClaimName: new("org_id"),
		}.Build(),
		authzEndpoint: fmt.Sprintf("https://%s", env.endpoint),
		authzStoreID:  env.storeID, authzModelID: env.modelID,
		authzKeyFile: keyFile, authzCAFile: env.certPath,
	}
	c.start()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	checker, err := openfga.New(ctx, openfga.Options{
		Endpoint: c.authzEndpoint, StoreID: env.storeID, ModelID: env.modelID,
		KeyFile: keyFile, CAFile: env.certPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := checker.Close(); err != nil {
			t.Errorf("close checker: %v", err)
		}
	})
	wantTuple := func(tuple authz.Tuple) {
		t.Helper()
		stored, err := checker.Read(ctx, tuple.Object)
		if err != nil || !slices.Contains(stored, tuple) {
			t.Fatalf("stored tuples = %v, %v, want %v", stored, err, tuple)
		}
	}
	useTenant := func(id string) {
		c.client.CloseIdleConnections()
		c.tenant = id
		c.client = authClient(filepath.Join(c.dir, "central-state", "tls.crt"), platformToken, id)
		t.Cleanup(c.client.CloseIdleConnections)
	}
	tenantService := identityv1connect.NewTenantServiceClient(c.client, c.baseURL())
	createTenant := func(organization, name string) *apiidentityv1.CreateTenantRequest {
		return apiidentityv1.CreateTenantRequest_builder{
			Issuer: new(iss.URL()), OrganizationClaimName: new("org_id"),
			OrganizationClaimValue: new(organization), Name: new(name),
		}.Build()
	}
	tenantRequest := createTenant("org-a", "Tenant A")
	created, err := tenantService.CreateTenant(ctx, connect.NewRequest(tenantRequest))
	if err != nil {
		t.Fatalf("CreateTenant A: %v", err)
	}
	tenantA := created.Msg.GetTenant().GetConfig().GetRef().GetTenant().GetId()
	createdB, err := tenantService.CreateTenant(ctx, connect.NewRequest(createTenant("org-b", "Tenant B")))
	if err != nil {
		t.Fatalf("CreateTenant B: %v", err)
	}
	tenantB := createdB.Msg.GetTenant().GetConfig().GetRef().GetTenant().GetId()
	if tenantA == "" || tenantB == "" || tenantA == tenantB {
		t.Fatalf("tenant IDs = %q, %q, want distinct UUIDs", tenantA, tenantB)
	}
	repeated, err := tenantService.CreateTenant(ctx, connect.NewRequest(tenantRequest))
	if err != nil || !proto.Equal(repeated.Msg.GetTenant(), created.Msg.GetTenant()) {
		t.Fatalf("CreateTenant retry = %v, %v, want the same record", repeated, err)
	}
	_, err = tenantService.CreateTenant(ctx, connect.NewRequest(createTenant("org-a", "Another name")))
	if connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Fatalf("CreateTenant conflicting name = %v, want AlreadyExists", err)
	}
	c.mu.Lock()
	hub := c.hub
	c.mu.Unlock()
	ts, err := tenantstore.New(ctx, hub.JetStream(), edgebus.TenantBucket)
	if err != nil {
		t.Fatal(err)
	}
	record, err := ts.LookupByOrg(ctx, iss.URL(), "org-a")
	if err != nil || !proto.Equal(record, created.Msg.GetTenant()) {
		t.Fatalf("LookupByOrg = %v, %v, want created tenant", record, err)
	}
	wantTuple(authz.Tuple{Object: "tenant:" + tenantA, Relation: "platform", User: "platform:flowseer"})
	wantTuple(authz.Tuple{Object: "platform:flowseer", Relation: "enrolled", User: "user:" + authn.ComputePrincipalID(iss.URL(), "platform-admin")})
	useTenant(tenantA)

	enroll := func(subject string) *identityv1.OperatorRef {
		t.Helper()
		member := identityv1.OperatorRef_builder{Issuer: new(iss.URL()), Subject: new(subject)}.Build()
		resp, err := c.tenantAdmin().EnrollMember(ctx, connect.NewRequest(apiidentityv1.EnrollMemberRequest_builder{Member: member}.Build()))
		if err != nil || !proto.Equal(resp.Msg.GetMember().GetOperator(), member) {
			t.Fatalf("EnrollMember %s = %v, %v", subject, resp, err)
		}
		return member
	}
	createRole := func(name string, relations ...identityv1.TenantRelation) *identityv1.RoleGlobalRef {
		t.Helper()
		resp, err := c.tenantAdmin().CreateRole(ctx, connect.NewRequest(apiidentityv1.CreateRoleRequest_builder{Name: new(name), Relations: relations}.Build()))
		if err != nil {
			t.Fatalf("CreateRole %s: %v", name, err)
		}
		return resp.Msg.GetRole().GetRef()
	}
	assign := func(member *identityv1.OperatorRef, role *identityv1.RoleGlobalRef) {
		t.Helper()
		resp, err := c.tenantAdmin().AssignRole(ctx, connect.NewRequest(apiidentityv1.AssignRoleRequest_builder{Member: member, Role: role}.Build()))
		if err != nil || !slices.ContainsFunc(resp.Msg.GetMember().GetRoles(), func(ref *identityv1.RoleGlobalRef) bool { return proto.Equal(ref, role) }) {
			t.Fatalf("AssignRole = %v, %v, want assigned role", resp, err)
		}
	}
	admin := enroll("admin-a")
	adminRole := createRole("admin", identityv1.TenantRelation_TENANT_RELATION_ADMIN)
	assign(admin, adminRole)
	adminClient := authClient(filepath.Join(dir, "central-state", "tls.crt"), token("admin-a", "org-a"), tenantA)
	t.Cleanup(adminClient.CloseIdleConnections)
	edgeAdmin := edgev1connect.NewEdgeAdminServiceClient(adminClient, c.baseURL())
	edge, err := edgeAdmin.CreateEdge(ctx, connect.NewRequest(apiedgev1.CreateEdgeRequest_builder{Name: new("real-edge-1")}.Build()))
	if err != nil {
		t.Fatalf("CreateEdge as admin role assignee: %v", err)
	}
	edgeRef := edge.Msg.GetEdge().GetConfig().GetRef()
	getRequest := apiedgev1.GetEdgeRequest_builder{Edge: edgeRef}.Build()
	gotEdge, err := edgeAdmin.GetEdge(ctx, connect.NewRequest(getRequest))
	if err != nil || gotEdge.Msg.GetEdge().GetConfig().GetName() != "real-edge-1" {
		t.Fatalf("GetEdge after CreateEdge = %v, %v, want real-edge-1", gotEdge, err)
	}

	alice := enroll("alice")
	operatorRole := createRole("operator", identityv1.TenantRelation_TENANT_RELATION_OPERATOR)
	assign(alice, operatorRole)
	aliceUser := "user:" + authn.ComputePrincipalID(iss.URL(), "alice")
	wantTuple(authz.Tuple{Object: "tenant:" + tenantA, Relation: "operator", User: "role:" + operatorRole.GetRole().GetId() + "#assignee"})
	wantTuple(authz.Tuple{Object: "role:" + operatorRole.GetRole().GetId(), Relation: "assignee", User: aliceUser})
	aliceClient := authClient(filepath.Join(dir, "central-state", "tls.crt"), token("alice", "org-a"), tenantA)
	t.Cleanup(aliceClient.CloseIdleConnections)
	aliceEdges := edgev1connect.NewEdgeAdminServiceClient(aliceClient, c.baseURL())
	if _, err := aliceEdges.GetEdge(ctx, connect.NewRequest(getRequest)); err != nil {
		t.Fatalf("GetEdge as operator role assignee: %v", err)
	}
	members, err := c.tenantAdmin().ListMembers(ctx, connect.NewRequest(&apiidentityv1.ListMembersRequest{}))
	if err != nil || !slices.ContainsFunc(members.Msg.GetMembers(), func(member *identityv1.Member) bool { return proto.Equal(member.GetOperator(), alice) }) {
		t.Fatalf("ListMembers = %v, %v, want alice before claim omission", members, err)
	}
	omittedClient := authClient(filepath.Join(dir, "central-state", "tls.crt"), token("alice", "org-b"), tenantA)
	t.Cleanup(omittedClient.CloseIdleConnections)
	_, err = edgev1connect.NewEdgeAdminServiceClient(omittedClient, c.baseURL()).GetEdge(ctx, connect.NewRequest(getRequest))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("GetEdge with token omitting tenant A = %v, want PermissionDenied", err)
	}
	_, err = identityv1connect.NewTenantServiceClient(aliceClient, c.baseURL()).CreateTenant(ctx, connect.NewRequest(createTenant("org-c", "Denied")))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("CreateTenant without platform claim = %v, want PermissionDenied", err)
	}
	_, err = c.tenantAdmin().DeleteRole(ctx, connect.NewRequest(apiidentityv1.DeleteRoleRequest_builder{Role: operatorRole}.Build()))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("DeleteRole while assigned = %v, want FailedPrecondition", err)
	}
	if _, err := c.tenantAdmin().UnassignRole(ctx, connect.NewRequest(apiidentityv1.UnassignRoleRequest_builder{Member: alice, Role: operatorRole}.Build())); err != nil {
		t.Fatal(err)
	}
	stored, err := checker.Read(ctx, "role:"+operatorRole.GetRole().GetId())
	if err != nil || slices.Contains(stored, authz.Tuple{Object: "role:" + operatorRole.GetRole().GetId(), Relation: "assignee", User: aliceUser}) {
		t.Fatalf("unassigned role tuples = %v, %v, want no alice assignee", stored, err)
	}
	_, err = aliceEdges.GetEdge(ctx, connect.NewRequest(getRequest))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("GetEdge after UnassignRole = %v, want PermissionDenied", err)
	}

	captureRequest := func(name string, fullPayload bool) *apicapturev1.CreateCaptureSessionRequest {
		return apicapturev1.CreateCaptureSessionRequest_builder{
			Edge: edgeRef, Name: new(name),
			Source: modelcapturev1.CaptureSource_builder{
				LocalInterface: modelcapturev1.LocalInterfaceSource_builder{InterfaceName: new("eth0")}.Build(),
			}.Build(),
			Budget:        modelcapturev1.CaptureBudget_builder{MaxPackets: new(uint64(10))}.Build(),
			Authorization: modelcapturev1.CaptureAuthorization_builder{Reason: new("real-engine test"), FullPayloadRequested: new(fullPayload)}.Build(),
		}.Build()
	}
	platformCapture := capturev1connect.NewCaptureServiceClient(c.client, c.baseURL())
	if _, err := c.admin().GetEdge(ctx, connect.NewRequest(getRequest)); err != nil {
		t.Fatalf("platform GetEdge without tenant enrollment: %v", err)
	}
	_, err = platformCapture.CreateCaptureSession(ctx, connect.NewRequest(captureRequest("full-payload", true)))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("platform full payload without grant = %v, want PermissionDenied", err)
	}
	platform := enroll("platform-admin")
	_, err = platformCapture.CreateCaptureSession(ctx, connect.NewRequest(captureRequest("full-payload", true)))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("platform full payload with enrollment alone = %v, want PermissionDenied", err)
	}
	grant, err := c.tenantAdmin().GrantFullPayload(ctx, connect.NewRequest(apiidentityv1.GrantFullPayloadRequest_builder{
		Member: platform, Lifetime: durationpb.New(time.Hour), Reason: new("case 42"),
	}.Build()))
	if err != nil || !grant.Msg.GetMember().GetFullPayload().GetExpiresAt().AsTime().After(time.Now()) {
		t.Fatalf("GrantFullPayload = %v, %v, want active grant", grant, err)
	}
	if _, err := platformCapture.CreateCaptureSession(ctx, connect.NewRequest(captureRequest("full-payload", true))); err != nil {
		t.Fatalf("platform full payload after grant: %v", err)
	}

	viewerRole := createRole("capture viewer", identityv1.TenantRelation_TENANT_RELATION_VIEWER, identityv1.TenantRelation_TENANT_RELATION_CAPTURER)
	assign(alice, viewerRole)
	aliceCapture := capturev1connect.NewCaptureServiceClient(aliceClient, c.baseURL())
	session, err := aliceCapture.CreateCaptureSession(ctx, connect.NewRequest(captureRequest("alice-session", false)))
	if err != nil {
		t.Fatalf("CreateCaptureSession as capturer role assignee: %v", err)
	}
	sessionID := session.Msg.GetSession().GetConfig().GetRef().GetCaptureSession().GetId()
	for _, tuple := range []authz.Tuple{
		{Object: "tenant:" + tenantA, Relation: "enrolled", User: aliceUser},
		{Object: "role:" + viewerRole.GetRole().GetId(), Relation: "assignee", User: aliceUser},
		{Object: "capture_session:" + sessionID, Relation: "requester", User: aliceUser},
	} {
		wantTuple(tuple)
	}
	listed, err := aliceCapture.ListCaptureSessions(ctx, connect.NewRequest(&apicapturev1.ListCaptureSessionsRequest{}))
	if err != nil || len(listed.Msg.GetSessions()) != 2 {
		t.Fatalf("ListCaptureSessions as tenant capturer = %v, %v, want both sessions", listed, err)
	}
	if _, err := aliceEdges.GetEdge(ctx, connect.NewRequest(getRequest)); err != nil {
		t.Fatalf("viewer GetEdge before removal: %v", err)
	}
	if _, err := c.tenantAdmin().RemoveMember(ctx, connect.NewRequest(apiidentityv1.RemoveMemberRequest_builder{Member: alice}.Build())); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	if err := checker.Scan(ctx, func(tuple authz.Tuple) error {
		if tuple.User == aliceUser {
			t.Errorf("tuple still names removed user: %v", tuple)
		}
		return nil
	}); err != nil {
		t.Fatalf("Scan after RemoveMember: %v", err)
	}
	_, err = aliceEdges.GetEdge(ctx, connect.NewRequest(getRequest))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("GetEdge after RemoveMember = %v, want PermissionDenied", err)
	}
	members, err = c.tenantAdmin().ListMembers(ctx, connect.NewRequest(&apiidentityv1.ListMembersRequest{}))
	if err != nil || slices.ContainsFunc(members.Msg.GetMembers(), func(member *identityv1.Member) bool { return proto.Equal(member.GetOperator(), alice) }) {
		t.Fatalf("ListMembers after removal = %v, %v, want no alice", members, err)
	}

	useTenant(tenantB)
	providerAdmin := enroll("provider-admin")
	providerRole := createRole("provider admin", identityv1.TenantRelation_TENANT_RELATION_ADMIN)
	assign(providerAdmin, providerRole)
	providerClient := authClient(filepath.Join(dir, "central-state", "tls.crt"), token("provider-admin", "org-b"), tenantA)
	t.Cleanup(providerClient.CloseIdleConnections)
	providerCapture := capturev1connect.NewCaptureServiceClient(providerClient, c.baseURL())
	_, err = providerCapture.CreateCaptureSession(ctx, connect.NewRequest(captureRequest("partner-session", false)))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("provider capture before connection = %v, want PermissionDenied", err)
	}
	useTenant(tenantA)
	partnerRef := createdB.Msg.GetTenant().GetConfig().GetRef()
	_, err = c.tenantAdmin().ConnectPartner(ctx, connect.NewRequest(apiidentityv1.ConnectPartnerRequest_builder{
		Partner: partnerRef, Relations: []identityv1.TenantRelation{identityv1.TenantRelation_TENANT_RELATION_CAPTURER},
	}.Build()))
	if err != nil {
		t.Fatalf("ConnectPartner: %v", err)
	}
	wantTuple(authz.Tuple{Object: "tenant:" + tenantA, Relation: "partner", User: "tenant:" + tenantB})
	wantTuple(authz.Tuple{Object: "tenant:" + tenantA, Relation: "capturer", User: "tenant:" + tenantB + "#active_admin"})
	if _, err := providerCapture.CreateCaptureSession(ctx, connect.NewRequest(captureRequest("partner-session", false))); err != nil {
		t.Fatalf("provider capture while connected: %v", err)
	}
	if _, err := c.tenantAdmin().DisconnectPartner(ctx, connect.NewRequest(apiidentityv1.DisconnectPartnerRequest_builder{Partner: partnerRef}.Build())); err != nil {
		t.Fatalf("DisconnectPartner: %v", err)
	}
	_, err = providerCapture.CreateCaptureSession(ctx, connect.NewRequest(captureRequest("disconnected-session", false)))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("provider capture after disconnection = %v, want PermissionDenied", err)
	}
}
