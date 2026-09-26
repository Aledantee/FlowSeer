# Measurement Primitives

The `flowseer.net.measure.v1` package defines measured quantities as values
rather than source-specific encodings: sensor readings with their alarm
thresholds, the basis-points ratio rule, and path quality. Every quantity has
one canonical integer unit named in the field, so a mapper converts once at
the edge of the system and a consumer never learns which unit a source used.

## Boundaries

Imports: nothing FlowSeer-owned

Imported by: net/phy, net/system, net/wlan

Deliberately absent:

- Floating-point values. A quantity has a native fixed-point resolution, so
  the unit name carries it and the field is an integer.
- Device, sensor, and time context. A reading does not name what it was read
  from or when; the row or entity that embeds it carries that.

## Contents

- `basis_points.proto` — the predefined rule for a ratio or percentage, 0
  through 10000 basis points. Whole-percent sources fit exactly, fractional
  ones fit to 0.01 %.
- `sensor.proto` — one message per quantity a sensor reports
  (`Temperature`, `Voltage`, `Current`, `Power`, `RotationSpeed`,
  `RelativeHumidity`), each `value_<unit>` with four thresholds, and
  `SensorReading`, the required oneof over them. Present thresholds must be
  ordered low alarm <= low warning <= high warning <= high alarm.
- `path_quality.proto` — `PathQuality`: round-trip latency, jitter, and
  packet loss in basis points.

## Sources

- SFF-8472 (<https://members.snia.org/document/dl/25916>) for the
  value-plus-ordered-thresholds shape of optical module diagnostics.
- Meraki `getOrganizationDevicesUplinksLossAndLatency`
  (<https://developer.cisco.com/meraki/api-v1/get-organization-devices-uplinks-loss-and-latency/>)
  for the path-quality quantities; its `latencyMs` is round-trip.
