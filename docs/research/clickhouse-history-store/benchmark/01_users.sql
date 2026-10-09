-- Users, profiles, workloads, row policies for the benchmark (decision 5; dossier 02 F56;
-- dossier 11 section 5.1). Run as the default user with access management enabled
-- (run.sh sets CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT=1).
--
-- Limits are scaled to the 7 GiB container: 25 % of RAM = 1.8 GB, 40 % = 2.8 GB,
-- 50 % = 3.5 GB. Half the 11 cores = 5 threads.
--
-- Items 26.8 might reject are marked "MAY REJECT" with the reason. Remove the line and
-- rerun if the server refuses it; the measured shapes do not depend on them.

-- ---------------------------------------------------------------------------
-- Workload scheduling (https://clickhouse.com/docs/operations/workload-scheduling)
-- "To enable it you have to create resources that will be used for scheduling and at
-- least one workload." Declaring a CPU resource "disables effect of
-- concurrent_threads_soft_limit_num" and the workload's max_concurrent_threads applies
-- instead. "Create a CREATE WORKLOAD default IN all to automatically apply limits to all
-- queries"; the generator and the driver's own queries run in `default` or `ingestion`.
-- The `workload` setting "can only refer to leaf workloads".
-- Sibling priority: "lower value means higher priority" (dossier 11 section 5.1 quote).
-- The benchmark runs one query at a time, so scheduling changes no measured latency except
-- through max_concurrent_threads on analytics (7 threads, above the profile's max_threads 5).
-- ---------------------------------------------------------------------------

CREATE RESOURCE cpu (MASTER THREAD, WORKER THREAD);
CREATE WORKLOAD all SETTINGS max_concurrent_threads = 22;
CREATE WORKLOAD ingestion  IN all SETTINGS priority = 0;
CREATE WORKLOAD default    IN all SETTINGS priority = 1;
CREATE WORKLOAD monitoring IN all SETTINGS priority = 1;
CREATE WORKLOAD analytics  IN all SETTINGS priority = 2, max_concurrent_threads = 7;

-- ---------------------------------------------------------------------------
-- Settings profiles. Constraint syntax per
-- https://clickhouse.com/docs/operations/settings/constraints-on-settings
-- (MIN / MAX / CONST). Limits the client must not raise are MAX- or CONST-bounded;
-- use_query_cache and use_query_condition_cache are off so repeated runs measure reads.
-- ---------------------------------------------------------------------------

-- Service read path (dossier 02 F56 layer a). timeout_overflow_mode is 'throw' here,
-- not the production 'break', so an overrun shows as an error in the results.
CREATE SETTINGS PROFILE IF NOT EXISTS svc_reader_profile SETTINGS
    readonly = 2,
    max_execution_time = 60 MAX 60,
    timeout_overflow_mode = 'throw',
    max_memory_usage = 1800000000 MAX 1800000000,
    max_rows_to_read = 50000000 MAX 50000000,
    read_overflow_mode = 'throw' CONST,
    max_threads = 5 MAX 5,
    max_concurrent_queries_for_user = 50 CONST,
    max_bytes_ratio_before_external_group_by = 0.5,  -- MAY REJECT if the setting name is unknown on 26.8 (dossier 11 cites it as a profile setting)
    max_bytes_ratio_before_external_sort = 0.5,      -- MAY REJECT, same reason
    use_query_cache = 0,
    use_query_condition_cache = 0,
    workload = 'monitoring' CONST;

-- Analyst path (dossier 11 section 5.1), scaled to the container. Single node, so
-- enable_parallel_replicas stays 0; the query cache is off for measurement (the dossier
-- turns it on for dashboards).
CREATE SETTINGS PROFILE IF NOT EXISTS analyst_profile SETTINGS
    readonly = 2,
    max_execution_time = 600 MAX 600,
    timeout_overflow_mode = 'throw',
    max_memory_usage = 2800000000 MAX 2800000000,
    max_memory_usage_for_user = 3500000000 MAX 3500000000,
    max_rows_to_read = 2000000000 MAX 2000000000,
    read_overflow_mode = 'throw' CONST,
    max_threads = 5 MAX 5,
    max_concurrent_queries_for_user = 4 CONST,
    max_bytes_ratio_before_external_group_by = 0.3,  -- MAY REJECT, see above
    max_bytes_ratio_before_external_sort = 0.3,      -- MAY REJECT, see above
    optimize_aggregation_in_order = 1,
    join_algorithm = 'direct,parallel_hash,grace_hash',
    max_bytes_in_join = 1000000000,
    join_overflow_mode = 'throw',
    use_query_cache = 0,
    use_query_condition_cache = 0,
    query_cache_nondeterministic_function_handling = 'save',
    enable_parallel_replicas = 0,
    workload = 'analytics' CONST;

-- ---------------------------------------------------------------------------
-- Users. Passwords are benchmark-only.
-- ---------------------------------------------------------------------------

CREATE USER IF NOT EXISTS svc_reader IDENTIFIED WITH sha256_password BY 'bench'
    SETTINGS PROFILE 'svc_reader_profile';
CREATE USER IF NOT EXISTS analyst IDENTIFIED WITH sha256_password BY 'bench'
    SETTINGS PROFILE 'analyst_profile';
CREATE USER IF NOT EXISTS analyst_t0 IDENTIFIED WITH sha256_password BY 'bench'
    SETTINGS PROFILE 'analyst_profile';
CREATE USER IF NOT EXISTS analyst_partner IDENTIFIED WITH sha256_password BY 'bench'
    SETTINGS PROFILE 'analyst_profile';
CREATE USER IF NOT EXISTS analyst_partner_sq IDENTIFIED WITH sha256_password BY 'bench'
    SETTINGS PROFILE 'analyst_profile';

GRANT SELECT ON flowseer.* TO svc_reader, analyst, analyst_t0, analyst_partner, analyst_partner_sq;
GRANT dictGet ON flowseer.* TO svc_reader, analyst, analyst_t0, analyst_partner, analyst_partner_sq;

-- ---------------------------------------------------------------------------
-- Partner mapping (one-level provider links). Kept in its own database so the
-- flowseer.* row policies never apply to the table the policy itself reads.
-- P = t0000 .. t0019 (T0 plus 19 tenants of unit 1, sizes 58 down to 3 devices).
-- ---------------------------------------------------------------------------

CREATE DATABASE IF NOT EXISTS flowseer_acl;

CREATE TABLE IF NOT EXISTS flowseer_acl.user_tenants
(
    user      String,
    tenant_id String
)
ENGINE = ReplacingMergeTree
ORDER BY (user, tenant_id);

INSERT INTO flowseer_acl.user_tenants
SELECT u, concat('t', leftPad(toString(number), 4, '0'))
FROM numbers(20)
ARRAY JOIN ['analyst_partner', 'analyst_partner_sq'] AS u;

CREATE DICTIONARY IF NOT EXISTS flowseer_acl.user_tenants_dict
(
    user      String,
    tenant_id String,
    granted   UInt8 DEFAULT 1
)
PRIMARY KEY user, tenant_id
SOURCE(CLICKHOUSE(
    QUERY 'SELECT user, tenant_id, toUInt8(1) AS granted FROM flowseer_acl.user_tenants FINAL'
    USER 'default' PASSWORD 'bench'))
LAYOUT(COMPLEX_KEY_HASHED())
LIFETIME(MIN 60 MAX 120);

GRANT dictGet ON flowseer_acl.user_tenants_dict TO analyst_partner;
GRANT SELECT ON flowseer_acl.user_tenants TO analyst_partner_sq;

-- ---------------------------------------------------------------------------
-- Row policies (https://clickhouse.com/docs/sql-reference/statements/create/row-policy):
-- target "ON { [db.]table | db.* }"; "By default, policies are permissive"; "A user to
-- whom no condition applies therefore sees every row"
-- (access_control_improvements.users_without_row_policies_can_read_rows, default on),
-- so svc_reader and analyst stay unfiltered without an allow-all policy.
-- Every object in flowseer has a tenant_id column, which a db-wide policy needs.
-- ---------------------------------------------------------------------------

CREATE ROW POLICY IF NOT EXISTS t0_only ON flowseer.*
    FOR SELECT USING tenant_id = 't0000' TO analyst_t0;

-- Mapping-driven policy, dictionary form. The row-policy page does not say whether the
-- condition may call dictHas or currentUser(): MAY REJECT (unverified). dictHas over a
-- complex key needs the key tuple's types to match the dictionary (String, String),
-- hence toString(tenant_id). Expected cost: the condition is per row and cannot prune
-- the primary key, so without an explicit tenant filter the read grows with total data.
CREATE ROW POLICY IF NOT EXISTS partner_dict ON flowseer.*
    FOR SELECT USING dictHas('flowseer_acl.user_tenants_dict', (currentUser(), toString(tenant_id)))
    TO analyst_partner;

-- Mapping-driven policy, IN-subquery form. Not stated on the row-policy page either:
-- MAY REJECT (unverified). An IN set over the key's first column can prune the primary
-- index, which is the property the benchmark compares with the dictionary form.
CREATE ROW POLICY IF NOT EXISTS partner_subquery ON flowseer.*
    FOR SELECT USING tenant_id IN (SELECT tenant_id FROM flowseer_acl.user_tenants WHERE user = currentUser())
    TO analyst_partner_sq;
