package host

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	connect "connectrpc.com/connect"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/protobuf/proto"

	attachv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/attach/v1"
	captureedgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/capture/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/capture/v1/capturev1connect"
	modelcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	"go.aledante.io/FlowSeer/src/common/spawn"
	agentcapture "go.aledante.io/FlowSeer/src/edge/agent/internal/capture"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/identity"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/subscribeloop"
	"go.aledante.io/FlowSeer/src/modules/capture"
	"go.aledante.io/FlowSeer/src/modules/capture/rawsocket"
)

func TestModules_DeclaresLaneAndCapture(t *testing.T) {
	t.Parallel()

	a := &assembly{}
	ca := &captureAssembly{}
	mods := modules(a, ca)
	if len(mods) != 2 {
		t.Fatalf("len(mods) = %d, want 2", len(mods))
	}
	if mods[0].Name != "lane" || mods[0].Leaf == nil || mods[0].Leaf.Setup == nil {
		t.Errorf("mods[0] = %+v, want lane module with non-nil leaf setup", mods[0])
	}
	if mods[1].Name != "capture" || mods[1].Leaf == nil || mods[1].Leaf.Setup == nil {
		t.Errorf("mods[1] = %+v, want capture module with non-nil leaf setup", mods[1])
	}
}

type fakeContactStream struct {
	delivered bool
}

func (s *fakeContactStream) Receive() bool {
	if !s.delivered {
		s.delivered = true
		return true
	}
	return false
}

func (s *fakeContactStream) Msg() *captureedgev1.SubscribeCaptureAssignmentsResponse {
	return &captureedgev1.SubscribeCaptureAssignmentsResponse{}
}

func (s *fakeContactStream) Err() error {
	return context.Canceled
}

func (s *fakeContactStream) Close() error {
	return nil
}

type noopCaptureHandler struct{}

func (noopCaptureHandler) Handle(_ context.Context, _ *captureedgev1.SubscribeCaptureAssignmentsResponse) error {
	return nil
}

func TestCaptureInstruments_RegisteredAndObservable(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	meter := mp.Meter("test")

	contact := &subscribeloop.Contact{}
	if err := registerCaptureInstruments(meter, contact); err != nil {
		t.Fatalf("registerCaptureInstruments: %v", err)
	}

	// Drive the contact counters using one subscribeloop iteration
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_ = subscribeloop.Run(ctx, subscribeloop.Config[captureedgev1.SubscribeCaptureAssignmentsResponse]{
		Open: func(_ context.Context) (subscribeloop.Stream[captureedgev1.SubscribeCaptureAssignmentsResponse], error) {
			cancel() // cancel after opening so Run stops after this attempt
			return &fakeContactStream{}, nil
		},
		Handler: noopCaptureHandler{},
		Events:  agentcapture.Events,
	}, contact)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("reader.Collect: %v", err)
	}

	counts := make(map[string]int64)
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if sum, ok := m.Data.(metricdata.Sum[int64]); ok {
				for _, dp := range sum.DataPoints {
					counts[m.Name] = dp.Value
				}
			}
		}
	}

	if got := counts["flowseer.edge.capture.connections"]; got != 1 {
		t.Errorf("connections counter = %d, want 1", got)
	}
	if got := counts["flowseer.edge.capture.messages"]; got != 1 {
		t.Errorf("messages counter = %d, want 1", got)
	}
	if _, ok := counts["flowseer.edge.capture.failures"]; !ok {
		t.Error("failures counter was not registered")
	}
}

type fakeCaptureServiceHandler struct {
	capturev1connect.UnimplementedCaptureEdgeServiceHandler

	subRequests chan *captureedgev1.SubscribeCaptureAssignmentsRequest
	startMsg    *captureedgev1.SubscribeCaptureAssignmentsResponse
}

func (f *fakeCaptureServiceHandler) SubscribeCaptureAssignments(
	ctx context.Context,
	req *connect.Request[captureedgev1.SubscribeCaptureAssignmentsRequest],
	stream *connect.ServerStream[captureedgev1.SubscribeCaptureAssignmentsResponse],
) error {
	select {
	case f.subRequests <- req.Msg:
	default:
	}
	if f.startMsg != nil {
		if err := stream.Send(f.startMsg); err != nil {
			return err
		}
	}
	<-ctx.Done()
	return ctx.Err()
}

func (f *fakeCaptureServiceHandler) UploadCapture(
	_ context.Context,
	stream *connect.ClientStream[captureedgev1.UploadCaptureRequest],
) (*connect.Response[captureedgev1.UploadCaptureResponse], error) {
	for stream.Receive() {
	}
	return connect.NewResponse(&captureedgev1.UploadCaptureResponse{}), nil
}

type testCaptureSource struct {
	frames chan rawsocket.Frame
}

func (s *testCaptureSource) Receive(_ context.Context) <-chan rawsocket.Frame {
	return s.frames
}

func (s *testCaptureSource) Stats() (uint64, uint64, error) {
	return 0, 0, nil
}

func (s *testCaptureSource) Close() error {
	return nil
}

func TestCaptureAssembly_OpenCaptureSource_PassedToRunner(t *testing.T) {
	t.Parallel()

	sessID := "0192e6a0-0000-7000-8000-000000000042"
	startMsg := captureedgev1.SubscribeCaptureAssignmentsResponse_builder{
		Start: modelcapturev1.CaptureSessionConfig_builder{
			Ref: modelcapturev1.CaptureSessionGlobalRef_builder{
				CaptureSession: modelcapturev1.CaptureSessionLocalRef_builder{Id: proto.String(sessID)}.Build(),
			}.Build(),
			Source: modelcapturev1.CaptureSource_builder{
				LocalInterface: modelcapturev1.LocalInterfaceSource_builder{
					InterfaceName: proto.String("eth0"),
					Promiscuous:   proto.Bool(true),
				}.Build(),
			}.Build(),
			Budget: modelcapturev1.CaptureBudget_builder{
				MaxPackets: proto.Uint64(1),
			}.Build(),
		}.Build(),
	}.Build()

	fakeServer := &fakeCaptureServiceHandler{
		subRequests: make(chan *captureedgev1.SubscribeCaptureAssignmentsRequest, 1),
		startMsg:    startMsg,
	}

	mux := http.NewServeMux()
	path, handler := capturev1connect.NewCaptureEdgeServiceHandler(fakeServer)
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	defer server.Close()

	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	enrollment := attachv1.EnrollResponse_builder{
		Edge: edgev1.EdgeGlobalRef_builder{
			Edge: edgev1.EdgeLocalRef_builder{Id: proto.String("test-edge")}.Build(),
		}.Build(),
		Audience: proto.String("flowseer-central"),
	}.Build()
	signer := identity.NewSigner(key, enrollment, time.Now)

	client := capturev1connect.NewCaptureEdgeServiceClient(http.DefaultClient, server.URL)

	sourceCalled := make(chan struct{})
	source := &testCaptureSource{frames: make(chan rawsocket.Frame, 1)}
	source.frames <- rawsocket.Frame{
		Data:           []byte("test packet"),
		OriginalLength: 11,
		CapturedAt:     time.Now(),
	}

	ca := &captureAssembly{
		cfg: &Config{},
		opts: Options{
			OpenCaptureSource: func(_ context.Context, cfg capture.Config) (capture.Source, bool, error) {
				if cfg.Source.GetLocalInterface().GetInterfaceName() == "eth0" {
					select {
					case <-sourceCalled:
					default:
						close(sourceCalled)
					}
				}
				return source, false, nil
			},
		},
		signer:        signer,
		captureClient: client,
	}

	attempt, err := ca.setup(context.Background())
	if err != nil {
		t.Fatalf("ca.setup: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runnerDone := make(chan error, 1)
	spawn.Go(ctx, "test runner", func() {
		runnerDone <- attempt.Runner(ctx)
	})

	select {
	case <-sourceCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for OpenCaptureSource to be called")
	}

	cancel()
	select {
	case <-runnerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for attempt.Runner to return")
	}
}
