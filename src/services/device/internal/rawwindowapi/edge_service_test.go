package rawwindowapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	connect "connectrpc.com/connect"

	edgeingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/ingest/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/ingest/v1/ingestv1connect"
	rawwindowv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/rawwindow/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/rawwindowapi"
)

// relay serves the edge API for the fixture's edge, which the handler reads from
// the context the way the signed-assertion middleware sets it.
type relay struct {
	*fixture
	svc    *rawwindowapi.EdgeService
	client ingestv1connect.RawWindowEdgeServiceClient
	ctx    context.Context
}

// newRelay starts the edge service with a resend interval too long to fire, so
// that only a change notification wakes a stream, unless the test passes a
// short one.
func newRelay(t *testing.T, resend time.Duration, edit ...func(*rawwindowapi.EdgeServiceConfig)) *relay {
	t.Helper()
	f := newFixture(t)
	cfg := rawwindowapi.EdgeServiceConfig{
		ResendInterval: resend,
		EdgeID:         func(context.Context) (string, error) { return f.edge, nil },
		EdgeTenant:     func(context.Context, string) (string, error) { return tenantA, nil },
	}
	for _, fn := range edit {
		fn(&cfg)
	}
	svc := rawwindowapi.NewEdgeService(f.store, cfg)

	path, handler := ingestv1connect.NewRawWindowEdgeServiceHandler(svc)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	t.Cleanup(cancel)
	return &relay{
		fixture: f,
		svc:     svc,
		client:  ingestv1connect.NewRawWindowEdgeServiceClient(server.Client(), server.URL),
		ctx:     ctx,
	}
}

const never = 10 * time.Minute

type stream = connect.ServerStreamForClient[edgeingestv1.SubscribeResponse]

func (r *relay) subscribeStream(generation uint64, epoch string) *stream {
	r.t.Helper()
	req := edgeingestv1.SubscribeRequest_builder{
		BootGeneration: new(generation),
		ProcessEpoch:   new(epoch),
		EnabledSources: sources("syslog"),
	}.Build()
	s, err := r.client.Subscribe(r.ctx, connect.NewRequest(req))
	if err != nil {
		r.t.Fatalf("subscribe: %v", err)
	}
	r.t.Cleanup(func() { _ = s.Close() })
	return s
}

func (r *relay) receive(s *stream) *edgeingestv1.SubscribeResponse {
	r.t.Helper()
	if !s.Receive() {
		r.t.Fatalf("stream ended while a frame was expected: %v", s.Err())
	}
	if err := protovalidate.Validate(s.Msg()); err != nil {
		r.t.Fatalf("frame violates its schema: %v", err)
	}
	return s.Msg()
}

func (r *relay) wantReady(s *stream) {
	r.t.Helper()
	if frame := r.receive(s); frame.GetReady() == nil {
		r.t.Fatalf("first frame = %v, want ready", frame)
	}
}

// wantControl reads the next frame and requires it to be the open or the close
// of the window at the revision, under the process's fence.
func (r *relay) wantControl(s *stream, window *rawwindowv1.RawWindowRecord, revision uint64, open bool) {
	r.t.Helper()
	frame := r.receive(s)
	control := frame.GetControl()
	if control == nil {
		r.t.Fatalf("frame = %v, want a control", frame)
	}
	if got := control.GetRef().GetRawWindow().GetId(); got != windowID(window) {
		r.t.Fatalf("control names window %s, want %s", got, windowID(window))
	}
	if control.GetRevision() != revision || (control.GetOpen() != nil) != open || (control.GetClose() != nil) == open {
		r.t.Fatalf("control = %v, want revision %d as an open=%v", control, revision, open)
	}
}

func (r *relay) reportActive(window *rawwindowv1.RawWindowRecord, epoch string, revision uint64, u *rawwindowv1.RawWindowUsage) error {
	r.t.Helper()
	req := edgeingestv1.ReportRequest_builder{
		BootGeneration: new(uint64(1)),
		ProcessEpoch:   new(epoch),
		Reports:        []*edgeingestv1.RawWindowReport{activeReport(window, revision, u, r.clock.Now())},
	}.Build()
	_, err := r.client.Report(r.ctx, connect.NewRequest(req))
	return err
}

func TestSubscribeSendsReadyFirstEvenWithNoWindows(t *testing.T) {
	r := newRelay(t, never)

	s := r.subscribeStream(1, epochOne)

	r.wantReady(s)
	rec := r.stored()
	if rec.GetBootGeneration() != 1 || rec.GetProcessEpoch() != epochOne || len(rec.GetEnabledSources()) != 1 {
		t.Errorf("stored fence = %d/%s with sources %v, want 1/%s with syslog", rec.GetBootGeneration(), rec.GetProcessEpoch(), rec.GetEnabledSources(), epochOne)
	}
}

func TestSubscribeSendsEveryOwedControlAfterReady(t *testing.T) {
	r := newRelay(t, never)
	r.mustSubscribe(1, epochOne, "syslog")
	first := r.open()
	second := r.open()

	s := r.subscribeStream(1, epochOne)

	r.wantReady(s)
	r.wantControl(s, first, 1, true)
	r.wantControl(s, second, 2, true)
}

func TestTheTickerResendsWithoutAnyNotification(t *testing.T) {
	r := newRelay(t, 10*time.Millisecond)
	s := r.subscribeStream(1, epochOne)
	r.wantReady(s)

	window := r.open()

	r.wantControl(s, window, 1, true)
	r.wantControl(s, window, 1, true)
}

func TestALostStartIsResentUntilTheEdgeReportsIt(t *testing.T) {
	r := newRelay(t, never)
	r.mustSubscribe(1, epochOne, "syslog")
	window := r.open()
	s := r.subscribeStream(1, epochOne)
	r.wantReady(s)
	r.wantControl(s, window, 1, true)

	// The edge never saw that start. Nothing else happens, and the start comes
	// again.
	r.svc.NotifyChange()
	r.wantControl(s, window, 1, true)
	if got := r.window(window).GetState().GetLifecycle(); got != rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_PENDING {
		t.Fatalf("lifecycle with no report = %v, want pending", got)
	}

	if err := r.reportActive(window, epochOne, 1, usage(0, 0)); err != nil {
		t.Fatalf("report: %v", err)
	}
	if got := r.window(window).GetState().GetLifecycle(); got != rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_ACTIVE {
		t.Fatalf("lifecycle after the report = %v, want active", got)
	}
	r.svc.NotifyChange()
	next := r.open()
	r.svc.NotifyChange()
	r.wantControl(s, next, 2, true)
}

func TestALostCloseIsResentWithoutAnotherOperatorCall(t *testing.T) {
	r := newRelay(t, never)
	r.mustSubscribe(1, epochOne, "syslog")
	window := r.open()
	r.mustReport(activeReport(window, 1, usage(12, 1), r.clock.Now()))
	if _, err := r.store.Close(t.Context(), tenantA, r.edge, windowID(window)); err != nil {
		t.Fatalf("close: %v", err)
	}
	s := r.subscribeStream(1, epochOne)
	r.wantReady(s)
	r.wantControl(s, window, 2, false)

	r.svc.NotifyChange()
	r.wantControl(s, window, 2, false)

	r.mustReport(terminalReport(window, 2, rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_OPERATOR, usage(12, 1), r.clock.Now()))
	r.svc.NotifyChange()
	next := r.open()
	r.svc.NotifyChange()
	r.wantControl(s, next, 3, true)
}

func TestAReconnectInTheSameEpochRedeliversAndKeepsTheCounters(t *testing.T) {
	r := newRelay(t, never)
	r.mustSubscribe(1, epochOne, "syslog")
	window := r.open()
	r.mustReport(activeReport(window, 1, usage(40, 2), r.clock.Now()))
	if _, err := r.store.Close(t.Context(), tenantA, r.edge, windowID(window)); err != nil {
		t.Fatalf("close: %v", err)
	}
	first := r.subscribeStream(1, epochOne)
	r.wantReady(first)
	r.wantControl(first, window, 2, false)
	if err := first.Close(); err != nil {
		t.Fatalf("close the first stream: %v", err)
	}

	second := r.subscribeStream(1, epochOne)

	r.wantReady(second)
	r.wantControl(second, window, 2, false)
	state := r.window(window).GetState()
	if state.GetLifecycle() != rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_ACTIVE || state.GetUsage().GetSelectedBytes() != 40 {
		t.Errorf("state after the reconnect = %v, want active with its 40 bytes", state)
	}
}

func TestANewGenerationEndsTheOldWindowsAndReissuesNone(t *testing.T) {
	r := newRelay(t, never)
	r.mustSubscribe(1, epochOne, "syslog")
	old := r.open()

	s := r.subscribeStream(2, epochTwo)
	r.wantReady(s)
	fresh := r.open()
	r.svc.NotifyChange()

	r.wantControl(s, fresh, 2, true)
	state := r.window(old).GetState()
	if state.GetLifecycle() != rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_TERMINAL ||
		state.GetStopReason() != rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_EDGE_RESTART {
		t.Errorf("old window state = %v, want terminal EDGE_RESTART", state)
	}
}

func TestAWindowClosedWhileTheEdgeWasAwayIsDeliveredOnlyAsAClose(t *testing.T) {
	r := newRelay(t, never)
	r.mustSubscribe(1, epochOne, "syslog")
	window := r.open()
	if _, err := r.store.Close(t.Context(), tenantA, r.edge, windowID(window)); err != nil {
		t.Fatalf("close: %v", err)
	}

	s := r.subscribeStream(1, epochOne)

	r.wantReady(s)
	r.wantControl(s, window, 2, false)
	next := r.open()
	r.svc.NotifyChange()
	r.wantControl(s, window, 2, false)
	r.wantControl(s, next, 3, true)
}

func TestAnUndeliveredStartSurvivesTheRevisionsOfOtherWindows(t *testing.T) {
	r := newRelay(t, never)
	r.mustSubscribe(1, epochOne, "syslog")
	first := r.open()
	second := r.open()
	if _, err := r.store.Close(t.Context(), tenantA, r.edge, windowID(second)); err != nil {
		t.Fatalf("close: %v", err)
	}

	s := r.subscribeStream(1, epochOne)

	r.wantReady(s)
	r.wantControl(s, first, 1, true)
	r.wantControl(s, second, 3, false)
}

func TestASubscriptionThatDoesNotOutrankTheStoredProcessIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name       string
		generation uint64
		epoch      string
	}{
		{name: "an older generation", generation: 1, epoch: epochOne},
		{name: "the current generation with another epoch", generation: 2, epoch: epochThree},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRelay(t, never)
			r.mustSubscribe(2, epochTwo, "syslog")
			s := r.subscribeStream(tc.generation, tc.epoch)

			if s.Receive() {
				t.Fatalf("frame = %v, want the subscription refused", s.Msg())
			}

			wantConnectCode(t, s.Err(), connect.CodeFailedPrecondition)
			if got := payloadCode(t, s.Err()); got != "rawwindowapi/stale-fence" {
				t.Errorf("error code = %s, want rawwindowapi/stale-fence", got)
			}
		})
	}
}

func TestANewerSubscriptionClosesAnOldStreamThatIsStillOpen(t *testing.T) {
	r := newRelay(t, never)
	old := r.subscribeStream(1, epochOne)
	r.wantReady(old)

	newer := r.subscribeStream(2, epochTwo)
	r.wantReady(newer)

	controls := 0
	for old.Receive() {
		if old.Msg().GetControl() != nil {
			controls++
		}
	}
	if controls != 0 {
		t.Errorf("the superseded stream received %d controls", controls)
	}
	wantConnectCode(t, old.Err(), connect.CodeAborted)

	window := r.open()
	r.svc.NotifyChange()
	frame := r.receive(newer)
	if frame.GetControl().GetBootGeneration() != 2 || frame.GetControl().GetProcessEpoch() != epochTwo ||
		frame.GetControl().GetRef().GetRawWindow().GetId() != windowID(window) {
		t.Errorf("the newer stream got %v, want the new window's control under generation 2", frame)
	}
}

func TestASupersededStreamSendsNothingEvenWhenAControlIsOwed(t *testing.T) {
	r := newRelay(t, never)
	r.mustSubscribe(1, epochOne, "syslog")
	old := r.subscribeStream(1, epochOne)
	r.wantReady(old)

	// The newer process takes the record without waking the old stream, which
	// is then parked with a control owed to someone else.
	r.mustSubscribe(2, epochTwo, "syslog")
	r.open()
	r.svc.NotifyChange()

	controls := 0
	for old.Receive() {
		if old.Msg().GetControl() != nil {
			controls++
		}
	}
	if controls != 0 {
		t.Errorf("the superseded stream sent %d controls into the old process", controls)
	}
	wantConnectCode(t, old.Err(), connect.CodeAborted)
}

func TestSubscribeRefusesAnInvalidRequest(t *testing.T) {
	r := newRelay(t, never)
	req := edgeingestv1.SubscribeRequest_builder{
		BootGeneration: new(uint64(1)),
		ProcessEpoch:   new("not-a-uuid"),
	}.Build()
	s, err := r.client.Subscribe(r.ctx, connect.NewRequest(req))
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if s.Receive() {
		t.Fatalf("frame = %v, want the request refused", s.Msg())
	}

	wantConnectCode(t, s.Err(), connect.CodeInvalidArgument)
}

func TestReportRefusals(t *testing.T) {
	r := newRelay(t, never)
	r.mustSubscribe(1, epochOne, "syslog")
	window := r.open()
	other := newFixture(t)
	other.mustSubscribe(1, epochOne, "syslog")
	foreign := other.open()

	t.Run("another edge's window", func(t *testing.T) {
		req := edgeingestv1.ReportRequest_builder{
			BootGeneration: new(uint64(1)),
			ProcessEpoch:   new(epochOne),
			Reports:        []*edgeingestv1.RawWindowReport{activeReport(foreign, 1, usage(1, 1), r.clock.Now())},
		}.Build()

		_, err := r.client.Report(r.ctx, connect.NewRequest(req))

		wantConnectCode(t, err, connect.CodePermissionDenied)
		if got := other.window(foreign).GetState().GetLifecycle(); got != rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_PENDING {
			t.Errorf("the other edge's window became %v", got)
		}
	})

	t.Run("another epoch", func(t *testing.T) {
		err := r.reportActive(window, epochTwo, 1, usage(1, 1))

		wantConnectCode(t, err, connect.CodeFailedPrecondition)
	})

	t.Run("an older generation", func(t *testing.T) {
		r.mustSubscribe(2, epochTwo, "syslog")

		err := r.reportActive(window, epochOne, 1, usage(1, 1))

		wantConnectCode(t, err, connect.CodeFailedPrecondition)
	})
}

func TestReportRefusesARevisionTheCentralNeverIssued(t *testing.T) {
	r := newRelay(t, never)
	r.mustSubscribe(1, epochOne, "syslog")
	window := r.open()

	err := r.reportActive(window, epochOne, 9, usage(1, 1))

	wantConnectCode(t, err, connect.CodeInvalidArgument)
}

func TestACallerWithoutAVerifiedEdgeIsUnauthenticated(t *testing.T) {
	r := newRelay(t, never, func(cfg *rawwindowapi.EdgeServiceConfig) {
		cfg.EdgeID = func(context.Context) (string, error) { return "", errs.Msg("no assertion") }
	})
	r.mustSubscribe(1, epochOne, "syslog")
	window := r.open()

	err := r.reportActive(window, epochOne, 1, usage(0, 0))
	wantConnectCode(t, err, connect.CodeUnauthenticated)

	s := r.subscribeStream(1, epochOne)
	if s.Receive() {
		t.Fatalf("frame = %v, want the subscription refused", s.Msg())
	}
	wantConnectCode(t, s.Err(), connect.CodeUnauthenticated)
}

func TestAnEdgeTheTenantResolverCannotPlaceIsUnavailable(t *testing.T) {
	r := newRelay(t, never, func(cfg *rawwindowapi.EdgeServiceConfig) {
		cfg.EdgeTenant = func(context.Context, string) (string, error) { return "", errs.Msg("edge store down") }
	})

	s := r.subscribeStream(1, epochOne)
	if s.Receive() {
		t.Fatalf("frame = %v, want the subscription refused", s.Msg())
	}

	wantConnectCode(t, s.Err(), connect.CodeUnavailable)
}
