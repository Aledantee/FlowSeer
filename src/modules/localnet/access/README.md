# Device access

The edge-resident runtime for one local-network integration's device
access: a bounded, ordered per-device lane that admits reads and
mutations, resolves route evidence and firmware epochs, drives the
interface capability through the mutation state machine (plan, checkpoint,
execute, observe, compare, result), recovers from ambiguity, honors a
control-plane freeze,
and reports a subset of the named OpenTelemetry signals plus the durable
`flowseer.event.device.v1.DeviceOperationEvent` audit record before
releasing the lane — see "Named events, spans, and the audit record" below
for exactly which signals are wired and which are defined but not yet
emitted. Grounded in
[the verified device access direction record](../../../../docs/architecture/2026-09-05-verified-device-access-direction.md).

## Exported surface

`Lane`, `Reporter`, `DeviceSession`, `SNMPSession` and `ShellSession`
(`lane.go`) are the exported types beyond the
capability facade functions in `access.go`. Every other type — `internal/evidence`,
`internal/epoch`, `internal/lane`, `internal/credential`,
`internal/telemetry`, `internal/freeze`, `internal/audit`,
`internal/mutation`, `internal/recovery` — stays
internal; a caller composes device access only through `Lane` or the
lower-level `access.Read*`/`access.Set*`/`access.Verify*` facade functions
for the interface capability directly.

```
NewLane(Config) *Lane
  .AddDevice(ctx, deviceKey, DeviceSession) error   // onboarding: acquires, probes, closes
  .Submit(ctx, SubmitOptions) (*ExecuteResult, error)
  .HandleCheckpoint(deviceKey, *CheckpointRequest) error
  .HandleTerminalAck(ctx, deviceKey, *TerminalResultAck) error
  .Freeze(ctx) error / .Unfreeze(ctx)
  .ResolveHold(ctx, deviceKey, *HoldResolved) error // clears a recovery hold
  .Close(ctx) (ShutdownReport, error)
```

## How the lane reaches a device

`DeviceSession` is a set of factories, not a connection. Every operation
acquires its own credential through `Config.ReadCredentials`, opens a
session from it, and closes that session when the operation ends —
onboarding's identity probe under the handle `DeviceSession.AccessPolicy`
carries, each read under the handle central put on that read's
`TypedRead.access_policy`, and a mutation's command over a shell opened from
the submission grant's own material, pinned to the host key the grant names.

A standing session would outlive the credential it was opened with, so a
credential central revoked would keep working for as long as the connection
stayed up. Acquiring per operation means the authority is checked by the act
of acquiring, every time. The cost is one acquisition per read, which is
what `AcquireReadCredential` is shaped for — there is no standing lease to
cache.

`ReadOverride` and `SubmitOverride` replace the device call itself and are
for tests. They do not replace the acquisition, which happens first
regardless: a hook able to skip it would let the rest of this package's
tests pass with the credential path switched off.

The observation's `FirmwareFingerprint` provenance is the lane's probed
value, overwriting whatever a host put in `DeviceSession.Prov` — an
observation must name the epoch it was actually taken under. The
package-level facade functions in `access.go` have no probe behind them, so
their callers set it themselves.

## What the lane tells its host, and what it asks of it

`Config.Reporter` is how everything this lane learns reaches central. The
lane calls it on the goroutine doing the work and never waits: a report is
one-way, and the host is the only party that can retry a delivery. An
implementation hands the message to its own queue and returns. A nil
`Reporter` is a no-op, and no operation ever fails because a report could
not be made — which is why the methods return nothing at all.

For one mutation a host sees a progress report at admission, the
`CheckpointAck`, the observation at `VERIFIED`, and the terminal phase; on
failure, an error report carrying `submitted`. A read is reported once with
its observation, and each caller of a coalesced read gets its own message
under its own sequence — central admitted each read separately and is owed
an answer for each.

`submitted` is the field the rest of the system turns on. False means the
command provably never left this edge, so central may dispose the mutation
`REJECTED`; true means the device may hold the change, and it must not.

## How an acknowledgement is applied

`HandleTerminalAck` decides central's `TerminalResultAck` synchronously,
against the mutation's phase as it stands, and either applies it before
returning or refuses it with nothing changed. It is not handed to the
waiting mutation to apply later: an acknowledgement stored for later has to
be re-validated every time the phase moves and re-armed on every path out of
a rest point, and central would have been told "accepted" by a call that
could not yet know whether it was.

| Disposition | Accepted at | Then |
| --- | --- | --- |
| `VERIFIED` | `VERIFIED` | `ACKNOWLEDGED`, then `RELEASED` |
| `REJECTED` | `ADMITTED`, or `POSSIBLY_APPLIED` before the command went out | `ACKNOWLEDGED`, then `RELEASED` |
| `INDETERMINATE_ABANDONED` | any open phase | `ABANDONED`, and the device's lane is held |

Three refusals, which central must tell apart: `access/no-pending-wait` (no
mutation open at that sequence), `access/already-terminal` (it ended, and
its own report is on the way), and `mutation/out-of-order` (the disposition
does not fit the phase). All three leave the mutation exactly as it was.

The `REJECTED` row is a latch, not a check. `Machine.Acknowledge` sets its
cancellation only while the command has not gone out, and `Execute` marks
the command sent only while no cancellation is set — both under the
machine's own lock, so exactly one of "central released this `REJECTED`" and
"the device was changed" can ever be true. Disposing a mutation `REJECTED`
after the command went out would record that nothing happened to a device
that was changed, which is the one outcome this lane must never produce.

An acknowledgement whose own audit delivery fails part-way returns the error
with its decision already marked, and central re-sends; the identical
acknowledgement is then accepted and finishes the same walk. The mutation
waits for that re-send rather than failing on its own, because failing would
engage a recovery hold over a mutation central has already released.

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
explicit two-attribute cap. Only `flowseer.device.operation.duration` is
recorded from production code today, around `Lane.process`;
`flowseer.device.route.selections` is defined but has no caller yet — see
"Named events, spans, and the audit record" above.

| Metric | Unit | Attributes | Allowed values | Worst-case series per device |
| --- | --- | --- | --- | --- |
| `flowseer.device.operation.duration` (histogram) | `s` | `flowseer.device.operation`, `error.type` | operation: `interface_description`, `interface_read` (closed set, grows with each new capability). `error.type`: present only on a failed operation, a bounded classified string (e.g. `mutation/out-of-order`, `mutation/revoked`, `mutation/firmware-epoch`, `mutation/conflicting-reads`, `context.deadline_exceeded`, `context.canceled`) — one series per operation class times one series per distinct failure class, plus one success series per operation class. | 2 operation classes × (1 success + ~6 known failure classes) = 14 |
| `flowseer.device.route.selections` (counter, not yet emitted) | `{selection}` | `flowseer.device.route`, `flowseer.device.outcome` | route: `snmp`, `ssh` (closed set, grows with each new protocol this module routes over). outcome: `success`, `failure` (closed, two values). | 2 routes × 2 outcomes = 4 |

Both bounds are per device; a deployment's total series count is this
table's per-device bound times the number of devices one edge serves,
which the process's `service.instance.id` Resource attribute — not a
per-metric attribute — distinguishes across edges.

## Named events, spans, and the audit record

`internal/telemetry` defines the eight `flowseer.device.*` named events
(`route.selected`, `route.fallback`, `discovery.completed`,
`firmware.epoch_changed`, `recovery.started`,
`lane.frozen`, `lane.blocked`, `lane.released`) as OpenTelemetry Events —
`otel.event.name`-tagged log records — and two spans,
`flowseer.device.operation` (INTERNAL, the bounded admission-to-result
stage) and `flowseer.device.route` (CLIENT, the actual outbound SNMP or
SSH call), meant to follow the extract-before-policy and link-not-parent
rules from
[the trace-propagation solution](../../../../docs/solutions/architecture-patterns/trace-context-relays-through-trace-disabled-modules.md)
across a recovery retry boundary. None of `internal/telemetry`'s methods
return an error or block past a bounded local call, so an exporter failure
never blocks or fails the operation it instruments — only a
`internal/audit.Deliverer` failure can, since the durable
`DeviceOperationEvent` audit record is delivered and blocks the mutation
state machine's release step, per decision 13's audit-before-release rule.

`Lane` wires the following at production call sites today:

- `discovery.completed` (telemetry event and audit record) at
  `AddDevice`.
- `recovery.started`, `lane.blocked`, `lane.released` (telemetry event and
  audit record) at their respective state-machine call sites.
- `lane.frozen` at `Freeze` as one telemetry event plus one audit record
  per registered device, and the freeze-path `lane.released` at `Unfreeze`
  as a telemetry event only. `internal/freeze.Gate` is shared across every
  device this Lane serves and has no `audit.Common.Device` to attribute a
  record to, so `Lane` emits the records itself, over the devices it knows.
  The gate is frozen before any record goes out and stays frozen whatever
  the deliveries do — a fence is called when something is already wrong,
  often the audit path itself. `Freeze` returns the joined delivery errors
  and remembers which devices were recorded, so a retry emits only what is
  missing; `Unfreeze` forgets the fence, and a device added while frozen
  gets its record at `AddDevice`, before it is registered.
- The `flowseer.device.operation` span and the
  `flowseer.device.operation.duration` metric, around `Lane.process`'s
  per-item work.

Not yet wired, defined but never called from production code: the
`route.selected`/`route.fallback` events, the
`flowseer.device.route.selections` metric, the
`flowseer.device.route` span, and `firmware.epoch_changed` (telemetry
event, audit record, and `BLOCK_REASON_FIRMWARE_EPOCH_CHANGED` alike —
see "Open gap: no mid-operation firmware-epoch re-check" below for why).
The route dimension itself is already
available: `interfaces.Read` sets the winning observation's
`Provenance.protocol` to the route that actually answered, `SelectRoute`
returns the SSH route only from its own fallback branch (so
`protocol == MANAGEMENT_PROTOCOL_SSH` from a route-independent read is
exactly the fall-through case), and `Lane.recordEvidence` already reads
that same provenance. What is missing is a signal on the SUBMIT path
(`Machine.Execute` never observes a route the way a read does) and on a
read that fails before any observation completes, plus a decision on where
the route span's boundaries sit relative to the operation span and a
recovery retry. That is a design question for a later unit, not a
call-site change these Lane.process/recordEvidence edits could have made
safely.

## Scope of `Lane.Submit`'s automatic handling

`Lane.process` (what the background drainer runs per queued item) runs the
phase-by-phase path: plan, checkpoint, execute, observe, compare, and — for
a verified mutation — the wait for central's acknowledgement and release.

A mutation whose effect it cannot establish does not fail. It enters
`RECOVERING`, the device's hold is engaged, `process` returns having
answered nobody, and a per-device poll takes the mutation over. `Submit` is
still blocked; what changes is who will unblock it.

The poll takes `ds.draining` with a blocking `Lock`, checks its context and
the lane's closed flag *after* acquiring, runs one `recovery.Runner.Attempt`,
releases, and drains. It is not a queue item: a queue item would need a
payload type, a capacity reservation and an outcome contract the drain loop
does not have, and could be refused by a full queue on the very tick that
would have abandoned. Holding the lock for the attempt alone gives the same
single active worker with none of that, and reads admitted meanwhile are
served between polls rather than waiting out the horizon.

**The release rule.** Any acquirer of `ds.draining` must, on release, drain
the queue and re-check its length. It belongs to the lock, not to any one
acquirer: a submitter whose `TryLock` loses exits immediately and never
retries, so an item admitted while another acquirer held the lock would have
no drainer at all. State it per acquirer and unconditionally — two acquirers
each draining is harmless, two each assuming the other will is a lost wakeup
that reproduces under no timing anybody arranges on purpose.

**Every exit answers the caller.** A recovering mutation can end four ways:
its observation verifies it, the horizon abandons it, central acknowledges
it, or `Lane.Close` cancels the poll. If none of those happens the poll's own
budget — the horizon plus one interval — expires and answers anyway, because
a mutation nobody is left holding is a `Submit` that never returns. Whichever
party gets there first answers exactly once; the result channel holds one
buffered send and a second would block its sender forever.

An abandonment is a *result*, not an error: the caller has to report it to
central, and central disposes the mutation from what it says. Failing the
call instead would leave a host with something to log and nothing to send.

**What recovery records.** Polls are silent. Recording each poll's
`OBSERVING` and `RECOVERING` transitions would put two records per poll into
a durable stream for the whole horizon and bury the `RecoveryStarted` and the
terminal record that actually answer "what happened to this device". A retry
is recorded, once, because resending a command to a device is a real event.

## Open gap: no mid-operation firmware-epoch re-check

Decision 7 blocks a typed mutation whose intent names a stale firmware
fingerprint, and `mutation.Admitted` enforces that once, at admission,
against `Lane`'s own probed `CurrentFingerprint`. There is deliberately no
check after that: a device that reboots into new firmware between
`CheckpointAck` and submission is not caught, because the only fingerprint
available after admission is `InterfaceObservation.Provenance.firmware_fingerprint`
— a field `interfaces.Read` copies verbatim from whatever the host's own
`ProvenanceInputs` supplied, never from a probe this module ran. Comparing
that value against `CurrentFingerprint` is not a mid-operation epoch check;
the two are unrelated inputs with no defined relationship, and on the
documented production path (a host sets `ProvenanceInputs.FirmwareFingerprint`
as the schema requires, no `FingerprintOverride`) they differ by
construction — blocking every mutation, not just a real epoch change. An
earlier version of this code did compare them; it is deliberately removed.

The hole this leaves is wider than "between checkpoint and submission,"
and it does not fail safe once a real firmware change happens:

- `epoch.Probe` runs exactly once, in `AddDevice`, and `ds.fingerprint` is
  never rewritten afterward. `CurrentFingerprint` is therefore central's
  expectation compared against a digest learned once at onboarding, not
  against the device's current firmware — the uncaught window is the
  entire life of the `deviceState`, not one operation's checkpoint-to-submission
  span.
- After a real firmware change, the admission check becomes the mirror
  image of the bug just removed: central learns the new fingerprint,
  every subsequent intent names it, `Admitted` compares it against the
  still-cached old one, and rejects every mutation for that device with
  `ErrCodeFirmwareEpoch` permanently. There is no supported refresh path
  — `Lane`'s own doc states that a second `AddDevice` for an
  already-registered key replaces its `*deviceState` wholesale, orphaning
  the queue and any in-flight drainer, and that onboarding must run
  exactly once per device, never as a way to reset or reconfigure one
  already added.
- `evidence.Store.InvalidateFingerprint` has no production caller. Route
  evidence recorded under the old firmware survives an epoch change
  undisturbed, so this plan's requirement 7 ("invalidates every
  route-evidence entry for the device... and forces `epoch.Probe` again")
  is unmet as well as unwired.

A real fix needs a fresh `epoch.Probe` run at observation time compared
against the fingerprint the earlier probe returned — probe output against
probe output, never probe output against a host-supplied provenance field
— which needs a live transport this module's synchronous `Submit` path
does not have, plus a decision on how a device's `CurrentFingerprint` gets
refreshed once that probe detects a real change (and `InvalidateFingerprint`
gets called) without requiring a disruptive re-`AddDevice`. That work is
the central-service plan's job, alongside the recovery auto-wiring the
previous section describes.
