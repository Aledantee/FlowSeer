---
title: ClickHouse clients, durability, dedup, tenancy, and migrations
date: 2026-10-08
status: research; sources fetched 2026-10-08
---

# ClickHouse for FlowSeer: research notes (fetched 2026-10-08)

Every claim below cites a URL fetched on 2026-10-08 and quotes it. ClickHouse docs pages were
fetched as Markdown (`<page>.md`) and reflect the docs as of that date (several pages say
"Last modified on July 3, 2026"). "Inference" marks my own reasoning from the quoted text.
"Unverified" marks things I could not confirm from a fetched source.

## 0. Server versions in play

Source: `https://api.github.com/repos/ClickHouse/ClickHouse/releases?per_page=40`

| Line | Latest tag | Published |
|---|---|---|
| 26.8 LTS | v26.8.20.9-lts | 2026-10-07 |
| 26.3 LTS | v26.3.44.2-lts | 2026-10-07 |
| 26.9 stable | v26.9.12.8-stable | 2026-10-06 |
| 26.7 stable | v26.7.24.8-stable | 2026-10-07 |

Several behaviours below changed in 26.2 (async insert on by default, unified dedup, text index
GA), so the 26.3 or 26.8 LTS line matters. Inference: 26.8 LTS is the natural pin. The operator
guide says "For production, pinning the channel to an explicit `<major>.<minor>` (e.g. `25.8`)
is generally preferred." (`https://clickhouse.com/docs/products/kubernetes-operator/guides/configuration.md`)

ClickHouse operator releases (`https://api.github.com/repos/ClickHouse/clickhouse-operator/releases`):
v0.0.8 (2026-09-25), v0.0.7 (2026-07-20). CRDs are `apiVersion: clickhouse.com/v1alpha1`
(`https://clickhouse.com/docs/products/kubernetes-operator/guides/introduction.md`).

---

## 1. Go clients

### Versions, dates, license (14-day rule: cutoff 2026-09-24)

| Module | Latest | Published | Prior | Source |
|---|---|---|---|---|
| `github.com/ClickHouse/clickhouse-go/v2` | v2.48.0 | 2026-08-04T10:38:02Z | v2.47.0 (2026-06-26) | `https://proxy.golang.org/github.com/!click!house/clickhouse-go/v2/@v/v2.48.0.info` |
| `github.com/ClickHouse/ch-go` | v0.74.0 | 2026-07-24T11:33:20Z | v0.73.0 (2026-06-19) | `https://proxy.golang.org/github.com/!click!house/ch-go/@v/v0.74.0.info` |

Both latest versions are older than 14 days. clickhouse-go v2.48.0 requires `github.com/ClickHouse/ch-go v0.74.0`
(`https://proxy.golang.org/github.com/!click!house/clickhouse-go/v2/@v/v2.48.0.mod`), so the pair is consistent.

License: both Apache 2.0.
- ch-go README: "## License / Apache License 2.0" (`https://raw.githubusercontent.com/ClickHouse/ch-go/v0.74.0/README.md`)
- clickhouse-go LICENSE: "Copyright 2016-2023 ClickHouse, Inc. ... Apache License" (`https://raw.githubusercontent.com/ClickHouse/clickhouse-go/v2.48.0/LICENSE`)

Go version: both `.mod` files declare `go 1.25.0`.

### Protocol

- ch-go: "Low level TCP ClickHouse client and protocol implementation in Go." and "NB: **No pooling, reconnects** and **not** goroutine-safe by default, only single connection. ... pooling for ch-go is available as chpool package." (ch-go README v0.74.0). Docs: "ch-go - Low level client. Native interface only." (`https://clickhouse.com/docs/integrations/language-clients/go/index.md`)
- clickhouse-go: "Supports both native ClickHouse TCP and HTTP client-server protocols" and "Utilises low level ch-go client for encoding/decoding and compression (versions >= 2.3.0)." (clickhouse-go README v2.48.0). "Both APIs use the native binary encoding regardless of transport, so HTTP carries no serialization overhead." (Go docs index)

### Batch insert API

- clickhouse-go: `conn.PrepareBatch(ctx, "INSERT INTO example")`, `Append`/`AppendStruct`, `Send`. "Batches are held in memory until `Send` is executed." "Batches shouldn't be shared across go-routines - construct a separate batch per routine." (`https://clickhouse.com/docs/integrations/language-clients/go/clickhouse-api.md`). README: "Use `Flush` to send currently buffered rows while keeping the batch usable (native protocol). For HTTP protocol, `Flush` is currently a no-op." "Use `Send` to flush any remaining rows and finalize the INSERT."
- ch-go: build `proto.Input` of typed columns (`proto.ColStr`, `proto.ColDateTime64`, `proto.NewLowCardinality(...)`) and call `conn.Do(ctx, ch.Query{Body: "INSERT INTO t VALUES", Input: input})`; streaming via `OnInput` returning `io.EOF` to stop (ch-go README).
- Docs guidance: "For insert heavy use cases, where millions of inserts are required per second, we recommend using the low level client ch-go. ... For query workloads focused on aggregations or lower throughput insert workloads, the clickhouse-go provides a familiar `database/sql` interface" (Go docs index).

### Async insert

- clickhouse-go: "Async insert is supported via `WithAsync()` helper on both Native and HTTP protocols." "You can use `WithSettings()` manually to add any async related settings. `WithAsync()` is just a simple wrapper" (README v2.48.0). `func WithAsync(wait bool) QueryOption` (`https://raw.githubusercontent.com/ClickHouse/clickhouse-go/v2.48.0/context.go`).
- ch-go: no dedicated helper found in the README. Async insert is a server setting, so it is set through `Query.Settings` (below). Inference.

### Per-query settings and `insert_deduplication_token`

Neither client has a dedicated `insert_deduplication_token` API. Both pass arbitrary per-query settings, and `insert_deduplication_token` is an ordinary session setting (section 3).

- clickhouse-go: `func WithSettings(settings Settings) QueryOption` with `type Settings map[string]any` (context.go v2.48.0). Docs: "we can use context to pass settings to a specific API call `ctx := clickhouse.Context(context.Background(), clickhouse.WithSettings(clickhouse.Settings{"async_insert": "1"}))`" (clickhouse-api.md). `prepareBatch` reads them: `options := queryOptions(ctx)` (`https://raw.githubusercontent.com/ClickHouse/clickhouse-go/v2.48.0/conn_batch.go`, line 28). It also has `WithQueryID` and `WithQuotaKey` (context.go).
- ch-go: `ch.Query` has "`// Settings are optional query-scoped settings. Can override client settings.` `Settings []Setting`", plus `QueryID` and `QuotaKey` fields (`https://raw.githubusercontent.com/ClickHouse/ch-go/v0.74.0/query.go`, lines 145-189).

### Dependency footprint (`go.mod` require blocks)

- ch-go v0.74.0 direct requires (20): backoff/v4, enumer, go-humanize, go-faster/city, go-faster/errors, google/uuid, hashicorp/go-version, jackc/puddle/v2, klauspost/compress, pierrec/lz4/v4, segmentio/asm, testify, otel (+metric, sdk, trace) v1.44.0, multierr, zap v1.28.0, x/crypto, x/sync. Indirect: 11.
- clickhouse-go v2.48.0 direct requires (13): ch-go v0.74.0, andybalholm/brotli, docker/go-units, google/uuid, mkevac/debugcharts, moby/moby/api, moby/moby/client, paulmach/orb, shopspring/decimal, testify, testcontainers-go v0.43.0, otel/trace, x/net. Indirect: about 50 (containerd, moby, gopsutil, logrus, otelhttp and so on).
- Inference: testcontainers and moby appear in clickhouse-go's module graph because they are test dependencies of that module. Go's module pruning (go ≥1.17) keeps packages FlowSeer does not import out of the build, but they still appear in FlowSeer's `go.sum` and module graph. ch-go's graph is much smaller, but it pulls zap and otel/sdk.

### Summary

| | clickhouse-go v2.48.0 | ch-go v0.74.0 |
|---|---|---|
| Transport | Native TCP and HTTP | Native TCP only |
| Pooling, failover | Built in | Single conn, `chpool` separate |
| Insert API | Row `Append`/`AppendStruct`, columnar `batch.Column(i).Append` | Columnar `proto.Input`, streaming `OnInput` |
| Per-query settings | `clickhouse.Context(ctx, WithSettings(...))` | `ch.Query.Settings` |
| Async insert | `WithAsync(wait)` | via settings |
| Raw formats (Protobuf etc.) | `InsertFormat` experimental, HTTP only | not documented |

---

## 2. Durability and acknowledgements

### Synchronous INSERT on ReplicatedMergeTree

- "By default, an INSERT query waits for confirmation of writing the data from only one replica. If the data was successfully written to only one replica and the server with this replica ceases to exist, the stored data will be lost. To enable getting confirmation of data writes from multiple replicas, use the `insert_quorum` option." (`https://clickhouse.com/docs/engines/table-engines/mergetree-family/replication.md`)
- "Replication is asynchronous and multi-master." (same page)
- "Each block of data is written atomically. The INSERT query is divided into blocks up to `max_insert_block_size = 1048576` rows." (same page)
- "If ClickHouse Keeper is unavailable during an `INSERT`, or an error occurs when interacting with ClickHouse Keeper, an exception is thrown." (same page)
- fsync: `fsync_after_insert` default `0`: "Do fsync for every inserted part. Significantly decreases performance of inserts, not recommended to use with wide parts." (`https://clickhouse.com/docs/reference/settings/merge-tree-settings/fsync.md`). Inference: by default an acknowledged part is written but not fsynced. Durability against node loss comes from replication (quorum), not fsync.

### `insert_quorum` / `insert_quorum_parallel`

Source: `https://clickhouse.com/docs/reference/settings/session-settings/insert-quorum.md`
- `insert_quorum` (type UInt64Auto, default 0): "If `insert_quorum < 2`, the quorum writes are disabled. If `insert_quorum >= 2`, the quorum writes are enabled. If `insert_quorum = 'auto'`, use majority number (`number_of_replicas / 2 + 1`) as quorum number."
- "`INSERT` succeeds only when ClickHouse manages to correctly write data to the `insert_quorum` of replicas during the `insert_quorum_timeout`. If for any reason the number of replicas with successful writes does not reach the `insert_quorum`, the write is considered failed and ClickHouse will delete the inserted block from all the replicas where data has already been written."
- "ClickHouse generates an exception: If the number of available replicas at the time of the query is less than the `insert_quorum`."
- `insert_quorum_parallel` (default 1, since 21.1): "If enabled, additional `INSERT` queries can be sent while previous queries have not yet finished. If disabled, additional writes to the same table will be rejected." With it disabled, "all replicas in the quorum are consistent ... (the `INSERT` sequence is linearized)" and `select_sequential_consistency` becomes usable.
- `insert_quorum_timeout` default `600000` ms: "If the timeout has passed and no write has taken place yet, ClickHouse will generate an exception and the client must repeat the query".
- `timeout_overflow_mode`: "a quorum write keeps waiting until its quorum is satisfied, or reports `UNKNOWN_STATUS_OF_INSERT` if `insert_quorum_timeout` elapses first." (`https://clickhouse.com/docs/reference/settings/session-settings/timeout-overflow-mode.md`)

Answer: with default settings an ack means written on one replica. With `insert_quorum=2` (or `'auto'`, which is 2 for 2 replicas) an ack means written on both. Inference for 1 shard × 2 replicas: quorum 2 makes every insert fail while one replica is down (rolling upgrade, pod reschedule). The JetStream sink would then stop acking and back up, rather than ack a single-copy write. `UNKNOWN_STATUS_OF_INSERT` is an ambiguous outcome, so the retry must be dedup-safe (section 3). Dev (1 replica) must use `insert_quorum` ≤ 1.

Note: goose's ClickHouse doc says "`insert_quorum_timeout` (60s by default)" (`https://raw.githubusercontent.com/pressly/goose/main/doc/dialect-clickhouse.md`). That contradicts the ClickHouse settings page (600000 ms). The settings page is autogenerated from `Settings.cpp`, so I trust it.

### `async_insert` + `wait_for_async_insert=1`

Source: `https://clickhouse.com/docs/optimize/asynchronous-inserts`
- `async_insert` default is now `1`: version history "26.2 / 1 / Enable async inserts by default." (`https://clickhouse.com/docs/reference/settings/session-settings/async-insert.md`)
- Flush triggers: "`async_insert_max_data_size`, default 100 MiB", "`async_insert_busy_timeout_ms`, default 200 ms", "`async_insert_max_query_number`, default 450". "Since version 24.2, ClickHouse uses adaptive flush timeouts by default".
- "When set to 1 (the default), ClickHouse only acknowledges the insert after the data is successfully flushed to disk." "Setting `wait_for_async_insert = 0` enables 'fire-and-forget' mode ... there's no guarantee the data will be persisted".
- "Our strong recommendation is to use `async_insert=1,wait_for_async_insert=1` if using asynchronous inserts."
- "If any row in an insert query has a parsing or type error, none of the data from that query is flushed — the entire query's payload is rejected."
- "in clusters, buffers are maintained per node".
- `wait_for_async_insert_timeout` default 120 s (`https://clickhouse.com/docs/reference/settings/session-settings/wait-for.md`).
- Batching guidance for sync inserts: "We recommend inserting data in batches of at least 1,000 rows, and ideally between 10,000–100,000 rows." "we recommend keeping the number of insert queries around one insert query per second." (`https://clickhouse.com/docs/best-practices/selecting-an-insert-strategy.md`)

Unverified: whether an async-insert flush honours `insert_quorum`, so that `wait_for_async_insert=1` acks only after the quorum. No fetched page states it either way.

Inference for FlowSeer: the sink already batches from JetStream, so synchronous batch INSERT with an explicit `async_insert=0` fits the "batch ≥1k rows, ~1 insert/s" guidance. It keeps the ack semantics simple: INSERT returned means the part is committed (with quorum, on both replicas). Async insert is the fallback if many small sinks produce small batches. Because 26.2 turned `async_insert` on by default, the sink must set `async_insert=0` explicitly if it wants synchronous semantics.

---

## 3. Deduplication

### Insert-time block dedup (ReplicatedMergeTree)

Sources: `https://clickhouse.com/docs/guides/developer/deduplicating-inserts-on-retries.md`, `https://clickhouse.com/docs/reference/settings/merge-tree-settings/replicated-deduplication-window.md`, `https://clickhouse.com/docs/reference/settings/session-settings/insert.md`, `https://clickhouse.com/docs/reference/settings/session-settings/deduplicate-insert.md`

- Mechanism: "each block is assigned a unique block_id, which is a hash of the data in that block. ... If the same block_id is found in the deduplication log, the block is considered a duplicate and isn't inserted". The user "will still receive a successful operation status".
- Whole-block hash: "The hash sum covers the whole inserted block, so an insert is deduplicated only when its entire data matches a previous insert (a retry), not per individual part."
- `replicated_deduplication_window` default `10000` (raised in 25.9): "The number of most recently inserted blocks for which ClickHouse Keeper stores hash sums". "A large number ... slows down `Inserts`".
- `replicated_deduplication_window_seconds` default `3600` (lowered in 25.10): "Hash sums older than `replicated_deduplication_window_seconds` are removed from ClickHouse Keeper ... The time is relative to the time of the most recent record, not to the wall time."
- "If more than *_deduplication_window other insert operations occur during the retry sequence, deduplication may not work as intended."
- `insert_deduplication_token`: "ClickHouse doesn't use the hash sum of the data when the token is provided." "`insert_deduplication_token` is tracked per partition". The example shows a third insert with different data but the same token being dropped.
- Switches since 26.2: `deduplicate_insert` default `enable`, "applies to both synchronous and asynchronous inserts, and it supersedes the `insert_deduplicate` and `async_insert_deduplicate` settings". `async_insert_deduplicate` (default 0) is "Legacy. Read only when deduplicate_insert = backward_compatible_choice". "Synchronous and asynchronous inserts now share one deduplication log". `replicated_deduplication_window_for_async_inserts` is now a "Legacy setting retained for mixed-version rolling upgrades".
- Async granularity: "Deduplication works per user query, not per batch: Each queued query contributes one deduplication token to the batch. A token is either the value of `insert_deduplication_token`, when the query provides one, or a hash of the rows that this query contributed."
- "For `INSERT ... VALUES` queries, splitting the inserted data into blocks is deterministic and is determined by settings. Therefore, you should retry insertions with the same settings values as the initial operation."

### ReplacingMergeTree

Sources: `https://clickhouse.com/docs/engines/table-engines/mergetree-family/replacingmergetree.md`, `https://clickhouse.com/docs/concepts/features/operations/update/replacing-merge-tree.md`, `https://clickhouse.com/docs/guides/developer/deduplication.md`
- "removes duplicate entries with the same sorting key value (`ORDER BY` table section, not `PRIMARY KEY`)."
- "Data deduplication occurs only during a merge. ... `ReplacingMergeTree` is suitable for clearing out duplicate data in the background in order to save space, but it does not guarantee the absence of duplicates."
- "Merging of data in ClickHouse occurs at a partition level." The partition key must not change for a row, so duplicates land in the same partition.
- FINAL: "To obtain correct answers, users will need to complement background merges with query time deduplication ... using the `FINAL` operator." "The `FINAL` operator does have a small performance overhead on queries. This will be most noticeable when queries aren't filtering on primary key columns". `do_not_merge_across_partitions_select_final=1` speeds it up. "if the partition key is a prefix of the sorting key, merging across partitions isn't performed at query time" (23.12+).
- "Using `FINAL` works okay if you have a small amount of data. If you're dealing with a large amount of data, using `FINAL` is probably not the best option." An alternative is GROUP BY with argMax/max (deduplication guide).
- `is_deleted`: "`is_deleted` can only be enabled when `ver` is used." Permanently dropping delete rows needs `allow_experimental_replacing_merge_with_cleanup` and `OPTIMIZE ... FINAL CLEANUP` or the `min_age_to_force_merge_*` settings.
- `min_age_to_force_merge_seconds` and `min_age_to_force_merge_on_partition_only` default to 0/false. Setting them lets "older partitions to merge down to a single part over time" (RMT guide).

### Other features asked about

- `deduplicate_merge_projection_mode` (MergeTree setting, default `throw` since 24.8): "Whether to allow create projection for the table with non-classic MergeTree, that is not (Replicated, Shared) MergeTree. ... if allowed, what is the action when merge projections, either drop or rebuild. ... It also controls `OPTIMIZE DEDUPLICATE`". Values `ignore|throw|drop|rebuild`. (`https://clickhouse.com/docs/reference/settings/merge-tree-settings/other.md`). This is about projections on Replacing* tables. It is not a dedup feature.
- `optimize_on_insert` default `1`: "Enables or disables data transformation before the insertion, as if merge was done on this block (according to table engine)." (`https://clickhouse.com/docs/reference/settings/session-settings/optimize.md`). Inference: on a ReplacingMergeTree this collapses duplicates within one inserted block only.
- `clean_deleted_rows`: "Obsolete setting, does nothing." (merge-tree-settings/other)
- "Lightweight dedup": no such feature was found in the fetched docs (unverified that none exists).

### Which gives exactly-once per (tenant, record_id) for a redelivery hours later in a different batch?

| Mechanism | Late redelivery in a different batch | Why (quoted basis) |
|---|---|---|
| Block hash dedup (no token) | No | Hash "covers the whole inserted block". A different batch composition gives a different hash. Window is 3600 s / 10000 blocks. |
| `insert_deduplication_token` per batch | No | Token identifies the insert, not the row. A new batch gets a new token. Window limits still apply. |
| Async insert, one INSERT per record, token = `tenant/record_id` | Only inside the window | "Each queued query contributes one deduplication token", but the token is kept only for `replicated_deduplication_window` (10000) and `_seconds` (3600, relative to the newest). Hours later, past the window, it is not caught. Raising the window "slows down Inserts". |
| ReplacingMergeTree, `record_id` in ORDER BY | Eventually yes, at read time with FINAL | Duplicates are removed at merge "within a partition" and are never guaranteed. FINAL or argMax at query time gives exact results. |

Inference: no mechanism gives exactly-once storage for arbitrarily late redelivery. The practical combination is:
1. ReplicatedReplacingMergeTree with ORDER BY ending in `record_id` (for example `(tenant, device, ts, record_id)`). The partition key is derived from the record's own event time, so a redelivered copy lands in the same partition and sorting key.
2. Readers that need exact counts use FINAL or `argMax`/`any` GROUP BY on `(tenant, record_id)`. Plain scans tolerate rare duplicates.
3. Keep insert-time dedup for the common case (a retry of the same batch inside the window). The sink should re-send the identical batch with the same settings, or set `insert_deduplication_token` to a stable batch identifier (for example the first and last JetStream stream sequence of the batch).

---

## 4. Multi-tenancy

### ClickHouse multi-tenancy guide

Source: `https://clickhouse.com/docs/cloud/bestpractices/multi-tenancy.md` (written for Cloud, but the mechanisms are core).
- Shared table: "data from all tenants is stored in a single shared table, with a field (or set of fields) used to identify each tenant's data. To maximize performance, this field should be included in the primary key. To ensure that you can only access data belonging to your respective tenants we use role-based access control, implemented through row policies."
- "**We recommend this approach as this is the simplest to manage, particularly when all tenants share the same data schema and data volumes are moderate (< TBs)**". "This method is particularly effective for handling a large number of tenants (potentially millions)."
- "In cases where there is a significant gap in data volume between tenants, smaller tenants may experience unnecessary query performance impacts. Note, this issue is largely mitigated by including the tenant field in the primary key."
- Example: `ORDER BY (tenant_id, timestamp)` and `CREATE ROW POLICY user_filter_1 ON default.events USING tenant_id=1 TO user_1`.
- Separate tables and separate databases: "Note this approach doesn't scale for 1000s of tenants." Separate databases are "useful if each tenant requires a large number of tables and possibly materialized views, and has different data schema".
- MergeTree reference: "Don't partition your data by client identifiers or names (instead, make client identifier or name the first column in the ORDER BY expression)." (`https://clickhouse.com/docs/engines/table-engines/mergetree-family/mergetree.md`)

### Row policy semantics

Source: `https://clickhouse.com/docs/sql-reference/statements/create/row-policy.md`, `https://clickhouse.com/docs/operations/access-rights.md`
- "Row policies make sense only for users with readonly access. If a user can modify a table or copy partitions between tables, it defeats the restrictions of row policies."
- USING: "A user can only see rows for which the condition is true ... similar to adding an extra `WHERE` condition to every query the user runs against the table."
- Combination: permissive policies are ORed, restrictive ones ANDed. "Database policies are combined with table policies."
- Default-open trap: "`access_control_improvements.users_without_row_policies_can_read_rows` is enabled by default. A user to whom no condition applies therefore sees every row, and `access_control_improvements.throw_on_unmatched_row_policies`, disabled by default, raises an exception instead".
- Roles: "Roles named in the `TO` section ... are matched against the current user's enabled roles ... so `SET ROLE` can change which policies apply."
- Views and buffers: "A `Buffer` table and a materialized view read through their destination or target table do **not** inherit that table's row policies". An `Alias` table and a `Merge` table do apply them.
- Distributed: "queries to such a table by users the policy applies to are rejected with an `ILLEGAL_PREWHERE` error." Policies belong on local tables. Also: "With `serialize_query_plan = 1` ... a remote server executing such a plan does not apply its own row policies ... Keep `serialize_query_plan = 0` for users whose row policies must be enforced."
- Join engine tables: "`JOIN` and `joinGet` queries against the table fail with `ACCESS_DENIED`" while a policy applies.

Do they hold against arbitrary SELECTs? For a read-only user that reads local MergeTree tables directly, the docs describe them as an implicit WHERE on every read, including through Alias or Merge tables. Documented holes: users without any policy see all rows by default; Buffer tables and MV targets; a SQL SECURITY DEFINER view; `serialize_query_plan=1` with Distributed; and any write or partition-copy privilege.

### `additional_table_filters`

Source: `https://clickhouse.com/docs/reference/settings/session-settings/additional.md`
- "An additional filter expression that is applied after reading from the specified table." Type `Map`, default `{}`. Example: `SETTINGS additional_table_filters = {'table_1': 'x != 2'}`.
- "Filters keyed on a table read through a view with `SQL SECURITY DEFINER` or `NONE` are not applied inside the view."
- Inference: it is a session setting, so a client can override or clear it unless a settings-profile constraint pins it (`CONST`/`READONLY`, section 5). It is a query-shaping aid for a trusted service, not an access-control mechanism. A row policy is the access-control mechanism.

Per-tenant ClickHouse users or roles vs one service user: the docs examples bind policies to users or roles (`TO user_1`). Unverified: a policy keyed on a custom setting (`getSetting('SQL_tenant')`) that a shared service user sets per query. I fetched no page on custom-setting prefixes. Even if it works, the client would control the tenant value unless it is pinned by a constraint.

---

## 5. Query limits that protect ingest

| Control | Default | What it does (quoted) | Source |
|---|---|---|---|
| `readonly` | 0 | "1 - only read requests, as well as changing explicitly allowed settings. 2 - only read requests, as well as changing settings, except for the 'readonly' setting." | session-settings/other.md |
| `max_execution_time` | 0 (s) | "ClickHouse will interrupt a query if the projected execution time exceeds the specified `max_execution_time`." Estimation starts after `timeout_before_checking_execution_speed` (10 s). "It currently cannot stop during merging of aggregation states, nor during most of query analysis". | session-settings/max-execution.md |
| `timeout_overflow_mode` | throw | `throw` or `break` (partial result). | session-settings/timeout-overflow-mode.md |
| `max_memory_usage` | 0 | "The maximum amount of RAM to use for running a query on a single server." Also `max_memory_usage_for_user` (default 0). | session-settings/max-memory-usage.md |
| `max_rows_to_read` | 0 | "The maximum number of rows that can be read from a table when running a query. The restriction is checked for each processed chunk of data, applied only to the deepest table expression". | session-settings/max-rows.md |
| `max_bytes_to_read` | 0 | "The maximum number of bytes (of uncompressed data) that can be read from a table". | session-settings/max-bytes.md |
| `max_concurrent_queries_for_user` | 0 | "The maximum number of simultaneously processed queries per user." | session-settings/max-concurrent.md |
| `max_threads` | auto(N) | "The maximum number of query processing threads ... The smaller the `max_threads` value, the less memory is consumed." | session-settings/max-threads.md |
| Quotas | n/a | "Place restrictions on a set of queries that can be run over a period of time". `CREATE QUOTA ... KEYED BY {user_name | ip_address | ... | client_key | client_key,user_name ...} FOR INTERVAL ... MAX {queries | ... | read_rows | read_bytes | execution_time ...}`. Keyed quotas: "the quota is tracked separately for each key value ... Using keys makes sense only if quota_key is transmitted by the program, not by a user." | `https://clickhouse.com/docs/operations/quotas.md`, `https://clickhouse.com/docs/reference/statements/create/quota.md` |

URLs use the prefix `https://clickhouse.com/docs/reference/settings/session-settings/`.

Inference: a quota keyed by `client_key` plus the clients' `WithQuotaKey` or `ch.Query.QuotaKey` set to the tenant gives per-tenant quotas under one service user.

### Stopping clients from overriding (constraints)

- SQL: `CREATE SETTINGS PROFILE ... SETTINGS variable [= value] [MIN [=] min_value] [MAX [=] max_value] [CONST|READONLY|WRITABLE|CHANGEABLE_IN_READONLY]` (`https://clickhouse.com/docs/reference/statements/create/settings-profile.md`).
- "If the user tries to violate the constraints, an exception is thrown and the setting remains unchanged." "The `readonly` or `const` constraint specifies that the user cannot change the corresponding setting at all." (`https://clickhouse.com/docs/operations/settings/constraints-on-settings.md`)
- Pitfalls on the same page: "Keep `readonly` off the `changeable_in_readonly` list in every profile". "`SET profile` is not access-checked, so any session can select any profile by name." "If there are multiple profiles active for a user, then constraints are merged", and `settings_constraints_replace_previous` is "**true** (recommended)" but "**false** (default)". "The `default` profile is handled uniquely: all the constraints defined for the `default` profile become the default constraints".
- So: yes. A query user's profile can pin `max_execution_time`, `max_memory_usage`, `max_rows_to_read`, `workload` and others with `CONST`, or bound them with `MAX`.

### Workload scheduling (CREATE RESOURCE / CREATE WORKLOAD)

Source: `https://clickhouse.com/docs/operations/workload-scheduling.md`
- "By default, workload scheduling is disabled. To enable it you have to create resources that will be used for scheduling and at least one workload."
- Resources: `CREATE RESOURCE cpu (MASTER THREAD, WORKER THREAD)`, `CREATE RESOURCE memory (MEMORY RESERVATION)`, `CREATE RESOURCE query (QUERY)`, `CREATE RESOURCE r (WRITE DISK d, READ DISK d)`.
- Example close to FlowSeer's case: `CREATE WORKLOAD production IN all SETTINGS max_concurrent_threads = 100`, `CREATE WORKLOAD analytics IN production SETTINGS max_concurrent_threads = 60, weight = 9`, `CREATE WORKLOAD ingestion IN production`. "Analytics has its own limit of 60 concurrent threads, always leaving at least 40 th[reads]".
- "weights defined for workloads are used for max-min fairness and thus only provide best-effort guarantee from below (not a limit or quota from above). All the scheduling is done on every host independently".
- Markup: "`SETTINGS workload = 'name'` ... Setting constraints can be used to make `workload` constant if you want all queries from the user to be marked with fixed value of `workload` setting."
- Query slots: `max_concurrent_queries`, `max_queries_per_second`, `max_waiting_queries` ("When the limit is reached, the server returns an error `SERVER_OVERLOADED`"). "Async insert queries and some specific queries like KILL are not counted towards the limit."
- Limits: "CPU scheduling is not supported for merges and mutations yet." "Memory reservation scheduling is experimental."

Inference for the 2-minute ingest stall: put the query user in a constrained profile (`readonly=1`, CONST `workload='analytics'`, `max_execution_time`, `max_memory_usage`, `max_rows_to_read`/`max_bytes_to_read`, `max_threads`, `max_concurrent_queries_for_user`). Put the sink user in its own `ingestion` workload with a separate CPU slot pool. Also consider `max_memory_usage_for_user` so the query user cannot starve the sink.

---

## 6. Schema features

- **LowCardinality**: "If a dictionary contains less than 10,000 distinct values, then ClickHouse mostly shows higher efficiency ... If a dictionary contains more than 100,000 distinct values, then ClickHouse can perform worse". "Consider using `LowCardinality` instead of Enum when working with strings." (`https://clickhouse.com/docs/sql-reference/data-types/lowcardinality.md`)
- **Enum**: "ClickHouse stores only numbers, but supports operations with the values through their names." 8-bit Enum: "up to 256 values enumerated in the `[-128, 127]` range." (`https://clickhouse.com/docs/sql-reference/data-types/enum.md`)
- **Map**: "maps are not unique in ClickHouse ... internally implemented as `Array(Tuple(K, V))`" and "`m[k]` scans the map, i.e. the runtime of the operation is linear in the size of the map." (`https://clickhouse.com/docs/sql-reference/data-types/map.md`)
- **JSON**: "In ClickHouse Open-Source JSON data type is marked as production ready in version 25.3. It's not recommended to use this type in production in previous versions." (`https://clickhouse.com/docs/sql-reference/data-types/newjson.md`). clickhouse-go JSON append contract: "one serialization version per `JSON` column per block" (README v2.48.0).
- **Variant / Dynamic**: the current reference pages carry no experimental or beta badge or note (`https://clickhouse.com/docs/sql-reference/data-types/variant.md`, `.../dynamic.md`). The production-ready version is not stated on those pages (unverified). Variant warns: "It's not recommended to use similar types as variants ... By default, creating such `Variant` type will lead to an exception".
- **DateTime64 with timezone**: `DateTime64(precision, [timezone])`. "stores data as a number of 'ticks' since epoch start (1970-01-01 00:00:00 UTC) as Int64." The time zone "is the same for the entire column" and affects text display and parsing. At precision 9 the range is "`1677-09-21 00:12:44` to `2262-04-11 23:47:16`". (`https://clickhouse.com/docs/sql-reference/data-types/datetime64.md`)
- **TTL**: "`DELETE` action can be used together with `WHERE` clause to delete only some of the expired rows", for example `TTL time + INTERVAL 1 MONTH DELETE WHERE event != 'error', time + INTERVAL 6 MONTH DELETE WHERE event = 'error'` (`https://clickhouse.com/docs/guides/developer/ttl.md`). TTL "Expressions must evaluate to Date, Date32, DateTime or DateTime64". "TTL is evaluated during background merges, and not at insert time." "The `TTL` clause can't be used for key columns". "Data with an expired `TTL` is removed when ClickHouse merges data parts." "If you perform the `SELECT` query between merges, you may get expired data." `ttl_only_drop_parts` (default 0) drops whole parts once all rows expire. (`https://clickhouse.com/docs/engines/table-engines/mergetree-family/mergetree.md`, merge-tree-settings/other.md). Inference: per-tenant or per-kind retention works either as several `DELETE WHERE` rules or as a TTL on a column-derived expression (for example `ts + toIntervalDay(retention_days)` with `retention_days` stored on the row). The latter is unverified as a documented pattern, but it fits the "deterministic, column-derived values" rule.
- **PARTITION BY**: "In most cases, you don't need a partition key, and if you do need to partition, generally you do not need a partition key more granular than by month. ... For partitioning by month, use the `toYYYYMM(date_column)` expression" (mergetree.md). "a **low-cardinality partitioning key**—with fewer than 100 - 1,000 distinct values - is usually optimal." "Partitioning is primarily a data management technique and not a query optimization tool" (`https://clickhouse.com/docs/best-practices/choosing-a-partitioning-key.md`). Guards: `max_partitions_per_insert_block` default 100; `parts_to_delay_insert` 1000 and `parts_to_throw_insert` 3000 active parts per partition (`.../merge-tree-settings/parts-to.md`). Inference: `toYYYYMM(ts)` or a weekly or daily key only if retention is short (daily partitions × retention days must stay well under ~1000).
- **ORDER BY / primary key**: "prioritize columns frequently used in query filters ... especially those that exclude large numbers of rows." "Columns highly correlated with other data in the table are also beneficial". "4-5 typically sufficient". "Ordering keys must be defined on table creation and can't be added." (`https://clickhouse.com/docs/best-practices/choosing-a-primary-key.md`). Inference: `ORDER BY (tenant, device, ts[, record_id])` matches the access pattern and the multi-tenancy guide's tenant-first advice.
- **Skip and text indexes**: "Text indexes are generally available (GA) in ClickHouse version 26.2 and newer. We strongly recommend using ClickHouse versions >= 26.2 for production use cases." (`https://clickhouse.com/docs/engines/table-engines/mergetree-family/invertedindexes.md`). Syntax: `INDEX text_idx str TYPE text(tokenizer = splitByNonAlpha | ngrams[(N)] | ... )`. Phrase search (`support_phrase_search`) "is experimental". `tokenbf_v1` and `ngrambf_v1` are listed as "*(Deprecated)*": "With general availability (GA) of the `text` index starting from ClickHouse version 26.2, the `tokenbf_v1` index is no longer recommended for full text search." (mergetree.md). Bloom-filter vs text: text indexes "are rather large (dozens to hundreds of megabytes per part)".
- **Codecs**: default "`lz4` compression in the self-managed version". "`DoubleDelta` ... Optimal compression rates are achieved for monotonic sequences with a constant stride, such as time series data." "`Gorilla` ... The smaller the difference between consecutive values is ... the better the compression rate." "Delta is a data preparation codec, i.e. it cannot be used stand-alone." `ZSTD[(level)]` "Default level: 1". `T64` crops unused high bits of integers. Example: `timestamp DateTime CODEC(DoubleDelta)`, `slow_values Float32 CODEC(Gorilla)`. `ALP` is "in beta". (`https://clickhouse.com/docs/reference/statements/create/table/codec.md`). Inference for interface counters (monotonic UInt64): `CODEC(Delta, ZSTD)` or `DoubleDelta, ZSTD`. Timestamps: `DoubleDelta, ZSTD`. Gauges in Float64: `Gorilla` or `FPC`.
- **AggregatingMergeTree and MVs for rollups**: "You can use `AggregatingMergeTree` tables for incremental data aggregation, including for aggregated materialized views." (`https://clickhouse.com/docs/engines/table-engines/mergetree-family/aggregatingmergetree.md`). Interplay with dedup: "Deduplication in the tables under materialized views is additionally governed by ... `deduplicate_blocks_in_dependent_materialized_views`, which is enabled by default since version 26.2." Under async inserts "If the view emits a second block, ClickHouse throws a `NOT_IMPLEMENTED` exception." (dedup-on-retries guide). Inference: a redelivered duplicate that slips past insert dedup is double-counted in a SummingMergeTree or AggregatingMergeTree rollup unless the aggregate is idempotent (for example `uniqExact(record_id)`, or max for counters).
- **Projections**: "Lightweight updates and deletes aren't supported for tables with projections." "Projections don't allow using different TTL for the source table and the (hidden) target table". Since 25.5, `_part_offset` lets a projection "Store only the sorting key + `_part_offset`". (`https://clickhouse.com/docs/data-modeling/projections.md`). On ReplacingMergeTree, `deduplicate_merge_projection_mode` defaults to `throw` (section 3).

---

## 7. Schema migrations from Go

| Tool | Latest | Published | ClickHouse driver dep | Source |
|---|---|---|---|---|
| golang-migrate v4 | v4.20.1 | 2026-09-09 | `github.com/ClickHouse/clickhouse-go v1.4.3` | `https://proxy.golang.org/github.com/golang-migrate/migrate/v4/@v/v4.20.1.mod` |
| pressly/goose v3 | v3.28.0 | 2026-09-02 | `github.com/ClickHouse/clickhouse-go/v2 v2.48.0` | `https://proxy.golang.org/github.com/pressly/goose/v3/@v/v3.28.0.mod` |

Both are older than 14 days.

- golang-migrate links clickhouse-go **v1**. The Go docs say "v1 of the driver is deprecated and won't reach feature updates or support for new ClickHouse types." (Go docs index). Its README: "Clickhouse cluster mode is not officially supported, since it's not tested right now". `x-multi-statement` "splits the migration text into separately-executed statements by a semi-colon" and "The queries are not executed in any sort of transaction/batch". The default migrations table engine is TinyLog. (`https://raw.githubusercontent.com/golang-migrate/migrate/master/database/clickhouse/README.md`)
- goose (doc fetched from `main`, `https://raw.githubusercontent.com/pressly/goose/main/doc/dialect-clickhouse.md`): "The current Goose `clickhouse` migrator is _not_ well suited for use on clustered `ClickHouse` with the `Replicated` engine, or on the `Atomic` engine using `WITH CLUSTER` DDL." "All migrations must be annotated with `-- +goose NO TRANSACTION`". "It is therefore only safe to use Goose to run DDL that can be harmlessly run repeatedly." For a Replicated database: "possible ... _only_ by manually pre-creating the Goose db version table" as `ReplicatedMergeTree()`, and the user should have "`select_sequential_consistency=1`, `insert_quorum='auto'`, and `insert_quorum_parallel=0`". "External co-ordination is still recommended to prevent concurrent Goose executions".

### ON CLUSTER vs `DATABASE ENGINE = Replicated`, and the operator's recommendation

- Operator: "**Best practice** Always use the Replicated database engine for production deployments." with `CREATE DATABASE my_database ON CLUSTER 'default' ENGINE = Replicated;`. "Non-replicated database engines (Atomic, Lazy, SQLite, Ordinary) require manual schema management: Tables must be created individually on each replica ... Schema drift can occur between nodes". Defaults: "Cluster named 'default' containing all ClickHouse nodes", macros `{cluster}`, `{shard}`, `{replica}`, and "Replicated storage for Role Based Access Control(RBAC) entities". (`https://clickhouse.com/docs/products/kubernetes-operator/guides/introduction.md`)
- Operator database sync: `enableDatabaseSync: true # Default: true`. "the `default` database is converted to the Replicated engine and the schema is synchronized" before a new replica is published. (`https://clickhouse.com/docs/products/kubernetes-operator/guides/configuration.md`)
- Replicated DB engine: "supports replication of metadata via DDL log being written to ZooKeeper and executed on all of the replicas". "the DDL request tries to execute on the initiator ... If the request has been successfully completed on the initiator, then all other hosts will automatically retry until they complete it." "for a `Replicated` database it is better to set [`distributed_ddl_output_mode`] to `null_status_on_timeout`". "The data is replicated at the `ReplicatedMergeTree` level, i.e. if the table is not replicated, the data will not be replicated (the database is responsible only for metadata)." `ReplicatedMergeTree` with no arguments uses "`/clickhouse/tables/{uuid}/{shard}` and `{replica}`". (`https://clickhouse.com/docs/engines/database-engines/replicated.md`)

Inference: with the operator, use one `Replicated` database, and write plain `CREATE TABLE ... ENGINE = ReplicatedReplacingMergeTree` without `ON CLUSTER` and without Keeper path arguments. Neither golang-migrate (clickhouse-go v1, cluster mode untested) nor goose (needs a hand-made replicated version table and idempotent DDL) is a clean fit. A small in-house migrator is a reasonable alternative: idempotent `CREATE ... IF NOT EXISTS` and `ALTER ... IF [NOT] EXISTS`, a `ReplicatedMergeTree` version table, and a single runner.

---

## 8. Protobuf input format

Sources: `https://clickhouse.com/docs/interfaces/formats/Protobuf.md`, `https://clickhouse.com/docs/interfaces/formats/ProtobufList.md`
- "This format requires an external format schema, which is cached between queries." "ClickHouse supports: both `proto2` and `proto3` syntaxes." Editions (FlowSeer uses edition 2024) are not mentioned (unverified, likely unsupported).
- Mapping is by name: "ClickHouse compares their names. This comparison is case-insensitive and the characters `_` (underscore) and `.` (dot) are considered as equal." Nested messages map to `x.y.z` / `x_y_z` columns.
- Framing: "ClickHouse inputs and outputs protobuf messages in the `length-delimited` format. ... before every message its length should be written as a varint". `ProtobufSingle` handles a single message without a length prefix.
- `ProtobufList`: "rows are represented as a sequence of sub-messages contained in a message with a fixed name of 'Envelope'." Marked "Not supported in ClickHouse Cloud".
- Defaults: "Ordinary **non-nullable** mapped columns use the protobuf schema field default ... not the table `DEFAULT` expression." `Nullable(...)` columns become NULL when absent. oneof presence needs `input_format_protobuf_oneof_presence` and an Enum column.
- Schema delivery: `format_schema='schemafile:MessageType'` from the server's `format_schema_path`, or `format_schema_source='string'` with the literal `.proto` content, or `'query'`. "To reload the Protobuf schema loaded from `format_schema_path` use the `SYSTEM DROP ... FORMAT CACHE` statement."
- Go path: clickhouse-go `InsertFormat(ctx, format, query, reader)` is "**Experimental**: the API may change or be removed in a future minor release" and "**HTTP protocol only**". ch-go documents no raw-format insert. (clickhouse-go README v2.48.0)

Inference: the sink could forward JetStream protobuf payloads as length-delimited `Protobuf` over HTTP via the experimental `InsertFormat`. The costs: the `.proto` must be shipped to the server (file or inline string) and match edition syntax ClickHouse may not parse; mapping is by field name, so renames silently change columns; no `google.protobuf.Timestamp` handling is documented (only `*Value` wrappers via `input_format_protobuf_flatten_google_wrappers`); and tenancy columns or computed fields must be in the message. Decoding in Go and inserting Native columns keeps type checking in FlowSeer and works over TCP.

---

## Unverified items

- Whether async-insert flushes honour `insert_quorum`.
- Exact production-ready version for Variant and Dynamic.
- Row policies keyed on custom `SQL_*` settings for a shared service user.
- ClickHouse support for protobuf editions syntax and for well-known types beyond wrappers.
- A "lightweight dedup" feature: none found.
