---
title: Spanning Tree Handshake Scope and Inherited Priority - Plan
type: refactor
date: 2026-10-07
artifact_contract: flowseer-plan/v2
execution: code
---

# Spanning Tree Handshake Scope and Inherited Priority - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

A proposal on a tree's Root or Alternate port cuts only the Designated ports
of that tree that are not synced, and a tree port that sets no priority of
its own follows the bridge port each time the configuration is normalized.
The means is the four behaviors of `layer/stp` that the spanning tree phase
left to a later one, each settled from a failing test. Stop condition: if a
port that keeps its agreement through a sync forwards on an agreement its
peer gave to a better vector than the port now offers (Inventory,
Correctness 5), the sync rule is unsafe alone, and the phase takes that
agreement clear on first or returns to the user.

## Decisions

- The parent's Decisions apply.
- This phase holds four behaviors of `layer/stp`: which Designated ports a
  proposal cuts (one holding an agreement keeps it), the same sync at a
  boundary port, an MSTI proposal in a record that is not stored, and an
  inherited port priority that follows the bridge port. The spanning tree
  phase's plan hands them to a later phase in the last entry of its
  Decisions
  (`docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase3-plan.md`).
  (decided by the user, 2026-10-07)
- A proposal blocks only those other Designated ports of the tree that are
  not synced, and a port holding an agreement keeps it and stays
  Forwarding. This holds for the CIST, an MSTI, and a PVST VLAN tree alike.
  Why: `Q2003` Figure 13-17 calls `setSyncTree()` in PROPOSED, a Designated
  port that is learning or forwarding leaves for LISTEN when `sync &&
  !synced` holds, and SYNCED is entered on `agreed && !synced` or `sync &&
  synced`. `Q2003` 13.24.27 has `sync` discard a port only "if the Port is
  not already synchronized". `D2009` Figure 13-25 puts the same conditions
  on DESIGNATED_DISCARD and DESIGNATED_SYNCED. `isSynced` already counts a
  Designated port synced when it is Discarding or agreed
  (`src/common/sim/layer/stp/roles.go:25`), so the cut and the test for it
  disagree today. A boundary port is left to Open questions.
- An instance or VLAN port that sets no priority of its own gets the
  bridge port's from `Config.Normalize`, and `PriorityPresent` stays as
  the caller wrote it, so normalizing again inherits again. This amends
  U3's "marks it present". Why: `Switch.Config` and `Fabric.Config` return
  normalized configurations, and a caller that reads one back and changes
  the bridge port priority must see the tree ports follow it. `Diff`
  reports the effective change on each inheriting tree port. (decided by
  the user, 2026-10-05)
- The Decision above is carried word for word from the spanning tree
  phase's plan as commit `bc06df79` holds it. Its U3 is that plan's unit
  "Wire format and configuration identity", whose Change has
  `Config.Normalize` mark the inherited value present.
- Unconfirmed: the sync at a boundary port and a proposal in a record that
  is not stored stay as the code has them until their Open questions are
  answered. Why: each has a source on both sides (Inventory, Correctness 2
  and 3), and the spanning tree phase's Decision names them without a
  direction.
- The switch composition phase does not wait on this one. Why: with either
  wanted change applied, every test under `src/common/sim` outside
  `layer/stp` passes (Inventory, Correctness 1 and 4), and that phase's
  inventory holds no entry on the handshake or on inheritance
  (`docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase9-plan.md`).
  The one test here that reads a configuration back through a switch or a
  fabric is written against the device API that has landed when this phase
  is re-planned.
- A count or expectation outside `layer/stp` that moves names its cause in
  the commit body. Why: none moves today, so one that does comes from a
  change this plan did not expect.

## Requirements

R1. Every Correctness entry ends as a test that fails before its fix, or is
struck with the reason. An entry whose Open question is answered "as today"
ends as a test that pins today's behavior. Example: with the boundary skip
kept, a test fails when the skip at
`src/common/sim/layer/stp/receive.go:16` also applies to a PVST tree.

R2. A proposal leaves an agreed port forwarding. Example: a PVST bridge's
VLAN 10 tree has a Designated port that holds an agreement and forwards,
and a proposal arrives on its VLAN 10 Root port. The Designated port is
still Forwarding with its agreement after the call, a second Designated
port that forwards without one is Discarding, and VLAN 20 is unchanged.

R3. An inherited port priority follows the bridge port. Example: a
configuration whose MSTI 1 port `1/1/1` sets only a path cost is
normalized, its bridge port `1/1/1` priority is changed from 128 to 64, and
it is normalized again. The MSTI 1 port's priority is 64, and the layer
built from it reports 64 for that port on VLAN 10.

R4. `src/common/sim/layer/stp/README.md` states each of the four behaviors
as the layer has it after the phase. Example: the Limits entry on boundary
sync either goes or names the clause that keeps it.

## Inventory

Paths are as of commit `d09b8eb0`. `S` is `src/common/sim/layer/stp`.
Correctness 1, 3, and 4 and the first Tests entry name the command that was
run. The rest were read, not run.

### Sources

- `Q2003`: IEEE Std 802.1Q, 2003 Edition, clause 13,
  https://bittwist.sourceforge.io/doc/802.1Q-2003.pdf.
- `D2009`: P802.1aq/D1.5 with suggested changes, 12 May 2009, clause 13,
  https://www.ieee802.org/1/files/public/docs2009/aq-seaman-merged-spanning-tree-protocols-0509.pdf.
  An unapproved working draft.
- Not read: the published IEEE Std 802.1Q-2011. A statement that rests on
  it is unverified.

Both documents were read as extracted text, which drops a figure's layout.
Which transition a condition labels is read from the variables each state
assigns.

### Correctness

1. High. A proposal cuts a Designated port that holds an agreement.
   `syncTree` clears `agreed` and sets Discarding on every non-edge
   Designated port of the tree (`S/receive.go:24-37`). Every proposal on a
   Root or Alternate port then stops traffic on each agreed downstream port
   until its peer agrees again. With `&& !otherP.agreed` added to the test
   at `:24`, `go test ./src/common/sim/...` fails four tests, all in `S`:
   `TestSyncCutRetainsTopologyActivity` (`S/topology_activity_test.go:11`),
   `TestSyncCutRestartsAutoEdgeDelay` (`S/layer_test.go:1250`),
   `TestNonCISTProposalCannotRestartEdgeDelay` (`S/edge_delay_test.go:76`),
   and `TestMSTISyncLeavesABoundaryPort` (`S/agreement_test.go:491`). Each
   expects an agreed port to be Discarding after the sync.
2. Medium, open. An MSTI's sync skips a boundary port. `syncTree` passes
   over it (`S/receive.go:16-18`), `isSynced` reads the CIST port's state
   and agreement in its place (`S/roles.go:17-24`), and
   `TestMSTISyncLeavesABoundaryPort` pins both. Commit `1bcf7338` removed
   the same skip from an earlier implementation of the spanning tree phase,
   where it also fired on PVST trees. For the skip: `Q2003` 13.13 f) says
   "At a Boundary Port frames allocated to the CIST and all MSTIs are
   forwarded or not forwarded alike", 13.26.9 gives every MSTI the CIST's
   `agreed` on a message from another region, and the layer mirrors the
   CIST's role and state onto a boundary MSTI port (`S/roles.go:193-205`,
   `:258-264`), so a state the sync writes there is overwritten by the next
   recompute. Against it: `Q2003` 13.26.18 and `D2009` 13.29.24 set `sync`
   "for this tree (the CIST or a given MSTI)" on every port of the bridge,
   and neither Port Role Transitions figure has a boundary condition.
3. Low, open. An MSTI proposal in a record that is not stored is ignored.
   `receiveMSTIs` leaves the loop for a record that is neither from the
   stored source nor superior (`S/receive.go:161-168`) and for one with at
   most one remaining hop (`:170-172`), and collects a proposal only after
   the store (`:193-195`). `answerProposals` at commit `1bcf7338`
   (`src/common/sim/layer/stp/agreement.go:103-112`) acted on every record
   with the Designated role, the Proposal flag, and more than one remaining
   hop. For today's code: `Q2003` Figure 13-14 and `D2009` Figure 13-20
   call the proposal procedure in SUPERIOR_DESIGNATED and
   REPEATED_DESIGNATED alone, and on InferiorDesignatedInfo `D2009` runs
   `recordDispute()` and no proposal procedure. Against it: `D2009`
   13.29.20 itself tests the Designated role and the flag and nothing else.
   No test separates the two: with a proposal also collected before the
   `continue` at `:167`, `go test ./src/common/sim/...` passes.
4. High. An inherited port priority stops following the bridge port after
   one normalize. `Config.Normalize` fills an instance or VLAN port's
   priority from the bridge port and sets `PriorityPresent`
   (`S/config.go:221-228`, `:239-246`), so a second normalize reads the
   value as the caller's. `Switch.Config` returns that normalized
   configuration (`src/common/sim/device/vswitch/switch.go:328`, `:486`).
   Whether `Fabric.Config` (`src/common/sim/fabric/fabric.go:1103`) holds
   normalized switch configurations was not traced. With the assignments at
   `S/config.go:227` and `:245` removed, `go test ./src/common/sim/...`
   fails one test, `TestInstancePortPriorityIsResolvedOnceInNormalize`
   (`S/wire_format_test.go:131`), in the two subtests that assert the flag.
5. Unverified. An agreement outlives a worse designated vector.
   `assignRoles` clears `agreed` on a role change alone
   (`S/roles.go:232-236`). `D2009` Figure 13-20 keeps it in UPDATE as
   `agreed = agreed && betterorsameInfo(Mine)`, which 13.29.1 b) makes true
   when "the designatedPriority vector is better than or the same as
   (13.10) the portPriority vector". `Q2003` Figure 13-14 has `agreed =
   agreed && betterorsameInfoXst() && !changedMaster`. Today the sync
   clears every agreement, so no sync shows the difference. Under entry 1's
   rule a port stays Forwarding on an agreement the standard has dropped.
6. Unverified. The layer computes `synced` where the standard stores it.
   `isSynced` reads it as Discarding or agreed (`S/roles.go:25`). `D2009`
   Figure 13-25 sets `synced` in DESIGNATED_SYNCED, which a Discarding port
   enters, and Figure 13-20 clears it in UPDATE and SUPERIOR_DESIGNATED as
   `synced = synced && agreed`. A Designated port that reached Forwarding
   through the forward-delay ladder with no agreement, and whose designated
   vector has not changed since, is synced in the standard and cut by the
   layer. R2's example cuts it.

### Completeness

- `S/README.md` states the stored-record rule in its MSTP section
  (`:405-407`) and the boundary rule under Limits (`:550`). It has no
  Limits entry for the cut of an agreed port or for inherited priority,
  which the spanning tree phase's last Decision says it lists.
- `PVST.Normalize` fills a VLAN tree's bridge priority from the bridge's
  and marks it present (`S/pvst.go:99-103`), which
  `S/config_test.go:1047-1048` pins. A changed bridge priority then does
  not reach an inheriting VLAN tree on a second normalize. The carried
  Decision names port priority alone (Open questions).

### Tests

- No test fails when the boundary skip also applies to a PVST tree: with
  the `l.mst != nil` operand removed at `S/receive.go:16`,
  `go test ./src/common/sim/layer/stp/` passes. Commit `1bcf7338` names
  that case as the defect of the skip it removed.
- R2's example on a PVST bridge. A VLAN 10 proposal blocks a VLAN 10
  Designated port that forwards without an agreement and leaves VLAN 20
  Forwarding. `PortInfo` carries no agreement (`S/info.go`), so an internal
  test reads `agreed`.
- The same on an MSTP bridge whose agreed Designated port is a boundary
  port, once the boundary question is answered.
- The stop condition's fixture: a Designated port holds an agreement, the
  bridge's Root port moves to a worse root, and a proposal arrives on the
  new Root port. The port must not stay Forwarding on the old agreement.
- R3's example through `Normalize` and through a layer's
  `VLANPortInfo(10, "1/1/1").Priority`.
- `TestInstancePortPriorityIsResolvedOnceInNormalize` gains a layer
  subtest: a tree port `{Priority: 128, PriorityPresent: true}` over bridge
  port priority 32 reports 128 from `VLANPortInfo(10, "1/1/1").Priority`,
  which fails if `addTree` ignores the tree port's priority. Its two
  subtests that assert `PriorityPresent` take the new rule.
- A `Fabric.Configure` or a switch reconfigure of a configuration read back
  from `Config()` with a new bridge port priority reaches the MSTI port.
- The comment of `TestMSTNormalizeDoesNotOverrideUnsetInstancePortPriority`
  (`S/mst_test.go:401-405`) names `Config.Normalize` as the reader of the
  flag.

## Open questions

- Does an MSTI's sync cut a boundary port (Correctness 2)? Recommended:
  keep the skip, pin that it stays off a PVST tree, and keep the Limits
  entry. Cutting it needs an MSTI state of its own at a boundary, which the
  role and state mirror in `S/roles.go` does not have. The user decides.
- Is an MSTI proposal in a record that is not stored acted on (Correctness
  3)? Recommended: no, as today, with a test that pins it, since both
  figures call the proposal procedure only for superior or repeated
  information, which is what the layer stores. The user decides.
- Does the agreement clear on a worse designated vector join this phase
  (Correctness 5)? Recommended: yes, since R2 keeps a port Forwarding on
  that agreement. It is a fifth behavior beside the four the user named, so
  the user decides.
- Does `PriorityPresent` stay as written for a VLAN tree's bridge priority
  as well (Completeness)? The user decides.
- Whether R2's unagreed port is cut when its designated vector has not
  changed (Correctness 6) is decided at the re-plan against the figures,
  and stated in `S/README.md` under Limits if the layer keeps its own rule.
- Unverified: the published IEEE Std 802.1Q-2011 for every clause cited
  here, as the parent's Open questions say of each protocol phase.
