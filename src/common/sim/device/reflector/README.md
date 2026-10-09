# Reflector

A reflector accepts mDNS queries and returns one rebuilt `device.Copy` per
other attachment of the query's family. A matching MAC/IP group pair is
required. Each attachment has a source address and a port with an optional
VLAN. Validation rejects invalid prefixes so a copy cannot select an invalid
source (`config.go`, `TestReflectorAttachmentRefusesZeroPrefix`).

```go
cfg := reflector.Config{
    Address: netaddr.MAC{2, 0, 0, 0, 0, 9},
    Ports: map[string]phy.Ethernet{"p1": {}, "p2": {}},
    Attachments: map[string]reflector.Attachment{
        "office": {Port: "p1", Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.9/24")}},
        "media": {Port: "p2", Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.20.9/24")}},
    },
}
r, err := reflector.New("mdns", cfg)
if err != nil {
    return err
}
fmt.Println(len(r.Spec().Attachments)) // 2
```

`Receive` returns copies in attachment name order. It rewrites the source MAC,
IP source, hop limit, and UDP source port. It retains the accepted destination
and payload (`reflector.go`). The reflector has no timers and refuses caller
injections. Calls require serial access. `Fork` shares immutable configuration
and keeps link changes independent through the immutable port table.

The fabric records the acceptance on the arriving journey, assigns each copy a
fresh frame identity, and seeds its re-entry history from its parent.
`OriginReflection` and `Origin.Of` preserve that relationship
(`src/common/sim/fabric/run.go`). A reflector acceptance is never host delivery.
