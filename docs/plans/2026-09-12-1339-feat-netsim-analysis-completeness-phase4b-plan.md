---
title: Network Simulation Analysis Completeness, Phase 4b - Plan
type: feat
date: 2026-09-15
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
parent: docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
---

# Network simulation analysis completeness, phase 4b: Neighbor lifecycle and address resolution - Plan

> Re-planned by `plan` when its turn comes; the tree will have moved. The
> Requirements and the cited evidence hold. The Decisions carry open questions
> the re-plan must put to the user before the units can be written, and the
> Units are a draft against the route candidate and resolution shapes phase 4
> lands.

## Goal

A routed frame whose next hop has no link-layer address gets an answer that
distinguishes "nobody has asked yet" from "we asked and nothing replied" from
"we never resolve here". A neighbor entry carries a state and a time, an
injected reply moves it, and a frame held while resolution is in progress
either leaves at a stated logical time or fails for a stated reason. The means
are a neighbor state machine in `routing`, ARP and Neighbor Solicitation codecs
under `src/common/net/`, and a queue the switch drains on an explicit event.

Stop condition: the plan is wrong if a held frame cannot be released at a
logical time without the fabric growing a wake the simulation does not
otherwise need, or if phase 5's retention model cannot key a queued frame.

This phase claims parent R20 and R21 and extends R9, R37, and R39. Route
selection and recursion (R19, R22) landed in
`docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-phase4-plan.md`.

## What exists

Verified against the tree at the time of writing:

- `Neighbor{Interface, Addr, MAC}`
  (`src/common/netsim/vswitch/routing/config.go:39`) is a static record with no
  state and no time. `newLayer` loads them into a map keyed by interface and
  address (`src/common/netsim/vswitch/routing/layer.go:244`).
- A lookup miss is an immediate terminal drop with `ReasonNeighborMiss`
  (`src/common/netsim/vswitch/routing/layer.go:468`, `:624`). This is the
  false answer parent R20 names.
- `routing.Layer` holds no timers and has no `Age` or `Wake`, unlike `lag` and
  `mcast`. Address resolution is the first thing in this package that needs
  logical time.
- `src/common/net/` has no `arp` and no `ndp` package; both are new.

## Requirements

1. **R20:** Neighbor state distinguishes unobserved, resolving, reachable,
   stale, and failed. **Acceptance example:** an absent entry does not become
   an immediate terminal drop unless resolution is disabled or has already
   failed for that address.
2. **R21:** Resolution accepts explicit logical-time transitions and decoded
   ARP or Neighbor Discovery records without running an autonomous host stack.
   **Acceptance example:** a queued routed frame proceeds after an injected ARP
   reply; with no reply it stays pending until an explicit timeout.
3. **R9:** Every added neighbor, ARP, and ND field extends validation,
   normalization, `Clone`, and `Diff`. **Acceptance example:** changing the
   resolution mode or the retry timing appears in `Diff`.
4. **R37:** Identical logical-time input gives an identical queue result and
   trace. **Acceptance example:** the same route and injected-reply sequence
   replayed on two separately constructed switches yields equal held-frame
   outcomes and equal facts.
5. **R39:** The corpus admits at least one case whose false answer is the
   current absent-entry terminal drop.

## Decisions

Evidence-backed, and not expected to change:

- **One state machine serves ARP and Neighbor Discovery.** RFC 4861 §7.3.2
  defines the states for IPv6: `INCOMPLETE` ("Address resolution is in progress
  and the link-layer address of the neighbor has not yet been determined"),
  `REACHABLE` ("known to have been reachable recently"), `STALE` ("no longer
  known to be reachable but until traffic is sent to the neighbor, no attempt
  should be made to verify its reachability"), `DELAY`, and `PROBE`
  (<https://www.rfc-editor.org/rfc/rfc4861#section-7.3.2>). IPv4 ARP (RFC 826)
  defines no state machine of its own, and Linux runs both families through one
  neighbour table. Mapping parent R20's five names onto this set, rather than
  inventing a second vocabulary for IPv4, is the decision.
- **The switch never solicits.** Nothing here generates an ARP request or a
  Neighbor Solicitation, exactly as phase 3 decided for multicast queries. A
  reply arrives because a scenario injected it or a fabric peer sent it. Why:
  an autonomous host stack is an explicit parent omission, and a capture may
  omit the request.
- **Codecs are separate from state.** `src/common/net/arp` and
  `src/common/net/ndp` decode and encode only. The state machine lives in
  `routing`. Why: this is the shape every other protocol in the tree already
  has, `src/common/net/igmp` with `vswitch/mcast` being the closest parallel.

## Open questions

The re-plan must settle these with the user before writing units. Each carries
a recommendation, and none is decided.

1. **What happens to a frame whose neighbor is `INCOMPLETE`?** Real gear holds
   a small number of packets per neighbor and drops the rest. Recommendation: a
   bounded per-neighbor queue with a documented depth, a held frame released by
   an injected reply and failed by an explicit timeout. The alternative, no
   queue at all and an immediate `Incomplete` result, is cheaper and answers
   "would this have been delivered" without modeling the hold.
2. **Where does the queue live, and who drains it?** Recommendation: in
   `routing`, drained by the switch on the same lazy `Age` call the multicast
   layer already rides (`src/common/netsim/vswitch/switch.go:1479`). A queue in
   the fabric would need a wake the simulation does not otherwise have, which
   the Goal names as this plan's stop condition.
3. **Does an unobserved neighbor make a result `Incomplete` or `Complete`?**
   Recommendation: `Incomplete` when resolution is enabled and nothing has been
   observed, because netsim genuinely does not know whether the neighbor would
   have answered; `Complete` when resolution is disabled or has already failed,
   because then the drop is definite. This is the distinction parent R20 exists
   to draw, and it is the one decision here most likely to be contentious.
4. **Do phase 4's withdrawn routes interact with neighbor state?** A route
   withdrawn for an unresolvable next hop and a next hop with no neighbor are
   different failures that an operator may well conflate. Recommendation: keep
   them separate and name both in the README.

## Units

Draft. Check each against what phase 4 left in `routing/layer.go`,
`vswitch/switch.go`, and the candidate-set shape before writing.

### U1. ARP and Neighbor Solicitation codecs

Files: `src/common/net/arp/` (new), `src/common/net/ndp/` (new)
After: none
Change: decode and encode ARP request and reply (RFC 826) and Neighbor
Solicitation, Neighbor Advertisement, and their source and target link-layer
address options (RFC 4861 §4.3, §4.4, §4.6.1).
Tests: wire vectors in both directions, malformed input, and the option cases.

### U2. Neighbor state machine

Files: `src/common/netsim/vswitch/routing/`
After: U1
Change: `Neighbor` carries a state and the time it can next change. Explicit
transitions apply decoded records. Timers age entries.
Tests: one case per transition, plus validation, `Clone`, and `Diff`.

### U3. Held frames and the switch seam

Files: `src/common/netsim/vswitch/routing/`, `src/common/netsim/vswitch/`,
`src/common/netsim/fabric/`
After: U2
Change: whatever open question 1 settles.
Tests: release, timeout, and the R37 replay example.

### U4. Corpus and documentation

Files: `src/common/netsim/internal/netsimtest/`, package READMEs, the direction
record
After: U3

## Verification

```bash
go test -race ./src/common/net/... ./src/common/netsim/...
go vet ./src/common/net/... ./src/common/netsim/...
.claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>
```

## Definition of done

- [ ] Parent R20 and R21 pass their acceptance examples.
- [ ] An absent neighbor, a resolving one, a failed one, and a disabled
      resolution give four different answers.
- [ ] Nothing generates a request; every transition is explicit or decoded.
- [ ] Dynamic routing and full host behavior remain explicit omissions.
