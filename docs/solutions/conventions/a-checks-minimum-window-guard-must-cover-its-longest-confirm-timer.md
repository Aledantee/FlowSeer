---
title: A Check's Minimum-Window Guard Must Cover Its Longest Confirm Timer, or the Band Between Them Misreports
date: 2026-10-17
category: conventions
module: src/edge/netpen/attacks
problem_type: bug
component: netpen-check
severity: medium
applies_when:
  - "Writing or reviewing a netpen check that pre-reads, sustains a probe, then confirms on a protocol timer (a hold time, a dead interval) longer than the check's minimum-window guard"
  - "A check's final verdict switch ends in a default (resisted) branch after one or more timer-keyed confirm cases"
  - "Choosing a check's minimum-window guard constant against the timers its confirm branches wait on"
related_components: [netpen]
tags: [netpen, check-model, verdict, observation-window, pending, first-hop]
symptoms:
  - "A silent (deposed) peer is reported resisted with a detail claiming it still sends, for a window the guard admits"
  - "The confirm-by-silence branch can never fire for a window the minimum-window guard lets through"
root_cause: "The minimum-window guard admits windows shorter than the timer a confirm branch waits on, so that band falls through to the default verdict"
resolution_type: add-pending-case
---

The netpen check model says a behavior whose confirmation needs a protocol
timer longer than the window returns `pending` naming the window to pass
(the plan Decision below). The trap is that a check's minimum-window guard
is a separate, usually smaller constant than the timer its silence-confirm
branch waits on. The band between the two is reachable, and a final switch
that ends in a default verdict misreports every observation in it.

## What is true and why

`hsrp` guards on `hsrpMinWindow = 2 * hsrpHellotime` (6 s) but confirms a
deposed router by its silence for `hsrpHoldtime` (10 s):

```go
hsrpHellotime = 3 * time.Second            // hsrp.go:30
hsrpHoldtime  = 10 * time.Second           // hsrp.go:31
hsrpMinWindow = 2 * hsrpHellotime          // hsrp.go:32
...
if deps.Observe < hsrpMinWindow {          // hsrp.go:67
    return check.Pending(...)              // too short to run at all
}
```

For `--observe` in [6 s, 10 s) the guard passes, but a peer that falls
silent after the coup can never be observed quiet for the full 10 s hold
time. `lastSeen` is set on every peer frame, and `found` is non-nil only on
a Resign or non-Active hello, so on the `found == nil` path a stale
`lastSeen` means silence and a recent one means the peer is still active.
The original switch had only the hold-time confirm and a default resisted,
so a silent peer in that band fell to `resisted "still sends hellos in
state active"`, the opposite of what happened. The fix adds a case keyed
on one hello interval of silence that returns `pending` naming the window
to pass:

```go
case time.Since(lastSeen) >= hsrpHoldtime:   // hsrp.go:170  confirm: silent a full hold time
    return check.Confirmed(...), nil
case time.Since(lastSeen) >= hsrpHellotime:  // hsrp.go:172  pending: quiet but window too short
    return check.Pending(fmt.Sprintf("...the %s window is shorter than the %s hold time: pass --observe %s", deps.Observe, hsrpHoldtime, hsrpHellotime+hsrpHoldtime)), nil
}
return check.Resisted(...)                   // hsrp.go:175  only a peer seen within a hello interval
```

`glbp` had and got the same at `glbp.go` (`glbpHelloTime`/`glbpHoldTime`).

## How to apply it

When a check's confirm branch waits on a timer `T` and the check runs for a
window the runner supplies, either guard the minimum window at
`pre-read + T` so the default verdict is only reached once `T` could be
observed, or add a `pending`-with-window-detail case for the band
`[one interval, T)` before the default. Prefer the pending case: it keeps
the check runnable at shorter windows for the confirm paths that do not
need `T` (a Resign, a non-Active hello), and only the silence path waits
for `T`. The recommended window is `pre-read + T` (here `hsrpHellotime +
hsrpHoldtime` = 13 s).

The regression test feeds pre-read-active then silence with a window in the
band and asserts `pending`. Removing the new case drops it to `resisted`
and fails it (`attacks/fh/observe_test.go`, the HSRP and GLBP tables,
case "silence short of the hold time is pending").

## Evidence

- `src/edge/netpen/attacks/fh/hsrp.go:30-32,67-68,170-175`, the guard, the
  timers, and the three-way switch.
- `src/edge/netpen/attacks/fh/glbp.go`, the same shape for the gateway.
- The rule this applies: the plan Decision "A behavior whose confirmation
  needs a protocol timer longer than the window returns `pending` with a
  detail naming the window to pass"
  (`docs/plans/2026-10-12-1259-feat-netpen-check-model-phase2-plan.md`), and
  the verdict model in
  `docs/architecture/2026-10-12-netpen-check-model-direction.md`.

## What it does not cover

A check whose only confirm path is the timer (no Resign-style early
confirm) is better served by raising the minimum-window guard to
`pre-read + T`, since a pending band buys nothing. This is about the
first-hop sustain-and-silence checks. A check that confirms on a received
frame alone has no such band.
