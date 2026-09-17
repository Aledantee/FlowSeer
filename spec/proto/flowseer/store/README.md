# Process Storage

## Identity

The `store/` root holds persisted records and configuration files that a single
process writes or reads at start. Messages here define private on-disk files,
mounted registry prototext, and key-value bucket records.

## Admission

A package belongs in `store/` if its messages are private persistence schemas
for a single process. `store/device/v1` passes because `DeviceLaneRecord` and
`DeviceServiceConfig` are read and written only by the device service. Shared
entity models fail admission and belong in `model/`.

## Boundaries

Packages under `store/` may import `model/` entities and handles, `net/`
primitives, and `errs/`. They are private persistence contracts and are
imported by nothing FlowSeer-owned.

## Packages

- `device/v1/`: Device service lane outbox records, registry prototext, and deployment configuration.
- `edge/v1/`: Device access agent deployment configuration.
