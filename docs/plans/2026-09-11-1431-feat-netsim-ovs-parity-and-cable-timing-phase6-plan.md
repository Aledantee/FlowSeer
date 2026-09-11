---
title: Network Simulation, Phase 6: Multicast Snooping - Plan
type: feat
date: 2026-09-11
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: code
parent: docs/plans/2026-09-11-1431-feat-netsim-ovs-parity-and-cable-timing-plan.md
---

# Network Simulation, Phase 6: Multicast Snooping - Plan

## Goal

A switch with snooping enabled on a VLAN learns which ports want which IP
multicast groups from IGMP and MLD reports, learns its router ports from
queries, forwards a group frame to the member ports and the router ports
only, floods or drops an unregistered group per the VLAN's option, forwards
reports to router ports alone and queries everywhere, and ages a
membership out after the membership interval with no report. The means is
an IGMP codec and an MLD codec under `src/common/net`, a
`netsim/vswitch/mcast` layer holding a group table per VLAN with router
ports and aging on arrival like the filtering database, a group resolver
the relay consults for a group destination, and the switch handling the
control frames after the relay's ingress half. The phase is wrong if a
consumer needs source-specific forwarding (IGMPv3 INCLUDE sources), which
stays a group-only table here, or a querier of the switch's own.

## Decisions

The parent's Decision on snooping holds. This phase settles the shapes
its stub left open:

- The codecs are `src/common/net/igmp` and `src/common/net/mld`, plain
  Go like `lacp`. `igmp.Message{Type Type; MaxResp time.Duration; Group
  netip.Addr; Records []GroupRecord; Sources []netip.Addr; QRV uint8;
  QQIC uint8}` with `Type` values `Query` (0x11), `ReportV1` (0x12),
  `ReportV2` (0x16), `Leave` (0x17), `ReportV3` (0x22) and
  `GroupRecord{Type RecordType; Group netip.Addr; Sources []netip.Addr}`
  with the six IGMPv3 record types (1 through 6); `Decode(payload
  []byte) (Message, error)` over the IGMP message after the IPv4 header,
  refusing a bad checksum, a short body, or an unknown type with an error
  wrapping `ErrUnsupported`; `Encode(m) []byte` writing the checksum. The
  IPv4 header is the caller's (`ip.Decode` yields it and the payload, and
  IGMP is protocol 2). `mld.Message{Type Type; MaxResp time.Duration;
  Group netip.Addr; Records []AddressRecord; Sources []netip.Addr}` with
  `Type` values `Query` (130), `ReportV1` (131), `Done` (132), `ReportV2`
  (143) and the six MLDv2 record types; `Decode(hdr ip.Header, payload
  []byte) (Message, error)` skips a Hop-by-Hop Options header (next
  header 0, which carries the Router Alert RFC 2710 requires) to reach
  ICMPv6 (58), verifies the ICMPv6 checksum over the IPv6 pseudo-header
  from `hdr`, and refuses as `igmp` does; `Encode(hdr ip.Header, m)
  []byte` writes the ICMPv6 message with its checksum, without the
  Hop-by-Hop header, which the caller adds when it builds a frame. Why:
  the formats are RFC 2236 section 2 (types 0x11, 0x12, 0x16, 0x17), RFC
  3376 sections 4.1 and 4.2 (the 0x22 report and its record types 1
  through 6), RFC 2710 section 3 (types 130, 131, 132; link-local source,
  hop limit 1, Router Alert in a Hop-by-Hop header), and RFC 3810
  section 5.2 (the 143 report); both codecs stay free of gopacket under
  the `netpenguard` test.
- `mcast.Config{VLANs map[vlan.ID]VLANSnooping}`;
  `VLANSnooping{FloodUnregistered *bool; FastLeave bool; RouterPorts
  []string; MembershipInterval time.Duration; RouterPortInterval
  time.Duration}` with `FloodUnregistered` nil meaning true,
  `MembershipInterval` 0 meaning 260 s (RFC 2236 section 8.4 and RFC 2710
  section 7.4: robustness 2 times query interval 125 s plus query response
  interval 10 s), and `RouterPortInterval` 0 meaning the same. A VLAN
  absent from the map has no snooping and floods every group frame as
  today. `Validate(ports)` refuses a VLAN outside 1..4094, a router port
  absent from the port table, and a negative interval; `Diff` reports per
  VLAN; `Clone` deep-copies. Why: OVS's `mcast_snooping_enable` and its
  `other_config` `mcast-snooping-disable-flood-unregistered`,
  `mcast-snooping-aging-time`, and static `mcast-snooping-flood` ports
  are the same knobs, and the interval follows the RFCs the parent names
  rather than OVS's 300 s.
- `mcast.Layer` keeps, per snooped VLAN, a group table keyed by
  (group address, port) with an expiry, and a router-port set keyed by
  port with an expiry (static router ports never expire). `Learn(now,
  vid, port, m igmp.Message)` and `LearnMLD(now, vid, port, m
  mld.Message)`: a report (IGMP v1, v2, or a v3 record of type
  MODE_IS_EXCLUDE, CHANGE_TO_EXCLUDE_MODE, ALLOW_NEW_SOURCES, or
  MODE_IS_INCLUDE and CHANGE_TO_INCLUDE_MODE with at least one source;
  MLD v1 report or a v2 record of the same types) adds or refreshes the
  (group, port) entry with expiry now plus the membership interval; a
  Leave, a Done, or a CHANGE_TO_INCLUDE_MODE record with no sources
  removes the entry at once when `FastLeave` is set and leaves it to age
  otherwise; a query with a non-zero (IGMP) or link-local (MLD) source
  adds or refreshes the ingress port as a router port with expiry now
  plus the router-port interval. `Age(now)` drops expired entries, called
  by the switch from its `Age` as the relay's is. `Resolve(vid, group
  netip.Addr) (ports []string, registered bool)` returns the union of the
  group's member ports and the VLAN's router ports, sorted; `registered`
  false when the group has no member and no router port has learned
  anything (an unregistered group). `Groups(vid) []Entry{Group, Port,
  Expires}` and `RouterPorts(vid) []RouterPort{Port, Expires, Static}`
  for the snapshot and tests. Why: RFC 4541 section 2.1.1 rule 1 (reports
  go to router ports, learned from queries with a non-zero source or
  configured) and section 2.1.2 rules 1 and 2; aging on arrival is how the
  filtering database ages and needs no wake.
- The relay consults the layer for a group destination through
  `bridge.GroupResolver` (`Resolve(vid vlan.ID, f ethernet.Frame)
  (ports []string, decided bool)`) set through `SetGroupResolver`, the
  shape of `Gate` and `Selector`: in `Egress`, a group destination on a
  VLAN with a resolver that decides gets the returned ports as its
  candidate set (minus the ingress port, through the same membership,
  gate, MTU, and tag rules as a flood, with a replicate step "group
  members"), and an empty decided set is a drop `mcast.ReasonUnregistered`
  ("unregistered"); a resolver that does not decide leaves the flood as
  today. The switch's resolver decides only when the VLAN is snooped and
  the frame is IP: it decodes the IP header, floods (does not decide) a
  destination in 224.0.0.0/24, the IPv6 all-nodes address ff02::1, an IPv6
  group of scope below 2 (`ff01::/16`), or a non-IP frame, and otherwise returns `mcast.Resolve` when
  registered, an empty decided set when unregistered and
  `FloodUnregistered` is false, and does not decide when unregistered and
  flooding. Why: RFC 4541 section 2.1.2 rules 1 and 2 word for word, and
  the resolver keeps the relay's egress rules in one place.
- The switch handles control frames after the relay's ingress half: when
  the classified VLAN is snooped and the frame decodes as IGMP (IPv4
  protocol 2) or MLD (IPv6 ICMPv6 with the MLD types), `Switch.forward`
  calls the layer's learn (when it mutates), then emits the frame through
  `bridge.EgressTo(in, f, ports)`, a new relay method that egresses on the
  named ports with the flood's rules: a report or a leave goes to the
  router ports only (nothing when there are none, recorded as a drop
  `mcast.ReasonNoRouterPort` ("no-router-port")), a query goes to every
  other port of the VLAN. A frame that decodes as neither is data and
  takes the relay's path. Why: RFC 4541 section 2.1.1 rules 1 and 3 (an
  unrecognized IGMP message floods; the codec's refusal makes the frame
  data, which floods to the group's members or everywhere).
- `Switch.Config.Mcast *mcast.Config`, `Capabilities` adding
  `port.LayerMcast` ("mcast") when set, `Diff` and `Derive` covering the
  layer, `Switch.Groups(vid)` and `Switch.RouterPorts(vid)` passing
  through, and `fabric.Device.Groups map[vlan.ID][]mcast.Entry` in the
  snapshot. No schema, loader, or export in this phase. Why: the parent's
  unit names the codecs, the layer, and the relay rule.
- Records: the vswitch README's ladder gains the snooping layer and its
  drop reasons gain `unregistered` and `no-router-port`; the new packages
  carry READMEs with the message layouts and the RFC sections; the fabric
  README's snapshot section names `Groups`.

## Requirements

Every example uses one switch `sw1` with ports `1/1/1` through `1/1/4`
untagged in VLAN 10 with PVID 10, hosts `h1` through `h4` on them, VLAN 10
snooped with defaults, and frames built with the codecs: an IGMPv2 report
for 239.1.1.1 is an IPv4 frame from the host's MAC and address to
239.1.1.1 (destination MAC 01:00:5e:01:01:01) carrying `igmp.Message{Type:
ReportV2, Group: 239.1.1.1}`; a general query is from 10.0.0.254 to
224.0.0.1 with `Type: Query`; an MLDv1 report for ff05::1 is an IPv6 frame
from a link-local source to ff05::1 (destination MAC 33:33:00:00:00:01)
with a Hop-by-Hop Router Alert and `mld.Message{Type: ReportV1, Group:
ff05::1}`.

1. Codecs. Acceptance: `igmp.Encode` of the v2 report yields 8 octets with
   type 0x16 and a checksum `igmp.Decode` verifies; a v3 report with one
   MODE_IS_EXCLUDE record for 239.1.1.1 round-trips; a body with a wrong
   checksum or type 0x30 is refused with `ErrUnsupported`. `mld.Encode`
   of the v1 report yields 24 octets with type 131 and a checksum
   `mld.Decode` verifies over the pseudo-header; a v2 report (143) with
   one record round-trips; `mld.Decode` given a payload starting with a
   Hop-by-Hop header reaches the message.
2. Group forwarding. Acceptance: `h1` and `h2` report 239.1.1.1, a query
   arrives on `1/1/4`; a frame from `h3` to 239.1.1.1 is delivered to `h1`,
   `h2`, and `h4` (the router port) and not flooded elsewhere; the hop's
   result reads a replicate step "group members"; `Snapshot().Devices
   ["sw1"].Groups[10]` lists the two entries.
3. Unregistered groups. Acceptance: with no report for 239.2.2.2, a frame
   to it from `h3` floods to `h1`, `h2`, and `h4`; with
   `FloodUnregistered` false it is dropped with `unregistered`; a frame to
   224.0.0.5 floods whatever the option; a frame to ff02::1 floods.
4. Control frames. Acceptance: after the query on `1/1/4`, `h1`'s report
   is delivered to `h4` only; before any query, the report is dropped with
   `no-router-port`; the query itself is delivered to `h1`, `h2`, and
   `h3`; `Snapshot` lists `1/1/4` among the router ports.
5. Aging. Acceptance: `h1` reports at `t0`; a frame to the group at
   `t0 + 259s` reaches `h1`; at `t0 + 261s` the entry is gone and the frame
   floods (default option); with `FastLeave`, a Leave from `h1` at
   `t0 + 1s` removes the entry at once; without it the entry stays until
   `t0 + 260s`.
6. MLD. Acceptance: `h1` sends the MLDv1 report for ff05::1 and `h4` an
   MLD query; a frame from `h3` to ff05::1 reaches `h1` and `h4` only, and
   `Groups[10]` lists ff05::1 on `1/1/1`.
7. Diff and Validate. Acceptance: `mcast.Diff` reports `flood_unregistered`
   and `router_ports` per VLAN; `Validate` refuses a router port `1/1/9`
   and a negative interval; `vswitch.Capabilities` lists `mcast`.

## Out of scope

Everything the parent lists; source-specific forwarding (IGMPv3 and MLDv2
sources are read to tell a join from a leave and otherwise ignored); a
querier of the switch's own; IGMPv1 queries; proxy reporting; a schema
for the group table; MVR.

## Units

### U1. The codecs

Files: `src/common/net/igmp/igmp.go`, `igmp_test.go`, `README.md`,
`src/common/net/mld/mld.go`, `mld_test.go`, `README.md`
After: none
Change: the two codecs of the first Decision with their READMEs.
Tests: `igmp_test.go` and `mld_test.go`, requirement 1. Each is evidence
for this unit.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/net src/common/internal/netpenguard`

### U2. The snooping layer

Files: `src/common/netsim/vswitch/mcast/config.go`, `layer.go`, `diff.go`,
`config_test.go`, `layer_test.go`, `README.md`
After: U1
Change: `Config`, `Validate`, `Clone`, `Diff`, `New(cfg, ports)`,
`Clone`, `Learn`, `LearnMLD`, `Age`, `Resolve`, `Groups`, `RouterPorts`,
`Entry`, `RouterPort`, `ReasonUnregistered`, `ReasonNoRouterPort`.
Tests: `layer_test.go`, the layer-level facts of requirements 2 through
6 (learning, router ports, resolve, aging, fast leave, MLD);
`config_test.go`, requirement 7's package part. Each is evidence for
this unit.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/mcast`

### U3. The relay, the switch, and the fabric

Files: `src/common/netsim/vswitch/port/port.go`,
`src/common/netsim/vswitch/bridge/bridge.go`, `bridge_test.go`,
`src/common/netsim/vswitch/config.go`, `switch.go`, `diff.go`, `derive.go`,
`switch_test.go`, `README.md`, `src/common/netsim/fabric/run.go`,
`mcast_test.go`, `README.md`, `src/common/netsim/README.md`
After: U2
Change: `port.LayerMcast`; `bridge.GroupResolver`, `SetGroupResolver`,
the group-destination rule in `Egress`, `EgressTo`; `Config.Mcast`,
`Capabilities`, the switch's resolver and control-frame handling,
`Groups`, `RouterPorts`, `Diff`, `Derive`; `fabric.Device.Groups`; the
READMEs and the netsim package table.
Tests: `bridge_test.go`, a resolver deciding a member set, an empty set
(`unregistered`), and `EgressTo` on two ports; `switch_test.go`, the
control-frame paths (report to router ports, query flooded, non-IGMP
data through the relay); `fabric/mcast_test.go`, requirements 2 through
6 end to end. Each is evidence for this unit.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim`

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- src/common/net src/common/netsim src/common/internal/netpenguard
go test -race ./src/common/net/... ./src/common/netsim/... ./src/common/internal/netpenguard/...
```

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] The codec, mcast, vswitch, fabric, and netsim READMEs match the
      landed API.
- [ ] This plan's `status` set with an outcome note under its title, the
      parent's `Landed:` line for this phase filled, and the parent set to
      `implemented` as its last phase.
- [ ] No plan labels in code, comments, or commit messages.

## Open questions

- Whether a report should also refresh the ingress port as a member when
  the VLAN has no router port (a network with no querier); the plan keeps
  the RFC rule, so such a report is learned and then dropped with
  `no-router-port`, which the journey names.
- RFC 4541 section 3 mandates MLD for groups of scope 2 or greater with
  ff02::1 the exception, so link-local-scope groups other than ff02::1 are
  snooped here; a lab switch that floods all of `ff02::/16` would be a
  documented difference.
