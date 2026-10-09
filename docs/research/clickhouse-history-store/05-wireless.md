---
title: Wireless radios, BSS, RF neighbors, and clients
date: 2026-10-08
status: research; sources fetched 2026-10-08, ClickHouse 26.8 LTS
---

# 05 Wireless: radios, BSS, RF neighbors, wireless clients

Read-only report for the ClickHouse history store plan. Builds on
`docs/research/clickhouse-history-store/` (cited as F-numbers from 02 and as
"03, section" for prior art). Repository paths are relative to the worktree
`/Users/a.tegtmeier/Projects/worktrees/FlowSeer/plan-clickhouse-store`.
External pages were fetched on 2026-10-08; anything no fetched source
confirms says "unverified" and is collected in section 10.

## 0. Result in one screen

| Domain | Table | Engine and key | Rows/day at 20k APs | Why this shape |
| --- | --- | --- | --- | --- |
| Radio RF and airtime | `radio_samples` | ReplicatedReplacingMergeTree `(tenant_id, device_id, radio_index, ts)` | 34.6 M at 150 s | Gauges, raw at source interval (F25, 03 samples vs changes); BSS client counts ride along as `Nested` arrays so no second 100 M-row table |
| Radio configuration transitions | `radio_changes` | ReplicatedReplacingMergeTree `(tenant_id, device_id, radio_index, ts, record_id)` | about 20 k (channel changes are rare) | "When did the channel change" is a key-range read, not a window function over samples |
| BSS and WLAN attribute transitions | `bss_changes`, `wlan_changes` | same as `radio_changes` with `bssid` or `wlan_id` in the key | under 100 k | SSID, security, VLAN, hidden, WlanStatus change rarely |
| RF neighbor sightings | `neighbor_presence` (hourly bucket) | ReplicatedAggregatingMergeTree `(tenant_id, device_id, bssid, hour)` | 8.2 M after merge (49 M inserted) | Idempotent `min/max/argMax` presence per neighbor per hour bounds volume to 24 rows per neighbor per day whatever the poll interval (03, set-valued domains) |
| RF neighbor raw sightings | `neighbor_sightings` (optional, 7 d) | ReplicatedReplacingMergeTree `(tenant_id, device_id, bssid, ts)` | 49 M | Only if per-sighting RSSI is needed for a live rogue hunt; TTL 7 days |
| Wireless client samples | `client_samples` | ReplicatedReplacingMergeTree `(tenant_id, mac, ts)` | 11.5 M at 300 s | RSSI/SNR/rate/counters per poll, short retention |
| Wireless client presence per attachment | `client_presence` | ReplicatedAggregatingMergeTree `(tenant_id, ap_key, bssid, mac, hour)` | about 1.5 M | Per-AP and per-site questions; sessions are chains of rows |
| Cross-device MAC lookup | `client_lookup` | ReplicatedAggregatingMergeTree `(tenant_id, mac, hour, ap_key, bssid)` fed by MV | same rows as `client_presence` | "Where was MAC X in the last 7 days" is a primary-key range: cost = hours x attachments of that one MAC |
| Client events | `client_events` | ReplicatedReplacingMergeTree `(tenant_id, mac, ts, record_id)` | roams about 1 M, failures and lifecycle small | Roam history and failures as rows; `EndpointEvent` arms map one to one |
| Rollups | `radio_hourly`, `ssid_clients_hourly` | ReplicatedAggregatingMergeTree keyed by bucket | 1.4 M and 1.4 M | Months of charts and "clients per SSID per hour per site" without touching raw rows |

Every query shape below has a cost model in rows and granules as a function
of (devices in scope, entities per device, window, poll interval). None
grows with total table size or total tenants. The two that would have
(`WHERE mac = X` without a key, `uniq` over raw presence for a site over
months) are redesigned with `client_lookup` and `ssid_clients_hourly`.

Facts the plan must carry:

- The envelope `IngestRecord` has one payload arm today, `syslog`
  (`spec/proto/flowseer/integration/ingest/v1/ingest_record.proto:25-30`).
  Radio, neighbor, and endpoint records are not yet ingest record types. The
  columns below come from the model and primitive messages that will become
  those arms: `RadioFacet` (carried on `ComponentState.radio`,
  `spec/proto/flowseer/model/inventory/v1/component.proto:207-209`),
  `NeighborBss`, `EndpointState`, `EndpointEvent`.
- `NeighborBss` is defined but nothing embeds it: a grep over `spec/proto/flowseer`
  finds it only in `net/wlan/v1/neighbor_bss.proto` and the package README. A
  carrier message (a neighbor scan record listing sightings per radio) is a
  schema change the plan has to name.
- F45 and 12.3 say T64 "would crop the top 16 bits" of a MAC in UInt64. That
  is backwards: T64 "crops unused high bits of values in integer data types"
  (codec reference, section 9), so a 48-bit MAC in `UInt64` is exactly the
  case T64 serves. This report uses `CODEC(T64, ZSTD(1))` on every MAC column.

## 1. Data semantics

### 1.1 Radios (`spec/proto/flowseer/net/wlan/v1/radio_facet.proto`)

| Field (line) | Kind | Change rate | Storage |
| --- | --- | --- | --- |
| `radio_index` (40) | key | never | key column |
| `band` (42), `standard` (55), `operating_class` (50), `country_code` (71), `country_environment` (73) | configuration state | months | `radio_changes` plus a constant-run column in samples |
| `primary_channel` (44), `secondary_channel` (46), `channel_width_mhz` (48) | state that RRM/DFS changes | hours to days; DFS hits cause bursts (OpenConfig `CHANGE_REASON_TYPE`: `DFS`, `NOISE`, `ERRORS`, `BETTER_CHANNEL`, section 9) | both |
| `admin_status` (57), `oper_status` (59) | state | rare, flaps on reboot (3 % of APs reboot per day, baseline) | both |
| `tx_power_millidbm` (61), `max_tx_power_millidbm` (63), `eirp_millidbm` (65), `antenna_gain_millidbi` (67) | state, power is RRM-adjusted | hours | samples; `tx_power` also in changes |
| `noise_floor_millidbm` (69) | gauge | every poll | samples |
| `channel_utilization` (79): `total/tx_dot11/rx_dot11/obss_rx/non_dot11_avg_basis_points`, `window` (`channel_utilization.proto:14-24`) | windowed statistic, 0..10000 | every poll | samples, `UInt16` |
| `bsses` (81): per BSS `bssid`, `ssid`, `security`, `pmf`, `vlan_id`, `hidden`, `beacon_interval`, `dtim_period`, `associated_client_count` (`bss.proto:16-42`) | set of 1..7 rows per radio; one gauge (`associated_client_count`), the rest attributes | attributes rare; count every poll | gauge as `Nested` arrays in samples; attributes in `bss_changes` |

No counters exist on `RadioFacet` or `Bss` (the package README lists "Radio
and BSS hardware counters" as deliberately absent). So the radio domain is
gauges and states only, and the counter rule (schema building blocks, rule 2)
does not apply here. Ruckus reports channel utilization as an "exponential
average" and Meraki as a windowed value (dossier 02, traps); the `window`
field keeps them apart, stored as `window_s UInt16`.

Per device: 2 to 3 radios (baseline fan-out), 3 SSIDs median and 7 max per
AP, so 1 to 7 BSS rows per radio. Poll 2 to 3 minutes from controllers.

### 1.2 WLAN entity (`spec/proto/flowseer/model/wireless/v1/wlan.proto`)

`WlanState` is current state (NATS KV). `WlanEvent` (146-168) is one
transition `from`/`to` of `WlanStatus` per WLAN id: an append-only change row.
Volume is negligible (one row when an SSID stops being broadcast anywhere).

### 1.3 RF neighbors (`spec/proto/flowseer/net/wlan/v1/neighbor_bss.proto`)

A sighting is `(bssid, ssid, band, primary_channel, channel_width_mhz,
rssi_millidbm, security, classification, radio_name)` (lines 22-42) reported
per device per scan. Semantics: a set per (device, radio) whose members churn
slowly (neighbors are the same buildings' APs) with one gauge per member
(`rssi_millidbm`) and one slowly changing enum (`classification`). Cardinality
17 per AP median, 70 max (baseline). It was 45 % of the legacy store's rows
because each scan wrote every member again. Scan interval 10 minutes (Cisco);
Ruckus neighbor polling "silently stops after the first API error" (baseline,
data quality), so gaps are a source failure, not an absence.

### 1.4 Wireless clients (`spec/proto/flowseer/model/endpoint/v1/endpoint.proto`, `spec/proto/flowseer/net/endpoint/v1/*.proto`)

| Message | Kind | Rows |
| --- | --- | --- |
| `EndpointState` (47-83): `ref.endpoint.id` UUID, `lifecycle`, `hostname`, `observed_mac_addresses` (min 1), `ipv4_address`, `ipv6_addresses`, `fingerprint`, `counters`, `oneof attachment { wired, wireless }` | a periodic sample of one client as the controller sees it | one per client per poll (3 to 10 min) |
| `WirelessAttachment` (`wireless_attachment.proto:12-65`): `ap_name`, `oneof serving_bss { bssid, vendor_bss_id }`, `ssid`, `band`, `channel`, `rssi_millidbm`, `snr_millidb`, `negotiated_rate_bps`, `mcs`, `nss`, `guard_interval` | attachment identity plus RF gauges | in the sample |
| `EndpointCounters` (`endpoint_counters.proto:8-21`): `in_bytes`, `out_bytes`, `in_frames`, `out_frames`, `tx_retry_frames`, `last_discontinuity` | cumulative counters with discontinuity marker (counter rule) | in the sample |
| `EndpointEvent` (142-157): `lifecycle` (from/to), `roam` (`from_attachment`, `to_attachment`, `reason`), `connection_failure` (`stage`, `detail`, attempted attachment) | events | per transition |

Two traps the sink must handle before the store sees a row:

- Ruckus client counters are per-report deltas: `spec/proto/ruckus/ap/ap_client.proto:81-107`
  marks `rxFrames`, `rxBytes`, `txFrames`, `txBytes` with `@property delta`.
  Cisco reports cumulative per-association counters (baseline, data quality).
  `EndpointCounters` is defined cumulative, so the Ruckus adapter accumulates
  at the edge and sets `last_discontinuity` at association start. The store
  stores what the contract says and never guesses.
- MAC randomisation. Apple: "By default, your device improves privacy by using
  a different MAC address for each Wi-Fi network"; with iOS 18 the Rotating
  setting "rotates to a different private address every 2 weeks" and is the
  default only on weak or open networks, Fixed is the default on WPA2 or
  stronger (support.apple.com/102509). Android: persistent randomisation
  derives the MAC from the network profile and keeps it "until a factory
  reset"; non-persistent randomisation (Android 12+, opt-in per suggestion or
  open SSID) re-randomises at connection start when the DHCP lease expired and
  more than 4 hours passed since disconnect, or the MAC is older than 24 hours
  (source.android.com, wifi-mac-randomization-behavior). So within one SSID a
  MAC is stable for days to weeks for most clients, and "where was MAC X in
  the last 7 days" is a well-posed question per tenant; the cross-MAC identity
  is the endpoint service's job (`endpoint_id`, README "Randomized-MAC merge
  policies (a service concern)"). The store keys on `mac` and carries
  `endpoint_id` as an attribute, so a search by endpoint resolves the
  endpoint's observed MAC list in Postgres and runs `mac IN (...)`.

Controller-reported and AP-reported duplicates: the same association may
arrive from the controller (record device = controller) and from the AP
(record device = AP), each with its own `record_id` and `Provenance.binding`.
The store must not key client history on the reporting device. It keys on the
serving attachment (`ap_key`, `bssid`) and keeps `source_device_id` and
`binding_id` as attributes; the presence aggregates are idempotent, so two
reporters of one fact produce one presence row. For counters and RF gauges
that differ by source, `argMax(..., (ts, source_rank))` takes the preferred
source, with `source_rank` stamped by the sink from a per-integration policy.

Scale (baseline 9 k APs: 15 k online clients, 25 k distinct per day, about 2
per AP). At 20 k APs: 30 k online, 50 k distinct per day, 5 min poll:
30 k x 288 = 8.6 M samples per day; with a 10 min poll 4.3 M. The table below
uses 11.5 M as a 40 k-online upper bound.

## 2. Candidate models

| Domain | Candidates | Verdict |
| --- | --- | --- |
| Radio | (a) wide samples with channel in every row; (b) samples plus `radio_changes`; (c) changes only, samples hourly | (b). (a) answers "when did the channel change" with a window function over all samples in the window (cost = radios x window / interval even if nothing changed). (c) loses airtime detail the baseline collects at 2 min. Constant columns in (b) cost near zero: a run of equal `UInt8` channel values under `ZSTD` compresses to a few bytes per granule, so storing the channel in samples as well is free and gives "what was it at T" from one row |
| BSS client counts | (a) `bss_samples` table, one row per BSS per poll (104 M rows/day); (b) `Nested` arrays on the radio row; (c) only an hourly rollup | (b). Volume stays at the radio row count, the BSS set per radio is at most 7 and polled together with the radio, and `ARRAY JOIN` inside a scope-bounded read is cheap. (a) triples the largest wireless sample table for one `UInt16`. (c) loses the per-poll count a troubleshooter wants for the last hour |
| BSS/WLAN attributes | (a) in every sample; (b) change rows | (b), with the attribute also stored once per hour in presence-style rows was considered and rejected: `bss_changes` plus the current state in KV covers "what SSID did this BSSID carry at T" with `argMax(..., ts) WHERE ts <= T` |
| Neighbors | (a) full set per scan (legacy, 45 % of volume); (b) change log with snapshots (F43); (c) presence per hour with idempotent aggregates (03, set-valued domains); (d) presence per day | (c), plus optional raw sightings at 7 days. (b) needs an ordered baseline that at-least-once delivery does not give (03) and the sink would hold a per-radio neighbor set. (d) loses the hour of appearance and the RSSI trend inside a day. (c) bounds rows to 24 per member per day and stays correct under redelivery because `min`, `max`, `argMax` are idempotent |
| Clients | (a) session rows written by the sink at session end; (b) presence per (attachment, hour); (c) samples only | (b) plus samples (short retention) plus events. (a) needs the sink to keep every open session in memory and to decide "ended" from silence; a restart or a redelivered old sample re-opens sessions, and a session row written at the end is invisible until then. Presence rows exist from the first poll, chain into sessions at read time, and roam events from `EndpointEvent` give exact transitions where the source reports them |
| Cross-device MAC lookup | (a) `bloom_filter` on `mac` in the AP-keyed presence table; (b) projection ordered by `(tenant, mac, hour)`; (c) second table keyed by mac fed by a MV | (c). (a) reads every index block of the tenant's 7-day range: "if a value occurs even once in an indexed block, it means the entire block must be read" and all values in a block are tested (skipping-indexes page), so cost grows with tenant volume, not with the MAC's history. (b) is not usable on an AggregatingMergeTree: `deduplicate_merge_projection_mode` defaults to `throw` "preventing projection parts from going out of sync" when a merge folds rows (projection reference), and projections cannot carry their own TTL ("Projections don't allow using different TTL", data-modeling page). (c) doubles the presence rows (about 1.5 M/day, small) and makes the lookup a primary-key range |

## 3. DDL

Conventions as in 02 section 12: database `flowseer` on a `Replicated`
database engine, `tenant_id LowCardinality(String)`, `device_id UUID`,
`ts DateTime`, `retention_class Enum8('short'=1,'standard'=2,'long'=3)`
stamped by the sink, `site_id LowCardinality(String)` stamped at write (F69),
no Nullable (F9), `ttl_only_drop_parts = 1`. MAC addresses are `UInt64`
(48-bit EUI-48 from `MacAddress.eui48.octets`, `eui.proto:35-44`;
an EUI-64 arm is stored in the same column with `mac_width UInt8 = 64`), codec
`T64, ZSTD(1)`. SSIDs are `LowCardinality(String)` holding the raw octets (a
non-UTF-8 SSID is still a byte string in ClickHouse; `String` is bytes). Enum8
values copy the protobuf enum numbers so the mapping is mechanical.

### 3.1 `radio_samples`

```sql
CREATE TABLE flowseer.radio_samples
(
    tenant_id        LowCardinality(String),
    device_id        UUID,
    radio_index      UInt8,
    ts               DateTime                CODEC(Delta, ZSTD(1)),
    -- configuration state copied into every sample (constant runs, near-zero cost)
    band             Enum8('unspecified'=0,'ghz2p4'=1,'ghz5'=2,'ghz6'=3,'ghz60'=4),
    standard         Enum8('unspecified'=0,'a'=1,'b'=2,'g'=3,'n'=4,'ac'=5,'ax'=6,'be'=7),
    primary_channel  UInt8                   CODEC(T64, ZSTD(1)),
    secondary_channel UInt8                  CODEC(T64, ZSTD(1)),
    channel_width_mhz UInt16                 CODEC(T64, ZSTD(1)),
    operating_class  UInt8                   CODEC(T64, ZSTD(1)),
    admin_status     Enum8('unspecified'=0,'up'=1,'down'=2),
    oper_status      Enum8('unspecified'=0,'up'=1,'down'=2),
    -- RF gauges, milli-units as on the wire (sint32)
    tx_power_millidbm      Int32             CODEC(T64, ZSTD(1)),
    max_tx_power_millidbm  Int32             CODEC(T64, ZSTD(1)),
    eirp_millidbm          Int32             CODEC(T64, ZSTD(1)),
    antenna_gain_millidbi  Int32             CODEC(T64, ZSTD(1)),
    noise_floor_millidbm   Int32             CODEC(T64, ZSTD(1)),
    -- channel utilization, basis points 0..10000, window in seconds (0 = moving average)
    util_total_bp     UInt16                 CODEC(T64, ZSTD(1)),
    util_tx_dot11_bp  UInt16                 CODEC(T64, ZSTD(1)),
    util_rx_dot11_bp  UInt16                 CODEC(T64, ZSTD(1)),
    util_obss_rx_bp   UInt16                 CODEC(T64, ZSTD(1)),
    util_non_dot11_bp UInt16                 CODEC(T64, ZSTD(1)),
    util_window_s     UInt16                 CODEC(T64, ZSTD(1)),
    -- hosted BSS set of this radio at this poll (Bss.bssid, Bss.ssid, Bss.associated_client_count)
    bss               Nested(bssid UInt64, ssid LowCardinality(String), clients UInt16),
    client_count      UInt16                 CODEC(T64, ZSTD(1)),   -- sum of bss.clients, hot column
    present_mask      UInt32                 CODEC(T64, ZSTD(1)),   -- bit i = field i was set in the facet (F9)
    retention_class   Enum8('short'=1,'standard'=2,'long'=3),
    site_id           LowCardinality(String)
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY toMonday(ts)
ORDER BY (tenant_id, device_id, radio_index, ts)
TTL ts + INTERVAL 90 DAY DELETE WHERE retention_class = 'short',
    ts + INTERVAL 1 YEAR DELETE WHERE retention_class = 'standard',
    ts + INTERVAL 2 YEAR DELETE WHERE retention_class = 'long'
SETTINGS ttl_only_drop_parts = 1, min_age_to_force_merge_seconds = 604800,
         min_age_to_force_merge_on_partition_only = 1;
```

Clause reasoning. Key order tenant, device, radio, time (F3 to F6); the
natural key is unique per poll so a redelivery collapses with no extra column
(F45). `Delta, ZSTD` on `ts` (uniform stride); `T64, ZSTD` on every gauge and
small integer: "T64 for sparse or small ranges" (compression guide) and
milli-dBm gauges sit in a narrow band per part. `Gorilla` is for floats and
is not used. `Nested` stores three parallel arrays with one shared offsets
column; a radio with 3 BSS adds 3 x (8 + 1 + 2) bytes uncompressed and the
`bssid` array is a constant run across polls. `client_count` is materialised
by the sink so a chart never has to `arraySum`. Weekly partitions give 52 to
104 partitions (F7, F8). Settings as in 12.1 so closed weeks merge to one part.

Storage estimate (measured in the benchmark): about 30 columns, most constant
runs or narrow gauges; expect 12 to 20 bytes per row compressed. At
20 k devices x 3 radios x 576 polls = 34.6 M rows/day, 0.4 to 0.7 GB/day,
150 to 250 GB per year of `standard` retention. Write cost: one batch per
flush containing every radio sample of the flush window; at 34.6 M/day that
is 400 rows/s, one part per insert per partition (F63).

### 3.2 `radio_changes`

```sql
CREATE TABLE flowseer.radio_changes
(
    tenant_id      LowCardinality(String),
    device_id      UUID,
    radio_index    UInt8,
    ts             DateTime                CODEC(Delta, ZSTD(1)),
    record_id      UUID,
    attribute      Enum8('primary_channel'=1,'secondary_channel'=2,'channel_width_mhz'=3,
                         'band'=4,'standard'=5,'operating_class'=6,'admin_status'=7,
                         'oper_status'=8,'tx_power_millidbm'=9,'country_code'=10,
                         'country_environment'=11),
    old_value      Int32                   CODEC(T64, ZSTD(1)),   -- 0 with old_known = 0 when the sink had no prior value
    new_value      Int32                   CODEC(T64, ZSTD(1)),
    old_known      UInt8,
    -- typed copies for the hot query (channel history) without decoding attribute
    new_channel    UInt8                   CODEC(T64, ZSTD(1)),
    new_width_mhz  UInt16                  CODEC(T64, ZSTD(1)),
    retention_class Enum8('short'=1,'standard'=2,'long'=3),
    site_id        LowCardinality(String),
    INDEX idx_ts ts TYPE minmax GRANULARITY 4
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY toMonday(ts)
ORDER BY (tenant_id, device_id, radio_index, ts, record_id)
TTL ts + INTERVAL 2 YEAR
SETTINGS ttl_only_drop_parts = 1, min_age_to_force_merge_seconds = 604800,
         min_age_to_force_merge_on_partition_only = 1;
```

Every attribute of `RadioFacet` is numeric or an enum, so one `Int32`
pair covers all (`country_code` is stored as its two ASCII bytes packed, or
as a separate `String` column if the plan prefers readability). The sink
derives a change by comparing the facet with its per-radio last value cache,
seeded from the KV current state at start. A record whose `observed_at` is
older than the cached value is compared but never updates the cache, so a
late redelivery cannot emit a "change back"; an identical redelivery emits
nothing; a redelivery of the record that caused a change re-emits the same
row with the same `record_id` and collapses on merge (F45). `old_known = 0`
marks the first observation after a sink restart. `bss_changes` is the same
table with `bssid UInt64` after `radio_index` in the key and attributes
`ssid`, `security`, `pmf`, `vlan_id`, `hidden`, `beacon_interval_tu`,
`dtim_period` (`bss.proto:16-40`) with `old_value`/`new_value` as `String`.
`wlan_changes` is `(tenant_id, wlan_id UUID, ts, record_id, from Enum8,
to Enum8)` from `WlanEvent` (`wlan.proto:146-168`).

### 3.3 `neighbor_presence` (hourly)

```sql
CREATE TABLE flowseer.neighbor_presence
(
    tenant_id       LowCardinality(String),
    device_id       UUID,
    bssid           UInt64                  CODEC(T64, ZSTD(1)),
    hour            DateTime                CODEC(Delta, ZSTD(1)),
    radio_name      SimpleAggregateFunction(anyLast, LowCardinality(String)),
    first_seen      SimpleAggregateFunction(min, DateTime),
    last_seen       SimpleAggregateFunction(max, DateTime),
    sightings       SimpleAggregateFunction(sum, UInt16),          -- inflated by redelivery, informational only
    rssi_min        SimpleAggregateFunction(min, Int32),
    rssi_max        SimpleAggregateFunction(max, Int32),
    rssi_last       AggregateFunction(argMax, Int32, DateTime),    -- argMax is not a SimpleAggregateFunction (F33)
    ssid            SimpleAggregateFunction(anyLast, LowCardinality(String)),
    band            SimpleAggregateFunction(max, UInt8),
    primary_channel SimpleAggregateFunction(max, UInt8),
    channel_width_mhz SimpleAggregateFunction(max, UInt16),
    security        SimpleAggregateFunction(max, UInt8),
    classification  SimpleAggregateFunction(max, UInt8),           -- highest severity seen in the hour (enum order 0..5)
    retention_class SimpleAggregateFunction(max, UInt8),
    site_id         SimpleAggregateFunction(anyLast, LowCardinality(String)),
    INDEX idx_bssid bssid TYPE bloom_filter(0.01) GRANULARITY 4
)
ENGINE = ReplicatedAggregatingMergeTree
PARTITION BY toMonday(hour)
ORDER BY (tenant_id, device_id, bssid, hour)
TTL hour + INTERVAL 180 DAY DELETE WHERE retention_class = 1,
    hour + INTERVAL 1 YEAR DELETE WHERE retention_class IN (2, 3)
SETTINGS ttl_only_drop_parts = 1;
```

The sink inserts one row per sighting with `first_seen = last_seen = ts`,
`sightings = 1`, `rssi_min = rssi_max = rssi`; merges fold them (F33). A
redelivered sighting changes nothing except `sightings`, which the design
labels informational. `max` on enums takes the most severe classification
seen in the hour, which is what a rogue report wants; `anyLast` on `ssid`
keeps the last beaconed name. The bloom filter serves the rare cross-device
"which APs hear BSSID X" and is kept only if `EXPLAIN indexes = 1` shows it
skipping (F19); the common neighbor queries are key ranges and never touch
it. Rows per day after merge = devices x neighbors x 24 = 20 k x 17 x 24 =
8.2 M; inserted rows = 20 k x 17 x 144 = 49 M (570 rows/s). Bytes per row
after merge: expected 25 to 40 (the `argMax` state holds value plus
timestamp). About 0.3 GB/day, 100 GB/year at `standard` retention.

Optional `neighbor_sightings` for per-sighting RSSI: ReplacingMergeTree
`ORDER BY (tenant_id, device_id, bssid, ts)` with `rssi_millidbm`,
`radio_name`, `primary_channel`, `TTL ts + INTERVAL 7 DAY`, daily
partitions. 49 M rows/day at about 8 bytes/row (two narrow gauges, constant
runs) = 0.4 GB/day, 2.8 GB resident. Include only if the UI shows a
per-scan RSSI trace; the hourly min/max/last covers rogue triage.

### 3.4 `client_samples`

```sql
CREATE TABLE flowseer.client_samples
(
    tenant_id        LowCardinality(String),
    mac              UInt64                  CODEC(T64, ZSTD(1)),
    ts               DateTime                CODEC(Delta, ZSTD(1)),
    source_device_id UUID,                                       -- reporting device (controller or AP)
    endpoint_id      UUID,                                       -- EndpointState.ref.endpoint.id
    ap_key           LowCardinality(String),                     -- WirelessAttachment.ap_name, or vendor_bss_id when ap_name is absent
    bssid            UInt64                  CODEC(T64, ZSTD(1)), -- 0 when serving_bss is vendor_bss_id
    vendor_bss_id    LowCardinality(String),
    ssid             LowCardinality(String),
    band             Enum8('unspecified'=0,'ghz2p4'=1,'ghz5'=2,'ghz6'=3,'ghz60'=4),
    channel          UInt8                   CODEC(T64, ZSTD(1)),
    rssi_millidbm    Int32                   CODEC(T64, ZSTD(1)),
    snr_millidb      Int32                   CODEC(T64, ZSTD(1)),
    negotiated_rate_bps UInt64               CODEC(T64, ZSTD(1)),
    mcs              UInt8                   CODEC(T64, ZSTD(1)),
    nss              UInt8                   CODEC(T64, ZSTD(1)),
    guard_interval_ns UInt16                 CODEC(T64, ZSTD(1)),
    in_bytes         UInt64                  CODEC(Delta, ZSTD(1)),
    out_bytes        UInt64                  CODEC(Delta, ZSTD(1)),
    in_frames        UInt64                  CODEC(Delta, ZSTD(1)),
    out_frames       UInt64                  CODEC(Delta, ZSTD(1)),
    tx_retry_frames  UInt64                  CODEC(Delta, ZSTD(1)),
    last_discontinuity DateTime              CODEC(T64, ZSTD(1)),
    ipv4             IPv4,
    hostname         LowCardinality(String),
    lifecycle        Enum8('unspecified'=0,'active'=1,'stale'=2,'retired'=3),
    source_rank      UInt8,                                      -- sink policy: lower wins when two sources report one association
    present_mask     UInt32                  CODEC(T64, ZSTD(1)),
    retention_class  Enum8('short'=1,'standard'=2,'long'=3),
    site_id          LowCardinality(String)
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY toMonday(ts)
ORDER BY (tenant_id, mac, ts, source_device_id)
TTL ts + INTERVAL 30 DAY DELETE WHERE retention_class = 'short',
    ts + INTERVAL 90 DAY DELETE WHERE retention_class IN ('standard', 'long')
SETTINGS ttl_only_drop_parts = 1, min_age_to_force_merge_seconds = 604800,
         min_age_to_force_merge_on_partition_only = 1;
```

Key by `mac` then time: a client's samples are contiguous, the counters
`Delta`-compress along one association, and "RSSI of client X over the last
day" is a key range. `source_device_id` in the key keeps controller and AP
reports of the same poll as distinct rows (they are distinct observations)
while a redelivery of either collapses. `EndpointState.observed_mac_addresses`
has at least one entry; the sink writes one row per address when more than
one is present (a rare relay case) and the wireless attachment is the same.
`ipv6_addresses` and `fingerprint` are not stored here: the fingerprint is
state (KV) and IPv6 is a list that a 30-day sample table does not need; if a
"what IP did MAC X have at T" question arrives, `client_presence` carries
`ipv4` and `ipv6_last Array(IPv6)` as `anyLast`.

Rows per day 4 to 12 M; bytes per row 20 to 30 (RF gauges with noise,
counters with Delta) = 0.1 to 0.4 GB/day, 3 to 11 GB resident at 30 days.

### 3.5 `client_presence` and `client_lookup`

```sql
CREATE TABLE flowseer.client_presence
(
    tenant_id       LowCardinality(String),
    ap_key          LowCardinality(String),
    bssid           UInt64                   CODEC(T64, ZSTD(1)),
    mac             UInt64                   CODEC(T64, ZSTD(1)),
    hour            DateTime                 CODEC(Delta, ZSTD(1)),
    ssid            SimpleAggregateFunction(anyLast, LowCardinality(String)),
    band            SimpleAggregateFunction(max, UInt8),
    endpoint_id     SimpleAggregateFunction(anyLast, UUID),
    first_seen      SimpleAggregateFunction(min, DateTime),
    last_seen       SimpleAggregateFunction(max, DateTime),
    samples         SimpleAggregateFunction(sum, UInt16),
    rssi_min        SimpleAggregateFunction(min, Int32),
    rssi_max        SimpleAggregateFunction(max, Int32),
    rssi_last       AggregateFunction(argMax, Int32, DateTime),
    snr_last        AggregateFunction(argMax, Int32, DateTime),
    rate_last       AggregateFunction(argMax, UInt64, DateTime),
    in_bytes_first  AggregateFunction(argMin, UInt64, DateTime),
    in_bytes_last   AggregateFunction(argMax, UInt64, DateTime),
    out_bytes_first AggregateFunction(argMin, UInt64, DateTime),
    out_bytes_last  AggregateFunction(argMax, UInt64, DateTime),
    retry_first     AggregateFunction(argMin, UInt64, DateTime),
    retry_last      AggregateFunction(argMax, UInt64, DateTime),
    discontinuity_max SimpleAggregateFunction(max, DateTime),
    ipv4            SimpleAggregateFunction(anyLast, IPv4),
    hostname        SimpleAggregateFunction(anyLast, LowCardinality(String)),
    source_device_id SimpleAggregateFunction(anyLast, UUID),
    retention_class SimpleAggregateFunction(max, UInt8),
    site_id         SimpleAggregateFunction(anyLast, LowCardinality(String))
)
ENGINE = ReplicatedAggregatingMergeTree
PARTITION BY toMonday(hour)
ORDER BY (tenant_id, ap_key, bssid, mac, hour)
TTL hour + INTERVAL 180 DAY DELETE WHERE retention_class = 1,
    hour + INTERVAL 1 YEAR DELETE WHERE retention_class IN (2, 3)
SETTINGS ttl_only_drop_parts = 1;

CREATE TABLE flowseer.client_lookup
(
    tenant_id   LowCardinality(String),
    mac         UInt64                   CODEC(T64, ZSTD(1)),
    hour        DateTime                 CODEC(Delta, ZSTD(1)),
    ap_key      LowCardinality(String),
    bssid       UInt64                   CODEC(T64, ZSTD(1)),
    ssid        SimpleAggregateFunction(anyLast, LowCardinality(String)),
    endpoint_id SimpleAggregateFunction(anyLast, UUID),
    first_seen  SimpleAggregateFunction(min, DateTime),
    last_seen   SimpleAggregateFunction(max, DateTime),
    rssi_last   AggregateFunction(argMax, Int32, DateTime),
    site_id     SimpleAggregateFunction(anyLast, LowCardinality(String)),
    retention_class SimpleAggregateFunction(max, UInt8)
)
ENGINE = ReplicatedAggregatingMergeTree
PARTITION BY toMonday(hour)
ORDER BY (tenant_id, mac, hour, ap_key, bssid)
TTL hour + INTERVAL 180 DAY DELETE WHERE retention_class = 1,
    hour + INTERVAL 1 YEAR DELETE WHERE retention_class IN (2, 3)
SETTINGS ttl_only_drop_parts = 1;

CREATE MATERIALIZED VIEW flowseer.client_lookup_mv TO flowseer.client_lookup AS
SELECT tenant_id, mac, hour, ap_key, bssid,
       anyLast(ssid) AS ssid, anyLast(endpoint_id) AS endpoint_id,
       min(first_seen) AS first_seen, max(last_seen) AS last_seen,
       argMaxMergeState(rssi_last) AS rssi_last,           -- see note
       anyLast(site_id) AS site_id, max(retention_class) AS retention_class
FROM flowseer.client_presence
GROUP BY tenant_id, mac, hour, ap_key, bssid;
```

The sink writes `client_presence` directly from each `EndpointState` sample
(one row per sample, `first_seen = last_seen = ts`, `argMinState`/`argMaxState`
over the single value). The MV on `client_presence` sees the inserted block
(not the merged state, F20) and forwards it; `argMaxMergeState` on an
`AggregateFunction` column inside a block is the documented way to pass a
state through a view (unverified exact combinator spelling; the alternative is
to have the sink insert into both tables itself, which is one more batch and
no MV). Insert dedup covers both because "Deduplication now works end-to-end
for asynchronous inserts and their dependent materialized views" (F44).

Why `ap_key` and not `device_id` of the AP: the attachment names the AP by
`ap_name` (string) or the BSS by `vendor_bss_id`; whether an AP is a FlowSeer
Device is an open question of dossier 02 (section 7, question 1). The sink
sets `ap_key` to the AP's device id string when the inventory resolves
`ap_name` to a device and to `ap_name` otherwise, so a later decision does
not change the key shape. `bssid` is 0 for sources that give only
`vendor_bss_id` (UniFi, `wireless_attachment.proto:25-27`), which keeps the
key total.

Sessions. A session is a maximal chain of consecutive hourly rows for the
same `(mac, ap_key, bssid)` where each row's `first_seen` is within one poll
interval plus slack of the previous row's `last_seen`. Counters at session
end are `argMax` of the last row; bytes in a session are
`last - first` per hour summed, discarding hours whose `discontinuity_max`
moved (counter rule). Roams come from `client_events` where the source
reports them and from chains that switch `(ap_key, bssid)` otherwise.

Rows: 30 k online clients x 24 hours x about 1.3 attachments per hour =
about 0.9 to 1.5 M rows/day per table after merge; inserted 4 to 12 M/day
each. Bytes per row after merge 40 to 60 (seven `argMin/argMax` states at
12 bytes each dominate); 0.05 to 0.1 GB/day per table.

### 3.6 `client_events`

```sql
CREATE TABLE flowseer.client_events
(
    tenant_id       LowCardinality(String),
    mac             UInt64                   CODEC(T64, ZSTD(1)),
    ts              DateTime                 CODEC(Delta, ZSTD(1)),
    record_id       UUID,
    endpoint_id     UUID,
    source_device_id UUID,
    kind            Enum8('lifecycle'=1,'roam'=2,'connection_failure'=3),
    -- lifecycle
    lifecycle_from  Enum8('unspecified'=0,'active'=1,'stale'=2,'retired'=3),
    lifecycle_to    Enum8('unspecified'=0,'active'=1,'stale'=2,'retired'=3),
    -- roam (from_attachment may be unset: from_* = 0 / '')
    from_ap_key     LowCardinality(String),
    from_bssid      UInt64                   CODEC(T64, ZSTD(1)),
    to_ap_key       LowCardinality(String),
    to_bssid        UInt64                   CODEC(T64, ZSTD(1)),
    to_ssid         LowCardinality(String),
    to_band         Enum8('unspecified'=0,'ghz2p4'=1,'ghz5'=2,'ghz6'=3,'ghz60'=4),
    to_channel      UInt8,
    to_rssi_millidbm Int32                   CODEC(T64, ZSTD(1)),
    roam_reason     Enum8('unspecified'=0,'standard'=1,'fast_bss_transition'=2,'okc'=3,'btm'=4,'band_steered'=5,'load_balanced'=6),
    -- connection failure
    failure_stage   Enum8('unspecified'=0,'association'=1,'authentication'=2,'dhcp'=3,'dns'=4),
    failure_detail  String                   CODEC(ZSTD(1)),
    retention_class Enum8('short'=1,'standard'=2,'long'=3),
    site_id         LowCardinality(String),
    INDEX idx_to_ap to_ap_key TYPE bloom_filter(0.01) GRANULARITY 4,
    INDEX idx_ts ts TYPE minmax GRANULARITY 4
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY toMonday(ts)
ORDER BY (tenant_id, mac, ts, record_id)
TTL ts + INTERVAL 1 YEAR DELETE WHERE retention_class = 'short',
    ts + INTERVAL 2 YEAR DELETE WHERE retention_class IN ('standard', 'long')
SETTINGS ttl_only_drop_parts = 1, min_age_to_force_merge_seconds = 604800,
         min_age_to_force_merge_on_partition_only = 1;
```

One row per `EndpointEvent` arm (`endpoint.proto:147-155`), typed columns per
arm, zero for the other arms (F9, no Nullable). `record_id` in the key makes
a redelivery collapse and two roams in one second distinct (F45). The roam
history of a MAC is a key range. "Failures on AP A in the last hour" uses the
`to_ap_key` bloom filter inside the tenant's one-hour key range (the `minmax`
on `ts` prunes granules by time first); if the filter does not skip in
`EXPLAIN`, replace it with an hourly rollup `ap_failures_hourly`
(tenant, ap_key, stage, hour, count) fed by a MV, where count inflation by
redelivery is accepted or removed with `uniqCombinedState(record_id)`.

### 3.7 Rollups

```sql
CREATE TABLE flowseer.radio_hourly
(
    tenant_id   LowCardinality(String),
    device_id   UUID,
    radio_index UInt8,
    hour        DateTime,
    samples     SimpleAggregateFunction(sum, UInt32),
    util_total_max   SimpleAggregateFunction(max, UInt16),
    util_total_sum   SimpleAggregateFunction(sum, UInt64),      -- avg = sum / samples; a duplicate adds one equal term, error <= 1/samples
    util_non_dot11_max SimpleAggregateFunction(max, UInt16),
    noise_max   SimpleAggregateFunction(max, Int32),
    noise_min   SimpleAggregateFunction(min, Int32),
    clients_max SimpleAggregateFunction(max, UInt16),
    clients_last AggregateFunction(argMax, UInt16, DateTime),
    channel_last AggregateFunction(argMax, UInt8, DateTime),
    tx_power_last AggregateFunction(argMax, Int32, DateTime)
)
ENGINE = ReplicatedAggregatingMergeTree
PARTITION BY toYYYYMM(hour)
ORDER BY (tenant_id, device_id, radio_index, hour)
TTL hour + INTERVAL 3 YEAR
SETTINGS ttl_only_drop_parts = 1;

CREATE MATERIALIZED VIEW flowseer.radio_hourly_mv TO flowseer.radio_hourly AS
SELECT tenant_id, device_id, radio_index, toStartOfHour(ts) AS hour,
       toUInt32(count()) AS samples, max(util_total_bp) AS util_total_max,
       sum(toUInt64(util_total_bp)) AS util_total_sum, max(util_non_dot11_bp) AS util_non_dot11_max,
       max(noise_floor_millidbm) AS noise_max, min(noise_floor_millidbm) AS noise_min,
       max(client_count) AS clients_max, argMaxState(client_count, ts) AS clients_last,
       argMaxState(primary_channel, ts) AS channel_last, argMaxState(tx_power_millidbm, ts) AS tx_power_last
FROM flowseer.radio_samples
GROUP BY tenant_id, device_id, radio_index, hour;

CREATE TABLE flowseer.ssid_clients_hourly
(
    tenant_id  LowCardinality(String),
    site_id    LowCardinality(String),
    ap_key     LowCardinality(String),
    ssid       LowCardinality(String),
    hour       DateTime,
    clients    AggregateFunction(uniqCombined(12), UInt64),
    samples    SimpleAggregateFunction(sum, UInt32)
)
ENGINE = ReplicatedAggregatingMergeTree
PARTITION BY toYYYYMM(hour)
ORDER BY (tenant_id, site_id, ap_key, ssid, hour)
TTL hour + INTERVAL 3 YEAR
SETTINGS ttl_only_drop_parts = 1;

CREATE MATERIALIZED VIEW flowseer.ssid_clients_hourly_mv TO flowseer.ssid_clients_hourly AS
SELECT tenant_id, site_id, ap_key, ssid, hour,
       uniqCombinedState(12)(mac) AS clients, toUInt32(count()) AS samples
FROM flowseer.client_presence
GROUP BY tenant_id, site_id, ap_key, ssid, hour;
```

`uniqCombined` is chosen because adding the same MAC twice (redelivery, two
reporters) leaves the estimate unchanged: it "uses an array" for small sets,
"a hash table" for larger ones, and HyperLogLog beyond that
(uniqCombined reference); precision 12 bounds the HLL state to 2^12 cells
when a site-level merge grows large, while per-AP states stay in the exact
array/hash regime (about 2 clients per AP per hour). Per-SSID per site per
hour is `uniqCombinedMerge(12)(clients) ... GROUP BY ssid, hour` over the
site's `ap_key` range. Rows per day: 20 k APs x 3 SSIDs x 24 = 1.4 M.
`radio_hourly` is 20 k x 3 x 24 = 1.4 M rows/day. Both MVs read from a raw
table, never from another MV (03, anti-patterns: chained MVs).

## 4. Dedup under redelivery

| Table | Mechanism | Residual |
| --- | --- | --- |
| `radio_samples`, `client_samples` | natural key unique per poll; ReplacingMergeTree collapses on merge (F45); insert-token dedup catches an in-window retry (F48) | a late duplicate before merge is a second identical row; charts with `max`/`argMax` per bucket are unaffected, `count()` is off by one |
| `radio_changes`, `bss_changes`, `wlan_changes`, `client_events` | `record_id` last in key; identical redelivery collapses | same as above; "number of channel changes" uses `uniqExact(record_id)` when exactness matters |
| `neighbor_presence`, `client_presence`, `client_lookup` | idempotent aggregates (`min`, `max`, `argMax`, `anyLast`) | `sightings`/`samples` sums inflate; documented as informational |
| `radio_hourly` | `max`, `min`, `argMax` idempotent; `sum`/`samples` inflate equally so `sum/samples` shifts by at most one equal term | none for max/last columns |
| `ssid_clients_hourly` | `uniqCombined` set insert is idempotent | none |

Sink rules that make this hold: one batch per table per flush with
`insert_deduplication_token` = JetStream sequence range (F48); partition key
derived from the record's own observation time so a redelivered row lands in
the same partition and key (01, section 3); `async_insert` explicit with
`wait_for_async_insert = 1` (pipeline direction, stores table).

## 5. Representative queries and cost models

Notation: D = devices in scope, R = radios per device (3), N = neighbors per
device (17), B = BSS per radio (3), W = window, I = poll interval, H = hours
in W, C = clients in scope, A = attachments per client per hour (about 1.3).
Granule = 8192 rows. Cost is rows read before LIMIT; granules = rows / 8192
rounded up per key range, so a per-device range costs at least one granule.

### Q1 Radio airtime for one device, newest first

```sql
SELECT ts, radio_index, util_total_bp, util_non_dot11_bp, noise_floor_millidbm, client_count
FROM flowseer.radio_samples
WHERE tenant_id = {t} AND device_id = {d} AND ts >= now() - INTERVAL 24 HOUR
ORDER BY radio_index, ts DESC LIMIT 500;
```

Rows = R x W / I = 3 x 576 = 1,728; granules 3 (one per radio range, the
radio ranges are adjacent so often 1). Reverse read-in-order on the key
prefix stops at LIMIT (F51). Independent of tenants and table size.

### Q2 Site view, last 24 h, top-N busiest radios

```sql
SELECT device_id, radio_index, max(util_total_bp) AS busiest
FROM flowseer.radio_samples
WHERE tenant_id = {t} AND device_id IN {site_devices} AND ts >= now() - INTERVAL 24 HOUR
GROUP BY device_id, radio_index ORDER BY busiest DESC LIMIT 20;
```

Rows = D x R x W / I; a 700-AP site = 700 x 3 x 576 = 1.2 M rows, about
150 granules (one key range per device). `IN` with a few thousand UUIDs is a
primary-key multi-range read. For "last 30 days" this grows 30x, so the
API routes windows above 48 h to `radio_hourly`: rows = D x R x H = 700 x 3 x
720 = 1.5 M for a month, the same order as a day of raw.

### Q3 Hourly chart over 3 months for one radio

`SELECT hour, util_total_sum / samples, argMaxMerge(channel_last) FROM radio_hourly WHERE tenant_id, device_id, radio_index AND hour BETWEEN ... GROUP BY hour`.
Rows = H = 2,160, one granule.

### Q4 When did the channel change

```sql
SELECT ts, radio_index, new_channel, new_width_mhz, old_value
FROM flowseer.radio_changes
WHERE tenant_id = {t} AND device_id = {d} AND radio_index = {r} AND attribute = 'primary_channel'
ORDER BY ts DESC LIMIT 100;
```

Rows = number of change rows of that radio in retention (tens to hundreds);
one granule. "Changes across a site in the last hour" = `device_id IN
{site} AND ts >= ...` with the `minmax` on `ts` pruning untouched granules:
rows = D x changes per device in the hour, at most one granule per device.

### Q5 What was the radio at T

`SELECT * FROM radio_samples WHERE tenant_id, device_id, radio_index AND ts <= T ORDER BY ts DESC LIMIT 1`: one granule. The sample row carries channel, width, power, status, so no join with changes.

### Q6 Neighbors of one AP, last 7 days, with first/last seen and RSSI trend

```sql
SELECT bssid, anyLast(ssid), min(first_seen), max(last_seen), min(rssi_min), max(rssi_max),
       argMaxMerge(rssi_last), max(classification)
FROM flowseer.neighbor_presence
WHERE tenant_id = {t} AND device_id = {d} AND hour >= now() - INTERVAL 7 DAY
GROUP BY bssid ORDER BY max(last_seen) DESC;
```

Rows = N x H = 17 x 168 = 2,856 (up to 70 x 168 = 11,760); one to two
granules. Unmerged parts add duplicates that `GROUP BY` folds, so no FINAL.
"Which neighbors appeared this week" = `HAVING min(first_seen) >= now() - 7
DAY`; "disappeared" = `HAVING max(last_seen) < now() - 2 x I`. Same cost.

### Q7 Rogue sweep: which APs in a site hear BSSID X

```sql
SELECT device_id, max(last_seen), max(rssi_max)
FROM flowseer.neighbor_presence
WHERE tenant_id = {t} AND device_id IN {site_devices} AND bssid = {x}
  AND hour >= now() - INTERVAL 24 HOUR
GROUP BY device_id;
```

Primary-key multi-range: for each device the `(device_id, bssid)` prefix
narrows to that neighbor's rows: rows = D x 24, granules about D. Cost
follows the site, not the tenant. Tenant-wide (no device list) falls back to
the bloom filter and reads every index block of the tenant's 24 h: rows
bounded by tenant volume, not by the answer; the plan should expose it as a
site-scoped operation or accept that bound.

### Q8 Where was MAC X in the last 7 days (cross-device)

```sql
SELECT hour, ap_key, bssid, anyLast(ssid), min(first_seen), max(last_seen), argMaxMerge(rssi_last)
FROM flowseer.client_lookup
WHERE tenant_id = {t} AND mac = {x} AND hour >= now() - INTERVAL 7 DAY
GROUP BY hour, ap_key, bssid ORDER BY hour DESC;
```

Rows = H x A = 168 x 1.3 = 220; one granule. Independent of everything but
the MAC's own activity. By endpoint: resolve `observed_mac_addresses` from
the read model, then `mac IN (...)`: rows x number of MACs.

### Q9 Clients on AP A now and in the last 24 h, with session chains

```sql
SELECT mac, bssid, anyLast(ssid), min(first_seen), max(last_seen),
       argMaxMerge(rssi_last), argMaxMerge(in_bytes_last) - argMinMerge(in_bytes_first) AS in_bytes_delta
FROM flowseer.client_presence
WHERE tenant_id = {t} AND ap_key = {a} AND hour >= now() - INTERVAL 24 HOUR
GROUP BY mac, bssid;
```

Rows = clients of the AP in 24 h x 24 x A; 50 clients x 24 x 1.3 = 1,560;
one granule. Site-level 24 h: D x that = 700 x 1,560 = 1.1 M rows, about 700
granules (one key range per `ap_key`). Session chaining runs in the API over
the returned hourly rows, or in SQL with `groupArray` ordered by hour per
`(mac, bssid)` and `arrayDifference` on `first_seen` to cut at gaps.

### Q10 Clients per SSID per hour for a site over 30 days

```sql
SELECT ssid, hour, uniqCombinedMerge(12)(clients)
FROM flowseer.ssid_clients_hourly
WHERE tenant_id = {t} AND site_id = {s} AND hour >= now() - INTERVAL 30 DAY
GROUP BY ssid, hour ORDER BY hour;
```

Rows = D x SSIDs x H = 700 x 3 x 720 = 1.5 M for the largest site, about 190
granules; the per-AP partial states are the exact array regime so the site
merge is exact until a site exceeds about 4,096 distinct clients per SSID per
hour (then HLL with its small error). Without the rollup this would be
`uniq(mac)` over `client_presence`: 700 x 1,560 x 30 = 33 M rows, which is
why the rollup exists. `site_id` is the write-time site (F69); an AP that
moved site keeps its old hours under the old site, which is what a historic
chart should show.

### Q11 Roam history of a client

`SELECT ts, from_ap_key, to_ap_key, to_bssid, roam_reason FROM client_events WHERE tenant_id, mac AND kind = 'roam' AND ts >= ... ORDER BY ts DESC LIMIT 200`: rows = events of that MAC in the window (tens); one granule.

### Q12 RSSI trace of one client, last 24 h

`FROM client_samples WHERE tenant_id, mac AND ts >= ... ORDER BY ts DESC`: rows = W / I x reporters = 288 x 1 or 2; one granule.

Queries whose cost would grow with total data, and the bound applied:

| Query | Unbounded form | Bound |
| --- | --- | --- |
| MAC lookup | bloom filter over the AP-keyed table | `client_lookup` keyed by MAC (Q8) |
| Clients per SSID over months | `uniq` over raw presence | `ssid_clients_hourly` (Q10) |
| Tenant-wide BSSID sweep | bloom filter over tenant range | site scope required, or accept tenant-volume bound (Q7) |
| Charts over weeks on raw samples | raw scan | API switches to `radio_hourly` above 48 h (Q2, Q3) |
| "All channel changes in the last hour" tenant-wide | scan of all devices' change ranges | `minmax` on `ts` prunes to granules with recent rows; a tenant with 10 k devices reads at most 10 k granules; acceptable, or add a `changes_hourly` count rollup if the UI needs it on every page load |

Pinned query limits (`max_rows_to_read`, `max_execution_time`, F58) on the
read-only user turn any remaining overrun into an error rather than a stall.

## 6. Failure modes and how the design avoids them

| Failure | Where it would come from | Avoided by |
| --- | --- | --- |
| Neighbor rows at 45 % of volume (baseline) | one row per sighting per scan | hourly presence, 24 rows per neighbor per day; raw sightings optional at 7 days |
| Channel history needing window functions over samples | storing changes nowhere | `radio_changes` from the sink's last-value cache |
| Sink restart emitting spurious changes | empty cache | cache seeded from KV; `old_known = 0` marks the first observation |
| Late redelivery emitting a "change back" | comparing against a newer cache | records older than the cache's `observed_at` never update the cache |
| Double counting controller plus AP reports | keying on reporting device | key on serving attachment; reporter is an attribute; `source_rank` picks one for gauges |
| Ruckus deltas stored as cumulative | adapter passes deltas through | contract is cumulative (`EndpointCounters`); accumulate at the edge; `last_discontinuity` set per association |
| Counter rollover or re-association inside an hour | `last - first` across a reset | `discontinuity_max` per hour; a changed discontinuity invalidates the hour's delta |
| Randomised MAC splitting one client | MAC as identity | `endpoint_id` attribute; lookup by endpoint resolves to its MAC list |
| Projections falling out of sync on an aggregating table | projection for the MAC lookup | second table fed by MV; `deduplicate_merge_projection_mode = throw` never fires |
| Too many parts | one insert per record or per device | one batch per table per flush; weekly partitions; 26.x async-insert batching (F15, F16) |
| Analytics stalling ingest (baseline) | shared CPU | read-only user with pinned limits and workload class (F56) |
| Site reassignment rewriting history | join to current site | write-time `site_id`; dictionary for "as it was" questions (F69) |
| Hidden SSID / empty SSID | treating empty as missing | `ssid` is a byte string; empty is a value (`neighbor_bss.proto:23-25`); `present_mask` says whether the field was set |

## 7. Retention classes (proposal)

| Table | short | standard | long | Partition |
| --- | --- | --- | --- | --- |
| `radio_samples` | 90 d | 1 y | 2 y | week |
| `radio_changes`, `bss_changes`, `wlan_changes`, `client_events` | 1 y | 2 y | 2 y | week |
| `neighbor_presence` | 180 d | 1 y | 1 y | week |
| `neighbor_sightings` (optional) | 7 d | 7 d | 7 d | day |
| `client_samples` | 30 d | 90 d | 90 d | week |
| `client_presence`, `client_lookup` | 180 d | 1 y | 1 y | week |
| `radio_hourly`, `ssid_clients_hourly` | 3 y | 3 y | 3 y | month |

Totals at 20 k APs, standard class, steady state: `radio_samples` 150 to
250 GB; `neighbor_presence` about 100 GB; `client_samples` 3 to 11 GB;
`client_presence` + `client_lookup` 40 to 70 GB; rollups under 30 GB.
Measured bytes per row from the benchmark replace these ranges.

## 8. Benchmark design

Target: single-node ClickHouse 26.8 container, tables as in section 3 with
`Replicated` engines replaced by their plain counterparts (one node), same
keys, codecs, TTL, and settings. Data goes through the same Go insert path
the sink will use (clickhouse-go v2, native batches, `async_insert = 1`,
`wait_for_async_insert = 1`, `insert_deduplication_token` per batch).

### 8.1 Generator

Seeded PRNG (seed in every output file name). Parameters and defaults:

| Parameter | Default | Note |
| --- | --- | --- |
| tenants | 200: 190 x 10 devices, 8 x 500, 2 x 10,000 | the two large tenants carry 20 k of the 25.9 k devices |
| sites per tenant | devices / 120, largest site 700 devices | baseline site sizes |
| radios per device | 2 or 3 (70 % three) | |
| BSS per radio | 1 to 7, median 3 | SSID names from a per-tenant pool of 8 |
| radio poll interval | 150 s, jitter +-10 s | |
| channel utilization | AR(1) around a per-radio mean (2.4 GHz mean 3,000 bp, 5 GHz 1,200 bp) with a diurnal term, clipped 0..10000 | gauge with noise |
| noise floor | per-radio mean -95,000 mdBm, sd 1,500 | |
| channel change rate | 0.3 per radio per day, bursts: 2 % of days a site-wide DFS event moves 30 % of 5 GHz radios in 5 minutes | drives `radio_changes` |
| tx power change rate | 1 per radio per day | |
| radio flap rate | 3 % of devices per day go down for 5 to 90 minutes | `oper_status` changes |
| neighbors per device | Poisson mean 17, max 70, drawn from the site's own BSSIDs plus 20 % foreign | set membership |
| neighbor churn | 2 % of members replaced per day; RSSI per sighting = per-pair mean (uniform -90..-40 dBm) + N(0, 3 dB) | |
| neighbor scan interval | 600 s | |
| clients | per device Poisson mean 2 online; 50 k distinct per day across the large tenants; 40 % of MACs locally administered | |
| client session length | log-normal median 45 min; roam probability per hour 0.3 within site; 10 % of sessions end by lifecycle stale | drives presence chains and events |
| client poll interval | 300 s | |
| client counters | cumulative, rate per client log-normal median 50 kB/s, reset at each association | |
| client RSSI | per-session mean -65 dBm, sd 4 dB per sample | |
| connection failures | 0.05 per client per day, stage weights assoc 20 %, auth 50 %, dhcp 25 %, dns 5 % | |
| dual reporting | 30 % of devices report clients from both controller and AP (two `source_device_id`) | duplicates by construction |
| redelivery | 1 % of batches re-sent after 15 minutes (outside the NATS window) | dedup behaviour |
| days | 7 (1x), 28 (4x), 112 (16x) | scale steps |

Output: one Parquet or native file per table per day so inserts replay in
order with fixed batch sizes (radio 50 k rows, neighbors 100 k, clients 50 k).

### 8.2 Scale steps and expected results

| Step | Data | Query scope | Expectation |
| --- | --- | --- | --- |
| S1 fixed scope, growing data | 1x, 4x, 16x days | Q1 to Q12 at fixed scope and a fixed 24 h / 7 d window inside the last day | `read_rows` flat within 2x across the three steps; `read_bytes` flat; p95 duration flat within 2x |
| S2 fixed data, growing scope | 28 days | site of 10, 120, 700 devices for Q2, Q7, Q9, Q10 | `read_rows` linear in D within 2x of the model; granules about D x R (Q2) and D (Q7, Q9) |
| S3 tenant isolation | 28 days | the same queries on a 10-device tenant and a 10,000-device tenant | per-device cost equal within 2x; a small tenant's queries do not slow when the big tenant's data grows (S1 on the small tenant) |
| S4 window growth | 28 days | Q2 at 24 h, 48 h on raw; 7 d, 28 d on `radio_hourly` | raw cost linear in W; hourly cost = D x R x H |
| S5 redelivery | 7 days with and without the 1 % re-sends | row counts per table, `count()` vs `uniqExact(record_id)`, presence values unchanged | presence tables identical in all non-sum columns; sample tables have at most 1 % extra rows before forced merge and 0 after `OPTIMIZE ... FINAL` on closed partitions (test-only) |

### 8.3 Metrics

- Inserts: rows/s per table and parts created per insert from
  `system.part_log` (`NewPart` events, F67); `system.parts` active part
  count per partition after 1 hour of replay.
- Storage: `sum(data_compressed_bytes) / sum(rows)` per table and per column
  from `system.parts` and `system.columns` after `OPTIMIZE TABLE ... FINAL`
  on closed partitions and again without it (both numbers reported).
- Queries: from `system.query_log` the fields `read_rows` ("Total number of
  rows read from all tables and table functions participated in query"),
  `read_bytes`, `memory_usage`, `query_duration_ms`; 20 runs per query per
  step with `use_query_cache = 0`, report p50 and p95; `EXPLAIN indexes = 1`
  once per query per step with `use_query_condition_cache = 0,
  use_skip_indexes_on_data_read = 0` (the EXPLAIN page says the output is
  only meaningful with those settings from 25.9), recording `Parts` and
  `Granules` after each index.
- Skip indexes: for Q7 (tenant-wide form) and the `client_events` bloom
  filter, granules before/after the index; drop an index whose ratio is
  above 0.5.

### 8.4 Pass criteria

| Query | Model (rows) | Pass |
| --- | --- | --- |
| Q1 | R x W / I | read_rows <= 2x model; granules <= R + 1; flat across S1 |
| Q2 raw | D x R x W / I | <= 2x model; linear in D (S2) |
| Q2/Q3 hourly | D x R x H | <= 2x model |
| Q4 | changes of the radio | <= 1 granule per radio |
| Q6 | N x H | <= 2x model |
| Q7 site-scoped | D x 24 | granules <= 2 x D |
| Q8 | H x A | <= 2 granules; flat across S1 and S3 |
| Q9 | C_ap x 24 x A | <= 2x model |
| Q10 | D x S x H | <= 2x model; result equals `uniqExact` over presence within 2 % |
| Q11, Q12 | events / samples of one MAC | <= 2 granules |
| Inserts | | parts per insert <= partitions touched; active parts per partition < 300 after replay |
| Storage | | `radio_samples` <= 24 B/row, `neighbor_presence` <= 48 B/row merged, `client_presence` <= 72 B/row merged; outside these the codec choice is revisited |

## 9. Sources fetched

- ClickHouse codec reference `https://clickhouse.com/docs/reference/statements/create/table/codec`: T64 is a "Compression approach that crops unused high bits of values in integer data types (including `Enum`, `Date` and `DateTime`)"; Delta "raw values are replaced by the difference of two neighboring values"; Gorilla "Calculates XOR between current and previous floating point value".
- ClickHouse compression guide `https://clickhouse.com/docs/data-compression/compression-in-clickhouse`: "`T64` can be effective on sparse data or when the range in a block is small. Avoid `T64` for random numbers."; "`Delta`-based codecs work well whenever you have monotonic sequences or small deltas in consecutive values."; "`Gorilla` for gauge data" (floating point).
- ClickHouse skipping indexes `https://clickhouse.com/docs/optimize/skipping-indexes`: bloom_filter "takes a single optional parameter of the allowed "false positive" rate between 0 and 1" (default .025); "if a value occurs even once in an indexed block, it means the entire block must be read into memory and evaluated"; "Each indexed block consists of GRANULARITY granules"; skip indexes "incurs a meaningful cost both on data ingest and on queries".
- ClickHouse MergeTree reference `https://clickhouse.com/docs/engines/table-engines/mergetree-family/mergetree`: bloom_filter supports `(U)Int*`, `UUID`, `LowCardinality`, functions `equals`, `in`, `has`, `hasAny`; `index_granularity` default 8192.
- ClickHouse projection reference `https://clickhouse.com/docs/sql-reference/statements/alter/projection`: "Projections will create internally a new hidden table, this means that more IO and space on disk will be required"; `deduplicate_merge_projection_mode` (24.8) default `throw`: "An exception is thrown, preventing projection parts from going out of sync" when a merge folds rows (ReplacingMergeTree, AggregatingMergeTree).
- ClickHouse projections guide `https://clickhouse.com/docs/data-modeling/projections`: "Projections don't allow using different TTL for the source table and the (hidden) target table"; "Lightweight updates and deletes aren't supported for tables with projections"; "materialized views are more effective for maintaining aggregates".
- `uniqCombined` reference `https://clickhouse.com/docs/sql-reference/aggregate-functions/reference/uniqcombined`: "For a small number of distinct elements, an array is used", "When the set size is larger, a hash table is used", "For a larger number of elements, HyperLogLog is used, which will occupy a fixed amount of memory"; default precision 17 "is effectively 96 KiB".
- `EXPLAIN` reference `https://clickhouse.com/docs/sql-reference/statements/explain`: `indexes` "Shows used indexes, the number of filtered parts and the number of filtered granules for every index applied"; from 25.9 meaningful only with `use_query_condition_cache = 0, use_skip_indexes_on_data_read = 0`.
- `system.query_log` `https://clickhouse.com/docs/operations/system-tables/query_log`: `read_rows` "Total number of rows read from all tables and table functions participated in query"; `memory_usage` "Memory consumption by the query".
- Apple `https://support.apple.com/en-us/102509`: "By default, your device improves privacy by using a different MAC address for each Wi-Fi network"; Rotating "rotates to a different private address every 2 weeks" (iOS 18 and siblings); Fixed is the default on WPA2 or stronger.
- Android `https://source.android.com/docs/core/connect/wifi-mac-randomization-behavior`: persistent randomisation "based on the network profile's parameters", kept "until a factory reset"; non-persistent re-randomises at connection start when the DHCP lease expired and more than 4 hours passed since disconnect, or the MAC is more than 24 hours old (Android 12+, opt-in cases).
- OpenConfig `openconfig-wifi-phy.yang` (raw.githubusercontent.com/openconfig/public/master): total-channel-utilization "should include all time periods the AP spent actively receiving and transmitting 802.11 frames" and "clear channel assessment (CCA) in a busy state"; obss-rx "Received channel utilization due to 802.11 frames NOT destined to a BSS on this AP"; `channel-change-reason` "When an Access Point changes channels, this will provide the reason that led to the change".
- OpenConfig `openconfig-wifi-types.yang`: `CHANGE_REASON_TYPE` identities `DFS` ("DFS hit occurred"), `NOISE`, `ERRORS`, `BETTER_CHANNEL`.

## 10. Unverified

1. `argMaxMergeState` as the combinator to forward an `AggregateFunction(argMax)` column through a materialized view into a second AggregatingMergeTree (3.5). If wrong, the sink inserts into `client_lookup` directly.
2. Bytes-per-row figures in sections 3 and 7 are estimates for the benchmark to replace.
3. Whether a `bloom_filter` on `bssid` in `neighbor_presence` and on `to_ap_key` in `client_events` skips enough to keep (F19 says measure).
4. The 26.8 `Nested` column behaviour under `ReplacingMergeTree` dedup and with `T64` on the inner `UInt64` array (codec applies to array elements; unverified on this version).
5. `RadioFacet` as the carrier of the radio sample and a not-yet-defined neighbor scan record: the ingest payload arms do not exist yet (`ingest_record.proto:25-30`).
6. OpenConfig channel-change reasons are not in `RadioFacet`; `radio_changes` has no reason column until the schema carries one.
7. A per-sighting raw neighbor table is worth its 0.4 GB/day: depends on the UI, not on evidence here.

## 11. Schema gaps (full pass)

Method: the FlowSeer messages of section 1 were re-read against what the
vendored controller and AP schemas report: Ruckus SmartZone GPB
(`spec/proto/ruckus/ap/ap_status.proto`, `ap_report.proto`, `ap_client.proto`,
`ap_rogue.proto`, `ap_mesh.proto`), Ruckus SNMP (`spec/mib/ruckus/wireless/RUCKUS-SZ-WLAN-MIB`,
`RUCKUS-ZD-WLAN-MIB`), Aruba SNMP (`spec/mib/aruba/wireless/WLSX-WLAN-MIB`,
`WLSX-MON-MIB`, `WLSX-TRAP-MIB`), the schema-building-blocks dossiers 01, 02,
03, 09, and the device-inventory target dossiers (ruckus, hpe-aruba,
cisco-meraki, ubiquiti-unifi, juniper-mist-junos; their telemetry sections
describe transports, not fields, so they add no column here). Line numbers
below are from the worktree copies. A gap is listed when at least two
sources report the fact, or one source reports it and a query shape in
section 5 needs it. Deliberately not proposed: per-rate and per-packet-size
histograms (Aruba `wlsxWlanAPRateStatsTable`, `wlsxWlanAPPktSizeStatsTable`,
Ruckus `PowerFlexHistogram`), Ruckus internal queue and lookup timings
(`fst_lookup_time`, `sessmgr_lookup_time`), and tunnel, IPsec, cable modem
and cellular blocks of `APStatusData`, which belong to other domains.

| # | Gap | Evidence | Proposed addition | Table design change |
| --- | --- | --- | --- | --- |
| G1 | `NeighborBss` has no carrier, and a scan has no window or per-sighting time | No message embeds `NeighborBss` (section 0). Ruckus reports a scan as `RogueAPStats` with `sampleTime` (`ap_rogue.proto:352`), `aggregationInterval` (359), `totScanTime` (387) and a list of `ReportType` sightings each with its own `timeStamp` (83); Aruba `wlsxMonAPInfoEntry` carries `monAPInfoMonitorTime` (`WLSX-MON-MIB:2544`) and `monAPInfoInactivityTime` (2555) | `NeighborScan` record in the ingest envelope: `radio_name`, `window Duration`, `repeated NeighborBss sightings`; on `NeighborBss` add `seen_at Timestamp` (device-reported) and `last_seen_at`. Dossier 02 section 4 proposed `first_seen_at`/`last_seen_at` on the sighting row | `neighbor_presence.first_seen`/`last_seen` take the device-reported times (min/max stay idempotent). The scan `window` lets the sink skip an inserted row for a sighting the device already reported inside the same hour, cutting inserted rows from 49 M to about 8 M per day with no change to the merged result |
| G2 | Neighbor sighting lifecycle is an event at the source, inferred from gaps in FlowSeer | Ruckus `ReportType` enum `DISCOVERY`/`UPDATE`/`DISAPPEAR` (`ap_rogue.proto:23-39`), `prevReportChannel` (125), `prevReportType` (132); Aruba traps `wlsxNRogueAPDetected`, `wlsxNRogueAPResolved`, `wlsxNInterferingAPDetected` (`WLSX-TRAP-MIB`); dossier 02 section 5 recommends modelling discovery and disappearance as events | `NeighborEvent` arm on the radio-owning entity's event: `appeared`, `disappeared`, `reclassified {from, to}`, `channel_moved {from, to}` | New `neighbor_events` table shaped like `radio_changes` with `bssid` in the key `(tenant_id, device_id, bssid, ts, record_id)`. "When did neighbor X appear" becomes a key range instead of a gap scan over presence rows (Q6 keeps working as the fallback for sources without events) |
| G3 | Neighbor sighting lacks the rogue's wired-side MAC, client count, beacon interval, allow-list and containment, classification confidence | Ruckus `rogueAPMac` (`ap_rogue.proto:104`, the MAC seen on the wire), `sta_count` (176), `beacon_intval` (164), `rx_packet_count` (170), `allowListed` (182), `rfband` (224); Aruba `monAPInfoConfidence` (`WLSX-MON-MIB:2597`), `monAPInfoMatchType` (2608), `monAPInfoMatchMethod` (2618), `monAPInfoStatus` (2587), `monAPInfoIBSS` (2671), `monAPNumClients` (174); Aruba containment traps `wlsxClientDeauthContainment`, `wlsxClientWiredContainment`; Meraki Air Marshal `contained` (dossier 02 section 3) | On `NeighborBss`: `wired_mac MacAddress`, `station_count uint32`, `beacon_interval Duration`, `containment` enum (`none`, `deauth`, `wired`, `tagged_wired`), `classification_confidence_basis_points`, `ibss bool` | `neighbor_presence` gains `wired_mac anyLast`, `station_count max`, `containment max`, `confidence_last argMax`. A second bloom filter on `wired_mac` serves "is this rogue on our wire" against the FDB tables of the switching domain |
| G4 | Radio has no counters | `RadioFacet` has none and the package README names "Radio and BSS hardware counters" as deliberately absent. Ruckus `APStatusRadio` `rxBytes`/`rxFrames`/`txBytes`/`txFrames` (`ap_status.proto:999-1034`), `retry` (1055), `drop` (1062), `phyError` (978), `rxMulticast`/`txMulticast` (1069-1076), `RxErrorPkts`/`TxErrorPkts` (1354-1361); Aruba `wlanAPRadioRxBytes` (`WLSX-WLAN-MIB:4127`), `wlanAPRadioTxDroppedPkts` (4160); Ruckus ZD `ruckusZDWLANAPRadioStatsRxBytes`, `TxRetries`, `TxFail` (`RUCKUS-ZD-WLAN-MIB`); Aruba channel stats `wlanAPChFCSErrorCount`, `wlanAPChRetryCount` (`WLSX-WLAN-MIB`, 3420-3905) | `RadioCounters` message per the counter rule: `rx_bytes`, `tx_bytes`, `rx_frames`, `tx_frames`, `tx_retry_frames`, `tx_dropped_frames`, `rx_error_frames`, `tx_error_frames`, `phy_error_frames`, `rx_multicast_frames`, `tx_multicast_frames`, `last_discontinuity` | `radio_samples` gains 11 `UInt64 CODEC(Delta, ZSTD(1))` columns and a `discontinuity_epoch` as in 12.1; `radio_hourly` gains `deltaSumTimestampState` columns for bytes, retries and errors. Bytes per row rise by an estimated 8 to 16. Rate queries follow F30 |
| G5 | Radio quality statistics are reported and have no field | Ruckus `latency` (`ap_status.proto:1132`, "time taken by a packet from ethernet ingress to Radio egress"), `capacity` (1139, "saturated throughput estimate"), `Rssi` (1617, average client RSSI; ZD `ruckusZDWLANAPRadioStatsAvgStaRSSI`), `NumMaxClients` (1431), `TxPktRetryRate` (1382), `ResourceUtil` (1536), `RxDesense` (1633), `chainmask` (1258); `APReportBinRadio` `medianTxRadioMCSRate`/`medianRxRadioMCSRate` (`ap_report.proto:954-961`), `TxPER` (982), `txRetryRate` (989); Aruba `wlanAPChInterferenceIndex` (`WLSX-WLAN-MIB:3590`), `wlanAPChCoverageIndex` (3579), `wlanAPChBusyRate` (3671), `wlanAPChFrameRetryRate`, `wlanAPRadioUtilization` (845) | `RadioQuality` windowed-statistics message beside `ChannelUtilization`: `client_rssi_avg_millidbm`, `tx_retry_rate_basis_points`, `tx_per_basis_points`, `median_tx_mcs`, `median_rx_mcs`, `capacity_bps`, `latency Duration`, `window Duration`; `max_clients uint32` on `RadioFacet` | `radio_samples` gains 7 narrow gauges (`T64, ZSTD`); `radio_hourly` gains their `max`. Q2 can rank by retry rate or PER, which the baseline's operators asked for and the current facet cannot answer |
| G6 | Channel change count, reason and DFS radar are not carried | Ruckus `numOfChannelChange` (`ap_status.proto:1174`, "number of channel change on radio"); OpenConfig `channel-change-reason` with `CHANGE_REASON_TYPE` identities `DFS`, `NOISE`, `ERRORS`, `BETTER_CHANNEL` and `dfs-hit-time` (section 9); Aruba traps `wlsxNChannelChanged`, `wlsxAPChannelChange`, `wlsxChannelInterferenceDetected`, `wlsxNRadioAttributesChanged` (`WLSX-TRAP-MIB`); dossier 02 section 4 proposed `DfsRadarEvent {channel_number, detected_at}` | A radio event arm on the radio-owning entity (no component event exists today): `channel_changed {from_channel, to_channel, from_width, to_width, reason ChannelChangeReason}`, `radar_detected {channel, detected_at}`; `channel_change_count uint64` in `RadioCounters` | `radio_changes` gains `reason Enum8('unspecified'=0,'dfs'=1,'noise'=2,'errors'=3,'better_channel'=4,'manual'=5)` and a `source Enum8('event'=1,'derived'=2)`. Rule: when a source emits channel events, the sink stops deriving that attribute from samples so one change yields one row. `channel_change_count` in samples lets the benchmark and the API cross-check derived rows |
| G7 | 6 GHz power mode and AFC state are absent | Ruckus `Radio6gInfo` (`ap_status.proto:3524-3560`): `power_mode`, `afc_status`, `available_channels`, `max_power_dbm`, `min_power_dbm`, `AFCRespCfiEirp {opclass, cfi, eirp}` (3518-3522) and a `GeoLocation` with uncertainty (3497-3513). Only one vendor in the corpus, but 6 GHz regulation makes it a standing fact a 6 GHz radio reports | On `RadioFacet`: `power_mode Dot11PowerMode` (`lpi`, `sp`, `vlp`), `afc_status` enum, `afc_max_eirp_millidbm` | `radio_samples` and `radio_changes` gain two enum attributes; negligible volume |
| G8 | BSS has counters at every source and only a client count in FlowSeer | Ruckus `APStatusWlan` `rxBytes`/`txBytes`/`rxFrames`/`txFrames` (`ap_status.proto:328-349`), `txDropDataFrames` (398), `TxRetryPkts` (667), `RxErrorPkts`/`TxErrorPkts` (674-681); `APReportBinWlan` `rxBytes_r`/`txBytes_r` (`ap_report.proto:189-196`); Ruckus ZD `ruckusZDWLANVapWlanRxBytes`/`TxBytes`/`TxDropPkt` (`RUCKUS-ZD-WLAN-MIB`); Aruba `wlsxWlanAPESSIDStatsTable` (`WLSX-WLAN-MIB:4003`) | `BssCounters` message on `Bss`: `rx_bytes`, `tx_bytes`, `rx_frames`, `tx_frames`, `tx_dropped_frames`, `tx_retry_frames`, `rx_error_frames`, `tx_error_frames`, `last_discontinuity` | This changes section 2's BSS decision. Counters do not fit `Nested` arrays well (Delta coding needs one series per column). Add `bss_samples` `ORDER BY (tenant_id, device_id, radio_index, bssid, ts)` with the counters, `retention_class`-bound TTL of 14 to 30 days, and a `bss_hourly` rollup with `deltaSumTimestampState`. Rows per day 104 M at the radio cadence; the sink may thin BSS counter rows to every third poll (10 min) since byte deltas over 10 minutes lose nothing a chart shows. The client-count arrays stay on `radio_samples` for the per-poll view |
| G9 | Association, authentication and disconnect statistics per BSS and per radio have no home | Ruckus `APStatusRadio` `connectionAuthFailureCount`/`connectionAssocFailureCount`/`connectionTotalCount` (`ap_status.proto:1153-1167`), `NumAuthReqs` to `NumAssocDeny` (1438-1515); `APStatusWlan` `NumAssocFail` (737), `NumAuthFail` (814), `roaming_success` (849), `ftassoc_success`/`ftassoc_failure` (510-517); `APReportBinWlan` `authFailureCount`, `eapFailureCount`, `radiusFailureCount`, `dhcpFailureCount` (`ap_report.proto:413-469`), `staSmartRoamDisconCnt`/`staIdleDisconCnt`/`staLeaveDisconCnt`/`staAPKickDisconCnt` (490-525), `btm_requests` (628), `sta_kickouts` (635), `roamingFailureCount` (572); Aruba `wlanApRadioAssocReqCount` and `wlanApRadioAssocReqSuccCount` (`WLSX-WLAN-MIB:4204` ff.); Ruckus ZD `ruckusZDWLANAPRadioStatsAssocFail`, `DiassocLeave`, `DiassocCapacity` | `ConnectionCounters` message reused on `Bss` and `RadioFacet`: `auth_attempts`, `auth_failures`, `assoc_attempts`, `assoc_failures`, `assoc_denied`, `eap_failures`, `radius_failures`, `dhcp_failures`, `roam_successes`, `roam_failures`, `btm_requests`, `disconnects_idle`, `disconnects_leave`, `disconnects_kicked`, `disconnects_steered`, `last_discontinuity` | Counters land in `radio_samples` and `bss_samples`; `radio_hourly`/`bss_hourly` carry `deltaSumTimestampState` per counter. "Failures on AP A in the last hour" (section 3.6) is then a sample delta and no longer depends on the `client_events` bloom filter, which the design drops once these counters exist |
| G10 | Controller-aggregated WLAN counters and client counts | `RUCKUS-SZ-WLAN-MIB` `ruckusSZWLANNumSta`, `ruckusSZWLANRxBytes`, `ruckusSZWLANTxBytes`; `RUCKUS-ZD-WLAN-MIB` `ruckusZDWLANNumSta`, `RxBytes`, `TxBytes`, `AuthFail`, `AssocFail`; `WlanState` (`wlan.proto:108-143`) has broadcasts only | `WlanSample`-style record: `WlanGlobalRef`, `associated_client_count`, `BssCounters`, `ConnectionCounters` as reported by the controller | New small table `wlan_samples` `ORDER BY (tenant_id, wlan_id, source_device_id, ts)`: tens of WLANs per tenant at the controller cadence. It gives the per-SSID chart without touching per-AP rows when the controller reports it; `ssid_clients_hourly` stays for sources that do not |
| G11 | Client association has no start time, session id, username, VLAN or radio index | Ruckus `APReportBinClient` `sessionId` (`ap_report.proto:1583`), `firstConnection` (1597, "Date and time of initial connection"), `firstAuth` (1604), `ipAssignTime` (1611), `sessionTime` (1625), `username` (1408), `clientVlan` (1422), `radioId` (1632); `ap_client.proto` `APClientRadio.radioId` (574), `APClientInfo.vlan` (79); Cisco `ms-assoc-time`, `ms-ap-slot-id`, `username` (dossier 03 section 2); Aruba `wlanStaUpTime` (`WLSX-WLAN-MIB:1522`), `wlanStaVlanId`, `wlanStaAssociationID`; UniFi `connectedAt`, Meraki `firstSeen` (dossier 03 section 2) | On `WirelessAttachment`: `associated_at Timestamp`, `radio_index uint32`, `vlan_id`, `session_id string` (vendor id, unique within the integration like `vendor_bss_id`); on `EndpointState`: `username string` (802.1X or web-auth identity, distinct from `hostname` per dossier 03 traps) | `client_presence` and `client_lookup` gain `assoc_start SimpleAggregateFunction(min, DateTime)` and `session_id anyLast`, `radio_index anyLast`, `vlan_id anyLast`, `username anyLast`. A session is then exact: rows sharing `(mac, ap_key, bssid, assoc_start)` are one session, and the gap heuristic of section 3.5 applies only to sources without `associated_at`. `radio_index` joins a client to its radio's `radio_samples` row for "was the channel busy when this client's RSSI dropped" |
| G12 | Disassociation is an event at every source and FlowSeer has no arm for it | Ruckus `APReportBinClient` `disconnectTime` (`ap_report.proto:1618`), `disconnectReason` (1660, "Reason for disconnect from the controller"), `sessDeauthData {sessStartTime, disconnectTime, sessTxBytes, sessRxBytes}` (1307-1328); Aruba traps `wlsxNUserEntryDeAuthenticated`, `wlsxNUserEntryDeleted`, `wlsxClientRejectedByMaxClientCount`; Ruckus ZD disassociation counters by cause (`DiassocLeave`, `DiassocCapacity`, `DiassocAbnormal`); Aruba `wlanStaTxDeauthentications` (`WLSX-WLAN-MIB:4648`) | `EndpointEvent` arm `disassociation {attachment WirelessAttachment, reason DisassociationReason (idle, left, kicked, capacity, steered, radio_down, abnormal), reason_code uint32 (802.11 reason code pass-through), counters EndpointCounters at end, duration Duration}` | `client_events` gains `kind = 'disassociation'`, `disassoc_reason Enum8`, `reason_code UInt16`, `session_in_bytes`/`session_out_bytes UInt64`, `session_duration_s UInt32`. Session rows with counters at end then exist for sources that report them, which was the first candidate in section 2 rejected only because the sink would have had to invent them |
| G13 | Connection failure has no reason code or per-stage timing; successful connection timing is not modelled | Ruckus `HccdClientConnection` `failure_type` (`ap_report.proto:2060`), `reason_code` (2074), `failed_msg_id` (2011), `connection_status` (2001), `snr` (2053); `ttcData` per-stage time to connect `clientAuthTTC`/`clientAssocTTC`/`clientEapTTC`/`clientRadiusTTC`/`clientDhcpTTC` (1248-1276, also on the client bin 1758-1786) and `isRoaming` (1241); `dns_rtt_average_msec_r`, `arp_rtt_average_msec_r` (1910, 1925); Aruba `wlanStaAssocFailureReason` (`WLSX-WLAN-MIB:1637`), `wlanStaAssocFailureElapsedTime`; Meraki connectionStats per stage (dossier 03); `EndpointConnectionFailure` (`endpoint.proto:119-139`) has `stage` and a free `detail` only | On `EndpointConnectionFailure`: `reason_code uint32` (IEEE 802.11 status/reason code registry pass-through), `rssi_millidbm`, `snr_millidb`, `elapsed Duration`; new `EndpointEvent` arm `connected {attachment, time_to_connect {association, authentication, eap, radius, dhcp, dns} Durations, roamed bool}` | `client_events` gains `reason_code UInt16`, `ttc_assoc_ms`, `ttc_auth_ms`, `ttc_eap_ms`, `ttc_radius_ms`, `ttc_dhcp_ms`, `ttc_dns_ms UInt32` and `kind = 'connected'`. A rollup `ap_connect_hourly (tenant, ap_key, ssid, hour, quantilesTDigestState(ttc_total))` answers "time to connect per SSID" over months at D x S x H rows |
| G14 | Client RF sample lacks rx rate, PER, noise floor, per-interval RSSI extremes | Ruckus `APClientInfo` `receiveSignalStrength` (`ap_client.proto:65`, distinct from `rssi` 58), `noiseFloor` (72), `throughputEst` (128), `txRetry` (156), `rxCRCErrFrames` (149), `txDropDataFrames` (135); `APReportBinClient` `maxRssi`/`minRssi`/`firstRssi` (`ap_report.proto:1464-1478`), `rxRatebps` (1702), `TxPER` (1832), `medianTxMCSRate`/`medianRxMCSRate` (1744-1751); Aruba `wlanStaFrameRetryRate` (`WLSX-WLAN-MIB:4668`), `wlanStaTransmitRate` (1471); Cisco `snr`, `rssi` (dossier 03) | On `WirelessAttachment`: `rx_rate_bps uint64` (rename `negotiated_rate_bps` to `tx_rate_bps`), `noise_floor_millidbm`, `tx_per_basis_points`; on `EndpointCounters`: `tx_dropped_frames`, `rx_error_frames` | `client_samples` gains 3 gauges and 2 counters; `client_presence` gains `rx_rate_last argMax`. The per-interval RSSI extremes are already produced by the hourly `min`/`max` and need no field |
| G15 | Client capabilities and MLO identity are reported and not modelled | Ruckus `APReportBinClient` `bandCap`, `vHTCap`, `streamCap`, `BTMCap` (`ap_report.proto:1716-1737`), `WiFi6Cap` (1864), `STACap` (1902), `mlo_capability`, `mlo_links`, `mld_addr`, `active_link` (1872-1895); Cisco `dot11-6ghz-cap`, `eht-capable`, `multi-link-client` (dossier 03 section 2); dossier 01 section 2 notes MLD address at AP and client | `EndpointWirelessCapabilities` on `EndpointState`: `standards repeated Dot11Standard`, `bands repeated WifiBand`, `max_nss`, `btm_capable`, `ft_capable`, `mld_address MacAddress`; on `WirelessAttachment`: `mlo_active_link bool` | Capabilities are state (KV, Postgres) and do not enter ClickHouse except `mld_address` as `anyLast` on `client_presence`, so an MLO client whose links report different MACs can be grouped by `mld_address` in Q8 without the endpoint service |
| G16 | Mesh links have no primitive | Ruckus `APMeshUplink` `upMac` (`ap_mesh.proto:96`), `rssi` (110), `txBytes`/`rxBytes` (117-131), `medianTxMCSRate` (145); `APMeshDownlink` (23-87); `APMeshNeighbor {mac, rssi}` (162-169); `ruckusSZAPMeshRole` (`RUCKUS-SZ-WLAN-MIB`), `ruckusZDWLANAPMeshHops`, `MeshType` (`RUCKUS-ZD-WLAN-MIB`); Aruba `WLSX-MESH-MIB`; the WLAN README lists mesh as deliberately absent | `MeshLink` table row under `net/wlan/v1`: `peer_mac`, `direction (uplink, downlink)`, `rssi_millidbm`, `median_tx_mcs`, `median_rx_mcs`, counters; `mesh_role` and `mesh_hops` on the radio-owning state | New `mesh_link_samples` `ORDER BY (tenant_id, device_id, peer_mac, ts)` at the controller cadence; mesh APs are a small minority so volume is low. A mesh uplink RSSI drop is the leading cause of an AP "offline" burst the baseline could not explain, so the row pays for itself in incident correlation |
| G17 | Neighbor stations (unassociated or rogue clients) are reported by monitoring radios and have no primitive | Aruba `wlsxMonStaInfoEntry` `monStaInfoBSSID`, `monStaInfoESSID`, `monStaInfoRSSI` (`WLSX-MON-MIB:2757`), `monStaInfoClassification` (2767), `monStaInfoMonitorTime`; `wlanAPRadioNumMonitoredClients` (`WLSX-WLAN-MIB:856`); Aruba WIDS traps (`wlsxStaImpersonation`, `wlsxValidClientMisassociation`, `wlsxClientFloodAttack`); Ruckus `sta_count` per rogue (G3) | `NeighborStation` sighting row: `mac`, `bssid`, `ssid`, `rssi_millidbm`, `classification`, carried by the `NeighborScan` of G1 | Optional `neighbor_sta_presence` with the shape of `neighbor_presence` keyed `(tenant_id, device_id, mac, hour)` and `retention_class = short` only: every passing phone is a member, so the hourly presence bound is what keeps it affordable. Add only for tenants that run WIDS |
| G18 | Controller join status, reboot and rejoin reasons of an AP are not a device fact in FlowSeer | Ruckus `APStatusSystem` `lastRebootReason` (`ap_status.proto:1827`), `rejoinReason` (1855), `apConnectedIp` (1932), `powerProfile` (2234); `ruckusSZAPConnStatus`, `ruckusSZAPRegStatus`, `ruckusSZAPConfigStatus` (`RUCKUS-SZ-WLAN-MIB`); `ruckusZDWLANAPFirstJoinTime`, `LastBootTime` (`RUCKUS-ZD-WLAN-MIB`); Aruba `wlsxNAPMasterStatusChange`, `wlsxAPNumRadioDown` (`WLSX-TRAP-MIB`); `DeviceState` has `uptime` only (`device.proto:210`); baseline: AP status was 30 % of legacy rows and Ruckus emits false single-sample offline readings | `ControllerAttachment` facet on `DeviceState` for controller-managed devices: `join_status` enum (`joined`, `disconnected`, `provisioning`, `rebooting`), `controller_address`, `joined_at`, `last_reboot_reason`, `rejoin_reason`; a `DeviceEvent` arm `join_status_changed` | Belongs to the device domain's `device_changes` table, keyed `(tenant_id, device_id, ts, record_id)`: a change row per transition instead of a status sample every 2 minutes. The wireless report notes it because the baseline's single-sample false offline problem is solved by storing transitions and debouncing them against `radio_samples.oper_status`, not by storing status samples |
| G19 | Radio antenna placement is reported by Aruba and has no field | `wlanAPRadioBearing` (`WLSX-WLAN-MIB:899`), `wlanAPRadioTiltAngle` (910); `wlanAPRadioTransmitPower10x` (962, 0.1 dBm resolution, fits `_millidbm`) | `bearing_degrees`, `tilt_degrees` on the component's placement, not on `RadioFacet` (it is installation data, not RF state) | None in ClickHouse; placement is inventory state |
| G20 | WLAN security registry pass-through and OWE transition pairing are still open | Dossier 09 section 4 proposed `Dot11AkmSuite`/`Dot11CipherSuite` pass-through enums and `WLAN_SECURITY_OWE_TRANSITION` with a referenced BSSID; `WlanSecurity` (`wlan_security.proto:9-29`) has no OWE transition value, and Ruckus `OWE_Transition` exists as an encryption method (dossier 01 section 3) | Add `WLAN_SECURITY_OWE_TRANSITION = 11` and `owe_transition_bssid MacAddress` on `Bss` | `bss_changes` attribute set gains the pair; no new table |

Consequences for the recommended model, in order of weight:

1. G8 and G9 reintroduce a per-BSS sample table (`bss_samples`) that section 2
   had avoided. The `Nested` client-count arrays on `radio_samples` stay for
   the per-poll view; the counters go to `bss_samples` at a 10-minute cadence
   with a 14 to 30 day TTL and an hourly rollup. At 20 k devices x 9 BSS x
   144 polls = 26 M rows/day, about 25 bytes/row, 0.65 GB/day, which the
   benchmark adds as a sixth table.
2. G11 and G12 make client sessions exact rows where the controller reports
   association start and disassociation, so `client_events` becomes the
   primary session source and `client_presence` the fallback and the
   per-hour RF/counter summary.
3. G2 and G6 turn two "derived from gaps or samples" answers into sourced
   change rows (`neighbor_events`, `radio_changes.reason`), each with a rule
   that the sink derives only when the source does not emit.
4. G4 and G5 add counters and quality gauges to `radio_samples`; the cost
   model of every radio query is unchanged (same rows), bytes per row rise
   by an estimated 50 to 80 %.
5. G1 cuts neighbor insert volume about six-fold without changing the merged
   presence rows.

Unverified in this pass: Ruckus field semantics beyond the vendored
comments (whether `connection*Count` fields are cumulative or per-interval
is not stated in `ap_status.proto`; `APReportBin*` `_r` suffixed fields are
per-bin by the message's own `binStartTime`, `ap_report.proto:105`), the Aruba
`ArubaRogueApType` value list (`ARUBA-TC`, not read), and whether Cisco 9800
exposes radio counters at the same granularity (dossier 02 did not read
`ap-oper.yang` field by field).
