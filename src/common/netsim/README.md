# netsim

Pure Go network simulation. Simulators in this tree evaluate forwarding rules
and state transitions in memory. Execution is synchronous with no background
goroutines or wall-clock dependencies.

| Package            | What it does                                              |
| ------------------ | ------------------------------------------------------------ |
| `../net/igmp`      | IGMPv1, IGMPv2, and IGMPv3 message codec                    |
| `../net/mld`       | MLDv1 and MLDv2 message codec                               |
| `trace`            | Step, outcome, and change trace records                      |
| `analysis`         | Analysis trust metadata, scoped issues, and evidence catalog |
| `vswitch`          | Virtual switch composing pipeline capabilities               |
| `vswitch/port`     | Port table, administrative state, and MTU                    |
| `vswitch/lag`      | Bond modes, member delays, LACP                              |
| `vswitch/phy`      | Physical Ethernet speeds and PoE budget allocation           |
| `vswitch/bridge`   | Filtering database, VLAN classification, and tagging         |
| `vswitch/mcast`    | Per-VLAN multicast memberships, router ports, and aging      |
| `vswitch/routing`  | Routed interfaces, per-VRF forwarding and neighbor tables    |
| `vswitch/stp`      | Rapid Spanning Tree Protocol state machine and BPDUs         |
| `vswitch/netmodel` | Translation boundary for FlowSeer network model protos       |
| `fabric`           | Switched topology, timed cables, stepped execution, journeys |

A run is a function of the configuration, the frame, and the time the caller
passes; nothing here reads a clock.
