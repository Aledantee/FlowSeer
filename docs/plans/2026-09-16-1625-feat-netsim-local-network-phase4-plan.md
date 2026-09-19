---
title: Local Network Analysis Phase 4 - The Filter Capability and Its Schema - Plan
type: feat
date: 2026-09-19
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
amends: docs/architecture/2026-08-20-network-model-structure-direction.md
parent: docs/plans/2026-09-16-1625-feat-netsim-local-network-plan.md
---

# Local Network Analysis Phase 4 - The Filter Capability and Its Schema - Plan

> Re-planned 2026-09-19 against the tree the earlier phases and the protobuf
> layout refactor left. The external prerequisite
> `docs/plans/2026-09-17-1141-refactor-proto-layout-phase1-plan.md` has landed:
> `spec/proto/flowseer/net/` is now cut by kind, the README and imports gates
> exist (`test/conformance/proto/layout_test.go`), and the network-model
> structure record is amended. Phases 1 through 3 are ancestors of `HEAD`
> (`7698b1a6`, `edd97001`, `95b45193`), and no `filter` package or schema
> exists yet. The line numbers below are re-pinned to the current
> `switch.go`, which phases 4b, 4c, and the sub-interface work moved since the
> first draft.

## Goal

The virtual switch filters routed traffic by interface-bound rule sets: a
binding decides accept, drop, or reject on the first matching rule and
otherwise on the set's default, a stateful set lets a reply through by matching
the reversed 5-tuple against the set at the packet's other side, a rule change
shows up as a divergence in a planning comparison, and the rules load from the
network model. The means: a `src/common/netsim/vswitch/filter` package shaped
like `routing`, two ingress hooks and one egress hook in the switch's routed
path, three corpus cases, and a `flowseer.net.filter.v1` schema package whose
bindings ride as a `FilterFacet` on the `Interface` primitive.

This plan is wrong if the reverse-match rule cannot be evaluated from the frame
and the configuration alone: if statefulness needs a connection table rather
than the reversed-tuple check the direction record fixes, the phase is a
different design and stops here.

## Decisions

The parent's decisions on the filter, first match, statefulness, and `reject`
apply
(`docs/architecture/2026-09-16-local-network-analysis-direction.md:28-43`). In
addition:

### Go configuration and evaluation

- Config: `filter.Config{Sets map[string]RuleSet, Bindings []Binding}`;
  `RuleSet{Stateful bool, Default Action, Rules []Rule}`;
  `Rule{Name string, Match Match, Action Action}`;
  `Match{Protocol *uint8, Src, Dst []netip.Prefix, SrcPorts, DstPorts []PortRange,
  ICMP *ICMPMatch{Type uint8, Code *uint8}, TCPFlags *FlagMatch{Mask, Value tcp.Flags}}`;
  `Binding{Interface string, Direction Direction, Set string}`. Empty match
  slices match anything; `Action` is `Accept`, `Drop`, or `Reject`;
  `Direction` is `In` or `Out`. Why: this is RFC 8519's field set narrowed to
  a local firewall rule, and `tcp.Flags` and the port and prefix types already
  exist from phases 1 and 2. The Go `Bindings` slice stays flat regardless of
  how the schema carries a binding, so `netmodel` is the only unit the schema
  shape reaches.
- Evaluation order, ingress binding on interface `I` with resolved egress `E`:
  1. Evaluate `I`'s set own rules, first match. A matching `Accept` lets the
     frame proceed to routing; a matching `Drop` or `Reject`, or a
     stateless set falling to a non-accept default, is terminal and needs no
     route.
  2. When the own rules do not accept and the set is stateful, the decision is
     deferred past `routing.Route`: with `E` known, reverse the 5-tuple and ask
     whether the set bound `In` on `E` accepts it. Accept when it does, naming
     that set's forward rule as an input fact; otherwise apply `I`'s own
     non-accept outcome. A frame with no route is dropped by routing before
     this check, since `E` does not exist.

  The egress binding on `E` evaluates in `assembleRouteResult` after `E` is
  known and applies to forwarded frames only; its own reverse-match, when the
  set is stateful and its rules do not accept, tests the set bound `Out` on the
  frame's ingress interface — the same direction as the egress binding, on the
  interface at the packet's other side. Why: this is the direction record's
  reverse match
  (`...direction.md:34-41`), and the switch originates no frame carrying an
  ingress interface, so packets to the switch itself take the ingress binding
  but never the egress one.
- A stateful exchange needs both sets stateful, and the receiving set must fall
  through to its default. For the parent R9 reply (arriving on `vlan20`, routed
  to `vlan10`): `srv-in` bound `vlan20 in` is stateful with default `Drop` and
  no rule the reply matches, so its own rules do not accept and the decision
  defers; the reverse-match then consults the set bound `In` on the egress
  `vlan10`, `lan-in`, which is stateful and carries the forward `Accept` rule
  that the reversed tuple `10.0.10.7:40000 -> 10.0.20.5:443` matches, so the
  reply forwards and the trace names `lan-in`'s rule. Why: the parent's R9
  example marks only `lan-in` stateful and writes `srv-in` as `deny any`, which
  a matching rule would make terminal before any reverse-match; this phase
  states the coherent setup rather than inheriting that wording, which is the
  parent's to correct.
- Trace: layer `port.LayerFilter` (`"filter"`); rule IDs `filter.accept`,
  `filter.drop`, `filter.reject`, `filter.default`, and `filter.state`;
  reasons `filter-drop` and `filter-reject`; facts `filter.rule_decision`
  (set, rule name or index, action, direction, interface) and `filter.match`
  (the 5-tuple snapshot). The layer emits `trace.Step` values
  (`src/common/netsim/trace/trace.go:129-137`) as `routing` does
  (`src/common/netsim/vswitch/routing/layer.go:767-774`), and consults a
  `FieldScope` keyed by interface and direction on every evaluation, including
  a set whose rules do not match, so the metadata tests see the scope the
  decision depended on (`routing/layer.go:22-25`, `:130-140`).

### Switch wiring

- The capability constant `LayerFilter trace.Layer = "filter"` joins the block
  in `src/common/netsim/vswitch/port/port.go:15-51`. The `filter` package
  holds a `Layer` type built by `filter.New(cfg, ports, nodeID)`, registered
  in `newSwitch` beside `routing` and `traffic`
  (`src/common/netsim/vswitch/switch.go:397-414`) and held in a
  `filter *filter.Layer` field.
- Ingress hook, routed-port path: inside `forward`, after
  `s.routing.Owns(ifaceName, f)` confirms ownership
  (`src/common/netsim/vswitch/switch.go:1234-1235`) and before
  `s.routing.Route` (`:1259`). Ingress hook, VLAN-interface path: after
  `s.routing.Owns(iface, f)` (`:1298`) and before `s.routing.Route` (`:1302`).
  Egress hook: in `assembleRouteResult`, after `egressIface` is resolved
  (`:1921-1922`) and before the transmit calls `s.bridge.Egress` (`:1939`) and
  `s.ports.Transmit` (`:1956`). Why: these are the points where the interface
  is known on each side; the pre-route ingress hook returns either a terminal
  drop or a deferral the egress point resolves.
- The multicast flooding in `Switch.Resolve`
  (`src/common/netsim/vswitch/switch.go:1648-1692`, the link-scope early
  returns at `:1667-1678` and the flood decision at `:1687-1689`) is unchanged;
  the filter sits on the routed path, not the resolver.
- A `Validate` cross-check in `vswitch.Config` requires `Routing` to be
  present when `Filter` is, and every binding's interface to name a routing
  interface. Why: a filter binds routed interfaces and has no meaning without
  them.
- `vswitch.Diff` gains a nil-guarded `filter.Diff(a, b)` block beside the
  other layers (`src/common/netsim/vswitch/diff.go:70-145`), and
  `diffCapabilities` (`:150-198`) flips `filter` presence once `port.LayerFilter`
  exists; `filter.Diff` mirrors `traffic.Diff`
  (`src/common/netsim/vswitch/traffic/diff.go`), emitting one `trace.Change`
  per differing rule or binding keyed by set name and rule name or index. A
  rule change that flips a forwarding outcome then surfaces through the
  existing `vswitch.Compare`/`CompareResults` as a `Difference{Observable:
  "outcome"}` with no new comparison code (`src/common/netsim/vswitch/compare.go:45-100`).

### Schema

- Package `flowseer.net.filter.v1` under `spec/proto/flowseer/net/filter/v1/`
  holds `FilterRuleSet` (`name`, `stateful`, `default` action, repeated
  `FilterRule`), `FilterRule` (`name`, `FilterMatch`, `FilterAction`),
  `FilterMatch`, `FilterAction` enum, `FilterDirection` enum, and `FilterFacet`
  (`in_set`, `out_set` naming rule sets), plus a README. `FilterMatch` reuses
  the `net/packet` atoms rather than restating them: `IpProtocol`
  (`spec/proto/flowseer/net/packet/v1/ip_protocol.proto:23-54`),
  `TransportPortMatch` (`transport_port.proto:33-42`) for source and
  destination ports, `IcmpMatch` (`icmp.proto:103`), and `TcpFlagsMatch`
  (`tcp_flags.proto:58-86`); prefixes are `flowseer.net.addr.v1.IpPrefix`
  (`spec/proto/flowseer/net/addr/v1/ip.proto:138`).
- A binding rides as a `FilterFacet` embedded on the `Interface` primitive at
  a new field, beside `ip = 20`
  (`spec/proto/flowseer/net/interface/v1/interface.proto:24-89`), so
  `net/interface/v1` imports `net/filter/v1`. Why: user-directed 2026-09-19;
  a binding is a singular per-interface attribute (one `in`, one `out`), which
  the network-model structure record classifies as a facet embedded by value,
  not a device table
  (`docs/architecture/2026-08-20-network-model-structure-direction.md:206-226`),
  and `IpFacet` is the standing precedent for a per-layer facet whose presence
  carries meaning. The facet names its rule sets by bare string, carrying no
  ref, as `net/` messages name other things by name (`...direction.md:491-501`).
  The rule sets themselves are device-scoped named values `netmodel` reads
  alongside the interfaces.
- `net/filter/v1` imports only `net/packet` and `net/addr`, never
  `net/interface`, so the graph stays acyclic
  (`net/interface -> net/filter -> net/packet`). The new
  `spec/proto/flowseer/net/filter/v1/README.md` carries the `## Boundaries`
  block the layout gate checks, with `Imports:` listing `net/addr, net/packet`
  and `Imported by:` listing `net/interface`, and `net/interface`'s README
  `Imports:` line gains `net/filter`
  (`test/conformance/proto/layout_test.go:119-157`, `:241-306`).
- Two mirrored artifacts carry the new import edge and must move with it. The
  layering gate's hardcoded allowlist gains a row
  `"net/filter": {"net/addr", "net/packet"}` and `"net/filter"` appended to the
  `"net/interface"` row (`test/conformance/proto/layering_test.go:25-39`), and
  the import-order table in the structure record gains
  `{net/addr, net/packet} <- net/filter` and `net/filter` in its `net/interface`
  row (`docs/architecture/2026-08-20-network-model-structure-direction.md:101-121`).
  Why: `layering_test.go` is a second gate distinct from `layout_test.go` and
  fails a schema-bearing package its table does not cover; the record table is
  that allowlist's prose mirror, kept in sync with it.

### netmodel

- `netmodel` accepts `port.LayerFilter` by adding it to the supported-layer
  switch (`src/common/netsim/vswitch/netmodel/netmodel.go:377-390`), reads
  each `Interface`'s `FilterFacet` into a `filter.Binding` and each device
  `FilterRuleSet` into a `filter.RuleSet`, and reports two non-fatal issues
  through the `addSkippedAt` closure (`netmodel.go:316-351`):
  `netmodel.filter.unbound_interface` when a facet names a set on an interface
  with no IP facet, and `netmodel.filter.missing_set` when a facet names a set
  the device does not define. Both downgrade readiness without failing the
  load, as the routing codes do (`netmodel.go:1662`, `:1672`). Why: the loader
  never aborts on a per-item defect; it records it and lets the rest load.

### Resolved carry-overs

- `Match.Src` and `Match.Dst` are prefixes only; a named address-list object
  is out of scope, and a later netmodel change can flatten aliases into
  prefixes. Why: the local network needs no aliases, and a list object is a
  second naming scheme the schema does not otherwise have.
- A binding names one set per direction; there is no `Both` direction. Two
  bindings express both directions. Why: `Both` is a shorthand that the
  reverse-match and the trace would each have to special-case.

## Requirements

Parent R8 to R11 hold on this phase's tree
(`docs/plans/2026-09-16-1625-feat-netsim-local-network-plan.md:147-163`). In
addition:

1. A rule set with no rules and default `Accept` bound to an interface is a
   no-op whose trace still carries a `filter.default` step and consulted
   scope. Example: binding `vlan10 in` to such a set changes no outcome and
   adds exactly one step.
2. A stateless set never consults the counterpart, and a stateful set whose
   own rule matches never consults it either. Example: the R9 reply forwards
   by `lan-in`'s rule only when `srv-in`'s own rules do not accept it; a
   `srv-in` that accepts the reply on its own rule names `srv-in`, not
   `lan-in`.
3. `filter.Diff` reports a rule added, removed, or changed by set name and
   rule name or index, and a binding added or removed by interface and
   direction.
4. `netmodel` reports `netmodel.filter.unbound_interface` on the ownership
   scope of an interface whose facet names a set but which has no IP facet, and
   `netmodel.filter.missing_set` when a facet names a set the device lacks,
   both without failing the load.

## Out of scope

- Connection tracking, TCP window or state validation, fragment handling.
- ICMP error generation for `reject`.
- Filters on bridged (non-routed) traffic; a MAC ACL is a separate capability.
- NAT and any rewrite action.
- A named address-list (alias) object in the match.
- The collector that reads a firewall's rules into the schema.

## Units

### U1. The `filter` Go package
Files: `src/common/netsim/vswitch/filter/config.go`,
`src/common/netsim/vswitch/filter/filter.go`,
`src/common/netsim/vswitch/filter/diff.go`,
`src/common/netsim/vswitch/filter/README.md`, and their `_test.go` files.
After: none
Change: `filter.Config`, `filter.New`, and a `Layer` that evaluates an ingress
or egress binding against a frame, returning a decision (`Accept`, `Drop`,
`Reject`, or a deferral pending the egress interface), the `trace.Step` values,
and the consulted scope. The reverse-match takes the counterpart set and the
reversed 5-tuple. `filter.Diff` compares two `Config` values. No switch
dependency: the package takes the frame, the header, and the binding as
arguments, as `routing` takes them.
Tests: `filter_test.go` proves first-match accept, drop, reject, and default
(parent R8); a stateful set accepting a reversed tuple its counterpart accepts
and naming the forward rule (parent R9, U-local R2); a stateless set never
consulting the counterpart (U-local R2); the empty-set `filter.default` step
(U-local R1). `diff_test.go` proves U-local R3. A drop is asserted a Complete
domain outcome.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/filter`

### U2. Switch wiring
Files: `src/common/netsim/vswitch/port/port.go`,
`src/common/netsim/vswitch/switch.go`,
`src/common/netsim/vswitch/config.go`,
`src/common/netsim/vswitch/diff.go`, and their `_test.go` files.
After: U1
Change: `port.LayerFilter` exists; `newSwitch` builds and holds a
`filter.Layer` when `Config.Filter` is set; the two ingress hooks and the
egress hook call it, a pre-route drop terminates before `routing.Route` and a
deferred stateful ingress resolves in `assembleRouteResult`; `vswitch.Config`
gains `Filter *filter.Config` and the `Validate` cross-check; `vswitch.Diff`
gains the nil-guarded `filter.Diff` block and `diffCapabilities` flips `filter`
presence.
Tests: `switch_test.go` proves an ingress drop is a Complete outcome that skips
routing, an egress binding applies to a forwarded frame only, a stateful reply
forwards end to end through the switch (parent R9), and the `Validate`
cross-check rejects a filter with no routing and a binding to an unknown
interface. A planning `Diff` reports a filter presence change (parent R10 seam).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/switch.go src/common/netsim/vswitch/port/port.go src/common/netsim/vswitch/config.go src/common/netsim/vswitch/diff.go`

### U3. The `flowseer.net.filter.v1` schema and the interface facet
Files: `spec/proto/flowseer/net/filter/v1/filter.proto`,
`spec/proto/flowseer/net/filter/v1/README.md`,
`spec/proto/flowseer/net/interface/v1/interface.proto`,
`spec/proto/flowseer/net/interface/v1/README.md`,
`test/conformance/proto/layering_test.go`,
`docs/architecture/2026-08-20-network-model-structure-direction.md`, and the
`generated/` output of `buf generate`.
After: none
Change: the `filter` schema package exists with `FilterRuleSet`, `FilterRule`,
`FilterMatch`, `FilterAction`, `FilterDirection`, and `FilterFacet` reusing the
`net/packet` atoms and `net/addr.IpPrefix`; `Interface` gains
`flowseer.net.filter.v1.FilterFacet filter = 21`; both READMEs carry the
`## Boundaries` block the layout gate checks; `layering_test.go`'s `importOrder`
allowlist and the record's mirrored import-order table gain the `net/filter`
rows; `buf lint`, `buf generate`, and the breaking-change gate pass.
Tests: `buf lint`; `test/conformance/proto/layout_test.go` (README coverage and
imports) and `test/conformance/proto/layering_test.go` (the import-order
allowlist) both green; protovalidate coverage of the match constraints as the
sibling `net/packet` schemas carry them.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/filter/v1/filter.proto spec/proto/flowseer/net/interface/v1/interface.proto`

### U4. netmodel translation
Files: `src/common/netsim/vswitch/netmodel/netmodel.go` and its `_test.go`
files.
After: U1, U2, U3
Change: `netmodel.Load` gains a `filterSets []*filterv1.FilterRuleSet`
parameter (the rule sets are device-scoped and no message embeds them, so they
enter through the signature beside `ifaces`, `netmodel.go:173-187`), accepts
`port.LayerFilter` in its supported-layer switch (`netmodel.go:377-390`),
translates each `FilterRuleSet` into a `filter.RuleSet` and each interface
`FilterFacet` into `filter.Binding` values onto `cfg.Filter` (`netmodel.go:776`),
and reports `netmodel.filter.unbound_interface` and `netmodel.filter.missing_set`
through `addSkippedAt` without failing the load. `port.LayerFilter` and
`vswitch.Config.Filter`, which U2 adds, are why this unit follows U2. The new
`Load` parameter is a positional signature change; its callers update in U5.
Tests: a facet on an interface with an IP facet loads to the expected
`filter.Config` (parent R11); a facet on an interface with no IP facet raises
`netmodel.filter.unbound_interface` and still loads; a facet naming an absent
set raises `netmodel.filter.missing_set` (U-local R4); a request for
`port.LayerFilter` is accepted rather than skipped.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/netmodel/netmodel.go`

### U5. Corpus cases
Files: `src/common/netsim/internal/netsimtest/filter_cases.go`,
`src/common/netsim/internal/netsimtest/cases.go`,
`src/common/netsim/internal/netsimtest/README.md`.
After: U2, U4
Change: `RegisterFilterCases` registers `planning/filter-rule-change` (parent
R10, built like `CasePlanningPortVLANChange` with `vswitch.Diff` and
`vswitch.Compare`, `cases.go:31-196`), `troubleshooting/filter-drops-mdns-unicast-probe`
(parent R8), and `troubleshooting/stateful-reply-allowed` (parent R9, the
`lan-in`/`srv-in` scenario); `DefaultRegistry` calls it
(`cases.go:2489-2502`); the README's admitted-cases list gains the three
entries. Every `netmodel.Load` call in `netsimtest` gains the new
`filterSets` argument U4 added.
Tests: the three cases pass under the corpus runner, each with exact
`ExpectedOutcome`, `ExpectedReason`, `ExpectedRules`, and `ExpectedFacts`, and
the planning case with `ExpectedChanges` and `ExpectedComparison`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/internal/netsimtest`

Waves: U1 U3 | U2 | U4 | U5

## Verification

```bash
go test -race ./src/common/net/... ./src/common/netsim/...
buf lint && buf generate && go test ./test/conformance/proto/...
.claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>
```

No lab device takes part.

## Definition of done

- [ ] Verifier green for every changed path in every unit.
- [ ] Parent R8 to R11 and this phase's R1 to R4 hold on the landed tree, with
      the three corpus cases admitted and listed in the netsimtest README.
- [ ] `src/common/netsim/vswitch/filter/README.md`, the two schema READMEs, and
      `src/common/netsim/README.md`'s capability list are current.
- [ ] The virtual-device direction record's "Remaining capability gaps" no
      longer lists packet filtering.
- [ ] This plan's `status` is set with an outcome note; the parent's U4
      `Landed:` line carries the commit range; no plan labels appear in code.

## Open questions

None. The schema binding placement, the address-list scope, and the `Both`
direction are settled under Decisions.
