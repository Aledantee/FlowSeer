# Inventory

The `flowseer.api.inventory.v1` package holds the entities of FlowSeer's
inventory plane: what the platform knows it manages, independent of any one
protocol or vendor. Entities here follow the triad and ref-pair shapes of
[`docs/conventions/protobuf.md`](../../../../../../docs/conventions/protobuf.md):
tenancy is ambient, provenance rides the envelope, and identity is a
FlowSeer-assigned UUID.

## Tags

A tag is an operator-curated grouping label. Tags are hierarchical: each tag
may name one parent, and a tag with no parent is a root of the tag tree. The point of the hierarchy is rollup. A filter, report, or
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

## Attributes

Where a tag marks, an attribute measures: an attached attribute always
carries at least one typed value, and a marker without a value belongs in
the tag tree instead. The schema splits the concern across two families and
a shared foundation:

- `entity.proto` holds the `EntityType` enum, the `EntityRef` that points at
  an entity whose kind is decided at runtime, and the `Entity` handle.
  Attribute targeting and value ownership are why it exists; the
  [`EntityRef` rule](../../../../../../docs/conventions/protobuf.md) in the
  conventions doc bounds when it may be used instead of a typed ref and what
  admission to `EntityType` obliges. Capability and the value assignment
  stay outside the enum: one is a closed enum rather than an identified
  entity, and nothing points at an assignment. Tenant is in the enum ahead
  of the tenant entity gaining an id surface; a ref to a tenant is content
  on the pointing entity, never the request's tenancy scope.
- The definition family names the attribute: a display name, one value
  type (string, number, closed enum with the vocabulary as data on the
  definition, or entity reference), the entity kinds it targets, and the
  bounds on how many values one assignment holds.
- The assignment family carries the values: one assignment per owner and
  definition, owned by exactly that owner, holding as many payloads as the
  definition's bounds allow.

Both families live in `attribute.proto`, and neither splits intent from
state: a definition and an assignment are pure operator intent with nothing
observed, so each family is its entity message (`Attribute`,
`AttributeValue`) plus its `Event`, and the file comment records the
deliberately absent members. The lifecycle rules
that need server state — target and cardinality checks, the drop-or-block
gate on invalidating definition edits, and the silent cascade when a referenced entity is deleted — live in
the message comments and are enforced by the inventory service.

## Devices

A device is the physical box, independent of every integration that reaches
it. Its identity is the FlowSeer-assigned UUID in the ref; the vendor serial
and chassis base MAC live on `DeviceState` as correlation data, because the
service merges new sightings onto existing devices by serial, and a ref that
carried vendor identity would make every consumer party to that decision.
Addresses, hostnames, and platform ids move between boxes, so they are
binding data and never device identity.

The family is a full triad. `DeviceConfig` is what the operator intends
(name and description), `DeviceState` is what the platform holds (the
identity read plus the lifecycle), and `DeviceEvent` carries one lifecycle
transition, with an unset `from` meaning the device entered the inventory.

Lifecycle and reachability are two axes that never share a word.
Reachability is per binding, machine-owned, and flaps and heals with nobody
acting. The lifecycle is about FlowSeer's knowledge of the box: `MISSING` is
the only system-set state — every binding unreachable past the tenant's
threshold — and is a suspicion that asks someone to look, and `RETIRED` is
the only state that means removed, set by a person or an explicit policy.
Retired devices keep history and refs; a reappearing serial un-retires
rather than duplicating. The threshold policy, the retire and un-retire
flows, and the evidence that sharpens a suspicion need state beyond these
messages and live in the inventory service, per the
[device-service direction record](../../../../../../docs/architecture/2026-08-20-device-service-and-inventory-direction.md).

## Other entities

`Tenant` and the `Capability` enum with its `CapabilitySet` are
early sketches predating the conventions doc and are refined entity by
entity; the tag, attribute, and device families above are the package's fully-shaped
ones. How tags attach to taggable entities (tag refs on the entity versus a
separate assignment entity) is still decided when the first taggable
entity's triad lands — the attribute assignment family is the separate-entity
precedent to weigh when that decision comes up.
