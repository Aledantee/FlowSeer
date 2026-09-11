---
title: Network Simulation, Phase 2: Relay Parity - Plan
type: feat
date: 2026-09-11
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: code
parent: docs/plans/2026-09-11-1431-feat-netsim-ovs-parity-and-cable-timing-plan.md
---

# Network Simulation, Phase 2: Relay Parity - Plan

> Implemented.

## Goal

The relay holds a bounded MAC table that evicts the oldest dynamic entry
when full and counts what it learned, expired, evicted, and moved; a static
entry can be added and removed while a run is going; and the VLAN layer
gains the port behaviors a live Open vSwitch exposes: a `dot1q-tunnel` port
that pushes a service tag on ingress and pops it on egress, a priority-tag
policy for untagged egress, flood-only VLANs, protected ports, and a bridge
option that forwards reserved-address frames when no spanning tree runs.
The means is fields on `bridge.Config` and `bridge.Switchport`, two
`Bridge` methods for the entries and the counters, `Switch` and
`fabric.Device` pass-throughs, and the loader's mapping of the tunnel mode
the schema already carries. The phase is wrong if a consumer needs the
eviction order Open vSwitch actually uses (least recently used within the
port holding the most entries) rather than the oldest entry, or needs a
tunnel port that rewrites the customer tag rather than carrying it, or
S-tag classification on ingress.

## Decisions

The parent's Decisions on the bounded table and the VLAN behaviors hold.
This phase settles the shapes the stub left open:

- Counters live on the bridge and are read through `Bridge.Counters()`
  returning `bridge.Counters{Learned, Expired, Evicted, Moved uint64}`,
  `Switch.RelayCounters()` passing it through, and
  `fabric.Device.RelayCounters` carrying it in a snapshot beside the
  per-port `Counters`. Why: the bridge is where every one of the four
  events happens, and `fabric.Device` already carries the entries the
  counters describe; OVS's `fdb/stats-show` reads `total_learned`,
  `total_expired`, `total_evicted`, `total_moved` off the learning table
  (`lib/mac-learning.h` on branch-3.3,
  https://raw.githubusercontent.com/openvswitch/ovs/branch-3.3/lib/mac-learning.h).
- `bridge.Config.MaxEntries int`, 0 unbounded. A learn of a new source that
  would make the dynamic count exceed it evicts the dynamic entry with the
  earliest `LearnedAt`, ties broken by FID then MAC bytes ascending, and
  counts one eviction; static entries never count against the bound and
  are never evicted. A learn that only refreshes or moves an existing entry
  evicts nothing. Why: the parent's Decision says oldest; ties are broken
  in `Entries()` order, so a reader can predict the evicted entry from a
  listing. OVS bounds the
  table with `mac-table-size`
  (https://www.openvswitch.org/support/dist-docs/ovs-vswitchd.conf.db.5.html,
  Bridge `other_config:mac-table-size`, default 8192).
- Counting rules: `Learned` counts a new dynamic entry, whether by a frame or
  by a `Learn` seed; `Expired` counts an entry `Age` removes; `Evicted`
  counts an entry the bound removes; `Moved` counts a refresh whose port
  differs from the entry's, which the trace already reports as "moved".
  Static seeds count nothing. `Derive` reseeds the current dynamic entries
  through `Learn`, so a derived switch starts with `Learned` equal to the
  reseeded count and the new bound evicts from the oldest during the
  reseed; static entries are not reseeded, as today, so a run-time static
  entry does not survive a derive. `Derive`'s doc comment says so. Why:
  OVS counts the same four and a static entry is configuration, not
  learning; a derive is a reconstruction, and its seeds are learns.
- Run-time static entries reuse `Learn` with `Seed.Static` true and gain
  `Bridge.Forget(fid vlan.ID, mac netaddr.MAC) bool`, which removes an
  entry of either kind and reports whether one was there; `Switch.Learn`
  and `Switch.Forget` pass through, and `Fabric.Switch(name)` already
  exposes the switch at run time. Why: `Learn` already accepts static seeds
  (`src/common/netsim/vswitch/bridge/bridge.go`, `Learn`), so one new
  method covers removal, and OVS's `fdb/add` and `fdb/del` are the same
  pair (https://www.openvswitch.org/support/dist-docs/ovs-vswitchd.8.html).
- Protected ports are a bridge set, `bridge.Config.ProtectedPorts
  []string`, not a switchport field. A frame that entered on a protected
  port is never transmitted on another protected port: a known unicast
  whose entry is on one is dropped with `ReasonProtected` ("protected"),
  and a flood records the port in `Result.Egress` with `Dropped`
  `protected` and a drop step, the way `port-blocked` is recorded, so the
  fabric counts the discard. Why: a bridge without VLAN awareness has no
  switchport records, and the rule is a pair property of ports, not a VLAN
  property. OVS carries it as the Port column `protected`, listed on the
  page cited above; the traffic rule is the parent's, from the live
  session.
- `bridge.Config.FloodVLANs []vlan.ID`: in such a VLAN the bridge learns
  nothing on ingress and every lookup is a miss, so every frame floods;
  the lookup is skipped, so a static entry in a flood VLAN never matches.
  Why: OVS
  describes `flood_vlans` as "VLAN IDs of VLANs on which MAC address
  learning should be disabled, so that packets are flooded instead of
  being sent to specific ports" (page cited above), and a lookup that
  could still hit would make "every frame floods" false.
- `bridge.Config.ForwardBPDU bool`: when set, the reserved-address check
  in `Ingress` is skipped and a frame to 01-80-C2-00-00-00 through
  0F is classified, learned from, and flooded like any group destination.
  The switch still intercepts 01-80-C2-00-00-00 for its own spanning tree
  when one runs, before the bridge sees the frame, so the option changes
  nothing for that address on such a switch. Why: OVS `forward-bpdu`
  says "When this option is true, such frames will not be treated
  specially" (page cited above).
- `bridge.Switchport.Tunnel *Tunnel` with `Tunnel{VID vlan.ID,
  CustomerVIDs []vlan.ID, TPID uint16}`; `TPID` 0 means 0x88A8. A
  switchport with `Tunnel` set has no `PVID`, `Tagged`, or `Untagged`
  (`Validate` refuses the combination) and `Admission` is ignored. Ingress
  on a tunnel port classifies every frame to `Tunnel.VID` and keeps all of
  the frame's tags as `RemainingTags`; when `CustomerVIDs` is non-empty
  and the frame's outer tag is a C-tag with a VID not in the list, the
  frame is dropped with `ReasonCustomerVLAN` ("customer-vlan"); an
  untagged or priority-tagged frame passes whatever the list. `Ingress`
  gains `TPID uint16`, the TPID an egress tag for this frame carries:
  `Tunnel.TPID` from a tunnel port and 0 elsewhere, which the codec and
  the egress builder read as 0x8100, so the routed egress in
  `switch.go` that builds an `Ingress` literal needs no change. The S-tag
  carries the ingress frame's outer C-tag PCP and DEI, zero when the frame
  was untagged. Egress toward a port that carries the FID tagged emits the
  tag with `Ingress.TPID` followed by the remaining tags; egress on a
  tunnel port whose `Tunnel.VID` is the FID emits the remaining tags only.
  A tunnel port is a member of its `Tunnel.VID` for flooding, for the
  `not-member` rule, and for the ingress filter. The S-tagged frame this
  produces is not re-classified by any ingress in this phase: an outer tag
  with TPID 0x88A8 is untagged to `Ingress`, as today. The tunnel port is a
  port option on the switch's one c-VLAN component; the direction record's
  component bullet is unchanged, and the trace names the option as an
  Open vSwitch shape rather than an IEEE 802.1Q clause. Why: this is OVS's description, "a dot1q-tunnel port
  treats these as double-tagged with the outer service VLAN tag and the
  inner customer VLAN taken from the 802.1Q header ... to egress on the
  port, a packet outer VLAN (or only VLAN) must be tag, which is removed
  before egress"; `cvlans` "If this is empty, the port includes all
  customer VLANs"; `qinq-ethtype` "The value 802.1ad specifies TPID
  0x88a8, which is also the default"
  (https://raw.githubusercontent.com/openvswitch/ovs/branch-3.3/vswitchd/vswitch.xml).
  The untagged-customer rule is unconfirmed against OVS and repeated under
  Open questions.
- `bridge.Switchport.PriorityTags PriorityTagPolicy`, a string type with
  `PriorityTagsNever` ("Never"), `PriorityTagsIfNonzero` ("IfNonzero"),
  `PriorityTagsAlways` ("Always"), PascalCase like `Admission`; empty
  means never. It applies only where the port carries the FID untagged: a
  C-tag with VID 0 and the ingress PCP and DEI is emitted ahead of the
  remaining tags for `always`, for `if-nonzero` only when the PCP is
  non-zero, and never for `never`. Why: OVS `priority-tags`: never
  "omitting the 802.1Q header entirely if the VLAN ID is zero";
  if-nonzero "omits the 802.1Q header on output if both the VLAN ID and
  priority would be zero"; always "retain the 802.1Q header in such
  frames" (vswitch.xml cited above). OVS keys `if-nonzero` on the priority
  alone once the VID is zero, and DEI rides along with the PCP.
- The loader maps `SWITCHPORT_MODE_DOT1Q_TUNNEL`: the facet's `pvid` is
  `Tunnel.VID`, its `tagged_vlan_ids` are `CustomerVIDs`, `TPID` stays 0
  and the report records the 0x88A8 default; a facet in that mode without
  a `pvid` is skipped with the reason "tunnel without pvid", and its
  `untagged_vlan_ids` are ignored with a `Skipped` entry when non-empty;
  the PVID fallback from a single untagged VLAN does not apply to a tunnel
  facet. The VLAN table gains `Tunnel.VID` and not the customer VLANs.
  Why: the facet has no service-VLAN field and a tunnel port carries one
  VLAN, which is what `pvid` names on an access port; the customer list
  is the only list a tunnel port has. Unconfirmed: a device whose model
  puts the service VLAN elsewhere will need a facet change, out of scope
  here.
- `bridge.Diff` reports `max_entries`, `flood_vlans`, `protected_ports`,
  and `forward_bpdu` as bridge fields and `tunnel` and `priority_tags` as
  switchport fields; `Validate` refuses a negative `MaxEntries`, a flood
  VLAN or tunnel VID outside 1 through 4094, a protected port absent from
  the port table, a tunnel switchport with `PVID`, `Tagged`, or
  `Untagged` set, a customer VID outside 1 through 4094, and an unknown
  priority-tag policy; the bridge-level checks run before `Validate`'s
  early return on a nil `VLAN`, so a plain bridge is checked too. Why: every configuration field the bridge reads is
  diffed and validated today, and a field the diff does not see is a
  change `Derive` cannot report.

## Requirements

Every example uses a VLAN-aware bridge with ports `1/1/1` through `1/1/4`
up, VLAN 10 and VLAN 20 in the table, `1/1/1` and `1/1/2` untagged in
VLAN 10 with PVID 10, `1/1/3` tagged in VLAN 10 and VLAN 20, and `1/1/4`
as the example says; MACs `A` through `D` are distinct unicast addresses.

1. Bounded table evicts. Acceptance: `MaxEntries` 2; frames from `A` on
   `1/1/1`, `B` on `1/1/2`, `C` on `1/1/1` at t0, t0+1s, t0+2s;
   `Entries()` holds `B` and `C`, `Counters()` reads `Learned` 3,
   `Evicted` 1; a fourth frame from `B` on `1/1/2` evicts nothing and
   `Learned` stays 3.
2. Static entries survive aging and the bound. Acceptance: `MaxEntries` 1;
   `Learn` of a static `D` on `1/1/3` in FID 10; a frame from `A` on
   `1/1/1` is learned without eviction; `Age` at t0+301s removes `A`
   (`Expired` 1) and keeps `D`, reported `Static` true; `Forget(10, D)`
   returns true and `Entries()` is empty; a second `Forget` returns false.
3. Moves count. Acceptance: a frame from `A` on `1/1/1` then from `A` on
   `1/1/2` reads `Moved` 1 and `Learned` 1.
4. Tunnel ingress and egress. Acceptance: `1/1/4` is a tunnel port with
   VID 10 and `CustomerVIDs` {100, 200}; a frame with a C-tag VID 100 from
   `A` on `1/1/4` to `B` known on `1/1/3` egresses on `1/1/3` with tags
   [S-tag TPID 0x88A8 VID 10, C-tag VID 100]; the same frame to a unicast
   known on `1/1/1` (untagged in VLAN 10) egresses with tags [C-tag VID
   100]; a frame with a C-tag VID 300 on `1/1/4` is dropped with
   `customer-vlan`; an untagged frame on `1/1/4` is classified to VLAN 10
   and egresses on `1/1/3` with tags [S-tag VID 10]; a frame from `1/1/3`
   with tags [C-tag VID 10, C-tag VID 100] to `A` known on `1/1/4`
   egresses on `1/1/4` with tags [C-tag VID 100]. The S-tagged frames
   are not re-admitted by any ingress in this phase.
5. Priority-tag policy. Acceptance: `1/1/2` carries VLAN 10 untagged; a
   frame from `1/1/3` with a C-tag VID 10 PCP 5 to a MAC known on `1/1/2`
   egresses with tags [C-tag VID 0 PCP 5] under `Always` and
   `IfNonzero`, and with no tags under `Never` and under an empty policy;
   with PCP 0 it egresses with [C-tag VID 0 PCP 0] under `Always` only.
6. Flood-only VLAN. Acceptance: `FloodVLANs` {10}; a frame from `A` on
   `1/1/1` to `B` learns nothing (`Entries()` empty, `Learned` 0) and
   floods to `1/1/2` and `1/1/3`; a second frame from `B` on `1/1/2` to
   `A` floods too, with the lookup step reading "flood vlan".
7. Protected ports. Acceptance: `ProtectedPorts` {`1/1/1`, `1/1/2`}; a
   known unicast from `1/1/1` to `B` on `1/1/2` is dropped with
   `protected`; a flood from `1/1/1` reaches `1/1/3` and lists `1/1/2` in
   `Result.Egress` with `Dropped` `protected`; a
   known unicast from `1/1/1` to a MAC on `1/1/3` is forwarded; the same
   from `1/1/3` to `B` on `1/1/2` is forwarded.
8. BPDU pass-through. Acceptance: a bridge with `ForwardBPDU` true and no
   spanning tree floods a frame to 01-80-C2-00-00-00 from `1/1/1` to
   `1/1/2` and `1/1/3` and learns its source; with `ForwardBPDU` false it
   is dropped with `reserved-address`; a switch with spanning tree and
   `ForwardBPDU` true still consumes a BPDU on 01-80-C2-00-00-00 and
   floods a frame to 01-80-C2-00-00-0E.
9. Diff and Validate. Acceptance: `Diff` of a bridge whose `MaxEntries`
   goes 0 to 2 reports field `max_entries` 0 to 2; of a switchport whose
   `Tunnel` goes nil to {VID 10} reports field `tunnel`; `Validate`
   refuses a tunnel switchport with `PVID` set, a `FloodVLANs` entry 0, a
   protected port `1/1/9` absent from the table, and `PriorityTags`
   "Sometimes"; the first three refusals hold on a bridge whose `VLAN` is
   nil.
10. Loader. Acceptance: an interface facet with mode
    `SWITCHPORT_MODE_DOT1Q_TUNNEL`, `pvid` 10, `tagged_vlan_ids` {100}
    loads as a switchport with `Tunnel{VID 10, CustomerVIDs [100]}` and
    the report records default `qinq_ethtype` 0x88A8, and the VLAN table
    gains 10 and not 100; the same facet without `pvid` is skipped with
    "tunnel without pvid", also when it carries one untagged VLAN.
11. Snapshot. Acceptance: after requirement 1's frames run through a
    fabric, `Snapshot().Devices["sw1"].RelayCounters` reads `Learned` 3,
    `Evicted` 1.

## Out of scope

Everything the parent lists; OVS's per-port fairness eviction (the bound
evicts the oldest entry); customer-tag rewriting on a tunnel port; a
`vlan_mode` field on the switchport, since the membership lists and the
tunnel already say the mode; export of the relay counters to the network
model; a facet field for the service TPID; S-tag (0x88A8) classification
on ingress, so a provider trunk that re-classifies a service tag is a
later phase.

## Units

### U1. Bounded table, counters, and run-time entries

Files: `src/common/netsim/vswitch/bridge/config.go`, `bridge.go`,
`fdb.go`, `diff.go`, `bridge_test.go`, `src/common/netsim/vswitch/switch.go`,
`switch_test.go`, `derive.go`
After: none
Change: `Config.MaxEntries`; `Counters` and `Bridge.Counters()`;
eviction on learn as the Decisions say, counting in `Ingress`'s learn
branch, `Age`, and `Learn`; `Bridge.Forget`; `Switch.Learn`,
`Switch.Forget`, `Switch.RelayCounters`; `Diff` field `max_entries`;
`Validate` refuses a negative bound before its nil-`VLAN` return;
`Derive`'s comment states the reseed rule. The trace's learn step reads
"evicted <mac> <port>" for the entry the bound removed, appended after the
learn step.
Tests: `bridge_test.go`, requirements 1 through 3, each watched failing
first by leaving the bound unenforced; `switch_test.go`, `Learn` then
`Forget` through the switch and `RelayCounters` after one learn. Each is
evidence for this unit.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch`

### U2. Flood VLANs, protected ports, and BPDU pass-through

Files: `src/common/netsim/vswitch/bridge/config.go`, `bridge.go`,
`result.go`, `diff.go`, `bridge_test.go`, `src/common/netsim/vswitch/switch_test.go`
After: U1
Change: `Config.FloodVLANs`, `Config.ProtectedPorts`, `Config.ForwardBPDU`;
the ingress learn and the egress lookup skip a flood VLAN with a lookup
step "flood vlan"; `ReasonProtected` and the protected rule on the unicast
hit and in the flood loop; the reserved-address check gated by
`ForwardBPDU`; `Diff` fields `flood_vlans`, `protected_ports`,
`forward_bpdu`; `Validate` covers the three.
Tests: `bridge_test.go`, requirements 6 through 8's bridge cases;
`switch_test.go`, requirement 8's spanning-tree switch. Each is evidence
for this unit.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch`

### U3. Tunnel ports and the priority-tag policy

Files: `src/common/netsim/vswitch/bridge/config.go`, `bridge.go`,
`result.go`, `diff.go`, `bridge_test.go`
After: U2
Change: `Tunnel`, `Switchport.Tunnel`, `PriorityTagPolicy`,
`Switchport.PriorityTags`, `Ingress.TPID`, `ReasonCustomerVLAN`, the
tunnel ingress classification and the customer check, the egress rules
in `buildEgressFrame` for a tunnel port, a tagged member, and an untagged
member under each policy, membership of a tunnel port in its VID for the
flood and `not-member` rules; `Diff` fields `tunnel` and `priority_tags`;
`Validate` covers both. `Switchport.Clone` copies the tunnel.
Tests: `bridge_test.go`, requirements 4, 5, and 9. Each is evidence for
this unit.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch`

### U4. Loader, snapshot, and records

Files: `src/common/netsim/vswitch/netmodel/netmodel.go`, `netmodel_test.go`,
`report.go`, `src/common/netsim/fabric/run.go`, `run_test.go`,
`src/common/netsim/vswitch/README.md`, `src/common/netsim/fabric/README.md`
After: U3
Change: the loader's tunnel mapping of the Decisions;
`fabric.Device.RelayCounters` filled from `Switch.RelayCounters`; the
vswitch README's capability ladder, rules, loader defaults, and drop
reasons carry the bound, the counters, the four VLAN behaviors,
`qinq_ethtype` (source: vswitch.xml `qinq-ethtype`, 802.1ad default
0x88A8), `customer-vlan`, and `protected`; its "Reserved group addresses"
rule and its protocol-schedule sentence "A switch without the layer drops
it as a reserved address" gain the `ForwardBPDU` exception; the fabric
README's snapshot section names `RelayCounters`.
Tests: `netmodel_test.go`, requirement 10; `run_test.go`, requirement 11.
Each is evidence for this unit.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim`

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim
go test -race ./src/common/netsim/...
```

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] The vswitch README and the fabric README match the landed API.
- [ ] This plan's `status` set with an outcome note under its title, and
      the parent's `Landed:` line for this phase filled.
- [ ] No plan labels in code, comments, or commit messages.

## Open questions

- Whether an untagged customer frame on a tunnel port with a non-empty
  `CustomerVIDs` passes. The plan says yes, since the frame has no customer
  VLAN to check; OVS's text does not say. Unconfirmed.
- Whether the loader's use of `pvid` as the service VLAN holds for every
  device the model will carry. Unconfirmed; the first device dossier that
  says otherwise changes the facet, not this mapping.
