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
related_components: [messaging, service_layer]
tags: [context-propagation, goroutine-ownership, shutdown, single-worker-pattern, mutex-trylock]
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

## How to apply

When reviewing or writing a single-active-worker queue:

1. Grep the drain/worker loop for a `ctx` parameter used across more than
   one dequeued item. If found, check whether each item instead carries its
   own context; if not, that is this bug.
2. When a type has both an admission method and a delivery method that
   unblocks already-admitted work, check whether a shutdown flag gates
   both. It should gate only admission.

## What this does not cover

This is about context and flag *scope*, not about whether a single-active-
drainer design is the right concurrency model at all — `Lane`'s own design
rationale for using one drainer per device (matching the direction record's
one-ordered-lane-per-device rule) is unaffected by either trap.
