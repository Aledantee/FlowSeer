---
title: Network Simulation Environment, Phase 2: Layer 2 Network Fabric - Plan
type: feat
date: 2026-09-10
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-10-1815-feat-netsim-network-environment-plan.md
---

# Network Simulation Environment, Phase 2: Layer 2 Network Fabric - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

`src/common/netsim/fabric` composes virtual switches, hosts, and the cables
between them into one network and runs frames through it one step at a
time: each step takes the earliest pending arrival, forwards it on its
device, and enqueues the copies that cross cables. A run halts after a step
budget, exposes a snapshot of every device and every frame in flight, and
reports a journey per frame. Cables have latency, a top speed, and declared
faults. Two fabrics run the same scenario and compare. The phase is wrong
if the first consumer needs a device that emits frames on its own timer or
a shared medium, since both need what this phase leaves out.

## Decisions

The parent's Decisions hold: one state per simulator, the stepped run with
its queue and snapshot, cables with declared faults, journeys and counters,
spec-built links. This phase adds the shape the re-planning starts from:

- `fabric.Config` holds switches by name (`vswitch.Config`), hosts by name,
  and cables. A cable joins two endpoints, each a node name and a port
  name, and carries a length, a top speed, and a fault. A host has one port
  and one address. Why: a host is an endpoint with an address and no relay,
  and modelling it as a one-port switch would give it a forwarding database
  it must never use.
- A host's port emits and accepts frames in one form: untagged, or tagged
  with one VID. Why: a station with several VLAN subinterfaces is several
  hosts on one port, which a shared endpoint expresses without a new kind.
- The fabric sets each switch port's operational state from its cable: Up
  when the peer is admin Up, the cable is not cut, and the speeds agree,
  else Down with the reason on the cable. A `vswitch.Config` inside a
  fabric leaves oper Unreported; the fabric rejects a spec that sets it.
  Why: in a network the cable decides, and two sources of one fact
  disagree.
- Speed negotiation between two Ethernet ends follows IEEE 802.3 Clause 28:
  two auto-negotiating ends take the highest speed both support and the
  cable allows, at full duplex; a forced end against an auto end takes the
  forced speed when the auto end and the cable allow it, else the link is
  Down with `speed-mismatch`; two forced ends must agree. A cable dead in
  one direction fails negotiation, so both ends are Down unless both are
  forced. An end without the Ethernet capability takes the peer's
  resolution. Why: the phase 1 speed rule resolves one end; the cable is
  where the other end exists, and Clause 28 needs both directions.
- `fabric.New(cfg)` builds the switches and resolves the cables; `Inject(at,
  origin, frame)` queues an arrival at a host's port or at a named device
  port and returns the frame's id; `Step()` processes one arrival and
  returns its journey entry; `Run(n)` steps at most n times; `Snapshot()`
  returns the clock, the queue, and per device the FDB, port states,
  allocations, and counters; `Report()` returns the journeys so far.
  Learning happens on `Step`; `Peek` variants do not exist at the fabric
  level, since a run is the record. Why: the same three shapes a single
  switch returns, lifted over time.
- The queue orders by arrival time, then injection sequence, then device
  and port name; a cable's latency is its length divided by two thirds of
  light speed, rounded to the nanosecond. Why: a fixed total order is what
  makes a run reproduce, and the latency gives parallel paths distinct
  arrival times without a caller-set delay.
- Cable faults are applied where a copy crosses: `cut` and one-way faults
  act on oper state, loss faults drop the copy at the cable with
  `cable-loss`, corruption lets the copy arrive and the receiving port
  drops it with `bad-frame` and raises its error counter; both reasons are
  `trace.Reason` constants the fabric declares. Every fault is a rule over
  the cable's own frame count, so it is deterministic. Why: the
  journey names where the frame was lost, which is what a person with a
  cable tester wants to know.
- Counters live in the fabric per device port: in, out, dropped by reason,
  errors. Why: a switch keeps no history beyond its forwarding database,
  and the run is where frames are counted as they cross.
- `Compare(a, b *Fabric, scenario)` runs the same injections on both and
  reports per frame both journeys and `Same` (equal deliveries in form and
  equal drop reasons); `Diff(a, b Config)` follows `vswitch.Diff` and adds
  cables added, removed, and changed (`length`, `top_speed`, `fault`) and
  hosts added, removed, and moved.

## Requirements

This phase claims requirements 22 through 32 of the parent, with these acceptance examples:

22. A run documents one frame across two switches. Acceptance: host `h1`
    on `sw1:1/1/1`, a cable from `sw1:1/1/24` to `sw2:1/1/24`, host `h2` on
    `sw2:1/1/1`, access ports of VLAN 10 and the uplinks tagged 10; a frame
    from `h1` to `h2`'s unknown address runs to completion in two steps and
    its journey lists the injection, the `sw1` trace, the cable crossing
    with its latency, the `sw2` trace, and one untagged delivery to `h2`;
    the reverse frame's journey shows `Forwarded` on both hops.
23. A run halts after a budget and its snapshot shows the transient state.
    Acceptance: the first frame of 22 with `Run(1)`; the snapshot's queue
    holds one arrival at `sw2:1/1/24` at the injection time plus the
    cable's latency, `sw1`'s FDB holds `h1` and `sw2`'s is empty; `Run(1)`
    again empties the queue and fills `sw2`'s FDB.
24. Several frames interleave by time. Acceptance: the cable of 22 is
    300 m long, so its latency is 1.5 µs; a frame from `h1` at t0 and one
    from `h2` at t0 + 1 µs step in the order `h1` on `sw1` at t0, `h2` on
    `sw2` at t0 + 1 µs, `h1`'s copy on `sw2` at t0 + 1.5 µs, `h2`'s copy on
    `sw1` at t0 + 2.5 µs, and every journey records its own steps only.
25. Operational link state comes from the cable. Acceptance: a port with no
    cable is oper Down and absent from flood sets; a port whose peer is
    admin DOWN is oper Down; a cut cable brings both ends Down with reason
    `cut`; a cable dead in one direction leaves both ends Down when either
    end auto-negotiates and both Up when both are forced.
26. Speeds negotiate across the cable. Acceptance: two auto-negotiating
    ends supporting up to 1000 and 10000 Mb/s resolve to 1000 on both; a
    cable with top speed 100 caps them at 100; forced 100 against auto
    resolves to 100; forced 100 against forced 1000 leaves both ends Down
    with `speed-mismatch`.
27. Cable loss and corruption are visible where they happen. Acceptance: a
    cable losing every second frame; two frames from `h1` to `h2`: the
    first delivers, the second's journey ends at the cable with
    `cable-loss` and no `sw2` hop; a corrupting cable instead ends the
    journey at `sw2:1/1/24` with `bad-frame`, and that port's `in_errors`
    reads 1 in the snapshot.
28. A loop is marked and the budget halts it. Acceptance: two switches
    joined by two cables in VLAN 10; an unknown-unicast frame from `h1`
    with `Run(50)` halts with the queue non-empty, the journey marks the
    first re-entry with `loop` naming the device and port, and the
    snapshot's counters show the storm on both uplinks.
29. Hosts receive frames in the form their port emits. Acceptance: a host
    on an access port receives untagged; a host on a port where VLAN 10 is
    tagged receives a C-TAG 10; a host sending tagged into an access port
    with admission `UNTAGGED_AND_PRIORITY_TAGGED_ONLY` sees a drop at hop
    one.
30. Counters export. Acceptance: after 22, `sw1:1/1/1` reads
    `in_unicast_packets` 1 and `sw1:1/1/24` `out_unicast_packets` 1, a
    drop at egress counts in `out_discards`, and the rows export as
    `InterfaceCounters` and pass `protovalidate`.
31. Two fabrics compare and diff. Acceptance: expected moves `sw2:1/1/1`
    to VLAN 20; the scenario of 22 run on both gives a delivery on current
    and none on expected, `Same: false` with both journeys; the diff lists
    the one switchport change under `sw2` and a cable's fault changed from
    `none` to `cut`.
32. Every phase 1 requirement holds inside a run. Acceptance: a one-switch
    fabric with a host cabled to every port the phase 1 frame tests name;
    the same frames injected at those hosts give the same outcomes and
    egress sets.

## Out of scope

Everything the parent lists, and loading a fabric from the network model.

## Open questions

- Whether negotiation lives in `phy` as `Negotiate(a, b Ethernet, cable)`
  or in `fabric`; the parent's layer rule says `phy`, since it is a rule
  over two Ethernet values, and the fabric calls it.
- Whether a host with no Ethernet capability of its own should count as
  auto-negotiating with every speed, as the Decision says, or as a fixed
  1000 Mb/s end.
- Whether snapshots are stored by the run at every step (memory grows with
  the run) or taken by the caller between `Run` calls; the second is the
  Decision, and a caller that wants every state runs one step at a time.
- Whether a corrupting cable's damage should be modelled on the bytes (a
  flipped bit the codec then rejects) or as a mark on the copy; the mark is
  simpler and the codec has no checksum to fail.
- Whether phase 1's `netmodel` gains a `Fabric` loader when the
  full-network view lands, or a sibling package does.
