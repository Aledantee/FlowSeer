---
title: Network Simulation Analysis Completeness, Phase 3c - Plan
type: feat
date: 2026-09-14
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
parent: docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
---

# Network simulation analysis completeness, phase 3c: Loop protection without spanning tree - Plan

> Re-planned by plan when its turn comes; the tree will have moved. The
> Decisions and Requirements are settled; the Units are a draft to check
> against the switch integration that phases 3 and 3b land.

## Goal

A switch configured with loop protection detects a forwarding loop that no
spanning tree breaks, such as two access ports joined through an unmanaged
switch. It detects the loop by sending probe frames and hearing its own
probe come back. It then blocks, stops learning on, or disables the port,
and recovers under one of three recovery modes. The means are a capability package
`vswitch/loopprotect` with a probe codec, per-port timers on the logical
clock, and a port action that the switch gate enforces.

Stop condition: the plan is wrong if a probe cannot return to its sender
through netsim's bridges, because they drop or consume it before the loop
closes.

This phase claims the parent's R41 and extends R9, R37, and R39.

## Decisions

- **The parent's Decisions govern, as amended on 2026-09-14.** User-directed
  2026-09-14: primitive loop protection outside spanning tree.
- **The design is netsim's own, drawn from vendor loop detection.**
  - [H3C loop detection](https://www.h3c.com/en/d_201906/1192978_294551_0.htm)
    sends detection frames to a multicast address and treats a returned
    frame as a loop. A frame that returns with a different VLAN tag is an
    inter-VLAN loop. Its actions are block, no-learning, and shutdown. Block
    and no-learning restore the port after three detection intervals without
    a returned frame. The default interval is 30 s.
  - HPE Aruba loop-protect and Huawei loopback-detect follow the same
    pattern: a multicast probe, a disable action with a re-enable timer, and
    per-VLAN probes. Their pages returned 403 or empty when fetched, so no
    detail rests on them.
  - Each vendor's frame format is proprietary, so netsim does not parse
    vendor probes.
- **Probe frame.**
  - Destination is `03:46:53:4c:50:00`, a locally administered group
    address. It floods as unregistered multicast and is outside the IEEE
    reserved range that netsim bridges drop
    (`src/common/netsim/vswitch/bridge/bridge.go:366`,
    `src/common/net/ethernet/ethernet.go:197`).
  - EtherType is `0x88b5`, IEEE Std 802 Local Experimental EtherType 1.
  - The source is the switch MAC. The payload is a version octet, the
    originating switch MAC, the sending port name (length-prefixed), the VID
    it was sent on, and a 32-bit sequence number.
  - A probe goes out untagged, or tagged per configured VLAN, on every
    enabled port whose operational state is up and whose gate forwards for
    that VLAN.
- **Detection.** An intercepted probe whose originating MAC is this switch's
  is a loop on the sending port. The received VID is recorded, and a
  mismatch with the sent VID is marked inter-VLAN. The switch consumes its
  own probe and never re-floods it. A foreign switch's probe is ordinary
  multicast data and floods, which lets the loop close through switches
  without loop protection.
- **Action on the sending port.** It is per port and matches Aruba's default
  target.
  - `Block`: inbound frames drop and learning stops. Probes still go out, so
    a persisting loop keeps the port blocked.
  - `NoLearn`: learning stops and forwarding continues.
  - `Disable`: the port neither sends nor receives, and no probes go out.
- **Recovery modes.** Three modes cover every vendor behavior found.
  User-directed 2026-09-14. Each port sets `Recovery.Mode`, plus
  `Recovery.Duration` where the mode uses one.
  - `Manual`: the action holds until one of three things clears it:
    - `Switch.ClearLoopProtect(port)`;
    - a port cycle, which is the port's admin status going down and then up
      through `Derive`, or its link reported down and then up through
      `LinkChange`, the way Cisco's `shutdown`/`no shutdown` re-enables an
      errdisabled port (user-directed 2026-09-14);
    - a configuration change that removes the port's loop protection or
      changes it.

    Sources:
    - Cisco: errdisable recovery is off by default; manual recovery is
      `shutdown` then `no shutdown`.
    - Juniper: the action holds until `clear loop-detect enhanced interface`
      unless a revert interval is set.
    - TP-Link: `recovery-mode manual` releases only with
      `loopback-detection recover`.
    - Extreme ELRP: `permanent`.
    - MikroTik: `loop-protect-disable-time` 0 means forever.
  - `Timer`: the action lifts `Duration` after it was applied, whether or not
    the loop is gone. Detection restarts, and a persisting loop reapplies the
    action at the next returned probe. Sources:
    - Cisco: errdisable recovery retries after 300 s by default, and the port
      returns to errdisabled if the cause persists.
    - MikroTik: disable time defaults to 5 min, and expiry resets loop
      protection.
    - Extreme ELRP: `duration`.
    - TP-Link: `recovery-mode auto`, whose recovery time is a whole number of
      detection intervals and defaults to 3.
  - `LoopCleared`: the action lifts after `Duration` passes with no returned
    probe. Every returned probe restarts the wait. Sources:
    - H3C: block and no-learning restore after three detection intervals
      without a loop frame.
    - Juniper: the revert interval runs after the loop condition is repaired.
  The recovery sources, each fetched on 2026-09-14:
  [Cisco errdisable recovery](https://www.cisco.com/c/en/us/support/docs/lan-switching/spanning-tree-protocol/69980-errdisable-recovery.html),
  [Juniper `clear loop-detect enhanced interface`](https://www.juniper.net/documentation/us/en/software/junos/cli-reference/topics/ref/command/clear-loop-detect-enhanced-interface.html),
  [MikroTik Loop Protect](https://manual.mikrotik.com/docs/bridging-and-switching/user-guides/loop-protect/),
  [Extreme ELRP port shutdown](https://documentation.extremenetworks.com/release_notes/ExtremeXOS/16.1.2/EXOS_Release_Notes/16.1.2/c_elrp-port-shutdown.shtml),
  [TP-Link configuration guide](https://static.tp-link.com/res/down/doc/Port_Configuration_Guide.pdf?configurationId=2978),
  and the H3C page above.
- **Recovery defaults and limits.**
  - A zero `Duration` means three probe intervals, the H3C and TP-Link
    default.
  - `Block` and `NoLearn` default to `LoopCleared`. `Disable` defaults to
    `Manual`, matching Cisco's recovery-off default.
  - `LoopCleared` with `Disable` is rejected. A disabled port sends no probes,
    so it can never observe the loop clearing.
  - A `Timer` recovery that re-detects the loop counts the recurrence in
    `PortInfo`. It adds no backoff: none of the fetched sources describes one.
- **Timers.** `Interval` zero means 5 s. That is netsim's default, shorter
  than H3C's so tests converge quickly, and every vendor makes it
  configurable. `NextWake` reports the next probe and recovery times, and the
  fabric drives them as it drives STP and LAG.
- **Independent of spanning tree.** Probes use the switch gate, so an STP
  Discarding port neither sends nor receives them and cannot report a loop
  that STP already broke. Both may be configured on one switch.
- **Outcomes are port state, not issues.** A protected port is a definite
  fact with trace steps `loopprotect.probe.return`, `loopprotect.port.block`,
  and `loopprotect.port.recover`. A journey dropped by a protected port
  cites the block step.
- **No netmodel mapping.** No schema reports loop protection.

## Requirements

1. **R41a:** A returned probe triggers the configured action on the sending
   port.
   **Acceptance example:** `sw1` enables `Block` on ports `1` and `2`. `sw2`
   has no loop protection. Two cables join `sw1:1`-`sw2:1` and
   `sw1:2`-`sw2:2`, and no STP runs. After one probe interval, the port of
   the first probe to return is blocked. A broadcast injected afterwards
   reaches each host once, where without loop protection it loops.
2. **R41b:** Each recovery mode follows its rule.
   **Acceptance example:** interval 5 s, `Duration` 15 s, action applied at
   t.
   - `LoopCleared` with the loop still cabled stays blocked, because probes
     keep returning. A cable fault just before the probe at t+20 s restores the port at
     t+30 s, 15 s after the last returned probe at t+15 s.
   - `Timer` with the loop still cabled lifts at t+15 s. The next returned
     probe reapplies the action, and the recurrence count reads 1.
   - `Manual` stays applied at t+1 h. It lifts on `ClearLoopProtect`, and
     separately on a `LinkChange` down then up; an up report alone leaves it
     applied.
3. **R41c:** An STP-blocked port reports no loop.
   **Acceptance example:** the R41a fabric with RSTP on both switches
   detects nothing, and the tree's Alternate port stays the only block.
4. **R41d:** A probe that returns on another VLAN is an inter-VLAN loop.
   **Acceptance example:** `sw2` bridges VLAN 10 to VLAN 20 through a cable
   between an access port in each. A probe sent on VID 10 returns tagged 20,
   and the trace marks it inter-VLAN.
5. **R9:** The `LoopProtect` config (`Ports`, `Interval`, `Action`,
   `Recovery.Mode`, `Recovery.Duration`, `VLANs`) is covered by validation, normalization, `Clone`, and
   `Diff`.
   Validation rejects unknown ports, LAG members (the LAG port carries the
   setting), negative durations, unknown actions and recovery modes, and `LoopCleared`
   with `Disable`.
   **Acceptance example:** changing `Action` from `Block` to `Disable` gives
   one change.
6. **R39:** The corpus admits
   `troubleshooting/loop-protect-contains-access-loop`, whose false answer is
   an unbounded broadcast loop.

## Out of scope

- Vendor probe formats, SNMP traps, and log actions.
- Loop detection through a VLAN the switch does not carry.
- Probes on a LAG's member ports. A LAG sends on its selected member.

## Units

### U1. `loopprotect` package

Files: `src/common/netsim/vswitch/loopprotect/` (new: config, codec, layer,
facts, diff, tests, `README.md`)
After: none
Change: config validation and `Diff`; `Encode`/`Decode` for the probe; a
layer with per-port probe timers, the three recovery modes, a manual clear, `Receive`, `Wake`,
`NextWake`, `Clone`, and port action queries.
Tests: codec round trip and malformed payloads; R41b per mode and R41d at
the layer level; recovery defaults per action; sequence and timer determinism.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/loopprotect`

### U2. Switch and fabric integration

Files: `src/common/netsim/vswitch/switch.go`, `config.go`, `diff.go`,
`derive.go`, their tests, `src/common/netsim/fabric/` tests,
`src/common/netsim/vswitch/README.md`
After: U1
Change: `vswitch.Config.LoopProtect`; `Switch.ClearLoopProtect`; probe
interception before the bridge;
the port action enforced at ingress, learning, and egress; probe emissions
and wakes; `Derive` keeps the layer only when its `Diff` is empty (phase 5
refines this).
Tests: R41a and R41c through fabrics, R9 through `vswitch.Diff`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch src/common/netsim/fabric`

### U3. Corpus and documentation

Files: `src/common/netsim/internal/netsimtest/cases.go`, `corpus_test.go`,
`README.md`, `docs/architecture/2026-09-10-virtual-device-direction.md`,
`src/common/netsim/README.md`
After: U2
Change: register the R39 case; the direction record states loop protection
as netsim's own mechanism and what it does not emulate.
Tests: corpus admission and deterministic re-execution.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim docs/architecture/2026-09-10-virtual-device-direction.md`

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
- [ ] R41 examples pass, including loop containment in a fabric without STP.
- [ ] Package README, the vswitch and netsim READMEs, and the direction
      record updated in the same change.
- [ ] This plan's `status` set with an outcome note; the parent's phase 3c
      `Landed:` line filled; no plan labels in code.
