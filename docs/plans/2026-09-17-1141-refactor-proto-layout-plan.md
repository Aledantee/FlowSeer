---
title: Protobuf Tree by Kind of Contract - Plan
type: refactor
date: 2026-09-17
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
amends: docs/architecture/2026-08-20-network-model-structure-direction.md
---

# Protobuf Tree by Kind of Contract - Plan

## Goal

Every package under `spec/proto/flowseer/` sits under a root that names what
kind of contract it is, every directory in the tree carries a README that
states that exact package's identity and boundaries, and two gates hold the
shape: a package that declares a Connect service is imported by nothing, and
a README's `Imports:` line matches the package's real imports. The means: move
the existing packages into the roots `net`, `errs`, `model`, `event`, `api`,
`edge`, `integration`, `store`, and `runtime` in three phases, amend the
network model structure record in the first, and write the two gates before
the second phase depends on them.

This plan is wrong if a message turns out to be needed by both a northbound
service and an edge-plane service and cannot live in `model/`: the sink rule
would then force a duplicate, and the roots should be re-cut before any of
them is created.

## Decisions

- **One axis at the root: the kind of contract.** Why: the current roots each
  answer a different question (`net` asks "is it a ref-free value", `api`
  asks "does an operator call it", `device` asks "is it shared vocabulary",
  `store` asks "is it one process's file"), so no rule places a new package.
  The kind of contract is the axis the layering test already enforces and the
  first thing a reader needs to know before importing.
- **The roots and what each admits.** `net/`: ref-free network values with no
  tenant, lifecycle, or observation time, unchanged. `errs/`: the error wire
  payload, unchanged. `model/`: everything that carries or names identity:
  entities with their refs, triads, and lifecycle enums, the device-access
  operation vocabulary, and the policy and credential leaves. `event/`:
  durable stream records that are not an entity's own transition. `api/`:
  northbound Connect services an operator, the web app, or a workflow calls.
  `edge/`: Connect services between central and an enrolled edge process, in
  either direction. `integration/`: the integration fabric contract the
  device service record names (announce, kind descriptor, event subjects),
  reserved and holding only a README until the bus plan lands. `store/`:
  files one process writes or reads at start, imported by nothing.
  `runtime/`: the process-local bus contract, outside the import order.
- **A package that declares a service is a sink, and no `model/` package
  declares one.** Why: today `api/edge` is imported by four packages for the
  Edge ref and the assertion, never for its services, and the layering table
  has to explain that in comments. Once request and response messages live
  with their service and shared values live in `model/`, "may I import this"
  is answered by the root alone. `test/conformance/proto/layering_test.go`
  enforces both halves.
- **Request and response messages live with their service; a message two
  services carry lives in `model/`, and so does every `<Entity>Record`.**
  Why: `SignedEdgeAssertion` rides on the upload stream in `api/capture` as
  well as on every edge call, and `CapturePacketChunk` rides on
  `UploadCapture` and `TailCaptureSession` (its file comment says so), so
  both are model; `ListedDevice` rides on one RPC and stays with it.
  `EdgeRecord` and `CaptureSessionRecord` pair a triad's Config and State
  and hold nothing a service adds, and `store/device` embeds the first, so
  the pair lives beside its triad in every case rather than by whether a
  store happens to embed it.
- **`DeviceOperationEvent` goes to `event/access`, not `model/access`.** Why:
  the protobuf conventions place a triad's `<Entity>Event` beside its triad
  because it is that entity's transition. The audit record is not a
  transition of any entity; it is a lane-level log row with nine kinds and
  its own delivery guarantee, and the record that decided it (decision 13 of
  the verified device access record) separates it from state. The
  `AuditService` that delivers it is a call an edge makes to central, so it
  sits in `edge/audit`; `event/` holds records, never services.
- **`edge/` names the plane, `model/edge` names the entity.** Why:
  `CONCEPTS.md` defines an Edge as the enrolled process, so "the contract
  between central and an Edge" is the same word used correctly, and the Go
  package names differ (`edgev1` for the entity, `attachv1`, `dispatchv1`,
  `auditv1`, `capturev1` for the plane). `agent/` was considered and
  rejected because the agent is one program that runs at an edge, and the
  plane outlives it.
- **`edge/attach` is the name for today's `EdgeService`.** Why: its RPCs
  (Enroll, Rekey, Heartbeat, AttachBus, ListDevices, AcquireReadCredential,
  OpenDeviceSubmission) are all what an edge calls on its own behalf to get
  and keep standing with central; "enrollment" names only the first.
- **`store/edge` becomes `store/agent`; `service` becomes `runtime`.** Why:
  the file belongs to the program in `src/edge/agent`, and "edge" is the
  entity everywhere else in the tree. `service` means "Connect service" in
  every other package name, and the local bus contract is the one root the
  import order excludes, so its name must not suggest a boundary.
- **Every directory under `spec/proto/flowseer/` that is not a bare
  version holder carries a README, in one shape.** A directory whose only
  children are version directories (`net/addr/`, holding `v1/`) needs none;
  every other directory does. The root map `flowseer/README.md` lists the
  roots and the order between them. A root or intermediate README
  (`model/README.md`, `net/protocol/README.md`) has the sections
  `## Identity` (what kind of contract lives here, in one paragraph),
  `## Admission` (the test a new package passes to live here, with one
  package that passes and one that does not), `## Boundaries` (which roots
  it may import and which may import it), and `## Packages` (one line per
  child). A versioned package README opens with the full package name and a
  one-paragraph identity, then a `## Boundaries` section with the lines
  `Imports:` (the FlowSeer packages its files import, or `nothing
  FlowSeer-owned`), `Imported by:` (the packages whose files import it, or
  `nothing`), and `Deliberately absent:` (what a reader would expect and
  will not find, with the reason). A package's own files importing each
  other count on neither line. Existing README content follows those
  sections unchanged. Why the fixed shape: a reader deciding where a
  message goes compares packages, and prose that varies in structure per
  package makes that a reading exercise. Why `Imports:` and `Imported by:`
  are both gated: they are the claims a README makes that the tree can
  contradict silently, and both are computable from it.
- **Go package-name collisions across roots are accepted and aliased.** Why:
  buf managed mode derives the Go package name from the last two path
  segments, so `api/capture`, `edge/capture`, `model/capture`, and
  `net/capture` all yield `capturev1`. Overriding `go_package` per package
  would put path knowledge into `buf.gen.yaml` for every package. The move
  reduces today's four-way `devicev1` collision to `api/device` and
  `store/device`; the rest is one alias per import site, in the landed
  form `<root><leaf>v1` for the package that is not the file's main one
  (`apicapturev1` beside `capturev1` in `src/modules/capture/engine.go`).
- **The Connect route names change, and that is a wire break.** A route is
  `/<package>.<Service>/<Method>`, and the edge assertion signs it as its
  audience (`src/edge/agent/internal/identity/assertion.go`), so phase 2
  changes what every edge signs and central verifies. Nothing external
  consumes it, and `AGENTS.md` says to state such a break as a fact rather
  than shim it; no compatibility route is kept.
- **Moves use `git mv`, and every phase leaves `buf lint`, `buf generate`,
  and `go test -race ./...` green.** Why: a directory rename that git records
  as delete-and-add loses the blame history that the schema comments cite,
  and the layering allowlist is rewritten in each unit so a half-moved tree
  is never committed.
- **The accepted network model structure record is amended in phase 1.** Why:
  the record fixes the package tree and the import graph, and `plan` requires
  code and record to change together. The device service, verified device
  access, and remote capture records name the old paths in prose and get a
  pointer amendment each rather than an edit in place.
- **Three phases, by dependency cluster.** `model/` first, because every
  other root imports it; the edge plane, `api/`, and `event/` second, because
  their imports point at `model/`; the process-private roots and the final
  README pass third. The parent plan holds the sequence.

## Requirements

1. After the last phase, `spec/proto/flowseer/` contains exactly the roots
   `net`, `errs`, `model`, `event`, `api`, `edge`, `integration`, `store`,
   `runtime`, each listed in `orderedRoots` except `runtime`
   (`integration` is listed while holding no schema, which the walk
   tolerates). Example:
   `ls spec/proto/flowseer` prints those nine names and `README.md`.
2. No FlowSeer package imports a package that declares a `service`, and no
   package under `model/` declares one. Example: a synthetic
   `model/inventory` file importing `api/edge/v1/edge_admin_service.proto`
   fails `TestImportOrder` with a message naming the sink rule; a synthetic
   `model/access/v1/x.proto` containing `service X {}` fails
   `TestModelDeclaresNoService`.
3. Every directory under `spec/proto/flowseer/` holds a `README.md` unless
   its only children are version directories. Example: creating
   `spec/proto/flowseer/net/wlan/v1/radio.proto` without a README fails
   `TestProtoReadmeCoverage` naming `flowseer/net/wlan/v1` and not
   `flowseer/net/wlan`; creating `flowseer/net/wlan/v1/README.md` makes it
   pass.
4. A versioned package README's `Imports:` and `Imported by:` lines name
   exactly the FlowSeer packages the tree shows, excluding the package
   itself. Example: `model/access/v1/README.md` listing `Imports: model/edge,
   model/inventory, model/policy, net/interface` while a file imports
   `net/addr` fails `TestProtoReadmeImports` naming the missing package; the
   same test fails when `Imported by:` omits `api/device`.
5. Every existing message, enum, and service keeps its simple name; only the
   package changes. Example: `flowseer.device.access.v1.MutationIntent`
   becomes `flowseer.model.access.v1.MutationIntent`, and
   `grep -rn 'message MutationIntent' spec/proto` finds one file.
6. The network model structure record's package tree describes the tree
   after phase 1 and names the two later phases as reserved paths; its
   import graph states the imports that exist, each within `importOrder`,
   and keeps the record's sentence that the allowlist is wider where a
   package may grow. Example: the record's tree lists `model/` with its six
   packages, and every row of its graph is a subset of the matching
   `importOrder` entry.

## Out of scope

- Any change to a message's fields, validation rules, or field encoding.
  Full names and Connect routes do change; the Decisions state it.
- The `ruckus/` module.
- New packages the direction records reserve (`net/wlan`, the Interface
  entity, discovery and ingestion records, the integration fabric contract).
  `integration/` gets a README and nothing else.
- Overriding Go package names in `buf.gen.yaml`.
- The frontend, which does not consume the schemas yet.

## Units

### U1. Phase 1: the model root, the record amendment, and the two gates

Files: docs/plans/2026-09-17-1141-refactor-proto-layout-phase1-plan.md
After: none
Landed: `aaa269e3..a688f322`

### U2. Phase 2: the edge plane, the northbound api, and the event root

Files: docs/plans/2026-09-17-1141-refactor-proto-layout-phase2-plan.md
After: U1
Landed:

### U3. Phase 3: the process-private roots and the root README pass

Files: docs/plans/2026-09-17-1141-refactor-proto-layout-phase3-plan.md
After: U2
Landed:

Waves: U1 | U2 | U3

## Verification

```bash
buf lint
buf generate && git status --porcelain generated/   # empty after commit
go build ./... && go vet ./...
go test -race ./test/conformance/... ./src/... 
.claude/skills/verify-change/scripts/verify-change.sh --full
```

After phase 3, `TestProtoReadmeCoverage` passes, and
`grep -rn 'flowseer/device/\|flowseer\.device\.\(access\|policy\|credential\)\.v1\|flowseer/service/\|flowseer\.service\.v1\|flowseer/integration/device\|flowseer\.integration\.\|flowseer/event/device\|flowseer\.event\.device\|flowseer/store/edge\|flowseer\.store\.edge' docs src test spec deploy`
finds only the plans and the record amendments that describe the move.
`flowseer.device.*` and `flowseer.service.*` without a version segment are
OpenTelemetry attribute names under `docs/conventions/observability.md`
and do not change.

## Definition of done

- [ ] Each phase plan reads `implemented` and its `Landed:` line above
      carries the commit range.
- [ ] Verifier green with `--full` after each phase.
- [ ] Every directory under `spec/proto/flowseer/` has a README in the shape
      the Decisions fix, and the two gates pass.
- [ ] The network model structure record's tree matches `ls`, and every
      row of its import graph is within `importOrder`.
- [ ] `docs/conventions/protobuf.md`, the two `docs/solutions/` entries
      whose `module:` names a moved package, the runbook, the lab
      `deploy/` files, and the `.repro` fixtures point at the new paths.
- [ ] This plan's `status` is `implemented` with an outcome note under the
      title, and no plan label appears in code or commit messages.

## Open questions

- Whether `edge/capture` should exist as its own package or `UploadCapture`
  should join `edge/attach`. The parent's decision keeps one package per
  service concern; the phase 2 re-plan confirms it against the capture
  record's open question about how a capture command reaches an edge, which
  may add a second RPC to the same package.
- Whether `Deliberately absent:` in a package README duplicates the
  file-level comment the triad hook reads for a missing family member. The
  README line is broader (it covers anything a reader expects), and the
  hook's comment stays where the hook looks; phase 3's README pass decides
  whether the README line should point at the file comment instead of
  restating it.
