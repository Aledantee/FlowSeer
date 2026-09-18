# Layer 2 Switching Primitives

The `flowseer.net.switching.v1` package defines device-local,
protocol-independent Ethernet switching values. It owns VLAN database rows,
exact 802.1Q tag stacks, port-side VLAN membership, aggregation attributes, and
unicast forwarding database rows.

## Boundaries

Imports: net/addr, net/packet

Imported by: net/capture, net/interface

Deliberately absent:

- Protocol-specific state (STP, LACP).
- Device and interface entity references.
- Observation time, provenance, and tenant context.

VLAN identifiers remain scalar `uint32` fields. The package's predefined
Protovalidate rules distinguish usable VLAN identifiers (`1..4094`) from the
tag VID field (`0..4094`), where zero represents a priority tag. Exact tag
stacks are ordered outermost to innermost and are not configuration match
expressions.

STP, LACP, and registration protocols own separate protocol packages. Device
identity, tenant, lifecycle, provenance, observation time, and collector health
remain in entities and envelopes rather than these reusable values.

## Sources

The package's field and enum contracts cite:

- [IEEE 802.1Q](https://standards.ieee.org/ieee/802.1Q/10323/) for VLAN
  identifiers, tags, and priority code points.
- [RFC 4363](https://www.rfc-editor.org/rfc/rfc4363.html) for VLAN bridge
  (Q-BRIDGE-MIB) semantics.
- [RFC 9542, section 2.1](https://www.rfc-editor.org/rfc/rfc9542.html#section-2.1)
  for MAC address usage conventions.
- [Protovalidate](https://protovalidate.com/) predefined rules for the shared
  VLAN-identifier range constraints.
