---
title: Operator Authorization - Direction
type: direction
date: 2026-09-30
topic: operator-authorization
status: accepted-direction
---

# Operator Authorization - Direction

`DeviceService`, `EdgeAdminService`, and `CaptureService` accept any caller
that reaches the API port. Nothing authenticates the caller, and a capture's
`requested_by` is whatever the caller writes about itself
(`src/services/device/README.md`, "Deployment"). `GOALS.md` names a
Zanzibar-style relationship engine for these surfaces and links this record.
The [capture record](2026-09-09-remote-packet-capture-direction.md#every-capture-is-bounded-and-authorized)
lists the relations capture needs and defers the model to this record.

This record describes how a caller becomes a principal and a tenant, how every
RPC is authorized, and who owns the relationships. Its engine is OpenFGA, chosen after a person read both measured spikes. The
measurements are in the
[OpenFGA note](../research/2026-09-30-openfga-authorization-spike.md) and the
[SpiceDB note](../research/2026-09-30-spicedb-authorization-spike.md).

This record takes over operator authorization from the
`2026-09-28-operator-authorization-direction.md` record. The earlier record
remains useful as history, including the identity and tenancy foundation that
landed under it. The user accepted this record on 2026-09-30.

## A request, end to end

```mermaid
sequenceDiagram
    participant C as Operator client
    participant A as authn interceptor
    participant Z as authz interceptor
    participant H as Handler
    participant F as authorization engine
    C->>A: bearer token, X-FlowSeer-Tenant header
    A->>A: verify token against the configured OIDC issuer
    A->>Z: principal = issuer + subject, org claims, acting tenant
    Z->>F: Check tenant#member with token-derived claim context
    F-->>Z: allowed or denied
    Z->>Z: read the RPC's authorization rule option
    alt rule names the object in the request
        Z->>F: Check relation on that object
        Z->>H: call when allowed
    else object known only after a load
        Z->>H: call with an obligation in the context
        H->>F: Check after loading the record
        H-->>Z: response
        Z->>Z: drop the response if no check ran
    end
```

## Decisions

### OpenFGA is the engine

The SpiceDB note measures both engines on one generated fixture and host.
Its dimensions match the OpenFGA note, but that note's individual fixture
list is absent. Grant distribution and stored-grant reference shape differ.
The OpenFGA note's client language is unknown. Neither note ranks the engines on latency or
throughput, and both keep revocation exact in their consistent modes. OpenFGA
was chosen: the rest of this record is written for its contextual tuples,
shared store, and check rules, and its consistency holds with the check cache
off or `HIGHER_CONSISTENCY`. SpiceDB would keep revocation exact with caching
on through ZedTokens. The triggers below say when that justifies a switch.
(decided by the user, 2026-09-30)

#### Why OpenFGA

OpenFGA is Apache-2.0, self-hostable, and a CNCF incubating project, which
satisfies the rule that infrastructure must be open and run in the EU under
our control.

The case for OpenFGA is that SpiceDB's advantages matter only under load
FlowSeer does not have: ZedTokens pay off when checks must be cached and
still see revocations at once, and cursored `LookupResources` pays off when
filtered listings run far past a page. Audit logging and Materialize sit in
SpiceDB's commercial builds. Under this argument, SpiceDB is worth revisiting
when any of these holds:

- authorization moves onto a hot path (per event, per assistant fan-out)
  where uncached checks cost too much,
- reads go to Postgres replicas or several regions while caching is on,
- a filtered listing must return far more than 1000 objects and cannot be
  paged against our own records,
- another system needs a push stream of access changes.

All tenants share one store. A store is OpenFGA's isolation boundary for
models and relationships, and a relationship cannot cross stores. Connected
tenants (a service provider's admins operating in a customer tenant) are a
relationship between two tenant objects, so they need one store.

Place OpenFGA close to its Postgres: one check makes several datastore round
trips and only one client round trip, and the spike measured the standalone
container faster than an embedded server that crossed a port forward to its
database.

Check caching stays off initially, so every read is strongly consistent.
The [OpenFGA note](../research/2026-09-30-openfga-authorization-spike.md#cache-staleness-across-replicas)
measured 1.509-9.019 s of stale allows across two instances on one datastore,
with the check cache and cache controller enabled at ten-second defaults.
The [SpiceDB note](../research/2026-09-30-spicedb-authorization-spike.md#revocation-and-cache-staleness)
re-measured OpenFGA over gRPC on its paired model with 292,238 relationships
plus temporary grant probes. Continuous polling and positive grant controls observed stale
allows, with last allows 1.924-8.743 s after the delete response. These
seconds-long windows agree. If caching is turned on later, calls that hand
out full payload, device credentials, or admin grants pass
`HIGHER_CONSISTENCY`. That preference skips the cache and reads the primary
even when a secondary datastore is configured
(`pkg/storage/postgres/postgres.go`, `getPgxPool`, OpenFGA v1.21.0).

The [OpenFGA membership note](../research/2026-09-30-openfga-authorization-spike.md#membership-gated-by-token-claims)
measured `and member from tenant` inside resource permissions on 336,249
relationships. At a 60-second deadline, it returned 11 of 1,104
Tag edges and zero platform-admin edges. The
[SpiceDB note's direct-tenant re-measurement](../research/2026-09-30-spicedb-authorization-spike.md#membership-aware-resource-lookup)
uses the paired model with 336,245 relationships and the same deadline. OpenFGA returned all 1,104
Tag edges in 905.764 ms, but zero platform-admin edges at 60 s. The Tag
lookup disagreement is unexplained. The fixtures and stored-grant reference
shape differ. On the 292,238-relationship union, the OpenFGA note measured
9 ms for Tag and 148 ms for admin. The SpiceDB note re-measured OpenFGA on the
paired model at
12.960 ms and 82.658 ms over gRPC. Because `ListObjects` caps results at
`listObjectsMaxResults` (1000 by default) and can return partial results at
its deadline without an indicator
([openfga/openfga#2828](https://github.com/openfga/openfga/issues/2828)),
lists check per parent against FlowSeer records rather than depending on that
endpoint. See the
[OpenFGA spike](../research/2026-09-30-openfga-authorization-spike.md) for the
measurements and configuration.

#### SpiceDB, the measured alternative

SpiceDB is Apache-2.0
([LICENSE](https://github.com/authzed/spicedb/blob/main/LICENSE)), a single Go
binary, and self-hosted on PostgreSQL
([datastores](https://authzed.com/docs/spicedb/concepts/datastores)).

The case for SpiceDB rests on four properties:

- **The tuple shape.** Its relationship API is already structured fields:
  `Relationship{resource: ObjectReference{object_type, object_id}, relation,
  subject: SubjectReference{object, optional_relation}}`
  ([authzed/api `core.proto`](https://raw.githubusercontent.com/authzed/api/main/authzed/api/v1/core.proto)).
- **Consistency.** It ships Zanzibar's consistency token as an opaque
  ZedToken ([ZedTokens](https://authzed.com/docs/spicedb/concepts/zedtokens)).
  A freshness-sensitive check uses `at_least_as_fresh` with the revoking
  write's ZedToken, or `fully_consistent` against the primary, to avoid
  serving a download from pre-revocation state. `minimize_latency` can
  select old state and allows the new-enemy problem
  ([consistency](https://authzed.com/docs/spicedb/concepts/consistency)).
- **Engine features.** SpiceDB provides caveats
  ([caveats](https://authzed.com/docs/spicedb/concepts/caveats)) and Watch.
  OpenFGA provides [conditions](https://openfga.dev/docs/modeling/conditions)
  and a polling [ReadChanges API](https://openfga.dev/docs/interacting/read-tuple-changes)
  for stored tuple changes. FlowSeer supplies tenant isolation with the
  tenant relation, key prefix, and handler check.
- **The vendor rule.** It is open source and self-hostable in our own
  environment without external cloud dependencies.

The [SpiceDB spike](../research/2026-09-30-spicedb-authorization-spike.md)
measures v1.56.2 on PostgreSQL 17 beside OpenFGA v1.21.0 over gRPC. Its tables
cover repeated point and batch latency (`fully_consistent` and
`minimize_latency`), throughput, cursor-paged resource lookup with last-page
timing, and continuously polled revocation with positive controls for all
three SpiceDB consistency modes. It also measures disposable rebuild/apply/diff,
and caveat-gated tenant and resource checks and lookup. It reports overlapping
spreads and unexplained contradictions of the OpenFGA note.

### A separate engine deployment next to its own Postgres

OpenFGA runs as its own service, never embedded in a FlowSeer
host, backed by a Postgres datastore external from the first deployment. The
engine and its datastore can then move or scale without redeploying a FlowSeer
host. OpenFGA starts with `Authn.Method: none` and plaintext gRPC by default.
Each deployment enables authentication and TLS before it serves FlowSeer
and runs its schema migration as its own job.

### Consistency is explicit at the adapter boundary

OpenFGA's check caching stays off initially. If caching is
enabled later, calls that hand out full payload, device credentials, or admin
grants use its higher-consistency option. Should SpiceDB replace it, the adapter
chooses among `minimize_latency`, `at_least_as_fresh` with the relevant
ZedToken, and `fully_consistent` for the same policy points. The
engine-specific defaults and measurements stay in the two research notes.

### The service boundary names no engine

FlowSeer reaches OpenFGA through a Go interface for checks, bulk
checks, resource lookup, and relationship writes and deletes. An in-memory
fake implements the same interface for tests. The model configuration belongs
to the service adapter. Nothing under `spec/proto/` names an engine, stores an
engine schema fragment, or exposes an engine token format.

The wire contract carries structured object, relation, and subject fields. A
consistency token that must survive a restart is opaque bytes owned by the
adapter. Changing engines therefore changes the adapter and its configuration,
not the protobuf contract.

### Tenants, as landed

The tenant is a UUID-identified entity in `flowseer.model.identity.v1`, beside
`OperatorRef`, with `TenantLocalRef` and `TenantGlobalRef` plus the Config,
State, and Event triad
([tenant.proto](../../spec/proto/flowseer/model/identity/v1/tenant.proto)).
Its Config binds an issuer, an organization claim name, and an organization
value.

The tenant store resolves an issuer and organization via `LookupByOrg` through
its `org_` index
(`src/services/device/internal/tenantstore/store.go:280`). `Create` stores a
tenant's record and its organization index together in one atomic batch on the
`tenants` bucket (`:113`, `:189`), so one organization binds at most one tenant
and a record without its index cannot exist (`:265`). Untested so far: a
physical file-store failure between the batch's stores, clustered
wrong-sequence responses, malformed records, and retry exhaustion.

Tenant ids are canonical lowercase UUIDs for tenant entities, in store keys,
bus subjects, and capture paths. `src/common/tenant/tenant.go` also accepts
`DefaultTenant = "default"`, and operator calls run as `default` when
`dev_tenant` is unset (`src/services/device/README.md:181`). The bus carries the
id in tenant subjects and central audit streams use a wildcard in the tenant
position ([edgebus/subjects.go](../../src/modules/edgebus/subjects.go),
[edgebus/hub.go](../../src/modules/edgebus/hub.go)).

Central's stores are partitioned by tenant: every key in the `device-lanes`,
`edges`, and `captures` buckets, and every key a later store adds, starts with
the tenant id. Lookup indexes are the exception because they resolve an
identifier before the tenant is known: `edge_<edge_id>` and `setupkey_<key_id>`
in `edges`, and `org_<hash>` in `tenants`
(`src/services/device/internal/edgestore/store.go:46-48`,
`orgIndexPrefix` and `OrgIndexKey` in
`src/services/device/internal/tenantstore/store.go`).
Stored bytes on disk, such as capture artifacts, live under
`<StateDir>/captures/<tenant_id>/`
(`Store.artifactPath`, `filepath.Join(s.capturesDir, tenantID)`, in
`src/services/device/internal/captureapi/store.go`).
A handler reads the tenant from the context and never from the request, so a
request cannot reach another tenant's keys.

An edge's tenant has one authority, its `edge_<edge_id>` index in `edges`
(`src/services/device/internal/edgestore/store.go:180`).
A device lane is keyed by its hosting edge's tenant
(`src/services/device/internal/journal/journal.go:100`,
`src/services/device/internal/host/serve.go:245`), and a
caller of another tenant gets `NotFound`
(`src/services/device/internal/deviceapi/service.go:157`,
`src/services/device/internal/deviceapi/errors.go:27`).
Nothing substitutes a default tenant for one it could not resolve
(`src/services/device/internal/host/host.go:265`).

The platform admin configuration names its issuer, organization claim name and
value, and subject. The host validates those fields before it serves the
operator APIs
(`parseConfig`, `protovalidate.Validate`, in
`src/services/device/internal/host/config.go`, and `PlatformAdmin` in
`spec/proto/flowseer/store/device/v1/service_config.proto`). The identity tenant
entity replaces the unused keyless inventory tenant shape, so
the tenant store is the source of existence and organization ownership.

`TenantService` is defined but not served until callers are authenticated.
Tests and development create tenants through the tenant store, so an
unauthenticated caller cannot create a tenant or claim an organization. State
written before the tenant change is not read: unprefixed keys, capture files
directly under `<StateDir>/captures/`, and edge accounts without a persisted
tenant are not migration inputs.

### Any OIDC provider, and a tenant the request names

Authentication accepts tokens from a configured OIDC issuer: discovery,
JWKS, and the `iss`, `aud`, and `exp` checks. Nothing depends on one
provider. The principal is the issuer and the subject together, because
OIDC makes `sub` unique only within one issuer.

OIDC has no standard tenant claim. A request names the tenant it acts in in
a header, and the authorization interceptor admits only a `member` of the
named tenant. Membership is FlowSeer enrollment with a token claiming the
tenant's configured organization, or reach through `partner` or `platform`.
The tenant binding resolves the token's issuer and organization claim to a
FlowSeer tenant id. A caller who is not a member of the named tenant is
`PermissionDenied`, not `Unauthenticated`. Selecting the tenant through a
provider's organization scope was rejected because providers spell it
differently, and Zitadel's `urn:zitadel:iam:org:id` scope rejects users
whose access comes from a grant
([zitadel#11869](https://github.com/zitadel/zitadel/issues/11869)). Tenancy
stays ambient inside FlowSeer: the admitted tenant rides the request context,
never a payload field.

### Membership: owned by FlowSeer, confirmed by the token

The initial OpenFGA shape is:

```
type tenant
  relations
    define platform: [platform]
    define partner: [tenant]
    define claimed: [user]
    define enrolled: [user]
    define admin: [user, role#assignee] or admin from platform
    define active_admin: admin and member
    define member: (claimed and enrolled) or active_admin from partner or admin from platform
```

- `enrolled` is written and removed by FlowSeer's own membership API
  (invite, remove). Removing a member also removes that member's grants in
  the tenant, so an access review reads exact state.
- `claimed` is never stored. The authentication layer resolves every
  organization claim through the tenant binding, then supplies the matching
  claimed relationship or caveat context for the named tenant. When a
  provider removes someone from an organization, their access ends at the
  next token refresh, the access-token lifetime the provider configures. An
  issuer with no organization claim cannot satisfy the claimed-member path.
- `platform#admin` is a global admin. It inherits `tenant#admin` everywhere.
  It does not inherit `tenant#full_payload`, the grant to read captured
  payload, which is the most restricted data FlowSeer holds. Full payload
  stays an explicit, time-bounded grant that is written to the operator
  action trail.
- `partner` connects a service-provider tenant. A customer grants a role to
  `tenant:<provider>#active_admin`, and removes it with one delete.

The membership check runs once per request, on the tenant object. Resource
permissions (`edge#capture`, `capture_session#download`) are plain unions
over stored relationships in the initial OpenFGA model, with no intersection.
The [OpenFGA note](../research/2026-09-30-openfga-authorization-spike.md#membership-gated-by-token-claims)
measured the direct-tenant intersection on 336,249 relationships:
11 of 1,104 Tag edges and zero platform-admin edges at a 60-second deadline.
The [SpiceDB note](../research/2026-09-30-spicedb-authorization-spike.md#membership-aware-resource-lookup)
re-measured that placement on 336,245 relationships over gRPC. OpenFGA
returned 1,104 Tag edges in 905.764 ms and zero platform-admin edges at 60 s.
The Tag disagreement is unexplained, with different fixtures and stored-grant
reference shapes. The OpenFGA note's 292,238-relationship union returned the
Tag and admin sets in 9 ms and 148 ms. The SpiceDB note's paired-model OpenFGA
counterparts took 12.960 ms and 82.658 ms. These observations apply to the
models and fixtures measured.

Because resource permissions carry no membership term in the initial
OpenFGA model, every object check also asks whether the object's `tenant`
relation names the admitted tenant, and FlowSeer's grant API writes grants
only to members of the granting tenant. A capture session's permissions
derive from the edge stored with it, never from the edge a request names: the
capture store reads a session by tenant and session id (`Store.Session` in
`src/services/device/internal/captureapi/store.go`).

### Every RPC declares its rule

Each operator RPC carries an authorization rule as a protobuf method
option. One Connect interceptor enforces it:

| Rule mode | The interceptor |
| --- | --- |
| The object is named in a request field | checks the relation on that object before the handler runs |
| The object is the admitted tenant (creating an edge) | checks the relation on the tenant before the handler runs |
| The object is the platform | checks the relation on `platform:flowseer` before the handler runs, reading no tenant header |
| The object is known only after a load | runs the handler with an obligation in the context, and returns `Internal` and drops the response when the handler returned without a check |
| The handler filters a list | same obligation as a load |
| No rule | refuses the call |

A conformance gate fails any operator RPC without a rule, so a forgotten
check is a denied call and a failed build, never an open door. Streaming
RPCs are refused until their rules are designed.

### Lists check per parent, never through ListObjects

A list reads a page from FlowSeer's own records and checks each distinct
parent once with `BatchCheck`: a page of capture sessions shares a few
edges. `ListObjects` is not used on a request path. OpenFGA caps results at
`listObjectsMaxResults` (1000 by default) and can return a partial result at
its deadline with no sign that the list is partial
([openfga/openfga#2828](https://github.com/openfga/openfga/issues/2828)).
The [SpiceDB spike](../research/2026-09-30-spicedb-authorization-spike.md)
reports its cursor-paged `LookupResources` equivalent.

### FlowSeer's records own every relationship

OpenFGA holds a projection. Each relationship derives from a
FlowSeer record: an edge's tenant, a session's edge and requester, a member's
enrollment, a role, or a grant. A projector writes relationships as records
change and repairs drift. A removal that revokes access (a member, a grant,
a role assignment) also deletes its relationships before the RPC returns,
so revocation never waits on the projector. Keeping the source in FlowSeer's
records means any tenant's relationships can be rebuilt from them. That is
needed for the loss preview, and avoids depending on engine-specific
relationship reads to select a single tenant's relationships.

### Previews of an access change

Adding or removing a Tag that changes access previews who gains or loses
what before an admin signs it off (`GOALS.md`). Sites and Tags have schemas
but no inventory service stores them yet, so the preview lands with that
service. Its shape is described here:

- **Gains**: OpenFGA can supply candidate relationships as contextual tuples,
  and a per-edge comparison gives who gains. SpiceDB has no request-scoped
  relationships, so its adapter must isolate candidate relationships in a
  disposable datastore before making that comparison.
- **Losses**: the tenant's relationships are rebuilt into an isolated
  disposable datastore, the change is applied there, and the same per-edge
  diff runs against the live store. The
  [OpenFGA note](../research/2026-09-30-openfga-authorization-spike.md#previewing-a-tag-change)
  rebuilt a 14,629-relationship union tenant, including sessions, in 0.55 s.
  Apply took 0.10 s and the all-user `ListUsers` diff of 406
  edges took 2.54 s, with 291 lost pairs. The
  [SpiceDB note](../research/2026-09-30-spicedb-authorization-spike.md#disposable-rebuild)
  re-measured a 14,615-relationship union tenant including sessions. OpenFGA
  took 1,489.714 ms to load, 56.711 ms to apply, and 1,793.662 ms to diff
  over gRPC. Its dedicated Tag-user fixture loses 406 pairs. The missing
  14 relationships and timing difference are unexplained. SpiceDB's
  corresponding times were 544.766, 38.017, and 3,204.364 ms.

The rejection of recursive Tag exclusion (`but not blocked`) applies to
the [OpenFGA note's exclusion fixture](../research/2026-09-30-openfga-authorization-spike.md#previewing-a-tag-change):
372,239 relationships, exclusion on recursive Tag permissions, and a
60-second deadline. It previewed losses exactly but returned zero Tag-derived
objects with both ListObjects algorithms.
The [SpiceDB note](../research/2026-09-30-spicedb-authorization-spike.md#tag-exclusion-and-preview)
did not measure the exclusion variant comparably.

### The operator action trail ships with authorization

Nothing records today who created an edge or minted a setup key
(`src/services/device/README.md`). Authorization adds global admins and
cross-tenant grants, so each admin-surface change, each full-payload
grant, and every capture download is recorded with its authenticated
`OperatorRef`, tenant, object, action, and outcome in the same change that
turns authorization on. That event goes to a per-tenant operator stream
rather than the device-scoped audit stream.

## Consequences

- Every operator request makes two checks. Both spikes measured the tenant
  gate and resource check separately, with the complete p50 and p99 tables in
  the research notes. The adapter keeps those checks behind one service
  contract.
- The operator API is unusable without an OIDC issuer and an OpenFGA
  deployment. The lab deployment gains both in this record's second phase.
- `OperatorRef` names an issuer and a subject, and handlers stamp it from
  the authenticated principal instead of trusting the payload.
- Site and Tag grants and the preview wait for the inventory service. The
  OpenFGA model reserves their types.

## Amendments

### 2026-10-02: platform rule mode

The rule-mode table gains a platform row for global admin RPCs on
`TenantService`. The interceptor checks `platform:flowseer#admin`, reads no
tenant header, and sets no tenant in the context.

A tenant in a platform request is the object the RPC reads, never the
tenant the call is admitted to, so the interceptor reads no tenant header,
makes no membership check and no tenant-relation check, puts no tenant in
the context, and makes one check on `platform:flowseer`, and `Require` and
`Filter` refuse under it. The interceptor reads the rule before
the membership check, which the diagram under
[A request, end to end](#a-request-end-to-end) draws the other way round.

