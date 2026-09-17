# Durable Events

## Identity

The `event/` root holds durable stream records that are not an entity's own
transition. Records here are audit logs and timeline facts written to durable
message streams.

## Admission

A package belongs in `event/` if it defines standalone, durable stream records
rather than an entity's Config/State/Event lifecycle. `event/device/v1` passes
because `DeviceOperationEvent` is an audit row across nine operational kinds. An
entity transition like `TagEvent` fails admission and belongs beside its triad
in `model/inventory`.

## Boundaries

Imports: model/access, model/inventory

Imported by: nothing

Packages under `event/` may import `model/` entities and handles, `net/`
primitives, and `errs/`. They are sinks and are imported by nothing
FlowSeer-owned.

## Packages

- `device/v1/`: Durable audit events for device mutations, lane blocks, route selections, and recovery.
