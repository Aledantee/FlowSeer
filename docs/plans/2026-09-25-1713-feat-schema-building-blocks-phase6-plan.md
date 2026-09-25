---
title: Schema Building Blocks Phase 6, L2 Protocols - Plan
type: feat
date: 2026-09-25
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-25-1713-feat-schema-building-blocks-plan.md
---

# Schema Building Blocks Phase 6, L2 Protocols - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

The L2 protocols the targeted switches run and FlowSeer cannot yet hold
get their tables: MSTP instances and the VLAN-to-instance map in
`protocol/stp`, the IEEE 802.3 and LLDP-MED extensions in `protocol/lldp`,
CDP neighbors in `protocol/cdp`, and IGMP/MLD snooping membership in
`net/multicast`.

## Decisions

The record's rule 6 (MSTP in `protocol/stp`; CDP its own package; no union
neighbor message; snooping is functional) governs. From dossier 05, to be
confirmed in the re-plan:

- MSTP rows are keyed by MST instance id and reuse
  `BridgeId`, `PortRole`, and `ForwardingState` from the same package,
  following `IEEE8021-MSTP-MIB` (`spec/mib/ieee/`). MSTIDs are 1 to 4094
  (`IEEE8021MstIdentifier`, `spec/mib/ieee/IEEE8021-TC-MIB:317`); the MIB
  keeps the CIST in separate tables, and the re-plan decides whether the
  schema follows that or spells the CIST as 0.
- The VLAN-to-MSTI map is its own row; it is the table every MSTP consumer
  needs first.
- The LLDP 802.3 extension carries MAC/PHY configuration, power via MDI,
  and maximum frame size (it detects an MTU mismatch from one side), from
  `LLDP-EXT-DOT3-MIB`.
- CDP fields come from `CISCO-CDP-MIB` in `github.com/cisco/cisco-mibs`,
  which is not vendored; the vendored `CISCOSB-CDP` has no neighbor table.
  The re-plan vendors the MIB or cites it by URL.
- Snooping has no standard model (RFC 4541 describes behavior, not a
  table); the re-plan shapes `[vlan, group, source?, interface]` membership
  from the vendor tables the atlas lists and says that it is
  FlowSeer-normalized.

## Requirements

1. An MSTP instance row with `mst_id = 4095` fails.
2. A VLAN-to-MSTI map row listing VLAN 10 twice fails (`repeated.unique`).
3. A CDP neighbor fixture from a lab ICX or a Cisco capture maps with its
   device id, port id, platform, and native VLAN.
