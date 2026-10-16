---
title: Edge Delivery over Connect, Phase 3, Agent Switch - Plan
type: refactor
date: 2026-10-04
artifact_contract: flowseer-plan/v2
execution: mixed
---

# Edge Delivery over Connect, Phase 3, Agent Switch - Plan

## Goal

The agent writes syslog records and its own OTLP bodies into two segmented
logs and drains each through its delivery call. It no longer calls
`AttachBus`, starts no leaf node, and imports nothing from
`src/modules/edgebus`. The means are a drain loop over `seglog`, a loopback
OTLP receiver in the agent that appends to the telemetry log, a narrower
publisher for the syslog source, and the pin helpers moved to a package both
trees can import.

Stop condition: the plan is wrong if a record the drain sends in one call
from one segment can exceed central's 1 MiB edge body bound, since the drain
would then stall on a segment it can never deliver.

## Decisions

The parent's decisions apply
([parent plan](2026-10-04-2232-refactor-edge-delivery-over-connect-plan.md)).
`A` is `src/edge/agent`, `S` is `src/edge/agent/internal/seglog`, and `E` is
`src/modules/edgebus`. This phase adds:

- One drain per log, in a new package `A/internal/drain`. It takes a
  segment with `Next`, sends the records from its resume offset in one
  call, advances the offset by `settled`, and calls `Ack` once the offset
  reaches the end. The offset lives in memory, keyed to
  `Segment.Sequence`, and resets to zero whenever `Next` returns another
  sequence, so a restart resends a partly settled segment from its start.
  Why: `Ack` removes a whole file and has no partial form
  (`S/log.go:418-439`, `S/doc.go:7-9`), and the byte or age bound can
  delete the held segment between two `Next` calls (`S/log.go:327-349`,
  `:352-353`). The ingest call stores a record delivered twice once
  (`spec/proto/flowseer/edge/ingest/v1/ingest_service.proto:17-18`), inside
  the typed streams' duplicate window of 10 minutes
  (`E/hub.go:82-86`). An agent down longer than that with a partly settled
  segment stores its settled prefix twice. The telemetry call promises no
  deduplication, so a restart can post an OTLP body to the collector twice.
  Both are accepted.
- A segment with nothing left to send is acknowledged without a call. That
  covers a segment recovered with zero records (`S/log.go:194-217`) and one
  whose remaining records were all undecodable. Why: both requests require
  at least one item (`ingest_service.proto:28-29`,
  `telemetry_service.proto:25-26`), and central's interceptors refuse an
  empty request (`src/services/device/internal/host/serve.go:153-156`), so
  sending it would stall the log for good.
- One segment is one call. Why: a segment holds at most 512 KiB including a
  20-byte header and 8 bytes per record (`S/format.go:24-25`). A repeated
  item in the request costs at most 4 bytes of tag and length for an item
  under 2 MiB, so the request body is smaller than the segment and well under
  `maxEdgeBody` (`src/services/device/internal/host/host.go:82`). A Connect
  unary request body is the bare marshaled message
  (`~/go/pkg/mod/connectrpc.com/connect@v1.21.0/protocol_connect.go:927-949`).
  Neither request has a `max_items` bound
  (`ingest_service.proto:28-31`, `spec/proto/flowseer/edge/telemetry/v1/telemetry_service.proto:25-28`).
- The drain has its own backoff, not `subscribeloop`'s. It starts at 1 s,
  doubles to a 30 s ceiling without jitter, and resets when a call settles at
  least one record. A call error and a zero `settled` both back off. A test
  substitutes the wait through a `Wait func(ctx, d) bool` field, as
  `subscribeloop.Config.Wait` does. Why: `subscribeloop` keeps its backoff
  inline in `Run` (`A/internal/subscribeloop/loop.go:165-219`), and the
  backoff helpers in `src/common/service` are unexported
  (`src/common/service/supervisor.go:807`). The 1 s and 30 s values match
  `loop.go:17-20`, so the parent's first requirement reads "within 30 s of
  central accepting again, plus one call per segment queued ahead".
- The drain never drops a record because a call failed. Only the log's byte
  and age bounds discard. Why: the edge delivery record keeps the retry and
  drop rules in central.
- Each drain runs as its own `service.Module`, appended in `host.Run` next to
  `rawWindowAssembly.module()`, not through `runAll`. A `Next` or `Ack` error
  other than `ErrCodeClosed` ends the module's attempt and the supervisor
  restarts it, with the log left open across attempts. A segment that keeps
  failing to read therefore holds back only its own log, until the age bound
  deletes it. Why: `runAll` cancels every loop when one returns
  (`A/host/host.go:511-545`), which would let a telemetry-log fault restart
  the ingest drain, against the parent's decision that telemetry cannot hold
  device records back. Restarts show in the runtime's module-attempt
  metrics.
- A record the drain cannot unmarshal into the call's item type is dropped,
  counted on `flowseer.edge.delivery.undecodable` (`{record}`), and treated
  as settled. Why: it can only come from a writer bug,
  since `S` checks a CRC-32C on every record, and leaving it would stall the
  log behind it. The source validates a record before it appends
  (`A/internal/syslogsource/source.go:268-276`), following
  `docs/solutions/architecture-patterns/a-decoder-wider-than-its-encoder-loses-whatever-you-queue.md`.
- The ingest log holds a marshaled `flowseer.integration.ingest.v1.IngestRecord`
  per record. The telemetry log holds a marshaled
  `flowseer.edge.telemetry.v1.TelemetryExport`, so the signal travels with
  the body.
- Both logs keep `S`'s defaults of `SegmentBytes` 512 KiB and
  `MaxRecordBytes` 256 KiB as code constants, not configuration. Why: a
  255 KiB body's `TelemetryExport` is 261126 bytes, 1018 under 262144. The
  largest syslog record is about 160 KB by estimate (raw data at most 65535
  bytes, `spec/proto/flowseer/integration/ingest/v1/ingest_record.proto:49`,
  and message text at most 65527, `spec/proto/flowseer/event/log/v1/syslog_record.proto:65`).
  The estimate is unverified, so U4 adds a test that pins it. A larger
  `SegmentBytes` would break the one-segment-one-call bound above.
- `AgentBuffer` splits into `AgentLogBounds ingest = 3` and
  `AgentLogBounds telemetry = 4`, with
  `AgentLogBounds { int64 max_bytes = 1; google.protobuf.Duration max_age = 2; }`.
  Fields 1 and 2 of `AgentBuffer` are not reused (`docs/code-style-proto.md:185`).
  `max_bytes` must be at least 1048576 and `max_age` at least 20 minutes.
  Unset fields default to 192 MiB and 7 days for ingest and 64 MiB and 7
  days for telemetry. The old `AgentBuffer.max_bytes` and `max_age` go
  without a `reserved` line (`docs/code-style-proto.md`, Evolution). Why:
  separate bounds are what the parent's decision on separate logs needs.
  The floors are the smallest values `seglog.Open` accepts
  (`S/log.go:143-168`: `SegmentBytes` at most `MaxBytes/2`, `RollAge` 10 min
  at most `MaxAge/2`), so the schema refuses a file the log would refuse,
  as `A/host/config.go:80-82` requires. The defaults keep today's 256 MiB
  total (`E/leaf.go:91-92`), weighted toward device records. This is a
  config file break, stated as a fact: an agent configuration with
  `buffer { max_bytes: … }` no longer loads.
- The logs live under `<state_dir>/buffer/ingest` and
  `<state_dir>/buffer/telemetry`. An upgraded agent leaves the old
  `<state_dir>/jetstream` directory and `hub.creds` on disk, and nothing
  removes them.
- The syslog source's `Publisher` becomes `Append(record []byte) error`,
  which `*seglog.Log` satisfies. The subject argument, the message id, and
  `Subject` go. Why: the subject carried the tenant and the edge, central now
  takes both from the assertion, and central deduplicates on the record id
  inside the record. An `ErrCodeRecord` refusal drops the record and counts
  it under the reason `too_large`. An `ErrCodeClosed` ends the source's loop.
  Any other error keeps today's retry with backoff
  (`source.go:279-299`), since an append that keeps failing would otherwise
  spin.
- The metric names `flowseer.edge.syslog.published` and
  `flowseer.edge.syslog.publish.retries` stay. Only their descriptions change
  to name the ingest log. Why: names are an operational API
  (`docs/conventions/observability.md:93-94`).
- The agent no longer learns its tenant, and `A/internal/busattach` is
  deleted. Why: `AttachBus` was the only call that told it
  (`A/internal/busattach/busattach.go:98`), and the tenant fed only the leaf's
  subjects (`E/leaf.go:165`, `:230-237`).
- The empty trust anchor refusal moves from `busattach.go:88-91` into
  `host.Run`, before the signed client is built. Why: an empty set refuses
  every certificate central presents, and the refusal is clearer at start
  than as a TLS failure on every call.
- The loopback receiver moves from `E/receiver.go` into a new package
  `A/internal/otlpreceiver`. It keeps its rules: 127.0.0.1 on a
  kernel-chosen port, uncompressed protobuf only (415 otherwise), bodies of
  at most 255 KiB (413 above), and 503 when the append fails so the
  exporter retries. It appends a marshaled `TelemetryExport` with the
  signal of the path, `telemetryv1.TelemetrySignal`, in place of
  `edgebus.OTelSignal`. `E/receiver.go` and its tests are deleted in U5,
  since busattach was their only user.
- `PinVerifier`, `PinnedTLSConfig`, `SPKIDigest`, and `ErrCodePin` move from
  `E/pin.go` into a new package `src/common/tlspin`, and the code string
  becomes `tlspin/pin`. The code only fails a local TLS handshake and never
  crosses a process, and `AGENTS.md` allows the rename before the first
  stable release. Why: central's
  certificate code uses `SPKIDigest`
  (`src/services/device/internal/host/certificate.go:107`, `:153`) and the
  agent's identity uses `PinnedTLSConfig` (`A/internal/identity/client.go:30`).
  Two unrelated trees import them and they hold no domain type, which is the
  test in `src/common/README.md`, "What belongs here". `identity` is
  internal to the agent, so central could not import the helpers from there.
- Buffer metrics are new instruments, read from `seglog.Stats` and carrying
  `flowseer.edge.buffer.log` with the values `ingest` and `telemetry`:
  `flowseer.edge.buffer.records` and `flowseer.edge.buffer.size` (unit `By`)
  as observable up-down counters, `flowseer.edge.buffer.oldest_age` (unit
  `s`) as an observable gauge, and `flowseer.edge.buffer.discarded`
  (`{record}`) and `flowseer.edge.buffer.corrupt` (`{segment}`) as
  observable counters. The drain adds `flowseer.edge.delivery.settled`
  (`{record}`), `flowseer.edge.delivery.undecodable` (`{record}`), and a
  `flowseer.edge.delivery.duration` histogram (`s`) over each call, whose
  count is the call total. A call that errs carries `error.type` from
  `fsconv`, and a call that answers `settled` 0 carries `error.type`
  `nothing_settled`. All carry the log attribute. Why: a failure measure
  needs its total (`docs/conventions/observability.md:301-304`). Why: `Leaf.BufferState` (`E/leaf.go:260-288`) feeds no
  metric today, so nothing is renamed. The instrument kinds follow
  `docs/conventions/observability.md:311-315`.
- The drain logs a warning when its log's discard count has risen since its
  previous attempt. Why: the telemetry log's own metrics travel through the
  telemetry log, and the conventions require drop diagnostics on a path that
  does not depend on the failing exporter
  (`docs/conventions/observability.md:398-400`). The agent's logger writes
  to its local handler as well as the exporter
  (`src/common/service/telemetry_sdk.go:136`, `:170`).
- Shutdown order: `service.Run` returns, then the receiver closes, then the
  telemetry log, then the ingest log. Why: the runtime's final export posts
  into the receiver during `service.Run`'s shutdown, and the drains and the
  syslog source run as modules inside it.

## Requirements

The parent's requirements 1, 2, 3, the edge half of 6, and 7 are claimed
here. The parent's second requirement holds at the log level through
`TestByteBounds` and `TestAgeBoundsAtAppendAndNext` in
`src/edge/agent/internal/seglog/log_test.go`, and requirement 4 here carries
it to the configuration.

1. A record appended while central refuses is delivered after it accepts.
   Example: with the fake ingest handler failing for 10 s of fake time, a
   record appended in that window reaches the handler within 30 s of the
   handler accepting.
2. A short `settled` count resumes at the right record. Example: with the
   handler settling two of five and then all, the handler has seen records
   three to five twice and one and two once, and `Ack` ran once.
3. A collector outage leaves device records flowing. Example: with the fake
   telemetry handler answering `settled` 0, a syslog record reaches the fake
   ingest handler and `flowseer.edge.buffer.records` for `telemetry` grows.
4. Each log takes its own bounds from the configuration. Example: a file
   with `buffer { ingest { max_bytes: 2097152 } }` opens the ingest log with
   `MaxBytes` 2097152 and the telemetry log with 64 MiB. A file with
   `buffer { ingest { max_bytes: 1000 } }` is refused at load.
5. A damaged byte costs at most one segment and is counted. Example: with a
   byte flipped in the last record of the middle segment of three, reopening
   the log and draining delivers every record except the damaged one, and `Stats` reports
   `CorruptSegments` 1, the value `flowseer.edge.buffer.corrupt` reads.
6. The largest record of each kind fits a log record. Example: a syslog
   `IngestRecord` built from a 65535-byte payload of empty structured-data
   parameters with raw evidence and 16 window refs has `proto.Size` at most
   262144, and a `TelemetryExport` with a 261120-byte body has size 261126.
7. The agent's telemetry still starts before its first export. Example: the
   `service.Config` the agent builds names the receiver's bound endpoint, and
   a POST of a protobuf body to its `/v1/metrics` answers 200 and appends one
   record to the telemetry log.
8. The agent starts no NATS server and joins no bus. Example: in the
   end-to-end test a syslog datagram reaches its typed stream, the hub's
   `LeafCount()` is 0, and `<agent state>/jetstream` does not exist.

## Out of scope

- Removing `StartLeaf`, `Leaf`, `TenantFromSubjects`, the hub's edge
  accounts, the per-edge streams, the forwarder, and `AttachBus` from
  central and the schema, which the last phase does. After this phase they
  have no agent caller.
- Central's bus listener. It still binds (`src/services/device/internal/host/host.go:313-317`),
  so the end-to-end test asserts the leaf count, not an absent listener.
- The process-local bus in `src/common/service`, which the agent still links.

## Units

### U1. Pin helpers in a common package

Files: src/common/tlspin/tlspin.go, src/common/tlspin/tlspin_test.go, src/common/README.md, src/modules/edgebus/pin.go, src/modules/edgebus/leaf.go, src/modules/edgebus/edgebus_test.go, src/modules/edgebus/README.md, src/modules/edgebus/doc.go, src/edge/agent/internal/identity/client.go, src/edge/agent/internal/busattach/busattach.go, src/edge/agent/internal/busattach/busattach_test.go, src/services/device/internal/host/certificate.go, src/services/device/internal/host/certificate_test.go
After: none
Change: `src/common/tlspin` holds `PinVerifier`, `PinnedTLSConfig`, `SPKIDigest`, and `ErrCodePin`, with the comments from `E/pin.go` minus the leaf. `E/pin.go` is deleted and every caller imports `tlspin`. The common README lists the package. The edgebus README's pin paragraph (`README.md:370-374`) and the `LeafConfig.TLS` comment (`leaf.go:55-56`) point at `tlspin`.
Tests: `tlspin_test.go` takes the pin cases from `edgebus_test.go:546` and `busattach_test.go:68` (`TestTheEmptyAnchorSetRejectsEveryCertificate`): an empty set refuses, the matching digest passes, another digest refuses with `ErrCodePin`. `certificate_test.go:73` uses `tlspin.SPKIDigest`.
Verify: `uv run tools/scripts/run.py verify change -- src/common/tlspin src/common/README.md src/modules/edgebus src/edge/agent/internal/identity src/edge/agent/internal/busattach src/services/device/internal/host`

### U2. Drain

Files: src/edge/agent/internal/drain/doc.go, src/edge/agent/internal/drain/drain.go, src/edge/agent/internal/drain/drain_test.go, src/edge/agent/internal/drain/send.go, src/edge/agent/internal/drain/send_test.go
After: none
Change: `drain.Run(ctx, Config) error` loops over `Next`, send, and `Ack` as the Decisions describe, with a send of shape `func(ctx context.Context, records [][]byte) (settled int, err error)`, on a `Log` interface (`Next`, `Ack`, `Stats`) that `*seglog.Log` satisfies. It returns nil when `ctx` ends or `Next` reports `seglog.ErrCodeClosed`, and returns any other `Next` or `Ack` error. `send.go` holds the two senders: `Ingest(client)` unmarshals each record into `IngestRecord` and calls `IngestService.Deliver`, and `Telemetry(client)` does the same with `TelemetryExport` and `TelemetryService.Deliver`. Each maps `settled` back to segment offsets past any dropped undecodable record. A `settled` above the number sent is treated as all sent. The drain registers the three delivery instruments and logs the discard warning.
Tests: `drain_test.go` runs under `testing/synctest` with a real log in `t.TempDir()` and a scripted send: requirement 1 (refuse for 10 s, then accept within 30 s), requirement 2 (two of five, then all), requirement 5 (flip a byte between `Close` and `Open`), the backoff sequence 1, 2, 4, 8, 16, 30, 30 s and its reset after a partial settle, an undecodable record counted and passed, a segment of only undecodable records acknowledged with no call, a segment recovered with zero records acknowledged with no call, a partial settle followed by the age bound deleting that segment so the next sequence is sent from its first record, the duration histogram's `error.type` on an error and on zero settled, `settled` above the count, return on `Close`, and the discard warning after the age bound drops a segment. Each test joins the drain before its body returns (`docs/solutions/conventions/a-synctest-body-joins-its-timed-goroutine-before-it-returns.md`). `send_test.go` mounts fake `IngestService` and `TelemetryService` handlers on `httptest` and checks the request items equal the records in order, that the records of one 512 KiB segment fill one request under 1 MiB, and requirement 6 for the telemetry record.
Verify: `uv run tools/scripts/run.py verify change -- src/edge/agent/internal/drain`

### U3. Loopback receiver in the agent

Files: src/edge/agent/internal/otlpreceiver/receiver.go, src/edge/agent/internal/otlpreceiver/receiver_test.go
After: none
Change: `otlpreceiver.Start(appender)` binds 127.0.0.1:0 and serves `POST /v1/logs`, `/v1/metrics`, and `/v1/traces`. Each body becomes a marshaled `TelemetryExport` with the path's signal, appended through an `Append(record []byte) error` interface. The status rules and the serve goroutine follow `E/receiver.go:51-132`, with `spawn.Go`. `Endpoint()` and `Close(ctx)` keep their meaning.
Tests: a protobuf body on each path appends one record that unmarshals to the right signal and the same bytes. A gzip body and a JSON body answer 415, a 261121-byte body answers 413, a 261120-byte body is accepted, and a failing append answers 503. The cases follow `E/receiver_internal_test.go`.
Verify: `uv run tools/scripts/run.py verify change -- src/edge/agent/internal/otlpreceiver`

### U4. The agent switches to the logs

Files: spec/proto/flowseer/store/agent/v1/agent_config.proto, spec/proto/flowseer/store/agent/v1/README.md, src/edge/agent/host/config.go, src/edge/agent/host/config_test.go, src/edge/agent/host/host.go, src/edge/agent/host/delivery.go, src/edge/agent/host/delivery_test.go, src/edge/agent/host/host_test.go, src/edge/agent/host/syslog_test.go, src/edge/agent/host/export_test.go, src/edge/agent/host/capture_test.go, src/edge/agent/internal/syslogsource/source.go, src/edge/agent/internal/syslogsource/source_test.go, src/edge/agent/internal/syslogsource/size_test.go, src/edge/agent/internal/busattach, src/edge/agent/internal/seglog/doc.go, src/edge/agent/README.md
After: U1, U2, U3
Change: the schema carries `AgentBuffer.ingest` and `AgentBuffer.telemetry` as the Decisions state, and `buf generate` refreshes `generated/`. `Config` exposes `IngestBuffer()` and `TelemetryBuffer()` with the defaults as named constants. `host.Run` refuses an empty anchor set, opens both logs, starts the receiver on the telemetry log, passes its endpoint to `service.Run`, and closes in the order the Decisions give. `delivery.go` builds one module per drain, appended in `host.Run` beside `rawWindowAssembly.module()` so `modules()` keeps its three (`capture_test.go:71`), and registers the buffer metrics. The syslog source appends to the ingest log through the narrowed `Publisher`. `busattach` is deleted. The `Run` doc comment, the agent README's opening (`README.md:8-9`), startup section (`README.md:76-86`), clock paragraph (`README.md:114-121`, which says a skewed clock fails the bus attachment), package table, and syslog paragraph (`README.md:207-210`) describe the logs and the drains. The comments at `agent_config.proto:24-29`, `:37`, `:66-67` and `config.go:113-115`, `:144-147` stop naming the leaf and the bus module. `seglog/doc.go` names its importer.
Tests: `config_test.go` covers requirement 4 and the defaults. `delivery_test.go` covers requirement 3 against fake ingest and telemetry handlers and requirement 7. `host_test.go` replaces `AttachBus` in `orderedCentral` with the fake delivery handlers, asserts Enroll unsigned and the next call signed, ends the run by cancelling, and moves the empty anchor case from `busattach_test.go:50`. `source_test.go` drops `startHub` and `startLeaf` (`:50-90`) and reads the ingest log back in its four listener tests (`:179`, `:284`, `:361`, `:486`), asserting the record carries its record id. It adds the `too_large` drop and the `ErrCodeClosed` return. The five stub publishers (`:568-593`, `:732`, `:806-819`, `:887`, `:1065`) take `Append`. `TestSource_PublisherRetryWithBackoff` asserts identical record bytes on both attempts in place of the message id (`:659-661`), and `TestSource_RunReturnsNilWhenCanceledInsidePublish` (`:821`) is deleted, since `Append` takes no context. `size_test.go` covers the syslog half of requirement 6. `syslog_test.go`'s `recordingPublisher` takes `Append`.
Verify: `uv run tools/scripts/run.py verify change -- spec/proto/flowseer/store/agent/v1 src/edge/agent` and `go test -race ./src/services/device/test/integration/...`, since the end-to-end tests run the switched agent

### U5. Edgebus receiver removal and the end-to-end test

Files: src/modules/edgebus/receiver.go, src/modules/edgebus/receiver_internal_test.go, src/modules/edgebus/collector.go, src/modules/edgebus/subjects.go, src/modules/edgebus/edgebus_test.go, src/modules/edgebus/README.md, src/modules/edgebus/doc.go, src/services/device/test/integration/delivery_test.go, src/services/device/test/integration/history_test.go
After: U1, U4
Change: `E/receiver.go`, its internal test, `TestReceiverToForwarderCarriesBodiesUnchanged` (`edgebus_test.go:288`), and its only helper `postOTLP` (`:273`) are deleted. `otlpContentType` moves from `receiver.go:20` into `collector.go`, its remaining user (`collector.go:111`). The `OTelSignal` comment (`subjects.go:63`) no longer names the receiver. The edgebus README's receiver text (`README.md:3-9`, `:333-366`) and `doc.go:5-11` say the agent delivers over Connect and the leaf waits for removal. The history test's comment at `history_test.go:159` no longer says the agent attached its bus.
Tests: `delivery_test.go` holds `TestASyslogDatagramReachesItsStreamWithNoLeafLink`, built on `assembleRawWindow` (`raw_window_test.go:55`): `waitDeviceResolved`, `send`, then `typedRecord`, then requirement 8's leaf count and state directory checks. The existing integration tests that run the real agent (`raw_window_test.go:305`, `:349`, `:382`, `history_test.go:266`) pass unchanged on the new path.
Verify: `uv run tools/scripts/run.py verify change -- src/modules/edgebus src/services/device/test/integration`

Waves: U1 U2 U3 | U4 | U5

## Verification

`go test -race ./src/edge/agent/... ./src/modules/edgebus/... ./src/common/tlspin/... ./src/services/device/...`
passes, and the verifier is green on every unit's paths. `grep -rn
"modules/edgebus" src/edge` prints nothing.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `src/edge/agent/README.md`, `src/modules/edgebus/README.md`,
      `src/common/README.md`, and `spec/proto/flowseer/store/agent/v1/README.md`
      describe the tree after the change.
- [ ] This plan's outcome recorded with
      `uv run tools/scripts/run.py plan record implemented <plan> --units <n> --from <t> --to <t>`
      or `partial`.
- [ ] No plan labels in code.
