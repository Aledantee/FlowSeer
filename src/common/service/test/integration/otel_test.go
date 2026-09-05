//go:build service_otel_integration

package integration_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"go.aledante.io/FlowSeer/src/common/service"
)

func TestManagedTelemetryHTTP(t *testing.T) {
	clearOTELTestEnvironment(t)
	collector := startOTelCollector(t)
	identity := uniqueTelemetryIdentity(t, "http_smoke")

	err := service.Run(context.Background(), service.Config{
		Identity: identity,
		Logger:   slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Telemetry: service.TelemetryConfig{
			Endpoint:         collector.httpEndpoint,
			TraceSampleRatio: fullTraceSampleRatio(),
			Signals: service.TelemetryPolicy{
				Logs:    service.TelemetryEnabled,
				Metrics: service.TelemetryEnabled,
				Traces:  service.TelemetryEnabled,
			},
		},
		Setup: syntheticTelemetryAttempt,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	got := collector.waitForServiceSignals(t, identity.Name, allTelemetrySignals)
	if got != allTelemetrySignals {
		t.Fatalf("exported signals = %s, want %s", got, allTelemetrySignals)
	}
	logs, err := collector.logRecords(identity.Name)
	if err != nil {
		t.Fatalf("read Collector logs: %v", err)
	}
	var logTraceID []byte
	for _, record := range logs {
		if record.GetBody().GetStringValue() == "synthetic module record" {
			logTraceID = record.GetTraceId()
		}
	}
	if len(logTraceID) == 0 {
		t.Fatal("synthetic log has no native trace correlation")
	}
	metrics, err := collector.metricRecords(identity.Name)
	if err != nil {
		t.Fatalf("read Collector metrics: %v", err)
	}
	if !slices.ContainsFunc(metrics, func(record *metricspb.Metric) bool {
		return record.GetName() == "flowseer.test.operations"
	}) {
		t.Fatal("Collector has no synthetic metric")
	}
	spans, err := collector.spanRecords(identity.Name)
	if err != nil {
		t.Fatalf("read Collector spans: %v", err)
	}
	if !slices.ContainsFunc(spans, func(span *tracepb.Span) bool {
		return span.GetName() == "flowseer.service.module.attempt" && bytes.Equal(span.GetTraceId(), logTraceID)
	}) {
		t.Fatal("synthetic log is not correlated with its module-attempt span")
	}
	for _, signal := range []string{"logs", "metrics", "traces"} {
		content, err := readBoundedFile(collector.sinkPath(signal), maximumArtifactBytes)
		if err != nil {
			t.Fatalf("read %s sink: %v", signal, err)
		}
		if bytes.Contains(content, []byte(telemetrySecretSentinel)) {
			t.Fatalf("%s sink contains the synthetic secret sentinel", signal)
		}
	}
}

func syntheticTelemetryAttempt(ctx context.Context) (service.Attempt, error) {
	if err := emitSyntheticTelemetry(ctx); err != nil {
		return service.Attempt{}, err
	}
	return service.Attempt{Runner: func(context.Context) error { return nil }}, nil
}

func emitSyntheticTelemetry(ctx context.Context) error {
	service.Logger(ctx).InfoContext(ctx, "synthetic module record",
		slog.String("flowseer.test.case", service.Name(ctx)),
		slog.String("authorization", telemetrySecretSentinel),
	)
	counter, err := service.Meter(ctx).Int64Counter(
		"flowseer.test.operations",
		metric.WithUnit("{operation}"),
		metric.WithDescription("Synthetic operations completed by the Collector integration test"),
	)
	if err != nil {
		return err
	}
	counter.Add(ctx, 1, metric.WithAttributes(attribute.String("authorization", telemetrySecretSentinel)))
	_, span := service.Tracer(ctx).Start(ctx, "flowseer.test.operation",
		trace.WithAttributes(attribute.String("authorization", telemetrySecretSentinel)),
	)
	span.End()
	return nil
}

func TestManagedTelemetrySignalMatrix(t *testing.T) {
	clearOTELTestEnvironment(t)
	collector := startOTelCollector(t)
	for mask := telemetrySignalSet(0); mask <= allTelemetrySignals; mask++ {
		mask := mask
		t.Run(mask.String(), func(t *testing.T) {
			identity := uniqueTelemetryIdentity(t, "mask")
			config := managedTelemetryConfig(identity, collector.httpEndpoint, mask)
			config.Setup = nil
			config.Modules = []service.Module{
				{
					Name: "branch",
					Branch: &service.Branch{Children: []service.Module{
						{Name: "leaf", Leaf: &service.Leaf{Setup: syntheticTelemetryAttempt}},
					}},
				},
			}
			if err := service.Run(context.Background(), config); err != nil {
				t.Fatalf("Run() error: %v", err)
			}
			collector.waitForServiceSignals(t, identity.Name, mask)
			modulePath := identity.Name + "/branch/leaf"
			got, err := collector.moduleSignals(identity.Name, modulePath)
			if err != nil {
				t.Fatalf("read module signals: %v", err)
			}
			if got != mask {
				t.Fatalf("module signals for %q = %s, want %s", modulePath, got, mask)
			}
		})
	}
}

func TestManagedTelemetryTransportsAndPreflight(t *testing.T) {
	clearOTELTestEnvironment(t)
	collector := startOTelCollector(t)
	tests := []struct {
		name     string
		endpoint string
		protocol string
	}{
		{name: "http_base_path", endpoint: collector.baseEndpoint},
		{name: "grpc", endpoint: collector.grpcEndpoint, protocol: "grpc"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			identity := uniqueTelemetryIdentity(t, test.name)
			config := managedTelemetryConfig(identity, test.endpoint, allTelemetrySignals)
			config.Telemetry.Protocol = test.protocol
			if err := service.Run(context.Background(), config); err != nil {
				t.Fatalf("Run() error: %v", err)
			}
			collector.waitForServiceSignals(t, identity.Name, allTelemetrySignals)
		})
	}

	t.Run("endpoint_absent", func(t *testing.T) {
		identity := uniqueTelemetryIdentity(t, "endpoint_absent")
		var local bytes.Buffer
		config := managedTelemetryConfig(identity, "", 0)
		config.Logger = slog.New(slog.NewJSONHandler(&local, nil))
		if err := service.Run(context.Background(), config); err != nil {
			t.Fatalf("Run() error: %v", err)
		}
		if !strings.Contains(local.String(), "synthetic module record") {
			t.Fatalf("local log does not contain synthetic module record: %s", local.String())
		}
		collector.waitForServiceSignals(t, identity.Name, 0)
	})

	t.Run("signal_specific_environment", func(t *testing.T) {
		identity := uniqueTelemetryIdentity(t, "invalid_environment")
		t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", collector.httpEndpoint)
		var setups int
		config := managedTelemetryConfig(identity, collector.httpEndpoint, allTelemetrySignals)
		config.Setup = func(context.Context) (service.Attempt, error) {
			setups++
			return service.Attempt{Runner: func(context.Context) error { return nil }}, nil
		}
		err := service.Run(context.Background(), config)
		if err == nil {
			t.Fatal("Run() succeeded with a signal-specific OTLP endpoint")
		}
		if setups != 0 {
			t.Fatalf("setup calls = %d, want 0", setups)
		}
		collector.waitForServiceSignals(t, identity.Name, 0)
		t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	})
}

func TestManagedTelemetryNestedLogOverride(t *testing.T) {
	clearOTELTestEnvironment(t)
	collector := startOTelCollector(t)
	identity := uniqueTelemetryIdentity(t, "nested_logs")
	var local bytes.Buffer
	config := managedTelemetryConfig(identity, collector.httpEndpoint, telemetryLogs)
	config.Logger = slog.New(slog.NewJSONHandler(&local, nil))
	config.Setup = nil
	config.Modules = []service.Module{
		{
			Name:      "disabled_branch",
			Telemetry: service.TelemetryPolicy{Logs: service.TelemetryDisabled},
			Branch: &service.Branch{Children: []service.Module{
				{Name: "local_only", Leaf: &service.Leaf{Setup: syntheticTelemetryAttempt}},
				{
					Name:      "reenabled",
					Telemetry: service.TelemetryPolicy{Logs: service.TelemetryEnabled},
					Leaf:      &service.Leaf{Setup: syntheticTelemetryAttempt},
				},
			}},
		},
	}
	if err := service.Run(context.Background(), config); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	collector.waitForServiceSignals(t, identity.Name, telemetryLogs)
	localOnlyPath := identity.Name + "/disabled_branch/local_only"
	reenabledPath := identity.Name + "/disabled_branch/reenabled"
	if !strings.Contains(local.String(), localOnlyPath) || !strings.Contains(local.String(), reenabledPath) {
		t.Fatalf("local log did not retain both module paths: %s", local.String())
	}
	logs, err := collector.logRecords(identity.Name)
	if err != nil {
		t.Fatalf("read Collector logs: %v", err)
	}
	if !hasModuleLog(logs, reenabledPath) {
		t.Fatal("Collector has no re-enabled descendant log")
	}
	if hasModuleLog(logs, localOnlyPath) {
		t.Fatal("Collector has a local-only module log")
	}
}

func TestManagedTelemetryMixedOwnership(t *testing.T) {
	clearOTELTestEnvironment(t)
	collector := startOTelCollector(t)
	identity := uniqueTelemetryIdentity(t, "mixed_ownership")
	var logExports atomic.Int64
	var shutdowns atomic.Int64
	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	config := managedTelemetryConfig(identity, collector.httpEndpoint, allTelemetrySignals)
	config.LogHandler = countingLogHandler{synthetic: &logExports}
	config.TracerProvider = tracerProvider
	config.TelemetryShutdown = func(ctx context.Context) error {
		shutdowns.Add(1)
		return tracerProvider.Shutdown(ctx)
	}
	if err := service.Run(context.Background(), config); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	collector.waitForServiceSignals(t, identity.Name, telemetryMetrics)
	metrics, err := collector.metricRecords(identity.Name)
	if err != nil {
		t.Fatalf("read managed metrics: %v", err)
	}
	var syntheticPoints int
	var syntheticTotal int64
	for _, record := range metrics {
		if record.GetName() != "flowseer.test.operations" {
			continue
		}
		for _, point := range record.GetSum().GetDataPoints() {
			syntheticPoints++
			syntheticTotal += point.GetAsInt()
		}
	}
	if syntheticPoints != 1 || syntheticTotal != 1 {
		t.Fatalf("managed synthetic metric = %d points totaling %d, want 1 point totaling 1", syntheticPoints, syntheticTotal)
	}
	if got := logExports.Load(); got != 1 {
		t.Fatalf("injected synthetic log exports = %d, want 1", got)
	}
	var syntheticSpans int
	for _, span := range spanRecorder.Ended() {
		if span.Name() == "flowseer.test.operation" {
			syntheticSpans++
		}
	}
	if syntheticSpans != 1 {
		t.Fatalf("injected synthetic spans = %d, want 1", syntheticSpans)
	}
	if got := shutdowns.Load(); got != 1 {
		t.Fatalf("aggregate injected shutdown calls = %d, want 1", got)
	}
}

func TestManagedTelemetryUnreachableEndpoint(t *testing.T) {
	clearOTELTestEnvironment(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve unreachable endpoint: %v", err)
	}
	endpoint := "http://" + listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("close unreachable endpoint reservation: %v", err)
	}
	identity := uniqueTelemetryIdentity(t, "unreachable")
	var completed atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	config := managedTelemetryConfig(identity, endpoint, telemetryLogs)
	config.Telemetry.Timeout = 100 * time.Millisecond
	config.Setup = func(ctx context.Context) (service.Attempt, error) {
		if err := emitSyntheticTelemetry(ctx); err != nil {
			return service.Attempt{}, err
		}
		return service.Attempt{Runner: func(ctx context.Context) error {
			completed.Store(true)
			<-ctx.Done()
			return nil
		}}, nil
	}
	started := time.Now()
	stderr, warned, runErr := captureProcessStderr(func(warning <-chan struct{}) error {
		warningErr := make(chan error, 1)
		go func() {
			select {
			case <-warning:
				warningErr <- nil
			case <-time.After(35 * time.Second):
				warningErr <- errors.New("telemetry warning deadline exceeded")
			}
			cancel()
		}()
		runErr := service.Run(ctx, config)
		return errors.Join(runErr, <-warningErr)
	})
	if !completed.Load() {
		t.Fatal("module work did not complete")
	}
	if !warned {
		t.Fatalf("stderr warning was not observed: %s", stderr)
	}
	if runErr != nil {
		t.Fatalf("Run() error = %v, want routine telemetry outage to remain nonfatal", runErr)
	}
	if elapsed := time.Since(started); elapsed > 47*time.Second {
		t.Fatalf("Run() took %s with unreachable telemetry endpoint, want at most 47s", elapsed)
	}
	if !strings.Contains(stderr, "telemetry export failed") || !strings.Contains(stderr, `"flowseer.telemetry.signal":"logs"`) {
		t.Fatalf("stderr has no bounded log export warning: %s", stderr)
	}
}

func TestManagedTelemetryFlushesBeforeCancellation(t *testing.T) {
	clearOTELTestEnvironment(t)
	collector := startOTelCollector(t)
	identity := uniqueTelemetryIdentity(t, "shutdown_flush")
	ctx, cancel := context.WithCancel(context.Background())
	config := managedTelemetryConfig(identity, collector.httpEndpoint, allTelemetrySignals)
	config.Setup = func(context.Context) (service.Attempt, error) {
		return service.Attempt{Runner: func(ctx context.Context) error {
			if err := emitSyntheticTelemetry(ctx); err != nil {
				return err
			}
			cancel()
			return nil
		}}, nil
	}
	if err := service.Run(ctx, config); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	collector.waitForServiceSignals(t, identity.Name, allTelemetrySignals)
}

func TestManagedTelemetryConcurrentServicesStayIsolated(t *testing.T) {
	clearOTELTestEnvironment(t)
	collector := startOTelCollector(t)
	identities := []service.Identity{
		uniqueTelemetryIdentity(t, "concurrent_a"),
		uniqueTelemetryIdentity(t, "concurrent_b"),
	}
	globalTracer := otel.GetTracerProvider()
	globalMeter := otel.GetMeterProvider()
	globalPropagator := otel.GetTextMapPropagator()
	globalLogger := slog.Default()
	ready := make(chan struct{}, len(identities))
	release := make(chan struct{})
	errors := make(chan error, len(identities))
	var runs sync.WaitGroup
	for _, identity := range identities {
		config := managedTelemetryConfig(identity, collector.httpEndpoint, allTelemetrySignals)
		config.Setup = func(context.Context) (service.Attempt, error) {
			return service.Attempt{Runner: func(ctx context.Context) error {
				ready <- struct{}{}
				<-release
				return emitSyntheticTelemetry(ctx)
			}}, nil
		}
		runs.Add(1)
		go func() {
			defer runs.Done()
			errors <- service.Run(context.Background(), config)
		}()
	}
	for range identities {
		select {
		case <-ready:
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent services did not reach their runners")
		}
	}
	close(release)
	runs.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("Run() error: %v", err)
		}
	}
	for _, identity := range identities {
		collector.waitForServiceSignals(t, identity.Name, allTelemetrySignals)
	}
	if otel.GetTracerProvider() != globalTracer || otel.GetMeterProvider() != globalMeter || otel.GetTextMapPropagator() != globalPropagator || slog.Default() != globalLogger {
		t.Fatal("managed telemetry mutated a process global")
	}
}

func TestManagedTelemetryDisabledTraceRelay(t *testing.T) {
	clearOTELTestEnvironment(t)
	collector := startOTelCollector(t)
	identity := uniqueTelemetryIdentity(t, "trace_relay")
	relayPath := identity.Name + "/relay"
	downstreamPath := identity.Name + "/downstream"
	relayReady := make(chan struct{})
	downstreamReady := make(chan struct{})
	handled := make(chan struct{}, 1)
	message := &wrapperspb.StringValue{}

	publisher := service.Module{
		Name: "publisher",
		Leaf: &service.Leaf{Setup: func(context.Context) (service.Attempt, error) {
			return service.Attempt{Runner: func(ctx context.Context) error {
				select {
				case <-relayReady:
				case <-ctx.Done():
					return nil
				}
				select {
				case <-downstreamReady:
				case <-ctx.Done():
					return nil
				}
				if err := service.Bus(ctx).Publish(ctx, wrapperspb.String("relay")); err != nil {
					return err
				}
				<-ctx.Done()
				return nil
			}}, nil
		}},
	}
	relay := service.Module{
		Name:      "relay",
		Telemetry: service.TelemetryPolicy{Traces: service.TelemetryDisabled},
		Leaf: &service.Leaf{
			Subscriptions: []service.Subscription{{Kind: service.MessageKindEvent, Message: message}},
			Setup: func(context.Context) (service.Attempt, error) {
				close(relayReady)
				return service.Attempt{
					Runner: func(ctx context.Context) error {
						<-ctx.Done()
						return nil
					},
					Handlers: []service.Handler{{
						Kind:    service.MessageKindEvent,
						Message: message,
						Handle: func(ctx context.Context, _ proto.Message) error {
							return service.Bus(ctx).Command(ctx, downstreamPath, wrapperspb.String("forwarded"))
						},
					}},
				}, nil
			},
		},
	}
	downstream := service.Module{
		Name: "downstream",
		Leaf: &service.Leaf{
			Subscriptions: []service.Subscription{{Kind: service.MessageKindCommand, Message: message}},
			Setup: func(context.Context) (service.Attempt, error) {
				close(downstreamReady)
				return service.Attempt{
					Runner: func(ctx context.Context) error {
						<-ctx.Done()
						return nil
					},
					Handlers: []service.Handler{{
						Kind:    service.MessageKindCommand,
						Message: message,
						Handle: func(context.Context, proto.Message) error {
							handled <- struct{}{}
							return nil
						},
					}},
				}, nil
			},
		},
	}

	runCtx, cancel := context.WithCancel(context.Background())
	config := managedTelemetryConfig(identity, collector.httpEndpoint, telemetryTraces)
	config.Setup = nil
	config.Bus = &service.BusConfig{StoreDir: t.TempDir(), FsyncPolicy: service.BusFsyncPeriodic}
	config.Modules = []service.Module{publisher, relay, downstream}
	done := make(chan error, 1)
	go func() { done <- service.Run(runCtx, config) }()
	select {
	case <-handled:
		cancel()
	case err := <-done:
		t.Fatalf("Run() returned before downstream delivery: %v", err)
	case <-time.After(15 * time.Second):
		cancel()
		t.Fatal("downstream delivery deadline exceeded")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("service shutdown deadline exceeded")
	}
	collector.waitForServiceSignals(t, identity.Name, telemetryTraces)
	spans, err := collector.spanRecords(identity.Name)
	if err != nil {
		t.Fatalf("read Collector spans: %v", err)
	}
	var publicationTraceID []byte
	for _, span := range spans {
		modulePath := modulePathValue(span.GetAttributes()).GetStringValue()
		if modulePath == relayPath {
			t.Fatalf("trace-disabled relay exported span %q", span.GetName())
		}
		if span.GetName() == "flowseer.message.publish" {
			publicationTraceID = span.GetTraceId()
		}
	}
	if len(publicationTraceID) == 0 {
		t.Fatal("Collector has no publisher event span")
	}
	for _, span := range spans {
		modulePath := modulePathValue(span.GetAttributes()).GetStringValue()
		if modulePath != downstreamPath || span.GetName() != "flowseer.message.deliver" {
			continue
		}
		for _, link := range span.GetLinks() {
			if bytes.Equal(link.GetTraceId(), publicationTraceID) {
				return
			}
		}
	}
	t.Fatal("downstream delivery did not link to the publisher trace through the disabled relay")
}

type countingLogHandler struct {
	synthetic *atomic.Int64
}

func (h countingLogHandler) Enabled(context.Context, slog.Level) bool {
	return true
}

func (h countingLogHandler) Handle(_ context.Context, record slog.Record) error {
	if record.Message == "synthetic module record" {
		h.synthetic.Add(1)
	}
	return nil
}

func (h countingLogHandler) WithAttrs([]slog.Attr) slog.Handler {
	return h
}

func (h countingLogHandler) WithGroup(string) slog.Handler {
	return h
}

func captureProcessStderr(run func(<-chan struct{}) error) (string, bool, error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return "", false, err
	}
	original := os.Stderr
	os.Stderr = writer
	defer func() {
		os.Stderr = original
		_ = writer.Close()
		_ = reader.Close()
	}()
	output := make(chan string, 1)
	warning := make(chan struct{})
	go func() {
		var content strings.Builder
		scanner := bufio.NewScanner(reader)
		warned := false
		for scanner.Scan() {
			line := scanner.Text()
			content.WriteString(line)
			content.WriteByte('\n')
			if !warned && strings.Contains(line, "telemetry export failed") {
				close(warning)
				warned = true
			}
		}
		output <- content.String()
	}()
	runErr := run(warning)
	os.Stderr = original
	_ = writer.Close()
	content := <-output
	_ = reader.Close()
	select {
	case <-warning:
		return content, true, runErr
	default:
		return content, false, runErr
	}
}

func managedTelemetryConfig(identity service.Identity, endpoint string, signals telemetrySignalSet) service.Config {
	return service.Config{
		Identity: identity,
		Logger:   slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Telemetry: service.TelemetryConfig{
			Endpoint:         endpoint,
			TraceSampleRatio: fullTraceSampleRatio(),
			Signals:          telemetryPolicy(signals),
		},
		Setup: syntheticTelemetryAttempt,
	}
}

func fullTraceSampleRatio() *float64 {
	ratio := 1.0
	return &ratio
}

func telemetryPolicy(signals telemetrySignalSet) service.TelemetryPolicy {
	declaration := func(signal telemetrySignalSet) service.TelemetryDeclaration {
		if signals&signal != 0 {
			return service.TelemetryEnabled
		}
		return service.TelemetryDisabled
	}
	return service.TelemetryPolicy{
		Logs:    declaration(telemetryLogs),
		Metrics: declaration(telemetryMetrics),
		Traces:  declaration(telemetryTraces),
	}
}
