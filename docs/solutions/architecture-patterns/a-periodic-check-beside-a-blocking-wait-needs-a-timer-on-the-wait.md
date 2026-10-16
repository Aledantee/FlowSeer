---
title: A Periodic Check Beside a Blocking Wait Needs a Timer on the Wait, or the Event It Watches Fires Inside the Wait
date: 2026-10-16
category: architecture-patterns
module: src/edge/agent/internal/drain
problem_type: bug
component: concurrency
severity: medium
symptoms:
  - "A discard warning for the only buffered record never fires while the drain waits for new data"
  - "On shutdown the last warning is missed because it was checked before the wait returned"
root_cause: "The drain checked the log's discard counters only between calls to seglog Next. Next waits until a segment is ready, and its linger timer is what ages the record out, so the discard happens while the caller is inside the wait."
resolution_type: code_fix
applies_when:
  - "Adding a periodic or change-driven check (a counter delta, a health probe) to a loop that blocks on a read with no deadline"
  - "The state being watched can change because of the wait itself, such as a timer inside the blocking call"
  - "Testing a check that depends on elapsed time inside a blocked call"
related_components: [edge-agent, seglog]
tags: [timeouts, blocking-wait, warning-signal, shutdown, synctest]
---

# A Periodic Check Beside a Blocking Wait Needs a Timer on the Wait

## Situation

`drain.run` warns when the log discards records the drain never delivered. It
read the counters after each `Next`. `Next` blocks on a timer sized to the
active segment's linger and returns only when a segment seals
(`src/edge/agent/internal/seglog/log.go:384-396`). A lone record that ages out
during that wait is discarded with the caller still inside `Next`, so the check
never ran.

## What is true

- A check placed between blocking calls sees only changes that happened
  before the call began.
- The fix gives each wait its own deadline and checks on expiry
  (`src/edge/agent/internal/drain/drain.go:147-158`): a child context of
  `maxBackoff` (30 s), and on `DeadlineExceeded` the loop calls
  `warnOnDiscards` and waits again. The own-context check runs first so a
  shutdown is not mistaken for a timeout.
- The final check runs after `run` returns, not only on its error path
  (`drain.go:115-117`).

## How to apply

Bound the wait by the longest staleness the check may have, and check on every
exit. Tests drive a synctest clock past the bound:

- `TestTheOnlyRecordAgingOutWarnsWhileNextWaits`
  (`src/edge/agent/internal/drain/drain_test.go:665`) failed with
  `warnings = 0, want 1` when the deadline branch skipped the check.
- `TestShutdownWarnsOnADiscardBeforeNextReturns` (`drain_test.go:692`) failed
  with `warnings before Run returns = 0, want 1` when the final check ran only
  on error.

The setup depends on the backoff schedule placing a retry inside the age
bound, so a schedule change fails the setup assertion in
`runOnlyRecordToAgeBound` first.

## Does not cover

A restarted `drain.Run` starts its discard baseline at zero, so its first
warning repeats the log's cumulative count. The comments in `drain.go` now say
so. Keeping the baseline across attempts is a separate change.
