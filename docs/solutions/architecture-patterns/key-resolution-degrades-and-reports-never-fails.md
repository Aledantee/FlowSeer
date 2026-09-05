---
title: Key Resolution Degrades and Reports, It Never Fails a Row or a Run
date: 2026-09-05
category: architecture-patterns
module: src/protocol/snmp
problem_type: architecture_pattern
component: code_generation
severity: high
applies_when:
  - "changing INDEX or AUGMENTS resolution in src/protocol/smi/resolve.go"
  - "changing how mibgen emits a table's key type in emit_key.go"
  - "changing DecodeIndex, DecodeIndexInto, or a generated table's Key/KeyValid shape"
  - "deciding whether an unresolvable index part, an unimported key type, or a malformed instance suffix should be an error"
  - "a mapper is dropping rows and the cause traces back to KeyValid rather than a decode error"
related_components:
  - snmp_library
  - code_generation
  - mib_mapping
tags: [snmp, mibgen, smi, index, keys, error-handling, blast-radius, code-generation]
---

# Key Resolution Degrades and Reports, It Never Fails a Row or a Run

## Context

[A Decoder's Decline Costs the Whole Table, Not the Field](decode-failure-blast-radius-in-generated-walks.md)
documents that a generated column decoder must coerce a malformed value
instead of erroring, because an error there ends the whole table walk. That
rule is scoped to column *values*: the fields a row carries once its identity
is known.

Working through mibgen's key resolution turned up the same rule one layer
down, applied to identity itself rather than to a field. A row's key comes
from four separate resolution steps that run at different times: the SMI
parser resolves what an INDEX part or an AUGMENTS clause names, mibgen
decides what Go type a key becomes, and the generated code decodes the wire
suffix into that type at collection time. Every one of those steps can fail
on real MIBs and real agents, for reasons the schema doesn't rule out: a
vendor MIB augments a row from a module nobody configured mibgen to see, an
index column's declaring module was never imported, an agent answers a
GETNEXT with a suffix that doesn't match its own MIB's INDEX clause. None of
the four steps turns that into a load failure, a generation failure, or a
lost table. Each reports the failure next to it and keeps going with a
degraded result: an unresolved index part, a base-typed key field, a zero key
on one row.

## Guidance

**When a key can't be resolved, keep resolving everything around it and say
so where the failure happened. Never fail the module, the table, or the
row's neighbors over it.** The shape of "say so" differs by layer because
each layer has a different audience and a different lifetime:

| Layer | On a resolution failure | Fallback | Reported to |
|---|---|---|---|
| SMI parser: INDEX/AUGMENTS resolution | Unresolvable index part or AUGMENTS target | `Node`/`Type` left nil, `Unresolved: true`; an AUGMENTS chain with no valid base leaves the table without a copied index | `Diagnostic` on the declaring row (`ErrCodeUnresolvedIndexPart`, `ErrCodeUnresolvedAugments`) |
| mibgen: key type emission | Key type not configured or not imported by the module | Field keeps the raw base type (integer or octet string) instead of the named convention | `degradedRef` collected during emission, printed as one line per reference |
| Runtime decode: `DecodeIndex`/`DecodeIndexInto` | Malformed instance suffix (short, over-length, out-of-range arc, leftover arcs) | `ok=false`, never an error; parts already decoded keep their values, the failing part and everything after it is zeroed | Caller reads `ok`/`KeyValid`, no diagnostic surface of its own |
| Collector table read (`collect`) | Row delivered with `KeyValid() == false` | Row skipped; the rows around it are unaffected | Nothing. The row is silently dropped before the mappers see it |

The SMI parser and mibgen layers report through a text channel a human reads
(a diagnostic, a stdout line) because the failure is a property of the MIB
corpus or the mibgen configuration, fixed once and then stable. The runtime
layers report through a boolean the caller must check, because the failure
is a property of one wire response and has to be handled per row, every
collection cycle.

## Why This Matters

Trace one failure end to end. `resolveIndexPart` in
`src/protocol/smi/resolve.go:1548-1559` looks up the node an INDEX part
names; when the lookup fails it sets `part.Unresolved = true` and raises
`ErrCodeUnresolvedIndexPart` on the row's declaration, but the table keeps
its other, resolved parts and the module load continues. `linkAugments`
(`src/protocol/smi/resolve.go:1568-1588`) does the same for an AUGMENTS
clause: an unresolvable target raises `ErrCodeUnresolvedAugments` and returns
without copying an index, rather than failing every table linkAugments still
has to visit.

`keyPartFor` in `src/protocol/snmp/cmd/mibgen/emit_key.go:371-392` decides
what Go type an index part becomes. When the part names a keyed convention
whose declaring module mibgen never imported, it calls `recordDegraded`
(comment at `emit_key.go:358-363`) and keeps the field at its base type, an
`int32` or `[]byte` instead of the named convention, because the field's
shape has to match the wire suffix for the row to decode at all; only the
label is lost. `reportEmit` in `src/protocol/snmp/cmd/mibgen/main.go:145-155`
prints one line per degraded reference and still exits `OK`.

`DecodeIndexInto` (`src/protocol/snmp/index.go:90-127`) decodes the arcs
against the declared shapes. Any of a short suffix, an over-length arc, an
out-of-range octet or IPv4 byte, an unknown shape kind, or leftover arcs
after the last part returns `false`. It never returns an error. The comment
at `index.go:66-74` states the reason directly: an error here would end the
whole table walk, the same blast radius the decoder doc already covers, and
a malformed index is a fact about one row, not the table.

The generated row type carries that boolean forward.
`fakekeysmib/mib.go:108-116` (`FakeKeyTableWalker.Iter`) decodes the key for
every row and yields it regardless of `KeyValid`; a false result gives a
zero `Key` and the raw suffix `OID` beside it, not a skipped row. The row
still reaches the caller with every other column decoded normally.

The collector is where the degraded key finally becomes an omission, and it's
a deliberate one. The table read in `src/modules/localnet/collect/mapper.go`
(`NewTable`) drops a row when `!row.KeyValid()` while walking on behalf of
the mappers: its suffix names no row the model can hold, so there's nothing
to key it by, but the rows around it are unaffected and the walk's error, if
any, is unrelated. Compare this to the
LLDP fatal-walk case in the decoder doc: that discards a whole `LLDPFacts{}`
because a *decode* error ended the walk. A `KeyValid` skip never triggers
that path: it isn't an error at all, so it can't become fatal.

## A Worked Example

A vendor MIB table augments a row declared in a module the mibgen
configuration doesn't list. Parsing that module hits `linkAugments`, which
can't find the augmented table and raises `ErrCodeUnresolvedAugments`
instead of failing the load; the table is left without a copied index.
Generation for a *different*, correctly configured table that keys off a
convention from the same missing module hits `keyPartFor`, records a
`degradedNotImported` reference, and emits that table's key field as a plain
integer; `mibgen` still writes the package and prints the degraded line.
Collection against a real agent later decodes that table's rows fine, since
the base-typed field decodes like any integer, so the only visible cost is a
less specific Go type, found by reading the tool's own output rather than by
a build failure.

Now change one variable: instead of a missing import, the agent itself
answers with a suffix one arc short of what its MIB declares (a firmware bug,
or a table shared across two loosely related device families).
`DecodeIndexInto` returns `false` on that row. The generated walker still
yields it with `KeyValid() == false`, `walkIfMIB` skips it, and every other
interface in the same GETBULK response is unaffected. Nothing in this chain
needed a special case for "vendor sends a bad suffix": the same fallback
that handles resolver and codegen gaps handles a bad wire value too.

## When to Apply

Any change to:

- `src/protocol/smi/resolve.go`: `resolveIndexPart`, `linkAugments`,
  `augmentedTable`, or the diagnostics they raise.
- `src/protocol/snmp/cmd/mibgen/emit_key.go`: `keyPartFor`,
  `conventionKeyBase`, `recordDegraded`, or the `degradedRef` type.
- `src/protocol/snmp/index.go`: `DecodeIndex`, `DecodeIndexInto`,
  `decodeIndexPart`.
- A generated table's `Key`/`KeyValid` shape (see
  `src/protocol/snmp/cmd/mibgen/emit_table.go` for the emitter, or any
  `*mib.go` golden file such as
  `src/protocol/snmp/cmd/mibgen/testdata/golden/fakekeysmib/mib.go` for the
  shape it produces).
- The collector's table read in `src/modules/localnet/collect/` that checks `KeyValid()` before keeping a
  row's key.

Adding a new failure mode to any of these should keep the same shape: report
where the failure is found, degrade to the most specific value still
knowable (a raw index part, a base-typed field, a zero key), and let
whatever's above continue rather than turning identity resolution into an
error that a caller has to catch.

## Examples

`src/protocol/snmp/cmd/mibgen/emit_key_test.go:148-172` pins three
`degradedNotConfigured` references from one module in a single render,
proving degradation collects across index parts, AUGMENTS-linked keys, and
refined index columns rather than stopping at the first one.
`src/protocol/snmp/cmd/mibgen/emit_descriptor_test.go:85-106` covers the
`degradedNotImported` path the same way.

`src/protocol/snmp/cmd/mibgen/main_test.go:277` (`TestRun_ReportsDegradedReferences`)
exercises the full command: a MIB with a reference mibgen can't resolve
still exits successfully and prints the degraded line, which is the
end-to-end proof that degraded keys don't fail a generation run.

## Related

- [A Decoder's Decline Costs the Whole Table, Not the Field](decode-failure-blast-radius-in-generated-walks.md)
  states the same principle for column values rather than row identity; that doc
  covers the runtime decode layer this one extends to resolution and
  generation.
