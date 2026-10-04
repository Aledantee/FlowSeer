---
title: Central Intake - Plan
type: feat
date: 2026-10-04
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: fixes needed
execution: mixed
amends: docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md
parent: docs/plans/2026-10-02-2331-feat-central-ingestion-pipeline-plan.md
---

# Central Intake - Plan

> Implemented. 3 units, 2026-10-04T12:14Z to 2026-10-04T13:02Z.

## Goal

A syslog `IngestRecord` an edge publishes appears in a CENTRAL stream for
its record type, under the tenant the edge belongs to and the device it
names, with raw evidence moved to a stream that ages out in 24 hours. A
repeat inside the stream's duplicate window is stored once. The
means is three units: the central streams, their subjects, and a shared
edge follower in `edgebus`, an intake package in the device service that follows every edge stream, and
the host wiring with the record amendments. Stop condition: if intake
cannot read an edge's stream and publish into CENTRAL from one process
without an account import or export, the account boundary in
`src/modules/edgebus/README.md` is wrong for ingestion and the direction
record is re-opened first.

## Decisions

`E` is `src/modules/edgebus`, `I` is `src/services/device/internal/intake`,
`H` is `src/services/device/internal/host`, and `NS` is
`~/go/pkg/mod/github.com/nats-io/nats-server/v2@v2.15.0/server`, the version
`go.mod` pins.

- The parent plan's Decisions and the
  [direction record](../architecture/2026-10-02-central-ingestion-pipeline-direction.md)
  apply.
- A typed record is published on
  `flowseer.<tenant>.ingest.<type>.<device-id>`, and raw evidence on
  `flowseer.<tenant>.evidence.<type>.<device-id>`. Why: it mirrors
  `AuditSubject` (`E/subjects.go`), and a later consumer can filter to one
  device without reading the stream. (decided by the user, 2026-10-04)
- One stream per record type, `FLOWSEER_INGEST_<TYPE>`, holds
  `flowseer.*.ingest.<type>.*`. One stream, `FLOWSEER_INGEST_EVIDENCE`,
  holds `flowseer.*.evidence.>`. Why: the direction record names one
  CENTRAL stream per record type, and one evidence stream gives raw payload
  a single retention rule.
- The evidence stream keeps a message 24 hours and is capped by bytes, with
  the oldest discarded first. Why: evidence then lives no longer than the
  per-edge stream it came from, whose default age is 24 hours
  (`E/hub.go`, `defaultEdgeStreamMaxAge`). (decided by the user, 2026-10-04)
- This phase builds no input for a centrally hosted adapter. Why: the tree
  holds no such adapter, so its tenant resolution would have no caller to
  test against. Intake's check and republish step takes the tenant, the
  edge, and the envelope as arguments, so that input is a later unit.
  (decided by the user, 2026-10-04)
- Intake lives in `I`, not under `src/modules/`. Why: `src/modules/README.md`
  admits a package only when two hosts assemble it, and the device service
  is the one host that runs the hub.
- The hub creates the streams and `E` owns their names and subjects. Why:
  `Hub.createStores` already creates every CENTRAL stream and bucket, and
  the CENTRAL disk budget it must fit is set in the same file. A stream's
  positive `MaxBytes` counts against its account's disk limit: stream
  creation on a standalone server sums it in `tieredReservation`
  (`NS/stream.go:931-940`, `NS/jetstream_api.go:1510-1524`) and
  `checkBytesLimits` refuses what does not fit (`NS/jetstream.go:2584-2607`).
- Intake reads the tenant and the edge from the stream. It takes the tenant
  from `Hub.EdgeTenant` and refuses a record whose subject does not start
  with `EdgeSubtree(tenant, edge) + ".ingest."`. Why: the same rule as the
  forwarder's `belongsToEdge`, built from the exported `EdgeSubtree` so `E`
  exports nothing new for it.
- Intake refuses a record whose `provenance.edge` is unset or names another
  edge. Why: `Provenance.edge` unset means a cloud-mediated answer
  (`spec/proto/flowseer/model/inventory/v1/provenance.proto`), which cannot
  arrive on an edge stream, and the edge source always sets it
  (`src/edge/agent/internal/syslogsource/mapper.go`).
- The record type is the payload arm, never the edge subject's source token.
  Why: central registers per record type, and the token after `ingest.` is
  the edge's vocabulary (`E/subjects.go`, `EdgePublishSubjects`).
- The device id in the subject is the one the payload names. For syslog it
  is `SyslogRecord.device`, a required UUID, so it is one subject token.
- A record with raw evidence is published twice: the bytes as received to
  the evidence subject, then the envelope with `raw` cleared to the typed
  subject. A record without raw evidence is republished as received. Both
  publishes carry `<tenant>.<record_id>` as the NATS message id, and
  intake acknowledges after the last one. Why evidence first: a failure
  between the two redelivers into a stream that drops the repeat. Why the
  tenant in the id: the central streams hold every tenant, the server
  matches a duplicate on the id alone and acknowledges it without comparing
  subject or body (`NS/stream.go:5553-5557`, `:6953-6970`), and `record_id`
  is an edge's own UUID. Without the tenant, one tenant's edge could
  publish another tenant's pending id first and suppress that record.
- Delivery to the central streams is at least once. Each stream drops a
  repeated message id inside a 10 minute duplicate window
  (`NS/stream.go:6953-6970`), the audit stream's value. Past that window a
  repeat is stored again, which happens when a hub restart re-sources an
  edge buffer (`E/README.md`, Subjects and streams). Why not a longer
  window: the server keeps every id of the window in a map until a timer
  purges it (`NS/stream.go:5614-5625`, `:5562-5571`) and refuses a window
  longer than the stream's age (`NS/stream.go:2008`). The history sink and
  the state projector therefore deduplicate on tenant and `record_id`,
  which the direction record gains as an amendment.
- A refused record is terminated, never redelivered. A publish that fails is
  redelivered after a delay with no delivery limit. Why: a refusal is about
  the record and a retry cannot change it. A failed publish is about
  CENTRAL, and dropping records while the central store is full would lose
  what the edge buffered. The per-edge stream's age and byte bounds still
  end the wait: that stream discards its oldest message at either bound
  whether or not intake acknowledged it (`E/hub.go`, `createEdgeStream`).
  `Term` clears the pending and redelivery state with no delivery-limit
  condition (`NS/consumer.go:3333-3335`, `:3796-3802`).
- Intake follows edges as the forwarder does: one durable consumer per edge
  stream, found through `Hub.AttachedEdges` on an interval. The discovery
  loop, the consumer map, and the drain on close move out of the forwarder
  into one `edgebus` type both callers use. Why: `docs/code-style.md` says
  logic that appears twice is extracted, and intake is the second caller.
- Ruled: a delivery from an edge `Hub.EdgeTenant` reports no tenant for is
  retried after the retry delay, not refused. Why: a refusal is about the
  record, a missing tenant is about the hub's state, and `Term` drops the
  record for good. Cost if wrong: the handler's first branch, one test, and
  one README sentence in `I`.
- Ruled: `flowseer.intake.records.duplicate` adds one per delivery when any
  of its publishes reports a duplicate. Why: the unit counts each instrument
  once per delivery, and a record with evidence makes two publishes. Cost if
  wrong: the counter's call site and one test in `I`.

## Requirements

1. A valid envelope in an edge stream appears in `FLOWSEER_INGEST_SYSLOG`
   on `flowseer.<tenant>.ingest.syslog.<device-id>`, with the tenant from the
   edge's attachment, and a repeat inside the duplicate window is stored
   once. A tenant is `default` or a canonical UUID
   (`src/common/tenant/tenant.go`), so the examples use tenant
   `0198a3c0-0000-7000-8000-0000000000aa`, written `<T>`. Example: an envelope for device
   `0198a3c0-0000-7000-8000-000000000001` published by a leaf attached under
   `<T>` is the stream's one message on
   `flowseer.<T>.ingest.syslog.0198a3c0-0000-7000-8000-000000000001`, and
   delivering the same edge message to intake a second time leaves the
   stream at one message.
2. An envelope whose subject is outside the edge's `ingest` branch is
   refused and counted. Example: a message stored in edge A's stream under
   `flowseer.<T>.edge.<B>.ingest.syslog` reaches no central stream and
   counts one refusal with reason `foreign_subject`.
3. An envelope that cannot be decoded or fails validation is refused,
   counted by reason, and terminated, so it is not delivered again.
   Example: three bytes `0xff 0xff 0xff` count one `malformed`, a decoded
   envelope with an empty `record_id` counts one `invalid`, the consumer
   then reports no pending acknowledgement, and neither is delivered again
   after the acknowledgement wait.
4. An envelope whose `provenance.edge` is not the stream's edge is refused
   with reason `foreign_provenance`. Example: edge A publishes, on its own
   subject, an envelope naming edge B in its provenance, and no central
   stream stores it.
5. Raw evidence never enters a typed stream. Example: an envelope with
   `raw.data` of 12 bytes and reason `RAW_REASON_PARSE_FAILURE` yields one
   message in `FLOWSEER_INGEST_EVIDENCE` whose bytes equal the envelope as
   published, and one message in `FLOWSEER_INGEST_SYSLOG` that decodes with
   `raw` unset and every other field equal.
6. A publish that fails is retried for as long as the edge stream still
   holds the record. Example: with the central publish failing twice and
   then succeeding, the record is stored once and the retry counter reads
   two.
7. The record type is the payload arm. Example: a valid syslog envelope
   published on the edge subject `ingest.other` arrives on
   `flowseer.<T>.ingest.syslog.<device-id>`.
8. Two tenants may use the same `record_id`. Example: edges of two tenants
   each publish an envelope with the same UUID and different bodies, and the
   syslog stream holds two messages, one per tenant subject.
9. The evidence stream's configuration reads a 24 hour maximum age, a byte
   limit, and discard-old. Example: `StreamInfo` on a fresh hub returns
   `MaxAge` of 24 hours.

## Out of scope

- The input for a centrally hosted adapter (Decisions).
- Any consumer of the central streams. Phases 3 and 5 add them.
- Whether the device a record names is hosted by the edge that sent it
  (Open questions).
- A record type this central build does not know. Its payload arm decodes
  as an unknown field, the required `oneof` is then empty, and the record
  is refused as `invalid`.
- Throughput tuning. Intake publishes one record at a time and waits for
  each store acknowledgement.
- Input trust: intake reads bytes an edge wrote, and an edge is not trusted
  (`E/README.md`, "What a compromised edge can and cannot do"). Decoding,
  validation, and the checks above are the whole defence, and a record that
  passes them is taken as that edge's statement about its own tenant.

## Units

### U1. Central ingest streams, subjects, and the edge follower
Files: src/modules/edgebus/subjects.go, src/modules/edgebus/hub.go, src/modules/edgebus/follower.go, src/modules/edgebus/follower_test.go, src/modules/edgebus/forwarder.go, src/modules/edgebus/README.md, src/modules/edgebus/edgebus_test.go, src/modules/edgebus/storage_test.go, docs/solutions/architecture-patterns/per-account-jetstream-disk-budgets-reserve-against-the-server-store-ceiling.md
After: none
Change: `subjects.go` gains `IngestRecordTypeSyslog = "syslog"`,
`IngestRecordTypes()` returning the known types,
`CentralIngestSubject(tenant, recordType, deviceID)`,
`EvidenceSubject(tenant, recordType, deviceID)`,
`IngestStream(recordType)` returning `FLOWSEER_INGEST_` plus the type in
upper case, and `EvidenceStream = "FLOWSEER_INGEST_EVIDENCE"`.
`Hub.createStores` creates one file-backed limits stream per record type
on `flowseer.*.ingest.<type>.*` and the evidence stream on
`flowseer.*.evidence.>`, each with a 10 minute duplicate window and
discard-old. `HubConfig` gains `IngestMaxBytes` (zero means 256 MiB per
type), `IngestMaxAge` (zero means 24 hours), `EvidenceMaxBytes` (zero means
64 MiB), and `EvidenceMaxAge` (zero means 24 hours). `defaultCentralBudget`
rises from 512 MiB to 1 GiB, since the audit stream's 256 MiB and the two
new defaults reserve 576 MiB and the four buckets set no `MaxBytes`. The
comment on the budget constants, the README's account and subject
sections, and the solution's figures change with it. `follower.go` adds
`FollowEdges(ctx, hub, interval, attach)`, where `attach` takes an edge id
and returns that edge's `jetstream.ConsumeContext`. It attaches every
edge in `Hub.AttachedEdges` that has no consumer, repeats on the interval,
and its `Close` stops discovery and drains every consumer. The forwarder
keeps its consumer configuration and its `forward` handler and uses the
follower for the rest, with no change in behaviour.
Tests: in `edgebus_test.go`, `TestCentralIngestStreamsExistWithTheirLimits`
reads `StreamInfo` for both streams and compares subjects, `MaxAge`,
`MaxBytes`, discard policy, and duplicate window (R9).
`TestCentralIngestSubjectsDoNotOverlap` publishes one message on each of
the audit, typed, and evidence subjects and on an edge-shaped subject
`flowseer.<T>.edge.<id>.ingest.syslog` through the CENTRAL connection, and
asserts each of the first three lands in its own stream only and the fourth
in none. In `follower_test.go`, `TestFollowerAttachesAnEdgeAttachedLater`
and `TestFollowerCloseDrainsEveryConsumer`. The forwarder's existing tests
pass unchanged, which is the evidence the move changed nothing. In
`storage_test.go`, `startStorageTestHub` sets `CentralBudgetBytes` to
512 MiB, under the new reservation, so it is given a budget that fits, and
`TestAttachEdgeRefusedPastStoreCeiling` keeps its meaning with it.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/edgebus docs/solutions/architecture-patterns/per-account-jetstream-disk-budgets-reserve-against-the-server-store-ceiling.md`

### U2. Intake package
Files: src/services/device/internal/intake/
After: U1
Change: `intake.Start(ctx, Config)` returns an `*Intake` that follows every
attached edge through `edgebus.FollowEdges`, and `Close` drains it.
`Config` holds `Hub` (`*edgebus.Hub`), `Central` (an interface with the
one `Publish` method of `jetstream.JetStream` that intake calls, so a test
can fail it), `RetryDelay` (zero means five seconds), `DiscoveryInterval`
(zero means ten seconds), `Logger`, and `MeterProvider`. Each edge gets
the durable consumer `ingest_intake` filtered to
`flowseer.*.edge.*.ingest.>` with explicit acknowledgement, a 30 second
acknowledgement wait, and no delivery limit. The filter is wider than the
edge's own subtree on purpose, so a foreign subject is delivered, refused,
and counted. Per message, in this order: the subject check,
`proto.Unmarshal`, `protovalidate.Validate`, the provenance edge check,
then the publishes the Decisions describe. A refusal calls `Term`, a
publish error calls `NakWithDelay(RetryDelay)`, and the last publish is
followed by `Ack`. The check and republish step is one function of tenant,
edge, subject, and bytes that returns the publishes to make or a refusal
reason, with no JetStream type in its signature. Instruments, scope
`go.aledante.io/FlowSeer/src/services/device/internal/intake`, each
counted once per delivery at the point named:
`flowseer.intake.records.republished` at `Ack`, by
`flowseer.intake.record_type`. `flowseer.intake.records.duplicate` when a
publish acknowledgement reports `Duplicate`
(`github.com/nats-io/nats.go@v1.54.0/jetstream/publish.go:145-147`), by
record type. `flowseer.intake.evidence.stored` at the evidence publish
acknowledgement, by record type. `flowseer.intake.records.refused` at
`Term`, by `flowseer.intake.reason` (`foreign_subject`, `malformed`,
`invalid`, `foreign_provenance`). `flowseer.intake.records.retried` at
`NakWithDelay`. These five are counters in `{record}`. Two histograms in
`s`: `flowseer.intake.record.duration`, from delivery to the final `Ack`,
`Term`, or `Nak`, and `flowseer.intake.record.age`, from the message's
stream timestamp in its metadata to delivery, which is how far intake is
behind the edge stream. A refusal logs WARN with event name
`flowseer.intake.record.refused`, the stream's edge, the reason, and the
subject, at most once per edge every ten seconds. A `README.md` states the
checks, the two publishes, the delivery guarantee with its two bounds, and
the instruments.
Tests: `intake_test.go` starts a real hub and leaves from `edgebus` under
tenant `<T>` and reads the central streams back.
`TestValidRecordReachesItsTypedStream` (R1).
`TestRedeliveredRecordIsStoredOnce` (R1), handing the same delivered
message to the handler twice and asserting one stored message and one
duplicate count. `TestForeignSubjectIsRefused` (R2) calls the handler with
a fake message carrying the foreign subject, as
`TestForwarderRefusesARecordOutsideItsStreamsEdge` does
(`src/modules/edgebus/attribution_test.go`). The hub's edge stream has no
subjects of its own and the leaf buffer takes only its own subtree, so no
test here stores a foreign subject in a real edge stream, and nothing
covers the consumer filter delivering one.
`TestRefusedRecordsAreTerminated` (R3) publishes the two bad records
through a leaf with a short acknowledgement wait, asserts the reason
counts, reads `NumAckPending` of zero from the consumer's info, and
asserts the handler saw each record once after two waits have passed.
`TestForeignProvenanceIsRefused` (R4).
`TestRawEvidenceIsMovedToTheEvidenceStream` (R5), comparing evidence
bytes with the published bytes and the typed message with `proto.Equal`
after clearing `raw` on the expected value.
`TestTypedPublishFailureAfterEvidenceRedelivers` (R5, R6): `Central`
accepts the evidence publish and fails the typed one, the source message
stays pending, and after redelivery each central stream holds one message
and the typed one has no `raw`. `TestFailedPublishIsRetried` (R6) fails
the first two publishes. `TestStoredPublishWithLostAckIsNotStoredTwice`
(R6): `Central` stores the message and returns an error, and the retry
leaves one message. `TestRecordTypeComesFromThePayload` (R7) publishes
through the leaf on `IngestSubject(<T>, edge, "other")`.
`TestSameRecordIDInTwoTenantsStoresBoth` (R8) attaches a second edge under
a second tenant UUID, then repeats each publish and asserts two messages.
`TestInstrumentsReportEachOutcome` reads a manual metric reader after one
republish, one duplicate, one refusal, and one retry. Each refusal test
changes only the refused property of an otherwise valid envelope
(`docs/solutions/conventions/a-refusal-test-needs-an-input-only-the-refusal-rejects.md`).
Nothing here covers a repeat past the duplicate window or a record the
edge stream evicted before intake stored it. The first is the sinks' to
handle and is tested with them.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/internal/intake`

### U3. Host wiring and record amendments
Files: src/services/device/internal/host/host.go, src/services/device/internal/host/host_test.go, src/services/device/README.md, docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md, docs/architecture/2026-08-20-device-service-and-inventory-direction.md, docs/plans/2026-10-02-2331-feat-central-ingestion-pipeline-plan.md
After: U2
Change: `H/host.go` adds the module `intake` after `forwarder`, with no
gate, whose `Setup` awaits the hub handle, calls `intake.Start`, closes it
when the context is already cancelled, and closes it when the runner ends,
as `setupForwarder` does. The ingestion direction record gains one dated
amendment that states the central subject shapes and stream names, the
at-least-once guarantee with deduplication on tenant and `record_id` at
each sink,
the 24 hour evidence age, and that the central adapter input is not built.
It names the landed work by date and scope and links no plan. The device
service record's Events bullet names the edge `ingest.<source>` branch and
the central per-record-type subjects in place of
`events.<tenant>.<integration>.>`. The service README lists the module.
The parent plan's Open questions drop the evidence stream's retention.
Tests: in `host_test.go`, `TestIntakeRepublishesAnEdgeRecord` runs the
host, takes the hub from `Options.Hub`, attaches an edge, publishes one
valid envelope through a leaf, and reads it from `FLOWSEER_INGEST_SYSLOG`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md docs/architecture/2026-08-20-device-service-and-inventory-direction.md docs/plans/2026-10-02-2331-feat-central-ingestion-pipeline-plan.md`

Waves: U1 | U2 | U3

The chain is forced: U2 imports the names and the follower U1 adds, and U3
imports U2.

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- src/modules/edgebus src/services/device docs/architecture docs/solutions/architecture-patterns docs/plans/2026-10-02-2331-feat-central-ingestion-pipeline-plan.md
go test -race ./src/modules/edgebus/... ./src/services/device/...
```

No lab check. The path from a datagram to the central stream is covered by
the phase 1 source tests and `TestIntakeRepublishesAnEdgeRecord` together.

## Definition of done

- [x] Verifier green for every changed path.
- [x] `src/modules/edgebus/README.md`, the intake README, and the device
      service README describe the streams, subjects, and module.
- [x] Both direction records carry their amendment, and the budget solution
      states the new default.
- [x] This plan's `status` is set with an outcome note under its title, and
      the parent's `Landed:` line for this phase holds the commit range.
- [x] No plan labels in code.

## Open questions

- Unconfirmed: the typed stream's limits, 256 MiB and 24 hours per record
  type. They bound a transit stream between intake and the sinks, and the
  parent leaves retention per record type open. Phase 3 revisits them with
  the sink's measured lag.
- An edge can publish a record naming any device id under its own tenant.
  Intake does not check the device against the edge's listing, because a
  record buffered before a device moved edges would then be refused after
  the move. Whether a sink needs that check, and against what, is decided
  with the first consumer that acts on a record.

## Review gaps

- src/services/device/internal/intake/intake.go:198: delete the final `msg.Ack()`; fails: a delivered valid record whose message is asserted acknowledged
- src/services/device/internal/intake/intake.go:252: check the prefix `EdgeSubtree(tenant, edge) + "."` without `ingest.`; fails: a subject inside the edge's subtree and outside its `ingest` branch
- src/services/device/internal/intake/intake.go:262: accept an unset `provenance.edge`; fails: an otherwise valid envelope with no edge in its provenance
- src/services/device/internal/intake/intake.go:270: build the message id from the edge id, not the tenant; fails: a published record whose stored `Nats-Msg-Id` is read back
- docs/architecture/2026-08-20-device-service-and-inventory-direction.md:338: "The Execute and Events bullets below record the earlier shape", while the Events bullet now states the current subjects
- docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md:25: "Nothing central reads the `ingest` branch", while intake follows it
- docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md:50: "The record id is also the NATS message id", while the central id is `<tenant>.<record_id>`
- docs/solutions/architecture-patterns/per-account-jetstream-disk-budgets-reserve-against-the-server-store-ceiling.md:160: a 512 MiB central budget with default stream limits, which reserve 640 MiB and fail hub start, and `hub.go` line cites the merge shifted
- src/services/device/internal/intake/README.md:29: the diagram draws the evidence and typed publishes as alternatives, while the code publishes evidence and then the typed record
- src/services/device/internal/host/host_test.go:1793: `//nolint:gosec`, a new suppression for a linter that is not enabled
