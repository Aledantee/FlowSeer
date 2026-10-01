---
title: Operator Authorization Phase 1, Rule Schema and Enforcement Core - Plan
type: feat
date: 2026-09-30
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
amends: docs/architecture/2026-08-20-network-model-structure-direction.md
parent: docs/plans/2026-09-30-1139-feat-operator-authorization-plan.md
---

# Operator Authorization Phase 1, Rule Schema and Enforcement Core - Plan

## Goal

Every RPC under `flowseer.api.` declares how it is authorized in a
`flowseer.authz.v1.rule` method option, a conformance gate fails the build
when one does not, and `src/services/device/internal/authz` holds the
interceptor that enforces the rules against a `Checker` interface. Nothing
is wired into the running service yet: phase 2 supplies the real
authenticator and OpenFGA client, and phase 3 turns enforcement on.

**Stop condition:** protobuf-go cannot read a custom `MethodOptions`
extension from the descriptor Connect hands the interceptor
(`connect.Spec.Schema`). The rule would then need a registry built at
startup, and the interceptor design below is wrong.

## Decisions

The parent plan's Decisions and its relation table apply. Local to this
phase:

- The option lives in a new leaf root `spec/proto/flowseer/authz/v1`,
  package `flowseer.authz.v1`, importing nothing FlowSeer-owned. Why:
  `errs/` is the precedent for a cross-cutting leaf
  (`spec/proto/flowseer/errs/README.md`), `model/` admits only domain
  identity and operation vocabulary (`spec/proto/flowseer/model/README.md`),
  and the names `access` and `policy` are taken by device-access packages.
- The extension is field 50000 on `google.protobuf.MethodOptions`. Why: no
  `MethodOptions` extension exists in the tree, and
  `docs/code-style-proto.md` keeps in-house extensions from 50000 with one
  table row per number.
- `Rule` carries `mode`, `relation`, `object_type`, and `object_id_path`.
  `object_id_path` is a dot-separated path of field names from the request
  to a string field, for example `session.edge.edge.id`. Why: every object
  id sits inside a `GlobalRef` (`EdgeGlobalRef.edge.id`,
  `spec/proto/flowseer/model/edge/v1/edge.proto`), and a path names it
  without a new field on any request.
- `RuleMode` has five values: `RULE_MODE_UNSPECIFIED`, `RULE_MODE_REQUEST`,
  `RULE_MODE_TENANT`, `RULE_MODE_LOADED`, `RULE_MODE_FILTERED`. Why:
  `CreateEdge` names no edge, so its object is the admitted tenant, and
  `docs/code-style-proto.md` requires a prefixed zero value.
- Protovalidate does not run on option values, so the gate calls
  `protovalidate.Validate` on every extracted `Rule`. Why: otherwise the
  schema's rules on `Rule` are never enforced.
- `authn` in this phase holds only the principal and its context carrier.
  `Principal.ID` is opaque here, and phase 2 decides how issuer and subject
  encode into it. Why: the interceptor needs a principal to test against,
  and the encoding depends on OpenFGA's id limits, which phase 2 verifies.
- The interceptor sends one contextual `claimed` tuple per tenant the
  principal's token vouches for on every check, not only the membership
  gate. Why: `tenant#capturer` reaches `tenant#active_admin`, which
  intersects with `member`, so a resource check through a partner needs the
  claims too.
- Every object check also asks `<object>#tenant@tenant:<admitted>`, in the
  same `BatchCheck`, and denies unless both hold. Why: resource permissions
  carry no membership term, so without it a member of tenant B holding a
  grant in tenant A could act on A's object while naming B.
- Handler-side checks go through `authz.Require` and `authz.Filter`, which
  work in every rule mode and discharge a deferred rule's obligation even
  when they deny. A denied `Require` followed by a successful handler
  response becomes `Internal`. Why: the obligation proves a check ran, and
  the denial rule catches a handler that ignores a `Require` answer.
  Nothing in the interceptor can see whether a handler used `Filter`'s
  answer. Phase 3 covers that with a denied-edge case in every list
  handler's tests.
- Errors reach the caller through `connecterr.WrapAs`
  (`src/services/device/internal/connecterr/connecterr.go`) with fixed
  messages: "authentication required", "permission denied", "no tenant
  named", "authorization is unavailable". Why: the wire carries only the
  public code and message, and the cause stays in the chain the telemetry
  interceptor logs.

Rules each RPC carries (the parent's relation table names the relations):

| RPC | Mode | Object type | Relation | `object_id_path` |
| --- | --- | --- | --- | --- |
| `DeviceService.ReadInterface` | request | `device` | `view` | `device.device.id` |
| `DeviceService.ApplyInterfaceDescription` | request | `device` | `operate` | `intent.device.device.id` |
| `DeviceService.GetDeviceAccessStatus` | request | `device` | `view` | `device.device.id` |
| `DeviceService.AbandonMutation` | request | `device` | `operate` | `device.device.id` |
| `DeviceService.ResolveDesynchronization` | request | `device` | `operate` | `device.device.id` |
| `DeviceService.ListEdgeOpenMutations` | request | `edge` | `view` | `edge_id` |
| `EdgeAdminService.CreateEdge` | tenant | `tenant` | `admin` | |
| `EdgeAdminService.IssueSetupKey` | request | `edge` | `administer` | `edge.edge.id` |
| `EdgeAdminService.RevokeSetupKey` | request | `edge` | `administer` | `edge.edge.id` |
| `EdgeAdminService.RetireEdge` | request | `edge` | `administer` | `edge.edge.id` |
| `EdgeAdminService.GetEdge` | request | `edge` | `view` | `edge.edge.id` |
| `EdgeAdminService.ListEdges` | filtered | `edge` | `view` | |
| `CaptureService.CreateCaptureSession` | request | `edge` | `capture` | `edge.edge.id` |
| `CaptureService.StopCaptureSession` | request | `capture_session` | `manage` | `session.capture_session.id` |
| `CaptureService.GetCaptureSession` | request | `capture_session` | `manage` | `session.capture_session.id` |
| `CaptureService.ListCaptureSessions` | filtered | `edge` | `capture` | |
| `CaptureService.DeleteCaptureSession` | request | `capture_session` | `manage` | `session.capture_session.id` |
| `CaptureService.TailCaptureSession` | request | `capture_session` | `download` | `session.capture_session.id` |
| `CaptureService.DownloadCaptureSession` | request | `capture_session` | `download` | `session.capture_session.id` |

Session RPCs check the session, not the edge its ref names. The capture
store reads a session by tenant and session id through
`Store.Session(ctx, tenantID, sessionID)`
(`src/services/device/internal/captureapi/store.go:160`). Within a tenant,
the request edge ref and stored session edge can differ, so RPCs check the
session rather than the request edge. A check on the request's edge would
authorize one edge and read another's session.
`capture_session#manage` derives from the session's stored edge instead.

`CreateCaptureSession` with `full_payload_requested` also needs
`tenant#full_payload`. The rule declares the edge check, and the handler
adds the tenant check through `authz.Require` in phase 3.

## Requirements

1. Every method of every service in a `flowseer.api.` package carries a
   valid rule. `TestEveryOperatorRPCHasAuthorizationRule` passes on the tree
   and reports a synthetic `flowseer.api.test.v1.TestService.Ping` built
   without the option.
2. A request rule's `object_id_path` resolves through singular message
   fields to a string field. A synthetic rule with path `edge.edge.name` on
   `GetEdgeRequest` is reported, and so is `edge.edge` (a message leaf).
3. A rule's (object type, relation) pair is one the parent's table lists.
   A synthetic rule naming `edge` with `delete` is reported.
4. A call with no principal in the context fails with `Unauthenticated`
   before any check.
5. A call without an `X-FlowSeer-Tenant` header, or with one outside
   `^[A-Za-z0-9_-]{1,64}$`, fails with `InvalidArgument` and makes no check.
6. A principal the checker does not find in `tenant:<header>#member` fails
   with `PermissionDenied`, and the handler does not run.
7. A request rule checks `<object_type>:<id at the path>` with its
   relation, and `<object_type>:<id>#tenant@tenant:<header>`, before the
   handler runs. `GetCaptureSession` for session S checks
   `capture_session:S#manage` and `capture_session:S#tenant`, and denies
   when either is false.
8. A tenant rule checks `tenant:<header>` with its relation.
9. A loaded or filtered rule runs the handler. If the handler returns
   without calling `Require` or `Filter`, the caller gets `Internal` and no
   response. If a `Require` in the call denied and the handler still returns
   a response, the caller gets `Internal` and no response. Otherwise the
   handler's response or error passes through unchanged.
10. A checker error becomes `Unavailable` with "authorization is
    unavailable", and the handler does not run.
11. Every check carries one `claimed` contextual tuple per tenant the
    principal lists, plus `platform:flowseer#claimed` when the principal
    carries the platform claim.
12. Every streaming call is refused with `PermissionDenied` until phase 3
    designs streaming rules.
13. A method with no rule, or a rule with `RULE_MODE_UNSPECIFIED`, is
    refused with `PermissionDenied`.

## Out of scope

- Token verification, the OpenFGA client, and configuration: phase 2.
- Wiring the interceptor into `internal/host/serve.go`: phase 3. The running
  service behaves exactly as before this phase.
- The gate reads schema the repository authors, who are trusted. It does
  not defend against a hostile descriptor.

## Units

### U1. The rule schema

Files: `spec/proto/flowseer/authz/README.md`, `spec/proto/flowseer/authz/v1/rule.proto`, `spec/proto/flowseer/authz/v1/README.md`, `spec/proto/flowseer/README.md`, `test/conformance/proto/layering_test.go`, `test/conformance/proto/field_constraint_class_test.go`, `docs/code-style-proto.md`, `docs/architecture/2026-08-20-network-model-structure-direction.md`, `generated/go/proto/flowseer/authz/v1/rule.pb.go`
After: none
Change: `rule.proto` (edition 2024) declares the top-level `RuleMode` enum,
the `Rule` message, and the extension
`extend google.protobuf.MethodOptions { Rule rule = 50000; }`. `mode` is
required with `enum = {defined_only: true, not_in: [0]}`. `relation` and
`object_type` are required and match
`^[a-z][a-z_]*$`. A message-level CEL rule with a stable `id` requires
`object_id_path` exactly when `mode` is `RULE_MODE_REQUEST`, and a second
requires `object_type == "tenant"` exactly when `mode` is
`RULE_MODE_TENANT`. Comments state the contract only
(`docs/code-style-proto.md`). The root and package READMEs follow the
`errs/` shape, with `Imports: nothing FlowSeer-owned`. `layering_test.go` gains
`"authz": nil` in `importOrder`, `"flowseer/authz"` in `orderedRoots`, and
`"authz"` in the allowed imports of `api/edge`, `api/capture`, and
`api/device`. `field_constraint_class_test.go` gains a blank import of the
generated `authzv1` package so the package counts as linked. The READMEs
say `Imported by: nothing` until U2 adds the importers. `docs/code-style-proto.md` adds a `MethodOptions` 50000 row
to its extension table. The network model structure record gains a dated
amendment adding the `authz` root to its tree and import rows.
`generated/` comes from `buf generate`, never a hand edit.
Tests: `buf lint`, and the existing `TestImportOrderCoversEveryPackage`,
`TestOrderedRootsCoverEveryTopLevelTree`, and `TestProtoReadmeCoverage`
pass with the new root.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/authz test/conformance/proto/layering_test.go docs/code-style-proto.md docs/architecture/2026-08-20-network-model-structure-direction.md spec/proto/flowseer/README.md`

### U2. Annotate the operator RPCs

Files: `spec/proto/flowseer/api/device/v1/device_service.proto`, `spec/proto/flowseer/api/edge/v1/edge_admin_service.proto`, `spec/proto/flowseer/api/capture/v1/capture_service.proto`, `spec/proto/flowseer/api/README.md`, `spec/proto/flowseer/authz/README.md`, `spec/proto/flowseer/authz/v1/README.md`, `spec/proto/flowseer/api/device/v1/README.md`, `spec/proto/flowseer/api/edge/v1/README.md`, `spec/proto/flowseer/api/capture/v1/README.md`, `generated/go/proto/flowseer/api/device/v1/device_service.pb.go`, `generated/go/proto/flowseer/api/edge/v1/edge_admin_service.pb.go`, `generated/go/proto/flowseer/api/capture/v1/capture_service.pb.go`
After: U1
Change: each service file adds `import option "flowseer/authz/v1/rule.proto";`
and every RPC carries `option (flowseer.authz.v1.rule) = {...}` exactly as
the Decisions table lists. `api/README.md` adds `authz` to the imports its
packages may take and says every RPC declares a rule. Each package README's
`Imports:` line gains `authz`, and both authz READMEs say `Imported by:
api/capture, api/device, api/edge`. Generated code comes from `buf generate`.
Tests: `buf lint`, and `TestProtoReadmeCoverage` passes with the new imports.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/api`

### U3. The conformance gate

Files: `test/conformance/proto/authz_rules_test.go`
After: U1, U2
Change: the gate walks `protoregistry.GlobalFiles` for packages starting
with `flowseer.api.`, and for each method reads the `flowseer.authz.v1.rule`
extension. It reports a method without one, a rule that fails
`protovalidate.Validate`, a request rule whose `object_id_path` does not
resolve through singular message fields to a string field of the method's
input, and an (object type, relation) pair outside a table declared in the
test that mirrors the parent's relation table, and a rule whose mode is
`RULE_MODE_UNSPECIFIED`. Each check also runs over
synthetic descriptors built with `protodesc.NewFile`, following the
negative-descriptor pattern in `schema_language_test.go`, so the walk proves
it read something.
Tests: `TestEveryOperatorRPCHasAuthorizationRule`,
`TestAuthorizationRuleObjectPathResolves`,
`TestAuthorizationRuleNamesKnownRelation`, each with its synthetic negative
(requirements 1 to 3).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- test/conformance/proto/authz_rules_test.go`

### U4. Principal carrier and the authorization interceptor

Files: `src/services/device/internal/authn/principal.go`, `src/services/device/internal/authn/principal_test.go`, `src/services/device/internal/authz/authz.go`, `src/services/device/internal/authz/interceptor.go`, `src/services/device/internal/authz/obligation.go`, `src/services/device/internal/authz/authz_test.go`, `src/services/device/README.md`
After: U1, U2
Change: `authn.Principal` holds `ID`, `Tenants` (tenant ids the token vouches
for), and `Platform` (the token carries the platform claim), with
`authn.NewContext` and `authn.FromContext`. `authz` declares `Tuple`,
`Query` (object, relation, user, contextual tuples), and a `Checker` with
`Check(ctx, Query) (bool, error)` and `BatchCheck(ctx, []Query) ([]bool,
error)`. `authz.Interceptor` implements `connect.Interceptor`: it reads the
rule from `req.Spec().Schema.(protoreflect.MethodDescriptor)`, refuses a
missing or unspecified rule (requirement 13) before anything else, then
applies requirements 4, 5, and 6, then the rule's mode (7, 8, or 9), with
requirement 10 on any checker error, and puts the admitted tenant in the
context (`authz.TenantFromContext`). `authz.Require(ctx, relation,
objectType, id)` returns nil or a `PermissionDenied` error.
`authz.Filter(ctx, relation, objectType, ids)` deduplicates ids, calls
`BatchCheck` in chunks of at most 50, and returns the allowed set. Both
discharge the obligation. The streaming handler wrapper refuses every call,
and the streaming client wrapper passes through. The device README's
Layout table gains the two packages.
Tests: `authz_test.go` serves the generated `CaptureService` and
`EdgeAdminService` handlers through `httptest` with a fake `Checker` that
records every `Query`: one case per requirement 4 to 13, including a
`ListCaptureSessions` handler that skips `Filter` and one whose `Require` denied
but that returns a response (requirement 9), a `GetCaptureSession` request
asserting the recorded `capture_session:<S>#manage` and
`capture_session:<S>#tenant` queries (requirement 7), a
`CreateCaptureSession` handler calling `Require` for `tenant#full_payload`
under a request rule, and the recorded contextual tuples for
a principal with two tenants and the platform claim (requirement 11).
`principal_test.go` covers the context round trip and an empty context.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/authn src/services/device/internal/authz src/services/device/README.md`

Waves: U1 | U2 | U3 U4

## Verification

```bash
buf lint
buf generate
go test ./test/conformance/proto/ ./src/services/device/internal/authn/ ./src/services/device/internal/authz/
.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer test/conformance/proto src/services/device docs
```

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] READMEs under `spec/proto/flowseer/authz/` and `api/` and the device
      README updated in the same change.
- [ ] The network model structure record carries the dated amendment.
- [ ] This plan's `status` set with an outcome note under its title, and
      the parent's `Landed:` line for U1 filled.
- [ ] No plan labels in code.

## Open questions

- Re-plans against a tree holding `model/identity`, `src/common/tenant`, and
  tenant-partitioned stores.
- Whether a nested value name inside an option literal trips buf's export
  rule for edition 2024. `buf lint` on U1's draft settles it. A top-level
  `RuleMode` enum avoids the question.
