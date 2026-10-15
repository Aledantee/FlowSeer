---
title: Edge Delivery over Connect, Phase 2, Delivery Calls and Central Handlers - Plan
type: feat
date: 2026-10-04
artifact_contract: flowseer-plan/v2
execution: mixed
amends: docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md
---

# Edge Delivery over Connect, Phase 2, Delivery Calls and Central Handlers - Plan

## Goal

Central accepts ingest records and OTLP bodies from an edge through two
Connect calls and handles them as it handles the same traffic from the
per-edge streams today. The means are two services under
`spec/proto/flowseer/edge/`, an ingest handler in
`src/services/device/internal/intake` that shares the stream consumer's
record preparation, and a telemetry handler that shares the forwarder's
collector post. The leaf path keeps working beside them until the last phase.

Stop condition: the plan is wrong if the ingest handler cannot publish a
batch without the per-edge stream, for example because a record's tenant or
subject can only be read from the hub.

## Decisions

The parent's decisions apply
([parent plan](2026-10-04-2232-refactor-edge-delivery-over-connect-plan.md)).
`I` is `src/services/device/internal/intake`, `E` is `src/modules/edgebus`,
`H` is `src/services/device/internal/host`. This phase adds:

- The calls are `flowseer.edge.ingest.v1.IngestService.Deliver` and
  `flowseer.edge.telemetry.v1.TelemetryService.Deliver`. `IngestService`
  joins `RawWindowEdgeService` in the existing `edge/ingest/v1` package.
  Why: the audit call is `flowseer.edge.audit.v1.AuditService.Deliver`, one
  package per concern, and `edge/ingest/v1` already holds the edge's
  ingestion calls (`spec/proto/flowseer/edge/ingest/v1/README.md`).
- Each response carries `settled`, the count of leading items central
  holds or refused for a reason about the item. A failure that is not about
  an item stops the count there and the call still answers. Why: intake
  already separates a refusal from a retry (`I/README.md`), and the edge
  needs to know where to resume.
- `edge/ingest` imports `integration/ingest` and carries `IngestRecord`
  typed. `test/conformance/proto/layering_test.go` admits that edge. Why:
  `edge/audit` carries `DeviceOperationEvent` typed the same way.
- Each request's item field has `min_items: 1` and
  `items: {ignore: IGNORE_ALWAYS}`, and each handler validates every item
  itself. Why: the edge chain's `ValidatingInterceptor` validates the whole
  request (`H/validation.go`, `WrapUnary`), so one invalid record would
  refuse the batch and the edge could never get past it. Protovalidate
  v1.4.0 returns before building any rule for a value whose ignore is
  `IGNORE_ALWAYS`, embedded message rules included
  (`~/go/pkg/mod/buf.build/go/protovalidate@v1.4.0/builder.go:280-282`),
  and builds a repeated field's items through that path
  (`builder.go:668-678`), so the container rule still applies.
- No request carries a count bound. The middleware's 1 MiB body bound
  (`H/host.go:79-82`, `maxEdgeBody`) bounds the work, and it stays as it
  is. Why: the middleware refuses a compressed body and reads at most
  `maxBody` bytes (`src/services/device/internal/edgeapi/middleware.go:107-118`).
  A segment is 512 KiB by default including its 20-byte header, and each
  record costs 8 header bytes in the segment
  (`src/edge/agent/internal/seglog/log.go:26-30`, `format.go:24-25`). A
  repeated message item costs one tag byte and at most three length bytes
  under 2 MiB, so one segment's records encode in less than the segment's
  size. A count bound low enough to matter would refuse a segment of small
  records, which the edge could not split.
- `MaxRecordBytes` stays at 256 KiB. The loopback receiver accepts OTLP
  bodies up to 255 KiB, and a `TelemetryExport` body has `max_len`
  261120 (255 KiB). Why: the decided record limit below. A 261120-byte body with
  its signal encodes in 261126 bytes, under the 262144-byte record bound.
- The record preparation, the publication list, and the message id stay in
  `I`. `prepare` splits into the stream's subject and unmarshal checks and
  a `prepareRecord` that both the stream consumer and the handler call. The
  handler lives in `I` beside the consumer. Why: both paths must refuse and
  publish alike while both exist, and the device service is the only host,
  so the code stays in its `internal/` (`src/modules/README.md`,
  Admission).
- The ingest handler takes the edge from the assertion and the tenant from
  the edge store, through the `edgeIDFromContext` and
  `busResources.edgeTenant` functions `H` already hands `dispatchapi`
  (`H/host.go`, `buildResources`). It keeps intake's refusal of a record
  whose provenance names another edge (`I/intake.go`,
  `reasonForeignProvenance`).
- The handler does not share the stream consumer's full-stream gate. A full
  typed stream (`edgebus.IsStreamFull`) stops the count, and the edge's
  backoff spaces the retry. Why: the gate lives in the `Intake` the intake
  module owns, and the connect module holds no handle to it. Sharing it would
  tie the handler to each intake attempt (`H/host.go`, `modules`).
- The collector post and its status rules move out of `Forwarder.forward`
  into an exported `edgebus.Collector`. Its `Post` returns one of four
  outcomes: accepted, refused, retry for a configuration answer, retry for
  an unavailable collector. The forwarder maps them to its acknowledgement,
  termination, and retry reasons as today. Why: the forwarder and the
  telemetry handler must classify an answer alike, and the forwarder lives
  in `E`.
- `NewCollector` refuses an endpoint that is not an absolute `http` or
  `https` URL, and `StartForwarder` applies the same check. Why: today a
  malformed endpoint fails every request build and the forwarder refuses
  every body as `collector_rejected` (`E/forwarder.go:185-189`). Under the
  handler that would settle and drop every body on a central configuration
  error, against the decided no-collector rule. The forwarder now fails to
  start instead, the one forwarder behavior this phase changes.
- The handlers' metric and log keys follow
  `docs/conventions/observability.md`. `flowseer.device.signal` moves into
  `src/common/fsconv`, since `otlpapi` becomes the second tree to emit it.
  The telemetry handler counts forwarded items beside refused ones, so a
  refusal has a population. The intake instruments the ingest handler
  shares get descriptions that cover both paths. Each handler keeps its own
  per-edge ten-second log limiter, since the forwarder's lives in
  `Forwarder` state.
- The telemetry handler lives in a new package
  `src/services/device/internal/otlpapi`. Why: it has one host, and
  `src/services/device/internal/telemetry` already names the service's own
  telemetry view.
- The telemetry handler settles an accepted body and a body the collector
  refused for its content (a 4xx other than 401, 403, 404, 408, and 429). It
  stops the count at any other answer or a transport error. It applies no
  delivery backstop, so a body waits in the edge log until a collector
  accepts it or the log's bounds expire it. Why: the forwarder's backstop
  exists because a stream cannot hold a body forever (`E/forwarder.go`,
  `forwarderMaxDeliver`), and the decided no-collector rule below keeps
  telemetry in the edge log.
- Both handlers mount on the edge listener's mux behind the assertion
  middleware, as the audit service does (`H/serve.go`, `mux`). The
  telemetry handler mounts whether or not a collector is configured. Why:
  the central high availability plan's first phase landed (commits
  `6890cdee..288cb9dc`, retired in that plan's state), so the edge listener
  is separate from the API listener (`H/host.go`, `setupConnect`). An
  unmounted call would answer `unimplemented`, which the edge cannot tell
  from a central that predates the call.

- Keep `MaxRecordBytes` at 256 KiB. Reduce telemetry receiver acceptance so each accepted body plus its stored envelope fits that record limit. Why: accepted telemetry must fit a segment and delivery call. (decided by the user, 2026-10-10)

- With no collector configured, return `settled: 0` and retain telemetry until a collector is configured or log bounds expire it. Why: missing configuration must not silently discard accepted telemetry. (decided by the user, 2026-10-10)

- The ingest benchmark measures and reports records per second on the host
  that runs it, with no pass or fail threshold. The user sets the threshold
  from the measured rate before this phase is reviewed. Why: no record
  states a sustained rate, a burst duration, or a recovery deadline, and a
  threshold chosen before a measurement would be a guess. (decided by the
  user, 2026-10-14)

- Full verification needs no repair to the verifier first.
  `uv run tools/scripts/run.py verify change --print-selection --
  ./generated/go/yang/foo.go` selects `module=generated/go/yang
  mode=build-sample-lint`, the same as the path without `./`, so the
  prefixed path no longer starts a broad race run. Why: the resumed worker
  merges `main`, which holds that routing.

## Requirements

The parent's requirements 4, 5, and the central half of 6 are claimed here.

1. A delivered record is published once per typed stream under
   `<tenant>.<record_id>`. Example: one `IngestRecord` with a syslog payload
   and raw evidence yields one message in `FLOWSEER_INGEST_SYSLOG` and one
   in `FLOWSEER_INGEST_EVIDENCE`, each with `Nats-Msg-Id` `<T>.<record_id>`.
2. A record that fails validation is settled and counted as refused.
   Example: a batch of three whose second record has no provenance answers
   `settled: 3` with the other two published and one `invalid` refusal
   counted.
3. A publish failure stops the count. Example: with the central publisher
   failing at the second record, the answer is `settled: 1`, and with the
   typed stream full at the first, `settled: 0` and one `stream_full`
   retry counted.
4. Central takes the tenant from its edge store. Example: an edge whose
   store tenant is `A` delivers a record, and the record is read back from
   `flowseer.A.ingest.syslog.<device-id>`. An edge store lookup error
   answers `unavailable` and publishes nothing.
5. A batch delivered twice is stored once. Example: the same request sent
   twice answers `settled: 1` both times and leaves one message in the
   typed stream.
6. A telemetry body reaches the collector unchanged. Example: the bytes the
   collector double receives on `/v1/logs` equal the bytes in the request,
   with content type `application/x-protobuf` and the configured headers.
7. The collector's answer maps as the forwarder maps it. Example: 400
   settles and counts the body as refused, and in a batch of three where
   the second post gets 503 the answer is `settled: 1` and the collector
   saw two posts.
8. With no collector configured, the telemetry call answers `settled: 0`
   and posts nothing. Example: a host started without a telemetry endpoint
   answers a batch of one body with `settled: 0`.
9. A collector outage does not affect the ingest call. Example: with the
   collector answering 503, an ingest delivery still answers `settled: 1`.
10. The receiver refuses a body over 255 KiB. Example: a 261121-byte export
    body answers 413.
11. A benchmark delivers batches of 256-byte syslog records to the ingest
    handler against an embedded hub with per-message fsync and reports
    records per second.

## Out of scope

- Any change to the agent, which the third phase makes. The receiver's
  bound is the one agent-facing change, and it lives in `E`.
- Removing the follower, the forwarder, its consumers, or `AttachBus`, which
  the last phase does.
- Sharing the full-stream gate between the stream consumer and the handler.

## Units

### U1. Delivery call schemas
Files: docs/architecture/2026-08-20-network-model-structure-direction.md, spec/proto/flowseer/edge/ingest/v1/ingest_service.proto, spec/proto/flowseer/edge/ingest/v1/README.md, spec/proto/flowseer/edge/telemetry/v1/telemetry_service.proto, spec/proto/flowseer/edge/telemetry/v1/README.md, spec/proto/flowseer/edge/README.md, spec/proto/flowseer/integration/ingest/v1/README.md, spec/proto/flowseer/integration/README.md, test/conformance/proto/layering_test.go, generated/go/proto/flowseer/edge/ingest/v1/, generated/go/proto/flowseer/edge/telemetry/v1/
After: none
Change: `IngestService.Deliver` takes `DeliverRequest { repeated
flowseer.integration.ingest.v1.IngestRecord records = 1 }` with
`repeated: {min_items: 1, items: {ignore: IGNORE_ALWAYS}}`, and answers
`DeliverResponse { uint32 settled = 1 }`. `TelemetryService.Deliver` takes
`repeated TelemetryExport exports = 1` with the same rules and answers the
same response shape. `TelemetryExport` holds `TelemetrySignal signal = 1`
(required, defined only, `not_in: [0]` as `RawEvidence.reason` does in
`spec/proto/flowseer/integration/ingest/v1/ingest_record.proto`) and
`bytes body = 2` (required, `max_len`
261120). `TelemetrySignal` is `TELEMETRY_SIGNAL_UNSPECIFIED`, `_LOGS`,
`_METRICS`, `_TRACES`, declared in the telemetry package. The comments state
the settled rule, that a request names no tenant or edge, and that items
are validated by the handler. The layering map gains
`"edge/ingest": {"model/rawwindow", "integration/ingest"}` and
`"edge/telemetry": nil`, and the READMEs' Imports and Imported by lines and
the edge root's package list follow. The edge root README's sentence that
`edge/audit` holds the tree's only edge from one boundary consumer to
another names `edge/ingest` as the second. The network model structure
record gains a dated amendment naming `edge/telemetry/v1` and the
`edge/ingest` to `integration/ingest` edge, as its 2026-10-12 amendment
does for the raw window packages. Generated with
`go tool -modfile=tools/buf/go.mod buf generate`.
Tests: `go tool -modfile=tools/buf/go.mod buf lint` passes, and
`test/conformance/proto` passes with the new edges. A test that the
interceptor admits a batch holding an invalid item is in U4, since only a
served call shows it.
Verify: `uv run tools/scripts/run.py verify change -- docs/architecture/2026-08-20-network-model-structure-direction.md spec/proto/flowseer/edge spec/proto/flowseer/integration test/conformance/proto generated/go/proto/flowseer/edge`

### U2. Ingest handler and benchmark
Files: src/services/device/internal/intake/intake.go, src/services/device/internal/intake/handler.go, src/services/device/internal/intake/errors.go, src/services/device/internal/intake/errors_test.go, src/services/device/internal/intake/handler_test.go, src/services/device/internal/intake/handler_bench_test.go, src/services/device/internal/intake/README.md
After: U1
Change: `prepare` keeps the subject and unmarshal checks and calls
`prepareRecord(tenantID, edgeID, record, data, validate)`, which holds the
validation, provenance, record type, evidence split, and message id. The
stream consumer's behavior is unchanged. `NewHandler(HandlerConfig)` takes
`Central Publisher`, `EdgeID func(context.Context) (string, error)`,
`EdgeTenant func(context.Context, string) (string, error)`, `Logger`, and
`MeterProvider`, and returns a handler for `ingestv1connect.IngestServiceHandler`.
`Deliver` resolves the edge and the tenant and wraps a failure in the
package codes `ErrCodeEdge` and `ErrCodeResolve`, which
`intake.ClientErrors` maps to `unauthenticated` and `unavailable`, as
`auditapi` does. The lookup's `edgestore.ErrCodeUnknownEdge` is wrapped, so
it never reaches the edge as `internal`. It then walks the records in
order. Each record is marshaled, prepared, and published typed first and
evidence second with `jetstream.WithMsgID`. A refusal settles the record and
counts `flowseer.intake.records.refused` with its reason. A validator
failure that is not a rule violation, a marshal failure, and a publish
failure stop the walk and count `flowseer.intake.records.retried` with
`validation`, `marshal`, `stream_full`, or `publish_failed`. A stored record
counts `republished`, and a duplicate acknowledgement counts `duplicate`.
The handler records `flowseer.intake.record.duration` per record and no
age, since it has no stream metadata. The `retried` and `duration`
descriptions say they cover records left for redelivery and time to the
final outcome on either path. Refusal logs use the consumer's per-edge
ten-second limit. The README draws
both paths into the shared preparation and states the settled rule.
Tests: in `handler_test.go`, reusing `startHub` and `validEnvelope` from
`intake_test.go` without editing it.
`TestDeliveredRecordReachesTypedAndEvidenceStreams` (R1, reads the
`Nats-Msg-Id` header as a literal `<T>.<record_id>`).
`TestInvalidRecordIsSettledAndRefused` (R2).
`TestForeignProvenanceIsSettledAndRefused`.
`TestPublishFailureStopsTheCount` and `TestFullTypedStreamStopsTheCount`
(R3, the second with a double returning the error `IsStreamFull` reads).
`TestTenantComesFromTheEdgeStore` and `TestTenantLookupErrorIsUnavailable`
(R4). `TestBatchDeliveredTwiceIsStoredOnce` (R5).
`TestMissingEdgeIsUnauthenticated`. `errors_test.go` holds
`TestEveryCodeThisPackageDeclaresIsMapped`, as `auditapi/errors_test.go`
does. The existing `intake_test.go` suite
passes unchanged, which is the proof the split kept the consumer's
behavior. `BenchmarkDeliver` in `handler_bench_test.go` starts its own hub
with `service.BusFsyncPerMessage`, runs batches of 64 and 1024 records with
256-byte syslog messages and unique record ids, and reports `records/s`
with `b.ReportMetric` (R11).
Verify: `uv run tools/scripts/run.py verify change -- src/services/device/internal/intake`

### U3. Collector post, telemetry handler, and receiver bound
Files: src/common/fsconv/keys.go, src/common/fsconv/README.md, src/modules/edgebus/collector.go, src/modules/edgebus/forwarder.go, src/modules/edgebus/receiver.go, src/modules/edgebus/receiver_internal_test.go, src/modules/edgebus/README.md, src/services/device/internal/otlpapi/handler.go, src/services/device/internal/otlpapi/errors.go, src/services/device/internal/otlpapi/errors_test.go, src/services/device/internal/otlpapi/handler_test.go, src/services/device/internal/otlpapi/README.md
After: U1
Change: `edgebus.NewCollector(CollectorConfig{Endpoint, Headers, Client})`
refuses an endpoint that is not an absolute `http` or `https` URL and
defaults the client as `StartForwarder` does today, on a copy.
`Collector.Post(ctx, signal, body)` posts to `<endpoint>/v1/<signal>` and
returns `PostAccepted`, `PostRefused`, `PostRetryRejected` (401, 403, 404,
408, 429), or `PostRetryUnavailable` (5xx, other answers, transport error,
request build error). `StartForwarder` checks the endpoint as `NewCollector`
does and keeps defaulting `f.cfg.Client` in place. `forward` builds its
collector from `f.cfg` on each call, so the tests that build a `Forwarder`
by literal and change `f.cfg.Client.Timeout` afterwards keep working.
`forward` keeps its subject checks and maps the outcomes to `Ack`, `refuse`, and
`retry` with the reasons it uses today. `maxOTLPBody` becomes `255 << 10`,
and its comment says why. `otlpapi.NewHandler(Config{Collector, EdgeID,
Logger, MeterProvider})` returns a `telemetryv1connect.TelemetryServiceHandler`.
A missing edge is `ErrCodeEdge`, which `otlpapi.ClientErrors` maps to
`unauthenticated`. With a nil collector, `Deliver` answers `settled: 0`.
Otherwise it validates each export, settles and counts an invalid one as
`invalid`, maps the signal, posts, settles on accepted, settles and counts
`collector_rejected` on refused, and stops on either retry. Forwarded items
count on `flowseer.otlpapi.records.forwarded` and refused ones on
`flowseer.otlpapi.records.refused` with `flowseer.otlpapi.reason`, both
with the signal key `fsconv` now holds, which the forwarder takes too.
Refusals log under the handler's own per-edge ten-second limiter. The edgebus README's "OTLP over the bus" section names the
collector post, the receiver's bound, and the telemetry call.
Tests: the forwarder suite passes unchanged
(`forwarder_internal_test.go`, `TestForwarderAnswerClasses` and
`TestForwarderLeavesTheCallersClientAlone`, `attribution_test.go`, and
`edgebus_test.go`, `TestReceiverToForwarderCarriesBodiesUnchanged`), which
is the proof the extraction kept the forwarder's rules.
`TestReceiverRefusesABodyOverTheRecordBound` in
`receiver_internal_test.go` posts 261121 bytes and wants 413 (R10). In
`otlpapi/handler_test.go`, outside `E`'s directory as the exported
`CollectorConfig` requires: `TestBodyReachesTheCollectorUnchanged` (R6),
`TestCollectorAnswersMapAsTheForwarderMapsThem` as a table over 200, 400,
401, 403, 404, 408, 429, 503 and a closed server (R7),
`TestRetryAnswerStopsTheCount` (R7, three exports),
`TestNewCollectorRefusesAMalformedEndpoint`,
`TestEveryCodeThisPackageDeclaresIsMapped` in `errors_test.go`,
`TestNoCollectorSettlesNothing` (R8), `TestInvalidExportIsSettledAndRefused`,
and `TestMissingEdgeIsUnauthenticated`.
Verify: `uv run tools/scripts/run.py verify change -- src/common/fsconv src/modules/edgebus src/services/device/internal/otlpapi`

### U4. Mount the handlers and record the change
Files: src/services/device/internal/host/serve.go, src/services/device/internal/host/host_test.go, src/services/device/internal/connecterr/consistency_test.go, src/services/device/README.md, docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md
After: U2, U3
Change: `mux` builds the ingest handler over `resources.hub.JetStream()`,
`edgeIDFromContext`, and `resources.edgeTenant`, and the telemetry handler
over a collector built from `TelemetryEndpoint` and `TelemetryHeaders` when
the endpoint is set and nil otherwise. Both mount on `edgeMux` behind
`middleware.Wrap` with the edge interceptors. The forwarder module and its
gate stay. The device README lists both services among the edge-facing
services and both packages in its package table. The ingestion record gains
a dated amendment: records also enter through `IngestService.Deliver`,
which shares intake's preparation and message id. The call takes the edge
from the assertion and the tenant from the edge store, where the stream
path reads both from the stream a record arrived in. A full typed stream
stops the call's count, so the record waits in the edge's log.
Tests: `TestNoCodeAnswersTwoDifferentThings` in
`connecterr/consistency_test.go` registers `intake.ClientErrors` and
`otlpapi.ClientErrors`. In `host_test.go`, `TestTheAPIListenerDoesNotRouteEdgeProcedures`
and the unauthenticated refusal cases gain both `Deliver` procedures.
`TestAnEnrolledEdgeDeliversIngestRecords` signs with
`edgeSigningRoundTripper`, delivers a batch of a valid record, an invalid
one, and a valid one, wants `settled: 3`, and reads two records from
`FLOWSEER_INGEST_SYSLOG` under the enrolled tenant (R4, and the item rule
of U1). `TestAnEmptyIngestBatchIsInvalidArgument` (U1's `min_items`).
`TestTelemetryWithoutACollectorSettlesNothing` (R8).
`TestACollectorOutageLeavesIngestFlowing` configures a collector double
answering 503 and wants the telemetry call at `settled: 0` and the ingest
call at `settled: 1` (R9).
Verify: `uv run tools/scripts/run.py verify change -- src/services/device/internal/host src/services/device/internal/connecterr src/services/device/README.md docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md`

Waves: U1 | U2 U3 | U4

## Verification

```bash
uv run tools/scripts/run.py verify change -- spec/proto/flowseer/edge spec/proto/flowseer/integration test/conformance/proto generated/go/proto/flowseer/edge src/common/fsconv src/services/device src/modules/edgebus docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md docs/architecture/2026-08-20-network-model-structure-direction.md
go test -run '^$' -bench BenchmarkDeliver -benchtime 20x ./src/services/device/internal/intake/
```

The benchmark's `records/s` for both batch sizes goes into the phase's
outcome note, so the user can set the threshold before review.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] The intake, otlpapi, edgebus, fsconv, edge schema, and device service
      READMEs, the ingestion record, and the network model structure record
      match the code.
- [ ] The benchmark's measured rate is in the outcome note.
- [ ] This plan's outcome is recorded with
      `uv run tools/scripts/run.py plan record implemented <plan> --units <n> --from <t> --to <t>` or `partial`.
- [ ] No plan labels in code.

## Open questions

- Unverified: whether Buf's protovalidate lint rule accepts
  `items: {ignore: IGNORE_ALWAYS}` beside `min_items`. The buf source is
  not in the module cache, so U1's `buf lint` settles it. If it refuses,
  each item field carries the encoded item as `bytes` and the handlers
  unmarshal each one, which keeps every Requirement.
- A runtime batch encoding above 255 KiB is refused with 413, and the
  in-repo exporter does not retry a 413
  (`src/common/service/telemetry_otlp.go:194`), so that batch is dropped
  and the exporter reports the error. The runtime batches 512 records
  (`src/common/service/telemetry_sdk.go:34`). Whether the agent pins a
  smaller batch is the third phase's question, since that phase owns the
  agent host.
- The third phase sizes `SegmentBytes` for each log. Any size up to
  `maxEdgeBody` minus a few bytes per record fits the call, by the
  arithmetic in Decisions.
- `docs/plans/2026-10-04-2023-fix-central-intake-review-items-plan.md`
  edits comments in `I/intake.go` and tests in `I/intake_test.go`. U2 edits
  `intake.go` and leaves `intake_test.go` alone, so whichever lands second
  merges the other. The parent's open question about that plan concerns the
  follower, which the last phase deletes, and changes nothing here.
