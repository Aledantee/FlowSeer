---
title: Network Simulation Analysis Completeness, Phase 4 - Plan
type: feat
date: 2026-09-15
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
parent: docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
---

# Network simulation analysis completeness, phase 4: Route selection and recursion - Plan

## Goal

A route lookup answers "which next hop carries this flow, and why that one"
the way a router's RIB does: the longest prefix wins, then the lowest
administrative distance, then the lowest metric, and what survives is a
candidate set a flow hash picks from. A static route whose next hop resolves
only through another static route is resolved when the table is built, and one
that self-recurses or resolves to nothing is not installed at all, so the
packet falls through to whatever else matches. The means are a candidate set
and a resolution pass in `routing`, both held in the table rather than
recomputed per packet.

Stop condition: the plan is wrong if the candidate set cannot reach the journey
without `routing.Result` growing a field the bridge has to thread through
`vswitch`, or if removing the one-route-per-prefix invariant from `Validate`
turns out to be load-bearing for `Diff` or `Derive` in a way this plan does not
account for.

This phase claims parent R19 and R22 and extends R9, R37, and R39. Neighbor
lifecycle and ARP/ND (R20, R21) moved to
`docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-phase4b-plan.md`.

## Decisions

- **The parent's Decisions govern; phases 1 to 3 are used as landed.**
- **Phase 4 splits in two.** Route selection lands here, neighbor lifecycle and
  ARP/ND in phase 4b. Why: the two together exceed six units, the capability
  packages are disjoint, and phase 4b needs two new protocol codec packages
  that nothing here touches. The shared integration files are
  `routing/layer.go` and `vswitch/switch.go`, so phase 4b runs after this phase
  and is re-planned against what this one leaves there.

### Ordering and the candidate set

- **Route order is prefix length, then preference, then metric.** `Route` gains
  `Preference *uint8` (administrative distance, lower wins) and `Metric
  uint32` (the tie-break within one preference). Nil preference normalizes to 1
  for a static route; a connected route is built at 0. Why: today the table
  breaks an equal-length tie on `kind` and then on interface name
  (`src/common/netsim/vswitch/routing/layer.go:234`), which is an invented
  order an operator cannot configure or predict. A pointer, rather than a
  sentinel zero, so an operator can configure a static route at preference 0
  and `Diff` can tell "unset" from "explicitly 0" — the shape
  `mcast.VLANSnooping.FloodUnregistered` and `lag.LAG.RebalanceInterval`
  already use. A `uint8` has no negative value, so R9's usual
  negative-rejection test does not apply to these two fields.
- **A prefix may carry several routes.** `Validate` today refuses a second
  route on one prefix ("The table holds one route per prefix",
  `src/common/netsim/vswitch/routing/config.go:277`, rejecting at `:295`).
  That check is removed and the uniqueness key becomes the whole tuple
  (prefix, preference, metric, next hop, interface), so a genuinely duplicated
  route is still an error. Why: without this, every equal-cost configuration in
  this plan fails construction, and an ECMP set is by definition several routes
  on one prefix. The doc comment on `Validate` changes with it.
- **What survives the tie-break is a candidate set, not one route.** Routes
  equal on prefix length, preference and metric are all kept and all recorded.
  Why: parent R19 asks that equal-cost candidates be recorded, and a lookup
  that keeps only the winner cannot answer "what else would have carried this".
- **Within a candidate set, order is next hop then egress interface.** Why: the
  flow hash reduces to an index, the route fact's canonical string is compared
  verbatim by the corpus, and R37 compares facts across constructions, so an
  unordered set makes all three non-deterministic. The old `kind`-then-interface
  tie-break at `layer.go:234` is removed, and two candidates sharing an egress
  interface would otherwise have no defined relative order.
- **`Normalize` sorts routes by that same total order.** `slices.SortFunc` is
  not stable, and today's comparator (`config.go:97`) compares prefixes only, so
  with several routes per prefix two constructions from one input can order
  them differently. Why: that is R37 and the direction record's "Re-running a
  simulation from its construction specification yields identical state and
  trace records" (`docs/architecture/2026-09-10-virtual-device-direction.md:169`),
  and it would fail intermittently rather than reproducibly.
- **`Diff` re-keys on the whole tuple.** `diff.go:272` builds
  `map[netip.Prefix]Route`, which silently drops all but one route per prefix.
  Why: without this an operator adding or removing an ECMP member sees no
  change at all.
- **`Route.Canonical` and the VRF snapshot carry the new fields.** `Route` is
  itself a `trace.Fact` (`config.go:112`), and `snapshotVRF` (`diff.go:116`)
  encodes a whole VRF. Why: two routes differing only in preference would
  otherwise produce identical facts, breaking the injective-fact contract the
  direction record states at
  `docs/architecture/2026-09-10-virtual-device-direction.md:275`.

### Flow hash

- **A candidate set resolves by hashing the IP flow.** The hash covers source
  address, destination address, protocol, and the source and destination ports
  when the packet carries them. The chosen member is
  `candidates[hash % len(candidates)]` over the canonical order above. Why: a
  router hashes layer 3 and 4; the bond hash in
  `src/common/netsim/vswitch/lag/hash.go:37` additionally folds in both MAC
  addresses and the EtherType, so it answers a different question and is not
  reused. The hash is a pure function of the packet, so `Peek` and `Forward`
  agree with no commit flag, and phase 3's commit discipline does not extend
  here. No bucket table: `lag`'s buckets exist to keep a flow on its member
  across a membership change, which has no analogue in a table rebuilt whole.
- **A fragmented datagram hashes on the three-tuple.** The ports enter the hash
  only when the packet is not a fragment at all: for IPv4, `V4.FragmentOffset`
  is zero **and** the more-fragments flag is clear. Why: a non-first fragment
  carries no ports, so hashing its first four payload octets would spread one
  datagram's fragments across next hops and break reassembly; excluding ports
  from the first fragment too is what keeps the whole datagram together.
- **IPv6 extension headers are a documented limitation.** `ip.Decode` does not
  walk the header chain (`src/common/net/ip/ip.go:180`), so `Header.Protocol`
  is the fixed header's first Next Header. A TCP segment behind a Hop-by-Hop,
  Routing, Fragment or Destination Options header therefore hashes on the
  three-tuple, and the extension header's own number is what lands in the
  protocol position. This is deterministic and safe, and the new README says so
  rather than claiming a five-tuple hash it does not perform. Extending
  `ip.Decode` is not this phase's work.
- **`Originate` selects the same way.** It performs its own table scan
  (`layer.go:578`) and would otherwise take an undefined member of a candidate
  set. It hashes the chosen source address, the destination, the protocol
  argument, and the first four octets of the caller's payload when the protocol
  is 6 or 17 and the payload is long enough. Why: a second silent selection rule
  in one package is worse than an imperfect shared one, and an originated
  datagram is never a fragment.

### Recursion

- **Recursion resolves when the table is built, not when a packet arrives.**
  This is what real gear does. Cisco IOS XR: "A recursive static route is valid
  (that is, it is a candidate for insertion in the routing table) only when the
  specified next hop resolves, either directly or indirectly, to a valid output
  interface, provided the route does not self-recurse, and the recursion depth
  does not exceed the maximum IPv6 forwarding recursion depth", and a route
  that becomes self-recursive is withdrawn from the routing table but not from
  the configuration
  (<https://www.cisco.com/c/en/us/td/docs/iosxr/cisco8000/routing/70x/b-routing-cg-cisco8000-70x/implement-static-routes.html>).
  FRR zebra keeps a path invalid when the chain loops or the next hop does not
  resolve (<https://docs.frrouting.org/en/latest/zebra.html>).
- **The on-link next-hop requirement moves into the resolver.** `Validate`
  today rejects a route whose next hop is in no interface prefix of the VRF
  (`src/common/netsim/vswitch/routing/config.go:333`). A recursive static route
  is by definition one whose next hop is not on-link, so that check and this
  phase are mutually exclusive. It becomes the resolver's `unresolved`
  withdrawal reason.
- **An unresolvable route is not installed, and that is a definite answer.**
  It is not a construction error, because the configuration stays valid on real
  gear, and not `Incomplete`, because a real router definitely would not install
  it either. Forwarding answers from the table that remains: a less specific
  route, or `no-route`. Why: `Incomplete` is reserved for an input netsim does
  not have, and here it has every input it needs. This is consistent with the
  result-contract axes at
  `docs/architecture/2026-09-10-virtual-device-direction.md:153`.
- **The recursion bound is a documented constant, not a configuration field.**
  No vendor publishes its bound: Cisco names "the maximum IPv6 forwarding
  recursion depth" without a value, and Juniper's resolver prevents infinite
  lookup with no operator control. The constant is netsim's own and says so,
  the way the lowest-name active-backup tie-break documents itself as netsim's
  stand-in for the Open vSwitch hash-map walk. User-confirmed 2026-09-15.
- **A route that is not installed is recorded with the chain walked.**
  `Layer.WithdrawnRoutes(vrf string) []WithdrawnRoute` returns
  `WithdrawnRoute{Prefix, NextHop, Interface, Reason, Chain []netip.Prefix}`,
  sorted by prefix then next hop. `Reason` is one of `self-recursive`,
  `depth-exceeded`, `unresolved`. Why: "why is my static route not being used"
  is the operator question this decision creates, and a route that silently
  vanishes answers it worse than today's construction error does.
- **A recursive route inherits the candidate set that resolved it.** When the
  next hop resolves to a route that itself has several candidates, the
  recursive route carries all of them. Why: this is why a BGP next hop spreads
  over an ECMP IGP path, and collapsing to one would invent an order.
- **Route resolution does not consult neighbors.** Resolution walks routes and
  connected prefixes only; a next hop with no neighbor keeps today's
  per-packet `neighbor-miss` drop. Why: phase 4b makes neighbor entries
  dynamic, and it must not find an expectation here that a route is withdrawn
  when its neighbor fails. The two failures stay separate and the README names
  both.

### Validation

- **Validation rejects a cross-family next hop and an unusable egress.** Why:
  parent R22, and today `Validate` checks neither
  (`src/common/netsim/vswitch/routing/config.go:142`).

## Requirements

1. **R19a:** Order is prefix length, then preference, then metric.
   **Acceptance example:** `10.0.0.0/8` via A at preference 1 and
   `10.0.0.0/16` via B at preference 250 both match `10.0.1.1`; B wins on
   prefix length. With both at `/8`, A wins on preference. With both at
   preference 1 and metrics 10 and 20, the metric decides.
2. **R19b:** Routes equal on all three form a candidate set, and every member
   is recorded in canonical order. **Acceptance example:** three `/8` routes at
   preference 1 and metric 10 give a lookup whose fact names all three next
   hops, ordered by next hop, and which one carried the packet.
3. **R19c:** A candidate set resolves by flow hash, stably and without
   mutation. **Acceptance example:** two candidates; flows differing only in
   source port reach both next hops across a set of flows; one flow repeated
   ten times takes the same next hop every time; `Peek` and `Forward` agree,
   and two `Peek` calls before a `Forward` do not change it.
4. **R19d:** Recursion resolves at table build, bounded, and a failure
   withdraws the route rather than dropping the packet.
   **Acceptance example:** `10.0.0.0/8 via 192.0.2.1`, where `192.0.2.0/24 via
   198.51.100.1` and `198.51.100.0/24` is connected on `ge-0`, installs with
   egress `ge-0` — a configuration today's `Validate` rejects. A route whose
   next hop is inside its own prefix is not installed. `A via B` and `B via A`
   installs neither. A chain one longer than the bound is not installed. In
   each failing case a packet matching a less specific installed route takes
   that route and the result is `Complete`.
5. **R19e:** A recursive route inherits the candidate set that resolved it.
   **Acceptance example:** the next hop resolves to a two-candidate set; the
   recursive route's lookup names both.
6. **R19f:** A withdrawn route is reachable with its reason and chain.
   **Acceptance example:** `WithdrawnRoutes("default")` returns one entry with
   reason `self-recursive` and the prefixes visited, and two withdrawals sort
   by prefix then next hop.
7. **R22:** Validation rejects a cross-family next hop and an unusable egress
   reference. **Acceptance example:** an IPv4 prefix with an IPv6 next hop is
   rejected with the field path `vrfs.default.routes.10.0.0.0/8.next_hop`; a
   route naming an `Interface` absent from the VRF keeps today's rejection; a
   next hop that is unspecified or multicast is rejected.
8. **R9:** `Preference` and `Metric` extend normalization, `Clone`, `Canonical`,
   and `Diff`. **Acceptance example:** two static routes on one prefix
   differing only in `Metric` give one `Diff` change; a nil `Preference` and an
   explicit 1 on a static route give none; a nil and an explicit 0 give one.
9. **R37:** Identical input gives an identical table, selection, and trace.
   **Acceptance example:** two separately constructed layers over a
   configuration with three routes on one prefix and one withdrawal produce
   equal installed tables, equal `WithdrawnRoutes`, and equal facts for the
   same packet, over ten repetitions.
10. **R39:** The corpus admits two cases, each naming its false answer:
    - `planning/ecmp-candidates-recorded`: one route wins and the alternatives
      are invisible.
    - `troubleshooting/recursive-route-not-installed`: a broken recursive route
      silently forwards, or fails construction, instead of leaving the table.

## Out of scope

- OSPF, IS-IS, BGP, RIP, policy routing, inter-VRF leaking, NAT, firewalling.
- Neighbor lifecycle, ARP, IPv6 Neighbor Discovery, and queued frames: phase 4b
  owns R20 and R21. Until it lands, a next hop with no configured neighbor
  keeps today's `neighbor-miss` drop
  (`src/common/netsim/vswitch/routing/layer.go:468`, `:624`).
- Weighted ECMP, per-packet spreading, and resilient hashing.
- Walking the IPv6 extension header chain in `ip.Decode`.
- The same fragmentation defect in the bond hash. `inspectTCPHashInput`
  (`src/common/netsim/vswitch/lag/hash.go:74`) reads the first four payload
  octets as ports whenever the protocol is 6 or 17, without checking
  `V4.FragmentOffset`, so a non-first fragment hashes datagram bytes as a port
  pair. The routing hash above does not repeat it. Fixing `lag` is a separate
  change against a package this phase does not touch.
- Re-resolving recursion when the table changes at runtime. `Derive` rebuilds
  the routing layer from the normalized configuration on every call
  (`src/common/netsim/vswitch/derive.go:29`), so resolution re-runs whole and
  no stale table survives.

## Units

### U1. Preference, metric, the candidate set, and the keys that follow

Files: `src/common/netsim/vswitch/routing/config.go`, `config_test.go`,
`diff.go`, `layer.go`, `layer_test.go`, `fact.go`
After: none
Change: `Route` gains `Preference *uint8` and `Metric uint32`. `Normalize`
fills nil preference to 1 for a static route and sorts routes by the total
order (prefix, preference, metric, next hop, interface). `Validate` drops the
one-route-per-prefix check and keys uniqueness on that whole tuple; its doc
comment changes with it. `routing.Result` gains an exported
`Candidates []Candidate` and the exported `Candidate` type, so the seam in U5
has somewhere to read from. The table lookup returns every route equal to the
winner on prefix length, preference and metric, in canonical order.
`Route.Canonical` and `snapshotVRF` encode the two new fields. `Diff` keys its
route maps on the whole tuple.
Tests: the R19a and R19b examples; connected beats static through preference,
not through `kind`; a single-candidate lookup keeps today's shape; two routes
identical in every field are still rejected; the R9 examples; `Canonical`
distinguishes two routes differing only in preference; `Diff` reports an added
ECMP member.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/routing`

### U2. Recursive next-hop resolution at table build

Files: `src/common/netsim/vswitch/routing/layer.go`, `config.go`,
`config_test.go`, `layer_test.go`, `fact.go`
After: U1
Change: `Validate` drops the on-link next-hop requirement at `config.go:333`.
Building a VRF resolves each static route's egress by walking the table, up to
a documented constant that states it is netsim's own bound. A route that
self-recurses, exceeds the bound, or reaches no connected interface is not
installed; the configuration stays valid. `Layer.WithdrawnRoutes` returns the
withdrawals as the Decisions describe. A recursive route carries the candidate
set of the route that resolved it. The construct-time next-hop scan at
`layer.go:204` is replaced by this pass.
Tests: the R19d, R19e, and R19f examples; a packet falling through to a less
specific route after a withdrawal, asserted `Complete`; the R37 example; a
next hop that is not on-link and does resolve is accepted where `Validate`
used to reject it.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/routing`

### U3. Cross-family and next-hop validation

Files: `src/common/netsim/vswitch/routing/config.go`, `config_test.go`
After: U2
Change: `Validate` rejects a route whose next hop family differs from its
prefix family, and a next hop that is unspecified or multicast. Each error
carries the field path and structured attributes, in the style of the errors
already in the file. The existing interface-outside-VRF rejection at
`config.go:318` stays.
Tests: the R22 example, one case per rejection; an IPv6 route on an interface
that also carries IPv4 is accepted, so the check is on the route, not the
interface.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/routing`

### U4. Flow-hash selection across a candidate set

Files: `src/common/netsim/vswitch/routing/layer.go`, `hash.go` (new),
`hash_test.go` (new), `layer_test.go`, `fact.go`,
`src/common/netsim/vswitch/routing/README.md` (new)
After: U2
Change: a lookup yielding more than one candidate picks
`candidates[hash % len(candidates)]`, hashing the IP flow as the Decisions
describe, including the fragment rule. `Originate` (`layer.go:578`) uses the
same selection over its own inputs. The route fact names every candidate, the
chosen one, and the fields that entered the hash. The README is new: the
ordering rule, the candidate set, the hash input in the byte order fed to it,
the fragment rule, the IPv6 extension-header limitation, the recursion bound as
netsim's own, what a withdrawn route means, and that a withdrawn route and a
`neighbor-miss` are different failures, with a working example.
Tests: the R19c example; a three-candidate set over a spread of flows reaches
every candidate; a non-TCP/UDP protocol hashes the three-tuple and two flows
differing only in port land together; a first fragment and a non-first fragment
of one datagram take the same next hop, where hashing the payload octets as
ports would separate them; a TCP segment behind an IPv6 extension header hashes
the three-tuple; `Originate` over a candidate set is stable.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/routing`

### U5. Switch seam, corpus cases, and documentation

Files: `src/common/netsim/vswitch/switch.go`, `switch_test.go`,
`src/common/netsim/internal/netsimtest/cases.go`, `corpus_test.go`,
`src/common/netsim/internal/netsimtest/README.md`,
`src/common/netsim/README.md`,
`docs/architecture/2026-09-10-virtual-device-direction.md`,
`docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md`
After: U3, U4
Change: `assembleRouteResult` (`switch.go:1289`, called from `:805` and `:848`)
carries `Result.Candidates` into the forwarding result, so a journey shows
which next hop was chosen among which alternatives. The two R39 cases are
registered as switch-only cases shaped like
`CasePlanningLAGMemberFaultKeepsSurvivingFlows`
(`src/common/netsim/internal/netsimtest/cases.go:1336`), which builds a
`vswitch.ConstructionSpec` directly — not
`CaseTroubleshootingUnicastForwarding` (`:372`), whose `netmodel.Load` fixture
produces no static routes
(`src/common/netsim/vswitch/netmodel/routing_test.go:269`). Their IDs go into
the hardcoded per-use-case lists at `corpus_test.go:590` and the case
catalogue in `netsimtest/README.md:14`. The direction record gains a routing
subsection next to the one phase 3 added, and its remaining-gap list at `:349`
drops the equal-cost and recursion clauses. The parent plan's capability table
row at
`docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md:42`
keeps its neighbor clause for phase 4b.
Tests: the two corpus cases, each re-executed deterministically; a
`Switch.Forward` whose routed egress names the candidate set.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim docs/architecture/2026-09-10-virtual-device-direction.md docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md`

## Verification

```bash
go test -race ./src/common/net/... ./src/common/netsim/...
go vet ./src/common/net/... ./src/common/netsim/...
.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim \
  docs/architecture/2026-09-10-virtual-device-direction.md \
  docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-phase4-plan.md \
  docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
```

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] Every requirement example has a named test.
- [ ] The new `routing/README.md`, the netsim README, the netsimtest README,
      and the direction record updated in the same change.
- [ ] This plan's `status` set with an outcome note; the parent's phase 4
      `Landed:` line filled; no plan labels in code.

## Open questions

- Does a withdrawn route belong in forward metadata as an assumption on the
  VRF scope, so a journey that fell through to a less specific route shows why?
  Recommendation: no. The result is `Complete` and the fall-through is the
  correct answer, so an assumption would add noise to every journey in the VRF.
  `WithdrawnRoutes` and the corpus case carry it instead. Decide in U5, where
  the seam is visible.
- Removing the one-route-per-prefix invariant is the largest single change here
  and it lands in U1, before any candidate-set behavior exists to justify it.
  If U1's test run shows a dependent the plan did not find, that is the stop
  condition above: report it rather than restoring the invariant behind a flag.
