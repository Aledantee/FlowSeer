# FlowSeer protobuf packages

FlowSeer-owned schemas use Protobuf Edition 2024. Package directories mirror
their versioned protobuf package names.

## Network packages

- `flowseer.net.addr.v1` owns canonical IP, prefix, range, lifetime, MAC, EUI,
  and OUI value types.
- `flowseer.net.packet.v1` owns packet-header registries, exact header values,
  and small reusable match atoms.
- `flowseer.net.phy.v1` owns Ethernet settings, capabilities, active link facts,
  MAU types and link modes, Ethernet error counters, a transport oneof whose
  copper arm carries PoE, the pluggable module with its per-lane diagnostics,
  and the PSE power budget.
- `flowseer.net.switching.v1` owns VLANs, exact tag stacks, switchport
  membership, aggregation attributes, and unicast forwarding-database rows.
- `flowseer.net.ip.v1` owns per-interface IPv4/IPv6 facets, assigned-address
  rows, and the ARP/IPv6-ND neighbor cache.
- `flowseer.net.interface.v1` owns normalized interfaces, their kind-specific
  attributes, status taxonomies, and generic traffic counters. It composes the
  optional routed facet from `flowseer.net.ip.v1`.
- `flowseer.net.protocol.lldp.v1` owns LLDP local-system, port, and neighbor
  observations. Neighbor rows identify their local interface by device-local
  name.

Table rows deliberately carry device-local interface names instead of entity
refs. Device identity and lifecycle belong to the entity. Observation time
belongs to the provenance envelope, while tenancy remains ambient context.
Network instances, RIBs, FIBs, and routes require future instance-aware
packages rather than being folded into the L3 interface model.

## Entity and runtime packages

- `flowseer.model.inventory.v1` owns the landed inventory entities, their refs,
  lifecycle events, and provenance.
- `flowseer.service.v1` owns the process-local durable mailbox and runtime
  control records. It is not the future ConnectRPC service API; that boundary's
  package remains unsettled.

## Standards grounding

Every normalized field and enum cites its primary source inline in the `.proto`
comment; each package README lists the sources that package uses. Across the
network packages those sources are:

IETF RFCs:

- [RFC 2474](https://www.rfc-editor.org/rfc/rfc2474.html),
  [RFC 2597](https://www.rfc-editor.org/rfc/rfc2597.html),
  [RFC 3168](https://www.rfc-editor.org/rfc/rfc3168.html),
  [RFC 3246](https://www.rfc-editor.org/rfc/rfc3246.html),
  [RFC 5865](https://www.rfc-editor.org/rfc/rfc5865.html),
  [RFC 8622](https://www.rfc-editor.org/rfc/rfc8622.html), and
  [RFC 9956](https://www.rfc-editor.org/rfc/rfc9956.html) for the DiffServ
  field, per-hop behaviors, and ECN.
- [RFC 8200](https://www.rfc-editor.org/rfc/rfc8200.html),
  [RFC 9293](https://www.rfc-editor.org/rfc/rfc9293.html),
  [RFC 5332](https://www.rfc-editor.org/rfc/rfc5332.html), and
  [RFC 9542](https://www.rfc-editor.org/rfc/rfc9542.html) for IPv6 and TCP
  header semantics, MPLS EtherType meanings, and IEEE 802 parameter usage.
- [RFC 4861](https://www.rfc-editor.org/rfc/rfc4861.html) and
  [RFC 8415](https://www.rfc-editor.org/rfc/rfc8415.html) for Neighbor
  Discovery (option link-layer addresses, reachability states) and address
  lifetimes.
- [RFC 8344](https://www.rfc-editor.org/rfc/rfc8344.html) and
  [RFC 4293](https://www.rfc-editor.org/rfc/rfc4293.html) for interface IP
  facets, address origins, and the neighbor-cache shape.
- [RFC 3635](https://www.rfc-editor.org/rfc/rfc3635.html),
  [RFC 3621](https://www.rfc-editor.org/rfc/rfc3621.html), and
  [RFC 4363](https://www.rfc-editor.org/rfc/rfc4363.html) for Ethernet-like
  interface, Power over Ethernet, and VLAN bridge MIB semantics.

IANA registries:

- [Address Family Numbers](https://www.iana.org/assignments/address-family-numbers)
- [Differentiated Services Field Codepoints](https://www.iana.org/assignments/dscp-registry)
- [ICMP Type Numbers](https://www.iana.org/assignments/icmp-parameters)
- [ICMPv6 Parameters](https://www.iana.org/assignments/icmpv6-parameters)
- [IEEE 802 Numbers](https://www.iana.org/assignments/ieee-802-numbers)
- [Assigned Internet Protocol Numbers](https://www.iana.org/assignments/protocol-numbers)

IEEE and other bodies:

- [IEEE 802.1Q](https://standards.ieee.org/ieee/802.1Q/10323/) for VLAN tags,
  identifiers, and priority code points.
- [IEEE 802.3](https://standards.ieee.org/ieee/802.3/10422/) for Ethernet
  physical-layer capabilities and auto-negotiation.
- The [IEEE Registration Authority](https://standards.ieee.org/products-programs/regauth/)
  for EUI-48, EUI-64, and OUI identifier formats.
- [SNIA SFF-8024](https://members.snia.org/document/dl/26423) for pluggable
  transceiver form-factor identifiers.

Validation contracts are expressed with
[Protovalidate](https://protovalidate.com/) rules compiled at lint time.
