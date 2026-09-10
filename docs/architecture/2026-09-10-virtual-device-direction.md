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
the change. The shadow projection record deferred the second to a projection
over `Config` checked by named invariants and ruled out Open vSwitch and
Batfish. It did not say who computes the projection or how a forwarding
question is answered.

## Decision

`src/common/netsim` is the home of network simulation. Its first simulator,
`vswitch`, builds a switch from FlowSeer's own typed model and evaluates the
standard's forwarding rules over it; it keeps a current and an expected state
of one device and answers a frame query on both. What every simulator shares,
the frame codec first, sits at the `netsim` level, so a link, host, or fabric
simulator later joins as a sibling of `vswitch` rather than a rewrite of it.

- The device is sized by its caller. A port table with caller-chosen names
  is the one place a port exists; every layer keys its attributes by port
  name and validates against the table. Port count, naming, speeds, and
  PoE budgets are inputs, never constants, because the lab alone holds
  three vendors that agree on none of them.
- One package per layer over the port table: `phy` for speeds and PoE,
  `bridge` for switching, later `stp` or a routing layer. A layer imports
  the port table and nothing of another layer; the `vswitch` package composes
  them and owns the diff and the comparison, and `netmodel` owns the
  protobuf boundary. This is the
  facet-versus-table split of the network model record applied to
  computation, so a new layer is a new package and a new field.
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
- The model is the standard's, not a vendor's. Ingress classification,
  admission, ingress filtering, learning, aging, flooding, and egress tag
  form follow IEEE 802.1Q as the Q-BRIDGE-MIB and BRIDGE-MIB under
  `spec/mib/ietf/` describe them. A vendor deviation is a documented
  difference the trace can name, never a second model.
- The library is pure. No goroutines, no wall clock, no I/O; the caller
  passes the time and holds the lock. A trace is a value, so two states can
  be compared and a result can be stored or shown.
- Protobuf messages appear only at the load and export boundary. The core
  holds plain Go types so a forwarding evaluation costs a few map lookups
  and a state clone is a copy.
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

## Consequences

- `src/common/netsim/frame` holds the codec; `src/common/netsim/vswitch`
  holds `port`, `phy`, `bridge`, `netmodel`, and the switch itself. A
  `service.Module` leaf is written with the first host.
- The forwarding scope grows by layer and protocol: spanning tree with
  `net/protocol/stp`, multicast filtering with its table, L3 with a routing
  model. Each is a new package under `netsim` or a new rule set, not a
  rewrite.
- A multi-device fabric, when the full-network view exists, composes
  virtual devices over resolved links with an event queue on a virtual
  clock, as ns-3 and ns-x do; the single device stays a function of time,
  port, and frame, with no scheduler of its own.
- An exhaustive differential, "every flow whose fate changes", is finite
  for a customer bridge: ingress port, classified VID, and destination
  class enumerate it. It is a later question over the same two traces.
- The virtual switch can later stand behind an SNMP or gNMI front so
  FlowSeer's own collectors have a fixture, the way OpenConfig's lemming
  serves its tests.
- The typed effects in `flowseer.device.access.v1` are applied to the
  expected `Config` by an adapter that lands when the first switching intent
  does.
