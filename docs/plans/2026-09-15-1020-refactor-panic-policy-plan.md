---
title: Panics Named, Handled, and Documented - Plan
type: refactor
date: 2026-09-15
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
---

# Panics Named, Handled, and Documented - Plan

## Goal

Every panic in FlowSeer is named in the function that raises it, handled by
something that is not a boundary meant for other people's bugs, and documented
as far as its invariant travels. No panic reaches the top of a running service:
the restart that would follow is the last resort and is not an outcome this
codebase plans for. The means: state the rule in `docs/code-style.md`, convert
or delete the sites that break it, route every goroutine through one supervised
helper, and add a check that catches the next one.

This plan is wrong if the three clauses cannot all be enforced together — if
the enforcement phase finds that "handled" is undecidable statically, the rule
rests on review alone for its most important clause, and it should be narrowed
to what a check can hold before it is codified.

## Decisions

The rule has three clauses that all must hold, plus a boundary condition for
goroutines. An earlier draft stated them as alternatives and a review showed
that made the rule vacuous for every package that runs under
`src/common/service`; this shape is the correction.

### Clause 1 - placement

- A panic appears only inside a function whose name begins with `Must` or
  `must`, exported or not, or inside one carrying a comment naming a committed
  benchmark that measures what the error return costs. Why: the prefix is what
  a reader sees at the call site. `MustOID`, `MustChangeIndicator` and
  `Registry.MustRegister` already carry it.
- The benchmark is cited as a repository-relative path and a function name, not
  as "a benchmark in this module". Why: FlowSeer is five modules
  (`src/edge/netpen`, `src/protocol/snmp/bench`, `src/protocol/smi/bench`,
  `src/protocol/smi/differential`, `src/protocol/syslog/bench`), and benchmarks
  live in separate modules on purpose so bench-only dependencies stay out of
  the main graph. A same-module requirement would make the exemption
  unreachable at the one site expected to claim it — the `smi` parser is in the
  root module and `BenchmarkRecovery` is not.

### Clause 2 - handling

A panic must be handled, and only these three answers count:

- **Init-time.** The panic is evaluated during package initialization and
  depends only on values fixed at compile time. Why worded by behavior rather
  than by position: `var x = sync.OnceValue(func() … MustCompile(cfg.Pattern))`
  sits in a `var` block and panics at first use inside a serving process, and
  `var x = MustLoad(os.Getenv("…"))` fails only in the deployment whose
  environment is wrong — every replica of a rolling deploy at once, which is an
  outage. A closure stored at init and invoked later is a runtime panic.
- **Proven.** An earlier stage established the invariant with a check that runs
  on every `go test`, cited at the panicking site. Why the check must run
  rather than merely exist: a generation-time validation in one package does
  not re-verify a committed artifact in another, so the proof decays to "a
  check that ran once". The proof is a test over the artifacts the panic reads,
  not the generator that wrote them.
- **Foreign-code boundary.** A recover stands between the panic and the process
  top, and the panic is raised by code that boundary does not own — a
  caller-supplied handler, a gate probe, a third-party library. First-party
  code inside a boundary may not name that boundary as its handler. Why: the
  recover sites in `src/common/service/{delivery,supervisor,gate}.go` cover
  every module and service in the repository, so an alternative reading lets a
  `mustDecodeField` helper panic inside a NATS handler, be caught by
  `callHandler`, and satisfy the rule — while what landed is a throw/catch flow
  over a half-processed message, the shape `docs/code-style.md:26-27` bans by
  name. Those boundaries exist to contain other people's bugs, not ours.

### Clause 3 - documentation, and where it stops

- The function that can panic documents the panic and its invariant, and so
  does every caller up to the first that fixes the invariant. Why a doc comment
  on an unexported function, which `docs/code-style.md:44-47` otherwise makes
  optional: an inherited panic is a non-obvious contract, which is the
  exception that section already names. U1 amends that section in the same
  edit.
- The obligation stops at a caller that supplies literal or compile-time
  constant arguments, and a panic proven unreachable under clause 2 carries no
  caller obligation at all. Why: this is why `regexp.MustCompile`'s callers
  document nothing and are not being lax — the argument is closed at the call
  site, so the invariant never travels. Without this bound the obligation is
  unbounded for generated MIB code, where no caller handles anything and the
  first handler is the supervisor.

### The goroutine boundary

- A `go` statement resets handling. A panic inside a spawned goroutine is
  unhandled by definition, whatever recover its spawner sits under, because Go
  does not propagate it to the spawning frame. Why this is not a technicality:
  there are 64 `go` statements in non-test `src/` against roughly eight
  recovers, including `services/device/internal/{host,dispatchapi,deviceapi}`.
  `src/protocol/smi/load.go:305` states the reason in its own comment — "this
  is a goroutine, and a panic here takes the process down".
- Every goroutine running first-party work is launched through one supervised
  helper under `src/common/`, which recovers and reports the panic as a
  structured error for that unit of work. Why a helper rather than 64 inline
  recovers: one reviewed implementation instead of 64 decisions about what
  reporting means, and an enforcement check can test for the helper instead of
  pattern-matching recover shapes. `src/protocol/snmp/watcher.go:632` and
  `src/protocol/smi/load.go:305` are what it generalizes.
- `readGuarded` (`src/protocol/smi/load.go:305`) is sanctioned, and it is the
  goroutine clause working before the clause existed. It does not license the
  parser bailout it catches: that is first-party code naming a boundary as its
  handler, which clause 2 refuses, so phase 3 still converts it.

### Remedies

- The sanctioned remedies are an error return, deleting a branch the caller's
  contract makes unreachable, and hoisting the evaluation to init time. Adding
  a new recover boundary is not one. Why deletion is listed: `newRing`'s
  non-positive limit is an error no caller can provoke, and
  `docs/code-style.md:38-40` bans handling errors that cannot occur — an error
  return there adds a resource-leak path on a branch that never executes.
  Deletion is conditioned on the same "proof that runs" standard as clause 2.
- A conversion that replaces a panic with a status the caller must ask for
  carries the caller-side assertion in the same change. Why: `errcheck` cannot
  see it. A sticky `Err()` field returns nothing, so a caller that never asks
  sees a run that ended exactly like a run that finished — and
  `fabric.Run` (`src/common/netsim/fabric/run.go:926`) is already that caller.

### What this retires

- "A panic that crosses a package boundary is a bug"
  (`docs/code-style.md:147-148`) is retired deliberately, not dropped in an
  edit. Why: `snmp.MustOID` panics into every generated MIB package and
  `catalog.MustRegister` into `registrations.go`, both by design. The successor
  ban is on *unnamed* panics crossing a package boundary.

### Scope and sequencing

- Pre-1.0 breaking changes land without shims, per `AGENTS.md`.
- A recover site that contains foreign code is untouched:
  `src/common/service/{delivery,supervisor,gate}.go` and
  `src/protocol/snmp/watcher.go`.
- Enforcement lands last. A check that flags a panic outside the sanctioned
  shapes fires on every file still awaiting conversion.

## Requirements

1. `docs/code-style.md` states the three clauses, the goroutine boundary, the
   sanctioned remedies, and the retirement of the cross-package sentence.
   Acceptance: a reader who greps it for `panic` finds all three clauses, the
   sentence that a `go` statement resets handling, and the sentence that adding
   a recover boundary is not a remedy.
2. No first-party panic names a `src/common/service` recover as its handler.
   Acceptance: a `must`-prefixed helper panicking inside a NATS handler is a
   violation even though `callHandler` (`src/common/service/delivery.go:509`)
   catches it.
3. Every goroutine running first-party work is launched through the supervised
   helper. Acceptance: a bare `go func() { … }()` in non-test `src/` is a
   violation; `src/protocol/snmp/watcher.go:632` and
   `src/protocol/smi/load.go:305` are the behavior it generalizes.
4. Every surviving panic satisfies all three clauses, and each documents which
   clause-2 answer it relies on. Acceptance: `MustOID` in generated MIB code
   cites the proof; `catalog.MustRegister` cites init-time evaluation.
5. `errs.NewCode` stops panicking and keeps its name and signature; the
   repo-wide scan gains the coverage the panic held. Acceptance:
   `NewCode("bad name!")` returns the code without panicking, and the scan
   fails on that literal.
6. `diag.Raise` keeps its panic as `diag.MustRaise` and gains a proof.
   Acceptance: a source-scan test reads every `MustRaise` call site, checks the
   argument count against the catalog row's arity, and fails on a mismatch —
   so the panic is unreachable by clause 2's proven answer.
7. `catalog.Register` returns an error and `catalog.MustRegister` is the
   init-time companion. Acceptance: `Register` with an empty `Name` returns a
   non-nil error and the catalog is unchanged.
8. `newRing`'s non-positive-limit branch is deleted rather than converted.
   Acceptance: no caller can supply a non-positive limit, and `newRing` neither
   panics nor returns an error.
9. `mibgen` validates every OID once, before any emitter runs. Acceptance: a
   resolved tree carrying an OID whose first arc is 3 fails generation, and
   `newOIDCall` keeps its `*jen.Statement` signature.
10. `mibgen`'s decoder variant is resolved before the `resolved` value carrying
    `DecodeFunc` is built. Acceptance: an unregistered variant fails generation
    and `DecodeFunc` keeps its `func() *jen.Statement` type.
11. `fabric.scheduleDequeue` and `vswitch.mustSetOperStatus` record a sticky
    fault instead of panicking, and each gains a caller-side assertion.
    Acceptance: `fabric.Run` fails its own test when a fault is recorded and
    never read.
12. Each `harvest_*.go` generator exits through `run() error`; `mustMAC` stays.
    Acceptance: `go run harvest_l2.go` against an unwritable directory prints
    one error line and exits non-zero without a stack trace.
13. The `smi` parser's bailout is converted; `readGuarded` stays. Acceptance:
    no `panic` or `recover` remains in `src/protocol/smi/internal/parse`.
14. A check enforces clauses 1 and 2 and the goroutine boundary, and says in
    `docs/code-style.md` whichever part it cannot cover. Acceptance: a bare
    `go func()` and a first-party panic under a service boundary both fail it.

## Out of scope

- The recover sites in `src/common/service` and `src/protocol/snmp/watcher.go`.
  They catch; they do not raise.
- `panic` in `_test.go` files. A test that panics fails the test binary, which
  is the outcome either way.
- Converting the existing `Must` functions to non-panicking forms. They are
  the sanctioned shape, not violations.

## Units

### U1. Phase 1 - the rule, and the conversions that are decided
Files: `docs/plans/2026-09-15-1020-refactor-panic-policy-phase1-plan.md`
After: none
Change: `docs/code-style.md` carries the rule; `errs`, `diag`, `catalog`,
`ssh`, `mibgen`, `fabric`, `vswitch` and the harvest scripts satisfy it.
Landed: Partially. The rule and every conversion landed except `diag.Raise` →
`MustRaise` (phase-1 U3), which is blocked pending a re-plan: it reaches the
public `smi.Raise` and its static arity-scan proof cannot resolve the variadic
forwarders. `errs`, `catalog`, `ssh`, `mibgen` (decoder-variant and OID
pre-pass), `fabric`, `vswitch` and the four harvest scripts all satisfy the
rule; the surviving `Must` functions cite their clause-2 answers. See the
phase-1 plan.

### U2. Phase 4 - the goroutine helper and the 64-site audit
Files: `docs/plans/2026-09-15-1020-refactor-panic-policy-phase4-plan.md`
After: U1
Change: a supervised-spawn helper lands under `src/common/`, and every
goroutine in non-test `src/` runs through it.
Landed:

### U3. Phase 2 - the smi parser's unwinding
Files: `docs/plans/2026-09-15-1020-refactor-panic-policy-phase2-plan.md`
After: U1
Change: the parser unwinds by flag-checked return rather than by panic.
Landed: Yes. `panic(bailout{})` and its recover are gone; the parser unwinds by
the `p.fatal` flag. `benchstat` showed no regression (a small improvement from
dropping the per-declaration recover-defer). See the phase-2 plan.

### U4. Phase 3 - enforcement
Files: `docs/plans/2026-09-15-1020-refactor-panic-policy-phase3-plan.md`
After: U1, U2, U3
Change: a repository check flags a new violation. Staged for guardrail review,
not landed by an agent.
Landed:

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>
go test -race ./...
go test -run '^$' -bench 'BenchmarkRecovery|BenchmarkParseFile' ./src/protocol/smi/bench/
```

The repo-wide check that the rule holds, once phase 3 lands:

```bash
tools/hooks/panic-policy.sh
```

## Definition of done

- [ ] Verifier green for every changed path in every phase, run per module —
      `src/edge/netpen`, `src/protocol/smi/bench`, `src/protocol/snmp/bench`,
      `src/protocol/smi/differential` and `src/protocol/syslog/bench` do not
      build from the root, so a root `go test ./...` does not reach U4 or U8.
- [ ] `docs/code-style.md` updated in the same change as the first conversion.
- [ ] Every phase plan reads `implemented`, and its `Landed:` line here is
      filled.
- [ ] `tools/hooks/panic-policy.sh` staged with a diff for guardrail review,
      not committed by an agent.
- [ ] This plan's `status` set with an outcome note under its title.
- [ ] No plan labels (U1, R3) in code, comments, or commit messages.

## Open questions

- Whether any site survives as a measured hot-path exemption. The plan assumes
  none does outside the `smi` parser, and phase 2 is the only place the
  question is asked with a benchmark rather than an opinion.
- Whether the enforcement check belongs in `tools/hooks/` as a Stop hook or in
  `verify-change.sh` as a diff-aware gate. Phase 3 decides; the Stop hook is
  the recommendation because the rule is repo-wide rather than diff-local.
