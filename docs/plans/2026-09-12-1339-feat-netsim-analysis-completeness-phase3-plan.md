---
title: Network Simulation Analysis Completeness, Phase 3 - Plan
type: feat
date: 2026-09-14
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
parent: docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
---

# Network simulation analysis completeness, phase 3: LAG and multicast correctness - Plan

> Implemented.

## Goal

A LAG answers "which member carries this flow, and which flows move when a
member fails" the way the Open vSwitch bond it names does: flows on surviving
members stay put, active-backup does not fail back to an invented primary, and
a balance-tcp bond without negotiated LACP carries nothing. Multicast snooping
forwards per source: an `(S,G)` join admits S and not another source, and a
non-fast leave follows the RFC 3376 router state tables and their timers. The
means are a bucket table and active member held as `lag` runtime state, and
per-port IGMPv3/MLDv2 router state in `mcast`.

Stop condition: the plan is wrong if an OVS 3.3 bond selection depends on an
input netsim does not see (packet or byte counts before the first rebalance),
or if RFC 3376 §6.4 state cannot be kept per port without a querier.

This phase claims parent R16-R18 and extends R9, R37, and R39. STP instances
and guards (R14, R15) moved to
`docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-phase3b-plan.md`.

## Decisions

- **The parent's Decisions govern; phases 1 and 2 are used as landed.**
- **Phase 3 splits in two.** LAG and multicast land here, STP in phase 3b.
  Why: the three protocols together exceed six units, and STP alone is six.
  The capability packages are disjoint. The integration files are shared:
  `vswitch/switch.go`, `metadata.go`, `bridge/bridge.go`, and
  `fabric/run.go`. Phase 3b therefore runs after this phase and is re-planned
  against what this phase leaves there. Its Decisions and Requirements,
  decided by the user on 2026-09-14, stand.
- **LAG balance modes follow OVS 3.3 bucket semantics.** User-directed
  2026-09-14.
  - `hash & 0xff` picks one of 256 buckets, as today.
  - A bucket's member is runtime state. On lookup, a bucket with no member or
    a disabled member takes the member at the front of the enabled list, and
    that member moves to the back (`ofproto/bond.c` `choose_output_member`,
    `get_enabled_member`, OVS `branch-3.3` at `73e38c8d`).
  - A newly enabled member joins the back of the enabled list
    (`bond_enable_member`: `ovs_list_insert` before the list head). Members
    enabled in one call join in name order.
  - A bucket keeps its member while that member stays enabled, so a member
    fault moves only its own buckets.
- **Selection commits only on a mutating path.** `Forward` and fabric
  transmission commit bucket assignments and the rotation. `Peek` computes
  the same choice and commits nothing. Why: `Peek` promises no state change.
- **Rebalancing is not modeled and is reported.** `LAG.RebalanceInterval` is
  a `*time.Duration`. Nil normalizes to 10 s, zero disables rebalancing, and
  a nonzero value below 1 s normalizes to 1 s (`vswitchd/bridge.c`
  `bond-rebalance-interval` default 10000; `vswitch.xml` column text). A
  balanced selection whose bucket was assigned at least one interval earlier,
  with two or more enabled members, carries an `Incomplete` issue
  `lag-rebalance-unmodeled` on the aggregator scope. Why: OVS moves buckets by
  measured load, which netsim does not measure.
- **Active-backup keeps the last active member.** `Normalize` no longer
  fills `Primary`. The active member is chosen as `bond_choose_member` does:
  the configured primary if enabled, else the last active member if enabled,
  else the lowest-named enabled member. OVS walks a hash map at the last step;
  the name order is netsim's deterministic stand-in and is documented as such.
- **Balance-tcp requires negotiated LACP.** With `LACP.Mode` `Off`, or with
  no attached partner, `BalanceTCP` selects nothing unless `Fallback`
  applies, and `Fallback` selects as active-backup (`choose_output_member`,
  `BM_TCP` and `LACP_CONFIGURED` branches).
- **LAG convergence is evidence, not status.** `lag.Info` gains `Pending`,
  one entry per member with a running up or down delay, an `Expired`
  partner, or an attached partner without synchronization, each with the
  time it can next change. A pending member does not downgrade a result; the
  answer at time t is definite, and phase 6 owns convergence.
- **Multicast state is RFC 3376 router state kept per port.** For each VLAN,
  group, and port the layer holds a filter mode, a group timer, and source
  records with timers (RFC 3376 §6.2). Reports apply the tables in §6.4.1
  and §6.4.2, group timer expiry applies §6.5, and forwarding applies §6.3.
  MLDv2 uses the same tables (RFC 3810 §7.4-§7.6).
  - IGMPv1/v2 reports are `IS_EX({})` and IGMPv2 leaves are `TO_IN({})`
    (RFC 3376 §7.3.2). MLDv1 reports and Done messages map the same way
    (RFC 3810 §8.3.2).
  - While an older-version host timer runs for a group on a port, `BLOCK` is
    ignored and `TO_EX(x)` acts as `TO_EX({})` (RFC 3376 §7.3.2). The timer
    is the group membership interval.
  - Fast leave deletes the port's group state on a record that leaves it in
    `INCLUDE({})`.
- **The switch never queries.** A "Send Q" action in §6.4.2 lowers no timer
  on its own. An observed group-specific or group-and-source-specific query
  with the suppress flag clear lowers the named timers to LMQT (§6.6.1).
  When a table row calls for a query, the VLAN has a router port, and no
  matching query arrives within LMQT, forwarding for that group carries an
  `Incomplete` issue `mcast-query-unobserved` on
  `ProtocolScope(node, "mcast", "<vid>/<group>")` until state changes. With
  no router port there is no querier, full timers are the real behavior, and
  the result stays `Complete`. Why: querier election is out of scope, and
  a result that a real querier would prune must not read `Complete`.
- **Timer defaults.** `VLANSnooping` gains `LastMemberQueryInterval` (zero
  means 1 s, RFC 3376 §8.8; RFC 3810 §9.8) and `LastMemberQueryCount` (zero
  means 2, the robustness variable, RFC 3376 §8.1, §8.9). LMQT is their
  product (§8.10). `MembershipInterval` keeps its 260 s default.
  LMQT always comes from this configuration, never from an observed query's
  Max Resp Code or QRV. Why: a capture may omit the query. User-confirmed
  2026-09-14.
- **Aging stays lazy.** `Switch.Age` runs at each fabric hop
  (`src/common/netsim/fabric/run.go:444`), so forwarding sees current
  timers. `mcast` adds no wake. Expiry produces no frame, so no event is
  lost.

## Requirements

1. **R16a:** A member fault moves only that member's buckets.
   **Acceptance example:** `lag1` has `BalanceSLB`, members `1`, `2`, `3`,
   and flows in buckets on each. Member `2` goes down. Every bucket that was
   on `1` or `3` keeps its member. Buckets that were on `2` go to the front of
   the enabled list in lookup order.
2. **R16b:** Active-backup does not fail back without a configured primary.
   **Acceptance example:** no `Primary`, members `a` and `b`. Traffic uses
   `a`, `a` fails and traffic moves to `b`, `a` recovers, and traffic stays
   on `b`. With `Primary: a`, traffic returns to `a`.
3. **R16c:** `BalanceTCP` without negotiated LACP drops with
   `lag.egress.no_member`. **Acceptance example:** `LACP.Mode` `Off` with two
   up members gives no selection.
4. **R16d:** Replaying the same explicit event sequence on two separately
   constructed switches yields the same bucket table, selections, and trace.
   A fault in `lag1` leaves `lag2`'s buckets, active member, and `Info`
   unchanged. **Acceptance example:** two LAGs on one switch; fault a member
   of `lag1`; `LagInfo("lag2")` and its selections are equal before and after.
5. **R16e:** `Peek` commits no selection state.
   **Acceptance example:** `Peek`, `Peek`, then `Forward` selects the same
   member as a lone `Forward` on a fresh switch.
6. **R16f:** A balanced selection older than one rebalance interval carries
   `lag-rebalance-unmodeled`. **Acceptance example:** interval 10 s, bucket
   assigned at t0; a frame at t0+9 s is `Complete`, one at t0+10 s is
   `Incomplete`; with `RebalanceInterval` set to 0 both are `Complete`.
7. **R17:** Source filtering follows §6.3.
   **Acceptance example:** port `p1` sends `IS_IN({S1})` for G. A frame from
   S1 to G egresses `p1`; a frame from S2 to G does not. After `p1` sends
   `IS_EX({S2})`, S1 egresses and S2 does not.
8. **R18:** Non-fast leave follows §6.4.2 and §6.6.1.
   **Acceptance example:** LMQI 1 s, LMQC 2, a router port on `p9`. `p1`
   joins G with an IGMPv2 report and then leaves at t. A group-specific query
   for G arrives from `p9` at t. A frame at t+1 s egresses `p1`; one at
   t+2 s does not. Without the query, a frame at t+3 s still egresses `p1`
   and carries `mcast-query-unobserved`. With no router port it egresses and
   stays `Complete`.
9. **R9:** `RebalanceInterval`, `LastMemberQueryInterval`, and
   `LastMemberQueryCount` are covered by validation (negative values
   rejected with field paths), normalization, `Clone`, and `Diff`. Dropping
   the invented `Primary` changes `lag.Diff` output and its tests.
   **Acceptance example:** changing `LastMemberQueryCount` from 0 to 3 gives
   one change; 0 and an explicit 2 give none.
10. **R39:** The corpus admits four cases, each naming its false answer:
    - `planning/lag-member-fault-keeps-surviving-flows`: all flows remap.
    - `troubleshooting/active-backup-no-failback`: traffic returns to the
      lowest-named member.
    - `troubleshooting/ssm-rejects-unjoined-source`: the group-only model
      admits S2.
    - `troubleshooting/leave-last-member-query`: forwarding continues for
      the full membership interval after a queried leave.

## Out of scope

- OVS load-based rebalancing, `bond-detect-mode miimon`, and LACP
  partner-system-priority matching.
- Querier election, query emission, IGMPv3 older-version querier
  compatibility (RFC 3376 §7.3.1), multicast routing, and proxying.
- netmodel mapping of multicast snooping. No schema carries it
  (`src/common/netsim/vswitch/netmodel/` has no multicast references).
- Retention of the new state across `Derive`; phase 5 owns it. Until then,
  `Derive` retains `lag` and `mcast` state under today's predicates
  (`src/common/netsim/vswitch/derive.go:63`, `:82`).

## Units

### U1. LAG bucket table, active member, and balance-tcp gate

Files: `src/common/netsim/vswitch/lag/` (all files and tests, `README.md`)
After: none
Change: `Select` and `bridge.Selector.Select` take `now`. Runtime state holds
the 256-bucket table with assignment times, the
enabled-list order, and the active member. `Select` takes a commit flag or
an equivalent split, applies the Decisions above, and returns the bucket,
prior member, and assignment cause (`kept`, `first-use`, `reassigned`,
`primary`, `last-active`, `first-enabled`). `SelectionFact` records them.
`Normalize` stops filling `Primary` and fills `RebalanceInterval`. `Info`
gains `Pending`. `Clone` deep-copies the new state. The README replaces the
modulo description with the bucket rules and their OVS sources.
Tests: the R16a, R16b, R16c, R16d, and R16e examples at the layer level;
enable-order ties; rebalance-interval normalization (nil, 0, 500 ms, 20 s);
`Pending` for each cause; clone isolation of the bucket table.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/lag`

### U2. Multicast source state and leave timing

Files: `src/common/netsim/vswitch/mcast/` (all files and tests, `README.md`)
After: none
Change: per-port group state replaces `groupKey` entries. `Learn` and
`LearnMLD` apply the §6.4 tables, the compatibility mapping, fast leave, and
observed specific queries. `Age` applies §6.5 and source timer expiry.
`Resolve(vid, group, source, now)` returns the admitted member ports plus
router ports, registration, and the pending-query issue when it applies.
`Groups` returns per-port mode, group timer, and sources in canonical order.
`Retain` keeps its predicate over (VLAN, port). The README replaces the
group-only approximation table with the state tables and the query rule.
Tests: one case per row of §6.4.1 and §6.4.2, both modes of §6.5, every §6.3
row, IGMPv2 and MLDv1 compatibility, an older-host `BLOCK` ignored, the R17
and R18 examples at the layer level, fast leave, config validation and
`Diff` for the new fields.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/mcast`

### U3. Switch and fabric integration

Files: `src/common/netsim/vswitch/switch.go`, `metadata.go`,
`src/common/netsim/vswitch/bridge/bridge.go` (selector and resolver
signatures), `src/common/netsim/fabric/run.go` (`SelectMember` commit),
their tests, `src/common/netsim/vswitch/README.md`
After: U1, U2
Change: `Switch.Resolve` passes the decoded IP source. The bridge's selector
call commits only when the forward mutates. Fabric transmission commits.
Forward metadata consults the aggregator scope for `lag-rebalance-unmodeled`
and the `(vid, group)` protocol scope for `mcast-query-unobserved`, and
cites switch construction evidence. `MembershipFact` records the source and
per-port admission.
Tests: the R16e, R16f, and R18 examples through `Switch.Forward` and
`Peek`; a two-switch fabric in which an `(S,G)` join on one switch filters
S2 across the uplink; journey metadata carries the rebalance issue only for
journeys through the balanced LAG.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch src/common/netsim/fabric`

### U4. Corpus cases and documentation

Files: `src/common/netsim/internal/netsimtest/cases.go`, `corpus_test.go`,
`README.md`, `docs/architecture/2026-09-10-virtual-device-direction.md`,
`src/common/netsim/README.md`
After: U3
Change: register the four R39 cases as switch-only cases shaped like
`CaseTroubleshootingUnicastForwarding`
(`src/common/netsim/internal/netsimtest/cases.go:367`). The direction record
states the bucket semantics, the query rule, and the two new issues; its
protocol-depth gap drops "fast leave" and keeps querier election.
Tests: the corpus runner admits each case and re-executes it
deterministically.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim docs/architecture/2026-09-10-virtual-device-direction.md`

## Verification

```bash
go test -race ./src/common/net/... ./src/common/netsim/...
go vet ./src/common/net/... ./src/common/netsim/...
.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim \
  docs/architecture/2026-09-10-virtual-device-direction.md \
  docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-phase3-plan.md \
  docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
```

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] Every requirement example and every §6.3/§6.4/§6.5 row has a named test.
- [ ] `lag` and `mcast` READMEs, the netsim README, and the direction record
      updated in the same change.
- [ ] This plan's `status` set with an outcome note; the parent's phase 3
      `Landed:` line filled; no plan labels in code.
