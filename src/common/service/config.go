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

// SetupFunc constructs the mutable state for one module attempt. Each call
// must return a fresh Runner whose lifetime is bounded by ctx.
type SetupFunc func(ctx context.Context) (Runner, error)

// Module declares one service module. Its zero value is invalid.
type Module struct {
	// Name is the module's stable lower-snake-case path segment.
	Name string
	// Setup constructs the mutable state for each execution attempt.
	Setup SetupFunc
}

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
}

type runtimeConfig struct {
	identity          Identity
	envPrefix         string
	modules           []runtimeModule
	telemetryShutdown func(context.Context) error
}

type runtimeModule struct {
	name  string
	path  string
	setup SetupFunc
}

func normalizeConfig(config Config) (runtimeConfig, error) {
	if err := validateIdentity(config.Identity); err != nil {
		return runtimeConfig{}, err
	}

	envPrefix := config.EnvPrefix
	if envPrefix == "" {
		envPrefix = strings.ToUpper(config.Identity.Namespace + "_" + config.Identity.Name + "_")
	}
	if !envPrefixPattern.MatchString(envPrefix) {
		return runtimeConfig{}, fmt.Errorf("service environment prefix %q is invalid", envPrefix)
	}
	if len(envPrefix) > maxIdentityLength {
		return runtimeConfig{}, fmt.Errorf("service environment prefix exceeds %d bytes", maxIdentityLength)
	}

	hasImplicit := config.Setup != nil
	hasExplicit := len(config.Modules) != 0
	if hasImplicit == hasExplicit {
		return runtimeConfig{}, fmt.Errorf("service declares neither or both implicit and explicit modules")
	}

	modules := make([]runtimeModule, 0, max(1, len(config.Modules)))
	if hasImplicit {
		modules = append(modules, runtimeModule{
			name:  config.Identity.Name,
			path:  config.Identity.Name,
			setup: config.Setup,
		})
	} else {
		declarations := append([]Module(nil), config.Modules...)
		for _, module := range declarations {
			if err := validateIdentitySegment("module name", module.Name); err != nil {
				return runtimeConfig{}, err
			}
			if module.Setup == nil {
				return runtimeConfig{}, fmt.Errorf("module %q has no setup", module.Name)
			}
			modules = append(modules, runtimeModule{
				name:  module.Name,
				path:  config.Identity.Name + "/" + module.Name,
				setup: module.Setup,
			})
		}
	}

	return runtimeConfig{
		identity:          config.Identity,
		envPrefix:         envPrefix,
		modules:           modules,
		telemetryShutdown: config.TelemetryShutdown,
	}, nil
}
