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

`src/common/netsim` is the home of network simulation. The library divides
ownership across specialized packages:
- `trace` is an import leaf defining typed execution steps, configuration diff
  changes, producer-owned rule IDs, and semantic facts.
- `analysis` defines the trust contract: analysis readiness status, scoped
  issues, evidence catalogs, and assumptions.
- `vswitch` composes capabilities over a port table, validates and normalizes
  configurations, and returns forwarding results combining domain outcomes with
  analysis trust metadata.
- Capability packages under `vswitch/` (`port`, `phy`, `bridge`, `lag`, `stp`,
  `mcast`, `routing`, `traffic`) own their respective configurations, validation,
  normalization, cloning, diffs, and rule identifiers.
- `vswitch/netmodel` translates FlowSeer network model messages into virtual
  switch construction specifications, loading reports, and readiness metadata.
- `fabric` composes switches, hosts, and cables into a stepped network simulation
  recording traversal journeys.
- `internal/netsimtest` maintains an admitted, versioned analysis conformance
  corpus.

- The device is sized by its caller. A port table with caller-chosen names
  is the one place a port exists; every layer keys its attributes by port
  name and validates against the table. Port count, naming, speeds, and
  PoE budgets are inputs, because the lab alone holds three vendors that
  agree on none of them.
- A device is built from capabilities, and a capability is the presence of
  a layer's configuration with no boolean beside it: a relay, VLAN
  awareness, Ethernet speeds, PoE, link aggregation, spanning tree, and
  routing. The
  switch derives its capability set from its configuration and rejects
  configuration of a capability it lacks. The ladder is hub, bridge,
  switch: a port table alone repeats every frame to every other port; the
  relay adds one filtering database with every tag payload; VLAN
  awareness adds classification and tag forms. This is the
  facet rule of the network model applied to the simulator, and it keeps
  one code path per capability rather than one per device model.
- One package per capability over the port table: `phy` for speeds and
  PoE, `bridge` for the relay and VLANs, `routing` for routed interfaces
  and per-VRF tables. A layer imports the port table, the shared trace package,
  and shared network value or codec packages under `src/common/net`. Sibling
  capability layers do not import one another; `vswitch` composes them, and
  `netmodel` owns the protobuf boundary. A new layer is a new package and a new
  field.
- A simulator holds one state. Comparison, diff, and deriving an expected
  state from a current one are functions over two values, on a switch and
  on a fabric alike, so a pair of fabrics composes pairs of switches.
- A simulation is a run: injected frames, each with a time and an origin,
  sit in a queue of pending arrivals ordered by time and then by a fixed
  tie-break; a step takes one arrival, forwards it on its device, and
  enqueues one copy per egress cable; a copy is a transmission on the
  egress port that starts when the port is free and its queue's turn comes,
  in strict priority by PCP, takes the frame's serialization at the negotiated
  rate, and arrives after the cable's propagation. The run halts after a
  caller's step budget, and a snapshot exposes the clock, the frames in flight,
  and every device's forwarding database, port states, and counters as values.
  Every frame's processing is a journey: hops, cable crossings, deliveries,
  and drops with reasons.
  This is the event model of ns-3 without goroutines. The queue also
  holds the wake-ups a layer schedules and the frames a device emits on
  its own, spanning tree first, under the same total order, so the single
  device stays a function of time, port, and frame and the run stays
  deterministic. A loop is a queue that does not drain; the run marks the
  re-entry and the budget halts it.
- A cable is a model of its own: a length and a medium that give a
  propagation time and bound the negotiated speed, an optional delay that
  replaces the propagation term, a top speed, and a declared fault (cut,
  one direction dead, a deterministic loss or corruption rule). Nothing in
  a run is random, so a run reproduces, and the journey names the cable
  where a frame was lost.
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

### Result contract axes

Every analysis evaluation separates four orthogonal axes:

1. **Input validity**: Whether caller inputs satisfy structural and invariant
   checks. Invalid inputs (such as non-existent port references or malformed
   VLAN IDs) are rejected with a Go `error` before simulation begins.
2. **Domain outcome**: What the simulated dataplane did with the frame:
   `Forwarded`, `Flooded`, `Dropped`, or `Consumed`, along with a domain reason
   (such as `ingress-filter`, `port-down`, or `unicast-hit`).
3. **Analysis readiness**: The trust placed in the answer: `Complete`,
   `Incomplete`, `Exhausted`, `Unstable`, or `Unsupported`, derived from an
   untruncated collection of scoped issues.
4. **Semantic trace**: The sequence of capability-owned operations (`Step`) and
   configuration transitions (`Change`) recording producer rule IDs, affected
   subjects, typed facts, and opaque evidence references.

None of these axes may be inferred from another:
- An input can be valid while yielding an `Incomplete` analysis if live state
  was unobserved.
- A frame drop in the domain (for instance, an ingress filter drop or a known-down
  port) is an authoritative, fully trusted answer (`Complete`), not an analysis
  failure.
- A frame forwarded in the domain can rely on assumed fallback defaults,
  rendering the answer `Incomplete`.
- An execution stop reason or human prose string cannot substitute for typed
  rule IDs or semantic facts in traces.

### Construction specifications and singular normalization

Every capability package exposes one normalization function (`Normalize`).
Public constructors validate only after normalization.

To reproduce simulations without drift, `vswitch.ConstructionSpec` and
`fabric.ConstructionSpec` pair normalized configuration with non-configuration
seeds (such as preloaded forwarding database entries). Re-running a simulation
from its construction specification yields identical state and trace records.

### Uncertainty localization and model loading

`netmodel.Load` translates network model records into virtual switch construction
specifications:
- Errors are reserved for unconstructible inputs, such as empty interface lists
  or duplicate port names.
- Partial, unobserved, or conflicting data produces a constructible `Result`
  with non-Complete readiness metadata and detailed loading reports.
- Port operational status distinguishes `Up`, `Down`, and `Unknown`. Zero values
  normalize to `Unknown`. A port with `Unknown` operational status never forwards
  traffic (`port.Forwards() == false`) and reduces readiness to `Incomplete`
  strictly for its own scope.
- A port known to be `Down` is an authoritative domain drop with `Complete`
  readiness.
- Sibling scopes remain independent. An unobserved port on a switch does not
  degrade the `Complete` readiness of an unrelated known-up port on the same
  switch.
- Standard defaults (such as aging time or unconfigured VLAN priority tags) are
  recorded as explicit `Assumption` values linked to evidence in the catalog.

### Fabric link and port state, host acceptance, and journey metadata

The readiness rules above extend from one device to the fabric connecting
them.

- **Effective link and port state.** Every non-LAG switch port is cabled,
  listed in `Config.Uncabled`, or unresolved. A cabled port's link decides
  its state; `Uncabled` is `Down` with `no-cable`, a stated absence; neither
  is `Unknown` with `adjacency-unresolved`, since nothing says what, if
  anything, is attached. A configured `OperStatus` that disagrees with the
  derived state is kept for the reader (`Config()`, `Spec()`), but the
  derived state executes, and the disagreement adds an `oper-status-conflict`
  issue instead of the derived value silently overwriting it.
- **`Config.PhyAssumption`.** An opt-in medium and Ethernet profile that
  fills only what an end or cable leaves unreported: empty speeds, an
  unknown capability, a nil setting, or an unspecified medium. Filled values
  appear in `Fabric.Links()`; `Config()`, `Spec()`, and `Diff` show only the
  knob itself, and each link it fills carries one `Assumption` naming the
  facts, so a standards default runs only on the record.
- **Protocol link-state input.** `Switch.LinkChange` takes `port.LinkState`,
  not a boolean, so an `Unknown` link leaves the port `Unknown` instead of
  being coerced to `Down`; STP and LAG still hear it as not operational. The
  switch raises `protocol-link-unknown` on every port whose role or
  membership those protocols compute once one of their inputs is unknown; a
  later `Up` report clears it. Without this, an unknown redundant uplink
  would silently re-elect a spanning tree root with every dependent journey
  reading `Complete`.
- **Host acceptance.** A frame the pipeline delivers to a host is not
  necessarily one the host keeps. After arrival, the host checks VLAN
  form, destination MAC, and, for a host with an IP stack, the IP
  destination, each under a named clause (own address, broadcast,
  solicited-node group, promiscuous mode). `Journey.Deliveries` holds only
  frames a host accepted; a rejection or an undecodable header is a
  recorded outcome of its own.
- **Journey dependency metadata.** `Journey.Metadata` is captured per entry
  at record time, not read live from `Fabric.Metadata()`. Each entry folds
  in the issues and assumptions of its own dependencies: its endpoint, its
  cable, and, for a hop, the ports and scopes its forwarding result
  consulted. A later `SetFault` changes `Fabric.Metadata()` but leaves an
  already-recorded journey's trust as it was. A flood that skips an
  `Unknown` port still consulted it and still carries that port's issue,
  because the flood could have reached one more host had the port resolved.

### LAG bucket selection and multicast query observation

- **LAG buckets are runtime state, not a stateless hash.** A `BalanceSLB` or
  `BalanceTCP` selection buckets `hash & 0xff` into 256 slots, and each slot
  remembers which member last carried it. A bucket keeps its member while
  that member stays enabled, so a member fault reassigns only the buckets
  that were on the faulted member; the other buckets, and the flows hashing
  to them, are untouched (`ofproto/bond.c` `choose_output_member` and
  `get_enabled_member`, OVS `branch-3.3` at `73e38c8d`). A newly enabled
  member joins the back of the enabled list rather than the front, so
  reassignment after a fault cycles through survivors in a fixed order
  instead of piling every orphaned bucket onto one member. `Select` commits
  a bucket's assignment and the enabled list's rotation; `Peek` computes the
  same choice without committing either, so a preview cannot perturb the
  live bond. Active-backup keeps the member it last chose rather than
  failing back once the member it moved away from recovers; only a
  configured `Primary` returns traffic to a specific member. A
  `RebalanceInterval` elapsing on an unchanged, balanced bucket with two or
  more enabled members raises `lag-rebalance-unmodeled`: OVS would have
  measured load and could have moved the bucket, and netsim does not
  measure load.
- **The switch never queries, so an unobserved query it depended on degrades
  readiness instead of silently keeping full timers.** RFC 3376 §6.4's leave
  and report tables call for a "Send Q" action in some rows, but the switch
  does not emit that query itself, so nothing lowers a member's timer to the
  last member query time on the switch's own initiative. When a table row
  calls for a query, the VLAN has a router port, and no matching
  group-specific or group-and-source-specific query with the suppress flag
  clear arrives within the last member query time, `mcast-query-unobserved`
  marks the group's forwarding result Incomplete on
  `ProtocolScope(node, "mcast", "<vid>/<group>")` until state changes: a real
  querier's query would have pruned the group sooner, and the switch cannot
  tell whether one was sent and lost or never existed. With no router port
  there is no querier to have sent one, so the full membership interval is
  the real behavior and the result stays Complete.

### Spanning-tree information lifetime, guards, and the gate

- **Received information is bounded twice, and the hop count uses the BPDU's
  own max age.** A BPDU is accepted only while `MessageAge + 1 <= MaxAge`
  against the `MaxAge` the BPDU carries, not this bridge's configured one,
  because the received value is the root's and a fabric holds bridges with
  differing timers. A BPDU that fails the test is discarded rather than
  stored, so a BPDU naming a root that no longer exists stops refreshing the
  port's timer on every hop; the port's own information then expires on the
  `3 × HelloTime` silence bound and the roles are recomputed. UNH-IOL's RSTP
  conformance suite states the rule as a hop count with max age as its
  maximum, citing IEEE Std 802.1Q-2011 sub-clauses 13.23.6, 13.27.30, and
  13.28.
- **Internal information ages by remaining hops instead of message age.** A
  BPDU whose configuration identifier matches this bridge's own is internal;
  it is accepted while `RemainingHops > 1` and re-originated one hop short, on
  the CIST and every MSTI alike, so a claim naming a regional root that no
  longer exists stops refreshing after `MaxHops` hops rather than a fixed
  time. External information (an RST or Configuration BPDU, or an MST BPDU
  from a different region) keeps the message-age test above, and both kinds
  still expire in silence after `3 × HelloTime`. `MaxHops` defaults to 20,
  bounded 6 through 40.
- **A guard's outcome is a port state, not an issue code.** A port state an
  operator can see beats an issue code they have to look for, so BPDU guard
  disables the port with reason `bpdu-guard` until a link bounce, restricted
  role keeps a port out of root selection, restricted TCN stops a received
  change from propagating, and loop guard holds a port whose information
  expired in silence while it was Root, Alternate, or Backup in a discarding
  Alternate role with reason `loop-inconsistent` until any BPDU arrives, one
  the message-age bound discards included, so a peer sending only stale
  information is not covered. Loop guard is
  netsim's own design drawn from Cisco, Juniper, and Arista, and is inactive
  on an operationally edge port and on a shared link, where a port that stops
  hearing BPDUs is not evidence of a link broken in one direction. `LoopGuard`
  beside `RestrictedRole` or beside `AdminEdge` is refused at construction:
  a configuration whose halves contradict each other has no correct simulated
  answer.
- **The layer keys its state by tree and the gate answers per VLAN.** A port's
  forwarding state belongs to a spanning tree, and more than one tree can run
  over one port, so `Learns` and `Forwards` take a port and a VLAN and the
  layer maps the VLAN to a tree. With no region configured, one tree, the
  CIST, carries every VLAN, so the answers agree; with a region configured, a
  VLAN an instance claims maps to that MSTI instead, and a boundary port's
  answer for it still traces back to the CIST because an MSTI takes the CIST
  port's role there. The port identifier and the port key set stay
  bridge-global: the identifier appears on the wire, and the key set's order
  reaches the caller as the order of `Effects.Flush`.
- **Classification precedes the ingress gate.** Active topology enforcement
  belongs to the forwarding process that follows ingress classification, so
  the bridge classifies first and asks the gate with the classified VLAN. A
  gate-blocked frame therefore names the VLAN it was classified into, and a
  frame that fails classification reports the classification reason rather
  than `port-blocked`, because a frame the port would never have admitted is
  not a spanning-tree question. A frame dropped in classification consults no
  spanning-tree scope, since no tree state could have changed its outcome.
- **`Decode` reads a version 3 BPDU's MST body, and falls back to its RST
  prefix only when the payload is too short for one.** The configuration
  identifier, internal root path cost, remaining hops, and MSTI records come
  back filled whenever the payload holds enough octets; a truncated capture
  or an RSTP peer's 39-octet version 3 BPDU still decodes as its RST prefix
  rather than being refused. UNH-IOL's MSTP suite states that a compliant
  device must not validate a BPDU on its protocol version identifier (Test
  MSTP.op.1.3, citing IEEE Std 802.1Q-2011 sub-clause 14.4), which is why the
  fallback exists at all: refusing a short version 3 payload would leave a
  netsim bridge facing that peer with both ends Designated and Forwarding, an
  unbroken loop and a worse answer than the RST-prefix approximation.
- **A region is a name, a revision, and a digest over the VID-to-MSTID
  table, carried as a 51-octet configuration identifier.** The digest is
  HMAC-MD5 over the 4096-entry table, two big-endian octets per VID, keyed
  with a constant that appears in no other file in this repository; the
  all-zero table digests to `ac36177f50283cd4b83821d8ab26de62` and VID 10 on
  MSTID 1 with VID 20 on MSTID 2 to `9357ebb7a8d74dd5fef4f2bab50531aa`, which
  is what proves the construction matches the standard rather than some other
  one. A port is internal when the BPDU it last received carries this
  bridge's own configuration identifier, and a boundary port otherwise; an
  RST or Configuration BPDU is always external.
- **One priority vector type serves the CIST and every MSTI.** Its six
  components compare in order: root, external root path cost, regional root,
  internal root path cost, designated bridge, designated port. An RSTP or
  CIST vector takes its regional root from its root and leaves the internal
  cost zero, collapsing to the landed four-component RSTP order; an MSTI
  vector takes its root from its regional root and leaves the external cost
  zero, collapsing to clause 13.11's MSTI order. Neither needs a branch,
  because a constant leading component drops out of a lexicographic
  comparison, so a second vector type would only duplicate the comparator.
- **Only the CIST computes a boundary port's role; every MSTI takes the
  CIST's role there outright.** Electing an independent role from information
  a different region sent would let an MSTI disagree with the CIST about
  which link is blocked, which is the boundary a region name and revision
  exist to detect. netsim also reports no Master role: both the CIST and MSTI
  role MIB tables list exactly Root, Alternate, Designated, and Backup, so a
  boundary port's MSTI mirrors the CIST's Root under the label the MIB can
  express, the same forwarding answer under a different name.

### Route selection and recursive next hops

- **A lookup yields a candidate set, not a single winner.** Routes are ordered
  by prefix length, then by preference (the Cisco administrative distance,
  lower wins, with 0 reserved for connected routes so a static route floors at
  1), then by metric within one preference. Everything that matches the
  destination and ties on all three is kept, ordered by next hop and then
  egress interface, and recorded in the lookup fact beside the member that
  carried the packet. A lookup that kept only the winner could not answer
  "what else would have carried this", which is the planning question a
  failure analysis starts from. The set is capped at 64, FRR's limit; the
  rest are withdrawn as `max-paths`.
- **One member of the set carries the flow, chosen by a layer-3 hash.** An
  FNV-1a hash over the source address, the destination address, and for IPv6
  the flow label — no protocol octet, no ports — is reduced by RFC 2992
  hash-threshold. That is what an unconfigured router does, and netsim has no
  field saying someone configured it otherwise. Hash-threshold rather than
  modulo-N because a withdrawn or added path must move as few flows as
  possible: "which flows move when this path fails" is answered wrongly by an
  algorithm that moves nearly all of them. The hash is a pure function of the
  packet, so `Peek` and `Forward` agree and neither commits anything.
- **Recursion resolves when the table is built, and a route that cannot
  resolve is withdrawn rather than left to fail per packet.** A static route
  whose next hop is not on-link is resolved against its own VRF's table, to a
  depth of 8, and installs carrying the on-link pair resolution reached while
  `Route.NextHop` keeps the configured value for the fact and the diff. A
  route that self-recurses, exceeds the depth, resolves to nothing, or
  resolves only through a default route is not installed, and its packets fall
  through to whatever else matches with a Complete result: the configuration
  stays valid on real gear, so this is neither a construction error nor
  `Incomplete`, which is reserved for an input netsim does not have.
  `Layer.WithdrawnRoutes` reports each withdrawal with its reason and the
  chain of prefixes walked, because "why is my static route not being used" is
  the question this decision creates. A withdrawn route and a `neighbor-miss`
  stay separate failures.

### Semantic traces and producer-owned facts

Prose strings are rejected as trace comparison keys. Capabilities define typed
value types implementing `trace.Fact` (`TypeID()` and `Canonical()`). Diff
records (`trace.Change`) carry typed `From` and `To` facts. Rule identifiers
(`trace.RuleID`) are owned by capability producers without central registry or
capability enums. Tooling compares traces and diffs using typed equality.

### Conformance corpus admission

The test corpus under `src/common/netsim/internal/netsimtest` locks simulation
contracts through executable cases rather than golden-file snapshots. Every
admitted case must define:
- Stable case identifier.
- Use-case class (`planning`, `topology-shadowing`, or `troubleshooting`).
- Evaluated question.
- False answer prevented by the case.
- Current result description.
- Expected analysis status and domain outcome.
- Decisive trace rules, subjects, and semantic facts.
- Expected issue codes, scopes, evidence references, and assumptions when
  readiness is non-Complete.
- Deterministic reproducibility invariants.

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
  trace, and change records; `src/common/netsim/analysis` holds the trust
  metadata, issue scopes, and evidence catalog; `src/common/netsim/vswitch`
  holds `port`, `phy`, `bridge`, `stp`, `netmodel`, and the switch itself;
  `src/common/netsim/fabric` holds switches, hosts, cables, the run,
  journeys, snapshots, comparison, diff, and derivation;
  `src/common/netsim/internal/netsimtest` holds the versioned conformance
  corpus. A `service.Module` leaf is written with the first host.
- The forwarding scope grows by capability: rapid spanning tree with
  `net/protocol/stp` as the first protocol layer, multicast filtering
  with its table, routing with `net/interface`'s VLAN interfaces and
  routed ports, and `net/ip`'s neighbor entries, LLDP and LACP on the
  same timer facility. Each is a new package under `vswitch` or a
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

### Remaining capability gaps

The following areas remain outside the foundation established here:

- **Physical media fidelity and PoE dynamics**: transceiver-dependent speed
  resolution, link downshift behavior, and PoE transient allocation
  dynamics. Autonegotiation, reach, and PoE allocation over reported facts
  are modeled, and unreported facts stay unknown.
- **Topology identity and adjacency ambiguity**: resolving links from noisy,
  conflicting, or unmanaged LLDP and CDP neighbor records.
- **Protocol depth**: rapid spanning tree convergence state machines (RSTP and
  MSTP), LACP dynamic aggregation negotiations, IGMP/MLD querier election,
  dynamic IP routing (BGP and OSPF), and transport protocol behavior.
- **Scenario overlays and search**: high-level scenario injection DSLs, packet
  generation search spaces, and multi-journey exploration budgets.
- **Convergence guarantees**: automated loop detection and settling criteria
  across active fabric runs.
- **Comparison, fingerprints, failure cones, and blame**: dependency graphs,
  blast-radius cones, configuration change fingerprints, and automated blame
  attribution for forwarding regressions.
- **System boundaries**: control-plane service wiring, OpenTelemetry span and
  metric instrumentation, persistent run storage, and user interfaces remain
  out of scope.
