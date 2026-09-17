# Northbound API Services

## Identity

The `api/` root holds northbound Connect services that an operator, the web
app, or a workflow calls. Services here are RPC sinks: they expose request
and response endpoints and are imported by no schema in the tree.

## Admission

A package belongs in `api/` if it defines northbound RPC services that an
operator, the web app, or a workflow invokes. `api/edge` passes because
`EdgeAdminService` is what an operator calls to create, provision, and retire
an edge. `edge/attach` fails admission because `EdgeService` is what an edge
calls on its own behalf; an operator never calls it.

## Boundaries

Imports: model/access, model/capture, model/edge, model/inventory, net/capture

Imported by: nothing

Packages under `api/` may import `model/` entities and handles, `net/`
primitives, and `errs/`. They are sinks: every package here declares a service,
so no schema in the tree may import one.

## Packages

- `capture/v1/`: Operator-facing `CaptureService` to create, control, and read back a capture.
- `device/v1/`: Operator-facing `DeviceService` for immediate device observation and mutation.
- `edge/v1/`: Operator-facing `EdgeAdminService` to create, provision, and retire edges.
