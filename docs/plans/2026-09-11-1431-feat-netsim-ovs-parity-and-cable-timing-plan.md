---
title: Network Simulation, Open vSwitch Parity and Cable Timing - Plan
type: feat
date: 2026-09-11
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: code
amends: docs/architecture/2026-09-10-virtual-device-direction.md
---

# Network Simulation, Open vSwitch Parity and Cable Timing - Plan

This is a parent plan. Its units are phases, each with its own plan; the
first is implementation-ready and the later five are re-planned when their
turn comes. It continues
`docs/plans/2026-09-10-1815-feat-netsim-network-environment-plan.md`, whose
four phases built the switch, the fabric, spanning tree, and routing.

## Goal

The fabric models the time a frame spends on a cable, as serialization at
the negotiated rate plus propagation by the cable's length and medium, with
an override for the propagation term, so a run's clock reflects link speed
and distance and a port that is still sending delays the next frame. The
switch then closes the gaps a live Open vSwitch 3.3.9 showed on 2026-09-11:
a bounded MAC table with eviction, static entries at run time, QinQ and a
priority-tag policy, flood-only VLANs, protected ports, BPDU pass-through,
the spanning tree knobs OVS exposes, link aggregation with bond modes and
LACP, mirrors, ingress policing and egress queues, and IGMP and MLD
snooping. The means is one phase per capability cluster over the packages
the last plan left, with the cable model first because policing, queues,
and bond hashing only mean something once a frame takes time. The plan is
wrong if a consumer needs the simulator to reproduce one vendor's timing or
queueing rather than the standard's, or if the fabric turns out to need a
per-bit clock rather than event times.

## Decisions

- Time on a cable is two terms. Serialization is the frame's wire length
  in bits over the link's negotiated rate, where the wire length is the
  encoded frame padded to 60 octets plus 4 per VLAN tag, plus 4 octets of
  FCS, 8 of preamble and start delimiter, and 12 of interpacket gap, so
  84 to 1542 octets on the wire for an untagged frame (the frame, FCS,
  preamble, and delimiter figures from
  https://en.wikipedia.org/wiki/Ethernet_frame; the gap from IEEE
  802.3-2022 clause 4.4.2). Propagation is the
  length divided by the medium's velocity factor times the speed of
  light; `Cable.Delay`, when set, replaces the propagation term and
  nothing else. Why: the two terms answer different questions, one scales
  with frame size and rate and the other with distance, and a caller who
  measured a link's latency has measured propagation, not the frame size
  they will send later. User-directed on 2026-09-11.
- A port is busy while it serializes. The fabric keeps a per-endpoint
  clock of when the last transmission ends; a frame's transmission starts
  at the later of now and that clock, and its arrival at the far end is
  the transmission end plus propagation. A host's own port obeys the same
  clock. Why: this is the whole reason to model serialization; without
  the busy clock two frames flooded in one step would arrive as if the
  port had two transmitters.
- A cable has a medium, one of twisted pair, multimode fiber, singlemode
  fiber, or twinax, with twisted pair the default. Velocity factors: 0.64
  for twisted pair, the Cat 5e figure for 100BASE-TX and 1000BASE-T
  (https://en.wikipedia.org/wiki/Velocity_factor); 0.67 for both fibers,
  the minimum that page gives for 10BASE-FL and 100BASE-FX and the
  200,000 km/s that https://en.wikipedia.org/wiki/Optical_fiber gives for
  a fiber signal (5 µs per kilometre); 0.77 for twinax, the generic RG-8
  coaxial figure on the velocity factor page, since no twinax figure was
  found and a direct-attach cable is a foamed-dielectric coaxial pair.
  Why: the medium is what decides the delay, not the length alone, and a
  four-value enum covers every cable in the lab. The twinax factor is
  unconfirmed. Length 0 reaches every speed, and a speed reaches a length
  when the length is at most the reach.
- Length bounds the negotiated speed. Each medium has a reach per speed,
  and negotiation takes the highest speed both ends support, the cable's
  top allows, and whose reach covers the length; a length no supported
  speed reaches puts the link down with the reason `reach-exceeded`.
  The table, from https://en.wikipedia.org/wiki/Gigabit_Ethernet,
  https://en.wikipedia.org/wiki/10_Gigabit_Ethernet, and
  https://en.wikipedia.org/wiki/Twinaxial_cabling: twisted pair 100 m at
  every speed up to 10 Gbit/s (1000BASE-T, 10GBASE-T over Cat 6A), and
  10 and 100 Mbit/s on twisted pair alone;
  multimode fiber 550 m at 1 Gbit/s (1000BASE-SX on OM2, 1000BASE-LX on
  OM1 to OM3) and 300 m at 10 Gbit/s (10GBASE-SR on OM3); singlemode
  fiber 5 km at 1 Gbit/s (1000BASE-LX) and 10 km at 10 Gbit/s
  (10GBASE-LR); twinax 15 m at 10 Gbit/s (10GBASE-CR). Speeds above 10
  Gbit/s have no row and are refused on every medium until a phase adds
  them; that is the plan's stop line for the table, not a claim about
  the standards. Why: reach is the only effect of length on a working
  link besides delay, and a length past reach is how a lab cable fails.
- The relay's MAC table is bounded and evicts. A `bridge.Config.MaxEntries`
  of 0 means unbounded as today; above it a learn that would exceed the
  bound evicts the oldest dynamic entry and counts it. Static entries can
  be added and removed at run time through the switch and survive aging.
  The bridge counts learned, expired, evicted, and moved entries. Why: OVS
  shows these four counters and a `mac-table-size`, and a storm test
  needs the table to fill.
- VLAN awareness gains what OVS's `vlan_mode` and port options hold:
  `dot1q-tunnel` with a customer VLAN list and a service-tag EtherType,
  a priority-tag policy (never, if nonzero, always) for untagged egress,
  flood-only VLANs, protected ports that never forward to each other, and
  a bridge option to forward reserved-address frames when no spanning
  tree runs. The loader maps `SWITCHPORT_MODE_DOT1Q_TUNNEL`, which the
  schema already has. Why: these are the switch behaviors the lab's
  managed switches expose and the comparison found absent.
- Spanning tree gains auto-edge, protocol migration (`mcheck` and the
  migration delay), a transmit hold count, and per-port transmit,
  receive, and error counters. Legacy 802.1D STP is a compatibility mode
  of the same layer, entered per port on receiving a version 0 BPDU, as
  IEEE 802.1D-2004 clause 17.24 describes. Why: OVS runs both and a lab
  switch with STP forced on one port is the ordinary interop case.
- Link aggregation becomes a layer. A LAG has a mode (active-backup,
  balance by source MAC and VLAN, balance by the layer 2 to 4 fields
  with a hash basis), member up and down delays, and a rebalance
  interval, and runs LACP (IEEE 802.1AX) as a protocol on the timer
  facility spanning tree added: active, passive, or off, fast or slow
  timers, system and port priorities, an aggregation key, and a fallback
  to active-backup when no partner speaks. The lowest-name member rule
  the tree has today becomes active-backup with the lowest name as the
  primary. Why: which cable a frame takes is the question a LAG exists to
  answer, and OVS answers it with a hash and a protocol. User-directed on
  2026-09-11: LACP lands with the modes.
- Mirrors, policing, and queues are one layer over the port table. A
  mirror selects by source port, destination port, VLAN, or all, and
  outputs to a port or a VLAN with a snap length; ingress policing is a
  token bucket of rate and burst that drops above it; egress queues are
  strict priority by PCP with per-queue minimum and maximum rates over
  the serialization clock. Why: each changes which frames leave and when,
  and they need the busy clock the first phase adds.
- Snooping is the last phase and the first to cut. It adds IGMP and MLD
  codecs under `src/common/net`, a group table per VLAN with its own
  aging, and the flooding rules for unregistered groups and for reports.
  Why: it needs two codecs and a querier model, and nothing above it
  depends on it.
- The direction record's cable and run bullets change with the first
  phase: a cable is a medium and a length that give a propagation delay
  and bound the speed, a frame occupies its port for its serialization
  time, and a step enqueues each copy at the transmission end plus
  propagation rather than at the arrival time plus a latency. Why: the
  record's run bullet states the one-term model, and the two-thirds
  constant lives in the fabric's code and README; code and record change
  together.

## Requirements

Phase 1 claims 1 through 8, phase 2 claims 9 through 16, phase 3 claims 17
through 21, phase 4 claims 22 through 28, phase 5 claims 29 through 33,
phase 6 claims 34 through 37. The phase plans carry the acceptance
examples; here they are one line each.

1. A tagged 64-octet frame on 300 m of multimode fiber at 1 Gbit/s
   arrives 704 ns of serialization plus 1494 ns of propagation after its
   transmission starts.
2. A cable with `Delay` set, on a link that negotiates, arrives after
   serialization plus that delay whatever its length or medium.
3. The same frame on 10 km of singlemode fiber at 10 Gbit/s arrives
   71 ns plus 49,786 ns later.
4. Two frames arriving at the same instant on two ports and flooded to
   one port leave it one serialization apart, and the second journey
   names the wait.
5. A twisted pair cable of 120 m negotiates no speed and both ends read
   `reach-exceeded`; 90 m of multimode fiber between ends supporting 1 and
   10 Gbit/s negotiates 10 Gbit/s, and 400 m negotiates 1 Gbit/s.
6. A host's injection is a transmission on the host's cable end with a
   crossing in its journey, and a host whose link is down cannot inject.
7. `Diff` reports a medium or delay change on a cable, and `Validate`
   refuses a negative delay and an unknown medium.
8. The fabric README's example and the direction record's cable line
   state the new model with the worked numbers.
9. A MAC table with `MaxEntries` 2 evicts the oldest dynamic entry on the
   third learn and counts one eviction.
10. A static entry added at run time survives aging and is reported by
    `Entries` as static.
11. The bridge reports learned, expired, evicted, and moved counts.
12. A `dot1q-tunnel` port pushes the service tag on ingress and pops it on
    egress, keeping a customer tag in the allowed list and dropping one
    outside it.
13. The priority-tag policy `always` emits a VID 0 tag with the PCP on an
    untagged egress; `if-nonzero` only for a non-zero PCP; `never` strips.
14. A flood-only VLAN learns nothing and floods every frame.
15. Two protected ports never forward to each other in either direction.
16. A bridge with `ForwardBPDU` and no spanning tree floods a frame to
    01-80-C2-00-00-00 like any group address.
17. A port with auto-edge becomes an edge port after no BPDU for the
    migration delay and stops being one on the first BPDU.
18. A port with `mcheck` set sends RSTP BPDUs after a legacy BPDU held it
    in STP compatibility mode.
19. A port sends at most the transmit hold count of BPDUs per second.
20. Per-port transmit, receive, and error counts are reported and
    exported.
21. A version 0 configuration BPDU received on a port makes that port
    speak version 0 until `mcheck` or the migration delay.
22. A balance-slb LAG sends frames with different source MACs on
    different members and one source always on the same member.
23. A balance-tcp LAG sends one flow on one member and two flows differing
    only by port number on different members.
24. An active-backup LAG sends every frame on the primary and moves to the
    next member the moment the primary goes down.
25. A member that comes up waits `UpDelay` before carrying traffic and one
    that goes down waits `DownDelay` before it stops.
26. Two LACP-active LAGs across two cables converge to one aggregator
    within three fast-timer periods, and a member whose partner key
    differs stays out.
27. An LACP-active LAG against a peer that never speaks falls back to
    active-backup when configured to and carries nothing otherwise.
28. The loader reads `LagInterface` and the aggregation facet into the
    layer's configuration and exports the aggregator state.
29. A mirror selecting one source port copies every frame that port
    receives to the output port with the snap length applied.
30. A mirror with an output VLAN emits the copy tagged with that VLAN on
    every member of it.
31. Ingress policing at 1 Mbit/s with a 10 kB burst passes the burst and
    drops the frame that exceeds it with the reason `policed`.
32. Two frames queued on one port with PCP 7 and PCP 0 leave in that
    order regardless of arrival order.
33. A queue with a maximum rate below the link rate spaces its frames at
    that rate.
34. A switch with snooping enabled forwards an IPv4 multicast frame only
    to ports that reported the group and to the querier port.
35. An unregistered group floods or drops per the bridge's flood option.
36. A group entry ages out after the membership interval with no report.
37. MLD reports drive the IPv6 table the same way.

## Out of scope

- Collisions and half-duplex arbitration; half duplex only halves nothing
  and is carried as a label.
- Bit errors from attenuation or length; a cable past reach is down, not
  noisy, and the existing corruption fault stays the only error source.
- Cable categories below Cat 6A and fiber grades below OM3 as separate
  media; the medium table names one grade each.
- Speeds above 10 Gbit/s in the reach table, and speeds below 1 Gbit/s
  on fiber and twinax; a port that supports only 10 or 100 Mbit/s links
  over twisted pair alone.
- OpenFlow, controllers, tunnels, patch ports, conntrack, sFlow, NetFlow,
  IPFIX, BFD, CFM, DPDK, and every other OVS feature the comparison
  marked not applicable.
- A querier of the simulator's own; snooping learns from reports and
  floods queries.
- MSTP and per-VLAN spanning tree, BPDU guard, root guard, storm control,
  and MAC security, as the last parent plan listed.

## Units

### U1. Phase 1: cable timing and media

Files: `docs/plans/2026-09-11-1431-feat-netsim-ovs-parity-and-cable-timing-phase1-plan.md`
After: none
Landed: 2026-09-11 on `unify-netsim-plan-phases`, commits 015f0dee through the review-fix commit that follows the documentation commit.
Change: `fabric.Cable` gains `Medium` and `Delay`, the run gains
serialization, the per-port busy clock, and propagation by medium, the
reach table bounds negotiation, and the direction record's cable line is
rewritten.

### U2. Phase 2: relay parity

Files: `docs/plans/2026-09-11-1431-feat-netsim-ovs-parity-and-cable-timing-phase2-plan.md`
After: U1
Landed: 2026-09-11 on `unify-netsim-plan-phases`, commits d591c9e4 through the review-fix commit that follows the loader and records commit.
Change: the bounded MAC table with eviction and counters, run-time static
entries, `dot1q-tunnel`, the priority-tag policy, flood-only VLANs,
protected ports, BPDU pass-through, and the loader's tunnel mode.

### U3. Phase 3: spanning tree parity

Files: `docs/plans/2026-09-11-1431-feat-netsim-ovs-parity-and-cable-timing-phase3-plan.md`
After: U2
Landed: 2026-09-11 on `unify-netsim-plan-phases`, commits 8e19ffcd through the schema, loader, and export commit and the review-fix commit that follows it.
Change: auto-edge, protocol migration with legacy STP compatibility, the
transmit hold count, per-port BPDU counters, and their export.

### U4. Phase 4: link aggregation

Files: `docs/plans/2026-09-11-1431-feat-netsim-ovs-parity-and-cable-timing-phase4-plan.md`
After: U1, U3
Landed: 2026-09-11 on `unify-netsim-plan-phases`, commits 045f7878 through the schema, loader, and export commit and the review-fix commit that follows it.
Change: `netsim/vswitch/lag` with bond modes, hashing, member delays, and
LACP on the timer facility, an LACPDU codec under `src/common/net/lacp`,
and the loader's aggregation facet.

### U5. Phase 5: mirrors, policing, and queues

Files: `docs/plans/2026-09-11-1431-feat-netsim-ovs-parity-and-cable-timing-phase5-plan.md`
After: U1, U2
Landed: 2026-09-11 on `unify-netsim-plan-phases`, commits a88b3f4c through the fabric commit and the review-fix commit that follows it.
Change: `netsim/vswitch/traffic` with mirrors, ingress policing, and
egress queues over the serialization clock.

### U6. Phase 6: multicast snooping

Files: `docs/plans/2026-09-11-1431-feat-netsim-ovs-parity-and-cable-timing-phase6-plan.md`
After: U2
Landed:
Change: IGMP and MLD codecs under `src/common/net`, `netsim/vswitch/mcast`
with a group table per VLAN, and the relay's group forwarding rule.

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- src/common/net src/common/netsim docs/architecture
go test -race ./src/common/net/... ./src/common/netsim/... ./src/common/internal/netpenguard/...
```

Each phase plan carries its own commands. The second command keeps the
`gopacket` guard on every new codec.

## Definition of done

- [ ] Every phase's `Landed:` line filled and this plan's `status` set to
      `implemented` with an outcome note under its title.
- [ ] The netsim READMEs, `CONCEPTS.md`, and the direction record match
      what landed.
- [ ] No plan labels in code, comments, or commit messages.

## Open questions

- The twinax velocity factor of 0.77 rests on a coaxial figure. A
  direct-attach cable datasheet with a propagation delay per metre would
  replace it; until then the fabric README says the figure is a coaxial
  proxy.
- Whether speeds above 10 Gbit/s get reach rows (25GBASE-CR at 5 m,
  40GBASE-SR4 on OM3 at 100 m are the figures commonly cited) waits for
  a lab cable that needs them.
