---
title: Network Simulation Environment, Phase 3: Spanning Tree Capability - Plan
type: feat
date: 2026-09-10
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: mixed
parent: docs/plans/2026-09-10-1815-feat-netsim-network-environment-plan.md
---

# Network Simulation Environment, Phase 3: Spanning Tree Capability - Plan

> Implemented. Every unit landed on 2026-09-11 through Herdr workers on
> Gemini 3.8 Flash, one unit per worker. The layer holds only the ports
> named in `stp.Config.Ports`; a port outside that map runs no protocol
> and forwards as phase 1 built it. The full verifier run the schema
> change asks for waits on the Docker daemon, which was not answering
> when the phase landed; every targeted run passed. The review's fixes
> landed on 2026-09-11: the fabric alone tells a layer about a link,
> the switch's `SetOperStatus` touches only the tables, a designated
> port on a point-to-point link falls back to the forward-delay ladder
> when no agreement comes, a link going down flushes the port, a
> repeated link report is ignored, `Derive` starts layers only after
> the cloned ones are in place, a LAG follows its up members alone,
> the export carries the values in effect, and a fabric with the layer
> refuses a zero `Start`.

## Goal

Bridges and switches gain the `stp` capability: Rapid Spanning Tree as
IEEE 802.1D-2004 specifies it, one instance per device, with the bridge id,
per-port path costs, roles, and states, the BPDU codec, and the protocol's
timers. The run gains what a device needs to act on its own schedule, a
wake-up in the queue and frames emitted from a port, so a ring of devices
converges step by step in the run, a cut cable re-converges, and every
snapshot shows each port's role and state. The protocol state loads from
and exports to a new `net/protocol/stp/v1` schema. The means is one
`stp` package under `vswitch` that computes over plain values and reports
what to send and when, a gate the relay consults, and a run that carries
wake-ups beside arrivals. The phase is wrong if the first consumer needs a
per-VLAN or multiple-instance tree, since those are a different state
machine over the same ports.

## Decisions

The parent's Decisions hold: spanning tree as a capability, the timer
facility in the run, the schema as part of the work. The landed phases
are the base: a switch answers `Forward` with a `bridge.Result`, the
bridge drops a reserved destination before classification, the fabric
runs a total-ordered queue of `Arrival`s and records a `Journey` per
frame, and `netmodel` is the one package that imports generated code.
This phase adds:

- `stp.Config{Priority uint16; Address netaddr.MAC; HelloTime, MaxAge,
  ForwardDelay time.Duration; Ports map[string]Port}` with `Port{Priority
  uint8; PathCost uint32; AdminEdge bool; PointToPoint PointToPointMode}`,
  where the mode is Auto, ForceTrue, or ForceFalse as
  `dot1dStpPortAdminPointToPoint` spells it (`spec/mib/ietf/RSTP-MIB:182`)
  and a path cost of 0 means the speed's recommendation, as
  `dot1dStpPortAdminPathCost` reads 0 (`spec/mib/ietf/RSTP-MIB:232-243`).
  The layer keys LAG ports, never members: the relay resolves a member to
  its LAG, and so does everything that talks to the layer; a LAG is up
  when any member is, point-to-point when every member is, and runs at
  its fastest member's speed. The
  bridge id is the priority (a multiple of 4096, default 32768) with the
  device's base MAC, as 802.1D-2004 clause 9.2.5 and 802.1t lay it out;
  a zero `Address` is refused by `Validate`, since two bridges with the
  same id cannot elect a root. The times default to 2, 20, and 15 s, the
  values `dot1dStpBridgeHelloTime`, `dot1dStpBridgeMaxAge`, and
  `dot1dStpBridgeForwardDelay` describe (`spec/mib/ietf/BRIDGE-MIB:482-532`).
  A port priority defaults to 128 and a zero path cost takes the
  802.1D-2004 recommendation for the port's speed: 2,000,000 at 10 Mb/s,
  200,000 at 100 Mb/s, 20,000 at 1 Gb/s, 2,000 at 10 Gb/s, 200 at 100
  Gb/s, and 20,000 for a speed the device could not resolve, since the
  negotiation rule of phase 2 gives an unstated end 1 Gb/s. Presence of
  `Config` is the `stp` capability, and `vswitch.Config.Validate`
  requires `relay`, since a hub repeats BPDUs rather than running the
  protocol.
  Why: these are the objects `dot1dStp` and `dot1dStpPortTable` carry,
  so the loader maps one to one.
- The path cost table is the 802.1D-2004 one (the 802.1t values), not
  the 1998 table. Why: every managed switch in the lab runs RSTP, whose
  standard adopted the 802.1t costs; a loader takes a reported cost as
  reported either way.
- The BPDU is the RST BPDU of 802.1D-2004 clause 9.3.3, carried in an
  802.3 frame with LLC DSAP and SSAP 0x42 and control 0x03, untagged, to
  01-80-C2-00-00-00: protocol id 0, version 2, type 2, a flags octet
  (topology change, proposal, two role bits, learning, forwarding,
  agreement, topology change acknowledgement), root id, root path cost,
  bridge id, port id, and message age, max age, hello time, and forward
  delay in 1/256 s, then a version 1 length of 0, padded to the 802.3
  minimum of 60 octets before the check sequence so the run's octet
  counters match a capture. The `ethernet` codec needs no change: a value
  below 0x0600 in the EtherType position is a length per IEEE 802.3
  clause 3.2.6, and `stp` reads and writes the LLC header and body inside
  the payload. The source address is the bridge address. A BPDU with a
  protocol id or version the layer does not run is dropped with
  `unsupported-bpdu`. Why: the codec pair stays a codec pair, and the
  frame the layer emits is the one a capture would show.
- The state machine is RSTP's, reduced to what a deterministic step-wise
  run needs and computed on demand: root selection from the best received
  priority vector per port, roles Root, Designated, Alternate, Backup,
  and Disabled, states Discarding, Learning, and Forwarding, the
  proposal and agreement handshake on point-to-point links, forward delay
  timers on shared links and where no agreement arrives, edge ports
  forwarding at once, received information aged out after three hello
  times, hello BPDUs from every Designated port each hello time, and a
  topology change that sets the flag for one hello time plus one second,
  the RSTP `tcWhile`, and flushes the dynamic entries of every port but
  the one it arrived on. A port is point-to-point when its mode forces
  it, or, in Auto, when the link is full duplex and the peer is not a
  hub. Classic 802.1D-1998
  compatibility (falling back to configuration BPDUs) is out of this cut.
  Why: the handshake is where convergence is fast and where a snapshot
  at the wrong moment shows a port Discarding on a link about to carry
  traffic, the state the run exists to show; the fallback needs a second
  BPDU format and a migration timer nothing in the lab exercises.
- The layer is a pure state machine over plain values with three entry
  points and no clock of its own: `Receive(now, port, bpdu)`,
  `LinkChange(now, port, up bool, pointToPoint bool, speedBPS)`, and
  `Wake(now)`; each returns the `Emission`s (a port and a frame) to send
  and the ports to flush, and `NextWake()` says when the next timer
  fires. Time enters only through those calls, so whoever drives the
  layer supplies the clock: the fabric from `Config.Start` and its
  queue, a standalone switch from `Switch.Start(now)`. `Layer.Clone()`
  copies the whole state, so a derived switch keeps its roles. Why: the
  switch and the fabric both drive it, and a layer that read a clock or
  held a goroutine could not replay from a capture.
- The relay consults a `bridge.Gate` before learning and before every
  egress: a Discarding port neither learns nor forwards, a Learning port
  learns only and then drops the frame, a Forwarding port both; with no
  gate every port forwards, which is what phase 1 built.
  `Bridge.FlushPorts(ports []string)` removes those ports' dynamic
  entries, and `Bridge.SetOperStatus(port, state)` changes one port's
  operational state in the table the relay reads, since a fault declared
  at a time reaches a live bridge. Why: this is the seam between two
  capabilities, and a gate the relay asks keeps the relay ignorant of
  which protocol answers.
- The switch intercepts a BPDU before the relay: a frame to
  01-80-C2-00-00-00 on a device with the `stp` layer goes to `Receive`
  and its trace ends `Consumed`, a new `trace.Outcome`; on a device
  without the layer the phase 1 reserved-address drop stands, and the
  fabric's journey still marks the frame as protocol traffic, so a ring
  that failed to converge shows where the BPDU died. A hub repeats it like
  any frame. The switch exposes `Start(now)` (tells the layer every
  port's link state from the table), `SetOperStatus(port, state)`
  (updates its table and the relay's; the caller follows with
  `LinkChange`, because only it knows whether a member's change moves
  its LAG), `Wake(now)`,
  `NextWake()`, `Drain()` (the emissions since the last drain), and
  `Roles()` (role and state per port). `vswitch.Derive` clones the
  current layer when the two configurations' `stp` parts diff empty and
  builds a fresh one otherwise, so `Compare` on a derived switch runs on
  a converged tree. Why: the emissions belong to the switch, not the
  result of one frame, since a wake-up produces them too.
- `fabric.Config` gains `Start time.Time`, the clock at which the fabric
  exists: `New` tells every layer its links at `Start`, injects the
  emissions that produces as protocol journeys dated `Start`, and
  schedules each device's first wake. The run carries wake-ups beside
  arrivals under the same total order: an `Arrival` with `Wake` set, no
  frame, frame id 0, and sequence 0, so a wake precedes any frame due at
  the same instant; one is outstanding per device, and re-scheduling
  removes the old one from the queue, so a stale wake never costs a step.
  After every `Forward` and `Wake`, the run drains the device's emissions
  and injects each as a frame from that device port with its own journey
  marked `Protocol`, so a BPDU crosses cables, faults, and hubs the way
  any frame does; a wake records nothing in any journey. Hellos keep the
  queue from ever draining, so a run halts on its budget; a caller reads
  convergence from two snapshots that agree, and the journeys grow with
  every hello, which `Report` copies, so a long run is a long report.
  Why: one queue keeps the run deterministic; a second queue for timers
  would need a merge rule.
- `Fabric.SetFault(a, b Endpoint, fault Fault) error` changes one cable's
  declared fault at the fabric's clock: the link is resolved again, both
  ends' operational states reach their switches through `SetOperStatus`,
  a member's LAG is recomputed from the members still up, and the fabric
  gives every layer that holds one of the ports one `LinkChange` at the
  clock; a report of the state the layer already holds changes nothing.
  Why: the parent says faults are declared, never random; a fault
  declared at a time is still declared, and a cut whose re-convergence
  keeps the protocol's state is what requirement 35 asks for.
- `spec/proto/flowseer/net/protocol/stp/v1` follows the `lldp` package:
  device-scoped rows that name interfaces, not facets embedded in
  `Interface`, since the network model record keeps protocol state in
  its own package. `BridgeId{priority, address Eui48Address}` as the
  MIB's eight-octet `BridgeId` reads (two octets of priority, six of
  address); `BridgeState` with the protocol version, the bridge id, the
  designated root, the root path cost, the root port's interface name,
  the times in force and the bridge's own times as
  `google.protobuf.Duration`, the topology change count, and the time
  since the last change; and `PortState` keyed by interface name with
  the admin priority, admin path cost (0 meaning automatic, as the MIB
  reads it), the path cost in use, role, forwarding state, designated
  root, cost, bridge, and port, admin edge and oper edge, the
  point-to-point mode and its oper value, and forward transitions. The
  enums are `PortRole`, `ForwardingState`, `PointToPointMode`, and
  `ProtocolVersion`; the BRIDGE-MIB's `blocking` and `listening` both
  read as Discarding. Why: the record reserves the package and says a
  protocol owns its own messages; a raw eight-byte id would hide the
  priority a loader sets.
- `netmodel.Load` takes the bridge state and the port states as two more
  inputs and builds `stp.Config` when a bridge state is present from the
  admin values (priority, admin path cost, admin edge, point-to-point
  mode), so loading what `netmodel.Stp(now, sw)` exported gives back
  the configuration it came from; `Stp` exports a `BridgeState` and one
  `PortState` per port the layer holds, with the priorities and times
  in effect and the time since the last topology change measured from
  `now`, or nil without the layer. A bridge state without an address or
  with a priority the layer cannot run is reported and skipped. Why:
  the loader already maps every other row one to one.

## Requirements

This phase claims requirements 33 through 40 of the parent (the parent's
range for this phase grows by three and phase 4's moves up), with these
acceptance examples. "Converged" below means two consecutive snapshots
report the same roles and states on every port; the queue never drains,
since Designated ports send hellos for as long as the run lasts.

33. A ring of bridges converges to a tree. Acceptance: `sw1`, `sw2`, `sw3`
    cabled in a ring, all with the `stp` capability, priorities 4096,
    8192, and 12288 so `sw1` is root; within `Run(200)` the ring is
    converged, `sw2` and `sw3` each have one Root port toward `sw1`, the
    cable between `sw2` and `sw3` has one Designated and one Alternate
    end, the Alternate port is Discarding, and an unknown-unicast frame
    from a host on `sw2` injected after convergence reaches a host on
    `sw3` once with no `loop` mark.
34. Convergence is observable step by step. Acceptance: the ring of 33
    with `Run(1)` repeated; the first snapshot shows every cabled port
    Designated and Discarding, later ones show proposals and agreements
    crossing as BPDU journeys marked protocol and ports moving to
    Forwarding, and the ports facing `sw1` reach Forwarding within
    `Start` plus 1 ms rather than after two forward delays.
35. A cut cable re-converges. Acceptance: after 33, `SetFault` gives the
    cable between `sw1` and `sw2` fault `cut`; within another `Run(200)`
    the ring is converged again, `sw2`'s former Alternate port is Root and
    Forwarding, the dynamic entries `sw2` and `sw3` learned through the
    old path are gone, and the frame of 33 injected again is delivered
    once over the new path.
36. A hub inside the ring is transparent. Acceptance: the cable between
    `sw2` and `sw3` is replaced by two cables through a hub (a switch with
    no bridge); the ring converges as in 33 with the same roles, the two
    ports facing the hub report point-to-point false, the Designated one
    reaches Forwarding only after two forward delays, and the Alternate
    one stays Discarding.
37. Protocol state exports. Acceptance: after 33, every device exports a
    bridge state naming `sw1`'s id as the designated root and a port state
    per cabled port whose role and forwarding state match `Roles()`, and
    each message passes `protovalidate`; loading those rows back builds a
    configuration whose `stp` part diffs empty against the original,
    since the admin values round-trip and the values in use are not
    configuration.
38. Two bridges on one cable converge in two exchanges. Acceptance: `sw1`
    (priority 4096) and `sw2` on one full-duplex cable; the layer alone,
    driven by hand, has `sw2`'s port Root and Forwarding after one
    proposal from `sw1` and one agreement back, and `sw1`'s port
    Designated and Forwarding on receiving the agreement.
39. A BPDU on a device without the layer is visible. Acceptance: `sw1`
    with the layer cabled to `sw2` without it; `sw1`'s first BPDU's journey
    is marked protocol and ends at `sw2` with `reserved-address`.
40. The codec round-trips an RST BPDU. Acceptance: the bytes of a proposal
    from bridge 4096 / 00:11:22:33:44:55 on port 1 with path cost 0 and
    the default times decode to those fields and encode back to the same
    60-octet frame, and a BPDU with version 0 is refused.

## Out of scope

Everything the parent lists, classic 802.1D-1998 compatibility, BPDU
guard and root guard, and loading a fabric from the network model.

## Units

### U1. The spanning tree layer

Files: `src/common/netsim/vswitch/stp/`
After: none
Change: `stp.Config` and `stp.Port` as the Decisions say, with
`Validate(ports port.Table)` rejecting a zero address, a priority that is
not a multiple of 4096, a port name absent from the table, and a LAG
member. `DefaultPathCost(speedBPS uint64) uint32` is the table.
`BridgeID{Priority uint16; Address netaddr.MAC}` with `Less` (priority,
then address) and `String`. `BPDU{Flags; RootID BridgeID; RootPathCost
uint32; BridgeID; PortID uint16; MessageAge, MaxAge, HelloTime,
ForwardDelay time.Duration}` with `Role`, `Proposal`, `Agreement`,
`Learning`, `Forwarding`, `TopologyChange`, and `TopologyChangeAck` read
from and written to the flags; `Encode(b BPDU, src netaddr.MAC)
ethernet.Frame` builds the untagged LLC frame padded to 60 octets and
`Decode(ethernet.Frame) (BPDU, error)` refuses a wrong LLC header,
protocol id, version, or type with `ReasonUnsupportedBPDU`
(`unsupported-bpdu`). `Role` (Root, Designated, Alternate, Backup,
Disabled), `State` (Discarding, Learning, Forwarding), and
`PointToPointMode` (Auto, ForceTrue, ForceFalse) are string types.
`New(cfg Config, ports port.Table) *Layer`; `Layer.LinkChange(now, port,
up, pointToPoint bool, speedBPS uint64) Effects`, `Layer.Receive(now,
port string, b BPDU) Effects`, `Layer.Wake(now) Effects`, and
`Layer.NextWake() (time.Time, bool)`, where `Effects{Emissions
[]Emission; Flush []string}` lists the frames to send (`Emission{Port
string; Frame ethernet.Frame}`) and the ports whose learned entries are
to be flushed. `Layer.Clone() *Layer` copies every port's state and
timers. `Layer.PortInfo(port) PortInfo{Role; State; PathCost; Designated
BridgeID; DesignatedPort uint16; DesignatedCost uint32; PointToPoint,
Edge bool; ForwardTransitions uint64}`, `Layer.Root() (BridgeID,
uint32, string)` (root id, root path cost, root port),
`Layer.TopologyChanges() (uint64, time.Time)`, and `Layer.Learns(port)
bool` and `Layer.Forwards(port) bool`, which is the gate the next unit
wires; a port the layer does not hold learns and forwards, so a port
without the protocol behaves as phase 1 built it. `stp.Diff(a, b Config)
[]trace.Change` covers `priority`, `hello_time`, `max_age`,
`forward_delay`, and per port `priority`, `admin_path_cost`,
`admin_edge`, and `admin_point_to_point`. The state machine is the
Decisions text: a
port coming up is Designated Discarding and, when point-to-point, sends a
proposal; the receiver of a superior proposal makes that port Root,
puts every other non-edge Designated port Discarding, and answers with
an agreement, on which the proposer's port goes Forwarding at once; a
Root port goes Forwarding once its bridge's other ports have synced; a
port with no agreement path waits a forward delay in Learning and
another in Forwarding; information ages out after three hello times and
the port recomputes; a Designated port sends a hello each hello time;
a role change to or from Forwarding on a non-edge port raises a
topology change that flags BPDUs for one hello time plus one second and
flushes. Every timer is a `time.Time` kept per port or per bridge, and
`NextWake` is their minimum. A `Layer` is not safe for concurrent use;
its doc comment says so.
Tests: `bpdu_test.go`, requirement 40, each refusal, every flag bit;
`layer_test.go`, requirement 38 driven by hand (`LinkChange` on both,
then the emissions of one fed to the other's `Receive`), a three-bridge
ring driven by a small hand-written scheduler in the test that feeds
emissions to the peer and calls `Wake` at `NextWake`, reaching the roles
of requirement 33; a link-down `LinkChange` on the Root port making the
Alternate Root with a flush; a shared port reaching Forwarding only after
two forward delays; an edge port Forwarding at once; aging of received
information after three hello times; a clone that keeps roles and moves
on its own; `Diff` over one bridge and one port change; and `Validate`'s
rules.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/stp`

### U2. The gate, the intercept, and the switch's schedule

Files: `src/common/netsim/trace/trace.go`,
`src/common/netsim/vswitch/port/port.go`,
`src/common/netsim/vswitch/bridge/bridge.go`, `bridge_test.go`,
`src/common/netsim/vswitch/config.go`, `switch.go`, `derive.go`,
`compare.go`, `diff.go`, `switch_test.go`,
`src/common/netsim/vswitch/README.md`
After: U1
Change: `trace.Consumed` joins the outcomes: the device took the frame
for itself. `port.LayerStp` (`stp`) joins the layer constants.
`bridge.Gate` is `interface{Learns(port string) bool; Forwards(port
string) bool}`; `Bridge.SetGate(g)` installs it, nil meaning every port
learns and forwards; `forward` asks the gate for the resolved ingress
port after the reserved-address check: a port that neither learns nor
forwards drops the frame with `ReasonPortBlocked` (`port-blocked`)
before classification, a port that learns and does not forward runs
classification and learning and then drops with the same reason, and an
egress candidate that does not forward is dropped with it per port;
`Bridge.FlushPorts(ports []string)` removes those ports' dynamic
entries; `Bridge.SetOperStatus(port string, state port.LinkState)`
rewrites that port in the relay's table. `vswitch.Config` gains `STP
*stp.Config`; `Capabilities` adds `port.LayerStp` when it is set, and
`Validate` refuses it without `Bridge`. `New` builds the layer and
installs it as the bridge's gate; the layer hears nothing until
`Start(now)`, which tells it every port's link state from the table (up
when the port forwards, point-to-point per the port's mode with Auto
reading true, speed from `Speeds()` or 0), or until the fabric's
`LinkChange`. `Forward` and `Peek` intercept a frame to
01-80-C2-00-00-00 when the layer is present, on the ingress port
resolved to its LAG: `Forward` decodes it, passes it to `Receive`,
applies the flushes to the bridge, keeps the emissions, and returns a
`bridge.Result` with outcome `Consumed` and one `classify` step naming
the layer; a decode failure drops with the codec's reason; `Peek`
returns the same result without calling the layer. `Wake(now)`,
`NextWake()`, `Drain() []stp.Emission`, `Roles() map[string]
stp.PortInfo`, `LinkChange(now, port, up, pointToPoint, speed)` (the
port resolved to its LAG), and `SetOperStatus(port, state)` are the
Decisions text. `Derive` clones the current layer when `stp.Diff` of the
two configurations is empty and builds a fresh one otherwise; `Compare`
is unchanged and therefore runs on the derived layer's roles. `Diff`
adds `stp.Diff` and the capability change. The vswitch README gains the
`stp` rung, the reasons, and the schedule contract.
Tests: `bridge_test.go`, a gate that blocks one port: no learning on
ingress there and a `port-blocked` drop, a learning-only ingress port
that learns and then drops, no egress to a blocked port, `FlushPorts`
leaving one port's entries, and `SetOperStatus` taking a port out of the
flood set; `switch_test.go`, capability `{relay, stp}`, `Validate`
refusing `stp` without a bridge, a BPDU consumed with an emission
drained, a BPDU on a LAG member consumed on the LAG, a hub repeating a
BPDU, `Diff` of a priority change, a `Peek` that leaves the layer
untouched, and a derived switch keeping a Root port Forwarding.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/trace src/common/netsim/vswitch`

### U3. Wake-ups, emissions, and a fault declared at a time

Files: `src/common/netsim/fabric/config.go`, `run.go`, `queue.go`,
`journey.go`, `fabric.go`, `derive.go`, `run_test.go`, `stp_test.go`,
`README.md`
After: U2
Change: `Config` gains `Start time.Time`. `Arrival` gains `Wake bool`; a
wake carries the device, an empty port, frame id 0, and sequence 0, so
`compareArrival` is unchanged and a wake precedes a frame due at the same
instant. `EntryWake` joins the entry kinds as what `Step` returns for a
wake; no journey records it. `Journey` gains `Protocol bool`. `New`
tells every switch with the layer its ports' link state at `Start` after
resolving the links (up from the link end, point-to-point from the mode
or, in Auto, from the peer being a host or a switch with a bridge over a
full-duplex link, speed from the link; a LAG from its members as the
Decisions say), drains the emissions that produces into protocol
journeys dated `Start`, and schedules each device's first wake; the
fabric keeps the outstanding wake per device and removes it from the
queue before queuing a new one, so no stale wake exists. `Step` takes
the wake branch before any journey lookup, calls `Wake(at)`, drains the
device's emissions, injects each as a frame from that device port with
a journey marked `Protocol` (through the same crossing, fault, hub, and
host rules as any frame), and re-schedules the device's wake when
`NextWake` moved; the same drain and re-schedule follow every
`Forward`. A `Consumed` hop ends the journey with a `Hop` entry and no
drop. `SetFault(a, b Endpoint, fault Fault) error` is the Decisions
text and refuses a pair that names no cable. `Derive` runs `Start` on
the new fabric at `cur`'s clock so a cloned layer's timers continue
from there. `Snapshot.Devices[name]` gains `Roles map[string]
stp.PortInfo`. `Report()` keeps every journey; a caller folds protocol
ones by the flag.
Tests: `stp_test.go`, requirements 33 through 36 and 39; `run_test.go`,
a wake before a frame at the same instant, a re-scheduled wake leaving
one entry in the queue, `SetFault` on a cable that was fine bringing
both ends Down in the next snapshot and refusing an unknown pair, and a
two-switch run without the layer unchanged by the wake facility (no
wake ever queued).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric`

### U4. The schema, the boundary, and the records

Files: `spec/proto/flowseer/net/protocol/stp/v1/` (`bridge_id.proto`,
`port_role.proto`, `forwarding_state.proto`, `point_to_point_mode.proto`,
`protocol_version.proto`, `bridge_state.proto`, `port_state.proto`,
`README.md`), the `buf generate` output under
`generated/go/proto/flowseer/net/protocol/stp/v1`,
`src/common/netsim/vswitch/netmodel/netmodel.go`, `export.go`,
`stp_test.go`, `testdata_icx7150_test.go`, `src/common/netsim/README.md`,
`docs/architecture/2026-09-10-virtual-device-direction.md`
After: U3
Change: the schema as the Decisions say, edition 2024 under
`docs/code-style-proto.md` and `docs/conventions/protobuf.md`, every
required field documented "Must be present.", enums with the RSTP names
and an unspecified zero, `BridgeId.priority` constrained to a multiple of
4096 below 65536 by a CEL rule, the admin path cost to 0 through
200,000,000 with 0 documented as the automatic default, as
`dot1dStpPortAdminPathCost` reads (`spec/mib/ietf/RSTP-MIB:232-243`),
the path cost in use to 1 through 200,000,000, the README stating what
the package owns and citing IEEE 802.1D-2004, RFC 4188 (BRIDGE-MIB), and
RFC 4318 (RSTP-MIB). `buf generate` produces the Go; `netmodel.Load`
takes `bridge *stpv1.BridgeState, stpPorts []*stpv1.PortState` after the
budgets and builds `stp.Config` from the admin values when the bridge
state is present, with `stp` inferred into the capability set and `relay`
implied; a port state naming an absent port or a LAG member is skipped
and reported; a port state without an admin path cost loads as 0, the
automatic default, and the report lists it. `netmodel.Stp(now
time.Time, sw *vswitch.Switch) (*stpv1.BridgeState, []*stpv1.PortState)`
exports the layer's state with the values in effect, and returns nil
without the layer. The ICX7150 fixture gains the capture's RSTP facts
(`docs/research/device-inventory/lab/labsw06-ruckus-icx7150.md:127`:
root reached via `lg1`, `1/1/12` and `1/3/1` designated forwarding) as a
bridge state and port states, and loads with `stp` in the set. The
`netsim` README lists the layer and the direction record's package list
for `vswitch` gains `stp`.
Tests: `stp_test.go` in `netmodel`, requirement 37 over the ring of 33
built through the fabric, a port state on a LAG member skipped, a
missing admin path cost reported, every message passing `protovalidate`;
`testdata_icx7150_test.go`, the capabilities now include `stp` and the
root port is `lg1`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/protocol/stp src/common/netsim docs/architecture`

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh --full
go test -race ./src/common/netsim/... ./src/common/internal/netpenguard/...
```

The full run is required by the schema change and the generator; it
needs the Docker daemon for the telemetry tier.

## Definition of done

- [x] Verifier green for every changed path and a full run after the
      generator ran.
- [x] The vswitch, fabric, and netsim READMEs, the schema README, and the
      direction record match the landed API.
- [x] This plan's `status` set with an outcome note under its title, and
      the parent's `Landed:` line for this phase filled.
- [x] No plan labels in code, comments, commit messages, or test names.

## Open questions

- The reduced state machine drops the RSTP `reRoot` and `sync` subtleties
  for a bridge with more than one Designated port already Forwarding when
  a superior proposal arrives; the plan puts every other non-edge
  Designated port Discarding, the conservative RSTP answer. A later
  phase that runs bigger rings may find convergence slower than a
  vendor's and refine the sync rule then.
- Whether `SetFault` should also accept a change of length or top speed;
  it takes a fault only, since only a fault has a run-time meaning the
  parent gave it.
- A wake-up's cost of one step means `Run(n)` budgets protocol time and
  frame time together; a caller counting frame hops reads the journeys.
- `Start` is a `fabric.Config` field rather than a `New` argument so a
  later emitting layer (LLDP, LACP, routing) inherits the same clock
  without another signature change; a phase that disagrees changes one
  field.
