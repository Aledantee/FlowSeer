# Network Protocol Primitives

## Identity

The `net/protocol/` directory holds protocol observation tables and state
machines for standard link-layer and network protocols. Rows identify their
local interface by device-local name rather than entity references, so walking
protocol state stands on its own without grafting facets onto interface models.

## Admission

A package belongs in `net/protocol/` if it models protocol-specific state tables
that a device observes or participates in. `net/protocol/lldp` passes because it
models LLDP local-system, port, and neighbor tables. `net/switching` fails
admission because VLANs and MAC forwarding are general Layer 2 bridging
facilities, not an individual control protocol.

## Boundaries

Imports: net/addr, net/key, net/packet, net/phy, net/switching

Imported by: nothing

A protocol package may import primitives from lower layers (`net/addr`,
`net/packet`, `net/phy`, `net/switching`, `net/ip`, `net/interface`) and never
another protocol package. Protocol packages are not imported by other `net/`
packages.

## Packages

- `lacp/v1/`: Link Aggregation Control Protocol aggregator and member port states.
- `lldp/v1/`: Link Layer Discovery Protocol local-system, port, and neighbor observations, with the neighbors' IEEE 802.3 and LLDP-MED extensions.
- `stp/v1/`: Spanning Tree Protocol bridge and port states and timers, MSTIs, and the VLAN-to-MSTI map.
- `cdp/v1/` (planned; schema building blocks record): CDP neighbors.
- `ntp/v1/` (planned; schema building blocks record): NTP associations.
- `dhcp/v1/` (planned; schema building blocks record): Leases, server pools, snooping bindings.
- `dns/v1/` (planned; schema building blocks record): Resolver configuration and servers.
- `bgp/v1/` (planned; schema building blocks record): Peers, address families, communities.
- `ospf/v1/` (planned; schema building blocks record): Neighbors, areas, interface types (v2 and v3).
- `isis/v1/` (planned; schema building blocks record): Adjacencies and levels.
- `vrrp/v1/` (planned; schema building blocks record): VRRP groups (v2 and v3).
- `bfd/v1/` (planned; schema building blocks record): Sessions and diagnostics.
