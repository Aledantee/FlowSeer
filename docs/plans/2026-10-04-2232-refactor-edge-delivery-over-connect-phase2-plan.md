---
title: Edge Delivery over Connect, Phase 2, Delivery Calls and Central Handlers - Plan
type: feat
date: 2026-10-04
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: mixed
amends: docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md
parent: docs/plans/2026-10-04-2232-refactor-edge-delivery-over-connect-plan.md
---

# Edge Delivery over Connect, Phase 2, Delivery Calls and Central Handlers - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

Central accepts ingest records and OTLP bodies from an edge through two
Connect calls and handles them as it handles the same traffic from the
per-edge streams today. The means are two schema packages under
`spec/proto/flowseer/edge/`, a handler that reuses intake's validation and
publication, and a handler that reuses the forwarder's collector post. The
leaf path keeps working beside them until the last phase.

Stop condition: the plan is wrong if the ingest handler's benchmark cannot
settle the load the device service record states.

## Decisions

The parent's decisions apply
([parent plan](2026-10-04-2232-refactor-edge-delivery-over-connect-plan.md)).
This phase adds:

- The calls are `flowseer.edge.ingest.v1.IngestService.Deliver` and
  `flowseer.edge.telemetry.v1.TelemetryService.Deliver`. Why: the audit
  call is `flowseer.edge.audit.v1.AuditService.Deliver`, one package per
  concern.
- Each response carries `settled`, the count of leading items central
  holds or refused for a reason about the item. A failure that is not about
  an item stops the count there and the call still answers. Why: intake
  already separates a refusal from a retry
  (`src/services/device/internal/intake/README.md`), and the edge needs to
  know where to resume.
- `edge/ingest` imports `integration/ingest`, and
  `test/conformance/proto/layering_test.go` admits that edge. Why: the
  call carries `IngestRecord` typed, as `edge/audit` imports
  `event/access`.
- The validation, the publication list, and the message id stay in
  `src/services/device/internal/intake`, callable from the handler and from
  the stream consumer. Why: both paths must refuse and publish alike while
  both exist.
- The collector post and its status rules move out of
  `edgebus.Forwarder.forward` into a function both the forwarder and the
  telemetry handler call. Why: the same reason.
- Both handlers mount with the other edge services behind the assertion
  middleware (`src/services/device/internal/host/serve.go`).
- The ingest handler keeps intake's refusal of a record whose provenance
  names another edge (`src/services/device/internal/intake/intake.go`,
  `reasonForeignProvenance`).

## Requirements

The parent's requirements 4, 5, and the central half of 6 are claimed here.

1. A delivered record is published once per typed stream under
   `<tenant>.<record_id>`. Example: one `IngestRecord` with a syslog payload
   and raw evidence yields one message in `FLOWSEER_INGEST_SYSLOG` and one
   in `FLOWSEER_INGEST_EVIDENCE`.
2. A record that fails validation is settled and counted as refused.
   Example: a batch of three whose second record has no provenance answers
   `settled: 3` with the other two published.
3. A publish failure stops the count. Example: with the CENTRAL stream
   unavailable at the second record, the answer is `settled: 1`.
4. A telemetry body reaches the collector unchanged. Example: the bytes
   the collector double receives equal the bytes in the request.
5. The collector's answer maps as it does today. Example: 400 settles and
   counts the body as refused, and 503 stops the count.
6. A benchmark delivers batches of 256-byte syslog records to the handler
   against an embedded hub with per-message fsync and reports records per
   second.

## Out of scope

- Any change to the agent, which the third phase makes.
- Removing the follower, the forwarder's consumers, or `AttachBus`, which
  the last phase does.

## Open questions

- The batch bounds: the largest request the assertion middleware hashes,
  and the item count the schema allows. `maxEdgeBody` in
  `src/services/device/internal/host/host.go` is 1 MiB, and the first
  phase sizes a segment at half of it. The loopback receiver accepts OTLP
  bodies up to 16 MiB (`src/modules/edgebus/receiver.go`, `maxOTLPBody`),
  so either the telemetry call gets a larger bound or the receiver a
  smaller one.
- Whether the telemetry handler answers when the deployment names no
  collector, where the forwarder module is gated off today
  (`src/services/device/internal/host/host.go`, `forwarderGate`).
- Which listener the handlers mount on depends on whether the central high
  availability plan's first phase has landed.
