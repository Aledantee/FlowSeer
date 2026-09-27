---
title: Claim Derived Companion Symbols in Scope Before Child Nodes
date: 2026-09-25
category: architecture-patterns
module: src/protocol/yang/cmd/yanggen
problem_type: architecture_pattern
component: code_generation
severity: medium
applies_when:
  - "flattening hierarchical schema hierarchies into a single package namespace"
  - "deriving companion types or variables (schemas, keys, descriptors) from a node name"
  - "resolving identifier collisions between generated companions and child or sibling nodes"
related_components:
  - yang_library
tags: [code-generation, yanggen, naming, collision-resolution, name-scope]
---

# Claim Derived Companion Symbols in Scope Before Child Nodes

## Context

When a code generator flattens a nested schema tree into a single Go package
namespace, a node's struct name is a run of its ancestors' names. In `yanggen`
the names are shortest unique suffixes: the list in
`container servers { list server { ... } }` becomes `ServersServer` in the
fixture, because its schema var `ServerSchema` clashes with the sibling
container `server-schema` and both grow a segment.

Generators also emit companion symbols. A struct has a schema variable
(`ServersServerSchema`). Each list instance has a key struct, a descriptor
function, and, when nested, a flat-row struct. These take the shortest unique
suffix of the instance's own path, so the fixture's list has `ServerKey` and
`ServerDescriptor`.

Without separators, a companion name and a node name can be the same string.
If a sibling's or child's joined name equals a companion's, both map to one Go
identifier and the package does not compile.

## The Rule

1. **Resolve every name in one scope.** Structs, schema vars, keys, flat rows,
   descriptors, and identities are all claimants in the same package scope.
   Clashing claimants grow by one ancestor segment together until no two
   want the same name. A clash is judged across all kinds, so the struct
   `ServerKey` and the key `Server` + `Key` count as a clash.
2. **Claim in one sorted pass after growth.** When growth runs out of
   segments, the claim order decides who keeps the clean name and who takes
   the `X<hash>` suffix. Claims are sorted by depth first, so a node and its
   companions claim before any deeper child node. At equal depth, kind decides:
   `kindStruct`, then `kindListKey`, `kindListFlatRow`, `kindListDescriptor`,
   `kindContainerDescriptor`, `kindSchema`, and `kindIdentity`. A sibling
   struct therefore keeps its name over a companion schema var at the same
   depth. Ties fall to the wanted name, then the schema path.
3. **Route declarations and references through the same claim.** Every
   emission site for a companion symbol, both where it is declared and where
   other generated code references it, looks the name up in the resolved scope
   instead of re-deriving an unsuffixed string.

## Working Example

`resolvePackageNaming` (`src/protocol/yang/cmd/yanggen/naming.go:454-565`)
registers every shape, list instance, top-level container, and identity as a
claimant. A shape claims its struct and its schema var at the same depth:

```go
ent := &claimantEntity{
	candidates: sCopy.candidates,
	getSymbols: func(base string) []claimSpec {
		return []claimSpec{
			{
				wanted:        base,
				kind:          kindStruct,
				depth:         len(sCopy.instances[0].segments),
				tieBreak:      sCopy.instances[0].entry.Path(),
				discriminator: sCopy.key,
			},
			{
				wanted:        base + "Schema",
				kind:          kindSchema,
				depth:         len(sCopy.instances[0].segments),
				tieBreak:      sCopy.instances[0].entry.Path(),
				discriminator: "schema:" + sCopy.key,
			},
		}
	},
}
```

`resolveFairGrowth` (`naming.go:105-172`) grows clashing claimants, then sorts
the final claims by depth, kind, wanted name, and path (`naming.go:154-165`)
and claims them into the scope in that order (`naming.go:167-169`).

The emitter reads names back from the scope by discriminator
(`src/protocol/yang/cmd/yanggen/emit_module.go:553-575`):

```go
func (em *moduleEmitter) schemaVar(shapeKey string) string {
	return em.scope.byPath["schema:"+shapeKey]
}
```

In the fixture, the sibling container `server-schema` wants the struct name
`ServersServerSchema`, the same string as the `server` list's schema var. Both
are at full length and at the same depth, so the struct wins on kind and the
schema var takes the suffix:

```go
type ServersServerSchema struct {
	Note *string
}

var ServersServerSchemaX9bc561 = &yang.Schema{ /* ... */ }
```

## Evidence

- `src/protocol/yang/cmd/yanggen/naming.go:154-165`: the final claim sort, by
  depth, then kind, then wanted name, then path.
- `src/protocol/yang/cmd/yanggen/naming.go:461-485`: a shape registers its
  struct and schema var as one claimant.
- `src/protocol/yang/cmd/yanggen/emit_module.go:553-575`: `structName`,
  `schemaVar`, and the list and descriptor helpers look names up in
  `em.scope`.
- `src/protocol/yang/cmd/yanggen/naming_test.go:83-124`:
  `TestFairGrowthReversedOrderByteIdentity` resolves the `server` and
  `server-schema` clash in both input orders and fails if the claim sort is
  removed.
- `src/protocol/yang/cmd/yanggen/emit_test.go:368-487`: `TestEmitNoUnderscores`
  parses the golden output and confirms `ServerKey` and `ServersSchema` exist
  and that `var ServersServerSchemaX…` does not overwrite
  `type ServersServerSchema`.
- `src/protocol/yang/cmd/yanggen/testdata/modules/fixture-main.yang:77-88`:
  `container server-schema` and `container schema` exercise the
  companion-against-node clashes.

## What It Does Not Cover

This pattern does not apply to code generators that keep path separators or
put companion types in separate Go packages. It also does not cover field
names within a struct, which use their own per-struct scope.
