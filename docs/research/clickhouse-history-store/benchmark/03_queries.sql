-- Named benchmark queries. run.sh splits this file on "-- Q:" lines.
--
-- Block layout:
--   -- Q:<id> <pattern> <shape>
--   -- user: <svc_reader | analyst | analyst_t0 | analyst_partner | analyst_partner_sq | default>
--   -- kind: <measure | partner | check | dedup>        (measure when absent)
--   -- expect: <identical | differs>                     (dedup blocks only)
--   -- model: <cost model, prose>
--   -- model_sql: <one-line SELECT returning the modelled read_rows>
--   <the query, ending with ;>
--
-- Parameters (run.sh writes them to results/params.env once, after step s1; T0 data is
-- identical at every step, so the values hold for every step):
--   {t:String} T0 tenant 't0000'           {d:UUID} {i:String} one faulty active port in site s00
--   {now:DateTime} end of the measured window (UTC midnight)   {T:DateTime} now - 30 h + 7 min
--   {devices:Array(UUID)} the devices of site {site:String} (about 60 of T0's 200)
--   {mac:UInt64} a roaming MAC of T0       {model:String} {sw_a:String} {sw_b:String} cohort
--   {tenants:Array(String)} partner set P (t0000..t0019)
--   {region:UInt64} location_id of region 't0000-r0'
--   {probe_dev:UUID} {target:IPv6} the probing device and anchor target of site s00
--   {t1:String} {t1_dev:UUID} a device of tenant t0001 (dictionary leak check)
--
-- Cost-model notation (dossier 04 section 5): G = 8192 rows per granule, P = parts that
-- hold the key range (measured with uniqExact(_part) on the same predicate, so the model
-- carries the merge state of the moment), E = entities in scope, H = hours in window.
-- None of the model terms is the total table size or the tenant count.

-- ===========================================================================
-- Monitoring shapes (svc_reader, workload monitoring)
-- ===========================================================================

-- Q:M01 samples device-interface-newest-first
-- user: svc_reader
-- model: P x 2 x G (reverse read in key order stops at LIMIT 200; 04 section 5.1)
-- model_sql: SELECT greatest(1, uniqExact(_part)) * 2 * 8192 FROM flowseer.interface_samples WHERE tenant_id = {t:String} AND device_id = {d:UUID} AND interface_name = {i:String} AND ts >= {now:DateTime} - INTERVAL 7 DAY AND ts < {now:DateTime}
SELECT ts, oper_status, admin_status, speed_bps, in_bytes, out_bytes, in_errors, present_mask
FROM flowseer.interface_samples
WHERE tenant_id = {t:String} AND device_id = {d:UUID} AND interface_name = {i:String}
  AND ts >= {now:DateTime} - INTERVAL 7 DAY AND ts < {now:DateTime}
ORDER BY ts DESC
LIMIT 200;

-- Q:M02 samples raw-rate-chart-7d
-- user: svc_reader
-- model: P x 2 x G (2016 rows of one entity; 04 section 5.1)
-- model_sql: SELECT greatest(1, uniqExact(_part)) * 2 * 8192 FROM flowseer.interface_samples WHERE tenant_id = {t:String} AND device_id = {d:UUID} AND interface_name = {i:String} AND ts >= {now:DateTime} - INTERVAL 7 DAY AND ts < {now:DateTime}
SELECT ts,
       nonNegativeDerivative(in_bytes, ts, INTERVAL 1 SECOND) OVER w * 8 AS in_bps,
       nonNegativeDerivative(out_bytes, ts, INTERVAL 1 SECOND) OVER w * 8 AS out_bps
FROM flowseer.interface_samples
WHERE tenant_id = {t:String} AND device_id = {d:UUID} AND interface_name = {i:String}
  AND ts >= {now:DateTime} - INTERVAL 7 DAY AND ts < {now:DateTime}
WINDOW w AS (ORDER BY ts ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW)
ORDER BY ts;

-- Q:M03 samples what-at-T
-- user: svc_reader
-- model: P x 2 x G (04 section 5.2)
-- model_sql: SELECT greatest(1, uniqExact(_part)) * 2 * 8192 FROM flowseer.interface_samples WHERE tenant_id = {t:String} AND device_id = {d:UUID} AND interface_name = {i:String} AND ts <= {T:DateTime}
SELECT *
FROM flowseer.interface_samples
WHERE tenant_id = {t:String} AND device_id = {d:UUID} AND interface_name = {i:String}
  AND ts <= {T:DateTime}
ORDER BY ts DESC
LIMIT 1;

-- Q:M04 changes when-changed
-- user: svc_reader
-- model: P x G (one entity's change rows fit one granule)
-- model_sql: SELECT greatest(1, uniqExact(_part)) * 8192 FROM flowseer.interface_changes WHERE tenant_id = {t:String} AND device_id = {d:UUID} AND interface_name = {i:String}
SELECT ts, attribute, old_value, new_value
FROM flowseer.interface_changes
WHERE tenant_id = {t:String} AND device_id = {d:UUID} AND interface_name = {i:String}
ORDER BY ts DESC
LIMIT 50;

-- Q:M05 samples scope-24h-time-first-rollup
-- user: svc_reader
-- model: 24 x E_tenant + P x 2 x G (key prefix (tenant, hour) makes the tenant's 24 hours one range; the device list filters inside it; 04 section 5.3)
-- model_sql: SELECT 24 * (SELECT count() FROM bench.interfaces FINAL WHERE tenant_id = {t:String}) + greatest(1, uniqExact(_part)) * 2 * 8192 FROM flowseer.interface_hourly_by_time WHERE tenant_id = {t:String} AND hour >= {now:DateTime} - INTERVAL 24 HOUR AND hour < {now:DateTime}
SELECT device_id, interface_name, hour, discontinuity_at, software_version,
       min(in_bytes_min) AS f_in, max(in_bytes_max) AS l_in, max(in_bytes_max) AS m_in,
       min(out_bytes_min) AS f_out, max(out_bytes_max) AS l_out, max(out_bytes_max) AS m_out,
       min(first_ts) AS f_ts, max(last_ts) AS l_ts, bitCount(groupBitOr(sample_minutes)) AS n
FROM flowseer.interface_hourly_by_time
WHERE tenant_id = {t:String}
  AND hour >= {now:DateTime} - INTERVAL 24 HOUR AND hour < {now:DateTime}
  AND device_id IN {devices:Array(UUID)}
GROUP BY device_id, interface_name, hour, discontinuity_at, software_version;

-- Q:M06 samples top-N-utilisation-in-scope-24h
-- user: svc_reader
-- model: as M05 plus two GROUP BY levels (04 section 5.4)
-- model_sql: SELECT 24 * (SELECT count() FROM bench.interfaces FINAL WHERE tenant_id = {t:String}) + greatest(1, uniqExact(_part)) * 2 * 8192 FROM flowseer.interface_hourly_by_time WHERE tenant_id = {t:String} AND hour >= {now:DateTime} - INTERVAL 24 HOUR AND hour < {now:DateTime}
WITH per_hour AS
(
    SELECT device_id, interface_name, hour, discontinuity_at, software_version,
           min(in_bytes_min) AS f_in, max(in_bytes_max) AS l_in, max(in_bytes_max) AS m_in,
           min(out_bytes_min) AS f_out, max(out_bytes_max) AS l_out, max(out_bytes_max) AS m_out,
           min(first_ts) AS f_ts, max(last_ts) AS l_ts, max(speed_bps_max) AS speed
    FROM flowseer.interface_hourly_by_time
    WHERE tenant_id = {t:String}
      AND hour >= {now:DateTime} - INTERVAL 24 HOUR AND hour < {now:DateTime}
      AND device_id IN {devices:Array(UUID)}
    GROUP BY device_id, interface_name, hour, discontinuity_at, software_version
),
per_iface AS
(
    SELECT device_id, interface_name,
           sum((m_in - f_in) + if(m_in > l_in, l_in, 0)) AS in_inc,
           sum((m_out - f_out) + if(m_out > l_out, l_out, 0)) AS out_inc,
           max(l_ts) - min(f_ts) AS span, max(speed) AS speed
    FROM per_hour
    GROUP BY device_id, interface_name
)
SELECT device_id, interface_name,
       greatest(in_inc, out_inc) * 8 / greatest(span, 1) AS peak_dir_bps,
       peak_dir_bps / speed AS utilisation
FROM per_iface
WHERE speed > 0 AND span >= 3600
ORDER BY utilisation DESC
LIMIT 20;

-- Q:M07 presence set-at-T
-- user: svc_reader
-- model: M x k + k x G (M members of the device, k parts holding its day; 06 section 6 Q1)
-- model_sql: SELECT ((SELECT any(fdb_members) FROM bench.devices FINAL WHERE device_id = {d:UUID}) + 8192) * greatest(1, uniqExact(_part)) FROM flowseer.fdb_presence WHERE tenant_id = {t:String} AND device_id = {d:UUID} AND day = toDate({T:DateTime})
WITH (
    SELECT max(ts) FROM flowseer.set_walks
    WHERE tenant_id = {t:String} AND device_id = {d:UUID} AND kind = 'fdb' AND complete AND ts <= {T:DateTime}
) AS p
SELECT network_instance, vlan_id, mac, interface_name,
       min(first_seen) AS fs, max(last_seen) AS ls
FROM flowseer.fdb_presence
WHERE tenant_id = {t:String} AND device_id = {d:UUID} AND day = toDate(p)
GROUP BY network_instance, vlan_id, mac, interface_name
HAVING fs <= p AND ls >= p
ORDER BY vlan_id, mac;

-- Q:M08 presence where-was-mac-in-tenant-7d
-- user: svc_reader
-- model: days x devices that saw the MAC, rounded to P x G (06 section 6 Q3)
-- model_sql: SELECT count() + greatest(1, uniqExact(_part)) * 8192 FROM flowseer.fdb_presence_by_mac WHERE tenant_id = {t:String} AND mac = {mac:UInt64} AND day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
SELECT device_id, day, interface_name, vlan_id, min(first_seen) AS fs, max(last_seen) AS ls
FROM flowseer.fdb_presence_by_mac
WHERE tenant_id = {t:String} AND mac = {mac:UInt64}
  AND day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
GROUP BY device_id, day, interface_name, vlan_id
ORDER BY day DESC, ls DESC;

-- Q:M09 events syslog-errors-in-site-newest-first-two-step
-- user: svc_reader
-- model: step 1 P x 2 x G on (tenant, site) read in reverse with early stop; step 2 <= 100 point lookups x G (07 E17)
-- model_sql: SELECT greatest(1, uniqExact(_part)) * 2 * 8192 + 100 * 8192 FROM flowseer.syslog_by_scope WHERE tenant_id = {t:String} AND site_id = {site:String} AND ts >= {now:DateTime} - INTERVAL 24 HOUR AND ts < {now:DateTime}
WITH hits AS
(
    SELECT device_id, ts, record_id
    FROM flowseer.syslog_by_scope
    WHERE tenant_id = {t:String} AND site_id = {site:String}
      AND ts >= {now:DateTime} - INTERVAL 24 HOUR AND ts < {now:DateTime}
      AND severity <= 'err'
    ORDER BY ts DESC
    LIMIT 100
)
SELECT s.ts, s.device_id, s.severity, s.vendor_tag, s.message
FROM flowseer.syslog AS s
WHERE s.tenant_id = {t:String}
  AND s.ts >= {now:DateTime} - INTERVAL 24 HOUR AND s.ts < {now:DateTime}
  AND (s.device_id, s.ts, s.record_id) IN (SELECT device_id, ts, record_id FROM hits)
ORDER BY s.ts DESC;

-- Q:M09N events syslog-errors-in-site-naive-device-list
-- user: svc_reader
-- model: D x G x parts per device-day (one granule per device even when it logged little; 07 E16)
-- model_sql: SELECT length({devices:Array(UUID)}) * 8192 * greatest(1, uniqExact(_part)) FROM flowseer.syslog WHERE tenant_id = {t:String} AND device_id = {d:UUID} AND ts >= {now:DateTime} - INTERVAL 24 HOUR AND ts < {now:DateTime}
SELECT ts, device_id, severity, vendor_tag, message
FROM flowseer.syslog
WHERE tenant_id = {t:String} AND device_id IN {devices:Array(UUID)}
  AND ts >= {now:DateTime} - INTERVAL 24 HOUR AND ts < {now:DateTime}
  AND severity <= 'err'
ORDER BY ts DESC
LIMIT 100;

-- Q:M10 events syslog-device-newest-first
-- user: svc_reader
-- model: P x 2 x G (07 E14: <= 3 granules per part)
-- model_sql: SELECT greatest(1, uniqExact(_part)) * 2 * 8192 FROM flowseer.syslog WHERE tenant_id = {t:String} AND device_id = {d:UUID} AND ts >= {now:DateTime} - INTERVAL 7 DAY AND ts < {now:DateTime}
SELECT ts, severity, vendor_tag, app_name, message
FROM flowseer.syslog
WHERE tenant_id = {t:String} AND device_id = {d:UUID}
  AND ts >= {now:DateTime} - INTERVAL 7 DAY AND ts < {now:DateTime}
ORDER BY ts DESC, record_id DESC
LIMIT 200;

-- Q:M11 events full-text-in-tenant-7d
-- user: svc_reader
-- model: granules holding a match x G, upper bound (07 E18; the text index reads per part)
-- model_sql: SELECT greatest(1, uniqExact(_part, intDiv(_part_offset, 8192))) * 8192 FROM flowseer.syslog WHERE tenant_id = {t:String} AND ts >= {now:DateTime} - INTERVAL 7 DAY AND ts < {now:DateTime} AND hasAllTokens(message, ['dhcp', 'snooping'])
SELECT ts, device_id, severity, message
FROM flowseer.syslog
WHERE tenant_id = {t:String}
  AND ts >= {now:DateTime} - INTERVAL 7 DAY AND ts < {now:DateTime}
  AND hasAllTokens(message, ['dhcp', 'snooping'])
ORDER BY ts DESC
LIMIT 200;

-- Q:M12 probes p95-per-target-per-day-7d-rollup
-- user: svc_reader
-- model: targets x 168 hours + P x G
-- model_sql: SELECT 2 * 168 + greatest(1, uniqExact(_part)) * 8192 FROM flowseer.probe_hourly WHERE tenant_id = {t:String} AND device_id = {probe_dev:UUID} AND hour >= {now:DateTime} - INTERVAL 7 DAY AND hour < {now:DateTime}
SELECT toDate(hour) AS day, target_ip,
       quantilesTimingMerge(0.5, 0.95, 0.99)(rtt_ms_q) AS rtt_ms,
       1 - sum(received) / greatest(sum(sent), 1) AS loss
FROM flowseer.probe_hourly
WHERE tenant_id = {t:String} AND device_id = {probe_dev:UUID}
  AND hour >= {now:DateTime} - INTERVAL 7 DAY AND hour < {now:DateTime}
GROUP BY day, target_ip
ORDER BY day, target_ip;

-- Q:M13 probes raw-rtt-chart-one-target-24h
-- user: svc_reader
-- model: 1440 rows + P x G (10 4.5)
-- model_sql: SELECT 1440 + greatest(1, uniqExact(_part)) * 8192 FROM flowseer.probe_intervals WHERE tenant_id = {t:String} AND device_id = {probe_dev:UUID} AND interface_name = '' AND target_ip = {target:IPv6} AND interval_start >= {now:DateTime} - INTERVAL 24 HOUR AND interval_start < {now:DateTime}
SELECT toStartOfFiveMinutes(interval_start) AS t5,
       quantileTiming(0.95)(toUInt32(intDiv(r, 1000))) AS p95_ms,
       count() AS probes_received
FROM
(
    SELECT interval_start, rtt_us
    FROM flowseer.probe_intervals
    WHERE tenant_id = {t:String} AND device_id = {probe_dev:UUID} AND interface_name = ''
      AND target_ip = {target:IPv6}
      AND interval_start >= {now:DateTime} - INTERVAL 24 HOUR AND interval_start < {now:DateTime}
)
ARRAY JOIN rtt_us AS r
GROUP BY t5
ORDER BY t5;

-- Q:M14 samples scope-24h-on-raw-anti-query
-- user: default
-- model: E_scope x 288 + D x P x G (04 section 5.3 "never on raw"; linear in the scope, flat in total data)
-- model_sql: SELECT (SELECT count() FROM bench.interfaces FINAL WHERE device_id IN {devices:Array(UUID)}) * 288 + length({devices:Array(UUID)}) * 8192 * greatest(1, uniqExact(_part)) FROM flowseer.interface_samples WHERE tenant_id = {t:String} AND device_id = {d:UUID} AND ts >= {now:DateTime} - INTERVAL 24 HOUR AND ts < {now:DateTime}
SELECT device_id, interface_name, max(in_bytes) - min(in_bytes) AS inc
FROM flowseer.interface_samples
WHERE tenant_id = {t:String} AND device_id IN {devices:Array(UUID)}
  AND ts >= {now:DateTime} - INTERVAL 24 HOUR AND ts < {now:DateTime}
GROUP BY device_id, interface_name
ORDER BY inc DESC
LIMIT 20;

-- Q:M15 changes scope-changes-24h
-- user: svc_reader
-- model: P x G per device range (change rows of a device fit one granule)
-- model_sql: SELECT length({devices:Array(UUID)}) * 8192 * greatest(1, uniqExact(_part)) FROM flowseer.interface_changes WHERE tenant_id = {t:String} AND device_id = {d:UUID}
SELECT device_id, interface_name, ts, new_value
FROM flowseer.interface_changes
WHERE tenant_id = {t:String} AND device_id IN {devices:Array(UUID)}
  AND ts >= {now:DateTime} - INTERVAL 24 HOUR AND ts < {now:DateTime}
ORDER BY ts DESC
LIMIT 100;

-- ===========================================================================
-- OLAP shapes (analyst, workload analytics), T0, window [now - 7 d, now)
-- Dossier 11 section 1; windows shortened from 30/90 days to the 7-day window the
-- dataset holds at every step.
-- ===========================================================================

-- Q:O01 samples q1-error-rate-by-model-firmware
-- user: analyst
-- model: E_tenant x 7 days (+ epoch and firmware splits) + P x G (11 Q1)
-- model_sql: SELECT 7 * (SELECT count() FROM bench.interfaces FINAL WHERE tenant_id = {t:String}) + greatest(1, uniqExact(_part)) * 2 * 8192 FROM flowseer.interface_daily WHERE tenant_id = {t:String} AND day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
WITH per_iface AS
(
    SELECT device_id, interface_name, day, discontinuity_at, software_version,
           min(in_errors_min) AS e_f, max(in_errors_max) AS e_l, max(in_errors_max) AS e_m,
           min(in_unicast_packets_min) AS p_f, max(in_unicast_packets_max) AS p_l,
           max(in_unicast_packets_max) AS p_m
    FROM flowseer.interface_daily
    WHERE tenant_id = {t:String} AND day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
    GROUP BY device_id, interface_name, day, discontinuity_at, software_version
)
SELECT dictGet('flowseer.device_dim', 'vendor', ({t:String}, device_id)) AS vendor,
       dictGet('flowseer.device_dim', 'model', ({t:String}, device_id)) AS model,
       software_version,
       sum((e_m - e_f) + if(e_m > e_l, e_l, 0)) AS errors,
       sum((p_m - p_f) + if(p_m > p_l, p_l, 0)) AS packets,
       errors / greatest(packets, 1) AS error_rate,
       uniqExact(device_id) AS devices
FROM per_iface
GROUP BY vendor, model, software_version
HAVING packets > 0
ORDER BY error_rate DESC;

-- Q:O03 samples+changes q3-errors-and-flaps-same-hour
-- user: analyst
-- model: E_tenant x 168 + tenant change rows in 7 d (11 Q3)
-- model_sql: SELECT 168 * (SELECT count() FROM bench.interfaces FINAL WHERE tenant_id = {t:String}) + greatest(1, uniqExact(_part)) * 2 * 8192 FROM flowseer.interface_hourly WHERE tenant_id = {t:String} AND hour >= {now:DateTime} - INTERVAL 7 DAY AND hour < {now:DateTime}
WITH errs AS
(
    SELECT device_id, interface_name, hour,
           max(in_errors_max) - min(in_errors_min) AS err_inc
    FROM flowseer.interface_hourly
    WHERE tenant_id = {t:String} AND hour >= {now:DateTime} - INTERVAL 7 DAY AND hour < {now:DateTime}
    GROUP BY device_id, interface_name, hour, discontinuity_at, software_version
    HAVING err_inc > 0
),
flaps AS
(
    SELECT device_id, interface_name, toStartOfHour(ts) AS hour, uniqExact(record_id) AS flaps
    FROM flowseer.interface_changes
    WHERE tenant_id = {t:String} AND ts >= {now:DateTime} - INTERVAL 7 DAY AND ts < {now:DateTime}
      AND attribute = 'oper_status'
    GROUP BY device_id, interface_name, hour
)
SELECT e.device_id, e.interface_name, e.hour, e.err_inc, f.flaps
FROM errs AS e
INNER JOIN flaps AS f ON f.device_id = e.device_id AND f.interface_name = e.interface_name AND f.hour = e.hour
ORDER BY e.err_inc DESC
LIMIT 500;

-- Q:O06 presence q6-fdb-growth-per-vlan-per-site
-- user: analyst
-- model: D x VLANs (10) x 7 + P x G (11 Q6 on fdb_vlan_daily)
-- model_sql: SELECT 70 * (SELECT count() FROM bench.devices FINAL WHERE tenant_id = {t:String}) + greatest(1, uniqExact(_part)) * 8192 FROM flowseer.fdb_vlan_daily WHERE tenant_id = {t:String} AND day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
SELECT site_id, vlan_id, toMonday(day) AS week, uniqExactMerge(members) AS macs
FROM flowseer.fdb_vlan_daily
WHERE tenant_id = {t:String} AND day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
GROUP BY site_id, vlan_id, week
ORDER BY site_id, vlan_id, week;

-- Q:O06R presence q6-on-raw-presence-for-contrast
-- user: analyst
-- model: sum of members x 7 days x parts per day (06 section 3: why the rollup exists)
-- model_sql: SELECT 7 * (SELECT sum(fdb_members) FROM bench.devices FINAL WHERE tenant_id = {t:String}) + greatest(1, uniqExact(_part)) * 8192 FROM flowseer.fdb_presence WHERE tenant_id = {t:String} AND day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
SELECT site_id, vlan_id, toMonday(day) AS week, uniqExact(mac) AS macs
FROM flowseer.fdb_presence
WHERE tenant_id = {t:String} AND day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
GROUP BY site_id, vlan_id, week
ORDER BY site_id, vlan_id, week;

-- Q:O08 events q8-syslog-volume-by-severity-vendor-week
-- user: analyst
-- model: D x (severity, tag) pairs (about 25) x 7 + P x G (11 Q8)
-- model_sql: SELECT 175 * (SELECT count() FROM bench.devices FINAL WHERE tenant_id = {t:String}) + greatest(1, uniqExact(_part)) * 8192 FROM flowseer.syslog_daily WHERE tenant_id = {t:String} AND day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
SELECT dictGet('flowseer.device_dim', 'vendor', ({t:String}, device_id)) AS vendor, severity,
       toMonday(day) AS week, sum(messages) AS messages
FROM flowseer.syslog_daily
WHERE tenant_id = {t:String} AND day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
GROUP BY vendor, severity, week
ORDER BY week, vendor, severity;

-- Q:O09 events q9-top-message-classes-per-model
-- user: analyst
-- model: as O08 (11 Q9)
-- model_sql: SELECT 175 * (SELECT count() FROM bench.devices FINAL WHERE tenant_id = {t:String}) + greatest(1, uniqExact(_part)) * 8192 FROM flowseer.syslog_daily WHERE tenant_id = {t:String} AND day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
SELECT model, vendor_tag, sum(messages) AS n
FROM
(
    SELECT dictGet('flowseer.device_dim', 'model', ({t:String}, device_id)) AS model, vendor_tag, messages
    FROM flowseer.syslog_daily
    WHERE tenant_id = {t:String} AND day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
      AND severity <= 'warning'
)
GROUP BY model, vendor_tag
ORDER BY model, n DESC
LIMIT 10 BY model;

-- Q:O11 samples q11-style-fraction-of-busy-hours-per-model
-- user: analyst
-- model: E_tenant x 168 + P x G (11 Q11 shape on interface_hourly)
-- model_sql: SELECT 168 * (SELECT count() FROM bench.interfaces FINAL WHERE tenant_id = {t:String}) + greatest(1, uniqExact(_part)) * 2 * 8192 FROM flowseer.interface_hourly WHERE tenant_id = {t:String} AND hour >= {now:DateTime} - INTERVAL 7 DAY AND hour < {now:DateTime}
SELECT dictGet('flowseer.device_dim', 'model', ({t:String}, device_id)) AS model, toDate(hour) AS day,
       countIf(util > 0.5) / count() AS busy_fraction, uniqExact(device_id) AS devices
FROM
(
    SELECT device_id, interface_name, hour,
           (max(in_bytes_max) - min(in_bytes_min)) * 8
             / greatest(1, max(last_ts) - min(first_ts)) / greatest(1, max(speed_bps_max)) AS util
    FROM flowseer.interface_hourly
    WHERE tenant_id = {t:String} AND hour >= {now:DateTime} - INTERVAL 7 DAY AND hour < {now:DateTime}
    GROUP BY device_id, interface_name, hour
    HAVING max(speed_bps_max) > 0
)
GROUP BY model, day
ORDER BY day, busy_fraction DESC;

-- Q:O22 probes q22-probe-p95-and-loss-per-site-per-day
-- user: analyst
-- model: targets x 168 + P x G (11 Q22)
-- model_sql: SELECT 168 * (SELECT count() FROM bench.probe_targets FINAL WHERE tenant_id = {t:String}) + greatest(1, uniqExact(_part)) * 8192 FROM flowseer.probe_hourly WHERE tenant_id = {t:String} AND hour >= {now:DateTime} - INTERVAL 7 DAY AND hour < {now:DateTime}
SELECT site_id, toDate(hour) AS day,
       quantilesTimingMerge(0.5, 0.95, 0.99)(rtt_ms_q) AS rtt_ms,
       1 - sum(received) / greatest(sum(sent), 1) AS loss
FROM flowseer.probe_hourly
WHERE tenant_id = {t:String} AND hour >= {now:DateTime} - INTERVAL 7 DAY AND hour < {now:DateTime}
GROUP BY site_id, day
ORDER BY site_id, day;

-- Q:O23 samples cohort-firmware-a-vs-b-device-list
-- user: analyst
-- model: E_model x 7 key ranges (device list resolved from the mirror, 11 Q23 remedy)
-- model_sql: SELECT 7 * (SELECT count() FROM bench.interfaces FINAL WHERE tenant_id = {t:String} AND model = {model:String}) + (SELECT count() FROM bench.devices FINAL WHERE tenant_id = {t:String} AND model = {model:String}) * 8192 * greatest(1, uniqExact(_part)) FROM flowseer.interface_daily WHERE tenant_id = {t:String} AND device_id = {d:UUID} AND day >= toDate({now:DateTime}) - 7
SELECT software_version,
       sum((e_m - e_f) + if(e_m > e_l, e_l, 0)) AS errors,
       sum((p_m - p_f) + if(p_m > p_l, p_l, 0)) AS packets,
       errors / greatest(packets, 1) AS error_rate,
       uniqExact(device_id) AS devices
FROM
(
    SELECT device_id, interface_name, day, discontinuity_at, software_version,
           min(in_errors_min) AS e_f, max(in_errors_max) AS e_l, max(in_errors_max) AS e_m,
           min(in_unicast_packets_min) AS p_f, max(in_unicast_packets_max) AS p_l,
           max(in_unicast_packets_max) AS p_m
    FROM flowseer.interface_daily
    WHERE tenant_id = {t:String} AND day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
      AND device_id IN (SELECT device_id FROM flowseer.device_dim_src FINAL
                        WHERE tenant_id = {t:String} AND model = {model:String})
    GROUP BY device_id, interface_name, day, discontinuity_at, software_version
)
WHERE software_version IN ({sw_a:String}, {sw_b:String})
GROUP BY software_version
ORDER BY software_version;

-- Q:O23D samples cohort-firmware-a-vs-b-dictget-in-where
-- user: analyst
-- model: E_tenant x 7 (dictGet in WHERE cannot use the index; 11 Q23)
-- model_sql: SELECT 7 * (SELECT count() FROM bench.interfaces FINAL WHERE tenant_id = {t:String}) + greatest(1, uniqExact(_part)) * 2 * 8192 FROM flowseer.interface_daily WHERE tenant_id = {t:String} AND day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
SELECT software_version,
       sum((e_m - e_f) + if(e_m > e_l, e_l, 0)) AS errors,
       sum((p_m - p_f) + if(p_m > p_l, p_l, 0)) AS packets,
       errors / greatest(packets, 1) AS error_rate,
       uniqExact(device_id) AS devices
FROM
(
    SELECT device_id, interface_name, day, discontinuity_at, software_version,
           min(in_errors_min) AS e_f, max(in_errors_max) AS e_l, max(in_errors_max) AS e_m,
           min(in_unicast_packets_min) AS p_f, max(in_unicast_packets_max) AS p_l,
           max(in_unicast_packets_max) AS p_m
    FROM flowseer.interface_daily
    WHERE tenant_id = {t:String} AND day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
      AND dictGet('flowseer.device_dim', 'model', ({t:String}, device_id)) = {model:String}
    GROUP BY device_id, interface_name, day, discontinuity_at, software_version
)
WHERE software_version IN ({sw_a:String}, {sw_b:String})
GROUP BY software_version
ORDER BY software_version;

-- Q:O30 events errors-by-region-dictGetHierarchy
-- user: analyst
-- model: as O08 (hierarchy lookups are per row in memory)
-- model_sql: SELECT 175 * (SELECT count() FROM bench.devices FINAL WHERE tenant_id = {t:String}) + greatest(1, uniqExact(_part)) * 8192 FROM flowseer.syslog_daily WHERE tenant_id = {t:String} AND day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
SELECT dictGet('flowseer.location_dict', 'name',
               arrayElement(dictGetHierarchy('flowseer.location_dict', cityHash64(tenant_id, site_id)), -1)) AS region,
       severity, sum(messages) AS messages, uniqExact(site_id) AS sites
FROM flowseer.syslog_daily
WHERE tenant_id = {t:String} AND day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
  AND severity <= 'err'
GROUP BY region, severity
ORDER BY region, severity;

-- Q:O31 samples interface-errors-in-region-dictIsIn
-- user: analyst
-- model: E_tenant x 7 (dictIsIn is a per-row filter after the tenant range)
-- model_sql: SELECT 7 * (SELECT count() FROM bench.interfaces FINAL WHERE tenant_id = {t:String}) + greatest(1, uniqExact(_part)) * 2 * 8192 FROM flowseer.interface_daily WHERE tenant_id = {t:String} AND day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
SELECT site_id, sum(e) AS errors, uniqExact(device_id) AS devices
FROM
(
    SELECT device_id, interface_name, day, discontinuity_at, software_version, any(site_id) AS site_id,
           max(in_errors_max) - min(in_errors_min) AS e
    FROM flowseer.interface_daily
    WHERE tenant_id = {t:String} AND day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
      AND dictIsIn('flowseer.location_dict', cityHash64(tenant_id, site_id), {region:UInt64})
    GROUP BY device_id, interface_name, day, discontinuity_at, software_version
)
GROUP BY site_id
ORDER BY errors DESC;

-- ===========================================================================
-- Partner set P (one-level provider links). Additive aggregations only: run.sh runs each
-- once with all of P and once per tenant of P, and compares read_rows(P) with the sum.
-- LIMIT-with-early-stop shapes are not additive and are not in this group.
-- ===========================================================================

-- Q:PM05 samples tenant-wide-24h-top-interfaces-per-tenant
-- user: analyst
-- kind: partner
-- model: sum over P of 24 x E_tenant
-- model_sql: SELECT 24 * count() FROM bench.interfaces FINAL WHERE tenant_id IN {tenants:Array(String)}
SELECT tenant_id, device_id, interface_name,
       max(in_bytes_max) - min(in_bytes_min) AS inc
FROM flowseer.interface_hourly_by_time
WHERE tenant_id IN {tenants:Array(String)}
  AND hour >= {now:DateTime} - INTERVAL 24 HOUR AND hour < {now:DateTime}
GROUP BY tenant_id, device_id, interface_name
ORDER BY tenant_id, inc DESC
LIMIT 20 BY tenant_id;

-- Q:PM08 presence where-was-mac-across-partner-tenants
-- user: analyst
-- kind: partner
-- model: sum over P of (days x devices that saw the MAC); tenants without the MAC cost one granule probe at most
-- model_sql: SELECT count() FROM flowseer.fdb_presence_by_mac WHERE tenant_id IN {tenants:Array(String)} AND mac = {mac:UInt64} AND day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
SELECT tenant_id, device_id, day, interface_name, min(first_seen) AS fs, max(last_seen) AS ls
FROM flowseer.fdb_presence_by_mac
WHERE tenant_id IN {tenants:Array(String)} AND mac = {mac:UInt64}
  AND day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
GROUP BY tenant_id, device_id, day, interface_name
ORDER BY tenant_id, day DESC;

-- Q:PM11 events full-text-count-per-tenant-7d
-- user: analyst
-- kind: partner
-- model: granules holding a match, summed over P
-- model_sql: SELECT greatest(1, uniqExact(_part, intDiv(_part_offset, 8192))) * 8192 FROM flowseer.syslog WHERE tenant_id IN {tenants:Array(String)} AND ts >= {now:DateTime} - INTERVAL 7 DAY AND ts < {now:DateTime} AND hasAllTokens(message, ['dhcp', 'snooping'])
SELECT tenant_id, count() AS hits
FROM flowseer.syslog
WHERE tenant_id IN {tenants:Array(String)}
  AND ts >= {now:DateTime} - INTERVAL 7 DAY AND ts < {now:DateTime}
  AND hasAllTokens(message, ['dhcp', 'snooping'])
GROUP BY tenant_id
ORDER BY tenant_id;

-- Q:PO01 samples q1-per-tenant-across-partner-set
-- user: analyst
-- kind: partner
-- model: sum over P of E_tenant x 7
-- model_sql: SELECT 7 * count() FROM bench.interfaces FINAL WHERE tenant_id IN {tenants:Array(String)}
WITH per_iface AS
(
    SELECT tenant_id, device_id, interface_name, day, discontinuity_at, software_version,
           min(in_errors_min) AS e_f, max(in_errors_max) AS e_l, max(in_errors_max) AS e_m,
           min(in_unicast_packets_min) AS p_f, max(in_unicast_packets_max) AS p_l,
           max(in_unicast_packets_max) AS p_m
    FROM flowseer.interface_daily
    WHERE tenant_id IN {tenants:Array(String)}
      AND day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
    GROUP BY tenant_id, device_id, interface_name, day, discontinuity_at, software_version
)
SELECT tenant_id,
       dictGet('flowseer.device_dim', 'model', (toString(tenant_id), device_id)) AS model,
       software_version,
       sum((e_m - e_f) + if(e_m > e_l, e_l, 0)) AS errors,
       sum((p_m - p_f) + if(p_m > p_l, p_l, 0)) AS packets,
       errors / greatest(packets, 1) AS error_rate
FROM per_iface
GROUP BY tenant_id, model, software_version
ORDER BY tenant_id, error_rate DESC;

-- Q:PO08 events q8-per-tenant-across-partner-set
-- user: analyst
-- kind: partner
-- model: sum over P of D x 25 x 7
-- model_sql: SELECT 175 * count() FROM bench.devices FINAL WHERE tenant_id IN {tenants:Array(String)}
SELECT tenant_id, dictGet('flowseer.device_dim', 'vendor', (toString(tenant_id), device_id)) AS vendor,
       severity, toMonday(day) AS week, sum(messages) AS messages
FROM flowseer.syslog_daily
WHERE tenant_id IN {tenants:Array(String)}
  AND day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
GROUP BY tenant_id, vendor, severity, week
ORDER BY tenant_id, week, vendor, severity;

-- Q:PO22 probes q22-per-tenant-across-partner-set
-- user: analyst
-- kind: partner
-- model: sum over P of targets x 168
-- model_sql: SELECT 168 * count() FROM bench.probe_targets FINAL WHERE tenant_id IN {tenants:Array(String)}
SELECT tenant_id, site_id, toDate(hour) AS day,
       quantilesTimingMerge(0.5, 0.95, 0.99)(rtt_ms_q) AS rtt_ms,
       1 - sum(received) / greatest(sum(sent), 1) AS loss
FROM flowseer.probe_hourly
WHERE tenant_id IN {tenants:Array(String)}
  AND hour >= {now:DateTime} - INTERVAL 7 DAY AND hour < {now:DateTime}
GROUP BY tenant_id, site_id, day
ORDER BY tenant_id, site_id, day;

-- ===========================================================================
-- Row-policy cost. Same shape with an explicit tenant filter (analyst) and with the
-- tenant supplied by a row policy only (analyst_t0, analyst_partner, analyst_partner_sq).
-- ===========================================================================

-- Q:R01 samples scope-24h-explicit-tenant
-- user: analyst
-- model: as M05
-- model_sql: SELECT 24 * (SELECT count() FROM bench.interfaces FINAL WHERE tenant_id = {t:String}) + greatest(1, uniqExact(_part)) * 2 * 8192 FROM flowseer.interface_hourly_by_time WHERE tenant_id = {t:String} AND hour >= {now:DateTime} - INTERVAL 24 HOUR AND hour < {now:DateTime}
SELECT device_id, interface_name, hour,
       max(in_bytes_max) AS l_in, bitCount(groupBitOr(sample_minutes)) AS n
FROM flowseer.interface_hourly_by_time
WHERE tenant_id = {t:String}
  AND hour >= {now:DateTime} - INTERVAL 24 HOUR AND hour < {now:DateTime}
  AND device_id IN {devices:Array(UUID)}
GROUP BY device_id, interface_name, hour, discontinuity_at, software_version;

-- Q:R02 samples scope-24h-row-policy-t0
-- user: analyst_t0
-- model: as R01 if the policy condition prunes the primary key
-- model_sql: SELECT 24 * (SELECT count() FROM bench.interfaces FINAL WHERE tenant_id = {t:String}) + greatest(1, uniqExact(_part)) * 2 * 8192 FROM flowseer.interface_hourly_by_time WHERE tenant_id = {t:String} AND hour >= {now:DateTime} - INTERVAL 24 HOUR AND hour < {now:DateTime}
SELECT device_id, interface_name, hour,
       max(in_bytes_max) AS l_in, bitCount(groupBitOr(sample_minutes)) AS n
FROM flowseer.interface_hourly_by_time
WHERE hour >= {now:DateTime} - INTERVAL 24 HOUR AND hour < {now:DateTime}
  AND device_id IN {devices:Array(UUID)}
GROUP BY device_id, interface_name, hour, discontinuity_at, software_version;

-- Q:R03 samples q1-explicit-tenant
-- user: analyst
-- model: as O01
-- model_sql: SELECT 7 * (SELECT count() FROM bench.interfaces FINAL WHERE tenant_id = {t:String}) + greatest(1, uniqExact(_part)) * 2 * 8192 FROM flowseer.interface_daily WHERE tenant_id = {t:String} AND day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
SELECT software_version, sum(e) AS errors, uniqExact(device_id) AS devices
FROM
(
    SELECT device_id, interface_name, day, discontinuity_at, software_version,
           max(in_errors_max) - min(in_errors_min) AS e
    FROM flowseer.interface_daily
    WHERE tenant_id = {t:String} AND day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
    GROUP BY device_id, interface_name, day, discontinuity_at, software_version
)
GROUP BY software_version
ORDER BY software_version;

-- Q:R04 samples q1-row-policy-t0
-- user: analyst_t0
-- model: as R03 if the policy prunes
-- model_sql: SELECT 7 * (SELECT count() FROM bench.interfaces FINAL WHERE tenant_id = {t:String}) + greatest(1, uniqExact(_part)) * 2 * 8192 FROM flowseer.interface_daily WHERE tenant_id = {t:String} AND day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
SELECT software_version, sum(e) AS errors, uniqExact(device_id) AS devices
FROM
(
    SELECT device_id, interface_name, day, discontinuity_at, software_version,
           max(in_errors_max) - min(in_errors_min) AS e
    FROM flowseer.interface_daily
    WHERE day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
    GROUP BY device_id, interface_name, day, discontinuity_at, software_version
)
GROUP BY software_version
ORDER BY software_version;

-- Q:R10 events partner-explicit-in
-- user: analyst
-- model: sum over P of D x 25 x 7
-- model_sql: SELECT 175 * count() FROM bench.devices FINAL WHERE tenant_id IN {tenants:Array(String)}
SELECT tenant_id, severity, sum(messages) AS messages
FROM flowseer.syslog_daily
WHERE tenant_id IN {tenants:Array(String)}
  AND day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
GROUP BY tenant_id, severity
ORDER BY tenant_id, severity;

-- Q:R11 events partner-row-policy-dictHas
-- user: analyst_partner
-- model: as R10 only if the condition prunes; expected to read every tenant's rows in the window (grows with total data)
-- model_sql: SELECT 175 * count() FROM bench.devices FINAL WHERE tenant_id IN {tenants:Array(String)}
SELECT tenant_id, severity, sum(messages) AS messages
FROM flowseer.syslog_daily
WHERE day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
GROUP BY tenant_id, severity
ORDER BY tenant_id, severity;

-- Q:R12 events partner-row-policy-in-subquery
-- user: analyst_partner_sq
-- model: as R10 if the IN set prunes the key
-- model_sql: SELECT 175 * count() FROM bench.devices FINAL WHERE tenant_id IN {tenants:Array(String)}
SELECT tenant_id, severity, sum(messages) AS messages
FROM flowseer.syslog_daily
WHERE day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
GROUP BY tenant_id, severity
ORDER BY tenant_id, severity;

-- Q:R13 events partner-row-policy-dictHas-plus-explicit-in
-- user: analyst_partner
-- model: as R10
-- model_sql: SELECT 175 * count() FROM bench.devices FINAL WHERE tenant_id IN {tenants:Array(String)}
SELECT tenant_id, severity, sum(messages) AS messages
FROM flowseer.syslog_daily
WHERE tenant_id IN {tenants:Array(String)}
  AND day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
GROUP BY tenant_id, severity
ORDER BY tenant_id, severity;

-- Q:R14 events partner-row-policy-in-subquery-plus-explicit-in
-- user: analyst_partner_sq
-- model: as R10
-- model_sql: SELECT 175 * count() FROM bench.devices FINAL WHERE tenant_id IN {tenants:Array(String)}
SELECT tenant_id, severity, sum(messages) AS messages
FROM flowseer.syslog_daily
WHERE tenant_id IN {tenants:Array(String)}
  AND day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
GROUP BY tenant_id, severity
ORDER BY tenant_id, severity;

-- Q:R20 samples partner-q1-explicit-in
-- user: analyst
-- model: sum over P of E_tenant x 7
-- model_sql: SELECT 7 * count() FROM bench.interfaces FINAL WHERE tenant_id IN {tenants:Array(String)}
SELECT tenant_id, sum(max_e - min_e) AS errors
FROM
(
    SELECT tenant_id, device_id, interface_name, day, discontinuity_at,
           max(in_errors_max) AS max_e, min(in_errors_min) AS min_e
    FROM flowseer.interface_daily
    WHERE tenant_id IN {tenants:Array(String)}
      AND day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
    GROUP BY tenant_id, device_id, interface_name, day, discontinuity_at
)
GROUP BY tenant_id
ORDER BY tenant_id;

-- Q:R21 samples partner-q1-row-policy-dictHas
-- user: analyst_partner
-- model: as R20 only if the condition prunes
-- model_sql: SELECT 7 * count() FROM bench.interfaces FINAL WHERE tenant_id IN {tenants:Array(String)}
SELECT tenant_id, sum(max_e - min_e) AS errors
FROM
(
    SELECT tenant_id, device_id, interface_name, day, discontinuity_at,
           max(in_errors_max) AS max_e, min(in_errors_min) AS min_e
    FROM flowseer.interface_daily
    WHERE day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
    GROUP BY tenant_id, device_id, interface_name, day, discontinuity_at
)
GROUP BY tenant_id
ORDER BY tenant_id;

-- Q:R22 samples partner-q1-row-policy-in-subquery
-- user: analyst_partner_sq
-- model: as R20 if the IN set prunes
-- model_sql: SELECT 7 * count() FROM bench.interfaces FINAL WHERE tenant_id IN {tenants:Array(String)}
SELECT tenant_id, sum(max_e - min_e) AS errors
FROM
(
    SELECT tenant_id, device_id, interface_name, day, discontinuity_at,
           max(in_errors_max) AS max_e, min(in_errors_min) AS min_e
    FROM flowseer.interface_daily
    WHERE day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
    GROUP BY tenant_id, device_id, interface_name, day, discontinuity_at
)
GROUP BY tenant_id
ORDER BY tenant_id;

-- ===========================================================================
-- Checks (run once per step, result saved to results/checks_<step>.tsv)
-- ===========================================================================

-- Q:C01 policy analyst-t0-sees-only-t0
-- user: analyst_t0
-- kind: check
-- model: expect ['t0000']
SELECT groupUniqArray(tenant_id) AS tenants FROM flowseer.interface_daily;

-- Q:C02 policy partner-dictHas-sees-only-P
-- user: analyst_partner
-- kind: check
-- model: expect 20, t0000, t0019
SELECT uniqExact(tenant_id) AS n, min(tenant_id) AS lo, max(tenant_id) AS hi FROM flowseer.syslog_daily;

-- Q:C03 policy partner-subquery-sees-only-P
-- user: analyst_partner_sq
-- kind: check
-- model: expect 20, t0000, t0019
SELECT uniqExact(tenant_id) AS n, min(tenant_id) AS lo, max(tenant_id) AS hi FROM flowseer.syslog_daily;

-- Q:C04 policy dictionary-bypasses-row-policy
-- user: analyst_t0
-- kind: check
-- model: a non-empty vendor means dictGet returns another tenant's dimension to a policy-bound user
SELECT dictGet('flowseer.device_dim', 'vendor', ({t1:String}, {t1_dev:UUID})) AS other_tenant_vendor;

-- Q:C05 presence walk-digest-equals-presence-set
-- user: default
-- kind: check
-- model: expect mismatches = 0 (decision 4: central digest check)
SELECT countIf(w.h != p.h) AS mismatches, count() AS devices
FROM
(
    SELECT device_id, argMax(set_hash, ts) AS h
    FROM flowseer.set_walks
    WHERE tenant_id = {t:String} AND kind = 'fdb' AND complete AND toDate(ts) = toDate({T:DateTime})
    GROUP BY device_id
) AS w
INNER JOIN
(
    SELECT device_id, groupBitXorDistinct(cityHash64(vlan_id, mac, interface_name)) AS h
    FROM flowseer.fdb_presence
    WHERE tenant_id = {t:String} AND day = toDate({T:DateTime})
    GROUP BY device_id
) AS p USING (device_id);

-- Q:C06 dimensions stamped-site-equals-range-dictionary
-- user: default
-- kind: check
-- model: expect 0 (11 section 7.4 consistency)
SELECT countIf(site_id != dictGet('flowseer.device_site_dict', 'site_id', (toString(tenant_id), device_id), ts)) AS mismatches,
       count() AS rows
FROM flowseer.interface_samples
WHERE tenant_id = {t:String} AND ts >= {now:DateTime} - INTERVAL 1 DAY AND ts < {now:DateTime};

-- Q:C07 samples stamped-firmware-flips-once-per-upgraded-device
-- user: default
-- kind: check
-- model: devices with two versions in the window = upgraded cisco devices of T0 (bench.devices upgrade_at > 0)
SELECT (SELECT count() FROM bench.devices FINAL WHERE tenant_id = {t:String} AND upgrade_at > 0) AS expected,
       countIf(v = 2) AS observed
FROM
(
    SELECT device_id, uniqExact(software_version) AS v
    FROM flowseer.interface_daily
    WHERE tenant_id = {t:String} AND day >= toDate({now:DateTime}) - 7 AND day < toDate({now:DateTime})
    GROUP BY device_id
);

-- ===========================================================================
-- Dedup checks (redelivery phase: before, right after, after settle). Results must be
-- "identical" across phases where marked; "differs" marks the documented
-- non-idempotent aggregates and raw row counts before merge.
-- ===========================================================================

-- Q:D01 samples raw-distinct-keys
-- user: default
-- kind: dedup
-- expect: identical
SELECT uniqExact(device_id, interface_name, ts) FROM flowseer.interface_samples WHERE tenant_id = {t:String};

-- Q:D02 samples raw-row-count
-- user: default
-- kind: dedup
-- expect: differs
SELECT count() FROM flowseer.interface_samples WHERE tenant_id = {t:String};

-- Q:D03 samples count-final
-- user: default
-- kind: dedup
-- expect: identical
SELECT count() FROM flowseer.interface_samples FINAL WHERE tenant_id = {t:String};

-- Q:D04 samples count-limit-1-by
-- user: default
-- kind: dedup
-- expect: identical
SELECT count() FROM (SELECT device_id, interface_name, ts FROM flowseer.interface_samples WHERE tenant_id = {t:String} LIMIT 1 BY device_id, interface_name, ts);

-- Q:D05 samples hourly-rollup-fingerprint
-- user: default
-- kind: dedup
-- expect: identical
SELECT count() AS entity_hours, sum(n) AS samples, sum(inc_in) AS in_bytes_increase, sum(inc_err) AS in_errors_increase,
       groupBitXor(cityHash64(device_id, interface_name, hour, discontinuity_at, software_version, n, fi, li, mi, fe, le)) AS fingerprint
FROM
(
    SELECT device_id, interface_name, hour, discontinuity_at, software_version,
           bitCount(groupBitOr(sample_minutes)) AS n,
           min(in_bytes_min) AS fi, max(in_bytes_max) AS li, max(in_bytes_max) AS mi,
           min(in_errors_min) AS fe, max(in_errors_max) AS le,
           (mi - fi) + if(mi > li, li, 0) AS inc_in, le - fe AS inc_err
    FROM flowseer.interface_hourly
    WHERE tenant_id = {t:String}
    GROUP BY device_id, interface_name, hour, discontinuity_at, software_version
);

-- Q:D06 samples hourly-by-time-rollup-fingerprint
-- user: default
-- kind: dedup
-- expect: identical
SELECT count() AS entity_hours, sum(n) AS samples, sum(inc_in) AS in_bytes_increase, sum(inc_err) AS in_errors_increase,
       groupBitXor(cityHash64(device_id, interface_name, hour, discontinuity_at, software_version, n, fi, li, mi, fe, le)) AS fingerprint
FROM
(
    SELECT device_id, interface_name, hour, discontinuity_at, software_version,
           bitCount(groupBitOr(sample_minutes)) AS n,
           min(in_bytes_min) AS fi, max(in_bytes_max) AS li, max(in_bytes_max) AS mi,
           min(in_errors_min) AS fe, max(in_errors_max) AS le,
           (mi - fi) + if(mi > li, li, 0) AS inc_in, le - fe AS inc_err
    FROM flowseer.interface_hourly_by_time
    WHERE tenant_id = {t:String}
    GROUP BY device_id, interface_name, hour, discontinuity_at, software_version
);

-- Q:D07 samples daily-rollup-fingerprint
-- user: default
-- kind: dedup
-- expect: identical
SELECT count() AS entity_days, sum(n) AS samples, sum(inc_in) AS in_bytes_increase, sum(inc_err) AS in_errors_increase,
       groupBitXor(cityHash64(device_id, interface_name, day, discontinuity_at, software_version, n, fi, li, mi, fe, le)) AS fingerprint
FROM
(
    SELECT device_id, interface_name, day, discontinuity_at, software_version,
           bitCount(groupBitOr(sample_hours)) AS n,
           min(in_bytes_min) AS fi, max(in_bytes_max) AS li, max(in_bytes_max) AS mi,
           min(in_errors_min) AS fe, max(in_errors_max) AS le,
           (mi - fi) + if(mi > li, li, 0) AS inc_in, le - fe AS inc_err
    FROM flowseer.interface_daily
    WHERE tenant_id = {t:String}
    GROUP BY device_id, interface_name, day, discontinuity_at, software_version
);

-- Q:D08 samples rollup-sample-count-equals-raw-distinct
-- user: default
-- kind: dedup
-- expect: identical
SELECT (SELECT sum(n) FROM (SELECT bitCount(groupBitOr(sample_minutes)) AS n FROM flowseer.interface_hourly WHERE tenant_id = {t:String}
                             GROUP BY device_id, interface_name, hour, discontinuity_at, software_version)) AS rollup_samples,
       (SELECT uniqExact(device_id, interface_name, ts) FROM flowseer.interface_samples WHERE tenant_id = {t:String}) AS raw_distinct;

-- Q:D09 changes distinct-record-ids
-- user: default
-- kind: dedup
-- expect: identical
SELECT uniqExact(record_id) FROM flowseer.interface_changes WHERE tenant_id = {t:String};

-- Q:D10 changes raw-row-count
-- user: default
-- kind: dedup
-- expect: differs
SELECT count() FROM flowseer.interface_changes WHERE tenant_id = {t:String};

-- Q:D11 events syslog-distinct-record-ids
-- user: default
-- kind: dedup
-- expect: identical
SELECT uniqExact(record_id) FROM flowseer.syslog WHERE tenant_id = {t:String};

-- Q:D12 events syslog-daily-sum-not-idempotent
-- user: default
-- kind: dedup
-- expect: differs
SELECT sum(messages) FROM flowseer.syslog_daily WHERE tenant_id = {t:String};

-- Q:D13 events syslog-by-scope-distinct
-- user: default
-- kind: dedup
-- expect: identical
SELECT uniqExact(record_id) FROM flowseer.syslog_by_scope WHERE tenant_id = {t:String};

-- Q:D14 probes hourly-sent-not-idempotent
-- user: default
-- kind: dedup
-- expect: differs
SELECT sum(sent) FROM flowseer.probe_hourly WHERE tenant_id = {t:String};

-- Q:D15 probes raw-final-sent
-- user: default
-- kind: dedup
-- expect: identical
SELECT sum(sent) FROM flowseer.probe_intervals FINAL WHERE tenant_id = {t:String};

-- Q:D16 probes hourly-min-max-fingerprint
-- user: default
-- kind: dedup
-- expect: identical
SELECT groupBitXor(cityHash64(device_id, target_ip, hour, mn, mx)) FROM
(
    SELECT device_id, target_ip, hour, min(rtt_min_us) AS mn, max(rtt_max_us) AS mx
    FROM flowseer.probe_hourly WHERE tenant_id = {t:String}
    GROUP BY device_id, target_ip, hour
);

-- Q:D17 presence day-set-fingerprint
-- user: default
-- kind: dedup
-- expect: identical
SELECT count() AS members, groupBitXor(cityHash64(device_id, day, vlan_id, mac, interface_name, fs, ls)) AS fingerprint
FROM
(
    SELECT device_id, day, vlan_id, mac, interface_name, min(first_seen) AS fs, max(last_seen) AS ls
    FROM flowseer.fdb_presence WHERE tenant_id = {t:String}
    GROUP BY device_id, day, network_instance, vlan_id, mac, interface_name
);

-- Q:D18 presence walks-sum-not-idempotent
-- user: default
-- kind: dedup
-- expect: differs
SELECT sum(walks) FROM flowseer.fdb_presence WHERE tenant_id = {t:String};

-- Q:D19 presence by-mac-fingerprint
-- user: default
-- kind: dedup
-- expect: identical
SELECT count() AS rows, groupBitXor(cityHash64(mac, day, device_id, vlan_id, interface_name, fs, ls)) AS fingerprint
FROM
(
    SELECT mac, day, device_id, vlan_id, interface_name, min(first_seen) AS fs, max(last_seen) AS ls
    FROM flowseer.fdb_presence_by_mac WHERE tenant_id = {t:String}
    GROUP BY mac, day, device_id, network_instance, vlan_id, interface_name
);

-- Q:D20 presence vlan-daily-members
-- user: default
-- kind: dedup
-- expect: identical
SELECT sum(n) FROM (SELECT uniqExactMerge(members) AS n FROM flowseer.fdb_vlan_daily WHERE tenant_id = {t:String} GROUP BY device_id, vlan_id, day);
