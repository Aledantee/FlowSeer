# Network Instance Primitives

The `flowseer.net.instance.v1` package defines device-scoped network instances
(routing and bridging domains) and route distinguishers. Every forwarding table
names the network instance it belongs to, and devices without multi-instance
support report a default instance of kind `DEFAULT`.

## Boundaries

Imports: net/addr, net/key

Imported by: nothing FlowSeer-owned

Deliberately absent:

- Routing tables, next hops, and protocol state. Routing belongs in `net/routing`,
  and protocol tables belong in their own `net/protocol/<x>` packages.
- Cross-instance route leaking or VRF route targets. Route targets are BGP
  extended communities and belong to routing protocol configuration.
- Device identity, tenant context, and provenance. A network instance is a
  device-scoped table row; context lives on the owning device and event envelope.

## Contents

- `network_instance_kind.proto` — `NetworkInstanceKind` enum normalized from
  OpenConfig (`openconfig-network-instance-types.yang`): `DEFAULT`, `L3VRF`,
  `L2VSI`, `L2P2P`, and `L2L3`.
- `route_distinguisher.proto` — `RouteDistinguisher` typed variant with required
  `format` oneof (`as2`, `ipv4`, `as4`) enforcing RFC 4364 §4.2 field widths and
  bounds.
- `network_instance.proto` — `NetworkInstance` table row with required `name`
  validated by `flowseer.net.key.v1.network_instance_name`, required `kind`,
  optional `route_distinguisher`, and optional `description`.

## Sources

- RFC 4364 §4.2 (<https://www.rfc-editor.org/rfc/rfc4364.html#section-4.2>) for
  Route Distinguisher binary formats and field bounds.
- OpenConfig `openconfig-network-instance-types.yang:144-176`
  (`spec/yang/openconfig/openconfig-network-instance-types.yang`) for network
  instance kind identities.
