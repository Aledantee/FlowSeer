package service

import (
	"context"
	"log/slog"
	"slices"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"google.golang.org/protobuf/proto"

	servicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/service/v1"
)

func TestServiceTelemetryScopesUseCurrentSemanticConvention(t *testing.T) {
	res, err := newTelemetryResource(testIdentity())
	if err != nil {
		t.Fatalf("newTelemetryResource() error: %v", err)
	}
	if got := res.SchemaURL(); got != semconv.SchemaURL {
		t.Errorf("resource schema URL = %q, want %q", got, semconv.SchemaURL)
	}

	recorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	telemetry, err := telemetryFromComponents(
		slog.New(slog.DiscardHandler),
		tracerProvider,
		meterProvider,
		defaultPropagator,
	)
	if err != nil {
		t.Fatalf("telemetryFromComponents() error: %v", err)
	}

	_, span := telemetry.tracer.Start(context.Background(), "scope probe")
	span.End()
	if got := recorder.Ended()[0].InstrumentationScope().SchemaURL; got != semconv.SchemaURL {
		t.Errorf("tracer schema URL = %q, want %q", got, semconv.SchemaURL)
	}

	telemetry.lifecycle.Add(context.Background(), 1)
	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	if got := metrics.ScopeMetrics[0].Scope.SchemaURL; got != semconv.SchemaURL {
		t.Errorf("meter schema URL = %q, want %q", got, semconv.SchemaURL)
	}
}

func TestRuntimeTelemetrySchema(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	recorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	telemetry, err := telemetryFromComponents(
		slog.New(slog.DiscardHandler),
		tracerProvider,
		meterProvider,
		defaultPropagator,
	)
	if err != nil {
		t.Fatalf("telemetryFromComponents() error: %v", err)
	}

	ctx, span := startLifecycleSpan(
		context.Background(),
		telemetry.tracer,
		"edge/worker",
		attemptSpanName,
		lifecycleActionStart,
	)
	if err := telemetry.recordLifecycle(ctx, "edge/worker", lifecycleActionStart, lifecycleOutcomeNormal); err != nil {
		t.Fatalf("recordLifecycle() error: %v", err)
	}
	endLifecycleSpan(span, lifecycleOutcomeNormal)

	ended := recorder.Ended()[0]
	if got, want := ended.Name(), attemptSpanName; got != want {
		t.Errorf("span name = %q, want %q", got, want)
	}
	assertAttributeKeys(t, "lifecycle span", ended.Attributes(), []string{
		"flowseer.module.lifecycle.action",
		"flowseer.module.lifecycle.outcome",
		"flowseer.module.path",
	})

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	if len(collected.ScopeMetrics) != 1 || len(collected.ScopeMetrics[0].Metrics) != 1 {
		t.Fatalf("collected metrics = %+v, want one runtime metric", collected.ScopeMetrics)
	}
	metric := collected.ScopeMetrics[0].Metrics[0]
	if got, want := metric.Name, "flowseer.service.module.lifecycle.transitions"; got != want {
		t.Errorf("metric name = %q, want %q", got, want)
	}
	if got, want := metric.Unit, "{transition}"; got != want {
		t.Errorf("metric unit = %q, want %q", got, want)
	}
	if got, want := metric.Description, "Lifecycle transitions recorded after a module starts, stops, or restarts"; got != want {
		t.Errorf("metric description = %q, want %q", got, want)
	}
	sum, ok := metric.Data.(metricdata.Sum[int64])
	if !ok || len(sum.DataPoints) != 1 {
		t.Fatalf("metric data = %T, want one int64 sum point", metric.Data)
	}
	assertAttributeKeys(t, "lifecycle metric", sum.DataPoints[0].Attributes.ToSlice(), []string{
		"flowseer.module.lifecycle.action",
		"flowseer.module.lifecycle.outcome",
		"flowseer.module.path",
	})
}

func TestManagedLogScopeAndResourceIdentityPlacement(t *testing.T) {
	localLogger, localSink := newRecordingLogger()
	capture := &telemetryRequestCapture{}
	factories := defaultTelemetryFactories
	factories.newTransport = func(context.Context, normalizedOTLPConnection) (otlpTransport, telemetryShutdown, error) {
		return capture, nil, nil
	}
	owner, err := newRunTelemetry(context.Background(), testIdentity(), normalizedTelemetryConfig{
		localLogger: localLogger,
		logs:        normalizedLogSignal{backing: signalManaged},
	}, factories)
	if err != nil {
		t.Fatalf("newRunTelemetry() error: %v", err)
	}
	view := owner.view(resolvedTelemetryPolicy{logs: true})
	view.values(testIdentity(), "FLOWSEER_EDGE_", "edge/worker").logger.InfoContext(context.Background(), "schema probe")
	if err := view.recordLifecycle(context.Background(), "edge/worker", lifecycleActionStart, lifecycleOutcomeRunning); err != nil {
		t.Fatalf("recordLifecycle() error: %v", err)
	}
	if err := owner.shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown managed telemetry: %v", err)
	}

	local, ok := localSink.find("schema probe")
	if !ok {
		t.Fatal("local logger did not receive schema probe")
	}
	for _, key := range []string{"service.name", "service.namespace", "service.version", "flowseer.module.path"} {
		if _, ok := local[key]; !ok {
			t.Errorf("local log is missing %q", key)
		}
	}
	lifecycleLocal, ok := localSink.find("module lifecycle")
	if !ok {
		t.Fatal("local logger did not receive lifecycle record")
	}
	for _, key := range []string{"service.name", "service.namespace", "service.version", "flowseer.module.path"} {
		if _, ok := lifecycleLocal[key]; !ok {
			t.Errorf("local lifecycle log is missing %q", key)
		}
	}

	if capture.logs == nil || len(capture.logs.ResourceLogs) != 1 || len(capture.logs.ResourceLogs[0].ScopeLogs) != 1 {
		t.Fatalf("managed logs = %+v, want one resource and scope", capture.logs)
	}
	resourceLogs := capture.logs.ResourceLogs[0]
	if got := resourceLogs.SchemaUrl; got != semconv.SchemaURL {
		t.Errorf("log resource schema URL = %q, want %q", got, semconv.SchemaURL)
	}
	scopeLogs := resourceLogs.ScopeLogs[0]
	if got := scopeLogs.SchemaUrl; got != semconv.SchemaURL {
		t.Errorf("logger schema URL = %q, want %q", got, semconv.SchemaURL)
	}
	if len(scopeLogs.LogRecords) != 2 {
		t.Fatalf("managed log records = %d, want 2", len(scopeLogs.LogRecords))
	}
	wantKeys := map[string][]string{
		"schema probe":     {"flowseer.module.path"},
		"module lifecycle": {"flowseer.module.lifecycle.action", "flowseer.module.lifecycle.outcome", "flowseer.module.path"},
	}
	for _, record := range scopeLogs.LogRecords {
		body := record.Body.GetStringValue()
		var keys []string
		for _, attr := range record.Attributes {
			keys = append(keys, attr.Key)
		}
		slices.Sort(keys)
		want, ok := wantKeys[body]
		if !ok {
			t.Errorf("unexpected managed log body %q", body)
			continue
		}
		if !slices.Equal(keys, want) {
			t.Errorf("managed %q attribute keys = %v, want %v", body, keys, want)
		}
		delete(wantKeys, body)
	}
	if len(wantKeys) != 0 {
		t.Errorf("managed logs are missing records: %v", wantKeys)
	}
}

func TestMessageTelemetrySchema(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	recorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	telemetry, err := telemetryFromComponents(
		slog.New(slog.DiscardHandler),
		tracerProvider,
		meterProvider,
		defaultPropagator,
	)
	if err != nil {
		t.Fatalf("telemetryFromComponents() error: %v", err)
	}
	view := telemetry.view(resolvedTelemetryPolicy{logs: true, metrics: true, traces: true})
	view.recordMessage(context.Background(), "edge/publisher", "google.protobuf.Empty", MessageKindCommand, messagePublished)

	publisher := &MessageBus{telemetry: view}
	_, publication := publisher.startPublicationTrace(context.Background(), MessageKindCommand)
	publication.End()
	envelope := servicev1.Message_builder{
		Kind:       MessageKindCommand.Enum(),
		TargetPath: proto.String("edge/worker"),
		TypeName:   proto.String("google.protobuf.Empty"),
	}.Build()
	_, delivery := (&messageRuntime{}).deliveryTrace(context.Background(), envelope, 2, view)
	delivery.End()

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	if len(collected.ScopeMetrics) != 1 || len(collected.ScopeMetrics[0].Metrics) != 1 {
		t.Fatalf("collected metrics = %+v, want one runtime metric", collected.ScopeMetrics)
	}
	messageMetric := collected.ScopeMetrics[0].Metrics[0]
	if got, want := messageMetric.Name, "flowseer.service.message.operations"; got != want {
		t.Errorf("metric name = %q, want %q", got, want)
	}
	if got, want := messageMetric.Unit, "{operation}"; got != want {
		t.Errorf("metric unit = %q, want %q", got, want)
	}
	if got, want := messageMetric.Description, "Durable message operations recorded after publication, delivery, retry, acknowledgment, rejection, or discard"; got != want {
		t.Errorf("metric description = %q, want %q", got, want)
	}
	sum, ok := messageMetric.Data.(metricdata.Sum[int64])
	if !ok || len(sum.DataPoints) != 1 {
		t.Fatalf("metric data = %T, want one int64 sum point", messageMetric.Data)
	}
	assertAttributeKeys(t, "message metric", sum.DataPoints[0].Attributes.ToSlice(), []string{
		"flowseer.message.kind",
		"flowseer.message.operation",
		"flowseer.message.type",
		"flowseer.module.path",
	})

	spans := recorder.Ended()
	publicationSpan := findRecordedSpan(spans, "flowseer.message.publish")
	if publicationSpan == nil {
		t.Error("message publication span was not recorded with the stable operation name")
	}
	deliverySpan := findRecordedSpan(spans, "flowseer.message.deliver")
	if deliverySpan == nil {
		t.Fatal("message delivery span was not recorded with the stable operation name")
	}
	assertAttributeKeys(t, "message delivery span", deliverySpan.Attributes(), []string{
		"flowseer.message.delivery_attempt",
		"flowseer.message.kind",
		"flowseer.message.type",
		"flowseer.module.path",
	})
}

func assertAttributeKeys(t *testing.T, signal string, attrs []attribute.KeyValue, want []string) {
	t.Helper()
	got := make([]string, 0, len(attrs))
	for _, attr := range attrs {
		got = append(got, string(attr.Key))
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("%s attribute keys = %v, want %v", signal, got, want)
	}
}
