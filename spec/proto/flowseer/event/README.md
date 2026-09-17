# Durable Events

## Identity

The `event/` root holds durable stream records that are not an entity's own
transition. Records here are audit logs and timeline facts written to durable
message streams.

## Admission

A package belongs in `event/` if it defines standalone, durable stream records
rather than an entity's Config/State/Event lifecycle. `event/access/v1` passes
because `DeviceOperationEvent` is an audit row across nine operational kinds,
delivered by `AuditService` in `edge/audit/v1` rather than declared here. An
entity transition like `TagEvent` fails admission and belongs beside its triad
in `model/inventory`.

## Boundaries

Imports: model/access, model/inventory

Imported by: edge/audit

Packages under `event/` may import `model/` entities and handles, `net/`
primitives, and `errs/`. `event/` holds records that a delivering service
reads; the sink rule is about service declarations, and no package here
makes one.

## Packages

- `access/v1/`: Durable audit events for device mutations, lane blocks, route selections, and recovery.
