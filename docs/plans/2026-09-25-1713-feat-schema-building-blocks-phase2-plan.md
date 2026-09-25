---
title: Schema Building Blocks Phase 2, Network Instances and Routing - Plan
type: feat
date: 2026-09-25
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-25-1713-feat-schema-building-blocks-plan.md
---

# Schema Building Blocks Phase 2, Network Instances and Routing - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

Every forwarding table names the network instance it belongs to, and a
routing table exists. `net/instance/v1` holds the `NetworkInstance` row and
its kind; `Vlan`, `FdbEntry`, and `IpFacet` require `network_instance`;
`net/routing/v1` holds routes, next hops, and the RIB/FIB discriminator.
Netsim and the SNMP mappers fill the key with the device's default
instance.

## Decisions

The record's rule 4 (the key, the default-instance naming, the
independent-learning statement on `FdbEntry`) and the parent's decisions
govern. From dossiers 05 and 06, to be confirmed in the re-plan:

- `NetworkInstanceKind` is normalized from
  `openconfig-network-instance-types.yang` (`DEFAULT`, `L3VRF`, `L2VSI`,
  `L2P2P`, `L2L3`). `NetworkInstance` may carry a route distinguisher; the
  re-plan decides its type (RFC 4364 §4.2 defines three typed formats,
  which argues for a typed variant rather than a string).
- `RouteSourceProtocol` is a pass-through of `IANAipRouteProtocol`
  (`spec/mib/ietf/IANA-RTPROTO-MIB`, `other(1)` to `ttdp(20)`); multicast
  route sources are a separate registry and do not share the enum.
- `NextHop` is a typed variant of a forwarding next hop (interface name
  and optional gateway) and a special next hop (`BLACKHOLE`,
  `UNREACHABLE`, `PROHIBIT`, `RECEIVE`, the four RFC 8349 values).
- A route row says whether it came from the RIB or the FIB, because no SNMP
  table distinguishes them (atlas 04 §3.7).

## Requirements

1. An `FdbEntry` without `network_instance` fails validation.
2. A netsim export of a single-bridge switch reports one `NetworkInstance`
   of kind `DEFAULT` named `default`, and every `Vlan` and `FdbEntry` row
   names it.
3. A static default route `0.0.0.0/0 via 192.0.2.1` in instance `default`
   round-trips through `Route` with `source_protocol = NETMGMT(3)`.
4. A `NextHop` with both arms set fails `oneof` validation.
