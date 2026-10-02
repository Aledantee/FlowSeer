---
title: Relay, Port Table, and Traffic - Plan
type: fix
date: 2026-10-01
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-plan.md
---

# Relay, Port Table, and Traffic - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

`layer/bridge` keeps a static entry authoritative, reports the same drop the
same way on every path, and validates in a fixed order. `layer/traffic`
mirrors a stacked-tag frame intact and states where a queue or policer may
be configured. Stop condition: if 802.1Q's relay rules contradict the
ingress and egress split the virtual-device record takes from bmv2, the
record is amended before the relay changes.

## Decisions

- The parent's Decisions apply. The relay follows IEEE 802.1Q as the
  virtual-device record states, read from the standard and from the
  Q-BRIDGE-MIB and BRIDGE-MIB under `spec/mib/ietf/`.
- Entry 1 was confirmed by reading `Bridge.Learn` at `61775c73`: the branch
  `!exists || existing.Lifetime == Static` replaces a static entry with an
  aging seed and counts it as learned. The other entries were read, not run.
- `bridge.go` (1,585 lines) splits into forwarding, learning, and
  replication files. `Ingress` (417 lines), `Egress` (264), `replicate`
  (195), and `Diff` (325) are decomposed in the same phase.

## Requirements

R1. Every Correctness entry has a test that fails before its fix.

R2. A static entry survives any aging seed for its key. Example: learn
`(FID 10, MAC A)` static on `1/1/1`, apply an aging seed for the same key on
`1/1/2`, age past the aging time, and the entry is still static on `1/1/1`.

R3. A drop on a down egress port has one layer, one rule identifier, and an
`Egress` record on both the unicast and the replication path.

R4. `Config.Validate` reports the same field for the same input on every
run. Example: a VLAN table holding both 0 and 5000 names the lower one.

## Inventory

Paths are as of commit `61775c73`. `B` is
`src/common/netsim/vswitch/bridge`, `PT` is
`src/common/netsim/vswitch/port`, `T` is
`src/common/netsim/vswitch/traffic`. Phase 1 moves `PT` to
`src/common/sim/port` and the others under `src/common/sim/layer/`. No entry
has a test in the tree.

### Correctness

1. High. An aging seed overwrites a static entry (`B/bridge.go:322`).
   `src/common/netsim/vswitch/README.md:490-492` says a static seed stays
   authoritative.
2. Medium. An unknown ingress port loses its name: `res.Ingress` takes the
   resolved name, which is empty (`B/bridge.go:505`). The switch patches it
   back (`src/common/netsim/vswitch/switch.go:1821-1824`).
3. Medium. `Config.Validate` ranges over the VLAN table map
   (`B/config.go:384`), so the reported field varies between runs.
4. Medium. `vlanCopyFrame` strips the outer tag before checking its TPID
   (`T/mirror.go:94`). An 802.1ad frame loses its S-tag and its priority.
5. Medium. `NormalizeSeeds` admits a seed on a port whose PVID is the FID
   although the port does not carry the VID on egress (`B/seed.go:109`,
   `B/config.go:110-115`). Traffic to that address is then dropped as not a
   member.
6. Low. A down egress port is `LayerRelay` with rule `port-down` and an
   `Egress` record on the unicast path (`B/bridge.go:1036`), and `LayerPort`
   with rule `port.status.down` and no record on the replication path
   (`:1238`).
7. Low. The `ReasonMTUExceeded` fallback in `replicate` is unreachable
   (`B/bridge.go:1394`).
8. Low. A `SelectAll` mirror with a direct output port copies a frame back
   out of the port it arrived on (`T/mirror.go:32`). The output-VLAN form
   guards against this (`:43`).

### Completeness

- `bridge` and `port` have no README. Their contracts are in the switch's.
- `Switchport.CarriesVID` names an ingress counterpart,
  `VLAN.AdmitsVIDOnIngress`, that does not exist (`B/config.go:108-115`).
  Ingress admission is inline in `B/bridge.go:616-670`.
- `PortQueues` declares a rate and a buffer per priority
  (`T/config.go:55-65`), and the scheduler that uses them lives in `fabric`.
  Phase 10 moves the queue discipline here or states why it stays.
- The offered-load record says a buffer stated on a LAG applies to each
  member's queue. `traffic.Config.Validate` accepts queues and policers on a
  member directly (`T/config.go:280-287`) and states no precedence.
- `traffic.Copies` returns copies without the scopes it consulted, and the
  switch rebuilds them (`src/common/netsim/vswitch/switch.go:1841-1848`).

### Design

- The VLAN membership predicate is written four times outside `bridge` with
  different rules (`src/common/netsim/vswitch/config.go:262-263`,
  `derive.go:185-186,252-254`, `switch.go:3127`). `bridge` exports one.
- The port table has an owner in the switch, in the bridge, and in each
  layer, and `SetOperStatus` rebuilds two of them through the builder
  (`src/common/netsim/vswitch/switch.go:3820-3837`, `B/bridge.go:271-282`).
  `port.Table` gains a method that returns a table with one status changed.
- `Bridge.Clone` copies gates that still point at the source's layers
  (`B/bridge.go:153-155`), and the README says bindings reset.
- `PriorityTagPolicy` uses the empty string for its default
  (`B/config.go:25-39`).

### Tests

- `TestLayerConstants` (`PT/port_test.go:454-472`) checks six of twelve
  constants against their own literals.
- `B/bridge_test.go` is 3,369 lines mixing validation, forwarding, the gate,
  multicast, diff, and table bounds. Split it by those subjects.

## Open questions

- Is a policer keyed by a LAG name valid, and does the fabric police by the
  member or the aggregate (`src/common/netsim/fabric/run.go:525`)?
- Can `QueueMaxRate` return a zero rate as stated? The fabric divides by it
  (`src/common/netsim/fabric/run.go:142`).
