---
title: ClickHouse model for interface, device system, and protocol counter history
date: 2026-10-08
status: research report for the history store plan; read-only toward the repository
domains: interface status and counters, device system resources and sensors, protocol counters (BGP, BFD, VRRP, DHCP server, NAT)
---

# ClickHouse model for interface, device system, and protocol counter history

Builds on dossiers 01 to 03 under `docs/research/clickhouse-history-store/` and cites their findings by number (F1 to F74). Sources fetched 2026-10-08 are cited by URL with a quote. "Inference" marks reasoning from cited text. Findings here are numbered D1 to D22. Corrections to dossier 02 are marked **Correction**.

## 0. Findings in one page

| # | Finding | Basis |
| --- | --- | --- |
| D1 | One wide samples table per `<Domain>Counters` message family (interface, ethernet, BGP peer, BGP peer AFI, BFD session, VRRP group, DHCP server message type), all with the same key shape `(tenant_id, device_id, <entity key>, ts)`. Not one narrow `(metric_id, value)` table. | Section 2, F3 to F6, 03 "table per domain" |
| D2 | The entity key for an interface is `interface_name`, not `if_index`. `Interface.if_index` is optional and "not stable across restarts" (`spec/proto/flowseer/net/interface/v1/interface.proto:33-35`). **Correction** to 02 section 12.1, which keys on `ifindex`. | Section 1.1 |
| D3 | Counter columns are named after the schema (`in_bytes`, not `in_octets`), with a `present_mask` bit per field because "an absent counter means the source does not report it, never a zero" (`interface_counters.proto:9`). No Nullable (F9). **Correction** to 02 section 12.1 column names. | Section 3.1 |
| D4 | Status fields (admin, oper, MTU, description, speed, duplex, `last_change`) ride on every counter sample row. Sorted per entity they compress to near zero, they make "what was it at T" a one-granule read, and `last_change` (ifLastChange) is the device's own oper-status change timestamp. A separate changes table is still the index for "when did X change" and scope-wide flap counts, and it needs a producer that diffs (section 3.2). | Section 3.1, 3.2 |
| D5 | Discontinuity is stored as `discontinuity_at DateTime` (from `last_discontinuity`, 0 when unreported) on every sample and is part of the rollup GROUP BY. RFC 2863 defines it as "The value of sysUpTime on the most recent occasion at which any one or more of this interface's counters suffered a discontinuity". | Section 1.4, 4 |
| D6 | Rollups use only idempotent aggregates: `argMin`/`argMax` of the value by `ts`, `min`, `max`, `uniqExact(ts)` for the sample count. A redelivered row past the dedup window merges to the same state. `deltaSumTimestamp` is also safe under exact duplicates (its merge ignores an overlapping state's sum, section 4.3) but it silently drops a genuinely interleaved state, so it is a secondary column, not the basis. | Section 4.3 |
| D7 | Exact increase per bucket from the rollup: within one discontinuity epoch `last - first`; across epochs add the next epoch's `first` only when it is below the previous epoch's `last` (a reset counted from zero), otherwise `first_next - last_prev`. This matches Prometheus semantics ("Breaks in monotonicity ... are automatically adjusted for") without ever producing a spike from a spurious epoch split. | Section 4.4 |
| D8 | 32-bit wrap is undetectable from the schema: `InterfaceCounters` has no width field and "a source with both 32-bit and 64-bit columns reports the 64-bit value here" (`interface_counters.proto:13-14`). A wrap on a 32-bit-only source looks like a reset. Schema gap for the plan: a counter-width field, or a mapper rule that drops 32-bit octet counters on links faster than the wrap-safe speed. | Section 1.4 |
| D9 | Two hourly rollup orderings: entity-first `(tenant, device, entity, hour)` for long per-entity charts, and time-first `(tenant, hour, device, entity)` for scope queries. Both are MV targets fed from raw (no chained MVs, 03 SigNoz "too many parts"). Scope cost then is `24 x entities_in_tenant` rows for 24 h, independent of total data. | Section 5.3, 6 |
| D10 | The raw table's granule (8192 rows, one entity per granule after sorting) makes a per-entity 24 h read cost 8192 to 16384 rows for 288 needed rows. For a single entity that is one millisecond. For a 2,000-device scope on raw it is 96,000 x 16,384 = 1.6 G rows, so scope queries never run on raw. The benchmark measures `index_granularity` 8192 vs 2048 for raw. | Section 6.1 |
| D11 | Top-N by utilisation over 24 h in a 2,000-device scope reads 2.3 M rollup rows (time-first table), ranks by `increase * 8 / (seconds * speed)`, and excludes `speed = 0` ports (about a quarter of up ports report speed 0, baseline). | Section 5.4 |
| D12 | Storage: 276 M interface counter rows/day at 20,000 devices x 48 interfaces x 5 min. Estimated 15 to 40 bytes/row raw with `Delta, ZSTD(1)` (idle ports at the low end). The benchmark measures it per column. | Section 7 |
| D13 | Device system: three tables. `device_samples` (uptime, whole-box CPU), `storage_samples` (device-level rows from `DeviceState.storage_utilization` and component-level from `ComponentState.storage_utilization`, tagged by source), `component_samples` (one row per component per poll holding the at-most-one reading per quantity that `component.proto:106-115` guarantees, plus oper status and CPU utilisation). | Section 3.3 |
| D14 | Sensor thresholds (`high_alarm_...` etc.) are configuration, not samples. They go to the component changes table, not to every sample row. | Section 3.3 |
| D15 | Uptime is stored as `uptime_seconds` per sample. A decrease is not a reboot ("a reboot is not inferred from it", `device.proto:206-209`). "When did it reboot" is a changes-table question answered by the state projector, not by a samples scan. | Section 3.3 |
| D16 | Generalisation test passes for BGP peer, BGP AFI, BFD, VRRP, DHCP server: each has a stable device-local key and a `<Domain>Counters` message, so the same template applies with the key columns swapped. It fails for `NatSession`: the entity is a 5-tuple that lives minutes, so it is a set-valued domain (presence interval per session with last counters), not a samples series. | Section 3.4 |
| D17 | A generic narrow numeric table for "all other counters" is not needed: every counter in the schema is typed. Where it would be used, it costs 12x the rows, about 3x the bytes, and 12 index range reads per chart, and it still needs per-metric rate partitions. It scales predictably but worse, and it reintroduces the generic-table regrets of 03 (ntopng, Telegraf). | Section 2.3 |
| D18 | Dedup: raw tables are `ReplicatedReplacingMergeTree` on the natural key `(tenant_id, device_id, entity, ts)` with `ts = provenance.observed_at`. A redelivery is byte-identical and collapses at merge (F45). Readers of raw tolerate a duplicate (same value, same ts, zero delta). Rollups are idempotent by D6. | Section 4 |
| D19 | Partition `toMonday(ts)` on raw, `toYYYYMM(hour)` on hourly, `toYear(day)` on daily. Retention classes raw 90 d / 1 y, hourly 2 y, daily 5 y as `DELETE WHERE retention_class` rules (F36). | Section 3 |
| D20 | Per-port cardinality is a non-issue for the key: 500-interface chassis means 500 contiguous key runs per device. The only cardinality risk is `LowCardinality(String)` for `interface_name` if the global distinct count passes 100,000 (F10). The benchmark measures it; the fallback is a `UInt64` name hash in the key with the name as payload. | Section 1.5 |
| D21 | Status enums are stored as `UInt8` holding the protobuf number, not `Enum8`, because the schema says "Unknown non-zero values stay valid" (`interface.proto:36-38`) and an `Enum8` insert of an undefined value fails. Presence of the status rides in `present_mask`. | Section 3.1 |
| D22 | The time-first rollup is the bounded answer to every scope query. Any query on raw whose scope is a device list is bounded by `devices x entities x 16384` rows and must carry a `max_rows_to_read` limit from the query profile (F56). | Section 6 |

## 1. Data semantics

### 1.1 Interfaces

Entity. `Interface.name` is required and is "the device-local interface name, as the device spells it" (`spec/proto/flowseer/net/interface/v1/interface.proto:27-32`). `if_index` is optional: "Absent means the source exposes no index or did not report one; the index is not stable across restarts" (`:33-35`). The entity key for history is therefore the name. `if_index` is payload. Sub-interfaces and VLAN interfaces are kinds of the same message (`:66-85`) with their own names, so they are ordinary entities, and `Subinterface.parent_interface_name` (`subinterface.proto:14-17`) and `VlanInterface.vlan_id` (`vlan_interface.proto:13-16`) are payload columns.

Status (slow-changing). `admin_status` and `oper_status` (`interface.proto:36-43`), `mtu` (`:44-46`), `description` (`:50-53`), `last_change` ("ifLastChange in RFC 2863, resolved against the device's uptime by the source. A zero ifLastChange means the state predates the agent's last restart and is reported as absent", `:57-62`), and for physical ports `EthernetFacet.active_speed_bps` and `active_duplex` (`spec/proto/flowseer/net/phy/v1/ethernet_facet.proto:44-47`). RFC 2863 on ifLastChange: "If the current state was entered prior to the last re-initialization of the local network management subsystem, then this object contains a zero value" (`https://www.rfc-editor.org/rfc/rfc2863.txt`, section 6).

Counters (monotonic). `InterfaceCounters` has 12 `uint64` counters and `last_discontinuity` (`interface_counters.proto:15-49`). "Each counter is the value the source last reported, and an absent counter means the source does not report it, never a zero. Counters are monotonic between device restarts; consumers difference successive observations and treat a decrease as a reset" (`:8-11`). `EthernetCounters` has 12 more `uint64` counters and its own `last_discontinuity` (`ethernet_counters.proto:16-50`), lives inside `EthernetFacet.counters` (`ethernet_facet.proto:89`), and so exists only for physical ports. RFC 3635 says of the collision family "This counter does not increment when the interface is operating in full-duplex mode" (dot3StatsSingleCollisionFrames and six siblings, `https://www.rfc-editor.org/rfc/rfc3635.txt`, section 4), so on a modern switch most of these twelve are constant zero. 64-bit variants: "Entries in this table are recommended for interfaces capable of operating at 1000 Mb/s or faster, and are required for interfaces capable of operating at 10 Gb/s or faster" (dot3HCStatsTable, same page).

Cardinality per device. Baseline: "about 850 to 880 [switches], about 44,000 interfaces" (`docs/research/2026-10-01-production-monitoring-baseline.md:83`), that is about 50 per switch. Task framing: 48-port access switches, chassis with 500+, plus sub-interfaces and SVIs. Planning figure: 48 per device average, 20,000 devices, so 960,000 interface entities per node.

Poll interval. "switch SNMP 5 min" (baseline `:245`). Links are idle: "Median switch traffic is a few Mbit/s, the busiest port a few hundred Mbit/s. A 1 to 5 minute counter interval is adequate" (`:235-236`).

### 1.2 Device system resources

`DeviceState` carries `uptime` ("measured relative to the envelope's observation time. In SNMP, sysUpTime wraps at about 497 days and can reset on an agent restart; a reboot is not inferred from it", `spec/proto/flowseer/model/inventory/v1/device.proto:206-210`), `processor_utilization` ("Whole-box processor utilization when the device reports utilization as an aggregate rather than per-CPU component", `:211-213`), and `repeated storage_utilization` ("Whole-box storage utilization rows", `:214-216`, each with a required unique `name`, `:122-132`). `ProcessorUtilization` is `utilization_avg_basis_points` plus an optional averaging `window` (`spec/proto/flowseer/net/system/v1/processor_utilization.proto:10-19`). `StorageUtilization` is `name`, `kind`, `total_bytes`, `used_bytes`, `used_basis_points` (`storage_utilization.proto:10-34`).

`ComponentState` is keyed by `ComponentLocalRef.name` within a device (`component.proto:23-40`), has a `kind` (chassis, PSU, fan, sensor, module, port, CPU, storage, transceiver, radio, `:45-70`), `oper_status` (`:196`), `repeated SensorReading sensors` with "at most one sensor reading per quantity" (`:106-115`), `processor_utilization` only on a CPU (`:117-120`), `storage_utilization` only on storage and without a name (`:122-130`). Each sensor quantity carries a value and four thresholds (`spec/proto/flowseer/net/measure/v1/sensor.proto:11-172`). Values are gauges with noise (temperature, voltage, current, power, rpm, humidity). Thresholds are configuration.

sysUpTime: "The time (in hundredths of a second) since the network management portion of the system was last re-initialized" (`https://www.rfc-editor.org/rfc/rfc3418.txt`). 2^32 hundredths is 497.1 days (inference from the type width).

Cardinality: one `DeviceState` per device per poll. Components: a 48-port switch reports typically 1 chassis, 1 to 2 PSUs, 2 to 4 fans, a few temperature sensors, 1 CPU, 1 to 3 storage, plus one transceiver per optic. Planning figure: 20 components per device, 400,000 component entities per node. Interval: 5 min, or 10 min for sensors (a choice for the plan).

### 1.3 Protocol counters

| Message | Entity key (device-local) | Counters | Discontinuity | Per device |
| --- | --- | --- | --- | --- |
| `BgpPeer` (`net/protocol/bgp/v1/bgp_peer.proto:15-87`) | `network_instance`, `protocol_instance`, `remote_address` | `BgpPeerCounters`: 5 (`:122-137`), state, `established_time` | `last_discontinuity` with "BGP4-MIB has no per-peer discontinuity object" (`:133-136`) | routers only, 1 to hundreds |
| `BgpPeerAddressFamily` (`:90-119`) | peer key + `afi`, `safi` | `received_prefixes`, `sent_prefixes`, `installed_prefixes` are gauges (uint32) | none | 1 to 4 per peer |
| `BfdSession` (`net/protocol/bfd/v1/bfd_session.proto:15-92`) | `network_instance`, `local_discriminator` | `BfdSessionCounters`: 3 (`:95-104`), state, remote_state, diagnostics | `last_discontinuity` | 0 to tens |
| `VrrpGroup` (`net/protocol/vrrp/v1/vrrp_group.proto:14-104`) | `interface_name`, `address_family`, `vrid` | `VrrpGroupCounters`: 2 (`:107-114`), state, priority, master_address | `last_discontinuity` | 0 to tens |
| `Dhcpv4ServerCounters` / `Dhcpv6ServerCounters` (`net/protocol/dhcp/v1/dhcpv4_server_counters.proto:10-39`, `dhcpv6_server_counters.proto:10-39`) | device + `message_type` (unique per message, `:11-15`) | `received_messages`, `sent_messages` | one `last_discontinuity` per message set | about 8 to 13 message types |
| `Dhcpv4Pool` (`dhcpv4_pool.proto:11-56`) | `network_instance`, `name` | `total_addresses`, `leased_addresses` gauges | none | tens |
| `NatSession` (`net/nat/v1/nat_session.proto:14-39`) | `network_instance`, protocol, private source, public source (a 5-tuple) | `NatSessionCounters`: 4 (`:50-61`), `expires_in` | `last_discontinuity` | thousands, each living minutes |

### 1.4 Discontinuity, resets, and wrap

RFC 2863 ifCounterDiscontinuityTime: "The value of sysUpTime on the most recent occasion at which any one or more of this interface's counters suffered a discontinuity. The relevant counters are the specific instances associated with this interface of any Counter32 or Counter64 object contained in the ifTable or ifXTable. If no such discontinuities have occurred since the last re-initialization of the local management subsystem, then this object contains a zero value" (`https://www.rfc-editor.org/rfc/rfc2863.txt`, section 6). The dot3 counters point at the same object: "Discontinuities in the value of this counter can occur at re-initialization of the management system, and at other times as indicated by the value of ifCounterDiscontinuityTime" (RFC 3635 section 4). The schema direction turns both into one rule: "A decrease between two readings is a necessary reset signal and not a sufficient one" (`docs/architecture/2026-09-25-schema-building-blocks-direction.md:139-142`).

Three facts follow for the store:

1. A reported discontinuity time is an absolute `Timestamp` after the mapper resolves it against uptime. Two polls of the same real discontinuity can resolve to values a second or two apart (sysUpTime is hundredths of a second, observation clocks drift). The stored epoch therefore cannot be trusted to be exactly equal across samples, and the read-time formula must tolerate a spurious epoch split (section 4.4).
2. A reset that the source does not report (BGP4-MIB) is visible only as a decrease. Both signals are used: epoch split where reported, decrease where not.
3. 32-bit wrap. RFC 2863 section 3.1.6: "the minimum time in which a 32 bit counter will wrap decreases" with speed, and 64-bit octet counters are required above 20 Mb/s (RFC 3635 section 3.2.10: "Required for ethernet-like interfaces that are capable of operating at 20 Mb/s or faster"). A 32-bit octet counter on a 100 Mbit/s link wraps in 2^32 x 8 / 10^8 = 343 s, less than the 5 min poll (inference). The schema carries no counter width (D8). The store treats a decrease inside an unchanged epoch as a reset counted from zero, which under-counts a wrap by `2^32 - previous`. The fix belongs to the schema or mapper, not to the store.

Prometheus, as the reference for "exact": "rate(v range-vector) calculates the per-second average rate of increase of the time series in the range vector. Breaks in monotonicity (such as counter resets due to target restarts) are automatically adjusted for" and "Any decrease in the value between two consecutive float samples is interpreted as a counter reset" (`https://prometheus.io/docs/prometheus/latest/querying/functions/`). Prometheus also loses the pre-reset accumulation between the last pre-reset sample and the reset, exactly as section 4.4 does. Nothing can recover it.

### 1.5 Entity cardinality and the key

Per-port cardinality only lengthens the per-device run in a key sorted `(tenant, device, interface_name, ts)`. A 500-interface chassis is 500 adjacent runs. The sparse index guide: "the primary index for a part has one index entry (known as a 'mark') per group of rows" and "A granule is the smallest indivisible data set that is streamed into ClickHouse for data processing" (`https://clickhouse.com/docs/guides/best-practices/sparse-primary-indexes.md`). After merges one entity's samples fill whole granules, so the number of entities per device changes the number of granules read linearly and nothing else.

`interface_name` as `LowCardinality(String)` in the key: global distinct names across all tenants are vendor naming patterns ("GigabitEthernet1/0/1", "Ethernet49", "ge-0/0/0.100"). F10: under 10,000 distinct is efficient, over 100,000 "can perform worse". With sub-interfaces and 500-port chassis the global count may approach 100,000. The benchmark measures `uniqExact(interface_name)` at 16x scale and the dictionary size. Fallback (D20): `interface_key UInt64 = cityHash64(interface_name)` in the key, `interface_name` as payload. The same applies to `component_name` (`max_len 256`, `component.proto:27-31`).

## 2. Candidate models

### 2.1 Wide samples per counter family (recommended)

One row per (entity, poll) with every field of the counters message as a typed column plus a presence bitmask, status fields on the same row, key `(tenant, device, entity, ts)`. This is the Akvorado, FastNetMon, Glaber shape (03 "table per domain") and the dossier's own sketch (02 section 12.1). Compression comes from sorting: within one entity's run the counter columns are monotone with small strides, which is what `Delta` needs (F25). Sparse serialization makes constant-zero columns (collision counters on full-duplex ports) nearly free (F71).

### 2.2 Narrow rows `(entity, metric_id, ts, value)`

The Zabbix shape (`history_uint (itemid, clock_ns, value)`, 03). Every counter becomes a row. Pros: schema never changes when a counter is added, absent counters are absent rows (no mask), one table for every family. Cons: 12 to 24 rows per sample instead of one, the key repeated per row (compresses, but marks and index grow 12x), a chart of in/out bytes reads two key ranges, a rate needs `PARTITION BY metric_id` in the window, and presence of status fields needs a second table or string rows. Zabbix works because it has no schema. FlowSeer's schema is the protobuf.

### 2.3 Generic narrow table for "all other counters"

The question was whether one narrow table for every protocol beats a table per protocol for predictable scaling. Cost comparison for a BGP peer with 5 counters polled at 5 min:

| | Table per family (wide) | Generic narrow |
| --- | --- | --- |
| Rows per poll per entity | 1 | 5 |
| Bytes per poll (estimate) | key ~2 B + 5 counters x ~2 B + mask = ~13 B | 5 x (key ~2 B + metric_id ~1 B + value ~2 B) = ~25 B |
| Chart of 2 counters over 7 d | 1 key range, 2016 rows | 2 key ranges, 4032 rows |
| Rate window partition | entity | entity x metric |
| Schema change on new counter | `ADD COLUMN` (instant, F64) | none |
| Status and state fields | same row | separate table or string table |

Both scale linearly in `entities x window / interval`. The narrow table costs a constant factor of the counter count and loses the row-level unit (one observation). Prior art that chose generic tables regretted it (03 anti-patterns: ntopng promoted `ifid` by a whole-table mutation, Telegraf needs an ALTER per tag). Since every counter FlowSeer stores is a typed field, a table per family generated from the message is the same amount of code as a mapper to metric ids. Rejected (D17). The one case where a narrow table would appear, vendor extras outside the schema, has no message today and so no columns, which the brief forbids inventing.

### 2.4 Samples plus changes

Status in the sample row (2.1) plus a changes table for transitions. Chosen for the reasons in D4 and section 3.2. The pure "derive status from samples" alternative serves single-entity questions well but not "all ports that went down in site S in the last hour" (a scan of `entities x 2 granules` on raw, D10).

### 2.5 Projections instead of a second rollup table

A projection could hold the time-first ordering inside the hourly table. The docs: "Projections are automatically updated and kept in-sync with the original table", "Users are comfortable with the potential associated increase in storage footprint and overhead of writing data twice", "Lightweight updates and deletes aren't supported for tables with projections", "the `optimize_read_in_order` optimization mentioned above isn't supported for projections" (`https://clickhouse.com/docs/data-modeling/projections.md`). Lightweight delete is the tenant-forget path (F57), and read-in-order is the newest-first path (F51). Two MV targets are the safer choice (D9). A projection stays a benchmark variant.

## 3. Recommended tables

Conventions as in 02 section 12: database `flowseer`, `ReplicatedReplacingMergeTree` without Keeper arguments under a `Replicated` database, `tenant_id LowCardinality(String)`, `device_id UUID`, `ts DateTime` = `Provenance.observed_at` (`spec/proto/flowseer/model/inventory/v1/provenance.proto:18`), `retention_class Enum8` stamped by the sink, `site_id LowCardinality(String)` stamped at write (F69), `ttl_only_drop_parts = 1`. `record_id` is not stored in sample tables (F45) and is stored in change tables.

### 3.1 `interface_samples`

```sql
CREATE TABLE flowseer.interface_samples
(
    tenant_id         LowCardinality(String),
    device_id         UUID,
    interface_name    LowCardinality(String),               -- Interface.name (interface.proto:29)
    ts                DateTime        CODEC(Delta, ZSTD(1)), -- Provenance.observed_at
    -- identity payload
    if_index          UInt32          CODEC(T64, ZSTD(1)),  -- interface.proto:35, unstable, payload only
    kind              UInt8           CODEC(T64, ZSTD(1)),  -- oneof kind arm number 10..17 (interface.proto:66-85)
    parent_interface_name LowCardinality(String),           -- Subinterface.parent_interface_name, '' otherwise
    vlan_id           UInt16          CODEC(T64, ZSTD(1)),  -- VlanInterface.vlan_id, 0 otherwise
    -- status, carried on every sample (D4)
    admin_status      UInt8           CODEC(T64, ZSTD(1)),  -- AdminStatus number (D21)
    oper_status       UInt8           CODEC(T64, ZSTD(1)),  -- OperStatus number
    mtu               UInt32          CODEC(T64, ZSTD(1)),
    description       String          CODEC(ZSTD(1)),
    last_change       DateTime        CODEC(Delta, ZSTD(1)), -- Interface.last_change, 0 when absent
    speed_bps         UInt64          CODEC(T64, ZSTD(1)),  -- EthernetFacet.active_speed_bps, 0 when absent
    duplex            UInt8           CODEC(T64, ZSTD(1)),  -- EthernetDuplex number
    -- discontinuity epoch (D5): InterfaceCounters.last_discontinuity, 0 when absent
    discontinuity_at  DateTime        CODEC(Delta, ZSTD(1)),
    -- InterfaceCounters fields 1..12 (interface_counters.proto:17-45)
    in_bytes              UInt64 CODEC(Delta, ZSTD(1)),
    out_bytes             UInt64 CODEC(Delta, ZSTD(1)),
    in_unicast_packets    UInt64 CODEC(Delta, ZSTD(1)),
    out_unicast_packets   UInt64 CODEC(Delta, ZSTD(1)),
    in_multicast_packets  UInt64 CODEC(Delta, ZSTD(1)),
    out_multicast_packets UInt64 CODEC(Delta, ZSTD(1)),
    in_broadcast_packets  UInt64 CODEC(Delta, ZSTD(1)),
    out_broadcast_packets UInt64 CODEC(Delta, ZSTD(1)),
    in_errors             UInt64 CODEC(Delta, ZSTD(1)),
    out_errors            UInt64 CODEC(Delta, ZSTD(1)),
    in_discards           UInt64 CODEC(Delta, ZSTD(1)),
    out_discards          UInt64 CODEC(Delta, ZSTD(1)),
    -- bit i set when field i of the message was present; bits 16.. for status fields
    present_mask      UInt32          CODEC(T64, ZSTD(1)),
    retention_class   Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    site_id           LowCardinality(String)
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY toMonday(ts)
ORDER BY (tenant_id, device_id, interface_name, ts)
TTL ts + INTERVAL 90 DAY DELETE WHERE retention_class = 'short',
    ts + INTERVAL 1 YEAR DELETE WHERE retention_class IN ('standard', 'long')
SETTINGS ttl_only_drop_parts = 1,
         min_age_to_force_merge_seconds = 604800,
         min_age_to_force_merge_on_partition_only = 1;
```

Clause reasoning. `ORDER BY` tenant, device, entity, time (F3 to F6) with the entity being the name (D2). `ReplacingMergeTree` on that key dedups a redelivery (F45, section 4). Weekly partitions give 13 to 52 partitions at 90 d to 1 y (F7, F8) and one part per insert. `Delta, ZSTD(1)` on counters because within one entity's run the stride is small and positive (F25). `T64` on small-range integers (F24). Status columns sorted per entity are runs of one value, so `T64, ZSTD(1)` and `ZSTD(1)` on `description` cost close to nothing (inference from F25, measured in the benchmark). `UInt8` for status instead of `Enum8` (D21). `present_mask` instead of Nullable (F9, F12 applied). `min_age_to_force_merge_*` merges closed weeks to one part so a scan of an old week touches one index (F40, F44).

Counter rate at read time (F28, F30): `nonNegativeDerivative(in_bytes, ts, INTERVAL 1 SECOND) OVER (PARTITION BY tenant_id, device_id, interface_name ORDER BY ts)`. Section 4.4 gives the exact form.

### 3.1a `ethernet_samples`

Same key and clauses. Columns: the 12 `EthernetCounters` fields (`ethernet_counters.proto:19-46`) as `UInt64 CODEC(Delta, ZSTD(1))`, `discontinuity_at` from its own `last_discontinuity` (`:49`), `present_mask UInt16`, `retention_class`, `site_id`. A separate table rather than 12 more columns on `interface_samples` because the message is separate, has its own discontinuity time, exists only for physical ports, and a poller may read dot3 tables at a different interval than ifXTable. Rows are mostly zeros on full-duplex ports (RFC 3635 above), so sparse serialization applies (F71) and the table should be a few bytes per row.

### 3.2 `interface_changes`

02 section 12.2 stands with two changes: the entity is `interface_name`, and `attribute` covers `admin_status`, `oper_status`, `speed_bps`, `duplex`, `mtu`, `description`, `if_index` (an ifIndex renumbering after restart is itself worth a row). `new_oper_status UInt8` instead of `Enum8` (D21).

Producer. The sink cannot diff without state, and the direction keeps current state in KV with a state projector (`docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md`, Stages). The interface has no Config/State/Event triad yet ("the Interface entity and its ref are not decided yet", `spec/proto/flowseer/model/access/v1/interface.proto:3-5`), while `DeviceEvent` and `ComponentEvent` exist (`device.proto:220-244`, `component.proto:219-250`). Recommendation for the plan: the state projector emits an interface event record when its compare-and-set changes a status field, and the history sink writes that record to `interface_changes`. Until then, the device's own `last_change` on every sample (3.1) answers "when did oper status last change" per entity exactly, and the hourly rollup's `oper_status_min`/`oper_status_max` (3.5) answers "which interfaces changed in scope S in hour H" without a changes table.

### 3.3 Device system tables

```sql
CREATE TABLE flowseer.device_samples
(
    tenant_id        LowCardinality(String),
    device_id        UUID,
    ts               DateTime  CODEC(Delta, ZSTD(1)),
    uptime_seconds   UInt64    CODEC(Delta, ZSTD(1)),   -- DeviceState.uptime (device.proto:210)
    cpu_avg_basis_points UInt16 CODEC(T64, ZSTD(1)),   -- ProcessorUtilization.utilization_avg_basis_points
    cpu_window_seconds   UInt32 CODEC(T64, ZSTD(1)),   -- ProcessorUtilization.window, 0 when absent
    present_mask     UInt8     CODEC(T64, ZSTD(1)),
    retention_class  Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    site_id          LowCardinality(String)
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY toMonday(ts)
ORDER BY (tenant_id, device_id, ts)
TTL ts + INTERVAL 90 DAY DELETE WHERE retention_class = 'short',
    ts + INTERVAL 1 YEAR DELETE WHERE retention_class IN ('standard', 'long')
SETTINGS ttl_only_drop_parts = 1, min_age_to_force_merge_seconds = 604800,
         min_age_to_force_merge_on_partition_only = 1;

CREATE TABLE flowseer.storage_samples
(
    tenant_id        LowCardinality(String),
    device_id        UUID,
    source           Enum8('device' = 1, 'component' = 2),   -- DeviceState.storage_utilization vs ComponentState.storage_utilization
    storage_name     LowCardinality(String),                 -- StorageUtilization.name, or the component name
    ts               DateTime  CODEC(Delta, ZSTD(1)),
    kind             UInt8     CODEC(T64, ZSTD(1)),          -- StorageKind number
    total_bytes      UInt64    CODEC(T64, ZSTD(1)),
    used_bytes       UInt64    CODEC(Gorilla, ZSTD(1)),
    used_basis_points UInt16   CODEC(T64, ZSTD(1)),
    present_mask     UInt8     CODEC(T64, ZSTD(1)),
    retention_class  Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    site_id          LowCardinality(String)
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY toMonday(ts)
ORDER BY (tenant_id, device_id, source, storage_name, ts)
TTL ts + INTERVAL 90 DAY DELETE WHERE retention_class = 'short',
    ts + INTERVAL 1 YEAR DELETE WHERE retention_class IN ('standard', 'long')
SETTINGS ttl_only_drop_parts = 1, min_age_to_force_merge_seconds = 604800,
         min_age_to_force_merge_on_partition_only = 1;

CREATE TABLE flowseer.component_samples
(
    tenant_id        LowCardinality(String),
    device_id        UUID,
    component_name   LowCardinality(String),      -- ComponentLocalRef.name (component.proto:27)
    ts               DateTime  CODEC(Delta, ZSTD(1)),
    kind             UInt8     CODEC(T64, ZSTD(1)),   -- ComponentKind number
    oper_status      UInt8     CODEC(T64, ZSTD(1)),   -- ComponentOperStatus number
    -- at most one reading per quantity (component.proto:106-115)
    temperature_millidegrees_celsius Int32  CODEC(Gorilla, ZSTD(1)),
    voltage_microvolts               Int32  CODEC(Gorilla, ZSTD(1)),
    current_microamperes             Int32  CODEC(Gorilla, ZSTD(1)),
    power_nanowatts                  UInt64 CODEC(Gorilla, ZSTD(1)),
    rotation_rpm                     UInt32 CODEC(Gorilla, ZSTD(1)),
    humidity_basis_points            UInt16 CODEC(T64, ZSTD(1)),
    cpu_avg_basis_points             UInt16 CODEC(T64, ZSTD(1)),   -- CPU components only
    cpu_window_seconds               UInt32 CODEC(T64, ZSTD(1)),
    present_mask     UInt16    CODEC(T64, ZSTD(1)),
    retention_class  Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    site_id          LowCardinality(String)
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY toMonday(ts)
ORDER BY (tenant_id, device_id, component_name, ts)
TTL ts + INTERVAL 90 DAY DELETE WHERE retention_class = 'short',
    ts + INTERVAL 1 YEAR DELETE WHERE retention_class IN ('standard', 'long')
SETTINGS ttl_only_drop_parts = 1, min_age_to_force_merge_seconds = 604800,
         min_age_to_force_merge_on_partition_only = 1;
```

Reasoning. Gauges get `Gorilla` ("can be effective on floating point data, specifically that which represents gauge readings", F25; the codec page lists it for any numeric type, F24; the benchmark compares `Gorilla` with `T64` on integer gauges because the guide's claim is about floats). `uptime_seconds` is a counter in disguise (monotone until reset) so `Delta`. Storage from the device and from a component share one table because the question "disk usage on device D" should not need a union. Component storage rows reuse the component name as `storage_name` since `ComponentState.storage_utilization` "must not set a name" (`component.proto:126-130`). Thresholds go to `component_changes` (D14): a changes table shaped like `interface_changes` with `attribute` naming the threshold field, fed by `ComponentEvent` (`component.proto:219-250`) which already exists and carries `before` and `after`. The sink writes one row per differing field. Uptime: a reboot question is answered by `DeviceEvent`/the state projector (D15). The history query "uptime at T" is a one-granule read of `device_samples`.

### 3.4 Protocol counter tables (the generalisation test)

The template: `(tenant_id, device_id, <key columns>, ts)`, the counters message's fields as `UInt64 CODEC(Delta, ZSTD(1))`, the state and gauge fields as typed columns, `discontinuity_at`, `present_mask`, `retention_class`, `site_id`, weekly partitions, same TTL and settings as 3.1. What changes per family:

| Table | Key columns after device | Counter columns | State and gauge columns |
| --- | --- | --- | --- |
| `bgp_peer_samples` | `network_instance LowCardinality(String)`, `protocol_instance LowCardinality(String)`, `remote_address IPv6` (IPv4 mapped, F11) | `in_update_messages`, `out_update_messages`, `in_messages`, `out_messages`, `established_transitions` (`bgp_peer.proto:124-132`) | `state UInt8`, `enabled UInt8`, `established_seconds UInt64` (`:78`), `remote_asn UInt32`, `local_asn UInt32`, `remote_router_id UInt32`, `interface_name LowCardinality(String)`, `local_address IPv6`, hold and keepalive seconds |
| `bgp_peer_afi_samples` | peer key + `afi UInt16`, `safi UInt8` | none | `active UInt8`, `received_prefixes`, `sent_prefixes`, `installed_prefixes` as `UInt32 CODEC(T64, ZSTD(1))` (gauges, `:114-118`) |
| `bfd_session_samples` | `network_instance`, `local_discriminator UInt32` | `in_packets`, `out_packets`, `up_transitions` (`bfd_session.proto:97-101`) | `remote_discriminator`, `session_type`, `interface_name`, `local_address`, `remote_address`, `state`, `remote_state`, both diagnostics, three intervals in milliseconds, `detect_multiplier` |
| `vrrp_group_samples` | `interface_name`, `address_family UInt8`, `vrid UInt8` | `master_transitions`, `received_advertisements` (`vrrp_group.proto:109-111`) | `version`, `state`, `priority`, `master_address IPv6`, `primary_address IPv6`, `virtual_mac UInt64`, `advertisement_interval_ms`, `preempt`, `accept_mode`; `virtual_addresses Array(IPv6)` |
| `dhcp_server_message_samples` | `ip_version UInt8` (4 or 6), `message_type UInt8` | `received_messages`, `sent_messages` (`dhcpv4_server_counters.proto:36-38`) | none; one `discontinuity_at` for the set, copied to every row |
| `dhcp_pool_samples` | `network_instance`, `pool_name` | none | `total_addresses`, `leased_addresses` gauges (`dhcpv4_pool.proto:46-48`) |

BGP AFI rows are a separate table rather than arrays on the peer row because a chart of received prefixes for one (peer, afi, safi) is a key range there and an `arrayJoin` scan otherwise. DHCP message types are rows, not a `Map(UInt8, UInt64)`, because a Map lookup loads the whole column and scans the map (F49) and the message set is validated unique (`dhcpv4_server_counters.proto:11-15`).

Result of the test: five families fit the template unchanged. `NatSession` does not. Its entity is `(network_instance, protocol, private_source, public_source)` (`nat_session.proto:16-30`), a session lives for `expires_in` of idle time (`:36`), and a NAT router holds thousands at a time. One row per session per poll would be a set snapshot per poll, the shape 03 warns against ("volume grows with set size times poll rate"). NAT sessions belong to the set-valued presence model (03, "Snapshots vs changes", F43 alternative): `nat_session_presence` as `AggregatingMergeTree` keyed `(tenant, device, network_instance, protocol, private_source, public_source, day)` with `min(first_seen)`, `max(last_seen)`, `argMax` of the four counters by `ts`, plus a per-instance gauge in `nat_instance_samples` (session count per poll, which the sink computes from the record). That table is for the set-valued report to settle. `NatMapping` (`nat_mapping.proto:14-55`) is configuration, a changes table.

### 3.5 Rollups

Two hourly tables fed by two MVs from `interface_samples`, one daily table fed by a third MV from raw (never chained, 03 SigNoz lessons). Shown for interface counters, the other families follow by column substitution.

```sql
CREATE TABLE flowseer.interface_hourly
(
    tenant_id        LowCardinality(String),
    device_id        UUID,
    interface_name   LowCardinality(String),
    hour             DateTime,
    discontinuity_at DateTime,                                  -- part of the key (D5)
    samples          AggregateFunction(uniqExact, DateTime),    -- idempotent count (D6)
    first_ts         SimpleAggregateFunction(min, DateTime),
    last_ts          SimpleAggregateFunction(max, DateTime),
    in_bytes_first   AggregateFunction(argMin, UInt64, DateTime),
    in_bytes_last    AggregateFunction(argMax, UInt64, DateTime),
    in_bytes_min     SimpleAggregateFunction(min, UInt64),
    in_bytes_max     SimpleAggregateFunction(max, UInt64),
    out_bytes_first  AggregateFunction(argMin, UInt64, DateTime),
    out_bytes_last   AggregateFunction(argMax, UInt64, DateTime),
    out_bytes_min    SimpleAggregateFunction(min, UInt64),
    out_bytes_max    SimpleAggregateFunction(max, UInt64),
    -- same four columns for in_errors, out_errors, in_discards, out_discards, in_unicast_packets, out_unicast_packets
    speed_bps_max    SimpleAggregateFunction(max, UInt64),
    oper_status_min  SimpleAggregateFunction(min, UInt8),
    oper_status_max  SimpleAggregateFunction(max, UInt8),
    present_mask_any SimpleAggregateFunction(groupBitOr, UInt32),
    retention_class  SimpleAggregateFunction(max, Enum8('short' = 1, 'standard' = 2, 'long' = 3)),
    site_id          SimpleAggregateFunction(anyLast, LowCardinality(String))
)
ENGINE = ReplicatedAggregatingMergeTree
PARTITION BY toYYYYMM(hour)
ORDER BY (tenant_id, device_id, interface_name, hour, discontinuity_at)
TTL hour + INTERVAL 2 YEAR
SETTINGS ttl_only_drop_parts = 1;

CREATE MATERIALIZED VIEW flowseer.interface_hourly_mv TO flowseer.interface_hourly AS
SELECT tenant_id, device_id, interface_name, toStartOfHour(ts) AS hour, discontinuity_at,
       uniqExactState(ts) AS samples, min(ts) AS first_ts, max(ts) AS last_ts,
       argMinState(in_bytes, ts) AS in_bytes_first, argMaxState(in_bytes, ts) AS in_bytes_last,
       min(in_bytes) AS in_bytes_min, max(in_bytes) AS in_bytes_max,
       argMinState(out_bytes, ts) AS out_bytes_first, argMaxState(out_bytes, ts) AS out_bytes_last,
       min(out_bytes) AS out_bytes_min, max(out_bytes) AS out_bytes_max,
       max(speed_bps) AS speed_bps_max, min(oper_status) AS oper_status_min, max(oper_status) AS oper_status_max,
       groupBitOr(present_mask) AS present_mask_any, max(retention_class) AS retention_class, anyLast(site_id) AS site_id
FROM flowseer.interface_samples
GROUP BY tenant_id, device_id, interface_name, hour, discontinuity_at;
```

`interface_hourly_by_time` is the same table with `ORDER BY (tenant_id, hour, device_id, interface_name, discontinuity_at)` and its own MV (D9). `interface_daily` is the same table with `day Date` in place of `hour`, `PARTITION BY toYear(day)`, `TTL day + INTERVAL 5 YEAR`, entity-first order, fed from raw.

Why these aggregates: F33 says `SimpleAggregateFunction` supports `min`, `max`, `anyLast`, `groupBitOr` but not `argMin`/`argMax`, so those are `AggregateFunction`. All of them are idempotent under a repeated identical input (section 4.3). `uniqExact(ts)` replaces `count()` because `count` doubles on a redelivery (03 Contentsquare warning) while a set of timestamps does not. The 5 m tier the task names is not built for 5 min polled data (raw is the 5 m tier). For controllers polled at 2 to 3 min the same MV with `toStartOfFiveMinutes` applies if a plan wants it.

TTL GROUP BY (F34) is not used: it would force the bucket into the raw primary key.

## 4. Dedup under redelivery

### 4.1 Delivery facts

Each central publication uses `<tenant>.<record_id>` as message id and the stream drops a repeat inside ten minutes. "Past that window a repeat is stored again, which happens when a hub restart re-sources an edge buffer ... Each sink that reads a central stream therefore deduplicates on tenant and `record_id` itself" (ingestion direction, 2026-10-04 amendment). Insert-block dedup covers a retried identical batch inside 3,600 s or 10,000 blocks (01 section 3, F48).

### 4.2 Raw tables

A sample row's natural key `(tenant_id, device_id, entity, ts)` is unique per poll by construction and a redelivery of the same record produces a byte-identical row (F45). `ReplacingMergeTree` collapses it at merge, and until then a reader sees two identical rows. Effect per query shape: newest-first lists show a duplicate line (dedup with `LIMIT 1 BY ts` if the UI cares: "A query with the `LIMIT n BY expressions` clause selects the first `n` rows for each distinct value of `expressions`", `https://clickhouse.com/docs/sql-reference/statements/select/limit-by.md`), rate windows see a zero-length, zero-delta step (`nonNegativeDerivative` returns 0 "when elapsed time is non-positive", F28), `argMax` at T picks one of two identical rows (F41). No raw query needs `FINAL`.

Interleaved redelivery (an old record arriving after newer ones) lands in the right place because the key is `ts`, not arrival time. The partition key is `toMonday(ts)`, so the duplicate lands in the same partition and merges can see it (01 section 3).

### 4.3 Rollups

The MV fires per insert block, before any merge, so a redelivered block past the dedup window reaches the rollup as a second state for the same key (the "Duplicate inserts past dedup double-count sums" risk in 02 section 11). Every column in 3.5 is chosen so that merging a state with a copy of itself, or with a state built from a subset of the same rows, changes nothing:

- `min`, `max`, `groupBitOr`, `anyLast` (same value): idempotent by definition.
- `argMin(value, ts)`, `argMax(value, ts)`: the extreme `ts` and its value are the same in both states. The docs' tie rule ("which of the associated `arg` is returned is not deterministic", F41) does not matter because tied rows carry identical values.
- `uniqExact(ts)`: a set union with the same members.

`deltaSumTimestamp`, used in 02 section 12.1, was checked in source (`https://raw.githubusercontent.com/ClickHouse/ClickHouse/master/src/AggregateFunctions/AggregateFunctionDeltaSumTimestamp.cpp`). `add` does `if ((data.last < value) && data.seen) data.sum += (value - data.last)`. `mergeImpl` adds `rhs_data.sum` only when one state lies entirely before the other (`before()` returns true when "lhs.last_ts < rhs.first_ts", or on an equal boundary timestamp). The final branch, commented "If none of those conditions matched, it means both states we are merging have all same timestamps", only picks `first`/`last` and does not modify `sum`. So an overlapping duplicate state is ignored (idempotent), but an overlapping state that holds novel samples is also ignored, which under-counts if a sink ever inserted interleaved batches of one entity. FlowSeer's sink reads one stream in sequence, so novel interleaving does not occur, but the first/last design (D6) has no such edge and additionally supports the epoch-exact increase (4.4). Keep `deltaSumTimestamp` out, or add it as a cross-check column in the benchmark only.

### 4.4 Exact increase and rate from the rollup

For one entity over a range of hours, ordered by `(hour, first_ts)`:

```sql
WITH rows AS (
    SELECT hour, discontinuity_at, minMerge(first_ts) AS f_ts, maxMerge(last_ts) AS l_ts,
           argMinMerge(in_bytes_first) AS first_v, argMaxMerge(in_bytes_last) AS last_v,
           min(in_bytes_min) AS min_v, max(in_bytes_max) AS max_v
    FROM flowseer.interface_hourly
    WHERE tenant_id = {t} AND device_id = {d} AND interface_name = {i}
      AND hour >= {from} AND hour < {to}
    GROUP BY hour, discontinuity_at
    ORDER BY hour, f_ts
)
SELECT hour,
       -- inside the epoch row: a reset inside the hour shows as max_v > last_v
       (max_v - first_v) + if(max_v > last_v, last_v, 0)
       -- across the boundary from the previous row: continuous or reset-from-zero
       + multiIf(prev_last = 0, 0, first_v >= prev_last, first_v - prev_last, first_v) AS increase_bytes,
       increase_bytes * 8 / greatest(1, l_ts - greatest(f_ts, prev_l_ts)) AS rate_bps
FROM (
    SELECT *, lagInFrame(last_v, 1, toUInt64(0)) OVER w AS prev_last,
              lagInFrame(l_ts, 1, f_ts) OVER w AS prev_l_ts
    FROM rows WINDOW w AS (ORDER BY hour, f_ts ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW)
);
```

Properties. A reported discontinuity splits the row and the boundary term counts the new epoch's `first_v` from zero only when it is below the previous `last_v`. A spurious split (epoch value drifting by a second between polls, section 1.4) yields `first_v >= prev_last` and so a plain difference, never a spike. An unreported reset inside an hour is caught by `max_v > last_v` and counted as `(max_v - first_v) + last_v`, which is the Prometheus result for one reset; two resets in one hour are under-counted (unverified how often that occurs; the baseline has about 3 % of APs rebooting per day, switches less). A 32-bit wrap is counted as a reset (D8). Rate uses the actual span between samples, not the bucket width. `lagInFrame` with `ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW` as F28 requires.

On raw, the same semantics are a window over samples: `multiIf(prev_v = 0 OR ts = prev_ts, 0, discontinuity_at != prev_epoch AND v < prev_v, v, v >= prev_v, v - prev_v, v)` with `lagInFrame` of value, ts, and epoch. `nonNegativeDerivative` (F28) is the shortcut that clamps a reset to zero instead of counting from zero, acceptable for charts.

## 5. Representative queries and cost models

Notation: `E` entities in scope, `W` window seconds, `I` poll interval seconds, `G` index granularity rows (8192), `P` parts overlapping the window in the touched partitions (1 to a few after merges, up to tens in the active week). A key-range read costs `ceil(W/I/G) + 1` granules per entity per part (the sparse index guide: "one out of the 1083 granules was selected as possibly containing rows", a range always rounds out to whole granules). `read_rows = E x P x G x (ceil(W/(I G)) + 1)`. None of these terms is the total table size or the tenant count.

### 5.1 Device and interface, newest first

```sql
SELECT ts, oper_status, admin_status, speed_bps, in_bytes, out_bytes, in_errors, present_mask
FROM flowseer.interface_samples
WHERE tenant_id = {t} AND device_id = {d} AND interface_name = {i} AND ts >= {from}
ORDER BY ts DESC LIMIT 200;
```

Reverse read in key order stops at the limit (F51). Cost: `P x (ceil(200/G) + 1)` granules, that is 1 to 2 granules per part, about 16 k rows. Flat in everything.

Chart of in/out rate for 7 days on raw: `E = 1, W = 604800, I = 300` gives 2016 rows, `ceil(2016/8192) + 1 = 2` granules per part. The window function runs over 2016 rows.

### 5.2 What was it at T, when did it change

```sql
SELECT * FROM flowseer.interface_samples
WHERE tenant_id = {t} AND device_id = {d} AND interface_name = {i} AND ts <= {T}
ORDER BY ts DESC LIMIT 1;
```

One to two granules per part. "When did oper status change" for one entity from samples: `SELECT DISTINCT last_change FROM ... WHERE ... AND ts BETWEEN ...` reads `ceil(W/(I G)) + 1` granules and returns the device-reported timestamps. From `interface_changes`: `WHERE tenant_id, device_id, interface_name ORDER BY ts DESC LIMIT n`, reverse key read, 1 to 2 granules.

### 5.3 Scope over 24 h (site resolved to a device list)

Never on raw (D10): `E x P x 2 x 8192` rows for 24 h, 1.6 G rows at 96,000 entities. On `interface_hourly_by_time`:

```sql
SELECT device_id, interface_name,
       argMaxMerge(in_bytes_last) ..., -- as in 4.4 per (device, interface, hour, epoch)
FROM flowseer.interface_hourly_by_time
WHERE tenant_id = {t} AND hour >= {now - 24h} AND hour < {now} AND device_id IN ({devices})
GROUP BY device_id, interface_name, hour, discontinuity_at;
```

The key prefix `(tenant_id, hour)` makes the range `24 x E_tenant` rows contiguous (`E_tenant` = all entities of the tenant), and `device_id IN` filters inside it (generic exclusion search on the third key column "is most effective when the predecessor key column has low(er) cardinality", which `hour` is). Cost: `read_rows <= 24 x E_tenant x P`, rounded to granules. Tenant of 10,000 devices x 48: 11.5 M rows read for any scope inside it, 2.3 M for the 2,000-device scope after exclusion search prunes granules that hold no listed device (granules are sorted by device within an hour, so a 2,000-of-10,000 device list hits about 20 % of granules plus boundaries; the benchmark verifies the pruning). Memory: GROUP BY keys `E x 24`, with `argMax` states of 16 bytes, about 2.3 M x 8 columns x 16 B = 300 MB worst case before result reduction. Bound it with `max_bytes_before_external_group_by` in the query profile and, where the UI only needs totals per interface, aggregate per `(device, interface)` with `first`/`last` per epoch inside a subquery first.

### 5.4 Top-N interfaces by utilisation in a 2,000-device scope over 24 h

```sql
WITH per_hour AS (
    SELECT device_id, interface_name, hour, discontinuity_at,
           argMinMerge(in_bytes_first) AS f_in, argMaxMerge(in_bytes_last) AS l_in, max(in_bytes_max) AS m_in,
           argMinMerge(out_bytes_first) AS f_out, argMaxMerge(out_bytes_last) AS l_out, max(out_bytes_max) AS m_out,
           minMerge(first_ts) AS f_ts, maxMerge(last_ts) AS l_ts, max(speed_bps_max) AS speed
    FROM flowseer.interface_hourly_by_time
    WHERE tenant_id = {t} AND hour >= {now - 24h} AND hour < {now} AND device_id IN ({devices})
    GROUP BY device_id, interface_name, hour, discontinuity_at
),
per_iface AS (
    SELECT device_id, interface_name,
           sum((m_in - f_in) + if(m_in > l_in, l_in, 0)) AS in_inc,     -- within-row term of 4.4
           sum((m_out - f_out) + if(m_out > l_out, l_out, 0)) AS out_inc,
           max(l_ts) - min(f_ts) AS span, max(speed) AS speed
    FROM per_hour GROUP BY device_id, interface_name
)
SELECT device_id, interface_name,
       greatest(in_inc, out_inc) * 8 / greatest(span, 1) AS peak_dir_bps,
       peak_dir_bps / speed AS utilisation
FROM per_iface
WHERE speed > 0 AND span >= 3600
ORDER BY utilisation DESC LIMIT 20;
```

The cross-row boundary term of 4.4 is dropped here (it matters only across a reset, and a ranking tolerates that), which keeps the query a plain two-level GROUP BY. Cost as 5.3: `24 x E_scope` rows after pruning, 2.3 M rows, two aggregations, result `E_scope` rows before the sort. `speed = 0` ports are excluded (baseline: "About a quarter of up switch ports report speed 0"). For a "busiest ports by bps" variant drop the division. Over 30 days use `interface_daily` with the same shape: `30 x E_scope` rows.

### 5.5 Hourly and daily charts over months for one entity

`interface_hourly` (entity-first): 720 rows per month contiguous, `ceil(720/8192) + 1 = 2` granules per part. A year is 8,760 rows, 3 granules. `interface_daily`: 365 rows, 1 to 2 granules. Flat.

### 5.6 Device CPU, storage, sensors

`device_samples` and `component_samples` follow 5.1 and 5.2 with the component name as entity. "Hottest components in scope over 24 h" is the 5.4 shape on a `component_hourly_by_time` table with `max(temperature)`; "which PSUs went down" uses `oper_status_min/max` on the same rollup. A single device's 20 components over 24 h on raw: `20 x 2` granules, 330 k rows read for 5,760 needed, a few milliseconds.

### 5.7 Protocol counters

"BGP peers with established transitions in the last 24 h in tenant T": `bgp_peer_hourly_by_time`, `WHERE hour range`, `HAVING argMaxMerge(established_transitions_last) > argMinMerge(established_transitions_first)`. Rows read `24 x peers_in_tenant`. Cross-device "which devices peer with address X": `bgp_peer_samples` has `remote_address` as the third key column after `network_instance` and `protocol_instance`, so a tenant-wide lookup scans the tenant's part of the table with exclusion search. A `bloom_filter` on `remote_address` (F19) is justified only if peers per tenant exceed a few hundred thousand rows per day; measure before adding.

### 5.8 Queries whose cost grows with total data, and the bound

| Query | Unbounded form | Bound |
| --- | --- | --- |
| Scope over 24 h on raw | `E x 16384 x P` rows | Route to `*_hourly_by_time` (D9), `max_rows_to_read` in the profile (F56) |
| Tenant-wide "any interface with errors in the last hour" on raw | tenant's entities x 2 granules | Same rollup, `HAVING in_errors_last > in_errors_first` |
| "When did X change" with no entity (all changes in tenant) | tenant's changes table range | `interface_changes` is ordered by entity, so a time-only filter scans the tenant range; the `minmax` index on `ts` (02 section 12.2) prunes granules outside the window, and the hourly rollup's `oper_status_min != oper_status_max` is the bounded alternative |
| Cross-tenant anything | whole table | Not a product query; the query builder always binds `tenant_id` |
| `FINAL` on raw | all parts of the key range | Not used (4.2) |

## 6. Granularity and read amplification

### 6.1 The granule problem on raw

After a merge, one entity's 5 min samples fill a granule of 8192 rows, which is 28.4 days of that entity. Any window shorter than that reads 1 to 2 whole granules per entity per part. Single-entity reads do not care (16 k rows is about a millisecond). Multi-entity reads on raw do, which is why every scope query routes to the time-first rollup (D9, D22). The benchmark compares `index_granularity = 8192` with `2048` on `interface_samples`: a 4x smaller granule cuts raw scope reads 4x and raises the primary index by 4x (a mark is one entry per granule, "completely loaded into the main memory", sparse index guide). At 8192 the index for a year of 100 G rows is about 12 M marks x ~40 B = 0.5 GB; at 2048 it is 2 GB (inference, measured by `system.parts` `primary_key_bytes_in_memory`). The default stays unless the benchmark shows a scope query on raw that the rollups cannot serve.

### 6.2 Parts

Each insert makes one part per partition touched (F7, F16). The sink batches per table, so at one insert every 5 s there are 17 k parts/day per table before merges and the usual few after. `P` in the cost models is the active part count in the window's partitions. Closed weeks merge to one part (`min_age_to_force_merge_*`, F44). The current week carries tens of parts, so "last 24 h" reads multiply the granule count by `P` of the current week. Monitor `system.parts` `WHERE active GROUP BY partition` (F63).

## 7. Write cost and storage

Rows per day at 20,000 devices (5 min interfaces, 5 min device, 10 min components):

| Table | Entities | Rows/day | Average rows/s |
| --- | --- | --- | --- |
| `interface_samples` | 960,000 | 276 M | 3,200 |
| `ethernet_samples` (physical only, say 40 per device) | 800,000 | 230 M | 2,700 |
| `device_samples` | 20,000 | 5.8 M | 67 |
| `storage_samples` (3 per device) | 60,000 | 17 M | 200 |
| `component_samples` (20 per device, 10 min) | 400,000 | 58 M | 670 |
| protocol tables (routers only, say 2,000 devices x 20 keys) | 40,000 | 12 M | 130 |
| hourly rollups (x2 orderings), per raw table | | rows/day / 12 x 2 | |

Inserts: batches of 10 k to 100 k rows ("at least 1,000 rows", "at least 10,000 events", F14) give under one insert per second per table. Each insert creates one raw part plus one part per MV target, so four parts per insert for `interface_samples` (raw, hourly, hourly_by_time, daily).

Bytes per row (estimates, to be measured): key columns compress to about 1 to 2 B/row in sorted runs (tenant and name are runs of one value, `device_id` a run of one UUID, `ts` a constant stride under `Delta`). Each idle counter under `Delta, ZSTD(1)` costs about 0.5 to 1 B/row (constant stride), an active counter 3 to 6 B/row. Status columns about 0.1 B/row each. Estimate 15 B/row for a mostly idle estate, 40 B/row worst case, so 4 to 11 GB/day for `interface_samples`, 0.4 to 1 TB at 90 days, 1.5 to 4 TB at one year. `ethernet_samples` 3 to 6 B/row (sparse zeros), about 1 GB/day. Rollups: one hourly row holds about 8 `argMin`/`argMax` states of 16 B plus mins and maxes, about 200 B/row before compression, 23 M rows/day x 2 orderings, about 2 to 4 GB/day compressed (unverified, the benchmark measures it). Daily: a twelfth of that. The baseline's "4 to 6 bytes per row" was for gauge-heavy AP rows (`baseline:103-105`) and is the floor, not the expectation, for counter rows.

## 8. Failure modes and how the design avoids them

| Failure | Where it bit | This design |
| --- | --- | --- |
| Absent counter stored as zero, read as a reset or a flat line | Baseline: "Zero means 'not reported'" | `present_mask` bit per field, queries filter `bitTest(present_mask, i)` |
| Spike after a reset or wrap | Any naive `value - lag` | Reset counted from zero, epoch split tolerated, `nonNegativeDerivative` as the clamped fallback |
| Double-counted rollups after a redelivery | 02 section 11 risk, Contentsquare | Idempotent aggregates only (D6) |
| Too many parts from chained MVs | SigNoz #7983, #9794 | Every MV reads raw, batches of 10 k+ rows |
| Scope query scanning every entity's granules | D10 | Time-first rollup for scopes, `max_rows_to_read` on the profile |
| ifIndex renumbering splits an interface's history | `interface.proto:33-35` | Key on name, `if_index` as payload with a change row |
| Utilisation division by zero | Baseline: a quarter of up ports report speed 0 | `WHERE speed > 0`, ranking by bps as the alternative |
| Key column rename or reorder later | 03 anti-patterns (qryn, Akvorado, OTel) | Name the entity key from the schema now (D2), never `ifindex` |
| `LowCardinality` over 100 k distinct names | F10 | Measured at 16x; hash-key fallback (D20) |
| Enum8 insert fails on an unknown status | `interface.proto:36-38` | `UInt8` (D21) |
| Threshold churn bloating sensor rows | D14 | Thresholds in `component_changes` only |
| Tenant forget blocked by projections | F57, projections page | No projections on raw or rollups (2.5) |

## 9. Benchmark design

Target: local single-node ClickHouse 26.8 container. Everything is generated inside ClickHouse with `INSERT ... SELECT FROM numbers(...)` so a later step needs no generator binary, and every value is a pure function of `(seed, tenant, device, entity, ts)` so runs are reproducible and parallel.

### 9.1 Generator

Parameters (defaults in brackets): `seed` [1], tenants `T` [50] with a size distribution of 45 small tenants (10 devices), 4 medium (500), 1 large (10,000 at 16x), devices per tenant from that, interfaces per device drawn from {24, 48, 52, 500} with weights {0.3, 0.5, 0.18, 0.02} plus 10 % sub-interfaces, components per device [20], poll interval `I` [300 s], days `D` [30], active-port share [0.2], reset period per entity [uniform 10 to 120 days], status flap rate [0.5 % of up ports flap per day], sensor noise [±1 °C], 32-bit-only share [0].

Value process for a counter (no window functions needed):

```sql
-- per entity constants from hashes
rate_bps   = if(h1 % 100 < 20, 1e6 * (1 + h2 % 400), 1e3 * (1 + h2 % 50))      -- active vs idle
reset_period = 86400 * (10 + h3 % 110)
epoch_start  = reset_period * intDiv(ts - h4 % reset_period, reset_period) + h4 % reset_period
in_bytes     = toUInt64((ts - epoch_start) * rate_bps / 8 * (1 + 0.1 * sin(ts / 3600)) + h5 % 1000)
discontinuity_at = if(reports_discontinuity, epoch_start + (h6 % 3), 0)   -- 0..2 s jitter on purpose
oper_status  = if(cityHash64(seed, entity, intDiv(ts, 86400)) % 100000 < flap_rate_per_day, 2, 1)
```

where `h1..h6 = cityHash64(seed, tenant_id, device_id, interface_name, k)`. Ethernet counters: FCS errors as a rare Poisson-like increment (`h % 10000 < 3`), collisions constant zero. Components: temperature = base + noise, fan rpm = base ± 2 %, PSU oper status with a 0.1 %/day flap. Protocol: 2 % of devices are routers with 20 BGP peers, 4 BFD sessions, 2 VRRP groups, 10 DHCP message types.

Loading: one `INSERT ... SELECT` per simulated hour per table (about 1.15 M interface rows at 1x), which mirrors the sink's batching and lets `system.part_log` count parts per insert. A second loader pass re-inserts 1 % of the hours chosen by hash to simulate redelivery, as whole-batch duplicates and as single-row duplicates (`LIMIT 1 BY` a hash), and the dedup checks (9.4) compare before and after merges.

### 9.2 Scale steps

| Step | Devices | Days | Raw interface rows | Purpose |
| --- | --- | --- | --- | --- |
| 1x | 2,000 | 30 | 830 M | baseline |
| 4x | 8,000 | 30 | 3.3 G | fixed query scope, growing total |
| 16x | 32,000 | 30 | 13 G | ditto, and the `interface_name` cardinality check |
| scope sweep at 4x | scope of 10, 100, 1,000, 2,000 devices | | | fixed total, growing scope |

Expected: every query in 9.3 has `read_rows` flat across 1x, 4x, 16x at fixed scope (within 2x of the cost model, the slack covers part count `P`), and linear in scope on the sweep.

### 9.3 Queries and cost models to check

| Id | Query (section) | Cost model `read_rows` | Pass |
| --- | --- | --- | --- |
| Q1 | newest 200 for one interface (5.1) | `P x 16384` | flat across scale, p95 < 50 ms |
| Q2 | 7 d rate chart one interface on raw (5.1) | `P x 16384` | flat |
| Q3 | value at T (5.2) | `P x 16384` | flat |
| Q4 | 24 h scope, 2,000 devices, `interface_hourly_by_time` (5.3) | `<= 24 x E_tenant x P`, expected `~24 x E_scope x 1.3` after pruning | flat across scale, linear in scope |
| Q5 | top-20 by utilisation (5.4) | as Q4 plus GROUP BY | flat, p95 < 2 s, `memory_usage` < 1 GB |
| Q6 | 12 months hourly chart one interface (5.5) | `P x 2 x 8192` | flat |
| Q7 | 24 h scope on raw (the anti-query, D10) | `E_scope x P x 16384` | documented as linear in `E_scope` and rejected by `max_rows_to_read` |
| Q8 | 20 components of one device, 24 h (5.6) | `20 x P x 16384` | flat |
| Q9 | BGP peers with transitions in 24 h, tenant-wide (5.7) | `24 x peers_tenant x P` | flat |
| Q10 | hottest components in scope (5.6) | as Q4 on `component_hourly_by_time` | flat |

Variants to run for each: `index_granularity` 8192 vs 2048 on raw (Q2, Q7, Q8), a projection on `interface_hourly` instead of the second table (Q4, Q5), `Gorilla` vs `T64` on integer gauges (storage only), `LowCardinality(String)` vs `UInt64` hash key for `interface_name` (Q1 to Q5 and `system.columns` dictionary size at 16x).

### 9.4 Metrics

- Insert: rows/s per table, parts per insert from `system.part_log` (`NewPart` events per `query_id`, F67), merge time and `peak_memory_usage` of merges.
- Storage: `data_compressed_bytes`, `data_uncompressed_bytes`, `marks_bytes` per column from `system.columns` (`https://clickhouse.com/docs/operations/system-tables/columns.md`), bytes/row per table from `system.parts` (the time-series guide's query: "SELECT table, formatReadableSize(sum(data_uncompressed_bytes)) AS uncompressed, formatReadableSize(sum(data_compressed_bytes)) AS compressed, count() AS parts FROM system.parts", `https://clickhouse.com/docs/use-cases/time-series/storage-efficiency.md`), `primary_key_bytes_in_memory`.
- Per query: `read_rows`, `read_bytes`, `memory_usage`, `query_duration_ms` p50/p95 over 20 runs from `system.query_log` with `is_initial_query = 1` (F67), granules from `EXPLAIN indexes = 1` ("Shows used indexes, the number of filtered parts and the number of filtered granules for every index applied", `https://clickhouse.com/docs/sql-reference/statements/explain.md`) and `EXPLAIN ESTIMATE` ("Shows the estimated number of rows, marks and parts to be read"), exact ranges from `mergeTreeAnalyzeIndexes` (F72). Query cache off (`use_query_cache = 0`) and query condition cache both on and off for one run each.
- Dedup: after the redelivery pass, `SELECT count() - uniqExact(tenant_id, device_id, interface_name, ts)` on raw before and after `OPTIMIZE ... FINAL` on one closed partition (allowed in a benchmark, F18), and for each rollup column `uniqExactMerge(samples)` and the 4.4 increase per entity-hour compared with the same values computed on the deduplicated raw table. Pass: rollup values identical before and after dedup.
- Correctness of rates: compute the 4.4 increase from the rollup and from raw for 1,000 random entity-days, including days with a generated reset and days with jittered `discontinuity_at`, and compare with the generator's known increase. Pass: equal except for the lost pre-reset slice (bounded by `rate x I`).

### 9.5 Pass criteria

For each query: `read_rows` within 2x of the cost model at every scale step, and the ratio between 16x and 1x below 1.5 at fixed scope. For the scope sweep: `read_rows` ratio between 2,000 and 10 devices within 2x of 200. Storage: `interface_samples` under 40 B/row at 1x, `ethernet_samples` under 8 B/row. Parts per insert exactly one per table per partition touched. Dedup and rate checks as above.

## 10. Unverified

1. Bytes/row figures in section 7 are estimates from F25 and the baseline's AP figure, not measurements.
2. That exclusion search on `device_id IN (...)` within an hour prefix prunes to about `E_scope` granules (5.3); the sparse index guide describes exclusion search, not the `IN` case.
3. Primary-index memory at `index_granularity = 2048` (6.1) is arithmetic, not measured. Whether 26.8 loads the primary index lazily by default was not checked.
4. How often two counter resets fall inside one hour on real devices (4.4).
5. The 343 s wrap time at 100 Mbit/s is arithmetic from RFC 2863's "the minimum time in which a 32 bit counter will wrap decreases"; the RFC's own example figures were not in the fetched excerpt.
6. Global distinct count of interface names at 20,000 devices (1.5) and the resulting `LowCardinality` behaviour.
7. `Gorilla` on integer gauges: the codec page allows it, the compression guide praises it for floats; integer effect unmeasured.
8. `deltaSumTimestamp` behaviour was read from master, not from the 26.8 tag.
9. `optimize_aggregation_in_order` (default 0 per the settings page) was not evaluated for the scope queries; it may cut GROUP BY memory on key-ordered reads.
10. The size of `argMin`/`argMax` states and of `uniqExact(DateTime)` states on disk (rollup bytes/row).
11. `storage_samples` with two sources in one table assumes device-level and component-level storage are never both reported for the same storage; the schema does not forbid it.
12. The state projector as producer of interface change records is a proposal; no record names it.

## 11. Schema gaps (full pass)

Second pass over what devices and controllers report, against the FlowSeer messages for these domains. Sources: the IETF and vendor MIBs under `spec/mib/`, the atlas (`docs/research/network-domain-atlas/entities/02-interface.md`, `01-platform.md`, `04-gaps-and-recommendations.md`), dossiers 02 and 04 under `docs/research/schema-building-blocks/`, the lab captures and target notes under `docs/research/device-inventory/`, and the Ruckus protobuf under `spec/proto/ruckus/`. Gaps are numbered G1 to G22. Priority: A changes a rate or a status that the store would otherwise get wrong, B adds a signal operators ask for, C is a completeness item.

| # | Pri | Missing | Proposed addition | Evidence | Table change if added |
| --- | --- | --- | --- | --- | --- |
| G1 | A | Counter width. A 32-bit-only source wraps and looks like a reset (D8). Agents also "populate [HC counters] with zeros while filling the 32-bit ones" | `enum CounterWidth { UNSPECIFIED, BITS_32, BITS_64 }` field `width` on every `<Domain>Counters` message, set by the mapper from which column it read | `interface_counters.proto:13-14` reports only the 64-bit value; atlas `02-interface.md:148-152` "32-bit wrap at 1 GE is ~34 seconds", "plenty of agents populate them with zeros"; RFC 2863 section 3.1.6 | `counter_width UInt8` on every samples table. The 4.4 formula adds a wrap branch: when `width = 32` and `prev > 2^31 > cur` inside one epoch, `increase = 2^32 - prev + cur` |
| G2 | A | Delta-reported counters. Controllers report per-interval deltas, not cumulative values: Ruckus AP report fields carry `@property delta` and an `_r` suffix (`rxBytes_r`, `txBytes_r`, `rxFrames_r`, `txFail_r`), baseline: "Ruckus reports per-report deltas, Cisco cumulative per-association counters" | `enum CounterSemantics { CUMULATIVE, DELTA_SINCE_LAST_REPORT }` plus `google.protobuf.Duration report_window` on each `<Domain>Counters` message, or a sibling `<Domain>CounterDeltas` message | `spec/proto/ruckus/ap/ap_report.proto:184-196` ("@property delta", `uint64 rxBytes_r = 14`), `:256` "total delta bytes in one din period"; `baseline:135-137`; `interface_counters.proto:9-11` admits only monotonic values | `semantics UInt8` column. Raw rows dedup as before (same key, same bytes). The rollup cannot `sum` deltas idempotently, so delta rows get `AggregateFunction(groupUniqArray, Tuple(DateTime, UInt64))` per hour (12 tuples) and the reader sums the array. A sink that converts deltas to running totals is wrong under redelivery and must not be built |
| G3 | B | RMON frame-size histogram and error breakdown: `etherStatsDropEvents`, `CRCAlignErrors`, `UndersizePkts`, `OversizePkts`, `Fragments`, `Jabbers`, `Collisions`, `Pkts64Octets` to `Pkts1024to1518Octets`, 64-bit in `etherStatsHighCapacityTable` | `net/phy/v1/RmonEtherStatsCounters` with the 15 counters as `uint64` and `last_discontinuity`, carried on `EthernetFacet`; the mapper resolves `etherStatsDataSource` to the interface | `spec/mib/ietf/RMON-MIB:313-562`, `spec/mib/ietf/HC-RMON-MIB:509-537`; atlas `02-interface.md:126-128`, `:156-159` (the `etherStatsDataSource` join trap), `:160-164` "Not present: ... the frame size histogram"; MikroTik `stats.b` exposes histograms (`lab/labsw02-mikrotik-css326.md:133`) | New family table `rmon_samples`, same template as 3.1a. Histogram buckets are counters, so `Delta, ZSTD(1)`; most are active on every port, so this table compresses worse than `ethernet_samples` (estimate 10 to 25 B/row) |
| G4 | B | 802.3x PAUSE frames and flow-control mode: `dot3InPauseFrames`, `dot3OutPauseFrames`, `dot3HCInPauseFrames`, `dot3HCOutPauseFrames`, `dot3PauseOperMode`, `dot3PauseAdminMode` | `in_pause_frames = 14`, `out_pause_frames = 15` on `EthernetCounters`; `pause_oper_mode` enum on `EthernetFacet`, `pause_admin_mode` on `EthernetSettings` | `spec/mib/ietf/EtherLike-MIB:873-1066`; atlas `02-interface.md:227-229` "Missing: flow-control (802.3x PAUSE) state"; Cisco SMB and LANCOM SX ship the table (`spec/mib/cisco/smb/CISCOSBport_statistics.mib`, `spec/mib/lancom/sx/sx-5.30-ys7154cf/etherlike.mib`) | Two counter columns on `ethernet_samples`, `pause_oper_mode UInt8` on `interface_samples`, one more `attribute` value in `interface_changes` |
| G5 | C | `ifInUnknownProtos` (packets discarded for an unknown protocol) | `in_unknown_protocol_packets = 14` on `InterfaceCounters` | `spec/mib/ietf/IF-MIB:396`; present in LANCOM SX `if.mib` | One column on `interface_samples` |
| G6 | B | Vendor pre-computed rates and utilisation gauges, which are the only utilisation source when `speed = 0`: D-Link `dIfCounterIfUtilizationTable`, Comware `hh3cIfFlowStatTable`/`hh3cIfHCFlowStatTable` (rates), Huawei `hwIfEtherStatTable`, LCOS byte-transport tables | `net/interface/v1/InterfaceRates` with `in_bps`, `out_bps`, `in_pps`, `out_pps`, `in_utilization_basis_points`, `out_utilization_basis_points`, and `window` (rule 2 of the schema direction: statistic plus window), carried on `Interface` | atlas `02-interface.md:138-142`; `baseline:233-234` "About a quarter of up switch ports report speed 0, so utilisation cannot be computed from reported speed alone" | Six gauge columns (`T64, ZSTD(1)`) plus `rate_window_seconds` on `interface_samples`, and `max`/`avg` states in the rollups. Q5 (top-N) gains a fallback ranking term for `speed = 0` ports |
| G7 | B | Link flap counter: D-Link `dIfCounterIfLinkChangeTable`, Huawei `hwEntityIfUpTimes` | `link_changes = 15` on `InterfaceCounters` (a cumulative count of oper transitions) | atlas `02-interface.md:138`; `spec/mib/huawei/HUAWEI-ENTITY-EXTENT-MIB:777` | One counter column. Flap count per hour becomes `last - first` in the rollup without a changes table; the device counts the flaps between polls that sampling misses |
| G8 | B | Error-disable and protocol shutdown state with cause (UDLD, loop protect, BPDU guard, port security). `OperStatus` is the RFC 2863 seven-value set and has no "err-disabled" | `oper_status_reason` enum on `Interface` (none, error_disabled_bpdu_guard, error_disabled_udld, error_disabled_loop, error_disabled_port_security, error_disabled_other, suspended_lacp) | `spec/mib/cisco/smb/CISCOSBinterfaces_recovery.mib` (`errdisable`); atlas `02-interface.md:667-673` (UDLD "with errdisable as the action"), `ARUBAWIRED-LOOPPROTECT-MIB`, `DLINKSW-ERROR-DISABLE-MIB`, `DLINKSW-BPDU-PROTECTION-MIB` under `spec/mib/` | `oper_status_reason UInt8` on `interface_samples`, an `attribute` value in `interface_changes`. The down-port list query gains a reason column without a join |
| G9 | B | Speed for non-physical interfaces. `ifHighSpeed` exists on every ifXTable row (LAG, SVI, tunnel), but `speed_bps` lives only on `EthernetFacet` | `speed_bps` on `Interface` (top level) or on `LagInterface` and the other arms | `spec/mib/ietf/IF-MIB:815` (`ifHighSpeed`); RFC 2863 "For a sub-layer which has no concept of bandwidth, this object should be zero"; atlas `02-interface.md:83` "`ifSpeed` saturates at 4.29 Gbit/s" (mapper must prefer `ifHighSpeed`) | None in DDL (`speed_bps` is already a generic column). LAG utilisation becomes computable in Q5 |
| G10 | C | IP-layer per-interface counters with their own discontinuity: `ipIfStatsTable` (`HCInOctets`, `InHdrErrors`, `InNoRoutes`, `InAddrErrors`, `InTruncatedPkts`, `ReasmFails`, `OutFragFails`, multicast and broadcast octets, `ipIfStatsDiscontinuityTime`) per IP version | `net/ip/v1/IpInterfaceCounters` on `Ipv4Facet` and `Ipv6Facet` | `spec/mib/ietf/IP-MIB:1403-2214` | Family table `ip_interface_samples` keyed `(tenant, device, interface_name, ip_version, ts)` |
| G11 | A | Memory has no first-class carrier. RAM utilisation is reportable only as `StorageUtilization` with `kind = RAM`, and no comment says so. Vendors report it everywhere: Huawei `hwEntityMemUsage`, `hwEntityMemSize`, `hwEntityMemoryAvgUsage`, ICX `snAgGblDynMemUtil`, `snAgGblDynMemTotal`, `snAgGblDynMemFree`, Aruba `arubaWiredVsfMemberMemoryUtil`, `arubaWiredVsfMemberTotalMemory`, UniFi `memoryUtilizationPct` | Keep `StorageUtilization` as the carrier and state in its comment that RAM and swap go through `kind = RAM` / `VIRTUAL_MEMORY`; add `free_bytes` for sources that report free rather than used (ICX) so the mapper does not subtract | `storage_kind.proto:8-11`; `spec/mib/huawei/HUAWEI-ENTITY-EXTENT-MIB:723,773,783`; `spec/mib/ruckus/icx/FOUNDRY-SN-AGENT-MIB:1919-1937`; `spec/mib/aruba/cx/ARUBAWIRED-VSF-MIB:153,156`; dossier 04 `:226`; `lab/labsw03-huawei-s220.md:83` "Memory Size : 2048 M bytes" | `storage_samples` already holds it (3.3). Add `free_bytes UInt64` if the field lands. Query "memory on device D" filters `kind = 2` |
| G12 | A | CPU utilisation over several windows at once. Devices report 1 s / 5 s / 1 min (ICX), current / 1 min / 5 min (Huawei), 100 ms / 1 s / 10 s (LANCOM GS2326), 5 s / 1 min / 5 min (Cisco `cpmCPUTotal*Rev`, absent from the corpus). `DeviceState.processor_utilization` is one message with one `window` | `repeated ProcessorUtilization processor_utilization` on `DeviceState` and `ComponentState`, unique by `window`, with a CEL rule like the storage one | `device.proto:211-213` (single field); `spec/mib/ruckus/icx/FOUNDRY-SN-AGENT-MIB:1895-1911` (`snAgGblCpuUtil1SecAvg`, `5SecAvg`, `1MinAvg`), `:5160-5192` (`snAgentCpuUtilTable[slot, cpuId, interval]`); `lab/labsw03-huawei-s220.md:120` "CPU: 4% current, 9%/13% one/five-min average"; `lab/labsw04-lancom-gs2326.md:157` "CPU Load (100ms, 1s, 10s)"; dossier 04 trap 5 | `device_samples` and `component_samples` lose the two `cpu_*` columns. New `cpu_samples` keyed `(tenant, device, component_name, window_seconds, ts)` with `utilization_basis_points UInt16`, `component_name = ''` for the whole box. Rollup `max`/`argMax` per window. Charts filter one window |
| G13 | C | Per-core and per-slot CPU: ICX `snAgentCpuUtilTable[slot, cpuId]`, Huawei `hwEntityCpuUsage` per entity, Aruba VSF per member | None. `ComponentState` with `kind = CPU` or `STACK_MEMBER` already carries it per component (`component.proto:117-120`) | atlas `01-platform.md:473-485` "CPU and memory per `entPhysicalIndex`, the right shape" | None beyond G12 |
| G14 | A | Reboot detection. Only `uptime` exists and "a reboot is not inferred from it". `snmpEngineBoots` counts reboots, Aruba reports `arubaWiredVsfMemberBootTime`, Huawei prints `StartupTime` | `boot_count uint32` (snmpEngineBoots, cumulative) and `booted_at google.protobuf.Timestamp` on `DeviceState` | `device.proto:206-210`; atlas `04-gaps-and-recommendations.md:276` "Collect `snmpEngineBoots` | unambiguous reboot detection"; `spec/mib/aruba/cx/ARUBAWIRED-VSF-MIB:154`; `lab/labsw03-huawei-s220.md:82` "StartupTime 2026/04/26 17:09:55"; dossier 04 trap 6 | `boot_count UInt32 CODEC(Delta)` and `booted_at DateTime` on `device_samples`. "When did it reboot" becomes `DISTINCT booted_at` over one key range, exact, no changes table needed. `boot_count` increments also mark a counter epoch for every counters table of the device when the source reports no discontinuity time |
| G15 | B | Forwarding-table utilisation (MAC table, ARP/ND, route table, ACL TCAM): D-Link `DLINKSW-SRM-MIB`, Huawei `HUAWEI-RES-MON-MIB`, `inetCidrRouteNumber`, D-Link `DLINKSW-CPU-PROTECT-MIB` | `net/system/v1/ResourceUtilization { kind enum (mac_table, arp_table, nd_table, ipv4_routes, ipv6_routes, acl_tcam, nat_sessions, dhcp_bindings, other), used, total, used_basis_points }`, `repeated` on `DeviceState` unique by `(kind, name)` | atlas `01-platform.md:486-490` "report forwarding-table utilisation ... a far better operational signal than CPU load on a switch, and almost nothing collects it"; `spec/mib/ietf/IP-FORWARD-MIB` (`inetCidrRouteNumber`); `spec/mib/dlink/DLINKSW-CPU-PROTECT-MIB` | New table `resource_samples` keyed `(tenant, device, resource_kind, resource_name, ts)`, gauge columns, same template as `storage_samples`. This is also where the NAT session count gauge from 3.4 lands |
| G16 | C | Sensor operational status and value age: ENTITY-SENSOR-MIB `EntitySensorStatus` (ok, unavailable, nonoperational) and `entPhySensorValueTimeStamp`. `SensorReading` has only the value and thresholds | `status` enum and `measured_at` on `SensorReading` | dossier 04 `:138-140`; Cisco SMB `rlEntPhySensorTable` augments the standard table (`spec/mib/cisco/smb/CISCOSB_sensor.mib:41`) | `sensor_status_mask UInt16` (two bits per quantity) on `component_samples`. A reading with status unavailable keeps its last value on some agents, so the mask tells a flat line from a dead sensor |
| G17 | B | PSU input and output readings at once. A PSU reports input voltage and current and output power (openconfig-platform-psu). The "at most one reading per quantity" rule allows one `Voltage`, one `Current`, one `Power` per component | `side` enum (`INPUT`, `OUTPUT`) on `Voltage`, `Current`, `Power`, and the CEL rule becomes one reading per `(quantity, side)`. Dossier 04 trap 2 (AC vs DC) is the same field: `kind` (`AC`, `DC`) | `component.proto:106-115`; atlas `01-platform.md:300` "`openconfig-platform-psu` (`components/component/power-supply/state/{capacity, ...`"; MikroTik `mtxrHlPower`, `mtxrHlCurrent`, `mtxrHlVoltage` and `mtxrPOEVoltage`, `mtxrPOECurrent` (`spec/mib/mikrotik/MIKROTIK-MIB:1358-1393, 3002-3003`); dossier 04 trap 2 | `component_samples` gains `input_voltage_microvolts`, `input_current_microamperes`, `output_power_nanowatts` beside the existing columns (or a `side` key column, which doubles rows; the columns are cheaper) |
| G18 | C | PSU capacity and fan tray state: `arubaWiredFanTrayState`, `mtxrHlPowerSupplyState`, `mtxrHlBackupPowerSupplyState`, `hwEntityFanState`, `hwEntityFanSpdAdjMode`, PSU `capacity` | `ComponentOperStatus` covers up/down. Add `rated_power_nanowatts` on `ComponentState` for PSUs (capacity is static, so a changes-table field) | `spec/mib/aruba/cx/ARUBAWIRED-FANTRAY-MIB:39`; `spec/mib/mikrotik/MIKROTIK-MIB:1407-1414`; `spec/mib/huawei/HUAWEI-ENTITY-EXTENT-MIB:3534,174`; atlas `01-platform.md:295-321` | `component_changes` attribute, no samples change. "PSU load %" in a dashboard divides `output_power` by `rated_power` |
| G19 | C | BGP peer last error and update age: `bgpPeerLastError` (two octets: error code and subcode), `bgpPeerInUpdateElapsedTime` | `last_error_code`, `last_error_subcode uint32` and `in_update_elapsed Duration` on `BgpPeer` | `spec/mib/ietf/BGP4-MIB` (`bgpPeerLastError`, `bgpPeerInUpdateElapsedTime`), also in LANCOM SX `bgp.mib` | Two `UInt8` columns and `in_update_elapsed_seconds UInt32` on `bgp_peer_samples`; the error pair becomes an `attribute` in a `bgp_peer_changes` table |
| G20 | B | Protocol event counters without a carrier: OSPF `ospfNbrEvents`, `ospfIfEvents`; LLDP `lldpStatsRxPortFramesTotal`, `...FramesDiscardedTotal`, `...FramesErrors`, `...TLVsDiscardedTotal`, `...AgeoutsTotal`, `lldpStatsTxPortFramesTotal`; BRIDGE-MIB `dot1dStpTopChanges` and `dot1dStpTimeSinceTopologyChange` at bridge level (`MstInstance.topology_changes` covers MSTP instances only) | `OspfNeighborCounters { events }`, `OspfInterfaceCounters { events }`, `LldpPortCounters` (6 counters), `topology_changes` and `time_since_topology_change` on the STP bridge state message, each with `last_discontinuity` | `spec/mib/ietf/OSPF-MIB`, `spec/mib/ieee/LLDP-MIB`, `spec/mib/ietf/BRIDGE-MIB`; `mst_instance.proto:49` (`uint64 topology_changes = 8` exists for MST only) | Three more family tables on the 3.4 template: `ospf_neighbor_samples` keyed `(area, interface_name, neighbor_router_id)`, `lldp_port_samples` keyed `(interface_name)`, and columns on an `stp_bridge_samples`. The generalisation test passes for all three (stable key, counters message) |
| G21 | C | Controller-reported device-level totals: `ruckusSZAPRXBytes`, `ruckusSZAPTXBytes`, `ruckusSZAPUptime`, `ruckusSZAPNumSta`, and the SZ MIB reports no AP CPU or memory at all | No new carrier. AP byte totals are the sum of radio and Ethernet counters that other domains carry; `uptime` maps to `DeviceState.uptime`; `NumSta` is a wireless-domain gauge. Record the absence of AP CPU/memory in the Ruckus adapter notes | `spec/mib/ruckus/wireless/RUCKUS-SZ-WLAN-MIB:206,215,223-224`; the Ruckus AP protobuf carries no CPU or memory field (grep of `spec/proto/ruckus/ap/*.proto` for cpu and memory finds none) | None |
| G22 | C | A string-named counter bag: Cisco SMB `CISCOSB-PORT-STATISTICS-MIB` is keyed `[ifIndex, subtype, counterName, statID]` | None in the schema. The mapper maps known counter names to the typed fields of G3, G4, and `InterfaceCounters`, and drops the rest. This is the one source where a narrow table would be natural, and it is the reason section 2.3 still rejects one: a bag of vendor names has no unit, no width, and no discontinuity semantics to store | atlas `02-interface.md:139` "a string-named counter bag, not fixed columns" | None |

### How the table design changes if the A and B items land

- `interface_samples` gains `counter_width UInt8` (G1), `semantics UInt8` and `rate_window_seconds` (G2, G6), six rate and utilisation gauges (G6), `link_changes` (G7), `oper_status_reason` (G8), `pause_oper_mode` (G4), and `in_unknown_protocol_packets` (G5). About 11 columns, mostly constant per entity, so a few bytes per row. `present_mask` widens to `UInt64`.
- `ethernet_samples` gains two PAUSE counters (G4) and `counter_width`.
- New family tables, same template: `rmon_samples` (G3), `ip_interface_samples` (G10), `cpu_samples` (G12, replacing the CPU columns on `device_samples` and `component_samples`), `resource_samples` (G15), `ospf_neighbor_samples`, `lldp_port_samples`, `stp_bridge_samples` (G20). Each adds its own entity-first and time-first hourly rollups.
- `device_samples` gains `boot_count` and `booted_at` (G14) and loses the CPU columns (G12). `storage_samples` gains `free_bytes` (G11). `component_samples` gains three PSU-side columns (G17) and a `sensor_status_mask` (G16).
- The 4.4 increase formula gains two branches: a wrap branch when `counter_width = 32` (G1) and a delta branch that sums `groupUniqArray` tuples when `semantics = DELTA` (G2). Both are per-row flags, so one query template serves every family.
- Rollup aggregates stay idempotent throughout: the delta case uses a set of `(ts, delta)` tuples rather than `sum`, and every gauge uses `max`, `min`, or `argMax`.
- Cost models in section 5 do not change shape. Rows per entity per poll stay one for every table except the per-window `cpu_samples` (one row per window, 1 to 3) and the per-version `ip_interface_samples` (1 to 2).

### What the pass did not find

No vendor source in the corpus contradicts the per-family wide table (D1) or the name key (D2). The counter bag (G22) and the delta-reporting controller (G2) are the two shapes that strain the model, and both are resolved by a mapper rule plus a per-row semantics flag rather than by a different table shape. Ruckus ICX and FASTPATH per-port tables, Huawei `hwIfEtherStatTable`, and LCOS transport tables all reduce to G3, G4, G6, and G7.
