---
title: Operator Authorization Phase 3, Token Authentication and Ambient Identity - Plan
type: feat
date: 2026-09-28
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: mixed
parent: docs/plans/2026-09-28-2029-feat-operator-authorization-plan.md
---

# Operator Authorization Phase 3, Token Authentication and Ambient Identity - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

A Connect interceptor on the three operator surfaces verifies an OIDC
bearer token against the configured issuers and resolves the tenant bound
to its issuer and organization claim. It puts the subject and tenant in the
request context. The `actor` fields leave `AbandonMutationRequest` and
`ResolveDesynchronizationRequest`, and the server writes the actor from the
context. `CaptureAuthorization.operator` is replaced by a server-written
`requested_by` `OperatorRef` on the stored session config, with field 1 and
the name `operator` reserved. The operator surfaces get the request-body
limit the edge-facing handlers have.

## Decisions

The parent's Decisions and `docs/architecture/2026-09-28-operator-authorization-direction.md` apply. Left to this phase's re-plan:

- The OIDC verification library is chosen here, from the module cache or
  Context7 evidence, against the vendor rule.
- A test issuer (an in-process key pair serving a JWKS) backs every test,
  so no test depends on a real identity provider.

## Requirements

1. Parent Requirements 2, 3, and 6.
2. The capture requester. Acceptance: a session created with a token for
   subject `sub-1` is stored with `requested_by.subject` `sub-1`, whatever
   the request body contains.
