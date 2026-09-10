---
title: Network Model Structure - Direction
type: direction
date: 2026-08-20
updated: 2026-09-04
topic: network-model-structure
status: accepted-direction
---

# Network Model Structure - Direction

How the FlowSeer-owned protobuf schemas under `spec/proto/flowseer/` are
partitioned: which packages exist, what kind of message each holds, who may
import whom, and how the interface — the one concept every layer touches — is
modelled. This record fixes the shape so the entity conventions, the first
`.proto` files, and the protocol-library mappers build toward one target. It
refines sequencing item 1 of
[the device service direction](2026-08-20-device-service-and-inventory-direction.md)
and is grounded in a survey of how YANG model families, NMS/source-of-truth
products, observed-state platforms, automation layers, vendor cloud APIs, and
the standard MIBs structure the same concepts (see Sources). Where this
direction deviates from that prior art it says so and why.

The focused protocol and Edition 2024 evidence behind the phy, packet,
switching, and ip boundary is recorded in the
[net core package research](2026-08-26-net-core-package-research.md).

## Decision in one paragraph

Two kinds of message, two trees, one import rule. **Primitives** under
`flowseer/net/…` are networking *values* — an address, a VLAN, a neighbor
entry, an interface — with no identity, tenant, lifecycle, or provenance.
**Entities** currently under `flowseer/api/inventory/v1` embed primitives by
value. UUID-identified entities carry ref pairs; intended-and-observed families
use the applicable lifecycle and Config/State/Event shapes. Deliberately partial
families, including the keyless Tenant sketch, follow the exceptions in the
protobuf model conventions. Address types and packet-header values live in
independent leaf packages; layer packages hold interface *facets* and protocol-agnostic
*tables*; each protocol owns its own package;
the interface is one message whose kind is a `oneof` and whose routed persona
is an optional cross-kind facet. Imports flow strictly upward according to the
order recorded below.

## The package tree

```
spec/proto/flowseer/
  net/
    addr/v1/            MAC/OUI and IP address, prefix, range, scope, and lifetime values
    packet/v1/          EtherType, DSCP, ECN, IP protocol, ports, TCP flags, and ICMP match atoms
    phy/v1/             Ethernet settings, capabilities, active facts, MAU, counters, transport arms, pluggable module, PoE
    switching/v1/       VLANs, tag stacks, SwitchportFacet, AggregationFacet, FdbEntry
    ip/v1/              IpFacet, InterfaceAddress, NeighborEntry
    capture/v1/         LinkType, CaptureCounters, CaptureFilter, mirror encapsulation, PacketRecord
    interface/v1/       Interface (oneof kind) and one message per kind arm
    wlan/v1/            Radio, Bss, WirelessClient — a peer of switching, not a child
    protocol/<x>/v1/    lldp, stp, lacp, … — one package per protocol, all it owns
  api/
    inventory/v1/       Device, Integration, Binding, Placement, IntegrationScope, provenance
    edge/v1/            Edge, its assertion and provisioning, EdgeService and EdgeAdminService (the first Connect service package)
    capture/v1/         CaptureSession and its lifecycle, CaptureService and CaptureEdgeService
    device/v1/          DeviceService, the operator-facing typed device API
  device/
    policy/v1/          AccessPolicyHandle, an opaque key and version; imports nothing
    access/v1/          the operation vocabulary every device-access boundary shares
  integration/device/v1/  execution envelopes between central and an integration (reserved)
  event/device/v1/      the durable DeviceOperationEvent audit record (reserved)
  errs/v1/              the error wire payload (reserved)
  service/v1/           process-local runtime messages and durable mailbox contracts
```

This tree uses current names for landed packages. `wlan/v1` and protocol
families beyond those present in the repository remain reserved locations,
as are the execution, audit, and error wire packages, whose paths the
2026-09-05 amendment below settles. `flowseer.service.v1` names the
process-local service runtime contract; it must not be treated as a
ConnectRPC API package by inference.

There is no base package. Ref pairs and lifecycle enums, when a family has
them, live in the package that owns the entity. The rules and deliberate
exceptions that shape a package's messages live in
[the protobuf model conventions](../conventions/protobuf.md), which this tree
assumes throughout.

Import layering is acyclic. The foundational dependency graph is:

```
net/addr ← {net/switching, net/ip}
net/packet ← net/switching
{net/addr, net/packet, net/switching} ← net/capture
{net/addr, net/packet, net/phy, net/switching, net/ip} ← net/interface
net/interface ← {net/protocol/*, net/wlan}
{net/interface, net/protocol/*, net/wlan} ← api/inventory
{api/edge, device/policy} ← api/inventory
{api/inventory, api/edge, device/policy, net/*} ← device/access
{device/access, api/inventory, device/policy, net/*} ← api/device
device/access ← {integration/device, event/device}
errs ← {api/device, integration/device, event/device}
{net/capture, api/edge} ← api/capture
```

`net/*` never imports `api/` or another entity or boundary package. Layers
never import a protocol.
`net/addr`, `net/packet`, and `net/phy` are leaves with respect to FlowSeer
packages; `net/switching` imports address and packet values, while `net/ip`
imports address values. Future integration, service API, and event packages
consume the entity model without introducing a downward import. `api/edge`
is the Edge's entity package and, as the first Connect service package, also
holds the Edge's services; it imports no FlowSeer package, and
`api/inventory` imports it because an integration names its hosting edge.
`device/policy` is the second leaf. The service API, the execution envelope,
and the event envelope are sibling boundary consumers of `device/access` and
never import one another; the event envelope reaches `api/edge` only through
`device/access`, which names the edge responsible for a mutation. The
order's home for automated checking is `test/conformance/proto/`;
`spec/proto/` holds only `.proto` and `README.md` files, so no test can sit
beside the schemas.

## Why this shape

### Primitives versus entities

A primitive is reusable precisely because it does not know which device it
came from. A discovery candidate's fingerprint, a topology edge, an ingestion
event, a host attachment, and the device service's response all need `Vlan`,
`LldpNeighbor`, `Interface` — none of them should drag in a `DeviceRef`, a
tenant, or a lifecycle. The moment a `net/` message grows a ref, it has become
an entity in disguise and moves up the tree. Prior art draws the same line at
the type-system level: NetBox separates field types (`MACAddressField`,
`IPAddressField`) from models; Infrahub separates attribute kinds
(`MacAddress`, `IPHost`) from nodes; YANG separates `typedef` modules from data
trees.

### Address types are a leaf, and MAC is not an L2 type

Every schema family surveyed keeps address *types* out of layer models: IETF
`ietf-yang-types` (`mac-address`, next to counters and timestamps) and
`ietf-inet-types`; OpenConfig `openconfig-yang-types` / `openconfig-inet-types`;
SNMP `SNMPv2-TC MacAddress` and `INET-ADDRESS-MIB`; SAI `saitypes.h`; Go's
dependency-free `net/netip`. RFC 8407 §4.12 states the rule: shared derived
types go in a separate module so they can be reused without coupling. A MAC is
used as a key by L2 (FDB), L3 (neighbor cache), device identity (base MAC),
wireless (client identity), and discovery; an IP prefix by routes, seeds,
integration configs, and LLDP management addresses. Putting `MacAddress` in
`switching` would make `switching` a de-facto base package imported by
everything. VLAN-id types, by contrast, sit with VLAN models everywhere
(`ieee802-dot1q-types`, `openconfig-vlan-types`), so the VLAN-id rule lives in
`switching`, not `addr`.

### `switching` / `ip` as package names

The survey is clear that tree roots are named by function — OpenConfig's
`interfaces`, `vlan`, `lldp`, `network-instance`; SuzieQ's `interfaces`,
`vlan`, `macs`, `arpnd`, `routes`; IP Fabric's `addressing`, `neighbors`,
`routing`; SONiC's `PORT`, `VLAN`, `INTERFACE`, `NEIGH`, `ROUTE`, `FDB` — and
that layer words survive mainly as qualifiers (`L2VSI`/`L3VRF`,
`ietf-l2-topology`, NX-OS `layer=Layer2|Layer3`). The exception is exactly the
content of these two packages: the *per-layer persona of an interface* is
named by layer in Ansible (`interfaces` / `l2_interfaces` / `l3_interfaces`),
Infrahub (`InterfaceLayer2` / `InterfaceLayer3` generics), SAI (`BRIDGE_PORT`
vs `ROUTER_INTERFACE`), Junos (`family ethernet-switching` / `family inet`),
and Meraki's WLC API (`interfaces/l2`, `interfaces/l3`). FlowSeer follows the
majority anyway and names the two packages `switching` and `ip`: the layer word
only ever qualified an interface persona, and these packages hold whole
functional domains. Messages inside are function-named too (`Vlan`,
`FdbEntry`, `IpFacet`, `NeighborEntry`), never `L2Thing`. Network instances,
RIBs, routes, and forwarding entries form separate future functional packages —
`net/routing` is reserved for them — rather than growing inside `net/ip`.

### Facets versus tables

Each layer package holds two shapes, and the distinction matters for where a
message is embedded:

- A **facet** is a bundle of per-interface attributes for one layer
  (`SwitchportFacet`: PVID and exact tagged/untagged memberships; `IpFacet`:
  per-family enablement, forwarding, and MTU; `EthernetFacet`: active speed,
  duplex, FEC, PoE, and capabilities). Facets are
  embedded by value in `net.interface.Interface`.
- A **table** is device-scoped state whose rows reference interfaces: the FDB
  keyed `(vlan, mac) → interface`, the IP neighbor cache keyed
  `(interface, ip) → mac`, and the VLAN database.
  Tables hang off the device entity's State with an interface reference in
  each row, never under the interface. This is how OpenConfig
  (`network-instance/fdb`), IEEE (`bridge/component/filtering-database`),
  Q-BRIDGE-MIB, SAI (`FDB_ENTRY(bv_id, mac)`), SuzieQ, and IP Fabric all place
  the MAC table. **Deviation:** YANG nests the ARP/ND cache under each
  interface; FlowSeer follows IP-MIB, SAI, SuzieQ, and IP Fabric and keeps it a
  device table with an interface column, because every consumer (discovery,
  topology, "where is this IP") queries it device-wide.

### VLAN membership has one canonical direction

VLAN definitions (`Vlan`: id, name, registration) are a device-level table.
Membership is stored **port-side** in the `SwitchportFacet` — as OpenConfig
`switched-vlan`, Ansible `l2_interfaces`, and NetBox `untagged_vlan` /
`tagged_vlans` do. The VLAN→members view that NAPALM `get_vlans`, OpenConfig's
read-only `vlan/members`, and Q-BRIDGE `PortList` expose is a *projection*
computed by consumers, never a second stored field. Storing both would be the
mirror drift Rule 1 exists to prevent.

### Protocols own their packages

LLDP, STP, LACP, and later BGP, OSPF, VRRP, CDP each get
`net/protocol/<x>/v1/` holding *everything the protocol owns*: its global
config and local-system block, its per-port config, its neighbor/peer/state
table, and — only if a parser ever needs it — the wire-faithful PDU decode, in
the same package. OpenConfig's `openconfig-lldp`, `openconfig-stp`,
`openconfig-lacp` as standalone modules, IEEE's `ieee802-dot1ab-lldp`, and the
`lldp` tables in SuzieQ, Ansible, and Genie are the precedent. The rule that
decides between a layer package and a protocol package: *if the table exists
regardless of which protocol populates it (neighbor cache, FDB, or a future
RIB) it is a functional-domain table; if it is the protocol's own table (LLDP
neighbors, STP port
state, BGP peers, LACP partner) it is `protocol/<x>`.* Protocols sit above the
layers because they reference layer types (LLDP-EXT-DOT1 carries a VLAN id,
MSTP references VLANs, BGP references prefixes and VRFs); layers never
reference a protocol — `AggregationFacet` holds static LAG membership, LACP
partner state lives in `protocol/lacp` referencing the LAG interface by name.
An earlier idea of splitting "normalized neighbor" (in `switching`) from "PDU
decode" (in `protocol/lldp`) is rejected: it creates two packages that must be
kept in sync for one protocol.

### Wireless is a peer of L2

Radios are not interfaces: UniFi models `interfaces{ports[], radios[]}` as
sibling arrays, Meraki exposes radios through `wireless/radio/settings` and
BSS lists rather than the port resources, OpenConfig keeps `wifi/` as a
separate tree that reuses `system` but not `interfaces`. `net/wlan` therefore
holds `Radio`, `Bss`, `WirelessClient` as a peer of `switching` and imports
`addr` and `switching` (a BSS maps to a VLAN id), never the reverse.

## The interface

The interface is the one concept every layer touches, so it gets its own
package and the most deliberate shape.

### Kind is a `oneof`; the routed persona is a cross-kind facet

Prior art models interfaces three ways: one wide row with all layers as
columns plus a mode discriminator (NetBox, LibreNMS, SuzieQ, IP Fabric,
Batfish, Genie); a base interface plus per-layer sub-blocks conditional on
type (IETF `ietf-interfaces` + `ietf-ip`, OpenConfig `ethernet` /
`aggregation` / `subinterfaces/ipv4` / `routed-vlan` under `when type = …`,
Junos families); or separate per-layer objects pointing at the port (SAI,
SONiC, NX-OS DME, Meraki switch "routing interfaces"). FlowSeer takes the
second and expresses it with protobuf semantics rather than YANG `when`
conditions:

```protobuf
edition = "2024";
package flowseer.net.interface.v1;

message Interface {
  // Common to every kind.
  string name = 1;                              // device-local identity
  uint32 if_index = 2;                          // when the source has one
  AdminStatus admin_status = 3;
  OperStatus oper_status = 4;
  uint32 mtu = 5;
  flowseer.net.addr.v1.MacAddress mac = 6;
  string description = 7;

  // What the interface is. Kind-specific attributes live in the arm, so an
  // SVI can never carry an Ethernet facet and a loopback can never carry
  // switchport config. Adding a kind is adding an arm.
  oneof kind {
    option (buf.validate.oneof).required = true;
    PhysicalInterface   physical   = 10;  // ethernet facet, switchport facet, lag parent
    LagInterface        lag        = 11;  // aggregation facet, switchport facet
    VlanInterface       vlan       = 12;  // SVI: the VLAN id it routes
    Subinterface        sub        = 13;  // parent name, 802.1Q tag(s)
    LoopbackInterface   loopback   = 14;
    TunnelInterface     tunnel     = 15;
    ManagementInterface management = 16;
    OtherInterface      other      = 17;  // carries the IANA ifType; nothing is dropped
  }

  // The routed persona. Present ⇔ routed. One message for a routed port, a
  // routed LAG, an SVI, a loopback — not one per underlying kind.
  flowseer.net.ip.v1.IpFacet ip = 20;
}
```

Why this and not the alternatives:

- **A kind enum plus optional facets** needs CEL rules to forbid invalid
  combinations (`ethernet` only when `PHYSICAL`); the `oneof` makes them
  unrepresentable, gives Go an exhaustive `WhichKind()` switch and TS a
  discriminated union, and is the same pattern the device service direction
  uses for `IntegrationConfig`.
- **"Physical interface" versus "routed interface" as the axis** is rejected.
  Physical is a *kind*; routed is a *persona*; they are orthogonal. A routed
  port, a routed LAG, an SVI, and a loopback share one identical L3 facet;
  Junos shows both personas on one port across units. Models that split them
  either duplicate the L3 facet per underlying kind (SONiC `INTERFACE` /
  `VLAN_INTERFACE` / `PORTCHANNEL_INTERFACE` / `LOOPBACK_INTERFACE`) or add a
  typed back-reference object (SAI `ROUTER_INTERFACE{type, port|vlan|…}`).
  Presence on `ip` is the discriminator and costs nothing.
- **`SwitchportFacet` appears in two arms** (`physical`, `lag`). That is one
  message type used twice, not two definitions; it does not trip Rule 1.
- **Layering is by name**: `PhysicalInterface.lag_parent` and
  `Subinterface.parent` are interface names within the same device — the
  `ifStackTable` relationship — so the value message stays ref-free.
- **`OtherInterface`** exists so an SNMP walk over the ~280 IANA `ifType`s
  never has to drop a row; it carries the raw type and the common fields.

### Hardware ports are a later, separate object; their values are not

The physical *port as hardware* — the component with its slot, its cage,
and its PoE PSE channel — is the one genuinely separate object in prior art
(OpenConfig `components/component[port]` ↔
`interfaces/interface/state/hardware-port`, ENTITY-MIB `entPhysical` ↔
`ifIndex` via `entAliasMappingTable`). That component tree, with its link to
the interface, arrives with ENTITY-MIB mapping. The *values* it will carry
do not wait for it: `EthernetFacet` already holds the pluggable module with
its SFF identity and per-lane diagnostics, the MAU type and link modes, the
Ethernet counters, and a transport oneof whose copper arm owns PoE, because
every one of those is a ref-free value a per-interface MIB reports today.
The component entity embeds the same messages when it lands; nothing in
`net/phy` references it.

### One interface identity

SNMP exposes three port-numbering spaces (`ifIndex`, `dot1dBasePort`,
`pethPsePortIndex`, plus `LldpPortNumber` which is one of the first two);
Meraki has `portId` strings, `interfaceId`s, appliance port integers, and AP
port names with no shared identity across product families; SmartZone keys
APs by MAC, RUCKUS One by serial. The normalized model has **one** interface
identity (`name`, plus `if_index` when known). Resolving the other spaces is
the SNMP mapper's and each adapter's job, never the schema's.

## Protobuf conventions this model relies on

The [style guide](../code-style-proto.md) governs; these are the decisions it
leaves to the model, made here so every file under `net/` is written the same
way. They are chosen for protobuf and edition 2024 semantics, not copied from
YANG or SQL habits. Every claim about compiler or validator behaviour below
was checked against primary documentation and reproduced locally (buf 1.72.0,
protoc-gen-go from protobuf-go 1.36.12 with the opaque API, protovalidate-go
1.3.0) on 2026-08-20; the edition 2024 defaults relied on are
`field_presence = EXPLICIT`, `enum_type = OPEN`,
`repeated_field_encoding = PACKED`, `enforce_naming_style = STYLE2024`,
`default_symbol_visibility = EXPORT_TOP_LEVEL`, and Go `api_level =
API_OPAQUE`.

1. **Explicit presence is the discriminator; zero is a value.** Every singular
   field tracks presence, so *unset* means "the source did not provide this"
   (direction rule 5) and `0`/`""` are real values: `length = 0` on a prefix
   is the default route; `access_vlan` unset means "no access VLAN reported",
   not VLAN 0. `mtu = 0` set explicitly survives a marshal/unmarshal round
   trip with `HasMtu() == true` (verified). Every field's comment answers
   "what does absent mean here". No `IMPLICIT` presence anywhere in `net/`; no
   sentinel values (`-1`, `""`, `UNKNOWN` strings) to encode absence. The
   `optional` label does not exist in edition 2024 (`unexpected 'optional'`),
   so there is nothing to write — presence is the default.
2. **Facets are message fields, so optionality is presence.** A routed
   interface is one with `ip` set; a switched one has `switchport` set in its
   arm; both may be set. No `is_routed` booleans that can disagree with the
   facet — protobuf's own guidance is not to use a boolean for something that
   may grow more states.
3. **Addresses are canonical bytes in typed variant messages.** A primitive
   with a closed set of variants that validate *differently* gets one message
   per variant, each carrying its own rule, and the common type is a required
   `oneof` of them: `Eui48Address{bytes octets}` (`bytes.len = 6`) and
   `Eui64Address{bytes octets}` (`len = 8`) behind
   `MacAddress{oneof kind}`; `Ipv4Address` (`len = 4`) and `Ipv6Address`
   (`len = 16`) behind `IpAddress{oneof family}`;
   `Ipv4Prefix{Ipv4Address address, uint32 length}` and
   `Ipv6Prefix{Ipv6Address address, uint32 length}` require masked network
   addresses through family-specific Protovalidate CEL rules behind
   `IpPrefix{oneof family}`. `Oui{bytes
   octets}` (`len = 3`) stands alone, because a vendor prefilter holds an OUI
   with no address behind it. Every payload field is `required`: the
   *containing* field's presence is what expresses optionality, so an address
   message that is set but empty is malformed, not absent.

   The variants replace the single-payload sketch this record originally
   carried (`MacAddress{bytes octets}` with `size() == 6 || size() == 8`, and
   an `IpPrefix` whose length was bounded by a message-level CEL rule keyed on
   the family). Every rule is now a plain field rule instead of an expression
   reconstructing a family from a byte count, a consumer switches on the arm
   instead of on a length, and adding a family is adding an arm.

   There is no `zone` on `IpAddress`. A link-local address is scoped by the
   interface column of whichever table carries it, and adding a field later is
   cheap where removing one is a permanent `reserved`.

   The rest of the IP value family follows the same structural rule.
   `IpRange` selects an `Ipv4Range` or `Ipv6Range`, making mixed-family
   endpoints unrepresentable; both endpoints are required, and message-level
   CEL validates their big-endian byte ordering. `IpLifetime` maps absent
   durations to the protocol-level infinite sentinel, distinguishes an omitted
   containing field from an explicit all-infinite lifetime, and validates the
   protocols' finite whole-second range plus preferred ≤ valid. `IpVersion`
   borrows the IANA address-family values for IPv4 and IPv6 but uses the
   registry-reserved zero
   as `IP_VERSION_UNSPECIFIED`; `IpScope` is a FlowSeer-normalized taxonomy.
   Packet-header registry values (`IpDscp`, `IpEcn`, `IpProtocol`, and
   `EtherType`) live in the independent `net/packet` leaf package.

   These are not the obsolete `google.protobuf` presence wrappers —
   protobuf.dev says those are unnecessary under explicit presence — they are
   structured values whose shape needs a message anyway, and the message gives
   Go and TS a named type with one home for the rule. Bytes are canonical: no
   `fe80::1` versus `FE80:0:0::1`, no `aa:bb` versus `AA-BB` normalization
   before correlation by serial + base MAC, and a direct
   `netip.AddrFromSlice` / `net.HardwareAddr` conversion. The costs are real
   and accepted: ProtoJSON renders bytes as base64, so raw JSON is not
   human-readable (display formatting is a library concern); Google's own
   APIs and `google.type` use plain strings for IPs and MACs, and protovalidate
   has no MAC rule at all, so FlowSeer defines its own. The choice is one-way:
   `string` ↔ `bytes` is a breaking change under every buf breaking category.
4. **Small domain scalars use predefined rules, not wrapper messages.**
   protovalidate *predefined rules* extend a standard rule message with a
   named CEL rule that any field of that type can switch on. `net/switching`
   defines `extend buf.validate.UInt32Rules { bool vlan_id = 5xxxx
   [(buf.validate.predefined).cel = { … "!rule || (this >= 1u && this <=
   4094u)" }] }` once, and fields write `uint32 access_vlan = 1
   `[(buf.validate.field).uint32.(vlan_id) = true]`. Repeated-item aggregates
   restate the same scalar bounds because protobuf text-format aggregates
   cannot name extension fields inside `items`. Both forms compile, lint, and
   enforce in edition 2024. A
   `VlanId{uint32 id}` wrapper is rejected: it costs a length-prefixed
   submessage on every FDB row, introduces a second presence (the wrapper set
   but its `id` unset), and buys only a type name that the rule's `id` already
   provides. Extension numbers come from the private range 50000–99999; the
   generated package carrying the extension must be linked into every
   validating binary (Go: a blank import) so the registry resolves it.
5. **Enums are prefixed and open; zero follows the enum's class.** Edition 2024
   enums are open: an unknown value from a newer producer is stored in the
   field as its number rather than dropped into unknown fields (language
   conformance varies; the generated Go here honours it). A normalized enum
   uses `<ENUM>_UNSPECIFIED = 0`. A registry pass-through enum preserves the
   registry's integer at zero, such as `IP_PROTOCOL_HOPOPT`; absence already
   says "not set", and consumers check presence before interpreting a zero
   getter value.
6. **Tables are repeated rows, never maps.** Rows carry their key fields
   (`FdbEntry{vlan, mac, interface}`, `NeighborEntry{interface, ip, mac}`).
   Protobuf map keys may be only integral or string types — not `bytes`, not
   messages, not composites — and map ordering is undefined on the wire and
   in iteration; repeated rows are uniform, keep deterministic serialization,
   and let a row grow fields.
7. **`oneof` for closed kinds, with `(buf.validate.oneof).required`.** A
   `oneof` is the protobuf sum type; a kind enum next to optional messages is
   a sum type reconstructed by hand and enforced by CEL. `required` means
   "exactly one member set" and is enforced (verified: `kind: exactly one
   field is required in oneof`). The opaque Go API generates `WhichKind()`
   with `Interface_Physical_case` constants plus `HasKind()`/`ClearKind()`.
   Two consequences from the language guide: a reader seeing `Kind_not_set`
   may be looking at an arm added by a newer producer, so consumers treat it
   as "unknown kind", not "no kind"; and moving a field into or out of a
   `oneof` loses data across versions, so `ip` staying outside the `oneof` is
   permanent.
8. **References inside a primitive are by local name; a ref lives in the
   package that owns its entity.** `net/` messages name other interfaces by
   `name` and carry no ref at all. Every UUID-identified entity has a
   `<Entity>LocalRef`/`<Entity>GlobalRef` pair beside its triad in its own
   package — an eventual `InterfaceRef` is an entity-package concern, not a
   `net/interface/v1` one; the landed Device ref lives in
   `api/inventory/v1` — because a package holding every ref would have to
   know every entity above it, which is the upward import this layering
   forbids. Refs do not carry tenancy scope: tenancy is ambient, resolved from
   the request context for RPC and from the producing integration for events.
   The keyless Tenant exception is defined in the model conventions.
   [The model conventions](../conventions/protobuf.md) hold the detail.
9. **Provenance rides the envelope, not State.** `observed_at` and the
   answering binding describe a live response or an event, so they are one
   message defined beside `Binding` in `api/inventory/v1` and embedded by the
   integration, service-response, and event envelopes — never a field of an
   `<Entity>State`, and never on a primitive. Rule 3 of
   [the device service direction](2026-08-20-device-service-and-inventory-direction.md)
   is about responses, and putting the answering binding on stored State would
   make reusable device state depend on inventory transport context.
   Observed-state systems that stamp each row (Netdisco `time_first/last`) do
   so because rows are their unit of storage; FlowSeer's is the response or
   event envelope.
10. **Field numbers and symbol visibility.** Numbers 1–15 (single-byte tags)
    go to the fields every consumer reads; in a message where a `oneof` sits
    alongside other fields, arms and facets start at 10 and 20 upward in
    blocks so 1–9 stay free for those fields. A message that is *nothing but*
    a variant `oneof` — the `net/addr` common types — has no such fields and
    numbers its arms from 1. Deleted numbers are `reserved`, never reused. Under
    edition 2024's `EXPORT_TOP_LEVEL` default, *nested* messages are local
    and cannot be used as field types from another file (verified: `found
    unexported message type`), so every `oneof` arm message and every facet
    is a top-level message. Most live in their own file; the tightly coupled IP
    variants share `net/addr/v1/ip.proto` and remain top-level exported types.
    Nothing in `net/` is `local`; everything is imported upward.
11. **Validation at the boundary, from the schema.** protovalidate rules on
    the primitives (`vlan_id`, per-variant address sizes and prefix lengths)
    run through the Connect interceptor and the web client; no hand-written
    checks that duplicate them. Two semantics to write rules against: a rule
    on an explicit-presence field is *skipped when the field is unset* unless
    `required` is set (verified), which is exactly the "unset = not provided"
    contract; and `required` checks presence only — `Interface.name` needs
    `required` *and* `string.min_len = 1`, because `""` set explicitly passes
    `required` (verified).

## Where this deviates from prior art, on purpose

- ARP/ND is a device table, not per-interface as in YANG (see *Facets versus
  tables*).
- Addresses are bytes, not strings as in every REST API and in NetBox.
- LLDP neighbors live in `protocol/lldp`, not in a link-layer package as in
  the earlier FlowSeer incarnation's `net/link/lldp` + `net/protocol/lldp`.
- No kind enum on the interface; the `oneof` is the kind.
- No per-kind L3 objects (SONiC/SAI/Meraki style); one `IpFacet`.
- VLAN membership stored port-side only; no `members` list on `Vlan`.
- Radios are not interfaces.

## What this enables, in order

Each layer lands as protos + relevant checks in the Go conformance package + one Go
mapper from the SNMP library (whose generated `ifmib`, `lldpmib`, `qbridgemib`,
`bridgemib`, `ipmib`, `entitymib` bindings already exist) + a wire-contract test, in
one commit with regenerated `generated/`:

1. [The entity conventions document](../conventions/protobuf.md), with
   `net/addr`, the independent `net/packet` header primitives, and repository
   conformance coverage outside the schema source tree.
2. `net/phy` and `net/interface` with the `physical`, `lag`, `vlan`,
   `loopback`, `other` arms. A separate Interface entity slice remains
   unlanded; the Device family currently lives in `api/inventory/v1`.
3. `net/switching` (the `vlan_id` rule, `Vlan`, `SwitchportFacet`,
   `AggregationFacet`, `FdbEntry`) and `net/protocol/lldp` — three of the five
   v1 capabilities (interfaces, neighbors, VLANs) via
   `qbridgemib`/`bridgemib`/`lldpmib`.
4. `api/inventory/v1` for the landed Device, Integration, Binding, Placement,
   and provenance families. Central integration execution, event envelopes,
   and ConnectRPC APIs still need settled package paths.
5. `net/ip` (`IpFacet`, `InterfaceAddress`, `NeighborEntry`) via `ipmib`; needed
   by discovery's table-walk sources anyway.
6. Network-instance and routing packages when a real RIB capability is ready;
   their keys must distinguish VRFs and multiple routing-protocol instances.
7. `net/wlan`; further `protocol/*` on demand; hardware components with
   ENTITY-MIB.

## Open questions

- **Host/Client entity family.** Netdisco's device-side (`device_*`) versus
  node-side (`node`, `node_ip`: hosts seen *through* devices, keyed by MAC
  with first/last-seen) split, mirrored by Meraki/UniFi/SmartZone "client"
  records, says FlowSeer needs an entity for end hosts distinct from
  Device/Interface — also what discovery's ARP/FDB crawl produces. Reserved,
  not in v1; `net/wlan.WirelessClient` and a future `host/v1` must share one
  shape.
- **Subinterface encapsulation** detail (single vs double tag, TPID) and
  whether `VlanInterface` and `Subinterface` converge on one tag-match
  message as in OpenConfig `subinterface/vlan/match`.
- **Cross-device VLAN entity** (NetBox makes VLAN a site-scoped object; here it
  is per-device State). Likely an inventory-level projection later, not a
  stored entity.
- **Config/State on interfaces**: OpenConfig's sibling `config`/`state` with
  intended values copied into state (to diff intended vs applied) versus
  NMDA's single tree with a datastore axis. The entity triad is the frame;
  whether `InterfaceState` carries a copy of applied config is decided when
  the first `Apply*` write lands.

## Sources

Researched 2026-08-20 from primary sources (RFCs, model repositories, product
documentation, API references).

- YANG/IETF/IEEE: RFC 6991 (`ietf-yang-types`, `ietf-inet-types`), RFC 8343
  (`ietf-interfaces`), RFC 8344 (`ietf-ip`), RFC 8349 (`ietf-routing`),
  RFC 8529 (`ietf-network-instance`), RFC 8345/8346/8944 (network topology,
  L3 and L2 overlays), RFC 8348 (`ietf-hardware`), RFC 8407 (author
  guidelines, §4.12 types modules), RFC 8342 (NMDA);
  `ieee802-dot1q-bridge.yang`, `ieee802-dot1q-types.yang`,
  `ieee802-dot1ab-lldp.yang` — https://www.rfc-editor.org/ ,
  https://ieee802.org/1/files/public/YANGs/
- OpenConfig: `release/models/{types,interfaces,vlan,lldp,network-instance,
  platform,system,wifi}` and the style guide —
  https://github.com/openconfig/public ; draft-openconfig-netmod-opstate.
- NMS / SoT: NetBox apps and models (`dcim.Interface`, `dcim.MACAddress` 4.2,
  `ipam.VLAN`, `ipam.IPAddress`, `dcim.Cable`/`CablePath`, Diode, Discovery)
  — https://github.com/netbox-community/netbox , https://netboxlabs.com/docs ;
  Nautobot core model (`Controller`, `ControllerManagedDeviceGroup`,
  `IPAddressToInterface`, Namespace) — https://docs.nautobot.com ; Infrahub
  schema and schema library (`InterfaceLayer2`/`InterfaceLayer3`) —
  https://docs.infrahub.app , https://github.com/opsmill/schema-library ;
  LibreNMS migrations (`ports`, `ipv4_mac`, `ports_fdb`, `vlans`,
  `ports_vlans`, `links`) — https://github.com/librenms/librenms ; Netdisco
  schema (`device_*` vs `node`/`node_ip`) — https://github.com/netdisco/netdisco
- Observed-state platforms: SuzieQ schemas (`interfaces`, `vlan`, `macs`,
  `arpnd`, `address`, `routes`, `lldp`, `topology`) —
  https://github.com/netenglabs/suzieq ; IP Fabric table catalogue
  (`addressing/{arp,mac,managed-devs}`, `neighbors/all`) —
  https://docs.ipfabric.io ; Batfish questions (`interfaceProperties`,
  `ipOwners`, `edges` with `layer1`/`layer3`) — https://batfish.readthedocs.io ;
  Arista Sysdb paths, NX-OS DME (`l1PhysIf.layer`, `sys/ipv4/inst/dom-…`),
  Junos `unit … family` hierarchy — vendor documentation.
- Automation layers and packet libraries: Ansible resource modules
  (`interfaces` / `l2_interfaces` / `l3_interfaces`, `lldp_global`,
  `lldp_interfaces`, `vlans`) — https://docs.ansible.com ,
  https://github.com/ansible-collections ; NAPALM getters and models —
  https://napalm.readthedocs.io ; Genie ops — https://github.com/CiscoTestAutomation/genielibs ;
  gopacket `layers`, scapy `layers/l2.py` and `contrib/lldp.py`; Go
  `net/netip` and https://tailscale.com/blog/netaddr-new-ip-type-for-go ;
  SONiC CONFIG_DB/APPL_DB schema — https://github.com/sonic-net/sonic-buildimage ,
  https://github.com/sonic-net/sonic-swss-common ; SAI object model —
  https://github.com/opencomputeproject/SAI ; gNOI `layer2` service —
  https://github.com/openconfig/gnoi
- Vendor platforms and MIBs: Meraki Dashboard API v1 (devices, switch ports,
  routing interfaces, appliance VLANs, `lldpCdp`, `topology/linkLayer`,
  clients, live tools) — https://developer.cisco.com/meraki/api-v1/ ; UniFi
  Network Integration API and legacy controller API — https://developer.ui.com ,
  https://github.com/beezly/unifi-apis ; RUCKUS SmartZone public API and
  switch-management API, RUCKUS One — https://docs.ruckuswireless.com ,
  https://docs.ruckus.cloud ; LANCOM Management Cloud —
  https://knowledgebase.lancom-systems.de ; IF-MIB (RFC 2863), ETHERLIKE-MIB
  (RFC 3635), BRIDGE-MIB (RFC 4188), Q-BRIDGE-MIB (RFC 4363), IP-MIB
  (RFC 4293), IP-FORWARD-MIB (RFC 4292), ENTITY-MIB (RFC 6933),
  POWER-ETHERNET-MIB (RFC 3621), SNMPv2-TC (RFC 2579), INET-ADDRESS-MIB
  (RFC 4001), LLDP-MIB and extensions, IEEE8023-LAG-MIB.
- Protobuf and protovalidate: edition 2024 feature defaults and migration
  notes — https://protobuf.dev/editions/features/ ,
  https://protobuf.dev/editions/overview/ ,
  https://protobuf.dev/reference/protobuf/edition-2024-spec/ ; symbol
  visibility — https://protobuf.dev/programming-guides/symbol_visibility/ ;
  language guide (oneof, maps, presence) —
  https://protobuf.dev/programming-guides/editions/ ,
  https://protobuf.dev/programming-guides/field_presence/ ; enums —
  https://protobuf.dev/programming-guides/enum/ ; opaque Go API —
  https://protobuf.dev/reference/go/go-generated-opaque/ ; best practices —
  https://protobuf.dev/best-practices/dos-donts/ ,
  https://protobuf.dev/best-practices/1-1-1/ ; wrappers are obsolete —
  https://protobuf.dev/reference/protobuf/google.protobuf/#wrappers ; ProtoJSON
  — https://protobuf.dev/programming-guides/json/ ; protovalidate rules,
  predefined rules, CEL extensions —
  https://protovalidate.com/reference/rules/ ,
  https://protovalidate.com/schemas/predefined-rules/ ,
  https://protovalidate.com/reference/cel_extensions/ ,
  https://github.com/bufbuild/protovalidate/blob/main/proto/protovalidate/buf/validate/validate.proto ;
  buf style guide, lint and breaking rules —
  https://buf.build/docs/best-practices/style-guide/ ,
  https://buf.build/docs/lint/rules/ , https://buf.build/docs/breaking/rules/ ;
  `google.type` has no address types —
  https://github.com/googleapis/googleapis/tree/master/google/type ; Google
  Compute API uses string IPs/MACs — `google/cloud/compute/v1/compute.proto`.
- Repository: `docs/code-style-proto.md` (edition 2024 presence, `oneof` +
  protovalidate, opaque Go API); `buf.yaml` (module, lint, and breaking policy);
  `docs/architecture/2026-08-20-device-service-and-inventory-direction.md`
  (rule 5 presence semantics, sequencing item 1, capability list);
  `generated/go/mib/` (existing `ifmib`, `lldpmib`, `qbridgemib`, `bridgemib`,
  `ipmib`, `entitymib` bindings).

## Amendments

### 2026-08-21 — no `core/v1`; typed address variants; provenance on the envelope

Landed with `docs/plans/2026-08-21-1257-feat-proto-base-types-plan.md`, which
wrote the first owned package and found that `core/v1`, as this record
originally specified it, contradicted the record's own primitive/entity line.

- **`core/v1` is gone** from the tree, the import order, and the sequencing.
  It held refs, provenance, and lifecycle enums — all entity concerns — yet
  sat *below* `net/addr`, under a name that said nothing about its contents.
  Every ref now lives in the package that owns its entity (convention 8), and
  the entity rules moved to `docs/conventions/protobuf.md`, which sequencing
  item 1 previously described as "the document `core/v1` assumes".
- **Provenance rides the response and event envelopes** (convention 9,
  reversed) rather than sitting on `<Entity>State`. The one job `core/v1` was
  doing was breaking a `device ↔ inventory` cycle, and that cycle only existed
  because the answering binding was stored on device State. Off State, no
  cycle; no cycle, no need for a base package.
- **Tenancy is ambient.** The landed, keyless `TenantRef` may appear as content,
  but it carries no tenant identity and never scopes a request or record. No
  other ref contains a tenant (convention 8), so payload data cannot override
  what authentication decided.
- **Address primitives are typed variants** (convention 3) instead of one
  `bytes` payload validated by size, and `IpAddress` lost its `zone` field —
  which also settles the IPv6-zone open question, removed above.
- **The import order is declared here.** Schema reviews compare new imports
  against this single order rather than maintaining a second copy in source
  fixtures.

### 2026-08-30 — the layer packages are named for their function

Landed with `docs/plans/2026-08-30-1420-feat-net-interface-lldp-plan.md`, which
opened by moving the two packages this record named after OSI layers.

- **`net/l2` is now `net/switching` and `net/l3` is now `net/ip`.** The section
  above conceded that function-named roots are what the survey found almost
  everywhere and then kept the layer names anyway, on the argument that a
  layer word describes an interface's persona. That argument holds for a
  persona; it does not hold for a package that owns a whole functional domain,
  and it left FlowSeer as the outlier against OpenConfig, SuzieQ, IP Fabric,
  and SONiC. Nothing consumed the generated `l2`/`l3` packages yet, so the move
  cost a rename of directories, package statements, and imports and no message
  shape at all. It would not have stayed that cheap.
- **`net/routing` stays unclaimed.** The IP package is `ip`, not `routing`,
  because network instances, RIBs, FIBs, and routes are a separate future
  package and that is the name they will want.
- **The unused `net/qos/v1` placeholder is gone.** It held a `.gitkeep` and no
  document referenced it; an empty directory is not a decision.
- **The import order is checked in Go, not beside the schemas.** The 2026-08-21
  amendment above left the order to review alone, and a later plan specified a
  layering test at `spec/proto/layering_test.go` that could never land —
  `spec/proto/` accepts only `.proto` and `README.md` files. The order's home
  for automated checking is `test/conformance/proto/`, where the rest of
  the schema gates already live.

### 2026-09-04 — inventory is under `api`; boundary package names remain open

The inventory work landed Device, Integration, Binding, Placement,
IntegrationScope, and provenance together under
`flowseer.api.inventory.v1`. That package is now the entity layer above the
network primitives. The earlier package tree split those families across
`device/v1`, `inventory/v1`, and `integration/v1`; those exact paths no longer
describe the repository.

The service runtime later claimed `flowseer.service.v1` for process-local
module messages and durable mailbox contracts. This amendment does not choose
new paths for the central integration execution API, ConnectRPC services, or
the event envelope. Their separation remains accepted, while their protobuf
names stay open until the first boundary schema is designed. It also does not
choose a package for a future Interface entity and its refs. Do not infer that
boundary from `net/interface/v1` or a former empty `api/interface/v1`
placeholder; amend this accepted record before adding the entity family.

### 2026-09-05 — phy owns the transport variants and the pluggable-module values

Landed with `docs/plans/2026-09-05-0004-feat-phy-transport-optics-plan.md`.

- **`EthernetFacet` splits by transport.** The flat facet with a medium enum
  accepted PoE on a fiber port without complaint, and every deferred field
  would have needed a cross-field guard of its own. The
  facet now keeps the link facts every medium shares and carries an optional
  transport oneof; the copper arm owns PoE intent, delivery state, and the PSE
  port row, while the fiber, backplane, and other arms state the medium and
  carry nothing else. This is the typed-variant convention applied to a
  facet, and it is the wire-breaking reshape the pre-stability rule requires
  when the design improves.
- **The pluggable module hangs off presence, not medium.** A direct-attach
  cable or a copper SFP module reports the same SFF-8472 identity as an
  optical one, so identity and per-lane diagnostics live on a module message
  beside the oneof, present exactly when a cage is reported. The "hardware
  ports" section above is rewritten to say so: the component *entity* is
  still later work, but the values it embeds are in phy now, ref-free, because
  the component model has no schema or consumer and waiting for it dropped
  walk values.
- **The deferred PHY values land.** Exact MAU types (a typed variant of IANA
  registration number or raw OID, so nothing is lost), advertised and received
  link modes as a registry pass-through enum numbered by IANA-MAU-MIB bit
  position, EtherLike-MIB error counters, per-port PoE fault counters keyed by
  PSE group and port, and a group-level PSE budget. PoE rows keep the MIB's own
  group and port key because POWER-ETHERNET-MIB never carries `ifIndex`; the
  join to an interface is the mapper's per-device rule, and a row with no rule
  is returned standalone rather than guessed.
- **Optics measurements are linear integers on the wire.** Nanowatts,
  microamperes, microvolts, and millidegrees carry SFF-8472's native steps
  without loss and give zero light a plain zero; consumers derive dBm. Two
  vendored DDM MIBs report dBm, and the mapper converts them once.

### 2026-09-05 — the device-access boundary packages

Landed with `docs/plans/2026-09-05-1709-feat-verified-local-device-access-plan.md`
under the [verified device access record](2026-09-05-verified-device-access-direction.md).

- **The boundary names the 2026-09-04 amendment left open are settled.**
  `api/device/v1` is the operator-facing Connect service; `device/policy/v1`
  is a dependency leaf holding the opaque access-policy handle;
  `device/access/v1` holds the operation vocabulary the operator API, the
  execution envelope (`integration/device/v1`), and the audit event
  (`event/device/v1`) share, so a phase means the same thing on every wire.
  The error wire payload the error-wire record describes lands in `errs/v1`
  with its first consumer. The package tree and the import graph above are
  updated in place; the three reserved packages land with their own plans.
- **The event envelope reaches `api/edge`.** The earlier sentence that it
  never imports `api/edge` is replaced: a mutation state names the edge
  responsible for it, so `device/access` imports `api/edge`, and every
  boundary that imports `device/access` reaches `api/edge` through it and
  never directly.
- **Provenance grew, in place.** The one provenance message beside `Binding`
  now carries the protocol that answered, the observing edge, and the
  firmware fingerprint, because a route is chosen per operation. No sibling
  provenance exists in `device/access`.
- **An interface is a name, not a ref.** The device-access messages identify
  an interface by the device ref plus the device-local name. The Interface
  entity and its ref stay undecided, as the 2026-09-04 amendment says.
- **`device/policy` holds one message.** The credential and host-trust
  handles arrive with the plan that delivers credentials to an edge.
### 2026-09-09 — net/capture holds the ref-free capture values

Remote packet capture splits on the primitive/entity line this record already
draws. `flowseer.net.capture.v1` now holds the values a capture produces and
selects on: a link type, capture counters, a capture filter, the mirror
encapsulation metadata, and a packet record. None of them carry a ref, so the
package sits under `net/` and the import order gains
`{net/addr, net/packet, net/switching} ← net/capture`. The session that owns
these values, with its own ref, lifecycle, and services, is a separate entity
package that a later change adds, with its own amendment here.

`net/capture` needs neither `net/phy` nor `net/ip`: it takes match atoms and
address types from `net/addr` and `net/packet`, and the VLAN-identifier
validation rules from `net/switching`, and nothing from the physical-layer or
IP-facet values.

### 2026-09-09 — api/capture holds the session identity and its services

`flowseer.api.capture.v1` is the entity package the entry above reserved:
`CaptureSession`'s ref pair and lifecycle, `CaptureService` for the operator
who creates and reads a capture back, and `CaptureEdgeService` for the edge
that uploads one. The import order gains `{net/capture, api/edge} ←
api/capture`.

Of the two edges, `net/capture ← api/capture` is the one carrying the
weight: a session's state holds the `net/capture` counters and link type it
observed, and every packet or artifact chunk on the wire holds `net/capture`
records rather than a copy of their fields. `api/edge` supplies only the
owning ref and the assertion a session's upload stream re-verifies.
