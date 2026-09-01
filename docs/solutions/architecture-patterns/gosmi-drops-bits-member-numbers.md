---
title: gosmi Drops BITS Member Numbers, So Generated Bit Positions Are a Guess
date: 2026-08-30
category: architecture-patterns
module: src/common/snmp/cmd/mibgen
problem_type: tooling_decision
component: code_generation
severity: medium
applies_when:
  - "weighing whether to replace a parsing dependency, where error recovery is the property in question"
  - "reading a verdict in this store that a later change overturned"
  - "tracing why src/common/smi exists rather than a gosmi fork"
related_components:
  - snmp_library
  - code_generation
  - third_party_dependencies
tags: [gosmi, snmp, mibgen, bits, code-generation, third-party, known-limitation, textual-convention]
---

# gosmi Drops BITS Member Numbers, So Generated Bit Positions Are a Guess

## Superseded: the replacement landed

`gosmi` is gone. `src/common/smi` is a hand-written SMIv1/SMIv2 parser and
`mibgen` renders from it, so a `BITS` member now carries the number the MIB
wrote and the emitted positions are read rather than inferred. The two
`-- FlowSeer local patch:` edits in the vendored IEEE MIBs are reverted, the
`DEFVAL { { } }` panic is gone, and a numbering-gap fixture exists.

The diagnosis below is correct and worth keeping: the loss really is in
`gosmi`'s conversion layer, there really is no override seam, and the
consequence really is a silent mis-decode. What did not survive is the verdict
in **Why nothing was changed** — that writing a replacement parser is the wrong
move regardless, because the grammar is only ~700 declarative lines against a
bulk of resolution code that would be rebuilt for no benefit.

That reasoning holds against a like-for-like replacement, and the work that
landed is not one. The verdict weighed this defect alone. The property actually
bought was error recovery: a participle grammar has no error recovery and
cannot gain it by patching, forking, or fixing individual defects, because the
first syntax error ends the file. That is a different kind of component, not a
cheaper version of the same one — and the resolution code the verdict counted
as wasted rebuild is what makes recovery mean anything, since a declaration
that survives its neighbour's failure still has to resolve.

The corpus settled the size of it. 4,994 of 5,650 `BITS` types under
`spec/mib/` are not numbered consecutively from zero, so the coincidence this
document rests on holds for today's configured modules and almost nothing else.
And with the two vendored patches reverted, `gosmi` panics on 78 of 1,680
files — a panic takes the process, not the module.

Read the rest as the record of how the dependency behaved and why the decision
looked right at the time. Two of its **Traps** are closed: the `emit_bits.go`
comment that misattributed the cause to libsmi is corrected, and a
numbering-gap fixture now exists. **Prevention** is obsolete; adding a MIB with
gapped `BITS` members needs no check.

## Context

`mibgen` emits a named `snmp.BitPos` constant for each member of a MIB `BITS`
type. The bit position it emits comes from the member's index in the parsed
slice — declaration order — not from the number written in the MIB.

That works today only by coincidence. Every `BITS` type currently generated
numbers its members consecutively from zero, so declaration order and the
declared numbers agree. `LldpSystemCapabilitiesMap` is the driving case:
`other(0)` through `stationOnly(7)`, no gaps.

The first MIB that skips a position breaks silently. A device reporting bit 5
would decode as whichever member happens to sit fifth in the file, and nothing
in the pipeline would flag the mismatch.

## What actually happens

gosmi parses the number correctly and then discards it during conversion.
Confirmed by executing a probe against `testdata/mibs/FAKE-MIB.mib`, whose
`FakeCapabilities` declares `alpha(0), beta(1), gamma(2)`:

```
type FakeCapabilities   base=Enum  enum=true
    member alpha    Value=0
    member beta     Value=0
    member gamma    Value=0
```

Every member arrives as zero.

The loss is in `smi/internal/type.go`:

```go
func GetValue(value string, baseType types.BaseType) types.SmiValue {
	v := types.SmiValue{BaseType: baseType}
	switch baseType {
	case types.BaseTypeInteger32:  // and Integer64, Unsigned32, Unsigned64
		v.Value = GetValueInt32(value)
	}
	return v  // anything else: Value stays nil
}
```

`GetValue` receives the number as a string and populates `Value` only for the
four integer base types. A `BITS` member is not among them, so `Value` is left
nil.

Two corrections to the obvious reading of this, both of which cost real time to
discover:

- **`convertValue` in `type.go` is not the bug.** It is downstream, and its
  existing `uint32` case would work fine if the value were ever populated.
  Patching it alone changes nothing.
- **gosmi reports the type's base as `Enum`, not `Bits`.** So neither the fix
  nor any detection logic can simply key on `types.BaseTypeBits` at this layer.

## Why nothing was changed

Reaching `GetValue` means owning a fork: gosmi is an external module and the
function lives in an internal package, so there is no override seam. The fork
buys more than this one fix — it would also let us patch the `DEFVAL { { } }`
parser panic and delete the `-- FlowSeer local patch:` edits in the vendored
IEEE MIBs that must otherwise be re-applied on every upstream re-sync. But it is
a standing maintenance commitment, and no MIB in `mibgen.yaml` needs it yet.

Worth knowing if that decision is revisited: gosmi is pure Go, roughly 6,700
lines, MIT licensed, with no cgo anywhere. It is not a libsmi binding. Writing a
replacement parser is the wrong move regardless — the grammar is ~700
declarative lines, while the bulk of the library is import resolution, type
resolution, and OID assembly that we would rebuild for no benefit.

## Traps

- **The comment in `emit_bits.go` attributes the loss to libsmi.** That is
  wrong; libsmi is not in the dependency tree. The loss is in gosmi's own
  conversion layer. The comment is otherwise accurate about the consequence.
- **The `FakeCapabilities` fixture cannot catch this.** It numbers its bits
  `0,1,2`, so declaration order and true values coincide. Any fix must add a
  fixture with a deliberate gap, or it will pass against a still-broken parser.
- **Current generated output is not evidence either way.** Correct bit
  positions today prove only that every current MIB is consecutive.

## Prevention

Before adding a MIB with a `BITS` type, check whether its members are numbered
consecutively from zero. If they are not, the emitted positions will be wrong,
and the fork becomes necessary rather than optional.
