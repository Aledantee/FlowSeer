# Device access

The edge-resident runtime for one local-network integration's device
access: a bounded, ordered per-device lane that admits reads and
mutations, resolves route evidence and firmware epochs, drives the
interface capability through the mutation state machine (plan, checkpoint,
execute, observe, compare, result), recovers from ambiguity, detects and
resolves drift under both management modes, honors a control-plane freeze,
and reports every named OpenTelemetry signal plus the durable
`flowseer.event.device.v1.DeviceOperationEvent` audit record before
releasing the lane. Grounded in
[the verified device access direction record](../../../../docs/architecture/2026-09-05-verified-device-access-direction.md).

## Exported surface

`Lane` (`lane.go`) is the only exported type beyond the capability facade
functions in `access.go`. Every other type — `internal/evidence`,
`internal/epoch`, `internal/lane`, `internal/credential`,
`internal/telemetry`, `internal/freeze`, `internal/audit`,
`internal/mutation`, `internal/recovery`, `internal/drift` — stays
internal; a caller composes device access only through `Lane` or the
lower-level `access.Read*`/`access.Set*`/`access.Verify*` facade functions
for the interface capability directly.

```
NewLane(Config) *Lane
  .AddDevice(ctx, deviceKey, DeviceSession) error   // onboarding: runs the identity probe
  .Submit(ctx, SubmitOptions) (*ExecuteResult, error)
  .HandleCheckpoint(deviceKey, *CheckpointRequest) error
  .HandleTerminalAck(deviceKey, *TerminalResultAck) error
  .Freeze(ctx) / .Unfreeze(ctx)
  .EvaluateDrift(ctx, deviceKey, observed, inFlight) (drift.Outcome, error)
  .ResolveHold(deviceKey) error
  .Close(ctx) (ShutdownReport, error)
```

## Lane position vs. central sequence

`ExecuteRequest.sequence` is central's: assigned before dispatch and
delivered to the edge on the envelope (`spec/proto/flowseer/integration/device/v1/README.md`).
This module's own `internal/lane.Item.Position` is a separate, edge-local
counter assigned at admission into one device's `lane.Queue` — it orders
FIFO dispatch and poll coalescing before dispatch, has no relation to
`sequence`, and never appears on the wire. Priority (`lane.Priority`)
compares only among items still waiting when a slot opens; once an item is
dequeued, its position is fixed and priority never reorders it again, per
the direction record's decision 3.

## Onboarding sequence

1. A host calls `Lane.AddDevice`, supplying a `DeviceSession` (an SNMP
   session, an optional shell adapter, and provenance inputs).
2. `AddDevice` runs `internal/epoch.Probe` — a route-independent SNMP
   `sysDescr`/`sysObjectID` read — to learn the device's starting firmware
   fingerprint, records `flowseer.device.discovery.completed`, and
   delivers a `DiscoveryCompleted` audit event.
3. The device is registered with an empty per-device `lane.Queue`. The
   first `Lane.Submit` for that device may now be admitted; a mutation
   whose intent names a different firmware fingerprint than the one
   `AddDevice` learned is blocked at `mutation.Admitted` before any device
   contact, per decision 7.

## Metric cardinality

Both metrics are defined in `internal/telemetry/metrics.go`. Every
attribute is namespaced `flowseer.device.*` except the one standard key,
`error.type`; neither metric ever carries more than two attributes at
once, per the observability convention's cardinality rule and the task's
explicit two-attribute cap.

| Metric | Unit | Attributes | Allowed values | Worst-case series per device |
| --- | --- | --- | --- | --- |
| `flowseer.device.operation.duration` (histogram) | `s` | `flowseer.device.operation`, `error.type` | operation: `interface_description`, `interface_read` (closed set, grows with each new capability). `error.type`: present only on a failed operation, a bounded classified string (e.g. `mutation/out-of-order`, `mutation/revoked`, `mutation/firmware-epoch`, `mutation/conflicting-reads`, `context.deadline_exceeded`, `context.canceled`) — one series per operation class times one series per distinct failure class, plus one success series per operation class. | 2 operation classes × (1 success + ~6 known failure classes) = 14 |
| `flowseer.device.route.selections` (counter) | `{selection}` | `flowseer.device.route`, `flowseer.device.outcome` | route: `snmp`, `ssh` (closed set, grows with each new protocol this module routes over). outcome: `success`, `failure` (closed, two values). | 2 routes × 2 outcomes = 4 |

Both bounds are per device; a deployment's total series count is this
table's per-device bound times the number of devices one edge serves,
which the process's `service.instance.id` Resource attribute — not a
per-metric attribute — distinguishes across edges.

## Named events, spans, and the audit record

The nine `flowseer.device.*` named events (`route.selected`,
`route.fallback`, `discovery.completed`, `firmware.epoch_changed`,
`recovery.started`, `drift.detected`, `lane.frozen`, `lane.blocked`,
`lane.released`) are OpenTelemetry Events — `otel.event.name`-tagged log
records — emitted by `internal/telemetry`. The two spans,
`flowseer.device.operation` (INTERNAL, the bounded admission-to-result
stage) and `flowseer.device.route` (CLIENT, the actual outbound SNMP or
SSH call), follow the extract-before-policy and link-not-parent rules from
[the trace-propagation solution](../../../../docs/solutions/architecture-patterns/trace-context-relays-through-trace-disabled-modules.md)
across a recovery retry boundary. None of `internal/telemetry`'s methods
return an error or block past a bounded local call, so an exporter failure
never blocks or fails the operation it instruments — only a
`internal/audit.Deliverer` failure can, since the durable
`DeviceOperationEvent` audit record is delivered and blocks the mutation
state machine's release step, per decision 13's audit-before-release rule.

## Scope of `Lane.Submit`'s automatic handling

`Lane.process` (the drain loop `Submit` calls into) runs the ordinary
phase-by-phase path only: plan, checkpoint, execute, observe, compare, and
— for a verified mutation — acknowledge and release. An ambiguous or
failed step (an execute error, a non-`VERIFIED` disposition, a conflicting
read) is reported as `Submit`'s own error rather than automatically
retried. Automatic recovery (`internal/recovery`) and drift resolution
(`internal/drift`) are proven directly by their own package tests;
wiring their retry loop into this synchronous drain needs a real
clock-driven poll only a host with a live transport can run — the edge
host that assembles this module through `src/common/service` and drives
that poll is a later plan's job, per
[the direction record](../../../../docs/architecture/2026-09-05-verified-device-access-direction.md)'s
own sequencing.
