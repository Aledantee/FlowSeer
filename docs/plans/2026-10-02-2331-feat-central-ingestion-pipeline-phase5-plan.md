---
title: Current State and the Postgres Read Model - Plan
type: feat
date: 2026-10-02
artifact_contract: flowseer-plan/v2
execution: mixed
---

# Current State and the Postgres Read Model - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

A state-bearing record updates current state in a JetStream KV bucket, and a
projector derives Postgres tables from that bucket so the API can list and
filter.

## Decisions

The parent plan's Decisions apply.

- KV is written with compare-and-set and is the only store a decision reads.
- The projector follows the bucket's stream with a durable consumer, and a
  dropped table is rebuilt by replay. Why: the read model must be disposable.
- A response that follows a mutation reads KV, never Postgres.
- The Postgres driver enters under the
  [dependency admission record](../architecture/2026-10-01-dependency-admission-direction.md).

## Requirements

1. A state record in the central stream updates its KV key, and an older
   record never overwrites a newer one.
2. The Postgres table matches the bucket after the projector catches up.
   Example: the parent plan's requirement 6.
3. Every table carries the tenant, and no query runs without it.

## Open questions

- Which state the phase projects first. The candidate is device and
  interface State from the `localnet` collector.
- Key layout in the bucket, and what orders two records for one key.
- Table migrations, the driver, and its version past the 14-day wait.
- Whether FlowSeer shares the Postgres instance the authorization engine
  uses or runs its own.
