# Audit delivery

`flowseer.edge.audit.v1` holds `AuditService`, the Connect call an edge (or
central's own drift detector) delivers a `DeviceOperationEvent` through. The
record itself lives in
[`event/access/v1`](../../../event/access/v1/README.md), which this package
imports and never restates; `AuditService` is the wire, not the record.

## Boundaries

Imports: event/access

Imported by: nothing

Deliberately absent:

- `DeviceOperationEvent` and its nine kinds. They live in `event/access/v1`
  so the record's contract can evolve without touching the RPC surface.
- Central's own read of the delivered stream. Central derives nothing from
  it afterwards; the fingerprint and every other fact it acts on arrive
  through `edge/dispatch/v1`'s reports.

## Delivery

`AuditService.Deliver` is how a record reaches the stream: the caller sends
one `DeviceOperationEvent` per call over Connect, central writes it into the
JetStream audit stream with the event id as the deduplication key, and
answers only after the stream has acknowledged it. Central is the stream's
only writer, and an error means the record is not held, so a caller that
must not release state before its record is durable simply does not proceed
on an error.
