# search

`search` provides bounded differential search, counterexample minimization, and
first-divergence trace alignment across simulated network fabrics.

The package layers above `analysis`, `vswitch`, and `fabric`. None of those
packages imports `search`, and `search` avoids consuming `netmodel` or protobuf
schemas directly.

## Concepts

Differential search evaluates whether two fabric configurations diverge under a
finite domain of injected scenarios and link faults.

- **`Domain`**: an enumerable finite state space with a declared candidate
  count (`Size() int`) and an internal iterator (`Enumerate(yield func(Candidate) bool)`).
  Enumeration follows a deterministic, total order independent of map iteration.
- **`Candidate`**: an evaluation target pairing a unique domain `Tuple`
  identity with traffic injections (`[]fabric.Injection`) and optional timed
  cable faults (`[]TimedFault`).
- **`Limits`**: bounds on the exploration space: `MaxCandidates` halts search
  after a declared count, `MaxDifferences` caps retained counterexample traces,
  and `Budget` sets the per-candidate simulation step limit.
- **`L2TrafficDomain`**: enumerates the cross product of declared source endpoints,
  destination MACs, VLAN IDs, and Ethernet frame shapes.
- **`TimedFaultDomain`**: enumerates declared cable faults across declared
  simulation times.
- **`L3Domain`**: search interface for routed traffic spaces. Concrete enumeration
  is deferred to a later increment.

## Example

```go
dom := search.NewL2TrafficDomain(search.L2TrafficDomainConfig{
    Sources: []fabric.Endpoint{
        {Node: "h1", Port: "eth0"},
        {Node: "h2", Port: "eth0"},
    },
    Destinations: []netaddr.MAC{
        netaddr.MustMAC("02:00:00:00:00:01"),
    },
    VLANs: []vlan.ID{10, 20},
    Shapes: []search.FrameShape{
        {EtherType: ethernet.EtherTypeIPv4},
    },
})

dom.Enumerate(func(cand search.Candidate) bool {
    // Process or evaluate candidate
    return true
})
```
