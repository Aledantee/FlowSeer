# Telemetry delivery

`flowseer.edge.telemetry.v1` holds `TelemetryService`, the Connect call an
edge delivers serialized OTLP request bodies through. Central posts each body
unchanged to its collector.

`TelemetryService.Deliver` takes a batch of `TelemetryExport`, each a signal
(logs, metrics, or traces) and the body. The response holds `settled`, the
count of leading exports the collector accepted or refused for their content.
A collector that cannot take a body now ends the count, and so does a central
with no collector configured. The edge resends from that index.

The request's `items: {ignore: IGNORE_ALWAYS}` keeps the interceptor from
validating each export. Central validates each one and settles an invalid
export as refused, so one invalid export cannot refuse the batch.

## Boundaries

Imports: nothing

Imported by: nothing

Deliberately absent:

- The OTLP schema. A body is opaque bytes, bounded at 255 KiB so that the body
  and its stored envelope fit an edge log record.
- The edge identity. The call's assertion names the edge, so a message carries
  no edge field of its own.
