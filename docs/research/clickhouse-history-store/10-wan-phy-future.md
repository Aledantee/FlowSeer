---
title: PoE, optics, path quality, cellular, and planned sources
date: 2026-10-08
status: research; sources fetched 2026-10-08, ClickHouse 26.8 LTS
---

# Lane G: WAN, path quality, physical-layer gauges, and planned sources

Scope: PoE, transceiver optics, path quality and ICMP probes, cellular, and
the planned sources without schema (flows, VPN tunnels, SD-WAN uplinks,
discovery sightings, webhook and stream payloads). Builds on the history
store dossiers (02 findings F1 to F74, 03 patterns) and does not repeat them.
Every external claim cites a URL fetched on 2026-10-08 or a repository
path:line. "Inference" marks reasoning from quoted text; "unverified" marks
what no fetched source confirms. Findings that other sections cite carry
a G number (G1 to G5, G28); the rest are referenced by section.

Worktree read: `/Users/a.tegtmeier/Projects/worktrees/FlowSeer/plan-clickhouse-store`.

## 0. Result in one screen

| Domain | Pattern | Table | Entity key after `(tenant_id, device_id, ...)` | Rows/day at 20k devices (assumptions in G3) |
| --- | --- | --- | --- | --- |
| PoE port | samples (+ changes for status) | `poe_port_samples`, `poe_port_changes` | `interface_name` | 25 M |
| PSE group | samples | `pse_budget_samples` | `pse_group` | 1 M |
| Optics | samples per lane (+ changes for module identity) | `optics_lane_samples`, `optics_module_changes` | `interface_name, lane` | 4.3 M |
| Path quality (controller time series) | samples | `path_quality_samples` | `interface_name, target_ip` | 11.5 M (resolution unverified, section 4.1) |
| ICMP probes (edge) | probes: interval rows holding raw RTT arrays, sketches built by MV | `probe_intervals`, `probe_hourly` | `interface_name, target_ip` | 29 M at one row per minute |
| Cellular | samples (+ changes for SIM and modem identity) | `cellular_samples`, `cellular_changes` | `interface_name` | 1.4 M |
| Flows (planned) | reserved: separate database, Akvorado shape | `flowseer_flows.flows`, rollups | none shared | traffic-driven, not device-driven (G28) |
| VPN tunnels, SD-WAN uplinks (planned) | changes (+ samples for counters in the sibling lane) | `uplink_changes` | `interface_name` | small |
| Discovery sightings (planned) | presence | `sightings` | `method, sighting_key` | small |
| Webhook and stream payloads | no table of their own: adapters map them into the typed records above or into the event pattern | none | none | none |

Lane G sums to about 72 M rows/day, which is below the legacy store's 40 M
rows/day for one customer only because rollups and interval rows replace
per-probe rows (section 4.2). Every query in this lane is bounded by
`devices in scope x entities per device x window / interval`, independent
of total tenants and total table size, because every table leads with
`(tenant_id, device_id, entity)` and the rollups carry the long windows
(02, F3 to F6, F33).

## 1. Shared facts for this lane

**G1. Interface identity is the name, not ifIndex.** `Interface.name` is
required and `if_index` "is not stable across restarts"
(`spec/proto/flowseer/net/interface/v1/interface.proto:28`, `:33-35`).
The draft DDL in 02 section 12.1 keys `interface_samples` by `ifindex
UInt32`. Correction for every table in this lane: the entity column is
`interface_name LowCardinality(String)`. Interface names repeat across
devices (`Gi1/0/1`), so the dictionary stays far under the 10,000 distinct
values F10 names. The sibling lane should align; a mixed key would break
joins between PoE and interface counters.

**G2. Where the messages hang.** `PoeFacet` and `PoePortDetail` sit on the
copper transport arm of an Ethernet facet
(`spec/proto/flowseer/net/phy/v1/copper_facet.proto:18`, `:22`), which a
physical interface embeds
(`spec/proto/flowseer/net/interface/v1/physical_interface.proto:14`).
`PluggableModule` sits on the Ethernet facet
(`spec/proto/flowseer/net/phy/v1/ethernet_facet.proto:74`) and on a
transceiver component
(`spec/proto/flowseer/model/inventory/v1/component.proto:206`). `PseBudget`,
`PathQuality`, and `CellularInterface` are embedded by nothing yet (grep over
`spec/proto`, 2026-10-08). The tables below assume the carrying record names
a device and, for path quality, an interface and a target address. That
assumption is the one open schema dependency of this lane.

**G3. Scale assumptions.** The baseline has about 900 switches among about
10,000 devices for one customer and plans for 20,000 devices
(`docs/research/2026-10-01-production-monitoring-baseline.md:28`, `:242`).
Rows/day here assume 1,800 switches with 48 PoE ports and 4 cages each,
about 10,000 installed modules with 1.5 lanes on average, 2,000 appliances
with 2 uplinks and 2 probe targets, 2,000 cellular interfaces, 5 minute
SNMP polls, 2 to 3 minute controller polls. These are planning inputs, not
measurements.

**G4. Counters in this lane carry no discontinuity marker.** The schema
rule says every `<Domain>Counters` message carries `last_discontinuity`
(`docs/architecture/2026-09-25-schema-building-blocks-direction.md:137-141`).
`PoePortDetail` is not a counters message and has none
(`poe_port_detail.proto:13-40`). RFC 3621 types all five port counters as
`Counter32` ("This counter is incremented when the PSE state diagram enters
the state SIGNATURE_INVALID", `https://www.rfc-editor.org/rfc/rfc3621.txt`).
Consequence: a decrease is the only reset signal, and a 32-bit wrap looks
like a reset. `nonNegativeDerivative` clamps both to zero (02, F28). These
counters move rarely, so under-counting one interval is acceptable. If the
sibling lane gives `interface_samples` a `discontinuity_epoch` from
`sysUpTime`, the PoE row should carry the same column so both tables share
one rate rule.

**G5. Dedup rule for this lane.** Samples and probe intervals have a
natural key that is unique per poll, so `ReplicatedReplacingMergeTree` on
the natural key collapses a late redelivery with no extra column (02, F45).
Changes tables end the key with `record_id` (02, F45, 12.2). Rollups fed by
MVs double-count `sum` and `count` for a duplicate that slips past the
insert dedup window, and `min`, `max`, `argMax`, and sketches of identical
values are idempotent or near-idempotent (03, samples vs changes; 02, 12.1).
The residue is bounded by F48.

## 2. PoE

### 2.1 Data semantics

| Field | Source | Kind | Change rate |
| --- | --- | --- | --- |
| `supported`, `role`, `power_class` (`poe_facet.proto:25`, `:28`, `:33`) | PoeFacet | attribute | changes when a powered device is plugged or replaced |
| `power_draw_nanowatts` (`:36`), `allocated_power_nanowatts` (`:41`) | PoeFacet | gauge | every poll, small noise around a plateau |
| `status` (`:38`, enum `poe_status.proto:10-25`) | PoeFacet | state | rare transitions; RFC 3621 sends a notification "on every status change except in the searching mode", rate-limited to one per 500 ms (`rfc3621.txt`) |
| five fault counters (`poe_port_detail.proto:27-39`) | PoePortDetail | cumulative counter, Counter32 at the source (G4) | rare increments |
| `pse_group`, `pse_port` (`:16`, `:21`) | PoePortDetail | source-local key | static |
| `oper_status` (`pse_budget.proto:23`), `power_nanowatts` (`:26`), `usage_threshold_basis_points` (`:35`) | PseBudget | state and static attributes | rare |
| `consumption_nanowatts` (`pse_budget.proto:30`) | PseBudget | gauge, `Gauge32` "Measured usage power expressed in Watts" at the source (`rfc3621.txt`) | every poll |

Cardinality per device: ports (24 to 48) and PSE groups (1 per box in a
stack, `pse_budget.proto:10-11`).

### 2.2 Candidates

| Model | Pros | Cons |
| --- | --- | --- |
| A. Wide sample row per port per poll, status and attributes on the row | one table answers "what at T" and the power chart; constant columns compress to near zero (F25, F71) | status transitions need a scan to find |
| B. Narrow attribute rows `(port, attribute, value)` | no defaults | 10 rows per port per poll, 10x the key overhead (03, generic designs paid for this) |
| C. A plus a changes table for status, class, and role transitions | flap queries are key-range reads (02, 12.2) | a second writer path that must diff against state |

Recommendation: C. The status column stays on the sample row so that
"what was it at T" is one granule, and transitions also go to a changes
table so that "when did it change" does not scan samples.

### 2.3 DDL

```sql
CREATE TABLE flowseer.poe_port_samples
(
    tenant_id        LowCardinality(String),
    device_id        UUID,
    interface_name   LowCardinality(String),
    ts               DateTime            CODEC(Delta, ZSTD(1)),
    pse_group        UInt16              CODEC(T64, ZSTD(1)),   -- poe_port_detail.proto:16
    pse_port         UInt16              CODEC(T64, ZSTD(1)),   -- poe_port_detail.proto:21
    supported        Enum8('absent' = 0, 'false' = 1, 'true' = 2),   -- poe_facet.proto:25, tri-state without Nullable (F9)
    role             Enum8('unspecified' = 0, 'pse' = 1, 'pd' = 2),  -- poe_role.proto:9-16
    power_class      UInt8               CODEC(T64, ZSTD(1)),   -- poe_facet.proto:33
    status           Enum8('unspecified' = 0, 'disabled' = 1, 'searching' = 2, 'delivering_power' = 3,
                           'test' = 4, 'fault' = 5, 'other_fault' = 6),  -- poe_status.proto:10-25
    power_draw_nw    UInt64              CODEC(T64, ZSTD(1)),   -- poe_facet.proto:36
    allocated_nw     UInt64              CODEC(T64, ZSTD(1)),   -- poe_facet.proto:41
    invalid_signature_count UInt64       CODEC(Delta, ZSTD(1)), -- poe_port_detail.proto:27
    power_denied_count      UInt64       CODEC(Delta, ZSTD(1)), -- :30
    overload_count          UInt64       CODEC(Delta, ZSTD(1)), -- :33
    short_count             UInt64       CODEC(Delta, ZSTD(1)), -- :36
    mps_absent_count        UInt64       CODEC(Delta, ZSTD(1)), -- :39
    present_mask     UInt16              CODEC(T64, ZSTD(1)),   -- bit per optional field above
    retention_class  Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    site_id          LowCardinality(String)
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY toMonday(ts)
ORDER BY (tenant_id, device_id, interface_name, ts)
TTL ts + INTERVAL 90 DAY DELETE WHERE retention_class = 'short',
    ts + INTERVAL 1 YEAR DELETE WHERE retention_class = 'standard',
    ts + INTERVAL 2 YEAR DELETE WHERE retention_class = 'long'
SETTINGS ttl_only_drop_parts = 1, min_age_to_force_merge_seconds = 604800,
         min_age_to_force_merge_on_partition_only = 1;
```

Reasoning per clause: key order, partition unit, retention classes, and
force-merge settings follow 02 (F3 to F8, F36, F40, 12.1). `T64` on the
power gauges because a port's draw sits in a narrow range inside one block
("T64 can be effective ... when the range in a block is small", F25); the
benchmark compares it with `Gorilla` on the same column (section 9).
`Delta` on counters because the stride is small and positive within one
port's run (F25). `supported` is a three-valued enum because the proto
distinguishes absent from explicit false (`poe_facet.proto:23-25`) and F9
forbids Nullable. `present_mask` carries the presence of each optional
field as in 02, 12.1.

```sql
CREATE TABLE flowseer.pse_budget_samples
(
    tenant_id        LowCardinality(String),
    device_id        UUID,
    pse_group        UInt16              CODEC(T64, ZSTD(1)),   -- pse_budget.proto:18
    ts               DateTime            CODEC(Delta, ZSTD(1)),
    oper_status      Enum8('unspecified' = 0, 'on' = 1, 'off' = 2, 'faulty' = 3),  -- pse_oper_status.proto:9-18
    power_nw         UInt64              CODEC(T64, ZSTD(1)),   -- pse_budget.proto:26
    consumption_nw   UInt64              CODEC(T64, ZSTD(1)),   -- pse_budget.proto:30
    usage_threshold_bp UInt16            CODEC(T64, ZSTD(1)),   -- pse_budget.proto:35
    present_mask     UInt8,
    retention_class  Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    site_id          LowCardinality(String)
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY toMonday(ts)
ORDER BY (tenant_id, device_id, pse_group, ts)
TTL ts + INTERVAL 90 DAY DELETE WHERE retention_class = 'short',
    ts + INTERVAL 1 YEAR DELETE WHERE retention_class = 'standard',
    ts + INTERVAL 2 YEAR DELETE WHERE retention_class = 'long'
SETTINGS ttl_only_drop_parts = 1, min_age_to_force_merge_seconds = 604800,
         min_age_to_force_merge_on_partition_only = 1;
```

`poe_port_changes` is the 02 section 12.2 `interface_changes` shape with
`attribute Enum8('status' = 1, 'power_class' = 2, 'role' = 3, 'supported' = 4)`
and a typed `new_status` column, keyed
`(tenant_id, device_id, interface_name, ts, record_id)`. Nothing else
differs, so the DDL is not repeated.

Rollup `poe_port_hourly` (AggregatingMergeTree, monthly partitions, 3 year
TTL as in 02, 12.1): `max(power_draw_nw)`, `min(power_draw_nw)`,
`avgState(power_draw_nw)`, `max(allocated_nw)`, `deltaSumTimestampState`
for each of the five counters, `samples sum`, `argMaxState(status, ts)`.
`pse_budget_hourly` carries `max`, `min`, `avgState` of `consumption_nw`
and `max(power_nw)`.

### 2.4 Dedup under redelivery

The natural key `(tenant_id, device_id, interface_name, ts)` is one row per
poll. A redelivered row is byte-identical and collapses on merge (G5). The
hourly MV sees it once more: `max`, `min`, `argMax`, and
`deltaSumTimestamp` (zero delta for an equal value at an equal timestamp,
02, 12.1) are unaffected, `avgState` and `samples` move by one sample in
one hour, which no chart can see.

### 2.5 Queries and cost model

Notation: D devices in scope, E ports per device, W window, P poll
interval, N = W / P samples per entity. One granule holds 8,192 rows.

| Query | SQL shape | Rows read | Granules |
| --- | --- | --- | --- |
| Power chart, one port, 24 h | `WHERE tenant_id = ? AND device_id = ? AND interface_name = ? AND ts BETWEEN ... ORDER BY ts` | N = 288 | 1 to 2 |
| Switch view, all ports, 24 h | same without `interface_name` | E x N = 13,824 | 2 to 3 |
| Site top-N draw, last 24 h | `WHERE tenant_id = ? AND device_id IN (list) AND ts >= now() - 1 DAY GROUP BY device_id, interface_name ORDER BY max(power_draw_nw) DESC LIMIT 20` | D x E x N (2,000 x 48 x 288 = 27.6 M) | about D x 3 = 6,000 |
| What was port X at T | `WHERE ... AND ts <= T ORDER BY ts DESC LIMIT 1` | 1 granule, read in reverse key order (F51) | 1 |
| When did status change | `poe_port_changes WHERE tenant_id, device_id, interface_name ORDER BY ts DESC LIMIT n` | change rows of that port only | 1 |
| Budget chart over 3 months | `pse_budget_hourly WHERE ... GROUP BY hour` | groups x 2,160 hours | few |
| PoE faults per site this week | `poe_port_hourly WHERE device_id IN (list) AND hour >= ...` with `deltaSumTimestampMerge` | D x E x 168 | D x 1 |

Every cost is a product of scope and window. The site top-N over raw rows
is the largest at 27.6 M rows for a 2,000 device site, which is a sequential
read of about 6,000 granules per primary-index range and finishes within the
per-query limits of F56. When the site is a whole 10,000 device tenant, the
query runs on `poe_port_hourly` (D x E x 24 rows = 11.5 M) or is bounded by
`LIMIT` and `max_rows_to_read`. Nothing grows with total data.

### 2.6 Write cost and storage

Writes: 25 M rows/day is 290 rows/s on average. With the sink batching per
table every few seconds, inserts stay at well under 1 per second per table
and one part per insert per partition (F16). Storage estimate with the
codecs above: key columns amortise to under 1 byte/row inside a device run,
`ts` Delta about 1 byte, two gauges 2 to 4 bytes, counters near 0 (constant
deltas), enums near 0, so about 6 to 10 bytes/row, or 150 to 250 MB/day.
Estimate, to be measured with `system.columns` (F25).

### 2.7 Failure modes

| Failure | How the design avoids it |
| --- | --- |
| A Counter32 wrap looks like a reset | rate clamped to zero, one interval under-counted (G4); these counters rarely move |
| Port absent from a poll (unplugged module, SNMP timeout) | no row, no fake zeros (F12); `WITH FILL ... STALENESS` for charts (F31) |
| A stack member joins and `pse_group` numbering shifts | `pse_group` is a source-local key kept on the row, not in the port key, so port history survives |
| Status flaps every poll | changes rows per transition stay small; a flap counter query is `count()` over the key range |

## 3. Transceiver optics

### 3.1 Data semantics

SFF-8472 reports each quantity as a 16-bit value: temperature "in
increments of 1/256 ºC", voltage with "LSB equal to 100 µV", bias with "LSB
equal to 2 µA", TX and RX power with "LSB equal to 0.1 µW, yielding a total
range of 0 to 6.5535 mW" (`https://members.snia.org/document/dl/25916`,
SFF-8472 Rev 12.5a, section 9.8, text extracted from the PDF). The
thresholds are "factory preset values" per quantity (section 9.4, Table
9-5), so they are static per module. The proto carries the same shape:
value plus four ordered thresholds per quantity
(`spec/proto/flowseer/net/measure/v1/sensor.proto:25-33`, `:53-61`,
`:80-88`, `:109-117`), nanowatts for power
(`module_lane.proto:24-30`), microamperes for bias (`:34`), millidegrees and
microvolts at module level (`module_diagnostics.proto:17`, `:21`).

| Field | Kind | Change rate |
| --- | --- | --- |
| `present` (`pluggable_module.proto:40`) | state | rare |
| identity: `form_factor`, `connector`, `vendor`, `part_number`, `revision`, `serial_number`, `date_code`, `encoding_code`, `nominal_bit_rate_bps`, `media_codes`, `application_codes` (`:43-85`) | attributes | change only when the module is swapped |
| module `temperature`, `voltage` values (`module_diagnostics.proto:17`, `:21`) | gauges | every poll, slow drift |
| per-lane `wavelength_nanometers` (`module_lane.proto:23`) | attribute | static |
| per-lane `tx_power`, `rx_power`, `bias` values (`:27`, `:30`, `:34`) | gauges | every poll, 16-bit source resolution, slow drift until a fibre degrades |
| all thresholds | attributes | static per module (SFF-8472 9.4) |

Cardinality: modules per device (4 to 48 cages, most empty on access
switches), lanes per module 1 to 8 (`module_lane.proto:12`,
`pluggable_module.proto:92`).

### 3.2 Candidates

| Model | Pros | Cons |
| --- | --- | --- |
| A. One row per module per poll with `Array` columns per lane | one row per poll | lane charts need `arrayJoin`, per-lane key impossible, arrays of 1 to 8 elements cost offsets |
| B. One row per lane per poll, module-level values repeated on each lane row | matches the 12.1 samples shape, lane is a key column, repeated values compress to near zero inside a block | 1.5x the rows of A at the assumed lane mix |
| C. B with thresholds in a separate changes table only | smaller sample row | "margin to threshold" needs a join or dictionary per query |
| D. B with thresholds on every sample row | margin queries are one-table; a static column in a sorted run costs almost nothing after ZSTD (F25, F71) | 12 extra columns, each expected under 0.3 bytes/row (estimate, benchmark measures) |

Recommendation: D for samples, plus an `optics_module_changes` table
for identity (serial, part, vendor, date code) so "where was module serial
S" and "when was it swapped" are key-range reads. Thresholds ride on the
sample row because that makes the degradation query self-contained. If the
benchmark shows the threshold columns above 1 byte/row, fall back to C.

### 3.3 DDL

```sql
CREATE TABLE flowseer.optics_lane_samples
(
    tenant_id        LowCardinality(String),
    device_id        UUID,
    interface_name   LowCardinality(String),
    lane             UInt8               CODEC(T64, ZSTD(1)),   -- module_lane.proto:17, 1..8
    ts               DateTime            CODEC(Delta, ZSTD(1)),
    present          UInt8,                                     -- pluggable_module.proto:40
    wavelength_nm    UInt16              CODEC(T64, ZSTD(1)),   -- module_lane.proto:23
    tx_power_nw      UInt32              CODEC(T64, ZSTD(1)),   -- module_lane.proto:27, sensor.proto:109; SFF-8472 max 6.5535 mW fits UInt32
    rx_power_nw      UInt32              CODEC(T64, ZSTD(1)),   -- module_lane.proto:30
    bias_ua          Int32               CODEC(T64, ZSTD(1)),   -- module_lane.proto:34, sensor.proto:80
    module_temp_mdeg Int32               CODEC(T64, ZSTD(1)),   -- module_diagnostics.proto:17, sensor.proto:25
    module_volt_uv   Int32               CODEC(T64, ZSTD(1)),   -- module_diagnostics.proto:21, sensor.proto:53
    -- static thresholds, repeated per row (sensor.proto:111-117, :82-88, :27-33, :55-61)
    tx_power_hi_alarm_nw UInt32 CODEC(ZSTD(1)), tx_power_hi_warn_nw UInt32 CODEC(ZSTD(1)),
    tx_power_lo_warn_nw  UInt32 CODEC(ZSTD(1)), tx_power_lo_alarm_nw UInt32 CODEC(ZSTD(1)),
    rx_power_hi_alarm_nw UInt32 CODEC(ZSTD(1)), rx_power_hi_warn_nw UInt32 CODEC(ZSTD(1)),
    rx_power_lo_warn_nw  UInt32 CODEC(ZSTD(1)), rx_power_lo_alarm_nw UInt32 CODEC(ZSTD(1)),
    bias_hi_alarm_ua Int32 CODEC(ZSTD(1)), bias_hi_warn_ua Int32 CODEC(ZSTD(1)),
    bias_lo_warn_ua  Int32 CODEC(ZSTD(1)), bias_lo_alarm_ua Int32 CODEC(ZSTD(1)),
    module_temp_hi_alarm_mdeg Int32 CODEC(ZSTD(1)), module_temp_hi_warn_mdeg Int32 CODEC(ZSTD(1)),
    module_temp_lo_warn_mdeg  Int32 CODEC(ZSTD(1)), module_temp_lo_alarm_mdeg Int32 CODEC(ZSTD(1)),
    module_volt_hi_alarm_uv Int32 CODEC(ZSTD(1)), module_volt_hi_warn_uv Int32 CODEC(ZSTD(1)),
    module_volt_lo_warn_uv  Int32 CODEC(ZSTD(1)), module_volt_lo_alarm_uv Int32 CODEC(ZSTD(1)),
    present_mask     UInt32              CODEC(T64, ZSTD(1)),   -- bit per optional value and threshold
    retention_class  Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    site_id          LowCardinality(String)
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY toMonday(ts)
ORDER BY (tenant_id, device_id, interface_name, lane, ts)
TTL ts + INTERVAL 90 DAY DELETE WHERE retention_class = 'short',
    ts + INTERVAL 1 YEAR DELETE WHERE retention_class = 'standard',
    ts + INTERVAL 2 YEAR DELETE WHERE retention_class = 'long'
SETTINGS ttl_only_drop_parts = 1, min_age_to_force_merge_seconds = 604800,
         min_age_to_force_merge_on_partition_only = 1;
```

`UInt32` for power: the proto is `uint64` nanowatts, and SFF-8472's 16-bit
field tops out at 6.5535 mW = 6,553,500 nW, so UInt32 holds any SFF-8472
value with margin. A CMIS module reporting more than 4.29 W per lane would
overflow; no fetched source shows such a value (unverified), and the
benchmark asserts the bound at insert with a check in the sink. Empty cages
(`present = 0`) write no lane rows, only a module changes row, so absent
modules cost nothing per poll.

```sql
CREATE TABLE flowseer.optics_module_changes
(
    tenant_id        LowCardinality(String),
    device_id        UUID,
    interface_name   LowCardinality(String),
    ts               DateTime            CODEC(Delta, ZSTD(1)),
    record_id        UUID,
    present          UInt8,                                     -- pluggable_module.proto:40
    form_factor      LowCardinality(String),                    -- :43 (enum name)
    connector        LowCardinality(String),                    -- :46
    vendor           LowCardinality(String),                    -- :49
    part_number      LowCardinality(String),                    -- :52
    revision         LowCardinality(String),                    -- :55
    serial_number    String              CODEC(ZSTD(1)),        -- :58
    date_code        LowCardinality(String),                    -- :61
    encoding_code    UInt8,                                     -- :64
    nominal_bit_rate_bps UInt64          CODEC(T64, ZSTD(1)),   -- :67
    media_codes      Array(UInt8),                              -- :71
    application_codes Array(UInt8),                             -- :80
    lanes            UInt8,                                     -- count of :92
    retention_class  Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    site_id          LowCardinality(String),
    INDEX idx_serial serial_number TYPE bloom_filter(0.01) GRANULARITY 4
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY toMonday(ts)
ORDER BY (tenant_id, device_id, interface_name, ts, record_id)
TTL ts + INTERVAL 2 YEAR
SETTINGS ttl_only_drop_parts = 1, min_age_to_force_merge_seconds = 604800,
         min_age_to_force_merge_on_partition_only = 1;
```

A row is written when the module identity in the poll differs from the
current state the producer holds (the edge or the state projector, not the
ClickHouse sink, which has no state). The bloom filter on `serial_number`
serves "which port holds module S now and where was it before" across
devices within a tenant, the same justification as the MAC filter in 02,
F19 and 12.3.

Rollup `optics_lane_hourly` (AggregatingMergeTree, `PARTITION BY
toYYYYMM(hour)`, `ORDER BY (tenant_id, device_id, interface_name, lane,
hour)`, TTL 3 years): `min`, `max`, `avgState` of `rx_power_nw`,
`tx_power_nw`, `bias_ua`, `module_temp_mdeg`, `samples`, and
`anyLast(rx_power_lo_warn_nw)` so the rollup is self-contained for margin
charts.

### 3.4 Degradation trend queries

Rx power trend of one lane over 90 days from the rollup, in dBm at read
time (the proto keeps linear units because zero has no dBm value,
`sensor.proto:91-94`):

```sql
SELECT hour,
       10 * log10(avgMerge(rx_power_avg) / 1e6) AS rx_dbm,       -- nW to mW, then dBm
       10 * log10(min(rx_power_min) / 1e6)      AS rx_min_dbm,
       10 * log10(anyLast(rx_power_lo_warn_nw) / 1e6) AS lo_warn_dbm
FROM flowseer.optics_lane_hourly
WHERE tenant_id = {t} AND device_id = {d} AND interface_name = {i} AND lane = {l}
  AND hour >= now() - INTERVAL 90 DAY
GROUP BY hour ORDER BY hour;
```

Cost: 90 x 24 = 2,160 rows, one key range. Guard `log10(0)` with
`greatest(x, 1)` in the API.

Lanes drifting toward their low-warning threshold across a site, last 7
days, slope by least squares over the daily minimum:

```sql
SELECT device_id, interface_name, lane,
       min(rx_power_min) AS worst_nw,
       anyLast(rx_power_lo_warn_nw) AS lo_warn_nw,
       arrayReduce('simpleLinearRegression', groupArray(toUnixTimestamp(hour)), groupArray(toFloat64(rx_power_min))).1 AS slope_nw_per_s
FROM flowseer.optics_lane_hourly
WHERE tenant_id = {t} AND device_id IN ({site devices}) AND hour >= now() - INTERVAL 7 DAY
GROUP BY device_id, interface_name, lane
HAVING worst_nw < lo_warn_nw * 1.5 OR slope_nw_per_s < 0
ORDER BY worst_nw / lo_warn_nw ASC LIMIT 50;
```

Cost: D x modules x lanes x 168 rows (2,000 x 4 x 1.5 x 168 = 2 M), D key
ranges. Independent of total data. `simpleLinearRegression` exists as an
aggregate function in ClickHouse (name from the 02 fetch set is not
confirmed in this pass, unverified); the benchmark either confirms it or
replaces it with a `covarPop / varPop` slope.

"Alarm now" without an alarm event source: latest sample per lane via
`argMax` over the last two poll intervals compared with its own threshold
columns, one granule per lane.

### 3.5 Dedup, write cost, storage, failure modes

Dedup as in G5. Writes: 4.3 M lane rows/day is 50 rows/s. Storage estimate:
five gauges at 1 to 2 bytes each after T64 and ZSTD (16-bit source
resolution, slow drift), thresholds and identity near zero, key and `ts`
under 2 bytes, so 8 to 14 bytes/row, about 50 MB/day. Failure modes: a
module swap inside a poll interval shows as a step in all gauges, which the
changes table explains; a module that reports `present` but no diagnostics
(copper SFP, DAC) writes one lane row per poll with values absent in
`present_mask`, which is the honest shape and a benchmark case for sparse
serialization (02, open point 18).

## 4. Path quality and ICMP probes

### 4.1 Two sources, two shapes

**Controller time series.** `PathQuality` carries round-trip `latency`,
`jitter`, and `loss_basis_points` (`path_quality.proto:17`, `:19`, `:21`),
modelled on Meraki's uplink series. The Meraki endpoint returns "the uplink
loss and latency for every MX in the organization from at latest 2 minutes
ago", a window where "t1 can be a maximum of 5 minutes after t0", with
points `ts`, `lossPercent`, `latencyMs` per `uplink` in `wan1, wan2, wan3,
cellular` and per destination `ip`
(`https://developer.cisco.com/meraki/api-v1/get-organization-devices-uplinks-loss-and-latency/`).
The page gives no point resolution and no jitter field (unverified
resolution; G19). The source hands FlowSeer summaries without a probe
count, so these are gauges: samples pattern, entity `(interface_name,
target_ip)`.

**Edge ICMP probes.** The baseline pings "every AP and switch" and finds
that "ICMP and controller availability measure different things"
(`docs/research/2026-10-01-production-monitoring-baseline.md:157`, `:177-179`).
The device service record uses the probe as evidence: "ICMP answers but the
management protocol does not (a credential or config problem, never counted
toward MISSING)"
(`docs/architecture/2026-08-20-device-service-and-inventory-direction.md:265-267`).
FlowSeer owns these probes, so it can choose the record shape. There is no
proto for a probe result yet; the fields below are what the edge measures
(sent, received, each RTT) and are the schema the store reserves.

### 4.2 Raw per probe versus per-interval aggregates at the edge

| Model | Rows/day at 20k targets | Pros | Cons |
| --- | --- | --- | --- |
| A. One row per probe (1 per 10 s) | 173 M | lossless, any statistic later | 2.4x all other lane G tables combined; every query reads 8,640 rows per target per day |
| B. Per-interval summary computed at the edge: count, loss, min, avg, max, fixed percentiles | 29 M at 1 min | small | a percentile of per-minute percentiles is not a percentile; merging to an hour or a day needs the raw values or a mergeable sketch |
| C. Per-interval row with the raw RTT list as `Array(UInt32)` plus `sent` and `received`; sketches built in ClickHouse by MV | 29 M at 1 min, 6 M at 5 min | lossless, mergeable, compact (60 small integers compress to about 1 byte each), sketches computed where they can be merged | array columns cost an offsets stream; quantile over a window needs `arrayJoin` |
| D. Per-interval row with a fixed log-bucket histogram | same as C | mergeable by element-wise sum | lossy, sum-based so duplicate-sensitive, bucket layout frozen at the edge |

Why sketches cannot come from the edge: a ClickHouse quantile state is an
`AggregateFunction` value with "implementation-specific binary
representations"
(`https://clickhouse.com/docs/sql-reference/data-types/aggregatefunction.md`),
so a pure Go edge cannot produce one. Sketches are therefore computed in
ClickHouse from raw values, and the raw values must reach ClickHouse in
some form. C does that at the lowest row count. Recommendation: C.

Which sketch for the rollup:

| Function | Quoted properties | Fit |
| --- | --- | --- |
| `quantileTiming` | "The result is deterministic"; exact when "Total number of values does not exceed 5670" or values under 1,024 ms, "Otherwise ... rounded to the nearest multiple of 16 ms"; values over 30,000 "assumed to be 30,000"; "If negative values are passed ... undefined" (`https://clickhouse.com/docs/sql-reference/aggregate-functions/reference/quantiletiming.md`) | right for RTT in ms: deterministic, 1 ms resolution below 1 s, hourly buckets at one probe per second hold 3,600 values and stay exact; cap at 30 s is harmless for ICMP |
| `quantileTDigest` / `Weighted` | "Memory consumption is log(n)"; "The maximum error is 1%"; "The result depends on the order of running the query, and is nondeterministic" (`.../quantiletdigest.md`, `.../quantiletdigestweighted.md`) | fallback when sub-ms resolution matters; nondeterminism is a benchmark nuisance |
| `quantileDD` | `quantileDD(relative_accuracy, [level])(expr)`, "Introduced in: v24.1.0", sketch size about "log(max_value/min_value)/relative_accuracy", "recommended value is 0.001 or higher" (`.../quantileddsketch.md`) | relative error at every scale, but at 0.01 accuracy and a 1e5 RTT range the sketch is about 1,000 buckets per row, too large for per-target hourly rows |

Recommendation: `quantileTimingState` over RTT in milliseconds in the
hourly rollup, raw microseconds kept in the interval row. Jitter is left to
the reader: RFC 3393 says "jitter" "is used in different ways by different
groups of people" and defines "the difference between the one-way-delay of
the selected packets" with percentiles over the sample
(`https://www.rfc-editor.org/rfc/rfc3393.txt`). With the raw RTT list,
`arrayDifference` gives per-interval delay variation and its percentile at
read time, and the controller-reported `jitter` is stored as the source
gave it, never merged across intervals.

### 4.3 DDL

```sql
CREATE TABLE flowseer.probe_intervals
(
    tenant_id        LowCardinality(String),
    device_id        UUID,                                      -- the device the row is about: the target of an edge probe, or the probing appliance
    interface_name   LowCardinality(String),                    -- uplink for appliance probes, '' for edge probes
    target_ip        IPv6,                                      -- IPv4 mapped (F11)
    interval_start   DateTime            CODEC(Delta, ZSTD(1)),
    interval_s       UInt16              CODEC(T64, ZSTD(1)),
    prober           Enum8('edge' = 1, 'device' = 2, 'controller' = 3),
    edge_id          LowCardinality(String),                    -- Provenance.edge, '' when central
    sent             UInt16              CODEC(T64, ZSTD(1)),
    received         UInt16              CODEC(T64, ZSTD(1)),
    rtt_us           Array(UInt32)       CODEC(T64, ZSTD(1)),   -- one entry per received probe, send order; empty for summary-only sources
    latency_us       UInt32              CODEC(T64, ZSTD(1)),   -- path_quality.proto:17 as reported, or the edge mean
    jitter_us        UInt32              CODEC(T64, ZSTD(1)),   -- path_quality.proto:19 as reported
    loss_bp          UInt16              CODEC(T64, ZSTD(1)),   -- path_quality.proto:21 as reported, or 10000 * (sent - received) / sent
    present_mask     UInt8,
    retention_class  Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    site_id          LowCardinality(String)
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY toMonday(interval_start)
ORDER BY (tenant_id, device_id, interface_name, target_ip, interval_start)
TTL interval_start + INTERVAL 30 DAY DELETE WHERE retention_class = 'short',
    interval_start + INTERVAL 90 DAY DELETE WHERE retention_class IN ('standard', 'long')
SETTINGS ttl_only_drop_parts = 1, min_age_to_force_merge_seconds = 604800,
         min_age_to_force_merge_on_partition_only = 1;

CREATE TABLE flowseer.probe_hourly
(
    tenant_id        LowCardinality(String),
    device_id        UUID,
    interface_name   LowCardinality(String),
    target_ip        IPv6,
    hour             DateTime,
    sent             SimpleAggregateFunction(sum, UInt64),
    received         SimpleAggregateFunction(sum, UInt64),
    rtt_min_us       SimpleAggregateFunction(min, UInt32),
    rtt_max_us       SimpleAggregateFunction(max, UInt32),
    rtt_sum_us       SimpleAggregateFunction(sum, UInt64),
    rtt_ms_q         AggregateFunction(quantilesTiming(0.5, 0.95, 0.99), UInt32),
    intervals_lost   SimpleAggregateFunction(sum, UInt32)       -- intervals with received = 0: onset counting
)
ENGINE = ReplicatedAggregatingMergeTree
PARTITION BY toYYYYMM(hour)
ORDER BY (tenant_id, device_id, interface_name, target_ip, hour)
TTL hour + INTERVAL 2 YEAR
SETTINGS ttl_only_drop_parts = 1;

CREATE MATERIALIZED VIEW flowseer.probe_hourly_mv TO flowseer.probe_hourly AS
SELECT tenant_id, device_id, interface_name, target_ip,
       toStartOfHour(interval_start) AS hour,
       sum(sent) AS sent, sum(received) AS received,
       min(arrayMin(rtt_us)) AS rtt_min_us, max(arrayMax(rtt_us)) AS rtt_max_us,
       sum(arraySum(rtt_us)) AS rtt_sum_us,
       quantilesTimingState(0.5, 0.95, 0.99)(rtt_ms) AS rtt_ms_q,
       sum(received = 0) AS intervals_lost
FROM flowseer.probe_intervals
ARRAY JOIN arrayMap(x -> intDiv(x, 1000), rtt_us) AS rtt_ms
WHERE notEmpty(rtt_us)
GROUP BY tenant_id, device_id, interface_name, target_ip, hour;
```

The `ARRAY JOIN` inside the MV multiplies the insert block by the array
length (60 at one probe per second and one row per minute), which is the
price of computing the sketch in ClickHouse. Whether `ARRAY JOIN` is
accepted in an incremental MV select on 26.8 is not confirmed by a fetched
page (unverified); the fallback is a second MV over a Null-engine staging
table, or the sink inserting the exploded rows into a Null table feeding
both the interval table and the rollup. The benchmark settles it.
`sum(received = 0)` over a row where `rtt_us` is empty does not fire
because of the `WHERE`, so `intervals_lost` needs a second, unfiltered MV
or the condition moved into `-If` combinators; the benchmark picks one.
Summary-only sources (controller series, empty `rtt_us`) feed only
`sent`, `received`, and `latency_us` averages.

Retention is short for interval rows because 29 M rows/day with arrays is
the largest table in the lane, and two years of hourly rollups answer the
long-window questions.

### 4.4 Dedup under redelivery

The natural key `(tenant_id, device_id, interface_name, target_ip,
interval_start)` is one row per interval. A redelivered row collapses on
merge. The rollup's `sent`, `received`, `rtt_sum_us`, and the Timing state
count a duplicate interval twice when it slips past the insert dedup window
(F48 residue). `min` and `max` do not. For an availability figure that must
be exact, the API reads `probe_intervals` with `GROUP BY` natural key over
the window, which is bounded by the window length.

### 4.5 Queries and cost model

| Query | Shape | Rows read |
| --- | --- | --- |
| RTT chart, one target, 24 h, p95 per 5 min | `probe_intervals WHERE key AND interval_start >= ...` with `arrayReduce('quantileTiming(0.95)', rtt_us)` per row, or `ARRAY JOIN` and `GROUP BY toStartOfFiveMinutes` | 1,440 rows, 1 key range |
| Was device D reachable at T | `WHERE key AND interval_start <= T ORDER BY interval_start DESC LIMIT 1`, `received > 0` | 1 granule |
| Site availability last 24 h | `sum(received) / sum(sent)` over `device_id IN (list)` | D x targets x 1,440 (2,000 x 1 x 1,440 = 2.9 M) |
| Loss onsets per hour (baseline's "new loss onsets per hour") | `probe_hourly` `intervals_lost` over a scope | D x 24 per day |
| 90-day p99 trend per uplink | `quantilesTimingMerge(0.5, 0.95, 0.99)(rtt_ms_q)` from `probe_hourly` | 2,160 rows per target |
| Tenant-wide worst 20 targets this hour | `probe_hourly WHERE tenant_id = ? AND hour = ... ORDER BY ... LIMIT 20` | all targets of the tenant for one hour: up to 10,000 rows for the largest tenant, bounded by tenant size, not total data; the primary index does not help because `hour` is last, so this reads every granule of the tenant's current month partition that holds that hour. Bounded by adding a `minmax` skip index on `hour` (F19), which the benchmark verifies with `EXPLAIN indexes=1` |

### 4.6 Write cost and storage

29 M interval rows/day is 335 rows/s and, through the MV, 20,000 exploded
rows/s into the rollup aggregation, which is modest. Storage estimate:
`rtt_us` is the cost driver. 60 values of a few thousand microseconds with
small variation compress to roughly 1 to 1.5 bytes each under T64 and ZSTD
(estimate from F25's statement on small ranges), so 60 to 90 bytes/row plus
about 5 bytes of scalars, 2 to 3 GB/day at 20k targets and one probe per
second. At one probe per 10 s the array holds 6 values and the row is about
15 bytes. The probe rate is the knob, not the row shape. The benchmark
measures both rates.

### 4.7 Failure modes

| Failure | Design answer |
| --- | --- |
| A target answers no probe for a day (baseline: 5% of APs) | rows with `received = 0` and empty arrays, cheap, and `intervals_lost` counts them |
| Edge clock skew | `interval_start` is the edge's clock; `Provenance.observed_at` is the same clock; the store records, the device service judges |
| Controller series arrives late and out of order | key includes `interval_start`, so order of arrival is irrelevant |
| Per-minute percentile averaged into a daily one | prevented by storing raw RTTs and a mergeable Timing state |

## 5. Cellular

### 5.1 Data semantics

| Field | Kind | Change rate |
| --- | --- | --- |
| `interface_name` (`cellular_interface.proto:16`) | key | static |
| `technology`, `band` (`:21`, `:24`) | state | rare (fixed routers), can flap at cell edge |
| `imei`, `imsi`, `iccid`, `mcc`, `mnc` (`:26-38`) | identity | change on SIM or modem swap; IMSI and ICCID "identify a subscription, and through it a person" (`spec/proto/flowseer/net/cellular/v1/README.md`, Identifiers) |
| `cell_id`, `tracking_area_code`, `physical_cell_id` (`:40-44`) | state | handovers; for fixed gear rare |
| `rsrp_millidbm`, `rsrq_millidb`, `rssi_millidbm`, `sinr_millidb` (`cellular_signal.proto:16`, `:22`, `:28`, `:31`) | gauges with bounds in milli-units | every poll |

### 5.2 Recommendation

Samples for signal plus serving-cell and technology columns on the row
(constant runs compress to nothing, and "which cell at T" is one granule),
and a changes table for identity and serving-cell transitions. Identity
columns do not go on sample rows: 288 copies a day of an IMSI across a
year of retention is needless personal data; one changes row per swap is
the minimum.

```sql
CREATE TABLE flowseer.cellular_samples
(
    tenant_id        LowCardinality(String),
    device_id        UUID,
    interface_name   LowCardinality(String),
    ts               DateTime            CODEC(Delta, ZSTD(1)),
    technology       LowCardinality(String),                    -- cellular_interface.proto:21 (enum name)
    band             UInt16              CODEC(T64, ZSTD(1)),   -- :24
    cell_id          UInt64              CODEC(T64, ZSTD(1)),   -- :40
    tracking_area_code UInt32            CODEC(T64, ZSTD(1)),   -- :42
    physical_cell_id UInt16              CODEC(T64, ZSTD(1)),   -- :44
    rsrp_mdbm        Int32               CODEC(T64, ZSTD(1)),   -- cellular_signal.proto:16
    rsrq_mdb         Int32               CODEC(T64, ZSTD(1)),   -- :22
    rssi_mdbm        Int32               CODEC(T64, ZSTD(1)),   -- :28
    sinr_mdb         Int32               CODEC(T64, ZSTD(1)),   -- :31
    present_mask     UInt16              CODEC(T64, ZSTD(1)),
    retention_class  Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    site_id          LowCardinality(String)
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY toMonday(ts)
ORDER BY (tenant_id, device_id, interface_name, ts)
TTL ts + INTERVAL 90 DAY DELETE WHERE retention_class = 'short',
    ts + INTERVAL 1 YEAR DELETE WHERE retention_class = 'standard',
    ts + INTERVAL 2 YEAR DELETE WHERE retention_class = 'long'
SETTINGS ttl_only_drop_parts = 1, min_age_to_force_merge_seconds = 604800,
         min_age_to_force_merge_on_partition_only = 1;
```

`cellular_changes` follows 02, 12.2 with `attribute Enum8('imei' = 1,
'imsi' = 2, 'iccid' = 3, 'mcc_mnc' = 4, 'technology' = 5, 'band' = 6,
'cell' = 7)`, `old_value`, `new_value` as `String`, keyed
`(tenant_id, device_id, interface_name, ts, record_id)`, TTL per the
privacy rules of `docs/conventions/observability.md` for the identity
attributes (a `DELETE WHERE attribute IN ('imsi', 'iccid')` rule with a
shorter interval, F36). Rollup `cellular_hourly`: `min`, `max`, `avgState`
of the four signal gauges and `argMaxState(cell_id, ts)`.

Queries and costs follow the PoE table with E = 1 to 2 interfaces per
device, so the site top-N worst RSRP over 24 h reads D x 2 x 288 rows and
the 90 day trend reads 2,160 rollup rows per interface. Meraki reports
`signalStat.rsrp`, `rsrq` per uplink alongside status
(`https://developer.cisco.com/meraki/api-v1/get-organization-uplinks-statuses/`),
so a controller-sourced row has `present_mask` without RSSI and SINR.
Storage: four gauges at 1 to 2 bytes, about 8 bytes/row, negligible.

## 6. Planned sources without schema

The rule for every item: name the pattern it will use and the trigger that
turns the reservation into DDL. No DDL until the trigger fires.

### 6.1 Flow records (sFlow, NetFlow, IPFIX)

**G28. Reserve a separate database now and plan a separate cluster at a
measured threshold.** `spec/proto/flowseer/net/flow/v1/README.md:14-17`
keeps decoding "on a different plane from the device configuration". The
history store should still state the model so the plane has a target.

Shape (Akvorado as the reference, 03 section Akvorado): a raw `flows`
table ordered by a five-minute bucket, exporter, in and out interface, with
TTL per tier and `ttl_only_drop_parts`; `SummingMergeTree` rollups per
resolution, where "an aggregate function sum() and GROUP BY clause should
be used in a query" because summation happens on merge
(`https://clickhouse.com/docs/engines/table-engines/mergetree-family/summingmergetree.md`);
high-cardinality columns (addresses, ports) in the raw table only
(Akvorado `ClickHouseMainOnly`). FlowSeer's adjustments: `tenant_id` first
in every key, `device_id` instead of `ExporterAddress`, and the rollup
TTL ladder as retention classes.

Why a separate database `flowseer_flows` from day one: a flow table's
volume scales with traffic and sampling rate, not with devices x interval.
One busy exporter sampled 1:1000 at 10 Gbit/s can produce more rows per day
than every gauge table in this lane combined, and Akvorado sizes its whole
ClickHouse around flows ("ClickHouse is tuned for 32 GB of RAM or more",
`https://raw.githubusercontent.com/akvorado/akvorado/main/console/data/docs/13-operating.md`).
A database boundary costs nothing, keeps the migration tool's per-domain
DDL separate, and makes a later move to its own cluster a `BACKUP`/`RESTORE`
of one database. It does not isolate resources: merges share
`background_pool_size` and the page cache, and workloads are per query user
(F56), so flows would compete with gauge inserts for merge threads and
disk. The cluster split is therefore the real isolation and needs a
trigger.

Trigger for the database and DDL: the first flow collector adapter and its
`IngestRecord` arm. Trigger for a separate cluster: projected flow rows/day
exceeding the sum of all other domains (about 100 M/day at 20k devices
across both lanes), or `system.parts` for `flowseer_flows` holding more
than half of the active parts of the node. Until a flow adapter exists,
nothing is created.

### 6.2 VPN tunnel status

`TunnelInterface` "carries no fields"
(`spec/proto/flowseer/net/interface/v1/tunnel_interface.proto:5-8`), and
tunnel status ownership is open
(`docs/research/schema-building-blocks/07-qos-security-ops-wan.md:355-358`,
`:437`). Reserved pattern: changes (`uplink_changes`, shared with 6.3) for
up, down, peer, and rekey transitions keyed `(tenant_id, device_id,
interface_name, ts, record_id)`, and the sibling lane's `interface_samples`
for tunnel counters because a tunnel is an interface. Trigger: a primitive
with a tunnel status enum lands in `net/`.

### 6.3 SD-WAN uplink and path status

Meraki reports per uplink `status` in `active, connecting, failed, not
connected, ready`, `ip`, `gateway`, `publicIp`, DNS, and cellular
`signalStat`
(`https://developer.cisco.com/meraki/api-v1/get-organization-uplinks-statuses/`;
dossier 07 at `:149-150`). Reserved pattern: changes (`uplink_changes`,
attributes `status`, `ip`, `gateway`, `public_ip`, `dns`) because a
controller poll every 2 to 3 minutes repeats an unchanged state, and the
path quality of the uplink already lands in `probe_intervals` and the
signal in `cellular_samples`. Trigger: an uplink status primitive in
`net/wan` or wherever the planner puts it.

### 6.4 Discovery sightings

Discovery "emit[s] candidates on the same event channel" from seeds,
neighbour crawl, tables, and sweeps
(`docs/architecture/2026-08-20-device-service-and-inventory-direction.md:107-118`).
A sighting is a set membership observed by a method: "this IP answered
ICMP in sweep S", "this chassis id appeared in LLDP on switch X". Reserved
pattern: presence, `sightings (tenant_id, method Enum8, sighting_key
String, bucket Date) min(first_seen), max(last_seen), anyLast(source
device_id), anyLast(evidence)` in an AggregatingMergeTree, idempotent
under redelivery by construction (03, set-valued domains). Trigger: the
discovery plane's candidate record type.

### 6.5 Webhook and controller stream payloads

These are sources, not domains. The ingestion record says "the bus carries
typed records only" and "Central registers one consumer per record type and
none per integration kind"
(`docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md:36-44`).
A Meraki webhook or a Mist stream message (`docs/research/device-inventory/README.md:138-140`)
is mapped by its adapter into a typed record and lands in that record's
table. The history store reserves no table per source. What it reserves is
the events pattern for the alarm-shaped payloads (an alert raised or
cleared, keyed `(tenant_id, device_id, ts, record_id)` with typed severity
and kind, the syslog table's shape without the text index), which the
sibling lanes' event domain or the first alert record type makes concrete.

## 7. Cross-domain conclusion: the patterns the whole store needs

Five patterns cover every domain seen in this lane and in 02 and 03. A new
domain picks one per write shape and does not invent a sixth.

| Pattern | Engine | Key shape | Dedup | Rollup | Retention | Domains |
| --- | --- | --- | --- | --- | --- | --- |
| Samples: a periodic row per entity per poll, state columns ride along | ReplicatedReplacingMergeTree, weekly partitions | `(tenant_id, device_id, entity..., ts)` | natural key, no `record_id` (F45) | hourly AggregatingMergeTree with `min`, `max`, `avgState`, `argMaxState`, `deltaSumTimestampState` for counters | 90 d / 1 y / 2 y classes, rollup 3 y | interface counters, CPU, sensors, PoE, PSE, optics lanes, cellular signal, controller path quality |
| Changes: one row per transition of one attribute | ReplicatedReplacingMergeTree, weekly partitions | `(tenant_id, device_id, entity..., ts, record_id)` | `record_id` in key | none | 1 to 2 y, shorter for personal data | interface status, PoE status, module identity, SIM identity, tunnel and uplink status |
| Presence: one row per member per bucket with first and last seen | ReplicatedAggregatingMergeTree | `(tenant_id, device_id or method, member_key, bucket)` | idempotent `min`/`max` | the table is its own rollup | 180 d to 1 y | FDB, neighbours, clients, discovery sightings |
| Events: an append-only row per message | ReplicatedReplacingMergeTree, daily partitions, optional text index | `(tenant_id, device_id, ts, record_id)` | `record_id` in key | counts per hour by severity if needed | 30 to 90 d | syslog, traps, alerts, webhook alarms |
| Probes: one row per interval holding the raw measurement list, sketches by MV | ReplicatedReplacingMergeTree raw, ReplicatedAggregatingMergeTree hourly with `quantilesTimingState` | `(tenant_id, device_id, interface_name, target_ip, interval_start)` | natural key | hourly sketch, `sum`, `min`, `max`, lost intervals | 30 to 90 d raw, 2 y rollup | ICMP probes, any future active measurement (DNS, HTTP, TWAMP) |

Three rules hold across all five, and each new table states which it
follows: the entity column is the stable device-local key (interface name,
lane, PSE group, target address), never an index that resets (G1); a state
that changes rarely lives on the samples row and in the changes table, so
"what at T" and "when did it change" each cost one key range; and the
rollup is always a separate table fed by an incremental MV, never a TTL
GROUP BY on the raw table (F34).

Flows stand outside the five. They are a samples-like fact table whose
volume is traffic-driven, which is exactly why they get their own database
and, past the trigger, their own cluster (G28).

## 8. Predictable scaling summary for lane G

| Query shape | Cost formula (rows read) | Grows with total data? | Bound |
| --- | --- | --- | --- |
| tenant + device + range, newest first | E x W/P, reverse key order with LIMIT | no | primary key |
| tenant + site, last 24 h | D x E x 24 h/P, D key ranges | no | `device_id IN` ranges; above about 5,000 devices use the hourly rollup |
| hourly or daily charts over weeks to months | entities x hours from the rollup | no | rollup table |
| top-N within a scope | same as site scope, `LIMIT` after `GROUP BY` | no | `max_rows_to_read` on the query user (F56) |
| what was it at T | one granule per entity | no | primary key |
| when did X change | change rows of one entity | no | primary key |
| cross-device by member key (module serial) | bloom filter granules in `optics_module_changes` within the tenant | weakly: granules of the tenant's parts the filter cannot skip | measured with `EXPLAIN indexes=1`; the tenant prefix bounds it |
| tenant-wide "worst this hour" from a rollup | all entities of the tenant for one hour | with tenant size, not total data | `minmax` on `hour`, verified by the benchmark |

Write cost for the lane: about 72 M rows/day, 830 rows/s, one insert per
table every few seconds, one or two parts per insert, plus the probe MV's
exploded rows. Storage: roughly 0.4 GB/day for all samples and changes and
2 to 3 GB/day for probe intervals at one probe per second, or 0.5 GB/day
at one per 10 seconds. All estimates, measured in section 9.

## 9. Benchmark design

Target: local single-node ClickHouse 26.8 container, the DDL of sections
2 to 5 unchanged except `Replicated` prefixes dropped.

### 9.1 Generator

One Go or Python generator writing native-format batches through the same
client the sink will use. Parameters, with the defaults for the 1x step:

| Parameter | Default | Note |
| --- | --- | --- |
| seed | 20261008 | every value process is a seeded PRNG stream per entity |
| tenants | 200, Zipf: 1 tenant with 10,000 devices, 10 with 1,000, the rest with 5 to 50 | matches "most small, a few with ~10,000" |
| devices | 20,000 total, 9% switches with PoE and cages, 10% appliances with uplinks, 10% cellular | G3 |
| PoE ports per switch | 48, 60% delivering power | draw: plateau per port from {3, 6, 12, 25, 60} W with 2% Gaussian noise, class fixed; status flap rate 0.5 per port per day; counters increment Poisson 0.1 per port per day with a reset probability 0.01 per device per day |
| cages per switch | 4, 70% populated, lanes 1 (80%), 4 (20%) | rx power: per lane plateau in -3 to -12 dBm, drift -0.01 dB per day for 5% of lanes, noise 0.1 dB; thresholds fixed per module; module swap rate 0.002 per cage per day |
| probe targets | every device once from one edge; appliances also probe 2 targets per uplink | RTT: lognormal with median 6 ms, p99 20 ms (baseline:179); loss: 5% of targets fully dark for a day, 100 onset events per hour across the fleet with 30 min median duration (baseline:206-212); rate 1/s and 1/10 s variants |
| cellular interfaces | 2,000 | RSRP random walk inside -110 to -80 dBm, handover 0.1 per day |
| poll intervals | 300 s switches, 150 s controllers, probe interval rows 60 s | |
| days | 30 at 1x | |
| duplicate injection | 0.1% of rows re-sent 15 minutes later in a new batch, 0.01% re-sent after 2 hours | exercises F48 |

### 9.2 Scale steps

| Step | Data | Scope held fixed | Expected |
| --- | --- | --- | --- |
| S1 | 1x (20k devices, 30 days) | one device, one site of 2,000 devices, one tenant of 10,000 | baseline numbers |
| S2 | 4x by 4x days (120 days) | same | `read_rows` flat for 24 h queries, rollup queries grow linearly with days only where the window grows |
| S3 | 16x by 4x days and 4x tenants (800 tenants, 80k devices) | same scope, same tenant | `read_rows` flat within 2x of S1 for every per-device, per-site, and at-T query |
| S4 | fixed 1x data, scope grows 100, 1,000, 2,000, 10,000 devices | | `read_rows` linear in D with slope E x 24 h/P |

### 9.3 Measurements

- Inserts: rows/s and parts per insert from `system.part_log` (`NewPart`
  events per insert, F67), and merges per hour for the probe rollup.
- Storage: `data_compressed_bytes / rows` per column from `system.columns`
  (F25), especially `rtt_us`, the optics threshold columns, and the T64
  versus Gorilla alternatives for `power_draw_nw` and `rsrp_mdbm`.
- Queries: for each query in sections 2.5, 3.4, 4.5, and 5: `read_rows`,
  `read_bytes`, `memory_usage`, `query_duration_ms` p50 and p95 over 20
  runs from `system.query_log`, granules from `EXPLAIN indexes = 1`,
  with the query condition cache and query cache off.
- MV correctness: `quantilesTimingMerge` on the rollup against
  `quantileTiming` over the raw arrays for the same hour, difference under
  1 ms below 1 s.
- Dedup: count of surviving duplicates after `OPTIMIZE` of closed
  partitions, and the error they introduce into `sum(received)` per hour.

### 9.4 Pass criteria

| Query | Criterion |
| --- | --- |
| every per-device and at-T query | `read_rows` within 2x of the formula and flat (within 10%) across S1 to S3 |
| site scope 24 h | `read_rows` within 2x of D x E x 24 h/P, linear in D across S4, flat across S1 to S3 |
| rollup charts 90 d | `read_rows` within 2x of entities x 2,160, p95 under 200 ms |
| tenant-wide worst-this-hour | granules read under 2x the tenant's rows for that hour divided by 8,192 after the `minmax` index; otherwise the index is dropped and the query moves to a per-tenant hourly summary |
| optics threshold columns | under 1 byte/row combined, else fall back to model C (3.2) |
| `rtt_us` | under 1.5 bytes per value at 1/s; the probe rate recommendation follows from the measurement |
| inserts | one part per insert per partition, no `parts_to_delay_insert` events in `system.events` |
| duplicates | zero visible after forced merge of closed partitions; hourly `sent` error under 0.2% before merge |

## 10. Schema gaps

The tables above are built from fields that exist today. The history the
lane needs is wider. Each row names the missing field, message, or carrier,
the proposed addition, and the evidence. "Carrier" means the record that
embeds a primitive and supplies device, interface, and time.

### 10.1 Carriers and keys

| Gap | Proposed addition | Evidence |
| --- | --- | --- |
| No record embeds `PseBudget`, `PathQuality`, or `CellularInterface` (G2) | A device-level state record carrying `repeated PseBudget pse_groups`; an uplink or interface state record carrying `CellularInterface` and `PathQuality` per target | grep over `spec/proto` 2026-10-08; `spec/proto/flowseer/net/phy/v1/README.md` ("stands on its own until an entity embeds it"); `spec/proto/flowseer/net/cellular/v1/README.md` ("Imported by: nothing") |
| `PathQuality` names no target and no probe count | `target_ip` beside the message in its carrier; see 10.3 for the probe record | Meraki returns one series per `uplink` and destination `ip` (`https://developer.cisco.com/meraki/api-v1/get-organization-devices-uplinks-loss-and-latency/`) |
| No `IngestRecord` arm for anything but syslog | one arm per record type above, as the ingestion record requires | `spec/proto/flowseer/integration/ingest/v1/*.proto:25-30`; `docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md:36-44` |
| PoE row to interface join rule is not represented; the MIB key is group plus port, "no portable mapping" | keep `pse_group`, `pse_port` on the sample row (done above) and add an explicit `join_source Enum` (`entAliasMapping`, `vendor_rule`, `ifindex_native`, `unjoined`) on `PoePortDetail` so history records how the row was attributed | `docs/research/network-domain-atlas/entities/02-interface.md:291-320` ("Joining PoE rows to interfaces requires a per-vendor rule and is a classic source of off-by-one bugs"); `00-methodology-and-lineages.md:225` ("no portable mapping"); atlas FlowSeer status for PoE ("no representation of the join rule") |

### 10.2 PoE

| Gap | Proposed addition | Evidence |
| --- | --- | --- |
| No 802.3bt type (Type 3/4, 4-pair, up to 90 W) | `PoeFacet.pair_mode Enum {UNSPECIFIED, TWO_PAIR, FOUR_PAIR}` and `powered_device_type Enum {TYPE_1..TYPE_4}`; sample column `pair_mode Enum8` | atlas `02-interface.md`: "802.3bt ... broke the single-pair model. Aruba added a whole parallel table; Cisco added spare-pair oper data. RFC 3621's `pethPsePortPowerClassifications` only enumerates classes 0-4"; `arubaWiredPoePethPseFourPairPortTable`, `Cisco-IOS-XE-poe-health-oper spare-pair-info` |
| Only two of the three power numbers are carried (allocated, drawn); the PD's request is missing | `PoeFacet.requested_power_nanowatts` from LLDP-MED `lldpXMedRemXPoEPDTable`; sample column `requested_nw UInt64` | atlas: "Three power numbers get conflated: what the PD requested (LLDP-MED), what the PSE allocated (vendor), and what is actually drawn" |
| No PoE priority on the facet (intent only) | `PoeFacet.priority PoePriority` as the applied value, since `PoeSettings` is intent | `poe_priority.proto` exists; `pethPsePortPowerPriority` is a MIB object and vendors report the effective priority (atlas vendor table) |
| No PSE group identity beyond an index (which stack member, which module) | `PseBudget.component_name` or a component ref resolved by the mapper; sample column `pse_component LowCardinality(String)` | atlas: "The group-unit-stack-member mapping is unspecified ... There is no portable join"; `04-gaps-and-recommendations.md:51` (PoE budgets key on `entPhysicalIndex`) |
| No per-group fault and power-supply detail (`hpicfPoePowerSupplyTable`, `rlPhdPoeTable`) | defer; `oper_status = FAULTY` on `PseBudget` covers the history question | atlas HP and Cisco SMB rows |
| PD-side view: an AP reports whether it is under-powered | `PoeFacet` on the PD role gains `under_powered bool` and `power_source Enum {POE, DC}` | `spec/proto/ruckus/ap/ap_status.proto:2058` (`poeMode`, "8023af PoE power source"), `:2093` (`poeUnderPowered`), `:1661` ("Brown out power. It could be PoE or 12VDC power supply"); baseline lesson that controller and switch views disagree |
| Counters have no discontinuity marker (G4) | a device-level `sysUpTime` epoch on the carrying state record, stamped onto every sample row of that poll | `docs/architecture/2026-09-25-schema-building-blocks-direction.md:137-144` |

### 10.3 Probes and path quality

| Gap | Proposed addition | Evidence |
| --- | --- | --- |
| No probe result record at all | `flowseer.net.measure.v1.ProbeInterval { target address, interval_start, interval Duration, sent uint32, received uint32, repeated uint32 rtt_microseconds, PathQuality summary }` as a record type with `Provenance`; the `prober` (edge, device, controller) comes from provenance | RFC 4560's `pingCtlTable` / `pingResultsTable` / `pingProbeHistoryTable` triple is "the canonical SNMP shape for 'a long-running operation with results'" (atlas `04-ip.md:439-470`); Comware `hh3cNqaJitterStatsTable`, Cisco IP SLA, LANCOM `lcsStatusSlaMonitorIcmpHistoryLogTable` all expose per-probe history |
| `PathQuality` has no probe count or loss count, so loss cannot be re-aggregated | `sent`, `received` on `ProbeInterval` (above); keep `loss_basis_points` for summary-only sources | Ruckus reports `PingLatency` as an average and `PingLossCount` as a count (`spec/proto/ruckus/ap/ap_report.proto:2396`, `:2403`); Meraki reports `lossPercent` only |
| No one-way or per-direction latency for device-run SLA probes | defer; round-trip is what every targeted source reports | `path_quality.proto:13` ("latency here is round-trip too"); RFC 3393 defines one-way delay variation, which no targeted source exports |
| Device-run probe definitions (owner, test name, frequency) have no carrier | a `ProbeDefinition` settings primitive keyed by owner and name, when FlowSeer configures device-side probes; not needed for edge probes | atlas: "An owner-plus-name key, a definition table, a results table, and a history table ... worth adopting as FlowSeer's operation pattern" |
| Heartbeat and tunnel keepalive latency from controllers | map onto `ProbeInterval` with `prober = controller` and the controller as target | `ap_report.proto:2389` (`HeartbeatLatency`), `ap_status.proto:49-60` (`APStatusTunnel.cICMP`, "Number of keepalive packets sent") |

### 10.4 Optics

| Gap | Proposed addition | Evidence |
| --- | --- | --- |
| No per-lane receive power kind (average versus OMA) | `ModuleLane.rx_power_kind Enum {AVERAGE, OMA}`; sample column `rx_kind Enum8` | SFF-8472 9.8: "Value can represent either average received power or OMA" (PDF text, `https://members.snia.org/document/dl/25916`) |
| No calibration flag; externally calibrated modules report raw A/D values unless the host applies constants | `PluggableModule.calibration Enum {INTERNAL, EXTERNAL}` so a mapper that could not apply the constants marks the row | SFF-8472 9.3 and 9.5: "If bit 5, 'Internally calibrated', is set, the transceiver directly reports calibrated values"; external calibration needs `Rx_PWR(4..0)`, `Tx_PWR(Slope)` |
| No laser temperature or TEC current for the optional cooled-laser monitors | `ModuleLane.laser_temperature Temperature`, `tec_current Current` | SFF-8472 Table 9-5 rows 40-55 ("Optional Laser Temp", "Optional TEC Current"); the proto reserves lane fields 3 to 5 (`module_lane.proto:14`) |
| No threshold provenance: D-Link thresholds are operator-configurable, others are module EEPROM | `PluggableModule.threshold_source Enum {MODULE, DEVICE_CONFIG}` so a margin query knows what it compares with | atlas `01-platform.md`: "A model that mixes them will compare an operator setting to a hardware limit"; FlowSeer status says the mapper uses module thresholds only, which the schema cannot assert |
| No alarm and warning flag bits as the module raises them | `ModuleLane.flags` bitmask (tx power high alarm, rx power low warning, ...) and module-level flags; sample column `flags UInt16` so "alarm at T" needs no threshold arithmetic | SFF-8472 9.9 "Alarm and Warning Flag Bits [Address A2h, Bytes 112, 113, 116, 117]"; OpenConfig `transceiver/state/fault-condition` (atlas) |
| No module uptime, FEC counters, or coherent telemetry | defer until a source in scope reports them | atlas: "any coherent module telemetry" listed as a remaining gap; no targeted source |

### 10.5 Cellular and WAN uplinks

| Gap | Proposed addition | Evidence |
| --- | --- | --- |
| No uplink status primitive (active, connecting, failed, not connected, ready) with addresses | `net/wan/v1 UplinkFacet { interface_name, status Enum, ip, gateway, public_ip, dns servers, provider, apn? }`, changes pattern (6.3) | Meraki `getOrganizationUplinksStatuses` fields (`https://developer.cisco.com/meraki/api-v1/get-organization-uplinks-statuses/`); dossier 07 `:149` ("core for gateway/appliance-class devices") |
| `CellularInterface` lacks provider name and APN; the cellular README deliberately omits APN | add `provider string`; keep APN out until a second source needs it | Meraki `signalStat`, `provider`, `apn`, `connectionType` on the uplink status; `net/cellular/v1/README.md` ("Deliberately absent: MSISDN, APN and bearer settings") |
| No high-availability role for appliance pairs | `UplinkFacet` carrier or device state gains `ha_enabled bool`, `ha_role Enum`; changes pattern | Meraki `highAvailability.enabled`, `.role` ("The HA role of the device on the network") |
| Signal lacks a timestamp of its own when the controller reports it late | `Provenance.observed_at` is the platform time, which suffices; no addition | `provenance.proto:15-18`; Meraki `lastReportedAt` maps onto `observed_at` |

### 10.6 Tunnels

| Gap | Proposed addition | Evidence |
| --- | --- | --- |
| `TunnelInterface` carries nothing; no tunnel state, peer, type, or re-establishment count | `TunnelInterface { kind Enum {GRE, IPSEC, WIREGUARD, SOFT_GRE, ...}, peer address, state Enum {ACTIVE, INACTIVE}, established_at Timestamp, reestablishments uint64 }`; changes pattern for state and peer, samples for the counter | `spec/proto/ruckus/ap/ap_status.proto:23-60`, `:139-146` (`APStatusTunnel`: gateway, type, `isActive`, uptime, `reEstablishment`); `ap_report.proto:1122-1129` (`IPsecTunnelType`); FortiGate monitor API exposes "VPN tunnel status" (`docs/research/device-inventory/targets/fortinet.md:139-140`); dossier 07 `:355-358` leaves ownership open |
| Split-tunnel traffic counters per AP | map onto the sibling lane's interface counters with the tunnel as the interface | `ap_report.proto:529-556` (`rxDataBytesSplitTunnel` and siblings) |

### 10.7 Flows

| Gap | Proposed addition | Evidence |
| --- | --- | --- |
| No decoded flow record type | reserve `flowseer.net.flow.v1 FlowRecord` on the decoding plane with the Akvorado column set (exporter device, in and out interface, addresses, ports, protocol, bytes, packets, sampling rate, AS and community fields optional) | `spec/proto/flowseer/net/flow/v1/README.md:12-17` says decoding is deliberately absent from the config package; 03 Akvorado `flows` DDL lists the columns a collector needs |

### 10.8 Discovery and events

| Gap | Proposed addition | Evidence |
| --- | --- | --- |
| No candidate or sighting record type | `Sighting { method Enum {SEED, NEIGHBOR_CRAWL, TABLE, SWEEP}, key (ip, mac, chassis id, serial), source device, evidence }` with `Provenance` | device record `:107-118` ("All emit candidates on the same event channel") |
| No alert or alarm record for webhook and stream payloads | an `event/alarm` record type with severity, kind, raised or cleared, source object; events pattern | `docs/research/device-inventory/README.md:138-140` (Meraki, Mist, Aruba webhooks; Ubiquiti alarm payload "undocumented"); `spec/proto/ruckus/sci/sci-alarm.proto` exists as a vendor shape |

## 11. Corrections and open points

1. 02 section 12.1 keys interface samples by `ifindex`; the stable key is
   the interface name (G1). The sibling lane and this one must agree.
2. `PoePortDetail` counters have no discontinuity marker (G4); the counter
   rule covers only `<Domain>Counters` messages. Either the PoE row borrows
   the device-level `discontinuity_epoch` from the interface samples or the
   rate query accepts a one-interval under-count on reset and wrap.
3. `PseBudget`, `PathQuality`, and `CellularInterface` are embedded by no
   record yet (G2); the carrying record decides the key columns assumed
   here.
4. No proto exists for an edge probe result; section 4.3 is the reserved
   shape (sent, received, RTT list per interval).
5. Unverified: Meraki time series point resolution; `ARRAY JOIN` inside an
   incremental MV on 26.8; `simpleLinearRegression` as an aggregate
   function name; whether a CMIS module can report more than 4.29 W per
   lane; T64 versus Gorilla on the gauges (benchmark decides).
6. Change rows need a producer that holds state (edge or state projector),
   which is a pipeline decision outside the store.
7. The flow cluster trigger (G28) needs a measured rows/day from the first
   flow adapter before the number is final.
