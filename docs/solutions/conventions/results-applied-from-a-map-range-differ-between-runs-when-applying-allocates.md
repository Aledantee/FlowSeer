---
title: Results Applied From a Map Range Differ Between Runs When Applying Allocates Ids or Orders a Report
date: 2026-10-09
category: conventions
module: src/common/sim/fabric
problem_type: bug
component: sim
severity: medium
symptoms:
  - "Two Derive calls on equal inputs assign a held frame's drop journey different FrameIDs, and the reports differ"
  - "A trace step lists the same set of tree transitions in a different order on different runs"
  - "A test of the ordering passes on most runs and fails on the rest"
root_cause: "The loop that applies results, or that samples state into an ordered list, ranged over a Go map. Iteration order is randomized per range, so whatever the loop allocates (a sequential id) or appends (a report entry) follows that order."
resolution_type: code_fix
applies_when:
  - "Writing a loop over a Go map whose body allocates a sequential id, appends to a report or trace step, or applies another component's output"
  - "A function builds an ordered slice from a set kept as a map, and a consumer compares the slice by position"
  - "Reviewing a Derive, Restore, or apply path that handles several named switches, ports, or VLANs in one pass"
  - "A test of order-dependent output passes on a single run and needs repetition to fail"
related_components: [vswitch, stp, derive]
tags: [determinism, map-order, derive, frame-id, mutation-testing]
---

## The situation

Two bugs in the switch composition work had one cause. The visible failure
differs: one changed ids, the other changed the order of facts in a trace.
Neither shows in a single run, and neither is visible in the code that
produces the values.

## What is true

- `assembly.Derive` builds switches in sorted name order
  ([assembly/assemble.go](../../../src/common/sim/assembly/assemble.go)).
  `fabric.New` applies each built node's output in the same order
  ([fabric/topology.go](../../../src/common/sim/fabric/topology.go)).
  A held-frame drop allocates a journey's `FrameID` from a shared counter,
  so that order decides which switch's drop gets the lower id.
- `configuredVLANs` collects VLANs into a set and returns them sorted
  (`src/common/sim/device/vswitch/inspect.go:179`, sorted at `:217`). The BPDU
  step records tree transitions in that order
  (`src/common/sim/device/vswitch/intercept.go:203`), so the order is the
  VLAN order and not the map's.
- A fork or a second construction of equal input is the comparison most
  tests make, so a nondeterministic order shows up as a flaky equality, not
  as a wrong value.

## How to apply

Ask of every `for k := range m` in a path that handles equal inputs twice:
does the body allocate, append, or call something that does? If so, range
over `slices.Sorted(maps.Keys(m))` or an existing sorted accessor.

```go
for _, name := range slices.Sorted(maps.Keys(t.nodes)) {
    f.applyOutput(f.clock, name, t.nodes[name].output)
}
```

A test for it needs two things a single run lacks:

- Two or more keys whose map order and sorted order disagree, so the
  fixture's instance numbers run opposite to the VLAN ids they carry
  (`src/common/sim/device/vswitch/stp_test.go:118`).
- Enough repetitions that a wrong order is near-certain, and a recorded
  mutation to prove it. The fabric test derives 128 times
  (`TestFabricDeriveOrdersHeldFrameDropsBySwitchName` in
  [`routing_test.go`](../../../src/common/sim/fabric/routing_test.go)).

## Evidence

The fix commit for the fabric loop states its failure rate with the loop
restored to `for name, sw := range next.switches`:

> Before the fix, 76 of 128 derives assigned sw1 the higher drop FrameID:
> "--- FAIL: TestFabricDeriveOrdersHeldFrameDropsBySwitchName (0.10s)"

(`06687017`). The follow-up pass states the same for the VLAN order, with
`slices.Sort(vids)` removed:

> --- FAIL: TestBPDURecordsTreeTransitionsInVLANOrder (map order
> decides, 12 of 20 runs failed)

(`8bec48e3`).

## What it does not cover

A loop whose body is order-independent (a sum, a set insert) needs no sort.
`validate-and-derive-judge-what-new-builds.md` covers the related case of a
lookup map whose keys collide.
