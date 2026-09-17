---
title: Local Network Analysis Phase 4 - The Filter Capability and Its Schema - Plan
type: feat
date: 2026-09-16
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: mixed
parent: docs/plans/2026-09-16-1625-feat-netsim-local-network-plan.md
---

# Local Network Analysis Phase 4 - The Filter Capability and Its Schema - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

> Blocked. This phase requires
> `docs/plans/2026-09-17-1141-refactor-proto-layout-phase1-plan.md` to have
> landed first. That phase cuts `spec/proto/flowseer/` by kind of contract,
> amends the network model structure record this phase's schema sits under,
> and adds the README and imports gates a new package must satisfy. Re-planning
> this phase before it lands would fix a package path, a record, and a set of
> obligations that are all about to change. Nothing enforces the order: the
> plan-status checker resolves only unit-level prerequisites inside one parent,
> so whoever picks this up checks that phase's `status` first.

## Goal

The virtual switch filters routed traffic by interface-bound rule sets, a
stateful set lets replies through, a rule change shows up in a planning
comparison, and the rules load from the network model. The means: a
`src/common/netsim/vswitch/filter` package in the shape of `traffic`, two
hooks in the switch's routed path, corpus cases, and a
`flowseer.net.filter.v1` schema package with its `netmodel` translation.
The plan is wrong if the reverse-match rule cannot be evaluated from the
frame and the configuration alone, which would mean statefulness needs a
connection table and a separate decision.

## Decisions

The parent's decisions on the filter, on first match, on statefulness, and
on `reject` apply. In addition:

- Config: `filter.Config{Sets map[string]RuleSet, Bindings []Binding}`;
  `RuleSet{Stateful bool, Default Action, Rules []Rule}`; `Rule{Name
  string, Match Match, Action Action}`; `Match{Protocol *uint8, Src, Dst
  []netip.Prefix, SrcPorts, DstPorts []PortRange, ICMP *ICMPMatch{Type
  uint8, Code *uint8}, TCPFlags *FlagMatch{Mask, Value tcp.Flags}}`;
  `Binding{Interface string, Direction Direction, Set string}`. Empty
  slices match anything; `Action` is `Accept`, `Drop`, or `Reject`. Why:
  this is RFC 8519's field set narrowed to what a local firewall rule uses,
  and the schema's `packet/v1` primitives already carry the same values.
- Hooks: the ingress binding evaluates after interface ownership and before
  `routing.Route` on both the routed-port path
  (`src/common/netsim/vswitch/switch.go:807-832`) and the VLAN interface
  path (`switch.go:869-875`); the egress binding evaluates in
  `assembleRouteResult` after the egress interface is known
  (`switch.go:1354`) and before the bridge or port transmit. The ingress
  binding applies to every packet entering the interface, packets to the
  switch itself included; the egress binding applies to forwarded packets
  only. Why: this is where the interface is known on both sides, and
  packets the switch originates carry no ingress interface.
- The stateful reverse match runs after the route lookup, so the trace
  shows the lookup, then the filter step naming the forward rule as an
  input fact. A packet with no route is dropped by routing before any
  stateful check, since no counterpart interface exists. Why: the
  counterpart is the egress interface, and only the lookup names it.
- Trace: layer `filter`, rule IDs `filter.accept`, `filter.drop`,
  `filter.reject`, `filter.default`, and `filter.state`; reasons
  `filter-drop` and `filter-reject`; facts `filter.rule_decision`
  (set, rule name or index, action, direction, interface) and
  `filter.match` (the 5-tuple snapshot). Scopes: `FieldScope` under the
  routing VRF scope keyed by interface and direction, consulted on every
  evaluation including a set with no matching rule. Why: the metadata tests
  require every path to consult the scopes it depends on.
- A `Validate` cross-check in `vswitch.Config` requires `Routing` to be
  present and every binding's interface to exist in a VRF; the capability
  constant is `port.LayerFilter`. Why: the filter has no meaning without
  routed interfaces.
- Corpus: `planning/filter-rule-change` (parent R10),
  `troubleshooting/filter-drops-mdns-unicast-probe` (parent R8), and
  `troubleshooting/stateful-reply-allowed` (parent R9).
- Schema: package `flowseer.net.filter.v1` with `FilterRuleSet`,
  `FilterRule`, `FilterMatch` (reusing `packet/v1` `TransportPortMatch`,
  `TcpFlags`, `Icmpv4Fields`, `Icmpv6Fields`, and `IpProtocol`),
  `FilterAction` enum, `FilterBinding` with `FilterDirection` enum, and a
  README. Whether bindings are a facet on `Interface` or device-level rows
  is the parent's second open question, decided when this phase is
  re-planned against the interface triad's state at that time. Why: the
  conventions make a `net/` message a Primitive with no refs, named by the
  interface's bare `name` (`docs/conventions/protobuf.md:162-179`).

## Requirements

Parent R8 to R11, plus:

1. A rule set with no rules and default `Accept` is a no-op binding whose
   trace still carries a `filter.default` step. Example: binding `vlan10
   in` to such a set changes no outcome and adds one step.
2. A binding to a stateless set never consults the counterpart; a stateful
   set with a matching rule of its own never consults the counterpart
   either. Example: the R9 reply with `srv-in` stateful and holding an
   explicit accept names `srv-in`'s rule, not `lan-in`'s.
3. `Diff` reports a rule added, removed, or changed by set name and rule
   index or name, and a binding added or removed by interface and
   direction.
4. `netmodel` reports a binding to an interface without an IP facet as
   `netmodel.filter.unbound_interface` on that interface's ownership scope
   and a rule set a binding names but the model lacks as
   `netmodel.filter.missing_set`, both without failing the load.

## Out of scope

- Connection tracking, TCP window or state validation, fragment handling.
- ICMP error generation for `reject`.
- Filters on bridged (non-routed) traffic; a MAC ACL is a separate
  capability.
- NAT and any rewrite action.
- The collector that reads a firewall's rules.

## Open questions

- Whether `Match.Src` and `Match.Dst` should also take an address list
  object with a name, as OPNsense aliases do. The plan prefers prefixes
  only; a later netmodel change can flatten aliases.
- Whether a binding may name one set for both directions with `Direction`
  `Both`. The plan prefers two bindings.
