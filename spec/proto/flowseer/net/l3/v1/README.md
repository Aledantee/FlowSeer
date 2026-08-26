# IP Interface Primitives

The `flowseer.net.l3.v1` package defines protocol-independent IP-on-interface
values: per-family enablement, forwarding, and MTU facets; assigned-address
rows; and the device-local ARP and IPv6 Neighbor Discovery cache.

The package does not own network instances, VRFs, RIBs, FIBs, routing policy,
multicast forwarding, tunnels, or protocol-specific state. Those domains need
separate packages whose keys can distinguish network instances and multiple
instances of one routing protocol.

Rows carry device-local interface names rather than entity references. Device
identity, tenant, lifecycle, provenance, observation time, and collector health
belong to the entity or envelope that carries these reusable values.
