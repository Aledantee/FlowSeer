---
title: Protobuf Tree Phase 3 - The Process-Private Roots and the Final README Pass - Plan
type: refactor
date: 2026-09-17
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept after fixes
compound: docs/solutions/architecture-patterns/a-package-rename-breaks-names-you-persisted-not-records-you-encoded.md
execution: mixed
parent: docs/plans/2026-09-17-1141-refactor-proto-layout-plan.md
amends: docs/architecture/2026-08-20-network-model-structure-direction.md
---

# Protobuf Tree Phase 3 - The Process-Private Roots and the Final README Pass - Plan

> Implemented. 4 units, 2026-09-18T07:47Z to 2026-09-18T08:20Z.
>
> Reviewed once over the finished tree. No blocker. Five prose defects fixed,
> among them an amendment that said a pre-rename bus store's queued records no
> longer resolve, which would have told an operator to discard a store that
> reads back fine.

## Goal

`ls spec/proto/flowseer` prints the nine roots the parent fixes and nothing
else: `store/edge` is `store/agent`, `service/v1` is `runtime/v1`, and no
README or architecture record describes a move still to come. The means:
two `git mv` units that carry their own README and gate edits, one unit
that corrects a false claim about the edge assertion in the streaming-frame
record, and one unit that leaves the three records naming the finished tree.

This plan is wrong if renaming `flowseer.service.v1` turns out to be
blocked by data someone is keeping: the package's own file comment calls
its full name a persisted identity, and a deployment with a local bus store
it has to keep would need a migration this plan does not write.

## Decisions

The parent's Decisions hold. Specific to this phase:

- `runtime/` stays outside `orderedRoots` and `importOrder`. The name lives
  in `unorderedRoots` in `test/conformance/proto/layering_test.go`, which
  `TestOrderedRootsCoverEveryTopLevelTree` reads to know the root is
  accounted for and `TestUnorderedRootsImportNothingFlowSeerOwned` reads to
  hold it to importing nothing FlowSeer-owned. Why: the local bus contract
  is process-local, so "which packages may it import" has no answer to put
  in the table, and the parent keeps it out of the boundary order for that
  reason.
- The Go package name for `runtime/v1` is `runtimev1` and for
  `store/agent/v1` is `agentv1`, both derived by buf managed mode from the
  last two path segments. Fifteen files import the first (thirteen under
  `src/common/service`, two under `test/conformance/proto`) and four import
  the second. Why: `buf.gen.yaml` sets one `go_package_prefix` and no
  per-package override, and the parent rejects adding one.
- `src/common/service` keeps its Go package name, and so do the
  OpenTelemetry names `flowseer.service.startup`,
  `flowseer.service.module.*`, `flowseer.service.message.operations`, and
  `flowseer.service.bus.fsync.*`. Why: they are semantic-convention names
  under `docs/conventions/observability.md`, not protobuf packages. Only a
  name carrying a `.v1` segment or a generated import path changes, so the
  rename is not a blanket substitution of `flowseer.service.`.
- `runtime/README.md` stays, although `runtime/` holds only `v1/` and
  `TestProtoReadmeCoverage` would not ask for it. Why: `errs/` is the same
  shape and carries both a root and a package README, the root map in
  `spec/proto/flowseer/README.md` points a reader at each root's own file,
  and a root with no Admission section would be the only one of the nine a
  reader cannot compare.
- The four `TestService*` functions in the two conformance files move to
  `TestRuntime*` with the files. Why: the parent renames the root because
  `service` means "Connect service" everywhere else in the tree, and
  `TestServiceMessageValidation` sitting in `package conformance` beside the
  rule tests for the Connect services reads as one of those.
- Ruled: the streaming-frame record's claim that the edge assertion "does
  not bind the RPC method or the body" is corrected in this phase, in place.
  Why: the field binding landed on 2026-09-05 in `3d06f2a9`, and the
  `api/edge/v1/README.md` correction landed the same day, one commit
  earlier, in `070d7d21` — four days before
  `docs/architecture/2026-09-09-streaming-frame-transport-direction.md` was
  written on a branch that did not have it, so the record was false on the
  merged tree from the day it landed and this refactor did not make it so.
  The record is `status: proposed-direction`, which the plan skill's
  direction-record reference leaves editable, and the fact is checkable in
  `spec/proto/flowseer/model/edge/v1/assertion.proto:47,57`. What the
  correction raises and does not answer — what a mid-stream re-assertion
  frame's `procedure` and `body_sha256` bind to, where there is no second
  HTTP body to hash — stays a question for a person, under Open questions.
  Cost if wrong: a person disagrees with the wording of one paragraph and
  one Sources bullet in a proposed record, and rewrites them.
- Renaming `flowseer.service.v1` breaks a persisted local bus store's
  `RuntimeManifest`. A store written before U2 carries `envelope_type:
  "flowseer.service.v1.Message"`, and `reconcileRuntimeManifest` refuses to
  start once `manifestAdditionCompatible` finds that value stale:
  `migrationRequired()` demands a migration this plan does not write. Queued
  `Message` records are not broken the same way: the wire format carries no
  package or message name, so `TestMessageV1Compatibility` decodes an
  unmodified pre-rename fixture into the renamed type without a migration —
  this plan wrongly said their descriptor name "no longer resolves"; nothing in
  a persisted `Message` names a descriptor at all. `AGENTS.md` says to state a
  break rather than shim it: a deployment holding a pre-rename manifest
  discards its bus store directory. Why this is cheap here: every store in
  existence belongs to a test or a lab run.

## Requirements

1. `ls spec/proto/flowseer` prints `README.md`, `api`, `edge`, `errs`,
   `event`, `integration`, `model`, `net`, `runtime`, `store` and nothing
   else, and `ls spec/proto/flowseer/store` prints `README.md`, `agent`,
   `device`.
2. The layering gates account for both renamed roots. Example: with
   `unorderedRoots` left as `{"flowseer/service"}`,
   `TestOrderedRootsCoverEveryTopLevelTree` fails with `flowseer/runtime
   carries schemas but is in neither orderedRoots nor unorderedRoots`; with
   `importOrder` still keyed `"store/edge"`,
   `TestImportOrderCoversEveryPackage` fails with `store/agent carries
   schemas but declares no layer in importOrder`.
3. Every generated package under the two moved paths is linked into the
   conformance binary. Example: with `field_constraint_class_test.go`'s
   blank import left at `.../flowseer/store/edge/v1`, the package does not
   build; with the import removed altogether,
   `TestEveryDeclaredProtoPackageIsLinked` fails naming
   `flowseer/store/agent/v1/agent_config.proto`.
4. Every `Imports:` and `Imported by:` line under `spec/proto/flowseer/`
   names exactly the packages the tree shows. Example:
   `TestProtoReadmeImports` passes, and `model/edge/v1/README.md`'s
   `Imported by:` reads `api/capture, api/edge, edge/attach, edge/capture,
   model/access, model/capture, model/inventory, store/device`; dropping
   `api/capture` from it fails the test naming the file and the field.
5. No README under `spec/proto/flowseer/` describes a rename still to come.
   Example: `grep -rn 'pending rename' spec/proto/flowseer --include=README.md`
   prints nothing, and the ninth root line of the tree block in
   `spec/proto/flowseer/README.md` begins `runtime/`.
6. The network model structure record's tree matches `ls` and marks nothing
   as absent. Example: `grep -n 'pending rename\|do not exist yet'
   docs/architecture/2026-08-20-network-model-structure-direction.md` prints
   nothing, and the record's tree lists `runtime/v1/` and `store/agent/v1/`.
7. No architecture record states that the edge assertion leaves the RPC
   method or the request body unbound. Example: `grep -rn 'does not bind'
   docs/architecture/` prints only lines inside the amendment that records
   the correction and quotes the old wording.

## Out of scope

- Any move other than the two named, and any rename of the Go package
  `src/common/service` or of the message, enum, and field names inside the
  two moved packages.
- A migration for a local bus store written before the rename. The
  Decisions state the break.
- The design question the assertion correction raises. U3 states it as
  unsettled in the record's own body and Open questions carries it out of
  this plan.
- `docs/solutions/architecture-patterns/local-bus-durability-is-a-runtime-setting-not-storage-identity.md`
  cites `flowseer.service.bus.*`, which are OpenTelemetry attribute names
  under `docs/conventions/observability.md` and not protobuf packages.

## Units

### U1. The agent's deployment file moves to store/agent

Files: `spec/proto/flowseer/store/edge/v1/{agent_config.proto,README.md}`
moved to `spec/proto/flowseer/store/agent/v1/`,
`spec/proto/flowseer/store/README.md`, `generated/go/proto/flowseer/**`,
`test/conformance/proto/{layering_test.go,field_constraint_class_test.go}`,
`src/edge/agent/host/config.go`,
`src/services/device/test/integration/{agent_test.go,lab_fixtures_test.go}`,
`deploy/lab/agent.textproto`
After: none
Change: `git mv spec/proto/flowseer/store/edge spec/proto/flowseer/store/agent`
moves the schema and its README; the `package` line reads
`flowseer.store.agent.v1`, and `buf generate` writes
`generated/go/proto/flowseer/store/agent/v1` while `clean: true` in
`buf.gen.yaml` removes the old directory. `importOrder`'s `"store/edge": nil`
row becomes `"store/agent": nil` and its comment names the agent's own
deployment file. The three Go importers drop the `storeedgev1` alias and
import `agentv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/agent/v1"`:
buf derives `agentv1`, and nothing at those sites is already called that, so
no alias is needed. `field_constraint_class_test.go`'s blank import names the
new path. The first comment line of `deploy/lab/agent.textproto` reads
`# flowseer.store.agent.v1.AgentConfig for the lab run.`
READMEs: the opening sentence of `store/agent/v1/README.md` names
`flowseer.store.agent.v1`, and the rest of the file, including "What it does
not carry, and why", is unchanged; `store/README.md`'s `## Packages` entry
becomes `- agent/v1/: Device access agent deployment configuration.` and
moves above the `device/v1/` line, because that list is alphabetical as the
lists in `model/`, `api/`, and `edge/` are. Both
`## Boundaries` lines of the moved README stay `nothing FlowSeer-owned` and
`nothing`, and `store/README.md`'s own boundary lines do not move, because
the package imports nothing, nothing imports it, and the move stays inside
the `store/` root.
The new import path sorts before `store/device/v1`, so every Go file this
unit touches goes through `gofumpt -w` and
`goimports -local go.aledante.io/FlowSeer -w` before the verifier runs;
`docs/solutions/conventions/a-package-rename-moves-every-importers-sort-key.md`
records why the local prefix is not optional.
Tests: `TestImportOrderCoversEveryPackage` passes with the renamed row and
reports `store/agent` without it; `TestEveryDeclaredProtoPackageIsLinked`
passes with the blank import at the new path and names
`flowseer/store/agent/v1/agent_config.proto` when it is dropped;
`TestProtoReadmeCoverage` reports nothing, since `store/agent/` holds only
`v1/`; `TestProtoReadmeImports` passes for `store/agent/v1/README.md` and
`store/README.md`; `TestTheLabFixturesParse` in
`src/services/device/test/integration` parses `agent.textproto` as
`agentv1.AgentConfig`; `go test ./src/edge/agent/...` covers `LoadConfig`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto test/conformance/proto src/edge/agent src/services/device/test/integration deploy/lab generated`

### U2. The process-local bus root becomes runtime

Files: `spec/proto/flowseer/service/` moved to
`spec/proto/flowseer/runtime/` (`README.md`, `v1/bus.proto`,
`v1/message.proto`, `v1/README.md`), `spec/proto/flowseer/README.md`,
`generated/go/proto/flowseer/**`, `test/conformance/proto/layering_test.go`,
`test/conformance/proto/service_bus_rules_test.go` renamed
`runtime_bus_rules_test.go`,
`test/conformance/proto/service_message_rules_test.go` renamed
`runtime_message_rules_test.go`,
`src/common/service/{bus_test.go,delivery.go,delivery_test.go,manifest.go,manifest_test.go,matrix_test.go,message.go,message_compat_test.go,message_test.go,module_test.go,telemetry.go,telemetry_schema_test.go}`,
`src/common/service/test/integration/durability_test.go`,
`docs/solutions/architecture-patterns/trace-context-relays-through-trace-disabled-modules.md`
After: U1
Change: `git mv spec/proto/flowseer/service spec/proto/flowseer/runtime`
moves the root with its two READMEs; both schemas declare
`package flowseer.runtime.v1`, and `bus.proto` imports
`flowseer/runtime/v1/message.proto`. In `layering_test.go`, `unorderedRoots`
becomes `{"flowseer/runtime"}` and its comment names that path; the
synthetic file in `TestUnorderedRootsImportNothingFlowSeerOwned` becomes
`flowseer/runtime/v1/x.proto` importing `flowseer/runtime/v1/message.proto`
and `flowseer/model/edge/v1/edge.proto`, with its `want` line following; the
`TestLayeringViolationRules` case "operator api imports the bus contract"
imports `runtime`.
The fifteen Go importers rename the alias `servicev1` to `runtimev1` and
take the new path. Six string literals change with it:
`src/common/service/manifest.go:149` and the mirrored line in the bus rules
test set `EnvelopeType` to `"flowseer.runtime.v1.Message"`, the bus rules
test's four wire-contract cases name `flowseer.runtime.v1.RuntimeManifest`,
`.ReconciliationRecord`, `.Settlement`, and `.StoreProvenance`, and the two
`FullName` assertions in `message_compat_test.go` and the message rules test
compare against `flowseer.runtime.v1.Message`. Nothing else spelled
`flowseer.service.` changes: the span, metric, and attribute names in
`telemetry.go` and the tests that pin them are OpenTelemetry names.
`src/common/service/testdata/message_v1.bin` is not regenerated and
`-update-message-v1` is not run. The envelope carries its payload's type
name, not its own, so the fixture's bytes name `google.protobuf.Timestamp`
and no FlowSeer package; only the `FullName` comparison beside it moves.
The two conformance files are renamed with `git mv`, and their four
`TestService*` functions become `TestRuntimeMessageValidation`,
`TestRuntimeMessageWireContract`, `TestRuntimeBusControlRecordValidation`,
and `TestRuntimeBusControlRecordWireContracts`.
`TestRuntimeManifestRequiresCapacityAndDeduplicationFields` already reads
right and keeps its name.
READMEs: `runtime/README.md` states the root in the present tense — the
`runtime/` root holds process-local runtime messages, durable mailbox
envelopes, and broker reconciliation records; `runtime/v1` passes admission
because `Message` is the local mailbox envelope; Connect RPC services fail
it and belong in `api/` or `edge/` — and drops the sentence calling
`runtime/` a reserved name. Its `## Packages` line and its two boundary
lines are unchanged. `runtime/v1/README.md`'s opening sentence names
`flowseer.runtime.v1`. In `spec/proto/flowseer/README.md`, the ninth line of
the tree block becomes
`  runtime/       Process-local bus contracts and durable mailboxes`, in
place, and the closing sentence of "Import order between roots" names
`runtime/`; no root is reordered, because neither document's order is
alphabetical and moving one would be churn no gate can check.
The two Go signatures quoted in the trace-context solution take
`runtimev1.Message` and `runtimev1.MessageKind`, so the file's claim that
all code it cites is at the current tree stays true.
Reformat as in U1: fifteen files take a new import path, `runtime` sorts
before `service`, and only `golangci-lint` sees it.
Tests: `TestUnorderedRootsImportNothingFlowSeerOwned` passes and its
synthetic case still produces exactly one violation naming
`flowseer/model/edge/v1/edge.proto`;
`TestOrderedRootsCoverEveryTopLevelTree` reports `flowseer/runtime` when
`unorderedRoots` is left unchanged; `TestLayeringViolationRules` passes with
the renamed case; the four renamed conformance functions and
`TestRuntimeManifestRequiresCapacityAndDeduplicationFields` pass;
`TestMessageV1Compatibility` passes against the unchanged fixture and fails
on the `FullName` comparison if that line is missed;
`TestProtoReadmeCoverage` reports nothing and `TestProtoReadmeImports`
passes for `runtime/README.md` and `runtime/v1/README.md`;
`go test -race ./src/common/service/...` covers the manifest, delivery, and
telemetry paths that carry the renamed type.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto test/conformance/proto src/common/service docs/solutions generated`

### U3. The streaming-frame record stops calling the assertion unbound

Files: `docs/architecture/2026-09-09-streaming-frame-transport-direction.md`
After: none
Change: the paragraph closing "A long stream re-proves the caller" states
what is true: the assertion binds the Connect procedure and a SHA-256 of the
request body, checked at steps 5 and 6 of the verifier order in
`spec/proto/flowseer/model/edge/v1/README.md`. It then states the question
that creates, which this record does not settle: a re-assertion frame
arrives mid-stream, where there is no second HTTP body to hash and the
procedure is the one the stream opened with, so what those two fields mean
on a re-assertion is open. The paragraph keeps its existing requirement that
the re-assertion frame be verified under whatever rule the opening assertion
is verified under, so the two cannot drift apart.
The Sources bullet that reads "Streams are authorized once, and method
binding is open" becomes one that cites
`spec/proto/flowseer/model/edge/v1/README.md` for the procedure check at
step 5, the body check at step 6, and the sentence "Streams are checked when
they open".
The 2026-09-17 amendment already in this record lists three pieces of
language that moved to `model/edge`; the "does not bind the RPC method"
quotation is struck from that list, because it never lived there and the
file says the opposite.
A new amendment, `### 2026-09-18 — the assertion already bound the method
and the body`, records the correction: the record quoted
`api/edge/v1/README.md` as it stood on the branch it was written on, the
binding and the README correction had landed on 2026-09-05 in `3d06f2a9`,
and the claim was therefore false on the merged tree from the day this
record landed rather than made false by the tree moves. It quotes the struck
wording so the history is legible, and names what stays open.
Tests: none. No gate reads `docs/architecture/`, and the plan states that
rather than naming a test that would not see it. The claim the unit asserts
is checkable in `spec/proto/flowseer/model/edge/v1/assertion.proto:47,57`,
where `procedure` and `body_sha256` are both required, and in
`test/conformance/proto/model_edge_rules_test.go`, which rejects an empty
procedure, a procedure without a leading slash, and a 31-byte body hash, and
fails when the README stops carrying the worked header computed from them.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/architecture/2026-09-09-streaming-frame-transport-direction.md`

### U4. The records name the finished tree

Files: `docs/architecture/2026-08-20-network-model-structure-direction.md`,
`docs/architecture/2026-09-05-verified-device-access-direction.md`,
`docs/architecture/2026-09-09-streaming-frame-transport-direction.md`
After: U1, U2, U3
Change: in the structure record, the package tree's `service/v1/` line
becomes `runtime/v1/` and its `store/edge/v1/` line becomes
`store/agent/v1/`, both without the parenthetical naming a pending rename.
The paragraph below the tree drops "`runtime/` and `store/agent` do not
exist yet; they are pending renames of today's `service/v1` and today's
`store/edge`" and keeps, with the new name, the warning that
`flowseer.runtime.v1` names the process-local service runtime contract and
must not be treated as a ConnectRPC API package by inference. The import
graph is untouched: neither moved package appears in a row, because neither
imports nor is imported by anything. The 2026-09-04 amendment's sentence
"The service runtime later claimed `flowseer.service.v1`" gains
"(now `flowseer.runtime.v1`)", so the one place the old name survives in
that record points at the new one. The 2026-09-17 "the tree is cut by kind
of contract" amendment ends by saying the tree above marks what still sits
where until each move lands; that clause becomes a pointer to the two
amendments that record the moves, because nothing is marked any more.
A third amendment, `### 2026-09-18 — the process-private roots take their
names`, records that `service/v1` is `runtime/v1` and `store/edge` is
`store/agent`, that the rename changes the persisted `envelope_type` and the
`Message` full name and no migration is written, and that `runtime/` remains
the one root outside the import order.
The verified device access record's 2026-09-17 amendment gains one line:
where decision 12 says `flowseer.service.v1` stays private to the
process-local bus, that package now reads `flowseer.runtime.v1`. Decision
12's own text is not edited, which is how phases 1 and 2 left the four other
records they touched.
The streaming-frame record's 2026-09-17 amendment gains one line: the bulk
`bytes` field this record cites in its opening paragraph and its Sources
list now lives in `spec/proto/flowseer/runtime/v1/message.proto`.
Tests: none, for the reason U3 gives. The tree claim is checked by reading
the record's block against `ls spec/proto/flowseer` and
`ls spec/proto/flowseer/store`, and the import-graph claim by reading each
row against `importOrder` in `test/conformance/proto/layering_test.go`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/architecture`

Waves: U1 U3 | U2 | U4

U1 and U2 both rewrite `test/conformance/proto/layering_test.go` and both
regenerate `generated/go/proto/flowseer/`, so they cannot run together. U3
touches one record and no code, so it runs beside U1. U4 waits on all three:
it states what the tree is, and it edits the same record U3 does.

## Verification

```bash
buf format -d --exit-code && buf lint
buf generate && git status --porcelain generated/   # empty after commit
go build ./... && go vet ./...
go test -race ./test/conformance/... ./src/...
.claude/skills/verify-change/scripts/verify-change.sh --full
```

`buf generate` and the tests under `src/common/service` open sockets and
write outside the repository, so both want an unsandboxed run;
`golangci-lint` must be the one on `PATH` that `.golangci.yml` matches.

The tree:

```bash
ls spec/proto/flowseer            # README.md api edge errs event integration model net runtime store
ls spec/proto/flowseer/store      # README.md agent device
```

The parent's residue grep, run from the repository root:

```bash
grep -rn 'flowseer/device/\|flowseer\.device\.\(access\|policy\|credential\)\.v1\|flowseer/service/\|flowseer\.service\.v1\|flowseer/integration/device\|flowseer\.integration\.\|flowseer/event/device\|flowseer\.event\.device\|flowseer/store/edge\|flowseer\.store\.edge' docs src test spec deploy
```

Outside `docs/plans/`, it must print exactly these and nothing else:

- `docs/architecture/2026-08-20-network-model-structure-direction.md`, the
  2026-09-04 amendment's historical sentence, which U4 makes point forward.
- `docs/architecture/2026-09-05-verified-device-access-direction.md:127`,
  decision 12's body, which its amendment corrects.
- `docs/architecture/2026-09-09-streaming-frame-transport-direction.md`,
  the opening paragraph and the Sources bullet that cite
  `spec/proto/flowseer/service/v1/message.proto`, which its amendment
  corrects. U3 rewrites part of this record, so do not pin these by line
  number.
- `docs/architecture/2026-09-09-mutation-shadow-projection-direction.md:103`
  and `docs/architecture/2026-09-10-virtual-device-direction.md:670`, the
  same shape from phase 1 and already amended.
- `docs/solutions/conventions/a-package-rename-moves-every-importers-sort-key.md:24`,
  which quotes `flowseer/integration/device/v1` as the before half of a
  worked example.
- `docs/runbooks/lab-icx7150-first-write.md:44`, which is the filesystem
  path `/var/lib/flowseer/device/tls.crt` and not a package at all.

The README prose no gate reads:

```bash
grep -rn 'pending rename' spec/proto/flowseer --include=README.md   # nothing
grep -rn 'does not bind' docs/architecture/                          # only the U3 amendment
```

## Definition of done

- [x] The verifier is green for every changed path, and `--full` is green
      once at the end of the phase.
- [x] `ls spec/proto/flowseer` prints the nine roots and `README.md`, and
      `ls spec/proto/flowseer/store` prints `README.md agent device`.
- [x] `TestProtoReadmeCoverage` and `TestProtoReadmeImports` pass, and the
      five READMEs U1 and U2 touch describe the finished tree.
- [x] The structure record's tree matches `ls`, every row of its import
      graph is within `importOrder`, and it marks nothing as pending.
- [x] The residue grep prints only the lines Verification names.
- [x] No architecture record says the edge assertion leaves the RPC method
      or the request body unbound.
- [x] This plan's `status` is `implemented` with an outcome note under its
      title, the parent's `U3. Landed:` line carries the commit range, and
      no plan label appears in code, comments, or commit messages.

## Open questions

- What a mid-stream re-assertion frame's `procedure` and `body_sha256` bind
  to. The opening call hashes its HTTP request body; a re-assertion frame
  sent on an open stream has no second body, and its procedure is the one
  the stream opened with, so binding both to the opening call's values makes
  the frame replayable within the stream and binding them to the frame needs
  a rule this repository does not have. U3 states the question in the record
  and answers none of it. It is due before the first streaming method lands,
  not before this phase does.
- Whether `Deliberately absent:` in a package README should point at the
  file-level comment the triad hook reads instead of restating it. The
  parent left this to phase 3's README pass; this phase creates no new
  `Deliberately absent:` line and moves the two it inherits unchanged, so
  the question is still open and is now a question about the README shape in
  `docs/conventions/protobuf.md` rather than about this tree.
