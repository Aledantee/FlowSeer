---
title: Capability Contract and Surface Trim - Plan
type: refactor
date: 2026-10-01
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-plan.md
---

# Capability Contract and Surface Trim - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

Every package under `sim/layer/` has the one shape the
[package shape record](../architecture/2026-10-01-simulation-package-shape-direction.md)
states, the shared types live in `sim/layer`, and the exported surface holds
only what a caller outside the package uses. Stop condition: if folding
`Age` into `Advance` changes when an entry ages relative to an arrival at the
same instant, the two verbs stay and the record is amended.

## Decisions

- The parent's Decisions apply.
- The contract is the record's "One capability contract" list. This phase
  changes shapes and moves code. It fixes no behaviour except where a shape
  change forces it, and each such case is named in the unit.
- Rule identifiers and layer names become constants in the package that
  produces them, named `LayerName` as `filter` already does. `port.Layer*`
  shrinks to `port.LayerName`. Why: the
  virtual-device record says rule identifiers are producer-owned, and
  `vswitch/compare.go` matches the literal `"traffic.mirror_decision"`.
- Fact wrapper types are unexported and tests assert on `TypeID` and
  `Canonical`. Why: `BoolFact` is declared in seven packages, `DurationFact`
  in five, `MACFact` in four, and none has a caller outside tests.
- The BPDU and SSTP codecs move to `src/common/net/bpdu`. Why: the record
  places wire codecs there, and `lag` already uses `net/lacp`.
- `Difference` and the metadata clone, equal, and merge helpers move into
  `analysis`. Why: `vswitch/compare.go` and `fabric/compare.go` declare the
  same struct, and both packages carry their own metadata helpers.

## Requirements

R1. Each layer package has `const LayerName`, `Config.Normalize(layer.Env)`,
`Config.Validate(layer.Env)`, `Config.Clone`, `Diff`, and, when it holds
runtime state, `New(cfg, layer.Env)`, `(*Layer).Clone`, and
`RetentionKey(cfg, layer.Env)`. Example: `filter.New` no longer takes a port
table it ignores, and `lag.Diff` normalizes its inputs like every other
`Diff`.

R2. No package under `sim/layer/` imports a sibling. Example: `go list -f
'{{.Imports}}' ./src/common/sim/layer/traffic` holds no `layer/bridge`.

R3. A conformance test under `test/conformance/` enforces R1 and R2 and the
absence of exported `*Fact` types. Example: adding `type FooFact string` to
`layer/mcast` fails the gate.

R4. Every exported identifier listed under Inventory, Dead exports, is
deleted, unexported, or has a named caller outside its package. Example:
`analysis.InputValidity` does not exist.

R5. `mcast.Layer` holds no mutex. Example: its doc comment states the caller
serializes, as `stp`, `lag`, `routing`, and `bridge` do.

## Inventory

Paths are as of commit `61775c73`, before phase 1 moves them. `N` is
`src/common/netsim`, `V` is `N/vswitch`. After phase 1, `V/<cap>` is
`src/common/sim/layer/<cap>` and `V` is `src/common/sim/device/vswitch`.

### Contract divergences

| Package | Validate | Normalize | Constructor | Time | RetentionKey | Layer constant | Rule IDs |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `port` | `Table.Validate()` | `Table.Normalize`, `Port.Normalize` | `NewBuilder().Build()` | none | none | all 12 `Layer*` (`port.go:18-54`) | literals |
| `phy` | `(ports)` | `()` | none: `Resolve`, `Allocate`, `Negotiate` | none | none | in `port` | none |
| `bridge` | `(ports)` and `Bridge.Validate` | `()` and `NormalizeSeeds` | `New(cfg, ports)` | `Age` | none | in `port` | literals |
| `lag` | `(ports)` | `(ports, systemID)` and alias `Defaults` | `New(cfg, ports, systemID)` | `Wake`, `NextWake`, `LinkChange(now, member, up)` | `(cfg, ports, systemID)` | in `port` | literals in `V/switch.go` |
| `stp` | `(ports)`, `ValidateTimers`, `MST.Validate`, `PVST.Validate` | `()`, `MST.Normalize`, `PVST.Normalize(priority)` | `New(cfg, ports)` | `Wake`, `NextWake`, `LinkChange(now, port, up, p2p, speed)` | `(cfg, ports, speeds)` | in `port` | literals in `V/switch.go` |
| `loopprotect` | `(ports)` | `()` | `New(cfg, ports, mac)` | `Wake`, `NextWake`, `LinkChange` | `(cfg, ports, mac)` | own (`diff.go:14`) and `port` | literals in `V/switch.go` |
| `mcast` | `(ports)` | `()` | `New(cfg, ports)` | `Age` | `(cfg, ports)`, unused (`V/derive.go:165-173`) | in `port` | literal in `V/switch.go` |
| `routing` | `(ports)` | `()` | `New(cfg, ports, nodeID)` | `Wake`, `NextWake`, `Age` | `(cfg, ports)`, omits `nodeID` | in `port` | literals in `routing/layer.go` and `V/switch.go` |
| `traffic` | `(ports)` | `()` | none: `NewBucket`, `Copies(cfg, *bridge.VLAN, ...)` | none | `(cfg)`, in `policer.go` | own (`config.go:18`) and `port` | five exported, plus literals |
| `filter` | `()` | `()` | `New(cfg, _ port.Table, nodeID)` | none | none, absent from `V/retention.go` | own (`filter.go:20`) and `port` | five exported |

### Declared more than once

- `FlushTarget`: `stp/layer.go:39`, `loopprotect/layer.go:43`,
  `bridge/bridge.go:217`. The `loopprotect` one never fills `FIDs`.
- `Emission`: `stp`, `lag`, `loopprotect`, and `V/switch.go:155`.
- `Effects`: four types across `stp`, `lag`, `loopprotect`, `routing`.
- `Difference`: `V/compare.go:19-31` and `N/fabric/compare.go:21-33`.
- Metadata helpers: `V/metadata.go:17-73` and `N/fabric/journey.go:439-541`,
  `N/fabric/compare.go:822`.
- `Origin` and `Lifetime`: `bridge/fdb.go:40-61` and `mcast/layer.go:51-72`.
- `bridge.Seed` and `bridge.Entry` are field-identical (`bridge/fdb.go:64-81`).
- `bridge.Selection` repeats `lag.Selection` with `Cause` as a string
  (`V/switch.go:3770-3786`).
- Layer constants for `loopprotect`, `filter`, and `traffic` exist in the
  package and in `port`.
- `mcast.MembershipDecisionFact` (`mcast/fact.go:31`) against
  `newMembershipFact` (`V/metadata.go:139`), and `loopprotect.ForwardingFact`
  (`loopprotect/fact.go:18`) against `loopProtectDecisionFact`
  (`V/switch.go:2564-2595`). The package's own fact is unused in production.
- `lag.LACPDecodeFact` and `lag.MemberTransitionFact` share the type
  identifier `lag.lacp_decision` (`lag/fact.go:21,112,120`).
- `routing.EgressFact` reuses `routing.lookup_decision` (`routing/fact.go:91`).

### Import rule breaches

- `traffic/mirror.go` imports `bridge` for `*bridge.VLAN`.
- BPDU and SSTP codecs sit in `stp/bpdu.go` and `stp/sstp.go`.
- `mcast.Layer` embeds `sync.RWMutex` (`mcast/layer.go:103-105`).
- `loopprotect.GroupAddress` is an exported mutable variable (`probe.go:33`).

### Dead exports

No caller outside the package and its tests, by a search of `src/`:

- `trace`: `Render`, `CompareStep`, `EqualChange`, `CompareChange`,
  `SortChanges`, `CanonicalChanges`.
- `analysis`: `InputValidity`, `InputValid`, `InputInvalid`.
- `port`: `IfIndexFact`, `MTUFact`, `LagParentFact`. `Table.Transmit`
  returns a member string that is always empty (`port.go:368-392`).
- `phy`: `SourceSetting`, `ClassPowerNanowatts`, `Class`, six `Reason*`, and
  the five fact wrappers.
- `bridge`: `Bridge.Forward`, `Bridge.Peek`, `Bridge.Validate`, `GateCount`
  (test only), `DefaultAgingTime`, `DefaultServiceTPID`, the fact wrappers.
- `lag`: `Config.Defaults`, `Pending`, `PendingCause`, seven timing
  constants, the fact wrappers.
- `stp`: `InstancePortInfo`, `DefaultPathCost`, `MigrateTime`,
  `DefaultMaxHops`, the eleven fact wrappers in `diff.go:16-112`.
- `loopprotect`: `ReasonUnsupportedProbe`, `EtherType`, `DefaultInterval`,
  `LayerLoopProtect`, four diff facts.
- `mcast`: `RetentionKey`, `DefaultLastMemberQuery*`, three diff facts,
  `ReasonNoRouterPort` and `ReasonBadControl` (produced by the switch).
- `routing`: `Candidate`, `LocalAddressLookupScope`, `DiscardHeld`, nine
  diff facts.
- `filter`: `HasBinding`, `BindingScope`, `Tuple`, `Config.Equal`,
  `Rule.Equal`, `Match.Equal`, five snapshot facts.
- `traffic`: ten diff facts, `Bucket.Tokens`, `Config.OutputPorts`.
- `vswitch`: `CompareResults`, `DeviceMACFact`, `LayerFact`,
  `AllKeptRetention`, `Switch.Err`, `Switch.PeekMember`, `Switch.Speeds`,
  `Switch.Resolve` and `Switch.MembershipFact` (exported for
  `bridge.GroupResolver` only), `ReasonHeldInterfaceUnknown`,
  `ReasonHeldCauseUnknown`.
- `fabric`: seven fact wrappers, `Fabric.Unlinked`, `JourneyOrigin.Validate`,
  `ReasonTruncatedRecord`, `Fabric.Mcheck`.
- `stream`: `SplitMix64` is exported only because `Variation.Apply` takes it
  (`stream/source.go:37`).
- `netsimtest`: forty `Case*` functions and `PermuteOrder`. Other packages
  call only `DefaultRegistry`, `Assert*`, and `Representative*`.

## Open questions

- Does folding `Age` into one `Advance(now)` keep arrival-time aging? Decide
  from `fabric/run.go:410,561`, where the fabric calls both today.
- Subject keys in `Diff` are quoted in `routing/diff.go:529` and raw in
  `filter/diff.go:271`. Pick one form and state it in `trace/README.md`.
- `lag` assigns a default LACP key from the LAG's position in a sorted list
  (`lag/config.go:188`), so adding a LAG shifts its neighbours' keys. Decide
  whether normalization keeps doing that.
