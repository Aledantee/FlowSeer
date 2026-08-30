---
title: Vendor dossier — LANCOM Systems
date: 2026-08-30
scope: LCOS routers/WLCs, LCOS LX access points, LCOS SX switches, LANCOM Management Cloud
---

# LANCOM

**Enterprise OID:** 2356.
**Corpus:** 261 MIB modules — the second-largest count in the atlas, though the
number is inflated by two full FASTPATH firmware trees being vendored twice.
Plus 13 OpenAPI documents for the cloud platform.

**Three device platforms, three unrelated firmware stacks**, plus a cloud:

| Platform | Products | Stack | Directory |
|---|---|---|---|
| **LCOS** | routers, VPN gateways, WLAN controllers | LANCOM's own | `spec/mib/lancom/lcos/` |
| **LCOS LX** | access points | LANCOM's own (different codebase) | `spec/mib/lancom/lx/` |
| **LCOS SX** | switches | **Broadcom FASTPATH** + LANCOM additions | `spec/mib/lancom/sx/` |
| **LMC** | cloud management | — | `spec/openapi/lancom/lmc-openapi/` |

---

## LCOS (routers and WLCs)

**Corpus:** 16 files — `LCOS-MIB` (**5.2 MB**, the largest single file in the
corpus) plus `LC-UNIFIED-LCOS-<version>-REL-OIDS.mib` for releases 9.20 through
10.94.

### The shape is unlike anything else here

LCOS's SNMP MIB is a **mechanical projection of the LCOS configuration and
status tree**, not a hand-designed MIB. Every CLI path becomes an OID, so table
names read like filesystem paths:

```
lcsStatusEthernetPortsPortsTable[...EntryPort]
lcsStatusEthernetPortsCableTestResultsTable[...EntryIfc]
lcsStatusLanBridgeSpanningTreeRstpPortTableTable[...EntryDescription]
lcsStatusTcpIpDhcpDhcpTableTable[...EntryIpAddress, ...EntryNetworkName]
lcsStatusWlanMngmtApConfAutowdsTopoErrorsTable[Index, Priority, SlaveApName, SlaveApWlanIfc]
lcsSetupIpRouterIpRoutingTableTable[EntryIpAddress, EntryIpNetmask, EntryRtgTag]
```

Two consequences:

1. **`lcsStatus*` vs `lcsSetup*` is a clean config/state split** carried all the
   way through — the same distinction OpenConfig makes, arrived at by a
   different route. That makes LCOS unusually easy to map onto FlowSeer's
   Config/State triad.
2. **The keys are LCOS's own names**, never `ifIndex`. `Ifc`, `Port`,
   `NetworkName`, `Description`, `RtgTag`. There is no `entPhysicalIndex` and
   the standard MIB joins do not apply.

### `RtgTag` — LANCOM's VRF

`lcsSetupIpRouterIpRoutingTableTable[ipAddress, ipNetmask, **rtgTag**]`,
`lcsSetupIpv6RouterRoutingTableTable[prefix, rtgTag]`,
`lcsSetupTcpIpAccessListTable[ipAddress, ipNetmask, rtgTag]`,
`lcsStatusMCastIpv4DownstreamMfibTable[rtgTag, source, group, interface, source]`.

The routing tag partitions the routing table, the ACLs, and the multicast FIB.
It is LANCOM's answer to the network-instance dimension, and it is *in the key*
where the IETF MIBs have nothing. See [vrf](../entities/04-ip.md#vrf).

### Where LCOS is the reference implementation

- **A live firewall session table over SNMP.**
  `lcsStatusIpv6FirewallForwardingSessionsTable[srcAddress, dstAddress, prot,
  srcPort, dstPort, srcInterface]` — a 6-tuple connection table. Nothing else
  in the corpus exposes this.
- **DHCP lease table.** `lcsStatusTcpIpDhcpDhcpTableTable[ipAddress,
  networkName]` plus `lcsStatusLanBridgeDhcpTableTable[macAddress]` — a real
  endpoint inventory, because LCOS devices are routers running DHCP.
- **DSL spectrum.** `lcsStatusAdslAdvancedDsBitLoadingTable[binNumber]` /
  `...UsBitLoadingTable` — per-subcarrier bit loading.
- **WLAN environment scanning.**
  `lcsStatusWlanChannelScanResultsTable[radioBand, radioChannel, interface]`,
  `lcsStatusWlanSpectralScanAllChannelsTable[index]`,
  `lcsStatusWlanNoiseImmunityCurrentParametersTable[band, channel, interface]`,
  `lcsStatusWlanThermalMitigationTable[Ifc]` (thermal throttling of a radio).
- **Public Spot** — a complete hotspot with its own user store:
  `lcsStatusPublicSpotStationTableTable[macAddress]`,
  `lcsSetupPublicSpotModuleVlanTableTable[vlanId]`,
  `lcsSetupPublicSpotModuleMacAddressTableTable[macAddress]`.
- **WLC role.** `lcsStatusWlanMngmt*` and `lcsStatusWtpMngmt*` are the
  controller and AP-profile trees, including CAPWAP tunnels
  (`lcsStatusWlanMngmtWlcTunnelsWlcConnectionsTable[connId]`) and WIDS
  (`lcsStatusWlanMngmtWIdsAlarmsTable[macAddress, ifc, signature]`).

### Traps

- **5.2 MB of MIB.** Loading it is slow and walking it blind is worse. Target
  specific subtrees.
- The per-release `LC-UNIFIED-LCOS-*-REL-OIDS.mib` files exist because the OID
  assignments change between releases as the config tree grows. **The OID for a
  given setting is not stable across LCOS versions.** This is the most important
  operational fact about LANCOM SNMP and it is why the corpus keeps fifteen
  release files.
- Table names ending in `TableTable` are not typos — the LCOS node is called
  "…Table" and the MIB generator appends its own suffix.

---

## LCOS LX (access points)

**Corpus:** `LCOS-LX-MIB` (211 KB) plus two per-model files
(`LC-LX-6402-7.14.0036-RU1.mib`, `LC-LX-7500-7.14.0036-RU1.mib`).

Same generated-from-config-tree design, same `lcosLXStatus*` / `lcosLXSetup*`
split, different codebase and different names. Notable tables:

```
lcosLXStatusWLANRadios[Ifc]
lcosLXStatusWLANStationTable[MACAddress]
lcosLXStatusWLANClientManagementStationTable[MACAddress]
lcosLXStatusWLANChannelScanResults[radioChannel, interface, radioBand]
lcosLXStatusWLANChannelsAllowedByRegulatory[channel, radioBand]   ← the regulatory channel list
lcosLXStatusWLANManagementRadioprofiles[Name]
lcosLXStatusBridgeVLANTable[interfaceName, vlanId]
lcosLXStatusLANSpanningTreePortTable[Port]                        ← STP on an AP
lcosLXStatusLANLACP[BondName]                                     ← LACP on an AP uplink
lcosLXStatusIPConfigurationAddresses[interfaceName, ipVersion, addressSource, address]
lcosLXStatusL2TPEndpoints[L2TPEndpoint]
lcosLXSetupRADIUSLANSupplicant[interfaceName]                     ← the AP as an 802.1X supplicant
lcosLXSetupBridgeDHCPSnooping[Port]
```

`lcosLXStatusWLANChannelsAllowedByRegulatory` is worth calling out: it is the
only table in the corpus that tells you *which channels the regulatory domain
permits*, which explains a large fraction of "why won't it use that channel"
questions.

---

## LCOS SX (switches)

**Corpus:** two full firmware trees —
`sx-5.20-gs4530x/` (114 modules) and `sx-5.30-ys7154cf/` (124 modules) — plus
`LCOS-SX-MIB`, `LCOS-SX-GENERAL-MIB`, and per-model
`LC-GS-2310-3.34.0371-RU11.mib` and `LC-GS-3628XP-4.30.0439-RU9.mib`.

### It is FASTPATH, unrenamed

The firmware trees contain `fastpathswitching.mib`, `fastpath_boxservices.mib`,
`fastpath_mab.mib`, `fastpath_dhcp.mib`, `fastpath_keying.mib`,
`fastpath_auth_mgr.mib`, `fastpath_bonjour.mib`, `fastpath_dos.mib`,
`fastpath_interface_app.mib` and so on — defining modules literally named
`FASTPATH-SWITCHING-MIB`, `FASTPATH-BOXSERVICES-PRIVATE-MIB`, etc. LANCOM did
not rename anything. It also ships the standard IETF and IEEE MIBs in the same
tree (`bridge.mib`, `entity.mib`, `etherlike.mib`, `dot3ad.mib`, `dvmrp.mib`,
`bgp.mib`, `diffserv_dscp_tc.mib`, …).

`lancomref.mib` defines `LANCOM-REF-MIB` with a root node `lcosSX2` under
enterprise 2356 — the same "Reference MIB" pattern Broadcom uses, with the root
renamed.

### Plus LANCOM's own tables, on the same device

`LCOS-SX-MIB` and `LCOS-SX-GENERAL-MIB` add a parallel set:

```
lcsVlanTable[ifIndex]                          ← VLAN table keyed on ifIndex
lcsVLANPortStatusTable[ifIndex]
lcsVLANTranslationPortTable[index] / lcsVLANTranslationMappingTable[index]
lcsPoeStatusTable[LocalPort] / lcsPoeStatusTotalTable[SwitchIndex]
lcsPoePowerDelayTable[Port] / lcsPoeAutoCheckTable[Port]      ← PoE watchdog
lcsSFPInfoTable[index]
lcsCableDiagnosticsTable[Port]
lcsEnergyEfficientEthernetConfigTable[Port]
lcsLoopProtectionConfigurationTable[Port] / lcsLoopProtectionStatusTable[Port]
lcsIPStatusNeighborCacheTable[index]
lcsMonitoringTempSensorsTable[unitIndex, sensorIndex]
lcsMonitoringFansTable[unitIndex, fanIndex]
lcsMonitoringPSUTable[unitIndex, psuIndex]
lcsQosWREDTable[queue] / lcsQosPortShaperTable[ifIndex] / lcsQosPortSchedulersTable[ifIndex]
lcsDot1xSupplicantTable[index]
lcsConfigFileTable[index]
lcsSNMPCommunityConfTable[index]
lcsDHCPServerConfigTable[index]
lcsIPSubnetBasedVLANConfigTable[index]
lcsMacBaesdVlanMembershipConfigTable[index]     ← sic
lcssFlowRcvrTable[index] / lcssFlowFsTable[dataSource, instance]
```

**So the environment sensors, PoE, and several other entities are reported
twice** — once by `boxServicesFansTable[unit, index]` and once by
`lcsMonitoringFansTable[unitIndex, fanIndex]`. They can disagree. A decoder must
choose one deliberately, and the LANCOM-native one is the better bet because it
is what the LANCOM UI shows.

### And a third agent on one product line

`LANCOM-GS2310-FUNCTION-MIB` covers the GS-2310 with an entirely separate
`gs2310*` object set: `gs2310LACPPortConfigurationTable[Port]`,
`gs2310VlanPortsTable[Port]`, `gs2310IGMPSnoopingVLANTable[vlanID]`,
`gs2310MLDSnoopingVLANTable[vlanID]`, `gs2310DHCPSnoopingPortModeConfigurationTable[Port]`,
`gs2310IPSourceGuardPortConfigTable[Port]`, `gs2310ARPInspectionConfTable[portIndex]`,
`gs2310QosPortSchedulerTable[Port, Queue]`, `gs2310SFPInfoTable[index]`,
`gs2310FilteringDataBaseStaticMACTable[index]` /
`gs2310FilteringDataBaseDynamicMACTable[index]`,
`gs2310SyslogDetailedInfoTable[index]`, `gs2310RADIUSAuthenticationServerTable[index]`,
`gs2310TACACSPlusAuthenticationServerTable[index]`,
`gs2310LoopProtectionConfigurationTable[Port]`, `gs2310STPPortStatisticsTable[index]`,
`gs2310VoiceVLANPortTable[Port]`, `gs2310VlanPortIsolationTable[Port]`.

Note `gs2310FilteringDataBase*MACTable[index]` — the FDB keyed on a **row
index**, not on the MAC. Entries renumber between polls.

---

## LANCOM Management Cloud (LMC)

`spec/openapi/lancom/lmc-openapi/` — 13 OpenAPI 3.0.3 documents, one per cloud
microservice: `auth`, `backstage`, `config`, `control`, `devices`,
`devicetunnel`, `dsc`, `fields`, `jobs`, `logging`, `messaging`, `monitoring`,
`notification`. Fetched unauthenticated from
`https://cloud.lancom.de/cloud-service-<name>/api-docs/index.json`.

For an LMC-managed site, `devices` and `monitoring` are richer and far easier
than SNMP, and `devicetunnel` is how you reach a device behind NAT. OAuth2 token
from the `auth` service; API keys are created in the LMC UI.

Two services (`geolocation`, `preferences`) returned 401 unauthenticated and are
not vendored — recorded in `spec/openapi/lancom/SOURCES.md`.

---

## Summary for a decoder author

| Platform | Decoder | Effort |
|---|---|---|
| LCOS SX | reuse the FASTPATH decoder; add `lcs*` overrides where LANCOM's own tables are preferred; special-case GS-2310 | low |
| LCOS | bespoke, subtree-targeted, **version-aware OIDs** | high |
| LCOS LX | bespoke but small and regular | medium |
| LMC | REST client against the vendored OpenAPI | low |
