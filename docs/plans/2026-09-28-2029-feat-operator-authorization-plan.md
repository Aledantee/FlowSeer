---
title: Operator Authorization - Plan
type: feat
date: 2026-09-28
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
amends: docs/architecture/2026-08-20-device-service-and-inventory-direction.md
---

# Operator Authorization - Plan

## Goal

Every call to `DeviceService`, `EdgeAdminService`, and `CaptureService`
carries an OIDC token. The server resolves the caller's tenant from it,
checks the caller's permission in a Zanzibar-style engine (SpiceDB first),
and records every change and every capture download in a per-tenant
operator event stream. This works for more than one tenant from the first
deployment. The means is six phases:
- an identity leaf package;
- a tenant entity with tenant-partitioned stores;
- token authentication with ambient identity;
- an engine interface with a SpiceDB adapter;
- enforcement in the three services;
- the operator event stream.

Stop condition: this plan is wrong if an amendment to
`docs/architecture/2026-09-28-operator-authorization-direction.md`
(accepted 2026-09-28) changes what a phase builds; that phase is then
re-planned.

## Decisions

The direction record carries the decisions that outlive this plan: OIDC
bearer tokens and ambient identity, the tenant entity and its binding to an
issuer and organization, tenant-partitioned stores, the Zanzibar model and
its first relation table, SpiceDB behind an interface, a schema that names
no engine, role grants in place of raw relationship writes, the
record-is-the-outbox relationship writes, and the operator event stream.
The user decided on 2026-09-28: the Zanzibar model, SpiceDB, identity from
the token, and multi-tenancy from the first deployment.

These are the plan's own:

- Six phases, in this order. Why: each needs what the one before it lands.
  Authentication needs a tenant to resolve a token to. Enforcement needs
  both the authenticated subject and the engine. The event stream needs
  the authenticated subject. The engine phase depends on nothing landed:
  its interface and SpiceDB schema take ids as values, so it runs beside
  phase 1.
- The capture authorization plan becomes phase 1. Why: its identity leaf is
  the first thing every later phase imports, and its prose units are the
  record amendments this change needs first. Its capture requester field
  moves to phase 3, where the server can fill it.
- Only phase 1 is implementation-ready. Why: later phases depend on library
  choices (OIDC verifier, SpiceDB client version) and on a tree that phase 1
  and 2 will have moved. Each is re-planned when its turn comes.

## Requirements

Each phase claims the requirements named in its unit.

1. `OperatorRef` and the tenant entity live in `flowseer.model.identity.v1`.
   Acceptance: `grep -rn 'message OperatorRef\|message Tenant' spec/proto`
   prints only files under `model/identity/v1/`.
2. A tenant resolves from a token's issuer and organization claim.
   Acceptance: with a tenant bound to issuer `https://idp.example` and
   organization `org-1`, a token with those claims reaches a handler whose
   context holds that tenant's id. A token with organization `org-2` fails
   with `PermissionDenied`.
3. An operator call without a valid token fails with `Unauthenticated`.
   Acceptance: `GetEdge` with no `Authorization` header, or with a token
   signed by a key the issuer does not publish, returns `Unauthenticated`,
   and the handler never runs.
4. Central's stores are partitioned by tenant. Acceptance: an edge created
   by a tenant A caller is stored under a key that begins with A's id, and
   `GetEdge` for that id from a tenant B caller returns `NotFound`.
5. Every operator RPC checks a relation from the direction record's table.
   Acceptance: an operator holding only `tenant`/`viewer` who calls
   `CreateCaptureSession` gets `PermissionDenied`, and one holding
   `tenant`/`operator` succeeds. A session created by operator X with
   `full_payload_requested: true` is refused unless X holds
   `tenant`/`full_payload`.
6. No request message carries the caller's identity. Acceptance:
   `AbandonMutationRequest`, `ResolveDesynchronizationRequest`, and
   `CreateCaptureSessionRequest` have no actor or requester field. The
   stored mutation record and capture session name the token's subject.
7. Capture downloads and every changing operator call emit an operator
   event. Acceptance: a completed `DownloadCaptureSession` publishes one
   event on the tenant's operator stream, naming the subject, the session,
   and the bytes served.
8. The schema names no engine. Acceptance:
   `grep -rniE 'openfga|spicedb|keto|zed' spec/proto` prints nothing.

## Out of scope

- Choosing or operating an identity provider. Any OIDC issuer works.
- Hosting the tenant's identity provider organization inside FlowSeer.
- The web app's login flow.
- Authorization for the edge-facing services, which keep their signed edge
  assertions.

## Units

### U1. Identity leaf and records

Files: `docs/plans/2026-09-28-2029-feat-operator-authorization-phase1-plan.md`
After: none
Landed:

Requirements 1 (`OperatorRef` part) and 8.

### U2. Tenant entity and partitioned stores

Files: `docs/plans/2026-09-28-2029-feat-operator-authorization-phase2-plan.md`
After: U1
Landed:

Requirements 1 (tenant part) and 4.

### U3. Token authentication and ambient identity

Files: `docs/plans/2026-09-28-2029-feat-operator-authorization-phase3-plan.md`
After: U2
Landed:

Requirements 2, 3, and 6.

### U4. Authorization engine interface and SpiceDB adapter

Files: `docs/plans/2026-09-28-2029-feat-operator-authorization-phase4-plan.md`
After: none
Landed:

No requirement of its own; it makes phase 5's checks possible.

### U5. Enforcement and role grants

Files: `docs/plans/2026-09-28-2029-feat-operator-authorization-phase5-plan.md`
After: U3, U4
Landed:

Requirement 5.

### U6. Operator event stream

Files: `docs/plans/2026-09-28-2029-feat-operator-authorization-phase6-plan.md`
After: U3
Landed:

Requirement 7.

Waves: U1 U4 | U2 | U3 | U5 U6

## Verification

Each phase lists its own. The whole change is proven by running phase 5's
and phase 6's acceptance tests against a central with SpiceDB and
PostgreSQL, and a test OIDC issuer holding two tenants' organizations.

## Definition of done

- [ ] Every phase plan reads `implemented`, and its `Landed:` line here carries its range.
- [ ] The direction record carries a dated amendment for anything that landed differently.
- [ ] `docs/conventions/protobuf.md` has lost the `ENTITY_TYPE_TENANT` exception.
- [ ] This plan's `status` set with an outcome note under its title.

## Open questions

None. The user accepted the direction record on 2026-09-28.
