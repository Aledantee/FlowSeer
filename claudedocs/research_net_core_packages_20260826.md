---
title: FlowSeer net core package research
date: 2026-08-26
scope: flowseer.net.{phy,packet,l2,l3}.v1
confidence: high
---

# FlowSeer net core package research

## Executive summary

FlowSeer's four foundational network packages should contain reusable, ref-free
domain values rather than entities, services, provenance, or collector health.
The minimum coherent boundaries are:

- `net/phy/v1`: Ethernet settings, capabilities, active link facts, PoE, and a
  deliberately shallow transceiver summary.
- `net/packet/v1`: wire-header registries and small exact/match atoms reusable
  by future ACL, QoS, firewall, flow, and protocol packages.
- `net/l2/v1`: VLANs, exact 802.1Q tag stacks, switchport membership,
  protocol-independent aggregation attributes, and unicast FDB rows.
- `net/l3/v1`: per-interface IPv4/IPv6 facets, assigned-address rows, and the
  ARP/IPv6-ND neighbor cache.

Routing tables, routes, and next hops are not L3 interface primitives. They
need a future network-instance-aware `net/routing/v1` design. STP, LACP, DHCP,
OSPF, BGP, IGMP, MLD, and PIM remain separate protocol packages.

## Cross-package rules

1. Keep Edition 2024's default explicit presence. An absent observation means
   unsupported or unreported; a present zero or false is an observed value.
2. Use Protovalidate `required` for structurally mandatory fields. Range rules
   alone deliberately do not reject absence.
3. Keep registry enums open, preserve assigned numbers, and validate their
   complete numeric width at each use. Do not apply `defined_only` to observed
   registry values.
4. Use required `oneof` values for typed families and exact-versus-range
   variants. Absence of a matcher means unconstrained; do not add `ANY`
   sentinels.
5. Exact observations and match expressions are different contracts. Never
   encode ranges, wildcards, or tag stacks in strings.
6. No package contains device refs, tenant, lifecycle, provenance, timestamps,
   service messages, or collector status. Avoid primitive names ending in
   `Config`, `State`, or `Event`.
7. Imports point upward from leaf values: `packet` imports only validation;
   `l2` may import `packet` and `addr`; `l3` imports only `addr` and validation.

Protobuf Edition 2024 provides explicit singular-field presence and open enums
by default, and exports top-level while keeping nested symbols local. These
defaults fit FlowSeer's contracts without feature overrides.

## PHY package

### Implement now

- Separate `EthernetSettings`, `EthernetCapabilities`, and `EthernetFacet` so
  configured/requested speed, duplex, auto-negotiation, and FEC cannot be
  confused with negotiated operational values.
- Keep `uint64 speed_bps`; line rates continue to grow and are not a stable
  closed enum.
- Add a small open `EthernetFecMode` taxonomy and an
  `AutoNegotiationStatus` taxonomy.
- Keep `EthernetMedium` orthogonal: copper, fiber, backplane, other. Direct
  attach is a cable assembly, not a distinct underlying medium.
- Split PoE intent into `PoeSettings`; keep capability, role, delivery status,
  class, allocation, and measured draw in `PoeFacet`.
- Keep `TransceiverFacet` to presence and identity summary. Remove a single
  wavelength value because multi-lane and coherent modules make it ambiguous.

### Defer

Per-lane optics, DOM measurements and thresholds, connector/cage identity,
CMIS application codes, coherent optics, chassis PoE budgets, exact MAU types,
and detailed advertised link modes belong to later hardware-component or
capability work. Generic packet/octet counters belong to `net/interface`; only
Ethernet-specific counters should ever enter PHY.

## Packet package

### Implement now

- `EtherType`, with selected common assignments and structural validation of
  `1536..65535` at use sites.
- Move `IpDscp`, `IpEcn`, and `IpProtocol` from `net/addr` without changing
  their numeric values. Their valid widths are `0..63`, `0..3`, and `0..255`.
- `TransportPortRange` and `TransportPortMatch`; port zero remains a valid
  observed wire value.
- Exact `TcpFlags` and predicate `TcpFlagsMatch`, with unique bit values and
  disjoint required-set/required-clear sets.
- Family-typed ICMPv4/ICMPv6 exact fields and match atoms, using numeric
  type/code values so new registry assignments do not force schema releases.

### Defer

A monolithic packet matcher, address-mask predicates, fragment and length
matches, IPv6 flow labels, MPLS header matches, service-name sets, connection
state, flow tuples, counters, direction, and timestamps should wait for their
first real consumer.

## L2 package

### Implement now

- Keep scalar VLAN IDs with shared predefined validation. Distinguish usable
  VLAN IDs `1..4094` from tag VID values `0..4094`; VID zero is valid for a
  priority tag and 4095 is reserved.
- Rename the current permanent/dynamic `VlanStatus` to
  `VlanRegistration`; it describes registration/persistence, not operational
  status.
- Add exact `VlanTag` values containing TPID, VID, PCP, and DEI, and an ordered
  outermost-to-innermost `VlanTagStack`. Do not cap the stack at two tags.
- Add `SwitchportFacet` with PVID, exact tagged/untagged membership sets,
  ingress filtering, frame-admission behavior, and an optional normalized mode.
- Add a minimal protocol-independent `AggregationFacet`; LACP actor/partner
  facts remain in `net/protocol/lacp`.
- Add `FdbEntry` keyed by VLAN ID and EUI-48, with entry kind separated from
  current usability.

### Defer

Bridge-domain/FID identity, VLAN/FID mappings, match/rewrite expressions,
counters, STP, LACP, and MVRP. Bridge-domain scoping must be settled before v1
stability because VLAN IDs alone are not universal across independent learning
domains.

## L3 package

### Implement now

- `IpFacet` containing independent `Ipv4Facet` and `Ipv6Facet` messages.
- Per-family enablement, forwarding, and MTU. Validate IPv4 MTU as
  `68..65535` and IPv6 MTU as at least `1280`.
- Retain the device-wide `InterfaceAddress` table row and its family-equality
  invariant.
- Retain the device-wide neighbor-cache row, rename `NeighborState` to
  `NeighborReachability`, and restrict `is_router` to IPv6 rows.
- Correct the address-origin taxonomy so one enum does not mix SLAAC assignment
  with link-layer/random interface-identifier generation.

### Remove or defer

The current routing table, route, next-hop, route-protocol, and route-type files
should leave `net/l3/v1` before stability. Their present keys are not safe across
VRFs or multiple instances of the same routing protocol, and selected RIB state
must not be conflated with installed FIB state. Redesign them with a future
network-instance-aware routing slice rather than relocating them mechanically.

Multicast forwarding, routing policy, policy routing, tunnels, and protocol
state remain separate future domains.

## Sources

### Schema and protobuf

- [Protobuf Editions overview](https://protobuf.dev/editions/overview/)
- [Edition feature settings](https://protobuf.dev/editions/features/)
- [Field presence](https://protobuf.dev/programming-guides/field_presence/)
- [Enum behavior](https://protobuf.dev/programming-guides/enum/)
- [Protovalidate rule reference](https://protovalidate.com/reference/rules/)

### PHY

- [OpenConfig Ethernet model](https://github.com/openconfig/public/blob/master/release/models/interfaces/openconfig-if-ethernet.yang)
- [RFC 3621, Power Ethernet MIB](https://www.rfc-editor.org/info/rfc3621/)
- [RFC 3635, Ethernet-like Interface MIB](https://www.rfc-editor.org/info/rfc3635/)
- [SNIA SFF-8024](https://members.snia.org/document/dl/26423)
- [OIF implementation agreements and CMIS](https://www.oiforum.com/technical-work/implementation-agreements-ias/)
- [OpenConfig transceiver model](https://github.com/openconfig/public/blob/master/release/models/platform/openconfig-platform-transceiver.yang)

### Packet

- [IANA IEEE 802 numbers](https://www.iana.org/assignments/ieee-802-numbers)
- [IANA protocol numbers](https://www.iana.org/assignments/protocol-numbers)
- [IANA DSCP registry](https://www.iana.org/assignments/dscp-registry)
- [IANA service names and ports](https://www.iana.org/assignments/service-names-port-numbers)
- [IANA ICMPv4 registry](https://www.iana.org/assignments/icmp-parameters)
- [IANA ICMPv6 registry](https://www.iana.org/assignments/icmpv6-parameters)
- [RFC 3168, ECN](https://www.rfc-editor.org/info/rfc3168/)
- [RFC 9293, TCP](https://www.rfc-editor.org/rfc/rfc9293.html)

### L2

- [IEEE 802.1Q types](https://www.ieee802.org/1/files/public/YANGs/ieee802-dot1q-types.yang)
- [IEEE 802.1Q bridge model](https://www.ieee802.org/1/files/public/YANGs/ieee802-dot1q-bridge.yang)
- [OpenConfig VLAN](https://openconfig.net/projects/models/schemadocs/yangdoc/openconfig-vlan.html)
- [OpenConfig LACP](https://openconfig.net/projects/models/schemadocs/yangdoc/openconfig-lacp.html)
- [OpenConfig STP](https://openconfig.net/projects/models/schemadocs/yangdoc/openconfig-spanning-tree.html)
- [RFC 4363, Q-BRIDGE-MIB](https://www.rfc-editor.org/rfc/rfc4363.html)

### L3 and routing boundary

- [RFC 8344, IP interface management](https://www.rfc-editor.org/rfc/rfc8344.html)
- [RFC 8349, routing management](https://www.rfc-editor.org/rfc/rfc8349.html)
- [RFC 8529, network instances](https://www.rfc-editor.org/rfc/rfc8529.html)
- [RFC 9067, routing policy](https://www.rfc-editor.org/rfc/rfc9067.html)
- [OpenConfig network instance](https://openconfig.net/projects/models/schemadocs/yangdoc/openconfig-network-instance.html)

## Confidence and open risks

Confidence is high for the four package boundaries and Edition 2024 rules.
Exact FEC labels and packet matcher names have medium-high confidence because
external registries evolve and no ACL/QoS consumer exists yet. The largest
unresolved architectural dependency is bridge-domain and network-instance
identity; neither should be guessed into these foundational values.
