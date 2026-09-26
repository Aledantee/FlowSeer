---
title: Generated Binding Size Phase 2 - mibgen - Plan
type: perf
date: 2026-09-26
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: code
parent: docs/plans/2026-09-26-1113-perf-generated-binding-size-plan.md
---

# Generated Binding Size Phase 2 - mibgen - Plan

## Goal

`generated/go/mib` is at most 27,012,354 bytes, 70% of its 38,589,077-byte
baseline, while generated table walks keep their current API, decoding,
presence, key, ordering, and failure behavior. The generator emits compact
typed glue over shared table, column, and enum behavior in `src/protocol/snmp`.

This phase is wrong if the shared column decoder cannot preserve the guarded
raw fast path and its generic fallback without increasing `allocs/op` or
`B/op` in the committed `BenchmarkTableWalk` baseline. Stop rather than land
an inline-only decoder, a weaker size target, or a rewritten benchmark
baseline.

## Decisions

- The parent plan's decisions apply. Each table keeps its exported row type,
  named walker type, and table singleton. Generated packages import only the
  public `snmp` API. Why: `src/protocol/snmp/bench/tablewalk_parity_test.go:88-92`
  names `*ifmib.IfTableWalker`, and callers throughout `src/modules/localnet`
  use the table singleton and concrete row fields.
- `snmp.Table[Row, Walk]` and `snmp.TableWalker[Row]` live in
  `src/protocol/snmp/table.go`. `Table` owns selected-column validation,
  deduplication, `Walk`, and `WalkWithOptions`; `TableWalker` owns `Iter`,
  `Err`, and `Close` over the existing `ColumnWalker`. The generated
  `<table>T` embeds `snmp.Table[Row, *NamedWalker]`, and the named walker embeds
  `snmp.TableWalker[Row]` by value. `NewTable` receives the table name, the
  columns in observed-bit order, a row-key binder, an ordinal-based cell
  decoder, and a wrapper from the runtime walker value to the named walker
  pointer; its callback types are `func(OID, *Row)`,
  `func(*Row, int, RawVarBind) error`, and `func(TableWalker[Row]) Walk`.
  Why: the embedded generic methods retain the exact generated return
  type without emitting five methods per table. Embedding the runtime walker
  by value avoids a second walker allocation.
- `Table` continues through `WalkColumns`; it does not call `BulkWalk` or
  `BulkWalkRaw`. It matches allowed columns by their precomputed `Key()`, keeps
  the matching column's stable ordinal, and passes that ordinal to the
  generated decoder. Why: `src/protocol/snmp/column_walk.go:26-131` owns the
  bounded ordered merge, and `newColumnRequester` calls `GetBulk` or `GetNext`
  directly. Keeping that path preserves request sizing, `tooBig` recovery,
  early stop, and test-double behavior.
- Generated columns use additive `NewTableColumn` and
  `NewFusedTableColumn` constructors in `src/protocol/snmp/column.go`.
  `NewTableColumn` stores the column's observed-bit ordinal.
  `NewFusedTableColumn` additionally stores a typed
  `func(RawVarBind) (T, bool)`. Existing `NewColumn` remains the constructor
  for test and external callers. Their signatures append `observedBit int` to
  the existing constructor arguments, with the fused function immediately
  before that integer.
  Why: the runtime unit remains buildable before regeneration, while generated
  columns carry everything shared decode and presence helpers need.
- `DecodeColumn` has type parameter `T any`, accepts `RawVarBind`, `Column[T]`,
  `*T`, and `[]uint64`, and returns `error`. It tries the column's fused decoder
  first; a decline calls `RawVarBind.Decode` and then `Column.Decode`. A field
  and observed bit change only after success. `RawInteger32As[T ~int32]`, the four uint32
  variants, and `RawCounter64As[T ~uint64]` in `rawwalk.go` preserve the named
  Go type of enums and textual conventions. Why: this is the same guarded
  choice currently emitted at
  `src/protocol/snmp/cmd/mibgen/emit_table.go:306-335`; moving it must not turn
  a raw decline into a walk error.
- The generated ordinal decoder is a switch whose cases contain one
  `snmp.DecodeColumn` call. The runtime translates a selected `AnyColumn` to
  its ordinal before I/O and passes that ordinal for each `ColumnCell`. Why:
  numeric cases avoid repeating long column identifiers while keeping the
  field address and its static Go type visible to the compiler.
- Each table passes one column slice in observed-bit order to `NewTable` and
  `ColumnObserved([]uint64, []AnyColumn, AnyColumn) bool`. The helper matches
  the candidate's full `Key()` against that slice and reads the matched bound
  column's observed bit from the existing `observed [N]uint64`. Every generated
  `Row.Observed` becomes one call to it. The row layout, bit meanings,
  and behavior of a separately constructed column with the same OID stay
  unchanged. Why: the current 1,299 methods repeat 11,204 key cases, and the
  foreign-column protection at
  `src/protocol/snmp/test/integration/presence_test.go:106-157` depends on full
  OID identity rather than the final sub-identifier.
- `EnumString` in a new `src/protocol/snmp/enum.go` receives an `int32` value,
  the Go type name, `[]int32`, and `[]string`, in that order. It uses a
  non-allocating binary search for declared values and formats an unknown as
  `<Type>(<n>)`. `emit_enum.go` emits package-level arrays of numeric values
  and names and a one-call `String` method. Static enum name tables invalidate
  the prior inline-return assertion in `TestEmit_DescriptionWithGoLiteralHazards`;
  the test now checks the table literal. Why: numeric arrays do not repeat
  long constant identifiers, work for sparse and negative values, and avoid
  3,607 init-time maps.
- The current tree is the parent baseline: 38,589,077 bytes in 33 generated Go
  files. Contiguous declaration measurement gives 7,033,725 bytes for walker
  `Iter`, 2,983,701 for the other four walker methods, 3,241,304 for enum
  stringers, and 1,559,105 for `Observed`, 14,817,835 bytes in total. The final
  tree must remove at least 11,576,723 bytes net; the compact replacements
  therefore have a 3,241,112-byte budget. Why: this measures the current tree
  before committing to the 30% requirement and includes the generated doc
  comments that disappear with promoted runtime methods.
- The committed `src/protocol/snmp/bench/testdata/baseline-micro.txt` stays
  byte-for-byte unchanged. `bench-gate.sh` is the allocation gate; its ten
  `BenchmarkTableWalk/impl=flowseer` samples are 364 allocs/op and 68,288 to
  68,290 B/op. Why: the parent plan forbids rebaselining this phase, and
  `bench-gate.sh:61-79` hard-fails significant `allocs/op` or `B/op`
  regressions.

## Requirements

1. `generated/go/mib` contains at most 27,012,354 bytes after `go generate .`.
   For example, the baseline command from the parent returns `38589077` before
   the change and a value no greater than `27012354` after it.
2. The generated public surface remains source-compatible. For example,
   `var w *ifmib.IfTableWalker = ifmib.IfTable.Walk(ctx, sess, ifmib.IfDescr)`
   compiles, and `w.Iter`, `w.Err`, and `w.Close` retain their current
   signatures and lifecycle behavior.
3. A raw value accepted by a fused decoder writes the typed row field and its
   observed bit without calling the generic decoder. A value that the fused
   decoder declines, such as an Integer32-declared column answered as Gauge32,
   goes through `Column.Decode` and keeps its current coercion. An OCTET STRING
   that neither path can decode omits that row, stops later rows, and leaves
   earlier rows available through the iterator.
4. Selection behavior is unchanged. For example, duplicate `IfDescr`
   selections produce one stream, no columns produce no request, and an
   `LLDPRemPortID` passed to `IfTable.Walk` returns `ErrForeignColumn` before
   I/O even though another foreign column may share `IfDescr`'s final sub-ID.
5. Row identity behavior is unchanged. For example, a malformed composite
   index still yields the row with its raw suffix, zero `Key`, and
   `KeyValid() == false`; the next valid row is still delivered.
6. `Row.Observed` distinguishes a reported zero from an absent value and
   rejects foreign columns. For example, an `IfMTU` value of zero reads
   observed, a missing `IfMTU` reads unobserved, a separately constructed
   column with `IfMTU`'s OID reads the same result, and
   `row.Observed(lldpmib.LLDPRemPortID)` is false.
7. Every declared enum value renders its SMI name and every undeclared value
   renders the existing Go-type form. For example,
   `FakeStatusValueUp.String()` is `"up"`, while `FakeStatusValue(-7).String()`
   is `"FakeStatusValue(-7)"`.
8. `src/protocol/snmp/bench/bench-gate.sh` passes against the unchanged
   committed baseline. In particular, the generated table-walk path has no
   significant increase over 364 allocs/op or 68,290 B/op.
9. Regeneration is stable. After `go generate .`, running
   `go run ./src/protocol/snmp/cmd/mibgen -check` succeeds against the
   checked-in bindings.

## Out of scope

- Generated identifier names, exported row fields, key types, row layout, and
  `Observed` semantics.
- `Watch` snapshot and merge behavior, including its generated equality and
  merge arms.
- Scalar accessors, scalar-group emission, replacing `ColumnWalker`, changing
  request scheduling, or calling `BulkWalk`/`BulkWalkRaw` from tables.
- Rewriting the benchmark baseline or accepting a smaller size reduction.

## Units

### U1. Runtime support for generated table bindings

Files:
- `src/protocol/snmp/column.go`
- `src/protocol/snmp/column_test.go`
- `src/protocol/snmp/rawwalk.go`
- `src/protocol/snmp/rawwalk_test.go`
- `src/protocol/snmp/table.go`
- `src/protocol/snmp/table_test.go`
- `src/protocol/snmp/enum.go`
- `src/protocol/snmp/enum_test.go`

After: none

Change: add the two generated-table column constructors, typed fused adapters,
`DecodeColumn`, `ColumnObserved`, `Table[Row, Walk]`, `TableWalker[Row]`, and
the parallel-array `EnumString` helper exactly as specified in Decisions.
`TableWalker` stores the existing `*ColumnWalker`, selected stable ordinals,
and row callbacks; its iterator initializes one row per suffix, decodes each
cell, calls `Fail` before returning on a decode error, and preserves rows
already yielded. `Table` performs foreign-column rejection and duplicate
removal before it calls `WalkColumns`.

Tests:
- `column_test.go` proves fused success bypasses the generic decoder, fused
  decline for a wrong tag or pre-decoded `VB` reaches it, decode failure leaves
  the destination and observed bit unchanged, a named-int32 adapter preserves
  its type, and the successful helper path allocates zero times
- `column_test.go` proves `ColumnObserved` accepts a reported zero through both
  the bound column and an unbound `NewColumn` with the same OID, and rejects a
  different table and an out-of-range bit
- `table_test.go` defines a local named wrapper and proves the promoted methods
  return that exact pointer type, selection is lazy and deduplicated, foreign
  columns fail before I/O, early break closes retrieval, malformed keys remain
  rows, and a decode error preserves only the delivered prefix
- `enum_test.go` covers declared, sparse, negative, and unknown values and
  verifies the declared-value path allocates zero times

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/snmp`

### U2. Compact mibgen table and enum emission

Files:
- `src/protocol/snmp/cmd/mibgen/emit_table.go`
- `src/protocol/snmp/cmd/mibgen/emit_enum.go`
- `src/protocol/snmp/cmd/mibgen/emit_test.go`
- `src/protocol/snmp/cmd/mibgen/emit_key_test.go`
- `src/protocol/snmp/cmd/mibgen/emit_descriptor_test.go`
- `src/protocol/snmp/cmd/mibgen/emit_resolve_test.go`
- `src/protocol/snmp/cmd/mibgen/doc.go`
- `src/protocol/snmp/cmd/mibgen/testdata/golden/fakemib/mib.go`
- `src/protocol/snmp/cmd/mibgen/testdata/golden/fakekeysmib/mib.go`
- `src/protocol/snmp/cmd/mibgen/goldentest/walk_test.go`

After: U1

Change: `emit_table.go` emits bound column declarations, one column slice in
observed-bit order, the one-call ordinal decoder, the one-call `Observed`, the
named walker embedding, and the table singleton embedding `snmp.Table`; it no
longer emits table-specific lifecycle methods. Raw-index rows assign `Index` in
the row binder; typed-key rows retain the existing decode helper and `KeyValid`
result. `emit_enum.go` emits sorted numeric and label arrays plus the one-call
stringer. `doc.go` describes the runtime-owned shape. Refresh both golden
packages with the repository's golden update command.

Tests:
- emitter assertions require `NewTableColumn` or `NewFusedTableColumn`, a
  numeric decoder case containing one `snmp.DecodeColumn` call, one-call
  `ColumnObserved`, embedded `snmp.Table` and `snmp.TableWalker`, and static
  enum arrays
- emitter assertions reject generated `Iter`, `Err`, `Close`, `Walk`, and
  `WalkWithOptions` method declarations and reject inline raw/generic decode
  blocks and enum switches
- `goldentest/walk_test.go` keeps malformed, raw, address, and augment key
  cases and adds the explicit named-walker assignment and known/unknown enum
  cases from Requirements 2 and 7
- `go test ./src/protocol/snmp/cmd/mibgen/...` compiles and exercises the
  refreshed goldens

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/snmp/cmd/mibgen`

### U3. Regenerate bindings and prove the hard gates

Files:
- `generated/go/mib/**`
- `docs/plans/2026-09-26-1113-perf-generated-binding-size-phase2-plan.md`

After: U2

Change: run `go generate .` from the repository root, audit the generated diff,
and record the final byte count in this plan's outcome note. Set this plan to
`implemented` only when the size, behavior, drift, and allocation gates all
pass. Do not edit a generated file or the benchmark baseline by hand.

Tests:
- `go generate .`, followed by
  `go run ./src/protocol/snmp/cmd/mibgen -check`
- the exact size command in Verification returns at most 27,012,354
- `go test ./src/protocol/snmp/test/integration` covers generated selection,
  lifecycle, prefix-on-error, presence, foreign-column, BITS, and key behavior
- `go test ./src/modules/localnet/...` compiles and exercises table consumers
- `src/protocol/snmp/bench/bench-gate.sh` passes without changing
  `baseline-micro.txt`; the bench package also compiles the explicit
  `*ifmib.IfTableWalker` assignment

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- generated/go/mib docs/plans/2026-09-26-1113-perf-generated-binding-size-phase2-plan.md`

Waves: U1 | U2 | U3

## Verification

```bash
go test ./src/protocol/snmp
go test ./src/protocol/snmp/cmd/mibgen/...
go generate .
go run ./src/protocol/snmp/cmd/mibgen -check
bytes=$(find generated/go/mib -name '*.go' -type f -print0 | xargs -0 cat | wc -c | tr -d ' ')
test "$bytes" -le 27012354
go test ./src/protocol/snmp/test/integration
go test ./src/modules/localnet/...
src/protocol/snmp/bench/bench-gate.sh
git diff --exit-code -- src/protocol/snmp/bench/testdata/baseline-micro.txt
go test -race ./...
.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/snmp generated/go/mib docs/plans/2026-09-26-1113-perf-generated-binding-size-phase2-plan.md
```

Inspect the generated diff to confirm that each table still has its named row,
walker, and singleton; every column arm is one helper call; every enum keeps
its constants; and no generated package imports an internal `snmp` path.

## Definition of done

- [ ] Every changed path passes the diff-aware verifier.
- [ ] Runtime, generator, generated-package, integration, consumer, race, and
      benchmark checks pass.
- [ ] `generated/go/mib` is at most 27,012,354 bytes, with the exact count in
      the outcome note.
- [ ] `baseline-micro.txt` is unchanged and the table-walk allocation gate is
      green.
- [ ] Generated code uses only public `snmp` APIs and contains no plan labels.
- [ ] This plan reads `status: implemented`, has an outcome note under the
      title, and the parent phase entry records the landed commit range.
