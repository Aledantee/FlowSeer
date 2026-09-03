package service

import (
	"context"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

func TestTelemetryDoesNotChangeGlobals(t *testing.T) {
	wantTracer := otel.GetTracerProvider()
	wantMeter := otel.GetMeterProvider()
	wantPropagator := otel.GetTextMapPropagator()
	wantLogger := slog.Default()

	got, err := newTelemetry(Config{})
	if err != nil {
		t.Fatalf("newTelemetry() error: %v", err)
	}
	if got.logger == nil || got.tracer == nil || got.meter == nil || got.propagator == nil {
		t.Fatal("newTelemetry() returned a nil capability")
	}
	if otel.GetTracerProvider() != wantTracer || otel.GetMeterProvider() != wantMeter || otel.GetTextMapPropagator() != wantPropagator || slog.Default() != wantLogger {
		t.Fatal("newTelemetry() changed process-global providers")
	}
}

func TestTelemetryPreservesInjectedCapabilities(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	tracerProvider := tracenoop.NewTracerProvider()
	meterProvider := metricnoop.NewMeterProvider()
	propagator := propagation.TraceContext{}

	got, err := newTelemetry(Config{
		Logger:         logger,
		TracerProvider: tracerProvider,
		MeterProvider:  meterProvider,
		Propagator:     propagator,
	})
	if err != nil {
		t.Fatalf("newTelemetry() error: %v", err)
	}
	if got.logger != logger {
		t.Error("newTelemetry() did not preserve the logger")
	}
	if _, ok := got.propagator.(propagation.TraceContext); !ok {
		t.Errorf("propagator type = %T, want propagation.TraceContext", got.propagator)
	}
}

func TestLifecycleTelemetryRejectsUnknownDimensions(t *testing.T) {
	telemetry, err := newTelemetry(Config{})
	if err != nil {
		t.Fatalf("newTelemetry() error: %v", err)
	}

	if err := telemetry.recordLifecycle(context.Background(), testIdentity(), "edge", lifecycleAction(99), lifecycleOutcomeRunning); err == nil {
		t.Fatal("recordLifecycle() accepted an unknown action")
	}
	if err := telemetry.recordLifecycle(context.Background(), testIdentity(), "edge", lifecycleActionStart, lifecycleOutcome(99)); err == nil {
		t.Fatal("recordLifecycle() accepted an unknown outcome")
	}
}
