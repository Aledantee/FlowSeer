---
title: Central Intake - Plan
type: feat
date: 2026-10-02
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: mixed
parent: docs/plans/2026-10-02-2331-feat-central-ingestion-pipeline-plan.md
---

# Central Intake - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

A module in the central host follows every edge's hub stream on the `ingest`
branch, validates each `IngestRecord`, and republishes it into the CENTRAL
account under the tenant the edge belongs to, one stream per record type.
The means: a durable consumer per edge stream, as the OTLP forwarder has for
`otel` (`src/modules/edgebus/forwarder.go`).

## Decisions

The parent plan's Decisions apply.

- Intake takes the tenant and edge from the stream it reads, never from the
  subject or payload. Why: one stream per edge in its own account is what
  keeps a record's edge honest (`src/modules/edgebus/README.md`).
- Intake strips raw evidence from the typed stream and writes it to an
  evidence stream with short retention. Why: the direction record keeps
  device payload out of long-lived stores.
- This phase amends the Events bullet of the device service record, which
  names `events.<tenant>.<integration>.>`.

## Requirements

1. A valid envelope in an edge stream appears once in the CENTRAL stream of
   its record type. Example: a redelivered envelope with the same record id
   is not stored twice.
2. An envelope whose subject is outside the edge's subtree is refused and
   counted. Example: the parent plan's requirement 3.
3. An envelope that fails validation is refused, counted by reason, and
   acknowledged, so it cannot block the stream.

## Open questions

- Central subject shape and stream names per record type.
- Where intake lives: `src/services/device/internal/` or `src/modules/`.
- How a centrally hosted adapter reaches intake's input.
- The evidence stream's retention.
