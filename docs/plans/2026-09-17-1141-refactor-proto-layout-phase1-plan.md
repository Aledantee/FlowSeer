---
title: Protobuf Tree Phase 1 - The Model Root, the Record, and the Gates - Plan
type: refactor
date: 2026-09-17
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
parent: docs/plans/2026-09-17-1141-refactor-proto-layout-plan.md
amends: docs/architecture/2026-08-20-network-model-structure-direction.md
---

# Protobuf Tree Phase 1 - The Model Root, the Record, and the Gates - Plan

## Goal

The identity-bearing packages live under `spec/proto/flowseer/model/`: the
policy and credential leaves, the Edge entity split out of `api/edge`, the
inventory, the CaptureSession entity and chunk frames split out of
`api/capture`, and the device-access operation vocabulary. The five service
packages that remain (`api/edge`, `api/device`, `api/capture`,
`integration/device`, `event/device`) are imported by nothing, and the
layering test says so. Every directory in the tree that is not a bare
version holder has a README in the parent's shape, a gate checks that, and
the network model structure record describes the result. The means: three
`git mv` units with the allowlist rewritten in each, then the README pass,
the gates, and the record amendment against the finished table.

This plan is wrong if a service package still needs to be imported after the
entity halves move out: that would mean a request or response message is
really a model value, and it moves too before the sink rule lands.

## Decisions

The parent's Decisions hold. Specific to this phase:

- **`api/edge` splits by file.** `edge.proto`, `assertion.proto`,
  `key_proof.proto`, `provisioning.proto`, and `EdgeRecord` moved out of
  `edge_admin_service.proto` go to `model/edge/v1`, which then imports
  nothing FlowSeer-owned (verified: `edge.proto` imports only
  `google/protobuf/timestamp.proto`; `assertion.proto` imports `edge.proto`
  in the same package). `edge_service.proto`, `edge_admin_service.proto`,
  `bus.proto`, `device.proto`, and `credential.proto` stay in `api/edge/v1`
  until phase 2. Why: `store/device` imports `EdgeRecord` and the ref,
  `api/capture` imports the assertion, and nothing outside `api/edge`
  imports the other five files.
- **`api/capture` splits by file.** `capture_session.proto`,
  `capture_chunk.proto`, and `CaptureSessionRecord` moved out of
  `capture_service.proto` go to `model/capture/v1`;
  `capture_service.proto` and `capture_edge_service.proto` stay. Why: the
  chunk file's own comment says both services share it, and the parent
  puts every `<Entity>Record` beside its triad.
- **Go aliases during this phase.** `model/edge/v1` keeps `edgev1`; a Go
  file that also imports `api/edge/v1` aliases it `apiedgev1`.
  `model/capture/v1` is `modelcapturev1` wherever `net/capture`'s
  `capturev1` or `api/capture`'s `apicapturev1` is also imported. Why: the
  landed form is `<root><leaf>v1` for the package that is not the file's
  main one, and a fixed name keeps phase 2's rename a search-and-replace.

Ruled: `model/inventory/v1/README.md`'s `Deliberately absent:` line in U1
names only the three package-wide absences already stated in the body prose
(Config messages for the discovered-only families, `EntityRef` admission for
the four entities that have not joined `EntityType`, and a `Tenant` entity
with an id surface) rather than restating every per-family "no
`XConfig`" aside. Why: the README coverage and `Imports:`/`Imported by:`
gates land in a later unit, so U1 only has to leave the section accurate,
not exhaustive, and duplicating each family's own absence note would drift
from the prose that already carries it. Cost if wrong: the later README
pass rewrites or expands the line; no test depends on its contents yet.

Ruled: `model/edge/v1/README.md`, created in U2, opens with the full package
name, a one-paragraph identity, the four moved sections, and a `## Boundaries`
section (`Imports:`, `Imported by:`, `Deliberately absent:`), matching the
shape U1 already gave the three package READMEs it created rather than
waiting for U5's pass. Why: the parent's README-shape decision holds for
every unit in this phase, and U1 already established the precedent of adding
`## Boundaries` to a new package README ahead of the gate. Cost if wrong:
U5's pass rewrites the section; no test depends on its contents until then.

## Requirements

1. `spec/proto/flowseer/model/` holds `policy`, `credential`, `edge`,
   `inventory`, `capture`, and `access`, each under `v1/`, and
   `spec/proto/flowseer/device/` no longer exists. Example: `buf lint`
   passes and `ls spec/proto/flowseer/model` prints the six names and
   `README.md`.
2. `importOrder` lists no importer for the five service packages, and
   `TestImportOrder` rejects an import of any package that declares a
   service. Example: `{importer: "model/inventory", imported: "api/edge"}`
   expects a violation whose reason contains "declares a service".
3. `TestModelDeclaresNoService` fails on any `service` declaration under
   `model/`. Example: a synthetic `service Probe {}` under
   `model/access/v1` is reported by path.
4. Every directory under `spec/proto/flowseer/` that is not a bare version
   holder has a README, and every versioned package README's `Imports:`
   and `Imported by:` lines match the tree. Example: deleting
   `model/README.md` fails `TestProtoReadmeCoverage` naming
   `flowseer/model`; `net/addr/` has no README and passes.
5. The structure record's tree names `model/` with its six packages and
   the five service packages phase 2 moves, and every row of its import
   graph is within `importOrder`. Example: the record's row for
   `model/access` names `model/edge, model/inventory, model/policy,
   net/interface`, and `importOrder["model/access"]` contains all four.
6. Every Go package compiles and tests as before. Example: `go test -race
   ./...` passes and `git diff -- src test ':!*_test.go' ':!*.repro'`
   shows only import paths, aliases, and telemetry-free comment paths.

## Out of scope

- Moving any service, `bus.proto`, `device.proto`, `credential.proto`, or
  `DeviceOperationEvent`. Phase 2.
- `store/edge`, `service/v1`. Phase 3.
- README prose beyond the identity paragraph and the `## Boundaries`
  section each package gains, and the root and intermediate READMEs.
- OpenTelemetry attribute names `flowseer.device.*` and
  `flowseer.service.*`, which are not package names.

## Units

### U1. Move the leaves and the inventory into model

Files: spec/proto/flowseer/device/policy/v1/*, spec/proto/flowseer/device/credential/v1/*,
spec/proto/flowseer/api/inventory/v1/*, every `.proto` importing them,
generated/go/proto/flowseer/**, test/conformance/proto/layering_test.go,
test/conformance/proto/{device_policy,device_credential,api_inventory,api_inventory_event,api_inventory_topology,api_attribute}_rules_test.go,
test/conformance/proto/{field_constraint_class,api_edge_bus_credential,store_device}_test.go,
every Go file importing the three generated packages,
src/services/device/test/integration/testdata/mutation-verification-repro/{e2e_test.go.repro,fixture_test.go.repro},
docs/solutions/conventions/document-intentional-schema-deviations-with-comment-and-test.md,
deploy/lab/registry.textproto,
src/modules/localnet/access/internal/capability/interfaces/doc.go
After: none
Change: `git mv` moves `device/policy` to `model/policy`,
`device/credential` to `model/credential`, and `api/inventory` to
`model/inventory`. Each file's `package` line and every
`import "flowseer/…"` line across the tree that named one of the three
names the new path; `buf format` runs after. `buf generate` rewrites
`generated/go/proto/flowseer/` (`clean: true` removes the old
directories). Go imports change path only; `policyv1`, `credentialv1`, and
`inventoryv1` are unchanged. The six rules tests are renamed with the
`model_` prefix in place of `device_` and `api_`;
`field_constraint_class_test.go`'s two exempt keys under
`flowseer.api.inventory.v1` read `flowseer.model.inventory.v1`;
`api_edge_bus_credential_rules_test.go` and `store_device_rules_test.go`
change import paths. `importOrder` renames the three keys and every entry
naming them; `orderedRoots` adds `flowseer/model` and keeps
`flowseer/device` until U3 empties it. The solution entry's `module:` and
cited paths, the registry comment naming `CredentialMaterial`, and the
`localnet` capability doc comment naming `Provenance` read the new full
names; `src/modules/localnet/README.md` and the runbook cite neither
package by path and need no edit. The three package READMEs open with
their new full name and gain `## Boundaries`.
Tests: the renamed rules tests pass unchanged in body;
`TestSharedFieldNamesCarryTheSameConstraints` passes with no unmatched
exemption; `TestImportOrder`, `TestImportOrderCoversEveryPackage`, and
`TestOrderedRootsCoverEveryTopLevelTree` pass; `TestLayeringViolationRules`
cases naming the old keys are renamed.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto test/conformance/proto src docs/solutions deploy docs/runbooks`

### U2. Split the Edge entity out of api/edge

Files: spec/proto/flowseer/api/edge/v1/{edge,assertion,key_proof,provisioning,edge_admin_service}.proto,
spec/proto/flowseer/api/edge/v1/README.md,
spec/proto/flowseer/model/edge/v1/*,
spec/proto/flowseer/model/inventory/v1/{integration,provenance}.proto,
spec/proto/flowseer/model/inventory/v1/README.md,
spec/proto/flowseer/api/capture/v1/*.proto, spec/proto/flowseer/api/capture/v1/README.md,
spec/proto/flowseer/device/access/v1/operation.proto,
spec/proto/flowseer/store/device/v1/{edge_record,registry,service_config}.proto,
generated/go/proto/flowseer/**, test/conformance/proto/layering_test.go,
test/conformance/proto/{api_edge,api_edge_bus_credential,store_device}_rules_test.go,
every Go file importing `api/edge/v1`,
src/services/device/test/integration/testdata/mutation-verification-repro/*,
docs/solutions/conventions/sign-protobuf-payload-bytes-never-fields.md,
deploy/lab/provisioning.textproto, deploy/lab/write-provisioning.sh
After: U1
Change: `git mv` moves the four entity files to `model/edge/v1/`;
`EdgeRecord` moves from `edge_admin_service.proto` into
`model/edge/v1/edge.proto` beside the triad it pairs, its comment saying
the admin service returns it and the device service stores it. Imports in
`api/edge`, `model/inventory`, `api/capture`, `device/access`, and
`store/device` follow. Go files importing both packages alias the service
package `apiedgev1`; files importing only the entity keep `edgev1`; the
`.repro` fixtures change the same way. `api_edge_rules_test.go` splits: the
cases over refs, lifecycle, `SetupKey`, the assertion, the key proof, and
provisioning move to `model_edge_rules_test.go`; request and response
cases stay. `importOrder` gains `"model/edge": nil`; `api/edge` becomes
`{model/edge, model/policy, model/credential, net/addr}`; `api/capture`,
`model/inventory`, `device/access`, and `store/device` swap `api/edge` for
`model/edge`. `model/edge/v1/README.md` takes the "Two secrets, two
lifetimes", "The assertion header", "Lifecycle and contact", and "What
central holds and what an attacker gets" sections from the `api/edge`
README, which keeps the rest and gains a first paragraph saying the entity
moved, and a `## Boundaries` section (imports nothing FlowSeer-owned; imported
by `api/capture, api/edge, device/access, model/inventory, store/device`).
The solution entry, the provisioning file's first-line comment, and the
script's comment read `flowseer.model.edge.v1`. `api/capture/v1/README.md`'s
link to the assertion-header section and `model/inventory/v1/README.md`'s
`Imports:` line follow the entity to `model/edge`.
Tests: `model_edge_rules_test.go` and the reduced `api_edge_rules_test.go`
pass; `TestLayeringViolationRules` gains `{importer: "store/device",
imported: "api/edge"}` expecting a violation and `{importer: "store/device",
imported: "model/edge"}` expecting none; the device service integration
test passes.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto test/conformance/proto src docs/solutions deploy`

### U3. Move device/access and split the CaptureSession entity

Files: spec/proto/flowseer/device/access/v1/*, spec/proto/flowseer/model/access/v1/*,
spec/proto/flowseer/api/capture/v1/{capture_session,capture_chunk,capture_service}.proto,
spec/proto/flowseer/model/capture/v1/*, spec/proto/flowseer/api/device/v1/*.proto,
spec/proto/flowseer/integration/device/v1/*.proto,
spec/proto/flowseer/event/device/v1/*.proto,
spec/proto/flowseer/store/device/v1/*.proto, generated/go/proto/flowseer/**,
test/conformance/proto/layering_test.go,
test/conformance/proto/{device_access,api_device,event_device,integration_device,store_device}_rules_test.go,
every Go file importing `device/access/v1` or `api/capture/v1`,
src/services/device/test/integration/testdata/mutation-verification-repro/*
After: U2
Change: `git mv` moves `device/access` to `model/access`,
`capture_session.proto` and `capture_chunk.proto` to `model/capture/v1/`,
and `CaptureSessionRecord` from `capture_service.proto` into
`capture_session.proto`, leaving `spec/proto/flowseer/device/` empty and
removed. Imports in `api/device`, `integration/device`, `event/device`,
`store/device`, and `api/capture` follow; `accessv1` is unchanged, and Go
files importing `model/capture` beside another capture package alias it
`modelcapturev1`. `device_access_rules_test.go` is renamed
`model_access_rules_test.go`; the other four change import paths.
`importOrder` renames the key, adds `model/capture` with
`{model/edge, net/capture}`, sets `api/capture` to
`{model/capture, model/edge, net/capture}`, and drops `flowseer/device`
from `orderedRoots`. The `api/capture` README's "The owning edge" section
moves to `model/capture/v1/README.md`.
Tests: the renamed rules test passes; `TestLayeringViolationRules` cases
naming `device/access` read `model/access`;
`TestOrderedRootsCoverEveryTopLevelTree` passes with `device` gone.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto test/conformance/proto src`

### U4. The sink rule

Files: test/conformance/proto/layering_test.go
After: U3
Change: `layeringViolation` rejects an import of any package that declares
a `service`, with the reason "<pkg> declares a service and is imported by
nothing"; the set of service-declaring packages is scanned from the tree.
`TestModelDeclaresNoService` walks `flowseer/model/` and fails on any
`service` declaration. The comments that explained `api/edge` being
imported are deleted; the table's comment states the rule in two
sentences.
Tests: `TestLayeringViolationRules` gains `{importer: "store/device",
imported: "api/device"}` and `{importer: "model/access", imported:
"integration/device"}` expecting violations naming the sink rule;
`TestModelDeclaresNoService` has a synthetic case with `service Probe {}`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- test/conformance/proto`

### U5. The README shape and its gates

Files: spec/proto/flowseer/README.md, spec/proto/flowseer/{net,net/protocol,model,api,errs,integration,event,store,service}/README.md,
every `spec/proto/flowseer/**/v1/README.md`, test/conformance/proto/layout_test.go
After: U3
Change: `spec/proto/flowseer/README.md` becomes the root map: one
paragraph on the axis, a tree with one line per root, the order between
roots, and a pointer to the structure record; its "Network packages" and
"Standards grounding" sections move to `net/README.md` and "Entity and
runtime packages" is dropped, because each package README states its own
identity. Each root and intermediate directory gets the README the
parent's Decisions fix; `integration/README.md` says the root is reserved
for the fabric contract and names the device service record's "Transport"
section; `service/README.md` says the root is renamed `runtime` in phase 3.
Every versioned package README opens with its full package name and one
identity paragraph and has `## Boundaries` with `Imports:`,
`Imported by:`, and `Deliberately absent:`; an existing "deliberately
absent" section's content moves under that line. `layout_test.go` gains
`TestProtoReadmeCoverage` (a README in every directory under `flowseer/`
whose children are not all version directories) and
`TestProtoReadmeImports` (for every versioned package, the packages on
`Imports:` equal those its files import outside itself, and those on
`Imported by:` equal the packages whose files import it; `nothing
FlowSeer-owned` and `nothing` mean the empty set), each with a synthetic
tree case for the failing shape.
Tests: `TestProtoReadmeCoverage` on a temporary tree with `x/v1/a.proto`
and no README reports `x/v1` and not `x`; `TestProtoReadmeImports` on a
README naming one package too many on `Imports:` and one too few on
`Imported by:` reports both.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto test/conformance/proto`

### U6. Amend the direction records and the conventions doc

Files: docs/architecture/2026-08-20-network-model-structure-direction.md,
docs/architecture/{2026-08-20-device-service-and-inventory,2026-09-05-verified-device-access,2026-09-09-remote-packet-capture,2026-09-09-streaming-frame-transport,2026-09-09-mutation-shadow-projection,2026-09-10-virtual-device}-direction.md,
docs/conventions/protobuf.md, docs/architecture/README.md
After: U4
Change: The structure record's "The package tree" section shows the tree
the parent fixes, with `edge/`, `integration/`, `runtime/`, and
`store/agent` marked as landing with phases 2 and 3, and its import-order
block lists the imports that exist after U3, each within the landed
`importOrder`, keeping the sentence that the allowlist is wider. A dated
amendment, "2026-09-17 — the tree is cut by kind of contract", states the
parent's root decisions, the sink rule, and the README shape, and names
this plan. Each of the six other records gains a one-paragraph dated
amendment saying the paths it cites under `api/inventory`, `api/edge`,
`device/`, `api/capture`'s entity files, `integration/device`, and
`event/device` now read under `model/`, `edge/`, and `event/access`, with
a pointer to the structure record. `docs/conventions/protobuf.md` replaces
every `api/inventory/v1`, `api/edge/v1`, and `device/policy/v1` citation
with the `model/` path, and its "Refs live beside the triad" paragraph adds
that a ref never lives in a service package. The architecture README's
"Read when" cell for the structure record adds "or a README under
`spec/proto`".
Tests: none; prose. The verifier's documentation checks run on the paths.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/architecture docs/conventions/protobuf.md`

Waves: U1 | U2 | U3 | U4 U5 | U6

## Verification

```bash
buf lint && buf generate && git status --porcelain generated/
go build ./... && go vet ./...
go test -race ./test/conformance/... ./src/...
.claude/skills/verify-change/scripts/verify-change.sh --full
grep -rn 'flowseer/device/\|flowseer\.device\.\(access\|policy\|credential\)\.v1\|flowseer/api/inventory\|flowseer\.api\.inventory\.' spec src test deploy docs/conventions docs/solutions docs/runbooks
```

The last command prints nothing.

## Definition of done

- [ ] Verifier green with `--full`.
- [ ] Every README under `spec/proto/flowseer/` has the sections the parent
      fixes, and both README gates pass.
- [ ] The structure record's tree matches `ls`; every graph row is within
      `importOrder`; the six pointer amendments are in place.
- [ ] This plan's `status` is `implemented` with an outcome note under the
      title, and the parent's U1 `Landed:` line carries the commit range.
- [ ] No plan label in code, comments, or commit messages.

## Open questions

- Whether `EdgeRecord` and `CaptureSessionRecord` keep their names in
  `model/`. The conventions doc has no name for the Config-and-State pair;
  the implementer keeps the names unless the doc gains one in the same
  change.
