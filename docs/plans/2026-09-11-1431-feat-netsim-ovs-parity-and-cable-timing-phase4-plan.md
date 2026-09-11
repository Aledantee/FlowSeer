---
title: Network Simulation, Phase 4: Link Aggregation - Plan
type: feat
date: 2026-09-11
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: code
parent: docs/plans/2026-09-11-1431-feat-netsim-ovs-parity-and-cable-timing-plan.md
---

# Network Simulation, Phase 4: Link Aggregation - Plan

> Implemented.

## Goal

A LAG answers which member carries a frame and whether a member belongs to
the aggregation: a bond mode (active-backup, balance by source MAC and
VLAN, balance by the layer 2 to 4 fields) picks the member by hash or by
primary, a member joins after an up delay and leaves after a down delay,
and LACP (IEEE 802.1AX) negotiates the aggregation with a partner on the
timer facility spanning tree uses, falling back to active-backup when
configured to and carrying nothing otherwise. The means is an LACPDU codec
under `src/common/net/lacp`, a `netsim/vswitch/lag` layer that owns
member selection and the protocol, the port table and the relay handing
selection to it, the fabric feeding member link events and carrying
LACPDUs as transmissions, and a `net/protocol/lacp/v1` schema with the
aggregation facet's bond settings for the loader and the export. The phase
is wrong if a consumer needs load-driven rebalancing of hash buckets, which
the hash assignment here never needs, or a LAG whose members sit on two
switches.

## Decisions

The parent's Decision on aggregation holds, narrowed in one place: the
rebalance interval is out (see Out of scope). This phase settles the
shapes its stub left open:

- The codec is `src/common/net/lacp`, plain Go like `ethernet` and
  `vlan`: `PDU{Actor, Partner Info; CollectorMaxDelay uint16}`,
  `Info{SystemPriority uint16; SystemID netaddr.MAC; Key uint16;
  PortPriority uint16; PortID uint16; State State}`, and `State` a `uint8`
  bit set with `StateActive` 0x01, `StateShortTimeout` 0x02,
  `StateAggregation` 0x04, `StateSynchronization` 0x08, `StateCollecting`
  0x10, `StateDistributing` 0x20, `StateDefaulted` 0x40, `StateExpired`
  0x80. `Encode(p, src) ethernet.Frame` writes the Slow Protocols frame:
  destination 01-80-C2-00-00-02, EtherType 0x8809 (`EtherTypeSlowProtocols`
  added to `ethernet`), subtype 1, version 1, the Actor TLV (type 1, length
  20), the Partner TLV (type 2, length 20), the Collector TLV (type 3,
  length 16), the terminator (type 0, length 0), and reserved octets to a
  110-octet body; `Decode(f) (PDU, error)` refuses another EtherType,
  subtype, version, TLV type or length, or a short body with an error
  wrapping the package's `ErrUnsupported` sentinel; the layer maps it to
  `lag.ReasonUnsupportedLACPDU` ("unsupported-lacpdu"), since the codec
  under `src/common/net` imports nothing from `netsim`. Why: the layout is IEEE 802.1AX's LACPDU as
  Open vSwitch lays it out (`struct lacp_pdu`, `LACP_PDU_LEN 110`, the
  eight state bits,
  https://raw.githubusercontent.com/openvswitch/ovs/branch-3.3/lib/lacp.c),
  and the bit names are the `LacpState` textual convention
  (`spec/mib/ieee/IEEE8023-LAG-MIB:73`, clause 7.3.2.1.20). The
  `netpenguard` test keeps the package free of gopacket like every codec.
- `lag.Config{LAGs map[string]LAG}` keyed by the LAG port's name;
  `LAG{Mode Mode; Primary string; UpDelay, DownDelay time.Duration;
  HashBasis uint32; MinLinks int; LACP LACPConfig; Members
  map[string]Member}`; `Mode` a string type with `ActiveBackup` (empty
  means it), `BalanceSLB`, `BalanceTCP`; `LACPConfig{Mode LACPMode; Fast
  bool; SystemPriority uint16; SystemID netaddr.MAC; Key uint16; Fallback
  bool}` with `LACPMode` `Off` (empty), `Active`, `Passive`;
  `Member{Priority uint16; Key uint16}`. Zero values: `SystemPriority`
  32768, `SystemID` the switch MAC, `Key` the LAG port's index, starting at
  1, in the sorted names of the port table's LAG ports, member `Priority`
  32768, member `Key` the LAG's key, `Primary` the lowest member name. A
  switch whose `Config.LAG` is nil, or whose LAG port is absent from
  `LAGs`, builds the layer with that default for every LAG port, so a LAG
  with no configuration keeps today's behavior. `Validate(ports)` refuses a LAG name
  that is not a `port.Lag` port, a member that is not that LAG's member in
  the port table, a `Primary` outside the members, an unknown mode, a
  negative delay, and `MinLinks` above the member count; it accepts a LAG
  port absent from `LAGs`, which runs active-backup with the lowest member
  as primary and LACP off, today's rule. `Diff` reports every field with
  the port-level fields under the member. Why: OVS's Port columns
  `bond_mode`, `lacp`, `other_config` `bond-updelay`, `bond-downdelay`,
  `bond-hash-basis`, `lacp-time`, `lacp-fallback-ab`, `lacp-system-id`,
  `lacp-system-priority` and the Interface `lacp-port-priority`,
  `lacp-aggregation-key`
  (https://www.openvswitch.org/support/dist-docs/ovs-vswitchd.conf.db.5.html)
  are these fields; `MinLinks` is the aggregation facet's
  `minimum_active_links`; the system priority default is 802.1AX's and
  the MIB's `dot3adAggActorSystemPriority`
  (`spec/mib/ieee/IEEE8023-LAG-MIB:264`).
- Selection is one method, `Layer.Select(lag string, f ethernet.Frame,
  vid vlan.ID) (member string, ok bool)`, over the enabled members sorted
  by name: active-backup returns the primary when enabled, else the
  lowest enabled member; balance-slb hashes `HashBasis`, the source MAC,
  and `vid` with FNV-1a 32; balance-tcp hashes `HashBasis`, the source and
  destination MACs, the EtherType, and, when the payload decodes with
  `ip.Decode`, the source and destination addresses and the protocol, and
  when the protocol is 6 or 17 the two port numbers read from the first
  four octets of the payload `ip.Decode` returns (an IPv6 extension header
  makes the protocol something else, and the ports are then not read); a
  payload `ip.Decode` refuses, a bad IPv4 checksum included, hashes the
  layer 2 fields only. The hash's low eight bits pick a bucket of 256 and
  the bucket picks `members[bucket % len(members)]`. No member enabled
  returns false. Why: OVS hashes source
  MAC and VLAN for slb and the 5-tuple for tcp with a basis, and masks the
  hash into 256 buckets (`ofproto/bond.c`, `bond_hash`, `BOND_MASK`,
  https://raw.githubusercontent.com/openvswitch/ovs/branch-3.3/ofproto/bond.c);
  OVS's hash functions are not reproduced, since the property a test
  checks is "one flow, one member" and "same source, same member", not
  the bucket number, and FNV-1a is one deterministic function the tree
  can state.
- Member enablement is the layer's, from three inputs: the link
  (`Layer.LinkChange(now, member, up) Effects`, delayed by `UpDelay` on
  the way up and `DownDelay` on the way down through timers `NextWake`
  reports; a zero delay applies in the same call), the protocol (below),
  and `MinLinks` (fewer enabled members than the minimum disables all). A
  member is enabled when its delayed link is up and either LACP is off, or
  the member is attached with a synchronized partner, or the fallback
  holds. The LAG port's operational state in the switch's port table is
  Up while any member's link is up, as today, so LACPDUs reach the layer
  before the aggregation has converged; enablement decides selection
  only, and a LAG with no enabled member drops with `no-member`.
  `Switch.LinkChange` on a member reports the member itself, not its
  parent, to the layer, keeps the switch's per-member point-to-point and
  speed maps, rewrites the LAG row, and tells spanning tree of the LAG's
  link, point-to-point, and speed (the enabled members' highest); `Start`,
  `LinkChange`, `Wake`, and `NextWake` run whenever either layer is
  present, no longer only with spanning tree. Why: OVS delays enabling and disabling by
  `bond-updelay` and `bond-downdelay` through `delay_expires`
  (`ofproto/bond.c` cited above), and the parent's requirement 24 wants
  the failover at the moment the primary goes down with no delay set.
- LACP is the actor and partner exchange of IEEE 802.1AX as OVS runs it.
  Per member: actor `Info` (system priority and ID, key, port priority,
  port ID as the member's index in the sorted member names, state bits:
  Active when the mode is active, ShortTimeout when `Fast`, Aggregation
  always, Synchronization when attached, Collecting and Distributing when
  enabled, Defaulted while the partner is defaulted, Expired while
  expired), partner `Info` (defaulted until a PDU arrives), a status
  Current, Expired, or Defaulted, receive and transmit timers, and
  counters. Transmission: every 1 s when `Fast` else 30 s, and at once
  when the actor's own information changed (need to transmit); a passive
  LAG transmits only while its partner's state has Active. Reception:
  `Layer.Receive(now, member, pdu) Effects` stores the partner info,
  sets Current, and restarts the receive timer at three times 1 s or
  30 s by the partner's ShortTimeout bit; a member starts Current with
  the timer armed at link up, using the LAG's own rate until a PDU
  arrives; the timer's expiry sets Expired and restarts the same period,
  whose expiry sets Defaulted with default partner information. Selection: the lead is the member with the best partner
  (lowest partner system priority, then system ID); a member is attached
  when its partner's system ID and key equal the lead's and its status
  is not Defaulted; an attached member with the partner's Synchronization
  bit is enabled. Fallback: with `Fallback` set, members whose status is
  Defaulted are enabled as active-backup over those members; without it,
  they carry nothing. Why: this is OVS's `lacp_update_attached`
  (lead by `memcmp` of the partner priority tuple, detach on key or
  system ID mismatch, fallback on Defaulted), `lacp_run` (transmit on
  timer or when `info_tx_equal` fails), and its status transitions with
  `LACP_RX_MULTIPLIER` 3 (`lib/lacp.c` cited above); the timers 1 s and
  30 s are `LACP_FAST_TIME_TX` and `LACP_SLOW_TIME_TX`.
- The layer's timer facility is the switch's: `Layer.NextWake()` and
  `Layer.Wake(now) Effects` cover the delays, the transmit period, and
  the receive timeouts; `lag.Effects{Emissions []lag.Emission; Changed
  []string}` carries LACPDUs to send and the members whose enablement
  changed. Each layer keeps its own `Emission` type; `Switch.applyEffects`
  converts both onto one queue of `vswitch.Emission{Port, Frame}`, which
  `Drain` returns and the fabric's `injectEmission` consumes;
  `Switch.NextWake` and `Switch.Wake` take the earlier of the two layers'
  wakes and drive both. Why: one queue, one rule for every
  frame, as phase 1 and phase 3 decided.
- The relay and the hub stop choosing members. `port.Table.Transmit`
  reports a LAG as forwarding when any member forwards and returns no
  member; `bridge.Bridge` gains `Selector` (`SelectMember(lag string, f
  ethernet.Frame, vid vlan.ID) (string, bool)`) set through
  `SetSelector`, consulted for a LAG egress to fill `Egress.Member`, and
  a LAG with no selector or no member selected is recorded as an `Egress`
  drop with `ReasonNoMember` ("no-member"); the hub path, the routed
  egress in `assembleRouteResult`, and the fabric's `transmit` ask
  `Switch.SelectMember` the same way, and a BPDU emitted on a LAG port
  takes the selected member (an LACPDU is emitted on a member and takes
  none). `Derive` installs the cloned layer as the cloned relay's selector,
  as it re-installs the gate. The switch exposes `LagInfo(lag)` and
  `MemberInfo(member)`, passing through the layer's `Info` and `PortInfo`. Why: which cable a frame takes is the LAG's
  question, and three copies of the lowest-name rule were the tree's
  answer.
- The switch intercepts LACPDUs before the relay: a frame with EtherType
  0x8809 whose first payload octet is 1, arriving on a member port whose
  own link is up (the member's row, not the parent `ports.Receive`
  resolves to), goes to `lag.Receive` with outcome Consumed (`Layer`
  `lag`), or is dropped `unsupported-lacpdu` when it does not decode; on a port that is not a
  LAG member it is dropped by the relay as a reserved address as today.
  `ethernet.IsReserved` already covers 01-80-C2-00-00-02. Why: the
  spanning tree interception is the shape.
- Schema: `AggregationFacet` gains `bond_mode` (`BondMode` enum in its
  own `bond_mode.proto`: `BOND_MODE_UNSPECIFIED`, `_ACTIVE_BACKUP`, `_BALANCE_SLB`,
  `_BALANCE_TCP`; absent means unreported), `up_delay` and `down_delay`
  (`google.protobuf.Duration`, absent means unreported), `hash_basis`
  (uint32, absent means unreported), and `primary_interface_name` (string
  1..64, absent means the lowest member), numbered 3 through 7. A new
  package `flowseer.net.protocol.lacp.v1` holds, one declaration per file with
  the enums in `lacp_mode.proto`, `lacp_status.proto`, and
  `lacp_state_bit.proto`, `AggregatorState` (one row per LAG:
  `interface_name`, admin `mode` (`LacpMode` enum Off, Active,
  Passive), `fast`, `system_priority`, `system_id` (Eui48Address), `key`,
  `fallback_active_backup`; observed `partner_system_id`,
  `partner_system_priority`, `partner_key`, `selected_members` (repeated
  interface names), with each family file's file-level comment naming
  the absent Config and Event, as `stp/v1` does) and `PortState` (one row per member:
  `interface_name`, `aggregator_interface_name`, admin `port_priority`,
  `key`; observed actor and partner `LacpInfo` messages (system priority,
  system id, key, port priority, port id, `state` repeated `LacpStateBit`
  open enum with the eight bit positions), `status` (`LacpStatus` enum
  Current, Expired, Defaulted), `attached`, `enabled`, `lacpdus_tx`,
  `lacpdus_rx`, `bad_lacpdus`), each field with its absence contract and
  the MIB object it mirrors (`dot3adAggPortActorOperState`,
  `IEEE8023-LAG-MIB:1516`; `dot3adAggPortPartnerOperKey`, `:1355`;
  `dot3adAggPortAttachedAggID`, `:1386`; `dot3adAggPortStatsLACPDUsRx`,
  `:1624`; `dot3adAggPortStatsLACPDUsTx`, `:1694`). Why: the facet's
  own comment sends LACP facts to `net.protocol.lacp`, the bond settings
  are protocol-independent so they stay on the facet, and `stp/v1` is the
  shape for a protocol package whose rows carry admin and observed
  values together.
- `netmodel.Load` gains two parameters, `lacpAggregators
  []*lacpv1.AggregatorState` and `lacpPorts []*lacpv1.PortState`, and
  reads them with the facet into `lag.Config` with `Default` entries for
  an absent bond mode (active-backup), delays (0), and LACP mode (off);
  `netmodel.Lacp(sw) ([]*lacpv1.AggregatorState, []*lacpv1.PortState)`
  exports one row per LAG and per member from `Switch.LagInfo` and
  `Switch.MemberInfo`, the shape of `Stp`. Why: requirement 28.
- `CONCEPTS.md`'s aggregation vocabulary and the direction record's
  capability ladder line are unchanged; the vswitch README's ladder gains
  the `lag` layer entry and the `lag` package gets its README with the
  worked example of requirement 22.

## Requirements

Every example uses two switches `A` and `B`, each with ports `1/1/1`
through `1/1/4` and a LAG `lag1` whose members are `1/1/1` and `1/1/2`,
joined by two cables `A:1/1/1` to `B:1/1/1` and `A:1/1/2` to `B:1/1/2`, a
host `h1` on `A:1/1/3` and a host `h2` on `B:1/1/3`, every port untagged in
VLAN 10, and `Start` at `t0`, unless the example says otherwise.

1. LACPDU codec. Acceptance: `Encode` of a `PDU` with an actor of system
   priority 32768, system ID 02:00:00:00:00:0a, key 1, port priority
   32768, port ID 1, state Active|ShortTimeout|Aggregation produces a
   frame to 01-80-C2-00-00-02 with EtherType 0x8809 and a 110-octet
   payload whose octets 0 and 1 are 1 and 1, whose octet 2 is 1 and
   octet 3 is 20, and it round-trips through `Decode`; a payload with
   subtype 2 or a partner TLV length 19 is refused with
   `unsupported-lacpdu`.
2. balance-slb. Acceptance: `lag1` on `A` in `BalanceSLB` with LACP off;
   `h1` sends frames from MAC `a1` and from MAC `a2` to `h2`; every frame
   from `a1` crosses the same cable, and the two sources cross different
   cables for at least one pair of source MACs among `a1` through `a8`
   (the test picks two that the hash separates and asserts the
   separation and the constancy).
3. balance-tcp. Acceptance: `BalanceTCP`; two IPv4 UDP flows from `h1` to
   `h2` differing only in source port cross different cables for at least
   one port pair among 40000 through 40007, and every frame of one flow
   crosses the same cable; the injected IPv4 headers carry a valid
   checksum, or the hash would fall back to the layer 2 fields.
4. Active-backup failover. Acceptance: the default mode; every frame
   from `h1` crosses `A:1/1/1`; after `SetFault` cuts the first cable at
   `t1`, the next frame crosses `A:1/1/2` and `Snapshot().Devices["A"].Ports`
   shows `lag1` with `OperStatus` Up.
5. Delays. Acceptance: `UpDelay` 2 s, `DownDelay` 1 s; a cut of the first
   cable at `t1` keeps frames on `1/1/1` until `t1 + 1s`, each recorded
   as an `EntryLoss` with `cable-loss` on that cable since the fabric's
   `transmit` does not serialize on a link end with no negotiated speed,
   and moves them after; the cable restored at `t2`
   keeps frames on `1/1/2` until `t2 + 2s`.
6. LACP convergence. Acceptance: `lag1` on both switches `Active` and
   `Fast`; at `t0 + 3s` every member on both sides is attached and
   enabled, `MemberInfo` reads the partner system ID of the other switch,
   and both switches' `lag1` rows read `OperStatus` Up; `A`'s member
   `1/1/2`, whose partner (`B`'s `1/1/2` with `Key` 2) advertises a key
   other than the lead's, stays detached while `1/1/1` is enabled.
7. Fallback. Acceptance: `A` `Active` against a `B` whose LACP is `Off`:
   with `Fallback` true `A`'s members are enabled as active-backup after
   the Defaulted transition (`t0 + 6s` on fast timers), and `h1`'s frames
   cross `A:1/1/1`; with `Fallback` false no member is enabled, the LAG
   row stays Up on its members' links, and a frame from `h1` to `h2` is
   dropped with `no-member` in its journey.
8. Passive. Acceptance: `A` `Active`, `B` `Passive`: `B` transmits its
   first LACPDU only after receiving `A`'s, and the aggregation converges
   as in requirement 6; `A` and `B` both `Passive` never transmit and stay
   defaulted.
9. LACPDU handling at the switch. Acceptance: an LACPDU injected at
   `A:1/1/1` is Consumed with the `lag` layer step and `PortInfo`
   reads `LACPDUsRx` 1; one with a bad TLV length is dropped
   `unsupported-lacpdu` and counts `BadLACPDUs` 1; an LACPDU injected at
   `A:1/1/3` (no LAG) is dropped `reserved-address`.
10. Loader and export. Acceptance: a `LagInterface` with `bond_mode`
    BALANCE_SLB, `up_delay` 2 s, and an `AggregatorState` row with mode
    ACTIVE, `fast` true, key 7 load as that `lag.LAG`; a `LagInterface`
    without `bond_mode` reports the default `bond_mode` active-backup;
    after requirement 6 converges, the export's `PortState` rows read
    `attached` and `enabled` true with partner system IDs, the
    `AggregatorState` lists both members as selected, and every message
    `Lacp` returns passes `protovalidate`.
11. Diff and Validate. Acceptance: `lag.Diff` reports `mode` on a LAG
    changed to `BalanceTCP` and `priority` on a member; `Validate` refuses
    a `Primary` naming `1/1/3`, a member key on a port that is not a
    member, and `MinLinks` 3.

## Out of scope

Everything the parent lists; the rebalance interval (OVS moves hash
buckets between members by measured load; buckets here are assigned by
modulus, so nothing accumulates to move; the parent's Decision is
narrowed here as a fact); multi-chassis aggregation; LACP marker protocol
(subtype 2); a host with a LAG; collector max delay above 0.

## Units

### U1. The LACPDU codec

Files: `src/common/net/lacp/lacp.go`, `lacp_test.go`, `README.md`,
`src/common/net/ethernet/ethernet.go` (`EtherTypeSlowProtocols`)
After: none
Change: the codec of the first Decision; the README states the frame
layout with its source and the state bits.
Tests: `lacp_test.go`, requirement 1, a fixture that decodes an LACPDU
captured from Open vSwitch when one is in `docs/research`, else the
round trip only. Each is evidence for this unit.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/net src/common/internal/netpenguard`

### U2. The aggregation layer

Files: `src/common/netsim/vswitch/lag/config.go`, `layer.go`, `lacp.go`,
`hash.go`, `diff.go`, `config_test.go`, `layer_test.go`, `README.md`
After: U1
Change: `Config`, `Validate`, `Diff`, `New(cfg, ports)`, `Select`,
`LinkChange`, `Receive`, `Wake`, `NextWake`, `Info(lag)`,
`PortInfo(member)`, `Effects`, the modes, the hash, the delays, the LACP
exchange and selection, the counters, `Clone`.
Tests: `layer_test.go`, the layer-level facts behind requirements 2
through 8: `Select` per mode and per source, the failover and the delays
on `LinkChange` and `Wake`, and the LACP exchange between two layers the
test wires to each other as the spanning tree tests wire two bridges,
with the key mismatch, the fallback, and the passive case, each watched
failing first against a selection that returns the lowest member;
`config_test.go`, requirement 11. Each is
evidence for this unit.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/lag`

### U3. The switch

Files: `src/common/netsim/vswitch/port/port.go`, `port_test.go`,
`src/common/netsim/vswitch/bridge/bridge.go`, `result.go`,
`bridge_test.go`, `src/common/netsim/vswitch/config.go`, `switch.go`,
`diff.go`, `derive.go`, `switch_test.go`, `README.md`,
`src/common/netsim/vswitch/netmodel/netmodel_test.go`
After: U2
Change: `Config.LAG *lag.Config`; `Capabilities` reads `LayerLag` from
it or from a LAG port as today; `port.Table.Transmit` returns no member;
`bridge.Selector`, `SetSelector`, `ReasonNoMember`; `vswitch.Emission`;
the switch builds the layer from the configuration or, when nil, from
the port table's LAG ports with the defaults, wires it as the relay's
selector, intercepts LACPDUs, drives it from `LinkChange` on members
with the switch's own per-member point-to-point and speed maps, rewrites
the LAG row's operational state from the members' links, drives spanning
tree with the LAG's link from the enabled members, runs `Start`,
`LinkChange`, `Wake`, and `NextWake` whenever either layer is present,
unions the wakes, exports `LagInfo`, `MemberInfo`, and `SelectMember`;
`Diff` and `Derive` cover the layer; the routed egress consults the
selector; `netmodel_test.go`'s LAG egress expectation reads the member
the default layer selects.
Tests: `bridge_test.go`, a LAG egress with a selector that returns the
second member and one that returns nothing; `switch_test.go`,
requirement 9 and a member link down moving the spanning tree's view of
the LAG's speed. Each is evidence for this unit.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch`

### U4. The fabric

Files: `src/common/netsim/fabric/fabric.go`, `run.go`, `derive.go`,
`compare.go`, `lag_test.go`, `counters_test.go`, `README.md`
After: U3
Change: `build`, `startLayers`, and `SetFault` drive member link changes
into the switch whenever it has a layer to hear them and read the LAG's
operational state back from it instead of computing it from the cables;
`transmit` asks `Switch.SelectMember` for a LAG egress without a member,
and records an `EntryLoss` with `cable-loss` instead of serializing when
the chosen member's link end has no negotiated speed; LACPDU emissions
are transmissions on the member; `injectEmission` takes
`vswitch.Emission`; the README's LAG paragraph states the layer decides;
`counters_test.go`'s LAG expectations read the member the default layer
selects.
Tests: `lag_test.go`, requirements 2 through 8 end to end on the example
topology, with the journeys' cables asserted. Each is evidence for this
unit.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric`

### U5. Schema, loader, export, and records

Files: `spec/proto/flowseer/net/switching/v1/aggregation_facet.proto`,
`bond_mode.proto`, `spec/proto/flowseer/net/protocol/lacp/v1/aggregator_state.proto`,
`port_state.proto`, `lacp_info.proto`, `lacp_mode.proto`,
`lacp_status.proto`, `lacp_state_bit.proto`, `README.md`,
`src/common/netsim/vswitch/netmodel/netmodel.go`, `export.go`,
`report.go`, `lacp_test.go`, `src/common/netsim/README.md`,
`src/common/netsim/vswitch/README.md`
After: U4
Change: the schema of the Decisions, `buf generate`, the loader and
export mapping, the netsim README's package table gaining `vswitch/lag`
and the vswitch README's ladder gaining the layer.
Tests: `netmodel/lacp_test.go`, requirement 10 with `protovalidate`.
Each is evidence for this unit.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net src/common/netsim`

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net src/common/net src/common/netsim
go test -race ./src/common/net/... ./src/common/netsim/... ./src/common/internal/netpenguard/...
```

## Definition of done

- [ ] Verifier green for every changed path, `buf lint` included.
- [ ] The lag, vswitch, fabric, and netsim READMEs and the schema README
      match the landed API.
- [ ] This plan's `status` set with an outcome note under its title, and
      the parent's `Landed:` line for this phase filled.
- [ ] No plan labels in code, comments, or commit messages.

## Open questions

- Whether the transmit period should follow the partner's ShortTimeout
  bit rather than the LAG's own `Fast`, as 802.1AX's periodic machine
  does. The plan follows OVS, which transmits at its own rate and reads
  the partner's bit for the receive timeout only.
- Whether an LACPDU captured from the lab's Ruckus ICX exists under
  `docs/research`; the codec's fixture is the round trip until one does.
