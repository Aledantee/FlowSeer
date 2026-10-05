---
title: Routing, Neighbor Resolution, and Filtering - Plan
type: fix
date: 2026-10-01
artifact_contract: flowseer-plan/v2
execution: code
---

# Routing, Neighbor Resolution, and Filtering - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

`layer/routing` forwards only what its EtherType says is IP, resolves
neighbors as ARP (RFC 826) and Neighbor Discovery (RFC 4861) specify, and
hands a held frame back with what its release needs. `layer/filter` never
accepts a frame it could not classify and never treats two orderings of one
rule as different. Stop condition: if sending solicitations makes the layer
a frame source the switch's outbox cannot carry before phase 9, the
solicitation work moves into phase 9 and this phase keeps the state machine.

## Decisions

- The parent's Decisions apply. The virtual-device record lists "the switch
  never sends a solicitation" and the absence of Delay and Probe states as a
  remaining gap. Under "protocols to standard" this phase closes it.
- A held frame carries an opaque token and its ingress interface. Why: the
  fabric attributes a release to the lowest frame identifier on the device
  (`src/common/netsim/fabric/run.go:1378-1416`) because nothing ties the
  release to its frame, and the switch cannot evaluate an egress filter on
  release without the ingress interface. Phases 9 and 10 consume both.
- `routing.Result` reports the VRF and the pending next-hop address. Why:
  the switch recovers them by parsing a trace subject key
  (`src/common/netsim/vswitch/switch.go:937-991`).
- Each Correctness entry was read, not run.

## Requirements

R1. Every Correctness entry has a test that fails before its fix.

R2. Neighbor entries move through Incomplete, Reachable, Stale, Delay, and
Probe as RFC 4861 section 7.3 describes, and an unresolved IPv4 next hop is
solicited with an ARP request. Example: a frame for an unresolved next hop
makes the layer emit a solicitation, and an entry that stays unanswered
through the retry limit fails and drops its held frames.

R3. A stateful set defers only a decoded IP packet. Example: a frame whose
payload fails IP decoding meets a stateful inbound set and takes the set's
default, not a deferred accept.

R4. `filter.Config.Normalize` yields one form for one meaning. Example: two
rules whose `Src` prefixes are listed in different orders diff as equal.

## Inventory

Paths are as of commit `61775c73`. `R` is
`src/common/netsim/vswitch/routing` and `F` is
`src/common/netsim/vswitch/filter`. Phase 1 moves them under
`src/common/sim/layer/`. No entry has a test in the tree.

### Correctness

1. High. `Route` checks the IP version against the EtherType for IPv4 only
   (`R/layer.go:759`). A frame with a non-IP EtherType whose payload decodes
   as IPv6 is routed and keeps its EtherType.
2. High. `routeKey` omits preference and metric (`R/diff.go:353-358`) while
   `Validate` allows two routes that differ only there
   (`R/config.go:396-407`). `Diff` then overwrites one with the other.
3. High. `extractPacket` ignores the EtherType and returns a zero tuple on a
   decode failure (`F/filter.go:495-536`). A stateful set defers it
   (`F/filter.go:259`), and `ResolveDeferred` (`:315`) can match the zero
   tuple against a wildcard counterpart rule and accept.
4. Struck. `neighborEntry.clone` copies the held slice and shares the
   frames in it (`R/neighbor.go:123-128`). The virtual-device record
   (`:248-252`) requires a fork to share immutable frames, so this is the
   rule and no test is written against it.
5. Medium. `indexRules` falls back to the rule's index as its key
   (`F/diff.go:387-390`). A rule named `"1"` at index 0 collides with an
   unnamed rule at index 1.
6. Medium. `Config.Normalize` copies `Match.Protocol` by pointer
   (`F/config.go:143`), unlike `ICMP.Code` a few lines on.
7. Medium. `Originate` picks the source address from the first interface by
   name before the route lookup (`R/layer.go:967-991`).
8. Medium. A non-override advertisement for a failed entry returns without
   recovering it (`R/neighbor.go:256-263`).
9. Medium. `Validate` accepts the same subnet on two interfaces of one VRF
   (`R/config.go:360-376`).
10. Medium. `Normalize` does not sort `Src`, `Dst`, `SrcPorts`, or
    `DstPorts` (`F/config.go:126-184`), against its doc comment.
11. Low. `filter.Config.Validate` accepts unmasked and IPv4-mapped prefixes
    (`F/config.go:217-232`), which `routing` rejects.
12. Low. `MatchFact` prints `invalid IP` for an undecoded packet
    (`F/filter.go:60-65`).
13. Medium. `RetentionKey` omits the node identity the layer stores
    (`R/layer.go:259,1131-1170`), so a derive that changes only the node
    keeps a layer whose scopes name the old node.

### Completeness

- No solicitation is ever sent and no entry reaches Delay or Probe.
  `R/README.md:28` describes retransmission up to a retry limit, and the
  layer fails an entry at its first expiry.
- `Originate` has no caller outside the package's tests.
- `F/README.md` does not say what a stateful set does with a frame that is
  not IP, or how the default action ranks against stateful evaluation.
- ICMP error generation for a `reject` rule is listed as unmodelled in the
  virtual-device record. Decide at re-planning whether RFC 792 and RFC 4443
  errors belong to this phase.

### Design

- Static neighbors are configured on the VRF and name their interface
  (`R/config.go:175`). Move them under the interface.
- `routing.Result` has no `Status` or `Outcome` method where `filter.Result`
  has both (`F/filter.go:133-138`).
- Filter bindings are a separate slice (`F/config.go:120`) checked against
  routing interfaces in the switch's `Validate`. The local-network record
  attaches a binding to an interface.

### Tests

- `TestFactTypeIDsUnique` (`R/config_test.go:1275`) omits the runtime
  decision facts.
- No test routes a non-IP EtherType, diffs routes that differ by metric
  alone, or evaluates a stateful set on an undecodable frame.

## Open questions

- May a filter apply on a switch without routing? The switch's `Validate`
  requires routing for a filter
  (`src/common/netsim/vswitch/config.go:375`), which makes `netmodel` drop
  filters on a layer 2 switch. Phase 9 decides with this phase's answer.
- Does `routing.Layer.NextWake` report a retained hold timer after a derive?
  Phase 10 depends on it (`src/common/netsim/fabric/fabric.go:843-845`).
