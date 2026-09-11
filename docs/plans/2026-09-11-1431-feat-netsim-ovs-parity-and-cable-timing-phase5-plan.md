---
title: Network Simulation, Phase 5: Mirrors, Policing, and Queues - Plan
type: feat
date: 2026-09-11
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-11-1431-feat-netsim-ovs-parity-and-cable-timing-plan.md
---

# Network Simulation, Phase 5: Mirrors, Policing, and Queues - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

The switch mirrors selected frames to a port or a VLAN, polices ingress
with a token bucket, and schedules egress by strict PCP priority with
per-queue rates over the serialization clock phase 1 added. The means is
`netsim/vswitch/traffic`, a stage after the relay's egress half, and the
busy clock extended with per-queue rate limits.

## Decisions

The parent's Decision on mirrors, policing, and queues holds. Shapes to
settle at re-planning: whether the token bucket refills on the run's clock
at each arrival, how a queue's maximum rate composes with the busy clock,
and whether a mirror copy gets its own journey or an entry in the
original's.

## Requirements

The parent's 29 through 33.
