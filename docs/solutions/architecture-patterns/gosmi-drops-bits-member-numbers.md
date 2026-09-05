---
title: Declaration-Level SMI Recovery Preserves Declared Semantics
date: 2026-09-04
category: architecture-patterns
module: src/protocol/smi
problem_type: architecture_pattern
component: code_generation
severity: medium
applies_when:
  - "evaluating whether an SMI parser dependency fits the shipped MIB corpus"
  - "changing declaration-level recovery in src/protocol/smi"
  - "changing how BITS members flow from the parser into mibgen"
  - "using gosmi as a differential reference"
related_components:
  - snmp_library
  - third_party_dependencies
tags: [smi, parser, error-recovery, bits, mibgen, gosmi, code-generation]
---

# Declaration-Level SMI Recovery Preserves Declared Semantics

## Context

`src/protocol/smi` is FlowSeer's production SMIv1/SMIv2 parser. Its model keeps
the meaning written in each declaration, and its recovery boundary lets later
declarations survive a malformed neighbour.

The gapped `BITS` fixture is a small example of both properties:

```text
SYNTAX BITS { alpha(0), gamma(4), delta(7) }
```

The semantic expectation records the same declared positions:

```text
syntax-members TEST-BITS-MIB.features alpha(0) gamma(4) delta(7)
```

Each member reaches the model as an `smi.Member` with its declared `Number`.
`mibgen` emits that number as the `snmp.BitPos` value. It does not reconstruct
positions from declaration order.

## Guidance

Treat declaration-level recovery as a defining parser contract. A malformed
declaration should cost that declaration while the parser continues with the
rest of the module. Unterminated strings, unterminated comments, a missing
module header, and configured resource limits are the file-level exceptions.

Keep source semantics explicit in the model. For `BITS`, the number belongs to
the member even when positions are sparse. Code generators should consume that
number directly rather than infer it from a slice index.

Evaluate parser dependencies against the corpus and the required failure
boundary. The replacement decision rests on whether one bad declaration
prevents useful declarations from loading. An isolated syntax or conversion
defect supplies supporting evidence, but does not measure that property.

Keep comparison dependencies outside the production module. `gosmi` remains in
the standalone `src/protocol/smi/differential` module as a reference
implementation. The SNMP benchmark uses the same module-boundary pattern for
its own external comparand.

## Why This Matters

The previous parser made the failure boundary too large. The committed gosmi
census classifies 1,680 corpus files as 1,567 loaded, 35 failed, and 78
panicked. One pinned panic comes from the valid empty-BITS default
`DEFVAL { { } }`. A parser that stops at the first error can discard valid
declarations after the fault, even when the application could use them.

The historical BITS defect shows the separate semantic risk. Gosmi parsed
member numbers but dropped them during conversion, so its model reported zero
for each member. The differential suite keeps that divergence explicit. The
current parser retains the numbers, and `emit_bits.go` reads them.

`TestConfiguredModulesFitGosmiBitsReconstruction` checks how far that
reconstruction reaches across the modules configured for `mibgen`. Most use
BITS shapes the gosmi member list can reconstruct; LCOS-MIB does not, since it
numbers its WLAN capability masks from the top bit down, so the differential
suite cannot check those declarations against gosmi. Such a module is accepted
by adding it to the test's `gappedBitsModules` allowlist; a gap in any other
module fails the test, and a listed module that stops declaring gapped BITS
fails it too, so configuring a new module with gapped BITS is a visible
decision rather than a log line. The test says nothing about the wider corpus.

## When to Apply

- Review recovery boundaries when adding grammar productions or changing parser
  synchronization.
- Preserve declared numbers when changing `smi.Member`, type resolution, or
  BITS emission.
- Use corpus outcomes when deciding whether to adopt, fork, or replace an SMI
  parser.
- Keep differential-only and benchmark-only dependencies in their standalone
  modules.

## Examples

The focused production checks are:

```sh
go test ./src/protocol/smi \
  -run 'TestSemanticFixtures|TestVendoredEmptyBitsDefaultsResolve'
```

The semantic fixture pins positions `0`, `4`, and `7`, and the vendored test
pins recovery of empty-BITS defaults. There is currently no end-to-end
`mibgen` golden fixture with gapped BITS. The parser fixture and the emitter's
direct use of `Member.Number` cover the two ends separately.

Historical comparator behavior lives in the isolated module:

```sh
cd src/protocol/smi/differential
go test ./... \
  -run 'TestBitsNumberingDivergencePreservesMembers|TestPanickingModuleIsRecordedAndTheRunContinues|TestConfiguredModulesFitGosmiBitsReconstruction'
```

## Related

- [`src/protocol/smi` package contract](../../../src/protocol/smi/doc.go)
- [`smi.Member` model](../../../src/protocol/smi/model.go)
- [`mibgen` BITS emission](../../../src/protocol/snmp/cmd/mibgen/emit_bits.go)
- [Gapped BITS semantic fixture](../../../src/protocol/smi/testdata/semantic/bits-numbering/expect.txt)
- [Gosmi differential census](../../../src/protocol/smi/differential/testdata/census.txt)
