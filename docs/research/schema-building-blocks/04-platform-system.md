---
title: Domain dossier — platform, hardware, and system building blocks
date: 2026-09-25
domain: b04 (platform/hardware/system)
---

# Platform, hardware, and system — schema dossier

## 1. Scope and sources

Covers: hardware component tree, sensors (temperature/voltage/current/power/
RPM/humidity), power supplies, fans, CPU/memory/storage utilization, firmware
images, licenses, system identity, time sync, config/backup state, alarms,
syslog severity/facility.

**Repo paths read:**
- `docs/conventions/protobuf.md` (full)
- `docs/architecture/2026-08-20-network-model-structure-direction.md:1-360`
  (facets vs tables, `net/` package tree, hardware-ports-are-later note at
  line 345)
- `spec/proto/flowseer/model/inventory/v1/component.proto` (full)
- `spec/proto/flowseer/model/inventory/v1/device.proto` (full)
- `spec/proto/flowseer/net/phy/v1/module_temperature.proto`,
  `supply_voltage.proto`, `bias_current.proto`, `optical_power.proto` (full,
  the four-threshold repeated shape)
- `docs/research/network-domain-atlas/entities/01-platform.md` (full — 10
  entities: system-identity, hw-component, transceiver, stacking,
  power-supply, environment, firmware-image, config-file, license,
  cpu-memory)
- `docs/research/network-domain-atlas/entities/09-ops.md` (full — snmp-agent,
  syslog-events, rmon, flow-export, time-sync, netconf-telemetry,
  scheduling-automation, openflow-sdn, host-resources)
- `spec/mib/ietf/ENTITY-SENSOR-MIB:75-260` (EntitySensorDataType/Scale/
  Precision/Value/Status)
- `spec/mib/ietf/HOST-RESOURCES-MIB:253-596` (hrStorageType, hrStorageSize/
  Used/AllocationUnits, hrDeviceStatus, hrProcessorLoad)
- `spec/mib/ietf/ENTITY-MIB`, `IANA-ENTITY-MIB`, `ENTITY-STATE-MIB`,
  `ALARM-MIB` (present, confirmed vendored, not fully dumped — RFC text used
  for exact clause numbers, cross-checked against local file structure)
- `spec/yang/ruckus/icx/9.0.00/openconfig-platform.yang:440-620` (temperature/
  power/memory groupings, chassis/port/power-supply/fan anchors)
- `spec/yang/ruckus/icx/9.0.00/openconfig-system.yang:440-540` (NTP server
  config/state, stratum semantics table)
- `spec/yang/ruckus/icx/9.0.00/openconfig-alarm-types.yang:92-145` (severity
  identities)
- `spec/yang/aruba/cx/aoscx-yang/10.17/openconfig/v5_0_0/platform/*.yang`,
  `spec/yang/cisco/iosxe/2611/openconfig-platform*.yang` (confirmed vendored
  copies of the same OpenConfig platform family, not separately diffed)
- **No `ietf-hardware` (RFC 8348), `ietf-system` (RFC 7317), or
  `ietf-alarms` (RFC 8632) YANG modules are vendored anywhere under
  `spec/yang/`.** Only OpenConfig platform/system/alarm modules are present.
  This is a real gap against the IETF-first sources this dossier set out to use —
  flagged as an open question.
- `spec/openapi/ubiquiti/unifi-network-openapi-v10.4.57.json` (schema:
  "Latest statistics for a device", "Adopted device overview")
- `spec/openapi/mikrotik/routeros-7.24-openapi.json` (path listing under
  `/system/resource/*`; response schema is a generic `RouterOSItem`, no typed
  field list — see traps)
- `spec/openapi/lancom/lmc-openapi/devices.json` (schemas `DeviceStatistics`,
  `DeviceStatusData`), `spec/openapi/lancom/lmc-openapi/monitoring.json`
  (schema name grep only: `cpuLoadPercent`, `temperature`,
  `LcosLxPowerStatusEnum`)
- `spec/openapi/ruckus/vsz/vsz-7.1.1-v13_1-openapi.json` (definition-name grep
  for Cpu/Memory/sensor/fan/power — none found; this spec is AP/switch
  *configuration* management, not telemetry)

**Web fetched for this dossier:**
- RFC 6933 (ENTITY-MIB) — https://www.rfc-editor.org/rfc/rfc6933 — §3.1
  (entPhysicalTable), §2.16.1 (IANAPhysicalClass supersedes PhysicalClass),
  §2.12.1 (entPhysicalAlias/AssetID)
- RFC 3433 (ENTITY-SENSOR-MIB) — https://www.rfc-editor.org/rfc/rfc3433 — §4
  (also cross-checked against the vendored MIB text directly, see above)
- RFC 2790 (HOST-RESOURCES-MIB) — https://www.rfc-editor.org/rfc/rfc2790 —
  §4.3, §4.4 (also cross-checked against vendored MIB text)
- RFC 8348 (ietf-hardware) — https://www.rfc-editor.org/rfc/rfc8348 — §3, §7.1,
  §7.2 (iana-hardware derived identities, sensor-data grouping, config/state
  split) — **not vendored, web-only**
- RFC 7317 (ietf-system) — https://www.rfc-editor.org/rfc/rfc7317 — §3.1–3.5
  (system identity, clock, ntp, dns-resolver, radius, users) — **not
  vendored, web-only**
- RFC 8632 (ietf-alarms) — https://www.rfc-editor.org/rfc/rfc8632 — §6
  (severity typedef), §4.8 (notifications) — **not vendored, web-only**
- RFC 5424 (syslog) — https://www.rfc-editor.org/rfc/rfc5424 — §6.2.1
  (severity and facility numeric tables)
- `raw.githubusercontent.com/openconfig/public/master/.../openconfig-platform-types.yang`
  — OPENCONFIG_HARDWARE_COMPONENT derived identities (cross-checked against
  the vendored copy's file existence, not line-diffed)
- `raw.githubusercontent.com/openconfig/public/master/.../openconfig-platform.yang`
  — temperature/power groupings (superseded by the vendored-file read above,
  which is authoritative)
- `raw.githubusercontent.com/openconfig/public/master/.../openconfig-platform-cpu.yang`
  — CPU utilization uses `oc-types:avg-min-max-instant-stats-pct`, no
  memory-utilization leaf in this module
- Cisco Meraki Dashboard API docs (developer.cisco.com/meraki/api-v1) — Get
  Device has no cpu/mem/temp fields; `getOrganizationDevicesSystemMemoryUsageHistoryByInterval`
  and `getOrganizationWirelessControllerDevicesSystemUtilizationHistoryByInterval`
  exist as separate history endpoints (found via search, not fetched in full
  — mark unverified for exact field names)
- Juniper Mist, HPE Aruba Central — web search only, no field-level schema
  retrieved; **mark unverified**, existence of cpu/mem/temp telemetry
  confirmed only qualitatively
- TP-Link Omada Open API, RUCKUS One API — web search only; Omada device
  status fields (`CPUUtil`, `MemUtil`, `Type`, `Uptime`) reported by
  third-party Go client docs, **not the primary OpenAPI spec — mark
  unverified**

## 2. Standards facts

**RFC 6933 (ENTITY-MIB), `entPhysicalClass`** (§3.1, `spec/mib/hp/procurve/HP-ENTITY-MIB`
and `spec/mib/ietf/ENTITY-MIB` vendored, `IANA-ENTITY-MIB` vendored for the
current registry): `other(1)`, `unknown(2)`, `chassis(3)`, `backplane(4)`,
`container(5)`, `powerSupply(6)`, `fan(7)`, `sensor(8)`, `module(9)`,
`port(10)`, `stack(11)`, `cpu(12)`. RFC 6933 deprecates the inline
`PhysicalClass` TC in favor of `IANAPhysicalClass` from `IANA-ENTITY-MIB`,
which is the open, IANA-maintained registry — the atlas doc confirms only 12
class values exist and are "regularly abused" (a transceiver reported as
`module(9)`, `port(10)`, or `other(1)` depending on vendor;
`docs/research/network-domain-atlas/entities/01-platform.md:145-146`).

**`entPhysicalContainedIn`** (§3.1): parent index, 0 = root of tree.
**`entPhysicalParentRelPos`** (§3.1): `Integer32`, sibling ordering, -1 =
indeterminate. FlowSeer's `ComponentState.position` (`uint32`,
`component.proto:133-136`) already narrows this to "no ordering" via absence
rather than a sentinel -1, which is the right explicit-presence translation.

**RFC 3433 / vendored `ENTITY-SENSOR-MIB:75-260`:**
- `EntitySensorDataType` (12 values, exact text quoted from the vendored
  file): `other(1)`, `unknown(2)`, `voltsAC(3)`, `voltsDC(4)`, `amperes(5)`,
  `watts(6)`, `hertz(7)`, `celsius(8)`, `percentRH(9)`, `rpm(10)`, `cmm(11)`
  (cubic meters/minute, airflow), `truthvalue(12)`.
- `EntitySensorDataScale`: SI-prefix scale, `yocto(1)` (10⁻²⁴) through
  `yotta(17)` (10²⁴), `units(9)` = 10⁰.
- `EntitySensorPrecision`: `Integer32(-8..9)`. 1–9 = decimal places in the
  fixed-point value; -8..-1 = accurate digits; 0 = not fixed-point. Worked
  example in the MIB: 0.1 °C steps over 0–100 °C → precision `1`, scale
  `units(9)`, value range 0–1000 (value = degrees C × 10).
- `EntitySensorValue`: `Integer32(-1000000000..1000000000)`.
- `EntitySensorStatus` (oper status): `ok(1)`, `unavailable(2)`,
  `nonoperational(3)`.
- `entPhySensorValueTimeStamp`: sysUpTime at last read. `entPhySensorValueUpdateRate`:
  milliseconds between agent updates, 0 = on-demand/unknown.

This is the standards-correct generic sensor shape and the atlas doc calls it
out explicitly as "the right model" (`01-platform.md:335-337`), but notes it
is under-implemented — most vendors ship a private per-class table instead
(fan table, temp table, voltage table, each with its own scale baked in).

**RFC 2790 / vendored `HOST-RESOURCES-MIB:253-596`:**
- `hrStorageType`: registry OIDs under `hrStorageTypes`
  (`HOST-RESOURCES-TYPES`, vendored), not a small closed INTEGER enum —
  `hrStorageRam`, `hrStorageFixedDisk`, `hrStorageFlashMemory`, etc. are OID
  arcs, so this is a registry pass-through, not a normalized enum.
- `hrStorageSize` / `hrStorageUsed`: `Integer32`, units = `hrStorageAllocationUnits`
  bytes-per-unit (a separate multiplier field, not a fixed unit) —
  **traps**: a raw copy of Size/Used without the allocation-units multiplier
  is meaningless.
- `hrProcessorLoad` (line 587–596): `Integer32(0..100)`, "the average, over
  the last minute, of the percentage of time that this processor was not
  idle." A single-minute exponential/approximate average, not instantaneous.
- `hrDeviceStatus`: `unknown(1)`, `running(2)`, `warning(3)`, `testing(4)`,
  `down(5)`.

**RFC 8348 (`ietf-hardware`, web-only — not vendored):** `iana-hardware`
identity base `hardware-class` with derived identities `chassis`,
`backplane`, `container`, `power-supply`, `fan`, `sensor`, `module`, `port`,
`stack`, `cpu`, plus two ENTITY-MIB does not have: `battery`,
`storage-drive`. Sensor-data leafs: `value` (`int32`, -1000000000..1000000000,
same range as ENTITY-SENSOR-MIB), `value-precision` (`int8`, -8..9),
`value-type` (same 10 types as ENTITY-SENSOR-MIB plus the naming is
kebab-case: `volts-AC`, `volts-DC`, etc.), `oper-status`, `value-timestamp`.
Explicit `config true`/`config false` split in the module: `name`, `class`,
`parent`, `parent-rel-pos`, `alias`, `asset-id`, `uri`, `admin-state` are
config; everything else (serial, mfg info, sensor-data, oper-state,
alarm-state, standby-state) is state-only. This maps directly onto
FlowSeer's "deliberately no `ComponentConfig`" decision
(`component.proto:6-10`) — the only config-side leaf ietf-hardware defines
(`admin-state`) has no landed FlowSeer equivalent and is out of scope unless
someone wants to model admin shutdown of a component.

**RFC 8632 (`ietf-alarms`, web-only) / vendored `openconfig-alarm-types.yang:92-145`:**
Severity identities based on X.733 (both sources agree; OpenConfig's
`OPENCONFIG_ALARM_SEVERITY` derives `UNKNOWN`(unclear→indeterminate),
`MINOR`, `WARNING`, `MAJOR`, `CRITICAL` — five levels, `WARNING` is
explicitly "detection of a potential ... fault, before any significant
effects" i.e. below `MINOR` in urgency despite the alphabetic/enum-number
ordering looking otherwise). `is-cleared` is a boolean, separate from
severity — "cleared" is not a severity value, it is orthogonal state.

**RFC 5424 §6.2.1 — syslog:** Severity 0–7 (`Emergency` … `Debug`), Facility
0–23 with `local0`–`local7` at 16–23. Both are small closed registries safe
to normalize (`_UNSPECIFIED = 0` would collide with `Emergency = 0` and
`kernel = 0`, both real values — **these must be registry pass-through
enums**, not normalized ones, exactly like `IpDscp`/`IpEcn`/`IpProtocol` in
`net/packet/v1` per `docs/conventions/protobuf.md:225-236`).

**Vendored `openconfig-platform.yang:454-517` (Ruckus ICX copy):**
`platform-component-power-state`: `allocated-power` (`uint32`, watts),
`used-power` (`uint32`, watts) — no capacity/rated-max leaf in this grouping.
`platform-component-temp-state`: `temperature` container using
`avg-min-max-instant-stats-precision1-celsius` (a shared stats grouping, one
decimal place fixed) plus an embedded `alarm-status`/`alarm-threshold`
(`uint32`)/`alarm-severity` (identityref to `OPENCONFIG_ALARM_SEVERITY`) —
i.e. OpenConfig ties one threshold-plus-severity pair directly to
temperature, not the four-threshold (low-alarm/low-warn/high-warn/high-alarm)
shape FlowSeer already uses for module diagnostics. `platform-component-memory-state`:
`available` / `utilized` (`uint64`, bytes) — a two-field absolute-bytes shape,
not a percentage.

**Vendored `openconfig-system.yang:452-538`:** NTP server config —
`address`, `port` (default 123), `version` (1–4, default 4),
`association-type` (`SERVER`/`PEER`/`POOL`), `iburst`, `prefer` (all
booleans/enums with defaults, i.e. optional-with-default rather than
required). NTP server state — `stratum` (`uint8`) with an explicit semantic
table: `0` = unspecified/invalid, `1` = primary (e.g. GPS), `2–15` =
secondary, `16` = unsynchronized, `17–255` = reserved. This stratum table is
exactly the kind of "registry with a real zero" that needs pass-through
enum treatment or at minimum a documented numeric range, not a boolean
"synced" flag — `16` (unsynchronized) is a real, common, alarm-worthy value.

## 3. Provider data matrix

| Concept | IETF/OpenConfig | Ubiquiti UniFi (vendored) | MikroTik RouterOS (vendored) | LANCOM LMC (vendored) | RUCKUS SmartZone (vendored) | Meraki/Mist/Aruba Central (web, unverified) |
|---|---|---|---|---|---|---|
| Model/serial/firmware | `entPhysicalModelName/SerialNum/FirmwareRev` (RFC 6933) | "Adopted device overview": `model`, `firmwareVersion`, `firmwareUpdatable` (no serial in this schema; MAC is the id-adjacent field) | `/system/resource/hardware` (path exists; response is generic `RouterOSItem`, fields not typed in the spec) | `DeviceFirmwareInfo`, `VendorHardwareInfo` schemas present (not field-dumped) | Not in this config-plane spec — config only | Meraki Get Device: `model`, `serial`, `firmware` (device object; **core**) |
| CPU utilization | `openconfig-platform-cpu` avg/min/max/instant, percent | "Latest statistics for a device": `cpuUtilizationPct` (double) — **core, present** | `/system/resource/cpu/*` paths exist, no typed schema in spec | `cpuLoadPercent` (schema-name grep only, `monitoring.json`) — **core, present but undocumented shape** | absent from this spec | Meraki: separate history endpoint only, not on Get Device (**niche/derived**); Mist/Aruba Central: qualitatively confirmed, fields unverified |
| Memory utilization | `openconfig-system` memory (bytes, not %); `openconfig-platform` per-component `available`/`utilized` bytes | `memoryUtilizationPct` (double, **percent** — unit mismatch vs OpenConfig's bytes) | `/system/resource` path only | not found by name grep | absent | Meraki: separate `...SystemMemoryUsageHistoryByInterval` endpoint, used/free bytes with min/max/median (**niche as live state, present as history**) |
| Temperature | ENTITY-SENSOR-MIB `celsius(8)`; OpenConfig avg/min/max/instant, 1-decimal precision | `temperature` (schema-name grep, `monitoring.json`) — present | not found | `temperature` (schema-name grep) — present | absent | Mist: qualitatively confirmed (ambient + internal CPU temp) via docs, fields unverified |
| Fan / RPM | ENTITY-SENSOR-MIB `rpm(10)`; `openconfig-platform-fan` speed | not found | not found | not found | absent | not verified |
| PSU power/voltage/current | no IETF MIB; `openconfig-platform-psu` capacity/input/output V,A,W | not found | not found | `LcosLxPowerStatusEnum`, `LcosLxPowerFailoverStatusEnum`, `PowerConsumptionReportTypeEnum` (present, PoE-adjacent naming, not confirmed as raw PSU telemetry) | absent | not verified |
| Uptime | `sysUpTime` (RFC 3418, wraps ~497 days); `ietf-system` `boot-time`/`current-datetime` | `uptimeSec` (int64) — **core, present, absolute seconds (no wrap)** | path exists, schema untyped | not found | absent | Meraki Get Device: no uptime field found |
| Reachability/online state | none standard | `state` enum: `ONLINE`, `OFFLINE`, `PENDING_ADOPTION`, `UPDATING`, `GETTING_READY`, `ADOPTING`, `DELETING`, `CONNECTION_INTERRUPTED`, `ISOLATED`, `U5G_INCORRECT_TOPOLOGY` — **10-value vendor enum, no IETF/OpenConfig equivalent** | not found | `heartbeatState` (`HeartbeatStateStatistic` schema, referenced not dumped) | not found | not verified |
| License/entitlement | `openconfig-license`; `WLSX-SWITCH-MIB` (Aruba) | not checked | not checked | `licenseState` (`LicenseStateStatistic`), `warrantyState` | present per atlas (config-plane licensing is core to controllers) | not verified |
| Config/backup state | no IETF MIB; vendor job-model MIBs (HH3C, Cisco SMB `CISCOSB-COPY-MIB`) | not checked | `/system/backup/*` paths present (save/load/cloud) — job-shaped, matches atlas finding | `configState` (`ConfigStateStatistic`) | present (config management is this API's whole purpose) | not verified |
| Alarms | `ALARM-MIB` model/active split; `ietf-alarms`; `openconfig-alarms` | `alertState` (`AlertStateStatistic`) — present, shape not dumped | not found | `alertState` equivalent present via `DeviceAlertingSimple` schema | not checked | not verified |
| NTP / time sync | `NTPv4-MIB` (RFC 5907, not confirmed vendored — not checked for this dossier); `ietf-system`/`openconfig-system` ntp container, stratum table | not checked | `/system/clock/*` (manual clock, no NTP association table seen in path list) | not checked | not checked | not verified |
| Syslog severity/facility | RFC 5424, universal | not checked | not checked | `logging.json` file present, not opened | not checked | not verified |

**Core across nearly every provider that exposes any telemetry at all:**
model, serial (or MAC as a proxy), firmware version, CPU%, some notion of
online/reachable state, uptime. **Niche or config-plane-only in this
corpus:** raw PSU voltage/current, fan RPM, humidity, per-lane/per-lane-group
sensor detail — RUCKUS SmartZone's vendored spec is exclusively
configuration management and carries none of this; LANCOM LMC is a cloud
fleet-management layer whose "monitoring" schemas lean toward
license/warranty/heartbeat *state machine* concepts rather than raw sensor
values.

## 4. Proposed primitives

All new messages are Primitives (`net/`, ref-free, no triad) unless noted as
Entities. Field numbering follows the direction doc: a bare-value message
numbers arms/fields from 1; a message with a `oneof` beside other fields
starts the `oneof` at 10.

### 4.1 `flowseer.net.env.v1` — new package, generic environmental sensor

Replaces the four near-duplicate messages in `net/phy/v1`
(`ModuleTemperature`, `SupplyVoltage`, `BiasCurrent`, `OpticalPower`), which
all repeat: one fixed-unit `int32`/`uint32` value plus four optional
ordered thresholds plus one CEL rule. Rather than a single message with a
runtime unit+scale (the ENTITY-SENSOR-MIB shape), which conflicts with the
convention that units live in field names/comments and validation must be a
plain field rule where possible (`protobuf.md:225-236`, `238-283`), use a
**typed variant**: the physical quantity is a closed set of arms with
different fixed canonical units and different sane ranges, so the same shape
the four existing messages hand-rolled becomes one reusable pattern applied
per arm.

```protobuf
// A measured physical quantity and its device-reported alarm thresholds.
// Exactly one quantity kind is set; each kind fixes its own canonical unit
// so no scale/precision runtime lookup is needed. Each threshold is absent
// when the source does not report it; present thresholds must be ordered
// low_alarm <= low_warning <= high_warning <= high_alarm (same CEL shape as
// today's ModuleTemperature/SupplyVoltage/BiasCurrent/OpticalPower).
message SensorReading {
  oneof quantity {
    option (buf.validate.oneof).required = true;

    Temperature temperature = 1;      // millidegrees Celsius, int32
    Voltage voltage = 2;               // microvolts, uint32 (DC) — AC vs DC
                                        // is a separate concern, see traps
    Current current = 3;               // microamperes, uint32
    Power power = 4;                   // nanowatts, uint32 (0 is real: no
                                        // draw, not "absent")
    RotationSpeed rotation_speed = 5;  // millirpm, uint32 (fan/blower)
    RelativeHumidity relative_humidity = 6; // millipercent RH, uint32
  }
}
```

Each arm (`Temperature`, `Voltage`, ...) is exactly today's
`ModuleTemperature` shape (value + 4 thresholds + ordering CEL) renamed and
moved here; `net/phy/v1` then holds only `SensorReading` fields where it
used to hold four bespoke messages, and `ModuleTemperature` /
`SupplyVoltage` / `BiasCurrent` / `OpticalPower` become deleted duplicates
(breaking change, consistent with the repo's pre-stability stance —
`CLAUDE.md` "Breaking changes welcome"). `Voltage`/`Current`/`Power` keep
their existing field names and units unchanged; only the message identity
and package move.

A device-scoped **table** of these hangs off `ComponentState` (a component
of `COMPONENT_KIND_SENSOR`, `COMPONENT_KIND_FAN`, or
`COMPONENT_KIND_POWER_SUPPLY` carries one or more readings — a PSU reports
voltage, current, *and* power simultaneously, so it is `repeated
SensorReading`, not a single field), matching the "table" pattern (repeated
rows, not a map, hung off the owning entity's State —
direction-doc:207-228):

```protobuf
// on ComponentState, per direction-doc facets-vs-tables: device/component-
// scoped rows, not a map.
repeated flowseer.net.env.v1.SensorReading sensors = 20;
```

### 4.2 `flowseer.model.inventory.v1.ComponentState` additions

- `oper_status` — new enum `ComponentOperStatus` (normalized:
  `_UNSPECIFIED`, `_UP`, `_DOWN`, `_TESTING`, `_UNKNOWN`), mirroring
  `hrDeviceStatus`/ENTITY-STATE-MIB `entStateOper` rather than
  ENTITY-SENSOR-MIB's narrower ok/unavailable/nonoperational — a fan/PSU
  needs "testing" too. This is new work; `ENTITY-STATE-MIB` is vendored
  (`spec/mib/ietf/ENTITY-STATE-MIB`) but not dumped for this dossier — planner
  should confirm exact value set before finalizing.
- `sensors` — see 4.1.
- Power/utilization stay off `ComponentState` directly as scalar fields the
  way OpenConfig does (`allocated_power_watts`, `used_power_watts` on a
  power-supply-kind component; `available_bytes`/`utilized_bytes` on a
  storage/cpu-kind component) rather than folding them into `SensorReading`,
  because they are not alarm-threshold quantities — no vendor in the corpus
  reports thresholds on power or memory the way it does on temperature/
  voltage/current. Keep them typed and separate:

```protobuf
// Set only when kind == COMPONENT_KIND_POWER_SUPPLY.
uint32 allocated_power_watts = 21;
uint32 used_power_watts = 22;
// Set only when kind == COMPONENT_KIND_CPU or COMPONENT_KIND_STORAGE.
uint64 available_bytes = 23;
uint64 utilized_bytes = 24;
// hrProcessorLoad-style: percent, last-minute average. Absent = not
// reported. 0-10000 basis points (hundredths of a percent) to avoid an
// integer/float split across sources that report finer than whole percent.
uint32 utilization_basis_points = 25 [(buf.validate.field).uint32.lte = 10000];
```

(Field numbers illustrative; planner assigns per the 10/20-block rule
already used at `module = 20` in the existing file.)

### 4.3 `flowseer.model.inventory.v1.DeviceState` additions — system identity

`DeviceState` already carries `hostname`, `vendor`, `model`,
`hardware_revision`, `software_version`, `sys_object_id` (SNMPv2-MIB
scalars, `device.proto:139-178`). Missing against RFC 3418/RFC 7317: contact,
location (device-reported free text, distinct from `DeviceConfig.location`,
which is FlowSeer's *own* placement — the atlas doc flags `sysDescr`/
`sysLocation` as "not an identifier", i.e. keep them as unreliable observed
strings, never promote to a ref), and uptime.

```protobuf
// sysContact (RFC 3418). Unset means none reported.
string system_contact = 11 [(buf.validate.field).string.max_len = 255];
// sysLocation (RFC 3418) — the device's own free-text claim, unrelated to
// DeviceConfig.location. Unset means none reported.
string system_location = 12 [(buf.validate.field).string.max_len = 255];
// Time since last (re)boot, from sysUpTime or the platform's own uptime
// field. Absent means not reported. Never derive a boot timestamp from this
// without recording when it was read — sysUpTime wraps at ~497 days
// (2^32 centiseconds) and some agents reset it on SNMP-agent restart, not
// device reboot.
google.protobuf.Duration uptime = 13;
```

### 4.4 CPU / storage as a device-scoped table, not fields on Device

`HOST-RESOURCES-MIB` and `openconfig-platform-cpu` both key CPU/storage by
index/component, and a device can have several (multi-core, multiple flash
partitions). Prefer a repeated table over singleton fields on `DeviceState`
for a whole-device (non-per-component) summary such as MikroTik or UniFi
device-level `cpuUtilizationPct`/`memoryUtilizationPct` where there's no
component tree granularity available (net-snmp based platforms). This is a
**separate concept from 4.2's per-component fields** — a source that gives
you `ComponentState` rows for individual CPU/storage components should use
4.2; a source that only gives a whole-box percentage (most cloud APIs) has
nowhere else to put it and needs a device-level table or a
`DeviceState`-level scalar. Planner should decide whether to force every
source through the component tree (cleaner, but many providers in §3 never
expose a component-level breakdown) or accept a device-level fallback field.

### 4.5 `flowseer.net.time.v1` — new package, NTP association table

```protobuf
// One NTP peer/server association as the device reports it (RFC 5905
// association model; stratum semantics per openconfig-system.yang, this
// repo's vendored copy at spec/yang/ruckus/icx/9.0.00/openconfig-system.yang).
message NtpAssociation {
  // Server address or hostname, as the device spells it. Must be present.
  string address = 1 [(buf.validate.field).string.min_len = 1];
  // 0 = unspecified/invalid, 1 = primary, 2-15 = secondary, 16 =
  // unsynchronized, 17-255 = reserved. Registry pass-through: the device's
  // own stratum number, not renumbered.
  uint32 stratum = 2 [(buf.validate.field).uint32.lte = 255];
  // Offset from local clock, in microseconds. Absent means not reported.
  int64 offset_microseconds = 3;
  // Reach register (RFC 5905 §9.2), the last 8 poll outcomes as a bitmask.
  // Absent means not reported.
  uint32 reach = 4 [(buf.validate.field).uint32.lte = 255];
}
```

Hangs off `DeviceState` as `repeated NtpAssociation ntp_associations = N`
(device-scoped table, no per-association ref needed — matches the FDB/
NeighborEntry table pattern). RFC 5905 itself was not fetched for this dossier
(`NTPv4-MIB` RFC 5907 covers the SNMP shape, and this dossier
fetched RFC 7317's YANG NTP container and the vendored OpenConfig copy
instead) — **mark the exact reach-register semantics unverified** pending a
direct RFC 5905/5907 read.

### 4.6 Syslog and alarms — two different Event shapes, per the atlas's own recommendation

The atlas doc is explicit that these must not be conflated
(`09-ops.md:123-126`): a syslog line is unstructured and point-in-time; an
alarm is named, stateful, and clearable.

**Syslog** — Event-only family, no Config, no State (a log line is never
"the current state of" anything):

```protobuf
// flowseer.model.inventory.v1 or a new model/syslog/v1 — planner decides
// package per the ref-pair-lives-with-owner rule.
message SyslogEvent {
  DeviceGlobalRef device = 1 [(buf.validate.field).required = true];
  // RFC 5424 §6.2.1 numeric severity (0=Emergency..7=Debug). Registry
  // pass-through: 0 is a real value (Emergency), so presence carries
  // "not reported", not "Emergency".
  SyslogSeverity severity = 2;
  // RFC 5424 §6.2.1 numeric facility (0-23; 16-23 = local0-local7).
  SyslogFacility facility = 3;
  string message = 4 [(buf.validate.field).string.min_len = 1];
  google.protobuf.Timestamp observed_at = 5;
}
```

**Alarms** — a genuine Entity with a triad-shaped but deliberately partial
family (no Config — nobody configures an alarm's existence, only the device
raises/clears it): `AlarmState` + `AlarmEvent`, keyed by (device, resource,
alarm-type) the way `ALARM-MIB`'s model/active split and `ietf-alarms`/
`openconfig-alarms` both do (`09-ops.md:114-118`, RFC 8632 §6). Severity is
the five-level `_UNSPECIFIED, INDETERMINATE, WARNING, MINOR, MAJOR, CRITICAL`
normalized enum (RFC 8632's own `indeterminate` "SHOULD be avoided" language
argues for keeping it distinct from `_UNSPECIFIED`, i.e. six values not
five). `is_cleared` stays a separate bool field, not folded into severity,
per RFC 8632 §6's explicit statement that clearing is orthogonal to
severity.

## 5. Entity candidates (model/)

| Entity | Identity key | Owning parent | Config/State/Event | Notes |
|---|---|---|---|---|
| `Component` (landed) | device-local `name` | `Device` | State + Event only, deliberately no Config (already documented at `component.proto:6-10`) | Extend per §4.1/4.2, do not re-key |
| `Alarm` | (device, resource string, alarm `type_id`) composite — needs a `AlarmLocalRef`/`AlarmGlobalRef` pair under `Device` | `Device` | State + Event, deliberately no Config | `resource` should probably be a `ComponentLocalRef` when the alarm is component-scoped and a bare device ref otherwise — planner should resolve this dynamic-target question; it resembles the `EntityRef` dynamic-kind case but `EntityRef` is top-level-only (`protobuf.md:135-137`) and an alarm's resource is nested (component), so `EntityRef` does not fit as-is |
| `FirmwareImage` | device-local slot/bank identifier (primary/secondary, A/B, "FirmSafe" position — no universal name, atlas `01-platform.md:401-403`) | `Device` | State + Event, no Config unless FlowSeer ever drives image activation | Every vendor has *a* dual-image concept, none name it the same — normalize to `slot` string + `is_active`/`is_running` booleans rather than an enum of vendor slot names |
| System identity, time sync, config-file/backup, license | not separate entities | fields/tables on existing `DeviceState` or device-scoped tables | — | See §4.3–4.5; none of these need their own UUID identity distinct from the device they describe |

`Component` is confirmed (§2, §4) as "the single highest-value unmodelled
[structure]" per the atlas and is already landed — the work here is additive
fields, not a new entity.

## 6. Traps

1. **Fixed-unit fields vs. runtime scale.** ENTITY-SENSOR-MIB and
   `ietf-hardware` both use a runtime value+scale+precision triple so one
   wire shape covers 0.1°C and 0.001°C sensors alike. FlowSeer's convention
   is fixed canonical units in the field name (`protobuf.md:225` "units in
   field names or comments" is implied by the existing
   millidegrees/microvolts/microamperes/nanowatts fields). This means every
   mapper must normalize vendor scale/precision into the canonical unit at
   ingestion time — the schema does not carry scale, so a mapper bug here is
   silent and undetectable from the wire alone. Precision loss direction
   matters: millidegrees (10⁻³) is coarser than ENTITY-SENSOR-MIB's typical
   0.1°C-scale-9-precision-1 example only by 100x margin, so it is safe, but
   a sensor reporting in micro-Celsius would need to be validated against
   the existing comment style (`module_temperature.proto:8-12` already does
   this arithmetic for SFF-8472's 1/256° steps — copy that reasoning per
   quantity).
2. **AC vs DC voltage.** ENTITY-SENSOR-MIB has separate `voltsAC(3)` and
   `voltsDC(4)` types; the existing `SupplyVoltage` message and the proposed
   `Voltage` arm above do not distinguish them. PSU input feed (often AC)
   and DC rail voltages (module supply) are different physical facts that
   must not share one arm without a family/kind field — a follow-up
   refinement, flagged here rather than resolved.
3. **`entPhysicalClass` is 12 values and is routinely misreported** —
   `module(9)` vs `port(10)` vs `other(1)` for the same physical transceiver
   depending on vendor (`01-platform.md:145-146`). FlowSeer's
   `COMPONENT_KIND_TRANSCEIVER` already resolves the specific
   transceiver-vs-module ambiguity by adding a value the MIB doesn't have,
   but the general "vendor misclassifies" problem persists for fans,
   sensors, and PSUs too and has no schema-level fix — it's a mapper
   correctness problem, not a modeling gap.
4. **`hrStorageSize`/`Used` need `hrStorageAllocationUnits` as a multiplier**
   (RFC 2790 §4.3) — do not copy the raw integer without also capturing (or
   pre-multiplying by) the allocation unit; a naive `available_bytes` field
   fed directly from `hrStorageSize` without multiplying is wrong by the
   allocation-unit factor.
5. **`hrProcessorLoad` is a trailing 1-minute average, not instantaneous**
   (RFC 2790 §4.4, "over the last minute ... may approximate"). OpenConfig's
   `avg-min-max-instant-stats-pct` grouping distinguishes instant from
   averaged; a field literally named `utilization_basis_points` with no
   averaging-window comment invites a consumer to treat a 60-second average
   as a live reading.
6. **`sysUpTime` wraps at ~497 days and some agents reset it on SNMP-agent
   restart, not device reboot** (`01-platform.md:71-73`) — never derive
   "device rebooted" purely from uptime decreasing; the atlas explicitly
   warns `snmpEngineTime` and `sysUpTimeInstance` "do not always agree."
7. **Stacked/clustered devices report *n* serials for one FlowSeer Device**
   (`01-platform.md:69-70`, `226-249`, `281-283`) — this domain's component
   tree and sensor tables are *per-member*, and `DeviceState.serial` (single
   field) cannot represent a stack. This is a pre-existing open problem
   flagged by the atlas, not solved by this dossier's proposals; component
   sensor rows should carry enough to disambiguate which stack member they
   belong to once stacking is modeled (parent chassis component, most
   likely), but stacking itself is out of this domain's scope.
8. **LANCOM LMC and RUCKUS SmartZone (vendored specs) are configuration/
   fleet-management planes, not raw SNMP-shaped telemetry** — do not assume
   every provider maps cleanly onto the ENTITY-SENSOR-MIB/OpenConfig shape;
   some will only ever supply `configState`/`licenseState`/`heartbeatState`
   coarse status enums, never a numeric sensor reading. A schema requiring
   every source to fill every `SensorReading` field would be unfillable for
   these two vendored specs.
9. **Percent fields differ in unit and precision across sources** — UniFi's
   `cpuUtilizationPct`/`memoryUtilizationPct` are doubles (presumably
   fractional percent, unconfirmed decimal precision from the schema alone
   — the OpenAPI type gives no scale), `hrProcessorLoad` is an integer
   0–100. §4.4's `utilization_basis_points` (0–10000) is proposed
   specifically to avoid forcing a float-to-integer precision decision per
   source, but the planner should confirm this against a couple of
   representative payloads before finalizing, since "format: double" in an
   OpenAPI schema does not prove any provider actually emits sub-percent
   precision.

## 7. Open questions for the planner

1. Should `SensorReading`'s `Voltage`/`Current`/`Power` arms be reused
   verbatim for chassis/PSU-level readings, or does PoE's already-landed
   `pse_budget.proto`/`poe_status.proto` in `net/phy/v1` need reconciling
   with this new package first (both describe power, at different layers)?
2. AC-vs-DC voltage distinction (trap 2) — worth a `kind` sub-field on
   `Voltage`, or a separate `AcVoltage`/`DcVoltage` typed-variant split
   mirroring the `IpAddress`/`MacAddress` pattern?
3. Package name: this dossier proposes `net/env/v1` for sensors and
   `net/time/v1` for NTP. Confirm these don't collide with a name another
   domain dossier is also proposing (system/platform boundary questions were
   flagged by the atlas itself — "the likely home is a future
   `flowseer.net.system.v1`; no such package exists yet",
   `01-platform.md:81-82`) — the planner has visibility across all ten
   domain dossiers and should reconcile naming here.
4. Should whole-device CPU/memory (no component breakdown available, §4.4)
   live as scalar fields directly on `DeviceState`, or as a single-row
   "table" for symmetry with the per-component case? A scalar is simpler
   but breaks the "utilization is always a table" invariant if any other
   domain also needs whole-device utilization rows (e.g. forwarding-table/
   TCAM utilization, flagged by the atlas as "a far better operational
   signal than CPU load on a switch" — `01-platform.md:487-490` — which is
   arguably this same domain's concern and was not otherwise covered here).
5. Alarm's dynamic resource target (§5) — needs a resolution the existing
   `EntityRef`/typed-ref conventions don't cleanly cover, since
   `EntityRef` is top-level-only and an alarm's resource is frequently a
   nested `Component`. Flag for guardrail-level convention discussion, not
   a decision this dossier should make unilaterally.
6. `ietf-hardware`, `ietf-system`, `ietf-alarms` YANG modules are not
   vendored (§1) although they are the primary sources for this domain. Should they be added to `spec/yang/` before schema work lands,
   or is the OpenConfig coverage judged sufficient? This affects whether
   the config/state split proposed in §4 (e.g. `admin-state`, trap 8's
   ietf-hardware-only leaf) can be cited against a vendored source or only
   against RFC text fetched ad hoc.
7. Firmware image slot naming (§5) has no cross-vendor convention — is a
   free-text `slot` string acceptable, or does the planner want a small
   normalized enum (`PRIMARY`/`SECONDARY`/`OTHER`) accepting that some
   vendors have three-plus images?

## Addendum (parallel repo-research pass, folded in)

A second read of the repo (conventions, `component.proto`, `device.proto`,
the direction doc, and a corpus listing) run in parallel confirmed everything
above and added:

- `spec/mib/ietf/ALARM-MIB` itself has no `PerceivedSeverity`/severity
  taxonomy; the severity textual convention lives in the vendored
  `spec/mib/ietf/ITU-ALARM-TC-MIB:49-64`:
  `ItuPerceivedSeverity ::= TEXTUAL-CONVENTION` with
  `cleared(1), indeterminate(2), critical(3), major(4), ...` — note this
  registry orders `critical` *before* `major` (opposite severity direction
  from §2's OpenConfig ordering, which numbers up from `MINOR` to
  `CRITICAL`). A pass-through of ITU's numbers would not sort the same way
  as the OpenConfig-derived normalized enum proposed in §4.6 — pick one
  registry as canonical and do not mix ordinal assumptions across them.
- `net/phy/v1/module_diagnostics.proto` composes exactly two of the four
  measurement kinds by value: `ModuleDiagnostics{ModuleTemperature
  temperature = 1; SupplyVoltage voltage = 2;}`, with a comment "Per-lane
  measurements live on the module's lanes" — `BiasCurrent`/`OpticalPower`
  are presumably on `module_lane.proto` (per-lane DOM), not read in full by
  either research pass. Before deleting/moving the four messages per §4.1,
  the planner must also update `module_diagnostics.proto` and
  `module_lane.proto` to reference the new `net/env/v1` types.
  `pse_budget.proto` and `poe_port_detail.proto` were likewise not read in
  full — they may already define a PSU/power-budget shape that the §4.2
  `allocated_power_watts`/`used_power_watts` proposal should reconcile with
  rather than duplicate (see open question 1, sharpened by this).
- `docs/research/network-domain-atlas/entities/04-gaps-and-recommendations.md`
  exists and was not read by either pass — it is referenced from
  `09-ops.md:14` as holding the atlas's own priority ranking and may already
  rank this domain's gaps; worth a read before finalizing scope.
- Confirmed (independently, by grep) that no `Cpu`/`Memory`/`Storage`/
  `Alarm`/`Ntp`/`Syslog`/`Firmware`/`License` message exists anywhere under
  `spec/proto/flowseer/` today — every message proposed in §4–5 is new, not
  a rename of existing FlowSeer schema.
