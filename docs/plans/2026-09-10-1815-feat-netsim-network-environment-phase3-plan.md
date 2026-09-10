---
title: Network Simulation Environment, Phase 3: Spanning Tree Capability - Plan
type: feat
date: 2026-09-10
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: mixed
parent: docs/plans/2026-09-10-1815-feat-netsim-network-environment-plan.md
---

# Network Simulation Environment, Phase 3: Spanning Tree Capability - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

Bridges and switches gain the `stp` capability: Rapid Spanning Tree as
IEEE 802.1D-2004 specifies it, one instance per device, with the bridge id,
per-port path costs, roles, and states, the BPDU codec, and the protocol's
timers. The run gains what a device needs to act on its own schedule, a
timer facility and frames emitted from a port, so a ring of devices
converges step by step in the run, a cut cable re-converges, and every
snapshot shows each port's role and state. The protocol state loads from
and exports to a new `net/protocol/stp/v1` schema. The phase is wrong if
the first consumer needs a per-VLAN or multiple-instance tree, since those
are a different state machine over the same ports.

## Decisions

The parent's Decisions hold: spanning tree as a capability, the timer
facility in the run, the schema as part of the work. This phase adds the
shape the re-planning starts from:

- `stp.Config` holds the bridge priority (the id is priority and the
  device's base MAC), per-port priority, path cost (defaulting from the
  resolved speed per the 802.1D-2004 recommended values), admin edge, and
  the hello, max age, and forward delay times with their defaults of 2,
  20, and 15 s. Its presence is the `stp` capability and requires `relay`.
  Why: those are the parameters `dot1dStp` and `dot1dStpPortTable` carry
  (`spec/mib/ietf/BRIDGE-MIB`), so the loader maps one to one once the
  schema mirrors them.
- The state machine is the RSTP one: roles Root, Designated, Alternate,
  Backup, Disabled; states Discarding, Learning, Forwarding; the
  proposal and agreement handshake on point-to-point full-duplex links;
  the timer path where the link is not. Point-to-point is what a cable
  between two devices is, and a hub in between makes the ports on either
  side shared, which the fabric can tell from the cable's peer. Why: the
  handshake is where convergence is fast and where a snapshot at the
  wrong moment shows a port Discarding on a link that is about to carry
  traffic, the state the run exists to show.
- The relay asks the layer before forwarding and learning: a Discarding
  port neither, a Learning port learns only, a Forwarding port both. A
  topology change flushes the dynamic entries of every port except the
  one the change arrived on, as RSTP does. BPDUs, addressed to
  01-80-C2-00-00-00, are consumed by the layer on a device that has it,
  dropped as reserved on one that does not, and repeated by a hub. Why:
  this is the seam between two capabilities, and the reserved-address
  rule of phase 1 already keeps BPDUs out of the relay.
- The run gains `Schedule(at, device, layer)` and `Emit(at, device, port,
  frame)`, both from inside a step, and the queue holds wake-ups beside
  arrivals under the same total order. A BPDU's journey is like any
  other frame's, marked as protocol traffic so a report can fold it.
  Why: one queue keeps the order total and the run deterministic; a
  second queue for timers would need a merge rule.
- `spec/proto/flowseer/net/protocol/stp/v1` is written under
  `docs/conventions/protobuf.md` and the network model record: a bridge
  facet (protocol version, bridge id, root id, root path cost, the
  times) and a per-port facet (role, state, path cost, designated bridge
  and port), as primitives keyed by interface name. Why: the record
  reserves the package and says a protocol owns all of its own
  messages.
- `netmodel` loads the layer from the new facets and exports the bridge
  and port facets from the layer's state.

## Requirements

This phase claims requirements 33 through 37 of the parent, with these
acceptance examples:

33. A ring of bridges converges to a tree. Acceptance: `sw1`, `sw2`, `sw3`
    cabled in a ring, all with the `stp` capability and priorities that
    make `sw1` root; after the run drains, `sw2` and `sw3` each have one
    Root port toward `sw1`, the cable between `sw2` and `sw3` has one
    Designated and one Alternate end, the Alternate port is Discarding,
    and an unknown-unicast frame from a host on `sw2` reaches a host on
    `sw3` once with no `loop` mark.
34. Convergence is observable step by step. Acceptance: the ring of 33
    with `Run(1)` repeated; the first snapshot shows every port
    Designated and Discarding, later ones show proposals and agreements
    crossing as BPDU journeys and ports moving to Forwarding, and the
    times of those steps agree with the handshake on point-to-point
    links rather than with two forward delays.
35. A cut cable re-converges. Acceptance: after 33, the cable between
    `sw1` and `sw2` gains fault `cut`; `sw2`'s former Alternate port
    becomes Root and Forwarding, the dynamic entries on `sw2` and `sw3`
    learned through the old path are flushed, and the frame of 33 is
    delivered once over the new path.
36. A hub inside the ring is transparent. Acceptance: the cable between
    `sw2` and `sw3` is replaced by two cables through a hub; the ring
    converges as in 33, and the ports facing the hub take the shared-link
    path rather than the handshake.
37. Protocol state exports. Acceptance: after 33, every device exports a
    bridge facet naming `sw1`'s id as root and a port facet per cabled
    port whose role and state match the snapshot, and each message passes
    `protovalidate`.

## Out of scope

Everything the parent lists, and interoperation with classic 802.1D-1998
bridges beyond receiving their configuration BPDUs.

## Open questions

- Whether classic-STP compatibility (falling back to configuration BPDUs
  on a port that receives them) is in the first cut or a later one.
- Whether the per-port path cost default uses the 802.1D-2004 table or
  the 802.1t values a vendor may report; the loader can take the reported
  cost either way.
- Whether a device without the `stp` capability should drop a BPDU
  silently (the phase 1 reserved rule) or the journey should say the
  device does not run the protocol; the second helps a person reading
  a report of a ring that failed to converge.
- How the schema represents the bridge id: `Eui48Address` plus a
  priority, or the eight raw bytes.
