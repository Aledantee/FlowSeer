---
title: Remote Packet Capture Operator Identity - Plan
type: refactor
date: 2026-09-28
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept after fixes
execution: mixed
amends: docs/architecture/2026-09-09-remote-packet-capture-direction.md, docs/architecture/2026-08-20-network-model-structure-direction.md
---

# Remote Packet Capture Operator Identity - Plan

> Implemented. 1 unit, 2026-09-29T08:05Z to 2026-09-29T08:05Z.

## Goal

A capture session names the person who asked for it the way a mutation intent
does: `CaptureAuthorization` carries an `OperatorRef`, the identity provider's
stable subject, where it carries a free string today. The capture direction
record names the OpenFGA relations each `CaptureService` RPC will be checked
against. The means is moving `OperatorRef` out of `model/access` into a new
leaf package, `flowseer.model.principal.v1`, which both `model/access` and
`model/capture` import. Stop condition: the plan is wrong if the OpenFGA
record lands first and decides a principal type of its own, because then the
capture field should take that type.

## Decisions

- **This plan changes the schema and the records only. `OperatorService` gains
  no authorization check and no audit publisher.** Why: the user decided on
  2026-09-18 that capture follows the repository-wide OpenFGA direction
  ([GOALS.md](../../GOALS.md), the OpenFGA line) instead of carrying its own
  mechanism. Enforcement waits for the record that covers `DeviceService`,
  `EdgeAdminService`, and `CaptureService` together. The gap is accepted in
  [the device service README](../../src/services/device/README.md) under the
  unauthenticated-operator-API section. No OpenFGA code exists in `src/`.
  This plan supersedes
  [the operator authorization plan](2026-09-18-2120-feat-capture-operator-authorization-plan.md),
  whose units contradicted that decision.
- **The download audit event waits for caller authentication as well.** Why:
  `DownloadCaptureSessionRequest`
  (`spec/proto/flowseer/api/capture/v1/capture_service.proto`) carries only the
  session ref. Nothing on the request names the caller, so an event emitted now
  could only record the session's creator, and that is the wrong person
  whenever someone else downloads. The device README also records that the
  audit stream is device-scoped by design and is not an operator action trail.
  The direction record's rule that "a download is itself an event" stays. The
  amendment says it lands with caller identity.
- **`OperatorRef` moves to a new leaf, `flowseer.model.principal.v1`, that
  imports nothing.** Why: `test/conformance/proto/layering_test.go` allows
  `model/capture` to import only `model/edge`, `net/capture`, and `net/key`.
  `model/access` imports `model/inventory`, `model/policy`, and interface
  provenance. The edge agent builds `CaptureAuthorization`
  (`src/edge/agent/internal/capture/capture_test.go`), and `edge/capture`
  carries `CaptureSessionConfig` on the assignment stream. Importing
  `model/access` would therefore pull the access plane into every capture
  schema down to the edge, all for one field. "Principal" is the authorization
  term for whoever a check is made about, the `user:` side of an OpenFGA tuple,
  and `Actor`'s oneof already uses the word. The user chose this over widening
  `model/capture`'s layering row on 2026-09-28.
- **The leaf is not the shared refs package `docs/conventions/protobuf.md`
  forbids.** Why: that rule ("There is no shared refs package") exists because
  a package holding every ref would have to know every entity above it.
  `OperatorRef` keys an identity-provider subject, not a FlowSeer entity. It
  has no ref pair and no triad, and nothing it names lives in FlowSeer, so the
  leaf imports nothing and knows nothing above it. The leaf's README says so,
  and the non-entity list in `protobuf.md` gains it beside the policy handles.
- **Only `OperatorRef` moves. `Actor`, `SystemActor`, and `SystemReason` stay
  in `model/access`.** Why: `SystemReason` (reconciliation) only means
  something for a mutation. `Actor` is "who asked for a mutation", not a
  general principal.
- **`OperatorRef` keeps its name, its one field, and its rule, so
  `Actor.operator` is unchanged on the wire.** Why: field 1, same message
  shape. `actorPart` in `src/services/device/internal/journal/journal.go`
  reads `GetOperator().GetSubject()` through the getter, so the intent digest
  projection does not change and `journal.go` needs no edit. The fully
  qualified name does change, which breaks anything that resolves the type by
  name. Nothing outside this repository does (`AGENTS.md`, Agent behavior).
- **`CaptureAuthorization` reserves field 1 and the name `operator`, and takes
  `flowseer.model.principal.v1.OperatorRef requested_by = 4`.** Why:
  `docs/code-style-proto.md` (Evolution) says never to reuse a field, and to
  reserve the number and the name on removal. That rule holds inside the
  pre-release window as well. Changing field 1 from `string` to a message in
  place would be a reuse. It would also make every stored record undecodable:
  `Store.Session` (`src/services/device/internal/captureapi/store.go`) decodes
  with `proto.Unmarshal`, and `ListSessions` returns on the first error, so
  one old record would break `ListCaptureSessions` outright. With a new
  number, an old record decodes and carries no requester. `requested_by`
  matches the message's own comment ("Who asked for a capture").
- **The break, stated as fact:** a session record written before the change
  decodes with `requested_by` unset, and nothing backfills it. An edge built
  before the change ignores field 4, and a central built before it ignores
  field 4 from a newer edge. A dev deployment that wants every record to carry
  a requester empties the `captures` bucket. No shim is added.
- **The handler's hand-written authorization guard checks the subject.** Why:
  `operator_service.go` checks `GetOperator() == ""` today, which stops
  compiling. The guard becomes `GetRequestedBy().GetSubject() == ""`. In the
  host, `ValidatingInterceptor`
  (`src/services/device/internal/host/validation.go`, mounted on
  `CaptureService` in `serve.go`) rejects a request that breaks the schema
  rules. The handler's own tests run without the interceptor, so they see only
  the guard, and a conformance rules test pins the schema rule separately.
- Ruled: the records' import rows leave out `net/key` for `model/capture`.
  Why: `net/key` is an `import option` there, and the rows leave option
  imports out; the `model/access` row omits its option import of `net/key`
  the same way. Cost if wrong: one token in two record lines.
- Ruled: the buf managed-mode drift (308 generated files at the parent
  commit, `java_multiple_files` in every raw descriptor) lands as its own
  `chore(generated)` commit before this unit. Why: the verifier regenerates
  and diffs against what is committed, so the unit cannot pass without it,
  and a separate commit keeps this unit's generated diff down to its own
  schema. Cost if wrong: one revert of a no-schema-change commit.
- **Relations, as proposed input for the OpenFGA record:**

  | RPC | Relation | Object |
  | --- | --- | --- |
  | `CreateCaptureSession` | `edge#capture` | the request's `edge` |
  | `CreateCaptureSession` with `full_payload_requested` | `edge#capture` and `tenant#full_payload` | the edge, and the caller's tenant |
  | `GetCaptureSession`, `StopCaptureSession`, `DeleteCaptureSession` | `edge#capture` | the session's owning edge |
  | `ListCaptureSessions` | `edge#capture`, as a filter | each listed session's owning edge |
  | `TailCaptureSession`, `DownloadCaptureSession` | `session#download` | the session |

  Why: starting a capture is an action on an edge, and the session's owning
  parent is that edge (`model/capture/v1/README.md`, "The owning edge"). Full
  payload is the most restricted data the system holds (capture direction
  record, "Every capture is bounded and authorized"). That is why it gets a
  grant of its own at tenant scope, separate from the per-edge one. Tenancy is
  ambient (`docs/conventions/protobuf.md`) and `TenantRef` carries no key, so
  the tenant object's id comes from the authenticated request context, not
  from the request. `ListCaptureSessionsRequest` carries only `page_size` and
  `page_token`, so List is not one check. It filters results to sessions on
  edges that grant `edge#capture`, which the OpenFGA record resolves either
  with a list-objects query or with a filter after the read. Either way a page
  holds only visible sessions, and the page token must not skip or repeat one
  across the filter. Tail and download hand out the captured bytes, which is
  what an audit is about, so they share one relation. `session#download` is
  proposed as held by the session's `requested_by` principal and by anyone
  with `edge#capture` on its owning edge. That needs a parent-edge tuple and
  a requester tuple written when the session is created. The OpenFGA record
  may rename these relations or derive them differently. This plan only
  writes them down.

## Requirements

1. `OperatorRef` is declared in `flowseer.model.principal.v1`, and that
   package imports nothing. Acceptance: with the `importOrder` row
   `"model/principal": nil`, `TestImportOrder` passes over the real tree, and
   the `TestLayeringViolationRules` case "the capture entity imports access
   values" (`model/capture` → `model/access`) is refused.
2. `CaptureAuthorization.requested_by` is a required
   `flowseer.model.principal.v1.OperatorRef`, field 1 and `operator` are
   reserved. Acceptance: a `CreateCaptureSession` whose
   `authorization.requested_by.subject` is `"zitadel|usr_123"` creates a
   session. The same request with `requested_by` unset, or with `subject`
   `""`, fails with `InvalidArgument`, and the store holds as many sessions
   as before the call.
3. The schema rule holds without the handler. Acceptance: protovalidate
   rejects a `CaptureAuthorization` with no `requested_by`, and one whose
   `requested_by.subject` is `""`, and accepts one with subject
   `"zitadel|usr_123"`.
4. `Actor.operator` carries `flowseer.model.principal.v1.OperatorRef` and the
   intent digest projection is unchanged. Acceptance:
   `TestEveryProjectedIntentFieldChangesTheDigest` in
   `src/services/device/internal/journal/journal_test.go` still passes, with
   the subject `"zitadel|2"` still changing the digest, and only the test's
   import changed.
5. The capture direction record names the relation for every `CaptureService`
   RPC, and says that enforcement and the download event land with the OpenFGA
   record and caller identity. Acceptance: the amendment's table covers all
   seven RPCs in `capture_service.proto`.

## Out of scope

- Any `PermissionDenied` path, OpenFGA client, or caller authentication
  interceptor.
- The download audit event.
- Backfilling `requested_by` on stored session records.
- A service or workload principal in `model/principal`. The leaf has room
  for one, and none is added.
- Rewriting the historical plans that mention `model/access`'s `OperatorRef`.

## Units

### U1. The principal leaf, the capture field, and the records

The record edits share the unit with the schema, because the network model
structure record's import block states "the imports that exist today"
(`docs/architecture/2026-08-20-network-model-structure-direction.md`).

Files:

- Schema: `spec/proto/flowseer/model/principal/v1/operator.proto` (new),
  `spec/proto/flowseer/model/principal/v1/README.md` (new),
  `spec/proto/flowseer/model/access/v1/operation.proto`,
  `spec/proto/flowseer/model/access/v1/README.md`,
  `spec/proto/flowseer/model/capture/v1/capture_session.proto`,
  `spec/proto/flowseer/model/capture/v1/README.md`,
  `spec/proto/flowseer/model/README.md`
- Conformance: `test/conformance/proto/layering_test.go`,
  `test/conformance/proto/model_access_rules_test.go`,
  `test/conformance/proto/model_capture_rules_test.go` (new)
- Capture service and edge: `src/services/device/internal/captureapi/operator_service.go`,
  `src/services/device/internal/captureapi/operator_service_test.go`,
  `src/services/device/internal/captureapi/edge_service_test.go`,
  `src/services/device/internal/captureapi/store_test.go`,
  `src/services/device/internal/captureapi/artifact_invariant_internal_test.go`,
  `src/services/device/test/integration/capture_test.go`,
  `src/edge/agent/internal/capture/capture_test.go`
- `OperatorRef` users: `src/services/device/test/integration/e2e_test.go`,
  `src/services/device/test/integration/testdata/mutation-verification-repro/e2e_test.go.repro`,
  `src/services/device/internal/journal/journal_test.go`,
  `src/services/device/internal/host/validation_test.go`,
  `src/services/device/internal/deviceapi/deviceapi_test.go`,
  `src/services/device/internal/drift/drift_test.go`,
  `src/services/device/internal/dispatchapi/relay_test.go`
- Records and docs: `docs/architecture/2026-09-09-remote-packet-capture-direction.md`,
  `docs/architecture/2026-08-20-network-model-structure-direction.md`,
  `docs/conventions/protobuf.md`, `src/services/device/README.md`

After: none

Change:

- Schema. `model/principal/v1/operator.proto` declares `OperatorRef` with the
  comment, field, and validation rule it has in `operation.proto` today. Its
  file-level comment says the leaf names whoever an authorization check is
  about. `operation.proto` drops `OperatorRef`, imports the leaf, and types
  `Actor.operator` as `flowseer.model.principal.v1.OperatorRef`.
  `CaptureAuthorization` reserves `1` and `operator`, and adds
  `flowseer.model.principal.v1.OperatorRef requested_by = 4
  [(buf.validate.field).required = true]`. The field comment says the caller
  names the requester. That nothing verifies it yet is status, which
  `docs/code-style-proto.md` keeps out of the schema, so it goes in the
  capture README.
- READMEs. The principal README's Boundaries read `Imports: nothing
  FlowSeer-owned` and `Imported by: model/access, model/capture`. It says
  `OperatorRef` keys an identity-provider subject and not a FlowSeer entity,
  so the leaf is not a shared refs package. The access and capture READMEs
  add `model/principal` to `Imports`. The `model/` README lists
  `principal/v1/` under Packages, and its Identity paragraph names the
  principal leaf beside the policy and credential leaves.
- Generated code. `buf generate` refreshes it, never an edit by hand.
- Layering. `layering_test.go` gains `"model/principal": nil` and adds
  `model/principal` to the `model/access` and `model/capture` rows, each with
  its comment updated. It gains three cases: "access values import the
  principal" and "the capture entity imports the principal", both allowed,
  and "the capture entity imports access values", refused.
- Go sites. Each `CaptureAuthorization_builder` that sets
  `Operator: proto.String("alice")` sets
  `RequestedBy: principalv1.OperatorRef_builder{Subject: proto.String("zitadel|usr_123")}.Build()`.
  Each `accessv1.OperatorRef` becomes `principalv1.OperatorRef`. The
  `.repro` file gets the same import change, so it still compiles when copied
  back. The guard in `operator_service.go` reads
  `GetRequestedBy().GetSubject()`.
- Capture direction record. It gains an amendment, "2026-09-28 — operator
  identity and the relations capture needs". The amendment says
  `CaptureAuthorization.requested_by` is a `principal.v1.OperatorRef` the
  caller asserts, and that field 1 was reserved. It carries the relation
  table and its reasons from this plan's Decisions as input for the OpenFGA
  record, with the List filter and the `session#download` derivation. It
  says the rule "an artifact is served only to a caller authorized for that
  session, and a download is itself an event" lands with caller identity, and
  why the download event cannot land before it. It restates the capture
  import line as
  `{model/edge, model/principal, net/capture} ← model/capture`.
- Network model structure record. Its package tree lists `principal/v1/`
  under `model/`. Its import-order block gains
  `model/principal ← {model/access, model/capture}`. Its `model/capture` row
  becomes `{model/edge, model/principal, net/capture} ← model/capture`,
  and its `model/access` row names
  `model/principal`. Its leaf paragraph lists `model/principal` with
  `model/edge`, `model/credential`, and `model/policy`.
- `protobuf.md`. The non-entity list gains `OperatorRef` in
  `model/principal/v1`: an identity-provider subject with no ref pair, no
  triad, and no place in the enum.
- Device README. The paragraph on the unauthenticated operator API says
  `authorization.requested_by` is an `OperatorRef` the caller writes about
  itself, still unverified. It points at the capture record's relation table.

Tests:

- `model_capture_rules_test.go`, in the table style of
  `model_access_rules_test.go`, covers requirement 3: a missing
  `requested_by` and an empty subject are rejected, and subject
  `"zitadel|usr_123"` is accepted.
- `operator_service_test.go`'s argument-validation test gains the cases
  "authorization with no requester" and "requester with an empty subject".
  Each expects `InvalidArgument` and compares `len(h.store.ListSessions(ctx))`
  before and after the call, because the test already creates a session
  before its end. The existing valid-request cases, now with subject
  `"zitadel|usr_123"`, cover the accepted path of requirement 2.
- The layering cases above cover the import boundary.
- The journal, deviceapi, drift, dispatchapi, host, and e2e tests passing with
  only their import changed is the regression check for the move. No test
  pins a fixed digest value, and this unit adds none. `actorPart` reads only
  the subject string and `journal.go` is not edited, so the move cannot change
  what it projects.
- Nothing runs a record written before the change against the new schema.
  The Decisions state the outcome as a fact, and no test pins it.
- Nothing checks the records' import blocks against `importOrder`. Compare
  them by hand before committing.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/model test/conformance/proto src/services/device src/edge/agent/internal/capture docs/architecture docs/conventions/protobuf.md`

Waves: U1

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/model test/conformance/proto src/services/device src/edge/agent/internal/capture docs/architecture docs/conventions/protobuf.md
go test -race ./src/services/device/... ./src/edge/agent/... ./test/conformance/...
grep -rn 'accessv1.OperatorRef\|model.access.v1.OperatorRef\|GetOperator() == ""' src test spec
```

The grep prints nothing.

## Definition of done

- [x] Verifier green for every changed path.
- [x] `OperatorRef` declared only in `model/principal/v1`.
- [x] `CaptureAuthorization` reserves `1` and `operator`, and carries
      `requested_by`.
- [x] Package READMEs under `spec/proto/flowseer/model/`, both direction
      records, `docs/conventions/protobuf.md`, and the device README updated
      in the same change.
- [x] This plan's `status` set, with an outcome note under its title.
- [x] No plan labels in code.

## Open questions

- Should `GetCaptureSession`, `StopCaptureSession`, and `DeleteCaptureSession`
  have a relation of their own on the session, such as a `session#viewer`
  derived from `edge#capture`, rather than checking the edge directly? And
  should `ListCaptureSessionsRequest` take an edge, so List becomes one
  check? The amendment proposes the edge check and the filter, and leaves
  both choices to the OpenFGA record.
