package captureapi_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	captureedgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/capture/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/capture/v1/capturev1connect"
	modelcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	netcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
	"go.aledante.io/FlowSeer/src/services/device/internal/captureapi"
	"go.aledante.io/FlowSeer/src/services/device/internal/edge"
)

const (
	testEdge1ID  = "0192e6a0-0000-7000-8000-000000000001"
	testEdge2ID  = "0192e6a0-0000-7000-8000-000000000002"
	testAudience = "flowseer-central"
)

type testHarness struct {
	store       *captureapi.Store
	broadcaster *captureapi.Broadcaster
	pubKey1     ed25519.PublicKey
	privKey1    ed25519.PrivateKey
	pubKey2     ed25519.PublicKey
	privKey2    ed25519.PrivateKey
}

func newTestHarness(t *testing.T) *testHarness {
	t.Helper()
	store := newTestStore(t)
	broadcaster := captureapi.NewBroadcaster()

	pub1, priv1, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key 1: %v", err)
	}
	pub2, priv2, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key 2: %v", err)
	}

	return &testHarness{
		store:       store,
		broadcaster: broadcaster,
		pubKey1:     pub1,
		privKey1:    priv1,
		pubKey2:     pub2,
		privKey2:    priv2,
	}
}

func (h *testHarness) newVerifier() *edge.Verifier {
	return edge.NewVerifier(testAudience, time.Minute, func(_ context.Context, id string) (ed25519.PublicKey, edgev1.EdgeLifecycle, error) {
		switch id {
		case testEdge1ID:
			return h.pubKey1, edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED, nil
		case testEdge2ID:
			return h.pubKey2, edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED, nil
		default:
			return nil, edgev1.EdgeLifecycle_EDGE_LIFECYCLE_UNSPECIFIED, nil
		}
	})
}

func (h *testHarness) signAssertion(t *testing.T, edgeID string, privKey ed25519.PrivateKey) *edgev1.SignedEdgeAssertion {
	t.Helper()
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatalf("rand nonce: %v", err)
	}

	bodyHash := sha256.Sum256(nil)
	now := time.Now()
	a := edgev1.EdgeAssertion_builder{
		Edge: edgev1.EdgeGlobalRef_builder{
			Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(edgeID)}.Build(),
		}.Build(),
		Audience:   proto.String(testAudience),
		IssuedAt:   timestamppb.New(now),
		ExpiresAt:  timestamppb.New(now.Add(30 * time.Second)),
		Nonce:      nonce,
		Procedure:  proto.String(capturev1connect.CaptureEdgeServiceUploadCaptureProcedure),
		BodySha256: bodyHash[:],
	}.Build()

	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(a)
	if err != nil {
		t.Fatalf("marshal assertion: %v", err)
	}
	return edgev1.SignedEdgeAssertion_builder{
		Payload:   payload,
		Signature: ed25519.Sign(privKey, payload),
	}.Build()
}

func newEdgeSessionConfig(edgeID, sessionID string) *modelcapturev1.CaptureSessionConfig {
	return modelcapturev1.CaptureSessionConfig_builder{
		Ref: modelcapturev1.CaptureSessionGlobalRef_builder{
			Edge: edgev1.EdgeGlobalRef_builder{
				Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(edgeID)}.Build(),
			}.Build(),
			CaptureSession: modelcapturev1.CaptureSessionLocalRef_builder{
				Id: proto.String(sessionID),
			}.Build(),
		}.Build(),
		Name:        proto.String("session-" + sessionID),
		Description: proto.String("test session"),
		Source: modelcapturev1.CaptureSource_builder{
			LocalInterface: modelcapturev1.LocalInterfaceSource_builder{
				InterfaceName: proto.String("eth0"),
				Promiscuous:   proto.Bool(true),
			}.Build(),
		}.Build(),
		Budget: modelcapturev1.CaptureBudget_builder{
			MaxPackets: proto.Uint64(10),
		}.Build(),
		Authorization: modelcapturev1.CaptureAuthorization_builder{
			Operator:             proto.String("alice"),
			Reason:               proto.String("investigation"),
			FullPayloadRequested: proto.Bool(false),
		}.Build(),
	}.Build()
}

func testPacketRecord(seq uint64, data []byte) *netcapturev1.PacketRecord {
	return netcapturev1.PacketRecord_builder{
		Sequence:       proto.Uint64(seq),
		CapturedAt:     timestamppb.Now(),
		OriginalLength: proto.Uint32(uint32(len(data))),
		Data:           data,
	}.Build()
}

func TestSubscribeCaptureAssignments_EdgeIsolation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newTestHarness(t)

	sess1 := "0192e6a0-0000-7000-8000-000000000022"
	sess2 := "0192e6a0-0000-7000-8000-000000000011"

	cfg1 := newEdgeSessionConfig(testEdge1ID, sess1)
	cfg2 := newEdgeSessionConfig(testEdge2ID, sess2)

	if _, err := h.store.CreateSession(ctx, cfg1); err != nil {
		t.Fatalf("create session 1: %v", err)
	}
	if _, err := h.store.CreateSession(ctx, cfg2); err != nil {
		t.Fatalf("create session 2: %v", err)
	}

	edgeSvc := captureapi.NewEdgeService(h.store, h.newVerifier(), h.broadcaster, captureapi.EdgeServiceConfig{
		EdgeID: func(context.Context) (string, error) {
			return testEdge1ID, nil
		},
		ResendInterval: 10 * time.Minute,
	})

	path, handler := capturev1connect.NewCaptureEdgeServiceHandler(edgeSvc)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := capturev1connect.NewCaptureEdgeServiceClient(srv.Client(), srv.URL)

	stream, err := client.SubscribeCaptureAssignments(ctx, connect.NewRequest(&captureedgev1.SubscribeCaptureAssignmentsRequest{}))
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = stream.Close() }()

	if !stream.Receive() {
		t.Fatalf("expected assignment, stream closed: %v", stream.Err())
	}

	msg := stream.Msg()
	start := msg.GetStart()
	if start == nil {
		t.Fatalf("expected start assignment, got: %+v", msg)
	}

	gotSessionID := start.GetRef().GetCaptureSession().GetId()
	if gotSessionID != sess1 {
		t.Fatalf("expected session %s for edge-1, got: %s", sess1, gotSessionID)
	}
	if gotEdgeID := start.GetRef().GetEdge().GetEdge().GetId(); gotEdgeID != testEdge1ID {
		t.Fatalf("expected edge %s, got: %s", testEdge1ID, gotEdgeID)
	}
}

func TestUploadCapture_WithdrawsOwedStartOnFirstChunk(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newTestHarness(t)
	sessID := "0192e6a0-0000-7000-8000-000000000012"
	cfg := newEdgeSessionConfig(testEdge1ID, sessID)

	if _, err := h.store.CreateSession(ctx, cfg); err != nil {
		t.Fatalf("create session: %v", err)
	}

	edgeSvc := captureapi.NewEdgeService(h.store, h.newVerifier(), h.broadcaster, captureapi.EdgeServiceConfig{
		EdgeID: func(context.Context) (string, error) {
			return testEdge1ID, nil
		},
		ResendInterval: 10 * time.Minute,
	})

	path, handler := capturev1connect.NewCaptureEdgeServiceHandler(edgeSvc)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := capturev1connect.NewCaptureEdgeServiceClient(srv.Client(), srv.URL)

	// Step 1: Initial subscribe receives start assignment for pending session
	subStream1, err := client.SubscribeCaptureAssignments(ctx, connect.NewRequest(&captureedgev1.SubscribeCaptureAssignmentsRequest{}))
	if err != nil {
		t.Fatalf("subscribe 1: %v", err)
	}
	if !subStream1.Receive() {
		t.Fatalf("expected assignment on open, stream closed: %v", subStream1.Err())
	}
	if gotID := subStream1.Msg().GetStart().GetRef().GetCaptureSession().GetId(); gotID != sessID {
		t.Fatalf("expected assignment for %s, got: %s", sessID, gotID)
	}
	_ = subStream1.Close()

	// Step 2: Open upload stream and deliver first chunk
	uploadCtx, uploadCancel := context.WithCancel(ctx)
	defer uploadCancel()
	uploadStream := client.UploadCapture(uploadCtx)

	// Send opening assertion
	openingSigned := h.signAssertion(t, testEdge1ID, h.privKey1)
	if err := uploadStream.Send(captureedgev1.UploadCaptureRequest_builder{Assertion: openingSigned}.Build()); err != nil {
		t.Fatalf("send opening assertion: %v", err)
	}

	// Send first chunk
	chunk := modelcapturev1.CapturePacketChunk_builder{
		Session:       cfg.GetRef(),
		FirstSequence: proto.Uint64(1),
		Packets: []*netcapturev1.PacketRecord{
			testPacketRecord(1, []byte("\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x08\x00")),
		},
	}.Build()

	if err := uploadStream.Send(captureedgev1.UploadCaptureRequest_builder{Chunk: chunk}.Build()); err != nil {
		t.Fatalf("send first chunk: %v", err)
	}

	// Wait for session state to transition to RUNNING in store
	var running bool
	for range 50 {
		rec, _, err := h.store.GetSession(ctx, sessID)
		if err == nil && rec != nil && rec.GetState().GetLifecycle() == modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_RUNNING {
			running = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !running {
		rec, _, _ := h.store.GetSession(ctx, sessID)
		t.Fatalf("expected lifecycle RUNNING, got: %v", rec.GetState().GetLifecycle())
	}

	// Step 3: Create second pending session for edge-1
	sessID2 := "0192e6a0-0000-7000-8000-000000000022"
	cfg2 := newEdgeSessionConfig(testEdge1ID, sessID2)
	if _, err := h.store.CreateSession(ctx, cfg2); err != nil {
		t.Fatalf("create session 2: %v", err)
	}

	// Reconnect subscribe stream; should receive assignment for sessID2, and NOT sessID
	subStream2, err := client.SubscribeCaptureAssignments(ctx, connect.NewRequest(&captureedgev1.SubscribeCaptureAssignmentsRequest{}))
	if err != nil {
		t.Fatalf("subscribe 2: %v", err)
	}
	defer func() { _ = subStream2.Close() }()

	if !subStream2.Receive() {
		t.Fatalf("expected assignment for session 2, stream closed: %v", subStream2.Err())
	}
	gotID := subStream2.Msg().GetStart().GetRef().GetCaptureSession().GetId()
	if gotID == sessID {
		t.Fatalf("expected sessID %s to be withdrawn, but it was re-sent", sessID)
	}
	if gotID != sessID2 {
		t.Fatalf("expected sessID2 %s, got: %s", sessID2, gotID)
	}
}

func TestUploadCapture_RejectsForeignEdge(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newTestHarness(t)
	sessID := "0192e6a0-0000-7000-8000-000000000013"
	cfg1 := newEdgeSessionConfig(testEdge1ID, sessID)

	if _, err := h.store.CreateSession(ctx, cfg1); err != nil {
		t.Fatalf("create session 1: %v", err)
	}

	edgeSvc := captureapi.NewEdgeService(h.store, h.newVerifier(), h.broadcaster, captureapi.EdgeServiceConfig{})

	path, handler := capturev1connect.NewCaptureEdgeServiceHandler(edgeSvc)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := capturev1connect.NewCaptureEdgeServiceClient(srv.Client(), srv.URL)

	uploadStream := client.UploadCapture(ctx)

	// Authenticate stream as edge-2
	openingSigned := h.signAssertion(t, testEdge2ID, h.privKey2)
	if err := uploadStream.Send(captureedgev1.UploadCaptureRequest_builder{Assertion: openingSigned}.Build()); err != nil {
		t.Fatalf("send opening assertion: %v", err)
	}

	// Upload chunk claiming edge-1's session
	foreignChunk := modelcapturev1.CapturePacketChunk_builder{
		Session:       cfg1.GetRef(),
		FirstSequence: proto.Uint64(1),
		Packets: []*netcapturev1.PacketRecord{
			testPacketRecord(1, []byte("foreign packet")),
		},
	}.Build()

	_ = uploadStream.Send(captureedgev1.UploadCaptureRequest_builder{Chunk: foreignChunk}.Build())
	_, err := uploadStream.CloseAndReceive()
	if err == nil {
		t.Fatal("expected PermissionDenied error, got nil")
	}

	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("expected connect.Error, got: %v", err)
	}
	if connectErr.Code() != connect.CodePermissionDenied {
		t.Fatalf("expected CodePermissionDenied, got: %v", connectErr.Code())
	}

	// Verify session for edge-1 is still PENDING
	rec, _, err := h.store.GetSession(ctx, sessID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if rec.GetState().GetLifecycle() != modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_PENDING {
		t.Fatalf("expected session to remain PENDING, got: %v", rec.GetState().GetLifecycle())
	}
}

func TestUploadCapture_ReAssertionLapseTerminatesStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newTestHarness(t)
	sessID := "0192e6a0-0000-7000-8000-000000000014"
	cfg := newEdgeSessionConfig(testEdge1ID, sessID)

	if _, err := h.store.CreateSession(ctx, cfg); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Short assertion window of 50ms for the test
	edgeSvc := captureapi.NewEdgeService(h.store, h.newVerifier(), h.broadcaster, captureapi.EdgeServiceConfig{
		AssertionWindow: 50 * time.Millisecond,
	})

	path, handler := capturev1connect.NewCaptureEdgeServiceHandler(edgeSvc)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := capturev1connect.NewCaptureEdgeServiceClient(srv.Client(), srv.URL)

	uploadStream := client.UploadCapture(ctx)

	// Send opening assertion
	openingSigned := h.signAssertion(t, testEdge1ID, h.privKey1)
	if err := uploadStream.Send(captureedgev1.UploadCaptureRequest_builder{Assertion: openingSigned}.Build()); err != nil {
		t.Fatalf("send opening assertion: %v", err)
	}

	// Send first chunk
	chunk1 := modelcapturev1.CapturePacketChunk_builder{
		Session:       cfg.GetRef(),
		FirstSequence: proto.Uint64(1),
		Packets: []*netcapturev1.PacketRecord{
			testPacketRecord(1, []byte("packet 1")),
		},
	}.Build()

	if err := uploadStream.Send(captureedgev1.UploadCaptureRequest_builder{Chunk: chunk1}.Build()); err != nil {
		t.Fatalf("send chunk 1: %v", err)
	}

	// Sleep past the 50ms assertion window without sending a re-assertion
	time.Sleep(80 * time.Millisecond)

	// Send chunk 2 after window lapsed
	chunk2 := modelcapturev1.CapturePacketChunk_builder{
		Session:       cfg.GetRef(),
		FirstSequence: proto.Uint64(2),
		Packets: []*netcapturev1.PacketRecord{
			testPacketRecord(2, []byte("packet 2")),
		},
	}.Build()

	_ = uploadStream.Send(captureedgev1.UploadCaptureRequest_builder{Chunk: chunk2}.Build())
	_, err := uploadStream.CloseAndReceive()
	if err == nil {
		t.Fatal("expected Unauthenticated error, got nil")
	}

	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("expected connect.Error, got: %v", err)
	}
	if connectErr.Code() != connect.CodeUnauthenticated {
		t.Fatalf("expected CodeUnauthenticated, got: %v", connectErr.Code())
	}

	// Verify session marked FAILED with STOP_REASON_ERROR
	rec, _, err := h.store.GetSession(ctx, sessID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if rec.GetState().GetLifecycle() != modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_FAILED {
		t.Fatalf("expected lifecycle FAILED, got: %v", rec.GetState().GetLifecycle())
	}
	if rec.GetState().GetStopReason() != modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_ERROR {
		t.Fatalf("expected stop reason ERROR, got: %v", rec.GetState().GetStopReason())
	}
}

func TestUploadCapture_FinalizationOnStreamCompletion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newTestHarness(t)
	sessID := "0192e6a0-0000-7000-8000-000000000015"
	cfg := newEdgeSessionConfig(testEdge1ID, sessID)

	if _, err := h.store.CreateSession(ctx, cfg); err != nil {
		t.Fatalf("create session: %v", err)
	}

	edgeSvc := captureapi.NewEdgeService(h.store, h.newVerifier(), h.broadcaster, captureapi.EdgeServiceConfig{})

	path, handler := capturev1connect.NewCaptureEdgeServiceHandler(edgeSvc)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := capturev1connect.NewCaptureEdgeServiceClient(srv.Client(), srv.URL)

	uploadStream := client.UploadCapture(ctx)

	// Send opening assertion
	openingSigned := h.signAssertion(t, testEdge1ID, h.privKey1)
	if err := uploadStream.Send(captureedgev1.UploadCaptureRequest_builder{Assertion: openingSigned}.Build()); err != nil {
		t.Fatalf("send opening assertion: %v", err)
	}

	// Send chunk with 5 packets
	packets := make([]*netcapturev1.PacketRecord, 5)
	for i := range packets {
		packets[i] = testPacketRecord(uint64(i+1), []byte("\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x08\x00payload"))
	}

	counters := netcapturev1.CaptureCounters_builder{
		Received: proto.Uint64(5),
		Accepted: proto.Uint64(5),
	}.Build()

	finalChunk := modelcapturev1.CapturePacketChunk_builder{
		Session:       cfg.GetRef(),
		FirstSequence: proto.Uint64(1),
		Packets:       packets,
		Counters:      counters,
		Final:         proto.Bool(true),
	}.Build()

	if err := uploadStream.Send(captureedgev1.UploadCaptureRequest_builder{Chunk: finalChunk}.Build()); err != nil {
		t.Fatalf("send final chunk: %v", err)
	}

	resp, err := uploadStream.CloseAndReceive()
	if err != nil {
		t.Fatalf("close and receive: %v", err)
	}

	if resp.Msg.GetSession().GetCaptureSession().GetId() != sessID {
		t.Fatalf("expected response session %s, got: %s", sessID, resp.Msg.GetSession().GetCaptureSession().GetId())
	}

	// Verify session state in store
	rec, _, err := h.store.GetSession(ctx, sessID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}

	if rec.GetState().GetLifecycle() != modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_COMPLETED {
		t.Fatalf("expected lifecycle COMPLETED, got: %v", rec.GetState().GetLifecycle())
	}
	if rec.GetState().GetStopReason() == modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_UNSPECIFIED {
		t.Fatal("expected non-unspecified stop reason")
	}

	artifact := rec.GetState().GetArtifact()
	if artifact == nil {
		t.Fatal("expected artifact to be present")
	}
	if artifact.GetPacketCount() != 5 {
		t.Fatalf("expected 5 packets in artifact, got: %d", artifact.GetPacketCount())
	}
	if len(artifact.GetDigest()) != 32 {
		t.Fatalf("expected 32-byte SHA-256 digest, got len %d", len(artifact.GetDigest()))
	}
	if !h.store.ArtifactExists(sessID) {
		t.Fatalf("expected artifact file on disk for session %s", sessID)
	}
}
