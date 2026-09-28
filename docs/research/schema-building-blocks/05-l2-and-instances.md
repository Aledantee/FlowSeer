---
title: Domain dossier — L2 technologies, protocols, and network instances
date: 2026-09-25
scope: network-instance/bridge-domain, STP/RSTP/MSTP, LACP, LLDP, CDP-like discovery, 802.1X/MAB, IGMP/MLD snooping, QinQ, private VLAN, VXLAN/EVPN, MACsec, ERPS, MVRP
---

# 1. Scope and sources

Repo paths read (line-cited where a fact is used below):

- `spec/proto/flowseer/net/switching/v1/*.proto` (all 14 files), `README.md`
- `spec/proto/flowseer/net/protocol/{lldp,stp,lacp}/v1/*.proto` (all files), `net/protocol/README.md`
- `docs/conventions/protobuf.md` (triad, ref pair, primitives-by-key, typed variants, enums)
- `docs/architecture/2026-08-20-network-model-structure-direction.md` (package tree, facets-vs-tables, "network instances... reserved for `net/routing`" note)
- `docs/research/network-domain-atlas/entities/03-switching.md` (full read) and `07-security.md` §dot1x/aaa/port-security (full read)
- `spec/mib/ieee/IEEE8021-BRIDGE-MIB.mib` (component id keys), `spec/mib/ieee/IEEE8021-Q-BRIDGE-MIB.mib`, `spec/mib/ieee/IEEE8021-MSTP-MIB.mib`, `spec/mib/ieee/IEEE8021-PAE-MIB`, `spec/mib/ieee/IEEE8021X-PAE-MIB-*.mib` (listed, not all opened — atlas 07-security already tabulates fields)
- `spec/mib/cisco/smb/CISCOSBCDP.mib` (opened; lacks a real neighbor-cache table)
- `spec/yang/openconfig/openconfig-network-instance-types.yang` (full identity list, lines 137–270)
- `spec/yang/openconfig/openconfig-network-instance.yang` (top-level list/key, lines 1–120, 373–420)
- `spec/yang/openconfig/openconfig-network-instance-l2.yang` (FDB/MAC-table groupings, lines 340–460, 520–930)
- `spec/yang/openconfig/openconfig-evpn-types.yang` (identities, lines 55–290)
- `spec/yang/openconfig/openconfig-igmp-types.yang` (full file, 64 lines)
- `spec/yang/openconfig/openconfig-vlan-types.yang` (TPID identities, VLAN/QinQ typedefs, lines 85–265)

Web (fetched for this dossier):

- RFC 7432 §7 — https://www.rfc-editor.org/rfc/rfc7432.html — EVPN NLRI route types 1–4
- RFC 4541 §2.1.1 — https://www.rfc-editor.org/rfc/rfc4541.html — IGMP/MLD snooping router-port detection and group-timeout recommendations
- RFC 8529 §3, §3.1, §3.1.1 — https://www.rfc-editor.org/rfc/rfc8529.html — `ietf-network-instance` name-keyed list and augmentation pattern
- Cisco `CISCO-CDP-MIB.my` — https://raw.githubusercontent.com/cisco/cisco-mibs/main/v2/CISCO-CDP-MIB.my — `cdpCacheTable`, `cdpInterfaceTable`, `cdpGlobal*` field list (not vendored; `CISCOSBCDP.mib` in the corpus is SMB-specific and has no neighbor table)

Not fetched / not verified for this dossier (flag as unverified below where relied on): IEEE 802.1Q-2022 clause text for S-VLAN TPID and dot1qVlanFdbId semantics beyond what the atlas and vendored MIBs already state; RFC 7348 VXLAN header layout (VNI is a well-known 24-bit field, cited from general knowledge only — **unverified**, no fetch for this dossier); RFC 9136 (EVPN IP Prefix route type 5) — **unverified**, not fetched, mentioned only because RFC 7432 itself stops at 4.

# 2. Standards facts

## Network instance (openconfig-network-instance-types.yang:137-270)

- `NETWORK_INSTANCE_TYPE` base identity, extended by:
  - `DEFAULT_INSTANCE` — "the 'default' or 'global' routing instance"
  - `L3VRF` — "private Layer 3 only routing instance... one or more RIBs"
  - `L2VSI` — "private Layer 2 only switch instance... one or more L2 forwarding tables"
  - `L2P2P` — "private Layer 2 only forwarding instance... point to point connection between two endpoints"
  - `L2L3` — "private Layer 2 and Layer 3 forwarding instance"
- `ENDPOINT_TYPE` → `LOCAL` / `REMOTE` (used for L2P2P/VPWS endpoints; LOCAL also doubles as the VXLAN tunnel endpoint interface).
- `ENCAPSULATION` → `MPLS`, `VXLAN` ("VXLAN (RFC7348) VNIs to distinguish network instances on the wire").
- `SIGNALLING_PROTOCOL` → `LDP`, `BGP_VPLS` (RFC 4761), `BGP_EVPN` (RFC 7432).
- Top-level list: `network-instances/network-instance[name]` (openconfig-network-instance.yang:382-393) — **string name is the sole key**, no separate integer id.
- `ietf-network-instance` (RFC 8529 §3): same shape — name-keyed list, no fixed type enum in the base module; §3.1 defines a `ni-type` choice as the augmentation anchor and §3.1.1 three "well-known mount points" (`vrf-root`, `vsi-root`, `vv-root`) other modules attach under. OpenConfig's closed identity enum and IETF's open-choice-augmentation are two different extensibility strategies for the same "one instance, one type" shape.

## Bridge component identity (IEEE8021-BRIDGE-MIB.mib:114-140, 367-400, 685-710; corroborated by atlas 03-switching "bridge-domain problem")

- Every IEEE8021-BRIDGE-MIB / IEEE8021-Q-BRIDGE-MIB table is indexed first by `IEEE8021PbbComponentIdentifier` (`ieee8021BridgeBaseComponentId`, `ieee8021BridgeBasePortComponentId`, `ieee8021BridgeTpPortComponentId`), then by port or FDB id.
- Legacy `BRIDGE-MIB` (RFC 4188) and `Q-BRIDGE-MIB` (RFC 4363) have **no component dimension** — one bridge per SNMP agent, and VLAN→FDB fan-in is only through `dot1qVlanFdbId` (many-to-one, the SVL/IVL map).
- Atlas verdict (03-switching.md, "bridge-domain problem" section, cites the MIB keys directly): "the component-aware MIB [is] a minority" among real devices; FlowSeer will mostly read component-less data and must state its default explicitly.

## STP / RSTP / MSTP

- FlowSeer's `net/protocol/stp/v1` (10 files) already models STP/RSTP faithfully against `BRIDGE-MIB`/`RSTP-MIB` (every field's proto comment cites a `BRIDGE-MIB:<line>` or `RSTP-MIB:<line>` locant) — verified by direct read, not the atlas summary.
- **MSTP is entirely absent.** Atlas (03-switching.md, "stp" section) states MSTP needs: `dot1sStpInstTable[instId]`, `dot1sStpInstPortTable[instId, port]`, `dot1sStpVlanTable[vlanIndex]` (the VLAN→instance map — "essential and frequently skipped"), and the IEEE-native `IEEE8021-MSTP-MIB::ieee8021MstpTable[componentId, mstId]`. PVST+/RPVST are Cisco-proprietary, vendor-MIB only, no IETF/IEEE standard.
- Existing `BridgeId` (`stp/v1/bridge_id.proto`) and `ProtocolVersion` enum have no MSTI dimension; MSTP needs a per-instance bridge/port state keyed by `(mst_id, ...)` plus the VID→MSTI map.

## LACP

- FlowSeer's `net/protocol/lacp/v1` (7 files) is complete against `IEEE8023-LAG-MIB` (every numeric field cites a MIB locant) — verified by direct read.

## LLDP

- FlowSeer's `net/protocol/lldp/v1` (13 files) covers the core `LLDP-MIB` shape (local system, port, neighbor, management address, TLV type, capabilities) faithfully — verified by direct read.
- Gaps per atlas (03-switching.md "lldp" section): no 802.3 (dot3) extension — advertised speed/duplex/autoneg, power-via-MDI, **max-frame-size** (the atlas flags this specifically: "turns an MTU mismatch from an invisible packet-loss problem into a comparable pair of observations"); no LLDP-MED (network policy — voice VLAN/DSCP/priority, location, inventory).

## CDP (Cisco `CISCO-CDP-MIB.my`, fetched from github.com/cisco/cisco-mibs; not vendored in `spec/mib/`)

- `cdpCacheTable` (neighbor cache) columns: `cdpCacheIfIndex`, `cdpCacheDeviceIndex`, `cdpCacheAddressType`/`cdpCacheAddress` (`CiscoNetworkProtocol`/`CiscoNetworkAddress`), `cdpCacheVersion` (DisplayString — the CDP protocol version banner text, not a version number), `cdpCacheDeviceId`, `cdpCacheDevicePort`, `cdpCachePlatform`, `cdpCacheCapabilities` (`OCTET STRING (0..4)` bitmap), `cdpCacheVTPMgmtDomain`, `cdpCacheNativeVLAN` (`VlanIndex`), `cdpCacheDuplex` (`unknown|halfduplex|fullduplex`), `cdpCacheApplianceID`, `cdpCacheVlanID`, `cdpCachePowerConsumption`, `cdpCacheMTU`, `cdpCacheSysName`, `cdpCacheSysObjectID`, primary/secondary management address pairs, `cdpCachePhysLocation`, `cdpCacheLastChange`.
- `cdpInterfaceTable`: per-port enable, message interval, group, port, name.
- `cdpGlobal*`: run, message interval, hold time, device-id, device-id format (serial number / MAC address / other).
- The vendored `CISCOSBCDP.mib` (Cisco Small Business line) has **no neighbor-cache table at all** — only counters and a `PortList` for duplex-mismatch logging. It cannot serve as the CDP neighbor model; the real `CISCO-CDP-MIB` must be sourced externally as done here.

## 802.1X / PAE (atlas 07-security.md "dot1x" section, citing `IEEE8021X-PAE-MIB`)

- Core tables: `dot1xPaePortTable` (capabilities/init/reauth), `dot1xAuthConfigTable` (`dot1xAuthAuthControlledPortStatus` ∈ authorized/unauthorized — the actual answer; `dot1xAuthAuthControlledPortControl` ∈ forceAuthorized/auto/forceUnauthorized), `dot1xAuthStatsTable` (EAPOL counters), `dot1xAuthDiagTable` (state-machine transition counters), `dot1xAuthSessionStatsTable` (`dot1xAuthSessionUserName`, octets, duration, terminate cause).
- **The base MIB is single-supplicant-per-port.** Every vendor in the corpus adds a private multi-session table keyed `(port, mac)` or `(port, mac, vlan)`: HP ProCurve `hpicfDot1xSMAuthConfigTable[paePort,macAddr]`, Aruba CX `arubaWiredPortAccessClientTable[portName,mac]` + `arubaWiredPortAccessRoleTable[roleName]` (cleanest — client row carries an assigned role), D-Link `dDot1xExtAuthStatsTable[portNumber,macAddr,vlanId]`, Huawei `hwDot1xSessionDisplayByMacTable[userMac]`.
- MAB (MAC authentication bypass) and web-auth/captive-portal are vendor-private extensions layered on the same port-authorization result; no IEEE/IETF standard.
- `openconfig` has no 802.1X model at all; only Ruckus augments its own tree (`icx-openconfig-aaa-aug`).

## IGMP/MLD snooping

- **No standard snooping MIB exists** (atlas 03-switching.md "igmp-snooping"). `Q-BRIDGE-MIB::dot1qTpGroupTable[dot1qVlanIndex, dot1qTpGroupAddress]` is the closest standard artifact (multicast forwarding entries) plus `dot1qForwardAllTable`/`dot1qForwardUnregisteredTable` (router-port/flood behavior). `IGMP-STD-MIB`/`openconfig-igmp` model the router function, not the switch's snooping function.
- RFC 4541 §2.1.1 (fetched): router-port detection is one of three methods — MRDISC solicitation, snooping IGMP Queries whose source is not `0.0.0.0`, or manual configuration; group-membership state "must not rely exclusively on... Leave announcements" and needs a timeout mechanism mirroring router-side behavior.
- Recurring vendor shape (atlas): config keyed `[vlan, protocol]` (protocol ∈ IGMP/MLD as a column, not two parallel tables — Aruba CX and FASTPATH do this cleanly), membership entries keyed `[vlan, group, (source)]` (FASTPATH is SSM-aware and keys on `(groupAddrType, group, source, vlanIndex)`).
- `openconfig-igmp-types.yang` (full file read): only two typedefs exist — `igmp-version` (`uint8`, range 1-3, "v1 = RFC1112, v2 = RFC2236, v3 = RFC3376") and `igmp-interval-type` (`uint16`, range 1-1024, units seconds, query interval, RFC3376 §8.2 p.40). No snooping-specific types in OpenConfig at all — confirms the "no standard" finding.

## QinQ / provider bridging (802.1ad)

- `openconfig-vlan-types.yang:85-265`: `TPID_TYPES` identity base with concrete values `TPID_0X8100`, `TPID_0X88A8`, `TPID_0X9100`, `TPID_0X9200`, and `TPID_ANY` — confirming the atlas's "the TPID is configurable" trap (0x8100 802.1Q, 0x88a8 802.1ad standard, 0x9100/0x9200 vendor legacy values seen in the wild). Also defines `vlan-id`, `vlan-range`, `qinq-id`, `qinq-id-range`, `vlan-mode-type`, `vlan-ref`, `vlan-stack-action` typedefs (not read in full detail — flag for planner if QinQ config modeling is prioritized).
- Standard MIB: `IEEE8021-PB-MIB` (802.1ad), keys `[componentId, port, localVid]` / `[componentId, port, cVid]` / `[componentId, port, sVid]` (atlas 03-switching.md "qinq"). 802.1ah (PBB, MAC-in-MAC) is a separate, deeper stack (`IEEE8021-PBB-MIB`) — not the same as QinQ.
- FlowSeer's existing `VlanTagStack` (`vlan_tag_stack.proto`) — ordered, uncapped list of `VlanTag` (each carrying its own TPID) — already matches this shape structurally; nothing to change there.

## VXLAN / EVPN

- Encapsulation type is a first-class `ENCAPSULATION` identity in `openconfig-network-instance-types.yang` (`VXLAN`, citing RFC 7348 by name in its description).
- `openconfig-evpn-types.yang:55-290`: `EVPN_TYPE` base → `VLAN_BASED`, `VLAN_BUNDLE`, `VLAN_AWARE` (service models); `EVPN_REDUNDANCY_MODE` → `SINGLE_ACTIVE`, `ALL_ACTIVE`; `EVPN_CAPABILITY` → `NVE`, `EVI`, `MAC_VRF`, `IP_VRF`, `IRB`. Also MAC-table-adjacent enums: MAC type `Static`/`Dynamically learned`/`Connected`; MAC origin `Local`/`Remote`/`All`; next-hop encapsulation `not set`/`VXLAN`/`Invalid`; learning mode `Control Plane Learning`/`Data Plane Learning`.
- RFC 7432 §7 (fetched): defines exactly four EVPN NLRI route types — **1** Ethernet Auto-Discovery (A-D), **2** MAC/IP Advertisement, **3** Inclusive Multicast Ethernet Tag, **4** Ethernet Segment. (Route type 5, IP Prefix Advertisement, comes from RFC 9136 — **unverified**, not fetched for this dossier; do not cite RFC 7432 for it.)
- `openconfig-network-instance-l2.yang:340-460`: FDB-adjacent groupings — `l2ni-fdb-mac-config` (`mac-learning` bool, `mac-aging-time` uint16 seconds, `maximum-entries` uint16); `l2ni-mac-table-state.entry-type` enum `STATIC`/`DYNAMIC` only (simpler two-value taxonomy than FlowSeer's five-value `FdbEntryKind`); `evi` leaf of type `oc-evpn-types:vni-id` links a MAC-table row to its EVPN instance. Lines 520-930 hold a much larger `l2rib` (L2 RIB) table shape for EVPN-learned MAC and MAC-IP entries with per-producer next-hop state — out of scope for a first pass, flagged for the planner as "exists if VXLAN/EVPN is ever prioritized."

## MACsec, ERPS, MVRP (all confirmed low-priority / no change needed this pass — atlas only)

- MACsec: `IEEE8021-SECY-MIB` (vendored, 6 revisions) models Secure Channel/Secure Association hierarchy (`secyIfTable`, `secyTxSCTable`/`secyTxSATable`, `secyRxSCTable`/`secyRxSATable`) — atlas flags the operational trap that a MACsec link failing key agreement goes down at L2 while PHY stays up.
- ERPS (G.8032): ITU-T, only Huawei/D-Link vendor MIBs (`HUAWEI-ERPS-MIB`, `DLINKSW-ERPS-MIB`); no IETF/IEEE model. Atlas flags: an ERPS-blocked port is indistinguishable from any other blocked port via `ifOperStatus` alone.
- MVRP: covered by `dot1qVlanStatus = dynamicGvrp(3)` already inside `VlanRegistration`'s dynamic case (atlas: "FlowSeer's VlanRegistration covers this... a separate GVRP/MVRP protocol package is low value").

# 3. Provider data matrix

| Concept | Core across vendors? | Field shape (source) |
|---|---|---|
| Network instance name+type | Core (every YANG/NMS surveyed keys on name; type varies OpenConfig closed enum vs IETF open augmentation) | `name` (string, key) + `type` (DEFAULT_INSTANCE / L3VRF / L2VSI / L2P2P / L2L3) |
| Bridge/component id | Niche in the wild (atlas: minority of vendors ship the component-aware MIB) but structurally required by the standard | `IEEE8021PbbComponentIdentifier` (uint) |
| VLAN table | Core | id (1-4094), name, registration — already modeled |
| Switchport membership | Core | PVID, tagged/untagged sets, frame admission — already modeled |
| FDB row | Core | (vlan\|fid, mac) → interface, kind, status — already modeled, FID/VID conflation is the only gap |
| STP/RSTP | Core (near-universal vendor support per atlas) | already modeled |
| MSTP | Core on enterprise switches (Comware/Huawei/Aruba CX/Cisco SMB/D-Link/HP/FASTPATH all have it; structurally identical MIB shape across H3C-lineage vendors) | instance id, per-instance bridge/port state, VID→MSTI map — **unmodeled** |
| LACP | Core | already modeled |
| LLDP core | Core (universal) | already modeled |
| LLDP dot3/MED extension | Common but optional | advertised speed/duplex, PoE-via-MDI, max-frame-size, network-policy, location, inventory — **unmodeled** |
| CDP | Niche (Cisco-only, but Huawei speaks it for phone compat) | DeviceId, Address, DevicePort, Platform, Capabilities (bitmap), VTPMgmtDomain, NativeVLAN, Duplex, PowerConsumption — **unmodeled**, no FlowSeer package |
| 802.1X port state | Core (near-universal on managed switches) | port control mode, controlled-port-status (authorized/unauthorized), one session row per (port, mac) minimum | 
| MAB | Common fallback, vendor-private | mac, vlan, port |
| IGMP/MLD snooping | Core on managed switches, zero standard model | `[vlan, protocol]` config, `[vlan, group, source?]` membership, router-port list |
| QinQ / S-VLAN | Common on provider-facing gear, niche on access switches | TPID (0x8100/0x88a8/0x9100/0x9200), C-VID, S-VID — already modeled via `VlanTagStack`/`VlanTag` |
| Private VLAN / port isolation | Niche, two distinct mechanisms (atlas insists they not share a message) | primary/secondary VLAN roles vs. local forwarding-mask isolation |
| VXLAN/EVPN | Niche for FlowSeer's stated device classes (enterprise switch/AP/WLC), but the reason the bridge-domain gap is mandatory once present | VNI, VTEP, EVI, route type — **unmodeled**, no urgency per direction doc ("net/routing is reserved") |
| MACsec | Niche (encrypted uplinks only) | SC/SA hierarchy — **unmodeled**, low priority |
| ERPS | Niche (ring topologies, ITU-T not IETF/IEEE) | ring/instance state — **unmodeled**, low priority |

# 4. Proposed primitives

All new packages are ref-free (device-local names only), per `docs/conventions/protobuf.md` "Primitives refer to peers by key, never by ref."

## 4.1 `net/instance/v1` (new package — network-instance / bridge-domain dimension)

This is the one genuinely new *layer* package this domain needs; everything else is either an existing package's gap or a new protocol package.

- `NetworkInstance` message: `name` (string, required, min_len 1 — matches every YANG surveyed keying on name, not an integer), `kind` enum (see below), `route_distinguisher` (string, optional — RD text form, only meaningful for L3VRF/L2VSI with BGP signalling; leave unset otherwise).
- `NetworkInstanceKind` enum, FlowSeer-normalized (no registry backing it — OpenConfig's identities are not integers): `NETWORK_INSTANCE_KIND_UNSPECIFIED = 0`, `_DEFAULT = 1` (mirrors `DEFAULT_INSTANCE`), `_L3VRF = 2`, `_L2VSI = 3`, `_L2P2P = 4`, `_L2L3 = 5`. Cite openconfig-network-instance-types.yang:144-176 in the enum comment.
- **How FDB rows carry it**: add an optional `NetworkInstance` reference to `FdbEntry` — but per the primitives-refer-by-key rule this must be `string network_instance_name` (not a message), defaulting-absent meaning "the device's single default bridge" (explicit, as the atlas recommends), present meaning "this row belongs to a named instance, e.g. an L2VSI or an EVI-backed VLAN." Do **not** invent a bridge component id primitive yet — the component-id dimension is a MIB artifact most vendors don't expose (atlas), whereas `network_instance_name` is what a source that *does* separate bridge domains (VSI, EVI, VRF-lite) will actually report. This is a deliberate divergence from the raw IEEE8021-Q-BRIDGE-MIB shape in favor of the OpenConfig/vendor convergence.
- Same `network_instance_name` string field should be added anywhere else that currently assumes "the device's one bridge": `Vlan` (a VLAN can exist per-instance in a multi-VSI device) is a candidate but the planner should weigh whether this widens `net/switching/v1` scope too far in one pass — flagged as an open question (§7).
- `net/routing` remains reserved for RIBs/routes per the direction doc; `net/instance/v1` should hold only identity + kind + encapsulation/signalling hints, not routing tables.

## 4.2 `net/protocol/mstp/v1` (new package)

Given the "protocols own their packages" rule and MSTP's near-universal vendor support (H3C/Huawei/Aruba CX/Cisco SMB/D-Link/HP/FASTPATH all structurally identical per atlas), model as:

- `InstanceState` (device-wide per MSTI, analogous to `stp.v1.BridgeState` but keyed by instance): `mst_id` (uint32, 0 = CIST, per IEEE8021-MSTP-MIB `ieee8021MstpTable[componentId, mstId]`), `bridge_id`, `designated_root`, `root_path_cost`, `root_port_interface_name` — reuse `stp.v1.BridgeId` by importing `net/protocol/stp/v1` (protocol packages may import layer primitives, not each other, per the boundary doc — **check this against the "never another protocol package" rule**: MSTP importing STP's `BridgeId` needs planner sign-off since `BridgeId` is protocol-owned, not a layer primitive; alternative is duplicating a `BridgeId`-shaped message inside `mstp/v1`).
- `InstancePortState`: `mst_id`, `interface_name`, `role` (reuse `stp.v1.PortRole`? same import question), `state` (reuse `stp.v1.ForwardingState`), `path_cost`, `designated_*` fields mirroring `stp.v1.PortState` per-instance.
- `VlanMapping`: `mst_id` → `repeated uint32 vlan_ids` (the VID→MSTI map the atlas calls "essential and frequently skipped"). This is the one message every collector needs and none of the per-instance state is useful without it.
- Cite `IEEE8021-MSTP-MIB` (vendored, multiple dated revisions) and `dot1sStpVlanTable`/`hwMstpVIDAllocationTable`/`hh3cdot1sVIDAllocationTable` as the cross-vendor shape (atlas).

## 4.3 `net/protocol/dot1x/v1` (new package)

- `PortState` (analogous shape to lacp/stp `PortState`, deliberately no `Config`/`Event` — settings ride with state per the existing protocol packages' pattern): `interface_name`, `admin_control` enum (`FORCE_AUTHORIZED`/`AUTO`/`FORCE_UNAUTHORIZED`, mirrors `dot1xAuthAuthControlledPortControl`), `controlled_port_status` enum (`AUTHORIZED`/`UNAUTHORIZED`, mirrors `dot1xAuthAuthControlledPortStatus` — **the field every consumer wants**), reauth/quiet/tx timers as `google.protobuf.Duration`.
- `Session` message, keyed `(interface_name, mac)` — **not** per-port singular, because atlas found every vendor without exception adds a multi-session table. Fields: `interface_name`, `mac` (`net/addr/v1/Eui48Address`), `method` enum (`DOT1X`/`MAB`/`WEB_AUTH` — FlowSeer-normalized, no registry), `vlan_id` (optional uint32, the assigned VLAN result), `role_name` (optional string, mirrors Aruba CX's `arubaWiredPortAccessRoleTable` — "the cleanest expression of the modern model" per atlas), `username` (optional string), `octets`/`duration`/`terminate_cause` if a counters/session-close view is wanted (defer to planner — this starts pulling in AAA-adjacent scope).
- This is the highest-value net-new protocol package per the atlas's own verdict ("quietly the most valuable" in 07-security.md) because joining it with `FdbEntry` turns a bare MAC into an identified endpoint.

## 4.4 Existing-package gaps to close (not new packages)

- `net/protocol/lldp/v1`: add a dot3-extension facet (advertised link speed/duplex/autoneg, power-via-MDI, **max frame size** — flagged by atlas as detecting MTU mismatch without touching the far end) and consider LLDP-MED network-policy/location/inventory as separate optional sub-messages on `Neighbor`, following the existing `ManagementAddress`-style oneof/optional pattern. Cite `LLDP-EXT-DOT3-MIB`/`-V2-MIB` and `LLDP-EXT-MED-MIB`.
- `net/switching/v1/fdb_entry.proto`: add `network_instance_name` (see §4.1) and state explicitly in the file's doc comment that the current `(vlan_id, mac)` key assumes independent VLAN learning (IVL) — the atlas calls this out as an implicit assumption that should be stated, not changed.

## 4.5 IGMP/MLD snooping — no new package recommended yet

No standard model exists to normalize against (RFC 4541 describes router behavior expectations, not a wire/MIB schema; OpenConfig's `igmp-types` has only two scalar typedefs). Recommend the planner treat this as **lower priority than MSTP/802.1X** until a multicast-focused dossier needs it — flagged as an open question in §7 rather than proposed as a package here, to avoid guessing a shape with no cross-vendor convergence beyond "`[vlan, protocol]` config + `[vlan, group, source?]` membership."

## 4.6 CDP / discovery-protocol generalization — decision needed, not proposed here

The dossier's "one-neighbor-entity question" is answered by the atlas with a clear recommendation (PTOPO-MIB's `ptopoConnDiscAlgorithm` shape: one `Neighbor`-like message with a `discovery_protocol` discriminator) but this dossier does **not** propose replacing the existing LLDP-specific `lldp.v1.Neighbor` — that decision affects a landed package and belongs to the planner with the tradeoff stated plainly in §7, not decided unilaterally in a domain dossier.

# 5. Entity candidates (model/)

No new UUID-identified entity is warranted by this domain research. Every concept above is device-local, protocol, or bridge-scoped state that fits the existing net/ Primitive shape (ref-free, keyed by device-local name/id). If a future `NetworkInstance` needs to be addressable across devices (e.g., for an EVPN service spanning many switches, an operator-defined VRF/VSI catalog), that would become a `model/` entity with its own ref pair — but nothing in the current corpus or standards shows FlowSeer needs that yet. Flag as an open question (§7) rather than propose prematurely.

# 6. Traps

- **FID vs VID conflation** (atlas, verified against Q-BRIDGE-MIB key structure): under SVL, `dot1qVlanFdbId` maps many VLANs to one FID; a collector that ignores this silently assumes IVL. FlowSeer's `FdbEntry` already bakes in the IVL assumption — must be stated in the file comment, not silently changed.
- **Bridge/component id is a minority feature** — do not make `NetworkInstance`/bridge-component a required field anywhere; absence must mean "the device's single default bridge," matching the atlas's explicit recommendation.
- **`lldpLocPortNum`, `dot1dBasePort`, `dot1xPaePortNumber`, `ifIndex` are four different port-numbering spaces** that frequently coincide but are not guaranteed to. FlowSeer's one-interface-identity rule (device-local `name`) already sidesteps this for its own schema, but any new protocol package (MSTP, 802.1X) must key by `interface_name`, never re-introduce a numeric port index, consistent with existing STP/LACP/LLDP packages.
- **CISCOSBCDP.mib (vendored) is not a substitute for CISCO-CDP-MIB** (github, not vendored) — it has no neighbor-cache table. Anyone modeling CDP later must fetch the real MIB, not assume the vendored SMB variant covers it.
- **RFC 7432 defines only 4 EVPN route types (1-4).** Do not cite it for route type 5 (IP Prefix) — that's RFC 9136, unverified/unfetched for this dossier.
- **VXLAN VNI / header layout claims are unverified** for this dossier — RFC 7348 was not fetched. Do not let the planner treat the `VXLAN` identity's existence in OpenConfig as evidence of VNI field width, etc.
- **Foundry "MRP" is Metro Ring Protocol, not IEEE MRP** (atlas, explicit warning) — a name collision that will bite string-based protocol matching if ERPS/ring-protection or MVRP/GARP packages are ever built from vendor MIB names alone.
- **Private VLAN and protected-ports/traffic-segmentation are two different mechanisms** that must not share one message (atlas, D-Link is the only vendor with both, named differently) — relevant if port-isolation is scoped into a future pass.
- **MSTP importing STP types crosses the "a protocol package... never [imports] another protocol package" rule** (`net/protocol/README.md` Boundaries) literally as written. This is a real tension the planner must resolve: either MSTP duplicates `BridgeId`/`PortRole`/`ForwardingState`-shaped messages, or the boundary rule gets an explicit, planner-approved exception for MSTP-extends-STP (802.1Q formally defines MSTP as an extension of RSTP, unlike LACP/LLDP which are independent protocols) — do not resolve this silently in schema code.

# 7. Open questions for the planner

1. Should `network_instance_name` be added to `Vlan` as well as `FdbEntry`, or only to FDB rows for this pass? Widening `net/switching/v1`'s scope touches a stable, well-regarded package.
2. Does MSTP get to import `net/protocol/stp/v1` types (`BridgeId`, `PortRole`, `ForwardingState`), or does the "no protocol imports another protocol" rule hold and MSTP duplicates equivalent messages? (§6 trap)
3. Is the CDP-vs-LLDP "one generic Neighbor with a discovery_protocol discriminator" refactor worth taking before `lldp.v1` stabilizes further, given it would change a landed, well-developed package? The atlas recommends it; this dossier surfaces the tradeoff without deciding it.
4. Is 802.1X/MAB in scope for this pass at all, or does it belong with a future AAA-focused dossier (RADIUS/TACACS+ servers, method ordering) since `dot1x.v1.Session.role_name`/`username` already brushes against AAA territory?
5. IGMP/MLD snooping has zero standard model to normalize against — does the planner want a FlowSeer-invented shape now (`[vlan, protocol]` + `[vlan, group, source?]`, the closest thing to cross-vendor convergence) or defer until a multicast-specific dossier exists?
6. VXLAN/EVPN: the direction doc reserves `net/routing` for RIBs/routes and this domain's device classes (enterprise switch/AP/WLC) rarely run VTEPs. Confirm this stays out of scope for this pass; if it comes in later, RFC 7348 and RFC 9136 need dedicated fetches this dossier did not do.
