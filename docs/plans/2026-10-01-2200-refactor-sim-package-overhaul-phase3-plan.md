---
title: Spanning Tree to Standard - Plan
type: fix
date: 2026-10-01
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-plan.md
---

# Spanning Tree to Standard - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

`layer/stp` follows IEEE 802.1D and IEEE 802.1Q for RSTP, MSTP, and
interoperation with legacy STP, and keeps its PVST and SSTP behaviour
consistent with them. Stop condition: if the standard's port state machines
cannot be expressed over the layer's current per-tree port record, the phase
starts with the restructure in Inventory, Design, and re-plans the fixes on
top of it.

## Decisions

- The parent's Decisions apply. Protocols are implemented to their standards.
- Link state has one owner per port and tree state one owner per tree.
  Why: the link flags are copied into every tree (`stp/layer.go:166`) and
  repaired after the fact (`:1220`), which is the cause of Correctness 2, 3,
  and 11.
- Transitions are computed before any frame is built. Why: Correctness 13
  transmits information the same wake then expires.
- The edition each clause is read from is decided at re-planning and written
  into `stp/README.md`. PVST and SSTP have no IEEE standard. Their source is
  named there too, or the behaviour is marked as modelled on observation.

## Requirements

R1. Every Correctness entry below has a test that fails before its fix.
Example: a PVST Designated port that earned an agreement, then loses and
regains link, stays Discarding until the new peer agrees.

R2. MSTI ports run proposal and agreement. Example: two bridges in one
region on a point-to-point link bring an MSTI Designated port to Forwarding
in the same exchange that brings the CIST there.

R3. A topology change reaches the root. Example: a settled non-root bridge
whose forwarding downstream port goes down transmits a BPDU with the
topology-change flag on its Root port within one hello time.

R4. Legacy STP interoperation acknowledges a TCN and uses the legacy
topology-change timer. Example: a TCN BPDU received on a Designated port is
answered by a Configuration BPDU with the acknowledgment flag set.

R5. BPDU encoding is checked against bytes from a second source, including
an MST BPDU with at least one MSTI record whose bridge and port priority are
not the defaults.

## Inventory

Every statement below about what the standard requires is unverified until
this phase is re-planned with the clauses cited. The code citations hold at
`61775c73`.

Paths are as of commit `61775c73`. `S` is
`src/common/netsim/vswitch/stp`, which phase 1 moves to
`src/common/sim/layer/stp`. No entry has a test in the tree.

### Correctness

1. High. MSTI bridge priority may use the wrong nibble. `S/layer.go:1336`
   shifts the priority by 12 before the record and `:1379` shifts the
   received octet back, so `0x8001` is emitted as `0x08`. The codec keeps
   the octet as given (`S/bpdu.go:514,769`). Which nibble the standard
   assigns is unverified. The known-bytes fixture of R5 settles it.
2. High. An agreement survives a link bounce on a non-CIST tree.
   `S/layer.go:1220` clears role, state, and received information on link
   down and leaves `agreed`. The CIST clears it (`:1872`). The port forwards
   at link up through `:1791`.
3. High. Losing auto-edge leaves other trees Forwarding. `S/layer.go:2049`
   returns the CIST to Discarding and `:2063` copies only link flags to the
   other trees. `:1794` keeps a Designated port that is already Forwarding.
4. High. Any agreement opens a Designated port. `S/layer.go:2375` checks
   neither point-to-point status nor the sender's role and priority vector.
5. High. A topology change is not sent toward the root. `S/layer.go:1132`
   flushes locally and schedules nothing, and the hello loop (`:2507`) sends
   on Designated ports only.
6. High. Loop guard watches the CIST only (`S/layer.go:2561`). A port that
   is Root for a PVST VLAN other than 1 becomes Designated when that tree's
   information expires. `S/README.md:125` promises the guard port-wide.
7. High. A rejected BPDU rewrites how the kept vector is read.
   `S/layer.go:2341` sets `external` before acceptance, and
   `candidateVector` (`:1513`) then reads fields the kept vector never set.
8. High. Two priorities share a retention key. `S/mst.go:44` omits
   `PriorityPresent` and substitutes 128, while the runtime inherits the
   bridge port's priority (`S/layer.go:589`).
9. High. Path-cost addition wraps (`S/layer.go:1511,1516,1519`). Costs 100
   and `0xfffffff0` over a 20,000 link elect the second path.
10. Medium. Forward delay is scheduled from the local timer
    (`S/layer.go:1784,1799,2532`) while `:742` reports the root's timer as
    the one in force. Hello scheduling does the same (`:1240,2504`).
11. Medium. An SSTP outcome clears loop guard and never recomputes roles
    (`S/layer.go:2209,2221,2225`). The port stays Alternate.
12. Medium. A speed-only update restarts the CIST handshake
    (`S/layer.go:1931`), against the contract at `:1926`.
13. Medium. A wake sends hellos (`S/layer.go:2472`) before expiring received
    information (`:2553`).
14. Medium. The MST record limit is the 16-bit length's capacity, 4,091
    (`S/bpdu.go:355`, `S/mst.go:252`). The standard's limit on instances is
    lower. The exact figure is unverified here and is cited at re-planning.

### Completeness

- MSTI BPDU records carry no Proposal or Agreement (`S/layer.go:1324,1357`),
  so an MSTI converges at timer speed. `S/README.md:422` does not list this.
- A received TCN is not acknowledged (`S/layer.go:2100`).
  `BPDU.TopologyChangeAck` and its setter (`S/bpdu.go:297,302`) have no
  runtime caller. Every topology-change timer is HelloTime plus one second
  (`S/layer.go:1135`), legacy links included.
- `BPDUDecisionFact` (`S/fact.go:68`) drops the configuration identifier,
  regional root, internal cost, remaining hops, and every MSTI record, so two
  BPDUs that differ only there compare equal.
- The switch records only CIST before and after on a BPDU
  (`src/common/netsim/vswitch/switch.go:2673`). An MSTI-only transition is
  invisible to comparison. Phase 9 owns the switch side.

### Design

- `S/layer.go` is 2,633 lines covering construction, observation, link
  state, election, receive, scheduling, and encoding. Split it along those
  lines once link state has one owner.
- Priority inheritance is resolved in two places (`S/mst.go:53`,
  `S/pvst.go:14`) and `Diff` reports equal From and To when only presence
  changed (`S/diff.go:419`). Resolve once at construction.
- `PortInfo.MSTID` carries a VLAN for a PVST tree (`S/layer.go:66,769`).
  Report a tree identity that tells an MSTI from a VLAN.

### Tests

- `S/link_state_internal_test.go:98,414` requires `agreed` to survive link
  down, which pins Correctness 2.
- `S/layer_test.go:2608-2613` overwrites the previous counter before
  comparing, so the assertion cannot fail.
- `S/layer_test.go:1528,1628,1640` lets MST convergence finish without
  checking MSTI forwarding state.
- `S/layer_test.go:3064` fixes VLAN 1's path cost, so the speed-change test
  takes only the preserving branch.
- `S/layer_test.go:2544` checks that an SSTP outcome clears the block reason
  and never that the role recovers.
- `S/bpdu_test.go:717` hands the codec a correctly placed priority octet,
  so the runtime's shift is never exercised. `:1019` mirrors the record
  limit of Correctness 14.

## Open questions

- Which edition is the reference: the README names its sources, and no IEEE
  text is vendored under `spec/`. Fetch, cite clause numbers, and say
  "unverified" where a clause cannot be read.
- `netmodel` rejects every reported protocol other than RSTP
  (`src/common/netsim/vswitch/netmodel/netmodel.go:1278`). Whether phase 11
  loads MSTP and PVST depends on what the network model's STP schema
  carries.
- Does the fabric replace the switch's point-to-point default
  (`src/common/netsim/vswitch/switch.go:2960`) before any BPDU is sent on an
  unresolved link? Phase 10 answers it.
- Parked by drive: the re-plan (`848d934e` on `parked/sim-p3-replan`, which
  holds the re-planned units and their fetched clauses) found that R3's
  example cannot be met as written. Its trigger is a forwarding port going
  down, and the sources the re-plan read (IEEE Std 802.1Q-2003 clauses 13
  and 14, P802.1aq/D1.5) raise no topology change for that. Options:
  replace R3's example with a port that starts forwarding, as U5 and
  Correctness 15 already prove (follows the parent's decision that
  protocols follow their standards, and changes a fixed requirement's
  example) | keep the example as a stated departure from the standard, a
  change raised on leaving Forwarding at the four sites of Correctness 15
  with that entry's test inverted (keeps R3 as written, and the layer then
  departs from the standard there). Recommended: replace the example,
  because the parent decided that protocols follow their standards.
