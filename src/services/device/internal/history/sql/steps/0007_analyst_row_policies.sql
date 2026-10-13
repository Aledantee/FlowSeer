-- The analyst reads only the tenants the user_tenants table maps to it. The
-- condition is an IN over a subquery, which prunes the primary key on the
-- leading tenant_id column, never a per-row function. Every table of the
-- history database that holds tenant_id has one policy. The analyst reads
-- user_tenants through a policy that shows only its own rows.
CREATE ROW POLICY IF NOT EXISTS analyst_syslog {{.OnCluster}} ON {{.DB}}.syslog
    FOR SELECT USING tenant_id IN (SELECT tenant_id FROM {{.Control}}.user_tenants WHERE user = currentUser())
    TO {{.Analyst}}
-- statement
CREATE ROW POLICY IF NOT EXISTS analyst_syslog_by_location {{.OnCluster}} ON {{.DB}}.syslog_by_location
    FOR SELECT USING tenant_id IN (SELECT tenant_id FROM {{.Control}}.user_tenants WHERE user = currentUser())
    TO {{.Analyst}}
-- statement
CREATE ROW POLICY IF NOT EXISTS analyst_syslog_daily {{.OnCluster}} ON {{.DB}}.syslog_daily
    FOR SELECT USING tenant_id IN (SELECT tenant_id FROM {{.Control}}.user_tenants WHERE user = currentUser())
    TO {{.Analyst}}
-- statement
CREATE ROW POLICY IF NOT EXISTS analyst_device_dim {{.OnCluster}} ON {{.DB}}.device_dim
    FOR SELECT USING tenant_id IN (SELECT tenant_id FROM {{.Control}}.user_tenants WHERE user = currentUser())
    TO {{.Analyst}}
-- statement
CREATE ROW POLICY IF NOT EXISTS analyst_user_tenants {{.OnCluster}} ON {{.Control}}.user_tenants
    FOR SELECT USING user = currentUser()
    TO {{.Analyst}}
