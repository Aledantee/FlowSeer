---
title: What Releases or Classifies State in Another Service Is That Service's Condition, Not the Nearest Thing the Sender Holds
date: 2026-09-09
category: architecture-patterns
module: src/edge/agent
problem_type: bug
component: service_layer
severity: critical
symptoms:
  - "A device operation is applied twice although a per-sequence duplicate guard exists and its unit tests pass"
  - "A guard's own doc names a precondition ('once it is terminal') that no caller establishes, and nothing fails"
  - "A receiver disposes work terminally, or retries it forever, citing a cause that never occurred"
  - "Both sides pass their own tests; the defect is only visible by reading the other service"
root_cause: "A signal crossing a service boundary was given the meaning the sender had available rather than the meaning the receiver branches on: a release callback fired on 'the report was accepted' where the receiver needed 'the operation ended', and a required error-code field was filled with a peer's code whose disposition the receiver resolves against its registry."
resolution_type: code_fix
applies_when:
  - "One component hands another a callback that releases state — Confirmer, Acker, OnDone, a completion channel — and the two track different conditions"
  - "Writing or reviewing a doc comment that names a precondition ('once it is terminal', 'after the last write') which the caller, not this function, has to establish"
  - "A required, pattern-constrained wire field must be filled for a failure the sender could not classify, and the nearest existing value belongs to a peer"
  - "Reviewing a seam where both sides' unit tests pass and the question is whether the two mean the same thing by the value that crosses"
  - "Deciding whether a duplicate-suppression or idempotency key may be released, and what the receiver does once it is"
related_components: [messaging, concurrency]
tags: [service-seam, idempotency, duplicate-suppression, wire-contract, error-codes, callback-lifetime, cross-service-invariant]
---

# What Releases or Classifies State in Another Service Is That Service's Condition

Two defects in the edge agent had one shape. In each, a value crossed a seam
carrying the meaning the sender happened to have, and the receiver branched on
it as though it carried the meaning the receiver needed. Each side's tests
passed. Neither defect is visible from either side alone.

## The release callback fires on the receiver's condition

The edge holds a per-sequence registry so a re-dispatched operation is answered
from the report already made instead of being run on the device again. The
report queue tells it when to forget an operation.

`forget`'s doc states the precondition:

> `// forget drops an operation once it is terminal and central has been told.`
> — `src/edge/agent/internal/dispatch/registry.go:118`

Neither half was checked, and the caller established neither. The queue
confirmed on *any* accepted report, including a progress report and the two
acknowledgements — all of which central takes while the operation is still
running. The registry then admitted the same sequence again, and because a
mutation never coalesces (`src/modules/localnet/access/lane.go`), the second
submission queued behind the first and the interface was written twice.

The trigger needs central: `OwedRows` owes an execute row only while
`!dispatch_confirmed` (`src/services/device/internal/journal/record.go:121`),
which the first report sets — so the window looks closed. It re-opens because
`applyOnboarded` is not atomic
(`src/services/device/internal/dispatchapi/report.go:283`): `MarkOnboarded`
clears the confirmations (`src/services/device/internal/journal/journal.go:302`)
and a failing `SetFingerprint` makes the edge re-send the whole report, so the
clear runs again at an arbitrary later moment — mid-mutation, with the registry
already emptied.

The fix narrows the firing condition to the receiver's:

```go
// src/edge/agent/internal/report/drain.go:116
if q.confirm != nil && closesOperation(k, e.report) {
    q.confirm.Confirmed(k.device, k.sequence)
}
```

`closesOperation` (`src/edge/agent/internal/report/kind.go:64`) admits a
terminal result and a refusal, and rejects a progress report, both
acknowledgements, and the reports that name no operation.

## A required code field is not a place to borrow a peer's value

`Refused.code` is required and pattern-constrained, so a refusal for an error
the lane returned without a code still has to carry one. It carried
`access/unknown-device`. Central does not treat that as generic: `terminalRefusal`
(`src/services/device/internal/dispatchapi/report.go:266`) resolves it against
the device registry and, when the device is no longer listed, disposes the
mutation `REJECTED` and writes an audit record naming a cause that never
happened. For a listed device it stays retryable, so a malformed dispatch is
re-sent forever with nothing saying the edge is failing on something else.

Uncoded errors reach that branch from ordinary paths — a mutation with no
sequence, a resume with no admission time, a cancelled context at shutdown. The
fix sends a code the receiver does not name, so its `default` arm applies:

```go
// src/edge/agent/internal/dispatch/subscribe.go:24
var ErrCodeUncodedRefusal = errs.NewCode("agent/uncoded-refusal")
```

## How to apply it

When a value crosses a seam, ask what the receiver *does* with it, and derive
what to send from that — not from what is at hand.

- A callback that releases another component's state fires on that component's
  condition. Where the two differ, the sender's is almost always the broader
  one, and broader means released too early.
- A precondition in a doc comment that the function does not check names an
  obligation. Say which caller discharges it, in that comment, or enforce it.
- A code, enum, or status the receiver branches on is part of its decision
  table. When you must fill one for a case you could not classify, add your own
  value outside the receiver's known set so its default applies. Never reuse a
  neighbour's.

Both fixes are pinned by tests that fail without them:
`TestOnlyAReportThatEndsAnOperationConfirmsIt`
(`src/edge/agent/internal/report/queue_test.go:403`) and
`TestAnUncodedLaneErrorIsRefusedUnderThisAgentsOwnCode`
(`src/edge/agent/internal/dispatch/demux_test.go:329`). Neither seam had a test
before; that is why both defects survived the units' own suites.

## What this does not cover

It does not say where a cross-service invariant should be enforced — the edge
fix leaves central's non-atomic `applyOnboarded` in place, which is still a
partially applied write on retry. It says nothing about code stability on the
wire; `errs-package-architecture-and-error-conventions.md` and
`docs/architecture/2026-09-04-error-wire-design-direction.md` own that. And it
is about meaning, not delivery: ordering and at-least-once are separate
problems.
