# IP Interface Primitives

The `flowseer.net.ip.v1` package defines protocol-independent IP-on-interface
values: per-family enablement, forwarding, and MTU facets; assigned-address
rows; and the device-local ARP and IPv6 Neighbor Discovery cache.

## Boundaries

Imports: net/addr

Imported by: net/interface

Deliberately absent:

- VRFs, network instances, RIBs, FIBs, and routing policies.
- Device refs, observation time, and provenance.
- Protocol-specific routing state (BGP, OSPF).

The package does not own network instances, VRFs, RIBs, FIBs, routing policy,
multicast forwarding, tunnels, or protocol-specific state. Those domains need
separate packages whose keys can distinguish network instances and multiple
instances of one routing protocol.

Rows carry device-local interface names rather than entity references. Device
identity, tenant, lifecycle, provenance, observation time, and collector health
belong to the entity or envelope that carries these reusable values.

## Sources

The package's field and enum contracts cite:

- [RFC 8344](https://www.rfc-editor.org/rfc/rfc8344.html) for interface IP
  facets, MTU ranges, address origin and status taxonomies, and neighbor
  origins.
- [RFC 4293](https://www.rfc-editor.org/rfc/rfc4293.html) for the IP-MIB
  link-layer mapping and neighbor-type semantics.
- [RFC 4861, section 7.3.2](https://www.rfc-editor.org/rfc/rfc4861.html#section-7.3.2)
  for Neighbor Unreachability Detection states.
- [RFC 8200, section 5](https://www.rfc-editor.org/rfc/rfc8200.html#section-5)
  for the IPv6 minimum link MTU.
