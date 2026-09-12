---
title: Network Simulation Analysis Completeness, Phase 5 - Plan
type: feat
date: 2026-09-12
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
parent: docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
---

# Network simulation analysis completeness, phase 5: State ownership, derivation, and fork isolation - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

Make expected-state derivation and current/candidate analysis repeatable. State
must survive only when its owner and dependencies still apply. Snapshots remain
observational values, while executable forks share no mutable state with a source.

This phase claims parent requirements R23-R26 and R40. It consumes R37 through
derivation, fingerprint, fork, and resource conformance tests.

## Decisions

- Apply the repository's Validate, normalize/construct, Derive, then Diff
  lifecycle consistently across netsim packages.
- Record three independent axes: owning package or layer, origin, and lifetime.
  Do not infer one axis from another or from a zero value.
- Derive retention keys include every normalized input that can affect the
  retained state, including base identity, ports, link operation, protocol
  bindings, timers, and static or observed construction inputs. Phase 6 extends
  the inventory for scenarios and seeds.
- Prefer explicit dependency fingerprints and invalidation reports over ad hoc
  equality checks against one local config struct.
- Keep `Snapshot` as a public immutable view. Add `Fork` for an executable deep
  copy of mutable maps, slices, queues, timers, journeys, traces, and protocol
  state.
- Share immutable normalized construction inputs and replay data across forks;
  deep-copy only mutable execution state. The re-plan must declare representative
  topology, event, and fork sizes plus allocation and runtime budgets before code
  changes.

## Requirements

1. **R23:** Owning package or layer, origin, and lifetime are explicit and
   inspectable. **Acceptance example:** a configured static FDB entry and an
   observed learned entry have distinct origin, lifetime, dependencies, and
   retention rules even though the bridge owns both.
2. **R24:** Derivation retains runtime state only when its full normalized
   dependency fingerprint matches. **Acceptance example:** a base MAC change
   invalidates dependent STP identity even with unchanged STP config.
3. **R25:** Static state is reconstructed from or matched against the target
   construction specification. **Acceptance example:** an unrelated port edit
   does not erase a static FDB entry, but omitting its target seed removes it.
4. **R26:** Snapshot is observational and Fork deeply isolates every mutable
   executable field. **Acceptance example:** advancing candidate timers cannot
   change the current snapshot or source fabric's next result.
5. **R37:** Conformance tests enforce idempotent derive, deterministic
   fingerprints, clone isolation, and globally unique state keys. **Acceptance
   example:** two consecutive derives with identical normalized input are
   semantically identical.
6. **R40:** Forking has a representative scale and resource contract.
   **Acceptance example:** benchmarks at declared topology, event, and fork sizes
   keep immutable inputs shared and enforce allocation and runtime budgets.

## Out of scope

- Persistent snapshots, serialization formats, databases, distributed locking,
  and backend lifecycle management.
- Scenario execution and comparison APIs assigned to phases 6 and 7.
- Retaining state whose dependencies cannot be proven equal.

## Units

### U1: Re-plan the ownership and dependency model

- **Files:** state and derive implementations across `src/common/netsim/`
- **After:** parent U2, U3, U4
- **Change:** Inventory every state field by owner, origin, and lifetime;
  enumerate complete dependencies; choose canonical fingerprints; and specify
  reconstruction, retention, or invalidation behavior. Declare the representative
  scale envelope and measurable allocation and runtime budgets.
- **Tests:** Three-axis inventory, dependency mutation matrix, target seed
  absence, and collision cases.
- **Verify:** Re-plan before editing.

### U2: Implement ownership-aware derivation

- **Files:** derive and state code under `src/common/netsim/vswitch/`,
  `src/common/netsim/fabric/`, and protocol packages
- **After:** U1
- **Change:** Replace local-config equality shortcuts with normalized dependency
  fingerprints, reconstruct or match static state against target construction
  inputs, localize invalidation, and return an evidence-backed report.
- **Tests:** One mutation per dependency, unrelated-state retention, static FDB
  retention and removal, idempotence, deterministic map order, and global key
  collisions.
- **Verify:** Focused derive tests across affected packages.

### U3: Separate observational snapshots from executable forks

- **Files:** clone, snapshot, and fork code under `src/common/netsim/vswitch/`,
  `src/common/netsim/fabric/`, and protocol packages
- **After:** U1, U2
- **Change:** Preserve Snapshot as an immutable public view and deep-copy every
  mutable executable state class in Fork. Add mutation probes for queues, timers,
  traces, journeys, maps, and slices. Phase 6 extends Fork for scenario and seed
  state.
- **Tests:** Bidirectional mutation isolation, repeatable next-event behavior,
  source/candidate order independence, immutable-input sharing, declared-scale
  allocation/runtime benchmarks, and race tests.
- **Verify:** Focused snapshot tests and package race tests.

### U4: Document derivation and snapshot invariants

- **Files:** affected package READMEs and the parent architecture record
- **After:** U2, U3
- **Change:** Document ownership, retention, invalidation, identity, snapshot,
  and fork behavior with working current/candidate examples.
- **Tests:** Compile documentation examples where practical.
- **Verify:** Netsim race tests, vet, diff-aware verifier.

## Verification

The re-plan must contain a field-level ownership and dependency inventory. Run:

```bash
go test -race ./src/common/netsim/...
go vet ./src/common/netsim/...
.claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>
```

## Definition of done

- [ ] Parent requirements R23-R26 and R40 pass their acceptance examples.
- [ ] Every retained field has explicit owner, origin, lifetime, and dependency
      keys.
- [ ] Static state survives unrelated changes; invalid state cannot leak forward.
- [ ] Snapshots are observational; forks share no mutable state with their source.
- [ ] Derivation and clone conformance tests are deterministic and race-clean.
