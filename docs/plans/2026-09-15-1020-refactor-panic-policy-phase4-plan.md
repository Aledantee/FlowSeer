---
title: Panic Policy Phase 4 - Enforcement - Plan
type: refactor
date: 2026-09-15
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: mixed
parent: docs/plans/2026-09-15-1020-refactor-panic-policy-plan.md
---

# Panic Policy Phase 4 - Enforcement - Plan

> Implemented. 3 units, verified together on 2026-09-15. The gate reports
> zero findings on the tree, and each acceptance example produces a finding
> naming its file and line when added: a `panic` in a plain function, a `go`
> statement outside the helper, and neither for the same `panic` in a `must`
> function, a `_test.go` file, or a `testdata` fixture.

> Re-planned against the post-phase-3 tree. The earlier draft's inventories were
> written before phase 3 landed and marked `needs-decisions`; every one of them
> has now been checked against the tree and the numbers are recorded below as
> measurements, not estimates. Phase 3's last open question — whether the
> deferred-completion ordering rule is machine-checkable — is answered here with
> a probe rather than an opinion, and the answer is no.

## Goal

A repository gate fails when a `panic` sits outside a `Must`/`must` function, or
when a `go` statement spawns work outside `src/common/spawn`, so the two
mechanically decidable halves of the panic rule hold without a reviewer
remembering them. The means: clear the one placement violation left in the tree
(`diag.Raise`), add a Go AST conformance test under `test/conformance/panic/`
that walks non-test `.go` files in `src/` and runs under `go test -race ./...`,
and record in `docs/code-style.md` which halves are gated and which stay review
rules.

This phase is wrong if either decidable half has a live violation that is not a
bug, because the gate would then force a false change rather than catch a real
one. Measured against the tree: the only `panic` outside a `Must` function is
`diag.Raise` (`src/protocol/smi/internal/diag/diagnostic.go:225,228`), which U1
converts, and the only `go` statement in non-test `src/` is
`src/common/spawn/spawn.go:84`. Both halves report zero findings after U1.

## Decisions

The parent's Decisions govern. This phase makes these:

Ruled: the parent's `Landed:` lines for phases 1, 2 and 3 are rewritten to
carry their commit ranges. Why: the ledger gate reads the range to prove a
phase's prerequisites are in the tree, and prose without one fails it, so
phase 4 cannot verify against a parent the earlier phases left in the old
shape. Cost if wrong: three prose lines in the parent plan; the ranges are
read off this branch's history and nothing depends on them but the gate.

### The tree, measured

The earlier draft asked for these to be re-checked. They were, on the current
`worktree-ban-panics` tree:

- Twelve `panic` calls in non-test, non-`testdata` `src/`. Ten sit in
  `MustOID`, `MustChangeIndicator`, `mustDecodeNatural`, `mustDecodeCast`,
  `catalog.MustRegister`, `Registry.MustRegister` and the four `mustMAC`, each
  already citing its clause-2 answer in a doc comment. Two sit in `diag.Raise`.
- One `go` statement in non-test `src/`: `src/common/spawn/spawn.go:84`. No
  `sync.WaitGroup.Go` survives; the 65 spawn sites all call `spawn.Go`.
- Five variadic forwarders reach `diag.Raise`, with 53 callers behind them —
  `(*lexer).raise` 8, `(*parser).raise` 32 (24 through a plain `p` receiver
  and 8 through `r.p` from the subtype and value readers), `(*cutter).raise`
  4, `(*resolver).raise` 9, and the public `smi.Raise`, which
  `(*resolver).raise` is the only non-test caller of. Every one of the 53
  passes a constant `ErrCode*` identifier as its code argument. These figures
  were re-measured by AST walk after review found the scan's first matcher
  dropped any call whose receiver was a selector chain, so the eight `r.p`
  calls were unscanned until that fix even though the count above included
  them. Four direct call sites pass a
  constant code and a fixed-length argument list: `lex.go:576`,
  `recover.go:186`, `frame.go:173`, `frame.go:652`.

So the earlier draft's counts hold and its plan of attack survives. What follows
is what changed.

### `src/edge/netpen` is not exempted, and the allowlist has one entry

Phase 3's open question left this undecided. It is decided by the tree: netpen
routes all eleven of its spawns through `spawn.Go` (`cmd/netpen/main.go`,
`cmd/netpen/stream.go`, `full/full.go`, `runner/runner.go`,
`attacks/testtest/leg.go`), so the gate needs a single allowlist entry,
`src/common/spawn`. No second entry, and nothing for `docs/code-style.md` to
justify. Why this matters beyond bookkeeping: an exemption would be a hole in
the only mechanically checkable half of the rule, and the module that would have
held it does not need one.

### The deferred-completion ordering rule is not checkable, and this settles it

Phase 3 found the same defect in five packages, three of them carrying a comment
asserting it was handled, and asked phase 4 to decide whether a gate could catch
the next one. The honest answer is no, and the evidence is a probe rather than a
judgment.

The tightest syntactic predicate the shape admits is: a `spawn.Go` call whose
`fn` literal contains a deferred completion (`close(…)`, a call to a method
named `Done` or `Close`) *and* which passes a reporting option. Run over the
post-phase-3 tree, that predicate flags three sites:

| Site | Deferred completion | Why it is correct |
| --- | --- | --- |
| `src/common/pump/merge.go:60` | `forwarders.Done()` | the joiner reads `results` by counted receive; the `WaitGroup` is not on the data path |
| `src/common/service/delivery.go:100` | `workers.Done()` | the sink writes the buffered `failures` channel, read independently of `workers.Wait()` |
| `src/common/service/supervisor.go:684` | `coordinator.owned.Done()` | the runner waits on the coordinator's terminal channel, which the sink fills |

Three flagged, three correct: the predicate is wrong every time it fires on a
tree phase 3 has already fixed. The fact that separates a defect from a correct
site is what the *joiner* reads — a counted receive versus a `WaitGroup.Wait`
followed by a read of what the sink writes — which is data flow across two
functions and often two packages, not a property of the call expression. A
static pass cannot see it, and a gate whose every finding on a clean tree is a
false positive trains reviewers to suppress it.

So the rule stays a review rule. It is already written down where a reviewer and
a call-site author will meet it — `docs/code-style.md:327-345` and `spawn.Go`'s
own doc comment (`src/common/spawn/spawn.go:53-74`), both landed in phase 3 — so
nothing new is written. U3 adds one thing the existing prose lacks: the
discriminating question a reviewer should ask, which is what the joiner reads.

### `diag.Raise` is cleared here, and the proof scans the forwarders' callers

Unchanged from the earlier draft, restated because it is the phase's largest
unit. `diag.Raise` is the last panic in non-test `src/` outside a `Must`
function, so the placement gate cannot be green without it, and a phase may not
depend on an earlier one reopening. Phase-1 U3 is withdrawn and lands here.

The proof cannot be a scan of `MustRaise` call sites, because five of the nine
are variadic forwarders passing a runtime `errs.Code` and a spread `args...`,
which no AST pass can read. It scans the 53 callers behind them instead, at one
hop — two for `src/protocol/smi/resolve.go:602`, which goes through the public
wrapper. That is total over first-party code only if the scan models the
forwarders explicitly, so it carries a registry naming each forwarder's package,
receiver, function name, and the parameter indices of its code and variadic
arguments:

| Package | Function | Code arg | Variadic arg |
| --- | --- | --- | --- |
| `internal/lex` | `(*lexer).raise` | 1 | 2 |
| `internal/parse` | `(*parser).raise` | 1 | 2 |
| `internal/frame` | `(*cutter).raise` | 1 | 2 |
| `smi` | `(*resolver).raise` | 2 | 3 |
| `smi` | `Raise` → `MustRaise` | 1 | 2 |

### The scan fails closed, and coverage is asserted, not assumed

It must fail on any call reaching `MustRaise` whose code it cannot resolve, or a
sixth forwarder added later escapes silently.
`src/protocol/smi/internal/parse/no_panic_test.go:56` is the precedent for that
self-check, and `src/common/errs/code_test.go:376-401` is the technique for
resolving a callee through its import path with `go/ast` alone.

Failing closed covers resolution, not coverage: a file the walk never opens
cannot fail. `smi.MustRaise` stays exported, so a caller can sit anywhere in the
repository, including the nested `src/protocol/smi/bench` and
`src/protocol/smi/differential` modules a root `go test ./...` does not reach.
(Neither holds a call today — checked — but the scan is the thing that keeps
that true.) So the walk is rooted at the repository root through the `repoRoot`
helper (`test/conformance/proto/layout_test.go:91`), and its self-check asserts
it saw every package holding a forwarder, not merely that it saw a file.

### Resolving a constant code to its arity

The scan reads two generated files to map identifier to code string, then asks
the catalog for the arity at runtime:

- `src/protocol/smi/internal/diag/zz_generated_codes.go` declares
  `ErrCodeLimitExceeded = errs.NewCode("smi/limit-exceeded")` — the string
  literal is the code.
- `src/protocol/smi/zz_generated_codes.go` re-exports each as
  `ErrCodeModuleNotFound = diag.ErrCodeModuleNotFound`, so package-`smi` call
  sites resolve through one alias hop.

Arity comes from `catalog.Entries()` at run time rather than from parsing the
catalog source. Why: the catalog already validates `Arity == CountVerbs(Format)`
and `Arity <= MaxArgs` (`internal/catalog/catalog.go:390-403`), so reading the
live table inherits that validation instead of re-implementing it.

### `smi.Raise` is renamed too and stays exported

It forwards an unchecked runtime code across a package boundary, so it takes the
`Must` prefix. It stays exported because `src/protocol/smi/doc.go:133` cites it
by name in the package's own account of the allocation-free raise path;
unexporting would gut that narrative to close an edge nothing outside the
repository consumes. `doc.go:133` and `internal/diag/diagnostic.go:301` are
updated in the same unit.

### `nargs` is bounded at its assignment

`Diagnostic.Message` loops `d.args[i]` over `d.nargs`
(`diagnostic.go:312-315`), and `d.args` is a `[catalog.MaxArgs]Arg`. Today only
the arity panic keeps `nargs` in range. Assign it as
`uint8(min(len(args), catalog.MaxArgs))`. This is not handling an error that
cannot occur, which `docs/code-style.md:150-151` would refuse: it is stating a
field's domain at the one place the field is written, and the domain is the
array's length. The arity panic still fires first and still carries the
diagnosis; the bound just stops a future edit to the panic from turning a
caller bug into an out-of-range index.

### The gate is a Go conformance test, not a `tools/hooks/` script

It lives at `test/conformance/panic/`, mirrors
`test/conformance/proto/layout_test.go` (a `filepath.WalkDir` with a `t.Errorf`
per violation plus a table test of the predicate), and runs under
`go test -race ./...`. Why: the rule is about the function enclosing a `panic`
and the location of a `go`, which a line pattern cannot see. AGENTS.md's
Enforced-rules section names `go test -race ./...` the authority and hooks "fast
feedback, not the authority", so a Go test *is* the enforcement and needs no
policy-surface review to land.

### The two predicates match on the right thing

Both were traced against the tree they will run over, because a predicate that
looks right and anchors wrong passes its own table test:

- `^[Mm]ust` over `FuncDecl.Name.Name` matches 30 function declarations in
  non-test `src/`, every one a genuine must-helper — `MustOID`, `mustMAC`,
  `mustDecodeCast`, and about eighteen `testing.TB` helpers in non-`_test.go`
  files (`mustNewBridge`, `mustVLAN`, `mustSwitchLearn`) that take a `TB` and
  never panic. No name matches the prefix by accident, and the predicate only
  fires on a `panic` anyway, so the `TB` helpers are not its business. A
  generic declaration (`mustGetLayer[T any]`) carries the plain name in
  `Name.Name`, so the type parameters do not defeat the match.
- The goroutine allowlist compares the file's directory to `src/common/spawn`
  for equality, not `strings.HasPrefix` against the path. There is no
  `src/common/spawnpool` today, and prefix matching would silently admit one
  the day somebody adds it — an allowlist that grows by accident is the failure
  mode this gate exists to prevent.

### The check walks the filesystem, and skips `testdata`

`go test ./...` from the root does not descend into `src/edge/netpen` or the
`bench` modules, but the gate parses files by path rather than importing them,
so one test in the root module inspects every module's source. The nested
modules hold sanctioned panics (`mustMAC`) and goroutines of their own.

It skips any path containing a `testdata` segment. There are thirteen `.go`
files under `src/**/testdata/`, including
`src/protocol/yang/test/integration/testenv/testdata/gnmitarget/`, a fixture
gNMI server in its own module. None holds a `panic` or a `go` statement today,
but they are fixtures standing in for foreign code, and the rule is about
first-party code. The earlier draft did not name this exclusion; it is the one
gap the re-check found.

### Only two halves are machine-checked, and the doc says so

The gate enforces clause 1 (a `panic` only inside a `^[Mm]ust` function) and the
goroutine boundary (a `go` statement only inside `src/common/spawn`). Clause 2's
"first-party code may not name a `src/common/service` recover as its handler"
and clause 3's caller documentation both need a repo-wide call graph plus a
judgment on prose, and `callOwned` takes a `Runner` interface
(`src/common/service/supervisor.go:707`), so whether a function runs under a
recover is a runtime property. Why review rather than a cheap approximation:
requiring a caller's doc to contain the word "panic" is noisy and gameable, and
the ordering-rule probe above is what a cheap approximation looks like in
practice — three findings, three false.

### The benchmark-placement exemption is not implemented

Clause 1 lets a `panic` sit in a non-`Must` function whose comment cites a
committed benchmark. No site claims it — phase 2 converted the only candidate —
so the gate enforces the `Must` prefix outright and `docs/code-style.md` notes
the exemption is review-granted if a site ever needs it. Principle 5: no
mechanism for a case with no caller.

### The gate does not see `sync.WaitGroup.Go`

It checks for an `*ast.GoStmt`. `src/common/pump/merge.go:26` was an
`*ast.CallExpr` — a spawn with no `go` keyword — and phase 3 converted it, but
this gate would not have caught it and does not claim to. `docs/code-style.md`
says so, so the next `WaitGroup.Go` is a review catch rather than a believed-in
gate's blind spot.

## Requirements

Parent requirement 14 lands here, minus the halves the Decisions move to review,
plus amended parent requirement 6.

1. `diag.Raise` is `diag.MustRaise` and `smi.Raise` is `smi.MustRaise`, and the
   panic satisfies clause 2's proven answer. Acceptance: a scan test resolves
   every first-party call that reaches `MustRaise` to a catalog row and fails
   when the argument count disagrees with the row's arity or the code is not
   cataloged; a fixture calling `(*parser).raise` with three `diag.Arg`
   arguments and `diag.ErrCodeHyphenSeparator`, whose arity is 1, fails the
   test.
2. The scan fails closed. Acceptance: each of these fails the test rather than
   being skipped — a `MustRaise` call whose code argument is not a resolvable
   constant identifier; a call to a sixth variadic forwarder the registry does
   not name; a run that scanned no files; a run that did not see every package
   the registry names.
3. A `panic` whose nearest enclosing function declaration is not
   `Must`/`must`-prefixed fails the gate, naming file and line. Acceptance:
   `panic("x")` added to a plain function makes the test fail with that
   `file:line`; the same `panic` inside `func mustFoo()` does not; and the gate
   reports zero findings on the post-U1 tree, whose twelve panics all sit in the
   ten `Must`/`must` functions the Decisions list plus `diag.MustRaise`.
4. A `go` statement outside `src/common/spawn` fails the gate, naming file and
   line. Acceptance: `go func() { … }()` added to any non-test, non-`testdata`
   file under `src/` other than the helper's makes the test fail; the `go`
   statement at `src/common/spawn/spawn.go:84` does not.
5. The gate ignores `_test.go` files and any path with a `testdata` segment.
   Acceptance: a `panic` in a plain function in
   `src/protocol/yang/test/integration/testenv/testdata/gnmitarget/main.go`
   produces no finding, and neither does one in any `_test.go` file.
6. The predicates are unit-tested in isolation, so a finding is provable without
   a real violation in the tree. Acceptance: a table test feeds the placement
   predicate a `panic` in a `must` function (pass), in a plain function (fail),
   and at package level outside any function declaration (fail); and feeds the
   goroutine predicate a `go` in the helper's path (pass) and elsewhere (fail) —
   mirroring `TestProtoPathPolicy`.
7. `docs/code-style.md` states which halves the gate enforces and which stay
   review rules. Acceptance: the Panics section names the placement and
   goroutine-boundary checks as gated by `test/conformance/panic`, says clause
   2's boundary nuance and clause 3's caller documentation are enforced by
   review, says the gate does not see `sync.WaitGroup.Go`, and gives the
   ordering rule's review question — what does the joiner read.

## Out of scope

- `_test.go` files (a test that panics fails its own binary).
- Transitive obligation across module boundaries: the gate reasons about
  `go.aledante.io/FlowSeer` source only; a panic from a dependency is its
  contract.
- Wiring the gate into the Stop hook (`tools/hooks/stop-check.sh`,
  `.claude/settings.json`, `.codex/hooks.json`). Those are policy surfaces under
  AGENTS.md; the merge-gate Go test is the authority, and fast-feedback wiring
  is a separate guardrail-reviewed diff. It was asked for after the phase
  landed and is in `tools/hooks/stop-check.sh`: the hook's single layout call
  became a list of gate packages, and `.claude/settings.json` and
  `.codex/hooks.json` needed no change, because both already register the
  script. AGENTS.md's Enforced-rules paragraph names the gate.
- Clause 2 handling and clause 3 caller documentation, and the
  deferred-completion ordering rule — all review-enforced, per the Decisions.
- A test for `readGuarded` (`src/protocol/smi/load.go:297`). Phase 3 left it
  unproven for want of a fault seam in `readOne`; adding that seam is not this
  phase's work and the recommendation there still stands.
- Making `diag.MustRaise`'s panic unexpressible by generating one constructor
  per catalog row. Feasible — the catalog is closed at compile time and already
  validates `Arity == CountVerbs(Format)` — but it changes ~57 call sites and
  reshapes all five forwarders to take a built `Diagnostic`, which moves when a
  throttled line pays for interning its arguments
  (`internal/lex/lex.go:448`). A larger diff for the same clause-2 answer.

## Units

### U1. `diag.Raise` becomes `MustRaise` and gains its proof
Files: `src/protocol/smi/internal/diag/diagnostic.go`,
`src/protocol/smi/diagnostic.go`, `src/protocol/smi/doc.go`,
`src/protocol/smi/internal/diag/arity_scan_test.go` (new),
`src/protocol/smi/codes_test.go`, `src/protocol/smi/internal/diag/diagnostic_test.go`,
and the forwarders in `src/protocol/smi/internal/lex/lex.go`,
`src/protocol/smi/internal/frame/frame.go`,
`src/protocol/smi/internal/parse/recover.go` and `src/protocol/smi/resolve.go`
After: none
Change: `diag.Raise` and the public `smi.Raise` are renamed `MustRaise`, keeping
their bodies and their reasoning about why an uncataloged code and a wrong arity
are both parser bugs. `nargs` is assigned as
`uint8(min(len(args), catalog.MaxArgs))`. Each of the five forwarders documents
the inherited panic and names the proven answer, per clause 3; the four direct
constant-code sites need no comment, because their arguments are closed at the
call site. A new scan test walks non-test, non-`testdata` source from the
repository root, resolves every call reaching `MustRaise` — the four direct sites
and the 53 callers behind the registry's five forwarders — to a catalog row via
the two generated code files, and fails on an arity mismatch, an uncataloged
code, a call it cannot resolve, a run that scanned nothing, or a registry
package it never saw. No catalog row is added, so `zz_generated_codes.go`,
`COVERAGE.md` and `shipped-codes.txt` are untouched.
Tests: `arity_scan_test.go` — the four failure modes of requirement 2 and the
arity mismatch of requirement 1, each driven from a fixture source string and
watched failing first; the real tree passes. `diagnostic_test.go:207-239`'s
recover-based cases retarget `MustRaise` and keep asserting both panics.
`codes_test.go:109` calls the exported wrapper and stops compiling on the
rename, so it is in the unit: out of scope for the rule is not out of scope for
the build.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/smi`

### U2. The panic-placement and goroutine-boundary conformance gate
Files: `test/conformance/panic/panic_policy_test.go` (new)
After: U1
Change: a `package conformance` test walks every `.go` file under `src/` that is
not `_test.go` and has no `testdata` path segment, parses each with
`go/parser`, and reports a finding when a `panic` call's nearest enclosing
`*ast.FuncDecl` name does not match `^[Mm]ust`, or when an `*ast.GoStmt` appears
in a file whose directory is not exactly `src/common/spawn`. A `panic` or `go`
outside any function declaration —
package-level, or in an `init` — is a finding too. The predicates
`panicPlacementViolation` and `goStmtViolation` are separate functions the table
test drives, and the walk fails if it scanned no files. `repoRoot` is the
~15-line helper from `test/conformance/proto/layout_test.go:91`.
Tests: `panic_policy_test.go` — the walk reports zero findings on the tree (this
is the gate); a table test drives each predicate over synthetic sources, watched
failing first by feeding the passing fixture to the opposite expectation.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- test/conformance/panic`

### U3. Record the enforced and review-only halves in the style guide
Files: `docs/code-style.md`
After: none
Change: the Panics section gains a short paragraph saying the placement rule and
the goroutine boundary are gated by a conformance test under
`test/conformance/panic`; that clause 2's handling nuance and clause 3's caller
documentation are review rules, because they need a call graph and a judgment on
prose; that the benchmark-placement exemption is review-granted, as no site
claims it; and that the gate sees the `go` keyword, not `sync.WaitGroup.Go`. The
existing ordering-rule prose at `docs/code-style.md:327-345` gains one sentence:
it stays a review rule because the fact that separates a defect from a correct
site is what the joiner reads, and a reviewer should ask that question rather
than look for a deferred `Done`.
Tests: none — prose, governed by `docs/doc-style.md`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/code-style.md`

Waves: U1 U3 | U2

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/smi test/conformance/panic docs/code-style.md
go test -race ./test/conformance/panic/ ./src/protocol/smi/...
```

The gate is proved against the tree it is written for: it reports zero findings
on the post-U1 tree, and reports a finding for each acceptance example in
requirements 3, 4 and 5 added to a scratch file and removed again.

## Definition of done

- [ ] Verifier green for `src/protocol/smi`, `test/conformance/panic` and
      `docs/code-style.md`.
- [ ] `go test -race ./test/conformance/panic/` reports zero findings.
- [ ] Each acceptance example produces a finding when added and none when
      removed.
- [ ] `docs/code-style.md` names the gated halves, the review-only halves, the
      `WaitGroup.Go` blind spot, and the ordering rule's review question.
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
- Whether the two conformance gates should share a `repoRoot`. They are separate
  packages under `test/conformance/`, so sharing means a third package for a
  fifteen-line helper. Recommendation: copy it, and revisit if a third gate
  appears.
