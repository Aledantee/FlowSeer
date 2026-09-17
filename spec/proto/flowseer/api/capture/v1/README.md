# Capture services

The `flowseer.api.capture.v1` package holds the two Connect services around the
CaptureSession entity: `CaptureService`, which an operator calls to create,
control, and read back a capture, and `CaptureEdgeService`, which an edge calls
to upload packet chunks. The entity itself and the chunk frames both services
share live in [`model/capture/v1`](../../../model/capture/v1/README.md), which
this package imports and returns as `CaptureSessionRecord` from every call that
hands back a session.

## Boundaries

Imports: model/capture, model/edge, net/capture

Imported by: nothing

Deliberately absent:

- The CaptureSession entity, its ref pair, its lifecycle, and its chunk frames.
  They live in `model/capture/v1` so the model stays separate from the RPC
  surface.
- Raw packet capture filters and link types. Those are ref-free values in
  `net/capture/v1`.
- Ambient tenancy. Scope is ambient from the authenticated request.

## Re-assertion on the upload stream

[The edge assertion contract](../../../model/edge/v1/README.md#the-assertion-header)
checks a `SignedEdgeAssertion` when a call opens, and states plainly that
streams are checked only there — an assertion itself is valid for at most 60
seconds. `UploadCapture` is a stream an edge holds open for as long as the
capture session runs, which routinely outlives that window, so
`UploadCaptureRequest` is a required `oneof` of `CapturePacketChunk` and
`SignedEdgeAssertion` rather than a plain stream of chunks. The edge sends a
fresh assertion on the stream at an interval shorter than the 60-second
window. The server verifies each one exactly as it verifies the opening
one, nonce replay check included, and closes the stream if the interval
passes without one arriving.

## Open question: reaching the edge

How an operator-originated capture command reaches the edge that must run
it is not decided by this schema. The edge calls central; central never
calls the edge. `EdgeService` has three RPCs — `Enroll`, `Rekey`,
`Heartbeat` — and none of them carries a command channel.
`CaptureService.CreateCaptureSession` records the operator's intent as a
`CaptureSessionConfig` on an edge, but nothing here specifies how that
intent reaches the edge that must act on it. A reader of this schema alone
should not conclude the command path is settled; it isn't.
