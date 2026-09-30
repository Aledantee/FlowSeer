package captureapi_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	captureedgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/capture/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/capture/v1/capturev1connect"
	modelcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	principalv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/principal/v1"
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

func newEdgeSessionConfig(t *testing.T, edgeID, sessionID string) *modelcapturev1.CaptureSessionConfig {
	t.Helper()
	cfg := modelcapturev1.CaptureSessionConfig_builder{
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
			RequestedBy:          principalv1.OperatorRef_builder{Subject: proto.String("zitadel|usr_123")}.Build(),
			Reason:               proto.String("investigation"),
			FullPayloadRequested: proto.Bool(false),
		}.Build(),
	}.Build()
	if err := protovalidate.Validate(cfg); err != nil {
		t.Fatalf("validate capture session fixture: %v", err)
	}
	return cfg
}

func testPacketRecord(seq uint64, data []byte) *netcapturev1.PacketRecord {
	return netcapturev1.PacketRecord_builder{
		Sequence:       proto.Uint64(seq),
		CapturedAt:     timestamppb.Now(),
		OriginalLength: proto.Uint32(uint32(len(data))),
		Data:           data,
	}.Build()
}

// tailTestSessionRef is a valid capture session ref for broadcaster fixtures.
// The broadcaster routes by the session argument, not by the chunk's own ref,
// but a fixture is still a claim the wire would carry the message, so it names
// a session the schema accepts.
func tailTestSessionRef() *modelcapturev1.CaptureSessionGlobalRef {
	return modelcapturev1.CaptureSessionGlobalRef_builder{
		Edge: edgev1.EdgeGlobalRef_builder{
			Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(testEdge1ID)}.Build(),
		}.Build(),
		CaptureSession: modelcapturev1.CaptureSessionLocalRef_builder{
			Id: proto.String("0192e6a0-0000-7000-8000-0000000000b0"),
		}.Build(),
	}.Build()
}

// tailTestChunk builds a chunk of the given packet count starting at firstSeq.
func tailTestChunk(firstSeq uint64, packets int) *modelcapturev1.CapturePacketChunk {
	recs := make([]*netcapturev1.PacketRecord, packets)
	for i := range recs {
		recs[i] = testPacketRecord(firstSeq+uint64(i), []byte("p"))
	}
	return modelcapturev1.CapturePacketChunk_builder{
		Session:       tailTestSessionRef(),
		FirstSequence: proto.Uint64(firstSeq),
		Packets:       recs,
	}.Build()
}

// The subscription buffer size the broadcaster gives every tail. Overflowing it
// is what these tests exercise, so they depend on the exact figure.
const tailBufferSize = 128

func TestBroadcaster_LaggingSubscriberRecordsGap(t *testing.T) {
	b := captureapi.NewBroadcaster()
	const session = "sess-gap"

	// A fixture is a claim the wire would carry this message.
	if err := protovalidate.Validate(tailTestChunk(1, 1)); err != nil {
		t.Fatalf("tail chunk fixture is not a message the wire would accept: %v", err)
	}

	sub, unsub := b.Subscribe(session)
	defer unsub()

	// Fill the buffer so nothing more fits, then overflow it. The reader never
	// drains, so every later chunk folds into one pending gap.
	for seq := uint64(1); seq <= tailBufferSize; seq++ {
		if dropped := b.Broadcast(session, tailTestChunk(seq, 1)); dropped != 0 {
			t.Fatalf("chunk %d dropped while the buffer had room", seq)
		}
	}
	if dropped := b.Broadcast(session, tailTestChunk(129, 4)); dropped != 1 {
		t.Fatalf("overflowing chunk dropped for %d subscribers, want 1", dropped)
	}
	if dropped := b.Broadcast(session, tailTestChunk(133, 2)); dropped != 1 {
		t.Fatalf("second overflowing chunk dropped for %d subscribers, want 1", dropped)
	}

	b.CloseSession(session)

	gap := sub.TerminalGap()
	if gap == nil {
		t.Fatal("expected a terminal gap after chunks dropped with nothing behind them")
	}
	if gap.DroppedChunks != 2 {
		t.Errorf("dropped chunks = %d, want 2", gap.DroppedChunks)
	}
	if gap.DroppedPackets != 6 {
		t.Errorf("dropped packets = %d, want 6", gap.DroppedPackets)
	}
	if gap.FirstDroppedSequence != 129 || gap.LastDroppedSequence != 134 {
		t.Errorf("dropped range = [%d,%d], want [129,134]", gap.FirstDroppedSequence, gap.LastDroppedSequence)
	}
}

func TestBroadcaster_FastSubscriberUnaffectedByLaggard(t *testing.T) {
	b := captureapi.NewBroadcaster()
	const session = "sess-mixed"

	fast, unsubFast := b.Subscribe(session)
	defer unsubFast()
	slow, unsubSlow := b.Subscribe(session)
	defer unsubSlow()

	// The fast subscriber is drained after every broadcast, so its buffer never
	// fills; the slow one is never read and drops everything past the buffer.
	const total = 200
	for seq := uint64(1); seq <= total; seq++ {
		b.Broadcast(session, tailTestChunk(seq, 1))
		item, ok := <-fast.Items()
		if !ok {
			t.Fatalf("fast subscriber channel closed at seq %d", seq)
		}
		if item.Chunk == nil {
			t.Fatalf("fast subscriber received a gap at seq %d: %+v", seq, item.Gap)
		}
		if item.Chunk.GetFirstSequence() != seq {
			t.Fatalf("fast subscriber got chunk %d, want %d", item.Chunk.GetFirstSequence(), seq)
		}
	}

	b.CloseSession(session)

	if gap := fast.TerminalGap(); gap != nil {
		t.Errorf("fast subscriber recorded a gap: %+v", gap)
	}

	gap := slow.TerminalGap()
	if gap == nil {
		t.Fatal("expected the slow subscriber to record a gap")
	}
	const wantDropped = total - tailBufferSize
	if gap.DroppedChunks != wantDropped {
		t.Errorf("slow subscriber dropped %d chunks, want %d", gap.DroppedChunks, wantDropped)
	}
	if gap.FirstDroppedSequence != tailBufferSize+1 || gap.LastDroppedSequence != total {
		t.Errorf("slow subscriber dropped range = [%d,%d], want [%d,%d]",
			gap.FirstDroppedSequence, gap.LastDroppedSequence, tailBufferSize+1, total)
	}
}

func TestBroadcaster_GapPrecedesNextChunkAfterDrain(t *testing.T) {
	b := captureapi.NewBroadcaster()
	const session = "sess-order"

	sub, unsub := b.Subscribe(session)
	defer unsub()

	for seq := uint64(1); seq <= tailBufferSize; seq++ {
		b.Broadcast(session, tailTestChunk(seq, 1))
	}
	// Overflow once to open a pending gap over sequences 129..130.
	b.Broadcast(session, tailTestChunk(129, 2))

	// Free two slots: one for the gap, one for the chunk that follows it.
	for seq := uint64(1); seq <= 2; seq++ {
		item := <-sub.Items()
		if item.Chunk == nil || item.Chunk.GetFirstSequence() != seq {
			t.Fatalf("expected buffered chunk %d, got %+v", seq, item)
		}
	}
	// Room now exists, so this broadcast flushes the gap and then delivers 131.
	b.Broadcast(session, tailTestChunk(131, 1))

	for seq := uint64(3); seq <= tailBufferSize; seq++ {
		item := <-sub.Items()
		if item.Chunk == nil || item.Chunk.GetFirstSequence() != seq {
			t.Fatalf("expected buffered chunk %d, got %+v", seq, item)
		}
	}

	gapItem := <-sub.Items()
	if gapItem.Gap == nil {
		t.Fatalf("expected a gap before the next chunk, got %+v", gapItem)
	}
	if gapItem.Chunk != nil {
		t.Fatalf("gap item also carried a chunk: %+v", gapItem)
	}
	if gapItem.Gap.DroppedChunks != 1 || gapItem.Gap.DroppedPackets != 2 ||
		gapItem.Gap.FirstDroppedSequence != 129 || gapItem.Gap.LastDroppedSequence != 130 {
		t.Errorf("gap = %+v, want 1 chunk / 2 packets / seq [129,130]", gapItem.Gap)
	}

	next := <-sub.Items()
	if next.Chunk == nil || next.Chunk.GetFirstSequence() != 131 {
		t.Fatalf("expected chunk 131 after the gap, got %+v", next)
	}
}

func TestSubscribeCaptureAssignments_EdgeIsolation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newTestHarness(t)

	sess1 := "0192e6a0-0000-7000-8000-000000000022"
	sess2 := "0192e6a0-0000-7000-8000-000000000011"

	cfg1 := newEdgeSessionConfig(t, testEdge1ID, sess1)
	cfg2 := newEdgeSessionConfig(t, testEdge2ID, sess2)

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
		t.Fatalf("got assignment %+v, want start assignment", msg)
	}

	gotSessionID := start.GetRef().GetCaptureSession().GetId()
	if gotSessionID != sess1 {
		t.Fatalf("got session %s for edge-1, want %s", gotSessionID, sess1)
	}
	if gotEdgeID := start.GetRef().GetEdge().GetEdge().GetId(); gotEdgeID != testEdge1ID {
		t.Fatalf("got edge %s, want %s", gotEdgeID, testEdge1ID)
	}
}

func TestUploadCapture_WithdrawsOwedStartOnFirstChunk(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newTestHarness(t)
	sessID := "0192e6a0-0000-7000-8000-000000000012"
	cfg := newEdgeSessionConfig(t, testEdge1ID, sessID)

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

	subStream1, err := client.SubscribeCaptureAssignments(ctx, connect.NewRequest(&captureedgev1.SubscribeCaptureAssignmentsRequest{}))
	if err != nil {
		t.Fatalf("subscribe 1: %v", err)
	}
	if !subStream1.Receive() {
		t.Fatalf("expected assignment on open, stream closed: %v", subStream1.Err())
	}
	if gotID := subStream1.Msg().GetStart().GetRef().GetCaptureSession().GetId(); gotID != sessID {
		t.Fatalf("got assignment for %s, want %s", gotID, sessID)
	}
	_ = subStream1.Close()

	uploadCtx, uploadCancel := context.WithCancel(ctx)
	defer uploadCancel()
	uploadStream := client.UploadCapture(uploadCtx)

	openingSigned := h.signAssertion(t, testEdge1ID, h.privKey1)
	if err := uploadStream.Send(captureedgev1.UploadCaptureRequest_builder{Assertion: openingSigned}.Build()); err != nil {
		t.Fatalf("send opening assertion: %v", err)
	}

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
		rec, _, err := h.store.Session(ctx, sessID)
		if err == nil && rec != nil && rec.GetState().GetLifecycle() == modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_RUNNING {
			running = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !running {
		rec, _, _ := h.store.Session(ctx, sessID)
		t.Fatalf("got lifecycle %v, want RUNNING", rec.GetState().GetLifecycle())
	}

	sessID2 := "0192e6a0-0000-7000-8000-000000000022"
	cfg2 := newEdgeSessionConfig(t, testEdge1ID, sessID2)
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
		t.Fatalf("got session %s, want second session %s", gotID, sessID2)
	}
}

func TestUploadCapture_RejectsForeignEdge(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newTestHarness(t)
	sessID := "0192e6a0-0000-7000-8000-000000000013"
	cfg1 := newEdgeSessionConfig(t, testEdge1ID, sessID)

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
		t.Fatalf("got error %v, want *connect.Error", err)
	}
	if connectErr.Code() != connect.CodePermissionDenied {
		t.Fatalf("got code %v, want CodePermissionDenied", connectErr.Code())
	}

	// Verify session for edge-1 is still PENDING
	rec, _, err := h.store.Session(ctx, sessID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if rec.GetState().GetLifecycle() != modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_PENDING {
		t.Fatalf("got session lifecycle %v, want PENDING", rec.GetState().GetLifecycle())
	}
}

func TestUploadCapture_ReAssertionLapseTerminatesStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newTestHarness(t)
	sessID := "0192e6a0-0000-7000-8000-000000000014"
	cfg := newEdgeSessionConfig(t, testEdge1ID, sessID)

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
		t.Fatalf("got error %v, want *connect.Error", err)
	}
	if connectErr.Code() != connect.CodeUnauthenticated {
		t.Fatalf("got code %v, want CodeUnauthenticated", connectErr.Code())
	}
	if got, want := connectErr.Message(), "the call is not authorized as an enrolled edge"; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
	if len(connectErr.Details()) != 0 {
		t.Fatalf("got %d details, want 0", len(connectErr.Details()))
	}

	// Verify session marked FAILED with STOP_REASON_ERROR
	rec, _, err := h.store.Session(ctx, sessID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if rec.GetState().GetLifecycle() != modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_FAILED {
		t.Fatalf("got lifecycle %v, want FAILED", rec.GetState().GetLifecycle())
	}
	if rec.GetState().GetStopReason() != modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_ERROR {
		t.Fatalf("got stop reason %v, want ERROR", rec.GetState().GetStopReason())
	}
}

func TestUploadCapture_FinalizationOnStreamCompletion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newTestHarness(t)
	sessID := "0192e6a0-0000-7000-8000-000000000015"
	cfg := newEdgeSessionConfig(t, testEdge1ID, sessID)
	cfg.GetBudget().ClearMaxPackets()
	cfg.GetBudget().SetMaxBytes(1000)
	cfg.GetBudget().SetMaxDuration(durationpb.New(time.Minute))

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
		packets[i] = testPacketRecord(uint64(i+1), bytes.Repeat([]byte{0x01}, 200))
	}

	counters := netcapturev1.CaptureCounters_builder{
		ReceivedPackets: proto.Uint64(5),
		AcceptedPackets: proto.Uint64(5),
	}.Build()

	finalChunk := modelcapturev1.CapturePacketChunk_builder{
		Session:       cfg.GetRef(),
		FirstSequence: proto.Uint64(1),
		Packets:       packets,
		Counters:      counters,
		Final:         proto.Bool(true),
		StopReason:    modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_BYTE_COUNT.Enum(),
	}.Build()

	if err := uploadStream.Send(captureedgev1.UploadCaptureRequest_builder{Chunk: finalChunk}.Build()); err != nil {
		t.Fatalf("send final chunk: %v", err)
	}

	resp, err := uploadStream.CloseAndReceive()
	if err != nil {
		t.Fatalf("close and receive: %v", err)
	}

	if resp.Msg.GetSession().GetCaptureSession().GetId() != sessID {
		t.Fatalf("got response session %s, want %s", resp.Msg.GetSession().GetCaptureSession().GetId(), sessID)
	}

	// Verify session state in store
	rec, _, err := h.store.Session(ctx, sessID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}

	if rec.GetState().GetLifecycle() != modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_COMPLETED {
		t.Fatalf("got lifecycle %v, want COMPLETED", rec.GetState().GetLifecycle())
	}
	if got := rec.GetState().GetStopReason(); got != modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_BYTE_COUNT {
		t.Errorf("stop reason = %v, want BYTE_COUNT", got)
	}

	artifact := rec.GetState().GetArtifact()
	if artifact == nil {
		t.Fatal("expected artifact to be present")
	}
	if artifact.GetPacketCount() != 5 {
		t.Fatalf("got %d packets in artifact, want 5", artifact.GetPacketCount())
	}
	if len(artifact.GetDigest()) != 32 {
		t.Fatalf("got SHA-256 digest length %d, want 32", len(artifact.GetDigest()))
	}
	if !h.store.ArtifactExists(sessID) {
		t.Fatalf("expected artifact file on disk for session %s", sessID)
	}
}

func TestUploadCapture_ReportedReasonControlsLifecycle(t *testing.T) {
	cases := []struct {
		name          string
		reported      modelcapturev1.CaptureStopReason
		canceled      bool
		wantLifecycle modelcapturev1.CaptureLifecycle
		wantReason    modelcapturev1.CaptureStopReason
		wantDisagree  bool
	}{
		{"operator on running", modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_OPERATOR, false, modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_CANCELED, modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_OPERATOR, false},
		{"packet count after cancellation", modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_PACKET_COUNT, true, modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_CANCELED, modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_OPERATOR, true},
		{"operator after cancellation", modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_OPERATOR, true, modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_CANCELED, modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_OPERATOR, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			h := newTestHarness(t)
			sessID := "0192e6a0-0000-7000-8000-0000000000c0"
			cfg := newEdgeSessionConfig(t, testEdge1ID, sessID)
			if _, err := h.store.CreateSession(ctx, cfg); err != nil {
				t.Fatalf("create session: %v", err)
			}
			if tc.canceled {
				_, err := h.store.MutateSession(ctx, sessID, func(rec *modelcapturev1.CaptureSessionRecord) error {
					rec.GetState().SetLifecycle(modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_CANCELED)
					rec.GetState().SetStopReason(modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_OPERATOR)
					return nil
				})
				if err != nil {
					t.Fatalf("cancel session: %v", err)
				}
			}

			var logs bytes.Buffer
			edgeSvc := captureapi.NewEdgeService(h.store, h.newVerifier(), h.broadcaster, captureapi.EdgeServiceConfig{
				Logger: slog.New(slog.NewTextHandler(&logs, nil)),
			})
			stream := newUploadServer(t, edgeSvc).UploadCapture(ctx)
			if err := stream.Send(captureedgev1.UploadCaptureRequest_builder{
				Assertion: h.signAssertion(t, testEdge1ID, h.privKey1),
			}.Build()); err != nil {
				t.Fatalf("send assertion: %v", err)
			}
			chunk := modelcapturev1.CapturePacketChunk_builder{
				Session:       cfg.GetRef(),
				FirstSequence: proto.Uint64(0),
				Counters:      &netcapturev1.CaptureCounters{},
				Final:         proto.Bool(true),
				StopReason:    tc.reported.Enum(),
			}.Build()
			if err := stream.Send(captureedgev1.UploadCaptureRequest_builder{Chunk: chunk}.Build()); err != nil {
				t.Fatalf("send final chunk: %v", err)
			}
			if _, err := stream.CloseAndReceive(); err != nil {
				t.Fatalf("close upload: %v", err)
			}
			rec, _, err := h.store.Session(ctx, sessID)
			if err != nil {
				t.Fatalf("get session: %v", err)
			}
			if got := rec.GetState().GetLifecycle(); got != tc.wantLifecycle {
				t.Errorf("lifecycle = %v, want %v", got, tc.wantLifecycle)
			}
			if got := rec.GetState().GetStopReason(); got != tc.wantReason {
				t.Errorf("stop reason = %v, want %v", got, tc.wantReason)
			}
			if got := strings.Contains(logs.String(), "capture stop reason disagrees with operator cancellation"); got != tc.wantDisagree {
				t.Errorf("disagreement logged = %v, want %v", got, tc.wantDisagree)
			}
		})
	}
}

func TestUploadCapture_RejectsInvalidChunkAndFailsSession(t *testing.T) {
	cases := []struct {
		name   string
		final  bool
		reason *modelcapturev1.CaptureStopReason
	}{
		{"final without reason", true, nil},
		{"final with error", true, modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_ERROR.Enum()},
		{"non-final with duration", false, modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_DURATION.Enum()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			h := newTestHarness(t)
			sessID := "0192e6a0-0000-7000-8000-0000000000d0"
			cfg := newEdgeSessionConfig(t, testEdge1ID, sessID)
			if _, err := h.store.CreateSession(ctx, cfg); err != nil {
				t.Fatalf("create session: %v", err)
			}

			stream := newUploadServer(t, captureapi.NewEdgeService(h.store, h.newVerifier(), h.broadcaster, captureapi.EdgeServiceConfig{})).UploadCapture(ctx)
			if err := stream.Send(captureedgev1.UploadCaptureRequest_builder{
				Assertion: h.signAssertion(t, testEdge1ID, h.privKey1),
			}.Build()); err != nil {
				t.Fatalf("send assertion: %v", err)
			}
			if err := stream.Send(captureedgev1.UploadCaptureRequest_builder{
				Chunk: modelcapturev1.CapturePacketChunk_builder{
					Session:       cfg.GetRef(),
					FirstSequence: proto.Uint64(0),
					Final:         proto.Bool(false),
				}.Build(),
			}.Build()); err != nil {
				t.Fatalf("send initial chunk: %v", err)
			}

			badChunk := modelcapturev1.CapturePacketChunk_builder{
				Session:       cfg.GetRef(),
				FirstSequence: proto.Uint64(0),
				Final:         proto.Bool(tc.final),
				StopReason:    tc.reason,
			}.Build()
			if err := stream.Send(captureedgev1.UploadCaptureRequest_builder{Chunk: badChunk}.Build()); err != nil {
				t.Fatalf("send invalid chunk: %v", err)
			}
			_, err := stream.CloseAndReceive()
			if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
				t.Fatalf("code = %v, want CodeInvalidArgument (error: %v)", got, err)
			}

			rec, _, err := h.store.Session(ctx, sessID)
			if err != nil {
				t.Fatalf("get session: %v", err)
			}
			if got := rec.GetState().GetLifecycle(); got != modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_FAILED {
				t.Fatalf("lifecycle = %v, want FAILED", got)
			}
			if got := rec.GetState().GetStopReason(); got != modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_ERROR {
				t.Fatalf("stop reason = %v, want ERROR", got)
			}
		})
	}
}

func TestCapturePacketChunk_FinalReasonValidation(t *testing.T) {
	cases := []struct {
		name      string
		final     bool
		reason    *modelcapturev1.CaptureStopReason
		wantValid bool
	}{
		{"final without reason", true, nil, false},
		{"final with error", true, modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_ERROR.Enum(), false},
		{"non-final with duration", false, modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_DURATION.Enum(), false},
		{"final with byte count", true, modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_BYTE_COUNT.Enum(), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chunk := modelcapturev1.CapturePacketChunk_builder{
				Session:       tailTestSessionRef(),
				FirstSequence: proto.Uint64(0),
				Final:         proto.Bool(tc.final),
				StopReason:    tc.reason,
			}.Build()
			err := protovalidate.Validate(chunk)
			if (err == nil) != tc.wantValid {
				t.Errorf("validation error = %v, want valid = %v", err, tc.wantValid)
			}
		})
	}
}

// newUploadServer mounts the handler the way the host does: the upload
// procedure behind the wrapper that hands it a transport read deadline.
func newUploadServer(t *testing.T, svc *captureapi.EdgeService) capturev1connect.CaptureEdgeServiceClient {
	t.Helper()
	path, handler := capturev1connect.NewCaptureEdgeServiceHandler(svc)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	mux.Handle(capturev1connect.CaptureEdgeServiceUploadCaptureProcedure, captureapi.WithUploadReadDeadline(handler))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return capturev1connect.NewCaptureEdgeServiceClient(srv.Client(), srv.URL)
}

// An edge that stops sending is the case the arrival-time check cannot see:
// nothing arrives to trigger it. Central has to close the stream on its own
// and stop leaving the session RUNNING behind a connection nobody is using.
func TestUploadCapture_SilentStreamLapsesAndFailsTheSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newTestHarness(t)
	sessID := "0192e6a0-0000-7000-8000-000000000016"
	cfg := newEdgeSessionConfig(t, testEdge1ID, sessID)
	if _, err := h.store.CreateSession(ctx, cfg); err != nil {
		t.Fatalf("create session: %v", err)
	}

	edgeSvc := captureapi.NewEdgeService(h.store, h.newVerifier(), h.broadcaster, captureapi.EdgeServiceConfig{
		AssertionWindow: 50 * time.Millisecond,
	})
	client := newUploadServer(t, edgeSvc)

	uploadStream := client.UploadCapture(ctx)
	if err := uploadStream.Send(captureedgev1.UploadCaptureRequest_builder{
		Assertion: h.signAssertion(t, testEdge1ID, h.privKey1),
	}.Build()); err != nil {
		t.Fatalf("send opening assertion: %v", err)
	}
	chunk := modelcapturev1.CapturePacketChunk_builder{
		Session:       cfg.GetRef(),
		FirstSequence: proto.Uint64(1),
		Packets:       []*netcapturev1.PacketRecord{testPacketRecord(1, []byte("packet 1"))},
	}.Build()
	if err := uploadStream.Send(captureedgev1.UploadCaptureRequest_builder{Chunk: chunk}.Build()); err != nil {
		t.Fatalf("send chunk: %v", err)
	}

	// Send nothing more, and do not half-close: this side goes quiet and
	// stays connected, which is the case that has to be enforced without any
	// further action from the caller. Polling the record rather than the
	// stream is the point — an implementation that only notices on the next
	// message never gets one.
	defer func() {
		cancel()
		_, _ = uploadStream.CloseAndReceive()
	}()

	deadline := time.Now().Add(10 * time.Second)
	var rec *modelcapturev1.CaptureSessionRecord
	for time.Now().Before(deadline) {
		var err error
		rec, _, err = h.store.Session(context.Background(), sessID)
		if err != nil {
			t.Fatalf("get session: %v", err)
		}
		if rec.GetState().GetLifecycle() == modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_FAILED {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if got := rec.GetState().GetLifecycle(); got != modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_FAILED {
		t.Fatalf("the silent stream's session rests in %v; the lapsed window never closed it", got)
	}
	if got := rec.GetState().GetStopReason(); got != modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_ERROR {
		t.Fatalf("got stop reason %v, want ERROR", got)
	}
}

// A completed capture is an audit record. An edge is authenticated, not
// trusted with the session's state, so a second stream naming a finished
// session must not rewrite the stored artifact or the digest describing it.
func TestUploadCapture_RefusesASessionThatAlreadyStopped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newTestHarness(t)
	sessID := "0192e6a0-0000-7000-8000-000000000017"
	cfg := newEdgeSessionConfig(t, testEdge1ID, sessID)
	if _, err := h.store.CreateSession(ctx, cfg); err != nil {
		t.Fatalf("create session: %v", err)
	}

	edgeSvc := captureapi.NewEdgeService(h.store, h.newVerifier(), h.broadcaster, captureapi.EdgeServiceConfig{})
	client := newUploadServer(t, edgeSvc)

	upload := func(payload string, packets int) error {
		stream := client.UploadCapture(ctx)
		if err := stream.Send(captureedgev1.UploadCaptureRequest_builder{
			Assertion: h.signAssertion(t, testEdge1ID, h.privKey1),
		}.Build()); err != nil {
			t.Fatalf("send opening assertion: %v", err)
		}
		records := make([]*netcapturev1.PacketRecord, packets)
		for i := range records {
			records[i] = testPacketRecord(uint64(i+1), []byte(payload))
		}
		final := modelcapturev1.CapturePacketChunk_builder{
			Session:       cfg.GetRef(),
			FirstSequence: proto.Uint64(1),
			Packets:       records,
			Counters: netcapturev1.CaptureCounters_builder{
				ReceivedPackets: proto.Uint64(uint64(packets)),
				AcceptedPackets: proto.Uint64(uint64(packets)),
			}.Build(),
			Final:      proto.Bool(true),
			StopReason: modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_PACKET_COUNT.Enum(),
		}.Build()
		if err := stream.Send(captureedgev1.UploadCaptureRequest_builder{Chunk: final}.Build()); err != nil {
			t.Fatalf("send final chunk: %v", err)
		}
		_, err := stream.CloseAndReceive()
		return err
	}

	if err := upload("first capture", 5); err != nil {
		t.Fatalf("first upload: %v", err)
	}
	first, _, err := h.store.Session(ctx, sessID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}

	err = upload("second capture", 1)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("got stopped-session error %v, want CodeFailedPrecondition", err)
	}

	second, _, err := h.store.Session(ctx, sessID)
	if err != nil {
		t.Fatalf("get session again: %v", err)
	}
	firstArtifact, secondArtifact := first.GetState().GetArtifact(), second.GetState().GetArtifact()
	if secondArtifact.GetPacketCount() != firstArtifact.GetPacketCount() ||
		secondArtifact.GetByteSize() != firstArtifact.GetByteSize() ||
		!bytes.Equal(secondArtifact.GetDigest(), firstArtifact.GetDigest()) {
		t.Fatalf("the completed capture's artifact was rewritten: %d packets became %d",
			firstArtifact.GetPacketCount(), secondArtifact.GetPacketCount())
	}
	if !h.store.ArtifactExists(sessID) {
		t.Fatal("the completed capture's file is gone")
	}
}

// A chunk naming a session that does not exist is refused by the chunk's own
// edge check, not by the store lookup behind it: with that check removed the
// answer would be NotFound rather than PermissionDenied.
func TestUploadCapture_ChunkEdgeCheckRefusesBeforeTheStoreIsRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newTestHarness(t)
	edgeSvc := captureapi.NewEdgeService(h.store, h.newVerifier(), h.broadcaster, captureapi.EdgeServiceConfig{})
	client := newUploadServer(t, edgeSvc)

	// Edge 2 uploads a chunk naming edge 1's unknown session.
	unknown := newEdgeSessionConfig(t, testEdge1ID, "0192e6a0-0000-7000-8000-000000000018")

	stream := client.UploadCapture(ctx)
	if err := stream.Send(captureedgev1.UploadCaptureRequest_builder{
		Assertion: h.signAssertion(t, testEdge2ID, h.privKey2),
	}.Build()); err != nil {
		t.Fatalf("send opening assertion: %v", err)
	}
	chunk := modelcapturev1.CapturePacketChunk_builder{
		Session:       unknown.GetRef(),
		FirstSequence: proto.Uint64(1),
		Packets:       []*netcapturev1.PacketRecord{testPacketRecord(1, []byte("packet 1"))},
	}.Build()
	if err := stream.Send(captureedgev1.UploadCaptureRequest_builder{Chunk: chunk}.Build()); err != nil {
		t.Fatalf("send chunk: %v", err)
	}
	_, err := stream.CloseAndReceive()
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("got chunk edge-check error %v, want CodePermissionDenied", err)
	}
}

// A stop is owed for a session an edge started and an operator then canceled,
// and for no other: a session canceled while it was still pending would
// otherwise owe one on every resend tick for the life of the record.
func TestSubscribeCaptureAssignments_StopIsOwedOnlyForAStartedSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newTestHarness(t)
	// The never-started session sorts first, so a missing started_at guard
	// sends its ref before the started session's.
	neverRanID := "0192e6a0-0000-7000-8000-000000000019"
	startedID := "0192e6a0-0000-7000-8000-00000000001a"

	for _, id := range []string{startedID, neverRanID} {
		if _, err := h.store.CreateSession(ctx, newEdgeSessionConfig(t, testEdge1ID, id)); err != nil {
			t.Fatalf("create session %s: %v", id, err)
		}
	}
	cancelSession := func(id string, started bool) {
		t.Helper()
		if _, err := h.store.MutateSession(ctx, id, func(rec *modelcapturev1.CaptureSessionRecord) error {
			if started {
				rec.GetState().SetStartedAt(timestamppb.Now())
			}
			rec.GetState().SetLifecycle(modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_CANCELED)
			rec.GetState().SetStopReason(modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_OPERATOR)
			return nil
		}); err != nil {
			t.Fatalf("cancel session %s: %v", id, err)
		}
	}
	cancelSession(startedID, true)
	cancelSession(neverRanID, false)

	edgeSvc := captureapi.NewEdgeService(h.store, h.newVerifier(), h.broadcaster, captureapi.EdgeServiceConfig{
		EdgeID:         func(context.Context) (string, error) { return testEdge1ID, nil },
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
		t.Fatalf("expected a stop assignment, stream closed: %v", stream.Err())
	}
	got := stream.Msg().GetStop().GetCaptureSession().GetId()
	if got != startedID {
		t.Fatalf("got stop for session %s, want %s", got, startedID)
	}
}

// One pcapng per session is written by one writer. Two streams uploading the
// same session would interleave their packets into it, and each would discard
// the other's partial file on its way out.
func TestUploadCapture_RefusesASecondConcurrentStreamForOneSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newTestHarness(t)
	sessID := "0192e6a0-0000-7000-8000-00000000001b"
	cfg := newEdgeSessionConfig(t, testEdge1ID, sessID)
	if _, err := h.store.CreateSession(ctx, cfg); err != nil {
		t.Fatalf("create session: %v", err)
	}

	edgeSvc := captureapi.NewEdgeService(h.store, h.newVerifier(), h.broadcaster, captureapi.EdgeServiceConfig{})
	client := newUploadServer(t, edgeSvc)

	openWithChunk := func(payload string) *connect.ClientStreamForClient[captureedgev1.UploadCaptureRequest, captureedgev1.UploadCaptureResponse] {
		t.Helper()
		stream := client.UploadCapture(ctx)
		if err := stream.Send(captureedgev1.UploadCaptureRequest_builder{
			Assertion: h.signAssertion(t, testEdge1ID, h.privKey1),
		}.Build()); err != nil {
			t.Fatalf("send opening assertion: %v", err)
		}
		if err := stream.Send(captureedgev1.UploadCaptureRequest_builder{
			Chunk: modelcapturev1.CapturePacketChunk_builder{
				Session:       cfg.GetRef(),
				FirstSequence: proto.Uint64(1),
				Packets:       []*netcapturev1.PacketRecord{testPacketRecord(1, []byte(payload))},
			}.Build(),
		}.Build()); err != nil {
			t.Fatalf("send chunk: %v", err)
		}
		return stream
	}

	first := openWithChunk("first stream")
	// The first stream's claim is taken while the handler processes its
	// chunk; wait for the session to reach RUNNING, which that same block does.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		rec, _, err := h.store.Session(ctx, sessID)
		if err != nil {
			t.Fatalf("get session: %v", err)
		}
		if rec.GetState().GetLifecycle() == modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_RUNNING {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	second := openWithChunk("second stream")
	if _, err := second.CloseAndReceive(); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Fatalf("got second concurrent upload error %v, want CodeAlreadyExists", err)
	}

	cancel()
	_, _ = first.CloseAndReceive()
}

// A partial pcapng has no artifact descriptor, so the retention sweep — which
// walks session records — can never reach it. Leaving one behind would put
// captured payload outside retention for good.
func TestUploadCapture_AbandonedStreamLeavesNoPartialArtifact(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newTestHarness(t)
	sessID := "0192e6a0-0000-7000-8000-00000000001c"
	cfg := newEdgeSessionConfig(t, testEdge1ID, sessID)
	if _, err := h.store.CreateSession(ctx, cfg); err != nil {
		t.Fatalf("create session: %v", err)
	}

	edgeSvc := captureapi.NewEdgeService(h.store, h.newVerifier(), h.broadcaster, captureapi.EdgeServiceConfig{})
	client := newUploadServer(t, edgeSvc)

	stream := client.UploadCapture(ctx)
	if err := stream.Send(captureedgev1.UploadCaptureRequest_builder{
		Assertion: h.signAssertion(t, testEdge1ID, h.privKey1),
	}.Build()); err != nil {
		t.Fatalf("send opening assertion: %v", err)
	}
	if err := stream.Send(captureedgev1.UploadCaptureRequest_builder{
		Chunk: modelcapturev1.CapturePacketChunk_builder{
			Session:       cfg.GetRef(),
			FirstSequence: proto.Uint64(1),
			Packets:       []*netcapturev1.PacketRecord{testPacketRecord(1, []byte("packet 1"))},
		}.Build(),
	}.Build()); err != nil {
		t.Fatalf("send chunk: %v", err)
	}

	// End the stream without a final chunk.
	if _, err := stream.CloseAndReceive(); err == nil {
		t.Fatal("expected an error for a stream that ended before its final chunk")
	}

	if h.store.ArtifactExists(sessID) {
		t.Fatal("an abandoned upload left a partial pcapng the retention sweep cannot reach")
	}
	rec, _, err := h.store.Session(ctx, sessID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got := rec.GetState().GetLifecycle(); got != modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_FAILED {
		t.Fatalf("got abandoned session lifecycle %v, want FAILED", got)
	}
}

// An operator can cancel between the assignment and the first chunk. Central
// only learns the edge started from that chunk, so the stop it owes has to
// survive the cancellation arriving first.
func TestUploadCapture_CancellationBeforeTheFirstChunkStillOwesAStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newTestHarness(t)
	sessID := "0192e6a0-0000-7000-8000-00000000001d"
	cfg := newEdgeSessionConfig(t, testEdge1ID, sessID)
	if _, err := h.store.CreateSession(ctx, cfg); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// The operator cancels while the session is still PENDING; the edge is
	// already capturing and has not reported yet.
	if _, err := h.store.MutateSession(ctx, sessID, func(rec *modelcapturev1.CaptureSessionRecord) error {
		rec.GetState().SetLifecycle(modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_CANCELED)
		rec.GetState().SetStopReason(modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_OPERATOR)
		return nil
	}); err != nil {
		t.Fatalf("cancel session: %v", err)
	}

	edgeSvc := captureapi.NewEdgeService(h.store, h.newVerifier(), h.broadcaster, captureapi.EdgeServiceConfig{
		EdgeID:         func(context.Context) (string, error) { return testEdge1ID, nil },
		ResendInterval: 10 * time.Minute,
	})
	client := newUploadServer(t, edgeSvc)

	upload := client.UploadCapture(ctx)
	if err := upload.Send(captureedgev1.UploadCaptureRequest_builder{
		Assertion: h.signAssertion(t, testEdge1ID, h.privKey1),
	}.Build()); err != nil {
		t.Fatalf("send opening assertion: %v", err)
	}
	if err := upload.Send(captureedgev1.UploadCaptureRequest_builder{
		Chunk: modelcapturev1.CapturePacketChunk_builder{
			Session:       cfg.GetRef(),
			FirstSequence: proto.Uint64(1),
			Packets:       []*netcapturev1.PacketRecord{testPacketRecord(1, []byte("packet 1"))},
		}.Build(),
	}.Build()); err != nil {
		t.Fatalf("send chunk: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		rec, _, err := h.store.Session(ctx, sessID)
		if err != nil {
			t.Fatalf("get session: %v", err)
		}
		if rec.GetState().HasStartedAt() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	rec, _, err := h.store.Session(ctx, sessID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if !rec.GetState().HasStartedAt() {
		t.Fatal("the first chunk did not record that the edge had started, so no stop will ever be owed")
	}
	if got := rec.GetState().GetLifecycle(); got != modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_CANCELED {
		t.Fatalf("got canceled session lifecycle %v, want CANCELED", got)
	}

	// The assignment stream now owes the stop the operator asked for.
	path, handler := capturev1connect.NewCaptureEdgeServiceHandler(edgeSvc)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	assignClient := capturev1connect.NewCaptureEdgeServiceClient(srv.Client(), srv.URL)

	stream, err := assignClient.SubscribeCaptureAssignments(ctx, connect.NewRequest(&captureedgev1.SubscribeCaptureAssignmentsRequest{}))
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = stream.Close() }()

	if !stream.Receive() {
		t.Fatalf("expected a stop assignment, stream closed: %v", stream.Err())
	}
	if got := stream.Msg().GetStop().GetCaptureSession().GetId(); got != sessID {
		t.Fatalf("got stop assignment %+v, want one for %s", stream.Msg(), sessID)
	}

	cancel()
	_, _ = upload.CloseAndReceive()
}

// A refused assertion limits wire output to the public unauthenticated message
// and keeps the cause chain for server-side logging.
func TestUploadCapture_RefusedAssertionExposesNoVerificationDetails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newTestHarness(t)
	edgeSvc := captureapi.NewEdgeService(h.store, h.newVerifier(), h.broadcaster, captureapi.EdgeServiceConfig{})

	path, handler := capturev1connect.NewCaptureEdgeServiceHandler(edgeSvc)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := capturev1connect.NewCaptureEdgeServiceClient(srv.Client(), srv.URL)

	t.Run("OpeningAssertionBadSignature", func(t *testing.T) {
		stream := client.UploadCapture(ctx)

		invalidSigned := h.signAssertion(t, testEdge1ID, h.privKey2)
		if err := stream.Send(captureedgev1.UploadCaptureRequest_builder{Assertion: invalidSigned}.Build()); err != nil {
			t.Fatalf("send assertion: %v", err)
		}

		_, err := stream.CloseAndReceive()
		if err == nil {
			t.Fatal("expected Unauthenticated error, got nil")
		}

		var connectErr *connect.Error
		if !errors.As(err, &connectErr) {
			t.Fatalf("got error %T, want *connect.Error", err)
		}
		if got := connectErr.Code(); got != connect.CodeUnauthenticated {
			t.Errorf("code = %v, want CodeUnauthenticated", got)
		}
		if got, want := connectErr.Message(), "the call is not authorized as an enrolled edge"; got != want {
			t.Errorf("message = %q, want %q", got, want)
		}
		if len(connectErr.Details()) != 0 {
			t.Fatalf("got %d details, want 0; details disclose verification failure code", len(connectErr.Details()))
		}
		if strings.Contains(err.Error(), "signature") || strings.Contains(err.Error(), "verify") {
			t.Errorf("error string discloses verification internals: %q", err.Error())
		}
	})

	t.Run("MidStreamAssertionBadSignature", func(t *testing.T) {
		sessID := "0192e6a0-0000-7000-8000-00000000001e"
		cfg := newEdgeSessionConfig(t, testEdge1ID, sessID)
		if _, err := h.store.CreateSession(ctx, cfg); err != nil {
			t.Fatalf("create session: %v", err)
		}

		stream := client.UploadCapture(ctx)

		if err := stream.Send(captureedgev1.UploadCaptureRequest_builder{
			Assertion: h.signAssertion(t, testEdge1ID, h.privKey1),
		}.Build()); err != nil {
			t.Fatalf("send opening assertion: %v", err)
		}

		chunk := modelcapturev1.CapturePacketChunk_builder{
			Session:       cfg.GetRef(),
			FirstSequence: proto.Uint64(1),
			Packets:       []*netcapturev1.PacketRecord{testPacketRecord(1, []byte("packet 1"))},
		}.Build()
		if err := stream.Send(captureedgev1.UploadCaptureRequest_builder{Chunk: chunk}.Build()); err != nil {
			t.Fatalf("send chunk: %v", err)
		}

		invalidSigned := h.signAssertion(t, testEdge1ID, h.privKey2)
		if err := stream.Send(captureedgev1.UploadCaptureRequest_builder{Assertion: invalidSigned}.Build()); err != nil {
			t.Fatalf("send invalid re-assertion: %v", err)
		}

		_, err := stream.CloseAndReceive()
		if err == nil {
			t.Fatal("expected Unauthenticated error, got nil")
		}

		var connectErr *connect.Error
		if !errors.As(err, &connectErr) {
			t.Fatalf("got error %T, want *connect.Error", err)
		}
		if got := connectErr.Code(); got != connect.CodeUnauthenticated {
			t.Errorf("code = %v, want CodeUnauthenticated", got)
		}
		if got, want := connectErr.Message(), "the call is not authorized as an enrolled edge"; got != want {
			t.Errorf("message = %q, want %q", got, want)
		}
		if len(connectErr.Details()) != 0 {
			t.Fatalf("got %d details, want 0; details disclose verification failure code", len(connectErr.Details()))
		}
		if strings.Contains(err.Error(), "signature") || strings.Contains(err.Error(), "verify") {
			t.Errorf("error string discloses verification internals: %q", err.Error())
		}
	})
}
