---
title: Operator Authorization - Direction
type: direction
date: 2026-09-28
topic: operator-authorization
status: superseded
superseded_by: docs/architecture/2026-09-30-operator-authorization-direction.md
---

# Operator Authorization - Direction

> `2026-09-28-operator-authorization-direction.md` is superseded by [the 2026-09-30 operator authorization direction](2026-09-30-operator-authorization-direction.md), which takes over this subject and binds once a person accepts it.

`DeviceService`, `EdgeAdminService`, and `CaptureService` authenticate no
caller and check no permission. `src/services/device/internal/host/serve.go`
says so in its package comment ("None carries an authorization check of its
own"), and `src/services/device/README.md` accepts the gap for now. The
deployment's network boundary is all that protects the device credentials.
Anyone who can reach the API port can do anything to any edge, including
start a promiscuous capture or mint a setup key. The actor a
`DeviceService` call records is whatever the request body claims
(`spec/proto/flowseer/api/device/v1/device_service.proto`, `actor` on
`AbandonMutationRequest` and `ResolveDesynchronizationRequest`).

This record decides how an operator call is authenticated, which tenant it
belongs to, and how it is authorized. It covers all three surfaces from the
first deployment, for more than one tenant.

## Callers authenticate with an OIDC token, and identity is ambient

Every call to an operator surface carries an OpenID Connect bearer token.
A Connect interceptor verifies it against the issuers the deployment
configures, and rejects a missing or invalid token with `Unauthenticated`.
It joins the chain every handler already shares
(`connect.WithInterceptors(TelemetryInterceptor(...), ValidatingInterceptor())`
in `serve.go`). `TelemetryInterceptor` already logs `Unauthenticated` and
`PermissionDenied` at WARN, so denials need no logging of their own.

FlowSeer names no identity provider. Any OIDC issuer works; one that runs
self-hosted in the EU, such as Zitadel or Keycloak, is what a deployment
would pick under the vendor rule. The token's `iss` and `sub` claims become
the caller's `flowseer.model.identity.v1.OperatorRef`. `sub` is the
provider's stable subject, which is what `OperatorRef.subject` already
means.

The caller's identity is ambient, the way tenancy already is under
`docs/conventions/protobuf.md` ("Tenancy is ambient"): the server derives it
from the authenticated context, and no request message carries it. The
`actor` fields on the two `DeviceService` requests go away, and the server
writes the actor into the records it stores. A capture session's requester
works the same way. A field a caller fills in about itself is a field the
server would have to check against the token on every call. Leaving it out
is simpler, and it cannot disagree with the token.

## Tenants are entities, and the token names one

A tenant becomes a UUID-identified entity in `flowseer.model.identity.v1`,
beside `OperatorRef`, with a `TenantLocalRef`/`TenantGlobalRef` pair and the
Config/State/Event triad. It replaces `model/inventory/v1/tenant.proto`,
whose keyless `TenantRef` and `Tenant` messages nothing imports; the file
goes. The
`ENTITY_TYPE_TENANT` exception in `docs/conventions/protobuf.md` ends once
the tenant store can answer an existence check.

A tenant's Config binds it to an identity provider organization: an issuer
URL and the value of an organization claim, where the claim's name is set
per issuer. The authentication interceptor looks up the tenant bound to the
token's issuer and organization, and puts it in the request context. A
token that maps to no tenant is `PermissionDenied`, not `Unauthenticated`:
the caller proved who they are and belongs nowhere here. A person who works
for two tenants holds two tokens.

The tenant id is also the tenant token in the bus subject layout. The
edgebus hub already has that token, hardcoded to `DefaultTenant = "default"`
(`src/modules/edgebus/subjects.go`, `Hub.Tenant` in `hub.go`). It also
names the per-tenant NATS account that
`docs/architecture/2026-08-20-device-service-and-inventory-direction.md`
("one NATS account per tenant") already decides. So one id names a tenant
on the bus, in central's stores, and in the authorization engine.

Central's own streams are the exception to per-tenant accounts: they sit
on the central account and take every tenant's subjects. The device
`AuditStream` today filters on `flowseer.default.audit.device.>`
(`src/modules/edgebus/hub.go`), which would drop every tenant but one once
the token is a real id. Such a stream subscribes with a wildcard in the
tenant position, and its consumers read the tenant from the subject.

Central's stores are partitioned by tenant: every key in the
`device-lanes`, `edges`, and `captures` buckets, and every key a later store adds, starts
with the tenant id. Lookup indexes are the exception because they resolve an
identifier before the tenant is known: `edge_<edgeID>` and `setupkey_<keyID>`
in `edges`, and `org_<hash>` in `tenants`. Stored bytes on disk, such as
capture artifacts, live under a per-tenant directory. A handler reads the
tenant from the context and never from the request, so a request cannot reach
another tenant's keys.

## Authorization follows the Zanzibar model

Permissions are relations between objects and subjects, as in Zanzibar
(Pang et al., "Zanzibar: Google's Consistent, Global Authorization System",
USENIX ATC 2019). Each FlowSeer entity an operator acts on (tenant, edge,
device, capture session) is an object type. A relation is either stored
(an operator is an `admin` of a tenant, an edge belongs to a tenant) or
computed from others (whoever administers a tenant may `capture` on its
edges). The service asks the engine whether a subject holds a relation on
an object, and it writes the stored relations when an entity is created
or deleted.

The object types and relations the first model needs are listed below.
Each is written as separate fields, never as an `object#relation` string:

| Object type | Relation | Kind | Grants or means |
| --- | --- | --- | --- |
| `tenant` | `admin` | stored | manage the tenant's operators and edges; implies every relation below |
| `tenant` | `operator` | stored | act on the tenant's edges and devices |
| `tenant` | `viewer` | stored | read the tenant's edges, devices, and sessions |
| `tenant` | `full_payload` | stored | request `full_payload_requested: true` on a capture |
| `edge` | `tenant` | stored | the tenant the edge belongs to |
| `edge` | `manage` | computed from `tenant.admin` | create, retire, and issue or revoke setup keys |
| `edge` | `capture` | computed from `tenant.operator` | create, stop, delete, get, and list capture sessions |
| `device` | `edge` | stored | the edge that reaches the device |
| `device` | `mutate` | computed from `edge`'s `tenant.operator` | apply, abandon, and resolve mutations |
| `device` | `read` | computed from `edge`'s `tenant.viewer` | read interfaces and access status |
| `capture_session` | `edge` | stored | the edge the session runs on |
| `capture_session` | `requester` | stored | the operator who created it |
| `capture_session` | `download` | computed from `requester`, or from the edge's `tenant.admin` | tail or download the session's packets |

Listing RPCs (`ListEdges`, `ListCaptureSessions`, `ListEdgeOpenMutations`)
ask the engine which objects the caller may see (Zanzibar's lookup
resources), rather than listing everything and dropping what fails a check.

Each handler still checks that the object it loaded belongs to the
caller's tenant before it asks the engine. The key prefix makes that
check cheap, and it keeps a wrong relation in the engine from leaking an
object across tenants.

### SpiceDB is the engine, behind an interface

The first deployment runs SpiceDB on PostgreSQL. The service reaches it
through a Go interface (check, bulk check, lookup resources, write and delete
relationships) that has a SpiceDB adapter and an in-memory fake for tests.
The permission model is a SpiceDB schema file owned by the service. It is
configuration, not protobuf.

SpiceDB won for four reasons:

- **The tuple shape.** Its relationship API is already structured fields:
  `Relationship{resource: ObjectReference{object_type, object_id}, relation,
  subject: SubjectReference{object, optional_relation}}`
  ([authzed/api `core.proto`](https://raw.githubusercontent.com/authzed/api/main/authzed/api/v1/core.proto)).
- **Consistency.** It ships Zanzibar's consistency token as an opaque
  ZedToken, "the SpiceDB equivalent of Google Zanzibar's Zookie"
  ([ZedTokens](https://authzed.com/docs/spicedb/concepts/zedtokens)). That
  closes the "new enemy" case this system has: an operator's access is
  revoked, and a download a moment later must not be served from the old
  state.
- **What an adapter can't add.** Caveats
  ([Caveats](https://authzed.com/docs/spicedb/concepts/caveats)) and a Watch
  stream are engine features. Per-tenant isolation is not: FlowSeer supplies
  it with the tenant relation, the key prefix, and the handler check.
- **The vendor rule.** It is Apache-2.0
  ([LICENSE](https://github.com/authzed/spicedb/blob/main/LICENSE)), a
  single Go binary, and self-hosted on PostgreSQL
  ([datastores](https://authzed.com/docs/spicedb/concepts/datastores)).

### The schema names no engine

The protobuf schema uses Zanzibar's concepts where they make it better and
never one engine's:

- A relationship on the wire is structured fields: an object type (an
  enum), an object id, a relation (an enum per object type), and a subject
  (an `OperatorRef`, or an object with an optional relation). None of it is
  a string in any engine's notation.
- No engine object id, schema-language fragment, or token format appears in
  `model/` or `api/`.
- A consistency token that must survive a restart is opaque bytes on a
  record under `store/`, the way Zanzibar treats a zookie. The adapter
  alone reads it.

Swapping SpiceDB for another Zanzibar engine then changes the adapter, the
schema file, and the stored tokens, not the wire.

### Operators get roles, not raw relationships

The admin API grants and revokes roles: an operator is made `admin`,
`operator`, or `viewer` of a tenant, or granted `full_payload`. It does not
expose relationship writes. Relations such as `edge.tenant` and
`capture_session.requester` are facts only the service knows, so only the
service writes them. An API that let a caller write one could move an edge
into another tenant.

A deployment's first tenant admin comes from its configuration: the issuer,
organization, and subject that may create tenants and grant the first
roles. That is the only identity outside the engine.

### Relationship writes follow the stored record

The service writes an entity's relationships after it stores the record,
through the same record-is-the-outbox pattern the lane journal uses
(`src/services/device/README.md`, "The record is the outbox"). The stored
record carries the relationships it still owes the engine, and a sweep
retries them. A crash between the two writes leaves a record whose
relations are missing. That fails closed: the object is invisible until the
sweep catches up, never visible to the wrong caller. Deleting runs in
reverse: relationships first, then the record.

## Actions leave a trail

Every call to an operator surface that changes something, and every capture
download, emits an operator event with the authenticated `OperatorRef`, the
tenant, the object, the action, and its outcome. It goes to a per-tenant
stream of its own. That stream is not the device-scoped audit stream,
which `src/services/device/README.md` says "is device-scoped by design and
is not" the operator trail. This answers both open items: the capture
direction record's "a download is itself an event", and the README's
missing operator action trail (who minted which setup key).

## Alternatives

**OpenFGA** (Apache-2.0, CNCF Incubating,
[CNCF announcement](https://www.cncf.io/blog/2025/11/11/openfga-becomes-a-cncf-incubating-project/))
was the closest alternative. Its stores give hard per-tenant isolation
([concepts](https://openfga.dev/docs/concepts)). But it has no consistency
token yet, only a mode that bypasses the cache, and tokens are "considered
for future releases"
([consistency](https://openfga.dev/docs/interacting/consistency)). Its
change feed is poll-only. FlowSeer can build tenant isolation itself; it
cannot build a consistency token.

**Ory Keto** (Apache-2.0) has no snapshot token: the issue that would add
one is open ([ory/keto#517](https://github.com/ory/keto/issues/517)). Its
documentation leads with the string notation this record rules out.

**Permify** is AGPL-3.0
([LICENSE](https://github.com/Permify/permify/blob/master/LICENSE)), and
since November 2025 it has belonged to FusionAuth
([announcement](https://fusionauth.io/blog/fusionauth-permify-pr)). Its
roadmap is unsettled while the two products merge.

**Topaz** (Apache-2.0) and **Warrant** were not carried forward. Topaz was
not examined in enough depth to be a finalist. Warrant's development has
moved to a closed WorkOS product.

**Policy engines** (Cerbos, OPA, Cedar) were ruled out. Every permission
here follows ownership (an edge's tenant, a session's requester), and a
policy engine would need those relations supplied on every call.

**A request-carried actor** that the server checks against the token was
rejected. It keeps a field that can only ever equal the token, and a check
that every handler must remember to make.

**One tenant per deployment** was rejected by the user in favor of
multi-tenancy from the first deployment.

## Consequences

- PostgreSQL and SpiceDB join the central deployment. They are the first
  relational database and the first external service central depends on for
  every operator call. SpiceDB's Watch on PostgreSQL needs commit
  timestamps tracked
  ([datastores](https://authzed.com/docs/spicedb/concepts/datastores)).
- `GOALS.md`, the transport diagram in
  `docs/architecture/2026-08-20-device-service-and-inventory-direction.md`,
  and the device README's accepted gap stop naming OpenFGA.
- The capture direction record's authorization section is amended in the
  capture authorization plan. Its relation names are the `edge`/`capture`,
  `capture_session`/`download`, and `tenant`/`full_payload` rows above.
- `docs/conventions/protobuf.md` loses the `ENTITY_TYPE_TENANT` exception
  when the tenant store lands, and "Tenancy is ambient" gains its source:
  the tenant bound to the token's issuer and organization.
- Central's buckets change key layout, and `edgebus.DefaultTenant` gives
  way to real tenant ids. Nothing is deployed, so no stored data is
  migrated.
- The operator surfaces get the request-body limit that today wraps only
  the edge-facing handlers.

## Amendments

### 2026-09-30 — a competing proposal

[`2026-09-30-operator-authorization-direction.md`](2026-09-30-operator-authorization-direction.md),
proposed without knowledge of this record, chooses OpenFGA over SpiceDB on
spike measurements and names a request's tenant per request, admitted by
FlowSeer-owned membership, instead of binding it to the token's
organization. This record's identity leaf, tenant entity, and partitioned
stores landed first. The two are reconciled before the engine or caller
authentication is planned.

### 2026-09-30 — tenancy as built

The identity leaf, the tenant entity, and tenant-partitioned stores landed
2026-09-30. What they settled beyond the sections above:

- `TenantService` is defined but not served until callers are
  authenticated. Tests and development create tenants through the tenant
  store, so no unauthenticated caller can create a tenant or claim an
  organization. The platform admin names its issuer, organization claim
  name and value, and subject.
- A tenant id is a canonical lowercase UUID wherever it builds a key, a bus
  subject, or a path. Capture artifacts live under
  `<StateDir>/captures/<tenant_id>/`.
- A tenant's record and its organization index commit together in one
  atomic batch on the `tenants` bucket, so one organization binds at most
  one tenant and a record without its index cannot exist. Untested so far:
  a physical file-store failure between the batch's stores, clustered
  wrong-sequence responses, malformed records, and retry exhaustion.
- An edge's tenant has one authority, its `edge_<edge_id>` index in
  `edges`. A device lane is keyed by its hosting edge's tenant, and a caller
  of another tenant gets `NotFound`. Nothing substitutes a default tenant
  for one it could not resolve.
- State written before this change is not read: unprefixed keys, capture
  files directly under `<StateDir>/captures/`, and edge accounts without a
  persisted tenant.
