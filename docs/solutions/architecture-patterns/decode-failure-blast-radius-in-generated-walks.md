---
title: A Decoder's Decline Costs the Whole Table, Not the Field
date: 2026-08-30
category: architecture-patterns
module: src/common/snmp
problem_type: architecture_pattern
component: code_generation
severity: high
applies_when:
  - "adding or tightening a validation bound in any Decode* helper under src/common/snmp/"
  - "deciding whether a malformed agent value should return an error or be coerced"
  - "reading the fast-path doc's claim that declining costs nothing, and wondering whether it generalizes"
  - "a device returns zero rows for a table it clearly implements"
related_components:
  - snmp_library
  - code_generation
  - mib_mapping
tags: [snmp, mibgen, decode, error-handling, blast-radius, walk-semantics, bits, validation]
---

# A Decoder's Decline Costs the Whole Table, Not the Field

## Context

While adding a size bound to `DecodeBitSet`, a review found the bound had
created a worse failure than the one it prevented. The bound existed for a real
reason — an agent answering a two-octet capability field with kilobytes of set
bits becomes one enum value per position in every message built from it — but
returning an error on the over-limit value meant that a single malformed field,
on a single neighbor, cost the caller **every fact collected for that device**.

The bound looked safe when written. Part of why is that this repo's
[SNMP fast-path conventions](snmp-collection-library-architecture-and-fast-path-conventions.md)
state that "declining costs nothing" — a claim that is true where it is written
and false one layer down.

## Guidance

**Before tightening any decoder that a generated column can call, work out what
a decline costs on the path that calls it.** The word "decline" describes three
different behaviors in this library:

| Layer | On a decode failure | Cost |
|---|---|---|
| Fused fast path (`Raw*` primitives) | Falls through to the generic decoder | Nothing — there is always a fallback |
| Generic decode inside a generated `Iter` | `ColumnWalker.Fail(derr)`, iteration stops | Failing row and later rows; caller may discard earlier facts |
| Watcher partial-fetch merge | Silently skipped | A stale field, no error |

Only the first is costless, and it is the one the existing conventions doc
documents. A decoder reached from a generated table walker has no fallback.

So when a value is malformed but its *meaning* is still recoverable, coerce it
rather than erroring. `DecodeBitSet` truncates at `MaxBitSetOctets` for exactly
this reason, and says so at `src/common/snmp/bits.go:68-73`:

```go
// A value longer than [MaxBitSetOctets] is truncated to the bound rather
// than declined: no MIB here names a position past it, so nothing
// meaningful is lost, and an error would cost far more than the octets
// do. A column decode error ends the whole table walk, so declining one
// malformed capability bitmap on one row can prevent delivery of that row
// and all later rows; a fatal caller may discard earlier facts too.
```

Reserve an error for a value whose meaning genuinely cannot be recovered — a
wrong wire variant, an exception marker. `DecodeBitSet` still declines on those.

## Why This Matters

The mechanism is in `src/common/snmp/cmd/mibgen/emit_table.go`, so it applies
to every generated table. The selected-column merge assembles the next complete
row; the generated iterator decodes that row immediately before yielding it.
A decoder error calls `ColumnWalker.Fail`, omits the failing row, and stops.
Earlier delivered rows remain available to the caller. There is no full-table
buffer or partial flush in this path.

A bad value on the first row still yields zero rows. A bad value on row 3
preserves rows 1 and 2, even if all three arrived in one GETBULK response.
`TestGeneratedWalkDecodeErrorPrefix` exercises this distinction. Raw response
validation can fail before any of that response's rows are delivered.

**A fatal table can void unrelated data.** In the LLDP mapper, an
`lldpRemTable` walk error is marked fatal (`src/common/snmpmap/lldp.go:382-386`),
and that marker makes `LLDP` return an empty `LLDPFacts{}`
(`src/common/snmpmap/lldp.go:149-157`) — discarding ports and the local system
block that were already collected successfully. One malformed capability bitmap
on one neighbor loses the device.

The full chain, verified end to end: oversized bitmap → `DecodeBitSet` error →
`derr` set (`generated/go/mib/lldpmib/mib.go:2413-2416`) → `tw.rw.Fail(derr)` →
`walk.Err() != nil` → `fatalWalk` → `LLDPFacts{}`.

## When to Apply

Any change to these thirteen helpers, or to an enum or override decoder
generated on top of them, since a generated column can reach all of them:

- `src/common/snmp/decode.go` — `DecodeInt32`, `DecodeUint32`, `DecodeUint64`,
  `DecodeBytes`, `DecodeOID`, `DecodeIP`
- `src/common/snmp/tc.go` — `DecodeMacAddress`, `DecodeDateAndTime`,
  `DecodeTruthValue`, `DecodeRowStatus`, `DecodeDisplayString`,
  `DecodePhysAddress`
- `src/common/snmp/bits.go` — `DecodeBitSet`

`RawVarBind.Decode` (`src/common/snmp/rawwalk.go:47`) sits on the same arm: a
wire-level failure also sets `derr` and kills the walk.

`DecodeDateAndTime` is the neighbor most worth a look — its tests
(`src/common/snmp/tc.go:124-157`) show it declines on bad length, bad direction,
and out-of-range fields, so three separate odd values from one agent can each
void a whole table.

Malformed selected-column indexes, including an empty suffix, terminate the
streaming walk. Spillover outside a selected column is discarded and ends that
column. No rows are discovered solely through unselected columns.

## Examples

The guard is executable, at two levels.

Unit — `src/common/snmp/bits_test.go:176`, `TestDecodeBitSet_OversizedTruncates`:
an over-limit value returns a set truncated to the bound rather than an error.

End-to-end — `src/common/snmpmap/lldp_test.go:320-337`,
`TestLLDP_OversizedCapabilityBitmapKeepsFacts`, which is the one that would have
caught the original defect:

```go
wide := make([]byte, snmp.MaxBitSetOctets+8)
wide[0] = 0x20 // bridge, inside the bound

vbs := append(macRemRow(), octetsAt(colOID(lldpRemEntry, 11, 1000, 3, 1), wide))

got := oneNeighbor(t, vbs, map[uint32]string{3: "eth0"})
```

It asserts a neighbor still comes back *and* that the in-bound capability
survives — so it pins the surviving-facts consequence, not just the decoder's
local behavior. A unit test on the decoder alone would have passed against the
erroring version.

## Related

- [SNMP collection library architecture and fast-path conventions](snmp-collection-library-architecture-and-fast-path-conventions.md)
  — its Convention 1 "declining costs nothing" is scoped to the fused fast path,
  where a decline always falls back to the generic decoder. That claim does not
  extend to the generic path documented here.
- [gosmi drops BITS member numbers](gosmi-drops-bits-member-numbers.md) — the
  other BITS learning in this area, unrelated in mechanism (codegen-time member
  numbering) but likely to be reached from the same starting symptom.

## Open

No package-level statement of this contract exists. `src/common/snmp/doc.go`
describes terminal-error latching but says nothing about decode-failure blast
radius, so the reasoning currently lives only in `bits.go` and in the docstring
emitted onto every generated walker (`emit_table.go:324-328`). Stating it in
`doc.go` would be an addition, not a correction.

The watcher's merge path silently dropping decode errors
(`emit_watch.go:154-186`) is deliberate — partial-fetch ticks report through
`Watcher.LastTickErr` at call-site granularity — but it means the same decoder
behaves oppositely depending on which generated path calls it. Nothing currently
warns a decoder author about that split.
