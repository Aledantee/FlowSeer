---
title: Vendor dossier — Ruckus (CommScope)
date: 2026-08-30
scope: ICX switches, SmartZone/vSZ, ZoneDirector, Unleashed, and the SCI/SCG telemetry protos
---

# Ruckus

Two acquisitions welded together: **ICX** switching (Brocade → Foundry) and
**Ruckus** wireless. They share a brand, an enterprise-OID arc (25053), and
nothing else. Foundry's own arc (1991) is still in use for the ICX MIBs.

Ruckus is also the only vendor in this corpus that ships **all four kinds of
machine-readable spec**: SNMP MIBs, YANG models, an OpenAPI document, and
protobuf telemetry schemas. That makes it the best available case study for how
the four planes relate.

---

## ICX

**Products:** ICX 7150/7250/7450/7550/7650/7850 and newer.
**Lineage:** Brocade FastIron, which is Foundry. **174 shared table names with
HP's `HP-SN-*` set** — HP resold Foundry hardware as the HP 9300 line. One
decoder serves both.
**Planes:** SNMP (30 `FOUNDRY-*` modules, the broadest surface — stacking, PoE,
environment, CAM utilisation, and the only trap source) and RESTCONF from
FastIron 09.0.00 (111 YANG modules).
**Corpus:** `spec/mib/ruckus/icx/`, `spec/yang/ruckus/icx/9.0.00/`.

### The SNMP set

| Module | Covers |
|---|---|
| `FOUNDRY-SN-ROOT-MIB` | the sysObjectID registry — you need it to identify an ICX model |
| `FOUNDRY-SN-AGENT-MIB` | chassis, serial, boot, CPU, fans (`snChasFanTable[index]` and `snChasFan2Table[unit, index]`), syslog buffer (`snAgSysLogBufferTable`, `snAgStaticSysLogBufferTable`), trap receivers |
| `FOUNDRY-SN-SWITCH-GROUP-MIB` | the L2 workhorse: FDB (`snFdbTable[stationIndex]`), mirroring, NetFlow/sFlow collectors, QoS profiles, VLAN-by-IP-subnet, DHCP gateway lists |
| `FOUNDRY-SN-STACKING-MIB` | `snStackingConfigUnitTable[index]`, `snStackingOperUnitTable[index]`, `snStackingConfigStackTrunkTable[unit, port1, port2]` |
| `FOUNDRY-POE-MIB` | `snAgentPoePortTable[portNumber]`, `snAgentPoeModuleTable`, `snAgentPoeUnitTable` — own port numbering, not RFC 3621 |
| `FOUNDRY-LAG-MIB` | **string-keyed LAGs**: `fdryLinkAggregationGroupTable[groupName]`, `fdryLinkAggregationGroupPortTable[groupName, ifIndex]`, `fdryLinkAggregationGroupLacpPortTable[groupName, ifIndex]` |
| `FOUNDRY-SN-CAM-MIB` | **CAM/TCAM utilisation** by slot and type (`snCamUsageL2Table`, `snCamUsageL3Table`, `snCamUsageSessionTable`, `snCamUsageOtherTable`) — forwarding-table headroom, one of only three vendors here that reports it |
| `FOUNDRY-SN-IP-MIB` | addressing, static routes (`snRtIpStaticRouteTable[dest, mask]`), loopback interfaces, port access lists |
| `FOUNDRY-SN-IP-ACL-MIB` | `snAgAclTable[aclIndex]`, `snAgAclBindToPortTable[portNum, direction]`, `snAgAclIfBindTable[ifBindIndex, direction]`, `agAclAccntTable[...]` (per-rule hit counters) |
| `FOUNDRY-SN-IP-VRRP-MIB` | `snVrrpVirRtrTable[port, vrId]` and `snVrrpVirRtr2Table[ifIndex, vrId]` |
| `FOUNDRY-SN-MAC-AUTHENTICATION-MIB` | `snMacAuthTable[ifIndex, vlanId, mac]` — MAB |
| `FOUNDRY-MAC-VLAN-MIB` / `-SN-MAC-VLAN-MIB` | MAC-based VLAN assignment |
| `FOUNDRY-CAR-MIB` / `-VLAN-CAR-MIB` | committed access rate (the Foundry name for storm control / policing) |
| `FOUNDRY-SN-MRP-MIB` | **Metro Ring Protocol** — *not* IEEE MRP. Name collision |
| `FOUNDRY-SN-VSRP-MIB` | VSRP, Foundry's combined L2+L3 redundancy protocol |
| `FOUNDRY-BFD-STD-MIB`, `-SN-BGP4-GROUP-MIB`, `-SN-OSPF-GROUP-MIB`, `-SN-IGMP-MIB`, `-SN-ARP-GROUP-MIB` | routing |
| `FOUNDRY-SN-TRAP-MIB`, `-SN-ROUTER-TRAP-MIB`, `-SN-NOTIFICATION-MIB` | the trap catalogue |
| `FOUNDRY-SN-SW-L4-SWITCH-GROUP-MIB` | server load balancing (ServerIron heritage) |
| `FOUNDRY-SN-POS-GROUP-MIB`, `-SN-APPLETALK-MIB`, `-SN-IPX-MIB`, `-SN-WIRELESS-GROUP-MIB` | legacy |

**The `2`-table rule.** Foundry adds a parallel `…2Table` whenever it outgrows a
key: `snChasFan2Table[unit, index]`, `snVrrpVirRtr2Table[ifIndex, vrId]`,
`snVrrpIf2Table[ifIndex]`, `snPOSInfo2Table[ifIndex]`. Always prefer the `2`
table and fall back.

### The RESTCONF set

111 modules in `spec/yang/ruckus/icx/9.0.00/`: upstream OpenConfig
(interfaces, vlan, lldp, spanning-tree, system, acl, aaa, ospfv2, bgp, isis,
mpls, network-instance, local-routing, poe, platform, alarms, probes,
keychain, packet-match, policy-forwarding, aft, rib-bgp) plus **17 ICX
augmentation and deviation modules**:

`icx-openconfig-{aaa,acl,if-aggregate,if-ethernet,if-ip,if-poe,if-tunnel,
local-routing,network-instance,ospfv2,spanning-tree,system,vlan}-dev` and
`icx-openconfig-{aaa,if-ethernet,if-poe}-aug`, plus `icx-generic-dev`.

**The `-dev` files are the documentation.** They state exactly which OpenConfig
leaves the platform supports; the `-aug` files add ICX-specific ones. Reading
them before writing a collector is the difference between a working RESTCONF
client and a pile of 404s.

FastIron 10.x ships updated model sets, but only 9.0.00 is public; 10.x sits
behind the Ruckus support portal.

---

## SmartZone, ZoneDirector, Unleashed

Three wireless architectures, three MIB families, one REST API.

| Architecture | MIBs | Notes |
|---|---|---|
| **SmartZone / vSZ** (controller cluster) | `RUCKUS-SZ-SYSTEM-MIB`, `-SZ-WLAN-MIB`, `-SZ-CONFIG-WLAN-MIB`, `-SZ-EVENT-MIB`, `RUCKUS-CTRL-MIB` | `RUCKUS-CTRL-MIB` carries the scope hierarchy: `ruckusCTRLDomainTable[domainId]` → `ruckusCTRLZoneTable[domainId, zoneId]` → `ruckusCTRLSummaryApTable[indexType, indexUUID, apMac]`, plus `ruckusCTRLApRadioTable[apMac, radioIndex]`, `ruckusCTRLClientTable[clientMac]`, `ruckusCTRLApClientTable[apMac, clientMac]`, `ruckusCTRLApWiredClientTable[apMac, clientMac]`, and `ruckusCTRLSystemNodeTable[serialNumber]` for cluster nodes |
| **ZoneDirector** (appliance controller) | `RUCKUS-ZD-SYSTEM-MIB`, `-ZD-AP-MIB`, `-ZD-WLAN-MIB`, `-ZD-WLAN-CONFIG-MIB`, `-ZD-AAA-MIB`, `-ZD-EVENT-MIB` | `ruckusZDAPConfigTable[configID]`, `ruckusZDWLANAPTable[apMacAddr]`, `ruckusZDWLANAPRadioStatsTable[apMacAddr, radioIndex]`, `ruckusZDWLANRogueTable[rogueIndex]` |
| **Unleashed** (controller-less cluster) | `RUCKUS-UNLEASHED-SYSTEM-MIB`, `-UNLEASHED-WLAN-MIB`, `-UNLEASHED-EVENT-MIB` | `ruckusUnleashedWLANAPEthStatusTable[apMac, ethPortId]` |
| Common | `RUCKUS-ROOT-MIB`, `-TC-MIB`, `-PRODUCTS-MIB` (sysObjectID registry), `-SYSTEM-MIB`, `-DEVICE-MIB`, `-HWINFO-MIB`, `-SWINFO-MIB` | |

### The vSZ REST API

`spec/openapi/ruckus/vsz/vsz-7.1.1-v13_1-openapi.json` — Swagger 2.0,
**688 paths, 1,068 operations, 936 definitions**, base path
`/wsg/api/public/v13_1`. Path distribution tells you where the model's weight
is:

```
232  /rkszones/...     zone-scoped configuration (WLANs, AP groups, policies)
 86  /aps/...          AP inventory and operations
 41  /profiles/...     reusable profiles
 36  /query/...        the search/report interface
 24  /services/...
 20  /system/...
 19  /avc/...          application visibility
 18  /identity/...
  ...  clients, rogue, alert, cluster, domains, planes, maps, upgrade, sci
```

This is the richest and easiest wireless surface in the corpus, and
`spec/openapi/ruckus/vsz/SOURCES.md` already records that Ruckus publishes no
static download — the vendored copy is a community capture of a live
controller's `/wsg/apiDoc/openapi`.

**The `domains → rkszones → aps` hierarchy maps almost one-to-one onto
FlowSeer's Integration → Integration Scope → Device model.** It is the best
available validation of that design.

---

## The protobuf telemetry schemas

`spec/proto/ruckus/` is unique in this corpus: a **push-based, protobuf-typed**
telemetry surface, which is the same wire technology FlowSeer's own model uses.

| Directory | Files | What it is |
|---|---|---|
| `ap/` | `ap_report.proto`, `ap_status.proto`, `ap_common.proto`, `ap_client.proto`, `ap_wired_client.proto`, `ap_rogue.proto`, `ap_mesh.proto`, `ap_peerlist.proto`, `ap_avc.proto`, `ap_avc_all.proto`, `ap_hccd_report.proto` | what an AP streams to its controller |
| `scg/` | `session_manager.proto`, `ScgSessMgrPubIpc.proto`, `commons.proto`, `simple-storage.proto` | SmartCell Gateway internal session management |
| `sci/` | `sci-message.proto`, `sci-event.proto`, `sci-alarm.proto`, `sci-configuration.proto`, `sci-rogue.proto`, `sci-pci.proto` | SmartCell Insight — the analytics feed out of a controller |
| `icx/` | `switches.proto`, `switch_all.proto` | ICX switch state as streamed to SmartZone |
| `nanopb/` | `nanopb.proto` | the embedded-protobuf options the AP schemas use |

Two things worth taking from these:

1. **`nanopb` options** mark them as generated for a memory-constrained
   embedded target — max field sizes, no dynamic allocation. It is a working
   example of protobuf on the device side of a network-management system.
2. **`sci-alarm.proto` versus `sci-event.proto`** — Ruckus made the same
   alarm/event distinction that [syslog-events](../entities/09-ops.md#syslog-events)
   argues FlowSeer needs, independently, in protobuf.

`spec/proto/ruckus/README.md` and the repo's proto style notes already record
that these are pinned to edition 2024 as vendored references.

---

## Traps

- ICX and wireless share nothing. Establish which you are talking to first.
- `FOUNDRY-SN-MRP-MIB` is Metro Ring Protocol; `IEEE8021-MIRP-MIB` is Multiple
  Registration. Do not match on `MRP`.
- SmartZone can manage ICX switches as well as APs (`/switches` in the REST
  API), so an ICX may be reachable through two integrations at once — exactly
  the case FlowSeer's Binding concept exists for.
- The ZoneDirector and Unleashed MIB families overlap in object naming but not
  in OID; `RUCKUS-PRODUCTS-MIB` is the discriminator.
