package rawwindow_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	connect "connectrpc.com/connect"

	edgeingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/ingest/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/ingest/v1/ingestv1connect"
	rawwindowv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/rawwindow/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/spawn"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/rawwindow"
)

// fakeCentral serves the edge service a test scripts: the frames Subscribe
// sends, and an error list Report answers with in turn.
type fakeCentral struct {
	ingestv1connect.UnimplementedRawWindowEdgeServiceHandler

	frames []*edgeingestv1.SubscribeResponse

	mu          sync.Mutex // guards the fields below
	subscribed  []*edgeingestv1.SubscribeRequest
	reports     []*edgeingestv1.ReportRequest
	reportErrs  []error
	reportsSeen chan struct{}
}

func (f *fakeCentral) Subscribe(_ context.Context, req *connect.Request[edgeingestv1.SubscribeRequest], stream *connect.ServerStream[edgeingestv1.SubscribeResponse]) error {
	f.mu.Lock()
	f.subscribed = append(f.subscribed, req.Msg)
	f.mu.Unlock()
	for _, frame := range f.frames {
		if err := stream.Send(frame); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeCentral) Report(_ context.Context, req *connect.Request[edgeingestv1.ReportRequest]) (*connect.Response[edgeingestv1.ReportResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reports = append(f.reports, req.Msg)
	defer func() {
		select {
		case f.reportsSeen <- struct{}{}:
		default:
		}
	}()
	if len(f.reportErrs) > 0 {
		err := f.reportErrs[0]
		f.reportErrs = f.reportErrs[1:]
		if err != nil {
			return nil, err
		}
	}
	return connect.NewResponse(&edgeingestv1.ReportResponse{}), nil
}

func (f *fakeCentral) reportsReceived() []*edgeingestv1.ReportRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.reports)
}

func serveCentral(t *testing.T, central *fakeCentral) ingestv1connect.RawWindowEdgeServiceClient {
	t.Helper()
	mux := http.NewServeMux()
	path, handler := ingestv1connect.NewRawWindowEdgeServiceHandler(central)
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return ingestv1connect.NewRawWindowEdgeServiceClient(server.Client(), server.URL)
}

func TestOpenCarriesTheFenceAndTheEnabledSources(t *testing.T) {
	c := newClocks()
	ctrl := newController(t, c)
	central := &fakeCentral{frames: []*edgeingestv1.SubscribeResponse{
		edgeingestv1.SubscribeResponse_builder{Ready: &edgeingestv1.RawWindowReady{}}.Build(),
		edgeingestv1.SubscribeResponse_builder{Control: open(t, windowOne, 1, deviceSelector(deviceA), c.Wall().Add(time.Hour), 10)}.Build(),
	}}
	client := serveCentral(t, central)

	stream, err := rawwindow.Open(client, ctrl)(context.Background())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	handler := rawwindow.Handler{Controller: ctrl}
	var frames int
	for stream.Receive() {
		frames++
		if err := handler.Handle(context.Background(), stream.Msg()); err != nil {
			t.Fatalf("Handle: %v", err)
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream ended with %v, want a clean end", err)
	}
	if frames != 2 {
		t.Fatalf("received %d frames, want the ready frame and one control", frames)
	}

	central.mu.Lock()
	request := central.subscribed[0]
	central.mu.Unlock()
	if request.GetBootGeneration() != generation || request.GetProcessEpoch() != epoch {
		t.Errorf("subscribe fence = %d/%s, want %d/%s", request.GetBootGeneration(), request.GetProcessEpoch(), generation, epoch)
	}
	var sources []string
	for _, source := range request.GetEnabledSources() {
		sources = append(sources, source.GetKey())
	}
	if !slices.Equal(sources, []string{syslogSource}) {
		t.Errorf("enabled sources = %v, want [%s]", sources, syslogSource)
	}
	if got := ctrl.Select(obs(deviceA, 1)); !slices.Equal(got, []string{windowOne}) {
		t.Errorf("the control the stream delivered was not applied: paid %v", got)
	}
}

func TestHandlerReturnsTheControllersRefusal(t *testing.T) {
	c := newClocks()
	ctrl := newController(t, c)
	control := open(t, windowOne, 1, deviceSelector(deviceA), c.Wall().Add(time.Hour), 10)
	control.SetBootGeneration(generation + 1)

	err := rawwindow.Handler{Controller: ctrl}.Handle(context.Background(), edgeingestv1.SubscribeResponse_builder{Control: control}.Build())

	if code, ok := errs.CodeOf(err); !ok || code != rawwindow.ErrCodeControl {
		t.Errorf("Handle error = %v, want code %v", err, rawwindow.ErrCodeControl)
	}
}

func TestReporterKeepsAFailedReportOwedAndSendsItAgain(t *testing.T) {
	c := newClocks()
	ctrl := newController(t, c)
	apply(t, ctrl, open(t, windowOne, 1, deviceSelector(deviceA), c.Wall().Add(time.Hour), 100))
	ctrl.Select(obs(deviceA, 7))
	central := &fakeCentral{
		reportErrs:  []error{connect.NewError(connect.CodeUnavailable, errors.New("central is restarting"))},
		reportsSeen: make(chan struct{}, 8),
	}
	client := serveCentral(t, central)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	spawn.Go(ctx, "reporter", func() {
		done <- rawwindow.RunReporter(ctx, rawwindow.ReporterConfig{Client: client, Controller: ctrl, Interval: 5 * time.Millisecond})
	}, spawn.ReportTo(func(err error) { done <- err }))

	waitForReports(t, central, 2)
	for deadline := time.Now().Add(10 * time.Second); len(ctrl.PendingReports(10)) != 0; {
		if time.Now().After(deadline) {
			t.Fatal("the report central accepted stayed owed")
		}
		time.Sleep(time.Millisecond) // polling the controller, which has no signal for an acknowledgement
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("RunReporter returned %v, want nil when its context ends", err)
	}

	reports := central.reportsReceived()
	first, second := reports[0], reports[1]
	if first.GetBootGeneration() != generation || first.GetProcessEpoch() != epoch {
		t.Errorf("report fence = %d/%s, want %d/%s", first.GetBootGeneration(), first.GetProcessEpoch(), generation, epoch)
	}
	for i, request := range []*edgeingestv1.ReportRequest{first, second} {
		if got := request.GetReports()[0].GetState().GetUsage().GetSelectedBytes(); got != 7 {
			t.Errorf("report %d carries %d bytes, want the cumulative 7", i, got)
		}
	}
	if got := len(ctrl.PendingReports(10)); got != 0 {
		t.Errorf("%d reports are still owed after central accepted one, want none", got)
	}
}

func TestReporterSendsAtMostSixteenReportsPerCall(t *testing.T) {
	c := newClocks()
	ctrl := newController(t, c)
	for i := range 20 {
		id := fmt.Sprintf("0192e6a0-0000-7000-8000-0000000b%04x", i)
		apply(t, ctrl, open(t, id, uint64(i+1), deviceSelector(fmt.Sprintf("0192e6a0-0000-7000-8000-0000000c%04x", i)), c.Wall().Add(time.Hour), 10))
	}
	central := &fakeCentral{reportsSeen: make(chan struct{}, 8)}
	client := serveCentral(t, central)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	spawn.Go(ctx, "reporter", func() {
		_ = rawwindow.RunReporter(ctx, rawwindow.ReporterConfig{Client: client, Controller: ctrl, Interval: time.Hour})
	})

	waitForReports(t, central, 2)
	reports := central.reportsReceived()
	if got := []int{len(reports[0].GetReports()), len(reports[1].GetReports())}; !slices.Equal(got, []int{16, 4}) {
		t.Errorf("reports per call = %v, want [16 4]", got)
	}
	for _, request := range reports {
		if err := protovalidate.Validate(request); err != nil {
			t.Errorf("report request fails its schema rules: %v", err)
		}
	}
}

func TestReporterReportsAnExpiryWithoutTraffic(t *testing.T) {
	c := newClocks()
	ctrl := newController(t, c)
	apply(t, ctrl, open(t, windowOne, 1, deviceSelector(deviceA), c.Wall().Add(time.Minute), 10))
	central := &fakeCentral{reportsSeen: make(chan struct{}, 8)}
	client := serveCentral(t, central)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	spawn.Go(ctx, "reporter", func() {
		_ = rawwindow.RunReporter(ctx, rawwindow.ReporterConfig{Client: client, Controller: ctrl, Interval: time.Hour})
	})
	waitForReports(t, central, 1)

	c.advance(time.Minute)
	ctrl.Sweep()
	waitForReports(t, central, 2)

	state := central.reportsReceived()[1].GetReports()[0].GetState()
	if state.GetStopReason() != rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_EXPIRED {
		t.Errorf("state = %v, want the expiry reported while no record arrived", state)
	}
}

func waitForReports(t *testing.T, central *fakeCentral, want int) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for len(central.reportsReceived()) < want {
		select {
		case <-central.reportsSeen:
		case <-deadline:
			t.Fatalf("timed out waiting for %d report calls, have %d", want, len(central.reportsReceived()))
		}
	}
}
