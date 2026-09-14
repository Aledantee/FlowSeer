---
title: Network Simulation Analysis Completeness, Phase 3b - Plan
type: feat
date: 2026-09-14
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
parent: docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
---

# Network simulation analysis completeness, phase 3b: Spanning-tree instances and guards - Plan

> Re-planned by plan when its turn comes; the tree will have moved. The
> Decisions, Requirements, and cited evidence are settled; the Units are a
> draft to check against the gate, selector, and metadata shapes phase 3
> lands.

## Goal

A simulated bridge answers "which link does VLAN 20 block" when VLANs run on
different spanning trees. It supports three modes: RSTP with one tree, MSTP
across one or more regions, and RSTP per VLAN. Information that has outlived
its message age or hop count stops holding a port blocked. Configured BPDU
guard, root guard, TCN restriction, and loop guard produce their port
outcomes. The means are a `stp.Layer` that runs one RSTP port-role machine
per tree, MST and per-VLAN BPDU codecs, and a VLAN-aware forwarding gate in
`vswitch`.

Stop condition: the plan is wrong if the CIST computation cannot reuse
today's RSTP machine with the MSTP priority vector substituted, which would
mean a second state machine rather than a generalized one.

This phase claims parent R14 and R15 and extends R9, R37, and R39.

## Decisions

- **The parent's Decisions govern, as amended on 2026-09-14.** Phase 3 lands
  first and changes `switch.go`, `metadata.go`, `bridge/bridge.go`, and
  `fabric/run.go`, which U5 also edits.
- **Three modes.** `stp.Config.Mode` is `RSTP` (zero value, today's
  behavior), `MSTP`, or `PVST`. User-directed 2026-09-14: MSTP with multiple
  regions, and PVST meaning RSTP per VLAN.
- **MSTP follows IEEE 802.1Q clause 13.** The clause numbers below are the
  ones mstpd cites (`mstp.c`, mstpd at `9f8cc634`). The BPDU offsets come
  from Wireshark (`epan/dissectors/packet-bpdu.c:39-64`, at `50636363`).
  - *Region.* The MST configuration identifier is format selector 0, a
    32-octet name, a 16-bit revision, and a 16-octet digest. The digest is
    HMAC-MD5 over the 4096 × 2-octet big-endian VID-to-MSTID table, keyed
    with the 13.7 Table 13-1 key (`mstp.h:40-42`). An unmapped VID maps to
    MSTID 0.
  - *Internal.* A port's received information is internal when it arrived in
    an MST BPDU whose configuration identifier equals this bridge's. An RST,
    STP, or foreign-region BPDU is external, and the port is a boundary port.
  - *CIST vector.* Root ID, external root path cost, regional root ID,
    internal root path cost, designated bridge, designated port
    (`mstp.h:95-104`). The external cost is added on an external port. The
    internal cost is added on an internal port, where the regional root is
    carried unchanged.
  - *MSTI vector.* Regional root, internal root path cost, designated bridge,
    designated port, computed from MSTI records on internal ports only.
  - *Boundary roles.* An MSTI port role on a boundary port is Master when the
    CIST role is Root, and otherwise equals the CIST role (13.13 f), as mstpd
    applies it in `updtRolesTree`, `mstp.c:2747-2780`).
  - *Aging.* External information stays valid while `MessageAge + 1 <=
    MaxAge`. Internal information stays valid while `remainingHops > 1`.
    Valid information lasts `3 × HelloTime` (13.26.22, `mstp.c:2565-2570`).
    MSTIs use the CIST timers and their own hop count.
  - *Topology change.* A TC on a tree flushes, on the other ports of that
    tree, only the entries of VLANs mapped to it. A CIST TC received on a
    boundary port applies to every tree (`setTcFlags`, `mstp.c:2296-2303`).
  - *Bridge identity.* An MSTI bridge ID is the tree priority (a multiple of
    4096) plus the MSTID in the system-ID extension. MSTI records carry only
    the priority nibbles (`mstp.h:116-127`).
- **PVST is RSTP per VLAN.** Each VLAN listed in `Config.VLANs` runs today's
  RSTP machine with bridge priority plus the VID as the system-ID extension.
  - *Encapsulation.* A tree's BPDU is an RST BPDU in LLC/SNAP with OUI
    `00-00-0c` and PID `0x010b`, sent to `01:00:0c:cc:cc:cd`, and followed by
    the originating-VLAN TLV: type 0, length 2, VID
    (`packet-bpdu.c:198`, `:242-290`; `packet-cisco-pid.h:20`).
  - *Where each tree goes* (Cisco's rules, quoted in
    [Troubleshoot Spanning Tree PVID- and Type-Inconsistencies](https://www.cisco.com/c/en/us/support/docs/lan-switching/spanning-tree-protocol/24063-pvid-inconsistency-24063.html)):
    - Every tree is sent to the SSTP address, tagged with its VID.
    - The native VLAN's tree is the exception and is sent untagged.
    - VLAN 1's tree is also sent untagged to `01:80:c2:00:00:00`, whatever
      the native VLAN is.

    Juniper documents the same shape: untagged IEEE BPDUs for the common
    tree on VLAN 1, and tagged SSTP BPDUs per VLAN
    ([VLAN Spanning Tree Protocol](https://www.juniper.net/documentation/en_US/junos12.3/topics/concept/mx-series-vlan-stp.html)).
    Why: it is the cross-vendor rule, and it lets an RSTP or MSTP neighbor
    hear VLAN 1's tree as its CST. User-directed 2026-09-14 on that condition.
  - *PVID check.* Cisco blocks on this condition; the Juniper page does not
    describe it. An SSTP BPDU whose PVID TLV differs from the VLAN it arrived
    on is not applied. That tree's port becomes Discarding with reason
    `pvid-inconsistent` until a consistent BPDU arrives.
  - *PVST simulation.* Cisco PVST simulation on MSTP boundary ports is not
    modeled
    ([Cisco, PVST Simulation on MST Switches](https://www.cisco.com/c/en/us/support/docs/lan-switching/multiple-instance-stp-mistp-8021s/116464-configure-pvst-00.html)).
    An MSTP bridge that receives an SSTP BPDU, or a PVST bridge that receives
    an MST BPDU, keeps CIST or VLAN 1 behavior on that port. It raises an
    `Unsupported` issue `stp-pvst-boundary` on the port scope, which every
    result through the port on another VLAN consults.
- **Guards.** They are per-port fields. Their outcomes are port states, not
  issues.
  - *`BPDUGuard`.* A BPDU received on the port disables it for spanning tree
    with reason `bpdu-guard` (`mstp.c:571-576`). The port stays disabled
    until a `LinkChange` reports it down and then up again.
  - *`RestrictedRole`*, root guard (13.25.14). The port is never selected
    as root port (`mstp.c:2624`). Superior information makes it Alternate.
  - *`RestrictedTCN`* (13.25.15). A TC received on the port does not
    propagate (`mstp.c:2315`).
  - *`LoopGuard`* is netsim's own design, drawn from Cisco loop guard, Juniper
    loop protection, and Arista EOS
    ([Cisco](https://www.cisco.com/c/en/us/support/docs/lan-switching/spanning-tree-protocol-stp-8021d/218321-configure-stp-with-loop-guard-and-bpdu-s.html),
    [Juniper](https://www.juniper.net/documentation/us/en/software/junos/stp-l2/topics/topic-map/spanning-tree-loop-protection.html),
    [Arista EOS](https://www.arista.com/en/um-eos/eos-spanning-tree-protocol)).
    User-directed 2026-09-14. All three apply it only to root and alternate
    ports and recover on the next BPDU.
    - *Trigger.* On one tree, the port's role is Root, Alternate, or Backup
      and its received information expires by timeout, message age, or
      remaining hops. The port then enters `loop-inconsistent`: Discarding,
      role Alternate. It is excluded from root-port selection, so the tree
      reconverges around it, but it never becomes Designated. A link-down
      clears the state and gives the port no loop guard trigger.
    - *Recovery.* A BPDU for that tree received on the port clears the state,
      and normal role selection runs.
    - *Scope.* Blocking is per tree. On an MSTP boundary port, the CIST
      state carries to every MSTI through the boundary role rule.
    - *Inactive.* The guard does nothing while the port is operationally
      edge or not point-to-point. Cisco and Arista rule out shared links and
      portfast ports.
    - *Validation.* `LoopGuard` with `RestrictedRole` on one port is
      rejected, as Cisco and Juniper make them mutually exclusive. So is
      `LoopGuard` with `AdminEdge`.
    - *Trace.* `stp.loop_guard.block` and `stp.loop_guard.unblock` steps
      record the expired information and the port state before and after.
- **The gate becomes VLAN-aware.** `bridge.Gate` is `Learns(port, vid)`
  and `Forwards(port, vid)`, and `stp.Layer` maps the VID to its tree. FID
  and VID are the same value in `bridge` today; the mapping is by VID.
- **Scopes.** Each tree has `ProtocolScope(node, "stp", "cist" | "msti:<id>"
  | "vlan:<vid>")`. `protocol-link-unknown` is raised on the ports of every
  tree whose inputs include the unknown port, which extends the landed rule
  (`src/common/netsim/vswitch/switch.go:2134`) to trees.
- **Validation.**
  - Region names are at most 32 octets.
  - MSTIDs are 1-4094. Each VID maps to at most one MSTID.
  - `MaxHops` is 6-40; zero means 20.
  - PVST VLANs are valid VIDs.
  - Guards and per-tree port overrides name ports that exist and are not LAG
    members.
  - `vswitch.New` rejects a PVST bridge that carries a VLAN with no tree
    listed.
  - `Instances` and `VLANs` are each valid only in their own mode.
- **netmodel stays RSTP-only.** No schema reports MSTP or per-VLAN trees
  (`spec/proto/flowseer/net/protocol/stp/v1/protocol_version.proto` lists STP
  and RSTP), and schema work is out of this plan.

## Requirements

1. **R14a:** MSTP instances select independent trees.
   **Acceptance example:** two switches in one region, joined by links L1 and
   L2, with VLAN 10 on MSTI 1 and VLAN 20 on MSTI 2. The priorities make L1
   block for MSTI 1 and L2 block for MSTI 2. A VLAN 10 frame crosses L2, and a
   VLAN 20 frame crosses L1.
2. **R14b:** Region boundaries follow the CIST.
   **Acceptance example:** switch A is in region R1 and switch B in region R2
   (same name, different revision), joined by two links. Both MSTIs block the
   same link as the CIST. The CIST root and external cost cross the boundary.
3. **R14c:** RSTP per VLAN selects per-VLAN roots.
   **Acceptance example:** two PVST switches over two trunks. VLAN 10's root
   is A and VLAN 20's root is B, so each VLAN blocks a different trunk.
   Captured emissions decode as SSTP with the matching PVID TLV and tag.
4. **R14d:** A PVST port facing an MSTP bridge carries `stp-pvst-boundary`
   for VLANs other than 1 and stays `Complete` for VLAN 1.
5. **R14e:** The MST digest of the all-zero table (every VID on MSTID 0) is
   `ac36177f50283cd4b83821d8ab26de62`. With VID 10 on MSTID 1 and VID 20 on
   MSTID 2 it is `9357ebb7a8d74dd5fef4f2bab50531aa`. Both were computed with
   HMAC-MD5 over the table as `mstp.c:77-84` builds it, run with Python's
   `hmac` module on 2026-09-14. Encode and decode
   round-trip a BPDU with two MSTI records byte for byte.
6. **R15a:** Stale root information ages out.
   **Acceptance example:** a four-switch RSTP ring. The root bridge is removed
   while BPDUs carrying it circulate. Each hop raises the message age. Once
   it reaches `MaxAge`, the information is discarded and a new root is
   elected. The same ring in one MST region ages out by `remainingHops`.
7. **R15b:** Guards.
   **Acceptance example:** a BPDU on a `BPDUGuard` edge port leaves it
   disabled with `bpdu-guard` until a down/up. A superior BPDU on a
   `RestrictedRole` port leaves the port Alternate and the old root
   unchanged. With `LoopGuard`, an Alternate port whose designated peer stops
   sending stays Discarding as `loop-inconsistent`, and the next BPDU
   restores Alternate. Without it, the port becomes Designated and forwards.
   An SSTP BPDU with PVID 20 arriving on VLAN 10 leaves VLAN 10's port
   `pvid-inconsistent`.
8. **R9:** `Mode`, `Region`, `MaxHops`, `Instances`, `VLANs`, and the four
   guard fields are covered by validation, normalization, `Clone`, and
   `Diff`.
   **Acceptance example:** moving VLAN 20 from MSTI 1 to MSTI 2 gives one
   change and a different digest.
9. **R39:** The corpus admits each case below with its false answer:
   - `planning/mstp-vlan-instances-diverge`: both VLANs block the same link.
   - `topology-shadowing/mst-region-boundary`: MSTIs ignore the boundary.
   - `planning/pvst-per-vlan-root`: one root for every VLAN.
   - `troubleshooting/stale-root-ages-out`: the port stays blocked by a
     vanished root.
   - `troubleshooting/bpdu-guard-disables-edge`: the edge port keeps
     forwarding.
   - `troubleshooting/loop-guard-unidirectional-link`: the port whose BPDUs
     stopped becomes Designated and opens a loop.

## Out of scope

- SPT/SPB BPDUs (version 4), agreement digests, L2GP, and Cisco pre-standard
  MSTI encoding, which decode as unsupported.
- PVST simulation and inconsistency states, 802.1D STP per VLAN, BPDU
  filter, and automatic guard recovery timers.
- netmodel loading of MSTP or per-VLAN trees, and retention of per-tree
  state across `Derive` (phase 5; until then `Derive` keeps the layer only
  when `stp.Diff` is empty, as today).

## Units

### U1. Configuration

Files: `src/common/netsim/vswitch/stp/config.go`, `diff.go`, `config_test.go`
After: none
Change: `Config` gains `Mode`, `Region`, `MaxHops`, `Instances` (MSTID to
priority, VLAN list, and per-port priority and path cost), `VLANs` (VID to
the same tree settings), and the guard fields on `Port`. Validation,
normalization, `Clone`, canonical facts, and `Diff` cover them. The package
comment names 802.1D, 802.1Q clause 13, and per-VLAN RSTP.
Tests: each validation rule with its field path, the R9 example, and the
unchanged RSTP normal form.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/stp`

### U2. MST and SSTP codecs

Files: `src/common/netsim/vswitch/stp/bpdu.go`, `mst.go`, `bpdu_test.go`
After: U1
Change: `BPDU` carries optional MST fields (configuration identifier, CIST
internal cost, CIST bridge ID, remaining hops, MSTI records) and an
optional PVID. `Encode`/`Decode` handle version 3 at the Wireshark offsets.
`EncodeSSTP`/`DecodeSSTP` handle the SNAP and TLV form. `ConfigDigest`
computes the region digest.
Tests: R14e; truncated and oversized MSTI lengths; a missing or malformed
PVID TLV rejected with a reason; version 4 unsupported.
Verify: as U1.

### U3. Tree engine

Files: `src/common/netsim/vswitch/stp/layer.go`, `tree.go`, `fact.go`,
`layer_test.go`
After: U2
Change: `Layer` holds trees keyed by tree ID. Each tree runs the landed
port-role and state machine over a priority vector type that covers the RSTP
and CIST/MSTI shapes. Receive classifies internal or external, updates the
CIST and every MSTI record, and applies boundary roles, aging, and per-tree
TC. PVST keeps one RSTP tree per VLAN. Queries (`Forwards`, `Learns`,
`PortInfo`, `Root`) take a VID or tree ID. `Effects.Flush` lists port and
VLAN-set pairs.
Tests: R14a and R14b on two directly wired layers; R14c at the layer level;
R15a in both aging forms; an inferior BPDU from the designated bridge
replaces stored information; identical event sequences give identical
effects.
Verify: as U1.

### U4. Guards

Files: `src/common/netsim/vswitch/stp/layer.go`, `layer_test.go`
After: U3
Change: apply the four guards and the PVID check per the Decisions. `PortInfo` gains the
blocking reason.
Tests: R15b, clearing each guard state, `RestrictedTCN` not flushing,
loop guard inactive on edge and shared ports, loop guard per tree with
another tree unaffected, and the two rejected guard combinations.
Verify: as U1.

### U5. Switch and fabric integration

Files: `src/common/netsim/vswitch/switch.go`, `config.go`, `metadata.go`,
`bridge/bridge.go`, their tests, `src/common/netsim/fabric/run.go` if
emissions need tags, `src/common/netsim/vswitch/README.md`
After: U4
Change: the VLAN-aware gate; interception of SSTP BPDUs (tagged or untagged)
and MST BPDUs; tagged PVST emissions; per-tree flushes; per-tree
`protocol-link-unknown`; `stp-pvst-boundary`; the PVST VLAN coverage check
in `vswitch.New`; and `Roles`, `Root`, and `Times` taking a tree.
Tests: R14a-R14d through fabrics; the PVST coverage error; a journey on
VLAN 10 that consults only MSTI 1's scope.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch src/common/netsim/fabric`

### U6. Corpus and documentation

Files: `src/common/netsim/internal/netsimtest/cases.go`, `corpus_test.go`,
`README.md`, `docs/architecture/2026-09-10-virtual-device-direction.md`,
`src/common/netsim/README.md`, `src/common/netsim/vswitch/stp/README.md`
(new)
After: U5
Change: register the six R39 cases. The boundary case cites the switch
construction evidence on its `Unsupported` issue. The new `stp` README gives
a two-instance MSTP example and the unsupported list. The direction record
states the three modes, boundary behavior, and guards, and drops MSTP from
its protocol-depth gap.
Tests: corpus admission and deterministic re-execution.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim docs/architecture/2026-09-10-virtual-device-direction.md`

## Verification

```bash
go test -race ./src/common/net/... ./src/common/netsim/...
go vet ./src/common/net/... ./src/common/netsim/...
.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim \
  docs/architecture/2026-09-10-virtual-device-direction.md \
  docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-phase3b-plan.md \
  docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
```

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] R14 and R15 examples pass; existing RSTP tests pass unchanged apart
      from signature moves.
- [ ] `stp` README added; the vswitch and netsim READMEs and the direction
      record updated in the same change.
- [ ] This plan's `status` set with an outcome note; the parent's phase 3b
      `Landed:` line filled; no plan labels in code.
