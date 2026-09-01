---
title: Vendor dossier — HPE (Comware, ProCurve/AOS-S, BladeSystem)
date: 2026-08-30
scope: HPE Comware (H3C), HP ProCurve / ArubaOS-Switch, HP BladeSystem, and the Foundry-derived HP-SN set
---

# HPE

`spec/mib/hp/` holds **370 modules across four unrelated MIB families**, which
makes it the most confusing directory in the corpus. Sorting them out first:

| Prefix | Directory | Platform | Lineage |
|---|---|---|---|
| `HH3C-*` (271) | `hp/hh3c/` | Comware 5/7 switches and routers | H3C |
| `HP-ICF-*`, `CONFIG-MIB`, `STATISTICS-MIB`, `NETSWITCH-*` (~50) | `hp/procurve/` | ProCurve / ArubaOS-Switch (AOS-S) | HP ICF |
| `HP-SN-*` (~20) | `hp/procurve/` | HP 9300 routing switches | **Foundry** — 174 shared table names with `FOUNDRY-SN-*` |
| `BLADETYPE*`, `CPQ*`, `HP-LASERJET-*` (17) | `hp/other/` | BladeSystem switches, ProLiant servers, printers | Blade/Compaq |

Enterprise OIDs: **11** (HP), **232** (Compaq), **25506** (H3C).

---

## Comware

**Products:** HPE FlexNetwork/FlexFabric 5000–12000, and every H3C-badged switch
and router.
**Plane:** NETCONF over SSH (port 830) and SOAP/HTTPS. Data models are
H3C-proprietary XML defined by XSD shipped per firmware release — **not YANG**,
and behind a support login. Devices do support RFC 6022 `get-schema`, so the
authoritative way to obtain models is to pull them off a live box. SNMP is the
practical alternative and is very broad.
**Corpus:** 271 `HH3C-*` modules, extracting to 1,480 distinct table names.

### Shape

Comware's MIB set is the most *complete* in the corpus for a general-purpose
switch/router — it covers essentially every feature the platform has, including
several nothing else models:

- **Config management as a job model.** `HH3C-CONFIG-MAN-MIB`:
  `hh3cCfgLogTable[index]` (who changed what, when),
  `hh3cCfgOperateTable[index]` (trigger a save/load),
  `hh3cCfgOperateResultTable`, `hh3cCfgExecuteResultTable`. Nothing else in the
  corpus is this complete for configuration operations.
- **ACL framework.** `HH3C-ACFP-MIB` lets multiple *clients* install rule sets:
  `hh3cAcfpClientInfoTable[clientID]` → `hh3cAcfpPolicyTable[clientID,
  policyIndex]` → `hh3cAcfpRuleTable[clientID, policyIndex, ruleIndex]`.
- **CBQoS.** `HH3C-CBQOS2-MIB` + `HH3C-IFQOS2-MIB` — classifier/behaviour/policy
  with per-interface run-time queue statistics
  (`hh3cCBQoSIfQueueRunInfoTable[ifIndex, direction, classIndex]`).
- **Subinterfaces and VLAN termination** modelled explicitly:
  `hh3cRTParentIfTable` / `hh3cRTSubIfTable[parentIfIndex, ordinal]`,
  `HH3C-VLANTERM-MIB::hh3cVlanTermDot1qTable[ifIndex, vidStart]`,
  `hh3cVlanTermQinqTable[ifIndex, firstVlan, secondVlanStart]`.
- **Full wireless suite** — 22 `HH3C-DOT11-*` modules covering AP management,
  radios, BSS, stations, WIDS/WIPS, mesh (802.11s), roaming, RRM, QoS, SAVI,
  licensing.
- **Full storage/FC suite** — 8 `HH3C-FC-*` plus FCoE, VSAN, NPV, FDMI.
- **MDC** (`HH3C-MDC-MIB`) — device virtualisation with per-MDC CPU/memory/disk
  resource tables. Not a VRF; see [vrf](../entities/04-ip.md#vrf).

### Traps

- Many features exist in two generations with a `2` suffix:
  `HH3C-DHCPSNOOP-MIB` / `-DHCP-SNOOP2-MIB`, `HH3C-DLDP-MIB` / `-DLDP2-MIB`,
  `HH3C-QINQ-MIB` / `-QINQV2-MIB`, `HH3C-RMON-EXT-MIB` / `-EXT2-MIB`,
  `HH3C-MP-MIB` / `-MP-V2-MIB`, `HH3C-TRNG-MIB` / `-TRNG2-MIB`,
  `HH3C-LB-MIB` / `-LBV2-MIB`, `HH3C-IPSEC-MONITOR-MIB` / `-V2-MIB`.
- The `HH3C-Lsw*` modules (`LswVLAN`, `LswMAM`, `LswINF`, `LswQos`, `LswMSTP`,
  `LswRSTP`, `LswIGSP`, `LswARP`, `LswDEVM`, `LswDHCP`, `LswSMON`, `LswTRAP`,
  `LswMix`, `LSW-DEV-ADM`) are the *older* legacy switch set, still present and
  still populated on many boxes alongside the newer modules.
- Structural inheritance from H3C means many table shapes match Huawei's; see
  [huawei.md](huawei.md).

---

## ProCurve / AOS-S

**Products:** 2530/2540/2930/3810/5400R and the older 2500/4000 lines.
**Plane:** SNMP (`HP-ICF-*`) plus a JSON REST API (v1–v7 depending on 16.x
release, at `/rest/`). HPE publishes no standalone OpenAPI; the schema is
documented in PDFs and served by the switch.
**Corpus:** `spec/mib/hp/procurve/`, mixed with the Foundry set.

### The ICF set

`HP-ICF-*` (Integrated Communication Facility) is the genuine ProCurve family:

| Area | Modules |
|---|---|
| Base | `HP-ICF-BASIC` (`hpicfSensorTable[index]`, `hpicfBasicAlarmTable [AUG alarmEntry]`), `HP-ICF-OID`, `HP-ICF-TC`, `HP-BASE-MIB`, `HP-SYSTEM-MIB` |
| Hardware | `HP-ICF-CHASSIS` (`hpicfSlotTable`, `hpicfEntityTable`, `hpicfPowerSupplyTable[slotNum]`, `hpicfPsTable[bayNum]`, `hpicfFanTable[index]`), `HP-ENTITY-MIB` (a **parallel** `hpEntPhysicalTable[hpEntPhysicalIndex]`, not an augment), `HP-ICF-TRANSCEIVER-MIB` (`hpicfXcvrInfoTable[ifIndex]` + `hpicfXcvrChannelInfoTable[ifIndex, channel]`) |
| Interfaces | `HP-IF-EXT-MIB`, `HP-ICF-POE-MIB` (`hpicfPoePethPsePortTable [AUG]`, `hpicfPseFeaturesTable[entPhysicalIndex]`, `hpicfPoePowerSupplyTable[entPhysicalIndex]`), `HP-ICF-JUMBO-MIB`, `HP-ICF-LINKTEST`, `HP-ICF-UDLD-MIB` |
| L2 | `HP-ICF-BRIDGE` (`hpicfBridgeRstpPortTable`, `hpicfBridgeLoopProtectPortTable[ifIndex]`, `hpicfBridgeManagementInterfaceConfigTable[ifIndex]`), `HP-VLAN` (`hpVlanIdentTable`), `HP-ICF-PROVIDER-BRIDGE`, `CONFIG-MIB` (`hpSwitchPortTable`, `hpSwitchStpVlanTable`, `hpSwitchPortIsolationConfigTable`, `hpSwitchCosDSCPPolicyConfigTable`), `STATISTICS-MIB` (`hpSwitchVlanFdbAddrTable[fdbId, address]`, `hpSwitchPortFdbAddrTable`) |
| L3 | `HP-ICF-IP-ROUTING` (`hpicfIpStaticRouteTable[6-part key]`, `hpicfIpStaticRouteBfdTable`), `HP-ICF-IPCONFIG` (`hpicfIpAddrTable[ifIndex, addr]`, `hpicfIpNetToPhysicalTable [AUG]`, `hpicfUdpTunnelTable [AUG]`), `HP-ICF-OSPF`, `HP-ICF-RIP`, `HP-ICF-PIM`, `HP-ICF-MLD-MIB`, `HP-ICF-VRRP-MIB` (+ `hpicfVrrpTrackTable`), `HP-ICF-XRRP`, `HP-ICF-UDP-FORWARD` |
| Security | `HP-DOT1X-EXTENSIONS-MIB` (+ `hpicfDot1xSMAuthConfigTable[paePort, macAddr]`), `HP-USER-AUTH`, `HP-AUTZ-MIB` (`hpSwitchAutzUserRoleTable[roleName]`, per-command privilege tables), `HP-ICF-SECURITY`, `HP-ICF-ARP-PROTECT`, `HP-ICF-CONNECTION-RATE-FILTER` |
| Ops | `HP-ICF-DOWNLOAD` (4 tables of upgrade job state), `HP-ICF-SNMP-MIB` (trap and response source-address policy), `HP-SNTPclientConfiguration-MIB`, `HP-ICF-FAULT-FINDER-MIB`, `HP-ICF-INST-MON`, `HP-MEMPROC-MIB`, `HP-ICF-STACK`, `HP-STACK-MIB`, `HP-SwitchStack-MIB` (three stack generations) |
| QoS | `HP-ICF-RATE-LIMIT-MIB` (`hpEgressRateLimitPortQueueConfigTable[port, queue]`, `hpICMPRateLimitPortConfigTable`), `HP-CAR-MIB`, `HP-VLAN-CAR-MIB` |
| Legacy | `HP-ICF-8023-RPTR`, `HP-ICF-GENERIC-RPTR`, `HP-ICF-VG-RPTR`, `ICF-VG-RPTR`, `NETSWITCH-MIB`, `NETSWITCH-DMA-MIB`, `NETSWITCH-DRIVERS-MIB`, `SEMI-MIB`, `FAN-MIB`, `POWERSUPPLY-MIB` |
| Wireless | `HP-PROCURVE-420-PRIVATE-MIB` (`hpdot11PhyOperationTable`, `hpdot11StationConfigTable`) — a standalone AP, the IEEE MIB renamed |

**Distinctive:** ProCurve keys several hardware tables on `entPhysicalIndex`
(PoE features, PoE power supplies, stack boxes) rather than inventing its own —
good practice, and it makes `entAliasMappingTable` mandatory rather than
optional for this vendor.

### The Foundry set living in the same directory

`HP-SN-AGENT-MIB`, `-SN-SWITCH-GROUP-MIB`, `-SN-IP-MIB`, `-SN-IP-ACL-MIB`,
`-SN-IP-VRRP-MIB`, `-SN-BGP4-GROUP-MIB`, `-SN-OSPF-GROUP-MIB`, `-SN-IGMP-MIB`,
`-SN-VSRP-MIB`, `-SN-ROOT-MIB`, `-SN-TRAP-MIB`, `-SN-ROUTER-TRAP-MIB`,
`-SN-MPLS-*`, `-SN-POS-GROUP-MIB`, `-SN-APPLETALK-MIB`, `-SN-IPX-MIB`,
`-SN-SW-L4-SWITCH-GROUP-MIB`.

These are the HP 9300 line — rebadged Foundry BigIron. **174 of their table
names are identical to `FOUNDRY-SN-*`**, so a Ruckus ICX decoder covers them.
See [ruckus.md](ruckus.md#icx).

---

## BladeSystem and ProLiant (`hp/other/`)

`BLADETYPE2-*` through `BLADETYPE6-*` — the Blade Network Technologies (later
IBM/Lenovo) switch modules for HP c-Class enclosures. Their whole MIB design is
built on a **current/new configuration pair**: `ipCurCfgStaticRouteTable` /
`ipNewCfgStaticRouteTable`, `aclCurCfgTable` / `aclNewCfgTable`,
`qosCurCfgPortPriorityTable` / `qosNewCfgPortPriorityTable`,
`dot1xCurCfgPortTable` / `dot1xNewCfgPortTable`,
`vrrpCurCfgVirtRtrTable` / `vrrpNewCfgVirtRtrTable`,
`agTacacsUserMapCurCfgTable` / `...NewCfgTable`.

That is a running/candidate datastore expressed in SNMP, twenty years before
NETCONF made it normal. Worth knowing as a design precedent even though the
platform is obsolete.

`CPQ*` are Compaq/ProLiant server health MIBs (`CPQHLTH-MIB`,
`CPQSINFO-MIB`, `CPQSTDEQ-MIB`, `CPQIDA-MIB`, `CPQPOWER-MIB`, `CPQRACK-MIB`,
`CPQHOST-MIB`) — server monitoring, out of scope for network management but
present because they came with the vendor bundle.

`HP-LASERJET-COMMON-MIB` is a printer MIB and is noise.
