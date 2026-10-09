---
title: Comparing Two Forked Executions Attributes Each Observable by Provenance, and What Has No Provenance Link Cannot Be Compared Per Item
date: 2026-09-19
last_verified: 2026-10-07
category: architecture-patterns
module: src/common/sim/fabric
problem_type: architecture_pattern
component: comparison
severity: high
applies_when:
  - "Diffing two independently forked or independently aged executions of a simulator, replayer, or interpreter, where each side carries pre-existing history plus the inputs under comparison."
  - "Deciding which produced items (journeys, events, records) belong to the change under test versus the shared history, and how to pair them across the two sides."
  - "Finding that a comparison over-reports differences from pre-existing state, or that a comparison block never fires because the items it looks for are never in its input set."
  - "Preserving a located difference during minimization when equal group counts could hide which side contains the item."
related_components: [netsim, analysis]
tags: [comparison, provenance, differential, fork, false-difference]
---

To diff two executions that each ran a shared history and then the inputs under
comparison, the hard part is not the field-by-field compare — it is deciding
*which* produced items to compare and how to *pair* them. Three tempting keys all
fail, and one works, and a fourth class of item cannot be compared at all.

## What fails

- **A positional index.** Pairing the nth produced item on side A with the nth on
  side B mis-pairs the moment one side produces more items than the other — which
  is exactly the difference you are trying to detect. A size mismatch is a
  finding, not an alignment key.
- **A raw per-side id.** Two forks assign ids from their own counters, so an id is
  not a cross-fork key. netsim's first `fabric.Compare` refused fabrics that had
  already run for this reason.
- **An "emitted after time T" threshold.** Selecting items whose id or timestamp
  is past the point the inputs were injected over-includes what the *pre-existing*
  history produced during the run: a queued frame processed after T, or a timer
  that fired after T, spawns new items past the threshold that have nothing to do
  with the inputs under test. netsim tried `FrameID >= startFID` and it admitted
  the mirror and released-hold descendants of pre-scenario frames.

## What works

Attribute each item by **provenance**, the causal chain back to the input under
comparison. Pair by the input's ordinal and descendant lineage. The fabric seeds
the comparison set with injected frame IDs and takes the transitive closure over
each journey's `Origin.Of` link (`collectScenarioJourneys` in
`src/common/sim/fabric/compare.go`). `pairJourneys` in
`src/common/sim/fabric/comparison_location.go` pairs those descendants by origin
kind, mirror name, and occurrence within each parent. A journey present on only
one side reports `multiplicity` with `Current` and `Expected` set to `"true"` or
`"false"` for that lineage's presence. Different mirror names can therefore
produce a difference even when both groups have the same size.
`TestDiffFabricRunsOneSidedJourneyPresence` in
`src/common/sim/fabric/compare_internal_test.go` pins the values and lineage in
both directions. Pre-existing history and its during-run descendants are excluded
because their provenance never reaches an input root. Mirror copies and released
held frames are included because theirs does.

## Presence values must belong to the located item

An equal-size pair of groups can contain different descendants. Reporting both
group sizes as `2` for a missing `span-a` journey loses which side contains it.
The minimizer compares both the location and displayed values, so an accepted
trial could exchange sides while retaining the same target. Report presence for
the located lineage instead: current `true`, expected `false`, with the values
reversed when the sides swap.

`src/common/sim/fabric/compare.go:265-267` returns:

```go
location.Field = "Journey"
return analysis.Difference{Observable: "multiplicity", Current: strconv.FormatBool(pair.current >= 0), Expected: strconv.FormatBool(pair.expected >= 0)}, location, true
```

`src/common/sim/fabric/compare_internal_test.go:33-34` pins both orientations:

```go
{"current", "true", "false", tc.a, tc.b},
{"expected", "false", "true", tc.b, tc.a},
```

The acceptance condition at `src/common/sim/search/minimize.go:102` includes
`comparison.Difference == result.Difference`. Commit `585549da` records restoring
the group-count result and observing:

```text
--- FAIL: TestDiffFabricRunsOneSidedJourneyPresence (0.00s)
```

## What cannot be compared per item

An item that carries **no provenance link** to the input under comparison cannot
be attributed to it across two independently aged executions. In netsim a protocol
emission (a spanning-tree BPDU) fires from a timer or from forwarding *some* frame,
and carries no `Origin.Of` back to the scenario, so there is no sound way to say
whether side A's extra BPDU is a real divergence or just a timer at a different
phase. Such state is compared only by its **final effect** — the converged device
state (spanning-tree roles, neighbors, forwarding database) — not per emitted item
(`diffFabricRuns` in `src/common/sim/fabric/compare.go`). Trying to include it by
threshold re-introduces the pre-existing-history false difference above.

## What it does not cover

Provenance selection needs every produced item to carry a link to its cause;
where the model does not record one (the protocol case), the per-item comparison
is genuinely out of reach, not merely unimplemented. And final-effect comparison
catches convergence divergence but not transient path or timing that settles to
the same state — a real narrowing, to be named in the contract rather than hidden.
This is the differential-comparison sibling of
[a slot that carries two roles](one-slot-two-roles-is-a-defect-class-not-a-defect.md):
a positional index and a raw id each try to make one number answer both "which
side's item" and "which input caused it".
