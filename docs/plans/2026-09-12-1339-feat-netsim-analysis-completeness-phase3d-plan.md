---
title: Network Simulation Analysis Completeness, Phase 3d - Plan
type: feat
date: 2026-09-15
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
parent: docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
---

# Network simulation analysis completeness, phase 3d: Multiple spanning tree instances - Plan

> Re-planned by `plan` when its turn comes; the tree will have moved. The
> Decisions and Requirements below are settled and their evidence was checked
> on 2026-09-14. The Units are not written: they depend on the tree structure
> phase 3b lands, and writing them before that would describe a shape that
> does not exist yet.

## Goal

A simulated bridge answers "which link does VLAN 20 block" when VLANs run on
different spanning trees within a region, and answers it consistently across a
region boundary. The means are MST BPDU encoding and decoding, a region
configuration digest, CIST and MSTI priority vectors over the tree structure
phase 3b lands, boundary roles, and hop-count aging.

This phase claims parent R14a, R14b, and R14e.

## Decisions

- **MSTP follows IEEE 802.1Q clause 13.** The clause numbers are the ones
  mstpd cites (`mstp.c`, mstpd at `9f8cc634`). The BPDU offsets come from
  Wireshark (`epan/dissectors/packet-bpdu.c:39-64`, at `50636363`).
- **Region.** The MST configuration identifier is format selector 0, a
  32-octet name, a 16-bit revision, and a 16-octet digest. The digest is
  HMAC-MD5 over the 4096 × 2-octet big-endian VID-to-MSTID table, keyed with
  the 13.7 Table 13-1 key (`mstp.h:40-42`). An unmapped VID maps to MSTID 0.
- **Internal against external.** A port's received information is internal
  when it arrived in an MST BPDU whose configuration identifier equals this
  bridge's. An RST, STP, or foreign-region BPDU is external, and the port is a
  boundary port.
- **CIST vector.** Root ID, external root path cost, regional root ID,
  internal root path cost, designated bridge, designated port
  (`mstp.h:95-104`). The external cost is added on an external port. The
  internal cost is added on an internal port, where the regional root is
  carried unchanged.
- **MSTI vector.** Regional root, internal root path cost, designated bridge,
  designated port, computed from MSTI records on internal ports only.
- **Boundary roles.** An MSTI port role on a boundary port is Master when the
  CIST role is Root, and otherwise equals the CIST role (13.13 f), as mstpd
  applies it in `updtRolesTree` (`mstp.c:2747-2780`).
- **Aging.** External information stays valid while `MessageAge + 1 <=
  MaxAge`, which phase 3b lands for the single tree. Internal information
  stays valid while `remainingHops > 1`. Valid information lasts
  `3 × HelloTime` (13.26.22, `mstp.c:2565-2570`). MSTIs use the CIST timers
  and their own hop count.
- **Topology change.** A topology change on a tree flushes, on the other ports
  of that tree, only the entries of VLANs mapped to it. A CIST topology change
  received on a boundary port applies to every tree (`setTcFlags`,
  `mstp.c:2296-2303`).
- **Bridge identity.** An MSTI bridge ID is the tree priority, a multiple of
  4096, plus the MSTID in the system-ID extension. MSTI records carry only the
  priority nibbles (`mstp.h:116-127`).
- **Multiple regions are modeled,** which is where the external vector and the
  boundary roles earn their place. User-directed 2026-09-15.
- **Loop guard extends across a boundary port's instances,** through the
  boundary role rule, over the per-tree guard phase 3b lands.
- **`Decode` gains MST support,** replacing the refusal phase 3b puts at
  `src/common/netsim/vswitch/stp/bpdu.go` for version 3.
- **Per-tree FDB flushing arrives here.** `stp.Effects.Flush` becomes port and
  VLAN-set pairs, and the bridge gains a flush that respects them; phase 3b
  leaves both as a bare port list because one tree makes them equivalent.
- **netmodel stays RSTP-only.** No schema reports MSTP
  (`spec/proto/flowseer/net/protocol/stp/v1/protocol_version.proto`), and
  schema work is out of this plan. MSTP configuration reaches the layer
  through `vswitch.ConstructionSpec`.

## Requirements

1. **R14a:** MSTP instances select independent trees.
   **Acceptance example:** two switches in one region, joined by links L1 and
   L2, with VLAN 10 on MSTI 1 and VLAN 20 on MSTI 2. The priorities make L1
   block for MSTI 1 and L2 block for MSTI 2. A VLAN 10 frame crosses L2, and a
   VLAN 20 frame crosses L1.
2. **R14b:** Region boundaries follow the CIST.
   **Acceptance example:** switch A is in region R1 and switch B in region R2,
   same name and different revision, joined by two links. Both MSTIs block the
   same link as the CIST. The CIST root and external cost cross the boundary.
3. **R14e:** The MST digest of the all-zero table, every VID on MSTID 0, is
   `ac36177f50283cd4b83821d8ab26de62`. With VID 10 on MSTID 1 and VID 20 on
   MSTID 2 it is `9357ebb7a8d74dd5fef4f2bab50531aa`. Both were computed with
   HMAC-MD5 over the table as `mstp.c:77-84` builds it, run with Python's
   `hmac` module on 2026-09-14. Encode and decode round-trip a BPDU with two
   MSTI records byte for byte.
4. **R15a-hops:** Internal information ages out by remaining hops.
   **Acceptance example:** a four-switch ring in one region whose root is
   removed. The information ages out by `remainingHops` rather than by message
   age, and a new regional root is elected.
5. **R39:** The corpus admits two cases:
   - `planning/mstp-vlan-instances-diverge`: both VLANs block the same link.
   - `topology-shadowing/mst-region-boundary`: MSTIs ignore the boundary.

## Out of scope

- SPT and SPB BPDUs, version 4, agreement digests, L2GP, and Cisco
  pre-standard MSTI encoding, which decode as unsupported.
- Per-VLAN RSTP and everything SSTP, which is phase 3e.
- netmodel loading of MSTP.

## Open questions

The re-plan writes the Units against what phase 3b landed, and settles:

- Whether the CIST and MSTI vectors extend `priorityVector`
  (`src/common/netsim/vswitch/stp/layer.go:123`) with fields RSTP leaves zero,
  or become a second vector type with its own comparator. The survey on
  2026-09-15 found the comparator reusable and the surrounding state not, so
  this is a question about the vector alone.
- Whether the tree scope gains a component, `ProtocolScope(node, "stp",
  "cist" | "msti:<id>")`, and whether `protocol-link-unknown` is raised per
  tree rather than per port
  (`src/common/netsim/vswitch/switch.go:2357`).
- How `Backup` detection, which compares a single bridge ID
  (`src/common/netsim/vswitch/stp/layer.go:732`), becomes per instance, since
  MSTI bridge IDs differ by the MSTID in the system-ID extension.
- Whether the per-port transmit hold budget
  (`src/common/netsim/vswitch/stp/layer.go:107`) stays per port, which is what
  the standard wants, while the pending-emission kind becomes per instance.
