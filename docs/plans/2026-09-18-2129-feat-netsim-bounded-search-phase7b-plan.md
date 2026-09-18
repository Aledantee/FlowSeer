---
title: Network Simulation Analysis Completeness, Phase 7b - Bounded Search and Conformance - Plan
type: feat
date: 2026-09-18
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
parent: docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
---

# Network simulation analysis completeness, phase 7b: bounded search and conformance - Plan

> Re-plan against phase 7a's landed comparison API before implementation: the
> search domains call `fabric.Compare`/`vswitch.Compare` and consume their
> `Disposition` and `Difference`, so the exact signatures and the difference
> shape 7a lands decide this phase's enumeration and minimization units.

## Goal

A caller searches a declared finite domain of candidate scenarios and gets
either a replayable, minimized causal counterexample or an honest statement of
what was searched and what was not. The means: a new `src/common/netsim/search`
package with finite domain interfaces, deterministic enumeration under a
resource contract, coverage-and-remainder accounting, deterministic
minimization, and first-divergence trace alignment over phase 7a's comparison;
plus cross-package conformance fixtures that pin the whole netsim result,
ordering, trace, clone, derive, run, and comparison contract.

This phase claims parent requirements R35-R40.

## Decisions

- **Search lives in `src/common/netsim/search`, importing `analysis`,
  `vswitch`, and `fabric`; none of them imports `search`.** Why: the parent
  fixes this dependency direction so comparison and result payloads stay in
  their owning packages and cross-domain enumeration is layered above them.
  Search does not consume `netmodel` or generated messages.
- **A domain is an explicit finite enumerable with declared limits.** L2 traffic
  and timed-fault domains are concrete; L3 is an interface with no concrete
  enumeration this phase. A search reports coverage (tuples tested) and remainder
  (tuples not tested) exactly, and budget exhaustion names both.
- **A found difference is minimized deterministically within the declared
  domain, and the mismatch is replayed after every reduction.** Reduction never
  leaves the domain; the result reports non-minimal status when the minimization
  limit is exhausted. Traces align at the first causal divergence.
- **Only a selected difference retains full artifacts.** Discarded candidates
  keep coverage and summary accounting but not full traces (parent R39/R40's
  resource contract).

## Requirements

Parent R35-R40, each to be given a concrete acceptance example against 7a's
landed comparison API during the re-plan. In outline:

1. **R35.** Search requires a finite domain and reports coverage and remainder;
   budget exhaustion names tested and untested tuples.
2. **R36.** A mismatch returns an immutable replay specification, aligned causal
   traces, and minimality status; the first differing rule is identified after
   deterministic minimization, or the limit is reported exhausted.
3. **R37.** Cross-package conformance fixtures enforce result, ordering, trace,
   clone, derive, run, and comparison invariants; random map insertion order
   does not change a canonical result.
4. **R38.** All comparison and search run in process under `go test` with no
   external simulator, device, daemon, or backend.
5. **R39/R40.** The planning-corpus case runs within the search resource
   contract: discarded candidates retain coverage and summary but not full
   traces; a difference retains its complete replay and diagnostic artifacts.

## Out of scope

- Symbolic verification, SAT/SMT, an unbounded packet domain, production traffic
  generation, and packet forwarding.
- External simulator adapters, distributed search, stored baselines, service
  APIs, dashboards, and report databases.
- A concrete L3 traffic domain (interface only this phase).

## Units

To be cut during the re-plan against 7a's landed API. The expected shape,
carried from the parent's U7 sketch:

- Finite differential domains and coverage (L2 traffic, timed fault, L3
  interface; deterministic enumeration, limits, coverage accounting, immutable
  replay specifications).
- Deterministic minimization and first-divergence trace alignment.
- Cross-package conformance fixtures under
  `src/common/netsim/internal/netsimtest/` consumed by package tests.
- Comparison-and-search documentation in the package READMEs and the parent
  architecture record.

## Open questions

- The exact domain interface shape (how a domain declares its finite enumerable,
  limits, and coverage), which depends on the `Difference` shape 7a lands.
- The minimality order for a counterexample and how reduction stays in-domain.
- Whether the conformance corpus is one package or a fixture library consumed by
  each package's own test binary (Go builds one test binary per package, so a
  cross-package invariant cannot be a single registration —
  [a gate selected by name stops running silently](../solutions/conventions/a-gate-selected-by-name-stops-running-silently.md)
  applies).
- The search resource contract's concrete bounds (what "retains summary but not
  full traces" costs and caps).
