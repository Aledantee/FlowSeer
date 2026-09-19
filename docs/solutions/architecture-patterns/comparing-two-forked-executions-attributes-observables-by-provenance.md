---
title: Comparing Two Forked Executions Attributes Each Observable by Provenance, and What Has No Provenance Link Cannot Be Compared Per Item
date: 2026-09-19
category: architecture-patterns
module: src/common/netsim/fabric
problem_type: architecture_pattern
component: comparison
severity: high
applies_when:
  - "Diffing two independently forked or independently aged executions of a simulator, replayer, or interpreter, where each side carries pre-existing history plus the inputs under comparison."
  - "Deciding which produced items (journeys, events, records) belong to the change under test versus the shared history, and how to pair them across the two sides."
  - "Finding that a comparison over-reports differences from pre-existing state, or that a comparison block never fires because the items it looks for are never in its input set."
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

Attribute each item by **provenance** — the causal chain back to the input under
comparison — and pair by the *input's* ordinal, not the item's. netsim seeds the
comparison set with the injected frame ids and takes the transitive closure over
each journey's `Origin.Of` link (`collectScenarioJourneys`,
`src/common/netsim/fabric/compare.go:155`), then pairs the nth injection's journey
*group* on each side (`collectInjectionJourneys`, `compare.go:256`), reporting a
group-size mismatch as a `multiplicity` difference. Pre-existing history and its
during-run descendants are excluded because their provenance never reaches an
input root; genuine descendants (mirror copies, released held frames) are included
because theirs does.

## What cannot be compared per item

An item that carries **no provenance link** to the input under comparison cannot
be attributed to it across two independently aged executions. In netsim a protocol
emission (a spanning-tree BPDU) fires from a timer or from forwarding *some* frame,
and carries no `Origin.Of` back to the scenario, so there is no sound way to say
whether side A's extra BPDU is a real divergence or just a timer at a different
phase. Such state is compared only by its **final effect** — the converged device
state (spanning-tree roles, neighbors, forwarding database) — not per emitted item
(`compare.go:210`, the removed per-journey protocol block). Trying to include it by
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
