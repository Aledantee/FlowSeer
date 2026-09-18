# Runtime messages

The `flowseer.runtime.v1` package owns the record stored in every durable
service mailbox. A command, an event delivery, and a reply use the same
`Message` envelope, so crash recovery does not depend on an in-memory request
or a Go type name.

## Boundaries

Imports: nothing FlowSeer-owned

Imported by: nothing

Deliberately absent:

- Connect service RPC definitions. This package is the process-local bus and
  mailbox storage contract.
- Domain types and entity payloads. Payloads are opaque serialized bytes.

For example, a reply to `edge/ingest/syslog` is persisted with kind
`MESSAGE_KIND_REPLY`, that module's path as its target, the request's
`correlation_id`, and the request message's id as its `causation_id`. The
`type_name` identifies the protobuf payload and `payload` holds its binary
encoding. Delivery resolves the name through the runtime's allowlist before it
decodes or invokes a handler.

Enum numbers, field numbers, and the logical module paths carried in
`source_path` and `target_path` are storage contracts: renaming or
renumbering one while queued records exist requires a migration or an
explicit compatibility path. The package and message full name are not,
because the wire format carries neither; the 2026-09-18 rename from
`flowseer.service.v1` to this package shipped without one for exactly that
reason. What does carry the envelope's persisted identity is
`RuntimeManifest.envelope_type`; see the [network model structure
record](../../../../../docs/architecture/2026-08-20-network-model-structure-direction.md)'s
2026-09-18 amendment for what a mismatch there requires on startup. Binary
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

The same package owns the bus control records. `RuntimeManifest` and
`ReconciliationRecord` make stream and consumer changes resumable without
inventing a private JSON compatibility surface. `Settlement` records retry or
terminal intent before a broker acknowledgement. `StoreProvenance` is a small
sidecar read before NATS opens an existing store, so a server-version change can
require the operator's backup rather than discovering the pin after the new
binary has already touched the files.
