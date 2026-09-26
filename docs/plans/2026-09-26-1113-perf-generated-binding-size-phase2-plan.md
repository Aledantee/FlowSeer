---
title: Generated Binding Size Phase 2 - mibgen - Plan
type: perf
date: 2026-09-26
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-26-1113-perf-generated-binding-size-plan.md
---

# Generated Binding Size Phase 2 - mibgen - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

`generated/go/mib` is at least 30% smaller than its 38,589,077-byte baseline,
and the SNMP table-walk hot path is not slower in allocations. The means:
every column's decode arm in a table walker's `Iter` becomes one call to a
generic runtime helper. The walker methods that are the same for every table
move into a generic runtime type that each generated walker wraps. Enum
`String` switches become a name table read by one shared helper.

This phase is wrong if the per-column helper cannot keep the fused
`RawInteger32`-style fast path
(`docs/solutions/architecture-patterns/snmp-collection-library-architecture-and-fast-path-conventions.md`)
without new allocations. In that case the column arms stay inline, and only
the walker and enum changes land.

## Decisions

The parent plan's decisions apply. These are this phase's direction; the
re-plan settles the exact shapes.

- Each generated table keeps its own named walker type, row type, and table
  singleton. The shared behavior moves into runtime code in
  `src/protocol/snmp` that those types delegate to. Why: consumers in
  `src/modules/localnet/` range over `<Table>.Walk(...).Iter()` and call
  `Row.Observed(col)` on the concrete types, and nothing reaches them through
  a common interface. Keeping the names keeps every consumer unchanged.
- A per-column arm becomes one call that decodes a `RawVarBind` into the
  row's field and sets the column's observed bit. The call tries the fused
  raw fast path when the column declares one and falls back to
  `Column.Decode` otherwise. The emitter currently chooses between these in
  `src/protocol/snmp/cmd/mibgen/emit_table.go:288-361`, and the runtime makes
  the same choice from the column value. Why: 11,204 fallback blocks of 13 to
  18 lines carry the same logic.
- `Walk`, `WalkWithOptions`, `Close`, `Err`, and the foreign-column check come
  from a generic runtime walker parameterized by the row type. The generated
  code supplies the column list, the key decoder, and the per-column
  dispatch. Why: these methods differ only in names and in the column set
  (`emit_table.go:189-269`).
- An enum keeps its named type and its constants. `String` reads a
  package-level value-to-name table through one runtime helper that renders
  unknown values as `Name(%d)`. Why: the switch in
  `src/protocol/snmp/cmd/mibgen/emit_enum.go:168-186` is 3.8 MB of the same
  control flow.
- The gate for this phase is `src/protocol/snmp/bench/bench-gate.sh`. Its
  baseline is not rewritten to make the phase pass. Why: parent Requirement 3.

## Requirements

1. `bench-gate.sh` passes against the committed `baseline-micro.txt`. The
   `BenchmarkTableWalk/impl=flowseer` samples there read 364 allocs/op and
   68,288 to 68,290 B/op.
2. A walk that selects a column whose agent value fails the fast path, for
   example an `INTEGER` column answered with an `OCTET STRING`, falls back to
   `Column.Decode` and fails the walk exactly as it does today
   (`docs/solutions/architecture-patterns/decode-failure-blast-radius-in-generated-walks.md`).
3. `Row.Observed(col)` returns the same answer for every column of the
   fakemib golden fixture after a walk that answers half the columns.
4. An enum value that is not declared renders as `<Type>(<n>)`, and every
   declared value renders its SMI name.
5. `generated/go/mib` measures at most 27,012,354 bytes, which is 70% of the
   baseline. The estimate is about 11 MB from the three levers, plus the
   per-table doc comments that go away with the methods they describe
   (11.9 MB of the tree is comments). The re-plan measures this before
   committing to it.

## Out of scope

- Changing generated identifier names, row field layout, or `Observed`
  semantics.
- Scalar-group code, unless the re-plan measures it as a significant share.
