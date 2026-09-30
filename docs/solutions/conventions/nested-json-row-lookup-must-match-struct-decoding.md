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
the module-`b` row schema. If the parent has only the grouped `b:c`, a bare
`c` selects that group. If two groups own `c` and no plain `c` exists,
decoding returns an ambiguity error naming both modules.

## The rule

Resolve every candidate member through the struct decoder's field-aware
qualification rule. A grouped field first accepts `module:name`. It also
accepts a bare name when no ordinary field and exactly one group in the parent
owns that name. Two groups with the same bare name produce an ambiguity error.
An ordinary field may use its bare name, with the module-qualified spelling
accepted too. After finding a field, match a target list by both local name and
owning module. Recurse through containers only.

`lookupJSONField` is the shared rule:

```go
// src/protocol/yang/structjson.go:213-257
func lookupJSONField(obj map[string]json.RawMessage, parent *Schema, f *Field, owner *Schema, group *Field) (json.RawMessage, bool, error) {
	name := f.Name
	if f.Child != nil {
		name = f.Child.Name
	}
	module := f.qualifiedModule(owner)
	if group == nil {
		raw, ok := lookupMember(obj, module, name)
		return raw, ok, nil
	}
	if raw, ok := obj[module+":"+name]; ok {
		return raw, true, nil
	}
	raw, ok := obj[name]
	if !ok {
		return nil, false, nil
	}

	var candidates []string
	plain := false
	if err := walkFields(parent, func(candidate *Field, candidateOwner *Schema, candidateGroup *Field) error {
		candidateName := candidate.Name
		if candidate.Child != nil {
			candidateName = candidate.Child.Name
		}
		if candidateName != name {
			return nil
		}
		if candidateGroup == nil {
			plain = true
			return nil
		}
		candidates = append(candidates, candidate.qualifiedModule(candidateOwner))
		return nil
	}); err != nil {
		return nil, false, err
	}
	if plain {
		return nil, false, nil
	}
	if len(candidates) == 1 {
		return raw, true, nil
	}
	return nil, false, errs.Msgf("ambiguous bare JSON member %q matches grouped fields from modules %s", name, strings.Join(candidates, ", "))
}
```

The nested walker applies it before checking the target list's owner:

```go
// src/protocol/yang/nested.go:311-320
raw, ok, err := lookupJSONField(obj, level, f, owner, group)
if err != nil {
	return err
}
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

- `src/protocol/yang/structjson.go` accepts a unique bare grouped member,
  rejects an ambiguous one, and keeps the bare fallback for ordinary fields.
- `src/protocol/yang/nested.go` uses that helper while walking fields, checks the
  child name and owning module, and descends only through non-list containers.
- `src/protocol/yang/structcodec_test.go` covers unique bare grouped members,
  plain-field precedence, and ambiguity across modules.
- `src/protocol/yang/nested_test.go` proves that a unique bare grouped
  container is decoded and that ambiguity names both grouped modules.
- `src/protocol/yang/nested_test.go` decodes the same fixtures with
  `UnmarshalJSON7951Struct` and `DecodeJSONNested`, then compares the rows each
  module receives.

## What this does not cover

The nested decoder cannot distinguish two nodes with the same name and module
under one ancestor when their paths are otherwise identical. It also does not
change XML namespace matching, direct list decoding, or the generator's choice
of which YANG nodes to emit.
