---
title: Network Simulation, Phase 6: Multicast Snooping - Plan
type: feat
date: 2026-09-11
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-11-1431-feat-netsim-ovs-parity-and-cable-timing-plan.md
---

# Network Simulation, Phase 6: Multicast Snooping - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

The switch learns IGMP and MLD group membership per VLAN from reports and
forwards a group frame only to the ports that reported it and to the
querier port, flooding or dropping unregistered groups per configuration.
The means is `src/common/net/igmp` and `src/common/net/mld` codecs beside
`ip`, `netsim/vswitch/mcast` with a group table and its own aging, and the
relay's group destination rule consulting it.

## Decisions

The parent's Decision on snooping holds. Shapes to settle at re-planning:
the IGMP versions carried (v2 and v3, RFC 2236 and RFC 3376) and MLD
versions (v1 and v2, RFC 2710 and RFC 3810), the membership interval
default (260 s per RFC 2236 section 8.4), and whether the layer needs the
timer facility for aging or ages on arrival like the FDB.

## Requirements

The parent's 34 through 37.
