package service

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

var envPrefixPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*_$`)

// Runner performs one module attempt. It must return after ctx is canceled.
// A Runner may be called only once and need not be safe for concurrent use.
type Runner func(ctx context.Context) error

// Attempt contains the fresh runner and canonical handlers constructed by one
// Setup call. Its values are owned by that attempt and need not be reusable.
type Attempt struct {
	// Runner performs the module's non-message work.
	Runner Runner
	// Handlers must exactly match the leaf's static subscriptions.
	Handlers []Handler
}

// SetupFunc constructs the mutable state for one module attempt. Each call
// must return a fresh Attempt whose lifetime is bounded by ctx.
type SetupFunc func(ctx context.Context) (Attempt, error)

// Config declares one service run. Callers must choose either Setup for an
// implicit singleton or Modules for explicit top-level modules. Config is
// copied during preflight and may be reused after Run returns.
type Config struct {
	// Identity is the service identity attached to every module attempt.
	Identity Identity

	// EnvPrefix prefixes service-owned environment variables. An empty value
	// derives NAMESPACE_NAME_ from Identity.
	EnvPrefix string

	// Logger receives structured runtime and module records. Nil discards logs.
	Logger *slog.Logger
	// TracerProvider creates attempt tracers. Nil selects a no-op provider.
	TracerProvider trace.TracerProvider
	// MeterProvider creates bounded runtime instruments. Nil selects a no-op provider.
	MeterProvider metric.MeterProvider
	// Propagator carries trace context through durable messages. Nil selects an empty propagator.
	Propagator propagation.TextMapPropagator

	// TelemetryShutdown flushes and closes telemetry owned by the caller. It
	// runs after all service-owned work has stopped.
	TelemetryShutdown func(context.Context) error

	// Setup declares an implicit singleton named for the service.
	Setup SetupFunc
	// Modules declares the service's explicit top-level modules.
	Modules []Module
	// Strategy selects the root supervisor's affected set. Zero is one-for-one.
	Strategy Strategy
	// Intensity bounds root strategy applications. Zero selects three per five minutes.
	Intensity RestartBudget
}

type runtimeConfig struct {
	identity          Identity
	envPrefix         string
	modules           []plannedModule
	registry          *staticRegistry
	admission         *admissionRevision
	rootSupervisor    normalizedSupervisor
	telemetryShutdown func(context.Context) error
}

func normalizeEnvPrefix(config Config) (string, error) {
	envPrefix := config.EnvPrefix
	if envPrefix == "" {
		envPrefix = strings.ToUpper(config.Identity.Namespace + "_" + config.Identity.Name + "_")
	}
	if !envPrefixPattern.MatchString(envPrefix) {
		return "", fmt.Errorf("service environment prefix %q is invalid", envPrefix)
	}
	if len(envPrefix) > maxIdentityLength {
		return "", fmt.Errorf("service environment prefix exceeds %d bytes", maxIdentityLength)
	}
	return envPrefix, nil
}
