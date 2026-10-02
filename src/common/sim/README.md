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
| `stream`           | Finite Ethernet frame sources with deterministic timing      |
| `vswitch`          | Virtual switch composing pipeline capabilities               |
| `vswitch/port`     | Port table, administrative state, and MTU                    |
| `vswitch/lag`      | Bond modes, the 256-bucket member selection table, member delays, LACP |
| `vswitch/phy`      | Physical Ethernet speeds and PoE budget allocation           |
| `vswitch/bridge`   | Filtering database, VLAN classification, and tagging         |
| `vswitch/mcast`    | Per-port RFC 3376/MLDv2 router state, router ports, and aging |
| `vswitch/routing`  | Routed interfaces, per-VRF tables, equal-cost selection, recursive next hops |
| `vswitch/filter`   | Interface-bound access-control rules and stateful reverse matches |
| `vswitch/stp`      | Rapid Spanning Tree Protocol state machine, BPDUs, and port guards |
| `vswitch/loopprotect` | netsim's own loop-protection probe and per-port block/no-learn action, independent of spanning tree |
| `vswitch/netmodel` | Translation boundary for FlowSeer network model protos       |
| `fabric`           | Switched topology, timed cables, stepped execution, journeys |
| `search`           | Bounded differential search, counterexample minimization, trace alignment |

A run is a function of the configuration, the frame, and the time the caller
passes; nothing here reads a clock.

Simulation workflows can be driven step-by-step, executed through declared
scenarios via `fabric.Fabric.RunScenario(scenario)`, and reproduced
deterministically from recorded specifications via `fabric.Replay(replaySpec)`.

## Exact comparison and dispositions

`vswitch.Compare` and `fabric.Compare` evaluate behavioral equivalence between
two switches or two topologies against identical frame arrivals or injection
scenarios without consuming either input.

Comparison returns an exact `analysis.Disposition`:

- `analysis.Equivalent`: Observable behavior matches across all evaluated elements.
- `analysis.Different`: Divergence detected at a behavioral observable; the result
  names the first differing observable (`Difference.Observable`) and the values
  observed on each side (`Difference.Current` and `Difference.Expected`). On fabric
  comparisons, `Comparison.Replay` is a `[2]ReplaySpec` providing an immutable
  replay specification for each side (`[0]` current, `[1]` candidate).
- `analysis.Inconclusive`: Behavioral observables matched on evaluated elements, but
  the comparison could not complete (such as step budget exhaustion leaving pending
  frames in transit, or incomplete operational knowledge).

### Comparison invariants

Comparison guarantees three fundamental properties:

1. **Non-consuming execution**: Neither input switch nor fabric is mutated.
   `vswitch.Compare` evaluates forwarding via `Switch.Peek` without learning or
   forwarding table mutation; `fabric.Compare` forks both topologies internally
   (`Fabric.Fork`) with isolated clocks and event queues.
2. **Order independence**: Current-first and candidate-first evaluation produce the
   same disposition and identical named difference observable.
3. **Determinism under randomized insertion**: Comparison outcomes are invariant
   under arbitrary map and slice insertion order of ports, links, and hosts.

## Bounded differential search and counterexamples

`search.Search` explores finite candidate domains (`L2TrafficDomain`,
`TimedFaultDomain`) across two topologies under declared resource limits
(`search.Limits`):

- **Domain enumeration**: Iterates candidate traffic and timed cable faults in
  a deterministic total order independent of map iteration.
- **Counterexample minimization**: `search.Minimize` prunes irrelevant injections
  and faults in fixed order, halting at `Minimal` when no further elements can be
  removed without altering the observable divergence.
- **Trace alignment**: `search.Align` locates the first differing hop entry
  between paired journeys.
- **Resource bounds and remainder**: Bounded exploration respects `MaxCandidates`
  and `MaxDifferences`, naming unvisited candidates in `Result.Remainder`.

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
