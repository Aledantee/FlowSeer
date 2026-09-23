---
title: Network Simulation, Offered-Load Streams - Plan
type: feat
date: 2026-09-18
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
---

# Network Simulation, Offered-Load Streams - Plan

This is a parent plan. Its units are phases, each with its own plan. The first
is implementation-ready and the later four are re-planned when their turn
comes. The shared decisions live in
`docs/architecture/2026-09-18-offered-load-streams-direction.md`, a proposed
record that amends the virtual device record.

## Goal

A caller states traffic as streams (a frame template, a rate, a burst shape, a
count, varying fields) and a fabric run answers what each stream delivered,
what it lost and where, and how late it arrived, with the trust metadata of
everything the answer rests on. The same stream value runs on a lab NIC, so
the simulator's queue and policer model is checked against a real switch. The
means is five phases: the engine holds millions of frames, egress queues get
stated buffers, the `stream` package and the pull loop land, a capture file
becomes a source, and an edge application transmits. The plan is wrong if a
load question needs rates the per-frame engine cannot reach after phase 1, a
sustained 10 Gbit/s for example, because that calls for a fluid model and a
different design.

## Decisions

- The direction record carries the decisions every phase shares: stream as a
  plain value, lazy pull, SplitMix64 with a spec seed, buffers only where
  stated, journey retention with per-flow statistics, payload-free identity in
  the simulator, the pcap reader under `src/common/net`, and the transmitter
  as an edge application. Why: each one constrains more than one package, which
  is the promotion test.
- The package is `src/common/netsim/stream`, not `traffic`. Why:
  `src/common/netsim/vswitch/traffic` already owns mirrors, policers, and queue
  rates, and two packages named `traffic` in one tree read as one.
- The engine phase comes first and ships flow statistics with it. Why: the
  statistics are folded from journey entries, which only `fabric` sees, and
  retention without a place to fold into would free journeys into nothing.
- Buffers come before streams. Why: both edit `fabric/run.go`, so they cannot
  run at once, and a stream layer that lands first would publish loss-free
  numbers for oversubscribed ports.
- User-directed on 2026-09-17: tail drop only on a stated buffer; runs of
  millions of frames; a pcap source; a stream spec that is usable on the wire;
  the on-wire transmitter in this plan.

## Requirements

Phase 1 claims 1 through 5, phase 2 claims 6 through 8, phase 3 claims 9
through 13, phase 4 claims 14 and 15, phase 5 claims 16 through 18. Each phase
plan carries the acceptance examples for its own.

1. The arrival queue pops in the order `compareArrival` defines, and
   `Snapshot.Queue` is sorted by it.
2. The fabric knows when the last copy of a frame has settled.
3. An injection with `RetainAggregate` leaves no per-frame state after it
   settles.
4. `Fabric.Flows()` reports per-flow offered, delivered, dropped by reason,
   lost, unresolved, latency, and merged trust metadata.
5. A run of 1,000,000 aggregated frames across two switches completes inside
   the benchmark budget phase 1 sets.
6. A queue with a stated buffer tail-drops with reason `queue-full` and counts
   it in `OutDiscards`.
7. A queue with no stated buffer never drops, reports peak depth, and raises
   an `Incomplete` issue once its depth passes one maximum-size frame.
8. The buffer field clones, validates, and diffs like `MaxRateBPS`.
9. A stream emits frames at its stated rate, burst, count, and start time.
10. A field variation steps or draws deterministically; SplitMix64 matches its
    published vectors.
11. An attached stream yields the same per-flow statistics and delivery times
    as the same frames injected eagerly.
12. A frame-size sweep and the RFC 2544 section 9.1 sizes are expressible.
13. The conformance corpus admits the oversubscribed-trunk cases.
14. `src/common/net/pcap` reads classic pcap and pcapng Ethernet captures into
    timestamped records, checked against fixture bytes, not a round trip.
15. A capture attaches as a stream source with its inter-frame timing.
16. The edge transmitter executes a `stream` value at its stated rate.
17. A receiver counts per-flow frames by the transmitter's payload signature.
18. One stream's simulator and lab statistics compare in a documented report.

## Out of scope

- A fluid or rate-based flow model, and sustained line rate at 10 Gbit/s.
- TCP behavior: congestion control, retransmission, and any stateful stream.
- A `flowseer/netsim/v1` schema for streams; it is written when a service
  carries a scenario, as the virtual device record says.
- A user interface, and Ostinato's or TRex's RPC compatibility.
- Weighted or deficit scheduling, WRED, and shared buffer pools. The queue
  model stays strict priority with a maximum rate.

## Units

### U1. Engine scale and flow statistics
Files: `docs/plans/2026-09-18-0000-feat-netsim-offered-load-streams-phase1-plan.md`
After: none
Landed: `cb5f0bae..b11463f5`

### U2. Stated egress buffers and tail drop
Files: `docs/plans/2026-09-18-0000-feat-netsim-offered-load-streams-phase2-plan.md`
After: U1
Landed: `6a51fd13..f805419d`

### U3. Stream package and the pull loop
Files: `docs/plans/2026-09-18-0000-feat-netsim-offered-load-streams-phase3-plan.md`
After: U1, U2
Landed:

### U4. Capture file as a stream source
Files: `docs/plans/2026-09-18-0000-feat-netsim-offered-load-streams-phase4-plan.md`
After: U3
Landed:

### U5. On-wire transmitter and the lab comparison
Files: `docs/plans/2026-09-18-0000-feat-netsim-offered-load-streams-phase5-plan.md`
After: U3
Landed:

Waves: U1 | U2 | U3 | U4 U5

## Verification

Each phase runs the verifier on its changed paths. After U3,
`go test -race ./src/common/netsim/...` and the phase 1 benchmark. After U5, a
lab run the owner approves in advance.

## Definition of done

- [ ] Every phase plan reads `implemented` and its `Landed:` line is filled.
- [ ] The direction record is accepted or its open points are resolved.
- [ ] The virtual device record's journey sentence, randomness sentence, and
      scenario gap are rewritten in the phases that make them false.
- [ ] `src/common/netsim/README.md` lists `stream`, and the `fabric` README
      states the scale limit.
- [ ] No plan labels in code.

## Open questions

- The direction record needs a person's acceptance.
- Phase 5's evidence on `src/edge/netpen/link` was not gathered. The research
  agent sent to read it was stopped by its model's security safeguard, and
  this plan did not route around that. Phase 5 is re-planned in its turn and
  starts by reading that package with the owner's say on which lane does it;
  `.claude/models/registry.yaml` lists `src/edge/netpen/**` as a sensitive
  path.
