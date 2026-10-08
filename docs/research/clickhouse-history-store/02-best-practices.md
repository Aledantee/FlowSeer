---
title: ClickHouse best practices and features for the history store
date: 2026-10-08
status: research; sources fetched 2026-10-08, targets ClickHouse 26.8 LTS
---

# ClickHouse best practices and features for the FlowSeer history store (deep pass, fetched 2026-10-08)

Scope. This pass builds on [`01-clients-durability-dedup.md`](01-clients-durability-dedup.md), the first pass, and does not repeat it. Where
a source disagrees with that file, the correction is marked **Correction**. Every claim cites a URL
fetched on 2026-10-08 with a short quote. "Inference" marks reasoning from quoted text; "unverified"
marks what no fetched source confirms. Findings are numbered F1 to F74 for cross reference.

The legacy store is treated as a source of lessons only (scale, the 2 minute insert stall from one
analytic query, the 8.6B-row set table). Nothing below adopts its shape; each schema choice is
derived from ClickHouse guidance and FlowSeer's access patterns.

Docs pages were fetched as Markdown (`<page>.md`) where that URL exists. Several settings pages are
split into group pages with non-obvious names; where a page could not be located it is said so.

---

## 0. Versions in play (verify of the "26.8 and 26.3 LTS" premise)

**F1. Supported lines.** `https://raw.githubusercontent.com/ClickHouse/ClickHouse/master/SECURITY.md`
lists supported versions: "26.9 ✔️, 26.8 ✔️, 26.7 ✔️, 26.6 ❌, 26.5 ❌, 26.4 ❌, 26.3 ✔️, 26.2 ❌ ...".
The file does not label LTS, but 26.3 being supported while 26.4 to 26.6 are not is the LTS pattern.
The 26.8 release post says 26.8 "is a long-term support release"
(`https://clickhouse.com/blog/clickhouse-release-26-08`, dated September 10, 2026), and the 26.3 post
says "starting with 26.3 LTS" (`https://clickhouse.com/blog/clickhouse-release-26-03`, April 7, 2026).
GitHub releases: `v26.8.20.9-lts` published 2026-10-07, `v26.7.24.8-stable` 2026-10-07
(`https://api.github.com/repos/ClickHouse/ClickHouse/releases?per_page=12`). Premise confirmed.

**F2. Pin.** 26.8 LTS. The reason to prefer it over 26.3 LTS: 26.9 made the analyzer mandatory
("Obsolete since v26.9. The analyzer can no longer be disabled",
`https://clickhouse.com/docs/reference/settings/session-settings/allow-experimental.md`), and
26.8 gets the `icu`/`splitByRegexp` tokenizers and `enable_adaptive_codec_selection` (below). 26.3
already has text index GA, unified dedup and async-insert-by-default, so either works for the
features FlowSeer needs.

---

## 1. Official best-practice pages (quoted guidance, applied)

### 1.1 Primary key / ORDER BY

**F3.** `https://clickhouse.com/docs/best-practices/choosing-a-primary-key.md`: prioritize columns
"frequently used in query filters" and "especially those that exclude large numbers of rows";
columns "highly correlated with other data in the table" also help; "4-5 typically sufficient";
"Ordering keys must be defined on table creation and can't be added"; later orderings via projections
"result in data duplication". Date granularity: the example uses `toDate(CreationDate)` because a date
"needs fewer bits, which produces a smaller index" (paraphrase of the page).

**F4.** `https://clickhouse.com/docs/guides/best-practices/sparse-primary-indexes.md`: "order the
primary key columns by cardinality in ascending order"; generic exclusion search is "most effective
when the predecessor key column has low(er) cardinality"; "a query that filters on the second key
column doesn't benefit much from the second key column being in the index" when both are high
cardinality; alternatives are a second table, a materialized view, or a projection ("the most
transparent option").

**F5.** Observability schema page adds: "best to order the keys in ascending order of cardinality"
but "filtering on later key columns is less efficient, so test variants"; "Don't use keys in attribute
maps for the ordering key"; timestamps "have been shown to cause slow query performance if this column
is used in the primary/ordering key" yet the default `otel_logs` key still ends with
`toUnixTimestamp(Timestamp)` (`https://clickhouse.com/docs/use-cases/observability/schema-design.md`).

**F6.** Multi-tenancy guide: tenant field "should be included in the primary key"; "Don't partition
your data by client identifiers or names (instead, make client identifier or name the first column in
the ORDER BY expression)" (`https://clickhouse.com/docs/cloud/bestpractices/multi-tenancy.md`,
`https://clickhouse.com/docs/engines/table-engines/mergetree-family/mergetree.md`, both quoted in the
first pass).

Applied to FlowSeer (inference): every table starts `ORDER BY (tenant_id, device_id, <entity>, ts)`.
Tenant first (low cardinality, every query filters on it, row policies filter on it), device second
(every per-device query), entity (ifindex, radio, MAC) third, time last. "Tenant + site last 24h"
queries do not have site in the key; they resolve site to a device list in Postgres/NATS KV and filter
`device_id IN (...)`, which the primary index serves well because device_id is the second key column.
Do not put `toDate(ts)` in the key for sample tables; `ts` is already the last column and
`optimize_read_in_order` covers newest-first (F70).

### 1.2 Partitioning key

**F7.** `https://clickhouse.com/docs/best-practices/choosing-a-partitioning-key.md`: "Partitioning is
primarily a data management technique and not a query optimization tool"; "a low-cardinality
partitioning key—with fewer than 100 - 1,000 distinct values - is usually optimal"; "ClickHouse only
merges data parts within, but not across partitions"; "too many partitions will result in too many
unmerged parts"; "ClickHouse creates one new data part for each unique partition key value among the
inserted rows"; "querying across all partitions can be slower than using a non-partitioned table";
"ClickHouse automatically builds MinMax indexes on partition columns"; "If you're unsure whether
partitioning is necessary, you may want to start without it"; it should align "with your data life
cycle policies (e.g., retention via TTL)".

**F8.** ClickStack TTL guidance: "We recommend always using the setting ttl_only_drop_parts=1" and
"If data is partitioned by the same unit at which you perform TTL expiration e.g. day" parts hold one
interval so whole parts drop (`https://clickhouse.com/docs/clickstack/managing/ttl.md`). The
observability build-your-own page: "queries which need to cover many partitions may perform worse
than if no partitioning is used" (`https://clickhouse.com/docs/guides/use-cases/observability/build-your-own/managing-data.md`).

Applied (inference, not legacy): choose the partition unit from retention so that
`retention / unit` stays well under 1,000 and each insert touches one or two partitions. Daily for
logs/traps with retention up to ~90 days; weekly (`toMonday(ts)` or `toStartOfWeek`) for
samples/changes with retention of a year or two (52 to 104 partitions); monthly only when retention
is multi-year. Never tenant in the partition key (F6). Per-tenant retention differences are handled by
TTL `DELETE WHERE` rules, not partitions (F36).

### 1.3 Data types

**F9.** `https://clickhouse.com/docs/best-practices/select-data-types.md`: "Nullable columns
introduce additional overhead by maintaining separate columns for tracking null values"; "Only use
Nullable if explicitly required to distinguish between empty and null states"; "For columns with
fewer than approximately 10,000 unique values, use LowCardinality types"; "prefer Enum types for
columns with a finite set of possible values"; "Choose the most coarse-grained date or datetime type
that meets query requirements"; "prefer DateTime over DateTime64 unless millisecond or finer precision
is essential"; "Select numeric types with minimal bit-width that still accommodate the expected data
range"; "use FixedString only when the column values are strictly fixed-length strings".
(The separate `avoid-nullable-columns.md` URL returns 404; the guidance lives on this page.)

**F10.** LowCardinality page: "less than 10,000 distinct values ... higher efficiency", "more than
100,000 distinct values ... can perform worse"; "Consider using LowCardinality instead of Enum when
working with strings"; "LowCardinality is not efficient for some data types" (see
`allow_suspicious_low_cardinality_types`) (`https://clickhouse.com/docs/sql-reference/data-types/lowcardinality.md`).

**F11.** UUID: "16-byte value"; UUIDs are "sorted by their second half", which hurts UUIDv7 time
locality in a primary index; workaround index on `UUIDv7ToDateTime(uuid)`
(`https://clickhouse.com/docs/sql-reference/data-types/uuid.md`). IPv6: "Stored in 16 bytes as
UInt128 big-endian"; `toIPv4('127.0.0.1') = toIPv6('::ffff:127.0.0.1')` is 1, so IPv4-mapped
comparison works (`https://clickhouse.com/docs/sql-reference/data-types/ipv6.md`).

**F12.** Time/Time64: "`Time` represents a time with hour, minute, and second components", range
"[-999:59:59, 999:59:59]", no time zones (`https://clickhouse.com/docs/sql-reference/data-types/time.md`).
Settings page: `enable_time_time64_type` "25.12: enabled by default", "25.6: added as new experimental
types" (`https://clickhouse.com/docs/reference/settings/session-settings/enable.md`). 26.9 adds
"`+` and `-` between `DateTime` and `Time`" (`https://clickhouse.com/docs/whats-new/changelog/index.md`).
Not needed by FlowSeer; durations are plain integers.

Applied (inference): `tenant_id LowCardinality(String)` (thousands of tenants is under 10k; the
value is a UUID string or "default", so `UUID` type is impossible without a sentinel).
`device_id UUID` (16 bytes, fixed) or a UInt64 surrogate if one exists; not LowCardinality (hundreds
of thousands across tenants). `ts DateTime` (second precision suffices for SNMP polls) unless a domain
truly carries sub-second timestamps (syslog may; use `DateTime64(3)`). Status enums as `Enum8`
(validated at insert, 1 byte) where the value set is closed by the protobuf enum; `LowCardinality(String)`
where it is open (vendor strings). No Nullable: counters that are absent are stored as a default plus a
presence bitmask or are simply omitted rows in a narrow layout (F76).

### 1.4 JSON type

**F13.** `https://clickhouse.com/docs/best-practices/use-json-where-appropriate.md`: use JSON when
"Your data has a dynamic or unpredictable structure with varying keys across documents"; with a fixed
schema "use normal columns, `Tuple`, `Array`, `Dynamic`, or `Variant` types instead"; opaque documents
"should be stored as a `String` field"; mixed explicit columns plus one JSON column is "generally
preferred"; "keep it below 10,000" for `max_dynamic_paths`. JSON "production-ready" since 25.3
(`https://clickhouse.com/blog/clickhouse-release-25-03`). Applied: FlowSeer rows are typed protobuf;
no JSON column except possibly for syslog structured data (see F49).

### 1.5 Insert strategy and batch sizes

**F14.** (first pass covered the synchronous guidance.) OTel collector page repeats: "at least 1,000
rows", "at least 10,000 events can go in each insert", "values up to 100,000 can be used if memory
allows"; "If large batches can't be guaranteed, you can delegate batching to ClickHouse using
Asynchronous Inserts"; "For MergeTree-family tables, ClickHouse automatically deduplicates inserts, so
retrying an unacknowledged insert with the same data and order is safe"
(`https://clickhouse.com/docs/use-cases/observability/clickstack/ingesting-data/otel-collector.md`).

**F15. Correction on the async-insert default version.** The settings page says "26.2 / 1 / Enable
async inserts by default" (`https://clickhouse.com/docs/reference/settings/session-settings/async-insert.md`),
while the 26.3 release post says "starting with 26.3 LTS, asynchronous inserts are enabled by default"
(`https://clickhouse.com/blog/clickhouse-release-26-03`). Two ClickHouse sources disagree by one
release; the operational conclusion is the same: on 26.3+ the sink must set `async_insert=0`
explicitly for synchronous semantics. Related defaults: `async_insert_max_data_size` 10485760 (OSS),
`async_insert_busy_timeout_max_ms` 200, `async_insert_use_adaptive_busy_timeout` 1 (same page).

**F16.** 26.2 adds "Time-based insert batching" `input_format_max_block_wait_ms` with
`input_format_connection_handling` and names `min_insert_block_size_rows` 1,000,000 and
`min_insert_block_size_bytes` 268 MB as the block flush defaults (`https://clickhouse.com/blog/clickhouse-release-26-02`).
Inference: FlowSeer batches are far below one block, so each INSERT is one block, one part per
partition touched.

### 1.6 Mutations and OPTIMIZE FINAL

**F17.** `https://clickhouse.com/docs/best-practices/avoid-mutations.md`: mutations "rewrite entire
data parts affected by the change"; "avoid frequent or large-scale mutations, especially on high-volume
tables"; "can't be rolled back once submitted"; alternatives are ReplacingMergeTree /
CollapsingMergeTree, lightweight deletes, and partition drops. "Inserts are not blocked by mutations."

**F18.** `https://clickhouse.com/docs/best-practices/avoid-optimize-final.md`: "initiates resource
intensive operations which may impact cluster performance"; normally merges avoid parts above ~150 GB
but `OPTIMIZE FINAL` "ignores this safeguard"; acceptable only for "finalizing data before freezing a
table or exporting"; query-time `FINAL` is "generally fine when queries filter on primary key columns".

### 1.7 Skip indexes

**F19.** `https://clickhouse.com/docs/best-practices/use-data-skipping-indices-where-appropriate.md`:
consider them only "when previous best practices have been followed"; they suit "Columns with high
overall cardinality but low cardinality within a block" and "Rare values that are critical for search
(e.g. error codes, specific IDs)"; effectiveness needs "a strong correlation between the indexed column
and the table's primary key"; "If even a single matching value exists in a block, that entire block
must still be read"; `tokenbf_v1`/`ngrambf_v1` "Deprecated in ClickHouse versions >= 26.2 in favor of
text indexes"; verify with `EXPLAIN indexes=1`. The observability page: "we find these to be generally
ineffective and don't recommend copying them into your custom schema" (schema-design.md).
Applied: a `minmax` on a secondary time or duration column is cheap; a `bloom_filter` on MAC address
in FDB/clients tables is justified ("rare values ... specific IDs") because "which switch port saw MAC
X" is a real query that does not filter on device; measure before keeping.

### 1.8 Materialized views

**F20.** `https://clickhouse.com/docs/best-practices/use-materialized-views.md`: incremental views
"shift the computational cost to insert time", "In most cases they will have no appreciable impact on
overall cluster performance", best for "aggregations over a single table"; refreshable views
"re-execute their full query and overwrite the result in the target table", suit "complex joins or
denormalization across multiple tables", must not be scheduled faster than they run, and
"APPEND adds rows ... suits history or periodic snapshots".

**F21.** Refreshable MV reference (`https://clickhouse.com/docs/sql-reference/statements/create/view.md`):
syntax `REFRESH [EVERY|AFTER interval [OFFSET interval]] [RANDOMIZE FOR interval] [DEPENDS ON ...]
[SETTINGS ...] [APPEND [INCREMENTAL]] [TO ...] [EMPTY]`; "Replicas coordinate through Keeper so only
one replica refreshes at each scheduled time"; "`ReplicatedMergeTree` is required so all replicas see
the refreshed data"; with APPEND, `SETTINGS all_replicas = 1` lets replicas refresh independently;
`APPEND INCREMENTAL` (26.9, `https://clickhouse.com/blog/clickhouse-release-26-09`) "processes only
rows committed since the previous refresh", requires a single plain MergeTree source with
`enable_block_number_column = 1` and `enable_block_offset_column = 1` and rejects JOIN/UNION/subqueries;
monitor via `system.view_refreshes`; `SYSTEM STOP|START|REFRESH|WAIT|CANCEL VIEW`. The guide warns
views "not coordinated through ClickHouse Keeper forget when they last refreshed"
(`https://clickhouse.com/docs/materialized-view/refreshable-materialized-view.md`). Production-ready
version: not stated on either page (unverified).

**F22.** 26.8 "Atomic POPULATE": "When you call `CREATE MATERIALIZED VIEW ... POPULATE`, the view is
subscribed to new inserts on the source table" during backfill (`https://clickhouse.com/blog/clickhouse-release-26-08`).
Useful when adding a rollup MV to a live samples table.

### 1.9 Joins

**F23.** `https://clickhouse.com/docs/best-practices/minimize-optimize-joins.md`: "denormalization
is strongly recommended" for latency-sensitive queries; dictionaries "don't allow duplicate keys" and
a one-to-many dictionary join "will result in silent data loss"; direct join via dictionary is "the
fastest method for point lookups"; "As of ClickHouse 24.12, the query planner now automatically places
the smaller table on the right side"; avoid more than 3 to 4 joins per query.

---

## 2. Time series and counters

### 2.1 Codecs

**F24.** `https://clickhouse.com/docs/reference/statements/create/table/codec.md`: `ZSTD(level)`
levels 1-22, "Default level: 1"; `Delta` "is a data preparation codec, i.e. it cannot be used
stand-alone" (its `delta_bytes` argument "is deprecated"); `DoubleDelta` "Can be used with any numeric
type", suited to monotonic series such as timestamps; `GCD()` "Can be used with integer, decimal and
date/time columns" (also a preparation codec); `Gorilla(bytes_size)` XOR of consecutive floats;
`FPC(level, float_size)` levels 1-28, default 12; `T64` "crops unused high bits of values in integer
data types", applies to integers, Enum, Date, DateTime; `ALP(variant)` is Beta, Float32/64, needs
`SET enable_alp_codec = 1`; `ZXC`, `SZ3`, `Quantized` are experimental; "Codecs can be combined in a
pipeline, for example, `CODEC(Delta, Default)`"; adaptive codec selection is experimental via the
MergeTree setting `enable_adaptive_codec_selection` (added 26.8, default 0,
`https://clickhouse.com/docs/reference/settings/merge-tree-settings/enable.md`).

**F25.** Compression guide (`https://clickhouse.com/docs/data-compression/compression-in-clickhouse.md`):
"`Delta`-based codecs work well whenever you have monotonic sequences or small deltas in consecutive
values"; "`Gorilla` can be effective on floating point data, specifically that which represents gauge
readings"; "`T64` can be effective on sparse data or when the range in a block is small"; "Avoid `T64`
for random numbers"; ZSTD level 1 is the starting point and "We rarely see sufficient benefits on
values higher than 3"; DoubleDelta "typically adds little if the first-level derivative from `Delta` is
already very small"; measure with `system.columns` (`data_compressed_bytes` / `data_uncompressed_bytes`).
Also `estimateCompressionRatio` exists since 25.3 for trying codecs before loading
(`https://clickhouse.com/blog/clickhouse-release-25-03`).

Applied (inference): `ts DateTime CODEC(Delta, ZSTD(1))` (ClickStack uses `Delta(8), ZSTD(1)` on
DateTime64(9)); cumulative counters `UInt64 CODEC(Delta, ZSTD(1))` because within one (device,
interface) run the deltas are small and positive; gauges in Float32/64 `CODEC(Gorilla, ZSTD(1))`
or plain ZSTD when values jump; small-range integers (signal dBm, channel, VLAN) `CODEC(T64, ZSTD(1))`;
strings `ZSTD(1)`. Because the ORDER BY groups rows of one interface contiguously, Delta sees the
true per-counter stride, which is the condition the guide names.

### 2.2 Rates over cumulative counters with resets

**F26.** `runningDifference`, `runningAccumulate`, `neighbor` are deprecated: "The internal state of
runningDifference state is reset for each new block", "Because of this error-prone behavior, the
function is deprecated", re-enable only with `allow_deprecated_error_prone_window_functions`
(`https://clickhouse.com/docs/sql-reference/functions/other-functions.md`). Use window functions.

**F27.** `deltaSum` (v21.3): "Sums the arithmetic difference between consecutive rows. If the
difference is negative, it is ignored"; "The underlying data must be sorted for this function to work
properly"; for MVs "you most likely want to use the deltaSumTimestamp function instead"
(`https://clickhouse.com/docs/sql-reference/aggregate-functions/reference/deltasum.md`).
`deltaSumTimestamp(value, timestamp)` (v21.6): orders by the timestamp argument so states "stay
correct when parts merge"; negative differences ignored
(`https://clickhouse.com/docs/sql-reference/aggregate-functions/reference/deltasumtimestamp.md`).
Inference: ignoring a negative delta drops the post-reset increment but never produces a negative
rate, which is the standard "discontinuity-aware" behaviour; it under-counts across a reset by the
amount accumulated after the reset within that pair of samples. For SNMP this is acceptable; to be
exact, store the device's `sysUpTime`/`ifCounterDiscontinuityTime` with each sample and compute
rates only between samples whose discontinuity time matches (schema below).

**F28.** `nonNegativeDerivative(metric_column, timestamp_column[, INTERVAL X UNITS]) OVER (...)`:
"`timestamp_column` must be strictly increasing in the window's evaluation order"; returns 0 for the
first row and when elapsed time is non-positive; "Negative results are clamped to `0`" which the page
describes as useful for counters where "a decrease usually signals a reset"
(`https://clickhouse.com/docs/sql-reference/window-functions/nonNegativeDerivative.md`). Window
functions page: `lagInFrame`/`leadInFrame` "respect the window frame"; default frame is
`RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW`; for lag semantics use
`ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING`
(`https://clickhouse.com/docs/sql-reference/window-functions/index.md`).

**F29. `timeSeries*ToGrid` family (PromQL-like).** `timeSeriesRateToGrid(start, end, step, staleness)
(timestamp, value)` "calculates PromQL-like rate over time series data on the specified grid", returns
`Array(Nullable(Float64))`, "The staleness window is a left-open and right-closed interval", introduced
v25.6 (`https://clickhouse.com/docs/sql-reference/aggregate-functions/reference/timeSeriesRateToGrid.md`).
Gate: `enable_time_series_aggregate_functions` (default 0, aliases
`allow_experimental_time_series_aggregate_functions`, `allow_experimental_ts_to_grid_aggregate_function`),
"26.9: moved to the private preview tier", "25.6: added", "25.1: cloud only"
(`https://clickhouse.com/docs/reference/settings/session-settings/enable.md`). **Correction to the
web-search summary** that named only the `allow_experimental_ts_to_grid...` setting: that is the
alias; the canonical name is `enable_time_series_aggregate_functions`. The page does not describe
counter-reset handling (unverified for the function; the PromQL blog says the implementation aims at
"PromQL's exact semantics, including reset handling", `https://clickhouse.com/blog/introducing-promql`).
Siblings named in the intro of the time-series functions page: `timeSeriesInstantRateToGrid`,
`timeSeriesLastToGrid`, `timeSeriesResampleToGridWithStaleness`
(`https://clickhouse.com/docs/sql-reference/functions/time-series-functions.md`); others listed in the
task brief (DeltaToGrid, ChangesToGrid, ResetsToGrid, DerivToGrid, PredictLinearToGrid) were not
confirmed by a fetched page (unverified).

**F30. Recommendation for FlowSeer rate queries.** Chart rates with
`nonNegativeDerivative(counter, ts, INTERVAL 1 SECOND) OVER (PARTITION BY tenant_id, device_id, ifindex ORDER BY ts)`
and then `avg()` per `toStartOfInterval(ts, INTERVAL 1 HOUR)`, or with `deltaSum` per bucket divided
by the bucket's time span. Keep `timeSeriesRateToGrid` out of production until it leaves private
preview; it is the right tool for Prometheus-exact semantics and could replace the window version
later without schema change. Precompute hourly rollups with an incremental MV into
AggregatingMergeTree using `deltaSumTimestampState(counter, ts)` plus `max(counter)`, `min(ts)`,
`max(ts)`, so a chart over months reads the rollup (F33).

### 2.3 Gap filling and bucketing

**F31.** `ORDER BY expr WITH FILL [FROM] [TO] [STEP] [STALENESS] ... [INTERPOLATE (...)]`: STEP
defaults "1 day for Date, and 1 second for DateTime"; "order of filling will follow the order of
fields in the ORDER BY clause"; "`INTERPOLATE` can be applied to columns not participating in
`ORDER BY WITH FILL`" and repeats the previous value when `expr` is omitted; `STALENESS` "generates
rows only until the gap from the previous original row exceeds the given value"
(`https://clickhouse.com/docs/sql-reference/statements/select/order-by.md`). Versions not stated on
the page (unverified). Inference: with multiple series in one result, fill per series by putting
the series key before the time column in ORDER BY and using `WITH FILL` only on time; `STALENESS`
stops filling across a device that went silent for days.

**F32.** `toStartOfInterval(value, INTERVAL x unit[, origin[, time_zone]])`, units YEAR to
NANOSECOND, aliases `date_bin`, `time_bucket`; "the calculation is always performed relative to
00:00:00 (midnight) of the current day" for hour units; weeks start Monday
(`https://clickhouse.com/docs/sql-reference/functions/date-time-functions.md`). `toStartOfHour`,
`toStartOfFifteenMinutes`, `toStartOfMinute` accept DateTime64 and return DateTime or DateTime64;
`enable_extended_results_for_datetime_functions` changes the return type (same page).

### 2.4 Downsampling: AggregatingMergeTree, TTL GROUP BY, tiered storage

**F33.** AggregatingMergeTree "replaces all rows with the same primary key ... with one row" storing
combined states; read with "GROUP BY clause and the same aggregate functions as when inserting data,
but using the -Merge suffix"
(`https://clickhouse.com/docs/engines/table-engines/mergetree-family/aggregatingmergetree.md`).
`SimpleAggregateFunction` supports any, anyLast, min, max, sum, sumWithOverflow, groupBit*,
groupArrayArray, groupUniqArrayArray, sumMap, minMap, maxMap, timeSeriesGroupArray; "performs better
than AggregateFunction for the same functions"; argMin/argMax are not listed (so an argMax column needs
`AggregateFunction(argMax, ...)`) (`https://clickhouse.com/docs/sql-reference/data-types/simpleaggregatefunction.md`).

**F34.** TTL GROUP BY: "The GROUP BY expression must be a prefix of the table's primary key";
non-grouped, non-SET columns get "an arbitrary value from the grouped rows, as if `any` were applied";
example `TTL d + INTERVAL 1 MONTH GROUP BY k1, k2 SET x = max(x), y = min(y)`
(`https://clickhouse.com/docs/engines/table-engines/mergetree-family/mergetree.md`). The TTL guide's
`hits` example adds `toStartOfDay(timestamp)` to the PRIMARY KEY for this reason and notes "Setting the
default value of `max_hits` and `sum_hits` to `hits` is necessary for our logic to work"
(`https://clickhouse.com/docs/guides/developer/ttl.md`). "there should be no more than one `DELETE`
rule" per table (mergetree.md). Inference: TTL GROUP BY rolls up in place inside the raw table and
forces the bucket expression into the primary key, which conflicts with `ORDER BY (..., ts)` for raw
queries. For FlowSeer the cleaner design is raw table with a plain DELETE TTL plus a separate rollup
table fed by an incremental MV with its own, longer TTL. TTL GROUP BY is a fit only for a table that
is already bucketed (the rollup table itself: hourly rows rolled to daily after N months).

**F35.** Tiered storage: `TO DISK` / `TO VOLUME` TTL rules; "The GROUP BY ... moving or recompressing,
all rows in a part must meet the expression"; `move_factor` default 0.1; "Data is never moved from the
last volume back to the first"; S3 disk `<type>s3</type>` plus a `cache` disk wrapping it; tiered
policy "Move data from a local SSD volume to an S3 volume as it ages" (mergetree.md). Recompression
example `TTL Timestamp + INTERVAL 4 DAY RECOMPRESS CODEC(ZSTD(3))` and column TTL
`Body String TTL Timestamp + INTERVAL 30 DAY` (observability managing-data). `merge_with_ttl_timeout`
"minimum delay in seconds before repeating a merge with delete TTL", default 14400; force with
`ALTER TABLE t MATERIALIZE TTL` (same page). 26.3: "TTL DELETE operations can use vertical merges"
via `vertical_merge_optimize_ttl_delete` on by default (`https://clickhouse.com/blog/clickhouse-release-26-03`);
26.9: "Reduce CPU and memory overhead of `TTL` deletions during merges" (changelog).

**F36. Per-tenant retention.** `DELETE WHERE` rules: `TTL time + INTERVAL 1 MONTH DELETE WHERE event
!= 'error', time + INTERVAL 6 MONTH DELETE WHERE event = 'error'` (first pass). Inference: a small set
of retention classes (for example 90d/1y/2y) implemented as `DELETE WHERE retention_class = ...`
rules, with `retention_class LowCardinality(String)` or `Enum8` stamped by the sink from the tenant's
plan, keeps TTL deterministic and column-derived, which the docs require ("Avoid non-deterministic
functions such as rand(), now()"). Because `ttl_only_drop_parts` only drops a part when every row in
it is expired, mixed-class parts fall back to row-level TTL merges; if whole-part drops matter, add
`retention_class` to the partition key (`PARTITION BY (retention_class, toMonday(ts))`), which keeps
cardinality low (3 classes x weeks). A TTL expression using a per-row interval column is not shown in
any fetched page (unverified).

### 2.5 TimeSeries engine and PromQL

**F37.** TimeSeries engine is experimental: enabled with `allow_experimental_time_series_table`,
"may change in backwards-incompatible ways"; stores `samples`, `tags`, `metrics` target tables;
26.9 renamed the outer column `time_series` to `samples` and rejected `CREATE TABLE ... AS
timeSeriesSamples(...)` (web search over `https://clickhouse.com/docs/reference/engines/table-engines/integrations/time-series.md`
and changelog). PromQL: "private preview of PromQL and the `TimeSeries` table engine on ClickHouse
Cloud", four access paths including `--dialect promql` and `prometheusQuery`/`prometheusQueryRange`
table functions, coverage "over 85%", ingestion only via remote-write v1
(`https://clickhouse.com/blog/introducing-promql`); 26.9 adds `sum_over_time`, `avg_over_time`,
`count_over_time` (changelog). 25.8 had "initial support for PromQL ... Only the rate, delta, and
increase functions" (`https://clickhouse.com/blog/clickhouse-release-25-08`).
Recommendation: not for FlowSeer's store. Its data model (metric name + label set) does not carry
typed per-domain columns, it is Cloud private preview, and FlowSeer serves its own queries.

---

## 3. State-change and slowly-changing-set storage

**F38. ReplacingMergeTree `ver`/`is_deleted`.** "the row with the biggest ver 'wins'", ties go to the
most recently inserted; `is_deleted` "1 is a 'deleted' row, 0 is a 'state' row", "can only be enabled
when `ver` is used"; by default the delete row is kept so that lower-version rows arriving later are
still overridden; permanent removal needs `allow_experimental_replacing_merge_with_cleanup` plus either
`OPTIMIZE ... FINAL CLEANUP` or `enable_replacing_merge_with_cleanup_for_min_age_to_force_merge`
(25.3, default 0, experimental) with `min_age_to_force_merge_on_partition_only` and
`min_age_to_force_merge_seconds`
(`https://clickhouse.com/docs/engines/table-engines/mergetree-family/replacingmergetree.md`,
`https://clickhouse.com/docs/reference/settings/merge-tree-settings/enable.md`). 25.12: "The text
index now works with `ReplacingMergeTree` tables" (`https://clickhouse.com/docs/whats-new/changelog/2025.md`).

**F39. Collapsing engines.** CollapsingMergeTree: rows collapse when "all the fields in a sorting key
(`ORDER BY`) are equivalent except for the special field `Sign`"; the writer must emit a cancel row that
copies the state; results "depend on the consistency of the change history"; query with `sum(Sign)`,
`sum(Sign * x) ... HAVING sum(Sign) > 0`; "min and max can't be computed"
(`https://clickhouse.com/docs/engines/table-engines/mergetree-family/collapsingmergetree.md`).
VersionedCollapsingMergeTree: same purpose "but with a collapsing algorithm that tolerates out-of-order
inserts"; "each pair of rows with the same primary key and version but opposite Sign is deleted";
"The writing program must remember prior states so it can produce matching cancel rows"
(`https://clickhouse.com/docs/engines/table-engines/mergetree-family/versionedcollapsingmergetree.md`).
Deduplication guide: Collapsing engines are "more complex to implement than ReplacingMergeTree" but
"queries and aggregations are simpler to write because they don't depend on whether merges have
happened yet" (`https://clickhouse.com/docs/guides/developer/deduplication.md`).
Inference: these engines model *current state*. FlowSeer explicitly keeps current state in NATS
KV/Postgres and wants history, so neither Collapsing variant fits; the sink would also have to
remember prior state to emit cancel rows. ReplacingMergeTree is used only for idempotent *dedup of
immutable events*, never for state.

**F40. FINAL cost today.** `FINAL` "fully merges the data before returning the result", "requires
additional compute and memory resources", "run in parallel", may read extra primary key columns; the
`final` setting applies it per query/session (`https://clickhouse.com/docs/sql-reference/statements/select/from.md`).
`do_not_merge_across_partitions_select_final` makes "partitions to be merged and processed
independently"; from 23.12 "if the partition key is a prefix of the sorting key, merging across
partitions isn't performed at query time"; partitioned example 2.338 s to 0.994 s; "setting
`min_age_to_force_merge_seconds` to a low value—significantly less than the partition period—is
preferred" (`https://clickhouse.com/docs/concepts/features/operations/update/replacing-merge-tree.md`).
New in 26.2: `enable_automatic_decision_for_merging_across_partitions_for_final` default 1, "ClickHouse
will automatically enable this optimization when the partition key expression is deterministic" and all
its columns are in the primary key (`https://clickhouse.com/docs/reference/settings/session-settings/enable.md`).
26.9: "makes `SELECT ... FINAL` from `ReplacingMergeTree` use up to 40% less CPU" (changelog).
`optimize_read_in_reverse_order_final` lets read-in-order work "in the reverse order" for
ReplacingMergeTree (order-by.md). "Parallel replicas are disabled with FINAL"
(`https://clickhouse.com/docs/deployment-guides/parallel-replicas.md`). The dedicated settings page
for `do_not_merge_across_partitions_select_final` could not be located under any guessed URL; the
Altinity KB's "added in 20.10" is unverified.
Inference: the partition-key-in-sorting-key condition is **not** met by `PARTITION BY toMonday(ts)`
with `ORDER BY (tenant, device, entity, ts)` (ts is in the key but `toMonday(ts)` is not a prefix).
It would be met by adding `toMonday(ts)` as a key column, which costs nothing on compression and makes
FINAL per-partition. For FlowSeer, FINAL is rarely needed (F43), so keep the simpler key and accept
rare duplicates in raw reads.

**F41. argMax / GROUP BY instead of FINAL.** The dedup guide: "Grouping as shown in the query above can
actually be more efficient" than FINAL; "Using FINAL works okay if you have a small amount of data"
(deduplication.md). `argMax(arg, val)`: with ties "which of the associated `arg` is returned is not
deterministic"; both arguments skip NULLs; `argMax(a, (b, a))` breaks ties with a tuple
(`https://clickhouse.com/docs/sql-reference/aggregate-functions/reference/argmax.md`).

**F42. ASOF JOIN.** "any number of equality conditions and exactly one closest match condition" with
`>`, `>=`, `<`, `<=`; "Equal timestamp values are the closest if available"; "supported only by `hash`
and `full_sorting_merge` join algorithms"; ASOF column types Int, UInt, Float, Date, DateTime, Decimal
(DateTime64 not listed, unverified); for `hash` "it can't be the only column in the JOIN clause"; not
supported in the Join engine (`https://clickhouse.com/docs/sql-reference/statements/select/join.md`).

**F43. Reconstructing a set at time T (FDB, neighbors, clients).** No ClickHouse page documents this
pattern; the following is inference from the quoted primitives.

Store one `changes` table with rows `(tenant_id, device_id, member_key, ts, op Enum8('add'=1,
'remove'=2, 'snapshot'=3), snapshot_id, attributes...)`. The sink emits a full `snapshot` row set
periodically (for example daily or when the collector restarts) with a shared `snapshot_id`, and `add`
/`remove` rows between snapshots. Set at time T:

```sql
WITH (SELECT max(ts) FROM fdb_changes WHERE tenant_id = {t} AND device_id = {d}
        AND op = 'snapshot' AND ts <= {T}) AS snap_ts
SELECT member_key, argMax(op, (ts, op)) AS last_op, argMax(port, (ts, op)) AS port
FROM fdb_changes
WHERE tenant_id = {t} AND device_id = {d} AND ts >= snap_ts AND ts <= {T}
GROUP BY member_key
HAVING last_op != 'remove';
```

Reads are bounded by one snapshot interval, so a 20,000-device node with daily snapshots scans at
most a day of changes per device for any T. "When did X change" is `WHERE member_key = X ORDER BY ts`
served by the bloom filter on `member_key` (F19). Duplicate deliveries of the same change row are
harmless because `argMax` picks one of identical rows; if exact counts of events are needed, make the
table `ReplacingMergeTree` keyed by `(tenant_id, device_id, member_key, ts, op)` so a redelivered row
collapses on merge (F45). Snapshots also bound the damage of a lost change row: the error lasts until
the next snapshot. Storage cost: snapshot rows dominate for stable sets; the snapshot interval is the
knob (a weekly snapshot cuts snapshot volume 7x and raises worst-case read 7x).

---

## 4. Dedup for at-least-once delivery (building on the first pass)

**F44.** New in this pass: 26.1 "Deduplication now works end-to-end for asynchronous inserts *and*
their dependent materialized views" (`https://clickhouse.com/blog/clickhouse-release-26-01`), which
matches the first pass's `deduplicate_blocks_in_dependent_materialized_views` default. 26.2 unified
sync/async under `deduplicate_insert` (first pass). 26.9: `min_partition_age_to_force_merge_seconds`
(default 0) "forces merges in partitions that no longer receive inserts"
(`https://clickhouse.com/docs/reference/settings/merge-tree-settings/min.md`), which is the setting to
collapse ReplacingMergeTree duplicates in closed weekly partitions without touching the active one.

**F45. Natural-key dedup without a record id.** Inference: for *samples*, the natural key
`(tenant_id, device_id, ifindex, ts)` is unique per record by construction (one poll per interface
per timestamp). A `ReplacedReplicatedMergeTree` with exactly that ORDER BY deduplicates a redelivery
with no extra column and no `ver`; the first-arrived and redelivered rows are byte-identical so which
one "wins" is irrelevant. For *changes*, `(tenant_id, device_id, member_key, ts, op)` is unique per
change event unless a device can emit two identical changes in one second; adding the protobuf
record id as the last key column makes it unique for certain at 16 bytes per row. Keep `record_id`
in the changes tables only.

**F46. Kafka/NATS engines do not provide exactly-once.** NATS engine with JetStream: "a message is
acknowledged only after it has been inserted into the dependent materialized views"; core NATS is
"at-most-once"; no exactly-once claim; acknowledged rows may be lost before fsync unless
`fsync_after_insert = 1` and `fsync_part_directory = 1` (`https://clickhouse.com/docs/engines/table-engines/integrations/nats.md`).
Kafka Keeper-offsets engine: "it is not production ready yet"; on a failed insert "the same amount of
messages will be consumed, thus enabling deduplication if necessary"; the setting is
`allow_kafka_offsets_storage_in_keeper` (not `allow_experimental_...`)
(`https://clickhouse.com/docs/engines/table-engines/integrations/kafka.md`). Recommendation: keep the
Go sink; it already owns acks and can set `insert_deduplication_token`.

**F47. Lightweight UPDATE is not a dedup tool.** It is "currently beta", needs
`enable_block_number_column`/`enable_block_offset_column`, "Patch parts add roughly 40 bytes of
uncompressed overhead per updated row", "Frequent small updates can cause 'too many parts' errors",
"Projections are not used while the table has any patch parts", designed for "updates touching about
10% of rows or fewer" (`https://clickhouse.com/docs/sql-reference/statements/update.md`). 25.7
introduced it ("Standard SQL UPDATE statements at scale, also known as lightweight updates",
`https://clickhouse.com/blog/alexey-favorite-features-2025`); 26.9 patch parts "carry the table's
sort-key columns" (changelog). The claim that it was "promoted to Beta with default enablement in
25.8" is from a third-party source and is unverified.

**F48. Answer: exact-once per key?** No server-side mechanism gives it for a redelivery after the
dedup window in a different batch (first pass table stands). The combination that approaches it:
(1) `insert_deduplication_token` = stable batch id (JetStream sequence range) so an in-window retry
is exact; (2) ReplacingMergeTree on the natural key so late duplicates collapse on merge; (3)
`min_partition_age_to_force_merge_seconds` (26.9) or `min_age_to_force_merge_*` so closed partitions
end up single-part and duplicate-free; (4) readers that need exact counts use `GROUP BY` natural key
or FINAL. Since FlowSeer's dedup window in NATS is 10 min and ClickHouse's is 3600 s / 10,000 blocks
relative to the newest record, a redelivery inside 10 min is almost always caught server-side as well
when the sink re-sends the same batch with the same token; the residue is tiny and handled by (2).

---

## 5. Logs and syslog

**F49. Reference schemas.** ClickStack `otel_logs`: `Timestamp DateTime64(9) CODEC(Delta(8), ZSTD(1))`,
`SeverityText LowCardinality(String)`, `ServiceName LowCardinality(String)`, attributes
`Map(LowCardinality(String), String)`, text indexes with the `array` tokenizer on `TraceId` and on map
keys/items, `text(tokenizer = splitByNonAlpha)` on `lower(Body)`, `PARTITION BY toDate(Timestamp)`,
`ORDER BY (toStartOfFiveMinutes(Timestamp), ServiceName, Timestamp)`, `TTL toDateTime(Timestamp) +
${TABLES_TTL}`, `SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1, enable_block_number_column
= 1, enable_block_offset_column = 1` (`https://clickhouse.com/docs/use-cases/observability/clickstack/ingesting-data/schemas.md`).
Map vs JSON: "recommended schema for observability workloads" is Map; JSON is beta "for users who want
to evaluate it on workloads with a small, stable set of attribute keys"; 26.3's sharded Map ("splits
map data into multiple sub-arrays by grouping keys into hash-based buckets", `map_serialization_version`)
"removes most of the historical read-time overhead of Map" (web search over
`https://clickhouse.com/docs/clickstack/ingesting-data/schema/map-vs-json.md`; 26.3 blog).
Map lookups: "`m[k]` scans the map ... linear in the size of the map" and "when querying a subkey of a
Map type, the entire parent column is loaded" (first pass; schema-design.md).

**F50. Text index.** GA 26.2 ("the text index is production-ready", 26.2 blog; settings page:
`enable_full_text_index` "26.2: default 1, text index became generally available", "25.12: default 0,
moved to Beta", "24.6: ... experimental"). Syntax `INDEX name col TYPE text(tokenizer = splitByNonAlpha
| splitByString(S) | splitByRegexp(re) | asciiCJK | chinese | icu(locale) | japanese | ngrams(N) |
sparseGrams(min,max,cutoff) | array | keyValuePairs [, preprocessor = ..., postprocessor = ...,
support_phrase_search = ...])`; columns String, FixedString, Array(String), Map via
`keyValuePairs`/`mapKeys`/`mapValues`, JSON via `JSONAllPaths`; "Nullable and LowCardinality are also
supported"; functions `=`, `IN`, `hasToken`, `hasAnyTokens`, `hasAllTokens`, `hasPhrase`, `LIKE`,
`match`, `startsWith`, `has`, `mapContainsKey`, `map['key']`; "NOT IN is not supported", "NOT LIKE is
not supported"; direct read from index on by default (`query_plan_direct_read_from_text_index`);
indexes are "tens to hundreds of megabytes per part"; log advice: strip timestamps with a
`preprocessor` regex, drop severity words with a `postprocessor`, prefer `keyValuePairs` for Map
(`https://clickhouse.com/docs/engines/table-engines/mergetree-family/invertedindexes.md`). Benchmarks on
the same page: `hasToken` 0.362 s to 0.008 s, 9.51 GB to 3.15 MB processed. Observability page:
preprocessing to one JSON field "shrank the index from gigabytes to a few hundred kilobytes".
`icu`/`splitByRegexp`/`japanese`/`chinese` tokenizers arrived in 26.8 (26.8 blog); `sparseGrams` and
array support in 26.1 (26.1 blog, with the caution that sparseGrams made "its index much larger and
ingestion far slower").

**F51. Newest-first by device.** "Optimization works with both `ASC` and `DESC`"; "If `ORDER BY`
expression has a prefix that coincides with the table sorting key" the read stops early "in case of
specified LIMIT"; "does not work together with the `GROUP BY` clause"
(`https://clickhouse.com/docs/sql-reference/statements/select/order-by.md`). Lazy materialization
(25.4, on by default, `query_plan_optimize_lazy_materialization`) "deferring column reads until
they're actually required", example 219 s to 0.139 s for `ORDER BY ... LIMIT 3`
(`https://clickhouse.com/docs/concepts/features/performance/lazy-materialization.md`); 25.11 raised its
row limit "from 10 to 100" and made it work with `optimize_read_in_order` simultaneously (changelog 2025).
Inference: `ORDER BY (tenant_id, device_id, ts)` plus `SELECT ... WHERE tenant_id = ? AND device_id IN
(...) ORDER BY ts DESC LIMIT 200` reads in reverse key order per device and materializes `message`
only for the 200 rows. ClickStack's `toStartOfFiveMinutes(Timestamp)` leading key is for its
service-agnostic "last N minutes across everything" access pattern, which FlowSeer does not have.

---

## 6. Multi-tenancy

**F52.** The guide's non-row-policy content: separate tables are "a good choice when tenants have
different data schemas" or "a few tenants with very large datasets", "Note this approach doesn't scale
for 1000s of tenants"; separate databases likewise; shared table "particularly effective for handling
a large number of tenants (potentially millions)"; "No mentions of quotas, settings profiles, or
workloads for tenant isolation" on the page (`https://clickhouse.com/docs/cloud/bestpractices/multi-tenancy.md`).
Shared tables it is.

**F53. Row policies at thousands of tenants.** The docs describe a policy as "an extra WHERE
condition" (first pass). Whether thousands of `CREATE ROW POLICY ... TO role_x` objects cost anything
measurable at query time is not documented (unverified). Inference: FlowSeer's query path is its own
Go service with one service user; the tenant filter is a query-builder invariant, and a single
restrictive row policy on a dedicated *read-only analyst/BI* user set (if ever exposed) is the
defence in depth. Do not create one ClickHouse role per tenant for the service path.

**F54. Per-tenant quotas.** `CREATE QUOTA ... KEYED BY client_key FOR INTERVAL 1 hour MAX queries =
..., read_rows = ..., execution_time = ...` with the client sending `quota_key` = tenant id (first
pass). Query log records `quota_key` (`https://clickhouse.com/docs/operations/system-tables/query_log.md`),
so per-tenant consumption is observable from one service user.

**F55. Settings profiles.** "A profile can inherit from others"; "If the same setting appears in
several profiles, the latest defined is used"; the `default` profile "must always be present";
`SET profile = 'web'` applies one (`https://clickhouse.com/docs/operations/settings/settings-profiles.md`).
Operator: users, profiles, quotas and grants go under `spec.settings.extraUsersConfig` (a ConfigMap;
"Avoid plain text secrets there") and server config under `spec.settings.extraConfig` (restart) or
`extraReloadableConfig` (`https://clickhouse.com/docs/products/kubernetes-operator/guides/configuration.md`).

**F56. Workload isolation so analytics cannot stall inserts.** Server-level thread cap:
`concurrent_threads_soft_limit_num` default 0 (unlimited), `concurrent_threads_soft_limit_ratio_to_cores`
default 2, "not a hard limit ... the query will still get at least one thread";
`concurrent_threads_scheduler` default `max_min_fair` ("freed slots go to the query with the fewest
allocated slots") (`https://clickhouse.com/docs/reference/settings/server-settings/settings/concurrent-threads`).
`max_concurrent_queries`, `max_concurrent_insert_queries`, `max_concurrent_select_queries` all default
0 and are changeable at runtime (`https://clickhouse.com/docs/reference/settings/server-settings/settings/max-concurrent`).
Background merges: `background_pool_size` 16, `background_merges_mutations_concurrency_ratio` 2,
`merges_mutations_memory_usage_to_ram_ratio` 0.5, `max_server_memory_usage_to_ram_ratio` 0.9,
`background_fetches_pool_size` 16, `background_move_pool_size` 8
(`https://raw.githubusercontent.com/ClickHouse/ClickHouse/master/src/Core/ServerSettings.cpp`,
`.../server-settings/settings/background`). Table-level `merge_workload` routes a table's merges to a
workload ("If empty, the server-level `merge_workload` setting applies",
`https://clickhouse.com/docs/reference/settings/merge-tree-settings/merge.md`), although the
scheduling page says "CPU scheduling is not supported for merges and mutations yet" (first pass), so
`merge_workload` applies to IO resources only (inference). 26.9 adds `workload_admission_timeout_ms`
"bounds how long a query waits" for a workload slot (changelog).

Recommendation (inference from the quoted mechanics, aimed at the legacy 2 minute stall): the stall
was a 21 GiB, 218 s query starving inserts of CPU and memory on one node. Guard with four layers:
(a) query user profile: `max_execution_time` 60 s (`timeout_overflow_mode = 'break'` for chart
queries that may return partial), `max_memory_usage` ≤ 25% of node RAM, `max_bytes_to_read` sized to
the largest legitimate scan, `max_threads` = half the cores, `max_concurrent_queries_for_user`,
all `CONST` or `MAX`-bounded; (b) `max_memory_usage_for_user` on the query user and
`max_server_memory_usage_to_ram_ratio` left at 0.9 so merges and inserts keep headroom;
(c) `CREATE RESOURCE cpu (MASTER THREAD, WORKER THREAD)` + `CREATE WORKLOAD all`, `ingestion IN all`,
`analytics IN all SETTINGS max_concurrent_threads = <cores - 4>, weight = 1`; pin `workload` per user
via a CONST constraint; (d) `concurrent_threads_soft_limit_ratio_to_cores = 2` as the global soft cap.
Rollups (F33) remove the reason for 90-day window queries over raw rows in the first place.

**F57. Per-tenant deletion and tenant forget.** Lightweight `DELETE FROM t WHERE tenant_id = 'x'`:
rewritten as `ALTER TABLE ... UPDATE _row_exists = 0`, "Wide parts update only the mask. Compact parts
rewrite all columns", "Deleted rows stay on disk until a later merge removes them", "By default, DELETE
doesn't work on tables with projections" (`lightweight_mutation_projection_mode`), needs `ALTER DELETE`
grant (`https://clickhouse.com/docs/sql-reference/statements/delete.md`). `ALTER TABLE ... DELETE [IN
PARTITION ...] WHERE` "rewrites affected data parts, which creates significant write I/O",
asynchronous by default (`mutations_sync`) (`https://clickhouse.com/docs/sql-reference/statements/alter/delete.md`).
`DROP PARTITION` deletes on all replicas, "approximately in 10 minutes" (deferred removal)
(`https://clickhouse.com/docs/sql-reference/statements/alter/partition.md`). Third-party GDPR guides
differ on whether a lightweight delete satisfies erasure; none is authoritative (unverified).
Recommendation: tenant forget = lightweight `DELETE ... WHERE tenant_id = ?` on every table (fast,
hides rows immediately), followed by `ALTER TABLE ... DELETE WHERE tenant_id = ?` with `mutations_sync
= 2` scheduled off-peak for physical removal, tracked in `system.mutations`; document the asynchronous
gap. Do not partition by tenant for this (F6, F7). Backups made before the forget retain the data
(inference; backups are immutable files).

---

## 7. Query protection

**F58.** Already covered in the first pass: `max_execution_time`, `timeout_overflow_mode`,
`max_memory_usage`, `max_rows_to_read`, `max_bytes_to_read`, `max_threads`,
`max_concurrent_queries_for_user`, quotas, constraints, workloads. New here:

**F59.** `max_result_rows`, `max_result_bytes` default 0; "The query will stop after processing a block
of data if the threshold is met" but "The last block is not cut"
(`https://clickhouse.com/docs/reference/settings/session-settings/max-result.md`). `read_overflow_mode`
"What to do when the limit is exceeded", default throw
(`https://clickhouse.com/docs/reference/settings/session-settings/read-overflow-mode.md`).
`result_overflow_mode` and `queue_max_wait_ms` pages could not be located under the guessed URLs
(definitions unverified in this pass; the 26.9 `workload_admission_timeout_ms` is the modern wait bound).

**F60. Query cache.** "transactionally inconsistent"; matched by AST; `query_cache_ttl` default 60 s;
entries per-user by default and sharing "is not recommended for security reasons" (row policies);
non-deterministic functions skip the cache unless forced; `query_cache_tag` for variants; per-user
`query_cache_max_size_in_bytes`; server `max_size_in_bytes` default 1073741824
(`https://clickhouse.com/docs/operations/query-cache.md`, server settings page). Inference: safe for
dashboard rollup queries whose time window is bucketed (`toStartOfHour(now())` arguments make the AST
stable for an hour); include the tenant in the query text (it is), so per-user sharing is not needed.

**F61. Query condition cache.** One bit per granule per filter; 100 MB default "holds 838,860,800
entries"; `use_query_condition_cache`, `use_query_condition_cache_for_top_k`, `query_condition_cache_size`;
best when "the same filters repeat, most data is immutable, and filters are selective"
(`https://clickhouse.com/docs/operations/query-condition-cache.md`); introduced 25.3 "on by default"
(`https://clickhouse.com/blog/alexey-favorite-features-2025`).

**F62. Parallel replicas.** Coordinator splits granules across replicas; `enable_parallel_replicas`
0/1/2; "Parallel replicas are disabled with FINAL"; projections are not used with them; "Small
queries: coordination overhead can outweigh the benefit" (`https://clickhouse.com/docs/deployment-guides/parallel-replicas.md`).
Status: "moved to the Beta tier in 24.10" (allow-experimental.md); 25.12 adds experimental
`automatic_parallel_replicas_mode` (changelog 2025); 26.9 `parallel_replicas_plan_based` "can gather
runtime stats and enable themselves automatically" (changelog). With 1 shard x 2 replicas a heavy
query could use both nodes, but it would then load the node the sink is inserting into; leave off for
the service user, consider for an analyst user later.

**F63. Insert/merge interplay.** `parts_to_delay_insert` 1000, `parts_to_throw_insert` 3000 (first
pass); `max_part_num_to_warn` 100000 (ServerSettings.cpp); monitor `system.parts` with
`WHERE active GROUP BY partition` (`https://clickhouse.com/docs/operations/system-tables/parts.md`).
26.9 adds table limits ("`max_table_size_rows` and related size limits",
`https://clickhouse.com/blog/clickhouse-release-26-09`).

---

## 8. Replication and operations on 1 shard x 2 replicas

**F64.** Replicated database, ON CLUSTER, `insert_quorum`: first pass. New: `ALTER TABLE ... MODIFY
ORDER BY` "you cannot add expressions containing existing columns to the sorting key", only columns
added "in the same ALTER query, without default column value", "Primary key remains the same"
(`https://clickhouse.com/docs/sql-reference/statements/alter/order-by.md`). `ADD COLUMN` "just changes
the table structure, without performing any actions with data"; `DROP COLUMN` "Deletes data from the
file system" instantly; `RENAME COLUMN` instant; `MODIFY COLUMN` type change may "create a READ_COLUMN
mutation"; key columns "cannot be renamed" and cannot be dropped
(`https://clickhouse.com/docs/sql-reference/statements/alter/column.md`). Inference: schema evolution
plan = add columns freely (protobuf field additions map 1:1), never rename a key column, and treat an
ORDER BY change as "new table + INSERT SELECT + REPLACE PARTITION" (REPLACE PARTITION "is atomic",
partition.md).

**F65. Keeper.** "For production environments we suggest to use separate servers for ClickHouse and
ZooKeeper/Keeper. Because ZooKeeper/Keeper are very sensitive for disk latency"; or "place ClickHouse
files and Keeper files on to separate disks" (`https://clickhouse.com/docs/operations/tips.md`).
Keeper defaults: `operation_timeout_ms` 10000, `session_timeout_ms` 100000, heartbeat 500 ms, snapshot
every 100000 records keeping 3, `force_sync` true; monitor with `mntr`/`ruok`
(`https://clickhouse.com/docs/guides/sre/keeper/clickhouse-keeper.md`). Operator `KeeperCluster`
replicas must be odd, default `preStop` hands off Raft leadership (operator configuration page). 26.1
adds `system.zookeeper_info` and a Keeper web dashboard (26.1 blog). Concrete CPU/RAM/disk sizes for
Keeper exist only in third-party guides (unverified); the official guidance is "separate" and "SSD".

**F66. Backups.** `BACKUP TABLE|DATABASE|ALL [ON CLUSTER] TO S3('<endpoint>/<path>', key, secret)
[SETTINGS base_backup = ...] [ASYNC]`; access entities are exported as SQL unless defined in
`users.xml`; `system.backups` and `system.backup_log` (`https://clickhouse.com/docs/operations/backup.md`).
Restore of replicated tables onto a different replica set is not described on that page (unverified).
26.9 adds BACKUP/RESTORE of WORKLOAD and RESOURCE entities and requires `SOURCES` grants for
`Disk(...)` targets (changelog). Altinity `clickhouse-backup`: hard-link based via `ALTER TABLE ...
FREEZE`, S3/GCS/Azure, incremental, REST API, "Don't run `clickhouse-backup` remotely", must be in the
same pod or a neighbour container (`https://raw.githubusercontent.com/Altinity/clickhouse-backup/master/ReadMe.md`).
Recommendation: native `BACKUP ... TO S3` with `base_backup` daily increments from one replica, run via
a Kubernetes CronJob against the service; `clickhouse-backup` is the fallback if the operator's
sidecar model is preferred.

**F67. Monitoring tables.** `system.part_log` (enable via the `part_log` server setting): event types
NewPart, MergeParts, MutatePart, MovePart, RemovePart, columns `duration_ms`, `rows`, `size_in_bytes`,
`merge_reason` (RegularMerge, TTLDeleteMerge, TTLDropMerge), `peak_memory_usage`
(`https://clickhouse.com/docs/operations/system-tables/part_log.md`). `system.asynchronous_insert_log`
(enable `asynchronous_insert_log` section): `status` Ok/ParsingError/FlushError, `flush_query_id`,
`rows`, `bytes`, `exception` (`https://clickhouse.com/docs/operations/system-tables/asynchronous_insert_log.md`).
`system.query_log`: `QueryStart`/`QueryFinish`/`ExceptionBeforeStart`/`ExceptionWhileProcessing`,
`query_duration_ms`, `read_rows`, `memory_usage`, `ProfileEvents`, `quota_key`, `used_*`, filter
`is_initial_query = 1` (query_log.md). Metrics live in `system.metrics`, `system.events`,
`system.asynchronous_metrics`, with a built-in `/dashboard`
(`https://clickhouse.com/docs/operations/monitoring.md`). The `<prometheus>` server block (endpoint,
port, metrics, events, asynchronous_metrics, errors) is referenced by the monitoring page but its
reference page could not be located in this pass; only `prometheus.keeper_metrics_only` (default 0)
appeared in the settings browser (block details unverified here; the integration page fetched is
Cloud-only, `https://clickhouse.com/docs/integrations/prometheus.md`).

---

## 9. Dimension data (device → site at time T)

**F68. range_hashed dictionary.** `RANGE(MIN valid_from MAX valid_to)`; `dictGet('d', 'attr', id,
date)` "returns the value for the range containing the date"; `range_lookup_strategy 'min'|'max'`
decides among overlapping ranges; "A `NULL` `range_max` is treated as the maximum possible value";
`complex_key_range_hashed` for composite keys (`https://clickhouse.com/docs/reference/statements/create/dictionary/layouts/range-hashed`).
LIFETIME: "During updates, the old version of a dictionary can still be queried"; `LIFETIME(MIN 300
MAX 360)` randomizes; `invalidate_query` reloads only on change; `update_field`/`update_lag` fetch
deltas (`https://clickhouse.com/docs/reference/statements/create/dictionary/lifetime`).

**F69. Recommendation.** Enrich at write time with the *current* site id as a plain column
(`site_id`) because the best-practice page says denormalization "shifts computational work from query
time to insert or pre-processing time" and site changes are rare; keep a `device_site_history`
table (device_id, site_id, valid_from, valid_to) mirrored from Postgres and expose it as a
`complex_key_range_hashed` dictionary keyed `(tenant_id, device_id)` for "as the site was at time T"
reports via `dictGet(..., (tenant_id, device_id), ts)`. The row-stamped `site_id` answers "last 24h
for site S" without a join; the dictionary answers historical reassignment questions. ASOF JOIN
remains for ad hoc analysis. Inference throughout.

---

## 10. Other recent features worth knowing (25.x to 26.x)

**F70.** Already placed above: lazy materialization 25.4 (F51), query condition cache 25.3 (F61),
JSON/Variant/Dynamic production-ready 25.3 (F13; "Variant and Dynamic types are now production-ready
as standalone features", 25.3 blog), lightweight UPDATE 25.7 beta (F47), text index GA 26.2 (F50),
unified dedup 26.1/26.2 (F44), async insert default 26.2/26.3 (F15), Time types GA 25.12 (F12),
automatic FINAL partition decision 26.2 (F40), `min_partition_age_to_force_merge_seconds` 26.9 (F44),
`APPEND INCREMENTAL` 26.9 (F21), adaptive codec selection 26.8 experimental (F24).

**F71.** Sparse serialization: `ratio_of_defaults_for_sparse_serialization` default 0.9375,
"Minimal ratio of the number of *default* values to the number of *all* values" above which a column
is stored sparsely (`https://clickhouse.com/docs/reference/settings/merge-tree-settings/other.md`).
Inference: a wide samples row with many zero counters gets sparse columns for free; this weakens the
case against wide rows but not the case for not storing absent counters as fake zeros.

**F72.** 26.2 automatic minmax indexes for temporal columns (`add_minmax_index_for_temporal_columns`)
and faster inserts with minmax (26.2 blog). 26.8: `CREATE USER ... VALID FOR INTERVAL`, background
queries, pipelined SQL, `system.user_query_log`, `parallel_full_sorting_merge` join (26.8 blog).
26.9: `CREATE TOKEN`, `LIMIT` boundary conditions, min/max/count from column statistics, external
DISTINCT spill, `system.session_query_ids` (26.9 blog). 26.1: `mergeTreeAnalyzeIndexes` table function
"returns the exact row ranges within each data part that will be scanned", `files` column in
`system.parts`, projection `INDEX` syntax (26.1 blog). 26.3: `MATERIALIZED` CTEs, `naturalSortKey`,
`EXPLAIN ... pretty=1`, WebAssembly UDFs (experimental) (26.3 blog).

**F73.** Backward-incompatible items to watch when upgrading past 26.9: Tuple subcolumns extracted
from Variant/Dynamic/JSON become `Nullable(Tuple)` (set `allow_nullable_tuple_in_extracted_subcolumns
= 0` before first start if that matters); TimeSeries outer column rename; BACKUP to Disk needs
`SOURCES` grants (changelog index). 25.11 removed the deprecated `Object` type; 25.12 JSON parts
cannot downgrade below 25.8 (changelog 2025).

**F74.** Vector search (HNSW GA 25.8, QBit) is irrelevant to this store.

---

## 11. Table: feature | version | use for FlowSeer | risk

| Feature | Version (source) | Use for FlowSeer | Risk |
|---|---|---|---|
| ReplicatedMergeTree + `insert_deduplication_token`, unified `deduplicate_insert` | dedup unified 26.2 (first pass) | Every table; token = JetStream batch id | Window 3600 s / 10,000 blocks relative to newest record; late redelivery slips through |
| ReplicatedReplacingMergeTree on natural key (no `ver`) | long-standing | Samples and changes tables to collapse late duplicates | Eventual only; readers needing exact counts use GROUP BY/FINAL |
| `min_partition_age_to_force_merge_seconds` | 26.9 (merge-tree-settings/min.md) | Collapse duplicates in closed weekly partitions | Not in 26.8 LTS; use `min_age_to_force_merge_*` there |
| `enable_automatic_decision_for_merging_across_partitions_for_final` | 26.2 (session-settings/enable.md) | Cheaper FINAL if partition expr columns are in the primary key | Our key does not satisfy it unless `toMonday(ts)` joins the key |
| Incremental MV → AggregatingMergeTree rollups | long-standing | Hourly/daily rollups, `deltaSumTimestampState` | Duplicate inserts past dedup double-count sums; rely on RMT on raw + `max()`-style idempotent aggregates where possible |
| Refreshable MV `APPEND INCREMENTAL` | 26.9 (changelog) | Alternative rollup path | Not in 26.8 LTS; needs block number/offset columns |
| TTL DELETE WHERE, `ttl_only_drop_parts` | long-standing | Retention classes per tenant plan | Mixed-class parts fall back to row TTL merges |
| TTL TO VOLUME (S3 + cache disk) | long-standing | Cold tier after N months | Operator storage-policy config; S3 latency for cold queries |
| TTL GROUP BY | long-standing | Roll the rollup table down (hourly → daily) | Forces bucket into primary key; not for raw tables |
| Text index (`text(tokenizer=...)`) | GA 26.2 (invertedindexes.md) | Syslog message search | "tens to hundreds of megabytes per part"; preprocess timestamps out |
| `bloom_filter` skip index | long-standing | MAC lookups across devices | Must prove skipping with `EXPLAIN indexes=1` |
| `nonNegativeDerivative`, `deltaSum[Timestamp]` | 21.3/21.6 (pages) | Counter rates | Resets under-count one interval |
| `timeSeriesRateToGrid` and friends | 25.6, private preview 26.9 | Future PromQL-exact rates | Private preview; API may change |
| TimeSeries engine, PromQL | experimental / Cloud private preview | None | Does not fit typed domain tables |
| `WITH FILL ... STALENESS`, `INTERPOLATE` | version unverified | Chart gap filling | Multi-series fill needs series key first |
| Lazy materialization | 25.4 default on | Newest-first log reads | Narrow gate (LIMIT ≤ 100 after 25.11) |
| Query cache | long-standing | Dashboard rollups with bucketed `now()` | Stale up to TTL; AST-exact matching |
| Query condition cache | 25.3 default on | Repeated per-device filters | Only immutable data benefits |
| Workloads (`CREATE RESOURCE/WORKLOAD`), `concurrent_threads_soft_limit_*` | long-standing; `workload_admission_timeout_ms` 26.9 | Isolate analytics from ingestion | No CPU scheduling for merges; memory scheduling experimental |
| Settings profile constraints, per-tenant quota via `quota_key` | long-standing | Bound the query user; meter tenants | `SET profile` is not access-checked |
| Row policies | long-standing | Defence in depth for non-service users | Default-open for users without a policy |
| Lightweight DELETE + ALTER DELETE | 22.8 (third party, unverified) / long-standing | Tenant forget | Physical removal is asynchronous; projections block LWD by default |
| Lightweight UPDATE | 25.7, beta (update.md) | None (history is immutable) | Patch parts, parts count |
| `range_hashed` dictionary | long-standing | Device→site at time T | One-to-many silently drops rows |
| ASOF JOIN | long-standing | Ad hoc point-in-time joins | hash/full_sorting_merge only; DateTime64 support unverified |
| Parallel replicas | Beta 24.10 | Later, analyst user only | Loads the insert node; disabled with FINAL |
| Replicated database engine | long-standing (first pass) | One `flowseer` database, no ON CLUSTER DDL | DDL output mode `null_status_on_timeout` |
| BACKUP TO S3 with `base_backup` | 23.4+ for ALL (backup.md) | Nightly increments | Replicated restore semantics unverified |
| `system.part_log`, `asynchronous_insert_log`, `query_log` | long-standing | Merge/insert/query observability | Must be enabled in server config |
| Adaptive codec selection | 26.8 experimental | Try on a staging copy | Experimental |

---

## 12. Schema sketches (DDL)

Common conventions (inference from F3 to F12, F24, F45): database `flowseer` with `ENGINE =
Replicated` (operator default), tables `ReplicatedReplacingMergeTree` without Keeper arguments,
`tenant_id LowCardinality(String)`, `device_id UUID`, `ts DateTime` (UTC), `retention_class
Enum8('short' = 1, 'standard' = 2, 'long' = 3)` stamped by the sink from the tenant plan, no Nullable,
weekly partitions for year-scale retention, daily partitions for the log table. Replace the TTL days
with the plan's values; the structure, not the numbers, is the recommendation. Settings
`ttl_only_drop_parts = 1` everywhere; `index_granularity` default 8192.

### 12.1 Interface samples (periodic counters and gauges)

```sql
CREATE TABLE flowseer.interface_samples
(
    tenant_id        LowCardinality(String),
    device_id        UUID,
    ifindex          UInt32          CODEC(T64, ZSTD(1)),
    ts               DateTime        CODEC(Delta, ZSTD(1)),
    -- discontinuity marker: SNMP sysUpTime or ifCounterDiscontinuityTime at sample time;
    -- a rate is valid only between two samples with equal discontinuity_epoch (F27)
    discontinuity_epoch UInt32       CODEC(T64, ZSTD(1)),
    -- cumulative 64-bit counters (Delta: small positive stride within one interface run, F25)
    in_octets        UInt64          CODEC(Delta, ZSTD(1)),
    out_octets       UInt64          CODEC(Delta, ZSTD(1)),
    in_ucast_pkts    UInt64          CODEC(Delta, ZSTD(1)),
    out_ucast_pkts   UInt64          CODEC(Delta, ZSTD(1)),
    in_errors        UInt64          CODEC(Delta, ZSTD(1)),
    out_errors       UInt64          CODEC(Delta, ZSTD(1)),
    in_discards      UInt64          CODEC(Delta, ZSTD(1)),
    out_discards     UInt64          CODEC(Delta, ZSTD(1)),
    -- gauges
    speed_bps        UInt64          CODEC(T64, ZSTD(1)),
    -- which counters were present in this sample (bit i = counter i); avoids Nullable (F9)
    present_mask     UInt16          CODEC(T64, ZSTD(1)),
    retention_class  Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    site_id          LowCardinality(String)   -- current site at write time (F69)
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY toMonday(ts)
ORDER BY (tenant_id, device_id, ifindex, ts)
TTL ts + INTERVAL 90 DAY DELETE WHERE retention_class = 'short',
    ts + INTERVAL 1 YEAR DELETE WHERE retention_class = 'standard',
    ts + INTERVAL 2 YEAR DELETE WHERE retention_class = 'long'
SETTINGS ttl_only_drop_parts = 1, min_age_to_force_merge_seconds = 604800,
         min_age_to_force_merge_on_partition_only = 1;
```

Reasoning. ORDER BY follows tenant-first, device, entity, time (F3 to F6). The natural key is unique
per poll, so ReplacingMergeTree dedups redeliveries with no extra column (F45). Weekly partitions give
52 to 104 partitions at 1 to 2 years (F7, F8) and one or two parts per insert batch. `Delta, ZSTD(1)`
on counters and timestamp, `T64` on small-range integers (F24, F25). `present_mask` replaces Nullable
(F9). `min_age_to_force_merge_*` lets closed weeks merge down to one part so FINAL, if used, sees a
single part per partition (F40); on 26.9+ use `min_partition_age_to_force_merge_seconds` instead
(F44). Note that "there should be no more than one DELETE rule" applies to *unconditioned* rules;
the TTL guide's own example uses several `DELETE WHERE` rules (F36). Hourly rollup:

```sql
CREATE TABLE flowseer.interface_hourly
(
    tenant_id   LowCardinality(String),
    device_id   UUID,
    ifindex     UInt32,
    hour        DateTime,
    in_octets_delta   AggregateFunction(deltaSumTimestamp, UInt64, DateTime),
    out_octets_delta  AggregateFunction(deltaSumTimestamp, UInt64, DateTime),
    in_errors_delta   AggregateFunction(deltaSumTimestamp, UInt64, DateTime),
    samples     SimpleAggregateFunction(sum, UInt64),
    first_ts    SimpleAggregateFunction(min, DateTime),
    last_ts     SimpleAggregateFunction(max, DateTime),
    max_speed   SimpleAggregateFunction(max, UInt64)
)
ENGINE = ReplicatedAggregatingMergeTree
PARTITION BY toYYYYMM(hour)
ORDER BY (tenant_id, device_id, ifindex, hour)
TTL hour + INTERVAL 3 YEAR
SETTINGS ttl_only_drop_parts = 1;

CREATE MATERIALIZED VIEW flowseer.interface_hourly_mv TO flowseer.interface_hourly AS
SELECT tenant_id, device_id, ifindex, toStartOfHour(ts) AS hour,
       deltaSumTimestampState(in_octets, ts)  AS in_octets_delta,
       deltaSumTimestampState(out_octets, ts) AS out_octets_delta,
       deltaSumTimestampState(in_errors, ts)  AS in_errors_delta,
       toUInt64(count()) AS samples, min(ts) AS first_ts, max(ts) AS last_ts, max(speed_bps) AS max_speed
FROM flowseer.interface_samples
GROUP BY tenant_id, device_id, ifindex, hour;
```

`deltaSumTimestamp` exists precisely for this ("materialized views that store data ordered by a
bucketed timestamp", F27). A duplicate sample that reaches the MV contributes a zero delta (same
value, same timestamp) and inflates `samples` by one; `max`/`min` are idempotent (F20, first pass F on
MV dedup). Months-long trends read `interface_hourly` with `deltaSumTimestampMerge(...)`.

### 12.2 Interface changes (status transitions)

```sql
CREATE TABLE flowseer.interface_changes
(
    tenant_id     LowCardinality(String),
    device_id     UUID,
    ifindex       UInt32            CODEC(T64, ZSTD(1)),
    ts            DateTime          CODEC(Delta, ZSTD(1)),
    record_id     UUID,             -- protobuf record id; makes the key unique for certain (F45)
    attribute     Enum8('oper_status' = 1, 'admin_status' = 2, 'speed' = 3, 'alias' = 4, 'mtu' = 5, 'descr' = 6),
    old_value     String            CODEC(ZSTD(1)),
    new_value     String            CODEC(ZSTD(1)),
    -- typed copies for the common numeric transitions, 0 when not applicable
    new_oper_status  Enum8('unknown' = 0, 'up' = 1, 'down' = 2, 'testing' = 3, 'dormant' = 5, 'notPresent' = 6, 'lowerLayerDown' = 7),
    retention_class  Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    site_id       LowCardinality(String),
    INDEX idx_ts_minmax ts TYPE minmax GRANULARITY 4
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY toMonday(ts)
ORDER BY (tenant_id, device_id, ifindex, ts, record_id)
TTL ts + INTERVAL 1 YEAR DELETE WHERE retention_class = 'short',
    ts + INTERVAL 2 YEAR DELETE WHERE retention_class IN ('standard', 'long')
SETTINGS ttl_only_drop_parts = 1, min_age_to_force_merge_seconds = 604800,
         min_age_to_force_merge_on_partition_only = 1;
```

Reasoning. Changes are sparse, so a narrow (attribute, old, new) layout avoids wide rows full of
defaults; the typed `new_oper_status` column serves the hot "flap count" query without string
parsing. "When did X change" = `WHERE tenant_id, device_id, ifindex ... ORDER BY ts DESC LIMIT n`,
served in reverse key order (F51). "What was the state at T" = `argMax(new_value, ts) WHERE ts <= T
GROUP BY attribute`, plus the samples table for the baseline when no change row exists before T.
Changes are retained longer than samples because they are small. The minmax index on `ts` is cheap
and helps time-only scans across devices ("all flaps in the last hour in tenant X").

### 12.3 FDB (MAC table) changes with snapshot markers

```sql
CREATE TABLE flowseer.fdb_changes
(
    tenant_id     LowCardinality(String),
    device_id     UUID,
    mac           UInt64            CODEC(ZSTD(1)),     -- 48-bit MAC in 8 bytes; T64 would crop the top 16 bits
    ts            DateTime          CODEC(Delta, ZSTD(1)),
    op            Enum8('add' = 1, 'remove' = 2, 'snapshot' = 3),
    record_id     UUID,
    snapshot_id   UInt32            CODEC(T64, ZSTD(1)), -- 0 for add/remove; monotonically increasing per device for snapshots
    port_ifindex  UInt32            CODEC(T64, ZSTD(1)),
    vlan          UInt16            CODEC(T64, ZSTD(1)),
    entry_status  Enum8('other' = 1, 'invalid' = 2, 'learned' = 3, 'self' = 4, 'mgmt' = 5),
    retention_class Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    INDEX idx_mac mac TYPE bloom_filter(0.01) GRANULARITY 4,
    INDEX idx_ts  ts  TYPE minmax GRANULARITY 4
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY toMonday(ts)
ORDER BY (tenant_id, device_id, mac, ts, op, record_id)
TTL ts + INTERVAL 180 DAY DELETE WHERE retention_class = 'short',
    ts + INTERVAL 1 YEAR DELETE WHERE retention_class IN ('standard', 'long')
SETTINGS ttl_only_drop_parts = 1, min_age_to_force_merge_seconds = 604800,
         min_age_to_force_merge_on_partition_only = 1;
```

Reasoning. `mac` in the key before `ts` makes "history of MAC X on device D" a primary-index range
and keeps rows of one MAC contiguous (compression of port/vlan). The bloom filter serves the
cross-device "where was MAC X seen" query ("Rare values that are critical for search (e.g. ...
specific IDs)", F19). Snapshot rows share `op = 'snapshot'` and a `snapshot_id`; the set-at-T query
in F43 anchors on `max(ts) WHERE op = 'snapshot' AND ts <= T` and replays adds/removes to T. The
snapshot interval is the storage/read trade-off knob. For an 8.6B-row legacy-scale set table, the
lesson applied here is only "snapshots must be bounded": daily full snapshots of a 9,000-AP estate
are the cost driver and should be sized against the change rate before choosing daily vs weekly.
If a device can emit the same MAC add twice in one second, `record_id` keeps the key unique;
otherwise identical redeliveries collapse on merge. Set `ts` codec to `Delta` because within one MAC's
run timestamps increase.

### 12.4 Syslog log table

```sql
CREATE TABLE flowseer.syslog
(
    tenant_id     LowCardinality(String),
    device_id     UUID,
    ts            DateTime64(3)     CODEC(Delta, ZSTD(1)),   -- received time, ms (F9: finer only if needed)
    device_ts     DateTime          CODEC(Delta, ZSTD(1)),   -- device-reported time, 0 if absent
    record_id     UUID,
    facility      Enum8('kern' = 0, 'user' = 1, 'mail' = 2, 'daemon' = 3, 'auth' = 4, 'syslog' = 5, 'lpr' = 6, 'news' = 7,
                        'uucp' = 8, 'cron' = 9, 'authpriv' = 10, 'ftp' = 11, 'ntp' = 12, 'audit' = 13, 'alert' = 14, 'clock' = 15,
                        'local0' = 16, 'local1' = 17, 'local2' = 18, 'local3' = 19, 'local4' = 20, 'local5' = 21, 'local6' = 22, 'local7' = 23),
    severity      Enum8('emerg' = 0, 'alert' = 1, 'crit' = 2, 'err' = 3, 'warning' = 4, 'notice' = 5, 'info' = 6, 'debug' = 7),
    app_name      LowCardinality(String),
    proc_id       LowCardinality(String),
    msg_id        LowCardinality(String),
    hostname      LowCardinality(String),
    source_ip     IPv6,                                      -- IPv4 stored mapped (F11)
    message       String            CODEC(ZSTD(1)),
    structured    Map(LowCardinality(String), String) CODEC(ZSTD(1)),   -- RFC 5424 SD-PARAMs (F49)
    retention_class Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    site_id       LowCardinality(String),
    INDEX idx_msg message TYPE text(tokenizer = splitByNonAlpha,
                                    preprocessor = replaceRegexpAll(lower(message), '\\d{2}:\\d{2}:\\d{2}(\\.\\d+)?', ' ')) GRANULARITY 64,
    INDEX idx_sd  structured TYPE text(tokenizer = keyValuePairs) GRANULARITY 64,
    INDEX idx_sev severity TYPE minmax GRANULARITY 1
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY toDate(ts)
ORDER BY (tenant_id, device_id, ts, record_id)
TTL toDateTime(ts) + INTERVAL 30 DAY DELETE WHERE retention_class = 'short',
    toDateTime(ts) + INTERVAL 90 DAY DELETE WHERE retention_class IN ('standard', 'long'),
    toDateTime(ts) + INTERVAL 14 DAY RECOMPRESS CODEC(ZSTD(3))
SETTINGS ttl_only_drop_parts = 1;
```

Reasoning. Daily partitions match a 30 to 90 day retention (30 to 90 partitions, F7, F8) and let
`ttl_only_drop_parts` drop whole days. ORDER BY tenant, device, ts serves "newest first for device"
in reverse key order with lazy materialization of `message` (F51). Enum8 for facility/severity (closed
RFC 5424 sets, validated at insert, F9); LowCardinality for open strings (F10). Text index with
`splitByNonAlpha` and a preprocessor that lowercases and strips clock tokens follows the docs' log
advice (F50); the `keyValuePairs` tokenizer on the Map is the documented way to search SD-PARAMs by
key and value together. The minmax on `severity` lets "errors only" skip debug-heavy granules when
severity is clustered per device burst; verify with `EXPLAIN indexes=1` and drop it if it does not
skip (F19). Recompress to `ZSTD(3)` after two weeks (F35) since older logs are read rarely.
`DateTime64(3)` is justified only if syslog relays preserve milliseconds; otherwise `DateTime`. The
`record_id` in the key makes two identical messages in the same millisecond distinct and makes a
redelivery collapse on merge (F45). If a text index on `ReplacingMergeTree` must be avoided on 26.3
LTS, note that 25.12 already allowed it ("The text index now works with ReplacingMergeTree tables",
F38), so both LTS lines are fine.

---

## 13. Open points and unverified list

1. Async-insert default version: settings page says 26.2, 26.3 release post says 26.3 LTS (F15).
   Set `async_insert` explicitly either way.
2. `timeSeriesRateToGrid` counter-reset semantics and the existence of the Delta/Changes/Resets/
   Deriv/PredictLinear ToGrid siblings (F29): not on fetched pages.
3. `WITH FILL`, `INTERPOLATE`, `STALENESS` introduction versions (F31).
4. `do_not_merge_across_partitions_select_final` reference page and its "added in 20.10" (F40).
5. Refreshable materialized view production-ready version (F21).
6. A TTL expression that uses a per-row interval column (F36); the docs only show constant intervals
   plus `DELETE WHERE` filters.
7. Row-policy count performance at thousands of tenants (F53).
8. Lightweight DELETE introduction version 22.8 (third party only) and whether a lightweight delete
   satisfies erasure obligations (F57).
9. Lightweight UPDATE "beta by default in 25.8" (third party only) (F47).
10. `result_overflow_mode`, `queue_max_wait_ms` reference pages not located (F59).
11. `<prometheus>` server config block details (endpoint, port, metrics, events, asynchronous_metrics,
    errors) not located; only the monitoring page's statement that it exists (F67).
12. Keeper CPU/RAM/disk sizing numbers exist only in third-party guides (F65).
13. Restore of a replicated table onto a replica set, and the interaction of `BACKUP ON CLUSTER` with
    the Replicated database engine (F66).
14. ASOF JOIN on `DateTime64` columns: the type list names DateTime, not DateTime64 (F42).
15. Whether `merge_workload` affects anything beyond IO given "CPU scheduling is not supported for
    merges and mutations yet" (F56).
16. Whether async-insert flushes honour `insert_quorum` (carried over from the first pass).
17. Snapshot interval for FDB/neighbor/client sets: needs measurement of change rate vs set size on
    real devices before fixing daily vs weekly (F43, 12.3).
18. Sparse serialization interaction with `present_mask`-style schemas: whether absent counters stored
    as 0 reach the 0.9375 default ratio in practice (F71).
