---
title: ClickHouse History Store - Plan
type: feat
date: 2026-10-02
artifact_contract: flowseer-plan/v2
execution: mixed
---

# ClickHouse History Store - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

A sink module consumes the central syslog stream and inserts each record into
ClickHouse, acknowledging the stream only after the insert is durable, and an
operator query reads a device's records back by time range.

## Decisions

The parent plan's Decisions apply.

- The sink acknowledges a message after ClickHouse acknowledges the insert
  with `wait_for_async_insert=1`. Why: ClickHouse then answers only after
  the flush to disk (<https://clickhouse.com/docs/optimize/asynchronous-inserts>).
- The ClickHouse client enters under the
  [dependency admission record](../architecture/2026-10-01-dependency-admission-direction.md).

## Requirements

1. A record in the central stream is queryable after the sink acknowledges
   it. Example: the parent plan's requirement 4.
2. A redelivered record is stored once.
3. One tagged integration test sends a datagram to an agent and reads the
   row from ClickHouse.

## Open questions

- Table layout, ordering key, and partitioning for `SyslogRecord`.
- Tenant isolation: a tenant column with row policies, or a database per
  tenant. Unverified which holds against an API-built query.
- Query limits that keep an API query from stalling inserts. Unverified.
- Which client library, and its version past the 14-day wait.
- Where table migrations live and what runs them.
- Whether the operator query API lands here or in a later plan.
