---
title: Multicast Snooping and Loop Protection - Plan
type: fix
date: 2026-10-01
artifact_contract: flowseer-plan/v2
execution: code
---

# Multicast Snooping and Loop Protection - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

`layer/mcast` gives the same answer whether or not the caller aged it first,
keeps IGMPv3 and MLDv2 source filters through a derive, and follows RFC 3376
and RFC 3810 for the timers it keeps. `layer/loopprotect` arms its timers on
every path and accepts only its own frames. Stop condition: if a retained
multicast layer cannot be rebased onto a new configuration without replaying
reports, retention falls back to a rebuild that the switch reports as not
kept.

## Decisions

- The parent's Decisions apply.
- `mcast` owns the restore of its retained state. Why: the switch replays
  each entry as an IGMPv2 or MLDv1 report
  (`src/common/netsim/vswitch/derive.go:287-315`), which discards
  `Entry.Mode` and `Entry.Sources`.
- Snooping follows RFC 4541 for what a switch does with reports and queries,
  and RFC 3376 and RFC 3810 for the timer values. Each is fetched and cited
  by section at re-planning.
- Loop protection is the simulator's own protocol, so its README is its
  specification. A behaviour that differs from the README is a defect in
  whichever is wrong, decided per entry.

## Requirements

R1. Every Correctness entry has a test that fails before its fix.

R2. `Resolve` at a time past an entry's expiry answers as if `Age` had run.
Example: a learned router port that expired 40 seconds ago is absent from
`Resolve`'s ports, and a group whose membership expired resolves as
unregistered.

R3. A derive keeps a source-specific membership. Example: `INCLUDE{10.0.0.9}`
on one port survives a derive that edits an unrelated port, and traffic from
another source is still not delivered to it.

R4. A frame is a loop-protection probe only when both its destination and
its EtherType say so.

## Inventory

Paths are as of commit `61775c73`. `M` is `src/common/netsim/vswitch/mcast`
and `LP` is `src/common/netsim/vswitch/loopprotect`. Phase 1 moves them
under `src/common/sim/layer/`. No entry has a test in the tree.

### Correctness

1. High. `Resolve` admits an expired learned router port and sets
   `hasRouter` when `Age` has not run (`M/layer.go:308`).
2. High. `Resolve` returns `registered` for a group whose membership has
   expired when `Age` has not run (`M/layer.go:318`). The frame is dropped
   where it should flood.
3. High. `Receive` sets a recovery deadline without arming the layer
   (`LP/layer.go:226`), so `NextWake` hides the timer until `LinkChange` or
   `Wake` runs. `LP/README.md:152-156` promises `Receive` arms it.
4. Medium. `clearIfMatched` clears `firedAt` only when the query arrives
   within the last-member query time (`M/state.go:342`). A late query leaves
   the obligation pending for good.
5. Medium. `Decode` reads the payload and ignores EtherType and destination
   (`LP/probe.go:75`), and the switch matches the destination alone
   (`src/common/netsim/vswitch/switch.go:1154`).
6. Medium. `Config.Validate` does not check `Port.VLANs`
   (`LP/config.go:152`). VLAN 0, 4095, and 5000 pass.
7. Low. `Encode` writes a port name's length into one octet without a bound
   (`LP/probe.go:61`).
8. Low. `Clear` leaves `interVLAN` set (`LP/layer.go:407`).
9. Low. `NextWake` keeps returning the probe time when every port is
   disabled and recovery is manual (`LP/layer.go:357`).
10. Low. A group-specific query does not clear a pending
    group-and-source obligation (`M/state.go:324`).

### Completeness

- `mcast.Layer` has no restore for retained entries. The virtual-device
  record's retention rule (`docs/architecture/2026-09-10-virtual-device-direction.md:262-277`)
  assumes every stateful layer is kept by key. `mcast.RetentionKey`
  (`M/layer.go:501`) has no caller, and the switch reports `Mcast` kept
  unconditionally (`src/common/netsim/vswitch/derive.go:169-173`).
- A derive that rebuilds spanning tree drops every membership on an STP
  port, because the new layer starts Discarding
  (`src/common/netsim/vswitch/derive.go:190`), and still reports `Mcast`
  kept.
- `M/README.md:91-95` defines `registered` by router state, and the code
  keeps it true after all state expired.

### Design

- `mcast.Layer` clones a whole port table to test `LagParent == ""`
  (`M/layer.go:123,154,400`).
- `Origin` and `Lifetime` use the empty string for `Configured` and `Aging`
  (`M/layer.go:55,67`), so the zero `RouterPort` reads as configured and
  aging at once.

### Tests

- `TestBehaviorMatrix` (`M/config_test.go:274-289`) asserts the length of a
  list it just wrote.
- No test resolves past an expiry without calling `Age` first.
- No test delivers a query after the last-member query time.
- `TestInterVLAN` never sets `SameUntaggedDomain`.

## Open questions

- What does `GroupExpires` hold for an `INCLUDE` entry? The switch subtracts
  an interval from it (`src/common/netsim/vswitch/derive.go:294`).
- Should a mirror destination or a loop-protected port emit protocol frames?
  Phase 9 decides at `src/common/netsim/vswitch/switch.go:3012-3030`.
