---
title: Protobuf Tree Phase 3 - The Process-Private Roots and the Final README Pass - Plan
type: refactor
date: 2026-09-17
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: mixed
parent: docs/plans/2026-09-17-1141-refactor-proto-layout-plan.md
---

# Protobuf Tree Phase 3 - The Process-Private Roots and the Final README Pass - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

The tree holds exactly the nine roots the parent fixes: `store/edge` is
`store/agent`, `service/v1` is `runtime/v1`, and every README under
`spec/proto/flowseer/` describes the finished tree rather than a phase in
progress. The means: two `git mv` units, a pass over every README's
`Imported by:` line and the root map, and the structure record's closing
amendment.

## Decisions

The parent's Decisions hold. Specific to this phase:

- `runtime/v1` stays outside `orderedRoots` and `importOrder`, and the
  exception in `TestOrderedRootsCoverEveryTopLevelTree` is renamed from
  `service` to `runtime`. Why: the local bus contract is process-local by
  the service runtime's design, and the parent keeps it out of the boundary
  order for that reason.
- The Go package name for `runtime/v1` is `runtimev1`, and `src/common/service`
  is the only importer. Why: the rename is one directory's imports.
- The `store/agent` README keeps the "What it does not carry, and why"
  section and gains the `## Boundaries` section with `Imports: nothing
  FlowSeer-owned` and `Imported by: nothing`. `deploy/lab/agent.textproto`'s
  first-line comment reads `flowseer.store.agent.v1.AgentConfig`.

## Requirements

1. `ls spec/proto/flowseer` prints `README.md api edge errs event integration
   model net runtime store` and nothing else.
2. `test/conformance/proto/service_bus_rules_test.go` and
   `service_message_rules_test.go` are renamed `runtime_bus_rules_test.go`
   and `runtime_message_rules_test.go`; `store_device_rules_test.go` is
   unchanged; the `store/edge` cases, if any exist by then, read
   `store/agent`.
3. Every versioned package README's `Imported by:` line names the packages
   that import it in the finished tree. Example: `model/edge/v1/README.md`
   names `model/inventory, model/capture, model/access, api/edge,
   edge/attach, edge/capture, store/device`.
4. The structure record's tree carries no "lands with phase" marks, and
   `spec/proto/flowseer/README.md` lists the nine roots.

## Out of scope

- Any move other than the two named.
- The solutions entry
  `docs/solutions/architecture-patterns/local-bus-durability-is-a-runtime-setting-not-storage-identity.md`
  cites `flowseer.service.bus.*` telemetry attribute names, which are
  OpenTelemetry names under `docs/conventions/observability.md`, not
  protobuf packages, and stay as they are.
