package host_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	connect "connectrpc.com/connect"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	devicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/device/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/device/v1/devicev1connect"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/deviceapi"
	"go.aledante.io/FlowSeer/src/services/device/internal/host"
	"go.aledante.io/FlowSeer/src/services/device/internal/journal"
	"go.aledante.io/FlowSeer/src/services/device/internal/telemetry"
)

// theCause is the sentence an operator must never see and an engineer must
// always find: the kind of internal detail the error-wire record keeps off a
// client boundary.
const theCause = "bucket device-lanes at nats://10.0.0.4:4222 refused the read"

// failing answers every call the way a real handler does — with the journal's
// failure rendered through deviceapi's own table — and records whether it ran.
type failing struct {
	devicev1connect.UnimplementedDeviceServiceHandler
	entered bool
}

func (f *failing) ApplyInterfaceDescription(
	context.Context, *connect.Request[devicev1.ApplyInterfaceDescriptionRequest],
) (*connect.Response[devicev1.ApplyInterfaceDescriptionResponse], error) {
	f.entered = true
	return nil, deviceapi.ClientErrors.Wrap(
		errs.New().Code(journal.ErrCodeStore).Attr("device", "0192e6a0-0000-7000-8000-0000000000d1").
			Msg(theCause))
}

// serve runs the handler behind both interceptors and returns a client for it.
func serve(t *testing.T, handler devicev1connect.DeviceServiceHandler, log *slog.Logger) devicev1connect.DeviceServiceClient {
	t.Helper()
	client, _ := serveWithMetrics(t, handler, log)
	return client
}

// serveWithMetrics runs the handler behind both interceptors and hands back a
// client and the reader the duration histogram lands in.
func serveWithMetrics(
	t *testing.T, handler devicev1connect.DeviceServiceHandler, log *slog.Logger,
) (devicev1connect.DeviceServiceClient, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	view, err := telemetry.NewView(telemetry.ViewConfig{
		MeterProvider: sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)),
		Logger:        log,
	})
	if err != nil {
		t.Fatalf("NewView: %v", err)
	}
	path, h := devicev1connect.NewDeviceServiceHandler(handler, connect.WithInterceptors(
		host.TelemetryInterceptor(log, view),
		host.ValidatingInterceptor(),
	))
	mux := http.NewServeMux()
	mux.Handle(path, h)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return devicev1connect.NewDeviceServiceClient(server.Client(), server.URL), reader
}

// The failure the caller is given and the failure the log records are two
// different things, on purpose. This is the one place that can drop the second
// while looking correct, so the test asserts the cause arrives — and asserts
// what the naive forms would have recorded instead.
func TestTheInterceptorLogsTheCauseTheCallerNeverSees(t *testing.T) {
	var logged bytes.Buffer
	handler := &failing{}
	client := serve(t, handler, slog.New(slog.NewJSONHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug})))

	_, err := client.ApplyInterfaceDescription(context.Background(), connect.NewRequest(validApply()))
	if err == nil {
		t.Fatal("the call succeeded")
	}

	// What the caller got: the sanitized sentence, and none of the cause.
	if got := connect.CodeOf(err); got != connect.CodeUnavailable {
		t.Errorf("code = %v, want unavailable", got)
	}
	if strings.Contains(err.Error(), theCause) {
		t.Fatalf("the caller was told the cause: %v", err)
	}
	if !strings.Contains(err.Error(), "cannot be reached right now") {
		t.Fatalf("the caller was not given the sanitized sentence: %v", err)
	}

	// What the log got: the cause, the code an engineer greps by, and the
	// attribute the error carried.
	record := logged.String()
	for _, want := range []string{theCause, "journal/store", "0192e6a0-0000-7000-8000-0000000000d1", "unavailable"} {
		if !strings.Contains(record, want) {
			t.Errorf("the log does not carry %q: %s", want, record)
		}
	}
}

// The naive forms are why the interceptor exists, and every one of them looks
// correct at the call site. Each renders the sentence the caller was given and
// drops the cause behind it, including errs.From, which recovers the code and
// the attributes but renders its message through the client-facing wrapper.
//
// This asserts the loss in both directions. If connecterr ever stopped
// sanitizing, these would start carrying the cause and this test would say so
// — which is the same news from the other side, because the text it would be
// carrying is the text that crosses the wire.
func TestEveryNaiveRenderingOfAHandlerErrorDropsTheCause(t *testing.T) {
	err := deviceapi.ClientErrors.Wrap(errs.New().Code(journal.ErrCodeStore).Msg(theCause))

	naive := map[string]string{
		"%v":             fmt.Sprintf("%v", err),
		"%s":             fmt.Sprintf("%s", err),
		"Error()":        err.Error(),
		"errs.From(err)": errs.From(err).Msg("apply interface description").Error(),
	}
	for name, rendered := range naive {
		if strings.Contains(rendered, theCause) {
			t.Errorf("%s now carries the cause (%q); the wire carries it too", name, rendered)
		}
	}

	// What the interceptor does instead: reach past the client-facing wrapper
	// to the failure that still holds the chain.
	var internal *errs.Error
	if !errors.As(err, &internal) {
		t.Fatal("the chain no longer reaches the failure")
	}
	if !strings.Contains(internal.Error(), theCause) {
		t.Fatalf("the recovered failure names no cause: %v", internal)
	}
}

func TestASuccessfulCallLogsNothing(t *testing.T) {
	var logged bytes.Buffer
	client := serve(t, &succeeding{}, slog.New(slog.NewJSONHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug})))

	if _, err := client.ApplyInterfaceDescription(context.Background(), connect.NewRequest(validApply())); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if logged.Len() != 0 {
		t.Fatalf("a successful call logged: %s", logged.String())
	}
}

type succeeding struct {
	devicev1connect.UnimplementedDeviceServiceHandler
}

func (s *succeeding) ApplyInterfaceDescription(
	context.Context, *connect.Request[devicev1.ApplyInterfaceDescriptionRequest],
) (*connect.Response[devicev1.ApplyInterfaceDescriptionResponse], error) {
	return connect.NewResponse(&devicev1.ApplyInterfaceDescriptionResponse{}), nil
}

// abandoned is a handler whose own context was canceled — the caller hung
// up mid-call — which is what it returns.
type abandoned struct {
	devicev1connect.UnimplementedDeviceServiceHandler
	entered bool
}

func (a *abandoned) ApplyInterfaceDescription(
	context.Context, *connect.Request[devicev1.ApplyInterfaceDescriptionRequest],
) (*connect.Response[devicev1.ApplyInterfaceDescriptionResponse], error) {
	a.entered = true
	return nil, context.Canceled
}

// A caller that hangs up has not been failed by the service, and an edge
// reconnecting its stream would otherwise log an error each time.
//
// The handler has to run for this to test anything. An earlier version
// canceled the client's context before the call, so http.Client.Do failed
// locally, no request reached the server, and the assertion was made against
// a log buffer that was empty because neither the handler nor the
// interceptor had ever run — the branch it names had no coverage at all. The
// entered check below is what keeps that from coming back.
func TestACallTheCallerAbandonedIsNotLoggedAsAFailure(t *testing.T) {
	var logged bytes.Buffer
	handler := &abandoned{}
	client := serve(t, handler, slog.New(slog.NewJSONHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug})))

	if _, err := client.ApplyInterfaceDescription(context.Background(), connect.NewRequest(validApply())); err == nil {
		t.Fatal("the abandoned call succeeded")
	}
	if !handler.entered {
		t.Fatal("the handler never ran, so the interceptor saw no call: this test would pass for any behavior")
	}
	if strings.Contains(logged.String(), "rpc call failed") {
		t.Fatalf("an abandoned call was logged as a failure: %s", logged.String())
	}
}

// A refusal is an answer the caller can act on, so it does not reach the level
// a service failure does. The two arrive on the same line shape, which is what
// makes the level the only thing separating them.
func TestARefusalAndAServiceFailureAreGradedDifferently(t *testing.T) {
	var refusal bytes.Buffer
	client := serve(t, &refusing{}, slog.New(slog.NewJSONHandler(&refusal, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if _, err := client.ApplyInterfaceDescription(context.Background(), connect.NewRequest(validApply())); err == nil {
		t.Fatal("the refused call succeeded")
	}
	if !strings.Contains(refusal.String(), `"level":"DEBUG"`) {
		t.Fatalf("a refusal was not graded as one: %s", refusal.String())
	}

	var failure bytes.Buffer
	failed := serve(t, &failing{}, slog.New(slog.NewJSONHandler(&failure, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if _, err := failed.ApplyInterfaceDescription(context.Background(), connect.NewRequest(validApply())); err == nil {
		t.Fatal("the failing call succeeded")
	}
	if !strings.Contains(failure.String(), `"level":"ERROR"`) {
		t.Fatalf("a service failure was not graded as one: %s", failure.String())
	}
}

type refusing struct {
	devicev1connect.UnimplementedDeviceServiceHandler
}

func (r *refusing) ApplyInterfaceDescription(
	context.Context, *connect.Request[devicev1.ApplyInterfaceDescriptionRequest],
) (*connect.Response[devicev1.ApplyInterfaceDescriptionResponse], error) {
	return nil, deviceapi.ClientErrors.Wrap(
		errs.New().Code(deviceapi.ErrCodeUnknownDevice).Msg("registry lists no such device"))
}

// The histogram is what makes a DEBUG refusal safe: the aggregate question a
// refusal raises — how often, and with what — is a metric's to answer. It
// counts every call, so its count is the denominator any failure rate is taken
// over, and it carries the classified error only on the calls that failed.
func TestEveryCallIsMeasuredAndOnlyFailuresCarryTheirError(t *testing.T) {
	client, reader := serveWithMetrics(t, &refusing{}, slog.New(slog.DiscardHandler))
	if _, err := client.ApplyInterfaceDescription(context.Background(), connect.NewRequest(validApply())); err == nil {
		t.Fatal("the refused call succeeded")
	}

	succeeded, okReader := serveWithMetrics(t, &succeeding{}, slog.New(slog.DiscardHandler))
	if _, err := succeeded.ApplyInterfaceDescription(context.Background(), connect.NewRequest(validApply())); err != nil {
		t.Fatalf("apply: %v", err)
	}

	refused := onlyPoint(t, reader)
	if _, ok := refused.Attributes.Value("error.type"); !ok {
		t.Errorf("a refused call carries no error.type: %v", refused.Attributes.ToSlice())
	}
	if got, ok := refused.Attributes.Value("error.type"); ok && got.AsString() != "deviceapi/unknown-device" {
		t.Errorf("error.type = %v, want the errs code", got)
	}
	if got, ok := refused.Attributes.Value("rpc.method"); !ok || !strings.HasSuffix(got.AsString(), "/ApplyInterfaceDescription") {
		t.Errorf("rpc.method = %v, present = %v", got, ok)
	}

	ok := onlyPoint(t, okReader)
	if _, carries := ok.Attributes.Value("error.type"); carries {
		t.Errorf("a successful call carries an error.type: %v", ok.Attributes.ToSlice())
	}
	if ok.Count != 1 {
		t.Errorf("count = %d, want the successful call counted", ok.Count)
	}
}

func onlyPoint(t *testing.T, reader *sdkmetric.ManualReader) metricdata.HistogramDataPoint[float64] {
	t.Helper()
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("collect: %v", err)
	}
	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != "rpc.server.call.duration" {
				continue
			}
			histogram, isHistogram := m.Data.(metricdata.Histogram[float64])
			if !isHistogram {
				t.Fatalf("%s is %T, want a float64 histogram", m.Name, m.Data)
			}
			if len(histogram.DataPoints) != 1 {
				t.Fatalf("%d series, want one", len(histogram.DataPoints))
			}
			return histogram.DataPoints[0]
		}
	}
	t.Fatal("rpc.server.call.duration was not collected")
	return metricdata.HistogramDataPoint[float64]{}
}
