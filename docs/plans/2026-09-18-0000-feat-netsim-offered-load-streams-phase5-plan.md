---
title: Offered-Load Streams Phase 5 - On-Wire Transmitter and the Lab Comparison - Plan
type: feat
date: 2026-09-18
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: mixed
parent: docs/plans/2026-09-18-0000-feat-netsim-offered-load-streams-plan.md
---

# Offered-Load Streams Phase 5 - On-Wire Transmitter and the Lab Comparison - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

An edge application sends a `stream` value out of one NIC at its stated rate,
counts what a second NIC receives per flow, and prints statistics in the shape
of `fabric.FlowStats`, so one stream's simulated and measured results sit side
by side. The plan is wrong if the lab host's NIC and kernel cannot pace
accurately enough for the comparison to mean anything; the first unit measures
that before anything else is built.

## Decisions

- The parent's decisions and the direction record apply.
- The transmitter is for the owner's lab switches on the isolated lab network
  and refuses to open an interface that is not named in its configuration.
  Every lab run is announced to the owner in advance and waits for approval,
  as the repository's investigation discipline requires for live devices.
- Flow identity on the wire is a signature in the payload: a magic value, the
  flow, a sequence number, and a transmit timestamp. Why: the receiver has
  nothing else to join a frame to its stream.
- Goroutines start through `src/common/spawn`, which the panic gate enforces.
- No evidence was gathered on `src/edge/netpen/link`, the runner's safety
  guards, or the netpen module boundary. The research agent sent to read them
  was stopped by its model's security safeguard. The re-plan starts there, on
  a lane the owner chooses; `.claude/models/registry.yaml` lists
  `src/edge/netpen/**` under `sensitive_paths`.

## Requirements

16. A stream of 10,000 frames at 1,000 frames per second leaves the NIC in
    10 s within a tolerance the first unit measures and records.
17. Two flows sent through a lab switch are counted separately at the
    receiver, with loss computed from sequence gaps.
18. A report lists, per flow, the simulator's and the lab's offered, delivered,
    lost, and latency figures, and names every simulator issue the flow
    carried.

## Open questions

- Where the application lives: a subcommand of netpen, or a sibling under
  `src/edge` in the root module. netpen has its own `go.mod`, and the
  transmitter has to import `src/common/netsim/stream`.
- Whether `netpen/link` is reused or a transmit path is written beside it.
- Whether `src/modules/capture` serves as the receiver.
- Timestamp source for latency: software timestamps on one host with two NICs
  share a clock; the achievable precision is unknown.
- Which lab switch is the first target. The ICX7150 is approved as a test
  device and needs advance notice to be powered on.
