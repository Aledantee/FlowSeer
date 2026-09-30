---
title: Operator Authorization Phase 2, Tenant Entity and Partitioned Stores - Plan
type: feat
date: 2026-09-28
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept after fixes
compound: docs/solutions/architecture-patterns/a-multi-key-uniqueness-claim-needs-one-conditional-batch.md
execution: mixed
amends: docs/architecture/2026-09-28-operator-authorization-direction.md
parent: docs/plans/2026-09-28-2029-feat-operator-authorization-plan.md
---

# Operator Authorization Phase 2, Tenant Entity and Partitioned Stores - Plan

> Implemented. 13 units, 2026-09-28T20:08:45Z to 2026-09-30T08:51:51Z.

This plan is phase 2 of the operator authorization parent plan, following
phase 1 (`docs/plans/2026-09-28-2029-feat-operator-authorization-phase1-plan.md`,
landed in `f1f75c2f..6f8f73d5`). It implements the tenant entity in the
identity leaf package `flowseer.model.identity.v1`, establishes central's
tenant store, partitions central's KeyValue stores and capture artifact
directories by tenant, updates the edgebus subject layout and `AuditStream`
to support multi-tenancy, and wires ambient tenancy into service handlers.

## Goal

A tenant is a UUID-identified entity in `flowseer.model.identity.v1` with
a ref pair and the Config/State/Event triad. Its Config binds it to an
identity provider organization (issuer URL, organization claim name and
value). Central keeps tenants in a store of its own. Every record key in the
`device-lanes`, `edges`, and `captures` buckets, and every capture artifact
directory, starts with the tenant id. The lookup indexes that find a tenant
from an identifier presented before the tenant is known (`edge_<edgeID>`
and `setupkey_<keyID>` in `edges`, `org_<hash>` in `tenants`) are the
exception. The edgebus tenant token is the
tenant id instead of `DefaultTenant`, and central's `AuditStream` (and
any other central-account stream) subscribes with a wildcard in the tenant
position instead of `DefaultTenant`. A platform admin, named in the
deployment's configuration, creates tenants. The means is a new protobuf
schema and central tenant store, key-partitioning across central's KeyValue
buckets and artifact directories, edgebus subject and stream updates, and
ambient tenant propagation in service handlers. The tenant store holds at
most one tenant per (issuer, organization claim value): a tenant's record
and its `org_` index key commit together in one atomic batch (ADR-50) or not
at all. Stop condition: this plan is wrong if an amendment to
`docs/architecture/2026-09-28-operator-authorization-direction.md`
removes the requirement for multi-tenancy or moves tenant identity out of
`flowseer.model.identity.v1`. The follow-up units U11–U13 are wrong if the
pinned nats-server stops checking each batched message's
`Nats-Expected-Last-Subject-Sequence` at commit.

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
- U12 replaces the organization claim U8 landed: record first, then a
  compare-and-set `org_` index with rollback and orphan takeover
  (`src/services/device/internal/tenantstore/store.go:85-245`). The Review
  section lists what five rounds found wrong with it. The decisions from
  here to the table below describe the replacement and the partition rule
  its index depends on.
- The batch client is `github.com/synadia-io/orbit.go/jetstreamext` v0.3.2,
  `PublishMsgBatch`. Why: Synadia ships it as the Go client for ADR-50 (its
  `README.md` says it needs nats-server 2.12.0 or later), it is Apache-2.0,
  and it checks the commit ack's batch id and message count
  (`jetstreamext@v0.3.2/publishbatch.go:464-467`), which a FlowSeer client
  would have to reimplement. It calls only `JetStream.Conn()` and
  `JetStream.Options()`, both on the v1.53.1 interface
  (`nats.go@v1.53.1/jetstream/jetstream.go:54, 58`). Its requirements
  (nats.go v1.52.0, nkeys v0.4.16, nuid v1.0.1, x/crypto v0.54.0, x/sys
  v0.47.0, klauspost/compress v1.19.1, and `orbit.go/natsext` v0.1.3 with the
  same set) are at or below `go.mod`'s pins, so adding it moves no existing
  version. The ADR-50 header protocol over core nats.go lost: it keeps
  `go.mod` unchanged, but FlowSeer would own the batch headers, the
  first-message flow-control ack, and the ack validation that the protocol's
  authors already publish and test. `PublishMsgBatch` calls `Header.Set` on
  every message (`publishbatch.go:405-407`), so each message is built with a
  non-nil header.
- The hub sets `AllowAtomicPublish` on the `KV_tenants` stream after every
  `CreateOrUpdateKeyValue` of the `tenants` bucket: it reads the stream's
  config and sends it back through `UpdateStream` with the flag set. Why:
  `jetstream.KeyValueConfig` has no field for the flag, and every KV create
  or update builds the stream config from a literal
  (`nats.go@v1.53.1/jetstream/kv.go:614-731`, literal at `:672-694`). The
  server field is `allow_atomic,omitempty`
  (`nats-server/v2@v2.14.6/server/stream.go:117`), so each start's
  `CreateOrUpdateKeyValue` in `createStores`
  (`src/modules/edgebus/hub.go:326-335`) turns the flag off and drops staged
  batches (`server/stream.go:2720-2723`). The server accepts the flag on an
  update and refuses it only on a mirror or with `PersistMode: async`
  (`server/stream.go:1851, 1898`). A KV stream is named `KV_<bucket>`
  (`kv.go:487`). Creating `KV_tenants` by hand with a KV-shaped config lost:
  it copies `prepareKeyValueConfig` and drifts when nats.go changes that
  shape. The flag is off only between the two calls inside `StartHub`,
  before any caller holds the bucket. The stream lives in the hub's own
  in-process server (`src/modules/edgebus/hub.go:227, 285`), which restores
  staged partial batches inside `Account.EnableJetStream`
  (`server/jetstream.go:1168, 1585-1651`), before the stream API
  `createStores` calls can answer, so no batch is staged or pending
  recovery while the flag is off.
- `tenantstore.New(ctx, js, bucket) (*Store, error)` replaces
  `New(kv, opts...)`. It opens the bucket with `js.KeyValue`, opens its
  stream with `js.Stream(ctx, "KV_"+bucket)`, and refuses a stream whose
  cached config lacks `AllowAtomicPublish` with `ErrCodeStore`. Batch
  messages go to `$KV.<bucket>.<key>`, the stream's own subject
  (`kv.go:489, 727`). Why: `Create` needs the stream for per-subject reads
  and the `JetStream` handle for the batch, and a bare `jetstream.KeyValue`
  exposes neither. A bucket without the flag fails at construction instead
  of on the first `Create` (10174, `jetstreamext@v0.3.2/errors.go:24`).
  Central's connection is a full-permission user of the CENTRAL account
  (`src/modules/edgebus/hub.go:278-282`), and orbit publishes each message
  on its own subject (`publishbatch.go:421, 451`). `WithRollbackTimeout`,
  `Option`, and `defaultRollbackTimeout` go with the rollback they bounded.
- A tenant is committed while its record is live and the `org_` key its
  config names is live and names its id. `Create` decides from the last
  message on each of its two subjects and writes through one batch only:
  1. Read the last message on `$KV.<bucket>.<tenantID>` with
     `Stream.GetLastMsgForSubject`. `jetstream.ErrMsgNotFound` means an
     expected sequence of 0. A marker's `Sequence` is the expected
     sequence. A marker is what `kv.get` treats as one: a `KV-Operation`
     header of `DEL` or `PURGE`, or a `Nats-Marker-Reason` header of
     `MaxAge`, `Purge`, or `Remove` (`nats.go@v1.53.1/jetstream/kv.go:962-980`).
     Any other message is a live record. A live record whose config is not
     `proto.Equal` to the request returns `ErrCodeAlreadyExists`, and one
     that does not unmarshal returns `ErrCodeDecode`. A live record with an
     equal config goes on to step 2 with its own sequence as the expected
     one.
  2. Read the last message on `$KV.<bucket>.org_<hash>` the same way. A live
     index that names this tenant while step 1 found its equal record means
     the tenant is committed, and `Create` returns the stored record. When
     the index names this tenant but step 1 found no live record, re-read
     the record. Return it if the new record has an equal config. Otherwise
     return `ErrCodeAlreadyExists`. Any other live index also returns
     `ErrCodeAlreadyExists`. `Create` never writes a live index.
  3. Publish the record, then the index, as one batch, each message with
     `Nats-Expected-Last-Subject-Sequence` set to its subject's expected
     sequence. When step 1 found an equal live record, the record message
     carries that record's stored bytes, so the pair completes with its
     first `created_at`.
  4. A reply of 10071 or 10164 (wrong last sequence) means another writer
     moved a subject after the reads. `Create` goes back to step 1, at most
     `casRetries` times, then returns `ErrCodeConflict`. Any other error
     returns `ErrCodeStore`.

  Why: the server checks each staged message's expectation against its own
  subject at commit, under the stream's isolation lock, before it stores any
  message, and one mismatch refuses the whole batch with 10071
  (`nats-server/v2@v2.14.6/server/jetstream_batching.go:753-801`,
  `server/stream.go:7547-7582, 7612-7636`). An expectation of 0 means the
  subject holds no message, and a marker is a message
  (`jetstream_batching.go:792-801`), so step 1 reads the marker's sequence
  the way `kv.Create` does (`nats.go@v1.53.1/jetstream/kv.go:1062-1093`).
  `kv.Get` hides a marker's revision (`kv.go:1007-1030`), so the reads go to
  the stream. The reads let every refusal return without writing, and the
  expectations make acting on them safe: a write between the reads and the
  commit fails the batch. 10164 is the same refusal on a clustered stream
  (`jetstream_batching.go:763-780`), and nats.go treats both codes as a
  wrong last sequence (`kv.go:1097-1104`). Step 1's equal-record branch
  exists for the one state a batch can leave half written (the file-store
  decision below): the record without its index. Completing that pair
  rewrites a record no reader treats as committed, under an expectation on
  its own sequence, and changes no live index.

  ```mermaid
  flowchart TD
      A[read last msg on record subject] -->|live, other config| X[ErrCodeAlreadyExists]
      A -->|live, equal config| B[read last msg on org_ subject]
      A -->|absent or marker| B
      B -->|live, names this tenant, record live and equal| R[return stored record]
      B -->|any other live index| X
      B -->|absent or marker| C[batch: record + index, each with expected last subject sequence]
      C -->|ack| R2[return committed record]
      C -->|10071 or 10164| A
      C -->|other error| S[ErrCodeStore, outcome unknown]
  ```
- A retried or repeated `Create` with the same id resumes nothing. It
  returns the stored record when the tenant is committed with an equal
  config and `ErrCodeAlreadyExists` when the stored config differs,
  including after `Mutate` changed the name or description. An error other
  than a wrong last sequence (a timeout, a lost connection, a cancelled
  context, 10210 too many batches in flight) leaves the outcome unknown,
  since the server finishes a commit whose reply the client stopped waiting
  for (`server/stream.go:7612-7636`). `Create` returns `ErrCodeStore` and
  does no recovery of its own. A retry of the same call returns the stored
  record once the batch has committed. Why: a batch writes both keys or
  neither, so there is no half-written state to roll back, and config
  equality is the retry test the user's 2026-09-30 ruling set. `Create`
  marshals the record (with its `created_at`) once per call, so a retry
  that finds the first commit returns that commit's `created_at`. A batch
  whose commit never arrives is abandoned after 10 s without traffic and
  stores nothing (`server/stream.go:447-451`,
  `server/jetstream_batching.go:77-111`). `Create` passes
  `jetstreamext.BatchFlowControl{AckFirst: false}`, so the record message
  goes out without its own acknowledgement and the one request is the
  commit, bounded by the caller's context. With the default, orbit waits
  for the first message's acknowledgement for the JetStream default
  timeout and ignores the context (`publishbatch.go:388-394, 427-433`).
- `Get`, `GetWithRevision`, and `List` keep the landed rule that a record
  counts only while it is committed (`store.go:256-279`). `LookupByOrg`
  keeps its check that the record the index names carries the issuer and
  organization it was looked up by (`store.go:283-302`). `Mutate` keeps its
  compare-and-set on the record key, still refuses to change the tenant id,
  issuer, or organization claim value, and still never creates a record
  (`store.go:340-377`). Why: a file-store error can leave a record without
  its index (next decision), and the rule keeps every read agreeing with
  `LookupByOrg`, phase 3's authentication path. The immutable binding keeps
  the index valid for the record's lifetime.
- On a single server a commit is all or nothing except when the file store
  fails between the two stores of one commit (`server/stream.go:7617-7636`).
  The record is the first message, so that failure leaves a record without
  its index. The server logs a critical write error but, on a single
  server, keeps taking writes afterwards (`server/stream.go:7066-7071,
  9412-9433`). On restart it writes the rest of a partial batch only when
  that batch's message is still the stream's last
  (`server/jetstream.go:1598-1606, 1638-1651`), so recovery never lands over
  a later commit. The store copes without the server: reads hide the
  record, a retried `Create` with the same config completes the pair
  (step 1's equal-record branch), and another tenant can claim the
  organization, in which case the record stays hidden and its id answers
  `ErrCodeAlreadyExists` to any other config.
- An `org_` key without its record can come only from a write outside the
  store, since the index is the batch's second message. `Create` refuses
  that organization with `ErrCodeAlreadyExists` until an operator removes
  the key. Why: the user's 2026-09-30 ruling removes stale-claim takeover,
  and failing closed keeps the one-tenant-per-organization rule.
- No path deletes a tenant: `Store` has no delete method, and
  `TenantService` defines only `CreateTenant`, `GetTenant`, and
  `ListTenants` (`spec/proto/flowseer/api/identity/v1/tenant_service.proto:12-16`).
  A delete, when one is planned, writes both keys' `KV-Operation: DEL`
  markers in one batch, each expecting its key's current sequence. The
  batch path ignores `KV-Operation` and allows `Nats-Rollup: sub` once per
  subject on a bucket that allows rollups
  (`jetstream_batching.go:921-932`). `Create` already handles the markers
  such a delete leaves (steps 1 and 2).
- `Store` holds its stream reader and its batch publish function in
  unexported fields, and an internal test in package `tenantstore` replaces
  them with fault wrappers. Why: the claim is three calls (two reads and
  one batch), and a lost commit reply can only be produced on demand by a
  publisher that commits and then returns an error.
- The batch claim stays a plan decision, with no direction record. Why: it
  changes no wire contract or schema, the direction record's section
  "Tenants are entities, and the token names one"
  (`docs/architecture/2026-09-28-operator-authorization-direction.md`)
  names no storage mechanism, and its one trace outside the store is the
  bucket flag, which U11 documents in `src/modules/edgebus/README.md`.
  Multi-key uniqueness by atomic batch is a `compound` candidate once U12
  lands.
- The user accepted this amendment on 2026-09-30. U13 amends the partition rule in
  `docs/architecture/2026-09-28-operator-authorization-direction.md`
  (lines 85-87: "every key in the `device-lanes`, `edges`, and `captures`
  buckets, and every key a later store adds, starts with the tenant id").
  Record keys keep the rule. A lookup index that finds a tenant from an
  identifier presented before the tenant is known is keyed by that
  identifier and holds the tenant id as its value: `edge_<edgeID>` and
  `setupkey_<keyID>` in `edges` (`src/services/device/internal/edgestore/store.go:42, 147, 169`)
  and `org_<hash>` in `tenants`. Why: those indexes landed in U3 and U4
  against the rule, and no key layout can satisfy both. An enrolling edge
  presents only a setup key, a verifying edge only its id, and phase 3's
  token only an issuer and organization, so the index cannot start with
  the tenant it resolves. A handler still reaches records only under the
  tenant from its context. The amendment changes an accepted record, so
  a person re-reads it before `land`.

How U12 rules out each failure the Review section lists:

| Review finding | Why it cannot happen after U12 |
| --- | --- |
| A retried `Create` of a committed tenant deletes it when the index read fails (`store.go:198-205`), and so do the rollbacks after a failed reconcile read. | `Create` has no delete call. Its only write is the batch, whose two messages carry values, never markers. A failed read returns before any write. |
| Two concurrent same-id `Create` calls share a record revision, and a cancelled one's rollback deletes the record the other reported. | There is no rollback. Both batches expect the same sequences, the server commits one, and the other gets 10071, re-reads, finds the committed tenant with an equal config, and returns it. A cancelled call ends before or after its commit and deletes nothing. |
| "The index names me" is a read, and a competitor holding the index's old revision takes it over after `Create` returned. | There is no takeover. `Create` writes an `org_` key only when its last message is absent or a marker, the batch's expectation enforces that at commit, and no `kv.Update` on an `org_` key remains. Once live, an index never changes, so the read in step 2 cannot go stale. |
| `ownership_test.go` injects no `Get` or `Keys` faults, faults only the first matching call, never retries, has no same-id concurrency, and its visibility check holds by construction. | U12's claim test places a fault at every call position `Create` reaches, re-reads included, runs an unfaulted retry after every fault, runs same-id and same-organization contention, checks invariants on raw stream messages, and then checks that `Get`, `List`, and `LookupByOrg` agree with them. `Get` and `Keys` are not calls `Create` makes, so they have no fault point in the claim. |
| Whether same-id `Create` resumes at all. | It does: config equality decides between the stored record and `ErrCodeAlreadyExists`. The one completion it performs is of a record whose index a file-store error kept from committing, by the same batch. |

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
   is removed so that `grep -rn 'message OperatorRef\|message Tenant' spec/proto/flowseer`
   prints only files under `model/identity/v1/`).
2. Partitioned keys. Acceptance: parent Requirement 4, with the tenant set
   from configuration instead of a token: an edge created by a tenant A caller
   is stored under a key that begins with A's id, and `GetEdge` for that id
   from a tenant B caller returns `NotFound`.
3. Every tenant's audit reaches central. Acceptance: with tenants A and B
   configured, a `DeviceOperationEvent` published on each tenant's audit
   subject is stored in central's `AuditStream`.
4. The tenants bucket takes atomic batches across restarts. Acceptance:
   after `StartHub`, and again after a second `StartHub` on the same state
   directory, `js.Stream(ctx, "KV_tenants")` reports
   `AllowAtomicPublish: true`.
5. One tenant per organization, written all or nothing. Acceptance: with
   tenant `0192e6a0-0000-7000-8000-00000000000a` committed for organization
   `org-o`, `Create` of tenant `0192e6a0-0000-7000-8000-00000000000b` for
   `org-o` returns `ErrCodeAlreadyExists` and
   `$KV.tenants.0192e6a0-0000-7000-8000-00000000000b` holds no message. When
   another writer commits `org-o`'s index after `Create`'s reads and before
   its batch, `Create` returns `ErrCodeAlreadyExists` and that subject still
   holds no message.
6. A same-config retry succeeds without writing. Acceptance: a `Create`
   whose batch committed but whose reply was lost returns `ErrCodeStore`. A
   second `Create` with the same config returns a record whose `created_at`
   equals the first attempt's, and the last sequences of both subjects are
   the ones the first batch wrote.
7. No `Create` deletes a key or changes a committed tenant. Acceptance:
   across the claim test's fault matrix, no `Create` call adds a message
   carrying a `KV-Operation` header, the value of a live `org_` key never
   changes, and a committed record's last sequence never changes.

## Out of scope

- OIDC token verification and Connect authentication interceptor (phase 3).
- Zanzibar engine interface, SpiceDB adapter, and in-memory fake (phase 4).
- Relationship authorization checks on operator RPCs (phase 5).
- Operator action audit stream for administrative RPCs (phase 6).
- A tenant delete path. The Decisions say what shape one takes.
- An explicit tenant field on `AttachBusResponse`
  (`spec/proto/flowseer/edge/attach/v1/bus.proto`). Central always sends the
  edge's subjects (`src/services/device/internal/edgeapi/service.go:209-215`),
  and the agent refuses a map that does not name one valid tenant and its
  own edge id, so nothing infers a wrong tenant today. The field is a wire
  change to a contract outside this phase's files.
- The one-tenant wording in
  `docs/solutions/architecture-patterns/a-restarted-hub-must-re-attach-every-persisted-edge-account.md`,
  which belongs to `compound`'s refresh of existing solutions.
- A NATS account per tenant.
  `docs/architecture/2026-08-20-device-service-and-inventory-direction.md`
  (line 398) decides one, and
  `docs/architecture/2026-09-28-operator-authorization-direction.md`
  ("Tenants are entities, and the token names one") names it by the tenant
  id, but no phase of the parent plan delivers it. What landed is one
  account per edge plus CENTRAL, with each edge user allowed to publish
  only under its own tenant's subtree (`src/modules/edgebus/keys.go:357-367`).
  Whether a later phase adds the per-tenant account is the parent plan's
  decision.

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
  `edge-<edgeID>.nk`. `persistedEdgeIDs` reads the sidecar and skips an edge
  account without one, and `StartHub` re-attaches persisted edge accounts
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

U12 replaces this unit's claim. As landed, `Create` resumes an existing id
whose stored config equals the request instead of returning
`ErrCodeAlreadyExists`, and the fixes after review added
`src/services/device/internal/tenantstore/ownership_test.go`.

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
    If `cfg.Tenant` is empty, returns `ErrCodeConfig` before writing state.
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
  - In `TestMultiTenantIsolationAndEdgeBusPartitioning` (`lines 971-985`), replaces the call to
    `tenantClient.CreateTenant` with direct tenant creation via
    `tenantstore.New(kvTenant).Create(...)`.
  - Verifies multi-tenant bucket partitioning, edge enrollment, and journal
    operations with the tenant seeded directly through the store.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/host src/services/device/test/integration`

Waves (landed): U7 U8 U9 | U10

### U11. Tenants bucket takes atomic batches

Files: `src/modules/edgebus/hub.go`,
`src/modules/edgebus/edgebus_test.go`,
`src/modules/edgebus/README.md`
After: none
Change:
- `createStores` (`src/modules/edgebus/hub.go:326-335`), after its bucket
  loop, loads the stream `"KV_" + TenantBucket` with `h.centralJS.Stream`,
  copies `CachedInfo().Config`, sets `AllowAtomicPublish`, and sends the
  config through `h.centralJS.UpdateStream`. A failure returns `ErrCodeHub`
  with the `bucket` attribute, as the loop's failures do. A comment says why
  the step runs on every start: `KeyValueConfig` has no field for the flag,
  and each `CreateOrUpdateKeyValue` clears it.
- `src/modules/edgebus/README.md`: the CENTRAL bullet (line 31) names all
  four buckets central keeps, and says the `tenants` bucket's stream takes
  atomic batches so the tenant store writes a record and its organization
  index together.
Tests:
- `TestTenantBucketAllowsAtomicPublishAcrossRestart` in
  `src/modules/edgebus/edgebus_test.go` starts a hub, asserts that
  `KV_tenants` reports `AllowAtomicPublish`, closes the hub, starts a second
  one on the same `StateDir`, and asserts the flag again (Requirement 4). It
  also asserts that `"KV_" + LaneBucket` has the flag off, so the step stays
  on one bucket.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/edgebus`

### U12. Tenant store claims an organization with one atomic batch

Files: `go.mod`,
`go.sum`,
`src/services/device/internal/tenantstore/store.go`,
`src/services/device/internal/tenantstore/store_test.go`,
`src/services/device/internal/tenantstore/ownership_test.go`,
`src/services/device/internal/tenantstore/claim_internal_test.go`,
`src/services/device/test/integration/e2e_test.go`
After: U11
Change:
- `go get github.com/synadia-io/orbit.go/jetstreamext@v0.3.2` adds it and
  `github.com/synadia-io/orbit.go/natsext` v0.1.3 as an indirect
  requirement. No other version in `go.mod` moves. If one does, stop: the
  Decision on the client rests on that.
- `src/services/device/internal/tenantstore/store.go`:
  - `Store` holds the bucket (`jetstream.KeyValue`), the subject prefix
    `$KV.<bucket>.`, and two unexported functions: `lastMsg`, backed by
    `jetstream.Stream.GetLastMsgForSubject`, and `publish`, backed by
    `jetstreamext.PublishMsgBatch` with
    `jetstreamext.BatchFlowControl{AckFirst: false}`. The `publish` closure
    captures the `JetStream` handle passed to `New`.
  - `New(ctx, js, bucket) (*Store, error)` as the Decisions describe.
    `Option`, `WithRollbackTimeout`, `defaultRollbackTimeout`,
    `rollbackDelete`, and `ownsOrgIndex` are removed, and nothing in the
    package calls `context.WithoutCancel`.
  - `Create` follows the Decisions' four steps. The record message's data
    is the marshalled `TenantRecord`, the index message's data is the
    tenant id, and each carries `jetstream.ExpectedLastSubjSeqHeader`. The
    marker test reads the `KV-Operation` header by name, since nats.go does
    not export it (`nats.go@v1.53.1/jetstream/kv.go:497-499`), and
    `jetstream.MarkerReasonHeader` by its constant. A wrong last
    sequence is an `*jetstream.APIError` whose `ErrorCode` is
    `jetstream.JSErrCodeStreamWrongLastSequence` or
    `jetstream.JSErrCodeStreamWrongLastSequenceConstant`, found with
    `errors.As`, because orbit returns it unwrapped
    (`jetstreamext@v0.3.2/publishbatch.go:332-334, 461-463`).
  - `Get`, `GetWithRevision`, `List`, `LookupByOrg`, and `Mutate` keep
    their behavior, including the committed-only rule.
  - The package comment states the invariant: a tenant's record and its
    `org_` index are written together by one batch, and a record counts
    only while its index names it. `Create`'s doc comment states the retry
    contract: an equal committed config returns the stored record, any
    other stored config or claimed organization returns
    `ErrCodeAlreadyExists`, and `ErrCodeStore` leaves the outcome unknown
    until a retry of the same call.
- `src/services/device/internal/tenantstore/store_test.go`:
  - `newStoreWithKV` builds the store with
    `tenantstore.New(ctx, hub.JetStream(), edgebus.TenantBucket)` and
    returns the bucket from `hub.JetStream().KeyValue`.
  - `TestOrphanedOrgIndexTakeover` becomes
    `TestCreateNeverTakesOverALiveIndex`: a raw `org_` key naming an id with
    no record makes `Create` return `ErrCodeAlreadyExists`, the key keeps its
    value and sequence, and the new id's subject holds no message.
  - `TestRollbackDeletesPrimaryRecordWhenOrgOwned` becomes
    `TestCreateForClaimedOrgWritesNothing`: the second tenant's subject holds
    no message (`GetLastMsgForSubject` returns `jetstream.ErrMsgNotFound`),
    which is stronger than a nil `Get` (Requirement 5, first half).
  - `TestConcurrentCreateOrgConflictSettlement` also asserts that no loser's
    record subject holds a message.
  - `TestDuplicatePrevention` loses its "rolled back" wording and keeps its
    assertions.
  - `TestCreateRetryReturnsStoredRecord` (new): two `Create` calls with one
    config return records with equal `created_at`, and the second call
    leaves both subjects' last sequences as the first left them.
  - `TestCreateAfterDeleteMarkers` (new), four cases: `kv.Delete` or
    `kv.Purge` of both keys of a committed tenant, followed by `Create` of
    the same config or of a new id for the same organization. Each `Create`
    succeeds and writes above the markers' sequences.
  - `TestNewRefusesBucketWithoutAtomicPublish` (new): `New` over a bucket
    made with `js.CreateKeyValue` alone returns `ErrCodeStore`.
- `src/services/device/internal/tenantstore/ownership_test.go` is deleted:
  its fault wrappers wrap `KeyValue.Create`, `Update`, and `Delete`, which
  `Create` no longer calls.
- `src/services/device/internal/tenantstore/claim_internal_test.go` (new,
  package `tenantstore`) holds `TestClaimMatrix`, the claim's state matrix,
  on a hub from `edgebus.StartHub`:
  - Eight initial states:
    - empty
    - the same tenant committed
    - the id committed for another organization
    - the organization committed by another id
    - both keys of the same tenant deleted, leaving markers
    - the same tenant's record with no index, as a file-store failure
      between the batch's two stores leaves it (seeded by a raw `kv.Create`
      of the record key)
    - a record with no index whose organization another tenant claimed
    - a raw `org_` key naming an id with no record
  - Faults:
    - a read fails
    - the batch fails before it is sent
    - the batch stages its record message through a
      `jetstreamext.BatchPublisher` and never commits (the server abandons
      it)
    - the batch commits and then returns `context.DeadlineExceeded` (a lost
      reply)
    - another `Store` commits a conflicting tenant between `Create`'s reads
      and its batch, either another id for the same organization or the same
      id for another organization

    Five further cases place a competing commit after the first read or at
    publish. The test pins the kind and position of every read and publish
    call, then injects a failure at each applicable position. A different-id
    competitor for the organization makes five calls (two reads, publish,
    record re-read, index re-read). A different-organization competitor for
    the same id makes four, since the record re-read finds the other config.
    The two cases separately exercise the index and record expected-sequence
    headers. Publish faults cover before-send, abandoned batch, and lost
    reply where a batch can commit. The competitor cases cover before-send.
  - Each (state, fault) case runs `Create` with the fault, checks the
    invariants, runs `Create` again without faults, and checks its outcome
    and the invariants again. An equal committed config returns the stored
    record. A different config or organization owner returns
    `ErrCodeAlreadyExists`, including after a conflicting commit. A
    competitor with an equal config returns the stored record (Requirement
    5, second half, and Requirement 6).
  - Contention without faults: eight `Create` calls with one config all
    return records with equal `created_at`, and the record subject holds
    one message. Eight with one id and eight organizations yield one success
    and seven `ErrCodeAlreadyExists`, and only the winner's `org_` subject
    holds a message. Eight with one organization and eight ids yield one
    success, and only the winner's record subject holds a message.
  - Contention with lost replies: eight `Create` calls for one organization
    and eight ids, through a publisher that returns
    `context.DeadlineExceeded` whenever the real batch committed. One call
    returns `ErrCodeStore` and seven return `ErrCodeAlreadyExists`. A
    fault-free retry of each returns the stored record for the one and
    `ErrCodeAlreadyExists` for the rest.
  - The invariants read raw stream messages first: the subjects come from
    `Stream.Info` with a subject filter on `$KV.tenants.>`, and each
    subject's last message from `GetLastMsgForSubject`. Every live record
    subject `Create` wrote has a live `org_` subject that names its id and
    matches its config's `OrgIndexKey`, and every live `org_` subject names
    a live record. The three raw seeds are exempt only while they keep their
    seeded sequence, and the index seed always keeps it. `Create` adds no
    message with a `KV-Operation` header, a live `org_` key's value never
    changes, and a committed record's last sequence never changes
    (Requirement 7). Then the `Store` view must agree with the raw one:
    `Get` and `List` return a tenant exactly when its pair is committed,
    and `LookupByOrg` returns it for its organization.
- `src/services/device/test/integration/e2e_test.go`:
  `TestMultiTenantIsolationAndEdgeBusPartitioning` builds its store with
  `tenantstore.New(ctx, js, edgebus.TenantBucket)` and drops its `kvTenant`
  handle (`e2e_test.go:971-976`).
Tests:
- `claim_internal_test.go` and the `store_test.go` cases above
  (Requirements 5, 6, and 7).
- `TestMultiTenantIsolationAndEdgeBusPartitioning` still seeds its tenant
  through the store.
- Two risks the Decisions name have no test in this unit. 10164 comes only
  from a clustered stream, and every test hub is a single server, so the
  code treats it as 10071 with the check nats.go uses (`kv.go:1097-1104`).
  The server's partial commit after a file-store failure cannot be provoked
  from a test, and nothing in the store detects it.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- go.mod go.sum src/services/device/internal/tenantstore src/services/device/test/integration`

### U13. Tenancy docs, the partition rule amendment, and the Review's low findings

Files: `docs/architecture/2026-09-28-operator-authorization-direction.md`,
`docs/architecture/2026-09-09-remote-packet-capture-direction.md`,
`src/modules/edgebus/README.md`,
`src/modules/edgebus/edgebus_test.go`,
`spec/proto/flowseer/store/device/v1/README.md`,
`src/services/device/README.md`,
`src/services/device/internal/host/host_test.go`
After: U11
Change:
- `docs/architecture/2026-09-28-operator-authorization-direction.md`
  (lines 85-89): the partition paragraph states the lookup-index exception
  as the Decisions word it, and names the three indexes.
- `docs/architecture/2026-09-09-remote-packet-capture-direction.md:293`:
  the artifact path reads `<StateDir>/captures/<tenant_id>/<session_id>.pcapng`,
  as `src/services/device/internal/captureapi/store.go:108-112` builds it.
- `src/modules/edgebus/README.md`:
  - The restart paragraph (line 110) says a restart re-attaches every edge
    whose account key and `edge-<edgeID>.tenant` file persisted, and skips
    one whose tenant file cannot be read (`src/modules/edgebus/hub.go:250-262`,
    `keys.go:102-104`).
  - The "A second tenant" bullet under "What is deliberately absent"
    (lines 222-224) goes. The accounts section says instead that the tenant
    token is enforced: an edge user publishes only under
    `flowseer.<tenant>.edge.<edge-id>.>` (`edgePermissions`,
    `keys.go:357-367`), and the forwarder refuses a record whose subject
    names another tenant or edge (`belongsToEdge`, `forwarder.go:226`). The
    bullet that replaces it under "What is deliberately absent" names what
    is still missing, the per-tenant NATS account that
    `docs/architecture/2026-08-20-device-service-and-inventory-direction.md`
    decides (line 398).
- `spec/proto/flowseer/store/device/v1/README.md:29-30`: the "A tenant"
  bullet says the lane record has no tenant field because the tenant is the
  `<tenant_id>.` prefix of the record's key.
- `src/services/device/README.md:181` names `TenantInterceptor` in
  `internal/host/serve.go` instead of the line range `serve.go:68-76`.
- `src/services/device/internal/host/host_test.go`: the comment above
  `TestTenantServiceIsNotMounted` (lines 415-417) states the behavior
  without saying when authentication lands.
  `TestStartupDoesNotIndexUnindexedRegistryEdge` registers a `t.Cleanup`
  that cancels the host and waits on `done`, so `host.Run` returns before
  the test's temporary directory is removed.
- `src/modules/edgebus/edgebus_test.go`:
  `TestStartLeafRefusesEmptyAndInvalidTenant` sets `StateDir` to a path
  that does not exist yet and asserts it still does not exist after each
  refusal, which shows the tenant check (`src/modules/edgebus/leaf.go:113`)
  runs before `os.MkdirAll` (`leaf.go:121`).
Tests:
- `TestStartLeafRefusesEmptyAndInvalidTenant` and
  `TestStartupDoesNotIndexUnindexedRegistryEdge` as changed above. The doc
  edits have no test beyond the verifier's prose check.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/architecture/2026-09-28-operator-authorization-direction.md docs/architecture/2026-09-09-remote-packet-capture-direction.md src/modules/edgebus spec/proto/flowseer/store/device/v1/README.md src/services/device/README.md src/services/device/internal/host`

Waves: U11 | U12 U13

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- go.mod go.sum spec/proto/flowseer/model/identity/v1 spec/proto/flowseer/api/identity/v1 spec/proto/flowseer/model/inventory/v1 spec/proto/flowseer/model/README.md spec/proto/flowseer/store/device/v1 src/modules/edgebus src/edge/agent/internal/busattach src/common/tenant src/services/device test/conformance/proto docs/conventions/protobuf.md
go test -race ./src/modules/edgebus/... ./src/edge/agent/internal/busattach/... ./src/common/tenant/... ./src/services/device/... ./test/conformance/proto/...
go test -race -count=20 -run 'TestClaim|TestCreate|TestConcurrent' ./src/services/device/internal/tenantstore/
```

Run targeted verification on these paths. The repeated tenant store run
shakes out ordering in the contention cases. Integration tests under
`src/services/device/test/integration` use Docker, so run them where Docker
is reachable. U12's `go get` changes `go.mod` and `go.sum` from Bash, which
marks the tree `<Bash mutation; verify with --full>`, and `implement` and
`land` then call for a `--full` run (`.claude/skills/implement/SKILL.md`,
sections "2. Work the units", step 2, and "3. Finish", step 4). A `--full` run also builds the nested
`generated/go/yang` module, since the verifier walks every `go.mod`.

## Definition of done

- [x] Verifier green for every changed path across U1–U6.
- [x] `TenantLocalRef`, `TenantGlobalRef`, `TenantConfig`, `TenantState`, `TenantEvent`, and `TenantRecord` live in `model/identity/v1`.
- [x] `spec/proto/flowseer/model/inventory/v1/tenant.proto` is deleted and `generated/` is regenerated.
- [x] `docs/conventions/protobuf.md` has lost the `ENTITY_TYPE_TENANT` exception.
- [x] Requirements 1, 2, and 3 hold by their acceptance tests.
- [x] Parent U2's `Landed:` line filled.
- [x] Verifier green for every changed path across follow-up units U7–U10.
- [x] Edge leaf attaches with concrete tenant from `AttachBus` and publishes under assigned tenant.
- [x] Organization index claim in `tenantstore.Store.Create` is clock-free with CAS commit after record write (replaced by U12).
- [x] Rollback tests drive `tenantstore.Store` directly (replaced by U12).
- [x] `PlatformAdmin` schema defines `organization_claim_name` and `dev_tenant` rejects uppercase UUIDs.
- [x] `host.go` contains no synthetic registry edge index auto-bind.
- [x] `edgebus.Hub.EdgeTenant` never returns `DefaultTenant` on a zero Hub.
- [x] `TenantService` is unmounted from central host until phase 3.
- [x] Capture artifact path documentation reflects per-tenant directory layout.
- [x] This plan's `status` set with an outcome note under its title.
- [x] No plan labels in code.
- [x] Verifier green for every changed path across follow-up units U11–U13.
- [x] `KV_tenants` allows atomic publish after every hub start (Requirement 4).
- [x] `tenantstore.Create` writes a record and its `org_` index in one atomic batch, and nothing in the package deletes, rolls back, or takes over a key (Requirements 5, 6, and 7).
- [x] `claim_internal_test.go` replaces `ownership_test.go`.
- [x] The tenancy docs and low findings U13 names are fixed, and the amended partition rule reads as the user accepted it on 2026-09-30 in `docs/architecture/2026-09-28-operator-authorization-direction.md`.
- [x] This plan's `status` set to `implemented` with an outcome note under its title.
- [x] No plan labels in code after U11–U13.

## Review

Verdict: accept after fixes (2026-09-30). This third review examined the
phase 2 change with focus on U11–U13 and the earlier `tenantstore.Create`
organization-claim failure. `Store.Create` now publishes the record and
organization index in one atomic batch, each with an expected last subject
sequence (`src/services/device/internal/tenantstore/store.go:126-202`). It
has no delete, rollback, or index takeover path. The committed-only reads
still hide a record without its index. The U11 hub test verifies that the
`tenants` stream accepts atomic batches before and after a restart.

The review fixed these findings:

- Medium: `TestClaimMatrix` did not exercise every read and publish fault
  position, compare returned records with the raw stream, or pin both CAS
  headers in independent competition cases. `344c86e2`, `c9b69461`, and
  `59eca71d` added the state matrix, raw-message invariants, retries after
  faults, exact call paths, and separate same-id and same-organization
  competitors. Removing either expected-sequence header now fails its own
  case in `TestClaimMatrix`. Wrong call kinds fail the path assertion.
- Low: tenant lookup and edge grant documentation, a host shutdown test,
  and unused tenant store test code were corrected in `f7f8e089`,
  `e5cf2335`, and `344c86e2`. U13 also brought the partition rule and
  affected READMEs into agreement with the pre-tenant lookup indexes.

The final unit and seam passes found no verified correctness finding.
`go test -race` and `go vet` passed for the tenant store package, and the
repository verifier passed on every changed path in the fix rounds. The
remaining testing limits are the clustered-stream 10164 response, a
physical file-store failure between a batch's two stores, `ErrCodeDecode`,
and retry exhaustion (`ErrCodeConflict`). `TestClaimMatrix` does not inject
an abandoned batch into a competing publish that the server would refuse.
The AttachBus schema permits an empty `subjects` map, although the edge
agent needs concrete subjects to derive its tenant. Central sends three
subjects (`spec/proto/flowseer/edge/attach/v1/bus.proto`,
`src/edge/agent/internal/busattach/busattach.go`).

## Open questions

None.
