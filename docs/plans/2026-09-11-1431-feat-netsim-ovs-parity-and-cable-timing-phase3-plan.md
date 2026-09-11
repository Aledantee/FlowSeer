---
title: Network Simulation, Phase 3: Spanning Tree Parity - Plan
type: feat
date: 2026-09-11
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-11-1431-feat-netsim-ovs-parity-and-cable-timing-plan.md
---

# Network Simulation, Phase 3: Spanning Tree Parity - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

The spanning tree layer gains auto-edge, protocol migration with legacy
802.1D compatibility per port, a transmit hold count, and per-port BPDU
counters, exported through the loader's protocol state. The means is
`stp.Port` and `stp.Config` fields, the migration state machine of IEEE
802.1D-2004 clause 17.24, and `PortInfo` counters.

## Decisions

The parent's Decision on spanning tree holds. Shapes to settle at
re-planning: the version 0 BPDU codec, the migration delay default (3 s per
clause 17.13), and which counters the `net/protocol/stp` schema needs
added to carry them.

## Requirements

The parent's 17 through 21.
