---
title: A Search Dimension That Produces No Behavioral Variation Is Dishonest Coverage
date: 2026-09-19
category: conventions
module: src/common/sim/search
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

When a search reports coverage ("8 of 12 tuples tested"), the number is only
honest if each enumerated tuple actually produces a distinct evaluation. A
dimension that enumerates but does not change behavior inflates the size and the
coverage with candidates that are byte-identical, so a caller reads broad
coverage over what was really one run repeated.

## The trap, from the tree

`TimedFaultDomain.buildCandidate` in `src/common/sim/search/fault.go` combines
base injection actions with a `fabric.ActionFault` carrying the selected
`Action.At`. `Candidate.ToScenario` in `domain.go` preserves those times, and
`fabric.Compare` runs that scenario through the fabric's timed action path.
This chain is necessary: encoding a time in a tuple alone cannot make a
downstream comparison observe it.

`TestTimedFaultDomainBehavioralTime` in
`src/common/sim/search/search_test.go:317` fixes the fault and varies its time.
The two candidates yield `Equivalent` and `Different`. The suite's
`TestSearchFabricsUnchangedAfterSearch` and `TestSearchBudgetCutExactRemainder`
also pin non-consuming evaluation and exact untested accounting.

## The rule

Before a dimension enters a domain, prove one of two things: that varying it
alone changes the evaluation (a test that fixes every other axis and asserts two
values of this one produce different results), or that it is dropped. A
dimension the downstream API cannot honor inflates coverage, because every
number the search reports counts it. When
a fix would require extending that downstream API (here, teaching `Compare`/`Run`
to schedule an event at a simulation time), extend it or remove the dimension.
Do not leave it enumerating.

## What it does not cover

This is about honesty of the coverage count, not the search strategy. A dimension
can be behaviorally real and still be a poor thing to enumerate exhaustively.
That is a domain-design question. A per-axis behavioral test proves the axis
matters in isolation. It does not prove every tuple is unique, as the
sibling [comparing two forked executions attributes observables by provenance](../architecture-patterns/comparing-two-forked-executions-attributes-observables-by-provenance.md)
covers for the pairing side.
