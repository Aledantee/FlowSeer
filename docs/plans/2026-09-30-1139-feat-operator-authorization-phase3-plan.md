---
title: Operator Authorization Phase 3, Enforcement On, Projector, and Stamped Identity - Plan
type: feat
date: 2026-09-30
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: rework
execution: mixed
amends: docs/architecture/2026-09-30-operator-authorization-direction.md
parent: docs/plans/2026-09-30-1139-feat-operator-authorization-plan.md
---

# Operator Authorization Phase 3, Enforcement On, Projector, and Stamped Identity - Plan

> Implemented. 8 units, 2026-10-03T18:04Z to 2026-10-03T21:15Z.

## Goal

`DeviceService`, `EdgeAdminService`, and `CaptureService` serve behind the
authentication and authorization interceptors, the edge-facing services do
not. A projector keeps OpenFGA's relationships for edges, devices, and
capture sessions derived from their records in JetStream KV.
`CreateCaptureSession` requires `tenant#full_payload` when full payload is
requested. Every call of `EdgeAdminService` and every full-payload capture
lands in an operator action trail naming the principal, in the same change
that turns enforcement on. Handlers whose
rule defers the check call `Require` or `Filter`, and every identity a
handler records comes from the authenticated principal.

**Stop condition:** against OpenFGA v1.21.0 with every cache off, a `Check`
sent after an acknowledged `Write` answers from the state before it. A
caller could then be denied the edge it just created, and the write-through
projection below is wrong. U3's tagged test finds this before U7 builds on
it.

## Decisions

The parent plan's Decisions apply. Sources under `~/go/pkg/mod`: `openfga@`
is `github.com/openfga/openfga@v1.21.0`, `api@` is
`github.com/openfga/api/proto@v0.0.0-20260723150800-6981fff8d33b/openfga/v1`,
`connect@` is `connectrpc.com/connect@v1.21.0`, `oidc@` is
`github.com/coreos/go-oidc/v3@v3.21.0/oidc`, and `nats@` is
`github.com/nats-io/nats.go@v1.54.0`. Paths under `internal/` are under
`src/services/device/`.

- The plan stays whole at eight units. Why: the units form one cluster.
  Every one feeds U7, and U7 cannot be cut. Turning enforcement on changes
  what every operator call needs (a token, a tenant header, a context the
  interceptor prepared), so the handlers, the host wiring, and every test
  that calls an operator RPC go red and green together. The parent fixes
  this phase as one unit of its own. It runs past 300 lines because the
  stub listed six designs to settle here and phases 1 and 2 handed over
  seven questions, and each answer below is one an implementer cannot
  choose alone.

### Interceptors and start

- Operator handlers get telemetry, authentication, validation,
  authorization, and the action trail, in that order. Edge-facing handlers
  keep telemetry and validation. Why: `connect.WithInterceptors` runs the
  first one outermost (`connect@/interceptor.go:83-87`). Authorization reads
  request fields, so it follows validation. That answers phase 1's question
  on object ids: every id a rule reads is a `string.uuid` field the
  validating interceptor has checked, and the adapter already answers false
  for an id OpenFGA would refuse. The trail is innermost, so it records only
  calls authorization admitted.
- `TenantInterceptor` and `DeviceServiceConfig.dev_tenant` are removed. Why:
  the admitted tenant now comes from `X-FlowSeer-Tenant`, and no edge-facing
  handler reads `tenant.FromContext`. Its only readers are
  `internal/edgeapi/admin.go`, `internal/captureapi/operator_service.go`,
  `internal/deviceapi`, and `laneAdmin` in `internal/host/serve.go`. The
  field's number and name are reserved (`docs/code-style-proto.md:185`), so
  a file that still sets `dev_tenant` parses and the line has no effect:
  prototext skips a reserved name
  (`google.golang.org/protobuf@v1.36.12/encoding/prototext/decode.go:204-208`).
  Such a deployment fails closed, since its operator calls now need a
  token.
- `authentication` and `authorization` become required in
  `DeviceServiceConfig`. Why: the operator authorization record's
  Consequences make the operator API unusable without an issuer and an
  engine, and a configuration that validates and then refuses every operator
  call is found by a user (the comment in `parseConfig`,
  `internal/host/config.go`).
- A fault the service can see without the network fails the start, and a
  fault it learns from the network does not. A malformed engine id or
  endpoint, a key or CA file that cannot be read, and a configuration that
  fails its rules return from `host.Run` before anything binds. An engine
  that is unreachable, refuses the key, or holds another store or model
  leaves the service running: operator calls answer `Unavailable` and
  edge-facing calls are served. Why: phase 2 built the verifier without
  network access because edge-facing services share the process, and the
  same holds for the engine. A module whose `Setup` fails is restarted with
  every module after it (`RestForOne`, `src/common/service/supervisor.go:301-304`)
  three times a minute (`src/common/service/policy.go:9-10`), and the next
  failure ends the supervisor with "outcome restart budget exhausted"
  (`supervisor.go:236-238`). An engine outage at start would take the
  listener the edges dial with it. This answers
  phase 2's question on an unreachable engine. For an unreachable issuer
  nothing changes: the verifier discovers it on the first token.
- The adapter runs its start check (`GetStore`, `ReadAuthorizationModel`,
  the comparison with the embedded model) before its first use instead of
  in `openfga.New`, repeats a failed check at most once in 5 s, and sends no
  query, write, or read to an engine it has not verified. Why: the decision
  above, and phase 2's reason for the check still holds (new code against
  an old model fails every check).

### Enforcement core

- A streaming call is authorized only under a request rule on a server
  stream. The interceptor reads the rule, the principal, and the tenant
  header and makes the membership check before it calls the next handler,
  then checks the object inside a wrapped `Receive` when the one request
  message arrives. Why: Connect decodes that message inside the innermost
  handler function (`connect@/handler.go:204-212`), so no interceptor sees
  it earlier, and an error from `Receive` returns before the implementation
  is called (`:205-208`). The context is fixed once the next handler is
  called, so the tenant goes in before. `Receive` is called a second time
  and must then return `io.EOF` (`connect@/connect.go:499-508`), so the
  wrapper checks on the first successful call and passes the rest through.
  The validating interceptor validates stream messages the same way, on
  operator handlers only, because `UploadCapture` validates its own chunks
  and fails the session when one is refused
  (`internal/captureapi/edge_service.go:570-581`).
- A stream is checked once, when it opens. Why: a tail ends with the
  capture's budget and a download with the artifact, and the engine has no
  push of access changes to re-check on.
- The tracker counts checks that have started and not returned, and a
  response returned while one is in flight is dropped with `Internal`. Why:
  this closes phase 1's question on a check started in a goroutine the
  handler does not join, and it fails closed.
- `authz.Abandon(ctx, err)` lets a handler return an error it met before it
  could check. It discharges the obligation and records a failed check, so
  a response after it is dropped. Why: this answers phase 1's question on a
  filtered list whose store read fails. Both filtered handlers read
  FlowSeer's store to learn the ids they filter, and a store failure there
  has to reach the caller as the retryable `Unavailable` it is. No rule in
  the tree is `loaded`, so no handler can use this to tell a caller whether
  an object exists.
- `authz` declares `Relations` (write, read by object, scan) and `Engine`,
  which is `Checker` and `Relations` together. `authztest.Engine` is the
  in-memory fake: it answers a query from stored and contextual tuples and
  from grants a test adds, and evaluates no model. Why: the record's "The
  service boundary names no engine" asks for one interface and an in-memory
  fake. A fake that evaluated the model would be a second reading of
  OpenFGA written from this plan (`AGENTS.md`, Investigation discipline), so
  what the model grants is proven only against the real server.

### Relationship writes

- A write is idempotent and at most 100 tuples. The adapter sends
  `on_duplicate: "ignore"` and `on_missing: "ignore"`, which the server
  accepts (`openfga@/pkg/server/commands/write.go:58-78`) and the pinned
  module carries (`api@/openfga_service.pb.go:665`, `:717`). Writes and
  deletes of one call count together against `DefaultMaxTuplesPerWrite`
  (`openfga@/pkg/server/config/config.go:19`,
  `openfga@/pkg/server/commands/write.go:211-212`) and run in one
  transaction (`openfga@/pkg/storage/postgres/postgres.go:592-644`). A
  concurrent write of the same tuple answers `codes.Aborted`
  (`write.go:105-106`), which the adapter maps to the retryable
  `authz/engine-conflict`.
- The adapter reads the tuples of one object, or every tuple in the store.
  Why: the server refuses a filter that names a type and neither an id nor
  a user (`openfga@/pkg/server/commands/read.go:68-72`). A page holds at
  most 100 (`api@/openfga_service.pb.validate.go:1234-1237`). Both reads
  send `HIGHER_CONSISTENCY`, which reads the primary
  (`openfga@/pkg/storage/postgres/postgres.go:321-331`), since a repair
  decided on a replica's view can delete what was just written.

### Projector

- The projector owns five relations and touches no other tuple:

  | Object | Relation | Derived from |
  | --- | --- | --- |
  | `edge:<id>` | `tenant` | the `edge_<id>` index entry (`internal/edgestore/store.go:169-189`) |
  | `device:<id>` | `tenant` | the registry listing the device, and the index entry of the registry's edge |
  | `capture_session:<id>` | `tenant` | the tenant part of the session's key (`internal/captureapi/store.go:88-93`) |
  | `capture_session:<id>` | `edge` | `config.ref.edge.edge.id` |
  | `capture_session:<id>` | `requester` | `user:` and `authn.ComputePrincipalID` of `config.authorization.requested_by` |

  Why: these are what the model's `from tenant` and `from edge` terms and
  the interceptor's tenant check read. Grants on an edge or device are
  phase 4's. A session stored with no issuer in `requested_by` gets no
  `requester` tuple.
- A device's relationship derives from the registry and the hosting edge's
  index entry, which is the path `deviceapi.Service.device` resolves today
  (`internal/deviceapi/service.go:137-162`). Why: the Goal names records in
  JetStream KV, and a device has none until its first use. The lane record
  is created lazily (`internal/journal/journal.go:175-176`) and names no
  edge, so a rule on it would deny the call that creates it. The tenant
  half does come from KV.
- `Sync(object)` is the only code that writes or deletes a relationship. It
  reads the object's stored tuples, then the record, and writes the
  difference in one call. Why this order: a creating handler writes the
  record before its tuples, and ids are random UUIDs that are never reused.
  Tuples whose record is absent when read afterwards therefore belong to a
  record that was deleted.
- A reconcile pass detects and `Sync` repairs. The pass takes a snapshot of
  every record, scans the store, and calls `Sync` for each object whose
  owned tuples differ on either side. Why: the snapshot is stale by the end
  of the scan, and deleting from it would remove the tuples of an object
  created in between.
- A handler that creates a record projects it before it answers. A failed
  projection is logged and the handler answers anyway. Why: the record is
  the source and the next pass repairs the projection. `CreateEdge` returns
  the one copy of a setup key (`internal/edgeapi/admin.go:126-127`), and a
  caller retrying a failed call would create a second edge.
- `DeleteCaptureSession` projects after the record is gone, which deletes
  the session's tuples before the RPC returns. Leftover tuples after a
  failure grant nothing, since every handler reads the session by tenant
  and id and answers `NotFound`. A removal that revokes access is phase 4's:
  it calls `Sync` and returns its error.
- A pass runs at start and then every 10 minutes
  (`intervals.relationship_reconcile`). A failed pass is retried after 5 s,
  doubling up to the interval. The module is declared last, so its restart
  restarts nothing else. A pass costs one `Read` per 100 stored tuples and
  one KV get per session. Its duration at the benchmark fixture's size is
  unmeasured.
- A device listed in the registry is authorized once the first pass after a
  start has run, since the registry is read once at start
  (`internal/host/host.go:90`). `host.Options.Reconciled` reports each
  completed pass, as `Bound` reports the listener, so a test waits on it.

### Identity

- `OperatorRef` gains a required `issuer` beside `subject`, as the parent
  decides. The verifier refuses a subject longer than 256 characters, the
  bound `OperatorRef.subject` carries, so every principal can be recorded.
- `MutationIntent.actor` and `CaptureAuthorization.requested_by` stay on
  their messages and stop being required. The handler replaces whatever a
  request carries with the principal. Why: both messages are the request
  payload and the stored record at once (the journal, the audit record,
  the `ExecuteRequest` an edge receives, the session config). A separate
  request shape for each is a larger API change than stamping needs.
- `AbandonMutationRequest.actor` and `ResolveDesynchronizationRequest.actor`
  are removed, number and name reserved. Why: no handler reads them
  (`internal/deviceapi/resolve.go`), so they are an identity a caller writes
  and nothing records.
- The idempotency digest's `actorPart` becomes `operator:`, the issuer, one
  zero byte, and the subject. A key admitted before this change and retried
  after it answers `journal/idempotency-mismatch`.

### Operator action trail

- The record is `OperatorActionEvent` in a new package
  `flowseer.event.operator.v1`, an event-only family. It holds `event_id`
  and `call_id` (UUIDs), `occurred_at`, `operator` (an `OperatorRef`),
  `action` (an enum), an optional object (`EdgeGlobalRef` or
  `CaptureSessionGlobalRef`), and a required `detail` of
  `OperatorActionAttempted` or `OperatorActionCompleted`. The attempt has
  no field of its own. The completion holds an `outcome` (succeeded,
  denied, failed) and, unless it succeeded, an `error_type` from
  `telemetry.ErrorType`. Why: `event/` admits durable
  stream records that are no entity's transition
  (`spec/proto/flowseer/event/README.md`), and a deliberately partial family
  says so in its file comment (`docs/conventions/protobuf.md:52-65`).
- The record carries no tenant. The tenant is a token of the subject, as on
  the device audit stream. Why: tenancy is ambient
  (`docs/conventions/protobuf.md:194-207`). The operator authorization
  record's "tenant" in its list of what is recorded is that token.
- Storage is one stream, `FLOWSEER_OPERATOR_ACTIONS`, on the central
  account, bound to `flowseer.*.operator.action.*`. A record goes to
  `flowseer.<tenant>.operator.action.<action>`, where the action token is
  the enum value's name in lower case without its prefix, such as
  `setup_key_issue`. Why: central's audit streams take a wildcard in the
  tenant position
  (`docs/architecture/2026-09-30-operator-authorization-direction.md:265-268`),
  so "per-tenant" is a subject and not a stream per tenant. The binding
  overlaps no other stream's, and an edge publishes only under
  `flowseer.<tenant>.edge.<edge>` (`EdgeSubtree` in
  `src/modules/edgebus/subjects.go`), so no edge can write a record.
- Retention is by limits: file storage, 64 MiB, at most 10,000 records per
  subject, the oldest discarded first (`DiscardOld` is the zero value,
  `nats@/jetstream/stream_config.go:594-596`), no maximum age, and the
  audit stream's duplicate window. Both limits are `HubConfig` fields. Why
  the cap per subject: reads are recorded too, and a caller who may only
  view edges could otherwise push the record of who minted a key out of a
  shared limit. With a subject per tenant and action, a flood evicts only
  its own tenant's records of that action. A file stream's `MaxBytes` is
  reserved against its account's limit
  (`github.com/nats-io/nats-server/v2@v2.15.0/server/jetstream.go:2603-2607`),
  so 64 MiB beside the audit stream's 256 MiB leaves the buckets 192 MiB of
  the central account's 512 MiB where they had 256. `defaultCentralBudget`
  stays, since
  `docs/solutions/architecture-patterns/per-account-jetstream-disk-budgets-reserve-against-the-server-store-ceiling.md`
  sizes edges against it.
- It differs from the device audit stream (`src/modules/edgebus/hub.go:371-378`)
  in three ways: its subject names a tenant and an action where that one
  names a device, it caps each subject, and its record names a person,
  which `DeviceOperationEvent` cannot.
- A recorded call writes two records: an attempt before the handler runs,
  and a completion after. When the attempt cannot be written the call
  answers `Unavailable` with `actiontrail/unavailable` and the handler does
  not run. A completion that cannot be written is logged and the response
  is returned. Why: the durable record comes before the state it describes
  is released (`docs/architecture/2026-09-05-verified-device-access-direction.md:125-128`).
  A refused caller can retry, so nothing is stranded
  (`docs/solutions/architecture-patterns/state-a-transient-refusal-must-not-block-is-state-nothing-retries.md`),
  and a completion cannot undo an action that ran.
- The trail records every call authorization admitted, from a table in the
  interceptor:

  | Procedure | Action | Object |
  | --- | --- | --- |
  | `EdgeAdminService.CreateEdge` | `EDGE_CREATE` | the created edge, on the completion |
  | `EdgeAdminService.IssueSetupKey` | `SETUP_KEY_ISSUE` | `edge` |
  | `EdgeAdminService.RevokeSetupKey` | `SETUP_KEY_REVOKE` | `edge` |
  | `EdgeAdminService.RetireEdge` | `EDGE_RETIRE` | `edge` |
  | `EdgeAdminService.GetEdge` | `EDGE_GET` | `edge` |
  | `EdgeAdminService.ListEdges` | `EDGE_LIST` | none |
  | `CaptureService.CreateCaptureSession` with `authorization.full_payload_requested` | `CAPTURE_FULL_PAYLOAD_CREATE` | `edge`, and the session on the completion |
  | `CaptureService.TailCaptureSession` | `CAPTURE_TAIL` | `session` |
  | `CaptureService.DownloadCaptureSession` | `CAPTURE_DOWNLOAD` | `session` |

  Why an interceptor and a table: a handler can forget a call, and a test
  holds the table to the service descriptor. Why tails and downloads: the
  record requires every capture download in the change that turns
  authorization on, and the capture record gives `download` to "tail or
  download" (`docs/architecture/2026-09-09-remote-packet-capture-direction.md:192`).
  A call an interceptor refuses is not in the trail. An unauthenticated
  one has no principal to name, and recording a refused attempt would let
  a caller with no right in a tenant write into that tenant's trail. The
  telemetry interceptor logs those at WARN
  (`internal/host/interceptor.go:144-153`). A denial inside the handler,
  which is the `full_payload` check, is recorded with outcome denied. The
  completion names what the handler returned. The authorization
  interceptor outside it can still drop that response for an obligation
  violation, and the trail then says succeeded for a call that answered
  `Internal`. Only a handler defect produces that, and the telemetry log
  holds it.

### Questions handed from phases 1 and 2

- A suspended tenant's organization yields no `claimed` tenant: the
  verifier counts a binding only when its tenant is
  `TENANT_LIFECYCLE_ACTIVE`. Why: active is "permitted to hold resources"
  (`spec/proto/flowseer/model/identity/v1/tenant.proto:26-33`), and a tenant
  is suspended to keep its trail and keys intact
  (`docs/conventions/protobuf.md:144-147`), which is no reason to keep
  operating it. A partner or platform admin still reaches it through the
  other terms of `member`. Nothing suspends a tenant yet: `TenantService` is
  not served.
- A key endpoint that answers 200 with a body that is no key set yields
  `authn/unavailable`. The verifier classifies on go-oidc's error alone and
  drops the transport test. Why: go-oidc wraps every failure to obtain the
  key set as `fetching keys` (`oidc@/jwks.go:178`), the decode failure
  included (`:327`).
- The platform claim name has no fallback. An empty name in
  `authn.Options` yields no platform principal. Why: the schema requires the
  field (`spec/proto/flowseer/store/device/v1/service_config.proto:189-193`)
  and the host passes it, so the fallback to the issuer's claim name is a
  second meaning nothing reaches.
- The phase 2 plan's sentence that first-party code needs no goroutine is
  stale and changes nothing here. The operator authorization record never
  carried it.

### Tests

- Tests that run the service pass `authztest.Engine` through
  `host.Options.Engine` and a real token from `authntest.Issuer`, an
  `httptest` TLS issuer trusted through `authentication.ca_file`. The tagged
  tier leaves `Options.Engine` unset and reaches a real OpenFGA through the
  configuration. Why: every existing integration test calls an operator RPC,
  and a container per test is not a default-tier cost. `internal/host` is
  importable only inside the device service, and `cmd/device` passes no
  options.
- After the third fix round, one more round changes tests and documentation
  and no production behavior: the property test's generator, a failing test
  for the edge-facing recover interceptor entry, the record's line
  citations, and the runbook token test. The review then records `accept
  after fixes`. Phase 4 follows, and its `TenantService` closes the missing
  tenant-record path. (decided by the user, 2026-10-04)

## Requirements

Parent requirements 1, 2, 4, 5, 7, 9, 10, and 11 hold against the running
device service, and the edge-facing services behave as before. For this
phase that means:

1. An operator RPC needs a valid bearer token, a request that passes its
   schema rules, and a tenant the principal is a member of, checked in that
   order. Against a running service, `GetEdge` with no `Authorization`
   header and an empty request answers
   `CodeUnauthenticated`. With a token and `X-FlowSeer-Tenant: B` for a
   principal the engine holds as `member` of A only, it answers
   `CodePermissionDenied`. With a token, tenant A, and an empty
   `GetEdgeRequest`, it answers `CodeInvalidArgument` with
   `host/invalid-request` and the engine records no query.
2. Edge-facing services need no token and no engine. With the engine
   endpoint naming a closed port, `EdgeService.Enroll` with an unknown
   setup key gets the answer it gets with the engine reachable, while
   `GetEdge` answers `CodeUnavailable` with `authz/unavailable`.
3. A configuration without `authentication` or without `authorization`
   fails `LoadConfig` with `host/config-invalid`. A key file that is absent
   makes `host.Run` return `credential/not-found` before the listener
   binds.
4. `TailCaptureSession` and `DownloadCaptureSession` check
   `capture_session:<id>#download` and `capture_session:<id>#tenant` on
   their request message. `DownloadCaptureSession` for session S records
   the membership query and those two. With either denied the caller gets
   `CodePermissionDenied`, the handler does not run, and no message is
   sent. A request with no `session` under the operator chain answers
   `CodeInvalidArgument` with `host/invalid-request`. A client stream, or a
   stream under any other rule mode, answers `CodePermissionDenied` with
   `authz/streaming-unsupported`.
5. A response returned while a `Require` or `Filter` of the call has not
   returned is dropped with `Internal` and `authz/obligation-violation`.
   A handler that returns `authz.Abandon(ctx, err)` passes `err` to the
   caller, and a response after `Abandon` is dropped the same way.
   `ListEdges` with the edge store failing answers `CodeUnavailable` with
   `edgestore/store`.
6. A filtered list holds only what the caller may see, and its token
   neither skips nor repeats an entry. With `capture` on edge E1 and not on
   E2, three sessions on each, and `page_size: 2`, the pages of
   `ListCaptureSessions` hold E1's three sessions once each and none of
   E2's. `ListEdges` for a caller with `view` on one of two edges returns
   that edge.
7. `CreateCaptureSession` with `full_payload_requested` needs
   `tenant#full_payload`. A caller without it gets `CodePermissionDenied`
   and no session is stored. The same request with headers only succeeds.
8. Every identity a handler stores is the principal's. A
   `CreateCaptureSession` whose request carries
   `requested_by { issuer: "https://other" subject: "x" }` stores the
   token's issuer and subject. `ApplyInterfaceDescription` stores them in
   `mutation.intent.actor.operator`. Two intents that differ only in the
   issuer have different digests.
9. Relationships follow records. After `CreateEdge` returns, `GetEdge` on
   the new edge by the same admin succeeds. After `DeleteCaptureSession`
   the engine holds no tuple on `capture_session:<id>`. With
   `edge:E#tenant@tenant:T` deleted from the engine, one pass restores it.
   A stored `edge:X#tenant@tenant:T` with no record is deleted by one pass,
   `edge:E#tenant@tenant:T2` beside the true `tenant:T` is deleted, and
   `edge:E#capture@user:u` is left as it is. A session created while a pass
   is scanning keeps its three tuples.
10. Every recorded call leaves an attempt and a completion in the trail.
    `IssueSetupKey` by issuer I and subject S in tenant T on edge E puts
    two records on `flowseer.T.operator.action.setup_key_issue`, both naming
    I, S, and E under one `call_id`, the second with outcome succeeded.
    With the stream refusing the publish, `IssueSetupKey` answers
    `CodeUnavailable` with `actiontrail/unavailable` and the edge's setup
    key is unchanged. A headers-only `CreateCaptureSession` writes none.
11. The verifier refuses a subject of 257 characters with
    `authn/token-invalid`, yields no tenant for an organization bound to a
    suspended tenant, answers `authn/unavailable` when the key endpoint
    returns 200 and `not json`, and yields `Platform: false` when the
    platform claim name is empty.
12. Against a real OpenFGA the model's terms hold through the running
    service. A token whose organization claim omits tenant A gets
    `CodePermissionDenied` on `GetEdge` in A while `tenant:A#enrolled`
    still holds. A platform admin gets `GetEdge` in a tenant it is not
    enrolled in, and `CodePermissionDenied` on a full-payload
    `CreateCaptureSession` until `tenant:A#full_payload` is written.

## Out of scope

- Serving `TenantService`, the membership, role, and grant APIs, and the
  platform admin bootstrap: phase 4. Until then a deployment writes
  `enrolled` and role tuples to OpenFGA itself, as `deploy/lab/README.md`
  shows.
- Recording who abandoned or resolved a mutation. The Goal's trail covers
  `EdgeAdminService` and capture, and `DeviceOperationEvent` has no
  operator field (`spec/proto/flowseer/event/access/v1/operation_event.proto`).
- A bound on an operator request body. Connect decodes the request before
  any interceptor runs, so an unauthenticated caller can still send a large
  one, and `src/services/device/README.md` keeps saying so.
- Check caching and a consistency preference on checks: the parent.
- Trust: a token and a request come from callers nobody trusts, and every
  malformed one is in scope. The projector reads FlowSeer's own records and
  the deployment's own OpenFGA over TLS, both trusted, as are the operators
  who write the configuration and the registry.

## Units

### U1. Operator identity in the schema and the intent digest

Files: `spec/proto/flowseer/model/identity/v1/operator.proto`, `spec/proto/flowseer/model/identity/v1/README.md`, `spec/proto/flowseer/model/access/v1/operation.proto`, `spec/proto/flowseer/model/capture/v1/capture_session.proto`, `spec/proto/flowseer/model/capture/v1/README.md`, `spec/proto/flowseer/api/device/v1/device_service.proto`, `spec/proto/flowseer/store/device/v1/README.md`, `generated/go/proto/flowseer/model/identity/v1/operator.pb.go`, `generated/go/proto/flowseer/model/access/v1/operation.pb.go`, `generated/go/proto/flowseer/model/capture/v1/capture_session.pb.go`, `generated/go/proto/flowseer/api/device/v1/device_service.pb.go`, `docs/conventions/protobuf.md`, `docs/architecture/2026-08-20-network-model-structure-direction.md`, `src/services/device/internal/journal/journal.go`, `src/services/device/internal/journal/journal_test.go`, `test/conformance/proto/model_identity_rules_test.go`, `test/conformance/proto/model_access_rules_test.go`, `test/conformance/proto/model_capture_rules_test.go`, `test/conformance/proto/api_device_rules_test.go`, `src/edge/agent/internal/capture/capture_test.go`, `src/services/device/internal/authz/authz_test.go`, `src/services/device/internal/captureapi/artifact_invariant_internal_test.go`, `src/services/device/internal/captureapi/edge_service_test.go`, `src/services/device/internal/captureapi/operator_service_test.go`, `src/services/device/internal/captureapi/store_test.go`, `src/services/device/internal/deviceapi/deviceapi_test.go`, `src/services/device/internal/dispatchapi/relay_test.go`, `src/services/device/internal/drift/drift_test.go`, `src/services/device/internal/host/validation_test.go`, `src/services/device/test/integration/capture_test.go`, `src/services/device/test/integration/e2e_test.go`
After: none
Change: `OperatorRef` gains `issuer`, required, a URI of 1 to 2048
characters, and its comment names a person by issuer and subject.
`MutationIntent.actor` and `CaptureAuthorization.requested_by` lose
`required`, and their comments say central sets the field from the
authenticated caller, or from its own reason for an intent it admits
itself, and replaces a value a request carries. `AbandonMutationRequest` and
`ResolveDesynchronizationRequest` drop `actor` and reserve number 3 and
the name. `actorPart` follows the Decisions. The three READMEs, the
`OperatorRef` sentence in `docs/conventions/protobuf.md` ("stable
subject"), and a dated amendment to the network model structure record's
`identity/v1` line say issuer and subject. Handlers are unchanged in this
unit: they still store what the caller sent. Every test fixture that
builds an `OperatorRef` gains an issuer, since a fixture passes
`protovalidate.Validate` (`docs/code-style.md:462-465`). The files after
the conformance tests in `Files:` are that inventory. It includes
`internal/authz/authz_test.go`, whose fixtures are validated
(`authz_test.go:254-262`, `:331`), so U2 follows this unit.
Tests: the conformance files hold `OperatorRef` with both fields accepted,
and refused without an issuer, with `issuer: "not a uri"`, and with a
2049-character issuer. A `MutationIntent` and a `CaptureAuthorization`
without the identity field are accepted, and so are the two device requests
without `actor`. `TestEveryProjectedIntentFieldChangesTheDigest` gains the
issuer case of requirement 8, and the same key resubmitted with another
issuer answers `journal/idempotency-mismatch`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/model spec/proto/flowseer/api/device spec/proto/flowseer/store/device/v1/README.md docs/conventions/protobuf.md docs/architecture/2026-08-20-network-model-structure-direction.md src/services/device/internal/journal src/services/device/internal/authz src/services/device/internal/captureapi src/services/device/internal/deviceapi src/services/device/internal/dispatchapi src/services/device/internal/drift src/services/device/internal/host src/services/device/test/integration src/edge/agent/internal/capture test/conformance/proto`

### U2. Streams, obligations, and the relationship interface

Files: `src/services/device/internal/authz/authz.go`, `src/services/device/internal/authz/interceptor.go`, `src/services/device/internal/authz/obligation.go`, `src/services/device/internal/authz/authz_test.go`, `src/services/device/internal/authz/authztest/engine.go`, `src/services/device/internal/authz/authztest/engine_test.go`, `docs/architecture/2026-09-30-operator-authorization-direction.md`
After: U1
Change: `authz` declares `Relations` with
`Write(ctx, writes, deletes []Tuple) error`,
`Read(ctx, object string) ([]Tuple, error)`, and
`Scan(ctx, func(Tuple) error) error`, and `Engine`. `Write` is idempotent:
a tuple already stored and a delete of an absent one are no error.
`Interceptor.Admit(ctx, tenantID)` holds what `WrapUnary` does inline
today (`interceptor.go:122-151`): it needs a principal, validates the
tenant id, makes the membership check, and returns a context carrying the
tenant and a tracker. Both wrappers call it, and a test in another package
calls it to prepare a context for a handler it invokes directly.
`WrapStreamingHandler` applies requirement 4 by the Decisions, with the
refusals of `WrapUnary` for a missing rule, principal, or tenant header,
and answers `Internal` when the handler returns nil after a failed
`Require` or `Filter`. The tracker and `Abandon` apply requirement 5.
`Abandon` returns the `Internal` that `Require` returns on a context the
interceptor did not prepare, and when `err` is nil. `authztest.Engine`
implements `Engine` over a set of tuples: `Check` is true for a stored or
contextual tuple equal to the query, or for a grant added through
`Grant(user, relation, objectType)`, which matches every object of the
type. It records queries, fails calls on request, and is safe for
concurrent use. The record gains a dated amendment: the streaming rule
replaces "Streaming RPCs are refused until their rules are designed", and
the rule-mode table's rows name the in-flight check and `Abandon`.
Tests: `authz_test.go` serves the generated `CaptureService` handler and
adds requirement 4's cases, each asserting the recorded queries before the
outcome: the allowed download, each of its three queries denied in turn,
a failing checker (`Unavailable`), no principal, no tenant header, and a
request with no session (`authz/no-object-id`, since this package mounts
no validation). A client-stream handler built with `connect.WithSchema`
of `TailCaptureSession`'s descriptor is refused, which only the
stream-type test produces. Requirement 5: a handler that starts `Require`
through `spawn.Go` against a checker that blocks and returns a response
gets `Internal`, beside the same handler joining the check. A handler
returning `Abandon(ctx, err)` yields `err`, one answering after it yields
`Internal`, and `Abandon` on a bare context yields `Internal`.
`engine_test.go` covers the idempotent write and delete, a
grant, a contextual tuple, `Scan`, and concurrent use under `-race`. A new
case counts once its failure against the code before the change has been
quoted (`docs/code-style.md:466-473`).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/authz docs/architecture/2026-09-30-operator-authorization-direction.md`

### U3. The adapter verifies lazily and writes relationships

Files: `src/services/device/internal/authz/openfga/checker.go`, `src/services/device/internal/authz/openfga/relations.go`, `src/services/device/internal/authz/openfga/checker_test.go`, `src/services/device/internal/authz/openfga/relations_test.go`, `src/services/device/test/integration/openfga_env_test.go`, `src/services/device/test/integration/openfga_checker_test.go`, `src/services/device/test/integration/openfga_relations_test.go`, `src/services/device/test/integration/openfga_bench_test.go`
After: U2
Change: `openfga.New` validates the ids and the endpoint, reads the key and
CA files, builds the connection, and makes no call. `Verify(ctx)` runs the
start check and keeps phase 2's codes (`authz/engine-store-mismatch`,
`authz/engine-model-mismatch`, and the three call classes). Every method
verifies first by the Decisions: one check in flight at a time, a success
kept for the connection's life, a failure kept for 5 s on an injectable
clock. The type implements `authz.Engine`. `Write` drops repeats, which
the server refuses in one request (`openfga@/pkg/server/commands/write.go:205-207`),
and splits into calls of 100, each with the model id and both `ignore`
options. A side with no tuple is left nil, since a side that is set must
hold one (`api@/openfga_service.pb.validate.go:1704-1707`), and a call
with nothing to send is not made. A tuple the adapter's identifier rules
reject fails the write with a new code, `authz/engine-invalid-tuple`, and
no call. `Read` filters on the object and `Scan` sends no tuple key.
Both page at 100 until the continuation token is empty and send
`HIGHER_CONSISTENCY`. `Aborted` maps to `authz/engine-conflict`,
retryable. The three calls share the client interceptor, so each is one
span and one `rpc.client.call.duration` point as the checks are.
Tests: on the fake gRPC server of `checker_test.go`, the first `Check`
makes `GetStore`, `ReadAuthorizationModel`, and `Check`, and 20 concurrent
first calls make one start check. With `GetStore` answering `Unavailable`
a call fails with `authz/engine-unreachable`, a second call inside 5 s
makes no start call, and after the clock passes 5 s with the server
healthy the call succeeds. With the model one relation short every method
fails with `authz/engine-model-mismatch` and the server sees no `Check`,
`Write`, or `Read`. A write of 250 tuples makes calls of 100, 100, and 50,
each carrying both options. `Aborted` yields `authz/engine-conflict`. A
two-page `Read` and `Scan` return both pages. Those status codes are this
plan's reading, so `openfga_relations_test.go`, tagged `authz_integration`,
repeats them on the real server: a duplicate write and a delete of an
absent tuple succeed, a write to `capture_session#manage` fails with
`authz/engine-protocol`, `Scan` returns 250 written tuples, and `Check` is
true after a `Write` and false after its delete, which is the stop
condition. Nothing in this unit produces a real `Aborted`.
`openfga_checker_test.go` and the benchmark move their start-check
assertions from `New` to `Verify`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/authz/openfga src/services/device/test/integration`

### U4. Verifier refinements and the test issuer

Files: `src/services/device/internal/authn/verifier.go`, `src/services/device/internal/authn/verifier_test.go`, `src/services/device/internal/authn/verifier_internal_test.go`, `src/services/device/internal/authn/interceptor_test.go`, `src/services/device/internal/authn/authntest/issuer.go`
After: none
Change: the verifier applies requirement 11 by the Decisions and drops
`replayTransport.HasOutage` when nothing else calls it.
`authntest.Issuer` is the `httptest` TLS issuer the verifier tests build
today, moved to a package other tests can import: it serves discovery and
a key set, signs a token from a claim map with `crypto/rsa` alone, writes
its certificate to a file for `ca_file`, and can replace its key endpoint's
answer.
Tests: one case per clause of requirement 11, each one property from the
accepted token: a 256-character subject accepted and 257 refused, a
binding whose record is `TENANT_LIFECYCLE_SUSPENDED` beside the same
record active, the key endpoint answering 200 with `not json` beside 200
with an empty key set (`authn/token-invalid`, since the issuer then says
it has no such key), and the platform claim name empty beside set. The
existing cases, those of `interceptor_test.go` included, run against
`authntest.Issuer` in place of `newTestOidcServer`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/authn`

### U5. The operator action trail

Files: `spec/proto/flowseer/event/operator/v1/operator_action_event.proto`, `spec/proto/flowseer/event/operator/v1/README.md`, `spec/proto/flowseer/event/README.md`, `spec/proto/flowseer/model/identity/v1/README.md`, `spec/proto/flowseer/model/edge/v1/README.md`, `spec/proto/flowseer/model/capture/v1/README.md`, `generated/go/proto/flowseer/event/operator/v1/operator_action_event.pb.go`, `test/conformance/proto/layering_test.go`, `test/conformance/proto/event_operator_rules_test.go`, `docs/architecture/2026-08-20-network-model-structure-direction.md`, `src/modules/edgebus/subjects.go`, `src/modules/edgebus/hub.go`, `src/modules/edgebus/edgebus_test.go`, `src/modules/edgebus/README.md`, `src/services/device/internal/actiontrail/actiontrail.go`, `src/services/device/internal/actiontrail/interceptor.go`, `src/services/device/internal/actiontrail/actiontrail_test.go`
After: U1
Change: the schema follows the Decisions: a top-level `OperatorAction`
enum with the nine actions, `OperatorActionOutcome`, the two detail
messages, and the event, each enum required, defined, and not zero, and
`error_type` of at most 128 characters set exactly when the outcome is not
succeeded. `layering_test.go` gains
`"event/operator": {"model/identity", "model/edge", "model/capture"}`. The
READMEs gain the package and its `Imported by` lines, and the network
model structure record a dated amendment adding it to the tree.
`edgebus` gains `OperatorActionStream`, `OperatorActionSubject(tenant,
action)`, and the stream in `createStores` by the Decisions, with
`HubConfig.OperatorActionMaxBytes` and `OperatorActionMaxPerSubject`. The
comment on `defaultCentralBudget` names this stream beside the journal
and the audit stream.
`actiontrail.NewInterceptor` takes a publisher with the signature of
`auditapi.JetStreamPublisher.Publish`, a clock, and a logger. For a
procedure in the Decisions' table it reads the principal and the tenant
from the context (`Internal` with `actiontrail/unprepared` when either is
missing), publishes the attempt under its `event_id` as message id, calls
the handler, and publishes the completion on a context detached from the
caller's with a 5 s timeout. A unary completion reads the created object
from the response (`edge.config.ref`, `session.config.ref`). A stream
publishes its attempt after the first
successful `Receive` and its completion when the handler returns. Any
other procedure passes through.
Tests: `event_operator_rules_test.go` holds the event's rules, each
refusal one property from an accepted event.
`TestLayeringViolationRules` gains the allowed import and one refused.
`actiontrail_test.go` serves the generated `EdgeAdminService` and
`CaptureService` handlers behind an interceptor that injects a principal
and a tenant, with a recording publisher: requirement 10's example, a
publisher that fails the attempt (handler not run) and one that fails
only the completion (response returned, one log record), a handler error
recorded as failed with its `error_type`, `PermissionDenied` as denied,
`CreateEdge`'s completion naming the edge in the response, a headers-only
and a full-payload `CreateCaptureSession`, a tail and a download whose
attempt is published before the handler's first `Send`, and a call with
no tenant in the context (`actiontrail/unprepared`, handler not run).
`TestEveryEdgeAdminProcedureIsRecorded` walks the service descriptor,
asserts each method has a row, and asserts the literal 6
(`docs/solutions/conventions/a-combinatorial-table-count-guard-must-assert-a-literal.md`).
One test starts a hub with `OperatorActionMaxPerSubject: 2`, publishes
three records on one subject and one on another, and reads back the
newest two and the one, which also tests the `HubConfig` fields from
outside the module. `edgebus_test.go` asserts the stream exists beside the
audit stream under the default budget with a KV write still accepted, and
that a hub whose `CentralBudgetBytes` is below the two streams' sum fails
`StartHub`, which pins the reservation.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/event spec/proto/flowseer/model test/conformance/proto docs/architecture/2026-08-20-network-model-structure-direction.md src/modules/edgebus src/services/device/internal/actiontrail`

### U6. The relationship projector

Files: `src/services/device/internal/projector/projector.go`, `src/services/device/internal/projector/reconcile.go`, `src/services/device/internal/projector/projector_test.go`, `src/services/device/internal/edgestore/store.go`, `src/services/device/internal/edgestore/store_test.go`, `src/services/device/internal/captureapi/store.go`, `src/services/device/internal/captureapi/store_test.go`
After: U1, U2
Change: `edgestore.Store.All` returns every edge id with the tenant of its
record key, and `captureapi.Store.EachSession` yields every session with
its tenant, as `SweepExpired` walks the bucket (`store.go:667-690`).
`projector.New` takes an `authz.Relations`, three sources as small
interfaces (the edge store, the registry, the capture store), an interval,
a logger, and a `Reconciled` callback. `Sync(ctx, Object{Type, ID,
Tenant})` follows the Decisions. A session's tenant comes from the
argument, else from the stored `tenant` tuple, and with neither the
desired set is empty. A write that fails with `authz/engine-conflict` is
re-read and retried, three times in all. `Reconcile(ctx)` is one pass and
returns counts of objects repaired. `Run(ctx)` passes at start and on the
interval by the Decisions, logs a failed pass with `error.type`, and
returns nil when the context ends.
Tests: `projector_test.go` uses `authztest.Engine` and fake sources. One
case per row of the Decisions' table, with a session lacking an issuer
getting two tuples. Requirement 9's drift cases, each asserting the
engine's tuples before the pass and after. The race: the fake engine's
`Scan` adds a session to the source and writes its tuples before it
yields, and the pass leaves them. A deleted session's `Sync` removes its
three tuples and no grant. An engine failing the first `Write` with
`authz/engine-conflict` is retried, and one failing with
`authz/engine-unreachable` returns it. `Run` calls `Reconciled` after the
first pass and keeps going after a failed one. The store tests cover `All`
and `EachSession` over two tenants.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/projector src/services/device/internal/edgestore src/services/device/internal/captureapi`

### U7. Enforcement on

Files: `spec/proto/flowseer/store/device/v1/service_config.proto`, `spec/proto/flowseer/store/device/v1/README.md`, `generated/go/proto/flowseer/store/device/v1/service_config.pb.go`, `spec/proto/flowseer/api/edge/v1/edge_admin_service.proto`, `spec/proto/flowseer/api/capture/v1/capture_service.proto`, `generated/go/proto/flowseer/api/edge/v1/edge_admin_service.pb.go`, `generated/go/proto/flowseer/api/capture/v1/capture_service.pb.go`, `spec/proto/flowseer/model/edge/v1/README.md`, `src/services/device/internal/deviceapi/service.go`, `src/services/device/internal/deviceapi/apply.go`, `src/services/device/internal/deviceapi/resolve.go`, `src/services/device/internal/deviceapi/deviceapi_test.go`, `src/services/device/internal/edgeapi/admin.go`, `src/services/device/internal/edgeapi/admin_test.go`, `src/services/device/internal/captureapi/operator_service.go`, `src/services/device/internal/captureapi/operator_service_test.go`, `src/services/device/internal/host/host.go`, `src/services/device/internal/host/serve.go`, `src/services/device/internal/host/handle.go`, `src/services/device/internal/host/interceptor.go`, `src/services/device/internal/host/interceptor_test.go`, `src/services/device/internal/host/validation.go`, `src/services/device/internal/host/validation_test.go`, `src/services/device/internal/host/config.go`, `src/services/device/internal/host/config_test.go`, `src/services/device/internal/host/host_test.go`, `src/services/device/internal/host/certificate_test.go`, `src/services/device/test/integration/fixture_test.go`, `src/services/device/test/integration/e2e_test.go`, `src/services/device/test/integration/capture_test.go`, `src/services/device/test/integration/runbook_test.go`, `src/services/device/test/integration/bootstrap_env_test.go`, `src/services/device/test/integration/bootstrap_test.go`, `src/services/device/test/integration/lab_fixtures_test.go`, `deploy/lab/central.textproto`, `src/services/device/README.md`, `docs/architecture/2026-09-30-operator-authorization-direction.md`
After: U1, U2, U3, U4, U5, U6
Change: `DeviceServiceConfig` requires both sections, reserves number 10
and `dev_tenant`, and `ServiceIntervals` gains `relationship_reconcile`,
at least 1 s, where unset means 10 minutes. `host.Options` gains `Engine`
and `Reconciled`. `host.Run` builds the adapter through `openfga.New`
unless `Options.Engine` is set, and the issuers' HTTP client from
`ca_file`, before the runtime starts, and closes the adapter on return.
The hub attempt opens the `tenants` bucket and builds the tenant store and
the projector. `mux` builds two interceptor lists by the Decisions. The
verifier takes the configured issuers, `platform_admin`'s issuer,
organization, and claim name, and `tenantstore.Store.LookupByOrg`. The
validating interceptor gains a variant that validates every message a
streaming handler receives, used on the operator list. `TenantInterceptor`
is deleted. A seventh module, `projector`, is declared last and runs
`projector.Run`.

`deviceapi` sets `intent.actor` to the principal's issuer and subject in
`ApplyInterfaceDescription` and in the `replace` arm before anything reads
the intent, and answers `Unauthenticated` with no principal.
`edgeapi.AdminService` and `captureapi.OperatorService` take a projection
hook, `func(ctx, objectType, id string)`, which the host fills with a call
to `Sync` that names the context's tenant and logs a failure. `CreateEdge`
calls it after `IndexEdge`.
`CreateCaptureSession` replaces `requested_by` with the principal, calls
`authz.Require(ctx, "full_payload", "tenant", tenantID)` when full payload
is requested and returns its error, and calls the hook after
`CreateSession`. Its own check on `requested_by` goes.
`DeleteCaptureSession` calls the hook after `DeleteSession`.

`ListEdges` and `ListCaptureSessions` apply requirement 6. Each takes the
candidates after the token in id order, in chunks of the page size, and
filters a chunk through `authz.Filter` (`view` on the edge ids, `capture`
on the sessions' distinct edge ids). It stops when the page is full or
500 candidates were examined. The token names the last id returned when
the page filled, else the last id examined, and it is set while candidates
remain. Each handler keeps its token form: `ListEdges` the base64 of the
id (`encodePageToken`), `ListCaptureSessions` the id itself. A store error before the first `Filter` returns through
`authz.Abandon`. Both response comments say a page can be short or empty
while `next_page_token` is set, where they say today that empty means
exhausted.

The device README's Deployment section describes the enforced surface,
the action trail, and seven modules, and its Layout gains
`internal/projector`, `internal/actiontrail`, and the two test packages.
The paragraph "Minting leaves no trail" in the edge model README goes.
`deploy/lab/central.textproto` loses its comment that the operator API
has no authorization check. The operator authorization record gains a
dated amendment holding the Decisions under Interceptors and start,
Projector, Identity, and Operator action trail.

The fixtures change with it. `central.start` in `fixture_test.go` starts
an `authntest.Issuer`, writes both sections, passes one
`authztest.Engine` kept across restarts, waits on `Reconciled`, and hands
out operator clients through an `http.Client` that adds the token and the
tenant header. The engine holds `tenant:<T>#member` for the test principal
and type-wide grants for the relations the suite uses, and answers
`<object>#tenant@tenant:<T>` only from what the projector wrote. The two
tests that restart central with another `dev_tenant`
(`startCentralWithDevTenant`) name the other tenant in the header instead.
Tests: `host_test.go` holds requirements 1, 2, and 3 against `host.Run`,
with requirement 2's engine section naming a closed port and no
`Options.Engine`. With an engine that fails `Write`, `CreateEdge` returns
the edge and its setup key, the record is stored, and the log holds one
record with `error.type`. `config_test.go` replaces `TestPlatformAdminAndDevTenant`
with the required sections, each missing in turn, and a `dev_tenant` line
that parses and changes nothing.
`validation_test.go` adds a stream message refused under the operator
variant and passed under the edge one. `deviceapi_test.go` holds
requirement 8's intent cases, including a `replace` arm.
`admin_test.go` and `operator_service_test.go` prepare their contexts
through `Interceptor.Admit` over `authztest.Engine`: requirement 6's
examples with a denied edge in each list handler, seeded with ids that
put a visible entry after the one that fills a page, an examined-candidates
case where 501 edges with none visible return an empty page and a token,
requirement 5's failing store, requirement 7 with the one recorded query
`tenant:<T>#full_payload`, requirement 8's capture case, and the hook
called once by each creating and deleting handler and not when the store
write failed. In `e2e_test.go` and `capture_test.go` the existing suites
pass through the enforced surface, which is requirement 9's write-through
on every edge and session they create, and new cases read the operator
action stream as `auditRecords` reads the audit stream for requirement
10's example and for a download. A tail and a download by a principal
without `download` answer `CodePermissionDenied`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/store/device/v1 spec/proto/flowseer/api/edge spec/proto/flowseer/api/capture spec/proto/flowseer/model/edge/v1/README.md test/conformance/proto src/services/device deploy/lab docs/architecture/2026-09-30-operator-authorization-direction.md`

### U8. The real-engine tier and the lab run

Files: `src/services/device/test/integration/authz_enforcement_test.go`, `src/services/device/test/integration/README.md`, `deploy/lab/README.md`
After: U7
Change: the test file carries `//go:build authz_integration`. It starts
phase 2's OpenFGA environment, writes the embedded model, and runs
`host.Run` with an `authorization` section naming the container and no
`Options.Engine`, an `authntest.Issuer`, and two tenants created through
the tenant store with their organization bindings. It writes `enrolled`,
role, and platform tuples through the environment's helper, as a
deployment does until phase 4. The integration README names the file with
the tier's command. The lab README's run gains the steps an operator call
now needs: the store block in `central.textproto`, a tenant, the
`enrolled` and `admin` tuples for the lab user by `curl`, a token, and
`GetEdge` with `X-FlowSeer-Tenant`. It says the same call without the
header answers `InvalidArgument`.
Tests: `TestEnforcementAgainstTheRealEngine` holds requirement 12, and
requirement 9's first sentence with the real model: an admin creates an
edge and reads it back, which needs `edge:<id>#tenant` in the engine and
`admin from tenant` in the model. A member with `edge:E1#capture` and no
tenant role lists only E1's sessions. `go vet` covers the tagged file.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/test/integration deploy/lab`

Waves: U1 U4 | U2 U5 | U3 U6 | U7 | U8

## Verification

```bash
go tool -modfile=tools/buf/go.mod buf lint
go tool -modfile=tools/buf/go.mod buf generate
go test -race ./src/services/device/... ./src/modules/edgebus/... ./src/edge/agent/internal/capture/... ./test/conformance/...
go vet -tags=authz_integration ./src/services/device/test/integration/
go test -race -tags=authz_integration ./src/services/device/test/integration/
.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer src/services/device src/modules/edgebus src/edge/agent/internal/capture test/conformance/proto deploy/lab docs/conventions/protobuf.md docs/architecture
```

The tagged run needs Docker and runs the package whole
(`docs/solutions/conventions/a-gate-selected-by-name-stops-running-silently.md`).
The run in `deploy/lab/README.md` is done once by hand.

## Definition of done

- [ ] Verifier green for every changed path, and the tagged run passes.
- [ ] `src/services/device/README.md`, the store and event package
      READMEs, `src/modules/edgebus/README.md`, `deploy/lab/README.md`,
      `docs/conventions/protobuf.md`, and both amended records updated in
      the change that invalidates them.
- [ ] This plan's `status` set with an outcome note under its title, and
      the parent's `Landed:` line for U3 filled.
- [ ] No plan labels in code.

## Open questions

- The Goal says relationships derive from records in JetStream KV. For a
  device that holds for the tenant and not for existence, which the
  registry file answers until an inventory service stores devices.
- Calls refused before a handler runs are outside the trail. If the plan's
  owner reads "every call of `EdgeAdminService`" to include a denied
  member's attempt, the trail interceptor moves between validation and the
  object check, and the per-subject cap is what bounds the flood.
- The trail's 64 MiB is shared by all tenants. Enough tenants at the
  per-subject cap exceed it, and the oldest record of any tenant then goes
  first. Phase 4 serves `TenantService` and sizes it.
- A tail is authorized when it opens and keeps running after a revocation
  until the capture ends.
- For phase 4: a revoking removal that deletes its record first and then
  fails in `Sync` leaves a retry with no record to act on. Phase 4 orders
  those two writes. It also adds the relations a tenant record owns
  (`tenant#platform`, `tenant#partner`) to the projector's table. Until
  then the tagged test and a deployment write them by hand.
- `docs/solutions/conventions/a-streaming-connect-handler-must-validate-messages-it-trusts.md`
  cites `validation.go` lines U7 changes. `compound` refreshes it.
- Unverified, for the implementer: a real `Aborted` from two concurrent
  writes of one tuple, and the duration of a pass at the benchmark
  fixture's size.
- No operator-facing path creates a tenant record. `tenantstore.Store.Create`
  has no caller outside tests, and `TenantService` is not mounted
  (`TestTenantServiceIsNotMounted` in
  `src/services/device/internal/host/host_test.go`). `member` needs
  `claimed`, which the verifier yields only from such a record
  (`src/services/device/internal/authn/verifier.go`), so the lab run in
  `deploy/lab/README.md` and the runbook cannot reach an admitted operator
  call on a fresh deployment. U8 asks the lab run for "a tenant". Both
  documents state the limit. Whether phase 3 gains a seed path or phase 4's
  `TenantService` closes it is the plan owner's call.
- An operator call that names a device the registry does not list answers
  `PermissionDenied` with `authz/denied`, where it answered `NotFound`
  before enforcement, because no `device:<id>#tenant` tuple exists for it.
  The Requirements do not name this case.
- From the review, after four fix rounds, still open. The recover
  interceptor on the edge-facing list
  (`edgeInterceptors` in `src/services/device/internal/host/serve.go`) has
  no test that fails when only that entry is removed, and none can be
  written without a production change. `connect.WithRecover`
  (`connectrpc.com/connect` v1.21.0, `recover.go`) is mounted on the same
  handlers and sits inside the interceptor list, so it recovers a handler
  panic first. Only a panic raised in `ValidatingInterceptor`
  (`src/services/device/internal/host/validation.go`) reaches the entry
  alone, and that interceptor calls `protovalidate.Validate` with no seam a
  test can reach. The smallest remedy is an injectable validation function
  there. The plan owner decides between that seam and accepting the entry
  as untested defence.
- From the fourth round's re-review of the tests it changed. None changes
  behavior.
  - `TestReconcileGeneratedWorldsPreserveTuplesOnReadFailure` now enumerates
    the record and grant bits from the world index. `faultTarget` is
    `index % 6`, which shares parity with `edges[0]`, so a read or write
    fault never targets the first edge, device, or session while the first
    edge record is present. The fault is the index block, so each 64-world
    block holds one grant combination. `registryEdge` is derived from the
    edge bits, so a registry edge absent beside a present other edge is not
    generated. The 64-layout literal is global and not per fault, the fault
    counts count labels and not fired faults, and four of the
    predicate-signature literals are 1 for any generator. The "edge record"
    state pair also flips the registry edge.
  - `TestTheRunbookAuthenticationAndTenantContracts` reads only the token
    step, so an `export TOKEN=` line in the runbook's setup block or a
    second recipe appended to the step passes. Its slice is anchored on a
    hard-wrapped sentence, so reflowing that paragraph fails it.
    `TestTheLabReadmeExpectedPresharedKeyAndEdgeResponses` passes with one
    of the two `GetEdge` calls hardcoded.
  - The record's citations of `captureapi/operator_service.go:127-134`,
    `edgeapi/admin.go:145-161`, and `actiontrail/interceptor.go:32-45` each
    hold the described code and a few lines beside it.
    `TestTheDirectionRecordCitationsAndDecisions` checks only that a range
    lies inside its file.
