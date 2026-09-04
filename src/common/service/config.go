package service

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

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

// BusConfig opts a service into its durable local message bus. The zero value
// uses the documented private store location and logical capacity defaults.
// StoreDir, when set, must be absolute.
type BusConfig struct {
	// StoreDir is the private file-store directory. Empty selects the service
	// state directory; a non-empty value must be absolute.
	StoreDir string
	// MaxStoreBytes is the total logical store ceiling. Zero selects 1 GiB.
	MaxStoreBytes int64
	// MailboxMaxBytes is the mailbox stream ceiling. Zero selects 75 percent of
	// MaxStoreBytes and must leave room for metadata and reserve capacity.
	MailboxMaxBytes int64
	// MetadataMaxBytes is the control-record stream ceiling. Zero selects 10
	// percent of MaxStoreBytes and must be smaller than the total ceiling.
	MetadataMaxBytes int64
	// ReserveBytes is capacity held outside the owned streams. Zero selects 15
	// percent of MaxStoreBytes.
	ReserveBytes int64
	// StartupTimeout bounds broker startup and readiness. Zero selects 10 seconds.
	StartupTimeout time.Duration
	// HealthInterval controls broker and stream health probes. Zero selects 5 seconds.
	HealthInterval time.Duration
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
	// Bus opts into a private file-backed message bus. Nil disables it without
	// resolving a store path or starting broker work.
	Bus *BusConfig

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
	bus               *normalizedBusConfig
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
