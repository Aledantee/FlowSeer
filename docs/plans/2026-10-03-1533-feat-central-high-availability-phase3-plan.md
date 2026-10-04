---
title: Central High Availability, Phase 3, Central as Several Replicas - Plan
type: feat
date: 2026-10-03
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-10-03-1533-feat-central-high-availability-plan.md
---

# Central High Availability, Phase 3, Central as Several Replicas - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

Two or more device service processes serve one deployment. The means are an
election for the background modules that must run once, a way for any
replica to serve an edge's dispatch stream, and a readiness endpoint.

Stop condition: the plan is wrong if the dispatch relay holds state in
memory that a second replica cannot rebuild from the lane record.

## Decisions

The parent's decisions apply
([parent plan](2026-10-03-1533-feat-central-high-availability-plan.md)). This phase adds:

- The drift poll, the read sweeper, and the capture artifact sweeper run on
  one elected replica. Why: each dispatches or closes work, and two would do
  it twice (`src/services/device/README.md`, "Drift is central's").
- The election is a key in a JetStream key-value bucket with a time to live,
  taken with compare-and-set. Why: central already holds that connection and
  the journal's one-writer rule already rests on compare-and-set. No new
  dependency enters.
- A replica learns of a lane change it did not write through a key-value
  watch. Why: what is owed is a pure function of the lane record
  (`journal.OwedRows`), so a replica that sees the record can send the row.
- Central serves a readiness endpoint on a port no Service exposes outside
  the cluster. It reports ready once the NATS connection is up and the
  listeners are bound. Why: the central high availability record makes readiness part of
  being ready for high availability.

## Requirements

1. Background work runs once. Example: with two replicas and one managed
   interface, one drift read is dispatched per interval.
2. The elected replica's loss moves the work. Example: after the elected
   replica is killed, the other dispatches the next drift read within the
   election time to live plus one interval.
3. A mutation admitted on one replica reaches an edge whose dispatch stream
   another replica holds. Example: an `ExecuteRequest` arrives on the stream
   without the edge reconnecting.
4. Two replicas never both write one lane record's next state. Example: two
   concurrent reports for one sequence produce one accepted write and one
   compare-and-set refusal that is retried against the new record.
5. A replica without a NATS connection reports not ready. Example: the
   readiness endpoint returns 503 while the connection is down.

## Out of scope

- Scaling beyond what one NATS cluster and one region carry.
- Authorization for the readiness endpoint.

## Open questions

- Whether the relay keeps per-stream state that the lane record does not
  hold. Read `src/services/device/internal/` at re-plan. Unverified.
- Whether captured artifacts live on local disk, which replicas cannot
  share.
- Whether the telemetry forwarder may run on every replica. It consumes
  per-edge streams, and a durable consumer shared by replicas would split
  the work without an election. Unverified.
- Which module supervision order holds once the hub module is a connection
  and not a server.
