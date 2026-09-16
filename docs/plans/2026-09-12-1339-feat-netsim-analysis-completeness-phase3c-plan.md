---
title: Network Simulation Analysis Completeness, Phase 3c - Plan
type: feat
date: 2026-09-16
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: rework
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
parent: docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
---

# Network simulation analysis completeness, phase 3c: Loop protection without spanning tree - Plan

> Implemented. 4 units, 2026-09-16T12:22:54Z to 2026-09-16T13:16:30Z.
> Two requirements needed a mechanism the Units had not named. R41c holds only
> because a probe leaves a port the spanning tree also forwards, so emission
> consults the tree's gate and the port's operational state; without that, a
> tree-discarding port probes, hears itself through the rest of the topology,
> and blocks for a loop the tree had already broken. And a probe carrying no
> VLAN resolves to the port's PVID, because VLAN 0 is not a VLAN a VLAN-aware
> bridge carries.
>
> Re-planned on 2026-09-16 against the tree that holds phases 3b (`10b696a1`)
> and 3d (`ca47a59d`). The 2026-09-14 draft's Decisions and Requirements
> survive; its Units did not, because the bridge admits exactly one gate and
> the draft assumed a layer could install a second one.

## Goal

A switch configured with loop protection detects a forwarding loop that no
spanning tree breaks — two of its ports joined through an unmanaged switch —
by sending probe frames and hearing its own probe return. It then blocks,
stops learning on, or disables the sending port, and recovers under one of
three recovery modes. The means are a capability package
`src/common/netsim/vswitch/loopprotect` holding a probe codec, per-port timers
on the fabric clock, and a `bridge.Gate` implementation, plus a bridge that
consults more than one gate so loop protection and spanning tree can both hold
a port.

Stop condition: the plan is wrong if a probe cannot return to its sender
through netsim's bridges, because they drop or consume it before the loop
closes.

This phase claims the parent's R41 and extends R9, R37, and R39.

## Decisions

The 2026-09-14 Decisions govern and are not re-opened; what follows records
them in short and states, with reasons, every place the landed tree moved them.

### What the draft settled, unchanged

- **Loop protection is netsim's own mechanism, drawn from vendor loop
  detection, not an emulation of any vendor's frames.** Each vendor's probe
  format is proprietary, so netsim parses no vendor probe. Sources, fetched
  2026-09-14: [H3C loop
  detection](https://www.h3c.com/en/d_201906/1192978_294551_0.htm) (multicast
  probe, a returned frame is a loop, a frame returning under a different VLAN
  tag is an inter-VLAN loop, actions block / no-learning / shutdown, block and
  no-learning restore after three detection intervals without a returned
  frame, default interval 30 s). HPE Aruba loop-protect and Huawei
  loopback-detect follow the same pattern; their pages returned 403 or empty
  when fetched, so nothing here rests on them.
- **Probe frame.** Destination `03:46:53:4c:50:00`, a locally administered
  group address; EtherType `0x88b5`, IEEE Std 802 Local Experimental
  EtherType 1; source is the switch MAC.
- **Detection.** A probe whose payload names this switch as the originator is
  a loop on the port the payload names as the sender. The switch consumes its
  own probe and never re-floods it. A foreign switch's probe is ordinary
  multicast data and floods, which is what lets a loop close through switches
  that have no loop protection.
- **Actions**, per port: `Block` (the port neither forwards nor learns, and
  keeps sending probes), `NoLearn` (learning stops, forwarding continues),
  `Disable` (the port neither forwards nor learns and sends no probes).
- **Recovery modes**: `Manual` (holds until `ClearLoopProtect`, a port cycle,
  or a configuration change), `Timer` (lifts `Duration` after the action was
  applied, whether or not the loop is gone, counting the recurrence),
  `LoopCleared` (lifts after `Duration` with no returned probe; every returned
  probe restarts the wait). Sources, each fetched 2026-09-14:
  [Cisco errdisable recovery](https://www.cisco.com/c/en/us/support/docs/lan-switching/spanning-tree-protocol/69980-errdisable-recovery.html),
  [Juniper `clear loop-detect enhanced interface`](https://www.juniper.net/documentation/us/en/software/junos/cli-reference/topics/ref/command/clear-loop-detect-enhanced-interface.html),
  [MikroTik Loop Protect](https://manual.mikrotik.com/docs/bridging-and-switching/user-guides/loop-protect/),
  [Extreme ELRP port shutdown](https://documentation.extremenetworks.com/release_notes/ExtremeXOS/16.1.2/EXOS_Release_Notes/16.1.2/c_elrp-port-shutdown.shtml),
  [TP-Link configuration guide](https://static.tp-link.com/res/down/doc/Port_Configuration_Guide.pdf?configurationId=2978),
  and the H3C page above.
- **Recovery defaults and limits.** `Duration` zero means three probe
  intervals. `Block` and `NoLearn` default to `LoopCleared`; `Disable`
  defaults to `Manual`, matching Cisco's recovery-off default. `LoopCleared`
  with `Disable` is refused at construction, because a disabled port sends no
  probes and can never observe the loop clearing. `Timer` adds no backoff:
  none of the fetched sources describes one.
- **Timers.** `Interval` zero means 5 s — netsim's own default, shorter than
  H3C's so tests converge quickly.
- **Independent of spanning tree.** Both may be configured on one switch, and
  an STP-discarding port neither sends nor receives probes.
- **Outcomes are port state, not issues**, and **no netmodel schema reports
  loop protection.**

### Where the landed tree moved the design

- **The bridge consults a list of gates, and `SetGate` keys the list by
  scope.** `Bridge` holds one `gate` and one `gateScope`
  (`src/common/netsim/vswitch/bridge/bridge.go:85-86`, `:131`), and `Switch`
  installs the STP layer into it (`src/common/netsim/vswitch/switch.go:291`).
  A second gate has nowhere to go. The two alternatives are to compose STP and
  loop protection behind one `bridge.Gate` inside `vswitch`, or to let the
  bridge hold several. Composition loses the scope: `gateScope` is a single
  `analysis.Scope` that the bridge records as a consulted dependency
  (`bridge.go:707-709`, `:977-979`, `:1180-1182`), `analysis.Scope` has no
  union (`src/common/netsim/analysis/scope.go:61-130`), and a composite would
  have to attribute a loop-protection block to the spanning-tree protocol
  scope — or to no scope at all on a switch that runs loop protection without
  STP. Each gate keeps its own scope and its own fact instead. Pre-1.0
  breaking changes land without shims (`AGENTS.md`).
- **`SetGate` keeps its name and its replacing semantics, keyed by scope**: it
  replaces the entry whose scope compares equal and appends otherwise. A
  plain appending `AddGate` would break `Derive`, which installs the freshly
  built layer through `newSwitch` and then installs the retained clone over it
  (`src/common/netsim/vswitch/derive.go:47`); appending would leave the bridge
  consulting both the converged clone and the unconverged layer it replaced,
  and `TestDerivedSwitchKeepsRolesWithAssignedBridgeAddress`
  (`src/common/netsim/vswitch/switch_test.go:1243`) is what would fail. Keying
  by scope makes every existing call site a list of one.
- **A denial is attributed to a gate that is sufficient on its own.** The
  ingress drop fires only when both `Learns` and `Forwards` are false
  (`bridge.go:716`), so its fact is the first gate, in installation order,
  that denies both; when no single gate denies both, it is the first gate
  denying `Forwards`. The egress and flood checks (`bridge.go:980`, `:1183`)
  ask `Forwards` alone, so theirs is the first gate denying `Forwards`. Plain
  first-denier attribution would name spanning tree for a port it merely holds
  in Learning (`Learns` true, `Forwards` false) while loop protection is what
  blocked it — the misattribution this whole decision exists to avoid. When
  every gate allows, the fact is the first non-nil gate's, which is what the
  single-gate bridge records today (`bridge.go:710-714`). STP installs first
  and loop protection second, so a port both layers hold discarding still
  reads `block_reason="bpdu-guard"` and the phase 3b corpus cases keep their
  traces.
- **A scope-only entry with a nil gate stays supported.** `newSwitch` calls
  `SetGate(nil, stpScope)` when STP is absent but the construction metadata
  carries spanning-tree content (`switch.go:236-240`), so the bridge reports
  the gap rather than silently forwarding. That entry consults its scope and
  gates nothing.
- **The returning probe is classified by `bridge.Ingress`, not by a second
  classifier.** The switch intercepts a frame addressed to the probe group
  ahead of the relay, as it does for a BPDU (`switch.go:735-744`), but then
  calls `s.bridge.Ingress(now, ingress, f, false, false)` to learn the VLAN
  the frame classified into and whether the ingress port is gated. Two things
  follow, both wanted. The inter-VLAN test of R41d compares the payload's VID
  against the classified VID rather than against a tag the probe may not
  carry — an access-port loop returns the probe untagged. And a probe arriving
  on a port that loop protection has already blocked is dropped with
  `ReasonPortBlocked` (`bridge.go:716-728`) before detection sees it, which is
  what stops a two-port loop from blocking both of its ports; see the
  reciprocal-probe decision below. This is the opposite of the BPDU rule, and
  deliberately: `interceptBPDU` bypasses the relay because a BPDU must be
  received on a discarding port (`switch.go:1754-1831`), while a loop probe
  must not be.
- **Under `Block` and `Disable`, exactly one port of a two-port loop is
  acted on; under `NoLearn`, both are.** Both ports probe, and each port's
  probe returns on the other, so each probe indicts the other port's sender.
  Whichever arrival the fabric processes first acts on its sender; the
  reciprocal probe then arrives on a port whose gate now denies both `Learns`
  and `Forwards`, and the bridge drops it at ingress (`bridge.go:716-728`)
  before detection sees it, so the second port is left alone. `NoLearn` denies
  only `Learns`, so the reciprocal probe is not dropped and both ports stop
  learning — which is what `NoLearn` is for. It contains the MAC flapping a
  loop causes; it does not break the loop, and the README says so. The outcome
  is deterministic in every case because the fabric pops arrivals from one
  totally ordered queue (`src/common/netsim/fabric/run.go:316-323`,
  `src/common/netsim/fabric/queue.go:35-55`).
- **A port emits probes on the strength of its operational state and the
  spanning-tree gate, never its own loop-protection verdict — except under
  `Disable`.** The switch applies both, in `applyLoopProtectEffects`; the
  layer knows about neither. The layer is itself a gate, so consulting every gate would stop
  a `Block`ed port from probing, its probes would stop returning, and
  `LoopCleared` would decay into a plain timer that lifts while the cable is
  still looped. `Disable` is the one action defined to stop probing, which is
  why `LoopCleared` with `Disable` is refused.
- **A self-originated probe is tagged by the bridge, through a new exported
  `Bridge.OriginateFrame(port string, vid vlan.ID, f ethernet.Frame)
  (ethernet.Frame, bool)`.** Egress tagging lives in the unexported
  `buildEgressFrame` (`bridge.go:1371`), which already answers "does this port
  carry this VID, and tagged or untagged" for the relay; the probe needs the
  same answer and must not re-implement it. The rejected alternative is to
  synthesize a `bridge.Ingress` descriptor and call the exported `EgressTo`
  (`bridge.go:1086`): it would fabricate an ingress port for a frame the
  switch originated, and put relay steps in the trace of something that is not
  a journey.
- **`Disable` is a port the layer holds down, not a port it reports down.**
  Spanning tree's BPDU guard disables a port entirely inside the layer
  (`src/common/netsim/vswitch/stp/layer.go:1577-1599`) and never touches
  `port.Table`, because effective port state is the fabric's to own and a
  capability layer consumes it. Loop protection follows. The consequence — a
  real errdisabled port drops carrier and its peer sees the link go down —
  is not modeled, and the package README says so.
- **The port cycle that clears a `Manual` action is a link report, and an
  admin-status change rebuilds the layer instead.** The draft named both a
  link cycle and an admin cycle "through `Derive`". `Derive` retains a layer
  on configuration equality alone (`src/common/netsim/vswitch/derive.go:44`),
  so a retained layer never learns that a port's admin status changed; only
  LAG compares member port state as well (`derive.go:63-77`, the
  `lagMemberStatesEqual` call at `:71`). Loop protection retains its layer only when the
  protected ports' admin and operational state match too, so an admin cycle
  rebuilds the layer and clears the action by construction, and a link cycle
  reaches the retained layer through `Switch.LinkChange`
  (`switch.go:2146-2192`), the path the fabric already drives. Both sides of
  the retention comparison are configurations `New` filled, per
  `docs/solutions/architecture-patterns/validate-and-derive-judge-what-new-builds.md`.
- **Two participation guards decide today that a switch has no timers, and
  both name spanning tree and LAG by hand.** `Fabric.startLayers` skips a
  switch whose configuration has neither
  (`src/common/netsim/fabric/fabric.go:530-532`), so it never reports the
  switch its links, never drains it, and never schedules its wake; a wake is
  otherwise reached only from a frame arrival, an earlier wake, `Mcheck`, or a
  link-state change. `Switch.Start` has the same guard
  (`src/common/netsim/vswitch/switch.go:1835-1838`). Left alone, a fabric of
  two loop-protected switches with no STP, no LAG and no traffic — which is
  every R41 acceptance example — emits no probe at all. Both guards admit a
  loop-protection configuration.
- **The ownership test runs on the decoded payload, before any `Ingress`
  call.** A foreign probe classified once in the interception and again on the
  fall-through would put two classification steps in one journey, and the
  corpus compares the ordered step list exactly
  (`src/common/netsim/internal/netsimtest/corpus.go:873-884`).
- **Recovery is a `PortInfo` transition, not a trace step.** The draft named
  three trace steps. A recovery fires in `Wake` with no frame in hand, and a
  trace belongs to a frame, so `loopprotect.port.recover` has nothing to
  attach to. Two steps survive — `loopprotect.probe.return` on the
  interception of a returned probe and `loopprotect.port.block` on the
  transition it causes — and a journey dropped by a protected port cites the
  gate fact carrying the reason, exactly as phase 3b's loop-guard case does
  (`src/common/netsim/internal/netsimtest/stp_cases.go:1087-1091`).
- **The payload puts its fixed fields ahead of the variable-length one**:
  version (1 octet), originating MAC (6), VID (2, big-endian), sequence
  number (4, big-endian), port-name length (1), port name. The draft ordered
  the name before the VID and sequence, which makes every fixed field's offset
  depend on a length read first, for nothing. The frame is not padded to the
  60-octet Ethernet minimum; netsim enforces no minimum, and padding would
  make the codec's placement assertions harder to read.
- **The codec is pinned by absolute octet offsets with distinct values, not by
  a round trip.** A symmetric codec passes its own round trip whatever it
  does with the wire, which is how phase 3d put two MST identifiers in each
  other's octets
  (`docs/solutions/conventions/a-codec-round-trip-cannot-locate-a-field-on-the-wire.md`).

- Ruled: `Layer.Receive` drops the ingress-port parameter the Units first gave
  it. Why: nothing in the layer reads it — the action targets the port the
  payload names, and the port a probe returned on is in the switch's hand at
  the interception, where the trace step is built. Cost if wrong: a later unit
  that wants the return port in `PortInfo` re-adds the parameter and a field.
- Ruled: `Layer.Wake` emits only once its own interval is due. Why: a switch
  wakes its layers together, so `Wake` also runs at every spanning tree hello
  and every recovery expiry, and emitting on each would probe far ahead of the
  configured interval. Cost if wrong: nothing outside the layer; the metering
  is one comparison and its test.

## Requirements

1. **R41a:** A returned probe applies the configured action to the port the
   probe was sent from, and to that port only.
   **Acceptance example:** `sw1` enables `Block` on ports `1` and `2` with
   interval 5 s; `sw2` has no loop protection; cables join `sw1:1`-`sw2:1` and
   `sw1:2`-`sw2:2`; host `h1` is on `sw1:3` and host `h2` on `sw2:3`; no STP
   runs. After one probe interval exactly one of `sw1:1` and `sw1:2` reports
   `Action=Block` and the other reports none. A broadcast `h1` sends
   afterwards reaches `h2` exactly once, where without loop protection the
   same fabric never stops forwarding it. With `NoLearn` in place of `Block`,
   both ports report the action and the broadcast still loops.
2. **R41b:** Each recovery mode follows its rule.
   **Acceptance example:** interval 5 s, `Duration` 15 s, action applied at t.
   - `LoopCleared` with the loop still cabled stays applied, because probes
     keep returning. A cable fault just before the probe due at t+20 s
     restores the port at t+30 s — 15 s after the last returned probe, which
     arrived at t+15 s.
   - `Timer` with the loop still cabled lifts at t+15 s. The next returned
     probe reapplies the action and `PortInfo.Recurrences` reads 1.
   - `Manual` is still applied at t+1 h. It lifts on `ClearLoopProtect`, and
     separately on a `LinkChange` down followed by up; an up report alone
     leaves it applied.
3. **R41c:** A port that spanning tree holds discarding reports no loop.
   **Acceptance example:** the R41a fabric with RSTP on both switches detects
   nothing, the tree's Alternate port is the only block, and no port reports a
   loop-protection action.
4. **R41d:** A probe that returns under another VLAN is an inter-VLAN loop.
   **Acceptance example:** `sw2` joins VLAN 10 to VLAN 20 through a cable
   between an access port in each. A probe `sw1` sent on VID 10 returns
   classified into VID 20, `PortInfo.InterVLAN` is true, and the
   `loopprotect.probe.return` step's fact names both VIDs.
5. **R9:** `LoopProtect` (`Ports` with `Action`, `Recovery.Mode`,
   `Recovery.Duration` and `VLANs` per port, plus `Interval`) participates in
   validation, normalization, `Clone`, and `Diff`. Validation refuses an
   unknown port, a LAG member port (the LAG port carries the setting), a
   negative `Interval` or `Duration`, an unknown action or recovery mode, and
   `LoopCleared` with `Disable`; `vswitch.Config.Validate` additionally
   refuses loop protection without a bridge and a `VLANs` entry the port's
   switchport does not admit.
   **Acceptance example:** changing one port's `Action` from `Block` to
   `Disable` yields exactly one `trace.Change`, with `Field` naming the
   action and typed `From` and `To` facts.
6. **R37:** Installing more than one gate leaves the single-gate traces
   unchanged, attributes a denial deterministically, and keeps derive
   invalidation intact.
   **Acceptance example:** a bridge with an STP gate and a loop-protection
   gate, both denying both predicates, records the STP gate's fact on the
   ingress drop; with spanning tree denying only `Forwards` and loop
   protection denying both, the same drop records the loop-protection fact;
   the ports consulted and their scopes are the same set in either order of
   arrival. `Derive` over an unchanged
   configuration leaves the bridge consulting one gate per layer, not two per
   layer, and the derived switch keeps its spanning tree roles.
7. **R39:** The corpus admits
   `troubleshooting/loop-protect-contains-access-loop`, whose false answer is
   that the fabric floods the broadcast forever because no spanning tree runs.

## Out of scope

- Vendor probe formats, SNMP traps, and log actions.
- Loop detection through a VLAN the switch does not carry.
- Probes on a LAG's member ports. A LAG sends on its selected member, so the
  LAG port carries the configuration and the member ports carry none.
- The physical consequences of an errdisabled port: netsim holds the port
  discarding and does not report its link down.
- Per-VLAN recovery. An action is per port, as every fetched vendor's default
  target is.

## Units

### U1. The `loopprotect` package

Files: `src/common/netsim/vswitch/loopprotect/` — new: `config.go`,
`probe.go`, `layer.go`, `fact.go`, `diff.go`, their `_test.go` siblings, and
`README.md`
After: none
Change: `Config` carries `Interval` and `Ports map[string]Port`, each `Port`
holding `Action`, `Recovery{Mode, Duration}` and `VLANs []vlan.ID`, with
`Clone`, `Normalize` (interval 5 s, duration three intervals, mode per action)
and `Validate(ports port.Table) error` following `stp.Config`
(`src/common/netsim/vswitch/stp/config.go:126-369`). `Diff(a, b Config)
[]trace.Change` normalizes both sides and emits one change per changed field
with typed facts, as `stp.Diff` does (`stp/diff.go:128`). `Encode(p Probe,
src netaddr.MAC) ethernet.Frame` and `Decode(f ethernet.Frame) (Probe, error)`
carry the payload layout the Decisions fix. `Layer` offers `New(cfg Config,
ports port.Table, mac netaddr.MAC) (*Layer, error)`, `Receive(now time.Time,
port string, vid vlan.ID, p Probe) Effects`, `Wake(now time.Time) Effects`,
`NextWake() (time.Time, bool)`, `LinkChange(now time.Time, port string, up
bool) Effects`, `Clear(now time.Time, port string) bool`, `Clone() *Layer`,
`PortInfo(port string) PortInfo`, and the `bridge.Gate` pair `Learns(port
string, vid vlan.ID) bool` and `Forwards(port string, vid vlan.ID) bool`,
plus `ForwardingFact` so the bridge can record a denial. `Effects` carries
`Emissions []Emission{Port string, VID vlan.ID, Frame ethernet.Frame}`; the
switch tags each one before it reaches the wire. Probes are emitted from
`Wake` only, and ports are walked in sorted order so a wake's emissions are
ordered.
Tests: `probe_test.go` asserts the encoded payload's absolute offsets against
literals whose octets differ in every field — MAC `02:11:22:33:44:55`, VID
`0x0141`, sequence `0xa1b2c3d4`, name `et-0/0/7` — and refuses a wrong
version, a payload shorter than 14 octets, a zero name length, a name running
past the payload, and trailing octets after the name. `layer_test.go` covers
R41b per mode against an injected clock, R41d at the layer, the recovery
default per action, `Clear`, the link-cycle clear and an up report alone, and
that two wakes an interval apart yield the same frames but for the sequence
number. It also pins the gate surface the later units consume: `Learns` and
`Forwards` for each of the three actions and for an unprotected port, the
`ForwardingFact` each denial produces, and that a `Block`ed port keeps
appearing in `Wake`'s emissions while a `Disable`d one does not.
`config_test.go` covers every refusal in R9's list and the R9 diff example.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/loopprotect`

### U2. A bridge that consults more than one gate

Files: `src/common/netsim/vswitch/bridge/bridge.go`,
`src/common/netsim/vswitch/bridge/bridge_test.go`,
`src/common/netsim/vswitch/switch_test.go`. The three `SetGate` call sites
(`switch.go:238`, `:291`, `derive.go:47`) need no edit, which is the point of
keying the list by scope; repository-wide there are no others.
After: none
Change: `Bridge` holds `gates []gateEntry{gate Gate; scope analysis.Scope}`.
`SetGate(g Gate, scope analysis.Scope)` keeps its name and replaces the entry
whose scope compares equal, appending when none does; a nil gate with a scope
stays a scope-only entry. The ingress consult (`bridge.go:704-728`), the
unicast egress check (`:977-999`) and the flood check (`:1180-1199`) consult
every entry's scope, take `Learns` and `Forwards` as the conjunction over the
non-nil gates, and record the fact of the gate the attribution decision names.
`gateFact` takes the deciding gate rather than reading `b.gate`. The three
existing call sites keep their behavior: each installs one scope, so each
bridge still holds a list of one.
Tests: `bridge_test.go` gains two stub gates and covers R37's bridge half —
both gates denying both predicates records the first's fact, spanning tree
denying only `Forwards` while the second denies both records the second's, a
scope-only nil entry gates nothing but is consulted, and the consulted scope
set is the union in installation order. `switch_test.go` covers the half the
bridge cannot see: `TestDerivedSwitchKeepsRolesWithAssignedBridgeAddress`
(`switch_test.go:1243`) still passes, and a new case asserts that `Derive`
over an unchanged configuration leaves one gate entry per scope rather than
two, so a stale replaced layer cannot be consulted alongside the retained one.
The existing gate tests stay and prove the single-gate behavior is unchanged.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/bridge src/common/netsim/vswitch`

### U3. Switch and fabric integration

Files: `src/common/netsim/vswitch/switch.go`, `config.go`, `diff.go`,
`derive.go`, `src/common/netsim/vswitch/port/port.go`,
`src/common/netsim/vswitch/bridge/bridge.go`,
`src/common/netsim/fabric/fabric.go`, their tests,
`src/common/netsim/fabric/loopprotect_test.go` (new),
`src/common/netsim/vswitch/README.md`
After: U1, U2
Change: `port.LayerLoopProtect trace.Layer = "loopprotect"` joins the layer
constants (`port/port.go:18-48`) and `Config.Capabilities()` reports it when
`LoopProtect` is present (`config.go:64-105`), so enabling the capability
diffs as a capability rather than as a bare field
(`diff.go:138-140`). `vswitch.Config` gains `LoopProtect
*loopprotect.Config`, validated (including the bridge and switchport-VLAN
cross-checks R9 names), normalized, cloned and diffed beside `STP`
(`config.go:37`, `:147-154`, `:392-398`, `:442-445`). `newSwitch` builds the
layer after STP and installs it with `bridge.SetGate(sw.loopprotect,
protocolScope(nodeID, port.LayerLoopProtect))`, whose scope differs from the
spanning tree's, so both stand. `Bridge.OriginateFrame` exports the egress
tagging `buildEgressFrame` holds (`bridge.go:1371`), including its
VLAN-unaware answer: a bridge with no `VLAN` configuration carries every VID
untagged (`bridge.go:1381-1383`). `Switch.forward` intercepts a frame whose
destination is the probe group ahead of the relay and decodes it; when the
payload names another switch it falls through untouched and floods as
ordinary multicast, and only when the payload names this switch does the
interception run `bridge.Ingress` with `learn` and `commit` false — to
classify the VLAN and to honour a gated ingress port — then call
`Layer.Receive` and emit the `loopprotect.probe.return` and
`loopprotect.port.block` steps. `Switch.ClearLoopProtect(now, port)` exposes
the manual clear. `Switch.Start` (`switch.go:1835-1838`), `Wake`
(`:2057`) and `NextWake` (`:2070`) admit and drive the layer beside STP and
LAG, and `Fabric.startLayers` admits a loop-protection configuration in the
same guard (`src/common/netsim/fabric/fabric.go:530-532`) so the switch is
told its links, drained, and given a wake. `Wake`'s emissions pass through
`OriginateFrame` for each port and VID before they reach `s.emissions`, so a
port that does not carry a VID sends no probe for it. `Switch.LinkChange`
forwards link reports to the layer. `Derive` retains the layer when
`loopprotect.Diff` is empty and every protected port's admin and operational
state matches, following the LAG precedent (`derive.go:63-77`); the
comparison reads the runtime tables, so a `Derive` taken while a protected
port is operationally down rebuilds the layer and clears its action, which is
the behavior LAG already has and which the package README states.
Tests: `switch_test.go` covers the interception (own probe consumed, foreign
probe flooded with one classification step and not two, probe on a gated port
dropped before detection), the `Derive` retention, the admin-cycle rebuild,
and the R9 diff through `vswitch.Diff` including the capability change.
`fabric/fabric_test.go` covers the widened participation guard: a fabric of
loop-protected switches with no STP and no LAG schedules a wake and emits a
probe without any traffic injected. `fabric/loopprotect_test.go` covers R41a
end to end — the single acted-on port under `Block`, both ports under
`NoLearn`, and the broadcast delivered once — plus R41c with RSTP on both
switches and R41d across the two-VLAN fabric.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch src/common/netsim/fabric`

### U4. Corpus, package README, and the direction record

Files: `src/common/netsim/internal/netsimtest/loopprotect_cases.go` (new),
`src/common/netsim/internal/netsimtest/corpus_test.go`,
`src/common/netsim/internal/netsimtest/README.md`,
`src/common/netsim/vswitch/loopprotect/README.md`,
`src/common/netsim/README.md`,
`docs/architecture/2026-09-10-virtual-device-direction.md`
After: U3
Change: `CaseTroubleshootingLoopProtectContainsAccessLoop` registers the R39
case through `registry.MustRegister`, built like the phase 3b loop-guard case
(`netsimtest/stp_cases.go:1022-1091`): the R41a fabric, a broadcast forwarded
after the block, `Dropped` with `bridge.ReasonPortBlocked`, and a gate fact
naming the loop-protection reason. The README gains its bullet. The
`loopprotect` README carries the package description, a runnable example, the
detection and action semantics, the recovery table with its vendor citations,
the sentence that `NoLearn` contains a loop's MAC flapping without breaking
the loop, the sentence that a `Derive` taken while a protected port is down
clears its action, and a "Not modeled" section naming vendor probe formats,
per-VLAN recovery, and the carrier loss a real errdisable causes. The direction record gains a
"Loop protection outside spanning tree" section under the spanning-tree one,
stating that the mechanism is netsim's own, that an action is a port state
rather than an issue, and what it does not emulate.
Tests: `corpus_test.go` admits the case through `AssertCase`, which executes
it twice and compares steps, changes and metadata for determinism.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim docs/architecture/2026-09-10-virtual-device-direction.md`

Waves: U1 U2 | U3 | U4

## Verification

```bash
go test -race ./src/common/net/... ./src/common/netsim/...
go vet ./src/common/net/... ./src/common/netsim/...
.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim \
  docs/architecture/2026-09-10-virtual-device-direction.md \
  docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-phase3c-plan.md \
  docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
```

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] Every R41 acceptance example passes, including loop containment in a
      fabric with no spanning tree.
- [ ] The `loopprotect` README, the vswitch and netsim READMEs, the corpus
      README, and the direction record are updated in the same change.
- [ ] This plan's `status` set with an outcome note under its title, and the
      parent's U3c `Landed:` line filled.
- [ ] No plan labels (U1, R41a) in code, comments, or commit messages.

## Open questions

- Whether `Timer` recovery needs a cap on recurrences. The plan adds none
  because no fetched source describes one, and a port that re-blocks every
  `Duration` forever is the honest simulation of a cable nobody unplugged. If
  a corpus case wants to assert "this converges", it asserts the recurrence
  count instead.
- Whether a probe should also go out on a port whose spanning tree state is
  Learning rather than Forwarding. The plan sends only where the spanning-tree
  gate forwards, so a port converging through Learning skips one probe
  interval. Nothing in the fetched sources settles it, and the cost of being
  wrong is one interval of detection delay on a port that is converging
  anyway.
