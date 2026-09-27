---
title: Reused Walker-Owned Row Storage in a Streaming Walk Prevents Loop-Local Allocations
date: 2026-09-27
last_verified: 2026-09-27
category: architecture-patterns
module: src/protocol/snmp
problem_type: architecture_pattern
component: snmp_library
severity: medium
applies_when:
  - "implementing or refactoring a streaming walker or iterator that decodes row data into structs via callbacks"
  - "investigating unexpected allocation growth in a streaming table walk where row values escape to the heap"
  - "sharing or pooling row memory across iterations of a streaming decoder"
  - "verifying that consumer retention of yielded rows remains safe when underlying walker storage is reused"
related_components:
  - code_generation
  - testing_framework
tags:
  - snmp
  - streaming
  - allocation-optimization
  - escape-analysis
  - iterator
  - memory-safety
---

# Reused Walker-Owned Row Storage in a Streaming Walk Prevents Loop-Local Allocations

## The situation

During the phase 2 MIB binding size optimization, generated table walking was
unified by delegating iteration and decoding to a generic runtime type,
`snmp.TableWalker[Row]` (`src/protocol/snmp/table.go:54`). Generated tables pass
an ordinal cell decoder callback `func(*Row, int, RawVarBind) error` and an
index key binder `func(OID, *Row)`.

In the first implementation, `TableWalker.Iter` declared a loop-local row
variable (`var row Row`) inside the iteration loop over `ColumnWalker.Iter`.
It passed `&row` to `w.bindKey` and `w.decode`, then yielded `row` by value
(`yield(idx, row)`).

Even though `Row` was yielded by value, taking the address of `row` and passing
it to indirect function pointers (`w.bindKey` and `w.decode`) caused Go's
escape analysis to allocate `row` on the heap for every yielded row. For a
50-row table walk, this added ~50 heap allocations per walk (TableWalk jumped
from 1,667 to 1,717 allocs/op), violating allocation parity with `main`.

## What is true and why

Moving row storage into a walker-owned field (`row Row` on
`TableWalker[Row]`, `src/protocol/snmp/table.go:59`) bounds the allocation to
walker construction. Because `TableWalker` exists for the duration of the walk,
taking `&w.row` inside `Iter` reuses existing storage and eliminates per-row
heap allocations.

Reusing row storage across iterations imposes two safety constraints:

1. **Explicit per-iteration reset.** Before decoding each row, `w.row` must be
   reset to the zero value (`w.row = zero`, `src/protocol/snmp/table.go:95`). If
   a sparse table receives a column on row 1 but not on row 2, omitting this
   reset causes row 1's field values and presence bits to leak into row 2.
2. **Single-consumer isolation.** Because `Iter` yields `w.row` by value
   (`yield(idx, w.row)`, `src/protocol/snmp/table.go:116`), consumers who
   retain rows across iterations receive independent copies. But `TableWalker`
   itself is single-use and single-consumer; multiple iterations or concurrent
   callers must not share a walker instance.

## How to apply it

When building or refactoring a streaming walker with callback-based decoding:

```go
type TableWalker[Row any] struct {
	cw     *ColumnWalker
	decode func(*Row, int, RawVarBind) error
	row    Row // walker-owned reusable storage
}

func (w *TableWalker[Row]) Iter() iter.Seq2[OID, Row] {
	return func(yield func(OID, Row) bool) {
		var zero Row
		for idx, cells := range w.cw.Iter() {
			w.row = zero // zero out prior row fields before decoding
			for _, cell := range cells {
				if err := w.decode(&w.row, cell.Column, cell.Value); err != nil {
					w.cw.Fail(err)
					return
				}
			}
			if !yield(idx, w.row) { // yield by value
				return
			}
		}
	}
}
```

## Evidence

- **Walker-owned row field:** `src/protocol/snmp/table.go:59` embeds `row Row`
  on `TableWalker[Row]`.
- **Reset and reuse:** `src/protocol/snmp/table.go:93-95` initializes
  `var zero Row` and clears `w.row = zero` per row before decoding.
- **Allocation parity:** With walker-owned row storage,
  `BenchmarkTableWalk/impl=flowseer` restored allocation parity with `main`
  at 1,667 allocs/op (commit `f7a99b8a`).
- **Retained row isolation test:** `TestTable_RetainedRowsSurviveLaterRows`
  (`src/protocol/snmp/table_test.go:426-510`) asserts that:
  - Rows appended to a slice during iteration retain their distinct values
    after later rows are yielded (`table_test.go:499-503`).
  - Column values present in row 1 do not leak into row 2 when row 2 omits
    that column (`table_test.go:507-509`). Removing `w.row = zero` fails the
    test with: `row 2 B = "c1.1", observed true; want empty and unobserved`.

## What this does not cover

- **Reference-type fields:** Yielding `w.row` by value is a shallow copy. If a
  row decoder allocates heap structures (such as byte slices or map entries)
  and assigns them to `Row` fields, those allocations occur unless managed
  separately.
- **Pointer-yielding iterators:** If an iterator yields `*Row` rather than
  `Row`, reusing walker-owned storage aliases the yielded pointers across
  iterations. A pointer-yielding walker requires caller-owned copies or an
  explicit buffer pool.
