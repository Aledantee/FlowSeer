---
title: Entity records — telemetry and operations
date: 2026-08-30
part: 9 of 10
scope: SNMP agent, syslog and alarms, RMON, flow export, time sync, telemetry transports, scheduling, OpenFlow, host resources
---

# Telemetry and operations

Nine entities. These are the *management plane about the management plane* —
how the device talks, what it reports, and how it schedules its own work. Most
are low-value as inventory and high-value as collector configuration, with two
exceptions: [syslog-events](#syslog-events), which is FlowSeer's Event source,
and [time-sync](#time-sync), whose failure silently corrupts every timestamp in
the system.

Index: [snmp-agent](#snmp-agent) · [syslog-events](#syslog-events) ·
[rmon](#rmon) · [flow-export](#flow-export) · [time-sync](#time-sync) ·
[netconf-telemetry](#netconf-telemetry) ·
[scheduling-automation](#scheduling-automation) · [openflow-sdn](#openflow-sdn) ·
[host-resources](#host-resources)

---

## snmp-agent

**What it is.** The SNMP agent's own configuration: communities, v3 users and
views, trap destinations, and engine identity.

### Canonical models

The SNMPv3 framework MIBs (RFC 3411–3418) are unusually complete and unusually
consistently implemented:

- `SNMP-FRAMEWORK-MIB` — `snmpEngineID`, `snmpEngineBoots`, `snmpEngineTime`.
- `SNMP-USER-BASED-SM-MIB` — `usmUserTable[usmUserEngineID, usmUserName]`.
- `SNMP-VIEW-BASED-ACM-MIB` — `vacmSecurityToGroupTable`,
  `vacmAccessTable`, `vacmViewTreeFamilyTable`.
- `SNMP-TARGET-MIB` — `snmpTargetAddrTable[snmpTargetAddrName]`,
  `snmpTargetParamsTable`.
- `SNMP-NOTIFICATION-MIB` — `snmpNotifyTable[snmpNotifyName]`,
  `snmpNotifyFilterProfileTable[snmpTargetParamsName]`.
- `SNMP-COMMUNITY-MIB` — the v1/v2c community ↔ v3 security-name bridge, and
  the home of the `contextName` used for the `community@vlan` and
  `community@vrf` hacks.
- `SNMPv2-MIB::sysORTable[sysORIndex]` — **the agent's declared list of
  supported MIB modules**. Almost nothing reads it, and it is the cheapest
  available capability discovery: one walk tells you which MIBs the agent claims
  to implement.

### Vendor extras worth knowing

| Family | Surface |
|---|---|
| Cisco SMB | `CISCOSB-SNMP-MIB::rlSNMPv3IpAddrToIndexTable[addrType, addr]`, `rlInet2EngineIdTable[addressType, address]` (remote engine IDs), `rlTargetAddrExtTable [AUG snmpTargetAddrExtEntry]` |
| ProCurve | `HP-ICF-SNMP-MIB::hpicfSnmpTrapSourceAddrTable[addressType]`, `hpicfSnmpResponseSourceAddrPolicyTable` — **which source address the agent uses**, which matters when the manager filters by source IP |
| Comware | `HH3C-SNMP-EXT-MIB::hh3cSnmpExtCommunityTable[securityLevel, securityName]`, `hh3cSnmpExtContextTable[contextName]` |
| Huawei | `HUAWEI-SNMP-EXT-MIB`, `HUAWEI-SNMP-NOTIFICATION-MIB::hwSnmpTargetAddrExtTable[index]` + `hwSnmpTargetSyncIndexTable` |
| D-Link | `DLINKSW-SNMP-MIB::dSnmpTrapIfCfgTable[ifIndex]` (per-interface trap enable), `dSnmpCommunityTable[name]` |
| net-snmp based (UniFi, EdgeMAX, MikroTik) | `NET-SNMP-AGENT-MIB::nsCacheTable[nsCachedOID]`, `nsLoggingTable`, `NET-SNMP-EXTEND-MIB` (**arbitrary script output over SNMP** — a real extension point and a real security consideration) |

### Use for FlowSeer

`sysORTable` plus `SNMP-FRAMEWORK-MIB`'s engine boots/time are the two things
worth collecting: the first for capability discovery, the second because
`snmpEngineBoots` incrementing is an unambiguous reboot signal that
`sysUpTime` wrapping cannot give you.

---

## syslog-events

**What it is.** The device's own account of what happened — syslog messages,
the on-box log buffer, trap history, and structured alarms.

This is the entity behind FlowSeer's Event side of the triad, and it splits
into three genuinely different things.

### 1. Syslog transport configuration

Where messages go: `lcsSetupSyslogServerTable[idx]` +
`lcsSetupSyslogFacilityMapperTable[source]` (LANCOM),
`agentLogSyslogHostTable[index]` (FASTPATH family),
`snAgSysLogServerTable[serverIP, serverUDPPort]` (Foundry lineage),
`syslogSrvTable[ipType, ip]` (D-Link), `DLINKSW-SYSLOG-MIB`,
`HH3C-SYSLOG-MIB` / `HH3C-INFOCENTER-MIB`, `HUAWEI-SYSLOG-MIB` /
`HUAWEI-INFOCENTER-MIB`, `CISCOSB-SYSLOG-MIB`, `openconfig-system-logging`.

### 2. The on-box log buffer, readable over SNMP

The genuinely useful and widely-overlooked part — you can pull recent log lines
without a syslog receiver:

| Family | Table |
|---|---|
| Foundry lineage (Ruckus ICX, HP ProCurve) | `snAgSysLogBufferTable[index]` (dynamic) + `snAgStaticSysLogBufferTable[index]` (static/persistent) |
| HP BladeSystem | `agSyslogMsgTable[index]` |
| Comware | `HH3C-INFOCENTER-MIB::hh3cICLogbufferContTable[index]` |
| LANCOM LCOS | `lcsStatusTcpIpSyslogLastMessagesTable[idx]` |
| LANCOM SX GS2310 | `gs2310SyslogDetailedInfoTable[index]` |
| IETF | `NOTIFICATION-LOG-MIB::nlmConfigLogTable[nlmLogName]`, `nlmLogTable`, `nlmLogVariableTable` — **the standard: a log of notifications the agent *sent*, replayable**. Present in the LANCOM SX set. Solves trap loss, and almost nobody uses it |
| Cisco SMB | `CISCOSB-EVENTS-MIB` |

`NOTIFICATION-LOG-MIB` deserves emphasis: SNMP traps are UDP and lossy, and this
MIB exists precisely so a manager can poll for the traps it missed. Any
FlowSeer event pipeline that relies on traps should read it where available.

### 3. Structured alarms

A named, stateful condition with a severity and a clear — different from a log
line, which is a point event:

| Model | Shape |
|---|---|
| `ALARM-MIB` (RFC 3877) | `alarmModelTable[alarmListName, alarmModelIndex, alarmModelState]` (the *catalogue* of possible alarms) + `alarmActiveTable[alarmListName, alarmActiveDateAndTime, alarmActiveIndex]` (currently raised) + `alarmClearTable`. The catalogue/active split is the right design |
| `ITU-ALARM-TC-MIB` / `IANA-ITU-ALARM-TC-MIB` | the X.733 severity and probable-cause vocabularies |
| `openconfig-alarms` | `alarms/alarm[id]` with `state/{resource, text, time-created, severity, type-id}` |
| Huawei | `HUAWEI-ALARM-MIB`: `hwAlarmActiveTable[targetAddrExtIndex, activeAlarmIndex]`, `hwAlarmSyncTable`, `hwEventSyncTable` — **alarm resynchronisation per trap target**, so a manager that missed traps can re-read active alarms. The most complete alarm model in the corpus |
| Cisco IOS-XE | `Cisco-IOS-XE-alarm`, `-alarm-profile`, `Cisco-IOS-XE-ios-events-oper` |
| D-Link | `DLINKSW-EXTERNAL-ALARM-MIB` (dry-contact inputs, for industrial switches) |
| RMON | `rmonAlarmTable[index]` + `rmonEventTable[index]` — threshold alarms on any OID, see [rmon](#rmon) |

**Recommendation.** FlowSeer's Event concept should distinguish a *log line*
(unstructured, point-in-time) from an *alarm* (named, stateful, clearable).
`ALARM-MIB`'s model/active split and `openconfig-alarms` agree on this; syslog
does not have it. Conflating them makes "is this still broken?" unanswerable.

Trap catalogues are also worth indexing: the corpus contains **12,656
NOTIFICATION-TYPE / TRAP-TYPE definitions** across 1,691 modules. That is the
complete vocabulary of what these devices can tell you asynchronously, and it is
mechanically extractable from the MIBs — see
[matrices](../matrices/README.md).

---

## rmon

**What it is.** RFC 2819 remote monitoring: statistics, history, alarms, and
events, plus the switched extensions in SMON (RFC 2613) and the high-capacity
versions.

| Group | Table | Use |
|---|---|---|
| statistics | `etherStatsTable[etherStatsIndex]` | the frame-size histogram and error counters — see [if-counters](02-interface.md#if-counters) |
| history | `historyControlTable[index]` + `etherHistoryTable[index, sampleIndex]` | **on-box time series**, sampled at a configured interval. The device stores it; you poll it rarely |
| alarm | `alarmTable[alarmIndex]` | rising/falling thresholds on any OID |
| event | `eventTable[eventIndex]` + `logTable[eventIndex, logIndex]` | what to do when an alarm fires |
| hosts / matrix / topN | `hostTable`, `matrixSDTable`, `hostTopNTable` | RMON1 groups 4–6, rarely implemented on switches |
| `HC-RMON-MIB` | 64-bit versions | |
| `HC-ALARM-MIB` | `hcAlarmTable[hcAlarmIndex]` | 64-bit thresholds |
| `SMON-MIB` | `dataSourceCapsTable`, `portCopyTable`, `smonVlanIdStatsTable`, `smonPrioStatsTable` | switched RMON: per-VLAN and per-priority statistics, and port mirroring |
| `RMON2-MIB` | `protocolDirTable`, `nlHostTable`, `alHostTable` | protocol-aware monitoring; essentially dead |

### Why it still matters

The **history group is a free on-device time series**. A switch sampling
`etherStats` every 30 seconds for 50 buckets gives you 25 minutes of
sub-poll-interval data that survives a collector outage. Nothing in the modern
stack replaces it. Netdisco and LibreNMS both ignore it; that is an
opportunity, not a validation.

The **alarm/event group is device-side thresholding**: the switch tells you when
a counter crosses a line, instead of you polling to find out. `rmonAlarmTable`
appears in the D-Link consumer line, LANCOM SX, and the FASTPATH family, and
`HC-ALARM-MIB` in the LANCOM set.

Vendor extras: `CISCOSB-RMON` (`rlEtherStatsTable [AUG etherStatsEntry]`,
`rlHistoryControlTable`), `CISCOSB-SMON-MIB`, `HH3C-RMON-EXT-MIB` /
`-EXT2-MIB`, `HH3C-LswSMON-MIB`, `hh3cdot1qVlanStatStatusTable` (per-VLAN
statistics enable).

---

## flow-export

**What it is.** Sampled or flow-based traffic export — sFlow, NetFlow, IPFIX,
NetStream.

### Canonical models

`SFLOW-MIB` is a real cross-vendor standard and is implemented consistently:

- `sFlowRcvrTable[sFlowRcvrIndex]` — collector address, port, owner, timeout,
  max datagram size.
- `sFlowFsTable[sFlowFsDataSource, sFlowFsInstance]` — **flow sampling**, with
  `sFlowFsPacketSamplingRate`.
- `sFlowCpTable[sFlowCpDataSource, sFlowCpInstance]` — **counter polling**.

`sFlowFsDataSource` is an OID pointing at an `ifIndex` or a `dot1dBasePort` —
the same indirection RMON uses.

There is **no standard NetFlow/IPFIX MIB** in the corpus (`IPFIX-MIB` RFC 5815
exists upstream and is absent here).

| Family | sFlow | NetFlow-family |
|---|---|---|
| IETF | `SFLOW-MIB` | — |
| D-Link | `DLINKSW-SFLOW-MIB` (`dSFlowRcvrTable[index]`, `dSFlowFsTable[dataSource, instance]`, `dSFlowCpTable`) | — |
| FASTPATH family | `FASTPATH-SFLOW-MIB` + `agentSflowRemoteAgentTable[index]` | — |
| LANCOM SX | `lcssFlowRcvrTable[index]`, `lcssFlowFsTable[dataSource, instance]`, `lcssFlowCpTable` — a renamed copy | — |
| Foundry lineage | `snSflowCollectorTable[index]` | `snNetFlowCollectorTable[index]`, `snNetFlowAggregationTable[index]`, `snNetFlowIfTable[index]` |
| Comware | — | `HH3C-FLOWTEMPLATE-MIB`: `hh3cFTGroupTable[groupIndex]`, `hh3cFTBasicGroupTable`, `hh3cFTExtendGroupTable[groupIndex, offsetType]`, `hh3cFTIfApplyTable[ifIndex, groupIndex]` — a **flow template** model, i.e. which fields are exported |
| Huawei | — | `HUAWEI-NETSTREAM-MIB`, plus `HUAWEI-BULKSTAT-MIB` (`hwBulkStatDefineFileTable[index]`, `hwBulkStatDefineObjectTable[fileIndex, objectIndex]` — **bulk statistics files pushed to an FTP server**, a wholly different export mechanism worth knowing about) |
| LANCOM LCOS | — | `lcsStatusNetflowExportersTable[name]`, `lcsSetupNetflowCollectorsTable[name]`, `lcsSetupNetflowInterfacesTable[ifc, collector]`, `lcsSetupNetflowMeteringProfilesTable[name]` |
| Cisco IOS-XE | — | `Cisco-IOS-XE-flow` (Flexible NetFlow: monitors, records, exporters), `Cisco-IOS-XE-flow-monitor-oper` |

For FlowSeer this is **collector configuration, not device state**: the model
that matters is "is sFlow enabled, at what rate, pointing where" — three leaves
per interface — plus a possible future role as a flow ingestion source, which is
a different plane entirely.

---

## time-sync

**What it is.** NTP, SNTP, and PTP client/server state, plus the timezone.

**Why it is load-bearing.** Every timestamp FlowSeer records —
`ipNetToPhysicalLastUpdated`, `ifLastChange`, syslog times, alarm times, FDB
ages — is only meaningful if the device's clock is right. An unsynchronised
switch produces plausible, wrong history. This is the cheapest high-value health
check in the atlas and almost nothing collects it.

| Family | Surface |
|---|---|
| IETF | `NTPv4-MIB` (RFC 5907): `ntpEntStatPktModeTable[pktMode]`, `ntpAssociationTable[ntpAssocId]` (**peer state: stratum, offset, jitter, reach** — the actual answer), `ntpEntStatusCurrentMode`, `ntpEntStatusStratum` |
| IETF | `PTPBASE-MIB` (RFC 8173) |
| ProCurve | `HP-SNTPclientConfiguration-MIB`: `hpSntpInetConfigServerTable[priority, addressType, address]`, `hpSntpServerStatisticsTable [AUG]`, `hpSntpAuthenticationKeyTable[keyId]` |
| Comware | `HH3C-NTP-MIB::hh3cNTPPeerTable[remAdr, hMode]` |
| Huawei | `HUAWEI-NTP-MIB`, `HUAWEI-NTP-TRAP-MIB`, `HUAWEI-SYS-CLOCK-MIB`, `HUAWEI-CLOCK-MIB` (`hwClockSourceSelTable[chassisIndex, type]`, `hwClockBitsCfgTable`, `hwClockPortCfgTable[ifIndex]` — **synchronous Ethernet / BITS clocking**, a carrier feature), `HUAWEI-PTP-MIB` |
| D-Link | `DLINKSW-NTP-MIB` (`dNtpAccessGroupTable[vrfName, addrType, address, prefixLength]` — **VRF-aware NTP ACLs**, `dNtpAuthenticationKeyTable[keyId]`, `dNtpCfgBroadcastClientTable[ifIndex]`), `DLINKSW-PTP-MIB`, `DLINKSW-TIME-MIB` |
| Cisco SMB | `CISCOSB-TIMESYNCHRONIZATION-MIB`: `rlSntpNtpConfigSrvTable[entryType]`, `rlTimeZoneTable[index]`, `rlSntpConfigBroadcastTable[ifIndex]` |
| FASTPATH family | `FASTPATH-NTP-MIB` (`agentNtpServerTable[index]`, `agentNtpAuthKeyTable[index]`), `FASTPATH-SNTP-CLIENT-MIB`, `EdgeSwitch-SNTP-CLIENT-MIB` (`agentSntpClientUcastServerTable[index]`), `FASTPATH-TIMEZONE-PRIVATE-MIB` |
| Aruba CX / Ruckus ICX | `openconfig-system` `ntp` + `ntp-keys/ntp-key[key-id]` |
| Cisco IOS-XE | `Cisco-IOS-XE-native` `ptp`, `Cisco-IOS-XE-alarm` `facility/ptp` |

**What to collect:** `ntpAssociationTable`'s stratum, offset, and reach where
available; failing that, compare the device's reported current time against the
collector's. The second works everywhere and costs nothing.

---

## netconf-telemetry

**What it is.** The model-driven management transports themselves — NETCONF,
RESTCONF, gNMI/gNOI, and streaming telemetry subscriptions.

Almost nothing here is in the MIBs (the corpus matches are false positives:
`netConfigTable` in RMON2-MIB is *network* config, `tunnelInetConfigTable` is
tunnels). It lives in YANG and in the per-vendor SOURCES notes already in this
repo:

| Family | Transports | Models |
|---|---|---|
| Cisco IOS-XE | NETCONF, RESTCONF, gNMI | 887 YANG modules; `Cisco-IOS-XE-gnmi-cfg`, `cisco-xe-ietf-yang-push-deviation`/`-ext`, `ietf-netconf-*`, `tailf-netconf-*` (ConfD underneath) |
| Aruba CX | REST v10.0x (primary), gNMI | 19 OpenConfig modules + deviations |
| Ruckus ICX | RESTCONF | 111 modules: OpenConfig plus `icx-openconfig-*-aug` / `-dev` |
| HPE Comware | NETCONF over SSH + SOAP; XSD schemas, not YANG, and login-walled | — |
| Aruba AOS-S (ProCurve) | REST v1–v7, per-device schema | — |
| MikroTik | REST (community-generated OpenAPI, 3652 paths) + proprietary binary API | none |
| LANCOM | none on-device; LMC cloud REST (13 OpenAPI services) | — |
| Ubiquiti | UniFi Network API + Site Manager API (official OpenAPI) | none |
| Ruckus SmartZone | REST (688 paths, 936 definitions) | — |
| Everything else | SNMP only | — |

**The `-dev` deviation files are the most valuable artifacts in the YANG
corpus** and are routinely ignored. `icx-openconfig-vlan-dev.yang` states
exactly which OpenConfig VLAN leaves the ICX actually supports. A collector that
reads deviations knows what to ask for before asking; one that does not
discovers it through errors. This is the YANG equivalent of `sysORTable`.

---

## scheduling-automation

**What it is.** The device doing things on a timer or in response to an event.

| Model | Shape |
|---|---|
| `DISMAN-SCHEDULE-MIB` (RFC 3231) | `schedTable[schedOwner, schedName]` — cron-style scheduled SNMP sets. A real standard, present in the corpus, essentially unimplemented |
| `DISMAN-EVENT-MIB` (RFC 2981) | `mteTriggerTable`, `mteEventTable` — device-side monitoring rules, the successor to RMON alarms |
| `DISMAN-SCRIPT-MIB` | remote script execution. Historic |
| Cisco EEM | `Cisco-IOS-XE-eem` + `-eem-oper` (`eem-policy-info[index]`) — the real, widely used version of this idea |
| Reload scheduling | `HH3C-SYS-MAN-MIB::hh3cSysReloadScheduleTable[index]`, `HUAWEI-SYS-MAN-MIB::hwSysReloadScheduleTable[index]` |
| Task tables | `HUAWEI-TASK-MIB::hwTaskTable[taskIndex, taskID]` |
| Time ranges | see [acl](06-qos.md#acl) — `timeRangeTable`, `rlTBITimeRangeTable`, `swTimeRangeSettingTable`, `hh3cTrngCreateTimerangeTable`, `HUAWEI-TRNG-MIB` |
| Scheduled port shutdown | `DLINK-POWER-SAVING-MIB::dpsPortShutdownScheduleTable[ifIndex]`, `CISCOSB-TIMEBASED-PORT-SHUTDOWN-MIB::rlTimeBasedPortTable[ifIndex, timeRangeName]` |
| Cisco SMB smart ports | `CISCOSB-SMARTPORTS-MIB` — macros triggered by what connects |

The recurring reusable object is the **time range** (absolute windows plus
periodic weekday/time patterns), referenced by name from ACLs, PoE schedules,
port shutdown, and QoS. Model it once.

---

## openflow-sdn

Thin and fading. `openconfig-openflow`
(`controllers/controller[name]/connections`), D-Link `DLINKSW-OPENFLOW-MIB`
(`dOfIfTable[ifIndex]`, `dOfControllerConfigTable[index]`,
`dOfFlowConfigTable[tableId]`), Comware `HH3C-OFP-MIB`
(`hh3cOfpInstanceControllerTable[instanceID, controllerID]`,
`hh3cOfpInstanceFlowTableTable`), Huawei `HUAWEI-OPENFLOW-MIB`
(`hwOpenflowConnectionTable[ipType, remoteIp, localIp, vpnInstanceName,
datapathId, auxiliaryId]`), `CISCOSB-openflow-MIB`, Cisco IOS-XE
`config-openflow-grouping`.

Worth one line: if an OpenFlow controller owns the forwarding table, the
switch's own FDB and VLAN state may not describe what it forwards. A
`datapathId` present and a controller connected is a signal to distrust the rest
of the L2 model.

---

## host-resources

`HOST-RESOURCES-MIB` (RFC 2790): `hrSystemUptime`, `hrStorageTable[index]`
(RAM, flash, and filesystems with size/used/allocation units),
`hrDeviceTable[index]`, `hrProcessorTable[hrDeviceIndex]` (`hrProcessorLoad`),
`hrNetworkTable[hrDeviceIndex]`, `hrSWRunTable` / `hrSWRunPerfTable` (running
processes), `hrSWInstalledTable`.

Present on anything Linux-based with net-snmp: Ubiquiti UniFi and EdgeMAX,
MikroTik, and appliance-style controllers. Absent from switch firmware that
implements only the networking MIBs.

Companions: `SYSAPPL-MIB` (installed applications),
`NETWORK-SERVICES-MIB` (`assocTable[applIndex, assocIndex]` — application
associations), `UCD-SNMP-MIB` (`laTable` load average, `memory` group,
`dskTable`), `NET-SNMP-EXTEND-MIB` (arbitrary script output),
`FROGFOOT-RESOURCES-MIB` (the third-party Linux resources MIB Ubiquiti
ships with UniFi).

See also [cpu-memory](01-platform.md#cpu-memory), which covers the switch-ASIC
side of the same question and is where the more interesting signal
(forwarding-table utilisation) lives.
