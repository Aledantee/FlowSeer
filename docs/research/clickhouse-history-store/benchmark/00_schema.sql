-- FlowSeer history store benchmark schema, ClickHouse 26.8 single node.
--
-- One representative table set per pattern (decisions 1, 2, 8):
--   samples   interface_samples + interface_hourly, interface_hourly_by_time, interface_daily
--   changes   interface_changes
--   presence  fdb_presence + fdb_presence_by_mac, fdb_vlan_daily, set_walks
--   events    syslog + syslog_by_scope, syslog_daily
--   probes    probe_intervals + probe_hourly
--   dimensions device_dim_src/device_dim, device_site_src/device_site_dict, location_src/location_dict
--
-- Production uses the Replicated* engine of each table on a 1 x 2 cluster. A
-- single node without Keeper takes the plain engines. That changes insert-block
-- deduplication only (non-replicated MergeTree keeps non_replicated_deduplication_window = 0,
-- so every insert is stored); reads, merges, and MVs behave the same.
-- https://clickhouse.com/docs/engines/table-engines/mergetree-family/replication
--
-- Shared key columns and types are identical across tables (decision 2):
-- tenant_id LowCardinality(String), device_id UUID, interface_name LowCardinality(String),
-- mac UInt64, hour DateTime, day Date. site_id and software_version are stamped as-of on
-- every fact row (dossier 11 section 6 changes 1 and 2). Rollups use only idempotent
-- aggregates (decision 3; dossier 04 section 4.3). MV-fed second tables, no projections.
-- Retention classes per decision 6 (short 90 d / standard 1 y / long 3 y raw; hourly
-- 1/2/5 y; daily 2/3/5 y). TTL never fires in the benchmark because data sits inside the
-- last 14 days.

CREATE DATABASE IF NOT EXISTS flowseer;

-- ---------------------------------------------------------------------------
-- Samples pattern (dossier 04 section 3.1, 3.5; dossier 11 section 6 changes 1, 3)
-- ---------------------------------------------------------------------------

CREATE TABLE flowseer.interface_samples
(
    tenant_id             LowCardinality(String),
    device_id             UUID,
    interface_name        LowCardinality(String),
    ts                    DateTime        CODEC(Delta, ZSTD(1)),
    if_index              UInt32          CODEC(T64, ZSTD(1)),
    kind                  UInt8           CODEC(T64, ZSTD(1)),
    parent_interface_name LowCardinality(String),
    vlan_id               UInt16          CODEC(T64, ZSTD(1)),
    admin_status          UInt8           CODEC(T64, ZSTD(1)),
    oper_status           UInt8           CODEC(T64, ZSTD(1)),
    mtu                   UInt32          CODEC(T64, ZSTD(1)),
    description           String          CODEC(ZSTD(1)),
    last_change           DateTime        CODEC(Delta, ZSTD(1)),
    speed_bps             UInt64          CODEC(T64, ZSTD(1)),
    duplex                UInt8           CODEC(T64, ZSTD(1)),
    discontinuity_at      DateTime        CODEC(Delta, ZSTD(1)),
    in_bytes              UInt64          CODEC(Delta, ZSTD(1)),
    out_bytes             UInt64          CODEC(Delta, ZSTD(1)),
    in_unicast_packets    UInt64          CODEC(Delta, ZSTD(1)),
    out_unicast_packets   UInt64          CODEC(Delta, ZSTD(1)),
    in_multicast_packets  UInt64          CODEC(Delta, ZSTD(1)),
    out_multicast_packets UInt64          CODEC(Delta, ZSTD(1)),
    in_broadcast_packets  UInt64          CODEC(Delta, ZSTD(1)),
    out_broadcast_packets UInt64          CODEC(Delta, ZSTD(1)),
    in_errors             UInt64          CODEC(Delta, ZSTD(1)),
    out_errors            UInt64          CODEC(Delta, ZSTD(1)),
    in_discards           UInt64          CODEC(Delta, ZSTD(1)),
    out_discards          UInt64          CODEC(Delta, ZSTD(1)),
    present_mask          UInt32          CODEC(T64, ZSTD(1)),
    retention_class       Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    site_id               LowCardinality(String),
    software_version      LowCardinality(String)
)
ENGINE = ReplacingMergeTree
PARTITION BY toMonday(ts)
ORDER BY (tenant_id, device_id, interface_name, ts)
TTL ts + INTERVAL 90 DAY DELETE WHERE retention_class = 'short',
    ts + INTERVAL 1 YEAR DELETE WHERE retention_class = 'standard',
    ts + INTERVAL 3 YEAR DELETE WHERE retention_class = 'long'
SETTINGS ttl_only_drop_parts = 1,
         min_age_to_force_merge_seconds = 604800,
         min_age_to_force_merge_on_partition_only = 1;

-- Rollups: entity-first hourly, time-first hourly (monitoring scope queries, 04 D9),
-- entity-first daily. Each MV reads interface_samples (never chained).
-- software_version is a key column after the bucket (11 change 1); daily partitions are
-- monthly (11 change 3).

CREATE TABLE flowseer.interface_hourly
(
    tenant_id        LowCardinality(String),
    device_id        UUID,
    interface_name   LowCardinality(String),
    hour             DateTime,
    discontinuity_at DateTime,
    software_version LowCardinality(String),
    sample_minutes   SimpleAggregateFunction(groupBitOr, UInt64),
    first_ts         SimpleAggregateFunction(min, DateTime) CODEC(Delta, ZSTD(1)),
    last_ts          SimpleAggregateFunction(max, DateTime) CODEC(Delta, ZSTD(1)),
    in_bytes_min   SimpleAggregateFunction(min, UInt64) CODEC(Delta, ZSTD(1)),
    in_bytes_max   SimpleAggregateFunction(max, UInt64) CODEC(Delta, ZSTD(1)),
    out_bytes_min   SimpleAggregateFunction(min, UInt64) CODEC(Delta, ZSTD(1)),
    out_bytes_max   SimpleAggregateFunction(max, UInt64) CODEC(Delta, ZSTD(1)),
    in_unicast_packets_min   SimpleAggregateFunction(min, UInt64) CODEC(Delta, ZSTD(1)),
    in_unicast_packets_max   SimpleAggregateFunction(max, UInt64) CODEC(Delta, ZSTD(1)),
    out_unicast_packets_min   SimpleAggregateFunction(min, UInt64) CODEC(Delta, ZSTD(1)),
    out_unicast_packets_max   SimpleAggregateFunction(max, UInt64) CODEC(Delta, ZSTD(1)),
    in_errors_min   SimpleAggregateFunction(min, UInt64) CODEC(Delta, ZSTD(1)),
    in_errors_max   SimpleAggregateFunction(max, UInt64) CODEC(Delta, ZSTD(1)),
    out_errors_min   SimpleAggregateFunction(min, UInt64) CODEC(Delta, ZSTD(1)),
    out_errors_max   SimpleAggregateFunction(max, UInt64) CODEC(Delta, ZSTD(1)),
    in_discards_min   SimpleAggregateFunction(min, UInt64) CODEC(Delta, ZSTD(1)),
    in_discards_max   SimpleAggregateFunction(max, UInt64) CODEC(Delta, ZSTD(1)),
    out_discards_min   SimpleAggregateFunction(min, UInt64) CODEC(Delta, ZSTD(1)),
    out_discards_max   SimpleAggregateFunction(max, UInt64) CODEC(Delta, ZSTD(1)),
    speed_bps_max    SimpleAggregateFunction(max, UInt64) CODEC(Delta, ZSTD(1)),
    oper_status_min  SimpleAggregateFunction(min, UInt8),
    oper_status_max  SimpleAggregateFunction(max, UInt8),
    present_mask_any SimpleAggregateFunction(groupBitOr, UInt32),
    retention_class  SimpleAggregateFunction(max, Enum8('short' = 1, 'standard' = 2, 'long' = 3)),
    site_id          SimpleAggregateFunction(anyLast, LowCardinality(String))
)
ENGINE = AggregatingMergeTree
PARTITION BY toYYYYMM(hour)
ORDER BY (tenant_id, device_id, interface_name, hour, discontinuity_at, software_version)
TTL hour + INTERVAL 1 YEAR DELETE WHERE retention_class = 'short',
    hour + INTERVAL 2 YEAR DELETE WHERE retention_class = 'standard',
    hour + INTERVAL 5 YEAR DELETE WHERE retention_class = 'long'
SETTINGS ttl_only_drop_parts = 1;

CREATE MATERIALIZED VIEW flowseer.interface_hourly_mv TO flowseer.interface_hourly AS
SELECT tenant_id, device_id, interface_name, toStartOfHour(ts) AS hour, discontinuity_at, software_version,
       groupBitOr(bitShiftLeft(toUInt64(1), toMinute(ts))) AS sample_minutes, min(ts) AS first_ts, max(ts) AS last_ts,
       min(in_bytes) AS in_bytes_min, max(in_bytes) AS in_bytes_max,
       min(out_bytes) AS out_bytes_min, max(out_bytes) AS out_bytes_max,
       min(in_unicast_packets) AS in_unicast_packets_min, max(in_unicast_packets) AS in_unicast_packets_max,
       min(out_unicast_packets) AS out_unicast_packets_min, max(out_unicast_packets) AS out_unicast_packets_max,
       min(in_errors) AS in_errors_min, max(in_errors) AS in_errors_max,
       min(out_errors) AS out_errors_min, max(out_errors) AS out_errors_max,
       min(in_discards) AS in_discards_min, max(in_discards) AS in_discards_max,
       min(out_discards) AS out_discards_min, max(out_discards) AS out_discards_max,
       max(speed_bps) AS speed_bps_max, min(oper_status) AS oper_status_min, max(oper_status) AS oper_status_max,
       groupBitOr(present_mask) AS present_mask_any, max(retention_class) AS retention_class, anyLast(site_id) AS site_id
FROM flowseer.interface_samples
GROUP BY tenant_id, device_id, interface_name, hour, discontinuity_at, software_version;

CREATE TABLE flowseer.interface_hourly_by_time
(
    tenant_id        LowCardinality(String),
    device_id        UUID,
    interface_name   LowCardinality(String),
    hour             DateTime,
    discontinuity_at DateTime,
    software_version LowCardinality(String),
    sample_minutes   SimpleAggregateFunction(groupBitOr, UInt64),
    first_ts         SimpleAggregateFunction(min, DateTime) CODEC(Delta, ZSTD(1)),
    last_ts          SimpleAggregateFunction(max, DateTime) CODEC(Delta, ZSTD(1)),
    in_bytes_min   SimpleAggregateFunction(min, UInt64) CODEC(Delta, ZSTD(1)),
    in_bytes_max   SimpleAggregateFunction(max, UInt64) CODEC(Delta, ZSTD(1)),
    out_bytes_min   SimpleAggregateFunction(min, UInt64) CODEC(Delta, ZSTD(1)),
    out_bytes_max   SimpleAggregateFunction(max, UInt64) CODEC(Delta, ZSTD(1)),
    in_unicast_packets_min   SimpleAggregateFunction(min, UInt64) CODEC(Delta, ZSTD(1)),
    in_unicast_packets_max   SimpleAggregateFunction(max, UInt64) CODEC(Delta, ZSTD(1)),
    out_unicast_packets_min   SimpleAggregateFunction(min, UInt64) CODEC(Delta, ZSTD(1)),
    out_unicast_packets_max   SimpleAggregateFunction(max, UInt64) CODEC(Delta, ZSTD(1)),
    in_errors_min   SimpleAggregateFunction(min, UInt64) CODEC(Delta, ZSTD(1)),
    in_errors_max   SimpleAggregateFunction(max, UInt64) CODEC(Delta, ZSTD(1)),
    out_errors_min   SimpleAggregateFunction(min, UInt64) CODEC(Delta, ZSTD(1)),
    out_errors_max   SimpleAggregateFunction(max, UInt64) CODEC(Delta, ZSTD(1)),
    in_discards_min   SimpleAggregateFunction(min, UInt64) CODEC(Delta, ZSTD(1)),
    in_discards_max   SimpleAggregateFunction(max, UInt64) CODEC(Delta, ZSTD(1)),
    out_discards_min   SimpleAggregateFunction(min, UInt64) CODEC(Delta, ZSTD(1)),
    out_discards_max   SimpleAggregateFunction(max, UInt64) CODEC(Delta, ZSTD(1)),
    speed_bps_max    SimpleAggregateFunction(max, UInt64) CODEC(Delta, ZSTD(1)),
    oper_status_min  SimpleAggregateFunction(min, UInt8),
    oper_status_max  SimpleAggregateFunction(max, UInt8),
    present_mask_any SimpleAggregateFunction(groupBitOr, UInt32),
    retention_class  SimpleAggregateFunction(max, Enum8('short' = 1, 'standard' = 2, 'long' = 3)),
    site_id          SimpleAggregateFunction(anyLast, LowCardinality(String))
)
ENGINE = AggregatingMergeTree
PARTITION BY toYYYYMM(hour)
ORDER BY (tenant_id, hour, device_id, interface_name, discontinuity_at, software_version)
TTL hour + INTERVAL 1 YEAR DELETE WHERE retention_class = 'short',
    hour + INTERVAL 2 YEAR DELETE WHERE retention_class = 'standard',
    hour + INTERVAL 5 YEAR DELETE WHERE retention_class = 'long'
SETTINGS ttl_only_drop_parts = 1;

CREATE MATERIALIZED VIEW flowseer.interface_hourly_by_time_mv TO flowseer.interface_hourly_by_time AS
SELECT tenant_id, device_id, interface_name, toStartOfHour(ts) AS hour, discontinuity_at, software_version,
       groupBitOr(bitShiftLeft(toUInt64(1), toMinute(ts))) AS sample_minutes, min(ts) AS first_ts, max(ts) AS last_ts,
       min(in_bytes) AS in_bytes_min, max(in_bytes) AS in_bytes_max,
       min(out_bytes) AS out_bytes_min, max(out_bytes) AS out_bytes_max,
       min(in_unicast_packets) AS in_unicast_packets_min, max(in_unicast_packets) AS in_unicast_packets_max,
       min(out_unicast_packets) AS out_unicast_packets_min, max(out_unicast_packets) AS out_unicast_packets_max,
       min(in_errors) AS in_errors_min, max(in_errors) AS in_errors_max,
       min(out_errors) AS out_errors_min, max(out_errors) AS out_errors_max,
       min(in_discards) AS in_discards_min, max(in_discards) AS in_discards_max,
       min(out_discards) AS out_discards_min, max(out_discards) AS out_discards_max,
       max(speed_bps) AS speed_bps_max, min(oper_status) AS oper_status_min, max(oper_status) AS oper_status_max,
       groupBitOr(present_mask) AS present_mask_any, max(retention_class) AS retention_class, anyLast(site_id) AS site_id
FROM flowseer.interface_samples
GROUP BY tenant_id, device_id, interface_name, hour, discontinuity_at, software_version;

CREATE TABLE flowseer.interface_daily
(
    tenant_id        LowCardinality(String),
    device_id        UUID,
    interface_name   LowCardinality(String),
    day              Date,
    discontinuity_at DateTime,
    software_version LowCardinality(String),
    sample_hours     SimpleAggregateFunction(groupBitOr, UInt32),
    first_ts         SimpleAggregateFunction(min, DateTime) CODEC(Delta, ZSTD(1)),
    last_ts          SimpleAggregateFunction(max, DateTime) CODEC(Delta, ZSTD(1)),
    in_bytes_min   SimpleAggregateFunction(min, UInt64) CODEC(Delta, ZSTD(1)),
    in_bytes_max   SimpleAggregateFunction(max, UInt64) CODEC(Delta, ZSTD(1)),
    out_bytes_min   SimpleAggregateFunction(min, UInt64) CODEC(Delta, ZSTD(1)),
    out_bytes_max   SimpleAggregateFunction(max, UInt64) CODEC(Delta, ZSTD(1)),
    in_unicast_packets_min   SimpleAggregateFunction(min, UInt64) CODEC(Delta, ZSTD(1)),
    in_unicast_packets_max   SimpleAggregateFunction(max, UInt64) CODEC(Delta, ZSTD(1)),
    out_unicast_packets_min   SimpleAggregateFunction(min, UInt64) CODEC(Delta, ZSTD(1)),
    out_unicast_packets_max   SimpleAggregateFunction(max, UInt64) CODEC(Delta, ZSTD(1)),
    in_errors_min   SimpleAggregateFunction(min, UInt64) CODEC(Delta, ZSTD(1)),
    in_errors_max   SimpleAggregateFunction(max, UInt64) CODEC(Delta, ZSTD(1)),
    out_errors_min   SimpleAggregateFunction(min, UInt64) CODEC(Delta, ZSTD(1)),
    out_errors_max   SimpleAggregateFunction(max, UInt64) CODEC(Delta, ZSTD(1)),
    in_discards_min   SimpleAggregateFunction(min, UInt64) CODEC(Delta, ZSTD(1)),
    in_discards_max   SimpleAggregateFunction(max, UInt64) CODEC(Delta, ZSTD(1)),
    out_discards_min   SimpleAggregateFunction(min, UInt64) CODEC(Delta, ZSTD(1)),
    out_discards_max   SimpleAggregateFunction(max, UInt64) CODEC(Delta, ZSTD(1)),
    speed_bps_max    SimpleAggregateFunction(max, UInt64) CODEC(Delta, ZSTD(1)),
    oper_status_min  SimpleAggregateFunction(min, UInt8),
    oper_status_max  SimpleAggregateFunction(max, UInt8),
    present_mask_any SimpleAggregateFunction(groupBitOr, UInt32),
    retention_class  SimpleAggregateFunction(max, Enum8('short' = 1, 'standard' = 2, 'long' = 3)),
    site_id          SimpleAggregateFunction(anyLast, LowCardinality(String))
)
ENGINE = AggregatingMergeTree
PARTITION BY toYYYYMM(day)
ORDER BY (tenant_id, device_id, interface_name, day, discontinuity_at, software_version)
TTL day + INTERVAL 2 YEAR DELETE WHERE retention_class = 'short',
    day + INTERVAL 3 YEAR DELETE WHERE retention_class = 'standard',
    day + INTERVAL 5 YEAR DELETE WHERE retention_class = 'long'
SETTINGS ttl_only_drop_parts = 1;

CREATE MATERIALIZED VIEW flowseer.interface_daily_mv TO flowseer.interface_daily AS
SELECT tenant_id, device_id, interface_name, toDate(ts) AS day, discontinuity_at, software_version,
       groupBitOr(bitShiftLeft(toUInt32(1), toHour(ts))) AS sample_hours, min(ts) AS first_ts, max(ts) AS last_ts,
       min(in_bytes) AS in_bytes_min, max(in_bytes) AS in_bytes_max,
       min(out_bytes) AS out_bytes_min, max(out_bytes) AS out_bytes_max,
       min(in_unicast_packets) AS in_unicast_packets_min, max(in_unicast_packets) AS in_unicast_packets_max,
       min(out_unicast_packets) AS out_unicast_packets_min, max(out_unicast_packets) AS out_unicast_packets_max,
       min(in_errors) AS in_errors_min, max(in_errors) AS in_errors_max,
       min(out_errors) AS out_errors_min, max(out_errors) AS out_errors_max,
       min(in_discards) AS in_discards_min, max(in_discards) AS in_discards_max,
       min(out_discards) AS out_discards_min, max(out_discards) AS out_discards_max,
       max(speed_bps) AS speed_bps_max, min(oper_status) AS oper_status_min, max(oper_status) AS oper_status_max,
       groupBitOr(present_mask) AS present_mask_any, max(retention_class) AS retention_class, anyLast(site_id) AS site_id
FROM flowseer.interface_samples
GROUP BY tenant_id, device_id, interface_name, day, discontinuity_at, software_version;

-- ---------------------------------------------------------------------------
-- Changes pattern (dossier 02 section 12.2 with the dossier 04 section 3.2 edits:
-- entity is interface_name, attribute list widened, new_oper_status UInt8)
-- ---------------------------------------------------------------------------

CREATE TABLE flowseer.interface_changes
(
    tenant_id        LowCardinality(String),
    device_id        UUID,
    interface_name   LowCardinality(String),
    ts               DateTime          CODEC(Delta, ZSTD(1)),
    record_id        UUID,
    attribute        Enum8('oper_status' = 1, 'admin_status' = 2, 'speed_bps' = 3, 'duplex' = 4,
                           'mtu' = 5, 'description' = 6, 'if_index' = 7),
    old_value        String            CODEC(ZSTD(1)),
    new_value        String            CODEC(ZSTD(1)),
    new_oper_status  UInt8             CODEC(T64, ZSTD(1)),
    retention_class  Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    site_id          LowCardinality(String),
    software_version LowCardinality(String),
    INDEX idx_ts ts TYPE minmax GRANULARITY 4
)
ENGINE = ReplacingMergeTree
PARTITION BY toMonday(ts)
ORDER BY (tenant_id, device_id, interface_name, ts, record_id)
TTL ts + INTERVAL 90 DAY DELETE WHERE retention_class = 'short',
    ts + INTERVAL 1 YEAR DELETE WHERE retention_class = 'standard',
    ts + INTERVAL 3 YEAR DELETE WHERE retention_class = 'long'
SETTINGS ttl_only_drop_parts = 1,
         min_age_to_force_merge_seconds = 604800,
         min_age_to_force_merge_on_partition_only = 1;

-- ---------------------------------------------------------------------------
-- Presence pattern (dossier 06 sections 5.1, 5.2, 5.4; dossier 11 changes 2, 4, 8, 13;
-- decision 4: day bucket, attribute in key, walk markers with count and set hash)
-- retention_class leads the partition key so every part is single-class (dossier 07 E6
-- pattern); it is constant per tenant, so AggregatingMergeTree never folds two classes.
-- ---------------------------------------------------------------------------

CREATE TABLE flowseer.fdb_presence
(
    tenant_id        LowCardinality(String),
    device_id        UUID,
    day              Date,
    network_instance LowCardinality(String),
    vlan_id          UInt16            CODEC(T64, ZSTD(1)),
    mac              UInt64            CODEC(T64, ZSTD(1)),
    interface_name   LowCardinality(String),
    first_seen       SimpleAggregateFunction(min, DateTime) CODEC(Delta, ZSTD(1)),
    last_seen        SimpleAggregateFunction(max, DateTime) CODEC(Delta, ZSTD(1)),
    walks            SimpleAggregateFunction(sum, UInt64),
    kind             SimpleAggregateFunction(anyLast, Enum8('unspecified' = 0, 'other' = 1, 'dynamic' = 2, 'static' = 3, 'self' = 4, 'remote' = 5)),
    status           SimpleAggregateFunction(anyLast, Enum8('unspecified' = 0, 'active' = 1, 'invalid' = 2)),
    retention_class  Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    site_id          SimpleAggregateFunction(anyLast, LowCardinality(String)),
    software_version SimpleAggregateFunction(anyLast, LowCardinality(String)),
    INDEX idx_mac mac TYPE bloom_filter(0.01) GRANULARITY 1
)
ENGINE = AggregatingMergeTree
PARTITION BY (retention_class, toYYYYMM(day))
ORDER BY (tenant_id, device_id, day, network_instance, vlan_id, mac, interface_name)
TTL day + INTERVAL 90 DAY DELETE WHERE retention_class = 'short',
    day + INTERVAL 1 YEAR DELETE WHERE retention_class = 'standard',
    day + INTERVAL 3 YEAR DELETE WHERE retention_class = 'long'
SETTINGS ttl_only_drop_parts = 1, index_granularity = 8192;

CREATE TABLE flowseer.fdb_presence_by_mac
(
    tenant_id        LowCardinality(String),
    mac              UInt64            CODEC(T64, ZSTD(1)),
    day              Date,
    device_id        UUID,
    network_instance LowCardinality(String),
    vlan_id          UInt16            CODEC(T64, ZSTD(1)),
    interface_name   LowCardinality(String),
    first_seen       SimpleAggregateFunction(min, DateTime) CODEC(Delta, ZSTD(1)),
    last_seen        SimpleAggregateFunction(max, DateTime) CODEC(Delta, ZSTD(1)),
    retention_class  Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    site_id          SimpleAggregateFunction(anyLast, LowCardinality(String))
)
ENGINE = AggregatingMergeTree
PARTITION BY (retention_class, toYYYYMM(day))
ORDER BY (tenant_id, mac, day, device_id, network_instance, vlan_id, interface_name)
TTL day + INTERVAL 90 DAY DELETE WHERE retention_class = 'short',
    day + INTERVAL 1 YEAR DELETE WHERE retention_class = 'standard',
    day + INTERVAL 3 YEAR DELETE WHERE retention_class = 'long'
SETTINGS ttl_only_drop_parts = 1;

CREATE MATERIALIZED VIEW flowseer.fdb_presence_by_mac_mv TO flowseer.fdb_presence_by_mac AS
SELECT tenant_id, mac, day, device_id, network_instance, vlan_id, interface_name, retention_class,
       min(first_seen) AS first_seen, max(last_seen) AS last_seen, anyLast(site_id) AS site_id
FROM flowseer.fdb_presence
GROUP BY tenant_id, mac, day, device_id, network_instance, vlan_id, interface_name, retention_class;

-- Dossier 11 change 4: per-VLAN daily member sets for Q6 (FDB growth per VLAN per site).
CREATE TABLE flowseer.fdb_vlan_daily
(
    tenant_id       LowCardinality(String),
    device_id       UUID,
    vlan_id         UInt16,
    day             Date,
    members         AggregateFunction(uniqExact, UInt64),
    retention_class Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    site_id         SimpleAggregateFunction(anyLast, LowCardinality(String))
)
ENGINE = AggregatingMergeTree
PARTITION BY (retention_class, toYYYYMM(day))
ORDER BY (tenant_id, device_id, vlan_id, day)
TTL day + INTERVAL 2 YEAR DELETE WHERE retention_class = 'short',
    day + INTERVAL 3 YEAR DELETE WHERE retention_class = 'standard',
    day + INTERVAL 5 YEAR DELETE WHERE retention_class = 'long'
SETTINGS ttl_only_drop_parts = 1;

CREATE MATERIALIZED VIEW flowseer.fdb_vlan_daily_mv TO flowseer.fdb_vlan_daily AS
SELECT tenant_id, device_id, vlan_id, day, retention_class,
       uniqExactState(mac) AS members, anyLast(site_id) AS site_id
FROM flowseer.fdb_presence
GROUP BY tenant_id, device_id, vlan_id, day, retention_class;

-- Walk markers (dossier 06 section 5.4 renamed set_walks; decision 4 adds set_hash:
-- groupBitXor of cityHash64(vlan, mac, interface) over the walk's members, order independent).
CREATE TABLE flowseer.set_walks
(
    tenant_id        LowCardinality(String),
    device_id        UUID,
    kind             Enum8('fdb' = 1, 'lldp' = 2, 'cdp' = 3, 'neighbor' = 4),
    ts               DateTime          CODEC(Delta, ZSTD(1)),
    record_id        UUID,
    complete         Bool,
    members          UInt32            CODEC(T64, ZSTD(1)),
    set_hash         UInt64,
    reason           Enum8('walk' = 1, 'baseline_reset' = 2),
    retention_class  Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    site_id          LowCardinality(String),
    software_version LowCardinality(String)
)
ENGINE = ReplacingMergeTree
PARTITION BY toYYYYMM(ts)
ORDER BY (tenant_id, device_id, kind, ts, record_id)
TTL ts + INTERVAL 90 DAY DELETE WHERE retention_class = 'short',
    ts + INTERVAL 1 YEAR DELETE WHERE retention_class = 'standard',
    ts + INTERVAL 3 YEAR DELETE WHERE retention_class = 'long'
SETTINGS ttl_only_drop_parts = 1;

-- ---------------------------------------------------------------------------
-- Events pattern (dossier 07 section 1.3 E5 to E17; dossier 11 changes 1 and 5)
-- Text index: GA since 26.2, no setting required; the page states "text indexes use an
-- infinite granularity (100 million)" and "An explicitly specified index granularity is
-- ignored", so the dossier's GRANULARITY 64 is dropped.
-- https://clickhouse.com/docs/engines/table-engines/mergetree-family/invertedindexes
-- The dossier's idx_sd (text index on the ALIAS column sd_items) is left out: indexing an
-- ALIAS column is not confirmed for 26.8 and the generator writes no structured data.
-- ---------------------------------------------------------------------------

CREATE TABLE flowseer.syslog
(
    tenant_id        LowCardinality(String),
    device_id        UUID,
    ts               DateTime64(3)         CODEC(Delta(8), ZSTD(1)),
    record_id        UUID,
    sent_at          DateTime64(3)         CODEC(Delta(8), ZSTD(1)),
    severity         Enum8('none' = -1, 'emerg' = 0, 'alert' = 1, 'crit' = 2, 'err' = 3,
                           'warning' = 4, 'notice' = 5, 'info' = 6, 'debug' = 7),
    facility         Enum8('none' = -1, 'kern' = 0, 'user' = 1, 'mail' = 2, 'daemon' = 3, 'auth' = 4,
                           'syslog' = 5, 'lpr' = 6, 'news' = 7, 'uucp' = 8, 'cron' = 9, 'authpriv' = 10,
                           'ftp' = 11, 'ntp' = 12, 'audit' = 13, 'alert' = 14, 'clock' = 15,
                           'local0' = 16, 'local1' = 17, 'local2' = 18, 'local3' = 19,
                           'local4' = 20, 'local5' = 21, 'local6' = 22, 'local7' = 23),
    hostname         LowCardinality(String),
    app_name         LowCardinality(String),
    proc_id          LowCardinality(String),
    msg_id           LowCardinality(String),
    sd_ids           Array(LowCardinality(String)),
    sd_param_sd_id   Array(LowCardinality(String)),
    sd_param_name    Array(LowCardinality(String)),
    sd_param_value   Array(String)          CODEC(ZSTD(1)),
    sd_items         Array(String) ALIAS arrayMap((e, n, v) -> concat(e, '/', n, '=', v),
                                                  sd_param_sd_id, sd_param_name, sd_param_value),
    message          String                 CODEC(ZSTD(1)),
    message_truncated Bool,
    message_is_utf8  Bool MATERIALIZED isValidUTF8(message),
    source_ip        IPv6,
    vendor_tag       LowCardinality(String) MATERIALIZED
        extract(message, '^%%?[0-9]*([A-Z0-9_]+[-/][0-7][-/][A-Z0-9_]+)'),
    vendor_family    LowCardinality(String),
    vendor_module    LowCardinality(String),
    vendor_mnemonic  LowCardinality(String),
    vendor_event_id  LowCardinality(String),
    vendor_severity  Int8,
    vendor_sequence  UInt64 CODEC(T64, ZSTD(1)),
    log_format       Enum8('unknown' = 0, 'rfc5424' = 1, 'rfc3164' = 2),
    device_clock     Enum8('absolute' = 1, 'no_year' = 2, 'uptime' = 3, 'none' = 4),
    parse_status     Enum8('ok' = 1, 'partial' = 2, 'failed' = 3),
    transport        Enum8('udp' = 1, 'tcp' = 2, 'tls' = 3),
    transport_authenticated Bool,
    repeat_count     UInt32 DEFAULT 1       CODEC(T64, ZSTD(1)),
    binding_id       UUID,
    edge_id          UUID,
    site_id          LowCardinality(String),
    software_version LowCardinality(String),
    retention_class  Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    INDEX idx_msg  message    TYPE text(tokenizer = 'splitByNonAlpha', preprocessor = lower(message)),
    INDEX idx_tag  vendor_tag TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_sev  severity   TYPE minmax GRANULARITY 1
)
ENGINE = ReplacingMergeTree
PARTITION BY (retention_class, toDate(ts))
ORDER BY (tenant_id, device_id, ts, record_id)
TTL toDateTime(ts) + INTERVAL 90 DAY DELETE WHERE retention_class = 'short',
    toDateTime(ts) + INTERVAL 1 YEAR DELETE WHERE retention_class = 'standard',
    toDateTime(ts) + INTERVAL 3 YEAR DELETE WHERE retention_class = 'long',
    toDateTime(ts) + INTERVAL 14 DAY RECOMPRESS CODEC(ZSTD(3))
SETTINGS ttl_only_drop_parts = 1, index_granularity = 8192;

CREATE TABLE flowseer.syslog_by_scope
(
    tenant_id       LowCardinality(String),
    site_id         LowCardinality(String),
    ts              DateTime64(3) CODEC(Delta(8), ZSTD(1)),
    device_id       UUID,
    record_id       UUID,
    severity        Enum8('none' = -1, 'emerg' = 0, 'alert' = 1, 'crit' = 2, 'err' = 3,
                          'warning' = 4, 'notice' = 5, 'info' = 6, 'debug' = 7),
    vendor_tag      LowCardinality(String),
    retention_class Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    INDEX idx_sev severity TYPE minmax GRANULARITY 1
)
ENGINE = ReplacingMergeTree
PARTITION BY (retention_class, toDate(ts))
ORDER BY (tenant_id, site_id, ts, device_id, record_id)
TTL toDateTime(ts) + INTERVAL 90 DAY DELETE WHERE retention_class = 'short',
    toDateTime(ts) + INTERVAL 1 YEAR DELETE WHERE retention_class = 'standard',
    toDateTime(ts) + INTERVAL 3 YEAR DELETE WHERE retention_class = 'long'
SETTINGS ttl_only_drop_parts = 1;

CREATE MATERIALIZED VIEW flowseer.syslog_by_scope_mv TO flowseer.syslog_by_scope AS
SELECT tenant_id, site_id, ts, device_id, record_id, severity, vendor_tag, retention_class
FROM flowseer.syslog;

-- Dossier 11 change 5. SummingMergeTree is not idempotent under redelivery past the
-- dedup window (dossier 07 E20 accepts that for charts); the dedup step measures the drift.
CREATE TABLE flowseer.syslog_daily
(
    tenant_id        LowCardinality(String),
    device_id        UUID,
    severity         Enum8('none' = -1, 'emerg' = 0, 'alert' = 1, 'crit' = 2, 'err' = 3,
                           'warning' = 4, 'notice' = 5, 'info' = 6, 'debug' = 7),
    vendor_tag       LowCardinality(String),
    day              Date,
    messages         UInt64,
    site_id          LowCardinality(String),
    software_version LowCardinality(String),
    retention_class  Enum8('short' = 1, 'standard' = 2, 'long' = 3)
)
ENGINE = SummingMergeTree(messages)
PARTITION BY (retention_class, toYYYYMM(day))
ORDER BY (tenant_id, device_id, severity, vendor_tag, day)
TTL day + INTERVAL 1 YEAR DELETE WHERE retention_class = 'short',
    day + INTERVAL 3 YEAR DELETE WHERE retention_class = 'standard',
    day + INTERVAL 7 YEAR DELETE WHERE retention_class = 'long'
SETTINGS ttl_only_drop_parts = 1;

CREATE MATERIALIZED VIEW flowseer.syslog_daily_mv TO flowseer.syslog_daily AS
SELECT tenant_id, device_id, severity, vendor_tag, toDate(ts) AS day, retention_class,
       sum(repeat_count) AS messages, anyLast(site_id) AS site_id,
       anyLast(software_version) AS software_version
FROM flowseer.syslog
GROUP BY tenant_id, device_id, severity, vendor_tag, day, retention_class;

-- ---------------------------------------------------------------------------
-- Probes pattern (dossier 10 section 4.3; dossier 11 changes 1 and 2)
-- The dossier's single MV left two open points (ARRAY JOIN in an MV; intervals_lost lost
-- by the WHERE). Two MVs into one AggregatingMergeTree settle both: probe_hourly_q_mv
-- explodes the RTT array for the quantile state only, probe_hourly_n_mv sums counts and
-- RTT extremes per interval row without ARRAY JOIN. Each MV writes every column
-- explicitly; rtt_min_us uses 4294967295 as "no sample" so a count-only row never
-- lowers the minimum.
-- ---------------------------------------------------------------------------

CREATE TABLE flowseer.probe_intervals
(
    tenant_id        LowCardinality(String),
    device_id        UUID,
    interface_name   LowCardinality(String),
    target_ip        IPv6,
    interval_start   DateTime            CODEC(Delta, ZSTD(1)),
    interval_s       UInt16              CODEC(T64, ZSTD(1)),
    prober           Enum8('edge' = 1, 'device' = 2, 'controller' = 3),
    edge_id          LowCardinality(String),
    sent             UInt16              CODEC(T64, ZSTD(1)),
    received         UInt16              CODEC(T64, ZSTD(1)),
    rtt_us           Array(UInt32)       CODEC(T64, ZSTD(1)),
    latency_us       UInt32              CODEC(T64, ZSTD(1)),
    jitter_us        UInt32              CODEC(T64, ZSTD(1)),
    loss_bp          UInt16              CODEC(T64, ZSTD(1)),
    present_mask     UInt8,
    retention_class  Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    site_id          LowCardinality(String),
    software_version LowCardinality(String)
)
ENGINE = ReplacingMergeTree
PARTITION BY toMonday(interval_start)
ORDER BY (tenant_id, device_id, interface_name, target_ip, interval_start)
TTL interval_start + INTERVAL 90 DAY DELETE WHERE retention_class = 'short',
    interval_start + INTERVAL 1 YEAR DELETE WHERE retention_class = 'standard',
    interval_start + INTERVAL 3 YEAR DELETE WHERE retention_class = 'long'
SETTINGS ttl_only_drop_parts = 1, min_age_to_force_merge_seconds = 604800,
         min_age_to_force_merge_on_partition_only = 1;

CREATE TABLE flowseer.probe_hourly
(
    tenant_id        LowCardinality(String),
    device_id        UUID,
    interface_name   LowCardinality(String),
    target_ip        IPv6,
    hour             DateTime,
    software_version LowCardinality(String),
    sent             SimpleAggregateFunction(sum, UInt64),
    received         SimpleAggregateFunction(sum, UInt64),
    rtt_min_us       SimpleAggregateFunction(min, UInt32),
    rtt_max_us       SimpleAggregateFunction(max, UInt32),
    rtt_sum_us       SimpleAggregateFunction(sum, UInt64),
    rtt_ms_q         AggregateFunction(quantilesTiming(0.5, 0.95, 0.99), UInt32),
    intervals_lost   SimpleAggregateFunction(sum, UInt64),
    retention_class  SimpleAggregateFunction(max, Enum8('short' = 1, 'standard' = 2, 'long' = 3)),
    site_id          SimpleAggregateFunction(anyLast, LowCardinality(String))
)
ENGINE = AggregatingMergeTree
PARTITION BY toYYYYMM(hour)
ORDER BY (tenant_id, device_id, interface_name, target_ip, hour, software_version)
TTL hour + INTERVAL 1 YEAR DELETE WHERE retention_class = 'short',
    hour + INTERVAL 2 YEAR DELETE WHERE retention_class = 'standard',
    hour + INTERVAL 5 YEAR DELETE WHERE retention_class = 'long'
SETTINGS ttl_only_drop_parts = 1;

CREATE MATERIALIZED VIEW flowseer.probe_hourly_n_mv TO flowseer.probe_hourly AS
SELECT tenant_id, device_id, interface_name, target_ip,
       toStartOfHour(interval_start) AS hour, software_version,
       sum(toUInt64(sent)) AS sent, sum(toUInt64(received)) AS received,
       min(if(empty(rtt_us), toUInt32(4294967295), arrayMin(rtt_us))) AS rtt_min_us,
       max(if(empty(rtt_us), toUInt32(0), arrayMax(rtt_us))) AS rtt_max_us,
       sum(toUInt64(arraySum(rtt_us))) AS rtt_sum_us,
       sum(toUInt64(p.received = 0)) AS intervals_lost,
       max(retention_class) AS retention_class, anyLast(site_id) AS site_id
FROM flowseer.probe_intervals AS p
GROUP BY tenant_id, device_id, interface_name, target_ip, hour, software_version;

CREATE MATERIALIZED VIEW flowseer.probe_hourly_q_mv TO flowseer.probe_hourly AS
SELECT tenant_id, device_id, interface_name, target_ip,
       toStartOfHour(interval_start) AS hour, software_version,
       toUInt64(0) AS sent, toUInt64(0) AS received,
       toUInt32(4294967295) AS rtt_min_us, toUInt32(0) AS rtt_max_us, toUInt64(0) AS rtt_sum_us,
       quantilesTimingState(0.5, 0.95, 0.99)(rtt_ms) AS rtt_ms_q,
       toUInt64(0) AS intervals_lost,
       max(retention_class) AS retention_class, anyLast(site_id) AS site_id
FROM flowseer.probe_intervals
ARRAY JOIN arrayMap(x -> toUInt32(intDiv(x, 1000)), rtt_us) AS rtt_ms
GROUP BY tenant_id, device_id, interface_name, target_ip, hour, software_version;

-- ---------------------------------------------------------------------------
-- Dimensions (dossier 11 section 2.4; dossier 09 section 9 device_site_dict).
-- Mirror tables are ReplacingMergeTree(ver), read with FINAL by the dictionary source.
-- The local CLICKHOUSE source authenticates as the default user; run.sh starts the
-- container with password 'bench'.
-- https://clickhouse.com/docs/sql-reference/dictionaries
-- ---------------------------------------------------------------------------

CREATE TABLE flowseer.device_dim_src
(
    tenant_id        LowCardinality(String),
    device_id        UUID,
    vendor           LowCardinality(String),
    model            LowCardinality(String),
    role             LowCardinality(String),
    software_version LowCardinality(String),
    site_id          LowCardinality(String),
    hostname         String,
    ver              UInt64
)
ENGINE = ReplacingMergeTree(ver)
ORDER BY (tenant_id, device_id);

CREATE DICTIONARY flowseer.device_dim
(
    tenant_id String,
    device_id UUID,
    vendor String,
    model String,
    role String,
    software_version String,
    site_id String
)
PRIMARY KEY tenant_id, device_id
SOURCE(CLICKHOUSE(
    QUERY 'SELECT toString(tenant_id) AS tenant_id, device_id, toString(vendor) AS vendor, toString(model) AS model, toString(role) AS role, toString(software_version) AS software_version, toString(site_id) AS site_id FROM flowseer.device_dim_src FINAL'
    USER 'default' PASSWORD 'bench'))
LAYOUT(COMPLEX_KEY_HASHED())
LIFETIME(MIN 300 MAX 360);

CREATE TABLE flowseer.device_site_src
(
    tenant_id       LowCardinality(String),
    device_id       UUID,
    site_id         LowCardinality(String),
    placement_id    UUID,
    effective_from  DateTime,
    effective_until DateTime,
    ver             UInt64
)
ENGINE = ReplacingMergeTree(ver)
ORDER BY (tenant_id, device_id, placement_id);

-- "If the range_max is NULL, the range is open" (range-hashed layout page, quoted in
-- dossier 09 section 9.1).
CREATE DICTIONARY flowseer.device_site_dict
(
    tenant_id String,
    device_id UUID,
    site_id String,
    effective_from DateTime,
    effective_until Nullable(DateTime)
)
PRIMARY KEY tenant_id, device_id
SOURCE(CLICKHOUSE(
    QUERY 'SELECT toString(tenant_id) AS tenant_id, device_id, toString(site_id) AS site_id, effective_from, if(effective_until = 0, NULL, effective_until) AS effective_until FROM flowseer.device_site_src FINAL'
    USER 'default' PASSWORD 'bench'))
LAYOUT(COMPLEX_KEY_RANGE_HASHED(range_lookup_strategy 'max'))
RANGE(MIN effective_from MAX effective_until)
LIFETIME(MIN 300 MAX 360);

-- Location hierarchy region > site. Hierarchical dictionaries need a numeric key:
-- "ClickHouse supports hierarchical dictionaries with a numeric key", example uses
-- LAYOUT(HASHED()) and `parent_region UInt64 DEFAULT 0 HIERARCHICAL`
-- (https://clickhouse.com/docs/reference/statements/create/dictionary/layouts/hierarchical).
-- The key is cityHash64(tenant_id, site_id) for sites and cityHash64(tenant_id, region)
-- for regions, so the tenant stays part of the identity without a composite key.
CREATE TABLE flowseer.location_src
(
    tenant_id   LowCardinality(String),
    location_id UInt64,
    parent_id   UInt64,
    kind        LowCardinality(String),
    name        String,
    ver         UInt64
)
ENGINE = ReplacingMergeTree(ver)
ORDER BY (tenant_id, location_id);

CREATE DICTIONARY flowseer.location_dict
(
    location_id UInt64,
    parent_id   UInt64 DEFAULT 0 HIERARCHICAL,
    tenant_id   String DEFAULT '',
    kind        String DEFAULT '',
    name        String DEFAULT ''
)
PRIMARY KEY location_id
SOURCE(CLICKHOUSE(
    QUERY 'SELECT location_id, parent_id, toString(tenant_id) AS tenant_id, toString(kind) AS kind, name FROM flowseer.location_src FINAL'
    USER 'default' PASSWORD 'bench'))
LAYOUT(HASHED())
LIFETIME(MIN 300 MAX 360);
