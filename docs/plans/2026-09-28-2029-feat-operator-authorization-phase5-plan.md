---
title: Operator Authorization Phase 5, Enforcement and Role Grants - Plan
type: feat
date: 2026-09-28
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: superseded
superseded_by: docs/plans/2026-09-30-1139-feat-operator-authorization-plan.md
execution: mixed
parent: docs/plans/2026-09-28-2029-feat-operator-authorization-plan.md
---

# Operator Authorization Phase 5, Enforcement and Role Grants - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

Every RPC on the three operator surfaces checks its relation from the
direction record's table and returns `PermissionDenied` on a denial.
Listing RPCs use lookup resources. Handlers check that a loaded object's
tenant matches the context's before asking the engine. Creating and
deleting an edge, a device lane, and a capture session writes and deletes
their stored relations through the record-is-the-outbox sweep. An admin
service grants and revokes tenant roles (`admin`, `operator`, `viewer`,
`full_payload`) with typed fields, and never takes a raw relationship.

## Decisions

The parent's Decisions and `docs/architecture/2026-09-28-operator-authorization-direction.md` apply. Left to this phase's re-plan:

- The admin service's package and name are decided here, under the
  network model structure record's rules for `api/` packages.
- The first tenant admin comes from the deployment configuration, as the
  record says.

## Requirements

1. Parent Requirement 5.
2. Fail closed. Acceptance: a capture session whose relationship write has
   not yet run is invisible to `ListCaptureSessions` and returns
   `PermissionDenied` on `GetCaptureSession`, until the sweep writes it.
