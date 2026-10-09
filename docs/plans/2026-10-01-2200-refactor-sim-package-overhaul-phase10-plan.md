---
title: Fabric, Device Seam, and Cable - Plan
type: refactor
date: 2026-10-01
artifact_contract: flowseer-plan/v2
execution: code
amends: docs/architecture/2026-09-10-virtual-device-direction.md
---

# Fabric, Device Seam, and Cable - Plan

## Goal

`fabric` schedules and records, and nothing else. It takes devices that are
already built, drives a switch, a host, and a reflector through `sim/device`
without naming one, asks `medium/cable` what a crossing costs, treats every
input as a queue event, and lets a held frame continue its own journey. A new
package, `sim/assembly`, builds the devices from configurations and holds
`Configure`, `Derive`, and `Diff`. The means is eighteen units: the seam and
what crosses it, the cable, the two endpoint devices, the checkpoint replay,
the split of `fabric` from its construction, then the event queue, the hold,
one executor, the scenario, and the close-out.

Stop condition: the parent's. If a layer-free `sim/device` cannot carry what
`fabric` reads from a switch, the package shape record is revised before
this phase writes code.

## Stop condition result

Checked on this tree. `Fb` is `src/common/sim/fabric`, `V` is
`src/common/sim/device/vswitch`, `L` is `src/common/sim/layer`.

`go list -f '{{join .Imports "\n"}}' ./src/common/sim/fabric` prints six
packages under `sim/layer/`: `bridge`, `mcast`, `phy`, `routing`, `stp`,
and `traffic`. It also prints the root `sim/layer` and `device/vswitch`.
What each is imported for, and what carries it instead:

| Import | Read for | Carried by |
| --- | --- | --- |
| `bridge`, `mcast`, `stp`, `routing` types | the fields of `fabric.Device` (`Fb/run.go:70-83`), `Fb/fingerprint.go`, `Fb/compare_state.go` | one state value the device returns, in types `sim/device` declares (U5) |
| `routing.Layer`, `routing.Config`, `routing.AddrFact`, `layer.Env` | the host's IP stack and acceptance (`Fb/fabric.go:181,517-526`, `Fb/config.go:271`, `Fb/accept.go:237,343`) | `device/host` and `device/reflector` (U7, U8) |
| `phy` | negotiation and link values (`Fb/fabric.go:1143-1345`, `Fb/link.go`), host and reflector Ethernet facts | `medium/cable` (U6), the endpoint devices |
| `stp.PointToPointForce*` | the administrative override, read twice (`Fb/fabric.go:1490-1538`) and a third time in `V/link.go:50-57` | the switch decides from a link report (U3) |
| `traffic` rules, reasons, facts | the tail drop and threshold steps (`Fb/run.go:955-961`, `Fb/fabric.go:1073-1093`), the policed test (`Fb/run.go:539`) | a decision the device returns with its step (U4) |
| `bridge.ReasonNoMember`, `bridge.Egress`, `bridge.Seed` | member selection in `transmit` (`Fb/run.go:870-883`), `Fb/journey.go:469`, `Fb/configure.go` | every emission names its member (U2), in a type of `sim/device` (U1). The seeds go with `Configure` to `sim/assembly` (U11) |
| `vswitch.Config`, `ConstructionSpec`, `Derive`, `Diff`, `NewWithSpec`, `Retention` | construction, `Configure`, `Derive`, `Diff`, and the `Configure` action of a scenario (`Fb/config.go:540`, `Fb/fabric.go:505`, `Fb/configure.go:75`, `Fb/derive.go:38`, `Fb/scenario.go:39,351`) | `sim/assembly` (U10, U11) |

Result: the seam carries all of it, and the stop condition does not fire.
The first pass found one sentence it could not meet while `fabric` built a
switch from `vswitch.Config`: the record's "on no other layer" and the
parent's `go list -deps` clause. The user's answer removes the cause, since
`fabric` no longer builds a device.

Whether the parent's R2 holds exactly, probed on this tree:

- `go list -deps ./src/common/sim/layer/phy` prints four packages under
  `src/common/sim`: `trace`, `port`, `layer`, and `layer/phy`.
- `sim/port` imports `errs` and `sim/trace`. `sim/analysis` imports the
  same two. `sim/stream` imports no package under `sim`.
- A test import is not a dependency. `go list -deps
  ./src/common/sim/device/vswitch` prints no `sim/fabric`, although
  `V/routed_egress_matrix_test.go:34` calls `fabric.New`.

After U11, `fabric` imports `sim/device`, `sim/medium/cable`, `sim/port`,
`sim/trace`, `sim/analysis`, `sim/stream`, and packages outside `sim`.
`sim/device` imports no layer. So R2 holds exactly as the parent writes it:
the direct imports hold no package under `sim/layer/`, and `go list -deps`
prints `sim/layer/phy` as the only one, reached through `medium/cable`.
One package stands beside it that the parent's sentence does not count. The
root `sim/layer`, which holds the contract's shared types, is in the list
because `L/phy/phy.go:46` takes a `layer.Env`. Its path is not under
`sim/layer/`, and it is not a capability package.

## Decisions

- The parent's Decisions apply. Both tests of the re-plan reference pass:
  the landed ranges of phases 8 and 9 (`c885895c`, `82fdcc23`) are
  ancestors of `HEAD`, and `main` holds this phase neither landed nor
  retired.
- `fabric` takes built devices and imports `sim/device` alone, with
  layer-free types there. Construction, `Configure`, `Derive`, and `Diff`
  move to callers or to a new assembly package. Cost: every caller of
  `fabric.New` changes, `sim/device` duplicates the state types of four
  layers, and this phase grows by an assembly package (decided by the user,
  2026-10-09).
- A host hands ARP and Neighbor Discovery frames to its stack and answers
  for its own addresses, and resolves nothing itself. A switch then
  resolves a host without a configured neighbor. A Neighbor Advertisement
  from a host has the Router flag clear, which RFC 4861 section 4.4 defines
  as "the sender is a router" (https://www.rfc-editor.org/rfc/rfc4861.txt),
  so the routing layer gains that input. `neighbor-unresolved` stays, since
  an absent host may be unmodelled. Cost: every routed fixture that
  configures a static neighbor for a host may drop it, and runs gain two
  steps per resolution (decided by the user, 2026-10-09).
- A replay of a run that continues a stepped fabric is a checkpoint.
  `ReplaySpec` holds a fork of each fabric taken before the scenario.
  Exact, including a cable's crossing count. Cost: a replay is an in-memory
  value and cannot be written out, and `search` keeps two forks per
  retained difference (decided by the user, 2026-10-09).
- `FlowStats.Held` counts the frames of the flow in a hold queue when
  `Flows()` is read. A released frame counts under `Delivered`, a timed-out
  one under `Drops`. Cost: the field is a gauge beside counters (decided by
  the user, 2026-10-09).
- One frame larger than the threshold raises `queue-buffer-unstated` on an
  empty queue, as the offered-load record (`:73-77`) has it. A lone jumbo
  frame on a port with no stated MTU reads `Incomplete`. Entry 11 is struck
  as the recorded rule (decided by the user, 2026-10-09).
- "Alone" in the import decision is read among device packages: `fabric`
  names `sim/device` and no package beneath it. `medium/cable` stays an
  import. Why: the shape record draws `medium/cable` into `fabric`
  (`:72-73,84`), and the parent's R2 names it as the path to `layer/phy`.
- The assembly package is `src/common/sim/assembly`, public, and not part
  of `netmodel`. Why: the answer's own words are "a new assembly package".
  The virtual-device record gives `netmodel` "the protobuf boundary"
  (`:70`), and `src/common/sim/netmodel/README.md:3-9` has it produce one
  switch's `vswitch.ConstructionSpec`. A caller outside `src/common/sim`
  builds a fabric from Go values
  (`src/edge/simload/test/integration/lab_test.go:137`), which an
  `internal` package would refuse.
- `sim/assembly` is functions over `*fabric.Fabric`, with no type that
  wraps one: `New`, `NewWithSpec`, `Spec`, `Derive`, `Configure`, `Diff`,
  `DiffSpecs`, and `Retention`. It keeps no state of its own. `Spec` reads
  each device's own specification and the configured port facts the fabric
  holds. Why: `docs/code-style.md` returns concrete types and adds nothing
  that was not asked for (`:184-186,553-556`), and a caller keeps holding a
  `*fabric.Fabric`, which `search`, `Compare`, and `simload` take.
- `sim/device` imports no package under `sim/layer` and not the root. It
  restates what crosses: the result of a receive, an emission, a copy, a
  drop, the hold token, a link report, and the state of the forwarding
  database, the groups, the router ports, the tree roles, the neighbors,
  and the power allocation. That is five layers, one more than the
  answer's cost names, since `vswitch.PowerResult` embeds `phy.Allocation`
  (`V/switch.go:30-33`). Why: one gate row then states the rule, and
  `sim/device` importing `layer/phy` would be a layer type in a package the
  answer calls layer-free. Types under `src/common/net`, such as
  `bpdu.Role`, cross as they are.
- A scenario keeps its `Configure` and `Mcheck` actions, and `fabric` keeps
  the executor and `Compare`. Each of the two actions carries a node and
  an `Operation`, an interface `fabric` declares with two methods: apply
  to a fabric, and diff against another operation. `sim/assembly` supplies
  both implementations. Why: the virtual-device record names
  `ActionConfigure` a scenario action (`:1063-1072`), the shape record
  puts comparison in `fabric` (`:61`), and `fabric` can no longer name
  `vswitch.Config` (`Fb/scenario.go:39,351`). `docs/code-style.md:184-186`
  defines an interface where it is consumed, once a second implementation
  or a test seam needs it. Both exist here.
- `fabric` keeps, per node, what a layer-free device cannot tell it: each
  port's Ethernet settings and its configured administrative and
  operational state, as a value of `medium/cable`, and whether the node
  repeats frames without a relay. `sim/assembly` supplies them. Why:
  `Fb/fabric.go:991-1015` reports a conflict between the configured and
  the derived state, and
  `docs/solutions/architecture-patterns/a-reconfigure-path-stores-configured-state-never-derived.md`
  keeps the two apart.
- `sim/assembly` derives each switch's port states from the cables before
  it builds the switch, as `build` does today (`Fb/fabric.go:492-508`),
  through the resolution `medium/cable` holds. `fabric` resolves the same
  cables with the same function. Why: building a switch from its
  configured states and correcting them by link reports is a behaviour
  change no test on this tree covers.
- A replay holds no contract string. `ReplayContract` and
  `ReplaySpec.Contract` go. Why: the run that returns a checkpoint builds
  it, and nothing reads one from outside the process, so the one check of
  the string (`Fb/run.go:1505`) can no longer fail.
- The plan stays whole. Why: every unit edits `Fb`, and
  `.claude/skills/plan/references/phases.md` keeps one cluster in one
  plan, with a new package joining the cluster of the units that import
  it. The plan is longer than the 300 lines that reference asks of a
  whole plan. The length is site lists and test names an implementer
  needs, and no split leaves two clusters. The units form a chain, since each edits `Fb/README.md` in the
  change that invalidates it (`docs/doc-style.md`). The ledger carries the
  phase across sessions.
- A reflector is a third package, `device/reflector`. Why: the shape record
  puts each device kind with behaviour in its own package under `device/`,
  and a reflector shares no rule with a host beyond the tag form
  (`Fb/accept.go:201-272` against `:299-431`). U8 adds it to the record's
  tree. The site lists of U7 and U8 follow
  `docs/solutions/architecture-patterns/a-third-kind-joins-a-two-kind-system-silently.md`.
- The egress queue stays in `fabric`. The two decisions that carry a
  `traffic` rule, the tail drop and the unstated-buffer threshold, are
  made by `traffic.Config` and reach `fabric` through the device with
  their step. Why: phase 7 left `traffic` the queue table, its lookups,
  the facts, and the rule identifiers, and no scheduler
  (`L/traffic/config.go:375,387`, `L/traffic/fact.go:48,61`). The shape
  record has a rule declared by the package that produces it. A queued
  item carries a journey, which no device may know.
- A flood lists no down member port in `Result.Egress`, as today. Why:
  `L/bridge/README.md:68` states the rule and its reason, a discard counted
  on a port no frame was sent to. Nothing in `fabric` needs the record.
- A released frame is mirrored where its egress port is in a mirror's
  `SelectDstPorts`. Why: the Open vSwitch schema describes
  `select_dst_port` as "Ports on which departing packets are selected for
  mirroring"
  (https://raw.githubusercontent.com/openvswitch/ovs/branch-3.3/vswitchd/vswitch.xml,
  Mirror table, read 2026-10-09), and `L/traffic/README.md:10-11` selects
  on a successful egress. `V/route.go:302-363` computes no copy at release.
- A fault's sequence positions keep today's count: every crossing of the
  cable since construction, both directions, protocol frames included
  (`Fb/run.go:1215-1216`). The README states it. Why: no source names
  another count, and `search` enumerates positions against this one.
- Every input is a queue event, and a held frame continues its own
  journey. Both are the parent's U10. `OriginRelease`, `JourneyReleased`,
  `heldAggregates`, and `findAndPopHeld` go. The virtual-device record
  (`:383-399`) and the offered-load record (`:88-89`) are amended in U13.
- A host arrival is a queue event. Why: the seam's receive verb takes a
  time, and the host-bound leg then records its crossing like any other
  (`Fb/run.go:1258-1274` returns before the crossing today). Step counts
  of every run that delivers to a host rise by one per delivery.
- `Scenario` loses `Spec` and `Budget`, and limits travel in one options
  value. An action is paired by a required identity, not by its index. Why: the
  inventory's Design entries state both, and `Compare` already discards
  the scenario's spec and overrides its budget (`Fb/compare.go:45-47,60-61`).
- `assembly.Config.Validate` is the contract for a configuration, and
  `normalizeConstructionSpec` calls it after the checks only a
  specification can fail (`Fb/fabric.go:310-397`). The fault and
  host-accept checks it repeats (`:318,343`) go. `fabric.New` refuses a
  topology on its own terms: an endpoint that names no node or no port of
  it, two cables on one port, and an invalid fault.
- `Switch.Roles` names the CIST (`V/inspect.go:148`, `L/stp/info.go:181`).
  `TreeRoles` keeps sampling VLAN 1 until `State` names the trees by
  identifier in U5.
- Entries 1 to 25 were re-read on this tree, not run. The fabric's `Fork`
  and queue order are not redesigned, as the outline decided.

## Requirements

R1. Every inventory entry ends as a test that fails before its fix, or is
struck with its commit or its decision. Example: entry 14, a scenario of
one `ActionRecord` compared on two fabrics that deliver it differently,
returns `Different` with one journey per side.

R2. The parent's R2, as written. `go list -f '{{join .Imports "\n"}}'
./src/common/sim/fabric` prints no package under `sim/layer/` and none
under `sim/device/`, and `go list -deps ./src/common/sim/fabric` prints
`sim/layer/phy` as the only package under `sim/layer/`. Example: a row of
`test/conformance/sim` fails when `fabric` imports `sim/layer/traffic` or
`sim/device/vswitch`, and a second fails when `sim/device` imports
`sim/layer/bridge`.

R3. A host injection takes effect at its own time. Example: on a stepped
fabric, injecting from one host at +10 s and then at +5 s transmits the
second frame first, and no `Entry.Wait` is negative.

R4. A release is attributed to the frame that was held. Example: frame 1
held for neighbor X and frame 2 for neighbor Y on one switch, Y resolves
first, and journey 2 is delivered while journey 1 is still held.

R5. `RunResult` reports the same status and issues for one run under
`RetainJourney` and under `RetainAggregate`. Example: a frame flooded past
an `Unknown` uplink reads `Incomplete` with `unknown-operational-status`
under both.

R6. A run of plain `Run` computes no fingerprint. Example: the scale
benchmark the fabric README records at 295 seconds against a 120-second
target is rerun, and its figure is recorded in the README with the command.

R7. A device is driven through one interface. Example: a host, a switch,
and a reflector on one fabric each receive a frame through
`device.Device.Receive`, and `Fb/run.go` holds no map lookup by node kind.

R8. Every frame a device returns for a LAG names its member. Example: an
ARP request for a next hop behind a routed LAG leaves with `Member` set to
the member the switch committed, and the fabric calls no selection.

R9. The fabric clock never moves backward. Example: an injection at a time
before the clock is refused before and after the first step, and a wake a
device reports before the clock is handled at the clock.

R10. A fabric built by `sim/assembly` runs as one built by `fabric.New`
does today. Example: every case of the corpus in `ST` passes with its
expectations unchanged across U10 and U11, and
`assembly.Spec(f)` of a fabric after a `Configure` that changes nothing
equals its value before.

R11. A replay reproduces a run that began on a stepped fabric. Example: a
fabric steps three frames across a cable whose fault loses position 4,
then runs a scenario of one injection. `Replay` of the result loses that
injection, as the run did.

## Out of scope

- A `Medium` interface, a radio medium, and an access point. The parent.
- Whether the layer engines move under `internal/`. The parent reopens it
  after this phase (Open questions).
- ICMP errors, a host that resolves its own next hop, and the Neighbor
  Discovery roles `L/routing/README.md` lists under Limits.
- A written form of a replay. The checkpoint decision leaves none.
- The review gaps of the switch composition phase that name no fabric
  file: the step of a release refused at a mirror output (`V/route.go:358`),
  the BPDU drop on an untracked port (`V/intercept.go:178`), the unread
  field in `V/layers.go:31`, and `badBPDU` (`V/intercept.go:240`).
- `netmodel`'s loaders. Phase 11. `netmodel` and `simtest` follow each
  renamed type in the unit that renames it.
- `fabric` and `sim/assembly` read configurations and scenarios built by
  FlowSeer's Go callers, trusted for shape. A frame from a capture is
  untrusted for shape: one a codec refuses is refused at injection and
  never panics.

## Inventory

Re-read on this tree. A struck entry names the commit that fixed it.

### Correctness, execution core

| # | State | Where now | Unit |
| --- | --- | --- | --- |
| 1 | Holds. The token exists and the fabric ignores it | `Fb/run.go:1351-1389`, `V/timers.go:19`, `L/routing/layer.go:915-916` | U13 |
| 2 | Narrower. Deferred before the first step when the queue holds an earlier arrival. Charged at call time otherwise | `Fb/run.go:332-341,1284` | U12 |
| 3 | Holds | `Fb/run.go:1760-1764`, `Fb/flow.go:92-98` | U14 |
| 4 | Holds. Pinned by `Fb/flow_internal_test.go:726-728` | `Fb/journey.go:226-230`, `Fb/run.go:1409-1433` | U13 |
| 5 | Wake half holds. ~~Drain half~~ fixed by `e3013067` | `Fb/fabric.go:861-863` | U3 |
| 6 | Holds | `Fb/run.go:175-177,303,400-402` | U12 |
| 7 | Holds | `Fb/run.go:1485-1497,1511` | U9 |
| 8 | Holds | `Fb/accept.go:221,238` | U8 |
| 9 | Holds. Decided: documented | `Fb/run.go:1215-1232`, `Fb/README.md:832-833` | U6 |
| 10 | Holds | `Fb/attach.go:124,140-154` | U12 |
| ~~11~~ | Struck as the recorded rule (Decisions). A stated port MTU moves the threshold | `Fb/run.go:971-978`, `Fb/fabric.go:1058` | U4 pins it |
| 12 | Holds, in both executors | `Fb/run.go:1615-1617`, `Fb/compare.go:164-172` | U14 |
| 13 | Holds | `Fb/record.go:27-46` | U16 |

### Correctness, comparison and scenario

| # | State | Where now | Unit |
| --- | --- | --- | --- |
| 14 | Holds | `Fb/compare.go:163-170`, `Fb/comparison_location.go:102` | U14 |
| 15 | Holds. Pinned by `Fb/compare_test.go:2276` | `Fb/compare.go:95-96`, `Fb/comparison_location.go:67-71` | U14 |
| 16 | Holds | `Fb/compare_state.go:27-48,80,120-136` | U5 |
| 17 | Holds | `Fb/compare.go:38-39,52,57` | U9 |
| 18 | ~~Injection and loss sequence~~ fixed by `1ad039c1`. `Record.Clone` still shares the frame pointer | `Fb/record.go:79-86`, `Fb/run.go:319` | U16 |
| 19 | Holds when the queue is empty. The convergence test also omits `sourcesPending` | `Fb/compare.go:153-181,213` | U14 |
| 20 | Holds. Pinned by `Fb/align_test.go:297-316` | `Fb/compare_state.go:48,228-239` | U5 |
| 21 | Holds | `Fb/scenario.go:372-379` | U15 |
| 22 | Holds | `Fb/config.go:1139` | U8 |
| 23 | Holds | `Fb/diff.go:674,691-731` against `:161` | U16 |
| 24 | Holds | `Fb/scenario.go:392,399` | U15 |
| 25 | Narrower. `Location.Field` names the entry since `71e930c8`. `Difference` text is still a count | `Fb/compare_state.go:30-69` | U5 |

### Completeness

| Entry | State | Unit |
| --- | --- | --- |
| README promises on comparison (`Fb/README.md:1163-1170,1220`) | Holds until 14 to 19 land | U9, U14, U15 |
| `Inconclusive` wording (`Fb/README.md:1174-1176`, `src/common/sim/README.md:57-59`) | Holds. The code agrees with the record | U18 |
| Snapshot neighbors "origin, lifetime" (`Fb/README.md:647`) | Holds | U5 |
| `PendingWork` omits sources and deferred hosts (`Fb/result.go:40-45`) | Holds | U12 |
| ~~`Arrival.Wake` in the README~~ | Fixed by `b20921f5` | none |
| Copy paths clone the frame (`Fb/run.go:260,285,609,981,1296,1309`) | Holds | U17 |
| Host PCP (`Fb/README.md:255-256`, `Fb/run.go:265-269`) | Holds | U7 |
| Admin-unknown rule undocumented (`Fb/fabric.go:1216-1218`) | Holds | U6 |
| `Parent` in the local-network record (`:72-73`) and `Fb/run.go:742` | Holds | U8 |
| No-injection precondition (virtual-device record `:219-220`) | Holds | U18 |
| Protocol journeys and `entered` never freed (`Fb/flow.go:92`) | Holds | U13 |
| Host-bound leg records no crossing (`Fb/run.go:1258-1274`) | Holds | U7 |

### Design

| Entry | State | Unit |
| --- | --- | --- |
| Reaches past the switch | Holds at every site but the policer, fixed by `d7f98aa5` | U2 to U5 |
| One scenario executor (`Fb/compare.go:131-229`, `Fb/run.go:1579-1673`) | Holds | U14 |
| One behavioural projection (`Fb/compare_state.go:16`, `Fb/fingerprint.go:44`) | Holds | U5 |
| Checkpoint against spec | Holds. Decided: a checkpoint (Decisions) | U9 |
| `Scenario` carries a topology and a budget (`Fb/scenario.go:419-421`) | Holds | U9 for the topology, U15 for the budget |
| Action matched by index (`Fb/scenario.go:266,444-457,615-617`) | Holds | U15 |
| `HostRoutingConfig` exported (`Fb/config.go:271`) | Holds | U7 |
| File sizes: `config.go` 1,318, `diff.go` 1,141, `run.go` 1,943, `fabric.go` 1,547. ~~`compare.go`~~ split by `71e930c8` | Holds | U6 to U8, U11, U16, U17 |
| `sw.Ports()` per arrival (`Fb/run.go:491,862`), three `Encode` per hop (`:497,892,943`) | Holds | U17 |
| Fingerprint every step (`Fb/run.go:1638`), `Report()` for a pending test (`:1722,1746`) | Holds | U14 |
| Aggregate builds then discards (`Fb/run.go:551-558`) | Holds | U17 |
| Duplicate queue state (`Fb/fabric.go:188-190`, `Fb/run.go:122-123`) | Holds | U12 |
| Hand-built `&Fabric{}` support (`Fb/run.go:1911-1943`, `Fb/queue.go:128-135`, sixteen guards in `Fork`) | Holds | U17 |
| Names | Holds for all seven | U17, and U6 for `Link` |
| `Fabric.Clock()` missing, 60 readers of `Snapshot().Clock` | Holds | U17 |
| `Run` has no window (`Fb/run.go:1467-1468`) | Holds | U14 |
| `Fabric.Switch()` returns the live switch (`Fb/fabric.go:1113`) | Holds | U11 replaces it with `Device`, U17 returns a fork |

### Tests

| Entry | State | Unit |
| --- | --- | --- |
| Fork probes that cannot fail (`Fb/fork_test.go:66-79,97-110,150-157,177-190,352-366`, and the map probes) | Holds | U17 |
| Wrong classes (`Fb/fork_internal_test.go:53,60,61,66,67,80`), table extended from `Fb/attach_internal_test.go:18-23` | Holds | U17 |
| Missing tests | Out-of-order holds U13. Host injection cases U12. Retention parity U14. Derived fabric with a hold U3 | as named |
| Hello-wake case discards its runs (`Fb/fingerprint_test.go:944-1009`) | Holds | U5 |
| Before-start refusal (`Fb/scenario_test.go:286-298`) | Holds | U9 |
| Clone mutation (`Fb/scenario_test.go:803-805`), ignored `Err` (`Fb/compare_test.go:1814`) | Holds | U15 |
| FDB count asserts the name only (`Fb/compare_test.go:1237`), fresh-only replay (`:1952-2043`) | Holds | U5, U9 |
| `compare_test.go` holds `Derive` and `Diff` tests (`:317,431,482,765,816`) | Holds | U11 |
| `TestStopReasonTableNoEmptyConstant` (`Fb/result_test.go:33-56`) | Holds | U17 |
| Routed fixtures inline (`Fb/flow_internal_test.go:439,552`, `Fb/flow_test.go:242`, thirteen in `Fb/routing_test.go`) | Holds | U17 |

## Carried items

Each retired phase plan was read at the commit before its retire commit
(`d7ac43ad`, `a51692c5`, `e152c4ed`, `6c4f1915`, `492f3488`, `830736a9`,
`cf429b87`, `06c14cac`, `14e4a39b`, `26705dbe`). Phases 1, 4, 5, and 13
carry nothing here.

| Item | Source | Disposition |
| --- | --- | --- |
| `fabric`'s layer constant, rule literals, and `nestedSubjectKey` | phase 2, Out of scope | U16. `Fb/diff.go:859` and `Fb/scenario.go:355` move to `trace.CompositeKey` |
| The fabric never uses the switch's point-to-point default | phase 3, Open questions | U3. The switch decides from the link report on both paths |
| A host answering a solicitation, ARP and Neighbor Discovery to a host's stack, the Router flag | phase 6, Out of scope and Open questions. Phase 9, Out of scope | Decided: a host answers for its own addresses (Decisions). U7 |
| Whether an unanswered next hop still raises `neighbor-unresolved` | phase 6 and phase 9, Decisions | Decided: it does (Decisions). U7 |
| No wake for a routing-only switch | phase 6, Inventory | U3, entry 5 |
| The journey of a release | phase 6, Out of scope | U13 |
| Whether a flood lists a down member port | phase 7, Decisions and Open questions | Decided: it does not (Decisions) |
| The home of the egress queue discipline | phase 7, Out of scope, Inventory, Open questions | Decided (Decisions). U4 |
| `Origin.Kind` and `Origin.Mirror` branches of `diffJourney` unreachable from `Compare` (`Fb/compare_journey.go:23`) | phase 8, Review gaps | U13 removes the release kind. U14 reaches the mirror branch through a recorded mirror root or deletes it |
| `State()` and the accessors the fabric reads | phase 9, Out of scope | U5 |
| `SelectMember`, `QueueMaxRate`, `QueueBuffer`, `PortMTU` | phase 9, Out of scope | U2, U4 |
| Three forms of point-to-point | phase 9, Out of scope | U3 removes the report type |
| The fabric reads no token: hold, ingress interface, emission member | named by the coordinator, confirmed at `Fb/run.go:1303-1349,1409-1433` | U2, U13 |
| A routing solicitation on a routed LAG commits a member and emits without it. The fabric selects again with the wire tag's VID | named by the coordinator, confirmed at `V/route.go:225-228`, `Fb/run.go:863-870` | U2 |
| A released frame produces no egress-selected mirror copy | named by the coordinator, confirmed at `V/route.go:302-363` against `V/forward.go:381` | U2 |
| `Derive` has no clock, so an expired record not yet aged counts as lost | named by the coordinator, confirmed at `V/derive.go:22,28,91`, stated at `V/derive.go:14-16` | U3 |
| Discarded errors of `LinkChange` and `setOperStatus` | commit `8647d908`, at `Fb/fabric.go:879,887`, `V/link.go:148` | U3 |
| The other open review gaps of `8647d908` | the same commit | Name no fabric file (Out of scope) |
| Whether the layer engines move under `internal/` | parent, Open questions | Not decided here (Open questions) |

`Fb/README.md` and `V/README.md` have no Limits section. The layer READMEs'
Limits add one item: `L/routing/README.md` says a host joins no group by
MLD, which U7 leaves as it is: a host answers for its own addresses and
joins nothing.

## Units

Each unit's Verify is
`.claude/skills/verify-change/scripts/verify-change.sh -- <its Files>`.
`D` is `src/common/sim/device`, `C` is `src/common/sim/medium/cable`,
`A` is `src/common/sim/assembly`, `ST` is `src/common/sim/internal/simtest`,
`N` is `src/common/sim/netmodel`. Until U11, `fabric` still builds its
devices from configurations, and names `vswitch`, `host`, and `reflector`
for that alone.

### U1. The device seam
Files: src/common/sim/device/device.go, src/common/sim/device/device_test.go, src/common/sim/device/README.md, src/common/sim/device/vswitch, src/common/sim/fabric, src/common/sim/netmodel, src/common/sim/search, src/common/sim/internal/simtest, test/conformance/sim/layer_contract_test.go, test/conformance/sim/testdata, docs/architecture/2026-10-01-simulation-package-shape-direction.md
After: none
Change: `D` declares `Device` with `Receive`, `ReceiveMirrored`, `Advance`,
`NextWake`, `LinkChange`, `Fork`, and `Ports`, and the types `Output`,
`Result`, `Emission`, `Drop`, `Copy`, and `HoldToken`. `D` imports no
package under `sim/layer` and not the root. `Result` restates what leaves
the switch of `bridge.Result` (`L/bridge/result.go:54-71`): the trace, the
ingress, the FID, each egress with its port, member, frame, PCP, and
drop reason, and the ports and scopes the decision consulted, which
`Fb/journey.go:451-454` reads through `ConsultedPorts` and
`ConsultedScopes`, beside the metadata. `Copy` restates `traffic.Copy`
(`L/traffic/mirror.go:35-41`), and `HoldToken` restates
`routing.HoldToken`. `*vswitch.Switch` implements `Device` and converts in
one place, `call.output` (`V/output.go`). `vswitch.Output`,
`ForwardResult`, `Emission`, and `NeighborDrop` are gone, with no alias.
`Fb` holds `map[string]device.Device` beside the switch map and calls
`Receive`, `Advance`, and `NextWake` through it. A journey's hop holds a
`device.Result` (`Fb/journey.go:97,460-469`, `Fb/compare_journey.go:217`).
No behaviour changes. The gate in `test/conformance/sim` gains a guard
list for one directory without its subdirectories, since `readLayerDir`
walks them (`test/conformance/sim/layer_contract_test.go:477-491`) and
`D` holds the device kinds beneath it. It refuses, in the non-test files
of `D` itself, an import of `fabric` and an import of `sim/layer` or any
path under it. The shape record's device-seam paragraph
(`:93-98`) gains the sentence that the seam's types name no layer.
Tests: `D/device_test.go` asserts at compile time that `*vswitch.Switch`
is a `Device`. The gate's fixture `testdata/device_imports_layer` fails
with the import named. The suites of `V`, `Fb`, `N`, `search`, and `ST`
pass with type names changed and no expectation changed, among them the
flood past an `Unknown` port of `Fb/journey_metadata_test.go:231`, which
fails when the consulted ports do not cross. An expectation
that moves is a deviation to report.

### U2. Every emission is complete
Files: src/common/sim/device/vswitch, src/common/sim/device/device.go, src/common/sim/fabric/run.go, src/common/sim/fabric/lag_test.go, src/common/sim/fabric/routing_test.go
After: U1
Change: an emission or copy on a LAG names the member the switch
committed, on every path (R8). `emitRouting` sets it
(`V/route.go:225-228`). A released frame yields the copies its egress
selects. Each copy names the held frame's `HoldToken` as its parent, and
is a frame of its own (U13). `Switch.SelectMember`
has no caller outside `V` and is unexported, and the LAG branch of
`Fb/run.go:859-889` is deleted.
Tests: a generated check in `V/routed_egress_matrix_test.go` of 12 cases:
{ARP request, Neighbor Solicitation, unicast probe, release} by {routed
LAG, sub-interface on a LAG, VLAN interface whose egress is a LAG}. Each
asserts `Member` is the member a second frame of the same flow leaves by.
`TestReleasedFrameIsMirroredOnEgress` in `V/mirror_test.go`: a mirror
with `SelectDstPorts` naming the release port yields one copy with the
held frame's token, and none before the release. `Fb/lag_test.go` gains a
case that fails when the fabric selects: a routed LAG whose wire tag VID
hashes to another member than VID 0. Expected to change: any
`Fb/routing_test.go` case that reads a solicitation's crossing on a LAG.

### U3. A link report, a clock for Derive, and every start
Files: src/common/sim/device/device.go, src/common/sim/device/vswitch, src/common/sim/fabric, src/common/sim/internal/simtest, docs/architecture/2026-09-10-virtual-device-direction.md
After: U2
Change: `LinkChange(now, port, device.Link)` takes the state, the speed,
this end's duplex, and whether the peer relays, in types `D` declares.
The switch applies its administrative override and decides point-to-point
in one place. `vswitch.PointToPoint`, `portPointToPoint`, and
`uncabledPointToPoint` are gone. `startLayers` reports every port of
every switch, the members of a LAG first, and schedules its wake
(entry 5). It returns the first `LinkChange` error to `New`, `Derive`,
and `Configure`. `V/link.go:148` returns its error. `Configure` keeps a
switch's static seeds through a method of the switch
(`Fb/configure.go:23-28`).
`vswitch.Derive(cur, target, now)` advances a clone of `cur` to `now`
before it carries a record, so a record past its lifetime is aged and not
reported lost. The record's Protocol link-state input paragraph is amended.
Tests: `TestRoutingOnlySwitchWakesAfterDerive` in `Fb/routing_test.go`: a
derived fabric with one retained hold and no traffic runs to a
`neighbor-miss` drop. It fails today with a drained queue.
`TestDeriveAgesBeforeCarry` in `V/derive_test.go`: an FDB entry and a
neighbor entry past their lifetime at `now` leave `Retention` at
`Kept: true`. `TestPointToPointDecidedBySwitch` in `V/link_test.go`: the
six rows of `Fb/fabric.go:1490-1522` as a table over `device.Link`, plus
the forced values. The startup error path cannot be reached from a validated
configuration in this unit (`src/common/sim/port/port.go:430`,
`src/common/sim/fabric/config.go:1262`), so its test is in U10.
Expected to change: no `Fb/stp_test.go`
expectation, since the rule moves unchanged.
The routing rebuild-drop and retain cases use an unresolved neighbor hold
that survives the advance, while a resolved hold emits during the advance
on both retain and rebuild.

### U4. Queue decisions come from the device
Files: src/common/sim/layer/traffic/config.go, src/common/sim/layer/traffic/layer.go, src/common/sim/layer/traffic/README.md, src/common/sim/layer/traffic/config_test.go, src/common/sim/device/device.go, src/common/sim/device/vswitch, src/common/sim/fabric/run.go, src/common/sim/fabric/fabric.go, src/common/sim/fabric/egress_buffer_internal_test.go, src/common/sim/fabric/queue_buffer_internal_test.go, src/common/sim/fabric/egress_buffer_test.go, src/common/sim/fabric/traffic_test.go, src/common/sim/fabric/README.md
After: U3
Change: `Device.AdmitEgress(port, pcp, depth, octets)` returns whether
the queue takes the frame, the drop or threshold step, and the queue's
maximum rate. `traffic.Config` builds both steps. The threshold rule stays
as the offered-load record states it (`:73-77`) and as `Fb/fabric.go:1058`
applies it. `Output.Policed`
replaces the test on `traffic.ReasonPoliced`. `Fb` imports no `traffic`.
`QueueMaxRate`, `QueueBuffer`, and `PortMTU` leave the switch's exported
surface. A host's queue uses the same decision with no table, in U7.
Tests: `TestLoneJumboThreshold` in `Fb/queue_buffer_internal_test.go`
pins the recorded rule for one 9,018-octet frame on an empty queue. With
an unstated MTU the threshold is 1,518 and the frame raises
`queue-buffer-unstated`. With a stated MTU of 9,000 the threshold is 9,018
and it raises nothing.
`TestQueueAdmit` in `L/traffic/config_test.go`: stated buffer at, one
under, and one over the limit, and the unstated threshold at depth zero.
`TestQueuedFrameUsesServiceTimeRate` in `Fb/traffic_test.go` keeps two
frames queued across `Configure` and pins their spacing to the new rate.
Expected to change: none of the existing tail-drop expectations. The step
bytes are pinned by `Fb/egress_buffer_internal_test.go` and must not move.

### U5. One device state and one projection
Files: src/common/sim/device/device.go, src/common/sim/device/state.go, src/common/sim/device/state_test.go, src/common/sim/device/README.md, src/common/sim/device/vswitch, src/common/sim/fabric, src/common/sim/netmodel, src/common/sim/search, src/common/sim/internal/simtest
After: U4
Change: `Device.State(now)` returns `device.State`, which replaces
`fabric.Device` and the nine accessors of `Fb/run.go:1845-1865`. Its
fields are types `D` declares, one for each layer type the snapshot holds
today: `bridge.Entry` and `bridge.Counters`, `mcast.Entry` with
`mcast.SourceEntry`, `mcast.RouterPort`, `stp.PortInfo`,
`routing.NeighborEntry`, and `phy.Allocation` with its port and group
rows. A field keeps the name it has in the layer. An enumeration crosses
as a named type of `D` with the layer's values. The switch converts in
`V/state.go`, and nothing else does. `D`
classifies every field of `State` once as behavioural or diagnostic, and
`State.Fingerprint` and `State.Diff` both read that table. Behavioural:
the FDB with origin and lifetime, groups, router ports, ports, power,
roles and tree roles without their four counters, neighbors with VRF and
hold depth. Diagnostic: counters. `State.Diff` returns the differing
entry in its text (entry 25). `Fb` imports no `bridge`, `mcast`, `stp`,
or `routing` for state.
Tests: a generated check in `D/state_test.go` of 18 cases, one per field
and named sub-field: `Entries`, FDB origin, FDB lifetime, `Groups`,
`RouterPorts`, `Ports`, `Power`, `Roles`, `TreeRoles`, `Neighbors`,
neighbor VRF, neighbor hold depth, `Counters`, `RelayCounters`, and the
four `PortInfo` counters. A behavioural case changes both `Fingerprint`
and `Diff`. A diagnostic case changes neither. The test fails on a field
of `State` with no class. `TestStateCarriesEveryLayerField` in
`V/state_test.go` walks the exported fields of the nine layer types by
reflection and fails on one with no field of the same name in its `D`
type, and on two constants of a layer enumeration that convert to one
value. Expected to change: `Fb/align_test.go:297-316`,
which expects the four counters as differences, and
`Fb/compare_test.go:1237`, which now asserts the entry. Every test that
compares a snapshot field to a layer value names the `D` type instead.
`TestFingerprintUnchangedAcrossHelloWake` runs on an `Up` link and
asserts a BPDU crossed.
The search field-identity case uses distinct link fields with identical
displayed values because device differences identify their rows. A second
case keeps the port setup and asserts distinct text for distinct ports. The FDB
exact-value case keeps its 2/2 versus 1/1 entry counts beside the differing
entry, so the same two actions remain necessary. The tree-kind escaping
case reads the public fingerprint because its encoder belongs to `D`.

### U6. The cable
Files: src/common/sim/medium/cable, src/common/sim/fabric, src/common/sim/netmodel, src/common/sim/search, src/common/sim/internal/simtest, src/common/sim/fact_contract_test.go, src/common/sim/device/vswitch/routed_egress_matrix_test.go, src/edge/simload/test/integration/lab_test.go, test/conformance/sim/layer_contract_test.go
After: U5
Change: `C` holds the medium, reach, propagation, the top speed, the
fault, the two-ended resolution of `Fb/fabric.go:1143-1345`, and the
crossing decision of `Fb/run.go:1215-1243` as a method that takes the
crossing count and the direction. The resolution takes each end as one
value of `C`: its Ethernet settings and its administrative state. It
returns each end's state, reason, speed, and duplex, with the issues and
the assumption it filled. `fabric.Cable` keeps the two endpoints
and holds a `cable.Cable`. `Link` no longer embeds `Cable`. `C` also holds the physical assumption
profile and the Ethernet validation of `Fb/config.go:511-535,790,1190`.
`Fb` names `phy` only for the host's and the reflector's Ethernet
fields, which leave in U7 and U8. `C/README.md` states the count a fault position uses and the
admin-unknown rule (`Fb/fabric.go:1216-1218`), with a named reason
constant in place of the literal.
Tests: `Fb/medium_test.go` and the resolution cases of `Fb/link_test.go`
move to `C` unchanged. `TestFaultPositionCountsBothDirections` in `C`:
positions 2 and 3 of a `LoseSequence` are one frame each way, and a BPDU
takes a position. It pins the documented count.

### U7. The host
Files: src/common/sim/device/host, src/common/sim/device/device.go, src/common/sim/device/vswitch, src/common/sim/fabric, src/common/sim/layer/routing, src/common/sim/netmodel, src/common/sim/search, src/common/sim/internal/simtest, src/edge/simload/test/integration/lab_test.go, docs/architecture/2026-10-01-simulation-package-shape-direction.md, docs/architecture/2026-09-10-virtual-device-direction.md
After: U6
Change: `device/host` holds the host's configuration, its acceptance
rules, and its IP stack, and implements `Device`. `fabric.Host`,
`HostIP`, `HostAccept`, and `HostRoutingConfig` are gone. `Device` gains
`Originate(at, port, frame, packet)`, the one verb an injection goes
through. A host builds the frame from a packet with its stack, applies
its VLAN form to the VID alone, and returns it as an emission at the PCP
it was handed. A switch returns the frame as an arrival on the named
port. A device that cannot originate returns the error `Inject` returns
today (`Fb/run.go:222-295`). A frame bound
for a host is a queue event at its delivery time, and the leg records a
crossing with wait, serialization, and PCP. The host returns `Consumed`
or `Dropped` with the acceptance step, and `fabric` records the delivery.
A host hands an ARP or Neighbor Discovery frame to its stack and returns
the answer for one of its own addresses as an emission. Its stack stays
`NeighborDisabled`, so it learns and resolves nothing, and the layer
already answers a local solicitation in that mode (virtual-device record
`:880-881`). `routing.Config` gains a field that clears the Router flag
of `L/routing/receive.go:136-139`, set for a host. The record's host
sentence (`:885-886`) is amended: a host processes these frames to answer
them. `neighbor-unresolved` is raised as today. Sites that classify by
`cfg.Hosts`:
`Fb/run.go:222,1259`, `Fb/attach.go:46,138`, `Fb/journey.go:436`,
`Fb/fabric.go:1387,1509`. Sites that iterate it: `Fb/fabric.go:94,276,288,
342,371,453,517,643`.
Tests: `Fb/host_accept_test.go` moves to `device/host` with its
expectations unchanged. `TestHostBoundLegRecordsCrossing` in
`Fb/run_test.go`. `TestHostInjectionKeepsPCP`: a frame injected with PCP 5
on a tagged host crosses at 5. `TestHostAnswersForItsOwnAddress` in
`device/host`: an ARP request and a Neighbor Solicitation for the host's
address each return one answer, and one for another address returns none.
`TestHostAdvertisementClearsRouterFlag` in `L/routing`, against the
Neighbor Advertisement layout of RFC 4861 section 4.4.
`TestSwitchResolvesHostWithoutStaticNeighbor` in `Fb/routing_test.go`: a
routed frame to a host with no configured neighbor is held, the host
answers, and the frame is delivered. `TestHostResolvesNothing`: a packet
injection whose next hop has no configured neighbor is refused as today.
Expected to change: every expectation of
`Steps` and of entry order on a run that delivers to a host, in
`Fb/run_test.go`, `Fb/result_test.go`, `Fb/flow_test.go`, and the scenario
cases of `ST`. Each rises by one step per delivery, and an Arrival entry
follows a Crossing. A routed fixture keeps its static neighbor for a host
unless its test is about resolution.

`TestHostAdvertisementClearsRouterFlag` checks exactly two stacks, original and
clone, against literal advertisement bytes. `TestRouterFlagConfigurationChangesDiffAndRetention`
checks the flag's diff entry and distinct retention keys. The fabric fork's host
ownership probe uses independent public link state after the host stack moves.
`TestHostInjectionKeepsPCP` also retains DEI across the VID rewrite.
`TestHostOriginateRefusesInvalidPriority` checks exactly two VLAN forms and
refuses PCP 8 before it can become a queue sidecar.

### U8. The reflector
Files: src/common/sim/device/reflector, src/common/sim/fabric, src/common/sim/internal/simtest, docs/architecture/2026-10-01-simulation-package-shape-direction.md, docs/architecture/2026-09-16-local-network-analysis-direction.md
After: U7
Change: `device/reflector` holds the reflector's configuration and
rules, implements `Device`, and returns each reflected copy as a `Copy`.
`Fb/run.go` has no branch on node kind (R7). A reflector accepts a frame
only when its group MAC and its IP destination are of one family (entry
8). An attachment with an invalid prefix fails `Validate` (entry 22). The
copy's origin kind is `OriginReflection`, apart from `OriginMirror`.
`Fb/accept.go` is deleted. The shape
record's tree gains `reflector/`, and the local-network record's `Parent`
sentence names `Origin.Of`.
Tests: `TestReflectorRefusesMixedFamily`: the IPv4 mDNS MAC with an IPv6
packet to `ff02::fb` is rejected with `reflector-ip-not-group`.
`TestReflectorAttachmentRefusesZeroPrefix` in `device/reflector`. A round
trip, written first: a fabric built with a reflector
returns it from `Spec()`. Expected to change: every assertion of
`OriginMirror` on a reflected copy under `Fb`, found by grep.
`TestReflectorSpecRoundTrip` also reconstructs the returned specification.
The acceptance suite pins the undecodable-header messages and journey scopes.
`TestCorruptingCableToAHostDropsTheCopy` keeps the cable reference on the host's
drop, and the reflector corrupt-arrival test keeps its cable reference absent.

### U9. A scenario names no topology, and a replay is a checkpoint
Files: src/common/sim/fabric/run.go, src/common/sim/fabric/compare.go, src/common/sim/fabric/result.go, src/common/sim/fabric/scenario.go, src/common/sim/fabric/align.go, src/common/sim/fabric/replay_test.go, src/common/sim/fabric/scenario_test.go, src/common/sim/fabric/compare_test.go, src/common/sim/fabric/result_test.go, src/common/sim/fabric/record_test.go, src/common/sim/fabric/seed_defaults_test.go, src/common/sim/fabric/README.md, src/common/sim/search, src/common/sim/internal/simtest/scenario_cases.go, docs/architecture/2026-09-10-virtual-device-direction.md
After: U8
Change: `ReplaySpec` holds a fork of the fabric taken before the first
action, and the scenario. `RunScenario` and each side of `Compare` take
that fork before they apply anything (entries 7 and 17, R11). `Replay`
runs the scenario on a fork of the checkpoint, so one replay value runs
any number of times. `ReplayContract` and `ReplaySpec.Contract` are gone.
`Scenario.Spec` is gone. The start guard of `Fb/scenario.go:438` moves to
`RunScenario` and `Compare`, which refuse an action of any kind dated
before the clock of the fabric it runs on, each side of a comparison
against its own. `Fb/README.md` and `src/common/sim/search/README.md:96`
say a replay is an in-memory value. The virtual-device record's sentences
on an immutable `ReplaySpec` (`:439,496`) are amended to name the
checkpoint.
Tests: `TestReplayOfSteppedFabric` in `Fb/replay_test.go` is R11's
example, and also asserts an FDB entry learned before the scenario
forwards the replayed frame. It fails today, where the replay rebuilds
from a specification. `TestCompareReplayOfSteppedFabrics`: the same
through `Compare`, one replay per side. `TestReplayRunsTwice`: two
replays of one value return equal results. Since `RunResult` carries no
journeys, the stepped replay tests run the checkpoint returned by the replay
to assert learned unicast and position-four cable loss through its public report.
The comparison and repeat loops assert two completed cases each. The
before-start case of `Fb/scenario_test.go:286-298` runs through
`RunScenario` with an inner time equal to its action time, once per
action kind, so only the guard can refuse it.
The clock guard also runs on stepped fabrics and on each comparison side,
with 30 execution-clock checks across the five action kinds. Expected to change:
`Fb/compare_test.go:1952-2043`, `ST/scenario_cases.go:186`, which rebuilds
from `Replay.Spec`, the four uses of `ReplayContract` in the tests of
`search`, and three files that set or read the removed fields:
`Fb/result_test.go:592-618` keeps its check that a plain `Run` leaves the
replay zero, and `Fb/record_test.go:79,135,196,259` and
`Fb/seed_defaults_test.go:52` build their fabric from the specification
they put in the scenario.

### U10. The fabric runs on built nodes
Files: src/common/sim/fabric, src/common/sim/device/device.go, src/common/sim/device/vswitch, src/common/sim/device/host, src/common/sim/device/reflector
After: U9
Change: `Fb` is two halves that meet at one unexported value, the
topology: per node its device, each port's Ethernet settings and
configured states as a value of `C`, and whether it repeats, with the
cables, the uncabled entries, the assumption, the evidence, and the start.
The construction half reads configurations and builds devices. It is
`config.go`, `diff.go`, `configure.go`, `derive.go`, and a new
`assemble.go` that takes `normalizeConstructionSpec`, the device-building
part of `build`, `rebuildSwitchPorts`, `Spec`, `Switch`, `Config`,
`Retention`, and `mcheck`
(`Fb/fabric.go:272-557,559-586,1113-1133,1411-1425`). The run half is
every other file but `scenario.go`, whose `Configure` action names
`vswitch.Config` until U11.
It holds the topology in place of `cfg`, `switches`, and `hostStacks`
(`Fb/fabric.go:174-182`), and imports no device kind and no layer. Its
reads of `cfg.Switches`, `cfg.Hosts`, and `cfg.Reflectors` go through the
node or the seam. A grep counts 60 such reads in the non-test files of
`Fb` today, 46 of them in `fabric.go`. The run half gains
three unexported entry points. One builds a fabric from a topology and
starts it: it reports every link itself, the members of a LAG first, in
place of `notifyLAGMembers` (`Fb/fabric.go:588-613`), and applies the
output a device was built with. One replaces a node at the clock, as the
second half of `Configure` does (`Fb/configure.go:80-118`). One runs a
function on the live device at the clock, applies its output, and
schedules its wake, as `mcheck` does (`Fb/fabric.go:1411-1425`). A
fourth returns a copy of the topology as it stands: the start, each
node's configured port values, the cables with their present faults, the
uncabled entries, the assumption, and the evidence. `Spec`
is rebuilt from that copy and each device's own specification, never
from a stored configuration. The twelve test files
of package `fabric` that call a constructor join package `fabric_test`
and read unexported state through `Fb/export_test.go`:
`egress_buffer_internal_test.go`, `flow_internal_test.go`,
`fingerprint_test.go`, `fork_internal_test.go`, `fork_test.go`,
`queue_buffer_internal_test.go`, `mcheck_internal_test.go`,
`journey_state_test.go`, `record_test.go`, `unlinked_internal_test.go`,
`result_test.go`, and `scenario_test.go`. No exported name and no
behaviour changes.
Tests: `TestLinkChangeErrorReachesNew` hands a built test device whose
`LinkChange` returns an error to the fabric, and asserts construction returns
that error and no fabric, with the same check for `Derive` and `Configure`
wherever those paths live after U10 and U11.
`TestRunHalfImportsNoDeviceKind` in `Fb` parses the imports of
every non-test file but the five of the construction half and
`scenario.go`, and fails on a path under `sim/layer` or `sim/device/`. `TestSpecRoundTrip`: for a
fabric of a switch with seeds, a tagged host, and a reflector,
`NewWithSpec(f.Spec())` returns a fabric whose `Spec()` equals the first.
`TestConfigureWithoutChangeKeepsSpec` is R10's second example.
`TestSpecKeepsConfiguredState`: a port configured `Down` over a cable
that derives `Up` reads `Down` in `Spec()` and keeps its
`oper-status-conflict` issue, and the start, the assumption, an uncabled
entry, and an evidence entry no cable cites all return unchanged. The corpus
of `ST` and the suites of `Fb`, `search`, and `N` pass with no expectation
changed. An expectation that moves on a derived fabric with a LAG is the
first unverified item of Open questions, and is reported.

### U11. The assembly package and the import rule
Files: src/common/sim/assembly, src/common/sim/fabric, src/common/sim/device/vswitch/routed_egress_matrix_test.go, src/common/sim/netmodel, src/common/sim/search, src/common/sim/internal/simtest, src/common/sim/fact_contract_test.go, src/edge/simload/test/integration/lab_test.go, test/conformance/sim/layer_contract_test.go, test/conformance/sim/testdata, docs/architecture/2026-10-01-simulation-package-shape-direction.md, docs/architecture/2026-09-10-virtual-device-direction.md
After: U10
Change: the construction half of U10 moves to `A` with its tests, and
`fabric` keeps the run half. `A` exports `Config`, `ConstructionSpec`,
`NewConstructionSpec`, `New`, `NewWithSpec`, `Spec`, `Derive`,
`Configure`, `Diff`, `DiffSpecs`, `PrefixesFact`, and `Retention`, each
with the behaviour of the `fabric` name it replaces and a `*fabric.Fabric` where
that name had a receiver. `fabric` exports the topology as `Config` with
its `Node`, and the four entry points of U10 as `New`, `Replace`,
`Operate`, and `Fabric.Config`, the copy `A.Spec` reads. `Fabric.Device(name)` replaces `Fabric.Switch`, and a caller
that needs a switch asserts `*vswitch.Switch`. `Fabric.Config` in its
old type, `Fabric.Spec`, `Fabric.Retention`, `Fabric.Configure`, `fabric.Derive`,
`fabric.Diff`, `fabric.DiffSpecs`, `fabric.NewWithSpec`,
`fabric.NewConstructionSpec`, `fabric.ConstructionSpec`, and
`fabric.PrefixesFact` are gone, with no alias. `fabric` declares
`Operation`. `ConfigureAction` and `McheckAction` hold a node and an
`Operation`, and `A` builds both: one calls `A.Configure`, one calls
`Operate` with the switch's `Mcheck`. `Action.Diff` asks the operation
for its changes in place of `vswitch.Diff` (`Fb/scenario.go:351`). The
`Derive` and `Diff` tests of `Fb/compare_test.go` (`:317,431,482,765,816`)
move to `A`. Every caller follows: some 280 calls of a constructor,
`Derive`, `Configure`, or `Diff` in the tests of `Fb`, the corpus builders of `ST`, the tests of `N` and
`search`, `V/routed_egress_matrix_test.go:34`, and
`src/edge/simload/test/integration/lab_test.go:137`. The gate gains R2's
rows. The shape record changes in four places: the tree (`:50-65`) gains
`assembly/`, the diagram (`:67-76`) draws `device/*` and `fabric` into
`assembly`, the import rule (`:84-86`) says `fabric` imports `device`
and no package beneath it, and the `netmodel` sentence (`:90-91`) says it
builds switch specifications. The virtual-device record gains a dated
amendment that names `assembly.Configure`, `assembly.Derive`, and
`assembly.ConstructionSpec` where it names the `fabric` ones
(`:205,225-232`). The parent's R2 needs no change.
Tests: two gate fixtures, `testdata/fabric_imports_layer` and
`testdata/fabric_imports_device_kind`, each fail with the import named.
`TestFabricDependsOnPhyAlone` in `test/conformance/sim` reads
`go list -deps ./src/common/sim/fabric` and fails on a path under
`sim/layer/` other than `phy`. The same function applied to `A` must
report `bridge`, so the test can fail. `TestOperationNeedsNoAssembly` in
`Fb`: a scenario whose `Configure` action carries a stub operation over a
stub device runs and diffs, with no import of `A`.
`TestFabricRefusesUnknownEndpoint` and `TestFabricRefusesTwoCablesOnAPort`
in `Fb`. `TestRunHalfImportsNoDeviceKind` is deleted for the gate row.
The corpus of `ST` passes with no expectation changed (R10).

### U12. Every input is a queue event
Files: src/common/sim/fabric/queue.go, src/common/sim/fabric/run.go, src/common/sim/fabric/attach.go, src/common/sim/fabric/result.go, src/common/sim/fabric/fabric.go, src/common/sim/fabric/queue_internal_test.go, src/common/sim/fabric/attach_test.go, src/common/sim/fabric/attach_internal_test.go, src/common/sim/fabric/run_test.go, src/common/sim/fabric/result_test.go, src/common/sim/fabric/scenario_test.go, src/common/sim/fabric/README.md, docs/architecture/2026-09-18-offered-load-streams-direction.md
After: U11
Change: a host transmit, a scenario action, and a stream's next frame are
arrivals of their own kinds, ordered with frames and wakes by
`compareArrival`. `pendingHosts`, `pullInputs`' merge, and the deferral
test of `Fb/run.go:332` are gone. A host charges its egress when its
event is stepped (R3). `Inject` refuses a time before the clock from
construction on, and `Step` handles an event dated before the clock at
the clock (R9). A stream frame the codec refuses, or one whose offset
runs backward, ends that stream with an issue on its flow and the run
continues. A stream on an `Unknown` link pulls one frame per event.
`RunScenario` accepts a fabric with an attached stream. One queue type
owns the heap and its wake and dequeue indexes. `PendingWork` counts
pending inputs. The zero `ArrivalKind` is invalid. The offered-load
record is amended where it pulls sources before each step (`:54-56`) and
where it refuses scenario actions with an attached stream (`:59-61`).
Tests: a generated check `TestHostInjectionOrder` of 24 cases: the six
orders of three injection times by {before the first step, after a step}
by {empty queue, a queued arrival}. Each asserts crossings in time order
and no negative `Wait`. `TestInjectBeforeClockRefused` before and after
the first step. `TestStreamFaultEndsOneStream`. `TestUnknownLinkPullsOne`.
`TestScenarioRunsWithAttachedStream`. Expected to change:
`TestEagerHostInjectionsReleaseInTimeOrder` (`Fb/attach_test.go:435`)
keeps its assertion and loses its setup arrival, and `Fb/run_test.go:
2042-2109` keeps `Wait == 672ns`.

### U13. A held frame continues its journey
Files: src/common/sim/fabric/run.go, src/common/sim/fabric/flow.go, src/common/sim/fabric/journey.go, src/common/sim/fabric/fabric.go, src/common/sim/fabric/compare_journey.go, src/common/sim/fabric/comparison_location.go, src/common/sim/fabric/flow_internal_test.go, src/common/sim/fabric/flow_test.go, src/common/sim/fabric/routing_test.go, src/common/sim/fabric/journey_state_test.go, src/common/sim/fabric/fork_test.go, src/common/sim/fabric/align_test.go, src/common/sim/fabric/README.md, src/common/sim/internal/simtest, src/common/sim/device/vswitch/routed_egress_matrix_test.go, src/edge/simload/report.go, src/edge/simload/report_test.go, docs/architecture/2026-09-10-virtual-device-direction.md, docs/architecture/2026-09-18-offered-load-streams-direction.md, docs/solutions/architecture-patterns/a-settled-aggregate-record-is-freed-at-settle-keep-its-identity-in-a-key.md
After: U12
Change: `Output.Hold` enters a map from device and token to `FrameID`,
and the frame counts as in flight while held. The fabric resolves a token once for a whole
`Output`. An emission or a drop that carries it continues the held
journey. A copy that carries it starts a mirror journey whose origin
names the held frame, with that frame's flow and retention, as a live
copy does (`Fb/run.go:600-633`). The key is removed after the output is
applied. A token the fabric does not know, as after `assembly.Derive`,
starts a journey of its own. `OriginRelease`, `JourneyReleased`,
`heldAggregates`, `findAndPopHeld`, and the one-entry journey of
`recordNeighborFailure` are gone. A protocol journey and its `entered`
set are freed at settle when nothing retains them. `FlowStats.Held` is
the number of the flow's frames in the hold map when `Flows()` is read.
A released frame counts under `Delivered`, and a timed-out one under
`Drops`. `src/edge/simload/report.go:165` reports the value read at the
end of the run, and its field says so. Both records and the solution are
amended.
Tests: a generated check `TestHoldExits` of 20 cases: {released, timed
out, evicted by overflow, refused by the filter at release, failed by an
`assembly.Configure` that rebuilds routing} by {`RetainJourney`,
`RetainAggregate`} by {holds end in order, in reverse}. Each asserts the
journey or flow of the right frame, an empty hold map, `Held == 0` at the
end, and `StopConverged`. `TestHeldIsAGauge`: two frames of one flow
held read `Held == 2`, and after one is released `Held == 1` and
`Delivered == 1`. R4's example as `TestReleaseOutOfOrder`.
`TestReleaseWithMirrorUnderAggregate`: one held frame released past an
egress mirror counts one `Delivered`, one `Copies`, one latency sample,
and leaves no key. Expected to
change: `Fb/flow_internal_test.go:686-690,726-728`,
`Fb/routing_test.go:1975`, `Fb/flow_test.go:326-327`,
`Fb/journey_state_test.go:184,269`, `Fb/fork_test.go:221-222`, `TestHeldLAGEgressUsesSelectedMemberOnWire`
(`V/routed_egress_matrix_test.go:87-95`), which asserts the held journey
itself reaches its host by the committed member, and the
release cases of `ST`, each from a release journey to one journey.

### U14. One executor
Files: src/common/sim/fabric/run.go, src/common/sim/fabric/compare.go, src/common/sim/fabric/comparison_location.go, src/common/sim/fabric/compare_journey.go, src/common/sim/fabric/result.go, src/common/sim/fabric/flow.go, src/common/sim/fabric/compare_test.go, src/common/sim/fabric/compare_internal_test.go, src/common/sim/fabric/result_test.go, src/common/sim/fabric/record_test.go, src/common/sim/fabric/bench_test.go, src/common/sim/fabric/README.md, src/common/sim/search, src/common/sim/internal/simtest
After: U13
Change: `Compare` runs each fork through the run loop, which reports the
root of every action that injects, records included (entry 14). The loop
keeps a count of pending journeys and reads no `Report()`. It computes a
fingerprint only under a window (R6), and `Run` takes a window. An action
error returns the result so far with the error (entry 12). `RunResult`
takes status and issues from the flows as well as the journeys (R5).
A frame of an attached source is a root, paired across the
sides by its attachment's ordinal and its frame's ordinal, one-sided
frames included. `Compare` pairs an aggregated root by its flow statistics and returns
`Inconclusive` where two aggregated sides cannot be told apart by
journey (entry 15).
Tests: `TestCompareRecordedFrame`. `TestCompareAttachedSources`: an empty
schedule and one source per side, with equal step counts and equal final
state, where one side's host rejects the frame, compares `Different`.
`TestRunResultAcrossRetentions`: 8 fixtures (delivered, dropped, lost,
released, timed out, unresolved, rejected, mirrored), each run under both
retentions with equal `Status` and `Issues`. `TestActionErrorKeepsSteps`.
`TestRunComputesNoFingerprint`, by a counter in an internal test. The
scale benchmark's figure and command go into `Fb/README.md`, with the
figure of a `RunScenario` over the same fabric beside it, which carries
U9's fork. Expected to
change: `Fb/compare_test.go:2276`.

### U15. Scenario
Files: src/common/sim/fabric, src/common/sim/assembly, src/common/sim/search, src/common/sim/internal/simtest, docs/architecture/2026-09-10-virtual-device-direction.md
After: U14
Change: `Scenario` is an action schedule, and budget and window travel in
one options value. An action has an `ID`, required and unique in its scenario,
and `DiffScenarios` pairs by it. `search` and `ST` name their actions.
`sameInjection` compares
`Packet`, `Retention`, and `Flow` (entry 21), and a summary names what
differs (entry 24). The virtual-device record's sentence that a scenario
declares its observation window and budget (`:407-408`) is amended to name
the options value.
Tests: `TestInsertedActionReportsOne`: one action inserted before five
reports one change. `TestDiffScenariosEveryInjectionField` with a case
each for `Packet`, `Retention`, and `Flow`. `TestActionSummaryNamesLoss`:
an interval of 2 against 3 reads differently on the two sides.
`TestDiffScenariosPairsConfigureByID` in `A`: two scenarios whose
`Configure` actions differ in one switch field report that field.
`Fb/scenario_test.go:803` mutates every field of the clone, and
`Fb/compare_test.go:1814` asserts `Err`.

### U16. Records, differences, and identifiers
Files: src/common/sim/fabric, src/common/sim/assembly
After: U15
Change: `Record.Validate` refuses a `Frame` that does not encode to
`Bytes` and a `CapturedLen` above `OriginalLen` (entry 13).
`Record.Clone` copies the frame value. An evidence-only edit produces no
change (entry 23). `nestedSubjectKey` is replaced by
`trace.CompositeKey`, in `A/diff.go` and in `Fb/scenario.go`. The
fabric's and the assembly's rule identifiers are named constants.
`A/diff.go` splits into the configuration walk and its serialization.
`normalizeConstructionSpec` drops the two checks `Validate` repeats.
Tests: `TestRecordRefusesDisagreeingFrame`,
`TestRecordRefusesCapturedAboveOriginal` in `Fb/record_test.go`.
`TestEvidenceOnlyEditIsNoChange` in `A/construction_result_test.go`.
Expected to change: the subject keys in `A/diff_coverage_test.go` and
`A/reflector_diff_test.go`, from the hand-joined form to the quoted one.

### U17. Fork gate, hot path, names, and file sizes
Files: src/common/sim/fabric, src/common/sim/assembly, src/common/sim/search, src/common/sim/internal/simtest
After: U16
Change: the fork gate classes `clock`, `stepped`, `nextFrameID`,
`nextSeq`, `runtimeEvidence`, and `err` as copied, holds its whole table
in one file, and every probe writes through an existing element. The
supports for a hand-built `&Fabric{}` are gone, and the tests that built
one use `assembly.New` or `fabric.New`. `Fabric.Clock()` exists.
`Fabric.Device()` returns a fork, and `assembly.Configure` and
`assembly.Spec` read it. A fork of a switch reports every layer kept
(`V/fork.go:51`), so `assembly.Retention` reads the live device through
`Fabric.Inspect(node, fn)`, which runs a function that must change
nothing.
A frame is shared on every copy path but `Report()`, as the record says.
`Step` encodes a frame once, reads no cloned port table, and builds no
hop result for an aggregated journey. The names of the inventory are
settled: `Retention` the type becomes `JourneyRetention`, `EntryWake` and
`EntryDequeue` become a step result, `Journey.State` is not stored.
`run.go` and `fabric.go` split by subject, and every non-test file of
`Fb` and `A` is under 1,200 lines and each function under 150 (parent
R9). One routed fixture serves the sixteen inline sites.
Tests: each probe of `Fb/fork_test.go` is rerun against a `Fork` that
shares that field and fails, per
`docs/solutions/conventions/a-perturbation-gate-that-copies-structs-by-field-passes-vacuously.md`.
`TestStopReasonTableNoEmptyConstant` is deleted. `TestDeviceAccessorIsACopy`:
a link change on the returned device leaves the fabric's snapshot
unchanged. The retention assertions of `Fb/routing_test.go:1757` and of
the configure test at `Fb/configure_test.go:269`, which U11 moves to `A`,
hold after the accessor forks. `V/size_test.go`'s check is repeated for `Fb` and `A`.
`Fb/bench_test.go` records allocations per step before and after.

### U18. READMEs and records
Files: src/common/sim/fabric/README.md, src/common/sim/README.md, src/common/sim/assembly/README.md, src/common/sim/device/README.md, src/common/sim/device/vswitch/README.md, src/common/sim/device/host/README.md, src/common/sim/device/reflector/README.md, src/common/sim/medium/cable/README.md, src/common/sim/netmodel/README.md, src/common/sim/search/README.md, CONCEPTS.md, docs/architecture/2026-09-10-virtual-device-direction.md, docs/architecture/2026-10-01-simulation-package-shape-direction.md
After: U17
Change: the READMEs word `Inconclusive` as the code and the record have
it. The record's no-injection sentence (`:219-220`) is removed. The shape
record gains one dated amendment that states the import rule as it
landed: `fabric` imports `sim/device` and `medium/cable`, `sim/device`
imports no layer, and `sim/assembly` builds what `fabric` runs. Each
README's working example builds its fabric through `assembly.New`.
`V/README.md:444-449` says which ports a `protocol-link-unknown` issue
names as `V/issues.go:413-458` has it: the ports configured under
`STP.Ports`, the members of a LAG with an unknown member, and that LAG.
Its "every STP port on the switch" becomes the configured ports. The code
and `TestProtocolLinkIssuesOnlyNameConfiguredSTPPorts`
(`V/protocol_link_test.go:397`) do not change. `CONCEPTS.md` names the
device, the host, the reflector, the cable, and the assembly.
Tests: `uv run tools/scripts/run.py verify check-prose` on each file.

Waves: U1 | U2 | U3 | U4 | U5 | U6 | U7 | U8 | U9 | U10 | U11 | U12 | U13 | U14 | U15 | U16 | U17 | U18

## Verification

```bash
go build ./src/common/sim/... ./src/edge/simload/...
go test -race ./src/common/sim/... ./src/edge/simload/... ./test/conformance/...
golangci-lint run ./src/common/sim/... ./src/edge/simload/...
go list -f '{{join .Imports "\n"}}' ./src/common/sim/fabric | grep -E 'sim/(layer|device)/' ; test $? -eq 1
test "$(go list -deps ./src/common/sim/fabric | grep 'sim/layer/')" = go.aledante.io/FlowSeer/src/common/sim/layer/phy
```

Never `--full`, as the parent says.

## Definition of done

- [ ] Verifier green for every changed path of every unit.
- [ ] Every row of Inventory and Carried items is closed by its unit, is
      struck, or is decided with its reason.
- [ ] `Fb/README.md`, the new package READMEs, and the four records are
      updated in the unit that invalidates them.
- [ ] The outcome is recorded with `uv run tools/scripts/run.py plan
      record implemented <plan> --units 18 --from <t> --to <t>` or
      `partial`.
- [ ] No plan label in code, comments, or commit messages.

## Open questions

None is the user's. The five of the first pass are Decisions, each marked
with its date.

For the implementer:

- Whether a link report to a LAG member changes a derived switch at start
  is unverified. `Derive` reports none to a derived switch today
  (`Fb/derive.go:22-49`, `Fb/fabric.go:552`), and `Configure` reports them
  (`Fb/configure.go:93`). U10 reports them on both paths. A `Derive`
  expectation that moves is a deviation to report, not to absorb.
- Whether a `Result` with no egress can carry an empty outcome is
  unverified (`L/bridge/egress.go:181-192`, `V/hub.go:98-108`). U1 adds a
  check over the corpus that every `Receive` returns one of the five
  outcomes, and reports a failure as a deviation.
- Whether a layer can leave a timer before `now` after `Advance(now)` is
  unverified. R9 holds either way.
- Whether `port.Table.WithOperStatus` can fail for a port of its own
  table is unverified. U3 returns the error either way.
- What a fork per `RunScenario` costs is unmeasured. U14 records it
  beside R6's figure.
- The names of fields inside `device.Link`, `device.State`,
  `fabric.Node`, and the options value, and the method names of
  `Operation`, are the implementer's. The exported type and function
  names and the rules above are fixed.
- Not decided here: whether the layer engines move under `internal/`.
  After U11, `go list -f '{{.ImportPath}}: {{join .Imports " "}}'
  ./src/common/sim/...` names the importers of each engine, which is the
  input the parent's question waits for.
- The built-node topology remains blocked by the actual-path link-error
  proof in U10. Prepared work is kept on `parked/sim10-topology-resume`
  at `f54f377e`, without counting as passed or integrating it.
  Its `src/common/sim/fabric/assemble.go:400-422` builds concrete devices
  from configurations before calling the built-device runtime entry.
  `Derive` replaces a failing test-device wrapper with a concrete switch
  and returns success. `Configure` refuses the wrapper with
  `node "n" is not a switch` at `configure.go:13-19`. A test calling
  the runtime entries alone cannot detect either caller discarding an
  error. U11 moves those construction contracts unchanged, so moving
  the proof there does not make the required failing device admissible.
- The parked work removes the allocation-calibration blocker. Its scaling
  gate measures the zero-queue floor and keeps the depths and 3.05
  multiplier. Fork allocations at depths 0, 4,096, and 16,384 are
  3,622, 15,947, and 52,907, compared with 4,502, 16,827, and 53,787
  before the topology change. The baseline tightens to 15,949. The
  paired measurements and failing nonlinear-growth and three-fixed-allocation
  mutations are recorded in `e2a038e5`. This calibration and the completed
  test-package moves remain parked with the topology.
