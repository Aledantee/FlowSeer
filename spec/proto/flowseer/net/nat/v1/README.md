# NAT Mappings and Sessions

The `flowseer.net.nat.v1` package defines primitives for Network Address
Translation (NAT) rules and active translation sessions.

## Boundaries

Imports: net/addr, net/key, net/packet

Imported by: nothing

Deliberately absent:

- RFC 4787 NAT behavior classes (endpoint-independent, address-dependent,
  address-and-port-dependent mapping and filtering), because no vendored source
  reports them.
- NAT Application Layer Gateways (ALGs) and timeout profile configuration.
- Directional counters on mapping rules (RFC 4008 `natAddrMapTable` counts live
  translations rather than packets; packet and byte counters live on `NatSession`).

## Contents

The package separates configured translation rules from live translation
sessions:

- `NatMapping` models configured translation rules (RFC 4008 `natAddrMapTable`).
  A mapping is keyed by network instance and name. It defines the local (private)
  and global (public) address and port ranges, the matching protocol, and the
  origin kind (`NatMappingKind`: static or dynamic).
- When a mapping has no `global_addresses` specified, it translates to the
  address assigned to its `interface_name` (MikroTik `masquerade`, Cisco IOS
  `overload` on an interface). The `nat_mapping.global_or_interface` CEL rule
  requires `interface_name` when `global_addresses` is omitted.
- `NatTranslation` identifies the translation entities (RFC 4008
  `NatTranslationEntity`). The enum values are numbered bit position plus one so
  zero is reserved for unspecified. A static bidirectional mapping sets multiple
  translation entities in the repeated `translations` field.
- `NatSession` models an active translation session (RFC 4008 `natSessionTable`).
  A session represents a conversation as seen in both the private and public
  realms, carrying four endpoints: `private_source`, `private_destination`,
  `public_source`, and `public_destination`. Cisco IOS-XE inside local, outside
  local, inside global, and outside global map onto these endpoints. Sources are
  required while destinations are optional because platforms like H3C report
  only inside, global, and peer addresses.
- `NatSessionCounters` carries packet and byte counters in each direction with a
  discontinuity timestamp.

## Sources

- RFC 4008 (`NAT-MIB`, `spec/mib/ietf/NAT-MIB`).
- RFC 2663 (NAT Terminology and Considerations).
- RFC 3022 (Traditional IP Network Address Translator).
- Cisco IOS-XE NAT operational YANG (`Cisco-IOS-XE-nat-oper.yang`).
- H3C NAT MIB (`HH3C-NAT-MIB`).
- MikroTik RouterOS OpenAPI (`routeros-7.24-openapi.json`).
