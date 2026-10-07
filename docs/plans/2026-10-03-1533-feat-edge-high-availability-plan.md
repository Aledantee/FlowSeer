---
title: Edge High Availability - Plan
type: feat
date: 2026-10-03
artifact_contract: flowseer-plan/v2
execution: mixed
amends: docs/architecture/2026-09-05-verified-device-access-direction.md
---

# Edge High Availability - Plan

## Goal

A site runs one to n edge nodes behind one virtual IP and keeps managing its
devices when a node stops. The means are an edge group in the registry, a
placement rule in central that spreads device lanes over the group's nodes,
shared receiver identity for syslog and traps, and an agent that builds for
FreeBSD.

Stop condition: the plan is wrong if a device can be written by two nodes
for one sequence.

## Decisions

The shape is in the
[edge high availability record](../architecture/2026-10-03-edge-high-availability-direction.md),
proposed and awaiting acceptance. The ones the user settled:

- A group has one to n nodes. (decided by the user, 2026-10-03)
- The nodes share a virtual IP for syslog and traps. (decided by the user,
  2026-10-03)
- Every node does outbound device access, and central places the lanes.
  (decided by the user, 2026-10-03)
- The virtual IP is moved outside FlowSeer, by CARP for now. (decided by the
  user, 2026-10-03)
- FreeBSD is a deployment target for the agent. (decided by the user,
  2026-10-03)
- A group is in a Site. (decided by the user, 2026-10-03)
- Device credentials reach a node on demand and are never stored. (decided
  by the user, 2026-10-03)
- No credential cache across operations. Why: the agent already keeps
  nothing standing (`src/edge/agent/README.md`), and a cache buys no
  availability because a node without central receives no dispatch.
- A write moves to another node only behind a positive fence. Why: decision
  8 of the verified device access record.
- The fence is the old node's own account on the device: every node has its
  own account, and the successor disables the old one, closes its sessions,
  and reads both back. (decided by the user, 2026-10-03)
- A device with no fence procedure keeps today's behavior for writes. Why:
  blocking is always safe, and reads still move.
- This plan starts after the central high availability plan lands. Why: both
  change the device service host and the registry, and moving a lane relies
  on any central replica serving an edge.
- Each node is its own enrolled Edge with its own key and NATS account. Why:
  `CONCEPTS.md` makes an Edge the process that holds a self-generated key,
  and the account per edge is the security boundary
  (`src/modules/edgebus/README.md`).
- Only central decides which node hosts a lane. Why: both halves of every
  comparison are central's (`src/services/device/README.md`), and nodes
  electing between themselves would be a second source of truth.

## Requirements

1. A group keeps its devices managed through the loss of one node. Example:
   with the hosting node stopped, a read of a managed interface completes
   through another node within the stale threshold plus one poll interval.
2. A device is never written by two nodes for one sequence. Example: when a
   node goes stale while a mutation is at `POSSIBLY_APPLIED`, no other node
   receives an `ExecuteRequest` for that device until a fence operation has
   read back that the old node's account is disabled and holds no session.
3. A node gets a device credential only for a lane placed on it. Example:
   `AcquireReadCredential` from a group member that does not host the
   device's lane is refused.
4. A node keeps no device credential after the operation. Example: after a
   read completes, the agent's state directory holds no credential material
   and a second read makes a second `AcquireReadCredential` call.
5. A returning node does not take its lanes back by itself. Example: after
   the stopped node restarts and reports `Onboarded`, its former lanes stay
   where central moved them.
6. Lanes are spread over the healthy nodes. Example: a group of three nodes
   and thirty devices places no more than ten lanes on any node.
7. Every node of a group presents one receiver identity. Example: two nodes
   answer an SNMPv3 inform with the same engine ID.
8. A node reports whether it holds the group address. Example: after the
   address moves, central's record of the holder changes within one
   heartbeat.
9. The agent builds for FreeBSD. Example:
   `CGO_ENABLED=0 GOOS=freebsd GOARCH=amd64 go build ./src/edge/agent/cmd/agent`
   exits 0. It fails today in `src/common/service`, whose store lock has no
   FreeBSD file.

## Out of scope

- Moving the virtual IP. A site configures CARP or another mechanism.
- The syslog and trap listeners themselves, which the ingestion plan adds.
  This plan gives them a shared identity and an address to bind.
- Telemetry buffered on a failed node. It is delivered when that node
  returns.
- Nodes of one group in different subnets.
- Local capture on FreeBSD.

## Open questions

- Which device families can disable an account and close its sessions.
  Unverified for every vendor, including the lab ICX7150. A lab check is a
  live-device write and needs its own approval.
- Who provisions the per-node accounts on a device.
- The shape of the fence credential and the fence operation in the schema.
- What the stale threshold is, and whether a short partition moves lanes.
- How central refuses the old host's late report: by Edge, by a placement
  generation in the lane record, or both.
- How a device reacts when an inform receiver's engine boots and time change
  on failover. Unverified.
- Whether the embedded NATS leaf node behaves on FreeBSD as on Linux.
  Unverified.
