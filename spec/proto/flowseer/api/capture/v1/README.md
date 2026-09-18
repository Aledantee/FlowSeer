# Capture services

The `flowseer.api.capture.v1` package holds `CaptureService`, which an
operator calls to create, control, and read back a capture. The
CaptureSession entity and the chunk frames both services share live in
[`model/capture/v1`](../../../model/capture/v1/README.md), which this
package imports and returns as `CaptureSessionRecord` from every call that
hands back a session.

## Reading a capture back

Two RPCs return packets, and they answer different questions.

`TailCaptureSession` watches a capture in flight. It is an observation, not a
record: the stream carries what the session uploads from the moment the tail
attaches, and an operator slower than the capture loses chunks rather than
stalling it. That is why the response opens with `attached` before any chunk —
without it a caller cannot tell a quiet capture from one whose packets it
subscribed too late to see. A tail of a session that has already stopped ends
immediately rather than waiting for packets that will never come.

`DownloadCaptureSession` returns the stored artifact, and answers from the
session's record rather than from whether a file happens to exist. A capture
still being written is `FailedPrecondition` — there will be something to
download, but not yet, and the bytes on disk are incomplete and do not match
the digest the session will record. A capture whose payload retention has
expired is `NotFound`, and stays `NotFound`: the session record and its
counters survive expiry, the payload does not.

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
- The edge-facing service that carries assignments to an edge and takes its
  packet chunks back. `CaptureEdgeService` lives in
  [`edge/capture/v1`](../../../edge/capture/v1/README.md), which is also
  where a session created here reaches the edge that runs it; an edge calls
  it and an operator never does.
