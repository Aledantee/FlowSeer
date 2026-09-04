package service

import (
	"context"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

func TestAttemptContextAccessors(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	tracer := tracenoop.NewTracerProvider().Tracer(instrumentationScope)
	meter := noop.NewMeterProvider().Meter(instrumentationScope)
	propagator := propagation.TraceContext{}
	messageBus := &MessageBus{sourcePath: "edge/ingest/syslog"}
	values := contextValues{
		identity:   testIdentity(),
		modulePath: "edge/ingest/syslog",
		envPrefix:  "FLOWSEER_EDGE_",
		logger:     logger,
		tracer:     tracer,
		meter:      meter,
		propagator: propagator,
		bus:        messageBus,
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
	if _, ok := Propagator(ctx).(propagation.TraceContext); !ok {
		t.Errorf("Propagator() type = %T, want propagation.TraceContext", Propagator(ctx))
	}
	if Bus(ctx) != messageBus {
		t.Error("Bus() did not preserve the attempt message bus")
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
