---
title: Generated Binding Size Phase 1 - yanggen - Plan
type: perf
date: 2026-09-26
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: code
parent: docs/plans/2026-09-26-1113-perf-generated-binding-size-plan.md
---

# Generated Binding Size Phase 1 - yanggen - Plan

## Goal

`generated/go/yang` shrinks to at most half of its 205,604,350-byte baseline
and decodes, diffs, merges, and addresses the same data. The means:

- yanggen emits one Go type and one Schema per distinct subtree shape and gives
  it the shortest name that is unique in the package.
- Module identity is corrected to follow RFC 7950 and RFC 7951, and is shared
  as one value per module.
- Nested-list descriptors are one call to a generic runtime helper.

This phase is wrong if two schema paths with the same shape key decode or
encode differently, since shared types assume they do not. It is also wrong if
the U3 checkpoint regeneration lands above the size target. In either case,
stop before U4 and report the measurement.

## Decisions

The parent plan's decisions apply. This phase adds the following.

- **Module qualification follows the instantiating module.** `moduleOf`
  (`src/protocol/yang/cmd/yanggen/emit_module.go:555-563`) returns
  `Entry.InstantiatingModule()` (goyang v1.6.3, `pkg/yang/entry.go:1431`)
  instead of the root of the defining node. Why: RFC 7950 §7.13 binds grouping
  nodes "to the namespace of the current module" at the `uses`, and §7.17 puts
  augmented nodes in the augmenting module's namespace. RFC 7951 §4 then
  qualifies a JSON member with the name of the module that owns the namespace.
  Today 240 `ciscoiosxenative` nodes pair `Module: "Cisco-IOS-XE-l2vpn"` with the
  atm namespace (`ciscoiosxenative_p010.go:1826-1828`), so their JSON member
  names are qualified with the wrong module. After the fix, a module name and
  its namespace always agree.
- **One module value per module.** `yang.Module{Name, Namespace string}` is a
  new type. `Schema.Module` and `Field.Module` become `*Module`, and the two
  `Namespace` fields go away. A nil `Field.Module` inherits the schema's
  module, and a nil `Schema.Module` reads as empty strings, the way the zero
  strings read today. Every read goes through nil-safe unexported accessors.
  yanggen emits `var module<Ident> = &yang.Module{...}` once per package for
  each module the package references. The var is unexported, so it cannot
  collide with an exported node name (`naming.go:20-22`). Why: the fix above
  makes the pair consistent. With one value per module, 17 MB of repeated
  strings become one line per node.
- **One type per shape.** yanggen computes a shape key bottom-up for each data
  node. The key covers the node's kind (container, presence container, or
  list), its YANG name, its module, its key leaves, and each child in schema
  order. For a child that means its name, its Go field name, its module, its
  leaf type (including enum, bits, union, decimal64, and identity details),
  whether it is a leaf-list, and the shape key of its child node. Nodes with
  equal keys share one struct and one `…Schema` var. Why: in
  `ciscoiosxenative`, 101,665 node structs have 12,885 distinct shapes (see the
  parent plan's Baseline). The shared codecs read only the Schema, so equal
  Schemas decode identically.
- **Names are the shortest unique suffix.** A shape's candidate names are the
  suffixes of the longest common suffix of its instances' Go-name segment
  sequences. Choices and cases are skipped, as today. A list instance's
  candidates, which name its `…Key`, `…FlatRow`, and `…Descriptor`, are the
  suffixes of its own path. Resolution runs over the whole package, with every
  companion name (`X`, `XSchema`, `XKey`, `XFlatRow`, `XDescriptor`,
  `Identity…`) in one set:
  - Every name starts at its shortest candidate.
  - Every member of a clashing group grows by one segment at a time, all
    together, until the names are unique. This makes the result independent
    of traversal order.
  - A group that runs out of segments falls back to the existing
    `X<hash>` suffix. For a shape, the hash is taken over its shape key; for
    a list instance, over its path.

  Why: the user directed Go naming, where the package is part of the
  identity. Measured on today's output, `openconfiginterfaces` names shrink
  from an average of 54 to 18 characters, for example
  `InterfacesInterfaceSubinterfacesSubinterface` becomes `Subinterface`. The
  catch is that adding a vendor revision can rename a type, which is accepted
  before the first stable release.
- **Comments.** A node type reads `X is the <module> node <path>.` when it has
  one instance. A shared type reads
  `X is the <module> node shape instantiated at <n> schema paths, such as <first path in sorted order>.`
  Companion comments carry no path. They read
  `XSchema describes X for the generic codecs.` and so on, the path-free forms
  the emitter already uses for Key, FlatRow, and Descriptor
  (`emit_module.go:366,396,422,474`). Why: each exported name keeps its doc
  comment, and the path appears once.
- **Nested-list codec.** A new generic helper goes next to `StructRowCodec` in
  `src/protocol/yang/structops.go`:

  ```go
  func NestedRowCodec[Entry, Row any, Key comparable](
  	chain []*Schema,
  	row func(ancestors [][]KeyValue, entry Entry) Row,
  	entry func(*Row) *Entry,
  	key func(*Row) Key,
  ) RowCodec[Row, Key]
  ```

  It builds the codec like this:
  - `DecodeXML` and `DecodeJSON` are `DecodeXMLNested` and `DecodeJSONNested`
    followed by `row`.
  - `Equal` is `EqualStructs[Row]`.
  - `Merge` applies `MergeStructs` over `entry(&base)` with the schema
    `chain[len(chain)-1]`.

  Why: the two emitted decode closures take 21 MB and differ only in the
  decode call.
- **Paths that cannot fail.** `yang.In(m *Module, names ...string) []Segment`
  returns one segment per name, the first qualified by `m` and the rest
  inheriting it. `yang.JoinPath(parts ...[]Segment) Path` concatenates the
  segments. Descriptors emit
  `yang.JoinPath(yang.In(moduleNative, "native", "router"), yang.In(moduleBGP, "bgp", "address-family", ...))`.
  Why: a descriptor needs every container on its path, and the list-only
  `chain` does not have them (`emit_module.go:156-158, 426-433`). A
  constructor with no failure mode involves no panic, so the panic policy's
  Handled and Proven clauses (`docs/code-style.md:261-290`) never come into
  play.
- **Field layout.** The emitter writes each `yang.Field` as one keyed literal on
  one line, and the list with
  `Custom(jen.Options{Open: "{", Close: "}", Separator: ",", Multi: true}, ...)`
  so each field sits on its own line. Why: `jen.Values` would put the whole
  list on one line (`emit_module.go:274`; compare golden
  `fixturemain.go:245`). A field whose union or decimal64 `Type` literal does
  not fit may stay multi-line.
- **Version bump.** `generatorVersion` becomes `yanggen-5`
  (`src/protocol/yang/cmd/yanggen/lockfile.go:17`). Why: the lockfile marks
  every module for regeneration when the version changes (`lockfile.go:102-110`).
- **One plan.** The phase stays whole: every unit touches `src/protocol/yang`
  or its generator, so it is a single dependency cluster.

## Requirements

1. A schema whose `Module` is `&Module{Name: "m", Namespace: "urn:m"}`, with a
   field whose `Module` is nil, decodes `<c xmlns="urn:m"><leaf>1</leaf></c>`
   as before. A field whose `Module` is `&Module{Name: "aug", Namespace: "urn:aug"}`
   decodes JSON member `aug:leaf` and, as today, bare `leaf`. It does not
   match an XML element in `urn:m`. A `Schema` with a nil `Module` decodes like
   one with empty module strings.
2. `yang.JoinPath(yang.In(a, "x", "y"), yang.In(b, "z"))`, where `a` is
   `{Name: "a", Namespace: "urn:a"}` and `b` is `{Name: "b", Namespace: "urn:b"}`,
   renders `String()` as `/a:x/y/b:z` and gives segment namespaces `urn:a`,
   empty, and `urn:b`.
3. `NestedRowCodec` over a two-level nested fixture list returns rows equal to
   those built by hand from `DecodeXMLNested` and `DecodeJSONNested`. A merge
   of an update that sets one leaf keeps the ancestor keys and every other
   leaf of the base.
4. In a new fixture module, a grouping from module `fixture-grp` is used
   under two containers of `fixture-main`. The generated code has one struct
   for the two instances, qualified with module `fixture-main` and its
   namespace. A JSON payload with member `fixture-main:<leaf>` under either
   container decodes into that struct.
5. Two fixture nodes whose own names clash (`config` under two different
   parents) receive distinct names grown by one ancestor segment each, for
   example `AConfig` and `BConfig`. Emitting twice with the module list in
   reverse order produces byte-identical output.
6. In the fixture goldens, no companion declaration's comment contains a
   `/`-separated path. Companions are selected by declaration kind: a `var`
   whose value is `&yang.Schema`, a `type` the emitter claimed as a Key or
   FlatRow, or a `func` returning `yang.ListDescriptor`. Every exported
   identifier has a doc comment.
7. At the U3 checkpoint, a regeneration into a scratch directory puts
   `ciscoiosxenative` at or below 85,052,199 bytes, half of its 170,104,399.
   At the end of U4, `generated/go/yang` is at or below 102,802,175 bytes, half
   of its baseline.

## Out of scope

- `yang.Identity` values and their `Module` string.
- `Path` and `Segment` keep string modules, because `ParsePath` and the gNMI
  conversion use them.
- mibgen, which is phase 2.

## Units

### U1. Runtime: module values, path constructors, nested codec
Files:
- `src/protocol/yang/schema.go`, `nested.go`, `structjson.go`,
  `structxml.go`, `structops.go`, `path.go`
- any other non-test file in `src/protocol/yang` that reads `Schema.Module`,
  `Schema.Namespace`, `Field.Module`, or `Field.Namespace`
- the literal-building tests `nested_test.go`, `structcodec_test.go`, and
  `watch_test.go` in `src/protocol/yang`
- the package README or doc comment

After: none

Change: the `Module` type, the pointer fields, and the nil-safe accessors
from Decisions. `In` and `JoinPath` are in `path.go`, and `NestedRowCodec` is
in `structops.go`. The doc describes module sharing.

Tests:
- existing literal tests move to keyed `&yang.Module{...}`
- `schema_test.go` table cases for Requirement 1, covering XML and JSON
- `path_test.go` for Requirement 2
- `structops_test.go` for Requirement 3

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/yang`

### U2. Emitter: instantiating-module qualification
Files:
- `src/protocol/yang/cmd/yanggen/emit_module.go` (`moduleOf`, `namespaceOf`)
- a new `src/protocol/yang/cmd/yanggen/testdata/modules/fixture-grp.yang`
- `fixture-main.yang`
- `testdata/fixture.yaml`
- `testdata/golden/**`
- `emit_test.go`
- `golden_roundtrip_test.go`

After: none

Change: module and namespace both come from the instantiating module. The
fixture adds `fixture-grp` with a grouping that `fixture-main` uses under
two containers. The goldens are regenerated with `-update-golden`.

Tests:
- a golden round-trip case decodes JSON `fixture-main:<leaf>` and XML in
  `fixture-main`'s namespace through both instances
- an `emit_test.go` case asserts that each instance's field literal names
  `fixture-main`

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/yang/cmd/yanggen`

### U3. Emitter: shared shapes and suffix naming, with a checkpoint
Files:
- `src/protocol/yang/cmd/yanggen/naming.go`
- `emit_module.go`
- `naming_test.go`
- `emit_test.go`
- `golden_roundtrip_test.go`
- `testdata/golden/**`
- `doc.go` (the naming scheme)
- `docs/solutions/architecture-patterns/claim-companion-symbols-in-scope-before-child-nodes.md`,
  whose `emit_module.go` line citations and claim-order description this
  unit changes

After: U2

Change:
1. A pre-pass over each package computes shape keys and instances.
2. The pre-pass resolves every name by the fair-growth rule in Decisions.
   This replaces the first-come `structName` claim in `emit_module.go:515-520`.
3. Emission walks shapes, not paths: one struct and one Schema per shape,
   and per list instance one Key, FlatRow, and Descriptor that reference the
   shape's types.
4. Comments take the forms in Decisions.

Tests:
- `naming_test.go` cases for Requirement 5, including the reversed-order
  byte-identity check
- a case where growth exhausts the segments and falls back to the hash
- `emit_test.go` for Requirement 4 (one struct for both grouping instances)
- the existing surface assertions updated to the new names
- `TestEmitNoUnderscores` and `TestGoldenPackagesBuild` stay green
- checkpoint: `go run ./src/protocol/yang/cmd/yanggen -out "$TMPDIR/yang-u3"`
  (unsandboxed), then measure `ciscoiosxenative` there against Requirement 7
  and record the number in the ledger note. Above the bound, stop.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/yang/cmd/yanggen docs/solutions`

### U4. Emitter: compact literals, descriptors, and version
Files:
- `src/protocol/yang/cmd/yanggen/emit_module.go`
- `emit_type.go`
- `lockfile.go`
- `doc.go`
- `emit_test.go`
- `testdata/golden/**`

After: U1, U3

Change:
- module vars and `*Module` references
- single-line field literals in a multi-line list
- nested descriptors built by `NestedRowCodec`
- every descriptor path built with `JoinPath` and `In`
- `generatorVersion` set to `yanggen-5`

Tests:
- `TestEmitFixtureSurface` (`emit_test.go:103-104`) expects
  `yang.NestedRowCodec(` in place of the `DecodeXMLNested` and
  `DecodeJSONNested` calls
- `TestEmitAugmentModule` (`emit_test.go:148`) expects a `Module:` that
  references a `module…` var
- a new golden-parsing test checks Requirement 6 and one field per line
- the golden round trip stays green

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/yang/cmd/yanggen`

### U5. Regenerate, update consumers, measure, record the rule
Files:
- `generated/go/yang/**`, written only by
  `go run ./src/protocol/yang/cmd/yanggen -update`
- `src/protocol/netconf/test/integration/*`
- `src/protocol/restconf/test/integration/*`
- `src/protocol/gnmi/test/integration/*` (renamed symbols)
- `docs/code-style.md` (Project layout)
- this plan's outcome note

After: U1, U4

Change:
1. Regenerate the tree.
2. Point the integration tests at the new names.
3. Measure as the parent Baseline defines and record the number.
4. Add the codegen rule to `docs/code-style.md` next to the regeneration
   commands at `:431-437`.

Tests:
- `go build ./generated/go/yang/...`
- `go vet ./generated/go/yang/aruba-cx/...`
- `go vet -tags yang_integration_t4 ./src/protocol/netconf/test/integration/ ./src/protocol/restconf/test/integration/ ./src/protocol/gnmi/test/integration/`;
  those files carry build tags (`t4_lab_test.go:1`), so an untagged vet
  compiles nothing
- `go test` of `src/protocol/{netconf,restconf,gnmi}/...`
- Requirement 7's tree bound

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/yang src/protocol/netconf src/protocol/restconf src/protocol/gnmi docs/code-style.md docs/plans`

Never run `--full`, and never run golangci-lint over the whole
`generated/go/yang` tree; lint two or three sample packages at most.

Waves: U1 U2 | U3 | U4 | U5

U2, U3, and U4 all edit `emit_module.go`, which is why they form a chain.

## Verification

- The verifier is green on every changed path outside `generated/`.
- `go test -race ./src/protocol/yang/... ./src/protocol/netconf/... ./src/protocol/restconf/... ./src/protocol/gnmi/...`
- `go build ./generated/go/yang/...`, unsandboxed, as a build with no race
  tests over the tree.
- The size measurement, recorded under this title.

## Definition of done

- [ ] The verifier is green for every changed path outside `generated/`.
- [ ] The `src/protocol/yang` docs, `cmd/yanggen/doc.go`, the companion-symbols
      solution, and `docs/code-style.md` are updated in the same change.
- [ ] `generated/go/yang` is at or below 102,802,175 bytes, and the number is
      recorded.
- [ ] `status` is `implemented`, with an outcome note, and the parent's P1
      `Landed:` line carries the commit range.
- [ ] No plan labels in code or commit messages.

## Open questions

- Is the shape key complete? A YANG property that the shared codecs read but
  the key leaves out would merge two shapes that must stay apart. U3's
  implementer checks the key against every field `yang.Schema`, `yang.Field`,
  and `yang.Type` carry (`src/protocol/yang/schema.go:19-61`,
  `value.go:74-100`) and adds any that are missing.
