// Package access is the edge-resident device-access runtime: it reads and
// mutates one device family over SNMP or SSH, orders every read, probe,
// mutation, and recovery step for one device through that device's own
// lane, and reports the result as a
// flowseer.model.access.v1.InterfaceObservation. [Lane] is the package's
// only exported surface beyond the capability facade functions below it;
// every other type lives under internal/ so a caller never depends on this
// module's own composition of them.
//
// Each capability lives under access/internal/capability in its own
// directory (interfaces/ for the interface capability), pairing a
// protocol-agnostic handler with one shell adapter per firmware as a
// sibling directory (capability/fastiron/ is the first) — kept internal so
// a firmware adapter's types never leak past this package's facade.
//
// [Lane] composes the rest of internal/: evidence and epoch (route
// evidence and the firmware-epoch identity probe), lane (per-device
// admission, priority, and poll coalescing), credential (the EdgeService
// RPCs this module consumes), telemetry (every OpenTelemetry signal),
// freeze (control-plane freeze), audit (the durable
// flowseer.event.device.v1.DeviceOperationEvent record), mutation (the
// phase-by-phase state machine), and recovery (the ambiguity
// handling). See
// docs/architecture/2026-09-05-verified-device-access-direction.md for the
// rules this module implements, and this package's README for the metric
// cardinality table and the onboarding sequence.
package access
