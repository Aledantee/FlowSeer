package service

import (
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"

	servicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/service/v1"
)

const (
	instrumentationScope   = "go.aledante.io/FlowSeer/src/common/service"
	instrumentationVersion = "1.0.0"
	startupSpanName        = "flowseer.service.startup"
	attemptSpanName        = "flowseer.service.module.attempt"
	shutdownSpanName       = "flowseer.service.shutdown"
	publicationSpanName    = "flowseer.message.publish"
	deliverySpanName       = "flowseer.message.deliver"

	modulePathKey             = "flowseer.module.path"
	legacyModulePathKey       = "service.module.path"
	moduleLifecycleActionKey  = "flowseer.module.lifecycle.action"
	moduleLifecycleOutcomeKey = "flowseer.module.lifecycle.outcome"
	messageTypeKey            = "flowseer.message.type"
	messageKindKey            = "flowseer.message.kind"
	messageOperationKey       = "flowseer.message.operation"
	messageDispositionKey     = "flowseer.message.disposition"
	messageRetryCountKey      = "flowseer.message.retry_count"
	messageDeliveryAttemptKey = "flowseer.message.delivery_attempt"
)

func startLifecycleSpan(
	ctx context.Context,
	tracer trace.Tracer,
	modulePath string,
	name string,
	action lifecycleAction,
) (context.Context, trace.Span) {
	actionName, _ := action.string()
	moduleSet := moduleAttributes(modulePath)
	attributes := moduleSet.ToSlice()
	attributes = append(attributes, attribute.String(moduleLifecycleActionKey, actionName))
	return tracer.Start(ctx, name, trace.WithAttributes(attributes...))
}

func endLifecycleSpan(span trace.Span, outcome lifecycleOutcome) {
	outcomeName, _ := outcome.string()
	span.SetAttributes(attribute.String(moduleLifecycleOutcomeKey, outcomeName))
	if outcome == lifecycleOutcomeError || outcome == lifecycleOutcomePanic {
		span.SetStatus(codes.Error, outcomeName)
	}
	span.End()
}

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
// can be found from its trace and the reverse. Records written without a valid
// span context, and records written outside the runtime, are unchanged. A
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

// moduleAttributes returns the bounded dimension set shared by runtime
// telemetry and by module instruments that read it through [Attributes].
func moduleAttributes(modulePath string) attribute.Set {
	return attribute.NewSet(
		attribute.String(modulePathKey, modulePath),
	)
}

// compatibilityAttributes preserves the public [Attributes] schema while
// runtime-owned telemetry uses Resource identity and namespaced occurrence
// attributes.
func compatibilityAttributes(identity Identity, modulePath string) attribute.Set {
	return attribute.NewSet(
		semconv.ServiceName(identity.Name),
		semconv.ServiceNamespace(identity.Namespace),
		semconv.ServiceVersion(identity.Version),
		attribute.String(legacyModulePathKey, modulePath),
	)
}

func newTelemetry(config Config) (telemetry, error) {
	logger := config.Logger
	if logger == nil {
		logger = defaultLogger
	}
	localHandler := traceLogHandler{Handler: logger.Handler().WithAttrs(serviceIdentityLogAttrs(config.Identity))}
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
	tracer := tracerProvider.Tracer(
		instrumentationScope,
		trace.WithInstrumentationVersion(instrumentationVersion),
		trace.WithSchemaURL(semconv.SchemaURL),
	)
	meter := meterProvider.Meter(
		instrumentationScope,
		metric.WithInstrumentationVersion(instrumentationVersion),
		metric.WithSchemaURL(semconv.SchemaURL),
	)
	lifecycle, err := meter.Int64Counter(
		"flowseer.service.module.lifecycle.transitions",
		metric.WithUnit("{transition}"),
		metric.WithDescription("Lifecycle transitions recorded after a module starts, stops, or restarts"),
	)
	if err != nil {
		return telemetry{}, fmt.Errorf("create service lifecycle counter: %w", err)
	}
	messages, err := meter.Int64Counter(
		"flowseer.service.message.operations",
		metric.WithUnit("{operation}"),
		metric.WithDescription("Durable message operations recorded after publication, delivery, retry, acknowledgment, rejection, or discard"),
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

func (v telemetryView) recordMessage(ctx context.Context, modulePath, typeName string, kind servicev1.MessageKind, action messageAction) {
	if v.policy.metrics {
		attrs := []attribute.KeyValue{
			attribute.String(modulePathKey, modulePath),
			attribute.String(messageTypeKey, typeName),
			attribute.String(messageKindKey, messageKindToken(kind)),
			attribute.String(messageOperationKey, string(action)),
		}
		v.messages.Add(ctx, 1, metric.WithAttributes(attrs...))
	}
	v.logger.DebugContext(ctx, "message lifecycle",
		modulePathKey, modulePath,
		messageTypeKey, typeName,
		messageKindKey, messageKindToken(kind),
		messageOperationKey, action,
	)
}

func (v telemetryView) recordDisposition(ctx context.Context, modulePath, typeName string, kind servicev1.MessageKind, settlement *servicev1.Settlement) {
	action := messageAcknowledged
	if settlement.GetState() == servicev1.SettlementState_SETTLEMENT_STATE_DISCARD {
		action = messageDiscarded
	}
	v.recordMessage(ctx, modulePath, typeName, kind, action)
	v.logger.InfoContext(ctx, "message disposition",
		modulePathKey, modulePath,
		messageTypeKey, typeName,
		messageKindKey, messageKindToken(kind),
		messageDispositionKey, settlement.GetState().String(),
		messageRetryCountKey, settlement.GetRetryCount(),
	)
	if v.policy.traces {
		trace.SpanFromContext(ctx).AddEvent("message disposition", trace.WithAttributes(
			attribute.String(messageDispositionKey, settlement.GetState().String()),
			attribute.Int64(messageRetryCountKey, int64(settlement.GetRetryCount())),
		))
	}
}

func (t telemetry) values(identity Identity, envPrefix, modulePath string) contextValues {
	return contextValues{
		identity:       identity,
		modulePath:     modulePath,
		envPrefix:      envPrefix,
		logger:         moduleLogger(t.logger, modulePath),
		tracer:         t.tracer,
		meter:          t.meter,
		tracerProvider: t.tracerProvider,
		meterProvider:  t.meterProvider,
		propagator:     t.propagator,
		attributes:     compatibilityAttributes(identity, modulePath),
	}
}

func moduleLogger(logger *slog.Logger, modulePath string) *slog.Logger {
	return logger.With(slog.String(modulePathKey, modulePath))
}

func serviceIdentityLogAttrs(identity Identity) []slog.Attr {
	return []slog.Attr{
		slog.String(string(semconv.ServiceNameKey), identity.Name),
		slog.String(string(semconv.ServiceNamespaceKey), identity.Namespace),
		slog.String(string(semconv.ServiceVersionKey), identity.Version),
	}
}

func (t telemetry) recordLifecycle(
	ctx context.Context,
	modulePath string,
	action lifecycleAction,
	outcome lifecycleOutcome,
) error {
	return recordLifecycle(ctx, t.logger, t.lifecycle, true, modulePath, action, outcome)
}

func recordLifecycle(
	ctx context.Context,
	logger *slog.Logger,
	lifecycle metric.Int64Counter,
	recordMetric bool,
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
			metric.WithAttributeSet(moduleAttributes(modulePath)),
			metric.WithAttributes(
				attribute.String(moduleLifecycleActionKey, actionName),
				attribute.String(moduleLifecycleOutcomeKey, outcomeName),
			),
		)
	}
	logger.InfoContext(ctx, "module lifecycle",
		modulePathKey, modulePath,
		moduleLifecycleActionKey, actionName,
		moduleLifecycleOutcomeKey, outcomeName,
	)

	return nil
}
