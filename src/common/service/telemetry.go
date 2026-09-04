package service

import (
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"

	servicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/service/v1"
)

const (
	instrumentationScope   = "go.aledante.io/FlowSeer/src/common/service"
	instrumentationVersion = "1.0.0"
)

type lifecycleAction uint8

const (
	lifecycleActionStart lifecycleAction = iota + 1
	lifecycleActionStop
	lifecycleActionRestart
)

func (a lifecycleAction) string() (string, bool) {
	switch a {
	case lifecycleActionStart:
		return "start", true
	case lifecycleActionStop:
		return "stop", true
	case lifecycleActionRestart:
		return "restart", true
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
	messages   metric.Int64Counter
}

func newTelemetry(config Config) (telemetry, error) {
	logger := config.Logger
	if logger == nil {
		logger = defaultLogger
	}
	propagator := config.Propagator
	if propagator == nil {
		propagator = defaultPropagator
	}

	tracer := defaultTracer
	if config.TracerProvider != nil {
		tracer = config.TracerProvider.Tracer(instrumentationScope, trace.WithInstrumentationVersion(instrumentationVersion))
	}
	meter := defaultMeter
	if config.MeterProvider != nil {
		meter = config.MeterProvider.Meter(instrumentationScope, metric.WithInstrumentationVersion(instrumentationVersion))
	}
	lifecycle, err := meter.Int64Counter(
		"flowseer.service.module.lifecycle",
		metric.WithDescription("Module lifecycle transitions"),
	)
	if err != nil {
		return telemetry{}, fmt.Errorf("create service lifecycle counter: %w", err)
	}
	messages, err := meter.Int64Counter(
		"flowseer.service.message.lifecycle",
		metric.WithDescription("Durable message lifecycle transitions"),
	)
	if err != nil {
		return telemetry{}, fmt.Errorf("create service message lifecycle counter: %w", err)
	}

	return telemetry{
		logger:     logger,
		tracer:     tracer,
		meter:      meter,
		propagator: propagator,
		lifecycle:  lifecycle,
		messages:   messages,
	}, nil
}

type messageAction string

const (
	messagePublished    messageAction = "published"
	messageDelivered    messageAction = "delivered"
	messageRetry        messageAction = "retry"
	messageAcknowledged messageAction = "acknowledged"
	messageDiscarded    messageAction = "discarded"
	messageRejected     messageAction = "rejected"
)

func (t telemetry) recordMessage(ctx context.Context, modulePath, typeName string, kind servicev1.MessageKind, action messageAction) {
	attrs := []attribute.KeyValue{
		attribute.String("service.module.path", modulePath),
		attribute.String("messaging.message.type", typeName),
		attribute.String("messaging.message.kind", messageKindToken(kind)),
		attribute.String("messaging.operation", string(action)),
	}
	t.messages.Add(ctx, 1, metric.WithAttributes(attrs...))
	t.logger.DebugContext(ctx, "message lifecycle", "module", modulePath, "message_type", typeName, "message_kind", messageKindToken(kind), "action", action)
}

func (t telemetry) recordDisposition(ctx context.Context, modulePath, typeName string, kind servicev1.MessageKind, settlement *servicev1.Settlement) {
	action := messageAcknowledged
	if settlement.GetState() == servicev1.SettlementState_SETTLEMENT_STATE_DISCARD {
		action = messageDiscarded
	}
	t.recordMessage(ctx, modulePath, typeName, kind, action)
	t.logger.InfoContext(ctx, "message disposition",
		"module", modulePath,
		"message_type", typeName,
		"message_kind", messageKindToken(kind),
		"disposition", settlement.GetState().String(),
		"disposition_id", settlement.GetDispositionId(),
		"retry_count", settlement.GetRetryCount(),
	)
	trace.SpanFromContext(ctx).AddEvent("message disposition", trace.WithAttributes(
		attribute.String("messaging.disposition", settlement.GetState().String()),
		attribute.String("messaging.disposition.id", settlement.GetDispositionId()),
		attribute.Int64("messaging.retry.count", int64(settlement.GetRetryCount())),
	))
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
		semconv.ServiceName(identity.Name),
		semconv.ServiceNamespace(identity.Namespace),
		semconv.ServiceVersion(identity.Version),
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
