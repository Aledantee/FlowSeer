---
title: A Combinatorial Table Count Guard Must Assert a Literal, Not a Partition Identity
date: 2026-10-03
category: conventions
module: src/services/device/internal/authn
problem_type: convention
component: authn
severity: high
applies_when:
  - "Writing or reviewing a combinatorial matrix, table-driven property test, or fuzzer that skips impossible or unsupported combinations."
  - "Checking whether a loop-terminating count guard proves that all intended test cases actually executed."
  - "Asserting that dynamic test actions, such as mid-request context cancellations or mock handler triggers, actually fired during test execution."
related_components: [testing, property-tests]
tags: [testing, property-tests, tautology, count-guards, combinatorial-matrix]
---

# A Combinatorial Table Count Guard Must Assert a Literal, Not a Partition Identity

A combinatorial or property-table test loops over the Cartesian product of
several axes, skipping combinations marked impossible or unexecutable. A count
guard at the end of the loop is intended to verify that coverage held and that
rows were not dropped silently. When that guard tests the executed count against
the total product minus the skipped count, the assertion is an identity: every
iteration is either counted or skipped by definition, so the condition can never
fail.

## The trap, from the tree

`src/services/device/internal/authn/verifier_test.go` exercises OIDC token
verification across five axes: discovery endpoint behaviors (4), keys endpoint
behaviors (4), token variants (7), prior cache states (6), and caller contexts
(3), yielding 2,016 total cases. Of those, 384 combinations are impossible
because mid-request cancellation requires a live network request to cancel in,
leaving 1,632 valid rows.

Before review round 3, the loop tracked `runCount` and `skippedCount` through the
Cartesian product:

```go
for ... {
    if _, skip := impossibleCases[caseKey]; skip {
        skippedCount++
        continue
    }
    runCount++
    // execute test case...
}

if runCount < totalProduct-skippedCount {
    t.Fatalf("ran %d cases, want at least %d", runCount, totalProduct-skippedCount)
}
```

Every pass through the nested loops either increments `skippedCount` or
increments `runCount`. The sum `runCount + skippedCount` is guaranteed to equal
`totalProduct` by the loop control flow. The guard `runCount < totalProduct - skippedCount`
simplifies algebraically to `runCount < runCount`, which is identically false.
If an axis value vanished, or if `impossibleCases` was populated with unmatched
keys or broad wildcards, the check still passed.

## How to apply

Assert row counts against independent literal constants, verify that the set of
impossible cases matches its expected size, and count dynamic side effects:

```go
// src/services/device/internal/authn/verifier_test.go:2067-2125
const (
    wantRowsRun      = 1632
    wantImpossible   = 384
    wantCancelsFired = 288
)

if len(impossibleCases) != wantImpossible {
    t.Fatalf("listed %d impossible combinations, want %d", len(impossibleCases), wantImpossible)
}

// ... loop over axes ...

if skippedCount != len(impossibleCases) {
    t.Errorf("matrix holds %d of the %d listed impossible combinations", skippedCount, len(impossibleCases))
}
if runCount != wantRowsRun {
    t.Errorf("ran %d rows, want %d", runCount, wantRowsRun)
}
if cancelsFired != wantCancelsFired {
    t.Errorf("fired the caller's cancel from an endpoint handler in %d rows, want %d", cancelsFired, wantCancelsFired)
}
```

The three checks enforce three distinct properties:
1. `len(impossibleCases) == wantImpossible` proves the exclusion table itself was
   constructed with the expected size.
2. `skippedCount == len(impossibleCases)` proves every listed exclusion was
   actually encountered during traversal, with no orphaned keys.
3. `runCount == wantRowsRun` checks the executed row count against an
   independent literal, so adding or dropping an axis value fails the test.
4. `cancelsFired == wantCancelsFired` proves dynamic hooks actually executed.

## Evidence

- The fixed assertions: `src/services/device/internal/authn/verifier_test.go:2086-2125`.
- Mutation proof: deleting `tokenExpired` from the `tokens` slice drops
  `runCount` from 1,632 to 1,408 and produces:
  `verifier_test.go:2120: ran 1408 rows, want 1632`.

## What this does not cover

This rule applies to combinatorial tables and property matrices that filter
unsupported combinations. It does not govern property fuzzers with unbounded
random generation, which instead assert invariant properties per generated
sample.
