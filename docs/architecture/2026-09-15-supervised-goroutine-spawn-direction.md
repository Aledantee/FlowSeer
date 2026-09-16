---
title: Supervised Goroutine Spawn - Direction
type: direction
date: 2026-09-15
topic: supervised-goroutine-spawn
status: proposed-direction
---

# Supervised Goroutine Spawn - Direction

A panic inside a spawned goroutine does not reach the frame that spawned it.
Go unwinds that goroutine's stack and takes the process with it, so a recover
anywhere in the spawning call chain catches nothing. FlowSeer has 65 spawn
sites in non-test `src/` and roughly eight recovers, and two of those sites
already know the problem and solve it by hand.

`docs/code-style.md` states the rule — every goroutine running first-party work
is launched through a supervised spawn helper in `src/common` — and the helper
does not exist yet. This record settles what it is, because the answer binds
every goroutine written from here on, not just the 65 being converted.

## One package, beside the primitives it serves

The helper is its own package under `src/common/`, a sibling of `errs`, `pump`
and `service`. It is not part of any of them.

Not `src/common/service`: `service.Go` (`src/common/service/supervisor.go:555`)
is already the shape — it recovers through `callOwned` and turns the panic into
a `panicDiagnostic` carrying the module path, the boundary and a stack. But it
reads an `attemptCoordinator` out of the context and returns `errUnmanagedTask`
when there is none (`supervisor.go:559-562`). Every protocol library spawns
outside any module attempt: `snmp`, `gnmi`, `yang`, `netconf`, `ssh`, `syslog`
and `smi` together hold 22 of the 65 sites. Routing them through `service` means
either importing `src/common/service` into `src/protocol/*`, which inverts the
layer order `AGENTS.md` sets out, or a helper that silently does nothing there.

Not `src/common/pump`: `pump` imports `context` and `sync` and nothing else. A
spawn helper builds an error and reports it, which means `errs` and a logger or
tracer, and putting that inside the lowest primitive that SNMP, gNMI, YANG,
syslog and capture all embed pushes telemetry down a layer for no one's benefit.
`pump` is also a channel state machine with no notion of a unit of work: `Fail`
is the sink a pump-owning goroutine happens to choose, and only 10 of the 65
sites own a pump at all.

## The helper recovers and reports; it does not own the goroutine

It does one thing. It does not join, restart, back off, or cancel siblings.

Joining is already owned elsewhere. Thirteen sites spawn under a
`sync.WaitGroup` whose `Done` is the goroutine's first defer — a different set
from the thirteen below that write a shared slot, though the counts coincide —
and `src/common/pump/merge.go:26` already uses `sync.WaitGroup.Go`, the standard
library's spawn-with-join. A
helper that returned its own waiter would duplicate that at 13 sites and compete
with the standard library on the one axis where the standard library is fine. So
the helper composes with a `WaitGroup` rather than replacing it: the call site
keeps its `wg.Add(1)` and its `defer wg.Done()` inside the function it passes.

Restart and backoff belong to the supervisor that owns the work.
`src/common/service` has a restart policy with outcomes and exponential backoff;
a spawn helper that retried would turn one crash into a loop at 65 sites with
nobody deciding the policy.

## Reporting has a floor, and a sink when one exists

The 65 sites are not one population. Forty already have somewhere an error can
go — 10 call `pump.Fail`, 12 send on a one-shot channel, 13 write a shared slot
under a `WaitGroup`, 3 carry the error in-band as a `Frame{Err: …}` value, 2 set
a struct field. The other 25 have nowhere: nine are fire-and-forget loops, nine
are watchdogs that only cancel, and the rest are joiners with no failure mode
expressible as an error. No single sink covers a majority, and `pump.Fail` — the
largest — covers one site in six.

So the helper cannot own the sink, and it cannot assume one exists. It always
reports the panic through the observability path, which is the floor for the 25,
and it hands the error to a caller-supplied sink when there is one. Those 25
sites are where this change earns its keep: today a panic there takes the process
down and names nothing.

The reported value is a structured `errs` error carrying a caller-supplied label
and the recovered value as an attribute, in the house style
(`src/common/errs/builder.go:34`, `Attr`). `src/protocol/snmp/watcher.go:635-637`
already builds it that way; `src/protocol/smi/load.go:309` interpolates instead
and is the outlier, not the model. It carries a stack, as `newPanicDiagnostic`
(`src/common/service/supervisor.go:733`) does and as neither hand-written
recover does.

The label is caller-supplied because the two existing specimens disagree about
what a panic should be attributed to and only one of the two answers generalizes.
`watcher.go` names the goroutine — `"Watcher.run panicked"`. `readGuarded`
(`src/protocol/smi/load.go:297`) names the work item, the file being read, which
is what makes its message useful: "a malformed declaration costs that
declaration". A helper cannot derive the work item, so the caller passes it.

## What this costs

Reporting is a behavior change at every site, not a refactor. Today one of the
65 spawn sites reaches a log line close enough to see —
`src/services/device/internal/dispatchapi/relay.go:196` spawns `sweepAll`, which
logs at `:216` and `:222` — and none of the 65 obtains a span. After this, a
panic in any of them produces a log record that did not exist before, and a span
event where the context carries a recording span. The span is the conditional
half: the sites with no error sink are the fire-and-forget loops and watchdogs,
which are the least likely to hold one, so the log record is the floor. That is
the point of the change, but it means the conversion is reviewed against
`docs/conventions/observability.md` rather than waved through as mechanical.

The helper's own `go` statement becomes the only one in non-test `src/`, which is
what makes the rule checkable at all: a conformance test can look for a `go`
keyword outside one file, where it could never have judged sixty-odd inline
recovers.

One conflict is worth naming because this helper does *not* resolve it.
`docs/code-style.md`'s handling clause says first-party code may not name a
`src/common/service` recover as its handler, and today a first-party panic in a
goroutine under a module attempt is caught by `callOwned`, which is exactly that.
Routing the spawn through this helper does not change it: `callOwned` is invoked
from inside the goroutine body (`src/common/service/supervisor.go:577`, `:674`),
so its deferred recover is the innermost one and still fires first. Hoisting the
helper's recover above it would delete the path by which a panicking owned task
terminates its attempt (`supervisor.go:581`, `:675`) and disable restart-on-panic
for every module in the repository. The conflict is real and stays open; it is
one of the halves enforcement leaves to review.

## Alternatives

**An inline recover at every `go` statement.** Sixty-five separate decisions
about what reporting means, and nothing for a check to look for.

**Extend `service.Go` to work outside a module attempt.** It would have to
degrade to something when there is no coordinator, and the degraded path is the
one the protocol libraries would take — the majority of the callers on the
quietest branch.

**Put the helper in `pump`.** Fits 10 sites and pushes `errs` and telemetry into
the repository's lowest-level primitive for the other 55.
