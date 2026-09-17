# Edge Plane Services

## Identity

The `edge/` root holds Connect services between central and an enrolled edge
process, in either direction: what an edge calls to reach central and what
central calls to reach an edge.

## Admission

A package belongs in `edge/` if its service is part of the exchange between
central and an enrolled edge. `edge/capture` passes because
`CaptureEdgeService.UploadCapture` is what an edge calls to hand off a
running capture's packets. `api/capture` fails because `CaptureService` is
what an operator calls to create and read back a capture; an edge never
calls it.

## Boundaries

Imports: errs, event/access, model/access, model/capture, model/credential,
model/edge, model/policy, net/addr

Imported by: nothing

## Packages

- `attach/v1/`: `EdgeService`, what an edge calls to enroll, stay attached, list its devices, and acquire credentials.
- `audit/v1/`: `AuditService`, delivering the durable `DeviceOperationEvent` audit record.
- `capture/v1/`: Edge-facing `CaptureEdgeService` to upload a running capture session's packets.
- `dispatch/v1/`: `DispatchService`, the execution envelope central and the edge hosting a device's lane exchange.
