# Syslog Event Records

`flowseer.event.log.v1` defines `SyslogRecord`, the durable stream record of
one syslog message received from a managed device. A syslog record represents
an append-only timeline event, never a reconciled current-state entity or an
alarm.

## Boundaries

Imports: model/inventory, net/addr, net/log

Imported by: integration/ingest

Deliberately absent:

- A record id: deduplication and record identity belong to the enclosing
  `flowseer.integration.ingest.v1.IngestRecord` envelope.
- `SyslogRecordConfig` and `SyslogRecordState`. This package is a pure event
  stream with no operator intent and no current-state query entity.
- A tenant. Tenancy is ambient and scoped by the enclosing transport envelope.
- Parsing of legacy BSD (RFC 3164) header fields: BSD-formatted messages place
  the payload into `message` and leave RFC 5424 header fields unset.
- Device syslog daemon configuration or forwarding rules: those belong to
  device-level management rather than the event stream.

## Structure and RFC 5424 bounds

`SyslogRecord` captures the header and payload fields defined in RFC 5424:
- `device`: The UUID reference of the device the record belongs to.
- `received_at`: Timestamp assigned by the collector upon receipt.
- `sent_at`: Timestamp from the RFC 5424 header, left unset when the sender emits NILVALUE (`-`).
- `severity` and `facility`: PRI fields from `flowseer.net.log.v1`. Unset when
  the message carried no PRI.
- Header identifiers: `hostname` (up to 255 chars), `app_name` (up to 48 chars),
  `proc_id` (up to 128 chars), and `msg_id` (up to 32 chars). Senders that emit
  NILVALUE or do not use RFC 5424 headers leave these fields unset.
- `structured_data`: Repeated elements with unique `id` (up to 32 chars) and
  key-value parameter pairs (`name` up to 32 chars, `value` up to 1024 chars).
- `message`: Carried as `bytes` because RFC 5424 §6.4 permits MSG to be UTF-8 or
  arbitrary octet sequences (`MSG-ANY`). The payload is bounded at 65527 octets,
  the maximum payload size that fits in a single UDP syslog datagram (RFC 5426 §3.2).
  If a collector on another transport receives a longer payload, it truncates it to
  65527 octets and sets `message_truncated = true`.
- `source_address`: The IP address of the sender observed by the collector.

## Sources

- RFC 5424 (<https://www.rfc-editor.org/rfc/rfc5424.html>) for syslog protocol fields,
  header syntax, structured data, and message bounds.
- RFC 5426 (<https://www.rfc-editor.org/rfc/rfc5426.html>) §3.2 for the UDP transmission
  payload bound.
