---
title: Network Simulation Analysis Completeness, Phase 3 - Plan
type: feat
date: 2026-09-12
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
parent: docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
---

# Network simulation analysis completeness, phase 3: STP, LAG, and multicast correctness - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

Close supported layer-2 protocol gaps that can reverse a planning answer while
keeping the simulator bounded and standards-oriented. This phase claims parent
requirements R14-R18. It consumes R9, R37, and R39 through protocol field-matrix,
seam, and corpus tests. Phase 5 later defines retention dependencies for the new
state.

## Decisions

- Build on phase 1 result and trace contracts and phase 2 operational-link
  semantics.
- Represent explicit spanning-tree instances and VLAN bindings, with one common
  instance as the default. Do not emulate vendor-specific PVST behavior.
- Consume caller-supplied normalized STP instance and VLAN bindings. Do not
  simulate MSTP region digest or negotiation; incompatible or unknown cross-node
  bindings return scoped unsupported or incomplete issues.
- Treat LAG membership, operational links, selection inputs, timers, and seed as
  dependencies of aggregation state.
- Represent multicast membership as `(*,G)` or `(S,G)` and use the logical clock
  for query and expiry behavior.
- Until phase 6 adds scenarios, repeatability means applying the same explicit
  logical-time event sequence to separately constructed fabrics.

## Requirements

1. **R14:** STP forwarding state is keyed by instance and VLAN binding.
   **Acceptance example:** two VLAN bindings can use different forwarding trees.
2. **R15:** STP applies message age and configured edge and guard behavior.
   **Acceptance example:** an expired superior BPDU cannot hold stale root state.
3. **R16:** LAG state exposes complete dependencies and deterministic selection.
   **Acceptance example:** a member fault invalidates only the affected LAG.
4. **R17:** Multicast forwarding honors source-specific membership.
   **Acceptance example:** `(S1,G)` does not admit `S2`.
5. **R18:** Non-fast leave uses last-member query and timer transitions.
   **Acceptance example:** membership persists until its deterministic query
   window completes.
6. **R9/R37:** Each added protocol field extends validation, normalization,
   cloning, and `Diff`, then phase 5 owns its dependency key and retention policy.
   Deterministic seam coverage remains mandatory.
   **Acceptance example:** a VLAN-to-STP-instance binding change appears in
   `Diff` and preserves deterministic result ordering.
7. **R39:** Each protocol addition names its operator question, current false
   answer, minimum semantics, unsupported boundary, and golden case. **Acceptance
   example:** source-specific multicast lands only with a case showing the current
   group-only model admits the wrong source.

## Out of scope

- Vendor STP dialects, full MSTP region negotiation, LACP packet compatibility,
  multicast routing, IGMP/MLD proxying, and snooping-querier election beyond the
  explicit planning cases.
- Wall-clock goroutines, background daemons, or nondeterministic timers.

## Units

### U1: Re-plan protocol state machines and limits

- **Files:** `src/common/netsim/vswitch/stp/`,
  `src/common/netsim/vswitch/lag/`, `src/common/netsim/vswitch/mcast/`,
  related switch and fabric paths
- **After:** parent U1, U2
- **Change:** Map phase 1 contracts to exact state machines, timers, dependencies,
  trace rules, invalid inputs, and unsupported boundaries. Complete the
  capability-value matrix before retaining each proposed behavior.
- **Tests:** State-transition tables and failure scenarios for every new branch.
- **Verify:** Re-plan before editing.

### U2: Implement STP instances and guards

- **Files:** `src/common/netsim/vswitch/stp/`
- **After:** U1
- **Change:** Add instance/VLAN binding, message-age handling, stale-state expiry,
  edge behavior, BPDU, root, and loop guards, and the phase 1 field-matrix
  contract. Keep composer migration for U5.
- **Tests:** Multi-instance forwarding, superior/stale BPDU, guard, timer, and
  link-fault traces.
- **Verify:** Focused STP tests.

### U3: Implement dependency-complete LAG behavior

- **Files:** `src/common/netsim/vswitch/lag/`
- **After:** U1
- **Change:** Include all selection and operational dependencies, expose
  convergence evidence, extend the phase 1 field matrix, and stabilize member
  choice across identical explicit event sequences. Keep composer migration for
  U5.
- **Tests:** Dependency mutation matrix, member fault/recovery, seed, ordering,
  and selection trace cases.
- **Verify:** Focused LAG tests.

### U4: Implement source-filtered multicast and leave timing

- **Files:** `src/common/netsim/vswitch/mcast/`
- **After:** U1
- **Change:** Add source-aware membership and deterministic last-member query,
  response, and expiry transitions. Extend the phase 1 field matrix and keep
  composer migration for U5.
- **Tests:** `(*,G)`, `(S,G)`, source rejection, fast leave, non-fast leave,
  timer boundaries, and identical-event-sequence stability.
- **Verify:** Focused multicast tests.

### U5: Integrate protocols and document supported fidelity

- **Files:** protocol integration under `src/common/netsim/vswitch/` and
  `src/common/netsim/fabric/`, cross-protocol tests, affected package READMEs,
  and the parent architecture record
- **After:** U2, U3, U4
- **Change:** Integrate the three capability packages in the switch and fabric
  composers. Add cross-protocol status, trace, ordering, fault, and dependency
  tests. Document supported scenarios, assumptions, and unsupported depth.
- **Tests:** Cross-protocol logical-time sequences on separate fabrics, partial
  input propagation, link fault/recovery, state isolation, and documentation
  examples where practical.
- **Verify:** Netsim race tests, vet, diff-aware verifier.

## Verification

The re-plan must make every state machine and unsupported branch explicit. Run:

```bash
go test -race ./src/common/netsim/...
go vet ./src/common/netsim/...
.claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>
```

## Definition of done

- [ ] Parent requirements R14-R18 pass their acceptance examples.
- [ ] All protocol transitions use logical time and deterministic ordering.
- [ ] Dependency changes invalidate only affected protocol state.
- [ ] Semantic traces identify the rule and evidence for every transition.
- [ ] Unsupported vendor or protocol depth is explicit rather than approximated.
