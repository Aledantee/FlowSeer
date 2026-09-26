---
name: Protobuf Model Conventions
last_updated: 2026-09-25
---

# FlowSeer — Protobuf Model Conventions

How FlowSeer-owned messages under `spec/proto/flowseer/` are *shaped*: what an
entity's message family looks like, how one entity refers to another, where
tenancy and provenance live, and where enums go.

This is the model layer, not the style layer.
[`code-style-proto.md`](../code-style-proto.md) owns how a `.proto` file is
written — edition 2024 presence, symbol visibility, naming, evolution,
protovalidate — and governs everything here. This document defines what
Primitive, Entity, Triad, Ref Pair, Typed Variant, Provenance Envelope, and
Facet oblige a schema author to write, and is the "conventions doc" that
`tools/hooks/proto-check.sh` names when it reports a missing family member.

The package tree, the import layering, and the primitive/entity split are fixed
by [the network model structure
direction](../architecture/2026-08-20-network-model-structure-direction.md),
as amended by the [schema building blocks
direction](../architecture/2026-09-25-schema-building-blocks-direction.md).

## The triad

An Entity with both an intended and an observed side has three messages,
named for the same base:

| Message | Holds |
| --- | --- |
| `<Entity>Config` | What was *intended* — the desired settings a human or a policy asked for. |
| `<Entity>State` | What was *observed* — what the device or platform actually reported. |
| `<Entity>Event` | What *changed* — one transition, carried on the broker and the event envelope. |

All three are defined together in the Entity's own package, in one file per
family — the tag, attribute, and attribute-value families are the shape to
copy. A family's messages are one contract designed together, so splitting
them across files hides the coupling; the one-declaration-per-file default in
the style doc yields to this. Defining them together is not tidiness: intended
and observed have to be diffable field-for-field, and an `Event` that does not
know both sides cannot describe a transition.

Config and State are separate messages rather than one message with a
datastore axis, and neither is a subset of the other by construction — a device
reports things nobody configured (link speed, uptime) and accepts things it
never reports back.

The triad is shaped for entities where intended and observed genuinely
diverge — device-level configuration a human asks for and a device reports
on. Do not apply it reflexively. A family may be **deliberately partial**: a
machine-observed entity nobody configures has no `Config` (such as `Endpoint`
in `model/endpoint/v1`, whose family is `EndpointState` and `EndpointEvent`,
with `EndpointConfig` deliberately absent per its file-level doc comment); a
projection nobody stores has no `Event`; and a pure-intent entity nobody
observes has no `State` — and no `Config` suffix either, because with no
observed side to separate from, the intent message *is* the entity and is named
plain `<Entity>`. Such a family is `<Entity>` plus `<Entity>Event`, whose
before and after carry `<Entity>` — the attribute families are the worked
example. When a member is deliberately absent, say so in the family file's
file-level doc comment, naming what is missing and why. The hook reports every
missing member and cannot tell deliberate from forgotten, so the comment is
what lets the next reader tell, and putting it in a predictable place is what
lets them find it.

## The ref pair

Every UUID-identified Entity has exactly two ref messages, and they compose:

- `<Entity>LocalRef` — the Entity's key *within its owning parent*. For a
  top-level Entity this is its own identifier and nothing else.
- `<Entity>GlobalRef` — the owning parent's `GlobalRef` plus this Entity's
  `LocalRef`. For a top-level Entity it wraps only the `LocalRef`.

Uniform composition is the point. A field added to a `LocalRef` reaches every
`GlobalRef` that contains it without a second edit, and the hook can check the
pair mechanically because the shape never varies.

The deliberately partial, keyless `Tenant` sketch is the only current
exception. Its empty `TenantRef` is content, not an identity ref and not one
half of a `LocalRef`/`GlobalRef` pair. The ordinary pair becomes mandatory in
the same change that gives Tenant a FlowSeer identifier.

**An Entity has at most one owning parent.** An Entity that relates several
others — a `Binding` joining an integration to a device, a `Placement` joining
a device to a site — is top-level, and carries the related Entities' `GlobalRef`s
as ordinary fields. A ref never has two parents; the relationship is the
Entity's own content.

**Refs live beside the triad, in the Entity's own package, never in a package
that declares a service.** There is no shared refs package: a package holding
every ref would have to know every Entity above it, which is exactly the
upward-import the layering forbids. A ref in a service package would tie
every reader of that ref to every RPC the service also declares, so the Edge
ref lives apart from `EdgeService` and `EdgeAdminService` the same way its
triad does: an eventual `InterfaceRef` lives with the Interface *entity*, not
in `net/interface/v1`; the landed Device ref lives in `model/inventory/v1`
and the Edge ref in `model/edge/v1`. The Interface entity package is
intentionally undecided. Do not add the entity or infer an `api/interface`
package until an accepted direction record chooses that boundary.

**Entity identifiers are UUID strings**, FlowSeer-assigned and opaque to the
wire model. A top-level `LocalRef` holds them as:

```protobuf
message DeviceLocalRef {
  // FlowSeer-assigned device identifier. Must be present; omit the
  // containing field instead.
  string id = 1 [
    (buf.validate.field).required = true,
    (buf.validate.field).string.uuid = true
  ];
}
```

Vendor-side identity — serial number, base MAC, cloud object id — is
correlation data on the Entity, never the ref's key. Correlating two sightings
into one Entity is a service concern; a ref that carried a serial would make
every consumer party to that decision.

## EntityRef: the dynamic-kind exception

The typed `LocalRef`/`GlobalRef` pair stays the norm for every reference whose
target kind is known when the schema is written. `EntityRef` in
`model/inventory/v1/entity.proto` exists for the one case the pair cannot express:
a field that points at "some entity of a kind decided at runtime", such as the
owner of an attribute value:

```protobuf
// The entity carrying the values. Must be present. The kind is dynamic,
// which is what EntityRef exists for; the assignment is owned by exactly
// this entity.
EntityRef owner = 2 [(buf.validate.field).required = true];
```

Three boundaries keep it from eroding the typed refs:

- **Top-level entities only.** `EntityRef` is a flat kind-and-id with no
  ancestry chain, so it cannot address an entity identified relative to an
  owning parent. A nested entity keeps its typed `GlobalRef`.
- **Admission to `EntityType` is a contract.** A kind joins the enum only when
  its entity is UUID-identified, its delete flow cascades attribute values
  that reference it, and its store can answer the existence check a
  reference-value write needs. Landing a new top-level entity includes joining
  the enum in the same change; the hook does not police `EntityRef`, so this
  rule is the only guard. `ENTITY_TYPE_TENANT` is the one transitional
  exception: the wire schema admits it before Tenant has an addressable
  identity or store. An `EntityRef` carrying it is syntactically valid but
  cannot yet resolve to an existing entity. Producers must not emit that kind
  until the Tenant identity and store land, and the inventory service rejects
  it during semantic existence checks in the meantime. The enum value reserves
  the future contract; it is not permission to invent a tenant identifier.
  The Edge in `model/edge/v1` is the opposite exception: a landed, UUID-keyed
  entity that has not joined the enum, because the cascade and the existence
  check need the edge store, which lands with the first host. Until it joins,
  nothing may name an edge through an `EntityRef`. `Location`, `PatchPanel`,
  `Cable`, and `Link` in `model/inventory/v1`, `Wlan` in
  `model/wireless/v1`, and `Endpoint` in `model/endpoint/v1`, are the same class:
  UUID-keyed, landed, and outside the enum until their stores answer for them. `AccessPolicyHandle`,
  `CredentialHandle`, and `HostTrustHandle` in `model/policy/v1` are the
  second deliberate class of non-entity: each an opaque key and version into
  the device service's store, with no ref pair, no triad, and no place in
  the enum, because nothing else points at one and the store that would
  answer an existence check lands with that service.
- **A ref with the tenant kind is data, not scoping.** Tenancy stays ambient:
  the ref is content on the pointing entity and never stands in for the
  request's tenant context.

## Primitives refer to peers by key, never by ref

Messages under `flowseer/net/` carry no refs at all — that is what makes them
Primitives. A `net/` message names another interface by its bare `name` field,
a VLAN by its id. The moment a `net/` message grows a ref it has become an
Entity and belongs further up the tree.

Primitives do not use the triad's suffixes either. A Primitive that bundles
what a source reports about one interface, or about one component for a radio,
for one layer is a `<Name>Facet`
(`EthernetFacet`, `IpFacet`, `SwitchportFacet`); the requested values a
caller may set for the same layer are a `<Name>Settings` message the facet
carries beside the observed values. `CopperFacet.poe_settings` is the
worked instance; `EthernetSettings` is declared and not yet carried by any
facet. It is the same intended-versus-observed line the triad draws, drawn
once inside a value rather than across three messages, because a Primitive
has no identity to diff against and no event to carry. `Config` and `State`
stay reserved for Entities so that the hook's family check means one thing.

## Tenancy is ambient

The landed `model/inventory/v1/tenant.proto` defines a deliberately keyless
`TenantRef` for content relationships. It carries no tenant identifier and
never scopes a request or record. Tenancy is resolved from context at the edge
of the system:

- **RPC** — from the authenticated request context.
- **Events and ingestion** — from the producing integration or binding, which
  is per-tenant by construction (a per-tenant broker account).

Refs stay small, and tenancy cannot drift between what the auth layer decided
and what a payload claims. A message that "knows" its tenant is a message that
can lie about it.

## Provenance rides the envelope

"Observed at" and "which binding answered" describe a *live response or an
event*, not a stored thing. One provenance message is defined beside `Binding`
in `model/inventory/v1`, and is embedded by value in the integration,
service-response, and event envelopes. It also names the protocol that
produced the payload, the edge that performed the observation, and the
device's firmware fingerprint at that moment, because a route is chosen per
operation and two observations from different firmware epochs must be
tellable apart. There is one such message; a boundary package that needs
more provenance extends it here rather than defining a sibling.

It is never a field of an `<Entity>State` and never a field of a Primitive. This
keeps `net/` packages independent of entity and binding packages, and a `Vlan`
or an `InterfaceAddress` row stays a value that any consumer can hold without
inheriting the story of how it was fetched.

## Enums

Lifecycle and status enums live beside the Entity or [facet][facet] that owns them,
never in a shared package — a shared enum package acquires the same
knows-everything-above-it problem a shared refs package does.

Every value carries the enum-name prefix (enum values share package scope, so
unprefixed values from two enums collide), and enums are open: a consumer will
receive values it does not know and must handle them.

Enums fall into two classes. A FlowSeer-normalized taxonomy numbers its own
values and uses `<ENUM_NAME>_UNSPECIFIED = 0`. A registry pass-through enum
keeps the external registry's integers exactly, including a real assignment at
zero such as `IP_PROTOCOL_HOPOPT`, `IP_DSCP_CS0`, or `IP_ECN_NON_ECT`.
Presence carries "not observed" for both classes. Consumers of a pass-through
enum whose registry owns zero must check presence before reading the generated
getter, because the getter's absent default is also that registry's real zero
value. Open pass-through enums preserve unknown registry values, but they do
not enforce the registry's numeric width by themselves. Every field using
`IpDscp`, `IpEcn`, or `IpProtocol` therefore validates the complete numeric
domain at the use site: `0..63`, `0..3`, or `0..255`, respectively. These
packet-header registries live in `net/packet/v1`, not the address package.

## Typed variants

Where a Primitive has a closed set of variants that validate *differently* —
IPv4 versus IPv6, EUI-48 versus EUI-64, v4 versus v6 prefixes — each variant is
its own message carrying its own rules, and the common type is a required
`oneof` of the variants:

```protobuf
message IpAddress {
  oneof family {
    option (buf.validate.oneof).required = true;

    Ipv4Address v4 = 1;
    Ipv6Address v6 = 2;
  }
}
```

A consumer switches on the arm instead of on a payload size, every rule is a
plain field rule instead of a CEL expression that reconstructs the family from
a byte count, and adding a variant is adding an arm.

`spec/proto/flowseer/net/addr/v1/` is the worked instance: `ip.proto` keeps
`Ipv4Address` / `Ipv6Address` / `IpAddress`, `Ipv4Prefix` / `Ipv6Prefix` /
`IpPrefix`, and the related IP value types together; the MAC family uses
`Eui48Address` / `Eui64Address` / `MacAddress`.

The pattern is not limited to addresses. `RouteDistinguisher` in
`net/instance/v1` is a required `format` oneof of `As2RouteDistinguisher`,
`Ipv4RouteDistinguisher`, and `As4RouteDistinguisher`, because the three
RFC 4364 §4.2 types split the same six octets into fields of different widths,
and each arm bounds its own fields; a `"65000:100"` string would push that
parsing into every consumer. `NextHop` in `net/routing/v1` is a required
`target` oneof of a `ForwardingNextHop` message and a `SpecialNextHop` enum:
a next hop either forwards or discards, and the oneof is what makes both at
once unrepresentable. Protobuf keeps at most one arm, and decoding keeps the
last arm on the wire, so `required` only has the empty case left to reject.

An IP prefix carries a typed family address plus a prefix length. Its address
is the canonical network address, not an observed interface address: every
host bit beyond the declared length is zero. Model an observed interface
address and its prefix as separate fields in the owning facet.

The variant's payload field is `required`. The *containing* message's presence
is what expresses optionality — an address message that is set but empty is not
an absent address, it is a malformed one. Both rules earn their place: an empty
`bytes` is *present*, so `required` passes and the length rule is what rejects
it, while an unset one is caught by `required` alone.

Name the `oneof` for what actually distinguishes the arms. `IpAddress` and
`IpPrefix` use `family` because address family is the term of art for v4/v6;
`MacAddress` uses `kind`, because EUI-48 and EUI-64 are widths, not families.
Reach for the domain's own word before reaching for consistency with a sibling.

This is not the pattern for a scalar with a range. A VLAN id is one type with
one rule; it gets a protovalidate predefined rule, not a wrapper message
(direction convention 4).

## Units and keys

Field-author checklist for quantities, keys, and naming. The
[schema building blocks direction](../architecture/2026-09-25-schema-building-blocks-direction.md)
holds the rationale and standards grounding for each rule.

- **Canonical units**: every physical quantity has one canonical unit, named in the
  field suffix, in integer fixed point ([rule 1](../architecture/2026-09-25-schema-building-blocks-direction.md#1-one-canonical-unit-per-quantity)):

  | Quantity | Wire type | Field suffix |
  | --- | --- | --- |
  | Point in time | `google.protobuf.Timestamp` | none |
  | Time span | `google.protobuf.Duration` | none |
  | Data rate | `uint64` | `_bps` |
  | Data size and byte counters | `uint64` | `_bytes` |
  | Frequency and channel width | `uint32` | `_mhz` |
  | Linear power | `uint64` | `_nanowatts` |
  | Power level and gain | `sint32` | `_millidbm`, `_millidb`, `_millidbi` |
  | Temperature | `sint32` | `_millidegrees_celsius` |
  | Voltage | `sint32` | `_microvolts` |
  | Current | `sint32` | `_microamperes` |
  | Rotation speed | `uint32` | `_rpm` |
  | Percentage and ratio | `uint32` | `_basis_points` |

  Floating point is reserved for coordinates on `Location` and decimal operator
  attributes; every other numeric quantity uses integer fixed point. Counters
  are `uint64`, named for the unit (`in_bytes`, `in_frames`), and live in a
  `<Domain>Counters` message; every `*Counters` message carries
  `google.protobuf.Timestamp last_discontinuity` ([rule 2](../architecture/2026-09-25-schema-building-blocks-direction.md#2-counters-and-statistics)).
- **Interface names**: an interface name is validated by a predefined rule from
  `net/key/v1/key.proto` ([rule 3](../architecture/2026-09-25-schema-building-blocks-direction.md#3-keys-and-cross-references)).
  Observed rows use `interface_name` (1 to 255 characters). Values FlowSeer sends
  back to a device (such as an operation target or capture source) use
  `shell_safe_interface_name` (`^[A-Za-z0-9][A-Za-z0-9 ./:_-]*$`). A field whose
  name spells an interface name carries exactly one of them.
- **Network instance key**: a forwarding table's row names the network instance
  it belongs to in a required `string network_instance` validated by
  `net/key/v1`'s `network_instance_name` rule ([rule 4](../architecture/2026-09-25-schema-building-blocks-direction.md#4-the-network-instance-is-part-of-the-key)).
  `Vlan`, `FdbEntry`, and `Route` carry it on the row. A routed interface
  carries it once, in `IpFacet.network_instance`; `InterfaceAddress` and
  `NeighborEntry` rows are keyed by interface name and inherit it. A device
  with no instance concept reports one `NetworkInstance` of kind `DEFAULT`,
  named as the device names it or `default` when it has no name, and every
  row names that instance; an absent key never stands for the default.
- **Facets, settings, and rows**: per-interface bundles, or per-component for a
  radio, are named `<Name>Facet`, and requested values for that layer are
  `<Name>Settings`, carried by the
  facet ([rule 5](../architecture/2026-09-25-schema-building-blocks-direction.md#5-facets-settings-and-table-rows)).
  Device-scoped table rows are named for the thing they describe (`Vlan`, `Route`,
  `BgpPeer`, `Session`); use `<Table>Entry` only when the table name is the natural noun and
  the row has none of its own (`FdbEntry`, `NeighborEntry`). `NetworkInstance`
  is the row for an instance itself, keyed by `name` with a required `kind`.
  `Route` is keyed by `(network_instance, destination_prefix)` and carries its
  next hops as a `NextHopGroup`, plus a `table_type` saying whether the row
  came from the RIB or the FIB. `Session` in `net/portaccess/v1` is the row
  for a port-access session, keyed by `(interface_name, mac)` and scoped by the
  interface column, so `network_instance` is omitted per Rule 4.

## Field numbering

Numbers 1–15 encode as a single-byte tag; they go to the fields every consumer
reads. A removed number is `reserved` — with its name, in the same change —
and never reused.

Where a `oneof` sits **alongside other fields** — an interface whose kind
selector shares the message with its identity and facets — its arms start at
10 and facets at 20, in blocks, so 1–9 stay free for those other fields and a
family can grow without interleaving (the direction record's convention 10).

Where the `oneof` **is** the message, as in every typed variant above, there
are no other fields to leave room for: number the arms from 1 and let them
have the single-byte tags. `MacAddress`, `IpAddress`, and `IpPrefix` are the
worked instance.

The distinction is what the 10/20 blocks are reserving space *for*. Read it
that way when a new message does not obviously match either shape.

## A worked example

Not compiled, and not the current `model/inventory/v1` package. The Device family
has landed there, while the Interface entity has not. This example shows the
shapes the rules above produce together for a `Device` that owns an
`Interface`.

```protobuf
// Illustrative entity package: a device, its owned interface, and their refs.

message DeviceLocalRef {
  string id = 1 [
    (buf.validate.field).required = true,
    (buf.validate.field).string.uuid = true
  ];
}

// A device is top-level, so its global ref wraps only the local ref.
message DeviceGlobalRef {
  DeviceLocalRef device = 1 [(buf.validate.field).required = true];
}

// An interface is keyed by name within its device.
message InterfaceLocalRef {
  // Device-reported interface name, as the device spells it.
  string name = 1 [
    (buf.validate.field).required = true,
    (buf.validate.field).string.min_len = 1
  ];
}

message InterfaceGlobalRef {
  DeviceGlobalRef device = 1 [(buf.validate.field).required = true];
  InterfaceLocalRef interface = 2 [(buf.validate.field).required = true];
}

message DeviceConfig {
  DeviceGlobalRef ref = 1 [(buf.validate.field).required = true];
  // Operator-assigned name. Unset means no name was configured.
  string display_name = 2;
}

message DeviceState {
  DeviceGlobalRef ref = 1 [(buf.validate.field).required = true];
  // Chassis MAC as the device reports it. Unset means the device does not
  // expose one.
  flowseer.net.addr.v1.MacAddress base_mac = 2;
  DeviceLifecycle lifecycle = 3 [(buf.validate.field).enum.defined_only = true];
}

message DeviceEvent {
  DeviceGlobalRef ref = 1 [(buf.validate.field).required = true];
  DeviceLifecycle from = 2 [(buf.validate.field).enum.defined_only = true];
  DeviceLifecycle to = 3 [(buf.validate.field).enum.defined_only = true];
}
```

Read it for four things: the ref pair composes (`InterfaceGlobalRef` = parent's
`GlobalRef` + own `LocalRef`), tenancy is ambient rather than a field on these
messages, no message carries an `observed_at` or a binding, and the Primitive
(`MacAddress`) is embedded by value from `net/addr` with nothing flowing back
the other way.

[facet]: ../architecture/2026-08-20-network-model-structure-direction.md#facets-versus-tables
