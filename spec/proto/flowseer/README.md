# FlowSeer protobuf packages

FlowSeer-owned schemas use Protobuf Edition 2024. Package directories mirror
their versioned protobuf package names.

## Network packages

- `flowseer.net.addr.v1` owns canonical IP, prefix, range, lifetime, MAC, EUI,
  and OUI value types.
- `flowseer.ip.v1` owns device-local layer-3 table primitives: interface address
  assignments, ARP and IPv6 neighbor-cache rows, RIB descriptors, routes, and
  weighted next hops. It imports address values from `flowseer.net.addr.v1` and
  does not redefine them.

The IP table primitives deliberately carry device-local interface and routing
table names instead of entity refs. Device identity, tenancy, observation time,
and lifecycle belong to the entity or provenance envelope that carries these
values.

## Standards grounding

The normalized fields and enums draw from these primary sources:

- [RFC 8344](https://www.rfc-editor.org/rfc/rfc8344.html) for interface address
  origins and statuses and for ARP/IPv6 neighbor table shape.
- [RFC 8349](https://www.rfc-editor.org/rfc/rfc8349.html) for RIB address-family
  boundaries, route preference, and next-hop structure.
- [RFC 4861](https://www.rfc-editor.org/rfc/rfc4861.html) and
  [RFC 8335](https://www.rfc-editor.org/rfc/rfc8335.html) for neighbor
  reachability states and state numbers.
- [RFC 4292](https://www.rfc-editor.org/rfc/rfc4292.html) and
  [RFC 4293](https://www.rfc-editor.org/rfc/rfc4293.html) for forwarding-table
  route attributes and IP-to-link-layer mapping types.
- [OpenConfig AFT](https://github.com/openconfig/public/tree/master/release/models/aft)
  for weighted next-hop groups and cross-table next-hop resolution.
- [Linux route netlink](https://www.kernel.org/doc/html/next/networking/netlink_spec/rt-route.html)
  for commonly implemented local, discard, reject, and throw route actions.
