---
title: Prior art for network and observability history in ClickHouse
date: 2026-10-08
status: research; sources fetched 2026-10-08, some DDL reconstructed from excerpts as marked
---

# Prior art: network monitoring and observability history in ClickHouse

Fetched 2026-10-08. Read-only toward the FlowSeer repository. Each claim cites the URL it came from; anything not confirmed is marked "unverified" and collected in section (d).

## Scope and method

30 systems examined. 23 store data in ClickHouse and are compared; 7 were checked and dropped because they do not (pmacct, ElastiFlow, Kentik, Netdata, LibreNMS, Grafana as a storage product, chproxy). Kentik and Netdata are kept as non-ClickHouse analogues only.

Source access: sources were read through WebFetch, which refused verbatim quotes longer than about 125 characters. DDL was therefore pulled in short chunks or per-clause extractions and joined. Every DDL block below carries its file path and commit or tag, and is labelled "reconstructed" or "paraphrase" where it is not byte-exact. Clause strings (ORDER BY, PARTITION BY, TTL, engine) were extracted as exact quotes in every case; column type lists in reconstructed blocks may differ in whitespace, order or codec detail. Before relying on a reconstructed block for a design decision, re-read the raw file at the cited ref.

The legacy FlowSeer store is not used as a baseline anywhere in this report. Prior-art patterns are judged on their own merits for FlowSeer's stated shape: typed protobuf observations from NATS JetStream (at least once), ClickHouse 1 shard x 2 replicas, multi-tenant, domains such as interfaces, radios, wireless clients, FDB, LLDP/CDP, system resources, syslog, traps and alarms.

Contents:
1. Network-specific systems (Akvorado, GoFlow2, ntopng, Zabbix 8.0, FastNetMon, Telegraf/clickstack, yt-snmp-go-poller, eait-itig flow-collector; dropped: pmacct, ElastiFlow, Kentik, Netdata)
2. OpenTelemetry exporter, ClickStack/HyperDX, Uptrace, Coroot, qryn/gigapipe (dropped: Grafana)
3. SigNoz, ClickHouse TimeSeries engine, PromHouse, prom2click (dropped: chproxy, Netdata)
4. Multi-tenant production users: Sentry Snuba, PostHog, Contentsquare, Cloudflare, Uber, Glaber (dropped: LibreNMS)
5. (a) Comparison table, (b) patterns and anti-patterns, (c) implications for FlowSeer, (d) unverified list

## Network-specific systems

DDL fidelity: WebFetch returned at most about 125 characters of quoted source at a time, so DDL here was requested in chunks of 100 characters or less and joined. Tokens are as returned; line breaks and indentation may differ. Text marked "paraphrase" was summarised by the tool, not quoted.

| System | Uses ClickHouse? | What is stored |
|---|---|---|
| Akvorado | Yes, sole store | Flows plus rollups, an exporters/interfaces table, dictionaries |
| GoFlow2 | Example only (`compose/kcg`) | Flows via Kafka engine plus a 5-minute rollup |
| pmacct | No native plugin found | Has a Kafka plugin; a Kafka-to-ClickHouse path is unverified |
| ElastiFlow | No ClickHouse output documented | |
| ntopng | Yes (Enterprise M and up) | Flows, alerts, assets, a generic timeseries table (interface/host counters) |
| Kentik | No, own engine (KDE) | |
| Netdata | No | |
| Zabbix | Yes, history backend (8.0 docs, "In development") | Item history incl. SNMP interface counters |
| FastNetMon | Yes | Per-host, per-network, per-interface traffic counters |
| Telegraf `outputs.sql` / IEISI-ORG/clickstack | Yes | SNMP ifTable counters, one table per metric |
| logingood/yt-snmp-go-poller | Yes | One wide table of interface counters plus inventory |
| eait-itig/flow-collector | Yes | SPAN-port flows with rollups |

LLDP/CDP, FDB/MAC and Wi-Fi client sessions: no public project storing any of these in ClickHouse was found (three web searches, not exhaustive). The only neighbour data found is a `neighbour VARCHAR(255)` column inline with counters in yt-snmp-go-poller.

### Akvorado

Uses ClickHouse as its only store. Ref: akvorado/akvorado `208139830c565f75dd52e8cce61532f3c4111806` (main, 2026-10-07).

Tables (per domain): raw `flows`; rollups `flows_1m0s`, `flows_5m0s`, `flows_1h0m0s` (one per configured resolution); `exporters` (last-seen exporter and interface metadata, filled by an MV from `flows`); `flows_<schemahash>_raw` (Null engine plus consumer view); HTTP-sourced dictionaries `asns`, `protocols`, `icmp`, `tcp`, `udp` and custom ones. Source: `orchestrator/clickhouse/migrations.go`, `migrations_helpers.go`, https://github.com/akvorado/akvorado/blob/208139830c565f75dd52e8cce61532f3c4111806/orchestrator/clickhouse/migrations_helpers.go

DDL is built in Go through `common/sqlbuilder`; the rendered state is captured in test fixtures under `orchestrator/clickhouse/testdata/states/`.

`orchestrator/clickhouse/testdata/states/013.csv` @208139830c, `flows` engine tail:

```sql
ENGINE = MergeTree PARTITION BY toYYYYMMDDhhmmss(toStartOfInterval(TimeReceived, toIntervalSecond(25920)))
PRIMARY KEY toStartOfFiveMinutes(TimeReceived)
ORDER BY (toStartOfFiveMinutes(TimeReceived), ExporterAddress, InIfName, OutIfName)
TTL TimeReceived + toIntervalSecond(1296000) SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1
```

Same file, `flows_1m0s`:

```sql
ENGINE = SummingMergeTree((Bytes, Packets)) PARTITION BY toYYYYMMDDhhmmss(toStartOfInterval(
TimeReceived, toIntervalSecond(12096))) PRIMARY KEY (TimeReceived, ExporterAddress, EType, Proto,
InIfName, SrcAS, ForwardingStatus, OutIfName, DstAS, SamplingRate) ORDER BY (TimeReceived,
ExporterAddress, EType, Proto, InIfName, SrcAS, ForwardingStatus, OutIfName, DstAS, SamplingRate,
SrcNetName, DstNetName, SrcNetRole, DstNetRole, SrcNetSite, DstNetSite, SrcNetRegion,
DstNetRegion, SrcNetTenant, DstNetTenant, SrcCountry, DstCountry, SrcGeoCity, DstGeoCity,
SrcGeoState, DstGeoState, Dst1stAS, Dst2ndAS, Dst3rdAS, FlowDirection) TTL TimeReceived +
toIntervalSecond(604800) SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1
```

Same file, `exporters`:

```sql
CREATE TABLE default.exporters (`TimeReceived` DateTime, `ExporterAddress` LowCardinality(IPv6),
`ExporterName` LowCardinality(String), `ExporterGroup` LowCardinality(String),
`ExporterRole` LowCardinality(String), `ExporterSite` LowCardinality(String), `ExporterRegion`
LowCardinality(String), `ExporterTenant` LowCardinality(String), `IfName` LowCardinality(String),
`IfDescription` LowCardinality(String), `IfSpeed` UInt32, `IfConnectivity` LowCardinality(String),
`IfProvider` LowCardinality(String), `IfBoundary`
Enum8('undefined' = 0, 'external' = 1, 'internal' = 2)) ENGINE =
ReplacingMergeTree(TimeReceived)
ORDER BY (ExporterAddress, IfName) TTL TimeReceived + toIntervalDay(1)
SETTINGS index_granularity = 8192
```

Same file, dictionary:

```sql
CREATE DICTIONARY default.asns (`asn` UInt32 INJECTIVE, `name` String) PRIMARY KEY asn
SOURCE(HTTP(URL 'http://127.0.0.1:0/api/v0/orchestrator/clickhouse/asns.csv' FORMAT 'CSVWithNames'))
 LIFETIME(MIN 0 MAX 3600) LAYOUT(HASHED()) SETTINGS(format_csv_allow_single_quotes = 0)
```

`testdata/states/002-cluster.csv` @208139830c (cluster mode):

```sql
ENGINE = ReplicatedMergeTree('/clickhouse/tables/shard-{shard}/{database}/flows_local',
 'replica-{replica}') PARTITION BY toYYYYMMDDhhmmss(toStartOfInterval(TimeReceived,
toIntervalSecond(25920))) ORDER BY (toStartOfFiveMinutes(TimeReceived), ExporterAddress,
 InIfName, OutIfName) TTL TimeReceived + toIntervalSecond(1296000)
 SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1
-- distributed front table:
CREATE TABLE default.flows ... ENGINE = Distributed('akvorado', 'default', 'flows_local', rand())
```

Historical Kafka engine, `testdata/states/001.csv` @208139830c (removed in 2.0.0):

```sql
ENGINE = Kafka SETTINGS kafka_broker_list = '127.0.0.1:9092', kafka_topic_list = 'flows-v1',
kafka_group_name = 'clickhouse', kafka_format = 'Protobuf',
kafka_schema = 'flow-1.proto:FlowMessage', kafka_num_consumers = 1,
kafka_thread_per_consumer = 1
```

Keys and partitions: raw sort key is a 5-minute bucket then exporter and interfaces; rollups sort by every dimension column; the partition interval is TTL divided by `max-partitions` (`createOrUpdateFlowsTable`). Release 1.4.2: "adapt partition key for each consolidated flow table in ClickHouse to limit the number of partitions" (changelog https://github.com/akvorado/akvorado/blob/208139830c565f75dd52e8cce61532f3c4111806/console/data/docs/99-changelog.md).

Rollups and retention (`console/data/docs/50-configuration.md`): defaults raw 360h, 1m 168h, 5m 2160h, 1h 8760h. Each rollup is a SummingMergeTree fed by MV `flows_<interval>_consumer` applying `toStartOfInterval(TimeReceived, toIntervalSecond(N))` to the local `flows` table. TTLs use `ttl_only_drop_parts = 1`. Release 2.2.0: orchestrator does "not materialize TTLs in ClickHouse when updating them"; `13-operating.md` tells users to run `ALTER TABLE flows MATERIALIZE TTL` manually. Removing a resolution does not drop its table.

High cardinality: columns flagged `ClickHouseMainOnly` (addresses, ports, AS path, communities, NAT, MPLS labels) exist only in the raw table (`common/schema/definition.go`). Labels use `LowCardinality(String)`/`LowCardinality(IPv6)`. Skip indexes configurable since 2.3.0, which also says "do not index `ExporterAddress`, `InIfName`, and `OutIfName`". Enrichment moved out of ClickHouse: in 2026.8.0 the outlet took over GeoIP and network attributes and the `networks` dictionary was dropped "because it still holds a copy of the GeoIP databases in ClickHouse memory"; `13-operating.md` had warned that it "can use a lot of memory".

Tenancy: none; tenant only as attribute columns (`ExporterTenant`, `SrcNetTenant`, `DstNetTenant`). Release 2026.8.1 allows several Akvorado databases on one cluster.

Dedup: `flows` plain MergeTree; rollups sum. `exporters` is `ReplacingMergeTree(TimeReceived)` ORDER BY `(ExporterAddress, IfName)` with a 1-day TTL. Whether outlet retries can duplicate rows: unverified.

Migrations (Go, replicated): no migration framework. `81-internals.md`: "the table schemas depend on the user configuration, it is preferred to use code to check if the existing tables are up-to-date and to update them." `migrateDatabase` runs ordered steps; each compares `system.tables.create_table_query` and `system.columns` against the wanted state (SQL compared with clickhouse-sql-parser) and returns `errSkipStep` when nothing changes. Missing columns, type and codec changes, skip indexes are reconciled in place. Cluster mode: counts shards via `countDistinct(shard_num)` from `system.clusters`, sets `alter_sync=2`, runs all DDL through `ExecOnCluster`, uses `Replicated*MergeTree` with the ZooKeeper path reused from the existing table or defaulting to `/clickhouse/tables/shard-{shard}/<db>/<table>`; `_local` plus `Distributed(..., rand())` only with more than one shard. The raw ingest table name carries a schema hash, so a schema change creates a new ingest table.

Regrets and lessons (`99-changelog.md`, `13-operating.md`, `50-configuration.md`, `81-internals.md` at the ref above):
- Kafka engine dropped in 2.0.0: "Previously, ClickHouse was fetching data directly from Kafka." The protobuf schema had to be pushed to ClickHouse out of band, complicating cloud setups. The outlet now decodes and inserts through ch-go, building native-format batches.
- Cluster mode cannot be enabled later: "Do not try to enable cluster mode on an existing setup!" (1.10.0); "No migration is done between the cluster and the non-cluster modes".
- 1.10.0 changed the primary key to `toStartOfFiveMinutes(TimeReceived)` for compression; not migrated automatically; the manual partition-by-partition copy warns "There is a risk of data loss".
- 1.5.0, 1.6.4, 1.7.0 required ClickHouse restarts after schema changes.
- Obsolete `flows_raw_errors` and `flows_XXXX_raw` tables are left behind.
- 2026.8.0: "pick the top rows with `topKWeighted` on the unconsolidated table to use less memory".

### GoFlow2 ClickHouse example

Example only. Ref netsampler/goflow2 `7c921519b6a167a3a5e7d512b8dd2ff4d41a1de0`, `compose/kcg/clickhouse/create.sh`. Kafka engine `flows`, MV `flows_raw_view`, `flows_raw` (MergeTree, `PARTITION BY date`, `ORDER BY time_received_ns`), MV `flows_5m_view`, `flows_5m`; a `dictionaries.protocols` FLAT dictionary from CSV. No TTL, tenancy, dedup or migrations (only `IF NOT EXISTS`).

```sql
CREATE TABLE IF NOT EXISTS flows_5m
    (
        date Date,
        timeslot DateTime,
        src_as UInt32,
        dst_as UInt32,
        etypeMap Nested (
            etype UInt32,
            bytes UInt64,
            packets UInt64,
            count UInt64
        ),
        bytes UInt64,
        packets UInt64,
        count UInt64
    ) ENGINE = SummingMergeTree()
    PARTITION BY date
    ORDER BY (date, timeslot, src_as, dst_as, `etypeMap.etype`);
```

### pmacct, ElastiFlow, Kentik

- pmacct: `CONFIG-KEYS` (master as fetched) lists memory, print, mysql, pgsql, sqlite3, nfprobe, sfprobe, tee, amqp, kafka; no "clickhouse" in the first 100k characters (remaining ~112k not read) nor in the README (https://raw.githubusercontent.com/pmacct/pmacct/master/CONFIG-KEYS, https://github.com/pmacct/pmacct). Dropped.
- ElastiFlow: no ClickHouse in https://docs.elastiflow.com/sitemap.md or https://docs.elastiflow.com/flowcoll/configuration/outputs.md. Dropped.
- Kentik: not ClickHouse, own columnar store KDE. "KDE maintains separate databases for each customer's flow records"; "Main Tables" per device; a "Full" and a subsampled "Fast" dataseries for queries over 24h or more (https://kb.kentik.com/Eb01.htm, https://www.kentik.com/blog/inside-the-kentik-data-engine-part-1/). Non-ClickHouse analogue only.

### ntopng

Uses ClickHouse. Ref ntop/ntopng `e132b5b7cb474092e428c85395ad5ea731ffd40b` (dev, 2026-10-08).

Flows and alerts (`httpdocs/misc/db_schema_clickhouse.sql`, plus `_cluster.sql`):
- `flows`: about 99 columns, including `NTOPNG_INSTANCE_NAME String` and `INTERFACE_ID UInt16`; `ENGINE = MergeTree() PARTITION BY toYYYYMMDD(FIRST_SEEN) ORDER BY (FIRST_SEEN, IPV4_SRC_ADDR, IPV4_DST_ADDR);`
- Alerts: one table per entity (`host_alerts`, `mac_alerts`, `snmp_alerts`, `interface_alerts`, `network_alerts`, `as_alerts`, `system_alerts`, `user_alerts`, `active_monitoring_alerts`), each `ENGINE = MergeTree() PARTITION BY toYYYYMMDD(tstamp) ORDER BY (tstamp);`, each with an `engaged_*` twin on `ENGINE = Memory`. Views are dropped and recreated on every run.
- `hourly_flows`, `hourly_asn` rollup-like tables.
- ReplacingMergeTree for state/dimension tables: `assets` is `ReplacingMergeTree(version) ... ORDER BY (\`type\`, \`key\`)`; also `l7_protocols`, `mitre_table_info`.
- Migrations: one idempotent SQL file re-run in full (`CREATE TABLE IF NOT EXISTS`, `ADD COLUMN IF NOT EXISTS`, `DROP COLUMN IF EXISTS`, `MODIFY COLUMN ... COMMENT`). Cluster variant uses `ON CLUSTER '$CLUSTER'` and `ReplicatedMergeTree('/clickhouse/{cluster}/tables/{database}/{table}', '{replica}')`.
- Tenancy: inline `ntopng_instance_name` column.

Timeseries incl. interface counters (`scripts/lua/modules/timeseries/drivers/clickhousets.lua` @e132b5b), paraphrase:

```sql
CREATE TABLE IF NOT EXISTS `%s`.`%s`
(
    `schema_name`  LowCardinality(String),
    `ifid`         Int16,
    `ntopng_instance_name` LowCardinality(String),
    `tstamp`       DateTime CODEC(Delta, ZSTD),
    `tags`         Map(LowCardinality(String), String),
    `metrics`      Map(LowCardinality(String), Float64)
)
ENGINE = MergeTree()
PARTITION BY toYYYYMMDD(tstamp)
ORDER BY (schema_name, ifid, tstamp)
-- migrateSchema():
ALTER TABLE `%s`.`%s` ADD COLUMN IF NOT EXISTS `ifid` Int16
ALTER TABLE `%s`.`%s` UPDATE `ifid` = toInt16OrZero(tags['ifid']) WHERE 1
ALTER TABLE `%s`.`%s` ADD COLUMN IF NOT EXISTS `ntopng_instance_name` LowCardinality(String)
```

- One generic table for every signal, no series table, labels in a `Map`, `ifid` later promoted from the map to a real column by a mutation over the whole table.
- No TTL; `deleteOldData()` drops daily partitions; the code comment says this avoids trouble when retention changes after the table exists.
- Docs (https://ntop.org/guides/ntopng/_sources/advanced_features/clickhouse_timeseries.rst.txt) show monthly `toYYYYMM` partitions, `ORDER BY (schema_name, tstamp)` and a TTL (365 days); the code at the ref above differs.
- C++ queue batches inserts via clickhouse-cpp, default batch 10000.
- Counter rates computed at query time: `argMax` per bucket, `lag()`, `greatest(0, delta)/step`, first bucket NULL.
- Caltech user report: moving to ClickHouse on SSD "solved our dropped data points problem" (https://www.ntop.org/ntopng-and-clickhouse-lessons-learnt-at-california-institute-of-technology/).

### Zabbix ClickHouse history backend

zabbix/zabbix `database/clickhouse/`, latest commit on the directory `c219427fd61825a50f0679b3882233d265fb63be` (2026-08-06, "added ability to select ReplicatedMergeTree"); `history_uint_schema.sh` fetched from master 2026-10-08:

```sql
CREATE TABLE $CH_DB.history_uint
(
	itemid UInt64,
	clock_ns DateTime64(9),
	value UInt64
)
ENGINE = $CH_ENGINE
PARTITION BY $CH_PARTITION(clock_ns)
PRIMARY KEY (itemid, clock_ns)
TTL clock_ns + toIntervalSecond($CH_TTL)
```

- Defaults (`clickhouse.sh`): `CH_ENGINE="MergeTree()"`, `CH_PARTITION="toDate"`, `CH_TTL="2678400"` (31 days), `CH_DB=zabbix`.
- One table per value type (`history`, `history_uint`, `history_str`, `history_log`, `history_text`, `history_json`).
- Series identity is the integer `itemid`; host, interface and labels live in Zabbix's relational DB.
- Housekeeper does not delete ClickHouse data; retention is TTL only; trends are not stored in ClickHouse.
- Cluster: `ReplicatedMergeTree()` plus `*_local` with `Distributed('cluster_2S_2R','zabbix','history_local', itemid)` (https://www.zabbix.com/documentation/8.0/en/manual/appendix/install/clickhouse_setup).
- README: "The housekeeping interval (TTL) and partition schema are loosely linked", suggesting `toYYYYMM` for longer TTLs.

### FastNetMon

https://fastnetmon.com/docs-fnm-advanced/traffic-metrics-in-clickhouse/ : separate wide tables per entity (`total_metrics`, `network_metrics`, `host_metrics[_ipv6]`, `interface_metrics`, `asn_metrics_*`), all `PARTITION BY metricDate`, `TTL metricDate + toIntervalDay(7)`, a `schema_version` column. `interface_metrics` (paraphrase) `ORDER BY (device_ip, interface_id, metricDate)`.

```sql
CREATE TABLE fastnetmon.network_metrics
(
    `metricDate` Date DEFAULT toDate(metricDateTime),
    `metricDateTime` DateTime,
    `network` String,
    `packets_incoming` UInt64,
    `packets_outgoing` UInt64,
    `bits_incoming` UInt64,
    `bits_outgoing` UInt64,
    ...  -- per-protocol packets/bits in/out
    `schema_version` UInt8 DEFAULT 0 COMMENT '1'
)
ENGINE = MergeTree
PARTITION BY metricDate
ORDER BY (network, metricDate)
TTL metricDate + toIntervalDay(7)
SETTINGS index_granularity = 8192
```

### Telegraf `outputs.sql` and IEISI-ORG/clickstack

- `plugins/outputs/sql/README.md`: "There is a table for each metric type with the table name corresponding to the metric name"; tags and fields become columns; new tags or fields fail unless `table_update_template` (`ALTER TABLE {TABLE} ADD COLUMN {COLUMN}`) is set; documented ClickHouse default `CREATE TABLE {TABLE}({COLUMNS}) ORDER BY ({TAG_COLUMN_NAMES}, {TIMESTAMP_COLUMN_NAME})`.
- clickstack (https://github.com/IEISI-ORG/clickstack, `docker/telegraf/telegraf.conf`) overrides with `ENGINE = MergeTree() ORDER BY time`, commenting that Telegraf 1.32 leaves the template variables unexpanded (claim from that comment only). Its `snmp` table has `time`, `source`, `ifName`, `ifIndex`, `ifHCInOctets`, `ifHCOutOctets`, `ifInErrors`, `ifOutErrors`, `ifOperStatus`, `ifAlias`, no partition, TTL or rollup. Its SCHEMA.md recommends window-function deltas over `max-min` per bucket because 60-second polls leave single-sample buckets at zero. It sits beside Akvorado's tables so flows and SNMP can be joined.

### logingood/yt-snmp-go-poller

`e36959793623` (2023-09-16), `storer/interfaces/iface_chouse/clickhouse.go`: one table mixing inventory (`sys_name`, `serial`, `location`, `neighbour VARCHAR(255)`, `if_alias`, `mac_address`) with counters, `ENGINE = MergeTree ORDER BY tuple()`, no partition, no TTL. A counter-example.

### eait-itig/flow-collector

`setup.sql` (main as fetched, paraphrase): `flows` SummingMergeTree, `PARTITION BY toStartOfDay(begin_at)`, `TTL toDateTime(end_at) + toIntervalHour(4)`; cascaded MVs `flows_5sec`, `flows_1min`, `flows_5min`, `flows_1hr` with TTLs 14, 180, 540 days and none; bloom-filter skip indexes on `daddr` and `(ipproto, daddr, dport)`.
## OpenTelemetry exporter, ClickStack/HyperDX, Uptrace, Coroot, qryn/gigapipe, Grafana

DDL fidelity: WebFetch refused verbatim quotes over 125 characters, so blocks marked "reconstructed" were rebuilt from per-column and per-clause extractions. Names, types, codecs and clauses are as WebFetch reported; whitespace and some column order may differ.

Refs: OTel contrib tag `v0.162.0` (latest, 29 Sep); HyperDX tag `@hyperdx/otel-collector@2.40.0` (01 Oct); Uptrace tag `v2.0.3` (2026-04-30; `v2.1.0-rc.1` exists, not read); Coroot tag `v1.27.1` (2026-10-06); gigapipe tag `v5.5.3` (2026-09-30); grafana/clickhouse-datasource main as fetched 2026-10-08.

### OpenTelemetry Collector ClickHouse exporter

Uses ClickHouse: yes; README: "The default DDL used by the exporter can be found in `internal/sqltemplates`" (https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/v0.162.0/exporter/clickhouseexporter/internal/sqltemplates). Stability: traces and logs beta, metrics alpha, profiles in development.

Tables: per signal and per metric point type: `otel_logs`, `otel_traces` (+ `otel_traces_trace_id_ts` lookup and MV), `otel_metrics_{gauge,sum,histogram,exp_histogram,summary}`, `otel_profiles`. Attributes inline as `Map(LowCardinality(String), String)`; no series table.

`logs_table.sql` @ v0.162.0 (Go `text/template`), reconstructed:

```sql
CREATE TABLE IF NOT EXISTS {{ident .Database}}.{{ident .TableName}} {{.ClusterString}} (
  Timestamp DateTime64(9) CODEC(Delta(8), ZSTD(1)),
  TraceId String CODEC(ZSTD(1)),
  SpanId String CODEC(ZSTD(1)),
  TraceFlags UInt8,
  SeverityText LowCardinality(String) CODEC(ZSTD(1)),
  SeverityNumber UInt8,
  ServiceName LowCardinality(String) CODEC(ZSTD(1)),
  Body String CODEC(ZSTD(1)),
  ResourceSchemaUrl LowCardinality(String) CODEC(ZSTD(1)),
  ResourceAttributes Map(LowCardinality(String), String) CODEC(ZSTD(1)),
  ScopeSchemaUrl LowCardinality(String) CODEC(ZSTD(1)),
  ScopeName String CODEC(ZSTD(1)),
  ScopeVersion LowCardinality(String) CODEC(ZSTD(1)),
  ScopeAttributes Map(LowCardinality(String), String) CODEC(ZSTD(1)),
  LogAttributes Map(LowCardinality(String), String) CODEC(ZSTD(1)),
  EventName String CODEC(ZSTD(1)),
  `__otel_materialized_k8s.cluster.name` LowCardinality(String) MATERIALIZED ResourceAttributes['k8s.cluster.name'] CODEC(ZSTD(1)),
  -- likewise: k8s.container.name, k8s.deployment.name, k8s.namespace.name, k8s.node.name,
  --           k8s.pod.name, k8s.pod.uid, deployment.environment.name
{{- if .HasFullTextSearch}}
  INDEX idx_trace_id TraceId TYPE text(tokenizer = 'array'),
  INDEX idx_res_attr_key mapKeys(ResourceAttributes) TYPE text(tokenizer = 'array'),
  INDEX idx_res_attr_value mapValues(ResourceAttributes) TYPE text(tokenizer = 'array'),
  INDEX idx_lower_body lower(Body) TYPE text(tokenizer = 'splitByNonAlpha')
{{- else}}
  INDEX idx_trace_id TraceId TYPE bloom_filter(0.001) GRANULARITY 1,
  INDEX idx_res_attr_key mapKeys(ResourceAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
  INDEX idx_res_attr_value mapValues(ResourceAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
  INDEX idx_lower_body lower(Body) TYPE tokenbf_v1(32768, 3, 0) GRANULARITY 8
{{- end}}
) ENGINE = {{.Engine}}
PARTITION BY toDate(Timestamp)
ORDER BY (toStartOfFiveMinutes(Timestamp), ServiceName, Timestamp)
{{.TTL}}
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1
```

Recent change: v0.140.0 had `TimestampTime DateTime DEFAULT toDateTime(Timestamp)`, `PRIMARY KEY (ServiceName, TimestampTime)`, `ORDER BY (ServiceName, TimestampTime, Timestamp)`. PR #47720 (merged 2026-04-23, https://github.com/open-telemetry/opentelemetry-collector-contrib/pull/47720) dropped `TimestampTime`, added the materialized columns, moved to the five-minute-bucket ORDER BY, added text indexes on ClickHouse 26.2+, to "optimize time-bucketed range scans for Grafana/HyperDX workloads". "Existing tables are not modified"; the changes "only apply to new deployments". A commenter asked why the ORDER BY had "changed twice recently" and how to migrate; no answer shown.

`traces_table.sql` @ v0.162.0, reconstructed (key clauses): Map attributes with bloom indexes, `Events Nested(...)`, `Links Nested(...)`, `PARTITION BY toDate(Timestamp)`, `ORDER BY (ServiceName, SpanName, toDateTime(Timestamp))`, `SETTINGS index_granularity=8192, ttl_only_drop_parts = 1`. Lookup table `%s_trace_id_ts` (`TraceId`, `Start`, `End`), `ORDER BY (TraceId, Start)`, filled by `SELECT TraceId, min(Timestamp) as Start, max(Timestamp) as End ... GROUP BY TraceId`.

`metrics_sum_table.sql` @ v0.162.0, reconstructed:

```sql
CREATE TABLE IF NOT EXISTS %s.%s %s (
  ResourceAttributes Map(LowCardinality(String), String) CODEC(ZSTD(1)),
  ResourceSchemaUrl String CODEC(ZSTD(1)),
  ScopeName String CODEC(ZSTD(1)),
  ScopeVersion String CODEC(ZSTD(1)),
  ScopeAttributes Map(LowCardinality(String), String) CODEC(ZSTD(1)),
  ScopeDroppedAttrCount UInt32 CODEC(ZSTD(1)),
  ScopeSchemaUrl String CODEC(ZSTD(1)),
  ServiceName LowCardinality(String) CODEC(ZSTD(1)),
  MetricName LowCardinality(String) CODEC(ZSTD(1)),
  MetricDescription String CODEC(ZSTD(1)),
  MetricUnit String CODEC(ZSTD(1)),
  Attributes Map(LowCardinality(String), String) CODEC(ZSTD(1)),
  StartTimeUnix DateTime CODEC(Delta, ZSTD(1)),
  TimeUnix DateTime CODEC(Delta, ZSTD(1)),
  Value Float64 CODEC(ZSTD(1)),
  Flags UInt32 CODEC(ZSTD(1)),
  Exemplars Nested (FilteredAttributes Map(LowCardinality(String), String), TimeUnix DateTime, Value Float64, SpanId String, TraceId String) CODEC(ZSTD(1)),
  AggregationTemporality Int32 CODEC(ZSTD(1)),
  IsMonotonic Boolean CODEC(Delta, ZSTD(1)),
  INDEX idx_res_attr_key mapKeys(ResourceAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
  INDEX idx_res_attr_value mapValues(ResourceAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
  INDEX idx_scope_attr_key mapKeys(ScopeAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
  INDEX idx_scope_attr_value mapValues(ScopeAttributes) TYPE bloom_filter(0.01) GRANULARITY 1,
  INDEX idx_attr_key mapKeys(Attributes) TYPE bloom_filter(0.01) GRANULARITY 1,
  INDEX idx_attr_value mapValues(Attributes) TYPE bloom_filter(0.01) GRANULARITY 1,
  INDEX idx_time_minmax TimeUnix TYPE minmax GRANULARITY 1
) ENGINE = %s
%s
PARTITION BY toDate(TimeUnix)
ORDER BY (ServiceName, MetricName, toStartOfHour(TimeUnix), cityHash64(Attributes), TimeUnix)
SETTINGS index_granularity=8192, ttl_only_drop_parts = 1
```

Gauge omits `AggregationTemporality`/`IsMonotonic`; histogram, exp_histogram and summary share PARTITION BY/ORDER BY and add their type's fields. A JSON variant (`logs_json_table.sql`, `json: true`) uses bare `JSON` attribute columns plus `*Keys Array(LowCardinality(String))` companion columns with bloom indexes.

Table creation (README, `config.go` @ v0.162.0): `create_schema` defaults to true; the README recommends `false` in production so collectors do not race to create tables; with it off the exporter only INSERTs and upgrades need manual `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`. `cluster_name` renders `ON CLUSTER`; `table_engine.name`/`params` default `MergeTree()` (set `ReplicatedMergeTree` for replication). `ttl` duration renders `TTL %s + toIntervalDay(%d)` (or Hour/Minute/Second). No migration or ALTER logic.

Tenancy none; dedup none (plain MergeTree); series identity is `cityHash64(Attributes)` inside the sort key; rollups none.

Issues and lessons:
- #33634: equal attribute sets in a different map key order became distinct rows; reorderings "split GROUP BY results" and hurt compression; "Maps are unpredictable in Go, so we would need to convert it to a slice and sort it"; fixed by PR #35725 "Sort attribute maps before insertion" (Dec 2024). https://github.com/open-telemetry/opentelemetry-collector-contrib/issues/33634
- #24675: `SELECT distinct(Attributes['system'])` over about 300M rows full-scanned; maintainers suggested time filters, PREWHERE, MVs or materialized columns; closed not planned. https://github.com/open-telemetry/opentelemetry-collector-contrib/issues/24675
- #38485: "Every table in the exporter can be manually created"; suggested path is a `Null`-engine table matching the exporter's columns plus an MV reshaping data. https://github.com/open-telemetry/opentelemetry-collector-contrib/issues/38485
- #35917: "The Time Series engine is an experimental way of storing metrics". https://github.com/open-telemetry/opentelemetry-collector-contrib/issues/35917
- #35713: on a 3-node cluster with `create_schema: true`, startup failed with "Database otel does not exist" and tables ended up non-clustered; advice was manual creation with `create_schema: false`; closed stale. https://github.com/open-telemetry/opentelemetry-collector-contrib/issues/35713
- #48770 (open): removing `TimestampTime` broke the Grafana datasource's default log query. https://github.com/open-telemetry/opentelemetry-collector-contrib/issues/48770

### ClickStack / HyperDX

Uses ClickHouse: "The ClickStack OpenTelemetry (OTel) collector uses the ClickHouse exporter to create tables in ClickHouse and insert data." (https://clickhouse.com/docs/clickstack/ingesting-data/schemas.md). At tag 2.40.0 the collector image creates tables from its own goose seed files (https://github.com/hyperdxio/hyperdx/tree/@hyperdx/otel-collector@2.40.0/docker/otel-collector/schema/seed): `00001_create_database.sql` ... `00008_otel_metrics_timeseries.sql`.

`00002_otel_logs.sql` @ 2.40.0, reconstructed (tail):

```sql
  ResourceAttributeItems Array(String) ALIAS arrayMap((arr) -> concat(arr.1, '=', arr.2), ResourceAttributes::Array(Tuple(String, String))),
  INDEX idx_trace_id TraceId TYPE text(tokenizer = 'array'),
  INDEX idx_res_attr_key mapKeys(ResourceAttributes) TYPE text(tokenizer = 'array'),
  INDEX idx_res_attr_items ResourceAttributeItems TYPE text(tokenizer = 'array'),
  INDEX idx_lower_body lower(Body) TYPE text(tokenizer = 'splitByNonAlpha')
) ENGINE = MergeTree
PARTITION BY toDate(Timestamp)
ORDER BY (toStartOfFiveMinutes(Timestamp), ServiceName, Timestamp)
TTL toDateTime(Timestamp) + ${LOGS_TTL}
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1, enable_block_number_column = 1, enable_block_offset_column = 1
```

(columns otherwise as the exporter's `otel_logs`, with `__hdx_materialized_*` hot-key columns.)

Metrics (`00003_otel_metrics.sql`): exporter columns, `ORDER BY (ServiceName, MetricName, toStartOfHour(TimeUnix), cityHash64(Attributes), TimeUnix)`, `TTL toDateTime(TimeUnix) + ${METRICS_TTL}`. The docs page lists a different metrics ORDER BY (`ServiceName, MetricName, Attributes, toUnixTimestamp64Nano(TimeUnix)`).

Rollups (`00006_otel_logs_rollups.sql`), reconstructed:

```sql
CREATE TABLE IF NOT EXISTS ${DATABASE}.otel_logs_kv_rollup_15m (
  Timestamp DateTime,
  ColumnIdentifier LowCardinality(String),
  Key LowCardinality(String),
  Value String,
  count UInt64,
  INDEX idx_count_minmax count TYPE minmax GRANULARITY 1,
  INDEX idx_timestamp_minmax Timestamp TYPE minmax GRANULARITY 1
) ENGINE = SummingMergeTree
PARTITION BY toDate(Timestamp)
ORDER BY (ColumnIdentifier, Key, Timestamp, Value)
TTL Timestamp + ${LOGS_TTL}
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;
```

The MV counts values of 14 native/materialized columns per 15 minutes; traces get the same over six columns. `00008` creates `metrics_ts ENGINE = TimeSeries` when PromQL is enabled.

Migrations (`packages/otel-collector/cmd/migrate/main.go` @ 2.40.0): pressly/goose v3 with `goose.SetDialect("clickhouse")` and `goose.WithNoVersioning()`, so every seed file re-runs on each start and the SQL "MUST be idempotent"; `${DATABASE}`/`${*_TTL}` substituted from env (`HYPERDX_OTEL_EXPORTER_TABLES_TTL` default `720h`); compat files chosen by `SELECT version()` (26.2+ text indexes, else bloom); no ON CLUSTER or Replicated handling. Opt-in TTL reconcile reads `system.tables.create_table_query`, refuses compound TTLs, and runs `ALTER TABLE ... MODIFY TTL ... SETTINGS materialize_ttl_after_modify = %d` with 1 when extending and 0 when shrinking so startup never triggers a bulk delete.

Tenancy: none (one database). Dedup: none.

Why they changed defaults (https://clickhouse.com/blog/whats-new-in-clickstack-april-2026): "The coarser leading bucket keeps adjacent log rows physically grouped"; over 70% improvement across sample queries with insert overhead "broadly comparable"; direct-read attribute filters on materialized columns ran 1.4 to 10 times faster than map subscripts.

Map vs JSON (https://clickhouse.com/docs/clickstack/ingesting-data/schema/map-vs-json.md): Map stays default: "Adding a new attribute key doesn't change the on-disk column layout or create new column files." JSON "dynamically creates a dedicated, strongly typed subcolumn for each path it sees", fits only a "small and stable" key set, and is "not backwards compatible" with the Map schema.

### Uptrace

Uses ClickHouse: "Uptrace uses OpenTelemetry framework to collect data and ClickHouse database to store it" (https://github.com/uptrace/uptrace). Migrations under `pkg/bunapp/chmigrations` @ v2.0.3 (https://github.com/uptrace/uptrace/tree/v2.0.3/pkg/bunapp/chmigrations).

Split by domain into an index table (searchable columns) and a data table (payload blob): `spans_index`/`spans_data`, `logs_index`/`logs_data`, `events_*`; `tracing_data` is a `Merge` engine over `^(spans|events|logs)_data$`; metrics in `datapoint_minutes`/`datapoint_hours`; `service_graph_edges`.

`20211205231031_initial.up.sql` @ v2.0.3, reconstructed (key columns):

```sql
CREATE TABLE spans_index ?ON_CLUSTER (
  id UInt64 Codec(T64, ?CODEC),
  trace_id UUID Codec(?CODEC),
  project_id UInt32 Codec(DoubleDelta, ?CODEC),
  group_id UInt64 Codec(Delta, ?CODEC),
  time DateTime Codec(Delta, ?CODEC),
  duration Int64 Codec(T64, ?CODEC),
  all_keys Array(LowCardinality(String)) Codec(?CODEC),
  string_keys Array(LowCardinality(String)) Codec(?CODEC),
  string_values Array(String) Codec(?CODEC),
  ...
) ENGINE = ?(REPLICATED)MergeTree()
PARTITION BY toDate(time)
ORDER BY (project_id, system, group_id, time)
TTL toDate(time) + INTERVAL ?SPANS_TTL DELETE
SETTINGS ttl_only_drop_parts = 1, storage_policy = ?SPANS_STORAGE

--migration:split

CREATE TABLE datapoint_minutes ?ON_CLUSTER (
  project_id UInt32 Codec(DoubleDelta, ?CODEC),
  metric LowCardinality(String) Codec(?CODEC),
  time DateTime Codec(DoubleDelta, ?CODEC),
  attrs_hash UInt64 Codec(Delta, ?CODEC),
  instrument LowCardinality(String) Codec(?CODEC),
  min SimpleAggregateFunction(min, Float64) Codec(?CODEC),
  max SimpleAggregateFunction(max, Float64) Codec(?CODEC),
  sum SimpleAggregateFunction(sum, Float64) Codec(?CODEC),
  count SimpleAggregateFunction(sum, UInt64) Codec(?CODEC),
  gauge SimpleAggregateFunction(anyLast, Float64) Codec(?CODEC),
  histogram AggregateFunction(quantilesBFloat16(0.5), Float32) Codec(?CODEC),
  string_keys Array(LowCardinality(String)) Codec(?CODEC),
  string_values Array(String) Codec(?CODEC),
  annotations SimpleAggregateFunction(max, String) Codec(?CODEC)
) ENGINE = ?(REPLICATED)AggregatingMergeTree
PARTITION BY toDate(time)
ORDER BY (project_id, metric, time, attrs_hash)
TTL toDate(time) + INTERVAL ?METRICS_TTL DELETE
SETTINGS ttl_only_drop_parts = 1, storage_policy = ?METRICS_STORAGE
```

`spans_data` is ordered `(trace_id, id)` with `index_granularity = 2048`. `datapoint_hours` is identical, fed by an MV grouping `toStartOfHour(time)`.

Metrics (`pkg/metrics/datapoint_processor.go`, `datapoint.go` @ v2.0.3): `AttrsHash` is xxhash over sorted attribute keys and values; metric name is a separate key column. Labels inline as parallel sorted arrays on every row; no series table. Cumulative-to-delta in the app. Time truncated to 1 minute (15 s in Prom-compat mode) before insert, so AggregatingMergeTree merges rows with the same `(project_id, metric, time, attrs_hash)`; this is also the dedup. A full buffer drops datapoints, counted as "dropped".

Tenancy: `project_id UInt32` leads every sort key in one shared database.

Migrations: own Go migrator `chmigrate` (`migrator.go`, `migration.go` @ v2.0.3). SQL embedded with `//go:embed`, split by `--migration:split`. Version table `ch_migrations` is `CollapsingMergeTree(sign)` (`Replicated...` when replicated), apply +1, rollback -1, read with `FINAL`. Lock: table `ch_migration_locks`, locked by `ALTER TABLE ... ADD COLUMN lock Int8`, which fails if the column exists. Options `WithOnCluster`, `WithReplicated`, `WithDistributed`. Placeholders (`?REPLICATED`, `?ON_CLUSTER`, `?CODEC`, `?SPANS_TTL`, ...) filled from config (`cmd/uptrace/command/ch.go`). CLI `ch wait|init|migrate|rollback|reset|lock|unlock|create_go|create_sql|status`. Turning on replication later means `uptrace ch reset`, which deletes all data (https://preview.uptrace.dev/get/hosted/config).

Lesson: an Uptrace post (2025-10-20) says v2.0 moved to the native JSON type, 2.754 s vs 0.287 s on 50M rows (https://dev.to/uptrace/uptrace-v20-how-clickhouse-json-type-accelerates-trace-queries-by-10x-3ph5); the OSS v2.0.3 migrations read still show arrays (unverified which applies).

### Coroot

Uses ClickHouse: "Coroot uses ClickHouse to store Logs, Traces, Profiles, and optionally Metrics." (https://docs.coroot.com/configuration/clickhouse). Prometheus is the default metrics store; with both configured "Coroot will prioritize ClickHouse for metrics storage" (https://docs.coroot.com/installation/architecture/).

Schema in `ch/client.go` @ v1.27.1 (https://github.com/coroot/coroot/blob/v1.27.1/ch/client.go), reconstructed key clauses:

```sql
CREATE TABLE IF NOT EXISTS metrics @on_cluster (
  Timestamp DateTime64(3, 'UTC') CODEC(Delta, ZSTD(1)),
  MetricHash UInt64 CODEC(ZSTD(1)),
  MetricName LowCardinality(String) CODEC(ZSTD(1)),
  Labels Map(LowCardinality(String), String) CODEC(ZSTD(1)),
  Value Float64 CODEC(ZSTD(1)),
  INDEX idx_metric_name MetricName TYPE bloom_filter(0.001) GRANULARITY 1,
  INDEX idx_labels_key mapKeys(Labels) TYPE bloom_filter(0.01) GRANULARITY 1,
  INDEX idx_labels_value mapValues(Labels) TYPE bloom_filter(0.01) GRANULARITY 1
) ENGINE @merge_tree
TTL toDateTime(Timestamp) + toIntervalSecond(@ttl_metrics)
PARTITION BY toDate(Timestamp)
ORDER BY (MetricName, MetricHash, toUnixTimestamp(Timestamp))
SETTINGS index_granularity = 8192
```

`otel_logs` (`ORDER BY (ServiceName, SeverityText, toUnixTimestamp(Timestamp), TraceId)`), `otel_traces`, `otel_traces_histogram` (SummingMergeTree latency buckets via MV), name catalogues as ReplacingMergeTree fed by `max(...) AS LastSeen` MVs, `profiling_stacks` (ReplacingMergeTree by hash) and `profiling_samples` (by `StackHash`), `metrics_metadata`.

Cluster: engines become `ReplicatedMergeTree('/clickhouse/tables/{shard}/{database}/{table}', '{replica}')`; `<name>_distributed ... ON CLUSTER` with keys `cityHash64(TraceId)`, `rand()` (logs), `MetricHash`, `StackHash`.

Tenancy: "Coroot automatically creates a dedicated database for each project." (docs above). Migrations: no version table; idempotent `CREATE ... IF NOT EXISTS` and `ALTER ... ADD COLUMN/INDEX IF NOT EXISTS` per project per process start. TTL changes on existing tables need manual `ALTER TABLE ... MODIFY TTL`. `clickhouse/space_manager.go` drops the oldest partition of `otel_*`/`profiling_*` tables when disk usage passes a threshold.

### qryn / gigapipe

Uses ClickHouse (also DuckDB/GigAPI); "formerly known as qryn" (https://github.com/metrico/gigapipe). `ctrl/qryn/sql/log.sql` @ v5.5.3, reconstructed:

```sql
CREATE TABLE IF NOT EXISTS {{.DB}}.time_series {{.OnCluster}} (
  date Date, fingerprint UInt64, labels String, name String
) ENGINE = {{.ReplacingMergeTree}}(date) PARTITION BY date ORDER BY fingerprint {{.CREATE_SETTINGS}};

CREATE TABLE IF NOT EXISTS {{.DB}}.samples_v3 {{.OnCluster}} (
  fingerprint UInt64, timestamp_ns Int64 CODEC(DoubleDelta), value Float64 CODEC(Gorilla), string String
) ENGINE = {{.MergeTree}}
PARTITION BY toStartOfDay(toDateTime(timestamp_ns / 1000000000))
ORDER BY ({{.SAMPLES_ORDER_RUL}}) {{.CREATE_SETTINGS}};

CREATE TABLE IF NOT EXISTS {{.DB}}.time_series_gin {{.OnCluster}} (
  date Date, key String, val String, fingerprint UInt64
) ENGINE = {{.ReplacingMergeTree}}() PARTITION BY date ORDER BY (key, val, fingerprint) {{.CREATE_SETTINGS}};

CREATE MATERIALIZED VIEW IF NOT EXISTS {{.DB}}.time_series_gin_view {{.OnCluster}} TO time_series_gin AS
  SELECT date, pairs.1 AS key, pairs.2 AS val, fingerprint
  FROM time_series ARRAY JOIN JSONExtractKeysAndValues(labels, 'String') AS pairs;

CREATE TABLE IF NOT EXISTS {{.DB}}.metrics_15s {{.OnCluster}} (
  fingerprint UInt64, timestamp_ns Int64 CODEC(DoubleDelta),
  last AggregateFunction(argMax, Float64, Int64),
  max SimpleAggregateFunction(max, Float64), min SimpleAggregateFunction(min, Float64),
  count AggregateFunction(count), sum SimpleAggregateFunction(sum, Float64),
  bytes SimpleAggregateFunction(sum, Float64)
) ENGINE = {{.AggregatingMergeTree}}
PARTITION BY toDate(toDateTime(intDiv(timestamp_ns, 1000000000)))
ORDER BY (fingerprint, timestamp_ns) {{.CREATE_SETTINGS}};
```

- Logs and metrics share one generic series/samples split. Labels stored once per series per day as a JSON string; an inverted index `time_series_gin` (key, val, fingerprint) resolves label matchers to fingerprints. Fingerprint is an FNV-style UInt64 over name-sorted pairs (`writer/utils/fingerprint.go`).
- Dedup: `ReplacingMergeTree(date)` on series rows; samples have none.
- Sort key lesson (`docs/table-ordering.md` @ v5.5.3): `samples_v3` defaults to `ORDER BY timestamp_ns`; `ADVANCED_SAMPLES_ORDERING` applies only at first creation and "It is not validated."; fingerprint compresses only with fingerprint-first ordering; `MODIFY ORDER BY` can only append columns, so reordering needs create, copy by partition, `EXCHANGE TABLES`; "Decide the sort key at first deployment."
- Later statements added a `type` column with `MODIFY ORDER BY (fingerprint, type)`, recreated MVs via rename/create/drop, and rewrote codecs (a comment notes MODIFY COLUMN affects only new parts).
- TTL applied at runtime (`ctrl/qryn/maintenance/rotate.go`): `ALTER TABLE ... MODIFY TTL ... + toIntervalDay(N)`, only when the value stored in a `settings` table changed; `SAMPLES_DAYS` default 7.
- Tenancy: none for logs/metrics (one database); traces table `tempo_traces` leads `PARTITION BY (oid, toDate(...))` and `ORDER BY (oid, trace_id, timestamp_ns)` (that `oid` is an org id is inferred).
- Migrations (`ctrl/qryn/maintenance/update.go`): script split on `";\n\n"`, statement index is the version, watermark in `ver (k UInt64, ver UInt64) ENGINE = ReplacingMergeTree(ver) ORDER BY k`; append-only; `{{.OnCluster}}` and a `Replicated` engine prefix in cluster mode.

### Grafana

grafana/clickhouse-datasource only queries ClickHouse ("Grafana supports ClickHouse through a plugin", https://github.com/grafana/clickhouse-datasource). Its `src/otel.ts` hard-codes OTel exporter schema presets (`otel129` uses `TimestampTime`, `otel130` drops it) and detects the version by column presence; #48770 shows the breakage when the exporter's schema moved. No Grafana storage product (Loki, Tempo, Mimir, Pyroscope) using ClickHouse was found (absence of evidence). Dropped from the comparison.
## SigNoz, Prometheus-on-ClickHouse, Netdata

Quoting limit: WebFetch would not return source files verbatim beyond about 125 characters per quote, and curl was not available. Short DDL (PromHouse, prom2click) is quoted in full as returned. SigNoz DDL is reconstructed from exact clause quotes (ORDER BY, PARTITION BY, TTL, sharding key) plus WebFetch column-list extractions; every such block is labelled "reconstructed" and its column types are not byte-exact.

Refs:
- SigNoz: `SigNoz/signoz-otel-collector` at commit `a3ae0d98abe9154f06b7911e8d3f6a516fb2517b` (2026-10-06, latest commit touching `cmd/signozschemamigrator`), https://github.com/SigNoz/signoz-otel-collector/commits/main/cmd/signozschemamigrator
- PromHouse: `b88c31e` (2019-03-09).
- prom2click: `920296cbccc19c64a43239d57f62823c7e4638d0` (2017-06-17).
- Netdata: master as fetched 2026-10-08.

### SigNoz

Uses ClickHouse: yes, a database per signal (`signoz_logs`, `signoz_traces`, `signoz_metrics`, `signoz_metadata`, `signoz_analytics`, `signoz_meter`). Every local table has a `distributed_*` twin and writers insert through the distributed tables.

#### Logs (`cmd/signozschemamigrator/schema_migrator/squashed_logs_migrations.go`, migration 16)

https://github.com/SigNoz/signoz-otel-collector/blob/a3ae0d98abe9154f06b7911e8d3f6a516fb2517b/cmd/signozschemamigrator/schema_migrator/squashed_logs_migrations.go

```sql
-- reconstructed; clause strings are exact quotes
CREATE TABLE signoz_logs.logs_v2 (
  ts_bucket_start UInt64 CODEC(DoubleDelta, LZ4),
  resource_fingerprint String CODEC(ZSTD(1)),
  timestamp UInt64 CODEC(DoubleDelta, LZ4),
  observed_timestamp UInt64 CODEC(DoubleDelta, LZ4),
  id String CODEC(ZSTD(1)),
  trace_id String CODEC(ZSTD(1)), span_id String CODEC(ZSTD(1)),
  trace_flags UInt32,
  severity_text LowCardinality(String) CODEC(ZSTD(1)),
  severity_number UInt8,
  body String CODEC(ZSTD(2)),
  attributes_string Map(LowCardinality(String), String) CODEC(ZSTD(1)),
  attributes_number Map(LowCardinality(String), Float64) CODEC(ZSTD(1)),
  attributes_bool   Map(LowCardinality(String), Bool) CODEC(ZSTD(1)),
  resources_string  Map(LowCardinality(String), String) CODEC(ZSTD(1)),
  scope_name String, scope_version String,
  scope_string Map(LowCardinality(String), String)
  -- 11 skip indexes: id_minmax, severity_*, trace_flags_idx, body_idx, scope_name_idx,
  -- attributes_string_idx_key (tokenbf_v1 on mapKeys), attributes_string_idx_val (ngrambf_v1),
  -- attributes_number_idx_key (tokenbf_v1), attributes_number_idx_val (bloom_filter),
  -- attributes_bool_idx_key (tokenbf_v1)
) ENGINE = MergeTree
PARTITION BY toDate(timestamp / 1000000000)
ORDER BY (ts_bucket_start, resource_fingerprint, severity_text, timestamp, id)
TTL toDateTime(timestamp / 1000000000) + toIntervalSecond(1296000)
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1
```

`distributed_logs_v2` (migration 17) shards on `cityHash64(id)`. `logs_v2_resource` (migration 20):

```sql
-- reconstructed
CREATE TABLE signoz_logs.logs_v2_resource (
  labels String CODEC(ZSTD(5)),
  fingerprint String CODEC(ZSTD(1)),
  seen_at_ts_bucket_start Int64 CODEC(Delta(8), ZSTD(1)),
  INDEX idx_labels lower(labels) TYPE ngrambf_v1(4, 1024, 3, 0) GRANULARITY 1,
  INDEX idx_labels_v1 labels TYPE ngrambf_v1(4, 1024, 3, 0) GRANULARITY 1
) ENGINE = ReplacingMergeTree
PARTITION BY toDate(seen_at_ts_bucket_start / 1000)
ORDER BY (labels, fingerprint, seen_at_ts_bucket_start)
TTL toDateTime(seen_at_ts_bucket_start) + toIntervalSecond(1296000) + toIntervalSecond(1800) DELETE
SETTINGS ttl_only_drop_parts = 1, index_granularity = 8192
```

`distributed_logs_v2_resource` shards on `cityHash64(labels, fingerprint)`. Key catalogue tables `logs_attribute_keys` and `logs_resource_keys` are `ReplacingMergeTree ORDER BY (name, datatype)`, filled by MVs over `arrayJoin(mapKeys(...))` (migration 19). Later logs migrations (`logs_migrations.go`): 1001 `tag_attributes_v2`; 1002 15-day TTL on key tables; 1004 a `resource` JSON column (max 100 dynamic paths); 1005 tokenbf indexes on `trace_id`/`span_id`; 2001 `body_v2` and `body_promoted` JSON columns plus a field-keys table; 2002 `inserted_at`/`created_at` DateTime64(3). None change ORDER BY and none copy data.

Writer (`exporter/clickhouselogsexporter/exporter.go`, same ref, https://github.com/SigNoz/signoz-otel-collector/blob/a3ae0d98abe9154f06b7911e8d3f6a516fb2517b/exporter/clickhouselogsexporter/exporter.go):
- `distributedLogsResourceV2Seconds = 1800`; bucket via `tsBucket(int64(ts/1000000000), distributedLogsResourceV2Seconds)`.
- Fingerprint via `fingerprint.CalculateFingerprint(res.Attributes().AsRaw(), fingerprint.ResourceHierarchy())`.
- One resource row per (bucket, fingerprint), repeats suppressed by a TTL cache keyed `MakeKeyForRFCache(bucketTs, fingerprint)`. Whether the key is set before a successful send (so a failed send could lose the resource row) is unverified.
- Synchronous batches (`PrepareBatch`/`Send`), no async_insert.
- Cardinality guards on the key catalogue: keys with too many distinct values in the last 6 h are skipped (`maxDistinctValues`), random-looking keys are skipped (`keycheck.IsRandomKey`), long values skipped (`common.MaxAttributeValueLength`).
- Batches failing with code 252 (too many partitions) are dropped, not retried.

Fingerprint (`utils/fingerprint/fingerprint.go`, `hash.go`): `label=value` pairs of a fixed hierarchy (cloud.provider, account, region, platform, k8s cluster, then namespace/workload/env/pod/host/container or node) joined by `;` with a trailing `hash=<FNV-64a>`. Because the fingerprint is a string sorted in ORDER BY, the hierarchy prefix clusters a cluster's or namespace's rows together.

#### Traces (`traces_migrations.go` migration 1000)

```sql
-- reconstructed
CREATE TABLE signoz_traces.signoz_index_v3 (
  ts_bucket_start UInt64, resource_fingerprint String,
  timestamp DateTime64(9), trace_id FixedString(32), span_id String,
  name LowCardinality(String), kind Int8, duration_nano UInt64, status_code Int16,
  attributes_string Map(...), attributes_number Map(...), attributes_bool Map(...),
  resources_string Map(...), events Array(String), links String
  -- materialized columns (has_error, http_url, db_name, `resource_string_service$$name` ...)
  -- ALIAS columns for v2 compatibility (traceID, serviceName ...)
) ENGINE = MergeTree
PARTITION BY toDate(timestamp)
ORDER BY (ts_bucket_start, resource_fingerprint, has_error, name, timestamp)
TTL toDateTime(timestamp) + toIntervalSecond(1296000)
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1
```

Distributed twin shards on `cityHash64(trace_id)`. `traces_v3_resource` mirrors `logs_v2_resource`. `trace_summary` is an `AggregatingMergeTree` (`start` min, `end` max, `num_spans` sum), `ORDER BY (trace_id)`, fed by an MV. The legacy v2 traces table used `ORDER BY (serviceName, hasError, toStartOfHour(timestamp), name, timestamp)`. Migrations 1001-1018 add `*_exists` columns, JSON `resource`/`scope`/`attributes`/`attributes_promoted` columns, materialized gen_ai columns, and bloom indexes on JSON paths.

#### Metrics (`squashed_metrics_migrations.go`)

https://github.com/SigNoz/signoz-otel-collector/blob/a3ae0d98abe9154f06b7911e8d3f6a516fb2517b/cmd/signozschemamigrator/schema_migrator/squashed_metrics_migrations.go

```sql
-- reconstructed (migration 7)
CREATE TABLE signoz_metrics.samples_v4 (
  env LowCardinality(String) DEFAULT 'default',
  temporality LowCardinality(String) DEFAULT 'Unspecified',
  metric_name LowCardinality(String),
  fingerprint UInt64 CODEC(Delta(8), ZSTD(1)),
  unix_milli Int64 CODEC(DoubleDelta, ZSTD(1)),
  value Float64 CODEC(Gorilla, ZSTD(1))
) ENGINE = MergeTree
PARTITION BY toDate(unix_milli / 1000)
ORDER BY (env, temporality, metric_name, fingerprint, unix_milli)
TTL toDateTime(unix_milli / 1000) + toIntervalSecond(2592000)
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1
```

```sql
-- reconstructed (migration 9)
CREATE TABLE signoz_metrics.time_series_v4 (
  env LowCardinality(String) DEFAULT 'default',
  temporality LowCardinality(String) DEFAULT 'Unspecified',
  metric_name LowCardinality(String),
  description LowCardinality(String) DEFAULT '' CODEC(ZSTD(1)),
  unit LowCardinality(String) DEFAULT '' CODEC(ZSTD(1)),
  type LowCardinality(String) DEFAULT '' CODEC(ZSTD(1)),
  is_monotonic Bool DEFAULT false CODEC(ZSTD(1)),
  fingerprint UInt64 CODEC(Delta(8), ZSTD(1)),
  unix_milli Int64 CODEC(Delta(8), ZSTD(1)),
  labels String CODEC(ZSTD(5))
) ENGINE = ReplacingMergeTree
PARTITION BY toDate(unix_milli / 1000)
ORDER BY (env, temporality, metric_name, fingerprint, unix_milli)
TTL toDateTime(unix_milli / 1000) + toIntervalSecond(2592000)
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1
```

```sql
-- reconstructed (migration 20, recreated in 28)
CREATE TABLE signoz_metrics.samples_v4_agg_5m (
  env ..., temporality ..., metric_name LowCardinality(String),
  fingerprint UInt64 CODEC(ZSTD(1)), unix_milli Int64 CODEC(Delta(8), ZSTD(1)),
  last SimpleAggregateFunction(anyLast, Float64),
  min SimpleAggregateFunction(min, Float64),
  max SimpleAggregateFunction(max, Float64),
  sum SimpleAggregateFunction(sum, Float64),
  count SimpleAggregateFunction(sum, UInt64)
) ENGINE = AggregatingMergeTree
PARTITION BY toDate(unix_milli / 1000)
ORDER BY (env, temporality, metric_name, fingerprint, unix_milli)
TTL toDateTime(unix_milli/1000) + INTERVAL 2592000 SECOND DELETE
SETTINGS ttl_only_drop_parts = 1
```

- `samples_v4_agg_30m` has the same shape, fed by an MV from `samples_v4_agg_5m`; `samples_v4_agg_5m_mv` reads `samples_v4` with `intDiv(unix_milli, 300000) * 300000`.
- `time_series_v4_6hrs`, `_1day` (MVs from `time_series_v4`) and `_1week` (MV from `_1day`) are ReplacingMergeTree tables shaped like `time_series_v4`.
- `exp_hist`: samples-shaped with `sketch AggregateFunction(quantilesDD(...), UInt64)`.
- Every metrics distributed table shards on `cityHash64(env, temporality, metric_name, fingerprint)`, so a series' samples and series rows land on one shard.
- No ReplacingMergeTree/AggregatingMergeTree table declares a version column.

Writer README (`exporter/signozclickhousemetrics/README.md`): `samples_v4` is "one row per data point"; the series table is "one row per series per hour" (timestamp floored to the hour); fingerprint is "a hash of the full label set" chained resource, scope, point, metric name. Docs: "The `time_series_v4` table has a granularity of 1 hour" and "The schemas are not final. We might change it in the future." https://signoz.io/docs/userguide/write-a-metrics-clickhouse-query/

#### Ordering pattern

Logs and traces lead with `(ts_bucket_start, resource_fingerprint, ...)`, 30-minute buckets, daily partitions. Queries filter the resource table first in a CTE, then `resource_fingerprint GLOBAL IN __resource_filter` (https://signoz.io/docs/userguide/logs_clickhouse_queries/). Metrics lead with `(env, temporality, metric_name, fingerprint, unix_milli)`.

#### Tenants

No tenant column in any OSS DDL above. Community PR #7060 (parameterized views, tenant columns) stayed a draft; a maintainer wrote (2025-03-03) "this looks very different from the approach we are considering" and (2025-06-17) that they were "very close to finalising our multi-tenant sql schema" (https://github.com/SigNoz/signoz/pull/7060). SigNoz Cloud's isolation: unverified.

#### Dedup

Series, resource, key and migration-status tables are ReplacingMergeTree without a version column; samples are plain MergeTree. A maintainer statement that `time_series_v4` can contain query-visible duplicates was found only via a search summary (unverified wording).

#### High-cardinality handling

- Series/samples split keeps the `labels` string out of the samples table; hourly series rows bound rewrite volume.
- Logs and traces keep attributes in typed Maps with bloom/tokenbf/ngram skip indexes, later adding JSON columns, promoted JSON paths and materialized hot columns.
- Metrics "volume control" (label reduction): rules drop/keep label keys, protected labels `le`, `quantile`, `__name__`, `__temporality__`, `deployment.environment`; switch by data time via `effective_from`; reduced data goes to `samples_v4_reduced_{last,sum}_{60s,5m,30m}`, `time_series_v4_reduced(_1day)`; landing buffers `samples_v4_buffer`/`time_series_v4_buffer` keep about 24 h; refreshable MVs build the reduced tables, sharded by `reduced_fingerprint` so "query-side joins never cross shards" (exporter README). User docs: "The most recent 24 hours are always full fidelity" (https://signoz.io/docs/metrics-management/aggregated-metric-data/).

#### Rollups and retention

| Data | Tier | Built from | Default TTL |
|---|---|---|---|
| Metric samples | `samples_v4_agg_5m` (AggregatingMergeTree) | MV on `samples_v4` | 30 d |
| Metric samples | `samples_v4_agg_30m` | MV on `samples_v4_agg_5m` | 30 d |
| Metric series | `time_series_v4_6hrs`, `_1day` | MVs on `time_series_v4` | 30 d |
| Metric series | `time_series_v4_1week` | MV on `_1day` | 30 d |
| Logs | none | | 15 d |
| Traces | `trace_summary` | MV on `signoz_index_v3` | 15 d |
| Resource tables | | | data TTL + 1800 s |

All set `ttl_only_drop_parts = 1`.

#### Lessons

- Logs v1 to logs_v2 (epic https://github.com/SigNoz/signoz/issues/5555): typed attribute maps with tokenbf indexes replaced key/value arrays; a minmax index on map numbers "did not work" so they chose to "Remove minmax index and only keep bloom filter"; materialized columns kept only for group-by.
- Blog (https://signoz.io/blog/query-performance-improvement): before the change a `namespace = 'production'` filter scanned 41,498 of 41,676 granules (99.5%) because the bloom filter "barely helped"; with `resource_fingerprint` in ORDER BY it read 222 of 26,135 (0.85%). "the biggest wins come from understanding your data layout and optimizing storage accordingly".
- Metrics v2 was already split (`samples_v2`, `time_series_v2`); v4 added `env`/`temporality` in the key, hourly series rows with rollup tiers, and sample aggregates. No design doc stating the v2-to-v4 reason was found (unverified).
- Too many parts on chained series MVs: #7983 (open, error chain through `time_series_v4_1week_mv_separate_attrs`, `1day`, `6hrs` MVs; maintainer blamed small batches, "about 100k rows per insert and no more than one insert per second"), https://github.com/SigNoz/signoz/issues/7983. #9794 "Too many parts (3001 ...)" on `time_series_v4_1week` with about 1.55 M series for one metric, https://github.com/SigNoz/signoz/issues/9794. That each base insert multiplies parts across chained MV targets is an inference (unverified).

#### Migrations (Go, replicated/sharded)

- `schema_migrator` package defines typed operations (`CreateTableOperation`, `AlterTable...`, `CreateMaterializedViewOperation`) and renders SQL. Since SigNoz v0.113 (collector v0.142.0) it runs as `signoz-otel-collector migrate bootstrap | sync up | async up | sync check | async check | ready`. Sync: "These mutate the store schema and must complete before the application starts." Async: "background data migrations that do not block the application." The change "removes the startup ordering issues caused by the old Job-based approach" (https://signoz.io/docs/operate/migration/upgrade-0-113/, https://signoz.io/changelog/2026-02-25--breaking-change-new-migration-component-replaces-signoz-schema-migrator-jf8y4e6rnpt9b8pobd01yfun/).
- Status table per database: `schema_migrations_v2`, ReplacingMergeTree `ORDER BY migration_id`, columns `migration_id UInt64, status String, error String, created_at/updated_at DateTime64(9)`, distributed twin `ShardingKey: "rand()"`. Reads `... WHERE migration_id = %d SETTINGS final = 1`; inserts via the distributed table; status updates via `ALTER TABLE %s.schema_migrations_v2 ON CLUSTER %s UPDATE status = $1, error = $2, updated_at = $3 WHERE migration_id = $4`.
- `ON CLUSTER` emitted only when a cluster is configured. `WithReplication()` only prefixes the engine name with `Replicated`; reliance on server `default_replica_path`/`default_replica_name` is an inference (unverified).
- An operation is sync if it is not a mutation, is idempotent and lightweight.
- Before each operation it polls `system.distributed_ddl_queue` (status != 'Finished') and `system.mutations` (is_done = 0) on every host from `system.clusters`; before column drops it checks `system.distribution_queue`. Comment: "no mutation should be running for more than 15 minutes".
- `sync_up.go` TODO: "Figure out how to run migrations on all shards when replication is not enabled".
- WebFetch's reading of `manager.go` (unverified): no cross-instance lock, a crash leaves `in-progress` stuck, the DDL-queue wait returns nil after 10 failed attempts.
- Big moves use dual tables, not in-place rewrites: v0.55 logs and v0.64 traces upgrades kept writing the old table behind flags until one retention period (15 days) passed; `signoz/migrate:0.55`/`0.64` copy only TTL settings and materialized columns (https://signoz.io/docs/operate/migration/upgrade-0-55/, https://signoz.io/docs/operate/migration/upgrade-0-64/).

### ClickHouse `TimeSeries` engine

Source: https://clickhouse.com/docs/engines/table-engines/special/time_series. "Private preview", enabled by `enable_time_series_table = 1`, table `version` 7 pinned at CREATE.

| Target | Engine | Key | Columns |
|---|---|---|---|
| samples | MergeTree | `ORDER BY (id, timestamp)`, index_granularity 32768 | `id Tuple(UInt64, LowCardinality(UUID))`, `timestamp DateTime64(3)` CODEC(Delta, T64, ZSTD(3)), `value Float64` CODEC(ALP, ZSTD(3)) |
| recent samples | MergeTree | partition `toStartOfInterval(..., toIntervalHour(5))`, `ORDER BY (id, timestamp)` | TTL 4 days, every insert writes both tables |
| tags | AggregatingMergeTree | `PRIMARY KEY metric_name`, `ORDER BY (metric_name, id)` | `metric_name`, `tags Map(LowCardinality(String), String)`, `min_time`/`max_time` SimpleAggregateFunction |
| metric families | ReplacingMergeTree | `ORDER BY metric_family` | `type`, `unit`, `help` |

- Default series id: `tuple(sipHash64(metric_name), toLowCardinality(reinterpretAsUUID(sipHash128(tags))))`; changing `id_generator` on existing data re-keys series.
- Tag dedup by min/max-time aggregation, or a server-local 100 MiB dedup cache (expiry 3600 s) whose reset makes the next insert rewrite all rows.
- `tags_to_columns` promotes chosen labels to real columns.
- Old table versions reject writes or PromQL; the fix is a new table plus `INSERT ... SELECT`.
- Tenancy, rollups, downsampling: not documented.
- "Introducing PromQL" (2026-09-15): coverage "over 85%" (https://clickhouse.com/blog/introducing-promql).
- ClickHouse's high-cardinality part 2 post (2026-05-15) for non-Prometheus metrics: "We are not attempting to directly model Prometheus metrics ... inside ClickHouse"; use `Map(LowCardinality(String), String)` labels with text indexes plus materialized columns for hot keys; "we recommend using the Map type rather than the JSON type for observability workloads"; example `ORDER BY (host, toStartOfMinute(time), status, application)` (https://clickhouse.com/blog/clickhouse-vs-promethous-high-cardinality-part-2-cardinality-in-clickhouse).

### PromHouse (Percona-Lab)

`storages/clickhouse/clickhouse.go` @ `b88c31e`, https://github.com/Percona-Lab/PromHouse/blob/master/storages/clickhouse/clickhouse.go

```sql
CREATE TABLE IF NOT EXISTS %s.time_series (
    date Date,
    fingerprint UInt64,
    labels String
)
ENGINE = ReplacingMergeTree
PARTITION BY date
ORDER BY fingerprint
```

```sql
CREATE TABLE IF NOT EXISTS %s.samples (
    fingerprint UInt64,
    timestamp_ms Int64,
    value Float64
)
ENGINE = MergeTree
PARTITION BY toDate(timestamp_ms / 1000)
ORDER BY (fingerprint, timestamp_ms)
```

README (https://github.com/Percona-Lab/PromHouse): fingerprint uses the Prometheus algorithm; ReplacingMergeTree prevents duplicates "if several ClickHouses wrote the same time series"; all series held in memory, reloaded via `SELECT DISTINCT fingerprint, labels` every 5 s; about 4.5:1 compression (about 5.3 B/sample) against Prometheus at about 12:1; "do not use it in production". No tenant, TTL or rollup.

### prom2click (mindis)

README @ `920296c`, https://github.com/mindis/prom2click

```sql
CREATE TABLE IF NOT EXISTS metrics.samples
(
      date Date DEFAULT toDate(0),
      name String,
      tags Array(String),
      val Float64,
      ts DateTime,
      updated DateTime DEFAULT now()
)
ENGINE = ReplicatedGraphiteMergeTree(
      '/clickhouse/tables/{shard}/metrics.samples',
      '{replica}', date, (name, tags, ts), 8192, 'graphite_rollup'
);
CREATE TABLE IF NOT EXISTS metrics.dist ( ...same columns... )
  ENGINE = Distributed(metrics, metrics, samples, sipHash64(name));
```

Single wide table, tags in the sort key, rollups by GraphiteMergeTree rules. "CPU-heavy" at hundreds of thousands of samples/s. Unmaintained since 2017.

### chproxy

Only a proxy: "Open-Source ClickHouse http proxy and load balancer" (https://github.com/ContentSquare/chproxy, https://www.chproxy.org/). No schema or storage. Dropped from the comparison.

### Netdata

Does not use ClickHouse. Storage is its own DBENGINE with three tiers (per-second, 60-point, 3,600-point aggregates) and disk-space retention (https://github.com/netdata/netdata/blob/master/src/database/engine/README.md). Exporting connectors do not list ClickHouse (https://github.com/netdata/netdata/blob/master/src/exporting/README.md). Its only ClickHouse link is a collector that monitors ClickHouse. Dropped from the comparison; its tiering is a non-ClickHouse analogue only.

### Samples/series split lessons (all Prometheus-shaped stores)

- PromHouse, qryn, SigNoz (v2 and v4) and the TimeSeries engine split series from samples by a label-hash id: series table Replacing/Aggregating MergeTree by name/id, samples MergeTree by `(id, ts)`. prom2click (single table) is the exception and is unmaintained.
- Series-row churn is bounded by a time bucket: PromHouse per-day `date`, SigNoz one row per series per hour plus coarser tiers, TimeSeries engine min/max-time aggregation.
- Writers cache seen series to avoid rewriting them; cache resets cause full rewrites.
- Dedup is eventual (ReplacingMergeTree without version).
## Multi-tenant production users: Sentry Snuba, PostHog, Contentsquare, Cloudflare, Uber, Glaber, LibreNMS

Blocks marked "verbatim" were printed exactly by the fetch tool; "reconstructed" blocks were rebuilt from per-line quotes and are not exact text.

### Sentry Snuba

Ref `getsentry/snuba` `285ff386e9eef593e03ce03598289f38f303fc59` (master, 2026-10-07).

Migration system:
- Groups (`snuba/migrations/groups.py`): SYSTEM, EVENTS, TRANSACTIONS, ..., EVENTS_ANALYTICS_PLATFORM, ...; "Migration groups are mandatory by default." MIGRATIONS.md: "Each migration is applied in order by group, and the groups are themselves ordered"; `system` "must always remain as the first group" (https://github.com/getsentry/snuba/blob/285ff386e9eef593e03ce03598289f38f303fc59/MIGRATIONS.md).
- Migrations "that cannot be completed immediately, such as those that contain a data migration, must be marked with blocking = True" (`snuba/migrations/migration.py`); `--force` "assumes that any consumers filling the corresponding table are stopped".
- Old migrations are squashed into `migration.SquashedMigration` stubs; a deprecated group's migrations are deleted while its tables remain (PR #8499, https://github.com/getsentry/snuba/pull/8499).
- Topology: storage nodes hold `_local` tables; query nodes hold `_dist` Distributed tables (https://getsentry.github.io/snuba/clickhouse/topology.html). Every operation names `OperationTarget.LOCAL` or `DISTRIBUTED`; add-column ops run local first, drop-column ops run distributed first.
- PR #7668 (2026-01-26) "Use ON CLUSTER for DDL statements": before, the same SQL ran "on each node individually"; after, DDL goes to one node with `alter_sync=2` waiting for all replicas and a 5-minute MIGRATE timeout (was 10 s). `InsertIntoSelect` stays per node (https://github.com/getsentry/snuba/pull/7668).

Verbatim, `snuba/migrations/operations.py` (excerpt):

```python
    def _get_on_cluster_clause(self) -> str:
        """Returns ON CLUSTER clause for multi-node clusters, empty string otherwise.

        Uses the appropriate cluster name based on target type:
        - LOCAL: uses cluster_name (for storage nodes)
        - DISTRIBUTED: uses distributed_cluster_name (for query nodes)
        """
        cluster = get_cluster(self._storage_set)
        if cluster.is_single_node():
            return ""
...
        try:
            connection.command(sql, settings=self._settings)
            # No polling needed - alter_sync=2 and mutations_sync=2 ensure ClickHouse
            # blocks until all replicas confirm completion
```

Engine selection (`snuba/migrations/table_engines.py`, reconstructed): single node emits `MergeTree()`; otherwise `ReplicatedMergeTree({zoo_path}, '{replica}')` with path `/clickhouse/tables/{storage_set}/{shard}/{database}/{table}`; ReplacingMergeTree becomes `ReplicatedReplacingMergeTree({zoo_path}, '{replica}', {version_column})`. The same migration code serves a single node and a cluster.

Status table, verbatim `snuba/migrations/system_migrations/0001_migrations.py` (excerpt):

```python
columns: Sequence[Column[Modifiers]] = [
    Column("group", String()),
    Column("migration_id", String()),
    Column("timestamp", DateTime()),
    Column(
        "status",
        Enum([("completed", 0), ("in_progress", 1), ("not_started", 2)]),
    ),
    Column("version", UInt(64, Modifiers(default="1"))),
]
...
                engine=ReplacingMergeTree(
                    storage_set=StorageSetKey.MIGRATIONS,
                    version_column="version",
                    order_by="(group, migration_id)",
                ),
```

Errors table, verbatim `snuba/snuba_migrations/events/0011_rebuild_errors.py`:

```python
sample_expr = "cityHash64(event_id)"
...
engine=table_engines.ReplacingMergeTree(
    storage_set=StorageSetKey.EVENTS,
    version_column="deleted",
    order_by=f"(project_id, toStartOfDay(timestamp), primary_hash, {sample_expr})",
    partition_by="(retention_days, toMonday(timestamp))",
    sample_by=sample_expr,
    ttl="timestamp + toIntervalDay(retention_days)",
    settings={"index_granularity": "8192"},
),
```

```python
Column("tags", Nested([("key", String()), ("value", String())])),
Column("contexts", Nested([("key", String()), ("value", String())])),
Column(
    "_tags_hash_map",
    Array(UInt(64), Modifiers(materialized=TAGS_HASH_MAP_COLUMN)),
),
Column("deleted", UInt(8)),
Column("retention_days", UInt(16)),
```

Docstring: "Partition key reflects retention_days value not only 30 or 90". Migration `0017_errors_add_indexes.py` adds a bloom filter on `_tags_hash_map` and sets `ttl_only_drop_parts: 1`. `TAGS_HASH_MAP_COLUMN` hashes each escaped `key=value` with `cityHash64` into a materialized `Array(UInt64)` (summary; the `has(...)` query form is inferred).

Transactions (`transactions/0001_transactions.py`, reconstructed): `ReplacingMergeTree` versioned on `deleted`, `order_by="(project_id, toStartOfDay(finish_ts), transaction_name, cityHash64(span_id))"`, `partition_by="(retention_days, toMonday(finish_ts))"`, TTL `finish_ts + toIntervalDay(retention_days)`.

Events analytics platform items, verbatim `events_analytics_platform/0024_items.py`: 40 hash-bucketed attribute maps per type (`attributes_string_0..39 Map(String, String)`, `attributes_float_0..39 Map(String, Float64)`), plus:

```python
engine=table_engines.ReplacingMergeTree(
    primary_key="(organization_id, project_id, item_type, timestamp)",
    order_by="(organization_id, project_id, item_type, timestamp, trace_id, item_id)",
    partition_by="(retention_days, toMonday(timestamp))",
    settings={"index_granularity": "8192"},
    storage_set=storage_set_name,
    ttl="timestamp + toIntervalDay(retention_days)",
),
```

- Attribute indexes were added and then dropped in later migrations (0006, 0008-0010, 0041-0044 by file name; reasons unverified, https://github.com/getsentry/snuba/tree/285ff386e9eef593e03ce03598289f38f303fc59/snuba/snuba_migrations/events_analytics_platform).
- 0069 (summary): `eap_items_2` created as `ReplacingMergeTree(version)` with identical structure "so ATTACH PARTITION works"; data attached from v1; reads switched behind an option; `version` defaults to a millisecond timestamp so live writes supersede attached rows.
- Downsampled tiers `eap_items_1_downsample_{8,64,512}` (0032/0034), populated by MV with deterministic hash sampling: `WHERE (cityHash64(item_id + {sampling_weight}) % {sampling_weight}) = 0`; a comment says `rand64()` made tier sample rates off by an order of magnitude.

Generic metrics (removed; last ref `b9669b6`, summary): raw `MergeTree` ORDER BY `(use_case_id, org_id, project_id, metric_id, timestamp)`, 7-day TTL; tag keys are integers from an indexer (`tags Nested(key UInt64, indexed_value UInt64, raw_value String)`); aggregate table AggregatingMergeTree with `primary_key="(org_id, project_id, metric_id, granularity, timestamp)"`, `partition_by="(retention_days, toMonday(timestamp))"`; an MV fans each raw row into 10 s, 60 s, 1 h, 1 d granularities via `arrayJoin([0,1,2,3])`.

Published lessons:
- "Data is partitioned by time and retention window"; "Inserting into ClickHouse in batches is critical because each insert creates a new physical directory." (https://blog.sentry.io/introducing-snuba-sentrys-new-search-infrastructure/)
- "By properly selecting the Clickhouse table engine to deduplicate rows we can achieve exactly once semantics if we accept eventual consistency"; single writer per table; events topic partitioned by project id (https://getsentry.github.io/snuba/architecture/overview.html).

### PostHog

Refs: `posthog/models/event/sql.py`, `posthog/models/person/sql.py` @ `d935e95e4348c38d3028e1ec0e22eda32cc9c69f`; `migration_tools.py` @ `fffac77218b6bc81365792ad3a22012dd35ed6e9`.

Verbatim (excerpts):

```python
def EVENTS_TABLE_SQL():
    return (
        EVENTS_TABLE_BASE_SQL
        + """PARTITION BY toYYYYMM(timestamp)
ORDER BY (team_id, toDate(timestamp), event, cityHash64(distinct_id), cityHash64(uuid))
{sample_by}
{storage_policy}
"""
```

```python
def EVENTS_DATA_TABLE_ENGINE():
    return ReplacingMergeTree("events", ver="_timestamp", replication_scheme=ReplicationScheme.SHARDED)
```

```python
def PERSON_DISTINCT_ID2_TABLE_SQL(on_cluster=True):
    return (
        PERSON_DISTINCT_ID2_TABLE_BASE_SQL
        + """
    ORDER BY (team_id, distinct_id)
    SETTINGS index_granularity = 512
    """
```

`properties VARCHAR CODEC(ZSTD(3))` holds event properties as a JSON string; person and group properties are denormalised onto events. Persons and `person_distinct_id2` are `ReplacingMergeTree(ver="version")` with `is_deleted`; queries use `argMax(..., version)` and `HAVING argMax(is_deleted, version) = 0` (https://posthog.com/handbook/engineering/clickhouse/schema/person-distinct-id.md). No TTL in `EVENTS_TABLE_SQL`.

Migrations: `posthog/clickhouse/migrations/README.md`: "Do not use the `ON CLUSTER` clause", because `run_sql_with_exceptions` already runs DDL per node by role and the default cluster holds only data nodes; status in `infi_clickhouse_orm_migrations`; "ClickHouse schema changes should be created as a separate PR from application code changes." Async migrations handle row-rewriting changes such as a new ORDER BY: "you cannot add expressions containing existing columns to the sorting key" (https://posthog.com/blog/async-migrations).

Handbook lessons (https://posthog.com/handbook/engineering/clickhouse/schema/sharded-events.md and siblings):
- ReplacingMergeTree dedup on uuid: "This design decision is a mistake." Dedup "only does work at merge-time", "merges are not guaranteed to occur", query-time dedup "prohibitively expensive".
- Monthly partitions: "Critical to PostHog functioning well."
- Sharding by `distinct_id`: "this needs fixing, however resharding data is hard".
- "This ORDER BY doesn't speed up filtering by common JSON properties"; JSON property columns are "the biggest ones we have".
- Person columns on events because "JOINs in ClickHouse are expensive and this frequently caused memory errors for our largest users".
- CollapsingMergeTree "is not ideal for frequently updating a single row"; ReplacingMergeTree with `version` was "over 2x faster".
- `app_metrics` AggregatingMergeTree: "we still need to sum values in queries as merges may never occur".
- Replication page: create tables "preferably via using `ON CLUSTER`" (conflicts with the migrations README); "Always use unique ZooKeeper paths for table definitions as re-use can and will lead to data loss."
- Operations page: a ReplacingMergeTree ORDER BY change "temporarily doubles the amount of disk space"; mutations hung for months on orphaned ZooKeeper records.
- Persons-on-events tradeoff: "Users will no longer be merged retroactively in some situations" (https://posthog.com/blog/persons-on-events).

### Contentsquare

https://clickhouse.com/blog/contentsquare-migration-from-elasticsearch-to-clickhouse : on Elasticsearch "it was not possible for us to handle any tenant that would not fit into a single cluster"; "Make sure all the data about your entities are in a single shard"; Kafka partitions match the ClickHouse sharding key; "Make sure you can shut down all the processes that write to a table easily"; "ClickHouse is very very fast but does very little query optimisation for you." Outcome 11x cheaper, 13-month retention. Events reconciliation by AggregatingMergeTree with `SimpleAggregateFunction`, warning "all aggregations are not idempotent" (https://engineering.contentsquare.com/2022/real-time-events-reconciliation-clickhouse/). No DDL or tenant-key layout published.

### Cloudflare

- HTTP analytics (https://blog.cloudflare.com/http-analytics-for-6m-requests-per-second-using-clickhouse/): first schema had eight minutely AggregatingMergeTree MVs, one per breakdown, and query joins over 300 lines; second schema used SummingMergeTree with Nested map columns, plus a separate view for a 5%-of-queries endpoint so "its more dispersed primary key will not affect performance of Zone dashboard queries". `index_granularity` 32 on aggregates halved latency. Primary key columns not stated.
- DNS analytics (https://blog.cloudflare.com/how-cloudflare-analyzes-1m-dns-queries-per-second/): several specialised tables rather than one compromise index; aggregate-state MVs.
- ABR (https://blog.cloudflare.com/explaining-cloudflares-abr-analytics/): "we write the same data at multiple resolutions into separate tables", "from 100% to 0.0001%", at "additional 12% of disk storage".
- Log analytics (https://blog.cloudflare.com/log-analytics-using-clickhouse/): same-type fields in arrays; "One common mistake ClickHouse users make is overly granular partitioning keys".
- Ready-Analytics (https://blog.cloudflare.com/clickhouse-query-plan-contention/, 2026-05-14): one shared table, "Datasets are disambiguated by a `namespace`", primary key `(namespace, indexID, timestamp)`; moving the partition key from `day` to `(namespace, day)` for per-namespace retention produced 30k then 160k parts per replica and 45% of CPU in `filterPartsByPartition`; "even a well-planned change can fall victim to incorrect assumptions".

### Uber

https://www.uber.com/blog/logging/ (2021-02-19): fields grouped by type into parallel arrays ("(string.names, string.values)"); tenancy by a `_namespace` column; "we pack multiple tenants into tables" to keep batches large; "only 5% of the indexed fields were being used", so hot fields become materialized columns backfilled asynchronously; "the write path has much less error budget than the query path". No DDL published.

### Glaber (Zabbix fork)

Archived GitLab `mikler/glaber`, branch 3.4 @ `324c32e5ae72c554cc42459b89a9157427184d84` (2025-10-16), `database/clickhouse/schema.sql`, verbatim:

```sql
CREATE TABLE glaber.history_dbl (   day Date,  
                                itemid UInt64,  
                                clock DateTime,  
                                hostname String,
                                itemname String,
                                ns UInt32, 
                                value Float64
                            ) ENGINE = MergeTree()
PARTITION BY toYYYYMM(day)
ORDER BY (itemid, clock) 
TTL day + INTERVAL 6 MONTH;
```

```sql
CREATE TABLE glaber.trends_dbl
(
    day Date,
    itemid UInt64,
    clock DateTime,
    value_min Float64,
    value_max Float64,
    value_avg Float64,
    count UInt32,
    hostname String,
    itemname String
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(day)
ORDER BY (itemid, clock)
TTL day + toIntervalMonth(24)
SETTINGS index_granularity = 8192;
```

Per value type tables; host and item names denormalised onto every row; trends are a separate plain MergeTree written by the server, not an MV; cluster variant shards on `itemid` (reconstructed). No tenant, no dedup.

Zabbix 8.0 native backend: see the network section. Additional docs facts (https://www.zabbix.com/documentation/8.0/en/manual/appendix/install/clickhouse_setup): "Zabbix does not calculate or store trends in ClickHouse"; `ON CLUSTER` "reaches only nodes that are part of the cluster when you run the command".

### LibreNMS

No ClickHouse support: "Storing Metrics" lists Graphite, InfluxDB, InfluxDBv2, OpenTSDB, Prometheus, Kafka (https://docs.librenms.org/Extensions/metrics/InfluxDB/); zero GitHub issues or PRs match "clickhouse" (https://github.com/librenms/librenms/issues?q=clickhouse). Dropped.
## (a) Comparison table

Sources for each cell are in the system sections above.

| System | Data | Table split | ORDER BY lead | PARTITION BY | Tenant | Dedup | Labels / cardinality | Rollups | TTL | Migrations |
|---|---|---|---|---|---|---|---|---|---|---|
| Akvorado | flows, interface metadata | per domain + per resolution | `toStartOfFiveMinutes(t), ExporterAddress, InIfName, OutIfName` | interval = TTL / max-partitions | none (attribute columns; several DBs per cluster) | none on flows; `ReplacingMergeTree(TimeReceived)` for exporters | wide LowCardinality columns; high-card columns raw-only | SummingMergeTree MVs 1m/5m/1h | per tier, `ttl_only_drop_parts` | Go reconciler vs `system.tables`/`columns`, `ExecOnCluster`, `alter_sync=2` |
| GoFlow2 example | flows | raw + 5m | `time_received_ns` | `date` | none | none | wide columns | SummingMergeTree 5m | none | `IF NOT EXISTS` |
| ntopng | flows, alerts, timeseries | flows wide; alerts per entity; timeseries generic | `FIRST_SEEN, ...` / `tstamp` / `schema_name, ifid, tstamp` | daily | `ntopng_instance_name` column | ReplacingMergeTree(version) for assets | Map tags + metrics, `ifid` promoted later | none | partition drop job (docs say TTL) | re-run idempotent SQL; `ON CLUSTER` variant |
| Zabbix 8.0 | item history | per value type | `itemid, clock_ns` | `toDate` | none | none | integer series id, metadata in RDBMS | none in CH | TTL 31 d | shell scripts |
| Glaber | item history + trends | per value type, trends separate | `itemid, clock` | `toYYYYMM(day)` | none | none | integer id + denormalised host/item names | trends table written by server | 6 mo / 24 mo | SQL file |
| FastNetMon | traffic counters | per entity (host, network, interface, ASN) | entity, date | `metricDate` | none | none | wide columns | none | 7 d | `schema_version` column |
| Telegraf/clickstack | SNMP ifTable | per metric name | `time` | none | none | none | tags as columns, ALTER on new tag | none | none | `table_update_template` |
| yt-snmp-go-poller | counters + inventory | one wide table | `tuple()` | none | none | none | inline | none | none | none |
| eait-itig flow-collector | flows | raw + cascaded tiers | (paraphrase) | daily | none | SummingMergeTree | wide | 5s/1m/5m/1h MVs | per tier | SQL file |
| OTel exporter | logs, traces, metrics | per signal, per metric type | logs `5-min bucket, ServiceName, ts`; metrics `ServiceName, MetricName, hour, cityHash64(Attributes), ts` | daily | none | none | inline Map + bloom indexes | none | optional | none (`create_schema` only) |
| ClickStack/HyperDX | same + sessions | per signal + kv rollups | as OTel | daily | none (one DB) | none | Map + materialized hot keys + text indexes | 15-min kv counts | env-driven, safe reconcile | goose without versioning, idempotent |
| Uptrace | spans, logs, events, metrics | per domain, index/payload split; metrics min/hour | `project_id, ...` | daily | `project_id` first | AggregatingMergeTree merge on bucket key | parallel arrays + `attrs_hash` | minute to hour MV | per signal | own Go `chmigrate`, CollapsingMergeTree version table, lock |
| Coroot | logs, traces, profiles, optional metrics | per signal + catalogues | `MetricName, MetricHash, ts` | daily | database per project | ReplacingMergeTree catalogues | Map + hash | trace histogram | TTL + disk-space partition drop | idempotent per start |
| qryn/gigapipe | logs, metrics, traces | generic series/samples | samples `timestamp_ns` (configurable once) | daily | none (traces `oid`) | ReplacingMergeTree(date) on series | JSON label string + inverted index table | 15 s aggregate | runtime `MODIFY TTL` | statement-index watermark |
| SigNoz | logs, traces, metrics | per signal; metrics series/samples split; logs resource table | logs `bucket, resource_fingerprint, ...`; metrics `env, temporality, metric_name, fingerprint, ts` | daily | none in OSS | ReplacingMergeTree (no version) on series | typed Maps + resource fingerprint + series table | 5m/30m samples; 6h/1d/1w series | 15/30 d | Go typed ops, sync/async, cluster waits |
| CH TimeSeries engine | Prometheus | samples / tags / metric families | `id, timestamp` | (samples none documented) | not documented | min/max time aggregation on tags | tags Map + hash id | none | recent-samples 4 d | engine version pinned |
| PromHouse | Prometheus | series / samples | `fingerprint, timestamp_ms` | daily | none | ReplacingMergeTree on series | label string per series | none | none | none |
| prom2click | Prometheus | one table | `name, tags, ts` | GraphiteMergeTree | none | none | tags array in key | Graphite rollup | none | none |
| Snuba | errors, transactions, items, metrics | per domain | `project_id` / `organization_id, project_id, ...` | `(retention_days, toMonday(ts))` | org/project first | ReplacingMergeTree versioned | Nested tags + hashed tag array + bloom; bucketed attribute maps | sampled tiers 8/64/512; metric granularities | `ts + retention_days` | Python groups, LOCAL/DIST targets, `ON CLUSTER` + `alter_sync=2` |
| PostHog | events, persons | per domain | `team_id, toDate(ts), event, ...` | `toYYYYMM` | `team_id` first | ReplacingMergeTree (called a mistake for events) | JSON string + materialized columns | app_metrics AggregatingMergeTree | none on events | Python per node role, `ON CLUSTER` banned; async migrations |
| Contentsquare | analytics events | not published | not published | not published | entity colocated per shard | AggregatingMergeTree reconciliation | not published | not published | 13 mo | not published |
| Cloudflare | HTTP, DNS, logs, Ready-Analytics | specialised tables per query shape; multi-resolution tables | `namespace, indexID, ts` (Ready-Analytics) | `day`, then `(namespace, day)` (caused part explosion) | `namespace` first | not stated | Nested maps; typed arrays | many MV aggregates; sampled tiers | partition drop job | not published |
| Uber | logs | tenants packed into shared tables | not published | not published | `_namespace` column | not stated | typed parallel arrays + materialized hot fields | none stated | purge job | not published |

## (b) Recurring patterns and anti-patterns

### Patterns

| Pattern | Seen in |
|---|---|
| Tenant id is the first ORDER BY column of a shared table, not a partition column and not a table per tenant | Snuba (`project_id`, `organization_id, project_id`), PostHog (`team_id`), Uptrace (`project_id`), Cloudflare Ready-Analytics (`namespace`), Uber (`_namespace` column) |
| Database per tenant | Coroot (per project); Kentik outside ClickHouse |
| Coarse time partitions (day or week or month) with `ttl_only_drop_parts = 1` so retention drops whole parts | Akvorado, OTel exporter, ClickStack, SigNoz, Uptrace, Snuba, qryn |
| Per-tenant retention as a low-cardinality column inside the partition key | Snuba `(retention_days, toMonday(ts))` with TTL `ts + toIntervalDay(retention_days)` |
| Typed columns for known dimensions; Map or arrays only for open-ended extras; hot map keys promoted to materialized columns | Akvorado (all typed), OTel/ClickStack (`__otel_materialized_*`), Uber (hot fields), PostHog (materialized columns), ntopng (`ifid` promoted), SigNoz (materialized trace columns) |
| Entity identity as a compact integer or hash in the sort key, entity metadata elsewhere | Zabbix/Glaber `itemid`, SigNoz/PromHouse/qryn `fingerprint`, Uptrace `attrs_hash`, Coroot `MetricHash`, TimeSeries engine `id` |
| Series/samples split when the label set is open-ended | PromHouse, qryn, SigNoz metrics, TimeSeries engine |
| Resource/entity attributes moved out of the fact row into a resource table keyed by a hierarchical fingerprint, and the fingerprint put in ORDER BY | SigNoz logs_v2/traces_v3 (granules scanned 99.5% to 0.85%) |
| Latest-state table as ReplacingMergeTree keyed by entity with a version or time column | Akvorado `exporters`, ntopng `assets`, PostHog persons |
| Interval-presence via `min(first_seen)`/`max(last_seen)` SimpleAggregateFunction on an AggregatingMergeTree keyed by entity | TimeSeries engine tags table, SigNoz `trace_summary`, OTel `trace_id_ts` |
| Rollup tiers as SummingMergeTree or AggregatingMergeTree fed by MVs from the raw table, each tier with its own TTL | Akvorado, SigNoz, Uptrace, GoFlow2, eait-itig, Snuba generic metrics, Cloudflare |
| Raw counters stored, rates computed at query time | ntopng (`argMax` per bucket, `lag()`, `greatest(0, delta)/step`), clickstack (window-function deltas) |
| Denormalise slowly changing enrichment onto fact rows at ingest | Akvorado outlet (GeoIP, network attributes), PostHog persons-on-events, Glaber host/item names |
| Application-side batched inserts rather than the Kafka engine | Akvorado (dropped Kafka engine in 2.0.0, ch-go native batches), SigNoz, Snuba, Uptrace |
| Replicated engine and `ON CLUSTER` with `alter_sync=2` handled by the migration tool, same code path for single node and cluster | Snuba (since PR #7668), Akvorado, SigNoz, Uptrace, qryn |
| Big schema moves by new table plus dual write or `ATTACH PARTITION`, not in-place rewrite | SigNoz v0.55/v0.64, Snuba `eap_items_2`, qryn reorder procedure, PostHog async migrations |

### Anti-patterns and regrets

| Anti-pattern | Who shows it, with the lesson |
|---|---|
| Relying on ReplacingMergeTree for correctness of fact rows | PostHog: "This design decision is a mistake", dedup "only does work at merge-time"; SigNoz series tables carry query-visible duplicates (unverified wording); PostHog app_metrics: "we still need to sum values in queries as merges may never occur" |
| Partition key too fine or including tenant | Cloudflare `(namespace, day)` gave 160k parts per replica and 45% CPU in part filtering; Cloudflare log post: "overly granular partitioning keys"; Akvorado 1.4.2 had to limit partition counts on rollup tables |
| Sort key chosen late or wrong | qryn: "Decide the sort key at first deployment"; Akvorado 1.10.0 primary key change not migrated, manual copy with "a risk of data loss"; OTel exporter changed logs ORDER BY twice with no migration path (PR #47720); PostHog ORDER BY changes need async migrations and double disk |
| Attributes only in a Map with bloom filters, no structural key | SigNoz: bloom filter "barely helped", 99.5% of granules scanned; OTel #24675 full scans; Snuba EAP added and then dropped several attribute indexes |
| Unsorted map attributes as identity | OTel #33634: different key order made distinct series and "split GROUP BY results" |
| One generic table for every signal | ntopng timeseries had to promote `ifid` out of the Map via a whole-table mutation; Telegraf per-metric-name tables need ALTER on every new tag |
| Small inserts and chained MVs | SigNoz #7983/#9794 "Too many parts" through chained series MVs; Snuba: "each insert creates a new physical directory" |
| Kafka engine inside ClickHouse | Akvorado removed it in 2.0.0: the protobuf schema had to be shipped to ClickHouse out of band |
| Large enrichment dictionaries in ClickHouse memory | Akvorado dropped the `networks` dictionary in 2026.8.0 because it held a copy of the GeoIP databases in ClickHouse memory |
| Retrofitting replication | Akvorado: "Do not try to enable cluster mode on an existing setup!"; Uptrace: enabling replication requires `ch reset` (data loss); SigNoz community reports of nodes left without tables (unverified) |
| Schema creation by many writers at startup | OTel exporter README recommends `create_schema: false` in production; #35713 cluster creation failed |
| ON CLUSTER used inconsistently | PostHog bans it in migrations while its replication page recommends it; Zabbix: it "reaches only nodes that are part of the cluster when you run the command" |
| ZooKeeper path reuse | PostHog: "re-use can and will lead to data loss" |
| Non-idempotent aggregates fed by at-least-once input | Contentsquare: "all aggregations are not idempotent" |
| Wide table mixing inventory and counters, `ORDER BY tuple()` | yt-snmp-go-poller |

## (c) Implications for FlowSeer

Statements marked "inference" are my reasoning from the cited prior art, not something a source states for FlowSeer's case.

### Table per domain vs per signal vs generic

- Prior art with typed, known schemas uses per-domain tables with typed columns: Akvorado, FastNetMon, Snuba (errors, transactions, items), Glaber/Zabbix (per value type), ntopng alerts per entity. Generic designs (ntopng timeseries Map table, OTel per-metric-type tables, Telegraf per-metric-name tables, qryn series/samples) exist because the producer does not know its schema in advance, and each paid for it (map promotion mutations, full scans, ALTER per tag, label-set identity bugs).
- FlowSeer's observations are typed protobuf messages, so the domain schema is known at build time. Inference: one table per domain, typed columns for every field used in filters, grouping or ORDER BY, and a `Map(LowCardinality(String), String)` only for vendor-specific extras. Map keys should be sorted before insert if the map ever feeds identity or grouping (OTel #33634).
- A Prometheus-style series/samples split solves open-ended label sets. FlowSeer's entity identities (device, interface, radio, client MAC) are closed and known, so the "series table" role is played by inventory. Inference: put a compact, stable entity key (as Zabbix `itemid`, Akvorado `ExporterAddress, IfName`) in the fact table's ORDER BY and keep descriptive metadata in an inventory-derived table, rather than a generic label-hash series table.
- Split a domain into more than one table only when query shapes differ (Cloudflare built specialised tables per query shape; Uptrace split index and payload) or when the write shape differs (samples vs changes below).

### Samples vs changes

- Samples (periodic numeric, such as interface counters, CPU, memory, sensors, radio utilisation): every surveyed time-series store keeps raw samples in a MergeTree ordered by entity then time (Zabbix/Glaber `itemid, clock`, PromHouse/SigNoz/TimeSeries `fingerprint|id, ts`). Store raw counter values, not deltas, and compute rates at query time with reset handling (ntopng `argMax`/`lag`/`greatest(0, delta)`, clickstack window deltas). Inference: raw counters make at-least-once duplicates harmless for rate queries, because a duplicate sample has the same value and timestamp and contributes a zero delta.
- Rollups of samples: SummingMergeTree or AggregatingMergeTree tiers fed by MVs, each with its own TTL (Akvorado, SigNoz, Uptrace). For counters the useful rollup columns are last/min/max per bucket (SigNoz `last` via `anyLast`, Uptrace `gauge anyLast`), which are idempotent under duplicates; sum and count are not (Contentsquare warning). Avoid chained MVs feeding MVs on high-cardinality tables (SigNoz too-many-parts). Akvorado keeps high-cardinality columns out of rollups (`ClickHouseMainOnly`).
- Changes (state transitions such as oper status, alarm raise/clear, radio channel change): prior art keeps transitions as append-only rows in an event-style table ordered by tenant, entity, time (ntopng alerts per entity, Snuba errors) and keeps current state separately as a latest-state ReplacingMergeTree keyed by entity with a version column (Akvorado `exporters`, ntopng `assets`, PostHog persons). PostHog's lesson applies to the latest-state table: queries must use `argMax(..., version)` or `FINAL`, never assume merges happened.
- Inference: the ingest path needs to know which domain is which, because a sample domain tolerates duplicates by construction while a change domain needs an idempotency key (entity, attribute, observed_at) so duplicates can be removed at query time or by insert dedup.

### Snapshots vs changes for set-valued domains (FDB, LLDP/CDP neighbors, wireless clients)

- No surveyed public project stores FDB, LLDP/CDP or wireless client sessions in ClickHouse (three searches; unverified absence). The closest structural precedents are presence-interval tables: the TimeSeries engine tags table (`min_time`/`max_time` SimpleAggregateFunction per series id), SigNoz `trace_summary` and OTel `trace_id_ts` (min start, max end per id), and SigNoz/PromHouse series tables that write one row per entity per time bucket (hour or day) rather than per sample.
- Inference: for set-valued domains, store per-member presence per time bucket in an AggregatingMergeTree keyed by `(tenant, device, member key, bucket)` with `min(first_seen)` and `max(last_seen)`. Each poll's snapshot inserts one row per member; repeated or redelivered snapshots collapse because min and max are idempotent. "What was in the set at time T" becomes `first_seen <= T AND last_seen >= T - poll interval`. This avoids both a full snapshot per poll (volume grows with set size times poll rate) and a pure change log (needs a reliable baseline and ordered delivery, which JetStream at-least-once does not guarantee across redeliveries).
- Add an explicit change table only for transitions users query as events (a MAC moving port, a neighbor appearing or disappearing). Inference: derive those from presence gaps or from the collector's own diff, and treat them as change-domain rows with an idempotency key.

### Tenant isolation

- Shared tables with tenant first in ORDER BY are the dominant pattern (Snuba, PostHog, Uptrace, Cloudflare, Uber). Database per tenant (Coroot) multiplies tables, parts and migrations by tenant count. Tenant in the partition key caused Cloudflare's 160k-parts incident.
- Per-tenant retention without partition explosion: Snuba's `retention_days` column in a partition key with a small set of values, plus TTL `ts + toIntervalDay(retention_days)` and `ttl_only_drop_parts = 1`.
- Inference for FlowSeer: `ORDER BY (tenant, entity key ..., time)` on every domain table; partition by a coarse time unit (and optionally a retention class), never by tenant; enforce tenant scoping in the query layer (row policies or a mandatory predicate). Row policies and parameterized views were not researched here (unverified). Contentsquare and Snuba colocate an entity's data on one shard; with 1 shard x 2 replicas this does not apply yet, but a sharding key of tenant or entity hash keeps that option.

### Dedup under at-least-once delivery

- ReplacingMergeTree gives eventual dedup only (Snuba accepts this explicitly; PostHog calls it a mistake for events; SigNoz shows visible duplicates).
- Idempotent aggregate engines (AggregatingMergeTree with min/max/anyLast) make redelivery harmless (Uptrace datapoints, TimeSeries tags).
- Inference for FlowSeer: (1) sample tables plain MergeTree and duplicate-tolerant queries (raw counters, `argMax`/`max` per bucket); (2) presence tables idempotent by construction; (3) change and event tables (syslog, traps, alarms, transitions) carry a deterministic event id derived from the JetStream message identity and are read with dedup at query time where it matters. ClickHouse replicated insert-block dedup and `insert_deduplication_token` were not covered by any fetched source (unverified); they could make a redelivered batch a no-op if the consumer rebuilds identical batches, and need separate research before relying on them.

### Migrations tool (Go, 1 shard x 2 replicas)

| Option | Shape | Evidence |
|---|---|---|
| Versioned, ordered migrations with a status table in ClickHouse, `ON CLUSTER` + `alter_sync=2`, replicated engines from day one | Uptrace `chmigrate` (CollapsingMergeTree version table, `ADD COLUMN lock` as a lock), Snuba (ReplacingMergeTree status table, LOCAL/DIST targets), SigNoz (typed ops, sync/async split, waits on `distributed_ddl_queue` and `system.mutations`) | Works on clusters, but SigNoz notes (unverified) no cross-instance lock and stuck `in-progress` after crash |
| Declarative reconciler comparing wanted schema with `system.tables`/`system.columns` | Akvorado | Suits schemas derived from config or code; FlowSeer derives tables from protobuf, which fits this shape (inference) |
| Idempotent statements re-run at every start | HyperDX goose `WithNoVersioning`, Coroot, ntopng | Simple, but cannot express data moves |

Inference for FlowSeer: a Go tool that renders DDL from the protobuf domain definitions, always emits `Replicated*MergeTree` and `ON CLUSTER` with `alter_sync=2`, records applied steps in a replicated table, and runs as a single job before writers start (SigNoz moved to this after Job-ordering failures; the OTel exporter recommends `create_schema: false` for writers). Decide ORDER BY per table before the first deployment (qryn, Akvorado, OTel); plan ORDER BY changes as new-table-plus-backfill (SigNoz, Snuba `ATTACH PARTITION`). Use unique ZooKeeper paths per table (PostHog). Shrinking a TTL should not trigger a bulk delete at startup (ClickStack `materialize_ttl_after_modify = 0` when shrinking).

### Enrichment (site of a device, tenant, names)

- Denormalise at ingest, point in time: Akvorado moved GeoIP and network attributes from ClickHouse dictionaries to its outlet and writes them as LowCardinality columns; PostHog put person properties on events because JOINs caused memory errors, accepting that history is not rewritten when a person changes; Glaber writes host and item names on every row.
- Dictionaries are fine for small lookup tables (Akvorado `asns`, protocols) and poor for large ones (Akvorado `networks` memory).
- Inference for FlowSeer: write `site` (and similar inventory attributes needed for filtering) as a `LowCardinality` column on each fact row at ingest, so history shows the site at observation time; use a dictionary or join against an inventory table only for display attributes that should reflect the current value. Keep site out of ORDER BY unless queries lead with it; tenant then entity key fit better (SigNoz's resource-fingerprint lesson suggests a hierarchical key such as tenant, site, device can be placed in ORDER BY if site-scoped scans dominate).

### Ingest

- Akvorado dropped the Kafka engine for application inserts (ch-go native batches). Snuba: batch inserts are "critical". SigNoz drops batches on code 252 (too many partitions).
- Inference for FlowSeer: a Go JetStream consumer that batches per table (large batches, few inserts per second per table), acknowledges JetStream only after a successful insert, and keeps MVs off the raw path except for rollups.

## (d) Unverified

Source fidelity:
1. All DDL marked "reconstructed" or "paraphrase" (SigNoz, OTel exporter, ClickStack, Uptrace, Coroot, qryn, Snuba engine strings and transactions, Glaber cluster schema, eait-itig, FastNetMon `interface_metrics`, ntopng timeseries) is not byte-exact.
2. Akvorado DDL comes from test fixtures (`testdata/states/013.csv`, `002-cluster.csv`); that a fresh install renders the same today is unverified. Default `max-partitions` not read.
3. Zabbix `history_uint_schema.sh` fetched from master, not pinned to `c219427`; which released Zabbix version ships the ClickHouse backend is unverified (8.0 docs "In development").

SigNoz:
4. How SigNoz Cloud isolates tenants.
5. Reason for metrics v2 to v4; v2 column list from a search summary.
6. Maintainer wording that `time_series_v4` "can contain duplicates".
7. `manager.go` behaviour: no cross-instance lock, stuck `in-progress` after crash, DDL-queue wait returning nil after 10 attempts.
8. `WithReplication()` relying on server `default_replica_path`/`default_replica_name`.
9. Community-reported migrator failures on clusters.
10. Logs exporter setting its resource cache key before a successful send.
11. Chained MVs as the cause of "too many parts" (inference).

Other systems:
12. Akvorado outlet dedup behaviour on retries; no Akvorado blog post on ClickHouse lessons found.
13. pmacct: absence of a ClickHouse plugin checked only in the first 100k characters of `CONFIG-KEYS` and the README.
14. ElastiFlow: absence based on the sitemap and one outputs page.
15. ntopng: flows retention mechanism; Distributed table in cluster schema; docs (monthly partitions, TTL) vs code (daily partitions, partition drops).
16. FastNetMon `interface_metrics` source (flow or SNMP).
17. Telegraf 1.32 template-variable bug (from a clickstack comment only).
18. OTel `otel_traces` ORDER BY `toDateTime(Timestamp)` not checked against raw bytes; root cause of #35713.
19. ClickStack metrics ORDER BY: docs and 2.40.0 source disagree.
20. Uptrace: whether OSS v2.x uses native JSON (blog says yes, v2.0.3 migrations show arrays); completeness of the migrations listing.
21. qryn: `oid` meaning org id; FNV constants.
22. Coroot space-manager defaults.
23. Grafana: no storage product on ClickHouse rests on absence of evidence.
24. Snuba: `has(_tags_hash_map, ...)` query form; current transactions DDL after later migrations; reasons EAP attribute indexes were dropped; whether `errors_local` dedups on `deleted` today (docstring conflicts); staleness of MIGRATIONS.md single-node sentence.
25. PostHog: TTL on `sharded_events` elsewhere; `events_json` DDL details; handbook `cityHash64` vs code `sipHash64` sharding key.
26. Contentsquare tenant key placement and schema.
27. Cloudflare HTTP analytics primary key; ABR sample-table population; log analytics DDL; Ready-Analytics TTL after the partition change; capacity-estimation figures (search snippets).
28. Uber ORDER BY, partitioning, TTL, skip indexes.
29. Glaber trend computation; whether git.glaber.ru differs from the archived branch.
30. Absence of public ClickHouse schemas for LLDP/CDP, FDB/MAC and Wi-Fi client sessions (three searches only); Vector and Netdisco not checked.
31. Not researched at all: ClickHouse insert-block dedup and `insert_deduplication_token`, row policies, `Replicated` database engine, async inserts. All matter for FlowSeer's dedup, tenancy and migration choices.
32. Every "Inference" in section (c) is reasoning from cited prior art, not a sourced claim.
