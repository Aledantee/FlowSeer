---
title: Network Simulation Analysis Completeness, Phase 7 - Plan
type: feat
date: 2026-09-12
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: superseded
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
parent: docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
superseded_by: docs/plans/2026-09-18-2129-feat-netsim-exact-comparison-phase7a-plan.md
---

# Network simulation analysis completeness, phase 7: Exact comparison and bounded counterexamples - Plan

> Superseded 2026-09-18. Re-planned against the landed tree and split into
> phase 7a (exact comparison and dispositions,
> `2026-09-18-2129-feat-netsim-exact-comparison-phase7a-plan.md`, implementation-ready)
> and phase 7b (bounded search and conformance,
> `2026-09-18-2129-feat-netsim-bounded-search-phase7b-plan.md`, to re-plan
> against 7a's landed API). The parent's U7 line is now U7a and U7b.

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

Make current/candidate comparison strong enough to justify a planning decision.
Exact equivalence includes all normalized observables and completion state;
bounded search either returns a replayable causal counterexample or states what
it did not search.

This phase claims parent requirements R32-R38 and consumes R39-R40 by completing
the planning corpus workflow and enforcing the search resource contract.

## Decisions

- The public contract has one exact behavioral equivalence definition and three
  dispositions: `Equivalent`, `Different`, and `Inconclusive`.
- Compare independent forks and leave caller-owned switch and fabric values
  untouched.
- Treat full frame content, selected members, mirrors, PCP, path, timing,
  multiplicity, journey result state, and externally relevant final state as
  behavioral observables. Construction metadata, evidence, and semantic trace
  remain diagnostics.
- `Equivalent` requires complete results. Proven behavioral mismatch is
  `Different`. Incomplete, exhausted, unstable, or unsupported evaluation is
  `Inconclusive`, and only `Different` produces a counterexample.
- Search accepts an explicit finite domain and limits. Minimize a found example
  deterministically and align traces at the first causal divergence.
- Switch and fabric comparison remain in their owning packages. Cross-domain
  enumeration lives in `src/common/netsim/search`, which imports `analysis`,
  `vswitch`, and `fabric`; none of them imports `search`. Search does not consume
  `netmodel` or generated messages.

## Requirements

1. **R32:** Switch comparison covers the complete normalized observable result.
   **Acceptance example:** a destination-MAC rewrite differs even when egress
   ports match; an added no-op diagnostic step does not.
2. **R33:** Fabric comparison covers path, time, multiplicity, drop location,
   journey terminal, final state, status, and issues without consuming inputs.
   **Acceptance example:** active periodic protocols can be compared repeatedly.
3. **R34:** Comparison returns `Equivalent`, `Different`, or `Inconclusive`.
   **Acceptance example:** equal exhausted results are inconclusive, while a
   proven MAC rewrite is different.
4. **R35:** Search requires a finite domain and reports coverage and remainder.
   **Acceptance example:** budget exhaustion names tested and untested tuples.
5. **R36:** A mismatch returns an immutable replay specification, aligned causal
   traces, and minimality status. **Acceptance example:** the first differing VLAN
   rule is identified after deterministic minimization, or the result reports
   that the minimization limit was exhausted.
6. **R37:** Cross-package conformance fixtures enforce result, ordering, trace,
   clone, derive, run, and comparison invariants. **Acceptance example:** random
   map insertion order does not change a canonical result.
7. **R38:** All comparison and search execute in process with no external
   simulator, live device, daemon, or backend. **Acceptance example:** the full
   conformance suite runs under `go test`.
8. **R39/R40:** The planning corpus case runs within the declared search resource
   contract. **Acceptance example:** discarded candidates retain coverage and
   summary accounting but not full traces; a difference retains its complete
   replay and diagnostic artifacts.

## Out of scope

- Symbolic verification, SAT/SMT integration, an unbounded packet domain,
  production traffic generation, and packet forwarding.
- External simulator adapters, distributed search workers, stored baselines,
  service APIs, dashboards, and report databases.
- Claiming equivalence outside the caller-supplied finite domain.

## Units

### U1: Re-plan observable and disposition contracts

- **Files:** comparison code under `src/common/netsim/vswitch/` and
  `src/common/netsim/fabric/`, semantic result types, related READMEs
- **After:** parent U6
- **Change:** Inventory every observable field, define canonical forms, exact
  behavioral equality, diagnostic-only fields, all three dispositions, and
  non-consuming fork lifecycle against the landed APIs.
- **Tests:** Observable-field matrix and disposition truth table.
- **Verify:** Re-plan before editing.

### U2: Implement exact switch and fabric comparison

- **Files:** `src/common/netsim/vswitch/compare.go`,
  `src/common/netsim/fabric/compare.go`, related result and test files
- **After:** U1
- **Change:** Replace shallow field checks and set-like drop comparison with full
  ordered normalized results over independent forks. Compare frames, mirrors,
  member choice, paths, timing, multiplicity, journey states, and externally
  relevant final state. Use status and issues to select `Inconclusive`; align
  evidence and traces diagnostically.
- **Tests:** One mismatch per behavioral observable, no-op trace difference,
  equivalent complete case, inconclusive incomplete case, active-protocol
  repeatability, and source immutability.
- **Verify:** Focused switch and fabric comparison tests.

### U3: Implement finite differential domains and coverage

- **Files:** new search types and implementation under
  `src/common/netsim/search/` and their tests
- **After:** U2
- **Change:** Add explicit L2 traffic and timed-fault domains, an explicit L3
  domain interface, deterministic enumeration, limits, coverage accounting, and
  immutable replay specifications. Keep comparison and result payloads in their
  owning packages. Retain full artifacts only for selected differences; discard
  candidate traces after summary and coverage accounting.
- **Tests:** Complete search, exhausted search, incomplete branch, unsupported
  domain, empty and duplicate members, cardinality overflow, zero/exact/one-short
  limits, stable enumeration, replay, and exact unsearched remainder.
- **Verify:** Focused search and integration tests.

### U6: Minimize and align counterexamples

- **Files:** minimization and alignment files under `src/common/netsim/search/`
  and their tests
- **After:** U3
- **Change:** Define a stable minimality order, reduce only within the declared
  domain, replay the mismatch after every reduction, align semantic traces at the
  first causal divergence, and report non-minimal status when the minimization
  limit is exhausted.
- **Tests:** Stable minimality order, still-in-domain reductions, mismatch replay,
  first-divergence alignment, exact-limit success, one-short exhaustion, and
  non-minimal result status.
- **Verify:** Focused search minimization and integration tests.

### U4: Add cross-package conformance fixtures

- **Files:** reusable test helpers and fixtures under
  `src/common/netsim/internal/netsimtest/`, package tests that consume them
- **After:** U2, U3, U6
- **Change:** Exercise deterministic ordering, status propagation, issue scope,
  trace rule stability, derive invalidation, clone isolation, scenario replay,
  run lifecycle, exact behavioral comparison, diagnostic trace equality, and
  search accounting across package seams.
- **Tests:** Conformance matrix plus regression fixtures for every omission found
  during the completeness review.
- **Verify:** Full netsim race tests and repeated deterministic test runs.

### U5: Document comparison guarantees and limits

- **Files:** affected package READMEs, parent architecture record, prior-art
  cross-reference
- **After:** U2, U3, U6, U4
- **Change:** Add working current/candidate and counterexample examples, define
  all three comparison dispositions, behavioral versus diagnostic equality, and
  finite-domain and external-runtime boundaries.
- **Tests:** Compile documentation examples where practical.
- **Verify:** Netsim race tests, vet, diff-aware verifier.

## Verification

The re-plan must include a behavioral/diagnostic field matrix, disposition truth
table, and explicit domain, limit, and resource rules before implementation. Run:

```bash
go test -race ./src/common/netsim/...
go vet ./src/common/netsim/...
.claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>
```

Repeat deterministic conformance tests with randomized input insertion order and
both current-first and candidate-first execution.

## Definition of done

- [ ] Parent requirements R32-R38 pass their acceptance examples.
- [ ] Exact comparison includes every normalized behavioral observable and uses
      completion evidence to distinguish all three dispositions.
- [ ] Trace and evidence differences remain diagnostic and cannot manufacture a
      behavioral difference.
- [ ] Comparison is repeatable and does not mutate inputs.
- [ ] Search reports its finite domain, limits, coverage, and remainder.
- [ ] Counterexamples replay, minimize deterministically, and identify the first
      causal divergence.
- [ ] All conformance tests run in process with no external simulator or service.
