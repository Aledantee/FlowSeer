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

## Snapshot after one step

Calling `fab.Run(1)` on the scenario processes the initial arrival at `sw1`
and leaves the transmission in flight:

- `Clock`: Set to `t0`, the arrival timestamp of the completed step.
- `Queue`: Holds one pending arrival at `sw2:1/1/24` scheduled at `t0 + 1501ns`.
- `Devices["sw1"]`: FDB holds dynamic entry `(10, macH1) -> 1/1/1`.
- `Devices["sw2"]`: FDB contains no entries.

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
