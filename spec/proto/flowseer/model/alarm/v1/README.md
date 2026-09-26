# Alarm Entity

`flowseer.model.alarm.v1` defines the Alarm entity (`AlarmState`, `AlarmEvent`),
representing a named, clearable fault condition observed on a managed device.

## Boundaries

Imports: model/inventory, net/key

Imported by: nothing FlowSeer-owned

Deliberately absent:

- `AlarmConfig`: Alarms are device-reported conditions and state transitions,
  not operator-intended configuration. Operator acknowledgement, shelving,
  and ticketing workflows are not modeled here.
- A tenant: Tenancy is ambient and inherited from the owning device.
- `EntityType` admission: The alarm is keyed within its owning device
  (`AlarmGlobalRef` composes `DeviceGlobalRef` with `AlarmLocalRef`), not by a
  flat top-level UUID.

## Key structure and arm resolution

An alarm is identified by `(device, resource, type_id, type_qualifier)`:
- `device`: The device reporting the alarm.
- `resource`: A typed `AlarmResource` oneof targeting `WholeDevice` (whole-box alarms),
  `ComponentLocalRef` (physical or logical components), `interface_name` (network interfaces),
  or `other` (any device-reported resource identifier not covered by the typed arms,
  such as a routing peer or software daemon).
- `type_id`: The alarm type identifier (mnemonic, OID, or model index).
- `type_qualifier`: An optional qualifier distinguishing distinct instances of the
  same alarm type on the same resource.

Mappers resolve the resource arm identically across raise, update, and clear
events so that the composite key remains stable throughout the alarm lifecycle.

## Severity mapping

`AlarmSeverity` normalizes source severities in urgency order:
`INDETERMINATE = 1`, `WARNING = 2`, `MINOR = 3`, `MAJOR = 4`, `CRITICAL = 5`.
Different sources structure and number severities differently:
- RFC 8632 orders the same five severities from indeterminate to critical and separates
  the cleared status into an independent boolean (`is-cleared`).
- ITU-ALARM-TC-MIB (`ItuPerceivedSeverity`) includes `cleared(1)` inside the severity
  enumeration and numbers the remaining severities in a different order.
- OpenConfig `openconfig-alarm-types.yang` defines `UNKNOWN` corresponding to `INDETERMINATE`.

Mappers map to `AlarmSeverity` by name, never by source integer value.

## Clear versus vanish

`cleared` is a required boolean on `AlarmState`.
- **Clear transition**: When a source explicitly reports that an alarm has cleared
  (such as ALARM-MIB `alarmClearTable` or RFC 8632 `is-cleared = true`), the mapper
  emits an `AlarmEvent` with `after.cleared = true`.
- **Vanish transition**: When a source only lists currently active alarms (as OpenConfig
  does) and an alarm disappears from subsequent queries, the mapper emits an
  `AlarmEvent` with `after` unset, never an inferred clear.

## Sources

- RFC 8632 (<https://www.rfc-editor.org/rfc/rfc8632.html>) for the alarm model,
  resource tuple, qualifiers, timestamps, and separate cleared state.
- RFC 3877 (ALARM-MIB) for whole-device resource conventions and active/clear tables.
- ITU-T M.3100 and ITU-ALARM-TC-MIB for perceived severity classifications.
- OpenConfig `openconfig-alarms.yang` for resource naming conventions.
