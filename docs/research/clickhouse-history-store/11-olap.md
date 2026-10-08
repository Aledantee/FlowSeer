---
title: OLAP use of the five-pattern history store
date: 2026-10-08
status: research report for the history store plan; read-only toward the repository
scope: the pattern set of dossier 10 section 7 reviewed against ad hoc fleet analytics (weeks to months, arbitrary dimensions, cross-domain joins, distributions, cohorts)
---

# OLAP use of the five-pattern history store

Builds on dossiers 00 to 10 under `docs/research/clickhouse-history-store/` and cites their findings by number (02 F1 to F74, 04 D1 to D22, 05 Q1 to Q12, 06 S and Q numbers, 07 E numbers, 08 findings 1 to 11, 09 L and Q numbers, 10 G numbers). Sources fetched 2026-10-08 are cited by URL with a quote. "Inference" marks reasoning from cited text; "unverified" marks what no fetched source confirms. Findings here are numbered O1 to O30. Corrections to earlier dossiers are marked **Change** and collected in section 6.

## 0. Result in one screen

| # | Finding |
| --- | --- |
| O1 | The five patterns survive OLAP unchanged. What changes is the **grain the analyst reads**: a fleet-wide scan over weeks reads the daily rollup, over days the hourly rollup, and raw only inside one week with an explicit row bound. Entity-first rollups with monthly partitions already bound a months-long fleet scan to about the window's rows (partition pruning), so the time-first companion (04 D9) stays a monitoring table and is not needed for OLAP. |
| O2 | Every OLAP cost is `rows = E_tenant x buckets(W, grain)` for the first stage and `distinct dimension values` for the second, and neither term holds total tenants or total table size. The first stage must aggregate **in key order** (`optimize_aggregation_in_order = 1`, prefix GROUP BY) so its memory is bounded by groups in flight, not by `E x buckets`; otherwise a 10,000-device tenant's 90-day interface query holds 43 M groups in RAM. |
| O3 | Dimension model: stamp **two** as-of attributes on every fact row, `site_id` (already) and `software_version` (new), both `LowCardinality(String)`, both constant within a device's key run and so about 0.1 to 0.3 bytes per row compressed (inference, measured in section 7). Everything else (vendor, model, hardware revision, hostname, lifecycle, integration, tags, site name and region) lives in dictionaries and mirror tables keyed `(tenant_id, device_id)`. |
| O4 | As-of versus current: `site_id` and `software_version` on the row are "as it was when observed"; the dictionaries are "as it is now"; the two range dictionaries (`device_site_dict` from 09 section 9, `device_software_dict` new) answer "as it was at T" for rows that carry a different device than the one in question (a client row needs the AP's firmware). Tags are current-only by construction (no history message exists). |
| O5 | Cross-domain joins run on rollup grain with identical key columns and types in every table: `tenant_id LowCardinality(String)`, `device_id UUID`, `interface_name LowCardinality(String)`, `mac UInt64`, `hour DateTime`, `day Date`. A join of two rollups on `(device_id, interface_name, hour)` inside one tenant is a `parallel_hash` join whose right side is bounded by the smaller table's rows in the window; a three-way or wider union is a bucketed `GROUP BY` over `UNION ALL`. |
| O6 | Projections are out for the analyst path: "Projections aren't used together with Parallel replicas", "Projections are not supported in the SELECT statements with the FINAL modifier", "Lightweight updates and deletes aren't supported for tables with projections", and on Replacing or Aggregating engines every merge needs `deduplicate_merge_projection_mode = 'rebuild'`. MV-fed second tables keep their own TTL and work with parallel replicas and tenant forget. The 09 Q3 `by_time` projection becomes an MV table. |
| O7 | Isolation: a third user `analyst` with its own profile (memory, time, rows, threads, external aggregation, join caps, query cache), a `workload = 'analytics'` with the lowest priority under a CPU resource and `cpu_slot_preemption = true`, and parallel replicas on for that user only. A dedicated analytics replica is not warranted at 1 x 2; the trigger is stated. |
| O8 | Seven additions to the dossier set are required for OLAP (section 6): the `software_version` stamp, `site_id` on the 06 tables and `link_events`, daily rollups for every samples family with monthly partitions, `fdb_vlan_daily`, `syslog_daily`, `ap_device_id` on wireless client tables, and the dimension mirror tables plus dictionaries. |

## 1. OLAP query catalogue

Notation: `D` devices in the tenant (10,000 for the large tenant), `E` entities of the family in the tenant (interfaces 480,000, radios 30,000, components 200,000), `W` window, `H = W` in hours, `Y = W` in days, `G` = 8,192 rows per granule, `P` = parts overlapping the window. Dimensions come from `dictGet` on the dictionaries of section 2; the parameter `{t}` is the tenant. Every query runs as the `analyst` user of section 5. "Stage 1" is the aggregate-state merge per entity and bucket, "stage 2" the dimension grouping.

Rows read in stage 1 are the table's rows for the tenant inside the partitions the window touches: for a monthly partitioned rollup and a 90-day window that is 3 to 4 months of the tenant's rows, `E x 2,160` hourly or `E x 90` daily plus boundary months. The sparse index guide: a range always rounds out to whole granules, and the partition key gives "MinMax indexes on partition columns" (02 F7).

### Q1. Interface error rate by model and firmware over 90 days

```sql
SET optimize_aggregation_in_order = 1;
WITH per_iface AS (
    SELECT device_id, interface_name, day, discontinuity_at, software_version,
           argMinMerge(in_errors_first) AS e_f, argMaxMerge(in_errors_last) AS e_l, max(in_errors_max) AS e_m,
           argMinMerge(in_unicast_packets_first) AS p_f, argMaxMerge(in_unicast_packets_last) AS p_l, max(in_unicast_packets_max) AS p_m
    FROM flowseer.interface_daily
    WHERE tenant_id = {t:String} AND day >= today() - 90
    GROUP BY device_id, interface_name, day, discontinuity_at, software_version
)
SELECT dictGet('flowseer.device_dim', 'vendor', ({t:String}, device_id)) AS vendor,
       dictGet('flowseer.device_dim', 'model',  ({t:String}, device_id)) AS model,
       software_version,
       sum((e_m - e_f) + if(e_m > e_l, e_l, 0)) AS errors,
       sum((p_m - p_f) + if(p_m > p_l, p_l, 0)) AS packets,
       errors / greatest(packets, 1) AS error_rate,
       uniqExact(device_id) AS devices
FROM per_iface
GROUP BY vendor, model, software_version
HAVING packets > 0
ORDER BY error_rate DESC;
```

`software_version` is the stamped as-of value (O3), so a device upgraded on day 40 contributes its first 40 days to the old cohort and the rest to the new one, which is the cohort comparison analysts ask for. The within-day increase term is 04 section 4.4 without the cross-row boundary term (04 section 5.4 accepts that for rankings). `software_version` in the daily rollup is `SimpleAggregateFunction(anyLast, ...)` grouped as a key here; a day on which the version changed yields two rows for that entity-day only when the rollup key includes it (section 6, change 1 makes `software_version` part of the rollup GROUP BY so the split is exact).

Cost: stage 1 reads `E x 90 x P` rows = 480,000 x 90 = 43 M rows (4 months of partitions at most, about 58 M), streams in key order (prefix `device_id, interface_name, day, discontinuity_at` of the sort key, with `software_version` appended; the in-order optimisation still applies because the appended column varies only inside the prefix group, inference), memory about `threads x groups in flight x state` = tens of MB. Stage 2 groups by about 40 models x 5 versions. Flat in total data, linear in `E`.

### Q2. APs whose channel utilisation p95 exceeds 70 % by site and hour of week

```sql
SELECT site_id, device_id, radio_index, toDayOfWeek(hour) AS dow, toHour(hour) AS hod,
       count() AS hours,
       countIf(util_total_sum / samples > 7000) AS busy_hours
FROM (
    SELECT device_id, radio_index, hour, anyLast(site_id) AS site_id,
           sum(util_total_sum) AS util_total_sum, sum(samples) AS samples
    FROM flowseer.radio_hourly
    WHERE tenant_id = {t:String} AND hour >= now() - INTERVAL 90 DAY
    GROUP BY device_id, radio_index, hour
)
GROUP BY site_id, device_id, radio_index, dow, hod
HAVING busy_hours > 0.05 * hours
ORDER BY busy_hours / hours DESC;
```

"p95 of hourly utilisation exceeds 70 %" is equivalent to "more than 5 % of the hours exceed 70 %", which needs two counters per group instead of a quantile sketch. With 30,000 radios x 168 (day of week x hour) = 5 M groups a `quantileTDigest` state would cost gigabytes; two `UInt64` cost 80 MB. Note `radio_hourly` (05 3.7) carries no `site_id`; section 6 adds the stamp to every rollup.

Cost: `E_radio x 2,160` = 65 M rows in stage 1, in order; 5 M groups in stage 2 (bounded by `E x 168`, independent of W). Hourly grain is required because the question is per hour of week; daily rollups cannot answer it.

### Q3. Ports with errors and link flaps in the same hour

```sql
WITH errs AS (
    SELECT device_id, interface_name, hour,
           argMaxMerge(in_errors_last) - argMinMerge(in_errors_first) AS err_inc
    FROM flowseer.interface_hourly
    WHERE tenant_id = {t:String} AND hour >= now() - INTERVAL 30 DAY
    GROUP BY device_id, interface_name, hour, discontinuity_at
    HAVING err_inc > 0
),
flaps AS (
    SELECT device_id, interface_name, toStartOfHour(ts) AS hour, uniqExact(record_id) AS flaps
    FROM flowseer.interface_changes
    WHERE tenant_id = {t:String} AND ts >= now() - INTERVAL 30 DAY AND attribute = 'oper_status'
    GROUP BY device_id, interface_name, hour
)
SELECT e.device_id, e.interface_name, e.hour, e.err_inc, f.flaps
FROM errs AS e
INNER JOIN flaps AS f ON f.device_id = e.device_id AND f.interface_name = e.interface_name AND f.hour = e.hour
ORDER BY e.err_inc DESC LIMIT 500
SETTINGS join_algorithm = 'parallel_hash';
```

The join keys are identical columns in both tables (section 3). The planner "places the smaller table on the right side" (02 F23 quoting the joins page), which here is `flaps` (tenant change rows in 30 days, about 0.2 M at 0.5 flaps per port per day on 1 % of ports). Where the question is "flap messages in syslog" instead of projector transitions, replace `flaps` with `syslog_hourly WHERE vendor_tag IN ('LINK-3-UPDOWN', ...)` grouped by `(device_id, hour)`; the interface name is inside `message` and not a column, so that variant joins on `(device_id, hour)` only, and the dossier 07 producer rule (E29: one producer, the state projector) is why `interface_changes` is the right table.

Cost: left `E x 720` = 346 M rows for 30 days hourly, which exceeds the 100 M-row budget of section 5; the analyst narrows to 7 days (81 M) or runs the daily variant (`E x 30` = 14 M) with the join on `day`. Right side: tenant changes in W. Join memory = right rows x row width = tens of MB.

### Q4. Client RSSI distribution per AP model, before and after a firmware upgrade

```sql
SELECT dictGet('flowseer.device_dim', 'model', ({t:String}, ap_device_id)) AS model,
       dictGetOrDefault('flowseer.device_software_dict', 'version', ({t:String}, ap_device_id), hour, '') AS ap_firmware,
       quantiles(0.1, 0.5, 0.9)(rssi) AS rssi_p10_p50_p90,
       count() AS client_hours
FROM (
    SELECT ap_device_id, hour, mac, argMaxMerge(rssi_last) AS rssi
    FROM flowseer.client_presence
    WHERE tenant_id = {t:String} AND hour >= now() - INTERVAL 60 DAY AND ap_device_id != toUUID('00000000-0000-0000-0000-000000000000')
    GROUP BY ap_device_id, bssid, mac, hour
)
GROUP BY model, ap_firmware
ORDER BY model, ap_firmware;
```

The row's device is the client, not the AP, so the AP's as-of firmware cannot be a stamp on this row; the range dictionary (section 2.4) resolves `(tenant, ap_device_id, hour)` to the version running at that hour. `ap_device_id UUID` is a new typed column beside `ap_key` (section 6, change 6). `quantiles` with three levels is one sketch per group (40 models x 5 versions), so memory is small.

Cost: `C x 24 x Y x A` presence rows = 30,000 clients x 24 x 60 x 1.3 = 56 M rows, in order on `(ap_key, bssid, mac, hour)`; one `dictGet` per row on a hashed dictionary and one on a range dictionary (hash lookup plus a binary search over that device's ranges, inference from "Hash table with ordered ranges").

### Q5. MTTR of alarms per vendor per quarter

```sql
SELECT dictGet('flowseer.device_dim', 'vendor', ({t:String}, device_id)) AS vendor,
       toStartOfQuarter(raised_at) AS quarter, max_severity,
       count() AS alarms,
       avg(cleared_at - raised_at) AS mttr_s,
       quantile(0.95)(cleared_at - raised_at) AS p95_s
FROM (
    SELECT device_id, resource_kind, resource_key, type_id, type_qualifier, raised_at,
           min(cleared_at) AS cleared_at, max(max_severity) AS max_severity, max(cleared_by) AS cleared_by
    FROM flowseer.alarm_intervals
    WHERE tenant_id = {t:String} AND raised_at >= toStartOfQuarter(now() - INTERVAL 1 YEAR)
    GROUP BY device_id, resource_kind, resource_key, type_id, type_qualifier, raised_at
)
WHERE cleared_by != 'open'
GROUP BY vendor, quarter, max_severity
ORDER BY quarter, vendor, max_severity;
```

Cost: tenant alarm instances raised in the year: `D x 5/day x 365` = 18 M rows (07 section 3.1 rates), in order (the GROUP BY is the full sort key after `tenant_id`). Stage 2: vendors x 4 quarters x 6 severities.

### Q6. FDB growth per VLAN per site

```sql
SELECT site_id, vlan_id, toMonday(day) AS week, uniqExactMerge(members) AS macs
FROM flowseer.fdb_vlan_daily
WHERE tenant_id = {t:String} AND day >= today() - 180
GROUP BY site_id, vlan_id, week
ORDER BY site_id, vlan_id, week;
```

`fdb_vlan_daily` is new (section 6, change 4): `(tenant_id, device_id, vlan_id, day)` with `uniqExactState(mac)` and `anyLast(site_id)`. Merging `uniqExact` states across the site's devices and the week's days counts a MAC seen on several switches once, which is the "hosts in the VLAN" number; a per-switch count is `GROUP BY device_id` instead. Memory: `uniqExact` holds the MAC set per group (site x VLAN x week), about 8 bytes per MAC, bounded by hosts per VLAN.

Cost: `D_switch x VLANs x 180` = 10,000 x 20 x 180 = 36 M rows. Without the rollup the same question reads `fdb_presence` at `D x M x 180` = 540 M rows (06 section 3 at 300 members), which is why the rollup exists.

### Q7. Devices unreachable more than 1 % of the month, by integration

```sql
SELECT dictGet('flowseer.integration_dim', 'kind_name', ({t:String}, integration_id)) AS integration_kind,
       integration_id,
       countIf(unreachable_ratio > 0.01) AS devices_over_1pct, count() AS devices
FROM (
    SELECT device_id, anyLast(integration_id) AS integration_id,
           sum(unreachable_seconds) / (uniqExact(day) * 86400) AS unreachable_ratio
    FROM flowseer.binding_state_daily
    WHERE tenant_id = {t:String} AND day >= toStartOfMonth(now() - INTERVAL 1 MONTH) AND day < toStartOfMonth(now())
    GROUP BY device_id, binding_id
)
GROUP BY integration_kind, integration_id
ORDER BY devices_over_1pct DESC;
```

`binding_state_daily` (09 section 7) carries `integration_id` (section 6, change 7) because a Binding "never changes either end" (`CONCEPTS.md:9`), so the integration of a binding is as-of by construction. Cost: `D x bindings x 30` = 600,000 rows. The same ratio from `probe_hourly` (`sum(received) / sum(sent)` per device, 20,000 x 720 = 14 M rows) measures reachability from the edge rather than from the management binding, and the two answer different questions.

### Q8. Syslog volume by severity, vendor, and week

```sql
SELECT dictGet('flowseer.device_dim', 'vendor', ({t:String}, device_id)) AS vendor, severity,
       toMonday(day) AS week, sum(messages) AS messages
FROM flowseer.syslog_daily
WHERE tenant_id = {t:String} AND day >= today() - 90
GROUP BY vendor, severity, week ORDER BY week, vendor, severity;
```

`syslog_daily` is new (section 6, change 5): `SummingMergeTree` keyed `(tenant_id, device_id, severity, vendor_tag, day)` with `sum(repeat_count)` as `messages`, fed from `syslog` like E20's hourly table. Cost: `D x tags x 90` = 10,000 x 20 x 90 = 18 M rows. The raw table for the same window holds `D x 1,700 x 90` = 1.5 G rows (07 E18 rates), which no analyst query reads.

### Q9. Top message classes per model over 30 days

```sql
SELECT model, vendor_tag, sum(messages) AS n
FROM (
    SELECT dictGet('flowseer.device_dim', 'model', ({t:String}, device_id)) AS model, vendor_tag, messages
    FROM flowseer.syslog_daily
    WHERE tenant_id = {t:String} AND day >= today() - 30 AND severity <= 'warning'
)
GROUP BY model, vendor_tag
ORDER BY model, n DESC
LIMIT 10 BY model;
```

Cost: `D x tags x 30` = 6 M rows; `LIMIT 10 BY model` after the aggregate.

### Q10. Reboots per model per month

```sql
SELECT dictGet('flowseer.device_dim', 'model', ({t:String}, device_id)) AS model, toStartOfMonth(day) AS month,
       sum(resets) AS reboots, uniqExact(device_id) AS devices, reboots / devices AS per_device
FROM (
    SELECT device_id, day,
           (max(uptime_max) > argMaxMerge(uptime_last)) OR (argMinMerge(uptime_first) < lagInFrame(argMaxMerge(uptime_last)) OVER (PARTITION BY device_id ORDER BY day ROWS BETWEEN 1 PRECEDING AND CURRENT ROW)) AS resets
    FROM flowseer.device_daily
    WHERE tenant_id = {t:String} AND day >= today() - 365
    GROUP BY device_id, day
)
GROUP BY model, month ORDER BY month, reboots DESC;
```

A reset inside a day shows as `max > last`; a reset between days as `first(today) < last(yesterday)`. 04 D15 notes a reboot is "not inferred" from uptime by the schema; this query counts uptime discontinuities, which is the operational proxy, and the plan can replace it with `entity_events` lifecycle rows once a producer exists (09 L2). Cost: `D x 365` = 3.7 M rows; the window function runs per device over 365 rows.

### Q11. CPU saturation: fraction of hours above 80 % per model per week

```sql
SELECT dictGet('flowseer.device_dim', 'model', ({t:String}, device_id)) AS model, toMonday(hour) AS week,
       countIf(cpu_max > 8000) / count() AS busy_fraction, uniqExact(device_id) AS devices
FROM (
    SELECT device_id, hour, max(cpu_avg_basis_points_max) AS cpu_max
    FROM flowseer.device_hourly
    WHERE tenant_id = {t:String} AND hour >= now() - INTERVAL 90 DAY
    GROUP BY device_id, hour
)
GROUP BY model, week ORDER BY week, busy_fraction DESC;
```

Cost: `D x 2,160` = 21.6 M rows. `device_hourly` needs `max` and `avg` of `cpu_avg_basis_points` (04 3.5 names the interface rollup only; the device table follows "by column substitution").

### Q12. Temperature versus CPU correlation per model (cross-domain join)

```sql
WITH cpu AS (
    SELECT device_id, hour, max(cpu_avg_basis_points_max) AS cpu
    FROM flowseer.device_hourly WHERE tenant_id = {t:String} AND hour >= now() - INTERVAL 30 DAY
    GROUP BY device_id, hour
),
temp AS (
    SELECT device_id, hour, max(temperature_max) AS temp
    FROM flowseer.component_hourly WHERE tenant_id = {t:String} AND hour >= now() - INTERVAL 30 DAY AND kind = 8 /* sensor */
    GROUP BY device_id, hour
)
SELECT dictGet('flowseer.device_dim', 'model', ({t:String}, cpu.device_id)) AS model,
       corr(cpu.cpu, temp.temp) AS r, count() AS device_hours
FROM cpu INNER JOIN temp ON temp.device_id = cpu.device_id AND temp.hour = cpu.hour
GROUP BY model HAVING device_hours > 1000 ORDER BY r DESC
SETTINGS join_algorithm = 'parallel_hash';
```

Cost: `cpu` = `D x 720` = 7.2 M rows; `temp` = `components x 720` = 144 M rows at 20 components per device, narrowed by `kind` to sensors (about 4 per device: 29 M), aggregated to `D x 720` before the join. Both sides are bounded by `D x H`; the join builds the smaller (`cpu`, 7.2 M rows x ~24 B = 170 MB) in memory. The analyst profile's `max_bytes_in_join` (section 5) caps that.

### Q13. PoE draw versus port errors (same key columns across two sample families)

```sql
SELECT p.device_id, p.interface_name, p.hour, p.draw_max_nw, e.err_inc
FROM (
    SELECT device_id, interface_name, hour, max(power_draw_max_nw) AS draw_max_nw
    FROM flowseer.poe_port_hourly WHERE tenant_id = {t:String} AND hour >= now() - INTERVAL 7 DAY
    GROUP BY device_id, interface_name, hour
) AS p
INNER JOIN (
    SELECT device_id, interface_name, hour, argMaxMerge(in_errors_last) - argMinMerge(in_errors_first) AS err_inc
    FROM flowseer.interface_hourly WHERE tenant_id = {t:String} AND hour >= now() - INTERVAL 7 DAY
    GROUP BY device_id, interface_name, hour, discontinuity_at HAVING err_inc > 0
) AS e ON e.device_id = p.device_id AND e.interface_name = p.interface_name AND e.hour = p.hour
ORDER BY err_inc DESC LIMIT 200
SETTINGS join_algorithm = 'parallel_hash';
```

Works because 04 D2 and 10 G1 both key by `interface_name LowCardinality(String)`. Cost: PoE side `E_poe x 168` = 1,800 x 48 x 168 = 14.5 M; interface side `E x 168` = 81 M, reduced by `HAVING` to ports with errors (a few percent).

### Q14. Optics lanes degrading, by module vendor and part

```sql
WITH trend AS (
    SELECT device_id, interface_name, lane,
           simpleLinearRegression(toUnixTimestamp(day) / 86400, 10 * log10(greatest(rx_avg, 1) / 1e6)) AS fit,  -- dBm per day
           count() AS days
    FROM (
        SELECT device_id, interface_name, lane, day, avgMerge(rx_power_avg) AS rx_avg
        FROM flowseer.optics_lane_daily WHERE tenant_id = {t:String} AND day >= today() - 90
        GROUP BY device_id, interface_name, lane, day
    )
    GROUP BY device_id, interface_name, lane HAVING days >= 60
),
modules AS (
    SELECT device_id, interface_name, argMax(vendor, ts) AS vendor, argMax(part_number, ts) AS part_number
    FROM flowseer.optics_module_changes WHERE tenant_id = {t:String}
    GROUP BY device_id, interface_name
)
SELECT m.vendor, m.part_number, count() AS lanes, countIf(fit.1 * 30 < -0.5) AS degrading
FROM trend INNER JOIN modules AS m ON m.device_id = trend.device_id AND m.interface_name = trend.interface_name
GROUP BY m.vendor, m.part_number ORDER BY degrading DESC
SETTINGS join_algorithm = 'parallel_hash';
```

Cost: `lanes x 90` = 15,000 x 90 = 1.4 M rows; `modules` is the tenant's module change rows (10 section 3: about 0.002 swaps per cage per day, so tens of thousands). The module identity lives in a changes table, so "current module per port" is `argMax` over that table, not a dictionary; a `module_dim` mirrored from the component tree would replace the subquery once inventory carries transceivers as components (`component.proto:206`).

### Q15. Roams per client per day, by AP model pair

```sql
SELECT dictGet('flowseer.device_dim', 'model', ({t:String}, from_ap_device_id)) AS from_model,
       dictGet('flowseer.device_dim', 'model', ({t:String}, to_ap_device_id)) AS to_model,
       count() AS roams, uniqExact(mac) AS clients, roams / clients AS per_client
FROM flowseer.client_events
WHERE tenant_id = {t:String} AND kind = 'roam' AND ts >= now() - INTERVAL 30 DAY
GROUP BY from_model, to_model ORDER BY roams DESC;
```

Needs `from_ap_device_id`/`to_ap_device_id UUID` beside the `*_ap_key` strings (change 6). Cost: tenant roam rows in 30 days (05: about 1 M per day fleet-wide, so a 10,000-AP tenant's share for 30 days, about 15 M), read through the weekly partitions of the window with the `minmax` on `ts` pruning inside them.

### Q16. Clients per SSID per site per hour of day (weekday profile)

```sql
SELECT site_id, ssid, toHour(hour) AS hod, avg(c) AS avg_clients
FROM (
    SELECT site_id, ssid, hour, uniqCombinedMerge(12)(clients) AS c
    FROM flowseer.ssid_clients_hourly
    WHERE tenant_id = {t:String} AND hour >= now() - INTERVAL 28 DAY AND toDayOfWeek(hour) <= 5
    GROUP BY site_id, ssid, hour
)
GROUP BY site_id, ssid, hod ORDER BY site_id, ssid, hod;
```

Cost: `D_ap x SSIDs x 672` = 10,000 x 3 x 672 = 20 M rows (05 Q10 shape widened to the tenant). The inner merge groups by `(site_id, ssid, hour)`, which is not a prefix of the key `(tenant_id, site_id, ap_key, ssid, hour)`, so it is a hash aggregate over `sites x SSIDs x 672` groups (200 sites: 400,000 groups of HLL state at 4 KB = 1.6 GB worst case). Bound: run per site (`WHERE site_id = ...`) or add `ssid_clients_site_hourly` fed by a second MV from `client_presence` with the key `(tenant_id, site_id, ssid, hour)`; section 6 lists it as optional.

### Q17. BGP peer flaps per week per vendor

```sql
SELECT dictGet('flowseer.device_dim', 'vendor', ({t:String}, device_id)) AS vendor, toMonday(hour) AS week,
       uniqExactMerge(flaps) AS flaps, uniqExactMerge(entities) AS peers_affected
FROM flowseer.protocol_transitions_hourly
WHERE tenant_id = {t:String} AND protocol = 'bgp_peer' AND attribute = 'state' AND hour >= now() - INTERVAL 180 DAY
GROUP BY vendor, week ORDER BY week;
```

Cost: `routers x peers x 4,320` rows only where transitions happened (the rollup has rows for hours with events only): at 10,000 peers in the tenant and 0.05 transitions per peer per day (08 section 8) it is under 100,000 rows. `uniqExact` states merge across devices and days; the state size is the flap count, small.

### Q18. Firmware adoption curve

```sql
SELECT day, dictGet('flowseer.device_dim', 'model', ({t:String}, device_id)) AS model, version, uniqExact(device_id) AS devices
FROM flowseer.device_software_images
WHERE tenant_id = {t:String} AND day >= today() - 180 AND running = 1
GROUP BY day, model, version ORDER BY day, model, version;
```

Cost: `D x images x 180` = 10,000 x 2 x 180 = 3.6 M rows (09 4.4). This is the source of `device_software_history` (section 2.4) as well.

### Q19. Topology churn per site per week

```sql
SELECT a_site_id AS site_id, toMonday(ts) AS week, status_to, uniqExact(link_id) AS links, uniqExact(record_id) AS events
FROM flowseer.link_events
WHERE tenant_id = {t:String} AND ts >= now() - INTERVAL 90 DAY AND event_kind = 'transitioned'
GROUP BY site_id, week, status_to ORDER BY week, site_id;
```

`link_events` (09 4.3) carries no `site_id`; change 2 adds `a_site_id` (the site of end a at write time). Cost: tenant link events in 90 days (09 section 2: 1,600 per day fleet-wide normal, 160,000 in a storm), monthly partitions prune to 3 to 4 months.

### Q20. Alarm density per alarm type per model

```sql
SELECT dictGet('flowseer.device_dim', 'model', ({t:String}, device_id)) AS model, type_id,
       uniqExact(record_id) AS raises, uniqExact(device_id) AS devices, raises / devices AS per_device
FROM flowseer.alarm_events
WHERE tenant_id = {t:String} AND transition = 'raise' AND ts >= now() - INTERVAL 90 DAY
GROUP BY model, type_id ORDER BY raises DESC LIMIT 50;
```

Cost: tenant alarm rows in the window via weekly partitions, `D x 5 x 90` = 4.5 M rows; `uniqExact(record_id)` is the exact count under redelivery (07 E13).

### Q21. Storage fill forecast: devices crossing 90 % within 60 days

```sql
SELECT device_id, storage_name, fit.1 AS slope_bp_per_day, fit.2 AS intercept,
       (9000 - intercept) / slope_bp_per_day AS days_to_90pct
FROM (
    SELECT device_id, storage_name,
           simpleLinearRegression(toUnixTimestamp(day) / 86400 - toUnixTimestamp(today()) / 86400, used_bp) AS fit, count() AS days
    FROM (
        SELECT device_id, storage_name, day, argMaxMerge(used_basis_points_last) AS used_bp
        FROM flowseer.storage_daily WHERE tenant_id = {t:String} AND day >= today() - 90
        GROUP BY device_id, source, storage_name, day
    )
    GROUP BY device_id, storage_name HAVING days >= 30
)
WHERE slope_bp_per_day > 0 AND days_to_90pct BETWEEN 0 AND 60
ORDER BY days_to_90pct;
```

Cost: `D x 3 x 90` = 2.7 M rows.

### Q22. Probe RTT p95 and loss per site per day

```sql
SELECT site_id, toDate(hour) AS day,
       quantilesTimingMerge(0.5, 0.95, 0.99)(rtt_ms_q) AS rtt_ms, 1 - sum(received) / sum(sent) AS loss
FROM flowseer.probe_hourly
WHERE tenant_id = {t:String} AND hour >= now() - INTERVAL 30 DAY
GROUP BY site_id, day ORDER BY site_id, day;
```

`probe_hourly` (10 4.3) carries no `site_id`; change 2 stamps it. Cost: `D x targets x 720` = 14 M rows; the `quantilesTiming` state merges across the site's devices (bounded, bucketed histogram).

### Q23. Cohort comparison: same model, firmware A versus B, radio noise floor and client count

```sql
SELECT software_version, quantile(0.5)(noise) AS noise_p50, quantile(0.9)(noise) AS noise_p90, avg(clients) AS avg_clients, uniqExact(device_id) AS aps
FROM (
    SELECT device_id, radio_index, hour, anyLast(software_version) AS software_version,
           max(noise_max) AS noise, max(clients_max) AS clients
    FROM flowseer.radio_hourly
    WHERE tenant_id = {t:String} AND hour >= now() - INTERVAL 30 DAY
      AND dictGet('flowseer.device_dim', 'model', ({t:String}, device_id)) = {model:String}
    GROUP BY device_id, radio_index, hour
)
GROUP BY software_version;
```

The `dictGet` in `WHERE` is evaluated per row after the primary-key range (no index can serve it), so the scan is the tenant's radio rows in the window, `E_radio x 720` = 21.6 M, and the filter removes most of them. If model-scoped scans are frequent, resolve the model to a device list first (`device_id IN (SELECT device_id FROM flowseer.device_dim_table WHERE tenant_id = ... AND model = ...)`), which turns the scan into `D_model` key ranges, the 09 section 9.2 scope rule.

### Q24. Operator actions per action and outcome per month (audit analytics)

```sql
SELECT toStartOfMonth(day) AS month, action, outcome, uniqExactMerge(events) AS n
FROM flowseer.operator_actions_daily
WHERE tenant_id = {t:String} AND day >= today() - 365
GROUP BY month, action, outcome ORDER BY month, n DESC;
```

Cost: `actions x outcomes x 365` rows, trivial.

### Cost summary

| Q | Table (grain) | Stage 1 rows (10,000-device tenant) | Stage 2 groups | Memory driver | Needs change |
| --- | --- | --- | --- | --- | --- |
| Q1 | `interface_daily` | 43 M (90 d) | 200 | in-order aggregation | 1, 3 |
| Q2 | `radio_hourly` | 65 M (90 d) | 5 M | two counters per group | 2 |
| Q3 | `interface_hourly` x `interface_changes` | 81 M (7 d) + 0.05 M | join | right side rows | none |
| Q4 | `client_presence` + range dict | 56 M (60 d) | 200 | sketches | 6, dictionaries |
| Q5 | `alarm_intervals` | 18 M (1 y) | 100 | in-order | none |
| Q6 | `fdb_vlan_daily` | 36 M (180 d) | sites x VLANs x 26 | `uniqExact` sets | 4 |
| Q7 | `binding_state_daily` | 0.6 M | 10 | none | 7 |
| Q8 | `syslog_daily` | 18 M (90 d) | 100 | none | 5 |
| Q9 | `syslog_daily` | 6 M (30 d) | models x tags | none | 5 |
| Q10 | `device_daily` | 3.7 M (1 y) | models x 12 | window per device | 3 |
| Q11 | `device_hourly` | 21.6 M (90 d) | models x 13 | none | 3 |
| Q12 | `device_hourly` x `component_hourly` | 7.2 M + 29 M | models | join 170 MB | 3 |
| Q13 | `poe_port_hourly` x `interface_hourly` | 14.5 M + 81 M (7 d) | rows | join | none |
| Q14 | `optics_lane_daily` x `optics_module_changes` | 1.4 M | parts | regression per lane | 3 |
| Q15 | `client_events` | 15 M (30 d) | models squared | none | 6 |
| Q16 | `ssid_clients_hourly` | 20 M (28 d) | 400,000 HLL | per-site run or new rollup | optional |
| Q17 | `protocol_transitions_hourly` | under 0.1 M | 26 x vendors | none | none |
| Q18 | `device_software_images` | 3.6 M | days x models x versions | none | none |
| Q19 | `link_events` | tenant events in 90 d | sites x weeks | none | 2 |
| Q20 | `alarm_events` | 4.5 M (90 d) | models x types | `uniqExact` | none |
| Q21 | `storage_daily` | 2.7 M | per storage | regression | 3 |
| Q22 | `probe_hourly` | 14 M (30 d) | sites x 30 | timing states | 2 |
| Q23 | `radio_hourly` | 21.6 M (30 d), filtered | versions | sketches | 1 |
| Q24 | `operator_actions_daily` | tiny | small | none | none |

No row count above holds total tenants or total table size. Each holds the tenant's entities and the window at the chosen grain, which is the predictable-scaling statement of section 5.4.

## 2. Dimension model

### 2.1 What the schema offers as dimensions

| Attribute | Source message | Changes over time | Cardinality (tenant of 10,000 devices) |
| --- | --- | --- | --- |
| vendor, model, hardware_revision, sys_object_id | `DeviceState` (`spec/proto/flowseer/model/inventory/v1/device.proto:159-191`) | in practice never for one device id: "a reappearing serial un-retires it instead of creating a duplicate" (`device.proto:45`), so a chassis swap is a new device | vendors 5 to 20, models 20 to 100, sysObjectIDs about the model count |
| software_version | `DeviceState.software_version` (`device.proto:178-185`) | a few times per year per device (upgrade waves) | 3 to 10 per model |
| firmware_fingerprint | `Provenance.firmware_fingerprint` on every record (`provenance.proto`, "a different fingerprint as a different device epoch") | same cadence as software_version, exact per record | about the version count |
| hostname, lifecycle, serial | `DeviceState` (`device.proto:136-158`) | rare | one per device |
| site | `DeviceConfig.location` (`device.proto:100-103`) into a `LOCATION_KIND_SITE` location (`location.proto:36`), or a `Placement` into an `IntegrationScope` (`placement.proto`, "a move is a new placement and a close") | rare; dated spans exist for placements, not for `location` | sites 10 to 1,000 |
| region, building, floor | `Location` parent chain (`location.proto`) | rare | tens |
| tags | `TagConfig` tree (`tag.proto`), membership assigned by the operator; "anything scoped to an ancestor also matches everything tagged under its descendants" (`CONCEPTS.md:39`) | at will; no history message | tens of tags, 0 to 5 per device |
| integration, integration kind | `Binding` joins device to `Integration`; "A Binding joins one Device to one Integration and never changes either end" (`CONCEPTS.md:9`); kind from `IntegrationConfig.kind` or `kind_name` (`integration.proto:60-66, 131-140`) | never per binding | 1 to 20 integrations per tenant |
| role | no field exists in `device.proto`, `tag.proto`, or `attribute.proto` (grep for `role` over the inventory package returns nothing device-related) | n/a | a role is a tag or an `Attribute` assignment; schema gap for the plan |
| edge | `Provenance.edge` (`provenance.proto`) | rare | tens |

### 2.2 Stamp versus dictionary versus join

The three mechanisms, with the guidance each rests on:

| Mechanism | Guidance | Fits |
| --- | --- | --- |
| Stamp on the fact row at write time | "denormalization is strongly recommended" for latency-sensitive queries (02 F23, joins page); Akvorado writes GeoIP and network attributes in its outlet, PostHog put person properties on events because "JOINs in ClickHouse are expensive and this frequently caused memory errors for our largest users" and accepts that "we simply look at the events which have the data from the event processing time" (`https://posthog.com/blog/persons-on-events`), Glaber writes host and item names on every row (03 patterns) | attributes whose **as-of** value is the analytical truth and which change slowly: site, firmware |
| Dictionary (`dictGet` per row) | "It is easier and more efficient to use dictionaries with functions than a `JOIN` with reference tables" (`https://clickhouse.com/docs/sql-reference/dictionaries/index.md`); hashed layouts are "completely stored in memory in the form of a hash table" (`.../layouts/hashed`); dictionaries "don't allow duplicate keys" and a one-to-many lookup "will result in silent data loss" (02 F23); Akvorado dropped its `networks` dictionary because it "still holds a copy of the GeoIP databases in ClickHouse memory" (03) | one-to-one attributes read as **current**: vendor, model, hardware revision, hostname, lifecycle, site name and region, integration kind; small tables (a few MB per 20,000 devices) |
| Range dictionary (`complex_key_range_hashed`) | `dictGet('d', 'attr', id, date)` "returns the value for the range containing the date" (02 F68); 09 section 9.1 chose it for site at T | **as-of** attributes for a device that is not the row's device (the AP of a client row), and historical re-reads of rows written before a stamp existed |
| JOIN on a mirror table | "The runtime and memory consumption of JOINs grows proportionally with the sizes of the left and right tables"; "Avoid more than 3-4 joins per query" (`https://clickhouse.com/docs/best-practices/minimize-optimize-joins.md`); right side is built in RAM ("creates a hash table for it in RAM", joins page) | **one-to-many** dimensions (tags) where the row must fan out, and ad hoc as-of questions (`ASOF JOIN`) |

### 2.3 Minimal stamped set and its cost per row

| Column | Type | Source at the sink | Semantics | Compressed cost per row (inference) |
| --- | --- | --- | --- | --- |
| `site_id` | `LowCardinality(String)` | sink's device map from the KV placement and location buckets (09 section 9) | site when observed | 0.1 to 0.3 B: constant inside a device's key run, dictionary index under ZSTD; measured in section 7 |
| `software_version` | `LowCardinality(String)` | sink's device map from the KV `DeviceState` bucket (same refresh as `site_id`) | firmware when observed | same |
| `retention_class` | `Enum8` | tenant plan | not a dimension; stays | 0.1 B |

Both stamps describe **the row's device**. For rows about another entity (client rows name the AP by `ap_key`, link rows have two ends), the stamp is the site of the row's device (`client_samples.site_id` is the reporting device's site, which is also the AP's site in every practical case) and the firmware comes from the range dictionary (Q4).

Why not stamp `firmware_fingerprint` instead: it is on the envelope (`Provenance.firmware_fingerprint`), so the sink needs no map lookup, and it is exact per record. Against it: analysts group by the human version string; the fingerprint is a 1 to 128 character identity probe output whose mapping to `software_version` lives nowhere in ClickHouse (the `DiscoveryCompleted` and `FirmwareEpochChanged` audit arms carry fingerprints, `operation_event.proto:127-140`, and `device_software_images` carries versions; nothing joins them). Recommendation: stamp `software_version`; keep `firmware_fingerprint` on the audit table only (09 4.6). If the plan later wants exactness, a `firmware_dim (tenant_id, fingerprint) -> version` dictionary fed by the device service closes the gap.

Why not stamp vendor and model: they never change for a device id (2.1), so `dictGet` on the current dictionary is as-of by construction, and stamping them adds 40 to 100 bytes per row uncompressed across hundreds of millions of rows per day for no analytical gain. The per-row compressed cost would also be near zero (constant runs), so the real cost is the sink map's width and the schema's width, not disk; the decision rests on "current equals as-of", not on bytes.

Why not stamp tags: one-to-many (a row cannot carry a set as a `LowCardinality` scalar), current-only semantics ("cohort as defined now"), and no history exists to make a stamp meaningful. An `Array(LowCardinality(String))` stamp would compress well but would freeze a tag set that the operator edits at will.

### 2.4 Dictionary layouts and sizes

All mirrors are ClickHouse tables written by the same projector that writes `entity_events` (09 section 9: "there is no Postgres mirror to keep in step"), `ReplicatedReplacingMergeTree(ver)` on the KV revision, and exposed as dictionaries with `SOURCE(CLICKHOUSE(QUERY '... FINAL'))`, `LIFETIME(MIN 300 MAX 360)` (02 F68: "During updates, the old version of a dictionary can still be queried").

```sql
CREATE TABLE flowseer.device_dim_table
(
    tenant_id         LowCardinality(String),
    device_id         UUID,
    vendor            LowCardinality(String),
    model             LowCardinality(String),
    hardware_revision LowCardinality(String),
    sys_object_id     LowCardinality(String),
    software_version  LowCardinality(String),     -- current
    hostname          String,
    serial            String,
    lifecycle         UInt8,                      -- DeviceLifecycle number
    site_id           LowCardinality(String),     -- current
    primary_integration_id UUID,                  -- the binding the device service names first, zero when none
    tag_ids           Array(LowCardinality(String)),  -- effective set including ancestors, current
    ver               UInt64
)
ENGINE = ReplicatedReplacingMergeTree(ver)
ORDER BY (tenant_id, device_id);

CREATE DICTIONARY flowseer.device_dim
(
    tenant_id String, device_id UUID,
    vendor String, model String, hardware_revision String, sys_object_id String,
    software_version String, hostname String, lifecycle UInt8, site_id String,
    primary_integration_id UUID, tag_ids Array(String)
)
PRIMARY KEY tenant_id, device_id
SOURCE(CLICKHOUSE(QUERY 'SELECT tenant_id, device_id, vendor, model, hardware_revision, sys_object_id, software_version, hostname, lifecycle, site_id, primary_integration_id, tag_ids FROM flowseer.device_dim_table FINAL'))
LAYOUT(COMPLEX_KEY_HASHED())
LIFETIME(MIN 300 MAX 360);
```

Whether `Array(String)` is accepted as a dictionary attribute type on 26.8 is unverified in this pass; the fallback is a `device_tags_effective` table (below) with no array attribute.

```sql
CREATE TABLE flowseer.device_tags_effective   -- one row per (device, tag or ancestor tag), current
(
    tenant_id LowCardinality(String), device_id UUID, tag_id LowCardinality(String), direct UInt8, ver UInt64
)
ENGINE = ReplicatedReplacingMergeTree(ver)
ORDER BY (tenant_id, tag_id, device_id);

CREATE TABLE flowseer.device_software_history   -- version epochs per device, from device_software_images running = 1 or from DeviceState diffs
(
    tenant_id LowCardinality(String), device_id UUID, version LowCardinality(String),
    effective_from DateTime, effective_until DateTime,   -- 0 = open
    ver UInt64
)
ENGINE = ReplicatedReplacingMergeTree(ver)
ORDER BY (tenant_id, device_id, effective_from);

CREATE DICTIONARY flowseer.device_software_dict
(
    tenant_id String, device_id UUID, version String,
    effective_from DateTime, effective_until Nullable(DateTime)
)
PRIMARY KEY tenant_id, device_id
SOURCE(CLICKHOUSE(QUERY 'SELECT tenant_id, device_id, version, effective_from, if(effective_until = 0, NULL, effective_until) AS effective_until FROM flowseer.device_software_history FINAL'))
LAYOUT(COMPLEX_KEY_RANGE_HASHED(range_lookup_strategy ''max''))
RANGE(MIN effective_from MAX effective_until)
LIFETIME(MIN 300 MAX 360);
```

`site_dim` (`tenant_id, site_id` to `name, region_id, parent chain`), `integration_dim` (`tenant_id, integration_id` to `kind_name, name, edge_id`), and `tag_dim` (`tenant_id, tag_id` to `name, parent_id, ancestor_ids`) follow the `device_dim` shape with `COMPLEX_KEY_HASHED`. `device_site_dict` is 09 section 9 unchanged.

Sizing (inference from "completely stored in memory in the form of a hash table", default `max_load_factor` 0.5, "Valid values: [0.5, 0.99]"):

| Dictionary | Entries at 20,000 devices per node | Bytes per entry (estimate) | Memory | At 100,000 devices per node |
| --- | --- | --- | --- | --- |
| `device_dim` | 20,000 | key 36 + 16, nine attributes about 150, tag array about 60, hash slot overhead at load factor 0.5 about 2x: about 500 | 10 MB | 50 MB |
| `device_site_dict` (ranges) | 20,000 x (1 + moves, about 1.1) | about 150 | 3 MB | 17 MB |
| `device_software_dict` (ranges) | 20,000 x versions in retention (about 6 over 2 years) | about 150 | 18 MB | 90 MB |
| `site_dim`, `integration_dim`, `tag_dim` | thousands | about 300 | under 2 MB | under 5 MB |

Thousands of tenants are inside these numbers because the key leads with `tenant_id` and the device count is the node's total, not per tenant. The brief's "millions of devices across the cluster" are spread over nodes, and each node's dictionaries hold its own devices; a 1 shard x 2 replica layout holds all of a deployment's devices on both nodes, so "per node" is "per deployment" today and the 100,000 column is the headroom. `max_load_factor = 0.75` halves the slot overhead if memory matters; `sparse_hashed` "uses less memory in favor more CPU usage" and is the fallback above a few hundred MB. `dictGet` is "non-deterministic" (ext-dict-functions page), which matters for the query cache (section 5.3).

### 2.5 Time validity summary

| Question | Mechanism | Correct when |
| --- | --- | --- |
| Site or firmware of the row's device when the row was observed | stamp (`site_id`, `software_version`) | always, modulo the sink map's refresh lag (benchmark consistency check, 09 section 9.1) |
| Site or firmware of another device at the row's time | `dictGet(range dict, ..., ts)` | history table complete; overlapping ranges resolved by `range_lookup_strategy` |
| Current vendor, model, hostname, lifecycle, site name, integration kind | `dictGet(hashed dict)` | always (immutable or current-by-definition) |
| Cohort by tag | `device_id IN (SELECT device_id FROM device_tags_effective WHERE tag_id IN ancestors-closed set)` or `arrayJoin(dictGet(..., 'tag_ids', ...))` | current membership only; the schema has no tag history |
| Ad hoc as-of with arbitrary history | `ASOF JOIN` against the mirror (02 F42; `hash` or `full_sorting_merge` only) | right side bounded by the tenant's history rows |

## 3. Cross-domain join contract

### 3.1 Shared columns

Every table that carries the concept uses exactly this name and type, so a join or `UNION ALL` needs no cast:

| Column | Type | Grain and meaning | Tables that deviate today |
| --- | --- | --- | --- |
| `tenant_id` | `LowCardinality(String)` | first key column everywhere | none |
| `device_id` | `UUID` | the row's device; zero UUID when unresolved | 05 client tables use `ap_key String` for the AP (change 6 adds `ap_device_id UUID`); 09 `entity_events` has `device_id` for device-scoped kinds only (fine) |
| `interface_name` | `LowCardinality(String)` | device-local name, `Interface.name` | 02 12.1 `ifindex` (already corrected by 04 D2, 10 G1); 06 `fdb_presence.interface_name` and `lldp_presence.local_interface_name` (rename to `interface_name` is not required: the LLDP column names the local end explicitly and joins spell it out) |
| `mac` | `UInt64` | EUI-48 in the low 48 bits | codec differs (05 `T64`, 06 `ZSTD`); type identical, no join impact; section 6 picks `T64` per 05's correction |
| `ts` | `DateTime` (samples, changes, presence inserts, probes), `DateTime64(3)` (events: syslog, traps, audit) | the key time; **samples, changes, probes: `Provenance.observed_at`**; **events: `received_at`** at the collector, with `sent_at` (syslog) or `device_ts` for the device clock | 09 audit tables use `occurred_at`; 07 alarms use `ts` = observed_at with `raised_at` as the instance key; 10 probes use `interval_start`. These stay: they are not the same concept and a join across them is always by bucket |
| `hour` | `DateTime` (`toStartOfHour`) | hourly rollup bucket; also hourly presence (05) | none |
| `day` | `Date` | daily rollup bucket and daily presence | 06 and 08 presence tables use `bucket Date` (change 8 renames to `day`) |
| `site_id` | `LowCardinality(String)` | site of the row's device at write time | missing on 06 tables, 09 `link_events`, 05 `radio_hourly`, 10 `probe_hourly` (change 2) |
| `software_version` | `LowCardinality(String)` | firmware of the row's device at write time | new everywhere (change 1) |
| `record_id` | `UUID` | the record; in the key of changes and events, absent from samples (02 F45) | none |

Time semantics for joins: a join across domains uses a **bucket** (`hour` or `day`) computed from each table's own key time. Samples are observed time and events are received time, so a sample bucket and an event bucket can disagree by the delivery lag (seconds to minutes normally, up to the edge buffer's replay window after an outage). For hourly joins that lag is inside the bucket except at the boundary; the analyst who needs exactness widens the event side by one bucket (`hour BETWEEN h - 1 AND h`) and dedups. Partitioning stays on the key time (07 E6: never on the device clock).

### 3.2 Rollup grain alignment

| Grain | Tables | Partition | Fleet-scan rule |
| --- | --- | --- | --- |
| raw (poll interval, 2 to 10 min) | `*_samples`, `*_changes`, `*_events`, `*_presence` inserts, `probe_intervals` | week (samples, changes, probes), day (syslog), month (events, presence) | per device or device list only; fleet-wide allowed inside **one week** with `max_rows_to_read` (section 4.1) |
| hour | `*_hourly`, `*_hourly_by_time`, `neighbor_presence`, `client_presence`, `client_lookup`, `probe_hourly`, `syslog_hourly`, `protocol_transitions_hourly` | month | fleet-wide up to **7 days** (`E x 168`) |
| day | `*_daily` (every samples family after change 3), `fdb_presence`, `fdb_vlan_daily`, `*_device_daily`, `binding_state_daily`, `syslog_daily`, `device_software_images` | month (change 3 moves daily rollups from `toYear` to `toYYYYMM`) | fleet-wide over **weeks to years** (`E x days`) |
| 5 min | none built; raw is the 5-minute tier for SNMP (04 3.5); controller families polled at 2 to 3 min may add `toStartOfFiveMinutes` rollups later | | |

The 5 min / hour / day ladder the brief names is therefore raw / hour / day in this store, and a rollup never feeds another rollup (03 anti-patterns, SigNoz chained MVs): hourly and daily are both fed from raw.

### 3.3 Which joins run in-database

| Join | Algorithm | Right side bound | Verdict |
| --- | --- | --- | --- |
| rollup x rollup on `(device_id[, interface_name], hour or day)` inside one tenant (Q3, Q12, Q13) | `parallel_hash` ("splits the data into buckets and builds several hashtables"); the planner puts the smaller side right | smaller side's rows in the window (`E x buckets` after the subquery's `HAVING`) x row width; cap with `max_bytes_in_join` and `join_overflow_mode = 'break'` in the profile | supported; two-table joins are the norm |
| rollup x changes or events bucketed (Q3) | `parallel_hash` | tenant change rows in the window | supported |
| rollup x mirror table (tags, module identity) (Q14) | `parallel_hash`, or `direct` when the right side is a dictionary ("performs a lookup in the right table using rows from the left table as keys", default `join_algorithm = direct,parallel_hash,hash,ie_join`) | mirror rows for the tenant | supported; the one-to-many fan-out is intended |
| as-of (`ASOF JOIN`) rollup x history | `hash` or `full_sorting_merge` only (02 F42) | history rows for the tenant | supported for ad hoc; the range dictionary is the production path |
| three or more fact tables | `UNION ALL` of per-table bucketed aggregates, then `GROUP BY (device_id, bucket)` with `-If` or `anyIf` per source | sum of the sides' rows in the window | preferred over chained joins ("Avoid more than 3-4 joins per query") |
| cross-tenant | none | | not a product query (04 5.8) |
| raw x raw fleet-wide | none | | out of budget; bucket first |

`UNION ALL` shape for a device-hour timeline across domains:

```sql
SELECT device_id, hour,
       maxIf(v, src = 'cpu') AS cpu_max, sumIf(v, src = 'errors') AS errors, sumIf(v, src = 'log_err') AS log_errors
FROM (
    SELECT 'cpu' AS src, device_id, hour, max(cpu_avg_basis_points_max) AS v FROM flowseer.device_hourly WHERE tenant_id = {t:String} AND hour >= {a:DateTime} GROUP BY device_id, hour
    UNION ALL
    SELECT 'errors', device_id, hour, sum(argMaxMerge(in_errors_last) - argMinMerge(in_errors_first)) FROM flowseer.interface_hourly WHERE tenant_id = {t:String} AND hour >= {a:DateTime} GROUP BY device_id, hour
    UNION ALL
    SELECT 'log_err', device_id, hour, sum(messages) FROM flowseer.syslog_hourly WHERE tenant_id = {t:String} AND hour >= {a:DateTime} AND severity <= 'err' GROUP BY device_id, hour
)
GROUP BY device_id, hour;
```

Each arm is an in-order or small hash aggregate over its own rollup; the outer `GROUP BY` holds `D x H` groups (7.2 M for 30 days) of three numbers. No hash table of a whole side is built.

## 4. Fleet-wide scans per pattern

### 4.1 Samples

The raw table keyed `(tenant, device, entity, ts)` with weekly partitions gives a tenant-wide scan of window W the cost `E x ceil(W / week) x week / I` rows plus active parts: a 24-hour fleet scan on `interface_samples` for the 10,000-device tenant reads a full week of the tenant, 480,000 x 2,016 = 968 M rows, for 138 M needed. 04 D10 already sends 24-hour scope queries to `interface_hourly_by_time`. For OLAP the rule is:

| Window | Table | Rows (10,000-device tenant, interfaces) | Why not the next grain down |
| --- | --- | --- | --- |
| under 1 week, fleet-wide | `*_hourly_by_time` (04 D9) for 1 day; `*_hourly` entity-first for 2 to 7 days | 11.5 M per day | raw is 7 to 70x more |
| 1 week to 90 days | `*_daily` (monthly partitions) | `E x days`, 43 M at 90 days | hourly is 24x more (1 G rows at 90 days) |
| 90 days to years | `*_daily` | `E x days` | nothing finer exists; p95-of-hours questions over a year read `E x 8,760` hourly = 4.2 G and must be scoped (site or model device list) |
| any window, device list of D | raw for W under 48 h (05 Q2), else hourly or daily | `D x E_dev x W / I` | 04 section 5 |

Entity-first versus time-first for OLAP (O1): with `PARTITION BY toYYYYMM(hour)` the entity-first hourly table holds, per partition, each entity's rows for one month contiguously; a 90-day window touches 3 or 4 partitions and reads all of their rows for the tenant, which is `E x 2,160` to `E x 2,880`. The time-first table reads exactly `E x 2,160`. The difference is at most one boundary month (33 % at 90 days, 4 % at a year), and the time-first table costs a second MV target with the same rows (04 section 7: "x2 orderings"). Decision: the time-first companion stays only where 04 placed it (hourly, for the 24-hour monitoring scope query); daily rollups get **no** time-first companion, and the analyst reads entity-first daily tables. A window under one week on the hourly table is the monitoring path's business and already has the companion.

Daily rollup contents for OLAP (change 3): the same aggregate set as the hourly table (04 3.5: `argMin`/`argMax` by `ts`, `min`, `max`, `uniqExact(ts)` samples, `groupBitOr(present_mask)`), keyed `(tenant_id, device_id, entity..., day, discontinuity_at, software_version)`, `anyLast(site_id)`. Adding `software_version` to the key makes an upgrade day two rows, which keeps cohort sums exact (Q1). Rows: one twelfth of the hourly table per ordering; storage about 0.2 to 0.4 GB/day at 20,000 devices for interfaces (04 section 7 scaled).

Gauge families (radio, sensors, PoE, optics, cellular, CPU) need `max`, `min`, and `avgState` (or `sum` + `uniqExact` count) in both grains; distributions across hours (Q2, Q11) come from hourly rows, never from a per-row quantile sketch in the rollup, because a sketch per entity-hour would multiply the rollup's bytes by about 20 (inference from the `quantileTDigest` state holding up to about 100 centroids).

### 4.2 Changes

Rows are sparse (04 3.2, 08 section 8: about 1.1 M per day fleet-wide in `protocol_transitions`, tens of thousands in `interface_changes`). A fleet scan over months reads the tenant's rows in the window's weekly partitions plus the `minmax` on `ts` inside them: `cost = tenant change rows in W + boundary weeks`. For the 10,000-device tenant that is under 100 M rows a year even in `protocol_transitions` (08 section 8: 0.55 M daily snapshot rows fleet-wide are the bulk; `kind = 'transition'` filtering leaves a fraction). Changes need no companion; they need the `*_hourly` count rollups 08 section 9 already defines (`uniqExact(record_id)` per `(device, protocol, attribute, hour)`), and `interface_changes` gets the same (`interface_changes_hourly`, `uniqExact(record_id)` per `(device, interface_name, attribute, hour)`), which Q3 can use in place of the raw changes scan when the window grows.

Snapshot rows (`kind = 'snapshot'`, daily per entity in `protocol_transitions`) are the one changes component that scales with entities, not change: `E x days`. Every OLAP query on a changes table filters `kind = 'transition'` so the snapshot rows are read (same granules) but skipped; that is a 1x to 2x cost factor, bounded.

### 4.3 Presence

Presence tables are their own rollup (10 section 7). Fleet-wide cost is `members x buckets`: FDB daily presence for 10,000 switches x 300 MACs x 90 days = 270 M rows, which is at the budget's edge; client hourly presence for 30,000 clients x 24 x 60 x 1.3 = 56 M (Q4). Two rules:

- Member-level OLAP (distributions over members: RSSI per model, session length per SSID) reads the presence table at its grain and is bounded as above; the analyst narrows the window or the scope when `members x buckets` exceeds the budget.
- Count-level OLAP (growth, churn, members per VLAN) reads the `*_device_daily` count rollups (06 7.1, 08 section 9) with `uniqExact` states. `fdb_vlan_daily` (change 4) adds the VLAN axis.

Key order for in-order aggregation: `fdb_presence` is keyed `(tenant, device, day, instance, vlan, mac, interface)` and `client_presence` `(tenant, ap_key, bssid, mac, hour)`; a GROUP BY that starts with `device_id, day` or `ap_key, bssid, mac, hour` streams. A GROUP BY `(mac, ...)` on `fdb_presence` does not; it goes to `fdb_presence_by_mac`.

### 4.4 Events

Syslog is the volume problem: `D x 1,700 x W` rows (07 E18) make any fleet scan on the raw table over more than a day exceed the budget (17 M rows per day for the 10,000-device tenant, 510 M for 30 days). The text index serves selective token searches at any window (bounded by matching granules, E18), and everything else goes to `syslog_hourly` (E20) for windows up to 7 days and `syslog_daily` (change 5) beyond. The `(device, severity, vendor_tag)` key of both rollups is the dimension set an analyst groups syslog by; message-text analytics over months stay a search, not a scan.

Traps, alarms, client events, entity events, and audit are small (07, 09 volumes) and their raw tables are OLAP-readable over their full retention with partition pruning; `alarm_intervals` (Q5) and the `*_daily` count tables (09 section 7) are the rollups they need. The 09 Q3 activity feed projection becomes an MV table `entity_events_by_time` ordered `(tenant_id, ts, entity_kind, entity_id)` without the `before`/`after` bytes (O6).

### 4.5 Probes

`probe_intervals` is 29 M rows per day with arrays (10 4.3) and a 30 to 90 day TTL; the analyst never reads it fleet-wide. `probe_hourly` (`quantilesTiming` states, `sum`, `min`, `max`) serves windows to 90 days at `D x targets x 2,160` = 43 M rows for the large tenant, and a `probe_daily` (change 3) with the same columns serves longer windows at `D x targets x days`. The Timing state merges across devices for site and tenant percentiles (Q22), which the per-row RTT arrays could not do without `ARRAY JOIN`.

### 4.6 Projections versus MV-fed tables

| Criterion | Projection (normal) | Projection index (`_part_offset` only) | MV-fed second table |
| --- | --- | --- | --- |
| Storage | full copy of the selected columns, "writing data twice" | sorting key plus offset, "works like an index", "reads the actual data from the base table" (`https://clickhouse.com/docs/data-modeling/projections.md`; 25.5 and 25.6 release notes) | full copy of the selected columns |
| Parallel replicas | "Projections aren't used together with Parallel replicas" (`https://clickhouse.com/docs/deployment-guides/parallel-replicas.md`) | same | used |
| `FINAL` | "Projections are not supported in the SELECT statements with the FINAL modifier" (mergetree.md) | same | used |
| `optimize_read_in_order` | "isn't supported for projections" | same | newest-first works |
| ReplacingMergeTree or AggregatingMergeTree base | every row-dropping or row-combining merge needs `deduplicate_merge_projection_mode`: "throw (default): An exception is thrown", "rebuild: The affected projection part is rebuilt" (`https://clickhouse.com/docs/sql-reference/statements/alter/projection.md`); rebuild on every merge | same | the second table merges on its own key; no rebuild |
| Lightweight DELETE (tenant forget, 02 F57) | "Lightweight updates and deletes aren't supported for tables with projections" unless `lightweight_mutation_projection_mode` is `drop` or `rebuild` | same | works |
| TTL | shares the parent's; a TTL delete merge is a row-dropping merge (rebuild) | same | own TTL (daily rollups outlive raw) |
| Transparency | "the most transparent option" (sparse index guide); query routing automatic | automatic | the query builder picks the table |

Verdict (O6): no projection on any analyst-visible table. The projection index is attractive for a secondary key on a plain `MergeTree` (it exists since 25.5 and "preserves its value through merges and mutations"), but every FlowSeer table is Replacing or Aggregating, where each merge triggers a rebuild, and the analyst path depends on parallel replicas. The benchmark keeps one projection-index variant (`entity_events` by time) to put a number on the rebuild cost, and the plan can revisit when a plain-MergeTree table appears.

## 5. Analytics workload isolation

### 5.1 Users, profiles, workloads

Three users, each with a settings profile whose limits are `CONST` or `MAX` constrained (01 section 5), and a workload pinned per user:

| | `sink` (writer) | `svc_query` (API, 02 F56) | `analyst` (BI, ad hoc) |
| --- | --- | --- | --- |
| `workload` | `ingestion` | `api` | `analytics` |
| `max_execution_time` | n/a | 60 s, `timeout_overflow_mode = 'break'` for charts | 600 s, `throw` |
| `max_memory_usage` | insert block bound | 25 % of RAM | 40 % of RAM; `max_memory_usage_for_user` 50 % |
| `max_rows_to_read` | n/a | sized to the largest monitoring scan (about 50 M) | 2 G, `read_overflow_mode = 'throw'` |
| `max_threads` | default | half the cores | half the cores |
| `max_concurrent_queries_for_user` | n/a | tens | 4 |
| `max_bytes_ratio_before_external_group_by` | n/a | default 0.5 | 0.3 (spill earlier; "if there was less than ... the query runs just as fast as without external aggregation", group-by page) |
| `max_bytes_ratio_before_external_sort` | n/a | default 0.5 | 0.3 |
| `optimize_aggregation_in_order` | n/a | 0 (default) | 1 (default is 0; the setting's own page was not located in this pass, unverified semantics beyond its name) |
| `join_algorithm` | n/a | `direct,parallel_hash,hash` | `direct,parallel_hash,grace_hash` with `max_bytes_in_join` = 2 GB, `join_overflow_mode = 'break'` for exploratory sessions, `'throw'` for scheduled reports |
| `use_query_cache` | n/a | 1 for dashboard rollup queries with bucketed `now()` (02 F60) | 1, `query_cache_ttl` 600, `query_cache_min_query_runs` 1 |
| `enable_parallel_replicas` | n/a | 0 | 1, `max_parallel_replicas` 2, `cluster_for_parallel_replicas` = the 1 x 2 cluster |
| quota (`quota_key` = tenant) | n/a | per tenant hourly `read_rows`, `execution_time` | per analyst user |

Workloads (quotes from `https://clickhouse.com/docs/operations/workload-scheduling.md`): `CREATE RESOURCE cpu (MASTER THREAD, WORKER THREAD)`; `CREATE WORKLOAD all SETTINGS max_concurrent_threads = <2 x cores>`; `CREATE WORKLOAD ingestion IN all SETTINGS priority = 0`; `CREATE WORKLOAD api IN all SETTINGS priority = 1`; `CREATE WORKLOAD analytics IN all SETTINGS priority = 2, max_concurrent_threads = <cores - 4>`. "sibling workloads are served according to static values (lower value means higher priority)"; declaring a CPU resource disables the soft-limit settings and "corresponds to `concurrent_threads_scheduler` setting set 'fair_round_robin' value"; slot scheduling does not guarantee fair CPU time "unless server setting `cpu_slot_preemption` is set to `true`", so the server sets it. "CPU scheduling is not supported for merges and mutations yet" and "Memory reservation scheduling is experimental", so merges are not in the hierarchy (their IO is, through `merge_workload`) and memory is bounded by the profile, not the workload. The query-level `priority` setting's reference page could not be located (unverified; the workload priority is the mechanism this report relies on).

### 5.2 Parallel replicas on 1 x 2

"The coordinator splits the workload into a set of granules that can be assigned to different replicas" and each replica "will be able to request a new task"; limitations: "disabled with FINAL", "Projections aren't used", "Complexity layers like CTEs, subqueries, JOINs ... can have a negative impact", and small queries pay coordination. On two replicas the ceiling is 2x for the stage-1 scan of every query in section 1, which is where the time goes (tens of millions of rows). 02 F62 worried that parallel replicas "would then load the node the sink is inserting into"; on 1 x 2 both nodes replicate every insert and run every merge, so there is no insert-free node to protect, and the workload priority plus `cpu_slot_preemption` is what protects ingestion on both. Decision: on for `analyst`, off for `svc_query`. A query with `FINAL` (rare: exact alarm MTTR, 07 3.4) runs single-replica.

Status: "moved to the Beta tier in 24.10" (02 F62); 26.9's `parallel_replicas_plan_based` is not in 26.8 LTS. The benchmark (section 7) measures the gain on Q1, Q2, Q6 and the cost on Q3 (a join).

### 5.3 Query cache

"entries in the query cache are not shared between users due to security reasons", `query_cache_ttl` "By default, this period is 60 seconds", and results of queries with non-deterministic functions are not cached unless `query_cache_nondeterministic_function_handling` says so. `dictGet` "is non-deterministic" (ext-dict-functions page), so every query of section 1 that enriches through a dictionary is **not cached by default**. For dashboards built on these queries set `query_cache_nondeterministic_function_handling = 'save'` on the `analyst` profile with a TTL at or below the dictionary `LIFETIME` (300 to 360 s), so a cached result is never staler than the dictionary it was built from; and bucket the window arguments (`toStartOfHour(now())`) so the AST repeats. Per-user size caps (`query_cache_max_size_in_bytes`, `query_cache_max_entries`) keep an analyst from evicting the API's entries.

### 5.4 A separate analytics replica

Not warranted at the start. Every replica holds all data and runs all merges, so a third replica buys isolation of **query CPU and page cache** only, at the price of a full copy, a third set of merges, and Keeper traffic. Trigger to add it: after the workload hierarchy is in place, if either (a) insert acknowledgement p99 (sink-side) rises above 2x its quiet baseline while analyst queries run, or (b) analyst p95 for the section 1 set exceeds the SLO the plan sets, measured over a week from `system.query_log` with `workload` as the dimension. Until then, pin `analyst` connections to replica 2 through the load balancer (plain replica choice, not parallel replicas) so page cache for rollups lives on one node and the API's raw-table reads on the other.

### 5.5 Predictable scaling statements for OLAP

For every query in section 1 and every future query built the same way:

1. **Rows read in stage 1 = the tenant's entities of the family x buckets of the window at the chosen grain x parts overlapping the window**, rounded up to whole granules and to whole partitions at the window's edges. Nothing in it is the number of tenants, the table's total rows, or the retention length, because the tenant prefix of every sort key bounds the key range and the partition minmax bounds the time range.
2. **Memory in stage 1 is bounded by groups in flight, not by rows**, when the GROUP BY is a prefix of the sort key with `optimize_aggregation_in_order = 1`; otherwise it is `groups x state size` and the profile's external-aggregation ratio spills it to disk at a 3x time cost (group-by page: "approximately three times").
3. **Stage 2 is bounded by the product of the dimension cardinalities**, hundreds to thousands, independent of the window.
4. **Joins are bounded by the smaller side's rows in the window** after its own aggregation, and a profile cap turns an overrun into an error, never a stall.
5. **The exceptions** are stated per table in the dossiers and unchanged here: bloom-filter lookups by a member key across a tenant (cost grows with the tenant's rows in the window), `dictGet` in a `WHERE` (full tenant range, Q23 remedy), and `FINAL` (single replica, all parts).
6. **Grain selection is the budget rule**: with a budget of 100 M stage-1 rows per analyst query (an inference for about 10 s on 8 cores at 10 to 20 M rows per core-second for state merges, to be measured), the ladder is raw inside one day with a device list, hourly inside 7 days, daily beyond. The query builder for BI picks the grain from the window, and an analyst writing SQL by hand gets the `max_rows_to_read` error with the ladder in its message.

## 6. Changes to the pattern set and the dossiers

| # | Change | Where | Why |
| --- | --- | --- | --- |
| 1 | **Add `software_version LowCardinality(String)` beside `site_id` on every fact table** (samples, changes, presence, events, probes) and to every rollup as a key column after the bucket (`..., day, discontinuity_at, software_version`) with `anyLast` for `site_id` | 04 3.1 to 3.5, 05 3.x, 06 5.x, 07 1.3, 2.3, 3.3, 08 5.x, 09 4.x, 10 2.3 to 5.2; the 02 section 12 conventions paragraph | O3, Q1, Q23: as-of firmware is the one slowly changing attribute that cohorts need and that the current dictionary cannot give |
| 2 | **Stamp `site_id` where it is missing**: `fdb_presence`, `fdb_presence_by_mac`, `fdb_transitions`, `set_polls`, `lldp_presence`, `cdp_presence`, ARP/ND `neighbor_presence` (06); `link_events` as `a_site_id` (09 4.3); `radio_hourly` and `ssid_clients_hourly` already has it (05 3.7); `probe_hourly`, `poe_port_hourly`, `optics_lane_hourly`, `cellular_hourly` (10) | 06, 09, 10 | Q6, Q19, Q22 group by site without a device list; the stamp is the as-of site |
| 3 | **A daily rollup for every samples family**, same aggregates as hourly, `PARTITION BY toYYYYMM(day)` (04 D19 says `toYear(day)`; change to monthly so a 90-day window reads 3 to 4 months, not a year), entity-first only, TTL 5 y: `interface_daily` (exists), `ethernet_daily`, `device_daily`, `storage_daily`, `component_daily`, protocol counter dailies (04); `radio_daily` (05); `stp_bridge_daily`, `ntp_association_daily` (08 has `route_table_daily`); `poe_port_daily`, `pse_budget_daily`, `optics_lane_daily`, `probe_daily`, `cellular_daily` (10) | 04 3.5, 05 3.7, 08 9, 10 2.3, 3.3, 4.3, 5.2 | O1, section 4.1: fleet scans over weeks need `E x days`, and the hourly table is 24x that |
| 4 | **`fdb_vlan_daily`**: `(tenant_id, device_id, vlan_id, day)` with `uniqExactState(mac)` as `members`, `anyLast(site_id)`, fed by an MV from `fdb_presence`, TTL 3 y, `PARTITION BY toYYYYMM(day)` | 06 section 7 | Q6 |
| 5 | **`syslog_daily`**: `SummingMergeTree` keyed `(tenant_id, device_id, severity, vendor_tag, day)` with `sum(repeat_count)` as `messages`, `anyLast(site_id)`, `anyLast(software_version)`, fed from `syslog`, TTL 3 y; `syslog_hourly` (E20) keeps 1 y | 07 E20 | Q8, Q9; the raw table is unreadable fleet-wide beyond a day |
| 6 | **Typed AP device id on wireless client tables**: `ap_device_id UUID` (zero when unresolved) beside `ap_key` on `client_samples`, `client_presence`, `client_lookup`, `ssid_clients_hourly`; `from_ap_device_id`, `to_ap_device_id` on `client_events` | 05 3.4 to 3.7 | Q4, Q15: `dictGet` and joins need the UUID, not a string that is sometimes a UUID and sometimes a name |
| 7 | **`binding_state_daily` carries `integration_id`** (`anyLast`) and `protocol`; `unreachable_seconds` as 09 section 7 states | 09 section 7 | Q7 |
| 8 | **Rename `bucket Date` to `day Date`** on 06 presence tables (`fdb_presence`, `fdb_presence_by_mac`, `lldp_presence`, `cdp_presence`, ARP/ND `neighbor_presence`, `fdb_device_daily`) and 08 presence tables; 05's hourly presence keeps `hour` | 06 5.x, 7.1; 08 5.4 | section 3.1: one name per grain across every table so joins and the BI model need no per-table mapping |
| 9 | **Replace the 09 Q3 projection with an MV table** `entity_events_by_time` ordered `(tenant_id, ts, entity_kind, entity_id, seq, record_id)` without `before`/`after`; same TTL | 09 Q3, 10.2 | O6: projections are unusable with parallel replicas and FINAL, and need `lightweight_mutation_projection_mode` for tenant forget |
| 10 | **`interface_changes_hourly`** (`uniqExactState(record_id)` per `(tenant_id, device_id, interface_name, attribute, hour)`) like `protocol_transitions_hourly` | 04 3.2 | Q3 at windows above a week |
| 11 | **Dimension mirrors and dictionaries** (section 2.4): `device_dim_table` + `device_dim`, `device_tags_effective`, `device_software_history` + `device_software_dict`, `site_dim`, `integration_dim`, `tag_dim`, written by the KV projector that writes `entity_events` (09 L2) | new, beside 09 section 9's `device_site_history` | section 2 |
| 12 | **`analyst` user, profile, and workload** (section 5.1), `cpu_slot_preemption = true` server setting, `enable_parallel_replicas` for the analyst only, `query_cache_nondeterministic_function_handling = 'save'` with a TTL at or below the dictionary lifetime | 02 F56 recommendation gains a third user | section 5 |
| 13 | **MAC codec**: `mac UInt64 CODEC(T64, ZSTD(1))` everywhere (06 uses `ZSTD(1)`), per 05's correction that T64 "crops unused high bits" and a 48-bit MAC is the case it serves | 06 5.1 to 5.3, 5.6 | consistency; type unchanged |
| 14 | **Optional**: `ssid_clients_site_hourly` keyed `(tenant_id, site_id, ssid, hour)` fed by a second MV from `client_presence` | 05 3.7 | Q16 at tenant scope without a 400,000-group HLL merge |

What does **not** change: the five patterns and their engines; tenant-first keys; raw tables' `ts` semantics; weekly and daily partitions on raw; no TTL GROUP BY; no chained MVs (daily rollups feed from raw); the no-projection rule (now universal); the `*_hourly_by_time` companions 04 defines (monitoring only).

Schema gaps this report adds to the dossiers' lists: no device **role** field exists (section 2.1); no message carries a **tag history** (as-of tags are impossible); no message maps `firmware_fingerprint` to `software_version` (section 2.3); `DeviceEvent` carries no before/after `DeviceState`, so `device_software_history` must be derived from `device_software_images` days or from a KV diff of `DeviceState.software_version` (09 4.4 already notes the first half).

## 7. Benchmark additions

Runs on the same single-node 26.8 container the dossiers specify, plus one two-replica compose variant (two ClickHouse containers, one Keeper) for the parallel-replica and isolation measurements, which a single node cannot produce.

### 7.1 Dataset dimensions (added to every lane's generator, seed 20261008)

| Dimension | Distribution | Why |
| --- | --- | --- |
| vendors | 6, Zipf (one vendor 50 % of devices) | realistic group-by skew |
| models | 40, 4 to 12 per vendor, Zipf within vendor; model fixes interface count (24, 48, 52, 500) and radio count (0, 2, 3) | Q1, Q10, Q11 group sizes |
| firmware | 3 to 5 versions per model; day 1 assignment 60/30/10; an **upgrade wave on day 15** moves 30 % of one vendor's devices to a new version over 48 hours; a second wave on day 45 for 10 % | Q1 cohorts, Q4, Q18, Q23; the `software_version` stamp flips mid-window |
| sites | large tenant: 200 sites, Zipf (one site 2,000 devices, median 20); small tenants: 1 to 5 sites | Q2, Q6, Q16, Q19, Q22 |
| site moves | 1 % of devices per month, uniformly in time | stamp versus dictionary consistency; as-of correctness |
| tags | 50 tags in a 2-level tree, 0 to 5 per device (mean 3), 5 % of devices re-tagged per month | tag cohort queries; `device_tags_effective` size |
| integrations | 5 kinds, 1 to 20 instances in the large tenant; each device bound to 1 (90 %) or 2 (10 %) | Q7 |
| behaviour coupling | error rate per model x firmware drawn from a seeded table so that Q1 has a known answer; reboot rate per model; PoE draw per model; RSSI offset per AP model | pass criteria compare query results with generator truth |
| syslog classes | 30 vendor tags with a per-vendor Zipf; link-flap tag correlated with `interface_changes` rows at 80 % | Q3, Q8, Q9 |

### 7.2 OLAP query set

Q1 to Q24 as written, each at two windows (7 and 90 days where the query allows), on the large tenant (10,000 devices) and on a median tenant (20 devices). Each query is run (a) as `analyst` single replica, (b) with `enable_parallel_replicas = 1` on the two-replica variant, (c) with `optimize_aggregation_in_order = 0` for Q1, Q5, Q6 to show the memory difference, and (d) twice in a row with the query cache on to measure the hit.

### 7.3 Scale steps

| Step | Data | Expected |
| --- | --- | --- |
| S1 | 1x: 200 tenants, 20,000 devices, 30 days | baseline |
| S2 | 4x days (120 days), same tenants | 90-day queries: `read_rows` grows with the window only where the window grows (Q1 at 90 days flat between S1-truncated and S2; at 120 days linear); 7-day queries flat |
| S3 | 16x: 120 days and 800 tenants, 80,000 devices, the large tenant unchanged | every query on the large tenant: `read_rows`, memory, p95 flat within 10 % of S2 |
| S4 | 1x data, tenant size 100, 1,000, 2,000, 10,000 devices | `read_rows` linear in `E_tenant`; stage-2 memory flat |
| S5 | isolation: Q1, Q2, Q6 in a loop as `analyst` on the two-replica variant while the generator inserts at the 1x rate | insert acknowledgement p99 and `parts_to_delay_insert` events compared with a quiet run |

### 7.4 Metrics

From `system.query_log` per query and step: `read_rows`, `read_bytes`, `memory_usage`, `query_duration_ms` p50 and p95 over 20 runs, `ProfileEvents['ExternalAggregationCompressedBytes']` (spill), `ProfileEvents['ParallelReplicasReadAssignedMarks']` (or the 26.8 equivalent, unverified name) for (b), `used_dictionaries`, and `query_cache_usage`. From `EXPLAIN indexes = 1`: parts and granules selected, partitions pruned. From `system.dictionaries`: `bytes_allocated`, `element_count`, `loading_duration` for every dictionary at S1 and S3. From `system.columns`: compressed bytes per row of `site_id` and `software_version` on `interface_samples` and `interface_daily`. From `system.parts` of the rollups: rows per day per ordering, to confirm the daily rollup is one twelfth of the hourly. Sink-side: insert acknowledgement latency histogram during S5. Consistency: stamped `software_version` versus `dictGet(device_software_dict, ..., ts)` over the upgrade-wave days (expected zero mismatches outside the dictionary reload window), and Q1's error rate per cohort versus the generator's table (expected within the counter-reset tolerance of 04 4.4).

### 7.5 Pass criteria

| Criterion | Threshold |
| --- | --- |
| stage-1 `read_rows` versus the section 1 cost model | within 2x for every query, both tenants |
| flatness across S2 to S3 (total data) | `read_rows` and p95 within 10 % for the large tenant |
| linearity across S4 (tenant size) | `read_rows` linear in `E_tenant` with slope `buckets(W, grain)` within 20 % |
| memory with in-order aggregation | Q1, Q5, Q6 under 1 GB at S3; without it, the spill metric is non-zero and p95 under 3x (group-by page's "approximately three times") |
| stamp cost | `site_id` plus `software_version` under 0.6 bytes per row combined on `interface_samples` |
| dictionary memory | `device_dim` under 20 MB at 20,000 devices and under 80 MB at 80,000; load under 5 s |
| parallel replicas | Q1, Q2, Q6 p95 at least 1.5x faster on two replicas; Q3 not slower than 1.2x single-replica (else parallel replicas stay off for joins) |
| isolation (S5) | insert acknowledgement p99 under 2x the quiet run; zero `parts_to_delay_insert` events; analyst queries complete (they may slow) |
| query cache | second run of each dashboard query under 50 ms with `query_cache_usage = Read` |
| correctness | Q1 cohort error rates within 5 % relative of generator truth; Q7 flags exactly the devices the generator marked; stamp versus range-dictionary mismatches zero outside the reload window |
| daily rollup size | rows per entity-day equal 1 (merged) with `software_version` constant, 2 on an upgrade day |

## 8. Unverified and open

1. `Array(String)` as a dictionary attribute type on 26.8 (section 2.4); fallback table given.
2. `optimize_aggregation_in_order` semantics and its interaction with a GROUP BY that appends a non-key column after a full key prefix (Q1): the setting's page was not located; only its default (0) was read from the settings index. The benchmark measures memory with and without it.
3. The query-level `priority` setting's reference page returned 404; the workload `priority` is the documented mechanism used here.
4. Per-row compressed cost of the two stamps (0.1 to 0.3 B) and dictionary bytes per entry (about 500 B) are inferences from the encoding, measured in section 7.
5. The 100 M-row stage-1 budget (about 10 s on 8 cores) is an inference, calibrated by the benchmark.
6. `ProfileEvents` names for parallel-replica mark assignment on 26.8.
7. Whether `dictGet` in a projection-free `WHERE` can use the `IN (subquery)` rewrite automatically (Q23 shows the manual form).
8. Parallel replicas' tier on 26.8 ("Beta tier in 24.10" per 02 F62; no later status statement was fetched).
9. The exact `SOURCE(POSTGRESQL(...))` syntax, should the plan prefer Postgres over the KV projector as the dictionary source (09 section 9.1 notes the same gap); the ClickHouse-table source avoids it.
10. `quantileTDigest` state size (used only to argue against per-row sketches in rollups, section 4.1).
