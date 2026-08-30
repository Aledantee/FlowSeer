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

## Integrations

An integration is a configured adapter instance: the cloud tenant,
controller, or edge agent through which FlowSeer reaches devices. Kinds are
code, instances are data — `IntegrationConfig` carries the operator's
intent (name, credential ref, request budget) plus a `kind` oneof whose arm
both identifies the kind and holds its typed configuration. Only the
local-network arm exists so far; each further first-party kind lands as a
new arm with its adapter, per the
[device-service direction record](../../../../../../docs/architecture/2026-08-20-device-service-and-inventory-direction.md).
Third-party descriptor-advertised kinds are that record's later step and
have no schema surface yet.

The credential never appears in inventory messages: `credential_ref` is a
handle into the secret store, so a leaked inventory dump leaks no secrets
and rotation touches nothing here. `IntegrationState` holds the identity
the adapter read off the platform at verification (id, name, version) and
the lifecycle. The lifecycle walks CANDIDATE → VERIFIED | FAILED, with
DEGRADED as the system's mass-drop and health guard and RETIRED as the
operator's exit; `IntegrationEvent` carries one transition like
`DeviceEvent` does. Verification order, the mass-drop threshold, and what
retiring cascades to bindings are service rules the messages cannot hold.

## Bindings

A binding is one path to a device through one integration: "device X is
reachable via integration Y at address Z". A device can have many; routing
reads only the binding, never the kind. The family has no `BindingConfig`
because discovery and correlation create bindings — nothing about one is
operator intent.

`BindingState` carries the two related refs (a binding relates entities, so
it is top-level and holds them as ordinary fields), the address as a oneof
— the platform's device id for mediated kinds, an IP-port-protocol
`ManagementEndpoint` for direct ones — the verified `CapabilitySet`, and
the machine-owned status. The status folds first-sighting (CANDIDATE),
reachability (VERIFIED / DEGRADED / UNREACHABLE), and RETIRED into one
axis because they never overlap in time and every consumer asks the same
question of them: can I route through this. Unreachability is "offline",
flaps and heals by itself, and never retires anything; the failure kind
distinguishes a timeout from a rejected credential from a host that
answers ping but not management, because the last one must never count
toward a device going `MISSING`.

`Provenance` — which binding answered, and when it observed the payload —
lives beside the binding family and is embedded by value in response and
event envelopes, per the conventions doc.

## Scopes and placements

`IntegrationScope` mirrors the platform's own hierarchy — a Meraki
network, a SmartZone zone, a site's subnet — generically: a name, a
kind-specific type label, and a parent. Scopes are owned by their
integration and keyed by the platform's own scope id, so the ref composes
the integration's global ref with that platform id instead of minting a
FlowSeer UUID for a row the platform can rename or drop on any sync. There
is no `IntegrationScopeConfig`; the hierarchy is synced, never intended.

A `Placement` is the dated record "device X belongs to scope Z": device
ref, scope ref, who asserted it (an operator, or derivation from the scope
the device appeared under — operator wins), and an effective span. The
family is append-only by construction: a move is a new placement plus a
close of the old one, `effective_until` unset marks the current placement,
and `PlacementEvent.after` is required because a placement is never
removed. Which placement currently answers "where is this device" when
operator and derived rows coexist is the service's precedence rule, not
the schema's.

## Other entities

`Tenant` and the `Capability` enum with its `CapabilitySet` are
early sketches predating the conventions doc and are refined entity by
entity; the tag, attribute, and device families above are the package's fully-shaped
ones. How tags attach to taggable entities (tag refs on the entity versus a
separate assignment entity) is still decided when the first taggable
entity's triad lands — the attribute assignment family is the separate-entity
precedent to weigh when that decision comes up.
