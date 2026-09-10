---
title: Network Simulation Environment, Phase 4: Routing Capability - Plan
type: feat
date: 2026-09-10
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-10-1815-feat-netsim-network-environment-plan.md
---

# Network Simulation Environment, Phase 4: Routing Capability - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

The virtual switch gains a `routing` capability: routed VLAN interfaces
with addresses, a forwarding table of connected and static routes, and a
neighbor table, so a frame addressed to the device on one VLAN is routed
and re-emitted on another with the trace naming each step. Hosts gain an IP
stack that sends through a gateway. The means is one more package under
`vswitch`, one more stage in the pipeline, and the codec's IPv4, IPv6, and
ARP headers. The phase is wrong if the first consumer needs a routing
protocol or a NAT, which are not tables this device holds.

## Decisions

The parent's Decisions hold: layer 3 is a capability of the same device,
`net/netip` for addresses. This phase adds the shape the re-planning starts
from:

- `routing.Config` holds routed interfaces keyed by VLAN id (a MAC, one or
  more prefixes as `netip.Prefix`), static routes (`netip.Prefix` to a next
  hop `netip.Addr` or to a connected interface), and static neighbors (VLAN
  id and `netip.Addr` to `netaddr.MAC`). Its presence is the `routing`
  capability and requires `vlan`. Why: `net/interface`'s `VlanInterface`
  with an `IpFacet` is the model's routed interface, and
  `net/ip.NeighborEntry` is its neighbor row, so the loader maps one to one.
- The relay hands a frame to routing when the classified VID has a routed
  interface, the destination MAC is that interface's MAC, and the payload
  EtherType is IPv4 or IPv6; otherwise the relay forwards as today. Why:
  this is the "router on a stick" a layer 3 switch is, and it keeps the
  layer 2 pipeline unchanged for every other frame.
- Routing decrements the TTL or hop limit, looks up the longest prefix,
  resolves the next hop (or the destination on a connected route) in the
  neighbor table, rewrites source and destination MACs, and re-enters
  egress on the egress VLAN with the relay's tag rules. Outcomes:
  `no-route`, `ttl-expired`, `neighbor-miss`, `not-routed` (a packet for
  the device itself). No ICMP is generated; the outcome is the answer.
  Why: the trace is the product, and a generated error packet would be a
  second propagation the caller did not ask for.
- A common packet package beside `ethernet` gains IPv4, IPv6, and ARP
  header decoding and encoding for the fields routing reads and rewrites,
  own code as with Ethernet, for every tree to use. Why: the `gopacket`
  boundary holds, and the value types are common by the parent's decision.
- A host with an IP stack (`netip.Prefix` per address, a gateway, static
  neighbors) builds the frame for a destination itself: to a neighbor on
  its prefix directly, else to the gateway's MAC. Why: the run's `Inject`
  then takes a destination address as well as a frame, and the question
  "can h1 reach 10.0.20.7" is one scenario.

## Requirements

This phase claims requirements 38 through 41 of the parent, with these acceptance examples:

38. A frame to the device's own address on a routed VLAN is routed to
    another VLAN. Acceptance: VLAN 10 with 10.0.10.1/24 and VLAN 20 with
    10.0.20.1/24 on `sw1`; a frame from a host in VLAN 10 to the
    interface's MAC carrying an IPv4 packet for 10.0.20.7 traces `classify`,
    `lookup` in the routing layer, `rewrite` of both addresses and TTL, and
    egress in VLAN 20 to the port the neighbor table names for 10.0.20.7.
39. A missing neighbor is an outcome, not a flood. Acceptance: the frame of
    33 with no neighbor entry for 10.0.20.7 ends with `neighbor-miss`
    naming the VLAN and address.
40. TTL exhaustion drops. Acceptance: the frame of 38 with TTL 1 drops with
    `ttl-expired`.
41. A host with an IP stack sends through its gateway. Acceptance: host `h1`
    with 10.0.10.7/24 and gateway 10.0.10.1 asked to send to 10.0.20.7
    emits a frame to the gateway's MAC, resolved from a static neighbor
    entry, and the result of 38 follows.

## Out of scope

Everything the parent lists, and ARP and ND exchanges: every neighbor is
static in this phase.

## Open questions

- Whether IPv6 is in the first cut or follows IPv4, given that the fields
  differ only in width and the hop limit.
- Whether the loader treats an `IpFacet` on a physical interface as a
  routed port (a VLAN of its own with one member) or reports it as
  unsupported.
- Whether the routed interface's MAC comes from `Interface.mac` or the
  device's base MAC when the model lacks it.
