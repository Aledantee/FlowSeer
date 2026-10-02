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

- The stop condition does not hold at the versions `go.mod` pins. A
  generated handler hands Connect its method descriptor (`connect.WithSchema`,
  `generated/go/proto/flowseer/api/capture/v1/capturev1connect/capture_service.connect.go:201-242`),
  Connect copies it into `Spec.Schema` (`connectrpc.com/connect@v1.21.0`,
  `handlerConfig.newSpec` in `handler.go`), and protobuf-go decodes option
  bytes on the first `Options()` call against `protoregistry.GlobalTypes`
  (`google.golang.org/protobuf@v1.36.12`, `optionsUnmarshaler` in
  `internal/filedesc/desc_lazy.go`). The extension resolves only where the
  generated `authzv1` package is linked, which the interceptor and the gate
  do by naming `authzv1.E_Rule`, since `import option` produces no Go
  import. An unlinked extension reads as a missing rule, so the call is
  refused. Why: the interceptor reads the rule from the descriptor and
  needs no registry.
- The option lives in a new leaf root `spec/proto/flowseer/authz/v1`,
  package `flowseer.authz.v1`, importing nothing FlowSeer-owned. Why:
  `errs/` is the precedent for a cross-cutting leaf
  (`spec/proto/flowseer/errs/README.md`), `model/` admits only domain
  identity and operation vocabulary (`spec/proto/flowseer/model/README.md`),
  and the names `access` and `policy` are taken by device-access packages.
- The extension is field 50000 on `google.protobuf.MethodOptions`. Why: no
  `MethodOptions` extension exists in the tree, convention 4 of the network
  model structure record takes extension numbers from 50000 to 99999, and
  `docs/code-style-proto.md` keeps one table row per number.
- `Rule` carries `mode`, `relation`, `object_type`, and `object_id_path`, a
  dot-separated path of field names from the request to a string field,
  for example `session.capture_session.id`. Why: every object id sits
  inside a `GlobalRef` (`spec/proto/flowseer/model/edge/v1/edge.proto`),
  and a path names it without a new field on any request.
- `RuleMode` is a top-level enum with six values: `RULE_MODE_UNSPECIFIED`,
  `RULE_MODE_REQUEST`, `RULE_MODE_TENANT`, `RULE_MODE_LOADED`,
  `RULE_MODE_FILTERED`, `RULE_MODE_PLATFORM`. Why: `CreateEdge` names no
  edge, so its object is the admitted tenant, and `docs/code-style-proto.md`
  requires a prefixed zero value and top-level placement for a shared type.
- `TenantService` (`spec/proto/flowseer/api/identity/v1/tenant_service.proto`)
  sits under `flowseer.api.`, so its three RPCs carry a rule. Each is a
  platform rule: the interceptor checks `platform:flowseer#admin`, reads no
  tenant header, and puts no tenant in the context. Why: the file states
  "Authorized as the platform admin", the parent makes global admins
  `platform#admin`, and `CreateTenant` runs before any tenant exists to
  name. U1 adds the mode to the rule table of the
  [operator authorization record](../architecture/2026-09-30-operator-authorization-direction.md#every-rpc-declares-its-rule).
  Unconfirmed: see Open questions.
- `authn` in this phase holds only the principal and its context carrier.
  `Principal.ID` is opaque, and queries name the caller `user:<Principal.ID>`.
  Why: the interceptor needs a principal to test against, and the id's
  encoding depends on OpenFGA's id limits, which phase 2 verifies.
- A tenant id is what `tenant.Validate` accepts, and the admitted tenant
  rides the context through `tenant.WithTenant` (`src/common/tenant/tenant.go`).
  Why: every operator handler already reads `tenant.FromContext`, and store
  keys hold only ids that function accepts.
- The interceptor sends one contextual `claimed` tuple per tenant the
  principal's token vouches for on every check, not only the membership
  gate. Why: `tenant#capturer` reaches `tenant#active_admin`, which
  intersects with `member`, so a resource check through a partner needs the
  claims too.
- The parent's tenant check on an object rides the same `BatchCheck` as its
  relation check: `<object>#tenant@tenant:<admitted>`. A check on a `tenant`
  object sends no second query and denies any id but the admitted tenant's
  without asking. Why: one round trip, and a tenant object has no `tenant`
  relation.
- Handler-side checks go through `authz.Require` and `authz.Filter`, which
  work under every rule that admits a tenant and discharge a deferred
  rule's obligation even when they deny. A platform rule admits no tenant,
  so its handlers do not call them. Why: the obligation proves a check ran, and requirement
  9's denial rule catches a handler that ignores a `Require` answer.
  Nothing in the interceptor can see whether a handler used `Filter`'s
  answer, so phase 3 puts a denied-edge case in every list handler's tests.
- Errors reach the caller through `connecterr.WrapAs`
  (`src/services/device/internal/connecterr/connecterr.go`) with fixed
  messages: "authentication required", "permission denied", "no tenant
  named", "authorization is unavailable". Each refusal has its own `errs`
  code under `authz/`, except that every `PermissionDenied` from a check
  the checker answered false shares `authz/denied`. Why: the wire carries
  the Connect code, the message, and the `errs` code, so one code keeps a
  caller from learning that an object exists in another tenant.
- The plan stays whole. Why: its units form one cluster, each importing the
  schema U1 adds.

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
| `TenantService.CreateTenant` | platform | `platform` | `admin` | |
| `TenantService.GetTenant` | platform | `platform` | `admin` | |
| `TenantService.ListTenants` | platform | `platform` | `admin` | |

`CreateCaptureSession` with `full_payload_requested` also needs
`tenant#full_payload`. The rule declares the edge check, and the handler
adds the tenant check through `authz.Require` in phase 3.

## Requirements

1. Every method of every service in a `flowseer.api.` package carries a
   valid rule. `TestEveryOperatorRPCHasAuthorizationRule` passes on the tree
   and reports a synthetic `flowseer.api.test.v1.TestService.Ping` built
   without the option.
2. A request rule's `object_id_path` resolves through singular message
   fields to a string field. Synthetic rules on `GetEdgeRequest` with paths
   `edge.edge.name` (no such field) and `edge.edge` (a message leaf) are
   reported, and so is `sequence` on `AbandonMutationRequest` (a `uint64`).
3. A rule's (object type, relation) pair is one the parent's table lists.
   A synthetic rule naming `edge` with `delete` is reported.
4. A call with no principal in the context fails with `Unauthenticated`
   before any check.
5. Outside a platform rule, a call without an `X-FlowSeer-Tenant` header,
   or with a value `tenant.Validate` rejects, fails with `InvalidArgument`
   and makes no check. `X-FlowSeer-Tenant: Acme` is refused, and
   `0192e6a0-0000-7000-8000-0000000000a1` reaches the membership check.
6. A principal the checker does not find in `tenant:<header>#member` fails
   with `PermissionDenied`, and the handler does not run.
7. A request rule checks `<object_type>:<id at the path>` with its relation
   and `<object_type>:<id>#tenant@tenant:<header>` before the handler runs.
   `GetCaptureSession` for session S checks `capture_session:S#manage` and
   `capture_session:S#tenant`, and denies when either is false.
8. A tenant rule checks `tenant:<header>` with its relation.
9. A loaded or filtered rule runs the handler, and a handler that returns
   without calling `Require` or `Filter` gets the caller `Internal` and no
   response. In every mode, so does a handler that returns a response after
   a `Require` in the call denied. Otherwise the handler's response or
   error passes through unchanged.
10. A checker error becomes `Unavailable` with "authorization is
    unavailable", and the handler does not run.
11. Every check carries one contextual tuple `tenant:<T>#claimed@user:<id>`
    per tenant the principal lists, plus `platform:flowseer#claimed@user:<id>`
    when the principal carries the platform claim.
12. Every streaming call is refused with `PermissionDenied` until phase 3
    designs streaming rules.
13. A method with no rule, a rule with `RULE_MODE_UNSPECIFIED`, or a mode
    the interceptor does not implement is refused with `PermissionDenied`.
14. A platform rule checks `platform:flowseer` with its relation, reads no
    tenant header, and puts no tenant in the context. `ListTenants` without
    the header records the one query `platform:flowseer#admin@user:<id>`,
    and a false answer is `PermissionDenied` with the handler not run.
15. A request rule whose path yields no id is refused with
    `PermissionDenied` and makes no object check. `GetEdgeRequest` with no
    `edge` set is refused.

## Out of scope

- Token verification, the OpenFGA client, and configuration: phase 2.
- Wiring the interceptor into `internal/host/serve.go`: phase 3. The running
  service behaves exactly as before this phase.
- Serving `TenantService`. No host mounts it (`TestTenantServiceIsNotMounted`
  in `src/services/device/internal/host/host_test.go`). This phase gives
  its RPCs a rule and the interceptor the mode.
- The gate reads schema the repository authors, who are trusted. It does
  not defend against a hostile descriptor.

## Units

### U1. The rule schema

Files: `spec/proto/flowseer/authz/README.md`, `spec/proto/flowseer/authz/v1/rule.proto`, `spec/proto/flowseer/authz/v1/README.md`, `spec/proto/flowseer/README.md`, `test/conformance/proto/layering_test.go`, `test/conformance/proto/authz_rules_test.go`, `docs/code-style-proto.md`, `docs/architecture/2026-08-20-network-model-structure-direction.md`, `docs/architecture/2026-09-30-operator-authorization-direction.md`, `generated/go/proto/flowseer/authz/v1/rule.pb.go`
After: none
Change: `rule.proto` (edition 2024) declares the top-level `RuleMode` enum,
the `Rule` message, and the extension
`extend google.protobuf.MethodOptions { Rule rule = 50000; }`. `mode` is
required with `enum = {defined_only: true, not_in: [0]}`. `relation` and
`object_type` are required and match `^[a-z][a-z_]*$`. `object_id_path`
matches `^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`. Three message-level CEL
rules, each with a stable `id`, hold `object_id_path` set exactly when
`mode` is `RULE_MODE_REQUEST`, `object_type == "tenant"` exactly when it
is `RULE_MODE_TENANT`, and `object_type == "platform"` exactly when it is
`RULE_MODE_PLATFORM`. The root and package READMEs follow the `errs/`
shape, with `Imports: nothing FlowSeer-owned` and `Imported by: nothing`
until U2 adds the importers. `layering_test.go` gains `"authz": nil` in
`importOrder`, `"flowseer/authz"` in `orderedRoots`, and `"authz"` in the
allowed imports of `api/edge`, `api/identity`, `api/capture`, and
`api/device`. `spec/proto/flowseer/README.md` adds the root to its tree and
names it a leaf. `docs/code-style-proto.md` widens its extension table from
rules messages to every extended message and adds the `MethodOptions` 50000
row. The network model structure record gains a dated amendment adding the
`authz` root to its tree as a leaf, and the operator authorization record
one adding the platform row to its rule-mode table.
Tests: `authz_rules_test.go` holds `TestRuleRules` over
`runValidationCases`, which also links the package for
`TestEveryDeclaredProtoPackageIsLinked`. Valid: one rule per mode. Invalid,
each one property away from a valid rule: a path outside request mode and
none inside it, the `tenant` and `platform` types outside their modes and
another type inside them, the unspecified mode, mode 99, relation `View`,
path `edges[0].id`, and an empty `Rule`. `TestLayeringViolationRules` gains
`api/edge` importing `authz` (allowed) and `authz` importing `model/edge`,
asserted on the reason
`importing model/edge is outside authz's declared layer ()` so it fails
when the row is missing. The root also has to pass
`TestOrderedRootsCoverEveryTopLevelTree` and `TestProtoReadmeImports`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/authz spec/proto/flowseer/README.md test/conformance/proto/layering_test.go test/conformance/proto/authz_rules_test.go docs/code-style-proto.md docs/architecture/2026-08-20-network-model-structure-direction.md docs/architecture/2026-09-30-operator-authorization-direction.md`

### U2. Annotate the operator RPCs

Files: `spec/proto/flowseer/api/device/v1/device_service.proto`, `spec/proto/flowseer/api/edge/v1/edge_admin_service.proto`, `spec/proto/flowseer/api/capture/v1/capture_service.proto`, `spec/proto/flowseer/api/identity/v1/tenant_service.proto`, `spec/proto/flowseer/api/README.md`, `spec/proto/flowseer/authz/README.md`, `spec/proto/flowseer/authz/v1/README.md`, `spec/proto/flowseer/api/device/v1/README.md`, `spec/proto/flowseer/api/edge/v1/README.md`, `spec/proto/flowseer/api/capture/v1/README.md`, `spec/proto/flowseer/api/identity/v1/README.md`, `docs/architecture/2026-08-20-network-model-structure-direction.md`, `generated/go/proto/flowseer/api/device/v1/device_service.pb.go`, `generated/go/proto/flowseer/api/edge/v1/edge_admin_service.pb.go`, `generated/go/proto/flowseer/api/capture/v1/capture_service.pb.go`, `generated/go/proto/flowseer/api/identity/v1/tenant_service.pb.go`
After: U1
Change: each service file adds `import option "flowseer/authz/v1/rule.proto";`
and every RPC carries `option (flowseer.authz.v1.rule) = {...}` exactly as
the Decisions table lists. `api/README.md` adds `authz` to its `Imports:`
line and to the roots its packages may import, and says every RPC declares
a rule. Each package README's `Imports:` line gains `authz`, and both authz
READMEs say `Imported by: api/capture, api/device, api/edge, api/identity`.
The network model structure record's import rows gain
`authz ← {api/capture, api/device, api/edge, api/identity}`. The Connect
files hold no descriptor bytes and do not change.
Tests: `go tool -modfile=tools/buf/go.mod buf lint`. `TestImportOrder` and
`TestProtoReadmeImports` pass with the new imports, which both read
`import option` lines (`importLine` in `layering_test.go`).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/api spec/proto/flowseer/authz docs/architecture/2026-08-20-network-model-structure-direction.md`

### U3. The conformance gate

Files: `test/conformance/proto/api_authorization_test.go`
After: U1, U2
Change: the gate walks `protoregistry.GlobalFiles` for packages starting
with `flowseer.api.`, and for each method reads the `flowseer.authz.v1.rule`
extension. It reports a method without one, a rule that fails
`protovalidate.Validate` (nothing else validates an option value), a
request rule whose `object_id_path` does not resolve through singular
message fields to a string field of the method's input, and an (object
type, relation) pair outside a table declared in the test that holds the
parent's relation table. It fails when it walked no method, and
`TestEveryDeclaredProtoPackageIsLinked` keeps a schema file out of the
binary from escaping the walk. Each check also runs over synthetic
descriptors built with `protodesc.NewFile`, the option set through
`proto.SetExtension` as `validateOption` does in
`field_constraint_class_test.go`.
Tests: `TestEveryOperatorRPCHasAuthorizationRule`,
`TestAuthorizationRuleObjectPathResolves`,
`TestAuthorizationRuleNamesKnownRelation` (requirements 1 to 3). Each
synthetic negative is valid in every respect but the one under test and
asserts the reported reason, so deleting one check fails its case
(`docs/solutions/conventions/a-refusal-test-needs-an-input-only-the-refusal-rejects.md`).
A synthetic compliant service reports nothing.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- test/conformance/proto/api_authorization_test.go`

### U4. Principal carrier and the authorization interceptor

Files: `src/services/device/internal/authn/principal.go`, `src/services/device/internal/authn/principal_test.go`, `src/services/device/internal/authz/authz.go`, `src/services/device/internal/authz/interceptor.go`, `src/services/device/internal/authz/obligation.go`, `src/services/device/internal/authz/authz_test.go`, `src/services/device/README.md`
After: U1, U2
Change: `authn.Principal` holds `ID`, `Tenants` (tenant ids the token vouches
for), and `Platform` (the token carries the platform claim), with
`authn.NewContext` and `authn.FromContext`. `authz` declares `Tuple`,
`Query` (object, relation, user, contextual tuples), and a `Checker` with
`Check(ctx, Query) (bool, error)` and `BatchCheck(ctx, []Query) ([]bool,
error)`. `authz.Interceptor` implements `connect.Interceptor`: it reads the
rule from `req.Spec().Schema.(protoreflect.MethodDescriptor)` and refuses
by requirement 13 before anything else, then applies requirement 4. A
platform rule then runs requirement 14. Every other mode applies
requirements 5 and 6, puts the admitted tenant in the context, and runs
the rule's mode (7 and 15, 8, or 9). Requirement 10 holds on any checker
error. `authz.Require(ctx, relation, objectType, id)` returns nil or an
error. `authz.Filter(ctx, relation, objectType, ids)` deduplicates ids,
sends the relation and tenant queries for each, at most 50 queries per
`BatchCheck` (OpenFGA's default limit,
`DefaultMaxChecksPerBatchCheck` in `github.com/openfga/openfga@v1.21.0`,
which `go.mod` does not pin until phase 2), and returns the allowed set.
Both discharge the obligation, return errors already rendered through
`connecterr`, and refuse with `Internal` on a context the interceptor did
not prepare or one with no admitted tenant. The streaming handler wrapper
refuses every call, and the streaming client wrapper passes through. The
device README's Layout table gains the two packages.
Tests: `authz_test.go` serves the generated `CaptureService`,
`EdgeAdminService`, and `TenantService` handlers through `httptest` with a
fake `Checker` that records every `Query` and answers true unless the case
names one query to deny or fail. One case per requirement 4 to 15. A
denial case fails exactly one query and asserts the recorded queries and
that the handler did not run, so only the refusal under test produces it.
The cases include a `ListCaptureSessions` handler that skips `Filter` and
one that returns a response after a denied `Require` (requirement 9),
`GetCaptureSession` with its `#manage` and `#tenant` queries denied in turn
(requirement 7), and a `CreateCaptureSession` handler under its request
rule whose `Require` for `tenant#full_payload` records the one query
`tenant:<admitted>#full_payload`, returns `PermissionDenied` when denied,
becomes `Internal` when the handler ignores that, and denies another
tenant's id with no query. They also include a handler reading the admitted
tenant through `tenant.FromContext`, a `Filter` over 60 ids with one
repeated, recorded as batches of 50, 50, and 18 queries, a `Require` under
a platform rule (`Internal`), and the contextual tuples of a principal with
two tenants and the platform claim (requirement 11). The rules come from
the generated descriptors, which pins the stop condition.
`principal_test.go` covers the context round trip and an empty context.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/authn src/services/device/internal/authz src/services/device/README.md`

Waves: U1 | U2 | U3 U4

## Verification

```bash
go tool -modfile=tools/buf/go.mod buf lint
go tool -modfile=tools/buf/go.mod buf generate
go test ./test/conformance/proto/ ./src/services/device/internal/authn/ ./src/services/device/internal/authz/
.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer test/conformance/proto src/services/device docs
```

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] READMEs under `spec/proto/flowseer/authz/` and `api/`, the device
      README, and both amended records updated in the same change.
- [ ] This plan's `status` set with an outcome note under its title, and
      the parent's `Landed:` line for U1 filled.
- [ ] No plan labels in code.

## Open questions

- How `TenantService` declares its rule. This blocks `implementation-ready`:
  a platform rule names no tenant, and the parent decides that a request
  names its tenant and is admitted by membership. The plan is written for
  option 1. The others change U1's enum and CEL rules, three table rows,
  requirement 14, and U4.
  1. Recommended: `RULE_MODE_PLATFORM`, enforced in this phase as the
     Decisions describe. Cost: an RPC class outside the membership gate,
     and a row in the accepted record's mode table for a person to re-read.
  2. The same mode in the schema, refused by the interceptor like streaming
     until a phase serves `TenantService`. Cost: a declared rule nothing
     enforces or tests until then.
  3. Five modes, with `TenantService` on loaded rules naming
     `platform#admin` and its handler calling `Require`. Cost: creating or
     listing tenants passes the membership gate of an unrelated tenant, and
     the first `CreateTenant` needs a bootstrap tuple on `tenant:default`.
  4. Move `TenantService` out of `flowseer.api.` until a phase serves it.
     Cost: `spec/proto/flowseer/api/README.md` admits every operator-called
     service to `api/`, so the move breaks the root's admission rule.
- Requirement 11 sends `platform:flowseer#claimed`, and no record states
  the `platform` type's relations yet. Phase 2's model has to define
  `claimed` on it, or OpenFGA rejects every check that carries the tuple.
