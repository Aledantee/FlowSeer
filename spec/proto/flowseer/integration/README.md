# Integration Contracts

## Identity

The `integration/` root holds contracts for a distributed integration to
announce itself, describe its kind, and publish observations to central.
`ingest/v1` defines the typed `IngestRecord` envelope carried by
`flowseer.edge.ingest.v1.IngestService.Deliver`. Central intake validates it
and publishes it into CENTRAL streams, as the
[device service direction record's delivery amendment](../../../../docs/architecture/2026-08-20-device-service-and-inventory-direction.md#2026-10-16-connect-replaces-the-edge-bus-attachment)
states. The root holds this README and one package per contract.
`ingest/v1` is the first.

## Admission

A package belongs in `integration/` when it defines an announcement, a kind
descriptor, or an observation contract, independent of its transport.
`edge/ingest/v1` defines the delivery call that carries ingest envelopes. `edge/dispatch/v1`
belongs under `edge/` because its messages control execution on an enrolled
edge rather than describe observations.

## Boundaries

Imports: event/log, model/inventory, model/rawwindow

Imported by: edge/ingest

## Packages

- `ingest/v1`: IngestRecord envelope carrying typed payload and raw evidence.
