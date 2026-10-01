---
title: Fabric, Device Seam, and Cable - Plan
type: refactor
date: 2026-10-01
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-plan.md
---

# Fabric, Device Seam, and Cable - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

`fabric` schedules and records, and nothing else. It drives a switch and a
host through `sim/device`, asks `medium/cable` what a crossing costs, treats
every input as a queue event, and lets a held frame continue its own
journey. Stop condition: the parent's. If the device seam cannot carry what
`fabric` reads from a switch without importing a capability package, the
package shape record is revised before this phase writes code.

## Decisions

- The parent's Decisions apply.
- `sim/device` declares the interface, and `device/host` is extracted from
  `fabric` as its second implementation. Why: `fabric` builds a host's IP
  stack as a bare `routing.Layer` (`fabric.go:180,511-520`, `run.go:249`)
  and holds the host's acceptance and reflector rules in `accept.go`.
- The cable model moves to `sim/medium/cable` as a concrete type: length,
  medium, propagation, top speed, faults, and the two-ended negotiation
  `fabric` runs today through `phy.Negotiate` (`fabric.go:1220,1308`). No
  `Medium` interface is written.
- Every input is a queue event: a host transmit, a scenario action, and a
  stream's next frame. Why: a host injection charges egress state at call
  time with a caller-chosen instant (Correctness 2), an injection before
  `Start` moves the clock backward (Correctness 6), and a scenario refuses a
  fabric with an attached stream (`run.go:1508`).
- A held frame's release and its timeout continue the held frame's journey,
  using the token phase 6 puts on a held frame. `heldAggregates`,
  `OriginRelease`, and `JourneyReleased` go. Why: Correctness 1 and 4. The
  offered-load record's statement that a released frame carries no flow is
  amended here.
- The egress queue discipline moves to `layer/traffic` or stays in `fabric`
  with its steps authored there. Decided at re-planning from what phase 7
  left in `traffic`.
- The fabric's `Fork` was read field by field at `61775c73` and no aliasing
  defect was found. The event queue's order
  `(At, Kind, Seq, Device, Port)` (`queue.go:41-56`) is total. Neither is
  redesigned.
- This phase is the largest. Re-planning splits it if the device seam and
  the comparison work do not fit one session: the seam, host, and cable
  first, then the executor, comparison, and replay.
- Each Correctness entry was read, not run.

## Requirements

R1. Every Correctness entry has a test that fails before its fix.

R2. `go list -deps ./src/common/sim/fabric` names no package under
`sim/layer/`.

R3. A host injection takes effect at its own time. Example: on a stepped
fabric, injecting from one host at +10 s and then at +5 s transmits the
second frame first, and no `Entry.Wait` is negative.

R4. A release is attributed to the frame that was held. Example: frame 1
held for neighbor X and frame 2 for neighbor Y on one switch, Y resolves
first, and journey 2 is delivered while journey 1 is still held.

R5. `RunResult` reports the same status and issues for one run under
`RetainJourney` and under `RetainAggregate`.

R6. A run of plain `Run` computes no fingerprint. Example: the scale
benchmark the fabric README records at 295 seconds against a 120-second
target is rerun, and its figure is recorded in the README with the command.

## Inventory

Paths are as of commit `61775c73`. `F` is `src/common/netsim/fabric`, which
phase 1 moves to `src/common/sim/fabric`. `V` is
`src/common/netsim/vswitch`. No entry has a test in the tree.

### Correctness, execution core

1. Medium. A release is attributed to the lowest frame identifier held on
   the device (`F/run.go:1378-1416`). The payload match at `:1406` never
   holds for a routed frame, because routing decrements the hop limit before
   the hold (`V/routing/layer.go:866`).
2. Medium. A host injection charges egress state when it is called
   (`F/run.go:330-340,358-373,1031-1041,1081-1086`). An injection for an
   earlier time waits behind a later one, and one for a later time can
   transmit early, giving a negative `Entry.Wait` (`:1311`).
3. Medium. `RunResult.Status` and `Issues` read `Report()` only
   (`F/run.go:1786-1797`), and an aggregated journey is deleted at settle
   (`F/flow.go:92-98`). The offered-load record (`:90-92`) says the trust
   metadata survives aggregation.
4. Medium. A hold that times out stays `Pending` under `RetainJourney`
   (`F/journey.go:255-259`, `F/run.go:1436-1460,1684,1749-1756`), so
   `StopConverged` never fires. `F/flow_internal_test.go:726-728` pins it.
5. Medium. `Derive` starts layers only for spanning tree, loop protection,
   and LAG (`F/fabric.go:843-845`). A routing-only switch with a retained
   hold gets no wake, and `Derive` never drains neighbor failures, unlike
   `F/configure.go:103-111`.
6. Medium. `Inject` accepts a time before `Start` until the first step
   (`F/run.go:174-176,302-307`), and `Step` sets the clock without a
   monotonic guard (`:400`).
7. Medium. `RunScenario` on a fabric that has already run returns a replay
   built from `f.Spec()` (`F/run.go:1512-1524`), which holds no clock,
   queue, or prior injection. `F/README.md:950` promises an identical
   replay.
8. Low. The reflector accepts a multicast MAC of one IP family with a
   packet of the other (`F/accept.go:221-240`).
9. Low. A fault's sequence positions count every crossing since the run
   began, in both directions, protocol frames included
   (`F/run.go:1242-1260`). `F/README.md:829` does not say so.
10. Low. A stream that yields an unencodable frame or a non-monotonic
    offset stops the fabric for good with `scheduling-fault`
    (`F/attach.go:151-154`). On an unknown host link, `pullInputs` consumes
    the whole source in one call (`:104-164`).
11. Low. A lone jumbo frame on an empty queue raises
    `queue-buffer-unstated` (`F/fabric.go:1041`, `F/run.go:998-1005`).
12. Low. An action error mid-run returns an empty `RunResult` after earlier
    steps changed the fabric (`F/run.go:1642-1644`).
13. Low. `Record` validation lets `Frame` and `Bytes` disagree and lets
    `CapturedLen` exceed `OriginalLen` (`F/record.go:27-46`).

### Correctness, comparison and scenario

14. High. A recorded scenario frame is left out of the comparison.
    `F/compare.go:199` collects roots for `ActionInject` only, so
    `collectScenarioJourneys` (`:121`) selects none of an `ActionRecord`'s
    journeys and the result is `Equivalent` over zero journeys.
15. High. Aggregate retention hides a delivery difference.
    `F/compare.go:118` compares `Report()`, and a settled aggregate journey
    is gone, so both groups at `:309` are empty and equal.
16. High. The final-state comparison (`F/compare.go:685`) omits
    `Device.TreeRoles`, `Groups`, `RouterPorts`, and `Power`, ignores FDB
    origin and lifetime (`:744`) and a neighbor's VRF and hold depth
    (`:777`). Protocol emissions are excluded on purpose (`:328`), so
    nothing else observes that state.
17. High. A mid-run comparison's replay cannot reproduce it.
    `F/compare.go:75` records `a.Spec()` and `b.Spec()` while execution runs
    on forks. A cable that has already counted a crossing toward a
    `LoseEveryNth` rule drops in the comparison and delivers in the replay.
18. High. A cloned action shares storage. `F/scenario.go:216` copies an
    `Injection` shallowly, `:221` keeps the loss-sequence slice, and
    `Scenario.Clone` (`:544`) inherits both. The two replay sides share
    injection storage after `F/compare.go:94`.
19. High. The comparison's executor never pulls attached sources.
    `F/compare.go:189` lacks the input pull of the run loop and treats an
    empty queue as drained (`:214`). Two fabrics whose sources transmit from
    different hosts compare `Equivalent` after zero steps.
20. Medium. Spanning-tree counters cause a false difference.
    `F/compare.go:717` compares whole `stp.PortInfo` values, counters
    included. The fingerprint excludes them (`F/fingerprint.go:319`).
21. Medium. `DiffScenarios` compares time, origin, and encoded frame
    (`F/scenario.go:371`) and never `Injection.Packet`, `Retention`, or
    `Flow`.
22. Medium. A reflector attachment with a zero-value prefix passes
    `Config.Validate` (`F/config.go:1138`), which checks the slice's length.
23. Medium. An evidence-only edit shows as a configuration change.
    `F/diff.go:674,691,704,725` serialize the evidence catalog and its
    references into `construction_inputs`, against the exclusion stated at
    `:161`.
24. Medium. An action change can carry identical From and To facts.
    `F/scenario.go:387` summarizes an injection by origin and time and
    `:394` a fault by endpoints and kind, so a loss interval changed from 2
    to 3 reads the same on both sides.
25. Medium. A final-state difference reports counts and generic strings
    (`F/compare.go:699,706,713,720,737`), not the entry that differs.

### Completeness

- `F/README.md:1159-1167,1201` promises equivalence over every scenario
  journey and final behavioural snapshot, mid-run inputs, and a replay of
  both sides. Entries 14 to 19 are where the code falls short.
- `F/README.md:1167` and the tree README define `Inconclusive` as matching
  observables on an incomplete run. `F/compare.go:133` returns it whenever a
  side is incomplete, with a difference filled, which agrees with the
  virtual-device record (`:413`). The READMEs are corrected.
- `F/README.md:645` says snapshot neighbors include origin and lifetime.
  `routing.NeighborEntry` has neither.
- `PendingWork` (`F/result.go:40`) counts arrivals, wakes, egress, and
  journeys, and not attached sources or deferred host injections.
- `F/README.md:666-668` describes an `Arrival.Wake` field that does not
  exist. Wakes sort by `Kind` (`F/queue.go:45`).
- The virtual-device record (`:248-252`) says every copy path shares the
  frame. The code clones at `F/run.go:259,284,636,1008,1323,1336`,
  `F/journey.go:559`, and `F/fabric.go:641,645`.
- `F/README.md:254-255` says a host injection uses the outer tag's PCP.
  `F/run.go:264-268` replaces the tags, so a host always sends at PCP 0.
- The admin-unknown rule is missing from `F/README.md:702-717,855-885`, and
  its reason is a literal (`F/fabric.go:1198-1200`).
- The local-network record (`:72-73`) and `F/run.go:769` name a `Parent`
  field. The copy carries `Origin{Kind: OriginMirror, Of}`.
- The virtual-device record (`:215-217`) keeps a no-injection precondition
  on `Compare` that `F/README.md:1201` says is lifted.
- Protocol journeys and `entered[fid]` are never freed (`F/flow.go:97`), so
  memory grows with simulated time on a spanning-tree fabric.
- A host-bound leg records no crossing (`F/run.go:1285-1301`), so wait,
  serialization, and PCP at the access port are absent.

### Design

- Where `fabric` reaches past the switch today: STP and loop-protection
  configuration to decide a start (`F/fabric.go:843`), admin point-to-point
  derived from `stp` configuration in two places (`F/fabric.go:1478-1526`)
  and a third in the switch (`V/switch.go:2960-2964`), `Phy.Ethernet`
  (`F/fabric.go:1380-1384`), policers and a hand-built `bridge.Result`
  (`F/run.go:525-558`), traffic drop and threshold steps
  (`F/run.go:972-1005`, `F/fabric.go:1056-1077`), the VID for member
  selection (`F/run.go:890-916`), shaping (`F/run.go:1117`), a `Device`
  value assembled from six accessors (`F/run.go:1870-1892`), a hand clone
  of `ForwardResult` (`F/journey.go:547-563`), and `routing.AddrFact`
  (`F/accept.go:237,343`).
- One scenario executor. `F/compare.go:167` repeats the scheduling,
  convergence, and result logic of `F/run.go:1606` and has already lost the
  source pull and the recorded-frame roots. The engine reports scenario
  roots and comparison observations through one path.
- One behavioural projection of a snapshot. `F/compare.go:685` and
  `F/fingerprint.go:44` choose different fields. Exact comparison and
  convergence stay separate operations over one field classification.
- A runtime checkpoint is not a construction spec. Replay of a continued
  run needs an owned checkpoint or the full input history. This changes the
  replay contract, `Replay`, and `search`.
- `Scenario` carries a topology and a budget (`F/scenario.go:416`) that
  `Compare` normalizes (`:495`) and then discards (`F/compare.go:96`), so a
  malformed unused topology rejects a valid comparison. A scenario becomes
  an action schedule, with limits in one options value.
- An action is matched by its index (`F/scenario.go:265,448,614`), which
  may repeat across timestamps. Inserting one action reports every later
  one as edited. An action gets an identity apart from its order.
- `HostRoutingConfig` (`F/config.go:270`) is exported with no caller outside
  the package and ignores a builder error.
- `config.go` (1,317 lines) splits at host, reflector, cable, and topology
  (`F/config.go:171,341,440,537`). `diff.go` (1,141) and `compare.go` (854)
  split into executor, provenance pairing, and observable comparison.
- Hot path: `sw.Ports()` clones the port table per arrival and per egress
  (`F/run.go:490,889`), and `Frame.Encode` runs four or more times per hop
  to learn a length (`F/run.go:496,524,919,970`).
- Run loop: `Fingerprint()` runs every step and the list grows without
  bound at window 0 (`F/run.go:1665-1666`), and `Report()` deep-clones every
  journey to answer whether one is pending (`F/run.go:1749-1756`).
- Aggregate retention builds the whole journey and then discards it
  (`F/run.go:572-579`, `F/flow.go:160-178`).
- Duplicate state: `wakes` with `wakeItems`, and the dequeue fields on
  `egressQueue` with `dequeueItems` (`F/fabric.go:187-189`,
  `F/queue.go:140-151`). One queue type owns the slice and its indexes.
- `initRunState` (`F/run.go:1938-1970`), `initQueueIndexes`
  (`F/queue.go:125-135`), and about twenty nil guards in `Fork` exist for
  tests that build `&Fabric{}` by hand.
- Names: the `Retention` type against `Fabric.Retention()`, which returns
  the switch's (`F/run.go:40`, `F/fabric.go:1108`). `Node` against `Device`.
  `EntryWake` and `EntryDequeue`, which are step results. `OriginMirror`
  for both a mirror and a reflection. `Journey.State` as a stored
  truncation marker (`F/run.go:1575`). The zero `ArrivalKind` is a wake.
  `Link` embeds `Cable`.
- `Fabric.Clock()` is missing. Fifty-six call sites read
  `Snapshot().Clock`.
- `Run` has no convergence window. `run(budget, window)` is unexported
  (`F/run.go:1494-1496`).
- `Fabric.Switch()` returns the live switch (`F/fabric.go:1096`), so a
  caller can drain what the fabric owns.
- `run.go` (1,970 lines, `Step` about 280) and `fabric.go` (1,526) split
  into spec, build, fork, link resolution, metadata, egress, step, hold and
  emission, run loop, and snapshot.

### Tests

- Fork probes that cannot fail: they write a value field or append, so a
  shared backing array passes (`F/fork_test.go:66-79,97-110,150-157,
  177-190,227-241,288-301,352-366`).
  `docs/solutions/conventions/a-perturbation-gate-that-copies-structs-by-field-passes-vacuously.md`
  states the rule.
- `F/fork_internal_test.go:50-81` classes `clock`, `stepped`,
  `nextFrameID`, `nextSeq`, `runtimeEvidence`, and `err` as immutable
  although a run writes them, and the table is extended from another file's
  `init` (`F/attach_internal_test.go:18-23`).
- No test resolves holds out of frame order, injects from a host into an
  empty queue or after a step, injects before `Start` before the first
  step, compares `RunResult.Status` across retentions, or runs a derived
  fabric with a retained hold and no traffic.
- `F/fingerprint_test.go:913-955` runs its hello-wake case on an unknown
  link, so no BPDU is sent and the run results are ignored.
- The before-start refusal case (`F/scenario_test.go:192,286`) fails on an
  inner timestamp mismatch first, so it passes with the guard at
  `F/scenario.go:437` removed.
- `F/scenario_test.go:803` mutates only time and origin of a clone, and
  `F/compare_test.go:1807` ignores `Comparison.Err`.
- `F/compare_test.go:1183,1230` changes an FDB count and asserts the
  observable's name only. `F/compare_test.go:1945` replays only from a
  freshly built fabric.
- `F/compare_test.go` (2,201 lines) also holds `Derive` tests (`:310`) and
  `Diff` tests (`:475`).
- `TestStopReasonTableNoEmptyConstant` (`F/result_test.go:31-54`) asserts
  properties of a list it just wrote.
- Routed two-port fixtures are rebuilt inline in six places
  (`F/flow_internal_test.go:410-463,523-579`, `F/flow_test.go:212-268`,
  `F/routing_test.go:935-992,1457-1496,1583-1622`).

## Open questions

- Can the switch's `NextWake` return a time before now? `Step` would move
  the clock backward.
- Can `Forward` return an outcome with no egress that is not a drop, a hold,
  or a consume? Such a journey reads `Pending` for good (`F/journey.go:312`).
- Is a monotonic offset part of the stream source's contract?
- How does a replay represent a continued run: a checkpoint, the full input
  history, or a stated restriction to fresh fabrics until one exists?
- `Record` promises a deep copy and shares the frame pointer
  (`F/record.go:78-80`). Does the immutable-frame rule cover a caller's
  record?
- `relinkAt` can return an error after `Configure` has installed the links
  and the derived switch (`F/configure.go:83-96`). Can it fail for a
  configuration that passed validation?
- The snapshot samples VLAN 1 for the common tree, which loses the CIST when
  VLAN 1 is in an MST instance (`V/switch.go:3431`). Does the device state
  of phase 9 name the CIST directly?
- `Config.Validate` and `normalizeConstructionSpec` (`F/fabric.go:316-345`)
  each validate part of a spec. Which one is the contract?
