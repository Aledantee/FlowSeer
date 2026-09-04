package service

import (
	"context"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

type contextKey struct{}

type contextValues struct {
	identity       Identity
	modulePath     string
	envPrefix      string
	logger         *slog.Logger
	tracer         trace.Tracer
	meter          metric.Meter
	tracerProvider trace.TracerProvider
	meterProvider  metric.MeterProvider
	propagator     propagation.TextMapPropagator
	attributes     attribute.Set
	bus            *MessageBus
}

// Bus returns the attempt-scoped durable message bus. Outside a bus-enabled
// attempt it returns a safe handle whose operations report that messaging is
// unavailable.
func Bus(ctx context.Context) *MessageBus {
	bus := valuesFromContext(ctx).bus
	if bus == nil {
		return disabledMessageBus
	}
	return bus
}

var (
	defaultLogger         = slog.New(slog.DiscardHandler)
	defaultTracerProvider = tracenoop.NewTracerProvider()
	defaultMeterProvider  = metricnoop.NewMeterProvider()
	defaultTracer         = defaultTracerProvider.Tracer(instrumentationScope)
	defaultMeter          = defaultMeterProvider.Meter(instrumentationScope)
	defaultPropagator     = propagation.NewCompositeTextMapPropagator()
)

func withContextValues(ctx context.Context, values contextValues) context.Context {
	return context.WithValue(ctx, contextKey{}, values)
}

func valuesFromContext(ctx context.Context) contextValues {
	if ctx == nil {
		return contextValues{}
	}
	values, _ := ctx.Value(contextKey{}).(contextValues)
	return values
}

func contextWithoutRecordingSpan(ctx context.Context) context.Context {
	return trace.ContextWithSpanContext(ctx, trace.SpanContextFromContext(ctx))
}

// Name returns the service name attached to ctx, or an empty string when ctx
// does not belong to a service attempt.
func Name(ctx context.Context) string {
	return valuesFromContext(ctx).identity.Name
}

// Namespace returns the service namespace attached to ctx, or an empty string
// when ctx does not belong to a service attempt.
func Namespace(ctx context.Context) string {
	return valuesFromContext(ctx).identity.Namespace
}

// Version returns the service version attached to ctx, or an empty string when
// ctx does not belong to a service attempt.
func Version(ctx context.Context) string {
	return valuesFromContext(ctx).identity.Version
}

// ModulePath returns the stable logical module path attached to ctx. It returns
// an empty string outside a module attempt.
func ModulePath(ctx context.Context) string {
	return valuesFromContext(ctx).modulePath
}

// EnvPrefix returns the service environment prefix attached to ctx. It returns
// an empty string outside a module attempt.
func EnvPrefix(ctx context.Context) string {
	return valuesFromContext(ctx).envPrefix
}

// EnvKey returns key in the service's environment namespace. A context without
// a service prefix leaves key unchanged.
func EnvKey(ctx context.Context, key string) string {
	return EnvPrefix(ctx) + key
}

// LookupEnv reads key from the service's environment namespace.
func LookupEnv(ctx context.Context, key string) (string, bool) {
	return os.LookupEnv(EnvKey(ctx, key))
}

// Logger returns the attempt logger attached to ctx. It returns a discard
// logger when ctx does not belong to a service attempt and never returns nil.
func Logger(ctx context.Context) *slog.Logger {
	logger := valuesFromContext(ctx).logger
	if logger == nil {
		return defaultLogger
	}
	return logger
}

// Tracer returns the attempt tracer attached to ctx. It returns a no-op tracer
// when ctx does not belong to a service attempt.
func Tracer(ctx context.Context) trace.Tracer {
	tracer := valuesFromContext(ctx).tracer
	if tracer == nil {
		return defaultTracer
	}
	return tracer
}

// Meter returns the attempt meter attached to ctx. It returns a no-op meter
// when ctx does not belong to a service attempt.
func Meter(ctx context.Context) metric.Meter {
	meter := valuesFromContext(ctx).meter
	if meter == nil {
		return defaultMeter
	}
	return meter
}

// TracerProvider returns the attempt tracer provider attached to ctx. A module
// that owns an instrumentation scope creates its own tracer from it rather than
// reusing [Tracer], whose scope names the service runtime. It returns a no-op
// provider when ctx does not belong to a service attempt.
func TracerProvider(ctx context.Context) trace.TracerProvider {
	provider := valuesFromContext(ctx).tracerProvider
	if provider == nil {
		return defaultTracerProvider
	}
	return provider
}

// MeterProvider returns the attempt meter provider attached to ctx. A module
// that owns an instrumentation scope creates its own meter from it rather than
// reusing [Meter], whose scope names the service runtime. It returns a no-op
// provider when ctx does not belong to a service attempt.
func MeterProvider(ctx context.Context) metric.MeterProvider {
	provider := valuesFromContext(ctx).meterProvider
	if provider == nil {
		return defaultMeterProvider
	}
	return provider
}

// Propagator returns the attempt text-map propagator attached to ctx. It
// returns an empty propagator when ctx does not belong to a service attempt.
func Propagator(ctx context.Context) propagation.TextMapPropagator {
	propagator := valuesFromContext(ctx).propagator
	if propagator == nil {
		return defaultPropagator
	}
	return propagator
}

// Attributes returns the compatibility attribute set for the current module
// attempt. It contains service name, namespace, version, and the legacy module
// path key. New instrumentation should keep service identity on the Resource
// and use flowseer.module.path for the bounded occurrence dimension. It is
// empty when ctx does not belong to a service attempt.
//
// The accessors of [attribute.Set] take a pointer receiver, so assign the
// result before reading it: attrs := service.Attributes(ctx) followed by
// attrs.ToSlice(). Calling a method on the return value directly does not
// compile.
func Attributes(ctx context.Context) attribute.Set {
	return valuesFromContext(ctx).attributes
}
