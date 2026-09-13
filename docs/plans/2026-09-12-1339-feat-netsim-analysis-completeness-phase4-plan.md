---
title: Network Simulation Analysis Completeness, Phase 4 - Plan
type: feat
date: 2026-09-12
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
parent: docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
---

# Network simulation analysis completeness, phase 4: Routing and neighbor resolution - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

Make supported layer-3 analysis distinguish a definite route outcome from a
missing observation, pending resolution, failed resolution, invalid recursion,
or unsupported host behavior. This phase claims parent requirements R19-R22. It
consumes R9, R37, and R39 through route, neighbor, codec, seam, and corpus tests.
Phase 5 later defines retention dependencies for the new state.

## Decisions

- Keep routing input static and explicit. Dynamic routing protocols and a live
  routing daemon are not required.
- Model route candidates and recursion as bounded deterministic selection with
  evidence for each decision.
- Do not turn a missing neighbor into an immediate drop. Routing or fabric input
  accepts explicit logical-time neighbor transitions and decoded ARP/ND records.
  It does not generate autonomous requests. Phase 6 later binds scenario actions
  to those transitions.
- Keep ARP/ND packet codecs and state transitions separate from file capture and
  host operating-system behavior.

## Requirements

1. **R19:** Route selection supports preference, metric, equal-cost candidates,
   and bounded recursion. **Acceptance example:** an equal-cost set selects by
   the configured deterministic policy and records all candidates.
2. **R20:** Neighbor state distinguishes unobserved, resolving, reachable, stale,
   and failed. **Acceptance example:** a missing entry can queue a frame when
   resolution is enabled.
3. **R21:** Optional deterministic ARP and IPv6 ND can resolve a queued frame.
   **Acceptance example:** an injected reply releases the frame at a defined
   logical time with causal trace links.
4. **R22:** Validation rejects cross-family and unusable egress combinations.
   **Acceptance example:** an IPv4 route cannot silently accept an IPv6 next hop.
5. **R37:** Routing and neighbor state behaves deterministically for identical
   logical-time inputs. **Acceptance example:** the same route and injected reply
   sequence yields the same queued-frame result and trace.
6. **R9:** Every added route, neighbor, ARP, and ND field extends validation,
   normalization, cloning, and `Diff`. **Acceptance example:** changing
   resolution mode or retry timing appears in `Diff`; phase 5 later owns
   dependency invalidation.
7. **R39:** Each routing or neighbor addition names its operator question,
   current false answer, minimum semantics, unsupported boundary, and golden
   case. **Acceptance example:** neighbor lifecycle lands with a case proving the
   current absent-entry terminal drop is false.

## Out of scope

- OSPF, IS-IS, BGP, RIP, policy routing, new inter-VRF leaking behavior, NAT,
  firewalling, SLAAC, DAD, full NUD, DHCP, and operating-system host stacks.
- Packet capture readers, live sockets, and wall-clock retry goroutines.

## Units

### U1: Re-plan route and neighbor state machines

- **Files:** `src/common/netsim/vswitch/routing/`, new protocol codecs under
  `src/common/net/arp/` and `src/common/net/ndp/`, switch and fabric integration
- **After:** parent U1, U2
- **Change:** Specify candidate ranking, ECMP policy, recursion limits and cycles,
  neighbor states, retries, timers, queued frames, trace rules, and unsupported
  boundaries against the landed contracts. Complete the capability-value matrix
  before retaining each proposed behavior.
- **Tests:** Selection and state-transition tables with failure scenarios.
- **Verify:** Re-plan before editing.

### U2: Implement candidate selection and bounded recursion

- **Files:** `src/common/netsim/vswitch/routing/`, routing integration under
  `src/common/netsim/vswitch/`
- **After:** U1
- **Change:** Preserve valid candidate sets, rank them deterministically, resolve
  bounded next-hop recursion, surface invalid cycles or incomplete facts, and
  extend the phase 1 field matrix.
- **Tests:** Preference, metric, ECMP, longest prefix, recursion success, cycle,
  limit, address-family, and egress-reference cases.
- **Verify:** Focused routing and switch tests.

### U3: Implement explicit ARP and ND transitions

- **Files:** new codecs under `src/common/net/arp/` and `src/common/net/ndp/`,
  neighbor state under `src/common/netsim/vswitch/routing/`, integration under
  `src/common/netsim/vswitch/` and `src/common/netsim/fabric/`
- **After:** U1, U2
- **Change:** Decode request and reply records and add only cache transition,
  retry, failure, and queued-frame behavior driven by explicit logical-time
  events. Extend the phase 1 field matrix. Do not generate requests; phase 6 owns
  scenario binding.
- **Tests:** Static-only miss, resolution success, timeout, retry, stale entry,
  malformed reply, queued release, and identical-event-sequence stability.
- **Verify:** Focused codec, routing, switch, and fabric tests.

### U4: Document supported layer-3 fidelity

- **Files:** affected package READMEs and the parent architecture record
- **After:** U2, U3
- **Change:** Document route and neighbor inputs, logical-time behavior, working
  examples, evidence, and explicit host and routing-protocol omissions.
- **Tests:** Compile documentation examples where practical.
- **Verify:** Netsim race tests, vet, diff-aware verifier.

## Verification

The re-plan must define finite recursion and retry limits before implementation.
Run:

```bash
go test -race ./src/common/net/... ./src/common/netsim/...
go vet ./src/common/net/... ./src/common/netsim/...
.claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>
```

## Definition of done

- [ ] Parent requirements R19-R22 pass their acceptance examples.
- [ ] Route selection records every candidate and deterministic tie-break.
- [ ] Neighbor absence, resolution, failure, and unsupported behavior differ.
- [ ] ARP/ND behavior is scenario-driven and bounded by logical time.
- [ ] Dynamic routing and full host behavior remain explicit omissions.
