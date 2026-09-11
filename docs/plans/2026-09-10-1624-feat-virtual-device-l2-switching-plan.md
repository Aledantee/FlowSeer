---
title: Virtual Device, Sizable Ports and Basic L2 Switching - Plan
type: feat
date: 2026-09-10
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: superseded
superseded_by: docs/plans/2026-09-10-1815-feat-netsim-network-environment-plan.md
execution: mixed
amends: docs/architecture/2026-09-09-mutation-shadow-projection-direction.md
---

# Virtual Device, Sizable Ports and Basic L2 Switching - Plan

> Superseded on 2026-09-10 by the network simulation environment parent plan; its phase 1 plan carries this plan's units with capability-based composition and one state per switch.

## Goal

A reusable Go library under `src/common/netsim` builds a virtual switch of
any size from a spec or from FlowSeer's typed network model: a port table
with caller-chosen identifiers, per-port Ethernet speeds, PoE budgets and
classes, and an IEEE 802.1Q bridge over the ports. It keeps a current and an
expected state of one device, diffs them, and answers what one Ethernet frame
does on each: classification, learning, egress ports and tag form, or the
drop reason. The means is one package per layer over a shared port table,
each pure and deterministic, composed by the `vswitch` package. The plan is wrong
if a consumer needs one vendor's dataplane rather than the standard's, since
the model would then be a vendor emulator and the record it amends would say
something else.

## Decisions

- Placement is `src/common/netsim`, the home of network simulation, with
  the switch simulator under `netsim/vswitch` and the frame codec at
  `netsim/frame` because a link, host, or fabric simulator will share it.
  User-directed. The simulators import nothing from `generated/`, which
  keeps the directory's rule; the one protobuf boundary,
  `netsim/vswitch/netmodel`, is documented in `src/common/README.md` as the
  exception, the way the SNMP mappers once were. `AGENTS.md`'s "no domain
  types" line is left as it is and named under Open questions, since it is
  a policy surface.
- The virtual device is the engine of the mutation shadow projection, and
  that record's "No emulator runs" narrows to "no third-party emulator
  runs". Why: the record rejected Open vSwitch and Batfish for modelling
  someone else's dataplane from someone else's inputs; a bridge computed
  from FlowSeer's typed `Config` over the standard's rules is the projection
  it asks for, with a frame query added. The record is `proposed-direction`
  and is edited in place: its Placement bullet keeps the invariants and the
  preview in the device service's `internal/` and names `vswitch` as the
  engine they call, and its Status paragraph keeps the gate deferred while
  releasing the engine, which this plan builds. Recorded with the layer
  design in `docs/architecture/2026-09-10-virtual-device-direction.md`.
  Unconfirmed.
- One package per layer, all keyed by port name, composed by `vswitch`. The
  port table (`port`) owns identity, kind, LAG membership, and link state;
  `phy` owns speeds and PoE; `bridge` owns switching; `vswitch` owns the pair
  and the diff; `netmodel` owns the protobuf boundary. Why: this is the
  facet-versus-table split of
  `docs/architecture/2026-08-20-network-model-structure-direction.md`
  applied to computation, so a new layer is a new package and a new field
  on `vswitch.Config`, and no layer imports another except `port`. The layers
  hold plain Go types keyed by port name, VLAN id, and `[6]byte`, cheap to
  clone and compare, so tests of the rules need no `protovalidate` fixture.
- A device is sized by a spec. `port.Builder` adds ports singly or by a
  numbered range under a caller-supplied naming pattern, and every per-port
  attribute is a map from name. Why: the lab holds a 24-port ICX7150 with
  four SFP+ uplinks, an SG220, and a LANCOM, and each names ports
  differently.
- Frames use the library's own codec for Ethernet II and an 802.1Q tag
  stack. Why: `src/common/internal/netpenguard` confines `github.com/gopacket/`
  to `src/edge/netpen`; the tag is four bytes of PCP, DEI, VID, and next
  EtherType as `gopacket/layers/dot1q.go` (v1.7.1, module cache) decodes it.
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
  `DEFVAL { 1 }` (`Q-BRIDGE-MIB:1386`) is a bridge default, while a facet
  with an untagged set and no PVID is a mapper that did not read
  `dot1qPvid`. Every default taken is in the load report.
- A frame is tagged when its outer tag is a C-TAG (0x8100) with a nonzero
  VID; a C-TAG with VID 0 is priority-tagged and, like an untagged frame,
  classifies to the PVID (`Q-BRIDGE-MIB:1379`). An S-TAG or any other outer
  EtherType is untagged data to this bridge: the codec keeps it in the tag
  stack, and egress pushes a C-TAG outside it or leaves it alone rather
  than rewriting it. Why: the model is one customer bridge component and
  provider bridging is out of scope; a provider tag is carried through
  untouched, never destroyed.
- A trace is a list of steps, each with a layer, an operation from a fixed
  vocabulary (`classify`, `filter`, `learn`, `lookup`, `replicate`,
  `rewrite`, `transmit`, `drop`), and detail, ending in one outcome; ingress
  ends in one decision (a port, a flood set, or a drop) and egress rewrites
  each copy. Why: Packet Tracer's per-layer operations, Batfish's per-hop
  actions, and bmv2's ingress-decides-egress-rewrites split converge on
  this shape, and a fixed vocabulary lets a later L3 layer add steps
  without changing the trace type
  (`docs/architecture/2026-09-10-network-simulation-prior-art-research.md`).
- Reserved group addresses 01-80-C2-00-00-00 through 0F are neither
  forwarded nor learned. Why: the IEEE Registration Authority lists them as
  the 802.1D and 802.1Q reserved addresses
  (https://standards.ieee.org/products-programs/regauth/grpmac/public/).
- The expected bridge inherits the current dynamic FDB minus entries the
  expected configuration invalidates. Why: learned entries are not intended
  state, yet an empty expected FDB would flood every known unicast and
  report a difference on every frame.
- A LAG is one bridge port; members are not ports of their own. Egress to a
  LAG leaves on its lowest-named forwarding member. Why:
  `PhysicalInterface.lag_parent` names the aggregate and
  `LagInterface.switchport` holds its VLAN membership, so a member's own
  facet is stale hardware state and the loader skips it; a hash-based
  member choice would depend on payload the model does not read.
- Active speed is the configured speed with auto-negotiation off and the
  highest supported speed at full duplex with it on; an observed
  `active_speed_bps` overrides both. A port with auto-negotiation off, no
  configured speed, and no observation resolves as unresolved, not as an
  error; only a configured speed outside the supported set is an error.
  Why: the network model carries no requested `EthernetSettings` on an
  interface (nothing embeds that message), so a loaded port often has only
  capabilities and an observation, and a link-down port has neither.
- PoE allocates by class at the PSE, per group, in priority order with the
  port name as tie-break: classes 0 and 3 draw 15.4 W, 1 draws 4 W, 2 draws
  7 W, 4 draws 30 W, 5 through 8 draw 45, 60, 75, and 90 W
  (https://en.wikipedia.org/wiki/Power_over_Ethernet, power levels table). A
  class over the group's remainder or the port's limit is denied. Why:
  `PseBudget.power_milliwatts` is the group's nominal power
  (`spec/mib/ietf/POWER-ETHERNET-MIB:420`), the class is what a PSE knows
  before it measures, and denial by priority is the "PoE budget holds"
  invariant the shadow record names.
- One plan, six units. Why: every unit touches `src/common/netsim` or the
  docs that describe it, one cluster under
  `.claude/skills/plan/references/phases.md`.

## Requirements

1. The codec round-trips a tagged frame. Acceptance: decoding
   `01 00 5e 00 00 fb  00 11 22 33 44 55  81 00  a0 64  08 00` plus payload
   yields one tag with PCP 5, DEI false, VID 100, EtherType 0x0800, and
   encoding reproduces the bytes.
2. A port table is built to any size under a caller's naming. Acceptance:
   `Range("1/1/%d", 1, 24, port.Port{Kind: port.Physical})`, then
   `Range("1/3/%d", 1, 4, ...)`, then `Add(port.Port{Name: "mgmt"})` yields
   29 ports in insertion order; a second `Add(port.Port{Name: "1/1/1"})`
   fails with the duplicate name as an attribute.
3. Speeds resolve per port. Acceptance: a port supporting 10, 100, and 1000
   Mb/s with auto-negotiation on resolves to 1000 full; off with speed 100
   it resolves to 100; speed 2500 fails validation naming the port.
4. PoE allocation honours budget, priority, and limit. Acceptance: group 1
   with 60 W and three class-4 ports at critical, high, low: two allocate
   30 W and the third is denied with reason `budget`; at 90 W all three
   allocate; a class-4 port with limit 15400 mW is denied with `limit`.
5. Untagged ingress classifies to the PVID and learns. Acceptance: port
   `1/1/1` PVID 10, untagged {10}; an untagged frame from
   `00:11:22:33:44:55` to an unknown unicast floods to every other
   forwarding member of VLAN 10, not to `1/1/1`, and the FDB gains
   `(10, 00:11:22:33:44:55) -> 1/1/1` kind dynamic.
6. Admission and ingress filtering drop as the MIB says. Acceptance:
   admission `TAGGED_ONLY` drops an untagged frame and a priority-tagged
   frame (C-TAG, VID 0) with reason `admission`; admission
   `UNTAGGED_AND_PRIORITY_TAGGED_ONLY` drops a frame tagged 20 and
   classifies the priority-tagged frame to the PVID; admission `ALL` admits
   all three; filtering true on a port without VLAN 20 drops a frame tagged
   20 with `ingress-filter`; filtering false forwards it within VLAN 20.
7. Egress tag form follows the port's set. Acceptance: VLAN 20 tagged on
   `1/1/2`, untagged on `1/1/3`; a frame tagged 20 from `1/1/4` leaves
   `1/1/2` with a C-TAG VID 20 carrying the ingress PCP and DEI and leaves
   `1/1/3` untagged.
8. A known unicast goes to one port, never back out the ingress port.
   Acceptance: after R5, a frame to that MAC in VLAN 10 from `1/1/2`
   egresses only `1/1/1`; from `1/1/1` it drops with `same-port`.
9. A down port neither ingresses nor egresses. Acceptance: `1/1/3` admin
   DOWN is absent from every flood set and a frame injected on it drops
   with `port-down`.
10. Dynamic entries age on the caller's clock; static entries do not move.
    Acceptance: an entry learned at t0 answers at t0 + 299 s and is gone
    after `Age(t0 + 301 s)`; a static entry survives, and a frame from its
    MAC on another port leaves it unchanged.
11. Reserved addresses drop. Acceptance: a frame to `01:80:c2:00:00:00`
    drops with `reserved-address` on every port and the FDB is unchanged.
12. A LAG forwards as one port. Acceptance: `lag1` with members `1/1/5` and
    `1/1/6`, VLAN 10 tagged; a frame ingressing `1/1/6` traces ingress port
    `lag1`, and a flood in VLAN 10 from `1/1/1` lists one egress `lag1` on
    member `1/1/5`; a `PhysicalInterface` with `lag_parent: "lag1"` and its
    own switchport facet loads as a member whose facet the report lists as
    skipped.
13. Loading from the network model reports what it assumed. Acceptance: an
    `Interface` with a `physical` arm, `untagged_vlan_ids: [30]`, no PVID,
    and no `frame_admission` loads as PVID 30, admission ALL, both listed
    as defaults by port name; an interface without a switchport facet is a
    port with no bridge membership, listed as such.
14. The FDB and PoE state export as `net/switching` and `net/phy` rows.
    Acceptance: after R5 the export holds one `FdbEntry` (`vlan_id: 10`,
    that MAC, `interface_name: "1/1/1"`, `DYNAMIC`, `ACTIVE`); after R4 a
    `PseBudget` for group 1 and a `PoeFacet` per port with
    `allocated_power_milliwatts`; every message passes `protovalidate`.
15. The device compares a frame across its two states. Acceptance: current
    has `1/1/2` untagged in VLAN 10, expected moves it to 20; a frame from
    `1/1/1` to an unknown unicast in VLAN 10 reaches `1/1/2` on current and
    not on expected, and the comparison reports `Same: false` with both
    traces.
16. The expected device keeps learned entries the new configuration still
    admits. Acceptance: the R15 device is built with two dynamic seeds,
    `(10, a) -> 1/1/2` and `(10, b) -> 1/1/1`; the expected FDB holds the
    second and not the first.
17. The diff names every changed field across layers. Acceptance: the R15
    pair diffs to two changes on `1/1/2`, `untagged_vlan_ids` `[10]` to
    `[20]` and `pvid` 10 to 20, with field names spelled as in the proto
    schema; adding a port in expected diffs as a port added; changing a
    PSE group budget diffs as one `power_milliwatts` change.

## Out of scope

- Spanning tree (every port forwards), multicast filtering (group addresses
  flood as basic filtering services, `Q-BRIDGE-MIB:619`), provider
  bridging, and `DOT1Q_TUNNEL`.
- Anything above L2; the IP facet is ignored.
- Link partners, negotiation with a peer, and links between devices.
- Optics, transceiver diagnostics, MAU types, and counters.
- Applying a typed `MutationIntent`; today's only arm is
  `InterfaceDescriptionChange`, which affects nothing modelled. Expected
  state loads as a snapshot.
- A `service.Module` leaf, telemetry, host wiring, and vendor behaviour
  such as FastIron dual-mode ports.

## Units

### U1. Frame codec

Files: `src/common/netsim/frame/frame.go`,
`src/common/netsim/frame/frame_test.go`
After: none
Change: `frame.Frame` holds destination and source as `[6]byte`, a tag stack
outermost first (TPID, PCP, DEI, VID), the payload EtherType, and the
payload. `Decode` reads Ethernet II and consumes a tag while the EtherType
is 0x8100 or 0x88A8; `Encode` reverses it. Decode rejects fewer than 14
bytes or a truncated tag with the byte count as an attribute.
`Frame.IsGroup()` reads the I/G bit; `IsReserved(addr)` matches
01-80-C2-00-00-00 through 0F. Imports only the standard library and `errs`.
Tests: `frame_test.go`, R1, a two-tag stack, untagged, truncated tag, the
reserved range's edges.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/frame`

### U2. Port table

Files: `src/common/netsim/vswitch/port/port.go`,
`src/common/netsim/vswitch/port/builder.go`,
`src/common/netsim/vswitch/port/diff.go`,
`src/common/netsim/vswitch/port/port_test.go`
After: none
Change: `port.Table` is an ordered set of ports, each with a name, an
optional ifIndex, a kind (Physical, Lag, Other), admin and oper link state
(Up, Down, Unreported), and a LAG parent, with lookup by name, `Members
(lag)`, `Resolve(name)` (a member's LAG or the port itself), `Validate()`,
and `Clone()`. A port forwards unless admin or oper is Down. `Builder` has
`Add(Port)`, `Range(pattern, from, to, attrs Port)` formatting the pattern
with the number, and `Build`, which rejects a duplicate name, a parent that
is not a LAG, and a LAG with a parent. `port.Change{Layer, Port, Field,
From, To}` is the diff record every layer returns, declared here because
every layer already imports `port`; `port.Diff(a, b Table) []Change` reports
ports added and removed and changes to `admin_status` and `lag_parent`.
Field names throughout are the proto field names. `port.Step{Layer,
Operation, Detail}` and the operation constants from Decisions are declared
here for the same reason. Every other layer holds a map from port name and
validates against a `Table`.
Tests: `port_test.go`, R2, `Resolve` on a member and a plain port, each
builder rule, a diff with one added port and one admin change.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/port`

### U3. Ethernet speeds and PoE

Files: `src/common/netsim/vswitch/phy/ethernet.go`,
`src/common/netsim/vswitch/phy/poe.go`,
`src/common/netsim/vswitch/phy/diff.go`,
`src/common/netsim/vswitch/phy/phy_test.go`
After: U2
Change: `phy.Config` holds per-port Ethernet (supported speeds,
auto-negotiation support, the setting, an optional observed active speed and
duplex) and a PSE model: groups with a power budget, and PSE ports with a
group, capability, maximum class (a spec input; the loader sets 8 and
reports it, since no `net/phy` message carries a per-port maximum), enabled
flag, optional limit, priority (Critical, High, Low), and the attached PD's
class if any. `Resolve` applies the speed rule from Decisions and rejects a
setting outside the supported set or auto-negotiation where unsupported.
`Allocate` walks each group's ports by priority then name, charges
`ClassPowerMW(class)`, and records per port an allocation or a denial
(`disabled`, `budget`, `limit`, `class-unsupported`) and per group the
remainder. `Validate(ports)` rejects a name absent from the table, a LAG, a
class above 8, and an unknown group. `phy.Diff(a, b Config) []port.Change`
covers `speed_bps`, `auto_negotiation_enabled`, `enabled`,
`power_limit_milliwatts`, `priority`, and `power_milliwatts` per group.
Tests: `phy_test.go`, R3 and R4, a class above the port's maximum, an
observed speed overriding the resolution, an unresolved link-down port, a
diff with one group and one port change.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/phy`

### U4. Bridge

Files: `src/common/netsim/vswitch/bridge/config.go`,
`src/common/netsim/vswitch/bridge/bridge.go`,
`src/common/netsim/vswitch/bridge/fdb.go`,
`src/common/netsim/vswitch/bridge/trace.go`,
`src/common/netsim/vswitch/bridge/diff.go`,
`src/common/netsim/vswitch/bridge/bridge_test.go`
After: U1, U2
Change: `bridge.Config` holds the VLAN table (id to name), per-port
switchports (PVID with presence, sorted tagged and untagged sets, ingress
filtering, admission as All, TaggedOnly, UntaggedAndPriorityTaggedOnly),
and the aging time. `Validate(ports)` rejects a name absent from the table
or naming a LAG member, a VLAN in both sets, and an id outside 1 through
4094. `New(cfg, ports)` copies both. `Forward(now, port, f) Trace` runs the
pipeline in Decisions order: resolve a member to its LAG, require the
ingress port forwarding, drop reserved destinations, classify by a nonzero
C-TAG VID or else the PVID, apply admission, filtering, and VLAN existence,
learn a unicast source on `(vid, port)` unless a static entry holds the key,
look up the destination (group or miss floods), and emit one egress per
forwarding member other than the ingress port, naming the port, the LAG
member, and the frame: on a tagged member the outer C-TAG is rewritten to
the classified VID with the ingress PCP and DEI, or pushed when the frame
had none; on an untagged member the outer C-TAG is popped. `Peek` is
`Forward` without learning; `Age(now)` drops dynamic entries older than the
aging time; `Learn` seeds; `Entries()` sorts by VID then MAC. `Trace`
is the step list from Decisions, with `Outcome` (`Forwarded`, `Flooded`,
`Dropped`) and, when dropped, the reason as one of `port-down`,
`reserved-address`, `admission`, `ingress-filter`, `undefined-vlan`,
`no-pvid`, `same-port`; `Step` and its operation vocabulary live in
`port` so every layer can append them. `bridge.Diff(a, b Config) []port.Change` covers
VLANs added, removed, and renamed (`name`) and per port `pvid`,
`tagged_vlan_ids`, `untagged_vlan_ids`, `ingress_filtering`, and
`frame_admission`. A `Bridge` is not safe for concurrent use; its doc
comment says so.
Tests: `bridge_test.go`, table-driven over R5 through R12, each validation
rule, an S-tagged frame carried through untouched under both egress forms,
an FDB entry whose port is down (the frame drops with `port-down` on
egress rather than flooding), the R17 bridge half of the diff.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/bridge`

### U5. The virtual switch

Files: `src/common/netsim/vswitch/switch.go`,
`src/common/netsim/vswitch/diff.go`,
`src/common/netsim/vswitch/switch_test.go`
After: U3, U4
Change: `vswitch.Config{Ports, Phy, Bridge}` validates each layer against
the port table and clones. `New(current, expected Config, seeds, now)`
builds both bridges, seeds the current FDB, and carries its entries into the
expected bridge minus those whose VLAN or port membership the expected
config no longer has; `Same(cfg, seeds, now)` builds an equal pair.
`Compare(now, port, f)` runs `Peek` on both and reports both traces and
`Same` (equal drop reason, VID, and egress port and tag sets). `Forward`
runs `Forward` on both. `Power()` and `Speeds()` return both allocations
and resolutions. `Diff() []port.Change` concatenates `port.Diff`,
`phy.Diff`, and `bridge.Diff` in that order, so a new layer adds one call.
Tests: `switch_test.go`, R15 through R17, `Same` reporting `Same: true`
over a small frame table, an empty diff of equal configs.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch`

### U6. Network model boundary, documentation, and the amended record

Files: `src/common/netsim/vswitch/netmodel/netmodel.go`,
`src/common/netsim/vswitch/netmodel/netmodel_test.go`,
`src/common/netsim/README.md`, `src/common/netsim/vswitch/README.md`,
`src/common/README.md`,
`docs/architecture/2026-09-09-mutation-shadow-projection-direction.md`,
`docs/architecture/2026-09-10-virtual-device-direction.md`, `CONCEPTS.md`
After: U5
Change: `netmodel.Load(ifaces, vlans, fdb, budgets) (vswitch.Config,
[]bridge.Seed, Report, error)` builds the port table from every interface
(`physical`, `lag`, else Other; `PhysicalInterface.lag_parent` becomes the
LAG parent; `AdminStatus` UP is Up, DOWN and TESTING are Down, else
Unreported; `OperStatus` UP is Up, DOWN, TESTING, NOT_PRESENT,
LOWER_LAYER_DOWN, and DORMANT are Down, else Unreported), the `phy` config
from `EthernetFacet` capabilities, applied auto-negotiation, and active
speed, with the setting's speed left unset, and from the copper arm's
`PoeFacet`, `PoeSettings`, and `PoePortDetail.pse_group`, and the bridge
config from each `SwitchportFacet` of a non-member port with the Decisions
defaults; a member's facet is skipped and reported. The VLAN table is the
`Vlan` rows plus every id any port is a member of. `DYNAMIC` and `STATIC`
rows not `INVALID` become seeds; `Report` lists skipped rows and facets,
ports without a switchport facet, and every default by port name.
`FdbEntries` and `Poe` export `FdbEntry` rows, a `PseBudget` per group, and
a map from port name to `PoeFacet`. The `netsim` README says what the
directory is for and lists `frame` and `vswitch` with room for later
simulators; the `vswitch` README shows a spec-built 8-port switch with one
PoE group, a frame, and its trace on both states, then the rules with their
sources, the defaults, the drop and denial reasons, the single-thread
contract, and how a layer is added. The common README gains a `netsim` row and names `netmodel` as the one package there
that imports `generated/go/proto`, with the reason. The shadow record is
edited as the Decisions describe: its opening Decision paragraph, its
Placement bullet, its Status paragraph, its Open vSwitch paragraph ("no
third-party emulator runs"), and its Consequences (the frame query and PoE
allocation). The virtual device record and the architecture README row,
both added with this plan, are checked against what landed. `CONCEPTS.md`
gains "Virtual Device" under Network model.
Tests: `netmodel_test.go`, R13 and R14, a LAG with two members, an `INVALID`
row, every built message passing `protovalidate.Validate`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim src/common/README.md docs/architecture CONCEPTS.md`

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim docs/architecture src/common/README.md CONCEPTS.md
go test -race ./src/common/netsim/... ./src/common/internal/netpenguard/...
```

The second command proves the codec kept `gopacket` out of the main module. A
smoke check, not a gate: build a device from the ICX7150 capture under
`docs/research/device-inventory/` and read the traces of a few frames for a
plausible VLAN assignment and PoE allocation.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] Both `netsim` READMEs and the `src/common/README.md` row and
      exception land with the code.
- [ ] Both direction records match the landed API.
- [ ] This plan's `status` set, with an outcome note under its title.
- [ ] No plan labels in code, comments, or commit messages.

## Open questions

- `AGENTS.md` says `src/common/` holds "no domain types". The core packages
  keep it; `netmodel` imports `generated/go/proto`. Whether that line gets
  an exception or `netmodel` moves next to its first host is a policy
  decision for the user; the plan keeps `netmodel` in `src/common/netsim`
  and documents the exception in the common README.
- Narrowing the shadow record's "no emulator" to third-party emulators is
  unconfirmed. If the record stays as written, U6 adds the virtual device
  record as a peer and the library stays a replay and test tool.
- Whether an absent PVID should fall back to VLAN 1 per the MIB `DEFVAL`
  rather than to the single untagged VLAN. Keep the Decisions rule unless
  the lab captures show every mapper reports a PVID with an untagged set.
