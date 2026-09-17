// Package mutation drives one admitted operation — a mutation or a read —
// through the phases flowseer.model.access.v1.OperationPhase names: plan
// (admission-time validation), checkpoint, execute, observe, compare, and
// result. Machine is a typestate: each method is valid from exactly the
// phases the checkpoint barrier allows it from, and an
// out-of-order call is rejected rather than silently accepted.
//
// Machine holds no protocol knowledge itself. Deps supplies already-bound
// Read/Submit/Verify closures (built from the interfaces capability package
// and whatever route a caller resolved, honoring an explicit pin or not),
// so this package never imports a firmware and never re-implements route
// selection. Every phase transition and block/release is recorded as a
// flowseer.event.device.v1.DeviceOperationEvent through the injected
// audit.Deliverer, and the call that would flip Machine's own phase blocks
// on that delivery succeeding first — the audit record is written before
// the state it describes is published — so a Deliverer failure leaves Phase() reporting the mutation's
// last durable value. OpenTelemetry signals go through telemetry.View and
// never gate a phase transition.
//
// See docs/architecture/2026-09-05-verified-device-access-direction.md for
// the boundaries this package keeps, and
// spec/proto/flowseer/edge/dispatch/v1/README.md for the envelope its
// Checkpoint/Execute/Observe/Result methods answer.
package mutation
