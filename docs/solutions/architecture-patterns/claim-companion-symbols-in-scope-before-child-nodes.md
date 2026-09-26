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
namespace, parent identifiers concatenate to form struct names. In `yanggen`,
the nested hierarchy `container servers { list server { ... } }` becomes the Go
struct `ServersServer`.

Generators also emit companion symbols derived from those structs: schema
metadata variables (`ServersServerSchema`), list key structs
(`ServersServerKey`), descriptor functions (`ServersServerDescriptor`), and
flat-row representations (`ServersServerFlatRow`).

Removing separator characters (such as underscores) creates ambiguity between
derived companion names and potential child or sibling schema nodes. If a child
or sibling node's path concatenates to the exact name of a companion symbol,
both map to the same Go identifier in the package.

## The Rule

1. **Claim companions immediately after the struct name.** As soon as a node
   claims its struct identifier in the package `nameScope`, claim all companion
   symbols derived from that struct before recursing into child nodes. This
   ensures companion symbols (which form the primary caller API) keep their
   predictable names over deeper child nodes.
2. **Sibling struct names precede companion names.** Sibling struct names
   claimed in an earlier iteration take precedence over a list or container's
   companions. When a sibling struct already holds the clean name, the companion
   must take the collision suffix.
3. **Route declarations and references through the same claim.** Every emission
   site for a companion symbol—both where the symbol is declared and where other
   generated code references it—must look up the claimed name through the scope
   registry rather than re-computing an unsuffixed string.

## Working Example

In `src/protocol/yang/cmd/yanggen/naming.go:470-602`, `resolvePackageNaming`
registers all shapes, list instances, top containers, and identities as claimant
entities, and resolves names through fair growth:

```go
ent := &claimantEntity{
	id:         "shape:" + sCopy.key,
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

In `src/protocol/yang/cmd/yanggen/naming.go:190-205`, `resolveFairGrowth` sorts all
candidate symbols by `kind` prior to claiming into the package `nameScope`. Struct
types (`kindStruct`) take precedence over companion schema variables
(`kindSchema`).

When emitting Go code, `moduleEmitter` queries the pre-resolved table via helper
methods in `src/protocol/yang/cmd/yanggen/emit_module.go:475-495`:

```go
func (em *moduleEmitter) schemaVar(shapeKey string) string {
	return em.names.structToSchema[shapeKey]
}
```

If a sibling container named `server-schema` claims `ServersServerSchema` as a
struct type, the companion schema variable for `ServersServer` yields and receives
a deterministic disambiguation suffix (such as `ServersServerSchemaX9bc561`). Both
compile cleanly without redeclaration errors:

```go
type ServersServerSchema struct {
	Note *string
}

var ServersServerSchemaX9bc561 = &yang.Schema{ /* ... */ }
```

## Evidence

- `src/protocol/yang/cmd/yanggen/naming.go:190-205`: `resolveFairGrowth` orders
  claims by kind (`kindStruct` before `kindSchema`).
- `src/protocol/yang/cmd/yanggen/naming.go:500-517`: `resolvePackageNaming` registers
  shapes and their companion schemas.
- `src/protocol/yang/cmd/yanggen/emit_module.go:475-495`: `schemaVar` and list
  descriptor helpers look up claimed names through `em.names`.
- `src/protocol/yang/cmd/yanggen/emit_test.go:200-280`: `TestEmitNoUnderscores`
  parses generated golden output and confirms `ServersServerKey` and
  `ServersSchema` exist, while verifying that sibling collision resolves
  deterministically (`var ServersServerSchemaX` does not overwrite
  `type ServersServerSchema`).
- `src/protocol/yang/cmd/yanggen/testdata/modules/fixture-main.yang:73-83`:
  `container server-schema` and `container schema` verify companion/child
  disambiguation.

## What It Does Not Cover

This pattern does not apply to code generators that preserve full path
separators or qualify companion types within separate Go packages. It also does
not cover field-level naming within a struct, which is isolated to an
independent per-struct field name scope.
