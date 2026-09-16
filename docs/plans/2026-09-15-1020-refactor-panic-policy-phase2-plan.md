---
title: Panic Policy Phase 2 - The smi Parser's Unwinding - Plan
type: refactor
date: 2026-09-15
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: code
parent: docs/plans/2026-09-15-1020-refactor-panic-policy-plan.md
---

# Panic Policy Phase 2 - The smi Parser's Unwinding - Plan

> Implemented. The parser unwinds by `p.fatal`; no `panic`/`recover` remains in
> `src/protocol/smi/internal/parse`. `benchstat` over `BenchmarkRecovery`,
> `BenchmarkParseFile` and `BenchmarkParseDecl` (count 6) showed no regression —
> a small improvement everywhere (ParseDecl −2% to −7%, Recovery −9%,
> allocations unchanged), from dropping the per-declaration
> `defer p.recoverBailout()`. The `&& !p.fatal` loop-gating in the draft
> deadlocked (typeSpan relies on group consuming its opener); the landed shape
> lets `group` run to completion and guards only the `nameList`/`members`
> appends. `p.fatal` was kept (not renamed to `p.limited`) so `recover_test.go`
> keeps its field reads.

## Goal

The `smi` parser stops using `panic(bailout{})` to unwind a resource limit, so
no `panic` or `recover` remains in `src/protocol/smi/internal/parse`. The means:
the parser stops unwinding at all — a limit sets the `p.fatal` flag that already
exists, `raise` and `limit` become no-ops once it is set, and the three bounded
loops that can reach a limit stop on it; the recursive descent runs to
completion on output the driver already discards. The per-declaration
`defer p.recoverBailout()` the change removes is measured against the committed
benchmarks so the cost is known rather than assumed.

This phase is wrong if the conversion turns out to need error returns threaded
through the recursive-descent productions. The evidence in Decisions says it
does not — the descent may keep running after a limit because its output is
discarded and `raise` is silenced — and that is the first thing to re-check
against the tree before writing code.

## Decisions

The parent's Decisions govern. The convert-or-exempt question is closed in
favor of converting: the bailout is first-party code, its only boundary is
`readGuarded` (`src/protocol/smi/load.go:305`) which clause 2 forbids it to name
as its handler, and it has no other clause-2 answer (not init-time, not proven).
The benchmark records the cost; it does not decide whether the conversion
happens.

- **The unwinding becomes flag-checked, not error-returning.** A limit sets
  `p.fatal`; nothing unwinds. Why this reaches so few functions: the descent
  after a limit does harmless work on a module the driver discards
  (`parse.go:172` sets `r.Modules = nil` whenever `p.fatal`), and `raise`
  returning early once `p.fatal` is set means that work appends no diagnostics.
  So no production between a limit site and the frame boundary needs to know a
  limit fired. The driver loop already breaks on `p.fatal`
  (`parse.go:151,156,165,171`), so a declaration that sets it ends the file.

- **`raise` and `limit` gain a `p.fatal` guard, and `limit` drops the panic.**
  Both are load-bearing and easy to miss:
  - `raise` (`recover.go:156`) returns immediately when `p.fatal` is set.
    Without this, on every path except the diagnostic-limit path the limit fires
    with the limit diagnostic not yet at the `frame.MaxDiagnostics` count, so
    `raise`'s `len(p.diags) >= frame.MaxDiagnostics` guard stays false and every
    later clause reader appends an ordinary diagnostic *after* the limit one.
    `recover_test.go:443` and `recover_test.go:465` assert the limit diagnostic
    is last and would fail.
  - `limit` (`recover.go:175`) returns immediately when `p.fatal` is already
    set, then (when not) appends the one limit diagnostic, sets `p.fatal`, and
    does **not** panic. Without the idempotency guard, a second `limit` — the
    depth path re-entering `enter`, or a member loop's next iteration — appends
    a second limit diagnostic, breaking the exactly-one-limit expectation of
    `recover_test.go:438`.
  The panic is what makes today's code correct on both counts; `p.fatal` is what
  replaces it.

- **The bounded loops keep advancing after a limit but stop growing.**
  `group` (`clause.go:570`), `nameList` (`clause.go:614`) and `reader.members`
  (`subtype.go:363`) call `limit` when a count is exceeded (`clause.go:596,631`,
  `subtype.go:379`; `group` also via `enter` at `clause.go:588`). They must not
  simply gate their loop on `!p.fatal`: an earlier draft did, and it deadlocked.
  `typeSpan` (`clause.go:553`) calls `group` and `continue`s **without advancing
  itself**, relying on `group` to consume the bracket group; a `group` that
  returned early on `p.fatal` consumed nothing, so `typeSpan` spun on the same
  opener forever. The panic was load-bearing for forward progress, not only for
  diagnostics. So:
  - `group` runs its loop to completion (no `p.fatal` gate). It grows nothing —
    it returns a span — so running on only consumes tokens, which is exactly the
    forward progress its callers need. It always advances, so it terminates.
  - `nameList` and `reader.members` keep their loops running (advancing) but
    guard the *append*: once `p.fatal` is set they stop growing their slices.
    That is what preserves the memory bound the cap exists for, while the loop
    still consumes to the closing brace so the caller makes progress.
  Silencing `raise` keeps the diagnostics correct; this keeps the parse both
  terminating and bounded.

- **Removing the unwind cannot overflow the stack.** `p.enter`
  (`recover.go:190`), the only depth-capped construct, is called from exactly one
  site — `clause.go:588` inside `group()` — and `group()` is an iterative
  token loop with its own local `depth` counter, not a recursive descent.
  `MaxDepth` (`frame.MaxDepth = 64`) bounds token nesting, not Go stack frames,
  so the panic never protected the stack and its removal cannot expose it.

- **`recoverBailout`, `guarded` and the `bailout` type are deleted.** `guarded`
  (`parse.go:197`) has one caller — `parse.go:161`, wrapping `p.grade(&m)` — and
  wraps it only to recover a bailout the grade pass could raise. With no panic,
  `grade` is called directly: a limit it reaches sets `p.fatal`, `grade`'s later
  raises are no-ops, and the driver discards the module. `declaration`'s
  `defer p.recoverBailout()` (`parse.go:214`) goes too. The doc comments on
  `declaration` and on the grade call that describe the recovered bail-out are
  rewritten to describe the flag.

- **`diag.Raise` is left exactly as it is.** It is called from `raise` and
  `limit` (`recover.go:169,176`) with cataloged codes and correct arity. Its own
  panic (uncataloged code or arity mismatch) lives in package
  `src/protocol/smi/internal/diag`, not in `internal/parse`, so it is outside
  this phase's DoD, which is about the `internal/parse` bailout. Phase 1's
  planned rename of `diag.Raise` to `diag.MustRaise` is **blocked and not
  landed** (see the phase-1 plan's U3 BLOCKED note); `diag.Raise` still exists
  under that name and still panics. This phase neither depends on nor changes
  that: it keeps calling `diag.Raise`. When the phase-1 unit is re-planned and
  lands, it updates these call sites, not this phase.

## Requirements

Parent requirement 13 lands here.

1. A file that reaches `frame.MaxDiagnostics` produces exactly one
   limit-exceeded diagnostic, last, and `nil` modules — as it does today.
   Acceptance: `TestDiagnosticLimitStopsTheFile`
   (`src/protocol/smi/internal/parse/recover_test.go:430`) passes unchanged:
   `len(r.Diagnostics) == frame.MaxDiagnostics+1`, the last is
   `diag.ErrCodeLimitExceeded`, `len(r.Modules) == 0`.
2. A construct that exceeds `MaxMembers` or `MaxDepth` produces one limit
   diagnostic last and sets `p.fatal`, with no ordinary diagnostic appended
   after it. Acceptance: `TestEnumerationMemberLimit` (`recover_test.go:451`)
   and `TestNestingBeyondCapIsFatal` (`recover_test.go:399`) pass unchanged.
3. A limit reached in the grade pass does not escape and leaves the limit
   diagnostic last. Acceptance: `TestGradeAtTheDiagnosticLimitDoesNotEscape`
   (`recover_test.go:507`) passes unchanged.
4. No `panic` or `recover` token remains in
   `src/protocol/smi/internal/parse` (excluding `_test.go`, e.g. the fuzz
   harness). `readGuarded` in `src/protocol/smi/load.go` is untouched.
   Acceptance: `grep -rn 'panic(\|recover()' src/protocol/smi/internal/parse
   --include='*.go' | grep -v _test.go` prints nothing.

## Out of scope

- `diag.Raise` and its rename — see the last Decision. No `MustRaise` symbol is
  created here.
- The parser's recovery *strategy*: the sync sets, the per-line diagnostic
  throttle (`recover.go:158`), `maxSyncAttempts`. Only the unwinding mechanism
  changes.
- `readGuarded` (`src/protocol/smi/load.go`) — the goroutine boundary phase 4
  generalizes. It catches; it does not raise.
- Phase 3's enforcement check for the no-panic rule.

## Units

### U1. Replace the bailout unwind with the p.fatal flag
Files: `src/protocol/smi/internal/parse/recover.go`,
`src/protocol/smi/internal/parse/parse.go`,
`src/protocol/smi/internal/parse/clause.go`,
`src/protocol/smi/internal/parse/subtype.go`
After: none
Change: `raise` returns early when `p.fatal` is set. `limit` returns early when
`p.fatal` is set, and otherwise appends its one diagnostic, sets `p.fatal`, and
returns without `panic(bailout{})`. `group` runs its loop to completion (no
`p.fatal` gate — see the Decision on forward progress); `nameList` and
`reader.members` keep their loops running but guard the append that grows their
slice with `!p.fatal` / `!r.p.fatal`. `recoverBailout`, `guarded` and the
`bailout` type are deleted; `parse.go` calls `p.grade(&m)` directly and
`declaration` drops its `defer p.recoverBailout()`. The doc comments on
`declaration` and the grade call are rewritten from "the bail-out is recovered
here" to the flag. `diag.Raise` calls are left unchanged. The parser continues
its descent after a limit; the output is discarded by the existing
`r.Modules = nil` and no diagnostic is appended after the limit because `raise`
is silenced.
Tests: the existing `recover_test.go` cases pass with no changed expectations —
`TestDiagnosticLimitStopsTheFile`, `TestEnumerationMemberLimit`,
`TestNestingBeyondCapIsFatal`, `TestGradeAtTheDiagnosticLimitDoesNotEscape`,
`TestNoPanicEscapesTheFrame`, and the ordinary-recovery cases. Two tests are
added, each watched failing against the defect first (undoing from a copy, not
`git restore`):
`TestRaiseAndLimitAreNoOpsOnceFatal` sets `p.fatal` on a fresh parser and
asserts `raise` and `limit` append nothing — it fails with a diagnostic
appended when either guard is removed. (This is white-box because `typeSpan`
absorbs non-clause tokens, so a source that reliably reaches a post-limit
`raise` is fragile; the guards are proven at the unit level instead.)
`TestNoPanicOrRecoverInPackage` walks the non-test package source by AST and
fails on any `panic`/`recover` call, proving requirement 4 on every `go test`.
The stale comment at `TestGradeAtTheDiagnosticLimitDoesNotEscape` ("pins the
recover the grade pass needs") is corrected to name the flag; a comment is not a
changed expectation.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/smi/internal/parse`

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/smi/internal/parse
```

The regression measurement, before and after, on the same machine and same
build tags:

Capture the baseline before applying U1 (this worktree shares the git stash
stack, so take the pre-change numbers on the base commit — a detached checkout
of `HEAD` before the unit lands, or the numbers recorded before the first edit
— rather than `git stash`):

```bash
# on the base, before U1
go test -run '^$' -bench 'BenchmarkRecovery|BenchmarkParseFile|BenchmarkParseDecl' \
  -count 10 ./src/protocol/smi/bench/ | tee before.txt
# after U1 lands
go test -run '^$' -bench 'BenchmarkRecovery|BenchmarkParseFile|BenchmarkParseDecl' \
  -count 10 ./src/protocol/smi/bench/ | tee after.txt
benchstat before.txt after.txt
```

What each benchmark measures for this change:

- `BenchmarkRecovery` (`src/protocol/smi/bench/bench_test.go:135`) parses 1000
  malformed declarations, each through `declaration`'s
  `defer p.recoverBailout()`. It does **not** reach a resource limit — 1000
  declarations against `frame.MaxDiagnostics = 10000`,
  `MaxDepth = 64`, `MaxMembers = 65536` — and does not need to: the bailout
  `panic` fires only at a real limit and is off every hot path, while the
  `defer` runs once per declaration. This benchmark is therefore the direct
  measurement of the cost the conversion removes; the expectation is no
  regression, likely a small improvement.
- `BenchmarkParseFile` and `BenchmarkParseDecl` measure the ordinary path,
  catching any regression from the added `!p.fatal` loop tests and the two
  early-return guards.

A regression `benchstat` reports as significant is recorded in the handoff and
is a finding, not an exemption: the conversion has no clause-2 alternative, so a
cost is optimized, not avoided.

## Definition of done

- [ ] Verifier green for `src/protocol/smi/internal/parse`.
- [ ] `recover_test.go` passes with no changed expectations (a corrected comment
      is not an expectation).
- [ ] `grep -rn 'panic(\|recover()' src/protocol/smi/internal/parse
      --include='*.go' | grep -v _test.go` prints nothing, and the new
      guard test asserts the same.
- [ ] `benchstat before.txt after.txt` recorded in the handoff, not just a
      verdict.
- [ ] This plan's `status` set with an outcome note under its title, and
      `U3. Landed:` filled in the parent (parent U3 is this phase).
- [ ] No plan labels (U1, R2) in code, comments, or commit messages.

## Open questions

- Whether `p.fatal` deserves a clearer name (say `p.limited`). Decided during
  implementation to keep `p.fatal`: `recover_test.go` reads the field white-box
  (`TestNestingBeyondCapIsFatal`), and renaming it would edit that test, which
  Requirement 1 asks to leave unchanged. A rename can be its own small change
  later if wanted.
