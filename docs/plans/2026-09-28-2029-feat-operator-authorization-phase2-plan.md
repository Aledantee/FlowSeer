---
title: Operator Authorization Phase 2, Tenant Entity and Partitioned Stores - Plan
type: feat
date: 2026-09-28
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: mixed
parent: docs/plans/2026-09-28-2029-feat-operator-authorization-plan.md
---

# Operator Authorization Phase 2, Tenant Entity and Partitioned Stores - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

A tenant is a UUID-identified entity in `flowseer.model.identity.v1` with
a ref pair and the Config/State/Event triad. Its Config binds it to an
identity provider organization (issuer URL, organization claim name and
value). Central keeps tenants in a store of its own. Every key in the
`device-lanes`, `edges`, and `captures` buckets, and every capture artifact
directory, starts with the tenant id. The edgebus tenant token is the
tenant id instead of `DefaultTenant`, and central's `AuditStream` (and
any other central-account stream) subscribes with a wildcard in the tenant
position instead of `DefaultTenant`. A platform admin, named in the
deployment's configuration, creates tenants.

## Decisions

The parent's Decisions and `docs/architecture/2026-09-28-operator-authorization-direction.md` apply. Left to this phase's re-plan:

- The tenant entity replaces `model/inventory/v1/tenant.proto`: both its
  `TenantRef` and `Tenant` messages go, and nothing imports either.
- Until phase 3 lands, handlers read the tenant from a context value that
  test and development hosts set from configuration. Phase 3 replaces that
  source with the token.

## Requirements

1. The tenant entity. Acceptance: a `TenantConfig` with an issuer and an
   organization claim validates, and one without an issuer fails.
2. Partitioned keys. Acceptance: parent Requirement 4, with the tenant set
   from configuration instead of a token.
3. Every tenant's audit reaches central. Acceptance: with tenants A and B
   configured, a `DeviceOperationEvent` published on each tenant's audit
   subject is stored in central's `AuditStream`.
