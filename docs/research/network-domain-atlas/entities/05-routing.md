---
title: Entity records — L3 routing protocols
date: 2026-08-30
part: 5 of 10
scope: RIP, OSPF, IS-IS, BGP, EIGRP, multicast routing, BFD, MPLS
---

# L3 — routing protocols

Eight entities. FlowSeer's stated device classes are switches, APs, and WLAN
controllers, so most of this is out of near-term scope. It is recorded because
(a) L3 switches in the corpus do run OSPF and BGP, (b) the *shape* of these
models is the strongest argument for the network-instance dimension, and (c)
knowing what exists prevents someone re-deriving it later.

Index: [rip](#rip) · [ospf](#ospf) · [isis](#isis) · [bgp](#bgp) ·
[eigrp](#eigrp) · [multicast-routing](#multicast-routing) · [bfd](#bfd) ·
[mpls](#mpls)

---

## The shared shape

Every routing protocol model in both the MIB and YANG worlds decomposes the same
way, and knowing the decomposition is most of the work:

1. **Global / instance** — router id, admin state, timers, distance.
2. **Areas / address families / peer groups** — the protocol's own scoping.
3. **Interfaces** — per-interface protocol config and state (cost, hello timers,
   authentication, passive).
4. **Neighbours / peers / adjacencies** — the operational relationships, with a
   state machine value and up/down counters. *This is the entity operators watch.*
5. **Database** — LSDB, RIB-in/out, topology table. Large, expensive to poll,
   rarely needed by a management product.
6. **Statistics / errors**.

A management product that models only layer 4 (neighbours) plus a slice of layer
1 gets most of the operational value at a fraction of the cost. NAPALM's
getters make exactly this choice: `get_bgp_neighbors` yes, full RIB no.
SuzieQ models `Bgp` and `Ospf` as neighbour-level tables. LibreNMS's
`bgp-peers` discovery module likewise.

For FlowSeer the recommendation is the same: if routing protocols are ever
modelled, model the **adjacency** first and treat databases as out of scope.

---

## rip

`RIPv2-MIB` (RFC 1724): `rip2IfConfTable[rip2IfConfAddress]` — **keyed on the
interface's IP address, not ifIndex**, which is a RIPv1-era design that breaks
on unnumbered and multi-address interfaces; `rip2IfStatTable[address]`;
`rip2PeerTable[peerAddress, peerDomain]`.

Vendors: Comware, Huawei (`HUAWEI-RIPV2-EXT-MIB`), D-Link
(`DLINKSW-RIP-MIB`, `DLINKSW-RIPNG-MIB`), ProCurve (`HP-ICF-RIP`), LANCOM LCOS,
FASTPATH family. OpenConfig has **no RIP model** — a deliberate omission that
tells you how much the modern world cares.

Legacy. Model only if a customer network actually runs it.

---

## ospf

The largest single protocol surface in the corpus: 555 MIB tables and 1,698
YANG nodes.

`OSPF-MIB` (RFC 4750) for v2: `ospfGeneralGroup` scalars,
`ospfAreaTable[areaId]`, `ospfStubAreaTable[areaId, tos]`,
`ospfLsdbTable[areaId, type, lsid, routerId]`,
`ospfAreaRangeTable[areaId, net]`, `ospfHostTable`, `ospfIfTable[ipAddress,
addressLessIf]`, `ospfIfMetricTable`, `ospfVirtIfTable`,
`ospfNbrTable[nbrIpAddr, nbrAddressLessIndex]`, `ospfVirtNbrTable`,
`ospfExtLsdbTable`, `ospfAreaAggregateTable[areaId, lsdbType, net, mask]`.

`OSPFV3-MIB` (RFC 5643) for v3, re-keyed on ifIndex rather than IP address —
the fix RIPv2-MIB never got.

`openconfig-ospfv2` splits into `-global`, `-area`, `-area-interface`,
`-common`, `-lsdb`, plus `openconfig-ospf-policy` and `openconfig-ospf-types`.
All of it hangs under
`network-instances/network-instance/protocols/protocol[OSPF, name]`.

Vendors: everyone with L3. Notable extensions — `HP-ICF-OSPF`
(`hpicfOspfAreaAggregateTable [AUGMENTS ospfAreaAggregateEntry]`),
`CISCOSB-IpRouter::rlOspfIfExtTable [AUG ospfIfEntry]`,
`HUAWEI-OSPFV2-MIB` / `-OSPFV3-MIB`, `HH3C-OSPF-MIB`, `DLINKSW-OSPFV2-MIB` /
`-OSPFV3-MIB`, `FOUNDRY-SN-OSPF-GROUP-MIB` / `HP-SN-OSPF-GROUP-MIB` (shared
lineage), `cisco-ospf.yang` + `cisco-xe-ietf-ospf-deviation`, LANCOM LCOS's own
tree keyed on `[ospfInstance, areaId, ...]`.

**The `[ipAddress, addressLessIf]` key in `ospfIfTable` and `ospfNbrTable` is
the trap.** `addressLessIf` is 0 for numbered interfaces and the ifIndex for
unnumbered ones. Collectors that assume it is always 0 lose unnumbered
adjacencies silently.

---

## isis

`ISIS-MIB` (RFC 4444): `isisManAreaAddrTable`, `isisAreaAddrTable`,
`isisSummAddrTable[addressType, address, prefixLen]`,
`isisRedistributeAddrTable`, `isisCircTable[isisCircIndex]`,
`isisISAdjTable[isisCircIndex, isisISAdjIndex]`, `isisLSPSummaryTable[level,
lspId]`, `isisRATable`.

`openconfig-isis` with `-lsp`, `-lsdb-types`, `-routing`, `-policy`, `-types`.

Vendors in corpus: Comware `HH3C-ISIS-MIB`, Huawei `HUAWEI-ISIS-CONF-MIB`,
D-Link `DLINKSW-ISIS-MIB`, Cisco IOS-XE. Absent from every access-switch family.

Note `isisCircIndex` is *not* ifIndex — IS-IS circuits have their own numbering,
with `isisCircIfIndex` as the mapping column. Same class of problem as
`lldpLocPortNum`.

---

## bgp

`BGP4-MIB` (RFC 4273) is genuinely inadequate and everyone knows it:
`bgpPeerTable[bgpPeerRemoteAddr]` — **IPv4 peers only, keyed on the peer address
with no VRF and no AFI/SAFI**; `bgp4PathAttrTable[prefix, prefixLen, peer]`.
`BGP4V2-MIB` (the draft that became RFC 4273's successor and never shipped as a
standard) fixes the family problem; only `BGP4V2-TC-MIB` is in this corpus.

The practical consequence: **BGP over SNMP cannot represent an IPv6 peer or a
VRF peer.** Every serious BGP collector uses the CLI, NETCONF, or BMP instead.

`openconfig-bgp` is the credible model, decomposed into `-global`, `-neighbor`,
`-peer-group`, `-common`, `-common-multiprotocol`, `-common-structure`,
`-errors`, `-types`, `-policy`, plus the `openconfig-rib-bgp*` family for
adj-rib-in-pre/post, loc-rib, and adj-rib-out per AFI/SAFI. It is the most
complete openly-specified BGP model in existence and it is in this corpus in
full.

Vendors: Comware `HH3C-BGP4V2-MIB` / `-BGP-VPN-MIB` / `-BGP-EVPN-MIB`, Huawei
`HUAWEI-BGP-VPN-MIB` / `-BGP-GR-MIB` / `-BGP-ACCOUNTING-MIB`, D-Link
`DLINKSW-BGP-MIB`, Foundry lineage `FOUNDRY-SN-BGP4-GROUP-MIB` /
`HP-SN-BGP4-GROUP-MIB`, `FASTPATH-BGP-MIB`, Cisco IOS-XE
`Cisco-IOS-XE-bgp` + `-bgp-oper` + `-bgp-rib-oper`, `openconfig-bgp` on Aruba CX
and Ruckus ICX.

---

## eigrp

Cisco proprietary, opened as RFC 7868 (informational). **Zero MIB tables in the
corpus**; 477 YANG nodes, all in `Cisco-IOS-XE-eigrp` and
`Cisco-IOS-XE-eigrp-obsolete`. `CISCO-EIGRP-MIB` exists upstream and is part of
the missing Cisco enterprise MIB set.

Relevant only on Cisco networks, and then only via YANG or CLI.

---

## multicast-routing

Four protocols and a forwarding table, all separate entities that get lumped
together:

| Model | Scope |
|---|---|
| `IPMROUTE-STD-MIB` (RFC 5132) | the multicast forwarding cache — `ipMRouteTable[group, source, sourceMask]`, `ipMRouteNextHopTable`, `ipMRouteInterfaceTable` |
| `PIM-MIB` / `PIM-STD-MIB` (RFC 5060) | PIM-SM/DM neighbours, RPs, interfaces |
| `PIM-BSR-MIB` (RFC 5240) | bootstrap router / candidate RP |
| `MSDP-MIB` | inter-domain source discovery |
| `DVMRP-MIB` / `DVMRP-STD-MIB` | `dvmrpInterfaceTable[ifIndex]`, `dvmrpNeighborTable[ifIndex, address]`, `dvmrpRouteTable[source, sourceMask]` — dead protocol, still shipped |
| `IGMP-STD-MIB` / `MGMD-STD-MIB` (RFC 5519) | the *router* side of IGMP/MLD; MGMD unifies both families |
| `openconfig-pim`, `openconfig-igmp` | modern, neighbour and interface level |

Vendors: Comware `HH3C-MULTICAST-MIB`, Huawei `HUAWEI-MULTICAST-MIB` /
`-IPMCAST-MIB` / `-MSDP-MIB` / `-PIM-STD-MIB` / `-PIM-BSR-MIB` / `-MGMD-STD-MIB`,
D-Link `DLINKSW-MGMD-EXT-MIB` / `-PIM-EXT-MIB` / `-MSDP-EXT-MIB` /
`-DVMRP-EXT-MIB` / `-IPMCAST-EXT-MIB` / `-MGMD-PROXY-MIB`, ProCurve
`HP-ICF-PIM` / `HP-ICF-MLD-MIB`, Cisco SMB `CISCOSB-PIM-MIB` /
`CISCOSB-MGMD-ROUTER-MIB`, FASTPATH `FASTPATH-MULTICAST-MIB`, LANCOM LCOS
(`lcsStatusMCastIpv4DownstreamMfibTable[rtgTag, source, group, interface,
source]` — again with the routing tag in the key).

Do not confuse with [igmp-snooping](03-switching.md#igmp-snooping), which is the
L2 side and has no standard at all.

---

## bfd

`BFD-STD-MIB` (RFC 7331): `bfdSessTable[bfdSessIndex]`,
`bfdSessPerfTable [AUGMENTS bfdSessEntry]`,
`bfdSessDiscMapTable[bfdSessDiscriminator]`,
`bfdSessIpMapTable[interface, srcAddrType, srcAddr, dstAddrType, dstAddr]` —
note the three lookup tables mapping different natural keys onto the internal
session index. That triple-index design is a good pattern for any entity whose
internal id is opaque.

`openconfig-bfd`: `bfd/interfaces/interface[id]/peers/peer[...]` with
`state/session-state` and `async`/`echo` sub-state.

Vendors: `HH3C-BFD-STD-MIB`, `HUAWEI-BFD-MIB`, `DLINKSW-BFD-MIB`,
`FOUNDRY-BFD-STD-MIB`, ProCurve's static-route BFD tracking
(`hpicfIpStaticRouteBfdTable`), Cisco IOS-XE `Cisco-IOS-XE-bfd-oper`.

Operationally interesting because BFD session state is the fastest available
signal that a link is logically down while physically up — the same blind spot
[udld](02-interface.md#udld) and [macsec](03-switching.md#macsec) create.

---

## mpls

Out of scope for FlowSeer's device classes; recorded for completeness.

Standards present: `MPLS-LSR-STD-MIB` (RFC 3813 — `mplsInterfaceTable`,
`mplsInSegmentTable`, `mplsOutSegmentTable`, `mplsXCTable`,
`mplsLabelStackTable`), `MPLS-TE-STD-MIB` (RFC 3812 —
`mplsTunnelTable[index, instance, ingressLSRId, egressLSRId]`,
`mplsTunnelHopTable`, `mplsTunnelResourceTable`, `mplsTunnelARHopTable`),
`MPLS-LDP-STD-MIB` (RFC 3815), `MPLS-L3VPN-STD-MIB` (RFC 4382),
`MPLS-TC-STD-MIB`, `PW-STD-MIB` / `PW-TC-STD-MIB`, `IANA-GMPLS-TC-MIB`.

OpenConfig: `openconfig-mpls` with `-igp`, `-ldp`, `-rsvp`, `-sr`, `-static`,
`-te`, `-types`; plus `openconfig-segment-routing` and `openconfig-srte-policy`.

Vendors: Comware (`HH3C-MPLS-*`, 8 modules), Huawei (`HUAWEI-MPLS*`, 6 modules
plus SRv6 and SR policy), D-Link `DLINKSW-MPLS-MIB`, Foundry lineage
`HP-SN-MPLS-LSR-MIB` / `-TE-MIB` / `-TC-MIB`, Cisco IOS-XE.

The one detail worth carrying forward: `mplsTunnelTable`'s four-part key
(index, instance, ingress LSR, egress LSR) is the most elaborate natural key in
the corpus, and it exists because a tunnel identity is genuinely
four-dimensional. It is a useful counterexample to the instinct that every
entity should have a single opaque id.
