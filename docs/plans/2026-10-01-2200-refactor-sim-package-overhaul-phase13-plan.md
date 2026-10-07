---
title: Spanning Tree Handshake Scope and Inherited Priority - Plan
type: refactor
date: 2026-10-07
artifact_contract: flowseer-plan/v2
execution: code
---

# Spanning Tree Handshake Scope and Inherited Priority - Plan

## Goal

A proposal on a tree's Root or Alternate port cuts only the Designated ports
of that tree that are not synced, an MSTI's boundary port included, and a
tree port that sets no priority of its own follows the bridge port each time
the configuration is normalized. The means is a stored `synced` flag per
tree port that the layer keeps the way the Port Information and Port Role
Transitions machines do, and a `Config.Normalize` that leaves
`PriorityPresent` as the caller wrote it. Stop condition: if holding an
MSTI's cut at a boundary port needs that MSTI port to forward while the
CIST's port there is Discarding, the CIST mirror no longer bounds the
change, and the phase returns to the user with U1, U3, and U4 landed.

## Decisions

"Rules" is the list under Inventory that cites each rule.

- The parent's Decisions apply.
- This phase holds four behaviors of `layer/stp`: which Designated ports a
  proposal cuts (one holding an agreement keeps it), the same sync at a
  boundary port, an MSTI proposal in a record that is not stored, and an
  inherited port priority that follows the bridge port. The spanning tree
  phase, landed 2026-10-07, left them to a later phase, and
  `src/common/sim/layer/stp/README.md` lists each under Limits.
  (decided by the user, 2026-10-07)
- A sync cuts a Designated port only when it is not synced, and `synced` is
  stored per tree port, for the CIST, an MSTI, and a PVST VLAN tree alike.
  A port is synced while it is Discarding, holds an agreement, or is an
  edge. It stays synced after it leaves Discarding, and loses that when its
  designated priority vector changes while it holds no agreement, or when
  it takes the Designated role from another one. Why: Rules 1 and 2. Today
  `syncTree` cuts every non-edge Designated port and `isSynced` computes
  the flag as Discarding or agreed (Correctness 1 and 6). A port that ends
  the ladder holds no agreement, as in the 2003 edition and the layer
  today. The draft gives it one (Rules 2).
- An agreement survives a change of the port's designated priority vector
  only when the new vector is better than or the same as the one it
  replaces. This joins the phase although it is not one of the four named
  behaviors. Why: Rules 3. A port that keeps its agreement through a sync
  would otherwise forward on an agreement its peer gave to a better vector,
  which makes the sync rule unsafe alone. The rule shares one site with
  the `synced` rule, the point where a tree's Designated ports take a new
  vector.
- A change of the designated times alone leaves `synced` and the agreement
  as they are. This is the layer's own rule, and U5 states it under Limits.
  Why: the draft runs UPDATE for it (Rules 4), the layer stores no times
  for a Designated port, and a timer change moves no port's role.
- An MSTI's sync takes in a boundary port that holds the Designated role.
  The port's MSTI state is then Discarding while the CIST forwards there,
  until the MSTI port holds an agreement or its own forward-delay ladder
  ends. Why: Rules 5. The sync is set on every port of the bridge, an MSTI
  has a role of its own where the CIST is Designated, and the 2003 text
  says an MSTI's Port State may differ to suppress temporary loops. The
  agreement reaches the MSTI port as today, copied from the CIST's on a
  BPDU from another region (Rules 6).
- The CIST mirror stays for everything else at a boundary port: the role,
  the state while no MSTI cut holds, and the CIST's own cut reaching the
  boundary MSTIs (`src/common/sim/layer/stp/receive.go:38-58`). A boundary
  port that holds the CIST's Root role, the standard's Master Port, is not
  cut by an MSTI's sync, which U5 states under Limits as the layer's own
  rule. Why: the layer has no Master role and no `synced` for a Root port,
  which the draft's condition for forwarding a Master Port again reads
  (Rules 7).
- An MSTI proposal in a record that is not stored is not acted on, as
  today. Why: Rules 8. Both figures record a proposal for superior or
  repeated information alone, which is what the layer stores. The layer's
  test for the same source is narrower than the standard's, and U5 states
  that under Limits.
- A record the layer discards for its hop count carries no proposal either.
  This is the layer's own rule, and U5 states it under Limits. Why: the
  layer discards information at the hop bound before storing it
  (`src/common/sim/layer/stp/receive.go:456-460`, `:170-172`), where the
  2003 edition stores it and ages it at once (Rules 9), and neither
  document fixes whether the port acts on the proposal first.
- An instance or VLAN port that sets no priority of its own gets the
  bridge port's from `Config.Normalize`, and `PriorityPresent` stays as
  the caller wrote it, so normalizing again inherits again. This amends
  U3's "marks it present". Why: `Switch.Config` and `Fabric.Config` return
  normalized configurations, and a caller that reads one back and changes
  the bridge port priority must see the tree ports follow it. `Diff`
  reports the effective change on each inheriting tree port. (decided by
  the user, 2026-10-05)
- The switch composition phase does not wait on this one, and no test of
  this phase is written against a switch or a fabric. Why: with the cut
  limited to ports that hold no agreement, and with `PriorityPresent` left
  alone, every test under `src/common/sim` outside `layer/stp` passes
  (Correctness 1 and 4). The device path adds no rule (Completeness), and
  a test there would edit files that phase rewrites in the same wave.

## Requirements

R1. Every Correctness entry ends as a test that fails before its fix, or as
a test that pins today's behavior and fails under the mutation the entry
names. Example: with a proposal also collected before the `continue` at
`src/common/sim/layer/stp/receive.go:167`, U3's test fails.

R2. A proposal leaves a synced port forwarding and cuts one that is not.
Example: a PVST bridge whose ports have each stored a VLAN 1 BPDU has, on
its VLAN 10 tree, a Root port `p1`, a Designated port `p2` that forwards on
an agreement, and a Designated port `p3` that reached Forwarding through
the forward-delay ladder. A VLAN 10 BPDU arrives on `p1` from the stored
designated port with a lower root path cost and the Proposal flag. After
the call `p2` is Forwarding with its agreement, `p3` is Discarding, and
every VLAN 20 port is as it was. With the stored root path cost in that
BPDU, `p3` is Forwarding after the call.

R3. An agreement does not outlive a worse designated vector. Example: the
BPDU of R2 arrives with a higher root path cost. After the call `p2` holds
no agreement and is Discarding.

R4. An MSTI's sync cuts a boundary port for that MSTI alone. Example: an
MSTP bridge has an internal Root port `p1` and a boundary port `p2` that is
Designated and forwards for the CIST and for MSTI 1 without an agreement.
A BPDU arrives on `p1` with unchanged CIST fields and an MSTI 1 record that
names a better regional root with the Proposal flag. After the call
`VLANPortInfo(10, "p2").State` is Discarding, `PortInfo("p2").State` is
Forwarding, and the MSTI 1 record sent on `p1` carries the Agreement flag.
The MSTI state returns to Forwarding through Learning as `Advance` steps
the port's forward-delay ladder.

R5. A record that is not stored starts no handshake. Example: MSTI 1's port
`p2` is Alternate on a record stored from bridge B. A BPDU from bridge C
arrives on `p2` whose MSTI 1 record is worse than the stored one and has
the Designated role and the Proposal flag. The call's `Effects` hold no
frame for `p2`, and every MSTI 1 port has the state it had.

R6. An inherited port priority follows the bridge port. Example: a
configuration whose MSTI 1 port `1/1/1` sets only a path cost is
normalized, its bridge port `1/1/1` priority is changed from 128 to 64, and
it is normalized again. The MSTI 1 port's priority is 64 with
`PriorityPresent` false, and the layer built from it reports 64 for that
port on VLAN 10.

R7. `src/common/sim/layer/stp/README.md` states each behavior as the layer
has it after the phase. Example: the Limits entry that begins "A proposal
cuts every other non-edge Designated port" is gone.

## Out of scope

- A VLAN tree's bridge priority, which stops following the bridge's after
  one normalize (Completeness). The Decision on inheritance names port
  priority alone. U5 adds it to Limits, and Open questions holds it.
- The Master role, the disputed flag, and a frame sent at once when a
  Designated port's vector changes. Limits in
  `src/common/sim/layer/stp/README.md` states each as absent.
- The `agree` flag of a port that has sent an Agreement. The layer has
  none, so every proposal on a Root or Alternate port starts a sync, where
  the figures need `proposed && !agree` (Rules 10). U5 adds it to Limits.
- A test against `vswitch` or `fabric` (Decisions).

## Inventory

Paths are as of commit `f4befaf4`. `S` is `src/common/sim/layer/stp`. Each
"with ..." sentence names an edit that was applied, tested with the command
it names, and reverted.

### Sources

- `Q2003`: IEEE Std 802.1Q, 2003 Edition, clauses 13 and 14,
  https://bittwist.sourceforge.io/doc/802.1Q-2003.pdf. Page numbers are the
  printed ones, 15 less than the PDF's.
- `D2009`: P802.1aq/D1.5 with suggested changes, 12 May 2009, clause 13,
  https://www.ieee802.org/1/files/public/docs2009/aq-seaman-merged-spanning-tree-protocols-0509.pdf.
  An unapproved working draft. Page numbers are the printed ones, 2 more
  than the PDF's.
- `UNH`: UNH-IOL Rapid Spanning Tree Conformance Test Suite,
  https://www.iol.unh.edu/sites/default/files/testsuites/bfc/RSTP_conformance_Q.pdf.
  Its text holds no test of a sync against a second Designated port: the
  word appears once, in the Migration Delay discussion (PDF page 44).
- Not read: the published IEEE Std 802.1Q-2011. A rule below that cites
  `D2009` alone rests on the draft: Rules 4 and 7, and the g) to k) half
  of Rules 5.

`Q2003` Figures 13-14 and 13-17 and `D2009` Figures 13-20 and 13-23 to
13-26 were read from the rendered pages, so each condition is the one drawn
on its transition.

### Rules

1. A sync discards a Designated port only when it is not synced. `D2009`
   Figure 13-25 (page 79) enters DESIGNATED_DISCARD on `((sync && !synced)
   || (reRoot && (rrWhile != 0)) || disputed || isolate) && !operEdge &&
   (learn || forward)`. `Q2003` Figure 13-17 (page 204) enters LISTEN on
   `(learn || forward) && !operEdge && (role != RootPort) && ((sync &&
   !synced) || (reRoot && (rrWhile != 0)))`. `Q2003` 13.24.27 (page 189)
   and `D2009` 13.27.65 (page 57) have `sync` discard a port only "if the
   Port is not already synchronized". ROOT_PROPOSED (`D2009` Figure 13-24,
   page 78) and ALTERNATE_PROPOSED (Figure 13-26, page 79) call
   `setSyncTree()`.
2. `synced` is stored. `D2009` Figure 13-25 enters DESIGNATED_SYNCED, which
   sets it, on `(!learning && !forwarding && !synced) || (agreed &&
   !synced) || (operEdge && !synced) || (sync && synced)`. `Q2003` Figure
   13-17 enters SYNCED on the same terms, with `&& (role != RootPort)` on
   the first, so a Discarding Root port is not synced by it. `D2009` Figure
   13-20 (page 75) clears the flag in UPDATE as `synced = synced &&
   agreed`. `Q2003` Figure 13-14 (page 201) has `synced = FALSE` there,
   after which SYNCED sets it again for a port whose agreement UPDATE kept.
   The two differ on a port that ends the ladder: `D2009` DESIGNATED_FORWARD
   has `agreed = sendRSTP`, `Q2003` FORWARD assigns no `agreed`, and the
   layer's ladder leaves it false (`S/advance.go:148-158`). The plan follows
   `Q2003` and the layer, so a later vector change costs such a port its
   `synced` where the draft's port keeps both.
3. `D2009` Figure 13-20 UPDATE has `agreed = agreed &&
   betterorsameInfo(Mine)`, and 13.29.1 b) (page 62) makes that true when
   "the designatedPriority vector is better than or the same as (13.10)
   the portPriority vector". `Q2003` Figure 13-14 has `agreed = agreed &&
   betterorsameInfoXst() && !changedMaster`.
4. `D2009` 13.29.33 k) (page 70) sets `updtInfo` for a Designated port when
   "the port priority vector differs from the designated priority vector
   or the port's associated timer parameter(s) differ(s) from the Root
   Port's associated timer parameters".
5. `D2009` 13.29.24 (page 66) and `Q2003` 13.26.18 (page 194) set `sync`
   "for this tree (the CIST or a given MSTI)" for all ports of the bridge.
   `D2009` 13.29.33 g) and h) (page 69) take an MSTI's role from the CIST's
   only where the CIST's port priority was received from another region,
   the Root and Alternate cases, and k) (page 70) makes every other port
   Designated by the MSTI's own vectors. Neither Port Role Transitions
   figure has a boundary condition. `Q2003` 13.24.25 NOTE (page 189) says
   "The Port State for each MSTI may differ for each MSTI as required to
   suppress temporary loops". `Q2003` 13.13 f) (page 169) has a Boundary
   Port forward the CIST and all MSTIs alike, under Stable Connectivity,
   which 13.13 opens as the case where spanning tree information "is not
   changing on any Bridge Port".
6. `D2009` 13.29.16 (page 65) and `Q2003` 13.26.9 (page 193) give every
   MSTI the CIST's `agreed` on a message from another region. The layer
   does it at `S/receive.go:557-570`.
7. `D2009` Figure 13-23 (page 77) enters MASTER_DISCARD on `sync &&
   !synced` and leaves for MASTER_LEARN and MASTER_FORWARD on `(fdWhile ==
   0) || allSynced`. 13.28.2 b) 4) (page 59) makes `allSynced` true for a
   Master Port when `synced` is true for every other port of the tree, the
   Root port among them, which Figure 13-24 (page 78) sets only on `agreed
   && !synced` or `sync && synced`. The standard's Master Port can
   therefore stay Discarding for its ladder, and the layer's does not.
8. `D2009` Figure 13-20 calls `recordProposal()` in SUPERIOR_DESIGNATED and
   REPEATED_DESIGNATED alone and `recordDispute()` in INFERIOR_DESIGNATED.
   `Q2003` Figure 13-14 calls `recordProposalXst()` in the same two states.
   `D2009` 13.29.13 (pages 63 and 64) returns SuperiorDesignatedInfo for a
   Designated message whose priority is superior, and 13.11 (pages 23 and
   24) makes an MSTI message superior when it is better or its "Designated
   Bridge Identifier Bridge Address and Designated Port Identifier Port
   Number components are the same". The layer stores a record that is
   better or whose whole bridge and port identifiers match, priority bits
   included (`S/receive.go:142-161`, and `:494` for the CIST), so a sender
   that changes a priority and sends a worse vector is stored by the
   standard and not by the layer. `D2009` 13.29.20 (page 66) tests the role
   and the flag and nothing else. It describes the procedure, and the
   figure decides when it runs.
9. `Q2003` 13.26.24 (page 196) sets an MSTI's `rcvdInfoWhile` to "zero, if
   mstiPortTimes remainingHops is less than or equal to zero", after
   setting that count to the received value less one, and 13.26.23 e) (page
   196) does the same for internal CIST information. `D2009` 13.29.32 (page
   68) takes the count from the CIST's `portTimes` alone, so the draft
   gives no rule for a record's own count.
10. `D2009` Figures 13-24 and 13-26 enter ROOT_PROPOSED and
    ALTERNATE_PROPOSED on `proposed && !agree`, and `Q2003` Figure 13-17
    enters PROPOSED on the same. `agree` is the agreement the port has
    sent, which Figure 13-20 keeps through superior information as `agree =
    agree && betterorsameInfo(Received)`. The layer answers every proposal
    on a Root or Alternate port (`S/receive.go:642-665`).

### Correctness

1. High. A proposal cuts a Designated port that holds an agreement.
   `syncTree` clears `agreed` and sets Discarding on every non-edge
   Designated port of the tree (`S/receive.go:24-37`). With `&&
   !otherP.agreed` added to the test at `:24`, `go test
   ./src/common/sim/...` fails four tests, all in `S`:
   `TestSyncCutRetainsTopologyActivity` (`S/topology_activity_test.go:11`),
   `TestSyncCutRestartsAutoEdgeDelay` (`S/layer_test.go:1250`),
   `TestNonCISTProposalCannotRestartEdgeDelay` (`S/edge_delay_test.go:76`),
   and `TestMSTISyncLeavesABoundaryPort` (`S/agreement_test.go:491`). Each
   expects an agreed port to be Discarding after the sync.
2. Medium. An MSTI's sync skips a boundary port. `syncTree` passes over it
   (`S/receive.go:16-18`), `isSynced` reads the CIST port's state and
   agreement in its place (`S/roles.go:17-24`), and
   `TestMSTISyncLeavesABoundaryPort` pins both. `updatePortStates` writes
   the CIST's state onto a boundary MSTI port on every recompute
   (`S/roles.go:258-264`), so a cut written there alone is overwritten.
3. Low. An MSTI proposal in a record that is not stored is ignored.
   `receiveMSTIs` leaves the loop for a record that is neither from the
   stored source nor superior (`S/receive.go:161-168`) and for one with at
   most one remaining hop (`:170-172`), and collects a proposal only after
   the store (`:193-195`). No test pins it: with a proposal also collected
   before the `continue` at `:167`, `go test ./src/common/sim/layer/stp/`
   passes.
4. High. An inherited port priority stops following the bridge port after
   one normalize. `Config.Normalize` fills an instance or VLAN port's
   priority from the bridge port and sets `PriorityPresent`
   (`S/config.go:221-228`, `:239-246`), so a second normalize reads the
   value as the caller's. With the assignments at `:227` and `:245`
   removed, `go test ./src/common/sim/...` fails one test,
   `TestInstancePortPriorityIsResolvedOnceInNormalize`
   (`S/wire_format_test.go:131`), in the two subtests that assert the flag.
5. High once entry 1 is fixed. An agreement outlives a worse designated
   vector. `assignRoles` clears `agreed` on a role change alone
   (`S/roles.go:232-236`). Today the sync clears every agreement, so no
   sync shows the difference.
6. Medium. The layer computes `synced` where the standard stores it
   (`S/roles.go:25`). A Designated port that reached Forwarding through the
   forward-delay ladder, and whose designated vector has not changed since,
   is synced in both documents and cut by the layer.

### Completeness

- `S/README.md` lists the four behaviors under Limits at `:544`, `:551`,
  `:552`, and `:559`, and each path and line range there matches the tree.
  The entry at `:556`, an MSTI Root port that cannot complete its sync
  while a boundary port forwards for the CIST without an agreement, stops
  being true with U2. The MSTP section describes the boundary sync at
  `:419-423`.
- `PVST.Normalize` fills a VLAN tree's bridge priority from the bridge's
  and marks it present (`S/pvst.go:99-103`), which
  `S/config_test.go:1047-1048` pins. A changed bridge priority then does
  not reach an inheriting VLAN tree on a second normalize (Out of scope).
- The Decision on inheritance is carried word for word from the spanning
  tree phase's plan as commit `bc06df79` holds it. Its U3 is that plan's
  unit "Wire format and configuration identity", whose Change has
  `Config.Normalize` mark the inherited value present.
- The device path normalizes through the layer and adds no rule.
  `vswitch.Config.Normalize` calls the layer's
  (`src/common/sim/device/vswitch/config.go:552`), a switch stores that
  result (`src/common/sim/device/vswitch/switch.go:85`) and returns it from
  `Config` (`:486`), and a fabric normalizes each switch the same way
  (`src/common/sim/fabric/fabric.go:395`, `:411`).

### Tests

- No test fails when the boundary skip also applies to a PVST tree: with
  the `l.mst != nil` operand removed at `S/receive.go:16`, `go test
  ./src/common/sim/layer/stp/` passes. R2's fixture gives every port a
  stored VLAN 1 BPDU for this reason, since that is what marks a PVST
  bridge's ports external.
- `PortInfo` carries no agreement and no `synced` (`S/info.go`), so a test
  that reads either is internal to the package.
- The comment of `TestMSTNormalizeDoesNotOverrideUnsetInstancePortPriority`
  (`S/mst_test.go:401-405`) names the layer as the reader of the flag. The
  reader is `Config.Normalize`.

## Units

### U1. A sync cuts only the Designated ports that are not synced
Files: src/common/sim/layer/stp/portstate.go, src/common/sim/layer/stp/receive.go, src/common/sim/layer/stp/roles.go, src/common/sim/layer/stp/link.go, src/common/sim/layer/stp/advance.go, src/common/sim/layer/stp/sync_internal_test.go, src/common/sim/layer/stp/link_state_internal_test.go, src/common/sim/layer/stp/topology_activity_test.go, src/common/sim/layer/stp/layer_test.go, src/common/sim/layer/stp/edge_delay_test.go, src/common/sim/layer/stp/agreement_test.go
After: none
Change: a tree port stores `synced` and the designated priority vector it
last held as a Designated port. A Designated port is synced while it is
Discarding, holds an agreement, or is an edge, and stays synced after it
leaves Discarding. When role assignment leaves a port Designated with a
vector that differs from the stored one, `agreed` stays only if the new
vector is better than or the same as the stored one, and `synced` stays
only if `agreed` does or the port is Discarding. A port that takes the
Designated role from another one loses its agreement as today, and is
synced only if it is Discarding. `syncTree` passes over a Designated port
that is synced and not Discarding, leaving its agreement, state, and
proposing flag, and treats every other non-edge Designated port as it does
today. `isSynced` reads the stored flag for a Designated port. The boundary
branches of both functions stay as they are until U2.
Tests: `sync_internal_test.go` holds R2 in both forms and R3, reading
`agreed` from the port state, and repeats R2's first form on an MSTP
bridge's CIST and on an MSTI's internal ports. One case sends R3's BPDU
with no Proposal flag and expects `p2` Forwarding with no agreement, which
separates the vector rule from the cut. Another has a ladder-forwarded
Designated port become Root and return to Designated while Forwarding,
asserts its stored `synced` is false, and expects a repeated proposal to
cut it, which fails if the role change keeps the flag. The four tests of
Correctness 1 keep their subject and move their fixture to a port the rule
cuts. So do `TestSyncTreeRequestsTheCutTree` and
`TestSyncTreeRequestsBoundaryMSTIInformation`
(`src/common/sim/layer/stp/link_state_internal_test.go:588`, `:652`), which
set a port Forwarding by hand and must also clear its `synced`.
`TestMSTISyncLeavesABoundaryPort` keeps its boundary assertions until U2.
Nothing covers a change of the designated times alone, which the layer
does not see.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/layer/stp/portstate.go src/common/sim/layer/stp/receive.go src/common/sim/layer/stp/roles.go src/common/sim/layer/stp/link.go src/common/sim/layer/stp/advance.go src/common/sim/layer/stp/sync_internal_test.go src/common/sim/layer/stp/link_state_internal_test.go src/common/sim/layer/stp/topology_activity_test.go src/common/sim/layer/stp/layer_test.go src/common/sim/layer/stp/edge_delay_test.go src/common/sim/layer/stp/agreement_test.go`

### U2. An MSTI's sync takes in a boundary port
Files: src/common/sim/layer/stp/portstate.go, src/common/sim/layer/stp/receive.go, src/common/sim/layer/stp/roles.go, src/common/sim/layer/stp/advance.go, src/common/sim/layer/stp/agreement_test.go, src/common/sim/layer/stp/link_state_internal_test.go, src/common/sim/layer/stp/sync_boundary_test.go
After: U1
Change: `syncTree` no longer passes over a boundary port for an MSTI. A
boundary port that holds the Designated role and is not synced for the MSTI
is cut for it: the MSTI port loses its agreement, proposes, and is
Discarding. While that cut holds, `updatePortStates` gives the MSTI port
the less forward of the CIST's state and its own, and its own steps the
forward-delay ladder. The cut ends when the MSTI port holds an agreement or
its ladder reaches Forwarding, and the port reads the CIST's state again.
U1's vector rule runs for a boundary MSTI port as for any other, and
`isSynced` reads the MSTI port's own flag there in place of the CIST's
state and agreement. A boundary port with any other role, and a PVST tree,
are untouched: every boundary branch keeps its `l.mst != nil` operand.
Tests: `sync_boundary_test.go` holds R4, stepping one forward delay at a
time and asserting Learning before Forwarding. A second case ends the cut
with a BPDU from the other region on `p2` that carries the Agreement flag
and expects the MSTI state Forwarding in that call. A third repeats the
stored MSTI record with the Proposal flag and expects `p2` Forwarding for
MSTI 1 and the Agreement flag on the record sent on `p1`, which is
`TestMSTISyncLeavesABoundaryPort` with its Agreement expectation reversed.
A fourth makes `p2` the CIST's Root port at the boundary and expects its
MSTI state unchanged by the sync. Nothing here covers an MSTI port that
forwards while the CIST's is Discarding, which the kept mirror rules out.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/layer/stp/portstate.go src/common/sim/layer/stp/receive.go src/common/sim/layer/stp/roles.go src/common/sim/layer/stp/advance.go src/common/sim/layer/stp/agreement_test.go src/common/sim/layer/stp/link_state_internal_test.go src/common/sim/layer/stp/sync_boundary_test.go`

### U3. A record that is not stored starts no handshake
Files: src/common/sim/layer/stp/receive_msti_proposal_test.go
After: none
Change: no code changes. The behavior of
`src/common/sim/layer/stp/receive.go:161-172` gains the test it lacks.
Tests: `receive_msti_proposal_test.go` holds R5, and a second case in which
the stored source repeats its record with one remaining hop and the
Proposal flag, with the same two expectations. The CIST's port `p2` is
Alternate in both, so the CIST requests no frame of its own. Each case is seen to fail
with the proposal collected before the `continue` it pins, at `:167` and at
`:171`, before the mutation is reverted. The assertion is on the frames of
the call, since a port's state alone would also hold if the proposal were
answered and found nothing to cut.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/layer/stp/receive_msti_proposal_test.go`

### U4. An inherited port priority follows the bridge port
Files: src/common/sim/layer/stp/config.go, src/common/sim/layer/stp/wire_format_test.go, src/common/sim/layer/stp/mst_test.go
After: none
Change: `Config.Normalize` writes the bridge port's priority into an
instance or VLAN port whose `PriorityPresent` is false and leaves the flag
false. A tree port the bridge has no port for takes `DefaultPortPriority`
the same way. `Diff`, `RetentionKey`, and `addTree` read the filled value
as they do today.
Tests: `TestInstancePortPriorityIsResolvedOnceInNormalize` takes the new
rule in the two subtests that assert the flag, and gains R6 through
`Normalize` and through `VLANPortInfo(10, "1/1/1").Priority` for an MSTI
port and a VLAN tree port. A further subtest builds a layer from a tree
port `{Priority: 128, PriorityPresent: true}` over bridge port priority 32
and expects 128, which fails if the inheritance overwrites a priority the
caller set. One more expects `Diff` between the two normalized
configurations of R6 to report the MSTI port's priority from 128 to 64.
The comment of `TestMSTNormalizeDoesNotOverrideUnsetInstancePortPriority`
names `Config.Normalize` as the reader of the flag.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/layer/stp/config.go src/common/sim/layer/stp/wire_format_test.go src/common/sim/layer/stp/mst_test.go`

### U5. The README states the behaviors as the layer has them
Files: src/common/sim/layer/stp/README.md
After: U1 U2 U3 U4
Change: the Port Role Transitions text states the sync rule, the stored
`synced` flag, and the agreement rule on a vector change, each with its
figure from Rules. The MSTP section's boundary paragraph states the MSTI
cut at a Designated boundary port and how it ends. Limits loses the entries
at `:544`, `:556`, and `:559`. The entry at `:552` names `D2009` Figure
13-20 and the hop-count rule, and the one at `:551` becomes the CIST's cut
reaching the boundary MSTIs. Limits gains a change of the designated times
alone, a boundary port that holds the CIST's Root role, a VLAN tree's
inherited bridge priority, the missing `agree` flag, the same-source test
of Rules 8, and the ladder's missing agreement of Rules 2. Every `path:line` in an edited entry is read
from the tree after U1 to U4.
Tests: `uv run tools/scripts/run.py verify check-prose` on the README.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/layer/stp/README.md`

Waves: U1 U3 U4 | U2 | U5

## Verification

- `go test -race ./src/common/sim/...` and the verifier on every changed
  path. R1 holds for each new test.

## Definition of done

- [ ] The verifier is green for every changed path, and
  `src/common/sim/layer/stp/README.md` matches the layer (R7).
- [ ] The commit body names each expectation outside `layer/stp` that moved.
- [ ] The outcome is recorded with `uv run tools/scripts/run.py plan record
  implemented <plan> --units 5 --from <t> --to <t>` or `partial`.
- [ ] No plan label appears in code, comments, or commit messages.

## Open questions

- Does a VLAN tree's inherited bridge priority follow the bridge's through
  every normalize, as a tree port's priority does after U4? No unit needs
  the answer. Following it changes what `PVST.Normalize` returns, which
  `src/common/sim/device/vswitch/config.go:426` reads. Leaving it keeps a
  changed bridge priority from reaching an inheriting VLAN tree in a
  configuration that was read back. The user decides, outside this phase.
- Unverified: the published IEEE Std 802.1Q-2011 for every clause cited
  here, as the parent's Open questions say of each protocol phase.
