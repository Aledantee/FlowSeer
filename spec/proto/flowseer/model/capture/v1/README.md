# Capture Session

`flowseer.model.capture.v1` holds the CaptureSession entity — its ref pair,
its lifecycle, its config and state — and the chunk frames its two Connect
services share: `CapturePacketChunk` is what an edge uploads and what an
operator tails, `CaptureArtifactChunk` is what an operator downloads. The
values a capture produces and matches against — `LinkType`,
`CaptureCounters`, `CaptureFilter`, the mirror encapsulation, and
`PacketRecord` — live in
[`flowseer.net.capture.v1`](../../../net/capture/v1/README.md) and are
ref-free by design; this package embeds them by value and adds everything
that needs an identity to exist: the ref, the lifecycle, the authorization
record, and the streaming contracts. The two Connect services around it —
the one an operator calls to create, control, and read back a capture, and
the one an edge calls to upload one — live in
[`api/capture/v1`](../../../api/capture/v1/README.md), which imports this
package for the entity and the chunk frames and returns `CaptureSessionRecord`
from every call that hands back a session.

## The owning edge

A capture session's one owning parent is the edge that runs it:
`CaptureSessionGlobalRef` wraps `EdgeGlobalRef` plus the session's own
`CaptureSessionLocalRef`. A session is not scoped to a device: both
`CaptureSource` arms name something local to the edge itself, a host
interface name or a UDP port to listen on, never a device in inventory.
Nothing here imports `api/inventory`.

## Boundaries

Imports: model/edge, net/capture

Imported by: api/capture

Deliberately absent:

- A Connect service. The two services that create, control, and stream a
  capture live in `api/capture/v1`; this package holds only the entity and
  the chunk frames every boundary that names a capture session agrees on,
  never the calls that act on it.
