# Spanning Tree Protocol (STP / RSTP)

The `flowseer.net.protocol.stp.v1` package holds what the Spanning Tree
Protocol owns: a bridge's protocol state and its ports' roles and states, as
device-scoped rows naming interfaces.

## Boundaries

Imports: net/addr

Imported by: nothing

Deliberately absent:

- Device and interface entity references. Rows use device-local interface names.
- Multiple spanning tree instances (MSTP) and per-VLAN instances (PVST).
- Wire-format BPDU encoding and decoding.
- Observation time, provenance, and tenant context.

Rows name the local interface by device-local name, so walking the port states
stands on its own without embedding protocol facets into the interface table.

Bridge identifiers split the standard eight-octet identifier into its two-octet
priority and six-octet MAC address. Exposing the priority explicitly lets
loaders and validators check and configure priorities without unpacking an
opaque byte string.

The package models a single spanning tree instance. Multiple instances (MSTP)
and per-VLAN instances (PVST) stay out until multi-instance topologies require
them. Wire-format BPDU encoding and decoding remain a simulator concern.

Device identity, tenant, lifecycle, provenance, and observation time belong to
the entity or envelope that carries these values.

## Sources

The package's field and enum contracts cite:

- [IEEE Std 802.1D-2004](https://standards.ieee.org/ieee/802.1D/3421/) for the
  Rapid Spanning Tree Protocol state machine, timers, priority vectors, and port
  roles.
- [RFC 4188 (BRIDGE-MIB)](https://datatracker.ietf.org/doc/html/rfc4188) for the
  base bridge and port spanning tree objects.
- [RFC 4318 (RSTP-MIB)](https://datatracker.ietf.org/doc/html/rfc4318) for the
  RSTP extension objects: administrative path costs, point-to-point modes, and
  edge port controls.
- [IEEE8021-MSTP-MIB](https://www.ieee802.org/1/files/public/MIBs/IEEE8021-MSTP-MIB-201806210000Z.txt)
  for the CIST auto-edge object; MSTP instances stay out.
- [Open vSwitch lib/rstp-common.h (branch-3.3)](https://raw.githubusercontent.com/openvswitch/ovs/branch-3.3/lib/rstp-common.h)
  for the per-port counters.
