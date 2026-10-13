-- The events pattern: one row per syslog message, deduplicated at merge on
-- the record id. ts is the received time. severity and facility hold the
-- protobuf enum number, and a bit of present says whether the message carried
-- one (bit 0 severity, bit 1 facility, bit 2 sent_at, bit 3 source_ip), since
-- no column is Nullable. retention_class holds the tenant's RetentionClass
-- enum number (1 short, 2 standard, 3 long) and leads the partition key, so
-- a part holds one class and expires whole.
CREATE TABLE IF NOT EXISTS {{.DB}}.syslog
(
    tenant_id         LowCardinality(String),
    device_id         UUID,
    ts                DateTime64(3) CODEC(Delta(8), ZSTD(1)),
    record_id         UUID,
    observed_at       DateTime64(3) CODEC(Delta(8), ZSTD(1)),
    sent_at           DateTime64(3) CODEC(Delta(8), ZSTD(1)),
    severity          UInt8,
    facility          UInt8,
    present           UInt8,
    hostname          LowCardinality(String),
    app_name          LowCardinality(String),
    proc_id           LowCardinality(String),
    msg_id            LowCardinality(String),
    sd_ids            Array(LowCardinality(String)),
    sd_param_sd_id    Array(LowCardinality(String)),
    sd_param_name     Array(LowCardinality(String)),
    sd_param_value    Array(String) CODEC(ZSTD(1)),
    message           String CODEC(ZSTD(1)),
    message_truncated Bool,
    message_is_utf8   Bool MATERIALIZED isValidUTF8(message),
    source_ip         IPv6,
    vendor_tag        LowCardinality(String) MATERIALIZED
        extract(message, '^%%?[0-9]*([A-Z0-9_]+[-/][0-7][-/][A-Z0-9_]+)'),
    location_id       UUID,
    site_id           LowCardinality(String),
    software_version  LowCardinality(String),
    retention_class   UInt8,
    INDEX idx_msg message TYPE text(tokenizer = 'splitByNonAlpha', preprocessor = lower(message)),
    INDEX idx_tag vendor_tag TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_sev severity TYPE minmax GRANULARITY 1
)
{{.Engine "ReplacingMergeTree"}}
PARTITION BY (retention_class, toMonday(ts))
ORDER BY (tenant_id, device_id, ts, record_id)
TTL toDateTime(ts) + INTERVAL 90 DAY DELETE WHERE retention_class = 1,
    toDateTime(ts) + INTERVAL 1 YEAR DELETE WHERE retention_class = 2,
    toDateTime(ts) + INTERVAL 3 YEAR DELETE WHERE retention_class = 3
SETTINGS ttl_only_drop_parts = 1
