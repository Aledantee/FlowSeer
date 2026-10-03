package captureapi_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	operatorcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1/capturev1connect"
	modelcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	netcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/common/spawn"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz/authztest"
	"go.aledante.io/FlowSeer/src/services/device/internal/captureapi"
	"go.aledante.io/FlowSeer/src/services/device/internal/edgestore"
)

const (
	testTenantB = "0192e6a0-0000-7000-8000-000000000002"
)

func testPrincipal() authn.Principal {
	return authn.Principal{
		ID:       authn.ComputePrincipalID("https://auth.example.test", "admin-user"),
		Issuer:   "https://auth.example.test",
		Subject:  "admin-user",
		Tenants:  []string{testTenantID, testTenantB},
		Platform: true,
	}
}

type testTenantInterceptor struct {
	defaultTenant string
	h             *operatorTestHarness
}

func (i testTenantInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		t := req.Header().Get("FlowSeer-Tenant-ID")
		if t == "" {
			t = req.Header().Get("X-FlowSeer-Tenant")
		}
		if t == "" {
			t = i.defaultTenant
		}
		if t != "none" && t != "" {
			p := testPrincipal()
			p.Tenants = []string{t}
			ctx = authn.NewContext(ctx, p)
			if i.h.engine != nil {
				i.h.engine.Grant("tenant:"+t, "tenant", "edge")
			}
			admitted, err := i.h.interceptor.Admit(ctx, t)
			if err != nil {
				return nil, err
			}
			ctx = admitted
		}
		return next(ctx, req)
	}
}

func (i testTenantInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		t := conn.RequestHeader().Get("FlowSeer-Tenant-ID")
		if t == "" {
			t = conn.RequestHeader().Get("X-FlowSeer-Tenant")
		}
		if t == "" {
			t = i.defaultTenant
		}
		if t != "none" && t != "" {
			p := testPrincipal()
			p.Tenants = []string{t}
			ctx = authn.NewContext(ctx, p)
			if i.h.engine != nil {
				i.h.engine.Grant("tenant:"+t, "tenant", "edge")
			}
			admitted, err := i.h.interceptor.Admit(ctx, t)
			if err != nil {
				return err
			}
			ctx = admitted
		}
		return next(ctx, conn)
	}
}

func (i testTenantInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

type operatorTestHarness struct {
	hub          *edgebus.Hub
	store        *captureapi.Store
	broadcaster  *captureapi.Broadcaster
	notifyCount  atomic.Int64
	projectCount atomic.Int64
	client       capturev1connect.CaptureServiceClient
	server       *httptest.Server
	frozenNanos  atomic.Int64
	engine       *authztest.Engine
	interceptor  *authz.Interceptor
}

// now reads the harness clock. The store and the operator service both read it
// through this accessor, and setNow writes it, so a test moving the clock while
// a handler goroutine reads it stays race-free — the concurrency contract
// NewStore's clock parameter now states.
func (h *operatorTestHarness) now() time.Time {
	return time.Unix(0, h.frozenNanos.Load()).UTC()
}

func (h *operatorTestHarness) setNow(t time.Time) {
	h.frozenNanos.Store(t.UnixNano())
}

func newOperatorTestHarness(t *testing.T) *operatorTestHarness {
	t.Helper()
	broadcaster := captureapi.NewBroadcaster()
	frozen := time.Date(2026, 9, 18, 14, 0, 0, 0, time.UTC)

	hub, err := edgebus.StartHub(context.Background(), edgebus.HubConfig{
		StateDir:    t.TempDir(),
		FsyncPolicy: service.BusFsyncPeriodic,
		ListenPort:  0,
	})
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	t.Cleanup(hub.Close)

	kv, err := hub.JetStream().KeyValue(context.Background(), edgebus.CapturesBucket)
	if err != nil {
		t.Fatalf("captures bucket: %v", err)
	}

	engine := authztest.New()
	p := testPrincipal()
	engine.Grant("user:"+p.ID, "member", "tenant")
	engine.Grant("user:"+p.ID, "view", "edge")
	engine.Grant("user:"+p.ID, "capture", "edge")
	engine.Grant("tenant:"+testTenantID, "tenant", "edge")
	engine.Grant("tenant:"+testTenantB, "tenant", "edge")
	interceptor := authz.NewInterceptor(engine)

	h := &operatorTestHarness{
		hub:         hub,
		broadcaster: broadcaster,
		engine:      engine,
		interceptor: interceptor,
	}
	h.setNow(frozen)

	capturesDir := filepath.Join(t.TempDir(), "captures")
	store, err := captureapi.NewStore(kv, capturesDir, h.now)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	h.store = store

	svc := captureapi.NewOperatorService(h.store, broadcaster, captureapi.OperatorServiceConfig{
		EdgeTenant: func(_ context.Context, edgeID string) (string, error) {
			switch edgeID {
			case testEdge1ID:
				return testTenantID, nil
			case testEdge2ID:
				return testTenantB, nil
			case "fault-edge":
				return "", errs.New().Code(edgestore.ErrCodeStore).Msg("store failure")
			default:
				return "", errs.New().Code(edgestore.ErrCodeUnknownEdge).Msg("edge not found")
			}
		},
		NotifyChange: func() {
			h.notifyCount.Add(1)
		},
		Clock: func() time.Time {
			return h.now()
		},
		Project: func(_ context.Context, _, _ string) {
			h.projectCount.Add(1)
		},
	})

	path, handler := capturev1connect.NewCaptureServiceHandler(svc, connect.WithInterceptors(testTenantInterceptor{
		defaultTenant: testTenantID,
		h:             h,
	}))
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	h.server = server
	h.client = capturev1connect.NewCaptureServiceClient(server.Client(), server.URL)
	return h
}

func newTestCreateRequest(maxPackets uint64) *operatorcapturev1.CreateCaptureSessionRequest {
	req := operatorcapturev1.CreateCaptureSessionRequest_builder{
		Edge: edgev1.EdgeGlobalRef_builder{
			Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(testEdge1ID)}.Build(),
		}.Build(),
		Name:        proto.String("test-capture"),
		Description: proto.String("operator test"),
		Source: modelcapturev1.CaptureSource_builder{
			LocalInterface: modelcapturev1.LocalInterfaceSource_builder{
				InterfaceName: proto.String("eth0"),
				Promiscuous:   proto.Bool(true),
			}.Build(),
		}.Build(),
		Authorization: modelcapturev1.CaptureAuthorization_builder{
			RequestedBy: identityv1.OperatorRef_builder{
				Issuer:  proto.String("https://auth.example.com"),
				Subject: proto.String("zitadel|usr_123"),
			}.Build(),
			Reason:               proto.String("debugging traffic"),
			FullPayloadRequested: proto.Bool(false),
		}.Build(),
	}
	if maxPackets > 0 {
		req.Budget = modelcapturev1.CaptureBudget_builder{
			MaxPackets: proto.Uint64(maxPackets),
		}.Build()
	}
	return req.Build()
}

func TestCreateCaptureSession_BudgetValidationAndCreation(t *testing.T) {
	ctx := context.Background()
	h := newOperatorTestHarness(t)

	// Missing edge
	{
		req := newTestCreateRequest(100)
		req.SetEdge(nil)
		_, err := h.client.CreateCaptureSession(ctx, connect.NewRequest(req))
		if err == nil || connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("got missing-edge error %v, want CodeInvalidArgument", err)
		}
	}

	// Missing source
	{
		req := newTestCreateRequest(100)
		req.SetSource(nil)
		_, err := h.client.CreateCaptureSession(ctx, connect.NewRequest(req))
		if err == nil || connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("got missing-source error %v, want CodeInvalidArgument", err)
		}
	}

	// Missing authorization
	{
		req := newTestCreateRequest(100)
		req.SetAuthorization(nil)
		_, err := h.client.CreateCaptureSession(ctx, connect.NewRequest(req))
		if err == nil || connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("got missing-authorization error %v, want CodeInvalidArgument", err)
		}
	}

	// Empty authorization reason is refused.
	{
		req := newTestCreateRequest(100)
		req.GetAuthorization().SetReason("")
		_, err := h.client.CreateCaptureSession(ctx, connect.NewRequest(req))
		if err == nil || connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("got empty-reason error %v, want CodeInvalidArgument", err)
		}
	}

	// Requirement 8: requested_by is replaced by the authenticated principal.
	{
		req := newTestCreateRequest(100)
		req.GetAuthorization().SetRequestedBy(identityv1.OperatorRef_builder{
			Issuer:  proto.String("https://other.example.test"),
			Subject: proto.String("other-subject"),
		}.Build())
		resp, err := h.client.CreateCaptureSession(ctx, connect.NewRequest(req))
		if err != nil {
			t.Fatalf("CreateCaptureSession with foreign requested_by failed: %v", err)
		}
		storedReqBy := resp.Msg.GetSession().GetConfig().GetAuthorization().GetRequestedBy()
		p := testPrincipal()
		if storedReqBy.GetIssuer() != p.Issuer || storedReqBy.GetSubject() != p.Subject {
			t.Fatalf("stored requested_by = %+v, want issuer=%q subject=%q", storedReqBy, p.Issuer, p.Subject)
		}
	}

	// Missing budget
	{
		req := newTestCreateRequest(0)
		_, err := h.client.CreateCaptureSession(ctx, connect.NewRequest(req))
		if err == nil || connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("got missing-budget error %v, want CodeInvalidArgument", err)
		}
	}

	// Unbounded budget (empty budget fields)
	{
		req := newTestCreateRequest(0)
		req.SetBudget(&modelcapturev1.CaptureBudget{})
		_, err := h.client.CreateCaptureSession(ctx, connect.NewRequest(req))
		if err == nil || connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("got empty-budget error %v, want CodeInvalidArgument", err)
		}
	}

	// Budget with only zero bounds
	{
		req := newTestCreateRequest(0)
		req.SetBudget(modelcapturev1.CaptureBudget_builder{
			MaxPackets: proto.Uint64(0),
			MaxBytes:   proto.Uint64(0),
		}.Build())
		_, err := h.client.CreateCaptureSession(ctx, connect.NewRequest(req))
		if err == nil || connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("got zero-bounds error %v, want CodeInvalidArgument", err)
		}
	}

	// Successful creation with duration budget
	{
		req := newTestCreateRequest(0)
		req.SetBudget(modelcapturev1.CaptureBudget_builder{
			MaxDuration: durationpb.New(30 * time.Second),
		}.Build())
		resp, err := h.client.CreateCaptureSession(ctx, connect.NewRequest(req))
		if err != nil {
			t.Fatalf("create session with duration budget: %v", err)
		}
		if resp.Msg.GetSession().GetConfig().GetBudget().GetMaxDuration().AsDuration() != 30*time.Second {
			t.Fatalf("unexpected duration budget: %v", resp.Msg.GetSession().GetConfig().GetBudget())
		}
	}

	// Successful creation with packet budget
	initialNotifies := h.notifyCount.Load()
	req := newTestCreateRequest(150)
	resp, err := h.client.CreateCaptureSession(ctx, connect.NewRequest(req))
	if err != nil {
		t.Fatalf("create valid session: %v", err)
	}

	session := resp.Msg.GetSession()
	if session == nil {
		t.Fatal("expected non-nil session response")
	}

	sessionID := session.GetConfig().GetRef().GetCaptureSession().GetId()
	if sessionID == "" {
		t.Fatal("expected generated session uuid, got empty")
	}

	if session.GetState().GetLifecycle() != modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_PENDING {
		t.Fatalf("got lifecycle %v, want PENDING", session.GetState().GetLifecycle())
	}
	if session.GetConfig().GetBudget().GetMaxPackets() != 150 {
		t.Fatalf("got max_packets %d, want 150", session.GetConfig().GetBudget().GetMaxPackets())
	}

	// Verify persistence in store
	stored, _, err := h.store.Session(ctx, testTenantID, sessionID)
	if err != nil {
		t.Fatalf("get session from store: %v", err)
	}
	if stored == nil {
		t.Fatal("session not found in store")
	}
	if stored.GetConfig().GetRef().GetCaptureSession().GetId() != sessionID {
		t.Fatalf("stored session id mismatch: got %s, want %s", stored.GetConfig().GetRef().GetCaptureSession().GetId(), sessionID)
	}

	// Verify notify callback was invoked
	if h.notifyCount.Load() <= initialNotifies {
		t.Fatalf("expected notifyChange to be invoked, count=%d", h.notifyCount.Load())
	}
}

func TestStopCaptureSession_IdempotentAndTerminal(t *testing.T) {
	ctx := context.Background()
	h := newOperatorTestHarness(t)

	// Missing session ID
	{
		req := &operatorcapturev1.StopCaptureSessionRequest{}
		_, err := h.client.StopCaptureSession(ctx, connect.NewRequest(req))
		if err == nil || connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("got missing-session error %v, want CodeInvalidArgument", err)
		}
	}

	// Unknown session ID
	{
		req := operatorcapturev1.StopCaptureSessionRequest_builder{
			Session: modelcapturev1.CaptureSessionGlobalRef_builder{
				CaptureSession: modelcapturev1.CaptureSessionLocalRef_builder{
					Id: proto.String("0192e6a0-0000-7000-8000-999999999999"),
				}.Build(),
			}.Build(),
		}.Build()
		_, err := h.client.StopCaptureSession(ctx, connect.NewRequest(req))
		if err == nil || connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("got unknown-session error %v, want CodeNotFound", err)
		}
	}

	// Stop a PENDING session
	createResp, err := h.client.CreateCaptureSession(ctx, connect.NewRequest(newTestCreateRequest(100)))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	pendingID := createResp.Msg.GetSession().GetConfig().GetRef().GetCaptureSession().GetId()

	stopReq := operatorcapturev1.StopCaptureSessionRequest_builder{
		Session: createResp.Msg.GetSession().GetConfig().GetRef(),
	}.Build()

	stopResp, err := h.client.StopCaptureSession(ctx, connect.NewRequest(stopReq))
	if err != nil {
		t.Fatalf("stop pending session: %v", err)
	}
	if stopResp.Msg.GetSession().GetState().GetLifecycle() != modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_CANCELED {
		t.Fatalf("got lifecycle %v, want CANCELED", stopResp.Msg.GetSession().GetState().GetLifecycle())
	}
	if stopResp.Msg.GetSession().GetState().GetStopReason() != modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_OPERATOR {
		t.Fatalf("got stop reason %v, want CAPTURE_STOP_REASON_OPERATOR", stopResp.Msg.GetSession().GetState().GetStopReason())
	}
	if stopResp.Msg.GetSession().GetState().GetEndedAt() == nil {
		t.Fatal("expected ended_at timestamp to be set on pending stop")
	}

	// Idempotent second stop on already CANCELED session
	secondStopResp, err := h.client.StopCaptureSession(ctx, connect.NewRequest(stopReq))
	if err != nil {
		t.Fatalf("idempotent stop on canceled session: %v", err)
	}
	if secondStopResp.Msg.GetSession().GetState().GetLifecycle() != modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_CANCELED {
		t.Fatalf("got lifecycle %v on re-stop, want CANCELED", secondStopResp.Msg.GetSession().GetState().GetLifecycle())
	}

	// Stop a RUNNING session
	runningConfig := newEdgeSessionConfig(t, testEdge1ID, "0192e6a0-0000-7000-8000-000000000033")
	if _, err := h.store.CreateSession(ctx, testTenantID, runningConfig); err != nil {
		t.Fatalf("create running session in store: %v", err)
	}
	// Transition to RUNNING
	if _, err := h.store.MutateSession(ctx, testTenantID, "0192e6a0-0000-7000-8000-000000000033", func(rec *modelcapturev1.CaptureSessionRecord) error {
		rec.GetState().SetLifecycle(modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_RUNNING)
		rec.GetState().SetStartedAt(timestamppb.New(h.now()))
		return nil
	}); err != nil {
		t.Fatalf("mutate session to running: %v", err)
	}

	runningStopReq := operatorcapturev1.StopCaptureSessionRequest_builder{
		Session: runningConfig.GetRef(),
	}.Build()

	runningStopResp, err := h.client.StopCaptureSession(ctx, connect.NewRequest(runningStopReq))
	if err != nil {
		t.Fatalf("stop running session: %v", err)
	}
	if runningStopResp.Msg.GetSession().GetState().GetLifecycle() != modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_CANCELED {
		t.Fatalf("got lifecycle %v, want CANCELED", runningStopResp.Msg.GetSession().GetState().GetLifecycle())
	}
	if runningStopResp.Msg.GetSession().GetState().GetStopReason() != modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_OPERATOR {
		t.Fatalf("got stop reason %v, want CAPTURE_STOP_REASON_OPERATOR", runningStopResp.Msg.GetSession().GetState().GetStopReason())
	}

	// Stopping COMPLETED session is a no-op
	if _, err := h.store.MutateSession(ctx, testTenantID, pendingID, func(rec *modelcapturev1.CaptureSessionRecord) error {
		rec.GetState().SetLifecycle(modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_COMPLETED)
		return nil
	}); err != nil {
		t.Fatalf("mutate to completed: %v", err)
	}
	completedStopResp, err := h.client.StopCaptureSession(ctx, connect.NewRequest(stopReq))
	if err != nil {
		t.Fatalf("stop on completed session: %v", err)
	}
	if completedStopResp.Msg.GetSession().GetState().GetLifecycle() != modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_COMPLETED {
		t.Fatalf("got session lifecycle %v, want COMPLETED", completedStopResp.Msg.GetSession().GetState().GetLifecycle())
	}
}

func TestGetAndDeleteCaptureSession(t *testing.T) {
	ctx := context.Background()
	h := newOperatorTestHarness(t)

	// Get with missing session ID
	{
		req := &operatorcapturev1.GetCaptureSessionRequest{}
		_, err := h.client.GetCaptureSession(ctx, connect.NewRequest(req))
		if err == nil || connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("got missing-session-id error %v, want CodeInvalidArgument", err)
		}
	}

	// Get unknown session
	{
		req := operatorcapturev1.GetCaptureSessionRequest_builder{
			Session: modelcapturev1.CaptureSessionGlobalRef_builder{
				CaptureSession: modelcapturev1.CaptureSessionLocalRef_builder{
					Id: proto.String("0192e6a0-0000-7000-8000-999999999999"),
				}.Build(),
			}.Build(),
		}.Build()
		_, err := h.client.GetCaptureSession(ctx, connect.NewRequest(req))
		if err == nil || connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("got unknown-session error %v, want CodeNotFound", err)
		}
	}

	// Create a session
	createResp, err := h.client.CreateCaptureSession(ctx, connect.NewRequest(newTestCreateRequest(100)))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	sessionRef := createResp.Msg.GetSession().GetConfig().GetRef()
	sessionID := sessionRef.GetCaptureSession().GetId()

	// Append some packets so an artifact file exists on disk
	packets := []*netcapturev1.PacketRecord{
		testPacketRecord(1, []byte("packet-payload")),
	}
	if err := h.store.AppendPackets(ctx, testTenantID, sessionID, netcapturev1.LinkType_LINK_TYPE_ETHERNET, 65535, packets); err != nil {
		t.Fatalf("append packets: %v", err)
	}
	if !h.store.ArtifactExists(testTenantID, sessionID) {
		t.Fatal("expected artifact to exist on disk")
	}

	// Register a broadcaster tail subscriber to test cleanup on delete
	sub, unsub := h.broadcaster.Subscribe(testTenantID, sessionID)
	defer unsub()

	// Get existing session
	getReq := operatorcapturev1.GetCaptureSessionRequest_builder{
		Session: sessionRef,
	}.Build()
	getResp, err := h.client.GetCaptureSession(ctx, connect.NewRequest(getReq))
	if err != nil {
		t.Fatalf("get capture session: %v", err)
	}
	if getResp.Msg.GetSession().GetConfig().GetRef().GetCaptureSession().GetId() != sessionID {
		t.Fatalf("got session %s, want %s", getResp.Msg.GetSession().GetConfig().GetRef().GetCaptureSession().GetId(), sessionID)
	}

	// Delete capture session
	deleteReq := operatorcapturev1.DeleteCaptureSessionRequest_builder{
		Session: sessionRef,
	}.Build()
	if _, err := h.client.DeleteCaptureSession(ctx, connect.NewRequest(deleteReq)); err != nil {
		t.Fatalf("delete capture session: %v", err)
	}

	// Broadcaster channel must be closed
	select {
	case _, ok := <-sub.Items():
		if ok {
			t.Fatal("expected subscriber channel to be closed upon session deletion")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for subscriber channel close")
	}

	// Artifact file must be unlinked
	if h.store.ArtifactExists(testTenantID, sessionID) {
		t.Fatal("expected artifact file to be unlinked after delete")
	}

	// Subsequent Get must return CodeNotFound
	_, err = h.client.GetCaptureSession(ctx, connect.NewRequest(getReq))
	if err == nil || connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("got post-deletion error %v, want CodeNotFound", err)
	}
}

func TestListCaptureSessions_Pagination(t *testing.T) {
	ctx := context.Background()
	h := newOperatorTestHarness(t)

	// Create 5 sessions with deterministic UUIDs
	sessionIDs := []string{
		"0192e6a0-0000-7000-8000-000000000001",
		"0192e6a0-0000-7000-8000-000000000002",
		"0192e6a0-0000-7000-8000-000000000003",
		"0192e6a0-0000-7000-8000-000000000004",
		"0192e6a0-0000-7000-8000-000000000005",
	}
	for _, id := range sessionIDs {
		cfg := newEdgeSessionConfig(t, testEdge1ID, id)
		if _, err := h.store.CreateSession(ctx, testTenantID, cfg); err != nil {
			t.Fatalf("create session %s: %v", id, err)
		}
	}

	// Page 1: page_size = 2
	req1 := operatorcapturev1.ListCaptureSessionsRequest_builder{
		PageSize: proto.Uint32(2),
	}.Build()
	resp1, err := h.client.ListCaptureSessions(ctx, connect.NewRequest(req1))
	if err != nil {
		t.Fatalf("list page 1: %v", err)
	}
	if len(resp1.Msg.GetSessions()) != 2 {
		t.Fatalf("got %d sessions on page 1, want 2", len(resp1.Msg.GetSessions()))
	}
	if resp1.Msg.GetSessions()[0].GetConfig().GetRef().GetCaptureSession().GetId() != sessionIDs[0] {
		t.Fatalf("got session %s, want %s", resp1.Msg.GetSessions()[0].GetConfig().GetRef().GetCaptureSession().GetId(), sessionIDs[0])
	}
	if resp1.Msg.GetSessions()[1].GetConfig().GetRef().GetCaptureSession().GetId() != sessionIDs[1] {
		t.Fatalf("got session %s, want %s", resp1.Msg.GetSessions()[1].GetConfig().GetRef().GetCaptureSession().GetId(), sessionIDs[1])
	}
	if resp1.Msg.GetNextPageToken() != sessionIDs[1] {
		t.Fatalf("got next_page_token %s, want %s", resp1.Msg.GetNextPageToken(), sessionIDs[1])
	}

	// Page 2: page_size = 2, page_token = sessionIDs[1]
	req2 := operatorcapturev1.ListCaptureSessionsRequest_builder{
		PageSize:  proto.Uint32(2),
		PageToken: proto.String(resp1.Msg.GetNextPageToken()),
	}.Build()
	resp2, err := h.client.ListCaptureSessions(ctx, connect.NewRequest(req2))
	if err != nil {
		t.Fatalf("list page 2: %v", err)
	}
	if len(resp2.Msg.GetSessions()) != 2 {
		t.Fatalf("got %d sessions on page 2, want 2", len(resp2.Msg.GetSessions()))
	}
	if resp2.Msg.GetSessions()[0].GetConfig().GetRef().GetCaptureSession().GetId() != sessionIDs[2] {
		t.Fatalf("got session %s, want %s", resp2.Msg.GetSessions()[0].GetConfig().GetRef().GetCaptureSession().GetId(), sessionIDs[2])
	}
	if resp2.Msg.GetSessions()[1].GetConfig().GetRef().GetCaptureSession().GetId() != sessionIDs[3] {
		t.Fatalf("got session %s, want %s", resp2.Msg.GetSessions()[1].GetConfig().GetRef().GetCaptureSession().GetId(), sessionIDs[3])
	}
	if resp2.Msg.GetNextPageToken() != sessionIDs[3] {
		t.Fatalf("got next_page_token %s, want %s", resp2.Msg.GetNextPageToken(), sessionIDs[3])
	}

	// Page 3: page_size = 2, page_token = sessionIDs[3] (last remaining session)
	req3 := operatorcapturev1.ListCaptureSessionsRequest_builder{
		PageSize:  proto.Uint32(2),
		PageToken: proto.String(resp2.Msg.GetNextPageToken()),
	}.Build()
	resp3, err := h.client.ListCaptureSessions(ctx, connect.NewRequest(req3))
	if err != nil {
		t.Fatalf("list page 3: %v", err)
	}
	if len(resp3.Msg.GetSessions()) != 1 {
		t.Fatalf("got %d sessions on page 3, want 1", len(resp3.Msg.GetSessions()))
	}
	if resp3.Msg.GetSessions()[0].GetConfig().GetRef().GetCaptureSession().GetId() != sessionIDs[4] {
		t.Fatalf("got session %s, want %s", resp3.Msg.GetSessions()[0].GetConfig().GetRef().GetCaptureSession().GetId(), sessionIDs[4])
	}
	if resp3.Msg.GetNextPageToken() != "" {
		t.Fatalf("got next_page_token %q on last page, want empty", resp3.Msg.GetNextPageToken())
	}
}

func TestTailCaptureSession_LiveStreaming(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newOperatorTestHarness(t)
	sessID := "0192e6a0-0000-7000-8000-000000000044"
	cfg := newEdgeSessionConfig(t, testEdge1ID, sessID)

	// Unknown session returns CodeNotFound
	{
		badReq := operatorcapturev1.TailCaptureSessionRequest_builder{
			Session: cfg.GetRef(),
		}.Build()
		stream, err := h.client.TailCaptureSession(ctx, connect.NewRequest(badReq))
		if err != nil {
			t.Fatalf("tail rpc invocation: %v", err)
		}
		if stream.Receive() {
			t.Fatal("expected no messages for unknown session")
		}
		if connect.CodeOf(stream.Err()) != connect.CodeNotFound {
			t.Fatalf("got unknown-session tail error %v, want CodeNotFound", stream.Err())
		}
		_ = stream.Close()
	}

	if _, err := h.store.CreateSession(ctx, testTenantID, cfg); err != nil {
		t.Fatalf("create session: %v", err)
	}

	counters1 := netcapturev1.CaptureCounters_builder{
		ReceivedPackets: proto.Uint64(1),
		AcceptedPackets: proto.Uint64(1),
	}.Build()
	counters2 := netcapturev1.CaptureCounters_builder{
		ReceivedPackets: proto.Uint64(2),
		AcceptedPackets: proto.Uint64(2),
	}.Build()

	chunk1 := modelcapturev1.CapturePacketChunk_builder{
		Session:       cfg.GetRef(),
		FirstSequence: proto.Uint64(1),
		Packets: []*netcapturev1.PacketRecord{
			testPacketRecord(1, []byte("tail-packet-1")),
		},
		Counters: counters1,
		Final:    proto.Bool(false),
	}.Build()

	chunk2 := modelcapturev1.CapturePacketChunk_builder{
		Session:       cfg.GetRef(),
		FirstSequence: proto.Uint64(2),
		Packets: []*netcapturev1.PacketRecord{
			testPacketRecord(2, []byte("tail-packet-2")),
		},
		Counters:   counters2,
		Final:      proto.Bool(true),
		StopReason: modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_PACKET_COUNT.Enum(),
	}.Build()

	// A fixture is a claim the wire could deliver this message.
	for _, chunk := range []*modelcapturev1.CapturePacketChunk{chunk1, chunk2} {
		if err := protovalidate.Validate(chunk); err != nil {
			t.Fatalf("chunk fixture is not a message the wire would accept: %v", err)
		}
	}

	// Broadcast asynchronously once the tail handler has subscribed
	spawn.Go(ctx, "test-tail-broadcast", func() {
		deadline := time.Now().Add(5 * time.Second)
		for h.broadcaster.SubscriberCount(testTenantID, sessID) == 0 {
			if time.Now().After(deadline) {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		h.broadcaster.Broadcast(testTenantID, sessID, chunk1)
		time.Sleep(20 * time.Millisecond)
		h.broadcaster.Broadcast(testTenantID, sessID, chunk2)
	})

	tailReq := operatorcapturev1.TailCaptureSessionRequest_builder{
		Session: cfg.GetRef(),
	}.Build()

	stream, err := h.client.TailCaptureSession(ctx, connect.NewRequest(tailReq))
	if err != nil {
		t.Fatalf("tail session: %v", err)
	}
	defer func() { _ = stream.Close() }()

	if !stream.Receive() {
		t.Fatalf("expected the attached marker from tail, stream ended: %v", stream.Err())
	}
	if !stream.Msg().GetAttached() {
		t.Fatalf("got opening tail message %+v, want attached marker", stream.Msg())
	}

	if !stream.Receive() {
		t.Fatalf("expected chunk 1 from tail, stream ended: %v", stream.Err())
	}
	gotChunk1 := stream.Msg().GetChunk()
	if gotChunk1.GetFinal() {
		t.Fatal("expected chunk 1 to be non-final")
	}
	if len(gotChunk1.GetPackets()) != 1 || string(gotChunk1.GetPackets()[0].GetData()) != "tail-packet-1" {
		t.Fatalf("unexpected chunk 1 payload: %+v", gotChunk1)
	}
	// What the edge sent has to arrive intact, sequence and counters with it.
	if gotChunk1.GetFirstSequence() != 1 || gotChunk1.GetCounters().GetAcceptedPackets() != 1 {
		t.Fatalf("chunk 1 lost its sequence or counters on the way through: %+v", gotChunk1)
	}

	if !stream.Receive() {
		t.Fatalf("expected chunk 2 from tail, stream ended: %v", stream.Err())
	}
	gotChunk2 := stream.Msg().GetChunk()
	if !gotChunk2.GetFinal() {
		t.Fatal("expected chunk 2 to be final")
	}
	if gotChunk2.GetFirstSequence() != 2 || gotChunk2.GetCounters().GetAcceptedPackets() != 2 {
		t.Fatalf("chunk 2 lost its sequence or counters on the way through: %+v", gotChunk2)
	}

	// Stream should complete cleanly after final chunk
	if stream.Receive() {
		t.Fatal("expected stream to terminate after final chunk")
	}
	if stream.Err() != nil {
		t.Fatalf("got stream termination error %v, want nil", stream.Err())
	}
}

func TestTailCaptureSession_InBandGapOnSlowConsumer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newOperatorTestHarness(t)
	sessID := "0192e6a0-0000-7000-8000-000000000045"
	cfg := newEdgeSessionConfig(t, testEdge1ID, sessID)
	if _, err := h.store.CreateSession(ctx, testTenantID, cfg); err != nil {
		t.Fatalf("create session: %v", err)
	}

	tailReq := operatorcapturev1.TailCaptureSessionRequest_builder{
		Session: cfg.GetRef(),
	}.Build()
	stream, err := h.client.TailCaptureSession(ctx, connect.NewRequest(tailReq))
	if err != nil {
		t.Fatalf("tail session: %v", err)
	}
	defer func() { _ = stream.Close() }()

	if !stream.Receive() {
		t.Fatalf("expected the attached marker, stream ended: %v", stream.Err())
	}
	if !stream.Msg().GetAttached() {
		t.Fatalf("got opening tail frame %+v, want attached marker", stream.Msg())
	}

	// Wait for the handler to subscribe, then flood far past the 128-item buffer
	// while this consumer holds off reading. The handler blocks on the transport,
	// the subscription overflows, and the final chunk is dropped into the gap —
	// so the drain below also exercises the terminal flush before EOF. Large
	// payloads guarantee the transport back-pressures rather than absorbing the
	// whole flood.
	deadline := time.Now().Add(5 * time.Second)
	for h.broadcaster.SubscriberCount(testTenantID, sessID) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("tail never subscribed")
		}
		time.Sleep(5 * time.Millisecond)
	}

	const total = 400
	payload := bytes.Repeat([]byte("x"), 32*1024)
	for seq := uint64(1); seq <= total; seq++ {
		chunk := modelcapturev1.CapturePacketChunk_builder{
			Session:       cfg.GetRef(),
			FirstSequence: proto.Uint64(seq),
			Packets:       []*netcapturev1.PacketRecord{testPacketRecord(seq, payload)},
			Final:         proto.Bool(seq == total),
		}.Build()
		if seq == total {
			chunk.SetStopReason(modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_PACKET_COUNT)
		}
		h.broadcaster.Broadcast(testTenantID, sessID, chunk)
	}
	h.broadcaster.CloseSession(testTenantID, sessID)

	// However the loss splits between delivered chunks and gaps, the delivered
	// sequences and the gap-covered spans must tile 1..total with no hole and no
	// overlap, in order: that is what an in-band gap buys over a silent drop.
	var expected uint64 = 1
	gaps := 0
	lastWasGap := false
	var lastGapLast uint64
	for stream.Receive() {
		msg := stream.Msg()
		switch msg.WhichBody() {
		case operatorcapturev1.TailCaptureSessionResponse_Chunk_case:
			c := msg.GetChunk()
			if c.GetFirstSequence() != expected {
				t.Fatalf("chunk starts at %d, want %d — a hole opened in the tail", c.GetFirstSequence(), expected)
			}
			expected += uint64(len(c.GetPackets()))
			lastWasGap = false
		case operatorcapturev1.TailCaptureSessionResponse_Gap_case:
			gaps++
			g := msg.GetGap()
			if g.GetDroppedChunks() == 0 {
				t.Fatalf("gap reports zero dropped chunks: %+v", g)
			}
			if g.GetFirstDroppedSequence() != expected {
				t.Fatalf("gap starts at %d, want %d — the gap does not abut the delivered chunks", g.GetFirstDroppedSequence(), expected)
			}
			if g.GetLastDroppedSequence() < g.GetFirstDroppedSequence() {
				t.Fatalf("gap range inverted: %+v", g)
			}
			expected = g.GetLastDroppedSequence() + 1
			lastWasGap = true
			lastGapLast = g.GetLastDroppedSequence()
		default:
			t.Fatalf("unexpected tail frame: %+v", msg)
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("tail stream ended with error: %v", err)
	}
	if expected != total+1 {
		t.Fatalf("tail covered sequences up to %d, want %d — packets went missing with no gap", expected-1, total)
	}
	if gaps == 0 {
		t.Fatal("expected at least one in-band gap when the consumer lagged behind a flood")
	}
	// The paused consumer never drains, so the final chunk is dropped and its
	// loss must be the last frame before EOF — a terminal flush, not a silent
	// EOF over the lost final packets.
	if !lastWasGap || lastGapLast != total {
		t.Fatalf("last frame before EOF was not a terminal gap covering the final sequence (lastWasGap=%v lastGapLast=%d, want a gap reaching %d)", lastWasGap, lastGapLast, total)
	}
}

func TestTailGap_SchemaBoundRejectsEmptyGap(t *testing.T) {
	// R1 acceptance: a gap carrying real counts validates on the wire.
	valid := operatorcapturev1.TailCaptureSessionResponse_builder{
		Gap: operatorcapturev1.TailGap_builder{
			DroppedChunks:        proto.Uint64(1),
			DroppedPackets:       proto.Uint64(256),
			FirstDroppedSequence: proto.Uint64(100),
			LastDroppedSequence:  proto.Uint64(355),
		}.Build(),
	}.Build()
	if err := protovalidate.Validate(valid); err != nil {
		t.Fatalf("a gap of 1 chunk / 256 packets should validate: %v", err)
	}

	// An empty gap must be rejected. Without the presence rule the count bound
	// is skipped for the absent field, so a zero-drop gap would slip through.
	empty := operatorcapturev1.TailCaptureSessionResponse_builder{
		Gap: operatorcapturev1.TailGap_builder{}.Build(),
	}.Build()
	if err := protovalidate.Validate(empty); err == nil {
		t.Fatal("an empty gap must fail validation: a gap reports at least one dropped chunk")
	}

	// A gap that explicitly claims zero dropped chunks is rejected by the bound.
	zero := operatorcapturev1.TailCaptureSessionResponse_builder{
		Gap: operatorcapturev1.TailGap_builder{DroppedChunks: proto.Uint64(0)}.Build(),
	}.Build()
	if err := protovalidate.Validate(zero); err == nil {
		t.Fatal("a gap claiming zero dropped chunks must fail validation")
	}
}

func TestDownloadCaptureSession_ChunkedAndNotFoundOnExpired(t *testing.T) {
	ctx := context.Background()
	h := newOperatorTestHarness(t)

	sessID := "0192e6a0-0000-7000-8000-000000000055"
	cfg := newEdgeSessionConfig(t, testEdge1ID, sessID)

	// Session not found
	{
		badReq := operatorcapturev1.DownloadCaptureSessionRequest_builder{
			Session: cfg.GetRef(),
		}.Build()
		stream, err := h.client.DownloadCaptureSession(ctx, connect.NewRequest(badReq))
		if err != nil {
			t.Fatalf("download invocation: %v", err)
		}
		if stream.Receive() {
			t.Fatal("expected no messages for non-existent session download")
		}
		if connect.CodeOf(stream.Err()) != connect.CodeNotFound {
			t.Fatalf("got error %v, want CodeNotFound", stream.Err())
		}
		_ = stream.Close()
	}

	if _, err := h.store.CreateSession(ctx, testTenantID, cfg); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// A session that has not produced an artifact is not the same answer as
	// one whose artifact is gone: the first will have something to download
	// later, the second never will again. Only the second is CodeNotFound.
	{
		downloadReq := operatorcapturev1.DownloadCaptureSessionRequest_builder{
			Session: cfg.GetRef(),
		}.Build()
		stream, err := h.client.DownloadCaptureSession(ctx, connect.NewRequest(downloadReq))
		if err != nil {
			t.Fatalf("download invocation: %v", err)
		}
		if stream.Receive() {
			t.Fatal("expected no stream messages when the capture has not finished")
		}
		if connect.CodeOf(stream.Err()) != connect.CodeFailedPrecondition {
			t.Fatalf("got unfinished-capture error %v, want CodeFailedPrecondition", stream.Err())
		}
		_ = stream.Close()
	}

	// Create > 2.5 MB artifact by appending 5 large packets (600 KB each)
	const (
		packetPayloadSize = 600 * 1024
		packetCount       = 5
	)
	largeData := bytes.Repeat([]byte("X"), packetPayloadSize)
	packets := make([]*netcapturev1.PacketRecord, packetCount)
	for i := range packets {
		packets[i] = testPacketRecord(uint64(i+1), largeData)
	}

	if err := h.store.AppendPackets(ctx, testTenantID, sessID, netcapturev1.LinkType_LINK_TYPE_ETHERNET, 65535, packets); err != nil {
		t.Fatalf("append large packets: %v", err)
	}

	counters := netcapturev1.CaptureCounters_builder{
		ReceivedPackets: proto.Uint64(packetCount),
		AcceptedPackets: proto.Uint64(packetCount),
	}.Build()

	artifact, err := h.store.FinalizeArtifact(ctx, testTenantID, sessID, netcapturev1.LinkType_LINK_TYPE_ETHERNET, 65535, counters, h.now().Add(time.Hour))
	if err != nil {
		t.Fatalf("finalize artifact: %v", err)
	}
	if artifact == nil {
		t.Fatal("expected non-nil artifact after finalization")
	}
	if _, err := h.store.MutateSession(ctx, testTenantID, sessID, func(r *modelcapturev1.CaptureSessionRecord) error {
		r.GetState().SetLifecycle(modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_COMPLETED)
		r.GetState().SetStopReason(modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_PACKET_COUNT)
		r.GetState().SetCounters(counters)
		r.GetState().SetArtifact(artifact)
		return nil
	}); err != nil {
		t.Fatalf("store artifact on session: %v", err)
	}
	if artifact.GetByteSize() <= 2*1024*1024 {
		t.Fatalf("got artifact size %d, want more than 2 MiB", artifact.GetByteSize())
	}

	// Download artifact and verify chunking
	downloadReq := operatorcapturev1.DownloadCaptureSessionRequest_builder{
		Session: cfg.GetRef(),
	}.Build()
	stream, err := h.client.DownloadCaptureSession(ctx, connect.NewRequest(downloadReq))
	if err != nil {
		t.Fatalf("start download: %v", err)
	}
	defer func() { _ = stream.Close() }()

	var (
		downloadedChunks int
		hasher           = sha256.New()
		totalBytes       int64
	)
	for stream.Receive() {
		chunk := stream.Msg().GetChunk()
		if chunk == nil {
			t.Fatal("expected non-nil chunk message")
		}
		data := chunk.GetData()
		if len(data) > 1024*1024 {
			t.Fatalf("chunk size %d exceeded 1MiB bound", len(data))
		}
		downloadedChunks++
		totalBytes += int64(len(data))
		hasher.Write(data)
	}
	if stream.Err() != nil {
		t.Fatalf("download stream error: %v", stream.Err())
	}

	if downloadedChunks < 3 {
		t.Fatalf("got %d chunks, want at least 3 for payload larger than 2.5 MiB", downloadedChunks)
	}
	if uint64(totalBytes) != artifact.GetByteSize() {
		t.Fatalf("downloaded bytes mismatch: got %d, want %d", totalBytes, artifact.GetByteSize())
	}
	if !bytes.Equal(hasher.Sum(nil), artifact.GetDigest()) {
		t.Fatalf("downloaded sha256 digest mismatch")
	}

	// Move both clocks past the artifact's expiry and sweep.
	expired := h.now().Add(2 * time.Hour)
	h.setNow(expired)

	removed, err := h.store.SweepExpired(ctx)
	if err != nil {
		t.Fatalf("sweep expired: %v", err)
	}
	if removed != 1 {
		t.Fatalf("got %d purged artifacts, want 1", removed)
	}
	if h.store.ArtifactExists(testTenantID, sessID) {
		t.Fatal("expected artifact to be swept from disk")
	}

	// What the retention departure keeps: the record, its counters, and the
	// descriptor of what was captured, now stamped with when it was purged.
	afterSweep, _, err := h.store.Session(ctx, testTenantID, sessID)
	if err != nil {
		t.Fatalf("get session after sweep: %v", err)
	}
	if got := afterSweep.GetState().GetLifecycle(); got != modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_COMPLETED {
		t.Fatalf("got session lifecycle %v after sweep, want COMPLETED", got)
	}
	if got := afterSweep.GetState().GetCounters().GetAcceptedPackets(); got != packetCount {
		t.Fatalf("got accepted count %d after sweep, want %d", got, packetCount)
	}
	swept := afterSweep.GetState().GetArtifact()
	if swept.GetByteSize() != artifact.GetByteSize() || !bytes.Equal(swept.GetDigest(), artifact.GetDigest()) {
		t.Fatal("expected the artifact descriptor to survive the sweep")
	}
	if !swept.HasPurgedAt() {
		t.Fatal("expected the swept artifact to be stamped purged_at")
	}

	// A second sweep finds nothing left to do: the stamp is what stops a
	// purged session being re-examined and re-unlinked on every tick.
	again, err := h.store.SweepExpired(ctx)
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if again != 0 {
		t.Fatalf("got %d purged artifacts on second sweep, want 0", again)
	}

	// Attempting download after sweep returns CodeNotFound
	stream2, err := h.client.DownloadCaptureSession(ctx, connect.NewRequest(downloadReq))
	if err != nil {
		t.Fatalf("download after sweep invocation: %v", err)
	}
	if stream2.Receive() {
		t.Fatal("expected stream to fail after artifact swept")
	}
	if connect.CodeOf(stream2.Err()) != connect.CodeNotFound {
		t.Fatalf("got swept-artifact error %v, want CodeNotFound", stream2.Err())
	}
	_ = stream2.Close()
}

// Only the upload relay closes a tail's channel, and only once, when the
// capture ends. A tail opened after that moment waits on a channel nobody will
// ever send to; the session's own state is what has to end it.
func TestTailCaptureSession_ReturnsForASessionThatAlreadyEnded(t *testing.T) {
	h := newOperatorTestHarness(t)
	ctx := context.Background()

	sessID := "0192e6a0-0000-7000-8000-000000000066"
	cfg := newEdgeSessionConfig(t, testEdge1ID, sessID)
	if _, err := h.store.CreateSession(ctx, testTenantID, cfg); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := h.store.MutateSession(ctx, testTenantID, sessID, func(r *modelcapturev1.CaptureSessionRecord) error {
		r.GetState().SetLifecycle(modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_COMPLETED)
		r.GetState().SetStopReason(modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_PACKET_COUNT)
		return nil
	}); err != nil {
		t.Fatalf("complete session: %v", err)
	}

	// A deadline here is the failure signal, not the mechanism: the handler
	// has to end the stream itself, well before this expires.
	tailCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	stream, err := h.client.TailCaptureSession(tailCtx, connect.NewRequest(operatorcapturev1.TailCaptureSessionRequest_builder{
		Session: cfg.GetRef(),
	}.Build()))
	if err != nil {
		t.Fatalf("open tail: %v", err)
	}
	defer func() { _ = stream.Close() }()

	if stream.Receive() {
		t.Fatal("expected no chunks for a session that already ended")
	}
	if stream.Err() != nil {
		t.Fatalf("got tail termination error %v, want nil", stream.Err())
	}
	if tailCtx.Err() != nil {
		t.Fatal("the tail blocked until its deadline instead of ending with the session")
	}
	if got := h.broadcaster.SubscriberCount(testTenantID, sessID); got != 0 {
		t.Fatalf("got %d tail subscribers after completion, want 0", got)
	}
}

func TestOperatorService_CrossTenantIsolation(t *testing.T) {
	h := newOperatorTestHarness(t)
	ctx := context.Background()

	// Tenant A creates a session
	createResp, err := h.client.CreateCaptureSession(ctx, connect.NewRequest(newTestCreateRequest(10)))
	if err != nil {
		t.Fatalf("CreateCaptureSession: %v", err)
	}
	sessionRef := createResp.Msg.GetSession().GetConfig().GetRef()
	sessionID := sessionRef.GetCaptureSession().GetId()

	// Tenant B cannot get Tenant A session
	getReq := connect.NewRequest(operatorcapturev1.GetCaptureSessionRequest_builder{
		Session: sessionRef,
	}.Build())
	getReq.Header().Set("FlowSeer-Tenant-ID", testTenantB)
	_, err = h.client.GetCaptureSession(ctx, getReq)
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("GetCaptureSession tenant B got code %v, want CodeNotFound", connect.CodeOf(err))
	}

	// Tenant B listing is empty
	listReq := connect.NewRequest(&operatorcapturev1.ListCaptureSessionsRequest{})
	listReq.Header().Set("FlowSeer-Tenant-ID", testTenantB)
	listResp, err := h.client.ListCaptureSessions(ctx, listReq)
	if err != nil {
		t.Fatalf("ListCaptureSessions tenant B: %v", err)
	}
	if len(listResp.Msg.GetSessions()) != 0 {
		t.Errorf("ListCaptureSessions tenant B saw %d sessions, want 0", len(listResp.Msg.GetSessions()))
	}

	// Tenant B cannot stop Tenant A session
	stopReq := connect.NewRequest(operatorcapturev1.StopCaptureSessionRequest_builder{
		Session: sessionRef,
	}.Build())
	stopReq.Header().Set("FlowSeer-Tenant-ID", testTenantB)
	_, err = h.client.StopCaptureSession(ctx, stopReq)
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("StopCaptureSession tenant B got code %v, want CodeNotFound", connect.CodeOf(err))
	}

	// Tenant B cannot download Tenant A session
	downloadReq := connect.NewRequest(operatorcapturev1.DownloadCaptureSessionRequest_builder{
		Session: sessionRef,
	}.Build())
	downloadReq.Header().Set("FlowSeer-Tenant-ID", testTenantB)
	downloadStream, err := h.client.DownloadCaptureSession(ctx, downloadReq)
	if err == nil {
		if downloadStream.Receive() {
			t.Error("DownloadCaptureSession tenant B received chunk, want error")
		}
		if connect.CodeOf(downloadStream.Err()) != connect.CodeNotFound {
			t.Errorf("DownloadCaptureSession tenant B stream err code = %v, want CodeNotFound", connect.CodeOf(downloadStream.Err()))
		}
	}

	// Tenant B cannot tail Tenant A session
	tailReq := connect.NewRequest(operatorcapturev1.TailCaptureSessionRequest_builder{
		Session: sessionRef,
	}.Build())
	tailReq.Header().Set("FlowSeer-Tenant-ID", testTenantB)
	tailStream, err := h.client.TailCaptureSession(ctx, tailReq)
	if err == nil {
		if tailStream.Receive() {
			t.Error("TailCaptureSession tenant B received chunk, want error")
		}
		if connect.CodeOf(tailStream.Err()) != connect.CodeNotFound {
			t.Errorf("TailCaptureSession tenant B stream err code = %v, want CodeNotFound", connect.CodeOf(tailStream.Err()))
		}
	} else if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("TailCaptureSession tenant B got code %v, want CodeNotFound", connect.CodeOf(err))
	}

	// Tenant A starts a live tail on the session
	liveSubA, unsubA := h.broadcaster.Subscribe(testTenantID, sessionID)
	defer unsubA()

	// Tenant B calls DeleteCaptureSession on Tenant A's session -> must return CodeNotFound
	delReq := connect.NewRequest(operatorcapturev1.DeleteCaptureSessionRequest_builder{
		Session: sessionRef,
	}.Build())
	delReq.Header().Set("FlowSeer-Tenant-ID", testTenantB)
	_, err = h.client.DeleteCaptureSession(ctx, delReq)
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("DeleteCaptureSession tenant B got code %v, want CodeNotFound", connect.CodeOf(err))
	}

	// Tenant A's live tail survives Tenant B's delete attempt and receives broadcast chunks
	chunk := modelcapturev1.CapturePacketChunk_builder{
		Session:       sessionRef,
		FirstSequence: proto.Uint64(1),
		Packets:       []*netcapturev1.PacketRecord{testPacketRecord(1, []byte("tail payload"))},
	}.Build()
	h.broadcaster.Broadcast(testTenantID, sessionID, chunk)

	select {
	case item, ok := <-liveSubA.Items():
		if !ok || item.Chunk == nil || item.Chunk.GetFirstSequence() != 1 {
			t.Fatalf("Tenant A live tail did not receive expected chunk: %+v", item)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for Tenant A live tail chunk after Tenant B delete")
	}

	// Tenant A's session still exists
	getA := connect.NewRequest(operatorcapturev1.GetCaptureSessionRequest_builder{
		Session: sessionRef,
	}.Build())
	if _, err := h.client.GetCaptureSession(ctx, getA); err != nil {
		t.Errorf("GetCaptureSession tenant A failed after tenant B delete: %v", err)
	}
}

func TestOperatorService_UnauthenticatedWithoutTenantContext(t *testing.T) {
	h := newOperatorTestHarness(t)
	ctx := context.Background()

	createReq := connect.NewRequest(newTestCreateRequest(10))
	createReq.Header().Set("FlowSeer-Tenant-ID", "none")
	_, err := h.client.CreateCaptureSession(ctx, createReq)
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("CreateCaptureSession without tenant got %v, want CodeUnauthenticated", connect.CodeOf(err))
	}

	listReq := connect.NewRequest(&operatorcapturev1.ListCaptureSessionsRequest{})
	listReq.Header().Set("FlowSeer-Tenant-ID", "none")
	_, err = h.client.ListCaptureSessions(ctx, listReq)
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("ListCaptureSessions without tenant got %v, want CodeUnauthenticated", connect.CodeOf(err))
	}
}

func TestCreateCaptureSessionRefusesForeignTenantEdge(t *testing.T) {
	h := newOperatorTestHarness(t)
	ctx := context.Background()

	// Caller is authenticated under testTenantID (tenant A).
	// testEdge2ID is owned by testTenantB (tenant B).
	req := newTestCreateRequest(100)
	req.SetEdge(edgev1.EdgeGlobalRef_builder{
		Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(testEdge2ID)}.Build(),
	}.Build())

	_, err := h.client.CreateCaptureSession(ctx, connect.NewRequest(req))
	if err == nil {
		t.Fatal("CreateCaptureSession accepted an edge owned by another tenant")
	}
	if got := connect.CodeOf(err); got != connect.CodeNotFound {
		t.Fatalf("code = %v, want CodeNotFound", got)
	}
}

func TestCreateCaptureSessionRefusesUnknownEdge(t *testing.T) {
	h := newOperatorTestHarness(t)
	ctx := context.Background()

	req := newTestCreateRequest(100)
	req.SetEdge(edgev1.EdgeGlobalRef_builder{
		Edge: edgev1.EdgeLocalRef_builder{Id: proto.String("unknown-edge-id")}.Build(),
	}.Build())

	_, err := h.client.CreateCaptureSession(ctx, connect.NewRequest(req))
	if err == nil {
		t.Fatal("CreateCaptureSession accepted an unknown edge")
	}
	if got := connect.CodeOf(err); got != connect.CodeNotFound {
		t.Fatalf("code = %v, want CodeNotFound", got)
	}
}

func TestCreateCaptureSessionStoreFaultUnavailable(t *testing.T) {
	h := newOperatorTestHarness(t)
	ctx := context.Background()

	req := newTestCreateRequest(100)
	req.SetEdge(edgev1.EdgeGlobalRef_builder{
		Edge: edgev1.EdgeLocalRef_builder{Id: proto.String("fault-edge")}.Build(),
	}.Build())

	_, err := h.client.CreateCaptureSession(ctx, connect.NewRequest(req))
	if err == nil {
		t.Fatal("CreateCaptureSession accepted a faulting edge resolver")
	}
	if got := connect.CodeOf(err); got != connect.CodeUnavailable {
		t.Fatalf("code = %v, want CodeUnavailable", got)
	}
}

func TestListCaptureSessionsFilteringAndPagination(t *testing.T) {
	ctx := context.Background()
	h := newOperatorTestHarness(t)

	// Create 3 sessions on testEdge1ID (allowed) and 3 on testEdge2ID (denied).
	// Seed IDs so that they alternate: E1, E2, E1, E2, E1, E2.
	sessionIDs := []string{
		"0192e6a0-0000-7000-8000-000000000001",
		"0192e6a0-0000-7000-8000-000000000002",
		"0192e6a0-0000-7000-8000-000000000003",
		"0192e6a0-0000-7000-8000-000000000004",
		"0192e6a0-0000-7000-8000-000000000005",
		"0192e6a0-0000-7000-8000-000000000006",
	}

	for i, id := range sessionIDs {
		edgeID := testEdge1ID
		if i%2 == 1 {
			edgeID = testEdge2ID
		}
		cfg := newEdgeSessionConfig(t, edgeID, id)
		if _, err := h.store.CreateSession(ctx, testTenantID, cfg); err != nil {
			t.Fatalf("create session %s: %v", id, err)
		}
	}

	// Engine: clear type-wide capture grant, grant capture only on testEdge1ID.
	engine := authztest.New()
	p := testPrincipal()
	engine.Grant("user:"+p.ID, "member", "tenant")
	engine.Grant("tenant:"+testTenantID, "tenant", "edge")
	if err := engine.Write(ctx, []authz.Tuple{
		{Object: "edge:" + testEdge1ID, Relation: "capture", User: "user:" + p.ID},
	}, nil); err != nil {
		t.Fatalf("engine.Write: %v", err)
	}

	// Update harness interceptor and engine
	h.engine = engine
	h.interceptor = authz.NewInterceptor(engine)

	// Page 1: page_size = 2 -> examines s1 (E1, allowed), s2 (E2, denied), s3 (E1, allowed).
	// Page 1 is filled with s1 and s3. Token is s3.
	req1 := operatorcapturev1.ListCaptureSessionsRequest_builder{
		PageSize: proto.Uint32(2),
	}.Build()
	resp1, err := h.client.ListCaptureSessions(ctx, connect.NewRequest(req1))
	if err != nil {
		t.Fatalf("ListCaptureSessions page 1: %v", err)
	}
	if len(resp1.Msg.GetSessions()) != 2 {
		t.Fatalf("page 1 got %d sessions, want 2", len(resp1.Msg.GetSessions()))
	}
	if resp1.Msg.GetSessions()[0].GetConfig().GetRef().GetCaptureSession().GetId() != sessionIDs[0] {
		t.Fatalf("page 1 session 0 = %s, want %s", resp1.Msg.GetSessions()[0].GetConfig().GetRef().GetCaptureSession().GetId(), sessionIDs[0])
	}
	if resp1.Msg.GetSessions()[1].GetConfig().GetRef().GetCaptureSession().GetId() != sessionIDs[2] {
		t.Fatalf("page 1 session 1 = %s, want %s", resp1.Msg.GetSessions()[1].GetConfig().GetRef().GetCaptureSession().GetId(), sessionIDs[2])
	}
	if resp1.Msg.GetNextPageToken() != sessionIDs[2] {
		t.Fatalf("page 1 next_page_token = %q, want %s", resp1.Msg.GetNextPageToken(), sessionIDs[2])
	}

	// Page 2: page_size = 2, page_token = s3 -> examines s4 (E2, denied), s5 (E1, allowed), s6 (E2, denied).
	// Returns s5 and empty next_page_token.
	req2 := operatorcapturev1.ListCaptureSessionsRequest_builder{
		PageSize:  proto.Uint32(2),
		PageToken: proto.String(resp1.Msg.GetNextPageToken()),
	}.Build()
	resp2, err := h.client.ListCaptureSessions(ctx, connect.NewRequest(req2))
	if err != nil {
		t.Fatalf("ListCaptureSessions page 2: %v", err)
	}
	if len(resp2.Msg.GetSessions()) != 1 {
		t.Fatalf("page 2 got %d sessions, want 1", len(resp2.Msg.GetSessions()))
	}
	if resp2.Msg.GetSessions()[0].GetConfig().GetRef().GetCaptureSession().GetId() != sessionIDs[4] {
		t.Fatalf("page 2 session 0 = %s, want %s", resp2.Msg.GetSessions()[0].GetConfig().GetRef().GetCaptureSession().GetId(), sessionIDs[4])
	}
	if resp2.Msg.GetNextPageToken() != "" {
		t.Fatalf("page 2 next_page_token = %q, want empty", resp2.Msg.GetNextPageToken())
	}
}

func TestListCaptureSessions501CandidatesNoneVisible(t *testing.T) {
	ctx := context.Background()
	h := newOperatorTestHarness(t)

	// Create 501 sessions on testEdge2ID
	for i := 0; i < 501; i++ {
		id := fmt.Sprintf("0192e6a0-0000-7000-8000-%012d", i)
		cfg := newEdgeSessionConfig(t, testEdge2ID, id)
		if _, err := h.store.CreateSession(ctx, testTenantID, cfg); err != nil {
			t.Fatalf("create session %s: %v", id, err)
		}
	}

	// Engine has member and tenant on edge, but no capture grant on testEdge2ID
	engine := authztest.New()
	p := testPrincipal()
	engine.Grant("user:"+p.ID, "member", "tenant")
	engine.Grant("tenant:"+testTenantID, "tenant", "edge")

	h.engine = engine
	h.interceptor = authz.NewInterceptor(engine)

	resp, err := h.client.ListCaptureSessions(ctx, connect.NewRequest(operatorcapturev1.ListCaptureSessionsRequest_builder{
		PageSize: proto.Uint32(50),
	}.Build()))
	if err != nil {
		t.Fatalf("ListCaptureSessions: %v", err)
	}
	if len(resp.Msg.GetSessions()) != 0 {
		t.Fatalf("expected 0 sessions, got %d", len(resp.Msg.GetSessions()))
	}
	if resp.Msg.GetNextPageToken() == "" {
		t.Fatal("expected next page token after examining 500 candidates with 1 candidate remaining")
	}

	// Page 2: examines the remaining 1 candidate
	resp2, err := h.client.ListCaptureSessions(ctx, connect.NewRequest(operatorcapturev1.ListCaptureSessionsRequest_builder{
		PageSize:  proto.Uint32(50),
		PageToken: proto.String(resp.Msg.GetNextPageToken()),
	}.Build()))
	if err != nil {
		t.Fatalf("ListCaptureSessions page 2: %v", err)
	}
	if len(resp2.Msg.GetSessions()) != 0 {
		t.Fatalf("expected 0 sessions on page 2, got %d", len(resp2.Msg.GetSessions()))
	}
	if resp2.Msg.GetNextPageToken() != "" {
		t.Fatalf("expected no next page token on page 2, got %q", resp2.Msg.GetNextPageToken())
	}
}

func TestListCaptureSessionsFailingStoreAbandons(t *testing.T) {
	h := newOperatorTestHarness(t)
	ctx := context.Background()
	h.hub.Close() // close the hub so JetStream store calls fail

	_, err := h.client.ListCaptureSessions(ctx, connect.NewRequest(&operatorcapturev1.ListCaptureSessionsRequest{}))
	if err == nil {
		t.Fatal("expected error on closed store")
	}
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("code = %v, want CodeUnavailable", connect.CodeOf(err))
	}
}

func TestCreateCaptureSessionFullPayloadCheck(t *testing.T) {
	ctx := context.Background()
	h := newOperatorTestHarness(t)
	p := testPrincipal()

	// 1. Full payload requested without tenant#full_payload grant -> PermissionDenied
	reqFull := newTestCreateRequest(100)
	reqFull.GetAuthorization().SetFullPayloadRequested(true)
	reqFull.GetAuthorization().SetReason("incident investigation")

	_, err := h.client.CreateCaptureSession(ctx, connect.NewRequest(reqFull))
	if err == nil {
		t.Fatal("expected error for full payload without grant")
	}
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want CodePermissionDenied", connect.CodeOf(err))
	}

	// Verify no session was created in store
	sessions, err := h.store.ListSessions(ctx, testTenantID)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("store holds %d sessions, want 0", len(sessions))
	}

	// 2. Grant full_payload on tenant:<testTenantID>
	queryStart := len(h.engine.Queries())
	if err := h.engine.Write(ctx, []authz.Tuple{
		{Object: "tenant:" + testTenantID, Relation: "full_payload", User: "user:" + p.ID},
	}, nil); err != nil {
		t.Fatalf("engine.Write: %v", err)
	}

	resp, err := h.client.CreateCaptureSession(ctx, connect.NewRequest(reqFull))
	if err != nil {
		t.Fatalf("CreateCaptureSession with full payload grant failed: %v", err)
	}
	if resp.Msg.GetSession() == nil {
		t.Fatal("session is nil in response")
	}

	// Verify query recorded by engine
	queries := h.engine.Queries()[queryStart:]
	var fullPayloadQueried bool
	for _, q := range queries {
		if q.Object == "tenant:"+testTenantID && q.Relation == "full_payload" {
			fullPayloadQueried = true
			break
		}
	}
	if !fullPayloadQueried {
		t.Fatalf("engine recorded queries %v, want tenant:%s#full_payload", queries, testTenantID)
	}

	// 3. Headers-only request succeeds even without full_payload grant
	if err := h.engine.Write(ctx, nil, []authz.Tuple{
		{Object: "tenant:" + testTenantID, Relation: "full_payload", User: "user:" + p.ID},
	}); err != nil {
		t.Fatalf("engine.Write delete: %v", err)
	}

	reqHeadersOnly := newTestCreateRequest(100)
	reqHeadersOnly.GetAuthorization().SetFullPayloadRequested(false)
	reqHeadersOnly.GetAuthorization().SetReason("routine monitoring")

	respHeaders, err := h.client.CreateCaptureSession(ctx, connect.NewRequest(reqHeadersOnly))
	if err != nil {
		t.Fatalf("CreateCaptureSession headers-only failed: %v", err)
	}
	if respHeaders.Msg.GetSession() == nil {
		t.Fatal("session is nil in response")
	}
}

func TestCreateAndDeleteCaptureSessionProjectHook(t *testing.T) {
	ctx := context.Background()
	h := newOperatorTestHarness(t)

	before := h.projectCount.Load()

	// 1. CreateCaptureSession calls project hook once
	req := newTestCreateRequest(100)
	resp, err := h.client.CreateCaptureSession(ctx, connect.NewRequest(req))
	if err != nil {
		t.Fatalf("CreateCaptureSession: %v", err)
	}
	if got := h.projectCount.Load(); got != before+1 {
		t.Fatalf("projectCount after create = %d, want %d", got, before+1)
	}

	sessionRef := resp.Msg.GetSession().GetConfig().GetRef()

	// 2. DeleteCaptureSession calls project hook once
	delReq := operatorcapturev1.DeleteCaptureSessionRequest_builder{
		Session: sessionRef,
	}.Build()
	if _, err := h.client.DeleteCaptureSession(ctx, connect.NewRequest(delReq)); err != nil {
		t.Fatalf("DeleteCaptureSession: %v", err)
	}
	if got := h.projectCount.Load(); got != before+2 {
		t.Fatalf("projectCount after delete = %d, want %d", got, before+2)
	}

	// 3. Failed store write or invalid call does NOT call project hook
	delFailed := operatorcapturev1.DeleteCaptureSessionRequest_builder{
		Session: sessionRef, // already deleted
	}.Build()
	_, _ = h.client.DeleteCaptureSession(ctx, connect.NewRequest(delFailed))
	if got := h.projectCount.Load(); got != before+2 {
		t.Fatalf("projectCount after failed delete = %d, want %d", got, before+2)
	}
}
