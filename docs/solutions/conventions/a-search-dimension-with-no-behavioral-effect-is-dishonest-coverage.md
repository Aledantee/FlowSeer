---
title: A Search Dimension That Produces No Behavioral Variation Is Dishonest Coverage
date: 2026-09-19
category: conventions
module: src/common/netsim/search
problem_type: convention
component: search
severity: high
applies_when:
  - "Building a finite domain for a differential search, fuzzer, or parametric test whose size and coverage a caller reads as a statement of what was exercised."
  - "Adding a dimension (a time, a seed, a mode, a delay) to an enumerated candidate whose effect passes through a downstream API you do not control."
  - "Reviewing a coverage or remainder number, and deciding whether the enumerated tuples are behaviorally distinct or only nominally distinct."
related_components: [netsim, fabric]
tags: [coverage, search, differential, honesty, false-coverage]
---

When a search reports coverage — "8 of 12 tuples tested" — the number is only
honest if each enumerated tuple actually produces a distinct evaluation. A
dimension that enumerates but does not change behavior inflates the size and the
coverage with candidates that are byte-identical, so a caller reads broad
coverage over what was really one run repeated.

## The trap, from the tree

netsim's `TimedFaultDomain` enumerated `faults × times`, giving each candidate a
tuple encoding the fault's declared time (`src/common/netsim/search/domain.go:24`,
`TimedFault.At`). But `Search` applied every fault with `SetFault` at the
fabric's start clock, ignoring `At`: `fabric.Compare` took only injections, with
no channel to schedule a fault at a simulation time. So a domain of 3 faults × 4
times reported `Size` 12 and 12 distinct tuples, but only 3 behaviorally distinct
evaluations. Every review test passed, because they counted tuples and asserted
`Size` — never that two tuples differing only in the time produced different
comparisons. The time was decoration.

The fix made the dimension real rather than hiding it: `fabric.Compare` now runs
a timed `Scenario` (`Candidate.ToScenario`, `domain.go:83`, builds an
`ActionFault` at each fault's `At`, `:111`), so a fault fires at its declared
time on the same `applyAction` path `RunScenario` uses. `TestTimedFaultDomainBehavioralTime`
(`src/common/netsim/search/search_test.go:290`) is the guard: the same fault at
time T1 versus T2 now yields `Different` versus `Equivalent`, proving the time
axis changes behavior.

## The rule

Before a dimension enters a domain, prove one of two things: that varying it
alone changes the evaluation (a test that fixes every other axis and asserts two
values of this one produce different results), or that it is dropped. A
dimension the downstream API cannot honor is not "not yet wired" — it is a
coverage lie until wired, because every number the search reports counts it. When
a fix would require extending that downstream API (here, teaching `Compare`/`Run`
to schedule an event at a simulation time), extend it or remove the dimension;
do not leave it enumerating.

## What it does not cover

This is about honesty of the coverage count, not the search strategy. A dimension
can be behaviorally real and still be a poor thing to enumerate exhaustively;
that is a domain-design question. And a per-axis behavioral test proves the axis
matters in isolation, not that every tuple is unique — a separate concern, as the
sibling [comparing two forked executions attributes observables by provenance](../architecture-patterns/comparing-two-forked-executions-attributes-observables-by-provenance.md)
covers for the pairing side.
