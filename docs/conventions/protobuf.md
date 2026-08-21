---
name: Protobuf Model Conventions
last_updated: 2026-08-21
---

# FlowSeer — Protobuf Model Conventions

How FlowSeer-owned messages under `spec/proto/flowseer/` are *shaped*: what an
entity's message family looks like, how one entity refers to another, where
tenancy and provenance live, and where enums go.

This is the model layer, not the style layer.
[`code-style-proto.md`](../code-style-proto.md) owns how a `.proto` file is
written — edition 2024 presence, symbol visibility, naming, evolution,
protovalidate — and governs everything here. The vocabulary is
[`CONCEPTS.md`](../../CONCEPTS.md) §Schema model: Primitive, Entity, Triad, Ref
Pair, Typed Variant, Provenance Envelope. This document says what those words
oblige a schema author to write, and is the "conventions doc" that
`.claude/hooks/proto-check.sh` names when it reports a missing family member.

The package tree, the import layering, and the primitive/entity split are fixed
by [the network model structure
direction](../architecture/2026-08-20-network-model-structure-direction.md);
`spec/proto/layering_test.go` enforces the layering.

## The triad

Every Entity has three messages, named for the same base:

| Message | Holds |
| --- | --- |
| `<Entity>Config` | What was *intended* — the desired settings a human or a policy asked for. |
| `<Entity>State` | What was *observed* — what the device or platform actually reported. |
| `<Entity>Event` | What *changed* — one transition, carried on the broker and the event envelope. |

All three are defined together in the Entity's own package, one message per
file. Defining them together is not tidiness: intended and observed have to be
diffable field-for-field, and an `Event` that does not know both sides cannot
describe a transition.

Config and State are separate messages rather than one message with a
datastore axis, and neither is a subset of the other by construction — a device
reports things nobody configured (link speed, uptime) and accepts things it
never reports back.

A family may be **deliberately partial**. A machine-observed entity nobody
configures has no `Config`; a projection nobody stores has no `Event`. When a
member is deliberately absent, say so in the package's file-level doc comment
on the message that does exist, naming what is missing and why. The hook reports
every missing member and cannot tell deliberate from forgotten — the comment is
what makes the next reader able to tell.

## The ref pair

Every Entity has exactly two ref messages, and they compose:

- `<Entity>LocalRef` — the Entity's key *within its owning parent*. For a
  top-level Entity this is its own identifier and nothing else.
- `<Entity>GlobalRef` — the owning parent's `GlobalRef` plus this Entity's
  `LocalRef`. For a top-level Entity it wraps only the `LocalRef`.

Uniform composition is the point. A field added to a `LocalRef` reaches every
`GlobalRef` that contains it without a second edit, and the hook can check the
pair mechanically because the shape never varies.

**An Entity has at most one owning parent.** An Entity that relates several
others — a `Binding` joining an integration to a device, a `Placement` joining
a device to a site — is top-level, and carries the related Entities' `GlobalRef`s
as ordinary fields. A ref never has two parents; the relationship is the
Entity's own content.

**Refs live beside the triad, in the Entity's own package.** There is no shared
refs package: a package holding every ref would have to know every Entity above
it, which is exactly the upward-import the layering forbids. `InterfaceRef`
lives with the Interface *entity* in `device/v1`, not in `net/interface/v1`.

**Entity identifiers are UUID strings**, FlowSeer-assigned and opaque to the
wire model. A top-level `LocalRef` holds them as:

```protobuf
message DeviceLocalRef {
  // FlowSeer-assigned device identifier. Absent is invalid; omit the
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

## Primitives refer to peers by key, never by ref

Messages under `flowseer/net/` carry no refs at all — that is what makes them
Primitives. A `net/` message names another interface by its bare `name` field,
a VLAN by its id. The moment a `net/` message grows a ref it has become an
Entity and belongs further up the tree.

## Tenancy is ambient

No `TenantRef` type exists, and no ref or entity message carries a tenant
field. Tenancy is resolved from context at the edge of the system:

- **RPC** — from the authenticated request context.
- **Events and ingestion** — from the producing integration or binding, which
  is per-tenant by construction (a per-tenant broker account).

Refs stay small, and tenancy cannot drift between what the auth layer decided
and what a payload claims. A message that "knows" its tenant is a message that
can lie about it.

## Provenance rides the envelope

"Observed at" and "which binding answered" describe a *live response or an
event*, not a stored thing. One provenance message is defined beside `Binding`
in `inventory/v1`, and is embedded by value in the integration, service-response,
and event envelopes.

It is never a field of an `<Entity>State` and never a field of a Primitive. Two
consequences make this the load-bearing choice: `device/` never names a binding,
so the `device ↔ inventory` reference cycle does not exist; and a `Vlan` or a
`Route` row stays a value that any consumer can hold without inheriting the
story of how it was fetched.

## Enums

Lifecycle and status enums live beside the Entity or [facet][facet] that owns them,
never in a shared package — a shared enum package acquires the same
knows-everything-above-it problem a shared refs package does.

Zero value is `<ENUM_NAME>_UNSPECIFIED`, every value carries the enum-name
prefix (enum values share package scope, so unprefixed values from two enums
collide), and enums are open: a consumer will receive values it does not know
and must handle them.

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

`spec/proto/flowseer/net/addr/v1/` is the worked instance: `Ipv4Address` /
`Ipv6Address` / `IpAddress`, `Eui48Address` / `Eui64Address` / `MacAddress`,
`Ipv4Prefix` / `Ipv6Prefix` / `IpPrefix`.

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

Not compiled, and not the real `device/v1` — that package is written when the
first device slice lands. This shows the shapes the rules above produce
together, for a `Device` that owns an `Interface`.

```protobuf
// device/v1: the device entity, its owned interface, and their refs.

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
`GlobalRef` + own `LocalRef`), no message carries a tenant, no message carries
an `observed_at` or a binding, and the Primitive (`MacAddress`) is embedded by
value from `net/addr` with nothing flowing back the other way.

[facet]: ../architecture/2026-08-20-network-model-structure-direction.md#facets-versus-tables
