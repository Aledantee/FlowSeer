# Device

`Device` is the contract a fabric drives. Its output types name no layer,
so a caller can record forwarding without depending on a device's engines.
The switch implements it in [vswitch](vswitch/README.md).

```go
func receive(d device.Device, now time.Time, ingress string, frame ethernet.Frame) device.Output {
    return d.Receive(now, ingress, frame)
}
```

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
