# Layer 2 Primitives

The `flowseer.net.l2.v1` package defines device-local layer-2 values. These
messages carry no device identity, tenant, lifecycle, or provenance, so device
entities and discovery payloads can embed the same values.

VLAN identifiers remain scalar `uint32` fields. The package's predefined
protovalidate rule supplies their shared `1..4094` contract without adding a
wrapper message or a second level of presence.
