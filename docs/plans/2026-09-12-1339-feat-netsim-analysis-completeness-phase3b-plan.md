---
title: Network Simulation Analysis Completeness, Phase 3b - Plan
type: feat
date: 2026-09-15
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
parent: docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
---

# Network simulation analysis completeness, phase 3b: Information lifetime, guards, and the VLAN-aware gate - Plan

> Implemented. 5 units, 2026-09-15T14:52Z to 2026-09-15T16:45Z.

## Goal

Spanning-tree information stops holding a port blocked once it has outlived
its message age, and the four configured guards produce their port outcomes,
so a simulated bridge answers "why is this port still blocked" and "why did
this edge port go down" from state rather than from silence. The same phase
puts the two structures the later tree phases need in place while only one
tree exists: the layer keys its state by tree, and the bridge asks the gate
about a port and a VLAN rather than a port alone.

Stop condition: the plan is wrong if moving VLAN classification ahead of the
ingress gate cannot preserve the `protocol-link-unknown` metadata that
`CaseTopologyShadowingUnknownUplinkSTP` rests on
(`src/common/netsim/internal/netsimtest/cases.go:1064`), because the gate
scope is consulted at `src/common/netsim/vswitch/bridge/bridge.go:446` for
every frame that reaches the gate and after the move a frame dropped in
classification never reaches it.

This phase claims parent R15, extends R9 and R39, and lands the gate and
tree-keying that phases 3d and 3e build on. It claims no part of R14.

## Decisions

- **The parent's Decisions govern.** Phases 3 and 4 are landed and used as
  they are.
- **Phase 3b is the first of three spanning-tree phases.** It covers
  information lifetime, guards, and the two structural seams. MSTP is phase
  3d and per-VLAN RSTP is phase 3e. Why: the original phase 3b drafted all
  three together as six chained units, which is the size the `plan` skill
  splits, and the three capabilities share only the structures this phase
  lands. User-directed 2026-09-15.

### Information lifetime

- **Received information ages on message age as well as on silence.** A
  received BPDU is accepted only while `MessageAge + 1 <= MaxAge`, taking
  `MaxAge` from the BPDU being processed rather than from this bridge's
  configuration; when the test fails the information is discarded at once
  rather than stored. Accepted information then lives for `3 × HelloTime`,
  which is what the layer already does
  (`src/common/netsim/vswitch/stp/layer.go:593`, `:1112`). Why: the layer
  today applies only the second half of the rule, so a BPDU naming a root that
  no longer exists refreshes the timer on every hop and keeps a port blocked
  forever. The UNH-IOL RSTP conformance suite states the rule: "RSTP treats
  the Message Age parameter in received BPDUs as an incrementing hop count
  with Max Age as its maximum value. Message Age is incremented after being
  received on the Root Port. If Message Age is greater than Max Age, the BPDU
  is discarded", and its `rcvdInfoWhile` test separates "Information Aged Out
  when Message Age greater than Max Age" from "Information Aged Out when
  Message Age less than Max Age"
  (<https://www.iol.unh.edu/sites/default/files/testsuites/bfc/RSTP_conformance_Q.pdf>,
  citing IEEE Std 802.1Q-2011 sub-clauses 13.23.6, 13.27.30, and 13.28). The
  received `MaxAge` is the bridge-local one only when this bridge is root, and
  the fabric suites already build bridges with differing timers
  (`src/common/netsim/fabric/stp_test.go:113`), so naming the source matters.
- **The increment on origination stays where it is.** `makeBPDU` already adds
  one second off the root port (`src/common/netsim/vswitch/stp/layer.go:524`).
  Why: the defect is the missing acceptance test, not the missing increment,
  and touching both at once would make it unclear which change the aging test
  proves.
- **A version 3 BPDU keeps decoding as an RST BPDU.** `Decode` accepts any
  `version >= 2` with type `0x02` (`src/common/netsim/vswitch/stp/bpdu.go:435`)
  and reads the CIST prefix, which stays as it is. Why: this looked like a
  silent misread worth refusing, and it is the opposite. The UNH-IOL MSTP
  conformance suite states that "A compliant device must not validate an MST
  BPDU based on the value encoded in the Protocol Version Identifier field.
  This allows future versions of the Spanning Tree Protocol to use this field
  while providing support for legacy versions"
  (<https://www.iol.unh.edu/sites/default/files/testsuites/bfc/MSTP_conformance.pdf>,
  Test MSTP.op.1.3, citing IEEE Std 802.1Q-2011 sub-clause 14.4). Reading the
  prefix is how an RSTP bridge peers with an MST region at all, and refusing
  it would leave a netsim RSTP bridge facing an MSTP neighbour with both ends
  Designated and Forwarding, which is an unbroken loop and a new false answer.
  Phase 3d replaces the prefix reading with real MST support.
- **Hop-count aging is not in this phase.** `remainingHops` is MSTP's internal
  form of the same idea and arrives with the MSTI vectors in phase 3d. Why:
  there is no hop count on the wire or in `portState` today, and adding one
  that nothing decrements is a field a reader would have to explain.

### Guards

Guards are per-port configuration whose outcomes are port states, not issues.
A port state an operator can see beats an issue code they have to look for.

- **`BPDUGuard`.** A BPDU received on the port disables it for spanning tree
  with reason `bpdu-guard`, and it stays disabled until a `LinkChange`
  reports the port down and then up again. Why: this is the near-universal
  vendor behavior and the reason the guard exists, to keep an unexpected
  bridge on an access port from joining the topology.
- **`RestrictedRole`, root guard.** The port is never selected as root port.
  Superior information on it makes it Alternate, and the bridge's own root
  choice is unaffected.
- **`RestrictedTCN`.** A topology change received on the port does not
  propagate to the other ports.
- **`LoopGuard` is netsim's own design, drawn from three vendors.** Cisco,
  Juniper, and Arista all apply loop protection only to ports that were
  receiving BPDUs and recover on the next BPDU
  (<https://www.cisco.com/c/en/us/support/docs/lan-switching/spanning-tree-protocol-stp-8021d/218321-configure-stp-with-loop-guard-and-bpdu-s.html>,
  <https://www.juniper.net/documentation/us/en/software/junos/stp-l2/topics/topic-map/spanning-tree-loop-protection.html>,
  <https://www.arista.com/en/um-eos/eos-spanning-tree-protocol>).
  User-directed 2026-09-14, carried forward unchanged.
  - *Trigger.* The port's role is Root, Alternate, or Backup and its received
    information expires, by silence or by message age. The port enters
    `loop-inconsistent`: Discarding, role Alternate, excluded from root-port
    selection so the tree reconverges around it, and never Designated.
  - *Recovery.* A BPDU received on the port clears the state and normal role
    selection runs.
  - *Inactive.* The guard does nothing on a port that is operationally edge or
    not point-to-point, which is where Cisco and Arista rule it out.
  - *Link down clears it,* and a port that comes up has no expired
    information, so it has no trigger.
- **Two guard combinations are rejected at construction.** `LoopGuard` with
  `RestrictedRole`, which Cisco and Juniper make mutually exclusive, and
  `LoopGuard` with `AdminEdge`, which asks the guard to watch a port it is
  defined not to watch. Why: a configuration whose two halves contradict each
  other has no correct simulated answer, and refusing it names the conflict
  where the operator can see it.
- **Guard state is per tree, and this phase has one tree.** Phase 3d extends
  loop guard across a boundary port's instances. Why: writing the guard
  against the tree structure U2 lands means phase 3d extends it rather than
  rewriting it.

### The two structural seams

- **The layer keys its state by tree, with exactly one tree.** A `tree` holds
  what `Layer` holds globally today: root ID, root path cost, root port, the
  hello and topology-change timers and counters, and the per-port received
  information, role, state, forward-delay timer, proposal and agreement flags,
  transmit-hold counters, and forward-transition count
  (`src/common/netsim/vswitch/stp/layer.go:48`, `:72`). Why: the
  priority-vector comparator is already factored (`:123`, `:130`), but every
  value it reads and writes is a bridge-global singleton, so the work MSTP
  needs is re-keying the state, not substituting a vector. Doing it here, with
  one tree, makes the existing RSTP suite the proof that the refactor changed
  no behavior; doing it in phase 3d would mix a refactor with new protocol in
  one diff, where a failing test names neither.
- **Two things stay bridge-global, and the unit says so.** The port
  identifier, derived from the index in sorted `cfg.Ports`
  (`src/common/netsim/vswitch/stp/layer.go:188`), appears on the wire and in
  `PortInfo`; and the port key set and its iteration order, which
  `raiseTopologyChange` (`:503`) and the emit loops walk through
  `sortedKeys(l.ports)` and which reaches the caller as the order of
  `Effects.Flush`. Why: a per-tree port map that iterates differently would
  reorder flush lists with no behavior change to point at.
- **The exported accessors keep their signatures and resolve the single tree
  internally.** `Root`, `TopologyChanges`, `BridgeID`, `Times`, and `PortInfo`
  (`src/common/netsim/vswitch/stp/layer.go:273` onward) are called from
  `src/common/netsim/vswitch/switch.go:1991` and asserted through `sw.Roles()`
  in `src/common/netsim/fabric/stp_test.go:807`. Why: a tree parameter would
  make U2 edit the switch and every caller, and "the refactor changes no
  exported behavior" would stop being true. Phase 3d adds the parameter when a
  second tree makes it mean something.
- **The gate takes a VLAN.** `bridge.Gate` becomes `Learns(port, vid)` and
  `Forwards(port, vid)`, and the layer maps the VID to its tree, which in this
  phase is always the one tree. Why: a port's forwarding state depends on the
  frame's VLAN as soon as more than one tree exists. Allied Telesis states the
  mechanism: MSTP "supports load balancing by mapping different VLANs to
  different spanning tree instances. As such, different instances can use
  different active links"
  (<https://www.alliedtelesis.com/wp-content/uploads/2026/06/c613-16036-00-a.pdf>).
  Landing the signature now means the bridge and switch seams move once,
  against one tree whose answers cannot yet differ by VLAN.
- **VLAN classification moves ahead of the ingress gate.** The ingress gate
  runs in `Bridge.Ingress` (`src/common/netsim/vswitch/bridge/bridge.go:406`)
  at `:450` and drops the frame as `port-blocked` at `:456`, before the
  classification block that starts at `:469`, so no VID is in scope.
  Classification moves first and the gate is consulted with the classified
  VID. Why: the alternative, leaving ingress gating VLAN-blind, would let a
  port blocked for one instance still accept that VLAN's frames, which the
  next phase would have to undo. It also matches the model the direction
  record commits to, where classification belongs to the ingress process and
  active topology enforcement to the forwarding process that follows it
  (`docs/architecture/2026-09-10-virtual-device-direction.md:104`), so today's
  order is the deviation. The reserved-address drop stays ahead of both.
  User-directed 2026-09-15.
- **A classification failure now outranks `port-blocked`.** A frame on a
  gate-blocked port that also fails classification reports the classification
  reason: `ReasonCustomerVLAN`, `ReasonAdmission`, `ReasonNoPVID`,
  `ReasonIngressFilter`, or `ReasonUndefinedVLAN`
  (`src/common/netsim/vswitch/bridge/bridge.go:502`, `:579`, `:628`, `:656`,
  `:540`). Why: this follows from the order, and it is the more useful answer,
  because a frame the port would never have admitted is not a spanning-tree
  question. The change is deliberate rather than incidental, so the unit
  asserts it.
- **A gate-blocked frame now carries its classified FID.** `res.FID` is left
  at zero today because the drop happens before classification
  (`src/common/netsim/vswitch/bridge/bridge.go:463` passes a literal `0` to
  `egressSnapshot`). After the move it carries the classified VID, which
  `compare` and the corpus treat as part of the observable
  (`src/common/netsim/internal/netsimtest/corpus.go:1110`). Why: a drop that
  names the VLAN it was classified into is strictly more informative, and
  suppressing it to keep the old value would be inventing a zero.
- **The gate scope is consulted where the gate is consulted.** A frame dropped
  during classification no longer consults the STP scope
  (`src/common/netsim/vswitch/bridge/bridge.go:446`). Why: spanning-tree state
  could not have changed that frame's outcome, so an unknown-uplink issue on
  it is noise. The unit checks
  `CaseTopologyShadowingUnknownUplinkSTP`
  (`src/common/netsim/internal/netsimtest/cases.go:1064`) still gets its
  metadata, since its frames are classified successfully; the stop condition
  above is the case where it does not.
- **The gate reaches the layer through an adapter.** `*stp.Layer` is installed
  as the gate directly (`src/common/netsim/vswitch/switch.go:285` and
  `src/common/netsim/vswitch/derive.go:47`), unlike LAG, which goes through
  `lagSelector` (`switch.go:2253`, installed at `derive.go:74`). A small
  `stpGate` adapter in `vswitch` takes the VID and calls the layer. Why: the
  translation from a bridge-level VLAN to a protocol-level tree belongs to the
  switch that owns both, and the adapter is where phase 3e's per-VLAN mapping
  will live.
- Ruled: no `stpGate` adapter this phase; `*stp.Layer` stays installed as the
  gate directly and its `Learns` and `Forwards` take the VID, resolving it to a
  tree through `treeFor`. Why: the mapping the adapter was to hold lives in the
  layer, which owns the trees, so an adapter here would be a pass-through with
  one caller, which `docs/code-style.md` refuses. The adapter earns its place in
  phase 3e, where the switch holds a per-VLAN mapping the layer does not own.
  Cost if wrong: phase 3e adds the adapter and changes the two installation
  sites, `src/common/netsim/vswitch/switch.go:285` and
  `src/common/netsim/vswitch/derive.go:47`.
- **The gate stays pure and keeps no commit flag.** Why: it is a query over
  state the receive path already settled, and the landed peek discipline
  (`src/common/netsim/vswitch/switch.go:690`) threads `mutate` to the receive
  path, not to the gate.

### Scopes and validation

- **The gate scope stays port-keyed.** `analysis.FieldScope(b.gateScope,
  "ports", name)` (`src/common/netsim/vswitch/bridge/bridge.go:446`) is
  unchanged. Why: with one tree a VLAN adds nothing to the scope, and phase 3d
  is where a tree component earns its place.
- **Guards name ports that exist and are not LAG members,** as the existing
  port validation does. Validation errors keep the file's field-path form,
  `ports.<name>.<field>` (`src/common/netsim/vswitch/stp/config.go:279`).

## Requirements

1. **R15a:** Information whose message age has reached max age is discarded
   rather than stored, so a vanished root ages out.
   **Acceptance example:** at the layer, a `Receive` of a hand-built BPDU with
   `MessageAge` one second below its `MaxAge` is stored, and one with
   `MessageAge` equal to `MaxAge` is not, leaving the port's previous
   information to expire on its own timer. At the fabric, a four-switch RSTP
   ring whose root is removed while BPDUs naming it circulate elects a new
   root once the circulating age reaches max age. The counterfactual belongs
   to the layer test, not the fabric one, because `makeBPDU` always increments
   (`src/common/netsim/vswitch/stp/layer.go:524`) and a fabric run has no hook
   to hold the age at zero.
2. **R15b:** Each guard produces its port outcome.
   **Acceptance example:** a BPDU on a `BPDUGuard` edge port leaves the port
   disabled with reason `bpdu-guard`, and it stays disabled across a wake with
   no further BPDU and until a link-down then link-up. A superior BPDU on a
   `RestrictedRole` port leaves that port Alternate and the bridge's root
   unchanged. A topology change received on a `RestrictedTCN` port flushes
   nothing on the other ports. With `LoopGuard`, an Alternate port whose
   designated peer stops sending stays Discarding with reason
   `loop-inconsistent` and does not become Designated; the next BPDU on it
   restores Alternate. Without `LoopGuard`, the same port becomes Designated
   and forwards.
3. **R15c:** The two contradictory guard combinations are refused at
   construction. **Acceptance example:** a port with `LoopGuard` and
   `RestrictedRole` is rejected with the field path `ports.1/1/1.loop_guard`,
   and so is a port with `LoopGuard` and `AdminEdge`.
4. **R15d:** Loop guard is inactive where the vendors exclude it.
   **Acceptance example:** an operationally edge port and a port whose
   point-to-point mode is shared both keep today's behavior when their
   information expires, with no `loop-inconsistent` state.
5. **R15e:** The blocking reason reaches the trace.
   **Acceptance example:** the port fact for a `bpdu-guard` port carries the
   reason, so a corpus case can bind a fact to the step that disabled it,
   which `ValidateCase` requires of every case
   (`src/common/netsim/internal/netsimtest/corpus.go:565`).
6. **Rgate:** The gate answers for a port and a VLAN, the ingress path
   consults it with the classified VID, and the three observables that move
   with the reorder are asserted.
   **Acceptance example:** `Forwards("1/1/1", 10)` and `Forwards("1/1/1", 20)`
   agree in this phase, because one tree answers both. A frame on a blocked
   port that classifies cleanly is dropped as `port-blocked` with its
   classified FID set and the classification step ahead of the gate step. A
   frame on a blocked port that also fails ingress filtering reports
   `ReasonIngressFilter`, not `ReasonPortBlocked`.
7. **R9:** The four guard fields are covered by validation, normalization,
   `Clone`, the canonical port fact, and `Diff`.
   **Acceptance example:** enabling `LoopGuard` on one port gives exactly one
   change on field `loop_guard`, and two configurations differing only in
   guard fields have different canonical port facts.
8. **R39:** The corpus admits three cases, each naming its false answer:
   - `troubleshooting/stale-root-ages-out`: the port stays blocked by a root
     that no longer exists.
   - `troubleshooting/bpdu-guard-disables-edge`: the edge port keeps
     forwarding after an unexpected BPDU.
   - `troubleshooting/loop-guard-unidirectional-link`: the port whose BPDUs
     stopped becomes Designated and opens a loop.

## Out of scope

- MSTP in every part: regions, the configuration digest, CIST and MSTI
  vectors, boundary roles, hop-count aging, and per-instance topology change.
  Phase 3d, which also replaces the CIST-prefix reading with real MST
  decoding.
- Per-VLAN RSTP: the SSTP encapsulation, per-VLAN trees, the PVID check, and
  the `stp-pvst-boundary` condition. Phase 3e.
- Per-VLAN or per-instance FDB flushing. `stp.Effects.Flush` stays a port list
  and `Bridge.FlushPorts` keeps flushing a port across every FID
  (`src/common/netsim/vswitch/bridge/bridge.go:156`). With one tree the two are
  the same thing; phase 3d needs the distinction and adds it.
- Tagged BPDU emission. `Encode` keeps emitting untagged LLC frames
  (`src/common/netsim/vswitch/stp/bpdu.go:293`) and `applySTPEffects` keeps
  adding no tag (`src/common/netsim/vswitch/switch.go:1901`). Phase 3e needs
  both.
- netmodel loading of guards or of any protocol version other than RSTP.
  `netmodel.go:1161` keeps refusing anything else as unsupported, and no
  schema field carries a guard
  (`spec/proto/flowseer/net/protocol/stp/v1/`). Guards reach the layer through
  `vswitch.ConstructionSpec` only.
- Automatic guard recovery timers, and BPDU filter.
- Retention of per-tree state across `Derive`, which is phase 5.

## Units

### U1. Guard configuration

Files: `src/common/netsim/vswitch/stp/config.go`, `diff.go`, `config_test.go`
After: none
Change: `Port` gains `BPDUGuard`, `RestrictedRole`, `RestrictedTCN`, and
`LoopGuard`, all `bool`. `Validate` refuses `LoopGuard` beside
`RestrictedRole` and beside `AdminEdge`, with the field path of the offending
port in the file's existing form. `Normalize`, `Clone`, `Port.Canonical`, and
`Diff` carry the four fields, with one `trace.Change` per field.
Tests: the R15c and R9 examples; a port with each guard alone is accepted;
`Normalize`'s output is unchanged for a configuration that sets no guard.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/stp`

### U2. Tree-keyed layer state

Files: `src/common/netsim/vswitch/stp/layer.go`, `tree.go` (new), `fact.go`,
`layer_test.go`
After: none
Change: a `tree` struct holds the per-tree state the Decisions list, and
`Layer` holds trees keyed by a tree ID plus a VID-to-tree map, both with
exactly one entry. The port identifier and the port key set and iteration
order stay bridge-global. The internal functions the survey names
(`recompute`, `Receive`, `Wake`, `LinkChange`, `emit`, `makeBPDU`,
`designatedOrBlocked`) take or resolve a tree; the exported accessors keep
their signatures and resolve the single tree internally.
Tests: the landed RSTP suite passes unchanged, which is what makes this unit a
refactor; one new test asserts the VID-to-tree map answers for every VID; one
asserts `Effects.Flush` keeps its order for a topology change that flushes
several ports.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/stp`

### U3. Message-age aging and the guards

Files: `src/common/netsim/vswitch/stp/layer.go`, `fact.go`, `layer_test.go`
After: U1, U2
Change: `Receive` accepts information only while `MessageAge + 1 <= MaxAge`
against the received BPDU's `MaxAge`, and discards it otherwise; accepted
information keeps its `3 × HelloTime` lifetime. The four guards apply as the
Decisions describe. `PortInfo` carries the blocking reason, one of
`bpdu-guard` and `loop-inconsistent` or empty, and `portInfoSnapshot`
(`fact.go:67`) carries it into the trace. Loop guard is inactive on an
operationally edge port and on a port that is not point-to-point.
Tests: the R15a, R15b, R15d, and R15e examples; each guard state clears the
way the Decisions say; `RestrictedTCN` leaves the other ports' entries in
place; a BPDU whose message age is one below max age is still accepted.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/stp`

### U4. The VLAN-aware gate

Files: `src/common/netsim/vswitch/stp/layer.go`, `layer_test.go`,
`src/common/netsim/vswitch/bridge/bridge.go`, `bridge_test.go`,
`src/common/netsim/vswitch/switch.go`, `switch_test.go`,
`src/common/netsim/vswitch/derive.go`, `derive_test.go`,
`src/common/netsim/vswitch/README.md`
After: U3
Change: `bridge.Gate` becomes `Learns(port string, vid vlan.ID) bool` and
`Forwards(port string, vid vlan.ID) bool`, with `semanticGate.ForwardingFact`
and `b.gateFact` (`bridge.go:1251`) taking the VID with them. VLAN
classification moves ahead of the ingress gate in `Bridge.Ingress`, with the
reserved-address drop still ahead of both, so a classification failure now
outranks `port-blocked` and a gate-blocked frame carries its classified FID.
An `stpGate` adapter in `vswitch` resolves the VID to a tree and calls the
layer, replacing the direct installation at `switch.go:285` and
`derive.go:47`, in the shape `lagSelector` already uses. The multicast
retention predicate at `derive.go:98` passes the `vid` it already has. The
egress call sites pass the FID they already hold (`bridge.go:944`, `:1147`).
Tests: the Rgate example, including the step order, the FID, and the reason
precedence; `testGate` and the four bridge gate tests move to the new
signature (`bridge_test.go:1346`, `:1367`, `:1393`, `:1420`, `:2803`); the
deferred-learning callers at `switch.go:816` and `:894` keep their behavior;
`CaseTopologyShadowingUnknownUplinkSTP` keeps its metadata.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch`

### U5. Corpus and documentation

Files: `src/common/netsim/internal/netsimtest/cases.go`, `corpus_test.go`,
`README.md`, `src/common/netsim/vswitch/stp/README.md` (new),
`src/common/netsim/README.md`,
`docs/architecture/2026-09-10-virtual-device-direction.md`,
`docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md`
After: U3, U4
Change: the three R39 cases are registered in a `RegisterSTPCases` function
beside the landed registrars, with the case count at `corpus_test.go:579` and
the per-use-case lists updated. The new `stp` README states the aging rule
with its source, each guard and what clears it, the two refused combinations,
that the gate answers per VLAN and today has one tree, and that a version 3
BPDU is read as its RST prefix until phase 3d. The direction record gains a
spanning-tree subsection. In the parent plan, R14 and R15 gain the lettered
sub-requirements the three phase plans claim, so that the parent's own
"every parent requirement is claimed by one re-planned phase" check can be
run.
Tests: corpus admission, the count, and deterministic re-execution.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim docs/architecture/2026-09-10-virtual-device-direction.md docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md`

Waves: U1 U2 | U3 | U4 | U5.

U3 and U4 are serial because both edit `src/common/netsim/vswitch/stp/layer.go`,
not because one reads better after the other.

## Verification

```bash
go test -race ./src/common/netsim/...
go vet ./src/common/netsim/...
.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim \
  docs/architecture/2026-09-10-virtual-device-direction.md \
  docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-phase3b-plan.md \
  docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
```

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] Every requirement example has a named test.
- [ ] The landed RSTP suite passes with no behavior change from U2.
- [ ] The new `stp` README, the vswitch and netsim READMEs, and the direction
      record updated in the same change.
- [ ] This plan's `status` set with an outcome note; the parent's phase 3b
      `Landed:` line filled; no plan labels in code.

## Open questions

Empty. The reorder's three consequences are decided above, and the one that
could still block the phase is the stop condition rather than a question.
