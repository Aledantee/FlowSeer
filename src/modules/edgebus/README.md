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
edge, its own JetStream domain, a bounded local buffer, and broker-enforced
accounts.

## Two accounts are the security boundary

An edge credential can make the server publish for it, with the server's own
internal client, into any stream in the edge's account: a JetStream
flow-control control message (an empty body under a `NATS/1.0 100 ` status)
names its own reply subject, and `processInboundSourceMsg` obeys it through a
client subject to no permissions, before the check that would bind the
message to the sourcing consumer. No permission list closes that, because the
publish is the server's, not the edge's. So the journal and the audit stream
must live where no edge credential reaches.

The hub therefore runs two data accounts under its operator, plus the system
account:

- **EDGE** holds the per-edge source streams and the minted edge users; the
  edges' leaf nodes join it. A reflected publish an edge provokes lands here,
  where there is nothing central to address.
- **CENTRAL** holds the `device-lanes` and `edges` key-value buckets and
  `FLOWSEER_DEVICE_AUDIT`. Central writes them through its own CENTRAL-account
  connection; no edge credential is in this account.

`TestEdgeCredentialCannotReachCentralStreams` constructs the attack, direct
and reflected, and asserts the lane record is untouched. The two accounts get
separate JetStream disk budgets (half the store each by default), so
telemetry volume in EDGE cannot exhaust the store the journal writes into,
and each per-edge stream and the audit stream are bounded within their
account.

## Subjects and streams

```
flowseer.<tenant>.edge.<edge-id>.otel.{logs,metrics,traces}   the agent's OTLP bodies
flowseer.<tenant>.edge.<edge-id>.ingest.<source>.>            a future ingestion source
flowseer.<tenant>.edge.<edge-id>.source.>                     the hub's sourcing deliveries
flowseer.<tenant>.audit.device.<device-id>                    central's audit records (CENTRAL)
```

The edge's `EDGE_BUFFER` stream, in JetStream domain `edge-<edge-id>`, holds
the `otel` and `ingest` branches, file-backed, bounded by bytes and age with
the oldest record discarded first. For each attached edge the hub creates
`FLOWSEER_EDGE_<edge-id>` in the EDGE account, a stream that sources that
edge's buffer and nothing else, across its leaf link through
`$JS.edge-<edge-id>.API`, delivering on the `source` branch: that branch is
inside the edge's publish permission and outside its buffer's subjects, which
matters because JetStream refuses a consumer that would deliver into the
stream it reads. A `SubjectTransform` on the source restricts it to the
edge's own `otel` and `ingest` branches. One stream per edge is what makes a
record's edge a fact of where it is stored rather than of the subject it
carries; the forwarder relies on that below. A stream emptied by its age
bound re-sources its edge's whole buffer on a hub restart, so the age is set
comfortably longer than any forwarder outage.

A leaf without a distinct domain silently extends the hub's; `EdgeDomain` is
the guard.

## The credential an edge is minted

`Hub.MintEdgeUser` signs a user in the EDGE account with the minimal set that
lets the hub source that edge's buffer and nothing more:

| Direction | Allowed | Why |
| --- | --- | --- |
| publish | `flowseer.<tenant>.edge.<edge-id>.>` | the edge's own records and the sourcing consumer's deliveries |
| publish | `$JSC.R.>` | the reply the hub's source client attaches to its consumer-create request |
| subscribe | `$JS.edge-<edge-id>.API.>` | so that consumer-create request reaches the edge domain |
| subscribe | `$JS.FC.>` | the flow-control replies the sourcing consumer sends |

Each subject was found by bisecting a sourcing failure with a test, not read
from a document; a narrower set silently stalls sourcing with no warning on
either server, so the reason each survives is recorded here. The edge is
deliberately **not** granted publish on its own JetStream API: nothing that
crosses the link needs it, and it is the path that would let a caller read
the source consumer's delivery subject through `CONSUMER.INFO`. Nor any
`_INBOX` subject: the stock random inbox prefix matches none of these, and an
account-wide `_INBOX` grant would reach central's own request replies.
`TestEdgePermissionsConfineTheLeaf` proves a publish under another edge's
subtree never crosses and a subscription on `flowseer.>` imports nothing,
while sourcing still flows.

The hub's keys (operator, system, EDGE, CENTRAL) are generated into
`<StateDir>/keys` on first start and read back afterwards, so a restart keeps
every minted credential valid. A leaf writes its credential to
`<StateDir>/hub.creds`, mode 0600, because a remote leaf authenticates from a
file.

## What a compromised edge can and cannot do

Constructed and proven:

- It cannot write into the journal buckets or the audit stream, directly or
  through the flow-control reflection: those are in CENTRAL, and the edge
  credential is in EDGE (`TestEdgeCredentialCannotReachCentralStreams`).
- A record it publishes carrying a forged `$JS.ACK` reply that names another
  edge's subject is not stored under that subject: the account split, the
  per-source `SubjectTransform`, and one stream per edge keep every stored
  record under the edge's own subtree
  (`TestAnEdgeCannotStoreARecordAsAnotherEdge`).
- A forged record that did reach the aggregate would still not be shipped as
  another edge's: the forwarder reads the edge from the stream's name and
  drops a record whose subject is outside that edge's subtree, before posting
  (`TestForwarderRefusesARecordOutsideItsStreamsEdge`).
- A publish inside the subtree but on neither buffered branch is refused with
  `edgebus/leaf`, never acknowledged by nothing
  (`TestPublishOutsideTheBufferedBranchesFailsLoudly`).

The edge cannot learn its source consumer's delivery subject: it has no
publish on its own JetStream API, and the sourcing consumer does not appear
in the edge's own consumer listing. That unreachability is a defence, not a
guarantee; the account split and the forwarder refusal are the guarantees,
and they hold whatever the delivery subject is.

Residual, accepted: an edge can put arbitrary bytes into its own subtree,
which is exactly what it can publish anyway; those records are its own data
and are shipped as its own. A fake idle heartbeat can still force its own
source consumer to retry and slow its own sourcing, a self-inflicted delay of
one edge's telemetry with no reach beyond it.

## Observability

A refusal is the one sign that an agent is trying to relabel another edge's
data, so it is not a silent discard: the forwarder emits a WARN record, event
name `flowseer.edgebus.record.refused`, carrying the stream's edge, the edge
the subject claimed, the reason, and the subject, rate-limited to one per edge
stream every ten seconds so a misbehaving agent cannot flood the log; and it
counts `flowseer.edgebus.records.refused` labeled by reason
(`foreign_subject`, `not_otel`, `collector_rejected`, `max_deliver_exceeded`)
and signal, closed sets, since an edge id is an identity the cardinality rule
keeps off metric attributes. `Forwarder.Dropped` keeps the exact count for a
test.

Both embedded servers run with `NoLog`, so their own diagnostics would vanish;
`HubConfig.Logger` and `LeafConfig.Logger` route the server's warnings and
errors to the host logger, so a line like "JetStream out of space" reaches an
operator. `Leaf.BufferState` reports the local buffer's depth and
oldest-record age, which is what an agent surfaces as "buffering since ..."
when the hub is not draining it.

## Durability

Both servers declare their fsync policy with `service.BusFsyncPolicy` and go
through `service.NormalizeFsync`, the rule the process-local bus follows, so
the three embedded JetStream servers in the repository tell one durability
story. Central declares `BusFsyncPerMessage` for the hub, which is the
journal's authority; an edge declares `BusFsyncPeriodic` for its buffer, since
a record lost to a power cut there is a gap in history. An undeclared policy
refuses to start, before any key is written, with `edgebus/config`.

## OTLP over the bus

The edge's runtime exports OTLP/HTTP to `Receiver`, bound to `127.0.0.1` on a
kernel-chosen port so a collision cannot stop the agent. The runtime must
export protobuf with no compression: the receiver stores and forwards the
bytes unchanged, so it accepts only what it can pass on verbatim and answers
415 to a compressed or non-protobuf body. An agent host therefore pins
`Protocol` `http/protobuf` and leaves compression off rather than letting the
`OTEL_EXPORTER_OTLP_*` environment variables choose gzip or grpc. The receiver
publishes each body into the buffer and answers 200; a refused buffer answers
503 so the runtime retries. Central's `Forwarder` follows every edge's hub
stream with a durable consumer, discovering edges attached after it started on
an interval, and posts each body to central's collector endpoint,
acknowledging only on a 2xx; a 4xx other than 408 and 429 drops the body
rather than retrying a malformed payload forever, with a delivery-count
backstop behind it. `TestReceiverToForwarderCarriesBodiesUnchanged` checks the
bytes end to end, and `TestRecordsPublishedWhileTheHubIsDownArriveAfterReconnect`
is the evidence that sourcing across a leaf link survives the link dropping
and returning. After a hub restart the source takes about forty seconds to
re-establish, the server's own retry cadence; a record published meanwhile
waits in the edge's buffer and arrives when it does.

## TLS

The hub's WebSocket listener serves Connect's certificate. An edge verifies it
with `PinVerifier`, which accepts a leaf certificate whose SPKI SHA-256 digest
is among the anchors `EdgeProvisioning` and `EnrollResponse` carry, and both
of the edge's dialers, the Connect client and the leaf remote, install the
same verifier so they cannot drift apart. A non-loopback listener must serve
TLS; the hub refuses a plaintext listener on any other host. The WebSocket
leaf path needs the leaf-node subsystem enabled, which the server keys on a
leaf port, so the hub also binds a plain leaf listener on loopback; an edge
never dials it.

## What is deliberately absent

- Any dispatch, report, or audit subject. Those are Connect calls.
- Announce subjects and capability advertisement.
- A second tenant, and the per-account isolation and JWT revocation that go
  with it. The `<tenant>` subject token is present but carries no broker
  enforcement while one tenant runs; the EDGE and CENTRAL accounts are the
  isolation that exists today.
