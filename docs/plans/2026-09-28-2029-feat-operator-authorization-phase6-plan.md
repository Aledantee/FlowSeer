---
title: Operator Authorization Phase 6, Operator Event Stream - Plan
type: feat
date: 2026-09-28
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: superseded
superseded_by: docs/plans/2026-09-30-1139-feat-operator-authorization-plan.md
execution: mixed
parent: docs/plans/2026-09-28-2029-feat-operator-authorization-plan.md
---

# Operator Authorization Phase 6, Operator Event Stream - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

Every changing call on the three operator surfaces, and every completed
capture download, publishes an operator event naming the authenticated
subject, the tenant, the object, the action, and its outcome, on a
per-tenant stream separate from the device audit stream.

## Decisions

The parent's Decisions and `docs/architecture/2026-09-28-operator-authorization-direction.md` apply. Left to this phase's re-plan:

- The event's package follows the network model structure record's rules
  for `event/` packages and is decided here.
- The capture direction record's "a download is itself an event" and the
  device README's missing operator trail are closed here, and both texts
  are amended in this phase.

## Requirements

1. Parent Requirement 7.
2. Setup keys. Acceptance: `IssueSetupKey` by subject `sub-1` publishes an
   event naming `sub-1`, the edge, and the action.
