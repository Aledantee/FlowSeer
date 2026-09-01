---
title: Vendor dossiers — index
date: 2026-08-30
---

# Vendor dossiers

One file per vendor family. Each records: what the corpus actually contains for
that vendor, which management planes exist and which is authoritative, the
lineage it belongs to, the shapes and keys that are peculiar to it, and the
specific traps a decoder author will hit.

These build on the per-vendor `SOURCES.md` files already in `spec/`, which
document *provenance* (where the files came from, how to refresh them). The
dossiers document *content* — what is in them and what it means.

## At a glance

| Vendor | Enterprise OID | Corpus | Lineage | Authoritative plane | Dossier |
|---|---|---|---|---|---|
| IETF / IEEE / IANA | — | 186 + 212 MIBs | — | standards | [standards.md](standards.md) |
| OpenConfig | — | 144 modules | — | gNMI / RESTCONF | [standards.md](standards.md#openconfig) |
| Cisco (SMB) | 9 | 116 `CISCOSB-*` MIBs | Marvell/Radlan | SNMP | [cisco.md](cisco.md) |
| Cisco (IOS-XE) | 9 | 1,071 YANG modules | IOS-XE | NETCONF / RESTCONF / gNMI | [cisco.md](cisco.md#ios-xe) |
| Cisco (enterprise SNMP) | 9 | **absent** | — | SNMP | [cisco.md](cisco.md#the-gap) |
| Aruba CX | 47196 | 35 MIBs + 19 YANG | clean-sheet | REST v10.0x | [aruba.md](aruba.md) |
| Aruba wireless | 14823 | 25 MIBs | ArubaOS | SNMP + Central API | [aruba.md](aruba.md#arubaos-wireless) |
| HPE Comware | 25506 | 271 `HH3C-*` MIBs | H3C | NETCONF (login-walled) | [hpe.md](hpe.md#comware) |
| HP ProCurve / AOS-S | 11 | 82 MIBs (two families) | ICF + Foundry | SNMP + REST | [hpe.md](hpe.md#procurve--aos-s) |
| Huawei | 2011, 34774 | 240 MIBs | VRP (H3C-adjacent) | SNMP + NETCONF | [huawei.md](huawei.md) |
| D-Link | 171 | 143 MIBs | own | SNMP | [dlink.md](dlink.md) |
| Ruckus ICX | 1991 (Foundry) | 30 MIBs + 111 YANG | Foundry/FastIron | SNMP + RESTCONF | [ruckus.md](ruckus.md#icx) |
| Ruckus wireless | 25053 | 21 MIBs + 21 protos + REST | own | SmartZone REST | [ruckus.md](ruckus.md#smartzone-zonedirector-unleashed) |
| Ubiquiti EdgeSwitch | 4413 (Broadcom) | 40 MIBs | **FASTPATH** | SNMP | [ubiquiti.md](ubiquiti.md) |
| Ubiquiti UniFi | 41112 | 3 MIBs | Linux | UniFi Network API | [ubiquiti.md](ubiquiti.md#unifi) |
| Netgear | 4526, 4413 | 4 MIBs | **FASTPATH** | SNMP | [netgear.md](netgear.md) |
| LANCOM | 2356 | 261 MIBs (3 platforms) | LCOS + **FASTPATH** | SNMP + LMC REST | [lancom.md](lancom.md) |
| MikroTik | 14988 | 1 MIB | RouterOS | REST API | [mikrotik.md](mikrotik.md) |

## Reading order

If you are writing a decoder, read [the lineages](../00-methodology-and-lineages.md#the-lineages)
first — it will tell you whether the vendor you are about to work on is already
covered by a decoder for a different name.
