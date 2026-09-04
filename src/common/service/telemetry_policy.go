package service

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/metric"
	metricembedded "go.opentelemetry.io/otel/metric/embedded"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	traceembedded "go.opentelemetry.io/otel/trace/embedded"
)

type telemetryView struct {
	policy         resolvedTelemetryPolicy
	logger         *slog.Logger
	tracer         trace.Tracer
	meter          metric.Meter
	tracerProvider trace.TracerProvider
	meterProvider  metric.MeterProvider
	propagator     propagation.TextMapPropagator
	lifecycle      metric.Int64Counter
	messages       metric.Int64Counter
}

type borrowingTracerProvider struct {
	traceembedded.TracerProvider
	provider trace.TracerProvider
}

func (p borrowingTracerProvider) Tracer(name string, options ...trace.TracerOption) trace.Tracer {
	return p.provider.Tracer(name, options...)
}

type borrowingMeterProvider struct {
	metricembedded.MeterProvider
	provider metric.MeterProvider
}

func (p borrowingMeterProvider) Meter(name string, options ...metric.MeterOption) metric.Meter {
	return p.provider.Meter(name, options...)
}

func (o *telemetryOwner) availableSignals() resolvedTelemetryPolicy {
	return resolvedTelemetryPolicy{
		logs:    o.logsBacking != signalUnavailable,
		metrics: o.metricsBacking != signalUnavailable,
		traces:  o.tracesBacking != signalUnavailable,
	}
}

func (o *telemetryOwner) view(policy resolvedTelemetryPolicy) telemetryView {
	logger := o.localLogger
	if policy.logs {
		logger = o.telemetry.logger
	}
	tracer := defaultTracer
	tracerProvider := trace.TracerProvider(defaultTracerProvider)
	if policy.traces {
		tracer = o.telemetry.tracer
		tracerProvider = o.tracerProvider
		if o.tracesBacking == signalManaged {
			tracerProvider = borrowingTracerProvider{provider: tracerProvider}
		}
	}
	meter := defaultMeter
	meterProvider := metric.MeterProvider(defaultMeterProvider)
	if policy.metrics {
		meter = o.telemetry.meter
		meterProvider = o.meterProvider
		if o.metricsBacking == signalManaged {
			meterProvider = borrowingMeterProvider{provider: meterProvider}
		}
	}
	return telemetryView{
		policy:         policy,
		logger:         logger,
		tracer:         tracer,
		meter:          meter,
		tracerProvider: tracerProvider,
		meterProvider:  meterProvider,
		propagator:     o.telemetry.propagator,
		lifecycle:      o.telemetry.lifecycle,
		messages:       o.telemetry.messages,
	}
}

func (t telemetry) view(policy resolvedTelemetryPolicy) telemetryView {
	if t.owner != nil {
		return t.owner.view(policy)
	}
	owner := telemetryOwner{
		telemetry:      t,
		localLogger:    t.logger,
		tracerProvider: t.tracerProvider,
		meterProvider:  t.meterProvider,
		logsBacking:    signalInjected,
		metricsBacking: signalInjected,
		tracesBacking:  signalInjected,
	}
	return owner.view(policy)
}

func (t telemetry) availableSignals() resolvedTelemetryPolicy {
	if t.owner != nil {
		return t.owner.availableSignals()
	}
	return resolvedTelemetryPolicy{logs: true, metrics: true, traces: true}
}

func (v telemetryView) context(ctx context.Context) context.Context {
	if v.policy.traces {
		return ctx
	}
	return contextWithoutRecordingSpan(ctx)
}

func (v telemetryView) values(identity Identity, envPrefix, modulePath string) contextValues {
	return contextValues{
		identity:       identity,
		modulePath:     modulePath,
		envPrefix:      envPrefix,
		logger:         moduleLogger(v.logger, modulePath),
		tracer:         v.tracer,
		meter:          v.meter,
		tracerProvider: v.tracerProvider,
		meterProvider:  v.meterProvider,
		propagator:     v.propagator,
		attributes:     moduleAttributes(modulePath),
	}
}

func (v telemetryView) recordLifecycle(
	ctx context.Context,
	modulePath string,
	action lifecycleAction,
	outcome lifecycleOutcome,
) error {
	return recordLifecycle(ctx, v.logger, v.lifecycle, v.policy.metrics, modulePath, action, outcome)
}
