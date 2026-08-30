---
title: Entity records — WAN and access technologies
date: 2026-08-30
part: 10 of 10
scope: serial/TDM/ATM, DSL, PON, DOCSIS, cellular, BRAS, Fibre Channel
---

# WAN and access technologies

Seven entities, all out of scope for FlowSeer's stated device classes. They are
recorded for three reasons: LANCOM routers in the lab do have DSL and cellular
interfaces; a corpus that includes 332 DSL tables and 308 PON tables will
otherwise keep surfacing them as unexplained noise; and two of them
([dsl](#dsl), [pon](#pon)) contain modelling patterns worth stealing.

Index: [wan-serial](#wan-serial) · [dsl](#dsl) · [pon](#pon) ·
[docsis](#docsis) · [cellular](#cellular) · [bras](#bras) ·
[fibre-channel](#fibre-channel)

---

## wan-serial

Legacy TDM and cell-based WAN: `DS1-MIB`, `DS3-MIB`, `SONET-MIB`, `ATM-MIB` +
`ATM-TC-MIB` + the six ATM Forum MIBs, `FRAME-RELAY-DTE-MIB`, `ISDN-MIB`,
`DIAL-CONTROL-MIB`, `SNA-SDLC-MIB`, `PPP` MIBs.

Vendor surfaces: Comware (`HH3C-E1-MIB`, `HH3C-T1-MIB`, `HH3C-ISDN-MIB`,
`HH3C-POS-MIB`, `HH3C-POSA-MIB`, `HH3C-ATM-DXI-MIB`, `HH3C-MP-MIB`,
`HH3C-PPP-MIB`, `HH3C-PPP-OVER-SONET-MIB`), Huawei (`HUAWEI-ATM-MIB`,
`HUAWEI-IMA-MIB`, `HUAWEI-PPP-MIB`, `HUAWEI-TDM-PSN-MIB`, `HUAWEI-LINE-MIB`),
Foundry lineage (`snPOSInfoTable[portNum]` / `snPOSInfo2Table[ifIndex]` —
another `2`-suffixed ifIndex-keyed replacement), LANCOM LCOS ISDN
(`lcsStatusIsdnLineS0Table[Ifc]`, `lcsStatusIsdnSignalingLayer2LapdTable[Channel]`),
Cisco IOS-XE alarm profiles for ds1/ds3/sonet-sdh.

One pattern worth noting: **the `2`-table replacement**. Foundry's
`snPOSInfoTable[snPOSInfoPortNum]` was superseded by
`snPOSInfo2Table[ifIndex]`, exactly as `snChasFanTable` was by
`snChasFan2Table[unit, index]` and `snVrrpVirRtrTable` by
`snVrrpVirRtr2Table`. A vendor that outgrows its own key adds a parallel table
rather than breaking the old one. Any collector for the Foundry lineage must
prefer the `2` table and fall back — and this is a general shape, not a Foundry
quirk.

---

## dsl

`ADSL-LINE-MIB` (RFC 2662) + `ADSL-LINE-EXT-MIB` + `ADSL-TC-MIB`,
`ADSL2-LINE-TC-MIB`, `VDSL-LINE-MIB`, `VDSL2-LINE-MIB` + `VDSL2-LINE-TC-MIB`,
`HDSL2-SHDSL-LINE-MIB`, `GBOND-MIB` (bonding), plus
`PerfHist-TC-MIB` / `HC-PerfHist-TC-MIB`.

**The pattern worth stealing** is the ADSL MIB's performance-history structure,
because it is the most careful treatment of time-series-over-SNMP anywhere in
the corpus:

- `adslAtucPerfDataTable [AUG adslLineEntry]` — current counters.
- `adslAtucIntervalTable[ifIndex, intervalNumber]` — **96 fifteen-minute
  buckets**, i.e. 24 hours of history, on the device.
- `adslAtucPerfDataExtTable` / `adslAtucIntervalExtTable` — the extensions.
- `adslLineAlarmConfProfileTable[profileName]` + `adslAlarmConfProfileExtTable`
  — thresholds as a *named profile* applied to many lines, rather than
  per-line settings.

The 15-minute-bucket-with-96-intervals convention comes from the ITU G.7710
performance-monitoring model and appears again in SONET-MIB, DOCSIS, and
`PerfHist-TC-MIB`. It is the telecoms answer to the same question RMON's history
group answers, and it is more rigorous: buckets are wall-clock aligned and
carry a validity flag.

Also `ATUC` vs `ATUR` throughout — the near end and the far end of the same
line, modelled as two symmetric table sets. That near/far duality shows up again
in [pon](#pon) (OLT/ONU) and in LLDP (local/remote).

Vendor: LANCOM LCOS is the only one in this corpus with a real DSL surface —
`lcsStatusAdslConnectionHistoryTable[index]`,
`lcsStatusAdslAdvancedDsBitLoadingTable[binNumber]` /
`...UsBitLoadingTable[binNumber]` (**per-subcarrier bit loading**, i.e. the DSL
spectrum), `lcsStatusVdslConnectionHistoryTable[index]`. Cisco IOS-XE has
`Cisco-IOS-XE-adsl` and `-controller-vdsl-oper`.

---

## pon

EPON and GPON: `HH3C-EPON-MIB` + `-EPON-DEVICE-MIB` + `-EPON-UNI-MIB` +
`-EPON-FB-MIB` + `HH3C-DOT3-EFM-EPON-MIB` (`hh3cDot3MpcpTable[ifIndex]`,
`hh3cDot3OmpEmulationTable[ifIndex]` — 802.3ah MPCP), `HUAWEI-EPON-MIB`,
`HUAWEI-XPON-MIB` + `-XPON-COMMON-MIB`, `UBNT-UFIBER-MIB`.

The keying is the interesting part: **`[ifIndex, onuIndex]` and
`[ifIndex, onuIndex, portId]`** — a three-level hierarchy (OLT port → ONU →
ONU's user port) expressed by extending the key rather than by nesting tables.
Compare CAPWAP's `[wtp, radio, station]` in
[08-wireless](08-wireless.md#the-four-architectures): the same three-level
shape, and both are cases where the managed device is not the device answering
SNMP.

That is the general lesson for FlowSeer: **whenever a controller answers for
subordinate devices, the key grows a level.** It happens in PON, in wireless, in
stacking, and in cluster management. A model that assumes "one agent, one
device" cannot express any of them.

---

## docsis

`DOCS-IF-MIB` (`docsIfDownstreamChannelTable[ifIndex]`,
`docsIfUpstreamChannelTable[ifIndex]`, `docsIfCmtsChannelUtilizationTable`,
`docsIfCmMacTable[ifIndex]`), `DOCS-CABLE-DEVICE-MIB`
(`docsDevNmAccessTable`, `docsDevEvControlTable[priority]`,
`docsDevEventTable[index]`, `docsDevFilterLLCTable`). Comware carries an EoC
(Ethernet over Coax) variant, `HH3C-EOC-COMMON-MIB`, and
`HH3C-CATV-TRANSCEIVER-MIB`.

Fully out of scope. Present because the IETF MIB set was vendored wholesale.

---

## cellular

`HH3C-3GMODEM-MIB` (`hh3cWirelessCardTable[cardIndex]`,
`hh3cUIMInfoTable[cardIndex, uimIndex]` — SIM info,
`hh3cSmsOperationTable[cardIndex]` — SMS over SNMP),
`HH3C-LTE-MEC-MIB`, `Cisco-IOS-XE-cellwan-oper`
(`cellwan-radio[cellular-interface]`), `Cisco-IOS-XE-cellular-rpc`,
`Cisco-IOS-XE-lte450-oper`, `Cisco-IOS-XE-controller` `Cellular/lte/{radio,
firmware}`.

Relevant to FlowSeer only if LANCOM routers with LTE backup are managed. The
observable that matters is signal strength plus the active APN/technology, and
LANCOM exposes it in the LCOS status tree rather than a standard MIB.

Note the name collision: `IEEE802.11u` cellular-network-information
(`lcsSetupIeee80211uCellularNetworkInformationListTable`) is Hotspot 2.0
advertising, not a cellular interface. Classifier false positive.

---

## bras

Broadband remote access server / subscriber management — PPPoE and IPoE session
termination at scale. Huawei has **34 `HUAWEI-BRAS-*` MIBs** and Comware has a
`HH3C-BRAS-ACCESS-MIB` plus the CUPM control/user-plane split
(`HH3C-CUPM-CP-MIB`, `HH3C-CUPM-UP-MIB`).

The one relevant fragment is PPPoE, which appears on LANCOM
(`lcsStatusPppoeServerConnectionsTable[channel, macAddress]`,
`lcsSetupLanBridgePppoeSnoopingTable[port]`) and Cisco
(`Cisco-IOS-XE-ppp-oper::pppoe/pppoe-session-list[ifname]`,
`Cisco-IOS-XE-bba-group`).

Everything else here is carrier equipment. The 335 matched tables are the single
largest block of clearly-out-of-scope evidence in the corpus, and knowing that
is itself useful when sizing a "support everything Huawei ships" ambition.

---

## fibre-channel

`FCMGMT-MIB` (`connUnitTable[connUnitId]`, `connUnitPortTable[unitId,
portIndex]`, `connUnitSensorTable[unitId, sensorIndex]`,
`connUnitRevsTable[unitId, index]`), Comware's eight `HH3C-FC-*` MIBs plus
`HH3C-FCOE-MIB`, `HH3C-VSAN-MIB`, `HH3C-NPV-MIB`, `HH3C-FDMI-MIB`, and Huawei's
`HUAWEI-FCOE-MIB`.

Out of scope. One observation worth keeping: `FCMGMT-MIB`'s `connUnit*` family
is a *complete parallel universe* to ENTITY-MIB + IF-MIB — its own component
tree, its own port table, its own sensors — invented because the FC world did
not want to reuse the IP world's MIBs. It is a cautionary example of what
happens when a domain models its own hardware tree instead of extending the
shared one, and it argues for FlowSeer keeping one component model
([hw-component](01-platform.md#hw-component)) rather than per-domain ones.
