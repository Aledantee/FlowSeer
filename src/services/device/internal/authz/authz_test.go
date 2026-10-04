package authz_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
	identitymodelv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	_ "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/key/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/spawn"
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
	groups       [][]authz.Query
	deny         func(q authz.Query) bool
	fail         func(q authz.Query) error
	batchAnswers func(queries []authz.Query) []bool
}

func (f *fakeChecker) Check(_ context.Context, q authz.Query) (bool, error) {
	f.mu.Lock()
	f.recorded = append(f.recorded, q)
	f.groups = append(f.groups, []authz.Query{q})
	denyFn := f.deny
	failFn := f.fail
	f.mu.Unlock()

	if failFn != nil {
		if err := failFn(q); err != nil {
			return true, err
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
	f.groups = append(f.groups, slices.Clone(queries))
	denyFn := f.deny
	failFn := f.fail
	batchAnswersFn := f.batchAnswers
	f.mu.Unlock()

	if failFn != nil {
		for _, q := range queries {
			if err := failFn(q); err != nil {
				results := make([]bool, len(queries))
				for i := range results {
					results[i] = true
				}
				return results, err
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

func (f *fakeChecker) Groups() [][]authz.Query {
	f.mu.Lock()
	defer f.mu.Unlock()
	copied := make([][]authz.Query, len(f.groups))
	for i, g := range f.groups {
		copied[i] = make([]authz.Query, len(g))
		for j, q := range g {
			copied[i][j] = q
			copied[i][j].ContextualTuples = slices.Clone(q.ContextualTuples)
		}
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
	f.groups = nil
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
		if vals := req.Header().Values("X-Test-Principal-ID"); len(vals) > 0 {
			p.ID = vals[0]
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
		if vals := conn.RequestHeader().Values("X-Test-Principal-ID"); len(vals) > 0 {
			p.ID = vals[0]
		}
		ctx = authn.NewContext(ctx, p)
		return next(ctx, conn)
	}
}

type innerRecordingInterceptor struct {
	entered atomic.Bool
}

func (i *innerRecordingInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		i.entered.Store(true)
		return next(ctx, req)
	}
}

func (i *innerRecordingInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *innerRecordingInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		i.entered.Store(true)
		return next(ctx, conn)
	}
}

func edgeRef(t *testing.T) *edgemodelv1.EdgeGlobalRef {
	t.Helper()
	ref := edgemodelv1.EdgeGlobalRef_builder{
		Edge: edgemodelv1.EdgeLocalRef_builder{
			Id: proto.String(validEdgeID),
		}.Build(),
	}.Build()
	if err := protovalidate.Validate(ref); err != nil {
		t.Fatalf("edgeRef fails protovalidate: %v", err)
	}
	return ref
}

func sessionRef(t *testing.T, sessionID string) *capturemodelv1.CaptureSessionGlobalRef {
	t.Helper()
	ref := capturemodelv1.CaptureSessionGlobalRef_builder{
		Edge: edgeRef(t),
		CaptureSession: capturemodelv1.CaptureSessionLocalRef_builder{
			Id: proto.String(sessionID),
		}.Build(),
	}.Build()
	if err := protovalidate.Validate(ref); err != nil {
		t.Fatalf("sessionRef(%q) fails protovalidate: %v", sessionID, err)
	}
	return ref
}

func validRequest[T any](t *testing.T, msg *T) *connect.Request[T] {
	t.Helper()
	if protoMsg, ok := any(msg).(proto.Message); ok {
		if err := protovalidate.Validate(protoMsg); err != nil {
			t.Fatalf("request %T fails protovalidate: %v", msg, err)
		}
	}
	return connect.NewRequest(msg)
}

func validResponse[T any](t *testing.T, msg *T) *connect.Response[T] {
	t.Helper()
	if protoMsg, ok := any(msg).(proto.Message); ok && protoMsg != nil {
		if err := protovalidate.Validate(protoMsg); err != nil {
			t.Fatalf("response %T fails protovalidate: %v", msg, err)
		}
	}
	return connect.NewResponse(msg)
}

func validEdgeRecord(t *testing.T) *edgemodelv1.EdgeRecord {
	t.Helper()
	rec := edgemodelv1.EdgeRecord_builder{
		Config: edgemodelv1.EdgeConfig_builder{
			Ref: edgeRef(t),
		}.Build(),
		State: edgemodelv1.EdgeState_builder{
			Ref:       edgeRef(t),
			Lifecycle: edgemodelv1.EdgeLifecycle_EDGE_LIFECYCLE_PENDING.Enum(),
		}.Build(),
	}.Build()
	if err := protovalidate.Validate(rec); err != nil {
		t.Fatalf("validEdgeRecord fails protovalidate: %v", err)
	}
	return rec
}

func validEdgeProvisioning(t *testing.T) *edgemodelv1.EdgeProvisioning {
	t.Helper()
	anchor := bytes.Repeat([]byte{3}, 32)
	prov := edgemodelv1.EdgeProvisioning_builder{
		CentralUrl:   proto.String("https://central.example.net"),
		SetupKey:     proto.String("fse1_abcdefghijklmnopqrstuvwxyz_abcdefghijklmnopqrstuvwxyz234567abcdefghijklmnopqrst"),
		TrustAnchors: [][]byte{anchor},
	}.Build()
	if err := protovalidate.Validate(prov); err != nil {
		t.Fatalf("validEdgeProvisioning fails protovalidate: %v", err)
	}
	return prov
}

func validCaptureSessionRecord(t *testing.T) *capturemodelv1.CaptureSessionRecord {
	t.Helper()
	rec := capturemodelv1.CaptureSessionRecord_builder{
		Config: capturemodelv1.CaptureSessionConfig_builder{
			Ref: sessionRef(t, validSessionID),
			Source: capturemodelv1.CaptureSource_builder{
				LocalInterface: capturemodelv1.LocalInterfaceSource_builder{
					InterfaceName: proto.String("eth0"),
				}.Build(),
			}.Build(),
			Budget: capturemodelv1.CaptureBudget_builder{
				MaxPackets: proto.Uint64(100),
			}.Build(),
			Authorization: capturemodelv1.CaptureAuthorization_builder{
				RequestedBy: identitymodelv1.OperatorRef_builder{
					Issuer:  proto.String("https://auth.example.com"),
					Subject: proto.String(testPrincipalID),
				}.Build(),
				Reason:               proto.String("authorized test session"),
				FullPayloadRequested: proto.Bool(false),
			}.Build(),
		}.Build(),
		State: capturemodelv1.CaptureSessionState_builder{
			Ref:       sessionRef(t, validSessionID),
			Lifecycle: capturemodelv1.CaptureLifecycle_CAPTURE_LIFECYCLE_PENDING.Enum(),
		}.Build(),
	}.Build()
	if err := protovalidate.Validate(rec); err != nil {
		t.Fatalf("validCaptureSessionRecord fails protovalidate: %v", err)
	}
	return rec
}

func validCreateCaptureSessionRequest(t *testing.T) *capturev1.CreateCaptureSessionRequest {
	t.Helper()
	return capturev1.CreateCaptureSessionRequest_builder{
		Edge: edgeRef(t),
		Source: capturemodelv1.CaptureSource_builder{
			LocalInterface: capturemodelv1.LocalInterfaceSource_builder{
				InterfaceName: proto.String("eth0"),
			}.Build(),
		}.Build(),
		Budget: capturemodelv1.CaptureBudget_builder{
			MaxPackets: proto.Uint64(100),
		}.Build(),
		Authorization: capturemodelv1.CaptureAuthorization_builder{
			RequestedBy: identitymodelv1.OperatorRef_builder{
				Issuer:  proto.String("https://auth.example.com"),
				Subject: proto.String(testPrincipalID),
			}.Build(),
			Reason:               proto.String("authorized test session"),
			FullPayloadRequested: proto.Bool(false),
		}.Build(),
	}.Build()
}

func validHeartbeatRequest(t *testing.T) *attachv1.HeartbeatRequest {
	t.Helper()
	return attachv1.HeartbeatRequest_builder{
		AgentVersion: proto.String("v1.0.0"),
	}.Build()
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
	t *testing.T
	capturev1connect.UnimplementedCaptureServiceHandler
	edgev1connect.UnimplementedEdgeAdminServiceHandler
	identityv1connect.UnimplementedTenantServiceHandler
	attachv1connect.UnimplementedEdgeServiceHandler

	mu                sync.Mutex
	createCaptureFn   func(ctx context.Context, req *connect.Request[capturev1.CreateCaptureSessionRequest]) (*connect.Response[capturev1.CreateCaptureSessionResponse], error)
	listCaptureFn     func(ctx context.Context, req *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error)
	getCaptureFn      func(ctx context.Context, req *connect.Request[capturev1.GetCaptureSessionRequest]) (*connect.Response[capturev1.GetCaptureSessionResponse], error)
	createEdgeFn      func(ctx context.Context, req *connect.Request[edgev1.CreateEdgeRequest]) (*connect.Response[edgev1.CreateEdgeResponse], error)
	getEdgeFn         func(ctx context.Context, req *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error)
	listTenantsFn     func(ctx context.Context, req *connect.Request[identityv1.ListTenantsRequest]) (*connect.Response[identityv1.ListTenantsResponse], error)
	downloadCaptureFn func(ctx context.Context, req *connect.Request[capturev1.DownloadCaptureSessionRequest], stream *connect.ServerStream[capturev1.DownloadCaptureSessionResponse]) error
	ranHandlers       map[string]bool
	admittedTenants   map[string]string
}

func newTestHandlers(t *testing.T) *testHandlers {
	return &testHandlers{
		t:               t,
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
	return validResponse(h.t, capturev1.CreateCaptureSessionResponse_builder{
		Session: validCaptureSessionRecord(h.t),
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
	return validResponse(h.t, capturev1.GetCaptureSessionResponse_builder{
		Session: validCaptureSessionRecord(h.t),
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
	return validResponse(h.t, capturev1.ListCaptureSessionsResponse_builder{}.Build()), nil
}

func (h *testHandlers) SetDownloadCaptureFn(fn func(ctx context.Context, req *connect.Request[capturev1.DownloadCaptureSessionRequest], stream *connect.ServerStream[capturev1.DownloadCaptureSessionResponse]) error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.downloadCaptureFn = fn
}

func (h *testHandlers) DownloadCaptureSession(ctx context.Context, req *connect.Request[capturev1.DownloadCaptureSessionRequest], stream *connect.ServerStream[capturev1.DownloadCaptureSessionResponse]) error {
	h.markRan(ctx, "DownloadCaptureSession")
	h.mu.Lock()
	fn := h.downloadCaptureFn
	h.mu.Unlock()
	if fn != nil {
		return fn(ctx, req, stream)
	}
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
	return validResponse(h.t, edgev1.CreateEdgeResponse_builder{
		Edge:         validEdgeRecord(h.t),
		Provisioning: validEdgeProvisioning(h.t),
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
	return validResponse(h.t, edgev1.GetEdgeResponse_builder{
		Edge: validEdgeRecord(h.t),
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
	return validResponse(h.t, identityv1.ListTenantsResponse_builder{}.Build()), nil
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
	server           *httptest.Server
	checker          *fakeChecker
	handlers         *testHandlers
	captureCli       capturev1connect.CaptureServiceClient
	edgeCli          edgev1connect.EdgeAdminServiceClient
	tenantCli        identityv1connect.TenantServiceClient
	edgeAttachCli    attachv1connect.EdgeServiceClient
	interceptor      *authz.Interceptor
	outerInterceptor *cancellableOuterInterceptor
	unspecifiedCli   *connect.Client[edgev1.GetEdgeRequest, edgev1.GetEdgeResponse]
	mode99Cli        *connect.Client[edgev1.GetEdgeRequest, edgev1.GetEdgeResponse]
	loadedCli        *connect.Client[edgev1.GetEdgeRequest, edgev1.GetEdgeResponse]
	noRuleCli        *connect.Client[edgev1.GetEdgeRequest, edgev1.GetEdgeResponse]
	inner            *innerRecordingInterceptor
	setLoadedFn      func(fn func(ctx context.Context, req *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error))
}

func setupTestEnv(t *testing.T) *testEnv {
	t.Helper()

	checker := &fakeChecker{}
	handlers := newTestHandlers(t)
	inner := &innerRecordingInterceptor{}
	authzInterceptor := authz.NewInterceptor(checker)
	outerInterceptor := &cancellableOuterInterceptor{}
	authnInterceptor := &testAuthnInterceptor{
		defaultPrincipal: authn.Principal{
			ID:       testPrincipalID,
			Tenants:  []string{validTenantID},
			Platform: false,
		},
	}

	opts := []connect.HandlerOption{
		connect.WithInterceptors(authnInterceptor, outerInterceptor, authzInterceptor, inner),
	}

	mux := http.NewServeMux()
	mux.Handle(capturev1connect.NewCaptureServiceHandler(handlers, opts...))
	mux.Handle(edgev1connect.NewEdgeAdminServiceHandler(handlers, opts...))
	mux.Handle(identityv1connect.NewTenantServiceHandler(handlers, opts...))
	mux.Handle(attachv1connect.NewEdgeServiceHandler(handlers, opts...))

	unspecifiedRule := authzv1.Rule_builder{
		Mode:       authzv1.RuleMode_RULE_MODE_UNSPECIFIED.Enum(),
		ObjectType: proto.String("edge"),
		Relation:   proto.String("view"),
	}.Build()
	var unspecifiedVErr *protovalidate.ValidationError
	if err := protovalidate.Validate(unspecifiedRule); err == nil {
		t.Fatal("expected unspecified rule to fail protovalidate, got nil")
	} else if !errors.As(err, &unspecifiedVErr) || len(unspecifiedVErr.Violations) != 1 || unspecifiedVErr.Violations[0].FieldDescriptor == nil || unspecifiedVErr.Violations[0].FieldDescriptor.Name() != "mode" {
		t.Fatalf("expected unspecified rule to fail on mode alone, got: %v", err)
	}
	unspecifiedMD := buildSyntheticMethod(t, "UnspecifiedModeMethod", unspecifiedRule)
	unspecifiedProc := "/" + string(unspecifiedMD.Parent().FullName()) + "/" + string(unspecifiedMD.Name())
	mux.Handle(unspecifiedProc, connect.NewUnaryHandler(
		unspecifiedProc,
		func(_ context.Context, _ *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error) {
			return validResponse(t, edgev1.GetEdgeResponse_builder{
				Edge: validEdgeRecord(t),
			}.Build()), nil
		},
		connect.WithSchema(unspecifiedMD),
		connect.WithInterceptors(authnInterceptor, authzInterceptor),
	))

	mode99Rule := authzv1.Rule_builder{
		Mode:       authzv1.RuleMode(99).Enum(),
		ObjectType: proto.String("edge"),
		Relation:   proto.String("view"),
	}.Build()
	var mode99VErr *protovalidate.ValidationError
	if err := protovalidate.Validate(mode99Rule); err == nil {
		t.Fatal("expected mode 99 rule to fail protovalidate, got nil")
	} else if !errors.As(err, &mode99VErr) || len(mode99VErr.Violations) != 1 || mode99VErr.Violations[0].FieldDescriptor == nil || mode99VErr.Violations[0].FieldDescriptor.Name() != "mode" {
		t.Fatalf("expected mode 99 rule to fail on mode alone, got: %v", err)
	}
	mode99MD := buildSyntheticMethod(t, "Mode99Method", mode99Rule)
	mode99Proc := "/" + string(mode99MD.Parent().FullName()) + "/" + string(mode99MD.Name())
	mux.Handle(mode99Proc, connect.NewUnaryHandler(
		mode99Proc,
		func(_ context.Context, _ *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error) {
			return validResponse(t, edgev1.GetEdgeResponse_builder{
				Edge: validEdgeRecord(t),
			}.Build()), nil
		},
		connect.WithSchema(mode99MD),
		connect.WithInterceptors(authnInterceptor, authzInterceptor),
	))

	loadedRule := authzv1.Rule_builder{
		Mode:       authzv1.RuleMode_RULE_MODE_LOADED.Enum(),
		ObjectType: proto.String("edge"),
		Relation:   proto.String("view"),
	}.Build()
	if err := protovalidate.Validate(loadedRule); err != nil {
		t.Fatalf("loaded rule fails protovalidate: %v", err)
	}
	loadedMD := buildSyntheticMethod(t, "LoadedModeMethod", loadedRule)
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
			return validResponse(t, edgev1.GetEdgeResponse_builder{
				Edge: validEdgeRecord(t),
			}.Build()), nil
		},
		connect.WithSchema(loadedMD),
		connect.WithInterceptors(authnInterceptor, outerInterceptor, authzInterceptor),
	))

	noRuleMD := buildSyntheticMethod(t, "NoRuleMethod", nil)
	noRuleProc := "/" + string(noRuleMD.Parent().FullName()) + "/" + string(noRuleMD.Name())
	mux.Handle(noRuleProc, connect.NewUnaryHandler(
		noRuleProc,
		func(_ context.Context, _ *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error) {
			return validResponse(t, edgev1.GetEdgeResponse_builder{
				Edge: validEdgeRecord(t),
			}.Build()), nil
		},
		connect.WithSchema(noRuleMD),
		connect.WithInterceptors(authnInterceptor, authzInterceptor),
	))

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := server.Client()
	return &testEnv{
		server:           server,
		checker:          checker,
		handlers:         handlers,
		captureCli:       capturev1connect.NewCaptureServiceClient(client, server.URL),
		edgeCli:          edgev1connect.NewEdgeAdminServiceClient(client, server.URL),
		tenantCli:        identityv1connect.NewTenantServiceClient(client, server.URL),
		edgeAttachCli:    attachv1connect.NewEdgeServiceClient(client, server.URL),
		interceptor:      authzInterceptor,
		outerInterceptor: outerInterceptor,
		inner:            inner,
		unspecifiedCli:   connect.NewClient[edgev1.GetEdgeRequest, edgev1.GetEdgeResponse](client, server.URL+unspecifiedProc),
		mode99Cli:        connect.NewClient[edgev1.GetEdgeRequest, edgev1.GetEdgeResponse](client, server.URL+mode99Proc),
		loadedCli:        connect.NewClient[edgev1.GetEdgeRequest, edgev1.GetEdgeResponse](client, server.URL+loadedProc),
		noRuleCli:        connect.NewClient[edgev1.GetEdgeRequest, edgev1.GetEdgeResponse](client, server.URL+noRuleProc),
		setLoadedFn: func(fn func(ctx context.Context, req *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error)) {
			loadedMu.Lock()
			currentLoadedFn = fn
			loadedMu.Unlock()
		},
	}
}

func TestNoPrincipalIsUnauthenticated(t *testing.T) {
	env := setupTestEnv(t)

	req := validRequest(t, edgev1.GetEdgeRequest_builder{
		Edge: edgeRef(t),
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

	t.Run("omitted principal with no tenant header fails Unauthenticated before tenant header is read", func(t *testing.T) {
		env.checker.Reset()
		req := validRequest(t, edgev1.GetEdgeRequest_builder{
			Edge: edgeRef(t),
		}.Build())
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
	})

	t.Run("no rule method with no principal fails PermissionDenied", func(t *testing.T) {
		env.checker.Reset()
		noRuleReq := validRequest(t, edgev1.GetEdgeRequest_builder{
			Edge: edgeRef(t),
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

	t.Run("principal with empty ID in context fails Unauthenticated", func(t *testing.T) {
		env.checker.Reset()
		req := validRequest(t, edgev1.GetEdgeRequest_builder{
			Edge: edgeRef(t),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)
		req.Header().Set("X-Test-Principal-ID", "")

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
	})
}

func TestTenantHeaderValidation(t *testing.T) {
	env := setupTestEnv(t)

	t.Run("call without X-FlowSeer-Tenant header fails InvalidArgument", func(t *testing.T) {
		env.checker.Reset()
		env.handlers.Reset()

		req := validRequest(t, edgev1.GetEdgeRequest_builder{
			Edge: edgeRef(t),
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

		req := validRequest(t, edgev1.GetEdgeRequest_builder{
			Edge: edgeRef(t),
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

		req := validRequest(t, edgev1.GetEdgeRequest_builder{
			Edge: edgeRef(t),
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

	req := validRequest(t, edgev1.GetEdgeRequest_builder{
		Edge: edgeRef(t),
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

		req := validRequest(t, capturev1.GetCaptureSessionRequest_builder{
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

		req := validRequest(t, capturev1.GetCaptureSessionRequest_builder{
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

		req := validRequest(t, capturev1.GetCaptureSessionRequest_builder{
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

		req := validRequest(t, edgev1.CreateEdgeRequest_builder{
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

		req := validRequest(t, edgev1.CreateEdgeRequest_builder{
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
		req := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
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
			return validResponse(t, capturev1.CreateCaptureSessionResponse_builder{
				Session: validCaptureSessionRecord(t),
			}.Build()), nil
		})

		req := validRequest(t, validCreateCaptureSessionRequest(t))
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
			return validResponse(t, edgev1.CreateEdgeResponse_builder{
				Edge:         validEdgeRecord(t),
				Provisioning: validEdgeProvisioning(t),
			}.Build()), nil
		})

		req := validRequest(t, edgev1.CreateEdgeRequest_builder{
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
			return validResponse(t, capturev1.ListCaptureSessionsResponse_builder{}.Build()), nil
		})

		req := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
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
			return validResponse(t, capturev1.CreateCaptureSessionResponse_builder{
				Session: validCaptureSessionRecord(t),
			}.Build()), nil
		})

		req := validRequest(t, validCreateCaptureSessionRequest(t))
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
			return validResponse(t, capturev1.CreateCaptureSessionResponse_builder{
				Session: validCaptureSessionRecord(t),
			}.Build()), nil
		})

		req := validRequest(t, validCreateCaptureSessionRequest(t))
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

	t.Run("CreateCaptureSession Require checker error ignored yields Internal", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.SetFail(func(q authz.Query) error {
			if q.Object == "tenant:"+validTenantID && q.Relation == "full_payload" {
				return errors.New("simulated checker failure")
			}
			return nil
		})

		env.handlers.SetCreateCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.CreateCaptureSessionRequest]) (*connect.Response[capturev1.CreateCaptureSessionResponse], error) {
			admitted, _ := tenant.FromContext(ctx)
			_ = authz.Require(ctx, "full_payload", "tenant", admitted)
			return validResponse(t, capturev1.CreateCaptureSessionResponse_builder{
				Session: validCaptureSessionRecord(t),
			}.Build()), nil
		})

		req := validRequest(t, validCreateCaptureSessionRequest(t))
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		resp, err := env.captureCli.CreateCaptureSession(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if resp != nil {
			t.Errorf("expected nil response, got %v", resp)
		}
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeInternal)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeObligationViolation.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeObligationViolation)
		}
		if got := retryDispositionOf(t, err); got == errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
			t.Errorf("got retry disposition %v, want non-retryable", got)
		}
	})

	t.Run("CreateCaptureSession Require checker error returned yields Unavailable", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.SetFail(func(q authz.Query) error {
			if q.Object == "tenant:"+validTenantID && q.Relation == "full_payload" {
				return errors.New("simulated checker failure")
			}
			return nil
		})

		env.handlers.SetCreateCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.CreateCaptureSessionRequest]) (*connect.Response[capturev1.CreateCaptureSessionResponse], error) {
			admitted, _ := tenant.FromContext(ctx)
			if err := authz.Require(ctx, "full_payload", "tenant", admitted); err != nil {
				return nil, err
			}
			return validResponse(t, capturev1.CreateCaptureSessionResponse_builder{
				Session: validCaptureSessionRecord(t),
			}.Build()), nil
		})

		req := validRequest(t, validCreateCaptureSessionRequest(t))
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.captureCli.CreateCaptureSession(context.Background(), req)
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
	})

	t.Run("ListCaptureSessions calling Filter passes response through", func(t *testing.T) {
		env := setupTestEnv(t)
		env.handlers.SetListCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
			_, err := authz.Filter(ctx, "capture", "edge", []string{validEdgeID, validEdgeID2})
			if err != nil {
				return nil, err
			}
			return validResponse(t, capturev1.ListCaptureSessionsResponse_builder{}.Build()), nil
		})

		req := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
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

		req := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.captureCli.ListCaptureSessions(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeNotFound)
		}
	})

	t.Run("ListCaptureSessions Filter checker error ignored yields Internal", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.SetFail(func(q authz.Query) error {
			if strings.HasPrefix(q.Object, "edge:") {
				return errors.New("simulated checker failure")
			}
			return nil
		})

		env.handlers.SetListCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
			_, _ = authz.Filter(ctx, "capture", "edge", []string{validEdgeID})
			return validResponse(t, capturev1.ListCaptureSessionsResponse_builder{}.Build()), nil
		})

		req := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		resp, err := env.captureCli.ListCaptureSessions(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if resp != nil {
			t.Errorf("expected nil response, got %v", resp)
		}
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeInternal)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeObligationViolation.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeObligationViolation)
		}
		if got := retryDispositionOf(t, err); got == errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
			t.Errorf("got retry disposition %v, want non-retryable", got)
		}
	})

	t.Run("ListCaptureSessions Filter checker error returned yields Unavailable", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.SetFail(func(q authz.Query) error {
			if strings.HasPrefix(q.Object, "edge:") {
				return errors.New("simulated checker failure")
			}
			return nil
		})

		env.handlers.SetListCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
			_, err := authz.Filter(ctx, "capture", "edge", []string{validEdgeID})
			if err != nil {
				return nil, err
			}
			return validResponse(t, capturev1.ListCaptureSessionsResponse_builder{}.Build()), nil
		})

		req := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.captureCli.ListCaptureSessions(context.Background(), req)
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
	})

	t.Run("Require on non-tenant object under filtered rule discharges obligation and verifies queries", func(t *testing.T) {
		t.Run("allowed passes response through", func(t *testing.T) {
			env := setupTestEnv(t)
			env.handlers.SetListCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
				if err := authz.Require(ctx, "capture", "edge", validEdgeID); err != nil {
					return nil, err
				}
				return validResponse(t, capturev1.ListCaptureSessionsResponse_builder{}.Build()), nil
			})

			req := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
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
				return validResponse(t, capturev1.ListCaptureSessionsResponse_builder{}.Build()), nil
			})

			req := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
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
				return validResponse(t, capturev1.ListCaptureSessionsResponse_builder{}.Build()), nil
			})

			req := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
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

func tuplesMatch(a, b []authz.Tuple) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[authz.Tuple]int, len(a))
	for _, t := range a {
		counts[t]++
	}
	for _, t := range b {
		counts[t]--
		if counts[t] < 0 {
			return false
		}
	}
	return true
}

func queryEquals(a, b authz.Query) bool {
	if a.Object != b.Object || a.Relation != b.Relation || a.User != b.User {
		return false
	}
	return tuplesMatch(a.ContextualTuples, b.ContextualTuples)
}

func assertGroupsEqual(t *testing.T, got, want [][]authz.Query) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d query groups, want %d: got=%+v want=%+v", len(got), len(want), got, want)
	}
	for g := range want {
		if len(got[g]) != len(want[g]) {
			t.Fatalf("group %d got %d queries, want %d: got=%+v want=%+v", g, len(got[g]), len(want[g]), got[g], want[g])
		}
		for j := range want[g] {
			if !queryEquals(got[g][j], want[g][j]) {
				t.Errorf("group %d query %d = %+v, want %+v", g, j, got[g][j], want[g][j])
			}
		}
	}
}

type shapeOutcome struct {
	callErr     error
	handlerRan  bool
	requireErr  error
	filterErr   error
	filteredIDs []string
}

type callShape struct {
	name               string
	wantQueries        []authz.Query
	interceptorQueries int
	usesFilter         bool
	usesRequire        bool
	run                func(t *testing.T, env *testEnv, defaultPrincipal bool) shapeOutcome
}

func TestQueryPropertyEnumeration(t *testing.T) {
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

	shapes := []callShape{
		{
			name: "platform rule",
			wantQueries: []authz.Query{
				{
					Object:           "platform:flowseer",
					Relation:         "admin",
					User:             "user:" + testPrincipalID,
					ContextualTuples: wantTuples,
				},
			},
			interceptorQueries: 1,
			usesFilter:         false,
			usesRequire:        false,
			run: func(t *testing.T, env *testEnv, defaultPrincipal bool) shapeOutcome {
				var (
					mu         sync.Mutex
					handlerRan bool
				)
				env.handlers.SetListTenantsFn(func(_ context.Context, _ *connect.Request[identityv1.ListTenantsRequest]) (*connect.Response[identityv1.ListTenantsResponse], error) {
					mu.Lock()
					handlerRan = true
					mu.Unlock()
					return validResponse(t, identityv1.ListTenantsResponse_builder{}.Build()), nil
				})
				req := validRequest(t, identityv1.ListTenantsRequest_builder{}.Build())
				if !defaultPrincipal {
					req.Header().Set("X-Test-Principal-Tenants", validTenantID+","+validTenantID2)
					req.Header().Set("X-Test-Principal-Platform", "true")
				}
				_, err := env.tenantCli.ListTenants(context.Background(), req)
				mu.Lock()
				ran := handlerRan
				mu.Unlock()
				return shapeOutcome{callErr: err, handlerRan: ran}
			},
		},
		{
			name: "tenant rule",
			wantQueries: []authz.Query{
				{
					Object:           "tenant:" + validTenantID,
					Relation:         "member",
					User:             "user:" + testPrincipalID,
					ContextualTuples: wantTuples,
				},
				{
					Object:           "tenant:" + validTenantID,
					Relation:         "admin",
					User:             "user:" + testPrincipalID,
					ContextualTuples: wantTuples,
				},
			},
			interceptorQueries: 2,
			usesFilter:         false,
			usesRequire:        false,
			run: func(t *testing.T, env *testEnv, defaultPrincipal bool) shapeOutcome {
				var (
					mu         sync.Mutex
					handlerRan bool
				)
				env.handlers.SetCreateEdgeFn(func(_ context.Context, _ *connect.Request[edgev1.CreateEdgeRequest]) (*connect.Response[edgev1.CreateEdgeResponse], error) {
					mu.Lock()
					handlerRan = true
					mu.Unlock()
					return validResponse(t, edgev1.CreateEdgeResponse_builder{
						Edge:         validEdgeRecord(t),
						Provisioning: validEdgeProvisioning(t),
					}.Build()), nil
				})
				req := validRequest(t, edgev1.CreateEdgeRequest_builder{
					Name: proto.String("edge-alpha"),
				}.Build())
				req.Header().Set("X-FlowSeer-Tenant", validTenantID)
				if !defaultPrincipal {
					req.Header().Set("X-Test-Principal-Tenants", validTenantID+","+validTenantID2)
					req.Header().Set("X-Test-Principal-Platform", "true")
				}
				_, err := env.edgeCli.CreateEdge(context.Background(), req)
				mu.Lock()
				ran := handlerRan
				mu.Unlock()
				return shapeOutcome{callErr: err, handlerRan: ran}
			},
		},
		{
			name: "request rule",
			wantQueries: []authz.Query{
				{
					Object:           "tenant:" + validTenantID,
					Relation:         "member",
					User:             "user:" + testPrincipalID,
					ContextualTuples: wantTuples,
				},
				{
					Object:           "edge:" + validEdgeID,
					Relation:         "view",
					User:             "user:" + testPrincipalID,
					ContextualTuples: wantTuples,
				},
				{
					Object:           "edge:" + validEdgeID,
					Relation:         "tenant",
					User:             "tenant:" + validTenantID,
					ContextualTuples: wantTuples,
				},
			},
			interceptorQueries: 3,
			usesFilter:         false,
			usesRequire:        false,
			run: func(t *testing.T, env *testEnv, defaultPrincipal bool) shapeOutcome {
				var (
					mu         sync.Mutex
					handlerRan bool
				)
				env.handlers.SetGetEdgeFn(func(_ context.Context, _ *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error) {
					mu.Lock()
					handlerRan = true
					mu.Unlock()
					return validResponse(t, edgev1.GetEdgeResponse_builder{
						Edge: validEdgeRecord(t),
					}.Build()), nil
				})
				req := validRequest(t, edgev1.GetEdgeRequest_builder{
					Edge: edgeRef(t),
				}.Build())
				req.Header().Set("X-FlowSeer-Tenant", validTenantID)
				if !defaultPrincipal {
					req.Header().Set("X-Test-Principal-Tenants", validTenantID+","+validTenantID2)
					req.Header().Set("X-Test-Principal-Platform", "true")
				}
				_, err := env.edgeCli.GetEdge(context.Background(), req)
				mu.Lock()
				ran := handlerRan
				mu.Unlock()
				return shapeOutcome{callErr: err, handlerRan: ran}
			},
		},
		{
			name: "request rule whose handler calls Require on the admitted tenant",
			wantQueries: []authz.Query{
				{
					Object:           "tenant:" + validTenantID,
					Relation:         "member",
					User:             "user:" + testPrincipalID,
					ContextualTuples: wantTuples,
				},
				{
					Object:           "edge:" + validEdgeID,
					Relation:         "capture",
					User:             "user:" + testPrincipalID,
					ContextualTuples: wantTuples,
				},
				{
					Object:           "edge:" + validEdgeID,
					Relation:         "tenant",
					User:             "tenant:" + validTenantID,
					ContextualTuples: wantTuples,
				},
				{
					Object:           "tenant:" + validTenantID,
					Relation:         "full_payload",
					User:             "user:" + testPrincipalID,
					ContextualTuples: wantTuples,
				},
			},
			interceptorQueries: 3,
			usesFilter:         false,
			usesRequire:        true,
			run: func(t *testing.T, env *testEnv, defaultPrincipal bool) shapeOutcome {
				var (
					mu         sync.Mutex
					handlerRan bool
					requireErr error
				)
				env.handlers.SetCreateCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.CreateCaptureSessionRequest]) (*connect.Response[capturev1.CreateCaptureSessionResponse], error) {
					admitted, _ := tenant.FromContext(ctx)
					rErr := authz.Require(ctx, "full_payload", "tenant", admitted)
					mu.Lock()
					handlerRan = true
					requireErr = rErr
					mu.Unlock()
					if rErr != nil {
						return nil, rErr
					}
					return validResponse(t, capturev1.CreateCaptureSessionResponse_builder{
						Session: validCaptureSessionRecord(t),
					}.Build()), nil
				})
				req := validRequest(t, validCreateCaptureSessionRequest(t))
				req.Header().Set("X-FlowSeer-Tenant", validTenantID)
				if !defaultPrincipal {
					req.Header().Set("X-Test-Principal-Tenants", validTenantID+","+validTenantID2)
					req.Header().Set("X-Test-Principal-Platform", "true")
				}
				_, err := env.captureCli.CreateCaptureSession(context.Background(), req)
				mu.Lock()
				ran := handlerRan
				rErr := requireErr
				mu.Unlock()
				return shapeOutcome{callErr: err, handlerRan: ran, requireErr: rErr}
			},
		},
		{
			name: "filtered rule with Filter",
			wantQueries: []authz.Query{
				{
					Object:           "tenant:" + validTenantID,
					Relation:         "member",
					User:             "user:" + testPrincipalID,
					ContextualTuples: wantTuples,
				},
				{
					Object:           "edge:" + validEdgeID,
					Relation:         "capture",
					User:             "user:" + testPrincipalID,
					ContextualTuples: wantTuples,
				},
				{
					Object:           "edge:" + validEdgeID,
					Relation:         "tenant",
					User:             "tenant:" + validTenantID,
					ContextualTuples: wantTuples,
				},
			},
			interceptorQueries: 1,
			usesFilter:         true,
			usesRequire:        false,
			run: func(t *testing.T, env *testEnv, defaultPrincipal bool) shapeOutcome {
				var (
					mu          sync.Mutex
					handlerRan  bool
					filterErr   error
					filteredIDs []string
				)
				env.handlers.SetListCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
					ids, fErr := authz.Filter(ctx, "capture", "edge", []string{validEdgeID})
					mu.Lock()
					handlerRan = true
					filterErr = fErr
					filteredIDs = ids
					mu.Unlock()
					if fErr != nil {
						return nil, fErr
					}
					return validResponse(t, capturev1.ListCaptureSessionsResponse_builder{}.Build()), nil
				})
				req := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
				req.Header().Set("X-FlowSeer-Tenant", validTenantID)
				if !defaultPrincipal {
					req.Header().Set("X-Test-Principal-Tenants", validTenantID+","+validTenantID2)
					req.Header().Set("X-Test-Principal-Platform", "true")
				}
				_, err := env.captureCli.ListCaptureSessions(context.Background(), req)
				mu.Lock()
				ran := handlerRan
				fErr := filterErr
				fIDs := slices.Clone(filteredIDs)
				mu.Unlock()
				return shapeOutcome{callErr: err, handlerRan: ran, filterErr: fErr, filteredIDs: fIDs}
			},
		},
		{
			name: "filtered rule with Require on a non-tenant object",
			wantQueries: []authz.Query{
				{
					Object:           "tenant:" + validTenantID,
					Relation:         "member",
					User:             "user:" + testPrincipalID,
					ContextualTuples: wantTuples,
				},
				{
					Object:           "edge:" + validEdgeID,
					Relation:         "capture",
					User:             "user:" + testPrincipalID,
					ContextualTuples: wantTuples,
				},
				{
					Object:           "edge:" + validEdgeID,
					Relation:         "tenant",
					User:             "tenant:" + validTenantID,
					ContextualTuples: wantTuples,
				},
			},
			interceptorQueries: 1,
			usesFilter:         false,
			usesRequire:        true,
			run: func(t *testing.T, env *testEnv, defaultPrincipal bool) shapeOutcome {
				var (
					mu         sync.Mutex
					handlerRan bool
					requireErr error
				)
				env.handlers.SetListCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
					rErr := authz.Require(ctx, "capture", "edge", validEdgeID)
					mu.Lock()
					handlerRan = true
					requireErr = rErr
					mu.Unlock()
					if rErr != nil {
						return nil, rErr
					}
					return validResponse(t, capturev1.ListCaptureSessionsResponse_builder{}.Build()), nil
				})
				req := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
				req.Header().Set("X-FlowSeer-Tenant", validTenantID)
				if !defaultPrincipal {
					req.Header().Set("X-Test-Principal-Tenants", validTenantID+","+validTenantID2)
					req.Header().Set("X-Test-Principal-Platform", "true")
				}
				_, err := env.captureCli.ListCaptureSessions(context.Background(), req)
				mu.Lock()
				ran := handlerRan
				rErr := requireErr
				mu.Unlock()
				return shapeOutcome{callErr: err, handlerRan: ran, requireErr: rErr}
			},
		},
		{
			name: "loaded rule with Require",
			wantQueries: []authz.Query{
				{
					Object:           "tenant:" + validTenantID,
					Relation:         "member",
					User:             "user:" + testPrincipalID,
					ContextualTuples: wantTuples,
				},
				{
					Object:           "edge:" + validEdgeID,
					Relation:         "view",
					User:             "user:" + testPrincipalID,
					ContextualTuples: wantTuples,
				},
				{
					Object:           "edge:" + validEdgeID,
					Relation:         "tenant",
					User:             "tenant:" + validTenantID,
					ContextualTuples: wantTuples,
				},
			},
			interceptorQueries: 1,
			usesFilter:         false,
			usesRequire:        true,
			run: func(t *testing.T, env *testEnv, defaultPrincipal bool) shapeOutcome {
				var (
					mu         sync.Mutex
					handlerRan bool
					requireErr error
				)
				env.setLoadedFn(func(ctx context.Context, _ *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error) {
					rErr := authz.Require(ctx, "view", "edge", validEdgeID)
					mu.Lock()
					handlerRan = true
					requireErr = rErr
					mu.Unlock()
					if rErr != nil {
						return nil, rErr
					}
					return validResponse(t, edgev1.GetEdgeResponse_builder{
						Edge: validEdgeRecord(t),
					}.Build()), nil
				})
				req := validRequest(t, edgev1.GetEdgeRequest_builder{
					Edge: edgeRef(t),
				}.Build())
				req.Header().Set("X-FlowSeer-Tenant", validTenantID)
				if !defaultPrincipal {
					req.Header().Set("X-Test-Principal-Tenants", validTenantID+","+validTenantID2)
					req.Header().Set("X-Test-Principal-Platform", "true")
				}
				_, err := env.loadedCli.CallUnary(context.Background(), req)
				mu.Lock()
				ran := handlerRan
				rErr := requireErr
				mu.Unlock()
				return shapeOutcome{callErr: err, handlerRan: ran, requireErr: rErr}
			},
		},
	}

	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			allowAllEnv := setupTestEnv(t)
			allowAllOut := shape.run(t, allowAllEnv, false)
			if allowAllOut.callErr != nil {
				t.Fatalf("allow-all call failed: %v", allowAllOut.callErr)
			}
			if !allowAllOut.handlerRan {
				t.Fatal("allow-all handler did not run")
			}
			if shape.usesFilter {
				if !slices.Equal(allowAllOut.filteredIDs, []string{validEdgeID}) {
					t.Errorf("allow-all filtered IDs = %v, want [%s]", allowAllOut.filteredIDs, validEdgeID)
				}
				if allowAllOut.filterErr != nil {
					t.Errorf("allow-all filter error = %v, want nil", allowAllOut.filterErr)
				}
			}
			if shape.usesRequire {
				if allowAllOut.requireErr != nil {
					t.Errorf("allow-all require error = %v, want nil", allowAllOut.requireErr)
				}
			}

			recorded := allowAllEnv.checker.Recorded()
			if len(recorded) != len(shape.wantQueries) {
				t.Fatalf("allow-all recorded %d queries, want %d", len(recorded), len(shape.wantQueries))
			}
			for i, want := range shape.wantQueries {
				if !queryEquals(recorded[i], want) {
					t.Errorf("allow-all query %d = %+v, want %+v", i, recorded[i], want)
				}
			}

			allowAllGroups := allowAllEnv.checker.Groups()

			// Second allow-all pass with the default principal (one tenant, no platform claim).
			defaultAllowAllEnv := setupTestEnv(t)
			defaultAllowAllOut := shape.run(t, defaultAllowAllEnv, true)
			if defaultAllowAllOut.callErr != nil {
				t.Fatalf("default-principal allow-all call failed: %v", defaultAllowAllOut.callErr)
			}
			if !defaultAllowAllOut.handlerRan {
				t.Fatal("default-principal allow-all handler did not run")
			}
			if shape.usesFilter {
				if !slices.Equal(defaultAllowAllOut.filteredIDs, []string{validEdgeID}) {
					t.Errorf("default-principal allow-all filtered IDs = %v, want [%s]", defaultAllowAllOut.filteredIDs, validEdgeID)
				}
				if defaultAllowAllOut.filterErr != nil {
					t.Errorf("default-principal allow-all filter error = %v, want nil", defaultAllowAllOut.filterErr)
				}
			}
			if shape.usesRequire {
				if defaultAllowAllOut.requireErr != nil {
					t.Errorf("default-principal allow-all require error = %v, want nil", defaultAllowAllOut.requireErr)
				}
			}
			wantDefaultTuples := []authz.Tuple{
				{
					Object:   "tenant:" + validTenantID,
					Relation: "claimed",
					User:     "user:" + testPrincipalID,
				},
			}
			defaultRecorded := defaultAllowAllEnv.checker.Recorded()
			if len(defaultRecorded) != len(shape.wantQueries) {
				t.Fatalf("default-principal allow-all recorded %d queries, want %d", len(defaultRecorded), len(shape.wantQueries))
			}
			for i, q := range defaultRecorded {
				if !tuplesMatch(q.ContextualTuples, wantDefaultTuples) {
					t.Errorf("default-principal query %d tuples = %+v, want %+v", i, q.ContextualTuples, wantDefaultTuples)
				}
			}

			for i, targetQuery := range recorded {
				isInterceptor := i < shape.interceptorQueries
				target := targetQuery

				targetGroupIdx := -1
				queryCursor := 0
				for gIdx, group := range allowAllGroups {
					if i >= queryCursor && i < queryCursor+len(group) {
						targetGroupIdx = gIdx
						break
					}
					queryCursor += len(group)
				}
				if targetGroupIdx == -1 {
					t.Fatalf("query %d not found in allow-all groups", i)
				}

				t.Run(fmt.Sprintf("query_%d_fails", i), func(t *testing.T) {
					failEnv := setupTestEnv(t)
					var matchCount atomic.Int64
					failEnv.checker.SetFail(func(q authz.Query) error {
						if queryEquals(q, target) {
							matchCount.Add(1)
							return errors.New("simulated checker failure")
						}
						return nil
					})

					out := shape.run(t, failEnv, false)

					if got := matchCount.Load(); got != 1 {
						t.Fatalf("fail hook matched %d queries, want 1", got)
					}

					wantFailGroups := allowAllGroups[:targetGroupIdx+1]
					assertGroupsEqual(t, failEnv.checker.Groups(), wantFailGroups)

					switch {
					case isInterceptor:
						if out.handlerRan {
							t.Error("handler ran, want not run")
						}
						if connect.CodeOf(out.callErr) != connect.CodeUnavailable {
							t.Errorf("call code = %v, want %v", connect.CodeOf(out.callErr), connect.CodeUnavailable)
						}
						if got := errCodeOf(t, out.callErr); got != authz.ErrCodeUnavailable.String() {
							t.Errorf("call error code = %q, want %q", got, authz.ErrCodeUnavailable)
						}
						if got := retryDispositionOf(t, out.callErr); got != errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
							t.Errorf("call retry disposition = %v, want retryable", got)
						}
						if !strings.Contains(out.callErr.Error(), "authorization is unavailable") {
							t.Errorf("call message = %q, want containing 'authorization is unavailable'", out.callErr.Error())
						}
					case shape.usesFilter:
						if !out.handlerRan {
							t.Error("handler did not run, want run")
						}
						if connect.CodeOf(out.filterErr) != connect.CodeUnavailable {
							t.Errorf("Filter code = %v, want %v", connect.CodeOf(out.filterErr), connect.CodeUnavailable)
						}
						if got := errCodeOf(t, out.filterErr); got != authz.ErrCodeUnavailable.String() {
							t.Errorf("Filter error code = %q, want %q", got, authz.ErrCodeUnavailable)
						}
						if got := retryDispositionOf(t, out.filterErr); got != errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
							t.Errorf("Filter retry disposition = %v, want retryable", got)
						}
						if !strings.Contains(out.filterErr.Error(), "authorization is unavailable") {
							t.Errorf("Filter message = %q, want containing 'authorization is unavailable'", out.filterErr.Error())
						}
						if connect.CodeOf(out.callErr) != connect.CodeUnavailable {
							t.Errorf("call code = %v, want %v", connect.CodeOf(out.callErr), connect.CodeUnavailable)
						}
						if got := errCodeOf(t, out.callErr); got != authz.ErrCodeUnavailable.String() {
							t.Errorf("call error code = %q, want %q", got, authz.ErrCodeUnavailable)
						}
						if got := retryDispositionOf(t, out.callErr); got != errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
							t.Errorf("call retry disposition = %v, want retryable", got)
						}
					default:
						if !out.handlerRan {
							t.Error("handler did not run, want run")
						}
						if connect.CodeOf(out.requireErr) != connect.CodeUnavailable {
							t.Errorf("Require code = %v, want %v", connect.CodeOf(out.requireErr), connect.CodeUnavailable)
						}
						if got := errCodeOf(t, out.requireErr); got != authz.ErrCodeUnavailable.String() {
							t.Errorf("Require error code = %q, want %q", got, authz.ErrCodeUnavailable)
						}
						if got := retryDispositionOf(t, out.requireErr); got != errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
							t.Errorf("Require retry disposition = %v, want retryable", got)
						}
						if !strings.Contains(out.requireErr.Error(), "authorization is unavailable") {
							t.Errorf("Require message = %q, want containing 'authorization is unavailable'", out.requireErr.Error())
						}
						if connect.CodeOf(out.callErr) != connect.CodeUnavailable {
							t.Errorf("call code = %v, want %v", connect.CodeOf(out.callErr), connect.CodeUnavailable)
						}
						if got := errCodeOf(t, out.callErr); got != authz.ErrCodeUnavailable.String() {
							t.Errorf("call error code = %q, want %q", got, authz.ErrCodeUnavailable)
						}
						if got := retryDispositionOf(t, out.callErr); got != errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
							t.Errorf("call retry disposition = %v, want retryable", got)
						}
					}
				})

				t.Run(fmt.Sprintf("query_%d_denied", i), func(t *testing.T) {
					denyEnv := setupTestEnv(t)
					var matchCount atomic.Int64
					denyEnv.checker.SetDeny(func(q authz.Query) bool {
						if queryEquals(q, target) {
							matchCount.Add(1)
							return true
						}
						return false
					})

					out := shape.run(t, denyEnv, false)

					if got := matchCount.Load(); got != 1 {
						t.Fatalf("deny hook matched %d queries, want 1", got)
					}

					var wantDenyGroups [][]authz.Query
					if shape.usesFilter && !isInterceptor {
						wantDenyGroups = allowAllGroups
					} else {
						wantDenyGroups = allowAllGroups[:targetGroupIdx+1]
					}
					assertGroupsEqual(t, denyEnv.checker.Groups(), wantDenyGroups)

					switch {
					case isInterceptor:
						if out.handlerRan {
							t.Error("handler ran, want not run")
						}
						if connect.CodeOf(out.callErr) != connect.CodePermissionDenied {
							t.Errorf("call code = %v, want %v", connect.CodeOf(out.callErr), connect.CodePermissionDenied)
						}
						if got := errCodeOf(t, out.callErr); got != authz.ErrCodeDenied.String() {
							t.Errorf("call error code = %q, want %q", got, authz.ErrCodeDenied)
						}
					case shape.usesFilter:
						if !out.handlerRan {
							t.Error("handler did not run, want run")
						}
						if out.filterErr != nil {
							t.Errorf("Filter returned error: %v, want nil", out.filterErr)
						}
						if slices.Contains(out.filteredIDs, validEdgeID) {
							t.Errorf("Filter returned id %q, want left out", validEdgeID)
						}
						if out.callErr != nil {
							t.Errorf("call returned error: %v, want nil", out.callErr)
						}
					default:
						if !out.handlerRan {
							t.Error("handler did not run, want run")
						}
						if connect.CodeOf(out.requireErr) != connect.CodePermissionDenied {
							t.Errorf("Require code = %v, want %v", connect.CodeOf(out.requireErr), connect.CodePermissionDenied)
						}
						if got := errCodeOf(t, out.requireErr); got != authz.ErrCodeDenied.String() {
							t.Errorf("Require error code = %q, want %q", got, authz.ErrCodeDenied)
						}
						if connect.CodeOf(out.callErr) != connect.CodePermissionDenied {
							t.Errorf("call code = %v, want %v", connect.CodeOf(out.callErr), connect.CodePermissionDenied)
						}
						if got := errCodeOf(t, out.callErr); got != authz.ErrCodeDenied.String() {
							t.Errorf("call error code = %q, want %q", got, authz.ErrCodeDenied)
						}
					}
				})
			}
		})
	}
}

func TestStreamingCallAuthorization(t *testing.T) {
	t.Run("allowed download", func(t *testing.T) {
		env := setupTestEnv(t)
		req := validRequest(t, capturev1.DownloadCaptureSessionRequest_builder{
			Session: sessionRef(t, validSessionID),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		stream, err := env.captureCli.DownloadCaptureSession(context.Background(), req)
		if err != nil {
			t.Fatalf("DownloadCaptureSession: %v", err)
		}
		for stream.Receive() {
		}
		if err := stream.Err(); err != nil {
			t.Fatalf("stream error: %v", err)
		}

		recorded := env.checker.Recorded()
		if len(recorded) != 3 {
			t.Fatalf("recorded queries count = %d, want 3: %v", len(recorded), recorded)
		}
		// Query 1: membership
		if recorded[0].Object != "tenant:"+validTenantID || recorded[0].Relation != "member" {
			t.Errorf("query 0 = %v, want tenant member query", recorded[0])
		}
		// Query 2 & 3: download and tenant
		if recorded[1].Object != "capture_session:"+validSessionID || recorded[1].Relation != "download" {
			t.Errorf("query 1 = %v, want download query", recorded[1])
		}
		if recorded[2].Object != "capture_session:"+validSessionID || recorded[2].Relation != "tenant" {
			t.Errorf("query 2 = %v, want tenant query", recorded[2])
		}
		if !env.handlers.DidRun("DownloadCaptureSession") {
			t.Error("handler did not run")
		}
	})

	t.Run("denied membership", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.deny = func(q authz.Query) bool {
			return q.Object == "tenant:"+validTenantID && q.Relation == "member"
		}

		req := validRequest(t, capturev1.DownloadCaptureSessionRequest_builder{
			Session: sessionRef(t, validSessionID),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		stream, err := env.captureCli.DownloadCaptureSession(context.Background(), req)
		if err == nil {
			if stream.Receive() {
				t.Fatal("expected stream to fail, got item")
			}
			err = stream.Err()
		}

		recorded := env.checker.Recorded()
		if len(recorded) != 1 {
			t.Fatalf("recorded queries count = %d, want 1", len(recorded))
		}
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeDenied.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeDenied)
		}
		if env.handlers.DidRun("DownloadCaptureSession") {
			t.Error("handler ran, want not run")
		}
		if env.inner.entered.Load() {
			t.Error("inner handler interceptor ran before membership was admitted")
		}
	})

	t.Run("denied download relation", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.deny = func(q authz.Query) bool {
			return q.Object == "capture_session:"+validSessionID && q.Relation == "download"
		}

		req := validRequest(t, capturev1.DownloadCaptureSessionRequest_builder{
			Session: sessionRef(t, validSessionID),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		stream, err := env.captureCli.DownloadCaptureSession(context.Background(), req)
		if err == nil {
			if stream.Receive() {
				t.Fatal("expected stream to fail, got item")
			}
			err = stream.Err()
		}

		recorded := env.checker.Recorded()
		if len(recorded) != 3 {
			t.Fatalf("recorded queries count = %d, want 3", len(recorded))
		}
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeDenied.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeDenied)
		}
		if env.handlers.DidRun("DownloadCaptureSession") {
			t.Error("handler ran, want not run")
		}
	})

	t.Run("denied tenant relation", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.deny = func(q authz.Query) bool {
			return q.Object == "capture_session:"+validSessionID && q.Relation == "tenant"
		}

		req := validRequest(t, capturev1.DownloadCaptureSessionRequest_builder{
			Session: sessionRef(t, validSessionID),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		stream, err := env.captureCli.DownloadCaptureSession(context.Background(), req)
		if err == nil {
			if stream.Receive() {
				t.Fatal("expected stream to fail, got item")
			}
			err = stream.Err()
		}

		recorded := env.checker.Recorded()
		if len(recorded) != 3 {
			t.Fatalf("recorded queries count = %d, want 3", len(recorded))
		}
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeDenied.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeDenied)
		}
		if env.handlers.DidRun("DownloadCaptureSession") {
			t.Error("handler ran, want not run")
		}
	})

	t.Run("failing checker on stream", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.fail = func(q authz.Query) error {
			if q.Relation == "download" {
				return errors.New("openfga boom")
			}
			return nil
		}

		req := validRequest(t, capturev1.DownloadCaptureSessionRequest_builder{
			Session: sessionRef(t, validSessionID),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		stream, err := env.captureCli.DownloadCaptureSession(context.Background(), req)
		if err == nil {
			if stream.Receive() {
				t.Fatal("expected stream to fail, got item")
			}
			err = stream.Err()
		}

		recorded := env.checker.Recorded()
		if len(recorded) != 3 {
			t.Fatalf("recorded queries count = %d, want 3", len(recorded))
		}
		if connect.CodeOf(err) != connect.CodeUnavailable {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeUnavailable)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeUnavailable.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeUnavailable)
		}
		if env.handlers.DidRun("DownloadCaptureSession") {
			t.Error("handler ran, want not run")
		}
	})

	t.Run("no principal", func(t *testing.T) {
		env := setupTestEnv(t)
		req := connect.NewRequest(capturev1.DownloadCaptureSessionRequest_builder{
			Session: sessionRef(t, validSessionID),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)
		req.Header().Set("X-Test-Omit-Principal", "true")

		stream, err := env.captureCli.DownloadCaptureSession(context.Background(), req)
		if err == nil {
			if stream.Receive() {
				t.Fatal("expected stream to fail, got item")
			}
			err = stream.Err()
		}

		recorded := env.checker.Recorded()
		if len(recorded) != 0 {
			t.Fatalf("recorded queries count = %d, want 0", len(recorded))
		}
		if connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeUnauthenticated)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeUnauthenticated.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeUnauthenticated)
		}
		if env.handlers.DidRun("DownloadCaptureSession") {
			t.Error("handler ran, want not run")
		}
	})

	t.Run("no tenant header", func(t *testing.T) {
		env := setupTestEnv(t)
		req := validRequest(t, capturev1.DownloadCaptureSessionRequest_builder{
			Session: sessionRef(t, validSessionID),
		}.Build())

		stream, err := env.captureCli.DownloadCaptureSession(context.Background(), req)
		if err == nil {
			if stream.Receive() {
				t.Fatal("expected stream to fail, got item")
			}
			err = stream.Err()
		}

		recorded := env.checker.Recorded()
		if len(recorded) != 0 {
			t.Fatalf("recorded queries count = %d, want 0", len(recorded))
		}
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeInvalidArgument)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeNoTenant.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeNoTenant)
		}
		if env.handlers.DidRun("DownloadCaptureSession") {
			t.Error("handler ran, want not run")
		}
	})

	t.Run("request with no session", func(t *testing.T) {
		env := setupTestEnv(t)
		req := connect.NewRequest(&capturev1.DownloadCaptureSessionRequest{})
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		stream, err := env.captureCli.DownloadCaptureSession(context.Background(), req)
		if err == nil {
			if stream.Receive() {
				t.Fatal("expected stream to fail, got item")
			}
			err = stream.Err()
		}

		recorded := env.checker.Recorded()
		if len(recorded) != 1 {
			t.Fatalf("recorded queries count = %d, want 1 (membership check)", len(recorded))
		}
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeNoObjectID.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeNoObjectID)
		}
		if env.handlers.DidRun("DownloadCaptureSession") {
			t.Error("handler ran, want not run")
		}
	})

	t.Run("handler returning nil after a failed check yields Internal", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.deny = func(q authz.Query) bool {
			return q.Object == "edge:"+validEdgeID && q.Relation == "view"
		}
		env.handlers.SetDownloadCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.DownloadCaptureSessionRequest], _ *connect.ServerStream[capturev1.DownloadCaptureSessionResponse]) error {
			_ = authz.Require(ctx, "view", "edge", validEdgeID)
			return nil
		})

		req := validRequest(t, capturev1.DownloadCaptureSessionRequest_builder{
			Session: sessionRef(t, validSessionID),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)
		stream, err := env.captureCli.DownloadCaptureSession(context.Background(), req)
		if err == nil {
			if stream.Receive() {
				t.Fatal("expected stream to fail, got item")
			}
			err = stream.Err()
		}
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Fatalf("code = %v, want %v", connect.CodeOf(err), connect.CodeInternal)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeObligationViolation.String() {
			t.Fatalf("error code = %q, want %q", got, authz.ErrCodeObligationViolation)
		}
	})

	t.Run("handler error wins over an in-flight check", func(t *testing.T) {
		env := setupTestEnv(t)
		started := make(chan struct{})
		unblock := make(chan struct{})
		done := make(chan struct{})
		env.checker.fail = func(q authz.Query) error {
			if q.Object == "edge:"+validEdgeID && q.Relation == "view" {
				close(started)
				<-unblock
			}
			return nil
		}
		env.handlers.SetDownloadCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.DownloadCaptureSessionRequest], _ *connect.ServerStream[capturev1.DownloadCaptureSessionResponse]) error {
			spawn.Go(ctx, "test.stream.inflight.require", func() {
				defer close(done)
				_ = authz.Require(ctx, "view", "edge", validEdgeID)
			})
			<-started
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("handler sentinel"))
		})

		req := validRequest(t, capturev1.DownloadCaptureSessionRequest_builder{
			Session: sessionRef(t, validSessionID),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)
		stream, err := env.captureCli.DownloadCaptureSession(context.Background(), req)
		if err == nil {
			if stream.Receive() {
				t.Fatal("expected stream to fail, got item")
			}
			err = stream.Err()
		}
		close(unblock)
		<-done
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("code = %v, want %v", connect.CodeOf(err), connect.CodeFailedPrecondition)
		}
		if !strings.Contains(err.Error(), "handler sentinel") {
			t.Fatalf("error = %v, want handler sentinel", err)
		}
	})
}

func TestClientStreamRefused(t *testing.T) {
	checker := &fakeChecker{}
	interceptor := authz.NewInterceptor(checker)
	authnInterceptor := &testAuthnInterceptor{
		defaultPrincipal: authn.Principal{
			ID:       testPrincipalID,
			Tenants:  []string{validTenantID},
			Platform: false,
		},
	}

	tailMD := capturev1.File_flowseer_api_capture_v1_capture_service_proto.Services().ByName("CaptureService").Methods().ByName("TailCaptureSession")
	proc := "/test.StreamTest/TailClientStream"

	handler := connect.NewClientStreamHandler(
		proc,
		func(_ context.Context, _ *connect.ClientStream[capturev1.TailCaptureSessionRequest]) (*connect.Response[capturev1.TailCaptureSessionResponse], error) {
			return nil, nil
		},
		connect.WithSchema(tailMD),
		connect.WithInterceptors(authnInterceptor, interceptor),
	)

	mux := http.NewServeMux()
	mux.Handle(proc, handler)
	server := httptest.NewServer(mux)
	defer server.Close()

	client := connect.NewClient[capturev1.TailCaptureSessionRequest, capturev1.TailCaptureSessionResponse](
		server.Client(),
		server.URL+proc,
	)

	stream := client.CallClientStream(context.Background())
	stream.RequestHeader().Set("X-FlowSeer-Tenant", validTenantID)
	stream.RequestHeader().Set("Authorization", "Bearer test")

	_, err := stream.CloseAndReceive()
	if got := len(checker.Recorded()); got != 0 {
		t.Fatalf("recorded queries count = %d, want 0", got)
	}
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
	}
	if got := errCodeOf(t, err); got != authz.ErrCodeStreaming.String() {
		t.Errorf("got error code %q, want %q", got, authz.ErrCodeStreaming)
	}
}

func TestInFlightRequireDropsResponse(t *testing.T) {
	t.Run("unjoined check drops response with Internal", func(t *testing.T) {
		env := setupTestEnv(t)

		blockCh := make(chan struct{})
		startedCh := make(chan struct{})
		doneCh := make(chan struct{})

		var startOnce sync.Once
		env.checker.fail = func(q authz.Query) error {
			if q.Object == "edge:"+validEdgeID && q.Relation == "view" {
				startOnce.Do(func() { close(startedCh) })
				<-blockCh
			}
			return nil
		}

		env.setLoadedFn(func(ctx context.Context, _ *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error) {
			spawn.Go(ctx, "test.unjoined.require", func() {
				defer close(doneCh)
				_ = authz.Require(ctx, "view", "edge", validEdgeID)
			})
			<-startedCh
			return validResponse(t, edgev1.GetEdgeResponse_builder{
				Edge: validEdgeRecord(t),
			}.Build()), nil
		})

		req := validRequest(t, edgev1.GetEdgeRequest_builder{
			Edge: edgeRef(t),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		resp, err := env.loadedCli.CallUnary(context.Background(), req)
		close(blockCh)
		<-doneCh

		if resp != nil {
			t.Errorf("got response %v, want nil", resp)
		}
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeInternal)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeObligationViolation.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeObligationViolation)
		}
	})

	t.Run("stream handler returning nil while a check is in flight drops the response", func(t *testing.T) {
		env := setupTestEnv(t)

		blockCh := make(chan struct{})
		startedCh := make(chan struct{})
		doneCh := make(chan struct{})
		var startOnce sync.Once
		env.checker.fail = func(q authz.Query) error {
			if q.Object == "edge:"+validEdgeID && q.Relation == "view" {
				startOnce.Do(func() { close(startedCh) })
				<-blockCh
			}
			return nil
		}

		env.handlers.SetDownloadCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.DownloadCaptureSessionRequest], _ *connect.ServerStream[capturev1.DownloadCaptureSessionResponse]) error {
			spawn.Go(ctx, "test.stream.unjoined.require", func() {
				defer close(doneCh)
				_ = authz.Require(ctx, "view", "edge", validEdgeID)
			})
			<-startedCh
			return nil
		})

		req := validRequest(t, capturev1.DownloadCaptureSessionRequest_builder{
			Session: sessionRef(t, validSessionID),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)
		stream, err := env.captureCli.DownloadCaptureSession(context.Background(), req)
		if err == nil {
			if stream.Receive() {
				t.Fatal("expected the stream response to be dropped")
			}
			err = stream.Err()
		}
		close(blockCh)
		<-doneCh

		if connect.CodeOf(err) != connect.CodeInternal {
			t.Fatalf("code = %v, want %v", connect.CodeOf(err), connect.CodeInternal)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeObligationViolation.String() {
			t.Fatalf("error code = %q, want %q", got, authz.ErrCodeObligationViolation)
		}
	})

	t.Run("joined check returns response successfully", func(t *testing.T) {
		env := setupTestEnv(t)

		env.setLoadedFn(func(ctx context.Context, _ *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error) {
			var checkErr error
			doneCh := make(chan struct{})
			spawn.Go(ctx, "test.joined.require", func() {
				defer close(doneCh)
				checkErr = authz.Require(ctx, "view", "edge", validEdgeID)
			})
			<-doneCh
			if checkErr != nil {
				return nil, checkErr
			}
			return validResponse(t, edgev1.GetEdgeResponse_builder{
				Edge: validEdgeRecord(t),
			}.Build()), nil
		})

		req := validRequest(t, edgev1.GetEdgeRequest_builder{
			Edge: edgeRef(t),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		resp, err := env.loadedCli.CallUnary(context.Background(), req)
		if err != nil {
			t.Fatalf("GetEdge: %v", err)
		}
		if resp == nil {
			t.Fatal("expected non-nil response, got nil")
		}
	})

	t.Run("unjoined check preserves a handler error", func(t *testing.T) {
		env := setupTestEnv(t)
		blockCh := make(chan struct{})
		startedCh := make(chan struct{})
		doneCh := make(chan struct{})
		var startOnce sync.Once
		env.checker.fail = func(q authz.Query) error {
			if q.Object == "edge:"+validEdgeID && q.Relation == "view" {
				startOnce.Do(func() { close(startedCh) })
				<-blockCh
			}
			return nil
		}
		env.setLoadedFn(func(ctx context.Context, _ *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error) {
			spawn.Go(ctx, "test.unjoined.require.error", func() {
				defer close(doneCh)
				_ = authz.Require(ctx, "view", "edge", validEdgeID)
			})
			<-startedCh
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("handler sentinel"))
		})

		req := validRequest(t, edgev1.GetEdgeRequest_builder{Edge: edgeRef(t)}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)
		resp, err := env.loadedCli.CallUnary(context.Background(), req)
		close(blockCh)
		<-doneCh
		if resp != nil {
			t.Errorf("got response %v, want nil", resp)
		}
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("code = %v, want %v", connect.CodeOf(err), connect.CodeFailedPrecondition)
		}
		if !strings.Contains(err.Error(), "handler sentinel") {
			t.Fatalf("error = %v, want handler sentinel", err)
		}
	})
}

func TestAbandonObligation(t *testing.T) {
	t.Run("handler returning Abandon yields err", func(t *testing.T) {
		env := setupTestEnv(t)
		expectedErr := connect.NewError(connect.CodeUnavailable, errors.New("edgestore unavailable"))

		env.setLoadedFn(func(ctx context.Context, _ *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error) {
			return nil, authz.Abandon(ctx, expectedErr)
		})

		req := validRequest(t, edgev1.GetEdgeRequest_builder{
			Edge: edgeRef(t),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.loadedCli.CallUnary(context.Background(), req)
		if connect.CodeOf(err) != connect.CodeUnavailable {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeUnavailable)
		}
		if !strings.Contains(err.Error(), "edgestore unavailable") {
			t.Errorf("got error %v, want containing 'edgestore unavailable'", err)
		}
	})

	t.Run("handler answering after Abandon yields Internal", func(t *testing.T) {
		env := setupTestEnv(t)

		env.setLoadedFn(func(ctx context.Context, _ *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error) {
			_ = authz.Abandon(ctx, errors.New("early store error"))
			return validResponse(t, edgev1.GetEdgeResponse_builder{
				Edge: validEdgeRecord(t),
			}.Build()), nil
		})

		req := validRequest(t, edgev1.GetEdgeRequest_builder{
			Edge: edgeRef(t),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		resp, err := env.loadedCli.CallUnary(context.Background(), req)
		if resp != nil {
			t.Errorf("got response %v, want nil", resp)
		}
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeInternal)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeObligationViolation.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeObligationViolation)
		}
	})

	t.Run("ignored Abandon under a platform rule yields Internal", func(t *testing.T) {
		env := setupTestEnv(t)
		env.handlers.SetListTenantsFn(func(ctx context.Context, _ *connect.Request[identityv1.ListTenantsRequest]) (*connect.Response[identityv1.ListTenantsResponse], error) {
			_ = authz.Abandon(ctx, errors.New("early platform store error"))
			return validResponse(t, identityv1.ListTenantsResponse_builder{}.Build()), nil
		})

		resp, err := env.tenantCli.ListTenants(context.Background(), validRequest(t, identityv1.ListTenantsRequest_builder{}.Build()))
		if resp != nil {
			t.Errorf("got response %v, want nil", resp)
		}
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Fatalf("code = %v, want %v", connect.CodeOf(err), connect.CodeInternal)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeObligationViolation.String() {
			t.Fatalf("error code = %q, want %q", got, authz.ErrCodeObligationViolation)
		}
	})

	t.Run("Abandon on bare context yields Internal", func(t *testing.T) {
		err := authz.Abandon(context.Background(), errors.New("boom"))
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeInternal)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeObligationViolation.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeObligationViolation)
		}
	})

	t.Run("Abandon with nil error yields Internal", func(t *testing.T) {
		interceptor := authz.NewInterceptor(&fakeChecker{})
		ctx, err := interceptor.Admit(authn.NewContext(context.Background(), authn.Principal{
			ID:      testPrincipalID,
			Tenants: []string{validTenantID},
		}), validTenantID)
		if err != nil {
			t.Fatalf("Admit: %v", err)
		}
		abandonErr := authz.Abandon(ctx, nil)
		if connect.CodeOf(abandonErr) != connect.CodeInternal {
			t.Errorf("got code %v, want %v", connect.CodeOf(abandonErr), connect.CodeInternal)
		}
		if got := errCodeOf(t, abandonErr); got != authz.ErrCodeObligationViolation.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeObligationViolation)
		}
	})
}

func TestUnsupportedOrMissingRuleRefused(t *testing.T) {
	env := setupTestEnv(t)

	t.Run("missing rule on attach service heartbeat", func(t *testing.T) {
		req := validRequest(t, validHeartbeatRequest(t))
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
		req := validRequest(t, edgev1.GetEdgeRequest_builder{
			Edge: edgeRef(t),
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
		req := validRequest(t, edgev1.GetEdgeRequest_builder{
			Edge: edgeRef(t),
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

			req := validRequest(t, edgev1.GetEdgeRequest_builder{
				Edge: edgeRef(t),
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
				return validResponse(t, edgev1.GetEdgeResponse_builder{
					Edge: validEdgeRecord(t),
				}.Build()), nil
			})

			req := validRequest(t, edgev1.GetEdgeRequest_builder{
				Edge: edgeRef(t),
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

		req := validRequest(t, identityv1.ListTenantsRequest_builder{}.Build())

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

		var (
			mu         sync.Mutex
			tenantErr  error
			requireErr error
		)
		env.handlers.SetListTenantsFn(func(ctx context.Context, _ *connect.Request[identityv1.ListTenantsRequest]) (*connect.Response[identityv1.ListTenantsResponse], error) {
			_, tErr := tenant.FromContext(ctx)
			rErr := authz.Require(ctx, "admin", "tenant", validTenantID)
			mu.Lock()
			tenantErr = tErr
			requireErr = rErr
			mu.Unlock()
			return validResponse(t, identityv1.ListTenantsResponse_builder{}.Build()), nil
		})

		req := validRequest(t, identityv1.ListTenantsRequest_builder{}.Build())

		resp, err := env.tenantCli.ListTenants(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if resp != nil {
			t.Fatalf("expected nil response, got %v", resp)
		}
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeInternal)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeObligationViolation.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeObligationViolation)
		}
		if got := retryDispositionOf(t, err); got == errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
			t.Errorf("got retry disposition %v, want non-retryable", got)
		}
		if !env.handlers.DidRun("ListTenants") {
			t.Error("handler did not run")
		}
		mu.Lock()
		gotTenantErr := tenantErr
		gotRequireErr := requireErr
		mu.Unlock()
		if gotTenantErr == nil {
			t.Error("tenant found in context under platform rule, want no tenant")
		}
		if gotRequireErr == nil {
			t.Fatal("Require under platform rule returned nil, want Internal")
		}
		if connect.CodeOf(gotRequireErr) != connect.CodeInternal {
			t.Errorf("Require under platform rule returned %v, want CodeInternal", connect.CodeOf(gotRequireErr))
		}
		if got := errCodeOf(t, gotRequireErr); got != authz.ErrCodeObligationViolation.String() {
			t.Errorf("Require under platform rule error code = %q, want %q", got, authz.ErrCodeObligationViolation)
		}
	})

	t.Run("Require refusal under platform rule returned by handler yields Internal", func(t *testing.T) {
		env := setupTestEnv(t)

		var (
			mu         sync.Mutex
			requireErr error
		)
		env.handlers.SetListTenantsFn(func(ctx context.Context, _ *connect.Request[identityv1.ListTenantsRequest]) (*connect.Response[identityv1.ListTenantsResponse], error) {
			rErr := authz.Require(ctx, "admin", "tenant", validTenantID)
			mu.Lock()
			requireErr = rErr
			mu.Unlock()
			return nil, rErr
		})

		req := validRequest(t, identityv1.ListTenantsRequest_builder{}.Build())

		resp, err := env.tenantCli.ListTenants(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if resp != nil {
			t.Errorf("expected nil response, got %v", resp)
		}
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeInternal)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeObligationViolation.String() {
			t.Errorf("error code = %q, want %q", got, authz.ErrCodeObligationViolation)
		}
		if got := retryDispositionOf(t, err); got == errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
			t.Errorf("got retry disposition %v, want non-retryable", got)
		}
		mu.Lock()
		gotRequireErr := requireErr
		mu.Unlock()
		if gotRequireErr == nil {
			t.Fatal("Require under platform rule returned nil, want Internal")
		}
		if connect.CodeOf(gotRequireErr) != connect.CodeInternal {
			t.Errorf("Require returned %v, want CodeInternal", connect.CodeOf(gotRequireErr))
		}
	})

	t.Run("Filter under platform rule returns Internal", func(t *testing.T) {
		env := setupTestEnv(t)

		var (
			mu        sync.Mutex
			filterErr error
		)
		env.handlers.SetListTenantsFn(func(ctx context.Context, _ *connect.Request[identityv1.ListTenantsRequest]) (*connect.Response[identityv1.ListTenantsResponse], error) {
			_, fErr := authz.Filter(ctx, "admin", "tenant", []string{validTenantID})
			mu.Lock()
			filterErr = fErr
			mu.Unlock()
			return validResponse(t, identityv1.ListTenantsResponse_builder{}.Build()), nil
		})

		req := validRequest(t, identityv1.ListTenantsRequest_builder{}.Build())

		resp, err := env.tenantCli.ListTenants(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if resp != nil {
			t.Fatalf("expected nil response, got %v", resp)
		}
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeInternal)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeObligationViolation.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeObligationViolation)
		}
		if got := retryDispositionOf(t, err); got == errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
			t.Errorf("got retry disposition %v, want non-retryable", got)
		}
		if !env.handlers.DidRun("ListTenants") {
			t.Error("handler did not run")
		}
		mu.Lock()
		gotFilterErr := filterErr
		mu.Unlock()
		if gotFilterErr == nil {
			t.Fatal("Filter under platform rule returned nil, want Internal")
		}
		if connect.CodeOf(gotFilterErr) != connect.CodeInternal {
			t.Errorf("Filter under platform rule returned %v, want CodeInternal", connect.CodeOf(gotFilterErr))
		}
		if got := errCodeOf(t, gotFilterErr); got != authz.ErrCodeObligationViolation.String() {
			t.Errorf("Filter under platform rule error code = %q, want %q", got, authz.ErrCodeObligationViolation)
		}
	})

	t.Run("Filter refusal under platform rule returned by handler yields Internal", func(t *testing.T) {
		env := setupTestEnv(t)

		var (
			mu        sync.Mutex
			filterErr error
		)
		env.handlers.SetListTenantsFn(func(ctx context.Context, _ *connect.Request[identityv1.ListTenantsRequest]) (*connect.Response[identityv1.ListTenantsResponse], error) {
			_, fErr := authz.Filter(ctx, "admin", "tenant", []string{validTenantID})
			mu.Lock()
			filterErr = fErr
			mu.Unlock()
			return nil, fErr
		})

		req := validRequest(t, identityv1.ListTenantsRequest_builder{}.Build())

		resp, err := env.tenantCli.ListTenants(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if resp != nil {
			t.Errorf("expected nil response, got %v", resp)
		}
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeInternal)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeObligationViolation.String() {
			t.Errorf("error code = %q, want %q", got, authz.ErrCodeObligationViolation)
		}
		if got := retryDispositionOf(t, err); got == errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
			t.Errorf("got retry disposition %v, want non-retryable", got)
		}
		mu.Lock()
		gotFilterErr := filterErr
		mu.Unlock()
		if gotFilterErr == nil {
			t.Fatal("Filter under platform rule returned nil, want Internal")
		}
		if connect.CodeOf(gotFilterErr) != connect.CodeInternal {
			t.Errorf("Filter returned %v, want CodeInternal", connect.CodeOf(gotFilterErr))
		}
	})

	for _, headerVal := range []string{validTenantID, "Acme"} {
		t.Run("tenant header "+headerVal+" ignored under platform rule", func(t *testing.T) {
			env := setupTestEnv(t)
			var (
				mu          sync.Mutex
				foundTenant string
				tenantErr   error
			)
			env.handlers.SetListTenantsFn(func(ctx context.Context, _ *connect.Request[identityv1.ListTenantsRequest]) (*connect.Response[identityv1.ListTenantsResponse], error) {
				tid, err := tenant.FromContext(ctx)
				mu.Lock()
				foundTenant = tid
				tenantErr = err
				mu.Unlock()
				return validResponse(t, identityv1.ListTenantsResponse_builder{}.Build()), nil
			})

			req := validRequest(t, identityv1.ListTenantsRequest_builder{}.Build())
			req.Header().Set("X-FlowSeer-Tenant", headerVal)

			resp, err := env.tenantCli.ListTenants(context.Background(), req)
			if err != nil {
				t.Fatalf("unexpected call error: %v", err)
			}
			if resp == nil {
				t.Fatal("expected response, got nil")
			}
			if !env.handlers.DidRun("ListTenants") {
				t.Error("handler did not run")
			}
			recorded := env.checker.Recorded()
			if len(recorded) != 1 {
				t.Fatalf("got %d queries, want 1", len(recorded))
			}
			wantQuery := authz.Query{
				Object:   "platform:flowseer",
				Relation: "admin",
				User:     "user:" + testPrincipalID,
			}
			if recorded[0].Object != wantQuery.Object || recorded[0].Relation != wantQuery.Relation || recorded[0].User != wantQuery.User {
				t.Errorf("got query %+v, want %+v", recorded[0], wantQuery)
			}
			mu.Lock()
			tErr := tenantErr
			tid := foundTenant
			mu.Unlock()
			if tErr == nil || tid != "" {
				t.Errorf("tenant found in context = %q, want none", tid)
			}
		})
	}
}

func TestRequestRuleNoIDRefused(t *testing.T) {
	tests := []struct {
		name string
		req  *connect.Request[edgev1.GetEdgeRequest]
	}{
		{
			name: "omitted edge field yields no id",
			req:  connect.NewRequest(edgev1.GetEdgeRequest_builder{}.Build()),
		},
		{
			name: "present edge field with empty id yields no id",
			req: connect.NewRequest(edgev1.GetEdgeRequest_builder{
				Edge: edgemodelv1.EdgeGlobalRef_builder{
					Edge: edgemodelv1.EdgeLocalRef_builder{
						Id: proto.String(""),
					}.Build(),
				}.Build(),
			}.Build()),
		},
	}

	wantQuery := authz.Query{
		Object:   "tenant:" + validTenantID,
		Relation: "member",
		User:     "user:" + testPrincipalID,
		ContextualTuples: []authz.Tuple{
			{
				Object:   "tenant:" + validTenantID,
				Relation: "claimed",
				User:     "user:" + testPrincipalID,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := setupTestEnv(t)
			tc.req.Header().Set("X-FlowSeer-Tenant", validTenantID)

			_, err := env.edgeCli.GetEdge(context.Background(), tc.req)
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
			if len(recorded) != 1 {
				t.Fatalf("got %d queries, want 1: %+v", len(recorded), recorded)
			}
			if !queryEquals(recorded[0], wantQuery) {
				t.Errorf("recorded query = %+v, want %+v", recorded[0], wantQuery)
			}
			if env.handlers.DidRun("GetEdge") {
				t.Error("handler ran, want not run")
			}
		})
	}
}

func TestAdmittedTenantInContext(t *testing.T) {
	env := setupTestEnv(t)

	req := validRequest(t, edgev1.GetEdgeRequest_builder{
		Edge: edgeRef(t),
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
			return validResponse(t, capturev1.ListCaptureSessionsResponse_builder{}.Build()), nil
		})

		req := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
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
			return validResponse(t, capturev1.ListCaptureSessionsResponse_builder{}.Build()), nil
		})

		req := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
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

		req := validRequest(t, edgev1.GetEdgeRequest_builder{
			Edge: edgeRef(t),
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

	t.Run("interceptor request rule receives over-long answer", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.SetBatchAnswers(func(queries []authz.Query) []bool {
			answers := make([]bool, len(queries)+1)
			for i := range answers {
				answers[i] = true
			}
			return answers
		})

		req := validRequest(t, edgev1.GetEdgeRequest_builder{
			Edge: edgeRef(t),
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

		var (
			mu         sync.Mutex
			requireErr error
		)
		env.handlers.SetListCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
			rErr := authz.Require(ctx, "capture", "edge", validEdgeID)
			mu.Lock()
			requireErr = rErr
			mu.Unlock()
			return nil, rErr
		})

		req := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, _ = env.captureCli.ListCaptureSessions(context.Background(), req)
		mu.Lock()
		rErr := requireErr
		mu.Unlock()
		if connect.CodeOf(rErr) != connect.CodeUnavailable {
			t.Errorf("Require returned code %v, want %v", connect.CodeOf(rErr), connect.CodeUnavailable)
		}
		if got := errCodeOf(t, rErr); got != authz.ErrCodeUnavailable.String() {
			t.Errorf("Require returned error code %q, want %q", got, authz.ErrCodeUnavailable)
		}
		if got := retryDispositionOf(t, rErr); got != errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
			t.Errorf("Require returned retry disposition %v, want retryable", got)
		}
	})

	t.Run("Require receives short answer and ignored yields Internal", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.SetBatchAnswers(func(_ []authz.Query) []bool {
			return []bool{true}
		})

		env.handlers.SetListCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
			_ = authz.Require(ctx, "capture", "edge", validEdgeID)
			return validResponse(t, capturev1.ListCaptureSessionsResponse_builder{}.Build()), nil
		})

		req := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		resp, err := env.captureCli.ListCaptureSessions(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if resp != nil {
			t.Errorf("expected nil response, got %v", resp)
		}
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeInternal)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeObligationViolation.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeObligationViolation)
		}
		if got := retryDispositionOf(t, err); got == errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
			t.Errorf("got retry disposition %v, want non-retryable", got)
		}
	})

	t.Run("Filter receives short answer", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.SetBatchAnswers(func(_ []authz.Query) []bool {
			return []bool{true}
		})

		var (
			mu        sync.Mutex
			filterErr error
		)
		env.handlers.SetListCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
			_, fErr := authz.Filter(ctx, "capture", "edge", []string{validEdgeID})
			mu.Lock()
			filterErr = fErr
			mu.Unlock()
			return nil, fErr
		})

		req := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, _ = env.captureCli.ListCaptureSessions(context.Background(), req)
		mu.Lock()
		fErr := filterErr
		mu.Unlock()
		if connect.CodeOf(fErr) != connect.CodeUnavailable {
			t.Errorf("Filter returned code %v, want %v", connect.CodeOf(fErr), connect.CodeUnavailable)
		}
		if got := errCodeOf(t, fErr); got != authz.ErrCodeUnavailable.String() {
			t.Errorf("Filter returned error code %q, want %q", got, authz.ErrCodeUnavailable)
		}
		if got := retryDispositionOf(t, fErr); got != errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
			t.Errorf("Filter returned retry disposition %v, want retryable", got)
		}
	})

	t.Run("Filter receives short answer and ignored yields Internal", func(t *testing.T) {
		env := setupTestEnv(t)
		env.checker.SetBatchAnswers(func(_ []authz.Query) []bool {
			return []bool{true}
		})

		env.handlers.SetListCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
			_, _ = authz.Filter(ctx, "capture", "edge", []string{validEdgeID})
			return validResponse(t, capturev1.ListCaptureSessionsResponse_builder{}.Build()), nil
		})

		req := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		resp, err := env.captureCli.ListCaptureSessions(context.Background(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if resp != nil {
			t.Errorf("expected nil response, got %v", resp)
		}
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeInternal)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeObligationViolation.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeObligationViolation)
		}
		if got := retryDispositionOf(t, err); got == errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
			t.Errorf("got retry disposition %v, want non-retryable", got)
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

func TestDeferredObligationDischarge(t *testing.T) {
	type deferredMode struct {
		name       string
		relation   string
		objectType string
		call       func(t *testing.T, env *testEnv, handler func(ctx context.Context) error) (any, error)
	}

	modes := []deferredMode{
		{
			name:       "loaded",
			relation:   "view",
			objectType: "edge",
			call: func(t *testing.T, env *testEnv, handler func(ctx context.Context) error) (any, error) {
				env.setLoadedFn(func(ctx context.Context, _ *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error) {
					if err := handler(ctx); err != nil {
						return nil, err
					}
					return validResponse(t, edgev1.GetEdgeResponse_builder{
						Edge: validEdgeRecord(t),
					}.Build()), nil
				})
				req := validRequest(t, edgev1.GetEdgeRequest_builder{
					Edge: edgeRef(t),
				}.Build())
				req.Header().Set("X-FlowSeer-Tenant", validTenantID)
				resp, err := env.loadedCli.CallUnary(context.Background(), req)
				if err != nil {
					return nil, err
				}
				return resp, nil
			},
		},
		{
			name:       "filtered",
			relation:   "capture",
			objectType: "edge",
			call: func(t *testing.T, env *testEnv, handler func(ctx context.Context) error) (any, error) {
				env.handlers.SetListCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
					if err := handler(ctx); err != nil {
						return nil, err
					}
					return validResponse(t, capturev1.ListCaptureSessionsResponse_builder{}.Build()), nil
				})
				req := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
				req.Header().Set("X-FlowSeer-Tenant", validTenantID)
				resp, err := env.captureCli.ListCaptureSessions(context.Background(), req)
				if err != nil {
					return nil, err
				}
				return resp, nil
			},
		},
		{
			name:       "request",
			relation:   "view",
			objectType: "edge",
			call: func(t *testing.T, env *testEnv, handler func(ctx context.Context) error) (any, error) {
				env.handlers.SetGetCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.GetCaptureSessionRequest]) (*connect.Response[capturev1.GetCaptureSessionResponse], error) {
					if err := handler(ctx); err != nil {
						return nil, err
					}
					return validResponse(t, capturev1.GetCaptureSessionResponse_builder{
						Session: validCaptureSessionRecord(t),
					}.Build()), nil
				})
				req := validRequest(t, capturev1.GetCaptureSessionRequest_builder{
					Session: sessionRef(t, validSessionID),
				}.Build())
				req.Header().Set("X-FlowSeer-Tenant", validTenantID)
				resp, err := env.captureCli.GetCaptureSession(context.Background(), req)
				if err != nil {
					return nil, err
				}
				return resp, nil
			},
		},
		{
			name:       "tenant",
			relation:   "view",
			objectType: "edge",
			call: func(t *testing.T, env *testEnv, handler func(ctx context.Context) error) (any, error) {
				env.handlers.SetCreateEdgeFn(func(ctx context.Context, _ *connect.Request[edgev1.CreateEdgeRequest]) (*connect.Response[edgev1.CreateEdgeResponse], error) {
					if err := handler(ctx); err != nil {
						return nil, err
					}
					return validResponse(t, edgev1.CreateEdgeResponse_builder{
						Edge:         validEdgeRecord(t),
						Provisioning: validEdgeProvisioning(t),
					}.Build()), nil
				})
				req := validRequest(t, edgev1.CreateEdgeRequest_builder{
					Name: proto.String("edge-alpha"),
				}.Build())
				req.Header().Set("X-FlowSeer-Tenant", validTenantID)
				resp, err := env.edgeCli.CreateEdge(context.Background(), req)
				if err != nil {
					return nil, err
				}
				return resp, nil
			},
		},
		{
			name:       "platform",
			relation:   "view",
			objectType: "edge",
			call: func(t *testing.T, env *testEnv, handler func(ctx context.Context) error) (any, error) {
				env.handlers.SetListTenantsFn(func(ctx context.Context, _ *connect.Request[identityv1.ListTenantsRequest]) (*connect.Response[identityv1.ListTenantsResponse], error) {
					if err := handler(ctx); err != nil {
						return nil, err
					}
					return validResponse(t, identityv1.ListTenantsResponse_builder{}.Build()), nil
				})
				req := validRequest(t, identityv1.ListTenantsRequest_builder{}.Build())
				resp, err := env.tenantCli.ListTenants(context.Background(), req)
				if err != nil {
					return nil, err
				}
				return resp, nil
			},
		},
	}

	type checkKind int
	const (
		checkNone checkKind = iota
		checkRequireAllowed
		checkRequireDenied
		checkRequireCheckerError
		checkRequireRefused
		checkFilterWithIDs
		checkFilterNilIDs
		checkFilterEmptyIDs
		checkFilterCheckerError
		checkFilterRefused
		checkShortBatchCheckAnswer
		checkRequireDeniedThenRequireAllowed
		checkRequireDeniedThenFilterAllowed
		checkFilterCheckerErrorThenRequireAllowed
		checkFilterCheckerErrorThenFilterAllowed
	)

	checkKinds := []struct {
		name         string
		kind         checkKind
		needsSuccess bool
		platformOnly bool
		skipPlatform bool
	}{
		{name: "none", kind: checkNone},
		{name: "Require allowed", kind: checkRequireAllowed, needsSuccess: true},
		{name: "Require denied", kind: checkRequireDenied, skipPlatform: true},
		{name: "Require checker error", kind: checkRequireCheckerError, skipPlatform: true},
		{name: "Require refused", kind: checkRequireRefused, platformOnly: true},
		{name: "Filter with ids", kind: checkFilterWithIDs, needsSuccess: true},
		{name: "Filter with nil ids", kind: checkFilterNilIDs, needsSuccess: true},
		{name: "Filter with empty non-nil ids", kind: checkFilterEmptyIDs, needsSuccess: true},
		{name: "Filter checker error", kind: checkFilterCheckerError, skipPlatform: true},
		{name: "Filter refused", kind: checkFilterRefused, platformOnly: true},
		{name: "short BatchCheck answer", kind: checkShortBatchCheckAnswer, skipPlatform: true},
		{name: "Require denied then Require allowed", kind: checkRequireDeniedThenRequireAllowed, needsSuccess: true},
		{name: "Require denied then Filter allowed", kind: checkRequireDeniedThenFilterAllowed, needsSuccess: true},
		{name: "Filter checker error then Require allowed", kind: checkFilterCheckerErrorThenRequireAllowed, needsSuccess: true},
		{name: "Filter checker error then Filter allowed", kind: checkFilterCheckerErrorThenFilterAllowed, needsSuccess: true},
	}

	type contextKind int
	const (
		contextLive contextKind = iota
		contextEnded
	)

	contextKinds := []struct {
		name string
		kind contextKind
	}{
		{name: "live context", kind: contextLive},
		{name: "ended context", kind: contextEnded},
	}

	type returnKind int
	const (
		returnResponse returnKind = iota
		returnError
	)

	returnKinds := []struct {
		name string
		kind returnKind
	}{
		{name: "response", kind: returnResponse},
		{name: "error", kind: returnError},
	}

	wantMembershipQuery := authz.Query{
		Object:   "tenant:" + validTenantID,
		Relation: "member",
		User:     "user:" + testPrincipalID,
		ContextualTuples: []authz.Tuple{
			{
				Object:   "tenant:" + validTenantID,
				Relation: "claimed",
				User:     "user:" + testPrincipalID,
			},
		},
	}

	wantPlatformQuery := authz.Query{
		Object:   "platform:flowseer",
		Relation: "admin",
		User:     "user:" + testPrincipalID,
		ContextualTuples: []authz.Tuple{
			{
				Object:   "tenant:" + validTenantID,
				Relation: "claimed",
				User:     "user:" + testPrincipalID,
			},
		},
	}

	handlerErr := connect.NewError(connect.CodeNotFound, errors.New("sentinel handler error"))

	for _, mode := range modes {
		t.Run(mode.name, func(t *testing.T) {
			for _, tcCheck := range checkKinds {
				if mode.name == "platform" && (tcCheck.needsSuccess || tcCheck.skipPlatform) {
					continue
				}
				if mode.name != "platform" && tcCheck.platformOnly {
					continue
				}
				t.Run(tcCheck.name, func(t *testing.T) {
					for _, tcContext := range contextKinds {
						t.Run(tcContext.name, func(t *testing.T) {
							for _, tcReturn := range returnKinds {
								t.Run(tcReturn.name, func(t *testing.T) {
									env := setupTestEnv(t)
									if tcContext.kind == contextEnded {
										env.outerInterceptor.setDeriveCtx(context.WithCancel)
									}

									switch tcCheck.kind {
									case checkRequireDenied:
										env.checker.SetDeny(func(q authz.Query) bool {
											return strings.HasPrefix(q.Object, mode.objectType+":")
										})
									case checkRequireCheckerError, checkFilterCheckerError:
										env.checker.SetFail(func(q authz.Query) error {
											if strings.HasPrefix(q.Object, mode.objectType+":") {
												return errors.New("simulated checker failure")
											}
											return nil
										})
									case checkShortBatchCheckAnswer:
										env.checker.SetBatchAnswers(func(queries []authz.Query) []bool {
											for _, q := range queries {
												if strings.HasPrefix(q.Object, mode.objectType+":") {
													return []bool{}
												}
											}
											ans := make([]bool, len(queries))
											for i := range ans {
												ans[i] = true
											}
											return ans
										})
									case checkRequireDeniedThenRequireAllowed, checkRequireDeniedThenFilterAllowed:
										env.checker.SetDeny(func(q authz.Query) bool {
											return q.Object == mode.objectType+":"+validEdgeID
										})
									case checkFilterCheckerErrorThenRequireAllowed, checkFilterCheckerErrorThenFilterAllowed:
										env.checker.SetFail(func(q authz.Query) error {
											if q.Object == mode.objectType+":"+validEdgeID {
												return errors.New("simulated checker failure")
											}
											return nil
										})
									}

									var (
										mu         sync.Mutex
										handlerRan bool
										filterIDs  []string
										filterErr  error
										requireErr error
									)

									resp, err := mode.call(t, env, func(ctx context.Context) error {
										var (
											rErr error
											fErr error
											fIDs []string
										)
										switch tcCheck.kind {
										case checkNone:
											// No check performed.
										case checkRequireAllowed, checkRequireDenied, checkRequireCheckerError, checkRequireRefused:
											rErr = authz.Require(ctx, mode.relation, mode.objectType, validEdgeID)
										case checkFilterWithIDs, checkFilterCheckerError, checkFilterRefused:
											fIDs, fErr = authz.Filter(ctx, mode.relation, mode.objectType, []string{validEdgeID})
										case checkFilterNilIDs:
											fIDs, fErr = authz.Filter(ctx, mode.relation, mode.objectType, nil)
										case checkFilterEmptyIDs:
											fIDs, fErr = authz.Filter(ctx, mode.relation, mode.objectType, []string{})
										case checkShortBatchCheckAnswer:
											rErr = authz.Require(ctx, mode.relation, mode.objectType, validEdgeID)
										case checkRequireDeniedThenRequireAllowed:
											rErr = authz.Require(ctx, mode.relation, mode.objectType, validEdgeID)
											if rErr2 := authz.Require(ctx, mode.relation, mode.objectType, validEdgeID2); rErr2 != nil {
												t.Errorf("second Require failed unexpectedly: %v", rErr2)
											}
										case checkRequireDeniedThenFilterAllowed:
											rErr = authz.Require(ctx, mode.relation, mode.objectType, validEdgeID)
											fIDs2, fErr2 := authz.Filter(ctx, mode.relation, mode.objectType, []string{validEdgeID2})
											if fErr2 != nil {
												t.Errorf("second Filter failed unexpectedly: %v", fErr2)
											} else if !slices.Equal(fIDs2, []string{validEdgeID2}) {
												t.Errorf("second Filter IDs = %v, want [%s]", fIDs2, validEdgeID2)
											}
										case checkFilterCheckerErrorThenRequireAllowed:
											_, fErr = authz.Filter(ctx, mode.relation, mode.objectType, []string{validEdgeID})
											if rErr2 := authz.Require(ctx, mode.relation, mode.objectType, validEdgeID2); rErr2 != nil {
												t.Errorf("second Require failed unexpectedly: %v", rErr2)
											}
										case checkFilterCheckerErrorThenFilterAllowed:
											_, fErr = authz.Filter(ctx, mode.relation, mode.objectType, []string{validEdgeID})
											fIDs2, fErr2 := authz.Filter(ctx, mode.relation, mode.objectType, []string{validEdgeID2})
											if fErr2 != nil {
												t.Errorf("second Filter failed unexpectedly: %v", fErr2)
											} else if !slices.Equal(fIDs2, []string{validEdgeID2}) {
												t.Errorf("second Filter IDs = %v, want [%s]", fIDs2, validEdgeID2)
											}
										}

										mu.Lock()
										handlerRan = true
										requireErr = rErr
										filterErr = fErr
										filterIDs = fIDs
										mu.Unlock()

										if tcContext.kind == contextEnded {
											env.outerInterceptor.Cancel()
										}

										if tcReturn.kind == returnResponse {
											return nil
										}
										return handlerErr
									})

									if tcContext.kind == contextEnded {
										if got := env.outerInterceptor.AfterCtxErr(); got != context.Canceled {
											t.Fatalf("call context err = %v, want context.Canceled", got)
										}
									}

									mu.Lock()
									ran := handlerRan
									rErr := requireErr
									fErr := filterErr
									fIDs := filterIDs
									mu.Unlock()

									if !ran {
										t.Fatal("handler did not run")
									}

									if tcCheck.kind == checkRequireAllowed && rErr != nil {
										t.Fatalf("Require failed unexpectedly: %v", rErr)
									}
									if (tcCheck.kind == checkRequireDenied || tcCheck.kind == checkRequireCheckerError || tcCheck.kind == checkShortBatchCheckAnswer) && rErr == nil {
										t.Fatal("expected Require to fail, got nil")
									}
									if tcCheck.kind == checkFilterCheckerError && fErr == nil {
										t.Fatal("expected Filter to fail, got nil")
									}
									if tcCheck.kind == checkRequireRefused {
										if rErr == nil {
											t.Fatal("expected Require to fail, got nil")
										}
										if got := errCodeOf(t, rErr); got != authz.ErrCodeObligationViolation.String() {
											t.Errorf("Require error code = %q, want %q", got, authz.ErrCodeObligationViolation)
										}
										recorded := env.checker.Recorded()
										if len(recorded) != 1 {
											t.Fatalf("recorded %d queries, want 1", len(recorded))
										}
										if !queryEquals(recorded[0], wantPlatformQuery) {
											t.Errorf("recorded query = %+v, want %+v", recorded[0], wantPlatformQuery)
										}
									}
									if tcCheck.kind == checkFilterRefused {
										if fErr == nil {
											t.Fatal("expected Filter to fail, got nil")
										}
										if got := errCodeOf(t, fErr); got != authz.ErrCodeObligationViolation.String() {
											t.Errorf("Filter error code = %q, want %q", got, authz.ErrCodeObligationViolation)
										}
										recorded := env.checker.Recorded()
										if len(recorded) != 1 {
											t.Fatalf("recorded %d queries, want 1", len(recorded))
										}
										if !queryEquals(recorded[0], wantPlatformQuery) {
											t.Errorf("recorded query = %+v, want %+v", recorded[0], wantPlatformQuery)
										}
									}
									if (tcCheck.kind == checkRequireDeniedThenRequireAllowed || tcCheck.kind == checkRequireDeniedThenFilterAllowed) && rErr == nil {
										t.Fatal("expected first Require to fail, got nil")
									}
									if (tcCheck.kind == checkFilterCheckerErrorThenRequireAllowed || tcCheck.kind == checkFilterCheckerErrorThenFilterAllowed) && fErr == nil {
										t.Fatal("expected first Filter to fail, got nil")
									}
									if tcCheck.kind == checkFilterWithIDs {
										if fErr != nil {
											t.Fatalf("Filter failed unexpectedly: %v", fErr)
										}
										if !slices.Equal(fIDs, []string{validEdgeID}) {
											t.Fatalf("Filter returned %v, want [%s]", fIDs, validEdgeID)
										}
									}
									if tcCheck.kind == checkFilterNilIDs || tcCheck.kind == checkFilterEmptyIDs {
										if fErr != nil {
											t.Errorf("Filter returned error: %v, want nil", fErr)
										}
										if fIDs == nil || len(fIDs) != 0 {
											t.Errorf("Filter returned IDs %v, want empty non-nil slice", fIDs)
										}
										if mode.name == "loaded" || mode.name == "filtered" {
											recorded := env.checker.Recorded()
											if len(recorded) != 1 {
												t.Errorf("recorded %d queries, want 1", len(recorded))
											} else if !queryEquals(recorded[0], wantMembershipQuery) {
												t.Errorf("recorded query = %+v, want %+v", recorded[0], wantMembershipQuery)
											}
										}
									}

									isLoadedOrFiltered := mode.name == "loaded" || mode.name == "filtered"
									isNone := tcCheck.kind == checkNone
									hasFailedCheck := tcCheck.kind == checkRequireDenied ||
										tcCheck.kind == checkRequireCheckerError ||
										tcCheck.kind == checkFilterCheckerError ||
										tcCheck.kind == checkShortBatchCheckAnswer ||
										tcCheck.kind == checkRequireDeniedThenRequireAllowed ||
										tcCheck.kind == checkRequireDeniedThenFilterAllowed ||
										tcCheck.kind == checkFilterCheckerErrorThenRequireAllowed ||
										tcCheck.kind == checkFilterCheckerErrorThenFilterAllowed ||
										tcCheck.kind == checkRequireRefused ||
										tcCheck.kind == checkFilterRefused
									isEnded := tcContext.kind == contextEnded

									var (
										wantInternalViolation   bool
										wantCtxErrItself        bool
										wantHandlerErrUnchanged bool
										wantResponsePasses      bool
									)

									if tcReturn.kind == returnResponse {
										switch {
										case isLoadedOrFiltered && isNone:
											wantInternalViolation = true
										case hasFailedCheck:
											wantInternalViolation = true
										default:
											wantResponsePasses = true
										}
									} else {
										// tcReturn.kind == returnError
										if isLoadedOrFiltered && isNone {
											if isEnded {
												wantCtxErrItself = true
											} else {
												wantInternalViolation = true
											}
										} else {
											wantHandlerErrUnchanged = true
										}
									}

									switch {
									case wantInternalViolation:
										if resp != nil {
											t.Errorf("got response %v, want nil", resp)
										}
										if err == nil {
											t.Fatal("expected error, got nil")
										}
										if connect.CodeOf(err) != connect.CodeInternal {
											t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeInternal)
										}
										if got := errCodeOf(t, err); got != authz.ErrCodeObligationViolation.String() {
											t.Errorf("got error code %q, want %q", got, authz.ErrCodeObligationViolation)
										}
										if got := retryDispositionOf(t, err); got == errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
											t.Errorf("got retry disposition %v, want non-retryable", got)
										}
									case wantCtxErrItself:
										if resp != nil {
											t.Errorf("got response %v, want nil", resp)
										}
										if err == nil {
											t.Fatal("expected error, got nil")
										}
										if outerErr := env.outerInterceptor.OuterErr(); outerErr != context.Canceled {
											t.Errorf("outer interceptor err = %v, want context.Canceled", outerErr)
										}
										if connect.CodeOf(err) != connect.CodeCanceled {
											t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeCanceled)
										}
									case wantHandlerErrUnchanged:
										if resp != nil {
											t.Errorf("got response %v, want nil", resp)
										}
										if err == nil {
											t.Fatal("expected error, got nil")
										}
										if outerErr := env.outerInterceptor.OuterErr(); outerErr != handlerErr {
											t.Errorf("outer interceptor err = %v (%p), want handlerErr %v (%p)", outerErr, outerErr, handlerErr, handlerErr)
										}
										if connect.CodeOf(err) != connect.CodeNotFound {
											t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodeNotFound)
										}
									case wantResponsePasses:
										if err != nil {
											t.Fatalf("unexpected call error: %v", err)
										}
										if resp == nil {
											t.Fatal("expected response, got nil")
										}
									}
								})
							}
						})
					}
				})
			}
		})
	}
}

type cancellableTestEnv struct {
	server           *httptest.Server
	checker          *fakeChecker
	handlers         *testHandlers
	captureCli       capturev1connect.CaptureServiceClient
	tenantCli        identityv1connect.TenantServiceClient
	edgeCli          edgev1connect.EdgeAdminServiceClient
	outerInterceptor *cancellableOuterInterceptor
}

type cancellableOuterInterceptor struct {
	mu          sync.Mutex
	cancel      context.CancelFunc
	outerErr    error
	afterCtxErr error
	deriveCtx   func(ctx context.Context) (context.Context, context.CancelFunc)
}

func (ci *cancellableOuterInterceptor) setDeriveCtx(fn func(ctx context.Context) (context.Context, context.CancelFunc)) {
	ci.mu.Lock()
	defer ci.mu.Unlock()
	ci.deriveCtx = fn
}

func (ci *cancellableOuterInterceptor) Cancel() {
	ci.mu.Lock()
	defer ci.mu.Unlock()
	if ci.cancel != nil {
		ci.cancel()
	}
}

func (ci *cancellableOuterInterceptor) OuterErr() error {
	ci.mu.Lock()
	defer ci.mu.Unlock()
	return ci.outerErr
}

func (ci *cancellableOuterInterceptor) AfterCtxErr() error {
	ci.mu.Lock()
	defer ci.mu.Unlock()
	return ci.afterCtxErr
}

func (ci *cancellableOuterInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		ci.mu.Lock()
		derive := ci.deriveCtx
		ci.mu.Unlock()

		if derive != nil {
			var cancel context.CancelFunc
			ctx, cancel = derive(ctx)
			ci.mu.Lock()
			ci.cancel = cancel
			ci.mu.Unlock()
		}

		resp, err := next(ctx, req)

		ci.mu.Lock()
		ci.outerErr = err
		ci.afterCtxErr = ctx.Err()
		ci.mu.Unlock()

		return resp, err
	}
}

func (ci *cancellableOuterInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (ci *cancellableOuterInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

func setupCancellableTestEnv(t *testing.T) *cancellableTestEnv {
	t.Helper()
	checker := &fakeChecker{}
	handlers := newTestHandlers(t)
	authzInterceptor := authz.NewInterceptor(checker)
	outerInterceptor := &cancellableOuterInterceptor{}
	authnInterceptor := &testAuthnInterceptor{
		defaultPrincipal: authn.Principal{
			ID:       testPrincipalID,
			Tenants:  []string{validTenantID},
			Platform: false,
		},
	}

	opts := []connect.HandlerOption{
		connect.WithInterceptors(authnInterceptor, outerInterceptor, authzInterceptor),
	}

	mux := http.NewServeMux()
	mux.Handle(capturev1connect.NewCaptureServiceHandler(handlers, opts...))
	mux.Handle(identityv1connect.NewTenantServiceHandler(handlers, opts...))
	mux.Handle(edgev1connect.NewEdgeAdminServiceHandler(handlers, opts...))

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := server.Client()
	return &cancellableTestEnv{
		server:           server,
		checker:          checker,
		handlers:         handlers,
		captureCli:       capturev1connect.NewCaptureServiceClient(client, server.URL),
		tenantCli:        identityv1connect.NewTenantServiceClient(client, server.URL),
		edgeCli:          edgev1connect.NewEdgeAdminServiceClient(client, server.URL),
		outerInterceptor: outerInterceptor,
	}
}

func TestContextCancellation(t *testing.T) {
	t.Run("membership check cancels context while answering true", func(t *testing.T) {
		env := setupCancellableTestEnv(t)
		env.outerInterceptor.setDeriveCtx(context.WithCancel)
		env.checker.SetFail(func(q authz.Query) error {
			if q.Relation == "member" {
				env.outerInterceptor.Cancel()
			}
			return nil
		})

		req := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.captureCli.ListCaptureSessions(context.Background(), req)

		if got := env.outerInterceptor.AfterCtxErr(); got != context.Canceled {
			t.Fatalf("call context err = %v, want context.Canceled", got)
		}
		recorded := env.checker.Recorded()
		if len(recorded) != 1 {
			t.Fatalf("recorded %d queries, want 1", len(recorded))
		}
		if recorded[0].Relation != "member" {
			t.Errorf("recorded query relation = %q, want member", recorded[0].Relation)
		}
		if env.handlers.DidRun("ListCaptureSessions") {
			t.Error("handler ran, want not run")
		}
		outerErr := env.outerInterceptor.OuterErr()
		if outerErr != context.Canceled {
			t.Errorf("outer interceptor err = %v, want context.Canceled", outerErr)
		}
		if connect.CodeOf(err) != connect.CodeCanceled {
			t.Errorf("client code = %v, want %v", connect.CodeOf(err), connect.CodeCanceled)
		}

		envLive := setupCancellableTestEnv(t)
		reqLive := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
		reqLive.Header().Set("X-FlowSeer-Tenant", validTenantID)
		_, errLive := envLive.captureCli.ListCaptureSessions(context.Background(), reqLive)
		if connect.CodeOf(errLive) != connect.CodeInternal {
			t.Errorf("live call code = %v, want %v", connect.CodeOf(errLive), connect.CodeInternal)
		}
		if got := errCodeOf(t, errLive); got != authz.ErrCodeObligationViolation.String() {
			t.Errorf("live call error code = %q, want %q", got, authz.ErrCodeObligationViolation)
		}
		if got := retryDispositionOf(t, errLive); got == errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
			t.Error("live call retry disposition is retryable, want not retryable")
		}
	})

	t.Run("membership check deadline exceeded while answering true", func(t *testing.T) {
		env := setupCancellableTestEnv(t)
		env.outerInterceptor.setDeriveCtx(func(ctx context.Context) (context.Context, context.CancelFunc) {
			return context.WithDeadline(ctx, time.Now().Add(-time.Hour))
		})

		req := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.captureCli.ListCaptureSessions(context.Background(), req)

		if got := env.outerInterceptor.AfterCtxErr(); got != context.DeadlineExceeded {
			t.Fatalf("call context err = %v, want context.DeadlineExceeded", got)
		}
		recorded := env.checker.Recorded()
		if len(recorded) != 1 {
			t.Fatalf("recorded %d queries, want 1", len(recorded))
		}
		if recorded[0].Relation != "member" {
			t.Errorf("recorded query relation = %q, want member", recorded[0].Relation)
		}
		if env.handlers.DidRun("ListCaptureSessions") {
			t.Error("handler ran, want not run")
		}
		outerErr := env.outerInterceptor.OuterErr()
		if outerErr != context.DeadlineExceeded {
			t.Errorf("outer interceptor err = %v, want context.DeadlineExceeded", outerErr)
		}
		if connect.CodeOf(err) != connect.CodeDeadlineExceeded {
			t.Errorf("client code = %v, want %v", connect.CodeOf(err), connect.CodeDeadlineExceeded)
		}

		envLive := setupCancellableTestEnv(t)
		envLive.outerInterceptor.setDeriveCtx(func(ctx context.Context) (context.Context, context.CancelFunc) {
			return context.WithTimeout(ctx, 5*time.Minute)
		})
		reqLive := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
		reqLive.Header().Set("X-FlowSeer-Tenant", validTenantID)
		_, errLive := envLive.captureCli.ListCaptureSessions(context.Background(), reqLive)
		if connect.CodeOf(errLive) != connect.CodeInternal {
			t.Errorf("live call code = %v, want %v", connect.CodeOf(errLive), connect.CodeInternal)
		}
		if got := errCodeOf(t, errLive); got != authz.ErrCodeObligationViolation.String() {
			t.Errorf("live call error code = %q, want %q", got, authz.ErrCodeObligationViolation)
		}
		if got := retryDispositionOf(t, errLive); got == errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
			t.Error("live call retry disposition is retryable, want not retryable")
		}
	})

	t.Run("membership check deadline exceeded and fails yields DeadlineExceeded", func(t *testing.T) {
		env := setupCancellableTestEnv(t)
		env.outerInterceptor.setDeriveCtx(func(ctx context.Context) (context.Context, context.CancelFunc) {
			return context.WithDeadline(ctx, time.Now().Add(-time.Hour))
		})
		env.checker.SetFail(func(q authz.Query) error {
			if q.Relation == "member" {
				return errors.New("simulated checker failure")
			}
			return nil
		})

		req := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.captureCli.ListCaptureSessions(context.Background(), req)

		if got := env.outerInterceptor.AfterCtxErr(); got != context.DeadlineExceeded {
			t.Fatalf("call context err = %v, want context.DeadlineExceeded", got)
		}
		recorded := env.checker.Recorded()
		if len(recorded) != 1 {
			t.Fatalf("recorded %d queries, want 1", len(recorded))
		}
		if recorded[0].Relation != "member" {
			t.Errorf("recorded query relation = %q, want member", recorded[0].Relation)
		}
		if env.handlers.DidRun("ListCaptureSessions") {
			t.Error("handler ran, want not run")
		}
		outerErr := env.outerInterceptor.OuterErr()
		if outerErr != context.DeadlineExceeded {
			t.Errorf("outer interceptor err = %v, want context.DeadlineExceeded", outerErr)
		}
		if connect.CodeOf(err) != connect.CodeDeadlineExceeded {
			t.Errorf("client code = %v, want %v", connect.CodeOf(err), connect.CodeDeadlineExceeded)
		}

		envLive := setupCancellableTestEnv(t)
		envLive.checker.SetFail(func(q authz.Query) error {
			if q.Relation == "member" {
				return errors.New("simulated checker failure")
			}
			return nil
		})
		reqLive := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
		reqLive.Header().Set("X-FlowSeer-Tenant", validTenantID)
		_, errLive := envLive.captureCli.ListCaptureSessions(context.Background(), reqLive)
		if connect.CodeOf(errLive) != connect.CodeUnavailable {
			t.Errorf("live call code = %v, want %v", connect.CodeOf(errLive), connect.CodeUnavailable)
		}
		if got := errCodeOf(t, errLive); got != authz.ErrCodeUnavailable.String() {
			t.Errorf("live call error code = %q, want %q", got, authz.ErrCodeUnavailable)
		}
		if got := retryDispositionOf(t, errLive); got != errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
			t.Errorf("live call retry disposition = %v, want retryable", got)
		}
	})

	t.Run("membership check cancels and fails yields Canceled", func(t *testing.T) {
		env := setupCancellableTestEnv(t)
		env.outerInterceptor.setDeriveCtx(context.WithCancel)
		env.checker.SetFail(func(q authz.Query) error {
			if q.Relation == "member" {
				env.outerInterceptor.Cancel()
				return errors.New("simulated checker failure")
			}
			return nil
		})

		req := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.captureCli.ListCaptureSessions(context.Background(), req)

		if got := env.outerInterceptor.AfterCtxErr(); got != context.Canceled {
			t.Fatalf("call context err = %v, want context.Canceled", got)
		}
		recorded := env.checker.Recorded()
		if len(recorded) != 1 {
			t.Fatalf("recorded %d queries, want 1", len(recorded))
		}
		if recorded[0].Relation != "member" {
			t.Errorf("recorded query relation = %q, want member", recorded[0].Relation)
		}
		if env.handlers.DidRun("ListCaptureSessions") {
			t.Error("handler ran, want not run")
		}
		outerErr := env.outerInterceptor.OuterErr()
		if outerErr != context.Canceled {
			t.Errorf("outer interceptor err = %v, want context.Canceled", outerErr)
		}
		if connect.CodeOf(err) != connect.CodeCanceled {
			t.Errorf("client code = %v, want %v", connect.CodeOf(err), connect.CodeCanceled)
		}

		envLive := setupCancellableTestEnv(t)
		envLive.checker.SetFail(func(q authz.Query) error {
			if q.Relation == "member" {
				return errors.New("simulated checker failure")
			}
			return nil
		})
		reqLive := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
		reqLive.Header().Set("X-FlowSeer-Tenant", validTenantID)
		_, errLive := envLive.captureCli.ListCaptureSessions(context.Background(), reqLive)
		if connect.CodeOf(errLive) != connect.CodeUnavailable {
			t.Errorf("live call code = %v, want %v", connect.CodeOf(errLive), connect.CodeUnavailable)
		}
		if got := errCodeOf(t, errLive); got != authz.ErrCodeUnavailable.String() {
			t.Errorf("live call error code = %q, want %q", got, authz.ErrCodeUnavailable)
		}
		if got := retryDispositionOf(t, errLive); got != errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
			t.Errorf("live call retry disposition = %v, want retryable", got)
		}
	})

	t.Run("tenant rule on CreateEdge cancels context and fails query yields Canceled", func(t *testing.T) {
		env := setupCancellableTestEnv(t)
		env.outerInterceptor.setDeriveCtx(context.WithCancel)
		env.checker.SetFail(func(q authz.Query) error {
			if q.Object == "tenant:"+validTenantID && q.Relation == "admin" {
				env.outerInterceptor.Cancel()
				return errors.New("simulated checker failure")
			}
			return nil
		})

		req := validRequest(t, edgev1.CreateEdgeRequest_builder{
			Name: proto.String("edge-alpha"),
		}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)

		_, err := env.edgeCli.CreateEdge(context.Background(), req)

		if got := env.outerInterceptor.AfterCtxErr(); got != context.Canceled {
			t.Fatalf("call context err = %v, want context.Canceled", got)
		}
		if env.handlers.DidRun("CreateEdge") {
			t.Error("handler ran, want not run")
		}
		outerErr := env.outerInterceptor.OuterErr()
		if outerErr != context.Canceled {
			t.Errorf("outer interceptor err = %v, want context.Canceled", outerErr)
		}
		if connect.CodeOf(err) != connect.CodeCanceled {
			t.Errorf("client code = %v, want %v", connect.CodeOf(err), connect.CodeCanceled)
		}

		envLive := setupCancellableTestEnv(t)
		envLive.checker.SetFail(func(q authz.Query) error {
			if q.Object == "tenant:"+validTenantID && q.Relation == "admin" {
				return errors.New("simulated checker failure")
			}
			return nil
		})
		reqLive := validRequest(t, edgev1.CreateEdgeRequest_builder{
			Name: proto.String("edge-alpha"),
		}.Build())
		reqLive.Header().Set("X-FlowSeer-Tenant", validTenantID)
		_, errLive := envLive.edgeCli.CreateEdge(context.Background(), reqLive)
		if connect.CodeOf(errLive) != connect.CodeUnavailable {
			t.Errorf("live call code = %v, want %v", connect.CodeOf(errLive), connect.CodeUnavailable)
		}
		if got := errCodeOf(t, errLive); got != authz.ErrCodeUnavailable.String() {
			t.Errorf("live call error code = %q, want %q", got, authz.ErrCodeUnavailable)
		}
		if got := retryDispositionOf(t, errLive); got != errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
			t.Errorf("live call retry disposition = %v, want retryable", got)
		}
	})

	t.Run("Require on tenant#full_payload cancels context and fails query yields Canceled", func(t *testing.T) {
		env := setupCancellableTestEnv(t)
		env.outerInterceptor.setDeriveCtx(context.WithCancel)
		env.checker.SetFail(func(q authz.Query) error {
			if q.Object == "tenant:"+validTenantID && q.Relation == "full_payload" {
				env.outerInterceptor.Cancel()
				return errors.New("simulated checker failure")
			}
			return nil
		})

		var (
			mu         sync.Mutex
			requireErr error
		)
		env.handlers.SetCreateCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.CreateCaptureSessionRequest]) (*connect.Response[capturev1.CreateCaptureSessionResponse], error) {
			rErr := authz.Require(ctx, "full_payload", "tenant", validTenantID)
			mu.Lock()
			requireErr = rErr
			mu.Unlock()
			return nil, rErr
		})

		req := validRequest(t, validCreateCaptureSessionRequest(t))
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)
		_, err := env.captureCli.CreateCaptureSession(context.Background(), req)

		if got := env.outerInterceptor.AfterCtxErr(); got != context.Canceled {
			t.Fatalf("call context err = %v, want context.Canceled", got)
		}
		mu.Lock()
		rErr := requireErr
		mu.Unlock()
		if rErr != context.Canceled {
			t.Errorf("Require returned %v, want context.Canceled", rErr)
		}
		outerErr := env.outerInterceptor.OuterErr()
		if outerErr != context.Canceled {
			t.Errorf("outer interceptor err = %v, want context.Canceled", outerErr)
		}
		if connect.CodeOf(err) != connect.CodeCanceled {
			t.Errorf("client code = %v, want %v", connect.CodeOf(err), connect.CodeCanceled)
		}

		envLive := setupCancellableTestEnv(t)
		envLive.checker.SetFail(func(q authz.Query) error {
			if q.Object == "tenant:"+validTenantID && q.Relation == "full_payload" {
				return errors.New("simulated checker failure")
			}
			return nil
		})
		envLive.handlers.SetCreateCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.CreateCaptureSessionRequest]) (*connect.Response[capturev1.CreateCaptureSessionResponse], error) {
			rErr := authz.Require(ctx, "full_payload", "tenant", validTenantID)
			return nil, rErr
		})
		reqLive := validRequest(t, validCreateCaptureSessionRequest(t))
		reqLive.Header().Set("X-FlowSeer-Tenant", validTenantID)
		_, errLive := envLive.captureCli.CreateCaptureSession(context.Background(), reqLive)
		if connect.CodeOf(errLive) != connect.CodeUnavailable {
			t.Errorf("live call code = %v, want %v", connect.CodeOf(errLive), connect.CodeUnavailable)
		}
		if got := errCodeOf(t, errLive); got != authz.ErrCodeUnavailable.String() {
			t.Errorf("live call error code = %q, want %q", got, authz.ErrCodeUnavailable)
		}
		if got := retryDispositionOf(t, errLive); got != errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
			t.Errorf("live call retry disposition = %v, want retryable", got)
		}
	})

	t.Run("checker cancels context and returns short BatchCheck answer yields Canceled", func(t *testing.T) {
		env := setupCancellableTestEnv(t)
		env.outerInterceptor.setDeriveCtx(context.WithCancel)
		env.checker.SetBatchAnswers(func(_ []authz.Query) []bool {
			env.outerInterceptor.Cancel()
			return []bool{}
		})

		var (
			mu         sync.Mutex
			requireErr error
		)
		env.handlers.SetListCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
			rErr := authz.Require(ctx, "capture", "edge", validEdgeID)
			mu.Lock()
			requireErr = rErr
			mu.Unlock()
			return nil, rErr
		})

		req := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)
		_, err := env.captureCli.ListCaptureSessions(context.Background(), req)

		if got := env.outerInterceptor.AfterCtxErr(); got != context.Canceled {
			t.Fatalf("call context err = %v, want context.Canceled", got)
		}
		mu.Lock()
		rErr := requireErr
		mu.Unlock()
		if rErr != context.Canceled {
			t.Errorf("Require returned %v, want context.Canceled", rErr)
		}
		outerErr := env.outerInterceptor.OuterErr()
		if outerErr != context.Canceled {
			t.Errorf("outer interceptor err = %v, want context.Canceled", outerErr)
		}
		if connect.CodeOf(err) != connect.CodeCanceled {
			t.Errorf("client code = %v, want %v", connect.CodeOf(err), connect.CodeCanceled)
		}

		envLive := setupCancellableTestEnv(t)
		envLive.checker.SetBatchAnswers(func(_ []authz.Query) []bool {
			return []bool{}
		})
		envLive.handlers.SetListCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
			rErr := authz.Require(ctx, "capture", "edge", validEdgeID)
			return nil, rErr
		})
		reqLive := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
		reqLive.Header().Set("X-FlowSeer-Tenant", validTenantID)
		_, errLive := envLive.captureCli.ListCaptureSessions(context.Background(), reqLive)
		if connect.CodeOf(errLive) != connect.CodeUnavailable {
			t.Errorf("live call code = %v, want %v", connect.CodeOf(errLive), connect.CodeUnavailable)
		}
		if got := errCodeOf(t, errLive); got != authz.ErrCodeUnavailable.String() {
			t.Errorf("live call error code = %q, want %q", got, authz.ErrCodeUnavailable)
		}
		if got := retryDispositionOf(t, errLive); got != errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
			t.Errorf("live call retry disposition = %v, want retryable", got)
		}
	})

	t.Run("Require under canceled context checker fails yields Canceled", func(t *testing.T) {
		env := setupCancellableTestEnv(t)
		env.outerInterceptor.setDeriveCtx(context.WithCancel)
		env.checker.SetFail(func(q authz.Query) error {
			if strings.HasPrefix(q.Object, "edge:") {
				env.outerInterceptor.Cancel()
				return errors.New("simulated checker failure")
			}
			return nil
		})

		var (
			mu         sync.Mutex
			requireErr error
		)
		env.handlers.SetListCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
			rErr := authz.Require(ctx, "capture", "edge", validEdgeID)
			mu.Lock()
			requireErr = rErr
			mu.Unlock()
			return nil, rErr
		})

		req := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)
		_, err := env.captureCli.ListCaptureSessions(context.Background(), req)

		if got := env.outerInterceptor.AfterCtxErr(); got != context.Canceled {
			t.Fatalf("call context err = %v, want context.Canceled", got)
		}
		mu.Lock()
		rErr := requireErr
		mu.Unlock()
		if rErr != context.Canceled {
			t.Errorf("Require returned %v, want context.Canceled", rErr)
		}
		outerErr := env.outerInterceptor.OuterErr()
		if outerErr != context.Canceled {
			t.Errorf("outer interceptor err = %v, want context.Canceled", outerErr)
		}
		if connect.CodeOf(err) != connect.CodeCanceled {
			t.Errorf("client code = %v, want %v", connect.CodeOf(err), connect.CodeCanceled)
		}
	})

	t.Run("Filter under canceled context checker fails yields Canceled", func(t *testing.T) {
		env := setupCancellableTestEnv(t)
		env.outerInterceptor.setDeriveCtx(context.WithCancel)
		env.checker.SetFail(func(q authz.Query) error {
			if strings.HasPrefix(q.Object, "edge:") {
				env.outerInterceptor.Cancel()
				return errors.New("simulated checker failure")
			}
			return nil
		})

		var (
			mu        sync.Mutex
			filterErr error
		)
		env.handlers.SetListCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
			_, fErr := authz.Filter(ctx, "capture", "edge", []string{validEdgeID})
			mu.Lock()
			filterErr = fErr
			mu.Unlock()
			return nil, fErr
		})

		req := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)
		_, err := env.captureCli.ListCaptureSessions(context.Background(), req)

		if got := env.outerInterceptor.AfterCtxErr(); got != context.Canceled {
			t.Fatalf("call context err = %v, want context.Canceled", got)
		}
		mu.Lock()
		fErr := filterErr
		mu.Unlock()
		if fErr != context.Canceled {
			t.Errorf("Filter returned %v, want context.Canceled", fErr)
		}
		outerErr := env.outerInterceptor.OuterErr()
		if outerErr != context.Canceled {
			t.Errorf("outer interceptor err = %v, want context.Canceled", outerErr)
		}
		if connect.CodeOf(err) != connect.CodeCanceled {
			t.Errorf("client code = %v, want %v", connect.CodeOf(err), connect.CodeCanceled)
		}
	})

	t.Run("platform check cancels and fails yields Canceled", func(t *testing.T) {
		env := setupCancellableTestEnv(t)
		env.outerInterceptor.setDeriveCtx(context.WithCancel)
		env.checker.SetFail(func(q authz.Query) error {
			if q.Object == "platform:flowseer" {
				env.outerInterceptor.Cancel()
				return errors.New("simulated checker failure")
			}
			return nil
		})

		req := validRequest(t, identityv1.ListTenantsRequest_builder{}.Build())
		_, err := env.tenantCli.ListTenants(context.Background(), req)

		if got := env.outerInterceptor.AfterCtxErr(); got != context.Canceled {
			t.Fatalf("call context err = %v, want context.Canceled", got)
		}
		if env.handlers.DidRun("ListTenants") {
			t.Error("handler ran, want not run")
		}
		outerErr := env.outerInterceptor.OuterErr()
		if outerErr != context.Canceled {
			t.Errorf("outer interceptor err = %v, want context.Canceled", outerErr)
		}
		if connect.CodeOf(err) != connect.CodeCanceled {
			t.Errorf("client code = %v, want %v", connect.CodeOf(err), connect.CodeCanceled)
		}
	})

	t.Run("checker cancels and answers membership query false yields PermissionDenied", func(t *testing.T) {
		env := setupCancellableTestEnv(t)
		env.outerInterceptor.setDeriveCtx(context.WithCancel)
		env.checker.SetFail(func(q authz.Query) error {
			if q.Relation == "member" {
				env.outerInterceptor.Cancel()
			}
			return nil
		})
		env.checker.SetDeny(func(q authz.Query) bool {
			return q.Relation == "member"
		})

		req := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)
		_, err := env.captureCli.ListCaptureSessions(context.Background(), req)

		if got := env.outerInterceptor.AfterCtxErr(); got != context.Canceled {
			t.Fatalf("call context err = %v, want context.Canceled", got)
		}
		if env.handlers.DidRun("ListCaptureSessions") {
			t.Error("handler ran, want not run")
		}
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeDenied.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeDenied)
		}
	})

	t.Run("checker cancels and answers false on Require query yields PermissionDenied", func(t *testing.T) {
		env := setupCancellableTestEnv(t)
		env.outerInterceptor.setDeriveCtx(context.WithCancel)
		env.checker.SetDeny(func(q authz.Query) bool {
			if strings.HasPrefix(q.Object, "edge:") {
				env.outerInterceptor.Cancel()
				return true
			}
			return false
		})

		var (
			mu         sync.Mutex
			requireErr error
		)
		env.handlers.SetListCaptureFn(func(ctx context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
			rErr := authz.Require(ctx, "capture", "edge", validEdgeID)
			mu.Lock()
			requireErr = rErr
			mu.Unlock()
			return nil, rErr
		})

		req := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)
		_, err := env.captureCli.ListCaptureSessions(context.Background(), req)

		if got := env.outerInterceptor.AfterCtxErr(); got != context.Canceled {
			t.Fatalf("call context err = %v, want context.Canceled", got)
		}
		mu.Lock()
		rErr := requireErr
		mu.Unlock()
		if connect.CodeOf(rErr) != connect.CodePermissionDenied {
			t.Errorf("Require code = %v, want %v", connect.CodeOf(rErr), connect.CodePermissionDenied)
		}
		if got := errCodeOf(t, rErr); got != authz.ErrCodeDenied.String() {
			t.Errorf("Require error code = %q, want %q", got, authz.ErrCodeDenied)
		}
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("client code = %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeDenied.String() {
			t.Errorf("client error code = %q, want %q", got, authz.ErrCodeDenied)
		}
	})

	t.Run("checker cancels and answers false on platform query yields PermissionDenied", func(t *testing.T) {
		env := setupCancellableTestEnv(t)
		env.outerInterceptor.setDeriveCtx(context.WithCancel)
		env.checker.SetDeny(func(q authz.Query) bool {
			if q.Object == "platform:flowseer" {
				env.outerInterceptor.Cancel()
				return true
			}
			return false
		})

		req := validRequest(t, identityv1.ListTenantsRequest_builder{}.Build())
		_, err := env.tenantCli.ListTenants(context.Background(), req)

		if got := env.outerInterceptor.AfterCtxErr(); got != context.Canceled {
			t.Fatalf("call context err = %v, want context.Canceled", got)
		}
		if env.handlers.DidRun("ListTenants") {
			t.Error("handler ran, want not run")
		}
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("got code %v, want %v", connect.CodeOf(err), connect.CodePermissionDenied)
		}
		if got := errCodeOf(t, err); got != authz.ErrCodeDenied.String() {
			t.Errorf("got error code %q, want %q", got, authz.ErrCodeDenied)
		}
	})

	t.Run("deferred rule handler cancels context and returns response yields Internal", func(t *testing.T) {
		env := setupCancellableTestEnv(t)
		env.outerInterceptor.setDeriveCtx(context.WithCancel)
		env.handlers.SetListCaptureFn(func(_ context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
			env.outerInterceptor.Cancel()
			return validResponse(t, capturev1.ListCaptureSessionsResponse_builder{}.Build()), nil
		})

		req := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)
		resp, err := env.captureCli.ListCaptureSessions(context.Background(), req)

		if got := env.outerInterceptor.AfterCtxErr(); got != context.Canceled {
			t.Fatalf("call context err = %v, want context.Canceled", got)
		}
		if resp != nil {
			t.Errorf("got response %v, want nil", resp)
		}
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

	t.Run("deferred rule handler cancels context and returns sentinel yields Canceled", func(t *testing.T) {
		env := setupCancellableTestEnv(t)
		env.outerInterceptor.setDeriveCtx(context.WithCancel)
		sentinelErr := connect.NewError(connect.CodeNotFound, errors.New("sentinel missing object"))
		env.handlers.SetListCaptureFn(func(_ context.Context, _ *connect.Request[capturev1.ListCaptureSessionsRequest]) (*connect.Response[capturev1.ListCaptureSessionsResponse], error) {
			env.outerInterceptor.Cancel()
			return nil, sentinelErr
		})

		req := validRequest(t, capturev1.ListCaptureSessionsRequest_builder{}.Build())
		req.Header().Set("X-FlowSeer-Tenant", validTenantID)
		_, err := env.captureCli.ListCaptureSessions(context.Background(), req)

		if got := env.outerInterceptor.AfterCtxErr(); got != context.Canceled {
			t.Fatalf("call context err = %v, want context.Canceled", got)
		}
		outerErr := env.outerInterceptor.OuterErr()
		if outerErr != context.Canceled {
			t.Errorf("outer interceptor err = %v, want context.Canceled", outerErr)
		}
		if errors.Is(outerErr, sentinelErr) {
			t.Errorf("outer interceptor saw sentinel error %v, want context.Canceled alone", outerErr)
		}
		if connect.CodeOf(err) != connect.CodeCanceled {
			t.Errorf("client code = %v, want %v", connect.CodeOf(err), connect.CodeCanceled)
		}
	})
}
