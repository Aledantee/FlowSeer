---
title: Operator Authorization - Plan
type: feat
date: 2026-09-30
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
---

# Operator Authorization - Plan

## Goal

Every operator and admin RPC (`DeviceService`, `EdgeAdminService`,
`CaptureService`) authenticates its caller against a configured OIDC issuer,
admits the tenant the request names only for a member of it, and authorizes
the call against OpenFGA by a rule the RPC declares in its schema. Handlers
record the authenticated principal instead of the identity a caller writes,
and every admin change lands in an operator action trail. The means: a rule
option in the schema enforced by one fail-closed interceptor, a standalone
OpenFGA on its own Postgres, and a projector that keeps OpenFGA's
relationships derived from FlowSeer's records.

**Stop condition:** a second process starts serving operator RPCs before
phase 3 lands. The enforcement code then belongs in `src/common/` or
`src/modules/`, not in the device service's `internal/`, and the phase
split below is wrong.

## Decisions

The [operator authorization record](../architecture/2026-09-30-operator-authorization-direction.md)
holds the decisions that outlive this plan. The ones below either restate
that record's choices the user made or are local to the work.

- OpenFGA is the engine, not SpiceDB. Why: SpiceDB's advantages (ZedTokens,
  cursored lookups) pay off only with cached checks or listings far past one
  page, and its audit logging is commercial. (decided by the user,
  2026-09-30)
- OpenFGA runs as its own service on a Postgres that is external from the
  first deployment, never embedded in a FlowSeer host. Why: each can move
  and scale alone. (decided by the user, 2026-09-30)
- Check caching stays off. Why: the spike measured revoked grants still
  allowed on another replica for up to 9.0 s with caching on
  ([spike](../research/2026-09-30-openfga-authorization-spike.md)).
- Authentication accepts any OIDC provider. The principal is issuer plus
  subject. Why: OIDC makes `sub` unique only within an issuer, and the
  project will not depend on one provider's claims. (decided by the user,
  2026-09-30)
- The request names its tenant in the `X-FlowSeer-Tenant` header, and the
  interceptor admits it only for a `tenant#member`. Why: OIDC has no
  standard tenant claim, and Zitadel's organization scope rejects users
  whose access comes from a grant (zitadel#11869).
- Membership is `(claimed and enrolled) or active_admin from partner or
  admin from platform`: `enrolled` is owned by FlowSeer (invite, remove),
  `claimed` is sent per request from the token's organization claims.
  Why: exact access reviews, and removal at the identity provider takes
  effect at the next token refresh. (decided by the user, 2026-09-30)
- The membership intersection is checked once per request on the tenant.
  Resource permissions are unions with no `and` and no `but not`. Why: an
  intersection inside resource permissions made `ListObjects` return 11 of
  1,104 edges after 60 s.
- All tenants share one OpenFGA store. Why: a connected service-provider
  tenant is a relationship between two tenant objects, and relationships
  cannot cross stores.
- Global admins are `platform#admin`, inherited by `tenant#admin` and not by
  `tenant#full_payload`. Why: captured payload is the most restricted data
  FlowSeer holds, so reading it stays an explicit, logged grant. (decided by
  the user, 2026-09-30)
- Each RPC declares its rule as the `flowseer.authz.v1.rule` method option,
  enforced by one fail-closed interceptor and a conformance gate. Why: a
  forgotten check becomes a denied call and a failed build. (decided by the
  user, 2026-09-30)
- FlowSeer's records own every relationship, and OpenFGA holds a projection
  a projector keeps current. Revoking removals also delete their
  relationships before the RPC returns. Why: a tenant's relationships can
  then be rebuilt for the loss preview, which `Read` on a shared store
  cannot select, and revocation never waits on the projector.
- The enforcement code lives in `src/services/device/internal/authn` and
  `internal/authz`. Why: `src/common/README.md` admits a package only when
  two unrelated trees import it, and the device service is the only host of
  operator RPCs.
- The OpenFGA model starts with `platform`, `role`, `tenant`, `edge`,
  `device`, and `capture_session`, and reserves `site` and `tag` without
  relations that grant. Why: no inventory service stores Sites or Tags yet,
  so their grants and the Tag-change preview land with it. (decided by the
  user, 2026-09-30)
- Every object check also requires the object's `tenant` to be the
  admitted tenant. Why: resource permissions carry no membership term, so a
  member of B with a grant in A could otherwise act on A while naming B.
- A provider configured without organization claims gets a `claimed` tuple
  for the tenant the request names, so its gate is `enrolled` alone. Why:
  the membership relation needs both terms, and such a provider has no
  claim to supply one.
- The work splits into four phases. Why: the schema and the enforcement
  core, the external systems, the service migration, and the tenancy admin
  surfaces touch disjoint files and each depends on the one before. The
  action trail for the admin RPCs that exist today ships in phase 3 with
  enforcement, as the direction record requires. Phase 4 extends it to the
  surfaces it adds.

The relations the rules name, fixed here so phase 1 can annotate RPCs
before phase 2 writes the model:

| Object type | Relations a rule may name |
| --- | --- |
| `tenant` | `member`, `admin`, `operator`, `capturer`, `viewer`, `full_payload` |
| `edge` | `tenant`, `view`, `operate`, `capture`, `administer` |
| `device` | `tenant`, `view`, `operate` |
| `capture_session` | `tenant`, `manage`, `download` |

`tenant` on a resource names its owning tenant. `capture_session#manage`
derives from the session's stored edge (`capture from edge`), never from
the edge a request names.

## Requirements

1. An operator RPC without a valid bearer token fails with
   `Unauthenticated`. `GetEdge` with no `Authorization` header returns
   `CodeUnauthenticated`.
2. A principal that is not a member of the tenant it names fails with
   `PermissionDenied` before any handler runs. A caller enrolled in tenant
   A who sends `X-FlowSeer-Tenant: B` gets `CodePermissionDenied` from
   `GetEdge`.
3. An RPC under `flowseer.api.` without a rule fails the conformance gate,
   and the interceptor refuses it. Adding `rpc Ping(PingRequest) returns
   (PingResponse);` to `DeviceService` makes the gate report
   `flowseer.api.device.v1.DeviceService.Ping`.
4. A handler whose rule defers the check to the handler and returns without
   calling it produces `Internal` and no response. A test handler for
   `ListCaptureSessions` that returns sessions without filtering gets
   `CodeInternal`.
5. Removal from the organization at the identity provider denies the next
   call made with a token that no longer lists it. A token whose
   organization claims omit tenant A gets `CodePermissionDenied` on
   `GetEdge` in A even while `enrolled` still holds.
6. Removal of a member in FlowSeer denies their next call and deletes their
   grants in that tenant. After `RemoveMember` for user U in tenant A,
   `GetEdge` by U in A returns `CodePermissionDenied`, and OpenFGA holds no
   relationship naming U on an object of A.
7. A global admin whose token carries the platform claim is admitted to
   every tenant but cannot read captured payload without an explicit grant.
   Such a caller gets `GetEdge` in any tenant, and
   `CreateCaptureSession` with `full_payload_requested` returns
   `CodePermissionDenied` until `tenant#full_payload` is granted.
8. An admin of a connected service-provider tenant acts in the customer
   tenant only while the customer grants it. With
   `tenant:C#capturer@tenant:M#active_admin` and `tenant:C#partner@tenant:M`,
   M's admin creates a capture session on an edge of C, and after the grant
   is deleted the same call returns `CodePermissionDenied`.
9. A filtered list returns only objects the caller may see, and a page never
   holds an object it may not see. A caller with `capture` on one of two
   edges lists only that edge's sessions.
10. Handlers record the authenticated principal. `CreateCaptureSession`
    stores `requested_by` with the token's issuer and subject whatever the
    request carries.
11. Every admin-surface change is recorded with principal, tenant, object,
    action, and time. `IssueSetupKey` appends an action-trail record naming
    the caller and the edge.
12. OpenFGA refuses unauthenticated clients in every deployment FlowSeer
    ships. A call to the lab OpenFGA without its preshared key fails.

## Out of scope

- Site and Tag grants and the Tag-change preview: they need the inventory
  service. The model reserves their types.
- Authorization of edge-facing services (`flowseer.edge.*`): edges
  authenticate with signed assertions and stay outside these rules.
- Moving FlowSeer's own records to Postgres. Postgres here is OpenFGA's
  datastore.
- Caching checks, and SpiceDB. The direction record names when to revisit.

## Units

### U1. Rule schema, annotations, and the enforcement core

Files: `docs/plans/2026-09-30-1139-feat-operator-authorization-phase1-plan.md`
After: none
Landed:

### U2. OIDC authentication, OpenFGA client, model, and deployment

Files: `docs/plans/2026-09-30-1139-feat-operator-authorization-phase2-plan.md`
After: U1
Landed:

### U3. Service migration: enforcement on, projector, stamped identity

Files: `docs/plans/2026-09-30-1139-feat-operator-authorization-phase3-plan.md`
After: U2
Landed:

### U4. Tenancy admin surfaces and the operator action trail

Files: `docs/plans/2026-09-30-1139-feat-operator-authorization-phase4-plan.md`
After: U3
Landed:

Waves: U1 | U2 | U3 | U4

## Verification

Each phase plan names its commands. After U4, the lab runbook in
`deploy/lab/README.md` brings up Postgres, OpenFGA, and an OIDC issuer, and
requirements 1, 2, 5, 6, 7, and 8 are exercised against the running device
service.

## Definition of done

- [ ] Every phase's `Landed:` line filled and its plan `implemented`.
- [ ] Verifier green on every changed path of every phase.
- [ ] The direction record accepted by a person, or amended where the work
      proved it wrong.
- [ ] `src/services/device/README.md` no longer says the operator API has
      no authorization.
- [ ] No plan labels in code.

## Open questions

- Which OIDC issuer the lab deployment runs (Zitadel, Keycloak, or Dex).
  Phase 2 decides. Any of them passes the vendor rule.
- How a provider's organization identifiers map to FlowSeer tenant ids
  when one tenant has several issuers. Phase 2 decides the configuration
  shape. One issuer with identical ids is the starting case.
