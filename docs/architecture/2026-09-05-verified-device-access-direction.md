---
title: Verified Device Access - Direction
type: direction
date: 2026-09-05
topic: verified-device-access
status: accepted-direction
amends: docs/architecture/2026-08-20-device-service-and-inventory-direction.md, docs/architecture/2026-08-20-network-model-structure-direction.md
---

# Verified Device Access - Direction

How a typed change reaches one device through the local-network integration
and how FlowSeer knows it happened. The accepted device-service record fixes
the planes, the entities, and NATS as the fabric; it leaves the execute path
itself at "every write carries an idempotency key" and an open question on
credential delivery. This record settles the rest so that the schema, the
edge module, and the central service can be planned one at a time and still
meet in the middle. It was worked out in a planning session on 2026-09-05
against the tree at that date; the decisions the user directed are marked.

## Context

The tree has the SNMP collection stack (`src/modules/localnet`), NETCONF and
RESTCONF clients, the Edge enrollment contract (`api/edge/v1`), and the
inventory entities. It has no SSH shell client, no edge host wired to
`src/common/service`, no central device service, and no error wire encoding
(`docs/architecture/2026-09-04-error-wire-design-direction.md` says the
transport lands with its first consumer). The lab fixture is one Ruckus
ICX7150-24P on FastIron 10.0.10g. The first mutation FlowSeer will make is an
interface description on that switch.

## Decision

1. **Route to the integration, choose the protocol locally.** Central resolves
   `DeviceRef` to Binding, Integration, and the Edge that hosts it, then
   dispatches one typed operation. The edge picks SNMP or SSH per operation
   from live evidence keyed by device, exact firmware fingerprint, and
   operation. SNMP is the cold-start prior and the tie-breaker; an SNMP read
   that is valid but incomplete for the operation falls through to a complete
   route and the caller gets one result with one provenance. User-directed.
2. **Every write has an independent semantic verification.** A write
   capability is advertised only with a read that observes the affected state
   over a fresh session and compares it with the intent. An idempotency key
   deduplicates a retry. Only the observation proves what the device applied.
   User-directed.
3. **One ordered lane per device at the edge.** Reads, probes, mutations,
   verification, and recovery for one device share one FIFO; passive traps and
   syslog stay outside it. Priority applies at admission and never reorders
   after a sequence is assigned. User-directed.
4. **The central journal is the authority and the barrier.** A mutation is
   recorded centrally before any side effect, checkpointed as
   `POSSIBLY_APPLIED` before the command is submitted, and closed only by a
   durable terminal disposition central acknowledges back to the edge. The
   next mutation on that device waits for that acknowledgement. An edge
   journal, if one ever exists, is a cache. User-approved.
5. **Ambiguity stays indeterminate.** A lost connection after submission does
   not fail the mutation; it enters recovery, which observes before it
   retries and retries only after a device-native fence or repeated fresh
   observations across the declared delayed-apply horizon show the state
   unchanged. An authorized cancellation or a qualified timeout abandons it.
   Abandoned work stays abandoned; the recovery hold that follows is resolved
   by an operator or a reconciliation intent. User-directed.
6. **Two management modes.** `OPERATOR_MANAGED` blocks the lane on an
   unexplained managed-field change until an operator accepts, restores, or
   replaces the intent. `AUTHORITATIVE` disposes interrupted work and queues
   an ordinary reconciliation intent. Both hold central expected state.
   User-directed.
7. **Firmware epoch gates writes.** Route and capability evidence is valid
   only in the epoch it was learned; an unknown or changed fingerprint blocks
   typed mutation and forces discovery without resetting the lane sequence.
8. **Positive fencing before failover.** A successor edge may write only after
   a device, network, session, or host fence proves the predecessor cannot.
   Lease expiry alone is insufficient, because a paused process resumes
   after its time check. Until that exists, a deployment runs one edge per
   local-network integration and loss of it pauses work. User-approved. The
   Pacemaker documentation states the same rule: assuming an uncommunicative
   node is down lets "multiple instances of a resource" start
   ([Pacemaker Explained, Fencing](https://clusterlabs.org/projects/pacemaker/doc/3.0/Pacemaker_Explained/html/fencing.html)).
9. **Credentials and write authority never ride the bus.** Device credentials
   reach the edge over authenticated Connect calls on the existing Edge
   identity: a read credential for reads and preflight, and a one-use
   submission grant, bound to Edge, Integration, Device, and sequence, that
   opens only after the checkpoint and carries authority pulses the edge
   checks before each command. The Connect heartbeat stays the Edge's contact
   authority; NATS announce carries Integration availability, capabilities,
   and health. This answers the accepted record's open question on credential
   handling: delivery is per operation rather than a standing lease.
10. **Legacy management protocols are off.** SSH requires a governed host key
    and SNMP requires v3 `authPriv`. Telnet and weaker SNMP need an auditable,
    time-bounded per-device exception and are never learned as a fallback.
11. **Capabilities are typed all the way down.** Protocol packages under
    `src/protocol` stay domain-free; the SNMP and shell mappings for a
    capability live in `src/modules/localnet` beside the capability handler.
    The first shell adapter is a typed Go adapter for one firmware with
    transcript fixtures. A shell DSL waits for a second firmware.
12. **Boundary packages.** The network-model record left boundary names open
    until the first boundary schema. They are:

    ```
    spec/proto/flowseer/
      device/policy/v1/     opaque handles: access policy, credential, host trust; imports nothing
      device/credential/v1/ the typed credential material a handle resolves to; imports nothing
      device/access/v1/     operation values shared by every boundary: phase, disposition,
                            typed intents and observations
      api/device/v1/        DeviceService (Connect), the operator-facing typed API
      integration/device/v1/ execution envelopes between central and an integration, and
                            the DispatchService that carries them
      event/device/v1/      the durable DeviceOperationEvent audit record and the
                            AuditService that delivers it
      errs/v1/              the error wire payload the error-wire record describes
      store/device/v1/      the device service's own storage records; imports
                            api/inventory, api/edge, device/access, device/policy,
                            device/credential, errs, and net/*, and is imported by none
    ```

    `api/inventory` imports `device/policy` and `api/edge`; `api/edge`
    imports `device/policy` and `device/credential` (added 2026-09-06, when
    the credential responses gained typed material); `device/access`
    imports `api/inventory`, `api/edge`, `device/policy`, and `net/*`;
    `api/device`, `integration/device`, and `event/device` each import
    `device/access`, and none of them imports another. Only
    `integration/device` also imports `errs`. The event envelope therefore
    reaches `api/edge` only through `device/access`,
    which amends the network-model record's sentence that it never does.
    Provenance stays the one message in `api/inventory`, extended with the
    answering protocol, the Edge, and the firmware fingerprint.
    `flowseer.service.v1` stays private to the process-local bus.
13. **Audit and telemetry are separate.** The durable operation event answers
    what happened and must be delivered before the state it records is
    released; OpenTelemetry events, spans, and bounded metrics explain why and
    may fail without blocking work.

## Alternatives

- Central protocol routing. Rejected: whether SSH is worth its session cost
  depends on evidence only the edge has.
- Idempotency markers as the write guarantee. Rejected: a replayed result
  proves the command was accepted, not what the device did.
- Terminal failure on connection loss. Rejected: the device may have applied
  the command; failing it invites a duplicate.
- Global or central-only ordering. Rejected: device effects share one local
  fault boundary and central cannot observe the device between commands.
- Asynchronous result telemetry instead of an acknowledgement barrier.
  Rejected: the next mutation needs a durable disposition, not a best-effort
  signal.
- Lease-expiry fencing. Rejected: see decision 8.
- A generic shell DSL first. Rejected: one firmware cannot show which
  variation is worth abstracting.

## Consequences

- The accepted device-service record changes where it says a binding names a
  protocol as its route, where it lists credential delivery as open, and
  where it treats the idempotency key as the write guarantee. The
  network-model record's package tree and import graph gain the boundary
  packages above. Both edits land with the contracts plan.
- Slice 1 is one edge per integration, one field per intent, SNMPv3 and SSH
  only, and a FastIron interface description as the proving mutation.
  Multi-member failover, multi-field intents, Telnet, and further vendors are
  later plans that build on these rules and may strengthen verification but
  never replace it.
- Every write path needs measured delayed-apply and recovery bounds from the
  real fixture before it is enabled; an absent bound blocks mutation.

## Amendments

### 2026-09-17 — the boundary packages moved to model/

Decision 12's tree and import list describe the boundary packages as they
stood on 2026-09-05. `device/policy`, `device/credential`, and
`api/inventory` now read `model/policy`, `model/credential`, and
`model/inventory`; `device/access` now reads `model/access`; the Edge ref
and lifecycle that `api/edge` carried split out to `model/edge`, and
`api/edge` keeps only `EdgeAdminService`. `api/device` and `store/device`
keep their names; where this decision says one of them imports a `device/`
or `api/inventory` package, that import now names the matching `model/`
package. `integration/device` now reads `edge/dispatch`; `event/device`
split, its `AuditService` into `edge/audit` and its `DeviceOperationEvent`
into `event/access`. `api/edge`'s `EdgeService` reads `edge/attach` while
`EdgeAdminService` stays. Decision 12's process-local bus package now reads
`flowseer.runtime.v1`. See [the
network model structure
record](2026-08-20-network-model-structure-direction.md#the-package-tree)
for the tree and import graph as they stand.
