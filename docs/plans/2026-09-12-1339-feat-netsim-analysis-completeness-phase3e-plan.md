---
title: Network Simulation Analysis Completeness, Phase 3e - Plan
type: feat
date: 2026-09-15
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
parent: docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
---

# Network simulation analysis completeness, phase 3e: Rapid spanning tree per VLAN - Plan

> Re-planned by `plan` when its turn comes; the tree will have moved. The
> Decisions and Requirements below are settled and their evidence was checked
> on 2026-09-14. The Units are not written: they depend on the tree structure
> phase 3b lands and on whatever phase 3d leaves in the receive path.

## Goal

A simulated bridge runs one rapid spanning tree per VLAN, so each VLAN elects
its own root and blocks its own trunk, and a bridge that meets an MSTP
neighbour says so rather than guessing. The means are per-VLAN trees over the
tree structure phase 3b lands, the SSTP encapsulation on the wire, and the
PVID consistency check.

This phase claims parent R14c and R14d.

## Decisions

- **PVST is RSTP per VLAN.** Each VLAN listed in the configuration runs the
  landed RSTP machine with bridge priority plus the VID as the system-ID
  extension. User-directed 2026-09-14.
- **Encapsulation.** A tree's BPDU is an RST BPDU in LLC/SNAP with OUI
  `00-00-0c` and PID `0x010b`, sent to `01:00:0c:cc:cc:cd`, followed by the
  originating-VLAN TLV: type 0, length 2, VID (`packet-bpdu.c:198`,
  `:242-290`; `packet-cisco-pid.h:20`).
- **Where each tree goes,** Cisco's rules, quoted in
  [Troubleshoot Spanning Tree PVID- and Type-Inconsistencies](https://www.cisco.com/c/en/us/support/docs/lan-switching/spanning-tree-protocol/24063-pvid-inconsistency-24063.html):
  - Every tree is sent to the SSTP address, tagged with its VID.
  - The native VLAN's tree is the exception and is sent untagged.
  - VLAN 1's tree is also sent untagged to `01:80:c2:00:00:00`, whatever the
    native VLAN is.

  Juniper documents the same shape: untagged IEEE BPDUs for the common tree on
  VLAN 1, and tagged SSTP BPDUs per VLAN
  ([VLAN Spanning Tree Protocol](https://www.juniper.net/documentation/en_US/junos12.3/topics/concept/mx-series-vlan-stp.html)).
  Why: it is the cross-vendor rule, and it lets an RSTP or MSTP neighbour hear
  VLAN 1's tree as its common spanning tree. User-directed 2026-09-14 on that
  condition.
- **PVID check.** An SSTP BPDU whose PVID TLV differs from the VLAN it arrived
  on is not applied. That tree's port becomes Discarding with reason
  `pvid-inconsistent` until a consistent BPDU arrives. Cisco blocks on this
  condition; the Juniper page does not describe it.
- **PVST simulation is not modeled.** An MSTP bridge that receives an SSTP
  BPDU, or a PVST bridge that receives an MST BPDU, keeps its CIST or VLAN 1
  behavior on that port and raises an `Unsupported` issue
  `stp-pvst-boundary` on the port scope, which every result through the port
  on another VLAN consults
  ([Cisco, PVST Simulation on MST Switches](https://www.cisco.com/c/en/us/support/docs/lan-switching/multiple-instance-stp-mistp-8021s/116464-configure-pvst-00.html)).
  The issue follows the pattern the landed phase used for
  `lag-rebalance-unmodeled` and `mcast-query-unobserved`
  (`src/common/netsim/vswitch/switch.go:2121`).
- **Tagged emission arrives here.** `Encode` emits untagged LLC frames
  (`src/common/netsim/vswitch/stp/bpdu.go:293`) and `applySTPEffects` adds no
  tag (`src/common/netsim/vswitch/switch.go:1901`); one of the two gains the
  tagging rule above, and the switch learns the second destination address at
  `switch.go:729`, which today matches only `stpGroupAddress`.
- **`vswitch.New` rejects a PVST bridge carrying a VLAN with no tree listed.**
  Why: a VLAN with no tree has no defined forwarding state, and guessing one
  is the false answer this phase exists to remove.
- **netmodel stays RSTP-only.** No schema reports per-VLAN trees, and schema
  work is out of this plan.

## Requirements

1. **R14c:** RSTP per VLAN selects per-VLAN roots.
   **Acceptance example:** two PVST switches over two trunks. VLAN 10's root
   is A and VLAN 20's root is B, so each VLAN blocks a different trunk.
   Captured emissions decode as SSTP with the matching PVID TLV and tag.
2. **R14d:** A PVST port facing an MSTP bridge carries `stp-pvst-boundary` for
   VLANs other than 1 and stays `Complete` for VLAN 1.
3. **R15b-pvid:** An SSTP BPDU with PVID 20 arriving on VLAN 10 leaves VLAN
   10's port `pvid-inconsistent`, and a consistent BPDU clears it.
4. **R39:** The corpus admits `planning/pvst-per-vlan-root`, whose false
   answer is one root for every VLAN.

## Out of scope

- 802.1D STP per VLAN, and PVST inconsistency states other than the PVID
  check.
- Cisco PVST simulation on MSTP boundary ports, which this phase reports as
  unsupported rather than models.
- netmodel loading of per-VLAN trees.

## Open questions

The re-plan writes the Units against what phases 3b and 3d landed, and
settles:

- Whether tagging happens in `Encode` or in `applySTPEffects`, which decides
  whether `stp` learns about VLAN tags at all.
- Whether the per-VLAN trees reuse the tree map phase 3b lands keyed by VID,
  or whether the VID-to-tree map gains a second form for this mode.
- Whether `stp-pvst-boundary` is raised per port or per port and VLAN, given
  that VLAN 1 stays `Complete` on the same port.
- Whether the three modes are one `Mode` field with `RSTP`, `MSTP`, and
  `PVST`, as the 2026-09-14 draft had it, or whether the mode is implied by
  which of the instance and VLAN tables the configuration fills. The first is
  a plain enum an operator recognizes; the second cannot express an empty
  configuration's mode.
