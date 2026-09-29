---
title: Operator Authorization Phase 2, Tenant Entity and Partitioned Stores - Plan
type: feat
date: 2026-09-28
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: implemented
review: rework
execution: mixed
parent: docs/plans/2026-09-28-2029-feat-operator-authorization-plan.md
---

# Operator Authorization Phase 2, Tenant Entity and Partitioned Stores - Plan

> Implemented. 10 units, 2026-09-28T20:08:45Z to 2026-09-29T21:10:37Z.

This plan is phase 2 of the operator authorization parent plan, following
phase 1 (`docs/plans/2026-09-28-2029-feat-operator-authorization-phase1-plan.md`,
landed in `e5418c45..c6811f4e`). It implements the tenant entity in the
identity leaf package `flowseer.model.identity.v1`, establishes central's
tenant store, partitions central's KeyValue stores and capture artifact
directories by tenant, updates the edgebus subject layout and `AuditStream`
to support multi-tenancy, and wires ambient tenancy into service handlers.

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
deployment's configuration, creates tenants. The means is a new protobuf
schema and central tenant store, key-partitioning across central's KeyValue
buckets and artifact directories, edgebus subject and stream updates, and
ambient tenant propagation in service handlers. Stop condition: this plan
is wrong if an amendment to
`docs/architecture/2026-09-28-operator-authorization-direction.md`
removes the requirement for multi-tenancy or moves tenant identity out of
`flowseer.model.identity.v1`.

## Decisions

The parent's Decisions and `docs/architecture/2026-09-28-operator-authorization-direction.md`
(accepted 2026-09-28) apply. These are this phase's own:

- The user decided on 2026-09-30 that the one-tenant-per-organization claim
  stays in this phase and follows prior art instead of a hand-built
  two-key protocol. The prior art is the multi-key conditional commit:
  etcd's `Txn` comparing each key's `CreateRevision` to 0, and DynamoDB's
  `TransactWriteItems` with a separate uniqueness item. NATS has the same
  primitive from server 2.12, atomic batch publish (ADR-50): a batch of
  messages commits to a stream all or none, and each message may carry
  `Nats-Expected-Last-Subject-Sequence` for its own subject. A KV bucket is
  a stream (`KV_tenants`, subjects `$KV.tenants.<key>`), and `go.mod` pins
  nats-server v2.14.6 and nats.go v1.53.1, whose `StreamConfig` has
  `AllowAtomicPublish`. So `Create` writes the tenant record and its `org_`
  index key in one atomic batch, each with an expected last subject
  sequence of 0 (or the delete marker's revision), on a bucket with
  `AllowAtomicPublish` set. Either both keys exist or neither does, so no
  rollback, stale-claim takeover, or ownership read remains. A retry that
  finds the keys present compares the stored config and reports success or
  `AlreadyExists`. Client side, `github.com/synadia-io/orbit.go/jetstreamext`
  (`PublishMsgBatch`) implements the ADR-50 headers; the ADR's header
  protocol over core nats.go is the alternative, which the re-plan weighs.

- The user decided on 2026-09-29 that the rework the review left open is a
  follow-up pass of this phase, not a move to phase 3. Its files widen to
  the edge leaf (`src/edge/agent/internal/busattach`,
  `src/modules/edgebus/leaf.go`) so that an edge enrolled under a UUID
  tenant publishes under that tenant. The pass takes every open item in the
  Review section, then implement and review run again.
- The user decided on 2026-09-29 that `TenantService` is not mounted until
  phase 3 authenticates callers. Tests and development create tenants
  through the tenant store; no unauthenticated caller can create a tenant
  or claim an organization.
- Edge leaf publication scopes under the tenant returned by AttachBus.
  Why: `busattach.Attach` (`src/edge/agent/internal/busattach/busattach.go:87-128`)
  receives concrete broker subjects from `AttachBus`
  (`src/services/device/internal/edgeapi/service.go:209-215`).
  `edgebus.TenantFromSubjects` (`src/modules/edgebus/subjects.go`) extracts
  and validates the tenant parameter from the subject prefix
  (`flowseer.<tenant>.edge.<edgeID>.>`). Setting `Tenant: tenant` in
  `edgebus.LeafConfig` (`src/modules/edgebus/leaf.go:26-70, 98-208`) ensures
  the leaf creates its local buffer stream and publishes OpenTelemetry signals
  under the assigned tenant. This ensures records forwarded to central match
  the edge's authoritative tenant and pass `belongsToEdge` in
  `src/modules/edgebus/forwarder.go:221-226`.
- TenantStore organization index claim is made clock-free via CAS commit after
  the primary record write.
  Why: In `src/services/device/internal/tenantstore/store.go:94-176`, writing
  the primary tenant record first (`s.kv.Create(recCtx, tenantID, data)`)
  establishes durable record ownership before claiming the secondary index.
  `Create` then attempts to commit the secondary index `org_<hash>` via
  compare-and-set. If the index key exists, `Create` reads the record of the
  tenant it points to. If an active tenant record exists, `Create` rolls back
  the newly written primary record (`s.kv.Delete(delCtx, tenantID, jetstream.LastRevision(recRev))`)
  and returns `ErrCodeAlreadyExists`. If the target tenant record does not exist
  (an orphaned index entry), `Create` takes over the index key via CAS
  `s.kv.Update(ctx, orgKey, []byte(tenantID), entry.Revision())`, retrying on
  revision conflict. Removing the `2*s.rollbackTimeout` window eliminates
  races against delayed claim acknowledgments. Rollback tests drive `Store`
  APIs directly (`src/services/device/internal/tenantstore/store_test.go`).
- Lowercase UUID validation for `dev_tenant` in deployment configuration.
  Why: `tenant.Validate` (`src/common/tenant/tenant.go:41-51`) accepts only
  canonical lowercase UUIDs or `DefaultTenant`. In
  `spec/proto/flowseer/store/device/v1/service_config.proto:69`, adding a regex
  pattern constraint
  `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$` prevents
  uppercase UUIDs from passing `host.LoadConfig` and halting central at
  startup during service initialization.
- Unindexed registry edge auto-bind is removed from `host.go`.
  Why: `src/services/device/internal/host/host.go:284-304` previously
  auto-indexed registry edges under `dev_tenant` on startup only to accommodate
  `host_test.go`. Per the landed shape established in Review, an edge's tenant
  has one authority: the `edge_<edgeID>` index in the `edges` bucket
  (`src/services/device/internal/edgestore/store.go:47, 169-176, 179-189`).
  Dropping this block enforces that all edges must be explicitly indexed via
  `CreateEdge` or enrollment; `host_test.go` seeds its test edge explicitly in
  the store via `host.Options.Hub`.
- `Hub.EdgeTenant` returns unknown (`"", false`) on a zero or uninitialized
  `Hub`.
  Why: In `src/modules/edgebus/hub.go:383-385`, `EdgeTenant` fell back to
  `DefaultTenant, true` when `h.server == nil && h.edges == nil && h.keys == nil`.
  Dropping this fallback ensures no component silently defaults to
  `DefaultTenant`. `forwarder_internal_test.go` configures edge accounts
  explicitly.
- `TenantService` is unmounted from central Connect listeners until Phase 3.
  Why: Per user ruling 2, `TenantServiceHandler` is removed from
  `src/services/device/internal/host/serve.go:149-157, 187-188, 321-336, 338-462`
  and the `tenantService` handler struct is removed. Tests (such as
  `src/services/device/test/integration/e2e_test.go:971-985`) and development
  environments seed tenants directly through `tenantstore.Store`.
- `PlatformAdmin` configuration message adds `organization_claim_name`.
  Why: `PlatformAdmin` in
  `spec/proto/flowseer/store/device/v1/service_config.proto:74-94` previously had
  only `organization` (the value), lacking the claim name field present in
  `TenantConfig` (`organization_claim_name`). Adding `string
  organization_claim_name = 4` aligns schema definitions for Phase 3 token claim
  matching.
- Capture artifact documentation paths reflect per-tenant directories.
  Why: `src/services/device/internal/captureapi/doc.go:6` and
  `src/services/device/README.md:224` are updated from
  `<StateDir>/captures/<session_id>.pcapng` to
  `<StateDir>/captures/<tenant_id>/<session_id>.pcapng`, matching the
  implementation in `src/services/device/internal/captureapi/store.go:108-112`.

- The tenant entity replaces `model/inventory/v1/tenant.proto`: both its
  `TenantRef` and `Tenant` messages go, and nothing imports either. The file
  `spec/proto/flowseer/model/inventory/v1/tenant.proto:1-17` is deleted.
  Why: `model/inventory/v1/tenant.proto` was a keyless placeholder sketch.
  `spec/proto/flowseer/model/identity/v1/README.md:15-18` noted that `TenantRef`
  and `Tenant` sat in `model/inventory/v1` until the tenant entity replaced
  them in `model/identity/v1`. As verified across `spec/proto/` and Go
  packages, no schema or production code imports the old messages. Per
  `AGENTS.md`, breaking changes improve overall design without legacy shims.
  Per `docs/solutions/architecture-patterns/a-package-rename-breaks-names-you-persisted-not-records-you-encoded.md`,
  moving a message between packages does not break serialized protobuf wire
  records, and no persisted record or runtime manifest in the repository
  records the old type name as string data.
- The `ENTITY_TYPE_TENANT` exception in `docs/conventions/protobuf.md` ends.
  Why: `docs/conventions/protobuf.md:80-84, 147-153` and
  `spec/proto/flowseer/model/inventory/v1/README.md:25-28, 76-78` established
  `ENTITY_TYPE_TENANT = 1` in `spec/proto/flowseer/model/inventory/v1/entity.proto:16`
  ahead of the tenant identity and store landing. Because central gains a
  durable tenant store in this phase that answers existence checks, the
  exception is no longer needed and is removed from the conventions document.
- The tenant entity lives in `spec/proto/flowseer/model/identity/v1/tenant.proto`
  with `TenantLocalRef` (UUID `id`), `TenantGlobalRef` (wrapping
  `TenantLocalRef`), `TenantLifecycle` enum (`ACTIVE = 1`, `SUSPENDED = 2`),
  `TenantConfig`, `TenantState`, `TenantEvent`, and `TenantRecord`.
  `TenantConfig` requires `issuer`, `organization_claim_name`, and
  `organization_claim_value`. Why: Follows `docs/conventions/protobuf.md`
  rules for top-level UUID entities, uniform ref pairs, and the
  Config/State/Event triad. `TenantConfig` validation enforces that issuer and
  organization claims are present and non-empty, satisfying Requirement 1.
  `model/identity/v1` remains a leaf package with no FlowSeer imports
  (`test/conformance/proto/layering_test.go:97`).
- Central stores tenants in a dedicated `tenants` KeyValue bucket in the
  central JetStream domain (`edgebus.TenantBucket = "tenants"`), managed by
  `src/services/device/internal/tenantstore`.
  Why: Central requires an authoritative, durable store for tenant records.
  Keeping tenants in a dedicated KV bucket provides compare-and-set updates
  and isolates tenant configuration from edge and device stores. Secondary
  index keys `org_<sha256(issuer + "\x00" + orgValue)>` enable O(1) resolution
  from token claims during authentication in Phase 3 without full-bucket scans.
  `tenantstore.Store.List` filters out `org_` index keys so callers receive
  only primary tenant records, matching the pattern in `edgestore.Store.Keys`.
- Central's `device-lanes`, `edges`, and `captures` buckets are partitioned
  by prefixing every key with `<tenantID>.`.
  Why: Today, keys are unpartitioned:
  - `device-lanes`: `src/services/device/internal/journal/journal.go:76-83`
    loads (`journal.go:102`) and mutates (`journal.go:139, 144`) by bare `deviceID`.
    `src/services/device/internal/deviceapi/watcher.go:30` watches bare `deviceID`.
    `src/services/device/internal/dispatchapi/relay.go:214-225` sweeps bare keys.
  - `edges`: `src/services/device/internal/edgestore/store.go:43-48` loads
    (`store.go:53`), lists (`store.go:148-161`), and mutates (`store.go:185, 190`)
    by bare `edgeID`. `src/services/device/internal/edgeapi/admin.go:142, 376, 402`
    calls the store with unpartitioned ids.
  - `captures`: `src/services/device/internal/captureapi/store.go:61-68` creates
    (`store.go:125`), loads (`store.go:138`), lists (`store.go:155`), mutates
    (`store.go:199`), and deletes (`store.go:240`) by bare `sessionID`.
  Formatting keys as `<tenantID>.<resourceID>` leverages NATS JetStream KV's
  native hierarchy where dots map to subject tokens (`$KV.<bucket>.<key>`).
  Because UUIDs contain hyphens and hex digits but no dots, splitting on `.`
  unambiguously isolates the tenant partition and resource identifier.
- Edge enrollment and assertion verification resolve the edge's tenant via
  central indexes without modifying edge assertion wire contracts.
  Why: An edge enrolling via `src/services/device/internal/edgeapi/enroll.go:32`
  carries only `setup_key` and does not know its tenant.
  `edgestore.Store.IndexSetupKey` (`store.go:120-132`) stores the tenant and
  edge association under `setupkey_<keyID>`. When enrolled, central records an
  index entry `edge_<edgeID>` pointing to `tenantID`.
  `src/services/device/internal/edge/verifier.go:55` and `store.go:73-83`
  (`Lookup`) resolve the edge's public key by looking up `edge_<edgeID>` to find
  the tenant, then loading `<tenantID>.<edgeID>`. This keeps tenancy ambient
  and prevents edge-facing assertion headers from needing self-asserted tenant
  fields.
- Capture artifact files live under per-tenant directories on disk:
  `<StateDir>/captures/<tenantID>/<sessionID>.pcapng`.
  Why: `src/services/device/internal/host/host.go:288` and
  `src/services/device/internal/captureapi/store.go:73, 92-97` construct
  artifact paths directly under `<StateDir>/captures`. Placing artifacts under
  `<capturesDir>/<tenantID>/` isolates tenant payload bytes on the filesystem.
  Per `docs/solutions/architecture-patterns/holding-a-secondary-file-store-to-a-swept-record-requires-in-memory-ownership.md`,
  in-memory writer ownership is tracked per session, directories are created on
  demand, and retention sweeps walk session records to prune per-tenant artifact
  files safely.
- Central's `AuditStream` subscribes to `flowseer.*.audit.device.>` with a
  wildcard in the tenant position instead of `DefaultTenant`.
  Why: `src/modules/edgebus/subjects.go:25-26` defines `AuditStream`, and
  `src/modules/edgebus/hub.go:337` filters on
  `fmt.Sprintf("flowseer.%s.audit.device.>", DefaultTenant)`. This drops any
  audit events emitted for other tenants. Subscribing to
  `flowseer.*.audit.device.>` ensures central captures `DeviceOperationEvent`
  emissions from all tenants, satisfying Requirement 3.
- `edgebus` subject generation uses the tenant ID parameter, and edge account
  tenancy is persisted across hub restarts.
  Why: `src/modules/edgebus/subjects.go:13-15` defines `const DefaultTenant = "default"`.
  `hub.go:459` and `keys.go:319` hardcode `EdgeSubtree(DefaultTenant, edgeID)`.
  `hub.go:359` hardcodes `Tenant() string { return DefaultTenant }`.
  `ensureEdgeAccount` is updated to record the edge's tenant in an
  `edge-<edgeID>.tenant` sidecar file alongside `edge-<edgeID>.nk`
  (`src/modules/edgebus/keys.go:64-76`). Per
  `docs/solutions/architecture-patterns/a-restarted-hub-must-re-attach-every-persisted-edge-account.md`,
  `StartHub` reads persisted edge accounts on startup and re-attaches each edge
  under its persisted tenant. `edgeAccount` in `hub.go:102` stores `tenant`, so
  `Hub.MintEdgeUser(ctx, edgeID)` mints user JWT permissions scoped to
  `flowseer.<tenant>.edge.<edgeID>.>` without changing its exported signature
  or breaking `edgeapi.BusMinter` (`src/services/device/internal/edgeapi/service.go:56-59`).
  `forwarder.go:225` verifies delivered telemetry against `f.hub.EdgeTenant(edgeID)`.
- Handlers read the tenant from an ambient context value populated from
  configuration until Phase 3 lands.
  Why: `src/common/tenant` provides `WithTenant(ctx, tenantID)` and
  `FromContext(ctx) (string, error)`. `DeviceService`, `EdgeAdminService`, and
  `CaptureService` handlers read the caller's tenant exclusively from
  `tenant.FromContext(ctx)` and never from request bodies. A Connect
  interceptor in `src/services/device/internal/host/serve.go` injects the
  configured development or test tenant into request contexts. In Phase 3,
  this interceptor is replaced by the OIDC token interceptor without requiring
  changes to service handlers.
- Platform admin configuration is added to `DeviceServiceConfig`, and
  `TenantService` schema is defined in `spec/proto`.
  Why: `spec/proto/flowseer/store/device/v1/service_config.proto:24` defines
  deployment configuration. Adding `PlatformAdmin platform_admin = 9` (with
  `issuer`, `organization`, and `subject`, and `organization_claim_name` in
  the follow-up pass) satisfies the Goal requirement that a platform admin
  named in deployment configuration creates tenants.
  `spec/proto/flowseer/api/identity/v1/tenant_service.proto` defines the
  Connect RPCs (`CreateTenant`, `GetTenant`, `ListTenants`). Per user ruling 2,
  the service is unmounted in central host until Phase 3 authenticates callers;
  tests and development seed tenants directly via `tenantstore.Store`.
- Units are sliced vertically by subsystem to preserve module-wide compilation.
  Why: In FlowSeer, `verify-change.sh` runs `go build ./...` across the entire
  module on every unit pass. Slicing horizontally between store method
  signatures and caller call-sites would leave callers broken across unit
  boundaries. Slicing vertically ensures each subsystem (edge domain in U4,
  device-lane domain in U5, capture domain and host in U6) updates its stores
  and callers atomically, keeping all intermediate states build-clean.

## Requirements

1. The tenant entity. Acceptance: a `TenantConfig` with an issuer and an
   organization claim validates, and one without an issuer fails.
   (Claims parent Requirement 1 tenant part: `Tenant` entity lives in
   `flowseer.model.identity.v1`, and `spec/proto/flowseer/model/inventory/v1/tenant.proto`
   is removed so that `grep -rn 'message OperatorRef\|message Tenant' spec/proto`
   prints only files under `model/identity/v1/`).
2. Partitioned keys. Acceptance: parent Requirement 4, with the tenant set
   from configuration instead of a token: an edge created by a tenant A caller
   is stored under a key that begins with A's id, and `GetEdge` for that id
   from a tenant B caller returns `NotFound`.
3. Every tenant's audit reaches central. Acceptance: with tenants A and B
   configured, a `DeviceOperationEvent` published on each tenant's audit
   subject is stored in central's `AuditStream`.

## Out of scope

- OIDC token verification and Connect authentication interceptor (phase 3).
- Zanzibar engine interface, SpiceDB adapter, and in-memory fake (phase 4).
- Relationship authorization checks on operator RPCs (phase 5).
- Operator action audit stream for administrative RPCs (phase 6).

## Units

### U1. Tenant entity in model/identity/v1 and schema conventions

Files: `spec/proto/flowseer/model/identity/v1/tenant.proto`,
`spec/proto/flowseer/model/identity/v1/README.md`,
`spec/proto/flowseer/model/inventory/v1/tenant.proto`,
`spec/proto/flowseer/model/inventory/v1/README.md`,
`spec/proto/flowseer/model/README.md`,
`docs/conventions/protobuf.md`,
`test/conformance/proto/layering_test.go`,
`test/conformance/proto/model_identity_rules_test.go`
After: none
Change:
- `spec/proto/flowseer/model/identity/v1/tenant.proto` declares
  `package flowseer.model.identity.v1` and defines `TenantLocalRef` (UUID `id`),
  `TenantGlobalRef` (`TenantLocalRef tenant = 1`), `TenantLifecycle` enum
  (`TENANT_LIFECYCLE_UNSPECIFIED = 0`, `TENANT_LIFECYCLE_ACTIVE = 1`,
  `TENANT_LIFECYCLE_SUSPENDED = 2`), `TenantConfig` (`TenantGlobalRef ref = 1`,
  `string issuer = 2`, `string organization_claim_name = 3`,
  `string organization_claim_value = 4`, optional `name` and `description`),
  `TenantState` (`TenantGlobalRef ref = 1`, `TenantLifecycle lifecycle = 2`,
  `google.protobuf.Timestamp created_at = 3`), `TenantEvent`
  (`TenantGlobalRef ref = 1`, `TenantLifecycle from = 2`, `TenantLifecycle to = 3`),
  and `TenantRecord` (`TenantConfig config = 1`, `TenantState state = 2`).
- `spec/proto/flowseer/model/inventory/v1/tenant.proto` is removed.
- `spec/proto/flowseer/model/identity/v1/README.md` removes the deliberately
  absent tenant entity note while preserving `Imported by: model/access`
  (until `api/identity` lands in U3).
- `spec/proto/flowseer/model/inventory/v1/README.md` updates Boundaries and
  removes references to `model/inventory/v1/tenant.proto`.
- `spec/proto/flowseer/model/README.md` updates `identity/v1/` description to
  "Operator and tenant identity."
- `docs/conventions/protobuf.md` removes the `ENTITY_TYPE_TENANT` exception
  note (`lines 80-84, 147-153`) and updates the ref pair section to cite
  `TenantLocalRef`/`TenantGlobalRef`.
- `test/conformance/proto/layering_test.go` updates doc comments for
  `model/identity` as an independent leaf (`nil` imports).
- `buf generate` regenerates Go bindings under
  `generated/go/proto/flowseer/model/identity/v1/`.
- `test/conformance/proto/model_identity_rules_test.go` provides protovalidate
  rule tests for `TenantConfig`, `TenantState`, and `TenantEvent`.
Tests:
- `TestModelIdentityRules` in `test/conformance/proto/model_identity_rules_test.go`
  proves that a `TenantConfig` with an issuer and organization claim validates,
  one without an issuer fails validation (Requirement 1 acceptance test), a
  `TenantLocalRef` with a non-UUID string fails, `TenantState` with unspecified
  lifecycle fails, and `TenantEvent` with `from == to` fails.
- `TestProtoReadmeImports` in `test/conformance/proto/layout_test.go` checks
  that README `Boundaries` lines match imports.
- `TestProtoLayering` in `test/conformance/proto/layering_test.go` checks that
  layering rules hold.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/model/identity/v1 spec/proto/flowseer/model/inventory/v1 spec/proto/flowseer/model/README.md docs/conventions/protobuf.md test/conformance/proto`

### U2. Edgebus multi-tenant subjects and AuditStream wildcard

Files: `src/modules/edgebus/subjects.go`,
`src/modules/edgebus/hub.go`,
`src/modules/edgebus/keys.go`,
`src/modules/edgebus/leaf.go`,
`src/modules/edgebus/forwarder.go`,
`src/modules/edgebus/edgebus_test.go`,
`src/modules/edgebus/attribution_test.go`,
`src/modules/edgebus/forwarder_internal_test.go`
After: none
Change:
- `src/modules/edgebus/subjects.go` defines `TenantBucket = "tenants"`.
  `EdgeSubtree(tenant, edgeID)` and `AuditSubject(tenant, deviceID)` use the
  provided `tenant` string.
- `src/modules/edgebus/hub.go` adds `TenantBucket` to `createBuckets`.
  `AuditStream` configuration sets `Subjects: []string{"flowseer.*.audit.device.>"}`
  (`hub.go:337`), subscribing with a wildcard in the tenant position.
- `src/modules/edgebus/keys.go`: on edge account creation, `ensureEdgeAccount`
  persists the edge's tenant in an `edge-<edgeID>.tenant` sidecar file beside
  `edge-<edgeID>.nk`. `persistedEdgeIDs` reads the sidecar (defaulting to
  `DefaultTenant` if absent), and `StartHub` re-attaches persisted edge accounts
  under their recorded tenant across hub restarts.
- `edgeAccount` in `hub.go:102` records `tenant string`. `Hub` exposes
  `EdgeTenant(edgeID string) string`.
- `Hub.MintEdgeUser(ctx, edgeID)` reads `ea.tenant` and calls
  `edgePermissions(ea.tenant, edgeID)`, preserving its exported signature and
  maintaining `edgeapi.BusMinter` interface compatibility.
  `Hub.AttachEdge(ctx, tenant, edgeID)` ensures the edge account is provisioned
  with its assigned tenant.
- `forwarder.go:225` verifies `belongsToEdge(f.hub.EdgeTenant(edgeID), edgeID, msg.Subject())`
  against the edge's actual tenant instead of a static `DefaultTenant`.
- `createEdgeStream` in `hub.go:459` roots the source stream at
  `EdgeSubtree(ea.tenant, edgeID)`.
Tests:
- `TestAuditStreamWildcardStoresEventsFromMultipleTenants` in
  `src/modules/edgebus/edgebus_test.go`: configures tenants A and B, publishes
  a `DeviceOperationEvent` on `AuditSubject("tenant-a", deviceID)` and
  `AuditSubject("tenant-b", deviceID)`, and asserts both records are stored
  and retrievable from `AuditStream` (Requirement 3 acceptance test).
- `TestMintEdgeUserPerTenant` in `src/modules/edgebus/edgebus_test.go`: verifies
  minted edge user JWT permissions are scoped to `flowseer.<tenant>.edge.<edgeID>.>`.
- `TestRestartedHubReattachesEdgeUnderPersistedTenant` in
  `src/modules/edgebus/edgebus_test.go`: verifies a restarted hub reloads
  `edge-<edgeID>.tenant` and re-attaches the edge under its persisted tenant.
- `attribution_test.go` and `forwarder_internal_test.go` pass with multi-tenant
  subject layouts.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/edgebus`

### U3. Central tenant store, platform admin config, and ambient context

Files: `spec/proto/flowseer/api/identity/v1/tenant_service.proto`,
`spec/proto/flowseer/api/identity/v1/README.md`,
`spec/proto/flowseer/model/identity/v1/README.md`,
`spec/proto/flowseer/store/device/v1/service_config.proto`,
`src/common/tenant/tenant.go`,
`src/common/tenant/tenant_test.go`,
`src/services/device/internal/tenantstore/store.go`,
`src/services/device/internal/tenantstore/store_test.go`,
`src/services/device/internal/host/config.go`,
`src/services/device/internal/host/config_test.go`,
`test/conformance/proto/layering_test.go`
After: U1
Change:
- `spec/proto/flowseer/api/identity/v1/tenant_service.proto` defines `TenantService`
  with RPCs `CreateTenant(CreateTenantRequest) returns (CreateTenantResponse)`,
  `GetTenant(GetTenantRequest) returns (GetTenantResponse)`, and
  `ListTenants(ListTenantsRequest) returns (ListTenantsResponse)`.
- `spec/proto/flowseer/api/identity/v1/README.md` documents boundaries
  (imports `model/identity/v1`).
- `spec/proto/flowseer/model/identity/v1/README.md` adds `api/identity` to
  `Imported by:`.
- `spec/proto/flowseer/store/device/v1/service_config.proto` adds
  `PlatformAdmin platform_admin = 9` to `DeviceServiceConfig` (with `issuer`,
  `organization`, and `subject`), and optional `string dev_tenant = 10` for
  test and development context injection.
- `src/common/tenant/tenant.go` implements `WithTenant(ctx, tenantID)` and
  `FromContext(ctx) (string, error)` (with `ErrCodeNoTenant = errs.NewCode("tenant/no-tenant")`).
- `src/services/device/internal/tenantstore/store.go` implements `Store` over the
  `tenants` KV bucket with `Create(ctx, config)`, `Get(ctx, tenantID)`,
  `LookupByOrg(ctx, issuer, orgClaimValue)`, `List(ctx)`, and
  `Mutate(ctx, tenantID, fn)`. Secondary index keys `org_<sha256(issuer + "\x00" + orgValue)>`
  map (issuer, organization claim) to tenant ID. `List` filters out `org_`
  index keys, returning only primary tenant records.
- `src/services/device/internal/host/config.go` exposes `PlatformAdmin()` and
  `DevTenant()`.
- `test/conformance/proto/layering_test.go` adds `"api/identity": {"model/identity"}`
  and permits `model/identity` in `store/device`.
- `buf generate` regenerates Go bindings under
  `generated/go/proto/flowseer/api/identity/v1/` and
  `generated/go/proto/flowseer/store/device/v1/`.
Tests:
- `src/common/tenant/tenant_test.go`: tests round-tripping tenant ID in context
  and error returns when tenant is absent.
- `src/services/device/internal/tenantstore/store_test.go`: tests creating
  tenants, getting by ID, lookup by organization claim value, filtering `org_`
  keys in `List`, duplicate prevention, and CAS mutation retry settlement.
- `src/services/device/internal/host/config_test.go`: tests parsing and
  validating `DeviceServiceConfig` with `platform_admin`.
- `TestProtoReadmeImports` in `test/conformance/proto/layout_test.go` checks
  updated `model/identity/v1` and new `api/identity/v1` READMEs.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/api/identity/v1 spec/proto/flowseer/model/identity/v1 spec/proto/flowseer/store/device/v1 src/common/tenant src/services/device/internal/tenantstore src/services/device/internal/host test/conformance/proto`

### U4. Edge partitioned store, setup keys, and EdgeAdminService / EdgeService

Files: `src/services/device/internal/edgestore/store.go`,
`src/services/device/internal/edgestore/store_test.go`,
`src/services/device/internal/edgeapi/admin.go`,
`src/services/device/internal/edgeapi/admin_test.go`,
`src/services/device/internal/edgeapi/enroll.go`,
`src/services/device/internal/edgeapi/service.go`,
`src/services/device/internal/edgeapi/service_test.go`,
`src/services/device/internal/edge/verifier.go`
After: U2, U3
Change:
- `src/services/device/internal/edgestore/store.go`:
  - Partitions edge keys in `edges` KV bucket as `<tenantID>.<edgeID>`.
  - `Get(ctx, tenantID, edgeID)`, `Mutate(ctx, tenantID, edgeID, fn)`, and
    `Keys(ctx, tenantID)` take `tenantID`.
  - `IndexSetupKey(ctx, keyID, tenantID, edgeID)` records `<tenantID>.<edgeID>`
    under `setupkey_<keyID>`. `EdgeForSetupKey` returns `(tenantID, edgeID, error)`.
  - Enrolling writes an edge index `edge_<edgeID>` pointing to `tenantID`.
  - `Lookup(ctx, edgeID)` resolves `tenantID` via `edge_<edgeID>`, then loads
    `<tenantID>.<edgeID>` to return the public key and lifecycle.
- `src/services/device/internal/edgeapi/admin.go`:
  - `CreateEdge`, `GetEdge`, `ListEdges`, `IssueSetupKey`, `RevokeSetupKey`,
    and `RetireEdge` read the ambient tenant from context via
    `tenant.FromContext(ctx)`.
  - `GetEdge` looking up an edge id belonging to another tenant returns
    `NotFound` (`edgeapi/not-found`).
  - `ListEdges` lists only the caller's tenant edges via `s.store.Keys(ctx, tenantID)`.
- `src/services/device/internal/edgeapi/enroll.go`:
  - `Enroll` uses `EdgeForSetupKey` to resolve `(tenantID, edgeID)`, records
    `edge_<edgeID>` in `edgestore`, and provisions the edge account on the bus
    via `s.bus.AttachEdge(ctx, tenantID, edgeID)`.
- `src/services/device/internal/edgeapi/service.go`:
  - `AttachBus` resolves the edge's tenant from `edgestore` and supplies
    `Subjects: edgebus.EdgePublishSubjects(tenantID, edgeID)`.
- `src/services/device/internal/edge/verifier.go`:
  - Verifier functions unchanged using `edgestore.Store.Lookup`.
Tests:
- `edgeapi/admin_test.go`: creates an edge under tenant A, asserts its key in
  `edges` bucket begins with A's id (`<tenantA>.<edgeID>`), and calls `GetEdge`
  with tenant B context, verifying it returns `NotFound` (Parent Requirement 4 /
  Phase 2 Requirement 2 acceptance test).
- `edgestore/store_test.go`: proves that an edge stored under tenant A cannot
  be retrieved with tenant B, and `Lookup` resolves enrolled edges via index.
- `edgeapi/service_test.go`: tests enrollment and bus attachment with
  tenant-scoped subjects.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/edgestore src/services/device/internal/edgeapi src/services/device/internal/edge`

### U5. Device-lane partitioned store, DeviceService, DispatchService, Drift, and Audit

Files: `src/services/device/internal/journal/journal.go`,
`src/services/device/internal/journal/journal_test.go`,
`src/services/device/internal/journal/resolve_test.go`,
`src/services/device/internal/journal/terminator_test.go`,
`src/services/device/internal/deviceapi/service.go`,
`src/services/device/internal/deviceapi/deviceapi_test.go`,
`src/services/device/internal/deviceapi/apply.go`,
`src/services/device/internal/deviceapi/read.go`,
`src/services/device/internal/deviceapi/resolve.go`,
`src/services/device/internal/deviceapi/status.go`,
`src/services/device/internal/deviceapi/orphans.go`,
`src/services/device/internal/deviceapi/watcher.go`,
`src/services/device/internal/deviceapi/watcher_internal_test.go`,
`src/services/device/internal/dispatchapi/service.go`,
`src/services/device/internal/dispatchapi/relay.go`,
`src/services/device/internal/dispatchapi/relay_test.go`,
`src/services/device/internal/dispatchapi/report_test.go`,
`src/services/device/internal/drift/drift.go`,
`src/services/device/internal/drift/drift_test.go`,
`src/services/device/internal/auditapi/service.go`,
`src/services/device/internal/auditapi/service_test.go`,
`src/services/device/internal/centralaudit/centralaudit.go`,
`src/services/device/internal/centralaudit/centralaudit_test.go`
After: U2, U3
Change:
- `src/services/device/internal/journal/journal.go`:
  - Partitions lane keys in `device-lanes` bucket as `<tenantID>.<deviceID>`.
  - `Record`, `load`, and `mutate` take `tenantID, deviceID`.
  - `SweepExpiredReads(ctx, tenantID, deviceID, now, errPayload)` sweeps under
    `<tenantID>.<deviceID>`.
- `src/services/device/internal/deviceapi/`:
  - `apply.go`, `read.go`, `resolve.go`, `status.go`, and `orphans.go` read
    the caller's tenant from context via `tenant.FromContext(ctx)` and pass
    `tenantID` to `journal` methods.
  - `KVWatcher.Watch(ctx, tenantID, deviceID)` in `watcher.go` watches
    `<tenantID>.<deviceID>`.
- `src/services/device/internal/dispatchapi/`:
  - `Subscribe` and report handling in `service.go` resolve the delivering
    edge's tenant via `hub.EdgeTenant(edgeID)` and pass `tenantID` to `journal`.
  - `RunSweeper` in `relay.go` iterates bucket keys, splits on `.`, extracts
    `tenantID` and `deviceID`, and invokes `SweepExpiredReads(ctx, tenantID, deviceID, ...)`.
- `src/services/device/internal/drift/`:
  - `drift.go` resolves device tenant via the hosting edge (`hub.EdgeTenant(edgeID)`)
    and passes `tenantID` to `Journal.Record`, `Admit`, and `OpenRead`.
- `src/services/device/internal/auditapi/service.go`:
  - `Deliver` resolves the delivering edge's tenant via `hub.EdgeTenant(edgeID)`
    and publishes to `edgebus.AuditSubject(tenantID, deviceID)`.
- `src/services/device/internal/centralaudit/centralaudit.go`:
  - `Emitter` accepts `tenantID` on emit calls and writes records to
    `edgebus.AuditSubject(tenantID, deviceID)`.
Tests:
- `deviceapi/deviceapi_test.go`: verifies that tenant A cannot read, admit, or
  mutate device lanes belonging to tenant B.
- `journal/journal_test.go`, `resolve_test.go`, `terminator_test.go`: verify
  lane records for tenant A and tenant B with identical device IDs are
  partitioned and isolated under `<tenantID>.<deviceID>`.
- `deviceapi/watcher_internal_test.go`: tests watcher wakeups on partitioned
  keys.
- `dispatchapi/relay_test.go`, `report_test.go`: verify sweeper and report
  handling work with partitioned device keys.
- `drift/drift_test.go`: verifies drift detection under partitioned tenant keys.
- `auditapi/service_test.go` and `centralaudit/centralaudit_test.go`: verify
  events are published to tenant-specific audit subjects.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/journal src/services/device/internal/deviceapi src/services/device/internal/dispatchapi src/services/device/internal/drift src/services/device/internal/auditapi src/services/device/internal/centralaudit`

### U6. Capture partitioned store, per-tenant artifact directories, host assembly, and integration tests

Files: `src/services/device/internal/captureapi/store.go`,
`src/services/device/internal/captureapi/store_test.go`,
`src/services/device/internal/captureapi/artifact_invariant_internal_test.go`,
`src/services/device/internal/captureapi/operator_service.go`,
`src/services/device/internal/captureapi/operator_service_test.go`,
`src/services/device/internal/captureapi/edge_service.go`,
`src/services/device/internal/captureapi/edge_service_test.go`,
`src/services/device/internal/host/host.go`,
`src/services/device/internal/host/serve.go`,
`src/services/device/test/integration/e2e_test.go`,
`src/services/device/test/integration/capture_test.go`
After: U4, U5
Change:
- `src/services/device/internal/captureapi/store.go`:
  - Partitions capture keys in `captures` bucket as `<tenantID>.<sessionID>`.
  - `CreateSession`, `Session`, `MutateSession`, and `DeleteSession` take
    `tenantID, sessionID`.
  - `ListSessions(ctx, tenantID)` returns sessions matching `<tenantID>.`.
  - `artifactPath(tenantID, sessionID)` constructs
    `filepath.Join(s.capturesDir, tenantID, sessionID+".pcapng")` and ensures
    `<capturesDir>/<tenantID>` exists with `0o700` permissions.
  - Writer registry and deletion tombstones track `tenantID` and `sessionID`.
  - `SweepExpired(ctx)` sweeps across tenant directories, unlinking expired
    artifact files.
- `src/services/device/internal/captureapi/operator_service.go`:
  - `CreateCaptureSession`, `GetCaptureSession`, `ListCaptureSessions`,
    `StopCaptureSession`, `DeleteCaptureSession`, and `DownloadCaptureSession`
    read `tenantID` from context via `tenant.FromContext(ctx)` and scope store
    and artifact reads to `tenantID`.
- `src/services/device/internal/captureapi/edge_service.go`:
  - `UploadCapture` and `SubscribeCaptureAssignments` resolve the calling edge's
    tenant via `hub.EdgeTenant(edgeID)` and pass `tenantID` to store calls.
- `src/services/device/internal/captureapi/artifact_invariant_internal_test.go`:
  - Updates sequence generation to verify in-memory ownership and disk
    synchronization across per-tenant directories.
- `src/services/device/internal/host/serve.go` & `host.go`:
  - Adds a Connect interceptor setting `tenant.WithTenant(ctx, ...)` from
    configuration in development and test environments.
  - Mounts `TenantServiceHandler` guarded by the configured `platform_admin`.
  - Wires `tenantstore.Store` and connects partitioned stores into service
    constructors.
Tests:
- `captureapi/operator_service_test.go`: verifies tenant A cannot get, list,
  stop, or download a capture session belonging to tenant B.
- `captureapi/edge_service_test.go`: tests packet upload and artifact
  finalization under the edge's tenant directory.
- `captureapi/store_test.go`: proves that capture sessions and artifact paths
  are isolated by tenant, and files are stored in `<capturesDir>/<tenantID>/`.
- `captureapi/artifact_invariant_internal_test.go`: holds the 729-sequence
  invariant across dual stores with per-tenant artifact directories.
- `test/integration/e2e_test.go`: end-to-end integration test verifying
  multi-tenant isolation, partitioned buckets, and edge bus connection with
  tenant token.
- `test/integration/capture_test.go`: integration test verifying that capture
  artifacts are stored in per-tenant directories and download access is
  isolated across tenants.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/captureapi src/services/device/internal/host src/services/device/test/integration`

Waves (landed): U1 U2 | U3 | U4 U5 | U6

### U7. PlatformAdmin organization claim name, dev_tenant lowercase UUID validation, and capture documentation paths

Files: `spec/proto/flowseer/store/device/v1/service_config.proto`,
`src/services/device/internal/host/config.go`,
`src/services/device/internal/host/config_test.go`,
`src/services/device/internal/captureapi/doc.go`,
`src/services/device/README.md`
After: none
Change:
- `spec/proto/flowseer/store/device/v1/service_config.proto:69` tightens the
  `dev_tenant` constraint with a regex pattern rule
  `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$` enforcing
  canonical lowercase UUIDs, ensuring uppercase UUIDs fail schema validation
  during configuration loading.
- `spec/proto/flowseer/store/device/v1/service_config.proto:74-94` adds
  `string organization_claim_name = 4 [(buf.validate.field).required = true, (buf.validate.field).string.min_len = 1, (buf.validate.field).string.max_len = 128]`
  to `PlatformAdmin`, matching `TenantConfig.organization_claim_name` in
  `spec/proto/flowseer/model/identity/v1/tenant.proto:47-51`.
- `buf generate` regenerates Go bindings under
  `generated/go/proto/flowseer/store/device/v1/`.
- `src/services/device/internal/host/config.go:175-179` exposes
  `PlatformAdmin()` returning the updated message.
- `src/services/device/internal/captureapi/doc.go:6` updates the capture
  storage location documentation from `<StateDir>/captures/<session_id>.pcapng`
  to `<StateDir>/captures/<tenant_id>/<session_id>.pcapng`.
- `src/services/device/README.md:224` updates the capture payload path
  documentation from `<StateDir>/captures/<session_id>.pcapng` to
  `<StateDir>/captures/<tenant_id>/<session_id>.pcapng`.
Tests:
- `TestPlatformAdminAndDevTenant` in
  `src/services/device/internal/host/config_test.go:234-286`:
  - Asserts `platform_admin` parses and validates
    `organization_claim_name: "org_id"`.
  - Asserts `platform_admin` missing `organization_claim_name` fails
    validation with `host.ErrCodeConfigInvalid`.
  - Asserts an uppercase UUID for `dev_tenant` (such as
    `"0192E6A0-0000-7000-8000-000000000001"`) fails validation with
    `host.ErrCodeConfigInvalid`, aligning schema validation with
    `tenant.Validate`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/store/device/v1 src/services/device/internal/host/config.go src/services/device/internal/host/config_test.go src/services/device/internal/captureapi/doc.go src/services/device/README.md`

### U8. Tenant store clock-free organization index claim and Store-driven rollback

Files: `src/services/device/internal/tenantstore/store.go`,
`src/services/device/internal/tenantstore/store_test.go`
After: none
Change:
- `src/services/device/internal/tenantstore/store.go:94-176`:
  - `Store.Create` writes the primary tenant record to the `tenants` KV bucket
    first (`recRev, err := s.kv.Create(recCtx, tenantID, data)`). If `tenantID`
    already exists, it returns `ErrCodeAlreadyExists`.
  - After the primary record write, `Create` commits the secondary
    organization index key `org_<sha256(issuer + "\x00" + orgValue)>` using
    compare-and-set:
    - Calls `s.kv.Create(ctx, orgKey, []byte(tenantID))`.
    - If `s.kv.Create` returns `jetstream.ErrKeyExists`, reads the existing
      entry (`s.kv.Get(ctx, orgKey)`).
    - Checks whether the tenant ID named by the existing entry has a valid,
      committed record matching the issuer and organization via
      `s.Get(ctx, existingTenantID)`.
    - If a valid matching tenant record exists, the organization is
      legitimately claimed by another tenant: `Create` rolls back the newly
      written primary record (`s.kv.Delete(delCtx, tenantID, jetstream.LastRevision(recRev))`)
      and returns `ErrCodeAlreadyExists`.
    - If no valid record exists (an orphaned index entry from an incomplete or
      deleted registration), takes over the index key via CAS
      `s.kv.Update(ctx, orgKey, []byte(tenantID), entry.Revision())`. If the
      update fails with revision mismatch due to a concurrent write, retries
      the CAS loop.
    - If the index commit fails due to unrecoverable error or context deadline,
      deletes the primary record (`s.kv.Delete(delCtx, tenantID, jetstream.LastRevision(recRev))`)
      and returns the joined error.
  - Drops the time bound (`time.Since(entry.Created()) <= 2*s.rollbackTimeout`)
    and `ErrCodeConflict` from `Create` entirely, making organization claim
    takeover clock-free.
- `src/services/device/internal/tenantstore/store_test.go`:
  - Removes clock-dependent assertions and sleeps in
    `TestOrphanedOrgIndexTakeover` (`lines 357-391`) and removes
    `TestConcurrentClaimWithinBoundRefused` (`lines 393-423`).
  - Rewrites `TestRollbackDoesNotDeleteNewerClaim` (`lines 425-455`) and adds
    rollback test cases that drive `Store` APIs (`s.Create`, `s.Get`) directly
    rather than raw KV operations:
    - Proves that when `Create` fails because the organization is already owned
      by an existing active tenant, the new tenant's primary record is deleted
      from the store (`s.Get(ctx, newID)` returns nil).
    - Proves that an orphaned index entry (an `org_` key pointing to a
      non-existent tenant ID) is immediately taken over by a new `Create`
      without sleeping or waiting for a clock expiration.
    - Proves concurrent `Create` calls competing for the same uncommitted
      organization settle cleanly via CAS without leaving orphaned primary
      records.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/tenantstore`

### U9. Edge leaf tenant publication and zero-hub edge tenant resolution

Files: `src/modules/edgebus/subjects.go`,
`src/modules/edgebus/leaf.go`,
`src/modules/edgebus/hub.go`,
`src/modules/edgebus/edgebus_test.go`,
`src/modules/edgebus/forwarder_internal_test.go`,
`src/edge/agent/internal/busattach/busattach.go`,
`src/edge/agent/internal/busattach/busattach_test.go`
After: none
Change:
- `src/modules/edgebus/subjects.go`:
  - Adds `TenantFromSubject(subject string) (string, error)` and
    `TenantFromSubjects(subjects map[string]string) (string, error)` to parse
    the tenant identifier out of `flowseer.<tenant>.edge.<edgeID>...` concrete
    subjects returned by `AttachBus`, validating each token with
    `tenant.Validate(tenantID)`.
- `src/modules/edgebus/leaf.go:26-70, 98-208`:
  - In `StartLeaf`, validates `cfg.Tenant` using `tenant.Validate(cfg.Tenant)`.
    If `cfg.Tenant` is empty, defaults to `DefaultTenant`.
  - The leaf creates its local buffer stream under
    `flowseer.<tenant>.edge.<edgeID>.>` and publishes OpenTelemetry signals
    under `OTelSubject(cfg.Tenant, cfg.EdgeID, signal)`.
- `src/edge/agent/internal/busattach/busattach.go:87-128`:
  - `Attach` inspects `response.Msg.GetSubjects()`, extracts the tenant via
    `edgebus.TenantFromSubjects`, and passes `Tenant: tenant` in
    `edgebus.LeafConfig`. If subject parsing fails, returns an error coded with
    `ErrCodeAttach`.
  - An edge enrolled under a UUID tenant creates its leaf buffer stream under
    `flowseer.<uuid>.edge.<edgeID>.>` and publishes telemetry on subjects
    matching its assigned tenant, ensuring hub forwarder acceptance.
- `src/modules/edgebus/hub.go:366-387`:
  - In `Hub.EdgeTenant(edgeID string) (string, bool)`, removes lines 383-385
    (`if h.server == nil && h.edges == nil && h.keys == nil { return DefaultTenant, true }`).
    A zero or uninitialized `Hub` returns `"", false` for all edge lookups.
- `src/modules/edgebus/forwarder_internal_test.go`:
  - Updates test forwarder setup (`lines 85-93`) to configure attached edge
    accounts on the fake hub with `DefaultTenant` rather than relying on
    zero-Hub fallback.
- `src/modules/edgebus/edgebus_test.go`:
  - Adds `TestLeafPublishesUnderAssignedTenant`: proves that a leaf configured
    with a UUID tenant publishes telemetry under
    `flowseer.<uuid>.edge.<edgeID>.otel.metrics` and the hub forwarder accepts
    it without refusal.
  - Adds `TestZeroHubEdgeTenantReturnsUnknown`: proves that
    `(&Hub{}).EdgeTenant("edge-1")` returns `"", false`.
- `src/edge/agent/internal/busattach/busattach_test.go`:
  - Adds `TestAttachConfiguresLeafWithTenantFromAttachBusSubjects`: verifies
    `busattach.Attach` with an `AttachBusResponse` carrying UUID-scoped
    subjects configures the leaf with that UUID tenant and publishes under that
    tenant.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/edgebus src/edge/agent/internal/busattach`

### U10. Host TenantService unmounting, registry edge binding removal, and e2e fixture alignment

Files: `src/services/device/internal/host/host.go`,
`src/services/device/internal/host/host_test.go`,
`src/services/device/internal/host/serve.go`,
`src/services/device/test/integration/e2e_test.go`
After: U7, U8, U9
Change:
- `src/services/device/internal/host/serve.go`:
  - Removes `serve.go:149-157` (`tenantsKV`, `tenantStore`, `tenantSvc := &tenantService{...}`).
  - Removes `TenantService` mounting (`lines 187-188`:
    `tenantPath, tenantHandler := identityv1connect.NewTenantServiceHandler(...)`).
  - Removes `serve.go:321-336` (`tenantErrors`, `errCodeAdminNotConfigured`,
    and tenant error wrapping functions).
  - Removes `serve.go:338-462` (the full `tenantService` handler struct and
    RPC methods `CreateTenant`, `GetTenant`, and `ListTenants`).
  - Cleans up unused imports (`apiidentityv1`, `identityv1connect`).
  - `TenantService` is no longer served on the Connect API mux, satisfying user
    ruling 2 that unauthenticated callers cannot create tenants or claim
    organizations.
- `src/services/device/internal/host/host.go:284-304`:
  - Removes the block auto-binding an unindexed registry edge to the
    development tenant at startup (`if edgeID := h.registry.EdgeID(); edgeID != "" ... edgeStore.IndexEdge(ctx, edgeID, devTenant)`).
  - Edges must have an authoritative index entry in the `edges` bucket created
    through `CreateEdge` or enrollment; startup no longer creates synthetic
    index entries for unindexed registry edges.
- `src/services/device/internal/host/host_test.go:163-168`:
  - Updates `runningServiceWithControl` to pass `Hub: func(hub *edgebus.Hub)` in
    `host.Options`, opening the `edges` KV bucket and calling
    `edgestore.New(edgesKV).IndexEdge(ctx, testEdgeID, edgebus.DefaultTenant)`.
  - Ensures `TestAListedDeviceIsAnsweredFromTheJournal` tests against an
    authoritatively indexed edge without host startup auto-binding.
- `src/services/device/test/integration/e2e_test.go`:
  - Updates `e2e_test.go:890-894` to include `organization_claim_name: "org_id"`
    in the `platform_admin` textproto fixture, satisfying U7's required schema
    validation on `centralhost.LoadConfig`.
  - In `TestE2EMultiTenantPartitioning` (`lines 971-985`), replaces the call to
    `tenantClient.CreateTenant` with direct tenant creation via
    `tenantstore.New(kvTenant).Create(...)`.
  - Verifies multi-tenant bucket partitioning, edge enrollment, and journal
    operations with the tenant seeded directly through the store.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/host src/services/device/test/integration`

Waves: U7 U8 U9 | U10

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/model/identity/v1 spec/proto/flowseer/api/identity/v1 spec/proto/flowseer/model/inventory/v1 spec/proto/flowseer/model/README.md spec/proto/flowseer/store/device/v1 src/modules/edgebus src/edge/agent/internal/busattach src/common/tenant src/services/device test/conformance/proto docs/conventions/protobuf.md
go test -race ./src/modules/edgebus/... ./src/edge/agent/internal/busattach/... ./src/common/tenant/... ./src/services/device/... ./test/conformance/proto/...
```

Run targeted verification on these paths, never `--full`. Integration tests
under `src/services/device/test/integration` use Docker; run them where Docker
is reachable.

## Definition of done

- [x] Verifier green for every changed path across U1–U6.
- [x] `TenantLocalRef`, `TenantGlobalRef`, `TenantConfig`, `TenantState`, `TenantEvent`, and `TenantRecord` live in `model/identity/v1`.
- [x] `spec/proto/flowseer/model/inventory/v1/tenant.proto` is deleted and `generated/` is regenerated.
- [x] `docs/conventions/protobuf.md` has lost the `ENTITY_TYPE_TENANT` exception.
- [x] Requirements 1, 2, and 3 hold by their acceptance tests.
- [x] Parent U2's `Landed:` line filled.
- [x] Verifier green for every changed path across follow-up units U7–U10.
- [x] Edge leaf attaches with concrete tenant from `AttachBus` and publishes under assigned tenant.
- [x] Organization index claim in `tenantstore.Store.Create` is clock-free with CAS commit after record write.
- [x] Rollback tests drive `tenantstore.Store` directly.
- [x] `PlatformAdmin` schema defines `organization_claim_name` and `dev_tenant` rejects uppercase UUIDs.
- [x] `host.go` contains no synthetic registry edge index auto-bind.
- [x] `edgebus.Hub.EdgeTenant` never returns `DefaultTenant` on a zero Hub.
- [x] `TenantService` is unmounted from central host until phase 3.
- [x] Capture artifact path documentation reflects per-tenant directory layout.
- [x] This plan's `status` set with an outcome note under its title.
- [x] No plan labels in code.

## Review

Verdict: rework (2026-09-30). This was the second review, over
`a813410a..4451c64c` with its weight on follow-up units U7–U10. It ran two
rounds with one fix round (`74bf2f06`, `38ad1659`, merged at `241253f5`
and `cdbce3f7`). The first review ran three rounds over `a813410a..72427348`
with fixes `f688cb7a..34b6638c`.

The fixes changed the landed shape in ways a re-plan starts from:

- An edge's tenant has one authority, the `edge_<edgeID>` index in the
  `edges` bucket. `CreateEdge` writes it, and Enroll writes the same value.
  Dispatch, drift, audit delivery, capture, AttachBus, and edge lane reads
  resolve through it and refuse an edge without one. Nothing substitutes
  the default tenant for a tenant it could not resolve: central startup
  indexes no registry edge, `Hub.EdgeTenant` answers unknown on a zero
  `Hub`, and `StartLeaf` refuses an empty tenant.
- The edge agent takes its leaf tenant from the AttachBus subjects, which
  must all name one valid tenant and the edge's own id.
- A device lane is keyed by its hosting edge's tenant, not by the caller's.
  A caller whose tenant is not that edge's gets `NotFound`.
  `CreateCaptureSession` refuses an edge the caller's tenant does not own.
- A tenant token is a lowercase canonical UUID or `default` wherever a key,
  subject, or path is built, and `dev_tenant` must be a lowercase UUID.
- `TenantService` is defined but not served; a host test fails if it is
  mounted. `PlatformAdmin` carries the organization claim name. Nothing
  reads `platform_admin` or the tenant store in production yet.
- A tenant record counts as committed only while the `org_` index names it.
  `Get`, `List`, and `LookupByOrg` hide any other record, and `Mutate`
  neither creates a record nor changes its organization binding.
- State written before this change is not read: unprefixed keys in
  `device-lanes`, `edges`, and `captures`, capture files directly under
  `<StateDir>/captures/`, and edge accounts without an
  `edge-<edgeID>.tenant` file, which the hub skips on start.

Closed by this review: the edge leaf publishes under its assigned tenant,
the dev tenant rejects upper case, the registry edge auto-bind and the
zero-`Hub` default are gone, `TenantService` is unmounted and a test holds
it there, `PlatformAdmin` has its claim name, and the capture path docs
match the code.

Open, and the reason for the verdict:

- The organization claim in `tenantstore.Create` has had five review
  rounds without a clean one: three with a time-bound claim, then two with
  record-first and a compare-and-set index. The second of the latest two
  found defects in the first's fix, so per the review skill the mechanism
  goes back to `plan` instead of another patch. What the rounds established:
  - A retried `Create` of a committed tenant resumes with the committed
    record's revision. If the index read then fails (the caller's context
    expiring is enough), the revision-guarded rollback deletes the
    committed tenant (`store.go:198-205`), and the unknown-outcome
    rollbacks after a failed reconcile read do the same.
  - Two concurrent `Create` calls with one id share a record revision, so a
    cancelled one's rollback deletes the record the other reported as
    created.
  - "The index names me" is a read, not a compare-and-set. A competitor
    holding the index's old revision can still take it over after `Create`
    returned success.
  - The property test (`ownership_test.go`) injects no `Get` or `Keys`
    faults, faults only the first matching call, never retries a failed
    `Create`, and has no same-id concurrency. Its visibility check holds by
    construction. A re-plan should give the claim a state space that
    covers every KV call and fault point, retries, and same-id contention,
    and should decide whether same-id `Create` resumes at all.
- `spec/proto/flowseer/edge/attach/v1/bus.proto` says an empty `subjects`
  map is valid, but the edge agent now needs the map to learn its tenant.
  Central always sends three subjects, so nothing fails today. An explicit
  tenant field on `AttachBusResponse` would remove the inference. That file
  is outside this change.
- These docs still describe the one-tenant state and are outside this
  change: `src/modules/edgebus/README.md` (the tenant token "carries no
  broker enforcement"; a restart "re-attaches every edge"),
  `spec/proto/flowseer/store/device/v1/README.md` ("A tenant" listed as
  absent), `docs/solutions/architecture-patterns/a-restarted-hub-must-re-attach-every-persisted-edge-account.md`,
  and the capture path in
  `docs/architecture/2026-09-09-remote-packet-capture-direction.md`.
- Low: `src/services/device/README.md` cites `serve.go:68-76` in prose.
  A comment in `host_test.go` narrates when authentication lands.
  `TestStartLeafRefusesEmptyAndInvalidTenant` does not show that validation
  runs before the state directory is written.
  `TestStartupDoesNotIndexUnindexedRegistryEdge` does not wait for the host
  to stop.
- Plan text that no longer matches the code: U8 and the tenant store
  Decision say an existing id returns `ErrCodeAlreadyExists`, but the code
  resumes it. U10 and a Decision name `TestE2EMultiTenantPartitioning`, but
  the test is `TestMultiTenantIsolationAndEdgeBusPartitioning`. U2 says a
  missing tenant sidecar defaults to `default`, but the hub skips the edge.
  Requirement 1's grep also prints two vendor protos under `spec/proto/ruckus/`.

## Open questions

None.
