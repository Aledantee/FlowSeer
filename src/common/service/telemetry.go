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

	modulePathKey = "service.module.path"
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
	owner          *telemetryOwner
	logger         *slog.Logger
	tracer         trace.Tracer
	meter          metric.Meter
	tracerProvider trace.TracerProvider
	meterProvider  metric.MeterProvider
	propagator     propagation.TextMapPropagator
	lifecycle      metric.Int64Counter
	messages       metric.Int64Counter
}

// traceLogHandler links a log record to the span that produced it so a record
// can be found from its trace and the reverse. Records written without a
// recording span, and records written outside the runtime, are unchanged. A
// module that opens a slog group before logging nests the two identifiers in
// that group, because slog offers no way to add a record attribute above an
// open group.
type traceLogHandler struct {
	slog.Handler
}

func (h traceLogHandler) Handle(ctx context.Context, record slog.Record) error {
	if span := trace.SpanContextFromContext(ctx); span.IsValid() {
		// Copies of a Record share state, so a handler that adds attributes has
		// to clone first. Cloning inside the branch keeps the far more common
		// no-span path free of the copy.
		record = record.Clone()
		record.AddAttrs(
			slog.String("trace_id", span.TraceID().String()),
			slog.String("span_id", span.SpanID().String()),
		)
	}
	return h.Handler.Handle(ctx, record)
}

func (h traceLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return traceLogHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h traceLogHandler) WithGroup(name string) slog.Handler {
	return traceLogHandler{Handler: h.Handler.WithGroup(name)}
}

// identityAttributes returns the bounded dimension set shared by runtime
// telemetry and by module instruments that read it through [Attributes].
func identityAttributes(identity Identity, modulePath string) attribute.Set {
	return attribute.NewSet(
		semconv.ServiceName(identity.Name),
		semconv.ServiceNamespace(identity.Namespace),
		semconv.ServiceVersion(identity.Version),
		attribute.String(modulePathKey, modulePath),
	)
}

func newTelemetry(config Config) (telemetry, error) {
	logger := config.Logger
	if logger == nil {
		logger = defaultLogger
	}
	localHandler := traceLogHandler{Handler: logger.Handler()}
	if config.LogHandler != nil {
		logger = slog.New(multiSlogHandler{handlers: []slog.Handler{localHandler, config.LogHandler}})
	} else {
		logger = slog.New(localHandler)
	}
	propagator := config.Propagator
	if propagator == nil {
		propagator = defaultPropagator
	}

	tracerProvider := config.TracerProvider
	if tracerProvider == nil {
		tracerProvider = defaultTracerProvider
	}
	meterProvider := config.MeterProvider
	if meterProvider == nil {
		meterProvider = defaultMeterProvider
	}
	return telemetryFromComponents(logger, tracerProvider, meterProvider, propagator)
}

func telemetryFromComponents(
	logger *slog.Logger,
	tracerProvider trace.TracerProvider,
	meterProvider metric.MeterProvider,
	propagator propagation.TextMapPropagator,
) (telemetry, error) {
	if tracerProvider == nil {
		tracerProvider = defaultTracerProvider
	}
	if meterProvider == nil {
		meterProvider = defaultMeterProvider
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
	messages, err := meter.Int64Counter(
		"flowseer.service.message.lifecycle",
		metric.WithDescription("Durable message lifecycle transitions"),
	)
	if err != nil {
		return telemetry{}, fmt.Errorf("create service message lifecycle counter: %w", err)
	}

	return telemetry{
		logger:         logger,
		tracer:         tracer,
		meter:          meter,
		tracerProvider: tracerProvider,
		meterProvider:  meterProvider,
		propagator:     propagator,
		lifecycle:      lifecycle,
		messages:       messages,
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
		attribute.String(modulePathKey, modulePath),
		attribute.String("messaging.message.type", typeName),
		attribute.String("messaging.message.kind", messageKindToken(kind)),
		attribute.String("messaging.operation", string(action)),
	}
	t.messages.Add(ctx, 1, metric.WithAttributes(attrs...))
	t.logger.DebugContext(ctx, "message lifecycle", modulePathKey, modulePath, "message_type", typeName, "message_kind", messageKindToken(kind), "action", action)
}

func (t telemetry) recordDisposition(ctx context.Context, modulePath, typeName string, kind servicev1.MessageKind, settlement *servicev1.Settlement) {
	action := messageAcknowledged
	if settlement.GetState() == servicev1.SettlementState_SETTLEMENT_STATE_DISCARD {
		action = messageDiscarded
	}
	t.recordMessage(ctx, modulePath, typeName, kind, action)
	t.logger.InfoContext(ctx, "message disposition",
		modulePathKey, modulePath,
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
		identity:       identity,
		modulePath:     modulePath,
		envPrefix:      envPrefix,
		logger:         moduleLogger(t.logger, identity, modulePath),
		tracer:         t.tracer,
		meter:          t.meter,
		tracerProvider: t.tracerProvider,
		meterProvider:  t.meterProvider,
		propagator:     t.propagator,
		attributes:     identityAttributes(identity, modulePath),
	}
}

func moduleLogger(logger *slog.Logger, identity Identity, modulePath string) *slog.Logger {
	return logger.With(
		slog.String(string(semconv.ServiceNameKey), identity.Name),
		slog.String(string(semconv.ServiceNamespaceKey), identity.Namespace),
		slog.String(string(semconv.ServiceVersionKey), identity.Version),
		slog.String(modulePathKey, modulePath),
	)
}

func (t telemetry) recordLifecycle(
	ctx context.Context,
	identity Identity,
	modulePath string,
	action lifecycleAction,
	outcome lifecycleOutcome,
) error {
	return recordLifecycle(ctx, t.logger, t.lifecycle, true, identity, modulePath, action, outcome)
}

func recordLifecycle(
	ctx context.Context,
	logger *slog.Logger,
	lifecycle metric.Int64Counter,
	recordMetric bool,
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

	if recordMetric {
		lifecycle.Add(ctx, 1,
			metric.WithAttributeSet(identityAttributes(identity, modulePath)),
			metric.WithAttributes(
				attribute.String("service.lifecycle.action", actionName),
				attribute.String("service.lifecycle.outcome", outcomeName),
			),
		)
	}
	logger.InfoContext(ctx, "module lifecycle",
		string(semconv.ServiceNameKey), identity.Name,
		string(semconv.ServiceNamespaceKey), identity.Namespace,
		string(semconv.ServiceVersionKey), identity.Version,
		modulePathKey, modulePath,
		"action", actionName,
		"outcome", outcomeName,
	)

	return nil
}
