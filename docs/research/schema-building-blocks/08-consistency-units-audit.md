---
title: Domain dossier — schema-language consistency (units and audit)
date: 2026-09-25
domain: b08 (cross-cutting consistency)
---

# Cross-cutting schema-language consistency — dossier

## 1. Scope and sources

Two halves per the brief: (A) external conventions for common value types;
(B) an audit of the current FlowSeer schema for carve-outs and drift.

**Repo paths read (full unless noted):**
- `docs/conventions/protobuf.md`
- `docs/code-style-proto.md`
- `docs/architecture/2026-08-20-network-model-structure-direction.md`
  (including all amendments through 2026-09-18)
- `test/conformance/proto/layering_test.go:1-150` (`importOrder` table,
  `orderedRoots`/`unorderedRoots`)
- Every `.proto` under `spec/proto/flowseer/net/` and `spec/proto/flowseer/model/`
  (`find` listing enumerated; contents read directly for the files cited by
  path:line below)
- `spec/proto/flowseer/net/phy/v1/{optical_power,module_temperature,supply_voltage,bias_current}.proto`
  (full)
- `spec/proto/flowseer/net/addr/v1/eui.proto`, `spec/proto/flowseer/net/interface/v1/interface.proto`,
  `spec/proto/flowseer/net/capture/v1/capture_filter.proto`,
  `spec/proto/flowseer/net/ip/v1/neighbor_entry.proto` (full)
- `spec/proto/flowseer/net/switching/v1/vlan_id.proto` (full — the predefined-rule
  worked instance)
- `spec/proto/flowseer/net/phy/v1/{ethernet_settings,ethernet_facet,ethernet_counters,pluggable_module,README}.proto/.md`
- `spec/proto/flowseer/net/interface/v1/interface_counters.proto`,
  `net/capture/v1/capture_counters.proto` (full)
- `spec/proto/flowseer/model/{capture/v1/capture_session,access/v1/operation,inventory/v1/attribute}.proto`
  (grepped for message/oneof/field-number structure)
- `spec/proto/flowseer/runtime/v1/bus.proto:130-150`
- `generated/go/proto/flowseer/net/{interface,capture,ip}/v1/*.pb.go` (grepped to
  confirm `EuiAddress` is the only generated symbol; no `MacAddress` exists
  anywhere in the tree)
- Sibling in-flight dossiers, read for cross-check per the coordinator's
  instruction: `01-wifi-technology.md`, `02-rf-and-ap-telemetry.md`,
  `04-platform-system.md` (full)

**Web/standards sources fetched this session** (part A):

- OpenConfig `openconfig-types.yang` / `openconfig-yang-types.yang` —
  https://github.com/openconfig/public/blob/master/release/models/types/openconfig-types.yang ,
  .../openconfig-yang-types.yang — `ieeefloat32` (single-precision IEEE 754 as
  a `binary` typedef, used for exact hardware-native floats), `percentage`
  (`uint8`, 0–100, `units "percent"`), `counter64`/`counter32` re-exports,
  `timeticks64`, `stat-interval` grouping (`avg-min-max-instant-stats`:
  `min`/`max`/`avg`/`instant` leaves of the underlying type,
  `interval`/`min-time`/`max-time` metadata).
- RFC 6991 (`ietf-yang-types`) — https://www.rfc-editor.org/rfc/rfc6991 —
  `counter32`, `counter64` ("wraps around", "no defined initial value",
  "discontinuities... indicated by publishing a corresponding discontinuity
  time"), `zero-based-counter32/64` ("a counter that has the defined initial
  value zero"), `gauge32`/`gauge64`, `date-and-time` (RFC 3339 profile).
- RFC 9181 supersedes RFC 6021 for `ietf-yang-types` timestamp guidance; RFC
  6991 remains the live `ietf-yang-types` module referenced by every YANG tree
  vendored in this repo (`spec/yang/`) — no RFC 9911 module exists; that
  brief-suggested number does not correspond to a published `ietf-yang-types`
  successor, so it is **unverified/does not exist** and is dropped from the
  citation set.
- RFC 2578 (SMIv2 SMI) — https://www.rfc-editor.org/rfc/rfc2578 — §7.1.6
  `Counter64` ("monotonically increasing... no defined initial value...
  wraps"), `Gauge32`/`TimeTicks` in §2/§7.
- RFC 2579 (SMIv2 TCs) — https://www.rfc-editor.org/rfc/rfc2579 — `TimeStamp`
  (sysUpTime snapshot), `TruthValue`, `RowStatus`.
- gNMI spec — https://github.com/openconfig/reference/blob/master/rpc/gnmi/gnmi-specification.md
  — `TypedValue.decimal_val` deprecated in favor of a plain `double` or a
  scaled integer with unit metadata; `Notification.timestamp` is "nanoseconds
  since the Unix epoch" `int64`, not `google.protobuf.Timestamp` (gNMI predates
  wide edition-2024 use and is proto3).
- Google AIP — https://google.aip.dev/140 (word choice, no unit-suffix rule
  stated there — corrected below), https://google.aip.dev/142 (time and
  duration: use `google.protobuf.Timestamp`/`Duration`, never a raw
  epoch-seconds `int64`), https://google.aip.dev/143 (standardized codes:
  language, currency, units of measure — recommends `google.type` types where
  one exists and a documented unit in the field name/comment otherwise),
  https://google.aip.dev/145 (range types: prefer two fields or a range
  message over a single packed value).

  Correction against the brief: AIP-140 governs *naming* generally (word
  choice, abbreviations), not unit suffixes specifically; the unit-in-name
  guidance actually lives at AIP-143 ("if a field represents a quantity with
  a unit... indicate the unit in the field name, e.g. `duration_seconds`, or
  in a comment when a self-describing type is used"). Citing 140 for this
  would have been a false clause reference.
- `google.protobuf.Timestamp`/`Duration` well-known types —
  https://protobuf.dev/reference/protobuf/google.protobuf/#timestamp , #duration.
- `google.type` — https://github.com/googleapis/googleapis/tree/master/google/type
  — `Decimal` (arbitrary-precision string, for currency-grade values only),
  `Money`, `LatLng`, `Interval` (two `Timestamp`s). Confirms the architecture
  doc's existing finding: no address, no power, no frequency type exists here;
  FlowSeer is on its own for every unit in this domain.

## 2. Part A — external unit/value conventions, and the one rule set

**Counters.** RFC 6991 and RFC 2578 agree on the shape FlowSeer already
half-has: a counter is monotonic, wraps, has no meaningful "zero" as an
absolute reading, and — this is the piece FlowSeer's three counter messages
all lack — a conformant source pairs it with a **discontinuity marker**
(RFC 6991's `counter32`/`counter64` description; OpenConfig's `counters`
groupings carry a sibling `last-clear` timestamp for the same reason SNMP
pairs `ifCounterDiscontinuityTime` with `IF-MIB::ifHCInOctets`). FlowSeer's
`InterfaceCounters`, `EthernetCounters`, and `CaptureCounters`
(`spec/proto/flowseer/net/{interface,phy,capture}/v1/*counters.proto`) all
say "consumers difference successive observations and treat a decrease as a
reset" and none carries a discontinuity or last-clear time. A decrease is a
correct *necessary* signal for a reset but not sufficient — a counter can
wrap exactly to its prior modulus, or a source can clear counters without a
device restart (a line-card reseat, an SNMP agent restart) without the value
decreasing at that instant if sampling is coarse. The gap is real and
consistent across all three messages, so it is a schema-level fix, not a
per-message one.

**Gauges vs. counters.** RFC 2578 draws gauges (`Gauge32`: "latched at a
maximum or minimum value", can decrease) as a distinct SMI type from
counters. FlowSeer's phy quartet (`OpticalPower`, `ModuleTemperature`,
`SupplyVoltage`, `BiasCurrent`) are gauges with alarm thresholds, not
counters — correctly modelled as plain scalars, no discontinuity concept
applies to them.

**Percentages.** OpenConfig's `percentage` typedef is `uint8`, range 0–100,
one whole-percent unit — no sub-percent precision. This is markedly coarser
than what providers in the sibling wifi/RF dossier actually report
(Ruckus/Meraki channel-utilization fields are floats; Cisco YANG states
`percentage` but the encoding is still whole numbers per that same typedef).
**Recommendation:** do not copy OpenConfig's `uint8` 0–100 verbatim. Use
`uint32` **basis points** (0–10000, hundredths of a percent) as FlowSeer's one
percent primitive, with a single predefined rule the way `vlan_id` is
predefined in `net/switching/v1`. This serves both a device that reports
whole percent (multiply by 100, exact) and one that reports fractional
percent (Ruckus/Meraki floats), without a float field anywhere and without
truncating precision a source actually has. See §7 for where the two sibling
dossiers already diverge on this and need reconciling.

**Fixed-point / linear-unit scaling (dBm vs. mW vs. native linear steps).**
The existing `net/phy/v1` quartet's own comments state the FlowSeer
precedent already reasoned about this: SFF-8472 (a DDM register spec)
reports power, temperature, voltage, and bias current as native fixed-point
integers (tenths of a µW, 1/256 °C, hundreds of µV, 2 µA steps), and FlowSeer
carries those linear units losslessly rather than converting to a "nicer"
unit (dBm) that would need a lossy log conversion on write and floating point
on read. That reasoning does **not** generalize to RF signal quantities
(tx power, EIRP, RSSI, SNR, noise floor): there is no equivalent
"native linear register" for these — IEEE802dot11-MIB's decade-old
`dot11TxPowerLevel*` in mW is the *outlier*, and every modern source (Ruckus,
Meraki, OpenConfig `openconfig-wifi-phy`, Cisco IOS-XE YANG) reports a plain
signed integer already in dBm. **The one rule that covers both cases:**
carry the unit the overwhelming majority of sources already report natively
and losslessly, name the field for that unit, and never invent a derived
"canonical" unit that forces every mapper to convert. This is a restatement
of AIP-143's field-naming guidance, not a departure from the existing phy
precedent — dBm-as-`sint32` for RF and linear-microunits-as-`uint32`/`int32`
for optics are both instances of the same rule, not two different rules.

**Bit rates.** `speed_bps`/`active_speed_bps`/`effective_speed_bps` are all
`uint64` bits-per-second in the existing schema, consistent with itself. The
one outlier is `PluggableModule.nominal_bit_rate_mbps`
(`spec/proto/flowseer/net/phy/v1/pluggable_module.proto:64`, `uint32`),
which is SFF-8472's own native register unit (its Byte 12 is literally
"nominal signalling rate, units of 100 MBd" scaled to Mbps in this schema's
comment) — same "carry the native register" rule as above, not an
unreasoned inconsistency, but it does mean a consumer comparing a module's
nominal rate against `EthernetFacet.active_speed_bps` must convert units
first, which the field's doc comment does not currently say.

**Durations.** `google.protobuf.Duration` is the correct type per AIP-142 and
is what nearly every duration-shaped field in `net/` and `model/` already
uses (`net/switching/v1/aggregation_facet.proto` `up_delay`/`down_delay`,
`net/addr/v1/ip.proto` `IpLifetime.preferred`/`valid`,
`model/capture/v1/capture_session.proto` `max_duration`). The one outlier
found is `runtime/v1/bus.proto:148` (`uint64 duplicate_window_seconds`) —
outside `net/`/`model/` scope for this audit but flagged in §5.

**Timestamps.** `google.protobuf.Timestamp` is used consistently everywhere
a point-in-time value appears in `net/`/`model/` — no raw epoch `int64`
anywhere in that tree (confirmed by grep across both roots). This is already
correct against AIP-142 and RFC 6991's `date-and-time`; nothing to fix.

**Statistics windows.** OpenConfig's `avg-min-max-instant-stats` grouping
(min/max/avg/instant + interval + min-time/max-time) is the standard shape
for a windowed statistic. FlowSeer has no equivalent primitive yet; every
counter and gauge in the current schema is a single point reading. This
becomes relevant the moment a domain proposes an averaged quantity (channel
utilization, CPU load) — flagged as an open question for the planner in §8,
not resolved here since no such primitive exists to audit.

**Recommended FlowSeer rule set (part A), in one place:**

1. Name every scalar for its unit (`_bps`, `_dbm`, `_mhz`, `_nanowatts`,
   `_millidegrees`, `_basis_points`) per AIP-143; never a bare `value` field
   with the unit only in a comment, except where the containing message's
   name already states the physical quantity unambiguously (the existing
   `OpticalPower.value_nanowatts` pattern is fine because the message name
   already says "optical power" — the unit suffix on the field is what is
   load-bearing, not the message name).
2. Prefer the unit a source reports natively and losslessly (native register
   steps for DDM/PHY hardware values; dBm directly for RF signal quantities;
   whole seconds only through `Duration`, never a bare integer).
3. `uint32` basis points (0–10000) is the one percent primitive, with one
   predefined rule, not a per-package reinvention.
4. Every counter gets a discontinuity marker. See §5 for the concrete fix.
5. A statistics-window primitive (min/max/avg/instant + interval) is future
   work, not retrofitted onto today's single-reading counters and gauges.
6. `flowseer.net.env.v1` (a new, function-named leaf package, sibling to
   `net/addr`/`net/packet`/`net/phy`) is the right home for a
   quantity-typed-variant sensor primitive — see §3 and §7's agreement with
   the platform-system dossier's `SensorReading` proposal.

## 3. Part B — audit findings, cited path:line

### B1. Duplicated shape: the phy four-threshold quartet

`spec/proto/flowseer/net/phy/v1/optical_power.proto:13-36`,
`module_temperature.proto:13-36`, `supply_voltage.proto:12-35`,
`bias_current.proto:12-35` are four independently-hand-written messages, each
exactly: one scaled integer value field, four ordered threshold fields
(`high_alarm`/`high_warning`/`low_warning`/`low_alarm`), and one CEL rule
whose text differs only by field-name substitution
(`optical_power.thresholds_ordered` vs. `module_temperature.thresholds_ordered`
vs. `supply_voltage.thresholds_ordered` vs. `bias_current.thresholds_ordered`).
This is the textbook case the typed-variant convention
(`docs/conventions/protobuf.md` "Typed variants") exists for: a closed set of
arms that share a shape but each fix their own unit. **Confirmed independently
by the platform-system dossier's `SensorReading` proposal — see §7, where I
agree with it.**

### B2. Naming drift: `EuiAddress` in code vs. `MacAddress` everywhere it is discussed

`spec/proto/flowseer/net/addr/v1/eui.proto:35` defines the landed message as
`EuiAddress`. But:

- `docs/conventions/protobuf.md:263,278,298,350,364` names the worked-example
  type `MacAddress` throughout, including inside a compiling code sample
  (`flowseer.net.addr.v1.MacAddress base_mac = 2;`, line 350) that would not
  compile against the actual tree.
- `docs/architecture/2026-08-20-network-model-structure-direction.md:168,176,181,298,404,416`
  does the same, including in `Interface.mac` (line 298) and the typed-variant
  worked example (line 404).
- Every current consumer in the schema uses the real name:
  `net/capture/v1/capture_filter.proto:44,46`, `net/interface/v1/interface.proto:48`,
  `net/ip/v1/neighbor_entry.proto:37`, and the generated Go
  (`generated/go/proto/flowseer/net/interface/v1/interface.pb.go:37` etc.) all
  say `EuiAddress`. Grepping the full generated tree for `MacAddress` returns
  zero hits — the name in the docs has never been the name in the schema.
- **This is not a latent risk, it is already spreading**: the sibling
  `01-wifi-technology.md` and `04-platform-system.md` dossiers, written this
  session from the conventions doc rather than the schema, both write
  `flowseer.net.addr.v1.MacAddress` as if it already exists (`01`:
  "`bssid` — reuse `flowseer.net.addr.v1.MacAddress`"). A planner assembling
  those dossiers' proposed messages verbatim would generate a compile error.

Fix is a rename, not a doc correction: the docs are unanimous and came first
(2026-08-20/21), the code name is the outlier, and two independent domain
researchers already picked the docs' name over the code's. Rename
`EuiAddress` → `MacAddress` (oneof stays `kind`, arms stay `eui48`/`eui64`
per the conventions doc's own reasoning, "EUI-48 and EUI-64 are widths, not
families" — that reasoning argues for the oneof-arm names, not the message
name, so it does not block the rename). This is a wire-breaking rename inside
the pre-stability window (`CLAUDE.md`, "Breaking changes welcome") touching
four `.proto` files and their generated Go.

### B3. `interface_name`-shaped strings repeated with no predefined rule

`min_len = 1` plus `max_len = 64` on a device-local name string appears at
`net/interface/v1/physical_interface.proto:20`, `subinterface.proto:15`,
`interface.proto:30`, `net/protocol/lldp/v1/port_settings.proto:16-17`,
`neighbor.proto:19-20`, `net/protocol/lacp/v1/aggregator_state.proto:17-18`,
`port_state.proto:17-18,24-25`, `net/protocol/stp/v1/bridge_state.proto:31-32`,
`port_state.proto:19-20`, `net/switching/v1/aggregation_facet.proto:34-35`,
`fdb_entry.proto:35-36`, `net/ip/v1/interface_address.proto:82-83`,
`neighbor_entry.proto:25-26` — 13 occurrences of the identical two-rule pair
across 5 packages, with no shared predefined rule the way `vlan_id`
(`net/switching/v1/vlan_id.proto:12-17`) has one. `interface.proto:28-31`
itself (the canonical `Interface.name` field the direction doc's worked
example shows) sets `min_len = 1` with **no** `max_len` at all — so even the
field this pattern is copied from is not itself consistent with its copies.
This is exactly the case code-style-proto.md's "Small domain scalars use
predefined rules, not wrapper messages" section describes, just not yet
applied outside `vlan_id`.

### B4. Facet/settings asymmetry: `EthernetSettings` is declared and never carried

`net/phy/v1/ethernet_settings.proto:11` defines `EthernetSettings`
("Requested Ethernet physical-link settings... Applied and negotiated facts
are represented separately by EthernetFacet"). Grepping the entire
`spec/proto/flowseer/` tree for `EthernetSettings` outside its own file
returns **zero** matches — `EthernetFacet` (`net/phy/v1/ethernet_facet.proto`)
has no `settings` field of that type, unlike the worked instance the
conventions doc names, `CopperFacet.poe_settings`
(`net/phy/v1/copper_facet.proto:15`, embedding `PoeSettings`). The package
README (`net/phy/v1/README.md`) describes only the observed side ("Requested
speed, duplex... are separate from negotiated or measured values") without
saying where the requested side is actually carried. This is a landed
message with no reachable field — either an oversight to fix now (add
`EthernetSettings requested = <n>` to `EthernetFacet`) or a declared-but-not-
yet-wired intent that should say so in the file-level comment the way a
deliberately-partial triad member must (`docs/conventions/protobuf.md`,
triad section) — the settings/facet split is drawn as the same
intended-versus-observed line as Config/State, so the same "say so when
partial" discipline should apply.

### B5. Counter semantics with no discontinuity marker (see also §2)

`InterfaceCounters` (`net/interface/v1/interface_counters.proto:5-13`),
`EthernetCounters` (`net/phy/v1/ethernet_counters.proto:5-13`), and
`CaptureCounters` (`net/capture/v1/capture_counters.proto:5-9`) each restate
"consumers difference successive observations and treat a decrease as a
reset" verbatim (three independent copies of the same policy prose, which
`doc-style.md`'s "write dense... a copy in every family is noise that
drifts" already argues against) and none carries the RFC 6991-style
discontinuity time. This is the one place where the three counter messages
are *not* differently modelled — they are identically modelled, and
identically missing the same field.

### B6. String length limits chosen with no visible rule

Across `model/` the same "identifier-ish string" shape gets `max_len` of 64,
128, 256, or 512 with no discernible pattern tied to what the string is:
`model/inventory/v1/component.proto:24-25` (`max_len = 256`) vs.
`component.proto:97-98` (`max_len = 512`) vs. `component.proto:103-131`
(five more fields at `max_len = 64` or `128`, same file); `model/edge/v1/edge.proto:105-106,156-157`
(`128`) vs. `model/credential/v1/material.proto:54,69,97,103,115` (`256`,
several times) vs. `model/access/v1/operation.proto:98` (`256`) vs. `:147`
(`128`). No comment in any of these files states why 64 stops and 128 starts,
or why 256 rather than 512. This is a smaller version of B3 (same missing
"predefined rule for a bounded string" gap) but for free-text/identifier
fields rather than device-local names specifically, so it likely wants a
*family* of predefined rules (short identifier / medium label / long
free-text) rather than one, which is a planner decision, not this audit's to
make.

### B7. Layering table: no violation found

`test/conformance/proto/layering_test.go:26-119`'s `importOrder` table was
compared line-by-line against the tree in
`docs/architecture/2026-08-20-network-model-structure-direction.md`'s import
graph (both current as of the 2026-09-18 amendments). They agree in every row
checked. **This is a clean finding, not a gap**: unlike the brief's
hypothesis, the layering allowlist has not drifted from the accepted
direction record at this point in time.

### B8. Naming near-misses that are not actually bugs, but are traps

- `Neighbor` (`net/protocol/lldp/v1/neighbor.proto:13`, an LLDP announcement
  row keyed by local interface) vs. `NeighborEntry`
  (`net/ip/v1/neighbor_entry.proto:13`, an ARP/ND cache row keyed by
  interface+IP) are **not** an inconsistency — the direction doc's own rule
  (`2026-08-20-network-model-structure-direction.md`, "Protocols own their
  packages") distinguishes a protocol's own table (unsuffixed, LLDP owns
  everything about itself) from a functional-domain table that several
  protocols could populate (`NeighborEntry`, `FdbEntry` — suffixed `Entry`).
  Flagged only because a future domain dossier proposing, say, a BGP or ND
  table needs to pick the right one of these two conventions rather than
  inventing a third.
- `LinkType` (`net/capture/v1/link_type.proto`, pcap framing) and `Link`
  (`model/inventory/v1/link.proto`, an inventory adjacency entity) share the
  word "Link" for unrelated concepts in different packages. No collision at
  the wire level (different packages, different kinds — one primitive, one
  entity), but a grep for "link" across the tree returns both; worth the
  planner naming a new domain's link-layer concept something other than
  bare "Link" if it is not the adjacency entity.
- `EuiAddress`/`MacAddress` (B2) is the one of these three that is an actual
  bug, not just a trap — already covered above.

## 4. Provider data matrix

Not applicable in the usual sense for this cross-cutting domain — there is no
single external "provider" surface to tabulate field-by-field. The relevant
matrix is the **standards-body comparison in §2** (OpenConfig / RFC 6991 /
RFC 2578 / RFC 2579 / gNMI / AIP / `google.type`), which is given inline
above rather than as a separate table, because the interesting facts are
textual (what wording each spec uses for "this may decrease", not a
row/column of field names).

## 5. Proposed primitives (this domain's own contribution)

- **`flowseer.net.env.v1`** (new leaf package, imports nothing FlowSeer-owned,
  sibling to `net/addr`/`net/packet`/`net/phy` in the package tree): a
  `SensorReading` typed variant replacing the phy quartet from B1. See §7 for
  the concrete shape, which the platform-system dossier already wrote and I
  am endorsing rather than re-deriving independently — see that section for
  the one refinement I'd make (arm-naming precision, not structure).
- **A `Percentage` predefined rule** in whichever package first needs one
  (likely `net/env` or a to-be-named stats leaf, not `net/switching` — this
  is not VLAN-specific): `uint32`, basis points, bounded `0..10000`, one
  extension number, following the exact `vlan_id` pattern
  (`net/switching/v1/vlan_id.proto:12-17`).
- **An `interface_name` predefined rule**, `string`, `min_len = 1`,
  `max_len = 64`, in `net/interface/v1` (the package that already defines the
  canonical `Interface.name` field and would own the rule the same way
  `net/switching` owns `vlan_id`), for the 13 duplicated occurrences in B3 —
  and a matching fix to `interface.proto:28-31` itself so the source of the
  copied pattern also carries a `max_len`.
- **A `CounterDiscontinuity` shape** (not a full statistics-window primitive —
  that is future work per §2) to close B5: either a shared
  `google.protobuf.Timestamp last_discontinuity` field added identically to
  all three counter messages, or (preferred, since it is one concept used in
  three unrelated packages with no import relationship between them) a small
  message `CounterEpoch { google.protobuf.Timestamp since }` defined once —
  the planner should decide the package. Whichever shape, the "decrease means
  reset" prose should be written once (per `doc-style.md`'s anti-duplication
  rule) rather than copied a fourth time into a new counters message.

## 6. Entity candidates (model/)

None. This is a cross-cutting primitives-and-consistency domain; it proposes
no new UUID-identified entity. `flowseer.net.env.v1` is a Primitive package
by construction (no ref, embedded by value the way `net/phy` messages are).

## 7. Cross-check against the sibling dossiers (01, 02, 04)

**Agree, strongly — `04-platform-system.md`'s `SensorReading` typed variant**
(its §4.1) is the correct fix for B1, independently arrived at from the same
evidence (the phy quartet's repeated shape) and the same convention
(typed variants for a closed set of differently-validated arms). Two
refinements, not disagreements:

1. Its arm named `Power` (nanowatts, `uint32`) is explicitly scoped in that
   dossier's own text to *replace `OpticalPower`* — good, because a `uint32`
   of nanowatts overflows at ~4.29 W and cannot hold a device-level power
   draw in watts. That dossier already keeps PSU/device wattage
   (`allocated_power_watts`/`used_power_watts`) as separate `ComponentState`
   fields for exactly this reason (its §4.2), so there is no actual unit-range
   conflict — but the arm should be named for what it measures precisely
   (e.g. keep it paired with a doc comment scoping it to optical/laser power
   specifically) so a future reader does not reach for the `Power` arm to
   represent a PSU reading and silently overflow it.
2. `RelativeHumidity` (millipercent RH) is a new unit family (relative
   humidity) with no existing FlowSeer precedent to check consistency
   against — flag it to the planner as the first instance of this unit, so
   whatever range/predefined rule it gets (0–100000 millipercent, presumably)
   is the one every future humidity sensor field reuses, rather than letting
   it drift the way string lengths did (B6).

**Agree — `02-rf-and-ap-telemetry.md`'s `sint32` dBm fields**
(`tx_power_dbm`, `eirp_dbm`, `antenna_gain_dbi`, `noise_floor_dbm`,
`rssi_dbm`, `snr_db`) are consistent with the "carry the native unit" rule
in §2, and its explicit reasoning (IEEE802dot11-MIB's mW is a 2002-era
outlier; every modern source states dBm) is the same reasoning the existing
phy quartet's own comments use for *linear* units — same rule, different
conclusion because the domain's native representation differs. No
disagreement.

**Disagree/reconcile — percent representation is inconsistent *between* the
two sibling dossiers, and this is exactly the kind of drift this domain
exists to catch.** `02-rf-and-ap-telemetry.md`'s `ChannelUtilization`
message (§ "ChannelUtilization message") proposes `uint32` **0–100** whole
percent for `total_percent`/`self_tx_percent`/`self_rx_percent`/
`other_bss_percent`/`non_wifi_percent`, while `04-platform-system.md`'s CPU
utilization field (§4.2, `utilization_basis_points`) proposes `uint32`
**0–10000** basis points for the identically-shaped "percent utilization"
concept, explicitly to avoid losing sub-percent precision some sources
report. Per §2's recommendation, basis points is the one FlowSeer percent
primitive; `ChannelUtilization`'s fields should be renamed
`*_basis_points` and rescaled 0–10000 to match, both because RF providers in
that same dossier's own provider matrix report fractional percent
(Ruckus/Meraki floats, cited in `02`'s §5/6) and because two independently
proposed "percent" shapes landing in the same schema generation round is
precisely the B6-style drift this dossier's whole purpose is to prevent
before it lands rather than after.

**Confirms — the `MacAddress` naming drift (B2) is not hypothetical.** Both
`01-wifi-technology.md` and `04-platform-system.md` write
`flowseer.net.addr.v1.MacAddress` as an existing type to reuse
(`01`: "`bssid` — reuse `flowseer.net.addr.v1.MacAddress`"). Neither dossier
author appears to have checked the actual `.proto` file, which is reasonable
— they were told to read the conventions doc, and the conventions doc is
wrong. This is the strongest evidence in this whole audit that B2 needs
fixing before, not after, any of these three domains' protos are written:
every one of them will otherwise reference a nonexistent type.

## 8. Open questions for the planner

1. **Rename `EuiAddress` → `MacAddress` now, or fix the docs instead?**
   I recommend the rename (B2/§7): two independent domain proposals already
   assume the docs' name, and the docs came first. But it is a call the
   planner should make explicit, not one this dossier should execute.
2. **Where does the basis-points `Percentage` predefined rule live?** Not
   `net/switching` (not VLAN-specific). Candidates: `net/env/v1` (if it
   becomes the general "measured quantity" package), or a new
   narrowly-scoped leaf. Needs a decision before `02`'s and `04`'s percent
   fields can both be corrected to use it.
3. **Discontinuity marker shape** (§5): one shared message reused by three
   otherwise-unrelated counters, or three copies of the same field? The
   packages involved (`net/interface`, `net/phy`, `net/capture`) have no
   import relationship today, so a shared message needs a new leaf package
   or one of the three importing from another for the first time.
4. **`EthernetSettings` (B4):** wire it into `EthernetFacet` now (small,
   mechanical, no design question) or leave it declared-but-unused with a
   file comment explaining why? The triad's "deliberately partial" pattern
   has no exact equivalent for Facet/Settings today; this is a small
   precedent-setting decision either way.
5. **String-length predefined-rule family (B6):** one generic
   `identifier`/`label`/`free_text` set of predefined rules, or leave
   per-field judgment as today? Lower priority than 1–4; flagged for
   completeness since the brief asked for it, but the current inconsistency
   has not caused a bug, only reader friction.
