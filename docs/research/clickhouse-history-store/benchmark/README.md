# History store benchmark (ClickHouse 26.8, single node)

This suite checks that the history store design scales predictably: a fixed tenant T0
reads the same rows and runs at the same latency while the other tenants' data grows
16x and while T0's own history grows in time. It runs one representative table set per
pattern (samples, changes, presence, events, probes), the dimension dictionaries, the
analyst and partner row policies, and a redelivery pass for dedup.

The design under test is the one the
[history store record](../../../architecture/2026-10-08-history-store-direction.md)
states (tables per domain, five patterns, idempotent rollups, presence with walk
markers, one read-only service user plus row policies for analysts, retention classes,
ClickHouse 26.8), with table shapes from the research dossiers in
`docs/research/clickhouse-history-store/` (04, 06, 07, 10, 11, 02 F56).

Every client runs with `prefer_column_name_to_alias = 1`. Without it, ClickHouse 26.8's
analyzer resolves a name such as `site_id` to an aggregate aliased `site_id` elsewhere in
the same query, and several queries here reuse a column name as an alias.

## Files

| File | Purpose |
| --- | --- |
| `00_schema.sql` | Database `flowseer`: 14 tables, 9 materialized views, 3 dictionaries |
| `01_users.sql` | CPU resource and workloads, profiles, 5 users, partner mapping, 3 row policies |
| `02_generate.sql` | Deterministic in-database generator, split into `-- @<section>` blocks |
| `gen.sh` | Runs the generator sections for one step with query parameters |
| `03_queries.sql` | 73 named queries: monitoring, OLAP, partner, row policy, checks, dedup |
| `run.sh` | Driver: container, schema, steps, settle, measure, collect, sweep, dedup, summary |

## Running it

```bash
./run.sh all          # about 2 to 4 hours, results in results/
./run.sh step s4      # one step at a time is possible after start and init
./run.sh destroy      # container and volume
```

`run.sh start` runs the pinned image
`clickhouse/clickhouse-server@sha256:9b61e3c635c04ad5bb521eb4f6e61ce7585b5580814e51c25bb9e8292ce43364`
with `--memory 7g --cpus 11`, the named volume `fsbench-data`, password `bench`, and a
`config.d` file that enables `part_log`. The driver talks to the server only through
`docker exec ... clickhouse-client` with `--param_<name>=value` query parameters and
settings as command-line options, both documented on
<https://clickhouse.com/docs/interfaces/client>. SQL files go in through stdin in batch
mode, which runs several statements per call. `--multiquery` is not passed because that
page no longer lists it, and `--ignore-error` ("Do not stop at a query that failed ...
Only applicable in batch mode") lets the schema and user files continue past a statement
26.8 rejects.

Production uses the `Replicated*` variant of every engine on a 1 x 2 cluster. A single
node without Keeper uses `MergeTree`, `ReplacingMergeTree`, `AggregatingMergeTree`,
`SummingMergeTree`. That changes nothing measured here except insert-block
deduplication, which non-replicated tables leave off (`non_replicated_deduplication_window`
is 0). The redelivery pass inserts a different block than the original, which a
replicated table would also store, so the dedup results carry over.

## Dataset

Every value is a function of `cityHash64(20261008, ...)` over tenant, device, entity, and
time, so T0's rows are byte-identical at every step and a redelivered row equals its
original. Time parameters are unix seconds. The measured window is the 7 days before the
most recent UTC midnight at `run.sh init` (stored in `results/meta.env`), so no TTL fires.

| Item | Model |
| --- | --- |
| Tenants | T0: 200 devices, 10 sites, 3 regions. Each unit: 40 tenants with `round(58/k)` devices (58, 29, 19, 14 ... 1), 248 devices, 41 sites. Partner set P = T0 plus the 19 largest tenants of unit 1 (404 devices) |
| Devices | access 48 ports 80 %, distribution 52 ports 8 %, 500-port chassis 0.5 % (T0 has two), router 8, firewall 12. Vendors 6 (cisco 50 %), models vendor-role-variant |
| Firmware | 9.1/9.2/9.3 at 60/30/10 %. 30 % of cisco devices upgrade to `cisco-9.4` between day 3 and day 5 of the window, so `software_version` flips mid-window |
| Interfaces | 5-minute polls with per-device phase and 0 to 2 s jitter. 62 % connected, 20 % of those active (1 to 900 Mbit/s), the rest 1 to 50 kbit/s. Counters are monotone inside an epoch (diurnal integral plus a bounded per-sample term). Resets every 10 to 120 days per interface. Half report `discontinuity_at`, with a 1 s spurious shift on 5 % of samples. 3 % faulty ports with an error rate set by (model, firmware), and the upgrade cuts it to a quarter |
| Changes | Link flaps 0.5 %/day of connected ports (5 %/day on faulty ports), 5 to 60 min down, written as down and up rows. Samples carry the same down window |
| Presence | Members per device: access lognormal median 300 (50 to 2,000), distribution 2,000, chassis 20,000, routers 50 to 200. Slot lifetime 20 days (5 %/day churn), port moves 0.5 %/day, 5 % roaming hosts shared per site. Evidence per six-hour block, 288 walk markers per device-day with member count and set hash, 3 % partial walks |
| Syslog | Lognormal rate, median 25 per device-hour. Severity mix of dossier 07 section 6.1. Cisco, Huawei, and RFC 5424 templates. One T0 storm (30,000 lines in one hour, 95 % identical) and 0.2 % storm device-days elsewhere. 80 % of link flaps also log a link line |
| Probes | Two targets per site (gateway, internet anchor), one row per minute with 10 RTTs, lognormal around the target base (median 6 ms for anchors), 0.2 % random loss, 2 % of target-hours with a 30-minute loss episode, 2 % of targets dark for a day |

### Steps

| Step | What is added | Tenants | Devices | Days held |
| --- | --- | --- | --- | --- |
| s1 (1x) | T0 + unit 1 | 41 | 448 | 7 |
| s1t (time) | 7 earlier days for T0 + unit 1 | 41 | 448 | 14 |
| s4 (4x) | units 2 to 4, window days | 161 | 1,192 | 14 / 7 |
| s16 (16x) | units 5 to 16, window days | 641 | 4,168 | 14 / 7 |
| sweep | none (scope 10, 25, 50, 100, 200 T0 devices) | | | |
| redeliver | 1 % of T0's rows of every raw table, 14 days | | | |

Other-tenant data grows 1x, 4x, 16x. Total raw interface rows grow 1x, 2x, 3.6x, 10x
across s1, s1t, s4, s16 because T0 and unit 1 stay fixed.

### Rows per table and disk (estimates)

Rows from the generator parameters (`build` arithmetic, not measured). Bytes per row are
assumptions to size the disk: raw samples 30 B, hourly rollups 130 B (entity-first) and
200 B (time-first), syslog 80 B plus 30 B for `syslog_by_scope`, presence 12 B plus 14 B
by MAC, probes 35 B. The run reports the real values in `tables_<step>.tsv`.

| Table | s1 | s1t | s4 | s16 |
| --- | --- | --- | --- | --- |
| interface_samples | 43 M | 85 M | 155 M | 432 M |
| interface_hourly, interface_hourly_by_time (each) | 4.4 M | 8.7 M | 16 M | 44 M |
| interface_daily | 0.2 M | 0.4 M | 0.7 M | 1.9 M |
| interface_changes | 1.5 k | 3 k | 5 k | 15 k |
| fdb_presence, fdb_presence_by_mac (each, merged) | 2.0 M | 3.9 M | 7.0 M | 19 M |
| fdb_vlan_daily | 31 k | 63 k | 115 k | 323 k |
| set_walks | 0.9 M | 1.8 M | 3.3 M | 9.3 M |
| syslog, syslog_by_scope (each) | 3.1 M | 6.2 M | 11 M | 32 M |
| syslog_daily | 78 k | 157 k | 287 k | 0.8 M |
| probe_intervals | 1.0 M | 2.1 M | 4.5 M | 15 M |
| probe_hourly | 17 k | 35 k | 75 k | 0.24 M |
| Estimated data on disk | 3 GB | 6 GB | 12 GB | 33 GB |

Peak disk at s16 is about 33 GB plus the largest running merge (one weekly partition of
`interface_samples`, about 9 GB), inside the 60 GB budget. Each generator INSERT is one
hour (samples, syslog, probes), one six-hour block (presence), or one day (changes, walk
markers) and runs with `max_memory_usage = 4 GB`, `max_insert_threads = 4`. The largest
single insert is about 1.7 M sample rows at s16.

## What is measured

Per query, step, and user, from `system.query_log` (runs 2 to 5 of 5, run 1 warms the
cache and is discarded): `read_rows`, `read_bytes`, `result_rows`, `memory_usage`,
`query_duration_ms` p50 and p95. With four kept runs, p95 is the slowest kept run.
Monitoring queries also get `EXPLAIN indexes = 1` with the query condition cache and
skip-index-on-read off, as dossier 07 section 6.3 prescribes
(<https://clickhouse.com/docs/sql-reference/statements/explain>).

Per step: bytes per row per table and per column (`system.parts`, `system.columns`),
parts per partition, dictionary memory, volume size, rows per table for T0 and in total,
and insert throughput per generator section and target table from `system.part_log`
joined to the INSERT's `log_comment` (rows per second, new parts per insert).

| Output | Content |
| --- | --- |
| `summary.tsv` | per query: read_rows and p95 per step, ratios, model ratio, pass flags |
| `query_stats_<step>.tsv`, `query_errors_<step>.tsv` | raw per-step statistics and failures |
| `partner_<step>.tsv` | read_rows for P against the sum of the 20 per-tenant runs |
| `sweep.tsv` | read_rows and p95 by scope size |
| `dedup.tsv` | snapshots before, right after, and after settle of the redelivery |
| `checks_<step>.tsv` | row-policy visibility, dictionary leak, walk digest, stamp checks |
| `tables_*`, `columns_*`, `partitions_*`, `inserts_*`, `dictionaries_*`, `rows_*`, `disk_*` | storage and ingest |
| `explain/<step>/<qid>.txt`, `granules_<step>.tsv` | index use for monitoring queries |

Settle between generation and measurement does not run `OPTIMIZE`. It waits until
`system.merges` shows no merge for `flowseer` in three polls 10 s apart, for at most
30 minutes, and records the wait in `settle.tsv`.

## Cost model per query

Notation from dossier 04 section 5: G = 8,192 rows per granule, P = parts that hold the
key range, E = entities, D = devices, H = hours in the window. `run.sh` evaluates each
model with the query's own predicate (`uniqExact(_part)` gives P at the moment of the run)
and stores it in `bench.model`. No term is the total table size or the tenant count.

| Id | Pattern, shape | read_rows model | Grows with |
| --- | --- | --- | --- |
| M01 | samples, newest 200 of one interface | P x 2 x G | parts of the entity |
| M02 | samples, 7-day raw rate chart | P x 2 x G | parts of the entity |
| M03 | samples, value at T (no lower bound) | P x 2 x G | parts of the entity, so s1t adds the backfilled week |
| M04 | changes, when it changed | P x G | nothing |
| M05 | samples, scope 24 h on `interface_hourly_by_time` | 24 x E_tenant + P x 2 x G | T0's interfaces |
| M06 | samples, top 20 by utilisation in scope | as M05 | T0's interfaces |
| M07 | presence, set at T | (M + G) x P for one device-day | the device's members |
| M08 | presence, where was MAC X, 7 days | days x devices that saw it + P x G | the MAC's path |
| M09 | events, site errors newest first, two-step | P x 2 x G + 100 x G | nothing |
| M09N | events, same via device list | D x G x P | scope size |
| M10 | events, one device newest first | P x 2 x G | nothing |
| M11 | events, full text in the tenant, 7 days | granules holding a match x G | T0's matches |
| M12 | probes, p95 per target per day, rollup | 2 x 168 + P x G | nothing |
| M13 | probes, raw RTT chart one target 24 h | 1,440 + P x G | nothing |
| M14 | samples, scope 24 h on raw (anti-query, `default` user) | E_scope x 288 + D x P x G | scope size |
| M15 | changes in scope, 24 h | D x P x G | scope size |
| O01 | Q1 error rate by model and firmware (`interface_daily`) | 7 x E_tenant + P x 2 x G | T0's interfaces |
| O03 | Q3 errors and flaps in the same hour | 168 x E_tenant + changes | T0's interfaces |
| O06 | Q6 FDB growth per VLAN per site (`fdb_vlan_daily`) | 70 x D_tenant + P x G | T0's devices |
| O06R | Q6 on raw presence, for contrast | 7 x members of T0 | T0's members |
| O08, O09 | Q8, Q9 syslog volume, top classes (`syslog_daily`) | 175 x D_tenant | T0's devices |
| O11 | Q11-style fraction of busy hours per model | 168 x E_tenant | T0's interfaces |
| O22 | Q22 probe p95 and loss per site per day | 168 x targets | T0's targets |
| O23 | cohort firmware A vs B, device list from the mirror | 7 x E_model + D_model x G x P | the model's devices |
| O23D | same with `dictGet` in `WHERE` | 7 x E_tenant | T0's interfaces |
| O30 | syslog errors by region via `dictGetHierarchy` | as O08 | T0's devices |
| O31 | interface errors in a region via `dictIsIn` | 7 x E_tenant | T0's interfaces |
| PM05, PM08, PM11, PO01, PO08, PO22 | the same shapes with `tenant_id IN P` | sum of the per-tenant models | P's size |
| R01 to R04 | M05 and Q1 shapes, explicit tenant vs `analyst_t0` row policy | as M05, O01 | as M05, O01 |
| R10 to R14, R20 to R22 | partner shapes: explicit IN, `dictHas` policy, IN-subquery policy | sum over P | P's size if the policy prunes |

## Pass criteria

| Check | Pass |
| --- | --- |
| T0 read_rows against the model | within 2x at every step (`within_2x_model`) |
| T0 read_rows across s1, s1t, s4, s16 | max over min at most 1.10 (`rr_flat_10pct`) |
| T0 p95 across steps | max over min at most 1.5 (`p95_flat_1_5x`) |
| Partner P | read_rows(P) equals the sum of the 20 per-tenant runs within 10 %, and flat across steps |
| Row policy cost | R02 equals R01 and R04 equals R03 in read_rows. The latency difference is the policy's cost |
| Mapping policies | R12 and R14 equal R10. R11 is expected to read every tenant's rows in the window and fail flatness (a per-row `dictHas` cannot prune the key) |
| Scope sweep | M05, M06 flat in scope (tenant range), M09N, M14, M15 linear in scope within 2x |
| Rollup idempotence | D05, D06, D07 (hourly, time-first hourly, daily) identical before, right after, and after settle of the redelivery; D08 rollup sample count equals the raw distinct count |
| Raw dedup | D01, D03, D04 identical; D02 grows by the redelivered rows until merges collapse them |
| Non-idempotent aggregates | D12 (`syslog_daily`), D14 (`probe_hourly.sent`), D18 (`fdb_presence.walks`) differ by the redelivered rows; reported, not a failure (dossier 07 E20, dossier 10 section 4.4) |
| Checks | C01 shows only `t0000`; C02 and C03 show 20 tenants t0000 to t0019; C05 and C06 report 0 mismatches; C07 observed equals expected |
| Storage | bytes per row reported per table and column for every step |

Known exceptions the run documents rather than fails: M03 reads one granule per part of
every week that holds the entity, so it rises at s1t. O06R, O23D, M14 exist to show the
cost the rollups and device lists avoid.

## What 26.8 may not accept, and what is not measured

| Item | Handling | Source |
| --- | --- | --- |
| Text index `GRANULARITY 64` (dossier 07) | Dropped. "text indexes use an infinite granularity (100 million)" and "An explicitly specified index granularity is ignored." Text indexes are GA since 26.2 and need no setting | <https://clickhouse.com/docs/engines/table-engines/mergetree-family/invertedindexes> |
| Text index on the ALIAS column `sd_items` (dossier 07 `idx_sd`) | Left out; indexing an ALIAS column is not confirmed, and the generator writes no structured data | same page |
| Row policy conditions calling `dictHas`, `currentUser()`, or an `IN (SELECT ...)` | Used for the partner policies and marked MAY REJECT in `01_users.sql`; the reference page states the syntax but says nothing about functions or subqueries in the condition (unverified) | <https://clickhouse.com/docs/sql-reference/statements/create/row-policy> |
| Dictionaries and row policies | `dictGet` is not filtered by a row policy as far as the pages say; check C04 measures whether a policy-bound user can read another tenant's dimensions | same page |
| Hierarchical dictionary keyed (tenant, site) | Not possible: "ClickHouse supports hierarchical dictionaries with a numeric key". The location key is `cityHash64(tenant_id, site_id)` with `LAYOUT(HASHED())` | <https://clickhouse.com/docs/reference/statements/create/dictionary/layouts/hierarchical> |
| `max_bytes_ratio_before_external_group_by`, `..._sort` in profiles | Used per dossier 11 section 5.1 and marked MAY REJECT (setting page not fetched) | dossier 11 section 8 |
| CPU workloads | Created per the docs; "Memory reservation scheduling is experimental" and merges are not CPU-scheduled, so neither is used. The run is sequential, so scheduling changes only the analytics thread cap | <https://clickhouse.com/docs/operations/workload-scheduling> |
| `ARRAY JOIN` inside an incremental MV (dossier 10 section 4.3 open point) | Used in `probe_hourly_q_mv`; a second MV carries counts and `intervals_lost`, so a rejected `ARRAY JOIN` breaks only the quantile column | dossier 10 section 4.3 |
| `nonNegativeDerivative` window function (M02) | Used as dossier 04 F28 cites it; not re-fetched in this pass (unverified) | dossier 04 section 3.1 |
| Parallel replicas, isolation under concurrent inserts (dossier 11 section 7.3 S5) | Not measured: a single node without Keeper cannot produce them | dossier 11 section 7 |
| TTL by class with `ttl_only_drop_parts` on mixed-class parts (`interface_samples`, rollups) | Not measured: no TTL fires inside the 14-day dataset | decision 6 |
| Insert-block dedup | Off on non-replicated tables; see above | <https://clickhouse.com/docs/engines/table-engines/mergetree-family/replication> |

`run.sh` applies `00_schema.sql` and `01_users.sql` with `clickhouse-client --ignore-error`
and keeps going when a measured query fails, logging to `results/client_errors.log` and
`query_errors_<step>.tsv`, so a rejected policy, profile setting, or query costs its own
rows only. A generator INSERT that fails stops `gen.sh`, because a step with missing data
would make every comparison after it meaningless.
