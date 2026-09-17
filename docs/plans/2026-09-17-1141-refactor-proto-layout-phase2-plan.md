---
title: Protobuf Tree Phase 2 - The Edge Plane, the Northbound API, and the Event Root - Plan
type: refactor
date: 2026-09-17
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
parent: docs/plans/2026-09-17-1141-refactor-proto-layout-plan.md
---

# Protobuf Tree Phase 2 - The Edge Plane, the Northbound API, and the Event Root - Plan

## Goal

Every Connect service sits under the root that names its plane: `api/` for what
an operator calls, `edge/` for what central and an enrolled edge exchange.
`DeviceOperationEvent` lives in `event/access` and `AuditService`, which
delivers it, lives in `edge/audit`; `integration/` holds only its README until
the fabric contract lands. The means: four `git mv` units, each rewriting the
layering table and every README the move invalidates, then the new root's own
README and the record amendments.

This plan is wrong if the Connect route rename turns out to need a
compatibility route: that would mean something outside this repository already
signs the old procedure, and the parent's wire-break decision would have to be
re-taken before any package moves.

## Decisions

The parent's Decisions hold. The moves this phase makes:

| From | To | Holds |
| --- | --- | --- |
| `api/capture/v1` (`capture_edge_service`) | `edge/capture/v1` | `CaptureEdgeService.UploadCapture`; the chunk has been in `model/capture` since phase 1 |
| `integration/device/v1` (both files) | `edge/dispatch/v1` | `DispatchService`, the execution envelope |
| `event/device/v1` (`audit_service`) | `edge/audit/v1` | `AuditService.Deliver` and its request and response |
| `event/device/v1` (`operation_event`) | `event/access/v1` | `DeviceOperationEvent` and its nine kinds |
| `api/edge/v1` (`edge_service`, `bus`, `device`, `credential`) | `edge/attach/v1` | `EdgeService` and its request, response, and stream messages |
| `api/edge/v1` (`edge_admin_service`) | `api/edge/v1` | `EdgeAdminService`, unchanged path, now the only file |

- **The Go package names become `capturev1`, `dispatchv1`, `auditv1`,
  `attachv1`, and `accessv1`.** Buf managed mode derives the name from the
  last non-version segment and the version (`buf.gen.yaml` sets only
  `go_package_prefix`), which the landed tree confirms: `net/protocol/lldp/v1`
  is `lldpv1`, `store/device/v1` is `devicev1`. `event/access/v1`'s `accessv1`
  collides with `model/access/v1`, and a file importing both aliases the event
  one `eventaccessv1`. `edge/capture/v1`'s `capturev1` collides with three
  other capture packages, but only a blank import in the conformance suite
  names it, and a blank import needs no alias.
- **The alias form is `<root><leaf>v1`, so the event package is
  `eventaccessv1` and not `accesseventv1`.** Phase 1 landed `apiedgev1`,
  `modelcapturev1`, and `storeedgev1`; an alias that reversed the two
  segments would be the only one in the tree that reads backwards.
- **Aliases go away, and that is part of the move.** `integrationv1` and
  `eventv1` exist only because `integration/device/v1` and `event/device/v1`
  both generated `devicev1`; once they are `dispatchv1` and `auditv1` the
  collision is gone and the import reverts to its plain name. So does
  `integrationv1connect`/`eventv1connect` in `src/edge/agent/host/host.go` and
  `src/services/device/internal/host/serve.go`. Most of the `apiedgev1` sites
  become a plain `attachv1` for the same reason.
- **`edge/capture` stays its own package rather than folding `UploadCapture`
  into `edge/attach`.** This settles the parent's open question. The capture
  record's command path is still undecided, and
  `spec/proto/flowseer/api/capture/v1/README.md`'s "Open question: reaching the
  edge" says so; whichever way it resolves, the command is about a capture
  session, so the second RPC lands beside `UploadCapture` rather than widening
  the package an edge calls to keep its standing. Folding it in would also put
  the upload stream's messages under a README about identity and credentials.
- **The route rename is one unit per service, and the unit carries both sides.**
  A Connect route is `/<package>.<Service>/<Method>` and neither side holds it
  as a constant: the agent signs `req.URL.Path`
  (`src/edge/agent/internal/identity/client.go:135`) and central verifies
  against `r.URL.Path`
  (`src/services/device/internal/edgeapi/middleware.go:120`), while every
  handler and client path comes from the generated `*connect` package. Agent
  and central are built from one generated tree in one repository, so a unit
  that moves a service, regenerates, and fixes both sides leaves no commit at
  which the two disagree. Splitting a service's move across units is the only
  way to strand the agent, and no unit here does that.
- **The four worked-vector literals change with `EdgeService` and are
  recomputed, not hand-edited.** The procedure is inside the signed payload:
  decoding the Heartbeat vector in
  `spec/proto/flowseer/model/edge/v1/README.md` puts
  `/flowseer.api.edge.v1.EdgeService/Heartbeat` at offset 99 behind a `0x2b`
  length prefix, so a 46-byte route changes the length prefix, the payload, the
  outer message length, and the Ed25519 signature over it.
  `TestEdgeAssertionHeaderVector` and `TestEdgeAssertionStreamOpenVector`
  compute the header from their own constants and log it before comparing it to
  the README, so changing `edgeProcedure` in
  `test/conformance/proto/model_edge_rules_test.go` and running those two tests
  produces the new strings to paste. The other three copies
  (`assertion_test.go`'s `vectorHeader`, `client_test.go`'s
  `vectorStreamHeader`, `verifier_test.go`'s `readmeVector`) take the same
  values.
- **The assertion's `audience` does not change.** It is a deployment-configured
  string that central puts in `EnrollResponse`
  (`src/services/device/internal/edgeapi/enroll.go:237`, from
  `Config.AssertionAudience()`) and the agent reads back
  (`src/edge/agent/internal/identity/assertion.go:73`), so a persisted
  enrollment stays valid across the rename and there is no migration. The
  parent's wire-break Decision said the assertion signs the route as its
  audience; it signs it as `procedure`, and that sentence is corrected in the
  parent in the same change as this plan.
- **`event/` stops being a sink root, and its README says so.** `edge/audit`
  imports `event/access` for the record it delivers, so `event/README.md`'s
  "They are sinks and are imported by nothing FlowSeer-owned" becomes false the
  moment the audit service moves. `event/` holds records that other packages
  read; the sink rule is about service declarations, and `event/access`
  declares none.
- **The layering table's `integration/device` and `event/device` rows are
  removed rather than renamed in place.** `TestImportOrderCoversEveryPackage`
  only reports packages that carry schemas and declare no row, so a stale row
  for a package that no longer exists would sit in the table unreported.

Ruled: the three schema comments phase 1 left pointing at packages it had just
moved are corrected in the unit that moves `EdgeService`. They are the two in
`spec/proto/flowseer/store/edge/v1/agent_config.proto` citing
`flowseer.api.edge.v1.EdgeProvisioning`, whose message has been in
`model/edge/v1/provisioning.proto` since phase 1, and the one in
`spec/proto/flowseer/api/edge/v1/device.proto:30` saying the package sits
"below api/inventory in the import graph", which has been `model/inventory`
since phase 1. Why: all three name a path or full name that does not exist in
the tree today, `device.proto` is a file the attach unit moves anyway, and the
attach unit is already correcting every other `flowseer.api.edge.v1.*`
citation. Leaving the `store/edge` pair for phase 3's `store/agent` rename
would carry a wrong name through another phase. Cost if wrong: two comment
lines in a file this phase otherwise does not touch, which phase 3 rewrites the
header of anyway.

Ruled: each move unit updates the prose in other packages' READMEs that names
the package it moves, not just the gated `Imports:` and `Imported by:` lines.
Why: `TestProtoReadmeImports` reads only the two lines, so a sentence such as
`model/access/v1/README.md`'s "envelope in `integration/device/v1`" would stay
green while pointing at a directory that no longer exists, and the repository
rule is that docs change in the change that invalidates them. Cost if wrong: a
unit's diff is a few lines wider than its schema move.

## Requirements

1. `spec/proto/flowseer/edge/` holds `attach`, `dispatch`, `audit`, and
   `capture`, each under `v1/`; `spec/proto/flowseer/event/` holds `access`;
   `spec/proto/flowseer/integration/` holds only `README.md`. Example:
   `ls spec/proto/flowseer/edge` prints the four names and `README.md`, and
   `find spec/proto/flowseer/integration -name '*.proto'` prints nothing.
2. `api/edge/v1` holds `edge_admin_service.proto` alone and imports only
   `model/edge`. Example: `TestProtoReadmeImports` passes with
   `api/edge/v1/README.md` reading `Imports: model/edge`, and fails when it
   still names `model/credential`.
3. An edge enrolls, heartbeats, lists its devices, subscribes to dispatches,
   and delivers audit records against the new route names in one run. Example:
   `TestAnAgentOnboardsTheDeviceCentralListsForIt` and
   `TestTheAuditStreamAccountsForTheChangeInOrder` in
   `src/services/device/test/integration` pass, with the agent's requests
   reaching `/flowseer.edge.attach.v1.EdgeService/Heartbeat`.
4. Both worked header vectors in `spec/proto/flowseer/model/edge/v1/README.md`
   carry the new procedure, and the three Go copies match them byte for byte.
   Example: `TestEdgeAssertionHeaderVector`,
   `TestTheHeaderMatchesTheSpecifiedVector`, and
   `TestVerifierAcceptsTheReadmeVector` all pass, and
   `grep -rn 'flowseer\.api\.edge\.v1\.EdgeService' spec src test | wc -l`
   prints 0.
5. The conformance rules tests whose names encode the old paths are renamed and
   keep their cases: `api_edge_bus_credential_rules_test.go` and the
   `EdgeService` half of `api_edge_rules_test.go` become
   `edge_attach_rules_test.go`; `integration_device_rules_test.go` becomes
   `edge_dispatch_rules_test.go`; `event_device_rules_test.go` splits into
   `edge_audit_rules_test.go` and `event_access_rules_test.go`. Example:
   `go test ./test/conformance/proto/ -run 'TestAttachBusResponseRules|TestDeliverRequestRules|TestDispatchStreamRules|TestDeviceOperationEventRules'`
   passes and names four tests.
6. Every package declared in the tree is linked into the conformance binary.
   Example: `TestEveryDeclaredProtoPackageIsLinked` passes with a blank import
   of `edge/capture/v1` beside the existing one for `api/capture/v1`.
7. The structure record's tree shows the tree after this phase, with `runtime/`
   and `store/agent` still marked as phase 3, and every row of its import graph
   is within `importOrder`. Example: the record's row
   `event/access ← edge/audit` exists and `importOrder["edge/audit"]` contains
   `event/access`.

## Out of scope

- Any new RPC, including the capture command the remote capture record leaves
  open, and any change to a message's fields or validation rules.
- `store/edge` to `store/agent` and `service/v1` to `runtime/v1`. Phase 3, which
  also does the closing pass over every `Imported by:` line and the root map's
  nine roots.
- `EdgeAdminService`, which stays at `api/edge/v1` and keeps its route, so
  `docs/runbooks/lab-icx7150-first-write.md`'s `CreateEdge` call is unchanged.
- The `audience` value and anything that would migrate a persisted enrollment.

## Units

### U1. Move the capture upload service to the edge plane

Files: spec/proto/flowseer/api/capture/v1/capture_edge_service.proto,
spec/proto/flowseer/edge/capture/v1/{capture_edge_service.proto,README.md},
spec/proto/flowseer/edge/README.md,
spec/proto/flowseer/api/capture/v1/README.md, spec/proto/flowseer/api/README.md,
spec/proto/flowseer/model/README.md,
spec/proto/flowseer/model/capture/v1/README.md,
spec/proto/flowseer/model/edge/v1/README.md,
generated/go/proto/flowseer/**, test/conformance/proto/layering_test.go,
test/conformance/proto/field_constraint_class_test.go
After: none
Change: `git mv` moves `capture_edge_service.proto` to
`spec/proto/flowseer/edge/capture/v1/`, its `package` line reads
`flowseer.edge.capture.v1`, and `buf generate` rewrites the Go tree. This is
the unit that creates the `edge/` root, so `orderedRoots` gains
`flowseer/edge` and `importOrder` gains
`"edge/capture": {"model/capture", "model/edge"}`; `api/capture` keeps its row,
because `capture_service.proto` still imports all three of `model/capture`,
`model/edge`, and `net/capture`. `edge/README.md` is created with the root
shape: identity, admission, the two boundary lines, and one `## Packages` line.
`edge/capture/v1/README.md` takes the "Re-assertion on the upload stream"
section and the sentence naming `CaptureEdgeService` out of
`api/capture/v1/README.md`, which keeps `CaptureService`, the open question
about reaching the edge, and a first paragraph saying the upload service moved.
`model/capture/v1/README.md` and `model/edge/v1/README.md` add `edge/capture` to
`Imported by:`, and `model/capture`'s prose sentence that the services "live in
`api/capture/v1`" names both packages. `model/README.md` adds `edge/capture`;
`api/README.md`'s `## Packages` line for `capture/v1/` drops
`CaptureEdgeService`. `field_constraint_class_test.go` gains a blank import of
`edge/capture/v1` beside the one it already has for `api/capture/v1`, since
neither package has an example-based test.
Tests: `TestEveryDeclaredProtoPackageIsLinked` passes with the new blank
import and fails without it, naming
`flowseer/edge/capture/v1/capture_edge_service.proto`;
`TestOrderedRootsCoverEveryTopLevelTree` passes with `flowseer/edge` declared
and reports it when the root is left out; `TestImportOrderCoversEveryPackage`
passes; `TestLayeringViolationRules` gains
`{importer: "edge/capture", imported: "model/capture"}` expecting no violation
and `{importer: "model/capture", imported: "edge/capture"}` with
`wantReason: "edge/capture declares a service and is imported by nothing"`;
`TestProtoReadmeCoverage` names no directory, since `edge/` carries a README
and `edge/capture/` is a bare version holder; `TestProtoReadmeImports` passes
for the two READMEs this unit creates and the five it edits.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto test/conformance/proto`

### U2. Move the execution envelope to the edge plane

Files: spec/proto/flowseer/integration/device/v1/{dispatch.proto,execution.proto,README.md},
spec/proto/flowseer/edge/dispatch/v1/{dispatch.proto,execution.proto,README.md},
spec/proto/flowseer/integration/README.md, spec/proto/flowseer/edge/README.md,
spec/proto/flowseer/errs/README.md, spec/proto/flowseer/errs/v1/README.md,
spec/proto/flowseer/model/README.md,
spec/proto/flowseer/model/access/v1/README.md,
spec/proto/flowseer/api/edge/v1/README.md,
generated/go/proto/flowseer/**, test/conformance/proto/layering_test.go,
test/conformance/proto/integration_device_rules_test.go renamed
test/conformance/proto/edge_dispatch_rules_test.go,
src/edge/agent/host/{host.go,report.go,lane_test.go},
src/edge/agent/internal/dispatch/{demux.go,demux_test.go,registry.go,registry_test.go,subscribe.go,subscribe_test.go},
src/edge/agent/internal/report/{kind.go,queue.go,queue_test.go},
src/modules/localnet/access/{lane.go,lane_test.go,ack_test.go,credential_test.go,freeze_audit_test.go,hold_test.go,horizon_test.go,recovery_entry_test.go,recovery_loop_test.go},
src/modules/localnet/access/internal/mutation/{machine.go,machine_test.go,latch_test.go},
src/modules/localnet/access/internal/recovery/recovery_test.go,
src/services/device/internal/dispatchapi/{service.go,relay.go,relay_test.go,report.go,report_test.go,subscribe_e2e_test.go},
src/services/device/internal/host/{serve.go,serve_internal_test.go}
After: U1
Change: `git mv` moves both files and the package README to
`spec/proto/flowseer/edge/dispatch/v1/`, the `package` line reads
`flowseer.edge.dispatch.v1`, and `dispatch.proto`'s intra-package import
follows. `importOrder` drops `integration/device` and gains
`"edge/dispatch": {"model/access", "errs"}`; `orderedRoots` keeps
`flowseer/integration`, whose directory survives with its README alone and
which `protoFilesUnder` walks to an empty result. `integration/README.md` loses
its `DispatchService` admission example and its `## Packages` entry and reads
`Imports: nothing FlowSeer-owned` and `Imported by: nothing`, saying the root is
reserved for the fabric contract the device service record names. `errs/README.md`,
`errs/v1/README.md`, and `model/access/v1/README.md` swap `integration/device`
for `edge/dispatch` on `Imported by:`; `model/access`'s prose sentence about the
"envelope in `integration/device/v1`" and `api/edge/v1/README.md`'s two
sentences naming `integration/device/v1` follow. `model/README.md` and
`edge/README.md` gain the package. Go files drop the `integrationv1` alias for
the plain `dispatchv1` and `integrationv1connect` for `dispatchv1connect`; in
`src/services/device/internal/host/serve.go` and `src/edge/agent/host/host.go`
that removes an alias rather than renaming one.
Tests: `edge_dispatch_rules_test.go` passes with its six tests keeping their
cases, the import path and the `integrationv1` identifier being all that
changes in them; `TestLayeringViolationRules`' three cases naming `integration/device`
read `edge/dispatch`, and the sink case keeps
`wantReason: "edge/dispatch declares a service and is imported by nothing"`;
`TestImportOrderCoversEveryPackage` passes and reports `edge/dispatch` when the
row is left out; `TestProtoReadmeImports` passes for the six edited READMEs and
reports `integration/README.md` when its old lines are kept; the agent's
dispatch tests under `src/edge/agent/internal/dispatch` and
`TestAnAgentOnboardsTheDeviceCentralListsForIt` pass.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto test/conformance/proto src`

### U3. Split the audit event from the service that delivers it

Files: spec/proto/flowseer/event/device/v1/{audit_service.proto,operation_event.proto,README.md},
spec/proto/flowseer/edge/audit/v1/{audit_service.proto,README.md},
spec/proto/flowseer/event/access/v1/{operation_event.proto,README.md},
spec/proto/flowseer/event/README.md, spec/proto/flowseer/edge/README.md,
spec/proto/flowseer/model/README.md,
spec/proto/flowseer/model/access/v1/README.md,
spec/proto/flowseer/model/inventory/v1/README.md,
spec/proto/flowseer/api/edge/v1/README.md,
generated/go/proto/flowseer/**, test/conformance/proto/layering_test.go,
test/conformance/proto/event_device_rules_test.go split into
test/conformance/proto/{edge_audit_rules_test.go,event_access_rules_test.go},
src/edge/agent/host/{host.go,lane_test.go},
src/edge/agent/internal/report/{deliver.go,deliver_test.go},
src/modules/localnet/access/{lane.go,lane_test.go,ack_test.go,epoch_test.go,freeze_audit_test.go,hold_test.go,recovery_entry_test.go},
src/modules/localnet/access/internal/audit/{event.go,event_test.go},
src/modules/localnet/access/internal/mutation/{machine.go,machine_test.go},
src/modules/localnet/access/internal/recovery/{recovery_test.go,verified_test.go},
src/services/device/internal/auditapi/{service.go,service_test.go},
src/services/device/internal/centralaudit/{centralaudit.go,centralaudit_test.go},
src/services/device/internal/host/serve.go,
src/services/device/test/integration/fixture_test.go
After: U2
Change: `git mv` moves `audit_service.proto` to
`spec/proto/flowseer/edge/audit/v1/` and `operation_event.proto` to
`spec/proto/flowseer/event/access/v1/`, leaving `event/device/` empty and
removed; `audit_service.proto`'s import becomes
`flowseer/event/access/v1/operation_event.proto`, which is now a cross-package
import. `importOrder` drops `event/device` and gains
`"edge/audit": {"event/access"}` and
`"event/access": {"model/inventory", "model/access", "errs"}`, keeping `errs`
as the allowlist entry the structure record already calls wider than the tree.
`event/device/v1/README.md` splits: the record's identity, "One record, nine
kinds", and "Why this package imports model/inventory directly" go to
`event/access/v1/README.md`; "Delivery" goes to `edge/audit/v1/README.md` with
the `Deliver` contract. `event/README.md` reads `Imported by: edge/audit`, its
`## Packages` line names `access/v1/`, and the sentence calling the root's
packages sinks imported by nothing FlowSeer-owned is replaced: `event/` holds
records that a delivering service reads, and the sink rule speaks about service
declarations, which no package here makes. `model/access/v1/README.md` and
`model/inventory/v1/README.md` swap `event/device` for `event/access`;
`model/README.md` and `edge/README.md` follow, and `api/edge/v1/README.md`'s two
sentences naming `event/device/v1` read `edge/audit/v1`. Go files using
`DeviceOperationEvent` and its kinds take `eventaccessv1` where they also
import `model/access`, otherwise `accessv1`; the seven files touching
`AuditService`, `DeliverRequest`, or `DeliverResponse` take `auditv1`, and
`src/edge/agent/host/host.go`, `src/edge/agent/internal/report/deliver.go`, and
`src/services/device/internal/{auditapi/service.go,centralaudit/centralaudit.go,host/serve.go}`
take one of each. `eventv1connect` becomes `auditv1connect` with no alias.
Tests: `event_access_rules_test.go` carries `TestDeviceOperationEventRules`,
`TestPhaseTransitionedRules`, and `TestDeviceOperationEventKindRules` with their
cases intact and only the `eventv1` identifier changed, and
`edge_audit_rules_test.go` carries `TestDeliverRequestRules`,
which is what links `edge/audit/v1` into the conformance binary, so
`TestEveryDeclaredProtoPackageIsLinked` passes without a blank import for it;
`TestLayeringViolationRules`' cases naming `event/device` read `event/access`,
and a new `{importer: "edge/audit", imported: "event/access"}` expects no
violation while `{importer: "event/access", imported: "edge/audit"}` expects
`wantReason: "edge/audit declares a service and is imported by nothing"`;
`TestProtoReadmeImports` reports `event/README.md` when its `Imported by:` line
still reads `nothing`; `TestTheAuditStreamAccountsForTheChangeInOrder` passes.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto test/conformance/proto src`

### U4. Move the edge standing service to the edge plane

Files: spec/proto/flowseer/api/edge/v1/{edge_service.proto,bus.proto,device.proto,credential.proto,README.md},
spec/proto/flowseer/edge/attach/v1/{edge_service.proto,bus.proto,device.proto,credential.proto,README.md},
spec/proto/flowseer/edge/README.md, spec/proto/flowseer/api/README.md,
spec/proto/flowseer/model/README.md, spec/proto/flowseer/net/README.md,
spec/proto/flowseer/model/edge/v1/{README.md,assertion.proto},
spec/proto/flowseer/model/credential/v1/README.md,
spec/proto/flowseer/model/policy/v1/README.md,
spec/proto/flowseer/net/addr/v1/README.md,
spec/proto/flowseer/store/edge/v1/agent_config.proto,
generated/go/proto/flowseer/**, test/conformance/proto/layering_test.go,
test/conformance/proto/{api_edge_bus_credential_rules_test.go,api_edge_rules_test.go}
becoming test/conformance/proto/{edge_attach_rules_test.go,api_edge_rules_test.go},
test/conformance/proto/model_edge_rules_test.go,
src/edge/agent/host/{host.go,host_test.go,lane_test.go},
src/edge/agent/internal/busattach/{busattach.go,busattach_test.go},
src/edge/agent/internal/identity/{assertion.go,assertion_test.go,client_test.go,enroll.go,enroll_test.go,store.go},
src/edge/agent/internal/lanehost/{heartbeat.go,heartbeat_test.go,onboard.go,onboard_test.go,session.go,session_test.go},
src/modules/localnet/access/{access.go,lane.go,lane_test.go,credential_test.go,drain_panic_test.go,epoch_test.go,fakesession_test.go,read_route_test.go},
src/modules/localnet/access/internal/credential/{connect_adapter.go,connect_adapter_test.go,source.go,relay_internal_test.go},
src/modules/localnet/access/internal/mutation/{machine.go,machine_test.go,latch_test.go},
src/services/device/internal/edge/{verifier.go,verifier_test.go},
src/services/device/internal/edgeapi/{service.go,service_test.go,admin.go,admin_test.go,enroll.go,export_test.go,listdevices_test.go,submission.go,submission_test.go,middleware_test.go},
src/services/device/internal/host/{serve.go,host_test.go},
src/services/device/test/integration/{agent_seams_test.go,agent_test.go,device_test.go,e2e_test.go,fixture_test.go},
src/services/device/test/integration/testdata/mutation-verification-repro/{device_test.go.repro,e2e_test.go.repro,fixture_test.go.repro}
After: U3
Change: `git mv` moves the four files to `spec/proto/flowseer/edge/attach/v1/`,
their `package` line reads `flowseer.edge.attach.v1`, and `edge_service.proto`'s
three intra-package imports follow the directory. `api/edge/v1` is left holding
`edge_admin_service.proto`, so `importOrder`'s `api/edge` row narrows to
`{"model/edge"}` and gains
`"edge/attach": {"model/edge", "model/policy", "model/credential", "net/addr"}`.
The Connect routes change with the package, which is the wire break the parent
states; nothing in production code spells a route, so the change reaches the
wire through `buf generate` alone. The five procedure literals that are test
data are updated together with the vectors they sign:
`test/conformance/proto/model_edge_rules_test.go`'s `edgeProcedure` and its
`OpenDeviceSubmission` string, `src/services/device/internal/edge/verifier_test.go`'s
`testProcedure`, `src/services/device/internal/edgeapi/middleware_test.go`'s
`testPath`, `src/edge/agent/internal/identity/enroll_test.go`'s inline
Heartbeat path, and `client_test.go`'s three request URLs. Running
`TestEdgeAssertionHeaderVector` and `TestEdgeAssertionStreamOpenVector` after
that logs the two recomputed headers; they are pasted into the two code blocks
in `spec/proto/flowseer/model/edge/v1/README.md` and into `assertion_test.go`'s
`vectorHeader`, `client_test.go`'s `vectorStreamHeader`, and
`verifier_test.go`'s `readmeVector`, and the README's prose naming the vector's
procedure follows. `model/edge/v1/assertion.proto`'s example procedure comment
and `assertion.go`'s `Header` doc comment read the new route, and the three
stale phase 1 citations named in the ruling above are corrected: the two in
`store/edge/v1/agent_config.proto` to `flowseer.model.edge.v1.EdgeProvisioning`
and `device.proto`'s to `model/inventory`. `api_edge_bus_credential_rules_test.go` is renamed
`edge_attach_rules_test.go` and takes `TestEdgeRequestRules`' `EnrollRequest`
and `HeartbeatRequest` cases out of `api_edge_rules_test.go`, which keeps the
`CreateEdgeRequest`, `IssueSetupKeyRequest`, and `ListEdgesRequest` cases and
`TestIntegrationConfigHostRules`. `api/edge/v1/README.md` keeps the
`EdgeAdminService` paragraph and reads `Imports: model/edge`; everything from
"Three lifecycles, never the same call" down, and the `Deliberately absent:`
entry about the RPC handlers, moves to `edge/attach/v1/README.md`, where the
two sentences explaining that `api/edge` sits below `model/inventory` and so
cannot name a `DeviceGlobalRef` name `edge/attach` instead. The other
`Deliberately absent:` entry, the Edge entity's admission to `EntityType`, goes
to `model/edge/v1/README.md` instead: it is a property of the entity, which
moved there in phase 1, and neither service package is where a reader looks for
it. `model/credential/v1`, `model/policy/v1`, `net/addr/v1`, and
`net/README.md` swap `api/edge` for `edge/attach` on `Imported by:`;
`model/edge/v1` keeps `api/edge` and adds `edge/attach`, and its
`Deliberately absent:` sentence naming the two services that live in
`api/edge/v1` names both packages. `api/README.md`'s `Imports:` narrows to
`model/access, model/capture, model/edge, model/inventory, net/capture` —
`net/addr` leaves with the device listing and the credential handles — and its
`## Packages` line for `edge/v1/` describes `EdgeAdminService` alone. Go files
importing only the service package drop the `apiedgev1` alias for a plain
`attachv1`; those importing `api/edge` for the admin messages keep it beside
`edgev1`, and `edgev1connect` stays for `NewEdgeAdminServiceHandler` beside a new
`attachv1connect` for `NewEdgeServiceHandler` in
`src/services/device/internal/host/serve.go`.
Tests: `TestTheHeaderMatchesTheSpecifiedVector`,
`TestVerifierAcceptsTheReadmeVector`, `TestEdgeAssertionHeaderVector`, and
`TestEdgeAssertionStreamOpenVector` pass against the recomputed strings, and
each fails on the old one, since the procedure is inside the signed payload;
`TestTheHeaderBindsTheBodyAndTheProcedure` keeps its two distinct procedures
under the new package; `edge_attach_rules_test.go` and the reduced
`api_edge_rules_test.go` pass; `TestLayeringViolationRules`' cases naming
`api/edge` as the importer read `edge/attach`, and the cases that import
`api/edge` keep `api/edge` so the sink rule is still exercised against the
package that keeps a service; `TestAnAgentOnboardsTheDeviceCentralListsForIt`,
`TestAnOperatorsReadReachesTheDeviceAndComesBack`, and
`TestAnAppliedDescriptionReachesTheDeviceAndComesBack` pass, which is the
evidence that enrollment, heartbeat, the device listing, and both credential
lanes work on the new routes end to end.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto test/conformance/proto src`

### U5. The edge root's README and the root map

Files: spec/proto/flowseer/README.md, spec/proto/flowseer/edge/README.md,
spec/proto/flowseer/edge/{attach,dispatch,audit,capture}/v1/README.md,
spec/proto/flowseer/api/README.md, spec/proto/flowseer/event/README.md,
spec/proto/flowseer/integration/README.md
After: U4
Change: `edge/README.md` gets the identity and admission prose the four moves
could only stub, written against all four landed packages: identity says the
root holds Connect services between central and an enrolled edge in either
direction, admission passes `edge/attach` because an edge calls it on its own
behalf and fails `api/edge` because an operator calls it about an edge, and the
`## Packages` section carries one line each. `spec/proto/flowseer/README.md`'s
root map gains the `edge/` line and rewrites the `integration/` line to say the
root is reserved and holds only a README; the roots' order sentence places
`edge/` beside `api/`. `api/README.md`'s identity paragraph says `api/` is what
an operator, the web app, or a workflow calls, now that the edge-facing
services have left, and its admission example contrasts `api/edge` with
`edge/attach` rather than with `model/access`. `event/README.md`'s admission
example reads `event/access` and names `edge/audit` as the service that
delivers the record. The four new package READMEs get their
`Deliberately absent:` lines settled against each other, so that no two of them
claim the same absence and each names what a reader of that package would look
for and not find.
Tests: `TestProtoReadmeCoverage` and `TestProtoReadmeImports` pass; the
`Imports:` and `Imported by:` lines are not touched by this unit, so a
violation reported here means an earlier unit's line was wrong.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto test/conformance/proto`

### U6. Amend the direction records

Files: docs/architecture/2026-08-20-network-model-structure-direction.md,
docs/architecture/{2026-08-20-device-service-and-inventory,2026-09-05-verified-device-access,2026-09-09-remote-packet-capture,2026-09-09-streaming-frame-transport}-direction.md
After: U4
Change: The structure record's "The package tree" section shows `edge/` with
its four packages, `event/access/v1/`, `integration/` holding only a README, and
`api/` with three packages, keeping `runtime/` and `store/agent` marked as
landing with phase 3. The paragraph below it says two roots rather than four
are not real yet: `edge/` leaves the list, and `integration/`'s sentence stops
describing the move as a future condition and says the root now holds only a
README, with the fabric contract still reserved. Its import-order block replaces
the `api/edge`, `api/capture`, `integration/device`, and `event/device` rows
with the rows this phase lands, among them `event/access ← edge/audit`, and
each row stays within the landed `importOrder`. The prose beneath it that
explains what `api/edge` and `api/capture` import is rewritten for
`edge/attach` and `edge/capture`, and the sentence granting `errs` to
`event/device` names `event/access`. A dated amendment,
"2026-09-17 — the edge plane is its own root", states the four moves, the route
rename and that the audience is unaffected, and names this plan. The four other
records each take a one-line addition to the phase 1 amendment they already
carry. The device service and verified device access records, which call
`integration/device` and `event/device` unchanged there, say those now read
`edge/dispatch`, `edge/audit`, and `event/access`, and that `api/edge`'s
`EdgeService` reads `edge/attach` while `EdgeAdminService` stays. The remote
capture record says `CaptureEdgeService` left `api/capture` for `edge/capture`
and that its own open question about reaching the edge moved with it. The
streaming frame transport record says the assertion's `procedure` example now
names the `edge/attach` route, since the record is about what the assertion
binds.
`docs/conventions/protobuf.md` needs no edit: phase 1 replaced its last
citation of a package this phase moves, and the only schema paths left in it
are under `net/`.
Tests: none; prose. The verifier's documentation checks run on the paths.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/architecture`

Waves: U1 | U2 | U3 | U4 | U5 U6

The four moves are a chain because each one rewrites `importOrder` and
`orderedRoots` in `test/conformance/proto/layering_test.go` and the
`Imported by:` lines in `spec/proto/flowseer/{model,edge}/README.md`, and the
two README gates are tree-wide, so no two of them can be in flight at once. U5
and U6 touch disjoint files and both only need the finished tree.

## Verification

```bash
buf format --diff --exit-code && buf lint
buf generate && git status --porcelain generated/   # empty after commit
go build ./... && go vet ./...
go test -race ./test/conformance/... ./src/...
.claude/skills/verify-change/scripts/verify-change.sh --full
```

Then, with `$TMPDIR` holding the output:

```bash
grep -rn 'flowseer/api/edge/v1/\(edge_service\|bus\|device\|credential\)\|flowseer\.api\.edge\.v1\.\(EdgeService\|EnrollRequest\|AttachBus\|ListedDevice\|DeviceCredential\|EdgeProvisioning\)\|flowseer/integration/device\|flowseer\.integration\.device\|flowseer/event/device\|flowseer\.event\.device\|flowseer/api/capture/v1/capture_edge_service\|flowseer\.api\.capture\.v1\.\(CaptureEdgeService\|UploadCapture\)' spec src test docs deploy tools
```

It prints only this plan, the parent, and the record amendments that describe
the move. `flowseer.device.*` and `flowseer.service.*` without a version
segment are OpenTelemetry attribute names under
`docs/conventions/observability.md` and do not change, and
`EdgeAdminService`'s route is deliberately still `flowseer.api.edge.v1`.

The lab runbook is not re-run: its only edge-plane call is
`EdgeAdminService/CreateEdge`, whose route is unchanged, and
`TestTheRunbooksCommandsRun` covers it.

## Definition of done

- [ ] Verifier green with `--full`, and `go test -race ./test/conformance/...
      ./src/...` green after each unit.
- [ ] `ls spec/proto/flowseer` prints `api edge errs event integration model
      net service store README.md`, and `edge/` holds the four packages.
- [ ] Both README gates pass, every README the moves touched names the packages
      the tree shows, and `edge/README.md` has the four sections the parent's
      Decisions fix.
- [ ] The two worked header vectors are recomputed and the three Go copies
      match; no `flowseer.api.edge.v1.EdgeService` string survives outside the
      plans.
- [ ] The structure record's tree matches `ls`, every row of its import graph
      is within `importOrder`, and the four other records' phase 1 amendments
      carry the follow-up line.
- [ ] This plan's `status` is `implemented` with an outcome note under the
      title, and the parent's U2 `Landed:` line carries the commit range.
- [ ] No plan label appears in code, comments, READMEs under `spec/`, or commit
      messages.

## Open questions

- How an operator-originated capture command reaches the edge. The capture
  record and `api/capture/v1/README.md` both leave it open, and this phase
  keeps `edge/capture` as the package that will hold the answer. Nothing in
  these units depends on it; the open question moves to
  `edge/capture/v1/README.md` with the rest of the upload service's prose.
- Whether `event/` earns a second package or `event/access` is the whole root.
  It is the only package there after U3, and the admission test in
  `event/README.md` is written against one example. Phase 3's README pass sees
  the finished tree and can say whether the root's prose should be narrowed to
  the audit record it actually holds.
