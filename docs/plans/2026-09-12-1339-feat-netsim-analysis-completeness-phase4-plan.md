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
on the trace `bridge.Result` already carries, since a field on `bridge.Result`
would make `bridge` import a capability package, or if removing the
one-route-per-prefix invariant from `Validate` turns out to be load-bearing for
`Diff`, `Derive`, or `fabric` beyond what U1 and U2 claim.

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
  `Preference uint8` (administrative distance, lower wins) and `Metric uint32`
  (the tie-break within one preference). Why: today the table breaks an
  equal-length tie on `kind` and then on interface name
  (`src/common/netsim/vswitch/routing/layer.go:234`), which is an invented
  order an operator cannot configure or predict. A `uint8` has no negative
  value, so R9's usual negative-rejection test does not apply to these two
  fields. Metric compares only within one preference, which is what real gear
  does — Cisco compares metrics only "if there are multiple paths to the same
  destination from a single routing protocol"
  (<https://www.cisco.com/c/en/us/support/docs/ip/enhanced-interior-gateway-routing-protocol-eigrp/8651-21.html>).
  In a static-only table one preference is one source, so the question does not
  arise until a dynamic protocol does; the README says the comparison is
  within-preference rather than global. `uint8` covers the Cisco 0-255
  administrative distance and cannot express a Junos preference, which the
  README also states.
- **Preference 0 is reserved for connected routes.** A connected route is built
  at preference 0 and metric 0. A static route's `Preference` of 0 means unset
  and normalizes to 1, so a static route can never reach the connected tier and
  a plain `uint8` needs no pointer to tell "unset" from "explicitly 0". Why:
  netsim follows the Cisco administrative-distance model, where the floor is
  explicit: NX-OS states a static route's "range is from 1 to 255. The default
  is 1"
  (<https://www.cisco.com/c/en/us/td/docs/switches/datacenter/nexus5500/sw/command/reference/unicast/n5500-ucast-cr/n5k-fibrib_cmds_i.html>),
  over a table that puts "Connected: 0" against "Static: 1"
  (<https://www.cisco.com/c/en/us/support/docs/ip/border-gateway-protocol-bgp/15986-admin-distance.html>);
  FRR zebra's table matches (<https://docs.frrouting.org/en/latest/zebra.html>).
  Junos is the exception and is not followed here: its static preference range
  is "0 through 4,294,967,295", so a static route there can be configured at
  the directly-connected tier, which is itself fixed at 0 with no statement to
  modify it
  (<https://www.juniper.net/documentation/us/en/software/junos/cli-reference/topics/ref/statement/preference-edit-routing-options.html>).
  No vendor documents what a static route at the connected tier resolves to.
  Without this reservation
  a static route at preference 0 ties the connected route covering its prefix,
  joins its candidate set, and the flow hash spreads one destination between
  on-link delivery and a next hop. The normalization is what the README and
  `Normalize`'s doc comment state; it is not a rejection, because 0 is also the
  zero value an unset field carries.
- **A prefix may carry several routes.** `Validate` today refuses a second
  route on one prefix ("The table holds one route per prefix",
  `src/common/netsim/vswitch/routing/config.go:277`, rejecting at `:295`).
  That check is removed and the uniqueness key becomes the whole tuple
  (prefix, preference, metric, next hop, interface), so a genuinely duplicated
  route is still an error. Why: without this, every equal-cost configuration in
  this plan fails construction, and an ECMP set is by definition several routes
  on one prefix. The doc comment on `Validate` changes with it.
- **What survives the tie-break is a candidate set, not one route.** Routes
  that match the destination and are equal on prefix length, preference and
  metric are all kept and all recorded. Matching the destination is part of the
  rule: equal-length prefixes that do not contain it are not candidates.
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
  trace records" (`docs/architecture/2026-09-10-virtual-device-direction.md:171`),
  and it would fail intermittently rather than reproducibly.
- **`Diff` re-keys on (prefix, next hop, interface), and preference and metric
  stay field changes.** `diff.go:272` builds `map[netip.Prefix]Route`, which
  silently drops all but one route per prefix. The key gains the next hop and
  the interface so an added or removed ECMP member is visible, and the
  per-field arms at `diff.go:316` become `preference` and `metric`. Why: keying
  on the whole tuple including preference and metric would make a metric change
  a key change, so the `rInA && !rInB` and `!rInA && rInB` arms at `diff.go:299`
  would fire and one edit would read as a removal plus an addition. R9 requires
  one change, and the two fields an operator most often retunes are exactly the
  two that would lose their field arm. The `next_hop` and `interface` arms go
  with the same reasoning applied the other way: once the key holds both, that
  arm only ever compares two routes agreeing on them, so retuning either reads
  as a removal plus an addition — which is what changing an ECMP member is.
- **`Route.Canonical` and the VRF snapshot carry the new fields.** `Route` is
  itself a `trace.Fact` (`config.go:112`), and `snapshotVRF` (`diff.go:116`)
  encodes a whole VRF. Why: two routes differing only in preference would
  otherwise produce identical facts, breaking the injective-fact contract the
  direction record states at
  `docs/architecture/2026-09-10-virtual-device-direction.md:275`.

### Flow hash

- **A candidate set resolves by hashing layer 3 only, with FNV-1a.** The hash
  is `hash/fnv`'s `fnv.New32a`, fed the source address bytes, then the
  destination address bytes, and for IPv6 the 20-bit flow label in big-endian
  order. No protocol octet and no ports. Why: an unconfigured router hashes
  layer 3. Cisco CEF's default per-destination load sharing takes "the source
  and destination IP addresses" with no transport fields
  (<https://www.cisco.com/c/en/us/support/docs/ip/express-forwarding-cef/18285-loadbal-cef.html>),
  Linux's default `fib_multipath_hash_policy=0` is L3-only with ports entering
  only at policy 1, and RFC 2991 section 3 warns that "including
  transport-layer information in the next-hop selection process can actually be
  problematic" (<https://datatracker.ietf.org/doc/html/rfc2991>). A five-tuple
  would model a box someone had configured, and netsim has no field saying
  they did. `fnv.New32a` is what `lag/hash.go:37` already uses, so one
  algorithm covers both selections; the input differs, not the function. The
  bond hash additionally folds in both MAC addresses and the EtherType, so it
  answers a different question and is not reused.
- **The flow label enters the IPv6 hash; the extension header chain does not.**
  `ip.Decode` does not walk the chain (`src/common/net/ip/ip.go:182`), so
  `Header.Protocol` is the fixed header's first Next Header — which is why the
  protocol octet is not hashed at all rather than hashed with an extension
  header's number standing in for it. The flow label is in the fixed header
  (`src/common/net/ip/ip.go:29`, decoded at `:187`) and is what RFC 6437
  designed for this: "A specific goal is to enable and encourage the use of the
  flow label for various forms of stateless load distribution, especially
  across Equal Cost Multi-Path (ECMP) and/or Link Aggregation Group (LAG)
  paths", precisely because the transport fields "may be unavailable due to
  either fragmentation or encryption, or locating them past a chain of IPv6
  extension headers may be inefficient"
  (<https://www.rfc-editor.org/rfc/rfc6437.txt>). Linux's default IPv6
  multipath hash includes the flow label for the same reason.
- **Fragments need no rule of their own.** Only addresses and the flow label
  enter the hash, so every fragment of one datagram lands on one next hop
  without a fragment test. Why: this is the property RFC 2991 section 2 asks
  for — it lists variable path MTU, reordering that "can cause packets to
  always arrive out of order" and trip fast-retransmit, and the difficulty of
  debugging as the reasons to keep a flow on one path. Reassembly is not among
  them, and a five-tuple hash would have had to buy this property back with a
  fragment test.
- **The reduction is RFC 2992 hash-threshold, not modulo-N.** The 32-bit hash
  space is divided into `len(candidates)` contiguous regions of equal width in
  canonical order, and the region containing the hash names the next hop. Why:
  RFC 2992 measures modulo-N as "the most disruptive of the algorithms; if a
  next-hop is added or removed the disruption is (N-1)/N", against "between 1/4
  and 1/2" for hash-threshold (<https://www.rfc-editor.org/rfc/rfc2992.txt>).
  A withdrawn or added route changes `len(candidates)`, and under modulo-N
  nearly every flow in the VRF would move, a wrong answer to "which flows move
  when this path fails", which is the planning question this phase exists to
  answer. Linux reduces by cumulative upper bounds and Cisco by a 16-bucket
  load-share table; hash-threshold is the published form of the same idea.
- **The hash is a pure function of the packet.** `Peek` and `Forward` agree with
  no commit flag, and phase 3's commit discipline does not extend here. No
  bucket table with remembered members: `lag`'s buckets persist a flow across a
  member's return, and hash-threshold over a table rebuilt whole by `Derive`
  gives the disruption bound without state to keep.
- **`Originate` selects the same way.** It performs its own table scan
  (`layer.go:578`) and would otherwise take an undefined member of a candidate
  set. It hashes the chosen source address and the destination, which is the
  whole input, so it needs nothing from the caller's payload. Why: one
  selection rule per package.
- **The candidate set is capped at 64.** A prefix with more than 64 equal
  routes installs the first 64 in canonical order and the rest are withdrawn
  with reason `max-paths`. Why: real gear caps multipath — FRR "is generally
  compiled with a limit of 64 way ECMP"
  (<https://docs.frrouting.org/en/latest/zebra.html>) — and an uncapped set
  would be netsim inventing the one behavior no router has. 64 is FRR's number
  and the README says so.

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
  (`src/common/netsim/vswitch/routing/config.go:321`, rejecting at `:335`). A
  recursive static route is by definition one whose next hop is not on-link, so
  that check and this phase are mutually exclusive. It becomes the resolver's
  `unresolved` withdrawal reason. The check already applies only to a route
  that names no `Interface` — the explicit-interface branch at `config.go:313`
  skips it — so what the phase removes is narrower than the file reads,
  and the interface-outside-VRF rejection beside it is untouched.
- **An unreachable host gateway in `fabric` becomes a withdrawn default route.**
  `fabric.Config.Validate` builds a synthetic host routing config and delegates
  to `routing.Config.Validate`
  (`src/common/netsim/fabric/config.go:787`, over the default route built at
  `:278`), so the on-link check is what rejects a host whose gateway is off its
  own subnet today (`src/common/netsim/fabric/config_test.go:593`). After the
  removal that configuration constructs, its default route is withdrawn as
  `unresolved`, and an off-link send from that host answers `no-route` rather
  than failing construction. Why: one rule for one config type. The host has no
  route to its gateway, which is the same fact the construction error carried,
  reported where every other unreachable next hop is now reported. The
  `fabric` case flips from `wantError: true` to a forwarding assertion in U2.
- **An unresolvable route is not installed, and that is a definite answer.**
  It is not a construction error, because the configuration stays valid on real
  gear, and not `Incomplete`, because a real router definitely would not install
  it either. Forwarding answers from the table that remains: a less specific
  route, or `no-route`. Why: `Incomplete` is reserved for an input netsim does
  not have, and here it has every input it needs. This is consistent with the
  result-contract axes at
  `docs/architecture/2026-09-10-virtual-device-direction.md:153`.
- **A route self-recurses when resolution reaches the route being resolved.**
  Not when its next hop falls inside its own prefix: `10.0.0.0/8 via 10.0.1.1`
  with `10.0.1.0/24` connected resolves in one step through the connected route
  and installs, which is an ordinary configuration. Why: the Cisco text quoted
  above conditions validity on the route not self-recursing, and a prefix
  containment test is a different and stricter question. The resolver carries
  the set of routes visited and stops when the next lookup returns one already
  in it.
- **The recursion bound is a documented constant of 8, not a configuration
  field.** Cisco names "the maximum IPv6 forwarding recursion depth" without a
  value. BIRD is the one implementation that publishes a depth, and it is one
  level: "the route obtained by the lookup must not be recursive itself, to
  prevent mutually recursive routes"
  (<https://github.com/CZ-NIC/bird/blob/master/doc/bird.sgml>). FRR publishes
  no number and bounds recursion by the self-match test instead. Eight is
  netsim's own, chosen above BIRD's one so an ordinary two-step chain resolves,
  and the README says whose number it is. Eight is netsim's own and says so,
  the way the lowest-name active-backup tie-break documents itself as netsim's
  stand-in for the Open vSwitch hash-map walk; a real static chain is one or
  two hops, so the bound is a guard against a configuration mistake rather than
  a limit an operator meets. Value user-confirmed 2026-09-15.
- **A route that is not installed is recorded with the chain walked.**
  `Layer.WithdrawnRoutes(vrf string) []WithdrawnRoute` returns
  `WithdrawnRoute{Prefix, NextHop, Interface, Reason, Chain []netip.Prefix}`,
  sorted by prefix then next hop. `Reason` is one of `self-recursive`,
  `depth-exceeded`, `unresolved`, `max-paths`. Why: "why is my static route not
  being used" is the operator question this decision creates, and a route that
  silently vanishes answers it worse than today's construction error does.
- **Resolution replaces the route's forwarding next hop, not only its egress.**
  An installed route carries the on-link `(next hop, interface)` pair that
  resolution reached, while `Route.NextHop` keeps the configured value for
  `Canonical`, `Diff`, and the fact. Why: the neighbor lookup keys on the
  route's next hop, not on its egress interface
  (`src/common/netsim/vswitch/routing/layer.go:459`, and again in `Originate`
  at `:606`). Resolving only the egress would send `10.0.0.0/8 via 192.0.2.1`
  out `ge-0` and then look for a neighbor entry for `192.0.2.1` on `ge-0`,
  which by construction is not there, so every recursive route would install
  and then drop `neighbor-miss`.
- **A recursive route inherits the candidate set that resolved it.** When the
  next hop resolves to a route that itself has several candidates, the
  recursive route carries all of them, each with its own resolved on-link pair.
  Why: this is why a BGP next hop spreads over an ECMP IGP path, and collapsing
  to one would invent an order.
- **A next hop does not resolve through the default route.** A route whose next
  hop matches only `0.0.0.0/0` or `::/0` is withdrawn as `unresolved`. Why: FRR
  states "Nexthop tracking doesn't resolve nexthops via the default route by
  default" and puts it behind `ip nht resolve-via-default`
  (<https://docs.frrouting.org/en/latest/zebra.html>). Without the rule every
  unresolvable next hop in a VRF carrying a default route would silently
  resolve through it, which is the opposite of the definite answer this phase
  is for. netsim does not model the knob.
- **Resolution walks the table netsim has, which is netsim's simplification.**
  FRR resolves a next hop only against the selected, installed route for the
  matching prefix and only for route types with recursion enabled
  (<https://github.com/FRRouting/frr/blob/master/zebra/zebra_nhg.c>). In a
  static-only table with one selection rule those gates coincide with walking
  the table, so the phase does not model them; the README says this is a
  simplification that a dynamic protocol would end.
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
2. **R19b:** Routes matching the destination and equal on all three form a
   candidate set, and every member is recorded in canonical order.
   **Acceptance example:** three `/8` routes at preference 1 and metric 10 give
   a lookup whose fact names all three next hops, ordered by next hop. Which
   one carries the packet is R19c's flow hash, which lands in U4; until it
   does, the lookup takes the first candidate in canonical order, and U1's test
   asserts that interim rule rather than a hash.
3. **R19c:** A candidate set resolves by an RFC 2992 hash-threshold over a
   layer-3 hash, stably and without mutation. **Acceptance example:** two
   candidates; a spread of source and destination addresses reaches both next
   hops; two flows differing only in source port take the same next hop,
   because ports do not enter the hash; one flow repeated ten times takes the
   same next hop every time; `Peek` and `Forward` agree, and two `Peek` calls
   before a `Forward` do not change it. Removing one candidate from a
   three-candidate set leaves every flow that was not on the removed candidate
   where it was, which modulo-N would not.
4. **R19d:** Recursion resolves at table build, bounded at 8, and a failure
   withdraws the route rather than dropping the packet.
   **Acceptance example:** `10.0.0.0/8 via 192.0.2.1`, where `192.0.2.0/24 via
   198.51.100.1` and `198.51.100.0/24` is connected on `ge-0`, installs with
   egress `ge-0` and forwarding next hop `198.51.100.1` — a configuration
   today's `Validate` rejects — and a packet to `10.1.1.1` leaves `ge-0` with
   the destination MAC of the `198.51.100.1` neighbor, not a `neighbor-miss`.
   `10.0.0.0/8 via 10.0.1.1` with `10.0.1.0/24` connected also installs: its
   next hop lies inside its own prefix but resolution does not return to it.
   `10.0.0.0/8 via 10.0.0.1` with nothing more specific covering `10.0.0.1`
   self-recurses and is not installed. `A via B` and `B via A` installs
   neither. A chain of 9 is not installed where a chain of 8 is. In each
   failing case a packet matching a less specific installed route takes that
   route and the result is `Complete`.
5. **R19e:** A recursive route inherits the candidate set that resolved it.
   **Acceptance example:** the next hop resolves to a two-candidate set; the
   recursive route's lookup names both.
6. **R19f:** A withdrawn route is reachable with its reason and chain.
   **Acceptance example:** `WithdrawnRoutes("default")` returns one entry with
   reason `self-recursive` and the prefixes visited, and two withdrawals sort
   by prefix then next hop. A next hop matching only the default route is
   withdrawn as `unresolved`, and the 65th equal route on one prefix as
   `max-paths`.
7. **R22:** Validation rejects a cross-family next hop and an unusable egress
   reference. **Acceptance example:** an IPv4 prefix with an IPv6 next hop is
   rejected with the field path `vrfs.default.routes.10.0.0.0/8.next_hop`; a
   route naming an `Interface` absent from the VRF keeps today's rejection; a
   next hop that is unspecified or multicast is rejected.
8. **R9:** `Preference` and `Metric` extend normalization, `Clone`, `Canonical`,
   and `Diff`. **Acceptance example:** one static route whose `Metric` changes
   from 10 to 20 gives exactly one `Diff` change, on field `metric`; the same
   for `preference`; a `Preference` of 0 on a static route normalizes to 1 and
   gives none against an explicit 1, which is one test and not two, because a
   `uint8` cannot distinguish unset from an explicit 0; adding a
   second next hop on a prefix that already has one gives one added-route
   change, not a modification of the first.
9. **R37:** Identical input gives an identical table, selection, and trace.
   **Acceptance example:** two separately constructed layers over a
   configuration with three routes on one prefix and one withdrawal produce
   equal `WithdrawnRoutes` and equal facts, including the candidate order, for
   the same packet, over ten repetitions. The installed table itself is not
   asserted directly: `Layer` exports no view of it
   (`src/common/netsim/vswitch/routing/layer.go:253` onward) and this phase
   does not add one, because the facts and the withdrawals already distinguish
   every ordering this requirement is about.
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
- A configurable hash input. Real gear has the knob (Cisco `ip cef
  load-sharing full`, Linux `fib_multipath_hash_policy`); netsim models the
  default only, and the README says which default it is.
- `resolve-via-default` as a configuration field, and the selected-route and
  recursion-enabled gates FRR applies to resolution.
- Giving `fabric` a rule of its own for host gateways. U2 changes what an
  off-link gateway does; it does not add a `fabric`-side check to preserve the
  old error.
- Walking the IPv6 extension header chain in `ip.Decode`.
- The fragmentation defect in the bond hash. `inspectTCPHashInput`
  (`src/common/netsim/vswitch/lag/hash.go:74`) reads the first four payload
  octets as ports whenever the protocol is 6 or 17, without checking
  `V4.FragmentOffset`, so a non-first fragment hashes datagram bytes as a port
  pair. The routing hash reads no ports and so cannot repeat it, but `lag`
  still has it, and fixing it is a separate change against a package this phase
  does not touch. A bond legitimately hashes layer 4 where a router does not:
  802.3ad hashing is the operator's choice per bond, and phase 3 landed it.
- Re-resolving recursion when the table changes at runtime. `Derive` rebuilds
  the routing layer from the normalized configuration on every call
  (`src/common/netsim/vswitch/derive.go:29`), so resolution re-runs whole and
  no stale table survives.

## Units

### U1. Preference, metric, the candidate set, and the keys that follow

Files: `src/common/netsim/vswitch/routing/config.go`, `config_test.go`,
`diff.go`, `layer.go`, `layer_test.go`, `fact.go`
After: none
Change: `Route` gains `Preference uint8` and `Metric uint32`. `Normalize`
raises a static route's preference of 0 to 1 and sorts routes by the total
order (prefix, preference, metric, next hop, interface); connected routes are
built at preference 0 and metric 0 (`layer.go:195`). `Validate` drops the
one-route-per-prefix check (`config.go:277`, rejecting at `:295`) and keys
uniqueness on the whole tuple; its doc comment changes with it. `routing.Result`
gains an exported `Candidates []Candidate` and the exported `Candidate` type,
so the seam in U5 has somewhere to read from. The table lookup returns every
route that contains the destination and equals the winner on prefix length,
preference and metric, in canonical order, and takes the first until U4 adds
the hash. `Route.Canonical` and `snapshotVRF` (`diff.go:99`, routes loop at
`:116`) encode the two new fields. `Diff` keys its route maps on (prefix, next
hop, interface), and its field arms at `diff.go:316` become `preference` and
`metric`, the `next_hop` and `interface` arms being unreachable under that
key. The `New` doc comment at `layer.go:143` states
the new order; its three current clauses about connected routes listed first
and a static route winning nothing all become false.
Tests: the R19a and R19b examples; connected beats static through preference,
not through `kind`, including against a static route configured at preference
0; an equal-length prefix that does not contain the destination is not a
candidate; a single-candidate lookup keeps today's shape; two routes identical
in every field are still rejected, while `config_test.go:339` ("duplicate route
prefix within VRF") differs in next hop and flips to `wantErr: false`; the R9
examples; `Canonical` distinguishes two routes differing only in preference;
`Diff` reports an added ECMP member as one added route.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/routing`

### U2. Recursive next-hop resolution at table build

Files: `src/common/netsim/vswitch/routing/layer.go`, `config.go`,
`config_test.go`, `layer_test.go`, `fact.go`,
`src/common/netsim/fabric/config.go`, `src/common/netsim/fabric/config_test.go`
After: U1
Change: `Validate` drops the on-link next-hop requirement in the
no-interface branch at `config.go:321`. Building a VRF resolves each static
route to an on-link `(next hop, interface)` pair by walking its own VRF's
table, to a depth of at most 8. A route that self-recurses, exceeds the bound,
or reaches no connected interface is not installed; the configuration stays
valid. `Layer.WithdrawnRoutes` returns the withdrawals as the Decisions
describe. A recursive route carries the candidate set of the route that
resolved it, capped at 64 members with the rest withdrawn as `max-paths`. A
next hop matching only a default route does not resolve. The construct-time
next-hop scan at `layer.go:204` is replaced by this pass. In `fabric`, a host
whose gateway is off-link now constructs and carries a withdrawn default route.
Tests: the R19d, R19e, and R19f examples, including the destination-MAC
assertion that proves the forwarding next hop was rewritten; a packet falling
through to a less specific route after a withdrawal, asserted `Complete`; the
R37 example; a next hop that is not on-link and does resolve is accepted where
`Validate` used to reject it; `layer_test.go:726`, which today asserts
`Validate` rejects a next hop reachable only in another VRF, becomes a
withdrawal assertion, and the resolver walking only its own VRF's table is what
keeps that isolation; `fabric/config_test.go:593` ("host with IP stack gateway
unreachable") stops asserting a construction error and asserts the host's send
answers `no-route`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/routing src/common/netsim/fabric`

### U3. Cross-family and next-hop validation

Files: `src/common/netsim/vswitch/routing/config.go`, `config_test.go`
After: U2
Change: `Validate` rejects a route whose next hop family differs from its
prefix family, and a next hop that is unspecified or multicast. Each error
carries the field path and structured attributes, in the style of the errors
already in the file. The existing interface-outside-VRF rejection at
`config.go:313` stays.
Tests: the R22 example, one case per rejection; an IPv6 route on an interface
that also carries IPv4 is accepted, so the check is on the route, not the
interface.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/routing`

### U4. Hash-threshold selection across a candidate set

Files: `src/common/netsim/vswitch/routing/layer.go`, `hash.go` (new),
`hash_test.go` (new), `layer_test.go`, `fact.go`,
`src/common/netsim/vswitch/routing/README.md` (new)
After: U2
Change: a lookup yielding more than one candidate hashes the source address,
the destination address, and for IPv6 the flow label with `fnv.New32a`, then
picks the candidate whose RFC 2992 hash-threshold region contains the hash.
`Originate` (`layer.go:526`, scanning the table at `:578`) uses the same
selection; its source and destination are the whole hash input, so it reads
nothing from the caller's payload. The route fact names every candidate, the
chosen one, and the fields that entered the hash. The README is new: the
ordering rule and that metric compares within one preference, the preference-0
reservation, the candidate set and its cap of 64, the hash input in the byte
order fed to it, the reduction and the disruption bound it buys, that the hash
input is the vendor default and not a configurable one, that `Preference`
cannot express a Junos preference, the recursion bound as netsim's own against
BIRD's one level, that a next hop does not resolve through the default route,
that walking the whole table is netsim's simplification of FRR's selected-route
gate, what a withdrawn route means, and that a withdrawn route and a
`neighbor-miss` are different failures, with a working example.
Tests: the R19c example; a three-candidate set over a spread of addresses
reaches every candidate; two flows differing only in port land together, and
two IPv6 flows differing only in flow label separate; the disruption property,
where removing one candidate of three leaves the other two candidates' flows in
place, which modulo-N would not; a first and a non-first fragment of one
datagram take the same next hop with no fragment test in the code; a TCP
segment behind an IPv6 extension header hashes the same as one without, since
the protocol octet does not enter; `Originate` over a candidate set is stable.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/routing`

### U5. Switch seam, corpus cases, and documentation

Files: `src/common/netsim/vswitch/switch.go`, `switch_test.go`,
`src/common/netsim/vswitch/README.md`,
`src/common/netsim/internal/netsimtest/cases.go`, `corpus_test.go`,
`src/common/netsim/internal/netsimtest/README.md`,
`src/common/netsim/README.md`,
`docs/architecture/2026-09-10-virtual-device-direction.md`,
`docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md`
After: U3, U4
Change: the candidate set reaches a journey through the route trace fact U4
already emits, on the trace `bridge.Result` embeds
(`src/common/netsim/vswitch/bridge/result.go:63`). `assembleRouteResult`
(`switch.go:1289`, called from `:806` and `:849`) is checked to confirm the
fact survives into the forwarding result on both the VLAN-interface path
(`switch.go:1344`) and the routed-port path, and gains nothing otherwise. No
field is added to `bridge.Result`: `bridge` imports no capability package, so a
`[]routing.Candidate` there would invert the dependency, and the VLAN branch
builds its result inside `bridge` where the switch cannot set one afterwards.
If the fact does not survive, the carrier is a `vswitch.ForwardResult` field
(`switch.go:128`) populated in `wrapResult`, not a `bridge` change. The two R39
cases are
registered as switch-only cases shaped like
`CasePlanningLAGMemberFaultKeepsSurvivingFlows`
(`src/common/netsim/internal/netsimtest/cases.go:1336`), which builds a
`vswitch.ConstructionSpec` directly — not
`CaseTroubleshootingUnicastForwarding` (`:372`), whose `netmodel.Load` fixture
produces no static routes
(`src/common/netsim/vswitch/netmodel/routing_test.go:269`). They are registered
in a `RegisterRoutingCases` function beside `RegisterLAGMulticastCases`
(`cases.go:1907`) and called from `DefaultRegistry`; the case-count assertion at
`corpus_test.go:579` goes from 13 to 15; their IDs go into the hardcoded
per-use-case lists at `corpus_test.go:590` and the case catalogue in
`netsimtest/README.md:14`. The direction record gains a routing subsection next
to the one phase 3 added; its remaining-gap list at `:351` is left alone, since
its only routing clause is "dynamic IP routing (BGP and OSPF)" (`:361`), which
this phase does not touch. The parent plan gains the route-selection and
recursion rows its protocol-addition table requires before phase 4
(`docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md:75`,
table at `:77`), each with the question, the current false answer, the minimum
semantics, the unsupported boundary, and the golden case; the capability table
row at `:42` keeps its neighbor clause for phase 4b.
`src/common/netsim/vswitch/README.md:141` describes the routing pipeline as
"longest-prefix route lookup" and is rewritten for preference, the candidate
set, the hash, and withdrawal.
Tests: the two corpus cases, each re-executed deterministically; a
`Switch.Forward` whose routed egress names the candidate set; the corpus
count and per-use-case lists.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim docs/architecture/2026-09-10-virtual-device-direction.md docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md`

## Verification

```bash
go test -race ./src/common/net/... ./src/common/netsim/...
go vet ./src/common/net/... ./src/common/netsim/...
.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim \
  src/common/netsim/fabric \
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
  The known dependents are `Normalize`'s comparator, `Diff`, and `fabric`'s
  delegated host validation, all claimed by U1 and U2. If U1's test run shows
  another, that is the stop condition above: report it rather than restoring
  the invariant behind a flag.
- Reserving preference 0 for connected routes means an operator cannot express
  a static route that competes with a connected one, which Cisco's CLI appears
  to allow even though no Cisco document describes the result. The reservation
  is reversible in one place — `Normalize`'s floor — and the README says
  it is netsim's rule, so a later phase can lift it if a real configuration
  needs it.
