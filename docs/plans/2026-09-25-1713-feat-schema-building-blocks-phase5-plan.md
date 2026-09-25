---
title: Schema Building Blocks Phase 5, Platform, System, and Operations - Plan
type: feat
date: 2026-09-25
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-25-1713-feat-schema-building-blocks-plan.md
---

# Schema Building Blocks Phase 5, Platform, System, and Operations - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

A device's hardware health, resources, software, time sync, alarms, and
logs have a place in the schema: sensor rows and an oper status on
`ComponentState`, system contact, location, and uptime on `DeviceState`,
`net/system/v1` for resource utilization, images, and licenses,
`net/protocol/ntp/v1` for associations, `net/log/v1` for the syslog
registries, the `Alarm` entity, and the `SyslogRecord` event.

## Decisions

The record's entity list (Alarm owned by a device with State and Event;
SyslogRecord in `event/log`) and rules 1 and 7 govern. From dossier 04,
to be confirmed in the re-plan:

- `ComponentState` carries `repeated measure.v1.SensorReading sensors`; a
  power supply reports voltage, current, and power at once.
- A device-level utilization row exists for sources with no component
  breakdown (most cloud APIs report whole-box CPU and memory percentages).
  `hrProcessorLoad` is a one-minute average (RFC 2790); the row says which
  window it holds.
- `sysUpTime` wraps at about 497 days and some agents reset it on an agent
  restart; the uptime field's comment says a reboot is not inferred from it.
- Syslog severity and facility are pass-through enums with real zeros
  (RFC 5424 §6.2.1).
- The alarm's resource is a named resource on the device (component,
  interface, or the device itself); `EntityRef` cannot address a nested
  component, so the re-plan chooses between a typed resource oneof and a
  resource name string.
- The re-plan confirms `net/system` as the package name or renames it and
  amends the record (parent open question).

## Requirements

1. A PSU `ComponentState` with voltage, current, and power readings passes;
   a reading with no quantity arm fails.
2. `SyslogSeverity` value 0 is `EMERGENCY` and presence distinguishes it
   from unset.
3. An `AlarmState` without its device ref fails.
4. `NtpAssociation.stratum = 256` fails.
