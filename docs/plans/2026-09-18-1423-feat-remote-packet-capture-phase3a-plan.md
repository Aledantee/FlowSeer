---
title: Remote Packet Capture Phase 3a, Shared Subscribe-Loop Transport - Plan
type: refactor
date: 2026-09-18
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: code
parent: docs/plans/2026-09-09-1213-feat-remote-packet-capture-plan.md
---

# Remote Packet Capture Phase 3a, Shared Subscribe-Loop Transport - Plan

> Implemented. 2 units, 2026-09-18T12:51:08Z to 2026-09-18T13:00:40Z.

## Goal

The reconnecting subscribe loop that holds central's dispatch stream open —
its backoff, its `Contact` counters, its pre-attempt resync, and its
attempt/receive machinery — becomes a reusable component generic over the
streamed message type, so the capture-assignment loop in U3c runs on the same
code rather than a second hand-written copy. The means is a new package that
holds the loop, with the dispatch loop rewired onto it and its observable
behavior unchanged: same backoff reset rule, same counts, same event names,
same log attributes. This refactor is wrong if the loop turns out to carry a
device-specific assumption that cannot be lifted into the caller, because then
capture cannot share it and the split was the wrong cut.

## Decisions

The parent plan's Decisions apply; this phase carries out its "the
subscribe-loop transport is shared, not duplicated" decision.

- The loop lives at `src/edge/agent/internal/subscribeloop`, not `src/common`.
  Why: both consumers are inside the agent — the edge is the only side that
  subscribes to a central stream — so `src/common`'s bar of cross-host reuse
  is not met yet. Promoting it later is a move, not a rewrite; keeping it
  agent-internal until a third caller exists follows the `src/modules/README.md`
  admission rule's spirit rather than pre-generalizing.
- The loop is generic over the streamed message type `T` and depends on
  neither connect nor the generated types. Why: the receive contract it needs
  is `Receive`/`Msg`/`Err`/`Close`, which `*connect.ServerStreamForClient[T]`
  already satisfies structurally, so a small `Stream[T]` interface plus an
  `Opener[T]` the caller implements keeps the loop testable with a fake stream
  and lets the dispatch client stay in the `dispatch` package.
- Event names, the two count-attribute keys, and per-message log attributes
  are all supplied by the caller, not derived from a namespace. Why: the
  dispatch loop's names are not uniform — the resync-failure event is
  `flowseer.edge.devices.listing_failed` while the stream events are
  `flowseer.edge.dispatch.connected`/`.disconnected`/`.dropped` — so a
  namespace-plus-suffix scheme could not reproduce them. Two of the loop's log
  attributes are loop-internal counts, not message fields: the connected event
  carries `slog.Int64("flowseer.edge.dispatch.connections", …)` from the
  `Contact` counter, and the disconnected event carries
  `slog.Int64("flowseer.edge.dispatch.messages", delivered)` from the receive
  count. A `LogAttrs func(*T)` hook cannot yield either — it gets no `Contact`,
  and the disconnected event fires after the receive loop with no message in
  hand — so the loop owns those two values and the caller names their keys.
  The `Events` struct therefore carries the four event-name strings plus a
  `ConnectionCountKey` and a `MessageCountKey`; the `LogAttrs func(*T)
  []slog.Attr` hook supplies only the genuinely per-message attributes (the
  dropped event's `flowseer.device.id`). Together they keep dispatch's output
  byte-for-byte and give capture its own names.
- `Contact` moves to the new package and keeps its three counters and their
  read methods. Why: `host.go`'s `registerContactInstruments` names the
  metrics (`flowseer.edge.dispatch.connections`/`.failures`/`.messages`) from
  the caller side and only reads `Contact.Connections()`/`Failures()`/
  `Messages()`, so moving the type leaves the metric names untouched and
  capture registers its own metrics over its own `Contact`.

## Requirements

1. The dispatch loop's observable behavior is unchanged. Acceptance: with the
   loop rewired, `subscribe_test.go`'s existing assertions pass unmodified in
   substance — backoff resets to the floor only after a delivered message, a
   stream that opens and delivers nothing still increments `connections` once
   when it ends without error, a mid-stream error increments `failures`, and a
   handler error is logged and does not end the stream.
2. The dispatch loop emits the same event names and log attributes as before.
   Acceptance: a stream open logs `otel.event.name=flowseer.edge.dispatch.connected`
   with `flowseer.edge.dispatch.connections` carrying the connection count, a
   drop logs `flowseer.edge.dispatch.dropped` with `flowseer.device.id` set
   from the message and `error.type` from the classifier, a stream end logs
   `flowseer.edge.dispatch.disconnected` with `flowseer.edge.dispatch.messages`
   carrying the delivered count, and a resync failure logs
   `flowseer.edge.devices.listing_failed` — the four event strings and the two
   count attributes the current loop emits, asserted in a test that reads a
   captured log handler.
3. The loop is reusable by a second caller with a different message type.
   Acceptance: a `subscribeloop` test drives `Run[fakeMsg]` with a fake
   `Opener[fakeMsg]` and a fake `Handler[fakeMsg]`, asserting the same
   backoff and counter behavior against a message type that is not
   `SubscribeResponse`.

## Out of scope

- Any change to what a dispatch does once received: the `Demux`, the
  `registry`, the report queue, and their tests are untouched except where
  they name a moved symbol.
- The capture-assignment loop itself. It is U3c; this phase only makes the
  transport it will use exist.
- The `errorType` classifier's mapping. It moves verbatim; its bounded output
  is a metric dimension and changing it is out of scope here.

## Units

### Ua1. The subscribeloop package

Files: `src/edge/agent/internal/subscribeloop/loop.go`,
`src/edge/agent/internal/subscribeloop/loop_test.go`,
`src/edge/agent/internal/subscribeloop/doc.go`
After: none
Change: `subscribeloop` holds a generic `Run` over message type `T`, taking a
context, a `Config` of `T`, and a `*Contact`, and returning an error. That
`Config` carries `Open` (an `Opener` of `T`), `Handler` (a `Handler` of `T`),
`MinBackoff` and `MaxBackoff` durations, a `Resync` hook, a `Wait` hook, an
`Events` struct, a `LogAttrs` hook returning the per-message log attributes,
and a logger. It also holds the `Stream`, `Opener`, and `Handler` interfaces
(each generic over `T`), the
`Events` struct (`Connected`, `Disconnected`, `Dropped`, `ResyncFailed`
event-name strings plus `ConnectionCountKey` and `MessageCountKey`
attribute-key strings), `Contact` with its three atomics and read methods, and
the moved `errorType` and `waitFor` helpers. The backoff, the first-message
connection count, the served-and-empty count, the failure count, and the
reset-on-delivered rule are the current loop's, transplanted with the
generated types replaced by `T` and the hardcoded event strings replaced by
`cfg.Events`. The connected event emits
`slog.Int64(cfg.Events.ConnectionCountKey, contact.connections.Load())` and the
disconnected event emits `slog.Int64(cfg.Events.MessageCountKey, delivered)`,
so those two loop-internal counts stay caller-named.
Tests: `loop_test.go` drives `Run` with a fake `Opener`/`Stream`/`Handler`
over a `fakeMsg` type and a controllable `Wait`, asserting Requirement 1's
backoff and counter behavior and Requirement 3's reuse; a captured `slog`
handler asserts the event names come from `cfg.Events` (Requirement 2 in the
generic form).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/agent/internal/subscribeloop`

### Ua2. Rewire dispatch and the host onto it

Files: `src/edge/agent/internal/dispatch/subscribe.go`,
`src/edge/agent/internal/dispatch/subscribe_test.go`,
`src/edge/agent/host/host.go`, `src/edge/agent/README.md`
After: Ua1
Change: `subscribe.go` loses `Run`, `Config`, `Contact`, `attempt`,
`errorType`, `waitFor`, and the concrete `Subscriber` and `Handler` interfaces
(subsumed by the generic package's `Opener`/`Stream`/`Handler`), keeps
`ErrCodeSubscribe` and `ErrCodeUncodedRefusal`, and gains a small
`Opener[dispatchv1.SubscribeResponse]`
adapter that opens the stream with `Subscribe(ctx,
connect.NewRequest(&dispatchv1.SubscribeRequest{}))`. `host.go` calls
`subscribeloop.Run[dispatchv1.SubscribeResponse]` with a `Config` whose
`Events` carries the four current strings and whose `LogAttrs` returns
`slog.String("flowseer.device.id", msg.GetDeviceId())`, and
`registerContactInstruments` takes `*subscribeloop.Contact` with the metric
names unchanged. `README.md`'s "The two things that are held open" section
keeps its meaning; where it names `dispatch.Contact` it names the type's new
home.
Tests: `subscribe_test.go`'s scenarios move to `loop_test.go` in Ua1 where
they are transport-generic; what stays here asserts the dispatch `Opener`
adapter opens the stream with an empty `SubscribeRequest` and that the
`LogAttrs` hook carries the device id onto the dropped event, so Requirement 2
holds for the concrete dispatch loop.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/agent/internal/dispatch src/edge/agent/host src/edge/agent/README.md`

Waves: Ua1 | Ua2

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/agent
go test -race ./src/edge/agent/...
```

The refactor is behavior-preserving, so the standing agent tests are the
proof: `go test -race ./src/edge/agent/...` green with the loop moved is the
statement that nothing observable changed.

## Definition of done

- [x] Verifier green for every changed path.
- [x] `src/edge/agent/README.md` names `Contact`'s new home in the change that
      moves it.
- [x] The dispatch loop emits the same four event names and the same log
      attributes as before — including the `connections` and `messages` count
      attributes — asserted in a test.
- [x] This plan's `status` set with an outcome note under its title, and the
      parent's U3a `Landed:` line filled.
- [x] No plan labels in code, comments, or commit messages.

## Open questions

None.
