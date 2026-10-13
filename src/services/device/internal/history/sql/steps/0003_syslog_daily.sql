-- Messages per device, severity, vendor tag, and day. A summing table counts
-- a redelivery past the insert deduplication window twice, so a reader of
-- these counts states that error. Severity 255 means the message carried no
-- priority. The view reads a subquery so that no aggregate shares a name with
-- a column of the source table, which ClickHouse 26.8 resolves to the
-- aggregate.
CREATE TABLE IF NOT EXISTS {{.DB}}.syslog_daily
(
    tenant_id        LowCardinality(String),
    device_id        UUID,
    severity         UInt8,
    vendor_tag       LowCardinality(String),
    day              Date,
    messages         UInt64,
    location_id      UUID,
    site_id          LowCardinality(String),
    software_version LowCardinality(String),
    retention_class  UInt8
)
{{.Engine "SummingMergeTree" "messages"}}
PARTITION BY (retention_class, toYYYYMM(day))
ORDER BY (tenant_id, device_id, severity, vendor_tag, day)
TTL day + INTERVAL 2 YEAR DELETE WHERE retention_class = 1,
    day + INTERVAL 3 YEAR DELETE WHERE retention_class = 2,
    day + INTERVAL 5 YEAR DELETE WHERE retention_class = 3
SETTINGS ttl_only_drop_parts = 1
-- statement
CREATE MATERIALIZED VIEW IF NOT EXISTS {{.DB}}.syslog_daily_mv TO {{.DB}}.syslog_daily
DEFINER = CURRENT_USER SQL SECURITY DEFINER AS
SELECT
    tenant_id,
    device_id,
    severity_key AS severity,
    vendor_tag,
    day,
    count() AS messages,
    anyLast(location_key) AS location_id,
    anyLast(site_key) AS site_id,
    anyLast(version_key) AS software_version,
    retention_class
FROM
(
    SELECT
        tenant_id,
        device_id,
        if(bitAnd(present, 1) = 1, severity, 255) AS severity_key,
        vendor_tag,
        toDate(ts) AS day,
        location_id AS location_key,
        site_id AS site_key,
        software_version AS version_key,
        retention_class
    FROM {{.DB}}.syslog
)
GROUP BY tenant_id, device_id, severity_key, vendor_tag, day, retention_class
