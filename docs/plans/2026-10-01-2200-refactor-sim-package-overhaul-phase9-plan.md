---
title: Switch Composition - Plan
type: refactor
date: 2026-10-01
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-plan.md
---

# Switch Composition - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

`device/vswitch` receives a frame through one entry that returns the result
together with everything the frame caused, drives its layers through the
capability contract, and forks and derives every capability it is configured
with. Stop condition: if one receive entry cannot serve both the mutating
path and the non-mutating `Peek` that `Compare` uses, the two stay separate
entries over one shared pipeline, and the plan says so.

## Decisions

- The parent's Decisions apply.
- One entry, `Receive(now, port, frame)`, returns the forward result, the
  emissions, the mirror copies, the neighbor drops, and the next wake.
  Why: the fabric repeats the sequence police, age, forward, drain, drain
  failures, copies, next wake at five sites and drains failures at two of
  them (`src/common/netsim/fabric/run.go`), and `Copies` is a field
  overwritten per call (`switch.go:552-557`).
- Per-call scratch state leaves the `Switch` struct for a call context.
  Why: the hit sets at `switch.go:244-255` are reset only in `forward`
  (`:1092-1095`), which is Correctness 3.
- Routed egress has one implementation for a live frame and a released one.
  Why: `switch.go:2056-2200` and `:3220-3286` already differ on filters,
  member choice, and mirrors.
- `Fork`, `Advance`, and `NextWake` loop over the layers the device holds.
  Why: `Fork` is a hand-written literal that omits `filter`.
- `LinkChange` returns an error and `operErr`, `Err`, and `SetOperStatus`'s
  exported form go. Why: the sticky error exists because `LinkChange`
  returns nothing (`switch.go:3843-3869`), and no production code reads it.
- Entry 1 was confirmed by reading `Fork` at `61775c73`: `filter` is set
  only in `newSwitch` (`switch.go:411`), and no fork test configures a
  filter. The other entries were read, not run.

## Requirements

R1. Every Correctness entry has a test that fails before its fix.

R2. A fork behaves as its source for every capability. Example: the fork
gate builds a switch with every capability configured and fails when a
classified field is zero on the source.

R3. A filter decides a routed frame whether it is forwarded at once or held
first. Example: an outbound `Default: Drop` set on `vlan20` drops a frame
released after its neighbor resolves, as it drops one whose neighbor was
static.

R4. A derive reports what it kept truthfully. Example: a derive that
rebuilds spanning tree either keeps the multicast memberships on STP ports
or reports `Mcast` as not kept.

R5. No non-test file in the package exceeds the parent's size limit.
Example: `switch.go`, 3,957 lines, is the files listed under Design.

## Inventory

Paths are as of commit `61775c73`. `V` is `src/common/netsim/vswitch`,
which phase 1 moves to `src/common/sim/device/vswitch`. No entry has a test
in the tree.

### Correctness

1. High. `Fork` drops the packet filter (`V/switch.go:462-533`). A forked
   switch skips every `s.filter != nil` guard (`:1290,1354,2001,2028`).
   `fabric.Fork` and `fabric.Compare` fork every switch, so a filter change
   compares `Equivalent`.
2. High. A held frame bypasses the egress filter and a deferred ingress
   decision. `V/switch.go:1962-1996` returns `Held` before
   `ResolveDeferred` (`:2001`) and `EvaluateEgress` (`:2028`), and
   `releaseHeldFrame` (`:3220-3286`) evaluates no filter.
3. Medium. Per-call hit sets leak into the next result.
   `ComposeForwardResult` (`V/switch.go:756-762`) appends the four hit-set
   issues (`:823-826`), which only `forward` resets. A policed drop inherits
   the previous frame's issues.
4. Medium. A derive turns every retained membership into any-source
   (`V/derive.go:293-305`).
5. Medium. A derive that rebuilds spanning tree drops memberships on STP
   ports and reports `Mcast` kept (`V/derive.go:169-173,190`).
6. Medium. `protocol-link-unknown` is raised for ports spanning tree does
   not track. `V/switch.go:3923-3956` walks every non-member port, and the
   layer tracks `cfg.Ports` only. `V/protocol_link_test.go:385-405` pins it.
7. Medium. `Forget` leaves the seed in the spec (`V/switch.go:663-669`), so
   a rebuild from `Spec()` reinstalls it and a later `Learn` of the same key
   fails as a duplicate (`:647-648`).
8. Medium. A derive that changes only the node identity keeps a routing
   layer bound to the old one (`V/derive.go:203-207`).
9. Low. A BPDU on a port the layer ignores is traced as consumed and
   admitted (`V/switch.go:2657-2678`). The SSTP path reports `port-down`.
10. Low. A derive can leave `portSpeed` nil beside a non-nil `portP2P`
    (`V/derive.go:91-100`), and `updateLagState` then writes to a nil map
    (`V/switch.go:3321-3327`).
11. Low. A derive drops undrained emissions, copies, neighbor failures, and
    the oper error (`V/derive.go:33-37`).
12. Low. `SetOperStatus` on an unknown port returns nil
    (`V/switch.go:3815-3841`), and `LinkChange` on one stores entries for it
    (`:3659-3664`).
13. Low. `Compare` cannot see a mirror copy's content. It compares decision
    facts (`V/compare.go:198-227`), which carry no VLAN or frame, so an
    output VLAN changed from 98 to 99 is `Equivalent`.

### Completeness

- The virtual-device record promises a per-class assertion for every
  `Switch` field. `filter` is asserted as nil equals nil
  (`V/fork_internal_test.go:56`, fixture `:197-262`).
- `V/README.md:320-370` omits `Age` from the protocol schedule, and
  `:334-338` says `SetOperStatus` is followed by `LinkChange`, which already
  rewrites the status.
- `V/README.md:486` promises exact reproducibility from `Spec`. `Spec` and
  `Config` return construction ports and `Ports` returns runtime ports.
- `V/README.md:580-586` describes retention by key. `Mcast` is always kept
  and `Traffic` reports not kept while unchanged buckets are kept
  (`V/derive.go:47-60`).
- `V/README.md:613-630` defines `Inconclusive` as "observables matched". The
  code returns it whenever a side is not complete, with a difference filled
  (`V/compare.go:48-55`). The outcome list omits `Flooded` and `Held`.
- Stale doc comments: `Wake` and `NextWake` (`V/switch.go:3496-3498,3518`),
  `Age` (`:2218`), `Drain` (`:3354`), `Diff` (`V/diff.go:38-42`), the
  package comment (`V/config.go:1`).
- `neighborObservationAllowed` (`V/switch.go:954-974`) is unreachable by its
  own comment.
- An MSTI-only transition on a BPDU is invisible, because the switch records
  CIST before and after only (`V/switch.go:2673`).

### Design

- Three output shapes with three drains (`Emission`, `traffic.Copy`,
  `NeighborDrop`) become one outbox.
- Policing is split: the switch owns the buckets, the fabric builds the
  drop, and the switch wraps it. `Receive` polices.
- About twenty one-line accessors return capability types, and the fabric
  assembles a snapshot from eight of them. One `State()` value replaces
  them.
- Issue codes are five constants (`V/switch.go:3597-3621`) and three
  literals (`:723,774,812`), one repeated in `fabric`.
- Point-to-point has three representations: a string enum
  (`V/switch.go:3580-3592`), `stp`'s enum, and a bool.
- Retention keys are strings parsed by line prefix (`V/retention.go:39-74`).
- `switch.go` splits by subject: spec and construction, fork, issues,
  forward, hub, route, multicast, mirror, protocol intercepts and timers,
  inspection. `forward` (300 lines), `assembleRouteResult` (255 lines, ten
  parameters, one step-assembly block repeated five times), and
  `interceptSSTP` (207 lines) are decomposed.
- `V/README.md:372-476` is spanning tree, loop protection, LACP, and PoE
  detail, and `:262-280` is loader defaults. Each moves to its package.

### Tests

- The fork gate passes for any field that is zero on the source.
- `TestDiffCoversEveryConfigField` seeds no filter
  (`V/diff_coverage_test.go:42-96`), and the helper returns on a nil pointer
  (`src/common/netsim/internal/netsimtest/diffcoverage.go:183-186`).
- `Compare` has no test for the observables `reason`, `fid`, `egress.port`,
  `egress.dropped`, `frame.src`, `frame.ethertype`, `frame.tags`, and
  `frame.payload`.
- Assertions that accept `Forwarded || Flooded`
  (`V/switch_test.go:4760,5054,5217,5302,6684`).
- `TestConformanceDeriveInvalidation` (`V/conformance_test.go:189-246`)
  never calls `Derive`.
- `switch_test.go` (9,741 lines) splits by the same subjects as the source.
  One two-VLAN routed configuration literal is repeated fourteen times.
- Planning identifiers and history in comments (`V/compare_test.go:20`,
  `V/switch_test.go:8418,8809,8845,8910,6506,6527,6612,6716,7461,7884,8732`).
- `time.Now()` in `V/construction_result_test.go:466,541`.

## Open questions

- Does a filter require routing (`V/config.go:375`)? Phase 6 answers for the
  layer and this phase for the switch.
- Should a mirror destination emit protocol frames
  (`V/switch.go:3012-3030,3130-3133`)?
- `Ingress` runs twice for a control candidate (`V/switch.go:1321,1548`).
  Is anything but learning repeated?
