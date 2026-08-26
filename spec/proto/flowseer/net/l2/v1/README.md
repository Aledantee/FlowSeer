# Layer 2 Switching Primitives

The `flowseer.net.l2.v1` package defines device-local, protocol-independent
Ethernet switching values. It owns VLAN database rows, exact 802.1Q tag stacks,
port-side VLAN membership, aggregation attributes, and unicast forwarding
database rows.

VLAN identifiers remain scalar `uint32` fields. The package's predefined
Protovalidate rules distinguish usable VLAN identifiers (`1..4094`) from the
tag VID field (`0..4094`), where zero represents a priority tag. Exact tag
stacks are ordered outermost to innermost and are not configuration match
expressions.

STP, LACP, and registration protocols own separate protocol packages. Device
identity, tenant, lifecycle, provenance, observation time, and collector health
remain in entities and envelopes rather than these reusable values.
