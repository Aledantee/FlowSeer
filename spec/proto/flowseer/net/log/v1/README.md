# Syslog Primitives

The `flowseer.net.log.v1` package defines the standard syslog severity and
facility registries as pass-through enums matching RFC 5424 §6.2.1.

## Boundaries

Imports: nothing FlowSeer-owned

Imported by: nothing FlowSeer-owned

Deliberately absent:

- Syslog records, structured data, headers, and framing. A syslog log line is an
  event record and lives in `event/log/v1`.
- Syslog daemon or forwarder configuration.

## Registry pass-through semantics

Both `SyslogSeverity` and `SyslogFacility` are registry pass-through enums where
integer values match the RFC 5424 tables directly (0..7 and 0..23, respectively).
In both enums, value 0 is a real assigned registry value (`SYSLOG_SEVERITY_EMERGENCY`
and `SYSLOG_FACILITY_KERN`). Neither enum declares an `_UNSPECIFIED` sentinel.
Absence of a field carries "not reported", and consumers must check field presence
with `Has*()` accessors before reading the getter.

Because these enums cover their complete numeric domains, `(buf.validate.field).enum.defined_only = true`
fully validates them without requiring custom predefined rules.

## Contents

- `syslog_severity.proto` — `SyslogSeverity`: RFC 5424 §6.2.1 Table 2 severities (0..7).
- `syslog_facility.proto` — `SyslogFacility`: RFC 5424 §6.2.1 Table 1 facilities (0..23).

## Sources

- RFC 5424 §6.2.1 (<https://www.rfc-editor.org/rfc/rfc5424.html#section-6.2.1>) for
  the facility and severity numerical codes and definitions.
