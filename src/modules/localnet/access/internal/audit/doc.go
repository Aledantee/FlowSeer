// Package audit builds and delivers the durable
// flowseer.event.device.v1.DeviceOperationEvent audit record. Deliverer is
// the seam this module's mutation state machine calls before releasing the
// phase an event describes, per decision 13's audit-before-release rule:
// audit and telemetry are separate, and only a Deliverer failure may block
// or fail the operation it records.
//
// See docs/architecture/2026-09-05-verified-device-access-direction.md,
// decision 13.
package audit
