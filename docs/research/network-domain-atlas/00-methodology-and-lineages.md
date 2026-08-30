---
title: Methodology, corpus census, and vendor lineages
date: 2026-08-30
part: 0 of 10
scope: how this atlas was built, what it is built from, and the firmware lineages that collapse 20 vendors into 8 decoder families
---

# Methodology, corpus census, and lineages

## What this is built from

Everything in the atlas is derived from two sources: the machine-readable
specifications already vendored in `spec/`, and targeted reading of the
standards and prior-art systems those specifications descend from. Nothing was
taken on trust from a vendor datasheet.

### Corpus census (2026-08-30)

| Source | Files | Modules parsed | Extracted |
|---|---|---|---|
| `spec/mib/` | 1,712 | **1,691 MIB modules** | 28,454 conceptual tables with their INDEX/AUGMENTS clauses; 367,844 OBJECT-TYPEs; 12,656 NOTIFICATION/TRAP definitions |
| `spec/yang/` | 1,295 | 1,163 modules + 132 submodules | 26,490 data-node paths (depth ≤ 4) with list keys |
| `spec/openapi/` | 19 | — | Ruckus vSZ 688 paths / 936 definitions; MikroTik RouterOS 3,652 paths; UniFi Network + Site Manager; 13 LANCOM LMC services |
| `spec/proto/` | 130 | — | FlowSeer's own schemas plus the vendored Ruckus AP/SCG/SCI telemetry protos |

MIB modules by issuer:

```
 370  hp (271 hh3c + 82 procurve + 17 other)
 261  lancom (124 sx-5.30 + 114 sx-5.20 + 16 lcos + 4 sx + 3 lx)
 240  huawei
 212  ieee
 186  ietf
 143  dlink
 116  cisco (all CISCOSB — see the gap below)
  60  aruba (35 cx + 25 wireless)
  51  ruckus (30 icx + 21 wireless)
  47  ubiquiti (40 edgemax + 5 airmax + 3 unifi)
   4  netgear
   1  mikrotik
```

YANG modules by issuer: 1,071 Cisco IOS-XE 26.1.1 (887 in the main set, 184
MIB-derived), 111 Ruckus ICX FastIron 9.0.00, 90 OpenConfig, 19 Aruba AOS-CX
10.17, 4 IETF. The union of OpenConfig modules present anywhere in the corpus is
**144**.

### How entities were derived

Not from a list someone wrote down. The taxonomy was built by:

1. Extracting every SNMP conceptual table (a `SEQUENCE OF` object plus the
   matching entry's `INDEX` or `AUGMENTS` clause) and every YANG
   container/list/augment path.
2. Clustering those 55,000 anchors against ~100 candidate domain patterns.
3. Reading the resulting evidence per cluster and splitting, merging, or
   discarding until each cluster was one thing a network operator would name.

That produced **101 entities in 10 domains**. The scripts are in
[`_raw/`](_raw/) and the per-domain evidence dumps they generated are
alongside them, so any claim in the atlas can be traced back to the tables it
came from.

### Precision caveat

The classifier is regex-based and over-matches. Known false positives, kept in
the record rather than silently fixed, because the next reader will hit them
too:

- `fec` matches MPLS *Forwarding Equivalence Class* in Cisco YANG.
- `lag`/`aggregate` matches BGP `aggregate-address` and OSPF `areaAggregate`.
- `loopback` matches 802.3ah OAM loopback tests and DNS loopback address lists.
- `cellular` matches 802.11u cellular-network-information (Hotspot 2.0).
- `channel` matches DOCSIS channels, optics lanes, and QoS scheduler channels.
- `keying` in `FASTPATH-KEYING-PRIVATE-MIB` is feature *licensing*, not crypto.
- `MRP` is IEEE Multiple Registration Protocol in the IEEE MIBs and Foundry
  **Metro Ring Protocol** in `FOUNDRY-SN-MRP-MIB`.

Each entity record states which of its matches it discounted.

---

## The lineages

The single most useful finding in the corpus: **twelve vendor directories
collapse into eight decoder families**, because several vendors ship the same
firmware stack under different names. This was verified by comparing extracted
table-name sets, not by reading marketing history.

### Broadcom FASTPATH / ICOS

| Pair | Shared table names |
|---|---|
| FASTPATH ∩ EdgeSwitch | **206** (of 317 / 227) |
| FASTPATH ∩ Netgear | 96 (of 317 / 101) |
| EdgeSwitch ∩ Netgear | 91 (of 227 / 101) |

Ubiquiti EdgeSwitch, Netgear managed switches, and LANCOM SX all run Broadcom's
FASTPATH (later ICOS) switch stack. Ubiquiti and Netgear renamed the MIB modules
(`EdgeSwitch-*`, `NETGEAR-*`) while keeping the object names (`agent*`,
`boxServices*`, `acl*`). **LANCOM did not rename them at all** — the LANCOM SX
directories contain `fastpathswitching.mib`, `fastpath_boxservices.mib`,
`fastpath_mab.mib` and so on, defining modules literally named
`FASTPATH-SWITCHING-MIB`.

LANCOM additionally ships its own `LCOS-SX-MIB` and `LCOS-SX-GENERAL-MIB` **on
the same devices**, so a LANCOM SX switch answers for both
`boxServicesFansTable` and `lcsMonitoringFansTable`. They can disagree, and the
collector has to choose.

Two LANCOM SX firmware trees are vendored (`sx-5.20-gs4530x`, `sx-5.30-ys7154cf`)
plus per-model MIBs (`LC-GS-2310-*`, `LC-GS-3628XP-*`). The GS2310 line has an
entirely separate `LANCOM-GS2310-FUNCTION-MIB` with its own `gs2310*` tables
covering LACP, VLAN, STP, PoE, DHCP snooping, IP source guard and QoS — a
different agent again on the same vendor's product line.

**Consequence:** one FASTPATH decoder covers three vendors and a large fraction
of a fourth. No other decoder in the corpus reaches as many devices.

### Foundry / Brocade

| Pair | Shared table names |
|---|---|
| FOUNDRY-* ∩ HP-SN-* | **174** (of 262 / 195) |

Ruckus ICX is Brocade FastIron, which is Foundry. HP resold Foundry hardware as
the HP 9300/ProCurve routing switch line and shipped the same MIBs with the
prefix changed from `FOUNDRY-SN-` to `HP-SN-`. Object names are identical
(`snAgAclTable`, `snAgSysLogBufferTable`, `snVrrpVirRtrTable`, …).

So `spec/mib/hp/procurve/` contains **two unrelated MIB families**: the real
ProCurve `HP-ICF-*` set (the AOS-S switches) and the Foundry-derived `HP-SN-*`
set. Anyone writing a "ProCurve decoder" against the wrong half will get
nothing.

Foundry's habit of adding a `2`-suffixed table when it outgrows a key recurs:
`snChasFanTable` → `snChasFan2Table[unit, index]`,
`snVrrpVirRtrTable[port, vrId]` → `snVrrpVirRtr2Table[ifIndex, vrId]`,
`snPOSInfoTable[portNum]` → `snPOSInfo2Table[ifIndex]`, `snVrrpIfTable` →
`snVrrpIf2Table[ifIndex]`. Prefer the `2` table, fall back to the original.

### H3C ancestry: Comware and Huawei

121 table names are shared between `HUAWEI-*` and `HH3C-*` once the vendor
prefix is stripped — `aclBasicRuleTable`, `aclAdvancedRuleTable`,
`cbqosClassifierCfgInfoTable`, `mstpInstanceTable`, `mstpVIDAllocationTable`,
`flashTable`, `flhChipTable`, and the whole CBQoS family.

Huawei and 3Com's joint venture H3C split in 2003; H3C became HP's Comware and
Huawei kept its own VRP. Twenty years of divergence later, the *structural*
inheritance is still visible in the MIB shapes even though the object names
differ. A decoder written for one is a good starting point for the other and
never a drop-in.

Scale note: Huawei extracts to 2,554 distinct table names and Comware to 1,480.
These are by far the largest vendor surfaces in the corpus, and most of that
volume is carrier features ([bras](entities/10-wan-access.md#bras),
[mpls](entities/05-routing.md#mpls), [pon](entities/10-wan-access.md#pon)) that
FlowSeer's device classes will never touch.

### HPE's two switch platforms

`ARUBAWIRED-*` (AOS-CX) and `HP-ICF-*` (AOS-S / ProCurve) share **zero** table
names. AOS-CX is a clean-sheet platform, not an evolution of ProCurve, and its
MIB set is deliberately thin because the real interface is the REST API and
OpenConfig-over-gNMI.

The exception that proves the inheritance is at the *design* level, not the
name level: `ARUBAWIRED-PROVIDER-BRIDGE-MIB` and `HP-ICF-PROVIDER-BRIDGE` each
define exactly two tables with matching roles
(`…ProviderBridgeVlanTypeTable`, `…ProviderBridgePortTable`). Same people, same
idea, new prefix.

### Cisco's two product lines

`spec/mib/cisco/` contains **only** `CISCOSB-*` — the small-business
(Sx/CBS/Catalyst 1200-1300) line, which is a Marvell/Radlan stack, not IOS. Its
object prefix is `rl` (Radlan) and it shares nothing with IOS or IOS-XE.

Cisco enterprise (`CISCO-*`, `AIRESPACE-*`, `LWAPP-*`) is **absent** — see
[gaps](04-gaps-and-recommendations.md).

### The single-file vendors

MikroTik ships one MIB (`MIKROTIK-MIB`, ~6 tables). Netgear ships four modules,
all FASTPATH. Ubiquiti UniFi ships three (`UBNT-MIB`, `UBNT-UniFi-MIB`, and the
third-party `FROGFOOT-RESOURCES-MIB`). For all three, **SNMP is the fallback
and the REST API is the real surface** — and for MikroTik and UniFi those APIs
are already vendored in `spec/openapi/`.

### Summary: the eight decoder families

| Family | Covers | Primary surface |
|---|---|---|
| **Standard** (IETF + IEEE) | baseline on every device | SNMP |
| **FASTPATH/ICOS** | Ubiquiti EdgeSwitch, Netgear, LANCOM SX | SNMP |
| **Foundry/FastIron** | Ruckus ICX, HP-SN half of ProCurve | SNMP + RESTCONF (ICX 9.x) |
| **H3C-derived** | HPE Comware, Huawei VRP | SNMP + NETCONF (Comware) |
| **ProCurve AOS-S** | HP-ICF half of ProCurve | SNMP + REST |
| **AOS-CX** | Aruba CX | REST v10.0x + gNMI/OpenConfig |
| **Radlan** | Cisco SMB | SNMP |
| **API-first** | MikroTik, UniFi, LANCOM LMC, Ruckus SmartZone, Cisco IOS-XE, Aruba controllers | REST / RESTCONF / NETCONF |

D-Link is the outlier: 143 modules, entirely its own (`DLINKSW-*` enterprise and
a separate `DGS-1210` consumer set), with no shared lineage — and the broadest
first-hop-security and IPv6 surface of any vendor here.

---

## Cross-cutting patterns worth naming

These recur across many entities and are collected once so the individual
records can point at them.

### 1. The port-namespace problem

At least seven different port identifiers appear in this corpus and none is a
superset of the others:

| Identifier | Where | Notes |
|---|---|---|
| `ifIndex` | IF-MIB and most things | not stable across reload |
| `dot1dBasePort` | BRIDGE-MIB, Q-BRIDGE-MIB, STP, PortList bitmaps | needs `dot1dBasePortIfIndex` to translate |
| `entPhysicalIndex` | ENTITY-MIB, ProCurve PoE, Huawei optics | needs `entAliasMappingTable` |
| `pethPsePortGroupIndex` + `pethPsePortIndex` | POWER-ETHERNET-MIB | no portable mapping |
| `lldpLocPortNum` | LLDP-MIB | subtype-dependent |
| `dot1xPaePortNumber` | 802.1X | usually but not always ifIndex |
| vendor-local (unit/slot/port, LCOS `Ifc`, Foundry port number) | many | vendor rule required |

**Every join between two entities in this atlas crosses at least one of these
boundaries.** The mapping tables (`dot1dBasePortIfIndex`,
`entAliasMappingTable`) are optional in practice, and a decoder that assumes
identity where a mapping is missing produces confidently wrong data. This
deserves to be a first-class concept in FlowSeer's collection layer, not a
per-decoder detail.

### 2. Deprecated-and-current table pairs

Several core entities have two or three generations of MIB alive at once:

| Entity | Deprecated | Current |
|---|---|---|
| addressing | `ipAddrTable[addr]` | `ipAddressTable[type, addr]` |
| neighbour cache | `ipNetToMediaTable` | `ipNetToPhysicalTable` |
| routing | `ipRouteTable` → `ipForwardTable` → `ipCidrRouteTable` | `inetCidrRouteTable` |
| bridge | `BRIDGE-MIB` / `Q-BRIDGE-MIB` | `IEEE8021-Q-BRIDGE-MIB` |
| LLDP | `LLDP-MIB` | `LLDP-V2-MIB` (barely implemented) |

Agents typically implement the old one fully and the new one partially, or the
reverse, with no way to tell but trying. SNMP::Info's read-both-and-prefer
pattern is the established answer.

### 3. TimeFilter tables

`dot1qVlanCurrentTable[timeMark, vlanIndex]`,
`lldpRemTable[timeMark, port, index]`, and the RMON history tables use the
`TimeFilter` textual convention: the first index is "only rows changed since
this sysUpTime". Walked naively from 0 they return duplicates; used properly
they are an efficient change feed. FlowSeer's change-watch path could exploit
this and currently has no concept of it.

### 4. Job tables

Four unrelated entities — [config-file](entities/01-platform.md#config-file),
[cable-diag](entities/02-interface.md#cable-diag),
[ip-diagnostics](entities/04-ip.md#ip-diagnostics), and firmware upgrade — are
all modelled by every vendor as a *job*: a definition row, a status, and a
result log. `DISMAN-PING-MIB`'s
`pingCtlTable[owner, testName]` / `pingResultsTable` / `pingProbeHistoryTable`
triple is the cleanest instance. FlowSeer's Config/State/Event triad has no
place for this shape, and four entities need one.

### 5. Near/far duality

LLDP local/remote, DSL ATUC/ATUR, PON OLT/ONU, LACP actor/partner, PoE PSE/PD,
MACsec tx-SC/rx-SC. Every link-level protocol models both ends, and in each case
the far-end view is a *claim* made by the peer, not an observation. Treating
them as the same kind of fact is a category error that shows up as
"the neighbour's data is wrong" bug reports.

### 6. Controllers add a key level

Wireless (`[wtp, radio, station]`), PON (`[oltPort, onu, uniPort]`), stacking
(`[unit, slot, port]`), cluster management (HGMP, Single-IP). Whenever one agent
answers for subordinate devices, the natural key gains a level. FlowSeer's
Integration/Binding/Integration Scope trio is the right shape for this and is
currently only exercised at the device level.
