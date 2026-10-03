# Ingest Envelopes

`flowseer.integration.ingest.v1` defines `IngestRecord`, the envelope carrying
observation data published from edge and centrally hosted adapters to the
central ingestion pipeline over NATS JetStream.

## Boundaries

Imports: event/log, model/inventory

Imported by: nothing FlowSeer-owned

Deliberately absent:

- A tenant identifier. Tenancy stays ambient: intake resolves the tenant from
  the edge account and per-edge stream the record arrived on.
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
