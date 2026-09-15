---
title: Panic Policy Phase 2 - The smi Parser's Unwinding - Plan
type: refactor
date: 2026-09-15
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-15-1020-refactor-panic-policy-plan.md
---

# Panic Policy Phase 2 - The smi Parser's Unwinding - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

The `smi` parser stops using `panic(bailout{})` to unwind a resource limit.
The means: flag-checked early return on the `p.fatal` field that already
exists, measured against the committed benchmarks so the cost is known rather
than assumed.

The convert-or-exempt question the earlier draft left open is closed. The
bailout is first-party code, and the only boundary above it is `readGuarded`
(`src/protocol/smi/load.go:305`) — which clause 2 forbids it to name as its
handler, because that boundary exists to contain a parser *bug*, not to
implement the parser's control flow. The benchmark still runs; it decides how
the conversion is written, not whether it happens.

This phase is wrong if the conversion turns out to need error returns through
the recursive-descent productions. The evidence below says it does not, and
that evidence is the first thing to re-check.

## Decisions

The parent's Decisions govern. This phase's own decision — convert or exempt —
is deliberately not made here, because it rests on a measurement nobody has
taken. What is decided is the shape the conversion takes if it happens:

- The unwinding becomes flag-checked early return on the `p.fatal` field that
  already exists, not error returns through the productions. Why: the bailout
  reaches only five raise sites (`recover.go:164,193`, `clause.go:596,631`,
  `subtype.go:379`) and unwinds to two boundaries (`parse.go:214` in
  `declaration`, `parse.go:198` in `guarded`). `p.fatal` is already read by
  the driver loop at `parse.go:151,156,165,171`, and already forces
  `r.Modules = nil`, so output after a limit is discarded either way.
- Removing the unwind cannot overflow the stack. Why: the depth cap that
  `p.enter` guards is reached from exactly one site, `clause.go:588` inside
  `group()`, and `group()` is an iterative loop with its own `depth` counter
  rather than a recursive descent. `MaxDepth` bounds token nesting, not Go
  stack frames.
- `readGuarded` (`src/protocol/smi/load.go:305`) stays and is not this phase's
  subject. Why: it is the goroutine boundary clause working before the clause
  existed — its own comment says "this is a goroutine, and a panic here takes
  the process down without naming the file that caused it". Phase 4 generalizes
  it. What it does not do is license the bailout it catches.
- The benchmark cannot buy an exemption here even if it shows a cost. Why:
  clause 1's benchmark exemption is about *placement* — it permits a panic
  outside a `Must` function. It does not answer clause 2, and the bailout has
  no clause-2 answer available: not init-time, not proven, and barred from
  naming `readGuarded`. A measured regression means the conversion needs
  optimizing, not abandoning.
- `limit` must become idempotent — returning immediately when `p.fatal` is
  already set — and `raise` must return early on `p.fatal` too. Why: two
  distinct breakages, and the second is the one that is easy to miss. First,
  on the `MaxDiagnostics` path, control returns to `raise` whose
  `len(p.diags) >= frame.MaxDiagnostics` guard is still true, so the next
  `raise` calls `limit` again and appends a second limit diagnostic. Second,
  on the depth and member paths (`recover.go:193`, `clause.go:596,631`,
  `subtype.go:379`), the limit fires with only one diagnostic on the list, so
  `raise`'s guard is false and every later clause reader appends ordinary
  diagnostics *after* the limit one. `recover_test.go:425` and
  `recover_test.go:464` both assert the limit diagnostic is last, and both
  would fail. The panic is what makes the current code correct on both paths;
  `p.fatal` has to be checked in `raise`, `limit`, `group`, `nameList`, and
  `members`.

## Requirements

Requirement 13 of the parent applies. Restated with its acceptance example:

1. A file that reaches `frame.MaxDiagnostics` produces exactly one
   limit-exceeded diagnostic and `nil` modules, as it does today. Acceptance:
   the existing cases in `src/protocol/smi/internal/parse/recover_test.go`
   pass unchanged — this is a refactor, and a changed expectation in that
   file is a defect in the conversion, not a new baseline.
2. No `panic` or `recover` remains in `src/protocol/smi/internal/parse`.
   `readGuarded`, in `src/protocol/smi`, is untouched.

## Out of scope

- `diag.Raise`, which phase 1 makes non-panicking under its existing name. No
  `MustRaise` is ever created; a session looking for that symbol has read an
  earlier draft of this plan.
- The parser's recovery *strategy* — the sync sets, the per-line diagnostic
  throttle, `maxSyncAttempts`. Only the unwinding mechanism is in question.

## Units

To be written when this phase is re-planned. The expected shape is one unit
converting `raise`, `limit`, `group`, `nameList`, and `members`, plus one deleting
`recoverBailout`, `guarded`, and the `bailout` type — but the split depends
on what the measurement says, so it is not fixed here.

## Verification

The discriminating measurement, before and after, on the same machine:

```bash
go test -run '^$' -bench 'BenchmarkRecovery|BenchmarkParseFile|BenchmarkParseDecl' \
  -count 10 ./src/protocol/smi/bench/ | tee after.txt
benchstat before.txt after.txt
```

`BenchmarkRecovery` at `src/protocol/smi/bench/bench_test.go:135` is the
intended limit-path measurement, but that is unverified and load-bearing:
`recoveryModule` (`bench_test.go:398,472-487`) builds 1000 malformed
declarations and `TestRecoveryModuleIsMalformed` asserts only that diagnostics
are non-zero. Nothing pins that the run reaches `frame.MaxDiagnostics`,
`MaxDepth`, or `MaxMembers`. If it reaches none of them, the benchmark
measures the per-declaration `defer p.recoverBailout()` (`parse.go:214`) and
nothing else, and the convert-or-exempt verdict would rest on a number that
never executed a panic. The first step of this phase is to establish which of
the three limits the fixture reaches and add the assertion, or add a fixture
that reaches one. `BenchmarkParseFile` and `BenchmarkParseDecl`
catch a regression in the ordinary path from the added `p.fatal` checks. A
regression that `benchstat` reports as significant is what buys the
exemption; anything else means the conversion stands.

## Definition of done

- [ ] `benchstat` output recorded in the handoff, not just a verdict.
- [ ] `recover_test.go` passes with no changed expectations, or each change is
      justified in the handoff.
- [ ] Verifier green for every changed path.
- [ ] This plan's `status` set, and `U2. Landed:` filled in the parent.

## Open questions

- Whether `p.fatal` deserves a clearer name once it is the only unwinding
  mechanism rather than a flag the panic sets on its way out.
