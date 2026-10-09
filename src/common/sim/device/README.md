# Device

`Device` is the contract a fabric drives. Its output types name no layer,
so a caller can record forwarding without depending on a device's engines.
The switch implements it in [vswitch](vswitch/README.md).

```go
func receive(cfg assembly.Config, node, ingress string, frame ethernet.Frame) (device.Output, error) {
    fab, err := assembly.New(cfg)
    if err != nil {
        return device.Output{}, err
    }
    d := fab.Device(node)
    return d.Receive(fab.Clock(), ingress, frame), nil
}
```

The caller supplies a validated configuration and a node and port it contains.
`Fabric.Device` returns a fork, so this example evaluates the seam without
changing the fabric. Use `Inject` and `Run` to execute on the fabric's queue.

Calls require serial access and a caller-owned clock. `Receive` settles due
timers before processing the frame. `ReceiveMirrored` skips policing and
copy generation. `Advance` settles timers, and `NextWake` reports their next
deadline. `LinkChange` reports operational state, point-to-point status, and
speed in bits per second. `Fork` returns an independent executable copy.
`Ports` returns the port table as a value.

`Output` carries the receive's `Result`, emissions, mirror copies, hold token,
and held-frame drops. Timer and link calls may have a zero result. A receive
has one of the five outcomes in [trace](../trace/trace.go), even with no egress.
The result carries each egress's logical port, selected member, frame, PCP,
and drop reason alongside its trace and trust metadata.

Consulted ports and scopes cross the seam because a port that receives no
frame can still change the answer's readiness. `ConsultedPorts` and
`ConsultedScopes` return independent slices. The fabric's
[journey metadata test](../fabric/journey_metadata_test.go) checks a flood
whose unknown port contributes an issue despite receiving no transmission.

The switch converts layer-owned values in
[`call.output`](vswitch/output.go). The
[import gate](../../../../test/conformance/sim/layer_contract_test.go) checks
this directory alone. Device kinds beneath it may import their layers.

`State(now)` samples the device without advancing it. Its independent rows
carry the layer fields through types declared in [state.go](state.go).
`TreeRoles` uses `TreeRef` keys, so a CIST and an MSTI remain distinct even
when VLAN 1 belongs to the MSTI.

```go
before := d.State(now)
next := d.State(later)
field, current, expected := before.Diff(next)
fmt.Printf("%s: %s -> %s\n", field, current, expected)
```

`State.Fingerprint` and `State.Diff` read the same field classification.
The forwarding database, multicast state, ports, power allocations, tree
roles, and neighbor resolution are behavioral. Port counters, relay
counters, and the four STP counters are diagnostic. Timer timestamps and
power analysis metadata stay available in the snapshot and do not enter
its behavioral projection. Differences include the rows that differ,
beside their field path and entry count. [TestStateProjection](state_test.go)
checks 18 mutations against both methods.
