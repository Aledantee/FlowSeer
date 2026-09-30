---
title: YANG Augment Namespaces, Phase 1 - Runtime Group Field Kind - Plan
type: fix
date: 2026-09-30
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept after fixes
execution: code
parent: docs/plans/2026-09-30-1129-fix-yang-augment-namespaces-plan.md
---

# YANG Augment Namespaces, Phase 1 - Runtime Group Field Kind - Plan

> Implemented. 3 units, 2026-09-30T10:32:20Z to 2026-09-30T10:32:20Z.

## Goal

`src/protocol/yang` accepts a schema field that groups the children one
module augments into a parent, and every codec treats those children as
direct members of the parent on the wire. Hand-written schemas in the
package's tests prove it. No generated code changes in this phase. Stop
condition: a codec path that needs to know the group's position in the
wire order, since YANG gives augmented nodes none.

## Decisions

- The parent plan's Decisions govern.
- `Field` gains `Group bool`. A group field has `Child` set to a schema
  whose `Module` is the augmenting module and whose `Name` is empty, and
  its Go field is a pointer to the group struct. A nil pointer means the
  module contributed no data. Why: a pointer keeps an unused group off the
  wire and out of `EqualStructs` without a separate emptiness check.
- A group's fields never contain another group. Why: an augment targets a
  data node and a group is not one, so a nested group has no YANG source.
  The iterator reports it as a schema error instead of recursing.
- One iterator in `schema.go` walks a schema's fields with groups
  flattened. It reads the schema only and yields each field, the schema
  that owns its namespace (the group's schema for a grouped field), and
  the group field it sits behind or nil. A helper `groupValue(rv, group,
  alloc)` resolves the group struct on a value: without `alloc` a nil
  group yields an invalid value, with `alloc` it sets a new pointer. Why:
  `matchField` (`structxml.go:224-241`) and `decodeJSONObject`
  (`structjson.go:198-211`) find the field before they touch a value, so
  an iterator that yields values would have to allocate every group up
  front and break requirement 2.
- `mergeStructValue` treats a group as a container and needs no
  iterator. Why: its pointer-to-struct branch (`structops.go:47-60`)
  already clones and descends, which is the merge a group needs.
- JSON qualifies a member when its module differs from the enclosing
  data node's module, not from the group schema's module. Why: RFC 7951
  §4 compares against the parent data node
  (https://www.rfc-editor.org/rfc/rfc7951#section-4), and every grouped
  child belongs to a module other than its parent's.
- On JSON decode a grouped member matches only its qualified form
  `module:name`. The bare-name fallback in `lookupMember`
  (`structjson.go:187-194`) stays for the parent's own fields. Why:
  RFC 7951 §4 requires the qualified form for a grouped child, and the
  fallback would otherwise copy a parent's own `enabled-protocol` into a
  same-named group, which re-encodes as two members.

## Requirements

1. XML and JSON encode a grouped leaf at the parent's level. Example:
   `Parent{X: ptr(1), B: &ParentB{Y: ptr("v")}}` under module `a`, group
   `B` in module `b`, marshals with `MarshalXMLStruct` to
   `<parent xmlns="urn:a"><x>1</x><y xmlns="urn:b">v</y></parent>` and
   with `MarshalJSON7951Struct` to `{"x":1,"b:y":"v"}`.
2. Decoding the documents in 1 yields the same struct, with `B`
   allocated. A document without `b:y` leaves `B` nil.
3. A group may hold a child whose local name the parent or another group
   also uses, and each decodes into its own field. Example: parent leaf
   `x` in `a`, groups `B` (`urn:b`) and `C` (`urn:c`) each with leaf `x`.
   The XML `<x>0</x><x xmlns="urn:b">1</x><x xmlns="urn:c">true</x>` sets
   `X` to 0, `B.X` to 1, and `C.X` to true. The JSON
   `{"x":0,"b:x":1,"c:x":true}` does the same, and `{"x":0}` leaves `B`
   and `C` nil.
4. `VisitStructLeaves` yields grouped leaves with no group segment in
   the path. Example: visiting the value in 1 yields paths `x` and `b:y`.
5. `MergeStructs` and `EqualStructs` descend into groups. Example: merging
   `B{Y:"w"}` into a base with `B` nil sets `B`, and two values differing
   only in `B.Y` are not equal.
6. A list under a container inside a group decodes through
   `DecodeJSONNested` and `DecodeXMLNested`. Example: group `B` holding
   container `c` holding list `row` keyed by `id` yields one
   `NestedEntry` per row with its ancestor keys.
7. A group field inside a group schema is rejected with an error naming
   both fields.

## Out of scope

- `yanggen` and the generated bindings (phase 2).
- The NETCONF, RESTCONF, and gNMI libraries. They consume `Path` and the
  codecs and never walk `Schema.Fields` (grep of `src/protocol/gnmi`,
  `src/protocol/netconf`, `src/protocol/restconf`, 2026-09-30).
- Rejecting a group schema passed as a root to `MarshalXMLStruct` or
  `SubtreeDescriptor`. Phase 2 decides whether group schemas are
  exported at all.

## Units

### U1. Group field, iterator, and shared fixture
Files: src/protocol/yang/schema.go, src/protocol/yang/doc.go, src/protocol/yang/schema_internal_test.go, src/protocol/yang/schema_test.go
After: none
Change: `Field.Group` exists with its doc comment. The iterator and
`groupValue` described under Decisions live in `schema.go`. `doc.go`
describes the group field kind. `schema_test.go` defines the shared
fixture the codec tests use: modules `a`, `b`, `c`, the `Parent`,
`ParentB`, and `ParentC` types and schemas of requirement 3, and a
`ptr` helper.
Tests: `schema_internal_test.go` (package `yang`) covers the iterator's
yield order (plain fields, then each group's fields, in schema order),
`groupValue` with and without `alloc`, and the nested-group error
(requirement 7).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/yang/schema.go src/protocol/yang/doc.go src/protocol/yang/schema_internal_test.go src/protocol/yang/schema_test.go`

### U2. XML and JSON struct codecs
Files: src/protocol/yang/structxml.go, src/protocol/yang/structjson.go, src/protocol/yang/structcodec_test.go
After: U1
Change: `writeXMLElement`, `decodeXMLInto`, `matchField`, the JSON
encode loop, and `decodeJSONObject` iterate through the iterator.
`matchField` returns the owning group so the decoder allocates it
through `groupValue`. `decodeJSONObject` looks a grouped member up by
its qualified name only.
Tests: `structcodec_test.go` gains round-trip cases for requirements 1,
2, and 3, in both encodings, with fixed expected bytes, using the U1
fixture.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/yang/structxml.go src/protocol/yang/structjson.go src/protocol/yang/structcodec_test.go`

### U3. Struct operations and nested row codecs
Files: src/protocol/yang/structops.go, src/protocol/yang/nested.go, src/protocol/yang/structops_test.go, src/protocol/yang/nested_test.go
After: U1
Change: `visitLeaves` and `findJSONDescendant` iterate through the
iterator, and paths gain no segment for a group. `mergeStructValue`
handles a group field through its existing container branch.
Tests: `structops_test.go` covers requirements 4 and 5 with the U1
fixture. `nested_test.go` covers requirement 6 in both encodings.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/yang/structops.go src/protocol/yang/nested.go src/protocol/yang/structops_test.go src/protocol/yang/nested_test.go`

Waves: U1 | U2 U3
## Verification

```bash
go test -race ./src/protocol/yang/...
.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/yang
```

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] This plan's `status` set with an outcome note, and the parent's
      `Landed:` line for U1 filled.
- [ ] No plan labels in code.

## Open questions

None.
