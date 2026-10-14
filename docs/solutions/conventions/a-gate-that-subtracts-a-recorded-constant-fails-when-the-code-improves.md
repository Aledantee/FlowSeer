---
title: A Gate That Subtracts a Recorded Constant Fails When the Code Improves, So Measure Its Zero Point in the Same Run
date: 2026-10-09
category: conventions
module: src/common/sim/fabric
problem_type: convention
component: sim
severity: medium
applies_when:
  - "Writing or reviewing a scaling or growth gate that subtracts a fixed floor, baseline, or overhead measured on an earlier date from a fresh measurement"
  - "A change lowers an allocation count, size, or latency and a scaling gate fails although nothing got worse"
  - "Tightening an absolute allocation baseline in the same change that alters the fixed cost it was measured over"
related_components: [testing, performance-gates]
tags: [testing, allocations, scaling, baseline, false-failure]
---

# A gate that subtracts a recorded constant fails when the code improves

A scaling gate asks how much a cost grows with input size. It computes the
growth as a measurement minus the cost at zero input. Recording that zero
point as a literal ties the gate to the code that existed on the day it was
measured. A change that lowers the fixed cost lowers the measurement and
leaves the literal, so the subtracted growth shrinks or goes negative and the
bound that compares against it fails on an improvement.

## What happened

`TestRepresentativeFabricForkAllocsQueueScaling` once subtracted
`forkAllocationFixedFloor = 4515` ("measured 2026-09-23") from the 4096-deep
fork count. Commit `9b57657f` lowered `Fabric.Fork` by 880 allocations at
every queue depth. The commit message records the paired counts at depths 0,
4096 and 16384 as `4502/3622`, `16827/15947` and `53787/52907`. The depth-zero
count fell with the others, the literal no longer matched, and the gate failed
until it measured its own zero.

## The rule

Measure the zero point in the gate, on the same code, at the start of the
run. The input that varies is the only thing the gate takes from outside.

```go
fab0x := simtest.RepresentativeFabricAtQueueDepth(0)
allocs0x := testing.AllocsPerRun(5, func() { _ = fab0x.Fork() })
// ...
incremental1x := allocs1x - allocs0x
```

(`src/common/sim/fabric/fork_scale_test.go:127-129,143`). The growth is now a
difference of two measurements taken together, so a change to the fixed cost
moves both and the bound still compares like with like.

An absolute baseline (`forkAllocationBaseline`,
`src/common/sim/fabric/fork_scale_test.go:19`) is a different gate. It is
meant to fail when the count rises, and it has to be re-measured and tightened
when the count falls, or it stops guarding the savings. Keep the two apart:
the absolute gate carries a dated literal, the scaling gate carries none.

## Evidence

The commit message of `9b57657f` names the discriminating mutations for each
gate and their failures:

```text
15950 > 15949, "--- FAIL: TestRepresentativeFabricForkAllocs (0.13s)".
118444 > 70131, "--- FAIL: TestRepresentativeFabricForkAllocsQueueScaling (0.52s)".
```

The first is three extra fixed allocations in `Fork`, caught by the absolute
baseline at two allocations of headroom. The second is a quadratic scratch
allocation per queued arrival, caught by the scaling gate.

## What it does not cover

Measuring the zero point inside the test does not make the numbers portable.
Allocation counts depend on the Go version and platform, and the literal in
`forkAllocationBaseline` was measured on 2026-10-09 on macOS. A fixed floor is
fine where the thing subtracted is a constant of the code under test and not a
measurement of it.
