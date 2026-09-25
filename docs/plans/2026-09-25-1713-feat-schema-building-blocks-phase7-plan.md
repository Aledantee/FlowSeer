---
title: Schema Building Blocks Phase 7, L3 Protocols and IP Services - Plan
type: feat
date: 2026-09-25
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-25-1713-feat-schema-building-blocks-plan.md
---

# Schema Building Blocks Phase 7, L3 Protocols and IP Services - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

Routing-protocol and IP-service state has a place: `protocol/bgp`,
`protocol/ospf`, `protocol/isis`, `protocol/vrrp`, `protocol/bfd`,
`protocol/dhcp`, `protocol/dns`, and the functional `net/nat`, every
table keyed by network instance where its protocol runs per instance.

## Decisions

The record's rules 4, 6, and 7 govern. From dossier 06, to be confirmed in
the re-plan:

- BGP peer state is a pass-through of `bgpPeerState` 1 to 6 (RFC 4271
  §8.2.2); ASNs are `uint32` (RFC 6793); AFI and SAFI are two pass-through
  enums from their IANA registries; standard and large communities are a
  typed variant (RFC 1997, RFC 8092).
- OSPF neighbor state is a pass-through 1 to 8; OSPF interface type keeps
  the registry's missing 4; the area id is a typed variant because OSPFv2
  uses a dotted quad and OSPFv3 an unsigned integer (RFC 5643).
- IS-IS adjacency state has four values (up, down, init, failed).
- BFD session state is a pass-through with `ADMIN_DOWN = 0` (RFC 5880 §4.1).
- VRRP's advertisement interval is a `Duration` (the protocol states
  centiseconds); priority 0 and 255 keep their reserved meanings in the
  comment. HSRP is a separate package if it comes (parent open question).
- DHCPv4 and DHCPv6 message types are separate enums; IA_NA and IA_PD are
  separate messages. DHCP snooping bindings live in `protocol/dhcp`.
- NAT keeps the mapping (pool rule) and the session (live translation)
  apart, as RFC 4008 does.
- A routing protocol can run several instances in one network instance
  (OSPF process ids, IS-IS tags), so a protocol's instance-level rows carry
  both `network_instance` and a protocol-instance key; rows keyed by
  interface inherit the network instance through the interface's
  `IpFacet` and do not repeat it (record, rule 4). The structure record's
  sequencing item 6 asked for exactly this distinction.
- IKE and IPsec are out of this phase (parent Out of scope).

## Requirements

1. A BGP peer with `remote_asn = 4200000000` passes.
2. `OspfInterfaceType` has no value 4.
3. A BFD session in `ADMIN_DOWN` is distinguishable from an unset state by
   presence.
4. An OSPF area row without `network_instance` fails; an OSPF neighbor
   row, keyed by interface, has no `network_instance` field.
