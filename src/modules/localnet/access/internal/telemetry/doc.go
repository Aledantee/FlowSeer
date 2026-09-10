// Package telemetry centralizes every OpenTelemetry signal this module
// emits: the nine flowseer.device.* named events, the
// flowseer.device.operation and flowseer.device.route spans, and the two
// bounded metrics. Every other internal package takes a [*View] rather than
// reading a provider from a context or a process global, so a caller outside
// src/common/service can still use this module, and a caller inside it gets
// this module's own instrumentation scope rather than the service runtime's.
//
// See docs/conventions/observability.md for the naming, attribute, and
// cardinality rules this package follows, and
// docs/architecture/2026-09-05-verified-device-access-direction.md,
// "Audit and telemetry are separate", for the split telemetry.View's methods
// are the
// "telemetry" half of: none of them return an error, and none may block or
// fail the caller past a bounded local operation, even when the configured
// TracerProvider or MeterProvider is backed by a failing exporter.
package telemetry
