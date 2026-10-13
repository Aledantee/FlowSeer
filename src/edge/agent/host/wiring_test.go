package host

import (
	"context"
	"crypto/ed25519"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	attachv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/attach/v1"
	edgeingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/ingest/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/ingest/v1/ingestv1connect"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	rawwindowv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/rawwindow/v1"
	agentv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/agent/v1"
	"go.aledante.io/FlowSeer/src/common/spawn"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/identity"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/rawwindow"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/syslogsource"
)

func TestProductionLaneQueueCapacity(t *testing.T) {
	if got := (&assembly{}).laneConfig(&laneReporter{}, nil).QueueCapacity; got != 4 {
		t.Errorf("production lane QueueCapacity = %d, want 4", got)
	}
}

const (
	wiringEdgeID   = "0192e6a0-0000-7000-8000-0000000000ed"
	wiringWindowID = "0192e6a0-0000-7000-8000-000000000a01"
	wiringDeviceID = "0192e6a0-0000-7000-8000-0000000000d1"
)

type errorLogs struct {
	mu     sync.Mutex // guards events
	events []string
}

func (h *errorLogs) Enabled(context.Context, slog.Level) bool { return true }
func (h *errorLogs) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *errorLogs) WithGroup(string) slog.Handler            { return h }

func (h *errorLogs) Handle(_ context.Context, record slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	record.Attrs(func(a slog.Attr) bool {
		if a.Key == "otel.event.name" {
			h.events = append(h.events, a.Value.String())
		}
		return true
	})
	return nil
}

func (h *errorLogs) seen(name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Contains(h.events, name)
}

func TestRawWindowControllerAdvancesTheGenerationAndRestartsEmpty(t *testing.T) {
	t.Parallel()

	store, err := identity.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	cfg := oneListenerConfig()
	log := slog.New(slog.DiscardHandler)

	first := newRawWindowController(store, cfg, Options{}, log)
	if first == nil {
		t.Fatal("first controller is nil")
	}
	windowStart := time.Now()
	if err := first.Apply(rawWindowOpenControl(t, first, windowStart.Add(time.Hour), 100)); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := first.Select(rawwindow.Observation{Source: syslogsource.SourceKey, DeviceID: wiringDeviceID, Bytes: 6}); len(got) != 1 {
		t.Fatalf("first process paid %v, want the window", got)
	}

	second := newRawWindowController(store, cfg, Options{}, log)

	if second.BootGeneration() != first.BootGeneration()+1 {
		t.Errorf("generation = %d, want %d: the counter must advance on every process start", second.BootGeneration(), first.BootGeneration()+1)
	}
	if second.Epoch() == first.Epoch() {
		t.Error("a restarted process reused its epoch")
	}
	if got := second.Select(rawwindow.Observation{Source: syslogsource.SourceKey, DeviceID: wiringDeviceID, Bytes: 1}); got != nil {
		t.Errorf("a restarted process paid %v for a window it never held", got)
	}
}

func TestRawWindowsAreDisabledWhenTheGenerationCannotAdvance(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := identity.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := os.WriteFile(dir+"/raw-window-epoch.textproto", []byte("boot_generation: 0\n"), 0o600); err != nil {
		t.Fatalf("seed the corrupt counter: %v", err)
	}
	logs := &errorLogs{}

	got := newRawWindowController(store, oneListenerConfig(), Options{}, slog.New(logs))

	if got != nil {
		t.Error("a controller exists although the boot generation could not advance")
	}
	if !logs.seen("flowseer.edge.raw_window.disabled") {
		t.Error("the degraded state was not logged as flowseer.edge.raw_window.disabled")
	}
	setUp, _ := runModule(t, (&rawWindowAssembly{controller: got}).module())
	if setUp {
		t.Error("the raw window module started without a controller")
	}
}

func oneListenerConfig() *Config {
	return syslogHostConfig(&agentv1.AgentSyslog_builder{}, "127.0.0.1:0")
}

func TestRawWindowModuleNeedsASourceToCollectFrom(t *testing.T) {
	t.Parallel()

	withSyslog := newRawWindowController(mustStore(t), oneListenerConfig(), Options{}, slog.New(slog.DiscardHandler))
	withoutSyslog := newRawWindowController(mustStore(t), &Config{}, Options{}, slog.New(slog.DiscardHandler))

	if got := withSyslog.Sources(); !slices.Equal(got, []string{syslogsource.SourceKey}) {
		t.Errorf("sources with syslog configured = %v, want [%s]", got, syslogsource.SourceKey)
	}
	if got := withoutSyslog.Sources(); len(got) != 0 {
		t.Errorf("sources without syslog = %v, want none", got)
	}
}

func mustStore(t *testing.T) *identity.Store {
	t.Helper()
	store, err := identity.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

func rawWindowOpenControl(t *testing.T, c *rawwindow.Controller, expires time.Time, maxBytes uint64) *edgeingestv1.RawWindowControl {
	t.Helper()
	ref := rawwindowv1.RawWindowGlobalRef_builder{
		Edge:      edgev1.EdgeGlobalRef_builder{Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(wiringEdgeID)}.Build()}.Build(),
		RawWindow: rawwindowv1.RawWindowLocalRef_builder{Id: proto.String(wiringWindowID)}.Build(),
	}.Build()
	return edgeingestv1.RawWindowControl_builder{
		BootGeneration: proto.Uint64(c.BootGeneration()),
		ProcessEpoch:   proto.String(c.Epoch()),
		Ref:            ref,
		Revision:       proto.Uint64(1),
		Open: rawwindowv1.RawWindowConfig_builder{
			Ref: ref,
			Selector: rawwindowv1.RawWindowSelector_builder{
				Device: inventoryv1.DeviceGlobalRef_builder{
					Device: inventoryv1.DeviceLocalRef_builder{Id: proto.String(wiringDeviceID)}.Build(),
				}.Build(),
			}.Build(),
			ExpiresAt: timestamppb.New(expires),
			MaxBytes:  proto.Uint64(maxBytes),
			Authorization: rawwindowv1.RawWindowAuthorization_builder{
				RequestedBy: identityv1.OperatorRef_builder{Issuer: proto.String("https://idp.example.com"), Subject: proto.String("op")}.Build(),
				Reason:      proto.String("why"),
			}.Build(),
		}.Build(),
	}.Build()
}

// streamingCentral serves the control stream: a ready frame, then the open
// control for a window, then it holds the stream until the caller leaves.
type streamingCentral struct {
	ingestv1connect.UnimplementedRawWindowEdgeServiceHandler

	control func(*rawwindow.Controller) *edgeingestv1.RawWindowControl
	ctrl    *rawwindow.Controller

	mu         sync.Mutex // guards the fields below
	headers    []string
	requests   []*edgeingestv1.SubscribeRequest
	sentReady  chan struct{}
	subscribed chan struct{}
}

func (c *streamingCentral) Subscribe(ctx context.Context, req *connect.Request[edgeingestv1.SubscribeRequest], stream *connect.ServerStream[edgeingestv1.SubscribeResponse]) error {
	c.mu.Lock()
	c.headers = append(c.headers, req.Header().Get("Authorization"))
	c.requests = append(c.requests, req.Msg)
	c.mu.Unlock()
	select {
	case c.subscribed <- struct{}{}:
	default:
	}

	if err := stream.Send(edgeingestv1.SubscribeResponse_builder{Ready: &edgeingestv1.RawWindowReady{}}.Build()); err != nil {
		return err
	}
	select {
	case c.sentReady <- struct{}{}:
	default:
	}
	if err := stream.Send(edgeingestv1.SubscribeResponse_builder{Control: c.control(c.ctrl)}.Build()); err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}

func (c *streamingCentral) Report(context.Context, *connect.Request[edgeingestv1.ReportRequest]) (*connect.Response[edgeingestv1.ReportResponse], error) {
	return connect.NewResponse(&edgeingestv1.ReportResponse{}), nil
}

func signedRawWindowClient(t *testing.T, central *streamingCentral) ingestv1connect.RawWindowEdgeServiceClient {
	t.Helper()
	mux := http.NewServeMux()
	path, handler := ingestv1connect.NewRawWindowEdgeServiceHandler(central)
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	enrollment := attachv1.EnrollResponse_builder{
		Edge:     edgev1.EdgeGlobalRef_builder{Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(wiringEdgeID)}.Build()}.Build(),
		Audience: proto.String("flowseer-central"),
	}.Build()
	signer := identity.NewSigner(key, enrollment, time.Now)
	httpClient := &http.Client{Transport: identity.SigningTransport(server.Client().Transport, signer)}
	return ingestv1connect.NewRawWindowEdgeServiceClient(httpClient, server.URL)
}

// runRawWindowAttempt sets one module attempt up and runs it until the returned
// stop function is called.
func runRawWindowAttempt(t *testing.T, ra *rawWindowAssembly) (stop func()) {
	t.Helper()
	attempt, err := ra.setup(context.Background())
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	spawn.Go(ctx, "raw window attempt", func() { done <- attempt.Runner(ctx) }, spawn.ReportTo(func(err error) { done <- err }))
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("the raw window attempt did not stop")
		}
	}
}

func waitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// TestRawWindowModuleOpensASignedStreamAndAppliesWhatFollowsReady runs a real
// attempt against a fake central that sends the ready frame with nothing owed,
// then a control. The stream carries an assertion, the fence, and the enabled
// sources, and a control sent after ready is applied.
func TestRawWindowModuleOpensASignedStreamAndAppliesWhatFollowsReady(t *testing.T) {
	t.Parallel()

	ctrl := newRawWindowController(mustStore(t), oneListenerConfig(), Options{}, slog.New(slog.DiscardHandler))
	central := &streamingCentral{
		ctrl:       ctrl,
		sentReady:  make(chan struct{}, 4),
		subscribed: make(chan struct{}, 4),
		control: func(c *rawwindow.Controller) *edgeingestv1.RawWindowControl {
			return rawWindowOpenControl(t, c, time.Now().Add(time.Hour), 100)
		},
	}
	ra := &rawWindowAssembly{cfg: &Config{}, controller: ctrl, client: signedRawWindowClient(t, central)}

	stop := runRawWindowAttempt(t, ra)
	defer stop()
	waitSignal(t, central.subscribed, "the subscribe call")
	waitSignal(t, central.sentReady, "the ready frame")

	deadline := time.Now().Add(10 * time.Second)
	var paid []string
	for time.Now().Before(deadline) && len(paid) == 0 {
		paid = ctrl.Select(rawwindow.Observation{Source: syslogsource.SourceKey, DeviceID: wiringDeviceID, Bytes: 1})
		time.Sleep(5 * time.Millisecond) // polling for a control applied on another goroutine
	}
	if !slices.Equal(paid, []string{wiringWindowID}) {
		t.Fatalf("a control sent after ready paid %v, want the window", paid)
	}

	central.mu.Lock()
	defer central.mu.Unlock()
	if central.headers[0] == "" {
		t.Error("the subscribe call carried no assertion")
	}
	request := central.requests[0]
	if request.GetBootGeneration() != ctrl.BootGeneration() || request.GetProcessEpoch() != ctrl.Epoch() {
		t.Errorf("subscribe fence = %d/%s, want %d/%s", request.GetBootGeneration(), request.GetProcessEpoch(), ctrl.BootGeneration(), ctrl.Epoch())
	}
	if len(request.GetEnabledSources()) != 1 || request.GetEnabledSources()[0].GetKey() != syslogsource.SourceKey {
		t.Errorf("enabled sources = %v, want [%s]", request.GetEnabledSources(), syslogsource.SourceKey)
	}
}

// TestModuleRetriesKeepWindowBudgetsAndOnlyAProcessRestartResetsThem runs two
// attempts over one controller, as the supervisor does after a failure, and
// shows the second sees the first's spending. A replayed start must not refill
// it.
func TestModuleRetriesKeepWindowBudgetsAndOnlyAProcessRestartResetsThem(t *testing.T) {
	t.Parallel()

	ctrl := newRawWindowController(mustStore(t), oneListenerConfig(), Options{}, slog.New(slog.DiscardHandler))
	expires := time.Now().Add(time.Hour)
	central := &streamingCentral{
		ctrl:       ctrl,
		sentReady:  make(chan struct{}, 4),
		subscribed: make(chan struct{}, 4),
		control: func(c *rawwindow.Controller) *edgeingestv1.RawWindowControl {
			return rawWindowOpenControl(t, c, expires, 10)
		},
	}
	ra := &rawWindowAssembly{cfg: &Config{}, controller: ctrl, client: signedRawWindowClient(t, central)}

	stop := runRawWindowAttempt(t, ra)
	waitSignal(t, central.sentReady, "the first ready frame")
	deadline := time.Now().Add(10 * time.Second)
	for len(ctrl.Select(rawwindow.Observation{Source: syslogsource.SourceKey, DeviceID: wiringDeviceID, Bytes: 6})) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the window never opened")
		}
		time.Sleep(5 * time.Millisecond) // polling for a control applied on another goroutine
	}
	stop()

	stop = runRawWindowAttempt(t, ra)
	defer stop()
	waitSignal(t, central.sentReady, "the second ready frame")
	time.Sleep(50 * time.Millisecond) // the replayed start has no signal of its own; a refill would show within this window
	if got := ctrl.Select(rawwindow.Observation{Source: syslogsource.SourceKey, DeviceID: wiringDeviceID, Bytes: 5}); got != nil {
		t.Errorf("after a module retry with a replayed start, a 5 byte record paid %v: only 4 of 10 bytes remain", got)
	}
}
