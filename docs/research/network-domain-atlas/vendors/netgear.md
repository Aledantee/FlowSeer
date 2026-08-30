---
title: Vendor dossier — Netgear
date: 2026-08-30
scope: managed and smart switches
---

# Netgear

**Enterprise OIDs:** 4526 (Netgear) and **4413 (Broadcom)**.
**Corpus:** four modules. The smallest vendor set in the atlas, and the one that
needs the least work, because it is not really its own platform.

| Module | Notes |
|---|---|
| `NETGEAR-REF-MIB` | the root. Its header reads `-- Netgear Fastpath  Reference MIB`, it defines a node literally named `broadcom`, and it roots at `::= { enterprises 4413 }` |
| `NETGEAR-SWITCHING-MIB` | the managed line — the FASTPATH `agent*` switching set |
| `NETGEAR-SMART-SWITCHING-MIB` | the smart/web-managed line — a subset of the same |
| `NETGEAR-BOXSERVICES-PRIVATE-MIB` | `boxServicesFansTable[unit, index]`, `boxServicesTempSensorsTable[unit, index]` |

---

## It is FASTPATH

96 of Netgear's 101 extracted table names are shared with the FASTPATH set, and
91 with Ubiquiti EdgeSwitch. Netgear renamed the modules and left the objects
alone, exactly as Ubiquiti did — independently, from the same Broadcom
reference MIBs, both keeping enterprise 4413 for the root.

Representative tables, all identical to their EdgeSwitch counterparts:

```
agentSwitchportConfigTable[intfIndex]
agentSwitchPortDVlanTagTable[ifIndex, TPid]
agentStpMstPortTable[mstId, ifIndex]
agentPvrstpVlanTable[vlanIndex]
agentPvrstpPortVlanTable[portIndex, vlanIndex]
agentSwitchMFDBTable[vlanId, mac, protocolType]
agentSwitchMFDBSummaryTable[vlanId, mac]
agentSwitchSnoopingVlanTable[vlanIndex, protocol]
agentSwitchVlanStaticMrouterTable[ifIndex, vlanIndex, protocol]
agentSwitchSnoopingQuerierVlanTable[vlanIndex, protocol]
agentSwitchSnoopSSMFDBTable[groupAddrType, group, source, vlanIndex]
agentDot3adAggPortTable[aggPort]
agentSwitchVlanMacAssociationTable[mac, vlanId]
agentSwitchVlanSubnetAssociationTable[ip, mask, vlanId]
agentDynamicAuthorizationClientTable[address]
agentPrivateVlanIntfAssocTable[ifIndex]
agentSnmpCommunityConfigTable[index]
agentUserAuthenticationConfigTable [AUGMENTS agentUserConfigEntry]
agentDhcpFilteringPortConfigTable[ifIndex]
agentDhcpSnoopingVlanConfigTable[vlanIndex]
agentDhcpSnoopingIfConfigTable[ifIndex]
agentDhcpSnoopingStatsTable[ifIndex]
boxServicesFansTable[unit, index]
boxServicesTempSensorsTable[unit, index]
```

---

## What this means practically

**Netgear needs no dedicated decoder.** A FASTPATH decoder written against
`EdgeSwitch-*` or the raw `FASTPATH-*` modules works, provided the module
resolution is by *object OID* rather than by module name. Since all three
vendors kept the objects at the same relative positions under their own
enterprise arcs, the safe implementation is to key on object *name* within a
resolved MIB tree, or to carry all three module sets and let the OID lookup
sort it out.

**The corpus is thin relative to the product.** Four modules cannot cover
everything a Netgear M4300 does. The real FASTPATH feature set is the ~40
modules Ubiquiti ships; Netgear collapsed them into two. If a Netgear device
answers an OID that is not in `NETGEAR-SWITCHING-MIB`, look for it in
`EdgeSwitch-*` or `FASTPATH-*` — it is very likely the same object.

**Netgear Insight** (the cloud platform) has no published API spec and is not
represented here. For Insight-managed switches, SNMP on the device remains the
only machine interface documented in this corpus.

---

## Traps

- Two switching MIBs for two product tiers; the smart-switch line implements a
  strict subset and will return `noSuchObject` for managed-line OIDs.
- The `broadcom` root node name inside `NETGEAR-REF-MIB` is not a mistake and
  should not be "corrected" during any MIB normalisation.
- Nothing here reaches ENTITY-MIB; hardware inventory is the FASTPATH
  unit/slot/card model, which has no `entPhysicalIndex` to join optics or PoE
  to. See [hw-component](../entities/01-platform.md#hw-component).
