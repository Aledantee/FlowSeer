package authz_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	capturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1/capturev1connect"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1/edgev1connect"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/identity/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/identity/v1/identityv1connect"
	attachv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/attach/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/attach/v1/attachv1connect"
	capturemodelv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	edgemodelv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	"go.aledante.io/FlowSeer/src/common/tenant"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz"
)

const (
	validTenantID   = "0192e6a0-0000-7000-8000-0000000000a1"
	validTenantID2  = "0192e6a0-0000-7000-8000-0000000000a2"
	testPrincipalID = "user-100"
)

// fakeChecker records queries and evaluates denial or failure hooks.
type fakeChecker struct {
	mu       sync.Mutex
	recorded []authz.Query
	batches  [][]authz.Query
	deny     func(q authz.Query) bool
	fail     func(q authz.Query) error
}

func (f *fakeChecker) Check(_ context.Context, q authz.Query) (bool, error) {
	f.mu.Lock()
	f.recorded = append(f.recorded, q)
	denyFn := f.deny
	failFn := f.fail
	f.mu.Unlock()

	if failFn != nil {
		if err := failFn(q); err != nil {
			return false, err
		}
	}
	if denyFn != nil && denyFn(q) {
		return false, nil
	}
	return true, nil
}

func (f *fakeChecker) BatchCheck(_ context.Context, queries []authz.Query) ([]bool, error) {
	f.mu.Lock()
	f.recorded = append(f.recorded, queries...)
	f.batches = append(f.batches, queries)
	denyFn := f.deny
	failFn := f.fail
	f.mu.Unlock()

	if failFn != nil {
		for _, q := range queries {
			if err := failFn(q); err != nil {
				return nil, err
			}
		}
	}

	results := make([]bool, len(queries))
	for i, q := range queries {
		if denyFn != nil && denyFn(q) {
			results[i] = false
		} else {
			results[i] = true
		}
	}
	return results, nil
}

func (f *fakeChecker) Recorded() []authz.Query {
	f.mu.Lock()
	defer f.mu.Unlock()
	copied := make([]authz.Query, len(f.recorded))
	copy(copied, f.recorded)
	return copied
}

func (f *fakeChecker) Batches() [][]authz.Query {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.batches
}

func (f *fakeChecker) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recorded = nil
	f.batches = nil
	f.deny = nil
	f.fail = nil
}

type testAuthnInterceptor struct {
	defaultPrincipal authn.Principal
}

func (t *testAuthnInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if req.Header().Get("X-Test-Omit-Principal") == "true" {
			return next(ctx, req)
		}
		p := t.defaultPrincipal
		if id := req.Header().Get("X-Test-Principal-ID"); id != "" {
			p.ID = id
		}
		if tenants := req.Header().Get("X-Test-Principal-Tenants"); tenants != "" {
			p.Tenants = strings.Split(tenants, ",")
		}
		if req.Header().Get("X-Test-Principal-Platform") == "true" {
			p.Platform = true
		}
		ctx = authn.NewContext(ctx, p)
		return next(ctx, req)
	}
}

func (t *testAuthnInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (t *testAuthnInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if conn.RequestHeader().Get("X-Test-Omit-Principal") == "true" {
			return next(ctx, conn)
		}
		p := t.defaultPrincipal
		ctx = authn.NewContext(ctx, p)
		return next(ctx, conn)
	}
}

func edgeRef(id string) *edgemodelv1.EdgeGlobalRef {
	return edgemodelv1.EdgeGlobalRef_builder{
		Edge: edgemodelv1.EdgeLocalRef_builder{
			Id: proto.String(id),
		}.Build(),
	}.Build()
}

func sessionRef(id string) *capturemodelv1.CaptureSessionGlobalRef {
	return capturemodelv1.CaptureSessionGlobalRef_builder{
		CaptureSession: capturemodelv1.CaptureSessionLocalRef_builder{
			Id: proto.String(id),
		}.Build(),
	}.Build()
}

// testHandlers manages test hooks for RPC handlers.
type testHandlers struct {
	capturev1connect.UnimplementedCaptureServiceHandler
	edgev1connect.UnimplementedEdgeAdminServiceHandler
	identityv1connect.UnimplementedTenantServiceHandler
	attachv1connect.UnimplementedEdgeServiceHandler

	createCaptureFn func(ctx context.Context, req *connect.Request[capturev1.CreateCaptureSessionRequest]) (*connect.Response[capturev1.CreateCaptureSessionResponse], error)
	listCaptureFn   func(ctx context.Context, req *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error)
	getCaptureFn    func(ctx context.Context, req *connect.Request[capturev1.GetCaptureSessionRequest]) (*connect.Response[capturev1.GetCaptureSessionResponse], error)

	createEdgeFn func(ctx context.Context, req *connect.Request[edgev1.CreateEdgeRequest]) (*connect.Response[edgev1.CreateEdgeResponse], error)
	getEdgeFn    func(ctx context.Context, req *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error)

	listTenantsFn func(ctx context.Context, req *connect.Request[identityv1.ListTenantsRequest]) (*connect.Response[identityv1.ListTenantsResponse], error)

	mu              sync.Mutex
	ranHandlers     map[string]bool
	admittedTenants map[string]string
}

func newTestHandlers() *testHandlers {
	return &testHandlers{
		ranHandlers:     make(map[string]bool),
		admittedTenants: make(map[string]string),
	}
}

func (h *testHandlers) markRan(ctx context.Context, name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ranHandlers[name] = true
	if t, err := tenant.FromContext(ctx); err == nil {
		h.admittedTenants[name] = t
	}
}

func (h *testHandlers) DidRun(name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ranHandlers[name]
}

func (h *testHandlers) AdmittedTenant(name string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.admittedTenants[name]
}

func (h *testHandlers) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ranHandlers = make(map[string]bool)
	h.admittedTenants = make(map[string]string)
	h.createCaptureFn = nil
	h.listCaptureFn = nil
	h.getCaptureFn = nil
	h.createEdgeFn = nil
	h.getEdgeFn = nil
	h.listTenantsFn = nil
}

func (h *testHandlers) CreateCaptureSession(ctx context.Context, req *connect.Request[capturev1.CreateCaptureSessionRequest]) (*connect.Response[capturev1.CreateCaptureSessionResponse], error) {
	h.markRan(ctx, "CreateCaptureSession")
	if h.createCaptureFn != nil {
		return h.createCaptureFn(ctx, req)
	}
	return connect.NewResponse(capturev1.CreateCaptureSessionResponse_builder{
		Session: capturemodelv1.CaptureSessionRecord_builder{}.Build(),
	}.Build()), nil
}

func (h *testHandlers) GetCaptureSession(ctx context.Context, req *connect.Request[capturev1.GetCaptureSessionRequest]) (*connect.Response[capturev1.GetCaptureSessionResponse], error) {
	h.markRan(ctx, "GetCaptureSession")
	if h.getCaptureFn != nil {
		return h.getCaptureFn(ctx, req)
	}
	return connect.NewResponse(capturev1.GetCaptureSessionResponse_builder{
		Session: capturemodelv1.CaptureSessionRecord_builder{}.Build(),
	}.Build()), nil
}

func (h *testHandlers) ListCaptureSessions(ctx context.Context, req *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
	h.markRan(ctx, "ListCaptureSessions")
	if h.listCaptureFn != nil {
		return h.listCaptureFn(ctx, req)
	}
	return connect.NewResponse(capturev1.ListCaptureSessionsResponse_builder{}.Build()), nil
}

func (h *testHandlers) DownloadCaptureSession(ctx context.Context, _ *connect.Request[capturev1.DownloadCaptureSessionRequest], _ *connect.ServerStream[capturev1.DownloadCaptureSessionResponse]) error {
	h.markRan(ctx, "DownloadCaptureSession")
	return nil
}

func (h *testHandlers) CreateEdge(ctx context.Context, req *connect.Request[edgev1.CreateEdgeRequest]) (*connect.Response[edgev1.CreateEdgeResponse], error) {
	h.markRan(ctx, "CreateEdge")
	if h.createEdgeFn != nil {
		return h.createEdgeFn(ctx, req)
	}
	return connect.NewResponse(edgev1.CreateEdgeResponse_builder{
		Edge: edgemodelv1.EdgeRecord_builder{}.Build(),
	}.Build()), nil
}

func (h *testHandlers) GetEdge(ctx context.Context, req *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error) {
	h.markRan(ctx, "GetEdge")
	if h.getEdgeFn != nil {
		return h.getEdgeFn(ctx, req)
	}
	return connect.NewResponse(edgev1.GetEdgeResponse_builder{
		Edge: edgemodelv1.EdgeRecord_builder{}.Build(),
	}.Build()), nil
}

func (h *testHandlers) ListTenants(ctx context.Context, req *connect.Request[identityv1.ListTenantsRequest]) (*connect.Response[identityv1.ListTenantsResponse], error) {
	h.markRan(ctx, "ListTenants")
	if h.listTenantsFn != nil {
		return h.listTenantsFn(ctx, req)
	}
	return connect.NewResponse(identityv1.ListTenantsResponse_builder{}.Build()), nil
}

type testEnv struct {
	server        *httptest.Server
	checker       *fakeChecker
	handlers      *testHandlers
	captureCli    capturev1connect.CaptureServiceClient
	edgeCli       edgev1connect.EdgeAdminServiceClient
	tenantCli     identityv1connect.TenantServiceClient
	edgeAttachCli attachv1connect.EdgeServiceClient
	interceptor   *authz.Interceptor
}

func setupTestEnv(t *testing.T) *testEnv {
	t.Helper()

	checker := &fakeChecker{}
	handlers := newTestHandlers()
	authzInterceptor := authz.NewInterceptor(checker)
	authnInterceptor := &testAuthnInterceptor{
		defaultPrincipal: authn.Principal{
			ID:       testPrincipalID,
			Tenants:  []string{validTenantID},
			Platform: false,
		},
	}

	opts := []connect.HandlerOption{
		connect.WithInterceptors(authnInterceptor, authzInterceptor),
	}

	mux := http.NewServeMux()
	mux.Handle(capturev1connect.NewCaptureServiceHandler(handlers, opts...))
	mux.Handle(edgev1connect.NewEdgeAdminServiceHandler(handlers, opts...))
	mux.Handle(identityv1connect.NewTenantServiceHandler(handlers, opts...))
	mux.Handle(attachv1connect.NewEdgeServiceHandler(handlers, opts...))

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := server.Client()
	return &testEnv{
		server:        server,
		checker:       checker,
		handlers:      handlers,
		captureCli:    capturev1connect.NewCaptureServiceClient(client, server.URL),
		edgeCli:       edgev1connect.NewEdgeAdminServiceClient(client, server.URL),
		tenantCli:     identityv1connect.NewTenantServiceClient(client, server.URL),
		edgeAttachCli: attachv1connect.NewEdgeServiceClient(client, server.URL),
		interceptor:   authzInterceptor,
	}
}

func TestRequirement4_NoPrincipalFailsUnauthenticated(t *testing.T) {
	env := setupTestEnv(t)

	req := connect.NewRequest(edgev1.GetEdgeRequest_builder{
		Edge: edgeRef("edge-1"),
	}.Build())
	req.Header().Set("X-FlowSeer-Tenant", validTenantID)
	req.Header().Set("X-Test-Omit-Principal", "true")

	_, err := env.edgeCli.GetEdge(context.Background(), req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeUnauthenticated)
	}
	if !strings.Contains(err.Error(), "authentication required") {
		t.Errorf("got error message %q, want containing 'authentication required'", err.Error())
	}
	if len(env.checker.Recorded()) != 0 {
		t.Errorf("got %d queries, want 0", len(env.checker.Recorded()))
	}
	if env.handlers.DidRun("GetEdge") {
		t.Error("handler ran, want not run")
	}
}

func TestRequirement5_TenantHeaderValidation(t *testing.T) {
	env := setupTestEnv(t)

	t.Run("call without X-FlowSeer-Tenant header fails InvalidArgument", func(t *testing.T) {
		env.checker.Reset()
		env.handlers.Reset()

		req := connect.NewRequest(edgev1.GetEdgeRequest_builder{
			Edge: edgeRef("edge-1"),
		}.Build())

		_, err := env.edgeCli.GetEdge(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeInvalidArgument)
		}
		if !strings.Contains(err.Error(), "no tenant named") {
			t.Errorf("got error message %q, want containing 'no tenant named'", err.Error())
		}
		if len(env.checker.Recorded()) != 0 {
			t.Errorf("got %d queries, want 0", len(env.checker.Recorded()))
		}
		if env.handlers.DidRun("GetEdge") {
			t.Error("handler ran, want not run")
		}
	})

	t.Run("call with Acme tenant header fails InvalidArgument", func(t *testing.T) {
		env.checker.Reset()
		env.handlers.Reset()

		req := connect.NewRequest(edgev1.GetEdgeRequest_builder{
			Edge: edgeRef("edge-1"),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", "Acme")

		_, err := env.edgeCli.GetEdge(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeInvalidArgument)
		}
		if !strings.Contains(err.Error(), "no tenant named") {
			t.Errorf("got error message %q, want containing 'no tenant named'", err.Error())
		}
		if len(env.checker.Recorded()) != 0 {
			t.Errorf("got %d queries, want 0", len(env.checker.Recorded()))
		}
		if env.handlers.DidRun("GetEdge") {
			t.Error("handler ran, want not run")
		}
	})

	t.Run("valid UUID tenant header reaches membership check", func(t *testing.T) {
		env.checker.Reset()
		env.handlers.Reset()

		req := connect.NewRequest(edgev1.GetEdgeRequest_builder{
			Edge: edgeRef("edge-1"),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.edgeCli.GetEdge(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		recorded := env.checker.Recorded()
		if len(recorded) == 0 {
			t.Fatal("expected queries, got 0")
		}
		membership := recorded[0]
		if membership.Object != "tenant:"+validTenantID || membership.Relation != "member" {
			t.Errorf("first query was %+v, want tenant:%s#member", membership, validTenantID)
		}
		if !env.handlers.DidRun("GetEdge") {
			t.Error("handler did not run")
		}
	})
}

func TestRequirement6_MembershipDenied(t *testing.T) {
	env := setupTestEnv(t)

	env.checker.deny = func(q authz.Query) bool {
		return q.Object == "tenant:"+validTenantID && q.Relation == "member"
	}

	req := connect.NewRequest(edgev1.GetEdgeRequest_builder{
		Edge: edgeRef("edge-denied"),
	}.Build())
	req.Header().Set("X-FlowSeer-Tenant", validTenantID)

	_, err := env.edgeCli.GetEdge(context.Background(), req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("got message %q, want containing 'permission denied'", err.Error())
	}
	recorded := env.checker.Recorded()
	if len(recorded) != 1 {
		t.Errorf("got %d queries, want 1", len(recorded))
	}
	if recorded[0].Object != "tenant:"+validTenantID || recorded[0].Relation != "member" {
		t.Errorf("recorded query was %+v, want tenant:%s#member", recorded[0], validTenantID)
	}
	if env.handlers.DidRun("GetEdge") {
		t.Error("handler ran, want not run")
	}
}

func TestRequirement7_RequestRuleRelationAndTenantChecks(t *testing.T) {
	sessionID := "session-77"

	t.Run("manage query denied", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.deny = func(q authz.Query) bool {
			return q.Object == "capture_session:"+sessionID && q.Relation == "manage"
		}

		req := connect.NewRequest(capturev1.GetCaptureSessionRequest_builder{
			Session: sessionRef(sessionID),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.captureCli.GetCaptureSession(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
		}
		if env.handlers.DidRun("GetCaptureSession") {
			t.Error("handler ran, want not run")
		}
		recorded := env.checker.Recorded()
		if len(recorded) != 3 {
			t.Fatalf("got %d queries, want 3 (1 membership + 2 batch)", len(recorded))
		}
		if recorded[1].Relation != "manage" || recorded[2].Relation != "tenant" {
			t.Errorf("batch queries were %v and %v, want manage and tenant", recorded[1], recorded[2])
		}
	})

	t.Run("tenant relation query denied", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.deny = func(q authz.Query) bool {
			return q.Object == "capture_session:"+sessionID && q.Relation == "tenant"
		}

		req := connect.NewRequest(capturev1.GetCaptureSessionRequest_builder{
			Session: sessionRef(sessionID),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.captureCli.GetCaptureSession(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
		}
		if env.handlers.DidRun("GetCaptureSession") {
			t.Error("handler ran, want not run")
		}
	})

	t.Run("both allowed", func(t *testing.T) {
		env := setupTestEnv(t)

		req := connect.NewRequest(capturev1.GetCaptureSessionRequest_builder{
			Session: sessionRef(sessionID),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.captureCli.GetCaptureSession(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !env.handlers.DidRun("GetCaptureSession") {
			t.Error("handler did not run")
		}
	})
}

func TestRequirement8_TenantRuleChecksRelation(t *testing.T) {
	t.Run("admin relation denied", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.deny = func(q authz.Query) bool {
			return q.Object == "tenant:"+validTenantID && q.Relation == "admin"
		}

		req := connect.NewRequest(edgev1.CreateEdgeRequest_builder{
			Name: proto.String("edge-alpha"),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.edgeCli.CreateEdge(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
		}
		if env.handlers.DidRun("CreateEdge") {
			t.Error("handler ran, want not run")
		}
		recorded := env.checker.Recorded()
		if len(recorded) != 2 {
			t.Fatalf("got %d queries, want 2 (member, admin)", len(recorded))
		}
		if recorded[1].Object != "tenant:"+validTenantID || recorded[1].Relation != "admin" {
			t.Errorf("second query was %+v, want tenant:%s#admin", recorded[1], validTenantID)
		}
	})

	t.Run("admin relation allowed", func(t *testing.T) {
		env := setupTestEnv(t)

		req := connect.NewRequest(edgev1.CreateEdgeRequest_builder{
			Name: proto.String("edge-alpha"),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.edgeCli.CreateEdge(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !env.handlers.DidRun("CreateEdge") {
			t.Error("handler did not run")
		}
	})
}

func TestRequirement9_ObligationAndDenialHandling(t *testing.T) {
	t.Run("ListCaptureSessions skips Filter gets Internal", func(t *testing.T) {
		env := setupTestEnv(t)
		// Default handler returns response without calling Filter
		req := connect.NewRequest(capturev1.ListCaptureSessionsRequest_builder{}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		resp, err := env.captureCli.ListCaptureSessions(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeInternal)
		}
		if resp != nil {
			t.Error("expected nil response on obligation failure")
		}
	})

	t.Run("CreateCaptureSession Require denied and ignored gets Internal", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.deny = func(q authz.Query) bool {
			return q.Object == "tenant:"+validTenantID && q.Relation == "full_payload"
		}

		env.handlers.createCaptureFn = func(ctx context.Context, _ *connect.Request[capturev1.CreateCaptureSessionRequest]) (*connect.Response[capturev1.CreateCaptureSessionResponse], error) {
			admitted, _ := tenant.FromContext(ctx)
			_ = authz.Require(ctx, "full_payload", "tenant", admitted)
			// Handler ignores denied Require and returns response
			return connect.NewResponse(capturev1.CreateCaptureSessionResponse_builder{
				Session: capturemodelv1.CaptureSessionRecord_builder{}.Build(),
			}.Build()), nil
		}

		req := connect.NewRequest(capturev1.CreateCaptureSessionRequest_builder{
			Edge: edgeRef("edge-1"),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		resp, err := env.captureCli.CreateCaptureSession(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeInternal)
		}
		if resp != nil {
			t.Error("expected nil response on ignored require denial")
		}
	})

	t.Run("CreateCaptureSession Require denied and returned gives PermissionDenied", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.deny = func(q authz.Query) bool {
			return q.Object == "tenant:"+validTenantID && q.Relation == "full_payload"
		}

		env.handlers.createCaptureFn = func(ctx context.Context, _ *connect.Request[capturev1.CreateCaptureSessionRequest]) (*connect.Response[capturev1.CreateCaptureSessionResponse], error) {
			admitted, _ := tenant.FromContext(ctx)
			if err := authz.Require(ctx, "full_payload", "tenant", admitted); err != nil {
				return nil, err
			}
			return connect.NewResponse(capturev1.CreateCaptureSessionResponse_builder{}.Build()), nil
		}

		req := connect.NewRequest(capturev1.CreateCaptureSessionRequest_builder{
			Edge: edgeRef("edge-1"),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.captureCli.CreateCaptureSession(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
		}
		// Confirm the one query tenant:<admitted>#full_payload was recorded
		recorded := env.checker.Recorded()
		var foundFullPayload bool
		for _, q := range recorded {
			if q.Object == "tenant:"+validTenantID && q.Relation == "full_payload" {
				foundFullPayload = true
			}
		}
		if !foundFullPayload {
			t.Error("expected query for tenant:validTenantID#full_payload")
		}
	})

	t.Run("CreateCaptureSession Require for other tenant denied with no query", func(t *testing.T) {
		env := setupTestEnv(t)
		otherTenantID := "0192e6a0-0000-7000-8000-000000000099"

		env.handlers.createCaptureFn = func(ctx context.Context, _ *connect.Request[capturev1.CreateCaptureSessionRequest]) (*connect.Response[capturev1.CreateCaptureSessionResponse], error) {
			if err := authz.Require(ctx, "full_payload", "tenant", otherTenantID); err != nil {
				return nil, err
			}
			return connect.NewResponse(capturev1.CreateCaptureSessionResponse_builder{}.Build()), nil
		}

		req := connect.NewRequest(capturev1.CreateCaptureSessionRequest_builder{
			Edge: edgeRef("edge-1"),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.captureCli.CreateCaptureSession(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
		}

		for _, q := range env.checker.Recorded() {
			if strings.Contains(q.Object, otherTenantID) {
				t.Errorf("unexpected query for other tenant: %+v", q)
			}
		}
	})

	t.Run("ListCaptureSessions calling Filter passes response through", func(t *testing.T) {
		env := setupTestEnv(t)
		env.handlers.listCaptureFn = func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
			_, err := authz.Filter(ctx, "capture", "edge", []string{"edge-1", "edge-2"})
			if err != nil {
				return nil, err
			}
			return connect.NewResponse(capturev1.ListCaptureSessionsResponse_builder{}.Build()), nil
		}

		req := connect.NewRequest(capturev1.ListCaptureSessionsRequest_builder{}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		resp, err := env.captureCli.ListCaptureSessions(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp == nil {
			t.Fatal("expected response, got nil")
		}
	})

	t.Run("ListCaptureSessions calling Filter passes error through", func(t *testing.T) {
		env := setupTestEnv(t)
		env.handlers.listCaptureFn = func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
			_, _ = authz.Filter(ctx, "capture", "edge", []string{"edge-1"})
			return nil, connect.NewError(connect.CodeNotFound, errors.New("resource missing"))
		}

		req := connect.NewRequest(capturev1.ListCaptureSessionsRequest_builder{}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.captureCli.ListCaptureSessions(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeNotFound)
		}
	})
}

func TestRequirement10_CheckerErrorBecomesUnavailable(t *testing.T) {
	env := setupTestEnv(t)
	env.checker.fail = func(_ authz.Query) error {
		return errors.New("storage engine timeout")
	}

	req := connect.NewRequest(edgev1.GetEdgeRequest_builder{
		Edge: edgeRef("edge-timeout"),
	}.Build())
	req.Header().Set("X-FlowSeer-Tenant", validTenantID)

	_, err := env.edgeCli.GetEdge(context.Background(), req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeUnavailable)
	}
	if !strings.Contains(err.Error(), "authorization is unavailable") {
		t.Errorf("got message %q, want containing 'authorization is unavailable'", err.Error())
	}
	if env.handlers.DidRun("GetEdge") {
		t.Error("handler ran, want not run")
	}
}

func TestRequirement11_ContextualTuples(t *testing.T) {
	env := setupTestEnv(t)

	req := connect.NewRequest(edgev1.GetEdgeRequest_builder{
		Edge: edgeRef("edge-1"),
	}.Build())
	req.Header().Set("X-FlowSeer-Tenant", validTenantID)
	req.Header().Set("X-Test-Principal-Tenants", validTenantID+","+validTenantID2)
	req.Header().Set("X-Test-Principal-Platform", "true")

	_, err := env.edgeCli.GetEdge(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	recorded := env.checker.Recorded()
	if len(recorded) == 0 {
		t.Fatal("expected recorded queries, got 0")
	}

	firstQuery := recorded[0]
	wantTuples := []authz.Tuple{
		{
			Object:   "tenant:" + validTenantID,
			Relation: "claimed",
			User:     "user:" + testPrincipalID,
		},
		{
			Object:   "tenant:" + validTenantID2,
			Relation: "claimed",
			User:     "user:" + testPrincipalID,
		},
		{
			Object:   "platform:flowseer",
			Relation: "claimed",
			User:     "user:" + testPrincipalID,
		},
	}

	if len(firstQuery.ContextualTuples) != len(wantTuples) {
		t.Fatalf("got %d contextual tuples, want %d", len(firstQuery.ContextualTuples), len(wantTuples))
	}
	for _, want := range wantTuples {
		if !slices.Contains(firstQuery.ContextualTuples, want) {
			t.Errorf("tuples %v do not contain %+v", firstQuery.ContextualTuples, want)
		}
	}
}

func TestRequirement12_StreamingCallRefused(t *testing.T) {
	env := setupTestEnv(t)

	req := connect.NewRequest(capturev1.DownloadCaptureSessionRequest_builder{
		Session: sessionRef("session-1"),
	}.Build())
	req.Header().Set("X-FlowSeer-Tenant", validTenantID)

	stream, err := env.captureCli.DownloadCaptureSession(context.Background(), req)
	if err == nil {
		if stream.Receive() {
			t.Fatal("expected stream to fail immediately, but received message")
		}
		err = stream.Err()
	}

	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("got message %q, want containing 'permission denied'", err.Error())
	}
	if env.handlers.DidRun("DownloadCaptureSession") {
		t.Error("streaming handler ran, want not run")
	}
}

func TestRequirement13_UnsupportedOrMissingRule(t *testing.T) {
	env := setupTestEnv(t)

	req := connect.NewRequest(attachv1.HeartbeatRequest_builder{}.Build())
	req.Header().Set("X-FlowSeer-Tenant", validTenantID)

	_, err := env.edgeAttachCli.Heartbeat(context.Background(), req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("got message %q, want containing 'permission denied'", err.Error())
	}
	if len(env.checker.Recorded()) != 0 {
		t.Errorf("got %d queries, want 0", len(env.checker.Recorded()))
	}
}

func TestRequirement14_PlatformRule(t *testing.T) {
	t.Run("false answer is PermissionDenied and handler does not run", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.deny = func(q authz.Query) bool {
			return q.Object == "platform:flowseer" && q.Relation == "admin"
		}

		// ListTenants without X-FlowSeer-Tenant header
		req := connect.NewRequest(identityv1.ListTenantsRequest_builder{}.Build())

		_, err := env.tenantCli.ListTenants(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
		}
		recorded := env.checker.Recorded()
		if len(recorded) != 1 {
			t.Fatalf("got %d queries, want exactly 1", len(recorded))
		}
		wantQuery := authz.Query{
			Object:   "platform:flowseer",
			Relation: "admin",
			User:     "user:" + testPrincipalID,
		}
		if recorded[0].Object != wantQuery.Object || recorded[0].Relation != wantQuery.Relation || recorded[0].User != wantQuery.User {
			t.Errorf("got query %+v, want %+v", recorded[0], wantQuery)
		}
		if env.handlers.DidRun("ListTenants") {
			t.Error("handler ran, want not run")
		}
	})

	t.Run("true answer runs handler with no tenant in context and Require returns Internal", func(t *testing.T) {
		env := setupTestEnv(t)

		env.handlers.listTenantsFn = func(ctx context.Context, _ *connect.Request[identityv1.ListTenantsRequest]) (*connect.Response[identityv1.ListTenantsResponse], error) {
			if _, err := tenant.FromContext(ctx); err == nil {
				t.Error("tenant found in context under platform rule, want no tenant")
			}
			if err := authz.Require(ctx, "admin", "tenant", validTenantID); err == nil {
				t.Error("Require under platform rule returned nil, want Internal")
			} else if connect.CodeOf(err) != connect.CodeInternal {
				t.Errorf("Require under platform rule returned %v, want CodeInternal", connect.CodeOf(err))
			}
			return connect.NewResponse(identityv1.ListTenantsResponse_builder{}.Build()), nil
		}

		req := connect.NewRequest(identityv1.ListTenantsRequest_builder{}.Build())

		resp, err := env.tenantCli.ListTenants(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp == nil {
			t.Fatal("expected response, got nil")
		}
		if !env.handlers.DidRun("ListTenants") {
			t.Error("handler did not run")
		}
	})
}

func TestRequirement15_RequestRuleNoIDRefused(t *testing.T) {
	env := setupTestEnv(t)

	// GetEdgeRequest with no edge set
	req := connect.NewRequest(edgev1.GetEdgeRequest_builder{}.Build())
	req.Header().Set("X-FlowSeer-Tenant", validTenantID)

	_, err := env.edgeCli.GetEdge(context.Background(), req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
	}

	recorded := env.checker.Recorded()
	// Only membership check should have run, no object check!
	for _, q := range recorded {
		if strings.HasPrefix(q.Object, "edge:") {
			t.Errorf("unexpected object check on edge: %+v", q)
		}
	}
	if env.handlers.DidRun("GetEdge") {
		t.Error("handler ran, want not run")
	}
}

func TestAdmittedTenantInContext(t *testing.T) {
	env := setupTestEnv(t)

	req := connect.NewRequest(edgev1.GetEdgeRequest_builder{
		Edge: edgeRef("edge-1"),
	}.Build())
	req.Header().Set("X-FlowSeer-Tenant", validTenantID)

	_, err := env.edgeCli.GetEdge(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if env.handlers.AdmittedTenant("GetEdge") != validTenantID {
		t.Errorf("admitted tenant was %q, want %q", env.handlers.AdmittedTenant("GetEdge"), validTenantID)
	}
}

func TestFilterBatchingAndDeduplication(t *testing.T) {
	env := setupTestEnv(t)

	// 60 IDs with 1 duplicate -> 59 unique IDs
	ids := make([]string, 60)
	for i := range 59 {
		ids[i] = fmt.Sprintf("edge-%02d", i+1)
	}
	ids[59] = "edge-01" // Duplicate of first

	var filteredIDs []string
	env.handlers.listCaptureFn = func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
		var err error
		filteredIDs, err = authz.Filter(ctx, "capture", "edge", ids)
		if err != nil {
			return nil, err
		}
		return connect.NewResponse(capturev1.ListCaptureSessionsResponse_builder{}.Build()), nil
	}

	req := connect.NewRequest(capturev1.ListCaptureSessionsRequest_builder{}.Build())
	req.Header().Set("X-FlowSeer-Tenant", validTenantID)

	_, err := env.captureCli.ListCaptureSessions(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 59 unique IDs * 2 queries per ID = 118 queries
	// Batched into 50, 50, 18
	batches := env.checker.Batches()
	if len(batches) != 3 {
		t.Fatalf("got %d batches, want 3", len(batches))
	}
	if len(batches[0]) != 50 {
		t.Errorf("batch 0 has %d queries, want 50", len(batches[0]))
	}
	if len(batches[1]) != 50 {
		t.Errorf("batch 1 has %d queries, want 50", len(batches[1]))
	}
	if len(batches[2]) != 18 {
		t.Errorf("batch 2 has %d queries, want 18", len(batches[2]))
	}

	if len(filteredIDs) != 59 {
		t.Errorf("got %d filtered IDs, want 59", len(filteredIDs))
	}
}
