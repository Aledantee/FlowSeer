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

Imports: net/addr

Imported by: nothing

A protocol package may import primitives from lower layers (`net/addr`,
`net/packet`, `net/phy`, `net/switching`, `net/ip`, `net/interface`) and never
another protocol package. Protocol packages are not imported by other `net/`
packages.

## Packages

- `lacp/v1/`: Link Aggregation Control Protocol aggregator and member port states.
- `lldp/v1/`: Link Layer Discovery Protocol local-system, port, and neighbor observations.
- `stp/v1/`: Spanning Tree Protocol bridge and port states and timers.
