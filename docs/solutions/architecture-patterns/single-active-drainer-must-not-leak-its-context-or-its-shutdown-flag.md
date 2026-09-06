---
title: A Single-Active-Drainer Queue Must Not Leak the Drainer's Context or Its Shutdown Flag Onto Other Waiters
date: 2026-09-06
category: architecture-patterns
module: src/modules/localnet/access
problem_type: bug
component: concurrency
severity: high
symptoms:
  - "Canceling one caller's context causes an unrelated caller's concurrently queued work on the same resource to fail with context.Canceled or context.DeadlineExceeded"
  - "A shutdown method's own doc says already-admitted work still completes, but a delivery call that would unblock it starts returning a closed/shutdown error right after shutdown runs"
root_cause: "A queue processed by whichever caller's goroutine becomes the sole active worker (a TryLock-guarded drain loop) used that worker's own context for every item in the loop, and a single shutdown flag gated both new admission and delivery to already-admitted work waiting on an external signal."
resolution_type: code_fix
applies_when:
  - "Implementing or reviewing a queue where one caller's goroutine becomes the sole active worker (via sync.Mutex.TryLock or an equivalent) and drains items other callers admitted"
  - "A worker loop like this takes a context.Context parameter and passes it to per-item work inside the loop"
  - "Adding a Close/Shutdown method to a type that both admits new work and delivers external signals (acks, callbacks) to work already admitted"
  - "The active-worker loop guards its queue and its own TryLock with two independent locks, and can return (unlock) between draining the queue empty and a new item being admitted"
  - "Multiple concurrent callers can coalesce onto one in-flight operation (a ticket/promise a late joiner waits on), where the joiner's deadline may outlive the caller that originally started the work"
  - "A method delivers one external signal (a checkpoint, an ack) to a single waiter that may have already received it once"
related_components: [messaging, service_layer]
tags: [context-propagation, goroutine-ownership, shutdown, single-worker-pattern, mutex-trylock, lost-wakeup, request-coalescing]
---

# A Single-Active-Drainer Queue Must Not Leak the Drainer's Context or Its Shutdown Flag Onto Other Waiters

## Situation

`src/modules/localnet/access/lane.go`'s `Lane` orders every operation for
one device through a single FIFO (`internal/lane.Queue`). Multiple
goroutines can call `Lane.Submit` concurrently for the same device;
exactly one of them becomes the "active drainer" via
`deviceState.draining.TryLock()` (`lane.go:325`) and processes every
item currently and subsequently queued, one at a time, until the queue
empties. Every other concurrent `Submit` call for that device just enqueues
its item and waits on a per-submission result channel.

This shape — one caller's goroutine does the work for N callers — creates
two traps that only show up under concurrent, cross-caller conditions a
single-caller test never exercises.

## What is true and why

**Trap 1: the drainer's context is not every item's context.** The obvious
signature for a drain loop is `drain(ctx context.Context, ds *deviceState)`,
using that one `ctx` for every item it processes. That `ctx` belongs to
whichever `Submit` call happened to win the `TryLock` race — an accident of
timing, not a fact about the item being processed. If that caller's context
is canceled while its goroutine is still draining *other* callers' items,
every other caller's item fails too, even though those callers never
canceled anything.

The fix is to carry each item's own context on the item itself and use it
per iteration, never the loop's ambient parameter:

```go
// src/modules/localnet/access/lane.go
type submission struct {
	// ctx is this submission's own caller's context, used to process this
	// item regardless of which goroutine's Submit call becomes the
	// device's active drainer.
	ctx     context.Context
	request *integrationv1.ExecuteRequest
	result  chan submissionOutcome
}

func (l *Lane) drain(ds *deviceState) {
	if !ds.draining.TryLock() {
		return
	}
	defer ds.draining.Unlock()
	for {
		item, ok := ds.queue.Next()
		if !ok {
			return
		}
		sub := item.Payload.(*submission)
		result, err := l.process(sub.ctx, ds, sub.request) // sub.ctx, not a shared ctx
		sub.result <- submissionOutcome{result: result, err: err}
	}
}
```

(Simplified to isolate this trap: the current `lane.go` also runs `drain` on
its own goroutine and rechecks the queue after unlocking, addressed
separately as Trap 3 below.)

Verified as a real defect, not a hypothetical: stashing this fix (reverting
`drain` to take one shared `ctx` again) makes
`TestLaneOneCallersCancellationDoesNotPoisonAnother`
(`src/modules/localnet/access/lane_test.go:449`) fail with `observe:
observe: context canceled` for the second, never-canceled caller.

`internal/mutation.Machine` already avoided this mistake structurally: every
method (`Checkpoint`, `Execute`, `Observe`, `Acknowledge`) takes its own
`ctx` parameter from its direct caller rather than storing one context on
the `Machine` at construction — there is no shared "operation context" to
leak in the first place. A single-active-worker loop that instead reuses
one context across multiple logical operations is the pattern this lesson
warns about.

**Trap 2: one shutdown flag can gate two different things that must not
share a gate.** `Lane.Close` sets one `closed` flag meant to stop *new
admissions*. `HandleCheckpoint` and `HandleTerminalAck` deliver central's
messages to a mutation *already admitted* before `Close` ran — they unblock
work that is waiting on an external signal, not admit anything new. Routing
both kinds of calls through the same "reject if closed" lookup
(`l.device`, `lane.go:206-211`) means `Close`'s own doc comment
("already-admitted items continue to their terminal result") becomes false
the instant it runs: the checkpoint or ack that item needs can never
arrive.

The fix separates the two lookups so only the admission path checks the
flag:

```go
// src/modules/localnet/access/lane.go
func (l *Lane) device(deviceKey string) (*deviceState, error) {
	if l.closed {
		return nil, errs.New().Code(ErrCodeClosed).Msg("lane is closed")
	}
	return l.deviceRegardlessOfClosed(deviceKey)
}

func (l *Lane) deviceRegardlessOfClosed(deviceKey string) (*deviceState, error) {
	// looked up by HandleCheckpoint and HandleTerminalAck, never gated by closed
	...
}
```

`TestLaneCloseStillDeliversCheckpointAndAckToAnAlreadyAdmittedMutation`
(`lane_test.go:381`) admits a mutation, then calls `Close` concurrently with
the checkpoint/ack delivery loop, and asserts the mutation still reaches
`RELEASED`.

**Trap 3: unlocking the active-worker mutex and rechecking the queue are two
separate steps, and an item can land in the gap between them.** `Lane.drain`
guards two independent things: `ds.queue` (its own lock) and
`ds.draining` (a `sync.Mutex` used as a TryLock gate). A drainer that calls
`ds.queue.Next()`, sees the queue empty, and returns without checking again
strands any item admitted after that `Next()` call but before the drainer
actually released `draining` — a concurrent `Submit`'s own `drain` call
loses the `TryLock` race (the exiting goroutine still holds it) and simply
returns, assuming the goroutine it lost the race to will pick up its item.
If that goroutine has already decided to exit, nobody ever does.

The fix re-checks the queue length after unlocking and loops back to retry
`TryLock` if it is non-empty, rather than trusting the racing `Submit` to
win a lock that has not been released yet (`lane.go:412-441`, the outer
`for` wrapping the drain-to-empty inner loop).

**Trap 4: a coalescing joiner's result must come from the work, never from
whichever caller's context finishes first.** When several callers coalesce
onto one in-flight operation, the caller that originally admitted it may
have a shorter deadline than a later joiner waiting on the same ticket. The
naive shape — finish the ticket and return in the same `select` arm as the
caller's own `ctx.Done()` — hands every joiner that caller's cancellation as
if it were the operation's own outcome, even though the operation is still
running and may still succeed. The fix runs the ticket's own completion
(`Coalescer.Finish`) from a goroutine that only ever reads the work's actual
result off its own channel, decoupled from any single caller's `ctx`; each
caller's own `select` races its own `ctx.Done()` against that goroutine's
`done` channel purely to decide when *it* stops waiting, never to decide
what the joiners are told (`lane.go:362-392`).

**Trap 5: a duplicate delivery to an already-served waiter must return an
error, not block forever.** `HandleCheckpoint`/`HandleTerminalAck` hand one
message to a channel a single waiter reads once. A plain unbuffered-channel
send blocks if the waiter already received a prior delivery for the same
wait and no new wait has been registered yet — central retrying a
checkpoint delivery (a legitimate, expected case) would then hang the
handler indefinitely. The fix does the whole check-in-flight/send/clear
sequence under one lock with a non-blocking `select`/`default` on the send,
returning `ErrCodeNoPendingWait` immediately instead of blocking
(`lane.go:448-...`, mirrored in `HandleTerminalAck`).

## How to apply

When reviewing or writing a single-active-worker queue:

1. Grep the drain/worker loop for a `ctx` parameter used across more than
   one dequeued item. If found, check whether each item instead carries its
   own context; if not, that is this bug.
2. When a type has both an admission method and a delivery method that
   unblocks already-admitted work, check whether a shutdown flag gates
   both. It should gate only admission.
3. When the worker loop's own exit condition is "queue looked empty", check
   whether it rechecks after releasing its lock, not before. Two
   independent locks (the queue's, the TryLock gate's) mean "empty, then
   unlock" is not atomic.
4. When multiple callers coalesce onto one in-flight operation, check that
   the operation's completion is driven by its own outcome on its own
   goroutine, never by racing it against any single caller's `ctx.Done()`
   in the same `select` that decides what every joiner receives.
5. When a method delivers one external signal to a single waiter, check
   that a duplicate delivery attempt returns an error rather than blocking
   — a caller retrying a legitimate delivery must never hang the receiver.

## What this does not cover

This is about context and flag *scope*, not about whether a single-active-
drainer design is the right concurrency model at all — `Lane`'s own design
rationale for using one drainer per device (matching the direction record's
one-ordered-lane-per-device rule) is unaffected by either trap.
