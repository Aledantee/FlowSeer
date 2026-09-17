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

Packages under `api/` may import `model/` entities and handles, `net/`
primitives, and `errs/`. They are sinks and are imported by nothing
FlowSeer-owned.

## Packages

- `capture/v1/`: Operator-facing `CaptureService` and edge-upload `CaptureEdgeService`.
- `device/v1/`: Operator-facing `DeviceService` for immediate device observation and mutation.
- `edge/v1/`: Operator-facing `EdgeAdminService` and edge-facing `EdgeService` (until split in a later phase).
