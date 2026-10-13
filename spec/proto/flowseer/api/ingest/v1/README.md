# Ingestion services

The `flowseer.api.ingest.v1` package holds `RawWindowService`, which an
operator calls to open, read, and close a raw window. The window entity and
its state live in
[`model/rawwindow/v1`](../../../model/rawwindow/v1/README.md), and every call
that hands back a window returns it as `RawWindowRecord`.

`OpenRawWindow` names the edge and carries a required future expiry and a
positive byte budget. `GetRawWindow` and `CloseRawWindow` name only the window
id, so the edge always comes from the stored window and a caller cannot
change it to reach a different authorization check.

## Boundaries

Imports: authz, model/edge, model/rawwindow

Imported by: nothing

Deliberately absent:

- The raw window entity, its lifecycle, and its selector. They live in
  `model/rawwindow/v1`.
- The edge-facing control stream. `RawWindowEdgeService` lives in
  [`edge/ingest/v1`](../../../edge/ingest/v1/README.md), where an edge calls it
  and an operator never does.
- Ambient tenancy. Scope is ambient from the authenticated request.
