# Cable

`Cable` owns physical resolution and the decision at one crossing. The fabric
keeps endpoint identities, counts crossings, and schedules delivery.

```go
wire := cable.Cable{
    Medium: cable.TwistedPair,
    LengthMeters: 10,
    Fault: cable.Fault{Kind: cable.FaultLoseSequence, Sequence: []uint{2, 3}},
}
lost, corrupt := wire.Crossing(2, true) // true, false
delay := wire.Propagation()
fab, err := assembly.New(assembly.Config{
    Hosts: map[string]host.Config{
        "a": {Address: netaddr.MAC{2, 0, 0, 0, 0, 1}},
        "b": {Address: netaddr.MAC{2, 0, 0, 0, 0, 2}},
    },
    Cables: []fabric.Cable{{
        Cable: wire,
        A: fabric.Endpoint{Node: "a"},
        B: fabric.Endpoint{Node: "b"},
    }},
})
if err != nil {
    return err
}
fmt.Println(lost, corrupt, delay, fab.Clock())
```

Fault positions are one-based and count every frame transmitted on the cable
since construction, in both directions together. A BPDU takes a position,
whether a host or a switch sent it. Positions 2 and 3 can therefore lose one
frame each way. `Crossing` receives the count and whether the frame travels
from A to B. The fabric increments the count before calling it
(`fabric/egress.go`, `transmitCable`). `TestFaultPositionCountsBothDirections`
and `TestFaultPositionCountsSwitchProtocolFrames` pin that count through the
fabric's journeys.

`Resolve` takes each end's Ethernet facts and administrative state. It returns
the facts used, each end's operational state, reason, speed and duplex, with
issues and any assumption that filled an unreported fact. Labels and scopes
keep those reports attached to the endpoint that supplied the facts.

```mermaid
flowchart LR
    assumption[Fill unstated facts] --> fault[Physical fault]
    fault --> admin[Administrative state]
    admin --> reach[Reach]
    reach --> negotiation[Negotiation]
    negotiation --> observed[Observed rule]
```

An unknown administrative state at either end leaves both ends `Unknown`
with `ReasonAdminUnknown` (`unknown-operational-status`). A cut, a dead
direction that prevents negotiation, or a disabled end is resolved before
that rule (`resolve.go`, `Cable.Resolve`).

The reach rows and velocity factors in `medium.go` are the simulator's existing
model. This package carries those values without adding Ethernet claims.
`ValidateEthernet` and `NormalizeEthernet` use the physical layer's port rules
so a host, reflector, and switch validate the same facts (`ethernet.go`).
