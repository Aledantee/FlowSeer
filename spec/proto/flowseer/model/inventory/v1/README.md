# Inventory

`flowseer.model.inventory.v1` holds the entities of FlowSeer's inventory
plane: what the platform knows it manages, independent of any one protocol
or vendor. Entities here follow the triad and ref-pair shapes of
[`docs/conventions/protobuf.md`](../../../../../../docs/conventions/protobuf.md):
tenancy is ambient, provenance rides the envelope, and identity is a
FlowSeer-assigned UUID.

## Boundaries

Imports: model/edge, model/policy, net/addr, net/phy

Imported by: api/device, event/access, model/access, store/device

Deliberately absent:

- Config messages for `Binding`, `Component`, `Link`, and `IntegrationScope`.
  Each is discovered or synced rather than intended, so there is nothing an
  operator configures.
- `EntityRef` admission for `Location`, `Cable`, `PatchPanel`, and `Link`.
  Each is UUID-identified already, but the existence check and cascade that
  admission obliges need the store tables that land with the first
  location- and topology-aware service.
- A `Tenant` entity with an id surface. `EntityType.TENANT` exists ahead of
  that identity and its store landing; producers must not emit a tenant
  `EntityRef` until then.

## Tags

A tag is an operator-curated grouping label. Tags are hierarchical: each tag
may name one parent, and a tag with no parent is a root of the tag tree.
The point of the hierarchy is rollup. A filter scoped to `EMEA` also
matches everything tagged `EMEA/Berlin/DC-1`, so nobody has to tag a
switch three times.

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
  of the tenant entity gaining an id surface. Producers must not emit a tenant
  `EntityRef` until that identity and its store exist; the inventory service
  rejects one during semantic existence checks in the meantime. Once
  supported, such a ref is content on the pointing entity, never the request's
  tenancy scope.
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
Addresses and platform ids move between boxes, so they are binding data
and never device identity.

The family is a full triad. `DeviceConfig` is what the operator intends
(name, description, management mode, the pinned access policy, and where
the operator says the box is: a location, and within a rack the lowest
unit and face), `DeviceState` is what the platform holds (the identity
read, the whole-box platform reading of host name, vendor, model, hardware
revision, software version, and sysObjectID, plus the lifecycle), and
`DeviceEvent` carries one lifecycle transition, with an unset `from`
meaning the device entered the inventory. The platform reading is the
box's own account of what it is; the vendor identity tables the MIB
generator builds key on `sys_object_id`, and the human-readable software
version here is not the fingerprint a mutation pins, which stays on the
provenance and the access status.

The management mode says who resolves drift. Suppose an operator changes an
interface description at the switch console. The next authoritative read
shows a managed field that no FlowSeer mutation explains. Under
`OPERATOR_MANAGED` the device's lane blocks until someone accepts the
observed state, restores the expected one, or replaces the interrupted
intent; under `AUTHORITATIVE` FlowSeer queues an ordinary reconciliation
intent that restores the expected description. Both modes keep central
expected state, so the difference is who acts, never whether drift is
noticed. The access policy is an opaque handle from
[`model/policy/v1`](../../../model/policy/v1/README.md): the policy body
stays in the device service's store, and a mutation pins the version it
was admitted under.

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
controller, or site-local network through which FlowSeer reaches devices.
Kinds are code, instances are data — `IntegrationConfig` carries the
operator's intent (name, credential ref, request budget, and for an
integration that runs at a site the [edge](../../edge/v1/README.md)
that hosts it) plus a `kind` oneof whose arm both identifies the kind and
holds its typed configuration. The edge is its own entity, so a site that
hosts only an on-prem controller adapter needs no local-network integration
to stand in for its process, and a local-network integration must name one.
Only the local-network arm exists so far; each further first-party kind
lands as a new arm with its adapter, per the
[device-service direction record](../../../../../../docs/architecture/2026-08-20-device-service-and-inventory-direction.md).

Third-party kinds do not get arms. A third-party adapter advertises the
`FileDescriptorSet` of its config message when it announces, so its
configuration cannot be compiled into this schema — all such kinds share
the one `third_party` arm, told apart by the announced `kind_name`. The
payload is the serialized config message named by `type_name`; the service
builds the type from the advertised descriptor and runs its protovalidate
rules on the dynamic message, and the web renders the add-form from the
same descriptor. `descriptor_revision` pins the payload to the descriptor
revision it was written under, so a re-announced descriptor cannot
silently reinterpret stored bytes. Typed at runtime, never free-form: the
oneof numbering keeps 10 through 18 for first-party arms so the escape
hatch stays one arm, not a habit.

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

`Provenance` lives beside the binding family and is embedded by value in
response and event envelopes, per the conventions doc. It says which
binding answered, when it observed the payload, which protocol produced it,
which edge performed the observation, and the device's firmware fingerprint
at that moment. The protocol is on the provenance rather than fixed by the
binding because the answering integration chooses a route per operation:
an SNMP read that turns out incomplete falls through to SSH inside one
call, and the caller sees which one produced the result. The fingerprint
lets a consumer tell two observations from different firmware epochs apart
without a second lookup.

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
the schema's. A placement answers it in the platform's terms; the
operator's own answer is `DeviceConfig.location`, and the two coexist.

## Components

A component is one physical part of a device as the device reports it:
the chassis, a backplane, a slot, a power supply, a fan, a sensor, a line
card, a port, a stack member, or a transceiver seated in a port. The tree
is the Entity MIB's (RFC 6933) `entPhysicalTable` with the transceiver
added as its own kind, because a pluggable module is what a cable
terminates on and what carries the per-lane diagnostics `net/phy`
already models; `ComponentState.module` embeds `PluggableModule` for
exactly that kind. A port names the interface it fronts by the device's
own interface name, which is the `entAliasMappingTable` join and the only
way from the physical tree to the interface model.

A component is owned by its device and keyed by a device-local name, so
`ComponentGlobalRef` composes the device's ref with that name.
`entPhysicalIndex` is never the key: it renumbers on a reboot. The family
is `ComponentState` plus `ComponentEvent` with no `ComponentConfig`,
because nothing about the tree is intended; a component that stops being
reported leaves as an event with `after` unset. Which sensor readings are
worth carrying, and whether a sensor's value belongs here or on a live
read, is not decided yet; `SENSOR` names the part, not its reading.

## Locations, cabling, and links

The operator's own hierarchy of places is the `Location` family: region,
site, building, floor, room, row, rack, each naming its parent. It is
FlowSeer-owned intent, unlike `IntegrationScope`, which mirrors a
platform's hierarchy and is never edited here. A device says where it is
through `DeviceConfig.location`, a patch panel through
`PatchPanel.location`, and a rack occupant adds its lowest rack unit and
face. Sibling-name uniqueness and cycle-freedom of the parent chain are
the service's rules, as for tags.

Cabling is documented, never observed. A `Cable` has a medium (the
ISO/IEC 11801 copper categories and fiber types, direct-attach and active
optical cables, coaxial, serial, power), a role in the plant (a patch
cord, the permanent link behind the panels, or a pre-terminated trunk),
a length, a color, a printed label, and two terminations. A termination
is a device port or transceiver component, a patch panel port, or a
location for an end with no modeled port such as a wall outlet, each with
the connector it presents. `PatchPanel` holds the panel's front and rear
ports as content keyed by name within the panel, with a front port naming
the rear port it is wired through to, so a permanent link and the patch
cords on either side of it are three cables meeting at two panels. Both
families are pure intent: `Cable` plus `CableEvent`, `PatchPanel` plus
`PatchPanelEvent`.

What discovery sees is the `Link` family: one adjacency between two
interfaces derived from an LLDP or CDP announcement, an LACP partner, or
forwarding-table inference, with first and last seen and an active, stale,
or gone status. An end is an inventoried device or, for a neighbor the
inventory does not hold, the chassis identifier and system name the
announcement carried. `LinkState.cable` is the cable the service
reconciled the link to; an active link with no cable and a cable with no
link are both findings the service raises. First and last seen are entity
data rather than provenance because the stale and gone statuses are
defined against them, as `BindingState.unreachable_since` is for a binding;
which observation last reported the link still rides the envelope. The family is `LinkState` plus
`LinkEvent` with no `LinkConfig`. The source rows a link is derived from
stay in `net/protocol/lldp` and `net/switching`; the link is the
inventory's conclusion, not a copy of the evidence.

None of Location, Cable, PatchPanel, or Link has joined `EntityType` yet.
Each is UUID-identified, but the existence check and the cascade that
admission obliges need the store tables that land with the first
location- and topology-aware service; until then nothing may name one
through an `EntityRef`.

## Other entities

`Tenant` and the `Capability` enum with its `CapabilitySet` are
early sketches predating the conventions doc and are refined entity by
entity; the tag, attribute, and device families above are the package's fully-shaped
ones. How tags attach to taggable entities (tag refs on the entity versus a
separate assignment entity) is still decided when the first taggable
entity's triad lands — the attribute assignment family is the separate-entity
precedent to weigh when that decision comes up.
