---
title: Network Model Boundary - Plan
type: refactor
date: 2026-10-01
artifact_contract: flowseer-plan/v2
execution: code
---

# Network Model Boundary - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

`netmodel.Load` takes one input value, is a coordinator over one loader per
layer, and raises an issue for every input it cannot translate. Stop
condition: if a per-layer loader needs more than the shared load context and
its own messages, the decomposition line is wrong and is redrawn before code
moves.

## Decisions

- The parent's Decisions apply.
- `Load(now, source, Input)` replaces fourteen positional parameters
  (`netmodel.go:178-193`). Why: the corpus and the package's own tests each
  define a private `loadInput` struct to manage the call.
- An untranslatable match term skips the rule set with an issue. It never
  leaves a match empty. Why: an empty `Src`, `Dst`, or port list means "any"
  to the filter, so a dropped term widens the rule (Correctness 1, 2, 4).
- `InterfaceCounters` leaves `netmodel` for the package that collects
  counters. Why: it is the only reason `netmodel` imported `fabric` before
  the move.
- The export functions stay only if a caller exists by then. Why: none of
  the six has a caller outside the package's tests.
- Each Correctness entry was read, not run.

## Requirements

R1. Every Correctness entry has a test that fails before its fix.

R2. `Load` is under the parent's function size limit, and each layer's
loading is one file. Example: `load_filter.go` holds the filter facet's
translation and its issue codes.

R3. No translation step discards an input silently. Example: a filter rule
with a port range whose start exceeds its end yields a skipped rule set and
an `Unsupported` or `Invalid` issue scoped to it, and `Readiness` is not
`Complete`.

R4. A requested capability that the loader drops is reported. Example:
requesting the filter layer on a device with no routed interface raises an
issue instead of removing the layer from `report.Capabilities` unremarked.

## Inventory

Paths are as of commit `61775c73`. `NM` is
`src/common/netsim/vswitch/netmodel`, which phase 1 moves to
`src/common/sim/netmodel`. No entry has a test in the tree.

### Correctness

1. High. `parsePortMatch` returns not-ok for an inverted range and the
   caller ignores it (`NM/netmodel.go:1955,2432`). The match keeps an empty
   port list, which matches any port.
2. High. `parseIPPrefix` accepts an IPv4-mapped IPv6 prefix
   (`NM/netmodel.go:2412`), unlike `parseIP` (`:2332`). The rule never
   matches an IPv4 packet.
3. High. The filter layer is removed from the reported capabilities when
   routing is absent (`NM/netmodel.go:2069`) with no issue.
4. High. A prefix that fails to parse is ignored in rule translation
   (`NM/netmodel.go:1945`), leaving `Src` or `Dst` empty.
5. Medium. `FdbEntries` returns an error for any entry of a bridge without
   VLAN awareness (`NM/export.go:96`).
6. Medium. `Poe` requires a PSE group key to parse as an unsigned integer
   (`NM/export.go:151`). The simulator keys groups by string.
7. Medium. A malformed interface MAC records both a skipped entry and a
   default assumption (`NM/netmodel.go:1789-1790`).
8. Medium. `translateAction` maps an unrecognized action to drop without an
   issue (`NM/netmodel.go:2383-2394`).
9. Low. `NetworkInstances` exports one instance named `default` and ignores
   configured VRFs (`NM/export.go:36`).
10. Low. `InterfaceCounters` accepts a start time after the current time
    (`NM/counters.go:34`).

### Completeness

- `Load` rejects the multicast, loop-protection, and traffic layers
  (`NM/netmodel.go:385-394`), and export covers six subsystems. Loading them
  needs facets in the network model. Whether those exist is checked under
  `spec/proto/flowseer/` at re-planning, and a missing facet is a schema
  plan of its own, not part of this one.
- `Load` rejects every spanning-tree protocol version other than RSTP
  (`NM/netmodel.go:1278`) although the layer runs MSTP and PVST.
- A rule set with an EtherType, MAC, PCP, or DSCP term is discarded whole
  (`NM/netmodel.go:2378`) without naming the term.
- The bridge aging time is always the 300-second default
  (`NM/netmodel.go:1018`). The network model has no field for it.
- `Result.Metadata` and `Result.Spec.Metadata` hold the same value
  (`NM/netmodel.go:2213-2227`), and `Report.Defaults`, `Skipped`, and
  `Conflicts` repeat what the metadata's assumptions and issues say
  (`NM/report.go:57-64`).

### Design

- `Load` is one 2,050-line function over about forty closed-over locals
  (`NM/netmodel.go:178-2228`). The loaders: ports, phy, bridge, spanning
  tree, LAG, routing, filter, and FDB seeds, over a shared load context.
- The parse helpers return `(T, bool)` and lose the reason
  (`NM/netmodel.go:2322,2345,2396,2427`). They return an error the caller
  turns into an issue.
- The missing transmit-hold-count default is the literal `"6"`
  (`NM/netmodel.go:1344-1353`) where `stp` has a constant.

### Tests

- `NM/testdata_icx7150_test.go:430` asserts that a spec exists and builds,
  not what the switch then forwards.
- Fixture helpers are repeated in `netmodel_test.go:40-80`,
  `trust_test.go:37-80`, and `routing_test.go:45-75`.
- `routing_test.go` (2,180 lines), `netmodel_test.go` (1,680), and
  `trust_test.go` (1,608) follow the loader split.

## Open questions

- `fdb_entry.proto` requires a VLAN identifier from 1 to 4094, and the PSE
  budget schema keys a group by integer. Are entries 5 and 6 loader defects
  or schema limits? Read `spec/proto/flowseer/net/switching/v1/` and
  `spec/proto/flowseer/net/phy/v1/` before deciding. A schema change is a
  separate plan under the protobuf conventions.
