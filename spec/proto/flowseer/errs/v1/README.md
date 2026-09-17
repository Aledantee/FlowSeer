# Error wire payload

The `flowseer.errs.v1` package holds `ErrorPayload`, the shape an
`src/common/errs` error takes when it crosses a process boundary — Connect
RPC, a broker envelope. The
[error wire design record](../../../../../docs/architecture/2026-09-04-error-wire-design-direction.md)
fixes what a payload may carry and to whom; this package is the wire shape
alone. The Go encoder and decoder live in `src/common/errs/wire.go`, not
here, so the schema stays free of any process's error-handling logic.

## Boundaries

Imports: nothing FlowSeer-owned

Imported by: integration/device, store/device

Deliberately absent:

- Any process-local field: an exit code, a log level, a trace ID. The
  error-wire record excludes the exit code by name — it describes the
  process that failed, not the failure — and the rest never cleared the
  `src/common/errs` payload's own bar in the first place.
- A discriminated cause "kind." Every cause is a plain `ErrorPayload`; there
  is nothing here for a decoder to fail to recognize.
- Any RPC or broker-specific framing. A Connect status code or a NATS
  header wraps this payload; it does not appear inside it.

## Two projections, one message

The same `ErrorPayload` message serves two different fillings:

- **Trusted internal transit** (service to service, a broker) fills `code`,
  `message`, `safe_attributes`, `user_message`, `hint`, `retry`, `causes`,
  and `stack` — the whole recursive tree, one node per wrapped error.
- **An untrusted client boundary** fills only `code`, `safe_attributes`,
  `user_message`, and `hint`, resolved from the whole chain into one flat
  node. `message`, `stack`, and `causes` are never set: the internal message
  names hosts and call paths, a stack is diagnostic only, and the chain's
  shape is not the client's business.

`safe_attributes` is the same field on both wires, because an attribute
either was marked client-safe when it was attached or it was not — no
attribute internal to a service ever appears on this message at all, whether
the payload is headed to a peer service or to a client.

## Total decoding

A cause is itself an `ErrorPayload`. Because every node in the tree shares
one message shape, decoding needs no type registry to stay total: an error
from a package the decoder does not import still arrives as a node carrying
whatever it set, and a foreign (non-`errs`) cause the encoder could not
introspect further arrives as a leaf carrying only its rendered message. The
chain's shape survives even when its original Go types do not.
