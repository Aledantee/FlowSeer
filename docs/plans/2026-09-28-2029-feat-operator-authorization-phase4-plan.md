---
title: Operator Authorization Phase 4, Authorization Engine Interface and SpiceDB Adapter - Plan
type: feat
date: 2026-09-28
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: mixed
parent: docs/plans/2026-09-28-2029-feat-operator-authorization-plan.md
---

# Operator Authorization Phase 4, Authorization Engine Interface and SpiceDB Adapter - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

A Go interface (check, bulk check, lookup resources, write and delete
relationships) with a structured relationship type (object type, object id,
relation, subject), an in-memory fake, and a SpiceDB adapter. The SpiceDB
schema file encodes the direction record's relation table. PostgreSQL and
SpiceDB join the development and test deployment. Consistency tokens are
stored as opaque bytes on `store/` records.

## Decisions

The parent's Decisions and `docs/architecture/2026-09-28-operator-authorization-direction.md` apply. Left to this phase's re-plan:

- Where the interface lives (`src/common` or a module under
  `src/modules`) follows `src/modules/README.md`'s admission rule and is
  decided here.
- The SpiceDB client version is pinned here from the module cache or
  Context7 evidence.
- The fake and the adapter pass one shared test suite, so a check that
  passes on the fake passes on SpiceDB.

## Requirements

1. The shared suite. Acceptance: with `tenant` `t1` `operator` `sub-1` and
   `edge` `e1` `tenant` `t1` written, a check of `edge` `e1` `capture` for
   `sub-1` is allowed on both the fake and SpiceDB, and for `sub-2` it is
   denied on both.
2. No engine in the schema. Acceptance: parent Requirement 8.
