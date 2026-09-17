# Edge Plane Services

## Identity

The `edge/` root holds Connect services between central and an enrolled edge
process, in either direction: what an edge calls to reach central and what
central calls to reach an edge.

## Admission

A package belongs in `edge/` if its service is part of the exchange between
central and an enrolled edge. `edge/attach` passes because `EdgeService` is
what an edge calls on its own behalf to enroll, stay attached, and keep its
credentials current. `api/edge` fails because `EdgeAdminService` is what an
operator calls to create, provision, and retire an edge; the edge itself
never calls it.

## Boundaries

Imports: errs, event/access, model/access, model/capture, model/credential,
model/edge, model/policy, net/addr

Imported by: nothing

## Packages

- `attach/v1/`: `EdgeService`, what an edge calls to enroll, stay attached, list its devices, and acquire credentials.
- `audit/v1/`: `AuditService`, delivering the durable `DeviceOperationEvent` audit record.
- `capture/v1/`: Edge-facing `CaptureEdgeService` to upload a running capture session's packets.
- `dispatch/v1/`: `DispatchService`, the execution envelope central and the edge hosting a device's lane exchange.
