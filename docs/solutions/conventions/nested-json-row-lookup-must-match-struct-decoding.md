---
title: A Nested JSON Row Lookup Must Match the Struct Decoder's Module Qualification
date: 2026-09-30
last_verified: 2026-09-30
category: conventions
module: src/protocol/yang
problem_type: bug
component: yang
severity: high
symptoms:
  - "DecodeJSONNested returns a row from a same-named list owned by another YANG module"
  - "Nested row extraction disagrees with UnmarshalJSON7951Struct for the same JSON object"
  - "A bare JSON member makes a grouped container appear populated even though struct decoding leaves the group nil"
root_cause: "The nested walker used a generic qualified-or-bare lookup and matched names without checking the field's owning module. A bare member from one module could therefore satisfy a row or container from another module."
resolution_type: code_fix
applies_when:
  - "Adding or reviewing DecodeJSONNested or another schema-guided JSON walker under src/protocol/yang"
  - "A nested-list path can reach same-named nodes from multiple YANG modules or a Field.Group"
  - "Comparing nested row extraction with UnmarshalJSON7951Struct when bare and module-qualified member names coexist"
related_components: [yanggen, netconf, restconf, gnmi]
tags: [yang, json, namespaces, nested-lists, codec]
---

# A nested JSON row lookup must match struct decoding's module qualification

## The situation

`DecodeJSONNested` walks an ancestor object to find the next list. A generic
lookup that tries `module:name` and then `name` can select a bare list owned by
another module. The resulting row is real JSON, so the decoder succeeds while
returning data that `UnmarshalJSON7951Struct` would leave in a different field
or ignore.

Grouped fields make the collision common. A parent in module `a` can contain a
plain `c` and a grouped `b:c`. In this payload, the `a` row belongs to `c` and
the `b` row belongs to `b:c`:

```json
{"a:outer":[{"c":{"row":[{"id":"A"}]},"b:c":{"row":[{"id":"B"}]}}]}
```

The nested decoder must return `A` for the module-`a` row schema and `B` for
the module-`b` row schema. If only the bare `c` exists, asking for module `b`
must return no rows.

## The rule

Resolve every candidate member through the struct decoder's field-aware
qualification rule. A grouped field requires `module:name`. An ordinary field
may use its bare name, with the module-qualified spelling accepted too. After
finding a field, match a target list by both local name and owning module.
Recurse through containers only. Do not fall back to a bare name before the
qualified candidate has been considered, and do not match by local name alone.

`lookupJSONField` is the shared rule:

```go
// src/protocol/yang/structjson.go:209-222
if group != nil {
	raw, ok := obj[module+":"+name]
	return raw, ok
}
return lookupMember(obj, module, name)
```

The nested walker applies it before checking the target list's owner:

```go
// src/protocol/yang/nested.go:301-323
raw, ok := lookupJSONField(obj, f, owner, group)
if f.List {
	if f.Child.Name == next.Name && f.qualifiedModule(owner) == next.moduleName() {
		found, foundOK = raw, true
	}
}
```

This keeps nested extraction aligned with the struct value. It also prevents a
bare field encountered earlier in schema order from hiding a later qualified
field from the requested module.

## Evidence

- `src/protocol/yang/structjson.go:209-222` makes grouped members require
  their module-qualified JSON name and keeps the bare fallback for ordinary
  fields.
- `src/protocol/yang/nested.go:301-340` uses that helper while walking fields,
  checks the child name and owning module, and descends only through non-list
  containers.
- `src/protocol/yang/nested_test.go:282-374` proves that a bare container from
  one module cannot satisfy a grouped target and that a conforming module match
  wins when both same-named containers are present.
- `src/protocol/yang/nested_test.go:453-512` decodes the same fixtures with
  `UnmarshalJSON7951Struct` and `DecodeJSONNested`, then compares the rows each
  module receives.

## What this does not cover

The nested decoder cannot distinguish two nodes with the same name and module
under one ancestor when their paths are otherwise identical. It also does not
change XML namespace matching, direct list decoding, or the generator's choice
of which YANG nodes to emit.
