---
title: Netsim In-Run Switch Reconfiguration - Plan
type: feat
date: 2026-09-25
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: code
amends: docs/architecture/2026-09-10-virtual-device-direction.md
---

# Netsim In-Run Switch Reconfiguration - Plan

## Goal

A running `fabric.Fabric` takes a new configuration for one switch at the
current simulation time and keeps running. Queued arrivals, in-flight
journeys, egress queues, attached streams, flow statistics, and the other
switches all stay as they are, and a frame that reaches the changed switch
afterwards is forwarded under the new configuration. This is what the
mutation shadow projection needs to check the order in which changes are
applied (`docs/architecture/2026-09-09-mutation-shadow-projection-direction.md`,
Consequences, last bullet). A change to several devices lands one device at
a time, and the state in between can cut the management path or loop while
spanning tree reconverges. The means is a `Fabric.Configure(node, cfg)`
method built on `vswitch.Derive` with the same link re-resolution
`SetFault` does, plus a scenario action `ActionConfigure` that calls it.

Stop condition: the plan is wrong if the check has to change hosts,
cables, or reflectors in the middle of a run as well as switches. The unit
would then be a whole-fabric delta, not one switch.

## Decisions

- **The unit of change is one switch's `vswitch.Config`.** Why: a Mutation
  Intent is sequenced per device, and each device applies it at its own
  time. Hosts, cables, and reflectors are not device configuration. A cable
  cut or restore is already `SetFault`.
- **`Configure` validates the whole fabric before it changes anything.** It
  builds the fabric's `Spec()` with this node's `Config` replaced and runs
  it through `normalizeConstructionSpec(f, spec)`. Every rule `New` applies
  therefore also applies here, for example that a cable or uncabled entry
  names a port the switch has, that MACs are unique, and that evidence
  references resolve. A refused change returns the error and leaves the
  fabric untouched. Why: `SetFault` validates before it writes. Passing
  `f` as `cur` also carries the node's assigned MAC across when the new
  config leaves it zero
  (`docs/solutions/architecture-patterns/validate-and-derive-judge-what-new-builds.md`).
  Without that carry, the bridge address would change and every retained
  spanning tree layer would rebuild.
- **The switch is rebuilt with `vswitch.Derive`, and the target keeps only
  the node's static seeds.** The target spec is the node's `sw.Spec()` with
  `Config` replaced and `Seeds` filtered to `bridge.Static`. `Metadata` and
  `NodeID` stay the same. Why: `vswitch.Derive` already decides what each
  layer keeps (`src/common/netsim/vswitch/derive.go:33`). Dynamic entries
  come from the current switch through its reseed. A dynamic construction
  seed that has aged out would come back if the spec's seeds were passed
  through unchanged.
- **Links that touch the node are re-resolved against the new
  configuration, and both ends are told.** Admin status, Ethernet facts,
  and spanning tree point-to-point settings are all part of the switch
  config that `resolveLink` and `portPointToPoint` read. `SetFault`'s
  per-link update and notify code (`src/common/netsim/fabric/fabric.go:1398`)
  moves into a shared helper, and `Configure` calls it for every link whose
  resolved state changed. The node's port table is rebuilt with oper states
  from the links, and LAG members hear `LinkChange` as they do in `build`.
  That code also moves into a helper that `build` and `Configure` share.
  Why: it is one rule in one place. A copy would drift from `build`.
- **Afterwards the derived switch is started like one from `fabric.Derive`.**
  `startLayers([node])` runs, the emissions and neighbor failures are
  drained and recorded at the fabric clock, and the node's wake is removed
  and scheduled again from the new switch. Why: `fabric.Derive` starts
  layers after the swap for the same reason (`derive.go`). `vswitch.Derive`
  fails held frames under `time.Time{}` when routing is not retained, and
  the fabric has to give those drops a time, which is its clock.
- **The switch's egress queues and queued arrivals are not re-filtered.**
  A frame already on an egress queue has passed the old pipeline and leaves
  under the link state at dequeue time. A frame that arrives later meets
  the new switch. Why: a device's configuration change does not recall
  frames already in its buffers, and those frames are the transient the
  check exists to see.
- **`Configure` records no journey entry.** The scenario action is the
  record of the change, and `Replay` carries it. Why: journeys are per
  frame, and a change to configuration is not a frame.
- **`ReplayContract` stays `netsim-fabric/v1`.** Why: this adds an action
  kind and does not change what an existing replay means. Nothing outside
  the repository stores replays (`AGENTS.md`, breaking changes).
- **The virtual-device record is amended.** Its State ownership section
  names snapshot, fork, and `Derive` as the three things one can do with a
  run's state, and says `Derive` "starts a fresh run rather than continuing
  one". In-run configuration is a fourth, and the record should name it so
  a reader does not conclude the only way to change configuration is a
  fresh run.

## Requirements

Unless a requirement says otherwise, each example uses the two-switch
fabric from `src/common/netsim/fabric/README.md` (Example). `h1` is on
`sw1:1/1/1` and `h2` on `sw2:1/1/1`, both access ports on VLAN 10, and a
300 m multimode trunk joins `1/1/24` on the two switches and tags VLAN 10.

1. **Traffic in flight survives the change.** Example: inject `h1→h2` at
   `t0` and step until the frame is on the trunk cable. `Configure("sw2",
   …)` with the trunk and access port moved to VLAN 20. `Run` ends the
   frame's journey with a drop at `sw2` under the bridge's ingress-filter
   reason. `Report()` still holds the journey from `t0`.
2. **The transient between two devices is visible.** Example: a scenario
   moves every port on both switches from VLAN 10 to VLAN 20, with
   `ActionConfigure` for `sw1` at `t1` and for `sw2` at `t2 = t1 + 1s`. An
   injection at `t1 + 100ms` is dropped at `sw2`, and one at `t2 + 100ms`
   is delivered to `h2`.
3. **Learned state the new configuration still admits is kept.** Example:
   after `h1` has sent a frame, `sw1` has learned `h1` on `1/1/1` in VLAN
   10. A `Configure("sw1", …)` that changes only a VLAN name keeps the
   entry in `Snapshot()`. One that moves `1/1/1` to VLAN 20 removes it.
   `Retention()["sw1"]` matches `vswitch.Derive` on the same pair.
4. **Link state follows the switch's config.** Example: `Configure("sw1",
   …)` with `1/1/24` set to `AdminStatus: Down` makes the trunk `Down` in
   `Links()` and `sw2:1/1/24` oper `Down` in `sw2`'s port table. An
   injection from `h2` afterwards is not delivered to `h1`. Configuring it
   `Up` again restores the link.
5. **A refused change leaves the fabric untouched.** Example:
   `Configure("sw1", cfg)` where `cfg`'s port table lacks `1/1/24`, which
   a cable names, returns an error naming the port. `Configure("h1", …)`
   and `Configure("nope", …)` return errors naming the node. After each,
   `Spec()`, `Links()`, and `Fingerprint()` equal their values before the
   call.
6. **Held frames that fail get the fabric's clock.** Example: a routed
   switch holds a frame for neighbor resolution. A `Configure` at `t` adds a
   static route to the switch's VRF. Any VRF change alters
   `routing.RetentionKey` (`src/common/netsim/vswitch/routing/layer.go:1131`),
   so the routing layer is not retained, and the fabric records a drop
   journey for the held frame with `At == t`, and
   `Run` reaches `StopQueueDrained` or `StopConverged`. The run does not
   hang on a pending journey.
7. **Protocol timers restart from the new switch.** Example: a three-switch
   triangle running spanning tree has converged with `sw1` as root.
   `Configure("sw3", …)` lowers `sw3`'s bridge priority to 4096. `Run`
   with a window ends `StopConverged`, and `sw3` is root in the final
   snapshot. A `Configure` that changes nothing keeps every port role and
   adds no topology change.
8. **The scenario action round-trips.** Example: a `Scenario` with an
   `ActionConfigure` runs through `RunScenario`, and `Replay(res.Replay)`
   gives the same `Fingerprint()` and the same `Report()`. `Action.Validate`
   refuses an empty `Node` and a `Kind`/payload mismatch. `DiffScenarios`
   of two scenarios whose actions differ only in configuration reports the
   `vswitch.Diff` changes under subject `scenario.action`.
9. **Forks stay independent.** Example: fork a fabric and `Configure` the
   fork. The source's `Spec()`, `Switch("sw1").Config()`, and `Links()` are
   unchanged. `Configure` on the source then does not reach the fork.
10. **Compare runs the action on both sides.** Example: `fabric.Compare(a,
    b, sc, budget)` where `sc` carries an `ActionConfigure` on a switch both
    fabrics have applies it to both forks. A switch only one side has makes
    the comparison `Inconclusive` with `Err` set, which is how a failing
    `ActionFault` is handled today (`src/common/netsim/fabric/compare.go:100`).

## Out of scope

- Changing hosts, cables, or reflectors in a running fabric, or adding or
  removing a switch.
- Different actions per side in `Compare`, and a `search` domain over timed
  configurations. The gate needs both eventually, but only once something
  calls it.
- Lifting `RunScenario`'s refusal to run actions while streams are
  attached. `Configure` called directly between `Run` calls works with
  streams attached, as `SetFault` does.
- Evaluating the gate's invariants (management path, VLAN consistency).
  That work belongs to the device service.
- Moving the runtime fault out of `fabric.Config` (virtual-device record,
  Remaining capability gaps).
- Building a fabric from the network model.

## Units

### U1. `Fabric.Configure`

Files: `src/common/netsim/fabric/configure.go` (new),
`src/common/netsim/fabric/fabric.go`, `src/common/netsim/fabric/configure_test.go` (new)
After: none
Change: `func (f *Fabric) Configure(node string, cfg vswitch.Config) error`
changes switch `node` at `f.clock` in the order the Decisions give:
validate the whole spec, re-resolve the links on `node`, rebuild its port
table, `vswitch.Derive`, swap the switch and write `f.cfg.Switches[node]`
(with the rebuilt `Ports`, as `fabric.Derive` does), notify the ends of
changed links, `startLayers([node])`, drain emissions and neighbor
failures, reschedule the wake, clear `metadataCache`, and settle touched
frames. A node that is not a switch is refused before anything is read.
`SetFault`'s update and notify loop becomes `relinkAt(idx int) error`,
which `SetFault` and `Configure` both call. `build`'s per-switch port
rebuild and LAG member `LinkChange` become a helper that `build` and
`Configure` both call. `SetFault`'s existing tests are the regression
check for the first extraction, and `build`'s for the second.
Tests: `configure_test.go` covers requirements 1, 3, 4, 5, 6, 7, and 9,
one test function each, named for the behavior
(`TestConfigureKeepsInFlightFrame`, `TestConfigureRefusalLeavesFabricUnchanged`,
and so on). The requirement 7 case also asserts that a no-op `Configure`
leaves every port role in place. The requirement 6 case asserts the drop
entry's `At` against the fabric clock, because `vswitch.Derive` stamps
`time.Time{}` and only that assertion can tell the difference.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric`

### U2. `ActionConfigure`

Files: `src/common/netsim/fabric/scenario.go`, `src/common/netsim/fabric/run.go`,
`src/common/netsim/fabric/scenario_test.go`, `src/common/netsim/fabric/replay_test.go`,
`src/common/netsim/fabric/compare_test.go`
After: U1
Change: `ActionConfigure ActionKind = "Configure"` with payload
`Configure *ConfigureAction{Node string; Config vswitch.Config}`.
`ConfigureAction.Validate` refuses an empty `Node`. `Action.Validate`
counts the new pointer among the exactly-one payloads. `Normalize` and
`Clone` deep-copy `Config` with `vswitch.Config.Clone`. `Action.Diff`
reports a changed `Node` as field `configure.node`, and for the same node
appends `vswitch.Diff(a, b)` with each change's subject re-keyed to
`scenario.action` and the action index. `applyAction` moves the clock to
`a.At` when it is later, as `ActionFault` does, and calls `f.Configure`.
`runScenarioFork` in `compare.go` reaches it through `applyAction` and
needs no change.
Tests: requirement 2 in `scenario_test.go` as a scenario test.
Requirement 8 in `scenario_test.go` (validate and diff) and
`replay_test.go` (replay equality). Requirement 10 in `compare_test.go`,
with both the both-sides case and the one-side `Inconclusive` case.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric`

### U3. Documentation and record amendment

Files: `src/common/netsim/fabric/README.md`, `src/common/netsim/README.md`,
`docs/architecture/2026-09-10-virtual-device-direction.md`
After: U2
Change: the fabric README gets a "Reconfiguring a switch in a run" section
with a working example: requirement 2's two-step VLAN move, written as a
scenario. The section says what is kept and what is not, and that the
action refuses attached streams as the other actions do. The "Derivation
and state retention" section is corrected: `fabric.Derive(cur, spec)` is a
package function that starts a fresh run, and the section names
`Configure` as the in-run alternative. The action list under "Scenarios
and replay" gains `ActionConfigure`. The netsim README's fabric row stays
as it is unless its wording claims the configuration is fixed. The
virtual-device record gets a dated amendment (2026-09-25) under
Amendments that names in-run configuration as the fourth operation on a
run's state next to snapshot, fork, and `Derive`. The amendment states
that it continues the run, and that `vswitch.Derive` decides what the
switch keeps.
Tests: none. This is prose. The README example is compiled by copying it
into a scratch `main` under `$TMPDIR` and running it once, as the
existing README example is.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric/README.md src/common/netsim/README.md docs/architecture/2026-09-10-virtual-device-direction.md`

Waves: U1 | U2 | U3

The chain is real: U2's `applyAction` calls U1's method, and U3 documents
the API of both.

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric src/common/netsim/README.md docs/architecture/2026-09-10-virtual-device-direction.md
go test -race ./src/common/netsim/...
```

Do not run `verify --full` (it builds `generated/go/yang`). `go test -race
./src/common/netsim/search/...` is part of the second command and covers
the `search` package, which calls `Compare` and so reaches `applyAction`.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `go test -race ./src/common/netsim/...` green.
- [ ] Fabric README and the virtual-device record amendment are updated in
      the same change.
- [ ] This plan's `status` is set, with an outcome note under the title.
- [ ] No plan labels in code, comments, or commit messages.

## Open questions

- Should a run carry a run-level record of each configuration change, so
  that a reader of `Report()` can see why a drop at `t` differs from one
  before `t` without reading the scenario? The plan records none (see
  Decisions). Revisit when the device service shows a preview to an
  operator.
