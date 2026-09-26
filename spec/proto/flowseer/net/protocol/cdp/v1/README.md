# CDP

The `flowseer.net.protocol.cdp.v1` package holds what the Cisco Discovery
Protocol owns on a receiving device: the neighbors it heard, one row per
neighbor per local interface.

## Boundaries

Imports: net/addr, net/key, net/phy, net/switching

Imported by: nothing

Deliberately absent:

- Device and interface entity references. Rows use device-local interface names.
- A union with LLDP neighbors. LLDP and CDP each keep their own table, and a
  protocol-blind view of what is on a port is a service projection.
- CDP's local settings (`cdpInterfaceTable`, `cdpGlobal*`).
- Non-IP address families (CLNS, DECnet, IPX), which have no FlowSeer address
  type and which no targeted device runs.
- The CDP TLVs that `HUAWEI-CDP-COMPLIANCE-MIB` carries undecoded.
- Decoding CDP frames off the wire.
- Observation time, provenance, and tenant context.

Neighbors are a device-scoped table, as in the LLDP package: a row names the
interface it was heard on by that interface's device-local name, so a walk
of the neighbor cache stands on its own.

The key is `(local_interface_name, device_id)`, and it is FlowSeer's own.
CISCO-CDP-MIB keys `cdpCacheTable` by `cdpCacheIfIndex` and
`cdpCacheDeviceIndex`, and the IOS-XE operational model keys its list by a
numeric `device-id`, "Device number of this device"; both numbers are
local to the agent and mean nothing off it. The Device-ID TLV string, the
MIB's `cdpCacheDeviceId` and IOS-XE's `device-name`, is what the neighbor
calls itself, so `device_id` holds it and is required. A mapper never puts
IOS-XE's numeric `device-id` into it.

A mapper converts at the edge: the MIB's 0 for no native VLAN and its
`unknown` duplex leave those fields absent, power consumption goes from
milliwatts to nanowatts, and an address of a non-IP family is dropped. The
appliance (voice) VLAN is kept as sent, 0 and 4095 included, because
rejecting a value a device really sent would fail the whole row.

Every string the MIB types as `DisplayString` takes that convention's 255
characters, except the software version: a Version TLV runs past it (the
Cisco SMB MIB carries the overflow in a second column), so it takes the
1024 the schema uses for free text with no standard size.

`time_to_live` is the hold time the neighbor announced, the same meaning as
the LLDP neighbor's field. A source that reports only the time remaining
before the entry expires leaves it absent.

Device identity, tenant, lifecycle, provenance, and observation time belong to
the entity or envelope that carries these values.

## Sources

The package's field and enum contracts cite:

- [CISCO-CDP-MIB](https://github.com/cisco/cisco-mibs/blob/main/v2/CISCO-CDP-MIB.my)
  for the neighbor cache. The MIB lives in the untracked Cisco enterprise
  clone that `spec/mib/README.md` restores; the schema cites line numbers in
  its tracked SMIv2-to-YANG translation,
  `spec/yang/cisco/iosxe/2611/MIBS/CISCO-CDP-MIB.yang`.
- The IOS-XE CDP operational model
  (`spec/yang/cisco/iosxe/2611/Cisco-IOS-XE-cdp-oper.yang`) for its device
  key and address families.
- The Cisco SMB CDP MIB (`spec/mib/cisco/smb/CISCOSBCDP.mib`) for the CDP
  version, the time remaining, and the Version TLV overflow.
- The Wireshark CDP dissector,
  [`epan/dissectors/packet-cdp.c` at commit 16c09df2](https://gitlab.com/wireshark/wireshark/-/blob/16c09df25f262e2e1b432521aba7eb6a3ea4afe3/epan/dissectors/packet-cdp.c),
  for the header TTL and the capability bit positions, which the MIB leaves
  to the CDP specification.
