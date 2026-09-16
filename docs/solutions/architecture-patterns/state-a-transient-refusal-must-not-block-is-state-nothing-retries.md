---
title: A State Change a Transient Refusal Blocks Is a State Change Nothing Retries
date: 2026-09-16
category: architecture-patterns
module: src/modules/localnet/access
problem_type: bug
component: audit
severity: high
symptoms:
  - "A mutation rests at POSSIBLY_APPLIED and INDETERMINATE forever on a device that may have been written to, and only an operator ends it"
  - "The edge logs no 'recovery started' for the mutation, while a passing run logs it within ~50ms of the dispatch stream opening"
  - "Central re-dispatches the sequence and the edge answers nothing, logging 'duplicate dispatch for an operation still running' each time"
  - "Central logs 'edge assertion rejected' with error.type edge/key-lookup-failed, or the hub logs 'Account fetch failed: account missing', shortly before the mutation stops moving"
  - "The edge's Subscribe call blocks in http2.(*ClientConn).roundTrip for minutes; the stream never reopens because central has nothing to send"
root_cause: "EnterRecovering delivered three audit records. The block and the RecoveryStarted wrote their state first and retained the record when delivery failed, so the recovery poll re-sent it. The phase transition alone was strict: a refused delivery returned with the phase unmoved, so Lane.enterRecovery saw !InRecovery(), started no poll, and nothing came back to it. A refusal that lasted a moment — central briefly unable to look up the edge's key after a restart — cost the mutation its recovery permanently."
resolution_type: code_fix
applies_when:
  - "Writing an audit-before-state rule: a record whose delivery must succeed before the state it describes is published"
  - "Reviewing a function that delivers several records around one state change, where some retain a failed record and others return on it"
  - "Deciding whether a caller that could not publish a fact may still act on it, when nothing schedules a second attempt"
  - "An operation rests in a non-terminal phase forever and the logs show a step that normally follows it never ran"
  - "A registry or dedupe table marks work in flight before it runs, and the re-dispatch that would restart it is answered as a duplicate"
  - "A test refuses one record of a sequence to prove a retry, and you are deciding which record to refuse"
related_components: [service_layer, messaging, testing_framework]
tags: [audit-trail, state-machine, transient-failure, retry, recovery, stranded-work, dedupe, flaky-test]
---

# A State Change a Transient Refusal Blocks Is a State Change Nothing Retries

## Situation

`Machine.EnterRecovering` (`src/modules/localnet/access/internal/mutation/machine.go`)
moves a mutation whose effect could not be established into `RECOVERING`, and
delivers three audit records doing it: the `PhaseTransitioned` record for the
move, the `LaneBlocked` record from `m.block`, and the `RecoveryStarted`
record. `Lane.enterRecovery` (`lane.go`) calls it, then starts the recovery
poll that is the only thing which will ever end the mutation.

The three records did not fail alike. `block` writes its state before
delivering and `EnterRecovering` retains a refused `RecoveryStarted`, both on
the reasoning the file states plainly: the poll follows the state, the records
stay owed, and the account catches up when the stream takes records again. The
transition was the exception, under the audit-before-state rule:

```go
// The phase moves only if its own record was delivered, which is
// the audit-before-state rule, and is why a failure here leaves nothing written:
// the mutation is not in recovery and a caller must not treat it as if
// it were.
```

## Guidance

That rule is right wherever the caller can still decline to act. Here it could
not: `enterRecovery` has already engaged the device's hold by the time it calls
`EnterRecovering`, and its failure path starts no poll —

```go
if err := m.EnterRecovering(ctx); err != nil && !m.InRecovery() {
	return stepOutcome{err: errs.Wrap(err, "enter recovery"), owed: true}
}
```

— so the mutation is left blocked, un-recovered, and unscheduled. Nothing
retries. The comment beside that call already named the outcome it was trying
to avoid, "it rests INDETERMINATE forever on a device that may have been
written to, and only an operator ends it", and applied that reasoning only to
the records after the first.

**Before making a delivery failure block a state change, ask what schedules the
second attempt.** If the answer is a poll, a timer, or a retry the state change
itself starts, then blocking the state change cancels the retry, and a failure
lasting one second produces an outcome lasting forever. Prefer publishing the
state and retaining the record as owed, which is what the neighbouring records
already did.

The trigger is ordinary. Central rejects the edge's assertion with
`edge/key-lookup-failed` for a moment after a restart, while it has not yet
restored the edge's key — the same window in which the hub logs
`Account fetch failed: account missing` (see
[a-restarted-hub-must-re-attach-every-persisted-edge-account.md](a-restarted-hub-must-re-attach-every-persisted-edge-account.md)).
Audit delivery fails inside that window and succeeds a moment later.

## Why This Matters

Nothing reports it. The edge's dispatch registry
(`src/edge/agent/internal/dispatch/registry.go`) marks the sequence admitted
*before* the lane runs it, so every re-dispatch central sends is answered as a
duplicate for an operation still running and deliberately met with silence —
"inventing one here would tell central something this edge does not know". The
two mechanisms are each correct and together they are a deadlock: central waits
for a report the edge will never make, and the edge waits for a recovery that
never started.

Downstream, the symptom points somewhere else entirely. With nothing to
dispatch, central never writes the first message of the `Subscribe` server
stream, so response headers are never flushed and the edge sits in
`http2.(*ClientConn).roundTrip` for minutes. A goroutine dump taken then shows
an edge blocked in its HTTP client, which reads as a transport defect and is
not one. No `ReadIdleTimeout` or `ResponseHeaderTimeout` is set anywhere in
`src/`, so that wait is unbounded — real, but a separate question from this
one, and fixing it here would only have converted a silent strand into a retry
loop around a mutation still not in recovery.

## Reading the Logs

One line separates the two cases, and it is worth knowing before opening any
code:

| Run | After `dispatch stream open` |
| --- | --- |
| Healthy | `recovery started`, within ~50ms; the stream reopens ~25s after a central restart and the lane is released |
| Stranded | no `recovery started` ever; the stream never reopens; the phase never leaves `POSSIBLY_APPLIED` |

`waitUntilResolved` in `src/services/device/test/integration/e2e_test.go`
samples the device's session count every 15s for exactly this reason: a count
that never rises means recovery never looked, as distinct from looking and
seeing nothing.

## Testing

Refuse the record the code does *not* already prove it survives.
`TestARefusedRecordCostsTheMutationNeitherItsPollNorItsAccount`
(`recovery_entry_test.go`) refused the `LaneBlocked` record and passed, because
that record is delivered after the phase has moved. The defect sat one record
earlier, in the only one of the three that could block the move, and a test
suite covering the other two read as covering the entry.

`TestARefusedRecoveryTransitionDoesNotStrandTheMutation` refuses the transition
instead and fails deterministically in 60s without the fix. Assert the poll
runs *while the refusal still stands*: asserting it after the stream recovers
passes on a poll that only started once delivery succeeded, which is the
behavior being ruled out.

The end-to-end case reproduces only under load — `go test -race` over the whole
tree, or a concurrent build — at roughly one run in three, rising to six in
eight with central at `LOG_LEVEL_DEBUG`. A test this shape passing once says
very little; the unit-level refusal is what makes the property checkable.

## Related

- `src/modules/localnet/access/README.md`: the module states the rule as
  audit-before-*release*, and already carries two state-before-record
  precedents — the hold rule and the firmware-epoch refresh.
- [a-restarted-hub-must-re-attach-every-persisted-edge-account.md](a-restarted-hub-must-re-attach-every-persisted-edge-account.md):
  the restart window this refusal falls in.
- [single-active-drainer-must-not-leak-its-context-or-its-shutdown-flag.md](single-active-drainer-must-not-leak-its-context-or-its-shutdown-flag.md):
  the same lane, and the same shape of defect — one caller's failure reaching
  work that should have outlived it.
