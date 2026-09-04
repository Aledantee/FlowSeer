package service

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/trace"
	collectorlogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

type failingOTLPTransport struct{}

func (failingOTLPTransport) uploadLogs(context.Context, *collectorlogspb.ExportLogsServiceRequest) error {
	return errors.New("collector unavailable")
}

func (failingOTLPTransport) uploadMetrics(context.Context, *collectormetricspb.ExportMetricsServiceRequest) error {
	return errors.New("collector unavailable")
}

func (failingOTLPTransport) uploadTraces(context.Context, []*tracepb.ResourceSpans) error {
	return errors.New("collector unavailable")
}

func TestManagedTelemetryConstructionFailureUnwindsInReverseOrder(t *testing.T) {
	errTrace := errors.New("trace construction failed")
	var events []string
	sharedResource := resource.Empty()
	factories := telemetryFactorySet{
		newResource: func(Identity) (*resource.Resource, error) {
			events = append(events, "resource")
			return sharedResource, nil
		},
		newTransport: func(context.Context, normalizedOTLPConnection) (otlpTransport, telemetryShutdown, error) {
			events = append(events, "transport")
			return nil, func(context.Context) error {
				events = append(events, "close transport")
				return nil
			}, nil
		},
		newLogs: func(*resource.Resource, otlpTransport, *telemetryDiagnostics, normalizedOTLPConnection) (slog.Handler, telemetryShutdown, error) {
			events = append(events, "logs")
			return slog.DiscardHandler, func(context.Context) error {
				events = append(events, "close logs")
				return nil
			}, nil
		},
		newMetrics: func(*resource.Resource, otlpTransport, *telemetryDiagnostics, normalizedOTLPConnection) (metric.MeterProvider, telemetryShutdown, error) {
			events = append(events, "metrics")
			return defaultMeterProvider, func(context.Context) error {
				events = append(events, "close metrics")
				return nil
			}, nil
		},
		newTraces: func(*resource.Resource, otlpTransport, *telemetryDiagnostics, normalizedOTLPConnection) (trace.TracerProvider, telemetryShutdown, error) {
			events = append(events, "traces")
			return nil, nil, errTrace
		},
	}

	_, err := newRunTelemetry(context.Background(), testIdentity(), normalizedTelemetryConfig{
		connection:  normalizedOTLPConnection{},
		localLogger: slog.New(slog.DiscardHandler),
		logs:        normalizedLogSignal{backing: signalManaged},
		metrics:     normalizedMetricSignal{backing: signalManaged},
		traces:      normalizedTraceSignal{backing: signalManaged},
	}, factories)
	if !errors.Is(err, errTrace) {
		t.Fatalf("newRunTelemetry() error = %v, want trace construction cause", err)
	}
	want := []string{"resource", "transport", "logs", "metrics", "traces", "close metrics", "close logs", "close transport"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestManagedTelemetryFactoriesShareOneResource(t *testing.T) {
	sharedResource := resource.Empty()
	var resources []*resource.Resource
	factories := telemetryFactorySet{
		newResource: func(Identity) (*resource.Resource, error) { return sharedResource, nil },
		newTransport: func(context.Context, normalizedOTLPConnection) (otlpTransport, telemetryShutdown, error) {
			return nil, func(context.Context) error { return nil }, nil
		},
		newLogs: func(res *resource.Resource, _ otlpTransport, _ *telemetryDiagnostics, _ normalizedOTLPConnection) (slog.Handler, telemetryShutdown, error) {
			resources = append(resources, res)
			return slog.DiscardHandler, func(context.Context) error { return nil }, nil
		},
		newMetrics: func(res *resource.Resource, _ otlpTransport, _ *telemetryDiagnostics, _ normalizedOTLPConnection) (metric.MeterProvider, telemetryShutdown, error) {
			resources = append(resources, res)
			return defaultMeterProvider, func(context.Context) error { return nil }, nil
		},
		newTraces: func(res *resource.Resource, _ otlpTransport, _ *telemetryDiagnostics, _ normalizedOTLPConnection) (trace.TracerProvider, telemetryShutdown, error) {
			resources = append(resources, res)
			return defaultTracerProvider, func(context.Context) error { return nil }, nil
		},
	}

	wantTracer := otel.GetTracerProvider()
	wantMeter := otel.GetMeterProvider()
	wantPropagator := otel.GetTextMapPropagator()
	wantLogger := slog.Default()
	owner, err := newRunTelemetry(context.Background(), testIdentity(), normalizedTelemetryConfig{
		connection:  normalizedOTLPConnection{},
		localLogger: slog.New(slog.DiscardHandler),
		logs:        normalizedLogSignal{backing: signalManaged},
		metrics:     normalizedMetricSignal{backing: signalManaged},
		traces:      normalizedTraceSignal{backing: signalManaged},
		propagator:  propagation.TraceContext{},
	}, factories)
	if err != nil {
		t.Fatalf("newRunTelemetry() error: %v", err)
	}
	defer func() { _ = owner.shutdown(context.Background()) }()
	for i, got := range resources {
		if got != sharedResource {
			t.Errorf("factory resource %d = %p, want shared %p", i, got, sharedResource)
		}
	}
	if otel.GetTracerProvider() != wantTracer || otel.GetMeterProvider() != wantMeter || otel.GetTextMapPropagator() != wantPropagator || slog.Default() != wantLogger {
		t.Fatal("managed telemetry changed process globals")
	}
}

func TestInvalidTelemetryConfigCallsNoFactories(t *testing.T) {
	var calls atomic.Int32
	factories := telemetryFactorySet{
		newResource: func(Identity) (*resource.Resource, error) {
			calls.Add(1)
			return resource.Empty(), nil
		},
	}
	err := runWithOptionsAndTelemetryFactories(context.Background(), Config{
		Identity:  testIdentity(),
		Setup:     testSetup(),
		Telemetry: TelemetryConfig{Endpoint: "not-an-endpoint"},
	}, supervisorOptions{}, factories)
	if err == nil {
		t.Fatal("runWithOptionsAndTelemetryFactories() error = nil, want config error")
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("factory calls = %d, want 0", got)
	}
}

func TestManagedTelemetryShutdownAttemptsEverySignalInOrder(t *testing.T) {
	errTrace := errors.New("trace shutdown failed")
	errLog := errors.New("log shutdown failed")
	var events []string
	var deadlines []time.Time
	recordDeadline := func(ctx context.Context) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("shutdown context has no deadline")
		}
		deadlines = append(deadlines, deadline)
	}
	factories := telemetryFactorySet{
		newResource: func(Identity) (*resource.Resource, error) { return resource.Empty(), nil },
		newTransport: func(context.Context, normalizedOTLPConnection) (otlpTransport, telemetryShutdown, error) {
			return nil, func(ctx context.Context) error {
				recordDeadline(ctx)
				events = append(events, "transport")
				return nil
			}, nil
		},
		newLogs: func(*resource.Resource, otlpTransport, *telemetryDiagnostics, normalizedOTLPConnection) (slog.Handler, telemetryShutdown, error) {
			return slog.DiscardHandler, func(ctx context.Context) error {
				recordDeadline(ctx)
				events = append(events, "logs")
				return errLog
			}, nil
		},
		newMetrics: func(*resource.Resource, otlpTransport, *telemetryDiagnostics, normalizedOTLPConnection) (metric.MeterProvider, telemetryShutdown, error) {
			return defaultMeterProvider, func(ctx context.Context) error {
				recordDeadline(ctx)
				events = append(events, "metrics")
				return nil
			}, nil
		},
		newTraces: func(*resource.Resource, otlpTransport, *telemetryDiagnostics, normalizedOTLPConnection) (trace.TracerProvider, telemetryShutdown, error) {
			return defaultTracerProvider, func(ctx context.Context) error {
				recordDeadline(ctx)
				events = append(events, "traces")
				return errTrace
			}, nil
		},
	}

	owner, err := newRunTelemetry(context.Background(), testIdentity(), normalizedTelemetryConfig{
		connection:  normalizedOTLPConnection{},
		localLogger: slog.New(slog.DiscardHandler),
		logs:        normalizedLogSignal{backing: signalManaged},
		metrics:     normalizedMetricSignal{backing: signalManaged},
		traces:      normalizedTraceSignal{backing: signalManaged},
	}, factories)
	if err != nil {
		t.Fatalf("newRunTelemetry() error: %v", err)
	}
	err = owner.shutdown(context.Background())
	if !errors.Is(err, errTrace) || !errors.Is(err, errLog) {
		t.Fatalf("shutdown error = %v, want trace and log causes", err)
	}
	want := []string{"traces", "metrics", "logs", "transport"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i := 1; i < len(deadlines); i++ {
		if deadlines[i] != deadlines[0] {
			t.Fatalf("shutdown deadline %d = %v, want shared %v", i, deadlines[i], deadlines[0])
		}
	}
	if secondErr := owner.shutdown(context.Background()); !errors.Is(secondErr, errTrace) || !errors.Is(secondErr, errLog) {
		t.Fatalf("second shutdown error = %v, want cached trace and log causes", secondErr)
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("second shutdown repeated cleanup: %v", events)
	}
}

func TestInjectedTelemetryShutdownRunsOnceWithoutClosingBorrowedProviders(t *testing.T) {
	var shutdowns atomic.Int32
	tracerProvider := defaultTracerProvider
	meterProvider := defaultMeterProvider
	owner, err := newRunTelemetry(context.Background(), testIdentity(), normalizedTelemetryConfig{
		localLogger: slog.New(slog.DiscardHandler),
		logs:        normalizedLogSignal{backing: signalInjected, handler: slog.DiscardHandler},
		metrics:     normalizedMetricSignal{backing: signalInjected, provider: meterProvider},
		traces:      normalizedTraceSignal{backing: signalInjected, provider: tracerProvider},
		propagator:  propagation.TraceContext{},
		injectedShutdown: func(context.Context) error {
			shutdowns.Add(1)
			return nil
		},
	}, telemetryFactorySet{})
	if err != nil {
		t.Fatalf("newRunTelemetry() error: %v", err)
	}
	if owner.tracerProvider != tracerProvider || owner.meterProvider != meterProvider {
		t.Fatal("newRunTelemetry() did not preserve borrowed providers")
	}
	if err := owner.shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown error: %v", err)
	}
	if err := owner.shutdown(context.Background()); err != nil {
		t.Fatalf("second shutdown error: %v", err)
	}
	if got := shutdowns.Load(); got != 1 {
		t.Fatalf("aggregate shutdown calls = %d, want 1", got)
	}
}

func TestDirectHTTPTransportDerivesSignalPaths(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Content-Type"); got != "application/x-protobuf" {
			t.Errorf("Content-Type = %q", got)
		}
		paths = append(paths, request.URL.Path)
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	endpoint, err := url.Parse(server.URL + "/collector/base/")
	if err != nil {
		t.Fatalf("parse endpoint: %v", err)
	}
	transport := newHTTPOTLPTransport(normalizedOTLPConnection{endpoint: endpoint, timeout: time.Second})
	if err := transport.uploadLogs(context.Background(), &collectorlogspb.ExportLogsServiceRequest{}); err != nil {
		t.Fatalf("upload logs: %v", err)
	}
	if err := transport.uploadMetrics(context.Background(), &collectormetricspb.ExportMetricsServiceRequest{}); err != nil {
		t.Fatalf("upload metrics: %v", err)
	}
	if err := transport.uploadTraces(context.Background(), nil); err != nil {
		t.Fatalf("upload traces: %v", err)
	}
	want := []string{"/collector/base/v1/logs", "/collector/base/v1/metrics", "/collector/base/v1/traces"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
}

func TestDirectGRPCTransportUsesEndpointAuthority(t *testing.T) {
	endpoint, err := url.Parse("https://collector.example:4317/ignored/base")
	if err != nil {
		t.Fatalf("parse endpoint: %v", err)
	}
	transport, shutdown, err := newGRPCOTLPTransport(normalizedOTLPConnection{endpoint: endpoint, insecure: true, timeout: time.Second})
	if err != nil {
		t.Fatalf("newGRPCOTLPTransport() error: %v", err)
	}
	grpcTransport := transport.(*grpcOTLPTransport)
	if got := grpcTransport.connection.Target(); got != endpoint.Host {
		t.Errorf("gRPC target = %q, want %q", got, endpoint.Host)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown error: %v", err)
	}
}

func TestGRPCResourceExhaustedRetriesOnlyWithRetryInfo(t *testing.T) {
	withoutHint := status.Error(codes.ResourceExhausted, "busy")
	if retryGRPCError(withoutHint) {
		t.Fatal("ResourceExhausted without RetryInfo is retryable")
	}

	withHint, err := status.New(codes.ResourceExhausted, "busy").WithDetails(&errdetails.RetryInfo{
		RetryDelay: durationpb.New(time.Second),
	})
	if err != nil {
		t.Fatalf("attach RetryInfo: %v", err)
	}
	if !retryGRPCError(withHint.Err()) {
		t.Fatal("ResourceExhausted with RetryInfo is not retryable")
	}
}

func TestManagedLogSanitizerPreservesLocalDetailsOnly(t *testing.T) {
	localLogger, localSink := newRecordingLogger()
	exportLogger, exportSink := newRecordingLogger()
	factories := telemetryFactorySet{
		newResource: func(Identity) (*resource.Resource, error) { return resource.Empty(), nil },
		newTransport: func(context.Context, normalizedOTLPConnection) (otlpTransport, telemetryShutdown, error) {
			return nil, func(context.Context) error { return nil }, nil
		},
		newLogs: func(*resource.Resource, otlpTransport, *telemetryDiagnostics, normalizedOTLPConnection) (slog.Handler, telemetryShutdown, error) {
			return exportLogger.Handler(), func(context.Context) error { return nil }, nil
		},
	}
	owner, err := newRunTelemetry(context.Background(), testIdentity(), normalizedTelemetryConfig{
		localLogger: localLogger,
		logs:        normalizedLogSignal{backing: signalManaged},
		propagator:  propagation.TraceContext{},
	}, factories)
	if err != nil {
		t.Fatalf("newRunTelemetry() error: %v", err)
	}
	defer func() { _ = owner.shutdown(context.Background()) }()
	longValue := strings.Repeat("x", telemetryValueLimit+20)
	owner.telemetry.logger.Info("credential secret body", "password", "do-not-leak", "safe", longValue)
	if attrs, ok := localSink.find("credential secret body"); !ok || attrs["password"] != "do-not-leak" || attrs["safe"] != longValue {
		t.Fatalf("local record = %v, %v; want trusted details", attrs, ok)
	}
	attrs, ok := exportSink.find("[redacted]")
	if !ok {
		t.Fatal("managed export did not receive sanitized record")
	}
	if _, exists := attrs["password"]; exists {
		t.Fatal("managed export retained prohibited password attribute")
	}
	if len(attrs["safe"]) != telemetryValueLimit {
		t.Fatalf("managed safe value length = %d, want %d", len(attrs["safe"]), telemetryValueLimit)
	}
}

func TestTelemetryDiagnosticsAreRateLimitedAndSanitized(t *testing.T) {
	var output bytes.Buffer
	diagnostics := newTelemetryDiagnostics(&output)
	now := time.Unix(1_000, 0)
	diagnostics.now = func() time.Time { return now }
	diagnostics.report("logs", "unavailable")
	diagnostics.report("logs", "do-not-leak-secret")
	if got := strings.Count(output.String(), "telemetry export failed"); got != 1 {
		t.Fatalf("diagnostic records = %d, want 1", got)
	}
	if strings.Contains(output.String(), "do-not-leak") {
		t.Fatal("diagnostic echoed untrusted detail")
	}
}

func TestRoutineExporterFailureIsNonfatalButFinalFailureIsReturned(t *testing.T) {
	var output bytes.Buffer
	exporter := &managedLogExporter{
		transport:   failingOTLPTransport{},
		diagnostics: newTelemetryDiagnostics(&output),
	}
	if err := exporter.Export(context.Background(), nil); err != nil {
		t.Fatalf("routine Export() error = %v, want nil", err)
	}
	finalCtx := context.WithValue(context.Background(), telemetryFinalExportKey{}, true)
	if err := exporter.Export(finalCtx, nil); err == nil {
		t.Fatal("final Export() error = nil, want final drain error")
	}
	if !strings.Contains(output.String(), `"signal":"logs"`) {
		t.Fatalf("routine failure diagnostic = %q, want fixed log signal", output.String())
	}
}
