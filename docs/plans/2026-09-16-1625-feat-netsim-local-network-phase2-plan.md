---
title: Local Network Analysis Phase 2 - Routed Sub-Interfaces - Plan
type: feat
date: 2026-09-16
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: code
parent: docs/plans/2026-09-16-1625-feat-netsim-local-network-plan.md
---

# Local Network Analysis Phase 2 - Routed Sub-Interfaces - Plan

## Goal

A firewall cabled to a trunk port routes between the VLANs carried on it. The
means: a routed interface may set `Port` and `VLAN` together, the switch picks
the sub-interface by the frame's outer C-TAG and pushes that interface's tag
on the way out, and `netmodel` loads the schema's `Subinterface`. This phase
also closes the two unpinned refusal guards phase 1 left behind. The plan is
wrong if the routed-port path cannot classify without the bridge, which would
mean the parent port has to become a switchport and the bans at
`src/common/netsim/vswitch/config.go:316-335` have to fall.

## Decisions

The parent's decisions on sub-interfaces apply. In addition:

- A routed interface sets `Port` alone (an untagged routed port), `VLAN` alone
  (a bridge VLAN interface), or both (a routed sub-interface); neither is
  still an error. Why: `routing.Interface` already carries both fields
  (`src/common/netsim/vswitch/routing/config.go:22-27`), so this phase deletes
  rules rather than adding a field.
- Four landed sites read `VLAN != 0` as "this is a bridge VLAN interface", and
  each gains `&& Port == ""`: the routing exclusivity check
  (`routing/config.go:222-231`, whose `hasVLAN` also gates the `claimedVLANs`
  block at `:232-243`), the bridge-VLAN-table requirement
  (`src/common/netsim/vswitch/config.go:286-303`), the `byVLAN` index
  (`routing/layer.go:262-264`), and the egress discriminator
  (`switch.go:1356`). A search for `VLAN != 0` and `VLAN == 0` outside tests
  finds these four and the diff snapshot at `routing/diff.go:111` and `:268`,
  which compares both fields already and needs no change. Why: this is the
  whole of the change's risk, and two of the four fail quietly. The
  bridge-table rule rejects the firewall in R2 outright, because a
  sub-interface's VID is in no bridge VLAN table and its parent port has no
  bridge. The `byVLAN` index is silent instead: `newLayer` walks interfaces in
  sorted name order (`routing/layer.go:252-256`) and the last write wins
  (`:262-264`), so a sub-interface named after its bridge sibling would answer
  bridge VLAN lookups. That is the failure
  `docs/solutions/architecture-patterns/validate-and-derive-judge-what-new-builds.md`
  already records for `ByVLAN`.
- A port-level lookup answers two questions, and the miss is not one answer.
  `byPort` becomes `map[string]map[vlan.ID]string`, and
  `ByPortVLAN(port string, vid vlan.ID) (name string, portRouted, matched bool)`
  distinguishes "no routed interface on this port", which must keep falling
  through to the bridge at `switch.go:841`, from "routed port, no interface at
  this VID", which drops. Why: today the `ByPort` miss at `switch.go:783` *is*
  the bridge fall-through, so a two-valued lookup would either drop every
  switchport frame or never drop a tag miss; and the nested map answers both
  questions from one lookup, which a flat (port, VID) key cannot.
- `Layer.ByPort` and `Layer.PortLookupScopes` are deleted rather than kept
  beside the new pair. Why: `byPort` is `map[string]string`
  (`routing/layer.go:202`), so two sub-interfaces on one trunk overwrite each
  other at `:265-267` and `PortLookupScopes` reports one arbitrarily; leaving
  the old pair would keep that reachable, and `AGENTS.md` rules out keeping a
  shape for compatibility.
- `PortVLANLookupScope(nodeID, vrf, port string, vid vlan.ID)` is
  `analysis.FieldScope(VRFScope(nodeID, vrf), "ports", port, strconv.Itoa(int(vid)))`,
  which nests beneath `PortLookupScope`'s
  `FieldScope(VRFScope(nodeID, vrf), "ports", port)` (`routing/layer.go:24-26`).
  The constructor `PortLookupScope` keeps its signature and its three outside
  callers. Why: scope matching is prefix containment
  (`src/common/netsim/analysis/scope.go:142-144`), so a sibling key would
  overlap nothing: a frame arriving for a sub-interface whose load failed
  would take a `Complete` drop with the parent port's issue unattached, and
  the landed `ByPort miss` case at `dependency_metadata_test.go:437-440` would
  stop selecting its issue.
- A sub-interface's VLAN must satisfy `vlan.ID.Valid()`, which is 1 through
  4094 (`src/common/net/vlan/vlan.go:23-25`), and `Validate` rejects one that
  does not. Why: the bridge-table rule bounded a VLAN interface's VID
  indirectly, and this phase removes that rule for a sub-interface, so nothing
  else would stop a hand-written VID of 5000 reaching egress and failing far
  away in `ethernet.Frame.Encode` (`ethernet.go:103-108`). VID 4095 is
  reserved by the schema too (`vlan_tag.proto:21-22`).
- Ingress classification reads the outer tag from `ethernet.Frame.Tags`
  (`src/common/net/ethernet/ethernet.go:83-89`), which `Decode` has already
  peeled, and leaves `routing.Layer.Owns` alone. Why: `Owns` compares the
  destination MAC and the inner EtherType (`routing/layer.go:599-608`), both
  already correct for a tagged frame, so two sub-interfaces sharing one MAC
  are told apart by the VID, which is how the parent's R4 example is written.
- An outer tag matches a sub-interface only when its TPID is zero or the C-TAG
  EtherType, the predicate `framePriority` already uses at `switch.go:1309`;
  any other TPID takes the tag-miss drop. Why: a hand-built
  `vlan.Tag{VID: 10}` leaves TPID zero and `Encode` writes that as a C-TAG
  (`ethernet.go:92-94`), so rejecting zero would break frames the existing
  tests build, while `Decode` peels an S-Tag as readily as a C-TAG
  (`ethernet.go:168`) and provider bridging is out of scope here.
- Egress discriminates on `Port`, not on `VLAN`. When the egress interface's
  `VLAN` is non-zero, one tag carrying that VID, the ingress PCP, the ingress
  DEI, and `uint16(ethernet.EtherTypeDot1Q)` is applied to `routeRes.Frame`
  before the transmit and LAG member checks; a plain routed port, whose
  `VLAN` is zero, keeps leaving its frame untagged, which
  `switch_test.go:4009-4011` pins. Why:
  `switch.go:1356` sends every interface with `VLAN != 0` to the bridge, so a
  sub-interface would be handed to a bridge its parent port is not a member
  of. The port egress record already reports `ingressPCP` (`switch.go:1475`)
  and the bridge path carries `ingressDEI` through (`switch.go:1365`), so
  dropping either would make a sub-interface lose what a VLAN interface on a
  trunk keeps. `Compare` matches egress records by tag struct equality
  (`src/common/netsim/vswitch/compare.go:39-50`), so a zero TPID is
  observable, and so is tagging only the success path: the transmit-refused
  and LAG-no-member records (`switch.go:1392-1408`, `:1431-1447`) carry the
  same frame and must carry the same tag.
- A frame on a routed port whose outer tag names no sub-interface, or that
  arrives untagged where no untagged interface exists, drops with
  `routing.ReasonNotBridged` and a fact naming the port and the VID. Why: the
  port is not a switchport, so there is no bridge to fall back to, and the
  reason already exists (`switch.go:812-819`).
- `netmodel` accepts a `Subinterface` whose `parent` names a physical or LAG
  interface in the same load and whose `encapsulation` is exactly one tag with
  TPID `ETHER_TYPE_DOT1Q` whose VID satisfies `vlan.ID.Valid()`; any other
  encapsulation, VID 0 and VID 4095 included, raises a new
  `netmodel.routing.unsupported_encapsulation` on the parent port's lookup
  scope. Why: every `VlanTag` carries a required `tpid`
  (`spec/proto/flowseer/net/switching/v1/vlan_tag.proto:16-20`), so a tag with
  no TPID is not representable; and VID 0 means priority-tagged
  (`vlan_tag.proto:21-26`), which would load as `VLAN: 0`, collide on the
  (port, 0) key with a genuine untagged routed interface, and model something
  this phase does not.
- A `Subinterface` contributes no port-table entry and does set
  `hasRoutedIface`. Why: `netmodel.go:381-398` would otherwise invent a port
  named `eth1.10` of kind `Other`, which `vswitch/config.go:340-359` then
  requires a bridgeless router to route, and nothing routes it. The flag is
  separate: `hasRoutedIface` implies `port.LayerVlan` on the inferred path
  (`netmodel.go:559-562`) and both `LayerVlan` and `LayerRelay` on the wanted
  path (`:581-590`), so a sub-interface-only firewall reports the same
  capabilities a VLAN-interface router would. U2 asserts the capability list
  rather than leaving that implicit.
- A `Subinterface` whose parent is absent from the load, or is itself a VLAN
  interface, a sub-interface, or another kind, keeps raising
  `netmodel.routing.unsupported_interface_kind` (`netmodel.go:1543`). Why: the
  condition is the one that code names, and a second code would split the
  family.
- This phase registers no conformance corpus case. Why: phase 3 and phase 4
  both register cases in `src/common/netsim/internal/netsimtest`, and the
  parent orders them against each other for that reason alone; this phase's
  `After:` is `none`, so it may run beside phase 3, and a third editor of
  those files would collide with both. The firewall workflow gets its corpus
  case in phase 4, which assembles the whole device.
- The two unpinned guards phase 1 recorded are closed here; the
  mutation-testing gate that phase 1's Open questions raise is not. Why: the
  guards are two test inputs in one file, while a gate is merge-gate
  configuration, which `AGENTS.md` ("Hard boundaries") puts behind explicit
  guardrail review and therefore behind its own plan.

## Requirements

Parent R4 (the tagged ingress and egress behaviour) and parent R5 (`netmodel`
loading a `Subinterface`) carry over, restated below as R2 and R7. In
addition:

1. A routed interface with `Port` and `VLAN` both set loads against a switch
   with no bridge at all; one with neither field set fails; and one whose
   VLAN is outside 1 to 4094 fails. Example: firewall `fw` with port `eth1`
   only, no `Bridge`, and `eth1.10` at 10.0.10.1/24 passes `vswitch.New`; an
   interface with both fields zero fails naming
   `vrfs.default.interfaces.eth1.10`; `{Port: "eth1", VLAN: 4095}` fails
   naming the same field with `.vlan`.
2. A frame arriving on a routed port with outer C-TAG 10 is routed by the
   interface holding (that port, 10), and leaves on the interface holding
   (its port, 20) with one C-TAG carrying VID 20, TPID `0x8100`, the ingress
   PCP, and the ingress DEI. Example: `fw` with trunk `eth1` outside the
   bridge, `eth1.10` at 10.0.10.1/24 and `eth1.20` at 10.0.20.1/24 sharing the
   router MAC; a frame tagged 10 with PCP 3 and DEI set, from 10.0.10.7 to
   10.0.20.5, leaves `eth1` tagged 20 with PCP 3 and DEI set.
3. Two differently named interfaces claiming the same (port, VID) fail
   validation; the same VID on two ports passes. Example: `eth1.10` and
   `fw-inside`, both `{Port: "eth1", VLAN: 10}`, fail naming the port and the
   VID; `eth1.10` and `eth2.10` both load.
4. A frame on a routed port whose outer tag names no sub-interface drops with
   `routing.ReasonNotBridged`, and the drop step's fact names the port and the
   VID. Example: `fw` receives a frame tagged 30 on `eth1`; the trace ends in
   a routing drop whose fact reads port `eth1` and VID 30.
5. A frame on a port with no routed interface still reaches the bridge, and a
   tagged frame on a plain untagged routed port now takes the tag-miss drop.
   Example: a switch with `eth1.10` on `eth1` and a plain switchport `eth2`
   forwards a frame arriving on `eth2` through the bridge exactly as today; a
   device whose only routed interface is untagged `eth3` drops a frame tagged
   10 arriving on `eth3` with `routing.ReasonNotBridged`.
6. A bridge VLAN lookup never answers with a sub-interface, whatever the
   interface names sort to. Example: a device holding both `vlan10` (VLAN 10,
   no port) and `xe1.10` (port `xe1`, VLAN 10) answers `ByVLAN(10)` with
   `vlan10`, although `xe1.10` sorts after it and wins today.
7. `netmodel` loads a `Subinterface` over a physical or LAG parent with one
   `ETHER_TYPE_DOT1Q` tag of a valid VID as a routed interface carrying that
   parent's port name and that tag's VID, contributes no port-table entry for
   it, reports the same capabilities a VLAN-interface router would, and the
   loaded spec builds through `vswitch.NewWithSpec`. A two-tag stack, an
   S-Tag, VID 0, VID 4095, or an absent encapsulation raises
   `netmodel.routing.unsupported_encapsulation` and leaves the interface out.
   Example: `eth1.10` with `encapsulation.tags = [{tpid: ETHER_TYPE_DOT1Q,
   vlan_id: 10}]` loads to `routing.Interface{Port: "eth1", VLAN: 10}` and the
   port table holds `eth1` and no `eth1.10`.
8. A sub-interface's parent port is still refused as a bridge switchport or an
   STP port. Example: `eth1.10` on `eth1` with `eth1` in
   `Bridge.VLAN.Switchports` fails construction with the switchport ban's
   message (`vswitch/config.go:319`), and the same interface with `eth1` in
   `STP.Ports` fails with the spanning-tree ban's (`:329`); the test asserts
   the message, since all three bans in that file name the same `.port`
   field. This is the direction record's commitment that a sub-interface's
   parent port stays outside the bridge and the spanning tree
   (`docs/architecture/2026-09-16-local-network-analysis-direction.md:44-48`).
9. `udp.Verify` returns false for a mixed address family pair and for a
   datagram `Decode` refuses, and a test fails when either guard is removed.
   Example: with the decode guard at `src/common/net/udp/udp.go:118-121`
   deleted, `Verify([]byte{0x14,0xe9,0x14,0xe9,0x00,0x04,0xe1,0x0d},
   10.0.10.7, 224.0.0.251)` returns true and its test fails.

## Out of scope

- A sub-interface on a bridge switchport, which the VLAN interface already
  models (`TestRoutedFrameLeavesOnSameTrunkPort`,
  `src/common/netsim/vswitch/switch_test.go:4213`).
- QinQ sub-interfaces, S-Tag encapsulation, non-`0x8100` TPIDs, and
  priority-tagged (VID 0) sub-interfaces.
- Static routes in the schema; `netmodel` still leaves `vrf.Routes` empty.
- A conformance corpus case for the firewall, which phase 4 registers.
- The mutation-testing gate phase 1's Open questions raise.

## Units

### U1. Routing and the switch understand sub-interfaces
Files: `src/common/netsim/vswitch/routing/config.go`, `src/common/netsim/vswitch/routing/config_test.go`, `src/common/netsim/vswitch/routing/layer.go`, `src/common/netsim/vswitch/routing/layer_test.go`, `src/common/netsim/vswitch/routing/README.md`, `src/common/netsim/vswitch/config.go`, `src/common/netsim/vswitch/config_test.go`, `src/common/netsim/vswitch/switch.go`, `src/common/netsim/vswitch/switch_test.go`, `src/common/netsim/vswitch/dependency_metadata_test.go`, `src/common/netsim/vswitch/README.md`
After: none
Change: the four `VLAN != 0` sites each gain `&& Port == ""`. `Validate`
accepts `Port` with `VLAN`, rejects the both-zero case and an invalid VLAN on
a `Port`-bearing interface, keys port claims on the (port, VID) pair, and
limits `claimedVLANs` to interfaces with no `Port`. `byPort` becomes
`map[string]map[vlan.ID]string`; `ByPort` and `PortLookupScopes` are deleted
in favour of `ByPortVLAN` and `PortVLANLookupScopes`, with the nested
`PortVLANLookupScope` constructor beside the untouched `PortLookupScope`. In
the switch, the routed-port ingress path reads the frame's outer tag, taking
VID zero when `Tags` is empty and treating a TPID that is neither zero nor the
C-TAG EtherType as no match; an unrouted port keeps falling through to the
bridge, and a routed port with no interface at that VID drops with
`routing.ReasonNotBridged` carrying a fact naming the port and the VID and
consulting `PortVLANLookupScopes`. The egress branch at `switch.go:1356` tests
`VLAN != 0 && Port == ""`, and the port path applies
`vlan.Tag{TPID: uint16(ethernet.EtherTypeDot1Q), VID: egress VID, PCP: ingressPCP, DEI: ingressDEI}`
to `routeRes.Frame` before the transmit and LAG checks, and only when the
egress interface's `VLAN` is non-zero. The routing package
and the switch land together because the two deleted methods have their only
callers at `switch.go:781` and `:783`, and the verifier builds the whole
module (`.claude/skills/verify-change/scripts/verify-change.sh:458`), so
neither half compiles alone.
Tests: `routing/config_test.go` gains a loaded sub-interface, two differently
named interfaces claiming one (port, VID) rejected naming both fields, the
same VID on two ports accepted, both fields zero rejected, and VLAN 4095 on a
port rejected (R1, R3). `routing/layer_test.go` gains a lookup returning
different interfaces for one port at two VIDs, a VID miss reporting
`portRouted` true and `matched` false, an unrouted port reporting both false,
and `vlan10` beside `xe1.10` asserting `ByVLAN(10) == "vlan10"` (R6); that
last case must use a sub-interface name sorting *after* the bridge interface,
because the last write in sorted order wins and a pair like `vlan10` and
`eth1.10` passes without the fix and pins nothing. The landed `ByPort` case at
`routing/layer_test.go:516-518` is rewritten onto `ByPortVLAN` rather than
deleted. `vswitch/config_test.go` gains the R1 bridgeless firewall passing
`New` and both R8 bans asserted by message. `switch_test.go` gains the R2
firewall asserting the egress tag's VID, TPID, PCP, and DEI; the R4 miss on
VID 30 asserting the reason and the fact's port and VID; an untagged frame
where no untagged interface exists taking the same drop; an S-Tagged frame
taking it too; a tagged frame on a plain untagged routed port taking it (R5);
the R5 switchport beside a sub-interface still bridging; and an untagged
routed port beside a sub-interface on another port, so a regression that sent
the sub-interface to the bridge fails here rather than in phase 4.
`dependency_metadata_test.go` gains a tagged miss asserting the VID-bearing
scope is consulted, beside the port-level entry at `:437-440`.
Docs: `routing/README.md` gains a "Sub-interfaces" section after "Example".
Four comments state rules this unit changes and are corrected with it: the
doc comments at `routing/config.go:20-21` and `:155-162`, which both give the
old exclusivity rule; `routing/layer.go:74`, which names the wrong-MAC drop as
`ReasonNotBridged`'s only cause; and in `vswitch/README.md`, the two-shape
interface sentence at `:160-162`, the same-reason sentence at `:162-164` and
its row in the reason table at `:286`, and the "with no tags" re-encapsulation
sentence at `:179-181`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/routing src/common/netsim/vswitch`

### U2. netmodel loads a Subinterface
Files: `src/common/netsim/vswitch/netmodel/netmodel.go`, `src/common/netsim/vswitch/netmodel/routing_test.go`, `src/common/netsim/vswitch/netmodel/README.md`
After: U1
Change: the routing walk gains a `case iface.GetSub() != nil` arm resolving
the parent by name against the loaded interfaces, accepting a physical or LAG
parent whose encapsulation holds exactly one `ETHER_TYPE_DOT1Q` tag whose VID
satisfies `vlan.ID.Valid()`, and producing a `routing.Interface` with the
parent's port name and that tag's VID. Any other encapsulation raises the new
`netmodel.routing.unsupported_encapsulation`; an absent or non-port parent
keeps raising `netmodel.routing.unsupported_interface_kind`. The port-table
walk at `netmodel.go:381-398` skips a `sub` interface, and the capability walk
at `:493-501` counts a `sub` interface carrying an IP facet toward
`hasRoutedIface`. That walk runs before the routing walk, so it cannot know
what the encapsulation check accepted; the proto-level test is the right one,
and an unsupported encapsulation still ends with an empty VRF, which drops
`LayerRouting` at `netmodel.go:1658-1671`.
`routingInterfaceLookupScope` gains the same arm so a sub-interface reports on
its parent port's `routing.PortLookupScope` rather than the ownership
fallback. After: U1, because its tests build the loaded spec through
`vswitch.NewWithSpec`, which only passes once U1 has relaxed the
bridge-VLAN-table rule.
Tests: `routing_test.go` gains a load of `eth1.10` and `eth1.20` over one
physical parent asserting both `routing.Interface` values, that the port table
holds `eth1` and neither sub-interface name, that the capability list matches
a VLAN-interface router's, and that the loaded spec builds through
`vswitch.NewWithSpec` (R7); a two-tag stack, an S-Tag tag, a VID 0 tag, a VID
4095 tag, and an absent encapsulation each asserting the new issue code and
the parent port's scope; a parent naming a VLAN interface and a parent naming
no loaded interface each asserting `unsupported_interface_kind`.
Docs: `netmodel/README.md` gains the sub-interface arm and its new issue code
under "Construction trust boundary", and the scope enumeration at `:153-156`
gains the port-and-VID lookup scope U1 adds. The doc comment at
`netmodel.go:155-156` enumerates the routed interface kinds that imply vlan
and relay, and gains the sub-interface.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/netmodel`

### U3. The UDP verifier's two refusal guards are pinned
Files: `src/common/net/udp/udp_test.go`
After: none
Change: no production change. `TestVerify`'s two negative subtests take inputs
for which the guard under test is the only thing that can return false. The
decode-refusal case uses `{0x14, 0xe9, 0x14, 0xe9, 0x00, 0x04, 0xe1, 0x0d}`,
whose `Length` of 4 makes `Decode` refuse while its eight octets checksum
correctly for 10.0.10.7 to 224.0.0.251. The mixed-family case keeps the IPv4
mDNS fixture's bytes but sets its checksum to `0x8b92`, which folds to zero
under the IPv6 pseudo-header the code reaches once `addressFamily`'s error is
ignored. Both values were derived by hand from RFC 768 and RFC 8200 section
8.1 on 2026-09-16 and belong in the tests' comments with their derivation.
Tests: the two rewritten subtests in `TestVerify`. The unit is done only once
the implementer has watched each fail against its own guard deleted, and the
guards are the two `if err != nil { return false }` blocks inside `Verify`
(`src/common/net/udp/udp.go:114-117` and `:118-121`), not the guards inside
`Decode` or `addressFamily`, which produce different failures and in one case
a panic. The unit reports which mutation it watched per subtest, since both
subtests pass today with either guard removed and the unit exists for that
reason alone
(`docs/solutions/conventions/a-refusal-test-needs-an-input-only-the-refusal-rejects.md`).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/net/udp`

Waves: U1 U3 | U2

U1 spans two packages because the verifier builds the whole module, so a unit
that deletes a method and a unit that fixes its callers cannot pass separately.
U2 follows U1 because its tests construct a switch from the loaded model. U3
shares no file with either.

## Verification

```bash
go test -race ./src/common/net/udp/... ./src/common/netsim/...
.claude/skills/verify-change/scripts/verify-change.sh --base main
```

No lab device takes part and no manual step remains.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] R1 to R9 hold on the landed tree.
- [ ] `src/common/netsim/vswitch/routing/README.md` gains its "Sub-interfaces"
      section; `src/common/netsim/vswitch/README.md` no longer says an
      interface has one of two shapes or that routed packets leave with no
      tags; the stale doc comments in `routing/config.go`, `routing/layer.go`,
      and `netmodel.go` are corrected; and
      `src/common/netsim/vswitch/netmodel/README.md` describes the new issue
      code and the new lookup scope.
- [ ] Each of U3's two subtests has been watched failing with its own guard
      deleted, and the unit's report names the mutation.
- [ ] This plan's `status` set with an outcome note under its title; the
      parent's U2 `Landed:` line carries the commit range.
- [ ] No plan labels in code.

## Open questions

- Whether the repository should gain a mutation-testing gate, and at what
  scope. Three review rounds over phase 1 each found a test that passed
  against deliberately broken code, and a later round found the same shape in
  this plan's own R6 example before anyone wrote it. U3 closes the last two
  known instances by hand. The property behind them is that every guard should
  be discriminated by at least one test, which mutation testing checks
  mechanically. A gate is merge-gate configuration and a policy surface, so it
  needs its own plan and a direction record. Recommended and unconfirmed: keep
  the gate out of the feature phases and raise it separately.
- Whether `phy` and PoE facts of the parent port need any change when a routed
  port carries tags. The plan expects none, and no unit adds a test for it.
