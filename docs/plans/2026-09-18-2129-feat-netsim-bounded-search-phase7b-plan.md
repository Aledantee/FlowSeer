---
title: Network Simulation Analysis Completeness, Phase 7b - Bounded Search and Conformance - Plan
type: feat
date: 2026-09-19
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept after fixes
compound: docs/solutions/conventions/a-search-dimension-with-no-behavioral-effect-is-dishonest-coverage.md
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
parent: docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
---

# Network simulation analysis completeness, phase 7b: bounded search and conformance - Plan

## Goal

A caller searches a declared finite domain of candidate scenarios against a
current-versus-candidate pair and gets either a replayable, deterministically
minimized causal counterexample or an honest statement of what was searched and
what was not. The means: a new `src/common/netsim/search` package with a finite
`Domain` interface, deterministic enumeration under a resource contract,
coverage-and-remainder accounting, counterexample minimization over phase 7a's
`fabric.Compare`, and first-divergence trace alignment; plus cross-package
conformance fixtures that pin the whole netsim result, ordering, trace, clone,
derive, run, and comparison contract.

This phase claims parent requirements R35 through R40. It consumes phase 7a's
`fabric.Compare(a, b, scenario, budget) Comparison` and its `Disposition`,
`Difference`, and `[2]ReplaySpec` (`src/common/netsim/fabric/compare.go`).

Stop condition: if a candidate scenario cannot be replayed deterministically —
`fabric.Compare` on the same inputs twice does not give the same `Disposition`
and `Difference` — then minimization (which replays after every reduction) has no
fixed point and this plan is wrong. Phase 7a's comparison is deterministic
(sorted observable walk, provenance selection); this phase rests on that.

## Decisions

- **Search lives in `src/common/netsim/search`, importing `analysis`, `vswitch`,
  and `fabric`; none of them imports `search`.** Cross-domain enumeration layers
  above the comparison and result payloads, which stay in their owning packages.
  Search does not consume `netmodel` or generated messages.
- **A `Domain` is a deterministic finite enumerable with a declared size.**
  `type Domain interface { Size() int; Enumerate(yield func(Candidate) bool) }`,
  where `Candidate` names a scenario (an ordered `[]fabric.Injection` plus timed
  faults) and the tuple identity that coverage accounts for. Enumeration is a
  total order over the domain, independent of map iteration, so a resumed or
  budget-cut search reports an exact remainder. Why an internal-iterator `yield`
  rather than a slice: a domain can be large, and the search stops early on
  budget, so materializing every candidate is wrong; why a `Size()` alongside:
  coverage is `tested / Size()` and the remainder is the untested tuples, which
  R35 requires named.
- **Two concrete domains this phase, plus an L3 interface.** `L2TrafficDomain`
  (a cross product of declared source/destination/VLAN/frame-shape tuples) and
  `TimedFaultDomain` (declared cable faults at declared times) are concrete
  `Domain`s. `L3Domain` is the interface only, with no concrete enumeration, so a
  later phase adds routed traffic without reshaping the search. Why: parent R35
  and the parent's protocol-addition table scope L3 to a later increment.
- **`Search` returns coverage, a remainder, and at most the selected
  differences.** `func Search(current, candidate *fabric.Fabric, dom Domain, lim
  Limits) Result`. It enumerates `dom`, runs `fabric.Compare(current, candidate,
  cand.Scenario, lim.Budget)` per candidate, and records: for a `Different`, the
  candidate's tuple, its `[2]ReplaySpec`, and its `Difference`; for every
  candidate, coverage accounting only. `Limits` caps the candidates examined and
  the wall of retained differences. Why the asymmetry: parent R39/R40's resource
  contract — a discarded candidate keeps coverage and summary but not its full
  traces, and only a retained difference keeps its complete replay and diagnostic
  artifacts.
- **`current` and `candidate` are compared, never consumed.** `Search` passes
  the same two fabrics to every `Compare` call; `Compare` forks internally (phase
  7a), so neither fabric is stepped across the whole search. The candidate
  scenario varies per domain tuple; the fabrics do not.
- **Minimization is deterministic reduction within the domain, replayed every
  step.** Given a `Different` counterexample, `Minimize` removes scenario elements
  (injections, then faults) in a fixed order, keeping a removal only when
  `fabric.Compare` still reports the same `Difference.Observable`; it reports the
  reduced scenario and whether the minimization limit was hit (`Minimal` versus
  `LimitReached`). Why replay-checked reduction rather than a structural guess: a
  reduction that changes the observable is not a smaller counterexample for the
  same divergence; only a re-run proves it still diverges the same way.
- **Trace alignment names the first causal divergence.** `Align(cmp Comparison)`
  walks the current and expected journeys of the retained `Comparison` in the
  same paired order 7a established and returns the first entry index where their
  `trace` facts differ, as a diagnostic beside the `Difference`. Why a separate
  step: the `Difference` names *what* differs (the observable); alignment names
  *where* in the causal trace it first shows, which is what a troubleshooter
  reads. Trace text remains diagnostic and never changes the disposition.
- **The conformance corpus is a fixture library each package's test binary
  consumes, enumerated and counted.** The fixtures live in
  `src/common/netsim/internal/netsimtest`; each of `vswitch`, `fabric`, and
  `search` has a `conformance_test.go` that runs the fixtures its package owns,
  and a top-level enumeration test asserts every fixture group is claimed by some
  package. Why not one package: Go builds one test binary per package, so a
  single registration is invisible to another package's binary —
  [a gate selected by name stops running silently](../solutions/conventions/a-gate-selected-by-name-stops-running-silently.md)
  — so the set is enumerated and counted, not discovered by name.

## Requirements

1. **R35.** `Search` requires a finite `Domain` and reports coverage and an exact
   remainder. Acceptance: a search over a 12-tuple domain with a budget that
   examines 8 reports 8 tested tuples and the exact 4 untested tuples; a search
   that examines all 12 reports full coverage and an empty remainder.
2. **R36.** A `Different` returns an immutable `[2]ReplaySpec`, an aligned causal
   trace, and a minimality status. Acceptance: a domain whose only difference is a
   single VLAN rule, padded with three irrelevant injections, minimizes to the one
   injection that still yields the same `Difference.Observable`, reports
   `Minimal`, and its `Align` names the first trace entry that differs; a domain
   whose minimization needs more than the limit reports `LimitReached` with the
   partially reduced scenario.
3. **R37.** Cross-package conformance fixtures enforce result, ordering, trace,
   clone, derive, run, and comparison invariants. Acceptance: adding a fixture
   group with no package claiming it fails the enumeration test; randomized map
   insertion order does not change any fixture's canonical result.
4. **R38.** All comparison and search run in process under `go test` with no
   external simulator, device, daemon, or backend. Acceptance: the whole
   conformance suite passes under `go test -race ./src/common/netsim/...` with no
   network, socket, or Docker dependency.
5. **R39/R40.** The planning-corpus case runs within the search resource
   contract. Acceptance: a search over a domain larger than the retained-difference
   wall keeps coverage and a summary count for every candidate but full traces
   only for the retained differences; a retained difference keeps its complete
   `[2]ReplaySpec` and aligned trace.

## Out of scope

- Symbolic verification, SAT/SMT, an unbounded packet domain, production traffic
  generation, and packet forwarding.
- External simulator adapters, distributed search, stored baselines, service
  APIs, dashboards, and report databases.
- A concrete L3 traffic domain (interface only this phase).
- Per-item protocol-journey comparison, ruled out of scope in phase 7a; search
  reads whatever `fabric.Compare` reports.

## Units

### U1. Amend the direction record with search, minimization, and the resource contract

Files: `docs/architecture/2026-09-10-virtual-device-direction.md`
After: parent U7a
Change: a "Bounded differential search" subsection states that search enumerates
a declared finite domain deterministically, reports coverage and an exact
remainder, runs the current-against-candidate comparison per tuple, minimizes a
found difference by replay-checked reduction within the domain, aligns causal
traces at the first divergence, and retains full artifacts only for the
differences it keeps while accounting coverage and a summary for the rest. It
names the `search` package's dependency direction and that L3 is an interface
this phase.
Tests: none; prose, tested by U2–U5.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/architecture/2026-09-10-virtual-device-direction.md`

### U2. Domain interface and the L2 and timed-fault domains

Files: `src/common/netsim/search/domain.go`,
`src/common/netsim/search/l2.go`, `src/common/netsim/search/fault.go`,
`src/common/netsim/search/domain_test.go`,
`src/common/netsim/search/l2_test.go`, `src/common/netsim/search/fault_test.go`,
`src/common/netsim/search/README.md`
After: U1
Change: `Domain` (`Size`, `Enumerate`), `Candidate` (scenario plus tuple
identity), and `Limits`. `L2TrafficDomain` enumerates a cross product of declared
tuples in a total order; `TimedFaultDomain` enumerates declared cable faults at
declared times; `L3Domain` is a documented interface with no concrete
implementation. Enumeration is deterministic and independent of map order.
Tests: enumeration is a stable total order over randomized construction input;
`Size` equals the number of candidates enumerated; an empty domain yields nothing
and `Size` zero; duplicate declared tuples collapse to one candidate.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/search/`

### U3. The search driver, coverage, and the resource contract

Files: `src/common/netsim/search/search.go`,
`src/common/netsim/search/result.go`,
`src/common/netsim/search/search_test.go`,
`src/common/netsim/search/result_test.go`
After: U2
Change: `Search(current, candidate *fabric.Fabric, dom Domain, lim Limits)
Result`. It enumerates `dom`, calls `fabric.Compare(current, candidate,
cand.Scenario, lim.Budget)` per candidate, and builds `Result{Coverage,
Remainder, Differences}` where `Coverage` counts tested tuples, `Remainder` names
the untested tuples exactly on a budget cut, and `Differences` holds only the
retained `Different` candidates with their tuple, `[2]ReplaySpec`, and
`Difference`; discarded candidates contribute coverage and a summary count only.
Neither fabric is consumed.
Tests: full coverage over a small domain; budget-cut coverage with the exact
remainder; a domain with one `Different` returns one counterexample with its
replay specs; the retained-difference wall keeps summary-only for the overflow;
`current` and `candidate` unchanged after `Search` (a re-run of each matches a
never-searched twin).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/search/`

### U4. Minimization and first-divergence trace alignment

Files: `src/common/netsim/search/minimize.go`,
`src/common/netsim/search/align.go`,
`src/common/netsim/search/minimize_test.go`,
`src/common/netsim/search/align_test.go`
After: U3
Change: `Minimize(current, candidate *fabric.Fabric, cand Candidate, budget int)
(Candidate, Minimality)` removes scenario elements in a fixed order, keeping a
removal only when `fabric.Compare` still reports the same `Difference.Observable`,
and returns the reduced candidate with `Minimal` or `LimitReached`. `Align(cmp
fabric.Comparison) (int, bool)` returns the first paired-journey entry index whose
trace facts differ. Reduction stays within the declared domain.
Tests: a padded counterexample minimizes to the one element that still yields the
same observable and reports `Minimal`; a reduction that would change the
observable is rejected; the minimization limit is reported as `LimitReached` with
the partially reduced scenario; `Align` names the first differing trace entry and
returns `false` for an `Equivalent` comparison.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/search/`

### U5. Cross-package conformance fixtures

Files: `src/common/netsim/internal/netsimtest/conformance.go`,
`src/common/netsim/internal/netsimtest/conformance_test.go`,
`src/common/netsim/vswitch/conformance_test.go`,
`src/common/netsim/fabric/conformance_test.go`,
`src/common/netsim/search/conformance_test.go`
After: U3, U4
Change: a fixture library grouping the invariants — result canonicality, ordering
determinism, trace-fact stability, clone isolation, derive invalidation, run
lifecycle, exact behavioral comparison, diagnostic trace equality, and search
coverage accounting. Each package's `conformance_test.go` runs the groups it
owns; a `netsimtest` enumeration test asserts every declared group is claimed by
exactly one package, so a group added without a consumer fails.
Tests: the enumeration test fails on an unclaimed group; each group runs under
randomized map insertion order and current-first and candidate-first execution
with a stable canonical result.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/`

### U6. Document the search guarantees and limits

Files: `src/common/netsim/search/README.md`, `src/common/netsim/README.md`,
`docs/architecture/2026-09-10-network-simulation-prior-art-research.md`
After: U2, U3, U4, U5
Change: a working search-and-counterexample example, the coverage/remainder and
resource-contract guarantees, the minimality and alignment semantics, the
finite-domain and external-runtime boundaries, and the L3-interface deferral, with
the Batfish differential cross-reference.
Tests: compile the documentation example where practical.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/`

Waves: U1 U2 | U3 | U4 U5 | U6

## Verification

```bash
go test -race ./src/common/netsim/...
go vet ./src/common/netsim/...
.claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>
```

Run the conformance suite with randomized input insertion order and both
current-first and candidate-first execution; every canonical result and the
search coverage accounting must be stable.

## Definition of done

- [ ] Parent R35–R40 pass their acceptance examples.
- [ ] `Search` reports a finite domain, limits, coverage, and an exact remainder.
- [ ] A counterexample replays, minimizes deterministically, and names its first
      causal divergence, reporting `Minimal` or `LimitReached`.
- [ ] Discarded candidates keep coverage and a summary but not full traces;
      retained differences keep their complete replay and diagnostic artifacts.
- [ ] The conformance suite runs in process under `go test -race` and fails on an
      unclaimed fixture group.
- [ ] `search` imports `analysis`/`vswitch`/`fabric` and none imports `search`.
- [ ] Package READMEs and the direction record updated in the same change; no
      plan labels in code.

## Open questions

- The concrete numbers of the retained-difference wall and the per-candidate
  summary — decided in U3 against the planning-corpus case, recorded with the
  resource contract, not fixed here.
- Whether minimization should also reduce the observation window and budget, not
  only scenario elements. This phase reduces scenario elements only; a smaller
  window that still diverges is a diagnostic refinement a later increment can add.
- Follow-up from review: `fabric.Compare`'s `runScenarioFork` (added so faults
  fire at their declared time) is a near-verbatim copy of `run.go`'s
  `runWithActions`, differing only by collecting the injection frame ids for
  ordinal pairing. The two are faithful today, but a future edit to one silently
  diverges the comparison path from `RunScenario`. Fold the two together — have
  `runWithActions` optionally return the inject frame ids (or take a per-inject
  callback) so `Compare` reuses it — as its own reviewed change, not squeezed
  into this phase's fix loop.
