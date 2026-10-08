-- In-database deterministic generator for the history store benchmark.
--
-- Every value is a pure function of (seed 20261008, tenant, device, entity, time) through
-- cityHash64, so a rerun, a redelivery, and the fixed tenant T0 produce byte-identical rows.
-- No rand(), no generateRandom().
--
-- The file is split into sections. Each section starts with a line "-- @<name>" and runs as
-- one clickhouse-client batch call (statements read from stdin) from gen.sh with query parameters
-- (https://clickhouse.com/docs/interfaces/client, "Query parameters"):
--   {u_lo:UInt16} {u_hi:UInt16}  unit range (unit 0 = T0, unit u >= 1 = 40 tenants)
--   {lo:UInt32} {hi:UInt32}      tenant_idx range
--   {start:UInt32}               unix start of the measured 7-day window
--   {h:UInt32}                   unix start of one hour
--   {d:UInt32}                   unix start of one day (UTC)
--   {b:UInt8}                    six-hour block of a day (0..3)
--   {rd:UInt8}                   1 = redelivery pass: emit only the 1 % subset chosen by hash
-- Time parameters are unix seconds so the shell needs integer arithmetic only.
--
-- Tenants: unit 0 is T0 (tenant_idx 0, 't0000', 200 devices, 10 sites, 3 regions).
-- Unit u >= 1 holds tenant_idx (u-1)*40+1 .. u*40 with round(58/k) devices for k = 1..40
-- (Zipf-like: 58, 29, 19, 14, 12, ... 1; 248 devices per unit). The partner set P is
-- tenant_idx 0..19. Steps: s1 = units 0..1, s4 adds 2..4, s16 adds 5..16.
--
-- Device mix: access 48 ports 80 %, distribution 52 ports 8 %, 500-port chassis 0.5 %
-- (T0 has two fixed), router 8 interfaces 6.5 %, firewall 12 interfaces 5 %. Vendors 6
-- (cisco 50 %), models vendor-role-variant, firmware 9.1/9.2/9.3 at 60/30/10 and an
-- upgrade wave: 30 % of cisco devices move to cisco-9.4 over 48 h starting 3 days into the
-- window. Interface error rates depend on (model, firmware) so the cohort queries have a
-- known answer.

-- @setup
CREATE DATABASE IF NOT EXISTS bench;

-- 32 hex chars from two hashes, formatted as a UUID.
CREATE FUNCTION IF NOT EXISTS fsb_hex16 AS (a, b, k) -> lower(leftPad(hex(cityHash64(20261008, a, b, k)), 16, '0'));
CREATE FUNCTION IF NOT EXISTS fsb_uuid AS (a, b) -> toUUID(concat(
    substring(fsb_hex16(a, b, 1), 1, 8), '-', substring(fsb_hex16(a, b, 1), 9, 4), '-',
    substring(fsb_hex16(a, b, 1), 13, 4), '-', substring(fsb_hex16(a, b, 2), 1, 4), '-',
    substring(fsb_hex16(a, b, 2), 5, 12)));
CREATE FUNCTION IF NOT EXISTS fsb_ifname AS (n_if, idx) -> multiIf(
    n_if = 500, concat('Ethernet', toString(intDiv(idx, 50) + 1), '/', toString(idx % 50 + 1)),
    n_if IN (48, 52), concat('GigabitEthernet1/0/', toString(idx + 1)),
    concat('ge-0/0/', toString(idx)));
-- As-of firmware at unix time t.
CREATE FUNCTION IF NOT EXISTS fsb_sw AS (sw_old, sw_new, up, t) -> if(up > 0 AND t >= up, sw_new, sw_old);
-- Start of the counter epoch (last reset) at t: resets every `period` seconds, phase `off`.
CREATE FUNCTION IF NOT EXISTS fsb_epoch AS (t, period, off) -> period * intDiv(t - off, period) + off;
-- Monotone counter inside an epoch: integral of rate * (1 + 0.1 sin(t / 13751)) plus a
-- per-sample offset below 120 * rate. The smooth part grows at least 270 * rate per 300 s
-- poll, so the sum never decreases between polls of one epoch.
CREATE FUNCTION IF NOT EXISTS fsb_ctr AS (rate, e, t, nh) -> toUInt64(
    rate * ((toFloat64(t) - toFloat64(e)) + 1375.1 * (cos(toFloat64(e) / 13751) - cos(toFloat64(t) / 13751)))
    + if(rate > 0, (nh % 1000) * 0.12 * rate, 0));
-- Error counter with a rate change at the firmware upgrade `up` (4e9 when none).
CREATE FUNCTION IF NOT EXISTS fsb_err AS (r_old, r_new, e, up, t) -> toUInt64(
    r_old * greatest(0, least(t, up) - e) + r_new * greatest(0, t - greatest(up, e)));
-- Link flap of one interface on one day number: hash, start, duration (300..3599 s, never
-- crossing midnight).
CREATE FUNCTION IF NOT EXISTS fsb_fh AS (dev, ifn, dn) -> cityHash64(20261008, dev, ifn, toUInt32(dn), 'flap');
CREATE FUNCTION IF NOT EXISTS fsb_fstart AS (fh, daystart) -> daystart + intDiv(fh, 1000) % 82800;
CREATE FUNCTION IF NOT EXISTS fsb_fdur AS (fh) -> 300 + intDiv(fh, 100000000) % 3300;
-- Standard normal from a hash (Box-Muller).
CREATE FUNCTION IF NOT EXISTS fsb_z AS (h) -> sqrt(-2 * log(((h % 1000000) + 1) / 1000001))
    * cos(2 * pi() * ((intDiv(h, 1000000) % 1000000) / 1000000));

CREATE TABLE IF NOT EXISTS bench.tenants
(
    tenant_idx      UInt32,
    tenant_id       String,
    unit            UInt16,
    k               UInt16,
    devices         UInt32,
    n_sites         UInt32,
    n_regions       UInt32,
    retention_class Enum8('short' = 1, 'standard' = 2, 'long' = 3)
)
ENGINE = ReplacingMergeTree ORDER BY tenant_idx;

CREATE TABLE IF NOT EXISTS bench.devices
(
    tenant_idx      UInt32,
    tenant_id       String,
    unit            UInt16,
    dev_idx         UInt32,
    device_id       UUID,
    role            LowCardinality(String),
    n_if            UInt16,
    vendor          LowCardinality(String),
    model           LowCardinality(String),
    sw_old          LowCardinality(String),
    sw_new          LowCardinality(String),
    upgrade_at      UInt32,
    site_idx        UInt32,
    site_id         String,
    region          String,
    fdb_members     UInt32,
    syslog_per_hour Float32,
    storm_at        UInt32,
    retention_class Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    hostname        String
)
ENGINE = ReplacingMergeTree ORDER BY (tenant_idx, dev_idx);

CREATE TABLE IF NOT EXISTS bench.interfaces
(
    tenant_idx      UInt32,
    tenant_id       String,
    unit            UInt16,
    device_id       UUID,
    dev_idx         UInt32,
    role            LowCardinality(String),
    vendor          LowCardinality(String),
    model           LowCardinality(String),
    site_id         String,
    retention_class Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    sw_old          LowCardinality(String),
    sw_new          LowCardinality(String),
    upgrade_at      UInt32,
    n_if            UInt16,
    if_idx          UInt16,
    interface_name  String,
    if_index        UInt32,
    connected       Bool,
    admin_status    UInt8,
    port_speed      UInt64,
    mtu             UInt32,
    description     String,
    rate_Bps        Float64,
    out_ratio       Float64,
    pkt_size        UInt32,
    reset_period    UInt32,
    reset_off       UInt32,
    reports_disc    Bool,
    err_old         Float64,
    err_new         Float64,
    flap_permille   UInt16,
    poll_off        UInt32
)
ENGINE = ReplacingMergeTree ORDER BY (tenant_idx, device_id, if_idx);

CREATE TABLE IF NOT EXISTS bench.probe_targets
(
    tenant_idx      UInt32,
    tenant_id       String,
    unit            UInt16,
    site_id         String,
    device_id       UUID,
    target_idx      UInt8,
    target_ip       IPv6,
    base_us         UInt32,
    dark_day        UInt32,
    retention_class Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    sw_old          LowCardinality(String),
    sw_new          LowCardinality(String),
    upgrade_at      UInt32
)
ENGINE = ReplacingMergeTree ORDER BY (tenant_idx, site_id, target_idx);

-- Cost-model values per query and step, filled by run.sh, joined by the summary.
CREATE TABLE IF NOT EXISTS bench.model
(
    step       String,
    qid        String,
    model_rows Float64
)
ENGINE = ReplacingMergeTree ORDER BY (step, qid);

-- Dedup snapshots, filled by run.sh (phase = before | after | settled).
CREATE TABLE IF NOT EXISTS bench.dedup
(
    phase  String,
    qid    String,
    result String
)
ENGINE = ReplacingMergeTree ORDER BY (phase, qid);

-- @tenants
INSERT INTO bench.tenants
SELECT tenant_idx, concat('t', leftPad(toString(tenant_idx), 4, '0')) AS tenant_id, unit, k, devices,
       greatest(1, intDiv(devices, 20)) AS n_sites,
       greatest(1, intDiv(greatest(1, intDiv(devices, 20)), 3)) AS n_regions,
       rc AS retention_class
FROM
(
    SELECT toUInt32(0) AS tenant_idx, toUInt16(0) AS unit, toUInt16(0) AS k, toUInt32(200) AS devices,
           'standard' AS rc
    WHERE {u_lo:UInt16} = 0
    UNION ALL
    SELECT toUInt32((u - 1) * 40 + kk) AS tenant_idx, toUInt16(u) AS unit, toUInt16(kk) AS k,
           toUInt32(greatest(1, round(58 / kk))) AS devices,
           multiIf(cityHash64(20261008, tenant_idx, 'rc') % 10 < 6, 'standard',
                   cityHash64(20261008, tenant_idx, 'rc') % 10 < 9, 'short', 'long') AS rc
    FROM (SELECT number AS u FROM numbers(1, 16))
    ARRAY JOIN range(1, 41) AS kk
    WHERE u BETWEEN {u_lo:UInt16} AND {u_hi:UInt16}
);

-- @devices
INSERT INTO bench.devices
SELECT tenant_idx, tenant_id, unit, dev_idx, device_id, role, n_if, vendor, model, sw_old, sw_new,
       upgrade_at, site_idx, site_id, region, fdb_members, syslog_per_hour, storm_at,
       retention_class, hostname
FROM
(
    SELECT tenant_idx, tenant_id, unit, n_sites, n_regions, retention_class,
           toUInt32(di) AS dev_idx,
           fsb_uuid(tenant_id, toString(dev_idx)) AS device_id,
           cityHash64(20261008, tenant_idx, dev_idx, 'dev') AS dh,
           multiIf(tenant_idx = 0 AND dev_idx < 2, 'chassis',
                   dh % 1000 < 800, 'access', dh % 1000 < 880, 'dist', dh % 1000 < 885, 'chassis',
                   dh % 1000 < 950, 'router', 'firewall') AS role,
           toUInt16(multiIf(role = 'access', 48, role = 'dist', 52, role = 'chassis', 500,
                            role = 'router', 8, 12)) AS n_if,
           cityHash64(20261008, tenant_idx, dev_idx, 'vendor') % 100 AS vh,
           multiIf(vh < 50, 'cisco', vh < 70, 'huawei', vh < 82, 'aruba', vh < 90, 'juniper',
                   vh < 96, 'hpe', 'ruckus') AS vendor,
           concat(vendor, '-', role, '-',
                  toString(cityHash64(20261008, tenant_idx, dev_idx, 'model') % if(role = 'access', 3, 2) + 1)) AS model,
           cityHash64(20261008, tenant_idx, dev_idx, 'fw') % 100 AS fwh,
           concat(vendor, '-', multiIf(fwh < 60, '9.1', fwh < 90, '9.2', '9.3')) AS sw_old,
           cityHash64(20261008, tenant_idx, dev_idx, 'upgrade') AS uh,
           vendor = 'cisco' AND uh % 100 < 30 AS upgraded,
           if(upgraded, 'cisco-9.4', sw_old) AS sw_new,
           toUInt32(if(upgraded, {start:UInt32} + 3 * 86400 + uh % 172800, 0)) AS upgrade_at,
           toUInt32(floor(n_sites * pow((cityHash64(20261008, tenant_idx, dev_idx, 'site') % 1000000) / 1000000, 2))) AS site_idx,
           concat(tenant_id, '-s', leftPad(toString(site_idx), 2, '0')) AS site_id,
           concat(tenant_id, '-r', toString(site_idx % n_regions)) AS region,
           toUInt32(multiIf(role = 'chassis', 20000, role = 'dist', 2000,
                            role IN ('router', 'firewall'), 50 + dh % 150,
                            least(2000, greatest(50, 300 * exp(0.8 * fsb_z(cityHash64(20261008, tenant_idx, dev_idx, 'fdb'))))))) AS fdb_members,
           toFloat32(least(400, greatest(1, 25 * exp(fsb_z(cityHash64(20261008, tenant_idx, dev_idx, 'syslog')))))) AS syslog_per_hour,
           toUInt32(multiIf(tenant_idx = 0 AND dev_idx = 3, {start:UInt32} + 2 * 86400 + 10 * 3600,
                            tenant_idx > 0 AND dh % 1000 < 2, {start:UInt32} + (dh % 7) * 86400 + (intDiv(dh, 7) % 24) * 3600,
                            0)) AS storm_at,
           concat('dev-', tenant_id, '-', toString(dev_idx)) AS hostname
    FROM bench.tenants FINAL
    ARRAY JOIN range(devices) AS di
    WHERE unit BETWEEN {u_lo:UInt16} AND {u_hi:UInt16}
);

-- @interfaces
INSERT INTO bench.interfaces
SELECT tenant_idx, tenant_id, unit, device_id, dev_idx, role, vendor, model, site_id, retention_class,
       sw_old, sw_new, upgrade_at, n_if, if_idx, interface_name, if_index, connected, admin_status,
       port_speed, mtu, description, rate_Bps, out_ratio, pkt_size, reset_period, reset_off,
       reports_disc, err_old, err_new, flap_permille, poll_off
FROM
(
    SELECT *,
           toUInt16(ii) AS if_idx,
           fsb_ifname(n_if, if_idx) AS interface_name,
           toUInt32(1000 + if_idx) AS if_index,
           role IN ('router', 'firewall', 'dist') OR cityHash64(20261008, device_id, if_idx, 1) % 100 < 60 AS connected,
           connected AND cityHash64(20261008, device_id, if_idx, 2) % 100 < 20 AS active,
           if(NOT connected, 0,
              if(active, 1e6 * (1 + cityHash64(20261008, device_id, if_idx, 3) % 900),
                         1e3 * (1 + cityHash64(20261008, device_id, if_idx, 3) % 50))) / 8 AS rate_Bps,
           toUInt64(multiIf(n_if = 500, 100e9, n_if IN (8, 12), 10e9,
                            n_if = 48 AND if_idx >= 44, 10e9, n_if = 52 AND if_idx >= 48, 10e9, 1e9)) AS port_speed,
           0.3 + (cityHash64(20261008, device_id, if_idx, 4) % 120) / 100 AS out_ratio,
           toUInt32(200 + cityHash64(20261008, device_id, if_idx, 5) % 1200) AS pkt_size,
           toUInt32(86400 * (10 + cityHash64(20261008, device_id, if_idx, 6) % 110)) AS reset_period,
           toUInt32(cityHash64(20261008, device_id, if_idx, 7) % reset_period) AS reset_off,
           cityHash64(20261008, device_id, if_idx, 8) % 2 = 0 AS reports_disc,
           connected AND cityHash64(20261008, device_id, if_idx, 9) % 100 < 3 AS faulty,
           if(faulty, (1 + cityHash64(20261008, model, sw_old) % 20) * 1e-4, 0) AS err_old,
           if(faulty, if(sw_new != sw_old, (1 + cityHash64(20261008, model, sw_new) % 20) * 0.25e-4, err_old), 0) AS err_new,
           toUInt16(if(faulty, 50, 5)) AS flap_permille,
           toUInt8(if(connected, 1, if(cityHash64(20261008, device_id, if_idx, 10) % 10 < 3, 2, 1))) AS admin_status,
           toUInt32(if(n_if = 500, 9216, 1500)) AS mtu,
           if(connected, concat('link-', toString(cityHash64(20261008, device_id, if_idx, 11) % 10000)), '') AS description,
           toUInt32(cityHash64(20261008, device_id) % 60) AS poll_off
    FROM bench.devices FINAL
    ARRAY JOIN range(n_if) AS ii
    WHERE unit BETWEEN {u_lo:UInt16} AND {u_hi:UInt16}
);

-- @probe_targets
-- Two targets per site (gateway, internet anchor), probed from the site's first device.
INSERT INTO bench.probe_targets
SELECT tenant_idx, tenant_id, unit, site_id, prober AS device_id, toUInt8(ti) AS target_idx,
       toIPv6(if(ti = 0,
                 concat('10.', toString(tenant_idx % 250), '.', toString(site_idx), '.1'),
                 concat('198.51.100.', toString(1 + tenant_idx % 200)))) AS target_ip,
       toUInt32(if(ti = 0, 1500 + th % 1500, 6000 * (1 + (th % 100) / 100))) AS base_us,
       toUInt32(if(th % 100 < 2, {start:UInt32} + (intDiv(th, 100) % 7) * 86400, 0)) AS dark_day,
       retention_class, sw_old, sw_new, upgrade_at
FROM
(
    SELECT *, ti, cityHash64(20261008, tenant_idx, site_id, ti) AS th
    FROM
    (
        SELECT tenant_idx, tenant_id, unit, site_id, site_idx, retention_class,
               argMin(device_id, dev_idx) AS prober,
               argMin(sw_old, dev_idx) AS sw_old, argMin(sw_new, dev_idx) AS sw_new,
               argMin(upgrade_at, dev_idx) AS upgrade_at
        FROM bench.devices FINAL
        WHERE unit BETWEEN {u_lo:UInt16} AND {u_hi:UInt16}
        GROUP BY tenant_idx, tenant_id, unit, site_id, site_idx, retention_class
    )
    ARRAY JOIN [0, 1] AS ti
);

-- @dims
INSERT INTO flowseer.device_dim_src
SELECT tenant_id, device_id, vendor, model, role, sw_new, site_id, hostname, 1
FROM bench.devices FINAL
WHERE unit BETWEEN {u_lo:UInt16} AND {u_hi:UInt16};

INSERT INTO flowseer.device_site_src
SELECT tenant_id, device_id, site_id, fsb_uuid(toString(device_id), 'placement'),
       toDateTime('2026-01-01 00:00:00', 'UTC'), toDateTime(0, 'UTC'), 1
FROM bench.devices FINAL
WHERE unit BETWEEN {u_lo:UInt16} AND {u_hi:UInt16};

INSERT INTO flowseer.location_src
SELECT DISTINCT tenant_id, cityHash64(tenant_id, region), toUInt64(0), 'region', region, 1
FROM bench.devices FINAL
WHERE unit BETWEEN {u_lo:UInt16} AND {u_hi:UInt16};

INSERT INTO flowseer.location_src
SELECT DISTINCT tenant_id, cityHash64(tenant_id, site_id), cityHash64(tenant_id, region), 'site', site_id, 1
FROM bench.devices FINAL
WHERE unit BETWEEN {u_lo:UInt16} AND {u_hi:UInt16};

SYSTEM RELOAD DICTIONARY flowseer.device_dim;
SYSTEM RELOAD DICTIONARY flowseer.device_site_dict;
SYSTEM RELOAD DICTIONARY flowseer.location_dict;
SYSTEM RELOAD DICTIONARY flowseer_acl.user_tenants_dict;

-- @samples
-- One poll per interface every 300 s, per-device phase 0..59 s plus 0..2 s jitter.
INSERT INTO flowseer.interface_samples
(
    tenant_id, device_id, interface_name, ts, if_index, kind, parent_interface_name, vlan_id,
    admin_status, oper_status, mtu, description, last_change, speed_bps, duplex, discontinuity_at,
    in_bytes, out_bytes, in_unicast_packets, out_unicast_packets, in_multicast_packets,
    out_multicast_packets, in_broadcast_packets, out_broadcast_packets, in_errors, out_errors,
    in_discards, out_discards, present_mask, retention_class, site_id, software_version
)
SELECT tenant_id, device_id, interface_name, toDateTime(t, 'UTC'), if_index, toUInt8(10), '', toUInt16(0),
       admin_status, toUInt8(if(connected AND NOT down, 1, 2)), mtu, description,
       toDateTime(multiIf(down, fstart, flapped AND t >= fstart + fdur, fstart + fdur, 1785542400), 'UTC'),
       if(connected, port_speed, 0), toUInt8(if(connected, 3, 1)),
       toDateTime(if(reports_disc, e + if(nh % 20 = 0, 1, 0), 0), 'UTC'),
       in_b, out_b, intDiv(in_b, pkt_size), intDiv(out_b, pkt_size),
       intDiv(in_b, pkt_size * 60), intDiv(out_b, pkt_size * 80),
       intDiv(in_b, pkt_size * 200), intDiv(out_b, pkt_size * 400),
       in_err, intDiv(in_err, 3), intDiv(in_b, 10000000), intDiv(out_b, 20000000),
       toUInt32(if(reports_disc, 0x1FFF, 0x0FFF) + 0x3F0000),
       retention_class, site_id, fsb_sw(sw_old, sw_new, upgrade_at, t)
FROM
(
    SELECT *,
           toUInt32({h:UInt32} + k * 300 + poll_off + cityHash64(20261008, device_id, if_idx, k, {h:UInt32}) % 3) AS t,
           toUInt32(fsb_epoch(t, reset_period, reset_off)) AS e,
           fsb_fh(device_id, interface_name, intDiv(t, 86400)) AS fh,
           connected AND (fh % 1000 < flap_permille) AS flapped,
           toUInt32(fsb_fstart(fh, intDiv(t, 86400) * 86400)) AS fstart,
           toUInt32(fsb_fdur(fh)) AS fdur,
           flapped AND t >= fstart AND t < fstart + fdur AS down,
           cityHash64(20261008, device_id, if_idx, intDiv(t, 300)) AS nh,
           fsb_ctr(rate_Bps, e, t, nh) AS in_b,
           fsb_ctr(rate_Bps * out_ratio, e, t, intDiv(nh, 1000)) AS out_b,
           fsb_err(err_old, err_new, toInt64(e), toInt64(if(upgrade_at = 0, 4000000000, upgrade_at)), toInt64(t)) AS in_err
    FROM bench.interfaces FINAL
    ARRAY JOIN range(12) AS k
    WHERE tenant_idx BETWEEN {lo:UInt32} AND {hi:UInt32}
)
WHERE {rd:UInt8} = 0 OR cityHash64(device_id, interface_name, t) % 100 = 0;

-- @changes
-- Two oper_status rows per flap (down, up), from the same flap function the samples use.
INSERT INTO flowseer.interface_changes
(
    tenant_id, device_id, interface_name, ts, record_id, attribute, old_value, new_value,
    new_oper_status, retention_class, site_id, software_version
)
SELECT tenant_id, device_id, interface_name, toDateTime(toUInt32(fstart + w * fdur) AS ct, 'UTC'),
       fsb_uuid(concat(toString(device_id), interface_name), toString(ct)),
       'oper_status', if(w = 0, 'up', 'down'), if(w = 0, 'down', 'up'), toUInt8(if(w = 0, 2, 1)),
       retention_class, site_id, fsb_sw(sw_old, sw_new, upgrade_at, ct)
FROM
(
    SELECT *,
           fsb_fh(device_id, interface_name, intDiv({d:UInt32}, 86400)) AS fh,
           toUInt32(fsb_fstart(fh, {d:UInt32})) AS fstart,
           toUInt32(fsb_fdur(fh)) AS fdur
    FROM bench.interfaces FINAL
    WHERE tenant_idx BETWEEN {lo:UInt32} AND {hi:UInt32}
      AND connected AND fh % 1000 < flap_permille
)
ARRAY JOIN [0, 1] AS w
WHERE {rd:UInt8} = 0 OR cityHash64(device_id, interface_name, toUInt32(fstart + w * fdur)) % 100 = 0
SETTINGS enable_analyzer = 1;

-- @syslog_flaps
-- 80 % of link flaps also produce a LINK-3-UPDOWN style syslog line (dossier 11 section 7.1
-- "link-flap tag correlated with interface_changes rows at 80 %").
INSERT INTO flowseer.syslog
(
    tenant_id, device_id, ts, record_id, sent_at, severity, facility, hostname, app_name, proc_id,
    msg_id, sd_ids, sd_param_sd_id, sd_param_name, sd_param_value, message, message_truncated,
    source_ip, vendor_family, vendor_module, vendor_mnemonic, vendor_event_id, vendor_severity,
    vendor_sequence, log_format, device_clock, parse_status, transport, transport_authenticated,
    repeat_count, binding_id, edge_id, site_id, software_version, retention_class
)
SELECT tenant_id, device_id,
       addMilliseconds(toDateTime64(toUInt32(fstart + w * fdur) AS ct, 3, 'UTC'), 1000 + ct % 1000) AS lts,
       fsb_uuid(toString(device_id), concat('flap-', interface_name, '-', toString(ct))),
       lts - toIntervalMillisecond(200), toInt8(3), toInt8(23),
       concat('dev-', tenant_id, '-', toString(dev_idx)), '', '', '', [], [], [], [],
       multiIf(vendor = 'cisco' AND w = 0, concat('%LINK-3-UPDOWN: Interface ', interface_name, ', changed state to down'),
               vendor = 'cisco', concat('%LINK-3-UPDOWN: Interface ', interface_name, ', changed state to up'),
               vendor = 'huawei', concat('%%01IFNET/3/LINK_STATE(l)[', toString(ct % 1000), ']:The line protocol on the interface ', interface_name, if(w = 0, ' has entered the DOWN state.', ' has entered the UP state.')),
               concat('lldpd: link ', if(w = 0, 'down', 'up'), ' on ', interface_name)),
       false, toIPv6(concat('10.', toString(tenant_idx % 250), '.', toString(dev_idx % 256), '.', toString(intDiv(dev_idx, 256) % 256 + 1))),
       '', '', '', '', toInt8(-1), toUInt64(0),
       if(vendor IN ('cisco', 'huawei'), 'rfc3164', 'rfc5424'), 'absolute', 'ok', 'udp', false,
       toUInt32(1), toUUID('00000000-0000-0000-0000-000000000000'), toUUID('00000000-0000-0000-0000-000000000000'),
       site_id, fsb_sw(sw_old, sw_new, upgrade_at, ct), retention_class
FROM
(
    SELECT *,
           fsb_fh(device_id, interface_name, intDiv({d:UInt32}, 86400)) AS fh,
           toUInt32(fsb_fstart(fh, {d:UInt32})) AS fstart,
           toUInt32(fsb_fdur(fh)) AS fdur
    FROM bench.interfaces FINAL
    WHERE tenant_idx BETWEEN {lo:UInt32} AND {hi:UInt32}
      AND connected AND fh % 1000 < flap_permille
)
ARRAY JOIN [0, 1] AS w
WHERE cityHash64(20261008, device_id, interface_name, toUInt32(fstart + w * fdur), 'sl') % 10 < 8
  AND ({rd:UInt8} = 0 OR cityHash64(device_id, interface_name, toUInt32(fstart + w * fdur), 'sl') % 100 = 0)
SETTINGS enable_analyzer = 1;

-- @syslog
-- Per device-hour count: lognormal device rate (median 25/h) times 0.5..1.5 per hour;
-- storm hour adds 30,000 lines, 95 % identical. Severity mix per dossier 07 section 6.1.
INSERT INTO flowseer.syslog
(
    tenant_id, device_id, ts, record_id, sent_at, severity, facility, hostname, app_name, proc_id,
    msg_id, sd_ids, sd_param_sd_id, sd_param_name, sd_param_value, message, message_truncated,
    source_ip, vendor_family, vendor_module, vendor_mnemonic, vendor_event_id, vendor_severity,
    vendor_sequence, log_format, device_clock, parse_status, transport, transport_authenticated,
    repeat_count, binding_id, edge_id, site_id, software_version, retention_class
)
WITH
    [
        ['%SYS-0-PANIC: Unrecoverable error in process {n}'],
        ['%PLATFORM-1-TEMP_ALERT: Temperature sensor {n} exceeds alert threshold', '%HA-1-FAILOVER: Supervisor failover initiated'],
        ['%PLATFORM-2-PS_FAIL: Power supply {n} failed', '%SYS-2-MALLOCFAIL: Memory allocation of {n} bytes failed'],
        ['%LINK-3-UPDOWN: Interface {if}, changed state to down', '%SNMP-3-AUTHFAIL: Authentication failure for SNMP req from host {ip}'],
        ['%DHCP_SNOOPING-4-DHCP_SNOOPING_ERRDISABLE_WARNING: DHCP Snooping received {n} DHCP packets on interface {if}',
         '%SW_MATM-4-MACFLAP_NOTIF: Host {mac} in vlan 10 is flapping between port {if} and port {if2}',
         '%ENVMON-4-FAN_LOW_RPM: Fan {n} is running below threshold',
         '%ARP-4-DUPADDR: Duplicate address {ip} on {if}'],
        ['%SYS-5-CONFIG_I: Configured from console by admin on vty0 ({ip})', '%LINEPROTO-5-UPDOWN: Line protocol on Interface {if}, changed state to up'],
        ['%SEC-6-IPACCESSLOGP: list 101 denied tcp {ip}({n}) -> 10.0.0.1(443), 1 packet', '%DOT1X-6-AUTH_SUCCESS: Authentication successful for client ({mac}) on Interface {if}'],
        ['%SYS-7-DEBUG: process {n} heartbeat', '%PM-7-DEBUG: port {if} state poll']
    ] AS tpl_cisco,
    [
        ['%%01SYSTEM/0/PANIC(l)[{n}]:System panic in process {n}.'],
        ['%%01DEVM/1/TEMP_ALARM(l)[{n}]:The temperature exceeded the alarm threshold.'],
        ['%%01DEVM/2/POWER_FAIL(l)[{n}]:Power module {n} failed.'],
        ['%%01IFNET/3/LINK_STATE(l)[{n}]:The line protocol on the interface {if} has entered the DOWN state.', '%%01SNMP/3/AUTHFAIL(l)[{n}]:SNMP authentication failed from {ip}.'],
        ['%%01DHCPSNP/4/DROP(l)[{n}]:DHCP snooping dropped packets on interface {if}.',
         '%%01MACFLAP/4/FLAP(l)[{n}]:MAC {mac} flapping between {if} and {if2}.',
         '%%01FAN/4/LOW(l)[{n}]:Fan speed is low.',
         '%%01ARP/4/DUP(l)[{n}]:Duplicate IP {ip}.'],
        ['%%01SHELL/5/CMDRECORD(l)[{n}]:Recorded command information. (Ip={ip}, User=admin)'],
        ['%%01SEC/6/ACL_DENY(l)[{n}]:ACL denied packet from {ip}.'],
        ['%%01DEBUG/7/POLL(l)[{n}]:Port {if} poll.']
    ] AS tpl_huawei,
    [
        ['kernel: panic code {n}'],
        ['kernel: thermal alert sensor {n}'],
        ['kernel: power module {n} failed'],
        ['lldpd: link down on {if}', 'snmpd: authentication failure from {ip}'],
        ['dhcpd: DHCP snooping violation on port {if}', 'kernel: mac {mac} moved from {if} to {if2}',
         'fand: fan {n} slow', 'arpd: duplicate address {ip}'],
        ['sshd: Accepted publickey for admin from {ip} port {n}'],
        ['sshd: session opened for user admin from {ip}', 'dhcpd: DHCPACK on {ip} to {mac}'],
        ['lldpd: poll {if}']
    ] AS tpl_generic
SELECT tenant_id, device_id, ts, record_id, ts - toIntervalMillisecond(200), sev, toInt8(if(family = 3, 3, 23)),
       hostname, if(family = 3, splitByChar(':', tpl)[1], ''), '', '', [], [], [], [],
       message, false,
       toIPv6(concat('10.', toString(tenant_idx % 250), '.', toString(dev_idx % 256), '.', toString(intDiv(dev_idx, 256) % 256 + 1))),
       '', '', '', '', toInt8(-1), toUInt64(0),
       if(family = 3, 'rfc5424', 'rfc3164'), 'absolute', 'ok', 'udp', false,
       toUInt32(1), toUUID('00000000-0000-0000-0000-000000000000'), toUUID('00000000-0000-0000-0000-000000000000'),
       site_id, fsb_sw(sw_old, sw_new, upgrade_at, {h:UInt32}), retention_class
FROM
(
    SELECT *,
           cityHash64(20261008, device_id, {h:UInt32}, k) AS sh,
           k >= n_base AS in_storm,
           in_storm AND sh % 100 < 95 AS storm_same,
           toInt8(if(in_storm, 4,
                     multiIf(sh % 10000 < 2, 0, sh % 10000 < 10, 1, sh % 10000 < 50, 2, sh % 10000 < 300, 3,
                             sh % 10000 < 800, 4, sh % 10000 < 2000, 5, sh % 10000 < 9000, 6, 7))) AS sev,
           multiIf(vendor = 'cisco', 1, vendor = 'huawei', 2, 3) AS family,
           arrayElement(multiIf(family = 1, tpl_cisco, family = 2, tpl_huawei, tpl_generic), sev + 1) AS variants,
           arrayElement(variants, intDiv(sh, 10000) % length(variants) + 1) AS tpl,
           fsb_ifname(n_if, intDiv(sh, 10000000) % n_if) AS ifn,
           fsb_ifname(n_if, intDiv(sh, 100000000) % n_if) AS ifn2,
           concat('10.', toString(intDiv(sh, 7) % 256), '.', toString(intDiv(sh, 11) % 256), '.', toString(intDiv(sh, 13) % 254 + 1)) AS ip,
           lower(leftPad(hex(intDiv(sh, 3) % 281474976710656), 12, '0')) AS mac_hex,
           concat(substring(mac_hex, 1, 4), '.', substring(mac_hex, 5, 4), '.', substring(mac_hex, 9, 4)) AS mac_txt,
           if(storm_same,
              concat('%SW_MATM-4-MACFLAP_NOTIF: Host ', substring(lower(hex(cityHash64(device_id))), 1, 4),
                     '.0000.0001 in vlan 10 is flapping between port GigabitEthernet1/0/1 and port GigabitEthernet1/0/2'),
              replaceAll(replaceAll(replaceAll(replaceAll(replaceAll(tpl, '{if2}', ifn2), '{if}', ifn), '{ip}', ip),
                                    '{mac}', mac_txt), '{n}', toString(intDiv(sh, 17) % 5000))) AS message,
           addMilliseconds(toDateTime64({h:UInt32}, 3, 'UTC'), sh % 3600000) AS ts,
           fsb_uuid(toString(device_id), concat(toString({h:UInt32}), '-', toString(k))) AS record_id
    FROM
    (
        SELECT *,
               toUInt32(syslog_per_hour * (0.5 + (cityHash64(20261008, device_id, {h:UInt32}) % 1000) / 1000)) AS n_base,
               n_base + if(storm_at = {h:UInt32}, 30000, 0) AS n_rows
        FROM bench.devices FINAL
        WHERE tenant_idx BETWEEN {lo:UInt32} AND {hi:UInt32}
    )
    ARRAY JOIN range(n_rows) AS k
)
WHERE {rd:UInt8} = 0 OR cityHash64(record_id) % 100 = 0
SETTINGS enable_analyzer = 1;

-- @probes
-- One row per (site target, minute): 10 probes per minute, lognormal RTT around the
-- target's base (sigma 0.35), 0.2 % random loss, 2 % of target-hours with a 30-minute
-- 50 % loss episode, 2 % of targets dark for one whole day.
INSERT INTO flowseer.probe_intervals
(
    tenant_id, device_id, interface_name, target_ip, interval_start, interval_s, prober, edge_id,
    sent, received, rtt_us, latency_us, jitter_us, loss_bp, present_mask, retention_class, site_id,
    software_version
)
SELECT tenant_id, device_id, '', target_ip, toDateTime(t, 'UTC'), toUInt16(60), 'edge', '',
       toUInt16(10), toUInt16(length(rtts_ok)), rtts_ok,
       toUInt32(if(empty(rtts_ok), 0, arrayAvg(rtts_ok))),
       toUInt32(if(length(rtts_ok) < 2, 0, arrayAvg(arrayMap(x -> abs(x), arrayPopFront(arrayDifference(rtts_ok)))))),
       toUInt16(intDiv(10000 * (10 - length(rtts_ok)), 10)), toUInt8(7), retention_class, site_id,
       fsb_sw(sw_old, sw_new, upgrade_at, t)
FROM
(
    SELECT *,
           toUInt32({h:UInt32} + m * 60) AS t,
           cityHash64(20261008, device_id, target_idx, {h:UInt32}) AS hh,
           dark_day > 0 AND t >= dark_day AND t < dark_day + 86400 AS dark,
           hh % 100 < 2 AND m >= hh % 30 AND m < hh % 30 + 30 AS lossy,
           arrayMap(j -> toUInt32(base_us * exp(0.35 * fsb_z(cityHash64(20261008, device_id, target_idx, t, j)))), range(10)) AS rtts,
           arrayMap(j -> (NOT dark)
                         AND ((NOT lossy) OR cityHash64(20261008, device_id, target_idx, t, j, 'l') % 2 = 0)
                         AND cityHash64(20261008, device_id, target_idx, t, j, 'b') % 1000 >= 2, range(10)) AS oks,
           arrayFilter((r, ok) -> ok, rtts, oks) AS rtts_ok
    FROM bench.probe_targets FINAL
    ARRAY JOIN range(60) AS m
    WHERE tenant_idx BETWEEN {lo:UInt32} AND {hi:UInt32}
)
WHERE {rd:UInt8} = 0 OR cityHash64(device_id, target_ip, t) % 100 = 0
SETTINGS enable_analyzer = 1;

-- @fdb
-- Presence evidence for one six-hour block of one day: one row per present member,
-- first_seen/last_seen inside the block, walks = 72 (one walk per 300 s). Member model:
-- slot lifetime 20 days (access, 5 %/day turnover) or 50 days (dist, chassis); port
-- epoch 200 days (0.5 %/day moves at day boundaries); 5 % of slots are roaming hosts
-- shared inside the site (64 per site), present on a device on one day in three, on a
-- port chosen per day, so "where was MAC X" spans several devices.
INSERT INTO flowseer.fdb_presence
(
    tenant_id, device_id, day, network_instance, vlan_id, mac, interface_name, first_seen, last_seen,
    walks, kind, status, retention_class, site_id, software_version
)
SELECT tenant_id, device_id, toDate(toDateTime({d:UInt32}, 'UTC')), 'default', vlan_id, mac, interface_name,
       toDateTime({d:UInt32} + {b:UInt8} * 21600 + cityHash64(device_id) % 60, 'UTC'),
       toDateTime({d:UInt32} + ({b:UInt8} + 1) * 21600 - 300 + cityHash64(device_id) % 60, 'UTC'),
       toUInt32(72), 'dynamic', 'active', retention_class, site_id,
       fsb_sw(sw_old, sw_new, upgrade_at, {d:UInt32} + {b:UInt8} * 21600)
FROM
(
    SELECT tenant_id, device_id, site_id, retention_class, sw_old, sw_new, upgrade_at, n_if, role,
           cityHash64(20261008, device_id, slot) AS sh,
           toUInt32(intDiv({d:UInt32}, 86400)) AS dd,
           toUInt32(if(role IN ('chassis', 'dist'), 50, 20)) AS lifetime,
           sh % 20 = 0 AS roamer,
           intDiv(dd + sh % lifetime, lifetime) AS gen,
           bitAnd(if(roamer, cityHash64(20261008, tenant_id, site_id, 'roam', sh % 64),
                             cityHash64(20261008, device_id, slot, gen)), 0xFEFFFFFFFFFF) AS mac,
           (NOT roamer) OR cityHash64(20261008, mac, device_id, dd) % 3 = 0 AS present,
           if(roamer, cityHash64(20261008, mac, device_id, dd) % n_if,
                      cityHash64(20261008, device_id, slot, gen, intDiv(dd + sh % 200, 200)) % n_if) AS port_idx,
           toUInt16(10 * (1 + sh % 10)) AS vlan_id,
           fsb_ifname(n_if, port_idx) AS interface_name
    FROM bench.devices FINAL
    ARRAY JOIN range(fdb_members) AS slot
    WHERE tenant_idx BETWEEN {lo:UInt32} AND {hi:UInt32}
)
WHERE present AND ({rd:UInt8} = 0 OR cityHash64(device_id, mac, {b:UInt8}) % 100 = 0)
SETTINGS enable_analyzer = 1;

-- @walks
-- 288 FDB walk markers per device-day (decision 4): member count and order-independent
-- set hash (groupBitXor over distinct cityHash64(vlan, mac, interface)); 3 % of walks are
-- partial (complete = false, 60 % of members, a different hash).
INSERT INTO flowseer.set_walks
(
    tenant_id, device_id, kind, ts, record_id, complete, members, set_hash, reason, retention_class,
    site_id, software_version
)
SELECT tenant_id, device_id, 'fdb',
       toDateTime(toUInt32({d:UInt32} + w * 300 + cityHash64(device_id) % 60) AS wt, 'UTC'),
       fsb_uuid(toString(device_id), toString(wt)),
       (cityHash64(20261008, device_id, wt) % 100 >= 3) AS complete,
       if(complete, m_cnt, toUInt32(m_cnt * 0.6)), if(complete, s_hash, cityHash64(s_hash, wt)),
       'walk', retention_class, site_id, fsb_sw(sw_old, sw_new, upgrade_at, wt)
FROM
(
    SELECT tenant_id, device_id, any(site_id) AS site_id, any(retention_class) AS retention_class,
           any(sw_old) AS sw_old, any(sw_new) AS sw_new, any(upgrade_at) AS upgrade_at,
           toUInt32(uniqExact(vlan_id, mac, interface_name)) AS m_cnt,
           groupBitXorDistinct(cityHash64(vlan_id, mac, interface_name)) AS s_hash
    FROM
    (
        SELECT tenant_id, device_id, site_id, retention_class, sw_old, sw_new, upgrade_at, n_if, role,
               cityHash64(20261008, device_id, slot) AS sh,
               toUInt32(intDiv({d:UInt32}, 86400)) AS dd,
               toUInt32(if(role IN ('chassis', 'dist'), 50, 20)) AS lifetime,
               sh % 20 = 0 AS roamer,
               intDiv(dd + sh % lifetime, lifetime) AS gen,
               bitAnd(if(roamer, cityHash64(20261008, tenant_id, site_id, 'roam', sh % 64),
                                 cityHash64(20261008, device_id, slot, gen)), 0xFEFFFFFFFFFF) AS mac,
               (NOT roamer) OR cityHash64(20261008, mac, device_id, dd) % 3 = 0 AS present,
               if(roamer, cityHash64(20261008, mac, device_id, dd) % n_if,
                          cityHash64(20261008, device_id, slot, gen, intDiv(dd + sh % 200, 200)) % n_if) AS port_idx,
               toUInt16(10 * (1 + sh % 10)) AS vlan_id,
               fsb_ifname(n_if, port_idx) AS interface_name
        FROM bench.devices FINAL
        ARRAY JOIN range(fdb_members) AS slot
        WHERE tenant_idx BETWEEN {lo:UInt32} AND {hi:UInt32}
    )
    WHERE present
    GROUP BY tenant_id, device_id
)
ARRAY JOIN range(288) AS w
SETTINGS enable_analyzer = 1;
