---
title: A Byte Golden Cannot Show That a Dedup Key Term Matters
date: 2026-09-27
category: conventions
module: src/protocol/yang/cmd/yanggen
problem_type: convention
component: code_generation
severity: high
applies_when:
  - "Adding or reviewing an equivalence key that lets a code generator emit one shared type, schema, or value for several inputs (shape sharing, interning, deduplication)"
  - "Mutation-testing a key-building function and seeing only a byte-for-byte golden comparison fail"
  - "Deciding which fixtures a generator needs before the key may drop, add, or reorder a term"
related_components: [yang_library, mibgen]
tags: [code-generation, golden, dedup, mutation-testing, yanggen]
---

# A byte golden cannot show that a dedup key term matters

## The situation

yanggen emits one Go struct and one `Schema` for every set of data nodes with
equal shape keys. The key hashes the node's kind, name, module, list keys, and
each child's name, Go name, module, and leaf type
(`src/protocol/yang/cmd/yanggen/naming.go:293-296`):

```go
raw := fmt.Sprintf("kind=%s;name=%s;module=%s;keys=%s;children=[%s]",
	kind, e.Name, mod, strings.Join(keys, ","), strings.Join(childSigs, "|"))
sum := sha256.Sum256([]byte(raw))
key := hex.EncodeToString(sum[:])
```

A term missing from the key merges two nodes that decode differently, and one of
them loses data. During the phase 1 review, removing the leaf type, a child's
module, or the node's module from the key failed `TestEmitFixtureGolden` and
nothing else. That looked like coverage. It was not: the fixture had no pair of
nodes that differed only in that term, so no two shapes merged. The golden
failed because clash suffixes derive from the key hash
(`naming.go:195`, `name = fmt.Sprintf("%sX%s", want, hex.EncodeToString(sum[:3]))`),
and any change to the key renames `AlarmStateXa0728a` and its schema var
(`testdata/golden/fixture/fixturemain/fixturemain.go:38,43`).

## The rule

Treat a golden failure under a key mutation as no evidence. For every term the
key holds, add a fixture pair that is identical except in that term, and assert
on freshly emitted output that the pair got two types. Also assert, on the
committed output, that each member decodes its own wire form.

- Assert on the emitter's output, not only on the committed goldens. A test that
  reads the goldens compiles the old output, so it cannot see an emitter
  mutation.
- Put one differing property in each pair. A pair that differs in two terms
  still separates when one term is dropped, which is the refusal-test trap in
  [a refusal test needs an input only the refusal rejects](a-refusal-test-needs-an-input-only-the-refusal-rejects.md).
- Children inherit their parent's namespace, so a pair that differs in a
  child's module also differs in the child's key. To isolate the node's own
  module, the pair needs empty containers, one of them augmented in.
- A term that only splits shapes (over-specification) costs size, not data, so
  the key may carry more than the codecs read. The test covers every term the
  codecs read.

## Example

The fixture's `shape-probe` container holds same-named pairs that differ in one
term each (`src/protocol/yang/cmd/yanggen/testdata/modules/fixture-main.yang:134-180`).
`fixture-aug` augments the leaf of `by-module-b/slot` and the empty
`by-owner-b/flag`. The emit-level test parses fresh output and compares field
types (`src/protocol/yang/cmd/yanggen/emit_test.go:262-267`):

```go
for _, pair := range []struct{ property, a, b, field string }{
	{"leaf type", "ByTypeA", "ByTypeB", "Setting"},
	{"presence", "ByPresenceA", "ByPresenceB", "Marker"},
	{"child module", "ByModuleA", "ByModuleB", "Slot"},
	{"node module", "ByOwnerA", "ByOwnerB", "Flag"},
} {
```

Dropping each term now fails with a semantic message, such as "leaf type:
ByTypeA.Setting and ByTypeB.Setting share type *Setting". The wire test
(`golden_roundtrip_test.go:272-321`) decodes `{"slot":{"fixture-aug:value":7}}`
through `ByModuleBSchema`, and the same payload leaves `ByModuleA`'s leaf empty.

## What it does not cover

It does not say which terms a key needs; read the runtime's schema types for
that. For the same completeness question on a convergence fingerprint compared
across steps, see
[a state fingerprint used as a convergence oracle lies when it omits a resolution axis](a-convergence-fingerprint-lies-when-it-omits-a-resolution-axis-or-keeps-a-timer.md).
