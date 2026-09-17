# Northbound API Services

## Identity

The `api/` root holds northbound Connect RPC services called by operators, web
applications, CLI tooling, and external automation workflows. Services here are
RPC sinks: they expose request and response endpoints and are imported by no
schema in the tree.

## Admission

A package belongs in `api/` if it defines northbound RPC services that external
clients invoke. `api/device` passes because it defines `DeviceService` for
operator access. `model/access` fails admission because it contains shared
operation vocabulary without RPC definitions.

## Boundaries

Imports: model/access, model/capture, model/credential, model/edge, model/inventory, model/policy, net/addr, net/capture

Imported by: nothing

Packages under `api/` may import `model/` entities and handles, `net/`
primitives, and `errs/`. They are sinks: every package here declares a service,
so no schema in the tree may import one.

## Packages

- `capture/v1/`: Operator-facing `CaptureService` to create, control, and read back a capture.
- `device/v1/`: Operator-facing `DeviceService` for immediate device observation and mutation.
- `edge/v1/`: Operator-facing `EdgeAdminService` to create, provision, and retire edges, and edge-facing `EdgeService` to enroll, attach to the bus, and acquire credentials.
