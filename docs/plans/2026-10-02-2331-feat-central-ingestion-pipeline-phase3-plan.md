---
title: ClickHouse History Store - Plan
type: feat
date: 2026-10-08
artifact_contract: flowseer-plan/v2
execution: mixed
amends: docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md
---

# ClickHouse History Store - Plan

## Goal

Syslog records from the central stream land in ClickHouse in the events
pattern of the [history store record](../architecture/2026-10-08-history-store-direction.md),
acknowledged after one replica holds them, and an operator reads them back by
device or by location, newest first, for one or several tenants. The means: a
history store package with its own migrator, a sink module, a tenant-set query
package behind one Connect call, ingest streams that survive a 72-hour store
outage, and a retention class on each tenant. Every other domain lands later
with its record type and picks a pattern from the record.

Stop condition: the U6 test or the rerun benchmark shows a device-scope read
whose rows read grow with other tenants' data.

## Decisions

The parent plan's Decisions apply. The history store record holds the ones
that outlive this phase (decided by the user, 2026-10-08): tables per domain in
five patterns, the key contract, stamped dimensions, flat tenants read as a
set, the `IN (SELECT …)` row policy form, idempotent rollups, synchronous
inserts acknowledged after one replica, 72-hour ingest retention, three
retention classes, weekly partitions for events, the analyst workload, and an
in-house migrator. This phase adds:

- The client is `github.com/ClickHouse/clickhouse-go/v2` v2.48.0 over the
  native protocol. Why: published 2026-08-04 per its `proxy.golang.org` info,
  past the 14-day wait, Apache-2.0, pure Go, per-query settings through
  `clickhouse.Context` ([01](../research/clickhouse-history-store/01-clients-durability-dedup.md),
  section 1).
- The packages live under `src/services/device/internal/` (`history`,
  `historysink`, `historyquery`, `historyapi`). Why: one host assembles them
  (`src/modules/README.md:29-31`).
- The read scopes are a device and a location. Each row stamps `location_id`
  from the registry device's `config.location`, and `site_id` stays empty.
  Why: a device's location may be a rack or a room
  (`spec/proto/flowseer/model/inventory/v1/device.proto:100-113`), and central
  holds no location tree to find the site. (decided by the user, 2026-10-08)
- `TenantConfig` gains `retention_class`, set by `CreateTenant`, unset meaning
  standard. (decided by the user, 2026-10-08)
- The device mirror takes its tenant from the registry edge's tenant
  (`edgestore.Store.TenantForEdge`, `src/services/device/internal/edgestore/store.go:180`),
  since the registry names none.
- The migrator runs only as `device -config <path> -migrate-history`, a
  one-shot mode of the device binary that exits after the last step. The host
  never migrates and never creates schema. Why: two central replicas would
  otherwise migrate at once, and `schema_steps` holds no lock.
- `X-FlowSeer-Tenant` may list up to 25 tenants, comma-separated, for a method
  whose rule sets `multi_tenant`. The interceptor reads every header line with
  `Values`, refuses duplicates and empty items, and admits the set in one
  `BatchCheck` (`maxChecksPerBatch = 50` and two checks per tenant,
  `src/services/device/internal/authz/obligation.go:11`). (decided by the
  user, 2026-10-08)
- The read relation is `viewer` on each tenant. A partner's active admins hold
  it on the customer through the partner link (operator authorization record,
  lines 588-591 and 975), and ordinary partner users do not.
- The deduplication token is a hash of the batch's sorted stream sequences.
  Why: a token naming only the first and last sequence would drop a later
  batch with the same ends and other members, which two replicas sharing a
  consumer produce.
- Severity and facility are `UInt8` holding the enum number with a presence
  bit each, following the record's key contract. `ts` is `DateTime64(3)`, the
  received time. `observed_at` from `Provenance` is a second column.
- No query aliases an aggregate to a source column's name, since ClickHouse
  26.8 then resolves the column to the aggregate
  ([12](../research/clickhouse-history-store/12-benchmark.md), and
  `benchmark/README.md`).
- Code defaults for ingest streams stay small (72 h, 256 MiB per type, the
  current CENTRAL budget). Production sizes live in configuration, with the
  formula in `src/modules/edgebus/README.md`. Why: the hub reserves budgets
  against 75 % of free disk (`src/modules/edgebus/hub.go:47-59`), and a
  fleet-sized default would stop every hub-starting test on a small host.

## Requirements

Tenant ids below are `a1…` for `018f0000-0000-7000-8000-0000000000a1`, device
ids `d1…` likewise.

1. A syslog record acknowledged by the sink is readable through the read call.
   Example: an `IngestRecord` with a `SyslogRecord` for device `d1`, published
   under tenant `a1`, appears once in `ListSyslogRecords` for `a1`, scope
   device `d1`, within 10 s.
2. With ClickHouse unreachable, published records stay unacknowledged, every
   other device-service module keeps running, and all records are readable
   after ClickHouse returns. Example: 1,000 records published during a 60 s
   pause yield 1,000 rows afterwards, and `ReadInterface` answers during the
   pause.
3. A record redelivered after the duplicate window is listed once. Example:
   the same `record_id` inserted twice, 15 minutes apart, appears once in
   `ListSyslogRecords` before and after merges.
4. A read lists one or several tenants in the header and returns only their
   rows, each naming its tenant. Example: header `a1,a2` returns rows of both.
   Header `a1,a3`, where the caller holds no `viewer` on `a3`, returns
   `PermissionDenied` and no rows.
5. A read that would pass its profile's row limit fails without stalling
   inserts. Example: with `svc_reader`'s `max_rows_to_read` at 10,000,000, a
   30-day device-scope read over 12,000,000 seeded rows returns
   `ResourceExhausted`, while a concurrent insert of 10,000 rows completes.
6. The migrator brings an empty server to the current schema and changes
   nothing on a second run. Example: two runs leave one row per step in
   `schema_steps` and identical `SHOW CREATE` output for every table.
7. Ingest streams keep records for 72 hours with the configured byte bounds.
   Example: with `ingest_streams.max_bytes_per_type` at 2 GiB, the hub creates
   `FLOWSEER_INGEST_SYSLOG` with `MaxAge` 72 h and `MaxBytes` 2 GiB, and refuses
   to start when the configured CENTRAL budget is below the streams' sum.
8. A tenant's retention class reaches its rows. Example: a tenant created with
   `retention_class: RETENTION_CLASS_SHORT` gets `retention_class = 1` on its
   syslog rows, and a tenant created without one gets `2`.

## Out of scope

- Every domain other than syslog, and their record types (the dossiers'
  schema gaps sections list them).
- A location tree, and with it site scope and `site_id`.
- Changing a tenant's retention class after creation (no update call exists).
- Phase 5's current-state store, which a later plan may use for
  `software_version`.
- Dashboards, rollups other than syslog's daily counts, and analyst queries.
- Audit trails in ClickHouse, and the production ClickHouse cluster.

## Units

### U1. Records
Files: docs/architecture/2026-10-08-history-store-direction.md, docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md, docs/architecture/2026-09-30-operator-authorization-direction.md, docs/architecture/README.md, docs/research/clickhouse-history-store/, docs/research/README.md, GOALS.md
After: none
Change: the history store record and its research land. The ingestion record gains a dated amendment: its Stores row points to the five patterns and replaces `wait_for_async_insert=1` with synchronous inserts acknowledged after one replica, its 2026-10-04 sentence that each sink deduplicates on tenant and `record_id` gives way to merge plus read-time deduplication, its open questions on tenant isolation, query limits, and retention name the history record, and ingest streams keep 72 hours. The operator authorization record gains a dated amendment for tenant sets on methods whose rule allows them. `GOALS.md` names the history store under Control plane.
Tests: none (documents).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs GOALS.md`

### U2. Configuration, retention class, ingest bounds, lab
Files: spec/proto/flowseer/store/device/v1/service_config.proto, spec/proto/flowseer/model/identity/v1/tenant.proto, spec/proto/flowseer/api/identity/v1/tenant_service.proto, src/services/device/internal/identityapi/tenant.go, src/services/device/internal/identityapi/tenant_test.go, src/services/device/internal/host/config.go, src/services/device/internal/host/config_test.go, src/services/device/internal/host/host.go, src/services/device/internal/host/host_test.go, src/modules/edgebus/hub.go, src/modules/edgebus/hub_test.go, src/modules/edgebus/README.md, deploy/lab/compose.yaml, deploy/lab/central.textproto, deploy/lab/README.md, src/services/device/test/integration/clickhouse_images_test.go, src/services/device/test/integration/lab_fixtures_test.go
After: none
Change: `DeviceServiceConfig` gains `HistoryStore history = 13`: native endpoints as `host:port`, database, an optional cluster name that selects `Replicated` engines and `ON CLUSTER`, the admin, writer, reader, and analyst user names with absolute password-file paths, and an optional CA file. It also gains `IngestStreams ingest_streams = 14` (`max_age`, `max_bytes_per_type`, `central_budget_bytes`). The comment at lines 19-24 that keeps bus bounds out of configuration is rewritten. `host.go` passes the ingest bounds into `HubConfig`. The edgebus ingest `MaxAge` default becomes 72 h, and the README states the production sizing formula (stored bytes per message, measured in `hub_test.go`, times 72 h, devices, and messages per device per minute). `TenantConfig` gains `retention_class` (enum `RetentionClass`, short, standard, long), `CreateTenantRequest` carries it, and `identityapi` copies it. The lab compose file gains ClickHouse at digest `sha256:9b61e3c635c04ad5bb521eb4f6e61ce7585b5580814e51c25bb9e8292ce43364`, a one-shot service running the migrate mode, and `central.textproto` a `history` block with production-sized ingest bounds.
Tests: `config_test.go` rejects a relative password path, an endpoint without a port, and a budget below the streams' sum, and accepts a config without `history`. `host_test.go` asserts the configured bounds reach the hub. `hub_test.go` covers Requirement 7 and records the measured bytes per message. `tenant_test.go` covers a created tenant with and without a class. `clickhouse_images_test.go` declares the image constant, and `lab_fixtures_test.go` checks the compose digest against it.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/store spec/proto/flowseer/model/identity spec/proto/flowseer/api/identity src/services/device/internal/identityapi src/services/device/internal/host src/modules/edgebus deploy/lab src/services/device/test/integration`

### U3. History store package
Files: go.mod, go.sum, docs/dependencies/statements/go/github.com/ClickHouse/clickhouse-go/v2.md, src/services/device/internal/history/
After: none
Change: the statement for clickhouse-go v2.48.0 follows `docs/conventions/dependencies.md:13-37` (criteria `deploy`). The unit stops as blocked until a person fills its `approved` field, since `docs/conventions/dependencies.md:5-7` requires approval before `go.mod` changes. `history.Open(ctx, Config)` returns a `*Store` with writer and reader pools and never fails on an unreachable server. `history.Migrate(ctx, *Store)` applies embedded, numbered SQL steps with the admin user, records each in `schema_steps`, skips a recorded step, and refuses to run when `schema_steps` holds a step newer than its own. With a cluster name every table uses a `Replicated*` engine and every user, profile, row policy, resource, and workload is created `ON CLUSTER`. The steps create `syslog` (events pattern, `(tenant_id, device_id, ts, record_id)`, `PARTITION BY (retention_class, toMonday(ts))`, `UInt8` severity and facility with a presence mask, raw `message` bytes, structured data as parallel arrays, a `text` index on the message, a vendor tag column, `location_id`, `site_id`, `software_version`), `syslog_by_location` ordered `(tenant_id, location_id, ts, device_id, record_id)` and fed by a view, `syslog_daily` counts, `device_dim` with its dictionary, `user_tenants`, the three workloads under a CPU resource, the `svc_reader` profile (`max_execution_time` 30 s, `max_rows_to_read` 10,000,000, `max_memory_usage` 2 GiB, `max_threads` 8, all `CONST`) and the `analyst` profile from [11](../research/clickhouse-history-store/11-olap.md), section 5, and one analyst row policy per table in the `IN (SELECT …)` form. TTLs follow the record's retention table. A dictionary source names no password in its DDL: the implementer cites the 26.8 dictionary docs for a credential-free local source and stops as blocked if none exists. `history.MirrorDevices(ctx, tenant, devices)` upserts `device_dim`.
Tests: `history/migrate_test.go` with a fake executor covers step order, skipping, refusing a newer schema, and stopping on a failed step. `history/test/integration/` (tag `history_integration`, its own image constant checked equal to U2's) covers Requirement 6, asserts every table has an analyst row policy, and asserts the device-scope read's `EXPLAIN indexes = 1` selects one key range.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- go.mod go.sum docs/dependencies src/services/device/internal/history`

### U4. Migrate mode and syslog sink
Files: src/services/device/cmd/device/main.go, src/services/device/internal/historysink/, src/services/device/internal/host/host.go, src/services/device/internal/host/host_test.go
After: U2, U3
Change: `-migrate-history` opens the store from the config, runs `history.Migrate`, and exits. The sink is a Service Module appended last to the host's module list behind `FixedGate(history configured)`. Its Setup mirrors the registry's devices with the edge's tenant and does not fail when ClickHouse is unreachable, and its Runner never returns on a store error. It follows `FLOWSEER_INGEST_SYSLOG` with a durable consumer (`AckExplicitPolicy`, `MaxDeliver -1`, `MaxAckPending` 10,000), takes the tenant from subject token 1, maps `SyslogRecord`, stamps `location_id`, `software_version` (empty), and the tenant's `retention_class`, and inserts batches of up to 10,000 rows or 1 s with `async_insert = 0` and the sequence-hash token. It acknowledges after the insert returns. A transient error naks the batch with a delay. A permanent rejection splits the batch to find the bad record, which is terminated and counted. A gauge reports records the stream discarded before acknowledgement (stream first sequence minus one, minus the consumer's acknowledged floor). Instruments: `flowseer.history.records.{inserted,refused,retried,discarded}`, `flowseer.history.batch.duration`, `flowseer.history.record.age`.
Tests: `historysink/sink_test.go` with an in-process JetStream and a fake inserter asserts no ack before the insert returns, a nak on a transient error, isolation of a permanently rejected record, distinct tokens for two batches with equal ends and different members, and that a redelivered record is inserted and acknowledged. A mapping test pins severity `EMERGENCY = 0` to `0` with its presence bit set, an absent PRI to a cleared bit, and a non-UTF-8 message byte for byte. `host_test.go` asserts the sink is last, absent without `history`, and that with ClickHouse unreachable the other modules stay up.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/cmd/device src/services/device/internal/historysink src/services/device/internal/host`

### U5. Tenant sets, query package, and the read call
Files: spec/proto/flowseer/authz/v1/rule.proto, spec/proto/flowseer/api/history/v1/, src/common/tenant/, src/services/device/internal/authz/interceptor.go, src/services/device/internal/authz/interceptor_internal_test.go, src/services/device/internal/authz/authz_test.go, src/services/device/internal/historyquery/, src/services/device/internal/historyapi/, src/services/device/internal/host/host.go, src/services/device/internal/host/serve.go, test/conformance/proto/api_authorization_test.go, test/conformance/proto/field_constraint_class_test.go
After: U3, U4
Change: `Rule` gains `bool multi_tenant`, valid only with `RULE_MODE_TENANT`. For such a method the interceptor admits the header's tenant set as the Decisions describe and puts it on the context (`tenant.WithSet`, `tenant.SetFrom`). Other methods refuse a list or a second header line. `historyquery.ListSyslog(ctx, Scope, Window, MinSeverity, Page)` reads the set from the context, refuses an empty one, starts every `WHERE` with `tenant_id IN {tenants:Array(String)}`, reads `syslog` for a device scope and `syslog_by_location` for a location scope, applies `LIMIT 1 BY record_id`, orders newest first, and pages by a keyset token of `(ts, record_id)`. A ClickHouse limit error maps to `ResourceExhausted`. `HistoryService.ListSyslogRecords` (`spec/proto/flowseer/api/history/v1/history_service.proto`) takes a scope (`DeviceGlobalRef` or `LocationGlobalRef`), a window, a minimum severity, and a page token, and returns entries of `record_id`, `TenantLocalRef tenant` as data, and the `SyslogRecord`. Its rule is `RULE_MODE_TENANT`, relation `viewer`, `multi_tenant: true`. `serve.go` mounts the handler behind the operator interceptors, and the conformance tests import the new package.
Tests: `interceptor_internal_test.go` covers a two-tenant header admitted, one denied tenant refusing the call, a 26-tenant list refused, and a list or a second header line refused on a single-tenant method. `historyquery/query_test.go` asserts the tenant condition leads every statement, `LIMIT 1 BY record_id` is present, and an empty set is refused. `historyapi/test/integration/` (tag `history_integration`) covers Requirements 3, 4, and 5.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/authz spec/proto/flowseer/api/history src/common/tenant src/services/device/internal/authz src/services/device/internal/historyquery src/services/device/internal/historyapi src/services/device/internal/host test/conformance/proto`

### U6. End-to-end tier
Files: src/services/device/test/integration/history_test.go, src/services/device/test/integration/README.md
After: U4, U5
Change: one test under the `history_integration` tag starts NATS, the device host with `history` configured, the migrate mode, an agent, and ClickHouse, sends `<34>1 2026-10-02T10:00:00Z sw1 app - - - link down` to the agent from a registry device's address, and reads it back through `ListSyslogRecords`. The README states the run command.
Tests: the test covers Requirements 1, 2, and 8. It pauses the ClickHouse container for 60 s (`docker pause`, so the mapped port stays) and fails, not skips, when the image is missing (`docs/solutions/README.md`, line 87).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/services/device/test/integration`

Waves: U1 U2 U3 | U4 | U5 | U6

## Verification

- `go test -race ./src/services/device/... ./src/modules/edgebus/... ./src/common/tenant/... ./test/conformance/...`
- `go test -tags history_integration ./src/services/device/internal/history/... ./src/services/device/internal/historyapi/... ./src/services/device/test/integration/...` with the Docker host the integration README names.
- `go tool -modfile=tools/buf/go.mod buf lint`
- Manual: rerun `docs/research/clickhouse-history-store/benchmark/run.sh all` after aligning its `syslog` DDL with the migrator's, and confirm the syslog queries stay flat.

## Definition of done

- [ ] The verifier is green for every changed path.
- [ ] The history store record is proposed, and the ingestion and operator authorization records carry their amendments.
- [ ] A person approved the clickhouse-go statement before `go.mod` changed.
- [ ] READMEs exist for `history`, `historysink`, `historyquery`, and `historyapi`.
- [ ] This plan's outcome is recorded with `uv run tools/scripts/run.py plan record implemented <plan> --units 6 --from <t> --to <t>` or `partial`.
- [ ] No plan labels in code.

## Open questions

- The planning rate of one syslog message per device per minute is an
  assumption, since the production baseline has no syslog rate.
- Whether the ClickHouse operator replicates access entities and workloads
  on its own. `ON CLUSTER` makes the migrator independent of it.
- The replicated lab cluster the stop condition would need does not exist.
  U6 runs one node, and replication behaviour stays unmeasured until the
  deployment phase builds the cluster.
