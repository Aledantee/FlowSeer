# Ingest Envelopes

`flowseer.integration.ingest.v1` defines `IngestRecord`, the envelope carrying
observation data from adapters to the central ingestion pipeline.
Edge records travel through the Connect call
`flowseer.edge.ingest.v1.IngestService.Deliver`. Intake validates each record
and publishes it into CENTRAL's typed and evidence streams.

## Boundaries

Imports: event/log, model/inventory, model/rawwindow

Imported by: edge/ingest

Deliberately absent:

- A tenant identifier. Tenancy stays ambient. Central takes the edge from
  the delivery call's verified assertion and the tenant from its edge store.
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
