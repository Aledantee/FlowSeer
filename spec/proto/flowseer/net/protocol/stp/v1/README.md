# Spanning Tree Protocol (STP / RSTP / MSTP)

The `flowseer.net.protocol.stp.v1` package holds what the Spanning Tree
Protocol family owns: a bridge's protocol state and its ports' roles and
states, and for an MSTP bridge its spanning tree instances, their ports, and
the VLAN-to-instance map, as device-scoped rows naming interfaces.

## Boundaries

Imports: net/addr, net/key, net/switching

Imported by: nothing

Deliberately absent:

- Device and interface entity references. Rows use device-local interface names.
- Per-VLAN instances (PVST and Rapid-PVST), which are Cisco-proprietary.
- The CIST port values only an MSTP bridge has (regional root, internal path
  cost, restricted role and TCN, disputed).
- Wire-format BPDU encoding and decoding.
- Observation time, provenance, and tenant context.

Rows name the local interface by device-local name, so walking the port states
stands on its own without embedding protocol facets into the interface table.

Bridge identifiers split the standard eight-octet identifier into its two-octet
priority and six-octet MAC address. Exposing the priority explicitly lets
loaders and validators check and configure priorities without unpacking an
opaque byte string.

## The CIST and the MSTIs

STP, RSTP, and MSTP are one family in IEEE 802.1Q, and MSTP reuses
`BridgeId`, `PortRole`, and `ForwardingState`, so the three share this
package. An MSTP bridge runs the common and internal spanning tree (CIST)
plus up to 4094 multiple spanning tree instances (MSTIs).

The CIST is the bridge's `BridgeState` and each port's `PortState`, the rows
an STP or RSTP bridge already fills. IEEE8021-MSTP-MIB splits the CIST the
same way: its common parameters (root, root cost, root port, timers) are the
BRIDGE-MIB values `BridgeState` carries, and `ieee8021MstpCistTable` adds
the MSTP-only ones, which `BridgeState` holds as `mst_config_id`,
`cist_regional_root`, `cist_internal_root_path_cost`, and `max_hops`. Each
MSTI is an `MstInstance` row keyed by `(network_instance, mst_id)` and each
of its ports an `MstPort` row keyed by `(mst_id, interface_name)`.
`ieee8021MstpTable` indexes MSTIs by `IEEE8021MstIdentifier`, `1..4094`, so
`mst_id` 0 is never an MSTI row: spelling the CIST there would put the same
tree in two rows.

`MstVlanMap` is one row per tree listing the VLANs allocated to it, the way
every CLI configures the map and the way `ieee8021MstpVids0..3` carry it per
MSTI. Its `mst_id` accepts 0, meaning the CIST, because every VLAN is
allocated to some tree (`ieee8021MstpVlanV2MstId`, `0..4095`); 4095 marks
SPVIDs on an SPT bridge, which is not a targeted feature, and the row
rejects it. A VLAN listed in two rows of one bridge is a device
inconsistency no single row can see, and no schema rule catches it.

`BridgeState`, `MstInstance`, and `MstVlanMap` name the network instance
whose bridge runs the tree. This is FlowSeer's forwarding domain, not the
PBB bridge component (`ieee8021Mstp*ComponentId`) that indexes every
IEEE8021-MSTP-MIB table: a mapper drops the component and names the
instance, `default` when the device has none. `PortState` and `MstPort` are
keyed by interface and do not repeat it.

An MSTI's bridge identifier carries the settable priority only.
`ieee8021MstpBridgePriority` is the four most significant bits, `0..61440`;
the twelve-bit system ID extension below them holds the MSTID for an MSTI,
which the row already carries as `mst_id`. A mapper masks it off, and the
`bridge_id.priority` rule rejects an unmasked value.

`PORT_ROLE_MASTER` is the MSTI role IOS-XE reports as `stp-master`.
`ieee8021MstpPortRole` has no master value, so a MIB-only source never
reports it.

Device identity, tenant, lifecycle, provenance, and observation time belong to
the entity or envelope that carries these values.

## Sources

The package's field and enum contracts cite:

- [IEEE Std 802.1D-2004](https://standards.ieee.org/ieee/802.1D/3421/) for the
  Rapid Spanning Tree Protocol state machine, timers, priority vectors, and port
  roles.
- [IEEE Std 802.1Q](https://standards.ieee.org/ieee/802.1Q/10323/) for the
  Multiple Spanning Tree Protocol, the MST configuration identifier (13.8),
  and the MSTI priority vectors.
- [RFC 4188 (BRIDGE-MIB)](https://datatracker.ietf.org/doc/html/rfc4188) for the
  base bridge and port spanning tree objects.
- [RFC 4318 (RSTP-MIB)](https://datatracker.ietf.org/doc/html/rfc4318) for the
  RSTP extension objects: administrative path costs, point-to-point modes, and
  edge port controls.
- IEEE8021-MSTP-MIB, revision 202211080000Z
  (`spec/mib/ieee/IEEE8021-MSTP-MIB-202211080000Z.mib`), for the CIST, MSTI,
  MSTI port, VLAN map, and configuration identifier objects, and
  IEEE8021-SPANNING-TREE-MIB (`spec/mib/ieee/IEEE8021-SPANNING-TREE-MIB-201412150000Z.mib`)
  for the MSTP protocol version.
- The IOS-XE spanning tree operational model
  (`spec/yang/cisco/iosxe/2611/Cisco-IOS-XE-spanning-tree-oper.yang`) for the
  master port role.
- [Open vSwitch lib/rstp-common.h (branch-3.3)](https://raw.githubusercontent.com/openvswitch/ovs/branch-3.3/lib/rstp-common.h)
  for the per-port counters.
