---
title: Network Simulation Analysis Completeness, Phase 5 - Plan
type: feat
date: 2026-09-17
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
parent: docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
---

# Network simulation analysis completeness, phase 5: state ownership, derivation invalidation, and fork isolation - Plan

> Implemented. 6 units, 2026-09-18. All units landed across state ownership
> direction, origin and lifetime separation on retained records, diff coverage
> gating, Switch/Bridge/Fabric fork cloning with isolated mutable state,
> dependency-keyed retention reporting, and representative scale topology with
> allocation gating.

## Goal

A caller can take a running simulation, make a second copy of it that diverges
freely, and separately ask what the same state becomes under a changed
configuration — and can tell the two apart. The means are a new `Fork` on
`vswitch.Switch`, `bridge.Bridge` and `fabric.Fabric` whose field-by-field copy
rule is a reflection-checked table, a retention key per capability layer that
names every input its runtime state depends on rather than diffing the layer's
own configuration, an origin and a lifetime on every retained record in place of
the `Static` boolean that answers both questions today, and a declared
representative scale with an allocation contract over `Fork`.

Stop condition: the switch binds itself into its own layers — the bridge's gate
is the `stp` or `loopprotect` layer itself
(`src/common/netsim/vswitch/switch.go:314`, `:325`), and its selector and
resolver hold the `Switch` (`:295`, `:273`). If those bindings cannot be rebound
to the fork from outside the layer packages, `Fork` cannot be a field-by-field
copy at all, and the plan's shape — a classification table over each struct's
fields — is the wrong one.

### What this phase claims of the parent

- Parent R24, R25 and R37's derive-and-clone half: claimed whole.
- Parent R26: claimed whole for `Fork`, and for `Snapshot` as narrowed below.
- Parent R23 asks for three axes — owner, an origin of `configured`,
  `observed`, `derived` or `assumed`, and a lifetime of `static`, runtime,
  timer, queue or scenario-owned. This phase delivers two values on each axis it
  keeps, `Configured | Observed` and `Static | Aging`, and no owner axis: the
  owner is the package the record lives in, which the type system already says,
  and `derived`, `assumed`, timer, queue and scenario-owned have no record to
  sit on until parent U6 adds scenarios. A narrowing, stated here rather than
  left to be noticed.
- Parent R40 spans forking, replay and search. Replay is parent U6 and search is
  parent U7, so this phase claims the forking third, and within it gates
  allocations while reporting runtime rather than gating it.

## Decisions

- **`Fork` is a new operation beside `Derive`, not a special case of it.**
  `func (s *Switch) Fork() *Switch` and `func (f *Fabric) Fork() *Fabric`. Why:
  `Derive` rebuilds from a `ConstructionSpec` and keeps only what a retention
  arm names, so `Derive(cur, cur.Spec())` is not an identity. It drops the
  routing layer outright — `src/common/netsim/vswitch/derive.go` has no routing
  arm at all, so after phase 4b every derived switch loses its neighbor table
  and every held frame. It reconstructs multicast state by replaying a
  fabricated IGMP query from `192.0.2.1` (`derive.go:277`). At the fabric it
  calls the same `build` as `New` (`src/common/netsim/fabric/derive.go:19`,
  `fabric.go:207`), so the queue, the journeys, and the frame-id counter all
  reset. A current-against-candidate comparison needs the candidate to start
  from the current execution, which none of that gives.
- **`Fork` shares what construction fixed and deep-copies what execution
  changes, and the split is a table each class is checked against.** Every field
  of `Switch`, `Bridge` and `Fabric` carries one of three classes —
  `immutableShared`, `deepCopied`, `resetOnFork` — and each class has its own
  assertion, not just an entry: an `immutableShared` field is pointer-identical
  across the two after `Fork`, a `resetOnFork` field equals its type's zero
  value, and a `deepCopied` field has a mutation probe. Why:
  [one slot, two roles](../solutions/architecture-patterns/one-slot-two-roles-is-a-defect-class-not-a-defect.md)
  records that a first version of exactly this table "recorded a class per field
  and never read it", and that the reading is the load-bearing part. A table
  whose only mechanism is the `deepCopied` probes leaves a field misclassified
  `immutableShared` passing green, which is the case `seeds` below would have
  been.
- **`Switch.Fork` shares `cfg`, `nodeID` and `metadata` and deep-copies
  `seeds`; `Fabric.Fork` deep-copies `cfg`.** Why: nothing assigns `cfg`,
  `nodeID` or `metadata` after `NewWithSpec`, and both accessors hand out clones
  (`src/common/netsim/vswitch/switch.go:362`, `:375`). `seeds` looks the same
  and is not: exported `Learn` replaces the slot (`switch.go:463`). It happens
  to be safe today because `Learn` clones before appending (`switch.go:455`),
  which is an invariant of one function body and not something a fork should
  rest on. The fabric's configuration is mutable outright: `SetFault` writes
  `f.cfg.Cables[idx].Fault` in place (`src/common/netsim/fabric/fabric.go:1018`).
- **`Fabric.Fork` rebuilds `byEnd` rather than copying it.** Why:
  `linkEndRef` holds pointers into the backing array of `f.links`
  (`src/common/netsim/fabric/fabric.go:410`), and `SetFault` overwrites a link
  in place precisely because `byEnd` points at it (`fabric.go:1016`). A copied
  `byEnd` would point into the source fabric's links, and a fault set on either
  side would be seen by both.
- **An `ethernet.Frame` is an immutable value, and every copy path inside the
  library shares it.** `Fork` shares the frames in the queue, in the egress
  queues, in the hold queues and in the journeys; `Snapshot` shares the frames
  it reports. Why: the premise holds on the current tree — `Arrival` is a value
  with no pointers (`src/common/netsim/fabric/queue.go:24`) and every tag rewrite
  allocates before appending
  (`src/common/netsim/vswitch/bridge/bridge.go:1505`, `:1521`,
  `src/common/netsim/vswitch/traffic/mirror.go:101`) — and copying every queued
  and held payload is the dominant term at the declared scale. The one exception
  stays: `Report()` deep-copies frames (`src/common/netsim/fabric/journey.go:308`,
  pinned by `run_internal_test.go:28`), because it hands journeys to callers
  outside the library, and that contract is already paid for. `Fork` therefore
  copies a journey with a shallow-frame rule of its own rather than reusing
  `Journey.clone`. No test in this phase covers a future in-place payload write
  — a static scan cannot decide what a body does with a slice it holds, per
  [prove a body property by execution](../solutions/conventions/prove-a-body-property-by-execution-not-by-ast-reading.md)
  — so the rule is stated in the direction record and in both package READMEs
  and carried by review. That is the one risk in U4 nothing in U4 verifies.
- **Retention is keyed by an explicit per-layer key, not by
  `Diff(ownConfig) == 0`.** Each capability layer exports
  `RetentionKey(...) string`, a canonical encoding of every normalized input its
  runtime state depends on, built from the snapshot-fact helpers each package
  already uses for `Diff`'s whole-record arms. Why a string and not a comparable
  struct: no layer `Config` is comparable — `stp.Config` alone holds
  `Ports map[string]Port`, `MST *MST` and `PVST *PVST`
  (`src/common/netsim/vswitch/stp/config.go:116`) — and the packages already own
  injective canonical encodings for these shapes. Why a key at all: three of the
  five arms in `vswitch.Derive` already reach outside the layer's own `Diff` by
  hand (`lagMemberStatesEqual`, `loopProtectPortStatesEqual`, and the per-entry
  predicate passed to `mcast.Retain`), which is the admission that the
  own-config diff is not the dependency set. Spanning tree has no such extension
  although its runtime state carries per-port link state, point-to-point and
  speed.
- **Both keys are computed from constructed switches, never from a raw target
  specification.** Why:
  [validate and derive judge what New builds](../solutions/architecture-patterns/validate-and-derive-judge-what-new-builds.md)
  is the record of what happens otherwise, and the comment U5 deletes
  (`src/common/netsim/vswitch/derive.go:43`, "Both sides are compared as New
  filled them, so a bridge address the switch assigned does not read as a
  change") is that fix's only in-code statement.
  `switch_test.go:2097` `TestDerivedSwitchKeepsRolesWithAssignedBridgeAddress`
  catches the regression, so this is a statement that must survive the rewrite,
  not a hole in coverage.
- **`lag`'s key normalizes with the port table and the system id before
  keying.** Why: `lag.Config.Normalize` takes `(ports, systemID)`
  (`src/common/netsim/vswitch/lag/config.go:173`), so `lag.Diff(a, b Config)`
  cannot self-normalize the way its niladic siblings do, and the switch's base
  MAC reaches the LAG layer only as that `systemID`. A key over an
  un-normalized `lag.Config` would miss a base-MAC change, which is the parent's
  own R24 acceptance example.
- **`traffic.Diff` self-normalizes; `lag.Diff` keeps its caller contract.** Why:
  `traffic.Config.Normalize` is niladic
  (`src/common/netsim/vswitch/traffic/config.go:100`) and covers all three
  mirror selector fields that `diffMirror` hand-normalizes today
  (`traffic/diff.go:234`, `:237`, `:240`), so the package can join its seven
  siblings — `bridge/diff.go:135`, `loopprotect/diff.go:65`, `mcast/diff.go:94`,
  `phy/diff.go:121`, `port/diff.go:9`, `routing/diff.go:230`, `stp/diff.go:128`
  — and the coverage gate then has one contract to test rather than two.
  `lag.Diff` cannot, per the previous decision, and keeps the sentence in its
  doc comment that puts normalization on its caller.
- **`Derive` stops copying `portP2P` and `portSpeed` wholesale.** It carries an
  entry only for a port whose target `port.Port` and resolved phy speed equal
  the current one's. Why: today the two maps are copied verbatim when the
  spanning tree layer is retained (`derive.go:50-61`), and
  `recomputeProtocolLinkIssues` reads `s.portP2P[p.Name]` to decide whether a
  port that is `Up` and not forced has an unknown duplex
  (`src/common/netsim/vswitch/switch.go:3085`). A port whose resolved speed
  changed in the target therefore keeps the old speed's path cost and the old
  `PointToPointTrue`, and suppresses the `Incomplete` issue it should raise
  until something calls `LinkChange`. An `Unknown` operational status is not the
  case to test here: it sets the flag earlier and `continue`s before `portP2P`
  is read at all (`switch.go:3075`).
- **`Derive` reports what it retained, through an accessor.**
  `func (s *Switch) Retention() Retention` lists each capability layer as kept
  or rebuilt and, for a rebuilt one, the dependency that differed;
  `func (f *Fabric) Retention() map[string]vswitch.Retention` does the same per
  node. Why an accessor and not a third return value: `Spec()` and `Config()`
  already establish the pattern on `Switch` for information most callers ignore.
  (`AGENTS.md` would permit the breaking signature; the precedent is the reason,
  not the compile cost.)
- **`Origin` and `Lifetime` replace the `Static` boolean on `bridge.Entry`,
  `bridge.Seed` and `mcast.RouterPort`.** `Origin` is `Configured` or `Observed`;
  `Lifetime` is `Static` or `Aging`. Why: `Static` answers both "who put this
  here" and "does this age", which is the defect shape the phase that just
  closed named. The cost is already visible: `restoreMulticastState` re-learns a
  retained dynamic router port by synthesising an IGMP query from the
  documentation address `192.0.2.1` (`derive.go:277`), because the layer has no
  way to install an observed record carrying its own expiry. With the two axes
  separated, retention installs the record. This breaks three exported types;
  `AGENTS.md` asks for the breaking change rather than a shim.
- **The `Diff` coverage gate is reflection-driven and its covered set is a
  literal checked against a directory walk.** A helper in
  `src/common/netsim/internal/netsimtest` walks a `Config` by reflection,
  perturbs one leaf at a time, and asserts `Diff` reports a change naming that
  leaf. A second helper walks `src/common/netsim/vswitch/`,
  `src/common/netsim/vswitch/*/` and `src/common/netsim/fabric/` for files named
  `diff.go`, and fails on a package path absent from a literal list in
  `netsimtest`. Why a literal checked against a walk rather than a filename
  convention:
  [a gate selected by name stops running silently](../solutions/conventions/a-gate-selected-by-name-stops-running-silently.md)
  says to enumerate and count what ran, and Go builds one test binary per
  package, so no registration a package's test makes is visible to another's.
  The walk is what grows; the literal is what a person edits deliberately. What
  it cannot check is that the named file's test actually calls the helper — a
  gutted `diff_coverage_test.go` still satisfies the enumeration.
- **The perturbation asserts it changed something.** For each leaf the helper
  compares the perturbed value against the original and fails the field if they
  are equal, rather than skipping. Why: the same solution records that a check
  which plants a value proves nothing unless the plant is known to differ from
  what was there, and a leaf whose type has one reachable value would otherwise
  pass green forever.
- **The gate lands against a tree that currently passes it.** Every exported
  `Config` field in the capability packages reaches its `Diff` today, so the
  gate adds no arm; its own fixture tests are the evidence that it can fail.
  Why: the parent wrote U5 expecting missing arms, and the cheap version of that
  unit — find the gaps, patch them — has nothing to do. The value is in the next
  field, which is the only value this kind of gate ever has.
- **`port` gets a builder-driven variant of the gate, not the reflection one.**
  Why: `port` has no `Config`; `Diff` takes `Table`
  (`src/common/netsim/vswitch/port/diff.go:9`), whose two fields are unexported
  and built through `port.NewBuilder` (`port/port.go:196`). The variant walks
  the exported `port.Port` instead and builds each perturbed table through the
  builder.
- **The representative scale is eight nodes of thirty-two ports.** Sixteen
  VLANs trunked between nodes, one LAG of two members per node, rapid spanning
  tree on every node, one VRF per node with sixty-four routes and sixty-four
  neighbors, sixty-four hosts, two thousand and forty-eight learned forwarding
  entries, and four thousand and ninety-six queued arrivals at the moment of the
  fork. Why: the lab captured five switches of twenty-four to thirty ports
  carrying two to four VLANs with one LAG each and a single spanning tree
  (`docs/research/device-inventory/README.md:30`, and
  `docs/research/device-inventory/lab/labsw06-ruckus-icx7150.md:125`, `:127`),
  so this is one step past what the library is being built to shadow, and every
  number is a power of two so a later change to one reads as deliberate.
- **The resource contract gates allocations and reports runtime.**
  `testing.AllocsPerRun` over `Fabric.Fork` at the declared scale must stay at
  or under a constant recorded in the test, and a second case at four times the
  queue depth must stay under a stated bound, so the copy is shown linear in
  mutable state rather than in the configuration. A `Benchmark` records
  wall-clock and is not a gate. Why: allocation counts are deterministic and
  machine-independent, while `AGENTS.md` runs the whole suite under `-race`,
  where a nanosecond budget is a flake source.
- Ruled: `mcast.Layer.InstallObserved` takes `(vid, port, expires)`, dropping
  the `group` parameter U2's own Change bullet names. Why: U2's Tests line
  describes only a router-port install ("produces a router port reporting
  Observed, Aging with the expiry it was given"), `RouterPort` carries no
  group field, and nothing in R4 or U5's restoreMulticastState rewrite reads
  a group for the router-port case the method exists to fix. Cost if wrong:
  U5, the only caller, gains one more parameter when it wires the method in;
  no wire shape or accepted record depends on the signature.

## Requirements

1. **R1 (parent R26).** `Switch.Fork` and `Fabric.Fork` return a copy that
   shares no mutable state with its source. Acceptance example: fork a fabric
   mid-run, run the fork one hundred steps, and the source's `Snapshot()`,
   `Report()`, every device's `Entries()`, `Roles()`, counters, neighbor table
   and held frames equal what they were before the fork; `Learn` on the forked
   switch leaves the source's `Spec().Seeds` unchanged; and stepping the source
   afterwards produces the arrival a never-forked twin produces.
2. **R2 (parent R26).** `fabric.Snapshot` shares no mutable state with the live
   fabric, frames excepted by the immutability rule. Acceptance example: take a
   snapshot, step the fabric until its queue, its counters and one device's
   forwarding database have all moved, and the snapshot's `Queue`, `Queued`,
   `Busy`, `Links` and `Devices` are what they were; the frames the snapshot
   reports are permitted to be the same backing arrays.
3. **R3 (parent R26).** A snapshot reports the state phase 4b added.
   Acceptance example: with one neighbor in `Incomplete` and one frame held for
   it, `Snapshot().Devices["sw1"]` names that neighbor, its state, and the depth
   of its hold queue.
4. **R4 (parent R23, narrowed).** A retained record that can be either
   configured or observed carries its origin and its lifetime as two separate
   values — the forwarding entry, the forwarding seed, the multicast router
   port, and the neighbor entry. A record with one reachable origin, such as
   `mcast.Entry`, is left alone and its README says why. Acceptance example: a
   seed with `Origin: Configured, Lifetime: Static` and a learned entry with
   `Origin: Observed, Lifetime: Aging` for the same MAC in different FIDs both
   appear in `Entries()` reporting their own two values, and a
   `Origin: Configured, Lifetime: Aging` entry — which the `Static` boolean
   cannot express — ages out while an `Origin: Observed, Lifetime: Static` one
   does not.
5. **R5 (parent R24).** A capability layer's runtime state survives `Derive`
   only when its retention key is unchanged, and both keys are computed from
   constructed switches. Acceptance example: with `cfg.STP` untouched, moving
   one spanning-tree port's `AdminStatus` to `Down` in the target rebuilds the
   spanning tree layer and `Retention()` names the port state as the dependency
   that differed; changing an unrelated traffic policer keeps it; and a switch
   whose bridge address `New` assigned keeps its roles through a `Derive` on the
   identical input.
6. **R6 (parent R24).** A derived switch does not claim a link property it has
   not observed. Acceptance example: the current switch has heard `1/1/3` as
   up, point-to-point, at one gigabit; the target changes that port's supported
   speeds so its resolved speed differs; the derived switch carries neither the
   old duplex nor the old speed for `1/1/3` and raises
   `IssueProtocolLinkUnknown` for it, and a following `LinkChange` clears the
   issue. A sibling port the target leaves alone keeps both.
7. **R7 (parent R24, R25).** The routing layer's neighbor table and hold queues
   are retained across `Derive` when the routing retention key is unchanged and
   dropped when it is not. Acceptance example: an unrelated VLAN edit keeps a
   `Reachable` neighbor and its held frame, so a following `Wake` releases the
   frame; changing that VRF's `NeighborPolicy.ResolutionTimeout` rebuilds the
   layer and the held frame is reported as failed rather than silently lost.
8. **R8 (parent R25).** Static state comes from the target construction
   specification. Acceptance example: `Derive` keeps a configured forwarding
   entry across a port rename elsewhere on the switch, and removes it when the
   target's `Seeds` no longer carry it.
9. **R9 (parent R37).** Every `Config` field in `src/common/netsim/vswitch/`
   and `src/common/netsim/fabric/` participates in its package's `Diff`, and the
   enumeration covers every package holding a `diff.go` in those trees.
   Acceptance example: adding a field to `stp.Config` with no arm in `stp.Diff`
   fails `TestDiffCoversEveryConfigField`, and adding a package with a `diff.go`
   and no entry in the covered list fails `TestEveryDiffPackageIsCovered`.
10. **R10 (parent R37).** Derivation is idempotent and forking is
    deterministic. Acceptance example: `Derive(Derive(cur, spec), spec)` and
    `Derive(cur, spec)` produce equal snapshots, retention reports and rendered
    diffs, over fifty constructions with randomised map insertion order; and a
    forked fabric run to quiescence produces the same journeys as its source run
    to quiescence from the same point.
11. **R11 (parent R40, forking third).** Forking has a declared scale and an
    enforced allocation contract. Acceptance example: at the declared scale
    `testing.AllocsPerRun(5, fork)` is at or under the recorded constant, and at
    four times the queue depth it is at or under the stated multiple of it.

## Out of scope

- Comparison of two forks and bounded counterexample search, which is parent U7.
  `fabric.Compare` refuses fabrics that have journeys
  (`src/common/netsim/fabric/compare.go:29`), so it cannot take a fork of a run;
  lifting that precondition is U7's, and U1 records the seam so the next phase
  does not have to rediscover it.
- Scenarios, replay, and honest run completion, which are parent U6. `Fork`
  copies no scenario state because none exists yet.
- Moving the runtime cable fault out of `fabric.Config`. `SetFault` mutating the
  configuration is why a fork cannot share it; separating the two is worth doing
  and is not this phase's subject.
- The owner axis and the `derived`, `assumed`, timer, queue and scenario-owned
  values of parent R23, per the narrowing above.
- Serialising a snapshot or a fork, persisting either, and any wire schema.
- Retaining state whose dependency key cannot be shown equal.

## Units

### U1. Amend the direction record with ownership, forking, and scale

Files: `docs/architecture/2026-09-10-virtual-device-direction.md`
After: none
Change: the decision gains a "State ownership, forking, and snapshots"
subsection. It states that a snapshot is an observational value, a fork is an
executable copy, and `Derive` is neither; that construction inputs are shared
and execution state is copied, with the three field classes named; that an
`ethernet.Frame` is an immutable value no layer writes into, which is why every
copy path inside the library shares one and only `Report()` copies; that a
retained record carries an origin and a lifetime as separate axes, with the two
values each takes today and the reason the record for a single-origin record is
left alone; that retention is keyed by a per-layer dependency key computed from
constructed switches rather than by the layer's own configuration diff; and that
a fork is the input a current-against-candidate comparison will take, while
`Compare`'s present "two fabrics that have not injected" precondition is the
next phase's to lift. The run-model paragraph at `:76-84`, which already says a
snapshot exposes the clock, the frames in flight and each device's state, is
extended to say what a snapshot does not expose and what a fork adds. The
"Remaining capability gaps" list gains the runtime cable fault living in the
configuration.
Tests: none; the record is prose and the behaviour it describes is tested in U2
through U6.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/architecture/2026-09-10-virtual-device-direction.md`

### U2. Separate origin from lifetime on retained records

Files: `src/common/netsim/vswitch/bridge/fdb.go`,
`src/common/netsim/vswitch/bridge/bridge.go`,
`src/common/netsim/vswitch/bridge/seed.go`,
`src/common/netsim/vswitch/mcast/layer.go`,
`src/common/netsim/vswitch/mcast/state.go`,
`src/common/netsim/vswitch/mcast/README.md`,
`src/common/netsim/vswitch/routing/neighbor.go`,
`src/common/netsim/vswitch/routing/fact.go`,
`src/common/netsim/vswitch/routing/README.md`,
`src/common/netsim/vswitch/switch.go`,
`src/common/netsim/vswitch/derive.go`,
`src/common/netsim/vswitch/netmodel/netmodel.go`,
`src/common/netsim/vswitch/netmodel/export.go`,
`src/common/netsim/fabric/diff.go`,
`src/common/netsim/internal/netsimtest/cases.go`,
`src/common/netsim/internal/netsimtest/stp_cases.go`, and the tests of each
After: none
Change: an `Origin` (`Configured`, `Observed`) and a `Lifetime` (`Static`,
`Aging`) enum in `bridge` and in `mcast`, replacing `Entry.Static`,
`Seed.Static` and `RouterPort.Static` at every read — `derive.go:128`, `:135`,
`:165`, `:173`, `:274`, `bridge/bridge.go`, `netmodel/export.go`, and
`fabric/diff.go:528`, which renders `static` into the diff string R10 compares
across derives. `routing`'s unexported `neighborOrigin` is renamed to the same
vocabulary and surfaced on the neighbor snapshot `fact.go` already builds.
`mcast.Layer` gains `InstallObserved(vid, port, group, expires)`, installing a
record that carries its own expiry; U5 is what switches `restoreMulticastState`
over to it and retires the synthesised query. Every reader that tested `Static`
now tests the axis it meant: aging tests `Lifetime`, provenance tests `Origin`.
Tests: `bridge/bridge_test.go`, beside its existing aging cases at `:567` and
`:2145`, gains one case per combination, asserting `Age` removes exactly the
`Aging` ones and that a `Configured, Aging` entry ages while an
`Observed, Static` one does not; `mcast/layer_test.go` asserts
`InstallObserved` produces a router port reporting `Observed, Aging` with the
expiry it was given, unchanged by the VLAN's `RouterPortInterval`, and that a
configured router port reports `Configured, Static`; `routing/neighbor_test.go`
asserts a configured neighbor reports `Configured, Static` and an observed one
`Observed, Aging`; `switch_test.go` asserts `Entries()` carries both axes;
`fabric/topology_state_test.go` asserts the same through `Snapshot().Devices`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/ src/common/netsim/fabric/ src/common/netsim/internal/netsimtest/`

### U3. Gate every configuration field against its Diff arm

Files: `src/common/netsim/internal/netsimtest/diffcoverage.go`,
`src/common/netsim/internal/netsimtest/diffcoverage_test.go`,
`src/common/netsim/internal/netsimtest/README.md`, one
`diff_coverage_test.go` in each of `bridge`, `lag`, `loopprotect`, `mcast`,
`phy`, `port`, `routing`, `stp`, `traffic`, `vswitch` and `fabric`,
`src/common/netsim/vswitch/traffic/diff.go`
After: none
Change: `netsimtest.AssertDiffCoversConfig` takes a seeded `Config`, a
normalization function, a `Diff` function and a per-package exemption list; it
walks the value by reflection including nested structs, map values and slice
elements, perturbs one leaf at a time to a value of its type, fails the leaf if
the perturbed value equals the original, normalizes both sides so a
constructor-filled default does not read as a change, and asserts `Diff` returns
at least one change whose `Subject` and `Field` name that leaf. An exemption
carries a reason, and an exemption naming a field that no longer exists fails.
`port` gets `AssertDiffCoversPort`, the same walk over the exported `port.Port`
with each perturbed table built through `port.NewBuilder`, since `port.Diff`
takes a `Table` whose fields are unexported. `netsimtest.AssertEveryDiffPackageIsCovered`
walks `src/common/netsim/vswitch/`, `src/common/netsim/vswitch/*/` and
`src/common/netsim/fabric/` for files named `diff.go` and fails on a package
path absent from a literal list in `netsimtest`. All eleven
`diff_coverage_test.go` files are `package <pkg>_test`, because `netsimtest`
imports `vswitch`, `fabric` and every capability package
(`internal/netsimtest/cases.go:15-26`) and an internal-test variant would cycle.
`traffic.Diff` calls `Config.Normalize` on both sides at the top and its three
hand-rolled selector normalizations in `diffMirror` go away; `lag.Diff` is left
as it is and keeps its doc comment's note that the caller normalizes first.
Tests: the eleven `diff_coverage_test.go` files are the gate, and every one
passes on the tree as it stands. In `diffcoverage_test.go`: a fixture config
whose `Diff` omits one field, asserting the gate names it; a fixture whose only
field is a single-valued type, asserting the gate fails rather than passing
vacuously; a fixture directory tree holding a `diff.go` with no entry in the
literal, asserting the enumeration fails. In `traffic`, a case passing
un-normalized configurations differing only in mirror selector order, asserting
`Diff` is empty. The enumeration cannot tell that a named file's test actually
calls the helper; nothing here covers a gutted `diff_coverage_test.go`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/internal/netsimtest/ src/common/netsim/vswitch/ src/common/netsim/fabric/`

### U4. Fork a switch, a bridge, and a fabric

Files: `src/common/netsim/vswitch/switch.go`,
`src/common/netsim/vswitch/fork_internal_test.go`,
`src/common/netsim/vswitch/fork_test.go`,
`src/common/netsim/vswitch/README.md`,
`src/common/netsim/vswitch/bridge/bridge.go`,
`src/common/netsim/vswitch/bridge/fork_internal_test.go`,
`src/common/netsim/fabric/fabric.go`, `src/common/netsim/fabric/run.go`,
`src/common/netsim/fabric/journey.go`,
`src/common/netsim/fabric/fork_internal_test.go`,
`src/common/netsim/fabric/fork_test.go`,
`src/common/netsim/fabric/README.md`,
`src/common/netsim/internal/netsimtest/cases.go`,
`src/common/netsim/internal/netsimtest/README.md`
After: U2, U3
Change: `bridge.Bridge` gains `func (b *Bridge) Clone() *Bridge`, which it has
no equivalent of today — it is the one capability runtime type with no clone
path, so `Derive` rebuilds it and reseeds. Every one of its thirteen fields
(`bridge.go:89-104`) carries a class: `cfg`, `ports`, `agingTime`, `fdbScope`,
`selectorScope` and `resolverScope` are `immutableShared`; `fdb`, `gates`,
`counters` and `dynamic` are `deepCopied` — `dynamic` included, because it is
what bounds `MaxEntries` without a table scan, so a clone that drops it changes
eviction on the fork while every forwarding probe still passes; `selector` and
`resolver` are `resetOnFork`, rebound by the switch after the clone.
`func (s *Switch) Fork() *Switch` and `func (f *Fabric) Fork() *Fabric` classify
every field of their own types the same way. `Fabric.Fork` rebuilds `byEnd` from
the copied `links` rather than copying the map, deep-copies `cfg`, and carries
the queue, the journeys, the egress queues, the counters, the clock and the
frame-id and sequence counters so the fork continues the same run; it copies a
journey by a shallow-frame rule of its own, leaving `Journey.clone` and
`Report()`'s deep copy as they are. `Fabric.Snapshot` gains the neighbor states
and hold-queue depths per device and keeps sharing the frames it reports.
`Switch.Fork` rebinds the bridge's gate — which is the `stp` or `loopprotect`
layer itself (`switch.go:314`, `:325`) — and its selector and resolver, which
hold the `Switch` (`:295`, `:273`), to the fork's own layers, since a copied
binding would have the fork ask the source for a LAG member or a multicast
resolution.
Tests: `fork_internal_test.go` in each of the three packages walks
`reflect.TypeOf` of the type, fails on a field with no class, and then checks
each class rather than recording it, in the shape of
`src/common/netsim/vswitch/stp/link_state_internal_test.go:138`: an
`immutableShared` field is pointer-identical across source and fork after
`Fork`; a `resetOnFork` field equals its type's zero value; a `deepCopied` field
has a probe in the package's `fork_test.go`, and a `deepCopied` field with no
probe fails. Each probe mutates through the exported surface on each side in
turn and asserts the other side is unchanged — both directions, because a probe
in one direction passes when the copy aliases in the other. `seeds` gets its own
probe: `Learn` on the fork, then the source's `Spec().Seeds` is unchanged. The
back-pointer probe removes a LAG member on the source and asserts the fork
selects from its own port table. A same-next-arrival test forks mid-run, runs
the fork to quiescence, then steps the source and asserts the arrival it
produces equals a never-forked twin's; a sibling test asserts the fork's
journeys from that point equal the twin's. A snapshot test takes a snapshot,
steps until the queue, the counters and one device's forwarding database have
all moved, and asserts every snapshot field unchanged. A phase-4b snapshot test
asserts `Snapshot().Devices` names a neighbor, its state and its hold depth. A
corpus case under `planning/` records that a candidate fork diverging does not
move the current answer. The class walk cannot see a pointer nested inside a
`deepCopied` map value, nor a field added to a nested struct; the probes are
what cover those.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/ src/common/netsim/fabric/ src/common/netsim/internal/netsimtest/`

### U5. Key retention by dependency and report it

Files: `src/common/netsim/vswitch/derive.go`,
`src/common/netsim/vswitch/retention.go`,
`src/common/netsim/vswitch/switch.go`,
`src/common/netsim/vswitch/derive_internal_test.go`,
`src/common/netsim/vswitch/switch_test.go`,
`src/common/netsim/vswitch/lag/layer.go`,
`src/common/netsim/vswitch/loopprotect/layer.go`,
`src/common/netsim/vswitch/mcast/layer.go`,
`src/common/netsim/vswitch/routing/layer.go`,
`src/common/netsim/vswitch/stp/layer.go`,
`src/common/netsim/vswitch/traffic/policer.go`,
`src/common/netsim/fabric/fabric.go`, `src/common/netsim/fabric/derive.go`,
`src/common/netsim/fabric/routing_test.go`,
`src/common/netsim/internal/netsimtest/diffcoverage.go`,
and each layer's README
After: U2, U3, U4
Change: each capability layer exports `RetentionKey(...) string`, a canonical
encoding of every normalized input its runtime state depends on: its own
configuration as `Diff` sees it, the `port.Port` entries it keys on with their
administrative and operational state, and the resolved speed where the layer
uses one. `lag.RetentionKey` takes the port table and the system id and
normalizes with them, so the switch's base MAC reaches the key. `vswitch.Derive`
computes both keys from constructed switches — never from the raw target spec,
which is the rule `derive.go:43`'s comment carries today and which moves into
`retention.go` with it — and retains only on equality, replacing `stp.Diff`,
`loopprotect.Diff` plus `loopProtectPortStatesEqual`, `lag.Diff` plus
`lagMemberStatesEqual`, and the `traffic.Policer` struct comparison. Spanning
tree gains the port-state dependency it lacks. `portP2P` and `portSpeed` carry
per port rather than wholesale, only where the target's port and resolved speed
match. The routing layer gains its first retention arm, keeping the neighbor
table and hold queues under an unchanged key and rebuilding otherwise.
`restoreMulticastState` calls `mcast.InstallObserved` from U2 and the
synthesised IGMP query from `192.0.2.1` goes away. `Derive` records a
`Retention` naming each layer kept or rebuilt and the differing dependency,
reachable through `(*Switch).Retention()` and `(*Fabric).Retention()`; `Fork`
sets every layer to kept. `netsimtest` gains
`AssertRetentionKeyCoversConfig`, the same reflection walk asserting a perturbed
leaf changes the key.
Tests: a dependency mutation matrix per layer, driven by the layer's
constructor inputs rather than by the key's own contents, so an input the key
omits produces a failing case rather than no case; one unrelated-input case per
layer asserting retention; `TestDerivedSwitchKeepsRolesWithAssignedBridgeAddress`
(`switch_test.go:2097`) kept green, which is what proves both keys still come
from constructed switches; R6's speed-change case asserting the derived switch
drops `1/1/3`'s duplex and speed, raises `IssueProtocolLinkUnknown`, clears it
on a `LinkChange`, and leaves a sibling port's carried values alone; a routing
case asserting a held frame survives an unrelated edit and releases on the next
`Wake`, and a second asserting a `NeighborPolicy` edit rebuilds and reports the
held frame as failed; the configured forwarding entry surviving a port rename
and vanishing when the target `Seeds` drop it; idempotence over two consecutive
derives comparing snapshots, retention reports and rendered diffs; and fifty
constructions with randomised map insertion order producing identical results.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/ src/common/netsim/fabric/ src/common/netsim/internal/netsimtest/`

### U6. Declare the representative scale and gate the fork's allocations

Files: `src/common/netsim/internal/netsimtest/scale.go`,
`src/common/netsim/internal/netsimtest/README.md`,
`src/common/netsim/fabric/fork_scale_test.go`,
`src/common/netsim/fabric/fork_bench_test.go`,
`src/common/netsim/fabric/README.md`
After: U4, U5
Change: `netsimtest.RepresentativeFabric` builds the declared topology — eight
nodes of thirty-two ports, sixteen VLANs, one two-member LAG per node, rapid
spanning tree, one VRF per node with sixty-four routes and sixty-four
neighbors, sixty-four hosts — and returns it primed with two thousand and
forty-eight learned forwarding entries and four thousand and ninety-six queued
arrivals. Its doc comment states each number and the lab measurement it comes
from. The `netsimtest` README says the package is not a benchmark
(`src/common/netsim/internal/netsimtest/README.md:7`); it gains a sentence
saying the fixture is a shared topology and the measurement lives in `fabric`,
so the boundary stays where it is. `fork_scale_test.go`, `package fabric_test`
because it imports `netsimtest` which imports `fabric`, gates
`testing.AllocsPerRun(5, ...)` over `Fabric.Fork` at that scale against a
constant recorded in the file with the date it was measured, and gates a second
case at four times the queue depth against a stated multiple of the first.
`fork_bench_test.go` reports wall-clock for the fork and for a hundred steps
after it, and gates nothing. The fabric README gains a "Representative scale and
fork cost" section stating the envelope, the allocation constant, and that
runtime is reported rather than gated because the suite runs under `-race`.
Tests: the two allocation cases above. A third asserts the fixture matches the
declared envelope — node, port, VLAN, route, neighbor, host, entry and queue
counts — so a later edit to the fixture that changes what the gate measures
fails rather than silently re-baselining it.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/internal/netsimtest/ src/common/netsim/fabric/`

Waves: U1 U2 U3 | U4 | U5 | U6

## Verification

```bash
go test -race ./src/common/netsim/...
go test -run 'TestDiffCoversEveryConfigField|TestEveryDiffPackageIsCovered' ./src/common/netsim/...
go test -run 'TestFork|TestRepresentative' ./src/common/netsim/fabric/ ./src/common/netsim/vswitch/
go test -bench BenchmarkFork -benchmem -run '^$' ./src/common/netsim/fabric/
go vet ./src/common/netsim/...
.claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>
```

The allocation constant in `fork_scale_test.go` is measured once on the
implementer's machine and recorded with its date; a later change that moves it
is a deliberate edit to that line, not a re-run.

## Definition of done

- [x] The verifier is green for every changed path.
- [x] Every field of `Switch`, `Bridge` and `Fabric` carries a fork class, and
      each class is asserted rather than recorded.
- [x] Every `Config` field in the two trees reaches its `Diff` and its retention
      key, or sits in an exemption list with a reason.
- [x] `Derive` retains only on an equal dependency key, computes both keys from
      constructed switches, and reports what it did.
- [x] Each touched package README and the direction record are updated in this
      change.
- [x] This plan's `status` is set with an outcome note under its title.
- [x] No plan labels appear in code, comments, or commit messages.

## Open questions

- Whether `mcast` needs a whole-layer key beside its per-entry `Retain`
  predicate. U5's matrix runs over the layer's constructor inputs, so an input
  that neither the key nor the predicate reads produces a failing case; the
  implementer adds it to the key. What is open is only whether any such input
  exists.
- The allocation constant itself, measured in U6. What is decided here is that a
  constant is recorded with its date and that the four-times case bounds the
  growth.
