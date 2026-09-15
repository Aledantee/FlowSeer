---
title: Panic Policy Phase 3 - The Goroutine Boundary - Plan
type: refactor
date: 2026-09-15
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: code
parent: docs/plans/2026-09-15-1020-refactor-panic-policy-plan.md
---

# Panic Policy Phase 3 - The Goroutine Boundary - Plan

## Goal

A panic inside a spawned goroutine stops that unit of work and is reported,
instead of taking the process down. The means: one supervised-spawn package
under `src/common/`, and every `go` statement in non-test `src/` routed through
it, so the only `go` keyword left outside tests is the one inside the helper.

This phase is wrong if the helper cannot carry what the call sites need. The 65
sites do different things, and a helper that fits only the simplest would be
adopted at the easy sites and worked around at the rest, which is worse than
none. The census under Decisions is what rules that out: the helper is small
because the population is not one shape, not because the easy sites were the
only ones counted.

## Decisions

The parent's Decisions govern, and
[Supervised Goroutine Spawn](../architecture/2026-09-15-supervised-goroutine-spawn-direction.md)
holds the reasoning that outlives this phase — where the package sits, why it is
not part of `service` or `pump`, why it does not join or restart, and why
reporting has a floor. Read it before U1. It is `proposed-direction` and needs a
person's acceptance; the phase does not wait on that, but a rejection re-opens
U1.

What this phase settles:

- **The population is 65 sites, not 64.** The grep in the parent's Verification
  misses `src/common/pump/merge.go:26`, `forwarders.Go(func() {` — a
  `sync.WaitGroup.Go` spawn with no `go` keyword. Both spellings convert. The
  enforcement gate in phase 4 sees only the `go` keyword, so this one would have
  passed a green gate unconverted; that is why it is named here rather than left
  to the implementer to notice.

- **This phase stays one plan at eight units.** It is over the six-unit line,
  and `references/phases.md` sends that to a cluster check: every migration unit
  depends on U1 and on nothing else, so there is one cluster. Its worked example
  splits a nine-unit plan whose units divide between `src/protocol/smi` and
  `src/protocol/snmp`, which looks like this plan and is not: there the two
  groups differ in what they do, and here U2 through U7 are the same conversion
  applied to different directories, deriving everything they need from U1 and
  nothing from each other. Splitting them would produce phases that differ only
  by package name. They are `After: U1` and independent, so `implement` may run
  them in parallel.

- **The helper's signature.** One package `src/common/spawn`, one entry point:

  ```go
  // Go runs fn in a new goroutine, recovering a panic and reporting it as a
  // structured error labelled by the unit of work fn performs.
  func Go(ctx context.Context, label string, fn func(), opts ...Option)

  // ReportTo also hands the recovered error to sink.
  func ReportTo(sink func(error)) Option
  ```

  Why a bare `func()` and not `func(ctx) error`: 25 of the 65 sites have no
  failure an error could describe, and the 40 that do already disagree about
  where it goes. Why an option rather than a fourth parameter or two functions:
  40 sites pass a sink and 25 do not, and a nil callback in the majority-case
  signature reads as an oversight at every one of them.

  Why the caller supplies the label: the two hand-written recovers this
  generalizes attribute differently, and only one answer generalizes.
  `src/protocol/snmp/watcher.go:631` names the goroutine; `readGuarded`
  (`src/protocol/smi/load.go:297`) names the file being read, which is what makes
  its message useful. The helper cannot derive a work item.

- **The helper composes with `sync.WaitGroup`; it does not join.** Thirteen
  sites spawn under a `WaitGroup` whose `Done` is the goroutine's first defer.
  The call site keeps `wg.Add(1)` and keeps `defer wg.Done()` inside the `fn` it
  passes.

- **A converted site completes its rendezvous on the panic path, or it hangs.**
  This is the conversion's one way to do real harm, and it is not visible at the
  call site. `src/common/service/service.go:117` sends on `done` and the
  supervisor closes `started`, which `service.go:118` then receives; today a
  panic before either kills the process and a supervisor restarts it. Recovered
  instead, with the send skipped, `service.Run` blocks on `<-started` forever
  with no context escape — an unkillable hung service whose only signal is a log
  line. So every converted site whose body completes a send or a `close` moves
  that completion into a `defer` inside `fn`, or supplies a `ReportTo` sink that
  completes it. Each migration unit names the rendezvous sites it converted and
  tests one of them with a panicking body.

- **A per-work-item recover inside a loop is a separate, sanctioned shape.** The
  helper recovers at the goroutine's outermost frame, which is the wrong grain
  where one goroutine processes many items. `readGuarded`
  (`src/protocol/smi/load.go:297`) is called once per file from inside the
  worker loop (`load.go:263`), and its recover costs one file rather than the
  worker. Moved out to the helper it would abandon the worker's remaining files
  and leave the unbuffered `jobs` producer (`load.go:268-270`) blocked forever,
  since nothing closes `jobs` from the consumer side — a hang in place of a
  crash. Per-item recovers stay where they are; the helper covers the `go`
  statement above them. Phase 4's gate checks placement, not recovers, so it
  does not object.

- **`fn` takes no parameters, so a site that passes one takes a local copy.**
  `supervisor.go:399,410` spawn `go func(done chan struct{}) { … }(slot.done)`
  to avoid capturing `slot`, which is reassigned at `supervisor.go:383` and
  `:430`. A `func()` closure must therefore open with `done := slot.done` before
  the `spawn.Go` call — the same snapshot, written at the call site. Capturing
  `slot` directly is the bug the parameter was there to prevent.

- **A site with no `context.Context` in scope threads one in.** Four do:
  `src/protocol/smi/load.go:261` (`readWave`), `src/protocol/netconf/session.go:191`
  (`NewSession`), `src/edge/netpen/attacks/routing/wpad.go:141` (`startWPADProxy`)
  and `src/protocol/snmp/trap_listen.go:155,156` (`(*listener).start`, which
  holds a `logCtx` field). The units may break those signatures, exported ones
  included — pre-1.0, per AGENTS.md. `context.Background()` is the fallback only
  where threading a context would reach past the package, and a site that takes
  it says why in a comment, because the helper's report loses its correlation
  there.

- **`service.Go` keeps its name and signature and launches through the helper.**
  `src/common/service/supervisor.go:555` is the existing shape but is unusable
  outside a module attempt (`errUnmanagedTask`, `:559-562`), so it becomes a
  caller of `spawn.Go` rather than the helper.

  It does not resolve the clause-2 conflict at `callOwned`, and no unit here
  tries to. `callOwned` (`supervisor.go:707`) is invoked from inside the
  goroutine body — `supervisor.go:577`, `:674` — so its deferred recover is
  innermost and still fires first for a first-party panic under a module
  attempt. Hoisting `spawn`'s recover above it would delete the path by which a
  panicking owned task terminates its attempt (`supervisor.go:581`, `:675`) and
  silently disable restart-on-panic for every module in the repository. So the
  conflict stands, and it is one of the review-only halves phase 4 records.

- **Reporting is a behavior change, and the conversion is reviewed as one.** One
  of the 65 bodies logs today (`src/services/device/internal/dispatchapi/relay.go:216`)
  and none opens a span. Every unit reads `docs/conventions/observability.md`
  for the record and event it emits; none of this is mechanical.

- **The three sites that swallow an error keep swallowing it, visibly.**
  `src/modules/edgebus/forwarder.go:159` (`_ = f.discover(ctx)`),
  `src/edge/netpen/attacks/routing/wpad.go:141` (`_ = srv.Serve(ln)`) and
  `src/modules/localnet/access/lane.go:1770` (`_ = open.machine.DeliverOwed`)
  are out of scope as error handling; this phase only stops their panics from
  killing the process. Converting a swallow is a separate judgment about each
  caller's contract and does not belong in a 65-site sweep.

## Requirements

Parent requirement 3 lands here.

1. `spawn.Go` recovers a panic in the goroutine it starts, and the process
   survives. Acceptance: a test whose `fn` panics observes the reported error
   and a still-running process; `go test -race` is clean.
2. The reported value is a structured `errs` error carrying the label and the
   recovered value as an attribute, plus a stack. Acceptance: `errs.Attributes`
   on the reported error yields the recovered value under a stable key, and the
   error's message names the label. A panic whose value is already an `error`
   is wrapped, not stringified.
3. A site with a sink receives the error there. Acceptance:
   `spawn.Go(ctx, "x", fn, spawn.ReportTo(p.Fail))` with a panicking `fn` leaves
   `p.Err()` non-nil.
4. A site with no sink still reports. Acceptance: `spawn.Go` with no `ReportTo`
   and a panicking `fn` emits one log record at error level, per
   `docs/conventions/observability.md`. It also adds one span event when the
   passed context carries a recording span, and does not when it does not — the
   25 no-sink sites are the fire-and-forget loops and watchdogs, which are the
   least likely to hold one. The log record is the floor; the span event is
   conditional and tested both ways.
5. The helper does not join. Acceptance: `spawn.Go` returns before `fn`
   completes, and a call site's own `sync.WaitGroup` still governs the join —
   proved by a test that would deadlock if the helper waited.
6. `src/protocol/snmp/watcher.go`'s hand-written recover moves onto the helper
   without losing its attribution. Acceptance: a panic in the watch loop still
   surfaces through `Watcher.Err()`, and `Err()` is already set when the data
   channel closes — not merely eventually. `readGuarded`
   (`src/protocol/smi/load.go:297`) keeps its own recover, per Decisions: it is
   per-file, not per-goroutine.
7. The `go`-keyword count in non-test `src/` outside the helper reaches zero, and
   so does the `WaitGroup.Go` count. Acceptance:

   ```bash
   grep -rn --include='*.go' -E '^[[:space:]]*go[[:space:]]' src/ | grep -v '_test.go'
   grep -rn --include='*.go' -E '\.Go\(func' src/ | grep -v '_test.go'
   ```

   The first prints exactly one line, in `src/common/spawn`. The second prints
   nothing: `spawn.Go` takes a context first, so the helper's own call does not
   match it. Both patterns are load-bearing — an earlier draft of this plan
   anchored the first on `^` alone and it matched one doc comment and no spawn
   at all, which would have read as a clean tree before any conversion.

## Out of scope

- `_test.go` goroutines.
- The recover sites that contain foreign code:
  `src/common/service/{delivery,supervisor,gate}.go`. They are the parent's
  clause-2 foreign-code answer and are not goroutine boundaries.
- Restart, backoff, and sibling cancellation. The supervisor owns those.
- Fixing the three deliberate error swallows, per Decisions.
- The enforcement gate that checks requirement 7 mechanically. That is phase 4.

## Units

### U1. The `spawn` package
Files: `src/common/spawn/spawn.go`, `src/common/spawn/spawn_test.go`,
`src/common/spawn/README.md` (all new)
After: none
Change: `spawn.Go` starts `fn` in one goroutine — the package's only `go`
statement and, once the migration units land, the repository's. It recovers,
builds the structured error of requirement 2, reports it per
`docs/conventions/observability.md`, and passes it to a `ReportTo` sink when one
is given. `newPanicDiagnostic` (`src/common/service/supervisor.go:733`) is the
model for what the error carries; `src/protocol/snmp/watcher.go:635-637` is the
model for the `error`-vs-value branch.
Where the logger comes from is U1's to settle and is not decided here: the
repository has no context-carried logger helper outside `src/common/service`, and
`spawn` cannot import that package without inverting the layer order. `slog`'s
default is the likely answer; whatever U1 picks, it records why in the package
README.
Tests: `spawn_test.go` — requirements 1 through 5, each watched failing first.
Requirement 1's test is evidence only once it has been seen failing against a
helper with no recover, and requirement 5's only once it has been seen deadlock
against a helper that waits. Assert the reported error, not the absence of a
crash: a process that did not die proves nothing about which branch ran.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/spawn`

### U2. `src/common/service` and `src/common/pump`
Files: `src/common/service/{supervisor,service,delivery,bus}.go`,
`src/common/pump/merge.go`
After: U1
Change: 11 service sites and 2 pump sites route through `spawn.Go`.
`service.Go` keeps its signature and launches through the helper.
`supervisor.go:399,410` take `done := slot.done` before the call in place of the
closure parameter, per Decisions. `merge.go:26`'s `forwarders.Go` becomes
`spawn.Go` with the `WaitGroup` managed at the call site — the 65th site, and
the one no `go`-keyword grep finds.
`service.go:117` is the rendezvous case the Decisions name; it converts with its
send in a `defer` inside `fn`.
Tests: the packages' existing suites, plus one case per package proving a panic
in a converted goroutine surfaces as that unit's failure rather than a crash, and
one proving `service.Run` returns rather than blocking on `<-started` when the
supervisor goroutine panics before `close(started)`. That last test is evidence
only once it has been watched hanging against a conversion without the defer.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/service src/common/pump`

### U3. `src/protocol/snmp`
Files: `src/protocol/snmp/{watcher,walker,rawwalk,trap_listen,trap,reactor}.go`,
`src/protocol/snmp/test/integration/waiter.go`
After: U1
Change: 8 sites convert. `watcher.go:622-641`'s hand-written recover is deleted
and its behavior comes from the helper plus `ReportTo(w.pump.Fail)`. Ordering is
the trap: `watcher.go:628` defers `CloseData` and `:631` defers the recover, so
today `Fail` records the error before the channel closes, and a bare move
reverses that — a consumer whose `Recv` returned false could read `Err() == nil`.
The sink must run before `CloseData`, so `CloseData` moves into the sink or into
a thin defer that calls `Fail` first.
`Walker.Pump` (`walker.go:139-147`) and `RawWalker.Pump` (`rawwalk.go:255-260`),
whose bodies are a single `go` statement, become single `spawn.Go` calls, each
passing `ReportTo(w.Fail)`. Without the sink a panic in `fn` closes the channel
with `Err() == nil`, which a consumer cannot tell from a walk that finished —
the silent status the parent's Remedies section bans.
Tests: the package suite; a panicking decode path still reaches `Watcher.Err()`
before the channel closes, and a panicking `fn` leaves `Walker.Err()` non-nil.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/snmp`

### U4. The rest of `src/protocol`
Files: `src/protocol/{syslog,ssh,gnmi,yang,netconf}/*.go`,
`src/protocol/smi/load.go`
After: U1
Change: 15 sites convert. `readGuarded` (`load.go:297`) is untouched — it is a
per-item recover, not the goroutine's, per Decisions. What converts in `load.go`
is the `go` statement at `:261`; its comment gains a line saying the helper
covers the worker and `readGuarded` covers each file, so the next reader does not
delete one as redundant with the other.
`netconf/session.go:191` is one of the four sites with no context in scope.
Tests: the package suites. This unit's plan claimed `load.go` had existing
coverage that a panic reading one file costs that file and not the wave, and
that it must still pass unchanged. It does not exist: no test in `src/protocol/smi`
drives a panic through `readGuarded`'s recover. `corpus_test.go:657-670` recovers
in the test body, which is the corpus runner protecting itself, not coverage of
`readGuarded`. Phase 2 removed the parser's `panic(bailout{})`, so no reachable
input panics there any more and a test would need an injected fault. `readGuarded`
is therefore unproven, not proven — recorded in Open questions rather than papered
over with a synthetic test that would prove only that `recover` recovers.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/syslog src/protocol/ssh src/protocol/gnmi src/protocol/yang src/protocol/netconf src/protocol/smi`

### U5. `src/modules`
Files: `src/modules/capture/{engine.go,rawsocket/*.go}`,
`src/modules/localnet/access/{lane.go,internal/freeze/freeze.go,internal/credential/connect_adapter.go}`,
`src/modules/edgebus/{forwarder,receiver}.go`
After: U1
Change: 14 sites convert. `receiver.go:68`'s unsynchronized `r.err = err` is
written through the sink instead, which is where the data race it looks like
today gets fixed or proved absent; say which in the commit.
Tests: the package suites, with `-race` carrying the `receiver.go` question.
`src/modules/localnet/access` is a module, so its exported `Config` keeps its
out-of-directory test.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/capture src/modules/localnet src/modules/edgebus`

### U6. `src/services/device` and `src/edge/agent`
Files: `src/services/device/internal/{host,dispatchapi,deviceapi}/*.go`,
`src/edge/agent/{host/host.go,internal/dispatch/demux.go}`
After: U1
Change: 5 sites convert. `relay.go:216,222` is the one site that already logs;
its log record stays and the helper's is additional, not a replacement.
Tests: the package suites.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device src/edge/agent`

### U7. `src/edge/netpen`
Files: `src/edge/netpen/{runner,link,full}/*.go`,
`src/edge/netpen/cmd/netpen/*.go`, `src/edge/netpen/attacks/routing/wpad.go`,
`src/edge/netpen/attacks/testtest/leg.go`
After: U1
Change: 10 sites convert — `runner` 3, `cmd/netpen` 3, `link` 1, `full` 1,
`attacks/routing` 1, `attacks/testtest` 1. `wpad.go:141` is one of the four
sites with no context in scope. `netpen` is a separate Go module, so it takes the
helper as a module dependency; check that `go.mod` resolves before converting.
Tests: the module's own suite, run from `src/edge/netpen`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/netpen`

### U8. Name the package in the style guide
Files: `docs/code-style.md`
After: U1
Change: "the supervised spawn helper in `src/common`"
(`docs/code-style.md:315-317`) names `src/common/spawn` and its entry point, and
the section says the helper does not join or restart so a reader does not expect
it to. Cite the direction record.
Tests: none — prose, governed by `docs/doc-style.md`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/code-style.md`

## Verification

```bash
go test -race ./...
cd src/edge/netpen && go test -race ./...
grep -rn --include='*.go' -E '^[[:space:]]*go[[:space:]]' src/ | grep -v '_test.go'
grep -rn --include='*.go' -E '\.Go\(func' src/ | grep -v '_test.go'
```

The first grep must print one line, in `src/common/spawn`; the second nothing.
Run both against the tree before converting anything: they must print 64 lines
and 1 respectively, or the patterns are wrong and the gate they stand for is
vacuous. `-race` matters more
here than elsewhere: routing a goroutine through a helper changes when its
closure captures, and a migration that introduces a data race is the likeliest
way this phase does harm.

## Definition of done

- [ ] Verifier green for every changed path, per module.
- [ ] Both greps in Verification print what Verification says they print, and
      printed 64 and 1 before the first conversion.
- [ ] A panic in a helper-launched goroutine is reported and the process
      survives, proved by a test watched failing first.
- [ ] `watcher.go` and `load.go` are on the helper with their attribution intact.
- [ ] `docs/code-style.md` names the package; the direction record is cited.
- [ ] This plan's `status` set with an outcome note under its title, and
      `U3. Landed:` filled in the parent.
- [ ] No plan labels in code, comments, or commit messages.

## Open questions

- Whether `spawn.Go`'s report belongs at error level unconditionally. A watchdog
  that panics during shutdown would be noise. Recommendation: unconditional for
  now — 25 sites have no other signal, and a level policy with no caller asking
  for it is the mechanism Principle 5 refuses.
- `src/modules/edgebus/receiver.go:68` writes `r.err` with no visible
  synchronization against its reader. U5 either fixes it through the sink or
  shows it is ordered by the `<-r.done` receive. It was observed, not confirmed
  as a race; `-race` on the converted test decides it.
- Whether `src/edge/netpen`, a separate module, should depend on `src/common/spawn`
  at all, or keep an inline recover and be exempted by the phase-4 gate.
  Recommendation: depend on it — netpen already imports `src/common`, and an
  exemption is a hole in the only mechanically checkable half of the rule.
- `readGuarded` (`src/protocol/smi/load.go:297`) has no test. This plan assumed
  one existed; it does not, and phase 2 removed the only input that reached it.
  The Decisions here rest on its recover being the right grain, so the claim is
  currently unproven. Proving it needs an injected fault in `readOne` — a seam
  this phase did not want and did not add. Recommendation: leave it, and let
  whoever next needs a fault seam in `smi` add the test with it.
- The ordering rule the Decisions state for `snmp/watcher.go` applies to every
  site whose `fn` defers a completion and passes a sink, not just that one. It
  was found again in `pump.Merge` (the forwarder reported `nil` on a panic and
  the coordinator read that as a clean source), at four sites in `gnmi` and
  `yang`, and at both `snmp` walkers — whose doc comments asserted the sink
  already prevented it. Five packages, three of them commented wrong. The rule
  belongs in `docs/code-style.md` beside the spawn rule, or in `spawn`'s README,
  so the next caller does not rediscover it. Phase 4 should decide whether it is
  checkable; "a reviewer notices" has now failed more often than it has held.

  A `sync.WaitGroup` join is not the exemption it looks like. `wg.Done` as
  `fn`'s defer runs before the recover, so a joiner that does `wg.Wait()` and
  then reads what the sink writes reads it too early: `src/edge/agent/host`
  returned `nil` for a panicking loop, and `dispatch.Demux` could drop a
  refusal report at shutdown. Worse, in `capture/rawsocket`'s mirror source the
  joiner closes the channel the sink sends on, and a send on a closed channel
  panics inside the recover where nothing catches it — the conversion turned a
  recovered panic into a process crash. Where the joiner consumes what the sink
  produces, the join has to be a counted receive rather than a `WaitGroup`, or
  the site must not report in band at all.
