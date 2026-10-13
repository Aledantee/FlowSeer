-- The device attributes a read joins to a row, mirrored from the registry. It
-- is a plain table: a read joins it, and no dictionary or dictionary source
-- exists. ver is the mirror time in nanoseconds, so the newest row wins at
-- merge and a read takes it with FINAL or argMax.
CREATE TABLE IF NOT EXISTS {{.DB}}.device_dim
(
    tenant_id   LowCardinality(String),
    device_id   UUID,
    name        String,
    location_id UUID,
    site_id     LowCardinality(String),
    ver         UInt64
)
{{.Engine "ReplacingMergeTree" "ver"}}
ORDER BY (tenant_id, device_id)
-- statement
-- Which tenants a ClickHouse user may read through a row policy. An operator
-- inserts the rows by hand.
CREATE TABLE IF NOT EXISTS {{.Control}}.user_tenants
(
    user      String,
    tenant_id String
)
{{.Engine "ReplacingMergeTree"}}
ORDER BY (user, tenant_id)
