# Network Primitives

## Identity

The `net/` root holds ref-free networking values representing addresses, packet
headers, physical links, switching tables, and protocol observations. Primitives
here are shared domain vocabulary carrying no tenant, lifecycle, or observation
timestamp (except where packet capture timestamps are intrinsic to the pcap
record).

## Admission

A package belongs in `net/` if its messages describe network facts independently
of which device or entity produced them, with no FlowSeer identity or
persistence lifecycle. `net/addr` passes because an IP prefix or MAC address is
identical regardless of context. An entity like `Edge` or `Device` fails
admission because it carries a FlowSeer-assigned UUID and lifecycle state; it
belongs in `model/`.

## Boundaries

Imports: nothing FlowSeer-owned

Imported by: api/capture, api/edge, model/access, model/capture, model/inventory, store/device

Packages under `net/` are leaves with respect to every other root: nothing here
imports outside `net/`, and any root may import them.

## Packages

- `addr/v1/`: Canonical IP, prefix, range, lifetime, MAC, EUI, and OUI value types.
- `packet/v1/`: Packet-header registries, exact header values, and small reusable match atoms.
- `phy/v1/`: Ethernet settings, capabilities, active link facts, MAU types, transport arms, pluggable module, and PoE.
- `switching/v1/`: VLAN database rows, exact tag stacks, switchport membership, aggregation attributes, and forwarding entries.
- `ip/v1/`: Per-interface IPv4 and IPv6 facets, assigned-address rows, and neighbor cache.
- `capture/v1/`: Ref-free packet capture values, counters, filter clauses, mirror encapsulation, and packet records.
- `interface/v1/`: Normalized interface message with kind-specific oneof arms and routed facet.
- `protocol/`: Protocol-specific observation tables and state machines.

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
