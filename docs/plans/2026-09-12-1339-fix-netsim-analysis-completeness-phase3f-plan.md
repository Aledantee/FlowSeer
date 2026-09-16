---
title: Network Simulation Analysis Completeness, Phase 3f - Plan
type: fix
date: 2026-09-16
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
parent: docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
---

# Network simulation analysis completeness, phase 3f: the link and VLAN 1 share one slot - Plan

## Goal

Per-VLAN spanning tree put VLAN 1's tree in the CIST slot, and that slot now
carries two unrelated things: VLAN 1's tree, and every property that belongs to
the link rather than to any tree. Nothing in the code distinguishes the two
roles, so every defect two review rounds found is the same mistake — reading or
writing one role's field through the other role's handle. This phase names the
two roles and makes three properties fail a test when violated, instead of
patching the instances. The means are a tree lookup that can answer "no tree", a
named `linkState` whose membership is enumerated by a test, and one SSTP entry
point that always runs the link half of a receive.

This plan is wrong if the link role and the tree role cannot be separated
inside one `portState`: if `recompute` needs a field to be simultaneously
replicated and CIST-only, the classification below has no answer and the
`portState` split named under Alternatives is the phase instead.

This phase claims no new parent requirement. It corrects phase 3e, which
claimed parent R14c, R14d, R15b and R39.

## Decisions

Phase 3e's decisions stand and are not repeated. VLAN 1's tree stays in the
CIST slot; PVST stays RSTP per VLAN; the mode stays the presence of
`Config.PVST`.

### The three properties

- **The link half of a receive runs for every BPDU that reaches the layer**,
  whatever tree it names and whether or not a tree applies it. Why: BPDU guard,
  the loop-guard clear, protocol migration and auto-edge loss are what make the
  port's own state trustworthy, and a caller that can skip them by declining to
  call the layer has a hole shaped exactly like round 2's finding A.
- **No lookup by VLAN silently answers with another VLAN's tree.** Why: under
  PVST the CIST is VLAN 1's tree and nothing else, so `treeFor`'s fallback is a
  wrong answer rather than a default. Outside PVST the CIST does carry every
  unclaimed VLAN, so the fallback is correct there and stays.
- **Every property of the link reaches every tree, by one rule.** Why: a
  property that lives only on the CIST's port state is invisible to the trees
  that read their own copy, and `sendRSTP` is the fourth such field to be found
  by inspection rather than by a test.

### The receive seam moves inside the layer

- **`ReceiveSSTP` takes the bridge's admission answer and returns what it did;
  the switch stops gating the call.** Shape:
  `ReceiveSSTP(now, port string, arrival SSTPArrival, b BPDU) (Effects, SSTPOutcome)`
  where `SSTPArrival` carries `ArrivalVID`, `TLVVID` and `Admitted`. Why a
  parameter rather than a VLAN table on the layer: `stp` has no VLAN table, and
  giving it one would put a second admission rule beside `bridge`'s, which is
  the defect round 2 reported as finding F. Why an outcome rather than a bare
  `Effects`: the switch has to render a step that says which of the six things
  happened, and today it renders a decode failure for a frame that decoded.
- **The layer refuses in two steps, in this order: guard, then admission, then
  tree lookup.** `receiveLink` runs first and can return `SSTPGuarded`; a frame
  the bridge did not admit returns `SSTPNotAdmitted`; a VLAN with no tree
  returns `SSTPNoTree`. Why guard first: the frame is evidence a bridge is on
  the port whatever VLAN it claims, which is the whole point of the guard.

### The bridge's ingress admission rule gets a name

- **`bridge.VLAN` gains `AdmitsVIDOnIngress(port string, vid vlan.ID, tagged bool) bool`,
  and `interceptSSTP` calls it.** Why on `VLAN` rather than on `Switchport`:
  the rule `Bridge.Ingress` applies has four parts and one of them is the VLAN
  table — untagged and priority-tagged frames need `PVID != nil` and
  `vid == *PVID`; `IngressFiltering` adds membership in `Tagged ∪ Untagged`;
  and every classified VID must be in `VLAN.Table` or the frame drops with
  `ReasonUndefinedVLAN` (`bridge/bridge.go:713`). A `Switchport` method could
  not see the table.
- **Agreement with `Bridge.Ingress` is proved by a test, not by shared code.**
  Why: extracting the predicate out of `Ingress` means unpicking it from the
  tunnel, admission-policy and priority-tag branches it is interleaved with,
  which is a larger change than this phase should carry against landed
  behavior. The test drives both over the same table and fails when either
  drifts.
- Round 1's `bridge.Switchport.CarriesVID` call in `interceptSSTP` is not
  merely the wrong side of the port: it is a second admission policy. It is
  stricter than ingress on an access port whose PVID is not also in `Untagged`
  (`CarriesVID` excludes PVID by its own doc comment, `bridge/config.go:107`)
  and looser than nothing on a tagged frame, where ingress additionally
  requires the table entry. Deleting it removes the policy rather than
  correcting it.

### Link properties are a named set

- **`portState` embeds `linkState{up, pointToPoint, edge, sendRSTP}`, and
  `syncInstancePorts` assigns the whole struct.** Why a struct: a field added
  to it is replicated by construction, so the propagation cannot be forgotten
  one field at a time.
- **Every field of `portState` is classified by a test into `link-replicated`,
  `link-on-cist`, `tree-owned`, or `link-derived`, and the test fails on an
  unclassified field.** Why reflection: a struct assignment makes the fields
  inside `linkState` safe but says nothing about the next field added outside
  it, which is exactly how `sendRSTP` was missed. `linkPathCost` is
  `link-on-cist` (the source `syncInstancePorts` reads), `pathCost` is
  `link-derived` (copied only when `!pathCostFixed`), `external`,
  `bpduGuardDisabled` and `loopInconsistent` are `link-on-cist` (every tree
  reads them through the CIST), `pvidInconsistent` is `tree-owned`. Every
  field these Decisions do not name is classified by one mechanical rule
  rather than by the implementer's judgment: a field some non-CIST tree's own
  code path writes is `tree-owned` (`role`, `state`, `agreed`, `proposing`,
  the `rcv*` set, `fwdDelayTimer`, `edgeDelayWhile`, the three counters);
  a field only `receiveLink`, `LinkChange`, `Mcheck` or `Wake`'s CIST branch
  writes is `link-on-cist` (`mdelayWhile`, `pvstBoundary`); a field written
  once at construction is `tree-owned`, because `addTree` may give a tree its
  own value (`name`, `cfg`, `portID`, `adminEdge`, `pathCostFixed`). A field
  the rule cannot place is this plan's stop condition, not a judgment call.
- **A field classified `link-on-cist` is read through the CIST by every
  reader, not only by `recompute`.** `portState.blockReason` reads the tree's
  own copy today, so `VLANPortInfo(10, p)` renders `role="Disabled"` with
  `block_reason=""` on a BPDU-guard-disabled port while `PortInfo(p)` renders
  `bpdu-guard` — a fourth instance of the collapse, and a `PortInfo` that
  contradicts itself. `blockReason` becomes `(l *Layer) blockReason(p, cistP *portState)`.

### A migrated port sends no per-VLAN BPDU

- **When the CIST's port has `sendRSTP` false, a non-CIST PVST tree emits
  nothing on that port and spends no transmit budget.** Why not the obvious
  alternative of sending the legacy shape per VLAN: `EncodeSSTP` forces a
  version of at least 2 and wire type `0x02`, and `DecodeSSTP` refuses
  anything else (`stp/sstp.go:60-72`, `:130-145`). SSTP has no legacy form in
  this codec, so "send a Configuration BPDU per VLAN" is not expressible.
  Suppression is what remains, and it is also the right model inside netsim:
  an SSTP frame is consumed by the neighbour here rather than flooded, so the
  legacy neighbour is the only audience and it cannot read the frame. The
  counter-argument, that real PVST+ tunnels per-VLAN BPDUs across a non-PVST
  region to a far PVST bridge, does not apply while netsim does not model that
  tunnel.
- `sendRSTP` still joins `linkState` and is replicated, so
  `VLANPortInfo(v, p).SendRSTP` answers the link's truth for every VLAN. The
  suppression reads the tree's own replicated copy, not across to the CIST.

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
  `stp.Layer` state this phase reshapes, and it edits the same files.
- **The direction record is amended, not superseded.** Its sentence "the
  second counts the SSTP BPDU and applies nothing"
  (`docs/architecture/2026-09-10-virtual-device-direction.md:440-452`) is
  false as written once the link half runs.

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
   `VLANPortInfo(30, "l1")` equals `stp.PortInfo{}`; on an RSTP layer with no
   PVST configured, `VLANPortInfo(30, "l1")` still equals `PortInfo("l1")`.
3. Every `portState` field is classified, and a `link-replicated` field
   reaches every tree.
   Acceptance: the classification test sets each `link-replicated` field to a
   distinguishable non-zero value on the CIST's port, calls
   `syncInstancePorts`, and finds it equal on VLAN 10's port; adding a field to
   `portState` without a classification entry fails the test.
4. A guard that fires reads the same on every VLAN.
   Acceptance: after BPDU guard fires on `l1` of a PVST bridge with trees for
   1 and 10, `VLANPortInfo(10, "l1").BlockReason` equals
   `PortInfo("l1").BlockReason` equals `bpdu-guard`. Today VLAN 10 reads `""`.
5. A port migrated to legacy STP answers `SendRSTP` false for every VLAN and
   emits no SSTP frame on that port.
   Acceptance: a PVST bridge with trees for 1 and 10; a Configuration BPDU
   arrives on `l1` past `MigrateTime`; `VLANPortInfo(10, "l1").SendRSTP` is
   false, and the next `Wake` produces no emission on `l1` with
   `Frame.Dst == GroupAddressSSTP`. Today `SendRSTP` is true and VLAN 10 emits
   a version-2 Rapid SSTP BPDU.
6. A frame the layer decoded is never traced as a decode failure, and every
   SSTP step names both VLANs.
   Acceptance: the refusal of an SSTP BPDU for a VLAN the port does not admit
   produces one step with `RuleID` `stp.sstp.vlan-not-admitted`, `Reason`
   `stp.ReasonVLANNotAdmitted`, inputs `BPDUDecodeFact(f, true, "")` and
   `sstpVLANFact(tlvVID, arrivalVID)`, and leaves `PortInfo(p).BadBPDUs`
   unchanged.
7. The bridge's ingress admission rule has one definition.
   Acceptance: over a table of at least eight switchport shapes (tunnel;
   access with PVID in `Untagged`; access with PVID absent from `Untagged`;
   trunk with and without `IngressFiltering`; a VID outside `VLAN.Table`;
   `Admission: TaggedOnly`; no PVID), `VLAN.AdmitsVIDOnIngress` agrees with
   whether `Bridge.Ingress` returns `ok` for the corresponding frame.
8. A link change that only changes speed reaches every tree.
   Acceptance: a PVST bridge whose VLAN 1 tree pins `l1` to path cost 55;
   `LinkChange` at 1 Gb/s then at 10 Gb/s leaves `PortInfo("l1").PathCost` 55
   and moves `VLANPortInfo(10, "l1").PathCost` from 20000 to 2000. Today it
   stays 20000.
9. A tree's port starts at the bridge port's configured cost.
   Acceptance: `stp.Port{PathCost: 100}` on `l1` with no per-tree override
   leaves `VLANPortInfo(10, "l1").PathCost` and `InstancePortInfo(1, "l1").PathCost`
   equal to 100 before any `LinkChange`, and `linkPathCost` equal to it.
   Today both read `DefaultPathCost(0)`, 20000.
10. Every prose claim about the SSTP receive path matches the code.
    Acceptance: `stp/README.md:244-249`, `vswitch/README.md:337-350` and
    `:455-463`, the `ReceiveSSTP` and `treeFor` doc comments, and the
    direction record's boundary paragraph each describe the behavior this
    phase leaves.
11. RSTP and MSTP behavior is unchanged.
    Acceptance: every existing test in `src/common/netsim/vswitch/stp`,
    `src/common/netsim/vswitch` and `src/common/netsim/vswitch/bridge` passes
    unchanged except the four this plan names in U2, U4 and U5.

## Out of scope

- Splitting `portState` into a per-port `linkState` and a per-tree
  `treePortState` held separately on the `Layer`. See Alternatives.
- Refactoring `Bridge.Ingress` to call `AdmitsVIDOnIngress`.
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
answers the rule `Bridge.Ingress` applies: a tunnel port admits its
`Tunnel.VID` alone; an untagged or priority-tagged frame needs `PVID != nil`
and `vid == *PVID`; `IngressFiltering` additionally requires `vid` in `Tagged`
or `Untagged`; and `vid` must be in `VLAN.Table`. Its doc comment names
`CarriesVID` as the egress rule and says why the two differ, and
`CarriesVID`'s names this one.
Tests: `ingress_admission_test.go` holds R7's table and, for each row, builds
the frame and asserts `AdmitsVIDOnIngress` equals the `ok` that
`Bridge.Ingress` returns.
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
every unclaimed VLAN and the answer stays the CIST with `true`. `VLANPortInfo`
returns the zero `PortInfo` on `false`, the way `InstancePortInfo` already does
for an unknown MSTID. `ForwardingFact` renders the zero snapshot on `false`.
`Learns` and `Forwards` answer `true` on `false`, and their doc comments say
so and say why. `ReceiveSSTP`'s call site is left compiling against the new
shape; U4 is what gives it an outcome.
Tests: `layer_test.go` gains `TestVLANPortInfoOnAVLANWithNoTreeIsZeroUnderPVST`
(R2, both halves: the PVST layer answering zero and the RSTP layer answering
the CIST's). `tree_internal_test.go:27` and `:771` are updated for the two-value
return; the RSTP case at `:27` keeps asserting the CIST answers.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/stp`

### U3. Link properties are a named, enumerated set
Files: `src/common/netsim/vswitch/stp/layer.go`,
`src/common/netsim/vswitch/stp/link_state_internal_test.go`,
`src/common/netsim/vswitch/stp/layer_test.go`
After: U2
Change: `portState` embeds `linkState{up, pointToPoint, edge, sendRSTP}` and
`syncInstancePorts` assigns it whole before the conditional path cost and the
link-down clears. `blockReason` becomes a `Layer` method taking the tree's port
and the CIST's, reading `bpduGuardDisabled` and `loopInconsistent` from the
CIST and `pvidInconsistent` from the tree, which is the order `recompute`
already uses (`layer.go:1585-1592`). `recompute`'s root-election loop
(`layer.go:1498`, `:1506`) reads the same two fields from the CIST. `emit`
returns without building or metering a BPDU when `l.pvst != nil`,
`t.id != cistID` and `p.sendRSTP` is false. `LinkChange` computes the link cost
and the tree cost before the no-op comparison and compares `p.linkPathCost`
against the new link cost as well as `p.pathCost` against the new tree cost.
`addTree` derives its base cost the way `newLayer` derives the CIST's, from
`l.cfg.Ports[name].PathCost` falling back to `DefaultPathCost(0)`, and sets
`linkPathCost` to it.
Tests: `link_state_internal_test.go` holds the classification table keyed by
`portState` field name with the four classes, walks `reflect.TypeOf(portState{})`
(descending into the embedded `linkState`), fails on a field with no entry, and
for each class asserts what `syncInstancePorts` does with it (R3).
`layer_test.go` gains `TestPVSTGuardBlockReasonIsTheSameOnEveryVLAN` (R4),
`TestPVSTMigratedPortSendsNoSSTPFrame` (R5),
`TestSpeedOnlyLinkChangeReachesEveryTreesCost` (R8), and
`TestATreesPortStartsAtTheBridgePortsConfiguredCost` (R9).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/stp`

### U4. One entry point that always runs the link half
Files: `src/common/netsim/vswitch/stp/layer.go`,
`src/common/netsim/vswitch/stp/bpdu.go`,
`src/common/netsim/vswitch/stp/layer_test.go`
After: U3
Change: `SSTPArrival{ArrivalVID, TLVVID vlan.ID; Admitted bool}` and
`SSTPOutcome` with the values `applied`, `bpdu-guard`, `pvst-boundary`,
`vlan-not-admitted`, `vlan-untracked`, `pvid-inconsistent` and `port-down`.
`ReceiveSSTP(now, port, arrival, b) (Effects, SSTPOutcome)` counts the frame,
runs `receiveLink`, and only then decides: `!l.pvst` yields `pvst-boundary`,
a guard return yields `bpdu-guard`, `!arrival.Admitted` yields
`vlan-not-admitted`, a `treeFor` miss yields `vlan-untracked`, a TLV
disagreement yields `pvid-inconsistent`, and otherwise `applyBPDU` runs and it
yields `applied`. `bpdu.go` gains `ReasonVLANNotAdmitted` and
`ReasonVLANUntracked` beside `ReasonUnsupportedBPDU`. `ReceiveSSTP`'s doc
comment states that the link half always runs and that the tree half is what
the outcome describes.
Tests: `layer_test.go` gains `TestReceiveSSTPRunsTheLinkHalfForEveryOutcome`,
a table over the six non-`port-down` outcomes asserting for each that the
loop-guard clear happened and that the outcome is the expected one.
`TestSSTPOnANonPVSTBridgeMarksTheBoundaryAndAppliesNothing` (`:2383`) is
renamed `TestSSTPOnANonPVSTBridgeRunsTheLinkHalfAndAppliesNoVector`, keeps its
existing assertions, and gains a second fixture with `BPDUGuard: true` proving
the guard fires — the old name overstated what the old fixture, which
configures no guards, could pin.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/stp`

### U5. The switch stops gating the layer
Files: `src/common/netsim/vswitch/switch.go`,
`src/common/netsim/vswitch/switch_test.go`
After: U1, U4
Change: `interceptSSTP` deletes the `CarriesVID` refusal block entirely. It
resolves `arrivalVID` as today, computes `admitted` from
`s.cfg.Bridge.VLAN.AdmitsVIDOnIngress(resolvedPort, arrivalVID, tagged)` —
`true` when `s.cfg.Bridge.VLAN` is nil — and calls `ReceiveSSTP` once. One
mapping turns the returned `SSTPOutcome` into the step: `applied`,
`bpdu-guard`, `pvst-boundary` and `pvid-inconsistent` render `trace.Consumed`
with `RuleID` `stp.sstp.admit`; `vlan-not-admitted` and `vlan-untracked`
render `trace.Dropped` with `stp.ReasonVLANNotAdmitted` or
`stp.ReasonVLANUntracked` and the matching `stp.sstp.<reason>` rule. Every one
of them carries `BPDUDecodeFact(f, true, "")` and `sstpVLANFact(tlvVID, arrivalVID)`
as inputs and `BPDUDecisionFact(bpdu, before, after)` as outputs, and none
calls `BadBPDU`. The doc comment drops the paragraph about refusing a frame
before the layer sees it and states the new seam. When `mutate` is false the
outcome is still computed from `admitted` and `treeFor` so the trace shape
does not depend on mutation.
Tests: `switch_test.go:6836` `TestPVSTForeignVLANDoesNotRewriteVLAN1sRoot`
keeps its name and its assertion that VLAN 1's root is unchanged, and moves
its evidence to the layer's `vlan-not-admitted` outcome and the new step
shape. New `TestSSTPForAnUnadmittedVLANStillFiresBPDUGuard` is R1, and
`TestSSTPRefusalTracesADecodedFrame` is R6.
`TestSSTPFrameConsultsTheSTPScopeWhenSpanningTreeIsMissing` (`:7085`) is
unaffected: it builds a switch with no STP, so `interceptSSTP` never runs.
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
the vector is withheld, and gains a sentence that a per-VLAN BPDU is admitted
by the bridge's own ingress rule rather than by a rule the spanning tree layer
keeps. `stp/README.md:216`, which describes `ReceiveSSTP`'s arguments, carries the
new `SSTPArrival` and `SSTPOutcome` shape. `stp/README.md:244-249` makes the
same correction and gains a paragraph
on the link/tree split: which port properties are the link's, that every tree
holds a copy, and that a VLAN with no tree has no answer rather than VLAN 1's.
Its "Emission" section says that a port migrated to legacy STP sends VLAN 1's
Configuration BPDU alone, because SSTP has no legacy form. Its paragraph on
`receiveLink` already says the link half runs once per frame for both entry
points and needs no change; only the boundary paragraph's "applies nothing"
does, which is true of the vector and false of the frame.
`vswitch/README.md:337-350` replaces the "refused as an unsupported BPDU
rather than handed to the layer" sentence with the new seam and keeps the
claim about naming both VLANs, which is now true on every path;
`vswitch/README.md:455-463` says the boundary issue covers every VLAN but
VLAN 1 and is not suppressed by a refusal. Two further sentences are stale
against the landed phase 3e and are corrected here: the spanning-tree ladder
bullet ends "One tree carries every VLAN today, so the two answers agree"
(`:134`), which PVST falsified; and the migration paragraph (`:393-395`) says
what a migrated port does per VLAN. The parent plan gains U3f with its
`After` and empty `Landed:`, and U5's `After` gains U3f. This plan's `status`
is set with an outcome note under its title.
Tests: none. This unit changes prose only; the verifier's documentation and
layout checks are what run over it.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/architecture/2026-09-10-virtual-device-direction.md src/common/netsim/vswitch/stp/README.md src/common/netsim/vswitch/README.md docs/plans`

Waves: U1 U2 | U3 | U4 | U5 | U6

U2, U3 and U4 form a chain only because all three edit `layer.go`; no `After`
between them is a semantic dependency except U4's use of U2's two-value
`treeFor`. U1 runs beside U2 and is what U5 waits on together with U4.

## Alternatives

Splitting `portState` into a per-port `linkState` held on the `Layer` and a
per-tree `treePortState` removes the collapse rather than testing for it:
there would be no CIST copy to confuse with the link's. It is not this phase
because `recompute`, `emit`, `makeBPDU`, `applyBPDU`, `LinkChange`, `Wake` and
`mst.go` all read one `portState` shape across roughly 2400 lines, the
behavior they implement was reviewed twice in the last week, and the
classification test catches the same class at a fraction of the risk. If a
fifth instance appears after this phase, the split is the answer and the
classification table is the map for it.

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>
go test -race ./src/common/netsim/...
go test -race ./test/conformance/...
```

R5 is the requirement no test can check against a real device: whether a PVST+
bridge suppresses per-VLAN BPDUs towards a legacy neighbour or tunnels them is
a capture question, and the codec's refusal of a legacy SSTP shape is what
decides it here. R7's table is the only thing standing between
`AdmitsVIDOnIngress` and `Bridge.Ingress` drifting apart; nothing in the build
couples them.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `src/common/netsim/vswitch/stp/README.md` and
      `src/common/netsim/vswitch/README.md` updated in the same change as the
      code they describe.
- [ ] The direction record amended in U6, with the amendment accepted before
      the phase closes.
- [ ] Every existing test outside the four U2, U4 and U5 name passes
      unchanged, which is R11.
- [ ] The parent plan carries U3f with an empty `Landed:` line, and U5's
      `After` names U3f.
- [ ] This plan's `status` set with an outcome note under its title.
- [ ] No plan labels (U1, R3) in code, comments, or commit messages.

## Open questions

- Whether `Learns` and `Forwards` should answer `false` rather than `true` for
  a VLAN with no tree. This plan keeps `true`, matching the answer they already
  give for an untracked port, and rests the safety on `Config.Validate`
  refusing a PVST switch with an uncovered table VLAN. `false` would black-hole
  a VLAN on a hand-assembled `Layer` instead of looping one. Unconfirmed.
- Whether the switch should raise an issue for the VLANs that fall silent on a
  port migrated to legacy STP, rather than leaving the silence readable only
  through `VLANPortInfo(v, p).SendRSTP`. Deferred: a new issue code changes the
  corpus contract, which belongs to the phase that owns it.
- Whether `Bridge.Ingress` should later be refactored to call
  `AdmitsVIDOnIngress`, retiring R7's agreement table.
