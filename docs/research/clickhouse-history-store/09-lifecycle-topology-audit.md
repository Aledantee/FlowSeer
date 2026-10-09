---
title: Lane F. Inventory, topology, lifecycle, audit history, and as-of dimensions in ClickHouse
date: 2026-10-08
status: research report for the history store plan. Repository read-only. Sources fetched 2026-10-08.
---

# Lane F: FlowSeer-generated history (inventory transitions, lifecycle, drift, audit, as-of dimensions)

Builds on `docs/research/clickhouse-history-store/` (cited as 01, 02 F-numbers, 03). Repository
paths are relative to the worktree. "Inference" marks reasoning from quoted sources. "Unverified"
marks claims no fetched source confirms. Findings are numbered L1 to L22.

## 0. Summary

| Domain | Recommended model | Why in one line |
| --- | --- | --- |
| Entity transitions (Device, Component, Binding, Integration, IntegrationScope, Link, Edge, CaptureSession, Tenant, Placement) | One `entity_events` table: typed common columns plus the serialized `before` and `after` protobuf bytes, `ReplicatedReplacingMergeTree` keyed by `(tenant_id, entity_kind, entity_id, ts, seq, record_id)` | Ten kinds share one shape (ref, before/after or from/to), volumes are tiny, and every query is "events of this entity" or "events of this tenant in a window" |
| Binding reachability | A typed `binding_reachability` table beside the generic one | The only inventory domain with a numeric question ("how long unreachable", "which devices were down at T") and the only one that can flood |
| Topology (Link) | `link_events` typed, one row per event, with both ends as columns and a bloom filter on the far end | Queried from either end and by foreign chassis id, which the generic entity key cannot serve |
| Lifecycle (firmware, licence) | Not events. `device_software_images` and `device_licenses` as presence rows (AggregatingMergeTree `min(first_seen)`, `max(last_seen)`) fed from inventory reads, plus `FirmwareEpochChanged` in the audit table | SoftwareImage and License are state rows read on every inventory pass (set-valued per device), not transitions |
| Drift | Rows of the audit table (`DriftDetected`), plus `interface_observations` for the observation that proved it | Drift is already an audit event with `expected` and `observed` attributes |
| Audit (DeviceOperationEvent, OperatorActionEvent) | Two typed tables, append-only by grant, `seq` from the JetStream stream, retention by class, ClickHouse as the store and JetStream as the transport | JetStream today is byte-bounded with `DiscardOld` and no per-tenant fairness. ClickHouse gives retention by rule and a reader that does not consume the stream |
| As-of dimension (device at site at T) | `device_placements` table mirrored from inventory, a `complex_key_range_hashed` dictionary over it, and `site_id` stamped on fact rows at write time | Write-time stamp answers "scope last 24 h" with no join. Dictionary answers "as it was at T". ASOF JOIN for ad hoc |

Volume: this whole lane writes under 0.5 M rows per day at 20,000 devices (section 2), under 0.2 % of
the interface samples lane (20,000 devices x 48 interfaces x 288 polls = 276 M rows/day). The design
problem here is correctness, retention, and reconstruction, not throughput.

## 1. What the repository gives this lane

**L1. The Event messages have two shapes.** Lifecycle-only events carry `from` and `to` of one enum:
`DeviceEvent` (`spec/proto/flowseer/model/inventory/v1/device.proto:220-244`), `IntegrationEvent`
(`integration.proto:184-208`), `EdgeEvent` (`model/edge/v1/edge.proto:166-190`),
`CaptureSessionEvent` (`model/capture/v1/capture_session.proto:254-278`), `TenantEvent`
(`model/identity/v1/tenant.proto:87-111`). Before/after events carry the whole State on both sides:
`ComponentEvent` (`component.proto:219-250`), `BindingEvent` (`binding.proto:164-201`),
`IntegrationScopeEvent` (`integration_scope.proto:70-101`), `LinkEvent` (`link.proto:148-177`),
`PlacementEvent` (`placement.proto:75-119`, `after` required because "placements are never removed").
A before/after event is a snapshot of the entity, so the latest event at or before T reconstructs the
entity at T without replay (section 6). A from/to event reconstructs only the lifecycle axis.

**L2. No producer exists yet.** `grep -rln "DeviceEvent\b|BindingEvent\b|PlacementEvent\b|LinkEvent\b|ComponentEvent\b" src --include='*.go'`
returns nothing outside generated code. The only events written to a stream today are
`DeviceOperationEvent` (`src/services/device/internal/centralaudit/centralaudit.go:122-141`, subject
`flowseer.<tenant>.audit.device.<device>`, message id = `event_id`) and `OperatorActionEvent`
(`src/services/device/internal/actiontrail/interceptor.go:237,510`). Inventory state lives in
JetStream KV buckets (`src/modules/edgebus/subjects.go:43-54`) and is read with its revision
(`src/services/device/internal/journal/journal.go:143`, `edgestore/store.go:87`,
`tenantstore/store.go:105`). So the sink for this lane is a KV watcher (or the projector the ingestion
record names) that diffs revisions into Event rows, not a consumer of an existing event stream.
Inference: the sequence to store is the KV revision (section 5), which the NATS docs define as "the
number the server assigns to a write" drawn from "one counter across all of its keys", so "a key's
revision always increases on each write, but not by one" (`https://docs.nats.io/learn/key-value/history-and-revisions`).

**L3. Two State fields are heartbeats, not transitions.** `BindingState.last_success` ("When a request
over this binding last succeeded", `binding.proto:135`) advances on every successful poll, and
`LinkState.last_seen` ("When the link was last reported", `link.proto:140`) advances on every LLDP
read. A diff-to-event sink that treats every State change as an Event would emit one BindingEvent
per device per poll: 20,000 devices / 300 s = 67 rows/s = 5.8 M rows/day, each carrying two
serialized BindingStates. That is a samples stream in an events table. The sink must exclude
`last_success` and `last_seen` (and `EdgeState.last_seen_at`, `edge.proto:152`) from the change
test, and the samples lane (or a small `binding_health` sample table) should carry them. Section 4.2
makes `binding_reachability` carry `last_success` only when the status changes, which is when it
matters ("down since", "last good before the outage").

**L4. Audit today is bounded by bytes, not by time.** The audit stream is `FLOWSEER_DEVICE_AUDIT`,
`LimitsPolicy`, `MaxBytes` 256 MiB default, no `Discard` set so the JetStream default `Old` applies
(`src/modules/edgebus/hub.go:66-68,397-409`), and the NATS docs describe `Old` as "When the stream
finally hits a limit, the oldest messages are deleted to make room"
(`https://docs.nats.io/nats-concepts/jetstream/streams`). `FLOWSEER_OPERATOR_ACTIONS` is 64 MiB with
10,000 messages per subject (`hub.go:69-74,412-428`), shared by every tenant, and the authorization
record states the eviction risk: "A tenant admin who repeats changes can fill 18 change subjects at
their cap, about 70 MiB, and push out the oldest change records of other tenants"
(`docs/architecture/2026-09-30-operator-authorization-direction.md:895-899`), with the per-tenant
stream left open (`:1042-1048`). Section 8 evaluates ClickHouse against that.

**L5. Protobuf bytes are safe to persist, type names are not.**
`docs/solutions/architecture-patterns/a-package-rename-breaks-names-you-persisted-not-records-you-encoded.md`:
"A serialized protobuf message carries field numbers and wire types, and no package name, message
name, or descriptor" and the exception "a format that stores type names with the data, such as
`google.protobuf.Any` ... those carry the name and change with it". Applied: store the serialized
State bytes in a `String` column (ClickHouse: "The value can contain an arbitrary set of bytes,
including null bytes", "The String type replaces the types VARCHAR, BLOB, CLOB"
`https://clickhouse.com/docs/sql-reference/data-types/string.md`) and never a full type name. The
row's `entity_kind Enum8` is FlowSeer's own closed enumeration, mapped in Go to the message type at
read time, so a package rename changes one Go switch and no stored data. Field renumbering does
break stored bytes ("What this does not cover"), which `docs/code-style-proto.md` already forbids.

**L6. ClickHouse cannot decode the bytes.** No fetched ClickHouse page documents a SQL function that
decodes a protobuf `String` column (unverified that none exists). The protobuf input format only
decodes at insert (01 section 8). Consequence: every field a query filters, groups, or sorts on must
be a typed column written by the sink, and the bytes serve fidelity (show the full before/after in
the UI, re-derive a column later by a Go backfill). This is the "mixed explicit columns plus one
opaque column" shape the JSON best-practice page prefers (02 F13) and the materialized-hot-columns
pattern of PostHog, Uber, and SigNoz (03 patterns).

## 2. Data semantics and volume at 20,000 devices per node

| Kind | Entity key | Trigger | Rows/day, normal | Rows/day, storm | Payload per row (uncompressed, inference from field limits) |
| --- | --- | --- | --- | --- | --- |
| Device lifecycle | device UUID | operator action or missing threshold (`device.proto:30-33`) | tens | 20,000 (site outage marks every device missing) | 2 enums |
| Component | device UUID + name (`component.proto:23-32`, up to 256 B) | inventory read diff: oper_status, serial, firmware_revision, appearance | 50 components x 20,000 x 0.1 % = 1,000 | 50 x 20,000 = 1 M (firmware upgrade wave rewrites every component's `software_revision`) | 2 x ComponentState, 200 to 600 B each |
| Binding | binding UUID | status, failure_kind, address change | 2 % of 20,000 flapping x 2 = 800 | 40,000 (every binding unreachable then back) | 2 x BindingState, about 150 B each |
| Integration, IntegrationScope | integration UUID, integration + platform_id | sync diff | tens | thousands (platform renames every scope) | 2 x State, under 300 B |
| Link | link UUID | first seen, status active/stale/gone, cable reconciliation | 20,000 x 8 links x 1 % = 1,600 | 160,000 (a core switch reboot stales every link) | 2 x LinkState, about 200 B |
| Edge, CaptureSession, Tenant | UUID | operator action, enrollment | tens | hundreds | 2 enums |
| Placement | placement UUID | operator move or scope sync | tens | 20,000 (bulk re-placement) | 2 x Placement, about 120 B |
| DeviceOperationEvent | device UUID + event_id | mutation phases (about 5 per mutation), discovery, drift, lane state | 1,000 mutations x 5 + 20,000 discoveries = 25,000 | 200,000 | attributes map up to 16 pairs, correlation ids up to 8 |
| OperatorActionEvent | event_id | attempted + completed per RPC, including views (`operator_action_event.proto:18-40`) | 100,000 | 1 M (a dashboard polling list calls) | about 410 B (`operator-authorization-direction.md:881-883`) |
| SoftwareImage, License | device + network_instance + slot / name | every inventory read (set-valued state) | presence rows, not events: 20,000 x (2 images + 5 licences) = 140,000 rows per day bucket | same | SoftwareImage 6 fields, License 7 fields |

Totals: normal day about 130,000 event rows plus 140,000 presence rows. Storm day under 2.5 M. For
comparison the samples lane writes 276 M interface sample rows per day. Inference: no table in this
lane needs partitions finer than a month, and no insert rate in this lane needs async-insert batching
beyond what the sink already does for other lanes (02 F14 to F16).

Storms are the design case, not the average. A firmware wave or a core reboot produces a burst that
is correlated by tenant and by time, which the ORDER BY below keeps contiguous.

## 3. Candidate models

### 3.1 One generic `entity_events` versus a table per kind

| | Generic (tenant, entity_kind, entity_id, ts, seq, event_kind, typed hot columns, payload bytes) | Table per kind with full typed columns |
| --- | --- | --- |
| Query "when did entity X change" | One primary-key range | One primary-key range |
| Query "what changed in tenant T in the last 24 h" (the activity feed) | One table, one range per kind, or a `ts` minmax skip index across kinds | A UNION over ten tables |
| Adding an entity kind | One Enum8 value (metadata-only: "extending an enum" is listed as a metadata-only type change, `https://clickhouse.com/docs/sql-reference/statements/alter/column.md`) and no DDL | A table, a migration, a sink branch |
| Field-level filters (failure_kind, link end) | Need a hot column, else decode in Go | Native |
| Schema drift when a State gains a field | Bytes already carry it. Add a hot column only if queried | ADD COLUMN per table |
| Prior art | PostHog `events` (team_id, event, ts, JSON properties plus materialized columns), Snuba errors (typed plus Nested tags), 03 comparison table | Akvorado, Zabbix per value type |
| Risk | Generic tables that hold heterogeneous *measurements* paid in Map promotion and full scans (03 anti-patterns: ntopng, Telegraf). That cost came from unknown schemas and attribute Maps, not from a kind column | Ten near-identical tables of under 10,000 rows per day each |

Inference: the anti-pattern evidence is against open-ended attribute maps as the only structure, not
against a kind discriminator over a known closed set. FlowSeer's ten kinds share the Event contract
(`docs/conventions/protobuf.md` "The triad": an Event "that does not know both sides cannot describe
a transition"), the volumes are tiny, and the activity-feed query spans kinds. One generic table with
typed hot columns wins for inventory transitions. Split out only the two kinds whose query shapes the
generic key cannot serve: binding reachability (numeric durations, "down at T" across a scope) and
links (lookup from either end). Audit stays in its own two tables because its retention, grants, and
readers differ (section 8).

### 3.2 How to store the payload

Three options for the Event payload:

1. **Typed columns only.** Loses fields nobody thought to column (L6 makes a later backfill
   impossible without the bytes).
2. **JSON type.** "Your data has a dynamic or unpredictable structure" is the JSON criterion (02
   F13). FlowSeer's structure is fixed by the schema. JSON would also persist field *names*, which a
   rename changes (L5).
3. **Typed hot columns plus serialized protobuf bytes for `before` and `after`.** Chosen. The bytes
   are the fidelity copy (L5), the columns are what queries touch (L6).

Codec for the bytes: `ZSTD(1)` per 02 F25 ("ZSTD level 1 is the starting point"). Two consecutive
States of one entity differ in a few fields, and the sort key keeps one entity's rows adjacent, so
ZSTD's window sees near-duplicate byte strings (inference). A measurement in the benchmark (section 10)
decides between `ZSTD(1)` and `ZSTD(3)` for the two payload columns.

### 3.3 Ordering by sequence versus by time

`ts` is the event's `occurred_at` or the observation time from `Provenance.observed_at`
(`provenance.proto:18`), set by the producer's clock. `seq` is the KV revision (L2) or the JetStream
stream sequence for audit. Two writers, a clock step, or a replayed edge buffer can put a later `seq`
at an earlier `ts`. Rules:

- Sort key ends `(ts, seq, record_id)`: `ts` first because every query has a time window and
  `optimize_read_in_order` serves newest-first (02 F51), `seq` second so two events in one second
  order correctly.
- "Latest state at T" uses `argMax(after, (ts, seq))` (02 F41: "`argMax(a, (b, a))` breaks ties with
  a tuple"), never `argMax(after, ts)` alone.
- Gap detection uses `seq` only (section 8.4).
- Audit `seq` is the stream sequence of the message the sink read. The NATS docs say only "The first
  message you publish gets sequence `1`"; that stream sequences are strictly increasing and never
  reused within a stream is unverified from the fetched page and must be checked in
  `nats-server@v2.15.0/server/filestore.go` before the gap query is trusted.

## 4. Recommendation and DDL

Conventions from 02 section 12 apply: database `flowseer`, `Replicated*` engines, `tenant_id
LowCardinality(String)`, `device_id UUID`, no Nullable (02 F9), `retention_class Enum8` stamped by
the sink (02 F36), `ttl_only_drop_parts = 1`. Dates for absent timestamps are stored as `0`
(`1970-01-01 00:00:00`) with the convention "0 means unset", the protobuf presence rule mapped to a
sentinel, because Nullable "introduce[s] additional overhead" (02 F9).

### 4.1 `entity_events` (all inventory kinds)

```sql
CREATE TABLE flowseer.entity_events
(
    tenant_id       LowCardinality(String),
    entity_kind     Enum8('device' = 1, 'component' = 2, 'binding' = 3, 'integration' = 4,
                          'integration_scope' = 5, 'link' = 6, 'edge' = 7,
                          'capture_session' = 8, 'tenant' = 9, 'placement' = 10),
    -- The entity's key as a string: the UUID for UUID-keyed kinds, "<device uuid>/<name>" for
    -- components (component.proto:23-32), "<integration uuid>/<platform_id>" for scopes
    -- (integration_scope.proto:13-21). Bounded by the schema's max_len rules.
    entity_id       String            CODEC(ZSTD(1)),
    ts              DateTime          CODEC(Delta, ZSTD(1)),
    seq             UInt64            CODEC(Delta, ZSTD(1)),   -- KV revision of the write that produced the after side
    record_id       UUID,                                      -- IngestRecord.record_id / sink-assigned UUIDv7
    event_kind      Enum8('created' = 1, 'transitioned' = 2, 'removed' = 3),
    -- Hot columns. For from/to kinds the enum number of the kind's own lifecycle enum
    -- (DeviceLifecycle, IntegrationLifecycle, EdgeLifecycle, CaptureLifecycle, TenantLifecycle);
    -- for before/after kinds the status field of the State (BindingStatus, LinkStatus,
    -- ComponentOperStatus). 0 when the kind has no status axis.
    status_from     UInt8             CODEC(T64, ZSTD(1)),
    status_to       UInt8             CODEC(T64, ZSTD(1)),
    -- The owning device for device-scoped kinds (component, binding, link end a, placement); zero UUID otherwise.
    device_id       UUID,
    -- Who or what caused it: operator ref string, "system", or the integration id. Bounded.
    actor           LowCardinality(String),
    -- Serialized State (or Placement / lifecycle-only marker) on each side. Empty when the side is absent.
    before          String            CODEC(ZSTD(1)),
    after           String            CODEC(ZSTD(1)),
    -- Which .proto schema revision wrote the bytes (a FlowSeer counter, not a type name; L5).
    schema_rev      UInt16            CODEC(T64, ZSTD(1)),
    retention_class Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    site_id         LowCardinality(String),                    -- device's site at write time (section 9)
    INDEX idx_ts     ts        TYPE minmax GRANULARITY 1,
    INDEX idx_device device_id TYPE bloom_filter(0.01) GRANULARITY 4
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY toYYYYMM(ts)
ORDER BY (tenant_id, entity_kind, entity_id, ts, seq, record_id)
TTL ts + INTERVAL 2 YEAR DELETE WHERE retention_class = 'short',
    ts + INTERVAL 5 YEAR DELETE WHERE retention_class IN ('standard', 'long')
SETTINGS ttl_only_drop_parts = 1, min_age_to_force_merge_seconds = 2592000,
         min_age_to_force_merge_on_partition_only = 1;
```

Reasoning per clause.

- ORDER BY: tenant first (02 F6), then the kind (10 values, lowest cardinality after tenant, 02 F4
  "ascending order of cardinality"), then the entity so one entity's history is one contiguous range,
  then time, sequence, record id (section 3.3, dedup in section 5). `device_id` is not in the key
  because the component, binding, and link kinds key on their own id, and "all events for device D"
  is served by the bloom filter plus the tenant prefix (02 F19: "Rare values that are critical for
  search (e.g. ... specific IDs)"). The benchmark verifies the skip with `EXPLAIN indexes=1`.
- PARTITION BY month: at 130,000 rows/day a monthly partition is about 4 M rows, retention of 2 to 5
  years gives 24 to 60 partitions, well under the "fewer than 100 - 1,000" guidance (02 F7), and an
  insert touches one partition.
- TTL: long retention because the table is small and "when did X change" questions reach back years
  (a device retired three years ago, a scope renamed last year). Two classes, both long. The numbers
  are placeholders for the plan.
- `min_age_to_force_merge_seconds` 30 days: closed months merge to one part so FINAL, when used, reads
  one part per partition (02 F40, F48).
- `idx_ts` minmax at granularity 1: the activity feed ("what changed in tenant T since yesterday")
  filters by tenant and time with no kind or entity, and the key puts time fourth. Rows arrive in time
  order within each insert, so within a tenant+kind prefix the granules are time-ordered and the
  minmax index prunes (02 F19 "a strong correlation between the indexed column and the table's primary
  key"). The benchmark measures it (Q3).
- `status_from`/`status_to` as UInt8 enum numbers rather than ten Enum8 columns: the UI maps them
  through the kind's enum, and the enum numbers are frozen by `docs/code-style-proto.md`. A reviewer
  may prefer `LowCardinality(String)` with the enum *name*; that persists a name (L5) and is rejected.

### 4.2 `binding_reachability` (typed, from BindingEvent)

Written for every BindingEvent whose `status` or `failure_kind` changed (`binding.proto:34-66`,
`:102-160`). The row is the after side's reachability facts.

```sql
CREATE TABLE flowseer.binding_reachability
(
    tenant_id         LowCardinality(String),
    device_id         UUID,
    binding_id        UUID,
    ts                DateTime          CODEC(Delta, ZSTD(1)),
    seq               UInt64            CODEC(Delta, ZSTD(1)),
    record_id         UUID,
    integration_id    UUID,
    protocol          Enum8('unspecified' = 0, 'snmp' = 1, 'ssh' = 2, 'netconf' = 3, 'rest' = 4),  -- ManagementProtocol numbers; take the list from binding.proto at build time
    status_from       Enum8('unspecified' = 0, 'candidate' = 1, 'verified' = 2, 'degraded' = 3, 'unreachable' = 4, 'retired' = 5),
    status_to         Enum8('unspecified' = 0, 'candidate' = 1, 'verified' = 2, 'degraded' = 3, 'unreachable' = 4, 'retired' = 5),
    failure_kind      Enum8('none' = 0, 'timeout' = 1, 'auth_failed' = 2, 'management_unresponsive' = 3),
    last_success      DateTime          CODEC(Delta, ZSTD(1)),  -- BindingState.last_success on the after side, 0 if none
    unreachable_since DateTime          CODEC(Delta, ZSTD(1)),  -- set exactly while status_to = unreachable (binding.proto:103-107)
    retention_class   Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    site_id           LowCardinality(String),
    INDEX idx_ts ts TYPE minmax GRANULARITY 1
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY toYYYYMM(ts)
ORDER BY (tenant_id, device_id, binding_id, ts, seq, record_id)
TTL ts + INTERVAL 1 YEAR DELETE WHERE retention_class = 'short',
    ts + INTERVAL 2 YEAR DELETE WHERE retention_class IN ('standard', 'long')
SETTINGS ttl_only_drop_parts = 1, min_age_to_force_merge_seconds = 2592000,
         min_age_to_force_merge_on_partition_only = 1;
```

Device second in the key because reachability questions are per device or per device list ("which
devices in site S were unreachable last night"), and device resolves the scope (02 F6 applied).
The `ManagementProtocol` and `BindingFailureKind` enum lists must be generated from the `.proto` at
build time, never typed by hand, so the Enum8 numbers equal the protobuf numbers.

A `binding_downtime_daily` rollup (section 7) turns these transitions into per-day unreachable
seconds, which is the chart and the SLA number.

### 4.3 `link_events` (typed, from LinkEvent)

```sql
CREATE TABLE flowseer.link_events
(
    tenant_id        LowCardinality(String),
    link_id          UUID,
    ts               DateTime          CODEC(Delta, ZSTD(1)),
    seq              UInt64            CODEC(Delta, ZSTD(1)),
    record_id        UUID,
    event_kind       Enum8('created' = 1, 'transitioned' = 2, 'removed' = 3),
    source           Enum8('unspecified' = 0, 'lldp' = 1, 'cdp' = 2, 'lacp' = 3, 'fdb_inference' = 4),
    status_from      Enum8('unspecified' = 0, 'active' = 1, 'stale' = 2, 'gone' = 3),
    status_to        Enum8('unspecified' = 0, 'active' = 1, 'stale' = 2, 'gone' = 3),
    -- End a and end b exactly as LinkState carries them (link.proto:79-96). A foreign system has a
    -- zero device id and a chassis id.
    a_device_id      UUID,
    a_interface      LowCardinality(String),
    a_chassis_id     String            CODEC(ZSTD(1)),
    b_device_id      UUID,
    b_interface      LowCardinality(String),
    b_chassis_id     String            CODEC(ZSTD(1)),
    first_seen       DateTime          CODEC(Delta, ZSTD(1)),
    cable_id         UUID,
    before           String            CODEC(ZSTD(1)),
    after            String            CODEC(ZSTD(1)),
    retention_class  Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    INDEX idx_a a_device_id TYPE bloom_filter(0.01) GRANULARITY 4,
    INDEX idx_b b_device_id TYPE bloom_filter(0.01) GRANULARITY 4,
    INDEX idx_chassis (a_chassis_id, b_chassis_id) TYPE bloom_filter(0.01) GRANULARITY 4,
    INDEX idx_ts ts TYPE minmax GRANULARITY 1
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY toYYYYMM(ts)
ORDER BY (tenant_id, link_id, ts, seq, record_id)
TTL ts + INTERVAL 2 YEAR DELETE WHERE retention_class = 'short',
    ts + INTERVAL 5 YEAR DELETE WHERE retention_class IN ('standard', 'long')
SETTINGS ttl_only_drop_parts = 1, min_age_to_force_merge_seconds = 2592000,
         min_age_to_force_merge_on_partition_only = 1;
```

`last_seen` is deliberately absent (L3). "Topology of device D at T" is `argMax` over links where
`a_device_id = D OR b_device_id = D` (section 6, Q5). `LinkState` says "Which end is a and which is b
carries no meaning; the service compares ends as a set" (`link.proto:114-115`), so the sink must
order ends canonically (lower UUID first) before writing, or the `OR` query is mandatory. Canonical
ordering is recommended: it halves the bloom lookups and makes `a_device_id` the only end a
device-scoped UI needs for half of the links.

### 4.4 Firmware and licence presence

`SoftwareImage` (`net/system/v1/software_image.proto:9-36`) and `License`
(`license.proto:11-41`) are primitives read with every inventory pass, keyed by
`network_instance` + `slot` or `name`. They are set-valued per device, like FDB entries, and 03
"set-valued domains" applies: a presence row per member and day bucket with idempotent `min`/`max`.

```sql
CREATE TABLE flowseer.device_software_images
(
    tenant_id        LowCardinality(String),
    device_id        UUID,
    network_instance LowCardinality(String),
    slot             LowCardinality(String),
    version          LowCardinality(String),           -- SoftwareImage.version; part of the key so a version change is a new row
    day              Date,
    first_seen       SimpleAggregateFunction(min, DateTime),
    last_seen        SimpleAggregateFunction(max, DateTime),
    running          SimpleAggregateFunction(max, UInt8),  -- 1 if running was observed true in the bucket
    next_boot        SimpleAggregateFunction(max, UInt8),
    size_bytes       SimpleAggregateFunction(max, UInt64),
    retention_class  Enum8('short' = 1, 'standard' = 2, 'long' = 3)
)
ENGINE = ReplicatedAggregatingMergeTree
PARTITION BY toYYYYMM(day)
ORDER BY (tenant_id, device_id, network_instance, slot, version, day)
TTL day + INTERVAL 2 YEAR DELETE WHERE retention_class = 'short',
    day + INTERVAL 5 YEAR DELETE WHERE retention_class IN ('standard', 'long')
SETTINGS ttl_only_drop_parts = 1;
```

`device_licenses` is the same shape keyed by `(tenant_id, device_id, network_instance, name, day)`
with `status Enum8`, `issued_at`, `expires_at` as `SimpleAggregateFunction(max, DateTime)` (a licence's
`expires_at` can be extended, so `max` is the right merge) and `entitlement_count max`. "Which
devices ran version V on date D" is a primary-key range per device plus a `version` filter. "When did
device D move from V1 to V2" is the first `day` where V2 has `running = 1`, resolution one day. The
`FirmwareEpochChanged` audit event (`operation_event.proto:127-140`) carries the exact moment with
fingerprints, and `DeviceState.software_version` (`device.proto:182`) is not carried by any
`DeviceEvent` (L1), so firmware history lives in these two places. Inference: that gap is worth a
schema note in the plan. A `DeviceEvent` that carried `before`/`after` DeviceState, like
ComponentEvent does, would make software_version, hostname, and serial changes reconstructable from
`entity_events` alone.

### 4.5 Drift

`DriftDetected` is a `DeviceOperationEvent` arm (`operation_event.proto:146-153`) with `expected`
and `observed` as attributes (`centralaudit.go:100-122`). It lives in the audit table (4.6) with
`field_name` promoted to a hot column. The observation that proved drift is an
`InterfaceObservation` (`model/access/v1/interface.proto:50-100`) with `Provenance`. Lane E owns the
interface observation table. For this lane: the drift read of GOALS.md:59-66 (trap triggers the
read, poll stays the backstop) produces the same audit event either way, so no second table is
needed. `correlation_ids` (`operation_event.proto:36-44`) carries the trap's record id when the read
was trap-triggered, which is the join to the trap row.

### 4.6 Audit: `device_operation_events`

```sql
CREATE TABLE flowseer.device_operation_events
(
    tenant_id        LowCardinality(String),
    device_id        UUID,
    occurred_at      DateTime64(3)     CODEC(Delta, ZSTD(1)),   -- occurred_at carries sub-second order within one lane
    seq              UInt64            CODEC(Delta, ZSTD(1)),   -- FLOWSEER_DEVICE_AUDIT stream sequence
    event_id         UUID,                                      -- DeviceOperationEvent.event_id (operation_event.proto:25-28)
    lane_sequence    UInt64            CODEC(Delta, ZSTD(1)),   -- DeviceOperationEvent.sequence, 0 for lane-level events
    detail           Enum8('phase_transitioned' = 10, 'lane_blocked' = 11, 'lane_released' = 12,
                           'route_selected' = 13, 'discovery_completed' = 14,
                           'firmware_epoch_changed' = 15, 'recovery_started' = 16,
                           'drift_detected' = 17, 'lane_frozen' = 18),   -- the oneof field numbers
    phase_from       UInt8             CODEC(T64, ZSTD(1)),     -- OperationPhase numbers
    phase_to         UInt8             CODEC(T64, ZSTD(1)),
    block_reason     UInt8             CODEC(T64, ZSTD(1)),
    protocol         UInt8             CODEC(T64, ZSTD(1)),     -- RouteSelected.protocol
    fell_through     UInt8,
    firmware_fingerprint     LowCardinality(String),           -- DiscoveryCompleted / FirmwareEpochChanged.new_fingerprint
    previous_fingerprint     LowCardinality(String),
    drift_field_name LowCardinality(String),
    correlation_ids  Map(LowCardinality(String), String) CODEC(ZSTD(1)),   -- up to 8 pairs
    attributes       Map(LowCardinality(String), String) CODEC(ZSTD(1)),   -- up to 16 pairs, Value rendered as JSON text
    payload          String            CODEC(ZSTD(1)),                      -- serialized DeviceOperationEvent
    retention_class  Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    site_id          LowCardinality(String),
    INDEX idx_ts occurred_at TYPE minmax GRANULARITY 1,
    INDEX idx_corr correlation_ids TYPE bloom_filter(0.01) GRANULARITY 4
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY toYYYYMM(occurred_at)
ORDER BY (tenant_id, device_id, occurred_at, seq, event_id)
TTL toDateTime(occurred_at) + INTERVAL 1 YEAR DELETE WHERE retention_class = 'short',
    toDateTime(occurred_at) + INTERVAL 3 YEAR DELETE WHERE retention_class = 'standard',
    toDateTime(occurred_at) + INTERVAL 7 YEAR DELETE WHERE retention_class = 'long'
SETTINGS ttl_only_drop_parts = 1, min_age_to_force_merge_seconds = 2592000,
         min_age_to_force_merge_on_partition_only = 1;
```

`attributes` is `map<string, google.protobuf.Value>` in the schema. A Map of String with the Value
rendered as JSON text keeps the column typed and searchable with the `keyValuePairs` text tokenizer
if ever needed (02 F50) and avoids Nullable. The `payload` column keeps the exact Value.

### 4.7 Audit: `operator_action_events`

```sql
CREATE TABLE flowseer.operator_action_events
(
    tenant_id        LowCardinality(String),                   -- "default" for CreateTenant records that name no tenant
    occurred_at      DateTime64(3)     CODEC(Delta, ZSTD(1)),
    seq              UInt64            CODEC(Delta, ZSTD(1)),  -- FLOWSEER_OPERATOR_ACTIONS stream sequence
    event_id         UUID,
    call_id          UUID,
    operator_id      String            CODEC(ZSTD(1)),         -- OperatorRef rendered as the identity service keys it
    action           Enum8('edge_create' = 1, 'setup_key_issue' = 2, /* ... */ 'full_payload_revoke' = 20),  -- OperatorAction numbers, generated
    detail           Enum8('attempted' = 20, 'completed' = 21),
    outcome          Enum8('unspecified' = 0, 'succeeded' = 1, 'denied' = 2, 'failed' = 3),
    error_type       LowCardinality(String),
    object_kind      Enum8('none' = 0, 'edge' = 10, 'capture_session' = 11, 'tenant' = 12, 'member' = 13,
                           'role' = 14, 'role_assignment' = 15, 'partner' = 16, 'full_payload_grant' = 17),
    object_id        String            CODEC(ZSTD(1)),         -- the object's primary id as a string
    payload          String            CODEC(ZSTD(1)),
    retention_class  Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    INDEX idx_ts       occurred_at TYPE minmax GRANULARITY 1,
    INDEX idx_operator operator_id TYPE bloom_filter(0.01) GRANULARITY 4,
    INDEX idx_object   object_id   TYPE bloom_filter(0.01) GRANULARITY 4,
    INDEX idx_call     call_id     TYPE bloom_filter(0.01) GRANULARITY 4
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY toYYYYMM(occurred_at)
ORDER BY (tenant_id, action, occurred_at, seq, event_id)
TTL toDateTime(occurred_at) + INTERVAL 90 DAY DELETE WHERE action IN ('edge_get', 'edge_list') AND retention_class = 'short',
    toDateTime(occurred_at) + INTERVAL 1 YEAR DELETE WHERE action IN ('edge_get', 'edge_list') AND retention_class != 'short',
    toDateTime(occurred_at) + INTERVAL 3 YEAR DELETE WHERE action NOT IN ('edge_get', 'edge_list') AND retention_class != 'long',
    toDateTime(occurred_at) + INTERVAL 7 YEAR DELETE WHERE action NOT IN ('edge_get', 'edge_list') AND retention_class = 'long'
SETTINGS ttl_only_drop_parts = 1, min_age_to_force_merge_seconds = 2592000,
         min_age_to_force_merge_on_partition_only = 1;
```

`action` second in the key mirrors the subject design (`flowseer.<tenant>.operator.action.<action>`,
`subjects.go:127-129`) and makes the dominant audit query ("who retired edges this quarter") a key
range. Views (`EDGE_GET`, `EDGE_LIST`, the read stream today) share the table with shorter TTL rules,
which replaces the two-stream split of the authorization record with one table and two retention
rules. Whether to keep views at all in ClickHouse is a plan decision: they are 10x the change volume
and the record created a separate stream precisely so "a change competes only with changes". In
ClickHouse there is no competition (no byte bound evicts rows), so the reason for the split
disappears, and the remaining cost is bytes.

## 5. Dedup under redelivery

Every table ends its ORDER BY in `record_id` or `event_id`. A redelivered record after the ten minute
JetStream window (`hub.go:81-85`, ingestion record 2026-10-04 amendment) and after the ClickHouse
block-dedup window (01 section 3) becomes a byte-identical row with the same key and collapses at
merge (02 F45, F48). Until the merge, the readers below are duplicate-safe:

- Entity history lists: `SELECT ... FINAL` is fine here because the data per entity is tiny and the
  query filters on the primary key ("FINAL ... generally fine when queries filter on primary key
  columns", 02 F18), or `LIMIT BY record_id`.
- State at T: `argMax` is idempotent under identical duplicates (02 F43 reasoning).
- Counts (events per day): `uniqExact(record_id)` or read the rollup fed with `uniqState` (section 7).
- Presence tables: `min`/`max` are idempotent (03 set-valued domains).

The sink's own dedup on `(tenant, record_id)` (ingestion record: "Each sink that reads a central
stream therefore deduplicates on tenant and `record_id` itself") is the first line. For audit the
sink sets `insert_deduplication_token` to `"<stream>:<first seq>-<last seq>"` of the batch so a
retried batch inside the window is a no-op (02 F48 item 1).

For `entity_events` produced by a KV watcher (L2), the natural idempotency key is
`(tenant, entity_kind, entity_id, seq)`: the KV revision is unique per bucket. The sink derives
`record_id` deterministically from it (UUIDv5 over `bucket/key/revision`) so a restarted watcher that
replays history writes identical rows, not new ones. Inference from the NATS revision semantics
quoted in L2.

## 6. Representative queries and cost models

Notation: D devices in scope, E entities per device of the kind, R events per entity in the window
(normal day about 0.1 per binding, up to 2 in a storm), W window. Granule = 8,192 rows. All costs
are independent of total tenants and total table size because the tenant prefix bounds every scan;
the one exception is named at Q3.

**Q1. History of one entity, newest first.**
```sql
SELECT ts, seq, event_kind, status_from, status_to, actor, after
FROM flowseer.entity_events
WHERE tenant_id = {t} AND entity_kind = 'binding' AND entity_id = {id}
ORDER BY ts DESC, seq DESC LIMIT 100;
```
Cost: one key range, rows read = min(R_total, 100 + one granule), newest-first served by read-in-order
(02 F51). Flat in everything.

**Q2. State of one entity at T (before/after kinds).**
```sql
SELECT argMax(after, (ts, seq)) AS state_at_t
FROM flowseer.entity_events
WHERE tenant_id = {t} AND entity_kind = 'component' AND entity_id = {id} AND ts <= {T};
```
Cost: rows read = events of the entity up to T, bounded by the entity's lifetime event count (tens
to hundreds). Because every before/after event is a full snapshot (L1), no replay from a baseline is
needed, which is the advantage over the change-log-plus-snapshot shape of 02 F43. For from/to kinds
the same query returns `status_to`, the lifecycle at T.

**Q3. Activity feed: everything that changed in tenant T in the last 24 h.**
```sql
SELECT entity_kind, entity_id, ts, event_kind, status_from, status_to
FROM flowseer.entity_events
WHERE tenant_id = {t} AND ts >= now() - INTERVAL 1 DAY
ORDER BY ts DESC LIMIT 500;
```
Cost: the key gives the tenant prefix only. Within it the `idx_ts` minmax prunes granules whose
time range ends before the window. Rows read = granules that overlap the window, per (kind, entity)
run. Worst case: a tenant with N entities each having one event inside the window spreads the window
across N runs, so granules read is up to min(N, tenant rows / 8,192). For a 10,000-device tenant
with 50 components, 2 bindings, 8 links per device that is 600,000 entity runs, and in the worst
case 600,000 granule reads for a 500-row answer. This is the one query whose cost grows with the
tenant's entity count (not with total data, but still unbounded by W). Bound it with a projection
ordered `(tenant_id, ts, entity_kind, entity_id)`:
```sql
ALTER TABLE flowseer.entity_events ADD PROJECTION by_time
  (SELECT entity_kind, entity_id, ts, seq, event_kind, status_from, status_to, device_id, actor
   ORDER BY (tenant_id, ts, entity_kind, entity_id));
```
Projections "result in data duplication" (02 F3) but the duplicated columns exclude `before`/`after`,
so the copy is a few percent of the table. With the projection, Q3 reads rows = tenant events in W
(about 130,000 x tenant share per day), independent of entity count. Note: "By default, DELETE doesn't
work on tables with projections" (02 F57), so the tenant-forget path must set
`lightweight_mutation_projection_mode` or use `ALTER TABLE ... DELETE`. The benchmark measures Q3
with and without the projection (section 10).

**Q4. Devices in scope that were unreachable at T, and for how long.**
```sql
SELECT device_id, binding_id,
       argMax(status_to, (ts, seq)) AS status_at_t,
       argMax(unreachable_since, (ts, seq)) AS since,
       argMax(failure_kind, (ts, seq)) AS kind
FROM flowseer.binding_reachability
WHERE tenant_id = {t} AND device_id IN ({scope_devices}) AND ts <= {T}
GROUP BY device_id, binding_id
HAVING status_at_t = 'unreachable';
```
Cost: D key ranges (device second in key), rows read = sum over D of events up to T. With a
long-lived device that is years of transitions, bounded by R_lifetime (hundreds). To keep it flat
over years add `AND ts >= {T} - INTERVAL 90 DAY` plus a fallback: a device with no event in 90 days
has the state of the `binding_state_daily` rollup (section 7). Then rows read = D x R_90d.

**Q5. Topology of device D at T.**
```sql
SELECT link_id, argMax(after, (ts, seq)) AS link_at_t, argMax(status_to, (ts, seq)) AS status
FROM flowseer.link_events
WHERE tenant_id = {t} AND (a_device_id = {d} OR b_device_id = {d}) AND ts <= {T}
GROUP BY link_id
HAVING status != 'gone';
```
Cost: the key is by link, so this is a bloom-filter scan over the tenant prefix: granules read =
granules of the tenant whose bloom matches D, about (links of D x R_lifetime / 8,192) plus false
positives at 1 %. For 8 links x 100 events that is a handful of granules. Grows with the tenant's
link-event count only through false positives (1 % of tenant granules). If that is too much for the
10,000-device tenant (10,000 x 8 x 100 / 8,192 = 977 granules x 1 % = 10 extra), it is still bounded.
With canonical end ordering (4.3) the same query is needed, since D may be on either side.

**Q6. Who changed what on object X (operator audit).**
```sql
SELECT occurred_at, operator_id, action, detail, outcome, error_type
FROM flowseer.operator_action_events
WHERE tenant_id = {t} AND object_id = {x}
ORDER BY occurred_at DESC LIMIT 200;
```
Cost: bloom on `object_id` over the tenant prefix. Granules read = granules holding X's events plus
1 % of the tenant's granules. For a tenant with 100,000 rows/day x 365 days = 36.5 M rows = 4,500
granules, that is 45 false-positive granules per year of retention. Grows with the tenant's audit
volume x retention, bounded by the TTL. Acceptable because audit lookups are rare. If not, a
projection ordered `(tenant_id, object_id, occurred_at)` bounds it to the object's own rows.

**Q7. Audit trail of one device over a window.**
```sql
SELECT occurred_at, seq, lane_sequence, detail, phase_from, phase_to, drift_field_name, attributes
FROM flowseer.device_operation_events
WHERE tenant_id = {t} AND device_id = {d} AND occurred_at BETWEEN {a} AND {b}
ORDER BY occurred_at, seq;
```
Cost: one key range, rows read = the device's events in the window. Flat.

**Q8. Which devices ran version V on day D (firmware).**
```sql
SELECT device_id FROM flowseer.device_software_images
WHERE tenant_id = {t} AND device_id IN ({scope}) AND day = {D} AND version = {V} AND running = 1;
```
Cost: D key ranges, rows read = D x images per device x versions seen that day (about 2). Flat.

**Q9. Site S's devices in the last 24 h for a high-volume table (the scope query, section 9).**
```sql
SELECT ... FROM flowseer.interface_samples
WHERE tenant_id = {t} AND device_id IN (SELECT device_id FROM scope_devices) AND ts >= now() - INTERVAL 1 DAY;
```
Cost: D key ranges x (E interfaces x 288 samples) rows. Flat in total data. Section 9 compares this
against a site-stamped filter.

## 7. Rollups

All rollups use idempotent or `uniq` aggregates so a late duplicate past merge does not shift them
(03 "Non-idempotent aggregates fed by at-least-once input").

```sql
CREATE TABLE flowseer.entity_events_daily
(
    tenant_id    LowCardinality(String),
    entity_kind  Enum8(/* as entity_events */),
    day          Date,
    event_kind   Enum8('created' = 1, 'transitioned' = 2, 'removed' = 3),
    events       AggregateFunction(uniqExact, UUID),         -- exact under duplicates
    entities     AggregateFunction(uniq, String)
)
ENGINE = ReplicatedAggregatingMergeTree
PARTITION BY toYYYYMM(day)
ORDER BY (tenant_id, entity_kind, day, event_kind)
TTL day + INTERVAL 7 YEAR;

CREATE MATERIALIZED VIEW flowseer.entity_events_daily_mv TO flowseer.entity_events_daily AS
SELECT tenant_id, entity_kind, toDate(ts) AS day, event_kind,
       uniqExactState(record_id) AS events, uniqState(entity_id) AS entities
FROM flowseer.entity_events GROUP BY tenant_id, entity_kind, day, event_kind;
```

`binding_state_daily`: one row per (tenant, device, binding, day) with
`argMaxState(status_to, (ts, seq))` as the end-of-day status and `unreachable_seconds` computed by
the sink per event (the sink knows the previous transition's time from the before side's
`unreachable_since`, `binding.proto:138`), summed with `sum` guarded by `uniqExact(record_id)` in the
same row so a duplicate can be detected (`sum / uniqExact` ratio). The simpler and fully idempotent
alternative is a daily refreshable view (02 F20, F21) that recomputes the last 2 days from
`binding_reachability` with window functions, since the table is small. Recommended: the refreshable
view, scheduled hourly, `APPEND` not needed because it overwrites a 2-day window. This is the one
place in this lane where a refreshable view beats an incremental one, and it stays off the insert
path (03 "Small inserts and chained MVs").

Audit tables get `operator_actions_daily` (tenant, action, outcome, day, `uniqExact(event_id)`) for
the "actions per day" chart, which also doubles as the completeness tally (section 8.4).

## 8. Audit store: ClickHouse versus JetStream

The question is whether ClickHouse replaces JetStream as the *store* of record for the two audit
streams, with JetStream remaining the transport. Recommendation, not decision: ClickHouse as the store,
JetStream as the transport with its current byte bounds as a buffer, for the reasons below. The
decision is the plan's because it changes the authorization record's open question
(`operator-authorization-direction.md:1042-1048`).

### 8.1 Immutability

JetStream: a stream is immutable to clients except through server-side deletion and limits. Rows are
evicted by `DiscardOld` when `MaxBytes` or `MaxMsgsPerSubject` is reached (L4). So the store is
immutable but not durable past the bound.

ClickHouse: no row is immutable by engine, but mutation is a grant. The GRANT page lists `ALTER
UPDATE` ("Level: `COLUMN`. Aliases: `UPDATE`"), `ALTER DELETE` ("Aliases: `DELETE`"), `DROP TABLE`
("Level: `TABLE`"), and `TRUNCATE` as separate privileges from `INSERT` and `SELECT`
(`https://clickhouse.com/docs/sql-reference/statements/grant.md`). Give the sink user `INSERT` only
and the API user `SELECT` only on the audit tables, keep `ALTER`, `DROP`, and `TRUNCATE` with the
migration job, and the only path that removes rows is TTL. ReplacingMergeTree merges collapse
byte-identical duplicates only, which does not alter content. The repository's hooks and the
`verify-change` gate can enforce the grant list as a conformance test, as the hub's stream limits are
tested today (`TestAViewFloodCannotEvictAChangeRecord`, cited in the authorization record).

Residual risk: a ClickHouse superuser can delete. The same is true of a NATS system account. Neither
store is a WORM device. If a regulatory regime needs tamper evidence, the cheap addition is a per-row
hash chain computed by the sink (`prev_hash`, `row_hash` columns) which a reader can verify per
tenant per day. Unverified whether any FlowSeer obligation requires that.

### 8.2 Retention and tenant forget versus audit obligations

JetStream: one `MaxBytes` and one `MaxAge` per stream, no per-tenant or per-action retention. Tenant
forget is a `Purge` by subject filter, which is per-tenant because the tenant is in the subject.

ClickHouse: `TTL ... DELETE WHERE` per class and per action (4.6, 4.7, 02 F36). Tenant forget is
the lightweight `DELETE WHERE tenant_id = ?` followed by `ALTER TABLE ... DELETE` (02 F57). The
conflict: GDPR Article 17(3) exempts erasure needed "for compliance with a legal obligation which
requires processing by Union or Member State law" (17(3)(b)) and "for the establishment, exercise or
defence of legal claims" (17(3)(e)) (`https://gdpr-info.eu/art-17-gdpr/`). Which obligation applies to
FlowSeer's audit trail is unverified and the plan's to decide with the operator. The schema should
support both outcomes: (a) forget deletes audit rows with everything else, or (b) forget deletes
inventory and samples and *pseudonymises* audit by rewriting `operator_id` to a tombstone through a
mutation while the action rows stay. Option (b) is why `operator_id` is its own column rather than
only inside `payload`: `payload` must then be cleared too (`ALTER TABLE ... UPDATE payload = ''
WHERE tenant_id = ?`), which is a column rewrite over the tenant's parts (02 F17, acceptable once per
forget). A grant for `ALTER UPDATE` on `operator_id, payload` only, held by the forget job, keeps 8.1.

### 8.3 Ordering

JetStream: stream sequence is the order of acceptance, per stream. Reading in order needs a consumer.

ClickHouse: `seq` column carries the stream sequence, so the order is preserved and queryable
without consuming. `occurred_at` is the producer's clock. The key `(..., occurred_at, seq, event_id)`
sorts by producer time first for display and by `seq` for ties (section 3.3). An auditor who needs
arrival order sorts by `seq`.

### 8.4 Completeness

JetStream: a gap means eviction or a failed publish. The hub's `Publish` returns "only after the
stream acknowledges the write" (`auditapi/publisher.go:10-13`), so a published event is in the
stream until evicted.

ClickHouse: the sink acknowledges a JetStream message only after the insert is acknowledged
(ingestion record: "acknowledges an asynchronous insert only after the flush when
`wait_for_async_insert=1`", 02 F15). A gap is then detectable: `seq` is contiguous per stream, and a
query over one stream's rows (`WHERE seq BETWEEN a AND b`) with `lagInFrame(seq) OVER (ORDER BY
seq)` (02 F28 window semantics) lists every missing sequence, independent of tenant because the
sequence is stream-wide. A daily conformance job runs that query over yesterday's range and reports
holes. JetStream's sequence contiguity (no reuse, no skip except on deletion) is unverified from the
fetched docs page (section 3.3) and must be checked against the server source before the job is
trusted. Within JetStream the equivalent check is impossible once the oldest messages are gone.

### 8.5 What stays in JetStream

The hub's audit stream stays as the transport and the 24 h-class buffer. Its byte bound becomes a
sink-outage budget rather than the retention. The authorization record's per-tenant stream question
loses its force: eviction from JetStream no longer loses the record once the sink has written it,
and the only remaining cross-tenant risk is a sink outage longer than the stream's fill time, which
`MaxBytes` sizing and an alert on `FLOWSEER_OPERATOR_ACTIONS` first-sequence movement cover. Whether
the operator read stream is worth storing in ClickHouse at all is a plan decision (4.7).

### 8.6 What ClickHouse does not give

- A read of the audit trail of an edge *while central is down* needs the stream (the ClickHouse
  reader is central).
- Exactly-once: a redelivered audit event past both windows is stored twice until merge (section 5).
  `uniqExact(event_id)` counts are exact anyway.

## 9. As-of dimension: device at site at T

Entities: `Placement` (`placement.proto:43-72`, append-only, `effective_from` required,
`effective_until` unset means current) places a device under an `IntegrationScope`; `DeviceConfig.location`
(`device.proto:103`) places it under an operator `Location`, and a Site is `LOCATION_KIND_SITE`
(`location.proto:36`); GOALS.md:21 says "A Site is a FlowSeer-owned inventory entity under a Tenant"
with "No record decides its shape yet", and GOALS.md:51-53 say a Placement into a Site "can derive
from the platform's scopes, and an operator's Placement wins". Inference: the as-of dimension is
"device D belonged to site S during [from, until)", one table, fed from PlacementEvents (and from
Location assignment changes once a record decides the Site shape).

### 9.1 The three options compared

| | (a) `site_id` stamped on fact rows at write time | (b) `complex_key_range_hashed` dictionary over a `device_site_history` table | (c) `device_site_history` table + ASOF JOIN |
| --- | --- | --- | --- |
| "Scope = site S, last 24 h" on a fact table | Resolve S to a device list in Postgres (bounded, a few thousand), then `device_id IN` key ranges (Q9). The stamp is *not* needed for this query because `site_id` is not in the key. The stamp lets a UI filter `WHERE site_id = S` without the list, at the cost of a tenant-prefix scan with no skip index unless a `set` or `bloom` index on `site_id` is added | `dictGet(..., (tenant_id, device_id), ts) = S` on every row: full tenant scan, no index | Join per row, hash algorithm, memory = history rows, "denormalization is strongly recommended" against it for latency-sensitive queries (02 F23) |
| Correctness when D moves from S1 to S2 at T0 | Rows before T0 say S1, after say S2: "where was D when this was measured", correct for history, and a query "site S2 last 7 days" misses D's rows before T0, which is *right* for a site-scoped report and wrong if the operator meant "everything about the devices now in S2" | Correct at every T by construction, as long as the history table is complete and ranges do not overlap. `range_lookup_strategy` (`min` default) resolves overlaps deterministically (`https://clickhouse.com/docs/sql-reference/dictionaries/index.md` range-hashed page) | Same as (b), with `ASOF LEFT JOIN ... ON d.device_id = f.device_id AND h.effective_from <= f.ts` ("Conditions supported for the closest match: `>`, `>=`, `<`, `<=`", "`ASOF JOIN`, `LEFT ASOF JOIN`", `https://clickhouse.com/docs/sql-reference/statements/select/join.md`) |
| Cost of a move | Zero on history. The sink reads the current site from a small in-memory map refreshed from the KV placement bucket | Dictionary reload: `LIFETIME(MIN 300 MAX 360)` or `invalidate_query` (02 F68); a move is visible after the next reload | Table insert only |
| Memory | None | Hash table of all (tenant, device) ranges: 20,000 devices x a few moves = under 100,000 ranges, a few MB (02 F68 page: "stored in memory in the form of a hash table") | Join build side per query |
| Source | Sink's map, fed by the KV watcher | `SOURCE(CLICKHOUSE(TABLE 'device_site_history'))` in the same server (the fetched range-hashed page uses this form). A direct PostgreSQL source exists per the dictionaries overview ("ClickHouse table, HTTP, PostgreSQL") but its page could not be located in this pass (unverified syntax); the ClickHouse-table source avoids a second connection and a Postgres dependency in the query path | Plain table |

Recommendation: (a) plus (b), with (c) for ad hoc analysis, as 02 F69 already concluded. New in this
lane: the `device_site_history` table is the Placement history itself, written by the same sink
that writes `entity_events`, so there is no Postgres mirror to keep in step.

```sql
CREATE TABLE flowseer.device_site_history
(
    tenant_id       LowCardinality(String),
    device_id       UUID,
    site_id         LowCardinality(String),      -- LocationLocalRef.id of the site, or the IntegrationScope key until Site lands
    placement_id    UUID,
    source          Enum8('operator' = 1, 'derived_from_scope' = 2),   -- PlacementSource numbers
    effective_from  DateTime,
    effective_until DateTime,                    -- 0 = open; the dictionary maps 0 to the open range below
    seq             UInt64,
    ver             UInt64                       -- seq of the latest PlacementEvent for this placement (ReplacingMergeTree version)
)
ENGINE = ReplicatedReplacingMergeTree(ver)
ORDER BY (tenant_id, device_id, placement_id);

CREATE DICTIONARY flowseer.device_site_dict
(
    tenant_id String, device_id UUID,
    site_id String,
    effective_from DateTime, effective_until Nullable(DateTime)
)
PRIMARY KEY tenant_id, device_id
SOURCE(CLICKHOUSE(QUERY '
    SELECT tenant_id, device_id, site_id, effective_from,
           if(effective_until = 0, NULL, effective_until) AS effective_until
    FROM flowseer.device_site_history FINAL'))
LAYOUT(COMPLEX_KEY_RANGE_HASHED(range_lookup_strategy ''max''))
RANGE(MIN effective_from MAX effective_until)
LIFETIME(MIN 300 MAX 360);
```

`PlacementEvent.after` is required and only `effective_until` may change
(`placement.proto:90-108`), so a placement row is overwritten once at most (open to closed), and
`ReplacingMergeTree(ver)` with `FINAL` in the dictionary query gives the closed row. "If the
`range_max` is `NULL`, the range is open" (range-hashed page), which is why the open placement maps
to NULL. `range_lookup_strategy 'max'` ("a matching range with maximal `range_min`") makes an
operator placement that starts later win over an earlier derived one when both are open, which
matches "an operator's Placement wins" (GOALS.md:53) when the operator acted later; if the operator
acted earlier, the sink must close the derived placement (the inventory service's rule, not the
dictionary's). A one-to-many dictionary "will result in silent data loss" (02 F23), which is why
ranges per (tenant, device) must not overlap for the same source, and the benchmark includes an
overlap check query.

Usage: `dictGet('flowseer.device_site_dict', 'site_id', (tenant_id, device_id), ts)` on any fact row
gives the site as it was at that row's time. Writing the stamp at insert time is the same call made
in Go against the sink's map, which gives the same answer as long as the map is fresh. The benchmark
includes a consistency query: stamped `site_id` versus `dictGet` over one day, expected zero
mismatches outside the reload window.

### 9.2 Scope = tag or location subtree

A tag or a subtree resolves to a device list in Postgres (the read model, ingestion record "Lists,
filters, sorts, and joins for the API"), bounded by the scope's device count, then `device_id IN
(...)` on the fact table. Cost is D key ranges regardless of how the scope was defined, independent
of total data, and the same for site, tag, and subtree. A stamped `site_id` cannot serve tag or
subtree scopes, so the device-list path is the primary path for every scope kind and the stamp is a
UI convenience plus the historical record. The ClickHouse `IN` list size: a few thousand UUIDs per
query is routine (unverified limit; `max_query_size` default 262,144 bytes bounds the SQL text, and a
UUID literal is 38 bytes, so about 6,000 literals fit; use an external table or a `(SELECT ...)`
against a temporary `scope_devices` table for larger scopes).

Predictability: the scan cost is D x E x (W / poll) rows in every case. Total tenants and total
table size do not enter. The device-list resolution in Postgres is bounded by the tenant's device
count and is not a ClickHouse cost.

## 10. Benchmark design

Target: a single-node ClickHouse 26.8 container. Generator in Go (pure, reproducible by seed), inserts
through clickhouse-go v2 Native batches, `async_insert = 0` (02 F15), `insert_deduplication_token`
per batch.

### 10.1 Generator parameters

| Parameter | Default | Variation |
| --- | --- | --- |
| Tenants | 200, device counts log-normal with median 40 and two tenants at 10,000 (planning scale) | 50, 200, 800 |
| Devices total | 20,000 | 20,000, 80,000, 320,000 (1x, 4x, 16x) |
| Entities per device | 50 components, 2 bindings, 8 links, 2 software images, 5 licences, 1 placement | fixed |
| Days of data | 90 | 90, 360 |
| Binding flap process | per binding, Poisson rate 0.02 transitions/day, with storm days (1 % of days) where 30 % of a tenant's bindings transition twice within one hour | rate x 10 |
| Component change process | 0.1 %/day per component, plus one firmware wave per 30 days rewriting every component's `software_revision` and the device's software image set | |
| Link process | 1 %/day status transitions, one core-reboot storm per 60 days per 10,000-device tenant staling 20 % of links | |
| Placement moves | 0.05 %/day per device | 1 %/day |
| Device operation events | 1,000 mutations/day per 20,000 devices, 5 phase events each, 1 discovery per device per day, drift 0.1 % of devices/day | x 10 |
| Operator actions | 100,000/day per 20,000 devices, 90 % views | views off |
| Redelivery | 0.5 % of batches re-sent after 15 min with the same token, 0.1 % of records re-sent 2 h later in a different batch | |
| Clock skew | 1 % of events get `ts` 30 s earlier than a lower `seq` | |
| Seed | 20261008 | |

### 10.2 Scale steps and expected results

Step A, growing total at fixed scope: 1x, 4x, 16x devices, the query scope fixed to one 40-device
tenant and one 2,000-device site. Expected: Q1, Q2, Q4, Q5, Q7, Q8, Q9 read_rows flat within noise
(within 2x of the cost model), Q3 without the projection grows with the tenant's entity count
(unchanged here, so flat), Q6 grows only through bloom false positives (expected under 2 % of the
tenant's granules).

Step B, fixed total at growing scope: 1x data, scope 40, 400, 4,000 devices. Expected: Q4, Q8, Q9
read_rows linear in D. Q3 flat (tenant-level). Q1, Q2, Q5, Q7 flat (single entity).

Step C, storm versus normal day: Q3 and Q4 over a storm day versus a normal day. Expected: read_rows
scale with the day's event count, not with table size.

Step D, retention: 90 versus 360 days of data at 1x. Expected: every query with a time window flat.
Q2 and Q4 (`ts <= T` without lower bound) grow with R_lifetime, and the 90-day lower bound variant of
Q4 is flat. This step is what justifies the lower bound plus rollup fallback.

### 10.3 Metrics

- Insert: rows/s per table, parts per insert (`system.part_log` NewPart count per insert query id,
  02 F67), expected 1 part per insert per table since one month partition is touched.
- Storage: `system.columns` compressed and uncompressed bytes per column, bytes/row per table. Expected
  (inference): `entity_events` 150 to 300 B/row with payloads, 40 B/row without; audit tables 100 to
  200 B/row; presence tables under 30 B/row; `device_site_history` negligible. Try `ZSTD(3)` on
  `before`/`after`/`payload` and keep it if it saves more than 20 % at under 2x insert CPU.
- Per query: `read_rows`, `read_bytes`, `query_duration_ms` p50/p95 over 50 runs, `memory_usage`
  from `system.query_log` with `is_initial_query = 1`, and granule counts from
  `EXPLAIN indexes = 1` (02 F19) for every query, recorded per scale step. For Q5 and Q6 record the
  skip-index "Dropped granules" line to prove the bloom filter works.
- Dedup: after the run, `SELECT count() - uniqExact(record_id)` per table before and after
  `OPTIMIZE TABLE ... FINAL` on a closed partition (allowed in a benchmark, not in production, 02
  F18). Expected: the duplicate count equals the generator's late-redelivery count before, zero after.
- Reconstruction correctness: for 1,000 random (entity, T) pairs compare Q2's `after` bytes with
  the generator's ground truth state at T. Expected 100 % equality, including the clock-skew cases
  (which fail if `argMax(after, ts)` is used instead of the tuple).
- Dictionary: `dictGet` versus stamped `site_id` over one day, expected zero mismatches; dictionary
  memory from `system.dictionaries.bytes_allocated`; reload time at 1x and 16x.
- Completeness: the gap query over one day of `device_operation_events.seq`, expected to list
  exactly the sequences the generator dropped (inject 10 drops).

### 10.4 Pass criteria

| Query | Criterion |
| --- | --- |
| Q1, Q7 | read_rows ≤ 2 x (entity events in window + 8,192), flat across steps A and D |
| Q2 | read_rows ≤ 2 x R_lifetime, correct state in 100 % of samples |
| Q3 with projection | read_rows ≤ 2 x tenant events in window, flat across A; without projection, document the growth and keep the projection |
| Q4 (90-day bound) | read_rows ≤ 2 x D x R_90d, linear in D (step B), flat in A and D |
| Q5 | granules read ≤ links of D x R_lifetime / 8,192 + 2 % of tenant granules |
| Q6 | granules read ≤ object granules + 2 % of tenant granules per year retained |
| Q8, Q9 | read_rows ≤ 2 x D x E x (W / interval), flat across A |
| Inserts | 1 part per insert per table, no `parts_to_delay_insert` hits (02 F63) |
| Storage | bytes/row within the expected ranges above; any column over 50 B/row compressed gets a codec review |

## 11. Failure modes and how the design avoids them

| Failure | Avoided by |
| --- | --- |
| Heartbeat fields turn events into samples (L3) | Sink excludes `last_success`, `last_seen`, `last_seen_at` from the change test; `binding_reachability` records `last_success` only on status change |
| Type names persisted, broken by a rename (L5) | `entity_kind` is FlowSeer's own enum, bytes carry no name, `schema_rev` is a counter |
| A field needed later was never a column (L6) | Bytes kept; backfill is a Go job that decodes and `INSERT ... SELECT`s into a new column (02 F64: ADD COLUMN is free) |
| Clock skew misorders "latest state" | `argMax(after, (ts, seq))`, `seq` in the key |
| Activity feed scans every entity run (Q3) | Projection ordered by `(tenant_id, ts, ...)` |
| Audit eviction under a flood (L4) | ClickHouse holds the record; JetStream bound becomes an outage budget |
| Audit row altered or deleted | Grants: sink INSERT only, reader SELECT only; forget job holds `ALTER UPDATE` on two columns |
| Tenant forget blocked by projections | `lightweight_mutation_projection_mode` set for the forget job, or `ALTER TABLE ... DELETE` (02 F57) |
| Dictionary silent loss on overlapping ranges | Inventory service closes the losing placement; benchmark overlap check; `range_lookup_strategy 'max'` for the remaining ties |
| Rollup double counts after a late duplicate | `uniqExact`/`uniq` states and idempotent `min`/`max`; the one `sum` (downtime) comes from a refreshable view that recomputes |
| Firmware history invisible because `DeviceEvent` is lifecycle-only | Presence table from inventory reads plus `FirmwareEpochChanged` in audit; schema note for the plan |
| Too many parts from many small tables | Monthly partitions, one insert per table per batch interval, no chained MVs |

## 12. Schema gaps

The tables above are designed for what the history needs, not only for the fields that exist today.
Each row names a missing field, message, or carrier, the proposed addition, and the evidence that the
history needs it. "Carrier" means the message that moves the data to the sink (an Event, an
IngestRecord arm, or a KV bucket the watcher reads). None of these are decisions; they are inputs to
the plan and to the schema owners.

| # | Gap | Proposed addition | Evidence |
| --- | --- | --- | --- |
| G1 | No carrier for inventory transitions. The Event messages exist (L1) but nothing publishes them, and `IngestRecord.payload` has one arm, `syslog` (`spec/proto/flowseer/integration/ingest/v1/ingest_record.proto:25-30`). | Either a KV-watch sink in central that diffs bucket revisions into Event rows (L2), or an `event` envelope with one arm per `<Entity>Event` published by the inventory service on a `FLOWSEER_INVENTORY_EVENTS` stream with `<tenant>.<record_id>` as message id, as the ingestion record does for records. The second gives the history a `seq` that is a stream sequence and lets other consumers subscribe. | `docs/conventions/protobuf.md` "The triad": an Event is "carried on the broker and the event envelope", and no envelope for Events exists (`spec/proto/flowseer/event/README.md` is referenced by the ingestion record's sources). `grep` in L2. |
| G2 | `DeviceEvent`, `IntegrationEvent`, `EdgeEvent`, `CaptureSessionEvent`, `TenantEvent` carry only `from`/`to` of the lifecycle enum. Changes of `DeviceState.serial`, `hostname`, `vendor`, `model`, `software_version`, `sys_object_id`, `system_location` (`device.proto:138-205`) produce no event, so "when did the hostname change", "when was the serial swapped (RMA)", and "which software version ran at T" are not reconstructable from events. | Make the five lifecycle-only events before/after events like `ComponentEvent` (`before`/`after` State, with the `one_side` and `*_matches_ref` CEL rules copied), or add a sibling `DeviceStateEvent` carrying before/after `DeviceState`. The `entity_events` table already has the `before`/`after` columns for it. | Section 4.4. The platform dossier adds `system_contact`, `system_location`, `uptime` to `DeviceState` (`docs/research/schema-building-blocks/04-platform-system.md` §4.3), which are more observed fields with no change carrier. Ruckus reports `lastRebootReason` and `totalBootCount` per AP (`spec/proto/ruckus/ap/ap_status.proto:1824-1831`), which are reboot facts a lifecycle enum cannot hold. |
| G3 | Reboot is not an event. `DeviceState.uptime` says "a reboot is not inferred from it" (`device.proto:206-209`), and nothing else records one. | A `DeviceRebooted` arm on a device event (or on `DeviceOperationEvent` when FlowSeer caused it) with `detected_at`, `reason` (free text, bounded), `boot_count` (uint32, unset when unknown), `source` enum (`SNMP_ENGINE_BOOTS`, `SYSUPTIME_DROP`, `PLATFORM_REPORTED`, `TRAP_COLDSTART`). Stored as `entity_events` rows of kind `device`, `event_kind = 'transitioned'`, hot column `status_from = status_to`, and the arm in `after`. | Atlas: "`snmpEngineBoots` incrementing is an unambiguous reboot signal" (`docs/research/network-domain-atlas/entities/09-ops.md:66`). Ruckus `lastRebootReason`, `totalBootCount` (`ap_status.proto:1824-1831`). Aruba Central's streaming **Audit** topic carries "device connectivity, config status, firmware status" (`docs/research/device-inventory/targets/hpe-aruba.md:185-186`). Mist webhooks have "Device Events, Device Updowns" topics (`juniper-mist-junos.md:129-130`). |
| G4 | Configuration change on the device is not an event. GOALS.md:59-66 wants a trap such as `snTrapRunningConfigChanged` ("generated when the running configuration was changed", `spec/mib/ruckus/icx/FOUNDRY-SN-NOTIFICATION-MIB:1189-1200`) to trigger the drift read, but the trap itself, and a platform's own change log, have no FlowSeer message. `DriftDetected` records only the *result* (`operation_event.proto:146-153`). | A `ConfigurationChanged` event (device ref, `observed_at`, `source` enum `TRAP`, `PLATFORM_CHANGELOG`, `CONFIG_MAN_MIB`, `actor` string when the device or platform names one, `change_ref` string such as the platform's change id, `description` bounded) as an `IngestRecord` arm so it rides the existing intake path with Provenance. Stored in `entity_events` as kind `device` with `actor` and the arm in `after`, and joined to the following `DriftDetected` through `correlation_ids`. | The atlas's config-file entity: Comware `hh3cCfgLogTable[index]` is "who changed what, when" (`network-domain-atlas/entities/01-platform.md` config-file table), and every vendor has a change signal. GOALS.md:63-64: "A trap says that a change happened and not what changed". |
| G5 | Firmware history has no carrier. `SoftwareImage` (`software_image.proto:9-36`) is a primitive with no device-scoped message that lists a device's images and no event when the running image changes. `FirmwareEpochChanged` (`operation_event.proto:127-140`) carries fingerprints, not versions or slots, and only from the lane's view. | A `DeviceSoftwareState` (device ref, `repeated SoftwareImage images`) read on every inventory pass and carried as an `IngestRecord` arm, which feeds `device_software_images` (4.4), plus a `SoftwareImageEvent` (before/after `SoftwareImage` per slot) derived by the sink when `running` or `version` of a slot changes. Add `activated_at` (Timestamp, unset when unknown) and `install_source` (bounded string) to `SoftwareImage` for platforms that report them. | Atlas firmware-image: "everybody has a *dual-image* concept ... On stacked devices there is one image set per member and version skew is a real fault condition" (`01-platform.md` firmware-image). Platform dossier §5 proposes `FirmwareImage` as "State + Event". Ruckus `ConfigFwStatus` enum has `FW_UPD_COMPLETE`, `FW_UPD_ONGOING`, `FW_UPD_FAIL` (`ap_status.proto:1728-1736`). |
| G6 | Stack members have no per-member firmware. `ComponentState.software_revision` (`component.proto:180-183`) is per component, so a `STACK_MEMBER` component carries it, but no event marks "member skew" and the `SoftwareImage` table has no member key. | Add `component_name` (ComponentLocalRef name, unset for the chassis) to `SoftwareImage` so the presence table keys on `(device, component_name, network_instance, slot)`. | Atlas firmware-image: Cisco SMB `rndImageInfoTable[stackUnitNumber]`, Huawei `HUAWEI-STACK-MIB for per-member versions`. |
| G7 | Licence history has no carrier and no event. `License` (`license.proto:11-41`) is a primitive only. | A `DeviceLicenseState` (device ref, `repeated License licenses`) as an `IngestRecord` arm feeding `device_licenses` (4.4). Expiry warnings are a query on `expires_at`, not an event. | Atlas `01-platform.md` license entity (line 436); platform dossier §5 lists licence as "fields/tables on existing DeviceState or device-scoped tables". |
| G8 | Link events carry no reason and no observation counters. `LinkState` (`link.proto:100-144`) has `status` and `first_seen`/`last_seen`, but an operator asks "why did the link go stale" (age-out versus deletion versus the far end rebooting). | Add `change_reason` enum to `LinkState` or to `LinkEvent` (`AGEOUT`, `DELETED_BY_PEER`, `LOCAL_PORT_DOWN`, `THRESHOLD`, `RECONCILED_TO_CABLE`) and `remote_last_change` (Timestamp from `lldpStatsRemTablesLastChangeTime` converted with the sample's sysUpTime). Stored as hot columns on `link_events`. | LLDP-MIB: `lldpStatsRemTablesLastChangeTime` is "The value of sysUpTime ... at the time an entry is created, modified, or deleted in the in tables associated with the lldpRemoteSystemsData objects", with `lldpStatsRemTablesInserts`, `Deletes`, `Ageouts` counters (`spec/mib/ieee/LLDP-MIB:719-800`). A sink that reads these can tell age-out from deletion. |
| G9 | Link ends are unordered ("Which end is a and which is b carries no meaning", `link.proto:114-115`). Every query from one device must test both ends (Q5). | Document a canonical order (lower device UUID first, foreign systems after devices) in `LinkState`, or let the inventory service guarantee it. Section 4.3. | Query cost in Q5. |
| G10 | Binding reachability has no duration and no last-good timestamp at the transition. `BindingState.last_success` is a heartbeat (L3) and `unreachable_since` exists only while unreachable (`binding.proto:136-138`). "How long was it down" needs the clear event to carry the start. | Add `unreachable_until` (set exactly on the transition out of unreachable) or, simpler, keep `unreachable_since` present on the *after* side of the clearing event (relax the `unreachable_since_matches_status` CEL rule to "set while unreachable or on the transition that clears it"). The sink computes `downtime_seconds` for the rollup. | Section 7 `binding_state_daily`; Q4. |
| G11 | No per-tenant retention class in any message. The tables stamp `retention_class` from "the tenant's plan" (02 F36), but `TenantConfig` (`tenant.proto:36-68`) has no retention field. | Add `history_retention` (enum `SHORT`, `STANDARD`, `LONG`) and `audit_retention` (same enum, separate because audit obligations differ, 8.2) to `TenantConfig`. The sink reads them from the `tenants` bucket. | 02 F36; section 8.2. |
| G12 | Audit events name no site and no scope. `DeviceOperationEvent` has a device ref only (`operation_event.proto:22`). The `site_id` stamp (section 9) is derived by the sink, which is fine, but a device whose placement is unknown at write time gets an empty stamp forever. | No schema change. Instead, the sink resolves site from the placement map and, when unknown, leaves it empty and lets the dictionary (9.1) answer. Recorded here so the plan does not add `site` to the event message, which would persist a derived value in the audit record. | `docs/conventions/protobuf.md` "Tenancy is ambient" argues the same for tenant. |
| G13 | `OperatorActionEvent.object` has no arm for devices, integrations, placements, locations, or tags (`operator_action_event.proto:163-172`), so operator actions on inventory (retire a device, move a placement, rename a site) cannot be audited by object once those RPCs exist. | Add arms `DeviceGlobalRef device`, `IntegrationGlobalRef integration`, `PlacementGlobalRef placement`, `LocationGlobalRef location`, `TagGlobalRef tag`, and extend `OperatorAction` with the inventory actions. `operator_action_events.object_kind` Enum8 grows by the same numbers. | `OperatorAction` enum stops at `FULL_PAYLOAD_REVOKE = 20` with no inventory action (`operator_action_event.proto:18-40`); GOALS.md:19 names Device, Integration, Binding, Placement as inventory with "one lifecycle each", which are operator-driven lifecycles (`device.proto:43-46`: retired "by a person"). |
| G14 | Audit events carry no actor identity for system-caused transitions. `DeviceOperationEvent` has `correlation_ids` but no `actor`, and `DeviceEvent` (lifecycle set "by a person or an explicit policy", `device.proto:43-44`) does not say which. | Add an `actor` oneof to the inventory Event messages (`OperatorRef operator`, `string policy`, `IntegrationGlobalRef integration`, `string system`) or carry it on the event envelope of G1. `entity_events.actor` is the column. | Section 4.1 `actor` column; GDPR pseudonymisation path in 8.2 needs the actor as a column, not only in bytes. |
| G15 | Alarms (named, stateful, clearable device conditions) have no entity, so "was this alarm active at T" cannot be answered by this lane or any other. | `AlarmState` + `AlarmEvent` keyed by (device, resource, type id) as the platform dossier §4.6 proposes. The history table is `entity_events` kind `alarm` with hot columns `severity`, `is_cleared`, and `resource`, which is why `entity_id` is a String (composite keys fit). | `09-ops.md` §3: "FlowSeer's Event concept should distinguish a *log line* ... from an *alarm*"; `ALARM-MIB` model/active split; Ruckus SCI `EventMessage` has `eventCode`, `eventType`, `mainCategory`, `subCategory`, `severity`, `apMac`, `clientMac` (`spec/proto/ruckus/sci/sci-event.proto:26-120`), which is an alarm-shaped feed with no FlowSeer target today. |
| G16 | Platform scope moves have no device-level event. `IntegrationScopeEvent` records the scope's own change, and `PlacementEvent` records FlowSeer's placement, but a controller moving an AP between zones (`ap_status.proto:1796-1806` `zone_id`, `zoneName`; `switch_all.proto:89-92` switch group levels) reaches the history only if the inventory service derives a new Placement from it. | Document in `placement.proto` that a derived placement closes and opens on every scope move the platform reports, with `source = DERIVED_FROM_SCOPE`, so `device_site_history` follows the platform. No new message. | GOALS.md:51-53 "a device's Placement into a FlowSeer Site can derive from the platform's scopes". |
| G17 | Site has no message (GOALS.md:21-22), so `device_site_history.site_id` has no typed source. Device to Location is `DeviceConfig.location` (`device.proto:103`) with no event when it changes (`DeviceConfig` has no Event of its own, the triad's Event covers the State side). | When Site lands, a `DeviceLocationEvent` or a before/after `DeviceConfigEvent`. Until then the sink derives site from Placement (scope) and from `DeviceConfig.location` via the `edges`/`device` KV buckets. | Section 9; `LocationEvent` and `TagEvent` already exist (`location.proto`, `tag.proto:89`) and are two more `entity_kind` values for `entity_events` ("location", "tag"), which the Enum8 should include from the start. |
| G18 | Operations (config backup, firmware upgrade, cable test) are jobs with a definition, a status, and a result log, and have no home in the triad. | An `Operation` entity (`OperationState` + `OperationEvent`, keyed by operation UUID under a device) whose events land in `entity_events` kind `operation` with hot columns `operation_kind`, `phase`, `result`. The audit table keeps the lane's view; the entity keeps the operator's. | Atlas 04 §3.3: "FlowSeer's Config/State/Event triad has no place for this ... decide whether an Operation concept belongs in the model before any of those four entities is attempted". |
| G19 | Device-reported event logs (the on-box log buffer and `NOTIFICATION-LOG-MIB` replay) have no carrier beyond syslog. | Not this lane's table, but the trap and log lanes need the same `record_id`, Provenance, and `correlation_ids` contract so `entity_events` and the audit table can join to them. Flagged so the ingest envelope grows arms consistently. | `09-ops.md` §2: `NOTIFICATION-LOG-MIB` "exists precisely so a manager can poll for the traps it missed". |

Fields the tables carry *because* of these gaps, so the DDL does not change when the schema catches
up: `entity_events.actor`, `entity_events.schema_rev`, `entity_events.entity_id` as a String,
`entity_kind` values reserved for `location`, `tag`, `alarm`, `operation`, `link_events` reason
columns (add `change_reason UInt8` and `remote_last_change DateTime` now, zero until G8 lands), and
`binding_reachability.unreachable_since` on the clearing row (G10). A column that is zero until its
carrier exists costs nothing in a sparse-serialized part (02 F71).

## 13. Unverified and open

1. A ClickHouse SQL function that decodes a protobuf `String` column: none found, absence unverified (L6).
2. JetStream stream sequence contiguity and non-reuse (needed by the gap query, 8.4): the fetched
   streams page says only "The first message you publish gets sequence `1`". Check
   `nats-server@v2.15.0/server/filestore.go`.
3. PostgreSQL dictionary source syntax (`SOURCE(POSTGRESQL(...))`): the dictionaries overview names it,
   the sources page returned 404 at two URLs. The design uses the ClickHouse-table source, which the
   range-hashed page shows.
4. Which legal retention obligation applies to FlowSeer audit records (8.2), and whether a lightweight
   delete satisfies erasure (02 F57 already lists this).
5. Whether `DeviceEvent` should carry `before`/`after` DeviceState (4.4). A schema question for the plan,
   outside this lane's remit.
6. Maximum practical size of an `IN (...)` literal list; the `max_query_size` bound is from memory of
   the setting's default and is unverified here.
7. The Site entity's shape (GOALS.md:22 "No record decides its shape yet"): `device_site_history`
   keys on the Location id of kind site, or on the IntegrationScope key until then.
8. The exact `ManagementProtocol` enum numbers for `binding_reachability.protocol`: not read in this
   pass (the enum lives in `binding.proto:60-98`, partially read). The Enum8 lists must be generated
   from the schema, which removes the risk.
9. Compression ratio of consecutive State payloads under ZSTD (3.2): inference, measured by 10.3.
10. Whether a `bloom_filter` on `device_id` in `entity_events` actually skips (depends on correlation
    with the key, 02 F19): measured by Q5-style checks in 10.3.
