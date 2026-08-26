# FlowSeer protobuf packages

FlowSeer-owned schemas use Protobuf Edition 2024. Package directories mirror
their versioned protobuf package names.

## Network packages

- `flowseer.net.addr.v1` owns canonical IP, prefix, range, lifetime, MAC, EUI,
  and OUI value types.
- `flowseer.net.packet.v1` owns packet-header registries, exact header values,
  and small reusable match atoms.
- `flowseer.net.phy.v1` owns Ethernet settings, capabilities, active link facts,
  PoE, and shallow pluggable-transceiver observations.
- `flowseer.net.l2.v1` owns VLANs, exact tag stacks, switchport membership,
  aggregation attributes, and unicast forwarding-database rows.
- `flowseer.net.l3.v1` owns per-interface IPv4/IPv6 facets, assigned-address
  rows, and the ARP/IPv6-ND neighbor cache.

Table rows deliberately carry device-local interface names instead of entity
refs. Device identity, tenancy, observation time, and lifecycle belong to the
entity or provenance envelope that carries these values. Network instances,
RIBs, FIBs, and routes require future instance-aware packages rather than being
folded into the L3 interface model.

## Standards grounding

The normalized fields and enums draw from these primary sources:

- [RFC 8344](https://www.rfc-editor.org/rfc/rfc8344.html) for interface address
  origins and statuses and for ARP/IPv6 neighbor table shape.
- [RFC 4861](https://www.rfc-editor.org/rfc/rfc4861.html) and
  [RFC 8335](https://www.rfc-editor.org/rfc/rfc8335.html) for neighbor
  reachability values.
- [RFC 4293](https://www.rfc-editor.org/rfc/rfc4293.html) for IP-to-link-layer
  mapping types.
- [RFC 8349](https://www.rfc-editor.org/rfc/rfc8349.html) and
  [RFC 8529](https://www.rfc-editor.org/rfc/rfc8529.html) for the routing and
  network-instance boundaries deliberately kept outside `net.l3`.
