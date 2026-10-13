-- A second ordering of the syslog keys for a read by location. It holds no
-- message text: a read selects the newest keys here and fetches their rows
-- from syslog.
CREATE TABLE IF NOT EXISTS {{.DB}}.syslog_by_location
(
    tenant_id       LowCardinality(String),
    location_id     UUID,
    ts              DateTime64(3) CODEC(Delta(8), ZSTD(1)),
    device_id       UUID,
    record_id       UUID,
    severity        UInt8,
    present         UInt8,
    vendor_tag      LowCardinality(String),
    retention_class UInt8,
    INDEX idx_sev severity TYPE minmax GRANULARITY 1
)
{{.Engine "ReplacingMergeTree"}}
PARTITION BY (retention_class, toMonday(ts))
ORDER BY (tenant_id, location_id, ts, device_id, record_id)
TTL toDateTime(ts) + INTERVAL 90 DAY DELETE WHERE retention_class = 1,
    toDateTime(ts) + INTERVAL 1 YEAR DELETE WHERE retention_class = 2,
    toDateTime(ts) + INTERVAL 3 YEAR DELETE WHERE retention_class = 3
SETTINGS ttl_only_drop_parts = 1
-- statement
-- The view runs with the privileges of the user that created it, so the
-- writer needs INSERT on syslog and nothing else.
CREATE MATERIALIZED VIEW IF NOT EXISTS {{.DB}}.syslog_by_location_mv TO {{.DB}}.syslog_by_location
DEFINER = CURRENT_USER SQL SECURITY DEFINER AS
SELECT tenant_id, location_id, ts, device_id, record_id, severity, present, vendor_tag, retention_class
FROM {{.DB}}.syslog
