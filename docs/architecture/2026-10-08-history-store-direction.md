---
title: History Store - Direction
type: direction
date: 2026-10-08
topic: history-store
status: proposed-direction
amends: docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md
---

# History Store - Direction

How FlowSeer keeps observation history in ClickHouse: which table shapes
exist, how a domain picks one, how tenants, duplicates, retention, and
dimensions are handled, and what bounds the cost of a query. It covers every
data domain the schema and the records name, present or planned, and serves
monitoring reads and fleet analytics from the same tables.

## Context

The [ingestion pipeline record](2026-10-02-central-ingestion-pipeline-direction.md)
sends append-only records and numeric time-series to ClickHouse and leaves
three questions open there: tenant isolation, query limits, and retention per
record type. Only `SyslogRecord` has a record type today
(`spec/proto/flowseer/integration/ingest/v1/ingest_record.proto:25-30`), but
the [domain map](../research/clickhouse-history-store/00-domain-map.md)
lists about 60 domains, and roughly 40 of them change over time and belong in
the store. A table per record type, designed one at a time, would give 40
shapes. This record fixes the few shapes the domains share, so a later
domain picks one instead of inventing a table.

Three facts from the tree constrain the design:

- Delivery is at least once. A central stream drops a repeat inside its ten
  minute duplicate window, and past it a repeat is stored again
  (ingestion record, 2026-10-04 amendment).
- Ingestion volume follows change: "delta events, not snapshots"
  ([device service record](2026-08-20-device-service-and-inventory-direction.md),
  lines 380 to 382).
- Current state lives in JetStream KV and lists in Postgres. ClickHouse
  never serves latest state through a materialized view (ingestion record,
  Alternatives).

The evidence is in the
[history store research](../research/clickhouse-history-store/README.md):
ClickHouse 26.8 guidance, 23 prior-art systems, one dossier per domain group,
an OLAP review, and a benchmark.

## Decision

### Tables per domain, in five patterns

A domain gets typed tables named after it, shared by all tenants. Every table
follows one of five patterns, chosen by the shape of what is written:

| Pattern | What a row is | Engine and key | Duplicates | Examples |
| --- | --- | --- | --- | --- |
| Samples | One entity at one poll, numeric values plus slow status fields | `ReplacingMergeTree`, `(tenant_id, device_id, <entity>, ts)`, weekly partitions | the natural key collapses a repeat at merge | interface counters, CPU, sensors, radios, PoE, optics |
| Changes | One transition of one attribute | `ReplacingMergeTree`, `(tenant_id, device_id, <entity>, ts, record_id)` | `record_id` | interface status, radio channel, protocol adjacencies |
| Presence | One member of a device's set on one day, with first and last seen | `AggregatingMergeTree`, `(tenant_id, device_id, day, <member key>, <history attribute>)` | `min` and `max` are idempotent | FDB, LLDP and CDP, ARP and ND, RF neighbors, clients |
| Events | One message or notification | `ReplacingMergeTree`, `(tenant_id, device_id, ts, record_id)`, weekly partitions | `record_id` | syslog, SNMP notifications, alarms, entity transitions, audit |
| Probes | One measurement interval holding its raw results | `ReplacingMergeTree`, `(tenant_id, device_id, <target>, interval_start)` | the natural key | ICMP and later active probes |

Why: prior art with a known schema (Akvorado, Snuba, Glaber) uses typed tables
per domain, and the generic tables (ntopng, Telegraf, qryn) paid in mutations,
full scans, or an `ALTER` per new tag
([03](../research/clickhouse-history-store/03-prior-art.md)). FlowSeer's
records are typed protobuf, so the schema is known when the code builds. A
domain is not a source: an SNMP poll, a gNMI stream, a trap, and a webhook
that report the same interface land in the same table.

Flows are the one exception. Their volume follows traffic, not devices, so
they get their own database when a flow adapter exists
([10](../research/clickhouse-history-store/10-wan-phy-future.md), G28).

### Rollups beside every samples table

Each samples table has an hourly and a daily rollup fed by an incremental
materialized view from the raw table, never from another rollup. The hourly
rollup has a second, time-first copy keyed `(tenant_id, hour, device_id,
<entity>)` for scope and top-N queries. A rollup holds `min` and `max` per
counter, which are its first and last values because the discontinuity epoch
is part of the key, plus a bitmap of the poll slots it saw. Every aggregate
in a rollup is idempotent, so a redelivered sample changes nothing.

Why: a month-long chart or a fleet query then reads rollup rows, and the
query shape that stalled ingest on the legacy store for two minutes
(`docs/runbooks/production-clickhouse-queries.md`) has no reason to exist.
Projections are not used: they block lightweight deletes and parallel
replicas ([11](../research/clickhouse-history-store/11-olap.md), O6).

### One key contract across all tables

Shared columns have one name and one type everywhere: `tenant_id
LowCardinality(String)`, `device_id UUID`, `interface_name
LowCardinality(String)`, `mac UInt64`, `ts` (observed time, from
`Provenance.observed_at`), `hour DateTime`, `day Date`. `ts` is `DateTime`
in samples, changes, presence, and probes, whose sources poll in seconds,
and `DateTime64(3)` in events, whose messages carry milliseconds. Joins
across domains meet on `hour` or `day`, never on `ts`. The entity key is the
stable device-local name, never an index that resets (`Interface.if_index` is
"not stable across restarts", `spec/proto/flowseer/net/interface/v1/interface.proto:33-35`).
Columns that a protobuf enum fills are `UInt8` holding the enum number,
because an unknown non-zero value stays valid. No column is `Nullable`: a
field the source did not report is a bit in a presence mask.

Why: analysts join domains on these columns
([11](../research/clickhouse-history-store/11-olap.md), O5), and a join on
two spellings of one key silently returns nothing.

### Dimensions: two stamped, the rest by dictionary

Every fact row carries `location_id`, `site_id`, and `software_version` as
they were when the row was observed. `location_id` is the location the device
names, which may be a rack or a room (`spec/proto/flowseer/model/inventory/v1/device.proto:100-113`).
`site_id` is its site ancestor and stays empty until central holds a location
tree that can resolve it, so rows written before then carry no site. Vendor, model, role, tags, integration, and the location
tree come from dictionaries keyed `(tenant_id, device_id)` and mirrored from
the inventory, with range dictionaries for "as it was at T".

Why: site and firmware change during a device's life and analysts compare
cohorts by them, so the value at observation time must be on the row. The
other attributes rarely change or are wanted as they are now, and a stamp
would cost bytes on billions of rows
([11](../research/clickhouse-history-store/11-olap.md), O3, O4).

### Tenants are flat, and a lookup names a set of them

A row holds only its owning `tenant_id`. ClickHouse stores no tenant
hierarchy. A query names a non-empty set of tenants, which the service
resolves per request from authorization: partner links today, any later tree.
Tenancy stays ambient: for a read whose authorization rule allows several
tenants, the `X-FlowSeer-Tenant` header lists them, and the interceptor admits
each one in a single batch check and fails the request if any is denied.
The interceptor reads every `X-FlowSeer-Tenant` line, refuses duplicates
and empty items, and caps the set at 25 tenants, one authorization batch.
The service reads through one read-only user, and a Go query package refuses
a query without an authorized tenant set. Human and BI users read through row
policies of the form `tenant_id IN (SELECT tenant_id FROM <mapping> WHERE
user = currentUser())`, never through a per-row function such as `dictHas`.
(decided by the user, 2026-10-08)

Why: a changed hierarchy then never touches stored data. The tenant leads
every sort key, so a set of n tenants costs the sum of their scoped reads.
In the benchmark a `dictHas` policy read every tenant's rows, while the
`IN (SELECT …)` policy cost the same as an explicit filter (benchmark
section of the research).

### Set domains: the edge diffs, central checks a digest

For a set a device reports in full on each poll, the edge keeps the last walk
and emits deltas plus one marker per walk carrying a complete flag, the
member count, and an order-independent set hash. Central keeps only the
count and hash per device, compares them with each marker, and on a mismatch
asks the edge for one full walk and rebuilds that device's presence.
(decided by the user, 2026-10-08)

Why: bus volume follows change, as the device service record requires, and
central needs about 20,000 small keys instead of a copy of every set (about
200 million keys at 20,000 devices, unmeasured in KV). The digest catches a
lost delta, an edge restart, and a diff bug in either place
([06](../research/clickhouse-history-store/06-set-domains.md), section 9).

### Duplicates: idempotent shapes, then merge, then read

No ClickHouse mechanism stores a late redelivery exactly once
([01](../research/clickhouse-history-store/01-clients-durability-dedup.md),
section 3). The store therefore relies on three layers in order: shapes that
a repeat does not change (raw counters, `min`, `max`, bitmaps), a
`ReplacingMergeTree` key that collapses a repeat at merge, and `FINAL` or
`LIMIT 1 BY record_id` in reads that list or count rows. Each batch also
carries an `insert_deduplication_token` that hashes the batch's stream
sequences, so an in-window retry of the same batch is exact. A token built
from a range would drop a later batch with the same ends and different
members, which two central replicas sharing a consumer produce. Counts kept
in a summing rollup, such as syslog's daily counts, over-count a redelivery
past the window, and their readers say so.

### Writes: synchronous, acknowledged after one replica

The sink batches per table, inserts with `async_insert = 0`, and acknowledges
JetStream after one replica confirms. A replication lag alert bounds the
window in which a node loss could lose acknowledged rows. The central ingest
streams keep records for 72 hours, with their byte limit and the CENTRAL
account budget sized from the measured bytes per record, so a ClickHouse
outage over a weekend loses nothing at the planning rate. Above it the
streams discard their oldest records, and a discard counter makes that
visible. (decided by the user, 2026-10-08)

Why: `insert_quorum = 2` would stop every insert while one replica is down,
and the [deployment record](2026-10-03-deployment-direction.md) runs
ClickHouse as two replicas so that inserts continue with one down.
Asynchronous inserts are on by default since 26.2 or 26.3 (two ClickHouse
sources disagree), so the sink sets the mode explicitly
([02](../research/clickhouse-history-store/02-best-practices.md), F15).

### Retention: three classes per tenant

Each row carries `retention_class`, stamped from the tenant's configuration
(`TenantConfig.retention_class`, unset meaning standard), and each class
has a `TTL ... DELETE WHERE` rule. Partitions lead with the class, so a
part holds one class and expires whole. (decided by the user, 2026-10-08)

| Data | Short | Standard | Long |
| --- | --- | --- | --- |
| Raw samples, changes, presence, events, probes | 90 days | 1 year | 3 years |
| Hourly rollups | 1 year | 2 years | 5 years |
| Daily rollups | 2 years | 3 years | 5 years |
| Audit | 1 year | 3 years | 7 years |

Personal data (wireless clients, 802.1X sessions, DHCP leases) keeps 90 days
in every class. A tenant forget is a lightweight `DELETE` on every table,
followed by a physical `ALTER ... DELETE` off-peak.

### Queries cannot stall ingestion

Three users read: `svc_reader` for the service, `analyst` for fleet analytics,
and the ingest writer. Each has a settings profile with pinned limits on
time, memory, rows read, and threads, and a workload: analytics runs at the
lowest priority under a CPU resource. A query whose scope is a device list
never runs on a raw table without a row limit.

Why: one analytic query held inserts back for about two minutes on the legacy
store, and the profile limits plus workloads keep that from recurring
([02](../research/clickhouse-history-store/02-best-practices.md), F56;
[11](../research/clickhouse-history-store/11-olap.md), O7).

### Migrations are FlowSeer's own

A small Go migrator applies ordered, idempotent DDL from one job before the
writers start, against a `Replicated` database with `Replicated*` table
engines, and records each applied step in a replicated table. Users,
profiles, row policies, and workloads are not database objects, so the
migrator creates them `ON CLUSTER`. Writers never create schema.

Why: golang-migrate links the deprecated v1 client, goose fits replicated
databases poorly, and Akvorado, Uptrace, SigNoz, and Snuba each run their own
tool for this reason
([01](../research/clickhouse-history-store/01-clients-durability-dedup.md),
section 7, and [03](../research/clickhouse-history-store/03-prior-art.md)).

## Measured

A benchmark on one ClickHouse 26.8 node held one tenant's data fixed (200
devices) while the store grew from 43 million to 430 million interface
samples, ten times the rows and about nine times the devices
([12](../research/clickhouse-history-store/12-benchmark.md)):

- Every monitoring and OLAP query of that tenant read about the same rows,
  or fewer, at ten times the data, and its p95 latency did not grow. Device
  history ran in 5 ms, a site's syslog errors in 24 ms, and a seven-day error
  rate by model and firmware in 275 ms.
- A provider query over 20 tenants read at most the sum of the 20
  single-tenant reads.
- Rollups built from `argMin` and `argMax` states took 2.7 times the raw
  samples. With `min` and `max` per counter they take 0.65 times, and raw
  inserts with three views attached ran at about 750,000 rows a second.
- After a 1 % redelivery, every idempotent rollup and every `FINAL` or
  `LIMIT 1 BY` read gave the same result as before. The summing counts
  grew by the redelivered rows, as designed.
- Raw interface samples take 13.4 bytes per row, FDB presence 5.3, and
  syslog with its text index 54.

## Alternatives

- **One generic events table with a JSON payload.** Rejected: untyped, worse
  compression, and the generic-table regrets of the prior art.
- **A table or database per tenant.** Rejected: ClickHouse's guide says it
  "doesn't scale for 1000s of tenants", and migrations run per tenant.
- **A tenant path stamped on rows.** Rejected by the user: a changed
  hierarchy would then change how stored data is found.
- **Full snapshots per poll for set domains.** Rejected: volume follows set
  size times poll rate, 7 to 59 billion members a day at core scale, and it
  breaks the delta-events rule.
- **A central copy of every set.** Rejected for the digest: about 200
  million KV keys, unmeasured, and two diff engines that can disagree.
- **`insert_quorum = 2`.** Rejected: ingestion stops while a replica is down.
- **The ClickHouse `TimeSeries` engine and PromQL.** Rejected: experimental
  or private preview, and a metric name plus labels model has no typed
  domain columns.
- **Projections for second orderings.** Rejected: they block lightweight
  deletes and parallel replicas.

## Consequences

- Each domain other than syslog needs a record type before its tables exist.
  The dossiers list the missing messages and fields (each ends with a schema
  gaps section). The largest are a carrier per domain under `IngestRecord`,
  counter width, delta-reported counters, set deltas with walk markers, and an
  alarm instance key.
- The ingestion record's open questions on tenant isolation, query limits,
  and retention are answered here, and its stores table gains the five
  patterns.
- The central ingest streams grow from 24 to 72 hours of retention, and the
  CENTRAL account budget grows with them.
- Audit trails and entity transitions move to ClickHouse as events tables.
  JetStream stays their transport.
- The query package takes an authorized tenant set, not one tenant, so the
  authorization engine resolves partner links before every history read.
- The [operator authorization record](2026-09-30-operator-authorization-direction.md)
  admits one tenant per request and checks membership once, on that tenant.
  It gains an amendment for methods whose rule allows a tenant set: each
  listed tenant is admitted, and the request fails if any is denied.
- `TenantConfig` gains `retention_class`, set when the tenant is created.

## Open questions

- Whether asynchronous-insert flushes honour `insert_quorum`. Unverified, and
  irrelevant while the sink inserts synchronously.
- How row policies perform with thousands of mapping rows. The benchmark
  measured 20.
- The snapshot interval for set domains needs change rates from real
  devices.
- Restore of replicated tables from a `BACKUP` onto a new replica set.
  Unverified.

## Sources

- Research: `docs/research/clickhouse-history-store/` (dossiers 00 to 11 and
  the benchmark).
- Repository: `spec/proto/flowseer/integration/ingest/v1/ingest_record.proto`,
  `spec/proto/flowseer/net/interface/v1/interface.proto`,
  `src/modules/edgebus/hub.go`, `docs/runbooks/production-clickhouse-queries.md`.
- ClickHouse multi-tenancy guide:
  <https://clickhouse.com/docs/cloud/bestpractices/multi-tenancy>.
- Insert deduplication and asynchronous inserts:
  <https://clickhouse.com/docs/reference/settings/session-settings/async-insert>.
- Row policies: <https://clickhouse.com/docs/sql-reference/statements/create/row-policy>.
