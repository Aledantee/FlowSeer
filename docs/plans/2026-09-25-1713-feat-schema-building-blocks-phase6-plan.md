---
title: Schema Building Blocks Phase 6, L2 Protocols - Plan
type: feat
date: 2026-09-25
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept
compound: no lesson
execution: code
parent: docs/plans/2026-09-25-1713-feat-schema-building-blocks-plan.md
---

# Schema Building Blocks Phase 6, L2 Protocols - Plan

> Implemented. 4 units, 2026-09-26T09:36Z to 2026-09-26T09:45Z. Each unit
> ran its targeted `Verify:` command; no `--full` run, which builds and
> race-tests `generated/go/yang` and exhausts host memory. The
> verification-dirty marker still holds the Bash-mutation line `buf
> generate` leaves, which only `--full` clears; every targeted run
> regenerated `generated/go/proto` and diffed it clean.

## Goal

The L2 protocols the targeted switches run and FlowSeer cannot yet hold
get their tables: MSTP instances and the VLAN-to-instance map in
`net/protocol/stp`, the IEEE 802.3 and LLDP-MED extensions in
`net/protocol/lldp`, CDP neighbors in `net/protocol/cdp`, and IGMP/MLD
snooping membership in `net/multicast`. The means is new rows and enums in
those four packages, each pinned by validation tests whose fixtures carry
values decoded from published captures, plus the README and allowlist
lines the conformance gates hold them to.

Stop condition: a targeted source reports an MSTI's VLAN allocation that
maps one VLAN to two instances in the same bridge, or a snooping source
whose member port cannot be resolved to an interface name. Either would
mean the row keys chosen here are wrong, and the phase stops for a
re-cut before it lands.

## Decisions

- **The CIST follows the MIB; the VLAN map spells it 0.** An MSTP bridge's
  CIST stays in the landed `BridgeState` and `PortState`, which gain the
  CIST-only MSTP values, and the new `MstInstance` and `MstPort` rows hold
  MSTIs 1 to 4094 only. The VLAN map row `MstVlanMap` accepts `mst_id` 0
  to 4094, with 0 meaning the CIST. Why: IEEE8021-MSTP-MIB splits the CIST
  across `ieee8021MstpCistTable` and the IEEE8021-SPANNING-TREE-MIB, whose
  common parameters (root, root cost, root port, timers) are exactly what
  `BridgeState` already carries from BRIDGE-MIB
  (`spec/mib/ieee/IEEE8021-MSTP-MIB-202211080000Z.mib:104-150`), while
  `ieee8021MstpTable` indexes MSTIs by `IEEE8021MstIdentifier`, `1..4094`
  (`spec/mib/ieee/IEEE8021-TC-MIB:317`). Spelling the CIST as instance 0
  there would put the same tree in two rows. The map is different: the
  current `ieee8021MstpVlanV2MstId` is `Unsigned32 (0..4095)`
  (`IEEE8021-MSTP-MIB-202211080000Z.mib:1289`) because every VLAN is
  allocated to some tree and 0 is the CIST; the deprecated V1 table, typed
  `1..4094`, could not say so. 4095 marks SPVIDs on an SPT bridge (same
  object's description); SPB is not a targeted feature, so the row rejects
  it.
- **MSTP reuses `BridgeId`, `PortRole`, and `ForwardingState` from the
  same package.** Why: the record's rule 6 puts MSTP in `protocol/stp`
  for this reason. `ProtocolVersion` gains `PROTOCOL_VERSION_MSTP = 3`
  (`ieee8021SpanningTreeVersion mstp(3)`,
  `spec/mib/ieee/IEEE8021-SPANNING-TREE-MIB-201412150000Z.mib:378`), and
  `PortRole` gains `PORT_ROLE_MASTER = 6`, the MSTI role IOS-XE reports as
  `stp-master` (`spec/yang/cisco/iosxe/2611/Cisco-IOS-XE-spanning-tree-oper.yang:116`).
  The IEEE MIB's `ieee8021MstpPortRole` has no master value
  (`IEEE8021-MSTP-MIB-202211080000Z.mib:965`), so a MIB-only source never
  reports it.
- **An MSTI bridge identifier keeps only the settable priority.** The
  landed `BridgeId.priority` rule accepts multiples of 4096. The mapper
  keeps the four most significant bits (`ieee8021MstpBridgePriority`,
  `0..61440`, "the most significant 4 bits",
  `IEEE8021-MSTP-MIB-202211080000Z.mib:379`) and drops the 12-bit system
  ID extension, which IEEE 802.1Q fills with the MSTID for an MSTI (the
  standard text is not fetched here; the MIB's range is the enforced
  evidence). The extension equals the row's `mst_id`, so nothing is lost.
  The existing rule rejects an unmasked value, and a test pins that.
- **MSTP rows and `BridgeState` carry a required `network_instance`.**
  Why: the record's rule 4 requires one on every table whose key is not
  scoped by an interface. `MstInstance` (keyed by `mst_id`) and
  `MstVlanMap` qualify, and the map's VLAN ids are instance-scoped the way
  `Vlan`'s are. `BridgeState` is the CIST row beside the MSTI rows and
  carries the same key, so a consumer joins the two by value rather than
  by assuming one of them. This is the record's network instance, not the
  PBB bridge component that indexes every IEEE8021-MSTP-MIB table
  (`ieee8021Mstp*ComponentId`, `IEEE8021-MSTP-MIB-202211080000Z.mib:157`,
  `:265`, `:1260`), which rule 4 keeps out of every key; a mapper reading
  that MIB drops the component and names the instance, `default` when the
  device has none. `PortState` and `MstPort` are interface-scoped and do
  not repeat it. This breaks the landed `BridgeState`; its one producer,
  `src/common/netsim/vswitch/netmodel/export.go`, names its single
  instance `default`.
- Ruled: `PortState` gains no `network_instance`; only `BridgeState`,
  `MstInstance`, and `MstVlanMap` carry it, as the decision above says.
  Why: the port rows are interface-scoped. Cost if wrong: one field and the
  netmodel port fixtures.
- **The VLAN-to-MSTI map is one row per instance with a unique VLAN
  list.** Why: every CLI configures it per instance, and the MIB's MSTI
  row carries the same set as the `ieee8021MstpVids0..3` bitmaps
  (`IEEE8021-MSTP-MIB-202211080000Z.mib:390-444`). A VLAN listed in two
  rows of one bridge is a device inconsistency that no single row can see;
  the README states it and no schema rule covers it.
- **The LLDP extensions are neighbor-side only.** `Neighbor` gains an
  `Ieee8023Extension` (MAC/PHY configuration, power via MDI, maximum frame
  size) and a `MedExtension` (capabilities and device class, network
  policies, inventory, locations, extended power via MDI). Why: the local
  values a port announces in these TLVs are its own auto-negotiation, MAU
  type, PoE state, and MTU, which `EthernetFacet`, `PoeFacet`, and
  `Interface.mtu` already hold; the neighbor side is new information, and
  the maximum frame size is what turns an MTU mismatch into a comparable
  pair (atlas `entities/03-switching.md`, "lldp"). The 802.3 link
  aggregation TLV is left out: the V2 MIB moved it to the 802.1 extension
  (`spec/mib/ieee/LLDP-EXT-DOT3-V2-MIB-200906080000Z.mib:133`), which this
  phase does not add.
- **The extensions reuse `net/phy`, `net/switching`, and `net/packet`.**
  The advertised capability bitmap is `ifMauAutoNegCapAdvertisedBits`
  (`spec/mib/ieee/LLDP-EXT-DOT3-MIB:523-531`), whose positions
  `MauLinkMode` already mirrors; the operational MAU type is `MauType`;
  the PSE/PD class is `PoeRole`; the MED priority is `PoePriority`; the
  policy priority takes the `vlan_pcp` rule; the DSCP is `IpDscp` with the
  `ip_dscp` rule. The policy VLAN does not take `vlan_tag_vid`: the MIB
  admits 4095, "reserved for implementation use"
  (`spec/mib/ieee/LLDP-EXT-MED-MIB:985-998`), and rejecting a value a
  device really sent would fail the whole neighbor row (record rule 3,
  `docs/solutions/architecture-patterns/decode-failure-blast-radius-in-generated-walks.md`),
  so it carries `uint32.lte = 4095`. The layering allowlist
  already permits all of these for a protocol row
  (`test/conformance/proto/layering_test.go:18-24`).
- **MED enums carry the TLV's integers, not the MIB's.** The MIB's
  `LocationSubtype` numbers civic address 3
  (`spec/mib/ieee/LLDP-EXT-MED-MIB:164-188`); the TLV's location data
  format numbers it 2 (Wireshark `epan/dissectors/packet-lldp.c:1074-1080`
  at commit `16c09df25f262e2e1b432521aba7eb6a3ea4afe3`). The TLV is the
  wire every source decodes, so `MedLocationFormat` uses its values and a
  MIB mapper subtracts one; the README names the offset.
- **CDP is not vendored; it is cited from files the repository already
  tracks.** The CISCO-CDP-MIB belongs in `spec/mib/cisco/enterprise/`, a
  gitignored clone of `github.com/cisco/cisco-mibs` restored by the
  command in `spec/mib/README.md` (Refresh, "Cisco enterprise"), so
  vendoring it into the tracked tree would contradict that layout. Its
  SMIv2-to-YANG translation is tracked at
  `spec/yang/cisco/iosxe/2611/MIBS/CISCO-CDP-MIB.yang`, with the
  `cdpCacheEntry` list at lines 419-740, and the schema cites those lines
  plus the upstream URL
  `https://github.com/cisco/cisco-mibs/blob/main/v2/CISCO-CDP-MIB.my`.
  The capability bit positions, which the MIB leaves to "the CDP
  specification", cite Wireshark `epan/dissectors/packet-cdp.c:1355-1397`
  at the same pinned commit.
- **The CDP row is keyed by `(local_interface_name, device_id)`.** Why:
  both sources key by an agent-local number: the MIB by
  `cdpCacheDeviceIndex`, and IOS-XE by a `uint32 device-id`, "Device
  number of this device"
  (`spec/yang/cisco/iosxe/2611/Cisco-IOS-XE-cdp-oper.yang:279-283`,
  `:473-474`), which means nothing off the agent. The key is FlowSeer's
  own: the Device-ID TLV string, `cdpCacheDeviceId` in the MIB
  (`CISCO-CDP-MIB.yang:493`) and `device-name` in IOS-XE
  (`Cisco-IOS-XE-cdp-oper.yang:284-289`), is what the neighbor calls
  itself, so `device_id` holds that string and is required. A mapper never
  puts IOS-XE's numeric `device-id` into it. Non-IP address families (CLNS, DECnet, IPX,
  which IOS-XE lists at `Cisco-IOS-XE-cdp-oper.yang:425-440`) have no
  FlowSeer address type and are dropped; no targeted device runs them.
- **Snooping membership is one per-port row for IGMP and MLD, marked
  FlowSeer-normalized.** `net/multicast/v1.GroupMembership` is keyed by
  `(network_instance, vlan_id, group, source?, interface_name)`. Why: no
  standard models it (dossier 05, RFC 4541 describes behavior). Aruba CX,
  D-Link, Cisco SMB, and FASTPATH hold IGMP and MLD in one table with an
  address-type column, which the atlas recommends copying
  (`entities/03-switching.md:553-555`); the address family of `group`
  plays that column here. Filter mode is router state per group per
  attached network (RFC 3376 §6.2.1, RFC 3810 §7.2.1), which is per port on
  a switch, so the row is per interface: Aruba's group-port cache
  (`spec/mib/aruba/cx/ARUBAWIRED-MGMD-SNOOPING-MIB:1166-1172`) and D-Link's
  group table (`spec/mib/dlink/DLINKSW-MGMD-SNOOPING-MIB:682-687`) index
  that way, and a PortList source expands its bitmap. The row requires
  `network_instance` for the reason the record's rule 4 gives `FdbEntry`
  one: it carries a `vlan_id` and joins to a `Vlan` row by that pair. Router ports,
  per-VLAN snooping settings, and querier state are separate tables in
  every source and stay out.
- **No union neighbor message** (record rule 6): LLDP and CDP neighbors
  stay in their own packages.
- **Fixtures come from published captures, decoded here.** No lab device
  holds a CDP neighbor (`docs/research/device-inventory/lab/labsw05-cisco-sg220.md:169`),
  so the tests use the Wireshark sample captures `cdp_v2.pcap` (sha256
  `90ec1ad708be84af0844a45e4e2f7ef6bdd014d72da215568d496231d4f87e4b`),
  `lldp.detailed.pcap` (sha256
  `480e6123969cc1084b7d41a54ff0947e0bffe72a4ce025664d5fb872998809dc`), and
  `lldpmed_civicloc.pcap` (sha256
  `d24a236b6e4ff04d29fb90f8b1448f5bff0007c6d3eafd3958c71a77c2d9869e`) from
  `https://wiki.wireshark.org/SampleCaptures`, decoded with tcpdump for
  this plan. The captures are not copied into the repository; each test
  names the capture and hash and hard-codes the decoded values. tcpdump
  4.99 reads the MED extended-power octet `0x03` as a PD; Wireshark's
  dissector masks the type from bits 7-6 (`packet-lldp.c:1041-1047`,
  `3740-3745`), which gives PSE, the sender's role as a PoE switch. The
  plan takes Wireshark's reading. No mapper fills these rows in this phase
  (parent, Out of scope), so a fixture proves the decoded values fit the
  rows, not that a mapper produces them.

- Ruled: the `Duration` fields of `cdp.v1.Neighbor` and
  `GroupMembership` (`time_to_live`, `uptime`, `expires_in`) also reject a
  negative span (`duration.gte = {}`), as the LLDP neighbor's `time_to_live`
  does. Why: no source reports a negative hold time or age. Cost if wrong:
  one rule per field.
- Ruled: the rule tests of all four units share `fieldCase` and
  `runFieldCases` in `stp_rules_test.go`, which assert the violated field
  path, or the rule id for a message-level rule, rather than valid or
  invalid alone. Why: a message rejected by another rule would otherwise
  pass for the one under test. Cost if wrong: four test files.

## Requirements

1. An MSTI row outside 1 to 4094 fails. Example: `MstInstance{mst_id: 4095}`
   and `MstInstance{mst_id: 0}` each fail on `mst_id`; `mst_id: 1` and
   `mst_id: 4094` with the other required fields pass. The same holds for
   `MstPort.mst_id`.
2. A VLAN-to-MSTI map row rejects a repeated VLAN, an out-of-range VLAN,
   and the SPVID marker. Example: `MstVlanMap{mst_id: 1, vlan_ids: [10, 10]}`
   fails (`repeated.unique`); `vlan_ids: [4095]` fails; `mst_id: 4095`
   fails; `mst_id: 0, vlan_ids: [1]` passes.
3. An MSTI bridge identifier with its system ID extension still set fails.
   Example: `MstInstance.bridge_id.priority = 32769` fails with the
   existing `bridge_id.priority` rule; `32768` passes.
4. `BridgeState`, `MstInstance`, and `MstVlanMap` without
   `network_instance` fail with `network_instance: value is required`.
   `MstPort` without `interface_name` fails.
5. A CDP neighbor carrying the values of `cdp_v2.pcap` validates. Example:
   `local_interface_name "GigabitEthernet1/0/1"`, `device_id "myswitch"`,
   `port_id "FastEthernet0/1"`, `platform "cisco WS-C2950-12"`, capability
   mask `0x00000028` as `[CAPABILITY_SWITCH, CAPABILITY_IGMP]`,
   `vtp_management_domain "MYDOMAIN"`, `native_vlan_id 1`, `duplex
   ETHERNET_DUPLEX_FULL`, `addresses` and `management_addresses`
   `[192.168.0.253]`, `time_to_live 180s`, `protocol_version 2`, and the
   capture's 272-octet `software_version` passes; so does
   `appliance_vlan_id 4095`. The same row without `device_id` fails; with
   `native_vlan_id 4095` fails; with a 33-octet `vtp_management_domain`
   fails; with `appliance_vlan_id 4096` fails.
6. An LLDP neighbor carrying the 802.3 TLVs of `lldp.detailed.pcap`
   validates. Example: `auto_negotiation_supported` and `_enabled` true;
   bitmap `0x6c00` as `advertised_link_modes [MAU_LINK_MODE_BASE_T_MBPS10,
   MAU_LINK_MODE_BASE_T_MBPS10_FULL, MAU_LINK_MODE_BASE_TX_MBPS100,
   MAU_LINK_MODE_BASE_TX_MBPS100_FULL]` (bit 0 is the most significant bit
   of the first octet); `operational_mau_type.iana 16`; `power_via_mdi
   {port_class POE_ROLE_PSE, supported true, enabled true,
   pair_control_capable false, pairs POWER_PAIRS_SIGNAL}` with no
   `power_class`, because the TLV's class octet is 0 and the MIB's range is
   1 to 5; `max_frame_size_bytes 1522`. `max_frame_size_bytes 65536` fails;
   `power_class 5` fails.
7. An LLDP neighbor carrying the MED TLVs of `lldpmed_civicloc.pcap`
   validates. Example: `capabilities_supported [MED_CAPABILITY_CAPABILITIES,
   MED_CAPABILITY_NETWORK_POLICY, MED_CAPABILITY_LOCATION,
   MED_CAPABILITY_EXTENDED_PSE]` (mask `0x000f`); `device_class
   MED_DEVICE_CLASS_NETWORK_CONNECTIVITY`; one network policy
   `{application_type MED_APPLICATION_TYPE_VOICE, tagged true, unknown
   false, vlan_id 50, priority 6, dscp IP_DSCP_EF}`; one location
   `{format MED_LOCATION_FORMAT_CIVIC_ADDRESS, info}` whose 41 octets start
   `28 02 55 53`; power `{role POE_ROLE_PSE, priority POE_PRIORITY_LOW,
   power_nanowatts 6500000000}`. Two policies with the same application
   type fail; a policy `vlan_id 4095` passes and `4096` fails;
   `power_nanowatts 102300000001` fails; a location `info` of 257 octets
   fails; a 33-octet `serial_number` fails.
8. A snooping membership row accepts a multicast group of either family
   and rejects anything else. Example: `{network_instance "default",
   vlan_id 10, group 239.1.1.1, interface_name "1/1/5"}` passes; group
   `10.0.0.1` fails; group `ff02::1:3` passes; group `239.1.1.1` with an
   IPv6 `source` fails; an IPv6 group with `compatibility_version 3` fails;
   the row without `network_instance` fails.
9. The conformance gates hold the new shapes: `TestImportOrder`,
   `TestProtoReadmeImports`, `TestProtoReadmeCoverage`,
   `TestKeyFieldsUseKeyRules`, `TestCanonicalUnitSuffixes`, and
   `TestEveryDeclaredProtoPackageIsLinked` pass with
   `net/protocol/cdp/v1` and `net/multicast/v1` present.

## Out of scope

- Mappers that fill any of these rows from SNMP, YANG, or a CLI (parent
  plan, Out of scope). `src/modules/localnet/snmpmap/lldp.go` keeps
  compiling unchanged; the new `Neighbor` fields are additive.
- PVST and Rapid-PVST instances: Cisco-proprietary and outside the record's
  tree.
- CIST port-level MSTP additions (`ieee8021MstpCistPortTable` regional
  root, internal path cost, restricted role and TCN, disputed): no
  consumer needs them yet, so `PortState` stays as landed.
- LLDP local-side extension tables, the 802.1 organizational TLVs (PVID,
  VLAN names), and the 802.3 link aggregation TLV.
- CDP local settings (`cdpInterfaceTable`, `cdpGlobal*`), non-IP CDP
  address families, and CDP-over-Huawei compatibility blobs
  (`HUAWEI-CDP-COMPLIANCE-MIB` carries the TLVs undecoded).
- Snooping router ports, per-VLAN snooping settings, querier state, and
  per-VLAN statistics.
- Decoding LLDP or CDP frames off the wire.

## Units

### U1. MSTP instances, ports, and the VLAN map in `net/protocol/stp`

Files: `spec/proto/flowseer/net/protocol/stp/v1/{protocol_version.proto,port_role.proto,port_state.proto,bridge_state.proto,mst_config_id.proto,mst_instance.proto,mst_port.proto,mst_vlan_map.proto,README.md}`,
`generated/go/proto/flowseer/net/protocol/stp/v1/`,
`spec/proto/flowseer/net/protocol/README.md`,
`spec/proto/flowseer/net/switching/v1/README.md`,
`test/conformance/proto/stp_rules_test.go` (new),
`test/conformance/proto/field_constraint_class_test.go`,
`src/common/netsim/vswitch/netmodel/{export.go,stp_test.go,testdata_icx7150_test.go,trust_test.go}`,
`CONCEPTS.md`
After: none

Change:

- `ProtocolVersion` gains `PROTOCOL_VERSION_MSTP = 3`; `PortRole` gains
  `PORT_ROLE_MASTER = 6`, each commented with the source above. The
  `PortRole` enum comment stops saying roles are assigned by RSTP and
  names RSTP and MSTP; `PortState.oper_protocol_version`'s comment
  (`port_state.proto:69-70`) stops saying "RSTP otherwise" and names the
  version the port transmits, MSTP included.
- `mst_config_id.proto` declares `MstConfigId`, the MST configuration
  identifier (IEEE 802.1Q 13.8 per the MIB's REFERENCE): `name = 1`
  (`string.max_bytes = 32`, `SnmpAdminString (SIZE(32))` counts octets,
  `IEEE8021-MSTP-MIB-202211080000Z.mib:1363`), `revision_level = 2`
  (`uint32.lte = 65535`, `:1373`), `digest = 3` (`bytes.len = 16`, `:1383`).
  Every field is optional; absence means unreported.
- `BridgeState` gains `network_instance = 15` (required,
  `network_instance_name`), `mst_config_id = 16`, `cist_regional_root = 17`
  (`BridgeId`, `:201`), `cist_internal_root_path_cost = 18`
  (`uint32.lte = 2147483647`, `:211`), and `max_hops = 19` (`gte 6`,
  `lte 40`, `:224`). The four MSTP fields say "Absent unless the bridge
  runs MSTP and reported it."
- `mst_instance.proto` declares `MstInstance`: `network_instance = 1`
  (required, rule), `mst_id = 2` (required, `gte 1`, `lte 4094`),
  `bridge_id = 3` (required), `designated_root = 4` (the root of this
  MSTI, which is the regional root, `:348`), `root_path_cost = 5`,
  `root_port_interface_name = 6` (`interface_name` rule), `topology_change
  = 7` (bool), `topology_changes = 8` (uint64), `time_since_topology_change
  = 9` (Duration). Each field cites its `ieee8021Mstp*` object line
  (`:306`-`:369`).
- `mst_port.proto` declares `MstPort`: `mst_id = 1` (required, 1..4094),
  `interface_name = 2` (required, rule), `role = 3`, `state = 4`,
  `priority = 5` (`lte 240`, `:903`), `path_cost = 6` (`gte 1`,
  `lte 200000000`, `:914`), `admin_path_cost = 7` (`lte 200000000`, zero is
  the automatic default, `:990`), `designated_root = 8` (the regional
  root component of the MSTI port priority vector, not the CIST root that
  `PortState.designated_root` holds, `:924`), `designated_cost = 9` (the
  internal root path cost component, `:934`), `designated_bridge = 10`, `designated_port = 11` (`lte 65535`),
  `disputed = 12` (`:979`).
- `mst_vlan_map.proto` declares `MstVlanMap`: `network_instance = 1`
  (required, rule), `mst_id = 2` (required, `lte 4094`, 0 is the CIST,
  `:1289`), `vlan_ids = 3` (repeated, `unique`, items `vlan_id` rule). An
  empty list means the instance has no VLANs allocated.
- None of the new messages carries a `State`, `Config`, or `Event` suffix,
  so the proto hook's family check does not apply (conventions doc,
  "Primitives refer to peers by key").
- The package README moves MSTP from "Deliberately absent" to the body:
  the CIST/MSTI split and why, the map's 0, the system ID extension the
  mapper masks, and that no row catches one VLAN in two map rows. PVST
  stays absent. Sources add `IEEE8021-MSTP-MIB-202211080000Z` and the
  IOS-XE operational model. `MstVlanMap` takes the `vlan_id` rule by
  `import option` of `net/switching`, which `scanImports` counts
  (`test/conformance/proto/layering_test.go:567`), so the stp README's
  `Imports:` becomes `net/addr, net/key, net/switching`,
  `net/protocol/README.md`'s `Imports:` becomes the same, and
  `net/switching/v1/README.md`'s `Imported by:` gains `net/protocol/stp`.
- `field_constraint_class_test.go` drops its blank import of the stp
  package, since `stp_rules_test.go` now links it and the comment above the
  imports says the list holds packages with no example test.
- `netmodel/export.go` sets `network_instance` to `default` on the
  exported `BridgeState`, and every `BridgeState` fixture in the
  package's tests sets it too: `stp_test.go` (built and validated at
  `:199`, `:328`, `:388`, `:468`, `:532`, `:622`, `:725`, `:791`),
  `testdata_icx7150_test.go:319`, and `trust_test.go` (`validBridgeState()`
  and the builder at `:447`, validated through `loadInput.validate`). `netmodel.go` keeps rejecting any
  version but RSTP (`netmodel.go:1278`), so MSTP needs no Go change there.
- `CONCEPTS.md`, Network model, gains "Spanning tree instance": the CIST
  is the bridge's own tree and every MSTI is a numbered row beside it, and
  a VLAN belongs to exactly one of them.

Tests: `stp_rules_test.go` holds Requirements 1 to 4 as `validationCase`
tables (`validation_rules_test.go:10`): `TestMstInstanceRules`,
`TestMstPortRules`, `TestMstVlanMapRules`, `TestBridgeStateRequiresNetworkInstance`,
including the `priority = 32769` case. `netmodel` tests pass with the
instance set; `go test -race ./src/common/netsim/vswitch/netmodel/...`.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/protocol/stp/v1 generated/go/proto/flowseer/net/protocol/stp/v1 spec/proto/flowseer/net/protocol/README.md spec/proto/flowseer/net/switching/v1/README.md test/conformance/proto/stp_rules_test.go test/conformance/proto/field_constraint_class_test.go src/common/netsim/vswitch/netmodel CONCEPTS.md`

### U2. IEEE 802.3 and LLDP-MED neighbor extensions in `net/protocol/lldp`

Files: `spec/proto/flowseer/net/protocol/lldp/v1/{neighbor.proto,ieee8023_extension.proto,power_via_mdi.proto,power_pairs.proto,med_extension.proto,med_capability.proto,med_device_class.proto,med_network_policy.proto,med_application_type.proto,med_inventory.proto,med_location.proto,med_location_format.proto,med_power.proto,med_power_source.proto,README.md}`,
`generated/go/proto/flowseer/net/protocol/lldp/v1/`,
`spec/proto/flowseer/net/protocol/README.md`,
`spec/proto/flowseer/net/{phy,switching,packet}/v1/README.md`,
`test/conformance/proto/lldp_rules_test.go`
After: U1 (U1 edits the `net/protocol/README.md` `Imports:` line and
the `net/switching/v1/README.md` `Imported by:` line this unit edits)

Change:

- `Neighbor` gains `ieee8023 = 12` (`Ieee8023Extension`) and `med = 13`
  (`MedExtension`). Absence of either means the neighbor sent none of that
  organization's TLVs.
- `Ieee8023Extension` (remote tables of `spec/mib/ieee/LLDP-EXT-DOT3-MIB`):
  `auto_negotiation_supported = 1`, `auto_negotiation_enabled = 2` (`:494-495`);
  `advertised_link_modes = 3` (repeated `flowseer.net.phy.v1.MauLinkMode`,
  `unique`; the TLV carries 16 bits, so only positions 0 to 15 occur,
  `:523`); `operational_mau_type = 4` (`MauType`; a source's 0 means not
  listed and leaves it absent, `:536`); `power_via_mdi = 5`
  (`PowerViaMdi`); `max_frame_size_bytes = 6` (uint64, `lte 65535`, `:758`).
- `PowerViaMdi` (`lldpXdot3RemPowerTable`, `:563-653`): `port_class = 1`
  (`PoeRole`), `supported = 2`, `enabled = 3`, `pair_control_capable = 4`,
  `pairs = 5` (`PowerPairs`), `power_class = 6` (uint32, `lte 4`: the IEEE
  802.3 class number, which is the MIB's `1..5` minus one, `:653`).
  `PowerPairs` is FlowSeer-normalized, `_UNSPECIFIED = 0`, `SIGNAL = 1`,
  `SPARE = 2`, the RFC 3621 `pethPsePortPowerPairs` integers.
- `MedExtension` (`spec/mib/ieee/LLDP-EXT-MED-MIB` remote tables):
  `capabilities_supported = 1` and `capabilities_current = 2` (repeated
  `MedCapability`, `unique`, `:880-914`); `device_class = 3`
  (`MedDeviceClass`, `:916`); `network_policies = 4` (repeated
  `MedNetworkPolicy`); `inventory = 5` (`MedInventory`); `locations = 6`
  (repeated `MedLocation`); `power = 7` (`MedPower`). Message-level CEL
  rules reject two policies with one `application_type` and two locations
  with one `format`, since the MIB indexes both tables by them
  (`:956-959`, `:1202-1205`).
- `MedCapability` is pass-through: the integers are the
  `LldpXMedCapabilities` bit positions, `CAPABILITIES = 0` through
  `INVENTORY = 5` (`:119-159`), and zero is real, as in
  `SystemCapability`. `MedDeviceClass` is pass-through with
  `NOT_DEFINED = 0` through `NETWORK_CONNECTIVITY = 4` (`:88-115`).
- `MedNetworkPolicy` (`:935-1015`): `application_type = 1`
  (`MedApplicationType`, required), `unknown = 2`, `tagged = 3`,
  `vlan_id = 4` (`uint32.lte = 4095`: 0 is priority-tagged, 4095 is
  reserved for implementation use, `:985-998`), `priority = 5`
  (`vlan_pcp` rule), `dscp = 6` (`IpDscp`, `ip_dscp` rule).
  `MedApplicationType` is FlowSeer-normalized with `_UNSPECIFIED = 0` and
  the TLV's values `VOICE = 1` through `VIDEO_SIGNALING = 8`
  (`packet-lldp.c:796-807`; the MIB's `PolicyAppType` bits use the same
  positions, `:192-239`).
- `MedInventory` (`:1064-1097`): seven strings, each `max_bytes 32`
  (`SnmpAdminString (SIZE(0..32))` counts octets): `hardware_revision`,
  `firmware_revision`, `software_revision`, `serial_number`,
  `manufacturer_name`, `model_name`, `asset_id`.
- `MedLocation` (`:1181-1225`): `format = 1` (`MedLocationFormat`,
  required), `info = 2` (bytes, `max_len 256`, the location data after the
  format octet). `MedLocationFormat` is FlowSeer-normalized:
  `_UNSPECIFIED = 0`, `COORDINATE_BASED = 1`, `CIVIC_ADDRESS = 2`,
  `ELIN = 3`, the TLV's values; the comment states the MIB is one higher.
- `MedPower` (`:1242-1467`): `role = 1` (`PoeRole`; a source's unknown or
  none leaves it absent), `source = 2` (`MedPowerSource`), `priority = 3`
  (`PoePriority`), `power_nanowatts = 4` (uint64, `lte 102300000000`: the
  field is `0..1023` tenths of a watt, `:1331`; a PSE's value is power
  available, a PD's is power requested). `MedPowerSource` is
  FlowSeer-normalized: `_UNSPECIFIED = 0`, `PSE_PRIMARY = 1`,
  `PSE_BACKUP = 2`, `PD_FROM_PSE = 3`, `PD_LOCAL = 4`,
  `PD_LOCAL_AND_PSE = 5` (`:1344`, `:1442`).
- The lldp README gains the two extensions, the neighbor-only decision,
  the MIB-versus-TLV location offset, the tenths-of-a-watt conversion, and
  the Wireshark reading of the power type; `Imports:` becomes
  `net/addr, net/key, net/packet, net/phy, net/switching` (`net/switching`
  for the `vlan_pcp` rule). The `net/protocol/README.md` `Imports:` line
  becomes `net/addr, net/key, net/packet, net/phy, net/switching`. The
  `Imported by:` lines of `net/phy/v1/README.md`,
  `net/switching/v1/README.md`, and `net/packet/v1/README.md` gain
  `net/protocol/lldp`.

Tests: `lldp_rules_test.go` gains `TestLldpIeee8023ExtensionRules` and
`TestLldpMedExtensionRules` with the capture fixtures of Requirements 6
and 7, each commented with its capture name and sha256, and the failing
cases listed there. Nothing in this phase decodes a bitmap or a format
octet, so no test here catches a wrong bit order or a MIB-numbered
location format; the fixtures record the decoded values a later mapper's
test compares against.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/protocol/lldp/v1 generated/go/proto/flowseer/net/protocol/lldp/v1 spec/proto/flowseer/net/protocol/README.md spec/proto/flowseer/net/phy/v1/README.md spec/proto/flowseer/net/switching/v1/README.md spec/proto/flowseer/net/packet/v1/README.md test/conformance/proto/lldp_rules_test.go`

### U3. CDP neighbors in `net/protocol/cdp`

Files: `spec/proto/flowseer/net/protocol/cdp/v1/{neighbor.proto,capability.proto,README.md}`,
`generated/go/proto/flowseer/net/protocol/cdp/v1/`,
`spec/proto/flowseer/net/protocol/README.md`,
`spec/proto/flowseer/net/{addr,key,phy,switching}/v1/README.md`,
`test/conformance/proto/cdp_rules_test.go` (new)
After: U2 (U3 edits the root and layer READMEs U1 and U2 edit, and the
root README's `stp/v1/` line describes what U1 landed; U2 is After U1)

Change:

- `neighbor.proto` declares `flowseer.net.protocol.cdp.v1.Neighbor`, each
  field citing its `cdpCache*` leaf in
  `spec/yang/cisco/iosxe/2611/MIBS/CISCO-CDP-MIB.yang`:
  `local_interface_name = 1` (required, `interface_name` rule; the
  mapper resolves `cdpCacheIfIndex`); `device_id = 2` (required,
  `min_len 1`, `max_len 255`, `DisplayString`; the Device-ID TLV string,
  `cdpCacheDeviceId`, `:493`, and IOS-XE's `device-name`, never its numeric
  `device-id`); `port_id = 3`
  (`max_len 255`, `:504`); `platform = 4` (`:516`); `software_version = 5`
  (the Version TLV text, `:482`; `max_len 1024`, the record's bound for
  free text with no standard size, because the TLV runs past
  `DisplayString`: the fixture's is 272 octets and the SMB MIB describes
  Version TLVs over 160 characters, `spec/mib/cisco/smb/CISCOSBCDP.mib:285`); `capabilities = 6` (repeated
  `Capability`, `unique`, `:527`); `vtp_management_domain = 7`
  (`max_bytes 32`, `:543`); `native_vlan_id = 8` (`vlan_id` rule; the MIB's
  0 means no Native VLAN TLV and leaves it absent, `:558-564`);
  `duplex = 9` (`flowseer.net.phy.v1.EthernetDuplex`; the MIB's unknown
  leaves it absent, `:569`); `addresses = 10` (repeated
  `flowseer.net.addr.v1.IpAddress`, the Address TLV, `:466`);
  `management_addresses = 11` (repeated `IpAddress`, `:671`, `:700`);
  `appliance_vlan_id = 12` (`uint32.lte = 4095`, the voice VLAN; the MIB's
  range is `0..4095` and 0 and 4095 are kept as the device sent them,
  `:603-605`);
  `power_consumption_nanowatts = 13` (uint64; the MIB's milliwatts times
  10⁶, `:616`); `mtu_bytes = 14` (uint64, `:628`); `system_name = 15`
  (`max_len 255`, `:639`); `system_object_id = 16` (the `MauType.oid`
  pattern, `:652`); `physical_location = 17` (`:720`); `time_to_live = 18`
  (Duration, `lte 255s`: the hold time the neighbor announced in the
  header's one-octet TTL, `packet-cdp.c:307`, the same meaning as LLDP's
  `Neighbor.time_to_live`; a source that reports only the time remaining,
  such as `rlCdpCacheTimeToLive` in `CISCOSBCDP.mib:292-298`, leaves it
  absent);
  `protocol_version = 19` (uint32, `gte 1`, `lte 2`,
  `rlCdpCacheCdpVersion`, `CISCOSBCDP.mib:301`). Every other string
  leaf is a `DisplayString` and takes `max_len 255`, the size that
  convention states.
- `capability.proto` declares `Capability`, pass-through: integers are
  the bit positions of the Capabilities TLV, `ROUTER = 0` (mask `0x01`),
  `TRANSPARENT_BRIDGE = 1`, `SOURCE_ROUTE_BRIDGE = 2`, `SWITCH = 3`,
  `HOST = 4`, `IGMP = 5`, `REPEATER = 6`, `VOIP_PHONE = 7`,
  `REMOTELY_MANAGED = 8`, `CVTA = 9`, `TWO_PORT_MAC_RELAY = 10`
  (`packet-cdp.c:1355-1397`). Zero is real, so the list is empty when no
  capability was announced.
- The package README follows the lldp README's shape: Boundaries
  (`Imports: net/addr, net/key, net/phy, net/switching`; `Imported by:
  nothing`), why neighbors are a table, the key, what is dropped, and
  Sources (the tracked YANG translation, the upstream MIB URL, the SMB
  MIB for TTL and version, the pinned Wireshark dissector).
- `net/protocol/README.md`: the `cdp/v1/` line loses its planned marker,
  and the `stp/v1/` and `lldp/v1/` lines name MSTIs and the 802.3 and
  MED extensions. `Imported by:` of `net/addr`, `net/key`, `net/phy`, and
  `net/switching` gain `net/protocol/cdp`.

Tests: `cdp_rules_test.go`, `TestCdpNeighborRules`, holds Requirement 5
with the `cdp_v2.pcap` fixture (named with its sha256) and its failing
cases; linking the package also satisfies
`TestEveryDeclaredProtoPackageIsLinked`.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/protocol/cdp/v1 generated/go/proto/flowseer/net/protocol/cdp/v1 spec/proto/flowseer/net/protocol/README.md spec/proto/flowseer/net/addr/v1/README.md spec/proto/flowseer/net/key/v1/README.md spec/proto/flowseer/net/phy/v1/README.md spec/proto/flowseer/net/switching/v1/README.md test/conformance/proto/cdp_rules_test.go`

### U4. IGMP/MLD snooping membership in `net/multicast`

Files: `spec/proto/flowseer/net/multicast/v1/{group_membership.proto,filter_mode.proto,membership_kind.proto,README.md}`,
`generated/go/proto/flowseer/net/multicast/v1/`,
`spec/proto/flowseer/net/README.md`,
`spec/proto/flowseer/net/{addr,key,switching}/v1/README.md`,
`test/conformance/proto/multicast_rules_test.go` (new),
`CONCEPTS.md`
After: U1 (`CONCEPTS.md`), U3 (the `addr`, `key`, and `switching`
READMEs, which U2 also edits)

Change:

- `group_membership.proto` declares `GroupMembership`, whose comment
  says it is FlowSeer-normalized from vendor tables because no standard
  models snooping: `network_instance = 1` (required, rule); `vlan_id = 2`
  (required, `vlan_id` rule); `group = 3` (required `IpAddress`);
  `source = 4` (`IpAddress`; absent means any source, present means a
  source-specific IGMPv3 or MLDv2 entry); `interface_name = 5` (required,
  rule); `filter_mode = 6` (`FilterMode`, RFC 3376 §6.2.1, RFC 3810
  §7.2.1); `compatibility_version = 7` (uint32, `gte 1`, `lte 3`: the
  group compatibility mode, RFC 3376 §7.3.2 and RFC 3810 §8.3.2);
  `kind = 8` (`MembershipKind`); `last_reporter = 9` (`IpAddress`);
  `uptime = 10` and `expires_in = 11` (Duration; RFC 4541 §2.1.1 requires
  a membership timeout).
- Message-level CEL rules: `group` is multicast (`v4` hex begins `e`,
  224/4; `v6` hex begins `ff`, ff00::/8, the `FdbEntry` formatting
  idiom); `source` and `last_reporter` share `group`'s family; an IPv6
  group's `compatibility_version` is at most 2.
- `FilterMode` is FlowSeer-normalized: `_UNSPECIFIED = 0`, `INCLUDE = 1`,
  `EXCLUDE = 2`. `MembershipKind`: `_UNSPECIFIED = 0`, `STATIC = 1`,
  `DYNAMIC = 2` (Aruba `GroupType`,
  `ARUBAWIRED-MGMD-SNOOPING-MIB:713-719`; Ruckus's static table,
  `spec/mib/ruckus/icx/FOUNDRY-SN-IGMP-MIB:161-193`).
- The package README: Boundaries (`Imports: net/addr, net/key,
  net/switching`; `Imported by: nothing`), the normalization and each
  vendor table it reads (Aruba, D-Link, Cisco SMB, FASTPATH, Q-BRIDGE),
  how a PortList source expands to rows, why one table holds both
  protocols, and what stays out.
- `net/README.md` drops the `multicast/v1/` planned marker; `Imported by:`
  of `net/addr`, `net/key`, and `net/switching` gain `net/multicast`.
- `CONCEPTS.md`, Network model, gains "Protocol table": a table a
  protocol owns lives in its protocol package, a table filled by several
  protocols (snooping membership) lives in a function package, and no
  message unions two protocols' rows (record rule 6).

Tests: `multicast_rules_test.go`, `TestGroupMembershipRules`, holds
Requirement 8.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/multicast/v1 generated/go/proto/flowseer/net/multicast/v1 spec/proto/flowseer/net/README.md spec/proto/flowseer/net/addr/v1/README.md spec/proto/flowseer/net/key/v1/README.md spec/proto/flowseer/net/switching/v1/README.md test/conformance/proto/multicast_rules_test.go CONCEPTS.md`

Waves: U1 | U2 | U3 | U4

The chain is forced by shared README lines, not by imports: the
`net/switching/v1/README.md` `Imported by:` line and the
`net/protocol/README.md` `Imports:` line are one line each, and every unit
edits at least one of them, since every new package takes a VLAN rule from
`net/switching`. Phases 2, 3, and 8 of the
parent edit some of the same `Imported by:` lines and `netmodel`;
resolve those merges by taking the union of both sides.

## Verification

```bash
buf lint
buf generate && git status --porcelain generated/   # empty after each unit's commit
go build ./... && go vet ./...
go test -race ./test/conformance/proto/... ./src/common/netsim/vswitch/netmodel/... ./src/modules/localnet/snmpmap/...
.claude/skills/verify-change/scripts/verify-change.sh -- <union of the four units' Verify paths>
```

Run the verifier over the union of changed paths, not with `--full`,
which builds and race-tests `generated/go/yang` and exhausts host memory.
No lab check: no mapper fills these rows in this phase.

## Definition of done

- [x] Verifier green for every changed path of every unit.
- [x] The stp, lldp, cdp, and multicast READMEs, `net/protocol/README.md`,
      `net/README.md`, the touched layer READMEs, and `CONCEPTS.md` match
      the tree in the unit that changed it.
- [x] Every requirement above has its test case, and the capture-derived
      fixtures name the capture and its sha256.
- [x] No plan label appears in schema, code, comments, or commit messages.
- [x] This plan's `status` is `implemented` with an outcome note under the
      title, and the parent's U6 `Landed:` line carries the commit range.

## Open questions

- Whether `BridgeState` should carry `network_instance`. The plan says
  yes (record rule 4 read literally, and the CIST row must key like the
  MSTI rows), which breaks a landed message that the parent's Requirement
  6 does not list. Against it: IEEE8021-MSTP-MIB's only extra key is the
  PBB bridge component, which rule 4 keeps out, so no standard source
  hands a mapper an instance for a spanning tree; the mapper always
  supplies it (normally `default`). If review reads rule 4 as covering
  only the four tables the record names, drop the field from
  `BridgeState` and keep it on `MstInstance` and `MstVlanMap`; the only
  other change is that U1 then leaves `netmodel` untouched.

- Resolved (phase-6 review ruling): `BridgeState` carries the required
  `network_instance` and `PortState` does not. The CIST row is not
  interface-scoped, so rule 4 applies to it like the MSTI rows; `PortState`
  is interface-scoped and inherits the instance, so it does not repeat the
  key. The wire break to the landed `BridgeState` is the right design under
  the pre-release breaking-changes rule. Review verdict: accept. Compound: no
  lesson (CDP cited-not-vendored follows the spec-vendor-layout convention;
  CIST keying follows rule 4; the wire break follows the evolution rule).
