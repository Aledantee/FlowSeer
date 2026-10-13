-- Runs on every migration, before the numbered steps. Each statement is
-- idempotent. The control database holds the migrator's own record and the
-- user to tenant mapping, so a row policy on the history database never
-- applies to the table the policy itself reads.
CREATE DATABASE IF NOT EXISTS {{.DB}} {{.OnCluster}} {{.DatabaseEngine}}
-- statement
CREATE DATABASE IF NOT EXISTS {{.Control}} {{.OnCluster}} {{.DatabaseEngine}}
-- statement
CREATE TABLE IF NOT EXISTS {{.Control}}.schema_steps
(
    step       UInt32,
    name       String,
    applied_at DateTime64(3) DEFAULT now64(3)
)
{{.Engine "ReplacingMergeTree" "applied_at"}}
ORDER BY step
