---
title: Virtual Device - Direction
type: direction
date: 2026-09-10
topic: virtual-device
status: proposed-direction
amends: docs/architecture/2026-09-09-mutation-shadow-projection-direction.md
---

# Virtual Device - Direction

## Context

FlowSeer holds a typed model of what a device is configured to do
(`net/interface`, `net/switching`) and, through the mutation journal, what it
is expected to do after an intent applies. Two questions keep coming back:
what does a frame do on this device as it stands, and what would it do after
the change. A third follows once links between devices are known: what does
it do across the network. The shadow projection record deferred the second
to a projection over `Config` checked by named invariants and ruled out Open
vSwitch and Batfish. It did not say who computes the projection or how a
forwarding question is answered.

## Decision

`src/common/netsim` is the home of network simulation. Its first simulator,
`vswitch`, builds a switch from a port table and the capabilities its caller
chooses, evaluates the standard's forwarding rules over it, and answers a
frame query as a trace. Its second, `fabric`, composes switches and hosts
over cables and runs frames through the network one step at a time, so a
run can be halted, inspected, and reported. What every simulator shares,
the trace package first, sits at the `netsim` level.

- The device is sized by its caller. A port table with caller-chosen names
  is the one place a port exists; every layer keys its attributes by port
  name and validates against the table. Port count, naming, speeds, and
  PoE budgets are inputs, because the lab alone holds three vendors that
  agree on none of them.
- A device is built from capabilities, and a capability is the presence of
  a layer's configuration with no boolean beside it: a relay, VLAN
  awareness, Ethernet speeds, PoE, link aggregation, spanning tree, and
  later routing. The
  switch derives its capability set from its configuration and rejects
  configuration of a capability it lacks. The ladder is hub, bridge,
  switch: a port table alone repeats every frame to every other port; the
  relay adds one filtering database with every tag payload; VLAN
  awareness adds classification and tag forms. This is the
  facet rule of the network model applied to the simulator, and it keeps
  one code path per capability rather than one per device model.
- One package per capability over the port table: `phy` for speeds and
  PoE, `bridge` for the relay and VLANs, later `routing`. A layer imports
  the port table and the shared trace package only; `vswitch` composes them,
  and `netmodel` owns the protobuf boundary. A new layer is a new package
  and a new field.
- A simulator holds one state. Comparison, diff, and deriving an expected
  state from a current one are functions over two values, on a switch and
  on a fabric alike, so a pair of fabrics composes pairs of switches.
- A simulation is a run: injected frames, each with a time and an origin,
  sit in a queue of pending arrivals ordered by time and then by a fixed
  tie-break; a step takes one arrival, forwards it on its device, and
  enqueues one copy per egress cable at the arrival time plus the cable's
  latency. The run halts after a caller's step budget, and a snapshot
  exposes the clock, the frames in flight, and every device's forwarding
  database, port states, and counters as values. Every frame's processing
  is a journey: hops, cable crossings, deliveries, and drops with reasons.
  This is the event model of ns-3 without goroutines. The queue also
  holds the wake-ups a layer schedules and the frames a device emits on
  its own, spanning tree first, under the same total order, so the single
  device stays a function of time, port, and frame and the run stays
  deterministic. A loop is a queue that does not drain; the run marks the
  re-entry and the budget halts it.
- A cable is a model of its own: a length that gives a latency, a top
  speed, and a declared fault (cut, one direction dead, a deterministic
  loss or corruption rule). Nothing in a run is random, so a run
  reproduces, and the journey names the cable where a frame was lost.
- The shapes come from prior art, recorded in
  [the prior art research](2026-09-10-network-simulation-prior-art-research.md):
  a trace is a list of per-layer operations as Packet Tracer shows them,
  ingress decides and egress rewrites as bmv2 splits them, the relay,
  forwarding table, tagger, and ports are separate parts as INET composes
  them, and a comparison carries a current and an expected trace side by
  side as Batfish's differential questions do. The switch is one `c-vlan`
  component of the IEEE 802.1Q YANG bridge model, so components and
  filtering-database ids are where a provider bridge or a multi-domain
  device grows later.
- The model is the standard's. Ingress classification,
  admission, ingress filtering, learning, aging, flooding, and egress tag
  form follow IEEE 802.1Q as the Q-BRIDGE-MIB and BRIDGE-MIB under
  `spec/mib/ietf/` describe them. A vendor deviation is a documented
  difference the trace can name.
- The library is pure. No goroutines, no wall clock, no I/O; the caller
  passes the time and holds the lock. A trace is a value, so two states can
  be compared and a result can be stored or shown.
- The core holds plain Go types; protobuf messages appear only in
  `netmodel`. The generated messages are shaped for what a device reports,
  with presence meaning unknown, while a rule needs every value decided
  and every default reported. They are pointer graphs that cannot key a
  map, and a forwarding evaluation is a few map lookups by port name, VLAN
  id, and a six-byte address. The model also lacks what only the simulator
  needs: links, hosts, a capability set, a trace. A `flowseer/netsim/v1`
  schema is written when a service carries a scenario or a trace over a
  wire, and it maps onto the same plain types through the boundary
  package. The value types the simulator computes over are common Go
  packages for every tree: `netaddr` for EUI addresses, `vlan` for VLAN
  ids and tags, `ethernet` for the frame and its codec. The collectors and
  the capture pipeline move to them in their own changes.
- It is the engine of the shadow projection. The projection record's
  "no emulator runs" means no third-party emulator; the virtual device is
  the projection, and a frame query joins the invariants as part of the
  preview once the device service wires it.
- It lives under `src/common` because both the device service, for the
  preview, and the edge, for replaying captured frames against the device
  they came from, import it, and neither wires it as a service module. The
  simulators keep `src/common`'s rule of importing nothing from
  `generated/`; `netmodel` is the documented exception, because a
  simulator nobody can load from the network model is a test fixture.

## Alternatives

- Open vSwitch or a kernel bridge in a namespace. They answer for their own
  dataplane from their own configuration, need root and Linux, and cannot
  be diffed against a typed expectation. Rejected in the shadow record; this
  record does not reopen it.
- Compute forwarding inside the device service's projection package. It
  would work for the gate and be unreachable from the edge, where a capture
  replay is the second consumer.
- Model vendor dataplanes. Every vendor in the lab differs somewhere
  (FastIron dual-mode ports, LANCOM management VLAN handling), and a model
  that tries to follow each becomes untestable; the standard's model with
  named deviations is checkable.
- Use the generated network model messages as the simulator's own types.
  One shape and free validation, at the cost of unknown-versus-decided
  semantics in every rule, allocation on every lookup, and a schema for
  links, hosts, and traces that nothing on a wire would read yet.
- A device model per feature set (an access switch, a core switch, an
  unmanaged bridge). Each would be a copy of the pipeline with steps
  removed; a capability set expresses the same devices as one pipeline
  with stages present or absent.

## Consequences

- `src/common/net/netaddr`, `src/common/net/vlan`, and `src/common/net/ethernet` hold
  the value types and the codec; `src/common/netsim/trace` holds the step,
  trace, and change records; `src/common/netsim/vswitch`
  holds `port`, `phy`, `bridge`, `stp`, `netmodel`, and the switch itself;
  `src/common/netsim/fabric` holds switches, hosts, cables, the run,
  journeys, snapshots, comparison, diff, and derivation. A
  `service.Module` leaf is written with the first host.
- The forwarding scope grows by capability: rapid spanning tree with
  `net/protocol/stp` as the first protocol layer, multicast filtering
  with its table, routing with `net/interface`'s VLAN interfaces and
  `net/ip`'s neighbor entries, LLDP and LACP on the same timer facility. Each is a new package under `vswitch` or a
  new rule set, not a rewrite.
- Links load from LLDP neighbors once the full-network view resolves a
  chassis and port id to a device; until then a fabric is built from a
  spec.
- An exhaustive differential, "every flow whose fate changes", is finite
  for a customer bridge: ingress port, classified VID, and destination
  class enumerate it. It is a later question over the same two traces.
- The virtual switch can later stand behind an SNMP or gNMI front so
  FlowSeer's own collectors have a fixture, the way OpenConfig's lemming
  serves its tests.
- The typed effects in `flowseer.device.access.v1` are applied to the
  expected `Config` by an adapter that lands when the first switching intent
  does.
