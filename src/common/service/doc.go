// Package service runs a service as a supervised tree of modules.
//
// A Config carries process identity and attempt-scoped logging and telemetry.
// Run never changes process-global slog or OpenTelemetry state. Module setup is
// called for each execution attempt, and every runner must stop when its context
// is canceled.
package service
