package service

import (
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

const (
	instrumentationScope   = "go.aledante.io/FlowSeer/src/common/service"
	instrumentationVersion = "1.0.0"
)

type lifecycleAction uint8

const (
	lifecycleActionStart lifecycleAction = iota + 1
	lifecycleActionStop
)

func (a lifecycleAction) string() (string, bool) {
	switch a {
	case lifecycleActionStart:
		return "start", true
	case lifecycleActionStop:
		return "stop", true
	default:
		return "", false
	}
}

type lifecycleOutcome uint8

const (
	lifecycleOutcomeRunning lifecycleOutcome = iota + 1
	lifecycleOutcomeNormal
	lifecycleOutcomeError
	lifecycleOutcomePanic
	lifecycleOutcomeCanceled
)

func (o lifecycleOutcome) string() (string, bool) {
	switch o {
	case lifecycleOutcomeRunning:
		return "running", true
	case lifecycleOutcomeNormal:
		return "normal", true
	case lifecycleOutcomeError:
		return "error", true
	case lifecycleOutcomePanic:
		return "panic", true
	case lifecycleOutcomeCanceled:
		return "canceled", true
	default:
		return "", false
	}
}

type telemetry struct {
	logger     *slog.Logger
	tracer     trace.Tracer
	meter      metric.Meter
	propagator propagation.TextMapPropagator
	lifecycle  metric.Int64Counter
}

func newTelemetry(config Config) (telemetry, error) {
	logger := config.Logger
	if logger == nil {
		logger = defaultLogger
	}
	tracerProvider := config.TracerProvider
	if tracerProvider == nil {
		tracerProvider = tracenoop.NewTracerProvider()
	}
	meterProvider := config.MeterProvider
	if meterProvider == nil {
		meterProvider = metricnoop.NewMeterProvider()
	}
	propagator := config.Propagator
	if propagator == nil {
		propagator = defaultPropagator
	}

	tracer := tracerProvider.Tracer(instrumentationScope, trace.WithInstrumentationVersion(instrumentationVersion))
	meter := meterProvider.Meter(instrumentationScope, metric.WithInstrumentationVersion(instrumentationVersion))
	lifecycle, err := meter.Int64Counter(
		"flowseer.service.module.lifecycle",
		metric.WithDescription("Module lifecycle transitions"),
	)
	if err != nil {
		return telemetry{}, fmt.Errorf("create service lifecycle counter: %w", err)
	}

	return telemetry{
		logger:     logger,
		tracer:     tracer,
		meter:      meter,
		propagator: propagator,
		lifecycle:  lifecycle,
	}, nil
}

func (t telemetry) values(identity Identity, envPrefix, modulePath string) contextValues {
	return contextValues{
		identity:   identity,
		modulePath: modulePath,
		envPrefix:  envPrefix,
		logger:     t.logger,
		tracer:     t.tracer,
		meter:      t.meter,
		propagator: t.propagator,
	}
}

func (t telemetry) recordLifecycle(
	ctx context.Context,
	identity Identity,
	modulePath string,
	action lifecycleAction,
	outcome lifecycleOutcome,
) error {
	actionName, ok := action.string()
	if !ok {
		return fmt.Errorf("unknown lifecycle action %d", action)
	}
	outcomeName, ok := outcome.string()
	if !ok {
		return fmt.Errorf("unknown lifecycle outcome %d", outcome)
	}

	attrs := []attribute.KeyValue{
		attribute.String("service.name", identity.Name),
		attribute.String("service.namespace", identity.Namespace),
		attribute.String("service.version", identity.Version),
		attribute.String("service.module.path", modulePath),
		attribute.String("service.lifecycle.action", actionName),
		attribute.String("service.lifecycle.outcome", outcomeName),
	}
	t.lifecycle.Add(ctx, 1, metric.WithAttributes(attrs...))
	t.logger.InfoContext(ctx, "module lifecycle",
		"service", identity.Name,
		"namespace", identity.Namespace,
		"version", identity.Version,
		"module", modulePath,
		"action", actionName,
		"outcome", outcomeName,
	)

	return nil
}
