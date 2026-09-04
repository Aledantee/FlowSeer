package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	collectorlogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	flowerrs "go.aledante.io/FlowSeer/src/common/errs"
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

type rejectedOTLPTransport struct{}

func (rejectedOTLPTransport) uploadLogs(context.Context, *collectorlogspb.ExportLogsServiceRequest) error {
	return errTelemetryExportRejected
}

func (rejectedOTLPTransport) uploadMetrics(context.Context, *collectormetricspb.ExportMetricsServiceRequest) error {
	return errTelemetryExportRejected
}

func (rejectedOTLPTransport) uploadTraces(context.Context, []*tracepb.ResourceSpans) error {
	return errTelemetryExportRejected
}

type rejectedSpanExporter struct{}

func (rejectedSpanExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error {
	return errTelemetryExportRejected
}

func (rejectedSpanExporter) Shutdown(context.Context) error { return nil }

type blockingOTLPTransport struct {
	started     chan struct{}
	release     chan struct{}
	startedOnce sync.Once
	releaseOnce sync.Once
}

type stalledRetryOTLPTransport struct {
	requestTimeout time.Duration
	attempts       int
	waits          int
	remaining      []time.Duration
	attemptErrors  []error
}

func (t *stalledRetryOTLPTransport) uploadLogs(ctx context.Context, _ *collectorlogspb.ExportLogsServiceRequest) error {
	return retryOTLPWithPolicy(ctx, t.requestTimeout, telemetryRetryPolicy{
		limit:       time.Hour,
		initialWait: time.Nanosecond,
		maximumWait: time.Nanosecond,
		wait: func(context.Context, time.Duration) error {
			t.waits++
			if t.waits == 2 {
				return context.DeadlineExceeded
			}
			return nil
		},
	}, func(attemptCtx context.Context) (time.Duration, bool, error) {
		t.attempts++
		deadline, ok := attemptCtx.Deadline()
		if !ok {
			return 0, false, errors.New("export attempt has no deadline")
		}
		t.remaining = append(t.remaining, time.Until(deadline))
		<-attemptCtx.Done()
		t.attemptErrors = append(t.attemptErrors, attemptCtx.Err())
		return 0, true, errors.New("telemetry request stalled")
	})
}

func (*stalledRetryOTLPTransport) uploadMetrics(context.Context, *collectormetricspb.ExportMetricsServiceRequest) error {
	return nil
}

func (*stalledRetryOTLPTransport) uploadTraces(context.Context, []*tracepb.ResourceSpans) error {
	return nil
}

func newBlockingOTLPTransport() *blockingOTLPTransport {
	return &blockingOTLPTransport{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (t *blockingOTLPTransport) uploadLogs(ctx context.Context, _ *collectorlogspb.ExportLogsServiceRequest) error {
	return t.block(ctx)
}

func (t *blockingOTLPTransport) uploadMetrics(ctx context.Context, _ *collectormetricspb.ExportMetricsServiceRequest) error {
	return t.block(ctx)
}

func (t *blockingOTLPTransport) uploadTraces(ctx context.Context, _ []*tracepb.ResourceSpans) error {
	return t.block(ctx)
}

func (t *blockingOTLPTransport) block(ctx context.Context) error {
	t.startedOnce.Do(func() { close(t.started) })
	select {
	case <-t.release:
		return errors.New("collector unavailable")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *blockingOTLPTransport) unblock() {
	t.releaseOnce.Do(func() { close(t.release) })
}

func waitForTelemetryTestSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case <-signal:
	case <-ctx.Done():
		t.Fatalf("timed out waiting for %s", description)
	}
}

func telemetryTestError(message string) *flowerrs.Error {
	return flowerrs.New().Msg(message).(*flowerrs.Error)
}

func TestManagedTelemetryConstructionFailuresUnwindTypedErrorsInReverseOrder(t *testing.T) {
	tests := []struct {
		name       string
		failure    string
		wantEvents []string
		wantClosed []string
	}{
		{
			name:       "transport",
			failure:    "transport",
			wantEvents: []string{"resource", "transport", "close injected"},
			wantClosed: []string{"injected"},
		},
		{
			name:       "logs",
			failure:    "logs",
			wantEvents: []string{"resource", "transport", "logs", "close transport", "close injected"},
			wantClosed: []string{"transport", "injected"},
		},
		{
			name:       "metrics",
			failure:    "metrics",
			wantEvents: []string{"resource", "transport", "logs", "metrics", "close logs", "close transport", "close injected"},
			wantClosed: []string{"logs", "transport", "injected"},
		},
		{
			name:       "traces",
			failure:    "traces",
			wantEvents: []string{"resource", "transport", "logs", "metrics", "traces", "close metrics", "close logs", "close transport", "close injected"},
			wantClosed: []string{"metrics", "logs", "transport", "injected"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cause := telemetryTestError(tt.failure + " construction failed")
			cleanupErrors := map[string]*flowerrs.Error{
				"injected":  telemetryTestError("injected cleanup failed"),
				"transport": telemetryTestError("transport cleanup failed"),
				"logs":      telemetryTestError("logs cleanup failed"),
				"metrics":   telemetryTestError("metrics cleanup failed"),
			}
			var events []string
			cleanup := func(name string) telemetryShutdown {
				return func(context.Context) error {
					events = append(events, "close "+name)
					return cleanupErrors[name]
				}
			}
			factories := telemetryFactorySet{
				newResource: func(Identity) (*resource.Resource, error) {
					events = append(events, "resource")
					return resource.Empty(), nil
				},
				newTransport: func(context.Context, normalizedOTLPConnection) (otlpTransport, telemetryShutdown, error) {
					events = append(events, "transport")
					if tt.failure == "transport" {
						return nil, nil, cause
					}
					return failingOTLPTransport{}, cleanup("transport"), nil
				},
				newLogs: func(*resource.Resource, otlpTransport, *telemetryDiagnostics, normalizedOTLPConnection) (slog.Handler, telemetryShutdown, error) {
					events = append(events, "logs")
					if tt.failure == "logs" {
						return nil, nil, cause
					}
					return slog.DiscardHandler, cleanup("logs"), nil
				},
				newMetrics: func(*resource.Resource, otlpTransport, *telemetryDiagnostics, normalizedOTLPConnection) (metric.MeterProvider, telemetryShutdown, error) {
					events = append(events, "metrics")
					if tt.failure == "metrics" {
						return nil, nil, cause
					}
					return defaultMeterProvider, cleanup("metrics"), nil
				},
				newTraces: func(*resource.Resource, otlpTransport, *telemetryDiagnostics, normalizedOTLPConnection) (trace.TracerProvider, telemetryShutdown, error) {
					events = append(events, "traces")
					if tt.failure == "traces" {
						return nil, nil, cause
					}
					return defaultTracerProvider, nil, nil
				},
			}

			_, err := newRunTelemetry(context.Background(), testIdentity(), normalizedTelemetryConfig{
				connection:       normalizedOTLPConnection{},
				localLogger:      slog.New(slog.DiscardHandler),
				logs:             normalizedLogSignal{backing: signalManaged},
				metrics:          normalizedMetricSignal{backing: signalManaged},
				traces:           normalizedTraceSignal{backing: signalManaged},
				injectedShutdown: cleanup("injected"),
			}, factories)
			if !errors.Is(err, cause) {
				t.Fatalf("newRunTelemetry() error = %v, want construction cause", err)
			}
			for _, name := range tt.wantClosed {
				if !errors.Is(err, cleanupErrors[name]) {
					t.Errorf("newRunTelemetry() error = %v, want %s cleanup cause", err, name)
				}
			}
			var first *flowerrs.Error
			if !errors.As(err, &first) || first != cause {
				t.Errorf("errors.As() = %v, want causal construction error %v", first, cause)
			}
			if !reflect.DeepEqual(events, tt.wantEvents) {
				t.Fatalf("events = %v, want %v", events, tt.wantEvents)
			}
		})
	}
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
	tests := []struct {
		name      string
		telemetry TelemetryConfig
		env       map[string]string
		modules   []Module
	}{
		{name: "malformed endpoint", telemetry: TelemetryConfig{Endpoint: "not-an-endpoint"}},
		{name: "unsupported common environment", env: map[string]string{"OTEL_EXPORTER_OTLP_PROTOCOL": "http/json"}},
		{name: "signal-specific environment", env: map[string]string{"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "https://logs.example"}},
		{
			name: "malformed module override",
			env:  map[string]string{"FLOWSEER_EDGE_WORKER_LOGS_ENABLED": "yes"},
			modules: []Module{{Name: "worker", Leaf: &Leaf{
				Setup: testSetup(),
			}}},
		},
		{name: "unavailable explicit signal", telemetry: TelemetryConfig{Signals: TelemetryPolicy{Traces: TelemetryEnabled}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var factoryCalls atomic.Int32
			var setupCalls atomic.Int32
			storeDir := filepath.Join(t.TempDir(), "bus")
			factories := telemetryFactorySet{
				newResource: func(Identity) (*resource.Resource, error) {
					factoryCalls.Add(1)
					return resource.Empty(), nil
				},
			}
			err := runWithOptionsAndTelemetryFactories(context.Background(), Config{
				Identity:  testIdentity(),
				Bus:       &BusConfig{StoreDir: storeDir},
				Telemetry: tt.telemetry,
				Modules:   tt.modules,
				Setup: func(context.Context) (Attempt, error) {
					setupCalls.Add(1)
					return Attempt{}, nil
				},
			}, supervisorOptions{lookup: mapLookup(tt.env)}, factories)
			if err == nil {
				t.Fatal("runWithOptionsAndTelemetryFactories() error = nil, want config error")
			}
			if got := factoryCalls.Load(); got != 0 {
				t.Fatalf("factory calls = %d, want 0", got)
			}
			if got := setupCalls.Load(); got != 0 {
				t.Fatalf("setup calls = %d, want 0", got)
			}
			if _, statErr := os.Stat(storeDir); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("bus store stat error = %v, want not exist", statErr)
			}
		})
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
		if !deadlines[i].After(deadlines[i-1]) {
			t.Fatalf("shutdown deadline %d = %v, want after %v", i, deadlines[i], deadlines[i-1])
		}
	}
	if secondErr := owner.shutdown(context.Background()); !errors.Is(secondErr, errTrace) || !errors.Is(secondErr, errLog) {
		t.Fatalf("second shutdown error = %v, want cached trace and log causes", secondErr)
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("second shutdown repeated cleanup: %v", events)
	}
}

func TestTelemetryShutdownGivesEveryDependencyTimeWithinTotalBound(t *testing.T) {
	const totalTimeout = 60 * time.Millisecond
	var attempts atomic.Int32
	shutdowns := make([]telemetryShutdown, 3)
	for i := range shutdowns {
		shutdowns[i] = func(ctx context.Context) error {
			attempts.Add(1)
			if !isFinalTelemetryExport(ctx) {
				t.Error("shutdown context is missing final-export marker")
			}
			<-ctx.Done()
			return ctx.Err()
		}
	}

	started := time.Now()
	err := runTelemetryShutdowns(context.Background(), shutdowns, totalTimeout)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runTelemetryShutdowns() error = %v, want deadline exceeded", err)
	}
	if got := attempts.Load(); got != int32(len(shutdowns)) {
		t.Fatalf("shutdown attempts = %d, want %d", got, len(shutdowns))
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("shutdown elapsed = %v, want bounded near %v", elapsed, totalTimeout)
	}
}

func TestServiceFailureRemainsFirstTypedCauseAfterTelemetryShutdown(t *testing.T) {
	serviceErr := telemetryTestError("service setup failed")
	cleanupErrors := map[string]*flowerrs.Error{
		"injected":  telemetryTestError("injected shutdown failed"),
		"transport": telemetryTestError("transport shutdown failed"),
		"logs":      telemetryTestError("logs shutdown failed"),
		"metrics":   telemetryTestError("metrics shutdown failed"),
		"traces":    telemetryTestError("traces shutdown failed"),
	}
	var events []string
	cleanup := func(name string) telemetryShutdown {
		return func(context.Context) error {
			events = append(events, name)
			return cleanupErrors[name]
		}
	}
	factories := telemetryFactorySet{
		newResource: func(Identity) (*resource.Resource, error) { return resource.Empty(), nil },
		newTransport: func(context.Context, normalizedOTLPConnection) (otlpTransport, telemetryShutdown, error) {
			return failingOTLPTransport{}, cleanup("transport"), nil
		},
		newLogs: func(*resource.Resource, otlpTransport, *telemetryDiagnostics, normalizedOTLPConnection) (slog.Handler, telemetryShutdown, error) {
			return slog.DiscardHandler, cleanup("logs"), nil
		},
		newMetrics: func(*resource.Resource, otlpTransport, *telemetryDiagnostics, normalizedOTLPConnection) (metric.MeterProvider, telemetryShutdown, error) {
			return defaultMeterProvider, cleanup("metrics"), nil
		},
		newTraces: func(*resource.Resource, otlpTransport, *telemetryDiagnostics, normalizedOTLPConnection) (trace.TracerProvider, telemetryShutdown, error) {
			return defaultTracerProvider, cleanup("traces"), nil
		},
	}

	err := runWithOptionsAndTelemetryFactories(context.Background(), Config{
		Identity: testIdentity(),
		Telemetry: TelemetryConfig{
			Endpoint: "https://collector.example",
		},
		TelemetryShutdown: cleanup("injected"),
		Modules: []Module{{
			Name:   "worker",
			Policy: Policy{Error: OutcomePolicy{Action: Escalate}},
			Leaf: &Leaf{Setup: func(context.Context) (Attempt, error) {
				return Attempt{}, serviceErr
			}},
		}},
	}, supervisorOptions{lookup: mapLookup(nil)}, factories)
	if !errors.Is(err, serviceErr) {
		t.Fatalf("runWithOptionsAndTelemetryFactories() error = %v, want service cause", err)
	}
	for name, cleanupErr := range cleanupErrors {
		if !errors.Is(err, cleanupErr) {
			t.Errorf("run error = %v, want %s cleanup cause", err, name)
		}
	}
	var first *flowerrs.Error
	if !errors.As(err, &first) || first != serviceErr {
		t.Errorf("errors.As() = %v, want causal service error %v", first, serviceErr)
	}
	wantEvents := []string{"traces", "metrics", "logs", "transport", "injected"}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("shutdown events = %v, want %v", events, wantEvents)
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
	owner.telemetry.logger.Debug("debug detail")
	owner.telemetry.logger.Info("credential secret body", "password", "do-not-leak", "safe", longValue)
	if _, ok := localSink.find("debug detail"); !ok {
		t.Fatal("local logger did not retain debug record")
	}
	if _, ok := exportSink.find("debug detail"); ok {
		t.Fatal("managed export retained production-disabled debug record")
	}
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

func TestTruncateTelemetryStringPreservesUTF8(t *testing.T) {
	value := strings.Repeat("x", telemetryValueLimit-1) + "€"
	got := truncateTelemetryString(value)
	if !utf8.ValidString(got) {
		t.Fatalf("truncateTelemetryString() = %q, want valid UTF-8", got)
	}
	if len(got) > telemetryValueLimit {
		t.Fatalf("truncateTelemetryString() length = %d, want at most %d", len(got), telemetryValueLimit)
	}
	if got != strings.Repeat("x", telemetryValueLimit-1) {
		t.Fatalf("truncateTelemetryString() = %q, want complete-rune prefix", got)
	}
}

func TestTelemetryDiagnosticsAreRateLimitedAndSanitized(t *testing.T) {
	var output bytes.Buffer
	diagnostics := newServiceTelemetryDiagnostics(&output, testIdentity())
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
	for _, field := range []string{
		`"service.name":"edge"`,
		`"flowseer.telemetry.signal":"logs"`,
		`"flowseer.telemetry.category":"unavailable"`,
		`"flowseer.telemetry.count":1`,
		`"flowseer.telemetry.suppressed":0`,
	} {
		if !strings.Contains(output.String(), field) {
			t.Errorf("diagnostic = %q, want field %s", output.String(), field)
		}
	}
	if got := strings.Count(output.String(), `"time":`); got != 1 {
		t.Errorf("diagnostic time fields = %d, want only the handler timestamp", got)
	}
}

func TestManagedTelemetryBlockedExportersDoNotBlockProducers(t *testing.T) {
	t.Run("logs bounded queue", func(t *testing.T) {
		transport := newBlockingOTLPTransport()
		defer transport.unblock()
		var diagnostics bytes.Buffer
		handler, shutdown, err := newManagedLogProvider(
			resource.Empty(),
			transport,
			newTelemetryDiagnostics(&diagnostics),
			normalizedOTLPConnection{},
		)
		if err != nil {
			t.Fatalf("newManagedLogProvider() error: %v", err)
		}
		logger := slog.New(handler)
		for range telemetryBatchSize {
			logger.Info("queue probe")
		}
		waitForTelemetryTestSignal(t, transport.started, "blocked log export")

		producerDone := make(chan struct{})
		go func() {
			for range telemetryQueueSize + telemetryBatchSize + 1 {
				logger.Info("queue overflow probe")
			}
			close(producerDone)
		}()
		waitForTelemetryTestSignal(t, producerDone, "log producer to finish while export is blocked")

		transport.unblock()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdown(shutdownCtx); err != nil {
			t.Fatalf("log shutdown: %v", err)
		}
		if got := strings.Count(diagnostics.String(), "telemetry export failed"); got != 1 {
			t.Fatalf("log export diagnostics = %d, want one rate-limited warning", got)
		}
	})

	t.Run("traces bounded queue", func(t *testing.T) {
		transport := newBlockingOTLPTransport()
		defer transport.unblock()
		var diagnostics bytes.Buffer
		provider, shutdown, err := newManagedTraceProvider(
			resource.Empty(),
			transport,
			newTelemetryDiagnostics(&diagnostics),
			normalizedOTLPConnection{},
		)
		if err != nil {
			t.Fatalf("newManagedTraceProvider() error: %v", err)
		}
		tracer := provider.Tracer("telemetry-queue-test")
		for range telemetryBatchSize {
			_, span := tracer.Start(context.Background(), "queue probe")
			span.End()
		}
		waitForTelemetryTestSignal(t, transport.started, "blocked trace export")

		producerDone := make(chan struct{})
		go func() {
			for range telemetryQueueSize + telemetryBatchSize + 1 {
				_, span := tracer.Start(context.Background(), "queue overflow probe")
				span.End()
			}
			close(producerDone)
		}()
		waitForTelemetryTestSignal(t, producerDone, "trace producer to finish while export is blocked")

		transport.unblock()
		flusher, ok := provider.(interface{ ForceFlush(context.Context) error })
		if !ok {
			t.Fatal("managed trace provider has no ForceFlush method")
		}
		flushCtx, flushCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer flushCancel()
		if err := flusher.ForceFlush(flushCtx); err != nil {
			t.Fatalf("trace force flush: %v", err)
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdown(shutdownCtx); err != nil {
			t.Fatalf("trace shutdown: %v", err)
		}
		if got := strings.Count(diagnostics.String(), "telemetry export failed"); got != 1 {
			t.Fatalf("trace export diagnostics = %d, want one rate-limited warning", got)
		}
	})

	t.Run("metrics synchronous recording", func(t *testing.T) {
		transport := newBlockingOTLPTransport()
		defer transport.unblock()
		var diagnostics bytes.Buffer
		provider, shutdown, err := newManagedMetricProvider(
			resource.Empty(),
			transport,
			newTelemetryDiagnostics(&diagnostics),
			normalizedOTLPConnection{},
		)
		if err != nil {
			t.Fatalf("newManagedMetricProvider() error: %v", err)
		}
		counter, err := provider.Meter("telemetry-queue-test").Int64Counter("flowseer.test.operations")
		if err != nil {
			t.Fatalf("create counter: %v", err)
		}
		counter.Add(context.Background(), 1)
		flusher, ok := provider.(interface{ ForceFlush(context.Context) error })
		if !ok {
			t.Fatal("managed metric provider has no ForceFlush method")
		}
		flushDone := make(chan error, 1)
		go func() { flushDone <- flusher.ForceFlush(context.Background()) }()
		waitForTelemetryTestSignal(t, transport.started, "blocked metric export")

		producerDone := make(chan struct{})
		go func() {
			for range telemetryQueueSize + telemetryBatchSize + 1 {
				counter.Add(context.Background(), 1)
			}
			close(producerDone)
		}()
		waitForTelemetryTestSignal(t, producerDone, "metric producer to finish while export is blocked")

		transport.unblock()
		flushCtx, flushCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer flushCancel()
		select {
		case err := <-flushDone:
			if err != nil {
				t.Fatalf("metric force flush: %v", err)
			}
		case <-flushCtx.Done():
			t.Fatal("timed out waiting for metric force flush")
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdown(shutdownCtx); err != nil {
			t.Fatalf("metric shutdown: %v", err)
		}
		if got := strings.Count(diagnostics.String(), "telemetry export failed"); got != 1 {
			t.Fatalf("metric export diagnostics = %d, want one rate-limited warning", got)
		}
	})
}

func TestManagedTraceShutdownCancelsBlockedExport(t *testing.T) {
	transport := newBlockingOTLPTransport()
	defer transport.unblock()
	provider, shutdown, err := newManagedTraceProvider(
		resource.Empty(),
		transport,
		newTelemetryDiagnostics(io.Discard),
		normalizedOTLPConnection{},
	)
	if err != nil {
		t.Fatalf("newManagedTraceProvider() error: %v", err)
	}
	tracer := provider.Tracer("telemetry-shutdown-test")
	for range telemetryBatchSize {
		_, span := tracer.Start(context.Background(), "blocked shutdown probe")
		span.End()
	}
	waitForTelemetryTestSignal(t, transport.started, "blocked trace export")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := shutdown(shutdownCtx); err == nil || !strings.Contains(err.Error(), "traces telemetry export failed") {
		t.Fatalf("trace shutdown error = %v, want final export failure", err)
	}
	if err := shutdownCtx.Err(); err != nil {
		t.Fatalf("trace shutdown consumed its deadline: %v", err)
	}
}

func TestManagedTelemetryStalledRequestsUsePerAttemptTimeout(t *testing.T) {
	const requestTimeout = 5 * time.Millisecond
	transport := &stalledRetryOTLPTransport{requestTimeout: requestTimeout}
	var diagnostics bytes.Buffer
	exporter := &managedLogExporter{
		transport:   transport,
		diagnostics: newTelemetryDiagnostics(&diagnostics),
	}

	if err := exporter.Export(context.Background(), nil); err != nil {
		t.Fatalf("routine Export() error = %v, want nonfatal outage", err)
	}
	if transport.attempts != 2 || transport.waits != 2 {
		t.Fatalf("stalled attempts/waits = %d/%d, want 2/2", transport.attempts, transport.waits)
	}
	for i, remaining := range transport.remaining {
		if remaining > requestTimeout {
			t.Errorf("attempt %d deadline remaining = %v, want no more than %v", i, remaining, requestTimeout)
		}
	}
	for i, err := range transport.attemptErrors {
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("attempt %d error = %v, want request deadline", i, err)
		}
	}
	if got := strings.Count(diagnostics.String(), "telemetry export failed"); got != 1 {
		t.Fatalf("stalled request diagnostics = %d, want one nonfatal warning", got)
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
	if !strings.Contains(output.String(), `"flowseer.telemetry.signal":"logs"`) {
		t.Fatalf("routine failure diagnostic = %q, want fixed log signal", output.String())
	}
}

func TestRoutineExporterRejectionUsesRejectedCategory(t *testing.T) {
	tests := []struct {
		name   string
		export func(*telemetryDiagnostics) error
	}{
		{
			name: "logs",
			export: func(diagnostics *telemetryDiagnostics) error {
				return (&managedLogExporter{transport: rejectedOTLPTransport{}, diagnostics: diagnostics}).Export(context.Background(), nil)
			},
		},
		{
			name: "metrics",
			export: func(diagnostics *telemetryDiagnostics) error {
				return (&managedMetricExporter{transport: rejectedOTLPTransport{}, diagnostics: diagnostics}).Export(context.Background(), &metricdata.ResourceMetrics{})
			},
		},
		{
			name: "traces",
			export: func(diagnostics *telemetryDiagnostics) error {
				exporter := &managedTraceExporter{
					SpanExporter: rejectedSpanExporter{},
					diagnostics:  diagnostics,
					active:       make(map[uint64]context.CancelFunc),
				}
				return exporter.ExportSpans(context.Background(), nil)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := tt.export(newTelemetryDiagnostics(&output)); err != nil {
				t.Fatalf("routine export error = %v, want nonfatal rejection", err)
			}
			if !strings.Contains(output.String(), `"flowseer.telemetry.category":"rejected"`) {
				t.Fatalf("routine rejection diagnostic = %q, want rejected category", output.String())
			}
		})
	}
}
