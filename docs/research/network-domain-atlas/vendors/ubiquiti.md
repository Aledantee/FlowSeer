---
title: Vendor dossier — Ubiquiti
date: 2026-08-30
scope: EdgeSwitch, UniFi, airMAX/airFiber/UFiber
---

# Ubiquiti

Three product lines with three completely different management stories, and
only one of them is really SNMP.

**Enterprise OIDs:** 41112 (Ubiquiti) and **4413 (Broadcom)** — the second one
is the tell.

---

## EdgeSwitch — Broadcom FASTPATH in disguise

**Corpus:** 40 modules in `spec/mib/ubiquiti/edgemax/`.

`EdgeSwitch-REF-MIB` says it outright:

> `-- Ubiquiti Fastpath  Reference MIB`
> `-- Copyright Broadcom Inc (2001-2007) All rights reserved.`
> `-- This MIBs were modified from Broadcom Reference MIBs to match UBNT MIB names.`
> `broadcom MODULE-IDENTITY … ::= { enterprises 4413 }`

Every `EdgeSwitch-*` module is a renamed Broadcom FASTPATH MIB. The object names
were left alone (`agent*`, `boxServices*`, `acl*`, `cp*`), so **206 of the 317
FASTPATH table names appear verbatim**. Netgear did the same thing
independently — see [netgear.md](netgear.md) — and LANCOM SX ships the
originals unrenamed.

**Consequence:** one FASTPATH decoder covers EdgeSwitch, Netgear, and a large
part of LANCOM SX. Nothing else in the corpus pays back as well.

### What the FASTPATH set covers

| Area | Modules |
|---|---|
| Switching core | `EdgeSwitch-SWITCHING-MIB` — the big one: `agentSwitchportConfigTable[intfIndex]`, `agentSwitchPortDVlanTagTable[ifIndex, TPid]`, `agentStpMstPortTable[mstId, ifIndex]`, `agentPvrstpVlanTable`, `agentSwitchMFDBTable[vlanId, mac, protocolType]`, `agentSwitchSnoopingVlanTable[vlanIndex, protocol]`, `agentDot3adAggPortTable`, `agentSwitchVlanMacAssociationTable[mac, vlanId]`, `agentSwitchVlanSubnetAssociationTable`, `agentSnmpCommunityConfigTable`, `agentUserAuthenticationConfigTable [AUG]` |
| Inventory | `EdgeSwitch-INVENTORY-MIB`: `agentInventoryUnitTable[unitNumber]`, `agentInventorySlotTable[unit, slot]`, `agentInventoryCardTypeTable[cardIndex]`, `agentInventorySupportedUnitTable`, `agentInventoryStackPortTable` — **a unit/slot/card model that ignores ENTITY-MIB entirely** |
| Environment | `EdgeSwitch-BOXSERVICES-PRIVATE-MIB`: `boxServicesFansTable[unit, index]`, `boxServicesTempSensorsTable[unit, index]` |
| PoE | `EdgeSwitch-POWER-ETHERNET-MIB`: `agentPethPsePortTable [AUG pethPsePortEntry]`, `agentPethMainPseTable [AUG]` |
| QoS / ACL | `-QOS-ACL-MIB` (`aclTable[aclIndex]`, `aclRuleTable[aclIndex, ruleIndex]`, `aclIfTable[ifIndex, direction, sequence, aclType, aclId]`, `aclVlanTable`, `aclMacTable`), `-QOS-COS-MIB` (`agentCosMapIntfTrustTable[ifIndex]` — the trust boundary), `-QOS-DIFFSERV-PRIVATE-MIB`, `-QOS-AUTOVOIP-MIB`, `-QOS-ISCSI-MIB`, `-QOS-MIB` |
| L3 | `-ROUTING-MIB` (`agentStaticRoutesTable[...]`, `agentRouterVrrpOperTable[ifIndex, vrId]`, `agentRouterVrrpTrackIntfTable`, `agentRouterVrrpTrackRouteTable`), `-ROUTING6-MIB` (`agentIpv6AddrTable[ifIndex, address]`, `agentIpv6StaticRouteTable`), `-LOOPBACK-MIB`, `-IPV6-LOOPBACK-MIB`, `-IPV6-TUNNEL-MIB` |
| DHCP / DNS | `-DHCPCLIENT-PRIVATE-MIB` (`agentdhcp4ClientLeaseParametersTable[ifIndex]`), `-DHCPSERVER-PRIVATE-MIB` (`agentDhcpServerPoolConfigTable[poolIndex]`, `agentDhcpServerPoolAllocationTable [AUG]`, `agentDhcpServerExcludedAddressRangeTable`), `-DNS-RESOLVER-CONTROL-MIB` |
| Security | `-DOT1X-ADVANCED-FEATURES-MIB` (incl. `agentDot1xPortAuthHistoryResultTable[ifIndex, index]` — **an authentication history log**), `-DOT1X-AUTHENTICATION-SERVER-MIB` (the switch as a local auth server), `-RADIUS-AUTH-CLIENT-MIB`, `-PORTSECURITY-PRIVATE-MIB` (`agentPortSecurityDynamicTable[ifIndex, vlanId, mac]`), `-DENIALOFSERVICE-PRIVATE-MIB`, `-LLPF-PRIVATE-MIB` (link-local protocol filtering, `agentSwitchLlpfPortConfigTable[ifIndex, protocolType]`), `-MGMT-SECURITY-MIB`, `-KEYING-PRIVATE-MIB` (**feature licences, not crypto keys**) |
| Captive portal | `-CAPTIVE-PORTAL-MIB`: `cpCaptivePortalTable[instanceId]`, `cpLocalUserTable[userIndex]`, `cpLocalUserGroupTable`, `cpLocalUserGroupAssociationTable[userIndex, groupIndex]`, `cpInterfaceAssociationTable[cpId, ifIndex]`, `cpCaptivePortalIntfClientAssocTable[ifIndex, macAddress]` — a complete guest system on a switch |
| Discovery | `-ISDP-MIB` — Industry Standard Discovery Protocol, FASTPATH's CDP clone |
| Ops | `-LOGGING-MIB`, `-SFLOW-MIB`, `-SNTP-CLIENT-MIB`, `-TIMEZONE-PRIVATE-MIB`, `-TIMERANGE-MIB`, `-UDLD-MIB`, `-MULTICAST-MIB`, `-MVR-PRIVATE-MIB`, `-NSF-MIB`, `-OUTBOUNDTELNET-PRIVATE-MIB` |
| Ubiquiti's own | `UBNT-EdgeMAX-MIB` — `ubntPsuTable[index]`, `ubntFansTable[index]`; the small amount Ubiquiti actually added |

EdgeRouter (EdgeOS, Vyatta-derived) has no MIB set here beyond
`UBNT-EdgeMAX-MIB`, and no public API. Its web UI uses undocumented internal
endpoints.

---

## UniFi

**Corpus:** three MIBs (`UBNT-MIB`, `UBNT-UniFi-MIB`, and the third-party
`FROGFOOT-RESOURCES-MIB`) plus **two official OpenAPI documents** in
`spec/openapi/ubiquiti/`.

The SNMP surface is trivial — `unifiRadioTable[unifiRadioIndex]`, a handful of
scalars, and Linux resources through Frogfoot and HOST-RESOURCES. **The API is
the real interface.**

### UniFi Network API (local controller, 44 paths)

`spec/openapi/ubiquiti/unifi-network-openapi-v10.4.57.json`, served at
`/proxy/network/integration/`, `X-API-Key` auth. Shape:

```
/v1/sites                                          the scope
/v1/sites/{siteId}/devices                         AP/switch/gateway inventory
/v1/sites/{siteId}/devices/{deviceId}/statistics/latest
/v1/sites/{siteId}/devices/{deviceId}/interfaces/ports/{portIdx}/actions
/v1/sites/{siteId}/clients                         connected clients
/v1/sites/{siteId}/clients/{clientId}/actions
/v1/sites/{siteId}/acl-rules       + /ordering
/v1/sites/{siteId}/firewall/policies + /zones + /ordering
/v1/sites/{siteId}/dns/policies
/v1/sites/{siteId}/device-tags
/v1/pending-devices                                adoption queue
/v1/dpi/applications, /v1/dpi/categories
/v1/countries, /v1/info
```

Two design notes worth carrying:

- **`/ordering` as a separate endpoint** for ACL and firewall rule sequence.
  Order is a first-class, separately-mutable property of a rule set — which is
  exactly the point [acl](../entities/06-qos.md#acl) makes about modelling a
  rule set as name + type + *ordered* rules.
- **`/pending-devices`** — an adoption queue. A device that exists but is not
  yet managed is a real state, and FlowSeer's Device lifecycle has no obvious
  equivalent.

### UniFi Site Manager API (cloud)

`unifi-site-manager-openapi-v1.0.0.json` at `https://api.ui.com` —
cross-site inventory and ISP metrics. The multi-site rollup, which maps onto
FlowSeer's Integration Scope.

`spec/openapi/ubiquiti/SOURCES.md` already documents the refresh path
(`opastorello/unifi-api-docs` catalog.json).

---

## airMAX / airFiber / UFiber

Point-to-point and point-to-multipoint radios, and a GPON OLT.

| MIB | Covers |
|---|---|
| `UBNT-AirMAX-MIB` | `ubntRadioTable[index]`, `ubntRadioRssiTable[index, rssiIndex]` |
| `UBNT-AirFIBER-MIB` | `airFiberConfig[index]`, `airFiberStatus[index]`, `airFiberStatistics[index]` |
| `UBNT-AFLTU-MIB` | airFiber LTU: `afLTUStationTable[remoteMac]` |
| `UI-AF60-MIB` | airFiber 60 GHz: `af60StationTable[staMac]` |
| `UBNT-UFIBER-MIB` | GPON OLT: `ubntSfpsTable[sfpIndex]`, `ubntPsuTable`, `ubntFansTable`, `ubntThermsTable`, `ubntPowerOutTable` |

"Station" here means a subscriber radio on a PtMP link, not an 802.11 client.
Different entity, same word — see
[wireless-client](../entities/08-wireless.md#wireless-client).

---

## Traps

- **Three lines, three stacks.** EdgeSwitch is Broadcom, UniFi is Linux + API,
  airMAX is its own embedded firmware. `sysObjectID` under 41112 vs 4413 is the
  first discriminator.
- `EdgeSwitch-KEYING-PRIVATE-MIB` is feature licensing. The name says crypto.
- UniFi devices answer SNMP but almost nothing useful comes back; going to SNMP
  first for UniFi is a wasted poll cycle.
- `FROGFOOT-RESOURCES-MIB` is a third-party MIB (Frogfoot Networks) that
  Ubiquiti shipped for Linux resource reporting — it is filed under `ietf/` in
  this corpus as well as `ubiquiti/unifi/`, which is confusing but harmless.
