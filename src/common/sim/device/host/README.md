# Host

A host has one cable attachment and no relay. It accepts frames for its own
addresses and answers local ARP and Neighbor Discovery requests. Its stack
uses `NeighborDisabled`, so packet origination requires a configured neighbor.
Neighbor Advertisements clear the Router flag.

```go
h, err := host.New("workstation", host.Config{
    Address: netaddr.MAC{2, 0, 0, 0, 0, 1},
})
if err != nil {
    return err
}
out, err := h.Originate(now, "", ethernet.Frame{
    Src: netaddr.MAC{2, 0, 0, 0, 0, 1},
    Dst: netaddr.MAC{2, 0, 0, 0, 0, 2},
    Tags: []vlan.Tag{{PCP: 5}},
}, nil)
if err != nil {
    return err
}
// out.Emissions[0] is untagged and carries PCP 5.
```

`Originate` applies the host's VLAN form to the VID and retains the supplied
PCP and DEI. PCP values above 7, a non-empty port, or a packet combined with
frame fields are refused.
`Receive` reports acceptance as `Consumed` or `Dropped`, with the deciding
step. An undecodable IP header carries incomplete metadata.

```mermaid
sequenceDiagram
    participant Switch
    participant Fabric
    participant Host
    Switch->>Fabric: Emit a solicitation
    Fabric->>Fabric: Record crossing and queue delivery
    Fabric->>Host: Receive at delivery time
    Host->>Fabric: Consumed and local answer emission
    Fabric->>Switch: Queue the answer
```

The [acceptance suite](accept_test.go) preserves VLAN, MAC, and IP check order.
[host_test.go](host_test.go) checks exactly four local and foreign request cases
and proves that hosts learn and resolve nothing. The advertisement's flags are
pinned to RFC 4861 section 4.4 in
[routing/host_test.go](../../layer/routing/host_test.go).
