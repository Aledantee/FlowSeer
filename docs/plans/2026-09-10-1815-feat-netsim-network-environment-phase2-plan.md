---
title: Network Simulation Environment, Phase 2: Layer 2 Network Fabric - Plan
type: feat
date: 2026-09-10
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: code
parent: docs/plans/2026-09-10-1815-feat-netsim-network-environment-plan.md
---

# Network Simulation Environment, Phase 2: Layer 2 Network Fabric - Plan

## Goal

`src/common/netsim/fabric` composes virtual switches, hosts, and the cables
between them into one network and runs frames through it one step at a
time: each step takes the earliest pending arrival, forwards it on its
device, and enqueues the copies that cross cables. A run halts after a step
budget, exposes a snapshot of every device and every frame in flight, and
reports a journey per frame. Cables have latency, a top speed, and declared
faults. Two fabrics run the same scenario and compare. The means is one
package over the landed `vswitch` API, with two-ended speed negotiation
added to `phy` and the counters export added to `netmodel`. The phase is
wrong if the first consumer needs a device that emits frames on its own
timer or a shared medium, since both need what this phase leaves out.

## Decisions

The parent's Decisions hold: one state per simulator, the stepped run with
its queue and snapshot, cables with declared faults, journeys and counters,
spec-built links. The phase 1 plan's landed API is the base: a switch is
built from `vswitch.Config` and answers `Forward(now, port, frame)` with a
`bridge.Result` that carries the resolved ingress port, the filtering
database id, and one `bridge.Egress` per egress port with the frame as it
leaves and a per-port drop reason. This phase adds:

- `fabric.Config` holds switches by name (`vswitch.Config`), hosts by name,
  and cables. A cable joins two endpoints, each a node name and a port
  name, and carries a length, a top speed, and a fault. A host has one port
  and one address. Why: a host is an endpoint with an address and no relay,
  and modelling it as a one-port switch would give it a forwarding database
  it must never use.
- A host's port emits and accepts frames in one form: untagged, or tagged
  with one VID. Why: a station with several VLAN subinterfaces is several
  hosts on one port, which a shared endpoint expresses without a new kind.
- The fabric sets each switch port's operational state from its cable
  before it builds the switch, overwriting whatever the spec carried: Up
  when the peer is admin Up, the cable is not cut, and negotiation
  succeeds, else Down with the reason on the link; a port with no cable is
  Down; a LAG port is Up when any member is Up. The port table is rebuilt
  through `port.NewBuilder` with the new states, since `port.Table` has no
  mutator. Why: in a network the cable decides, and a configuration loaded
  by `netmodel` arrives with the device's reported oper state, which the
  fabric replaces rather than rejects. The switch is built once, after the
  links are resolved, because `vswitch.New` reads the port table once and
  the fabric's configuration does not change during a run.
- Speed negotiation is `phy.Negotiate(a, b Ethernet, top uint64) Link`, a
  rule over two Ethernet values and a cable's top speed. An end is auto
  when its `Setting` is nil or `Setting.AutoNegotiation` is true, over its
  `SupportedSpeedsBPS`, or over every speed when that set is empty; an end
  with `AutoNegotiationSupported` false and an auto setting fails with
  `speed-mismatch`. An end is forced at `Setting.SpeedBPS` when
  `AutoNegotiation` is false; a forced speed of 0 fails. Two auto ends take
  the highest speed both support and the cable allows, at full duplex; a
  forced end against an auto end takes the forced speed when the auto end
  and the cable allow it; two forced ends must agree and fit the cable;
  two all-speeds auto ends with an unlimited cable take 1000 Mb/s, the
  speed a lab host without a stated capability runs at. Duplex is full
  for an auto outcome and the forced end's `Setting.Duplex` otherwise. A
  cable dead in one direction fails negotiation, so both ends are Down
  unless both are forced. An end with the Ethernet capability absent, and
  a host, is the all-speeds auto end. Why: the parent's layer rule puts a
  rule over two Ethernet values in `phy`; the fabric owns the cable and
  calls it. A fixed host speed would fail against a 10 Mb/s port for no
  reason a scenario intended. The host rule is unconfirmed.
- The negotiated speed lives on the fabric's link record.
  `vswitch.Switch.Speeds()` keeps reporting the one-ended resolution of
  phase 1, and `Snapshot.Links` reports the two-ended one. Why: writing the
  negotiated value into `phy.Ethernet.Observed` before building the switch
  would present the fabric's computation as a source's report, which
  `Observed` means.
- `vswitch.Switch` gains `Age(now time.Time)`, which ages the bridge's
  dynamic entries, and the run calls it before `Forward` on every step.
  Why: the parent's run ages at each arrival, and phase 1 exposed aging
  on `bridge.Bridge` only.
- The counters export stays in `vswitch/netmodel`, which imports `fabric`
  for the `Counters` type; `fabric` imports `vswitch` and never
  `netmodel`, so there is no cycle. Why: the common README names one
  package as the boundary that imports `generated/go/proto`, and a second
  boundary package would need a second exception.
- `fabric.New(cfg)` validates, resolves the links, and builds the
  switches; `Inject(Injection)` queues an arrival at a host's port or at a
  named device port and returns the frame's id; `Step()` processes one
  arrival and returns its journey entry; `Run(n)` steps at most n times
  and reports how many it took; `Snapshot()` returns the clock, the queue,
  the links, and per device the FDB, port states, allocations, and
  counters; `Report()` returns the journeys so far. A copy whose far end
  is a host never enters the queue: it is recorded as a delivery at
  enqueue time, dated at the crossing's arrival, so a frame's last hop
  costs no step and requirement 22 runs in two. Learning happens on
  `Step`; `Peek` variants do not exist at the fabric level, since a run is
  the record. Why: the same three shapes a single switch returns, lifted
  over time.
- Snapshots are taken by the caller between `Run` calls; the run stores
  none. Why: memory would grow with the run, and a caller that wants every
  state runs one step at a time.
- The queue orders by arrival time, then injection sequence, then device
  and port name; a cable's latency is its length divided by two thirds of
  light speed, rounded to the nanosecond. Why: a fixed total order is what
  makes a run reproduce, and the latency gives parallel paths distinct
  arrival times without a caller-set delay.
- Cable faults are applied where a copy crosses: `cut` and one-way faults
  act on oper state, loss faults drop the copy at the cable with
  `cable-loss`, corruption marks the copy and the receiving port drops it
  with `bad-frame` and raises its error counter; both reasons are
  `trace.Reason` constants the fabric declares. Every fault is a rule over
  the cable's own frame count, so it is deterministic. The mark is a field
  on the queued arrival; the codec has no checksum a flipped bit would
  fail, so damaging the bytes would prove nothing. Why: the journey names
  where the frame was lost, which is what a person with a cable tester
  wants to know.
- Counters live in the fabric per device port: octets and unicast,
  multicast, and broadcast frames in and out, discards by reason, and
  errors. Why: a switch keeps no history beyond its forwarding database,
  and the run is where frames are counted as they cross. The export to
  `InterfaceCounters` is `netmodel.InterfaceCounters(c fabric.Counters)`,
  since `netmodel` is the one package that imports `generated/go/proto`
  and `fabric` does not.
- A loop is a queue that never drains. The run keeps, per frame id, the
  set of (device, port) pairs its copies have entered; a copy entering a
  pair already in the set is marked `loop` in the journey and still
  processed, so the storm is visible in the counters and the budget is
  what halts it. Why: a marked re-entry with processing continued is what
  a real storm looks like, and stopping the copy would hide the storm.
- `Compare(a, b *Fabric, scenario []Injection, budget int) Comparison`
  runs the same injections on both with the same budget and reports per
  frame both journeys and `Same` (equal deliveries in host, form, and
  order, and equal drop reasons). It consumes both fabrics: they learn,
  count, and advance their clocks, so a comparison is made on fabrics that
  have not run. `Diff(a, b Config)` emits `vswitch.Diff` per switch with
  each change's subject key prefixed by the switch name and a slash, and
  adds a `switch` subject for a switch added or removed, a `cable` subject
  keyed by both endpoints for cables added, removed, and changed
  (`length`, `top_speed`, `fault`), and a `host` subject for hosts added,
  removed, and moved (`port`, `address`, `vlan`); an added or removed
  subject carries an empty `Field` and a nil `From` or `To`, as
  `port.Diff` does, and every fabric change carries the `fabric` layer
  the package declares. `Derive(cur *Fabric, cfg Config) (*Fabric, error)`
  builds a fabric from `cfg` with each switch derived through
  `vswitch.Derive` from its namesake in `cur`, so learned entries survive
  the way phase 1 decided. Why: the same three functions phase 1 gave a
  switch, lifted over a set of them; a fabric that peeked would need a
  copy of every switch and a queue, which is a second fabric.
- Loading a fabric from the network model waits, as the parent says; a
  sibling loader beside `netmodel` is the shape when it comes. Why: a
  `Neighbor` names a chassis and port and resolving those needs the
  inventory's full-network view.

## Requirements

This phase claims requirements 22 through 32 of the parent, with these
acceptance examples:

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
    300 m long, so its latency is 1501 ns (300 m over two thirds of
    299 792 458 m/s, rounded to the nanosecond); a frame from `h1` at t0
    and one from `h2` at t0 + 1 µs step in the order `h1` on `sw1` at t0,
    `h2` on `sw2` at t0 + 1 µs, `h1`'s copy on `sw2` at t0 + 1501 ns,
    `h2`'s copy on `sw1` at t0 + 2501 ns, and every journey records its
    own steps only.
25. Operational link state comes from the cable. Acceptance: a port with no
    cable is oper Down and absent from flood sets; a port whose peer is
    admin DOWN is oper Down; a cut cable brings both ends Down with reason
    `cut`; a cable dead in one direction leaves both ends Down when either
    end auto-negotiates and both Up when both are forced; a spec whose
    port says oper Up with no cable builds with that port Down; a LAG
    with one member cabled is Up.
26. Speeds negotiate across the cable. Acceptance: two auto-negotiating
    ends supporting up to 1000 and 10000 Mb/s resolve to 1000 on both; a
    cable with top speed 100 caps them at 100; forced 100 against auto
    resolves to 100; forced 100 against forced 1000 leaves both ends Down
    with `speed-mismatch`; forced 1000 on both ends over a cable with top
    speed 100 leaves both Down with `speed-mismatch`; a host against a
    port forced to 10 resolves to 10; two hosts on one cable resolve to
    1000.
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
    fabric with a host cabled to every port of the table, so no port is
    Down for want of a cable; the frames of the phase 1 tests injected at
    the hosts on their ingress ports give the same outcomes and egress
    sets.

## Out of scope

Everything the parent lists, and loading a fabric from the network model.

## Units

### U1. Two-ended negotiation

Files: `src/common/netsim/vswitch/phy/`
After: none
Change: `phy.Link{SpeedBPS uint64; Duplex Duplex; Reason trace.Reason}`
is the outcome of `Negotiate(a, b Ethernet, top uint64) Link`; a zero
speed with `ReasonSpeedMismatch` (`speed-mismatch`) is a failed link.
The classification of an end and the rule over two ends are the
Decisions text; `top` of 0 is unlimited. Test names describe the
behavior, never a requirement number.
Tests: `negotiate_test.go`, requirement 26's seven cases, two forced ends
that agree, a forced end with speed 0, and an auto setting on an end that
does not support auto-negotiation.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/phy`

### U2. Fabric configuration, links, and hosts

Files: `src/common/netsim/fabric/config.go`, `link.go`, `fabric.go`,
`src/common/netsim/vswitch/switch.go`, `switch_test.go`
After: U1
Change: `vswitch.Switch.Age(now)` ages the bridge's dynamic entries and
is a no-op on a hub. `fabric.Config{Switches map[string]vswitch.Config;
Hosts map[string]Host; Cables []Cable}`. `Host{Port Endpoint; Address
netaddr.MAC; VLAN *vlan.ID}` where a nil VLAN emits and accepts untagged
frames and a set one emits and accepts a C-TAG with that VID. `Endpoint{
Node, Port string}` names a switch port or, for the far end of a host's
cable, the host itself with an empty port. `Cable{A, B Endpoint;
LengthMeters float64; TopSpeedBPS uint64; Fault Fault}`. `Fault` is a
struct with `Kind` (None, Cut, DeadAToB, DeadBToA, LoseEveryNth,
LoseSequence, CorruptEveryNth) and its parameter (`N uint` or `Sequence
[]uint`). The package declares `Layer trace.Layer = "fabric"`.
`Config.Validate()` rejects a switch config that fails its own
`Validate`, a cable endpoint naming an absent node or port, a port on two
cables, a host on two cables or none, and a LAG port as an endpoint (a
cable joins a member; the bridge resolves a member to its LAG on ingress
and picks a forwarding member on egress). `Config.Clone()` deep-copies.
`Link{Cable; A, B LinkEnd}` with `LinkEnd{Endpoint; Oper port.LinkState;
Reason trace.Reason; Speed phy.Link}` is the resolved form; `New`
computes every link by the Decisions rule with reasons `cut`,
`peer-down`, `no-cable`, `dead-direction`, and `speed-mismatch` from
`phy`, then rebuilds each switch's port table through `port.NewBuilder`
with the oper states the links give (a LAG Up when any member is Up),
and builds the switches with `vswitch.New`. `New` sorts the cables by
their endpoint names so every later listing is stable. `Fabric.Links()`
returns the links in cable order.
Tests: `switch_test.go`, `Age` removing an aged entry and leaving a
static one; `link_test.go`, requirement 25's seven cases and requirement
26's host cases; `config_test.go`, each validation rule and a
table-driven `Validate` over a two-switch spec.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric`

### U3. The run: queue, injection, steps, journeys

Files: `src/common/netsim/fabric/run.go`, `journey.go`, `queue.go`
After: U2
Change: `Injection{At time.Time; Origin Endpoint; Frame ethernet.Frame}`
and `Inject(inj Injection) FrameID` queue an `Arrival{At, Seq, Device,
Port, FrameID, Frame, Corrupt bool}`, the exported shape the snapshot's
queue lists; an origin that is a host applies the host's form (adds its
C-TAG or leaves the frame untagged) and the arrival is at the switch port
the host is cabled to; an origin that is a device port is a capture
replay and takes the frame as given. The queue is a slice kept sorted by
(At, Seq, Device, Port). `Step() (Entry, bool)` pops the first arrival,
records `loop` when its (device, port) is already in that frame's entered
set, drops a corrupt arrival with `bad-frame`, calls `Age(at)` then
`Forward(at, port, frame)` on the device, and for each egress that is
not dropped enqueues one copy at `at + latency` on the cable's far end,
applying the cable's fault to that crossing (`cable-loss` ends the copy
with a journey entry; a corrupting crossing sets `Corrupt`); a copy whose
far end is a host never enters the queue and is recorded at once as a
`Delivery{Host, At, Frame}` dated at the crossing's arrival. `Run(n int)
int` steps until the queue is empty or n steps ran and returns the count.
`Journey{FrameID; Injection; Entries []Entry; Deliveries []Delivery}` and
`Entry{At; Kind (Hop, Crossing, Loss, Delivery, Drop, Loop); Device,
Port string; Cable *Cable; Latency time.Duration; Result *bridge.Result;
Reason trace.Reason}`; `Report() []Journey` sorted by frame id.
`Snapshot()` returns `{Clock time.Time; Queue []Arrival; Links []Link;
Devices map[string]Device}` with `Device{Entries []bridge.Entry; Ports
[]port.Port; Power phy.Allocation}`; counters join in U4. The clock is
the `At` of the last step or the zero time before the first.
Tests: `run_test.go`, requirements 22, 23, 24, 27, 28, and 29, a device
port origin (capture replay) that takes a tagged frame as given, and
`Run(0)` returning 0 with the queue untouched.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric`

### U4. Counters and their export

Files: `src/common/netsim/fabric/counters.go`, `run.go`,
`src/common/netsim/vswitch/netmodel/counters.go`
After: U3
Change: `fabric.Counters{InOctets, OutOctets, InUnicast, OutUnicast,
InMulticast, OutMulticast, InBroadcast, OutBroadcast, InErrors,
OutErrors, InDiscards, OutDiscards uint64; Discards map[trace.Reason]
uint64}` per device port; `Step` counts an arrival on its port (in
octets and one of the three frame classes by destination address, or
`InErrors` for a corrupt arrival), each transmitted egress on its port
(out octets and class), each per-port egress drop as `OutDiscards`
with its reason, and a whole-frame drop as `InDiscards` with its reason.
`Snapshot.Devices[name].Counters map[string]Counters`.
`netmodel.InterfaceCounters(c fabric.Counters) *interfacev1.InterfaceCounters`
maps the twelve fields by name.
Tests: `counters_test.go` in `fabric`, requirement 30's counts and a
storm's growth over two `Run` calls; `counters_test.go` in `netmodel`,
the twelve fields round-tripping and `protovalidate.Validate` passing.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric src/common/netsim/vswitch/netmodel`

### U5. Compare, diff, the phase 1 frames inside a run, and the docs

Files: `src/common/netsim/fabric/compare.go`, `diff.go`, `derive.go`,
`src/common/netsim/fabric/README.md`, `src/common/netsim/README.md`,
`docs/architecture/2026-09-10-virtual-device-direction.md`
After: U4
Change: `Compare(a, b *Fabric, scenario []Injection, budget int)
Comparison` with `Comparison{Current, Expected []Journey; Same bool;
Steps [2]int}`, `Diff(a, b Config) []trace.Change`, and `Derive(cur
*Fabric, cfg Config) (*Fabric, error)` are as the Decisions say; a
switch in `cfg` with no namesake in `cur` is built fresh. The `fabric`
README shows
the two-switch scenario of requirement 22, its journey as a program
reads it, the snapshot after one step, then the link rule, the fault
kinds, the ordering rule, the reasons, and the single-thread contract.
The `netsim` README lists `fabric`. The direction record's Consequences
are checked against the landed API.
Tests: `compare_test.go`, requirement 31, `Same` true for equal fabrics,
and a derived fabric keeping the entries phase 1's requirement 19 keeps;
`replay_test.go`, requirement 32 over the phase 1 frame table (the table
lives in the test, transcribed from `bridge_test.go` and
`switch_test.go`, one host per port of the table).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim docs/architecture`

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim docs/architecture
go test -race ./src/common/netsim/... ./src/common/internal/netpenguard/...
```

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] The `fabric` and `netsim` READMEs and the direction record match the
      landed API.
- [ ] This plan's `status` set with an outcome note under its title, and
      the parent's `Landed:` line for this phase filled.
- [ ] No plan labels in code, comments, commit messages, or test names.

## Open questions

- A host counts as an all-speeds auto-negotiating end, and two such ends
  settle at 1000 Mb/s (see Decisions); unconfirmed. If a fixed host speed
  is wanted, `Host` gains a `Speed` and the negotiation takes it as a
  forced end.
- One phase 1 change rides in this plan (`vswitch.Switch.Age`) because
  the fabric is its first caller; the phase 1 plan is not amended, since
  its contract for a single switch is unchanged.
- Whether the queue should break a same-instant tie between two devices
  by device name (the Decision) or by injection order alone; the name
  rule is deterministic either way and matters only to the order of
  journey entries in a report.
