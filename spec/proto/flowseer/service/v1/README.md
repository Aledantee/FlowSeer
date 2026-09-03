# Service messages

The `flowseer.service.v1` package owns the record stored in every durable
service mailbox. A command, an event delivery, and a reply use the same
`Message` envelope, so crash recovery does not depend on an in-memory request
or a Go type name.

For example, a reply to `edge/ingest/syslog` is persisted with kind
`MESSAGE_KIND_REPLY`, that module's path as its target, the request's
`correlation_id`, and the request message's id as its `causation_id`. The
`type_name` identifies the protobuf payload and `payload` holds its binary
encoding. Delivery resolves the name through the runtime's allowlist before it
decodes or invokes a handler.

The package and message full names, enum numbers, field numbers, and logical
module paths are storage contracts. Renaming or renumbering one while queued
records exist requires a migration or an explicit compatibility path. Binary
readers also preserve unknown fields. This lets an older compatible binary
round-trip an envelope written with fields it does not yet understand.

Validation rejects records with no message id, target, payload type, or payload
presence. It also rejects unknown kinds and malformed stable identifiers before
the record reaches a mailbox handler. Payload support is a separate runtime
check: a syntactically valid `type_name` can still be absent from that service's
allowlisted resolver and is then discarded with an observable disposition.

Trace context uses the W3C `traceparent` and `tracestate` header values captured
at publication. Payload and trace-header contents are durable data and must not
be written to logs or metric labels.
