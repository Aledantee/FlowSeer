package captureapi_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
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
	netcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
	"go.aledante.io/FlowSeer/src/common/spawn"
	"go.aledante.io/FlowSeer/src/services/device/internal/captureapi"
)

type operatorTestHarness struct {
	store       *captureapi.Store
	broadcaster *captureapi.Broadcaster
	notifyCount atomic.Int64
	client      capturev1connect.CaptureServiceClient
	server      *httptest.Server
	frozenClock time.Time
}

func newOperatorTestHarness(t *testing.T) *operatorTestHarness {
	t.Helper()
	store := newTestStore(t)
	broadcaster := captureapi.NewBroadcaster()
	frozen := time.Date(2026, 9, 18, 14, 0, 0, 0, time.UTC)

	h := &operatorTestHarness{
		store:       store,
		broadcaster: broadcaster,
		frozenClock: frozen,
	}

	svc := captureapi.NewOperatorService(store, broadcaster, captureapi.OperatorServiceConfig{
		NotifyChange: func() {
			h.notifyCount.Add(1)
		},
		Clock: func() time.Time {
			return h.frozenClock
		},
	})

	path, handler := capturev1connect.NewCaptureServiceHandler(svc)
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
			Operator:             proto.String("alice"),
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
			t.Fatalf("expected CodeInvalidArgument for missing edge, got: %v", err)
		}
	}

	// Missing source
	{
		req := newTestCreateRequest(100)
		req.SetSource(nil)
		_, err := h.client.CreateCaptureSession(ctx, connect.NewRequest(req))
		if err == nil || connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("expected CodeInvalidArgument for missing source, got: %v", err)
		}
	}

	// Missing authorization
	{
		req := newTestCreateRequest(100)
		req.SetAuthorization(nil)
		_, err := h.client.CreateCaptureSession(ctx, connect.NewRequest(req))
		if err == nil || connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("expected CodeInvalidArgument for missing authorization, got: %v", err)
		}
	}

	// Missing budget
	{
		req := newTestCreateRequest(0)
		_, err := h.client.CreateCaptureSession(ctx, connect.NewRequest(req))
		if err == nil || connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("expected CodeInvalidArgument for missing budget, got: %v", err)
		}
	}

	// Unbounded budget (empty budget fields)
	{
		req := newTestCreateRequest(0)
		req.SetBudget(&modelcapturev1.CaptureBudget{})
		_, err := h.client.CreateCaptureSession(ctx, connect.NewRequest(req))
		if err == nil || connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("expected CodeInvalidArgument for empty budget, got: %v", err)
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
			t.Fatalf("expected CodeInvalidArgument for zero bounds, got: %v", err)
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
		t.Fatalf("expected PENDING lifecycle, got: %v", session.GetState().GetLifecycle())
	}
	if session.GetConfig().GetBudget().GetMaxPackets() != 150 {
		t.Fatalf("expected max_packets 150, got: %d", session.GetConfig().GetBudget().GetMaxPackets())
	}

	// Verify persistence in store
	stored, _, err := h.store.GetSession(ctx, sessionID)
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
			t.Fatalf("expected CodeInvalidArgument for missing session ref, got: %v", err)
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
			t.Fatalf("expected CodeNotFound for unknown session, got: %v", err)
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
		t.Fatalf("expected CANCELED lifecycle, got: %v", stopResp.Msg.GetSession().GetState().GetLifecycle())
	}
	if stopResp.Msg.GetSession().GetState().GetStopReason() != modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_OPERATOR {
		t.Fatalf("expected CAPTURE_STOP_REASON_OPERATOR, got: %v", stopResp.Msg.GetSession().GetState().GetStopReason())
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
		t.Fatalf("expected CANCELED lifecycle on re-stop, got: %v", secondStopResp.Msg.GetSession().GetState().GetLifecycle())
	}

	// Stop a RUNNING session
	runningConfig := newEdgeSessionConfig(testEdge1ID, "0192e6a0-0000-7000-8000-000000000033")
	if _, err := h.store.CreateSession(ctx, runningConfig); err != nil {
		t.Fatalf("create running session in store: %v", err)
	}
	// Transition to RUNNING
	if _, err := h.store.MutateSession(ctx, "0192e6a0-0000-7000-8000-000000000033", func(rec *modelcapturev1.CaptureSessionRecord) error {
		rec.GetState().SetLifecycle(modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_RUNNING)
		rec.GetState().SetStartedAt(timestamppb.New(h.frozenClock))
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
		t.Fatalf("expected CANCELED lifecycle, got: %v", runningStopResp.Msg.GetSession().GetState().GetLifecycle())
	}
	if runningStopResp.Msg.GetSession().GetState().GetStopReason() != modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_OPERATOR {
		t.Fatalf("expected CAPTURE_STOP_REASON_OPERATOR, got: %v", runningStopResp.Msg.GetSession().GetState().GetStopReason())
	}

	// Stopping COMPLETED session is a no-op
	if _, err := h.store.MutateSession(ctx, pendingID, func(rec *modelcapturev1.CaptureSessionRecord) error {
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
		t.Fatalf("expected session to remain COMPLETED, got: %v", completedStopResp.Msg.GetSession().GetState().GetLifecycle())
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
			t.Fatalf("expected CodeInvalidArgument for missing session id, got: %v", err)
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
			t.Fatalf("expected CodeNotFound for unknown session, got: %v", err)
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
	if err := h.store.AppendPackets(ctx, sessionID, netcapturev1.LinkType_LINK_TYPE_ETHERNET, 65535, packets); err != nil {
		t.Fatalf("append packets: %v", err)
	}
	if !h.store.ArtifactExists(sessionID) {
		t.Fatal("expected artifact to exist on disk")
	}

	// Register a broadcaster tail subscriber to test cleanup on delete
	subCh, unsub := h.broadcaster.Subscribe(sessionID)
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
	case _, ok := <-subCh:
		if ok {
			t.Fatal("expected subscriber channel to be closed upon session deletion")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for subscriber channel close")
	}

	// Artifact file must be unlinked
	if h.store.ArtifactExists(sessionID) {
		t.Fatal("expected artifact file to be unlinked after delete")
	}

	// Subsequent Get must return CodeNotFound
	_, err = h.client.GetCaptureSession(ctx, connect.NewRequest(getReq))
	if err == nil || connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("expected CodeNotFound after deletion, got: %v", err)
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
		cfg := newEdgeSessionConfig(testEdge1ID, id)
		if _, err := h.store.CreateSession(ctx, cfg); err != nil {
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
		t.Fatalf("expected 2 sessions on page 1, got: %d", len(resp1.Msg.GetSessions()))
	}
	if resp1.Msg.GetSessions()[0].GetConfig().GetRef().GetCaptureSession().GetId() != sessionIDs[0] {
		t.Fatalf("expected session %s, got: %s", sessionIDs[0], resp1.Msg.GetSessions()[0].GetConfig().GetRef().GetCaptureSession().GetId())
	}
	if resp1.Msg.GetSessions()[1].GetConfig().GetRef().GetCaptureSession().GetId() != sessionIDs[1] {
		t.Fatalf("expected session %s, got: %s", sessionIDs[1], resp1.Msg.GetSessions()[1].GetConfig().GetRef().GetCaptureSession().GetId())
	}
	if resp1.Msg.GetNextPageToken() != sessionIDs[1] {
		t.Fatalf("expected next_page_token %s, got: %s", sessionIDs[1], resp1.Msg.GetNextPageToken())
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
		t.Fatalf("expected 2 sessions on page 2, got: %d", len(resp2.Msg.GetSessions()))
	}
	if resp2.Msg.GetSessions()[0].GetConfig().GetRef().GetCaptureSession().GetId() != sessionIDs[2] {
		t.Fatalf("expected session %s, got: %s", sessionIDs[2], resp2.Msg.GetSessions()[0].GetConfig().GetRef().GetCaptureSession().GetId())
	}
	if resp2.Msg.GetSessions()[1].GetConfig().GetRef().GetCaptureSession().GetId() != sessionIDs[3] {
		t.Fatalf("expected session %s, got: %s", sessionIDs[3], resp2.Msg.GetSessions()[1].GetConfig().GetRef().GetCaptureSession().GetId())
	}
	if resp2.Msg.GetNextPageToken() != sessionIDs[3] {
		t.Fatalf("expected next_page_token %s, got: %s", sessionIDs[3], resp2.Msg.GetNextPageToken())
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
		t.Fatalf("expected 1 session on page 3, got: %d", len(resp3.Msg.GetSessions()))
	}
	if resp3.Msg.GetSessions()[0].GetConfig().GetRef().GetCaptureSession().GetId() != sessionIDs[4] {
		t.Fatalf("expected session %s, got: %s", sessionIDs[4], resp3.Msg.GetSessions()[0].GetConfig().GetRef().GetCaptureSession().GetId())
	}
	if resp3.Msg.GetNextPageToken() != "" {
		t.Fatalf("expected empty next_page_token on last page, got: %s", resp3.Msg.GetNextPageToken())
	}
}

func TestTailCaptureSession_LiveStreaming(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newOperatorTestHarness(t)
	sessID := "0192e6a0-0000-7000-8000-000000000044"
	cfg := newEdgeSessionConfig(testEdge1ID, sessID)

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
			t.Fatalf("expected CodeNotFound for unknown session tail, got: %v", stream.Err())
		}
		_ = stream.Close()
	}

	if _, err := h.store.CreateSession(ctx, cfg); err != nil {
		t.Fatalf("create session: %v", err)
	}

	counters1 := netcapturev1.CaptureCounters_builder{
		Received: proto.Uint64(1),
		Accepted: proto.Uint64(1),
	}.Build()
	counters2 := netcapturev1.CaptureCounters_builder{
		Received: proto.Uint64(2),
		Accepted: proto.Uint64(2),
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
		Counters: counters2,
		Final:    proto.Bool(true),
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
		for h.broadcaster.SubscriberCount(sessID) == 0 {
			if time.Now().After(deadline) {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		h.broadcaster.Broadcast(sessID, chunk1)
		time.Sleep(20 * time.Millisecond)
		h.broadcaster.Broadcast(sessID, chunk2)
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
		t.Fatalf("expected the tail to open with its attached marker, got: %+v", stream.Msg())
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
	if gotChunk1.GetFirstSequence() != 1 || gotChunk1.GetCounters().GetAccepted() != 1 {
		t.Fatalf("chunk 1 lost its sequence or counters on the way through: %+v", gotChunk1)
	}

	if !stream.Receive() {
		t.Fatalf("expected chunk 2 from tail, stream ended: %v", stream.Err())
	}
	gotChunk2 := stream.Msg().GetChunk()
	if !gotChunk2.GetFinal() {
		t.Fatal("expected chunk 2 to be final")
	}
	if gotChunk2.GetFirstSequence() != 2 || gotChunk2.GetCounters().GetAccepted() != 2 {
		t.Fatalf("chunk 2 lost its sequence or counters on the way through: %+v", gotChunk2)
	}

	// Stream should complete cleanly after final chunk
	if stream.Receive() {
		t.Fatal("expected stream to terminate after final chunk")
	}
	if stream.Err() != nil {
		t.Fatalf("expected clean stream termination, got error: %v", stream.Err())
	}
}

func TestDownloadCaptureSession_ChunkedAndNotFoundOnExpired(t *testing.T) {
	ctx := context.Background()
	h := newOperatorTestHarness(t)

	sessID := "0192e6a0-0000-7000-8000-000000000055"
	cfg := newEdgeSessionConfig(testEdge1ID, sessID)

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
			t.Fatalf("expected CodeNotFound, got: %v", stream.Err())
		}
		_ = stream.Close()
	}

	if _, err := h.store.CreateSession(ctx, cfg); err != nil {
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
			t.Fatalf("expected CodeFailedPrecondition while the capture is unfinished, got: %v", stream.Err())
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

	if err := h.store.AppendPackets(ctx, sessID, netcapturev1.LinkType_LINK_TYPE_ETHERNET, 65535, packets); err != nil {
		t.Fatalf("append large packets: %v", err)
	}

	counters := netcapturev1.CaptureCounters_builder{
		Received: proto.Uint64(packetCount),
		Accepted: proto.Uint64(packetCount),
	}.Build()

	artifact, err := h.store.FinalizeArtifact(ctx, sessID, netcapturev1.LinkType_LINK_TYPE_ETHERNET, 65535, counters, h.frozenClock.Add(time.Hour))
	if err != nil {
		t.Fatalf("finalize artifact: %v", err)
	}
	if artifact == nil {
		t.Fatal("expected non-nil artifact after finalization")
	}
	if _, err := h.store.MutateSession(ctx, sessID, func(r *modelcapturev1.CaptureSessionRecord) error {
		r.GetState().SetLifecycle(modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_COMPLETED)
		r.GetState().SetStopReason(modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_PACKET_COUNT)
		r.GetState().SetCounters(counters)
		r.GetState().SetArtifact(artifact)
		return nil
	}); err != nil {
		t.Fatalf("store artifact on session: %v", err)
	}
	if artifact.GetByteSize() <= 2*1024*1024 {
		t.Fatalf("expected artifact size > 2MB, got: %d", artifact.GetByteSize())
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
		t.Fatalf("expected at least 3 chunks for >2.5MB payload, got: %d", downloadedChunks)
	}
	if uint64(totalBytes) != artifact.GetByteSize() {
		t.Fatalf("downloaded bytes mismatch: got %d, want %d", totalBytes, artifact.GetByteSize())
	}
	if !bytes.Equal(hasher.Sum(nil), artifact.GetDigest()) {
		t.Fatalf("downloaded sha256 digest mismatch")
	}

	// Move both clocks past the artifact's expiry and sweep.
	expired := h.frozenClock.Add(2 * time.Hour)
	h.frozenClock = expired
	h.store.SetClock(func() time.Time { return expired })

	removed, err := h.store.SweepExpired(ctx)
	if err != nil {
		t.Fatalf("sweep expired: %v", err)
	}
	if removed != 1 {
		t.Fatalf("expected one artifact purged, got: %d", removed)
	}
	if h.store.ArtifactExists(sessID) {
		t.Fatal("expected artifact to be swept from disk")
	}

	// What the retention departure keeps: the record, its counters, and the
	// descriptor of what was captured, now stamped with when it was purged.
	afterSweep, _, err := h.store.GetSession(ctx, sessID)
	if err != nil {
		t.Fatalf("get session after sweep: %v", err)
	}
	if got := afterSweep.GetState().GetLifecycle(); got != modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_COMPLETED {
		t.Fatalf("expected the session record to survive the sweep as COMPLETED, got: %v", got)
	}
	if got := afterSweep.GetState().GetCounters().GetAccepted(); got != packetCount {
		t.Fatalf("expected counters to survive the sweep with %d accepted, got: %d", packetCount, got)
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
		t.Fatalf("expected the second sweep to purge nothing, got: %d", again)
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
		t.Fatalf("expected CodeNotFound for swept artifact, got: %v", stream2.Err())
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
	cfg := newEdgeSessionConfig(testEdge1ID, sessID)
	if _, err := h.store.CreateSession(ctx, cfg); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := h.store.MutateSession(ctx, sessID, func(r *modelcapturev1.CaptureSessionRecord) error {
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
		t.Fatalf("expected the tail to end cleanly, got: %v", stream.Err())
	}
	if tailCtx.Err() != nil {
		t.Fatal("the tail blocked until its deadline instead of ending with the session")
	}
	if got := h.broadcaster.SubscriberCount(sessID); got != 0 {
		t.Fatalf("expected the tail's subscription to be released, got %d subscribers", got)
	}
}
