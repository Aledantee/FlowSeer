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

In `src/protocol/yang/cmd/yanggen/emit_module.go:156-172`, `emitNode` claims the
node's struct name, claims its companions, and only then claims its children:

```go
func (em *moduleEmitter) emitNode(e *goyang.Entry, parentStruct string, path []pathSeg, ancestors []ancestorList) error {
	structName := em.structName(e, parentStruct)
	nodePath := append(append([]pathSeg{}, path...), em.segFor(e))

	em.claimCompanions(e, structName, nodePath, ancestors)

	children := dataChildren(e)
	fieldScope := newNameScope()

	childStructs := make(map[string]string)
	for _, c := range children {
		if isDataDir(c) {
			childStructs[c.Name] = em.structName(c, structName)
		}
	}
	// ...
```

In `src/protocol/yang/cmd/yanggen/emit_module.go:501-512`, `claimCompanions`
reserves companion names:

```go
func (em *moduleEmitter) claimCompanions(e *goyang.Entry, structName string, nodePath []pathSeg, ancestors []ancestorList) {
	em.schemaVar(structName)
	if e.IsList() && len(strings.Fields(e.Key)) > 0 {
		em.scope.claim(structName+"Key", "key:"+structName)
		em.scope.claim(structName+"Descriptor", "desc:"+structName)
		if len(ancestors) > 0 {
			em.scope.claim(structName+"FlatRow", "flat:"+structName)
		}
	} else if !e.IsList() && len(nodePath) == 1 {
		em.scope.claim(structName+"Descriptor", "desc:"+structName)
	}
}
```

The helper `schemaVar` (`emit_module.go:526-528`) returns
`em.scope.claim(structName+"Schema", "schema:"+structName)`. When emitting the
`var` declaration and all downstream references, callers invoke
`em.schemaVar(structName)` instead of formatting `structName + "Schema"`.

If a sibling container named `server-schema` claims `ServersServerSchema` first
as a struct type, `em.schemaVar` receives a deterministic disambiguation suffix
(such as `ServersServerSchemaX4d76e3`). Both compile cleanly without
redeclaration errors:

```go
type ServersServerSchema struct {
	Note *string
}

var ServersServerSchemaX4d76e3 = yang.NewSchema( /* ... */ )
```

## Evidence

- `src/protocol/yang/cmd/yanggen/emit_module.go:160`: `claimCompanions` is
  invoked prior to inspecting and claiming `childStructs`.
- `src/protocol/yang/cmd/yanggen/emit_module.go:526-528`: `schemaVar` claims
  `structName+"Schema"` through `em.scope.claim`.
- `src/protocol/yang/cmd/yanggen/emit_test.go:195-283`: `TestEmitNoUnderscores`
  parses generated golden output and confirms `ServersServerKey` and
  `ServersSchema` exist, while verifying that sibling collision resolves
  deterministically (`var ServersServerSchema` does not overwrite
  `type ServersServerSchema`).
- `src/protocol/yang/cmd/yanggen/testdata/modules/fixture-main.yang:73-83`:
  `container server-schema` and `container schema` verify companion/child
  disambiguation.

## What It Does Not Cover

This pattern does not apply to code generators that preserve full path
separators or qualify companion types within separate Go packages. It also does
not cover field-level naming within a struct, which is isolated to an
independent per-struct field name scope.
