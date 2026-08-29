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
tree. The point of the hierarchy is rollup. A filter, report, or
authorization grant scoped to `EMEA` also matches everything tagged
`EMEA/Berlin/DC-1`, so nobody has to tag a switch three times.

The family is a full triad, split along intended versus derived:

- `TagConfig` holds what the operator intends: name, description, and
  parent. The parent is an ordinary `TagGlobalRef` field rather than part of
  the tag's identity, so a reparented tag keeps its key and the global ref
  stays the same size no matter how deep the tree gets.
- `TagState` holds what the platform derives: `ancestors` (refs, root first)
  and `path` (the names along that chain, ending with the tag's own name).
  The inventory service denormalizes both so that one read answers "where
  does this tag sit and what do I print for it", and rewrites them whenever
  a rename or reparent anywhere on the chain invalidates them.
- `TagEvent` carries one transition as the intended definition before and
  after. An unset side means the tag was created or deleted.

The messages cannot express every invariant, so the inventory service holds
the rest: sibling names must be unique, the parent chain must stay
cycle-free, and the service decides what deleting a tag that still has
children means. A tag names only its parent; to find its children, query.

Tag names are localized at serving time. When the RPC's language header
matches a stored translation, `name` and the entries of `TagState.path` come
back in that language; otherwise they come back in English. Translations
live on the server, so each response carries exactly one name per tag and
the wire messages stay unchanged.

## Other entities

`Tenant`, `Device`, and the `Capability` enum with its `CapabilitySet` are
early sketches predating the conventions doc and are refined entity by
entity; the tag family above is the package's first fully-shaped one. How
tags attach to taggable entities (tag refs on the entity versus a separate
assignment entity) is decided when the first taggable entity's triad lands.
