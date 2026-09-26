# IS-IS Protocol Primitives

The `flowseer.net.protocol.isis.v1` package models Intermediate System to
Intermediate System (IS-IS) instances, levels, and circuit adjacencies.

## Boundaries

Imports: net/addr, net/key

Imported by: nothing

Deliberately absent:

- IS-IS link state database (LSDB), link state PDUs (LSPs), and sequence number PDUs (CSNP/PSNP).
- IS-IS circuits, authentication keys, and metric style (narrow/wide) details.
- Mesh groups and three-way handshake parameters.

## Design decisions

- **Key split**: `IsisInstance` is keyed by `(network_instance, protocol_instance)`.
  `IsisAdjacency` is keyed by `interface_name`, `protocol_instance`,
  `neighbor_system_id`, and `usage` (Level 1, Level 2, or Level 1 and 2),
  without a `network_instance` field; the network instance is inherited from
  the interface row.
- **System identifiers and area addresses**: System IDs are 6 octets (`bytes`,
  length 6). Area addresses are 1 to 20 octets (RFC 1195, ISO/IEC 10589).
- **Adjacency states and levels**: `IsisAdjacencyState` numbers the four states
  `DOWN = 1`, `INITIALIZING = 2`, `UP = 3`, `FAILED = 4` following ISIS-MIB
  (`isisISAdjState`, RFC 4444). OpenConfig defines the same labels in different
  order; IOS-XE reports `standby` where standard models report `failed`, which
  a mapper leaves absent rather than guessing. `IsisLevel` numbers
  `LEVEL1 = 1`, `LEVEL2 = 2`, `LEVEL1_AND_LEVEL2 = 3` following ISIS-MIB `IsisLevel`.

## Contents

- `isis_adjacency_state.proto` — `IsisAdjacencyState`: IS-IS circuit adjacency operational states.
- `isis_adjacency.proto` — `IsisAdjacency`: IS-IS neighbor adjacencies, state, and hold times.
- `isis_instance.proto` — `IsisInstance`: IS-IS routing process instance, system ID, and area addresses.
- `isis_level.proto` — `IsisLevel`: IS-IS routing levels (Level 1, Level 2, Level 1 and 2).

## Sources

- ISO/IEC 10589 for Intermediate System to Intermediate System intra-domain routeing information exchange protocol.
- RFC 1195 (<https://www.rfc-editor.org/rfc/rfc1195.html>) for Use of OSI IS-IS for Routing in TCP/IP and Dual Environments.
- RFC 4444 (<https://www.rfc-editor.org/rfc/rfc4444.html>) for Management Information Base for Intermediate System to Intermediate System (IS-IS).
- IETF ISIS-MIB (`spec/mib/ietf/ISIS-MIB`).
- OpenConfig `openconfig-isis.yang` and `openconfig-isis-types.yang`.
- Cisco IOS-XE `Cisco-IOS-XE-isis-oper.yang`.
