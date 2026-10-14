---
title: Bounded Ingest Backpressure - Plan
type: feat
date: 2026-10-14
artifact_contract: flowseer-plan/v2
execution: mixed
amends: docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md
---

# Bounded Ingest Backpressure - Plan

## Goal

A syslog record the central typed ingest stream accepted stays stored until
the history sink resolved it, however long ClickHouse is down, and a full
stream refuses new records instead of removing pending ones. An operator
pages through every stored record once, across tenants that share a record
id. The means: discard-new typed streams with no age, a closed set of
member consumers and a cleanup pass that purges only what every member
resolved, typed-first intake with a capacity gate, sink retries that keep a
batch's members and token, and a keyset that includes the tenant.

Stop condition: the pinned nats-server removes a record from a limits
stream with discard-new and no maximum age, or moves a member's
acknowledgement floor past a record it did not resolve. Research 15's
probes would then fail, and the cleanup rule is wrong.

## Decisions

The bounded-backpressure contract is decided and binds every unit below (decided by the user, 2026-10-13). This plan carries it onto main, reusing main's landed history API and tests (decided by the user, 2026-10-14).
[Research 15](../research/clickhouse-history-store/15-bounded-backpressure.md)
holds the pinned-source lines and probes behind it. Its probes pass against
the `go.mod` pins (nats-server v2.15.0, nats.go v1.54.0).

- Typed ingest streams keep limits retention with `DiscardNew`, the
  configured byte bound, `MaxAge` 0, and the requested duplicate window,
  changed in place on hub start. `HubConfig.IngestMaxAge`, its default, and
  `IngestStreams.max_age` are removed with no `reserved` line
  (`docs/code-style-proto.md:185-187`). Why: the server then refuses a
  publication at the bound and removes nothing itself (15, "What the server
  does"), and an in-place update keeps the stored records (15, probe 1).
- The members of a typed stream are the durable consumers
  `edgebus.IngestConsumers(recordType)` names. For syslog that is
  `history_sink` alone, with main's current consumer config: no filter,
  deliver all, explicit acknowledgement, `MaxDeliver` -1, `AckWait` two
  minutes, `MaxAckPending` 10,000. The hub creates each member with
  `CreateConsumer` right after the stream, before intake can publish. Code
  never updates a member. Why: cleanup needs a closed set whose floors move
  only by resolution (15, "The cleanup rule"). Keeping main's name and
  config lets an existing state directory's consumer pass as the member.
- `edgebus.CleanIngest` purges below the lowest member floor plus one, only
  when the consumers on the stream are exactly the members, each with the
  member's durable name, filter, deliver policy, acknowledgement policy,
  and delivery limit. A missing, stray, or differing consumer returns
  `ErrCodeCleanup` and purges nothing. A floor of zero sends no request.
  Why: `WithPurgeSequence(0)` purges everything (15, probe 2).
- A central host without a `history` section still provisions the member.
  Nothing resolves its syslog records, so the stream fills to its bound and
  intake is refused. Why: membership is closed in code, not per deployment,
  and an unresolved record is pending work under this contract.
- The sink terminates only a record ClickHouse rejects or one it cannot map,
  and counts it in `flowseer.history.records.refused`. A transient failure
  never terminates. Main already does both
  (`src/services/device/internal/historysink/sink.go`, `insert` and
  `refuse`), so the plan keeps them and adds the cleanup that frees the
  terminated record.
- A transient insert error is treated as ambiguous: the server may hold the
  block. The sink keeps that batch's members and retries them together with
  the same `batchToken`, once all have been redelivered. Records that
  arrive meanwhile form batches of their own. A retained batch whose
  members do not all return within `2 * RetryDelay + 10 s` is released, and
  its returned members are NACKed and become new records. Why: a member a
  peer sink took never returns. The deadline is a liveness bound, not a
  correctness one, and no upper bound on `RetryDelay` is enforced: `Start`
  only defaults a non-positive delay to 5 s (`sink.go`, `Start`), and the
  host sets none (`src/services/device/internal/host/host.go`,
  `setupHistorySink`), so the deadline is 20 s against the two-minute
  `AckWait`. A delay of 55 s or more puts the deadline past `AckWait`. The
  server then redelivers a held member, and the batch takes the newer
  delivery, whose acknowledgement the server honors. The rows a release can
  duplicate are absorbed by the `ReplacingMergeTree` key and read-time
  deduplication.
- The single-node `syslog` table gets `non_replicated_deduplication_window`
  1000 through a new migration step, and the cluster form keeps its
  replicated window. Why: the setting defaults to 0, which disables
  insert deduplication on a non-replicated table
  ([MergeTreeSettings.cpp at v26.8.20.9-lts](https://github.com/ClickHouse/ClickHouse/blob/v26.8.20.9-lts/src/Storages/MergeTree/MergeTreeSettings.cpp),
  `non_replicated_deduplication_window`), so without it a preserved token
  does nothing on the lab and integration tier. The parked branch set it
  at table creation (`262603e0`). Main's steps are recorded once, so the
  setting arrives as `ALTER TABLE ... MODIFY SETTING`. On the pinned image
  that ALTER succeeds on an existing table and turns deduplication on (15,
  "Insert deduplication on a single node"). U6 keeps an integration test of
  it.
- No instrument counts lost or discarded records. The discard gauge and
  `pollDiscarded` are deleted. Visibility is four sink gauges read from the
  last cleanup report (`flowseer.history.records.pending`,
  `flowseer.history.stream.bytes`, `flowseer.history.stream.limit`,
  `flowseer.history.stream.oldest.age`), `flowseer.history.cleanup.duration`,
  `flowseer.history.records.redelivered`, and
  `flowseer.intake.records.retried` with a `flowseer.intake.reason`
  attribute. Why: under this contract the server removes no unresolved
  record by itself, and the sampled gap main exports cannot be exact. A
  stream and consumer snapshot shows the first sequence and one floor, and
  the floor stays below a record a peer stored (15, prefix probe, first
  row), so `FirstSeq - 1 - AckFloor` cannot separate removed records from
  stored ones. A purge reply counts physical removals, records a peer
  already stored among them (15, prefix probe, third row), so it is not a
  loss count either.
- Intake publishes the typed record first and the evidence second. After a
  typed publication fails with `edgebus.IsStreamFull`, intake publishes
  nothing of that record type for one retry delay and naks each delivery in
  that window. A duplicate acknowledgement does not reopen the gate. Why:
  evidence first stores the raw bytes again on every retry past the
  duplicate window and pushes older evidence out (15, buffers table).
- Accepted work starts when a typed stream stored the record. The edge
  buffer, the per-edge hub stream, and the evidence stream stay rolling and
  uncounted, and no document claims the pipeline loses nothing. Why: moving
  that loss to the edge needs the per-edge stream split, an unprobed server
  path (15, last section).
- A read deduplicates on `(tenant_id, record_id)` and pages on
  `(ts, tenant_id, record_id)`, newest first. The token carries all three.
  Why: two admitted tenants may hold one record id at one receive time, and
  `(ts, record_id) < cursor` skips the second
  (`src/services/device/internal/historyquery/statement.go:52-69,90`).
- Main's plain `device_dim` table and its `clickhouse-go/v2` client stay.
  Why: backpressure touches neither (`src/services/device/internal/history/README.md`, Schema).
- The parked branch `parked/ingestion3-bp-main-reconcile` (`dc76ce71`) is
  reuse material, never merged. Its history store differs from main's
  (ch-go client, dictionary dimension, other package layout). The reuse map
  below names what each unit ports and how it adapts.

### Reuse map

| Unit | Port from `dc76ce71` | Adaptation |
| --- | --- | --- |
| U1 | `src/modules/edgebus/cleanup.go`, `cleanup_test.go`, the typed-stream hunk of `hub.go`, and the bounded-config cases of `hub_test.go` | Member `history_sink` with `AckWait` two minutes in place of `history_syslog` and one minute. Main's `CentralStreamBytes` check stays and the branch's `centralStreamsSum` is not ported |
| U2 | The `gated`, `closeGate`, reasoned `retry`, and typed-first `prepare` hunks of `intake.go`, and its `intake_test.go` cases | Apply onto main's `handle` and `retry`, whose retry carries no reason today |
| U3 | `historysink/retained.go`, the `clean` pass and `streamGauges` of `cleanup.go`, `cleanup_test.go` cases, and the retained and attach cases of `sink_test.go` | Rewrite against main's `Row`, `pending`, `Inserter`, and `fakeInserter`. Main fetches with `Fetch`, so the retained lookup goes in `process` before mapping |
| U4 | The keyset sentence of the branch plan | Main's `statement.go` already deduplicates on the pair. Only the order, keyset, and token change |
| U6 | The `non_replicated_deduplication_window` setting of the branch's `001_syslog.sql` and `identical_token_retry_stores_one_block` from `262603e0` | A new step 8 that alters main's table, and the test written against main's `ClickHouseInserter` in place of ch-go |
| U5 | Nothing | Main's `history_test.go` already holds the agent, pause, and `ReadInterface` steps |

## Requirements

Tenant ids `a1` and `a2` stand for `018f0000-0000-7000-8000-0000000000a1`
and `…a2`.

1. A typed stream is bounded without age. Example: a hub started on a state
   directory whose syslog stream was rolling with a 72-hour age and holds two
   records comes up with `DiscardNew`, `MaxAge` 0, and both records. Filled
   to its byte bound, it refuses a publication with an error for which
   `edgebus.IsStreamFull` is true, and its first sequence is unchanged.
2. Cleanup removes only what every member resolved. Example: records 1 to 3
   fill the stream, sink A holds 1, and a peer acknowledges 2. A pass removes
   nothing and a fourth publication is refused. A acknowledges 1, the next
   pass removes 1 and 2, and the fourth publication is accepted.
3. Cleanup refuses an open membership. Example: a stray consumer beside
   `history_sink`, a deleted member, or a member updated by hand to a filter
   makes `CleanIngest` return `ErrCodeCleanup` with all records stored.
4. An accepted record survives a store outage. Example: with the inserter
   failing transiently for 3 s, the stream holds every record throughout,
   every record is inserted after the inserter recovers, and `refused`
   reads 0.
5. A permanent refusal is terminated, counted, and does not hold the stream.
   Example: ClickHouse rejects the second of three records. The store holds
   the other two, `flowseer.history.records.refused` reads 1, and after one
   pass the stream holds none of the three.
6. An ambiguous retry keeps its batch. Example: batch `{1,2,3}` fails
   transiently and records 4 and 5 arrive. The next inserts, in either order,
   are `{4,5}` and `{1,2,3}`, the second with the first attempt's token.
7. Intake refuses under capacity without churning evidence. Example: with the
   typed stream full, a record with raw evidence retried through three retry
   delays leaves the evidence stream's count unchanged and adds 3 to
   `flowseer.intake.records.retried{flowseer.intake.reason="stream_full"}`.
   A second delivery inside one delay publishes nothing. Once cleanup frees
   room, the record is stored once.
8. Overload is visible without a loss count. Example: with the stream full
   and 5 records pending, `flowseer.history.records.pending` reads 5 and
   `flowseer.history.stream.bytes` is within one record of
   `flowseer.history.stream.limit`. No instrument is named
   `flowseer.history.records.discarded`.
9. Paging is tenant-aware. Example: record id `r` at one receive time under
   `a1` and `a2`, read with header `a1,a2` at page size 1, returns two pages
   holding one row each, `a2` first, with no repeat and no gap.
10. A full outage loses nothing accepted. Example: real agent, JetStream,
    device host, and ClickHouse. 1,000 records sent during a 60 s
    `docker pause` of ClickHouse, `ReadInterface` answering during the
    pause, and afterwards `ListSyslogRecords` paging returns each of the
    1,000 records exactly once.
11. A retry with the batch's token is stored once on a single node.
    Example: on the migrated single-node table, two rows inserted through
    `historysink.ClickHouseInserter` with token `T`, then again with `T`,
    count 2. A third insert with token `T2` makes 4.

## Out of scope

- The per-edge stream split and any refusal or count at the edge buffer or
  the per-edge hub stream (15, last section).
- A NATS fork, broker changes, and exact discard accounting.
- Phase 5's Postgres read model
  (`docs/plans/2026-10-02-2331-feat-central-ingestion-pipeline-phase5-plan.md`).
  No unit here depends on it or it on this plan.
- Changing the parent plan's retirement history.

## Units

### U1. Bounded typed streams and closed membership
Files: src/modules/edgebus/hub.go, src/modules/edgebus/cleanup.go, src/modules/edgebus/cleanup_test.go, src/modules/edgebus/hub_test.go, src/modules/edgebus/edgebus_test.go, src/modules/edgebus/README.md, spec/proto/flowseer/store/device/v1/service_config.proto, generated/go/proto/flowseer/store/device/v1/, src/services/device/internal/host/config.go, src/services/device/internal/host/config_test.go, src/services/device/internal/host/host_test.go, deploy/lab/central.textproto
After: none
Change: `createStores` creates each typed stream as the Decisions state and calls `EnsureIngestConsumer` for every member right after it. `cleanup.go` adds `ErrCodeCleanup`, `IngestConsumers`, `IngestConsumerConfig`, `EnsureIngestConsumer` (create only, refuses an unknown name or a differing member and leaves it untouched), `CleanReport` (floor, first and last sequence, bytes, byte bound, first record time, summed member pending, purged), `CleanIngest`, and `IsStreamFull` (a `jetstream.APIError` with error code 10077 and description `maximum bytes exceeded`). `IngestMaxAge`, `defaultIngestStreamMaxAge`, `IngestStreams.max_age`, and `host.IngestStreams.MaxAge` go. `buf generate` regenerates the bindings. The edgebus README replaces the typed half of its oldest-first text with the refusal, the members, the cleanup rule, the no-history consequence, the operator requests that remove unresolved records uncounted (15, "The cleanup rule"), and the sizing formula `stored_bytes_per_message x records_per_second x (horizon + cleanup interval) + stored_bytes_per_message x MaxAckPending`, with the horizon a capacity target and not a retention. `central.textproto` drops `max_age` and its comment states the horizon.
Tests: `hub_test.go` renames `TestIngestStreamsKeepSeventyTwoHoursByDefault` and `TestIngestStreamsTakeConfiguredBounds` to assert discard-new, `MaxAge` 0, the byte bound, and a ten-minute window. A new case covers Requirement 1's upgrade and fill. `edgebus_test.go` changes the typed row of `TestCentralIngestStreamsExistWithTheirLimits` and drops the typed case of `TestCentralIngestDuplicateWindowDoesNotExceedMaxAge`. `cleanup_test.go` on a real hub covers Requirements 2 and 3, a member that exists before the first publish, a floor of zero sending no purge, a member deleted and recreated being offered every stored record, a terminated record removed while a pending one above it stays, two concurrent passes, a hub restart keeping the purge and the pending record, an unknown name refused, a pre-existing consumer with main's sink config accepted as the member, and `IsStreamFull` false for error code 10077 with `maximum messages exceeded` and for other store errors. `config_test.go` drops `max_age` from `TestHistoryAndIngestBoundsAreRead` and asserts a textproto naming it fails to parse. `host_test.go` replaces the age assertions of `TestConfiguredIngestBoundsReachTheHub` and `TestUnsetIngestBoundsTakeTheHubDefaults`.
Verify: `uv run tools/scripts/run.py verify change -- src/modules/edgebus spec/proto/flowseer/store generated/go/proto/flowseer/store/device/v1 src/services/device/internal/host deploy/lab`

### U2. Typed-first intake with a capacity gate
Files: src/services/device/internal/intake/intake.go, src/services/device/internal/intake/intake_test.go, src/services/device/internal/intake/README.md
After: U1
Change: `prepare` returns the typed publication first. `handle` consults a per-record-type gate before publishing, closes it for one retry delay when a typed publication fails with `edgebus.IsStreamFull`, and labels each retry with `flowseer.intake.reason`: `stream_full`, `publish_failed`, `unknown_edge`, `validation`, or `marshal`. Inside a closed gate, the first delivery of each message id counts one retry and later ones none. The README states the order, the reasons, that the retried counter counts attempts and not records, the alert order (stream bytes against its limit, then `stream_full` retries, then `flowseer.intake.record.age` nearing the per-edge age), and where a record waits when central is full.
Tests: `TestTypedPublishFailureAfterEvidenceRedelivers` becomes a typed-first case asserting the subject sequence failed typed, typed, evidence. New cases: Requirement 7 against a full typed stream from U1, a counting `Publisher` that sees no publish inside the delay, exactly 3 counted retries over three delays, a duplicate success between two refusals leaving the gate closed, an evidence failure after a stored typed record ending with one typed record, and a non-capacity error labeled `publish_failed`.
Verify: `uv run tools/scripts/run.py verify change -- src/services/device/internal/intake`

### U3. Sink cleanup, retained retries, and gauges
Files: src/services/device/internal/historysink/sink.go, src/services/device/internal/historysink/retained.go, src/services/device/internal/historysink/cleanup.go, src/services/device/internal/historysink/metrics.go, src/services/device/internal/historysink/sink_test.go, src/services/device/internal/historysink/cleanup_test.go, src/services/device/internal/historysink/README.md, src/services/device/test/integration/history_test.go
After: U1, U6
Change: `Start` binds the hub's member with `stream.Consumer`. When the server reports it missing, it calls `edgebus.EnsureIngestConsumer` and logs a warning. The sink's own consumer config goes. `Run` runs `edgebus.CleanIngest` every `Config.CleanInterval` (default 5 s), records `flowseer.history.cleanup.duration`, and stores the report for the four gauges. A failed pass keeps the last report and logs at the limited rate. `pollDiscarded`, `discardInterval`, and the `discarded` gauge go. `process` adds to `flowseer.history.records.redelivered` for a delivery count above one and routes a retained member to its batch before mapping. A transient insert failure naks and retains the batch as the Decisions state. When a rejected batch's first half fails transiently, that half is retained and the never-sent second half is NACKed without retention. Shutdown releases a held batch without counting a retry. The README replaces the discard paragraph with the cleanup pass, its flow chart, and what one held record does to the stream, and its Instruments table lists the new instruments without `discarded`. Its Deduplication paragraph says the token deduplicates on a single node through the window U6 sets. In `history_test.go`, `ingested` reads `State.LastSeq`, since cleanup now lowers `State.Msgs`.
Tests: `TestTheDiscardedGaugeCountsRecordsTheStreamRemovedBeforeTheyWereAcknowledged` is deleted. `cleanup_test.go` on a real hub with two `Sink` values covers Requirement 2 through the sinks, a record NACKed by one sink and inserted by the peer stored once and removed, Requirements 4, 5, and 8, a failed pass leaving the gauges and the runner alive, the five-second default, and a metrics set with no `discarded` instrument. `sink_test.go` adds Requirement 6 through `fakeInserter.tokens`, a retained batch released at its deadline, a retained member delivered twice before its batch completes inserted once and acknowledged through the later delivery, a missing member recreated with a warning, and a differing member left as it is while each cleanup pass fails with its error type recorded on `flowseer.history.cleanup.duration`. `TestATransientErrorNaksTheBatchAndItIsInsertedOnceTheStoreReturns` also asserts the retry carries the first token. The other cases pass unchanged.
Verify: `uv run tools/scripts/run.py verify change -- src/services/device/internal/historysink src/services/device/test/integration`

### U4. Tenant-aware keyset pagination
Files: src/services/device/internal/historyquery/statement.go, src/services/device/internal/historyquery/query.go, src/services/device/internal/historyquery/query_test.go, src/services/device/internal/historyquery/README.md, src/services/device/internal/historyapi/test/integration/history_test.go
After: none
Change: both scopes order `ts DESC, tenant_id DESC, record_id DESC`, inner and outer statements alike, and the keyset condition is `(ts, tenant_id, record_id) < ({after_ts:DateTime64(3)}, {after_tenant:String}, {after_id:UUID})`. `cursor` gains the tenant. The token encodes milliseconds, tenant, and record id, and `decodeToken` refuses a tenant that fails `tenant.Validate` with `ErrCodeBadToken`. The README describes the new key and token.
Tests: `query_test.go` asserts the three-column order in every statement, the keyset condition and its `after_tenant` parameter, a token round trip with `default` and a UUID tenant, and a token with an invalid tenant refused. The historyapi integration file adds Requirement 9. `TestPagingWalksTheWindowNewestFirstWithoutRepeatsOrGaps` passes unchanged.
Verify: `uv run tools/scripts/run.py verify change -- src/services/device/internal/historyquery src/services/device/internal/historyapi`

### U5. End-to-end acceptance and direction records
Files: src/services/device/test/integration/history_test.go, src/services/device/test/integration/README.md, docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md, docs/architecture/2026-10-08-history-store-direction.md
After: U1, U2, U3, U4, U6
Change: the outage step of `TestSyslogFromAnAgentIsReadBackAndAnOutageLosesNothing` keeps its 1,000 records, its 60 s pause, and its `ReadInterface` call. `assembleHistory` expects `applied step 8`. It also asserts during the pause that the typed stream is discard-new with no age and still holds every unresolved record. Afterwards it pages `ListSyslogRecords` at page size 100 until no token remains and asserts each outage record appears exactly once, then waits for a cleanup pass to leave the stream empty. The integration README describes the step. The ingestion record gains a dated amendment replacing its 2026-10-08 bullet on 72-hour retention with the accepted-work boundary, the refusal, the members and cleanup, and the rolling upstream buffers. Its read-time deduplication sentence names `(tenant_id, record_id)`. The history record's "Writes" section replaces its 72-hour and discard-counter sentences, its "Duplicates" section names the pair, and its consequences list replaces the 72-hour bullet.
Tests: the changed `history_test.go` covers Requirement 10 under `history_integration` and fails, not skips, when Docker or the image is missing.
Verify: `uv run tools/scripts/run.py verify change -- src/services/device/test/integration docs/architecture`

### U6. Single-node insert deduplication window
Files: src/services/device/internal/history/sql/steps/0008_syslog_dedup_window.sql, src/services/device/internal/history/migrate.go, src/services/device/internal/history/migrate_test.go, src/services/device/internal/history/test/integration/migrate_test.go, src/services/device/internal/history/test/integration/schema_test.go, src/services/device/internal/history/README.md
After: none
Change: step 8 holds `ALTER TABLE {{.DB}}.syslog MODIFY SETTING non_replicated_deduplication_window = 1000` inside `{{if not .OnCluster}}`. `Migrate` skips a statement whose rendered text is blank, so a cluster run records step 8 and executes nothing for it. The README's Schema notes and dedup paragraph state the window and that a replicated table keeps its own.
Tests: `migrate_test.go` expects 8 embedded steps and adds a fake-executor case where a blank rendered statement is recorded and not executed. The integration `migrate_test.go` expects 8 steps on both runs, `SHOW CREATE TABLE flowseer.syslog` naming the window on a single node, and no such setting on the cluster node. `schema_test.go` adds Requirement 11 through `historysink.NewInserter` as the writer. Nothing here breaks a connection after the server stored a block, so the ambiguous case is proven in two parts: this test shows a resent token is stored once, and U3 shows an ambiguous failure resends the same token.
Verify: `uv run tools/scripts/run.py verify change -- src/services/device/internal/history`

Waves: U1 U4 U6 | U2 U3 | U5

## Verification

- Each unit's verifier, then `go test -race ./src/modules/edgebus/... ./src/services/device/...`.
- `TESTCONTAINERS_RYUK_DISABLED=true go test -race -tags=history_integration ./src/services/device/internal/history/test/integration/ ./src/services/device/internal/historyapi/test/integration/ ./src/services/device/test/integration/ -run 'TestMigrate|Token|TestSyslogFromAnAgent|TestPaging|Tenant'` with a Docker daemon.
- `go tool -modfile=tools/buf/go.mod buf lint`.

## Definition of done

- [ ] The verifier is green for every changed path.
- [ ] The edgebus, intake, historysink, historyquery, and integration READMEs and both direction records describe the bounded contract in the change that implements it.
- [ ] No instrument, test, or document counts discarded records.
- [ ] This plan's outcome is recorded with `uv run tools/scripts/run.py plan record implemented <plan> --units 6 --from <t> --to <t>` or `partial`.
- [ ] No plan labels in code.

## Open questions

None blocks a unit.

- Phase 5 (`codex/postgres-state-plan` in its state file, no such local
  branch) and U1 and U5 edit `service_config.proto`, `host/config.go`,
  `config_test.go`, `deploy/lab/central.textproto`, the integration README,
  and the ingestion record. The edits do not interact. The second to land
  merges main, keeps both, and reruns `buf generate`.
- Unmeasured: one purge over millions of records, Linux timings, and a
  clustered hub. Unverified: consumer state across a power loss, where a
  floor that falls back redelivers rows read-time deduplication absorbs.
- A member's config changed by hand and changed back is undetectable.
