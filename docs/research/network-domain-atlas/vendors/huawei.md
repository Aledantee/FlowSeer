---
title: Vendor dossier — Huawei
date: 2026-08-30
scope: VRP switches, routers, WLAN, and the carrier surface
---

# Huawei

**Enterprise OIDs:** 2011 (Huawei) and 34774 (Huawei Symantec / storage).
**Products in scope:** S-series campus switches, AC-series WLAN controllers and
APs. Out of scope but heavily represented: NE/CX routers, MA5200 BRAS, OptiX
transport, OceanStor storage.
**Planes:** SNMP (very broad), NETCONF/YANG (models on-device via `get-schema`,
not published), and NCE/iMaster for cloud management. No YANG is vendored here.
**Corpus:** 240 modules, extracting to **2,554 distinct table names** — the
largest single-vendor surface in the atlas.

---

## Lineage

Huawei and 3Com's H3C joint venture split in 2003. Comware went to H3C (and
thence HP), VRP stayed with Huawei. **121 table names are still shared once the
`hw`/`hh3c` prefix is stripped** — the ACL family (`aclBasicRuleTable`,
`aclAdvancedRuleTable`, `aclNumGroupTable`, `aclIfRuleTable`), the whole CBQoS
family, MSTP (`mstpInstanceTable`, `mstpVIDAllocationTable`, `mstpPortTable`),
flash management (`flashTable`, `flhChipTable`), and BGP peer tables.

A Comware decoder is a useful starting point for Huawei and never a drop-in:
after twenty years the object names and semantics inside those
identically-shaped tables have drifted.

---

## Where the volume is

Of 240 modules, roughly:

| Group | Modules | In scope for FlowSeer? |
|---|---|---|
| BRAS / subscriber (`HUAWEI-BRAS-*`) | 34 | no |
| WLAN (`HUAWEI-WLAN-*`) | 13 | **yes** |
| MPLS / VPN / PWE3 / L2VPN | ~15 | no |
| Storage / server (`HUAWEI-STORAGE-*`, `-SERVER-IBMC-*`, `ISM-*`, `OPTIX-*`) | ~12 | no |
| PON / EPON / XPON | 4 | no |
| Security / firewall / NAT / ATK / SECSTAT | ~15 | partly |
| Core switching, routing, platform | the rest | **yes** |

That distribution is worth stating plainly: **most of Huawei's SNMP surface is
carrier equipment**, and a campus-switch decoder needs perhaps 30 of the 240
modules.

---

## The modules that matter for campus

| Entity | Module and key |
|---|---|
| Platform | `HUAWEI-ENTITY-EXTENT-MIB` — **the single most important Huawei MIB**: optical modules, temperature, CPU, memory, all keyed `entPhysicalIndex`; plus `HUAWEI-DEVICE-MIB`, `-DEVICE-EXT-MIB`, `-SYS-MAN-MIB` |
| Environment | `HUAWEI-ENVIRONMENT-MIB`: `hwFanStatusTable[slot, sn]`, `hwTemperatureThresholdTable[slot, i2cId, addr, channel]` — an **I²C-addressed key**, the most hardware-leaky in the corpus; `hwOpticalModuleTable[frame, type, chnIndex]` |
| Power / energy | `HUAWEI-POWER-MIB`, `HUAWEI-ENERGYMNGT-MIB` (`hwBoardPowerMngtTable`, `hwEnergySavingMethodTable`, `hwEnergySavingParameterTable`, `hwEnergySavingCapabilityMngtTable`) |
| Interfaces | `HUAWEI-IF-EXT-MIB`: `hwIFExtTable[hwIFExtIndex]`, **`hwIfQueryTable[hwIfName]`** (a name→index resolver shipped in the MIB), `hwTrunkIfTable[trunkIndex]` + `hwTrunkMemTable[trunkIndex, memIfIndex]`, `hwIfIpAddrTable[ifIndex, addr]`, `hwIfIpUnnumberedTable[ifIndex]`, `hwLogicIfTable`, `hwIfEtherStatTable` |
| PHY | `HUAWEI-PORT-MIB::hwEthernetTable[ifIndex]` |
| PoE | `HUAWEI-POE-MIB`: `hwPoePortTable[ifIndex]` — **the only ifIndex-keyed PoE model in the corpus** |
| L2 | `HUAWEI-L2IF-MIB`, `-L2VLAN-MIB`, `-L3VLAN-MIB` (`hwSubIfVlanTable[subIfIndex, vlanId]`), `-L2MAM-MIB` + `-SWITCH-L2MAM-EXT-MIB` (FDB, `hwdbCfgFdbTable[mac, vlanId, vsiName]` — **VSI in the key**), `-MSTP-MIB`, `-VBST-MIB`, `-QINQ-MIB`, `-L2MULTICAST-MIB` |
| L3 | `HUAWEI-ETHARP-MIB` (`hwEthARPShowWithInterAndVidTable[ifIndex, vid, ipAddr]` — **VLAN in the ARP key**), `-ND-MIB`, `-RM-EXT-MIB` (`hwStaticRouteTable[sourceVpnName, destIp, destMask, destVpnName, nextHop, outIfIndex]` — **two VPN names**, the only VRF-complete static route model here), `-VRRP-EXT-MIB` |
| Loop / link protection | four separate MIBs: `-LOOPDETECT-MIB`, `-LDT-MIB` (per-VLAN), `-MFLP-MIB` (MAC-flap detection), `-DLDP-MIB` |
| Redundancy | `-STACK-MIB`, `-HGMP-MIB` (cluster), `-M-LAG-MIB`, `-E-TRUNK-MIB`, `-MC-TRUNK-MIB`, `-SUPERLAG-MIB`, `-SMARTLINK-MIB`, `-RRPP-MIB`, `-ERPS-MIB` |
| Security | `-AAA-MIB` (domain/scheme model), `-HWTACACS-MIB`, `-MAC-AUTHEN-MIB`, `-PORTAL-MIB`, `-DHCP-SNOOPING-MIB` (`hwDhcpSnpBindTable[ipIndex, pVlan, cVlan, vrfId, vsiIndex]` — the most complete binding key in the corpus), `-MACSEC-MIB`, `-MFF-MIB`, `-ATK-MIB` (detected attack sources per VRF), `-SECSTAT-*` |
| Ops | `-SYSLOG-MIB`, `-INFOCENTER-MIB`, **`-ALARM-MIB`** (`hwAlarmActiveTable[targetAddrExtIndex, activeAlarmIndex]`, `hwAlarmSyncTable`, `hwEventSyncTable` — alarm resynchronisation per trap target, the best alarm model here), `-NTP-MIB`, `-CLOCK-MIB` (SyncE/BITS), `-PTP-MIB`, `-NETSTREAM-MIB`, `-BULKSTAT-MIB` (statistics pushed as files to FTP), `-CONFIG-MAN-MIB`, `-FLASH-MAN-MIB` |
| WLAN | `-WLAN-AP-MIB` (**a model catalogue**, `hwWlanApTypeTable[apType]` — not instances), `-WLAN-AP-RADIO-MIB`, `-WLAN-AP-SERVICE-MIB`, `-WLAN-AP-UPDATE-MIB`, `-WLAN-CAPWAP-MIB`, `-WLAN-CONFIGURATION-MIB` (instances, incl. `hwWlanNeighborRelationTable[apName, neighborApName]` — the AP neighbour graph), `-WLAN-GLOBAL-MIB`, `-WLAN-MIB`, `-WLAN-NPE-MIB`, `-WLAN-SAC-MIB`, `-WLAN-STATION-MIB`, `-WLAN-VAP-MIB`, `-WLAN-WIDS-SERVICE-MIB`, `-UNIMNG-MIB` (unified management of APs *and* switches: `hwAsTable[asIndex]`, `hwAsIfTable`, `hwAsSlotTable[asIndex, slotId]`) |

---

## Distinctive design habits

1. **VRF in the key, everywhere.** `mplsVpnVrfName` or `hwStaticRouteSourceVpnName`
   appears as an index in static routes, NAT zones, attack tables, ARP limits,
   and IP pools. Huawei is the most VRF-aware vendor in the corpus by a wide
   margin, and its tables are the best reference for what a VRF-complete key
   looks like.
2. **VLAN and VSI in L2 keys.** `hwEthARPShowWithInterAndVidTable`,
   `hwdbCfgFdbTable[mac, vlanId, vsiName]`,
   `hwDhcpSnpBindTable[..., vsiIndex]` — Huawei carries the bridge-domain
   dimension the IETF MIBs lack.
3. **Model catalogues alongside instances.** `hwWlanApTypeTable` describes AP
   *models* and their radio/port counts; instances live elsewhere. Useful (you
   can know an AP's capabilities before it joins) and a trap for anyone who
   walks it expecting inventory.
4. **Hardware-addressed keys.** The I²C temperature key is the extreme case.
   Several tables key on `[chassis, slot, card, subcard]` rather than on
   `entPhysicalIndex`, so ENTITY-MIB joins are not always available.
5. **`EUDM` suffix** (`-NAT-EUDM-MIB`, `-ASPF-EUDM-MIB`, `-ATK-EUDM-MIB`,
   `-PFLT-EUDM-MIB`, `-SECSTAT-EUDM-MIB`, `-SLOG-EUDM-MIB`) marks the
   Eudemon firewall product line. Different device class, same MIB tree.

---

## Traps

- **Scale.** Walking Huawei blind is expensive. `sysORTable` and targeted
  module selection matter more here than anywhere else.
- Several MIBs exist in `-MIB` and `-EXT-MIB` pairs where the extension carries
  the useful columns (`HUAWEI-IF-EXT-MIB`, `-VRRP-EXT-MIB`, `-L3VPN-EXT-MIB`,
  `-ENTITY-EXTENT-MIB`, `-SNMP-EXT-MIB`, `-L2TP-EXT-MIB`, `-RIPV2-EXT-MIB`,
  `-MPLS-EXTEND-MIB`, `-VPLS-EXT-MIB`).
- `NQA-MIB` in the Huawei directory is Huawei's, despite the un-prefixed name.
  Same for `HWMUSA-DEV-MIB`, `ISM-*`, and `OPTIX-*`.
- The WLAN MIB set changed shape between AC6605-era and AirEngine-era firmware;
  `HUAWEI-WLAN-MIB` and `HUAWEI-WLAN-CONFIGURATION-MIB` overlap and the
  populated one depends on version.
