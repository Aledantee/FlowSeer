# port

Package `port` holds the port table of the virtual switch: the ordered
interfaces of one device with their link states, MTU, ifIndex, and LAG
membership. It makes no forwarding decision beyond whether a port may carry a
frame, so the layers and the switch read it and decide the rest.

## Example

`WithOperStatus` returns a changed table and leaves the receiver as it was.

```go
package main

import (
	"fmt"

	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func main() {
	up, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
	if err != nil {
		panic(err)
	}

	down, err := up.WithOperStatus("1/1/1", port.Down)
	if err != nil {
		panic(err)
	}

	before, _ := up.Port("1/1/1")
	after, _ := down.Port("1/1/1")
	fmt.Println(before.OperStatus, after.OperStatus)
	// Output: Up Down
}
```

## The table

`Table` is built by `Builder` ([builder.go](builder.go)), which validates and
normalizes it. A normalized table lists its ports in byte order of the name, so
`1/1/10` sorts before `1/1/2`, and the zero value is an empty table. An unset
kind becomes `Physical` and an unset admin or oper status becomes `Unknown`.

A port forwards only when its admin and oper status are both `Up`. An `Unknown`
status therefore does not forward.

`WithOperStatus(name, state)` returns a table equal to the receiver except for
that port's oper status, and stores an empty state as `Unknown`. It refuses a
name the table lacks at field `ports.<name>` and a state outside `Unknown`,
`Up`, and `Down` at `ports.<name>.oper_status`.

## LAG membership

A port whose `LagParent` is set is a member of that LAG. `Members` lists a LAG's
members in name order. `Resolve` maps a member to its parent LAG and any other
port to itself. It reports false when the port is absent or the named parent is
missing. `Validate` requires a parent to exist, to be of kind `LAG`, and to have
no parent itself.

## Receive and Transmit

`Receive(name)` decides ingress. An absent port, a member that does not forward,
a missing parent, or a parent that does not forward is refused with
`ReasonPortDown`, and `Decisive` names the port whose state refused. A port with
`Unknown` status is refused as down.

`Transmit(name, payloadLen)` decides egress. An absent port, a port that does
not forward, and a LAG with no forwarding member return `ReasonPortDown`. A
configured MTU smaller than `payloadLen` returns `ReasonMTUExceeded`. An
`Unknown` port returns `ReasonPortDown`.

## Files

| File | Holds |
| --- | --- |
| [port.go](port.go) | `Port`, `Table`, `Receive`, `Transmit`, rule and reason constants |
| [builder.go](builder.go) | `Builder` with `Add`, `Range`, and `Build` |
| [diff.go](diff.go) | `Diff` of two tables as `trace.Change` values |
| [fact.go](fact.go) | `ForwardingFact` and `HubEgressFact` trace facts |

## Identifiers under layer `port`

The hub path of the virtual switch (`Switch.forwardHub` in
[switch.go](../device/vswitch/switch.go)) emits these rules under layer `port`:

| Rule | Emitted when |
| --- | --- |
| `port.status.not_found` | the ingress port is not in the table |
| `port.status.down` | the ingress port or its resolved LAG does not forward |
| `port.lag.parent_not_found` | the ingress member names a missing parent |
| `port.hub.no_egress` | no other port can take the frame |
| `port.status.mtu-exceeded` | the payload exceeds an egress port's MTU |
| `lag.egress.no_member` | an egress LAG has no member to select |
| `port.hub.replicate` | the frame is copied to an egress port |

The same path drops with reasons `port-down`, `mtu-exceeded`, and the relay's
`no-egress` and `no-member`.

## Sources

The table's state and membership contracts are in [port.go](port.go) and
[builder.go](builder.go). The relay applies these decisions in its Q2003
clause 8.4.1 stage, described in [bridge/README.md](../layer/bridge/README.md).

## Limits

`WithOperStatus` changes the named port alone. A member's status does not
derive its LAG's status, because the caller owns that decision.
`Receive` and `Transmit` require known `Up` states and infer no eligibility
from `Unknown`.
