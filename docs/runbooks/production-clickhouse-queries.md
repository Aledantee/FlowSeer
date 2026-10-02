# Querying production ClickHouse

The `mcp-clickhouse` server reaches the live monitoring store of a customer.
Inserts queue behind a heavy read, so an exploratory query can stall ingest.

The MCP tool gives up after 30 seconds, but the query keeps running on the
server. The connection is read-only, which also forbids setting
`max_execution_time`, so nothing on the client side bounds a query. A
window function over 90 days of `edge_monitor.ap` ran for 218 seconds, used
21 GiB of memory, and held inserts back for about two minutes.

- Keep a query on the large tables to a window of 30 days or less.
- Sample instead of scanning, for example one hour per day.
- After a timeout, check `system.processes` before running the query again.
  The first run is probably still going.
- Never retry a timed-out heavy query in parallel with itself.
