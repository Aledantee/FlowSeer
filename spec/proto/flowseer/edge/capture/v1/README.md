# Capture assignments and upload

`flowseer.edge.capture.v1` holds `CaptureEdgeService`, which an edge calls to
receive capture assignments and upload packet chunks. The CaptureSession
entity, its configuration, and the chunk frames the streams carry live in
[`model/capture/v1`](../../../model/capture/v1/README.md), which this
package imports and returns as `CaptureSessionGlobalRef` from the closed
stream; the edge's own identity rides on
[`model/edge/v1`](../../../model/edge/v1/README.md)'s `SignedEdgeAssertion`.

## Boundaries

Imports: model/capture, model/edge

Imported by: nothing

Deliberately absent:

- The CaptureSession entity, its ref pair, its lifecycle, and its chunk
  frames. They live in `model/capture/v1` so the model stays separate from
  the RPC surface.
- The operator-facing service that creates, controls, and reads back a
  capture. `CaptureService` stays in
  [`api/capture/v1`](../../../api/capture/v1/README.md); an operator calls
  it and an edge never does.

## Assignments stream

`SubscribeCaptureAssignments` delivers owed assignments to the connected edge,
authenticated by its opening assertion. Central delivers only assignments for
sessions scoped to that edge (`ref.edge.edge.id == edgeID`). Assignments carry
either `start` with `CaptureSessionConfig` (for sessions in
`CAPTURE_LIFECYCLE_PENDING` before upload begins) or `stop` with
`CaptureSessionGlobalRef` (when an operator requests cancellation of an active
session). Once the edge begins uploading packet chunks for a session, central
marks the session `RUNNING` and withdraws the start assignment.

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

## Reaching the edge

An operator-originated capture command reaches the edge via
`SubscribeCaptureAssignments`. The edge calls central and holds the
server-streaming RPC open. Central originates a `start` assignment carrying
`CaptureSessionConfig` for every pending session configured for that edge, and
re-originates it until the edge connects `UploadCapture` and delivers the first
packet chunk. An operator cancellation similarly originates a `stop` assignment
carrying `CaptureSessionGlobalRef`.
