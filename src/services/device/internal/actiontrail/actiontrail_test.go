package actiontrail_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	connect "connectrpc.com/connect"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	capturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1"
	capturev1connect "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1/capturev1connect"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	edgev1connect "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1/edgev1connect"
	errsv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/errs/v1"
	operatorv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/operator/v1"
	modelcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	modeledgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/common/tenant"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/actiontrail"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz"
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

func TestEveryEdgeAdminProcedureIsRecorded(t *testing.T) {
	const wantProcedures = 6

	methods := edgev1.File_flowseer_api_edge_v1_edge_admin_service_proto.Services().ByName("EdgeAdminService").Methods()
	if methods.Len() != wantProcedures {
		t.Fatalf("EdgeAdminService has %d methods, want %d", methods.Len(), wantProcedures)
	}
	if len(actiontrail.EdgeAdminProcedureActions) != wantProcedures {
		t.Fatalf("EdgeAdminProcedureActions has %d entries, want %d", len(actiontrail.EdgeAdminProcedureActions), wantProcedures)
	}
	for i := 0; i < methods.Len(); i++ {
		m := methods.Get(i)
		proc := fmt.Sprintf("/%s/%s", m.Parent().FullName(), m.Name())
		if _, ok := actiontrail.EdgeAdminProcedureActions[proc]; !ok {
			t.Errorf("procedure %q missing from EdgeAdminProcedureActions", proc)
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

func (p *fakePublisher) Publish(_ context.Context, subject string, data []byte, msgID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	event := &operatorv1.OperatorActionEvent{}
	if err := proto.Unmarshal(data, event); err != nil {
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

type contextInjector struct {
	principal       authn.Principal
	tenantID        string
	injectPrincipal bool
	injectTenant    bool
}

func (ci *contextInjector) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if ci.injectPrincipal {
			ctx = authn.NewContext(ctx, ci.principal)
		}
		if ci.injectTenant {
			ctx = tenant.WithTenant(ctx, ci.tenantID)
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
	server        *httptest.Server
	pub           *fakePublisher
	logs          *logRecorder
	edgeHandler   *fakeEdgeAdminHandler
	capHandler    *fakeCaptureHandler
	injector      *contextInjector
	adminClient   edgev1connect.EdgeAdminServiceClient
	captureClient capturev1connect.CaptureServiceClient
}

func newTestHarness(t *testing.T, fixedTime time.Time) *testHarness {
	t.Helper()
	pub := &fakePublisher{}
	logs := &logRecorder{}
	logger := slog.New(logs)

	interceptor := actiontrail.NewInterceptor(pub, func() time.Time { return fixedTime }, logger)

	injector := &contextInjector{
		principal: authn.Principal{
			ID:      "principal-1",
			Issuer:  "https://issuer.example.com",
			Subject: "operator-42",
		},
		tenantID:        "tenant-alpha",
		injectPrincipal: true,
		injectTenant:    true,
	}

	edgeHandler := &fakeEdgeAdminHandler{}
	capHandler := &fakeCaptureHandler{}

	opts := connect.WithInterceptors(injector, interceptor)

	mux := http.NewServeMux()
	edgePath, edgeH := edgev1connect.NewEdgeAdminServiceHandler(edgeHandler, opts)
	mux.Handle(edgePath, edgeH)
	capPath, capH := capturev1connect.NewCaptureServiceHandler(capHandler, opts)
	mux.Handle(capPath, capH)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	adminClient := edgev1connect.NewEdgeAdminServiceClient(server.Client(), server.URL)
	captureClient := capturev1connect.NewCaptureServiceClient(server.Client(), server.URL)

	return &testHarness{
		server:        server,
		pub:           pub,
		logs:          logs,
		edgeHandler:   edgeHandler,
		capHandler:    capHandler,
		injector:      injector,
		adminClient:   adminClient,
		captureClient: captureClient,
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
	ref.SetEdge(edgeGlobalRef("edge-1"))
	sessLocal := &modelcapturev1.CaptureSessionLocalRef{}
	sessLocal.SetId(sessionID)
	ref.SetCaptureSession(sessLocal)
	return ref
}

func TestRequirement10IssueSetupKeyTrail(t *testing.T) {
	ctx := context.Background()
	fixedTime := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	h := newTestHarness(t, fixedTime)

	targetEdge := edgeGlobalRef("edge-1")
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

	wantSubject := "flowseer.tenant-alpha.operator.action.setup_key_issue"
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
		if rec.event.GetEdge().GetEdge().GetId() != "edge-1" {
			t.Errorf("event %d edge id = %q, want edge-1", idx, rec.event.GetEdge().GetEdge().GetId())
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
	req.SetEdge(edgeGlobalRef("edge-1"))
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
	req.SetEdge(edgeGlobalRef("edge-1"))
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
	req.SetEdge(edgeGlobalRef("edge-1"))
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
	req.SetEdge(edgeGlobalRef("edge-1"))
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

	createdEdgeRef := edgeGlobalRef("edge-created-99")
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
	if events[1].event.GetEdge().GetEdge().GetId() != "edge-created-99" {
		t.Fatalf("completion edge ID = %q, want edge-created-99", events[1].event.GetEdge().GetEdge().GetId())
	}
}

func TestCreateCaptureSession(t *testing.T) {
	ctx := context.Background()
	h := newTestHarness(t, time.Now())

	createdSessionRef := sessionGlobalRef("sess-555")
	h.capHandler.createSessionFunc = func(_ context.Context, _ *connect.Request[capturev1.CreateCaptureSessionRequest]) (*connect.Response[capturev1.CreateCaptureSessionResponse], error) {
		resp := &capturev1.CreateCaptureSessionResponse{}
		resp.SetSession(&modelcapturev1.CaptureSessionRecord{})
		resp.GetSession().SetConfig(&modelcapturev1.CaptureSessionConfig{})
		resp.GetSession().GetConfig().SetRef(createdSessionRef)
		return connect.NewResponse(resp), nil
	}

	t.Run("headers-only writes none", func(t *testing.T) {
		req := &capturev1.CreateCaptureSessionRequest{}
		req.SetEdge(edgeGlobalRef("edge-1"))
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
		req.SetEdge(edgeGlobalRef("edge-1"))
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

		wantSubj := "flowseer.tenant-alpha.operator.action.capture_full_payload_create"
		if events[0].subject != wantSubj || events[1].subject != wantSubj {
			t.Errorf("subject mismatch: %q, %q, want %q", events[0].subject, events[1].subject, wantSubj)
		}

		if events[0].event.WhichObject() != operatorv1.OperatorActionEvent_Edge_case {
			t.Fatalf("attempt object = %v, want Edge_case", events[0].event.WhichObject())
		}
		if events[0].event.GetEdge().GetEdge().GetId() != "edge-1" {
			t.Fatalf("attempt edge ID = %q, want edge-1", events[0].event.GetEdge().GetEdge().GetId())
		}

		if events[1].event.WhichObject() != operatorv1.OperatorActionEvent_CaptureSession_case {
			t.Fatalf("completion object = %v, want CaptureSession_case", events[1].event.WhichObject())
		}
		if events[1].event.GetCaptureSession().GetCaptureSession().GetId() != "sess-555" {
			t.Fatalf("completion session ID = %q, want sess-555", events[1].event.GetCaptureSession().GetCaptureSession().GetId())
		}
	})
}

func TestStreamingCaptureProcedures(t *testing.T) {
	ctx := context.Background()

	t.Run("TailCaptureSession attempt published before Send", func(t *testing.T) {
		h := newTestHarness(t, time.Now())
		sessRef := sessionGlobalRef("sess-tail-1")

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
		if events[0].event.GetCaptureSession().GetCaptureSession().GetId() != "sess-tail-1" {
			t.Errorf("attempt session ID = %q, want sess-tail-1", events[0].event.GetCaptureSession().GetCaptureSession().GetId())
		}
		if events[1].event.GetCaptureSession().GetCaptureSession().GetId() != "sess-tail-1" {
			t.Errorf("completion session ID = %q, want sess-tail-1", events[1].event.GetCaptureSession().GetCaptureSession().GetId())
		}
		if events[1].event.GetCompleted().GetOutcome() != operatorv1.OperatorActionOutcome_OPERATOR_ACTION_OUTCOME_SUCCEEDED {
			t.Errorf("completion outcome = %v, want SUCCEEDED", events[1].event.GetCompleted().GetOutcome())
		}
	})

	t.Run("DownloadCaptureSession attempt published before Send", func(t *testing.T) {
		h := newTestHarness(t, time.Now())
		sessRef := sessionGlobalRef("sess-down-1")

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
		sessRef := sessionGlobalRef("sess-fail-1")

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
		req.SetEdge(edgeGlobalRef("edge-1"))
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
		req.SetEdge(edgeGlobalRef("edge-1"))
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

func TestOperatorActionStreamRetentionFromOutsideModule(t *testing.T) {
	ctx := context.Background()
	hub, err := edgebus.StartHub(ctx, edgebus.HubConfig{
		StateDir:                    t.TempDir(),
		FsyncPolicy:                 service.BusFsyncPeriodic,
		OperatorActionMaxPerSubject: 2,
	})
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	defer hub.Close()

	const tenantID = "tenant-outside"
	subj1 := edgebus.OperatorActionSubject(tenantID, "setup_key_issue")
	subj2 := edgebus.OperatorActionSubject(tenantID, "edge_create")

	// Publish three records on subj1.
	for i, body := range []string{"outside-rec-1", "outside-rec-2", "outside-rec-3"} {
		if _, err := hub.JetStream().Publish(ctx, subj1, []byte(body), jetstream.WithMsgID(fmt.Sprintf("outside-s1-%d", i))); err != nil {
			t.Fatalf("publish on subj1: %v", err)
		}
	}
	// Publish one record on subj2.
	if _, err := hub.JetStream().Publish(ctx, subj2, []byte("outside-rec-4"), jetstream.WithMsgID("outside-s2-0")); err != nil {
		t.Fatalf("publish on subj2: %v", err)
	}

	stream, err := hub.JetStream().Stream(ctx, edgebus.OperatorActionStream)
	if err != nil {
		t.Fatalf("get stream: %v", err)
	}

	cons1, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		FilterSubject: subj1,
		DeliverPolicy: jetstream.DeliverAllPolicy,
	})
	if err != nil {
		t.Fatalf("create consumer 1: %v", err)
	}
	batch1, err := cons1.Fetch(10, jetstream.FetchMaxWait(2*time.Second))
	if err != nil {
		t.Fatalf("fetch 1: %v", err)
	}
	var got1 []string
	for msg := range batch1.Messages() {
		got1 = append(got1, string(msg.Data()))
		_ = msg.Ack()
	}
	if len(got1) != 2 || got1[0] != "outside-rec-2" || got1[1] != "outside-rec-3" {
		t.Fatalf("subj1 messages = %v, want [outside-rec-2 outside-rec-3]", got1)
	}

	cons2, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		FilterSubject: subj2,
		DeliverPolicy: jetstream.DeliverAllPolicy,
	})
	if err != nil {
		t.Fatalf("create consumer 2: %v", err)
	}
	batch2, err := cons2.Fetch(10, jetstream.FetchMaxWait(2*time.Second))
	if err != nil {
		t.Fatalf("fetch 2: %v", err)
	}
	var got2 []string
	for msg := range batch2.Messages() {
		got2 = append(got2, string(msg.Data()))
		_ = msg.Ack()
	}
	if len(got2) != 1 || got2[0] != "outside-rec-4" {
		t.Fatalf("subj2 messages = %v, want [outside-rec-4]", got2)
	}
}
