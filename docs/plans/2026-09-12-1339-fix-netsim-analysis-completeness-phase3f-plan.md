---
title: Network Simulation Analysis Completeness, Phase 3f - Plan
type: fix
date: 2026-09-16
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept after fixes
compound: docs/solutions/architecture-patterns/one-slot-two-roles-is-a-defect-class-not-a-defect.md
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
parent: docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
---

# Network simulation analysis completeness, phase 3f: the link and VLAN 1 share one slot - Plan

> Implemented. 6 units, 2026-09-16T17:50:08Z to 2026-09-16T18:48:27Z.

## Goal

Per-VLAN spanning tree put VLAN 1's tree in the CIST slot, and that slot now
carries two unrelated things: VLAN 1's tree, and every property that belongs to
the link rather than to any tree. Nothing in the code distinguishes the two
roles, so every defect two review rounds found is the same mistake — reading or
writing one role's field through the other role's handle. This phase names the
two roles and makes three properties fail a test when violated, instead of
patching the instances. The means are a tree lookup that can answer "no tree",
a named `linkState` whose membership is enumerated by a test, and one SSTP
entry point that always runs the link half of a receive.

This plan is wrong if the link role and the tree role cannot be separated
inside one `portState`: if a field must be simultaneously replicated and
CIST-only, or if the classification rule below places a field nowhere, the
`portState` split named under Alternatives is the phase instead.

This phase claims no new parent requirement. It corrects phase 3e, which
claimed parent R14c, R14d, R15b and R39.

## Decisions

Phase 3e's decisions stand and are not repeated. VLAN 1's tree stays in the
CIST slot; PVST stays RSTP per VLAN; the mode stays the presence of
`Config.PVST`.

### The three properties

- **The link half of a receive runs for every BPDU that reaches the layer**,
  whatever tree it names and whether or not a tree applies it, and what it
  changed reaches every tree before the frame is judged. Why: BPDU guard, the
  loop-guard clear, protocol migration and auto-edge loss are what make the
  port's own state trustworthy, and a caller that can skip them by declining to
  call the layer has a hole shaped exactly like round 2's finding A.
- **No lookup by VLAN silently answers with another VLAN's tree.** Why: under
  PVST the CIST is VLAN 1's tree and nothing else, so `treeFor`'s fallback is a
  wrong answer rather than a default. Outside PVST the CIST does carry every
  unclaimed VLAN, so the fallback is correct there and stays.
- **Every property of the link reaches every tree, by one rule, and every
  reader of a given property uses the same copy.** Why: a property that lives
  only on the CIST's port state is invisible to the trees that read their own,
  and `sendRSTP` is the fourth such field to be found by inspection rather than
  by a test. The second half matters as much as the first: `blockReason` and
  `rxBPDUs` are correct on the CIST and read from the wrong copy.

### The receive seam moves inside the layer

- **`ReceiveSSTP` takes the bridge's admission answer and returns what it did;
  the switch stops gating the call.** Shape:
  `ReceiveSSTP(now time.Time, port string, arrival SSTPArrival, b BPDU) (Effects, SSTPOutcome)`
  where `SSTPArrival` carries `ArrivalVID`, `TLVVID` and `Admitted`. Why a
  parameter rather than a VLAN table on the layer: `stp` has no VLAN table, and
  giving it one would put a second admission rule beside `bridge`'s, which is
  the defect round 2 reported as finding F. Why an outcome rather than a bare
  `Effects`: the switch has to render a step that says which of seven things
  happened, and today it renders a decode failure for a frame that decoded.
- **The layer decides in one order, and the outcome names the first thing that
  stopped it:** `receiveLink` first, so a guard that fires yields `SSTPGuarded`;
  then the non-PVST bridge, `SSTPBoundary`; then `!arrival.Admitted`,
  `SSTPNotAdmitted`; then a tree-lookup miss, `SSTPUntrackedVLAN`; then a TLV
  disagreement, `SSTPPVIDInconsistent`; otherwise `SSTPApplied`. Why guard
  first: the frame is evidence a bridge is on the port whatever VLAN it claims.
  The PVST boundary mark is still set on a non-PVST bridge even when the guard
  fired, because the mark is about the neighbour and the outcome is about the
  frame.

### The bridge's ingress admission rule gets a name

- **`bridge.VLAN` gains
  `AdmitsVIDOnIngress(port string, vid vlan.ID, tagged bool) bool`, and
  `interceptSSTP` calls it.** Why on `VLAN` rather than on `Switchport`: the
  rule `Bridge.Ingress` applies needs the VLAN table, which a `Switchport`
  method cannot see. The predicate is, in full: a tunnel port answers `false`;
  `Admission` refuses a tagged frame under `UntaggedAndPriorityTaggedOnly` and
  an untagged one under `TaggedOnly`; an untagged frame needs `PVID != nil` and
  `vid == *PVID`; `IngressFiltering` additionally requires `vid` in `Tagged` or
  `Untagged`; and `vid` must be in `VLAN.Table`. A port absent from
  `Switchports` is judged against the zero `Switchport`, which is what
  `Bridge.Ingress` does (`bridge/bridge.go:527`), so an untagged frame there
  fails on the missing PVID and a tagged one passes on the table alone.
- **A tunnel port answers `false`, and an SSTP frame arriving on one is not
  admitted.** Why: `Bridge.Ingress` classifies every frame on a tunnel port
  into the service VLAN and filters the outer tag against `CustomerVIDs`
  (`bridge/bridge.go:536-557`), so the VLAN an SSTP TLV names inside a tunnel
  belongs to the customer's spanning tree, not this bridge's. Modelling a
  customer tree through a provider tunnel is not this phase.
- **Agreement with `Bridge.Ingress` is proved by a test, not by shared code.**
  Why: extracting the predicate out of `Ingress` means unpicking it from the
  tunnel, admission-policy and priority-tag branches it is interleaved with,
  which is a larger change than this phase should carry against landed
  behavior. The test drives both over the same table and fails when either
  drifts.
- **Deleting round 1's `CarriesVID` refusal widens what the layer accepts, and
  that is the intended direction.** `IngressFiltering` is false by default
  (`bridge/config.go:97-105`), so after this phase a BPDU tagged for any VLAN
  in `VLAN.Table` is admitted on any non-tunnel port and drives that VLAN's
  tree, where `CarriesVID` refused it. Why accept that: a port that forwards
  VLAN 30 data frames without filtering is a port on which VLAN 30 exists, and
  a spanning tree stricter than the data path is precisely the mismatch that
  produced findings A, B and F. The lever for an operator who wants the old
  behavior is `IngressFiltering`, which now governs both.
- Round 1's `CarriesVID` call is not merely the wrong side of the port: it is a
  second admission policy. It is stricter than ingress on an access port whose
  PVID is not also in `Untagged` (`CarriesVID` excludes PVID by its own doc
  comment, `bridge/config.go:107`) and it ignores `Admission`, the table, and
  `IngressFiltering` alike. Deleting it removes the policy rather than
  correcting it.

### Link properties are a named set with five classes

- **`portState` embeds `linkState{up, pointToPoint, edge, sendRSTP}`, and
  `syncInstancePorts` assigns the whole struct.** Why a struct: a field added
  to it is replicated by construction, so the propagation cannot be forgotten
  one field at a time.
- **`receiveLink` ends with an unconditional `l.syncInstancePorts(p.name, p)`.**
  Why: today it syncs only inside the `wasAutoEdge` branch
  (`stp/layer.go:1904-1916`), so a protocol migration reaches no other tree
  even once `sendRSTP` is replicated. Without this the property is a struct
  field that nothing carries.
- **Every field of `portState` is classified by a test into one of five
  classes, and the test fails on a field with no entry.**
  - `link-replicated`: inside `linkState`, assigned wholesale by
    `syncInstancePorts`. Members: `up`, `pointToPoint`, `edge`, `sendRSTP`.
  - `link-on-cist`: written only on the CIST's port state, and read through
    `l.cist()` by every reader. Members: `linkPathCost`, `external`,
    `bpduGuardDisabled`, `loopInconsistent`, `pvstBoundary`, `mdelayWhile`,
    `rxBPDUs`, `badBPDUs`.
  - `link-derived`: `pathCost` alone, copied from `cistP.linkPathCost` only
    when `!pathCostFixed`.
  - `tree-owned`: each tree writes and reads its own. `syncInstancePorts` may
    additionally clear a `tree-owned` field on the link-down branch, which the
    table records as a flag rather than as a class. Members: `portID`,
    `pathCostFixed`, `role`, `state`, `pvidInconsistent`, `proposing`,
    `agreed`, `fwdDelayTimer`, `edgeDelayWhile`, `forwardTransitions`,
    `txBPDUs`, the eight `rcv*` fields, and `rcvInfoValid`.
  - `port-constant`: set once at construction, identical in every tree, never
    assigned again. Members: `name`, `cfg`, `adminEdge`.
  A field the implementer cannot place is this plan's stop condition, not a
  judgment call.
- **A `link-on-cist` field is read through the CIST by every reader, not only
  by `recompute`.** Three readers reach for the tree's own copy today:
  `portState.blockReason` (`layer.go:274-285`), `portInfo`'s `RxBPDUs`
  (`layer.go:807`), and `recompute`'s root-election loop (`layer.go:1498`,
  `:1506`). `blockReason` becomes `(l *Layer) blockReason(p, cistP *portState)`
  with its precedence unchanged; `portInfo` takes the CIST's port for the two
  counters; the election loop reads `cistP` the way the role switch below it
  already does. `BadBPDU` stops walking every tree and bumps the CIST alone,
  so the two counters answer by one rule.

### A migrated port sends no SSTP frame at all

- **When a port's `sendRSTP` is false, a PVST bridge sends VLAN 1's untagged
  IEEE Configuration BPDU on it and nothing else.** A non-CIST tree returns
  from `emit` without building or metering a BPDU; `frames` drops the CIST's
  SSTP copy and keeps the IEEE one. Why not the obvious alternative of sending
  the legacy shape per VLAN: `EncodeSSTP` forces a version of at least 2 and
  wire type `0x02`, and `DecodeSSTP` refuses anything else
  (`stp/sstp.go:58-63`, `:135-149`). SSTP has no legacy form in this codec.
  Why the CIST's copy goes too, which today's code does not do: `makeBPDU`
  forces `Version 0` and `BPDUTypeConfiguration` when `sendRSTP` is false
  (`layer.go:1341-1348`), so `EncodeSSTP` re-labels that legacy BPDU as a
  version-2 RST frame — a frame whose header contradicts its content. That is a
  live wire lie today, not a consequence of this phase.
  `frames` takes the port state to see it.
- Suppression is also the right model inside netsim: an SSTP frame is consumed
  by the neighbour here rather than flooded, so the legacy neighbour is the
  only audience and it cannot read the frame. The counter-argument, that real
  PVST+ tunnels per-VLAN BPDUs across a non-PVST region to a far PVST bridge,
  does not apply while netsim does not model that tunnel.
- `sendRSTP` is `link-replicated`, so `VLANPortInfo(v, p).SendRSTP` answers the
  link's truth for every VLAN and the suppression reads the tree's own copy.

### What changes for MSTP

RSTP behavior is unchanged: outside PVST every tree is the CIST, so every rule
above reads the same field it reads today. MSTP changes in three ways, each a
correction of the same class this phase exists to close, and each is a
requirement below rather than a side effect.

- An MSTI port with no instance path cost starts at the bridge port's
  configured cost rather than at `DefaultPathCost(0)`. `addTree` ignores
  `l.cfg.Ports[name].PathCost` today (`layer.go:576`), so the value is wrong
  until the first `LinkChange` overwrites it.
- `InstancePortInfo(mstid, p).BlockReason` renders `bpdu-guard` or
  `loop-inconsistent` where it renders `""` today, matching the role the same
  call already reports as Disabled or Alternate.
- `InstancePortInfo(mstid, p).RxBPDUs` reports the frames the port received
  rather than zero, and `BadBPDUs` reports them once rather than once per tree.
- An MSTI does not elect a root port through a BPDU-guard-disabled port in the
  window between the guard firing and its own information ageing out.
  `receiveLink` clears `rcvInfoValid` on the CIST alone (`layer.go:1869`)
  while an MSTI's stays valid until `Wake` ages it (`layer.go:2340`).

The corpus rows that pin `rx_bpdus=0` and an empty `block_reason` for an MSTI
(`src/common/netsim/internal/netsimtest/stp_cases.go:613`, `:616`, `:722`,
`:725`) change with the behavior, in the same unit.

### Placement

- **The plan stays whole rather than splitting again.** Its units touch three
  packages, but `bridge` contributes one unit that `vswitch`'s unit consumes
  and `vswitch` contributes one that the `stp` units feed, so the dependency
  graph is a single cluster with one edge into it and one out. Splitting a
  corrective phase of six units into three would put the seam this phase exists
  to move on a plan boundary.
- **This is a new parent unit U3f with `After: U3e`, not an amendment to the
  phase 3e plan.** Why: phase 3e is `status: implemented` with a filled
  `Landed:` range (`840d36c2..29515c73`, an ancestor of this worktree's HEAD),
  and folding the correction into it would leave the parent's ledger unable to
  say which commits carry which behavior. The parent's U5 gains U3f in its
  `After` line: U5 owns derivation and fork isolation over the same
  `stp.Layer` state this phase reshapes, and it edits the same files. Both
  parent edits were made while this plan was written.
- **The direction record is amended, not superseded.** Two of its sentences are
  false against the tree as it stands, before this phase touches anything: "the
  second counts the SSTP BPDU and applies nothing"
  (`docs/architecture/2026-09-10-virtual-device-direction.md:440-452`), which
  round 1 falsified when it moved `receiveLink` ahead of the boundary return,
  and "Reception resolves the arrival VLAN … rather than through the bridge's
  ingress pipeline" (`:426-431`), which this phase falsifies for the admission
  rule while leaving it true for the gate.

## Requirements

1. The link half of a receive runs for every SSTP BPDU the switch hands the
   layer, whatever VLAN it names.
   Acceptance: a PVST switch, port `1/1/1` with `BPDUGuard: true`,
   `Switchport{PVID: 1, Untagged: [1], Tagged: [10], IngressFiltering: true}`,
   VLAN table `{1, 10, 30}` and a tree per table VLAN. `Switch.Forward` of an
   SSTP BPDU tagged VID 30 leaves `PortInfo("1/1/1").BlockReason` equal to
   `bpdu-guard`. Today it is `""`.
2. A lookup by VLAN never answers with another VLAN's tree.
   Acceptance: on a PVST layer with trees for VLANs 1 and 10,
   `VLANPortInfo(30, "l1")` equals `stp.PortInfo{}` and `TracksVLAN(30)` is
   false; on an RSTP layer with no PVST configured, `VLANPortInfo(30, "l1")`
   still equals `PortInfo("l1")` and `TracksVLAN(30)` is true.
3. Every `portState` field is classified, and a `link-replicated` field reaches
   every tree.
   Acceptance: the classification test sets each `link-replicated` field to a
   distinguishable non-zero value on the CIST's port, calls
   `syncInstancePorts`, and finds it equal on VLAN 10's port; adding a field to
   `portState` without a classification entry fails the test.
4. Every reader of a `link-on-cist` field reads the CIST's copy.
   Acceptance: for each `link-on-cist` field the test asserts
   `VLANPortInfo(10, "l1").X == PortInfo("l1").X` on a PVST bridge after the
   field has been set. Concretely, after BPDU guard fires on `l1`,
   `VLANPortInfo(10, "l1").BlockReason` and `.RxBPDUs` equal
   `PortInfo("l1").BlockReason` and `.RxBPDUs`. Today `BlockReason` is `""` and
   `RxBPDUs` is 0 on VLAN 10.
5. A migration reaches every tree, and a migrated port sends one untagged IEEE
   Configuration BPDU and nothing else.
   Acceptance: a PVST bridge with trees for 1 and 10, trunk `l1`; a version 0
   Configuration BPDU arrives IEEE-addressed on `l1` past `MigrateTime` through
   `Receive`; `VLANPortInfo(10, "l1").SendRSTP` is false, and the next `Wake`
   produces no emission on `l1` whose `Frame.Dst` is `GroupAddressSSTP` and one
   whose `Frame.Dst` is the IEEE bridge group address with `Version` 0. Today
   `SendRSTP` reads true on VLAN 10, VLAN 10 emits a version-2 Rapid SSTP BPDU,
   and VLAN 1's SSTP copy carries a Configuration BPDU under a version-2 RST
   header.
6. A frame the layer decoded is never traced as a decode failure, and every
   SSTP step names both VLANs.
   Acceptance: the refusal of an SSTP BPDU for a VLAN the port does not admit
   produces one step with `RuleID` `stp.sstp.vlan-not-admitted`, `Reason`
   `stp.ReasonVLANNotAdmitted`, inputs `BPDUDecodeFact(f, true, "")` and
   `sstpVLANFact(tlvVID, arrivalVID)`, and leaves `PortInfo(p).BadBPDUs`
   unchanged.
7. The bridge's ingress admission rule has one definition.
   Acceptance: over a table of at least ten switchport shapes — access with
   PVID in `Untagged`; access with PVID absent from `Untagged`; no PVID; trunk
   with `IngressFiltering`; trunk without it, VID in `VLAN.Table` but in
   neither `Tagged` nor `Untagged`; a VID outside `VLAN.Table`;
   `Admission: TaggedOnly` with an untagged frame;
   `Admission: UntaggedAndPriorityTaggedOnly` with a tagged frame; a port
   absent from `Switchports`; a tunnel port — `VLAN.AdmitsVIDOnIngress` agrees
   with whether `Bridge.Ingress` returns `ok` for the corresponding frame,
   except on the tunnel row, where the method answers `false` by the decision
   above and the test asserts that difference with its reason.
8. A link change that only changes speed reaches every tree without disturbing
   the port.
   Acceptance: a PVST bridge whose VLAN 1 tree pins `l1` to path cost 55, with
   `l1` converged and Forwarding; `LinkChange` at 1 Gb/s then at 10 Gb/s leaves
   `PortInfo("l1").PathCost` 55, moves `VLANPortInfo(10, "l1").PathCost` from
   20000 to 2000, and leaves `PortInfo("l1").State` and `.ForwardTransitions`
   unchanged. Today the cost stays 20000.
9. A tree's port starts at the bridge port's configured cost.
   Acceptance: `stp.Port{PathCost: 100}` on `l1` with no per-tree override
   leaves `VLANPortInfo(10, "l1").PathCost` and
   `InstancePortInfo(1, "l1").PathCost` equal to 100 before any `LinkChange`.
   Today both read `DefaultPathCost(0)`, 20000.
10. An MSTI does not elect a root port through a BPDU-guard-disabled port.
    Acceptance: an MSTP bridge with `BPDUGuard` on `l1` and MSTI 1 holding
    valid information from `l1`; a BPDU fires the guard; MSTI 1's root is this
    bridge itself and its root port is empty. Today it keeps `l1` as root port
    with a Disabled role.
11. Every prose claim about the SSTP receive path matches the code.
    Acceptance: `stp/README.md`'s "Two receive entry points", "Emission" and
    "The boundary this package reports" sections, `vswitch/README.md:134`,
    `:337-350`, `:393-395` and `:455-463`, the `ReceiveSSTP` and `treeFor` doc
    comments, and the direction record's `:426-431` and `:440-452` paragraphs
    each describe the behavior this phase leaves.
12. RSTP behavior is unchanged, and MSTP changes only in the four ways R4, R9,
    R10 and the Decisions name.
    Acceptance: every existing test in `src/common/netsim/vswitch/stp`,
    `src/common/netsim/vswitch` and `src/common/netsim/vswitch/bridge` passes
    without a change to its expectations, except the corpus rows named under
    "What changes for MSTP" and the tests U2, U4 and U5 name.

## Out of scope

- Splitting `portState` into a per-port `linkState` and a per-tree
  `treePortState` held separately on the `Layer`. See Alternatives.
- Refactoring `Bridge.Ingress` to call `AdmitsVIDOnIngress`.
- Modelling a customer spanning tree arriving inside a provider tunnel.
- A new analysis issue code for the VLANs that fall silent on a port migrated
  to legacy STP. The existing `stp-pvst-boundary` keeps its meaning.
- Changing what `Learns` and `Forwards` answer for a VLAN with no tree. They
  keep answering `true`, the answer they already give for an untracked port.
- Any change to the SSTP wire format, the PVID check, or per-VLAN election.

## Units

### U1. The bridge's ingress admission rule, named
Files: `src/common/netsim/vswitch/bridge/config.go`,
`src/common/netsim/vswitch/bridge/ingress_admission_test.go`
After: none
Change: `VLAN.AdmitsVIDOnIngress(port string, vid vlan.ID, tagged bool) bool`
answers the predicate stated under Decisions, in that order: tunnel port
`false`; `Admission`; PVID for an untagged frame; `IngressFiltering`
membership; `VLAN.Table`. A port absent from `Switchports` is judged against
the zero `Switchport`. Its doc comment names `CarriesVID` as the egress rule,
says why the two differ, and says why a tunnel port answers `false`;
`CarriesVID`'s doc comment names this one.
Tests: `ingress_admission_test.go` holds R7's table and, for each row, builds
the frame and asserts `AdmitsVIDOnIngress` equals the `ok` that
`Bridge.Ingress` returns, with the tunnel row asserting the documented
difference.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/bridge`

### U2. A VLAN lookup that can say "no tree"
Files: `src/common/netsim/vswitch/stp/tree.go`,
`src/common/netsim/vswitch/stp/layer.go`,
`src/common/netsim/vswitch/stp/fact.go`,
`src/common/netsim/vswitch/stp/tree_internal_test.go`,
`src/common/netsim/vswitch/stp/layer_test.go`
After: none
Change: `treeFor` returns `(*tree, bool)`. It answers `false` only when
`l.pvst != nil` and `vidToTree` has no entry; outside PVST the CIST carries
every unclaimed VLAN and the answer stays the CIST with `true`.
`Layer.TracksVLAN(vid vlan.ID) bool` exports the same question for the switch,
with a doc comment saying it is always true outside PVST. `VLANPortInfo`
returns the zero `PortInfo` on `false`, the way `InstancePortInfo` already does
for an unknown MSTID. `ForwardingFact` renders the zero snapshot on `false`.
`Learns` and `Forwards` answer `true` on `false`, and their doc comments say so
and say why. `ReceiveSSTP`'s call site is left compiling against the new shape;
U4 is what gives it an outcome.
Tests: `layer_test.go` gains
`TestVLANPortInfoOnAVLANWithNoTreeIsZeroUnderPVST` (R2, both halves: the PVST
layer answering zero and the RSTP layer answering the CIST's).
`tree_internal_test.go:27` and `:771` are updated for the two-value return; the
RSTP case at `:27` keeps asserting the CIST answers.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/stp`

### U3. Link properties are a named, enumerated set
Files: `src/common/netsim/vswitch/stp/layer.go`,
`src/common/netsim/vswitch/stp/link_state_internal_test.go`,
`src/common/netsim/vswitch/stp/layer_test.go`,
`src/common/netsim/internal/netsimtest/stp_cases.go`
After: U2
Change: `portState` embeds `linkState{up, pointToPoint, edge, sendRSTP}` and
`syncInstancePorts` assigns it whole before the conditional path cost and the
link-down clears. `receiveLink` ends with an unconditional
`l.syncInstancePorts(p.name, p)`, and the `wasAutoEdge` branch's own call goes.
`blockReason` becomes a `Layer` method taking the tree's port and the CIST's,
reading `bpduGuardDisabled` and `loopInconsistent` from the CIST and
`pvidInconsistent` from the tree; its precedence is unchanged. `portInfo` takes
the CIST's port and reads `RxBPDUs` and `BadBPDUs` from it. `BadBPDU` bumps the
CIST's port alone instead of walking every tree. `recompute`'s root-election
loop (`layer.go:1498`, `:1506`) reads those two guard fields from the CIST, the
way the role switch at `:1585-1592` already does. `emit` returns without
building or metering a BPDU when `l.pvst != nil`, `t.id != cistID` and
`p.sendRSTP` is false; `frames` takes the port state and drops the CIST's SSTP
copy when `p.sendRSTP` is false, keeping the IEEE frame. `LinkChange` computes
the link cost and the tree cost before the no-op comparison: when only
`p.linkPathCost` differs it writes the new link cost, runs `syncInstancePorts`
and `recomputeAll`, and returns without the role, agreement, migration, edge or
forward-delay reset the link-up path performs. `addTree` derives its base cost
the way `newLayer` derives the CIST's, from `l.cfg.Ports[name].PathCost`
falling back to `DefaultPathCost(0)`, and sets `linkPathCost` to it. The corpus
rows that pin an MSTI's `rx_bpdus=0` and empty `block_reason`
(`stp_cases.go:613`, `:616`, `:722`, `:725`) are updated to the values the
corrected readers produce.
Tests: `link_state_internal_test.go` holds the classification table keyed by
`portState` field name with the five classes and the link-down-clear flag,
walks `reflect.TypeOf(portState{})` descending into the embedded `linkState`,
fails on a field with no entry, and for each class asserts what
`syncInstancePorts` does with it (R3). `layer_test.go` gains
`TestEveryReaderOfALinkPropertyReadsTheCISTsCopy` (R4, driven through `Receive`
and `Switch`-free layer calls, never through a direct `syncInstancePorts`
call), `TestPVSTMigrationReachesEveryTreeAndSilencesSSTP` (R5, driven through
`Receive`), `TestSpeedOnlyLinkChangeReachesEveryTreesCostWithoutBouncing` (R8),
`TestATreesPortStartsAtTheBridgePortsConfiguredCost` (R9), and
`TestAnMSTIDoesNotElectThroughAGuardDisabledPort` (R10).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/stp src/common/netsim/internal/netsimtest`

### U4. One entry point that always runs the link half
Files: `src/common/netsim/vswitch/stp/layer.go`,
`src/common/netsim/vswitch/stp/bpdu.go`,
`src/common/netsim/vswitch/stp/layer_test.go`
After: U3
Change: `SSTPArrival{ArrivalVID, TLVVID vlan.ID; Admitted bool}` and
`type SSTPOutcome string` with `SSTPApplied` (`"applied"`), `SSTPGuarded`
(`"bpdu-guard"`), `SSTPBoundary` (`"pvst-boundary"`), `SSTPNotAdmitted`
(`"vlan-not-admitted"`), `SSTPUntrackedVLAN` (`"vlan-untracked"`),
`SSTPPVIDInconsistent` (`"pvid-inconsistent"`) and `SSTPPortDown`
(`"port-down"`). `ReceiveSSTP(now, port, arrival, b) (Effects, SSTPOutcome)`
returns `SSTPPortDown` for a port it does not track or holds down, then counts
the frame, runs `receiveLink`, and decides in the Decisions' order. A non-PVST
bridge still gets its `pvstBoundary` mark whatever the outcome. `bpdu.go` gains
`ReasonVLANNotAdmitted` and `ReasonVLANUntracked` beside
`ReasonUnsupportedBPDU`. `ReceiveSSTP`'s doc comment states that the link half
always runs, that `syncInstancePorts` carries what it changed, and that the
outcome describes the tree half alone.
Tests: `layer_test.go` gains `TestReceiveSSTPRunsTheLinkHalfForEveryOutcome`, a
table over the six non-`SSTPPortDown` outcomes asserting for each that the
loop-guard clear happened and that the outcome is the expected one.
`TestSSTPOnANonPVSTBridgeMarksTheBoundaryAndAppliesNothing` (`:2383`) is renamed
`TestSSTPOnANonPVSTBridgeRunsTheLinkHalfAndAppliesNoVector`, keeps its existing
assertions, and gains a second fixture with `BPDUGuard: true` proving the guard
fires and that the outcome is `SSTPGuarded` while the boundary mark is still
set — the old name overstated what the old fixture, which configures no guards,
could pin. Every other `ReceiveSSTP` call site is updated mechanically for the
new parameter and the second return value: the shared helper
`convergePVSTLayers` (`layer_test.go:1921`, call at `:1966`) passes
`Admitted: true`, as do `:2290`, `:2319`, `:2414`, `:2476`, `:2482`, `:2553`,
`:2597` and `:2641`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/stp`

### U5. The switch stops gating the layer
Files: `src/common/netsim/vswitch/switch.go`,
`src/common/netsim/vswitch/switch_test.go`
After: U1, U4
Change: `interceptSSTP` deletes the `CarriesVID` refusal block entirely. It
resolves `arrivalVID` as today, computes `tagged` as
`len(f.Tags) > 0 && f.Tags[0].VID != 0 && (f.Tags[0].TPID == 0 || f.Tags[0].TPID == uint16(ethernet.EtherTypeDot1Q))`,
which is the tag form `Bridge.Ingress` reads (`bridge/bridge.go:589-602`),
computes `admitted` from
`s.cfg.Bridge.VLAN.AdmitsVIDOnIngress(resolvedPort, arrivalVID, tagged)` —
`true` when `s.cfg.Bridge.VLAN` is nil — and calls `ReceiveSSTP` once. One
mapping turns the returned `SSTPOutcome` into the step: `SSTPApplied`,
`SSTPGuarded`, `SSTPBoundary` and `SSTPPVIDInconsistent` render
`trace.Consumed` with `RuleID` `stp.sstp.admit`; `SSTPNotAdmitted` and
`SSTPUntrackedVLAN` render `trace.Dropped` with `stp.ReasonVLANNotAdmitted` or
`stp.ReasonVLANUntracked` and the matching `stp.sstp.<reason>` rule;
`SSTPPortDown` renders the `port.status.down` step the function already builds
for a dead port, which it reaches when the port table holds a port that
`stp.Config.Ports` does not. Every step carries `BPDUDecodeFact(f, true, "")`
and `sstpVLANFact(tlvVID, arrivalVID)` as inputs and
`BPDUDecisionFact(bpdu, before, after)` as outputs, and none calls `BadBPDU`.
The doc comment drops the paragraph about refusing a frame before the layer
sees it and states the new seam. When `mutate` is false the outcome is computed
from `admitted` and `Layer.TracksVLAN` rather than from `ReceiveSSTP`, so the
trace shape does not depend on mutation.
Tests: `switch_test.go:6836` `TestPVSTForeignVLANDoesNotRewriteVLAN1sRoot`
keeps its name and its assertion that VLAN 1's root is unchanged, and moves its
evidence to the layer's `SSTPUntrackedVLAN` outcome and the new step shape. New
`TestSSTPForAnUnadmittedVLANStillFiresBPDUGuard` is R1;
`TestSSTPRefusalTracesADecodedFrame` is R6; and
`TestSSTPTraceShapeIsTheSameWithAndWithoutMutation` drives `Forward` and `Peek`
over all six non-`SSTPPortDown` outcomes and compares the rendered steps, which
is the claim nothing else pins.
`TestSSTPFrameConsultsTheSTPScopeWhenSpanningTreeIsMissing` (`:7085`) is
unaffected: it builds a switch with no STP, so `interceptSSTP` never runs
(`switch.go:860`).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch`

### U6. Documentation and the record
Files: `docs/architecture/2026-09-10-virtual-device-direction.md`,
`src/common/netsim/vswitch/stp/README.md`,
`src/common/netsim/vswitch/README.md`,
`docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md`,
`docs/plans/2026-09-12-1339-fix-netsim-analysis-completeness-phase3f-plan.md`
After: U5
Change: the direction record's boundary paragraph (`:440-452`) says that the
link-level half of a receive runs on both sides of the boundary and that only
the vector is withheld; its reception bullet (`:426-431`) distinguishes the
spanning tree gate, which a per-VLAN BPDU still bypasses, from the bridge's
ingress admission rule, which it now obeys. `stp/README.md`'s "Two receive
entry points" section carries the new `SSTPArrival` and `SSTPOutcome` shape;
its "Emission" section says that a port migrated to legacy STP sends VLAN 1's
untagged Configuration BPDU alone, because SSTP has no legacy form; its
boundary section replaces "applies nothing", which is true of the vector and
false of the frame; and it gains a paragraph on the link/tree split: which port
properties are the link's, that every tree holds a copy, which the CIST holds
alone, and that a VLAN with no tree has no answer rather than VLAN 1's. Its
paragraph on `receiveLink` already says the link half runs once per frame for
both entry points and needs no change. `vswitch/README.md:337-350` replaces the
"refused as an unsupported BPDU rather than handed to the layer" sentence with
the new seam and keeps the claim about naming both VLANs, which is now true on
every path; `:455-463` says the boundary issue is not suppressed by a refusal;
`:134` loses "One tree carries every VLAN today, so the two answers agree",
which PVST falsified in phase 3e; and `:393-395` says what a migrated port does
per VLAN. The parent plan already carries U3f and U5's widened `After` from
this planning session; this unit only fills U3f's `Landed:` range and sets this
plan's `status` with an outcome note under its title.
Tests: none. This unit changes prose only; the verifier's documentation and
layout checks are what run over it.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/architecture/2026-09-10-virtual-device-direction.md src/common/netsim/vswitch/stp/README.md src/common/netsim/vswitch/README.md docs/plans`

Waves: U1 U2 | U3 | U4 | U5 | U6

U2, U3 and U4 form a chain only because all three edit `layer.go`; no `After`
between them is a semantic dependency except U4's use of U2's two-value
`treeFor`. U1 runs beside U2 and is what U5 waits on together with U4.

## Alternatives

Splitting `portState` into a per-port `linkState` held on the `Layer` and a
per-tree `treePortState` removes the collapse rather than testing for it: there
would be no CIST copy to confuse with the link's. It is not this phase because
`recompute`, `emit`, `makeBPDU`, `applyBPDU`, `LinkChange`, `Receive` and
`Wake` all read one `portState` shape across roughly 2400 lines of `layer.go`,
the behavior they implement was reviewed twice in the last week, and the
classification test catches the same class at a fraction of the risk. If a
sixth instance appears after this phase, the split is the answer and the
classification table is the map for it.

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>
go test -race ./src/common/netsim/...
go test -race ./test/conformance/...
```

R5 is the requirement no test can check against a real device: whether a PVST+
bridge suppresses per-VLAN BPDUs towards a legacy neighbour or tunnels them to
a far PVST bridge is a capture question, and the codec's refusal of a legacy
SSTP shape is what decides it here. R7's table is the only thing standing
between `AdmitsVIDOnIngress` and `Bridge.Ingress` drifting apart; nothing in
the build couples them.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `src/common/netsim/vswitch/stp/README.md` and
      `src/common/netsim/vswitch/README.md` updated in the same change as the
      code they describe.
- [ ] The direction record amended in U6, with the amendment accepted before
      the phase closes.
- [ ] Every existing test passes without a change to its expectations, except
      the corpus rows and the tests R12 names.
- [ ] U3f's `Landed:` range filled in the parent plan.
- [ ] This plan's `status` set with an outcome note under its title.
- [ ] No plan labels (U1, R3) in code, comments, or commit messages.

## Open questions

- Whether `Learns` and `Forwards` should answer `false` rather than `true` for
  a VLAN with no tree. This plan keeps `true`, matching the answer they already
  give for an untracked port, and rests the safety on `Config.Validate`
  refusing a PVST switch with an uncovered table VLAN
  (`src/common/netsim/vswitch/config.go:375-398`). `false` would black-hole a
  VLAN on a hand-assembled `Layer` instead of looping one. Unconfirmed.
- Whether the switch should raise an issue for the VLANs that fall silent on a
  port migrated to legacy STP, rather than leaving the silence readable only
  through `VLANPortInfo(v, p).SendRSTP`. Deferred: a new issue code changes the
  corpus contract, which belongs to the phase that owns it.
- Whether `Bridge.Ingress` should later be refactored to call
  `AdmitsVIDOnIngress`, retiring R7's agreement table.
- Whether an SSTP BPDU arriving on a provider tunnel port should eventually
  drive a customer spanning tree rather than being refused.
