---
title: Network Simulation, Phase 4: Link Aggregation - Plan
type: feat
date: 2026-09-11
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-11-1431-feat-netsim-ovs-parity-and-cable-timing-plan.md
---

# Network Simulation, Phase 4: Link Aggregation - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

Link aggregation becomes a layer with bond modes, member selection by hash,
member up and down delays, and LACP as a protocol on the timer facility,
so a LAG answers which cable a frame takes and whether a member belongs to
the aggregator. The means is `netsim/vswitch/lag`, an LACPDU codec under
`src/common/net/lacp`, the relay and routing handing member selection to
the layer, and the loader reading the aggregation facet.

## Decisions

The parent's Decision on aggregation holds. Shapes to settle at
re-planning: the hash function and basis for balance-slb and balance-tcp
(OVS hashes source MAC and VLAN for slb and the layer 2 to 4 fields with a
basis for tcp), how `port.Table.Transmit` learns the chosen member, the
LACP timer defaults (1 s fast, 30 s slow, from IEEE 802.1AX), and whether
`LagInterface` in the schema needs an LACP state message.

## Requirements

The parent's 22 through 28.
