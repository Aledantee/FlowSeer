-- The service read path. Every limit is CONST so a client cannot raise it, and
-- an overrun throws instead of returning a partial result.
CREATE SETTINGS PROFILE IF NOT EXISTS svc_reader {{.OnCluster}} SETTINGS
    readonly = 2,
    max_execution_time = 30 CONST,
    max_rows_to_read = 10000000 CONST,
    max_memory_usage = 2147483648 CONST,
    max_threads = 8 CONST,
    read_overflow_mode = 'throw' CONST,
    timeout_overflow_mode = 'throw' CONST,
    workload = 'monitoring' CONST
    TO {{.Reader}}
-- statement
CREATE SETTINGS PROFILE IF NOT EXISTS svc_writer {{.OnCluster}} SETTINGS
    workload = 'ingestion' CONST
    TO {{.Writer}}
-- statement
-- Fleet analytics. Limits are MAX or CONST bounded and sized for a host with
-- 16 GiB of memory or more.
CREATE SETTINGS PROFILE IF NOT EXISTS analyst {{.OnCluster}} SETTINGS
    readonly = 2,
    max_execution_time = 600 MAX 600,
    timeout_overflow_mode = 'throw' CONST,
    max_memory_usage = 8589934592 MAX 8589934592,
    max_memory_usage_for_user = 12884901888 MAX 12884901888,
    max_rows_to_read = 2000000000 MAX 2000000000,
    read_overflow_mode = 'throw' CONST,
    max_threads = 8 MAX 8,
    max_concurrent_queries_for_user = 4 CONST,
    max_bytes_ratio_before_external_group_by = 0.3,
    max_bytes_ratio_before_external_sort = 0.3,
    optimize_aggregation_in_order = 1,
    join_algorithm = 'direct,parallel_hash,grace_hash',
    max_bytes_in_join = 1000000000,
    join_overflow_mode = 'throw',
    use_query_cache = 0,
    use_query_condition_cache = 0,
    enable_parallel_replicas = 0,
    workload = 'analytics' CONST
    TO {{.Analyst}}
-- statement
GRANT {{.OnCluster}} INSERT ON {{.DB}}.syslog TO {{.Writer}}
-- statement
GRANT {{.OnCluster}} INSERT ON {{.DB}}.device_dim TO {{.Writer}}
-- statement
GRANT {{.OnCluster}} SELECT ON {{.DB}}.* TO {{.Reader}}
-- statement
-- The analyst gets SELECT table by table, so a table added later stays closed
-- to it until a step grants it together with its row policy.
GRANT {{.OnCluster}} SELECT ON {{.DB}}.syslog TO {{.Analyst}}
-- statement
GRANT {{.OnCluster}} SELECT ON {{.DB}}.syslog_by_location TO {{.Analyst}}
-- statement
GRANT {{.OnCluster}} SELECT ON {{.DB}}.syslog_daily TO {{.Analyst}}
-- statement
GRANT {{.OnCluster}} SELECT ON {{.DB}}.device_dim TO {{.Analyst}}
-- statement
GRANT {{.OnCluster}} SELECT ON {{.Control}}.user_tenants TO {{.Analyst}}
