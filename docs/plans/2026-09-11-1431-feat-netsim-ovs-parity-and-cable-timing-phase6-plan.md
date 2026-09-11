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

- The codecs are `src/common/net/igmp` and `src/common/net/mld`, plain Go
  like `lacp`. `igmp.Message{Version Version; Type Type; MaxResp
  time.Duration; Group netip.Addr; Records []GroupRecord; Sources
  []netip.Addr; Suppress bool; QRV uint8; QQIC uint8}` represents an
  IGMPv2 or IGMPv3 query explicitly. A v2 query is 8 octets; a v3 query
  is 12 octets plus four octets per source and carries `Suppress`, `QRV`,
  and `QQIC`. The decoder maps an 8-octet query to v2 even when its Max
  Resp Code is zero; IGMPv1 queries remain outside this phase. Its other
  `Type` values are `ReportV1` (0x12), `ReportV2` (0x16), `Leave` (0x17),
  and `ReportV3` (0x22), with the six IGMPv3 record types in
  `GroupRecord`. `mld.Message` has the same fields with an MLDv1 or MLDv2
  query version, `Type` values `Query` (130), `ReportV1` (131), `Done`
  (132), and `ReportV2` (143), and the six MLDv2 record types in
  `AddressRecord`. An MLDv1 query is 24 octets; an MLDv2 query is 28
  octets plus 16 octets per source and carries `Suppress`, `QRV`, and
  `QQIC`.

  `igmp.Decode(payload []byte) (Message, error)` reads the message after
  the IPv4 header. `mld.Decode(hdr ip.Header, payload []byte) (Message,
  error)` parses the Hop-by-Hop Header Extension Length instead of
  assuming eight octets, requires that header's Next Header to be ICMPv6
  (58), and verifies the ICMPv6 suffix alone. Its pseudo-header uses Next
  Header 58 and the suffix length, excluding the Hop-by-Hop header.
  `igmp.Encode(m)` and `mld.Encode(hdr, m)` return `([]byte, error)` and
  write the checksum; the MLD encoder writes the ICMPv6 message only, so
  its caller owns the Hop-by-Hop header. Both codecs expose distinct
  `ErrMalformed` and `ErrUnsupported` sentinels. A bad checksum, short or
  inconsistent body, or invalid field wraps `ErrMalformed`; a
  structurally valid message with an unknown type or unsupported wire
  version wraps `ErrUnsupported`.

  Timer encoding is version-specific. IGMPv2 represents `MaxResp` as an
  unsigned eight-bit count of 100 ms units. For IGMPv3, RFC 3376 section
  4.1.1 makes codes below 128 linear in 100 ms units and decodes a code
  at or above 128 as `(mant | 0x10) << (exp + 3)` units. MLDv1 uses an
  unsigned 16-bit millisecond count. For MLDv2, RFC 3810 section 5.1.3
  makes codes below 32768 linear in milliseconds and decodes a code at
  or above 32768 as `(mant | 0x1000) << (exp + 3)` milliseconds. Encoders
  require an exact representation and reject a wrong-family address, a
  non-multicast group where the message requires one, a source or record
  count that does not fit the wire field, an excessive wire length, and
  an unrepresentable timer. Why: RFC 2236 section 2, RFC 3376 sections
  4.1 and 4.2, RFC 2710 section 3, and RFC 3810 sections 5.1 and 5.2
  define different legacy and current query layouts. Explicit versions,
  checked encoders, and separate error classes let the switch preserve
  the forwarding behavior RFC 4541 assigns to malformed and unknown
  control messages; both packages stay free of gopacket under the
  `netpenguard` test.
- `mcast.Config{VLANs map[vlan.ID]VLANSnooping}`;
  `VLANSnooping{FloodUnregistered *bool; FastLeave bool; RouterPorts
  []string; MembershipInterval time.Duration; RouterPortInterval
  time.Duration}` with `FloodUnregistered` nil meaning true,
  `MembershipInterval` 0 meaning 260 s (RFC 2236 section 8.4 and RFC 2710
  section 7.4: robustness 2 times query interval 125 s plus query response
  interval 10 s), and `RouterPortInterval` 0 meaning the same. A VLAN
  absent from the map has no snooping and floods every group frame as
  today. Package validation refuses a VLAN outside 1..4094, a router port
  absent from the port table, and a negative interval. Switch-level
  validation also requires a VLAN-aware bridge, requires every snooped
  VLAN to exist in the bridge VLAN table, and requires every static router
  port to be a logical forwarding member of that VLAN; a physical LAG
  member is not a valid static egress. `Diff` reports per VLAN; `Clone`
  deep-copies. Why: OVS's `mcast_snooping_enable` and its
  `other_config` `mcast-snooping-disable-flood-unregistered`,
  `mcast-snooping-aging-time`, and static `mcast-snooping-flood` ports
  are the same knobs, and the interval follows the RFCs the parent names
  rather than OVS's 300 s.
- `mcast.Layer` keeps, per snooped VLAN, a group table keyed by (group
  address, port) with an expiry, and a router-port set keyed by port with
  an expiry (static router ports never expire). `Learn(now, vid, port,
  source, m igmp.Message)` and `LearnMLD(now, vid, port, source, m
  mld.Message)` receive the decoded source address and the logical ingress
  port classified by `bridge.Ingress`; a physical LAG member never enters
  either table. IGMPv1 and IGMPv2 reports and MLDv1 reports add or refresh
  membership. For IGMPv3 and MLDv2, `MODE_IS_EXCLUDE` and
  `CHANGE_TO_EXCLUDE_MODE` add or refresh even with no sources;
  `MODE_IS_INCLUDE` and `CHANGE_TO_INCLUDE_MODE` add or refresh only with
  a source, while an empty record follows leave behavior;
  `ALLOW_NEW_SOURCES` adds or refreshes only when it contains a source;
  `BLOCK_OLD_SOURCES` is a group-only no-op and does not create, refresh,
  or remove membership. A Leave, Done, or record following leave behavior
  removes the entry immediately with `FastLeave` and otherwise lets the
  existing expiry stand. A query with a non-zero IGMP source or a
  link-local MLD source adds or refreshes its logical ingress as a router
  port.

  `Age(now)` drops expired entries and runs from the switch's `Age`.
  `Resolve(vid, group) (ports []string, registered bool)` returns the
  sorted union of members and router ports, but `registered` means that
  the group has at least one membership entry; router discovery alone
  never registers every group. `Groups(vid) []Entry{Group, Port,
  Expires}` and `RouterPorts(vid) []RouterPort{Port, Expires, Static}`
  serve snapshots and tests. Why: RFC 4541 section 2.1.1 learns router
  ports from a non-zero query source, while section 2.1.2 defines
  registration from membership reports and still sends unregistered
  traffic to router ports. The explicit six-record approximation keeps
  source-specific state out of this phase without inventing membership
  from an empty source change.
- The relay consults the layer for a group destination through
  `bridge.GroupResolver` (`Resolve(vid vlan.ID, f ethernet.Frame)
  (ports []string, decided bool)`) set through `SetGroupResolver`, the
  shape of `Gate` and `Selector`. The resolver decides only for a decoded
  IP multicast destination on a snooped VLAN. A non-IP frame, an IP
  unicast or broadcast packet carried in a group destination MAC, an IPv4
  destination in 224.0.0.0/24, ff02::1, or an IPv6 multicast address whose
  scope nibble is 0 or 1 stays undecided and follows the ordinary bridge
  flood. For other groups, a registered group resolves to the member and
  router union. An unregistered group with flooding enabled stays
  undecided. With flooding disabled it resolves to router ports only; an
  empty decided set and `mcast.ReasonUnregistered` occur only when no
  router port remains.

  `Egress` and `EgressTo` share one candidate and replication helper. It
  excludes ingress and physical LAG members, checks port state and VLAN
  membership, applies the gate and protected-port rule, enforces MTU,
  selects a logical LAG member, rewrites the tag form, records each
  candidate's drop, and computes the aggregate outcome. Its result keeps
  the classified ingress, FID, and prior steps, appends the replication
  and per-port steps, and accepts a caller-supplied reason for a genuinely
  empty candidate set. Every switch path using either method passes the
  result through `finishForward`, so mirror-output filtering and traffic
  copies still run. Why: RFC 4541 sections 2.1.2 and 3 reserve ordinary
  flooding for IPv4 link-local groups, ff02::1, and IPv6 scope 0 or 1;
  the shared helper makes selective and control replication obey the
  flood path's existing port, LAG, isolation, and trace rules.
- The switch classifies candidate control frames through
  `bridge.Ingress` with learning disabled, then validates the outer header
  and codec result before it mutates either table. IGMP requires TTL 1
  and a multicast destination; IGMPv1 reports are valid joins. MLD
  requires Hop Limit 1, a link-local source, and a Router Alert option in
  the parsed Hop-by-Hop header. A failed outer check or `ErrMalformed`
  drops with `mcast.ReasonBadControl` ("bad-control") and commits neither
  ordinary MAC learning nor snooping state. A valid supported message
  commits ordinary MAC learning once before snooping learning. A
  well-formed candidate that returns `ErrUnsupported` also commits
  ordinary MAC learning, makes no snooping mutation, and floods unchanged
  to every other forwarding port of the VLAN. Only a frame that is not a
  control candidate resumes the data path.

  Supported reports and leaves go through `bridge.EgressTo` to router
  ports only; no router port produces `mcast.ReasonNoRouterPort`
  ("no-router-port"). Queries go to every other forwarding port of the
  VLAN after their source and classified logical ingress are passed to
  the layer. `Peek` uses the same classification and egress decisions but
  commits neither the MAC table nor the multicast tables. Ordinary data
  still learns once before egress, and that learning is intentional. Why:
  RFC 4541 section 2.1.1 requires unknown IGMP control to flood and bad
  control to contribute no state, while RFC 2236 section 2 and RFC 2710
  section 3 supply the outer-header invariants that make a report or query
  safe to learn.
- `Switch.Config.Mcast *mcast.Config`, `Capabilities` adding
  `port.LayerMcast` ("mcast") when set, `Diff`, `Switch.Groups(vid)`, and
  `Switch.RouterPorts(vid)` pass through the layer. `Derive` keeps group
  and learned router entries only when their port is still a forwarding
  member of a VLAN that remains snooped, preserves each retained entry's
  original expiry, and drops every other entry; static router ports come
  from the new configuration. `fabric.Device` gains both `Groups
  map[vlan.ID][]mcast.Entry` and `RouterPorts
  map[vlan.ID][]mcast.RouterPort`, populated by `Fabric.Snapshot`. No
  schema, loader, or export lands in this phase. Why: derivation must not
  reset live timers or retain state across a removed VLAN membership, and
  both halves of the snooping table must be observable in the snapshot
  used by the acceptance examples.
- Records: the vswitch README's ladder gains the snooping layer and its
  drop reasons gain `unregistered`, `no-router-port`, and `bad-control`;
  the new packages carry READMEs with the message layouts and cited RFC
  sections; the fabric README's snapshot section names `Groups` and
  `RouterPorts`. The virtual-device direction record amends its
  capability-layer import rule to allow shared network value and codec
  packages under `src/common/net` while still forbidding imports between
  sibling capability layers. Why: the new layer needs the shared address,
  VLAN, IGMP, and MLD packages, and leaving the accepted record narrower
  than existing and planned layers would make the implementation choose
  between the architecture and the codebase.

## Requirements

Every example uses one switch `sw1` with ports `1/1/1` through `1/1/4`
untagged in VLAN 10 with PVID 10, hosts `h1` through `h4` on them, VLAN 10
snooped with defaults, and frames built with the codecs. An IGMPv2 report
for 239.1.1.1 is an IPv4 frame from the host's MAC and address to
239.1.1.1 (destination MAC 01:00:5e:01:01:01) carrying `igmp.Message{Type:
ReportV2, Group: 239.1.1.1}`. A general query is from 10.0.0.254 to
224.0.0.1 with `Type: Query, Version: V2`. An MLDv1 report for ff05::1 is
an IPv6 frame from a link-local source to ff05::1 (destination MAC
33:33:00:00:00:01) with a Hop-by-Hop Router Alert and
`mld.Message{Type: ReportV1, Group: ff05::1}`.

1. Codecs. Acceptance: `igmp.Encode` of the v2 report yields 8 octets with
   type 0x16 and a checksum `igmp.Decode` verifies; an 8-octet IGMPv2
   query and a 12-octet IGMPv3 query with `Suppress`, `QRV`, and `QQIC`
   round-trip with their versions; a v3 report with one
   MODE_IS_EXCLUDE record for 239.1.1.1 round-trips. A wrong checksum
   returns `ErrMalformed`, while a checksummed body with type 0x30 returns
   `ErrUnsupported`; the encoder rejects a wrong-family group, an
   unrepresentable response time, and an overflowing source count.
   `mld.Encode` of the v1 report yields 24 octets with type 131 and a
   checksum `mld.Decode` verifies over the pseudo-header; a 24-octet
   MLDv1 query and a 28-octet MLDv2 query with `Suppress`, `QRV`, and
   `QQIC` round-trip with their versions. A v2 report (143) with one
   record round-trips. A bad MLD checksum returns `ErrMalformed`, while a
   checksummed unknown type returns `ErrUnsupported`. Given a
   Hop-by-Hop header longer than eight octets, `mld.Decode` uses its
   encoded length, requires terminal Next Header 58, and verifies the
   checksum over the ICMPv6 suffix; its encoder rejects the corresponding
   IPv4, count, length, group, and timer errors.
2. Group forwarding. Acceptance: `h1` reports 239.1.1.1, `h2` remains a
   nonmember, and a query arrives on `1/1/4`; a frame from `h3` to
   239.1.1.1 is delivered to `h1` and `h4` (the router port), not `h2`;
   the hop's result reads a replicate step "group members";
   `Snapshot().Devices["sw1"].Groups[10]` lists the `h1` entry.
3. Unregistered groups. Acceptance: with no report for 239.2.2.2, a frame
   to it from `h3` floods to `h1`, `h2`, and `h4`; with
   `FloodUnregistered` false and `h4` learned as a router port it reaches
   `h4` only; without a router port it is dropped with `unregistered`. A
   frame to 224.0.0.5 floods whatever the option, as do ff02::1 and IPv6
   multicast addresses with scope nibble 0 or 1. An IPv4 limited
   broadcast to 255.255.255.255 in a group destination MAC remains an
   ordinary bridge flood.
4. Control frames. Acceptance: after the query on `1/1/4`, `h1`'s report
   is delivered to `h4` only; before any query, the report is dropped with
   `no-router-port`; the query itself is delivered to `h1`, `h2`, and
   `h3`; `Snapshot().Devices["sw1"].RouterPorts[10]` lists `1/1/4`.
   An IGMP packet with TTL 2 or a unicast destination, and an MLD packet
   with Hop Limit 2, a non-link-local source, or no Router Alert each drop
   with `bad-control` and change neither the MAC table nor the snooping
   tables; so does a checksummed control packet with a malformed body.
   A checksummed unknown control type floods unchanged to every other
   forwarding port of VLAN 10 and changes no snooping state. An IGMPv1
   report with valid outer headers creates membership, and `Peek` for
   each case leaves both tables unchanged.
5. Aging. Acceptance: after `h4` becomes a router port, `h1` reports at
   `t0` and `h2` stays a nonmember; a frame to the group at `t0 + 259s`
   reaches `h1` and `h4`, not `h2`. At `t0 + 261s` the entry is gone and
   the frame floods to `h1`, `h2`, and `h4` under the default option. With
   `FastLeave`, a Leave from `h1` at `t0 + 1s` removes the entry at once;
   without it the entry stays until `t0 + 260s`.
6. MLD. Acceptance: `h1` sends the MLDv1 report for ff05::1 and `h4` an
   MLDv1 query; a frame from `h3` to ff05::1 reaches `h1` and `h4`, not
   nonmember `h2`, and `Groups[10]` lists ff05::1 on `1/1/1`.
7. Diff and Validate. Acceptance: `mcast.Diff` reports `flood_unregistered`
   and `router_ports` per VLAN; package validation refuses a router port
   `1/1/9` and a negative interval. Switch validation refuses multicast
   configuration without a VLAN-aware bridge, a snooped VLAN absent from
   the bridge table, and a static router port that is a physical LAG
   member or is not a forwarding member of the VLAN;
   `vswitch.Capabilities` lists `mcast`.

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
Tests: `igmp_test.go` and `mld_test.go`, requirement 1: both query
versions and sizes, exact timer-code boundaries, encoder validation,
distinct malformed and unsupported errors, and variable-length MLD
Hop-by-Hop checksum handling. Each is evidence for this unit.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/net src/common/internal/netpenguard`

### U2. The snooping layer

Files: `src/common/netsim/vswitch/mcast/config.go`, `layer.go`, `diff.go`,
`config_test.go`, `layer_test.go`, `README.md`
After: U1
Change: `Config`, `Validate`, `Clone`, `Diff`, `New(cfg, ports)`,
`Clone`, `Learn`, `LearnMLD`, `Age`, `Resolve`, `Groups`, `RouterPorts`,
`Entry`, `RouterPort`, `ReasonUnregistered`, `ReasonNoRouterPort`, and
`ReasonBadControl`; source-aware router learning, the six record rules,
and membership-only registration.
Tests: `layer_test.go`, the layer-level facts of requirements 2 through
6 (all record types, logical router ports, registered and unregistered
resolution, aging, fast leave, and MLD);
`config_test.go`, requirement 7's package part. Each is evidence for
this unit.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/mcast`

### U3. The relay, the switch, and the fabric

Files: `src/common/netsim/vswitch/port/port.go`,
`src/common/netsim/vswitch/bridge/bridge.go`, `bridge_test.go`,
`src/common/netsim/vswitch/config.go`, `switch.go`, `diff.go`, `derive.go`,
`switch_test.go`, `README.md`, `src/common/netsim/fabric/run.go`,
`mcast_test.go`, `README.md`, `src/common/netsim/README.md`,
`docs/architecture/2026-09-10-virtual-device-direction.md`
After: U2
Change: `port.LayerMcast`; `bridge.GroupResolver`, `SetGroupResolver`,
the IP-multicast-only group rule in `Egress`, `EgressTo`, and their shared
replication helper; `Config.Mcast` and switch-level validation;
`Capabilities`; two-stage control classification, outer-header checks,
MAC learning, and `finishForward`; `Groups`, `RouterPorts`, `Diff`, and
the state-retention rules in `Derive`; both multicast maps on
`fabric.Device` and their `Snapshot` population; the READMEs and netsim
package table. Amend the direction record's capability-layer import rule
to allow shared value and codec packages under `src/common/net` while
continuing to forbid sibling capability-layer imports.
Tests: `bridge_test.go`, a resolver deciding a member set, an empty set
with a caller reason, and `EgressTo` sharing every flood rule;
`switch_test.go`, malformed, unsupported, legacy, and outer-header
control cases, non-multicast IP broadcast, router-only unregistered
delivery, validation, `Peek`, `finishForward`, and derivation retention;
`fabric/mcast_test.go`, requirements 2 through 6 end to end, including
the nonmember assertions and both snapshot maps. Each is evidence for
this unit.
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
