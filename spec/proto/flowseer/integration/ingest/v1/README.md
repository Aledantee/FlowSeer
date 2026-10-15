# Ingest Envelopes

`flowseer.integration.ingest.v1` defines `IngestRecord`, the envelope carrying
observation data published from edge and centrally hosted adapters to the
central ingestion pipeline. An edge record arrives by one of two paths: its
per-edge NATS JetStream stream, or the Connect call
`flowseer.edge.ingest.v1.IngestService.Deliver`.

## Boundaries

Imports: event/log, model/inventory, model/rawwindow

Imported by: edge/ingest

Deliberately absent:

- A tenant identifier. Tenancy stays ambient. For a record on the per-edge
  stream, intake resolves the tenant from the edge account and the stream it
  arrived on. For a record delivered through `IngestService.Deliver`, central
  takes the edge from the call's assertion and the tenant from its edge store.
- A per-payload id. Deduplication and record identity belong to the envelope's
  `record_id`, which is also passed as the NATS message id.

## Structure

`IngestRecord` combines:
- `record_id`: A UUID identifying this record for deduplication across retries.
- `provenance`: The observation's origin, including binding, timestamp,
  observing edge, and protocol.
- `payload`: A typed oneof with one arm per record kind (`syslog` for
  `SyslogRecord`).
- `raw`: Optional raw evidence (`RawEvidence`) attached on parse failures or
  during an operator-opened raw window.
