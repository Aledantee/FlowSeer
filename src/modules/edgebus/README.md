# edgebus

The NATS carrier between an edge and central, assembled by both hosts. The
device service starts the hub; the edge agent starts the leaf node and the
loopback receiver. Nothing an edge must act on rides here: dispatches,
reports, and audit records are Connect calls in `integration/device/v1` and
`event/device/v1`. The bus carries what the edge publishes, the agent's own
OpenTelemetry signals today and, on the same buffer, the device logs, traps,
and change events the ingestion sources will add. The accepted
[device-service record](../../../docs/architecture/2026-08-20-device-service-and-inventory-direction.md)
fixes the shape ("Edge attachment and enrollment"): an embedded leaf node per
edge, its own JetStream domain, a bounded local buffer, and one account per
tenant enforced by the broker.

## Subjects and streams

```
flowseer.<tenant>.edge.<edge-id>.otel.{logs,metrics,traces}   the agent's OTLP bodies
flowseer.<tenant>.edge.<edge-id>.ingest.<source>.>            a future ingestion source
flowseer.<tenant>.edge.<edge-id>.source.>                     the hub's sourcing deliveries
flowseer.<tenant>.audit.device.<device-id>                    central's audit records
```

The edge's `EDGE_BUFFER` stream, in JetStream domain `edge-<edge-id>`, holds
the `otel` and `ingest` branches, file-backed, bounded by bytes and age with
the oldest record discarded first. For each attached edge the hub creates
`FLOWSEER_EDGE_<edge-id>`, a stream that sources that edge's buffer and
nothing else, across its leaf link through `$JS.edge-<edge-id>.API`,
delivering on the `source` branch: that branch is inside the edge's publish
permission and outside its buffer's subjects, which matters because JetStream
refuses a consumer that would deliver into the stream it reads. One stream
per edge is what makes a record's edge a fact of where it is stored rather
than of the subject it carries; the forwarder relies on that below. The hub
also owns `FLOWSEER_DEVICE_AUDIT` and the `device-lanes` and `edges`
key-value buckets the device service writes.

A leaf without a distinct domain silently extends the hub's; `EdgeDomain`
is the guard.

## The credential an edge is minted

`Hub.MintEdgeUser` signs a user in the tenant account whose permissions
follow the traffic directions and nothing else:

| Direction | Allowed |
| --- | --- |
| publish | `flowseer.<tenant>.edge.<edge-id>.>`, `$JS.edge-<edge-id>.API.>`, `_INBOX.<edge-id>.>`, `_INBOX.>`, `$JSC.R.>` |
| subscribe | `$JS.edge-<edge-id>.API.>`, `_INBOX.<edge-id>.>`, `$JS.FC.>` |

Publishing under the subtree is the edge's job; its own JetStream API and
inbox are what the hub's sourcing requests reach and what the edge answers
on; `_INBOX.>` is where a client's requests expect their replies and
`$JSC.R.>` is where the hub server's own source client expects the answer
to its consumer request. The subscribe side admits the sourcing requests
and the flow-control replies the hub sends the sourcing consumer, so
nothing central publishes reaches an edge by this path. Each of those
subjects was found by bisecting a sourcing failure, not read from a
document; a narrower set silently stalls sourcing with no warning on either
server. `TestEdgePermissionsConfineTheLeaf` proves
both: a publish under another edge's subtree never crosses, and a
subscription on `flowseer.>` imports nothing. The hub runs one account,
`default`, so the tenant token carries no broker enforcement until a second
account exists.

The hub's keys (operator, system account, tenant account) are generated
into `<StateDir>/keys` on first start and read back afterwards, so a restart
keeps every minted credential valid. A leaf writes its credential to
`<StateDir>/hub.creds`, mode 0600, because a remote leaf authenticates from a
file.

## Durability

Both servers declare their fsync policy with `service.BusFsyncPolicy` and go
through `service.NormalizeFsync`, the rule the process-local bus follows, so
the three embedded JetStream servers in the repository tell one durability
story. Central declares `BusFsyncPerMessage` for the hub, which is the
journal's authority; an edge declares `BusFsyncPeriodic` for its buffer,
since a record lost to a power cut there is a gap in history. An undeclared
policy refuses to start with `edgebus/config`.

## What a compromised edge can and cannot do

The permission set is the boundary, and it was found by widening until
sourcing worked, so what the last widening let in was checked rather than
assumed. A publish on the edge's own `otel` or `ingest` subjects is captured
by its buffer and sourced honestly, which is the ordinary path. A publish
dressed with a sourcing ack reply and a `Nats-Stream-Source` header naming
another edge's subject is dropped by the hub, not stored under that subject
(`TestForgedSourceHeadersDoNotRelabelRecords`). A publish inside the
subtree but on neither buffered branch is refused with `edgebus/leaf`, never
acknowledged by nothing (`TestPublishOutsideTheBufferedBranchesFailsLoudly`).

What remains is the delivery subject itself: the hub's source consumer
delivers on `<subtree>.source.S.<nonce>`, which the edge may publish on.
The consumer is ephemeral, unlisted, and unpersisted, so no client path
learns the nonce, but the embedded server's own memory holds it, and a
compromised agent process could publish a record there with a header
naming another edge's subject, which the hub would store under that
subject. That is why the hub keeps one stream per edge rather than one
aggregate: the forwarder reads the edge from the stream's name and drops a
record whose subject lies outside that edge's subtree
(`TestForwarderRefusesARecordOutsideItsStreamsEdge`), so a relabeled record
is refused where it is read, whatever the permission set allows. A refusal
is the one sign that an agent is trying to relabel another edge's data, so
it is not a silent discard: the forwarder emits a WARN record, event name
`flowseer.edgebus.record.refused`, carrying the stream's edge, the edge the
subject claimed, the reason, and the subject, rate-limited to one per edge
stream every ten seconds; and it counts `flowseer.edgebus.records.refused`
labeled by reason (`foreign_subject`, `not_otel`) and signal, two closed
sets, since an edge id is an identity the cardinality rule keeps off metric
attributes. `Forwarder.Dropped` keeps the exact count for a test. Before the
per-edge streams, that guarantee rested on a nonce no client path could
learn; now it rests on where the record is stored, which no permission
change can widen.

## OTLP over the bus

The edge's runtime exports OTLP/HTTP to `Receiver`, bound to `127.0.0.1` on a
kernel-chosen port so a collision cannot stop the agent. The receiver
publishes each `/v1/{logs,metrics,traces}` body as received into the buffer
and answers 200; the runtime keeps its batching and retry, a refused buffer
answers 503 so the runtime retries, and a compressed body is refused with
415 since the bytes are stored and forwarded unchanged. Central's `Forwarder`
follows every edge's hub stream with a durable consumer, discovering edges
attached after it started on an interval, and posts each body to central's
collector endpoint, acknowledging only on a 2xx. `TestReceiverToForwarderCarriesBodiesUnchanged` checks the bytes end
to end, and `TestRecordsPublishedWhileTheHubIsDownArriveAfterReconnect` is
the evidence that sourcing across a leaf link survives the link dropping
and returning. After a hub restart the source takes about forty seconds to
re-establish, the server's own retry cadence; a record published meanwhile
waits in the edge's buffer and arrives when it does.

## TLS

The hub's WebSocket listener serves Connect's certificate. An edge verifies
it with `PinVerifier`, which accepts a leaf certificate whose SPKI SHA-256
digest is among the anchors `EdgeProvisioning` and `EnrollResponse` carry,
and both of the edge's dialers, the Connect client and the leaf remote,
install the same verifier so they cannot drift apart. The WebSocket leaf
path needs the leaf-node subsystem enabled, which the server keys on a leaf
port, so the hub also binds a plain leaf listener on loopback; an edge never
dials it.

## What is deliberately absent

- Any dispatch, report, or audit subject. Those are Connect calls.
- Announce subjects and capability advertisement.
- A second tenant account, and the JWT revocation push that goes with it.
