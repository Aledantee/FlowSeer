# Edge administration

The `flowseer.api.edge.v1` package holds `EdgeAdminService`, the Connect
service an operator calls to create an edge, issue and revoke its setup keys,
retire it, and read it back. The Edge entity itself, its ref pair, lifecycle,
keys, assertion, and provisioning file live in
[`model/edge/v1`](../../../model/edge/v1/README.md), which this package imports
and returns as `EdgeRecord` from every call that hands back an edge.

## Boundaries

Imports: model/edge

Imported by: nothing

Deliberately absent:

- The calls an edge makes on its own behalf. Enrollment, rekey, heartbeat, bus
  attachment, the device listing, and the two credential lifecycles are
  `EdgeService` in [`edge/attach/v1`](../../../edge/attach/v1/README.md); an
  operator never makes them and this package never names their messages.
- A device reference. `RetireEdgeResponse` reports each orphaned lane by device
  id rather than by `DeviceGlobalRef`, because the dependency runs the other
  way — a device names the edge responsible for it — and this package stays
  below `model/inventory` so that it can.
