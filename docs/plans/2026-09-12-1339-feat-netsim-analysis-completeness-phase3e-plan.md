---
title: Network Simulation Analysis Completeness, Phase 3e - Plan
type: feat
date: 2026-09-16
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
parent: docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
---

# Network simulation analysis completeness, phase 3e: Rapid spanning tree per VLAN - Plan

> Re-planned on 2026-09-16 against the tree phase 3d left (`ca47a59d`). The
> Decisions below keep the 2026-09-14 wire and behavior rulings, whose
> evidence was re-fetched on 2026-09-16, and settle the four questions the
> earlier draft left open.

## Goal

A simulated bridge runs one rapid spanning tree per VLAN, so each VLAN elects
its own root and blocks its own trunk, and a bridge that meets an MSTP or RSTP
neighbour says what it cannot answer rather than guessing. The means are one
tree per VLAN over the tree structure phase 3b landed and phase 3d generalized,
the SSTP encapsulation on the wire, tagged emission through the bridge's own
egress rules, and the PVID consistency check.

This plan is wrong if the per-VLAN trees cannot reuse the `tree` and
`priorityVector` machinery phase 3d generalized: if a VLAN tree needs its own
election rules rather than the landed RSTP ones, PVST is a second protocol
rather than the landed one instantiated per VLAN, and the phase should be
re-scoped before any of it lands.

This phase claims parent R14c and R14d, and extends R15b and R39.

## Decisions

### What PVST is

- **PVST is RSTP per VLAN.** Each VLAN runs the landed RSTP machine with the
  configured bridge priority as its own. Why: user-directed 2026-09-14, and it
  is what the landed `tree` type already supports.
- **The MSTI-specific paths are gated on `l.mst != nil`, which two of them are
  not today.** Hop-count aging and MSTI records already branch on it, but
  `recompute`'s boundary-role branch
  (`layer.go:1291`, `if t.id != cistID && l.boundary(name)`) branches only on
  the tree not being the CIST, and it must gain `l.mst != nil`. Why this is a
  correctness bug and not tidying: `l.boundary` reads
  `l.cist().ports[name].external` (`layer.go:858`), `Receive` sets
  `p.external = !internal` (`layer.go:1723`) and `internal` is
  `l.mst != nil && b.ConfigID != nil && …` (`layer.go:1657`), so on a PVST
  bridge, where `l.mst` is nil by construction, the first ordinary VLAN 1
  hello marks every port external. Ungated, every non-VLAN-1 tree would then
  copy VLAN 1's role on every port instead of running its own election, and
  R1 could not pass. PVST's own boundary concept is the separate
  `pvstBoundary` field below and never reuses `external`.
- **The mode is the presence of a pointer, not an enum.** `Config` gains
  `PVST *PVST` beside `MST *MST`, and `Validate` refuses a configuration
  setting both. Why: `MST != nil` already selects MSTP
  (`src/common/netsim/vswitch/stp/config.go:112-123`), so a `Mode` enum would
  make the same fact readable two ways and let them disagree. The earlier
  draft's objection to the implied form, that it "cannot express an empty
  configuration's mode", holds for a bare map but not for a pointer: a
  non-nil `PVST` whose `Trees` map is empty is a PVST bridge with no
  per-VLAN trees configured, which `Normalize` then fills with VLAN 1's.
- **VLAN 1's tree always exists and occupies the CIST slot (`treeID` 0).**
  `PVST.Normalize` inserts a default entry for VLAN 1 when `Trees` has none,
  and `newLayer` maps VLAN 1 to `cistID` and every other VLAN `v` to
  `treeID(v)`. Why the CIST slot rather than `treeID(1)`: in PVST+ VLAN 1's
  tree *is* the common spanning tree that a neighbouring RSTP or MSTP bridge
  converges with, and `Root`, `PortInfo`, `TopologyChanges`, `Times` and
  `boundary` all read `l.cist()` (`layer.go:525-546`, `layer.go:858`), so
  putting the common tree there keeps every bridge-level accessor answering
  about the common tree in all three modes. `treeID(1)` is then never used,
  and `treeID` stays free of collisions because the modes are exclusive.
- **A tree whose VLAN the bridge does not carry is not the layer's business,
  but a VLAN with no tree is.** `vswitch.New` refuses a PVST switch whose
  `Bridge.VLAN.Table` holds a VID absent from `PVST.Trees` after
  normalization. Why in `vswitch` and not in `stp`: `stp.Config` never sees
  the bridge's VLAN table, and a VLAN with no tree has no defined forwarding
  state, which is the false answer this phase exists to remove.
  `Layer.treeFor` keeps its fallback to `l.cist()` (`tree.go:71-77`), so a
  `Layer` built directly in a package test with an unlisted VID answers from
  VLAN 1's tree rather than panicking.

### Ruled during implementation

- Ruled: `recompute`'s state branch gains `l.mst != nil` alongside its role
  branch, not only the role branch at `layer.go:1291`. Why: the state loop
  carries a second boundary branch that mirrors the CIST's state the same way,
  so gating one alone leaves every non-VLAN-1 tree with its own roles and VLAN
  1's states. Cost if wrong: one condition in `recompute`.
- Ruled: `ReceiveSSTP` takes the arrival VLAN and the TLV VLAN as separate
  parameters. Why: the PVID check compares the two, and the single-VID
  signature this plan wrote cannot express the comparison it requires. Cost if
  wrong: the signature and its two call sites.
- Ruled: a per-VLAN tree's bridge identifier carries the VLAN in the low 12
  bits of the system-ID extension, the way an MSTI carries its MSTID, and a
  tree that configures no priority falls back to `Config.Priority` rather than
  the standard instance default, so `PVST.Normalize` takes the bridge
  priority. Why: without the fallback `Config.Priority` is dead in PVST mode
  and a bridge configured to be root is root on no VLAN; without the extension
  the multiple-of-4096 rule `PVST.Validate` enforces reserves bits nothing
  uses. Cost if wrong: `pvstBridgeID`, `PVST.Normalize`'s signature, and the
  bridge identifier on the wire.
- Ruled: `makeBPDU` sends the tree's own bridge identifier and `BridgeID()`
  answers the CIST's. Why: under PVST a BPDU sent under the layer's identifier
  matches no receiving tree's Backup test (`rcvBridgeID == t.bridgeID`), so a
  Backup port reads as Alternate. Outside PVST the CIST's identifier is the
  layer's and both are unchanged. Cost if wrong: two field reads.

### The wire

Evidence re-fetched 2026-09-16: Wireshark `epan/dissectors/packet-bpdu.c`
(`BPDU_PVST_TLV 36`, `BPDU_PVST_TLV_ORIGVLAN 0`, the two-octet type and
two-octet length read at that offset) and `epan/dissectors/packet-cisco-pid.h`
(`#define CISCO_PID_PVSTPP 0x010B`); Cisco,
[Troubleshoot Spanning Tree PVID- and Type-Inconsistencies](https://www.cisco.com/c/en/us/support/docs/lan-switching/spanning-tree-protocol/24063-pvid-inconsistency-24063.html)
for the addresses, the tagging rules, and the PVID check.

- **Encapsulation.** An SSTP BPDU is an RST BPDU in LLC/SNAP (`AA AA 03`, OUI
  `00-00-0C`, PID `0x010B`) addressed to `01:00:0c:cc:cc:cd`, followed by the
  originating-VLAN TLV: type `0x0000`, length `0x0002`, then the VID. The
  payload is 50 octets: 8 of SNAP header, the 36-octet RST BPDU, and 6 of TLV
  (two of type, two of length, two of VID). That puts an untagged SSTP frame
  at 64 octets with its 14-octet Ethernet header and 68 with a VLAN tag, the
  sizes a PVST+ capture shows, so no padding to `minDataLength` applies.
  Wireshark reads the TLV at offset 36 counted from the protocol identifier,
  which is where the 36-octet RST body ends, so the TLV follows the RST shape
  and not the 35-octet Configuration one.
- **The codec reuses `putBody` and `readBody` by slicing, not by a new offset
  argument.** In an LLC payload the protocol identifier sits at octet 3, after
  the 3-octet LLC header; in a SNAP payload it sits at octet 8, after the
  8-octet SNAP header. So `putBody(payload[5:], b)` and `readBody(payload[5:])`
  land every field exactly where the SSTP layout puts it, with no change to
  either function. Why this matters enough to write down: the alternative,
  giving both functions a base offset, would touch the MST codec phase 3d
  landed for no behavior gain.
- **Where each tree goes**, Cisco's rules verbatim. With native VLAN 1: VLAN 1
  BPDUs go untagged to `0180.c200.0000` and untagged to the SSTP address, and
  non-VLAN-1 BPDUs go to the SSTP address tagged. With a native VLAN other
  than 1: VLAN 1 BPDUs go to the SSTP address tagged, and also to
  `0180.c200.0000` untagged on the native VLAN; every other VLAN goes to the
  SSTP address tagged. The unifying rule, and the one the code implements, is
  that each tree's SSTP BPDU rides its own VLAN through the port's ordinary
  egress tagging, and VLAN 1's tree additionally emits one IEEE-addressed
  untagged frame per port.
- **Tagging happens in `vswitch`, through `bridge.OriginateFrame`, not in
  `stp.Encode`.** `stp.Emission` gains `VID vlan.ID`. A zero VID means the
  frame goes out as the layer built it, untagged, with no VLAN membership
  check, which is exactly today's behavior and what the IEEE-addressed frame
  needs on a trunk with no native VLAN. A non-zero VID means
  `applySTPEffects` passes the frame through
  `bridge.OriginateFrame(port, vid, frame)` (`bridge/bridge.go:1544`) and
  drops the emission when the port does not carry that VLAN. Why: that
  function already implements "tagged when the VLAN is in `Tagged`, untagged
  when it is the port's untagged VLAN", which is Cisco's native-versus-tagged
  rule with no second implementation, and `applyLoopProtectEffects`
  (`switch.go:2054-2098`) is the landed precedent for a layer naming a VLAN
  and the switch resolving the frame. `stp` therefore never builds a VLAN tag.
  Consequence to accept: a port configured `PriorityTags: Always` gets a
  VID-0 priority tag on its untagged SSTP BPDU, which is what that port does
  to every other frame it originates.
- **`Emission` stops being convertible.** `applySTPEffects` currently writes
  `Emission(em)` (`switch.go:2046`), a struct conversion that the new field
  breaks. It becomes an explicit construction. `lag`'s identical conversion at
  `switch.go:2149` is untouched.

### Receiving

- **`Receive` keeps its signature and gains a sibling.** The link-level half of
  `Receive` (`layer.go:1558-1650`: receive counters, BPDU guard, loop-guard
  clear, protocol migration, auto-edge loss, the TCN shape) runs once per
  received frame whatever tree the frame belongs to, and the per-tree half
  (`layer.go:1652` onward, from the `internal :=` classification to the
  proposal and agreement handling) is extracted into an unexported
  `applyBPDU(t *tree, p *portState, now time.Time, b BPDU, flushes *[]FlushTarget) []Emission`.
  `Receive` calls it on `l.cist()`; the new
  `ReceiveSSTP(now time.Time, port string, vid vlan.ID, b BPDU) Effects` runs
  the link-level half on the CIST port state, syncs the link properties with
  the landed `syncInstancePorts` (`layer.go:874`), and calls `applyBPDU` on
  `l.treeFor(vid)`. Why not one `Receive` taking a VID: every existing caller
  and every layer test passes a BPDU that belongs to the common tree, and a
  VID parameter there would read as though an RSTP bridge classified its
  BPDUs per VLAN, which it does not.
- **An IEEE-addressed BPDU on a PVST bridge is VLAN 1's.** It reaches
  `Receive`, which applies it to `l.cist()`, which in PVST mode is VLAN 1's
  tree. No new code path.
- **The PVID check.** An SSTP BPDU whose TLV VID differs from the VLAN the
  switch classified the frame into is not applied. The arrival VLAN's tree
  marks that port `pvidInconsistent`, which holds it Discarding and excludes
  it from contributing a root vector, the same two effects `loopInconsistent`
  has in `recompute` (`layer.go:1236-1240`). A consistent SSTP BPDU on that
  tree clears it. Why the arrival VLAN's tree and not the TLV's: Cisco blocks
  "the traffic in the corresponding VLAN on a corresponding port", and the
  arrival VLAN is the one whose local traffic would cross a link the two ends
  disagree about.
- **`pvidInconsistent` is per tree, and is read from the tree's own port
  state, not from the CIST's.** `recompute` reads `loopInconsistent` two ways
  four lines apart: the root-vector loop reads the current tree's copy
  (`layer.go:1239`) and the role-assignment loop reads the CIST's
  (`layer.go:1307-1313`), because BPDU guard and loop guard are bridge-global
  and only the CIST's copy is ever written. PVID inconsistency is the
  opposite: it is set on the arrival VLAN's tree port, so both sites read
  `p.pvidInconsistent`. Why it is worth stating: following the neighbouring
  `cistP` pattern would read a field no tree but VLAN 1's ever has set, which
  disables the check for every VLAN it exists to protect.
- **`BlockReason` gains `pvid-inconsistent`, ranked below BPDU guard and above
  loop guard** in `portState.blockReason` (`layer.go:220-232`). Why that rank:
  BPDU guard disables the port outright, so nothing below it can be the
  decisive reason; and a PVID-inconsistent port is receiving BPDUs, which is
  the condition that clears `loopInconsistent` on every receive
  (`layer.go:1603`), so the two cannot both be true after a receive and the
  order only fixes what a reader sees if a future change makes them overlap.

### The boundary this phase does not model

- **PVST simulation is reported, not modeled.** A PVST bridge that receives an
  MST BPDU, and a non-PVST bridge that receives an SSTP BPDU, marks the
  receiving port a PVST boundary and keeps its existing behavior on it: the
  PVST bridge applies the MST BPDU's RST prefix to VLAN 1's tree, and the
  non-PVST bridge discards the SSTP BPDU's contents after counting it. Why
  discard rather than apply: an MSTP bridge's CIST does not run VLAN 20's
  tree, so feeding a VLAN 20 vector into it would elect a root from a tree
  that bridge is not running. The neighbour relationship still converges,
  because a PVST+ bridge sends VLAN 1's tree to the IEEE address and that
  reaches the CIST unchanged. Source: Cisco,
  [PVST Simulation on MST Switches](https://www.cisco.com/c/en/us/support/docs/lan-switching/multiple-instance-stp-mistp-8021s/116464-configure-pvst-00.html).
- **The boundary mark lives on the CIST port state and clears on a link
  transition**, like `bpduGuardDisabled`. Why: it is a statement about which
  protocol the neighbour speaks, and the neighbour can only be replaced by
  something that takes the link down.
- **`stp-pvst-boundary` is raised per port and VLAN, through the hit-set
  pattern, not through scope nesting.** `Switch` records a
  `{port, vid}` hit when a journey crosses a boundary-marked port on a VLAN
  other than 1, and `pvstBoundaryIssues` turns the hits into
  `analysis.Unsupported` issues scoped
  `analysis.ProtocolScope(nodeID, "stp", "<port>/<vid>")`, the shape
  `mcastGroupScope` uses (`switch.go:706`). Why not
  `FieldScope(FieldScope(stpScope, "ports", p), "vlans", "20")`: `Contains`
  is a key-prefix test (`analysis/scope.go:141-143`), the bridge consults
  `FieldScope(stpScope, "ports", ingress)` on every gated frame
  (`bridge/bridge.go:742`), and a nested scope is contained by it, so a VLAN 1
  journey through the port would pick up VLAN 20's issue and R14d would fail
  in exactly the case it exists to check. A protocol scope with a different
  instance string shares no prefix with the `"0"` instance scope, so it
  reaches a result only through the hit set that named it.

### What stays out

- **netmodel stays RSTP-only.** No schema reports per-VLAN trees, so nothing
  loads a `PVST` configuration from a model. The `netmodel` report is
  unchanged and keeps saying what it says today.

## Requirements

1. A PVST bridge elects a separate root per VLAN, and each VLAN blocks its
   own trunk.
   Acceptance: two PVST switches joined by `l1` and `l2`, with sw2's VLAN 10
   path cost inflated on `l1` and its VLAN 20 path cost inflated on `l2`. A
   VLAN 10 frame crosses `l2` and a VLAN 20 frame crosses `l1`;
   `VLANPortInfo(10, "l1")` reports `Alternate`/`Discarding` while
   `VLANPortInfo(20, "l1")` reports a forwarding role.
2. Each tree's SSTP BPDU rides its own VLAN with the port's ordinary egress
   tagging, and VLAN 1's tree also emits one untagged IEEE-addressed frame per
   port.
   Acceptance: on a trunk carrying VLAN 10 tagged with PVID 1, one hello
   produces three emissions: an SSTP frame with one `0x8100` tag for VID 10,
   an untagged SSTP frame for VID 1, and an untagged frame to
   `01:80:c2:00:00:00`. On the same trunk configured with PVID 20 and VLAN 1
   tagged, VLAN 1's SSTP frame carries a VID 1 tag and the IEEE-addressed
   frame is still untagged.
3. `EncodeSSTP` and `DecodeSSTP` round-trip, and `DecodeSSTP` refuses a frame
   that is not SSTP.
   Acceptance: `EncodeSSTP(b, 20, src)` produces a 50-octet payload beginning
   `AA AA 03 00 00 0C 01 0B` and ending `00 00 00 02 00 14`, which reads as
   type 0, length 2, VID 20; a payload with OUI `00-00-0D`, a TLV type other
   than 0, a TLV length other than 2, or fewer than 50 octets is refused with
   `ReasonUnsupportedBPDU`.
4. An SSTP BPDU whose TLV VID disagrees with the VLAN it arrived on is not
   applied, and blocks the arrival VLAN on that port until a consistent BPDU
   arrives.
   Acceptance: a BPDU with TLV VID 20 classified into VLAN 10 leaves
   `VLANPortInfo(10, port).BlockReason` equal to `pvid-inconsistent` and
   `Forwards(port, 10)` false, while `Forwards(port, 20)` is unchanged; a
   subsequent BPDU with TLV VID 10 on VLAN 10 clears both.
5. A PVST port facing an MSTP bridge reports `stp-pvst-boundary` for VLANs
   other than 1 and stays `Complete` for VLAN 1.
   Acceptance: after an MST BPDU arrives on `l1`, a VLAN 20 journey through
   `l1` carries one `Unsupported` issue with code `stp-pvst-boundary` scoped
   `protocol["stp","l1/20"]`, and a VLAN 1 journey through `l1` carries none.
   The reciprocal holds: an MSTP bridge that receives an SSTP BPDU on `l1`
   raises the same issue for the VLAN the journey uses.
6. `vswitch.New` refuses a PVST switch carrying a VLAN with no tree.
   Acceptance: `Bridge.VLAN.Table` holding VIDs 10 and 20 with
   `PVST.Trees` holding only 10 returns an error naming field
   `stp.pvst.trees` and VLAN 20; adding VLAN 20's tree constructs.
7. `stp.Config` refuses `MST` and `PVST` set together.
   Acceptance: `Validate` returns an error naming field `pvst` when both are
   non-nil, and the error is returned before either sub-validation runs.
8. The corpus admits `planning/pvst-per-vlan-root`, whose false answer is one
   root for every VLAN.
   Acceptance: `RegisterSTPCases` registers it and the corpus conformance test
   passes with its expected metadata `Complete` over `WholeScope`.
9. RSTP and MSTP behavior is unchanged.
   Acceptance: every existing test in `src/common/netsim/vswitch/stp` and
   `src/common/netsim/vswitch` passes without a change to its expectations,
   including the canonical strings in `portInfoSnapshot`.

## Out of scope

- 802.1D STP per VLAN, and PVST inconsistency states other than the PVID
  check (type inconsistency, port-VLAN-ID mismatch on an access port).
- Modeling Cisco PVST simulation on an MSTP boundary port, which this phase
  reports as unsupported.
- netmodel loading of per-VLAN trees, and any schema change.
- A per-VLAN `Root()` accessor on `Switch`. `VLANPortInfo` on the layer is
  what this phase adds; the fabric-level view stays the common tree's.

## Units

### U1. PVST configuration
Files: `src/common/netsim/vswitch/stp/pvst.go`,
`src/common/netsim/vswitch/stp/config.go`,
`src/common/netsim/vswitch/stp/config_test.go`
After: none
Change: `PVST` holds `Trees map[vlan.ID]Tree`, where `Tree` carries
`Priority uint16`, `PriorityPresent bool`, and `Ports map[string]InstancePort`
reusing the landed per-port priority and path-cost type. `PVST`, `Tree` and
`Config` implement the `TypeID`/`Canonical` fact pair the way `MST` and
`Instance` do. `Config.PVST` selects the mode; `Clone` deep-copies it;
`Normalize` fills each tree's priority with the default, inserts a default
VLAN 1 tree when absent, and sorts nothing else. `Config.Validate` refuses
`MST` and `PVST` together before validating either, and `PVST.Validate`
refuses a VID outside 1..4094, a priority that is not a multiple of 4096, a
port absent from the port table, a port absent from `Config.Ports`, and a
path cost above `MaxPathCost`, matching `MST.Validate`'s field names with
`pvst.trees.<vid>` in place of `mst.instances.<id>`.
Tests: `config_test.go` cases for both pointers set, each `PVST.Validate`
rejection with its field name, `Normalize` inserting VLAN 1 into an empty and
into a non-empty `Trees`, `Normalize` leaving an explicitly configured VLAN 1
tree alone, `Clone` independence after mutating the clone's `Trees` and a
tree's `Ports`, and the canonical string for a two-tree `PVST`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/stp`

### U2. The SSTP encapsulation codec
Files: `src/common/netsim/vswitch/stp/sstp.go`,
`src/common/netsim/vswitch/stp/bpdu_test.go`
After: none
Change: `GroupAddressSSTP` is `01:00:0c:cc:cc:cd`.
`EncodeSSTP(b BPDU, vid vlan.ID, src netaddr.MAC) (ethernet.Frame, error)`
writes the 50-octet payload described under Decisions, reusing
`putBody(payload[5:], b)` for the RST body, forcing wire type `0x02` and
version at least 2, and refusing a `b` whose `ConfigID` is non-nil, since an
MST BPDU has no SSTP form.
`DecodeSSTP(f ethernet.Frame) (BPDU, vlan.ID, error)` refuses a payload
shorter than 50 octets, a SNAP header other than `AA AA 03`, an OUI other
than `00-00-0C`, a PID other than `0x010B`, a protocol identifier other than
0, a version below 2 or a wire type other than `0x02`, a TLV type other than
0, and a TLV length other than 2, each with `ReasonUnsupportedBPDU` and an
`errs` attribute naming the offending field; otherwise it returns
`readBody(payload[5:])`'s BPDU with `Version`, `Type` and `Flags` filled, and
the TLV's VID.
Tests: `bpdu_test.go` gains an SSTP block: a round trip over a table of BPDUs
including every flag bit set and cleared; a golden payload asserted octet by
octet as a hex literal for VID 20, so the layout is pinned by bytes rather
than by the encoder's own choices; one rejection case per refused field; and
a case asserting that `Decode` refuses an SSTP frame and `DecodeSSTP` refuses
an LLC BPDU frame, so the two codecs cannot be crossed.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/stp`

### U3. Per-VLAN trees in the layer
Files: `src/common/netsim/vswitch/stp/layer.go`,
`src/common/netsim/vswitch/stp/tree.go`,
`src/common/netsim/vswitch/stp/layer_test.go`,
`src/common/netsim/vswitch/stp/tree_internal_test.go`
After: U1, U2
Change: `tree` gains `vid vlan.ID`, zero outside PVST mode. `Layer` gains
`pvst *PVST`. `newLayer` builds VLAN 1's tree at `cistID` with `vid` 1 and
each other VLAN's at `treeID(vid)`, fills `vidToTree` and `treeVLANs` for
every tree including VLAN 1's (whose entry is `[]vlan.ID{1}`, not the empty
"every FID" marker MSTP's CIST uses, because in PVST mode VLAN 1's topology
change stales only VLAN 1). The transmit budget moves behind
`func (l *Layer) tx(t *tree, name string) *portTx`, which returns the single
per-port budget outside PVST mode and a per-tree, per-port budget inside it,
so seven VLANs at a hold count of six cannot starve the seventh; every
`l.portTx[...]` site becomes an `l.tx(t, ...)` call against the tree that
site belongs to.
`recompute`'s boundary-role branch (`layer.go:1291`) gains `l.mst != nil`, as
Decisions requires. `Wake`'s two emission-driving loops stop being bound to
`l.cist()`: the held-transmission release loop (`layer.go:1849-1870`, which
reads `l.portTx[name]` and calls `emit` with the CIST) and the hello-timer
loop (`layer.go:1872-1880`, which reads `t.helloTimer`) both move inside a
walk over `l.treeOrder`, reading each tree's own `helloTimer` and
`l.tx(mt, name)`, the way `Wake`'s forward-delay, age-out and
topology-change-timer loops already do (`layer.go:1888-1948`). Why this is
part of the unit rather than a later tidy: outside PVST mode `l.treeOrder`
holds the CIST first and the budget is shared, so the walk is behavior-neutral
for RSTP and MSTP; inside it, without the walk a per-VLAN tree's periodic
hello never fires and a held transmission on it is never released, so R2
cannot be observed from a `Wake` tick and the seven-VLAN budget test cannot
pass. `recomputeAll` passes `emit` true for every tree in PVST mode. `emit`
builds the frame per mode:
`Encode` with `Emission.VID` zero outside PVST, and inside it `EncodeSSTP`
with `Emission.VID` set to `t.vid`, plus, for VLAN 1's tree only, a second
`Emission` carrying `Encode`'s IEEE-addressed frame with `VID` zero, both
frames spending one budget slot because they are one transmission.
`Receive`'s per-tree half moves to `applyBPDU`; `ReceiveSSTP` is added as
described under Decisions, including the PVID check, which sets
`portState.pvidInconsistent` on the arrival VLAN's tree port and returns
before `applyBPDU`. `portState` gains `pvidInconsistent`, `blockReason` gains
its case, and `recompute`'s root-vector exclusion (`layer.go:1239`) and its role-assignment
switch (`layer.go:1307`) both gain a `p.pvidInconsistent` case reading the
tree's own port state, not `cistP`'s. `portState` gains `pvstBoundary` on the
CIST, set by `Receive` when an MST BPDU arrives on a PVST bridge and by
`ReceiveSSTP` when an SSTP BPDU arrives on a bridge that is not one, cleared
by `LinkChange` on a down transition, and read through a new
`PVSTBoundary(port string) bool`. `ReceiveSSTP` on a non-PVST bridge counts
the BPDU, marks the boundary, and applies nothing.
`VLANPortInfo(vid vlan.ID, port string) PortInfo` joins `InstancePortInfo`.
Tests: `layer_test.go` gains per-VLAN root election over a two-port bridge
with diverging per-tree path costs, run after both ends have exchanged
ordinary VLAN 1 hellos, so the test fails if the boundary-role branch is left
ungated; a `Wake`-driven hello tick asserting that each VLAN's tree emits, not
only VLAN 1's; the three-emission and the
native-VLAN-20 emission shapes of R2, asserted on `Emission.VID` and the
decoded frames; the PVID check setting and clearing `pvid-inconsistent` and
leaving the other VLAN's port untouched; the budget test that seven VLANs
each emit at a hold count of six; VLAN 1's flush naming `[]vlan.ID{1}` rather
than an empty FID set; the boundary flag set by an MST BPDU on a PVST bridge
and by an SSTP BPDU on an MSTP bridge, cleared by a link down; and an SSTP
BPDU on an MSTP bridge leaving every tree's port state unchanged.
`tree_internal_test.go` gains the `vidToTree` and `treeVLANs` shape for a
three-VLAN PVST bridge and the `tx` budget's keying in both modes. Nothing
here covers whether the emitted frame is tagged, which is U4's; these tests
assert the VID the layer names, not the tag.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/stp`

### U4. The switch side
Files: `src/common/netsim/vswitch/switch.go`,
`src/common/netsim/vswitch/config.go`,
`src/common/netsim/vswitch/switch_test.go`
After: U3
Change: `sstpGroupAddress` joins `stpGroupAddress` (`switch.go:40`).
`forward` intercepts a frame addressed to it through a new `interceptSSTP`,
which classifies the frame with `s.bridge.Ingress(now, ingress, f, false,
false)` the way `interceptLoopProtect` does (`switch.go:1790`), decodes with
`DecodeSSTP`, and calls `s.stp.ReceiveSSTP(now, port, in.FID, bpdu)`, with
the same bad-BPDU and port-down branches `interceptBPDU` has and a trace step
carrying the TLV VID and the classified VID so a PVID inconsistency is
visible in the trace. A switch with no bridge cannot classify, so an SSTP
frame there is treated as an unsupported BPDU. The `missingSTP` branch
(`switch.go:846`) consults the STP scope for the SSTP address too.
`applySTPEffects` constructs `Emission` explicitly and routes a non-zero
`em.VID` through `s.bridge.OriginateFrame`, dropping the emission when the
port does not carry the VLAN. `vswitch.New` refuses a PVST switch whose
`Bridge.VLAN.Table` holds a VID absent from the normalized `PVST.Trees`.
`Switch` gains `pvstBoundaryHits map[pvstBoundaryHit]struct{}` reset in
`forward` beside the other hit sets (`switch.go:711-712`), recorded when a
journey's ingress or egress port reports `PVSTBoundary` and the journey's
VLAN is not 1, and drained by `pvstBoundaryIssues` into `Unsupported` issues
carrying `IssuePVSTBoundary`, following `mcastQueryUnobservedIssues`
(`switch.go:661-686`).
Tests: `switch_test.go` gains the R2 tagging cases end to end (tagged VLAN 10
SSTP frame, untagged VLAN 1 SSTP frame, untagged IEEE frame, and the
native-VLAN-20 variant), an SSTP frame on a port that does not carry its VLAN
producing no emission, the PVID-inconsistency trace step naming both VIDs,
`vswitch.New`'s refusal and its accepting counterpart, the boundary issue
raised for VLAN 20 and absent for VLAN 1 on the same port in both directions
of R5, and an SSTP frame reaching a switch with `missingSTP` set consulting
the STP scope.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch`

### U5. The corpus case
Files: `src/common/netsim/internal/netsimtest/stp_cases.go`
After: U4
Change: `stpPVSTFabricSpec` builds the two-switch, two-link fixture
`stpMSTFabricSpec` (`stp_cases.go:201`) builds, with `PVST` configurations in
place of the MST regions: sw2's VLAN 10 path cost inflated on `l1` and its
VLAN 20 path cost inflated on `l2`, so the two VLANs elect opposite links.
`CasePlanningPVSTPerVLANRoot` asks whether VLAN 10's frame crosses the link
VLAN 10's own tree elected, names the false answer "one spanning tree covers
the bridge, so both VLANs follow the same link", and expects `Complete` over
`WholeScope`. `RegisterSTPCases` registers it and its doc comment gains the
case.
Tests: the corpus conformance test (`netsimtest/corpus_test.go`) is the test;
it runs the registered case against its expected trace, metadata, rules,
subjects and facts.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/internal/netsimtest`

### U6. Documentation
Files: `docs/architecture/2026-09-10-virtual-device-direction.md`,
`src/common/netsim/vswitch/stp/README.md`,
`src/common/netsim/vswitch/README.md`,
`docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md`,
`docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-phase3e-plan.md`
After: U4, U5
Change: the direction record's "The layer keys its state by tree and the gate
answers per VLAN" bullet states the third mode and that VLAN 1's tree holds
the CIST slot as the common tree; its "A topology change flushes by tree, and
the CIST flushes everything" bullet states that in PVST mode VLAN 1's tree
names VLAN 1 rather than every FID, and why the MSTP over-flush argument does
not carry over; and three bullets are added for the SSTP encapsulation and
where tagging happens, the PVID check, and the PVST boundary this phase
reports rather than models. The `stp` README documents `Config.PVST`, the two
receive entry points, and `VLANPortInfo`. The `vswitch` README documents the
SSTP intercept and the `stp-pvst-boundary` issue. The parent plan's `U3e`
gains its `Landed:` range, and this plan's `status` is set with an outcome
note.
Tests: none. This unit changes prose only; the verifier's documentation and
layout checks are what run over it.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/architecture/2026-09-10-virtual-device-direction.md src/common/netsim/vswitch/stp/README.md src/common/netsim/vswitch/README.md docs/plans`

Waves: U1 U2 | U3 | U4 | U5 | U6

U6 trails U5 rather than running beside it because it fills the parent plan's
`Landed:` range for this phase and writes this plan's outcome note, neither of
which is knowable while the corpus case is still landing.

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>
go test -race ./src/common/netsim/...
go test -race ./test/conformance/...
```

The R2 tagging rules are the part no unit test can confirm against a real
device, so the golden payload in U2 and the decoded-tag assertions in U4 are
what stand in for a capture. If a PVST+ capture becomes available, decoding it
against `DecodeSSTP` is the check worth adding.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `src/common/netsim/vswitch/stp/README.md` and
      `src/common/netsim/vswitch/README.md` updated in the same change as the
      code they describe.
- [ ] The direction record amended in U6, with the amendment accepted before
      the phase closes.
- [ ] Every existing `stp` and `vswitch` test passes unchanged, which is R9.
- [ ] The parent plan's `U3e` `Landed:` line filled.
- [ ] This plan's `status` set with an outcome note under its title.
- [ ] No plan labels (U1, R3) in code, comments, or commit messages.

## Open questions

- Whether a PVST bridge should emit VLAN 1's IEEE-addressed BPDU on a trunk
  whose native VLAN is not 1 as VLAN 1's tree or as the native VLAN's. Cisco's
  page says VLAN 1's BPDUs go to the IEEE address "on the Native VLAN of the
  802.1Q trunk, untagged", which this plan reads as VLAN 1's tree information
  in an untagged frame, since that is what makes the PVID check detectable at
  the far end. The implementer should keep this reading unless a capture says
  otherwise; the alternative reading would put the native VLAN's tree in that
  frame and make the mismatch invisible.
- Whether `pvstBoundary` should also be set by an RSTP bridge receiving an
  SSTP BPDU, which this plan does, or only by an MSTP one. The plan chose the
  wider rule because an RSTP bridge is equally unable to answer for VLAN 20,
  and R14d names only the MSTP direction, so the wider rule adds an issue the
  parent did not ask for. Narrowing it is a one-line change in U3.
