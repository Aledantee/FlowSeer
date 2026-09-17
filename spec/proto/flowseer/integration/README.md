# Integration Contracts

## Identity

The `integration/` root holds contracts between central services and
distributed integrations, including dispatch envelopes, execution reports, and
the integration fabric contract described in the [device service direction record's "Transport" section](../../../../docs/architecture/2026-08-20-device-service-and-inventory-direction.md#transport-nats-as-the-integration-fabric).
The root is reserved for this fabric contract until the bus plan lands.

## Admission

A package belongs in `integration/` if it defines internal execution envelopes,
report streams, or fabric descriptors between central and an integration host.
`integration/device/v1` passes because it defines `DispatchService`.
`api/device/v1` fails admission because it is an external client API, not an
internal execution envelope.

## Boundaries

Imports: errs, model/access

Imported by: nothing

Packages under `integration/` may import `model/` operation vocabulary and
`errs/`. They are sinks and are imported by nothing FlowSeer-owned.

## Packages

- `device/v1/`: Execution dispatches, status reports, and checkpoint envelopes on a device's lane.
