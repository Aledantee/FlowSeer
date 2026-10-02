---
title: Link Aggregation and Physical Layer to Standard - Plan
type: fix
date: 2026-10-01
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-plan.md
---

# Link Aggregation and Physical Layer to Standard - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

`layer/lag` runs LACP as IEEE 802.1AX specifies, and `layer/phy` resolves
speed, duplex, and PoE inside the bounds its inputs state. Stop condition:
if 802.1AX and the layer's Open vSwitch-derived behaviour disagree on member
selection in a way the bucket table cannot express, the selection model is
redesigned first and the entries below are re-planned on it.

## Decisions

- The parent's Decisions apply. LACP follows IEEE 802.1AX. The virtual-device
  record's "as Open vSwitch runs it" is amended in this phase.
- A behaviour the README attributes to Open vSwitch is checked against the
  standard first. Where the standard turns out to be silent, the README
  names Open vSwitch as the model and cites the source file it was read
  from. Whether 802.1AX is silent on bond modes, the bucket table, and
  rebalancing is unverified.
- Each Correctness entry was read, not run. Entries 1 and 4 touch behaviour
  the README describes on purpose, so the failing test is written against
  the standard's clause, not against the README.

## Requirements

R1. Every Correctness entry has a test that fails before its fix or is
struck with the clause that makes the current behaviour right.

R2. The LACP Receive, Periodic, Mux, and Selection machines match 802.1AX.
Example: a partner that stops sending LACPDUs moves the member through
Expired to Defaulted on the standard's timers, with the actor state bits the
standard gives each step.

R3. A forced link and an observed link respect the cable's top speed and
report a duplex mismatch. Example: two ends observed at 10 Gb/s over a cable
whose top speed is 100 Mb/s do not resolve at 10 Gb/s.

## Inventory

Paths are as of commit `61775c73`. `L` is `src/common/netsim/vswitch/lag`
and `P` is `src/common/netsim/vswitch/phy`. Phase 1 moves them under
`src/common/sim/layer/`. No entry has a test in the tree.

### Correctness

1. High. Carrier loss under LACP disables the member at once and bypasses
   `DownDelay`. `setCarrier` (`L/layer.go:518-528`) sets `Defaulted`, and
   `updateLag` (`L/lacp.go:131,236`) then clears `attached` and `enabled`.
   `L/README.md:185` promises the delay.
2. High. `checkObserved` (`P/negotiate.go:226`) compares observed speeds
   only. It ignores the cable's top speed and a duplex conflict.
3. Medium. `inspectTCPHashInput` (`L/hash.go:62`) decodes the payload as IP
   without checking the EtherType. `L/README.md:163` says a non-IP EtherType
   stops hashing at layer 2. The test fixture hides it: `makeARPFrame`
   (`L/layer_test.go:83,261`) is all zeros and fails decoding by accident.
4. Medium. LACP fallback enables one member (`L/lacp.go:199`).
   `L/README.md:247` says it forwards over whichever members have carrier,
   and `MinLinks` above 1 then disables the LAG.
5. Medium. `Config.Allocate` reports `MaxNanowatts` for an unknown powered
   device without clamping to the group budget (`P/poe.go:351,404`).
6. Medium. `Negotiate` returns `SourceNegotiated` when both ends have
   auto-negotiation off (`P/negotiate.go:169,220`). `SourceSetting` exists
   for that case and has no producer. `P/negotiate_test.go:180-212` asserts
   the current value.
7. Low. With LACP off, `updateLag` skips `updateActorInfo`
   (`L/lacp.go:134`), so an enabled member never shows Collecting or
   Distributing. `L/README.md:200` says it does.
8. Low. `Ethernet.Resolve` treats an observation of speed 0 as observed
   (`P/ethernet.go:176`), against its own doc comment and `Negotiate`.
9. Low. A disabled PSE port with an unknown device reports `PowerUnknown`
   (`P/poe.go:339`), which raises `poe-demand-unknown` on the switch.
10. Low. `Config.Validate` rejects a fixed speed when supported speeds are
    unreported (`P/validate.go:73`), a case `Negotiate` supports.
11. Low. `activeBackupSelect` fills `Selection.Prior` (`L/layer.go:425`),
    against the field's doc comment (`:290`).

### Completeness

- `phy` has no README. Its sibling layers each have one.
- `GroupAllocation` exposes one remainder (`P/poe.go:268`) where the
  allocation computes a minimum and a maximum.
- The marker protocol, load-driven rebalancing, and members on two switches
  are listed as unmodelled in the virtual-device record. Under "to
  standard", decide each against 802.1AX at re-planning.

### Design

- `phy.Diff` reports `resolve_source`, a derived value, beside the settings
  that produce it (`P/diff.go:208`).
- `Normalize` defaults `Duplex` only when auto-negotiation is off
  (`P/phy.go:47`), so an unset duplex differs from an explicit `Unknown`.
- A member change is a subject of kind `port` whose key is not a port name
  (`L/diff.go:321`).
- The switch reports a LAG's speed to spanning tree as the fastest member
  (`src/common/netsim/vswitch/switch.go:3310`). Whether that or the sum is
  right for path cost is decided against 802.1D's path cost table.

### Tests

- `TestDelays` (`L/layer_test.go:331`) runs with LACP off only.
- `TestFallback` (`L/layer_test.go:502`) has one member.
- No `Negotiate` case has an observed speed above the cable's top speed.
- `TestPoeAllocateTruthTable` (`P/phy_test.go:793`) has no port whose
  maximum exceeds the group budget.

## Open questions

- Is IEEE 802.1AX obtainable to cite by clause? If only a summary is, the
  phase says "unverified" for each clause it could not read.
- Does retention report `Kept: true` for a layer that is absent on both
  sides (`src/common/netsim/vswitch/derive.go:150`)? Phase 9 decides.
