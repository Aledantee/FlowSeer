# netsim

Pure Go network simulation. Simulators in this tree evaluate forwarding rules
and state transitions in memory. Execution is synchronous with no background
goroutines or wall-clock dependencies.

| Package            | What it does                                              |
| ------------------ | ------------------------------------------------------------ |
| `../net/igmp`      | IGMPv1, IGMPv2, and IGMPv3 message codec                    |
| `../net/mld`       | MLDv1 and MLDv2 message codec                               |
| `../net/udp`       | UDP header codec with pseudo-header checksums               |
| `../net/tcp`       | TCP header decoder: ports, sequence numbers, control bits   |
| `../net/icmp`      | ICMPv4 and ICMPv6 header decoder: type, code, checksum       |
| `trace`            | Step, outcome, and change trace records                      |
| `analysis`         | Analysis trust metadata, scoped issues, and evidence catalog |
| `vswitch`          | Virtual switch composing pipeline capabilities               |
| `vswitch/port`     | Port table, administrative state, and MTU                    |
| `vswitch/lag`      | Bond modes, the 256-bucket member selection table, member delays, LACP |
| `vswitch/phy`      | Physical Ethernet speeds and PoE budget allocation           |
| `vswitch/bridge`   | Filtering database, VLAN classification, and tagging         |
| `vswitch/mcast`    | Per-port RFC 3376/MLDv2 router state, router ports, and aging |
| `vswitch/routing`  | Routed interfaces, per-VRF tables, equal-cost selection, recursive next hops |
| `vswitch/stp`      | Rapid Spanning Tree Protocol state machine, BPDUs, and port guards |
| `vswitch/loopprotect` | netsim's own loop-protection probe and per-port block/no-learn action, independent of spanning tree |
| `vswitch/netmodel` | Translation boundary for FlowSeer network model protos       |
| `fabric`           | Switched topology, timed cables, stepped execution, journeys |

A run is a function of the configuration, the frame, and the time the caller
passes; nothing here reads a clock.

## Conformance corpus

`internal/netsimtest` holds a versioned corpus of executable conformance
cases. It locks behavioral contracts across these packages: planning
comparisons, topology-shadowing constructions with partial or uncertain
physical and topology facts, and troubleshooting traces. Each case pairs an
evaluated question and the false answer it prevents with an exact,
structured expectation of trust metadata, decisive trace steps, and
configuration diffs. It is internal and imported only by this tree's own
external test packages (`vswitch_test`, `netmodel_test`); see
`internal/netsimtest/README.md` for the admitted cases and admission bar.
