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

This is how an operator-originated capture command reaches the edge. The edge
calls `SubscribeCaptureAssignments` and holds the server stream open; central
never calls the edge.

A capture session belongs to one edge by the `EdgeGlobalRef` inside its
`CaptureSessionGlobalRef`, so central delivers an assignment only where
`ref.edge` names the edge the call's assertion authenticated. There is no field
on the request for an edge to name itself with, and that is deliberate: a
request field would be a second answer to a question the assertion has already
settled.

Central does not track what it has delivered. It derives what each session is
owed from that session's state, sends it, and derives it again when the state
changes or a resend interval passes:

- a `start` carrying `CaptureSessionConfig` while the session is
  `CAPTURE_LIFECYCLE_PENDING`. It stops being owed when the edge opens
  `UploadCapture` and delivers a first chunk, which moves the session to
  `CAPTURE_LIFECYCLE_RUNNING`;
- a `stop` carrying `CaptureSessionGlobalRef` while a session the edge started
  is `CAPTURE_LIFECYCLE_CANCELED` and has no artifact. It stops being owed when
  the edge flushes its final chunk, which produces one. A session canceled
  before any edge started it was never owed a stop.

Both are therefore re-delivered until the edge's own progress withdraws them,
so an edge takes each one as a statement of what central currently wants rather
than as an event. Starting a session it is already running, or stopping one it
is not, is a repeat to discard, not an error to report.

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
one, nonce replay check included, and closes the stream if the window passes
without one arriving — including when nothing at all arrives, which it bounds
with a read deadline rather than waiting for a message it can check.

## What the upload stream's assertions cover

An assertion on this stream is verified with an empty body hash, unlike the
header assertion on a unary call, which commits to the request body. There is
no whole body to commit to here and no single request to bind to, so what an
assertion binds is the edge, the procedure, the audience, its clock window, and
its nonce. Its window is what keeps the stream honest, which is why a lapsed
one closes it.

Central still checks every chunk against the session it names: the chunk's
`session.edge.edge.id` must be the edge the stream authenticated, and the
session record must agree. An edge cannot upload into another edge's session,
and cannot reopen one that has already produced its artifact.
