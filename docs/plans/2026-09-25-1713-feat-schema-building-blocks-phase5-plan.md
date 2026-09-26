---
title: Schema Building Blocks Phase 5, Platform, System, and Operations - Plan
type: feat
date: 2026-09-25
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept
compound: no lesson
execution: code
parent: docs/plans/2026-09-25-1713-feat-schema-building-blocks-plan.md
---

# Schema Building Blocks Phase 5, Platform, System, and Operations - Plan

> Implemented. 5 units, 2026-09-26T10:44:34Z to 2026-09-26T11:11:15Z. Targeted verification run over union of changed paths per directive (replacing --full).

## Goal

A device's hardware health, resources, software, time sync, alarms, and
logs get a place in the schema. `ComponentState` gains sensor rows, an
operational status, and per-component CPU and storage utilization.
`DeviceState` gains system contact, location, uptime, and the whole-box
utilization for sources with no component breakdown. `net/system/v1` holds
the utilization values, software images, and licenses; `net/protocol/ntp/v1`
holds NTP associations; `net/log/v1` holds the RFC 5424 severity and facility
registries. The `Alarm` entity lands in `model/alarm/v1` and the
`SyslogRecord` event in `event/log/v1`. The means is new messages and
fields, each pinned by conformance rule tests, plus the README, layering,
and `CONCEPTS.md` lines the gates hold them to. Nothing in this phase is
required on a landed message, so no Go consumer changes.

Stop condition: a targeted source reports two readings of the same
quantity for one component and gives no name to either measurement point,
and a child sensor component cannot be named for each. The
one-reading-per-quantity rule on `ComponentState` would then drop data,
and the phase stops for a re-cut of the sensor row before it lands.

## Decisions

- **`ComponentState` carries `repeated flowseer.net.measure.v1.SensorReading
  sensors`, with at most one reading per quantity.** Why: a power supply
  reports voltage, current, and power at once, so the field is repeated,
  and `SensorReading` is the landed shape the record gives every domain
  (`spec/proto/flowseer/net/measure/v1/sensor.proto`); it is reused, not
  redefined. A reading carries no name, so two readings of one quantity on
  one component could not be told apart. ENTITY-MIB models every sensor as
  its own physical entity of class `sensor(8)` with one value
  (`entPhySensorEntry` is indexed by `entPhysicalIndex`,
  `spec/mib/ietf/ENTITY-SENSOR-MIB:297`), and openconfig-platform-psu reports input and output
  voltage and current on the PSU itself
  (`spec/yang/cisco/iosxe/2611/openconfig-platform-psu.yang:85-113`). The
  rule follows the MIB: a second measurement point of one quantity is a
  child component of kind sensor, named by path the way
  `ComponentLocalRef.name` already allows ("the path of names from the
  chassis down", `component.proto`), for example `PSU 1/input` beside the
  supply's own output readings. A message CEL rule rejects a second reading
  of the same arm. The AC/DC voltage distinction (dossier 04, trap 2)
  follows the same route: the input sensor component's description says
  which it is, and no new arm is added.
- **`ComponentState` gains `oper_status`, a `ComponentOperStatus` with the
  interface's values.** Values: `UNSPECIFIED = 0`, `UP = 1`, `DOWN = 2`,
  `TESTING = 3`, `UNKNOWN = 4`, the same numbers
  `net/interface/v1.OperStatus` uses for the same words
  (`spec/proto/flowseer/net/interface/v1/oper_status.proto:11-19`). Why:
  ENTITY-STATE-MIB's `EntityOperState` is `unknown(1)`, `disabled(2)`,
  `enabled(3)`, `testing(4)` (`spec/mib/ietf/ENTITY-STATE-TC-MIB:79-82`),
  RFC 8348's `oper-state` repeats it, and openconfig's
  `COMPONENT_OPER_STATUS` is `ACTIVE` (up), `INACTIVE` (down), `DISABLED`
  (`spec/yang/cisco/iosxe/2611/openconfig-platform-types.yang:252-268`).
  The mapping (enabled and ACTIVE to up; disabled, INACTIVE, and DISABLED
  to down; `entPhySensorOperStatus` ok to up, nonoperational to down,
  unavailable to unknown, `ENTITY-SENSOR-MIB:245-260`; `hrDeviceStatus`
  running and warning to up, down to down) goes in the inventory README.
  `entStateOper` does not follow the administrative state
  (`spec/mib/ietf/ENTITY-STATE-MIB:262-276`), so there is no admin field.
  The enum lives in `component.proto` beside the entity (conventions doc,
  Enums).
- **Utilization is a `net/system` value carried by the component it
  describes, and by `DeviceState` when the source has no component
  breakdown.** `ProcessorUtilization` holds `utilization_avg_basis_points`
  beside a `window` Duration, the name and shape the record's rule 2 gives a
  windowed statistic. `hrProcessorLoad` is "the average, over the last
  minute" and may approximate that period
  (`spec/mib/ietf/HOST-RESOURCES-MIB:587-596`), so its mapper
  sets `window` to 60 s; openconfig-platform-cpu reports its own `interval`
  (`spec/yang/cisco/iosxe/2611/openconfig-types.yang:153-163`, `:401-409`);
  a source that states no window (UniFi `cpuUtilizationPct`, dossier 04 §3)
  leaves `window` unset, and the field comment says unset means the source
  did not say. `StorageUtilization` holds `kind`, `total_bytes`,
  `used_bytes`, and `used_basis_points` for RAM, flash, and disk alike,
  because HOST-RESOURCES-MIB's `hrStorageTable` covers all three
  (`spec/mib/ietf/HOST-RESOURCES-TYPES:37-105`) and a mapper multiplies
  `hrStorageSize` and `hrStorageUsed` by `hrStorageAllocationUnits`
  (`HOST-RESOURCES-MIB:349-359`) before filling the bytes; a source that
  gives only a percentage (UniFi `memoryUtilizationPct`) fills
  `used_basis_points` alone. On `ComponentState`, `processor_utilization`
  is set only on kind CPU and `storage_utilization` only on kind storage,
  as `module` and `radio` are today. On `DeviceState`,
  `processor_utilization` and `repeated storage_utilization` hold the
  whole-box reading from sources such as UniFi, MikroTik
  `/system/resource`, and LANCOM `cpuLoadPercent` (dossier 04 §3); each
  device-level storage row requires a `name` (`hrStorageDescr`, a
  `DisplayString`, `HOST-RESOURCES-MIB:340-347`) and names are unique. The
  field comments say a consumer reads the component rows when they exist
  and the device-level ones otherwise.
- **`net/system` is the right name; the record stands.**
  openconfig-system groups exactly this content under `/system`: `cpus`
  (`spec/yang/cisco/iosxe/2611/openconfig-system.yang:1295`, `:1360`),
  `memory` (`:1184`), and `license` (`uses oc-license:license-top`,
  `:1356`). HOST-RESOURCES-MIB names the device-wide group `hrSystem`. The
  record's other candidate, `net/env`, was folded into `net/measure`, and
  the sensors live there. System identity (contact, location, uptime) is
  `DeviceState`'s, not this package's, and the package README says so. This
  closes the parent's open question without amending the record.
- **`DeviceState` gains `system_contact`, `system_location`, and `uptime`.**
  Contact and location are `DisplayString (SIZE (0..255))` with the empty
  string meaning none known (`spec/mib/ietf/SNMPv2-MIB:114-144`, RFC 3418),
  so each is `min_len 1`, `max_len 255`, and a mapper leaves an empty value
  unset. `system_location` is the device's own free text and unrelated to
  `DeviceConfig.location`, FlowSeer's placement; the comment says so.
  `uptime` is a `Duration`, not a boot time, because a boot time derived
  from it inherits both of its faults: `sysUpTime` counts `TimeTicks`
  (`0..4294967295` hundredths of a second, `spec/mib/ietf/SNMPv2-SMI:196-198`,
  about 497 days before it wraps) and measures "the network management
  portion of the system" (`SNMPv2-MIB:104-112`), which some agents
  restart without a reboot; `hrSystemUptime` is the host's
  (`HOST-RESOURCES-MIB:166-176`). The comment says the value is relative to
  the observation time on the envelope that carried the state, that it
  wraps and can reset, and that a reboot is never inferred from it.
- **NTP associations are rows in `net/protocol/ntp/v1`, keyed by
  `(network_instance, address)`.** `NtpAssociation` carries the RFC 5905
  peer variables the sources report: `stratum` (`uint32`, `lte 255`: the
  field is 8 bits, 0 is unspecified or invalid, 16 unsynchronized, 17 to
  255 reserved, RFC 5905 §7.3), `reference_id` (4 octets, §7.3), `reach`
  (the 8-bit shift register, §9.2, `lte 255`), `offset`, `delay`,
  `dispersion`, and `jitter` as `Duration` (§8, §9.1; record rule 1), and
  `poll_interval` (§7.3 gives it in log2 seconds; the mapper converts).
  `stratum` accepts the reserved range rather than `lte 16`, because
  rejecting a value a device really sent fails the whole row (record rule
  3). The required `network_instance` follows rule 4: IOS-XE keys the
  address with a `vrf-name`
  (`spec/yang/cisco/iosxe/2611/Cisco-IOS-XE-ntp-oper.yang:604-620`) and
  openconfig-system binds a server to a `network-instance`
  (`spec/yang/cisco/iosxe/2611/openconfig-system.yang:802-810`).
  `address` is an `IpAddress`, because an association's peer is an address
  on the wire (`ntpAssocAddress`,
  `spec/mib/lancom/sx/sx-5.30-ys7154cf/ntpv4.mib:534-543`); the configured
  host name is an optional `name` bounded at 253 (a DNS name, record rule
  3). `selection` is a FlowSeer-normalized `NtpPeerSelection` from IOS-XE's
  `peer-select-status` (`Cisco-IOS-XE-ntp-oper.yang:277-328`): rejected,
  false ticker, excess, outlier, candidate, backup, system peer, PPS peer.
  It answers "which server is the clock following", which the MIB cannot
  (its selection lives in system-wide scalars, `ntpv4.mib:192-248`). No
  message holds the rows: like every table since phase 2, they are values
  a later store or service carries, so `model/inventory` does not import
  this package.
- **Software images and licenses are rows in `net/system/v1`, not fields
  on `DeviceState`.** `SoftwareImage` is keyed by `slot`, the device's own
  name for where the image lives, because "everybody has a dual-image
  concept ... and nobody names it the same" (atlas
  `entities/01-platform.md:401-404`): Aruba CX keys by `primary` and
  `secondary` (`spec/mib/aruba/cx/ARUBAWIRED-SWITCH-IMAGE-MIB:93-133`) and
  IOS-XE by version (`Cisco-IOS-XE-install-oper.yang:1453-1458`), so a
  string beats an enum of slot names. It carries `version`, `running`,
  `next_boot` (Aruba `arubaWiredDefaultBootEnum`, `:64-73`), and
  `size_bytes`. `License` is keyed by `name` (openconfig `license-id`,
  `spec/yang/openconfig/openconfig-license.yang:45-50`; IOS-XE smart
  licensing's entitlement, `cisco-smart-license.yang:1049-1082`) and
  carries `description`, `status`, `issued_at`, `expires_at`, and
  `entitlement_count`. `LicenseStatus` is FlowSeer-normalized (inactive,
  in use, evaluation, expired, out of compliance) from openconfig's
  `in-use`, `expired`, and `valid` booleans (`openconfig-license.yang:98-117`),
  `clmgmtLicenseStatus`
  (`spec/yang/cisco/iosxe/2611/MIBS/CISCO-LICENSE-MGMT-MIB.yang:1699-1739`),
  and smart licensing's enforcement mode (`cisco-smart-license.yang:238-315`);
  the mapping goes in the package README. openconfig's
  `expiration-date` of zero means "never"; `expires_at` unset covers both
  "never" and "not reported", and the comment says so.
- **`SoftwareImage` and `License` carry a required `network_instance`;
  the utilization values do not.** Why: the record's summary says "Every
  table whose key is not already scoped by an interface carries a required
  network-instance name" (`docs/architecture/2026-09-25-schema-building-blocks-direction.md:32-33`),
  and both rows are device tables keyed by a slot or a name. Phase 6
  applied the same sentence to `MstInstance`. The mapper names the
  device's default instance, `default` when the device has no name for it
  (record rule 4). `ProcessorUtilization` and `StorageUtilization` are
  values carried inside `ComponentState` and `DeviceState`, not table rows
  (record rule 5), so the sentence does not reach them. Rule 4's own
  reason is forwarding tables, and an image or license has no forwarding
  context, so this reads the record literally rather than amend it; the
  amendment is an open question below.
- **Syslog severity and facility are pass-through enums in `net/log/v1`
  with real zeros.** `SyslogSeverity` is `EMERGENCY = 0` through
  `DEBUG = 7`; `SyslogFacility` is `KERN = 0` through `LOCAL7 = 23`, the
  RFC 5424 §6.2.1 tables (record rule 7). The registries are complete, so
  `enum.defined_only` is the whole numeric domain and no predefined rule
  is needed; the `EnumRules` extension number 50004 stays free
  (`docs/code-style-proto.md`, predefined-rule table). Presence carries
  "not reported", and a consumer checks it before reading the getter, as
  for `IpProtocol` (conventions doc, Enums). The conventions doc's
  pass-through paragraph gains one sentence naming both, so the doc and
  the schema agree.
- **`SyslogRecord` in `event/log/v1` is one received message.** It requires
  the device, a UUID `record_id` for deduplication (the
  `DeviceOperationEvent.event_id` precedent), `received_at`, `severity`,
  and `facility`: PRI is mandatory, and the priority is facility times 8
  plus severity (RFC 5424 §6.2.1). The header fields keep RFC 5424 §6's
  bounds: `hostname` 255, `app_name` 48, `proc_id` 128, `msg_id` 32;
  `sent_at` is the header TIMESTAMP, unset when the sender sent NILVALUE.
  `structured_data` is a repeated element of an `id` and `params`, SD-ID
  and PARAM-NAME at most 32 (§6); a CEL rule rejects a repeated SD-ID,
  because "The same SD-ID MUST NOT exist more than once in a message"
  (§6.3.2). `message` is `bytes`, because MSG is `MSG-ANY / MSG-UTF8`
  and only the UTF-8 form is marked by a BOM (§6.4; record rule 8), bounded
  at 65527 octets: UDP carries "syslog messages up to 65535 octets minus
  the UDP header length" (RFC 5426 §3.2), so every UDP MSG fits. A
  collector on another transport can receive a longer one; it stores the
  first 65527 octets and sets `message_truncated`, so a long message never
  fails the whole record (record rule 3). `source_address` is the sender address the collector
  saw, from `net/addr`. A sender that does not use the RFC 5424 header
  leaves the header fields unset. The layering row
  (`test/conformance/proto/layering_test.go:157`) already permits
  `model/inventory`, `net/addr`, and `net/log`.
- **The Alarm is keyed by a typed resource oneof plus the alarm type.**
  RFC 8632 §3.4 identifies an alarm instance by "the tuple (resource,
  alarm-type identifier, and alarm-type qualifier)". `AlarmLocalRef` holds
  `resource` (a required `AlarmResource`), `type_id` (required), and
  `type_qualifier` (optional); `AlarmGlobalRef` composes it with
  `DeviceGlobalRef` (conventions doc, the ref pair). `AlarmResource` is a
  required `target` oneof of `WholeDevice device = 1` (an empty message:
  ALARM-MIB's resource `0.0`, "no corresponding resource",
  `spec/mib/ietf/ALARM-MIB:530-539`), `ComponentLocalRef component = 2`,
  `string interface_name = 3` (the `interface_name` key rule), and
  `string other = 4` (the device's own name or path for a resource none of
  the arms addresses, such as a BGP peer or a process; 1 to 1024, record
  rule 3). Why a oneof over one resource string: RFC 8632's `resource` is a
  union of instance-identifier, object-identifier, string, and UUID
  (§3.3), and openconfig's `resource` is a string that names a component
  or interface "exactly" (`spec/yang/openconfig/openconfig-alarms.yang:95-109`),
  so a string would push that parsing into every consumer that joins an
  alarm to `ComponentState` or an interface row, the problem typed
  variants remove (conventions doc, Typed variants). `EntityRef` cannot
  address a nested component (conventions doc, EntityRef), and a
  `ComponentGlobalRef` arm would repeat the device the global ref already
  carries, so the arm is the local ref. The `other` arm keeps every
  resource representable. A oneof holds at most one arm and wire decoding
  is last-tag-wins, so both arms set is unrepresentable; `required` rejects
  the empty case, and the test pins that case only
  (`docs/solutions/conventions/a-oneof-both-arms-set-is-unrepresentable-so-validate-the-empty-case.md`).
  The mapper resolves the arm the same way on raise and clear, so the key
  stays stable; the README says so. `type_id` is a string because the
  sources disagree on its form: an identity or mnemonic in openconfig
  (`type-id`, `openconfig-alarms.yang:143-160`), a model index in ALARM-MIB
  (`alarmModelIndex`, `ALARM-MIB:170-178`), an integer code in SmartZone
  (`alarmCode`, `spec/openapi/ruckus/vsz/vsz-7.1.1-v13_1-openapi.json:82316`).
- **`AlarmState` holds severity and clearing as separate fields.**
  `AlarmSeverity` is FlowSeer-normalized in urgency order: `UNSPECIFIED = 0`,
  `INDETERMINATE = 1`, `WARNING = 2`, `MINOR = 3`, `MAJOR = 4`,
  `CRITICAL = 5`. Why: RFC 8632 orders the same five from indeterminate
  to critical and excludes clear ("Whether or not an alarm is cleared is a
  separate boolean flag", §3.2); ITU's `ItuPerceivedSeverity` numbers them
  in another order with `cleared(1)` inside
  (`spec/mib/ietf/ITU-ALARM-TC-MIB:59-67`), and openconfig's `UNKNOWN` is
  indeterminate (`spec/yang/cisco/iosxe/2611/openconfig-alarm-types.yang:103-148`).
  A mapper maps by name, never by number. `cleared` is a required bool
  (RFC 8632 `is-cleared`, §4.4): the question "is this still broken" is the
  reason the entity exists (atlas `entities/09-ops.md:123-126`).
  `severity` is optional, because LANCOM reports alerts without one
  (`spec/openapi/lancom/lmc-openapi/devices.json:3248-3253`). The state also
  carries `text` (1 to 1024), `created_at`, `last_raised_at`, and
  `last_changed_at` (RFC 8632 §4.4).
- **`AlarmEvent` is the before-and-after shape `ComponentEvent` uses.** An
  unset `before` means the device raised a new alarm; an unset `after`
  means the source stopped listing it. A clear is an `after` with
  `cleared` true, reported only when the source reports the clear
  (ALARM-MIB's `alarmClearTable`, `ALARM-MIB:858-1017`; RFC 8632
  `is-cleared`). A source that lists only active alarms, as openconfig
  does, yields an unset `after` when an alarm leaves the list, never an
  inferred clear. The side-matches-ref rules compare `ref` with CEL `==`,
  which protovalidate evaluates as protobuf message equality over the
  whole composite key; a test pins a mismatch in `type_qualifier` alone.
  There is no `AlarmConfig`: the device raises and clears an alarm, and
  operator acknowledgement (RFC 8632 `operator-state-change`) is not
  modeled here. The family file's file-level comment names the absence.
- **The `model/alarm` layering row becomes `{model/inventory, net/key}`.**
  Phase 1 guessed `net/log`
  (`test/conformance/proto/layering_test.go:130`); the alarm uses no
  syslog registry, and its `interface_name` arm needs the `net/key` rule
  that `TestKeyFieldsUseKeyRules` requires. The row is this phase's own.
- **Nothing new is required on a landed message.** `ComponentState` and
  `DeviceState` gain optional fields only, so
  `src/common/netsim/vswitch/netmodel`, `src/modules/localnet/snmpmap`, and
  the existing inventory tests compile and pass unchanged. No mapper fills
  the new fields in this phase (parent, Out of scope), so each fixture
  proves that source values fit a row, not that a mapper produces them.
- **Tests reuse the phase 6 helpers.** Each new rule test uses
  `fieldCase` and `runFieldCases` from
  `test/conformance/proto/stp_rules_test.go:17-25`, which assert the
  violated field path or the rule id, so a message rejected by another
  rule does not pass for the one under test. Inventory fixtures reuse
  `deviceRef` and `componentRef`
  (`test/conformance/proto/model_inventory_event_rules_test.go:27`,
  `model_inventory_topology_rules_test.go`).

## Requirements

1. A power supply reports several quantities at once, and a reading needs
   a quantity. Example: `ComponentState{kind POWER_SUPPLY, sensors
   [voltage{value_microvolts 12000000}, current{value_microamperes
   8500000}, power{value_nanowatts 102000000000}]}` passes; `sensors
   [SensorReading{}]` fails at `sensors[0].quantity` with "exactly one
   field is required"; two voltage readings fail with rule
   `component_state.one_reading_per_quantity`.
2. Component status and utilization validate by kind. Example:
   `oper_status UP` passes and `0` fails; a CPU component with
   `processor_utilization{utilization_avg_basis_points 4250, window 60s}`
   passes; the same on a fan fails with
   `component_state.processor_utilization_only_cpu`;
   `utilization_avg_basis_points 10001` fails; `window 0s` fails;
   `storage_utilization` on a CPU fails with
   `component_state.storage_utilization_only_storage`;
   `StorageUtilization{total_bytes 100, used_bytes 101}` fails with
   `storage_utilization.used_within_total`.
3. System identity and whole-box utilization validate on `DeviceState`.
   Example: a 255-character `system_contact` passes and 256 fails; an
   empty `system_location` fails; `uptime 42949672.95s` (the largest
   `sysUpTime`) passes and `-1s` fails; a device-level storage row without
   `name` fails with `device_state.storage_named`; two rows named `flash`
   fail with `device_state.storage_names_unique`.
4. `SyslogSeverity` value 0 is `EMERGENCY` and presence distinguishes it
   from unset. Example: a `SyslogRecord` with `severity EMERGENCY` and
   `facility KERN` passes and `HasSeverity()` is true; the same record
   without `severity` fails with `severity: value is required`;
   `severity 8` and `facility 24` fail (`defined_only`).
5. A syslog record keeps RFC 5424's bounds. Example: `app_name` of 49
   characters fails; two structured-data elements with id `timeQuality`
   fail with `syslog_record.sd_ids_unique`; an SD-ID of 33 characters
   fails; a `message` of 65528 octets fails and 65527 passes with
   `message_truncated true`; the record
   without `device`, `record_id`, or `received_at` fails.
6. An `AlarmState` without its device ref fails. Example: `ref.device`
   unset fails with `value is required`; `ref.alarm.resource` with no arm
   fails at `ref.alarm.resource.target` with "exactly one field is
   required"; each of the four arms alone passes (`device {}`, `component
   {name "PSU 1"}`, `interface_name "GigabitEthernet1/0/1"`, `other
   "bgp peer 192.0.2.1"`); `type_id` unset fails; `cleared` unset fails;
   `severity 0` fails.
7. An alarm event names one alarm. Example: an `AlarmEvent` whose
   `before.ref` differs from `ref` only in `type_qualifier` fails with
   `alarm_event.before_matches_ref`; one with neither side fails with
   `alarm_event.one_side`; a raise (`after` only) and a clear (`before`
   uncleared, `after` cleared) pass.
8. `NtpAssociation.stratum = 256` fails. Example: stratum 0, 16, and 255
   pass; the row without `network_instance` fails with `value is
   required`; `reach 256` fails; a 3-octet `reference_id` fails; `jitter
   -1ms` fails; `offset -5ms` passes.
9. The `net/system` rows validate their keys. Example: a `SoftwareImage`
   without `slot` or without `network_instance` fails;
   `License{network_instance "default", name "ipbase", status 0}` fails;
   with `status IN_USE` it passes.
10. The conformance gates hold the new shapes: `TestImportOrder`,
    `TestProtoReadmeImports`, `TestProtoReadmeCoverage`,
    `TestKeyFieldsUseKeyRules`, `TestCanonicalUnitSuffixes`, and
    `TestEveryDeclaredProtoPackageIsLinked` pass with `net/system/v1`,
    `net/log/v1`, `net/protocol/ntp/v1`, `model/alarm/v1`, and
    `event/log/v1` present, and the proto hook reports no family gap for
    the Alarm family.

## Out of scope

- Mappers that fill any new field or row (parent, Out of scope).
- Operator acknowledgement, shelving, and closing of alarms, the ITU event
  type and probable cause, and the ALARM-MIB model catalogue
  (`alarmModelTable`).
- Syslog forwarding settings on a device (remote hosts, levels), and the
  BSD syslog header parse.
- NTP server configuration, authentication keys, per-association packet
  counters, and the system clock and leap status.
- Configuration-file and backup state, stacking serials (dossier 04, trap
  7), component administrative state, and the PoE budget reconciliation
  (dossier 04, open question 1).
- `EntityType` admission for Alarm, stores, and services (record,
  Entities).
- Vendoring `ietf-hardware`, `ietf-system`, or `ietf-alarms` YANG; this
  plan cites the RFC text.

## Units

### U1. `net/system` and `net/log` leaves

Files: `spec/proto/flowseer/net/system/v1/{processor_utilization.proto,storage_utilization.proto,storage_kind.proto,software_image.proto,license.proto,license_status.proto,README.md}`,
`spec/proto/flowseer/net/log/v1/{syslog_severity.proto,syslog_facility.proto,README.md}`,
`generated/go/proto/flowseer/net/system/v1/`,
`generated/go/proto/flowseer/net/log/v1/`,
`spec/proto/flowseer/net/README.md`,
`spec/proto/flowseer/net/measure/v1/README.md`,
`spec/proto/flowseer/net/key/v1/README.md`,
`test/conformance/proto/layering_test.go`,
`docs/conventions/protobuf.md`,
`test/conformance/proto/system_rules_test.go` (new),
`test/conformance/proto/log_rules_test.go` (new)
After: none

Change:

- `ProcessorUtilization`: `utilization_avg_basis_points = 1` (`uint32`,
  required, `measure.v1.basis_points`), `window = 2` (`Duration`, `gt 0s`; unset
  means the source did not state its averaging window).
- `StorageKind` (normalized from `hrStorageTypes`,
  `HOST-RESOURCES-TYPES:37-105`): `UNSPECIFIED = 0`, `OTHER = 1`,
  `RAM = 2`, `VIRTUAL_MEMORY = 3`, `FIXED_DISK = 4`, `REMOVABLE_DISK = 5`,
  `FLASH_MEMORY = 6`; floppy, compact disc, RAM disk, and network disk map
  to `OTHER`.
- `StorageUtilization`: `name = 1` (1 to 255, `hrStorageDescr`; unset on a
  component, which names itself), `kind = 2` (`defined_only`,
  `not_in [0]`), `total_bytes = 3`, `used_bytes = 4`,
  `used_basis_points = 5` (`uint32`, `basis_points`); the three byte
  fields are `uint64` (record rule 1). CEL
  `storage_utilization.used_within_total`: `used_bytes <= total_bytes`
  when both are present.
- `SoftwareImage`: `network_instance = 1` (required,
  `network_instance_name`), `slot = 2` (required, 1 to 255), `version = 3`
  (1 to 128, the bound of `DeviceState.software_version`), `running = 4`
  and `next_boot = 5` (`bool`), `size_bytes = 6` (`uint64`). Unset
  booleans mean the source does not say.
- `LicenseStatus`: `UNSPECIFIED = 0`, `INACTIVE = 1`, `IN_USE = 2`,
  `EVALUATION = 3`, `EXPIRED = 4`, `OUT_OF_COMPLIANCE = 5`.
- `License`: `network_instance = 1` (required, `network_instance_name`),
  `name = 2` (required, 1 to 1024), `description = 3` (1 to 1024),
  `status = 4` (`defined_only`, `not_in [0]`), `issued_at = 5`,
  `expires_at = 6` (`Timestamp`), `entitlement_count = 7` (`uint32`,
  the `count` of smart licensing and `clmgmtLicenseMaxUsageCount`).
- `SyslogSeverity` (`EMERGENCY = 0`, `ALERT`, `CRITICAL`, `ERROR`,
  `WARNING`, `NOTICE`, `INFORMATIONAL`, `DEBUG = 7`) and `SyslogFacility`
  (`KERN = 0` through `LOCAL7 = 23`, names from RFC 5424 §6.2.1), each value
  prefixed with its enum name and commented with the RFC's text. Each file
  comment says the zero is a real value and presence carries "not
  reported".
- `net/system/v1/README.md`: identity (resources, images, licenses of the
  whole box; system identity is `DeviceState`'s), `Imports: net/key,
  net/measure`,
  `Imported by: nothing` (U3 changes it), the averaging-window rule, the
  allocation-unit multiplication, the component-or-device placement rule,
  the `LicenseStatus` mapping from openconfig, `clmgmtLicenseStatus`, and
  smart licensing, and why the image and license rows carry the
  default network instance.
  `net/log/v1/README.md`: `Imports: nothing FlowSeer-owned`,
  `Imported by: nothing` (U4 changes it), the pass-through class, and the
  presence check.
- `net/README.md`: the `system/v1/` and `log/v1/` lines lose their
  "(planned; ...)" marker. `net/measure/v1/README.md` and
  `net/key/v1/README.md`: `Imported by:` gains `net/system`.
- `layering_test.go:57`: the `net/system` row becomes
  `{"net/key", "net/measure"}`.
- `docs/conventions/protobuf.md`, Enums: one sentence after the
  `IpDscp`/`IpEcn`/`IpProtocol` domain rule saying `SyslogSeverity` and
  `SyslogFacility` in `net/log/v1` define every registry value, so
  `enum.defined_only` is their complete domain rule.

Tests: `system_rules_test.go` holds Requirement 9 and the
`StorageUtilization` and `ProcessorUtilization` cases of Requirement 2 on
the bare messages (`TestProcessorUtilizationRules`,
`TestStorageUtilizationRules`, `TestSoftwareImageRules`,
`TestLicenseRules`). `log_rules_test.go` holds
`TestSyslogRegistriesKeepTheirIntegers`: every value's number equals the
RFC 5424 table (0 to 7, 0 to 23), `SYSLOG_SEVERITY_EMERGENCY` and
`SYSLOG_FACILITY_KERN` are 0, and both enums have no `_UNSPECIFIED` value.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/system/v1 spec/proto/flowseer/net/log/v1 generated/go/proto/flowseer/net/system/v1 generated/go/proto/flowseer/net/log/v1 spec/proto/flowseer/net/README.md spec/proto/flowseer/net/measure/v1/README.md spec/proto/flowseer/net/key/v1/README.md test/conformance/proto/layering_test.go docs/conventions/protobuf.md test/conformance/proto/system_rules_test.go test/conformance/proto/log_rules_test.go`

### U2. NTP associations in `net/protocol/ntp`

Files: `spec/proto/flowseer/net/protocol/ntp/v1/{association.proto,peer_selection.proto,README.md}`,
`generated/go/proto/flowseer/net/protocol/ntp/v1/`,
`spec/proto/flowseer/net/protocol/README.md`,
`spec/proto/flowseer/net/addr/v1/README.md`,
`spec/proto/flowseer/net/key/v1/README.md`,
`test/conformance/proto/ntp_rules_test.go` (new)
After: U1 (both edit `net/key/v1/README.md`'s `Imported by:` line)

Change:

- `NtpPeerSelection`: `UNSPECIFIED = 0`, `REJECTED = 1`,
  `FALSE_TICKER = 2`, `EXCESS = 3`, `OUTLIER = 4`, `CANDIDATE = 5`,
  `BACKUP = 6`, `SYSTEM_PEER = 7`, `PPS_PEER = 8`, each citing its
  `Cisco-IOS-XE-ntp-oper.yang` line (`:279`-`:318`).
- `NtpAssociation`: `network_instance = 1` (required,
  `network_instance_name`), `address = 2` (`net/addr/v1.IpAddress`,
  required), `name = 3` (1 to 253), `stratum = 4` (`lte 255`, the RFC 5905
  §7.3 table in the comment, `uint32`), `reference_id = 5`
  (`bytes.len = 4`), `reach = 6` (`uint32`, `lte 255`), `offset = 7`, `delay = 8`, `dispersion = 9`
  (`gte 0s`), `jitter = 10` (`gte 0s`), `poll_interval = 11` (`gt 0s`),
  `selection = 12` (`defined_only`, `not_in [0]`). Every field but the key
  is optional; unset means unreported.
- README: the key and why it carries the instance, the stratum range and
  why it is not `lte 16`, the unit conversions (IOS-XE `offset`, `delay`,
  and `jitter` are decimal milliseconds,
  `Cisco-IOS-XE-ntp-oper.yang:661-681`; openconfig's are nanoseconds,
  `openconfig-system.yang:846-876`; NTPv4-MIB's are display strings,
  `ntpv4.mib:545-585`), and that no row catches two rows with the same key.
  `Imports: net/addr, net/key`, `Imported by: nothing`.
- `net/protocol/README.md`: the `ntp/v1/` line loses its marker.
  `net/addr/v1/README.md` and `net/key/v1/README.md`: `Imported by:` gains
  `net/protocol/ntp`.

Tests: `ntp_rules_test.go`, `TestNtpAssociationRules`, holds Requirement 8
with a fixture of IOS-XE-shaped values (stratum 2, reach 255, offset
-0.32 ms, delay 1.25 ms, jitter 0.08 ms, poll 64 s, `SYSTEM_PEER`,
instance `Mgmt-vrf`).

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/protocol/ntp/v1 generated/go/proto/flowseer/net/protocol/ntp/v1 spec/proto/flowseer/net/protocol/README.md spec/proto/flowseer/net/addr/v1/README.md spec/proto/flowseer/net/key/v1/README.md test/conformance/proto/ntp_rules_test.go`

### U3. Platform fields on `ComponentState` and `DeviceState`

Files: `spec/proto/flowseer/model/inventory/v1/{component.proto,device.proto,README.md}`,
`generated/go/proto/flowseer/model/inventory/v1/`,
`spec/proto/flowseer/model/README.md`,
`spec/proto/flowseer/net/measure/v1/README.md`,
`spec/proto/flowseer/net/system/v1/README.md`,
`test/conformance/proto/model_inventory_platform_rules_test.go` (new)
After: U1 (imports `net/system`; edits the two READMEs U1 writes)

Change:

- `component.proto` imports `net/measure/v1/sensor.proto` and the two
  `net/system` utilization files. `ComponentOperStatus` is declared beside
  `ComponentKind` with the values in Decisions.
- `ComponentState` gains `oper_status = 14` (`defined_only`, `not_in [0]`;
  unset means unreported), `sensors = 15` (repeated `SensorReading`),
  `processor_utilization = 22`, and `storage_utilization = 23`. New
  message CEL rules: `component_state.one_reading_per_quantity` (for each
  of the six arms, `this.sensors.filter(s, has(s.<arm>)).size() <= 1`),
  `component_state.processor_utilization_only_cpu` (`kind == 11`), and
  `component_state.storage_utilization_only_storage` (`kind == 12`), and
  `component_state.storage_unnamed`
  (`!has(this.storage_utilization) || !has(this.storage_utilization.name)`),
  because the component names itself. The
  `sensors` comment states the child-component rule for a second
  measurement point.
- `DeviceState` gains `system_contact = 11`, `system_location = 12`
  (each `min_len 1`, `max_len 255`), `uptime = 13` (`Duration`,
  `gte 0s`), `processor_utilization = 14`, `storage_utilization = 15`
  (repeated). New CEL rules: `device_state.storage_named` (every row has a
  `name`) and `device_state.storage_names_unique`
  (`this.storage_utilization.map(s, s.name).unique()`).
- The uptime comment is contract-only: what it measures, relative to the
  envelope's observation time, that `sysUpTime` wraps at about 497 days
  and can reset on an agent restart, and that a reboot is not inferred
  from it.
- `model/inventory/v1/README.md`: `Imports:` gains `net/measure` and
  `net/system`; a "Platform health" section gives the oper-status mapping,
  the sensor child-component rule, and the component-or-device
  utilization rule. The Devices paragraph (`README.md:113-117`) lists the
  new `DeviceState` fields, and the sentence saying which sensor readings
  to carry "is not decided yet" (`:257-259`) is replaced by the rule this
  unit lands. `model/README.md`: `Imports:` gains `net/measure` and
  `net/system`. `net/measure/v1/README.md` and
  `net/system/v1/README.md`: `Imported by:` gains `model/inventory`.

Tests: `model_inventory_platform_rules_test.go` holds Requirements 1 to 3:
`TestComponentSensorRules`, `TestComponentOperStatusAndUtilizationRules`,
`TestDeviceSystemIdentityRules`, `TestDeviceUtilizationRules`;
the second includes a named `storage_utilization` on a storage component
failing `component_state.storage_unnamed`. The
existing `model_inventory_*_rules_test.go` files pass unchanged, which
proves no landed fixture needed a new field.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/model/inventory/v1 generated/go/proto/flowseer/model/inventory/v1 spec/proto/flowseer/model/README.md spec/proto/flowseer/net/measure/v1/README.md spec/proto/flowseer/net/system/v1/README.md test/conformance/proto/model_inventory_platform_rules_test.go`

### U4. The syslog record in `event/log`

Files: `spec/proto/flowseer/event/log/v1/{syslog_record.proto,README.md}`,
`generated/go/proto/flowseer/event/log/v1/`,
`spec/proto/flowseer/event/README.md`,
`spec/proto/flowseer/net/log/v1/README.md`,
`spec/proto/flowseer/net/addr/v1/README.md`,
`spec/proto/flowseer/net/README.md`,
`spec/proto/flowseer/model/README.md`,
`spec/proto/flowseer/model/inventory/v1/README.md`,
`CONCEPTS.md`,
`test/conformance/proto/event_log_rules_test.go` (new)
After: U1 (imports `net/log`), U2 (`net/addr` README), U3 (the two model
READMEs)

Change:

- `syslog_record.proto` declares `SyslogRecord`: `device = 1`
  (`DeviceGlobalRef`, required), `record_id = 2` (required, `uuid`),
  `received_at = 3` (required), `sent_at = 4`, `severity = 5` and
  `facility = 6` (each required, `defined_only`), `hostname = 7` (1 to
  255), `app_name = 8` (1 to 48), `proc_id = 9` (1 to 128), `msg_id = 10`
  (1 to 32), `structured_data = 11` (repeated), `message = 12` (`bytes`,
  `max_len 65527`), `source_address = 13` (`IpAddress`),
  `message_truncated = 14` (`bool`; true when the collector cut a longer
  MSG to the bound). CEL
  `syslog_record.sd_ids_unique`:
  `this.structured_data.map(e, e.id).unique()`.
  `SyslogStructuredDataElement`: `id = 1` (required, 1 to 32),
  `params = 2` (repeated). `SyslogStructuredDataParam`: `name = 1`
  (required, 1 to 32), `value = 2` (required, may be empty: PARAM-VALUE is a UTF-8 string, RFC 5424
  §6.3.3; max 1024, record rule 3).
  The file comment says the package is a stream of records with no Config,
  State, or entity transition.
- `event/log/v1/README.md`: identity, `Imports: model/inventory, net/addr,
  net/log`, `Imported by: nothing`, the RFC 5424 bounds, why `message` is
  bytes, and what a sender without the RFC 5424 header leaves unset.
- `event/README.md`: `Imports:` gains `net/addr` and `net/log`; Admission
  names `log/v1` as the second passing package; Packages gains `log/v1/`.
  `net/log/v1/README.md`, `net/addr/v1/README.md`, `net/README.md`,
  `model/README.md`, and `model/inventory/v1/README.md`: `Imported by:`
  gains `event/log`.
- `CONCEPTS.md`, Inventory: "Syslog record", one received log line tied
  to its device, never diffed and never an alarm.

Tests: `event_log_rules_test.go` holds Requirements 4 and 5:
`TestSyslogRecordPresence` (the `EMERGENCY` and `KERN` zero cases and
`HasSeverity`), `TestSyslogRecordRules`. `net/log/v1` and `event/log/v1`
both generate Go package `logv1`, so the test imports them as
`netlogv1` and `eventlogv1`.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/event/log/v1 generated/go/proto/flowseer/event/log/v1 spec/proto/flowseer/event/README.md spec/proto/flowseer/net/log/v1/README.md spec/proto/flowseer/net/addr/v1/README.md spec/proto/flowseer/net/README.md spec/proto/flowseer/model/README.md spec/proto/flowseer/model/inventory/v1/README.md CONCEPTS.md test/conformance/proto/event_log_rules_test.go`

### U5. The Alarm entity in `model/alarm`

Files: `spec/proto/flowseer/model/alarm/v1/{alarm.proto,README.md}`,
`generated/go/proto/flowseer/model/alarm/v1/`,
`spec/proto/flowseer/model/README.md`,
`spec/proto/flowseer/model/inventory/v1/README.md`,
`spec/proto/flowseer/net/key/v1/README.md`,
`spec/proto/flowseer/net/README.md`,
`test/conformance/proto/layering_test.go`,
`CONCEPTS.md`,
`test/conformance/proto/model_alarm_rules_test.go` (new)
After: U2 (`net/key` README), U3 and U4 (the model READMEs, `net/README.md`,
`CONCEPTS.md`)

Change:

- `alarm.proto`, one family file: `AlarmSeverity`; `WholeDevice` (empty);
  `AlarmResource` (required `target` oneof, arms 1 to 4 as in Decisions,
  with the both-arms comment from the solution doc); `AlarmLocalRef`
  (`resource = 1` required, `type_id = 2` required 1 to 1024,
  `type_qualifier = 3` 1 to 1024); `AlarmGlobalRef` (`device = 1`,
  `alarm = 2`, both required); `AlarmState` (`ref = 1` required,
  `severity = 2` `defined_only` `not_in [0]`, `cleared = 3` required,
  `text = 4` 1 to 1024, `created_at = 5`, `last_raised_at = 6`,
  `last_changed_at = 7`); `AlarmEvent` (`ref = 1` required, `before = 2`,
  `after = 3`, CEL `alarm_event.before_matches_ref`
  (`!has(this.before) || !has(this.ref) || !has(this.before.ref) ||
  this.before.ref == this.ref`, tolerating absent fields as
  `component.proto` does), the same for `after`, and
  `alarm_event.one_side`). The file-level comment names
  `AlarmConfig` as deliberately absent and why, and says the alarm is
  device-owned and outside `EntityType` like `Component`.
- `model/alarm/v1/README.md`: identity, `Imports: model/inventory,
  net/key`, `Imported by: nothing`, the key and the arm-resolution
  stability rule, severity mapping by name from RFC 8632, ITU, and
  openconfig, and the clear-versus-vanish rule.
- `layering_test.go:130`: the `model/alarm` row becomes
  `{"model/inventory", "net/key"}`.
- `model/README.md`: Packages gains `alarm/v1/`.
  `model/inventory/v1/README.md` and `net/key/v1/README.md`: `Imported by:`
  gains `model/alarm`. `net/README.md`: `Imported by:` gains `model/alarm`.
- `CONCEPTS.md`, Inventory: "Alarm", a named, clearable condition a device
  raises on one of its resources, keyed by resource and type; distinct
  from a syslog record.

Tests: `model_alarm_rules_test.go` holds Requirements 6 and 7:
`TestAlarmStateRules` (including the empty-resource case and one case per
arm, and an `interface_name` of `""` failing the key rule),
`TestAlarmEventRules` (the `type_qualifier` mismatch, one side, a raise,
and a clear). The proto hook's family check runs on the edit and reports
nothing, because the file comment names `AlarmConfig`.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/model/alarm/v1 generated/go/proto/flowseer/model/alarm/v1 spec/proto/flowseer/model/README.md spec/proto/flowseer/model/inventory/v1/README.md spec/proto/flowseer/net/key/v1/README.md spec/proto/flowseer/net/README.md test/conformance/proto/layering_test.go CONCEPTS.md test/conformance/proto/model_alarm_rules_test.go`

Waves: U1 | U2 U3 | U4 | U5

U2 waits for U1 only because both add to `net/key/v1/README.md`'s
`Imported by:`; U3 needs U1's packages. The last two are a chain because
each edits a README boundary line or `CONCEPTS.md` the one before it
edits. Every README edit here adds a name to a line, so when phase 4 lands
first and adds its own names to the same lines, the merge keeps both.

## Verification

```bash
buf lint
buf generate && git status --porcelain generated/   # empty after the commit
go build ./... && go vet ./...
go test -race ./test/conformance/proto/...
.claude/skills/verify-change/scripts/verify-change.sh -- <union of the five units' Verify paths>
```

No `--full` run: it builds and race-tests `generated/go/yang` and
exhausts host memory. The targeted run over the union of changed paths is
the phase gate.

## Definition of done

- [x] Verifier green for every changed path, with the targeted union run
      above.
- [x] Every new package has a README whose `Imports:` and `Imported by:`
      lines pass `TestProtoReadmeImports`; `net/README.md`,
      `net/protocol/README.md`, `model/README.md`, and `event/README.md`
      list their packages without a "planned" marker.
- [x] `docs/conventions/protobuf.md` names the syslog registries;
      `CONCEPTS.md` has Alarm and Syslog record.
- [x] No plan label (R1, U2) in code, comments, or commit messages.
- [x] This plan's `status` is `implemented` with an outcome note under the
      title, and the parent's phase 5 `Landed:` line carries the commit
      range; the parent's `net/system` open question is answered by the
      Decision above.

## Open questions

- Whether to amend the record's summary sentence to "every forwarding
  table", so software images and licenses drop `network_instance`.
  Options: amend the sentence in `docs/architecture/2026-09-25-schema-building-blocks-direction.md`
  and drop the two fields (recommended: rule 4's stated reason is
  forwarding tables, and a required `default` on an image slot tells a
  consumer nothing); or keep the literal rule as this plan does. The plan
  follows the record as written, so this blocks no unit; amending it is
  the user's call and costs one field on two rows later.
- Whether `DeviceState.uptime` stays readable once a store keeps
  `DeviceState` without its envelope. The value is relative to the
  observation time, which rides the envelope and never the State
  (conventions doc, Provenance). No store holds `DeviceState` yet; the
  store that first does keeps the observation time beside it, or a later
  plan replaces `uptime` with a boot time and accepts its wrap and reset
  faults.
- Whether protovalidate's CEL `==` on two `AlarmGlobalRef` messages
  compares them as protobuf messages. The plan relies on it and the
  `type_qualifier` test pins it; if the test shows it does not, the
  implementer writes the comparison field by field, as `ComponentEvent`
  does, including the resource arm, and records that here.
  Resolved during implementation: protovalidate's CEL `==` on two
  `AlarmGlobalRef` messages compares them as protobuf messages and correctly
  flags field mismatches (verified in `TestAlarmEventRules` with `type_qualifier`
  mismatch).

- Review (accept) confirmed the CEL `==` on `AlarmGlobalRef` does deep message
  equality (the `type_qualifier` mismatch test fires), so the open note is
  resolved; no field-by-field fallback needed. Non-blocking residual test gaps
  for a follow-up: `alarm_event.before/after_matches_ref` is pinned only by a
  scalar `type_qualifier` mismatch — a resource-arm swap and a device-id
  mismatch ride the same proven mechanism but are not individually exercised;
  `component_state.one_reading_per_quantity` runs only the duplicate-voltage
  case. Compound: no solution (the real-zero enum and seam-safety patterns are
  already established; the CEL deep-equality is a cel-go behavior, not a
  FlowSeer-specific lesson).
