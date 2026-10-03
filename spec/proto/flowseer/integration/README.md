# Integration Contracts

## Identity

The `integration/` root is reserved for the integration fabric contract the
[device service direction record's "Transport" section](../../../../docs/architecture/2026-08-20-device-service-and-inventory-direction.md#transport-nats-as-the-integration-fabric)
names: what a distributed integration and central exchange over NATS to
announce itself, describe its kind, and publish events. The root holds this
README and one package per contract; `ingest/v1` is the first.

## Admission

A package belongs in `integration/` when it defines that fabric contract: an
announcement, a kind descriptor, or an event subject a distributed
integration and central exchange over NATS. `edge/dispatch/v1` fails
admission even though central and an edge exchange its messages too, because
that exchange is a Connect call between central and an already-enrolled
edge, not the fabric an integration uses to announce and describe itself
before one exists.

## Boundaries

Imports: event/log, model/inventory

Imported by: nothing

## Packages

- `ingest/v1`: IngestRecord envelope carrying typed payload and raw evidence.
