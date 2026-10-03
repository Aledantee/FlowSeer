# Durable Events

## Identity

The `event/` root holds durable stream records that are not an entity's own
transition. Records here are audit logs, timeline facts, and log records written
to durable message streams.

## Admission

A package belongs in `event/` if it defines standalone, durable stream records
rather than an entity's Config/State/Event lifecycle. `event/access/v1` and
`event/log/v1` pass because `DeviceOperationEvent` is an audit row across nine
operational kinds and `SyslogRecord` is a received log line, delivered as
stream records rather than declared as queryable entities. An entity transition
like `TagEvent` fails admission and belongs beside its triad in `model/inventory`.

## Boundaries

Imports: model/access, model/inventory, net/addr, net/log

Imported by: edge/audit, integration/ingest

Packages under `event/` may import `model/` entities and handles, `net/`
primitives, and `errs/`. `event/` holds records that a delivering service
reads; the sink rule is about service declarations, and no package here
makes one.

## Packages

- `access/v1/`: Durable audit events for device mutations, lane blocks, route selections, and recovery.
- `log/v1/`: Durable syslog records parsed into RFC 5424 fields.
