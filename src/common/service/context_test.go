package service

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

func TestAttemptContextAccessors(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	tracerProvider := tracenoop.NewTracerProvider()
	meterProvider := noop.NewMeterProvider()
	tracer := tracerProvider.Tracer(instrumentationScope)
	meter := meterProvider.Meter(instrumentationScope)
	propagator := propagation.TraceContext{}
	messageBus := &MessageBus{sourcePath: "edge/ingest/syslog"}
	values := contextValues{
		identity:       testIdentity(),
		modulePath:     "edge/ingest/syslog",
		envPrefix:      "FLOWSEER_EDGE_",
		logger:         logger,
		tracer:         tracer,
		meter:          meter,
		tracerProvider: tracerProvider,
		meterProvider:  meterProvider,
		propagator:     propagator,
		bus:            messageBus,
	}
	ctx := context.WithoutCancel(withContextValues(context.Background(), values))

	if got := Name(ctx); got != "edge" {
		t.Errorf("Name() = %q, want %q", got, "edge")
	}
	if got := Namespace(ctx); got != "flowseer" {
		t.Errorf("Namespace() = %q, want %q", got, "flowseer")
	}
	if got := Version(ctx); got != "v1" {
		t.Errorf("Version() = %q, want %q", got, "v1")
	}
	if got := ModulePath(ctx); got != "edge/ingest/syslog" {
		t.Errorf("ModulePath() = %q, want %q", got, "edge/ingest/syslog")
	}
	if got := EnvPrefix(ctx); got != "FLOWSEER_EDGE_" {
		t.Errorf("EnvPrefix() = %q, want %q", got, "FLOWSEER_EDGE_")
	}
	if got := EnvKey(ctx, "ENABLED"); got != "FLOWSEER_EDGE_ENABLED" {
		t.Errorf("EnvKey() = %q, want %q", got, "FLOWSEER_EDGE_ENABLED")
	}
	if Logger(ctx) != logger {
		t.Error("Logger() did not preserve the injected logger")
	}
	if Tracer(ctx) != tracer {
		t.Error("Tracer() did not preserve the attempt tracer")
	}
	if Meter(ctx) != meter {
		t.Error("Meter() did not preserve the attempt meter")
	}
	if TracerProvider(ctx) != tracerProvider || MeterProvider(ctx) != meterProvider {
		t.Error("provider accessors did not preserve attempt provider identity")
	}
	if _, ok := Propagator(ctx).(propagation.TraceContext); !ok {
		t.Errorf("Propagator() type = %T, want propagation.TraceContext", Propagator(ctx))
	}
	if Bus(ctx) != messageBus {
		t.Error("Bus() did not preserve the attempt message bus")
	}
}

func TestContextWithoutRecordingSpanPreservesContextButIsolatesSpan(t *testing.T) {
	type identityKey struct{}
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	deadline := time.Now().Add(time.Minute)
	parent, cancel := context.WithDeadline(context.WithValue(context.Background(), identityKey{}, "caller"), deadline)
	defer cancel()
	parent, parentSpan := provider.Tracer("parent").Start(parent, "parent")

	got := contextWithoutRecordingSpan(parent)
	if got.Value(identityKey{}) != "caller" {
		t.Fatal("context identity was not preserved")
	}
	if gotDeadline, ok := got.Deadline(); !ok || !gotDeadline.Equal(deadline) {
		t.Fatalf("deadline = %v, %t, want %v", gotDeadline, ok, deadline)
	}
	if gotSpanContext := trace.SpanContextFromContext(got); !gotSpanContext.Equal(parentSpan.SpanContext()) {
		t.Fatalf("span context = %v, want %v", gotSpanContext, parentSpan.SpanContext())
	}
	isolated := trace.SpanFromContext(got)
	if isolated == parentSpan || isolated.IsRecording() {
		t.Fatal("context retained the mutable recording parent span")
	}
	isolated.End()
	if len(recorder.Ended()) != 0 {
		t.Fatal("ending the isolated span ended the parent")
	}
	parentSpan.End()

	canceled, cancelNow := context.WithCancel(got)
	cancelNow()
	select {
	case <-canceled.Done():
	case <-time.After(time.Second):
		t.Fatal("cancellation was not preserved")
	}
}

func TestContextAccessorsHaveSafeDefaults(t *testing.T) {
	ctx := context.Background()

	if Logger(ctx) == nil || Tracer(ctx) == nil || Meter(ctx) == nil || Propagator(ctx) == nil || Bus(ctx) == nil {
		t.Fatal("context accessors returned a nil default")
	}
	if Name(ctx) != "" || Namespace(ctx) != "" || Version(ctx) != "" || ModulePath(ctx) != "" || EnvPrefix(ctx) != "" {
		t.Fatal("identity accessors returned values for a plain context")
	}
}

func TestLookupEnvUsesAttemptPrefix(t *testing.T) {
	t.Setenv("FLOWSEER_EDGE_PORT", "8080")
	ctx := withContextValues(context.Background(), contextValues{envPrefix: "FLOWSEER_EDGE_"})

	got, ok := LookupEnv(ctx, "PORT")
	if !ok || got != "8080" {
		t.Errorf("LookupEnv() = %q, %t, want %q, true", got, ok, "8080")
	}
}
