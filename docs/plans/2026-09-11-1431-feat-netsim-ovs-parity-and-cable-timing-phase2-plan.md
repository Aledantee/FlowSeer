---
title: Network Simulation, Phase 2: Relay Parity - Plan
type: feat
date: 2026-09-11
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-11-1431-feat-netsim-ovs-parity-and-cable-timing-plan.md
---

# Network Simulation, Phase 2: Relay Parity - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

The relay holds a bounded MAC table that evicts and counts, accepts static
entries at run time, and gains the VLAN behaviors a live Open vSwitch
exposes: `dot1q-tunnel` ports, a priority-tag policy, flood-only VLANs,
protected ports, and BPDU pass-through without spanning tree. The means is
`bridge.Config` fields and `Switch` methods for the entries, with the
loader mapping the tunnel mode the schema already carries.

## Decisions

The parent's Decisions on the bounded table and the VLAN behaviors hold.
Shapes to settle at re-planning: where the counters are read (`Switch`
method or `fabric.Device`), whether a protected port is a switchport field
or a bridge set, the service-tag EtherType default (0x88A8), and how a
customer VLAN outside the list is reported.

## Requirements

The parent's 9 through 16.
