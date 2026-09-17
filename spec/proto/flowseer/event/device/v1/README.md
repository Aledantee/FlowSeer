# Device operation audit event

The `flowseer.event.device.v1` package holds `DeviceOperationEvent`, the
durable audit record of what happened on one device's lane. Decision 13 of
the
[verified device access record](../../../../../../docs/architecture/2026-09-05-verified-device-access-direction.md)
draws the line this package sits on: this event answers *what happened* and
must be delivered before the state it records is released, while
OpenTelemetry events, spans, and metrics answer *why* and may fail without
blocking work. This package carries only the former.

## One record, nine kinds

Every event carries the same envelope — the device, a unique event id, the
lane sequence it concerns (unset for a lane-level event with no single
mutation in scope), when it occurred, bounded correlation ids, and bounded
attributes — plus exactly one `detail` kind:

| Kind | Answers |
| --- | --- |
| `PhaseTransitioned` | A mutation moved from one `OperationPhase` to another. |
| `LaneBlocked` | The lane stopped admitting the next mutation, and why. |
| `LaneReleased` | The lane is free again. |
| `RouteSelected` | Which protocol answered, and whether it was reached after an incomplete read fell through. |
| `DiscoveryCompleted` | Identity and capability discovery finished, and the fingerprint it learned. |
| `FirmwareEpochChanged` | The fingerprint changed, invalidating prior route and capability evidence. |
| `RecoveryStarted` | Recovery began for a mutation whose effect could not be established. |
| `DriftDetected` | A managed field changed with no mutation to explain it. |
| `LaneFrozen` | The lane paused because the hosting edge's contact could not be confirmed. |

A separate kind per fact keeps every event row typed instead of a single
struct wide enough for the union of all nine. `LaneBlocked` carries the same
`BlockReason` enum `MutationState` uses, so a reader never has to reconcile
two vocabularies for the same concept.

## Delivery

`AuditService.Deliver` is how a record reaches the stream: the edge (or
central's own drift detector) sends one `DeviceOperationEvent` per call over
Connect, central writes it into the JetStream audit stream with the event
id as the deduplication key, and answers only after the stream has
acknowledged it. Central is the stream's only writer, and an error means the
record is not held, so a caller that must not release state before its
record is durable simply does not proceed on an error. Central derives
nothing from the stream afterwards; the fingerprint and every other fact it
acts on arrive through `integration/device/v1`'s reports.

## Why this package imports model/inventory directly

Unlike the execution envelope in `integration/device/v1`, which never
restates a device or edge ref because every message travels over an
already-addressed channel, an audit record is read and queried outside any
live transport context — a compliance report, an incident timeline. It must
name its device on its own, so this package imports
`flowseer/model/inventory/v1/device.proto` for `DeviceGlobalRef` in addition to
`device/access` and `errs`. It never imports `api/edge` directly; the
[verified device access record](../../../../../../docs/architecture/2026-09-05-verified-device-access-direction.md)
states that an event envelope reaches `api/edge` only through
`device/access`, where `MutationState.responsible_edge` already names it
when a mutation is in scope.

## What is deliberately absent

- `DeviceOperationConfig` and `DeviceOperationState`. This package is a pure
  event stream; there is nothing here to configure and nothing to query as
  current state. `MutationState` in `device/access` is the live state this
  audit trails.
- A secret, a credential, or a transcript. `attributes` is bounded and
  client-owned facts only — a field name, a fingerprint, a protocol — never
  raw device output.
- A protocol path or raw command. `RouteSelected` names which protocol
  answered, never how it was spoken to the device.
- A tenant. Scope is ambient.
