---
title: Entity records — L2, switching
date: 2026-08-30
part: 3 of 10
scope: VLANs, membership, QinQ, FDB, STP, discovery protocols, snooping, mirroring, isolation, ring protection, MACsec, overlays
---

# L2 — switching

Twenty-one entities, and the largest body of evidence in the corpus (53 KB of
matched tables). This is also where FlowSeer's `net/switching/v1` sits and where
its one acknowledged architectural hole — bridge-domain identity — lives.

Index: [vlan](#vlan) · [vlan-membership](#vlan-membership) · [qinq](#qinq) ·
[fdb](#fdb) · [stp](#stp) · [loop-protect](#loop-protect) · [lldp](#lldp) ·
[cdp-like](#cdp-like) · [igmp-snooping](#igmp-snooping) · [mvr](#mvr) ·
[storm-control](#storm-control) · [mirroring](#mirroring) ·
[port-isolation](#port-isolation) · [garp](#garp) · [voice-vlan](#voice-vlan) ·
[ring-protection](#ring-protection) · [macsec](#macsec) ·
[fabric-overlay](#fabric-overlay) · [l2pt](#l2pt) · [jumbo-mtu](#jumbo-mtu)

---

## The bridge-domain problem, once, up front

Every entity below is scoped to *a bridge*. Standards say so explicitly and
vendors say so implicitly, and getting it wrong is the failure mode that
poisons FDB, VLAN, and STP data simultaneously.

- **`BRIDGE-MIB` (RFC 4188)** assumes **one** bridge per SNMP agent. Its tables
  (`dot1dBasePortTable`, `dot1dTpFdbTable`, `dot1dStpPortTable`) have no bridge
  dimension.
- **`Q-BRIDGE-MIB` (RFC 4363)** adds VLANs but still assumes one bridge, and
  reaches multiple filtering databases only through `dot1qVlanFdbId` — a
  many-to-one VLAN→FID map that is the standard's whole answer to shared vs
  independent VLAN learning (SVL/IVL).
- **`IEEE8021-Q-BRIDGE-MIB`** (the 802.1Q-2011+ replacement) fixes this by
  putting a bridge-component id first in **every** table key. Look at the keys
  in the evidence: `ieee8021QBridgeFdbTable[componentId, fdbId]`,
  `ieee8021QBridgeTpFdbTable[componentId, fdbId, address]`,
  `ieee8021QBridgeCVlanPortTable[componentId, portNumber]`. The component is a
  first-class part of identity. The index object is named per table
  (`ieee8021QBridgeFdbComponentId`, `ieee8021QBridgeVlanCurrentComponentId`, …)
  rather than shared, so it is easy to overlook when skimming.
- **`openconfig-network-instance`** does the same with
  `network-instances/network-instance[name]` of type `L2L3` / `L2P2P` /
  `L2VSI`, hanging `fdb`, `vlans`, and protocols underneath.

FlowSeer's net-core research already named this "the largest unresolved
architectural dependency" and said VLAN IDs alone are not universal across
independent learning domains. The corpus confirms it, and adds a practical
note: **the vendors that ship the component-aware IEEE MIB are a minority**, so
FlowSeer will be reading component-less data from most devices and must decide
what the implied component is. The honest answer is "the device's single default
bridge", stated explicitly, with room to add the dimension later.

---

## vlan

**What it is.** The VLAN database: which VIDs exist on this device, their
names, and whether they are statically configured or dynamically registered.

### Canonical models

| Table | Key | Notes |
|---|---|---|
| `Q-BRIDGE-MIB::dot1qVlanStaticTable` | `dot1qVlanIndex` | configured VLANs: `dot1qVlanStaticName`, `dot1qVlanStaticEgressPorts`, `dot1qVlanForbiddenEgressPorts`, `dot1qVlanStaticUntaggedPorts`, `dot1qVlanStaticRowStatus` |
| `Q-BRIDGE-MIB::dot1qVlanCurrentTable` | `dot1qVlanTimeMark, dot1qVlanIndex` | **operational** VLANs including dynamically learned ones; `dot1qVlanStatus` ∈ other/permanent/dynamicGvrp, `dot1qVlanCurrentEgressPorts`, `dot1qVlanCurrentUntaggedPorts`, `dot1qVlanFdbId` |
| `Q-BRIDGE-MIB::dot1qVlanTable` (scalars) | — | `dot1qVlanNumDeletes`, `dot1qNextFreeLocalVlanIndex` |
| `IEEE8021-Q-BRIDGE-MIB::ieee8021QBridgeVlanCurrentTable` | `componentId, timeMark, vlanIndex` | the component-aware replacement |
| `openconfig-vlan` | `vlans/vlan[vlan-id]` | `config/{vlan-id, name, status}`, `state/members` |

Note `dot1qVlanTimeMark` — a `TimeFilter` textual convention that turns the
table into "rows changed since T". Almost every collector walks it with
`timeMark = 0` and never uses the filter, but it exists and is the standard's
change-detection mechanism.

### Vendor mapping

| Family | Surface |
|---|---|
| IETF | `Q-BRIDGE-MIB` — implemented essentially everywhere |
| D-Link | `dot1qVlanTable[dot1qVlanName]` on the DGS-1210 consumer line — **keyed on the name**, a private redefinition, plus `DLINKSW-VLAN-MIB` on the enterprise line |
| Cisco SMB | `CISCOSB-vlan-MIB` (`vlanPortModeTable`, `vlanMacBaseVlanPortTable`), plus PVST/MSTP VLAN tables in `CISCOSB-BRIDGEMIBOBJECTS-MIB` |
| HP ProCurve | `HP-VLAN::hpVlanIdentTable[hpVlanIdentIndex]`, `CONFIG-MIB::hpSwitchStpVlanTable` |
| Aruba CX | `ARUBAWIRED-PORTVLAN-MIB::arubaWiredPortVlanMemberTable` |
| Comware / Huawei | `HH3C-LswVLAN-MIB`, `HUAWEI-L2VLAN-MIB`, `HUAWEI-L3VLAN-MIB` |
| Ruckus ICX | `FOUNDRY-MAC-VLAN-MIB` / `FOUNDRY-SN-MAC-VLAN-MIB` (MAC-based VLANs), Q-BRIDGE for the rest |
| LANCOM SX | `LCOS-SX-MIB::lcsVlanTable[ifIndex]` — VLAN table keyed on *ifIndex*, an unusual inversion |
| LANCOM LX | `lcosLXStatusBridgeVLANTable[interfaceName, vlanId]` |
| FASTPATH family | `agentSwitchIpVlanTable`, `agentSwitchInternalVlanTable` (internal VLANs reserved for routed ports — a real thing that confuses inventory) |
| Cisco enterprise | `CISCO-VTP-MIB::vtpVlanTable` — VTP-distributed VLANs; **the MIB is absent from this corpus** but referenced by the IOS-XE YANG deviations |

### Traps and pitfalls

- **VID ranges.** 1–4094 usable, 0 = priority-tag only, 4095 reserved. Several
  vendors reserve a block at the top (Cisco 1002–1005 for legacy, FASTPATH
  "internal VLANs" for routed ports, Comware reserved ranges). A discovery that
  reports these as operator VLANs produces noise.
- **`dot1qVlanStaticTable` vs `dot1qVlanCurrentTable`** are configured vs
  operational and disagree whenever GVRP/MVRP is running. FlowSeer's
  `VlanRegistration` rename (from `VlanStatus`) captures exactly this
  distinction and is the right call.
- **PortList bitmaps.** `dot1qVlanStaticEgressPorts` is an octet string bitmap
  over `dot1dBasePort` numbers, not `ifIndex`. Translating requires
  `dot1dBasePortIfIndex`. On a 48-port switch it is 6 bytes; on a 10-slot
  chassis with `dot1dBasePort` numbering that skips, it is a trap. This is the
  same bitmap problem as LAG member lists.
- MAC-based, protocol-based, subnet-based, and voice VLANs are *assignment
  mechanisms*, not VLAN kinds — see [voice-vlan](#voice-vlan).

### FlowSeer status

`net/switching/v1/vlan.proto`, `vlan_id.proto`, `vlan_registration.proto`.
Solid. Missing: the bridge/FID dimension (see above) and any statement of how
the port bitmap is resolved.

---

## vlan-membership

**What it is.** Which VLANs a port carries, tagged or untagged, and its PVID.

### Canonical models

Two halves that must be read together:

1. **From the VLAN side** — `dot1qVlanStaticEgressPorts` and
   `dot1qVlanStaticUntaggedPorts` bitmaps per VLAN.
2. **From the port side** — `Q-BRIDGE-MIB::dot1qPortVlanTable [AUGMENTS
   dot1dBasePortEntry]`: `dot1qPvid`, `dot1qPortAcceptableFrameTypes`
   (`admitAll` | `admitOnlyVlanTagged` | `admitOnlyUntagged`),
   `dot1qPortIngressFiltering`, `dot1qPortGvrpStatus`.

Plus `dot1qPortVlanStatisticsTable[dot1dBasePort, dot1qVlanIndex]` and its HC
form — per-port-per-VLAN counters, rarely implemented and very useful when they
are.

`openconfig-vlan` puts it on the interface:
`interfaces/interface/ethernet/switched-vlan/config/{interface-mode, native-vlan,
access-vlan, trunk-vlans}` where `interface-mode` ∈ `ACCESS` | `TRUNK`.

### The mode question

There is **no standard "switchport mode"**. `ACCESS`/`TRUNK` is a Cisco CLI
concept that OpenConfig adopted. The 802.1Q model has no mode: a port simply has
a PVID, an egress set, an untagged set, and an acceptable-frame-type rule. Every
"mode" is a shorthand for a combination of those, and vendors disagree on the
combinations — hybrid (Comware/Huawei), general (Cisco SMB, D-Link), promiscuous
and isolated (private VLAN), dot1q-tunnel (QinQ).

FlowSeer's `SwitchportFacet` carrying "PVID, exact tagged/untagged membership
sets, ingress filtering, frame-admission behavior, and an *optional* normalized
mode" is precisely right, and the optionality of the mode is the load-bearing
detail. The primitive facts are portable; the mode is a lossy summary.

### Vendor mapping

| Family | Surface |
|---|---|
| IETF | `dot1qPortVlanTable [AUG dot1dBasePortEntry]` |
| IEEE | `ieee8021QBridgePortVlanTable [AUG ieee8021BridgeBasePortEntry]`, `ieee8021QBridgeCVlanPortTable[componentId, portNumber]` |
| Cisco SMB | `vlanPortModeTable[ifIndex]`, `vlanPortModeExtTable[ifIndex]` — mode as an explicit column |
| Comware | `HH3C-PROTOCOL-VLAN-MIB::hh3cProtocolVlanPortTable[port, vlanId, protocolId]`, `HH3C-SUBNET-VLAN-MIB::hh3cSubnetVlanPortCreateTable`, `HH3C-VOICE-VLAN-MIB::hh3cvoiceVlanPortTable[ifIndex]` |
| Huawei | `HUAWEI-L2IF-MIB`, `hwCppsPortVlanTable[port, vlanId]` |
| HP ProCurve | `CONFIG-MIB::hpSwitchPortTable[hpSwitchPortIndex]` |
| Aruba CX | `arubaWiredPortVlanMemberTable[memberIndex]` |
| LANCOM LCOS | `lcsStatusVlanPortTableTable[Port]` / `lcsSetupVlanPortTableTable[Port]` |
| LANCOM SX | `lcsVLANPortStatusTable[ifIndex]`, GS2310 line `gs2310VlanPortsTable[Port]` |
| FASTPATH family | `agentSwitchportConfigTable[intfIndex]`, `agentSwitchPortDVlanTagTable[ifIndex, TPid]` |

### FlowSeer status

`net/switching/v1/switchport_facet.proto`, `switchport_mode.proto`,
`frame_admission.proto`. Complete for the primitives. The mapping from vendor
mode strings to the normalized mode is undone and belongs in a decoder table,
not the schema.

---

## qinq

**What it is.** VLAN stacking (802.1ad provider bridging): an outer S-tag added
by the provider over the customer's C-tags, and the VID translation that goes
with it.

### Canonical models

`IEEE8021-PB-MIB` is the standard and its key structure repays reading:

| Table | Key |
|---|---|
| `ieee8021PbVidTranslationTable` | `componentId, port, localVid` |
| `ieee8021PbCVidRegistrationTable` | `componentId, port, cVid` |
| `ieee8021PbEdgePortTable` | `componentId, port, sVid` |
| `ieee8021PbServicePriorityRegenerationTable` | `componentId, port, sVid, receivedPriority` |

Also `IEEE8021-PBB-MIB` (802.1ah MAC-in-MAC, I-SID/B-VID) and
`IEEE8021-PBBTE-MIB`.

### Vendor mapping

| Family | Surface |
|---|---|
| HP ProCurve | `HP-ICF-PROVIDER-BRIDGE::hpicfProviderBridgeVlanTypeTable [AUG dot1qVlanStaticEntry]` + `hpicfProviderBridgePortTable[ifIndex]` |
| Aruba CX | `ARUBAWIRED-PROVIDER-BRIDGE-MIB` — same two-table shape as ProCurve (shared HPE heritage) |
| Comware | `HH3C-QINQ-MIB` + `HH3C-QINQV2-MIB`: `hh3cQinQVidTable[ifIndex, vlanID]`, `hh3cQinQVidSwapTable[ifIndex, vlanID, oldVid]`, `hh3cQinQBpduTunnelTable[ifIndex, protocolIndex]`, `hh3cQinQPriorityRemarkTable` — the most complete vendor model |
| Huawei | `HUAWEI-QINQ-MIB::hwQinQSubIfVlanStackingTable[ifIndex, ceVlanStart]`, plus QinQ-aware multicast limits |
| D-Link | `DLINKSW-VLAN-TUNNEL-MIB::dVlanTunnelEtherTypeTable[ifIndex]`, `dVlanTunnelInterfaceTable[ifIndex]`, `dVlanMappingTranslateTable[ifIndex, originalVlan, originalInnerVlan]` |
| LANCOM SX | `lcsVLANTranslationPortTable[index]`, `lcsVLANTranslationMappingTable[index]` |
| Cisco IOS-XE | `cisco-bridge-domain` `pbb`, `Cisco-IOS-XE-ethernet-oam` `dot1ad` |

### Traps and pitfalls

- **The TPID is configurable.** 0x88A8 is the 802.1ad standard, 0x8100 the
  802.1Q value, and 0x9100/0x9200 appear in the wild. FASTPATH exposes it in the
  *key* (`agentSwitchPortDVlanTagTable[ifIndex, TPid]`). FlowSeer's `VlanTag`
  message carrying TPID explicitly is correct and necessary.
- Translation/swap tables mean the VID observed on the wire is not the VID
  configured on the port. Any topology inference from VLAN IDs across a provider
  edge is unsound.
- FlowSeer's `VlanTagStack` (ordered outermost-to-innermost, uncapped) matches
  what the vendor models actually need — Huawei's `PeVid`/`CeVid` pairs and
  Comware's swap tables both assume exactly two, but 802.1ah adds a third layer.

---

## fdb

**What it is.** The MAC forwarding table: which MAC was seen behind which port,
in which VLAN, and how it got there.

This is the entity a network-management product is most often bought for
("where is this device plugged in"), and it is the one with the most subtle
keying.

### Canonical models

| Table | Key | Problem |
|---|---|---|
| `BRIDGE-MIB::dot1dTpFdbTable` | `dot1dTpFdbAddress` | **no VLAN dimension** — one flat table per bridge. `dot1dTpFdbPort` is a `dot1dBasePort`, `dot1dTpFdbStatus` ∈ other/invalid/learned/self/mgmt |
| `Q-BRIDGE-MIB::dot1qTpFdbTable` | `dot1qFdbId, dot1qTpFdbAddress` | keyed on the **FID**, not the VID. `dot1qVlanFdbId` maps VLAN→FID |
| `Q-BRIDGE-MIB::dot1qFdbTable` | `dot1qFdbId` | the FID list and its `dot1qFdbDynamicCount` |
| `Q-BRIDGE-MIB::dot1qStaticUnicastTable` | `dot1qFdbId, address, receivePort` | configured entries |
| `IEEE8021-Q-BRIDGE-MIB::ieee8021QBridgeTpFdbTable` | `componentId, fdbId, address` | the component-aware form |
| `openconfig-network-instance-l2` | `l2ni-instance/fdb` | under a network instance |

### The FID vs VID trap

`dot1qTpFdbTable` is keyed on `dot1qFdbId`. Under **independent VLAN learning
(IVL)** the FID happens to equal the VID and everyone gets away with treating
them as the same. Under **shared VLAN learning (SVL)** several VLANs share one
FID and the mapping is many-to-one — so a MAC in FID 1 cannot be attributed to a
specific VLAN at all. `dot1qVlanFdbId` in `dot1qVlanCurrentTable` is the map,
and a collector that never reads it silently assumes IVL.

FlowSeer's `FdbEntry` is "keyed by VLAN ID and EUI-48". That is the IVL
assumption baked into the schema. It is a defensible default for enterprise
switching, but it should be *stated* rather than implied, and it is the same
bridge-domain gap noted at the top.

### The Cisco community-string trap

On Cisco IOS with CatOS heritage, `dot1dTpFdbTable` is only accessible
**per-VLAN via community string indexing** (`community@vlanID`) — SNMPv1/v2c
only, one query per VLAN. SNMP::Info, Netdisco, and LibreNMS all carry explicit
machinery for this. It does not apply to the vendors in this corpus, but it
applies to Cisco enterprise, which this corpus is missing.

### Vendor mapping

| Family | Surface |
|---|---|
| IETF | `BRIDGE-MIB`, `Q-BRIDGE-MIB` |
| D-Link | `DLINKSW-L2FDB-MIB::dL2FdbStaticUnicastTable[vlanID, macAddr]`; `AGENT-GENERAL-MIB::agentFDBClearByPortTable` / `agentFDBClearByVlanTable` (clear *operations*) ; `agentFDBSecurityTable[vid, mac]` |
| HP ProCurve | `STATISTICS-MIB::hpSwitchVlanFdbAddrTable[fdbId, address]` and `hpSwitchPortFdbAddrTable[fdbId, address]`; `HP-SN-SWITCH-GROUP-MIB::snFdbTable[stationIndex]` (Foundry lineage) |
| Comware | `HH3C-LswMAM-MIB`, `HH3C-MAC-INFORMATION-MIB` (MAC-change notification) |
| Huawei | `HUAWEI-L2MAM-MIB`, `HUAWEI-SWITCH-L2MAM-EXT-MIB`, `hwdbCfgFdbTable[mac, vlanId, vsiName]` — note the **VSI name** in the key, Huawei's bridge-domain dimension |
| Cisco SMB | `CISCOSB-rldot1q-MIB::rldot1qTpFdbTable [AUG dot1qTpFdbEntry]`, `rldot1qTpFdbCountTable[vlanTag, port, type]` |
| Aruba CX | `ARUBAWIRED-MACNOTIFY-MIB::macNotifyChangeTable[macAddress]` — change events, not the table |
| Ruckus ICX | `FOUNDRY-SN-CAM-MIB::snCamUsageL2Table[slot, processor, type]` — CAM *utilisation*, not entries |
| FASTPATH family | `agentSwitchMFDBTable[vlanId, mac, protocolType]` — the **multicast** FDB; unicast comes from Q-BRIDGE |
| HP BladeSystem | `fdbTable[fdbMacAddr]`, `fdbNewCfgStaticTable` |
| LANCOM SX GS2310 | `gs2310FilteringDataBaseStaticMACTable[index]`, `gs2310FilteringDataBaseDynamicMACTable[index]` — **index-keyed, not MAC-keyed**, so entries renumber |

### Traps and pitfalls

- **No timestamps.** The standard FDB has no age and no first/last-seen. Netdisco
  solves this with its own `node` table carrying `time_first`/`time_last`; that
  temporal layer is *the product*, and it lives outside the device model.
  FlowSeer's `FdbEntry` likewise has no time axis — correct for the device
  model, but the service above it will need one.
- MACs behind a LAG report the LAG's `dot1dBasePort`, not a member port.
- MACs learned on a trunk from a downstream switch are not endpoints. Every
  real "find the endpoint" implementation prunes ports that also carry
  LLDP/CDP neighbours — the join between [fdb](#fdb), [lldp](#lldp), and
  [arp-nd](04-ip.md#arp-nd) is the actual feature.
- Multicast FDB (`dot1qTpGroupTable`, `agentSwitchMFDBTable`) is a separate
  table with a separate key and is not the unicast FDB.

### FlowSeer status

`net/switching/v1/fdb_entry.proto`, `fdb_entry_kind.proto`,
`fdb_entry_status.proto` — with kind separated from current usability, which is
the right distinction (`dot1dTpFdbStatus` conflates them). Gaps: FID/bridge
dimension, no multicast FDB, no time axis.

---

## stp

**What it is.** Spanning tree: which ports forward, which block, who the root
is, and per-instance for MSTP/PVST.

### The four protocols

| Protocol | Standard | Instances | MIB |
|---|---|---|---|
| STP | 802.1D-1998 | 1 | `BRIDGE-MIB::dot1dStpPortTable[dot1dStpPort]` |
| RSTP | 802.1w | 1 | `RSTP-MIB::dot1dStpExtPortTable [AUG]` |
| MSTP | 802.1s / 802.1Q-2005 | N (CIST + MSTIs), VLANs mapped to instances | `MSTP-MIB::dot1sStpInstTable[instId]`, `dot1sStpInstPortTable[instId, port]`, `dot1sStpVlanTable[vlanIndex]`; IEEE's `IEEE8021-MSTP-MIB::ieee8021MstpTable[componentId, mstId]` |
| PVST+/RPVST | Cisco proprietary, widely cloned | one per VLAN | vendor MIBs only |

`openconfig-spanning-tree` covers all four: `stp/global`, `stp/mstp/mst-instances`,
`stp/rapid-pvst/vlan[vlan-id]`, `stp/rstp`, `stp/interfaces/interface[name]`
with `state/{role, port-state, edge-port, port-num, counters}`.

### Vendor mapping

| Family | Surface |
|---|---|
| IETF/IEEE | `BRIDGE-MIB`, `RSTP-MIB`, `MSTP-MIB`, `IEEE8021-SPANNING-TREE-MIB`, `IEEE8021-MSTP-MIB` (the corpus holds 7 dated revisions of each IEEE one) |
| Comware | `HH3C-LswMSTP-MIB`: `hh3cdot1sInstanceTable[instanceID]`, `hh3cdot1sPortTable[instanceID, portIndex]`, `hh3cdot1sVIDAllocationTable[mstVID]`, `hh3cdot1dStpPortXTable [AUG dot1dStpPortEntry]`; plus `HH3C-LswRSTP-MIB`, `HH3C-PVST-MIB` |
| Huawei | `HUAWEI-MSTP-MIB` — `hwMstpInstanceTable[instanceID]`, `hwMstpPortTable[instanceID, portIndex]`, `hwMstpVIDAllocationTable[VID]` (structurally identical to Comware's, shared H3C ancestry); plus `HUAWEI-VBST-MIB` (their PVST) |
| Aruba CX | `ARUBAWIRED-MSTP-MIB`, `ARUBAWIRED-RPVST-MIB` (`arubaWiredRpvstVlanTable[vlanId]`, `arubaWiredRpvstPortVlanTable[vlanId, portIndex]`) |
| Cisco SMB | `CISCOSB-BRIDGEMIBOBJECTS-MIB`: `rldot1dStpVlanTable[vlan]`, `rldot1dStpVlanPortTable[vlan, port]`, `rldot1sMstpInstanceVlanTable[vlanId, dbType]`, `rlBrgPvstVlanTable`; `rldot1dStpPortBpduGuardTable[dot1dBasePort]` |
| D-Link | `DLINKSW-STP-EXT-MIB::dStpExtPortTable[portNumber]`, `dStpExtMstpPortTable[mstId, portNum]` |
| HP ProCurve | `HP-ICF-BRIDGE::hpicfBridgeRstpPortTable[portIndex]`, `CONFIG-MIB::hpSwitchStpVlanTable[vlan]` |
| FASTPATH family | `agentStpMstPortTable[mstId, ifIndex]`, `agentPvrstpVlanTable[vlanIndex]`, `agentPvrstpPortVlanTable[portIndex, vlanIndex]` |
| LANCOM LCOS | `lcsStatusLanBridgeSpanningTreePortTableTable[Description]` — **keyed on a description string**; `...RstpPortTableTable`, `...LogTableTable[index]` |
| LANCOM LX | `lcosLXStatusLANSpanningTreePortTable[Port]` — STP on an access point |
| Ruckus ICX | `openconfig-spanning-tree` over RESTCONF |

### Traps and pitfalls

- **Port state is not port role.** `dot1dStpPortState` ∈ disabled/blocking/
  listening/learning/forwarding/broken; the *role* (root/designated/alternate/
  backup) is in RSTP-MIB and is the more useful diagnostic. Many collectors only
  read state.
- The `dot1dStpPort` key is a `dot1dBasePort`. Again.
- MSTP's VLAN→instance map (`dot1sStpVlanTable` /
  `hh3cdot1sVIDAllocationTable` / `hwMstpVIDAllocationTable`) is essential and
  frequently skipped; without it, per-instance state cannot be attributed to a
  VLAN.
- The root bridge id and the designated bridge id per port are what let you
  *infer topology* from STP — an alternative to LLDP that works on
  discovery-protocol-free networks. Netdisco and LibreNMS both exploit this.

### FlowSeer status

`spec/proto/flowseer/net/protocol/stp/v1/` is an empty `.gitkeep`. Nothing
modelled. Given the topology-inference value and the near-universal vendor
support, this is the highest-value unmodelled L2 protocol.

---

## loop-protect

**What it is.** Detecting a layer-2 loop by transmitting a probe frame and
seeing it return, then disabling the port. Distinct from
[udld](02-interface.md#udld) (unidirectional link) and from STP (which prevents
loops rather than detecting them after the fact).

Entirely vendor private. No standard.

| Family | Surface |
|---|---|
| Aruba CX | `ARUBAWIRED-LOOPPROTECT-MIB::arubaWiredLoopProtectPortTable[ifIndex]` |
| HP ProCurve | `HP-ICF-BRIDGE::hpicfBridgeLoopProtectPortTable[ifIndex]` |
| Cisco SMB | `CISCOSB-LBD-MIB::rlLbdPortTable[ifIndex]` |
| D-Link | `DLINKSW-LBD-MIB::dLbdIfCfgTable[index]`; `DLINKSW-BPDU-PROTECTION-MIB::dBpduProtectionIfTable[ifIndex]` |
| Huawei | four separate MIBs — `HUAWEI-LOOPDETECT-MIB::hwPortLoopDetectTable[ifIndex]`, `HUAWEI-LDT-MIB::hwLdtPortConfigTable[ifIndex]` + `hwLdtPortStatusTable[ifIndex, vlanIDIndex]` (per-VLAN!), `HUAWEI-MFLP-MIB` (MAC-flapping loop detection), `HUAWEI-L2IF-MIB::hwL2IfPortLoopDetectTable` |
| Comware | `HH3C-LPBKDT-MIB` |
| LANCOM SX | `lcsLoopProtectionConfigurationTable[Port]` + `lcsLoopProtectionStatusTable[Port]`; GS2310 line has its own pair |
| Cisco IOS-XE | `Cisco-IOS-XE-loop-detect` + `Cisco-IOS-XE-loop-detect-events` |

**Note the Huawei MAC-flapping variant**: detecting a loop by observing the same
MAC move between ports rapidly. That is derivable from FDB data FlowSeer already
plans to collect, which makes it a candidate for a *service-side* detection
rather than a device-reported entity.

Related but separate: BPDU guard / root guard / BPDU filter, which live in the
STP extension MIBs (`rldot1dStpPortBpduGuardTable`,
`DLINKSW-BPDU-PROTECTION-MIB`, `CISCOSB-SpecialBpdu-MIB`) and are STP port
policies, not loop detection.

---

## lldp

**What it is.** 802.1AB Link Layer Discovery Protocol — the neighbour table.
With 973 matched tables it is the single largest entity in the corpus, mostly
because IEEE ships many dated revisions.

### Canonical models

`LLDP-MIB` (the IETF-formatted version of 802.1AB-2005) is the one everyone
implements:

| Table | Key | Carries |
|---|---|---|
| `lldpLocPortTable` | `lldpLocPortNum` | local port id subtype/id/desc |
| `lldpLocManAddrTable` | `addrSubtype, addr` | our management addresses |
| `lldpRemTable` | `lldpRemTimeMark, lldpRemLocalPortNum, lldpRemIndex` | `lldpRemChassisIdSubtype`, `lldpRemChassisId`, `lldpRemPortIdSubtype`, `lldpRemPortId`, `lldpRemPortDesc`, `lldpRemSysName`, `lldpRemSysDesc`, `lldpRemSysCapSupported`, `lldpRemSysCapEnabled` |
| `lldpRemManAddrTable` | `+ addrSubtype, addr` | neighbour management addresses |
| `lldpRemUnknownTLVTable` | `+ tlvType` | TLVs the agent did not parse |
| `lldpRemOrgDefInfoTable` | `+ OUI, subtype, index` | organisationally-specific TLVs |
| `lldpPortConfigTable`, `lldpConfigManAddrTable` | | admin status per port |
| `lldpStatsTxPortTable` / `lldpStatsRxPortTable` | `lldpLocPortNum` | frame counters, TLV discards, ageouts |

**`lldpLocPortNum` is not `ifIndex`.** `lldpLocPortIdSubtype` tells you what the
local port id *means* — and when it is `interfaceAlias(1)`, `interfaceName(5)`,
or `local(7)` you have to resolve it. Some agents make `lldpLocPortNum` equal
`ifIndex`; some make it the `dot1dBasePort`; some make it a private index. There
is no portable rule. This is the LLDP equivalent of the PoE group/port problem
and it is worse because it silently produces a *plausible* wrong answer.

Extensions:

- `LLDP-EXT-DOT1-MIB` / `-V2-MIB` — port VLAN id, protocol VLANs, VLAN names,
  protocol identity, link aggregation (dot1 TLVs).
- `LLDP-EXT-DOT3-MIB` / `-V2-MIB` — MAC/PHY config-status (autoneg, speed,
  duplex advertised), power via MDI, link aggregation, max frame size.
- `LLDP-EXT-MED-MIB` — the ANSI/TIA-1057 media endpoint extension:
  capabilities, network policy (voice VLAN, DSCP, priority), location id (civic
  address, ELIN, coordinates!), extended PoE, and **inventory** (hardware rev,
  firmware, software, serial, manufacturer, model, asset id).
- `LLDP-EXT-DCBX-MIB`, EVB extensions, `LLDP-V2-LRP-EXT-MIB`.
- `LLDP-V2-MIB` (802.1AB-2009) re-keys everything with a *destination MAC
  address* dimension, because 802.1AB-2009 allows LLDP on three reserved
  multicast addresses. Barely implemented, present in the corpus in 4 revisions.

`openconfig-lldp`: `lldp/interfaces/interface[name]/neighbors/neighbor[id]` with
`state/{chassis-id, chassis-id-type, port-id, port-id-type, system-name,
system-description, management-address, ttl, age}` and a `custom-tlvs` bag.

`ieee802-dot1ab-lldp.yang` is the IEEE's own YANG (present upstream, not in this
corpus).

### Vendor mapping

Universal. The interesting variations:

| Family | Surface |
|---|---|
| Aruba wireless | `WLSX-RS-MIB::wlsxLldpNeighborTable[apMacAddress, remotePortNumber, neighborIndex]` — **the controller reports LLDP neighbours of its APs**, keyed by AP MAC. A controller-mediated neighbour table, which is a different entity shape entirely |
| Aruba CX | `ARUBAWIRED-LLDP-MIB::arubaWiredLldpXdot3LocPowerTable[lldpLocPortNum]`, `arubaWiredLldpXdot3RemPowerTable` — extended PoE over LLDP |
| Cisco SMB | `CISCOSB-LLDP-MIB::rlLldpAutoAdvLocPortManAddrTable`, plus the EEE-over-LLDP tables |
| Huawei | `HUAWEI-LLDP-MIB` |
| Comware | `HH3C-LLDP-EXT-MIB` |
| D-Link | `DLINKSW-LLDP-EXT-MIB`; the DGS-1210 line redefines `dlinklldpConfigManAddrTable` privately |
| LANCOM SX | `LLDP-MIB` + `LLDP-EXT-DOT3-MIB` + `LLDP-EXT-MED-MIB` unmodified |

### Traps and pitfalls

- **`lldpRemTimeMark` in the key** makes the remote table a TimeFilter table.
  Walking it returns every historical row unless you filter, and different
  agents interpret the filter differently. Most collectors walk from 0 and
  de-duplicate on `(localPortNum, remIndex)`.
- **Chassis id subtype matters.** `macAddress(4)` is common and comparable;
  `networkAddress(5)`, `local(7)`, and `interfaceName(6)` are not comparable
  across devices. Topology stitching must compare subtype-aware, and FlowSeer's
  `chassis_id_subtype.proto` + `chassis_id.proto` pair does exactly that.
- The port id has the same problem, and `local(7)` is common on APs and phones.
- `lldpRemSysCapEnabled` is a bitmap (repeater, bridge, wlanAccessPoint, router,
  telephone, docsisCableDevice, stationOnly, cVLANComponent, sVLANComponent,
  twoPortMACRelay) — the cheapest available answer to "what kind of thing is
  this neighbour", and worth keeping as a set rather than collapsing.

### FlowSeer status

`net/protocol/lldp/v1` is the best-developed protocol package in the repo:
`neighbor`, `chassis_id` + `chassis_id_subtype`, `port_id` + `port_id_subtype`,
`local_system`, `management_address` + `other_management_address`,
`system_capability`, `port_settings`, `port_admin_status`, `tlv_type`. Gaps:
no dot3 extension (advertised speed/duplex, power via MDI), no MED (network
policy, location, inventory), and no statement of the `lldpLocPortNum` →
interface resolution rule.

---

## cdp-like

**What it is.** The other discovery protocols. Modelling only LLDP means missing
neighbours on a surprising fraction of real networks.

| Protocol | Owner | Corpus evidence |
|---|---|---|
| CDP | Cisco | `CISCOSB-CDP-MIB`; `HUAWEI-CDP-COMPLIANCE-MIB` (Huawei speaks CDP for phone compatibility). `CISCO-CDP-MIB` itself is **missing** from the corpus |
| ISDP | Broadcom FASTPATH's CDP clone | `EdgeSwitch-ISDP-MIB`, `FASTPATH-ISDP-MIB` — Netgear/EdgeSwitch/LANCOM SX |
| FDP | Foundry | Ruckus ICX / HP-SN lineage |
| EDP | Extreme | not in corpus |
| SONMP / NDP | Nortel/Avaya | not in corpus |
| AMAP | Alcatel | not in corpus |
| PTOPO | IETF | `PTOPO-MIB` — RFC 2922, a *generic* physical topology MIB meant to unify all of the above. Essentially unimplemented, but its data model (`ptopoConnTable` keyed on `ptopoConnLocalSegmentIndex, ptopoConnIndex` with local/remote chassis+port and a `ptopoConnDiscAlgorithm` column naming the protocol that found it) is exactly the shape a multi-protocol neighbour entity should have |
| LLTD / Bonjour / mDNS / DDP | service discovery, not topology | `CISCOSB-BONJOUR-MIB`, `CISCOSB-1-BONJOUR-SERVICE-MIB`, `ARUBAWIRED-MDNS-MIB`, `FASTPATH-BONJOUR-MIB`, `DLINKSW-DDP-CLIENT-MIB`, `DLINKSW-NBF-MIB` |
| HGMP / Single-IP | cluster management that looks like discovery | `HUAWEI-HGMP-MIB`, `HH3C-HGMP-MIB`, `SINGLE-IP-MIB` |

### Recommendation

`PTOPO-MIB`'s `ptopoConnDiscAlgorithm` column is the prior art worth copying,
and it agrees with SNMP::Info's unified `c_*` methods and disagrees with
SuzieQ's separate LLDP and CDP tables. A single `Neighbor` message with a
`discovery_protocol` discriminator, carrying the subtype-tagged chassis and port
identifiers, generalises cleanly; per-protocol messages do not, because the
consumer question ("what is on this port") is protocol-blind.

FlowSeer currently has an LLDP-specific `neighbor.proto`. Whether that becomes
the general shape or gains a sibling is a decision worth taking before the
LLDP package stabilises.

---

## igmp-snooping

**What it is.** The switch learning which ports want which multicast groups by
watching IGMP/MLD, plus the router-port list and the querier.

### Canonical models

- `Q-BRIDGE-MIB::dot1qTpGroupTable[dot1qVlanIndex, dot1qTpGroupAddress]` — the
  multicast forwarding entries, and `dot1qForwardAllTable` /
  `dot1qForwardUnregisteredTable` for the router-port and flood behaviour.
- `IGMP-STD-MIB` / `MGMD-STD-MIB` (RFC 5519, unified IGMP+MLD) are about the
  *router* function, not snooping.
- There is **no standard snooping MIB**. `openconfig-igmp` covers the router
  side only.

### Vendor mapping

| Family | Surface |
|---|---|
| Aruba CX | `ARUBAWIRED-MGMD-SNOOPING-MIB::arubaWiredMgmdSnoopingVlanTable[vid, type]` — `type` discriminating IGMP from MLD, the cleanest design here |
| FASTPATH family | `agentSwitchSnoopingVlanTable[dot1qVlanIndex, protocol]`, `agentSwitchVlanStaticMrouterTable[ifIndex, vlanIndex, protocol]`, `agentSwitchSnoopingQuerierVlanTable[vlanIndex, protocol]`, `agentSwitchSnoopSSMFDBTable[groupAddrType, group, source, vlanIndex]` (SSM-aware) |
| Comware | `HH3C-LswIGSP-MIB`, `HH3C-MULTICAST-SNOOPING-MIB` |
| Huawei | `HUAWEI-L2MULTICAST-MIB`, `HUAWEI-MGMD-STD-MIB` |
| D-Link | `DLINKSW-MGMD-SNOOPING-MIB`, `DLINKSW-PIM-SNOOPING-MIB`, `DLINKSW-IPMCAST-EXT-MIB` |
| Cisco SMB | `CISCOSB-rlBrgMulticast-MIB`, `CISCOSB-rlBrgMcMngr-MIB`, `CISCOSB-MGMD-ROUTER-MIB` |
| HP ProCurve | `HP-ICF-MLD-MIB` |
| LANCOM SX GS2310 | `gs2310IGMPSnoopingVLANTable[vlanID]`, `gs2310MLDSnoopingVLANTable[vlanID]` |

The recurring shape is `[vlan, protocol]` for config and
`[vlan, group, (source)]` for entries. `protocol` as a key column (FASTPATH,
Aruba) rather than two parallel tables is the better design and worth copying.

---

## mvr

**What it is.** Multicast VLAN Registration — receivers in their own VLAN join
groups carried in a shared multicast VLAN, so the stream is not duplicated per
subscriber VLAN. An IPTV/access-network feature.

No standard. Consistent shape across the clones:

| Family | Surface |
|---|---|
| FASTPATH family | `FASTPATH-MVR-PRIVATE-MIB` / `EdgeSwitch-MVR-PRIVATE-MIB` |
| D-Link | `DLINKSW-MCAST-VLAN-MIB` |
| Huawei | `HUAWEI-BRAS-MVLAN-MIB::hwMulticastVlanTable[ifIndex]` |
| Comware | within `HH3C-MULTICAST-MIB` |

Low priority for enterprise; matters for the LANCOM/Netgear/Ubiquiti access
switch segment.

---

## storm-control

**What it is.** Rate limits on broadcast, multicast, and unknown-unicast (DLF)
traffic per port, with an action when exceeded.

No standard. `dot1qPortIngressFiltering` is unrelated.

| Family | Surface |
|---|---|
| Cisco SMB | `CISCOSB-STORMCTRL-MIB`; `CISCOSB-Dlf-MIB` (destination-lookup-failure flooding control separately) |
| Comware | `HH3C-STORM-CONSTRAIN-MIB` |
| D-Link | `DLINKSW-STORM-CTRL-MIB` |
| HP ProCurve | `HP-ICF-RATE-LIMIT-MIB`, `HP-CAR-MIB`, `HP-VLAN-CAR-MIB`, `HP-ICF-CONNECTION-RATE-FILTER` (a virus-throttle feature) |
| Ruckus ICX | `FOUNDRY-CAR-MIB`, `FOUNDRY-VLAN-CAR-MIB` |
| Cisco IOS-XE | `cisco-storm-control.yang` |
| Huawei | in `HUAWEI-QOS`/`HUAWEI-XQoS-MIB` |

Note the vocabulary split: "storm control" (Cisco/D-Link lineage) vs
"CAR — committed access rate" (Foundry/HP lineage) vs "storm constrain"
(H3C). Same feature, three names, and CAR overlaps with
[qos-policy](06-qos.md#qos-policy) policing.

---

## mirroring

**What it is.** Copying traffic from source ports/VLANs to a destination port,
locally (SPAN) or across the network (RSPAN/ERSPAN).

No standard MIB, no OpenConfig model (`openconfig-sampling` covers sFlow, not
mirroring).

| Family | Surface |
|---|---|
| Cisco SMB | `CISCOSB-SPAN-MIB`, `CISCOSB-MIR-MIB` |
| Comware | `HH3C-MIRRORGROUP-MIB` — the *mirror group* abstraction (a group with source list, destination, direction), which is the model most vendors converged on |
| Huawei | `HUAWEI-MIRROR-MIB` |
| D-Link | `DLINKSW-PACKET-MONITOR-MIB` |
| Ruckus ICX / HP-SN | mirroring objects inside `FOUNDRY-SN-SWITCH-GROUP-MIB` |
| RMON | `SMON-MIB::dataSourceCapsTable`, `portCopyTable` — the IETF's forgotten attempt (RFC 2613); `portCopyTable[portCopySource, portCopyDest]` is a real standard port-mirroring model that essentially nobody implements |

`SMON-MIB`'s `portCopyTable` is worth knowing about precisely because it shows
the standard existed and lost.

---

## port-isolation

**What it is.** Preventing ports in the same VLAN from talking to each other —
private VLANs (802.1Q-2011 PVLAN with primary/community/isolated), protected
ports, traffic segmentation.

| Family | Surface |
|---|---|
| D-Link | `DLINKSW-PRIVATE-VLAN-MIB` (full PVLAN), `DLINKSW-TRAFFIC-SEGMENT-MIB` (a simpler per-port forwarding mask) |
| Cisco SMB | `CISCOSB-ProtectedPorts-MIB` |
| Huawei | `HUAWEI-L2ISOLATE-MIB` |
| HP ProCurve | `CONFIG-MIB::hpSwitchPortIsolationConfigTable[port]` |
| LANCOM SX GS2310 | `gs2310VlanPortIsolationTable[Port]` |
| Comware | `HH3C-L2ISOLATE`-equivalent objects |
| IEEE | `IEEE8021-Q-BRIDGE-MIB` has PVLAN support in later revisions via the component model |

Two genuinely different mechanisms here: **PVLAN** (a VLAN-level construct with
primary/secondary relationships that propagates across trunks) and **protected
ports / traffic segmentation** (a local per-port forwarding mask that does not).
They should not share a message. D-Link is the only vendor in the corpus that
ships both and names them differently.

---

## garp

**What it is.** Dynamic VLAN registration — GVRP (802.1D GARP-based) and its
replacement MVRP (802.1ak MRP-based), plus MMRP for multicast.

| Family | Surface |
|---|---|
| IETF | `Q-BRIDGE-MIB::dot1qPortGvrpTable`, `dot1qPortGvrpStatus` |
| IEEE | `IEEE8021-MVRPX-MIB`, `IEEE8021-MIRP-MIB` (4 revisions each in the corpus) |
| Cisco SMB | `CISCOSB-GVRP-MIB` |
| D-Link | `DLINKSW-GVRP-MIB`, `DLINKSW-MVRP-MIB` |
| Aruba CX | `ARUBAWIRED-MVRP-MIB` |
| Comware | GARP objects in `HH3C-LswVLAN-MIB` |
| Ruckus ICX | `FOUNDRY-SN-MRP-MIB` — note: **Foundry MRP is Metro Ring Protocol, not IEEE MRP**. A name collision that will bite anyone matching on the string. See [ring-protection](#ring-protection) |

The observable that matters is already in
[vlan](#vlan): `dot1qVlanStatus = dynamicGvrp(3)` in `dot1qVlanCurrentTable`
tells you a VLAN was learned rather than configured. FlowSeer's
`VlanRegistration` covers this. A separate GVRP/MVRP protocol package is low
value.

---

## voice-vlan

**What it is.** A family of *automatic VLAN assignment* mechanisms: voice VLAN
(assign by OUI or LLDP-MED), MAC-based VLAN, protocol-based VLAN, subnet-based
VLAN, surveillance VLAN, auto-VoIP, smart ports.

These are frequently modelled as VLAN kinds. They are not — they are rules that
decide which VLAN a frame or a port lands in.

| Mechanism | Vendors | Surface |
|---|---|---|
| Voice VLAN by OUI | Comware `HH3C-VOICE-VLAN-MIB::hh3cvoiceVlanPortTable[ifIndex]`, Huawei `HUAWEI-VOICE-VLAN`, D-Link `DLINKSW-VOICE-VLAN-MIB`, Cisco SMB `CISCOSB-vlanVoice-MIB`, LANCOM SX `gs2310VoiceVLANPortTable`, Aruba wireless `WLSX-VOICE-MIB` | per-port + an OUI list |
| Surveillance VLAN | D-Link `DLINKSW-SURVEILLANCE-VLAN-MIB` | the same idea for cameras |
| Auto-VoIP | FASTPATH `FASTPATH-QOS-AUTOVOIP-MIB` / `EdgeSwitch-QOS-AUTOVOIP-MIB` | QoS-side, not VLAN-side |
| Auto-VLAN / auto-camera | FASTPATH `FASTPATH-QOS-AUTOVLAN-MIB::agentAutoCameraTable[intfIndex]` | |
| MAC-based VLAN | Ruckus ICX `FOUNDRY-MAC-VLAN-MIB::fdryMacBasedVlanTable[ifIndex, vlanId, mac]`, Cisco SMB `vlanMacBaseVlanPortTable[dot1dBasePort, groupId]`, Huawei `HUAWEI-MACBIND-MIB` | |
| Protocol-based VLAN | Comware `hh3cProtocolVlanPortTable[port, vlanId, protocolId]`, `Q-BRIDGE-MIB::dot1vProtocolGroupTable` + `dot1vProtocolPortTable` (**the standard one**, RFC 4363) | |
| Subnet-based VLAN | Comware `hh3cSubnetVlanPortCreateTable`, Ruckus/HP-SN `snVLanByIpSubnetTable[vlanId, ip, mask]`, LANCOM SX `lcsIPSubnetBasedVLANConfigTable` | |
| Super VLAN | D-Link `DLINKSW-SUPER-VLAN-MIB` | VLAN aggregation |
| Smart ports | Cisco SMB `CISCOSB-SMARTPORTS-MIB` | macro-driven port config |

**`dot1vProtocolGroupTable`/`dot1vProtocolPortTable` from Q-BRIDGE-MIB is the
one standard member of this family** and is worth noting because it shows the
general shape: a classification rule table plus a per-port binding.

For FlowSeer: these are *why* a port is in a VLAN, not *that* it is. The
observed membership already comes from
[vlan-membership](#vlan-membership). Modelling the rules is a config-plane
concern and can wait.

---

## ring-protection

**What it is.** Sub-second L2 ring recovery, faster than STP. Every vendor has
one and they do not interoperate.

| Protocol | Standard? | Vendors | Surface |
|---|---|---|---|
| ERPS (G.8032) | ITU-T | Huawei `HUAWEI-ERPS-MIB` (+ `hwErpsPortStatisticsTable`), D-Link `DLINKSW-ERPS-MIB` | the only standardised one |
| RRPP | H3C/Huawei | `HH3C-RRPP-MIB`, `HUAWEI-RRPP-MIB` | |
| Smart Link | H3C/Huawei | `HH3C-SMLK-MIB`, `HUAWEI-SMARTLINK-MIB` | an active/standby uplink pair, not a ring |
| Flex Links | Cisco/D-Link | `DLINKSW-FLEXLINKS-MIB` | same idea as Smart Link |
| MRP (Metro Ring Protocol) | Foundry | `FOUNDRY-SN-MRP-MIB` | **not** IEEE MRP |
| RPR (802.17) | IEEE | `IEEE-802DOT17-RPR-MIB`, `HH3C-RPR-MIB`, `HUAWEI-RPR-MIB` | dead technology, still in the MIBs |
| XRRP | HP | `HP-ICF-XRRP` | |

Low priority for FlowSeer's device classes, except that a port blocked by ERPS
or RRPP looks identical to a port blocked by nothing at all if you only read
`ifOperStatus`.

---

## macsec

**What it is.** 802.1AE link-layer encryption, with 802.1X-2010 MKA for key
agreement.

### Canonical models

`IEEE8021-SECY-MIB` (6 revisions in the corpus) — `secyIfTable[ifIndex]`,
`secyTxSCTable`, `secyTxSATable`, `secyRxSCTable`, `secyRxSATable`, and the
per-SA statistics. The Secure Channel / Secure Association hierarchy is the
model, and it is unusually faithful across implementations because the standard
is recent enough.

`openconfig-macsec` and `ieee802-dot1ae-secy.yang` are the YANG forms.

| Family | Surface |
|---|---|
| IEEE | `IEEE8021-SECY-MIB`, `IEEE8021-AE-PRY-MIB` |
| Huawei | `HUAWEI-MACSEC-MIB` |
| Comware | `HH3C-MACSEC-MIB` |
| LANCOM SX | ships `IEEE8021-SECY-MIB` unmodified |
| Aruba CX / Cisco IOS-XE | `openconfig-macsec` |

Relevant to FlowSeer only if encrypted uplinks are in scope. Worth one note:
a MACsec-protected link that fails key agreement goes *down* at L2 while the PHY
stays up — another `ifOperStatus`-only blind spot.

---

## fabric-overlay

**What it is.** VXLAN, EVPN, TRILL, SPB, VPLS, PWE3 — L2 extended over an L3 or
MPLS core.

| Technology | Standard model | Vendor evidence |
|---|---|---|
| VXLAN | `openconfig-evpn` `vxlan`, `openconfig-network-instance-l2` | `HH3C-VXLAN-MIB`, `HUAWEI-NVO3-MIB`, Cisco IOS-XE `nve-vxlan-encap-grouping` |
| EVPN | `openconfig-evpn`, `openconfig-evpn-types`, `openconfig-ethernet-segments` | `HH3C-EVPN-MIB`, `HH3C-BGP-EVPN-MIB`, `HUAWEI-EVPN-MIB` |
| TRILL | RFC 7176 family | `HUAWEI-TRILL-CONF-MIB` |
| SPB | `IEEE8021-SPB-MIB` (5 revisions) | `HH3C-SPB-MIB` |
| VPLS / VPWS | `PW-STD-MIB`, `PW-TC-STD-MIB` | `DLINKSW-VPLS-MIB`, `DLINKSW-VPWS-MIB`, `HH3C-L2VPN-MIB`, `HH3C-L2VPN-PWE3-MIB`, `HUAWEI-VPLS-MIB`, `HUAWEI-VPLS-EXT-MIB`, `HUAWEI-PWE3-MIB`, `cisco-pw.yang` |
| EVI | H3C's own | `HH3C-EVI-MIB::hh3cEviIfExtendVlanTable[ifIndex, vlanIndex]` |
| PBB | `IEEE8021-PBB-MIB` | `openconfig-evpn` `pbb`, `cisco-bridge-domain` `pbb` |

Out of scope for FlowSeer's stated device classes (enterprise switches, APs,
WLAN controllers) but relevant for one reason: **these are the technologies that
make the bridge-domain dimension mandatory rather than optional.** If a VSI or
EVI ever appears, VLAN-ID-keyed FDB entries stop being unique.

---

## l2pt

**What it is.** Layer-2 protocol tunnelling — carrying a customer's STP/CDP/LLDP
BPDUs transparently across a provider bridge by rewriting the destination MAC.

Thin: `DLINKSW-L2PT-MIB::dL2ptTunnelingMacTable[protocolType]` and Comware's
`hh3cQinQBpduTunnelTable[ifIndex, protocolIndex]`.

Worth one line in the atlas because it explains a confusing observation: a
switch that tunnels LLDP will show *no* LLDP neighbour on a link where the two
ends do see each other. Topology inference must know this is possible.

---

## jumbo-mtu

**What it is.** Maximum frame size.

Three separate numbers that get conflated:

1. **`ifMtu`** (IF-MIB) — the L3 payload MTU, per interface.
2. **Maximum frame size** — the L2 frame including headers and tags, per port or
   per system. `dot3StatsFrameTooLongs` counts violations.
3. **`dot1qMaxSupportedVlans`-adjacent frame limits** for tagged frames.

Vendor surfaces: `CISCOSB-JUMBOFRAMES-MIB`, `HP-ICF-JUMBO-MIB`,
`LLDP-EXT-DOT3-MIB::lldpXdot3LocMaxFrameSizeTable` /
`lldpXdot3RemMaxFrameSizeTable` (**the neighbour's max frame size, advertised —
which makes MTU mismatch detectable without touching the far end**), plus
per-vendor system-wide jumbo toggles.

The LLDP dot3 max-frame-size TLV is the interesting one: it turns an MTU
mismatch from an invisible packet-loss problem into a comparable pair of
observations. FlowSeer's LLDP package does not carry the dot3 extension yet.
