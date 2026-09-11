# fabric

A Layer 2 network fabric composes virtual switches, hosts, and cables into
a simulated network. Execution advances synchronously in discrete steps.
Each step takes the earliest scheduled arrival, forwards it through the
destination switch, and queues copies onto connected cables.

## Example

The example builds two switches joined by a 300-meter cable with hosts `h1`
and `h2` in VLAN 10. A frame injected at `h1` traverses `sw1`, crosses
the cable, traverses `sw2`, and delivers to `h2`.

```go
package main

import (
	"fmt"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

func main() {
	b1 := port.NewBuilder()
	b1.Add(port.Port{
		Name:        "1/1/1",
		Kind:        port.Physical,
		AdminStatus: port.Up,
		OperStatus:  port.Up,
	})
	b1.Add(port.Port{
		Name:        "1/1/24",
		Kind:        port.Physical,
		AdminStatus: port.Up,
		OperStatus:  port.Up,
	})
	ports1, err := b1.Build()
	if err != nil {
		panic(err)
	}

	b2 := port.NewBuilder()
	b2.Add(port.Port{
		Name:        "1/1/1",
		Kind:        port.Physical,
		AdminStatus: port.Up,
		OperStatus:  port.Up,
	})
	b2.Add(port.Port{
		Name:        "1/1/24",
		Kind:        port.Physical,
		AdminStatus: port.Up,
		OperStatus:  port.Up,
	})
	ports2, err := b2.Build()
	if err != nil {
		panic(err)
	}

	vid10 := vlan.ID(10)
	makeBridge := func() *bridge.Config {
		return &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "prod"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1":  {PVID: &vid10, Untagged: []vlan.ID{10}},
					"1/1/24": {Tagged: []vlan.ID{10}},
				},
			},
		}
	}

	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}

	cfg := fabric.Config{
		Start: time.Unix(1700000000, 0),
		Switches: map[string]vswitch.Config{
			"sw1": {Ports: ports1, Bridge: makeBridge()},
			"sw2": {Ports: ports2, Bridge: makeBridge()},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: macH1},
			"h2": {Address: macH2},
		},
		Cables: []fabric.Cable{
			{
				A: fabric.Endpoint{Node: "h1"},
				B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			},
			{
				A:            fabric.Endpoint{Node: "sw1", Port: "1/1/24"},
				B:            fabric.Endpoint{Node: "sw2", Port: "1/1/24"},
				LengthMeters: 300,
			},
			{
				A: fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
				B: fabric.Endpoint{Node: "h2"},
			},
		},
	}

	fab, err := fabric.New(cfg)
	if err != nil {
		panic(err)
	}

	now := time.Unix(1700000000, 0)
	_, err = fab.Inject(fabric.Injection{
		At:     now,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Dst:       macH2,
			Src:       macH1,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("hello"),
		},
	})
	if err != nil {
		panic(err)
	}

	steps := fab.Run(10)
	fmt.Printf("Completed in %d steps\n", steps)
}
```

## Traversal journey

Calling `Report()` returns the journey recorded for each frame id. The entries
record the progression across devices and cables:

- `Injection`: Introduces the frame at `h1` at `t0`.
- `Hop`: Evaluates forwarding on `sw1:1/1/1`. Because `macH2` is unknown, the
  bridge floods VLAN 10 out `1/1/24` with a C-TAG of VID 10.
- `Crossing`: Transmits the copy across the 300-meter cable to `sw2:1/1/24`.
  The cable length divided by two thirds of light speed yields 1501 ns latency.
- `Hop`: Evaluates forwarding on `sw2:1/1/24` at `t0 + 1501ns`. Flooding VLAN 10
  selects access port `1/1/1` and strips the C-TAG.
- `Delivery`: Records arrival at destination host `h2` at `t0 + 1501ns`.

Frames cabled directly to destination hosts deliver on transmission without
occupying an arrival queue step.

## Hosts with an IP stack and address assignment

A host configured with `IP *HostIP` translates to an internal routing layer
operating with VRF `default` and a single routed port carrying the host's
assigned MAC and configured IP address prefixes. When an injected packet is
originated:

- Source address selection chooses among the host's interface addresses
  matching the destination family by longest matching prefix, falling back
  to the first configured address of that family.
- Datagrams transmit with hop limit 64 direct on a connected prefix, and
  through the default gateway when destination is off-link.
- Link-layer encapsulation looks up destination or gateway addresses in the
  static neighbor table.

`Inject` validates packet injections and refuses them with an error when:

- `Origin` names a switch or a host without an IP stack.
- `Origin.Port` is non-empty.
- `Frame` specifies any fields alongside `Packet`.
- Packet origination encounters `no-route` (destination off every prefix with
  no gateway) or `neighbor-miss` (destination or gateway missing from neighbors),
  naming the address and reason without creating a journey.

MAC addresses left zero in a fabric configuration are assigned during `New`
and `build` before subsystem instantiation. The allocator walks switch names
then host names in sorted order, assigning the first locally administered
unicast MAC `netaddr.Local(n)` (`02:00:00:` followed by n big-endian, starting
from 1) that is not explicitly used by any switch base MAC, host address, routed
interface MAC, or spanning tree address. `Fabric.Config()` returns the filled
values, and `Derive` preserves existing assignments for nodes whose new
configuration leaves them zero.

## Snapshot after one step

Calling `fab.Run(1)` on the scenario processes the initial arrival at `sw1`
and leaves the transmission in flight:

- `Clock`: Set to `t0`, the arrival timestamp of the completed step.
- `Queue`: Holds one pending arrival at `sw2:1/1/24` scheduled at `t0 + 1501ns`.
- `Devices["sw1"]`: FDB holds dynamic entry `(10, macH1) -> 1/1/1`.
- `Devices["sw2"]`: FDB contains no entries.

## Protocol traffic and wake-ups

`Config.Start` is the fabric's clock when it is built: every protocol layer
hears its links at `Start`, the first proposals are dated `Start`, and a
fabric with a spanning tree switch refuses a zero `Start`, since its hellos
would otherwise fire in year 1, ahead of any frame a caller injects.

Spanning tree and other internal protocols schedule state transitions and
frame emissions through the run's arrival queue. Each device holds at most one
wake entry in the queue (`Arrival.Wake` true, port empty, frame ID 0, sequence
0). Because sequence 0 sorts ahead of positive frame sequences, a wake executes
before frames scheduled for the same instant. `Step` returns `EntryWake` for
these timer events without appending to any frame journey.

Frames emitted by switch layers during wake-ups or forwarding cross cables and
hubs as journeys marked `Protocol`. Periodic hellos ensure the queue never
drains; callers supply a step budget to `Run(n)` and evaluate topology
convergence by checking whether consecutive snapshots report identical roles
and forwarding states across all ports.

## Link operational state rule

Switch port operational states derive from connected cables during `New`,
replacing values provided in the input configurations:

- A port without a cable is `Down`; it has no link, so `Unlinked` lists it
  with reason `no-cable`.
- When either end is administratively disabled both are `Down`: the disabled
  end reads `admin-down`, the other `peer-down`.
- A severed cable (`FaultCut`) leaves both endpoints `Down` with `cut`.
- Unidirectional failure (`FaultDeadAToB` or `FaultDeadBToA`) leaves both ends
  `Down` with `dead-direction` unless both ends disable auto-negotiation.
- Operational cables run two-ended negotiation (`phy.Negotiate`). If speeds
  disagree, ports transition to `Down` with `speed-mismatch`.
- A Link Aggregation Group (LAG) is `Up` when any member port is `Up`.

## Fault kinds

Cables support deterministic defect configurations:

| Kind              | Effect                                           |
| ----------------- | ------------------------------------------------ |
| `None`            | Unimpaired transmission                          |
| `Cut`             | Physical severance; both endpoints remain Down   |
| `DeadAToB`        | Blocks transmission from endpoint A to B         |
| `DeadBToA`        | Blocks transmission from endpoint B to A         |
| `LoseEveryNth`    | Drops every Nth frame crossing the cable         |
| `LoseSequence`    | Drops frames matching 1-based crossing positions |
| `CorruptEveryNth` | Marks every Nth crossing damaged; recipient drop |

`SetFault(a, b Endpoint, fault Fault) error` alters a cable defect mid-run at
the current clock. The fabric re-resolves the link, updates each endpoint's
operational status on its switch, and notifies active spanning tree layers of
the link change. Both endpoints must identify an existing cable.

## Queue ordering

Simulation arrivals follow a deterministic total order in the queue:

1. Arrival time (`At`) in ascending chronological order.
2. Injection sequence number (`Seq`), which every copy of a frame keeps.
3. Destination device name.
4. Destination port name.

A run therefore replays in the same order every time.

## Reasons

The package declares reasons for link failures and frame discards:

| Reason           | Meaning                                           |
| ---------------- | ------------------------------------------------- |
| `cut`            | Cable is physically severed                       |
| `peer-down`      | Peer interface is administratively disabled       |
| `admin-down`     | This interface is administratively disabled       |
| `no-cable`       | Switch port has no connected cable                |
| `dead-direction` | Defect in one direction prevents auto-negotiation |
| `cable-loss`     | Configured cable fault dropped frame in transit   |
| `bad-frame`      | Frame was corrupted during cable transit          |

## Concurrency contract

A `Fabric` is not safe for concurrent use. Simulators mutate internal clock
timestamps, queues, forwarding entries, and interface counters during execution.
Callers running concurrent simulations must create separate `Fabric` instances.
