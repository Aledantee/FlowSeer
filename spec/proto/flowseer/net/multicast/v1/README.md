# Multicast Snooping Membership

The `flowseer.net.multicast.v1` package holds what IGMP and MLD snooping
leave on a switch: which ports have joined which multicast groups, in which
VLAN.

## Boundaries

Imports: net/addr, net/key, net/switching

Imported by: nothing

Deliberately absent:

- Multicast router ports, per-VLAN snooping settings, querier state, and
  per-VLAN statistics, which every source keeps in tables of their own.
- Multicast routing (PIM, IGMP and MLD on a router interface).
- Device and interface entity references. Rows use device-local names.
- Observation time, provenance, and tenant context.

## One table for IGMP and MLD

Snooping is function, not protocol: IGMP fills the table for IPv4 groups and
MLD for IPv6 ones, so the table lives in a function package rather than
under `net/protocol/`. No standard models snooping state (RFC 4541
describes the behavior only), so `GroupMembership` is FlowSeer-normalized
from vendor tables. Aruba CX, D-Link, Cisco SMB, and the FASTPATH family
hold IGMP and MLD in one table with an address-type column, and the row
copies that shape: the address family of `group` plays the column, and the
row's rules keep `source` and `last_reporter` in the same family and an MLD
group's compatibility version at 1 or 2.

The row is per port. Filter mode is state a multicast router keeps per group
per attached network (RFC 3376 section 6.2.1, RFC 3810 section 7.2.1), and
on a switch the attached network is the port. The row is keyed by
`(network_instance, vlan_id, group, source, interface_name)`, where an absent
`source` means any source. It requires `network_instance` for the reason
`FdbEntry` does: it carries a `vlan_id`, and joins to a `Vlan` row by that
pair.

The sources a mapper reads:

- Aruba CX `arubaWiredMgmdSnoopingGroupPortCacheTable`, indexed by VLAN,
  group address type, group, and port
  (`spec/mib/aruba/cx/ARUBAWIRED-MGMD-SNOOPING-MIB:1160-1172`).
- D-Link `dMgmdSnpGroupTable`, indexed by VLAN, group address type, group,
  and port (`spec/mib/dlink/DLINKSW-MGMD-SNOOPING-MIB:678-687`).
- The FASTPATH family's source-specific `agentSwitchSnoopSSMFDBTable`,
  indexed by group address type, group, source, and VLAN
  (`spec/mib/ubiquiti/edgemax/EdgeSwitch-SWITCHING-MIB:3457-3465`).
- The Cisco SMB bridge multicast MIBs the network domain atlas lists
  (`docs/research/network-domain-atlas/entities/03-switching.md`).
- Q-BRIDGE-MIB `dot1qTpGroupTable`, indexed by VLAN and the group's MAC
  address (RFC 4363). It names member ports but not the IP group, so it
  cannot fill a row alone.
- Ruckus ICX's static group table
  (`spec/mib/ruckus/icx/FOUNDRY-SN-IGMP-MIB:161-193`), for static
  memberships.

A source that reports a group's members as a PortList bitmap, rather than
one row per port, expands to one `GroupMembership` per set bit, each naming
that port's interface.

## Sources

The package's field and enum contracts cite:

- [RFC 3376](https://www.rfc-editor.org/rfc/rfc3376.html) (IGMPv3), sections
  6.2.1 and 7.3.2, for filter mode and the group compatibility mode.
- [RFC 3810](https://www.rfc-editor.org/rfc/rfc3810.html) (MLDv2), sections
  7.2.1 and 8.3.2, for the same in MLD.
- [RFC 4541](https://www.rfc-editor.org/rfc/rfc4541.html), section 2.1.1,
  for snooping behavior and the membership timeout.
- The vendor MIBs listed above for the table shape and the static and
  dynamic kinds.
