package capture_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	captureedgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/capture/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/capture/v1/capturev1connect"
	modelcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	agentcapture "go.aledante.io/FlowSeer/src/edge/agent/internal/capture"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/subscribeloop"
	"go.aledante.io/FlowSeer/src/modules/capture"
	"go.aledante.io/FlowSeer/src/modules/capture/rawsocket"
)

const (
	testEdgeID   = "0192e6a0-0000-7000-8000-000000000001"
	testAudience = "flowseer-central"
)

type fakeSource struct {
	frames chan rawsocket.Frame
	closed chan struct{}
}

func newFakeSource(buffer int) *fakeSource {
	return &fakeSource{
		frames: make(chan rawsocket.Frame, buffer),
		closed: make(chan struct{}),
	}
}

func (s *fakeSource) Receive(_ context.Context) <-chan rawsocket.Frame {
	return s.frames
}

func (s *fakeSource) Stats() (uint64, uint64, error) {
	return 0, 0, nil
}

func (s *fakeSource) Close() error {
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
	return nil
}

type fakeCaptureServiceHandler struct {
	capturev1connect.UnimplementedCaptureEdgeServiceHandler

	mu          sync.Mutex
	requests    []*captureedgev1.UploadCaptureRequest
	chunks      []*modelcapturev1.CapturePacketChunk
	assertions  []*edgev1.SignedEdgeAssertion
	closed      chan struct{}
	finalSeen   bool
	subRequests chan *captureedgev1.SubscribeCaptureAssignmentsRequest
}

func newFakeCaptureServiceHandler() *fakeCaptureServiceHandler {
	return &fakeCaptureServiceHandler{
		closed:      make(chan struct{}),
		subRequests: make(chan *captureedgev1.SubscribeCaptureAssignmentsRequest, 10),
	}
}

func (f *fakeCaptureServiceHandler) SubscribeCaptureAssignments(
	ctx context.Context,
	req *connect.Request[captureedgev1.SubscribeCaptureAssignmentsRequest],
	stream *connect.ServerStream[captureedgev1.SubscribeCaptureAssignmentsResponse],
) error {
	f.subRequests <- req.Msg
	if err := stream.Send(captureedgev1.SubscribeCaptureAssignmentsResponse_builder{
		Stop: modelcapturev1.CaptureSessionGlobalRef_builder{
			CaptureSession: modelcapturev1.CaptureSessionLocalRef_builder{Id: proto.String("dummy")}.Build(),
		}.Build(),
	}.Build()); err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}

func (f *fakeCaptureServiceHandler) UploadCapture(
	_ context.Context,
	stream *connect.ClientStream[captureedgev1.UploadCaptureRequest],
) (*connect.Response[captureedgev1.UploadCaptureResponse], error) {
	defer close(f.closed)

	for stream.Receive() {
		msg := stream.Msg()
		f.mu.Lock()
		f.requests = append(f.requests, msg)
		if a := msg.GetAssertion(); a != nil {
			f.assertions = append(f.assertions, a)
		}
		if c := msg.GetChunk(); c != nil {
			f.chunks = append(f.chunks, c)
			if c.GetFinal() {
				f.finalSeen = true
			}
		}
		f.mu.Unlock()
	}

	if err := stream.Err(); err != nil {
		return nil, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.finalSeen {
		return nil, connect.NewError(connect.CodeDataLoss, errors.New("stream terminated before final chunk"))
	}

	return connect.NewResponse(&captureedgev1.UploadCaptureResponse{}), nil
}

func (f *fakeCaptureServiceHandler) AssertionsCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.assertions)
}

func (f *fakeCaptureServiceHandler) Chunks() []*modelcapturev1.CapturePacketChunk {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]*modelcapturev1.CapturePacketChunk, len(f.chunks))
	copy(cp, f.chunks)
	return cp
}

func (f *fakeCaptureServiceHandler) HasFinal() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.finalSeen
}

func testSessionConfig(sessionID string, maxPackets uint64) *modelcapturev1.CaptureSessionConfig {
	cfg := modelcapturev1.CaptureSessionConfig_builder{
		Ref: modelcapturev1.CaptureSessionGlobalRef_builder{
			Edge: edgev1.EdgeGlobalRef_builder{
				Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(testEdgeID)}.Build(),
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
			MaxPackets: proto.Uint64(maxPackets),
		}.Build(),
		Authorization: modelcapturev1.CaptureAuthorization_builder{
			Operator:             proto.String("alice"),
			Reason:               proto.String("investigation"),
			FullPayloadRequested: proto.Bool(false),
		}.Build(),
	}.Build()
	return cfg
}

func testAssertionSigner(t *testing.T) func(context.Context) (*edgev1.SignedEdgeAssertion, error) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	return func(_ context.Context) (*edgev1.SignedEdgeAssertion, error) {
		nonce := make([]byte, 16)
		if _, err := rand.Read(nonce); err != nil {
			return nil, err
		}
		now := time.Now()
		bodyHash := sha256.Sum256(nil)
		assertion := edgev1.EdgeAssertion_builder{
			Edge: edgev1.EdgeGlobalRef_builder{
				Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(testEdgeID)}.Build(),
			}.Build(),
			Audience:   proto.String(testAudience),
			IssuedAt:   timestamppb.New(now),
			ExpiresAt:  timestamppb.New(now.Add(30 * time.Second)),
			Nonce:      nonce,
			Procedure:  proto.String(capturev1connect.CaptureEdgeServiceUploadCaptureProcedure),
			BodySha256: bodyHash[:],
		}.Build()

		payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(assertion)
		if err != nil {
			return nil, err
		}

		return edgev1.SignedEdgeAssertion_builder{
			Payload:   payload,
			Signature: ed25519.Sign(priv, payload),
		}.Build(), nil
	}
}

func startTestServer(t *testing.T, handler *fakeCaptureServiceHandler) (capturev1connect.CaptureEdgeServiceClient, func()) {
	t.Helper()
	mux := http.NewServeMux()
	path, h := capturev1connect.NewCaptureEdgeServiceHandler(handler)
	mux.Handle(path, h)
	server := httptest.NewServer(mux)
	client := capturev1connect.NewCaptureEdgeServiceClient(server.Client(), server.URL)
	return client, server.Close
}

func TestHandler_StartAndUploadChunks(t *testing.T) {
	sessID := "0192e6a0-0000-7000-8000-000000000010"
	cfg := testSessionConfig(sessID, 3)
	if err := protovalidate.Validate(cfg); err != nil {
		t.Fatalf("config fixture failed validation: %v", err)
	}

	fakeServer := newFakeCaptureServiceHandler()
	client, closeServer := startTestServer(t, fakeServer)
	defer closeServer()

	source := newFakeSource(10)
	for i := range 3 {
		source.frames <- rawsocket.Frame{
			Data:           []byte("packet payload"),
			OriginalLength: 14,
			CapturedAt:     time.Now(),
		}
		_ = i
	}

	h, err := agentcapture.NewHandler(agentcapture.HandlerConfig{
		Client:        client,
		SignAssertion: testAssertionSigner(t),
		OpenCaptureSource: func(_ context.Context, _ capture.Config) (capture.Source, bool, error) {
			return source, false, nil
		},
		InactivityTimeout: time.Second,
		ReassertInterval:  10 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	defer func() { _ = h.Close() }()

	startMsg := captureedgev1.SubscribeCaptureAssignmentsResponse_builder{
		Start: cfg,
	}.Build()

	if err := h.Handle(context.Background(), startMsg); err != nil {
		t.Fatalf("Handle(Start): %v", err)
	}

	select {
	case <-fakeServer.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for upload stream completion")
	}

	if !fakeServer.HasFinal() {
		t.Fatal("expected upload stream to terminate with final chunk")
	}

	chunks := fakeServer.Chunks()
	// Should have initial chunk + data chunk(s)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks (initial + data), got: %d", len(chunks))
	}
	// Initial chunk has FirstSequence 0 and no packets
	if chunks[0].GetFirstSequence() != 0 || len(chunks[0].GetPackets()) != 0 {
		t.Errorf("initial chunk mismatch: seq=%d, len=%d", chunks[0].GetFirstSequence(), len(chunks[0].GetPackets()))
	}

	var totalPackets int
	for _, ch := range chunks {
		totalPackets += len(ch.GetPackets())
	}
	if totalPackets != 3 {
		t.Errorf("total uploaded packets = %d, want 3", totalPackets)
	}
}

func TestHandler_PeriodicMidStreamReAssertion(t *testing.T) {
	sessID := "0192e6a0-0000-7000-8000-000000000011"
	cfg := testSessionConfig(sessID, 100)

	fakeServer := newFakeCaptureServiceHandler()
	client, closeServer := startTestServer(t, fakeServer)
	defer closeServer()

	source := newFakeSource(10)

	h, err := agentcapture.NewHandler(agentcapture.HandlerConfig{
		Client:        client,
		SignAssertion: testAssertionSigner(t),
		OpenCaptureSource: func(_ context.Context, _ capture.Config) (capture.Source, bool, error) {
			return source, false, nil
		},
		InactivityTimeout: 2 * time.Second,
		ReassertInterval:  20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	defer func() { _ = h.Close() }()

	startMsg := captureedgev1.SubscribeCaptureAssignmentsResponse_builder{
		Start: cfg,
	}.Build()

	if err := h.Handle(context.Background(), startMsg); err != nil {
		t.Fatalf("Handle(Start): %v", err)
	}

	// Wait for at least 3 assertions (1 opening + at least 2 mid-stream re-assertions)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if fakeServer.AssertionsCount() >= 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if count := fakeServer.AssertionsCount(); count < 3 {
		t.Fatalf("expected at least 3 assertions (1 opening + 2 mid-stream), got: %d", count)
	}
}

func TestHandler_OperatorStopCancelsAndFlushesFinalChunk(t *testing.T) {
	sessID := "0192e6a0-0000-7000-8000-000000000012"
	cfg := testSessionConfig(sessID, 1000)

	fakeServer := newFakeCaptureServiceHandler()
	client, closeServer := startTestServer(t, fakeServer)
	defer closeServer()

	source := newFakeSource(10)
	source.frames <- rawsocket.Frame{
		Data:           []byte("packet 1"),
		OriginalLength: 8,
		CapturedAt:     time.Now(),
	}

	h, err := agentcapture.NewHandler(agentcapture.HandlerConfig{
		Client:        client,
		SignAssertion: testAssertionSigner(t),
		OpenCaptureSource: func(_ context.Context, _ capture.Config) (capture.Source, bool, error) {
			return source, false, nil
		},
		InactivityTimeout: 10 * time.Second,
		ReassertInterval:  10 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	defer func() { _ = h.Close() }()

	if err := h.Handle(context.Background(), captureedgev1.SubscribeCaptureAssignmentsResponse_builder{
		Start: cfg,
	}.Build()); err != nil {
		t.Fatalf("Handle(Start): %v", err)
	}

	// Let the initial packet arrive
	time.Sleep(50 * time.Millisecond)

	// Operator stop assignment arrives
	stopMsg := captureedgev1.SubscribeCaptureAssignmentsResponse_builder{
		Stop: cfg.GetRef(),
	}.Build()

	if err := h.Handle(context.Background(), stopMsg); err != nil {
		t.Fatalf("Handle(Stop): %v", err)
	}

	select {
	case <-fakeServer.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for upload stream after operator stop")
	}

	if !fakeServer.HasFinal() {
		t.Fatal("operator stop did not flush a final chunk")
	}
}

func TestHandler_InactivityTimeoutAbortsWithoutFinalChunk(t *testing.T) {
	sessID := "0192e6a0-0000-7000-8000-000000000013"
	cfg := testSessionConfig(sessID, 100)

	fakeServer := newFakeCaptureServiceHandler()
	client, closeServer := startTestServer(t, fakeServer)
	defer closeServer()

	// Source yields no packets (idle interface)
	source := newFakeSource(10)

	h, err := agentcapture.NewHandler(agentcapture.HandlerConfig{
		Client:        client,
		SignAssertion: testAssertionSigner(t),
		OpenCaptureSource: func(_ context.Context, _ capture.Config) (capture.Source, bool, error) {
			return source, false, nil
		},
		InactivityTimeout: 50 * time.Millisecond,
		ReassertInterval:  10 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	defer func() { _ = h.Close() }()

	if err := h.Handle(context.Background(), captureedgev1.SubscribeCaptureAssignmentsResponse_builder{
		Start: cfg,
	}.Build()); err != nil {
		t.Fatalf("Handle(Start): %v", err)
	}

	select {
	case <-fakeServer.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for inactivity timeout stream closure")
	}

	if fakeServer.HasFinal() {
		t.Fatal("silent capture sent final: true; must abort without final chunk so central records FAILED")
	}
}

func TestHandler_CustomCaptureSourceExecution(t *testing.T) {
	sessID := "0192e6a0-0000-7000-8000-000000000014"
	cfg := testSessionConfig(sessID, 1)

	fakeServer := newFakeCaptureServiceHandler()
	client, closeServer := startTestServer(t, fakeServer)
	defer closeServer()

	source := newFakeSource(5)
	source.frames <- rawsocket.Frame{
		Data:           []byte("custom source frame"),
		OriginalLength: 19,
		CapturedAt:     time.Now(),
	}

	customSourceCalled := make(chan struct{}, 1)
	h, err := agentcapture.NewHandler(agentcapture.HandlerConfig{
		Client:        client,
		SignAssertion: testAssertionSigner(t),
		OpenCaptureSource: func(_ context.Context, _ capture.Config) (capture.Source, bool, error) {
			select {
			case customSourceCalled <- struct{}{}:
			default:
			}
			return source, false, nil
		},
		InactivityTimeout: 5 * time.Second,
		ReassertInterval:  10 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	defer func() { _ = h.Close() }()

	if err := h.Handle(context.Background(), captureedgev1.SubscribeCaptureAssignmentsResponse_builder{
		Start: cfg,
	}.Build()); err != nil {
		t.Fatalf("Handle(Start): %v", err)
	}

	select {
	case <-fakeServer.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for custom source capture completion")
	}

	select {
	case <-customSourceCalled:
	default:
		t.Fatal("OpenCaptureSource seam was not invoked")
	}
	if !fakeServer.HasFinal() {
		t.Fatal("expected final chunk from custom source")
	}
}

func TestOpen_AdaptsClient(t *testing.T) {
	fakeServer := newFakeCaptureServiceHandler()
	client, closeServer := startTestServer(t, fakeServer)
	defer closeServer()

	opener := agentcapture.Open(client)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := opener(ctx)
	if err != nil {
		t.Fatalf("opener error: %v", err)
	}
	defer func() { _ = stream.Close() }()

	if !stream.Receive() {
		t.Fatalf("stream.Receive() failed: %v", stream.Err())
	}

	select {
	case <-fakeServer.subRequests:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for subscribe request on server")
	}
}

func TestEvents_ConfiguredNames(t *testing.T) {
	want := subscribeloop.Events{
		Connected:          "flowseer.edge.capture.connected",
		Disconnected:       "flowseer.edge.capture.disconnected",
		Dropped:            "flowseer.edge.capture.dropped",
		ConnectionCountKey: "flowseer.edge.capture.connections",
		MessageCountKey:    "flowseer.edge.capture.messages",
	}
	if got := agentcapture.Events; got != want {
		t.Errorf("Events = %+v, want %+v", got, want)
	}
}

func TestLogAttrs_SessionID(t *testing.T) {
	msg := captureedgev1.SubscribeCaptureAssignmentsResponse_builder{
		Start: testSessionConfig("0192e6a0-0000-7000-8000-000000000015", 1),
	}.Build()

	attrs := agentcapture.LogAttrs(msg)
	if len(attrs) != 1 {
		t.Fatalf("len(attrs) = %d, want 1", len(attrs))
	}
	if got, want := attrs[0].Key, "flowseer.capture.session.id"; got != want {
		t.Errorf("attr key = %q, want %q", got, want)
	}
	if got, want := attrs[0].Value.String(), "0192e6a0-0000-7000-8000-000000000015"; got != want {
		t.Errorf("attr val = %q, want %q", got, want)
	}
}

// TestHandler_SourceFailureAbortsWithoutFinalChunk proves a capture whose
// packet source dies mid-run ends its upload the way a silent one does: with
// no final chunk, so central records FAILED. A final chunk is the only thing
// that tells central a capture finished, and central would answer one from a
// run that died after a single packet by finalizing the artifact and deriving
// PACKET_COUNT from a budget of 100 that was never reached.
func TestHandler_SourceFailureAbortsWithoutFinalChunk(t *testing.T) {
	sessID := "0192e6a0-0000-7000-8000-000000000016"
	cfg := testSessionConfig(sessID, 100)

	fakeServer := newFakeCaptureServiceHandler()
	client, closeServer := startTestServer(t, fakeServer)
	defer closeServer()

	source := newFakeSource(5)
	source.frames <- rawsocket.Frame{
		Data:           []byte("packet 1"),
		OriginalLength: 8,
		CapturedAt:     time.Now(),
	}
	source.frames <- rawsocket.Frame{Err: errors.New("recvfrom: network is down")}

	h, err := agentcapture.NewHandler(agentcapture.HandlerConfig{
		Client:        client,
		SignAssertion: testAssertionSigner(t),
		OpenCaptureSource: func(_ context.Context, _ capture.Config) (capture.Source, bool, error) {
			return source, false, nil
		},
		InactivityTimeout: 10 * time.Second,
		ReassertInterval:  10 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	defer func() { _ = h.Close() }()

	if err := h.Handle(context.Background(), captureedgev1.SubscribeCaptureAssignmentsResponse_builder{
		Start: cfg,
	}.Build()); err != nil {
		t.Fatalf("Handle(Start): %v", err)
	}

	select {
	case <-fakeServer.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the upload stream to end")
	}

	if fakeServer.HasFinal() {
		t.Fatal("a capture killed off by its source sent final: true; central would record it COMPLETED")
	}
}
