# OSPF Protocol Primitives

The `flowseer.net.protocol.ospf.v1` package models Open Shortest Path First (OSPF)
routing instances, areas, interfaces, and neighbor conversations for OSPFv2
(IPv4) and OSPFv3 (IPv6).

## Boundaries

Imports: net/addr, net/key

Imported by: nothing

Deliberately absent:

- OSPF LSDB (link-state database), LSA payloads, and virtual links (atlas, "The shared shape": adjacency first, databases rarely).
- Designated router election details (DR/BDR router IDs and IP addresses) and cryptographic authentication state.
- OSPF multi-area adjacency and sham links.

## Design decisions

- **Key split**: Instance-level rows (`OspfInstance`, `OspfArea`) are keyed by
  `(network_instance, version, protocol_instance)`. Interface-keyed rows
  (`OspfInterface`, `OspfNeighbor`) are keyed by `interface_name` and
  `(version, protocol_instance)` without a `network_instance` field; the network
  instance is inherited from the interface row.
- **Version key and instance ID**: OSPFv2 (RFC 2328, version 2) and OSPFv3
  (RFC 5340, version 3) share the tables. `instance_id` (0 to 255) is an
  OSPFv3-specific interface instance identifier (`Ospfv3IfInstIdTC`); it is
  valid only when `version == 3`. On `OspfNeighbor`, `neighbor_address` must
  match the version family: IPv4 for version 2, IPv6 for version 3.
- **Area identifier**: `area_id` is a 32-bit integer (`uint32`). 0 represents
  the backbone area (0.0.0.0). A mapper reading a dotted-quad area string (e.g.
  `192.0.2.0`) converts the four octets to a 32-bit big-endian integer:
  `(a << 24) | (b << 16) | (c << 8) | d`.
- **Interface types and states**: `OspfInterfaceType` keeps the MIB registry
  gap: value 4 is unassigned in OSPF-MIB and OSPFV3-MIB, and stays so.
  `OspfInterfaceState` includes `STANDBY = 8`, which OSPFv3 reports.

## Contents

- `ospf_area_type.proto` — `OspfAreaType`: area types (normal, stub, NSSA).
- `ospf_area.proto` — `OspfArea`: OSPF area metrics and border router counts.
- `ospf_instance.proto` — `OspfInstance`: OSPF routing process instance, version, and router ID.
- `ospf_interface_state.proto` — `OspfInterfaceState`: OSPF interface operational states.
- `ospf_interface_type.proto` — `OspfInterfaceType`: OSPF interface network types with the MIB gap at 4.
- `ospf_interface.proto` — `OspfInterface`: OSPF interface configuration, state, and timers.
- `ospf_neighbor_state.proto` — `OspfNeighborState`: OSPF neighbor conversation FSM states.
- `ospf_neighbor.proto` — `OspfNeighbor`: OSPF neighbor router conversations and states.

## Sources

- RFC 2328 (<https://www.rfc-editor.org/rfc/rfc2328.html>) for OSPF Version 2.
- RFC 5340 (<https://www.rfc-editor.org/rfc/rfc5340.html>) for OSPF for IPv6 (OSPFv3).
- RFC 3101 (<https://www.rfc-editor.org/rfc/rfc3101.html>) for OSPF NSSA Option.
- IETF OSPF-MIB (`spec/mib/ietf/OSPF-MIB`) for OSPFv2 MIB objects.
- IETF OSPFV3-MIB (`spec/mib/ietf/OSPFV3-MIB`) for OSPFv3 MIB objects.
- OpenConfig `openconfig-ospfv2.yang`.
