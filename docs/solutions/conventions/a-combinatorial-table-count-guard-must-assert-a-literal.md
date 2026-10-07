---
title: A Combinatorial Table Count Guard Must Assert a Literal, Not a Partition Identity
date: 2026-10-03
last_verified: 2026-10-07
category: conventions
module: src/services/device/internal/authn
problem_type: convention
component: authn
severity: high
applies_when:
  - "Writing or reviewing a combinatorial matrix, table-driven property test, or fuzzer that skips impossible or unsupported combinations."
  - "Checking whether a loop-terminating count guard proves that all intended test cases actually executed."
  - "Asserting that dynamic test actions, such as concrete-expectation branches, mid-request context cancellations, or mock handler triggers, actually fired during test execution."
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
// src/services/device/internal/authn/verifier_test.go:1992-2049
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

- The fixed assertions: `src/services/device/internal/authn/verifier_test.go:2011-2049`.
- Mutation proof: deleting `tokenExpired` from the `tokens` slice drops
  `runCount` from 1,632 to 1,408 and produces:
  `ran 1408 rows, want 1632`.

## Count concrete-expectation branches

A row count can hold while every row misses a conditional expectation.
`TestFDBFrameTimeMatchesPriorAdvance` increments `concreteCount` inside its
expired-seed expectation (`src/common/sim/layer/bridge/fdb_time_test.go:89-95`)
and checks a literal at `:143-145`:

```go
if concreteCount != 2 {
    t.Errorf("concrete expectations ran %d times, want 2", concreteCount)
}
```

Commit `6e752071` records disabling that block with `false &&` and the result:

```text
--- FAIL: TestFDBFrameTimeMatchesPriorAdvance (0.16s)
```

The same test independently requires 4,128 rows and two boundary-expectation
hits (`:140-147`). Count the cases that reach each concrete oracle even when
all generated rows execute.

## Count representation axes and successful yields separately

`TestUDPSizeValidationInEitherOrder` crosses value versus pointer variations,
declaration order, and a size below versus at the UDP packet boundary. Four of
its eight rows refuse, and each accepted row yields two frames.
`src/common/sim/stream/stream_test.go:169-170` pins both counts independently:

```go
if cases != 8 || yields != 8 {
    t.Fatalf("cases/yields = %d/%d, want 8/8", cases, yields)
}
```

Commit `412266d2` records that dropping the pointer axis produced `4/4` and:

```text
--- FAIL: TestUDPSizeValidationInEitherOrder (0.00s)
```

## What this does not cover

This rule applies to combinatorial tables and property matrices that filter
unsupported combinations. It does not govern property fuzzers with unbounded
random generation, which instead assert invariant properties per generated
sample.
