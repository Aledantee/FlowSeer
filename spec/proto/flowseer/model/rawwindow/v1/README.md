# Raw Window Model

A raw window is a bounded period in which one edge's adapter keeps the
original bytes of the records a selector matches. `raw_window.proto` holds the
ref pair, the selector, the immutable configuration, the observed state, the
transition event, and the lifecycle and stop-reason enums.

The selector is a required oneof of a device, an integration, or an
edge-local source key. Central never reads an integration's kind, so the
integration arm matches the integration of the binding the edge resolved.

State separates three outcomes an accepted API call cannot tell apart:
pending (recorded centrally), active (the edge confirmed it), and terminal
with a typed stop reason. The transport and the operator API live in
[`edge/ingest/v1`](../../../edge/ingest/v1/README.md) and
[`api/ingest/v1`](../../../api/ingest/v1/README.md). The design is recorded in
the [Raw Window Control direction](../../../../../../docs/architecture/2026-10-08-raw-window-control-direction.md).

## Boundaries

Imports: model/edge, model/identity, model/inventory

Imported by: api/ingest, edge/ingest, event/operator, integration/ingest, store/device

Deliberately absent:

- A Connect service. The operator and edge services import this package and
  live in service roots.
- A tenant field. Tenancy is ambient from the request or the producing edge.
- Captured bytes. A window decides whether an edge keeps bytes. The bytes ride
  `integration/ingest`'s `RawEvidence`.
