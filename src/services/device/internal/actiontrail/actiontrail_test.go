package actiontrail_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"buf.build/go/protovalidate"
	connect "connectrpc.com/connect"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	capturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1"
	capturev1connect "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1/capturev1connect"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	edgev1connect "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1/edgev1connect"
	identityapiv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/identity/v1"
	identityapiv1connect "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/identity/v1/identityv1connect"
	errsv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/errs/v1"
	operatorv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/operator/v1"
	modelcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	modeledgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/common/tenant"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/actiontrail"
	"go.aledante.io/FlowSeer/src/services/device/internal/auditapi"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz/authztest"
	"go.aledante.io/FlowSeer/src/services/device/internal/edgestore"
)

func codeOf(t *testing.T, err error) string {
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

const (
	testTenant              = "00000000-0000-0000-0000-000000000001"
	testEdgeID              = "11111111-1111-4111-8111-111111111111"
	testCreatedEdgeID       = "22222222-2222-4222-8222-222222222222"
	testCreatedSessionID    = "33333333-3333-4333-8333-333333333333"
	testTailSessionID       = "44444444-4444-4444-8444-444444444444"
	testDownloadSessionID   = "55555555-5555-4555-8555-555555555555"
	testFailedSessionID     = "66666666-6666-4666-8666-666666666666"
	testUnpreparedSessionID = "77777777-7777-4777-8777-777777777777"
)

func TestEveryEdgeAdminProcedureIsRecorded(t *testing.T) {
	const wantProcedures = 6

	methods := edgev1.File_flowseer_api_edge_v1_edge_admin_service_proto.Services().ByName("EdgeAdminService").Methods()
	if methods.Len() != wantProcedures {
		t.Fatalf("EdgeAdminService has %d methods, want %d", methods.Len(), wantProcedures)
	}
	ctx := context.Background()
	for i := 0; i < methods.Len(); i++ {
		m := methods.Get(i)
		proc := fmt.Sprintf("/%s/%s", m.Parent().FullName(), m.Name())
		h := newTestHarness(t, time.Now())
		switch m.Name() {
		case "CreateEdge":
			_, _ = h.adminClient.CreateEdge(ctx, connect.NewRequest(&edgev1.CreateEdgeRequest{}))
		case "IssueSetupKey":
			_, _ = h.adminClient.IssueSetupKey(ctx, connect.NewRequest(&edgev1.IssueSetupKeyRequest{}))
		case "RevokeSetupKey":
			_, _ = h.adminClient.RevokeSetupKey(ctx, connect.NewRequest(&edgev1.RevokeSetupKeyRequest{}))
		case "RetireEdge":
			_, _ = h.adminClient.RetireEdge(ctx, connect.NewRequest(&edgev1.RetireEdgeRequest{}))
		case "GetEdge":
			_, _ = h.adminClient.GetEdge(ctx, connect.NewRequest(&edgev1.GetEdgeRequest{}))
		case "ListEdges":
			_, _ = h.adminClient.ListEdges(ctx, connect.NewRequest(&edgev1.ListEdgesRequest{}))
		default:
			t.Fatalf("unhandled descriptor method %s", m.Name())
		}
		events := h.pub.getEvents()
		if len(events) == 0 {
			t.Errorf("procedure %q was not recorded by interceptor", proc)
		}
	}
}

type recordedEvent struct {
	subject string
	data    []byte
	msgID   string
	event   *operatorv1.OperatorActionEvent
}

type fakePublisher struct {
	mu             sync.Mutex
	events         []recordedEvent
	failAttempt    bool
	failCompletion bool
	failErr        error
}

func (p *fakePublisher) Publish(ctx context.Context, subject string, data []byte, msgID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if ctx.Err() != nil {
		return ctx.Err()
	}

	event := &operatorv1.OperatorActionEvent{}
	if err := proto.Unmarshal(data, event); err != nil {
		return err
	}
	if err := protovalidate.Validate(event); err != nil {
		return err
	}

	if p.failAttempt && event.WhichDetail() == operatorv1.OperatorActionEvent_Attempted_case {
		if p.failErr != nil {
			return p.failErr
		}
		return errors.New("simulated attempt publish failure")
	}

	if p.failCompletion && event.WhichDetail() == operatorv1.OperatorActionEvent_Completed_case {
		if p.failErr != nil {
			return p.failErr
		}
		return errors.New("simulated completion publish failure")
	}

	p.events = append(p.events, recordedEvent{
		subject: subject,
		data:    data,
		msgID:   msgID,
		event:   event,
	})
	return nil
}

func (p *fakePublisher) getEvents() []recordedEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	copied := make([]recordedEvent, len(p.events))
	copy(copied, p.events)
	return copied
}

type logRecorder struct {
	mu      sync.Mutex
	records []slog.Record
}

func (r *logRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (r *logRecorder) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, rec)
	return nil
}
func (r *logRecorder) WithAttrs(_ []slog.Attr) slog.Handler { return r }
func (r *logRecorder) WithGroup(_ string) slog.Handler      { return r }

func (r *logRecorder) getRecords() []slog.Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	copied := make([]slog.Record, len(r.records))
	copy(copied, r.records)
	return copied
}

type fakeEdgeAdminHandler struct {
	edgev1connect.UnimplementedEdgeAdminServiceHandler
	createEdgeCalls     int
	createEdgeFunc      func(context.Context, *connect.Request[edgev1.CreateEdgeRequest]) (*connect.Response[edgev1.CreateEdgeResponse], error)
	issueSetupKeyCalls  int
	issueSetupKeyFunc   func(context.Context, *connect.Request[edgev1.IssueSetupKeyRequest]) (*connect.Response[edgev1.IssueSetupKeyResponse], error)
	revokeSetupKeyCalls int
	revokeSetupKeyFunc  func(context.Context, *connect.Request[edgev1.RevokeSetupKeyRequest]) (*connect.Response[edgev1.RevokeSetupKeyResponse], error)
	retireEdgeCalls     int
	retireEdgeFunc      func(context.Context, *connect.Request[edgev1.RetireEdgeRequest]) (*connect.Response[edgev1.RetireEdgeResponse], error)
	getEdgeCalls        int
	getEdgeFunc         func(context.Context, *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error)
	listEdgesCalls      int
	listEdgesFunc       func(context.Context, *connect.Request[edgev1.ListEdgesRequest]) (*connect.Response[edgev1.ListEdgesResponse], error)
}

func (h *fakeEdgeAdminHandler) CreateEdge(ctx context.Context, req *connect.Request[edgev1.CreateEdgeRequest]) (*connect.Response[edgev1.CreateEdgeResponse], error) {
	h.createEdgeCalls++
	if h.createEdgeFunc != nil {
		return h.createEdgeFunc(ctx, req)
	}
	return h.UnimplementedEdgeAdminServiceHandler.CreateEdge(ctx, req)
}

func (h *fakeEdgeAdminHandler) IssueSetupKey(ctx context.Context, req *connect.Request[edgev1.IssueSetupKeyRequest]) (*connect.Response[edgev1.IssueSetupKeyResponse], error) {
	h.issueSetupKeyCalls++
	if h.issueSetupKeyFunc != nil {
		return h.issueSetupKeyFunc(ctx, req)
	}
	return h.UnimplementedEdgeAdminServiceHandler.IssueSetupKey(ctx, req)
}

func (h *fakeEdgeAdminHandler) RevokeSetupKey(ctx context.Context, req *connect.Request[edgev1.RevokeSetupKeyRequest]) (*connect.Response[edgev1.RevokeSetupKeyResponse], error) {
	h.revokeSetupKeyCalls++
	if h.revokeSetupKeyFunc != nil {
		return h.revokeSetupKeyFunc(ctx, req)
	}
	return h.UnimplementedEdgeAdminServiceHandler.RevokeSetupKey(ctx, req)
}

func (h *fakeEdgeAdminHandler) RetireEdge(ctx context.Context, req *connect.Request[edgev1.RetireEdgeRequest]) (*connect.Response[edgev1.RetireEdgeResponse], error) {
	h.retireEdgeCalls++
	if h.retireEdgeFunc != nil {
		return h.retireEdgeFunc(ctx, req)
	}
	return h.UnimplementedEdgeAdminServiceHandler.RetireEdge(ctx, req)
}

func (h *fakeEdgeAdminHandler) GetEdge(ctx context.Context, req *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error) {
	h.getEdgeCalls++
	if h.getEdgeFunc != nil {
		return h.getEdgeFunc(ctx, req)
	}
	return h.UnimplementedEdgeAdminServiceHandler.GetEdge(ctx, req)
}

func (h *fakeEdgeAdminHandler) ListEdges(ctx context.Context, req *connect.Request[edgev1.ListEdgesRequest]) (*connect.Response[edgev1.ListEdgesResponse], error) {
	h.listEdgesCalls++
	if h.listEdgesFunc != nil {
		return h.listEdgesFunc(ctx, req)
	}
	return h.UnimplementedEdgeAdminServiceHandler.ListEdges(ctx, req)
}

type fakeCaptureHandler struct {
	capturev1connect.UnimplementedCaptureServiceHandler
	createSessionCalls   int
	createSessionFunc    func(context.Context, *connect.Request[capturev1.CreateCaptureSessionRequest]) (*connect.Response[capturev1.CreateCaptureSessionResponse], error)
	tailSessionCalls     int
	tailSessionFunc      func(context.Context, *connect.Request[capturev1.TailCaptureSessionRequest], *connect.ServerStream[capturev1.TailCaptureSessionResponse]) error
	downloadSessionCalls int
	downloadSessionFunc  func(context.Context, *connect.Request[capturev1.DownloadCaptureSessionRequest], *connect.ServerStream[capturev1.DownloadCaptureSessionResponse]) error
}

func (h *fakeCaptureHandler) CreateCaptureSession(ctx context.Context, req *connect.Request[capturev1.CreateCaptureSessionRequest]) (*connect.Response[capturev1.CreateCaptureSessionResponse], error) {
	h.createSessionCalls++
	if h.createSessionFunc != nil {
		return h.createSessionFunc(ctx, req)
	}
	return h.UnimplementedCaptureServiceHandler.CreateCaptureSession(ctx, req)
}

func (h *fakeCaptureHandler) TailCaptureSession(ctx context.Context, req *connect.Request[capturev1.TailCaptureSessionRequest], stream *connect.ServerStream[capturev1.TailCaptureSessionResponse]) error {
	h.tailSessionCalls++
	if h.tailSessionFunc != nil {
		return h.tailSessionFunc(ctx, req, stream)
	}
	return h.UnimplementedCaptureServiceHandler.TailCaptureSession(ctx, req, stream)
}

func (h *fakeCaptureHandler) DownloadCaptureSession(ctx context.Context, req *connect.Request[capturev1.DownloadCaptureSessionRequest], stream *connect.ServerStream[capturev1.DownloadCaptureSessionResponse]) error {
	h.downloadSessionCalls++
	if h.downloadSessionFunc != nil {
		return h.downloadSessionFunc(ctx, req, stream)
	}
	return h.UnimplementedCaptureServiceHandler.DownloadCaptureSession(ctx, req, stream)
}

// fakeTenantHandler answers CreateTenant with its canned response, or the
// service's unimplemented error when none is set.
type fakeTenantHandler struct {
	identityapiv1connect.UnimplementedTenantServiceHandler
	createTenant      *identityapiv1.CreateTenantResponse
	createTenantCalls int
	err               error
}

func (h *fakeTenantHandler) CreateTenant(ctx context.Context, req *connect.Request[identityapiv1.CreateTenantRequest]) (*connect.Response[identityapiv1.CreateTenantResponse], error) {
	h.createTenantCalls++
	if h.err != nil {
		return nil, h.err
	}
	if h.createTenant == nil {
		return h.UnimplementedTenantServiceHandler.CreateTenant(ctx, req)
	}
	return connect.NewResponse(h.createTenant), nil
}

// fakeTenantAdminHandler answers each change RPC with its canned response, or
// the service's unimplemented error when none is set.
type fakeTenantAdminHandler struct {
	identityapiv1connect.UnimplementedTenantAdminServiceHandler
	enrollMember      *identityapiv1.EnrollMemberResponse
	removeMember      *identityapiv1.RemoveMemberResponse
	createRole        *identityapiv1.CreateRoleResponse
	deleteRole        *identityapiv1.DeleteRoleResponse
	assignRole        *identityapiv1.AssignRoleResponse
	unassignRole      *identityapiv1.UnassignRoleResponse
	connectPartner    *identityapiv1.ConnectPartnerResponse
	disconnectPartner *identityapiv1.DisconnectPartnerResponse
	grantFullPayload  *identityapiv1.GrantFullPayloadResponse
	revokeFullPayload *identityapiv1.RevokeFullPayloadResponse
	err               error
}

// answer returns the canned response, the configured error, or fallback when
// the test set neither.
func answer[Resp any](h *fakeTenantAdminHandler, canned *Resp, fallback func() (*connect.Response[Resp], error)) (*connect.Response[Resp], error) {
	if h.err != nil {
		return nil, h.err
	}
	if canned == nil {
		return fallback()
	}
	return connect.NewResponse(canned), nil
}

func (h *fakeTenantAdminHandler) EnrollMember(ctx context.Context, req *connect.Request[identityapiv1.EnrollMemberRequest]) (*connect.Response[identityapiv1.EnrollMemberResponse], error) {
	return answer(h, h.enrollMember, func() (*connect.Response[identityapiv1.EnrollMemberResponse], error) {
		return h.UnimplementedTenantAdminServiceHandler.EnrollMember(ctx, req)
	})
}

func (h *fakeTenantAdminHandler) RemoveMember(ctx context.Context, req *connect.Request[identityapiv1.RemoveMemberRequest]) (*connect.Response[identityapiv1.RemoveMemberResponse], error) {
	return answer(h, h.removeMember, func() (*connect.Response[identityapiv1.RemoveMemberResponse], error) {
		return h.UnimplementedTenantAdminServiceHandler.RemoveMember(ctx, req)
	})
}

func (h *fakeTenantAdminHandler) CreateRole(ctx context.Context, req *connect.Request[identityapiv1.CreateRoleRequest]) (*connect.Response[identityapiv1.CreateRoleResponse], error) {
	return answer(h, h.createRole, func() (*connect.Response[identityapiv1.CreateRoleResponse], error) {
		return h.UnimplementedTenantAdminServiceHandler.CreateRole(ctx, req)
	})
}

func (h *fakeTenantAdminHandler) DeleteRole(ctx context.Context, req *connect.Request[identityapiv1.DeleteRoleRequest]) (*connect.Response[identityapiv1.DeleteRoleResponse], error) {
	return answer(h, h.deleteRole, func() (*connect.Response[identityapiv1.DeleteRoleResponse], error) {
		return h.UnimplementedTenantAdminServiceHandler.DeleteRole(ctx, req)
	})
}

func (h *fakeTenantAdminHandler) AssignRole(ctx context.Context, req *connect.Request[identityapiv1.AssignRoleRequest]) (*connect.Response[identityapiv1.AssignRoleResponse], error) {
	return answer(h, h.assignRole, func() (*connect.Response[identityapiv1.AssignRoleResponse], error) {
		return h.UnimplementedTenantAdminServiceHandler.AssignRole(ctx, req)
	})
}

func (h *fakeTenantAdminHandler) UnassignRole(ctx context.Context, req *connect.Request[identityapiv1.UnassignRoleRequest]) (*connect.Response[identityapiv1.UnassignRoleResponse], error) {
	return answer(h, h.unassignRole, func() (*connect.Response[identityapiv1.UnassignRoleResponse], error) {
		return h.UnimplementedTenantAdminServiceHandler.UnassignRole(ctx, req)
	})
}

func (h *fakeTenantAdminHandler) ConnectPartner(ctx context.Context, req *connect.Request[identityapiv1.ConnectPartnerRequest]) (*connect.Response[identityapiv1.ConnectPartnerResponse], error) {
	return answer(h, h.connectPartner, func() (*connect.Response[identityapiv1.ConnectPartnerResponse], error) {
		return h.UnimplementedTenantAdminServiceHandler.ConnectPartner(ctx, req)
	})
}

func (h *fakeTenantAdminHandler) DisconnectPartner(ctx context.Context, req *connect.Request[identityapiv1.DisconnectPartnerRequest]) (*connect.Response[identityapiv1.DisconnectPartnerResponse], error) {
	return answer(h, h.disconnectPartner, func() (*connect.Response[identityapiv1.DisconnectPartnerResponse], error) {
		return h.UnimplementedTenantAdminServiceHandler.DisconnectPartner(ctx, req)
	})
}

func (h *fakeTenantAdminHandler) GrantFullPayload(ctx context.Context, req *connect.Request[identityapiv1.GrantFullPayloadRequest]) (*connect.Response[identityapiv1.GrantFullPayloadResponse], error) {
	return answer(h, h.grantFullPayload, func() (*connect.Response[identityapiv1.GrantFullPayloadResponse], error) {
		return h.UnimplementedTenantAdminServiceHandler.GrantFullPayload(ctx, req)
	})
}

func (h *fakeTenantAdminHandler) RevokeFullPayload(ctx context.Context, req *connect.Request[identityapiv1.RevokeFullPayloadRequest]) (*connect.Response[identityapiv1.RevokeFullPayloadResponse], error) {
	return answer(h, h.revokeFullPayload, func() (*connect.Response[identityapiv1.RevokeFullPayloadResponse], error) {
		return h.UnimplementedTenantAdminServiceHandler.RevokeFullPayload(ctx, req)
	})
}

type contextInjector struct {
	principal       authn.Principal
	tenantID        string
	injectPrincipal bool
	injectTenant    bool
}

// testTenantHeader overrides the injector's tenant for one call, so a test can
// act for many tenants without writing the injector while the server reads it.
const testTenantHeader = "X-Test-Tenant"

func (ci *contextInjector) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if ci.injectPrincipal {
			ctx = authn.NewContext(ctx, ci.principal)
		}
		if ci.injectTenant {
			tenantID := ci.tenantID
			if override := req.Header().Get(testTenantHeader); override != "" {
				tenantID = override
			}
			ctx = tenant.WithTenant(ctx, tenantID)
		}
		return next(ctx, req)
	}
}

func (ci *contextInjector) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if ci.injectPrincipal {
			ctx = authn.NewContext(ctx, ci.principal)
		}
		if ci.injectTenant {
			ctx = tenant.WithTenant(ctx, ci.tenantID)
		}
		return next(ctx, conn)
	}
}

func (ci *contextInjector) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

type testHarness struct {
	server         *httptest.Server
	pub            *fakePublisher
	logs           *logRecorder
	edgeHandler    *fakeEdgeAdminHandler
	capHandler     *fakeCaptureHandler
	tenantHandler  *fakeTenantHandler
	idAdminHandler *fakeTenantAdminHandler
	injector       *contextInjector
	adminClient    edgev1connect.EdgeAdminServiceClient
	captureClient  capturev1connect.CaptureServiceClient
	tenantClient   identityapiv1connect.TenantServiceClient
	idAdminClient  identityapiv1connect.TenantAdminServiceClient
}

func newTestHarness(t *testing.T, fixedTime time.Time) *testHarness {
	t.Helper()
	pub := &fakePublisher{}
	h := newHarnessOver(t, fixedTime, pub)
	h.pub = pub
	return h
}

// newHarnessOver serves the edge, capture, and identity services behind the
// trail interceptor, publishing to pub.
func newHarnessOver(t *testing.T, fixedTime time.Time, pub actiontrail.Publisher) *testHarness {
	t.Helper()
	logs := &logRecorder{}
	logger := slog.New(logs)

	interceptor := actiontrail.NewInterceptor(pub, func() time.Time { return fixedTime }, logger)

	injector := &contextInjector{
		principal: authn.Principal{
			ID:      "principal-1",
			Issuer:  "https://issuer.example.com",
			Subject: "operator-42",
		},
		tenantID:        testTenant,
		injectPrincipal: true,
		injectTenant:    true,
	}

	edgeHandler := &fakeEdgeAdminHandler{}
	capHandler := &fakeCaptureHandler{}
	tenantHandler := &fakeTenantHandler{}
	idAdminHandler := &fakeTenantAdminHandler{}

	opts := connect.WithInterceptors(injector, interceptor)

	mux := http.NewServeMux()
	edgePath, edgeH := edgev1connect.NewEdgeAdminServiceHandler(edgeHandler, opts)
	mux.Handle(edgePath, edgeH)
	capPath, capH := capturev1connect.NewCaptureServiceHandler(capHandler, opts)
	mux.Handle(capPath, capH)
	tenantPath, tenantH := identityapiv1connect.NewTenantServiceHandler(tenantHandler, opts)
	mux.Handle(tenantPath, tenantH)
	idAdminPath, idAdminH := identityapiv1connect.NewTenantAdminServiceHandler(idAdminHandler, opts)
	mux.Handle(idAdminPath, idAdminH)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return &testHarness{
		server:         server,
		logs:           logs,
		edgeHandler:    edgeHandler,
		capHandler:     capHandler,
		tenantHandler:  tenantHandler,
		idAdminHandler: idAdminHandler,
		injector:       injector,
		adminClient:    edgev1connect.NewEdgeAdminServiceClient(server.Client(), server.URL),
		captureClient:  capturev1connect.NewCaptureServiceClient(server.Client(), server.URL),
		tenantClient:   identityapiv1connect.NewTenantServiceClient(server.Client(), server.URL),
		idAdminClient:  identityapiv1connect.NewTenantAdminServiceClient(server.Client(), server.URL),
	}
}

func edgeGlobalRef(edgeID string) *modeledgev1.EdgeGlobalRef {
	ref := &modeledgev1.EdgeGlobalRef{}
	edgeLocal := &modeledgev1.EdgeLocalRef{}
	edgeLocal.SetId(edgeID)
	ref.SetEdge(edgeLocal)
	return ref
}

func sessionGlobalRef(sessionID string) *modelcapturev1.CaptureSessionGlobalRef {
	ref := &modelcapturev1.CaptureSessionGlobalRef{}
	ref.SetEdge(edgeGlobalRef(testEdgeID))
	sessLocal := &modelcapturev1.CaptureSessionLocalRef{}
	sessLocal.SetId(sessionID)
	ref.SetCaptureSession(sessLocal)
	return ref
}

func testMemberRef() *identityv1.OperatorRef {
	ref := &identityv1.OperatorRef{}
	ref.SetIssuer(testMemberIssuer)
	ref.SetSubject(testMemberSubject)
	return ref
}

func tenantGlobalRef(id string) *identityv1.TenantGlobalRef {
	ref := &identityv1.TenantGlobalRef{}
	local := &identityv1.TenantLocalRef{}
	local.SetId(id)
	ref.SetTenant(local)
	return ref
}

func roleGlobalRef(id string) *identityv1.RoleGlobalRef {
	ref := &identityv1.RoleGlobalRef{}
	local := &identityv1.RoleLocalRef{}
	local.SetId(id)
	ref.SetRole(local)
	return ref
}

func TestIssueSetupKeyTrailAttemptAndCompletion(t *testing.T) {
	ctx := context.Background()
	fixedTime := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	h := newTestHarness(t, fixedTime)

	targetEdge := edgeGlobalRef(testEdgeID)
	h.edgeHandler.issueSetupKeyFunc = func(_ context.Context, _ *connect.Request[edgev1.IssueSetupKeyRequest]) (*connect.Response[edgev1.IssueSetupKeyResponse], error) {
		resp := &edgev1.IssueSetupKeyResponse{}
		prov := &modeledgev1.EdgeProvisioning{}
		prov.SetSetupKey("setup-key-secret-123")
		resp.SetProvisioning(prov)
		return connect.NewResponse(resp), nil
	}

	req := &edgev1.IssueSetupKeyRequest{}
	req.SetEdge(targetEdge)
	resp, err := h.adminClient.IssueSetupKey(ctx, connect.NewRequest(req))
	if err != nil {
		t.Fatalf("IssueSetupKey failed: %v", err)
	}
	if resp.Msg.GetProvisioning().GetSetupKey() != "setup-key-secret-123" {
		t.Fatalf("got key %q, want setup-key-secret-123", resp.Msg.GetProvisioning().GetSetupKey())
	}
	if h.edgeHandler.issueSetupKeyCalls != 1 {
		t.Fatalf("handler calls = %d, want 1", h.edgeHandler.issueSetupKeyCalls)
	}

	events := h.pub.getEvents()
	if len(events) != 2 {
		t.Fatalf("published %d events, want 2", len(events))
	}

	wantSubject := "flowseer." + testTenant + ".operator.action.setup_key_issue"
	for idx, rec := range events {
		if rec.subject != wantSubject {
			t.Errorf("event %d subject = %q, want %q", idx, rec.subject, wantSubject)
		}
		if rec.msgID != rec.event.GetEventId() {
			t.Errorf("event %d msgID = %q, want %q", idx, rec.msgID, rec.event.GetEventId())
		}
		if rec.event.GetOperator().GetIssuer() != "https://issuer.example.com" {
			t.Errorf("event %d operator issuer = %q, want https://issuer.example.com", idx, rec.event.GetOperator().GetIssuer())
		}
		if rec.event.GetOperator().GetSubject() != "operator-42" {
			t.Errorf("event %d operator subject = %q, want operator-42", idx, rec.event.GetOperator().GetSubject())
		}
		if rec.event.GetEdge().GetEdge().GetId() != testEdgeID {
			t.Errorf("event %d edge id = %q, want %s", idx, rec.event.GetEdge().GetEdge().GetId(), testEdgeID)
		}
		if rec.event.GetAction() != operatorv1.OperatorAction_OPERATOR_ACTION_SETUP_KEY_ISSUE {
			t.Errorf("event %d action = %v, want SETUP_KEY_ISSUE", idx, rec.event.GetAction())
		}
	}

	if events[0].event.GetCallId() != events[1].event.GetCallId() {
		t.Errorf("call_id mismatch: attempt=%q completion=%q", events[0].event.GetCallId(), events[1].event.GetCallId())
	}
	if events[0].event.GetEventId() == events[1].event.GetEventId() {
		t.Errorf("event_id must be unique across attempt and completion, got %q", events[0].event.GetEventId())
	}

	if events[0].event.WhichDetail() != operatorv1.OperatorActionEvent_Attempted_case {
		t.Errorf("event 0 detail is %v, want Attempted", events[0].event.WhichDetail())
	}
	if events[1].event.WhichDetail() != operatorv1.OperatorActionEvent_Completed_case {
		t.Errorf("event 1 detail is %v, want Completed", events[1].event.WhichDetail())
	}

	completed := events[1].event.GetCompleted()
	if completed.GetOutcome() != operatorv1.OperatorActionOutcome_OPERATOR_ACTION_OUTCOME_SUCCEEDED {
		t.Errorf("completion outcome = %v, want SUCCEEDED", completed.GetOutcome())
	}
	if completed.HasErrorType() || completed.GetErrorType() != "" {
		t.Errorf("completion has unexpected error_type %q", completed.GetErrorType())
	}
}

func TestPublisherFailsAttempt(t *testing.T) {
	ctx := context.Background()
	h := newTestHarness(t, time.Now())
	h.pub.failAttempt = true

	h.edgeHandler.issueSetupKeyFunc = func(_ context.Context, _ *connect.Request[edgev1.IssueSetupKeyRequest]) (*connect.Response[edgev1.IssueSetupKeyResponse], error) {
		t.Fatal("handler should not have been called when attempt publish fails")
		return nil, nil
	}

	req := &edgev1.IssueSetupKeyRequest{}
	req.SetEdge(edgeGlobalRef(testEdgeID))
	_, err := h.adminClient.IssueSetupKey(ctx, connect.NewRequest(req))
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("got connect code %v, want CodeUnavailable", connect.CodeOf(err))
	}
	if got := codeOf(t, err); got != actiontrail.ErrCodeUnavailable.String() {
		t.Fatalf("got errs code %q, want %q", got, actiontrail.ErrCodeUnavailable)
	}
	if h.edgeHandler.issueSetupKeyCalls != 0 {
		t.Fatalf("handler was called %d times, want 0", h.edgeHandler.issueSetupKeyCalls)
	}
	if len(h.pub.getEvents()) != 0 {
		t.Fatalf("published %d events, want 0", len(h.pub.getEvents()))
	}
}

func TestPublisherFailsCompletion(t *testing.T) {
	ctx := context.Background()
	h := newTestHarness(t, time.Now())
	h.pub.failCompletion = true

	h.edgeHandler.issueSetupKeyFunc = func(_ context.Context, _ *connect.Request[edgev1.IssueSetupKeyRequest]) (*connect.Response[edgev1.IssueSetupKeyResponse], error) {
		resp := &edgev1.IssueSetupKeyResponse{}
		prov := &modeledgev1.EdgeProvisioning{}
		prov.SetSetupKey("setup-key-ok")
		resp.SetProvisioning(prov)
		return connect.NewResponse(resp), nil
	}

	req := &edgev1.IssueSetupKeyRequest{}
	req.SetEdge(edgeGlobalRef(testEdgeID))
	resp, err := h.adminClient.IssueSetupKey(ctx, connect.NewRequest(req))
	if err != nil {
		t.Fatalf("IssueSetupKey failed: %v", err)
	}
	if resp.Msg.GetProvisioning().GetSetupKey() != "setup-key-ok" {
		t.Fatalf("got key %q, want setup-key-ok", resp.Msg.GetProvisioning().GetSetupKey())
	}
	if h.edgeHandler.issueSetupKeyCalls != 1 {
		t.Fatalf("handler calls = %d, want 1", h.edgeHandler.issueSetupKeyCalls)
	}

	events := h.pub.getEvents()
	if len(events) != 1 {
		t.Fatalf("published %d events, want 1 (attempt only)", len(events))
	}
	if events[0].event.WhichDetail() != operatorv1.OperatorActionEvent_Attempted_case {
		t.Fatalf("event is not attempted: %v", events[0].event.WhichDetail())
	}

	records := h.logs.getRecords()
	if len(records) != 1 {
		t.Fatalf("logged %d records, want 1", len(records))
	}
	if records[0].Level != slog.LevelError {
		t.Fatalf("log level = %v, want Error", records[0].Level)
	}
	var hasErrorType bool
	records[0].Attrs(func(a slog.Attr) bool {
		if a.Key == "error.type" && a.Value.String() != "" {
			hasErrorType = true
		}
		return true
	})
	if !hasErrorType {
		t.Errorf("log record missing error.type attribute")
	}
}

func TestHandlerError(t *testing.T) {
	ctx := context.Background()
	h := newTestHarness(t, time.Now())

	h.edgeHandler.issueSetupKeyFunc = func(_ context.Context, _ *connect.Request[edgev1.IssueSetupKeyRequest]) (*connect.Response[edgev1.IssueSetupKeyResponse], error) {
		return nil, connect.NewError(connect.CodeUnavailable, errs.New().Code(edgestore.ErrCodeStore).Msg("database down"))
	}

	req := &edgev1.IssueSetupKeyRequest{}
	req.SetEdge(edgeGlobalRef(testEdgeID))
	_, err := h.adminClient.IssueSetupKey(ctx, connect.NewRequest(req))
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	events := h.pub.getEvents()
	if len(events) != 2 {
		t.Fatalf("published %d events, want 2", len(events))
	}

	completed := events[1].event.GetCompleted()
	if completed.GetOutcome() != operatorv1.OperatorActionOutcome_OPERATOR_ACTION_OUTCOME_FAILED {
		t.Fatalf("outcome = %v, want FAILED", completed.GetOutcome())
	}
	if completed.GetErrorType() != string(edgestore.ErrCodeStore) {
		t.Fatalf("error_type = %q, want %q", completed.GetErrorType(), string(edgestore.ErrCodeStore))
	}
}

func TestHandlerPermissionDenied(t *testing.T) {
	ctx := context.Background()
	h := newTestHarness(t, time.Now())

	h.edgeHandler.issueSetupKeyFunc = func(_ context.Context, _ *connect.Request[edgev1.IssueSetupKeyRequest]) (*connect.Response[edgev1.IssueSetupKeyResponse], error) {
		return nil, connect.NewError(connect.CodePermissionDenied, errs.New().Code(authz.ErrCodeDenied).Msg("not allowed"))
	}

	req := &edgev1.IssueSetupKeyRequest{}
	req.SetEdge(edgeGlobalRef(testEdgeID))
	_, err := h.adminClient.IssueSetupKey(ctx, connect.NewRequest(req))
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	events := h.pub.getEvents()
	if len(events) != 2 {
		t.Fatalf("published %d events, want 2", len(events))
	}

	completed := events[1].event.GetCompleted()
	if completed.GetOutcome() != operatorv1.OperatorActionOutcome_OPERATOR_ACTION_OUTCOME_DENIED {
		t.Fatalf("outcome = %v, want DENIED", completed.GetOutcome())
	}
	if completed.GetErrorType() != string(authz.ErrCodeDenied) {
		t.Fatalf("error_type = %q, want %q", completed.GetErrorType(), string(authz.ErrCodeDenied))
	}
}

func TestCreateEdgeCompletionNamesEdge(t *testing.T) {
	ctx := context.Background()
	h := newTestHarness(t, time.Now())

	createdEdgeRef := edgeGlobalRef(testCreatedEdgeID)
	h.edgeHandler.createEdgeFunc = func(_ context.Context, _ *connect.Request[edgev1.CreateEdgeRequest]) (*connect.Response[edgev1.CreateEdgeResponse], error) {
		resp := &edgev1.CreateEdgeResponse{}
		resp.SetEdge(&modeledgev1.EdgeRecord{})
		resp.GetEdge().SetConfig(&modeledgev1.EdgeConfig{})
		resp.GetEdge().GetConfig().SetRef(createdEdgeRef)
		return connect.NewResponse(resp), nil
	}

	req := &edgev1.CreateEdgeRequest{}
	req.SetName("edge-new")
	_, err := h.adminClient.CreateEdge(ctx, connect.NewRequest(req))
	if err != nil {
		t.Fatalf("CreateEdge failed: %v", err)
	}

	events := h.pub.getEvents()
	if len(events) != 2 {
		t.Fatalf("published %d events, want 2", len(events))
	}

	if events[0].event.WhichObject() != operatorv1.OperatorActionEvent_Object_not_set_case {
		t.Fatalf("attempt object should be unset, got %v", events[0].event.WhichObject())
	}

	if events[1].event.WhichObject() != operatorv1.OperatorActionEvent_Edge_case {
		t.Fatalf("completion object is %v, want Edge_case", events[1].event.WhichObject())
	}
	if events[1].event.GetEdge().GetEdge().GetId() != testCreatedEdgeID {
		t.Fatalf("completion edge ID = %q, want %s", events[1].event.GetEdge().GetEdge().GetId(), testCreatedEdgeID)
	}
}

func TestCreateCaptureSession(t *testing.T) {
	ctx := context.Background()
	h := newTestHarness(t, time.Now())

	createdSessionRef := sessionGlobalRef(testCreatedSessionID)
	h.capHandler.createSessionFunc = func(_ context.Context, _ *connect.Request[capturev1.CreateCaptureSessionRequest]) (*connect.Response[capturev1.CreateCaptureSessionResponse], error) {
		resp := &capturev1.CreateCaptureSessionResponse{}
		resp.SetSession(&modelcapturev1.CaptureSessionRecord{})
		resp.GetSession().SetConfig(&modelcapturev1.CaptureSessionConfig{})
		resp.GetSession().GetConfig().SetRef(createdSessionRef)
		return connect.NewResponse(resp), nil
	}

	t.Run("headers-only writes none", func(t *testing.T) {
		req := &capturev1.CreateCaptureSessionRequest{}
		req.SetEdge(edgeGlobalRef(testEdgeID))
		req.SetAuthorization(&modelcapturev1.CaptureAuthorization{})
		req.GetAuthorization().SetFullPayloadRequested(false)

		_, err := h.captureClient.CreateCaptureSession(ctx, connect.NewRequest(req))
		if err != nil {
			t.Fatalf("CreateCaptureSession headers-only failed: %v", err)
		}
		if len(h.pub.getEvents()) != 0 {
			t.Fatalf("published %d events for headers-only, want 0", len(h.pub.getEvents()))
		}
	})

	t.Run("full-payload writes attempt with edge and completion with session", func(t *testing.T) {
		req := &capturev1.CreateCaptureSessionRequest{}
		req.SetEdge(edgeGlobalRef(testEdgeID))
		req.SetAuthorization(&modelcapturev1.CaptureAuthorization{})
		req.GetAuthorization().SetFullPayloadRequested(true)

		_, err := h.captureClient.CreateCaptureSession(ctx, connect.NewRequest(req))
		if err != nil {
			t.Fatalf("CreateCaptureSession full-payload failed: %v", err)
		}

		events := h.pub.getEvents()
		if len(events) != 2 {
			t.Fatalf("published %d events for full-payload, want 2", len(events))
		}

		wantSubj := "flowseer." + testTenant + ".operator.action.capture_full_payload_create"
		if events[0].subject != wantSubj || events[1].subject != wantSubj {
			t.Errorf("subject mismatch: %q, %q, want %q", events[0].subject, events[1].subject, wantSubj)
		}

		if events[0].event.WhichObject() != operatorv1.OperatorActionEvent_Edge_case {
			t.Fatalf("attempt object = %v, want Edge_case", events[0].event.WhichObject())
		}
		if events[0].event.GetEdge().GetEdge().GetId() != testEdgeID {
			t.Fatalf("attempt edge ID = %q, want %s", events[0].event.GetEdge().GetEdge().GetId(), testEdgeID)
		}

		if events[1].event.WhichObject() != operatorv1.OperatorActionEvent_CaptureSession_case {
			t.Fatalf("completion object = %v, want CaptureSession_case", events[1].event.WhichObject())
		}
		if events[1].event.GetCaptureSession().GetCaptureSession().GetId() != testCreatedSessionID {
			t.Fatalf("completion session ID = %q, want %s", events[1].event.GetCaptureSession().GetCaptureSession().GetId(), testCreatedSessionID)
		}
	})
}

func TestStreamingCaptureProcedures(t *testing.T) {
	ctx := context.Background()

	t.Run("TailCaptureSession attempt published before Send", func(t *testing.T) {
		h := newTestHarness(t, time.Now())
		sessRef := sessionGlobalRef(testTailSessionID)

		h.capHandler.tailSessionFunc = func(_ context.Context, _ *connect.Request[capturev1.TailCaptureSessionRequest], stream *connect.ServerStream[capturev1.TailCaptureSessionResponse]) error {
			events := h.pub.getEvents()
			if len(events) != 1 {
				return connect.NewError(connect.CodeInternal, fmt.Errorf("expected 1 event before first Send, got %d", len(events)))
			}
			if events[0].event.WhichDetail() != operatorv1.OperatorActionEvent_Attempted_case {
				return connect.NewError(connect.CodeInternal, fmt.Errorf("expected Attempted detail, got %v", events[0].event.WhichDetail()))
			}
			resp := &capturev1.TailCaptureSessionResponse{}
			return stream.Send(resp)
		}

		req := &capturev1.TailCaptureSessionRequest{}
		req.SetSession(sessRef)
		stream, err := h.captureClient.TailCaptureSession(ctx, connect.NewRequest(req))
		if err != nil {
			t.Fatalf("TailCaptureSession failed: %v", err)
		}
		for stream.Receive() {
		}
		if err := stream.Err(); err != nil {
			t.Fatalf("stream error: %v", err)
		}

		events := h.pub.getEvents()
		if len(events) != 2 {
			t.Fatalf("published %d events, want 2", len(events))
		}
		if events[0].event.WhichDetail() != operatorv1.OperatorActionEvent_Attempted_case {
			t.Errorf("event 0 detail = %v, want Attempted", events[0].event.WhichDetail())
		}
		if events[1].event.WhichDetail() != operatorv1.OperatorActionEvent_Completed_case {
			t.Errorf("event 1 detail = %v, want Completed", events[1].event.WhichDetail())
		}
		if events[0].event.GetCaptureSession().GetCaptureSession().GetId() != testTailSessionID {
			t.Errorf("attempt session ID = %q, want %s", events[0].event.GetCaptureSession().GetCaptureSession().GetId(), testTailSessionID)
		}
		if events[1].event.GetCaptureSession().GetCaptureSession().GetId() != testTailSessionID {
			t.Errorf("completion session ID = %q, want %s", events[1].event.GetCaptureSession().GetCaptureSession().GetId(), testTailSessionID)
		}
		if events[1].event.GetCompleted().GetOutcome() != operatorv1.OperatorActionOutcome_OPERATOR_ACTION_OUTCOME_SUCCEEDED {
			t.Errorf("completion outcome = %v, want SUCCEEDED", events[1].event.GetCompleted().GetOutcome())
		}
	})

	t.Run("DownloadCaptureSession attempt published before Send", func(t *testing.T) {
		h := newTestHarness(t, time.Now())
		sessRef := sessionGlobalRef(testDownloadSessionID)

		h.capHandler.downloadSessionFunc = func(_ context.Context, _ *connect.Request[capturev1.DownloadCaptureSessionRequest], stream *connect.ServerStream[capturev1.DownloadCaptureSessionResponse]) error {
			events := h.pub.getEvents()
			if len(events) != 1 {
				return connect.NewError(connect.CodeInternal, fmt.Errorf("expected 1 event before first Send, got %d", len(events)))
			}
			resp := &capturev1.DownloadCaptureSessionResponse{}
			return stream.Send(resp)
		}

		req := &capturev1.DownloadCaptureSessionRequest{}
		req.SetSession(sessRef)
		stream, err := h.captureClient.DownloadCaptureSession(ctx, connect.NewRequest(req))
		if err != nil {
			t.Fatalf("DownloadCaptureSession failed: %v", err)
		}
		for stream.Receive() {
		}
		if err := stream.Err(); err != nil {
			t.Fatalf("stream error: %v", err)
		}

		events := h.pub.getEvents()
		if len(events) != 2 {
			t.Fatalf("published %d events, want 2", len(events))
		}
		if events[1].event.GetCompleted().GetOutcome() != operatorv1.OperatorActionOutcome_OPERATOR_ACTION_OUTCOME_SUCCEEDED {
			t.Errorf("completion outcome = %v, want SUCCEEDED", events[1].event.GetCompleted().GetOutcome())
		}
	})

	t.Run("Stream publisher fails attempt", func(t *testing.T) {
		h := newTestHarness(t, time.Now())
		h.pub.failAttempt = true
		sessRef := sessionGlobalRef(testFailedSessionID)

		h.capHandler.tailSessionFunc = func(_ context.Context, _ *connect.Request[capturev1.TailCaptureSessionRequest], _ *connect.ServerStream[capturev1.TailCaptureSessionResponse]) error {
			t.Fatal("handler should not run when attempt publish fails")
			return nil
		}

		req := &capturev1.TailCaptureSessionRequest{}
		req.SetSession(sessRef)
		stream, err := h.captureClient.TailCaptureSession(ctx, connect.NewRequest(req))
		if err == nil {
			for stream.Receive() {
			}
			err = stream.Err()
		}
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatalf("got connect code %v, want CodeUnavailable", connect.CodeOf(err))
		}
		if got := codeOf(t, err); got != actiontrail.ErrCodeUnavailable.String() {
			t.Fatalf("got errs code %q, want %q", got, actiontrail.ErrCodeUnavailable)
		}
	})
}

func TestUnpreparedContext(t *testing.T) {
	ctx := context.Background()

	t.Run("no tenant in context", func(t *testing.T) {
		h := newTestHarness(t, time.Now())
		h.injector.injectTenant = false

		h.edgeHandler.issueSetupKeyFunc = func(_ context.Context, _ *connect.Request[edgev1.IssueSetupKeyRequest]) (*connect.Response[edgev1.IssueSetupKeyResponse], error) {
			t.Fatal("handler should not run when unprepared")
			return nil, nil
		}

		req := &edgev1.IssueSetupKeyRequest{}
		req.SetEdge(edgeGlobalRef(testEdgeID))
		_, err := h.adminClient.IssueSetupKey(ctx, connect.NewRequest(req))
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Fatalf("got connect code %v, want CodeInternal", connect.CodeOf(err))
		}
		if got := codeOf(t, err); got != actiontrail.ErrCodeUnprepared.String() {
			t.Fatalf("got errs code %q, want %q", got, actiontrail.ErrCodeUnprepared)
		}
		if h.edgeHandler.issueSetupKeyCalls != 0 {
			t.Fatalf("handler calls = %d, want 0", h.edgeHandler.issueSetupKeyCalls)
		}
	})

	t.Run("no principal in context", func(t *testing.T) {
		h := newTestHarness(t, time.Now())
		h.injector.injectPrincipal = false

		h.edgeHandler.issueSetupKeyFunc = func(_ context.Context, _ *connect.Request[edgev1.IssueSetupKeyRequest]) (*connect.Response[edgev1.IssueSetupKeyResponse], error) {
			t.Fatal("handler should not run when unprepared")
			return nil, nil
		}

		req := &edgev1.IssueSetupKeyRequest{}
		req.SetEdge(edgeGlobalRef(testEdgeID))
		_, err := h.adminClient.IssueSetupKey(ctx, connect.NewRequest(req))
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Fatalf("got connect code %v, want CodeInternal", connect.CodeOf(err))
		}
		if got := codeOf(t, err); got != actiontrail.ErrCodeUnprepared.String() {
			t.Fatalf("got errs code %q, want %q", got, actiontrail.ErrCodeUnprepared)
		}
		if h.edgeHandler.issueSetupKeyCalls != 0 {
			t.Fatalf("handler calls = %d, want 0", h.edgeHandler.issueSetupKeyCalls)
		}
	})

	t.Run("invalid tenant in context", func(t *testing.T) {
		h := newTestHarness(t, time.Now())
		h.injector.tenantID = "invalid.tenant"

		h.edgeHandler.issueSetupKeyFunc = func(_ context.Context, _ *connect.Request[edgev1.IssueSetupKeyRequest]) (*connect.Response[edgev1.IssueSetupKeyResponse], error) {
			t.Fatal("handler should not run when unprepared")
			return nil, nil
		}

		req := &edgev1.IssueSetupKeyRequest{}
		req.SetEdge(edgeGlobalRef(testEdgeID))
		_, err := h.adminClient.IssueSetupKey(ctx, connect.NewRequest(req))
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Fatalf("got connect code %v, want CodeInternal", connect.CodeOf(err))
		}
		if got := codeOf(t, err); got != actiontrail.ErrCodeUnprepared.String() {
			t.Fatalf("got errs code %q, want %q", got, actiontrail.ErrCodeUnprepared)
		}
		if h.edgeHandler.issueSetupKeyCalls != 0 {
			t.Fatalf("handler calls = %d, want 0", h.edgeHandler.issueSetupKeyCalls)
		}
	})
}

func TestUnpreparedStreamingContext(t *testing.T) {
	for _, tc := range []struct {
		name         string
		injectTenant bool
		tenantID     string
	}{
		{name: "no tenant", injectTenant: false, tenantID: testTenant},
		{name: "invalid tenant", injectTenant: true, tenantID: "invalid.tenant"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHarness(t, time.Now())
			h.injector.injectTenant = tc.injectTenant
			h.injector.tenantID = tc.tenantID
			h.capHandler.tailSessionFunc = func(_ context.Context, _ *connect.Request[capturev1.TailCaptureSessionRequest], _ *connect.ServerStream[capturev1.TailCaptureSessionResponse]) error {
				t.Fatal("handler should not run when unprepared")
				return nil
			}

			req := &capturev1.TailCaptureSessionRequest{}
			req.SetSession(sessionGlobalRef(testUnpreparedSessionID))
			stream, err := h.captureClient.TailCaptureSession(context.Background(), connect.NewRequest(req))
			if err == nil {
				for stream.Receive() {
				}
				err = stream.Err()
			}
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if connect.CodeOf(err) != connect.CodeInternal {
				t.Fatalf("got connect code %v, want CodeInternal", connect.CodeOf(err))
			}
			if got := codeOf(t, err); got != actiontrail.ErrCodeUnprepared.String() {
				t.Fatalf("got errs code %q, want %q", got, actiontrail.ErrCodeUnprepared)
			}
			if h.capHandler.tailSessionCalls != 0 {
				t.Fatalf("handler calls = %d, want 0", h.capHandler.tailSessionCalls)
			}
		})
	}
}

func TestTruncateErrorTypeRuneBoundary(t *testing.T) {
	// A 50-rune string of 3-byte runes is 150 bytes (> 128 bytes).
	// The schema bound is 128 characters, so 50 runes must not be truncated.
	fiftyRunes := strings.Repeat("日", 50)
	if got := actiontrail.TruncateErrorType(fiftyRunes); got != fiftyRunes {
		t.Fatalf("TruncateErrorType(50 runes) = %q (len %d), want full string (len %d)", got, len(got), len(fiftyRunes))
	}

	// A 130-rune string of 3-byte runes must be truncated to 128 runes on a rune boundary.
	manyRunes := strings.Repeat("日", 130)
	got := actiontrail.TruncateErrorType(manyRunes)
	if !utf8.ValidString(got) {
		t.Fatalf("TruncateErrorType produced invalid UTF-8 string: %x", got)
	}
	if runeCount := utf8.RuneCountInString(got); runeCount != 128 {
		t.Fatalf("TruncateErrorType rune count = %d, want 128", runeCount)
	}
}

func TestListEdgesAttemptPublishFailureWithAuthzInterceptor(t *testing.T) {
	ctx := context.Background()
	engine := authztest.New()
	engine.Grant("user:principal-1", "member", "tenant")
	authzInt := authz.NewInterceptor(engine)

	pub := &fakePublisher{failAttempt: true}
	trailInt := actiontrail.NewInterceptor(pub, time.Now, nil)

	const tid = "00000000-0000-0000-0000-000000000001"
	p := authn.Principal{
		ID:      "principal-1",
		Issuer:  "https://issuer.example.com",
		Subject: "operator-42",
		Tenants: []string{tid},
	}
	injector := &contextInjector{
		principal:       p,
		tenantID:        tid,
		injectPrincipal: true,
		injectTenant:    true,
	}

	handler := &fakeEdgeAdminHandler{}
	mux := http.NewServeMux()
	path, h := edgev1connect.NewEdgeAdminServiceHandler(handler, connect.WithInterceptors(injector, authzInt, trailInt))
	mux.Handle(path, h)

	server := httptest.NewServer(mux)
	defer server.Close()

	client := edgev1connect.NewEdgeAdminServiceClient(http.DefaultClient, server.URL)

	req := connect.NewRequest(&edgev1.ListEdgesRequest{})
	req.Header().Set("X-FlowSeer-Tenant", tid)

	_, err := client.ListEdges(ctx, req)
	if err == nil {
		t.Fatal("ListEdges succeeded, want error")
	}
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("got connect code %v, want CodeUnavailable", connect.CodeOf(err))
	}
	if got := codeOf(t, err); got != actiontrail.ErrCodeUnavailable.String() {
		t.Fatalf("got errs code %q, want %q", got, actiontrail.ErrCodeUnavailable)
	}
}

// objectSpec is the object an event is expected to carry. An edge or session
// spec names the ref's id, and any other kind brings its own check.
type objectSpec struct {
	isEdge    bool
	isSession bool
	id        string
	check     func(t *testing.T, stage string, ev *operatorv1.OperatorActionEvent)
}

// The member and role the table's rows and the completion cases name.
const (
	testMemberIssuer  = "https://issuer.example.com"
	testMemberSubject = "member-7"
	testRoleID        = "11111111-1111-4111-8111-111111111111"
)

func memberSpec() objectSpec {
	return objectSpec{check: func(t *testing.T, stage string, ev *operatorv1.OperatorActionEvent) {
		t.Helper()
		if ev.WhichObject() != operatorv1.OperatorActionEvent_Member_case {
			t.Fatalf("%s: which object = %v, want Member_case", stage, ev.WhichObject())
		}
		if want := testMemberRef(); !proto.Equal(ev.GetMember(), want) {
			t.Fatalf("%s: member = %v, want %v", stage, ev.GetMember(), want)
		}
	}}
}

func tenantSpec(id string) objectSpec {
	return objectSpec{check: func(t *testing.T, stage string, ev *operatorv1.OperatorActionEvent) {
		t.Helper()
		if ev.WhichObject() != operatorv1.OperatorActionEvent_Tenant_case {
			t.Fatalf("%s: which object = %v, want Tenant_case", stage, ev.WhichObject())
		}
		if want := tenantGlobalRef(id); !proto.Equal(ev.GetTenant(), want) {
			t.Fatalf("%s: tenant = %v, want %v", stage, ev.GetTenant(), want)
		}
	}}
}

func roleSpec(id string, relations ...identityv1.TenantRelation) objectSpec {
	return objectSpec{check: func(t *testing.T, stage string, ev *operatorv1.OperatorActionEvent) {
		t.Helper()
		if ev.WhichObject() != operatorv1.OperatorActionEvent_Role_case {
			t.Fatalf("%s: which object = %v, want Role_case", stage, ev.WhichObject())
		}
		want := &operatorv1.OperatorActionRole{}
		want.SetRole(roleGlobalRef(id))
		want.SetRelations(relations)
		if !proto.Equal(ev.GetRole(), want) {
			t.Fatalf("%s: role = %v, want %v", stage, ev.GetRole(), want)
		}
	}}
}

func roleAssignmentSpec() objectSpec {
	return objectSpec{check: func(t *testing.T, stage string, ev *operatorv1.OperatorActionEvent) {
		t.Helper()
		if ev.WhichObject() != operatorv1.OperatorActionEvent_RoleAssignment_case {
			t.Fatalf("%s: which object = %v, want RoleAssignment_case", stage, ev.WhichObject())
		}
		want := &operatorv1.OperatorActionRoleAssignment{}
		want.SetRole(roleGlobalRef(testRoleID))
		want.SetMember(testMemberRef())
		if !proto.Equal(ev.GetRoleAssignment(), want) {
			t.Fatalf("%s: role assignment = %v, want %v", stage, ev.GetRoleAssignment(), want)
		}
	}}
}

func partnerSpec(providerID string, relations ...identityv1.TenantRelation) objectSpec {
	return objectSpec{check: func(t *testing.T, stage string, ev *operatorv1.OperatorActionEvent) {
		t.Helper()
		if ev.WhichObject() != operatorv1.OperatorActionEvent_Partner_case {
			t.Fatalf("%s: which object = %v, want Partner_case", stage, ev.WhichObject())
		}
		want := &operatorv1.OperatorActionPartner{}
		want.SetTenant(tenantGlobalRef(providerID))
		want.SetRelations(relations)
		if !proto.Equal(ev.GetPartner(), want) {
			t.Fatalf("%s: partner = %v, want %v", stage, ev.GetPartner(), want)
		}
	}}
}

func fullPayloadGrantSpec(expiresAt time.Time) objectSpec {
	return objectSpec{check: func(t *testing.T, stage string, ev *operatorv1.OperatorActionEvent) {
		t.Helper()
		if ev.WhichObject() != operatorv1.OperatorActionEvent_FullPayloadGrant_case {
			t.Fatalf("%s: which object = %v, want FullPayloadGrant_case", stage, ev.WhichObject())
		}
		want := &operatorv1.OperatorActionFullPayloadGrant{}
		want.SetMember(testMemberRef())
		want.SetExpiresAt(timestamppb.New(expiresAt))
		if !proto.Equal(ev.GetFullPayloadGrant(), want) {
			t.Fatalf("%s: full payload grant = %v, want %v", stage, ev.GetFullPayloadGrant(), want)
		}
	}}
}

func TestOperatorActionTrailTable(t *testing.T) {
	ctx := context.Background()

	const (
		providerID    = "22222222-2222-4222-8222-222222222222"
		createdTenant = "33333333-3333-4333-8333-333333333333"
	)
	member := testMemberRef()
	grantExpiry := time.Date(2026, 10, 4, 13, 0, 0, 0, time.UTC)
	relations := []identityv1.TenantRelation{
		identityv1.TenantRelation_TENANT_RELATION_OPERATOR,
		identityv1.TenantRelation_TENANT_RELATION_VIEWER,
	}

	tests := []struct {
		name         string
		tenantless   bool
		setupHandler func(h *testHarness)
		invoke       func(ctx context.Context, h *testHarness) error
		wantSubject  string
		wantAction   operatorv1.OperatorAction
		wantAttempt  objectSpec
		wantComplete objectSpec
	}{
		{
			name: "EdgeAdminService.CreateEdge",
			setupHandler: func(h *testHarness) {
				h.edgeHandler.createEdgeFunc = func(_ context.Context, _ *connect.Request[edgev1.CreateEdgeRequest]) (*connect.Response[edgev1.CreateEdgeResponse], error) {
					resp := &edgev1.CreateEdgeResponse{}
					resp.SetEdge(&modeledgev1.EdgeRecord{})
					resp.GetEdge().SetConfig(&modeledgev1.EdgeConfig{})
					resp.GetEdge().GetConfig().SetRef(edgeGlobalRef(testCreatedEdgeID))
					return connect.NewResponse(resp), nil
				}
			},
			invoke: func(ctx context.Context, h *testHarness) error {
				_, err := h.adminClient.CreateEdge(ctx, connect.NewRequest(&edgev1.CreateEdgeRequest{}))
				return err
			},
			wantSubject:  "flowseer." + testTenant + ".operator.action.edge_create",
			wantAction:   operatorv1.OperatorAction_OPERATOR_ACTION_EDGE_CREATE,
			wantAttempt:  objectSpec{},
			wantComplete: objectSpec{isEdge: true, id: testCreatedEdgeID},
		},
		{
			name: "EdgeAdminService.IssueSetupKey",
			setupHandler: func(h *testHarness) {
				h.edgeHandler.issueSetupKeyFunc = func(_ context.Context, _ *connect.Request[edgev1.IssueSetupKeyRequest]) (*connect.Response[edgev1.IssueSetupKeyResponse], error) {
					return connect.NewResponse(&edgev1.IssueSetupKeyResponse{}), nil
				}
			},
			invoke: func(ctx context.Context, h *testHarness) error {
				req := &edgev1.IssueSetupKeyRequest{}
				req.SetEdge(edgeGlobalRef(testEdgeID))
				_, err := h.adminClient.IssueSetupKey(ctx, connect.NewRequest(req))
				return err
			},
			wantSubject:  "flowseer." + testTenant + ".operator.action.setup_key_issue",
			wantAction:   operatorv1.OperatorAction_OPERATOR_ACTION_SETUP_KEY_ISSUE,
			wantAttempt:  objectSpec{isEdge: true, id: testEdgeID},
			wantComplete: objectSpec{isEdge: true, id: testEdgeID},
		},
		{
			name: "EdgeAdminService.RevokeSetupKey",
			setupHandler: func(h *testHarness) {
				h.edgeHandler.revokeSetupKeyFunc = func(_ context.Context, _ *connect.Request[edgev1.RevokeSetupKeyRequest]) (*connect.Response[edgev1.RevokeSetupKeyResponse], error) {
					return connect.NewResponse(&edgev1.RevokeSetupKeyResponse{}), nil
				}
			},
			invoke: func(ctx context.Context, h *testHarness) error {
				req := &edgev1.RevokeSetupKeyRequest{}
				req.SetEdge(edgeGlobalRef(testEdgeID))
				_, err := h.adminClient.RevokeSetupKey(ctx, connect.NewRequest(req))
				return err
			},
			wantSubject:  "flowseer." + testTenant + ".operator.action.setup_key_revoke",
			wantAction:   operatorv1.OperatorAction_OPERATOR_ACTION_SETUP_KEY_REVOKE,
			wantAttempt:  objectSpec{isEdge: true, id: testEdgeID},
			wantComplete: objectSpec{isEdge: true, id: testEdgeID},
		},
		{
			name: "EdgeAdminService.RetireEdge",
			setupHandler: func(h *testHarness) {
				h.edgeHandler.retireEdgeFunc = func(_ context.Context, _ *connect.Request[edgev1.RetireEdgeRequest]) (*connect.Response[edgev1.RetireEdgeResponse], error) {
					return connect.NewResponse(&edgev1.RetireEdgeResponse{}), nil
				}
			},
			invoke: func(ctx context.Context, h *testHarness) error {
				req := &edgev1.RetireEdgeRequest{}
				req.SetEdge(edgeGlobalRef(testEdgeID))
				_, err := h.adminClient.RetireEdge(ctx, connect.NewRequest(req))
				return err
			},
			wantSubject:  "flowseer." + testTenant + ".operator.action.edge_retire",
			wantAction:   operatorv1.OperatorAction_OPERATOR_ACTION_EDGE_RETIRE,
			wantAttempt:  objectSpec{isEdge: true, id: testEdgeID},
			wantComplete: objectSpec{isEdge: true, id: testEdgeID},
		},
		{
			name: "EdgeAdminService.GetEdge",
			setupHandler: func(h *testHarness) {
				h.edgeHandler.getEdgeFunc = func(_ context.Context, _ *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error) {
					return connect.NewResponse(&edgev1.GetEdgeResponse{}), nil
				}
			},
			invoke: func(ctx context.Context, h *testHarness) error {
				req := &edgev1.GetEdgeRequest{}
				req.SetEdge(edgeGlobalRef(testEdgeID))
				_, err := h.adminClient.GetEdge(ctx, connect.NewRequest(req))
				return err
			},
			wantSubject:  "flowseer." + testTenant + ".operator.read.edge_get",
			wantAction:   operatorv1.OperatorAction_OPERATOR_ACTION_EDGE_GET,
			wantAttempt:  objectSpec{isEdge: true, id: testEdgeID},
			wantComplete: objectSpec{isEdge: true, id: testEdgeID},
		},
		{
			name: "EdgeAdminService.ListEdges",
			setupHandler: func(h *testHarness) {
				h.edgeHandler.listEdgesFunc = func(_ context.Context, _ *connect.Request[edgev1.ListEdgesRequest]) (*connect.Response[edgev1.ListEdgesResponse], error) {
					return connect.NewResponse(&edgev1.ListEdgesResponse{}), nil
				}
			},
			invoke: func(ctx context.Context, h *testHarness) error {
				_, err := h.adminClient.ListEdges(ctx, connect.NewRequest(&edgev1.ListEdgesRequest{}))
				return err
			},
			wantSubject:  "flowseer." + testTenant + ".operator.read.edge_list",
			wantAction:   operatorv1.OperatorAction_OPERATOR_ACTION_EDGE_LIST,
			wantAttempt:  objectSpec{},
			wantComplete: objectSpec{},
		},
		{
			name: "CaptureService.CreateCaptureSession_FullPayload",
			setupHandler: func(h *testHarness) {
				h.capHandler.createSessionFunc = func(_ context.Context, _ *connect.Request[capturev1.CreateCaptureSessionRequest]) (*connect.Response[capturev1.CreateCaptureSessionResponse], error) {
					resp := &capturev1.CreateCaptureSessionResponse{}
					resp.SetSession(&modelcapturev1.CaptureSessionRecord{})
					resp.GetSession().SetConfig(&modelcapturev1.CaptureSessionConfig{})
					resp.GetSession().GetConfig().SetRef(sessionGlobalRef(testCreatedSessionID))
					return connect.NewResponse(resp), nil
				}
			},
			invoke: func(ctx context.Context, h *testHarness) error {
				req := &capturev1.CreateCaptureSessionRequest{}
				req.SetEdge(edgeGlobalRef(testEdgeID))
				req.SetAuthorization(&modelcapturev1.CaptureAuthorization{})
				req.GetAuthorization().SetFullPayloadRequested(true)
				_, err := h.captureClient.CreateCaptureSession(ctx, connect.NewRequest(req))
				return err
			},
			wantSubject:  "flowseer." + testTenant + ".operator.action.capture_full_payload_create",
			wantAction:   operatorv1.OperatorAction_OPERATOR_ACTION_CAPTURE_FULL_PAYLOAD_CREATE,
			wantAttempt:  objectSpec{isEdge: true, id: testEdgeID},
			wantComplete: objectSpec{isSession: true, id: testCreatedSessionID},
		},
		{
			name: "CaptureService.TailCaptureSession",
			setupHandler: func(h *testHarness) {
				h.capHandler.tailSessionFunc = func(_ context.Context, _ *connect.Request[capturev1.TailCaptureSessionRequest], stream *connect.ServerStream[capturev1.TailCaptureSessionResponse]) error {
					return stream.Send(&capturev1.TailCaptureSessionResponse{})
				}
			},
			invoke: func(ctx context.Context, h *testHarness) error {
				req := &capturev1.TailCaptureSessionRequest{}
				req.SetSession(sessionGlobalRef(testTailSessionID))
				stream, err := h.captureClient.TailCaptureSession(ctx, connect.NewRequest(req))
				if err != nil {
					return err
				}
				for stream.Receive() {
				}
				return stream.Err()
			},
			wantSubject:  "flowseer." + testTenant + ".operator.action.capture_tail",
			wantAction:   operatorv1.OperatorAction_OPERATOR_ACTION_CAPTURE_TAIL,
			wantAttempt:  objectSpec{isSession: true, id: testTailSessionID},
			wantComplete: objectSpec{isSession: true, id: testTailSessionID},
		},
		{
			name: "CaptureService.DownloadCaptureSession",
			setupHandler: func(h *testHarness) {
				h.capHandler.downloadSessionFunc = func(_ context.Context, _ *connect.Request[capturev1.DownloadCaptureSessionRequest], stream *connect.ServerStream[capturev1.DownloadCaptureSessionResponse]) error {
					return stream.Send(&capturev1.DownloadCaptureSessionResponse{})
				}
			},
			invoke: func(ctx context.Context, h *testHarness) error {
				req := &capturev1.DownloadCaptureSessionRequest{}
				req.SetSession(sessionGlobalRef(testDownloadSessionID))
				stream, err := h.captureClient.DownloadCaptureSession(ctx, connect.NewRequest(req))
				if err != nil {
					return err
				}
				for stream.Receive() {
				}
				return stream.Err()
			},
			wantSubject:  "flowseer." + testTenant + ".operator.action.capture_download",
			wantAction:   operatorv1.OperatorAction_OPERATOR_ACTION_CAPTURE_DOWNLOAD,
			wantAttempt:  objectSpec{isSession: true, id: testDownloadSessionID},
			wantComplete: objectSpec{isSession: true, id: testDownloadSessionID},
		},
		{
			name:       "TenantService.CreateTenant",
			tenantless: true,
			setupHandler: func(h *testHarness) {
				cfg := &identityv1.TenantConfig{}
				cfg.SetRef(tenantGlobalRef(createdTenant))
				record := &identityv1.TenantRecord{}
				record.SetConfig(cfg)
				resp := &identityapiv1.CreateTenantResponse{}
				resp.SetTenant(record)
				h.tenantHandler.createTenant = resp
			},
			invoke: func(ctx context.Context, h *testHarness) error {
				_, err := h.tenantClient.CreateTenant(ctx, connect.NewRequest(&identityapiv1.CreateTenantRequest{}))
				return err
			},
			wantSubject:  "flowseer.platform.operator.action.tenant_create",
			wantAction:   operatorv1.OperatorAction_OPERATOR_ACTION_TENANT_CREATE,
			wantAttempt:  objectSpec{},
			wantComplete: tenantSpec(createdTenant),
		},
		{
			name: "TenantAdminService.EnrollMember",
			setupHandler: func(h *testHarness) {
				h.idAdminHandler.enrollMember = &identityapiv1.EnrollMemberResponse{}
			},
			invoke: func(ctx context.Context, h *testHarness) error {
				req := &identityapiv1.EnrollMemberRequest{}
				req.SetMember(member)
				_, err := h.idAdminClient.EnrollMember(ctx, connect.NewRequest(req))
				return err
			},
			wantSubject:  "flowseer." + testTenant + ".operator.action.member_enroll",
			wantAction:   operatorv1.OperatorAction_OPERATOR_ACTION_MEMBER_ENROLL,
			wantAttempt:  memberSpec(),
			wantComplete: memberSpec(),
		},
		{
			name: "TenantAdminService.RemoveMember",
			setupHandler: func(h *testHarness) {
				h.idAdminHandler.removeMember = &identityapiv1.RemoveMemberResponse{}
			},
			invoke: func(ctx context.Context, h *testHarness) error {
				req := &identityapiv1.RemoveMemberRequest{}
				req.SetMember(member)
				_, err := h.idAdminClient.RemoveMember(ctx, connect.NewRequest(req))
				return err
			},
			wantSubject:  "flowseer." + testTenant + ".operator.action.member_remove",
			wantAction:   operatorv1.OperatorAction_OPERATOR_ACTION_MEMBER_REMOVE,
			wantAttempt:  memberSpec(),
			wantComplete: memberSpec(),
		},
		{
			name: "TenantAdminService.CreateRole",
			setupHandler: func(h *testHarness) {
				role := &identityv1.Role{}
				role.SetRef(roleGlobalRef(testRoleID))
				role.SetRelations(relations)
				resp := &identityapiv1.CreateRoleResponse{}
				resp.SetRole(role)
				h.idAdminHandler.createRole = resp
			},
			invoke: func(ctx context.Context, h *testHarness) error {
				req := &identityapiv1.CreateRoleRequest{}
				req.SetRelations(relations)
				_, err := h.idAdminClient.CreateRole(ctx, connect.NewRequest(req))
				return err
			},
			wantSubject:  "flowseer." + testTenant + ".operator.action.role_create",
			wantAction:   operatorv1.OperatorAction_OPERATOR_ACTION_ROLE_CREATE,
			wantAttempt:  objectSpec{},
			wantComplete: roleSpec(testRoleID, relations...),
		},
		{
			name: "TenantAdminService.DeleteRole",
			setupHandler: func(h *testHarness) {
				h.idAdminHandler.deleteRole = &identityapiv1.DeleteRoleResponse{}
			},
			invoke: func(ctx context.Context, h *testHarness) error {
				req := &identityapiv1.DeleteRoleRequest{}
				req.SetRole(roleGlobalRef(testRoleID))
				_, err := h.idAdminClient.DeleteRole(ctx, connect.NewRequest(req))
				return err
			},
			wantSubject:  "flowseer." + testTenant + ".operator.action.role_delete",
			wantAction:   operatorv1.OperatorAction_OPERATOR_ACTION_ROLE_DELETE,
			wantAttempt:  roleSpec(testRoleID),
			wantComplete: roleSpec(testRoleID),
		},
		{
			name: "TenantAdminService.AssignRole",
			setupHandler: func(h *testHarness) {
				h.idAdminHandler.assignRole = &identityapiv1.AssignRoleResponse{}
			},
			invoke: func(ctx context.Context, h *testHarness) error {
				req := &identityapiv1.AssignRoleRequest{}
				req.SetMember(member)
				req.SetRole(roleGlobalRef(testRoleID))
				_, err := h.idAdminClient.AssignRole(ctx, connect.NewRequest(req))
				return err
			},
			wantSubject:  "flowseer." + testTenant + ".operator.action.role_assign",
			wantAction:   operatorv1.OperatorAction_OPERATOR_ACTION_ROLE_ASSIGN,
			wantAttempt:  roleAssignmentSpec(),
			wantComplete: roleAssignmentSpec(),
		},
		{
			name: "TenantAdminService.UnassignRole",
			setupHandler: func(h *testHarness) {
				h.idAdminHandler.unassignRole = &identityapiv1.UnassignRoleResponse{}
			},
			invoke: func(ctx context.Context, h *testHarness) error {
				req := &identityapiv1.UnassignRoleRequest{}
				req.SetMember(member)
				req.SetRole(roleGlobalRef(testRoleID))
				_, err := h.idAdminClient.UnassignRole(ctx, connect.NewRequest(req))
				return err
			},
			wantSubject:  "flowseer." + testTenant + ".operator.action.role_unassign",
			wantAction:   operatorv1.OperatorAction_OPERATOR_ACTION_ROLE_UNASSIGN,
			wantAttempt:  roleAssignmentSpec(),
			wantComplete: roleAssignmentSpec(),
		},
		{
			name: "TenantAdminService.ConnectPartner",
			setupHandler: func(h *testHarness) {
				h.idAdminHandler.connectPartner = &identityapiv1.ConnectPartnerResponse{}
			},
			invoke: func(ctx context.Context, h *testHarness) error {
				req := &identityapiv1.ConnectPartnerRequest{}
				req.SetPartner(tenantGlobalRef(providerID))
				req.SetRelations(relations)
				_, err := h.idAdminClient.ConnectPartner(ctx, connect.NewRequest(req))
				return err
			},
			wantSubject:  "flowseer." + testTenant + ".operator.action.partner_connect",
			wantAction:   operatorv1.OperatorAction_OPERATOR_ACTION_PARTNER_CONNECT,
			wantAttempt:  partnerSpec(providerID, relations...),
			wantComplete: partnerSpec(providerID, relations...),
		},
		{
			name: "TenantAdminService.DisconnectPartner",
			setupHandler: func(h *testHarness) {
				h.idAdminHandler.disconnectPartner = &identityapiv1.DisconnectPartnerResponse{}
			},
			invoke: func(ctx context.Context, h *testHarness) error {
				req := &identityapiv1.DisconnectPartnerRequest{}
				req.SetPartner(tenantGlobalRef(providerID))
				_, err := h.idAdminClient.DisconnectPartner(ctx, connect.NewRequest(req))
				return err
			},
			wantSubject:  "flowseer." + testTenant + ".operator.action.partner_disconnect",
			wantAction:   operatorv1.OperatorAction_OPERATOR_ACTION_PARTNER_DISCONNECT,
			wantAttempt:  tenantSpec(providerID),
			wantComplete: tenantSpec(providerID),
		},
		{
			name: "TenantAdminService.GrantFullPayload",
			setupHandler: func(h *testHarness) {
				grant := &identityv1.FullPayloadGrant{}
				grant.SetExpiresAt(timestamppb.New(grantExpiry))
				stored := &identityv1.Member{}
				stored.SetOperator(member)
				stored.SetFullPayload(grant)
				resp := &identityapiv1.GrantFullPayloadResponse{}
				resp.SetMember(stored)
				h.idAdminHandler.grantFullPayload = resp
			},
			invoke: func(ctx context.Context, h *testHarness) error {
				req := &identityapiv1.GrantFullPayloadRequest{}
				req.SetMember(member)
				_, err := h.idAdminClient.GrantFullPayload(ctx, connect.NewRequest(req))
				return err
			},
			wantSubject:  "flowseer." + testTenant + ".operator.action.full_payload_grant",
			wantAction:   operatorv1.OperatorAction_OPERATOR_ACTION_FULL_PAYLOAD_GRANT,
			wantAttempt:  memberSpec(),
			wantComplete: fullPayloadGrantSpec(grantExpiry),
		},
		{
			name: "TenantAdminService.RevokeFullPayload",
			setupHandler: func(h *testHarness) {
				h.idAdminHandler.revokeFullPayload = &identityapiv1.RevokeFullPayloadResponse{}
			},
			invoke: func(ctx context.Context, h *testHarness) error {
				req := &identityapiv1.RevokeFullPayloadRequest{}
				req.SetMember(member)
				_, err := h.idAdminClient.RevokeFullPayload(ctx, connect.NewRequest(req))
				return err
			},
			wantSubject:  "flowseer." + testTenant + ".operator.action.full_payload_revoke",
			wantAction:   operatorv1.OperatorAction_OPERATOR_ACTION_FULL_PAYLOAD_REVOKE,
			wantAttempt:  memberSpec(),
			wantComplete: memberSpec(),
		},
	}

	assertObject := func(t *testing.T, stage string, ev *operatorv1.OperatorActionEvent, want objectSpec) {
		t.Helper()
		switch {
		case want.check != nil:
			want.check(t, stage, ev)
		case want.isEdge:
			if ev.WhichObject() != operatorv1.OperatorActionEvent_Edge_case {
				t.Fatalf("%s: which object = %v, want Edge_case", stage, ev.WhichObject())
			}
			if got := ev.GetEdge().GetEdge().GetId(); got != want.id {
				t.Fatalf("%s: edge ID = %q, want %q", stage, got, want.id)
			}
		case want.isSession:
			if ev.WhichObject() != operatorv1.OperatorActionEvent_CaptureSession_case {
				t.Fatalf("%s: which object = %v, want CaptureSession_case", stage, ev.WhichObject())
			}
			if got := ev.GetCaptureSession().GetCaptureSession().GetId(); got != want.id {
				t.Fatalf("%s: session ID = %q, want %q", stage, got, want.id)
			}
		default:
			if ev.WhichObject() != 0 {
				t.Fatalf("%s: which object = %v, want 0 (none)", stage, ev.WhichObject())
			}
		}
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHarness(t, time.Now())
			h.injector.injectTenant = !tc.tenantless
			if tc.setupHandler != nil {
				tc.setupHandler(h)
			}

			if err := tc.invoke(ctx, h); err != nil {
				t.Fatalf("invoke failed: %v", err)
			}

			events := h.pub.getEvents()
			if len(events) != 2 {
				t.Fatalf("published %d events, want 2", len(events))
			}

			attempt := events[0]
			complete := events[1]

			if attempt.subject != tc.wantSubject {
				t.Errorf("attempt subject = %q, want %q", attempt.subject, tc.wantSubject)
			}
			if complete.subject != tc.wantSubject {
				t.Errorf("completion subject = %q, want %q", complete.subject, tc.wantSubject)
			}

			if attempt.event.GetAction() != tc.wantAction {
				t.Errorf("attempt action = %v, want %v", attempt.event.GetAction(), tc.wantAction)
			}
			if complete.event.GetAction() != tc.wantAction {
				t.Errorf("completion action = %v, want %v", complete.event.GetAction(), tc.wantAction)
			}

			assertObject(t, "attempt", attempt.event, tc.wantAttempt)
			assertObject(t, "completion", complete.event, tc.wantComplete)

			if attempt.event.WhichDetail() != operatorv1.OperatorActionEvent_Attempted_case {
				t.Errorf("attempt detail = %v, want Attempted_case", attempt.event.WhichDetail())
			}
			if complete.event.WhichDetail() != operatorv1.OperatorActionEvent_Completed_case {
				t.Errorf("completion detail = %v, want Completed_case", complete.event.WhichDetail())
			}
			if complete.event.GetCompleted().GetOutcome() != operatorv1.OperatorActionOutcome_OPERATOR_ACTION_OUTCOME_SUCCEEDED {
				t.Errorf("outcome = %v, want SUCCEEDED", complete.event.GetCompleted().GetOutcome())
			}
		})
	}
}

func TestCompletionPublishedWhenCallerContextCancelled(t *testing.T) {
	h := newTestHarness(t, time.Now())

	ctx, cancel := context.WithCancel(context.Background())
	h.edgeHandler.issueSetupKeyFunc = func(handlerCtx context.Context, _ *connect.Request[edgev1.IssueSetupKeyRequest]) (*connect.Response[edgev1.IssueSetupKeyResponse], error) {
		cancel()
		<-handlerCtx.Done()
		resp := &edgev1.IssueSetupKeyResponse{}
		prov := &modeledgev1.EdgeProvisioning{}
		prov.SetSetupKey("setup-key-ok")
		resp.SetProvisioning(prov)
		return connect.NewResponse(resp), nil
	}

	req := &edgev1.IssueSetupKeyRequest{}
	req.SetEdge(edgeGlobalRef(testEdgeID))
	_, _ = h.adminClient.IssueSetupKey(ctx, connect.NewRequest(req))

	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	var events []recordedEvent
waitForCompletion:
	for {
		events = h.pub.getEvents()
		if len(events) == 2 {
			break
		}
		select {
		case <-deadline.C:
			break waitForCompletion
		case <-ticker.C:
		}
	}
	if len(events) != 2 {
		t.Fatalf("published %d events, want 2 (completion published via detached context)", len(events))
	}
	if events[1].event.WhichDetail() != operatorv1.OperatorActionEvent_Completed_case {
		t.Errorf("completion detail = %v, want Completed", events[1].event.WhichDetail())
	}
}

// A procedure of either identity service is recorded, or is one of the five
// reads the trail leaves out, so a new RPC cannot ship unrecorded by default.
func TestEveryIdentityProcedureIsRecordedOrListedUnrecorded(t *testing.T) {
	const (
		wantRecorded   = 11
		wantUnrecorded = 5
	)
	unrecorded := map[string]bool{
		"/flowseer.api.identity.v1.TenantService/GetTenant":         true,
		"/flowseer.api.identity.v1.TenantService/ListTenants":       true,
		"/flowseer.api.identity.v1.TenantAdminService/ListMembers":  true,
		"/flowseer.api.identity.v1.TenantAdminService/ListRoles":    true,
		"/flowseer.api.identity.v1.TenantAdminService/ListPartners": true,
	}
	if len(unrecorded) != wantUnrecorded {
		t.Fatalf("the unrecorded list holds %d procedures, want %d", len(unrecorded), wantUnrecorded)
	}

	services := []protoreflect.ServiceDescriptor{
		identityapiv1.File_flowseer_api_identity_v1_tenant_service_proto.Services().ByName("TenantService"),
		identityapiv1.File_flowseer_api_identity_v1_tenant_admin_service_proto.Services().ByName("TenantAdminService"),
	}
	var recorded, listed int
	for _, svc := range services {
		methods := svc.Methods()
		for i := range methods.Len() {
			proc := fmt.Sprintf("/%s/%s", svc.FullName(), methods.Get(i).Name())
			h := newTestHarness(t, time.Now())
			client := connect.NewClient[emptypb.Empty, emptypb.Empty](h.server.Client(), h.server.URL+proc)
			_, _ = client.CallUnary(context.Background(), connect.NewRequest(&emptypb.Empty{}))
			events := h.pub.getEvents()
			if unrecorded[proc] {
				listed++
				if len(events) != 0 {
					t.Errorf("procedure %q is listed unrecorded and wrote %d events", proc, len(events))
				}
				continue
			}
			recorded++
			if len(events) != 2 {
				t.Errorf("procedure %q wrote %d events, want an attempt and a completion", proc, len(events))
			}
		}
	}
	if recorded != wantRecorded {
		t.Errorf("recorded %d identity procedures, want %d", recorded, wantRecorded)
	}
	if listed != wantUnrecorded {
		t.Errorf("found %d of the %d unrecorded procedures in the descriptors", listed, wantUnrecorded)
	}
}

func TestCreateTenantWithTheTrailUnavailableDoesNotRunTheHandler(t *testing.T) {
	h := newTestHarness(t, time.Now())
	h.injector.injectTenant = false
	h.pub.failAttempt = true
	h.tenantHandler.createTenant = &identityapiv1.CreateTenantResponse{}

	_, err := h.tenantClient.CreateTenant(context.Background(), connect.NewRequest(&identityapiv1.CreateTenantRequest{}))
	if err == nil {
		t.Fatal("CreateTenant succeeded, want error")
	}
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("got connect code %v, want CodeUnavailable", connect.CodeOf(err))
	}
	if got := codeOf(t, err); got != actiontrail.ErrCodeUnavailable.String() {
		t.Fatalf("got errs code %q, want %q", got, actiontrail.ErrCodeUnavailable)
	}
	if h.tenantHandler.createTenantCalls != 0 {
		t.Fatalf("handler calls = %d, want 0", h.tenantHandler.createTenantCalls)
	}
}

func TestOnlyCreateTenantRecordsWithoutAnAdmittedTenant(t *testing.T) {
	ctx := context.Background()

	t.Run("another recorded procedure answers unprepared", func(t *testing.T) {
		h := newTestHarness(t, time.Now())
		h.injector.injectTenant = false
		h.idAdminHandler.enrollMember = &identityapiv1.EnrollMemberResponse{}

		req := &identityapiv1.EnrollMemberRequest{}
		req.SetMember(testMemberRef())
		_, err := h.idAdminClient.EnrollMember(ctx, connect.NewRequest(req))
		if err == nil {
			t.Fatal("EnrollMember without a tenant succeeded, want error")
		}
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Fatalf("got connect code %v, want CodeInternal", connect.CodeOf(err))
		}
		if got := codeOf(t, err); got != actiontrail.ErrCodeUnprepared.String() {
			t.Fatalf("got errs code %q, want %q", got, actiontrail.ErrCodeUnprepared)
		}
		if events := h.pub.getEvents(); len(events) != 0 {
			t.Fatalf("published %d events, want 0", len(events))
		}
	})

	t.Run("an invalid tenant does not fall back to the platform token", func(t *testing.T) {
		h := newTestHarness(t, time.Now())
		h.injector.tenantID = "invalid.tenant"
		h.tenantHandler.createTenant = &identityapiv1.CreateTenantResponse{}

		_, err := h.tenantClient.CreateTenant(ctx, connect.NewRequest(&identityapiv1.CreateTenantRequest{}))
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Fatalf("got connect code %v, want CodeInternal (err %v)", connect.CodeOf(err), err)
		}
		if got := codeOf(t, err); got != actiontrail.ErrCodeUnprepared.String() {
			t.Fatalf("got errs code %q, want %q", got, actiontrail.ErrCodeUnprepared)
		}
		if h.tenantHandler.createTenantCalls != 0 {
			t.Fatalf("handler calls = %d, want 0", h.tenantHandler.createTenantCalls)
		}
	})

	t.Run("a platform call without a principal answers unprepared", func(t *testing.T) {
		h := newTestHarness(t, time.Now())
		h.injector.injectTenant = false
		h.injector.injectPrincipal = false
		h.tenantHandler.createTenant = &identityapiv1.CreateTenantResponse{}

		_, err := h.tenantClient.CreateTenant(ctx, connect.NewRequest(&identityapiv1.CreateTenantRequest{}))
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Fatalf("got connect code %v, want CodeInternal (err %v)", connect.CodeOf(err), err)
		}
		if got := codeOf(t, err); got != actiontrail.ErrCodeUnprepared.String() {
			t.Fatalf("got errs code %q, want %q", got, actiontrail.ErrCodeUnprepared)
		}
		if h.tenantHandler.createTenantCalls != 0 {
			t.Fatalf("handler calls = %d, want 0", h.tenantHandler.createTenantCalls)
		}
	})
}

// A completion names the object the response carries. When the handler fails,
// or answers without it, the completion repeats the attempt's object, which
// is none for the two calls that mint their object.
func TestIdentityCompletionWithoutAResponseObject(t *testing.T) {
	member := testMemberRef()
	failure := connect.NewError(connect.CodeFailedPrecondition, errors.New("refused"))

	tests := []struct {
		name         string
		tenantless   bool
		setupHandler func(h *testHarness)
		invoke       func(ctx context.Context, h *testHarness) error
		wantComplete objectSpec
		wantOutcome  operatorv1.OperatorActionOutcome
	}{
		{
			name: "GrantFullPayload fails",
			setupHandler: func(h *testHarness) {
				h.idAdminHandler.err = failure
			},
			invoke: func(ctx context.Context, h *testHarness) error {
				req := &identityapiv1.GrantFullPayloadRequest{}
				req.SetMember(member)
				_, err := h.idAdminClient.GrantFullPayload(ctx, connect.NewRequest(req))
				return err
			},
			wantComplete: memberSpec(),
			wantOutcome:  operatorv1.OperatorActionOutcome_OPERATOR_ACTION_OUTCOME_FAILED,
		},
		{
			name: "GrantFullPayload answers no expiry",
			setupHandler: func(h *testHarness) {
				stored := &identityv1.Member{}
				stored.SetOperator(member)
				resp := &identityapiv1.GrantFullPayloadResponse{}
				resp.SetMember(stored)
				h.idAdminHandler.grantFullPayload = resp
			},
			invoke: func(ctx context.Context, h *testHarness) error {
				req := &identityapiv1.GrantFullPayloadRequest{}
				req.SetMember(member)
				_, err := h.idAdminClient.GrantFullPayload(ctx, connect.NewRequest(req))
				return err
			},
			wantComplete: memberSpec(),
			wantOutcome:  operatorv1.OperatorActionOutcome_OPERATOR_ACTION_OUTCOME_SUCCEEDED,
		},
		{
			name: "CreateRole fails",
			setupHandler: func(h *testHarness) {
				h.idAdminHandler.err = failure
			},
			invoke: func(ctx context.Context, h *testHarness) error {
				_, err := h.idAdminClient.CreateRole(ctx, connect.NewRequest(&identityapiv1.CreateRoleRequest{}))
				return err
			},
			wantComplete: objectSpec{},
			wantOutcome:  operatorv1.OperatorActionOutcome_OPERATOR_ACTION_OUTCOME_FAILED,
		},
		{
			name:       "CreateTenant is denied",
			tenantless: true,
			setupHandler: func(h *testHarness) {
				h.tenantHandler.err = connect.NewError(connect.CodePermissionDenied, errors.New("denied"))
			},
			invoke: func(ctx context.Context, h *testHarness) error {
				_, err := h.tenantClient.CreateTenant(ctx, connect.NewRequest(&identityapiv1.CreateTenantRequest{}))
				return err
			},
			wantComplete: objectSpec{},
			wantOutcome:  operatorv1.OperatorActionOutcome_OPERATOR_ACTION_OUTCOME_DENIED,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHarness(t, time.Now())
			h.injector.injectTenant = !tc.tenantless
			tc.setupHandler(h)
			_ = tc.invoke(context.Background(), h)

			events := h.pub.getEvents()
			if len(events) != 2 {
				t.Fatalf("published %d events, want 2", len(events))
			}
			completion := events[1].event
			if got := completion.GetCompleted().GetOutcome(); got != tc.wantOutcome {
				t.Fatalf("outcome = %v, want %v", got, tc.wantOutcome)
			}
			switch {
			case tc.wantComplete.check != nil:
				tc.wantComplete.check(t, "completion", completion)
			case completion.WhichObject() != 0:
				t.Fatalf("completion object = %v, want none", completion.WhichObject())
			}
		})
	}
}

// A flood of views fills the read stream to its byte limit and evicts only
// view records. The tenants are many and the calls per tenant few, so no
// subject reaches its own cap and the byte limit is the only thing that
// removes a record.
func TestAViewFloodCannotEvictAChangeRecord(t *testing.T) {
	const (
		readMaxBytes     = 1 << 20
		floodTenants     = 48
		callsPerTenant   = 40
		perSubjectCap    = 1000
		changeSubjectFmt = "flowseer.%s.operator.action.setup_key_issue"
	)
	ctx := context.Background()
	hub, err := edgebus.StartHub(ctx, edgebus.HubConfig{
		StateDir:             t.TempDir(),
		FsyncPolicy:          service.BusFsyncPeriodic,
		OperatorReadMaxBytes: readMaxBytes,
	})
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	defer hub.Close()

	h := newHarnessOver(t, time.Now(), auditapi.JetStreamPublisher{JS: hub.JetStream()})
	h.edgeHandler.issueSetupKeyFunc = func(_ context.Context, _ *connect.Request[edgev1.IssueSetupKeyRequest]) (*connect.Response[edgev1.IssueSetupKeyResponse], error) {
		return connect.NewResponse(&edgev1.IssueSetupKeyResponse{}), nil
	}
	h.edgeHandler.getEdgeFunc = func(_ context.Context, _ *connect.Request[edgev1.GetEdgeRequest]) (*connect.Response[edgev1.GetEdgeResponse], error) {
		return connect.NewResponse(&edgev1.GetEdgeResponse{}), nil
	}

	issue := &edgev1.IssueSetupKeyRequest{}
	issue.SetEdge(edgeGlobalRef(testEdgeID))
	if _, err := h.adminClient.IssueSetupKey(ctx, connect.NewRequest(issue)); err != nil {
		t.Fatalf("IssueSetupKey: %v", err)
	}

	for i := range floodTenants {
		tenantID := fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1)
		for range callsPerTenant {
			get := &edgev1.GetEdgeRequest{}
			get.SetEdge(edgeGlobalRef(testEdgeID))
			req := connect.NewRequest(get)
			req.Header().Set(testTenantHeader, tenantID)
			if _, err := h.adminClient.GetEdge(ctx, req); err != nil {
				t.Fatalf("GetEdge for %s: %v", tenantID, err)
			}
		}
	}

	reads, err := hub.JetStream().Stream(ctx, edgebus.OperatorReadStream)
	if err != nil {
		t.Fatalf("open the read stream: %v", err)
	}
	readInfo, err := reads.Info(ctx, jetstream.WithSubjectFilter(">"))
	if err != nil {
		t.Fatalf("read stream info: %v", err)
	}
	if readInfo.State.FirstSeq <= 1 {
		t.Fatalf("read stream first sequence = %d, want the byte limit to have evicted records", readInfo.State.FirstSeq)
	}
	if readInfo.State.Bytes > readMaxBytes {
		t.Fatalf("read stream holds %d bytes, want at most %d", readInfo.State.Bytes, readMaxBytes)
	}
	for subject, n := range readInfo.State.Subjects {
		if n >= perSubjectCap {
			t.Fatalf("subject %q holds %d records, so the per-subject cap could have evicted instead of the byte limit", subject, n)
		}
	}

	actions, err := hub.JetStream().Stream(ctx, edgebus.OperatorActionStream)
	if err != nil {
		t.Fatalf("open the action stream: %v", err)
	}
	actionInfo, err := actions.Info(ctx, jetstream.WithSubjectFilter(">"))
	if err != nil {
		t.Fatalf("action stream info: %v", err)
	}
	if got := actionInfo.State.Subjects[fmt.Sprintf(changeSubjectFmt, testTenant)]; got != 2 {
		t.Fatalf("action stream holds %d setup_key_issue records after the flood, want 2", got)
	}
	if actionInfo.State.Msgs != 2 {
		t.Fatalf("action stream holds %d records, want only the 2 of the change", actionInfo.State.Msgs)
	}
}
