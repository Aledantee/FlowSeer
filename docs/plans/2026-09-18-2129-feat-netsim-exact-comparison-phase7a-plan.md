---
title: Network Simulation Analysis Completeness, Phase 7a - Exact Comparison - Plan
type: feat
date: 2026-09-18
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
parent: docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
---

# Network simulation analysis completeness, phase 7a: exact comparison and dispositions - Plan

## Goal

A caller compares a current network against a candidate and gets one of three
honest answers — `Equivalent`, `Different`, or `Inconclusive` — backed by the
complete set of behavioral observables, not a shallow equality. The means:
`vswitch.Compare` and `fabric.Compare` return a `Disposition` and, on
`Different`, the observable that differed; `fabric.Compare` forks each input
internally so it can compare fabrics that are already mid-run without consuming
them, replacing today's `Same bool` and its "two fabrics that have not injected"
refusal (`src/common/netsim/fabric/compare.go:29`).

Stop condition: if two forks of running fabrics cannot have their new
scenario journeys paired by anything stable — injection identity rather than the
frame id each fork assigns from its own history — then comparison of running
fabrics is not a field-by-field diff and this plan's shape is wrong. Phase 5's
`Fork` carries the queue, journeys, and frame-id counter
(`src/common/netsim/fabric/fabric.go`, `Fork`), so the pre-fork journeys exist
on both sides and only the scenario's own injections must pair.

### What this phase claims of the parent

- Parent R32, R33, R34: claimed whole (exact switch and fabric comparison over
  all behavioral observables, returning the three dispositions, without
  consuming inputs).
- Parent R35, R36 (search, coverage, minimization) and R37, R38, R39, R40
  (cross-package conformance and the search resource contract) are phase 7b's;
  this phase builds the comparison API they call.

## Decisions

- **Comparison is field-level over the observables, not a fingerprint match.**
  Why: `Snapshot.Fingerprint()` (phase 6) is the convergence oracle and covers
  device and link state, but not per-journey path, timing, or multiplicity, and
  a caller who gets `Different` needs to know *which* observable differed for the
  counterexample. The fingerprint stays the oracle; comparison walks the
  observable set and returns the first difference in a declared order.
- **`fabric.Compare` forks each input internally.** `func Compare(a, b *Fabric,
  scenario []Injection, budget int) Comparison` calls `a.Fork()` and `b.Fork()`,
  injects the scenario into the forks, runs them, and compares; `a` and `b` are
  never stepped, injected, or learned into. Why: the no-injection precondition
  existed because journeys paired by a raw frame id each fabric assigns from its
  own count (`compare.go:19-25`); forking a running fabric makes that pairing
  impossible for the pre-existing journeys, so comparison pairs only the
  scenario's own journeys, by the injection that produced them. Chosen over
  caller-supplied forks (session-settled, user-directed): keeps the fork
  lifecycle inside Compare rather than on every caller.
- **New scenario journeys pair by injection index, not frame id.** Each
  `Injection` in the scenario is applied to both forks in the same order; the
  journey each produces on each side pairs by that ordinal. A fork's pre-scenario
  journeys are not compared — they are shared history, equal by construction.
  Why: frame ids diverge across forks with different histories, so the id is not
  a cross-fork key; the injection order is.
- **`Disposition` is a new type in `analysis`, beside `Status`.** `Equivalent`,
  `Different`, `Inconclusive`, all non-empty strings, no zero value (a
  never-compared result is not a disposition, per
  [one-slot-two-roles](../solutions/architecture-patterns/one-slot-two-roles-is-a-defect-class-not-a-defect.md)).
  Why: `analysis` already owns `Status` and its conservative precedence
  (`src/common/netsim/analysis/status.go`), and both `vswitch` and `fabric`
  comparison return the same three dispositions, so the type is shared where
  `Status` is.
- **The disposition is derived, not stored.** `Equivalent` requires both sides'
  `Status` to be `Complete` and every behavioral observable equal. A proven
  observable mismatch is `Different`. Any side that is `Incomplete`, `Exhausted`,
  `Unstable`, or `Unsupported`, or a run stopped by budget with pending work, is
  `Inconclusive` — equivalence over an incomplete evaluation is not proven. Why:
  parent R34; a `Different` must be a fact, and an `Inconclusive` must not be
  mistaken for an `Equivalent`.
- **Behavioral observables, from the landed types.** For a switch
  (`vswitch.ForwardResult`, `src/common/netsim/vswitch/compare.go:13`): the
  forwarded frames per egress port with their rewritten fields, the selected LAG
  member, the mirror copies, the PCP, and the typed forwarding outcome. For a
  fabric, per paired journey (`fabric.Journey`,
  `src/common/netsim/fabric/journey.go`): `State`, `Origin`, the ordered
  `Entries` (hops, cable crossings, deliveries, drops with reasons and location),
  `Deliveries`, `Protocol`, and the frame content each carries; plus the run's
  `Stop`, `Status`, `Pending`, and `Issues`, and the final `Snapshot` behavioral
  state. `Metadata`, `Evidence`, the semantic `trace` text, and the raw
  `Fingerprints`/`Cycle` diagnostics are diagnostic only and never make a
  `Different`. Why: parent R32, R33, and the direction record's observable axis.

## Requirements

1. **R32 (parent R32).** `vswitch.Compare` reports a `Difference` over the
   complete normalized forward result, not a bool. Acceptance: two switches whose
   egress ports match but whose destination-MAC rewrite differs compare
   `Different` and name the rewritten frame field; an added no-op diagnostic
   trace step leaves them `Equivalent`.
2. **R33 (parent R33).** `fabric.Compare` compares path, time, multiplicity, drop
   location, journey terminal state, final state, status, and issues, over
   internal forks, without consuming `a` or `b`. Acceptance: comparing two
   fabrics that have each already injected and run a frame succeeds (no
   "have not injected" refusal), and running `a` again afterwards produces what a
   never-compared twin produces; two fabrics carrying active periodic protocols
   compare repeatably, giving the same disposition each call.
3. **R34 (parent R34).** Comparison returns `Equivalent`, `Different`, or
   `Inconclusive`. Acceptance: two equal `Exhausted` results are `Inconclusive`;
   a proven destination-MAC rewrite is `Different`; two `Complete` results equal
   on every observable are `Equivalent`.
4. **R-pair.** New scenario journeys pair by injection ordinal across the two
   forks; a fork's pre-scenario journeys are excluded from the diff. Acceptance:
   comparing two mid-run fabrics whose pre-run histories differ in frame count
   still pairs the scenario's injected frames correctly and reports a difference
   only in the scenario's own behavior.
5. **R-imm.** A `Different` result carries an immutable replay specification for
   the scenario and the first differing observable with both sides' values;
   nothing in the result aliases either fork's live state. Acceptance: a returned
   `Comparison` read after both forks are stepped further is unchanged.

## Out of scope

- Finite differential domains, coverage accounting, and bounded counterexample
  search (parent R35): phase 7b.
- Deterministic minimization and first-divergence trace alignment (parent R36):
  phase 7b. This phase returns the first differing observable in a declared
  order, not a minimized counterexample.
- Cross-package conformance fixtures and the search resource contract (parent
  R37-R40): phase 7b.
- Any L3 traffic domain or enumeration.

## Units

### U1. Disposition type and the observable/diagnostic contract

Files: `src/common/netsim/analysis/disposition.go`,
`src/common/netsim/analysis/disposition_test.go`,
`docs/architecture/2026-09-10-virtual-device-direction.md`
After: parent U6
Change: add `analysis.Disposition` with `Equivalent`, `Different`, and
`Inconclusive` as non-empty string constants and a `Validate` that rejects the
zero value. The direction record gains a "Current-against-candidate comparison"
subsection: it names the behavioral observables of a switch and a fabric, the
diagnostic-only fields, the three dispositions and the rule that derives them
from equality plus `Status`, that `Compare` forks its inputs and pairs new
scenario journeys by injection ordinal, and that `fabric.Compare`'s former
"two fabrics that have not injected" precondition is now lifted by forking. It
records that the fingerprint remains the convergence oracle and comparison is a
separate field-level walk.
Tests: `disposition_test.go` proves the disposition truth table — the three
values, the zero-value rejection, and that no two encode alike.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/analysis/ docs/architecture/2026-09-10-virtual-device-direction.md`

### U2. Exact switch comparison

Files: `src/common/netsim/vswitch/compare.go`,
`src/common/netsim/vswitch/compare_test.go`,
`src/common/netsim/vswitch/README.md`
After: U1
Change: replace `Comparison.Same bool` with a `Disposition` and a `Difference`
that names the first differing observable in a declared order over the complete
normalized `ForwardResult` — forwarded frames per egress port with rewritten
fields, selected LAG member, mirror copies, PCP, and the typed forwarding
outcome — treating the semantic trace and metadata as diagnostic. Equal complete
results are `Equivalent`; a mismatch is `Different` naming the observable; a
result the switch could not fully evaluate is `Inconclusive`.
Tests: `compare_test.go` gains one mismatch case per behavioral observable (a
destination-MAC rewrite with matching egress, a different LAG member, a missing
mirror copy, a changed PCP, a different outcome), a no-op trace-difference case
that stays `Equivalent`, and an `Inconclusive` case.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/`

### U3. Exact fabric comparison over internal forks

Files: `src/common/netsim/fabric/compare.go`,
`src/common/netsim/fabric/compare_test.go`,
`src/common/netsim/fabric/README.md`
After: U1
Change: `Compare(a, b *Fabric, scenario []Injection, budget int) Comparison`
forks `a` and `b`, injects the scenario into the forks in order, runs each to
`budget`, and compares. The refusal at `compare.go:29` is removed. The result
carries a `Disposition`, and on `Different` the first differing observable with
both sides' values and an immutable `ReplaySpec` for the scenario. Journeys pair
by injection ordinal; pre-scenario journeys are excluded. Comparison walks, per
paired journey, `State`, `Origin`, the ordered `Entries` (path, crossings,
deliveries, drops with reason and location), `Deliveries`, `Protocol`, and frame
content, then the run's `Stop`, `Status`, `Pending`, and `Issues`, then the
final `Snapshot` behavioral state; `Metadata`, evidence, and trace text are
diagnostic. The disposition follows U1's rule. Neither `a` nor `b` is stepped,
injected, or learned into.
Tests: `compare_test.go` gains a mismatch per fabric observable (path, timing,
multiplicity, drop location, journey terminal, final state, status, issues), a
non-consuming assertion (both inputs unchanged after Compare, and a re-run of
`a` matches a never-compared twin), an active-periodic-protocol repeatability
case (same disposition across repeated calls), a mid-run-inputs case exercising
the lifted precondition and ordinal pairing, an `Equivalent` complete case, an
`Inconclusive` incomplete case, and a result-immutability case (the returned
`Comparison` is unchanged after both forks are stepped further).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric/`

### U4. Comparison invariants and documentation

Files: `src/common/netsim/internal/netsimtest/comparison_cases.go`,
`src/common/netsim/internal/netsimtest/comparison_cases_test.go`,
`src/common/netsim/README.md`, `src/common/netsim/fabric/README.md`,
`src/common/netsim/vswitch/README.md`
After: U2, U3
Change: add a small comparison-invariant corpus that a package test consumes —
determinism under randomized map insertion order, current-first versus
candidate-first execution giving the same disposition, and the non-consuming
guarantee — and document the three dispositions, the behavioral-versus-diagnostic
split, and the internal-fork lifecycle in the package READMEs with a working
current-against-candidate example.
Tests: `comparison_cases_test.go` runs the corpus with randomized insertion order
and both execution orders and asserts a stable disposition and difference.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/`

Waves: U1 | U2 U3 | U4

## Verification

```bash
go test -race ./src/common/netsim/...
go vet ./src/common/netsim/...
.claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>
```

Run the comparison corpus with randomized input insertion order and both
current-first and candidate-first execution; the disposition and the named
difference must not change.

## Definition of done

- [ ] Parent R32, R33, R34 pass their acceptance examples.
- [ ] `vswitch.Compare` and `fabric.Compare` return a `Disposition` and, on
      `Different`, the first differing behavioral observable.
- [ ] `fabric.Compare` forks its inputs, compares mid-run fabrics, and never
      mutates `a` or `b`.
- [ ] Trace and metadata differences stay diagnostic and cannot make a
      `Different`.
- [ ] Package READMEs and the direction record updated in the same change.
- [ ] Verifier green for every changed path; no plan labels in code.

## Open questions

- Whether `Difference` should report only the first differing observable or all
  of them. This plan returns the first in a declared order, because that is what
  a counterexample needs and what phase 7b will minimize; a full diff is a
  diagnostic convenience 7b can add if a caller needs it.
