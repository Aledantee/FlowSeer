---
title: Central Ingestion Pipeline - Plan
type: feat
date: 2026-10-02
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
---

# Central Ingestion Pipeline - Plan

## Goal

A syslog datagram a device sends to an edge becomes a typed record an
operator can query from central, and a state-bearing record becomes current
state the API can list and filter. The means: one ingest envelope, an adapter
at the edge that maps before it publishes, a central intake that republishes
per record type, and three stores with one role each.

**Stop condition:** a second ingestion source needs kind-specific code in
central to be usable. The schema-conformance decision is then wrong and the
[direction record](../architecture/2026-10-02-central-ingestion-pipeline-direction.md)
is re-opened before more phases land.

## Decisions

The direction record holds the decisions that outlive this plan. The ones
below restate the choices made for it or are local to the work.

- Integrations conform to FlowSeer-owned schema and central registers
  consumers per record type, never per integration kind. Why: rule 10 of the
  [device service record](../architecture/2026-08-20-device-service-and-inventory-direction.md)
  keeps routing blind to the kind, and that record rejects in-process
  plugins.
- The adapter maps at its host and drops raw bytes at the edge. Raw bytes
  ride the envelope only on a parse failure or inside an operator-opened raw
  window. Why: carrying raw to central doubles buffer and link traffic.
  (decided by the user, 2026-10-02)
- A parse failure keeps raw bytes up to a bound per source, then samples.
  Why: the failure that prompts debugging must arrive with evidence, and a
  device flooding malformed input must not cost unbounded traffic. (decided
  by the user, 2026-10-02)
- The central planes are Service Modules in the one host that runs the hub.
  Why: the device service record keeps a single-node hub until a deployment
  plan exists. (decided by the user, 2026-10-02)
- ClickHouse holds append-only records and numeric time-series. Why: one
  Apache-2.0 store for both, already operated. (decided by the user,
  2026-10-02)
- JetStream KV is the source of truth for current state. Why: stream
  processors need latest state on the bus they hold, and existing state is
  compare-and-set in KV. (decided by the user, 2026-10-02)
- Postgres serves API lists and filters as a read model derived from KV.
  Why: KV indexes by key only. (decided by the user, 2026-10-02)
- No ClickHouse materialized view serves latest state. Why: earlier
  operation of that pattern had problems. The tree holds no evidence for
  them. (decided by the user, 2026-10-02)
- Syslog is the first source. Why: `src/protocol/syslog` and `SyslogRecord`
  exist, so it tests the envelope and stages with the least new code.
- The work is five phases along its dependency clusters: schema with the edge
  source, intake, the history store, the raw window, and state with its read
  model.

## Requirements

1. An edge that hosts a device publishes each syslog message from it as an
   `IngestRecord` on its `ingest.syslog` subject. Example: a UDP datagram
   `<34>1 2026-10-02T10:00:00Z sw1 app - - - link down` from a hosted
   device's address yields one envelope in `FLOWSEER_EDGE_<edge-id>` whose
   payload is a valid `SyslogRecord` naming that device, with no raw
   evidence. (Phase 1)
2. A message the parser cannot complete carries its raw bytes until the
   bound, and sampled raw bytes past it. Example: with a bound of 20 per
   minute and a sample of 1 in 100, 220 malformed datagrams in one minute
   yield 220 envelopes, of which the first 20 and 2 later ones carry raw
   evidence, each of those 2 reporting 99 suppressed. (Phase 1)
3. Intake republishes a valid envelope from an edge stream into the CENTRAL
   stream of its record type under the tenant the edge belongs to, and
   refuses one whose subject is outside that edge's subtree. Example: an
   envelope stored in edge A's stream under edge B's subject is counted as
   refused and reaches no central stream. (Phase 2)
4. A syslog record in the central stream is stored in ClickHouse and
   acknowledged only after the insert is durable. Example: with ClickHouse
   stopped, published records stay pending on the consumer, and all of them
   are queryable after it restarts. (Phase 3)
5. An operator opens a raw window for a device with an expiry, and the edge
   attaches raw bytes to that device's records until the expiry by its own
   clock. Example: a window of 60 seconds with the close call lost yields raw
   evidence for 60 seconds and none after. (Phase 4)
6. A state-bearing record updates current state in KV, and the API lists it
   from Postgres. Example: after the Postgres table is dropped and the
   projector restarts, the same list query returns the same rows. (Phase 5)

## Out of scope

- Traps, webhooks, and poller sources. Each is a later source on the same
  envelope.
- Third-party integration kinds and descriptor-advertised record types.
- Alerting, alarm derivation from syslog, and correlation.
- Splitting the central host into separate services.
- Syslog from an address no hosted device answers on. Phase 1 counts and
  drops it. Turning it into a discovery sighting is discovery-plane work.
- ClickHouse and Postgres deployment, backup, and high availability.

## Follow-ups

Work the edge syslog source phase left for a later plan. Each needs a plan
of its own, and none blocks a phase below.

- Validation of `ListDevicesResponse`, with a uniqueness rule on device ids
  in `spec/proto/flowseer/edge/attach/v1/device.proto` and a check in the
  agent's client path. Nothing validates a listing before its ids and
  bindings enter the index, and an invalid id or binding from central makes
  each datagram from that address fail envelope validation and end `Run`
  (`src/edge/agent/internal/syslogsource/source.go`). Central sends one row
  per id today: `ListDevices` builds a row for each id the registry returns
  (`src/services/device/internal/edgeapi/service.go`), and the registry
  keys devices by id (`src/services/device/internal/registry/registry.go`,
  `spec/proto/flowseer/store/device/v1/registry.proto`).
- Vendor parse options against real device output. The source passes the
  parser no option beyond `CaptureRaw`
  (`src/edge/agent/internal/syslogsource/source.go`), so well-formed vendor
  output parses partial and draws on the raw budget: a leading Cisco counter
  (`src/protocol/syslog/legacy.go`), a zone token after the clock
  (`src/protocol/syslog/timestamp.go`), and a Cisco tag with components
  (`src/protocol/syslog/vendor.go`).

## Units

### U1. Ingest envelope and edge syslog source

Files: docs/plans/2026-10-02-2331-feat-central-ingestion-pipeline-phase1-plan.md
After: none
Landed: `341f4cd3..646d080c`

### U2. Central intake

Files: docs/plans/2026-10-02-2331-feat-central-ingestion-pipeline-phase2-plan.md
After: U1
Landed:

### U3. ClickHouse history store

Files: docs/plans/2026-10-02-2331-feat-central-ingestion-pipeline-phase3-plan.md
After: U2
Landed:

### U4. Raw window

Files: docs/plans/2026-10-02-2331-feat-central-ingestion-pipeline-phase4-plan.md
After: U1
Landed:

### U5. Current state and the Postgres read model

Files: docs/plans/2026-10-02-2331-feat-central-ingestion-pipeline-phase5-plan.md
After: U2
Landed:

Waves: U1 | U2 U4 | U3 U5

## Verification

Each phase plan names its own commands. The whole change is proven by one
tagged integration test that sends a datagram to an agent and reads the row
back from ClickHouse, added in phase 3.

## Definition of done

- [ ] Every phase plan reads `implemented` and its `Landed:` line above holds
      a commit range.
- [ ] The direction record is accepted, and the device service record's
      Events bullet is amended in the phase that lands intake.
- [ ] `GOALS.md` names the ingestion pipeline and its stores with the
      direction record.
- [ ] This plan's `status` is `implemented` with an outcome note under its
      title.

## Open questions

- Tenant isolation inside ClickHouse and Postgres. Phases 3 and 5 decide it
  for their store.
- Which state-bearing record phase 5 projects first. The candidate is device
  and interface State from the `localnet` collector.
- Retention per record type and for the evidence stream.
