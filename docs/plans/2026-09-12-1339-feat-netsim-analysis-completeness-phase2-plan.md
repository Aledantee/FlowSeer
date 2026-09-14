---
title: Network Simulation Analysis Completeness, Phase 2 - Plan
type: feat
date: 2026-09-13
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
parent: docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
---

# Network simulation analysis completeness, phase 2: Physical and topology uncertainty - Plan

## Goal

A fabric built from partial physical and topology facts gives definite
results where the facts decide them. Where they don't, it gives scoped
`Incomplete` or `Unsupported` metadata. An unreported fact no longer becomes
a zero reach, a 1 Gb/s full-duplex link, a known-down port, or "no device".
The unreported fact can be a medium, a capability, an adjacency, or a powered
device. A frame that reaches a host is delivered only when the host accepts
it. The means are tri-state facts in `phy`, link-state input to the protocol
layers in `vswitch`, and reach and topology states in `fabric`. `fabric` also
gains dependency-captured journey metadata and host acceptance.

Stop condition: the dependency design is wrong if a result that could change
with an unknown fact reads `Complete`. Two cases trigger it. One is a
forwarding path whose only reached ports are known. The other is an unknown
link that alters spanning-tree or aggregation state. Either one returns the
plan to `plan`.

This phase claims parent R8 and R10-R13 and extends R2, R7, R9, and R39.

## Decisions

- **Parent and phase 1 contracts govern.** The parent's Decisions apply as
  written. Phase 1 contracts are used as landed
  (`src/common/netsim/analysis/metadata.go:39`,
  `src/common/netsim/vswitch/bridge/result.go:97`).
- **An omitted port is unresolved.** A non-LAG switch port with no cable is
  unresolved: operational `Unknown`, with an `Incomplete` issue
  `adjacency-unresolved` on its port. Known absence is stated in
  `fabric.Config.Uncabled` and stays `Down`. Why: parent R8. User-directed
  2026-09-13.
- **Unreported physical facts make the link `Unknown`.** A caller may opt in
  to `fabric.Config.PhyAssumption`, which holds a medium and an Ethernet
  profile.
  - It fills only the missing facts of an end or cable: empty speeds, unknown
    capability, a nil `Setting`, or an unspecified medium.
  - Each affected link gets one `analysis.Assumption` that lists the filled
    facts.
  - Filled values appear in `Links()`. `Config()`, `Spec()`, and `Diff` show
    only the `PhyAssumption` field.
  - Why: a standards default may run only as a recorded assumption.
    User-directed 2026-09-13.
- **Cable length 0 is a stated fact.** A `LengthMeters` of 0 means a stated
  0 m cable. Why: nothing loads cables from discovery yet, so every length is
  caller-stated. Revisit when links load from LLDP.
- **Package ownership.**
  - `phy` owns the negotiation and PoE tables and their reasons.
  - `vswitch` owns how an unknown link reaches STP and LAG.
  - `fabric` owns reach, topology state, link issues, conflicts, host
    acceptance, and journey metadata.
  - `netmodel` maps source facts to the new tri-states.
  - Why: the parent's ownership table. `Host` and `Medium` are already in
    `fabric` (`src/common/netsim/fabric/config.go:129`, `medium.go:11`).
- **`Switch.LinkChange` takes `port.LinkState`, not `up bool`.** An `Unknown`
  link leaves the port `Unknown`. STP and LAG still hear it as not
  operational. The switch then adds an `Incomplete` issue
  `protocol-link-unknown` to every port whose role, state, or membership that
  protocol computes: every STP port of the switch, or the ports of the
  affected LAG. The same happens when an STP port's point-to-point status
  rests on an unknown duplex. Forwarding metadata includes these issues for
  results that consult those ports. Why: `LinkChange` today overwrites the
  port to `Down` (`src/common/netsim/vswitch/switch.go:1935`). An unknown
  uplink would then silently re-elect the root with every journey reading
  `Complete`.
- **Duplex matters only through RSTP point-to-point.** Point-to-point uses
  the local end's duplex (`src/common/netsim/fabric/fabric.go:814`).
  Otherwise an unknown or mismatched duplex is recorded on the link and emits
  no issue.
- **PoE uncertainty stays out of forwarding.** It lives in a switch power
  result scoped to `FieldScope(NodeScope(node), "poe", port)`, not in
  construction or forwarding metadata. Why: PoE does not gate link state
  here. A port-descendant issue would overlap every result on that port
  (`src/common/netsim/analysis/scope.go:146`).
- **Journey metadata is evaluated over the whole analysis.** It holds only
  the journey's dependencies, captured when each entry is appended.
  - Dependencies are each entry's port and cable, every hop result's
    `ConsultedScopes()` and `ConsultedPorts()`, and each dependent port's
    cable mapped to its `LinkScope`.
  - The metadata keeps the hop results' issues and the fabric issues that
    overlap a dependency at append time.
  - Delivery and host-injection entries gain `Port` and `Cable`.
  - Why: a port issue does not overlap `JourneyScope`
    (`src/common/netsim/analysis/metadata.go:39`). A later `SetFault` must not
    rewrite a past journey (`src/common/netsim/fabric/fabric.go:735`).
- **A valid host injection on a link that isn't up records a journey.** On a
  `Down` link it records a drop with the link reason. On an `Unknown` link it
  records an `EntryUnresolved`. Why: a valid input must not be an error
  (`src/common/netsim/fabric/run.go:209`).
- **Observed state that disagrees with the derived state is kept.** A
  configured port `OperStatus` of `Up` or `Down` that differs from the
  topology-derived state adds an `Incomplete` issue `oper-status-conflict` on
  the port, and the derived value executes.
  - Zero and `Unknown` are not observations.
  - `Config()` and `Spec()` return the configured value. Effective state is
    read from `Switch(name).Ports()` and `Links()`.
  - Why: the parent says to preserve the observation, but
    `src/common/netsim/fabric/fabric.go:397` overwrites it today.
- **Fabric evidence.** `fabric.ConstructionSpec` gains an
  `analysis.EvidenceCatalog`, and cables and `Uncabled` entries carry
  references into it. Issues cite the references of the facts they rest on.
  Evidence references are not behavior and stay out of `Diff`.
- **The plan stays one phase.** Its units form one dependency cluster: `phy`
  types feed `vswitch` and `netmodel`, and both feed `fabric`. The truth
  tables are what stretch the length, and they are the decisions an
  implementer would otherwise re-derive.
- **Shapes that changed while landing.** The code settled five points the plan
  left open or had wrong.
  - `Medium.Reach(lengthMeters, speedBPS)` returns the state and meters,
    because a state cannot say in range or exceeded without a length.
  - A resolved link with an unspecified medium, no `Delay`, and a nonzero
    length adds `Incomplete` `propagation-unknown`, because its timing has no
    velocity factor.
  - A dead-direction fault with an end whose negotiation mode is unreported is
    `Unknown`, because such an end may be forced.
  - `LinkEnd` carries the `Ethernet` facts the link resolved from, so filled
    assumptions show in `Links()`.
  - A construction spec rejects cable or `Uncabled` evidence references that
    its catalog lacks, as `vswitch` does. A bare `Config` has no catalog.
- **No new physical models.** No downshift, optical budget, or transceiver
  model. An unspecified medium is the unresolved-transceiver case. The
  existing reach rows are kept (`src/common/netsim/fabric/medium.go:54`); a
  missing row is unknown. `normalizedMedium`
  (`src/common/netsim/fabric/diff.go:704`) and the default branch of
  `VelocityFactor` stop mapping an unset medium to twisted pair.

## Truth tables

Every table is symmetric in its two ends.

### Reach

`Medium.Reach(speed)` returns `ReachInRange`, `ReachExceeded`, or
`ReachUnknown`. A stated medium with a row compares the length against it. A
stated medium without a row, or an unspecified medium with no assumption, is
`ReachUnknown`.

The candidates are the speeds negotiation could select: the common known
speeds and forced speeds, at or below `TopSpeedBPS`. Speeds known exceeded
are removed, as today. The link is `Unknown` with `reach-unknown` when the
speed negotiation would pick has `ReachUnknown`, or when a higher remaining
candidate does. The observed rule below still applies. `Delay` sets timing
only and never resolves reach.

### Negotiation (`phy.Negotiate`)

What the ends report:

- Empty `SupportedSpeedsBPS` means unreported.
- `AutoNegotiationSupported` is a `Capability`: `CapabilityUnknown` (zero),
  `CapabilitySupported`, or `CapabilityUnsupported`.
- A nil `Setting` means the mode is unreported.
- A forced setting with speed 0 means the speed is unreported.
- An empty forced `Duplex` normalizes to `Unknown`, no longer `Full`
  (`src/common/netsim/vswitch/phy/phy.go:48`).

`phy.Link` holds `State` (`LinkResolved`, `LinkFailed`, `LinkUnknown`, or
`LinkUnsupported`), `SpeedBPS`, `DuplexA`, `DuplexB`, `Source` (`negotiated`
or `observed`), and `Reason`.

| End A | End B | Result |
| --- | --- | --- |
| mode unreported, or forced speed unreported | any | `LinkUnknown`, `capability-unknown` |
| auto, speeds known | auto, speeds known | highest common speed ≤ top, Full/Full; none: `LinkFailed`, `speed-mismatch` |
| auto, speeds unreported | auto | `LinkUnknown`, `capability-unknown` |
| forced S | forced S | S ≤ top: resolved with each set duplex, stated and different adds `duplex-mismatch`; S > top: `LinkFailed`, `speed-mismatch` |
| forced S | forced T ≠ S | `LinkFailed`, `speed-mismatch` |
| forced S ≤ 100 Mb/s | auto, speeds known | S supported and ≤ top: forced end keeps its duplex, auto end Half; forced Full adds `duplex-mismatch`; else `LinkFailed` |
| forced S ≤ 100 Mb/s | auto, speeds unreported | `LinkUnknown`, `capability-unknown` |
| forced S > 100 Mb/s | auto | `LinkUnsupported`, `forced-against-auto-unmodeled` |

Parallel detection matches speed but can't detect duplex, so the negotiating
end assumes half duplex. 1000BASE-T requires auto-negotiation (IEEE Std 802.3
Clause 28.2.3.1 and Clause 40;
[Autonegotiation](https://en.wikipedia.org/wiki/Autonegotiation), "Function").
Above 100 Mb/s netsim models no forced-against-auto behavior for any medium.

Observed rule: when the table or reach gives `LinkUnknown`, and both ends
carry `Observed` with the same nonzero speed, the link resolves with
`Source: observed` and each end's observed duplex. When the link resolves
some other way and an end's `Observed` speed differs, the port gets an
`Incomplete` issue `observed-speed-conflict`.

### PoE (`phy.Config.Allocate`)

`PsePort.PD` is `PDUnknown` (zero), `PDAbsent`, or `PDAttached`. `PDClass` is
valid only with `PDAttached`. `PortAllocation` holds `State`
(`PowerNoDevice`, `PowerUnknown`, `PowerDenied`, or `PowerDelivered`),
`MinMilliwatts`, `MaxMilliwatts`, and `Denial`.

Each group carries a remainder interval `[min, max]`, starting at
`[budget, budget]`. Subtraction from `min` saturates at 0. Ports are walked in
today's priority-then-name order (`src/common/netsim/vswitch/phy/poe.go:209`).
`P` is the class power of a known class. `D` is the largest class power among
classes ≤ `MaxClass` that fit `Limit`, or 0 when none fit. Because class 0
draws more than classes 1 and 2 (`poe.go:164`), `D` is a maximum and not a
lookup at `MaxClass`.

| Port | PD | Result |
| --- | --- | --- |
| disabled | absent | `PowerNoDevice` 0..0 |
| disabled | attached | `PowerDenied` `disabled` 0..0 |
| disabled | unknown | `PowerUnknown` 0..0 |
| enabled | absent | `PowerNoDevice` 0..0 |
| enabled | attached, class > MaxClass | `PowerDenied` `class-unsupported` |
| enabled | attached, P > Limit | `PowerDenied` `limit` |
| enabled | attached, P ≤ min | `PowerDelivered` P..P; min and max −P |
| enabled | attached, P > max | `PowerDenied` `budget` |
| enabled | attached, otherwise | `PowerUnknown` 0..P; min −P |
| enabled | attached with unknown class, or PD unknown | `PowerUnknown` 0..D; min −D |

## Requirements

1. **R10:** Reach follows its table.
   **Acceptance example:** a 2 m cable with an unspecified medium, between two
   gigabit auto ends that report no observations, makes the link `Unknown`
   with `reach-unknown` on its `LinkScope`. It is not `reach-exceeded`.
2. **R11:** Negotiation follows its table.
   **Acceptance example:** a host with no Ethernet facts, against a gigabit
   auto switch port, leaves both ports `Unknown` and floods nothing onto the
   port. A forced 100 Mb/s Full end against a known auto end resolves
   100 Mb/s, `Full`/`Half`, `duplex-mismatch`.
3. **R12:** PoE follows its table.
   **Acceptance example:** the group budget is 30 W.
   - The Critical port is unknown with MaxClass 4, so it yields
     `PowerUnknown` 0..30000.
   - The Low class 3 port yields `PowerUnknown` 0..15400.
   - The Low class 1 port yields `PowerUnknown` 0..4000.
   - `min` stays at 0.
4. **R13:** Arrival at a host appends `EntryArrival`. It then appends either
   `EntryDelivery`, which carries the `Delivery`, or `EntryRejection` with a
   reason. Each decision carries a `trace.Step` with layer `host`, op
   `trace.OpFilter`, a host rule ID, the host subject, and the facts it read.
   The checks run in this order:
   1. VLAN form. A nil `Host.VLAN` accepts untagged frames and VID 0
      priority-tagged frames. A set VLAN accepts only a C-TAG with that VID.
   2. Destination MAC. The host's own address and broadcast are accepted. A
      group MAC is accepted when any of these holds:
      - `Host.Accept.AllMulticast` is set.
      - `Host.Accept.Multicast` lists it. The entries are MAC addresses.
      - For a host with an IPv4 stack, it is the MAC of 224.0.0.1
        ([RFC 1112 §7.2, §6.4](https://www.rfc-editor.org/rfc/rfc1112.html)).
      - For a host with an IPv6 stack, it is the MAC of ff02::1 or the
        solicited-node group of an own address
        ([RFC 4291 §2.8, §2.7.1](https://www.rfc-editor.org/rfc/rfc4291.html),
        [RFC 2464 §7](https://www.rfc-editor.org/rfc/rfc2464.html)).

      `Host.Accept.Promiscuous` accepts any MAC and skips step 3.
   3. IP. This step applies only to a host with an IP stack. A packet is
      accepted when it is addressed to an own address, to the limited or
      directed broadcast of an own prefix, or to a group whose MAC step 2
      accepted. Non-IP EtherTypes are accepted at step 2. An undecodable IP
      header gives `Unknown` acceptance and an `Incomplete` journey issue.

   **Acceptance example:** a unicast frame to another MAC reaches `h2`.
   `Deliveries` stays empty, and the journey ends `EntryRejection`
   `host-unicast-not-addressed`. With `Promiscuous` set, the frame is
   delivered.
5. **R8:** Each non-LAG switch port is cabled, `Uncabled`, or unresolved.
   `Uncabled` names an existing non-LAG switch port that is not cabled, and
   has no duplicates.
   **Acceptance example:** `sw1:3` is `Uncabled` and `sw1:4` is omitted, each
   with a static FDB entry.
   - The hop out `sw1:3` drops with `port-down`, `Unlinked` reports
     `no-cable`, and the journey is `Complete`.
   - The hop out `sw1:4` carries `adjacency-unresolved` on
     `PortScope("sw1","4")`.
6. **R2/R7:** Issues are scoped, cite evidence, and reach only dependent
   results.
   **Acceptance example:** on one switch, a port configured `Down` that
   negotiates `Up` emits one `oper-status-conflict`. A known-unicast journey
   between two other ports stays `Complete`. A `LinkUnknown` redundant uplink
   gives `protocol-link-unknown` on the STP ports, and a journey forwarded by
   STP state carries it.
7. **R9:** Every added field is covered by validation, normalization,
   `Clone`, `Equal`, `ConstructionSpec` plumbing, and `Diff`, except evidence
   references, which stay out of `Diff`. The added fields are `Capability`,
   `PD`, `Uncabled`, `PhyAssumption`, `Host.Ethernet`, and `Host.Accept`.
   **Acceptance example:** each of these changes gives one field-matrix change
   and a spec that is not `Equal`:
   - `PD` from `PDUnknown` to `PDAbsent`;
   - adding an `Uncabled` entry;
   - setting `Host.Accept.Promiscuous`.
8. **R39:** The corpus admits the partial-topology workflow and the physical
   false answers.
   **Acceptance example:** `topology-shadowing/unresolved-transceiver` keeps
   a `Complete` known-unicast delivery between two resolved hosts. Beside it,
   an `Incomplete` journey runs over a cable with an unspecified medium.

## Out of scope

- Optical budgets, downshift, EEE, FEC, transceiver identity, LLDP or CDP
  adjacency, and netmodel cable production.
- PoE-powered link state, PoE timing, and netmodel mapping of forced speed and
  duplex.
- ARP or ND replies (phase 4), STP instances and guards (phase 3), journey
  terminal states (phase 6), and comparison semantics beyond compiling
  (phase 7).
- A host with no cable. Validation already requires exactly one
  (`src/common/netsim/fabric/config.go:641`).

## Units

U1, U2, and U4 form one handoff checkpoint. U1 changes the meaning of
unreported facts, so fabric tests that state none stay red until U4 moves the
fixtures. Implement them in order and run their verifier together.

### U1. PHY truth tables and compile adaptation

Files: `src/common/netsim/vswitch/phy/` (all files and tests),
`src/common/netsim/vswitch/switch.go` (power result),
`src/common/netsim/vswitch/netmodel/netmodel.go` and `export.go` (types only),
`src/common/netsim/fabric/fabric.go` and `run.go` (types only),
`src/common/netsim/vswitch/README.md`
After: none
Change:
- Add `Capability`, `PDState`, `PowerState`, and `LinkState`, each with a
  safe zero value.
- `Negotiate` and `Allocate` implement the tables.
- Validation rejects auto negotiation with `CapabilityUnsupported`, `PDClass`
  without `PDAttached`, and unknown enum values.
- `Normalize` stops defaulting forced duplex.
- `Diff` covers the new fields.
- `Switch.Power()` returns an allocation and metadata. The metadata holds one
  `poe-demand-unknown` issue per `PowerUnknown` port.
- Callers compile without semantic change beyond what the types force.

Tests: one case per negotiation row, including the observed rule. One case
per PoE row, plus the R12 example and a saturation case. Validation failures
with field paths. Diff field-matrix additions.
Verify: with U2 and U4, below.

### U2. Unknown links in STP and LAG

Files: `src/common/netsim/vswitch/switch.go`,
`src/common/netsim/vswitch/stp/layer.go`,
`src/common/netsim/vswitch/lag/layer.go`, their tests,
`src/common/netsim/fabric/fabric.go` (`LinkChange` callers)
After: U1
Change:
- `Switch.LinkChange` takes `port.LinkState` and keeps `Unknown` on the port.
- It maintains the `protocol-link-unknown` issues from Decisions, including
  the ones for unknown duplex on STP ports.
- Forward metadata includes those issues when a result consults those ports.

Tests:
- An unknown uplink among redundant STP paths gives `Incomplete` for a
  journey forwarded by STP state and `Complete` for a switch without STP.
- An unknown LAG member downgrades only results through that LAG.
- A later `Up` report clears the issue.

Verify: with U1 and U4, below.

### U3. netmodel physical fact mapping

Files: `src/common/netsim/vswitch/netmodel/netmodel.go`, `export.go`,
`trust_test.go`, `netmodel_test.go`, `testdata_icx7150_test.go`, `README.md`
After: U1
Change:
- An absent `auto_negotiation_supported` maps to `CapabilityUnknown`.
- `PoeFacet.status` maps as follows:
  - `DELIVERING_POWER` maps to `PDAttached`, with the class when reported.
  - `SEARCHING` maps to `PDAbsent`, recorded as an assumption. RFC 3621 calls
    every non-listed PSE state searching, so absence is inferred, not
    reported.
  - `DISABLED`, `TEST`, `FAULT`, `OTHER_FAULT`, `UNSPECIFIED`, unrecognized
    values, and an absent status map to `PDUnknown`.
- A class without `DELIVERING_POWER` is skipped with an issue, because the
  class is valid only while power is delivered
  ([RFC 3621 §4, pethPsePortDetectionStatus and pethPsePortPowerClassifications](https://www.rfc-editor.org/rfc/rfc3621.html)).
- Export maps `PowerDelivered` to `DELIVERING_POWER` with the class, `disabled`
  to `DISABLED`, `PowerNoDevice` and the other denials to `SEARCHING` with no
  class, and `PowerUnknown` to `UNSPECIFIED`.

Tests: one load case per status value, for absent capability, and for a class
without delivery; each asserts code, scope, and assumption. Round trips cover
delivered, disabled, no device, and unknown. A budget denial re-imports as
`PDAbsent` with the searching assumption.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/netmodel`

### U4. Fabric reach, topology state, and link metadata

Files: `src/common/netsim/fabric/medium.go`, `config.go`, `fabric.go`,
`link.go`, `derive.go`, `diff.go`, `run.go` (injection), their tests and
every fabric fixture, `src/common/netsim/fabric/README.md`
After: U2, U3
Change:
- `Medium` has an unspecified zero value, and `Reach` returns a state.
- `Config` gains `Uncabled` and `PhyAssumption`, `Host` gains
  `Ethernet phy.Ethernet`, and `ConstructionSpec` gains the evidence catalog.
- `resolveLink` applies the fault, admin, reach, negotiation, and observed
  rules.
- Ports resolve as cabled, `Uncabled`, or unresolved, and conflicts are
  detected.
- `Fabric.Metadata()` returns link and port issues plus assumptions. Link
  issues use `LinkScope`, keyed by the existing injective cable encoding.
- Host injection follows Decisions.
- Fixtures move to a shared test helper that states host Ethernet facts and
  media, or sets `PhyAssumption`, so each test keeps its asserted behavior.

Tests: reach rows; the R10, R11, R8, and R2/R7 examples; one assumption per
affected link; `Uncabled` validation; conflict and non-conflict for zero
`OperStatus`; the R9 matrix for fabric fields; injection onto `Down` and
`Unknown` links.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch src/common/netsim/fabric` (closes the U1, U2, U4 checkpoint)

### U5. Host acceptance and journey metadata

Files: `src/common/netsim/fabric/config.go`, `run.go`, `journey.go`,
`diff.go`, their tests, `src/common/netsim/fabric/README.md`
After: U4
Change:
- `Host.Accept` holds `Promiscuous`, `AllMulticast`, and `Multicast`
  (a list of `netaddr.MAC`).
- Arrival and acceptance follow R13.
- `Journey.Metadata` is captured as in Decisions.
- IP decoding uses the codecs under `src/common/net/ip`.

Tests: one case per R13 clause, including solicited-node, 224.0.0.1, and
directed broadcast. Rejected frames are absent from `Deliveries`. Journey
metadata includes a crossed link's issue and excludes an uncrossed one, and a
later `SetFault` leaves past journey metadata unchanged. The R9 matrix covers
`Host.Accept`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric`

### U6. Corpus cases and direction record

Files: `src/common/netsim/internal/netsimtest/corpus.go`, `cases.go`,
`corpus_test.go`, `README.md`,
`docs/architecture/2026-09-10-virtual-device-direction.md`,
`src/common/netsim/README.md`
After: U3, U5
Change:
- `ExecutionResult` gains a fabric journey and fabric metadata.
- Add these cases:
  - `topology-shadowing/unresolved-transceiver`
  - `topology-shadowing/uncabled-port-definite-drop`
  - `topology-shadowing/unreported-negotiation`, whose false answer is 1 Gb/s
    full duplex
  - `topology-shadowing/unknown-uplink-stp`
  - `troubleshooting/host-rejects-foreign-unicast`
- Fixtures use static FDB entries or known unicast.
- The direction record states, as landed: the effective-state table, the
  assumption knob, protocol link-state input, host acceptance, and journey
  dependency metadata.
- In the record's remaining gaps, only the settled physical and topology
  items are removed. LLDP and CDP resolution, transceiver-dependent speed
  resolution, downshift, and PoE dynamics stay.

Tests: the corpus runner admits every case and re-executes each one
deterministically. Only external test packages import `netsimtest`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim docs/architecture/2026-09-10-virtual-device-direction.md`

## Verification

```bash
go test -race ./src/common/net/... ./src/common/netsim/...
go vet ./src/common/net/... ./src/common/netsim/...
.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim \
  docs/architecture/2026-09-10-virtual-device-direction.md \
  docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-phase2-plan.md \
  docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
```

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] Every truth-table row has a named test; R8 and R10-R13 examples pass.
- [ ] No unreported physical or topology fact yields `Up`, `Down`, delivered
      power, or `Complete` protocol-dependent forwarding without a recorded
      assumption.
- [ ] `Deliveries` holds only accepted frames.
- [ ] Package READMEs, the netsim README, and the direction record updated in
      the same change.
- [ ] This plan's `status` set with an outcome note; the parent's phase 2
      `Landed:` line filled; no plan labels in code.

## Open questions

- Unconfirmed: `SEARCHING` imports as an assumed `PDAbsent`, not `PDUnknown`.
  Recommended because the other choice makes every idle PoE port on a loaded
  switch uncertain. Revisit if a planning question needs an attached PD that
  the switch denied for budget.
