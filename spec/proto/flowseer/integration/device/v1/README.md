# Execution envelope

The `flowseer.integration.device.v1` package is what central and the edge
running one integration exchange to execute a single operation on one
device's lane: a dispatch, its result, and the two acknowledgements decision
4 of the
[verified device access record](../../../../../../docs/architecture/2026-09-05-verified-device-access-direction.md)
requires around the central journal barrier. It imports only
`device/access` and `errs`, per the record's amendment, and it is not the
operator-facing API — `api/device/v1` is that, and never imports this
package.

## No device or edge ref on any message

Every message here travels over a channel already addressed to one device
and one edge — one subject per device per edge in decision 1's NATS fabric.
Restating `DeviceGlobalRef` or `EdgeGlobalRef` on top of that would import
`api/inventory` and `api/edge` directly into this package, which its import
set forbids, and would carry an identity the transport already carries.
`ExecuteRequest.mutation` supplies its own device ref through
`MutationIntent.device` when the operation is a write; a `TypedRead` names
none, because the transport is enough for a read.

## The barrier, message by message

1. Central admits a mutation or a read to the device's lane and sends
   `ExecuteRequest`: the sequence, a deadline, a fresh idempotency key, and
   either a `MutationIntent` or a `TypedRead`.
2. For a mutation, central checkpoints `POSSIBLY_APPLIED` durably before any
   command reaches the device and sends `CheckpointRequest`; the edge answers
   `CheckpointAck` and only then submits the command. From here the effect is
   unknown until an observation says otherwise.
3. The edge runs the operation and returns `ExecuteResult`: the sequence, the
   `OperationPhase` it reached, and either the `InterfaceObservation` or an
   `flowseer.errs.v1.ErrorPayload`. A read reports `OPERATION_PHASE_OBSERVING`
   as the phase it reached, because a read is exactly that phase in the
   shared vocabulary.
4. Central durably records the terminal disposition and sends
   `TerminalResultAck`, which is what frees the device's lane for the next
   sequence — the barrier decision 4 describes.

## What is deliberately absent

- A device or edge ref, per the section above.
- A protocol, a path, or a raw command. The typed intent or read is the
  contract; how a route expresses it is the adapter's concern behind the
  edge.
- A credential or a session identifier. Decision 9 delivers those over their
  own authenticated channel, never inside this envelope.
- A tenant. Scope is ambient.
