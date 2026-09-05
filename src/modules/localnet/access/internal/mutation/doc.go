// Package mutation drives one admitted operation — a mutation or a read —
// through the phases flowseer.device.access.v1.OperationPhase names: plan
// (admission-time validation), checkpoint, execute, observe, compare, and
// result. Machine is a typestate: each method is valid from exactly the
// phases the direction record's decision 4 barrier allows it from, and an
// out-of-order call is rejected rather than silently accepted.
//
// Machine holds no protocol knowledge itself. Deps supplies already-bound
// Read/Submit/Verify closures (built from the interfaces capability package
// and whatever route a caller resolved, honoring an explicit pin or not),
// so this package never imports a firmware and never re-implements route
// selection. Every phase transition and block/release is recorded as a
// flowseer.event.device.v1.DeviceOperationEvent through the injected
// audit.Deliverer, and the call that would flip Machine's own phase blocks
// on that delivery succeeding first — decision 13's audit-before-release
// rule — so a Deliverer failure leaves Phase() reporting the mutation's
// last durable value. OpenTelemetry signals go through telemetry.View and
// never gate a phase transition.
//
// See docs/architecture/2026-09-05-verified-device-access-direction.md,
// decisions 2, 4, 5, 9, and 13, and
// spec/proto/flowseer/integration/device/v1/README.md for the envelope this
// package's Checkpoint/Execute/Observe/Result methods answer.
package mutation
