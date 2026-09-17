# Execution envelope

The `flowseer.integration.device.v1` package is what central and the edge
running one integration exchange to execute operations on a device's lane:
the dispatches central sends, the reports the edge answers with, and the
two Connect calls that carry them. Decision 4 of the
[verified device access record](../../../../../../docs/architecture/2026-09-05-verified-device-access-direction.md)
puts the central journal at the barrier; this package is the wire between
that journal and the edge. It imports only `model/access` and `errs`, per
the record's amendment, and it is not the operator-facing API,
`api/device/v1` is that and never imports this package.

## Two directions, both Connect

Central to edge is `DispatchService.Subscribe`: one server stream per edge,
which the edge opens with its assertion and holds open, reconnecting with
backoff. Every message on it names a device by id and carries one of
`ExecuteRequest`, `CheckpointRequest`, `TerminalResultAck`, or
`HoldResolved`. Central re-sends a message while it is unconfirmed and a
closed stream leaves what is owed in central's record for the next open, so
a message never depends on the stream that first carried it.

Edge to central is `DispatchService.Report`, unary: one `ExecuteResult`,
`CheckpointAck`, `HoldResolvedAck`, `Refused`, or `Onboarded` per call,
answered once central's write is durable. The envelope messages carry no
device or edge ref of their own; the stream and the report name the device
by id, and `MutationIntent.device` still travels inside a mutation because
the intent is recorded centrally with it.

Nothing here rides the bus. The leaf node carries what the edge publishes
for observability and nothing the edge must act on.

## The barrier, message by message

1. Central admits a mutation or a read, assigns the next sequence on that
   device's lane (reads and mutations share one counter), and sends
   `ExecuteRequest`: the sequence, a deadline, a fresh idempotency key, and
   either a `MutationIntent` or a `TypedRead`.
2. The edge admits it and reports `ADMITTED` with the `progress` arm. That
   report is central's durable record that the command may reach the
   device: central moves the mutation to `POSSIBLY_APPLIED` and sends
   `CheckpointRequest`; the edge answers `CheckpointAck` and only then
   submits the command. From here the effect is unknown until an
   observation says otherwise.
3. The edge reports every phase it reaches: `RECOVERING` with `progress`
   when the effect could not be established, `VERIFIED` with the
   `InterfaceObservation` that proved it, or an `ErrorPayload` with
   `submitted` false when the mutation failed before any command was sent.
   A read reports once, `OBSERVING` with its observation or its error,
   because a read is exactly that phase in the shared vocabulary.
4. Central durably records the terminal disposition and sends
   `TerminalResultAck`. The edge applies it and reports `RELEASED` or
   `ABANDONED`, which frees the device's lane for the next sequence, the
   barrier decision 4 describes.

Central applies reports in phase order for the open sequence and ignores a
stale or duplicate one; a read's report touches only that read.

## What the edge must send

- One report per phase reached, as above, and `RELEASED` after applying a
  terminal acknowledgement. Central confirms `ExecuteRequest` on the
  `ADMITTED` report (a read: on its result), `CheckpointRequest` on
  `CheckpointAck`, `TerminalResultAck` on `RELEASED` or `ABANDONED`, and
  `HoldResolved` on `HoldResolvedAck`.
- `Refused` for a dispatch it cannot apply, naming the sequence, the kind,
  and the lane's error code. Central keeps owing the message on a
  retryable code and disposes the mutation on a terminal one: retryable are
  `access/lane-closed`, `lane/overload`, `access/desynchronized`,
  `access/unknown-device` for a device central still lists, and a context
  deadline; terminal are `mutation/firmware-epoch` and
  `access/unknown-device` for a device central no longer lists. A code not
  listed is retryable. A refusal answering a `TerminalResultAck` or a
  `HoldResolved` counts as its confirmation: the edge holds nothing for
  that sequence. A refusal answering a `CheckpointRequest` with
  `access/no-pending-wait` counts as the checkpoint's confirmation only
  when the edge had already reported a phase past `ADMITTED` for that
  sequence; earlier, the request is still owed.
- `Onboarded` for each device after every start, carrying the fingerprint
  the identity probe learned. Central treats it as the point after which
  nothing the edge held in memory for that device survives, and re-sends
  what the record still owes.

## Resume

An `ExecuteRequest` with `resume` set is central re-dispatching a mutation
after the edge restarted past central's own checkpoint. The command may
already have been sent, so the edge admits the mutation straight into
recovery and observes before any retry, never submitting first; the carried
`admitted_at` is the start of the delayed-apply horizon, so a restart does
not reset it. An edge that restarted while parked before its checkpoint
resumes into recovery for a command it never sent and abandons at the
horizon: the conservative direction, and the common outcome of a restart.

## The disposition matrix

`TerminalResultAck` carries the disposition central recorded, and the edge
accepts it only where its phase allows: `VERIFIED` at `VERIFIED`;
`REJECTED` at `ADMITTED`, or at `POSSIBLY_APPLIED` before the command was
handed to the device; `INDETERMINATE_ABANDONED` at every open phase, since
abandonment is central's authority and abandoned work stays abandoned. An
acknowledgement the phase does not allow is answered with
`mutation/out-of-order` and changes nothing; central's record does not
change on it either.

## What is deliberately absent

- A device or edge ref on the envelope messages, per the section above.
- A protocol, a path, or a raw command. The typed intent or read is the
  contract; how a route expresses it is the adapter's concern behind the
  edge.
- A credential or a session identifier. Decision 9 delivers those over their
  own authenticated channel, never inside this envelope.
- Central's outbox policy: which row the record owes when is the device
  service's, in `src/services/device/README.md`. This README states only
  what a correct edge sends.
- A tenant. Scope is ambient.
