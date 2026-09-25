---
title: Schema Building Blocks Phase 1, Shared Leaves and the Schema Language - Plan
type: feat
date: 2026-09-25
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept
execution: mixed
parent: docs/plans/2026-09-25-1713-feat-schema-building-blocks-plan.md
amends: docs/architecture/2026-08-20-network-model-structure-direction.md
---

# Schema Building Blocks Phase 1, Shared Leaves and the Schema Language - Plan

> Implemented. 5 units, 2026-09-25T17:48Z to 2026-09-25T20:26Z. The
> Verification and Definition-of-done lines naming `--full` are replaced by
> one targeted verifier run over the union of paths changed in this phase
> (`spec/proto/flowseer`, `test/conformance/proto`, `src/common/netsim`,
> `src/modules/localnet`, `src/modules/capture`, `src/services/device`, and
> `src/common/service/manifest.go`), because `--full` builds and race-tests
> `generated/` and exhausts host memory; that targeted run ended `FlowSeer
> verification passed.` Two rulings were made at implementation time and
> are in Decisions: same-name fields moving to `net/measure` reserve only
> their old number, and the registry-row count, repeated interface-name
> carriers, the two carriers outside the key-rules parenthetical, the
> `duplicate_window` Duration, and the missing `ipAddressOrigin` mapper
> carrier are all recorded there.

## Goal

The two leaves every later phase imports exist (`net/key` for key rules,
`net/measure` for sensor readings, percentages, and path quality), and the
schema that has already landed speaks the record's language: canonical
units, `MacAddress`, one interface-name rule per use, counters with a
discontinuity marker, and an address origin split from the IPv6
interface-identifier method. Four conformance tests hold the language for
every later package. The means is five units: docs, the leaves, then three
reshapes of existing packages with their Go consumers.

Stop if the record is amended before this phase lands in a way that
changes a unit, a key rule, or a package name; re-plan from the amended
record instead.

## Decisions

The parent's decisions and the
[schema building blocks record](../architecture/2026-09-25-schema-building-blocks-direction.md)
govern. The ones below are local to this phase.

- `net/key/v1` is one file, `key.proto`, extending
  `buf.validate.StringRules` with `interface_name = 50000`,
  `shell_safe_interface_name = 50001`, and `network_instance_name = 50002`.
  Why: the three are one family of device-local key rules and are evolved
  together, the case the style doc allows one file for.
- `net/measure/v1` holds `basis_points.proto` (the `UInt32Rules` extension
  `basis_points = 50003`, `!rule || this <= 10000u`), `sensor.proto`
  (`Temperature`, `Voltage`, `Current`, `Power`, `RotationSpeed`,
  `RelativeHumidity`, and the typed variant `SensorReading`), and
  `path_quality.proto`. Why: the six quantity messages share one shape
  (value plus four ordered thresholds plus one CEL rule) and are one
  contract, the way `net/addr/v1/ip.proto` groups the IP variants.
  `UInt32Rules` numbers 50000 to 50002 are taken by `net/switching`
  (`vlan_id.proto:13`), so the new one is 50003.
- The predefined-rule extension numbers get a registry table in
  `docs/code-style-proto.md`, one row per (rules message, number, rule,
  file). Why: two packages already use 50000 on different rules messages,
  and nothing today stops a third package from taking a number that is
  already used on the same message.
- Each quantity message has `value_<unit>` and four thresholds
  `high_alarm_<unit>`, `high_warning_<unit>`, `low_warning_<unit>`,
  `low_alarm_<unit>`, with the CEL rule the four phy messages use today,
  renamed per message (`temperature.thresholds_ordered`, and so on). Units
  come from the record's table: `millidegrees_celsius` (sint32),
  `microvolts` (sint32), `microamperes` (sint32), `nanowatts` (uint64),
  `rpm` (uint32), and `basis_points` (uint32, with the rule) for
  relative humidity.
- `PathQuality` is `{google.protobuf.Duration latency, google.protobuf.Duration
  jitter, uint32 loss_basis_points}`, with `latency` and `jitter` validated
  `gte: {}` (non-negative) and loss by the basis-points rule. Latency is
  round-trip, as Meraki's `latencyMs` is (dossier 07); the comment says so.
- `EthernetFacet` carries `EthernetSettings settings = 33`. Why: the record
  makes a declared, uncarried `Settings` a finding; 33 is the next number
  in the facet's 30s block.
- `AddressOrigin` keeps its name and becomes the assignment mechanism:
  `UNSPECIFIED = 0`, `OTHER = 1`, `STATIC = 2`, `DHCP = 3`, `SLAAC = 6`
  (RFC 4862 stateless autoconfiguration), `LINK_LOCAL = 7` (RFC 4862 §5.3
  and RFC 3927 link-local configuration), with `reserved 4, 5` and
  `reserved "ADDRESS_ORIGIN_LINK_LAYER", "ADDRESS_ORIGIN_RANDOM"`. Why:
  the old 4 and 5 meant identifier generation, and handing their numbers
  to new meanings is the reuse `docs/code-style-proto.md` (Evolution)
  forbids even before the first stable release. A new
  `InterfaceIdentifierMethod` enum carries how an IPv6 interface identifier
  was generated: `UNSPECIFIED = 0`, `OTHER = 1`, `MODIFIED_EUI64 = 2`
  (RFC 4291 Appendix A), `STABLE_OPAQUE = 3` (RFC 7217), `TEMPORARY = 4`
  (RFC 8981), `RANDOMIZED = 5` (a non-EUI-64 identifier the source did not
  qualify further, which is what RFC 8344's `random` origin reports).
  `InterfaceAddress` gains `InterfaceIdentifierMethod iid_method = 8`,
  valid only on an IPv6 address (CEL). Why: RFC 8344 `link-layer` and
  `random` are both SLAAC with different identifier generation, and RFC
  8981 temporariness is a third axis (dossier 06, "three-axis
  disambiguation"). A mapper from RFC 8344 writes `link-layer` as `SLAAC`
  plus `MODIFIED_EUI64` and `random` as `SLAAC` plus `RANDOMIZED`.
- A field whose type or unit changes takes the next free number, and its
  old number and name are `reserved` in the same change; a pure rename
  that keeps type and unit keeps its number. Why: a semantic change behind
  an unchanged number is invisible on the wire (`docs/code-style-proto.md`,
  Evolution). So the PoE and PSE power fields, `usage_threshold_percent`,
  `nominal_bit_rate_mbps`, the diagnostics and lane fields whose message
  type moves to `net/measure`, and the LLDP TTL get new numbers, while the
  counter renames below keep theirs.
- Counters rename to the record's rule: `InterfaceCounters.in_octets` and
  `out_octets` become `in_bytes` and `out_bytes`; `CaptureCounters`'
  bare-verb fields become `received_packets`, `accepted_packets`,
  `dropped_by_interface_packets`, `dropped_by_budget_packets`, and
  `dropped_by_transport_packets`. All three counters messages gain
  `google.protobuf.Timestamp last_discontinuity`, numbered after their last
  field.
- `lldp.v1.Neighbor.time_to_live_seconds` becomes
  `google.protobuf.Duration time_to_live`, validated `gte: {}` and
  `lte: {seconds: 65535}` (the TTL TLV is 16 bits of seconds, IEEE 802.1AB
  §8.5.4, as the field comment cites today).
- A field whose name spells an interface name (the existing
  `namesAnInterface` predicate in `test/conformance/proto/field_constraint_class_test.go`)
  carries exactly one of the two `net/key` interface-name rules and no
  `string.pattern` or `string.max_len` of its own. Observed rows take
  `interface_name`; `model/access/v1/interface.proto`'s three sites and
  `model/capture/v1/capture_session.proto:43` take
  `shell_safe_interface_name`. `AggregationFacet`'s member names are
  observed and move from the pattern to `interface_name`. Two interface
  names do not match the predicate by name, `PhysicalInterface.lag_parent`
  and `Subinterface.parent`; they are renamed `lag_parent_interface_name`
  and `parent_interface_name` so that the name says what the value is and
  the predicate finds them. Why: the record, rule 3, and a predicate that
  matches by name is complete only when the names follow the rule.
- Ruled: the diagnostics and lane fields whose message type moves to
  `net/measure` unchanged in name and meaning (`ModuleDiagnostics.temperature`
  and `.voltage`; `ModuleLane.tx_power`, `.rx_power`, and `.bias`) keep their
  names, take the next free number, and reserve the old number only: the old
  name is not reserved, because a name cannot be both `reserved` and reused,
  and the name keeps its meaning. The scalar unit/type changes reserve the
  old number and name together, since those fields are also renamed to their
  suffixed form. Why: name reservation exists to stop a revival with a new
  meaning; these names keep theirs. Cost if wrong: a later change re-adds
  one of these names with a different meaning and must reserve it then; the
  reserved numbers still stop a wire-level misread.
- Ruled: a repeated interface-name field carries its class the way
  structure-record convention 4 prescribes for a predefined rule on a
  repeated field: the `items` aggregate restates the class rule's bounds
  (`min_len 1`, `max_len 255`, plus the character class for
  `shell_safe_interface_name`), and the 64-character bound goes away.
  `store/device/v1/registry.proto`'s `managed_interfaces` is the live case
  and takes the shell-safe class: its names are operator-supplied and drive
  drift checks against the device. Why: aggregates cannot name an extension
  inside `items`, so restating is the only form a repeated field can take.
  Cost if wrong: a later repeated carrier restates other bounds and the
  key-rule walk flags it.
- Ruled: the key-rule walk covers every FlowSeer package, which pulls in two
  carrier files the key-rules unit's parenthetical omits:
  `api/device/v1/device_service.proto` (`ReadInterfaceRequest.interface_name`
  is sent to the device to read it, so it takes
  `shell_safe_interface_name`) and `store/device/v1/registry.proto` (the
  repeated case above). The layering table gains `net/key` on the
  `model/access`, `model/capture`, and `api/device` rows: the leaf-table
  unit listed no consumer of `net/key` outside `net/` and
  `model/inventory`, and this unit is where those imports land. Cost if
  wrong: `TestProtoReadmeImports` or the import-order gate fails and names
  the row.
- Ruled: requirement 8's unit-suffix walk covers `flowseer.runtime.v1`,
  where `duplicate_window_seconds = 16` violates rule 1 (a time span is a
  `google.protobuf.Duration`, not a `_seconds` integer). The counters unit
  converts it to `google.protobuf.Duration duplicate_window`, reserving 16
  and the old name, with `required` and `duration.gte = {seconds: 1}`
  in place of the uint64 bounds, and the unit's Files line gains
  `spec/proto/flowseer/runtime/v1/bus.proto` and
  `src/common/service/manifest.go` with its tests. Why: the walk cannot pass
  over every FlowSeer package otherwise; no Go code reads the field (only
  `manifest.go`'s desired-manifest construction names it). Cost if wrong:
  persisted operator manifests carrying the old field fail to parse and are
  rewritten.
- Ruled: the `ipAddressOrigin` mapping this phase's counters unit names has
  no carrier: `snmpmap` has no IP-MIB mapping today (nothing builds
  `InterfaceAddress` from a live source), and the parent plan scopes new
  live-source mappers out. The unit therefore ships the schema split
  (`AddressOrigin`, `InterfaceIdentifierMethod`, `iid_method`) and the
  conformance cases (requirement 7) and leaves the mapper mapping sentences
  and the `ifmib_test.go` `ipAddressOrigin` table cases out. Why: the tree
  wins over the plan about what exists. Cost if wrong: a later phase that
  lands the IP-MIB walk writes the mapping from the enum comments, which
  spell it out.
- The unit-suffix test works from two lists in the test file: canonical
  suffixes (`_bps`, `_bytes`, `_mhz`, `_nanowatts`, `_millidbm`, `_millidb`,
  `_millidbi`, `_millidegrees_celsius`, `_microvolts`, `_microamperes`,
  `_rpm`, `_basis_points`, `_nanometers`, `_packets`, `_frames`) and
  forbidden ones (`_mbps`, `_kbps`, `_milliwatts`, `_watts`, `_percent`,
  `_seconds`, `_ms`, `_millis`, `_octets`, `_millidegrees`, `_dbm`, `_db`,
  `_dbi`, `_mw`). A numeric field ending in a forbidden suffix fails. Why:
  a forbidden list catches the drift the audit found; a closed list of
  every allowed name would fail on every count field (`overload_count`)
  that has no unit.

## Requirements

1. `flowseer.net.key.v1` validates: `interface_name` rejects `""` and a
   256-character name and accepts `"Gi1/0/1 (uplink)"`;
   `shell_safe_interface_name` rejects `"eth0;reboot"` and accepts
   `"GigabitEthernet1/0/1"`.
2. `flowseer.net.measure.v1.Temperature` with `high_warning = 70000` and
   `high_alarm = 60000` millidegrees fails with rule id
   `temperature.thresholds_ordered`; `SensorReading{}` with no arm fails
   `quantity: exactly one field is required`; a `uint32` field with
   `basis_points = true` rejects 10001.
3. `ModuleLane.rx_power` is a `measure.v1.Power`; a D-Link
   `dDdmIfInfoCurrentRxPower` of 4660 (0.1 µW units, the scale
   `phy_ddm.go` applies today) maps to `value_nanowatts = 466000`.
4. `PoeFacet.power_draw_nanowatts` holds 30 W as `30000000000`;
   `PseBudget.usage_threshold_basis_points` rejects 0 and 10000 and accepts
   8000 (the MIB's 1..99 percent, scaled).
5. `PluggableModule.nominal_bit_rate_bps` holds a D-Link
   `dPortSfpInfoBitRate` of 10300, which `phy_ddm.go` reads as Mbps today,
   as `10300000000`.
6. `EuiAddress` no longer exists; `flowseer.net.addr.v1.MacAddress` does,
   with arms `eui48 = 1` and `eui64 = 2`.
7. `InterfaceAddress{address: 2001:db8::1, iid_method: MODIFIED_EUI64}`
   passes; the same with an IPv4 address fails
   `interface_address.iid_method_is_ipv6_only`.
8. `TestNoFloatingPointFields`, `TestCanonicalUnitSuffixes`,
   `TestCountersCarryDiscontinuity`, and `TestKeyFieldsUseKeyRules` pass
   over every FlowSeer package, and each fails on a synthetic descriptor
   that breaks only its own rule.
9. `go build ./...` and `go test -race ./src/... ./test/...` pass.

## Out of scope

- Every package the record lists as new except `net/key` and
  `net/measure`; phases 2 to 8 add them.
- Network-instance keys on `Vlan`, `FdbEntry`, and `IpFacet` (phase 2).
- Sensor rows on `ComponentState` (phase 5).
- String bounds outside interface names (parent open question).

## Units

### U1. Conventions, record amendment, and package lists

Files: docs/conventions/protobuf.md, docs/code-style-proto.md,
docs/architecture/2026-08-20-network-model-structure-direction.md,
spec/proto/flowseer/net/README.md, spec/proto/flowseer/net/protocol/README.md,
CONCEPTS.md
After: none
Change: `protobuf.md` gains a "Units and keys" section that states the
record's unit table as the field-author checklist, the counters rule, the
two interface-name rules, and the facet/row naming rule, each linking the
record for the reason. `code-style-proto.md` gains the extension-number
registry table with today's seven rows (correcting a miscount of five:
three UInt32Rules, four EnumRules) plus the four this phase adds.
The structure record gains a dated amendment (2026-09-25) that points to
the new record and lists what it changes: the Host/Client open question
answered by Endpoint, no `WirelessClient`, the wider `net/wlan` imports,
radios as components. The `net/` README lists every package in the
record's tree; a package that does not exist yet reads
`(planned; schema building blocks record)`. The protocol README does the
same for its packages. Its `Imported by:` stays `nothing`: the four
packages this plan originally said import `protocol/lldp` today are only
permitted to by the layering allowlist; no `.proto` imports a protocol
package. `CONCEPTS.md` gains "Canonical unit" under
Network model.
Tests: none (docs); `TestProtoReadmeCoverage` still passes because no
package directory is added.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/conventions/protobuf.md docs/code-style-proto.md docs/architecture/2026-08-20-network-model-structure-direction.md spec/proto/flowseer/net/README.md spec/proto/flowseer/net/protocol/README.md CONCEPTS.md`

### U2. The `net/key` and `net/measure` leaves

Files: spec/proto/flowseer/net/key/v1/{key.proto,README.md},
spec/proto/flowseer/net/measure/v1/{basis_points.proto,sensor.proto,path_quality.proto,README.md},
test/conformance/proto/layering_test.go,
test/conformance/proto/{key_rules_test.go,measure_rules_test.go,schema_language_test.go},
generated/go/proto/flowseer/net/{key,measure}/ (by `buf generate`)
After: none
Change: the two packages exist as the Decisions describe. `importOrder` in
`layering_test.go` gains a row for every package in the record's tree with
these imports: `net/key` and `net/measure` none; `net/instance` key;
`net/switching` addr, packet, key; `net/ip` addr, key; `net/routing` addr,
key; `net/filter` addr, packet, key; `net/qos` addr, packet, filter, key,
measure; `net/nat` addr, packet, key; `net/wlan` addr, key, measure,
switching; `net/cellular` key, measure; `net/endpoint` addr, key, measure,
switching, wlan; `net/portaccess` addr, key, switching; `net/system`
measure; `net/multicast` addr, key, switching; `net/aaa` addr; `net/flow`
addr, packet; `net/log` none; `net/phy` measure; `net/interface` its
current set plus key; every `net/protocol/*` row every non-protocol `net/`
package; `model/inventory` its current set plus key, measure, wlan, system;
`model/wireless` model/inventory, addr, key, switching, wlan;
`model/endpoint` model/inventory, addr, key, measure, switching, wlan,
endpoint; `model/alarm` model/inventory, log; `event/log` model/inventory,
addr, log. `schema_language_test.go` adds `TestNoFloatingPointFields`
(allowlist: the three fields the parent's requirement 2 names) with its
synthetic-descriptor failure case.
Tests: `key_rules_test.go` (requirement 1), `measure_rules_test.go`
(requirement 2, one ordered-threshold failure per quantity, the
empty-oneof failure, basis points 10000 accepted and 10001 rejected,
`PathQuality` with a negative latency rejected), the layering test's
existing cases plus `{importer: "net/wlan", imported: "net/measure", want: true}`
and `{importer: "net/key", imported: "net/addr"}`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/key spec/proto/flowseer/net/measure test/conformance/proto`

### U3. `net/phy` on the canonical units

Files: spec/proto/flowseer/net/phy/v1/{module_diagnostics,module_lane,poe_facet,poe_settings,pse_budget,pluggable_module,ethernet_facet}.proto,
deletes spec/proto/flowseer/net/phy/v1/{module_temperature,supply_voltage,bias_current,optical_power}.proto,
spec/proto/flowseer/net/phy/v1/README.md, test/conformance/proto/phy_rules_test.go,
src/modules/localnet/snmpmap/{phy.go,phy_ddm.go,phy_test.go,phy_ddm_test.go},
src/common/netsim/vswitch/phy/{poe.go,diff.go,*_test.go},
src/common/netsim/vswitch/netmodel/{export.go,netmodel.go,*_test.go},
src/common/netsim/vswitch/switch_test.go,
src/protocol/snmp/test/integration/{t4_manual_verify_test.go,README.md},
generated/go/proto/flowseer/net/phy/
After: U2
Change: `ModuleDiagnostics.temperature` is `measure.v1.Temperature` and
`.voltage` is `measure.v1.Voltage`; `ModuleLane.tx_power` and `rx_power`
are `measure.v1.Power` and `bias` is `measure.v1.Current`. The four phy
quantity messages are deleted. `PoeFacet.power_draw_milliwatts` and
`allocated_power_milliwatts` become `uint64 *_nanowatts`;
`PseBudget.power_milliwatts` and `consumption_milliwatts` likewise;
`usage_threshold_percent` becomes `usage_threshold_basis_points` with
`gte: 100, lte: 9900`. `nominal_bit_rate_mbps` becomes `uint64
nominal_bit_rate_bps`. `EthernetFacet.settings` exists. Numbering follows the
Decisions: every field whose type or unit changes is new, with the old
number and name `reserved`. Temperature, voltage, current, and optical power keep the
scale `phy_ddm.go` applies today; only the wire types change (voltage and
current become signed, power becomes `uint64`), so the `setSigned`,
`setUnsigned`, `setNonzero`, and `setHpDbm` helpers gain the matching
setter signatures and their overflow bounds follow the new types. PoE
milliwatts convert to nanowatts (×1 000 000) and the module bit rate from
Mbps to bps (×1 000 000). `src/common/netsim/vswitch/phy` holds PoE power
in nanowatts end to end.
Tests: `phy_rules_test.go` updated to the new fields; `phy_ddm_test.go`
gains the requirement 3 and requirement 5 D-Link fixtures and a voltage
case below zero that the old `uint32` could not hold; `phy_test.go`
asserts requirement 4 from a `POWER-ETHERNET-MIB` fixture; the netsim PoE tests keep
their assertions in nanowatts.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/phy test/conformance/proto src/modules/localnet/snmpmap src/common/netsim/vswitch src/protocol/snmp/test/integration`

### U4. `MacAddress` and the interface-name key rules

Files: spec/proto/flowseer/net/addr/v1/{eui.proto,oui.proto,README.md},
every `.proto` under spec/proto/flowseer/ that uses `EuiAddress` or
declares an interface-name field (the `namesAnInterface` predicate finds
them: net/switching, net/ip, net/interface, net/capture, net/protocol/*,
model/inventory, model/access, model/capture),
test/conformance/proto/{field_constraint_class_test.go,addr_rules_test.go,ip_rules_test.go,schema_language_test.go},
src/common/netsim/vswitch/netmodel/, src/modules/localnet/snmpmap/ifmib.go,
src/modules/capture/filter/compile.go, generated/go/proto/flowseer/
After: U3
Change: `EuiAddress` is renamed `MacAddress` in the schema and every Go
consumer, and the two interface-parent fields are renamed as the Decisions
say (keeping their numbers: type and meaning do not change); `Eui48Address` and `Eui64Address` keep their names, because they
name the IEEE formats. Every interface-name field carries one `net/key`
rule per the Decisions and drops its own `min_len`, `max_len`, and
`pattern`. `Interface.name` takes `interface_name`, which gives it the
upper bound it lacks today. `field_constraint_class_test.go`'s `exempt`
map and `interfaceNamePattern` constant are deleted; its walk becomes
`TestKeyFieldsUseKeyRules` in `schema_language_test.go`, which fails a
field that carries neither rule or both, or that carries its own length or
pattern beside one, and keeps the existing carrier-shape cases
(`TestTheClassWalkReadsEveryCarrierShape`) against the new expectation.
Tests: `addr_rules_test.go` builds `MacAddress`; the key-rule walk passes
over the tree and fails on the synthetic carriers; the capture filter and
netmodel tests pass with the renamed type.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer test/conformance/proto src/common/netsim src/modules/localnet src/modules/capture`

### U5. Counters, LLDP TTL, the address origin, and the unit-suffix gate

Files: spec/proto/flowseer/net/interface/v1/interface_counters.proto,
spec/proto/flowseer/net/phy/v1/ethernet_counters.proto,
spec/proto/flowseer/net/capture/v1/capture_counters.proto,
spec/proto/flowseer/net/protocol/lldp/v1/neighbor.proto,
spec/proto/flowseer/net/ip/v1/{address_origin.proto,interface_identifier_method.proto,interface_address.proto,README.md},
test/conformance/proto/{schema_language_test.go,ip_rules_test.go,lldp_rules_test.go,interface_rules_test.go},
src/common/netsim/vswitch/netmodel/{counters.go,counters_test.go,netmodel.go},
src/common/netsim/fabric/{counters.go,*_test.go},
src/modules/localnet/snmpmap/{ifmib.go,ifmib_test.go,lldp.go},
src/modules/capture/{engine.go,pcapng/writer.go,pcapng/writer_test.go},
src/services/device/internal/captureapi/store_test.go,
generated/go/proto/flowseer/
After: U4
Change: the counters, TTL, and address-origin changes in the Decisions.
Mappers fill `last_discontinuity` where the source has one (IF-MIB
`ifCounterDiscontinuityTime` is a `sysUpTime` value, so converting it
needs the agent's boot time; `snmpmap` reads neither today, see Open
questions) and leave it unset otherwise; netsim sets it to the simulated link's start. `snmpmap` maps
IP-MIB `ipAddressOrigin` `manual(2)` to `STATIC`, `dhcp(4)` to `DHCP`,
`linklayer(5)` to `SLAAC` + `MODIFIED_EUI64` (IPv6) or `LINK_LOCAL`
(IPv4 169.254/16), `random(6)` to `SLAAC` + `RANDOMIZED`, `other(1)` to
`OTHER`. `schema_language_test.go` adds `TestCanonicalUnitSuffixes` and
`TestCountersCarryDiscontinuity`, each with a synthetic failure case.
Tests: `ifmib_test.go` table cases for every `ipAddressOrigin` value above
and for `ifCounterDiscontinuityTime` of zero (unset) and nonzero;
requirement 7 in `ip_rules_test.go`; `lldp_rules_test.go` rejects a TTL of
65536 s; netsim and capture counter tests use the renamed fields.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer test/conformance/proto src/common/netsim src/modules/localnet src/modules/capture src/services/device`

Waves: U1 U2 | U3 | U4 | U5

U3 to U5 are a chain because each rewrites `netmodel.go` and the `snmpmap`
mappers; their protos are disjoint, their consumers are not.

## Verification

```bash
buf lint
buf generate && git status --porcelain generated/   # empty after each commit
go build ./... && go vet ./...
go test -race ./test/conformance/... ./src/...
.claude/skills/verify-change/scripts/verify-change.sh --full
grep -rn 'EuiAddress\|_milliwatts\|_percent\b\|_mbps\|time_to_live_seconds\|ADDRESS_ORIGIN_RANDOM' spec/proto/flowseer   # empty
```

A lab check is not needed: every mapper change has a fixture with known
source bytes, and no value on the wire to a device changes.

## Definition of done

- [ ] Verifier green with `--full`.
- [ ] `net/key`, `net/measure`, `net/phy`, `net/addr`, and `net/ip` READMEs
      match their packages; `docs/conventions/protobuf.md` and
      `docs/code-style-proto.md` carry the units, keys, and extension
      registry.
- [ ] The parent's `Landed:` line for this phase carries the commit range.
- [ ] This plan's `status` is `implemented` with an outcome note under the
      title; no plan label in code, comments, or commit messages.

## Open questions

- Whether U5 adds a `sysUpTime` read to `snmpmap` so it can convert
  `ifCounterDiscontinuityTime`, or leaves `last_discontinuity` unset for
  SNMP sources and says so in the mapper's doc comment. The field is
  declared either way; the implementer picks the smaller change and
  records it in the ledger note.

- Review (accept) flagged an enforcement gap, not a defect: the
  `namesAnInterface` predicate in `test/conformance/proto/schema_language_test.go`
  matches `interface_name`, `*_interface_name`, and `managed_interfaces`, but
  not the bare `Interface.name` field in `net/interface/v1/interface.proto`,
  which carries `interface_name` correctly today. A future edit dropping that
  field's key rule would pass `TestKeyFieldsUseKeyRules`. Decide whether the
  walk should also cover the canonical `name` field (or the field be spelled to
  match the predicate). Deferred to compound or a follow-up; the landed schema
  is compliant.
