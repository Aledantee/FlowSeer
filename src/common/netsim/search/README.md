# search

Package `search` provides bounded differential search, counterexample minimization,
and first-divergence trace alignment across simulated network fabrics.

The package layers above `analysis`, `vswitch`, and `fabric`. None of those
packages imports `search`, and `search` avoids consuming `netmodel` or protobuf
schemas directly.

## Concepts

Differential search evaluates whether two fabric configurations diverge under a
finite domain of injected traffic and cable faults.

- **`Domain`**: an enumerable finite state space with a declared candidate
  count (`Size() int`) and an internal iterator (`Enumerate(yield func(Candidate) bool)`).
  Enumeration follows a deterministic, total order independent of map iteration.
- **`Candidate`**: an evaluation target pairing a unique domain `Tuple`
  identity with traffic injections (`[]fabric.Injection`) and optional timed
  cable faults (`[]TimedFault`).
- **`Limits`**: bounds on the exploration space: `MaxCandidates` halts search
  after a declared count, `MaxDifferences` caps retained counterexample traces,
  and `Budget` sets the per-candidate simulation step limit.
- **`Coverage`**: records exploration progress (`Tested` / `Total`).
  `Result.Remainder` enumerates all tuples unvisited when search halts early.
- **`L2TrafficDomain`**: enumerates the cross product of declared source endpoints,
  destination MACs, VLAN IDs, and Ethernet frame shapes in stable total order.
- **`TimedFaultDomain`**: enumerates declared cable faults across declared
  simulation times.
- **`L3Domain`**: search interface for routed traffic spaces. Concrete enumeration
  is deferred to a later increment.

## Working example

```go
package main

import (
	"fmt"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/search"
)

func runDifferentialSearch(current, candidate *fabric.Fabric) {
	// Declare an L2 traffic domain over hosts, destinations, and VLANs
	domain := search.NewL2TrafficDomain(search.L2TrafficDomainConfig{
		Sources: []fabric.Endpoint{
			{Node: "h1"},
		},
		Destinations: []netaddr.MAC{
			netaddr.MustMAC("02:00:00:00:00:02"),
		},
		VLANs: []vlan.ID{10, 20},
		Shapes: []search.FrameShape{
			{EtherType: ethernet.EtherTypeIPv4, Payload: []byte("probe")},
		},
	})

	limits := search.Limits{
		MaxCandidates:  50,
		MaxDifferences: 5,
		Budget:         100,
	}

	// 1. Run bounded differential search
	res := search.Search(current, candidate, domain, limits)
	fmt.Printf("Coverage: %s (Remainder: %d)\n", res.Coverage, len(res.Remainder))

	// 2. Process and minimize discovered divergences
	for _, diff := range res.Differences {
		fmt.Printf("Divergence on tuple %s: %s\n", diff.Tuple, diff.Difference)

		// Minimize counterexample scenario while preserving the divergence observable
		minimalCandidate, status := search.Minimize(current, candidate, diff.Candidate, limits.Budget)
		fmt.Printf("Minimization status: %s (injections: %d)\n", status, len(minimalCandidate.Scenario))

		// Compare reduced candidate and locate first divergent causal trace hop
		cmp := fabric.Compare(current, candidate, minimalCandidate.Scenario, limits.Budget)
		if hopIdx, ok := search.Align(cmp); ok {
			fmt.Printf("First diverging trace entry index: %d\n", hopIdx)
		}
	}
}
```

## Guarantees and resource contract

1. **Non-consuming execution**: `Search` never mutates `current` or `candidate`.
   Both fabrics remain in their exact pre-search state.
2. **Exact remainder accounting**: When search halts before evaluating every
   candidate (via `MaxCandidates`), `Coverage.Tested + len(Remainder) == domain.Size()`.
   `Remainder` preserves the unvisited tuples in deterministic order.
3. **Bounded memory**: Divergences exceeding `MaxDifferences` are counted in
   `Result.TotalDifferences` and `Result.DiscardedDifferences()`, preventing
   unbounded memory growth on divergent networks while retaining full replay
   specifications for the first `MaxDifferences` candidates.
4. **Deterministic minimality**: `Minimize` iteratively prunes injections then
   faults, keeping a removal only if `fabric.Compare` produces the exact same
   `Difference.Observable`. It halts at `Minimal` when no further elements can be
   removed, or `LimitReached` if the evaluation trial limit is reached.
5. **Trace alignment**: `Align` pairs journey traversal entries by ordinal,
   returning the index of the first differing causal trace fact or step.

## Prior art cross-reference: Batfish

Batfish introduced configuration change validation via `differentialReachability`,
searching for packet headers delivered in one snapshot but dropped or routed
differently in another. However, Batfish stores VLAN data without applying it to
L2 reachability (Batfish issue #783), omitting Layer-2 forwarding divergence.

FlowSeer models complete IEEE 802.1Q switching, MAC learning, STP port states,
and physical link faults in pure Go. `search` provides bounded differential
verification at Layer 2 over finite enumerable domains, pairing divergence
detection with deterministic counterexample reduction and trace alignment.
Layer-3 routed reachability (`L3Domain`) is deferred to a subsequent phase.
