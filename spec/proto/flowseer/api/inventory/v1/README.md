# Inventory

The `flowseer.api.inventory.v1` package holds the entities of FlowSeer's
inventory plane: what the platform knows it manages, independent of any one
protocol or vendor. Entities here follow the triad and ref-pair shapes of
[`docs/conventions/protobuf.md`](../../../../../../docs/conventions/protobuf.md):
tenancy is ambient, provenance rides the envelope, and identity is a
FlowSeer-assigned UUID.

## Tags

A tag is an operator-curated grouping label. Tags are hierarchical: each tag
may name one parent, and a tag with no parent is a root of its tenant's tag
tree. The hierarchy buys rollup — anything tagged `EMEA/Berlin/DC-1` is
matched by a filter, report, or authorization grant scoped to `EMEA` without
being tagged three times.

The family is a full triad, split along intended versus derived:

- `TagConfig` — what the operator intends: name, description, and parent.
  The parent is an ordinary `TagGlobalRef` field, not part of the tag's
  identity; a tag's key never changes when it is reparented, and the global
  ref does not grow with tree depth.
- `TagState` — what the platform derives: the tag's `ancestors` (refs, root
  first) and its `path` (the names along that chain, ending with the tag's
  own name). Both are denormalized by the inventory service so consumers
  resolve ancestry and display paths from one read instead of one lookup per
  tree level, and both are rewritten when a rename or reparent anywhere on
  the chain invalidates them.
- `TagEvent` — one transition, carried as the intended definition before and
  after; an unset side means creation or deletion.

Invariants the messages cannot express are the inventory service's to hold:
sibling-name uniqueness, cycle-freedom of the parent chain, and the deletion
semantics of a tag that still has children. A tag names its parent but not
its children; the children of a tag are a query, not a field.

Tag names are operator-authored opaque strings. There is no per-locale
variant today; if localization is ever needed, a locale map can be added
beside `name` without breaking the existing contract.

## Other entities

`Tenant`, `Device`, and the `Capability` enum with its `CapabilitySet` are
early sketches predating the conventions doc and are refined entity by
entity; the tag family above is the package's first fully-shaped one. How
tags attach to taggable entities (tag refs on the entity versus a separate
assignment entity) is decided when the first taggable entity's triad lands.
