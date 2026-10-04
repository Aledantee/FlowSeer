---
title: Operator Authorization Phase 4, Tenancy Admin Surfaces and the Action Trail - Plan
type: feat
date: 2026-09-30
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: fixes needed
execution: mixed
amends: docs/architecture/2026-09-30-operator-authorization-direction.md
parent: docs/plans/2026-09-30-1139-feat-operator-authorization-plan.md
---

# Operator Authorization Phase 4, Tenancy Admin Surfaces and the Action Trail - Plan

> Implemented. U1 through U7 passed from `54dfbd98` through `8a95245a`. U7
> also changed `src/services/device/internal/authz/openfga/checker.go`,
> `checker_test.go`, and `relations_test.go`, which were not named by the
> original U7 `Files:` line. The existing adapter rejected the usersets that
> the projector emits for role assignees and partner active admins, so the
> tagged OpenFGA run could not pass until those two model-defined shapes were
> accepted while malformed usersets remained fail-closed.

## Goal

Tenant admins enroll and remove members, define roles, and grant them.
Customers connect and disconnect service-provider tenants. Platform admins
are bootstrapped from configuration. Full-payload grants are explicit and
expire. Every one of these changes lands in the operator action trail
phase 3 introduces.

The means: `TenantService` is served, a new `TenantAdminService` writes
member, role, and partner records to a tenant-partitioned bucket, the
projector derives every `tenant`, `role`, and `platform` relationship from
those records and the configuration, and the trail interceptor records each
change.

**Stop condition:** a second writer of `tenant`, `role`, or `platform`
relationships appears before this lands (an inventory service granting on
Sites, an identity-provider sync). The projector then cannot own those
relations whole, and the ownership table below is wrong.

## Decisions

The parent plan's Decisions apply. Sources under `~/go/pkg/mod`: `openfga@`
is `github.com/openfga/openfga@v1.21.0`, `api@` is
`github.com/openfga/api/proto@v0.0.0-20260723150800-6981fff8d33b/openfga/v1`,
and `nats-server@` is `github.com/nats-io/nats-server/v2@v2.15.0/server`.
Paths under `internal/` are under `src/services/device/`. "The record" is
`docs/architecture/2026-09-30-operator-authorization-direction.md`.

- The plan stays whole at seven units. Why: the units form one cluster.
  Every one feeds U6, which mounts the services, and U6 cannot be cut:
  serving `TenantAdminService` without the records, their projection, or
  the trail entries would land a change with no tuple or no record of it.
  It runs past 300 lines because the stub left three designs to settle, the
  parent handed over three questions, and phase 3 left one.

### Records

- Three record kinds live in `flowseer.model.identity.v1`, file
  `access.proto`, and are stored as they are in a new `access` bucket:

  | Record | Key | Holds |
  | --- | --- | --- |
  | `Member` | `<tenant>.member.<principal id>` | `operator`, `enrolled_at`, `enrolled_by`, `roles` (role refs), `full_payload` (`expires_at`, `reason`, `granted_by`, `granted_at`) |
  | `Role` | `<tenant>.role.<role id>` | `ref`, `name`, `description`, `relations` (one to four of `admin`, `operator`, `capturer`, `viewer`) |
  | `Partner` | `<tenant>.partner.<provider tenant id>` | `tenant` (the provider's `TenantGlobalRef`), `relations` (one to three of `operator`, `capturer`, `viewer`), `connected_at`, `connected_by` |

  Why: every key a later store adds starts with the tenant id (the record,
  "Tenants, as landed"), and the `tenants` bucket lists every key without
  the `org_` prefix as a tenant (`internal/tenantstore/store.go:311-316`).
  The families are intent only and named plain
  (`docs/conventions/protobuf.md:52-65`): nobody observes a member, and the
  action trail carries the change, so the file comment names the absent
  `State` and `Event`. `Role` is UUID-identified and has the ref pair. A
  member is keyed by its `OperatorRef` and a partner by the provider's
  tenant ref. The principal id is `authn.ComputePrincipalID`.
- The member record holds everything that names the user in the tenant:
  the enrollment, the role assignments, and the full-payload grant. Why:
  parent requirement 6 deletes a member's grants with the member, and one
  key makes that one compare-and-set delete.
- `admin`, `operator`, `capturer`, and `viewer` reach a user only through
  a role, and a role holds tenant-wide relations. Full payload is the one
  grant made to a member directly. Why: the Goal says roles are defined
  and granted, parent requirements 6, 7, 8, and 11 name no grant on a
  single edge or device, and the Goal and the record keep full payload
  explicit, outside any role.
- A role assignment and a full-payload grant name an enrolled member. Why:
  the record has the grant API write "only to members of the granting
  tenant", and a member record is what FlowSeer's records can say about
  membership. A platform or partner admin who needs full payload in a
  tenant is enrolled there first. Enrollment admits nobody whose token
  does not claim the tenant's organization, since `enrolled` counts toward
  `member` only with `claimed`.
- `EnrollMember` takes an `OperatorRef` whose issuer is one of
  `authentication.issuers`. Why: a subject of any other issuer can never
  present a token, and FlowSeer has no way to look a person up at an
  identity provider.
- Role names are labels and may repeat. Why: a unique name needs a second
  key claimed in one conditional batch
  (`docs/solutions/architecture-patterns/a-multi-key-uniqueness-claim-needs-one-conditional-batch.md`),
  and the role id is the identity.
- A partner link is the customer's record and needs no consent from the
  provider. `Partner.relations` excludes `admin`. Why: the Goal has
  customers connect and disconnect, and the model admits
  `tenant#active_admin` on `operator`, `capturer`, and `viewer` only (the
  record, "The authorization model").
- Platform admins are the subjects the configuration names.
  `PlatformAdmin.subject` becomes `subjects`, one to 16 distinct values.
  No RPC adds one. Why: the Goal bootstraps them from configuration, the
  projector writing `platform:flowseer#enrolled` at start needs no prior
  grant, and `subject` is read by no code today (`internal/host/serve.go:143-148`
  passes issuer, claim name, and organization only), so any subject
  carrying the platform claim value already has `Platform` true.

### Admin API

- `TenantService` is served as defined. `TenantAdminService` is new in
  `flowseer.api.identity.v1`:

  | RPC | Rule | Trail action | Object in the trail |
  | --- | --- | --- | --- |
  | `TenantService.CreateTenant` | platform, `admin` | `TENANT_CREATE` | the created tenant, on the completion |
  | `TenantService.GetTenant`, `ListTenants` | platform, `admin` | none | |
  | `EnrollMember` | tenant, `admin` | `MEMBER_ENROLL` | member |
  | `RemoveMember` | tenant, `admin` | `MEMBER_REMOVE` | member |
  | `CreateRole` | tenant, `admin` | `ROLE_CREATE` | role and its relations, on the completion |
  | `DeleteRole` | tenant, `admin` | `ROLE_DELETE` | role |
  | `AssignRole`, `UnassignRole` | tenant, `admin` | `ROLE_ASSIGN`, `ROLE_UNASSIGN` | role and member |
  | `ConnectPartner` | tenant, `admin` | `PARTNER_CONNECT` | provider tenant and relations |
  | `DisconnectPartner` | tenant, `admin` | `PARTNER_DISCONNECT` | provider tenant |
  | `GrantFullPayload` | tenant, `admin` | `FULL_PAYLOAD_GRANT` | member, and `expires_at` on the completion |
  | `RevokeFullPayload` | tenant, `admin` | `FULL_PAYLOAD_REVOKE` | member |
  | `ListMembers`, `ListRoles`, `ListPartners` | tenant, `admin` | none | |

  Why one rule: `tenant#admin` holds a user with an admin role and a
  platform admin, and never a partner's admin (the record, "The
  authorization model"), so a provider cannot administer a customer's
  membership. The conformance table already allows both pairs
  (`knownRelations` in `test/conformance/proto/api_authorization_test.go`).
- Every change RPC but `CreateRole`, which mints an id, is idempotent.
  Enrolling a member, assigning a role, or
  connecting a partner that is already stored answers the stored record,
  and `ConnectPartner` replaces the relations of an existing link. A
  removal of an absent record runs its projection and answers success.
  `CreateTenant` resolves the organization through `LookupByOrg` first and
  answers the stored tenant when the request equals its configuration, or
  `AlreadyExists`. Why: the retry rule under Projection needs it, and
  `tenantstore.Create` is idempotent only for an id the caller keeps
  (`internal/tenantstore/store.go:108-112`), which a handler minting a
  fresh UUID does not.
- `DeleteRole` answers `FailedPrecondition` while a member holds the role.
  A member that still names a role with no record, after a race between
  the two, gets no tuple from it. Why: a cascade over every member record
  is many writes with no atomic commit.
- The list RPCs page as `ListTenants` does (`page_size` up to 500,
  `page_token`).

### Projection

- The projector owns the relations below whole, whatever user a stored
  tuple names, and adds one condition to a relation it owns today:

  | Object | Relation | User | Derived from |
  | --- | --- | --- | --- |
  | `platform:flowseer` | `enrolled` | `user:<principal id>` | each of `platform_admin.subjects` with its issuer |
  | `tenant:<id>` | `platform` | `platform:flowseer` | the committed tenant record |
  | `tenant:<id>` | `enrolled` | `user:<principal id>` | a member record |
  | `tenant:<id>` | `partner` | `tenant:<provider>` | a partner record |
  | `tenant:<id>` | `admin`, `operator`, `capturer`, `viewer` | `role:<role id>#assignee` | a role record's relations |
  | `tenant:<id>` | `operator`, `capturer`, `viewer` | `tenant:<provider>#active_admin` | a partner record's relations |
  | `tenant:<id>` | `full_payload` | `user:<principal id>` | a member's grant whose `expires_at` is after now |
  | `role:<role id>` | `assignee` | `user:<principal id>` | each member record naming a role that has a record |
  | `capture_session:<id>` | `requester` | `user:<principal id>` | as today, and only while the requester has a member record in the session's tenant |
  | `edge:<id>` | `administer`, `operate`, `capture`, `view` | any | nothing: no record grants on one edge |
  | `device:<id>` | `operate`, `view` | any | nothing: no record grants on one device |

  Why whole relations: the record has FlowSeer's records own every
  relationship. A tuple no record explains would grant access no list
  shows, so a pass deletes it. This answers the parent's question on
  `tenant#platform` and `tenant#partner`. Why the last two rows: parent
  requirement 6 leaves no relationship naming a removed user on an object
  of the tenant, and a grant written by hand on an edge would survive the
  removal. `TestReconcileDriftRestoration` keeps such a tuple today, which
  holds on for a projector with no access source.
- The `requester` condition is what parent requirement 6 asks: after a
  removal OpenFGA holds no relationship naming the user on an object of
  the tenant, and a capture session is one. A platform or partner admin
  who requested a session keeps `download` through `capture from edge`.
- With no access source configured the projector owns the five relations
  it owns today and nothing else. Why: U3 lands before the host passes
  one, and the running service must not delete tuples it cannot yet
  derive.
- `Sync` stays the only writer. `SyncTenant(tenant)` syncs `tenant:<id>`
  and every role of the tenant. `SyncRequester(tenant, operator)` syncs
  the sessions of that tenant the operator requested. Why: both are
  computable from the request alone, which the retry below relies on. One
  `SyncTenant` costs one `Read` per 100 tuples on the tenant and one per
  role. Its duration for a tenant of thousands of members is unmeasured.
- A revoking removal writes its record first, then projects, and answers
  success only after the projection. A failed projection answers
  retryable `Unavailable` with the projector's error as cause. The retry
  finds no record, which the idempotency rule treats as removed, and
  projects again. This answers the parent's question on a retryable
  order:

  ```mermaid
  flowchart TD
      A[RemoveMember U in tenant A] --> B{member record present}
      B -->|yes| C[delete under compare-and-set]
      B -->|no| D
      C --> D[SyncTenant A, then SyncRequester A and U]
      D -->|error| E[Unavailable, retryable]
      E -.->|caller retries| A
      D -->|ok| F[answer]
  ```

  Why this order: the projector derives tuples from records, so a
  projection before the delete would write the tuples back. Between the
  delete and the projection the user keeps access for one engine round
  trip. A pass repairs what a caller never retries.
- Every other change RPC projects the same way and returns the error,
  on its idempotent path too: a call that finds its record already stored
  projects before it answers. `CreateRole` alone logs the error and
  answers. Why: a `CreateTenant` whose projection failed leaves the tenant
  without `tenant#platform`, and the retry is what writes it. `CreateRole`
  mints an id, so a retry would store a second role, and a role with no
  assignee grants nothing until `AssignRole` projects it. `CreateEdge`
  sets the precedent (`internal/edgeapi/admin.go:194`).

### Full payload

- A grant expires by its record. `GrantFullPayload` takes a `lifetime` of
  more than zero and at most 24 hours and a `reason`, and central stores
  `expires_at` from its own clock. A second grant replaces the first. Why
  24 hours: the grant gates creating a capture, a capture is bounded by
  its budget, and a longer need is a new grant in the trail.
- `CreateCaptureSession` with full payload passes
  `authz.Require(ctx, "full_payload", "tenant", tenant)` as today and then
  reads the member's grant, refusing one whose `expires_at` is not after
  now. The projector deletes the expired tuple on its next pass. Why: the
  handler's read makes expiry exact without waiting on the projector,
  as a revoking removal does not wait on it.
- No OpenFGA condition carries the expiry. Why: a check that meets a
  conditioned tuple without `current_time` in its context fails with
  validation error 2000 (`openfga@/internal/condition/eval/eval.go:131-137`,
  `api@/errors_ignore.pb.go:96`),
  which the adapter maps to `authz/engine-protocol` and which fails a
  whole `BatchCheck` (`internal/authz/openfga/checker.go:646-653`). A
  re-write with another expiry under `on_duplicate: "ignore"` answers
  `Aborted` (`openfga@/pkg/storage/sqlcommon/sqlcommon.go:924-931`), which
  the adapter retries as a conflict (`checker.go:452-453`), and one
  `Write` cannot delete and write the same key
  (`openfga@/pkg/server/commands/write.go:193-209`). The embedded model's
  test refuses a conditioned type
  (`internal/authz/openfga/model_test.go:139-141`).

### Action trail

- The trail records the changes in the table under Admin API and no read
  of the new services. Why: parent requirement 11 names changes, and a
  list of members discloses nothing a setup key or a payload does.
- `OperatorActionEvent.object` gains an arm per kind: a tenant, a member
  (`OperatorRef`), a role with its relations, a role assignment (role and
  member), a partner with its relations, and a full-payload grant (member
  and `expires_at`). Why the relations and the expiry: a removed record is
  deleted, so the trail is the only history of what was granted.
- A platform-rule call has no admitted tenant, and its records go to
  `flowseer.platform.operator.action.<action>`. Why: the stream binds
  `flowseer.*.operator.action.*` (`src/modules/edgebus/hub.go:400`), and
  `platform` can never be a tenant id (`tenant.Validate`,
  `src/common/tenant/tenant.go:41-51`).
- `GetEdge` and `ListEdges` records move to a second stream,
  `FLOWSEER_OPERATOR_READS`, on `flowseer.<tenant>.operator.read.<action>`,
  16 MiB and 1,000 records per subject. `FLOWSEER_OPERATOR_ACTIONS` keeps
  64 MiB and 10,000 per subject. The limits stay `HubConfig` fields with
  no deployment setting, as `DeviceServiceConfig` keeps the bus's storage
  bounds out until a deployment needs one
  (`spec/proto/flowseer/store/device/v1/service_config.proto:18-23`). Why
  the split: a stored record is 34 bytes plus
  subject, header, and payload (`nats-server@/filestore.go:10055-10062`),
  about 430 bytes for an 85-byte subject, a 63-byte `Nats-Msg-Id` header,
  and a 250-byte event. A subject at its cap holds 4.1 MiB, so 16 full
  subjects fill 64 MiB. The two view actions are open to every viewer, and
  5,000 calls fill one, so eight tenants with full view subjects fill the
  stream. From then on each write removes the oldest record in the stream
  whatever its subject (`nats-server@/filestore.go:5904-5920`), which is
  the rare change the trail exists for. After the split a change competes
  only with changes: 64 MiB holds about 156,000 records, or 78,000 change
  calls. The record's 2026-10-03 sentence that a flood evicts only records
  of its own action holds only below the stream's byte limit, and U5
  corrects it. This answers the parent's sizing question for views. What
  stays shared is under Open questions.
- The two streams reserve 80 MiB of the central account's 512 MiB beside
  the audit stream's 256 MiB (`nats-server@/jetstream.go:2603-2607`),
  which leaves the five buckets 176 MiB where four had 192.

### Carried from phase 3

- A call naming an object no record holds keeps answering
  `PermissionDenied` with `authz/denied`. Why: an id with no `tenant`
  tuple and an id in another tenant fail the same check
  (`internal/authz/obligation.go:253-260`), and telling them apart would
  let a caller learn that another tenant holds the id. The record's
  2026-10-02 amendment already refuses an answer that "could say whether
  an object exists", and `TestTheServiceStartsFromAFileAndAnswers`
  asserts the refusal. The handlers still return `NotFound` for a caller
  that reaches them, which their own tests hold. U6 corrects the record's
  sentence that a caller of another tenant gets `NotFound` and states the
  answer in the README.

### Tests

- The default tier passes `authztest.Engine`, which evaluates no model. It
  proves which tuples a change writes and deletes. That a role, a partner
  link, or a platform enrollment grants access is proven only by the
  tagged tier against OpenFGA v1.21.0, through the API (U7).
- A fixture that wrote `tenant:<id>#admin@user` into the fake engine uses
  `Engine.Grant` once the projector owns that relation, since a pass
  deletes the stored tuple. `tenant#member`, which fixtures store because
  the fake computes nothing, is outside the table and stays. A test that
  asserts no tuple names a user gives that user `member` through `Grant`.
- The operator action trail splits views into `FLOWSEER_OPERATOR_READS`
  beside `FLOWSEER_OPERATOR_ACTIONS`, amending the record's single-stream
  design, and the 24-hour full-payload ceiling and the one-sided partner
  link stand as written. (decided by the user, 2026-10-04)

## Requirements

Parent requirements 6, 7, 8, and 11 hold for the surfaces this phase adds, against the running device
service. For this phase that means:

1. A tenant is created through the API. With `platform_admin.subjects: "P"`
   and P's token carrying the platform claim,
   `CreateTenant{issuer: I, organization_claim_name: "groups", organization_claim_value: "acme"}`
   answers a record with a new UUID, `LookupByOrg(I, "acme")` returns it,
   and the engine holds `tenant:<id>#platform@platform:flowseer`. The same
   request again answers the same id. The request with another `name`
   answers `CodeAlreadyExists`. A caller without the platform claim gets
   `CodePermissionDenied`.
2. Platform admins come from the configuration. After the first pass the
   engine holds `platform:flowseer#enrolled@user:<ComputePrincipalID(I, "P")>`,
   and a stored `platform:flowseer#enrolled@user:x` the configuration does
   not name is gone.
3. Removal ends access and leaves nothing behind (parent 6). U is enrolled
   in A, holds a role with `viewer`, and requested session S. After
   `RemoveMember`, `GetEdge` by U in A answers `CodePermissionDenied`, and
   the engine holds no tuple with user `user:<U>` on `tenant:A`, on a role
   of A, on `capture_session:S`, or on an edge or device of A.
4. A removal converges on retry. With the engine failing, `RemoveMember`
   answers `CodeUnavailable` and the member record is gone. With the
   engine healthy the same call answers success and U's tuples are gone.
5. A role grants its relations to its assignees. `CreateRole` with
   `relations: [OPERATOR]` and `AssignRole` for U store
   `tenant:A#operator@role:R#assignee` and `role:R#assignee@user:<U>`.
   `UnassignRole` deletes the second before it answers. `DeleteRole` on an
   assigned role answers `CodeFailedPrecondition` with
   `identityapi/role-assigned`.
6. A grant names a member. `AssignRole` or `GrantFullPayload` for an
   operator with no member record answers `CodeFailedPrecondition` with
   `identityapi/not-a-member` and stores nothing. `EnrollMember` with an
   issuer the configuration does not list answers `CodeInvalidArgument`.
7. A provider acts only while linked (parent 8). `ConnectPartner` in C
   naming M with `[CAPTURER]` stores `tenant:C#partner@tenant:M` and
   `tenant:C#capturer@tenant:M#active_admin`, and M's admin creates a
   capture session on an edge of C. After `DisconnectPartner` the same
   call answers `CodePermissionDenied`. Naming C itself answers
   `CodeInvalidArgument`, and naming a tenant with no record `CodeNotFound`.
8. Full payload is granted and expires (parent 7). A platform admin gets
   `CodePermissionDenied` on a full-payload `CreateCaptureSession` in A,
   and succeeds after `EnrollMember` and
   `GrantFullPayload{lifetime: 1h, reason: "case 42"}` for itself. With
   the clock one hour later the call answers `CodePermissionDenied` while
   the tuple is still stored, and the next pass deletes the tuple. A
   `lifetime` of 25 hours answers `CodeInvalidArgument`.
9. Every change is in the trail (parent 11). `EnrollMember` by issuer I
   and subject S in tenant T for U puts an attempt and a completion on
   `flowseer.T.operator.action.member_enroll`, both naming I, S, and U
   under one `call_id`. `CreateTenant` puts two on
   `flowseer.platform.operator.action.tenant_create`, the completion
   naming the new tenant. `ListMembers` writes none.
10. Views cannot evict changes. `GetEdge` writes to
    `flowseer.T.operator.read.edge_get` in `FLOWSEER_OPERATOR_READS`, and
    with that stream at its byte limit a `setup_key_issue` record written
    before the flood is still in `FLOWSEER_OPERATOR_ACTIONS`.
11. The projector owns its relations whole. One pass deletes a stored
    `tenant:A#admin@user:x` and a stored `edge:E#capture@user:u`, neither
    of which a record explains, keeps `edge:E#tenant@tenant:A`, and keeps
    the tuple of a member enrolled while the pass is scanning.
12. An unknown object reveals nothing. `ReadInterface` naming a device the
    registry does not list answers `CodePermissionDenied` with
    `authz/denied`, as it does for a device of another tenant.

## Out of scope

- Grants on a single edge or device. They need a model package above
  `model/edge` and `model/inventory`, since `model/identity` imports
  nothing FlowSeer-owned, and a rule for a grant whose object is retired.
  Until then a pass deletes a grant written by hand on one.
- Inviting a person by email or looking one up at the identity provider.
- Suspending or changing a tenant. `api/identity/v1` has no such RPC.
- An RPC that adds a platform admin.
- Reading or exporting the trail. Nothing consumes the streams yet.
- Re-checking a running capture when a grant expires or is revoked. A
  stream is checked when it opens (the record, 2026-10-03), and the
  capture ends with its budget.
- Site and Tag grants, and check caching: the parent.
- Trust: a token and a request come from callers nobody trusts, and every
  malformed one is in scope. A tenant admin is trusted within its tenant
  only. The projector reads FlowSeer's own records and the deployment's
  own OpenFGA, both trusted, as is whoever writes the configuration.

## Units

### U1. Schema: access records, admin service, trail actions, platform subjects

Files: `spec/proto/flowseer/model/identity/v1/access.proto`, `spec/proto/flowseer/model/identity/v1/README.md`, `spec/proto/flowseer/model/README.md`, `spec/proto/flowseer/api/identity/v1/tenant_admin_service.proto`, `spec/proto/flowseer/api/identity/v1/README.md`, `spec/proto/flowseer/api/README.md`, `spec/proto/flowseer/event/operator/v1/operator_action_event.proto`, `spec/proto/flowseer/event/operator/v1/README.md`, `spec/proto/flowseer/store/device/v1/service_config.proto`, `spec/proto/flowseer/store/device/v1/README.md`, `generated/go/proto/flowseer/model/identity/v1/access.pb.go`, `generated/go/proto/flowseer/api/identity/v1/tenant_admin_service.pb.go`, `generated/go/proto/flowseer/api/identity/v1/identityv1connect/tenant_admin_service.connect.go`, `generated/go/proto/flowseer/event/operator/v1/operator_action_event.pb.go`, `generated/go/proto/flowseer/store/device/v1/service_config.pb.go`, `test/conformance/proto/model_identity_rules_test.go`, `test/conformance/proto/api_identity_rules_test.go`, `test/conformance/proto/event_operator_rules_test.go`, `test/conformance/proto/store_device_rules_test.go`, `src/services/device/internal/host/config_test.go`, `src/services/device/test/integration/fixture_test.go`, `src/services/device/test/integration/authz_enforcement_test.go`, `deploy/lab/README.md`
After: none
Change: `access.proto` declares `TenantRelation` (`ADMIN`, `OPERATOR`,
`CAPTURER`, `VIEWER`), `RoleLocalRef` and `RoleGlobalRef`, and `Role`,
`Member`, `FullPayloadGrant`, and `Partner` with the fields in the
Decisions. Bounds: a role name of 1 to 128 characters, relations distinct
and defined, at most 64 roles on a member, a reason of 1 to 512
characters. `tenant_admin_service.proto` declares the thirteen RPCs of the
Admin API table, each with `mode: RULE_MODE_TENANT`,
`object_type: "tenant"`, `relation: "admin"`. A request names a member by
`OperatorRef`, a role by `RoleGlobalRef`, and a partner by
`TenantGlobalRef`. A change answers the stored `Member`, `Role`, or
`Partner`, and a removal an empty message.
`GrantFullPayloadRequest.lifetime` carries
`duration: {gt: {seconds: 0}, lte: {seconds: 86400}}`. `OperatorAction`
gains the eleven values of the table, numbered from 10, and `object` gains
the six arms of the Decisions, numbered from 12. `PlatformAdmin.subject` is
removed as `docs/code-style-proto.md`, Evolution, prescribes, and
`subjects` is a repeated string of one to 16 distinct values, each 1 to 256
characters. The generated files come from `buf generate`. The READMEs say what each
package now holds, and `api/identity/v1/README.md` drops "no host serves
it" and the absent operator-management line. The three Go fixtures that
set `subject` set `subjects`, and the lab README's sentence on
`platform_admin.subject` names `subjects`.
Tests: `model_identity_rules_test.go` accepts each record whole and
refuses a role with no relation, a repeated relation, an undefined one, a
`Partner` holding `ADMIN`, and a member with 65 roles.
`api_identity_rules_test.go` refuses each request without its ref and a
`lifetime` of zero, of 86,401 seconds, and unset.
`event_operator_rules_test.go` accepts one event per new arm.
`store_device_rules_test.go` refuses `platform_admin` with no subject,
with 17, and with a repeated one.
`TestEveryOperatorRPCHasAuthorizationRule` and
`TestAuthorizationRuleNamesKnownRelation` cover the new service with no
edit, since they walk `flowseer.api.`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/model spec/proto/flowseer/api spec/proto/flowseer/event/operator spec/proto/flowseer/store/device test/conformance/proto src/services/device/internal/host src/services/device/test/integration deploy/lab/README.md`

### U2. Access store

Files: `src/services/device/internal/accessstore/store.go`, `src/services/device/internal/accessstore/store_test.go`
After: U1
Change: `accessstore.New(kv, now)` is the store of the three record kinds
over one `jetstream.KeyValue`, with the keys of the Decisions built through
`tenant.Validate` as `edgeRecordKey` builds its own
(`internal/edgestore/store.go:50-55`). It creates a member or a partner
only when absent and returns the stored one otherwise, mutates a member
under compare-and-set with the retry count `edgestore` uses, deletes
reporting whether a record was there, lists one tenant's members, roles,
and partners in key order, lists the tenant ids that hold a record, and
answers `FullPayloadActive(tenant, operator)` as true only for a grant
whose `expires_at` is after `now()`. `DeleteRole` refuses a role a member
names. Codes: `accessstore/store` (retryable), `accessstore/conflict`,
`accessstore/decode`, `accessstore/not-found`, `accessstore/role-assigned`.
Tests: against a hub's JetStream, as `internal/edgestore/store_test.go:23-39`
starts one, on a bucket the test creates itself with `CreateKeyValue`,
since `edgebus.AccessBucket` lands in U5. Every key the store writes starts with its tenant id, read from the raw
bucket. A list for tenant A never holds a record of B. Two concurrent
mutations of one member both land. Creating a stored member returns the
first `enrolled_at`. Deleting an absent record reports false and no error.
`FullPayloadActive` is false at `expires_at` exactly and for no grant. A
record that will not decode yields `accessstore/decode`, and a closed
connection `accessstore/store`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/accessstore`

### U3. Projector owns tenant, role, and platform relations

Files: `src/services/device/internal/projector/projector.go`, `src/services/device/internal/projector/reconcile.go`, `src/services/device/internal/projector/projector_test.go`, `src/services/device/internal/projector/export_test.go`
After: U1
Change: the projector declares `TenantSource` (`List`, which
`tenantstore.Store` has) and `AccessSource` (members, roles, and partners
of a tenant, one member, and the tenant ids holding a record), and takes
them, the platform admins' principal ids, and a clock through options that
leave `New`'s signature as it is. With an access source it owns the
relations of the Projection table, the edge and device grant relations
among them. `desiredTuples` gains `tenant`, `role`,
and `platform`, and the `requester` condition. A reconcile pass covers
every tenant holding a tenant record or an access record, every role, and
`platform:flowseer`, with the snapshot and scan flags the three landed
kinds use, so a failed source read leaves that kind's tuples alone.
`RepairedCounts` gains the three kinds. `SyncTenant` and `SyncRequester`
do what the Decisions say and return the first error. With no access
source nothing changes.
Tests: `TestProjectorOwnedRelations` gains one row per table line. With
an access source, one pass deletes a hand-written `tenant#admin@user`,
`edge#capture@user`, and `device#view@user`, and keeps `edge#tenant` and
a stored `tenant#member@user`. `TestReconcileDriftRestoration`, which has
no access source, keeps its `edge:E#capture@user:u` as it does today. A
grant past its `expires_at` on the test clock yields no `full_payload`
tuple and a pass deletes a stored one. A member naming a role with no
record yields no `assignee` tuple. A session whose requester has no member
record yields no `requester` tuple, and gains one after enrollment and a
pass. A member enrolled while a pass scans keeps its tuple, built as
`TestReconcileRaceCondition` is. With the access source failing, a pass
returns the error and deletes no `tenant` or `role` tuple.
`SyncTenant` after a deleted role removes `tenant#operator@role:R#assignee`.
A projector with no access source leaves `tenant:A#admin@user:x` in place.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/projector`

### U4. Tenant and tenant-admin handlers

Files: `src/services/device/internal/identityapi/tenant.go`, `src/services/device/internal/identityapi/admin.go`, `src/services/device/internal/identityapi/errors.go`, `src/services/device/internal/identityapi/identityapi_test.go`
After: U2
Change: package `identityapi` implements `TenantServiceHandler` and
`TenantAdminServiceHandler` over `tenantstore.Store`, `accessstore.Store`,
the list of configured issuers, a clock, and a `Projector` interface it
declares with `SyncTenant` and `SyncRequester`. Each handler reads the
principal from `authn.FromContext`, and each admin handler the tenant from
`tenant.FromContext`. The handlers apply the idempotency, order, and error
rules of the Decisions and stamp `enrolled_by`, `connected_by`, and
`granted_by` from the principal. `CreateTenant` mints the UUID and calls
`SyncTenant`. `errors.go` maps `accessstore` and `tenantstore` codes to
Connect codes through `connecterr`, with `identityapi/not-a-member`,
`identityapi/role-assigned`, `identityapi/unknown-role`, and
`identityapi/unknown-issuer` as its own.
Tests: handlers run over real stores on an in-process JetStream and a
recording projector, with the context `Interceptor.Admit` prepares, as
`internal/edgeapi/admin_test.go:66-83` does. The record and projection
halves of requirements 1, 5, 6, and 7: what is stored, what is refused,
and which projector calls follow.
Requirement 4 with the projector failing once: the answer is
`CodeUnavailable` and retryable, the record is gone, and the retry calls
`SyncTenant` and `SyncRequester` again and succeeds. `CreateTenant` with
the projector failing once answers `CodeUnavailable`, and the same request
again answers the same id after a second `SyncTenant`. `CreateRole` with
the projector failing answers the role. `GrantFullPayload` stores
`expires_at` as the test clock plus the lifetime, and a second grant
replaces it. `ListMembers` with `page_size: 2` over five members returns
each once.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/identityapi`

### U5. Trail: new actions, the platform subject, and the read stream

Files: `src/modules/edgebus/subjects.go`, `src/modules/edgebus/hub.go`, `src/modules/edgebus/edgebus_test.go`, `src/modules/edgebus/README.md`, `src/services/device/internal/actiontrail/interceptor.go`, `src/services/device/internal/actiontrail/actiontrail_test.go`, `src/services/device/README.md`, `docs/architecture/2026-09-30-operator-authorization-direction.md`
After: U1
Change: `edgebus` gains `AccessBucket` (`access`), created with the other
buckets, `OperatorReadStream`, and `OperatorReadSubject`. `createStores`
creates the read stream from `HubConfig.OperatorReadMaxBytes` (16 MiB when
zero) and `OperatorReadMaxPerSubject` (1,000 when zero). The interceptor's
table gains the eleven procedures of the Admin API table with the object
each names, and sends `EDGE_GET` and `EDGE_LIST` to the read subject. A
call with no tenant in its context whose procedure is `CreateTenant`
publishes under the `platform` token. Any other recorded call without a
tenant still answers `actiontrail/unprepared`. The completion fills the
object from the response where the table says so. Both READMEs name the
two streams, their subjects, and the sizing rule. The record gains a dated
amendment: the two streams and their limits, the read subject, the
platform token, and the eleven actions. Its 2026-10-03 sentence that a
flood evicts only records of its own action is corrected in place: the
cap per subject holds only below the stream's byte limit.
Tests: `TestOperatorActionTrailTable` gains a row per new procedure with
its subject, attempt object, and completion object. A test in the shape of
`TestEveryEdgeAdminProcedureIsRecorded` walks both identity service
descriptors and fails on a procedure that is neither in the table nor in
the list of five unrecorded reads. `CreateTenant` with the publish
refused answers `actiontrail/unavailable` and the handler does not run.
Requirement 10 is in `actiontrail_test.go`, outside the module, against a
hub started with `OperatorReadMaxBytes` of 1 MiB: `GetEdge` calls through
the interceptor fill the read stream past its limit, and an earlier
`IssueSetupKey` record is still in the action stream. In
`edgebus_test.go`, `TestOperatorActionStreamMaxPerSubject` moves its
`edge_get` subject to the read stream and its cap, an `IssueSetupKey`
attempt and completion with a 60-character issuer and a 36-character
subject store under 512 bytes each, read from the stream's byte count,
which pins the arithmetic the sizing rests on, and both streams and five
buckets exist under the default central budget.
`TestTheDirectionRecordCitationsAndDecisions`
(`src/services/device/test/integration/lab_fixtures_test.go`) resolves
every `path:line` the amendment cites and stays green.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/edgebus src/services/device/internal/actiontrail src/services/device/README.md src/services/device/test/integration docs/architecture/2026-09-30-operator-authorization-direction.md`

### U6. The host serves the services and enforces grant expiry

Files: `src/services/device/internal/host/serve.go`, `src/services/device/internal/host/host.go`, `src/services/device/internal/host/host_test.go`, `src/services/device/internal/captureapi/operator_service.go`, `src/services/device/internal/captureapi/operator_service_test.go`, `src/services/device/test/integration/fixture_test.go`, `src/services/device/test/integration/e2e_test.go`, `src/services/device/test/integration/capture_test.go`, `src/services/device/test/integration/lab_fixtures_test.go`, `src/services/device/README.md`, `deploy/lab/README.md`, `docs/runbooks/lab-icx7150-first-write.md`, `CONCEPTS.md`, `docs/architecture/2026-09-30-operator-authorization-direction.md`
After: U3, U4, U5
Change: the host opens the `access` bucket, builds the access store, and
passes it, the tenant store, the platform admins' principal ids, and
`time.Now` to the projector. It mounts both identity services on
`operatorInterceptors`. `OperatorServiceConfig` gains
`FullPayload func(ctx, tenant, operator) (bool, error)`, which the host
sets to `FullPayloadActive`. `CreateCaptureSession` calls it after
`authz.Require` and answers `PermissionDenied` with
`captureapi/full-payload-expired` on false, `Unavailable` on an error, and
`Unavailable` when it is unset, as it does for an unset `edgeTenant`
(`internal/captureapi/operator_service.go:111-113`). The trail records
that refusal as denied, as it does the one from `Require`. Fixtures that
stored `tenant#admin@user` in the fake engine use `Grant`, and tests that
create a full-payload capture through the service enroll the caller and
grant first. The README's Deployment section names the two services, the
bootstrap order (configuration, `CreateTenant`, role, enrollment), and the
answer for an object no record holds. The lab README and the runbook drop
the statement that no path creates a tenant record and their citation of
`TestTenantServiceIsNotMounted`, and name `CreateTenant`. The gates that
required that citation (`lab_fixtures_test.go:580-582`, `:653-655`)
require the mounted-services test in its place. `CONCEPTS.md` gains Member,
Role, Partner, and Platform admin. The record gains a dated amendment:
`TenantService` is served, the access records and the ownership table,
the removal order, expiry by record, and platform admins from
`subjects`. Its "Tenants, as landed" sentence on `NotFound` is corrected.
Tests: `TestTenantServiceIsNotMounted` becomes a test that `CreateTenant`
without a token answers `CodeUnauthenticated`, and
`TestHostMountsServicesOnTheCorrectInterceptorChains` lists both services.
Through the running service with the fake engine: requirement 2 after
`Options.Reconciled`, requirement 9 read from the streams, requirement
11's two hand-written tuples, and requirement 3's tuples, with the removed
user's `member` given through `Grant`. `operator_service_test.go` covers
requirement 8's expiry with a clock, the unset hook, and a failing hook.
Requirement 12 is `TestTheServiceStartsFromAFileAndAnswers`, unchanged.
`TestTheDirectionRecordCitationsAndDecisions` stays green over the new
amendment.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device deploy/lab/README.md docs/runbooks/lab-icx7150-first-write.md CONCEPTS.md docs/architecture/2026-09-30-operator-authorization-direction.md`

### U7. Lab run and the tagged tier through the API

Files: `deploy/lab/README.md`, `deploy/lab/central.textproto`, `docs/runbooks/lab-icx7150-first-write.md`, `src/services/device/test/integration/authz_enforcement_test.go`, `src/services/device/test/integration/lab_fixtures_test.go`, `src/services/device/test/integration/runbook_test.go`, `src/services/device/test/integration/README.md`
After: U6
Change: `central.textproto` gains a `platform_admin` block for the Dex
`admin` user. The lab README's step 5 creates the tenant with
`CreateTenant` under `ADMIN_TOKEN`, then creates an admin role, enrolls
alice, and assigns the role, each as a `curl` with its expected answer.
The hand-written OpenFGA write goes, since a pass would delete its
tuples. The runbook's tenant section points at that step. The text gates
in `lab_fixtures_test.go` and `runbook_test.go` hold the new step.
`TestEnforcementAgainstTheRealEngine` creates its tenants, members, roles,
and partner link through the API and writes no tuple itself.
Tests: against OpenFGA v1.21.0, requirements 1, 3, 7, and 8's grant by
their examples, the role of requirement 5 admitting `GetEdge` for its
assignee, and after requirement 3's removal a `Scan` that finds no tuple
whose user is `user:<U>`. Parent requirement 5 still holds: a token whose
claims omit A is denied while the member record stands.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- deploy/lab docs/runbooks/lab-icx7150-first-write.md src/services/device/test/integration`

Waves: U1 | U2 U3 U5 | U4 | U6 | U7

## Verification

```bash
go tool -modfile=tools/buf/go.mod buf lint
go test -race ./src/services/device/... ./src/modules/edgebus/... ./test/conformance/proto/...
go vet -tags=authz_integration ./src/services/device/test/integration/
go test -race -tags=authz_integration ./src/services/device/test/integration/
.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer src/services/device src/modules/edgebus test/conformance/proto deploy/lab docs/runbooks/lab-icx7150-first-write.md docs/architecture CONCEPTS.md
```

The tagged run needs Docker and runs the package whole. The run in
`deploy/lab/README.md` is done once by hand, from `CreateTenant` to a
`GetEdge` by alice.

## Definition of done

- [ ] Verifier green for every changed path, and the tagged run passes.
- [ ] `src/services/device/README.md`, `src/modules/edgebus/README.md`,
      the schema package READMEs, `deploy/lab/README.md`, the runbook,
      `CONCEPTS.md`, and the amended record updated in the change that
      invalidates them.
- [ ] This plan's `status` set with an outcome note under its title, and
      the parent's `Landed:` line for U4 filled.
- [ ] No plan labels in code.

## Open questions

- The change stream's 64 MiB is still shared by all tenants. A tenant
  admin who repeats changes can hold 10,000 records on each of 17
  subjects, about 70 MiB, and push out every other tenant's oldest
  records. A stream per tenant bounds that and reserves its bytes per
  tenant against the central budget, which caps the tenant count. It
  changes the record's subject-per-tenant design, so it is the plan
  owner's call and a plan of its own.
- If the plan's owner reads "define roles, and grant them" to include a
  grant on one edge or device, that is a follow-up plan with the model
  package Out of scope names.
- A deployment that wrote grants or memberships to OpenFGA by hand loses
  them at the first pass after U6. The lab in `deploy/lab` is the only one
  known, and U7 moves it to the API.
- Unmeasured: `SyncTenant` for a tenant with thousands of members, and
  `RemoveMember`, which reads every session of the tenant to find the
  member's.
- Unverified, carried from phase 3 for the implementer: a real `Aborted`
  from two concurrent writes of one tuple, and the duration of a pass at
  the benchmark fixture's size.

## Review gaps

- `src/services/device/internal/projector/reconcile.go:226`: replace the membership condition on the pass's requester tuple with `true`; fails: a session whose `tenant`, `edge`, and `requester` tuples are all stored keeps `requester` after a pass once the requester has no member record
- `src/services/device/internal/projector/projector.go:436`: drop `&& name != "admin"`; fails: a stored `Partner` holding `ADMIN` yields no `tenant#admin@tenant:<provider>#active_admin`
- `src/services/device/internal/projector/reconcile.go:38`: drop `relation == "partner"`, `relation == "platform"`, or `relation == "capturer"`; fails: a pass deletes a stored `tenant#partner`, `tenant#platform`, and `tenant#capturer` tuple no record explains
- `src/services/device/internal/authz/openfga/checker.go:490`: `isValidObject` returns `len(s) <= maxObjectBytes && isValidUser(s)`; fails: `Check`, a contextual tuple, and `Write` with object `role:r1#assignee` make no engine call
- `src/services/device/internal/authz/openfga/checker.go:508`: `strings.EqualFold(kind, "role")`; fails: user `Role:r1#assignee` is refused without a call
- `spec/proto/flowseer/api/identity/v1/tenant_admin_service.proto:138`: delete every rule on `CreateRoleRequest.name` and `relations`, or the `page_size` bounds at `:258`, `:281`, `:304`; fails: a request with no name, a 129-character name, no relation, a repeated relation, an undefined relation, or `page_size` 501 is refused
- `spec/proto/flowseer/model/identity/v1/access.proto:43`: drop `min_len` or `max_len` on `Role.name`, or on `FullPayloadGrant.reason` at `:69`; fails: an empty or 129-character name and an empty or 513-character reason are refused
- `spec/proto/flowseer/store/device/v1/service_config.proto:189`: drop the item `min_len` or `max_len` on `PlatformAdmin.subjects`; fails: an empty subject and a 257-character subject are refused
- `spec/proto/flowseer/event/operator/v1/operator_action_event.proto:103`: delete the `operator_action_partner.relations_exclude_admin` rule; fails: an `OperatorActionPartner` holding `ADMIN` is refused
- `src/services/device/internal/accessstore/store.go:244`: `return ids, nil`; fails: `TenantIDs` names a tenant holding two records once
- `src/services/device/internal/identityapi/admin.go:95`: set the description unconditionally, or the name at `tenant.go:49`; fails: `CreateRole` and `CreateTenant` without the optional field store a record that passes validation
- `src/services/device/internal/identityapi/errors.go:62`: keep only `tenantstore.ErrCodeStore` in the switch; fails: `accessstore/conflict` and `tenantstore/conflict` answer retryable
- `src/services/device/internal/connecterr/consistency_test.go:31`: map `tenant.ErrCodeNoTenant` to `CodeInvalidArgument` in `identityapi.ClientErrors`; fails: `TestNoCodeAnswersTwoDifferentThings` reads the identity table
- `spec/proto/flowseer/store/device/v1/README.md:88`: "sixteen distinct subject values;"; fails: `docs/doc-style.md`, no semicolons in prose
- `src/modules/edgebus/edgebus_test.go:1314`: "330 MiB is above the 320 MiB the sum was before the read stream"; fails: `docs/code-style.md`, a comment describes the code as it is
- `docs/architecture/2026-09-30-operator-authorization-direction.md:755`: cites `projector.go:90-132` for `Sync`; fails: those lines hold the `Projector` struct and `New`, and `Sync` is at `projector.go:147-198`
- `docs/architecture/2026-09-30-operator-authorization-direction.md:642`: the record names no user shape the adapter accepts; fails: it states that the adapter accepts `type:id`, `role:<id>#assignee`, and `tenant:<id>#active_admin`, and answers false without a call for every other user
- `deploy/lab/README.md:411`: step `9.` follows step `7.`; fails: the steps count 1 through 8
- `docs/architecture/2026-09-30-operator-authorization-direction.md:528`: a call whose context has ended answers `ctx.Err()` bare "in two places"; fails: `identityapi/errors.go` (`connectErr`) and `accessstore/store.go` (`storeError`) answer it too, and the 2026-10-04 amendment says so
