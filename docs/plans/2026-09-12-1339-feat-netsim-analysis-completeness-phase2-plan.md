---
title: Network Simulation Analysis Completeness, Phase 2 - Plan
type: feat
date: 2026-09-12
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
parent: docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
---

# Network simulation analysis completeness, phase 2: Physical and topology uncertainty - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

Make physical feasibility and endpoint reachability honest enough for planning.
Known absence, known failure, assumptions, and unresolved observations must
produce different results without contaminating unrelated paths.

This phase claims parent requirements R8 and R10-R13. It consumes R2, R7, R9,
and R39 through physical, topology, endpoint, and corpus conformance cases.

## Decisions

- Use the phase 1 status, issue, evidence, and semantic trace contracts.
- Keep electrical and optical feasibility in `phy`, topology intent and graph
  adjacency in `fabric`, and endpoint acceptance in the endpoint-owning package.
  `netmodel` may translate caller facts but does not own topology.
- A standards default may be executed only when it is recorded as an
  assumption. Unknown capabilities do not receive a silent optimistic default.
- Physical arrival and endpoint acceptance are separate trace operations and
  terminal outcomes.
- Phase 2 owns the host-acceptance result used by fabric delivery accounting.
  Rejected arrivals do not enter `Deliveries`; phase 6 later maps the result to a
  journey terminal.

## Requirements

1. **R10:** Media reach returns known-in-range, known-exceeded, or unknown with
   evidence. **Acceptance example:** an unknown medium cannot become a zero-range
   failure.
2. **R11:** Negotiation handles unknown capability sets and forced/auto duplex
   combinations explicitly. **Acceptance example:** unreported peers return an
   incomplete link state rather than 1 Gb/s full duplex.
3. **R12:** PoE models no PD, unknown PD requirements, denial, and delivery as
   distinct states. **Acceptance example:** an attached PD with unknown class
   cannot be treated as absent.
4. **R13:** Endpoint configuration defines accepted unicast, broadcast,
   multicast, VLAN, and IP traffic. **Acceptance example:** wrong-destination
   unicast is rejected after physical arrival.
5. **R8:** Fabric distinguishes a resolved cable, explicit uncabled state, and
   unresolved adjacency and applies the parent's effective-state precedence.
   **Acceptance example:** one unresolved transceiver affects only paths that
   require it while a deliberately uncabled path remains a definite drop.
6. **R2/R7:** Physical and topology uncertainty stays localized and carries scope
   and evidence through fabric construction. **Acceptance example:** conflicting
   device and topology evidence produces one scoped issue without erasing other
   links.
7. **R9:** Every added PHY, topology, and endpoint field extends validation,
   normalization, cloning, and `Diff`. **Acceptance example:** changing unknown
   PD demand or endpoint acceptance changes the field matrix.
8. **R39:** Add the partial-topology golden workflow and physical false-answer
   cases to the analysis corpus. **Acceptance example:** the corpus preserves a
   definite known path beside one unresolved link.

## Out of scope

- Optical power budgets, environmental modeling, wireless propagation, cable
  inventory ingestion, and live-device probing.
- A complete host networking stack or operating-system-specific filtering.
- Protocol convergence work assigned to phases 3, 4, and 6.

## Units

### U1: Re-plan the physical truth model

- **Files:** `src/common/netsim/vswitch/phy/`,
  `src/common/netsim/fabric/`, `src/common/netsim/vswitch/netmodel/`
- **After:** parent U1
- **Change:** Confirm phase 1 result shapes, then specify reach, negotiation,
  duplex, transceiver, PoE, known-absence, and unresolved-input truth tables.
- **Tests:** Exhaustive truth-table and localized-issue cases.
- **Verify:** Re-plan before editing.

### U2: Implement physical and endpoint semantics

- **Files:** `src/common/netsim/vswitch/phy/`,
  `src/common/netsim/vswitch/netmodel/`, `src/common/netsim/fabric/`,
  endpoint configuration and tests
- **After:** U1
- **Change:** Implement the reviewed truth tables, endpoint acceptance, semantic
  traces, delivery exclusion for rejection, phase 1 field-matrix obligations, and
  cross-layer status propagation. Add the topology-shadowing corpus case.
- **Tests:** Media, negotiation, PoE, topology, arrival, acceptance, and
  unaffected-path cases; prove rejected arrivals are absent from `Deliveries`.
- **Verify:** Focused tests, netsim race tests, vet, diff-aware verifier.

### U3: Document supported physical fidelity

- **Files:** affected package READMEs and the parent architecture record
- **After:** U2
- **Change:** Add working examples, evidence behavior, assumptions, and explicit
  physical and host-model limits.
- **Tests:** Documentation examples compile where practical.
- **Verify:** Diff-aware verifier on all changed paths.

## Verification

The re-planned phase must enumerate physical truth tables before code changes.
It must run focused tests for every table row, then:

```bash
go test -race ./src/common/netsim/...
go vet ./src/common/netsim/...
.claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>
```

## Definition of done

- [ ] Parent requirements R10-R13 pass their acceptance examples.
- [ ] Known absence, known failure, assumed state, and unknown state are distinct.
- [ ] Unknown physical facts never become optimistic link operation.
- [ ] Host delivery reflects endpoint acceptance rather than port arrival alone.
- [ ] Documentation states supported fidelity and explicit omissions.
