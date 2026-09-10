---
title: Network Simulation Environment, Phase 1: Capability-Built Virtual Switch - Plan
type: feat
date: 2026-09-10
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: mixed
amends: docs/architecture/2026-09-09-mutation-shadow-projection-direction.md
parent: docs/plans/2026-09-10-1815-feat-netsim-network-environment-plan.md
---

# Network Simulation Environment, Phase 1: Capability-Built Virtual Switch - Plan

> Implemented. Every unit landed on 2026-09-10 through Herdr workers, one
> unit per worker, with the value packages placed under `src/common/net`
> at the user's direction after the first unit landed.

## Goal

`src/common` gains the network value packages and `src/common/netsim` a
trace type and a virtual switch
built from a port table and the capabilities its caller chooses: Ethernet
speeds, PoE, a relay, VLAN awareness, and link aggregation. The switch
answers what one frame does as a trace, ages and exports its forwarding
database, and two switches compare and diff. `netmodel` loads one from the
typed network model and reports every default and every facet it set
aside. The means is one package per capability keyed by port name,
composed by `vswitch`. The phase is wrong if a consumer needs a vendor's
dataplane.

## Decisions

The parent's Decisions hold: plain Go core, capability as presence, the
hub, bridge, switch ladder, one state per switch, the value types as
common packages. This phase adds:

- Frames use the library's own codec for Ethernet II and an 802.1Q tag
  stack. Why: `src/common/internal/netpenguard` confines `github.com/gopacket/`
  to `src/edge/netpen`; the tag layout is as `gopacket/layers/dot1q.go`
  (v1.7.1, module cache) decodes it.
- The trace vocabulary is a package of its own, `netsim/trace`, that every
  layer, the switch, and the fabric import: a step (layer, operation from
  `classify`, `filter`, `learn`, `lookup`, `replicate`, `rewrite`,
  `transmit`, `drop`, and detail), a trace (steps, outcome, reason), and a
  change record for diffs with a subject (kind and key: a port, a VLAN, a
  PSE group, a capability) and a field. A reason is a string constant
  declared by the package that emits it. Why: the prior art record shows
  three tools converging on this shape, and a shared type is what lets a
  routing layer or a cable add steps and reasons without touching `bridge`.
- Time is a `time.Time` the caller passes; nothing calls `time.Now()`. Why:
  aging must replay from a capture's timestamps and a comparison must see
  one instant.
- Absent switchport fields take the Q-BRIDGE-MIB `DEFVAL`s: admission
  `admitAll` (`spec/mib/ietf/Q-BRIDGE-MIB:1413`), ingress filtering false
  (`:1437`); aging takes the 300 s that `dot1dTpAgingTime` describes as the
  802.1D recommendation (`spec/mib/ietf/BRIDGE-MIB:778`). An absent PVID
  takes the port's single untagged VLAN when it has exactly one, else the
  port has no PVID and untagged ingress drops. A present PVID is used as
  reported even when the untagged set does not hold it, since a trunk with
  `pvid: 1` and an empty untagged set is the ordinary shape. Why:
  `DEFVAL { 1 }` (`Q-BRIDGE-MIB:1386`) is a bridge default, while an
  untagged set without a PVID is a mapper that did not read `dot1qPvid`.
- On a VLAN-aware bridge a frame is tagged when its outer tag is a C-TAG
  (0x8100) with a nonzero VID; a C-TAG with VID 0 is priority-tagged and,
  like an untagged frame, classifies to the PVID (`Q-BRIDGE-MIB:1379`). Any
  other outer tag is payload, kept in the tag stack and carried through;
  on a bridge without VLAN every tag is. Why: the model is one customer
  bridge component; a provider tag is never destroyed.
- Reserved group addresses 01-80-C2-00-00-00 through 0F are neither
  forwarded nor learned. Why: the IEEE Registration Authority lists them as
  reserved (https://standards.ieee.org/products-programs/regauth/grpmac/public/).
- A derived expected switch inherits the current dynamic FDB minus entries
  the expected configuration invalidates. Why: an empty expected FDB would
  flood every known unicast and report a difference on every frame.
- A LAG is one bridge port; members are not ports of their own. Egress to a
  LAG leaves on its lowest-named forwarding member. Why:
  `LagInterface.switchport` holds the VLAN membership, so a member's own
  facet is stale hardware state; a hash-based member choice would depend
  on payload the model does not read.
- Active speed is the configured speed with auto-negotiation off and the
  highest supported speed at full duplex with it on; an observed
  `active_speed_bps` overrides both. A port with auto-negotiation off and
  neither a setting nor an observation is unresolved, not an error. Why:
  nothing in the model embeds `EthernetSettings`, so a loaded port often
  has only capabilities and an observation, and a link-down port neither.
- PoE allocates by class at the PSE, per group, in priority order with the
  port name as tie-break: classes 0 and 3 draw 15.4 W, 1 draws 4 W, 2 draws
  7 W, 4 draws 30 W, 5 through 8 draw 45, 60, 75, and 90 W
  (https://en.wikipedia.org/wiki/Power_over_Ethernet, power levels table). A
  class over the group's remainder or the port's limit is denied. Why:
  `PseBudget.power_milliwatts` is the group's nominal power
  (`spec/mib/ietf/POWER-ETHERNET-MIB:420`), and denial by priority is the
  "PoE budget holds" invariant the shadow record names.
- Diff field names are the proto field names where a field exists;
  `aging_time` and `capability` are the simulator's own names, since no
  schema message carries them.
- The shadow record's "No emulator runs" sentence narrows to "no
  third-party emulator runs" and its Consequences gain the frame query;
  its Placement and Status paragraphs stay as they are. Why: the record
  rejected Open vSwitch and Batfish for modelling someone else's dataplane;
  a bridge computed from FlowSeer's typed `Config` is the projection it
  asks for. The virtual device record states the same reading; both revert
  together if the user keeps the sentence as written. Taken on 2026-09-10
  as the recommendation, since the virtual device record already read it
  that way; the user has not confirmed it.
- The value packages live under `src/common/net` (`net/netaddr`,
  `net/vlan`, `net/ethernet`), not at the common root. Why: they are one
  family of wire value types, and a directory named for the subject keeps
  the common root from filling with peers that only share a domain.
  User-directed on 2026-09-10, after the first unit landed.

## Requirements

This phase claims requirements 1 through 21 of the parent.

## Out of scope

Everything the parent lists, and cables, hosts, and peer negotiation,
which are phase 2.

## Units

### U1. Network value packages and the trace

Files: `src/common/net/netaddr/`, `src/common/net/vlan/`, `src/common/net/ethernet/`,
`src/common/netsim/trace/`, `src/common/README.md`
After: none
Change: `netaddr.MAC` is `[6]byte` with `String` (colon-separated lower
hex), `Parse`, `IsGroup` (the I/G bit), `HardwareAddr()`, and
`FromHardwareAddr`, which rejects any length but six; `netaddr.EUI64` is
the eight-byte sibling. `vlan.ID` is a `uint16` with `Valid()` for 1
through 4094, `vlan.PCP` a `uint8` with `Valid()` for 0 through 7, and
`vlan.Tag{TPID, PCP, DEI, VID}` the 802.1Q tag value, where a tag VID may
be 0. `ethernet.Frame` holds destination and source `netaddr.MAC`, a tag
stack outermost first, the payload EtherType, and the payload; `Decode`
consumes a tag while the EtherType is 0x8100 or 0x88A8 and rejects a short
frame or a truncated tag; `Encode` reverses it; `IsReserved(mac)` matches
01-80-C2-00-00-00 through 0F; the EtherType constants mirror the values of
`flowseer.net.packet.v1.EtherType`. `trace.Step`, `trace.Trace` with
`Outcome` (`Forwarded`, `Flooded`, `Dropped`) and `Reason`, and
`trace.Change{Layer, Subject{Kind, Key}, Field, From, To}` are as the
Decisions say. Every package imports only the standard library and `errs`.
The common README gains one row per package.
Tests: `netaddr_test.go`, parsing and both conversions; `vlan_test.go`,
the range edges; `ethernet_test.go`, requirement 1, a two-tag stack,
untagged, a truncated tag, the reserved range's edges.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/net src/common/netsim/trace src/common/README.md`

### U2. Port table

Files: `src/common/netsim/vswitch/port/`
After: U1
Change: `port.Table` is an ordered set of ports, each with a name, an
optional ifIndex, a kind (Physical, Lag, Other), admin and oper link state
(Up, Down, Unreported), an MTU (0 is unlimited), and a LAG parent, with
lookup by name, the members of a LAG, resolution of a member to its LAG,
validation, and cloning. A port forwards unless admin or oper is Down.
`Builder` has `Add(Port)`, `Range(pattern, from, to, attrs Port)`, which
formats the pattern with the number and ignores `attrs.Name`, and `Build`,
which rejects a pattern without exactly one `%d` verb, a duplicate name, a
parent that is not a LAG, and a LAG with a parent. `port.Layer` is a
string with a constant per layer a step or a capability can name: `port`,
`lag`, `ethernet`, `poe`, `relay`, `vlan`. `port.Diff(a, b Table)
[]trace.Change` reports ports added and removed and changes to
`admin_status`, `mtu`, and `lag_parent`.
Tests: `port_test.go`, requirement 2, resolution of a member and a plain
port, each builder rule, a diff with one added port and one admin change.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/port`

### U3. Ethernet speeds and PoE

Files: `src/common/netsim/vswitch/phy/`
After: U2
Change: `phy.Config{Ethernet map[string]Ethernet; PoE *PoE}`; a nil map is
the Ethernet capability absent, a nil `PoE` the PoE capability absent.
`Ethernet` holds supported speeds, auto-negotiation support, the setting,
and an optional observed active speed and duplex. `PoE` holds groups with a
power budget and PSE ports with a group, maximum class (a spec input; the
loader sets 8 and reports it, since no `net/phy` message carries one),
enabled flag, optional limit, priority, and the attached PD's class.
`Resolve` applies the speed rule from Decisions. `Allocate` walks each
group's ports by priority then name, charges `ClassPowerMW(class)`, and
records per port an allocation or a denial (`disabled`, `budget`, `limit`,
`class-unsupported`) and per group the remainder. `Validate(ports)` rejects
a name absent from the table, a LAG, a class above 8, an unknown group, and
a setting outside the supported set. `phy.Diff(a, b Config)
[]trace.Change` covers `speed_bps`, `auto_negotiation_enabled`, `enabled`,
`power_limit_milliwatts`, and `priority` per port and `power_milliwatts`
per group.
Tests: `phy_test.go`, requirements 4 and 5, a class above the port's
maximum, an observed speed overriding the resolution, an unresolved
link-down port, a diff with one group and one port change.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/phy`

### U4. Bridge

Files: `src/common/netsim/vswitch/bridge/`
After: U1, U2
Change: `bridge.Config{AgingTime; VLAN *VLAN}`. `VLAN` holds the table (id
to name) and per-port switchports (PVID with presence, sorted tagged and
untagged sets, ingress filtering, admission as All, TaggedOnly,
UntaggedAndPriorityTaggedOnly); nil is the VLAN capability absent. The FDB
is keyed by filtering database id and `netaddr.MAC`; a VLAN-aware bridge
uses the VID as the id, a bridge without VLAN uses id 0 for every frame.
`bridge.Seed{FID, MAC netaddr.MAC, Port, Static bool, LearnedAt time.Time}` is an
entry to preload; `Learn(seeds)` adds them and `Entries()` returns them
sorted by id then MAC. `Validate(ports)` rejects a switchport naming an
absent port or a LAG member, a VLAN in both sets, an id outside 1 through
4094, and any switchport when `VLAN` is nil. `Forward(now, port, f)
trace.Trace` runs the pipeline in the order the parent's requirements 6
through 15 and 21 describe: resolve a member to its LAG, require the
ingress port forwarding, drop reserved destinations, classify, apply
admission, filtering, and VLAN existence when VLAN-aware, learn a unicast
source unless a static entry holds the key, look up the destination (group
or miss floods), and emit one egress per forwarding member other than the
ingress port in its egress tag form, or that port's `mtu-exceeded` drop.
`Peek` is `Forward` without learning; `Age(now)` ages dynamic entries. The
drop reasons are `port-down`, `reserved-address`, `admission`,
`ingress-filter`, `undefined-vlan`, `no-pvid`, `same-port`,
`mtu-exceeded`. `bridge.Diff(a, b Config) []trace.Change` covers VLANs
added, removed, and renamed (`name`), per port `pvid`, `tagged_vlan_ids`,
`untagged_vlan_ids`, `ingress_filtering`, and `frame_admission`, and
`aging_time`. A `Bridge` is not safe for concurrent use; its doc comment
says so.
Tests: `bridge_test.go`, table-driven over requirements 6 through 15 and
21 as far as they name the bridge, each validation rule, an S-tagged frame
carried through under both egress forms, an FDB entry whose port is down
(the frame drops with `port-down` on egress rather than flooding), the
bridge half of requirement 20.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/bridge`

### U5. The virtual switch

Files: `src/common/netsim/vswitch/`
After: U3, U4
Change: `vswitch.Config{Ports port.Table; Phy *phy.Config; Bridge
*bridge.Config}`. `Capabilities()` returns the sorted `[]port.Layer` the
config implies; `Validate()` runs each present layer's validation against
the table. `New(cfg)` copies the config and builds each present layer.
`Forward` and `Peek(now, port, f)` return the bridge trace, or, without a
bridge, the hub trace the switch computes itself: the ingress port must
forward, then one `replicate` step per other forwarding port with the
frame untouched or its `mtu-exceeded` drop, outcome `Flooded`; the
layers' state (entries, allocations, resolved speeds, the
config) is readable. `Compare(a, b *Switch, now, port, f) Comparison` runs
`Peek` on both and reports both traces and `Same` (equal outcome, reason,
classified id, and egress port and tag sets). `Diff(a, b Config)
[]trace.Change` concatenates `port.Diff`, a `capability` change per layer
present in one config only, `phy.Diff`, and `bridge.Diff`, so a new layer
adds one call. `Derive(cur *Switch, cfg Config) (*Switch, error)` builds a
switch from `cfg` seeded with every dynamic entry of `cur` whose port and
VLAN `cfg` still admits.
Tests: `switch_test.go`, requirements 3, 18, 19, and 20, `Same` reporting
true over a small frame table, an empty diff of equal configs, the hub
half of requirement 14 and a hub with a down port.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch`

### U6. Network model boundary, documentation, and the records

Files: `src/common/netsim/vswitch/netmodel/`, `src/common/netsim/README.md`,
`src/common/netsim/vswitch/README.md`, `src/common/README.md`,
`docs/architecture/2026-09-09-mutation-shadow-projection-direction.md`,
`docs/architecture/2026-09-10-virtual-device-direction.md`, `CONCEPTS.md`
After: U5
Change: `netmodel.Load(now, ifaces, vlans, fdb, budgets, want
[]port.Layer) (vswitch.Config, []bridge.Seed, Report, error)`. An empty
`want` infers the set from the facets present: `relay` always, `vlan` from
any `SwitchportFacet`, `ethernet` from any `EthernetFacet`, `poe` from any
`PoeFacet` or `PseBudget`, `lag` from any LAG interface; a non-empty `want`
is the set built, and a facet outside it is skipped and reported. The port
table takes every interface: kind from the `oneof` (`physical`, `lag`,
else Other), `if_index`, `lag_parent`, `mtu` (absent is unlimited; an
explicit 0 is reported and treated as unlimited), and `AdminStatus` and
`OperStatus` (UP is Up, unspecified is Unreported, every other value is
Down). The Ethernet map takes `EthernetFacet` capabilities, applied
auto-negotiation, and active speed with the setting left unset. PoE takes
the copper arm's `PoeFacet`, `PoeSettings`, and `PoePortDetail.pse_group`;
a PSE port without `poe_detail` is skipped and reported, and a `PseBudget`
without `power_milliwatts` loads with budget 0 and is reported. The bridge
takes each `SwitchportFacet` of a non-member port with the Decisions
defaults; a member's facet is skipped and reported. The VLAN table is the
`Vlan` rows plus every id any port is a member of. `FdbEntry` rows not
`INVALID` become seeds, `STATIC` kinds static, every other kind dynamic
with `LearnedAt` equal to `now`. `Report` lists the capability set and how
each was chosen, skipped rows and facets, ports without a switchport
facet, and every default by port name. `FdbEntries` and `Poe` export
`FdbEntry` rows, a `PseBudget` per group, and a `PoeFacet` per port. The
`netsim` README lists its packages; the `vswitch` README shows a
spec-built switch, a frame, and its trace on two states, then the
capabilities, the rules with their sources, the defaults, the reasons, and
the single-thread contract. The common README's admission paragraph names
`netmodel` as the one package that imports `generated/go/proto` and why,
and the table gains a `netsim` row. The shadow record is edited as the
Decisions describe; the virtual device record is checked against the
landed API. `CONCEPTS.md` gains "Virtual Device" under Network model: its
capabilities are the layers it is built with, and the inventory's
`Capability` is the coarser area a Binding reports.
Tests: `netmodel_test.go`, requirements 13, 16, and 17, a LAG with two
members, an `INVALID` row, a wanted set without `vlan`, a port without
`poe_detail`, every built message passing `protovalidate.Validate`, and
the ICX7150 capture under `docs/research/device-inventory/lab/` loading
with a report that names no skipped facet.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim src/common/README.md docs/architecture CONCEPTS.md`

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim docs/architecture src/common/README.md CONCEPTS.md
go test -race ./src/common/netsim/... ./src/common/internal/netpenguard/...
```

## Definition of done

- [x] Verifier green for every changed path.
- [x] Both `netsim` READMEs, the `src/common/README.md` row, and both
      direction records match the landed API.
- [x] This plan's `status` set with an outcome note under its title, and
      the parent's `Landed:` line for this phase filled.
- [x] No plan labels in code, comments, or commit messages.

## Open questions

- Narrowing the shadow record's "no emulator" to third-party emulators
  was applied without the user's confirmation (see Decisions). If the
  sentence should stay as written, revert that edit and the same claim in
  the virtual device record together.
- Whether an absent PVID should fall back to VLAN 1 per the MIB `DEFVAL`
  rather than to the single untagged VLAN. Keep the Decisions rule unless
  the lab captures show every mapper reports a PVID with an untagged set.
