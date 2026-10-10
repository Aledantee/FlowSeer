---
title: Exact Diff Attribution - Plan
type: refactor
date: 2026-10-10
artifact_contract: flowseer-plan/v2
execution: code
---

# Exact Diff Attribution - Plan

## Goal

Every configuration leaf has an independently specified diff attribution,
and the perturbation gate checks the complete expected changes. The means
is a mapping, an exact matcher, and bounded migrations of the twelve diff
owners. The current matcher accepts any nonempty result
(`src/common/sim/internal/simtest/diffcoverage.go:490`).

Stop condition: a field grammar or lifecycle name has several source-compatible
outputs. Readiness stays `needs-decisions` until those outputs are chosen.
The units below are a blocked migration outline, not an implementation handoff.

## Decisions

- The parent's Decisions apply.
- The gate proves exact attribution in this parent, not in a later plan.
  A uniform mapping takes each configuration leaf to its diff subject and
  field, and the capability `Diff` fields follow it. Cost: trace and diff
  consumers see changed fields (decided by the user, 2026-10-10).
- Attribution is a phase of its own, run before the close-out phase, so
  the close-out documents the final diff fields. Why: with the close-out's
  nine units it exceeds one bounded phase (decided by the user,
  2026-10-10).
- Fields are granular. Existing subject kinds and keys stay. A change to
  one member of an aggregate gets its own field, a capability that appears
  or disappears gets an explicit presence field, and an identity change
  reports a removed and an added record. Cost: coarse or empty fields
  change, and their tests and corpus consumers change with them. Keeping
  aggregate fields and proving the leaf through complete before and after
  facts was the alternative (decided by the user, 2026-10-10).

- Compare effective values after each owner's normalization. Why: the gate
  normalizes both sides at `src/common/sim/internal/simtest/diffcoverage.go:490`,
  and every owner normalizes inside `Diff`. LAG's zero-environment boundary
  is explicit at `src/common/sim/layer/lag/README.md:153`. Filter's masked,
  sorted lists have an empty diff at `src/common/sim/layer/filter/README.md:64`.
  A submitted change erased by normalization expects no records. A presence
  flag that changes an effective priority expects the `priority` record,
  even when the submitted numeric priority stays zero
  (`src/common/sim/layer/stp/config.go:152`, `:205`).
- Preserve established granular names. Why: `lacp_system_priority` and
  `recovery.duration` are already consumer-visible fields
  (`src/common/sim/layer/lag/diff.go:288`,
  `src/common/sim/layer/loopprotect/diff.go:134`). Those examples establish
  two naming patterns, so neither settles the new aggregate grammar.
- Preserve each owner's deterministic output order. Why: corpus assertions
  compare changes by position (`src/common/sim/internal/simtest/corpus.go:890`).
  The perturbation matcher also compares the exact expected sequence. A
  reordered equivalent result fails rather than silently changing this contract.
- Keep `trace.Change`, `Subject`, and `CompositeKey` unchanged. Why: they
  already carry the layer, kind, key, field, and typed facts
  (`src/common/sim/trace/trace.go:61`, `:89`, `:327`). The renderer quotes
  the whole field without parsing it (`src/common/sim/trace/render.go:114`).

## Requirements

Proposed R1, blocked by Q3. For each perturbed leaf, compare the independently expected sequence of
layer, subject kind, subject key, field, and typed From/To facts. No record
outside that expectation is accepted. Example: a wrong field with the right
subject fails, as does an unrelated record that masks an omitted arm.
Identity changes require the complete removal/addition pair from the fixed
Decision. Normalization can produce an empty sequence or dependent effective
changes. The one-record wording of the stub needs the resolution in Q3.

R2. Discover structural paths from configuration types as well as concrete
paths from seeds. A nil capability or an unseeded optional branch fails.
Example: clearing `vswitch.Config.Filter` in the coverage seed fails even
when the other eight capabilities remain populated. The existing top-level
check is at `src/common/sim/device/vswitch/diff_coverage_test.go:114`.

R3. Count leaves with concrete expectations per owner, including identity
leaves. Each unmapped, unseeded, unperturbable, or intentionally nonsemantic
leaf has its full path, reason, and disposition in the result. None is
silently skipped. Example: a new exported filter match member fails until
its seed, mutation, and exact expectation exist. The current walker skips
nil pointers at `src/common/sim/internal/simtest/diffcoverage.go:182`.

R4. Each owner migration removes its own temporary allowance and keeps all
focused tests green. The exact matcher never falls back to a nonempty check
for an owner already migrated. Example: after bridge migrates, removing its
`tunnel` member arm fails even while the LAG owner is still allowed.

R5. Preserve existing retention checks and whole-copy behavior. Example:
perturbing the switch MAC preserves its port table, and deleting the MAC
arm still fails. Sources: `src/common/sim/internal/simtest/diffcoverage_test.go:71`
and `src/common/sim/internal/simtest/diffcoverage.go:586`.

## Out of scope

Runtime forwarding, protocol behavior, retention identity, and comparison
locations are unchanged. The checker reads trusted repository-authored
configuration types and seeds. Its purpose is missing or misattributed
coverage, not hostile input handling. This planning change edits only this
plan and its state. Future units name the direction-record amendment.
No parent or direction-record edit is part of this planning change.

## Attribution inventory and mapping

Snapshot: `4944caed`. The drafted inventory is recoverable from
`9ee39543:docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase12-plan.md`,
section Attribution split inventory. The current twelve owners match the
literal list at `src/common/sim/internal/simtest/diffcoverage.go:26`.

In the table, `S` is `src/common/sim`, `K(...)` is `trace.CompositeKey(...)`,
`n` is a port or device name, `v` a VLAN, `r` a VRF, and `i` an instance.
Fields separated by commas correspond to the configuration members in the
same order. `Q1` blocks a new aggregate member grammar. `Q2` blocks the
capability presence field. Empty lifecycle fields are the current output,
not a proposed universal presence spelling. Map-key identities use their
existing subject keys. This table records current fields and fixed scalar
mappings. The Q rows do not yet have concrete target expectations.

| Configuration leaf or family | Subject kind | Key | Current field and target disposition | Source on this tree |
| --- | --- | --- | --- | --- |
| bridge AgingTime, MaxEntries, ForwardBPDU | bridge | empty | aging_time, max_entries, forward_bpdu | S/layer/bridge/diff.go:423, :440, :479 |
| bridge FloodVLANs, ProtectedPorts members | bridge | empty | flood_vlans, protected_ports. Split members after Q1 | S/layer/bridge/diff.go:453, :466 |
| bridge VLAN pointer | bridge | empty | vlan_awareness today. Presence spelling awaits Q2 | S/layer/bridge/diff.go:144 |
| bridge VLAN.Table[v] value and key identity | vlan | decimal v | name. Key replacement emits removed/added records, currently empty field | S/layer/bridge/diff.go:186, :205, :223 |
| bridge Switchports[n].PVID, IngressFiltering, Admission, PriorityTags | port | n | pvid, ingress_filtering, frame_admission, priority_tags | S/layer/bridge/diff.go:311, :350, :363, :409 |
| bridge Switchports[n].Tagged, Untagged members | port | n | tagged_vlan_ids, untagged_vlan_ids. Split after Q1 | S/layer/bridge/diff.go:324, :337 |
| bridge Tunnel.VID, CustomerVIDs members, TPID | port | n | tunnel snapshot. Split after Q1. TPID compares its defaulted value | S/layer/bridge/diff.go:369, :396. S/layer/bridge/config.go:291 |
| phy Ethernet[n].Setting.SpeedBPS, Duplex, AutoNegotiation | port | n | speed_bps, duplex, auto_negotiation_enabled | S/layer/phy/diff.go:149, :158, :167 |
| phy Ethernet[n].SupportedSpeedsBPS members, AutoNegotiationSupported | port | n | supported_speeds_bps needs Q1. auto_negotiation_supported remains | S/layer/phy/diff.go:176, :185 |
| phy Ethernet[n].Observed.SpeedBPS, Duplex | port | n | observed_speed_bps, observed_duplex | S/layer/phy/diff.go:194, :203 |
| phy PoE.Groups[n].PowerNanowatts | pse_group | n | power_nanowatts | S/layer/phy/diff.go:241, :251 |
| phy PoE.Ports[n].Group, MaxClass, Enabled, Limit, Priority, PD, PDClass | port | n | group, max_class, enabled, power_limit_nanowatts, priority, pd, pd_class | S/layer/phy/diff.go:273, :284, :293, :302, :311, :320, :329, :338 |
| loopprotect Interval | bridge | empty | interval | S/layer/loopprotect/diff.go:69, :75 |
| loopprotect Ports[n].Action, Recovery.Mode, Recovery.Duration | port | n | action, recovery.mode, recovery.duration | S/layer/loopprotect/diff.go:122, :128, :134 |
| loopprotect Ports[n].VLANs members | port | n | vlans. Split after Q1 | S/layer/loopprotect/diff.go:140 |
| mcast VLANs[v].FloodUnregistered, FastLeave | vlan | decimal v | flood_unregistered, fast_leave | S/layer/mcast/diff.go:109, :112, :145 |
| mcast VLANs[v].RouterPorts members | vlan | decimal v | router_ports. Split after Q1 | S/layer/mcast/diff.go:116 |
| mcast MembershipInterval, RouterPortInterval, LastMemberQueryInterval, LastMemberQueryCount | vlan | decimal v | membership_interval, router_port_interval, last_member_query_interval, last_member_query_count | S/layer/mcast/diff.go:119, :122, :125, :129 |
| lag LAGs[n].Mode, Primary, UpDelay, DownDelay, HashBasis, MinLinks, RebalanceInterval | lag | n | mode, primary, up_delay, down_delay, hash_basis, min_links, rebalance_interval | S/layer/lag/diff.go:196, :206, :216, :226, :236, :246, :258 |
| lag LACP.Mode, Fast, SystemPriority, SystemID, Key, Fallback | lag | n | lacp_mode, lacp_fast, lacp_system_priority, lacp_system_id, lacp_key, lacp_fallback | S/layer/lag/diff.go:268, :278, :288, :298, :308, :318 |
| lag Members[m].Priority, Key | lag_member | K(n,m) | priority, key. Map identity changes use lifecycle pair | S/layer/lag/diff.go:327, :344, :354, :389 |
| stp Priority and PriorityPresent, Address, HelloTime, MaxAge, ForwardDelay, TxHoldCount | bridge | empty | priority (effective), address, hello_time, max_age, forward_delay, tx_hold_count | S/layer/stp/diff.go:140, :150, :160, :170, :180, :190 |
| stp Ports[n].Priority and PriorityPresent, PathCost, AdminEdge, PointToPoint, AutoEdge, guards | port | n | priority (effective), admin_path_cost, admin_edge, admin_point_to_point, auto_edge, bpdu_guard, restricted_role, restricted_tcn, loop_guard | S/layer/stp/diff.go:252, :263, :273, :283, :293, :304 |
| stp MST/PVST pointer presence | bridge | empty | mst, pvst boolean facts today. Presence spelling awaits Q2 | S/layer/stp/diff.go:198, :216 |
| stp MST.Name, Revision, MaxHops | bridge | empty | mst.name, mst.revision, mst.max_hops | S/layer/stp/diff.go:343, :347, :353, :359 |
| stp MST.Instances[i].Priority and PriorityPresent, VLANs members | mst_instance | decimal i | priority (effective), vlans. Split VLAN members after Q1 | S/layer/stp/diff.go:403, :407, :413 |
| stp MST.Instances[i].Ports[n].Priority and PriorityPresent, PathCost | mst_instance_port | K(decimal i,n) | priority (effective), path_cost | S/layer/stp/diff.go:420, :432, :438 |
| stp PVST.Trees[v].Priority and PriorityPresent, Ports[n] priority flags and PathCost | pvst_tree, pvst_tree_port | decimal v, K(decimal v,n) | priority (effective), priority (effective), path_cost | S/layer/stp/diff.go:505, :509, :517, :528, :534 |
| filter Sets[s].Stateful, Default | set | s | stateful, default | S/layer/filter/diff.go:164, :210, :219 |
| filter Rules.Name and rule identity | rule | K(s,name), or K(s,empty,index) for unnamed/duplicate names | lifecycle pair, currently empty. Preserve existing matching | S/layer/filter/diff.go:331, :337, :347, :367 |
| filter Rule.Action, Match.Protocol, Src/Dst members, SrcPorts/DstPorts Start/End, ICMP Type/Code, TCPFlags Mask/Value | rule | existing rule key | complete rule snapshot, empty field. Split every changed member after Q1 | S/layer/filter/config.go:41, :53, :74, :89, :100. S/layer/filter/diff.go:353 |
| filter Bindings.Interface, Direction, Set | binding | K(interface,direction) | identity pair for Interface/Direction, set for retained binding | S/layer/filter/diff.go:264, :270, :280, :290 |
| routing ClearRouterFlag | routing | empty | clear_router_flag | S/layer/routing/diff.go:242 |
| routing VRFs[r].Interfaces[n].VLAN, Port, MAC, Prefixes members | interface | K(r,n) | vlan, port, mac, prefixes. Split prefixes after Q1 | S/layer/routing/diff.go:308, :326, :335, :344, :353 |
| routing Routes.Prefix, NextHop, Interface | route | K(r,prefix,nextHop,interface) | removed/added pair, currently empty field | S/layer/routing/diff.go:393, :400, :408 |
| routing Routes.Preference, Metric | route | existing route key | preference, metric | S/layer/routing/diff.go:417, :426 |
| routing Neighbors.Interface, Addr, MAC | neighbor | K(r,interface,addr) | identity pair for Interface/Addr, mac for retained neighbor | S/layer/routing/diff.go:463, :470, :478, :487 |
| routing NeighborPolicy.Mode, ReachableTime, RetransTimer, HoldDepth | neighbor-policy | r | mode, reachable_time, retrans_timer, hold_depth | S/layer/routing/diff.go:495, :500, :509, :518, :527 |
| traffic Mirrors.Name | mirror | name | identity pair, currently empty field | S/layer/traffic/diff.go:178, :185, :276 |
| traffic Mirrors.SelectAll, OutputPort, OutputVLAN, SnapLen | mirror | name | select_all, output_port, output_vlan, snap_len | S/layer/traffic/diff.go:245, :257, :260, :263 |
| traffic Mirrors.SelectSrcPorts, SelectDstPorts, SelectVLANs members | mirror | name | select_src_ports, select_dst_ports, select_vlans. Split after Q1 | S/layer/traffic/diff.go:248, :251, :254 |
| traffic Policers[n].RateBPS, BurstOctets | port | n | rate, burst | S/layer/traffic/diff.go:197, :200, :205 |
| traffic Queues[n].MaxRateBPS[pcp], BufferOctets[pcp] | port | K(n,decimal pcp) | max_rate, buffer_octets | S/layer/traffic/diff.go:214, :220, :226 |
| port Name, IfIndex, Kind, AdminStatus, OperStatus, MTU, LagParent | port | Name | identity pair for Name, ifindex, kind, admin_status, oper_status, mtu, lag_parent | S/port/diff.go:19, :38, :51, :64, :77, :90, :103, :114 |
| vswitch MAC, Ports, capability members | device, delegated subject kinds | empty for device, delegated keys | mac, delegated port and layer fields | S/device/vswitch/diff.go:46, :56, :66, :153 |
| vswitch capability presence | capability | layer name | empty field today. Explicit field awaits Q2 | S/device/vswitch/diff.go:159, :188, :199 |
| assembly Start | fabric | empty | start | S/assembly/diff.go:93 |
| assembly Switches[name] members | delegated kinds | K(name,existing key) | delegated fields | S/assembly/diff.go:511 |
| assembly host Address, VLAN, IP.Addresses members, Gateway, Neighbors[addr] | host | name | address, vlan, addresses (Q1), gateway, neighbors.<addr> | S/assembly/diff.go:280, :308, :344, :674, :690 |
| assembly host Accept.Promiscuous, AllMulticast, Multicast members | host | name | accept.promiscuous, accept.all_multicast, accept.multicast.<mac> | S/assembly/diff.go:184, :187, :191 |
| assembly host Ethernet members | host | name | phy fields, prefixed ethernet. Lists need Q1 | S/assembly/diff.go:169 |
| assembly reflector Address, Ports[name] Ethernet members, Attachments[name] Port/VLAN/Addresses members | reflector | reflector name | address, ports.<name>.<phy field>, attachments.<name>.port/.vlan/.addresses (Q1) | S/assembly/diff.go:124, :132, :225, :233, :243, :248, :743 |
| assembly Cables A/B Node/Port identity | cable | canonical endpoint pair | removed/added pair, currently empty | S/assembly/diff.go:415, :420, :543, :614 |
| assembly cable LengthMeters, Medium, Delay, TopSpeedBPS | cable | canonical endpoint pair | length, medium, delay, top_speed | S/assembly/diff.go:557, :566, :582, :591 |
| assembly cable Fault.Kind, N, Sequence members | cable | canonical endpoint pair | fault snapshot. Split after Q1, using normalizedFault | S/assembly/diff.go:424, :600. S/medium/cable/fault.go:35 |
| assembly Uncabled.Endpoint Node/Port | uncabled | Endpoint.Canonical() | removed/added pair | S/assembly/diff.go:153, :162 |
| assembly PhyAssumption.Medium and Ethernet members | fabric | empty | phy_assumption snapshot. Split after Q1 | S/assembly/diff.go:99 |
| assembly Cable/Uncabled Evidence members | none | none | explicitly nonsemantic, expects no change | S/assembly/diff.go:84. S/assembly/diff_coverage_test.go:87 |
| assembly DiffSpecs switch construction inputs | switch | name | construction_inputs aggregate. Separate input from Config leaf gate | S/assembly/diff.go:24, :63 |

### Coverage accounting

The existing seeded subtest counts below are reproducible with
`go test -json ./src/common/sim/layer/... ./src/common/sim/port ./src/common/sim/device/vswitch ./src/common/sim/assembly -run '^TestDiffCoversEveryConfigField$' -count=1`.
Count `run` events whose test name begins `TestDiffCoversEveryConfigField/`.
They are nonempty-check counts, not exact-attribution counts.

| Owner | Current exercised leaves | Concrete complete exact expectations | Seed source |
| --- | --- | --- | --- |
| bridge | 15 | 0 | S/layer/bridge/diff_coverage_test.go:18 |
| phy | 15 | 0 | S/layer/phy/diff_coverage_test.go:14 |
| loopprotect | 5 | 0 | S/layer/loopprotect/diff_coverage_test.go:17 |
| mcast | 7 | 0 | S/layer/mcast/diff_coverage_test.go:17 |
| lag | 15 | 0 | S/layer/lag/diff_coverage_test.go:17 |
| stp | 31 | 0 | S/layer/stp/diff_coverage_test.go:20 |
| filter | 18 | 0 | S/layer/filter/diff_test.go:160 |
| routing | 17 | 0 | S/layer/routing/diff_coverage_test.go:19 |
| traffic | 12 | 0 | S/layer/traffic/diff_coverage_test.go:15 |
| port | 6 | 0 | S/port/diff_coverage_test.go:16 |
| vswitch | 92 | 0 | S/device/vswitch/diff_coverage_test.go:32 |
| assembly | 33 | 0 | S/assembly/diff_coverage_test.go:33 |

A literal target count cannot be asserted yet. Q1 determines collection
member correspondence, Q2 determines capability expectations, and Q3
settles dependent effective changes. The zero column states what the current
gate proves. Before readiness changes, replace it with concrete expectation
counts for the selected mapping and the expanded type-driven seed manifest.
Current nil branches, empty containers, and map keys are not counted by the
walker (`S/internal/simtest/diffcoverage.go:182`, `:196`, `:205`). Port.Name
is currently exempt at `S/port/diff_coverage_test.go:34`. Routing's
ClearRouterFlag is walked even though omitted from its literal seed.
Assembly currently exempts inactive Fault.Sequence and evidence at
`S/assembly/diff_coverage_test.go:87`. Cover active Sequence in an additional
FaultLoseSequence seed. Evidence gets explicit no-change expectations.

The type manifest must list exported structural paths independently of the
seed walk, bind them to concrete map keys, slice members, and pointer cases,
and reconcile discovered, expected, exercised, and nonsemantic counts.
`port.Table` is opaque to reflection. Reach its seven Port leaves through
`port.NewBuilder`, including Name's lifecycle pair. A mutation strategy
failure names the leaf and fails the gate, as the current mutator does at
`S/internal/simtest/diffcoverage.go:395`. A branch erased by normalization
gets an explicit empty expectation and another seed where it is active.

### Consumer audit

`ChangeExpectation.Matches` compares every field and fact, and AssertCase
checks count and position (`S/internal/simtest/corpus.go:251`, `:890`).
The port VLAN case holds `pvid` and coarse `untagged_vlan_ids` expectations
at `S/internal/simtest/cases.go:74`. The added filter rule holds an empty
field and complete snapshot at `S/internal/simtest/filter_cases.go:475`.
Migrate a changed corpus expectation in its producer unit.

`search` consumes behavioral comparison locations, rather than
configuration `trace.Change.Field` (`S/search/minimize.go:102`,
`S/search/search.go:57`). Its location assertions at `S/search/minimize_test.go:701`
stay unchanged. `netmodel` uses empty LAG and STP diffs as equality checks
(`S/netmodel/load_lag_test.go:389`, `S/netmodel/load_stp_test.go:762`). Its
Default.Field is a separate contract (`S/netmodel/report.go:36`). No field
rewrite is indicated in those packages. `src/edge/simload/report.go:90` projects flow statistics and analysis metadata.
`src/edge/simload/run.go:30` carries transmission inputs, rather than
configuration diff fields. Verify
those dependents after migration. No runtime consumer is authorized to
reinterpret a new field before Q1 and Q2 are settled.

## Units

The six ownership groups retain the proposed split from `9ee39543`. Shared
corpus test ownership puts filter/routing after the simple layers. Each
unit's output contract remains blocked by the Open questions.

### U1. Mapping and exact gate
Files: src/common/sim/internal/simtest/diffcoverage.go, src/common/sim/internal/simtest/diffcoverage_test.go, src/common/sim/internal/simtest/README.md, src/common/sim/layer/bridge/diff_coverage_test.go, src/common/sim/layer/phy/diff_coverage_test.go, src/common/sim/layer/loopprotect/diff_coverage_test.go, src/common/sim/layer/mcast/diff_coverage_test.go, src/common/sim/layer/lag/diff_coverage_test.go, src/common/sim/layer/stp/diff_coverage_test.go, src/common/sim/layer/filter/diff_test.go, src/common/sim/layer/routing/diff_coverage_test.go, src/common/sim/layer/traffic/diff_coverage_test.go, src/common/sim/port/diff_coverage_test.go, src/common/sim/device/vswitch/diff_coverage_test.go, src/common/sim/assembly/diff_coverage_test.go, docs/architecture/2026-10-01-simulation-package-shape-direction.md
After: none
Change: After Q1-Q3 are settled, the type manifest and selected leaf mapping produce independent complete expectations. A temporary allowance names each unmigrated owner in its coverage test, with its current leaf manifest. Exact fixture checks always run. Removing an allowance requires that owner to pass the exact matcher. Replace the direction record's `Diff(a, b Config) []trace.Change` paragraph at line 126 with the selected field grammar, lifecycle rule, and normalized expectation rule. Update the simtest README's perturbation-gate description. No new trace type or cross-layer imports enter the gate.
Tests: In diffcoverage_test.go, leavesOrEmpty/checkLeaf build scalar, nested map/slice, opaque port.Table, normalized no-op, dependency, and identity-pair fixtures. Wrong layer/kind/key/field/fact, a missing arm plus an unrelated record, a duplicate record, reordered equivalent records, half a lifecycle pair, a nil required seed branch, and an unsupported mutation each fail. A new exported fixture leaf fails the type manifest. Existing deep-copy and retention regressions stay. Gate sources: diffcoverage.go:175, :468, :586. These exact fixture failures are independent of the temporary owner allowances.
Verify: `uv run tools/scripts/run.py verify change -- src/common/sim/internal/simtest/diffcoverage.go src/common/sim/internal/simtest/diffcoverage_test.go src/common/sim/internal/simtest/README.md src/common/sim/layer/bridge/diff_coverage_test.go src/common/sim/layer/phy/diff_coverage_test.go src/common/sim/layer/loopprotect/diff_coverage_test.go src/common/sim/layer/mcast/diff_coverage_test.go src/common/sim/layer/lag/diff_coverage_test.go src/common/sim/layer/stp/diff_coverage_test.go src/common/sim/layer/filter/diff_test.go src/common/sim/layer/routing/diff_coverage_test.go src/common/sim/layer/traffic/diff_coverage_test.go src/common/sim/port/diff_coverage_test.go src/common/sim/device/vswitch/diff_coverage_test.go src/common/sim/assembly/diff_coverage_test.go docs/architecture/2026-10-01-simulation-package-shape-direction.md`

### U2. Bridge, PHY, loop protection, and multicast
Files: src/common/sim/layer/bridge/diff.go, src/common/sim/layer/bridge/diff_test.go, src/common/sim/layer/bridge/diff_coverage_test.go, src/common/sim/layer/bridge/README.md, src/common/sim/layer/phy/diff.go, src/common/sim/layer/phy/diff_coverage_test.go, src/common/sim/layer/phy/phy_test.go, src/common/sim/layer/phy/README.md, src/common/sim/layer/loopprotect/diff.go, src/common/sim/layer/loopprotect/diff_coverage_test.go, src/common/sim/layer/loopprotect/config_test.go, src/common/sim/layer/loopprotect/README.md, src/common/sim/layer/mcast/diff.go, src/common/sim/layer/mcast/diff_coverage_test.go, src/common/sim/layer/mcast/config_test.go, src/common/sim/layer/mcast/README.md, src/common/sim/internal/simtest/cases.go, src/common/sim/internal/simtest/corpus_test.go
After: U1
Change: The four owners emit selected granular aggregate-member fields and exact typed leaf facts, then remove their allowances. Existing scalar fields stay. The port VLAN corpus expectation changes in this unit if its untagged member output changes. The source inventory above is the rule for each family. No claimed owner retains a nonempty-only fallback.
Tests: Each current Config seed and Normalize/Diff entry point builds its exact leaf cases. bridge/diff_test.go:209 replaces tunnel snapshot assertions with VID, TPID and individual customer-member assertions. TPID 0 to 34984 is a no-op, and 0 to 33024 reports effective 34984 to 33024. PHY seeds cover Setting and Observed separately and the supported-speed members. loopprotect.Config.Normalize builds both explicit and inherited recovery-duration cases. mcast.Config.Normalize builds router-port replacement. DefaultRegistry().Get("planning/port-vlan-change") and AssertCase prove corpus count, order and facts. Removing one migrated arm while an unrelated record remains fails its gate.
Verify: `uv run tools/scripts/run.py verify change -- src/common/sim/layer/bridge/diff.go src/common/sim/layer/bridge/diff_test.go src/common/sim/layer/bridge/diff_coverage_test.go src/common/sim/layer/bridge/README.md src/common/sim/layer/phy/diff.go src/common/sim/layer/phy/diff_coverage_test.go src/common/sim/layer/phy/phy_test.go src/common/sim/layer/phy/README.md src/common/sim/layer/loopprotect/diff.go src/common/sim/layer/loopprotect/diff_coverage_test.go src/common/sim/layer/loopprotect/config_test.go src/common/sim/layer/loopprotect/README.md src/common/sim/layer/mcast/diff.go src/common/sim/layer/mcast/diff_coverage_test.go src/common/sim/layer/mcast/config_test.go src/common/sim/layer/mcast/README.md src/common/sim/internal/simtest/cases.go src/common/sim/internal/simtest/corpus_test.go`

### U3. LAG and STP
Files: src/common/sim/layer/lag/diff.go, src/common/sim/layer/lag/diff_coverage_test.go, src/common/sim/layer/lag/config_test.go, src/common/sim/layer/lag/diff_injectivity_test.go, src/common/sim/layer/lag/README.md, src/common/sim/layer/stp/diff.go, src/common/sim/layer/stp/diff_coverage_test.go, src/common/sim/layer/stp/config_test.go, src/common/sim/layer/stp/README.md
After: U1
Change: LAG and STP use exact expectations for their existing scalar and identity mappings and selected granular list and presence fields, then remove both allowances. Preserve the already present up_delay, down_delay and admin_point_to_point arms. Keep effective priority handling and existing subject keys. The LAG-member port name is a map identity, not a new field under the lag subject (lag/config.go:93, diff.go:327).
Tests: Config literals plus Config.Normalize(layer.Env{}) and Diff build delay, member rename, MST/PVST, explicit-zero PriorityPresent, and inherited instance/tree priority cases. config_test.go:489 pins admin_point_to_point. Wrappers remove each delay or PointToPoint record and retain an unrelated change, which the exact matcher rejects. lag/diff_injectivity_test.go preserves quoted member keys. STP config_test.go lifecycle and VLAN-list assertions change with Q1/Q2. No existing corpus literal names these newly split fields.
Verify: `uv run tools/scripts/run.py verify change -- src/common/sim/layer/lag/diff.go src/common/sim/layer/lag/diff_coverage_test.go src/common/sim/layer/lag/config_test.go src/common/sim/layer/lag/diff_injectivity_test.go src/common/sim/layer/lag/README.md src/common/sim/layer/stp/diff.go src/common/sim/layer/stp/diff_coverage_test.go src/common/sim/layer/stp/config_test.go src/common/sim/layer/stp/README.md`

### U4. Filter and routing
Files: src/common/sim/layer/filter/diff.go, src/common/sim/layer/filter/diff_test.go, src/common/sim/layer/filter/diff_injectivity_test.go, src/common/sim/layer/filter/README.md, src/common/sim/layer/routing/diff.go, src/common/sim/layer/routing/diff_coverage_test.go, src/common/sim/layer/routing/config_test.go, src/common/sim/layer/routing/diff_internal_test.go, src/common/sim/layer/routing/README.md, src/common/sim/internal/simtest/filter_cases.go, src/common/sim/internal/simtest/corpus_test.go
After: U1 U2
Change: Filter emits a field for each retained rule member instead of its empty whole-rule record. Existing named, unnamed, and duplicate-name subject identities stay. Routing splits interface prefixes and preserves route/neighbor identity pairs. Both owners remove their allowances. Update the filter-rule corpus lifecycle expectation only if the selected lifecycle rule changes it. U2 precedes this unit because both edit corpus_test.go.
Tests: filter.Config literals and Normalize/Diff build protocol, prefixes, range boundaries, ICMP and flag members. Include sorted-range displacement and duplicate range normalization, with independently authored expected correspondence after Q1. diff_injectivity_test.go preserves named/unnamed/duplicate-name keys. routing.Config seeds and Normalize/Diff build interface prefixes, preference/metric, and both halves of route and neighbor identity replacement. A missing half or incorrect retained-member fact fails. DefaultRegistry().Get("planning/filter-rule-change") and AssertCase check the added-rule case's exact structured expectation at filter_cases.go:475.
Verify: `uv run tools/scripts/run.py verify change -- src/common/sim/layer/filter/diff.go src/common/sim/layer/filter/diff_test.go src/common/sim/layer/filter/diff_injectivity_test.go src/common/sim/layer/filter/README.md src/common/sim/layer/routing/diff.go src/common/sim/layer/routing/diff_coverage_test.go src/common/sim/layer/routing/config_test.go src/common/sim/layer/routing/diff_internal_test.go src/common/sim/layer/routing/README.md src/common/sim/internal/simtest/filter_cases.go src/common/sim/internal/simtest/corpus_test.go`

### U5. Traffic, port, and switch
Files: src/common/sim/layer/traffic/diff.go, src/common/sim/layer/traffic/diff_coverage_test.go, src/common/sim/layer/traffic/config_test.go, src/common/sim/layer/traffic/README.md, src/common/sim/port/diff.go, src/common/sim/port/diff_coverage_test.go, src/common/sim/port/port_test.go, src/common/sim/port/README.md, src/common/sim/device/vswitch/diff.go, src/common/sim/device/vswitch/diff_coverage_test.go, src/common/sim/device/vswitch/diff_test.go, src/common/sim/device/vswitch/README.md
After: U2 U3 U4
Change: Traffic selector members use the selected grammar. Port coverage includes Name's removed/added pair through NewBuilder. Switch capability presence uses the selected explicit field and normalized architectural presence, and delegates exact owner mappings. The three owners remove their allowances. The original split omitted port/diff.go. It is included here for any selected lifecycle output change.
Tests: traffic.Config.Normalize/Diff builds mirror rename, selector membership replacement, policer and per-PCP queue inputs. port.NewBuilder builds both tables and the fixed LAG companion, including the currently exempt Name at port/diff_coverage_test.go:34. vswitch.Config.Normalize and Diff build every populated capability and empty Traffic addition/removal. Preserve MAC and delegated-layer tests in diff_test.go. A nil top-level capability seed fails before matching, and an omitted LAG inferred from its port table has independently expected presence. Reordering selectors still has an empty diff (traffic/diff_coverage_test.go:54).
Verify: `uv run tools/scripts/run.py verify change -- src/common/sim/layer/traffic/diff.go src/common/sim/layer/traffic/diff_coverage_test.go src/common/sim/layer/traffic/config_test.go src/common/sim/layer/traffic/README.md src/common/sim/port/diff.go src/common/sim/port/diff_coverage_test.go src/common/sim/port/port_test.go src/common/sim/port/README.md src/common/sim/device/vswitch/diff.go src/common/sim/device/vswitch/diff_coverage_test.go src/common/sim/device/vswitch/diff_test.go src/common/sim/device/vswitch/README.md`

### U6. Assembly and corpus closure
Files: src/common/sim/assembly/diff.go, src/common/sim/assembly/diff_coverage_test.go, src/common/sim/assembly/compare_test.go, src/common/sim/assembly/construction_result_test.go, src/common/sim/assembly/README.md, src/common/sim/internal/simtest/diffcoverage.go, src/common/sim/internal/simtest/diffcoverage_test.go, src/common/sim/internal/simtest/README.md, src/common/sim/internal/simtest/corpus_test.go
After: U5
Change: Assembly uses the selected granular grammar for host and reflector wrappers, fault members, and physical-assumption members. Preserve prefixed switch subject keys and directional fault orientation. Expand optional seeds, list evidence as nonsemantic, and cover active fault Sequence. Remove assembly's allowance and the temporary allowance facility after the last owner passes. Every enumerated diff owner now uses the exact matcher. DiffSpecs construction_inputs remains a separately named contract unless Q1 explicitly includes that input family.
Tests: assembly.Config literals and assembly.Diff build cable endpoint identity pairs, forward/reversed cable fault members, host address and multicast membership, reflector attachments with separators, physical assumptions, and delegated switch leaves. TestDiffDetectsReversedDirectionalFaults at assembly/compare_test.go:476 and snapshot helpers at :605/:627/:659 change atomically with the selected fields. ConstructionSpec plus DiffSpecs keep evidence-only changes empty (construction_result_test.go). DefaultRegistry().Get and AssertCase run both planning comparison cases and the full registry. AssertEveryDiffPackageIsCovered still checks all twelve owners. The type manifest fails when any allowance remains or a leaf has no disposition.
Verify: `uv run tools/scripts/run.py verify change -- src/common/sim/assembly/diff.go src/common/sim/assembly/diff_coverage_test.go src/common/sim/assembly/compare_test.go src/common/sim/assembly/construction_result_test.go src/common/sim/assembly/README.md src/common/sim/internal/simtest/diffcoverage.go src/common/sim/internal/simtest/diffcoverage_test.go src/common/sim/internal/simtest/README.md src/common/sim/internal/simtest/corpus_test.go`

Waves: U1 | U2 U3 | U4 | U5 | U6

## Verification

Each producer runs its package tests before its verifier. The final code
migration verifies `src/common/sim` and `src/edge/simload`, so behavioral
search, model-loading equality checks, corpus comparisons, and simload
still pass. No lint or race invocation covers all of `generated/go/yang`.

For this plan and state:

```sh
uv run tools/scripts/run.py verify check-prose docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase14-plan.md
uv run tools/scripts/run.py verify check-plan-state
uv run tools/scripts/run.py verify change -- docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase14-plan.md docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase14-plan.state.json
```

## Definition of done

- [ ] Q1-Q3 are resolved and complete target mappings replace blocked rows.
- [ ] Literal concrete-expectation counts cover the full type-driven manifest.
- [ ] All six units pass their focused tests and every changed-path verifier.
- [ ] The direction record and owner READMEs state the selected contract.
- [ ] No temporary allowance survives and retention regressions stay green.
- [ ] Record the six-unit implementation outcome with `plan record implemented`.
- [ ] No plan labels enter code, comments, or commit messages.

## Open questions

Q1. Which injective aggregate-member field grammar and collection coordinates
apply? `Field` is an opaque string (`S/trace/trace.go:330`, `:376`), and
`CompositeKey` specifies subject keys only (package-shape direction:126).
Static members already use dots, but dynamic assembly names are concatenated
raw at `S/assembly/diff.go:129`. Both proposed grammars fit the fixed
Decisions and produce different consumer-visible strings. Sorted filter
ranges have Start/End but no persistent identity
(`S/layer/filter/config.go:41`, `:201`). A range whose sort position changes
needs an independently defined correspondence, not its raw seed index.

| Option | Tradeoff | Example proposed diff |
| --- | --- | --- |
| Dotted members with bracketed collection coordinates (recommended) | Distinguishes static names from quoted string keys. Use canonical value for scalar-set identity and normalized position for structured ranges. Existing dynamic assembly fields change. | Attachment edge.1 VLAN: `(fabric, reflector, r1, attachments["edge.1"].vlan): 10 -> 20`. Mirror replacement: `select_src_ports["eth2"]: eth2 -> nil` and `select_src_ports["eth3"]: nil -> eth3`. Range Start: `match.src_ports[0].start: 100 -> 101`. |
| Dotted members with individually quoted dynamic segments | Resembles current dotted paths. Consumers distinguishing key segments need quoting rules. Same value/position coordinate policy as above. | Attachment edge.1 VLAN: `(fabric, reflector, r1, attachments."edge.1".vlan): 10 -> 20`. Mirror replacement: `select_src_ports."eth2": eth2 -> nil` and `select_src_ports."eth3": nil -> eth3`. Range Start: `match.src_ports.0.start: 100 -> 101`. |

Both give a tunnel TPID member the field `tunnel.tpid`. They do not rewrite
established scalar fields such as `lacp_system_priority`. Before readiness,
specify correspondence for insertion, deletion, sorting displacement,
deduplication, and a range member edit in every affected list family.

Q2. What is the explicit capability presence name, including nested
capabilities? Current architectural presence uses `Subject{Kind:
"capability", Key: string(layer)}` with `Field: ""` and nil/layer facts
(`S/device/vswitch/diff.go:181`, `:193`). Nested VLAN awareness and MST/PVST
presence have differently named boolean fields (`S/layer/bridge/diff.go:144`,
`S/layer/stp/diff.go:203`, `:216`). None defines a common suffix.

| Option | Tradeoff | Example proposed diff |
| --- | --- | --- |
| presence and nested .presence (recommended) | Names lifecycle without implying administrative enablement. Nested field strings change. Existing facts stay. | Empty Traffic addition: `(traffic, capability, traffic, presence): nil -> vswitch.layer(traffic)`. MST addition: `(stp, bridge, empty, mst.presence): false -> true`. |
| present and nested .present | Reads as a property. Top-level nil/layer facts and nested boolean facts still have different encodings. | Empty Traffic addition: `(traffic, capability, traffic, present): nil -> vswitch.layer(traffic)`. MST addition: `(stp, bridge, empty, mst.present): false -> true`. |

Removal reverses each example's facts. Presence follows normalized
Capabilities, including LAG inferred from port entries
(`S/device/vswitch/config.go:563`), rather than raw pointer toggles.

Q3. Which gate contract handles normalized no-ops and dependency fan-out?
The stub required a changed leaf's record "and no other record appears".
That universal cardinality cannot hold for submitted configuration leaves:
`S/layer/loopprotect/config.go:108` computes recovery duration as three times
the interval when unset, and `S/layer/loopprotect/diff.go:71`, `:132`
report both values. Neither option suppresses legitimate Diff output.

| Option | Tradeoff | Example expected diff |
| --- | --- | --- |
| Exact expected sequence per submitted perturbation (recommended) | Independently enumerate zero, one, or several normalized changes. R1 becomes an ordered-sequence contract. A dependency oracle must not read Diff's returned records. | Interval 1s -> 2s with Timer duration submitted as 0: `(loopprotect, bridge, empty, interval): 1s -> 2s` plus `(loopprotect, port, p1, recovery.duration): 3s -> 6s`. TPID 0 -> 34984: `[]`. |
| Isolated effective-leaf gate plus separate ordered dependency/no-op cases | Controlled seeds keep each ordinary leaf case singular. Every normalization control and dependency needs separate enumerated coverage. | Interval 1s -> 2s with explicit Duration 3s on both sides: only `(loopprotect, bridge, empty, interval): 1s -> 2s`. Separate submitted Duration 0 case asserts both records above. TPID 0 -> 34984 still expects `[]`. |

The comparison domain itself is settled by the sources in Decisions. The
choice of gate architecture belongs here because both approaches satisfy
that domain and produce different coverage structures. This blocks final
expectation counts as well as implementation readiness.
