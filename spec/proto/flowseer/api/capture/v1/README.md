# Capture services

`CaptureEdgeService`, the Connect service an edge calls to upload packet
chunks, moved to [`edge/capture/v1`](../../../edge/capture/v1/README.md)
along with the open question of how an operator-originated capture command
reaches the edge.

The `flowseer.api.capture.v1` package holds `CaptureService`, which an
operator calls to create, control, and read back a capture. The
CaptureSession entity and the chunk frames both services share live in
[`model/capture/v1`](../../../model/capture/v1/README.md), which this
package imports and returns as `CaptureSessionRecord` from every call that
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
