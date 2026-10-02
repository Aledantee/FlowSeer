# layer

Package `layer` defines shared types and contracts common to data-link and
network layer models in the switch pipeline.

| Type | What it represents |
| --- | --- |
| `Emission` | An Ethernet frame to transmit out a virtual switch port |
| `FlushTarget` | A port whose learned forwarding table entries must be flushed, and which FIDs on it are stale |
| `Effects` | Frames to emit, forwarding entries to flush, and LAGs whose enabled membership changed |

## Emission

`Emission` pairs an egress port name, a VLAN ID, and an Ethernet frame.

A zero VID retains layer-specific egress semantics:
- Spanning tree protocol (`stp`) emissions with VID 0 leave untagged without a
  VLAN membership check.
- Loop-protection (`loopprotect`) emissions with VID 0 request transmission
  onto the port's native VLAN.

## Effects

Layer state changes and timer advances produce `Effects`. Virtual switch
integrations collect these effects to dispatch frame transmissions, invalidate
filtering database entries, or update port state.
