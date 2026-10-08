---
title: History store benchmark results
date: 2026-10-08
status: research; one run on a single ClickHouse 26.8.20.9 node in Docker, 11 CPUs, 7 GiB
---

# History store benchmark results

The [benchmark suite](benchmark/README.md) ran on 2026-10-08 against the
image `clickhouse/clickhouse-server@sha256:9b61e3c6…` (tag 26.8, server
26.8.20.9) with 11 CPUs and a 7 GiB memory limit. It tests whether a query's
cost follows its scope and not the store's total size. One tenant, T0 (200
devices, about 9,600 interfaces), keeps identical data at every step while
the other tenants grow:

| Step | Devices | `interface_samples` rows | What changes |
| --- | --- | --- | --- |
| s1 | 448 | 43 M | baseline, 7 days |
| s1t | 448 | 85 M | 7 earlier days added |
| s4 | 1,192 | 155 M | 3 more tenant units |
| s16 | 4,168 | 430 M | 12 more tenant units |

Each query ran five times per step, and the first run was discarded. The
tables below are taken from `benchmark/results/`.

## Result

Every monitoring and OLAP query for T0 read about the same rows at 16x as at
1x, or fewer, and its p95 latency did not grow. Two findings changed the
design, and one anomaly is open.

| Shape | Query | Rows read, s1 → s16 | p95 ms, s1 → s16 |
| --- | --- | --- | --- |
| Device history, newest first | M01 | 131,072 → 32,768 | 8 → 5 |
| Value at time T | M03 | 90,112 → 98,304 | 8 → 9 |
| Scope 24 h from the time-first rollup | M05 | 319,488 → 303,104 | 61 → 47 |
| FDB set at time T | M07 | 24,576 → 32,768 | 16 → 8 |
| Syslog errors in a site, newest first | M11 | 1,261,568 → 1,335,296 | 20 → 24 |
| Error rate by model and firmware, 7 days | O03 | 2,065,576 → 2,066,738 | 212 → 275 |
| Fraction of hours above a threshold | O11 | 2,064,384 → 2,056,192 | 343 → 397 |
| Firmware cohort comparison | O23 | 180,672 → 90,560 | 19 → 10 |
| Errors by region through the location hierarchy | O31 | 196,608 → 114,688 | 65 → 25 |
| Probe p95 per site and day | O22 | 11,762 → 8,192 | 8 → 8 |

Rows read fall where more tenants made merges produce larger parts, since a
read rounds out to whole granules of 8,192 rows. The small change tables
(M04, M15) rise from a few hundred rows to about one granule as the table
fills its first granules, then stay there.

### Row policies: the `IN (SELECT …)` form, never `dictHas`

| Query | Policy | Rows read, s1 → s16 |
| --- | --- | --- |
| R20 | none, explicit `tenant_id IN (…)` | 351,319 → 212,992 |
| R22 | `tenant_id IN (SELECT tenant_id FROM user_tenants WHERE user = currentUser())` | 351,359 → 213,032 |
| R21 | `dictHas('user_tenants_dict', (currentUser(), tenant_id))` | 368,816 → 2,285,372 |

A per-row function cannot narrow the primary key, so the `dictHas` policy
read every tenant's rows in the window and grew with the store. The
subquery form cost the same as the explicit filter.

### Provider queries over 20 tenants

A query over the 20 tenants of a provider read 0.1 to 1.0 times the rows of
the 20 single-tenant queries added together (`benchmark/results/partner.tsv`),
because one pass over adjacent key ranges shares granules that 20 passes read
separately. Its cost is bounded by the sum.

### Rollups were larger than the raw data

The first run stored `argMin` and `argMax` states per counter and a
`uniqExact` state for the sample count. At s16 the hourly rollup took 181
bytes per row and its time-first copy 172, together 2.7 times the raw
samples (`benchmark/results/tables_s16_argminmax_rollups.tsv`). Within one
discontinuity epoch a counter is monotonic, so `min` and `max` are its first
and last values. The second run used `SimpleAggregateFunction(min|max)` with
`Delta` codecs and a minute bitmap (`groupBitOr`) for the sample count:

| Table | Bytes per row, argMin/argMax | Bytes per row, min/max | GiB at s16 |
| --- | --- | --- | --- |
| `interface_samples` (raw) | 13.4 | 13.4 | 5.4 |
| `interface_hourly` | 181 | 28.1 | 1.2 |
| `interface_hourly_by_time` | 172 | 54.7 | 2.2 |
| `interface_daily` | 467 | 40.7 | 0.1 |

Rollups now take 0.65 times the raw samples. The time-first copy is half of
that: sorted by hour, neighbouring rows belong to different interfaces and
`Delta` finds little to remove. Raw-sample inserts with three views attached
rose from about 360,000 to 750,000 rows a second.

### Deduplication after a 1 % redelivery of T0

Every rollup fingerprint, every read through `FINAL` or `LIMIT 1 BY`, and the
rollup sample count against the distinct raw samples gave the same value
before and after redelivery (`benchmark/results/dedup.tsv`). Raw counts read
without `FINAL` stayed 1 % high until merges ran. The three aggregates known
not to be idempotent (`syslog_daily` counts, `probe_hourly.sent`, walk counts)
grew by the redelivered rows.

### Scope sweep

With the time-first rollup, a scope of 10 to 200 devices read the same
303,104 rows, the tenant's whole 24 hours, and latency rose from 71 to 171 ms
with the size of the device list. Its cost follows the tenant, not the scope,
which holds for T0 and is unmeasured for a tenant of 10,000 devices. The raw
table's scope query grew linearly with scope (1.3 M to 8.6 M rows), which is
why scope queries never run on raw tables.

### Storage at s16

| Table | Rows | Bytes per row on disk |
| --- | --- | --- |
| `interface_samples` | 430 M | 13.4 |
| `fdb_presence` | 23.9 M | 5.3 |
| `fdb_presence_by_mac` | 25.5 M | 8.6 |
| `probe_intervals` | 14.5 M | 21.7 |
| `syslog`, text index included | 31.1 M | 54.0 |
| `syslog_by_scope` | 31.1 M | 21.9 |
| `set_walks` | 9.3 M | 17.0 |

## Open

- M13, a probe chart for one target over 24 hours, read 92,465 rows at s16
  where the 1,440 rows it needs sit in one granule of one part. `EXPLAIN`
  shows the primary key analysis selecting 12 granules. It costs 4 ms, and
  the cause is not explained.
- One node holds no replica, so insert deduplication by block, quorum, and
  parallel replicas were not measured.
- No TTL fired, since the data spans 14 days.
- The time-first rollup's cost for a 10,000-device tenant is extrapolated,
  not measured.
