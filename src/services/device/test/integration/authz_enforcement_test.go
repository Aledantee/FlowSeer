//go:build authz_integration

package integration_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	apicapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1/capturev1connect"
	apiedgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1/edgev1connect"
	modelcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn/authntest"
	"go.aledante.io/FlowSeer/src/services/device/internal/tenantstore"
)

func TestEnforcementAgainstTheRealEngine(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	env := startOpenFGAEnv(t, nil)
	iss := authntest.New(t)
	keyFile := writeTempKeyFile(t, testPresharedKey)

	dir := t.TempDir()
	writeCredentials(t, filepath.Join(dir, "credentials"))
	regDir := writeRegistry(t, filepath.Join(dir, "registry.textproto"), "0192e6a0-0000-7000-8000-00000000dead", 0)

	c := &central{
		t:         t,
		dir:       dir,
		configDir: dir,
		registry:  regDir,
		apiPort:   freePort(t),
		busPort:   freePort(t),
		issuer:    iss,
		audience:  "flowseer-real-engine",
		platformAdmin: storev1.PlatformAdmin_builder{
			Issuer:                proto.String(iss.URL()),
			Organization:          proto.String("flowseer-platform"),
			Subjects:              []string{"platform-admin"},
			OrganizationClaimName: proto.String("org_id"),
		}.Build(),
		authzEndpoint: fmt.Sprintf("https://%s", env.endpoint),
		authzStoreID:  env.storeID,
		authzModelID:  env.modelID,
		authzKeyFile:  keyFile,
		authzCAFile:   env.certPath,
	}
	c.start()
	defer c.shutdown()

	c.mu.Lock()
	hub := c.hub
	c.mu.Unlock()
	if hub == nil {
		t.Fatal("centralHub is nil")
	}

	client := authClient(filepath.Join(c.dir, "central-state", "tls.crt"), "", "")
	t.Cleanup(client.CloseIdleConnections)

	edgeAdminClient := edgev1connect.NewEdgeAdminServiceClient(client, c.baseURL())
	captureClient := capturev1connect.NewCaptureServiceClient(client, c.baseURL())

	js := hub.JetStream()
	ts, err := tenantstore.New(ctx, js, edgebus.TenantBucket)
	if err != nil {
		t.Fatalf("tenantstore.New: %v", err)
	}

	const (
		tenantA = "0192e6a0-0000-7000-8000-00000000000a"
		tenantB = "0192e6a0-0000-7000-8000-00000000000b"
	)

	_, err = ts.Create(ctx, identityv1.TenantConfig_builder{
		Ref: identityv1.TenantGlobalRef_builder{
			Tenant: identityv1.TenantLocalRef_builder{Id: proto.String(tenantA)}.Build(),
		}.Build(),
		Issuer:                 proto.String(iss.URL()),
		OrganizationClaimName:  proto.String("org_id"),
		OrganizationClaimValue: proto.String("org-a"),
		Name:                   proto.String("Tenant A"),
	}.Build())
	if err != nil {
		t.Fatalf("Create tenant A: %v", err)
	}

	_, err = ts.Create(ctx, identityv1.TenantConfig_builder{
		Ref: identityv1.TenantGlobalRef_builder{
			Tenant: identityv1.TenantLocalRef_builder{Id: proto.String(tenantB)}.Build(),
		}.Build(),
		Issuer:                 proto.String(iss.URL()),
		OrganizationClaimName:  proto.String("org_id"),
		OrganizationClaimValue: proto.String("org-b"),
		Name:                   proto.String("Tenant B"),
	}.Build())
	if err != nil {
		t.Fatalf("Create tenant B: %v", err)
	}

	// Admin creates an edge and reads it back, which needs edge:<id>#tenant in the engine
	// and admin from tenant in the model.
	adminSub := "admin-a"
	adminID := authn.ComputePrincipalID(iss.URL(), adminSub)
	if err := env.WriteTuple(ctx, "user:"+adminID, "enrolled", "tenant:"+tenantA); err != nil {
		t.Fatalf("write enrolled: %v", err)
	}
	if err := env.WriteTuple(ctx, "user:"+adminID, "admin", "tenant:"+tenantA); err != nil {
		t.Fatalf("write admin: %v", err)
	}
	adminToken := iss.Sign(map[string]any{
		"iss":    iss.URL(),
		"sub":    adminSub,
		"aud":    "flowseer-real-engine",
		"org_id": "org-a",
		"exp":    time.Now().Add(time.Hour).Unix(),
	})

	createReq := connect.NewRequest(apiedgev1.CreateEdgeRequest_builder{
		Name: proto.String("real-edge-1"),
	}.Build())
	createReq.Header().Set("Authorization", "Bearer "+adminToken)
	createReq.Header().Set("X-FlowSeer-Tenant", tenantA)
	createResp, err := edgeAdminClient.CreateEdge(ctx, createReq)
	if err != nil {
		t.Fatalf("CreateEdge: %v", err)
	}
	edge1ID := createResp.Msg.GetEdge().GetConfig().GetRef().GetEdge().GetId()

	getReq := connect.NewRequest(apiedgev1.GetEdgeRequest_builder{
		Edge: edgev1.EdgeGlobalRef_builder{
			Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(edge1ID)}.Build(),
		}.Build(),
	}.Build())
	getReq.Header().Set("Authorization", "Bearer "+adminToken)
	getReq.Header().Set("X-FlowSeer-Tenant", tenantA)
	getResp, err := edgeAdminClient.GetEdge(ctx, getReq)
	if err != nil {
		t.Fatalf("GetEdge immediately after CreateEdge failed: %v", err)
	}
	if getResp.Msg.GetEdge().GetConfig().GetName() != "real-edge-1" {
		t.Fatalf("got edge name %q, want real-edge-1", getResp.Msg.GetEdge().GetConfig().GetName())
	}

	// A token whose organization claim omits tenant A gets CodePermissionDenied on GetEdge
	// in A while tenant:A#enrolled still holds.
	aliceSub := "alice"
	aliceID := authn.ComputePrincipalID(iss.URL(), aliceSub)
	if err := env.WriteTuple(ctx, "user:"+aliceID, "enrolled", "tenant:"+tenantA); err != nil {
		t.Fatalf("write alice enrolled: %v", err)
	}
	if err := env.WriteTuple(ctx, "user:"+aliceID, "admin", "tenant:"+tenantA); err != nil {
		t.Fatalf("write alice admin: %v", err)
	}
	// Alice has org_id: "org-b" (not tenant A's org-a)
	aliceToken := iss.Sign(map[string]any{
		"iss":    iss.URL(),
		"sub":    aliceSub,
		"aud":    "flowseer-real-engine",
		"org_id": "org-b",
		"exp":    time.Now().Add(time.Hour).Unix(),
	})

	aliceGetReq := connect.NewRequest(apiedgev1.GetEdgeRequest_builder{
		Edge: edgev1.EdgeGlobalRef_builder{
			Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(edge1ID)}.Build(),
		}.Build(),
	}.Build())
	aliceGetReq.Header().Set("Authorization", "Bearer "+aliceToken)
	aliceGetReq.Header().Set("X-FlowSeer-Tenant", tenantA)
	_, err = edgeAdminClient.GetEdge(ctx, aliceGetReq)
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Fatalf("alice GetEdge in tenant A: code = %v, want CodePermissionDenied (%v)", got, err)
	}

	// A platform admin gets GetEdge in a tenant it is not enrolled in, and CodePermissionDenied
	// on a full-payload CreateCaptureSession until tenant:A#full_payload is written.
	platSub := "platform-admin"
	platID := authn.ComputePrincipalID(iss.URL(), platSub)
	if err := env.WriteTuple(ctx, "platform:flowseer", "platform", "tenant:"+tenantA); err != nil {
		t.Fatalf("write platform relation: %v", err)
	}
	if err := env.WriteTuple(ctx, "user:"+platID, "enrolled", "platform:flowseer"); err != nil {
		t.Fatalf("write platform enrolled: %v", err)
	}
	platToken := iss.Sign(map[string]any{
		"iss":    iss.URL(),
		"sub":    platSub,
		"aud":    "flowseer-real-engine",
		"org_id": "flowseer-platform",
		"exp":    time.Now().Add(time.Hour).Unix(),
	})

	platGetReq := connect.NewRequest(apiedgev1.GetEdgeRequest_builder{
		Edge: edgev1.EdgeGlobalRef_builder{
			Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(edge1ID)}.Build(),
		}.Build(),
	}.Build())
	platGetReq.Header().Set("Authorization", "Bearer "+platToken)
	platGetReq.Header().Set("X-FlowSeer-Tenant", tenantA)
	platGetResp, err := edgeAdminClient.GetEdge(ctx, platGetReq)
	if err != nil {
		t.Fatalf("platform admin GetEdge failed: %v", err)
	}
	if platGetResp.Msg.GetEdge().GetConfig().GetRef().GetEdge().GetId() != edge1ID {
		t.Fatalf("platform admin got edge %v, want %v", platGetResp.Msg.GetEdge().GetConfig().GetRef().GetEdge().GetId(), edge1ID)
	}

	fullPayloadReq := connect.NewRequest(apicapturev1.CreateCaptureSessionRequest_builder{
		Edge: createResp.Msg.GetEdge().GetConfig().GetRef(),
		Name: proto.String("session-1"),
		Source: modelcapturev1.CaptureSource_builder{
			LocalInterface: modelcapturev1.LocalInterfaceSource_builder{InterfaceName: proto.String("eth0")}.Build(),
		}.Build(),
		Budget: modelcapturev1.CaptureBudget_builder{
			MaxPackets: proto.Uint64(10),
		}.Build(),
		Authorization: modelcapturev1.CaptureAuthorization_builder{
			Reason:               proto.String("full-payload-test"),
			FullPayloadRequested: proto.Bool(true),
		}.Build(),
	}.Build())
	fullPayloadReq.Header().Set("Authorization", "Bearer "+platToken)
	fullPayloadReq.Header().Set("X-FlowSeer-Tenant", tenantA)
	_, err = captureClient.CreateCaptureSession(ctx, fullPayloadReq)
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Fatalf("platform admin full payload without grant: code = %v, want CodePermissionDenied (%v)", got, err)
	}

	if err := env.WriteTuple(ctx, "user:"+platID, "full_payload", "tenant:"+tenantA); err != nil {
		t.Fatalf("write full_payload: %v", err)
	}

	fullPayloadResp, err := captureClient.CreateCaptureSession(ctx, fullPayloadReq)
	if err != nil {
		t.Fatalf("platform admin full payload with grant: %v", err)
	}
	session1ID := fullPayloadResp.Msg.GetSession().GetConfig().GetRef().GetCaptureSession().GetId()

	// 4. A member with edge:E1#capture and no tenant role lists only E1's sessions.
	createReq2 := connect.NewRequest(apiedgev1.CreateEdgeRequest_builder{
		Name: proto.String("real-edge-2"),
	}.Build())
	createReq2.Header().Set("Authorization", "Bearer "+adminToken)
	createReq2.Header().Set("X-FlowSeer-Tenant", tenantA)
	createResp2, err := edgeAdminClient.CreateEdge(ctx, createReq2)
	if err != nil {
		t.Fatalf("CreateEdge 2: %v", err)
	}

	sess2Req := connect.NewRequest(apicapturev1.CreateCaptureSessionRequest_builder{
		Edge: createResp2.Msg.GetEdge().GetConfig().GetRef(),
		Name: proto.String("session-2"),
		Source: modelcapturev1.CaptureSource_builder{
			LocalInterface: modelcapturev1.LocalInterfaceSource_builder{InterfaceName: proto.String("eth0")}.Build(),
		}.Build(),
		Budget: modelcapturev1.CaptureBudget_builder{
			MaxPackets: proto.Uint64(10),
		}.Build(),
		Authorization: modelcapturev1.CaptureAuthorization_builder{
			Reason:               proto.String("session-2-test"),
			FullPayloadRequested: proto.Bool(false),
		}.Build(),
	}.Build())
	sess2Req.Header().Set("Authorization", "Bearer "+adminToken)
	sess2Req.Header().Set("X-FlowSeer-Tenant", tenantA)
	sess2Resp, err := captureClient.CreateCaptureSession(ctx, sess2Req)
	if err != nil {
		t.Fatalf("CreateCaptureSession 2: %v", err)
	}
	session2ID := sess2Resp.Msg.GetSession().GetConfig().GetRef().GetCaptureSession().GetId()

	capturerSub := "capturer-e1"
	capturerID := authn.ComputePrincipalID(iss.URL(), capturerSub)
	if err := env.WriteTuple(ctx, "user:"+capturerID, "enrolled", "tenant:"+tenantA); err != nil {
		t.Fatalf("write capturer enrolled: %v", err)
	}
	if err := env.WriteTuple(ctx, "user:"+capturerID, "capture", "edge:"+edge1ID); err != nil {
		t.Fatalf("write capturer capture grant: %v", err)
	}
	capturerToken := iss.Sign(map[string]any{
		"iss":    iss.URL(),
		"sub":    capturerSub,
		"aud":    "flowseer-real-engine",
		"org_id": "org-a",
		"exp":    time.Now().Add(time.Hour).Unix(),
	})

	listReq := connect.NewRequest(&apicapturev1.ListCaptureSessionsRequest{})
	listReq.Header().Set("Authorization", "Bearer "+capturerToken)
	listReq.Header().Set("X-FlowSeer-Tenant", tenantA)
	listResp, err := captureClient.ListCaptureSessions(ctx, listReq)
	if err != nil {
		t.Fatalf("ListCaptureSessions: %v", err)
	}
	var gotS1, gotS2 bool
	for _, sess := range listResp.Msg.GetSessions() {
		sid := sess.GetConfig().GetRef().GetCaptureSession().GetId()
		if sid == session1ID {
			gotS1 = true
		}
		if sid == session2ID {
			gotS2 = true
		}
	}
	if !gotS1 {
		t.Errorf("ListCaptureSessions: expected session on E1 (%s) to be visible", session1ID)
	}
	if gotS2 {
		t.Errorf("ListCaptureSessions: session on E2 (%s) must NOT be visible to capturer with E1-only grant", session2ID)
	}

	// Positive control: admin lists both sessions
	adminListReq := connect.NewRequest(&apicapturev1.ListCaptureSessionsRequest{})
	adminListReq.Header().Set("Authorization", "Bearer "+adminToken)
	adminListReq.Header().Set("X-FlowSeer-Tenant", tenantA)
	adminListResp, err := captureClient.ListCaptureSessions(ctx, adminListReq)
	if err != nil {
		t.Fatalf("ListCaptureSessions as admin: %v", err)
	}
	var adminGotS1, adminGotS2 bool
	for _, sess := range adminListResp.Msg.GetSessions() {
		sid := sess.GetConfig().GetRef().GetCaptureSession().GetId()
		if sid == session1ID {
			adminGotS1 = true
		}
		if sid == session2ID {
			adminGotS2 = true
		}
	}
	if !adminGotS1 || !adminGotS2 {
		t.Errorf("ListCaptureSessions as admin: got s1=%v, s2=%v; want both visible", adminGotS1, adminGotS2)
	}
}
