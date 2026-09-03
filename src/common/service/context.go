package service

import (
	"context"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

type contextKey struct{}

type contextValues struct {
	identity   Identity
	modulePath string
	envPrefix  string
	logger     *slog.Logger
	tracer     trace.Tracer
	meter      metric.Meter
	propagator propagation.TextMapPropagator
}

var (
	defaultLogger     = slog.New(slog.DiscardHandler)
	defaultTracer     = tracenoop.NewTracerProvider().Tracer(instrumentationScope)
	defaultMeter      = metricnoop.NewMeterProvider().Meter(instrumentationScope)
	defaultPropagator = propagation.NewCompositeTextMapPropagator()
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

// Propagator returns the attempt text-map propagator attached to ctx. It
// returns an empty propagator when ctx does not belong to a service attempt.
func Propagator(ctx context.Context) propagation.TextMapPropagator {
	propagator := valuesFromContext(ctx).propagator
	if propagator == nil {
		return defaultPropagator
	}
	return propagator
}
