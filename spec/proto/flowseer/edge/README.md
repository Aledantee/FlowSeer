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

Imports: errs, model/access, model/capture, model/edge

Imported by: nothing

## Packages

- `capture/v1/`: Edge-facing `CaptureEdgeService` to upload a running capture session's packets.
- `dispatch/v1/`: `DispatchService`, the execution envelope central and the edge hosting a device's lane exchange.
