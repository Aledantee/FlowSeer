-- CPU scheduling by workload. Ingestion runs ahead of monitoring reads, and
-- both run ahead of analytics, so an analytic query cannot hold inserts back.
-- No thread cap is set on any workload: the right number depends on the
-- cores of the deployment, and a profile bounds each query's own threads.
CREATE RESOURCE IF NOT EXISTS cpu {{.OnCluster}} (MASTER THREAD, WORKER THREAD)
-- statement
CREATE WORKLOAD IF NOT EXISTS flowseer {{.OnCluster}}
-- statement
CREATE WORKLOAD IF NOT EXISTS ingestion {{.OnCluster}} IN flowseer SETTINGS priority = 0
-- statement
CREATE WORKLOAD IF NOT EXISTS monitoring {{.OnCluster}} IN flowseer SETTINGS priority = 1
-- statement
CREATE WORKLOAD IF NOT EXISTS analytics {{.OnCluster}} IN flowseer SETTINGS priority = 2
