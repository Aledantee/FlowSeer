# Routing Table Primitives

The `flowseer.net.routing.v1` package defines the device-scoped routing
table: the `Route` row, its next hops, the mechanism that learned it, and
which table the row came from. Every route names the network instance whose
table holds it.

## Boundaries

Imports: net/addr, net/key

Imported by: nothing FlowSeer-owned

Deliberately absent:

- Routing-protocol state (BGP peers, OSPF adjacencies, IS-IS levels). Each
  protocol's tables belong in its own `net/protocol/<x>` package.
- Multicast routes. Multicast route sources come from the separate
  `IANAipMRouteProtocol` registry and share nothing with `RouteSourceProtocol`.
- The network-instance row itself. `net/instance` owns it; a route names its
  instance by key.
- Device identity, tenant context, and provenance. A route is a
  device-scoped table row; context lives on the owning device and event
  envelope.

## Contents

- `route_source_protocol.proto` — `RouteSourceProtocol`, a registry
  pass-through of `IANAipRouteProtocol`. A static route is `NETMGMT`.
- `route_table_type.proto` — `RouteTableType`, which says whether a row came
  from the RIB or the FIB. The standard SNMP routing tables do not say, so an
  SNMP-sourced route leaves it unspecified rather than guess.
- `special_next_hop.proto` — `SpecialNextHop`, the four RFC 8349 actions that
  discard or locally receive a packet.
- `next_hop.proto` — `NextHop`, a typed variant whose required `target` oneof
  is either a `ForwardingNextHop` (outgoing interface, gateway address, or
  both) or a `SpecialNextHop`. Protobuf keeps at most one oneof arm, so a next
  hop that both forwards and discards cannot be sent; validation rejects only
  the next hop with no arm.
- `next_hop_group.proto` — `NextHopGroup`, one or more next hops; several
  model an equal-cost multipath route.
- `route.proto` — `Route` table row keyed by `network_instance` and
  `destination_prefix`.

A static default route in the default instance, as protobuf text format:

```textproto
network_instance: "default"
destination_prefix { v4 { address { octets: "\x00\x00\x00\x00" } length: 0 } }
source_protocol: ROUTE_SOURCE_PROTOCOL_NETMGMT
next_hop_group {
  next_hops { forwarding { address { v4 { octets: "\xc0\x00\x02\x01" } } } }
}
```

## Sources

- [RFC 8349](https://www.rfc-editor.org/rfc/rfc8349.html) §5.1 and §5.2 for
  routes, route preference, and RIBs, and §7 (the `ietf-routing` module) for
  `next-hop-list`, `special-next-hop`, and `active`.
- `IANAipRouteProtocol` (`spec/mib/ietf/IANA-RTPROTO-MIB:48-79`) for source
  protocol values, and [RFC 4292](https://www.rfc-editor.org/rfc/rfc4292.html)
  `inetCidrRouteProto`, which reports them.
