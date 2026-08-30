---
title: Entity records — wireless
date: 2026-08-30
part: 8 of 10
scope: controllers, access points, radios, BSS/VAP, clients, RF, rogue/WIDS, mesh, roaming, guest, app visibility
---

# Wireless

Eleven entities. FlowSeer manages APs and WLAN controllers, and
`spec/proto/flowseer/net/wlan/v1/` is an empty `.gitkeep` — so unlike the
switching side, nothing here is modelled and everything is open.

This is also the domain where **the controller, not the device, is the source of
truth**, which changes the collection architecture: an AP's state is read from
its controller, and FlowSeer's Binding concept (device reachable *via* an
integration) is a better fit here than anywhere else.

Index: [wlan-controller](#wlan-controller) · [access-point](#access-point) ·
[radio](#radio) · [bss-vap](#bss-vap) · [wireless-client](#wireless-client) ·
[wireless-rf](#wireless-rf) · [rogue-wids](#rogue-wids) · [mesh](#mesh) ·
[roaming](#roaming) · [wireless-guest](#wireless-guest) ·
[app-visibility](#app-visibility)

---

## The four architectures

Before the entity records, the structural fact that governs all of them. The
corpus contains four different wireless architectures, and each puts the same
entities in a different place:

1. **Controller-based (split MAC, CAPWAP)** — the AP is a thin WTP, the
   controller holds all state. Aruba (WLSX), Huawei (`HUAWEI-WLAN-*`), Comware
   (`HH3C-DOT11-*`), Cisco 9800 (`Cisco-IOS-XE-wireless-*`), Ruckus SmartZone,
   LANCOM WLC (`lcsStatusWlanMngmt*`). The standard is
   **`CAPWAP-BASE-MIB` (RFC 5833)**.
2. **Controller-less / cluster** — one AP elects itself master. Aruba Instant
   (`AI-AP-MIB`), Ruckus Unleashed (`RUCKUS-UNLEASHED-*`), UniFi without a
   controller.
3. **Cloud-managed** — the state lives in a vendor cloud API. UniFi Site
   Manager, LANCOM LMC, Aruba Central, Ruckus R1. No MIB at all.
4. **Standalone AP** — the AP answers for itself. LANCOM LX
   (`LCOS-LX-MIB`), HP ProCurve 420 (`HP-PROCURVE-420-PRIVATE-MIB`), airMAX.

**Only architecture 4 makes the AP an SNMP-reachable Device in the ordinary
sense.** For 1–3 the AP is data inside another system, and FlowSeer's
Integration + Binding + Integration Scope model is exactly the right shape for
it — Ruckus SmartZone's `domains` → `rkszones` → `aps` hierarchy maps onto
Integration Scope almost one-to-one.

### CAPWAP-BASE-MIB, the standard nobody uses

`CAPWAP-BASE-MIB` (RFC 5833) is the only IETF wireless-controller model:

| Table | Key |
|---|---|
| `capwapBaseWtpProfileTable` | `capwapBaseWtpProfileId` |
| `capwapBaseWtpStateTable` | `capwapBaseWtpStateWtpId` |
| `capwapBaseWtpTable` | `capwapBaseWtpCurrId` |
| `capwapBaseWirelessBindingTable` | `wtpCurrId, radioId` |
| `capwapBaseStationTable` | `wtpCurrId, radioId, stationId` |
| `capwapBaseAcNameListTable`, `capwapBaseMacAclTable` | |
| `capwapBaseRadioEventsStatsTable` | `wtpCurrId, radioId` |

The three-level key **(WTP, radio, station)** is the correct decomposition and
every vendor reinvents it with different names. Nobody in this corpus implements
the MIB — Huawei has `HUAWEI-WLAN-CAPWAP-MIB` and Comware has
`hh3cDot11CAPWAPTunnelTable`, both private. It is still the best available
statement of the entity relationships.

---

## wlan-controller

**What it is.** The controller itself: its identity, cluster membership,
capacity, licence, and the scope hierarchy it imposes.

| Family | Surface |
|---|---|
| Aruba | `WLSX-SWITCH-MIB::wlsxSwitchListTable[switchIPAddress]` (the controller cluster), `wlsxSwitchLicenseTable[licenseIndex]`, `wlsxSysXProcessorTable`, `wlsxSysXStorageTable`; `WLSX-SYSTEMEXT-MIB` |
| Ruckus SmartZone | `RUCKUS-CTRL-MIB`: `ruckusCTRLSystemNodeTable[serialNumber]` (cluster nodes), **`ruckusCTRLDomainTable[domainId]` → `ruckusCTRLZoneTable[domainId, zoneId]`** — the scope hierarchy; `RUCKUS-SZ-SYSTEM-MIB`; and the vSZ REST API with 688 paths, 232 of them under `/rkszones` |
| Ruckus ZoneDirector | `RUCKUS-ZD-SYSTEM-MIB` |
| Ruckus Unleashed | `RUCKUS-UNLEASHED-SYSTEM-MIB` |
| Huawei | `HUAWEI-WLAN-GLOBAL-MIB`, `HUAWEI-WLAN-CONFIGURATION-MIB`, `HUAWEI-WLAN-CAPWAP-MIB`, `HUAWEI-UNIMNG-MIB` (`hwAsTable[asIndex]`, `hwAsIfTable`, `hwAsSlotTable[asIndex, slotId]` — unified management of "AS" devices, i.e. APs *and* switches through one controller) |
| Comware | `HH3C-DOT11-ACMT-MIB` (AC management), `HH3C-DOT11-CFG-MIB` |
| Cisco 9800 | `Cisco-IOS-XE-wireless-general-cfg` / `-general-oper`, `-mobility-cfg` / `-oper`, plus 90 `Cisco-IOS-XE-wireless-*` modules total |
| LANCOM | `lcsStatusWlanMngmt*` on a WLC, `lcsStatusWtpMngmt*` on the AP side |

**The licence point** from [license](01-platform.md#license) applies here:
`wlsxSwitchLicenseTable` and `HH3C-DOT11-LIC-MIB` gate AP adoption. A controller
at its AP limit rejects new APs, and that presents as a discovery failure.

---

## access-point

**What it is.** An AP as an inventory item: MAC, serial, model, name, group,
location, uptime, firmware, IP, and its adoption/join state.

| Family | Key | Surface |
|---|---|---|
| Aruba Instant | AP MAC | `AI-AP-MIB::aiAccessPointTable[aiAPMACAddress]` |
| Aruba controller | AP MAC | `WLSX-WLAN-MIB::wlsxWlanAPTable[wlanAPMacAddress]`, `wlsxWlanAPGroupTable[wlanAPGroup]`; `WLSR-AP-MIB` for the AP-resident view |
| Comware | AP ID | `HH3C-DOT11-APMT-MIB::hh3cDot11APObjectTable[apObjID]`, `hh3cDot11APObjectStatusTable[apID]` |
| Huawei | AP type / MAC | `HUAWEI-WLAN-AP-MIB::hwWlanApTypeTable[apType]`, `hwWlanApTypeRadioTable[apType, radioIndex]`, `hwWlanApTypeWiredPortTable[apType, portIndex]` — note this is an **AP *model* catalogue**, not the AP instances; instances are in `HUAWEI-WLAN-CONFIGURATION-MIB` keyed on AP MAC |
| Ruckus ZD | config ID | `RUCKUS-ZD-AP-MIB::ruckusZDAPConfigTable[configID]`, `ruckusZDAPLBSVenueTable[apGroupID]` |
| Ruckus SmartZone | AP MAC | `ruckusCTRLSummaryApTable[indexType, indexUUID, apMac]`, `ruckusSZAPTable[apMac]`; REST `/aps` (86 paths) |
| Cisco 9800 | WTP MAC | `Cisco-IOS-XE-wireless-access-point-oper::access-point-oper-data/capwap-data[wtp-mac]`, `/capwap-pkts[wtp-mac]`; `-ap-cfg`, `-ap-global-oper`, `-ap-types` |
| LANCOM WLC | index | `lcsStatusWlanInterpointsAccesspointListTable[index]`, plus the `lcsStatusWlanMngmtApConf*` profile tree |
| UniFi | — | no AP table in `UBNT-UniFi-MIB` beyond radios; the Network API is the surface |

**The key is the AP's Ethernet MAC almost everywhere**, which is a genuinely
good cross-vendor identity — better than anything on the switching side. Ruckus
SmartZone additionally carries a UUID. Huawei's model/instance split is the
odd one out and will mislead anyone who walks `hwWlanApTypeTable` expecting
inventory.

For FlowSeer: an AP reached through a controller is a Device with a Binding to
the controller Integration, placed in an Integration Scope (the zone/AP
group/site). That is exactly what the inventory model already expresses.

---

## radio

**What it is.** One 802.11 radio on an AP: band, channel, width, transmit
power, antenna, mode (access/monitor/mesh), and its PHY statistics.

### Canonical models

`IEEE802dot11-MIB` — `dot11PhyOperationTable[ifIndex]`,
`dot11PhyAntennaTable[ifIndex]`, `dot11PhyTxPowerTable[ifIndex]`,
`dot11PhyDSSSTable`, `dot11PhyOFDMTable`, `dot11StationConfigTable[ifIndex]`,
`dot11OperationTable`, `dot11CountersTable`. **Keyed on `ifIndex`**, because it
models a radio as an interface — which is right for a standalone AP and useless
for a controller holding 500 APs.

`openconfig-wifi-phy` and `openconfig-wifi-mac` (both present in the corpus's
OpenConfig set, via `openconfig-access-points`):
`access-points/access-point[hostname]/radios/radio[id]` with `config/{operating-frequency,
channel, channel-width, transmit-power, enabled}` and rich `state` including
scan and neighbour data. This is the modern model and it uses the
**(AP, radio-id)** key that the vendors converge on.

### Vendor mapping

| Family | Key |
|---|---|
| Aruba Instant | `aiRadioTable[aiRadioAPMACAddress, aiRadioIndex]` |
| Aruba controller | `wlsxWlanRadioTable[wlanAPMacAddress, wlanAPRadioNumber]`, `wlsxWlanAPRadioStatsTable[same]` |
| Comware | `hh3cDot11APRadioTable[curAPID, radioID]`, `hh3cDot11RadioAssocStatisTable[same]`, `hh3cDot11RadioMngFrmStatisTable[apID, radioID, frameType]` |
| Huawei | `hwWlanRadioInfoTable[apMac, radioID]`, `hwWlanRadioQueryPowerlevelTable[apMac, radioId, channel, bandwidth]` |
| Ruckus SmartZone | `ruckusCTRLApRadioTable[apMac, radioIndex]` |
| Ruckus ZD | `ruckusZDWLANAPRadioStatsTable[apMacAddr, radioIndex]` |
| LANCOM LCOS/LX | `lcsStatusWlanRadiosTable[Ifc]`, `lcosLXStatusWLANRadios[Ifc]` — **interface-keyed**, the standalone-AP shape |
| UniFi | `UBNT-UniFi-MIB::unifiRadioTable[unifiRadioIndex]` |
| airMAX | `UBNT-AirMAX-MIB::ubntRadioTable[index]` + `ubntRadioRssiTable[index, rssiIndex]` |
| HP ProCurve 420 | `hpdot11PhyOperationTable[hpdot11Index]` — the IEEE MIB, renamed |
| IETF | `capwapBaseWirelessBindingTable[wtpCurrId, radioId]` |
| Cisco 9800 | `Cisco-IOS-XE-wireless-radio-cfg`, `-rf-cfg`, `-dot11-cfg`, `-power-cfg` |

**Nine of eleven use (AP identity, radio index).** That is the strongest
cross-vendor agreement anywhere in this atlas and makes the radio key an easy
decision.

Watch for: band is not the same as radio index (tri-band APs, 6 GHz), channel
width has grown to 320 MHz in 802.11be, and transmit power is reported variously
in dBm, mW, and as a percentage of maximum.

---

## bss-vap

**What it is.** A broadcast SSID on one radio — the BSSID — and the WLAN/SSID
*profile* it instantiates. Two entities that are constantly merged.

- **WLAN / SSID profile**: name, SSID string, security (WPA2/WPA3, PSK/802.1X),
  VLAN, band steering, client isolation, rate limits. Configuration, one per
  controller.
- **BSS / VAP**: the instantiation of a profile on a specific radio, with its
  own BSSID and client count. Observation, one per (AP, radio, WLAN).

| Family | Profile | BSS/VAP |
|---|---|---|
| Aruba | `AI-AP-MIB::aiWlanSSIDTable[aiSSIDIndex]` | `wlsxWlanAPBssidTable[apMacAddress, radioNumber, apBSSID]` |
| Comware | `hh3cDot11ServicePolicyTable[servicePolicyID]`, `hh3cDot11ServicePolicyExtTable`, `hh3cDot11RadioPolicyTable[policyName]` | `hh3cDot11APBSSTable[curAPID, radioID, wlanID]` |
| Huawei | `HUAWEI-WLAN-VAP-MIB`, `HUAWEI-WLAN-MIB` | per-VAP tables keyed on AP MAC + radio + WLAN |
| Ruckus SmartZone | `RUCKUS-SZ-CONFIG-WLAN-MIB::ruckusSZConfigWLANTable[wlanID]` | `RUCKUS-SZ-WLAN-MIB::ruckusSZWLANTable[index]` |
| Ruckus ZD | `RUCKUS-ZD-WLAN-CONFIG-MIB` | `RUCKUS-ZD-WLAN-MIB` |
| Ruckus Unleashed | `RUCKUS-UNLEASHED-WLAN-MIB` | |
| Cisco 9800 | `Cisco-IOS-XE-wireless-rlan-cfg`, `-apf-cfg`, `-hotspot-cfg` | in `-access-point-oper` |
| LANCOM | `lcsSetupWlanNetwork*` / `lcsStatusWlanMngmtApConf*` | |

**The BSSID is the join key** between this entity, the FDB
([fdb](03-switching.md#fdb) — a BSSID appears as a MAC on the wired side),
[rogue-wids](#rogue-wids) (a rogue is identified by BSSID), and
[wireless-client](#wireless-client) (a client associates to a BSSID). Modelling
BSSID as a first-class value pays for itself several times.

---

## wireless-client

**What it is.** An associated station: MAC, IP, username, AP, radio, BSSID,
SSID, signal, rate, and traffic counters. 523 MIB tables and 2,326 YANG nodes —
the largest wireless entity by evidence, and the one operators look at most.

| Family | Surface |
|---|---|
| Aruba Instant | `AI-AP-MIB::aiClientTable[aiClientMACAddress]`, `aiVoiceClientTable[clientMac]` |
| Aruba controller | `WLSX-USER-MIB` / `WLSX-USER6-MIB` (the user table — **with the authenticated identity**), `WLSX-STATS-MIB`, `WLSX-MON-MIB::wlsxMonStationStatsTable[monPhyAddress, monRadioNumber, monitoredStaPhyAddress]` and `wlsxMonStationInfoTable[same]` — note the key: **monitoring AP + radio + monitored station**, i.e. one row per *observer*, so the same client appears many times |
| Ruckus SmartZone | `ruckusCTRLClientTable[clientMac]` (controller-wide), `ruckusCTRLApClientTable[apMac, clientMac]` (per AP), `ruckusCTRLApWiredClientTable[apMac, clientMac]` (**clients on the AP's Ethernet ports**) |
| Comware | `HH3C-DOT11-STATION-MIB`, `HH3C-DOT11-ACMT-MIB`, `hh3cDot11ACIFLoadInfoTable[acIfIndex]` |
| Huawei | `HUAWEI-WLAN-STATION-MIB`, `HUAWEI-WLAN-SAC-MIB` |
| LANCOM LCOS | `lcsStatusWlanStationTableTable[index]`, `lcsStatusWlanForeignStationsTable[macAddress]`, `lcsStatusLanStationTableTable[interface, macAddress]`, `lcsStatusPublicSpotStationTableTable[macAddress]` |
| LANCOM LX | `lcosLXStatusWLANStationTable[macAddress]`, `lcosLXStatusWLANClientManagementStationTable[macAddress]` |
| Cisco 9800 | `Cisco-IOS-XE-wireless-client-oper`, `-client-global-oper`, `-client-types`, `-client-rpc` |
| IETF | `capwapBaseStationTable[wtpCurrId, radioId, stationId]` |
| airMAX / airFiber | `UBNT-AFLTU-MIB::afLTUStationTable[remoteMac]`, `UI-AF60-MIB::af60StationTable[staMac]` — point-to-multipoint "stations", a different meaning |
| Ruckus SCI/SCG proto | `spec/proto/ruckus/ap/ap_client.proto`, `ap_wired_client.proto` — the **streaming telemetry** form of the same entity |

### Notes

- **Key choice**: client MAC alone is the natural identity, but it is not unique
  over time (randomised MACs) and not unique across controllers. Aruba's
  observer-keyed monitoring tables and Ruckus's per-AP tables are *views*, not
  identities. Model the client keyed on MAC within the controller's scope, with
  AP/radio/BSSID as attributes.
- **MAC randomisation** has broken MAC-as-identity for client tracking since
  iOS 14 / Android 10. Every wireless inventory built after 2020 has to carry
  the "is this a randomised (locally-administered) MAC" bit, which is simply the
  U/L bit of the first octet. FlowSeer's `net/addr/v1/eui.proto` should expose
  it if it does not already.
- The **wired client behind an AP** (`ruckusCTRLApWiredClientTable`,
  `ap_wired_client.proto`) is a real and frequently forgotten case.
- Aruba's `WLSX-USER-MIB` carries the *authenticated username*, which links this
  entity to [dot1x](07-security.md#dot1x) and is the same differentiator
  identified there.

---

## wireless-rf

**What it is.** The RF environment and the automatic management of it —
channel selection, power control, noise, interference, and the neighbour graph
between APs.

| Family | Surface |
|---|---|
| Aruba | `WLSX-SNR-MIB`, `WLSR-AP-MIB::wlsrChannelStatsTable[channel]`, `wlsrChannelRateStatsTable`, `wlsrChannelDATypeStatsTable`, `wlsrChannelFrameTypeStatsTable` — per-channel airtime breakdown by frame type, unusually detailed; ARM is configured through `WLSX-WLAN-MIB` |
| Huawei | `HUAWEI-WLAN-CONFIGURATION-MIB::hwWlanNeighborRelationTable[apName, neighborApName]` — **the AP neighbour graph**, which is what RRM actually runs on; `hwWlanRadioQueryPowerlevelTable[apMac, radioId, channel, bandwidth]` |
| Comware | `HH3C-DOT11-RRM-MIB`, `HH3C-DOT11-PROBE-MIB` (`hh3cDot11PROBERadioCfgTable[apName, radioId]`, `hh3cDot11PROBEApTable[apMacAddress]`, `hh3cDot11PROBEClientTable[clientMac]`) |
| LANCOM LCOS | `lcsStatusWlanChannelScanResultsTable[radioBand, radioChannel, interface]`, `lcsStatusWlanSpectralScanAllChannelsTable[index]` (**spectral analysis**), `lcsStatusWlanNoiseImmunityCurrentParametersTable[band, channel, interface]`, `lcsStatusWlanNoiseImmunityLogTableTable[index]`, `lcsStatusWlanEnvironmentScanResultsTable[bssid, interface]` |
| LANCOM LX | `lcosLXStatusWLANChannelScanResults[radioChannel, interface, radioBand]`, `lcosLXStatusWLANChannelsAllowedByRegulatory[channel, radioBand]` (**the regulatory channel list**, which nothing else exposes and which explains half of "why won't it use that channel") |
| Cisco 9800 | `Cisco-IOS-XE-wireless-rf-cfg`, `-afc-oper` / `-afc-cloud-oper` (6 GHz Automated Frequency Coordination) |
| Ruckus | ChannelFly is not exposed in the MIBs; SmartZone REST has it |
| OpenConfig | `openconfig-wifi-mac` scan and neighbour state |

**The AP neighbour graph** (Huawei's `hwWlanNeighborRelationTable`, and the same
data inside every RRM implementation) is the wireless analogue of
[lldp](03-switching.md#lldp): it is how you build a wireless topology. Only
Huawei exposes it as a table in this corpus.

---

## rogue-wids

**What it is.** APs and stations seen in the air that are not ours, classified
by threat, plus attack signatures.

| Family | Surface |
|---|---|
| Aruba | `WLSX-RS-MIB` (rogue/RAPIDS), `WLSX-MON-MIB::wlsxMonAPStatsTable[monPhyAddress, monRadioNumber, monitoredApBSSID]` and the rate/DA-type variants, `WLSR-AP-MIB::wlsrAirMonitorApListTable[amApBSSID]` |
| Comware | `HH3C-DOT11-WIDS-MIB`: `hh3cDot11WIDSPermitVendorTable[vendorOUI]`, `hh3cDot11WIDSPermitSSIDTable[permitSSID]`, `hh3cDot11WIDSIgnoreListTable[ignoreMAC]`, `hh3cDot11WIDSAttackListTable[attackDeviceMac]` — an allow/ignore/attack triple; plus `HH3C-DOT11-WIPS-MIB`, `HH3C-WIPS-MIB` |
| Huawei | `HUAWEI-WLAN-WIDS-SERVICE-MIB::hwWidsProfileTable[profileName]`, `hwWidsSpoofProfileTable[profileName, ssidRegex]` |
| Ruckus ZD | `RUCKUS-ZD-WLAN-MIB::ruckusZDWLANRogueTable[rogueIndex]` |
| LANCOM | `lcsStatusWlanWIdsEventTableTable[eventType, id]`, `lcsStatusWlanMngmtWIdsEventTableTable[macAddress, eventType, id]`, `lcsStatusWlanMngmtWIdsAlarmsTable[macAddress, ifc, signature]` |
| Cisco 9800 | `Cisco-IOS-XE-wireless-rogue-cfg`, `-rogue-authz-rpc`, `-awips-oper` (`awips-alarm[alarm-timestamp]`, `st-awips-per-ap-alarm`, and named alarms like `alarm-krack`) |
| Ruckus SCI proto | `spec/proto/ruckus/ap/ap_rogue.proto`, `sci-rogue.proto` |

**Two different data shapes here**, and they should not be one entity:

1. A **rogue AP/station record** — BSSID, SSID, channel, RSSI, first/last seen,
   classification. This is state with a lifetime.
2. A **WIDS event** — an attack signature fired at a time. This is an event.

LANCOM and Cisco model both; most vendors model only one. FlowSeer's
Config/State/Event triad handles this cleanly if the split is respected.

---

## mesh

**What it is.** APs backhauling wirelessly to each other: the mesh link, its
parent/child relationship, and link quality.

| Family | Surface |
|---|---|
| Aruba | `WLSX-MESH-MIB::wlsxMeshNodeTable[wlanAPMacAddress]`, `AI-AP-MIB::aiMeshTable[aiMeshIndex]` |
| Comware | `HH3C-DOT11S-MESH-MIB` (802.11s): `hh3cDot11sMeshPflTable[index]`, `hh3cDot11sMpPlcyTable[index]`, `hh3cDot11sMlspCfgTable[policyIndex, proxyIndex]`; plus `hh3cDot11WlanMeshIfTable[meshIfNumber]` |
| Cisco 9800 | `Cisco-IOS-XE-wireless-mesh-cfg`, `-mesh-oper`, `-mesh-global-oper`, `-mesh-rpc` |
| Ruckus | `spec/proto/ruckus/ap/ap_mesh.proto` — mesh state as streaming telemetry |
| LANCOM | AutoWDS (`lcsStatusWlanMngmtApConfAutowdsTopoErrorsTable[index, priority, slaveApName, slaveApWlanIfc]`) |
| Huawei | mesh objects inside `HUAWEI-WLAN-*` |

The mesh link is a **parent/child edge between two APs**, which makes it a
topology entity like [lldp](03-switching.md#lldp) rather than an AP attribute.
LANCOM's table key even names the slave AP and its WLAN interface. If FlowSeer
ever builds a topology graph, wired LLDP edges and wireless mesh edges belong in
the same structure with different edge kinds.

---

## roaming

**What it is.** A client moving between APs, and the controller-to-controller
mobility that supports it.

| Family | Surface |
|---|---|
| Aruba | `WLSX-MOBILITY-MIB`, `WLSX-HA-MIB` (controller HA and AP failover), `WLSX-TUNNELEDNODE-MIB` |
| Comware | `HH3C-DOT11-ROAM-MIB` |
| Cisco 9800 | `Cisco-IOS-XE-wireless-mobility-cfg` / `-oper` / `-types` |
| Ruckus | in the SmartZone REST API |
| 802.11 standards | 802.11r fast transition, 802.11k neighbour reports, 802.11v BSS transition management — configured per WLAN, observable only as client behaviour |

Thin in the MIBs, rich in the YANG and REST surfaces. The observable worth
capturing is the **roam event** (client, from-AP, to-AP, time, reason), which is
an Event in FlowSeer's triad and is where client-experience analysis starts.

---

## wireless-guest

**What it is.** Captive-portal guest access: the portal, its user database, and
guest sessions.

Overlaps heavily with [dot1x](07-security.md#dot1x)'s web-auth entry.
Wireless-specific surfaces: LANCOM's Public Spot
(`lcsStatusPublicSpotStationTableTable[macAddress]`,
`lcsSetupPublicSpotModuleVlanTableTable[vlanId]`,
`lcsSetupPublicSpotModuleMacAddressTableTable[macAddress]` — a complete
hotspot with its own user store), Ruckus ZD guest access,
`Cisco-IOS-XE-wireless-hotspot-cfg` (Hotspot 2.0 / Passpoint),
`EdgeSwitch-CAPTIVE-PORTAL-MIB` on the wired side, Aruba's ClearPass
(`CPPM-MIB`).

Hotspot 2.0 / Passpoint is worth a separate note: it changes guest access from
a portal to an 802.1X-with-roaming-consortium model, and only Cisco exposes it
in this corpus.

---

## app-visibility

**What it is.** Per-client, per-application traffic classification — DPI on the
AP or controller.

| Family | Surface |
|---|---|
| Ruckus | **`spec/proto/ruckus/ap/ap_avc.proto` and `ap_avc_all.proto`** — AVC as protobuf streaming telemetry from the AP, plus `/avc` (19 paths) in the vSZ REST API. This is the richest app-visibility surface in the corpus and it is already vendored |
| Comware | `HH3C-DAR-MIB` (deep application recognition) |
| Huawei | `HUAWEI-BRAS-DPI-MIB` |
| Cisco | NBAR2, exposed through Flexible NetFlow rather than a model |

Note that Ruckus's telemetry protos in `spec/proto/ruckus/` are a *different
kind of source* from everything else in this atlas: a push-based, protobuf-typed
stream (`ap_report.proto`, `ap_status.proto`, `ap_client.proto`,
`ap_avc.proto`, `ap_rogue.proto`, `ap_mesh.proto`, `ap_peerlist.proto`,
`ap_hccd_report.proto`, `ap_wired_client.proto`, plus the SCI and SCG message
sets). For FlowSeer, whose own model is protobuf, they are the closest thing in
the corpus to a native wire format — worth studying for shape even where the
content is Ruckus-specific.
