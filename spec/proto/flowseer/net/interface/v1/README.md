# Interface Primitives

The `flowseer.net.interface.v1` package defines the normalized interface: the
attributes every consumer reads, one arm per interface kind, the administrative
and operational status taxonomies, and generic traffic counters.

## Boundaries

Imports: net/addr, net/ip, net/phy, net/switching

Imported by: model/access

Deliberately absent:

- Device refs and an Interface entity ref pair. Primitives here are ref-free and
  use device-local names.
- Observation time and provenance. Those belong to the carrying envelope.
- Routing domains and VRFs.

Kind is a required `oneof`, so an interface cannot carry attributes its kind
has no meaning for. The IP facet sits outside that `oneof` because routing is a
persona rather than a kind: a routed port, a routed aggregation, a switched
virtual interface, and a loopback share one facet, and its presence is what
says the interface is routed. Interfaces name each other by their device-local
name — a member port names its aggregation, a subinterface names its parent —
so the messages stay free of references.

The package holds one interface identity: the name, plus the SNMP index when a
source has one. Resolving the other port-numbering spaces a device exposes,
such as the bridge port number or the LLDP local port number, is the collecting
mapper's job.

Kinds with no surveyed source attributes are empty arm messages. They classify
the interface and gain fields when a source provides them.

Device identity and lifecycle belong to the entity model. Provenance and
observation time belong to the carrying envelope, and tenancy remains ambient
context. An Interface entity and its references have not landed; their future
package remains unsettled. Do not add them to this primitive package or infer
an `api/interface/v1` boundary. Amend the accepted network-model direction
before adding the entity family.

## Sources

The package's field and enum contracts cite:

- [RFC 2863](https://www.rfc-editor.org/rfc/rfc2863.html) for the interface
  status taxonomies and the traffic counter definitions.
- [IANAifType-MIB](https://www.iana.org/assignments/ianaiftype-mib/ianaiftype-mib)
  for the interface type carried by the unclassified arm.
- [IEEE 802.1Q](https://standards.ieee.org/ieee/802.1Q/10323/) for the routed
  VLAN identifier and subinterface tag encapsulation.
