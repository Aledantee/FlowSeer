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

// Runner performs one module attempt. A nil return is a normal lifecycle
// outcome; a non-nil error is governed by the module's error [Policy]. Context
// cancellation takes precedence over an error returned after ctx is canceled.
// A Runner must return after cancellation. It may be called only once and need
// not be safe for concurrent use.
type Runner func(ctx context.Context) error

// Attempt contains the fresh runner and canonical handlers constructed by one
// Setup call. Its values are owned by that attempt and need not be safe for
// concurrent use, except that handlers may run concurrently when the leaf
// declares parallel delivery.
type Attempt struct {
	// Runner performs the module's non-message work.
	Runner Runner
	// Handlers must exactly match the leaf's static subscriptions.
	Handlers []Handler
}

// SetupFunc constructs the mutable state for one module attempt. A non-nil
// error selects the module's error [Policy] unless ctx was canceled. Each call
// must return a fresh Attempt whose lifetime is bounded by ctx. A SetupFunc
// reused by sibling modules must be safe for concurrent calls.
type SetupFunc func(ctx context.Context) (Attempt, error)

// BusFsyncPolicy selects how the local bus flushes file-backed stream writes.
// Its zero value is BusFsyncUnspecified, which fails normalization; a service
// must declare one of the other values. Values are safe to copy.
type BusFsyncPolicy uint8

const (
	// BusFsyncUnspecified is the zero value. A BusConfig carrying it is rejected
	// before the store is locked or opened.
	BusFsyncUnspecified BusFsyncPolicy = iota
	// BusFsyncPeriodic asks the operating system to flush pending stream writes
	// on a target interval. Acknowledged records survive a process kill. The
	// interval is not a guaranteed loss bound: scheduler delay and slow storage
	// can complete a sync later than the interval, so a power loss can drop
	// records older than the interval.
	BusFsyncPeriodic
	// BusFsyncPerMessage flushes each stream write before acknowledging it.
	// Acknowledged records survive a process kill and a power loss.
	BusFsyncPerMessage
)

// BusConfig opts a service into its durable local message bus. FsyncPolicy
// must be set; every other zero-valued field uses the documented private store
// location and logical capacity defaults. StoreDir, when set, must be
// absolute. Callers must use keyed literals and must not mutate a BusConfig
// while a Run using it is active. When MaxStoreBytes changes, callers must set
// the component limits too if the fixed defaults do not fit the new ceiling.
type BusConfig struct {
	// StoreDir is the private file-store directory. Empty selects the service
	// state directory; a non-empty value must be absolute.
	StoreDir string
	// MaxStoreBytes is the total logical store ceiling. Zero selects 1 GiB.
	MaxStoreBytes int64
	// MailboxMaxBytes is the mailbox stream ceiling. Zero selects 768 MiB. A
	// custom MaxStoreBytes does not rescale this default.
	MailboxMaxBytes int64
	// MetadataMaxBytes is the control-record stream ceiling. Zero selects 64 MiB.
	// A custom MaxStoreBytes does not rescale this default.
	MetadataMaxBytes int64
	// ReserveBytes is capacity held outside the owned streams. Zero selects
	// 192 MiB. A custom MaxStoreBytes does not rescale this default.
	ReserveBytes int64
	// StartupTimeout bounds broker startup and readiness. Zero selects 10 seconds.
	StartupTimeout time.Duration
	// HealthInterval controls broker and stream health probes. Zero selects 30 seconds.
	HealthInterval time.Duration
	// FsyncPolicy selects periodic or per-message flushing. Must be set:
	// BusFsyncPeriodic survives a process kill, BusFsyncPerMessage also survives
	// power loss.
	FsyncPolicy BusFsyncPolicy
	// FsyncInterval sets the target periodic-sync interval. Nil selects the
	// five-second default; a non-nil value must be at least one millisecond and
	// requires BusFsyncPeriodic.
	FsyncInterval *time.Duration
}

// Config declares one service run. Callers must choose either Setup for an
// implicit singleton or Modules for explicit top-level modules. Run copies the
// declaration before starting modules. Callers must use keyed literals and
// must not mutate Config or any
// referenced declaration slices until Run returns. A Config may be reused by
// concurrent runs only when its callbacks are safe for concurrent calls.
type Config struct {
	// Identity is the service identity attached to every module attempt.
	Identity Identity

	// EnvPrefix prefixes service-owned environment variables. An empty value
	// derives NAMESPACE_NAME_ from Identity.
	EnvPrefix string

	// Logger receives structured local runtime and module records. Nil selects a
	// service-owned structured stderr logger.
	Logger *slog.Logger
	// LogHandler exports caller-owned OpenTelemetry log records. Nil leaves log
	// export unavailable unless Telemetry configures a managed endpoint.
	LogHandler slog.Handler
	// TracerProvider creates caller-owned attempt tracers. Nil selects a managed
	// provider when Telemetry has an endpoint and otherwise leaves traces unavailable.
	TracerProvider trace.TracerProvider
	// MeterProvider creates caller-owned runtime instruments. Nil selects a managed
	// provider when Telemetry has an endpoint and otherwise leaves metrics unavailable.
	MeterProvider metric.MeterProvider
	// Propagator carries trace context through durable messages. Nil selects a
	// run-scoped W3C Trace Context propagator.
	Propagator propagation.TextMapPropagator
	// Telemetry configures the managed OTLP connection and root signal policy.
	// Its zero value uses common OTLP environment settings and otherwise keeps
	// managed export off.
	Telemetry TelemetryConfig

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

// runtimeConfig is the validated runtime plan assembled before service startup.
type runtimeConfig struct {
	identity       Identity
	envPrefix      string
	modules        []plannedModule
	registry       *staticRegistry
	admission      *admissionRevision
	bus            *normalizedBusConfig
	rootSupervisor normalizedSupervisor
	telemetry      normalizedTelemetryConfig
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
