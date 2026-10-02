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

The OpenFGA-specific decisions below hold if OpenFGA is chosen. Choosing
SpiceDB requires re-planning those decisions before implementation.

**Stop condition:** a second process starts serving operator RPCs before
phase 3 lands. The enforcement code then belongs in `src/common/` or
`src/modules/`, not in the device service's `internal/`, and the phase
split below is wrong.

## Decisions

The [operator authorization record](../architecture/2026-09-30-operator-authorization-direction.md)
holds the decisions that outlive this plan. The ones below either restate
that record's choices the user made or are local to the work.

- The landed foundation is the identity leaf and the tenant entity with
  tenant-partitioned stores. The identity leaf landed in
  `f1f75c2f..6f8f73d5`, and the tenant entity and partitioned stores landed in
  `2088ca38..b0eafd4b`. Why: this plan starts from the existing
  `model/identity`, `src/common/tenant`, and tenant-partitioned stores.
- A request's tenant is named per request and admitted by membership, as
  the 09-30 record decides: the `X-FlowSeer-Tenant` header names it, and
  the caller is admitted when FlowSeer has enrolled them and their token
  claims the tenant's organization. Partner admins through `partner` and
  global admins through `platform` are also admitted. Why: one token can act
  in several tenants, and a service provider's admins reach customer tenants through
  the `partner` relation, which a token-bound tenant cannot model. (decided
  by the user, 2026-09-30)
- The 09-30 record absorbs the 09-28 record, and the 09-30 parent plan
  continues. Why: the 09-30 record carries the spike evidence, the per-RPC
  rule, the list checks, and the membership model. The 09-28 parent's
  remaining phases (3 to 6) are stubs that never planned. (decided by the
  user, 2026-09-30)
- The engine is chosen after a SpiceDB spike that repeats the OpenFGA
  spike's measurements. (decided by the user, 2026-09-30) Why this spike
  and not a paper comparison: the 09-28 record chose SpiceDB for its
  consistency token from documentation, the 09-30 record chose OpenFGA from
  measurements, and the two never ran on the same workload.
  This replaces the earlier decision that named OpenFGA as the engine.
- OpenFGA is the engine, chosen after reading the SpiceDB spike beside the
  OpenFGA spike. Why: neither ranks on speed, both keep revocation exact in
  their consistent modes, and this plan's design is written for OpenFGA.
  (decided by the user, 2026-09-30)
- OpenFGA runs as its own service on a Postgres that is external from the
  first deployment, never embedded in a FlowSeer host. Why: each can move
  and scale alone. (decided by the user, 2026-09-30)
- Check caching stays off. Why: the
  [OpenFGA note](../research/2026-09-30-openfga-authorization-spike.md#cache-staleness-across-replicas)
  measured 1.509-9.019 s of stale allows across two instances on one database
  with the check cache and controller at ten-second defaults. The
  [SpiceDB note](../research/2026-09-30-spicedb-authorization-spike.md#revocation-and-cache-staleness)
  re-measured OpenFGA's gRPC union (292,238 relationships plus trial grants)
  with continuous polling and positive controls. Last allows were
  1.924-8.743 s after delete responses, which agrees with seconds of
  staleness.
- Authentication accepts any OIDC provider. The principal is issuer plus
  subject. Why: OIDC makes `sub` unique only within an issuer, and the
  project will not depend on one provider's claims. (decided by the user,
  2026-09-30)
- The request names its tenant in the `X-FlowSeer-Tenant` header. The
  interceptor admits only a `member` of the named tenant. Membership is
  FlowSeer enrollment with a token claiming the tenant's organization, or
  reach through `partner` or `platform`. A caller who is not a member of the
  named tenant is `PermissionDenied`, not `Unauthenticated`. Why: OIDC has no
  standard tenant claim, and Zitadel's organization scope rejects users whose
  access comes from a grant (zitadel#11869).
- Membership is `(claimed and enrolled) or active_admin from partner or
  admin from platform`: `enrolled` is owned by FlowSeer (invite, remove),
  `claimed` is sent per request from the token's organization claims.
  Why: exact access reviews, and removal at the identity provider takes
  effect at the next token refresh. (decided by the user, 2026-09-30)
- The landed tenant binding is the membership model's `claimed` mapping:
  a `TenantConfig` binds a tenant to an issuer, an organization claim name,
  and a value, and the `tenants` bucket's `org_` index resolves an (issuer,
  organization) pair to one tenant in one atomic batch
  (`src/services/device/internal/tenantstore/store.go`). The `claimed`
  relationships come from the token's organization claims resolved through
  the tenant binding's `org_` index, and a tenant id is never assumed equal
  to an organization id. Why: the index exists, is tested against every
  applicable read and publish fault, and is the lookup the interceptor needs
  to turn a token's organization claims into tenants.
- The membership intersection is checked once per request on the tenant.
  Resource permissions are unions without `and`, and recursive Tag
  permissions do not use `but not`. Why:
  [OpenFGA resource intersections](../research/2026-09-30-openfga-authorization-spike.md#membership-gated-by-token-claims)
  on 336,249 relationships returned 11 of 1,104 Tag edges at a 60-second
  deadline. The [OpenFGA exclusion fixture](../research/2026-09-30-openfga-authorization-spike.md#previewing-a-tag-change)
  used `but not` on recursive Tag permissions and returned zero Tag-derived
  objects after 60 seconds. The
  [SpiceDB note](../research/2026-09-30-spicedb-authorization-spike.md#membership-aware-resource-lookup)
  re-measured the direct-tenant placement on 336,245 relationships. OpenFGA
  returned all 1,104 Tag edges in 905.764 ms over gRPC, but still returned
  zero global-admin edges at 60 s. The Tag disagreement is unexplained,
  with fixture and stored-grant reference-shape differences.
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
- An issuer configured without organization claims cannot satisfy the
  claimed-member path. Why: `claimed` relationships derive from the token's
  organization claims resolved through the tenant binding, and a provider
  with no organization claim provides nothing the binding can resolve.
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
| `platform` | `admin` |
| `tenant` | `member`, `admin`, `operator`, `capturer`, `viewer`, `full_payload` |
| `edge` | `tenant`, `view`, `operate`, `capture`, `administer` |
| `device` | `tenant`, `view`, `operate` |
| `capture_session` | `tenant`, `manage`, `download` |

`platform` has one object, `platform:flowseer`, and `TenantService`'s rules
name it (`spec/proto/flowseer/api/identity/v1/tenant_service.proto`).
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
- Choosing the engine. The user decides after reading the spike. That
  decision amends the direction record and sets it accepted-direction in a
  change of its own. Caching checks is also out of scope until an initial
  deployment settles.

## Units

### U1. Rule schema, annotations, and the enforcement core

Files: `docs/plans/2026-09-30-1139-feat-operator-authorization-phase1-plan.md`
After: none
Landed: `5b8a83e6..6ebca6cb`

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

- How one tenant with several issuers maps to organization claims.
  `TenantConfig` binds one issuer, so multiple issuers for one tenant need an
  answered pattern before phase 2 writes the configuration.
- Which OIDC issuer the lab deployment runs (Zitadel, Keycloak, or Dex).
  Phase 2 decides. Any of them passes the vendor rule.
