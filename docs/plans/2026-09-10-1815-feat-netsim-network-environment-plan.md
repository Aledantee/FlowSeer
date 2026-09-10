---
title: Network Simulation Environment - Plan
type: feat
date: 2026-09-10
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
---

# Network Simulation Environment - Plan

This is a parent plan. Its units are phases, each with its own plan; the
first is implementation-ready and the later two are re-planned when their
turn comes. It replaces
`docs/plans/2026-09-10-1624-feat-virtual-device-l2-switching-plan.md`, whose
single device is now phase 1.

## Goal

A Go library under `src/common/netsim` simulates a network environment:
hubs, bridges, and switches built from a chosen set of capabilities, hosts,
and the cables between them. A run injects one or more frames and documents their processing
device by device and cable by cable, can be halted after any number of
steps with every device's state open to inspection, and answers the same
question on a current and on an expected state as a report a person can
read and a program can compare. Layer 2 is the scope of the first
three phases, spanning tree included; layer 3 joins as one more capability
of the same device and one more stage of the same pipeline. The means is one package per capability
over a shared port table, a switch that composes the capabilities it was
built with, and a fabric that composes switches over links. The plan is
wrong if a consumer needs the simulator to answer for one vendor's dataplane
rather than the standard's, since the model would then be a vendor emulator.

## Decisions

- The simulator's core holds plain Go types; protobuf messages appear only
  in `netmodel`, the boundary package that loads a device from the network
  model and exports what the simulator learned. Why: the generated messages
  are shaped for what a device reports, with presence meaning unknown,
  while a rule needs every value decided and every default reported; they
  are pointer graphs that cannot key a map; and the model lacks what only
  the simulator needs (cables, hosts, a capability set, a trace), so a
  schema for those would have no reader on a wire yet. The direction
  record carries the full argument and the alternative. A
  `flowseer/netsim/v1` schema is written when a service carries a scenario
  or a trace, mapped through the same boundary package. User-directed on
  2026-09-10.
- A capability is the presence of a layer's configuration, with no boolean
  beside it: a switch built with `Bridge` set relays frames, with
  `Bridge.VLAN` set it is VLAN-aware, with `Phy.Ethernet` set it resolves
  speeds, with `Phy.PoE` set it allocates power, and with a port of kind LAG
  it aggregates. `vswitch.Capabilities()` derives the set from the config;
  validation rejects configuration that belongs to a capability the device
  does not have. Why: this is the facet rule of `CONCEPTS.md` applied to
  the simulator (a facet's presence is its own discriminator), and it keeps
  one code path per capability rather than one per device model. The
  capability names are the layer names the trace already carries.
- The capability ladder is hub, bridge, switch. A hub is a port table
  alone: it repeats every frame to every other forwarding port, learns
  nothing, and leaves tags untouched. A bridge adds the relay: one
  filtering database keyed by address alone, every tag payload. A switch
  adds VLAN awareness, and any of Ethernet, PoE, and LAG may join at any
  rung. Why: transparent bridging is what `dot1dTpFdbTable` describes
  (`spec/mib/ietf/BRIDGE-MIB:787`), in 802.1Q terms one filtering database
  id for every frame (`spec/mib/ietf/Q-BRIDGE-MIB:340`), and a repeater is
  the device below it; the lab's unmanaged switches and any media
  converter sit on the first two rungs. User-confirmed on 2026-09-10.
- A simulator holds one state. Comparison, diff, and derivation of an
  expected state from a current one are functions over two values:
  `vswitch.Compare(a, b, ...)`, `vswitch.Diff(a, b)`, and the same on
  `fabric`. Why: the earlier plan paired current and expected inside one
  `Switch`, which a fabric of many devices would have to reach into; a pair
  of fabrics composes pairs of switches for free.
- A simulation is a run over a fabric. The caller injects one or more
  frames, each with a time and an origin (a host, or a device port for a
  capture replay), and the run keeps a queue of pending arrivals ordered by
  time, then injection order, then device and port name. `Step` takes one
  arrival, ages and runs that device's `Forward` at the arrival time, and
  enqueues one copy per egress link at that time plus the cable's latency;
  a copy that reaches a host is a delivery. `Run(n)` steps at most n times
  and halts; the caller resumes or stops. `Snapshot` returns the clock, the
  queue with each frame's location, and every device's forwarding
  database, port states, allocations, and counters as values, so two
  snapshots diff. Why: a transient state (a frame in flight, an entry not
  yet learned, a storm building) exists only between steps, and a walk
  that runs to completion hides it. This is the ns-3 event model without
  goroutines. Phase 2's run has no timers; phase 3 adds them, because
  spanning tree is the first capability whose device emits frames on a
  schedule of its own. A loop is a queue that never drains: the run marks
  a re-entry of the same bytes at the same device and port as `loop` in
  the journey, and the step budget is what halts it.
- A cable is the link's physical model: a length, from which its latency
  follows at two thirds of light speed, a top speed, and a fault. Faults
  are declared, never random: none, cut, one direction dead, every nth
  frame lost, a listed set of frame sequence numbers lost, or every nth
  frame corrupted, which the receiving port drops as an error with its
  error counter raised. Why: the runs worth having are the ones where
  something is wrong, and a run must reproduce exactly.
- Every frame's processing is a journey: its injection, each hop's trace,
  each cable crossing with its delay or its loss, and each delivery or
  drop with a reason. A run's report is the journeys plus the final
  snapshot. Ports count octets and unicast, multicast, and broadcast
  frames in and out, discards by reason, and errors, and export as
  `InterfaceCounters`. Why: the report and the
  counters are what a person reads to diagnose, and the counters are what
  a real device would show for the same run.
- Spanning tree is the `stp` capability, a layer of its own that runs
  Rapid Spanning Tree as IEEE 802.1D-2004 specifies it, one instance per
  device, over bridges and switches alike. The layer owns the bridge id,
  the per-port path cost, role, and state, the BPDU codec, and the timers;
  the relay consults it before forwarding or learning on a port and
  flushes entries on a topology change, and a hub passes BPDUs like any
  frame. The run gains a timer facility for it: a layer schedules a
  wake-up at a time and emits frames from a port into the queue, and
  every BPDU is a frame with a journey. The `net/protocol/stp/v1` schema
  does not exist and is part of that phase, so the loader and the export
  wait for it. Why: redundant cables are the ordinary shape of a network
  and a run that storms on them answers nothing; RSTP is what every
  managed switch in the lab runs by default, and its convergence is the
  briefly present state the run exists to show. User-directed on
  2026-09-10.
- Links in phase 2 come from the caller's spec. Loading links from LLDP
  neighbors waits for the full-network view the shadow projection record
  names as its prerequisite, because a `Neighbor` names a chassis and a port
  and resolving those to a device needs the inventory. Why: the fabric is
  useful to tests and to the edge's capture replay before that view exists.
- Layer 3 is a capability of the same device: a routing package that owns
  routed VLAN interfaces, a forwarding table, and a neighbor table, and a
  pipeline stage entered when a frame's destination is the device's own
  address on a routed VLAN. Addresses are `net/netip` values. Phase 4 is
  re-planned when its turn comes; its Decisions here are the shape, not
  the API.
- The network value types are packages of `src/common` in their own
  right, for every tree to use: `netaddr` for EUI-48 and EUI-64 addresses
  as comparable arrays with parsing, formatting, and conversion to and
  from `net.HardwareAddr` and the proto octets; `vlan` for the VLAN id,
  priority code point, and 802.1Q tag values with their range rules; and
  `ethernet` for the Ethernet II frame, its tag stack, the EtherType
  constants, the reserved group addresses, and the codec. `netsim` imports
  them and adds nothing of its own to them. Why: `src/protocol/snmp`
  decodes `net.HardwareAddr`, a slice that cannot key a map, and the
  mappers write `Eui48Address` bytes; one comparable address type and one
  tag value shared by the collectors, the capture pipeline, and the
  simulator is what the network model record wants of an address type on
  the wire, brought to Go. Existing users move to them in their own
  changes. User-directed on 2026-09-10.
- `src/common/netsim` is the home, `frame` at its root, `vswitch` and
  `fabric` as siblings. The simulators import nothing from `generated/`.
  User-directed; the reasons are in the direction record.
- Three phases, written on purpose. Why: the request names a decided
  sequence, one device, then the network, then layer 3, each bounded enough
  for one plan and each depending on the one before; the `phases` reference
  of the plan skill describes this shape.

## Requirements

Phase 1 claims 1 through 21, phase 2 claims 22 through 32, phase 3 claims
33 through 37, phase 4 claims 38 through 41. The later phases carry their acceptance examples in their
own plans; here they are one line each.

1. The codec round-trips a tagged frame. Acceptance: decoding
   `01 00 5e 00 00 fb  00 11 22 33 44 55  81 00  a0 64  08 00` plus payload
   yields one tag with PCP 5, DEI false, VID 100, EtherType 0x0800, and
   encoding reproduces the bytes.
2. A port table is built to any size under a caller's naming. Acceptance:
   `Range("1/1/%d", 1, 24, port.Port{Kind: port.Physical})`, then
   `Range("1/3/%d", 1, 4, ...)`, then `Add(port.Port{Name: "mgmt"})` yields
   29 ports in insertion order; a second `Add(port.Port{Name: "1/1/1"})`
   fails with the duplicate name as an attribute.
3. The capability set follows the configuration. Acceptance: a config with
   ports and a bridge without a VLAN part reports `{relay}`; adding the VLAN
   part, an Ethernet map, a PoE model, and one LAG port reports `{relay,
   vlan, ethernet, poe, lag}`; a config with ports alone reports the empty
   set, validates, and is a hub.
4. Speeds resolve per port. Acceptance: a port supporting 10, 100, and 1000
   Mb/s with auto-negotiation on resolves to 1000 full; off with speed 100
   it resolves to 100; speed 2500 fails validation naming the port.
5. PoE allocation honours budget, priority, and limit. Acceptance: group 1
   with 60 W and three class-4 ports at critical, high, low: two allocate
   30 W and the third is denied with reason `budget`; at 90 W all three
   allocate; a class-4 port with limit 15400 mW is denied with `limit`.
6. Untagged ingress classifies to the PVID and learns. Acceptance: port
   `1/1/1` PVID 10, untagged {10}; an untagged frame from
   `00:11:22:33:44:55` to an unknown unicast floods to every other
   forwarding member of VLAN 10, not to `1/1/1`, and the FDB gains
   `(10, 00:11:22:33:44:55) -> 1/1/1` kind dynamic.
7. Admission and ingress filtering drop as 802.1Q and the MIB say. Acceptance:
   admission `TAGGED_ONLY` drops an untagged frame and a priority-tagged
   frame (C-TAG, VID 0) with reason `admission`; admission
   `UNTAGGED_AND_PRIORITY_TAGGED_ONLY` drops a frame tagged 20 and
   classifies the priority-tagged frame to the PVID; admission `ALL` admits
   all three; filtering true on a port without VLAN 20 drops a frame tagged
   20 with `ingress-filter`; filtering false forwards it within VLAN 20.
8. Egress tag form follows the port's set. Acceptance: VLAN 20 tagged on
   `1/1/2`, untagged on `1/1/3`; a frame tagged 20 from `1/1/4` leaves
   `1/1/2` with a C-TAG VID 20 carrying the ingress PCP and DEI and leaves
   `1/1/3` untagged.
9. A known unicast goes to one port, never back out the ingress port.
   Acceptance: after 6, a frame to that MAC in VLAN 10 from `1/1/2`
   egresses only `1/1/1`; from `1/1/1` it drops with `same-port`.
10. A down port neither ingresses nor egresses. Acceptance: `1/1/3` admin
    DOWN is absent from every flood set and a frame injected on it drops
    with `port-down`.
11. Dynamic entries age on the caller's clock; static entries do not move.
    Acceptance: an entry learned at t0 answers at t0 + 299 s and is gone
    after `Age(t0 + 301 s)`; a static entry survives, and a frame from its
    MAC on another port leaves it unchanged.
12. Reserved addresses drop. Acceptance: a frame to `01:80:c2:00:00:00`
    drops with `reserved-address` on every port and the FDB is unchanged.
13. A LAG forwards as one port. Acceptance: `lag1` with members `1/1/5` and
    `1/1/6`, VLAN 10 tagged; a frame ingressing `1/1/6` traces ingress port
    `lag1`, and a flood in VLAN 10 from `1/1/1` lists one egress `lag1` on
    member `1/1/5`; a `PhysicalInterface` with `lag_parent: "lag1"` and its
    own switchport facet loads as a member whose facet the report lists as
    skipped.
14. A bridge without the VLAN capability relays by address alone.
    Acceptance: four ports, no VLAN part; a frame tagged 20 from `1/1/1` to
    an unknown unicast floods to the other three ports with the tag
    untouched, the FDB gains the source under one filtering database, and
    a later untagged frame to that source from `1/1/3` egresses `1/1/1`
    only; the trace's classify step names the single database. The same
    two frames on a hub (ports only) each flood to the other three ports
    untouched, with no FDB and no `learn` step.
15. Configuration of an absent capability is rejected. Acceptance: a bridge
    config with switchports and no VLAN table fails validation naming the
    first port; a PoE port naming a group the model lacks fails naming the
    group; a switchport naming a LAG member fails naming the member.
16. Loading from the network model infers capabilities and reports every
    assumption. Acceptance: interfaces with switchport facets and no
    Ethernet facet load with `{relay, vlan}` and the report says so; a
    `SwitchportFacet` with `untagged_vlan_ids: [30]`, no `pvid`, and no
    `frame_admission` loads as PVID 30, admission ALL, both listed as
    defaults by port name; loading with a wanted set of `{relay}` drops
    every switchport facet and lists each as skipped.
17. The FDB and PoE state export as `net/switching` and `net/phy` rows.
    Acceptance: after 6 the export holds one `FdbEntry` (`vlan_id: 10`,
    that MAC, `interface_name: "1/1/1"`, `DYNAMIC`, `ACTIVE`); after 5 a
    `PseBudget` for group 1 and a `PoeFacet` per port with
    `allocated_power_milliwatts`; every message passes `protovalidate`.
18. Two switches compare a frame. Acceptance: current has `1/1/2` untagged
    in VLAN 10, expected moves it to 20; a frame from `1/1/1` to an unknown
    unicast in VLAN 10 reaches `1/1/2` on current and not on expected, and
    the comparison reports `Same: false` with both traces.
19. A derived expected switch keeps learned entries the new configuration
    still admits. Acceptance: the current switch of 18 holds dynamic
    `(10, a) -> 1/1/2` and `(10, b) -> 1/1/1`; deriving with the expected
    config keeps the second and drops the first.
20. The diff names every changed field across layers. Acceptance: the pair
    of 18 diffs to two changes on `1/1/2`, `untagged_vlan_ids` `[10]` to
    `[20]` and `pvid` 10 to 20, with field names spelled as in the proto
    schema; a port added in expected diffs as a port added; a PSE group
    budget change diffs as one `power_milliwatts` change; a bridge that
    gains a VLAN part diffs as a capability added.
21. An oversized frame drops at egress. Acceptance: `1/1/2` has MTU 1500;
    a 1600-byte payload frame flooded in its VLAN lists `1/1/2` as dropped
    with `mtu-exceeded` and still egresses the other members.
22. A run documents one frame across two switches.
23. A run halts after a budget and its snapshot shows the transient state.
24. Several frames interleave by time.
25. Operational link state comes from the cable.
26. Speeds negotiate across the cable.
27. Cable loss and corruption are visible where they happen.
28. A loop is marked and the budget halts it.
29. Hosts receive frames in the form their port emits.
30. Counters export.
31. Two fabrics compare and diff.
32. Every phase 1 requirement holds inside a run.
33. A ring of bridges converges to a tree with one alternate port, and a frame crosses it once.
34. Convergence is observable step by step, with roles and states per port in every snapshot.
35. A cut cable re-converges onto the alternate port and flushes the affected entries.
36. A hub inside the ring is transparent to the protocol.
37. Bridge and port protocol state export through the new `net/protocol/stp/v1` schema.
38. A frame to the device's own address on a routed VLAN is routed to another VLAN.
39. A missing neighbor is an outcome, not a flood.
40. TTL exhaustion drops.
41. A host with an IP stack sends through its gateway.

## Out of scope

- Multiple spanning tree instances (MSTP, per-VLAN variants), classic
  802.1D-1998 timers beyond what RSTP keeps for compatibility, BPDU guard
  and root guard, provider bridging, `DOT1Q_TUNNEL`, multicast filtering
  beyond basic flooding, MAC security, and storm control.
- Shared media: a cable joins exactly two ends.
- LLDP and LACP as frame-emitting protocols; the timer facility phase 3
  adds is theirs to use later.
- Random behaviour of any kind.
- Loading links or hosts from the network model; `netmodel` loads one
  device.
- Dynamic routing protocols, ARP and ND exchanges, NAT, ACLs, and QoS;
  phase 4 routes over static tables.
- Applying a typed `MutationIntent`, a `service.Module` leaf, telemetry,
  and vendor behaviour such as FastIron dual-mode ports.

## Units

### U1. Phase 1: capability-built virtual switch

Files: `docs/plans/2026-09-10-1815-feat-netsim-network-environment-phase1-plan.md`
After: none
Landed:
Change: `src/common/netaddr`, `src/common/vlan`, `src/common/ethernet`,
`netsim/trace`, `netsim/vswitch` with `port`, `phy`, `bridge`,
and `netmodel`, the READMEs, the `CONCEPTS.md` entry, and the direction
record edits.

### U2. Phase 2: layer 2 network fabric

Files: `docs/plans/2026-09-10-1815-feat-netsim-network-environment-phase2-plan.md`
After: U1
Landed:
Change: `netsim/fabric` with cables, hosts, the run, journeys, snapshots,
counters, negotiation, comparison, and diff.

### U3. Phase 3: spanning tree capability

Files: `docs/plans/2026-09-10-1815-feat-netsim-network-environment-phase3-plan.md`
After: U2
Landed:
Change: `netsim/vswitch/stp`, the timer facility and BPDU journeys in
`fabric`, the relay's port-state gate, `spec/proto/flowseer/net/protocol/stp/v1`,
and the loader's and export's protocol state.

### U4. Phase 4: routing capability

Files: `docs/plans/2026-09-10-1815-feat-netsim-network-environment-phase4-plan.md`
After: U3
Landed:
Change: `netsim/vswitch/routing`, the IPv4, IPv6, and ARP decoders in
a common packet package beside `ethernet`, host IP stacks in `fabric`,
and the loader's routed interfaces.

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim docs/architecture src/common/README.md CONCEPTS.md
go test -race ./src/common/netsim/... ./src/common/internal/netpenguard/...
```

The second command proves the codec kept `gopacket` out of the main module.
Each phase plan carries its own commands.

## Definition of done

- [ ] Every phase's `Landed:` line filled and this plan's `status` set to
      `implemented` with an outcome note under its title.
- [ ] The netsim READMEs, the `src/common/README.md` row, `CONCEPTS.md`,
      and both direction records match what landed.
- [ ] No plan labels in code, comments, or commit messages.

## Open questions

- `AGENTS.md` says `src/common/` holds "no domain types". The core keeps
  it; `netmodel` imports `generated/go/proto`. Whether that line gets an
  exception or `netmodel` moves next to its first host is a policy decision
  for the user; the plan keeps `netmodel` in `src/common/netsim` and
  documents the exception in the common README.
