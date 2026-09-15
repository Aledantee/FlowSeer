---
title: Panic Policy Phase 4 - Enforcement - Plan
type: chore
date: 2026-09-15
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: mixed
parent: docs/plans/2026-09-15-1020-refactor-panic-policy-plan.md
---

# Panic Policy Phase 4 - Enforcement - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

The Decisions and Requirements below are settled and the Units are a worked
sketch, but requirement 3's panic inventory and requirement 4's allowlist both
describe the tree phase 3 leaves behind, which does not exist yet. Re-check them
against the tree, not against this file.

## Goal

A repository gate fails when a `panic` sits outside a `Must`/`must` function, or
when a `go` statement spawns work outside the one supervised helper, so the two
mechanically decidable halves of the panic rule hold without a reviewer
remembering them. The means: clear the one placement violation left in the tree,
then a Go AST conformance test under `test/conformance/` that walks every
non-test `.go` file in `src/` and runs in `go test -race ./...`, the merge-gate
authority, plus a `docs/code-style.md` note that clause 2's handling nuance and
clause 3's caller documentation are review-enforced, not checked.

This phase is wrong if either decidable half turns out to have live violations
that are not bugs — a legitimate `panic` outside a `Must` function or a
legitimate `go` outside the helper — because then the gate would force a false
change rather than catch a real one. The evidence says the only such panic is
`diag.Raise`, which U1 converts here, and the only such `go` statements are the
ones phase 3 removes.

## Decisions

The parent's Decisions govern. This phase makes these:

- **`diag.Raise` is cleared here, not left in phase 1.** It is the last panic in
  non-test `src/` outside a `Must` function, so the placement gate cannot be
  green without it, and a phase may not depend on an earlier phase reopening.
  Phase-1 U3 is withdrawn and its work lands as U1 below, re-planned against what
  blocked it. Parent requirement 6 is amended to match: the proof is not a scan
  of `MustRaise` call sites, because five of the nine are variadic forwarders
  passing a runtime `errs.Code` and a spread `args...`, which no AST pass can
  resolve.

- **The proof scans the forwarders' callers, and fails closed.** Behind the five
  forwarders sit 53 call sites, every one passing a generated `ErrCode*`
  identifier and a fixed-length argument list, at one hop — two for
  `src/protocol/smi/resolve.go:602`, which goes through the public wrapper. So a
  scan is total over first-party code if it models the forwarders explicitly:
  a registry naming each forwarder's receiver, function name, code parameter and
  variadic parameter. It must fail on any `MustRaise` call it cannot resolve, or
  a sixth forwarder added later escapes silently.
  `src/protocol/smi/internal/parse/no_panic_test.go:56` is the precedent for that
  self-check, and `src/common/errs/code_test.go:376-401` is the technique for
  resolving a callee through its import path with `go/ast` alone.

  Failing closed covers resolution, not coverage: a file the walk never opens
  cannot fail. `smi.MustRaise` stays exported, so a caller can sit anywhere in
  the repository, including the nested `src/protocol/smi/bench` and
  `src/protocol/smi/differential` modules that a root `go test ./...` does not
  reach. So the walk is rooted at the repository root through the `repoRoot`
  helper (`test/conformance/proto/layout_test.go:91`), and its self-check asserts
  that it saw every package holding a forwarder — not merely that it saw a
  file.

- **`smi.Raise` is renamed too and stays exported.** It forwards an unchecked
  runtime code across a package boundary, so it takes the `Must` prefix. It stays
  exported because `src/protocol/smi/doc.go:133` cites it by name in the
  package's own account of the allocation-free raise path; unexporting would gut
  that narrative to close an edge nothing outside the repository consumes.
  `doc.go:133` and `internal/diag/diagnostic.go:301` are updated in the same unit.

- **The gate is a Go conformance test, not a `tools/hooks/` script.** It lives at
  `test/conformance/panic/`, mirrors `test/conformance/proto/layout_test.go`
  (a `filepath.WalkDir` over the tree with a `t.Errorf` per violation and a
  table test of the predicate), and runs under `go test -race ./...`. Why: the
  rule is about the function enclosing a `panic` and the location of a `go`,
  which a line pattern cannot see. AGENTS.md's Enforced-rules section names
  `go test -race ./...` the authority and hooks "fast feedback, not the
  authority", so a Go test *is* the enforcement and needs no policy-surface
  review to land.

- **The check walks the filesystem, so it covers the nested modules.**
  `go test ./...` from the root does not descend into `src/edge/netpen` or the
  `bench` modules, but the gate parses files by path rather than importing them,
  so one test in the root module inspects every module's source. The nested
  modules hold sanctioned panics (`mustMAC`) and goroutines of their own.

- **Only two halves are machine-checked; the rest is review, and the doc says
  so.** The gate enforces clause 1 (a `panic` only inside a `^[Mm]ust` function)
  and the goroutine boundary (a `go` statement only inside `src/common/spawn`).
  Clause 2's "first-party code may not name a `src/common/service` recover as its
  handler" and clause 3's caller documentation both need a repo-wide call graph
  plus a judgment on prose, and `callOwned` takes a `Runner` interface
  (`src/common/service/supervisor.go:707`) so whether a function runs under a
  recover is a runtime property a static pass cannot decide. Why review rather
  than a cheap approximation: requiring a caller's doc to contain the word
  "panic" is noisy and gameable, and a gate that claims clause 2 while only
  approximating it is worse than one that says what it covers.

- **The benchmark-placement exemption is not implemented.** Clause 1 lets a
  `panic` sit in a non-`Must` function whose comment cites a committed benchmark.
  No site claims it — phase 2 converted the only candidate — so the gate enforces
  the `Must` prefix outright and `docs/code-style.md` notes the exemption is
  review-granted if a site ever needs it. Principle 5: no mechanism for a case
  with no caller.

- **The goroutine allowlist is `src/common/spawn`'s own file.** Phase 3 routes
  every first-party goroutine through it, so after that phase the only `go`
  statement in non-test `src/` is the one inside that package. A location
  allowlist rather than a callee selector: the helper owns the single `go`, and
  callers invoke it as an ordinary function, so there is nothing else to allow.

- **The gate does not see `sync.WaitGroup.Go`.** It checks for an `*ast.GoStmt`,
  and `src/common/pump/merge.go:26` is an `*ast.CallExpr` — a spawn with no `go`
  keyword. Phase 3 converts that site; this gate would not have caught it and
  does not claim to. `docs/code-style.md`
  says so, so the next `WaitGroup.Go` is a review catch rather than a believed-in
  gate's blind spot.

## Requirements

Parent requirement 14 lands here, minus the halves the Decisions move to review,
and amended parent requirement 6.

1. `diag.Raise` is `diag.MustRaise` and `smi.Raise` is `smi.MustRaise`, and the
   panic satisfies clause 2's proven answer. Acceptance: a scan test resolves
   every first-party call that reaches `MustRaise` to a catalog row and fails
   when the argument count disagrees with the row's arity or the code is not
   cataloged; a fixture call with four arguments to a two-arity code fails it.
2. The scan fails closed. Acceptance: a `MustRaise` call whose code argument the
   scan cannot resolve — a sixth forwarder, or a computed code — fails the test
   rather than being skipped, and the test fails if it scanned no files.
3. A `panic` whose nearest enclosing function declaration is not
   `Must`/`must`-prefixed fails the gate, naming the file and line. Acceptance:
   `panic("x")` added to a plain function makes the test fail with that
   file:line; the same `panic` inside `func mustFoo()` does not. Every panic the
   post-U1 tree holds is inside such a function — `snmp.MustOID`,
   `snmp.MustChangeIndicator`, `catalog.MustRegister`,
   `netsimtest.Registry.MustRegister`, `mibgen.mustDecodeNatural` and
   `mustDecodeCast`, the four `mustMAC`, and `diag.MustRaise` — so the gate
   reports zero findings on it.
4. A `go` statement outside `src/common/spawn` fails the gate, naming the file
   and line. Acceptance: `go func() { … }()` added to any non-test file under
   `src/` other than the helper's makes the test fail; the `go` statement inside
   the helper does not.
5. The predicates are unit-tested in isolation, so a finding is provable without
   a real violation in the tree. Acceptance: a table test feeds the placement
   predicate a `panic` in a `must` function (pass) and in a plain function
   (fail), and the goroutine predicate a `go` in the helper file (pass) and
   elsewhere (fail) — mirroring `TestProtoPathPolicy`.
6. `docs/code-style.md` states which halves the gate enforces and which stay
   review rules. Acceptance: the Panics section names the placement and
   goroutine-boundary checks as gated, says clause 2's boundary nuance and
   clause 3's caller documentation are enforced by review, and says the gate does
   not see `sync.WaitGroup.Go`.

## Out of scope

- `_test.go` files (a test that panics fails its own binary).
- Transitive obligation across module boundaries: the gate reasons about
  `go.aledante.io/FlowSeer` source only; a panic from a dependency is its
  contract.
- Wiring the gate into the Stop hook (`tools/hooks/stop-check.sh`,
  `.claude/settings.json`, `.codex/hooks.json`). Those are policy surfaces under
  AGENTS.md; the merge-gate Go test is the authority, and adding fast-feedback
  wiring is a separate guardrail-reviewed diff.
- Clause 2 handling and clause 3 caller documentation — moved to review by the
  Decisions above.
- Making `diag.MustRaise`'s panic unexpressible by generating one constructor per
  catalog row. It is feasible — the catalog is closed at compile time and already
  validates `Arity == CountVerbs(Format)` (`internal/catalog/catalog.go:398`) —
  but it changes ~57 call sites and reshapes all five forwarders to take a built
  `Diagnostic`, which moves when a throttled line pays for interning its
  arguments (`internal/lex/lex.go:448`). A larger diff for the same clause-2
  answer.

## Units

### U1. `diag.Raise` becomes `MustRaise` and gains its proof
Files: `src/protocol/smi/internal/diag/diagnostic.go`,
`src/protocol/smi/diagnostic.go`, `src/protocol/smi/doc.go`,
`src/protocol/smi/internal/diag/arity_scan_test.go` (new),
`src/protocol/smi/codes_test.go`, and the call sites in `internal/lex/lex.go`,
`internal/frame/frame.go`, `internal/parse/recover.go` and `resolve.go`
After: none
Change: `diag.Raise` and the public `smi.Raise` are renamed `MustRaise`, keeping
their bodies and their reasoning about why an uncataloged code and a wrong arity
are both parser bugs. Each of the five forwarders documents the panic and names
the proven answer, per clause 3. A new scan test walks non-test source, resolves
every call that reaches `MustRaise` — the four direct constant-code sites and the
53 callers behind the forwarders — to a catalog row, and fails on an arity
mismatch, an uncataloged code, a call it cannot resolve, or a run that scanned
nothing. `Diagnostic.nargs` is clamped to `catalog.MaxArgs` (`catalog.go:27`,
currently 4) regardless, because the arity panic is what keeps `Message`'s
`d.args[i]` loop (`diagnostic.go:312-315`) in bounds and the proof is a test, not
a compile-time guarantee. No catalog row is added, so `zz_generated_codes.go`,
`COVERAGE.md` and `shipped-codes.txt` are untouched.
Tests: `arity_scan_test.go` — the four failure modes of requirement 2, each
watched failing first against a fixture; the real tree passes.
`diagnostic_test.go:209`'s existing recover-based cases target `MustRaise`, and a
five-argument call renders rather than indexing out of range. `codes_test.go:109`
calls the exported wrapper and stops compiling on the rename, so it is in the
unit: out of scope for the rule is not out of scope for the build.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/smi`

### U2. The panic-placement and goroutine-boundary conformance gate
Files: `test/conformance/panic/panic_policy_test.go` (new)
After: U1
Change: this unit needs phase 3 landed; the whole plan does, and its title line
says so. A `package conformance` test walks every non-test `.go` file under
`src/`, parses each with `go/parser`, and reports a finding when a `panic` call's
nearest enclosing `*ast.FuncDecl` name does not match `^[Mm]ust`, or when a
`*ast.GoStmt` appears outside `src/common/spawn`. A `panic` or `go` outside any
function declaration — package-level, or in an `init` — is a finding too. The
predicates `panicPlacementViolation` and `goStmtViolation` are separate functions
the table test drives, and the walk fails if it scanned no files. `repoRoot` is
the ~15-line helper from `test/conformance/proto/layout_test.go`.
Tests: `panic_policy_test.go` — the walk reports zero findings on the tree (this
is the gate); a table test drives each predicate over synthetic sources, watched
failing first by feeding the passing fixture to the opposite expectation.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- test/conformance/panic`

### U3. Record the enforced and review-only halves in the style guide
Files: `docs/code-style.md`
After: none
Change: the Panics section gains a short paragraph: the placement rule and the
goroutine boundary are gated by a conformance test under `test/conformance/panic`;
clause 2's handling nuance and clause 3's caller documentation are enforced by
review, because they need a call graph and a judgment on prose the gate cannot
make; the benchmark-placement exemption is review-granted, as no site claims it;
and the gate sees the `go` keyword, not `sync.WaitGroup.Go`.
Tests: none — prose, governed by `docs/doc-style.md`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/code-style.md`

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/smi test/conformance/panic docs/code-style.md
go test -race ./test/conformance/panic/ ./src/protocol/smi/...
```

The gate is proved against the tree it is written for: it reports zero findings
on the post-phase-3, post-U1 tree, and reports a finding for each acceptance
example added to a scratch file and removed. A run before phase 3 lands reports
65 spawn sites and is evidence the prerequisite is not met, not a gate defect.

## Definition of done

- [ ] Verifier green for `src/protocol/smi`, `test/conformance/panic` and
      `docs/code-style.md`.
- [ ] `go test -race ./test/conformance/panic/` reports zero findings.
- [ ] Each acceptance example produces a finding when added and none when
      removed.
- [ ] `docs/code-style.md` names the gated halves, the review-only halves, and
      the `WaitGroup.Go` blind spot.
- [ ] A note in the handoff that the Stop-hook fast-feedback wiring is available
      but out of scope, so a person can stage it under guardrail review.
- [ ] This plan's `status` set with an outcome note under its title, and
      `U4. Landed:` filled in the parent.
- [ ] No plan labels in code, comments, or commit messages.

## Open questions

- Whether the scan's forwarder registry should live beside the forwarders rather
  than in the test, so adding a forwarder and registering it are one edit.
  Recommendation: keep it in the test. A production-side registry is a mechanism
  whose only caller is a test, and the fail-closed check already makes an
  unregistered forwarder loud.
- Whether `src/edge/netpen` keeps a `go` statement. Phase 3's last open question
  decides it; if netpen is exempted there, this gate needs a second allowlist
  entry and `docs/code-style.md` has to say why.
