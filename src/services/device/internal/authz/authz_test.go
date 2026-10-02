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

	"buf.build/go/protovalidate"
	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	capturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1/capturev1connect"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1/edgev1connect"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/identity/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/identity/v1/identityv1connect"
	authzv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/authz/v1"
	attachv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/attach/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/attach/v1/attachv1connect"
	errsv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/errs/v1"
	capturemodelv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	edgemodelv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/tenant"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz"
)

const (
	validTenantID   = "0192e6a0-0000-7000-8000-0000000000a1"
	validTenantID2  = "0192e6a0-0000-7000-8000-0000000000a2"
	testPrincipalID = "user-100"
	validEdgeID     = "0192e6a0-0000-7000-8000-0000000000e1"
	validEdgeID2    = "0192e6a0-0000-7000-8000-0000000000e2"
	validSessionID  = "0192e6a0-0000-7000-8000-0000000000c1"
)

func testUUID(prefix byte, n int) string {
	return fmt.Sprintf("0192e6a0-0000-7000-8000-%c%011x", prefix, n)
}

type fakeChecker struct {
	mu           sync.Mutex
	recorded     []authz.Query
	batches      [][]authz.Query
	deny         func(q authz.Query) bool
	fail         func(q authz.Query) error
	batchAnswers func(queries []authz.Query) []bool
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
	batchAnswersFn := f.batchAnswers
	f.mu.Unlock()

	if failFn != nil {
		for _, q := range queries {
			if err := failFn(q); err != nil {
				return nil, err
			}
		}
	}

	if batchAnswersFn != nil {
		return batchAnswersFn(queries), nil
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
	copied := make([][]authz.Query, len(f.batches))
	for i, b := range f.batches {
		copied[i] = append([]authz.Query(nil), b...)
	}
	return copied
}

func (f *fakeChecker) SetDeny(fn func(q authz.Query) bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deny = fn
}

func (f *fakeChecker) SetFail(fn func(q authz.Query) error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = fn
}

func (f *fakeChecker) SetBatchAnswers(fn func(queries []authz.Query) []bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.batchAnswers = fn
}

func (f *fakeChecker) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recorded = nil
	f.batches = nil
	f.deny = nil
	f.fail = nil
	f.batchAnswers = nil
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

func edgeRef(t *testing.T, id string) *edgemodelv1.EdgeGlobalRef {
	t.Helper()
	ref := edgemodelv1.EdgeGlobalRef_builder{
		Edge: edgemodelv1.EdgeLocalRef_builder{
			Id: proto.String(id),
		}.Build(),
	}.Build()
	if err := protovalidate.Validate(ref); err != nil {
		t.Fatalf("edgeRef(%q) fails protovalidate: %v", id, err)
	}
	return ref
}

func sessionRef(t *testing.T, sessionID string) *capturemodelv1.CaptureSessionGlobalRef {
	t.Helper()
	ref := capturemodelv1.CaptureSessionGlobalRef_builder{
		Edge: edgeRef(t, validEdgeID),
		CaptureSession: capturemodelv1.CaptureSessionLocalRef_builder{
			Id: proto.String(sessionID),
		}.Build(),
	}.Build()
	if err := protovalidate.Validate(ref); err != nil {
		t.Fatalf("sessionRef(%q) fails protovalidate: %v", sessionID, err)
	}
	return ref
}

func errCodeOf(t *testing.T, err error) string {
	t.Helper()
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("expected *connect.Error, got %T: %v", err, err)
	}
	for _, detail := range connectErr.Details() {
		msg, valErr := detail.Value()
		if valErr != nil {
			continue
		}
		if payload, ok := msg.(*errsv1.ErrorPayload); ok {
			return payload.GetCode()
		}
	}
	if code, ok := errs.CodeOf(err); ok {
		return code.String()
	}
	t.Fatalf("no error code found on error: %v", err)
	return ""
}

func retryDispositionOf(t *testing.T, err error) errsv1.RetryDisposition {
	t.Helper()
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("expected *connect.Error, got %T: %v", err, err)
	}
	for _, detail := range connectErr.Details() {
		msg, valErr := detail.Value()
		if valErr != nil {
			continue
		}
		if payload, ok := msg.(*errsv1.ErrorPayload); ok {
			return payload.GetRetry()
		}
	}
	t.Fatalf("no retry disposition found on error: %v", err)
	return errsv1.RetryDisposition_RETRY_DISPOSITION_UNSPECIFIED
}

type testHandlers struct {
	capturev1connect.UnimplementedCaptureServiceHandler
	edgev1connect.UnimplementedEdgeAdminServiceHandler
	identityv1connect.UnimplementedTenantServiceHandler
	attachv1connect.UnimplementedEdgeServiceHandler

	mu              sync.Mutex
	createCaptureFn func(ctx context.Context, req *connect.Request[capturev1.CreateCaptureSessionRequest]) (*connect.Response[capturev1.CreateCaptureSessionResponse], error)
	listCaptureFn   func(ctx context.Context, req *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error)
	getCaptureFn    func(ctx context.Context, req *connect.Request[capturev1.GetCaptureSessionRequest]) (*connect.Response[capturev1.GetCaptureSessionResponse], error)
	createEdgeFn    func(ctx context.Context, req *connect.Request[edgev1.CreateEdgeRequest]) (*connect.Response[edgev1.CreateEdgeResponse], error)
	getEdgeFn       func(ctx context.Context, req *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error)
	listTenantsFn   func(ctx context.Context, req *connect.Request[identityv1.ListTenantsRequest]) (*connect.Response[identityv1.ListTenantsResponse], error)
	ranHandlers     map[string]bool
	admittedTenants map[string]string
}

func newTestHandlers() *testHandlers {
	return &testHandlers{
		ranHandlers:     make(map[string]bool),
		admittedTenants: make(map[string]string),
	}
}

func (h *testHandlers) SetCreateCaptureFn(fn func(ctx context.Context, req *connect.Request[capturev1.CreateCaptureSessionRequest]) (*connect.Response[capturev1.CreateCaptureSessionResponse], error)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.createCaptureFn = fn
}

func (h *testHandlers) SetListCaptureFn(fn func(ctx context.Context, req *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.listCaptureFn = fn
}

func (h *testHandlers) SetGetCaptureFn(fn func(ctx context.Context, req *connect.Request[capturev1.GetCaptureSessionRequest]) (*connect.Response[capturev1.GetCaptureSessionResponse], error)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.getCaptureFn = fn
}

func (h *testHandlers) SetCreateEdgeFn(fn func(ctx context.Context, req *connect.Request[edgev1.CreateEdgeRequest]) (*connect.Response[edgev1.CreateEdgeResponse], error)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.createEdgeFn = fn
}

func (h *testHandlers) SetGetEdgeFn(fn func(ctx context.Context, req *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.getEdgeFn = fn
}

func (h *testHandlers) SetListTenantsFn(fn func(ctx context.Context, req *connect.Request[identityv1.ListTenantsRequest]) (*connect.Response[identityv1.ListTenantsResponse], error)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.listTenantsFn = fn
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
	h.mu.Lock()
	fn := h.createCaptureFn
	h.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	return connect.NewResponse(capturev1.CreateCaptureSessionResponse_builder{
		Session: capturemodelv1.CaptureSessionRecord_builder{}.Build(),
	}.Build()), nil
}

func (h *testHandlers) GetCaptureSession(ctx context.Context, req *connect.Request[capturev1.GetCaptureSessionRequest]) (*connect.Response[capturev1.GetCaptureSessionResponse], error) {
	h.markRan(ctx, "GetCaptureSession")
	h.mu.Lock()
	fn := h.getCaptureFn
	h.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	return connect.NewResponse(capturev1.GetCaptureSessionResponse_builder{
		Session: capturemodelv1.CaptureSessionRecord_builder{}.Build(),
	}.Build()), nil
}

func (h *testHandlers) ListCaptureSessions(ctx context.Context, req *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
	h.markRan(ctx, "ListCaptureSessions")
	h.mu.Lock()
	fn := h.listCaptureFn
	h.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	return connect.NewResponse(capturev1.ListCaptureSessionsResponse_builder{}.Build()), nil
}

func (h *testHandlers) DownloadCaptureSession(ctx context.Context, _ *connect.Request[capturev1.DownloadCaptureSessionRequest], _ *connect.ServerStream[capturev1.DownloadCaptureSessionResponse]) error {
	h.markRan(ctx, "DownloadCaptureSession")
	return nil
}

func (h *testHandlers) CreateEdge(ctx context.Context, req *connect.Request[edgev1.CreateEdgeRequest]) (*connect.Response[edgev1.CreateEdgeResponse], error) {
	h.markRan(ctx, "CreateEdge")
	h.mu.Lock()
	fn := h.createEdgeFn
	h.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	return connect.NewResponse(edgev1.CreateEdgeResponse_builder{
		Edge: edgemodelv1.EdgeRecord_builder{}.Build(),
	}.Build()), nil
}

func (h *testHandlers) GetEdge(ctx context.Context, req *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error) {
	h.markRan(ctx, "GetEdge")
	h.mu.Lock()
	fn := h.getEdgeFn
	h.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	return connect.NewResponse(edgev1.GetEdgeResponse_builder{
		Edge: edgemodelv1.EdgeRecord_builder{}.Build(),
	}.Build()), nil
}

func (h *testHandlers) ListTenants(ctx context.Context, req *connect.Request[identityv1.ListTenantsRequest]) (*connect.Response[identityv1.ListTenantsResponse], error) {
	h.markRan(ctx, "ListTenants")
	h.mu.Lock()
	fn := h.listTenantsFn
	h.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	return connect.NewResponse(identityv1.ListTenantsResponse_builder{}.Build()), nil
}

func buildSyntheticMethod(t *testing.T, name string, rule *authzv1.Rule) protoreflect.MethodDescriptor {
	t.Helper()
	m := &descriptorpb.MethodDescriptorProto{
		Name:       proto.String(name),
		InputType:  proto.String(".flowseer.api.edge.v1.GetEdgeRequest"),
		OutputType: proto.String(".flowseer.api.edge.v1.GetEdgeResponse"),
	}
	if rule != nil {
		opts := &descriptorpb.MethodOptions{}
		proto.SetExtension(opts, authzv1.E_Rule, rule)
		m.Options = opts
	}
	fdp := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("flowseer/conformance/synthetic/v1/" + name + ".proto"),
		Package: proto.String("flowseer.conformance.synthetic.v1"),
		Syntax:  proto.String("proto3"),
		Dependency: []string{
			"flowseer/authz/v1/rule.proto",
			"flowseer/api/edge/v1/edge_admin_service.proto",
		},
		Service: []*descriptorpb.ServiceDescriptorProto{
			{
				Name:   proto.String("SyntheticService"),
				Method: []*descriptorpb.MethodDescriptorProto{m},
			},
		},
	}
	file, err := protodesc.NewFile(fdp, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatalf("build synthetic method %s: %v", name, err)
	}
	return file.Services().Get(0).Methods().Get(0)
}

type testEnv struct {
	server         *httptest.Server
	checker        *fakeChecker
	handlers       *testHandlers
	captureCli     capturev1connect.CaptureServiceClient
	edgeCli        edgev1connect.EdgeAdminServiceClient
	tenantCli      identityv1connect.TenantServiceClient
	edgeAttachCli  attachv1connect.EdgeServiceClient
	interceptor    *authz.Interceptor
	unspecifiedCli *connect.Client[edgev1.GetEdgeRequest, edgev1.GetEdgeResponse]
	mode99Cli      *connect.Client[edgev1.GetEdgeRequest, edgev1.GetEdgeResponse]
	loadedCli      *connect.Client[edgev1.GetEdgeRequest, edgev1.GetEdgeResponse]
	noRuleCli      *connect.Client[edgev1.GetEdgeRequest, edgev1.GetEdgeResponse]
	setLoadedFn    func(fn func(ctx context.Context, req *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error))
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

	unspecifiedMD := buildSyntheticMethod(t, "UnspecifiedModeMethod", authzv1.Rule_builder{
		Mode:       authzv1.RuleMode_RULE_MODE_UNSPECIFIED.Enum(),
		ObjectType: proto.String("tenant"),
		Relation:   proto.String("admin"),
	}.Build())
	unspecifiedProc := "/" + string(unspecifiedMD.Parent().FullName()) + "/" + string(unspecifiedMD.Name())
	mux.Handle(unspecifiedProc, connect.NewUnaryHandler(
		unspecifiedProc,
		func(_ context.Context, _ *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error) {
			return connect.NewResponse(edgev1.GetEdgeResponse_builder{}.Build()), nil
		},
		connect.WithSchema(unspecifiedMD),
		connect.WithInterceptors(authnInterceptor, authzInterceptor),
	))

	mode99MD := buildSyntheticMethod(t, "Mode99Method", authzv1.Rule_builder{
		Mode:       authzv1.RuleMode(99).Enum(),
		ObjectType: proto.String("tenant"),
		Relation:   proto.String("admin"),
	}.Build())
	mode99Proc := "/" + string(mode99MD.Parent().FullName()) + "/" + string(mode99MD.Name())
	mux.Handle(mode99Proc, connect.NewUnaryHandler(
		mode99Proc,
		func(_ context.Context, _ *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error) {
			return connect.NewResponse(edgev1.GetEdgeResponse_builder{}.Build()), nil
		},
		connect.WithSchema(mode99MD),
		connect.WithInterceptors(authnInterceptor, authzInterceptor),
	))

	loadedMD := buildSyntheticMethod(t, "LoadedModeMethod", authzv1.Rule_builder{
		Mode:       authzv1.RuleMode_RULE_MODE_LOADED.Enum(),
		ObjectType: proto.String("edge"),
		Relation:   proto.String("view"),
	}.Build())
	loadedProc := "/" + string(loadedMD.Parent().FullName()) + "/" + string(loadedMD.Name())
	var loadedMu sync.Mutex
	var currentLoadedFn func(ctx context.Context, req *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error)
	mux.Handle(loadedProc, connect.NewUnaryHandler(
		loadedProc,
		func(ctx context.Context, req *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error) {
			loadedMu.Lock()
			fn := currentLoadedFn
			loadedMu.Unlock()
			if fn != nil {
				return fn(ctx, req)
			}
			return connect.NewResponse(edgev1.GetEdgeResponse_builder{}.Build()), nil
		},
		connect.WithSchema(loadedMD),
		connect.WithInterceptors(authnInterceptor, authzInterceptor),
	))

	noRuleMD := buildSyntheticMethod(t, "NoRuleMethod", nil)
	noRuleProc := "/" + string(noRuleMD.Parent().FullName()) + "/" + string(noRuleMD.Name())
	mux.Handle(noRuleProc, connect.NewUnaryHandler(
		noRuleProc,
		func(_ context.Context, _ *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error) {
			return connect.NewResponse(edgev1.GetEdgeResponse_builder{}.Build()), nil
		},
		connect.WithSchema(noRuleMD),
		connect.WithInterceptors(authnInterceptor, authzInterceptor),
	))

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := server.Client()
	return &testEnv{
		server:         server,
		checker:        checker,
		handlers:       handlers,
		captureCli:     capturev1connect.NewCaptureServiceClient(client, server.URL),
		edgeCli:        edgev1connect.NewEdgeAdminServiceClient(client, server.URL),
		tenantCli:      identityv1connect.NewTenantServiceClient(client, server.URL),
		edgeAttachCli:  attachv1connect.NewEdgeServiceClient(client, server.URL),
		interceptor:    authzInterceptor,
		unspecifiedCli: connect.NewClient[edgev1.GetEdgeRequest, edgev1.GetEdgeResponse](client, server.URL+unspecifiedProc),
		mode99Cli:      connect.NewClient[edgev1.GetEdgeRequest, edgev1.GetEdgeResponse](client, server.URL+mode99Proc),
		loadedCli:      connect.NewClient[edgev1.GetEdgeRequest, edgev1.GetEdgeResponse](client, server.URL+loadedProc),
		noRuleCli:      connect.NewClient[edgev1.GetEdgeRequest, edgev1.GetEdgeResponse](client, server.URL+noRuleProc),
		setLoadedFn: func(fn func(ctx context.Context, req *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error)) {
			loadedMu.Lock()
			currentLoadedFn = fn
			loadedMu.Unlock()
		},
	}
}

func TestNoPrincipalIsUnauthenticated(t *testing.T) {
	env := setupTestEnv(t)

	req := connect.NewRequest(edgev1.GetEdgeRequest_builder{
		Edge: edgeRef(t, validEdgeID),
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
	if got := errCodeOf(t, err); got != authz.ErrCodeUnauthenticated.String() {
		t.Errorf("got error code %q, want %q", got, authz.ErrCodeUnauthenticated)
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

	t.Run("no rule method with no principal fails PermissionDenied", func(t *testing.T) {
		env.checker.Reset()
		noRuleReq := connect.NewRequest(edgev1.GetEdgeRequest_builder{
			Edge: edgeRef(t, validEdgeID),
		}.Build())
		noRuleReq.Header().Set("X-FlowSeer-Tenant", validTenantID)
		noRuleReq.Header().Set("X-Test-Omit-Principal", "true")

		_, err := env.noRuleCli.CallUnary(context.Background(), noRuleReq)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeUnsupportedRule.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeUnsupportedRule)
		}
		if len(env.checker.Recorded()) != 0 {
			t.Errorf("got %d queries, want 0", len(env.checker.Recorded()))
		}
	})
}

func TestTenantHeaderValidation(t *testing.T) {
	env := setupTestEnv(t)

	t.Run("call without X-FlowSeer-Tenant header fails InvalidArgument", func(t *testing.T) {
		env.checker.Reset()
		env.handlers.Reset()

		req := connect.NewRequest(edgev1.GetEdgeRequest_builder{
			Edge: edgeRef(t, validEdgeID),
		}.Build())

		_, err := env.edgeCli.GetEdge(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeInvalidArgument)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeNoTenant.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeNoTenant)
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
			Edge: edgeRef(t, validEdgeID),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", "Acme")

		_, err := env.edgeCli.GetEdge(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeInvalidArgument)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeNoTenant.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeNoTenant)
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
			Edge: edgeRef(t, validEdgeID),
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

func TestMembershipDeniedRefused(t *testing.T) {
	env := setupTestEnv(t)

	env.checker.SetDeny(func(q authz.Query) bool {
		return q.Object == "tenant:"+validTenantID && q.Relation == "member"
	})

	req := connect.NewRequest(edgev1.GetEdgeRequest_builder{
		Edge: edgeRef(t, validEdgeID),
	}.Build())
	req.Header().Set("X-FlowSeer-Tenant", validTenantID)

	_, err := env.edgeCli.GetEdge(context.Background(), req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
	}
	if got := errCodeOf(t, err); got != authz.ErrCodeDenied.String() {
		t.Errorf("got error code %q, want %q", got, authz.ErrCodeDenied)
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

func TestRequestRuleRelationAndTenantChecks(t *testing.T) {
	sessionID := validSessionID

	t.Run("manage query denied", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.SetDeny(func(q authz.Query) bool {
			return q.Object == "capture_session:"+sessionID && q.Relation == "manage"
		})

		req := connect.NewRequest(capturev1.GetCaptureSessionRequest_builder{
			Session: sessionRef(t, sessionID),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.captureCli.GetCaptureSession(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeDenied.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeDenied)
		}
		var connectErr *connect.Error
		if errors.As(err, &connectErr) && connectErr.Message() != "permission denied" {
			t.Errorf("got message %q, want 'permission denied'", connectErr.Message())
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
		env.checker.SetDeny(func(q authz.Query) bool {
			return q.Object == "capture_session:"+sessionID && q.Relation == "tenant"
		})

		req := connect.NewRequest(capturev1.GetCaptureSessionRequest_builder{
			Session: sessionRef(t, sessionID),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.captureCli.GetCaptureSession(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeDenied.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeDenied)
		}
		var connectErr *connect.Error
		if errors.As(err, &connectErr) && connectErr.Message() != "permission denied" {
			t.Errorf("got message %q, want 'permission denied'", connectErr.Message())
		}
		if env.handlers.DidRun("GetCaptureSession") {
			t.Error("handler ran, want not run")
		}
	})

	t.Run("both allowed and query fields verified", func(t *testing.T) {
		env := setupTestEnv(t)

		req := connect.NewRequest(capturev1.GetCaptureSessionRequest_builder{
			Session: sessionRef(t, sessionID),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.captureCli.GetCaptureSession(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !env.handlers.DidRun("GetCaptureSession") {
			t.Error("handler did not run")
		}

		batches := env.checker.Batches()
		if len(batches) != 1 {
			t.Fatalf("got %d batches, want 1", len(batches))
		}
		batch := batches[0]
		if len(batch) != 2 {
			t.Fatalf("batch length = %d, want 2", len(batch))
		}
		if batch[0].Object != "capture_session:"+sessionID || batch[0].Relation != "manage" || batch[0].User != "user:"+testPrincipalID {
			t.Errorf("batch[0] = %+v, want capture_session:%s#manage@user:%s", batch[0], sessionID, testPrincipalID)
		}
		if batch[1].Object != "capture_session:"+sessionID || batch[1].Relation != "tenant" || batch[1].User != "tenant:"+validTenantID {
			t.Errorf("batch[1] = %+v, want capture_session:%s#tenant@tenant:%s", batch[1], sessionID, validTenantID)
		}
	})
}

func TestTenantRuleChecksRelation(t *testing.T) {
	t.Run("admin relation denied", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.SetDeny(func(q authz.Query) bool {
			return q.Object == "tenant:"+validTenantID && q.Relation == "admin"
		})

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
		if got := errCodeOf(t, err); got != authz.ErrCodeDenied.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeDenied)
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

func TestObligationAndDenialHandling(t *testing.T) {
	t.Run("ListCaptureSessions skips Filter gets Internal", func(t *testing.T) {
		env := setupTestEnv(t)
		req := connect.NewRequest(capturev1.ListCaptureSessionsRequest_builder{}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		resp, err := env.captureCli.ListCaptureSessions(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeInternal)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeObligationViolation.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeObligationViolation)
		}
		if resp != nil {
			t.Error("expected nil response on obligation failure")
		}
	})

	t.Run("CreateCaptureSession Require denied and ignored gets Internal", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.SetDeny(func(q authz.Query) bool {
			return q.Object == "tenant:"+validTenantID && q.Relation == "full_payload"
		})

		env.handlers.SetCreateCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.CreateCaptureSessionRequest]) (*connect.Response[capturev1.CreateCaptureSessionResponse], error) {
			admitted, _ := tenant.FromContext(ctx)
			_ = authz.Require(ctx, "full_payload", "tenant", admitted)
			return connect.NewResponse(capturev1.CreateCaptureSessionResponse_builder{
				Session: capturemodelv1.CaptureSessionRecord_builder{}.Build(),
			}.Build()), nil
		})

		req := connect.NewRequest(capturev1.CreateCaptureSessionRequest_builder{
			Edge: edgeRef(t, validEdgeID),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		resp, err := env.captureCli.CreateCaptureSession(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeInternal)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeObligationViolation.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeObligationViolation)
		}
		if resp != nil {
			t.Error("expected nil response on ignored require denial")
		}
	})

	t.Run("CreateEdge under tenant rule ignores denied Require gets Internal", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.SetDeny(func(q authz.Query) bool {
			return q.Object == "edge:"+validEdgeID && q.Relation == "view"
		})

		env.handlers.SetCreateEdgeFn(func(ctx context.Context, _ *connect.Request[edgev1.CreateEdgeRequest]) (*connect.Response[edgev1.CreateEdgeResponse], error) {
			_ = authz.Require(ctx, "view", "edge", validEdgeID)
			return connect.NewResponse(edgev1.CreateEdgeResponse_builder{
				Edge: edgemodelv1.EdgeRecord_builder{}.Build(),
			}.Build()), nil
		})

		req := connect.NewRequest(edgev1.CreateEdgeRequest_builder{
			Name: proto.String("edge-alpha"),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		resp, err := env.edgeCli.CreateEdge(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeInternal)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeObligationViolation.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeObligationViolation)
		}
		if resp != nil {
			t.Error("expected nil response on ignored require denial under tenant rule")
		}
	})

	t.Run("ListCaptureSessions under filtered rule ignores denied Require gets Internal", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.SetDeny(func(q authz.Query) bool {
			return q.Object == "edge:"+validEdgeID && q.Relation == "view"
		})

		env.handlers.SetListCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
			_ = authz.Require(ctx, "view", "edge", validEdgeID)
			return connect.NewResponse(capturev1.ListCaptureSessionsResponse_builder{}.Build()), nil
		})

		req := connect.NewRequest(capturev1.ListCaptureSessionsRequest_builder{}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		resp, err := env.captureCli.ListCaptureSessions(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeInternal)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeObligationViolation.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeObligationViolation)
		}
		if resp != nil {
			t.Error("expected nil response on ignored require denial under filtered rule")
		}
	})

	t.Run("CreateCaptureSession Require denied and returned gives PermissionDenied", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.SetDeny(func(q authz.Query) bool {
			return q.Object == "tenant:"+validTenantID && q.Relation == "full_payload"
		})

		env.handlers.SetCreateCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.CreateCaptureSessionRequest]) (*connect.Response[capturev1.CreateCaptureSessionResponse], error) {
			admitted, _ := tenant.FromContext(ctx)
			if err := authz.Require(ctx, "full_payload", "tenant", admitted); err != nil {
				return nil, err
			}
			return connect.NewResponse(capturev1.CreateCaptureSessionResponse_builder{}.Build()), nil
		})

		req := connect.NewRequest(capturev1.CreateCaptureSessionRequest_builder{
			Edge: edgeRef(t, validEdgeID),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.captureCli.CreateCaptureSession(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeDenied.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeDenied)
		}

		recorded := env.checker.Recorded()
		if len(recorded) != 4 {
			t.Fatalf("got %d queries, want exactly 4", len(recorded))
		}
		if recorded[0].Object != "tenant:"+validTenantID || recorded[0].Relation != "member" {
			t.Errorf("query 0 = %+v, want tenant membership", recorded[0])
		}
		if recorded[1].Object != "edge:"+validEdgeID || recorded[1].Relation != "capture" {
			t.Errorf("query 1 = %+v, want edge capture", recorded[1])
		}
		if recorded[2].Object != "edge:"+validEdgeID || recorded[2].Relation != "tenant" {
			t.Errorf("query 2 = %+v, want edge tenant", recorded[2])
		}
		if recorded[3].Object != "tenant:"+validTenantID || recorded[3].Relation != "full_payload" || recorded[3].User != "user:"+testPrincipalID {
			t.Errorf("query 3 = %+v, want tenant:%s#full_payload@user:%s", recorded[3], validTenantID, testPrincipalID)
		}
	})

	t.Run("CreateCaptureSession Require for other tenant denied with no query", func(t *testing.T) {
		env := setupTestEnv(t)
		otherTenantID := "0192e6a0-0000-7000-8000-000000000099"

		env.handlers.SetCreateCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.CreateCaptureSessionRequest]) (*connect.Response[capturev1.CreateCaptureSessionResponse], error) {
			if err := authz.Require(ctx, "full_payload", "tenant", otherTenantID); err != nil {
				return nil, err
			}
			return connect.NewResponse(capturev1.CreateCaptureSessionResponse_builder{}.Build()), nil
		})

		req := connect.NewRequest(capturev1.CreateCaptureSessionRequest_builder{
			Edge: edgeRef(t, validEdgeID),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.captureCli.CreateCaptureSession(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeDenied.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeDenied)
		}

		for _, q := range env.checker.Recorded() {
			if strings.Contains(q.Object, otherTenantID) {
				t.Errorf("unexpected query for other tenant: %+v", q)
			}
		}
	})

	t.Run("ListCaptureSessions calling Filter passes response through", func(t *testing.T) {
		env := setupTestEnv(t)
		env.handlers.SetListCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
			_, err := authz.Filter(ctx, "capture", "edge", []string{validEdgeID, validEdgeID2})
			if err != nil {
				return nil, err
			}
			return connect.NewResponse(capturev1.ListCaptureSessionsResponse_builder{}.Build()), nil
		})

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
		env.handlers.SetListCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
			_, _ = authz.Filter(ctx, "capture", "edge", []string{validEdgeID})
			return nil, connect.NewError(connect.CodeNotFound, errors.New("resource missing"))
		})

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

	t.Run("Require on non-tenant object under filtered rule discharges obligation and verifies queries", func(t *testing.T) {
		t.Run("allowed passes response through", func(t *testing.T) {
			env := setupTestEnv(t)
			env.handlers.SetListCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
				if err := authz.Require(ctx, "capture", "edge", validEdgeID); err != nil {
					return nil, err
				}
				return connect.NewResponse(capturev1.ListCaptureSessionsResponse_builder{}.Build()), nil
			})

			req := connect.NewRequest(capturev1.ListCaptureSessionsRequest_builder{}.Build())
			req.Header().Set("X-FlowSeer-Tenant", validTenantID)

			resp, err := env.captureCli.ListCaptureSessions(context.Background(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp == nil {
				t.Fatal("expected response, got nil")
			}

			batches := env.checker.Batches()
			if len(batches) != 1 {
				t.Fatalf("got %d batches, want 1", len(batches))
			}
			batch := batches[0]
			if len(batch) != 2 {
				t.Fatalf("batch length = %d, want 2", len(batch))
			}
			if batch[0].Object != "edge:"+validEdgeID || batch[0].Relation != "capture" || batch[0].User != "user:"+testPrincipalID {
				t.Errorf("batch[0] = %+v, want edge:%s#capture@user:%s", batch[0], validEdgeID, testPrincipalID)
			}
			if batch[1].Object != "edge:"+validEdgeID || batch[1].Relation != "tenant" || batch[1].User != "tenant:"+validTenantID {
				t.Errorf("batch[1] = %+v, want edge:%s#tenant@tenant:%s", batch[1], validEdgeID, validTenantID)
			}
		})

		t.Run("relation query denied gives PermissionDenied", func(t *testing.T) {
			env := setupTestEnv(t)
			env.checker.SetDeny(func(q authz.Query) bool {
				return q.Object == "edge:"+validEdgeID && q.Relation == "capture"
			})

			env.handlers.SetListCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
				if err := authz.Require(ctx, "capture", "edge", validEdgeID); err != nil {
					return nil, err
				}
				return connect.NewResponse(capturev1.ListCaptureSessionsResponse_builder{}.Build()), nil
			})

			req := connect.NewRequest(capturev1.ListCaptureSessionsRequest_builder{}.Build())
			req.Header().Set("X-FlowSeer-Tenant", validTenantID)

			_, err := env.captureCli.ListCaptureSessions(context.Background(), req)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
			}
			if got := errCodeOf(t, err); got != authz.ErrCodeDenied.String() {
				t.Errorf("got error code %q, want %q", got, authz.ErrCodeDenied)
			}
		})

		t.Run("tenant query denied gives PermissionDenied", func(t *testing.T) {
			env := setupTestEnv(t)
			env.checker.SetDeny(func(q authz.Query) bool {
				return q.Object == "edge:"+validEdgeID && q.Relation == "tenant"
			})

			env.handlers.SetListCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
				if err := authz.Require(ctx, "capture", "edge", validEdgeID); err != nil {
					return nil, err
				}
				return connect.NewResponse(capturev1.ListCaptureSessionsResponse_builder{}.Build()), nil
			})

			req := connect.NewRequest(capturev1.ListCaptureSessionsRequest_builder{}.Build())
			req.Header().Set("X-FlowSeer-Tenant", validTenantID)

			_, err := env.captureCli.ListCaptureSessions(context.Background(), req)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
			}
			if got := errCodeOf(t, err); got != authz.ErrCodeDenied.String() {
				t.Errorf("got error code %q, want %q", got, authz.ErrCodeDenied)
			}
		})
	})
}

func TestCheckerErrorBecomesUnavailable(t *testing.T) {
	t.Run("request rule batch check fails", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.SetFail(func(q authz.Query) error {
			if strings.HasPrefix(q.Object, "edge:") {
				return errors.New("edge query failed")
			}
			return nil
		})

		req := connect.NewRequest(edgev1.GetEdgeRequest_builder{
			Edge: edgeRef(t, validEdgeID),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.edgeCli.GetEdge(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodeUnavailable {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeUnavailable)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeUnavailable.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeUnavailable)
		}
		if got := retryDispositionOf(t, err); got != errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
			t.Errorf("got retry disposition %v, want retryable", got)
		}
		if !strings.Contains(err.Error(), "authorization is unavailable") {
			t.Errorf("got message %q, want containing 'authorization is unavailable'", err.Error())
		}
		if env.handlers.DidRun("GetEdge") {
			t.Error("handler ran, want not run")
		}
		recorded := env.checker.Recorded()
		if len(recorded) != 3 {
			t.Errorf("got %d queries, want 3", len(recorded))
		}
	})

	t.Run("tenant rule relation query fails", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.SetFail(func(q authz.Query) error {
			if q.Object == "tenant:"+validTenantID && q.Relation == "admin" {
				return errors.New("tenant relation query failed")
			}
			return nil
		})

		req := connect.NewRequest(edgev1.CreateEdgeRequest_builder{
			Name: proto.String("edge-alpha"),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.edgeCli.CreateEdge(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodeUnavailable {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeUnavailable)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeUnavailable.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeUnavailable)
		}
		if got := retryDispositionOf(t, err); got != errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
			t.Errorf("got retry disposition %v, want retryable", got)
		}
		if !strings.Contains(err.Error(), "authorization is unavailable") {
			t.Errorf("got message %q, want containing 'authorization is unavailable'", err.Error())
		}
		if env.handlers.DidRun("CreateEdge") {
			t.Error("handler ran, want not run")
		}
		recorded := env.checker.Recorded()
		if len(recorded) != 2 {
			t.Errorf("got %d queries, want 2", len(recorded))
		}
	})

	t.Run("platform query fails", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.SetFail(func(q authz.Query) error {
			if q.Object == "platform:flowseer" {
				return errors.New("platform query failed")
			}
			return nil
		})

		req := connect.NewRequest(identityv1.ListTenantsRequest_builder{}.Build())

		_, err := env.tenantCli.ListTenants(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodeUnavailable {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeUnavailable)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeUnavailable.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeUnavailable)
		}
		if got := retryDispositionOf(t, err); got != errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
			t.Errorf("got retry disposition %v, want retryable", got)
		}
		if !strings.Contains(err.Error(), "authorization is unavailable") {
			t.Errorf("got message %q, want containing 'authorization is unavailable'", err.Error())
		}
		if env.handlers.DidRun("ListTenants") {
			t.Error("handler ran, want not run")
		}
		recorded := env.checker.Recorded()
		if len(recorded) != 1 {
			t.Errorf("got %d queries, want 1", len(recorded))
		}
	})

	t.Run("Require query fails", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.SetFail(func(q authz.Query) error {
			if q.Object == "tenant:"+validTenantID && q.Relation == "full_payload" {
				return errors.New("require query failed")
			}
			return nil
		})

		var requireErr error
		env.handlers.SetCreateCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.CreateCaptureSessionRequest]) (*connect.Response[capturev1.CreateCaptureSessionResponse], error) {
			admitted, _ := tenant.FromContext(ctx)
			requireErr = authz.Require(ctx, "full_payload", "tenant", admitted)
			return nil, requireErr
		})

		req := connect.NewRequest(capturev1.CreateCaptureSessionRequest_builder{
			Edge: edgeRef(t, validEdgeID),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.captureCli.CreateCaptureSession(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(requireErr) != connect.CodeUnavailable {
			t.Errorf("Require returned code %v, want %v", connect.CodeOf(requireErr), connect.CodeUnavailable)
		}
		if got := errCodeOf(t, requireErr); got != authz.ErrCodeUnavailable.String() {
			t.Errorf("Require returned error code %q, want %q", got, authz.ErrCodeUnavailable)
		}
		if got := retryDispositionOf(t, requireErr); got != errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
			t.Errorf("Require returned retry disposition %v, want retryable", got)
		}
		if !strings.Contains(requireErr.Error(), "authorization is unavailable") {
			t.Errorf("Require returned message %q, want containing 'authorization is unavailable'", requireErr.Error())
		}
		recorded := env.checker.Recorded()
		if len(recorded) != 4 {
			t.Errorf("got %d queries, want 4", len(recorded))
		}
	})

	t.Run("Filter batch check fails", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.SetFail(func(q authz.Query) error {
			if q.Object == "edge:"+validEdgeID && q.Relation == "capture" {
				return errors.New("filter batch failed")
			}
			return nil
		})

		var filterErr error
		env.handlers.SetListCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
			_, filterErr = authz.Filter(ctx, "capture", "edge", []string{validEdgeID})
			return nil, filterErr
		})

		req := connect.NewRequest(capturev1.ListCaptureSessionsRequest_builder{}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.captureCli.ListCaptureSessions(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(filterErr) != connect.CodeUnavailable {
			t.Errorf("Filter returned code %v, want %v", connect.CodeOf(filterErr), connect.CodeUnavailable)
		}
		if got := errCodeOf(t, filterErr); got != authz.ErrCodeUnavailable.String() {
			t.Errorf("Filter returned error code %q, want %q", got, authz.ErrCodeUnavailable)
		}
		if got := retryDispositionOf(t, filterErr); got != errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
			t.Errorf("Filter returned retry disposition %v, want retryable", got)
		}
		if !strings.Contains(filterErr.Error(), "authorization is unavailable") {
			t.Errorf("Filter returned message %q, want containing 'authorization is unavailable'", filterErr.Error())
		}
		recorded := env.checker.Recorded()
		if len(recorded) != 3 {
			t.Errorf("got %d queries, want 3", len(recorded))
		}
	})
}

func TestContextualTuplesSentOnEveryCheck(t *testing.T) {
	env := setupTestEnv(t)

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

	assertTuplesMatch := func(t *testing.T, queries []authz.Query) {
		t.Helper()
		for i, q := range queries {
			if len(q.ContextualTuples) != len(wantTuples) {
				t.Fatalf("query %d (%s#%s) got %d tuples, want %d", i, q.Object, q.Relation, len(q.ContextualTuples), len(wantTuples))
			}
			for _, want := range wantTuples {
				if !slices.Contains(q.ContextualTuples, want) {
					t.Errorf("query %d (%s#%s) tuples %v do not contain %+v", i, q.Object, q.Relation, q.ContextualTuples, want)
				}
			}
		}
	}

	t.Run("request rule call carries tuples on every query", func(t *testing.T) {
		env.checker.Reset()
		req := connect.NewRequest(edgev1.GetEdgeRequest_builder{
			Edge: edgeRef(t, validEdgeID),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)
		req.Header().Set("X-Test-Principal-Tenants", validTenantID+","+validTenantID2)
		req.Header().Set("X-Test-Principal-Platform", "true")

		_, err := env.edgeCli.GetEdge(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		recorded := env.checker.Recorded()
		if len(recorded) != 3 {
			t.Fatalf("got %d queries, want 3", len(recorded))
		}
		assertTuplesMatch(t, recorded)
	})

	t.Run("platform call carries tuples on every query", func(t *testing.T) {
		env.checker.Reset()
		req := connect.NewRequest(identityv1.ListTenantsRequest_builder{}.Build())
		req.Header().Set("X-Test-Principal-Tenants", validTenantID+","+validTenantID2)
		req.Header().Set("X-Test-Principal-Platform", "true")

		_, err := env.tenantCli.ListTenants(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		recorded := env.checker.Recorded()
		if len(recorded) != 1 {
			t.Fatalf("got %d queries, want 1", len(recorded))
		}
		assertTuplesMatch(t, recorded)
	})

	t.Run("Require call carries tuples on every query", func(t *testing.T) {
		env.checker.Reset()
		env.handlers.SetListCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
			if err := authz.Require(ctx, "capture", "edge", validEdgeID); err != nil {
				return nil, err
			}
			return connect.NewResponse(capturev1.ListCaptureSessionsResponse_builder{}.Build()), nil
		})

		req := connect.NewRequest(capturev1.ListCaptureSessionsRequest_builder{}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)
		req.Header().Set("X-Test-Principal-Tenants", validTenantID+","+validTenantID2)
		req.Header().Set("X-Test-Principal-Platform", "true")

		_, err := env.captureCli.ListCaptureSessions(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		recorded := env.checker.Recorded()
		if len(recorded) != 3 {
			t.Fatalf("got %d queries, want 3 (1 membership + 2 require)", len(recorded))
		}
		assertTuplesMatch(t, recorded)
	})

	t.Run("Filter call carries tuples on every query", func(t *testing.T) {
		env.checker.Reset()
		env.handlers.SetListCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
			_, err := authz.Filter(ctx, "capture", "edge", []string{validEdgeID})
			if err != nil {
				return nil, err
			}
			return connect.NewResponse(capturev1.ListCaptureSessionsResponse_builder{}.Build()), nil
		})

		req := connect.NewRequest(capturev1.ListCaptureSessionsRequest_builder{}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)
		req.Header().Set("X-Test-Principal-Tenants", validTenantID+","+validTenantID2)
		req.Header().Set("X-Test-Principal-Platform", "true")

		_, err := env.captureCli.ListCaptureSessions(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		recorded := env.checker.Recorded()
		if len(recorded) != 3 {
			t.Fatalf("got %d queries, want 3 (1 membership + 2 filter)", len(recorded))
		}
		assertTuplesMatch(t, recorded)
	})
}

func TestStreamingCallRefused(t *testing.T) {
	env := setupTestEnv(t)

	req := connect.NewRequest(capturev1.DownloadCaptureSessionRequest_builder{
		Session: sessionRef(t, validSessionID),
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
	if got := errCodeOf(t, err); got != authz.ErrCodeStreaming.String() {
		t.Errorf("got error code %q, want %q", got, authz.ErrCodeStreaming)
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("got message %q, want containing 'permission denied'", err.Error())
	}
	if env.handlers.DidRun("DownloadCaptureSession") {
		t.Error("streaming handler ran, want not run")
	}
}

func TestUnsupportedOrMissingRuleRefused(t *testing.T) {
	env := setupTestEnv(t)

	t.Run("missing rule on attach service heartbeat", func(t *testing.T) {
		req := connect.NewRequest(attachv1.HeartbeatRequest_builder{}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.edgeAttachCli.Heartbeat(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeUnsupportedRule.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeUnsupportedRule)
		}
		if !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("got message %q, want containing 'permission denied'", err.Error())
		}
		if len(env.checker.Recorded()) != 0 {
			t.Errorf("got %d queries, want 0", len(env.checker.Recorded()))
		}
	})

	t.Run("unspecified mode refused with no query", func(t *testing.T) {
		env.checker.Reset()
		req := connect.NewRequest(edgev1.GetEdgeRequest_builder{
			Edge: edgeRef(t, validEdgeID),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.unspecifiedCli.CallUnary(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeUnsupportedRule.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeUnsupportedRule)
		}
		if len(env.checker.Recorded()) != 0 {
			t.Errorf("got %d queries, want 0", len(env.checker.Recorded()))
		}
	})

	t.Run("mode 99 refused with no query", func(t *testing.T) {
		env.checker.Reset()
		req := connect.NewRequest(edgev1.GetEdgeRequest_builder{
			Edge: edgeRef(t, validEdgeID),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.mode99Cli.CallUnary(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeUnsupportedRule.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeUnsupportedRule)
		}
		if len(env.checker.Recorded()) != 0 {
			t.Errorf("got %d queries, want 0", len(env.checker.Recorded()))
		}
	})

	t.Run("loaded mode requires obligation discharge", func(t *testing.T) {
		t.Run("handler without Require gives Internal", func(t *testing.T) {
			env.checker.Reset()
			env.setLoadedFn(nil)

			req := connect.NewRequest(edgev1.GetEdgeRequest_builder{
				Edge: edgeRef(t, validEdgeID),
			}.Build())
			req.Header().Set("X-FlowSeer-Tenant", validTenantID)

			_, err := env.loadedCli.CallUnary(context.Background(), req)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if connect.CodeOf(err) != connect.CodeInternal {
				t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeInternal)
			}
			if got := errCodeOf(t, err); got != authz.ErrCodeObligationViolation.String() {
				t.Errorf("got error code %q, want %q", got, authz.ErrCodeObligationViolation)
			}
		})

		t.Run("handler with allowed Require passes response", func(t *testing.T) {
			env.checker.Reset()
			env.setLoadedFn(func(ctx context.Context, _ *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error) {
				if err := authz.Require(ctx, "view", "edge", validEdgeID); err != nil {
					return nil, err
				}
				return connect.NewResponse(edgev1.GetEdgeResponse_builder{
					Edge: edgemodelv1.EdgeRecord_builder{}.Build(),
				}.Build()), nil
			})

			req := connect.NewRequest(edgev1.GetEdgeRequest_builder{
				Edge: edgeRef(t, validEdgeID),
			}.Build())
			req.Header().Set("X-FlowSeer-Tenant", validTenantID)

			resp, err := env.loadedCli.CallUnary(context.Background(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp == nil {
				t.Fatal("expected response, got nil")
			}
		})
	})
}

func TestPlatformRuleAuthorization(t *testing.T) {
	t.Run("false answer is PermissionDenied and handler does not run", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.SetDeny(func(q authz.Query) bool {
			return q.Object == "platform:flowseer" && q.Relation == "admin"
		})

		req := connect.NewRequest(identityv1.ListTenantsRequest_builder{}.Build())

		_, err := env.tenantCli.ListTenants(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeDenied.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeDenied)
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

		env.handlers.SetListTenantsFn(func(ctx context.Context, _ *connect.Request[identityv1.ListTenantsRequest]) (*connect.Response[identityv1.ListTenantsResponse], error) {
			if _, err := tenant.FromContext(ctx); err == nil {
				t.Error("tenant found in context under platform rule, want no tenant")
			}
			if err := authz.Require(ctx, "admin", "tenant", validTenantID); err == nil {
				t.Error("Require under platform rule returned nil, want Internal")
			} else if connect.CodeOf(err) != connect.CodeInternal {
				t.Errorf("Require under platform rule returned %v, want CodeInternal", connect.CodeOf(err))
			} else if got := errCodeOf(t, err); got != authz.ErrCodeObligationViolation.String() {
				t.Errorf("Require under platform rule error code = %q, want %q", got, authz.ErrCodeObligationViolation)
			}
			return connect.NewResponse(identityv1.ListTenantsResponse_builder{}.Build()), nil
		})

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

func TestRequestRuleNoIDRefused(t *testing.T) {
	env := setupTestEnv(t)

	// GetEdgeRequest deliberately omits the required edge field to test that
	// a request rule whose path yields no id is refused.
	req := connect.NewRequest(edgev1.GetEdgeRequest_builder{}.Build())
	req.Header().Set("X-FlowSeer-Tenant", validTenantID)

	_, err := env.edgeCli.GetEdge(context.Background(), req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
	}
	if got := errCodeOf(t, err); got != authz.ErrCodeNoObjectID.String() {
		t.Errorf("got error code %q, want %q", got, authz.ErrCodeNoObjectID)
	}

	recorded := env.checker.Recorded()
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
		Edge: edgeRef(t, validEdgeID),
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
	t.Run("batches across 50 limit and deduplicates", func(t *testing.T) {
		env := setupTestEnv(t)

		ids := make([]string, 60)
		for i := range 59 {
			ids[i] = testUUID('e', i+1)
		}
		ids[59] = testUUID('e', 1)

		var (
			mu          sync.Mutex
			filteredIDs []string
		)
		env.handlers.SetListCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
			res, err := authz.Filter(ctx, "capture", "edge", ids)
			if err != nil {
				return nil, err
			}
			mu.Lock()
			filteredIDs = res
			mu.Unlock()
			return connect.NewResponse(capturev1.ListCaptureSessionsResponse_builder{}.Build()), nil
		})

		req := connect.NewRequest(capturev1.ListCaptureSessionsRequest_builder{}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.captureCli.ListCaptureSessions(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

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

		for _, b := range batches {
			for i := 0; i < len(b); i += 2 {
				relQuery := b[i]
				tenantQuery := b[i+1]
				if relQuery.Relation != "capture" || relQuery.User != "user:"+testPrincipalID {
					t.Errorf("relQuery = %+v, want relation capture, user:%s", relQuery, testPrincipalID)
				}
				if tenantQuery.Relation != "tenant" || tenantQuery.User != "tenant:"+validTenantID {
					t.Errorf("tenantQuery = %+v, want relation tenant, tenant:%s", tenantQuery, validTenantID)
				}
			}
		}

		mu.Lock()
		count := len(filteredIDs)
		mu.Unlock()
		if count != 59 {
			t.Errorf("got %d filtered IDs, want 59", count)
		}
	})

	t.Run("deny one relation and one tenant query across batches preserves order", func(t *testing.T) {
		env := setupTestEnv(t)

		// 30 unique IDs: 25 pairs in batch 0 (50 queries), 5 pairs in batch 1 (10 queries).
		ids := make([]string, 30)
		for i := range 30 {
			ids[i] = testUUID('f', i+1)
		}

		deniedRelID := ids[5]
		deniedTenantID := ids[25]

		env.checker.SetDeny(func(q authz.Query) bool {
			if q.Object == "edge:"+deniedRelID && q.Relation == "capture" {
				return true
			}
			if q.Object == "edge:"+deniedTenantID && q.Relation == "tenant" {
				return true
			}
			return false
		})

		var (
			mu          sync.Mutex
			filteredIDs []string
		)
		env.handlers.SetListCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
			res, err := authz.Filter(ctx, "capture", "edge", ids)
			if err != nil {
				return nil, err
			}
			mu.Lock()
			filteredIDs = res
			mu.Unlock()
			return connect.NewResponse(capturev1.ListCaptureSessionsResponse_builder{}.Build()), nil
		})

		req := connect.NewRequest(capturev1.ListCaptureSessionsRequest_builder{}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.captureCli.ListCaptureSessions(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var wantIDs []string
		for _, id := range ids {
			if id != deniedRelID && id != deniedTenantID {
				wantIDs = append(wantIDs, id)
			}
		}

		mu.Lock()
		gotIDs := filteredIDs
		mu.Unlock()

		if !slices.Equal(gotIDs, wantIDs) {
			t.Fatalf("got IDs %v, want %v", gotIDs, wantIDs)
		}
	})
}

func TestBatchCheckShortAnswerIsUnavailable(t *testing.T) {
	t.Run("interceptor request rule receives short answer", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.SetBatchAnswers(func(_ []authz.Query) []bool {
			return []bool{true}
		})

		req := connect.NewRequest(edgev1.GetEdgeRequest_builder{
			Edge: edgeRef(t, validEdgeID),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.edgeCli.GetEdge(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodeUnavailable {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeUnavailable)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeUnavailable.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeUnavailable)
		}
		if got := retryDispositionOf(t, err); got != errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
			t.Errorf("got retry disposition %v, want retryable", got)
		}
		if env.handlers.DidRun("GetEdge") {
			t.Error("handler ran, want not run")
		}
	})

	t.Run("Require receives short answer", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.SetBatchAnswers(func(_ []authz.Query) []bool {
			return []bool{true}
		})

		var requireErr error
		env.handlers.SetListCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
			requireErr = authz.Require(ctx, "capture", "edge", validEdgeID)
			return nil, requireErr
		})

		req := connect.NewRequest(capturev1.ListCaptureSessionsRequest_builder{}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, _ = env.captureCli.ListCaptureSessions(context.Background(), req)
		if connect.CodeOf(requireErr) != connect.CodeUnavailable {
			t.Errorf("Require returned code %v, want %v", connect.CodeOf(requireErr), connect.CodeUnavailable)
		}
		if got := errCodeOf(t, requireErr); got != authz.ErrCodeUnavailable.String() {
			t.Errorf("Require returned error code %q, want %q", got, authz.ErrCodeUnavailable)
		}
		if got := retryDispositionOf(t, requireErr); got != errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
			t.Errorf("Require returned retry disposition %v, want retryable", got)
		}
	})

	t.Run("Filter receives short answer", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.SetBatchAnswers(func(_ []authz.Query) []bool {
			return []bool{true}
		})

		var filterErr error
		env.handlers.SetListCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
			_, filterErr = authz.Filter(ctx, "capture", "edge", []string{validEdgeID})
			return nil, filterErr
		})

		req := connect.NewRequest(capturev1.ListCaptureSessionsRequest_builder{}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, _ = env.captureCli.ListCaptureSessions(context.Background(), req)
		if connect.CodeOf(filterErr) != connect.CodeUnavailable {
			t.Errorf("Filter returned code %v, want %v", connect.CodeOf(filterErr), connect.CodeUnavailable)
		}
		if got := errCodeOf(t, filterErr); got != authz.ErrCodeUnavailable.String() {
			t.Errorf("Filter returned error code %q, want %q", got, authz.ErrCodeUnavailable)
		}
		if got := retryDispositionOf(t, filterErr); got != errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
			t.Errorf("Filter returned retry disposition %v, want retryable", got)
		}
	})
}

func TestDirectObligationContextValidation(t *testing.T) {
	t.Run("Require on background context returns Internal", func(t *testing.T) {
		err := authz.Require(context.Background(), "view", "edge", validEdgeID)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeInternal)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeObligationViolation.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeObligationViolation)
		}
	})

	t.Run("Filter on background context returns Internal", func(t *testing.T) {
		_, err := authz.Filter(context.Background(), "view", "edge", []string{validEdgeID})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeInternal)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeObligationViolation.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeObligationViolation)
		}
	})
}
