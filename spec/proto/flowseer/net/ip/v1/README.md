# IP Interface Primitives

The `flowseer.net.ip.v1` package defines protocol-independent IP-on-interface
values: per-family enablement, forwarding, and MTU facets; assigned-address
rows; and the device-local ARP and IPv6 Neighbor Discovery cache.

## Boundaries

Imports: net/addr, net/key

Imported by: net/interface

Deliberately absent:

- The network-instance row and routing tables. `net/instance` and
  `net/routing` own them; `IpFacet` names its instance by key.
- Routing policies.
- Device refs, observation time, and provenance.
- Protocol-specific routing state (BGP, OSPF).

The package does not own network instances, VRFs, RIBs, FIBs, routing policy,
multicast forwarding, tunnels, or protocol-specific state. Those domains need
separate packages whose keys can distinguish network instances and multiple
instances of one routing protocol.

A routed interface names its network instance once, in the required
`IpFacet.network_instance`. `InterfaceAddress` and `NeighborEntry` rows are
keyed by interface name and inherit that instance rather than repeat it, so an
address and its interface cannot disagree about which VRF they are in.

Rows carry device-local interface names rather than entity references. Device
identity, tenant, lifecycle, provenance, observation time, and collector health
belong to the entity or envelope that carries these reusable values.

## Contents

- Per-family facets: `IpFacet`, `Ipv4Facet`, and `Ipv6Facet`.
- Assigned-address rows: `InterfaceAddress` with `AddressOrigin`,
  `AddressStatus`, and `InterfaceIdentifierMethod`.
- The neighbor cache: `NeighborEntry` with `NeighborOrigin` and
  `NeighborReachability`.

An address row keeps two facts apart that RFC 8344's ip-address-origin
merges. `AddressOrigin` is the assignment mechanism (static, DHCP, SLAAC,
link-local), and `InterfaceIdentifierMethod` in
`interface_identifier_method.proto` is how an IPv6 interface identifier was
generated (modified EUI-64, stable opaque, temporary, randomized). RFC 8344's
`link-layer` and `random` are both SLAAC and differ only in the identifier, so
a mapper writes `link-layer` as SLAAC with modified EUI-64 and `random` as
SLAAC with randomized. The identifier method is valid only on an IPv6 address.

## Sources

The package's field and enum contracts cite:

- [RFC 8344](https://www.rfc-editor.org/rfc/rfc8344.html) for interface IP
  facets, MTU ranges, address origin and status taxonomies, and neighbor
  origins.
- [RFC 4862](https://www.rfc-editor.org/rfc/rfc4862.html) and
  [RFC 3927](https://www.rfc-editor.org/rfc/rfc3927.html) for stateless and
  link-local address origins.
- [RFC 4291, Appendix A](https://www.rfc-editor.org/rfc/rfc4291.html#appendix-A),
  [RFC 7217](https://www.rfc-editor.org/rfc/rfc7217.html), and
  [RFC 8981](https://www.rfc-editor.org/rfc/rfc8981.html) for interface
  identifier methods.
- [RFC 4293](https://www.rfc-editor.org/rfc/rfc4293.html) for the IP-MIB
  link-layer mapping and neighbor-type semantics.
- [RFC 4861, section 7.3.2](https://www.rfc-editor.org/rfc/rfc4861.html#section-7.3.2)
  for Neighbor Unreachability Detection states.
- [RFC 8200, section 5](https://www.rfc-editor.org/rfc/rfc8200.html#section-5)
  for the IPv6 minimum link MTU.
