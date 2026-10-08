---
title: Events: syslog, SNMP notifications, alarms, and transitions
date: 2026-10-08
status: research; sources fetched 2026-10-08, ClickHouse 26.8 LTS
---

# 07. Events: syslog, SNMP notifications, alarms, state transitions

Scope: the ClickHouse model for FlowSeer's event domains. Builds on
`docs/research/clickhouse-history-store/` (cited as 01, 02 F-numbers, 03) and does
not repeat them. External sources were fetched on 2026-10-08; quotes are short.
"Inference" marks reasoning from quoted text; "unverified" marks claims no fetched
source confirms. Statements about FlowSeer cite `path:line` in the worktree
`/Users/a.tegtmeier/Projects/worktrees/FlowSeer/plan-clickhouse-store`.

Findings are numbered E1 to E30; schema gaps G1 to G24.

---

## 0. Result in one table

| Domain | Table | Engine | ORDER BY | PARTITION BY | Dedup | Retention class default |
|---|---|---|---|---|---|---|
| Syslog | `syslog` | ReplicatedReplacingMergeTree | `(tenant_id, device_id, ts, record_id)` | `(retention_class, toDate(ts))` | key ends in `record_id`; read-time `LIMIT 1 BY` only where counts matter | 30 / 90 / 180 days |
| Syslog scope index | `syslog_by_scope` (MV target) | ReplicatedReplacingMergeTree | `(tenant_id, site_id, ts, device_id, record_id)` | same | same | same as `syslog` |
| SNMP notifications | `snmp_notifications` | ReplicatedReplacingMergeTree | `(tenant_id, device_id, ts, record_id)` | `(retention_class, toMonday(ts))` | same | 180 d / 1 y / 2 y |
| Alarm transitions | `alarm_events` | ReplicatedReplacingMergeTree | `(tenant_id, device_id, resource_kind, resource_key, type_id, type_qualifier, ts, record_id)` | `(retention_class, toMonday(ts))` | same | 1 y / 2 y / 3 y |
| Alarm intervals | `alarm_intervals` (MV target) | ReplicatedAggregatingMergeTree | `(tenant_id, device_id, resource_kind, resource_key, type_id, type_qualifier, raised_at)` | `toYYYYMM(raised_at)` | idempotent `min`/`max`/`argMax` | 3 y |
| Other transitions | one `<domain>_changes` per domain, shared column prefix | as 02 §12.2 | `(tenant_id, device_id, <entity>, ts, record_id)` | `(retention_class, toMonday(ts))` | same | 1 y / 2 y |

Every query shape has a cost that depends on (devices in scope, rows per device in
the window) or on LIMIT, never on total tenants or total rows. The one query whose
cost would otherwise grow with tenant data, "errors in site S last 24 h newest
first", is bounded by the `syslog_by_scope` index table (E17). Full-text search is
bounded by parts in the window plus matching granules (E18).

---

## 1. Syslog

### 1.1 Data semantics

**E1. The record.** `spec/proto/flowseer/event/log/v1/syslog_record.proto:17-74`:
`device` (required), `received_at` (required), `sent_at` (optional, "Unset when the
sender emitted NILVALUE"), `severity` and `facility` (pass-through enums, "Unset means
the message carried no PRI"), `hostname` (1..255), `app_name` (1..48), `proc_id`
(1..128), `msg_id` (1..32), `structured_data` (repeated element with repeated params,
element ids unique by CEL rule at line 19-23), `message bytes` ("may be UTF-8 or any
character encoding. Bounded at 65527 octets"), `source_address` (oneof v4/v6,
`spec/proto/flowseer/net/addr/v1/ip.proto:63-70`), `message_truncated bool`.
`spec/proto/flowseer/net/log/v1/syslog_severity.proto:4-8`: "Value 0 (EMERGENCY) is a
real registry value; presence distinguishes reported values from unset"; facility
likewise (`syslog_facility.proto:4-8`, `KERN = 0`).

The envelope `spec/proto/flowseer/integration/ingest/v1/ingest_record.proto:11-33`
adds `record_id` (UUID, "used as the bus message id for deduplication") and
`Provenance` (`spec/proto/flowseer/model/inventory/v1/provenance.proto:12-50`:
`binding`, `observed_at`, `edge`, `firmware_fingerprint`, protocol oneof with
`LogProtocol log = 11`). Tenant is ambient (stream), as the brief says.

**E2. What the bytes look like.** RFC 5424 §6.4
(`https://www.rfc-editor.org/rfc/rfc5424.txt`): "The character set used in MSG SHOULD
be UNICODE, encoded using UTF-8 ... If the syslog application cannot encode the MSG in
Unicode, it MAY use any other encoding"; a UTF-8 MSG "MUST start with the Unicode byte
order mask (BOM)". RFC 3164 §4.1.3 (`https://www.rfc-editor.org/rfc/rfc3164.txt`):
MSG "MUST contain visible (printing) characters", "no indication of the code set used
within the MSG is required", and the TAG "MUST NOT exceed 32 characters", terminated by
"[", ":" or space. FlowSeer's parser keeps the post-envelope payload as `Content`
including "a legacy process tag or vendor prefix" (`src/protocol/syslog/README.md:33-35`)
and the mapper copies `record.Content` into `message` unchanged except the 65527 cut
(`src/edge/agent/internal/syslogsource/mapper.go:135-143`). So `message` is bytes,
starts with the vendor prefix, and carries no timestamp or hostname (those were parsed
out of the envelope).

**E3. Vendor shapes.** The parser's corpus (`src/protocol/syslog/testdata/corpus/manifest.json`)
and README table (`src/protocol/syslog/README.md:48-61`) document the families FlowSeer
already recognises:

| Family | Content shape (corpus payload) | Extracted by parser |
|---|---|---|
| Cisco IOS/IOS-XE/NX-OS | `%LINK-3-UPDOWN: Interface down` (line 138), `%ETHPORT-5-IF_DOWN: down` (184) | `Vendor.Module`, `Severity`, `Mnemonic`, `Sequence`, `Counter` (`record.go:125-139`) |
| Cisco ASA | `%ASA-6-302013: connection built` (205) | `EventID` |
| Huawei/Comware | `%%01IFNET/4/LINK_STATE(l): down` (226), `%%10IFNET/4/LINK_UPDOWN: down` (310) | module, severity, mnemonic, flags |
| RUCKUS ICX | legacy `System: interface up` (268) and RFC 5424 with `[meta@1991 slot="1"]` (289) | structured data |
| Aruba AOS-S | `00076 ports: port up` (354) | numeric event id, subsystem |
| Aruba AOS-CX | `ops-switchd[214]: Event|403|LOG_INFO|||Port up` (375) | process tag, pipe fields |
| NETGEAR/D-Link | `MSTP[2110]: mstp_api.c(318) 237%% port enabled` (73) | component, thread, source, sequence |

Independent confirmation of the Cisco tag grammar: Logstash's pattern
`CISCOTAG [A-Z0-9]+-%{INT}-(?:[A-Z0-9_]+)` and
`CISCO_TAGGED_SYSLOG ^<%{POSINT}>%{CISCOTIMESTAMP}( %{SYSLOGHOST})? ?: %%{CISCOTAG}:`
(`https://raw.githubusercontent.com/logstash-plugins/logstash-patterns-core/main/patterns/ecs-v1/firewalls`,
lines 7-9). Juniper: the Junos page at `juniper.net/documentation/.../syslog-message-format.html`
returned 404; Junos structured-data uses the `junos@2636...` SD-ID and the
`process[pid]: TAG: text` legacy shape (memory, **unverified**; the Logstash `junos`
pattern file only covers `RT_FLOW_SESSION_*` tags). Cisco's and Aruba's own documentation
pages were blocked by their CDN ("Access Denied"); the shapes above rest on the
repository corpus (each entry cites its vendor source in `manifest.json`, e.g. line 266,
308, 352, 373) and the Logstash pattern.

Consequence for the schema: the vendor tag (`LINK-3-UPDOWN`, `IFNET/4/LINK_UPDOWN`,
`ASA-6-302013`, `00076`) is the single most useful filter after severity, and the parser
already extracts it at the edge, but `SyslogRecord` does not carry it. Either add
`vendor_module`, `vendor_mnemonic`, `vendor_event_id`, `vendor_severity` to
`SyslogRecord` (recommended; the parser has them as `Vendor` fields, `record.go:125-139`)
or derive a column in ClickHouse with a regexp (E9, fallback). Both are shown below.

**E4. Rates and cardinality.** The baseline records no syslog rate
(`docs/research/2026-10-01-production-monitoring-baseline.md` has no syslog volume).
Planning assumption, stated as such (**unverified**): 0.02 msg/s per device average
(1,700/day), with a long tail; storms of 10,000 msg/s from one device (the brief's
case). At 20,000 devices: about 35 M rows/day, 400 rows/s steady. Entities per device:
one stream per device; `app_name`/`vendor_tag` cardinality in the hundreds per vendor,
thousands fleet-wide (fits LowCardinality, F10).

### 1.2 Candidate models

| Candidate | Shape | Verdict |
|---|---|---|
| A. Device-led key `(tenant_id, device_id, ts, record_id)` | 02 §12.4 | **Chosen.** Every FlowSeer query is per device or per device list. |
| B. Time-bucket-led key `(tenant_id, toStartOfHour(ts), device_id, ts)` (ClickStack/OTel/SigNoz shape) | 03: `ORDER BY (toStartOfFiveMinutes(Timestamp), ServiceName, Timestamp)`; SigNoz `(ts_bucket_start, resource_fingerprint, ...)` | Rejected: per-device reads touch one range per bucket in the window (E15). ClickStack chose it because its queries are "last N minutes across everything" (F51), which FlowSeer does not have. |
| C. Site-led key `(tenant_id, site_id, device_id, ts, record_id)` | SigNoz resource-fingerprint lesson (03, patterns) | Rejected as the main key: a device that moves site splits its history across two ranges, and per-device reads must then know the historical site. Used instead as a secondary MV-fed table (E17). |
| D. Plain MergeTree + read-time `LIMIT 1 BY record_id` | 03 anti-patterns (PostHog on Replacing) | Rejected as the engine, kept as the read rule (E13). |
| E. One generic `events` table for syslog + traps + alarms | ntopng/Telegraf generic tables (03) | Rejected: three different column sets and query shapes (E36). |

### 1.3 Recommended DDL

```sql
CREATE TABLE flowseer.syslog
(
    tenant_id        LowCardinality(String),
    device_id        UUID,
    -- received_at from the collector (SyslogRecord.received_at). Partition and key time.
    ts               DateTime64(3)         CODEC(Delta(8), ZSTD(1)),
    record_id        UUID,
    -- device header timestamp (SyslogRecord.sent_at); 0 when NILVALUE or year-less clock
    sent_at          DateTime64(3)         CODEC(Delta(8), ZSTD(1)),
    -- RFC 5424 PRI; -1 = message carried no PRI (presence per syslog_severity.proto:7)
    severity         Enum8('none' = -1, 'emerg' = 0, 'alert' = 1, 'crit' = 2, 'err' = 3,
                           'warning' = 4, 'notice' = 5, 'info' = 6, 'debug' = 7),
    facility         Enum8('none' = -1, 'kern' = 0, 'user' = 1, 'mail' = 2, 'daemon' = 3, 'auth' = 4,
                           'syslog' = 5, 'lpr' = 6, 'news' = 7, 'uucp' = 8, 'cron' = 9, 'authpriv' = 10,
                           'ftp' = 11, 'ntp' = 12, 'audit' = 13, 'alert' = 14, 'clock' = 15,
                           'local0' = 16, 'local1' = 17, 'local2' = 18, 'local3' = 19,
                           'local4' = 20, 'local5' = 21, 'local6' = 22, 'local7' = 23),
    hostname         LowCardinality(String),
    app_name         LowCardinality(String),
    proc_id          LowCardinality(String),
    msg_id           LowCardinality(String),
    -- RFC 5424 SD, flattened one row per SD-PARAM; elements without params appear in sd_ids only
    sd_ids           Array(LowCardinality(String)),
    sd_param_sd_id   Array(LowCardinality(String)),
    sd_param_name    Array(LowCardinality(String)),
    sd_param_value   Array(String)          CODEC(ZSTD(1)),
    sd_items         Array(String) ALIAS arrayMap((e, n, v) -> concat(e, '/', n, '=', v),
                                                  sd_param_sd_id, sd_param_name, sd_param_value),
    message          String                 CODEC(ZSTD(1)),   -- raw bytes, any encoding
    message_truncated Bool,
    message_is_utf8  Bool MATERIALIZED isValidUTF8(message),
    source_ip        IPv6,                                    -- v4 stored mapped (F11)
    -- vendor fields (schema gap G1): from the new SyslogRecord vendor fields; until then derived:
    vendor_tag       LowCardinality(String) MATERIALIZED
        extract(message, '^%%?[0-9]*([A-Z0-9_]+[-/][0-7][-/][A-Z0-9_]+)'),
    vendor_family    LowCardinality(String),                  -- parser Vendor.Family (G1)
    vendor_module    LowCardinality(String),                  -- Cisco FACILITY, Huawei module (G1)
    vendor_mnemonic  LowCardinality(String),                  -- Cisco MNEMONIC, Huawei mnemonic (G1)
    vendor_event_id  LowCardinality(String),                  -- ASA 302013, AOS-S 00076, AOS-CX 403 (G1)
    vendor_severity  Int8,                                    -- the digit inside the tag, -1 absent (G1)
    vendor_sequence  UInt64 CODEC(T64, ZSTD(1)),              -- Cisco sequence number, 0 absent (G1)
    log_format       Enum8('unknown' = 0, 'rfc5424' = 1, 'rfc3164' = 2),  -- parser Format (G2)
    device_clock     Enum8('absolute' = 1, 'no_year' = 2, 'uptime' = 3, 'none' = 4),  -- why sent_at is 0 (G2)
    parse_status     Enum8('ok' = 1, 'partial' = 2, 'failed' = 3),        -- G3
    transport        Enum8('udp' = 1, 'tcp' = 2, 'tls' = 3),               -- G4
    transport_authenticated Bool,                                          -- G4
    -- sink-side storm suppression (E19): rows this row stands for
    repeat_count     UInt32 DEFAULT 1       CODEC(T64, ZSTD(1)),
    binding_id       UUID,                                    -- Provenance.binding
    edge_id          UUID,                                    -- Provenance.edge, 0 if central
    site_id          LowCardinality(String),                  -- site at write time (F69)
    retention_class  Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    INDEX idx_msg  message   TYPE text(tokenizer = splitByNonAlpha, preprocessor = lower(message)) GRANULARITY 64,
    INDEX idx_sd   sd_items  TYPE text(tokenizer = array) GRANULARITY 64,
    INDEX idx_tag  vendor_tag TYPE bloom_filter(0.01) GRANULARITY 1,
    INDEX idx_sev  severity  TYPE minmax GRANULARITY 1
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY (retention_class, toDate(ts))
ORDER BY (tenant_id, device_id, ts, record_id)
TTL toDateTime(ts) + INTERVAL 30 DAY  DELETE WHERE retention_class = 'short',
    toDateTime(ts) + INTERVAL 90 DAY  DELETE WHERE retention_class = 'standard',
    toDateTime(ts) + INTERVAL 180 DAY DELETE WHERE retention_class = 'long',
    toDateTime(ts) + INTERVAL 14 DAY  RECOMPRESS CODEC(ZSTD(3))
SETTINGS ttl_only_drop_parts = 1, index_granularity = 8192;
```

Reasoning per clause:

**E5. ORDER BY.** Tenant first (F6), device second (every query filters it; the row
policy and the query builder filter tenant), `ts` third, `record_id` last so a
redelivered row is adjacent to its original and collapses on merge (01 §3; F45). Newest
first per device is a reverse read in key order with early stop under LIMIT: "If ORDER BY
expression has a prefix that coincides with the table sorting key ... This allows to avoid
reading all data in case of specified LIMIT"
(`https://clickhouse.com/docs/sql-reference/statements/select/order-by.md`, "Optimization
of data reading"), and `message` is read lazily for the LIMIT rows only (F51).

**E6. PARTITION BY `(retention_class, toDate(ts))`.** Daily unit for 30 to 180 day
retention (F7, F8). `retention_class` in the partition key makes every part single-class,
so `ttl_only_drop_parts = 1` drops whole parts (F36; Snuba's `(retention_days,
toMonday(ts))` pattern, 03). Cardinality: 3 classes x 180 days = 540 partitions, inside
the "fewer than 100 - 1,000" guidance (F7). Each insert touches at most 2 days x 3
classes = 6 parts (F16); at one insert per second that is bounded and far from
`parts_to_delay_insert` (F63). Partition time is `ts` (received), never `sent_at`: device
clocks without a year (`*Mar 1 00:00:01`, corpus line 138) or wrong by years would scatter
one batch across many partitions and hit `max_partitions_per_insert_block` ("Too many
partitions for a single INSERT block", `scratchpad/md/s_maxpart.md:131`; SigNoz drops such
batches, 03).

**E7. Types.** `ts DateTime64(3)`: bursts put many rows in one second per device and the
collector clock has sub-second precision; F12 allows finer-than-second "only if needed",
and here ordering inside a burst needs it. `sent_at DateTime64(3)` with 0 for absent (no
Nullable, F9); RFC 5424 TIMESTAMP carries optional fractions. `severity`/`facility` as
`Enum8` with `'none' = -1`: Enum8 values range over `[-128, 127]` and "numbers can be in
an arbitrary order" (`https://clickhouse.com/docs/sql-reference/data-types/enum.md`, lines
15 and 130 of the fetched page), so the real zero values (`emerg`, `kern`) stay 0 and
absence is a distinct 1-byte value; no Nullable column. `message String`: "Strings can
contain an arbitrary set of bytes, which are stored and output as-is"
(`https://clickhouse.com/docs/sql-reference/data-types/string.md`, "Encodings"), so
non-UTF-8 is stored losslessly (E10). `source_ip IPv6` with mapped v4 (F11).
`hostname`, `app_name`, `proc_id`, `msg_id` LowCardinality (open sets under 10k, F10).
`binding_id`/`edge_id` from `Provenance` (`provenance.proto:14-21`) so a row can be traced
to the lane that received it (the amendment at
`docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md:225-246` says a
record's lane need not serve the device, so the binding is evidence, not identity).

**E8. Structured data: parallel arrays, not Map.** Three options compared:

| Option | Preserves repeated params and order | Search | Cost when empty | Notes |
|---|---|---|---|---|
| `Map(LowCardinality(String), String)` keyed `sdid/param` | No (a repeated PARAM-NAME overwrites) | `keyValuePairs` tokenizer (F50) | tiny | ClickStack default for attributes (03) |
| Parallel arrays (`Nested`-style: `sd_param_sd_id`, `sd_param_name`, `sd_param_value`) | Yes, exactly the proto shape (`syslog_record.proto:77-98`) | `has(...)`, `arrayExists`, and a text index with `tokenizer = array` on an ALIAS of `concat(sd, '/', name, '=', value)` (ClickStack's `ResourceAttributeItems ALIAS arrayMap(...)` + `text(tokenizer = 'array')`, 03) | tiny (three empty arrays) | "All column arrays of a single nested data structure have the same length" (`https://clickhouse.com/docs/sql-reference/data-types/nested-data-structures/nested.md`); filtering needs array functions or ARRAY JOIN (same page) |
| `JSON` | Yes if nested | path subcolumns | small | For "dynamic or unpredictable structure" (F13); SD-IDs are few per vendor, so overkill |

The proto permits a PARAM-NAME to repeat inside an element (`repeated SyslogStructuredDataParam params`,
`syslog_record.proto:80`), and the parser "preserve[s] order and duplicates"
(`src/protocol/syslog/README.md:35-36`). Only the array layout keeps that. Network
syslog rarely carries SD (one corpus entry out of 26 does, line 289), so the column cost is
nil either way; correctness decides. Element ids without params go to `sd_ids`.

**E9. Vendor tag column.** `vendor_tag` is the filter for "all link flaps" or "all
config-change messages" without a text search. Derived form: a RE2 `extract` over the
Cisco `%FAC-SEV-MNEMONIC`, Huawei `%%NNMODULE/SEV/MNEMONIC`, and ASA `%ASA-6-NNNNNN`
shapes (E3). Preferred form: a plain column filled from new `SyslogRecord` vendor fields
(no regexp in the database, and the parser's tested extraction is reused). The
`bloom_filter(0.01) GRANULARITY 1` skip index serves "rare values that are critical for
search" (F19): inside a device's range, `LINK-3-UPDOWN` rows are sparse among `info`
rows, so granules without the tag are skipped; verify with `EXPLAIN indexes = 1` and drop
it if it skips nothing (F19).

**E10. Non-UTF-8 bytes.** Stored as-is (E7). The text index's `splitByNonAlpha`
tokenizer splits on bytes outside `[0-9A-Za-z_]` ("a token is defined as the longest
possible sub-sequence of consecutive characters `[0-9A-Za-z_]`",
`https://clickhouse.com/docs/sql-reference/functions/string-search-functions.md`,
`hasToken`), so a BOM or Latin-1 byte is a separator and never corrupts a token. `lower()`
as preprocessor lowercases ASCII bytes only, which is what the tag vocabulary needs. The
API returns `message` as bytes and a display string via `toValidUTF8(message)`
("replacing any invalid UTF-8 characters with the replacement character",
`https://clickhouse.com/docs/sql-reference/functions/string-functions.md`, `toValidUTF8`);
`message_is_utf8` (1 bit, materialized with `isValidUTF8`) lets the UI mark such rows
without scanning `message`.

**E11. Text index configuration.**
`https://clickhouse.com/docs/engines/table-engines/mergetree-family/invertedindexes.md`
(fetched page `scratchpad/md/textidx.md`): the preprocessor's typical uses are
"Lower/upper-casing" and "Removing or transforming unwanted characters" (lines 298-302);
"we therefore recommend using preprocessor expressions" over indexing `lower(col)`
because the emulated form "is only applied if it matches the filter condition in the
WHERE clause" (326-329); the functions `hasToken`, `hasAllTokens`, `hasAnyTokens`,
`hasPhrase` "use the preprocessor to first transform the search term" (332); `hasToken`
"has certain pitfalls ... We recommend using `hasAnyTokens` and `hasAllTokens` instead"
(string-search-functions page, `hasToken` note). Decisions:

- Tokenizer `splitByNonAlpha` (default; the docs' and ClickStack's choice for log
  bodies). It splits `%LINK-3-UPDOWN` into `link`, `3`, `updown`, which is why the tag
  has its own column (E9). It splits `10.1.2.3` into four digit tokens; an IP search is
  `hasAllTokens(message, ['10','1','2','3'])` narrowed by the key range, then an exact
  `LIKE` on the survivors. The `asciiCJK` tokenizer keeps "`.` and `'` for same-type
  characters" as connectors (`tokens` function page), so `10.1.2.3` would stay one token;
  benchmark both for index size and IP/MAC search (section 6).
- No timestamp stripping: the envelope timestamp is already parsed out (E2). Cisco
  sequence numbers and uptime stamps are envelope too (corpus line 138 content starts at
  `%`). So the preprocessor is only `lower(message)`.
- No postprocessor dropping severity words: the severity is a column, and the words
  `error`/`down` inside messages are exactly what people search.
- `GRANULARITY 64`: the text index is "tens to hundreds of megabytes per part" (F50); a
  coarse granularity bounds index size, and the primary key already narrows the device
  range before the index is consulted. Benchmark 16 and 64.
- `support_phrase_search` stays off (experimental, line 108 of the page).
- Works on ReplacingMergeTree since 25.12 (F38).

**E12. Codecs and bytes/row (estimate, to be measured).** `ts Delta(8), ZSTD(1)`
as ClickStack (F25). Within a device's run: `device_id` compresses to near zero (runs of
one UUID), `tenant_id` LowCardinality, `severity`/`facility` repeat, `source_ip`
repeats, `app_name`/`vendor_tag` dictionary-coded. `record_id` (UUIDv7) is the one
incompressible column: ~16 B/row. `message` at ~120 B raw compresses 4-8x on repetitive
device logs: ~20-30 B. Estimate 50-70 B/row data plus 10-30 B/row text index. At 35 M
rows/day: 2-3.5 GB/day; 90 days standard retention: 180-315 GB on one shard, before
replication. Validate with `system.columns` `data_compressed_bytes` and `system.parts`
`secondary_indices_compressed_bytes` (`https://clickhouse.com/docs/operations/system-tables/parts.md`).

### 1.4 Dedup under redelivery

**E13. Record_id in key + ReplacingMergeTree vs plain MergeTree + `LIMIT 1 BY`.**

| | Replacing, `record_id` last in key | Plain MergeTree, `LIMIT 1 BY (tenant_id, device_id, ts, record_id)` |
|---|---|---|
| Duplicate lifetime | Until the parts merge; "does not guarantee the absence of duplicates" (01 §3; replacing page line 13) | Forever on disk; hidden only by queries that use `LIMIT BY` |
| Storage | duplicates removed in background | kept (hub restart re-sourcing, `src/modules/edgebus/README.md` via direction record line 258-262, is the source; volume small but unbounded) |
| Read cost | identical rows are adjacent in key order; `LIMIT 1 BY` or `FINAL` is cheap on a PK-filtered range ("query-time FINAL is generally fine when queries filter on primary key columns", F18) | same read; `LIMIT BY` "selects the first n rows for each distinct value of expressions" after sorting (`https://clickhouse.com/docs/sql-reference/statements/select/limit-by.md`) |
| Projections | `deduplicate_merge_projection_mode` default `throw` for non-classic MergeTree (01 §3; `scratchpad/md/mt_other.md:266-274`): no projections | projections allowed, but lightweight DELETE (tenant forget, F57) "aren't supported for tables with projections" (projections page line 460) |
| Merge CPU | Replacing merges compare keys; marginal | plain |

Recommendation: Replacing with `record_id` last in the key, plus the read rule
"list queries accept a transient duplicate; count and chart queries use
`LIMIT 1 BY record_id` inside the key range or `FINAL`". The reason to prefer Replacing
over plain is storage hygiene, not read correctness; the reason to keep `record_id` in
the key in both cases is adjacency. The window of visible duplicates is small: a
redelivery comes from a hub restart (direction record line 258-262) and the sink's
`insert_deduplication_token` catches an in-window retry of the same batch (F48). The
JetStream stream itself drops repeats inside its ten-minute window (same lines), so
ClickHouse sees a duplicate only past that window. Count queries use `sum(repeat_count)`
(E19) with `LIMIT 1 BY record_id` first:

```sql
SELECT severity, sum(repeat_count) AS n
FROM (
  SELECT severity, repeat_count
  FROM flowseer.syslog
  WHERE tenant_id = {t:String} AND device_id = {d:UUID}
    AND ts >= {from:DateTime64(3)} AND ts < {to:DateTime64(3)}
  ORDER BY ts, record_id
  LIMIT 1 BY record_id)
GROUP BY severity;
```

### 1.5 Representative queries and cost model

Notation: D = devices in scope, r = rows per device in the window (rate x window),
G = 8,192 rows per granule, P = parts overlapping the window (bounded by partitions in
the window x parts per partition, typically ≤ 20 per day after merges).

**E14. Per device, newest first.**

```sql
SELECT ts, severity, vendor_tag, app_name, message
FROM flowseer.syslog
WHERE tenant_id = {t:String} AND device_id = {d:UUID}
  AND ts >= now() - INTERVAL 7 DAY
ORDER BY ts DESC, record_id DESC
LIMIT 200;
```

Cost: index range = one key range; granules read ≈ ceil(200 / G) + 1 from the end of the
range (reverse read in order, early stop: order-by page "Optimization of data reading");
`message` materialized for 200 rows (F51). Independent of r, D, total size. With
`WHERE severity <= 'err'` added, the read continues backwards until 200 matches: granules
≈ 200 / (r_err / r) / G, bounded by the device's granules in the window, r / G; the
`minmax` on `severity` skips granules whose minimum severity is above `err` (F19,
"minmax indexes work particularly well with ranges", skip-index page).

**E15. Why the bucket-led key loses this query.** With `(tenant_id, toStartOfHour(ts),
device_id, ts)` the same query is one key range per hour bucket: 168 ranges for 7 days,
each at least one granule, even for a device that logged ten lines: ≥ 168 granules
(1.4 M rows) versus ≤ 2 granules. The bucket key wins only for "every device in the
tenant in the last 5 minutes", which is a tenant-wide range in both keys but contiguous in
time only under the bucket key; FlowSeer's scope queries resolve to device lists (brief),
which the device key serves directly.

**E16. Scope query the naive way.** "errors in site S last 24 h newest first, LIMIT
100" with S resolved to D = 2,000 device ids:

```sql
SELECT ts, device_id, severity, vendor_tag, message
FROM flowseer.syslog
WHERE tenant_id = {t:String} AND device_id IN {devices:Array(UUID)}
  AND ts >= now() - INTERVAL 24 HOUR AND severity <= 'err'
ORDER BY ts DESC LIMIT 100;
```

Cost: D key ranges; granules ≥ D (one per device even if the device has one row), rows
read ≈ Σ max(G, r_dev) ≈ D x G when r < G = 16 M rows for D = 2,000, then a sort of the
survivors. Read columns are the key plus `severity` (lazy materialization defers
`message`). The read-in-order early stop does not apply because `ORDER BY ts DESC` is not
a prefix of the key once `device_id` is a set rather than a constant (inference from the
"prefix that coincides with the table sorting key" rule). This is bounded (by D x G) and
independent of total tenants, but 16 M rows per page load is the wrong constant. Hence:

**E17. Scope index table, fed by an incremental MV (allowed: it is a projection of
append-only rows, not current state).**

```sql
CREATE TABLE flowseer.syslog_by_scope
(
    tenant_id       LowCardinality(String),
    site_id         LowCardinality(String),
    ts              DateTime64(3) CODEC(Delta(8), ZSTD(1)),
    device_id       UUID,
    record_id       UUID,
    severity        Enum8('none' = -1, 'emerg' = 0, 'alert' = 1, 'crit' = 2, 'err' = 3,
                          'warning' = 4, 'notice' = 5, 'info' = 6, 'debug' = 7),
    vendor_tag      LowCardinality(String),
    retention_class Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    INDEX idx_sev severity TYPE minmax GRANULARITY 1
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY (retention_class, toDate(ts))
ORDER BY (tenant_id, site_id, ts, device_id, record_id)
TTL toDateTime(ts) + INTERVAL 30 DAY  DELETE WHERE retention_class = 'short',
    toDateTime(ts) + INTERVAL 90 DAY  DELETE WHERE retention_class = 'standard',
    toDateTime(ts) + INTERVAL 180 DAY DELETE WHERE retention_class = 'long'
SETTINGS ttl_only_drop_parts = 1;

CREATE MATERIALIZED VIEW flowseer.syslog_by_scope_mv TO flowseer.syslog_by_scope AS
SELECT tenant_id, site_id, ts, device_id, record_id, severity, vendor_tag, retention_class
FROM flowseer.syslog;
```

Two-step scope query:

```sql
WITH hits AS (
  SELECT device_id, ts, record_id
  FROM flowseer.syslog_by_scope
  WHERE tenant_id = {t:String} AND site_id = {s:String}
    AND ts >= now() - INTERVAL 24 HOUR AND severity <= 'err'
  ORDER BY ts DESC LIMIT 100)
SELECT s.ts, s.device_id, s.severity, s.vendor_tag, s.message
FROM flowseer.syslog AS s
WHERE s.tenant_id = {t:String}
  AND (s.device_id, s.ts, s.record_id) IN (SELECT device_id, ts, record_id FROM hits)
ORDER BY s.ts DESC;
```

Cost: step 1 is one key range `(tenant, site)` read in reverse order with early stop:
granules ≈ ceil(100 / (r_err/r) / G) + 1, where r is now the site's rows per 24 h; with
the `minmax` on severity skipping info-only granules. Step 2 is 100 point lookups by
primary key: ≤ 100 granules. Both independent of D and of total data. The index table
costs ~40 B/row (no `message`), roughly half of the main table; the MV runs on the insert
path (F20, "no appreciable impact"). Its `site_id` is the site at write time (F69); a
device moved yesterday shows under its old site for yesterday's rows, which is the
historically correct answer for a log. Dedup: same key rule, `record_id` last.

Why not a projection: `deduplicate_merge_projection_mode = throw` on
ReplacingMergeTree (E13) and the lightweight-DELETE conflict (F57). Why not `site_id` in
the main key: E/C in 1.2.

**E18. Full-text search across a tenant, 7 days.**

```sql
SELECT ts, device_id, severity, message
FROM flowseer.syslog
WHERE tenant_id = {t:String}
  AND ts >= now() - INTERVAL 7 DAY
  AND hasAllTokens(message, ['dhcp', 'snooping'])
ORDER BY ts DESC LIMIT 200
SETTINGS max_rows_to_read = 200000000, max_execution_time = 30;
```

Cost model: the primary key prunes to the tenant's granules in the 7-day window
(`tenant_id` prefix plus partition minmax on `toDate(ts)`, F7); the text index is then
consulted per part for the needle tokens: P parts x posting-list lookups, then only the
granules whose postings contain all tokens are read (the docs' benchmark: 9.51 GB to
3.15 MB processed, F50). Rows read ≈ matching granules x G + the index reads; the
index read is per part, so it depends on P (partitions in window x parts per partition),
not on rows. For a 10,000-device tenant at 1,700 rows/day/device: 119 M rows in the
window, of which a selective search touches thousands of granules at most. Bounds the
query builder enforces: a tenant, a time window ≤ retention, at least one token of ≥ 3
characters, `max_rows_to_read` (F58), `max_execution_time` with `break` for the UI
(F56), and the read-only user's profile (F56). The `tokens(message)` function on a sample
shows what the index will hold (`https://clickhouse.com/docs/sql-reference/functions/splitting-merging-functions.md`,
`tokens`).

**E19. Storm handling (one device at 10,000 msg/s).**

Where the storm is absorbed, in order:

1. Edge receiver: "UDP drops new datagrams when admission is full"; partial plus queued
   frames default 256 (`src/protocol/syslog/README.md:88-91,112-118`); drops are counted
   (`src/edge/agent/internal/syslogsource/source.go:105-107`,
   `flowseer.edge.syslog.dropped`). So the edge already caps a storm at what it can parse
   and publish; the number is not fixed by policy today (**open**).
2. Sink: a per-(tenant, device) token bucket. Above the budget the sink keeps one row per
   (device, vendor_tag or first 64 bytes of `message`) per 10 s and sets `repeat_count`
   to the number it stands for, as `RawEvidence.suppressed_since_last` already does for
   raw bytes (`ingest_record.proto:46-48`). Counts stay exact through `sum(repeat_count)`
   (E13); the lines lost are duplicates of a line that is kept. This is sink policy, not a
   ClickHouse feature.
3. ClickHouse: with batching by time (one insert per table per second) a storm raises rows
   per insert, not parts per insert ("one new data part for each unique partition key
   value among the inserted rows", F7), so part counts stay flat. The device's rows are
   contiguous in its key range, so no other device's or tenant's granules gain rows:
   every per-device and per-site cost above is unchanged for everyone else. What the
   storm does cost: that day's partition grows (merges of larger parts; the text index
   for a flood of identical lines adds few tokens but larger posting lists), TTL drops it
   on schedule, and the storming device's own queries read more granules (r grows). Per
   tenant metering is visible through `quota_key` on the query side (F54); on the insert
   side the sink is one user, so a storm's cost is accounted by the sink's own per-tenant
   counters, not by ClickHouse quotas.

**E20. Rollups.** One incremental MV into a `SummingMergeTree` keyed
`(tenant_id, device_id, severity, vendor_tag, hour)` with `sum(repeat_count)`, like
ClickStack's `otel_logs_kv_rollup_15m` (03). Serves "messages per severity per hour over
30 days" (chart) at ≤ 24 x 30 x |severity| rows per device, independent of r.
Duplicates past the dedup window double-count in a sum (F20 risk column, Contentsquare
lesson in 03); accepted for a chart, and the raw table with `LIMIT 1 BY` is the exact
path.

**E21. Failure modes avoided.**

| Failure | Avoided by |
|---|---|
| Device clock scatters partitions (`max_partitions_per_insert_block`) | partition on `ts` = received_at (E6) |
| Timestamp tokens bloat the text index (F50) | envelope timestamps never reach `message` (E2); Cisco uptime stamps are envelope in the parser |
| Severity zero mistaken for absent | `Enum8('none' = -1, ...)` (E7) |
| Invalid UTF-8 breaks display or tokenization | bytes stored as-is; `toValidUTF8` at the API; byte-level tokenizer (E10) |
| Scope query cost grows with site size | `syslog_by_scope` with early-stop reverse read (E17) |
| ORDER BY changed later (OTel changed it twice, 03) | the key is derived from the query shapes above and benchmarked before the first deployment |
| A tenant's storm slows another tenant's reads | tenant-first, device-second key keeps granules disjoint (E19) |

---

## 2. SNMP notifications (traps and informs)

### 2.1 Data semantics

**E22. No record type exists.** `IngestRecord.payload` has only `syslog`
(`ingest_record.proto:28-32`). The decoded shape FlowSeer already has is
`src/protocol/snmp/trap.go:22-41`: `Source net.IP`, `Community secret.Value`, `Version`,
`EngineID []byte`, `UserName string`, `VarBinds []VarBind` ("in wire order, including the
SNMPv2-MIB sysUpTime.0 and snmpTrapOID.0 canonical leading bindings"), `Received`. v1
traps are normalised to the v2 shape with `snmpTrapEnterprise.0` appended last
(`trap_listen.go:275-304`, RFC 2576 §3.1). The varbind sum type has 15 value kinds plus 3
exceptions (`varbind.go:15-30`, `kind.go:12-57`).

Standards: "the first two variable bindings in the variable binding list of an
InformRequest-PDU are sysUpTime.0 [RFC3418] and snmpTrapOID.0 [RFC3418] respectively"
(RFC 3416 §4.2.7, `https://www.rfc-editor.org/rfc/rfc3416.txt`; §4.2.6 says the same for
SNMPv2-Trap-PDU); `snmpTrapOID` is "The authoritative identification of the notification
currently being sent" (RFC 3418, `https://www.rfc-editor.org/rfc/rfc3418.txt`, line 554+);
`snmpTrapEnterprise` "occurs as the last varbind" when a v1 trap is proxied (same page).
Trap definitions in `spec/mib/` (697 files with `NOTIFICATION-TYPE`): `linkDown
NOTIFICATION-TYPE OBJECTS { ifIndex, ifAdminStatus, ifOperStatus }`
(`spec/mib/ietf/IF-MIB:1106-1107`); `snTrapRunningConfigChanged NOTIFICATION-TYPE OBJECTS
{ snAgGblTrapMessage }` (`spec/mib/ruckus/icx/FOUNDRY-SN-NOTIFICATION-MIB:1189-1200`),
which `GOALS.md:59-66` names as the trigger of the drift read: "A trap says that a change
happened and not what changed"; `alarmActiveState NOTIFICATION-TYPE OBJECTS {
alarmActiveModelPointer, alarmActiveResourceId }` with a mandated two-second throttle
per alarm ("When traps are throttled, they are dropped, not queued",
`spec/mib/ietf/ALARM-MIB:1021-1043`).

A notification is therefore: a typed header (uptime, trap OID, enterprise, version,
source), an ordered varbind list of (OID, kind, value), and FlowSeer's mapping outcome
(which domain record it produced, if any). Rates: low (tens per device per day) with
flap storms (a port flapping at 10/s). Cardinality of distinct trap OIDs per tenant: tens
to low hundreds.

The proto record type to add (`flowseer.event.snmp.v1.NotificationRecord`, inference from
the `event/` admission rule `spec/proto/flowseer/event/README.md:10-16`) carries exactly
these columns; the table below is designed from the Go struct and the RFCs so that the
message and the table agree when the message is written.

### 2.2 Candidates

| Candidate | Verdict |
|---|---|
| A. One `snmp_notifications` table, varbinds as parallel arrays, mapping result as columns | **Chosen**: one row per notification, queryable by trap name without unpacking varbinds, and the mapping outcome is a first-class filter ("unmapped traps last 7 days"). |
| B. Varbinds exploded, one row per varbind | Rejected: multiplies rows by 3-10, and "which trap" becomes a GROUP BY. |
| C. Map the trap into the domain `changes` table only (no notification table) | Rejected: loses the unmapped and partially mapped traps, which are precisely the ones an operator debugs; the GOALS trigger needs the raw fact "config-change trap at T" independent of what the drift read found. |
| D. Varbinds as `Map(String, String)` | Rejected: varbind OIDs can repeat (table rows), order matters for v1 translation; the array layout keeps wire order (E8 reasoning). |

### 2.3 DDL

```sql
CREATE TABLE flowseer.snmp_notifications
(
    tenant_id        LowCardinality(String),
    device_id        UUID,
    ts               DateTime64(3)          CODEC(Delta(8), ZSTD(1)),   -- Trap.Received
    record_id        UUID,
    source_ip        IPv6,                                             -- Trap.Source, v4 mapped
    snmp_version     Enum8('v1' = 1, 'v2c' = 2, 'v3' = 3),              -- Trap.Version
    pdu              Enum8('trap' = 1, 'inform' = 2),                   -- needs a field on Trap (today implicit)
    sys_uptime       UInt32                 CODEC(T64, ZSTD(1)),       -- sysUpTime.0, TimeTicks
    trap_oid         String                 CODEC(ZSTD(1)),            -- snmpTrapOID.0, dotted
    trap_name        LowCardinality(String),                           -- SMI-resolved, e.g. 'IF-MIB::linkDown'
    enterprise_oid   String                 CODEC(ZSTD(1)),            -- snmpTrapEnterprise.0 or '' (v2/v3)
    engine_id        String                 CODEC(ZSTD(1)),            -- v3 only, hex
    user_name        LowCardinality(String),                           -- v3 USM securityName
    -- varbinds in wire order, excluding the two canonical leaders and the enterprise trailer
    vb_oid           Array(String)          CODEC(ZSTD(1)),
    vb_name          Array(LowCardinality(String)),                    -- SMI-resolved object name with index
    vb_kind          Array(Enum8('unknown' = 0, 'integer32' = 1, 'uinteger32' = 2, 'octet_string' = 3,
                                 'object_id' = 4, 'bit_string' = 5, 'counter32' = 6, 'gauge32' = 7,
                                 'timeticks' = 8, 'counter64' = 9, 'ip_address' = 10, 'nsap' = 11,
                                 'opaque' = 12, 'opaque_float' = 13, 'opaque_double' = 14, 'null' = 15,
                                 'no_such_object' = 16, 'no_such_instance' = 17, 'end_of_mib_view' = 18)),
    vb_value         Array(String)          CODEC(ZSTD(1)),            -- canonical text rendering
    vb_items         Array(String) ALIAS arrayMap((n, v) -> concat(n, '=', v), vb_name, vb_value),
    -- mapping outcome
    mapping          Enum8('mapped' = 1, 'unknown_trap' = 2, 'unknown_varbind' = 3, 'rejected' = 4, 'ignored' = 5),
    mapped_domain    LowCardinality(String),                           -- 'interface', 'config', 'alarm', '' when none
    mapped_record_id UUID,                                             -- record_id of the produced transition, 0 when none
    mapped_entity    String                 CODEC(ZSTD(1)),            -- e.g. ifIndex or interface name
    binding_id       UUID,
    edge_id          UUID,
    site_id          LowCardinality(String),
    retention_class  Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    INDEX idx_trap trap_name TYPE set(256) GRANULARITY 4,
    INDEX idx_vb   vb_items  TYPE text(tokenizer = array) GRANULARITY 16
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY (retention_class, toMonday(ts))
ORDER BY (tenant_id, device_id, ts, record_id)
TTL toDateTime(ts) + INTERVAL 180 DAY DELETE WHERE retention_class = 'short',
    toDateTime(ts) + INTERVAL 1 YEAR   DELETE WHERE retention_class = 'standard',
    toDateTime(ts) + INTERVAL 2 YEAR   DELETE WHERE retention_class = 'long'
SETTINGS ttl_only_drop_parts = 1;
```

**E23. Reasoning.** The community string is never stored (it is `secret.Value`,
`trap.go:26-27`); `engine_id`/`user_name` are identifiers, not secrets. Weekly partitions
because retention is a year or two (F7): 3 x 104 = 312 partitions. `trap_name` with a
`set(256)` index: per device the distinct trap names in 32k rows are few, so "config-change
traps for device D" skips granules without the name (skip-index page: set indexes suit
columns with few distinct values per block). `vb_items` with the `array` tokenizer
answers "any trap mentioning ifIndex 12" or a MAC without exploding rows. The mapping
columns tie the notification to the transition it produced (E35): the trap is evidence,
the transition is the domain fact.

### 2.4 Queries and cost

```sql
-- config-change traps per device, last 30 days (drift trigger audit)
SELECT device_id, count() AS n, max(ts) AS last
FROM flowseer.snmp_notifications
WHERE tenant_id = {t:String} AND device_id IN {devices:Array(UUID)}
  AND ts >= now() - INTERVAL 30 DAY AND mapped_domain = 'config'
GROUP BY device_id;
```

Cost: D key ranges; granules ≈ D x ceil(r / G) with r = traps per device per 30 days
(tens to hundreds): ≈ D granules. Independent of total size.

```sql
-- unmapped traps in a tenant, last 7 days, top trap OIDs
SELECT trap_oid, trap_name, count() AS n
FROM flowseer.snmp_notifications
WHERE tenant_id = {t:String} AND ts >= now() - INTERVAL 7 DAY AND mapping != 'mapped'
GROUP BY trap_oid, trap_name ORDER BY n DESC LIMIT 20;
```

Cost: tenant range in the window: rows = tenant traps in 7 days (D_tenant x r), a
narrow read of three small columns; a 10,000-device tenant at 50 traps/day/device is
3.5 M rows, a few hundred granules. Bounded by the tenant's own data.

### 2.5 Dedup, rollup, failure modes

Same key rule as syslog (E13). No rollup needed at these rates; the per-device counts above
are cheap. Failure modes: a flap storm (10 traps/s from one port for an hour: 36k rows)
sits in one device range; `linkDown`/`linkUp` throttling belongs to the transition
producer (E35), not to this table, which records what arrived. Informs: the acknowledgement
is a transport matter at the edge (`usm_inform.go`); the table records the PDU type.

---

## 3. Alarms

### 3.1 Data semantics

**E24. The messages.** `spec/proto/flowseer/model/alarm/v1/alarm.proto`: `AlarmLocalRef`
= `resource` (oneof `device` | `component` ComponentLocalRef | `interface_name` | `other`,
lines 40-57) + `type_id` + optional `type_qualifier` (RFC 8632's `alarm-type-qualifier`,
lines 61-77). `AlarmState` (lines 89-108): `severity` (zero rejected, "Unset means the
source did not report one"), `cleared bool` (required), `text`, `created_at`,
`last_raised_at`, `last_changed_at`. `AlarmEvent` (lines 114-139): `ref`, `before`
("Unset means the alarm was newly raised"), `after` ("Unset means the device stopped
listing the alarm"), at least one side. The package comment (lines 3-8) says
acknowledgment is not modelled. `CONCEPTS.md:79-84`: "An Alarm is managed as an observed
state and transition (AlarmState, AlarmEvent), distinct from an append-only syslog record";
the baseline lesson "Standing versus transition: Alarm state and alarm events are separate"
(`docs/research/2026-10-01-production-monitoring-baseline.md:279`).

RFC 8632 §3.5.1 (`https://www.rfc-editor.org/rfc/rfc8632.txt`): lifecycle "raise, change
severity, change severity, clear, being raised again"; "Alarms are not deleted when they
are cleared"; "Alarms are not cleared by operators; only the underlying instrumentation
can clear an alarm"; "the alarm severity does not include 'cleared'; alarm clearance is a
boolean flag"; the optional `alarm-history` feature keeps a `status-change* [time]` list
with `perceived-severity` and `alarm-text` per change. ALARM-MIB (RFC 3877) keys active
alarms by `(alarmListName, alarmActiveDateAndTime, alarmActiveIndex)`
(`https://www.rfc-editor.org/rfc/rfc3877.txt`, line 1241), so the raise time is part of
the instance identity in the SNMP model too.

Transition kinds the sink derives from (before, after):

| before | after | transition |
|---|---|---|
| unset | cleared = false | `raise` |
| cleared = false | cleared = true | `clear` |
| cleared = true | cleared = false | `raise` (re-raise, new interval) |
| severity a | severity b, cleared = false | `severity_change` |
| text a | text b, otherwise equal | `text_change` |
| any | unset | `delisted` (device stopped listing; treated as clear with `cleared_by = 'delisted'`) |

Rates: low (20,000 devices x ~5 transitions/day = 100k rows/day). Standing alarms per
device: tens. Cardinality of `type_id` per vendor: hundreds.

### 3.2 Candidates

| Candidate | "Active at T" cost | MTTR | Redelivery | Verdict |
|---|---|---|---|---|
| A. Transition rows only (`alarm_events`), intervals reconstructed at read time with `leadInFrame` | all events of the scope's alarms from retention start to T (an alarm raised a year ago and still active is found only by scanning to it) | window function over events | adjacent duplicate rows, `LIMIT 1 BY` | Needed as the truth table, insufficient alone |
| B. Interval rows only (one row per alarm instance, updated on clear) | one range per device, rows = instances with `raised_at <= T` | `cleared_at - raised_at` | requires update-in-place (ReplacingMergeTree with `ver`), FINAL on reads; loses severity-change history | Rejected alone |
| C. A + an `AggregatingMergeTree` interval table fed by an MV (`min(cleared_at)`, `max(severity)`, `argMax`) | as B | as B | idempotent aggregates (03, presence-interval pattern; TimeSeries tags table `min_time`/`max_time`) | **Chosen** |

### 3.3 DDL

```sql
CREATE TABLE flowseer.alarm_events
(
    tenant_id        LowCardinality(String),
    device_id        UUID,
    resource_kind    Enum8('device' = 1, 'component' = 2, 'interface' = 3, 'other' = 4),
    resource_key     String                 CODEC(ZSTD(1)),   -- '' for device; component local key; interface name; other
    type_id          LowCardinality(String),
    type_qualifier   String                 CODEC(ZSTD(1)),   -- '' when absent
    ts               DateTime                CODEC(Delta, ZSTD(1)),   -- Provenance.observed_at
    record_id        UUID,
    transition       Enum8('raise' = 1, 'clear' = 2, 'severity_change' = 3, 'text_change' = 4, 'delisted' = 5),
    -- instance identity: after.last_raised_at (raise) or before.last_raised_at (clear/change); ts when the device reports none
    raised_at        DateTime                CODEC(Delta, ZSTD(1)),
    has_before       Bool,
    before_severity  Enum8('none' = 0, 'indeterminate' = 1, 'warning' = 2, 'minor' = 3, 'major' = 4, 'critical' = 5),
    before_cleared   Bool,
    before_text      String                 CODEC(ZSTD(1)),
    before_last_changed_at DateTime         CODEC(Delta, ZSTD(1)),
    has_after        Bool,
    after_severity   Enum8('none' = 0, 'indeterminate' = 1, 'warning' = 2, 'minor' = 3, 'major' = 4, 'critical' = 5),
    after_cleared    Bool,
    after_text       String                 CODEC(ZSTD(1)),
    after_created_at DateTime               CODEC(Delta, ZSTD(1)),
    after_last_raised_at DateTime           CODEC(Delta, ZSTD(1)),
    after_last_changed_at DateTime          CODEC(Delta, ZSTD(1)),
    source_protocol  LowCardinality(String),                  -- Provenance.protocol arm value
    source_record_id UUID,                                    -- notification/syslog row that produced it, 0 if poll
    binding_id       UUID,
    site_id          LowCardinality(String),
    retention_class  Enum8('short' = 1, 'standard' = 2, 'long' = 3),
    INDEX idx_ts ts TYPE minmax GRANULARITY 4
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY (retention_class, toMonday(ts))
ORDER BY (tenant_id, device_id, resource_kind, resource_key, type_id, type_qualifier, ts, record_id)
TTL ts + INTERVAL 1 YEAR DELETE WHERE retention_class = 'short',
    ts + INTERVAL 2 YEAR DELETE WHERE retention_class = 'standard',
    ts + INTERVAL 3 YEAR DELETE WHERE retention_class = 'long'
SETTINGS ttl_only_drop_parts = 1;

CREATE TABLE flowseer.alarm_intervals
(
    tenant_id        LowCardinality(String),
    device_id        UUID,
    resource_kind    Enum8('device' = 1, 'component' = 2, 'interface' = 3, 'other' = 4),
    resource_key     String,
    type_id          LowCardinality(String),
    type_qualifier   String,
    raised_at        DateTime,
    -- open interval carries the sentinel max DateTime; min() picks the real clear when it arrives
    cleared_at       SimpleAggregateFunction(min, DateTime),
    cleared_by       SimpleAggregateFunction(max, Enum8('open' = 0, 'clear' = 1, 'delisted' = 2)),
    max_severity     SimpleAggregateFunction(max, UInt8),
    last_severity    AggregateFunction(argMax, UInt8, DateTime),
    raise_text       SimpleAggregateFunction(any, String),
    transitions      SimpleAggregateFunction(sum, UInt32),
    site_id          SimpleAggregateFunction(any, LowCardinality(String)),
    INDEX idx_cleared cleared_at TYPE minmax GRANULARITY 1
)
ENGINE = ReplicatedAggregatingMergeTree
PARTITION BY toYYYYMM(raised_at)
ORDER BY (tenant_id, device_id, resource_kind, resource_key, type_id, type_qualifier, raised_at)
TTL raised_at + INTERVAL 3 YEAR
SETTINGS ttl_only_drop_parts = 1;

CREATE MATERIALIZED VIEW flowseer.alarm_intervals_mv TO flowseer.alarm_intervals AS
SELECT tenant_id, device_id, resource_kind, resource_key, type_id, type_qualifier, raised_at,
       if(transition IN ('clear', 'delisted'), ts, toDateTime('2106-02-07 06:28:15')) AS cleared_at,
       multiIf(transition = 'clear', 'clear', transition = 'delisted', 'delisted', 'open') AS cleared_by,
       toUInt8(greatest(before_severity, after_severity)) AS max_severity,
       argMaxState(toUInt8(if(has_after, after_severity, before_severity)), ts) AS last_severity,
       if(transition = 'raise', after_text, '') AS raise_text,
       toUInt32(1) AS transitions,
       site_id
FROM flowseer.alarm_events;
```

**E25. Reasoning.** The events key leads with the alarm identity after the device, so
"history of this alarm" and "all transitions of this device" are both single ranges.
`raised_at` is the instance key (ALARM-MIB and RFC 8632 both time-key instances): the
producer must stamp it on every transition from the alarm's `last_raised_at`
(`alarm.proto:104`), which the before side of a clear carries. When a device reports no
timestamps, the producer (the state projector, which holds the current `AlarmState` in
KV) stamps the raise's `observed_at`; the sink cannot do that alone from a clear event
whose `before` lacks `last_raised_at`. This is the one contract the plan has to state.
`cleared_at` as `SimpleAggregateFunction(min, DateTime)` with the sentinel max value for
open: `min` is idempotent (a redelivered clear gives the same minimum; a redelivered raise
re-adds the sentinel, which `min` ignores). A re-raise is a new `raised_at`, hence a new
row. `last_severity` needs `AggregateFunction(argMax, ...)` because `argMax` is not in the
SimpleAggregateFunction list (F33). Partition monthly on `raised_at`: 36 partitions.

### 3.4 Queries and cost

```sql
-- alarms active at T for site S (D devices)
SELECT device_id, resource_kind, resource_key, type_id, type_qualifier, raised_at,
       max_severity, argMaxMerge(last_severity) AS severity, raise_text
FROM flowseer.alarm_intervals
WHERE tenant_id = {t:String} AND device_id IN {devices:Array(UUID)}
  AND raised_at <= {T:DateTime} AND cleared_at > {T:DateTime}
GROUP BY tenant_id, device_id, resource_kind, resource_key, type_id, type_qualifier, raised_at,
         max_severity, raise_text;
```

Cost: D key ranges; rows per device = alarm instances with `raised_at <= T` within
retention (alarm rate x retention: 5/day x 3 y ≈ 5,500 rows, under one granule), so
granules ≈ D. The `minmax` on `cleared_at` skips granules whose maximum `cleared_at` is
below T, so long-closed history is not read; an open interval carries the sentinel and
keeps its granule readable, which is correct. Independent of total data.

```sql
-- MTTR by severity over raises in [a, b), site S
SELECT max_severity, count() AS n,
       avg(cleared_at - raised_at) AS mttr_s, quantile(0.95)(cleared_at - raised_at) AS p95_s
FROM flowseer.alarm_intervals
WHERE tenant_id = {t:String} AND device_id IN {devices:Array(UUID)}
  AND raised_at >= {a:DateTime} AND raised_at < {b:DateTime} AND cleared_by != 'open'
GROUP BY max_severity;
```

Cost: D ranges, rows = instances raised in [a, b): D x rate x window. A partially merged
row (raise part and clear part not yet merged) shows the sentinel until merge; use `FINAL`
or `GROUP BY` the key with `min(cleared_at)` when exactness matters (F41).

```sql
-- transition log of one alarm, newest first (standing vs transition: this is the transition view)
SELECT ts, transition, before_severity, after_severity, after_text, source_protocol, source_record_id
FROM flowseer.alarm_events
WHERE tenant_id = {t:String} AND device_id = {d:UUID} AND resource_kind = 'interface'
  AND resource_key = {ifname:String} AND type_id = {type:String} AND type_qualifier = ''
ORDER BY ts DESC LIMIT 100;
```

Cost: one range, reverse read with early stop: ≤ 2 granules.

```sql
-- intervals reconstructed from events at read time (verification of the MV, per device)
SELECT resource_kind, resource_key, type_id, type_qualifier, raised_at,
       leadInFrame(ts) OVER (PARTITION BY resource_kind, resource_key, type_id, type_qualifier
                             ORDER BY ts ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) AS next_ts,
       transition
FROM flowseer.alarm_events
WHERE tenant_id = {t:String} AND device_id = {d:UUID} AND ts >= {a:DateTime}
QUALIFY transition = 'raise';
```

`leadInFrame` with the unbounded frame is the documented `lead` equivalent
(`https://clickhouse.com/docs/sql-reference/window-functions/index.md`, row "lag/lead").
Cost: the device's events in the window; fine per device, unbounded at tenant scope
(hence the intervals table).

**E26. Stored intervals vs reconstructed.** Reconstruction answers "active at T" only by
reading every event before T for every alarm in scope, which grows with retention and
cannot be cut by any index (an active alarm's raise may be arbitrarily old). Stored
intervals read one row per instance, and the `minmax` on `cleared_at` removes closed
history. The MV costs one extra narrow row per transition. Both are kept: events are the
truth, intervals the bounded index.

### 3.5 Dedup, failure modes

Events: `record_id` last in key (E13). Intervals: idempotent by construction (E25). The
MV sees a redelivered event again only past the stream's and ClickHouse's dedup windows
(F44, F48); `transitions` (a sum) then over-counts by one, which is a diagnostic column,
not a fact. ALARM-MIB throttling drops traps ("dropped, not queued", E22), so a trap-only
source misses clears; the poll backstop (`GOALS.md:62-63` pattern) produces the clear
from `AlarmState` and the row carries `source_protocol` so the gap is visible.

---

## 4. Generic state transitions for other domains

**E27. Which messages exist.** Nineteen `<Entity>Event` messages exist under
`spec/proto/flowseer/model/` (grep `^message .*Event {`): `DeviceEvent` (from/to
lifecycle, `device.proto:220`), `ComponentEvent`, `LinkEvent`, `BindingEvent`,
`WlanEvent`, `EndpointEvent`, and others; none under `net/interface/v1` (no
`InterfaceEvent`; the interface oper status is a State field, `interface.proto:33-40`).
`docs/conventions/protobuf.md:35`: `<Entity>Event` is "What changed — one transition,
carried on the broker and the event envelope". The direction record routes "one CENTRAL
stream per record type" to "history sink" and "state projector"
(`2026-10-02-central-ingestion-pipeline-direction.md:72-83`).

**E28. Per-domain changes tables, with a shared column prefix.** The 02 §12.2
`interface_changes` shape (attribute, old, new, typed copy of the hot attribute) is the
right one, and prior art says typed-per-domain beats generic (03 implications: generic
designs "paid for it (map promotion mutations, full scans, ALTER per tag ...)").
Cross-domain questions are served without a shared table:

- "What happened on device D between T1 and T2" = `UNION ALL` of k changes tables, each
  one key range: k x ceil(r/G) granules, k ≈ 10.
- "All transitions in site S last 24 h" = k x D ranges, bounded by k x D granules; the
  common prefix `(tenant_id, device_id, ts, record_id, source, site_id, retention_class)`
  lets one Go query builder emit the union.
- A shared `timeline` table would duplicate every transition, need its own dedup, and
  still carry a `Map` or `String` for the typed payload: the generic anti-pattern.

Decision rule: a domain gets a `*_changes` table when it has an `<Entity>Event` message
or a State field whose transitions users query as events (oper status, channel, site
placement). Set-valued domains stay presence intervals (03), not changes.

**E29. Link up/down from traps and from polls.** The trap (`linkDown` with
`ifIndex, ifAdminStatus, ifOperStatus`, E22) and the next poll both observe the same
state. Two producers would write two rows minutes apart with different `record_id`s, and
no key-based dedup can pair them. The design that avoids the problem: one producer. The
state projector holds the current `InterfaceState` (NATS KV) and emits a transition when
an observation differs from it; the trap-mapped observation arrives first and produces
the transition, the poll confirms the same state and produces nothing. The row carries
`source Enum8('poll' = 1, 'trap' = 2, 'syslog' = 3, 'integration' = 4)` and
`source_record_id` (the `snmp_notifications` row, whose `mapped_record_id` points back:
E23). A flapping port therefore yields one `changes` row per actual transition and N
notification rows, which is the honest count of both.

Fallback if two producers must coexist for a while: read with
`LIMIT 1 BY (device_id, ifindex, attribute, new_value, toStartOfInterval(ts, INTERVAL 5 MINUTE))`
over the key range (the poll interval is the ambiguity window); state it in the plan as
temporary.

**E30. Sample tables already hold the status per poll** (02 §12.1 holds counters; an
oper-status gauge column belongs there too). "What was it at T" for a status is then
`argMax(new_value, ts) FROM changes WHERE ts <= T` with the samples table as the baseline
when no change row exists before T (02 §12.2 reasoning), both single ranges.

---

## 5. Answers to the key questions

| Question | Answer | Evidence |
|---|---|---|
| Log key: `(tenant, device, ts)` vs time-bucket-led | Device-led. Bucket-led costs one range per bucket per device for every per-device read (E15); FlowSeer has no "everything in the last 5 minutes" query. Scope queries get the `syslog_by_scope` index (E17). | order-by page, F51, 03 ClickStack rationale, E14-E17 |
| Text index config | `splitByNonAlpha`, `preprocessor = lower(message)`, no timestamp stripping (envelope already parsed), `GRANULARITY 64`, `hasAllTokens`/`hasAnyTokens` at query time; `asciiCJK` as the benchmark variant for IP/MAC tokens | E11, textidx page lines 296-333, hasToken note |
| Mnemonic as a column | Yes: `vendor_tag LowCardinality(String)` from new `SyslogRecord` vendor fields (parser has them, `record.go:125-139`), regexp-derived until then; bloom skip index | E3, E9 |
| Structured data storage | Parallel arrays (Nested-style), `array`-tokenizer text index on an ALIAS of `sd/name=value`; Map loses repeated params; JSON is for open structure | E8 |
| Non-UTF-8 | `String` stores bytes as-is; byte-level tokenizer; `toValidUTF8` at the API; `message_is_utf8` materialized bit | E10 |
| Storm 10k msg/s | Edge admission drop (counted) → sink per-device budget with `repeat_count` rows → ClickHouse sees more rows per insert, not more parts; other devices'/tenants' granules untouched | E19 |
| Per-tenant retention classes | `retention_class` in the partition key with `DELETE WHERE` rules and `ttl_only_drop_parts`; 3 classes x 180 days = 540 partitions | E6, F36, Snuba pattern |
| "errors in site S last 24 h newest first" over 2,000 devices | Naive: D x G rows (16 M). Bounded: `syslog_by_scope` reverse read with early stop (≈ LIMIT / error share / G granules) + 100 PK point reads | E16, E17 |
| Full-text search, tenant, 7 days | PK prunes to the tenant window; text index lookups per part; granules read = matching granules; bounded by parts in window and `max_rows_to_read` | E18 |
| Alarm intervals: raise/clear rows vs stored intervals | Both: events as truth, `AggregatingMergeTree` intervals via MV with idempotent `min(cleared_at)`; "active at T" reads one row per instance, `minmax` on `cleared_at` skips closed history | E24-E26 |
| Dedup: `record_id` in key + Replacing vs plain + `LIMIT 1 BY` | Replacing with `record_id` last; list reads accept transient duplicates; counts use `LIMIT 1 BY record_id` or `FINAL` on the PK range; plain MergeTree keeps duplicates forever and only buys projections, which the lightweight-DELETE rule forbids anyway | E13 |
| Transitions: per-domain or shared | Per-domain `*_changes` with a shared column prefix; cross-domain by `UNION ALL`, k x D bounded; one producer (state projector) so trap and poll yield one row | E28, E29 |

---

## 6. Benchmark design

Runs on a single-node ClickHouse 26.8 container. Generator in Go (the sink's own batch
code path, clickhouse-go v2), seeded.

### 6.1 Generator parameters

| Parameter | Default | Notes |
|---|---|---|
| `seed` | 1 | all randomness from one PRNG |
| `tenants` | 200 | sizes Zipf: 1 tenant with 10,000 devices, 4 with 1,000, 195 with 5-50 |
| `devices_total` | 20,000 | per tenant as above; each device: vendor family (Cisco 60 %, Huawei 10 %, Ruckus 10 %, Aruba 10 %, RFC 5424 generic 10 %), site (≈ 60 sites in the big tenant, 20-700 devices each, as the baseline's site sizes `baseline.md:77`) |
| `syslog_rate` | 0.02/s per device, lognormal across devices (σ = 1.5) | severity mix: debug 10 %, info 70 %, notice 12 %, warning 5 %, err 2.5 %, crit 0.4 %, alert 0.08 %, emerg 0.02 % |
| `syslog_vocab` | per family: 300 templates with 1-3 variable slots (interface names, IPs, MACs, counters) | Cisco templates start with `%MOD-SEV-MNEMONIC: `; 3 % carry RFC 5424 SD with 1-4 params; 0.5 % contain Latin-1 bytes; 0.1 % exceed 4 KiB |
| `storm` | one device per 1,000 enters a storm per day: 10,000 msg/s for 60 s, 95 % identical template | exercises E19 |
| `trap_rate` | 20/day per device, 40 trap OIDs per family, 3-8 varbinds | 1 % unmapped; a flap storm (10/s for 10 min) on one device per 2,000 per day |
| `alarm_rate` | 5 transitions/day per device; 30 type_ids per family; interval lengths lognormal median 20 min; 10 % severity changes; 3 % never clear | re-raise after clear for 20 % |
| `days` | 30 | partitions and TTL classes populated with class mix 60/30/10 |
| `redelivery` | 0.5 % of rows duplicated 15-120 minutes later, in a different batch | exercises E13, E25 |
| `batch` | 10,000 rows or 1 s per table | matches F14 |

### 6.2 Scale steps and expected results

| Step | Data | Query scope | Expected |
|---|---|---|---|
| S1 | 1x (20,000 devices, 30 days) | fixed: 1 device, 1 site of 200 devices, big tenant | baseline numbers |
| S2 | 4x rows (devices x 4 in new tenants) | same scope | per-query `read_rows` within 1.2x of S1 (flat) |
| S3 | 16x rows | same scope | flat; E18 index time grows ≤ parts count growth |
| S4 | 1x data, scope D = 10, 100, 1,000, 2,000 devices | E16 naive scope query | `read_rows` ≈ D x 8,192 (linear in D) |
| S5 | 1x data, same D ladder | E17 two-step query | `read_rows` flat in D (≤ 200 granules total) |
| S6 | 1x, window 1 d, 7 d, 30 d, per device | E14 | flat (LIMIT bound) |
| S7 | 1x, storm day vs quiet day | all queries on a non-storming device in the same tenant | identical `read_rows` |

### 6.3 Metrics

- Insert: rows/s per table, `system.part_log` `NewPart` events per insert and merge
  duration (F67), active parts per partition from `system.parts`.
- Storage: `system.columns` compressed/uncompressed per column; `system.parts`
  `secondary_indices_compressed_bytes` for the text index; bytes/row per table and per
  class.
- Queries: `system.query_log` `read_rows`, `read_bytes`, `memory_usage`,
  `query_duration_ms` p50/p95 over 20 runs with cold and warm caches
  (`https://clickhouse.com/docs/operations/system-tables/query_log.md`); granules from
  `EXPLAIN indexes = 1` run with `SETTINGS use_query_condition_cache = 0,
  use_skip_indexes_on_data_read = 0` ("this statement only shows reasonable output when
  used with" those settings, `https://clickhouse.com/docs/sql-reference/statements/explain.md`);
  index skip ratio per index (`Granules` after/before, same page).
- Text index variants: `splitByNonAlpha` vs `asciiCJK`, `GRANULARITY 16` vs `64`: index
  bytes/row, IP-search and word-search `read_rows`.
- Dedup: duplicate share visible in raw reads at 1 min, 1 h, 24 h after redelivery;
  `LIMIT 1 BY` and `FINAL` overhead on E14 and the count query.

### 6.4 Pass criteria

| Query | Cost model | Pass |
|---|---|---|
| E14 per device newest 200 | ≤ 3 granules | `read_rows` ≤ 3 x 8,192 at every scale step |
| E14 with `severity <= err` | ≤ 200 / 0.03 / 8,192 + 1 ≈ 2 granules, ≤ device granules in window | within 2x |
| E16 naive scope | ≈ D x 8,192 | within 2x, linear in D, flat across S1-S3 |
| E17 two-step scope | ≤ (100 / 0.03 / 8,192 + 1) + 100 granules | `read_rows` ≤ 1 M, flat in D and across S1-S3 |
| E18 text search | matching granules + P index lookups | `read_rows` ≤ 4 x matches rounded to granules; duration grows ≤ 1.5x from S1 to S3 |
| Trap counts per device | ≈ D granules | within 2x |
| Alarms active at T | ≈ D granules | within 2x; flat across steps |
| MTTR | D x instances in [a, b) | within 2x |
| Inserts | one part per (class, day) touched per insert | parts per insert ≤ 6; active parts per partition < 300 after 10 min |
| Storm | non-storming device's queries | `read_rows` identical to quiet day |
| Dedup | after merge | zero visible duplicates in closed partitions; `LIMIT 1 BY` overhead ≤ 20 % on E14 |

---

## 7. Schema gaps

What the history needs that the FlowSeer protobuf model does not carry today. Each row
names the missing field, message, or carrier, the proposed addition, and the evidence.
The DDL above already includes the columns these additions feed.

### 7.1 Syslog (`flowseer.event.log.v1.SyslogRecord`)

| Gap | Proposed addition | Evidence |
|---|---|---|
| G1. Vendor-parsed fields are extracted at the edge and dropped | Add `message SyslogVendorFields { string family; string module; string mnemonic; string event_id; int32 severity (presence); uint64 sequence; uint64 counter; string thread; string source_location; string slot; repeated string flags; }` as `SyslogRecord.vendor = 15` | Parser already yields `Vendor{Family, Module, Mnemonic, EventID, Severity, Sequence, Counter, Thread, Source, Slot, Flags}` (`src/protocol/syslog/record.go:125-139`); the mapper copies none of it (`mapper.go:95-160`). The tag is the main non-severity filter (E3, E9); Logstash `CISCOTAG` grammar; corpus families README lines 48-61 |
| G2. Envelope format and clock quality are lost | `enum LogFormat { RFC5424, RFC3164 }` as `SyslogRecord.format`; `enum DeviceClock { ABSOLUTE, NO_YEAR, UPTIME, NONE }` as `sent_at_quality`; optional `sent_at_original string` (the device's own text) | `Record.Format` (`record.go:38-41`); README: "A device clock without a year or timezone has no invented absolute instant", `DeviceTime.Original` kept (`README.md:16-17,66-71`); RFC 5424 §7.1 `timeQuality` (`tzKnown`, `isSynced`, `syncAccuracy`) exists for exactly this and should map to the same field when present (`rfc5424.txt:1111-1193`) |
| G3. Parse outcome is binary (raw attached or not) | `enum ParseStatus { OK, PARTIAL, FAILED }` plus `repeated string diagnostics` (finite codes) on `SyslogRecord` | Parser diagnostics "have finite codes and bounded counts" (`README.md:36-37`, `record.go:141-146`); mapper collapses them into `isParseFailure` (`mapper.go:99-137`); the "unparsed messages per device" query needs the code, not the raw bytes |
| G4. Transport and authentication of the observation | `enum LogTransport { UDP, TCP, TLS }` and `bool transport_authenticated` on `SyslogRecord` (or on `Provenance` as a log-transport sub-message) | "`Observation.Authenticated` means a receiver verified a client certificate. Plain TCP and UDP never assert authentication" (`README.md:86-87`); the 2026-10-03 amendment says a UDP source "is spoofable" (`2026-10-02-central-ingestion-pipeline-direction.md:231-233`), so trust level is a per-row fact |
| G5. Standard SD-IDs have no typed home | Keep them in `structured_data`; the sink promotes `origin` (`ip`, `enterpriseId`, `software`, `swVersion`) and `meta` (`sequenceId`, `sysUpTime`) into columns `origin_enterprise_id`, `origin_software`, `origin_sw_version`, `meta_sequence_id`, `meta_sys_uptime` when present | RFC 5424 §7.2, §7.3 (`rfc5424.txt:1225-1242`, index lines 129-135); Ruckus ICX emits `[meta@1991 slot="1"]` (corpus line 289) |
| G6. Storm suppression has no carrier | `uint32 repeat_count` on `SyslogRecord` (or on `IngestRecord`, since the sink may also set it) | E19; the pattern already exists for raw evidence as `RawEvidence.suppressed_since_last` (`ingest_record.proto:46-48`) |
| G7. `message_truncated` has no original length | `uint32 original_length` next to `message_truncated` | `mapper.go:135-138` cuts at 65527 and loses the length; RFC 5424 §6.1 lets receivers "receive messages larger than 2048 octets" (`rfc5424.txt:470-476`), so the cut is a FlowSeer policy whose size should be recorded |

### 7.2 SNMP notifications (no record type exists)

| Gap | Proposed addition | Evidence |
|---|---|---|
| G8. No `IngestRecord.payload` arm for a trap or inform | `flowseer.event.snmp.v1.NotificationRecord { DeviceGlobalRef device; Timestamp received_at; IpAddress source_address; SnmpVersion version; NotificationPdu pdu (TRAP, INFORM); uint32 sys_uptime; string trap_oid; string trap_name; string enterprise_oid; bytes engine_id; string user_name; repeated VarBind varbinds; MappingResult mapping; }` under `event/`; new `IngestRecord.snmp_notification = 11` | `ingest_record.proto:28-32` has only `syslog`; `src/protocol/snmp/trap.go:22-41` holds every header field; RFC 3416 §4.2.6/4.2.7 fix the leading varbinds; `event/README.md:10-16` admission rule ("standalone, durable stream records") fits |
| G9. `VarBind` has no protobuf form | `message VarBind { string oid; string name; VarBindKind kind; oneof value { int64 int; uint64 uint; bytes octets; string oid_value; bytes bits; uint32 ip_v4; bytes ip_v6; double real; } ; string rendered; }` with `VarBindKind` mirroring `kind.go:12-57` (15 value kinds, 3 exceptions) | `varbind.go:15-30` sealed sum type; a trap's meaning lives in its varbinds (`IF-MIB:1106-1107`: `ifIndex, ifAdminStatus, ifOperStatus`) |
| G10. Trap vs inform is not on `snmp.Trap` | Add `PDU` (trap/inform) and `RequestID` to `snmp.Trap`, then to G8 | `trap.go:17-19` says the stream carries "INFORM/TRAP" but the struct has no discriminator; `usm_inform.go` exists; an inform is acknowledged and so is a stronger delivery claim |
| G11. Mapping result has no carrier | `message MappingResult { MappingStatus status (MAPPED, UNKNOWN_TRAP, UNKNOWN_VARBIND, REJECTED, IGNORED); string domain; string mapped_record_id; string entity_key; }` on the notification record, filled by the component that maps trap to transition | `GOALS.md:59-66`: the trap triggers the drift read but "says that a change happened and not what changed"; the audit "which traps did not map" needs the status (E23) |
| G12. Trap loss has no recovery carrier | A poll record for `NOTIFICATION-LOG-MIB::nlmLogTable` (and Huawei `hwAlarmSyncTable`) that produces `NotificationRecord`s with `source = REPLAYED` | Atlas: "SNMP traps are UDP and lossy, and this MIB exists precisely so a manager can poll for the traps it missed" (`docs/research/network-domain-atlas/entities/09-ops.md:101-106`); ALARM-MIB throttling drops traps (`spec/mib/ietf/ALARM-MIB:1035-1037`) |
| G13. Provenance has no SNMP-notification protocol arm | `Provenance.protocol` gains `NotificationProtocol notification = 12 { SNMP_TRAP, SNMP_INFORM, WEBHOOK }` or `ManagementProtocol` is reused with a `direction` flag | `provenance.proto:35-50` has `management` and `log` arms only; a trap is neither a management response nor a log line |

### 7.3 Alarms (`flowseer.model.alarm.v1`)

| Gap | Proposed addition | Evidence |
|---|---|---|
| G14. No instance identity across raise and clear | `Timestamp raised_at` on `AlarmEvent` (required), stamped by the producer from `last_raised_at` or the raise's `observed_at` | E25; ALARM-MIB keys active alarms by `alarmActiveDateAndTime` (`rfc3877.txt:1241`); RFC 8632 lifecycle "raise ... clear, being raised again" (`rfc8632.txt:517-519`) makes (ref, raised_at) the instance key |
| G15. Transition kind is implicit in (before, after) | `enum AlarmTransition { RAISE, CLEAR, SEVERITY_CHANGE, TEXT_CHANGE, DELISTED }` on `AlarmEvent`, derived once by the producer | E24 table; RFC 8632 `status-change* [time]` list with `perceived-severity` (severity-with-clear) (`rfc8632.txt:540-546`) |
| G16. No probable cause or vendor code | `string probable_cause` (X.733 vocabulary) and `string vendor_code` on `AlarmState` | Atlas: `ITU-ALARM-TC-MIB`/`IANA-ITU-ALARM-TC-MIB` "the X.733 severity and probable-cause vocabularies" (`09-ops.md:116`); Ruckus SCI `AlarmMessage.alarmCode`, `alarmType`, `mainCategory`, `subCategory`, `initEventCode` (`spec/proto/ruckus/sci/sci-alarm.proto:57-85,197`) |
| G17. No impacted or related resources | `repeated AlarmResource impacted_resources`, `repeated AlarmLocalRef related_alarms` on `AlarmState` | RFC 8632: "impacted-resource" leaf-list, "root-cause-resource", "related-alarm" list (`rfc8632.txt:654-686`) |
| G18. No operator state (ack, shelve, close) | A separate operator-owned record `AlarmOperatorEvent { AlarmGlobalRef ref; Timestamp raised_at; OperatorState state (NONE, ACK, SHELVED, CLOSED); string text; operator ref }` under `event/operator/v1` (not on `AlarmState`, which stays device-reported) | `alarm.proto:5-6`: "operator acknowledgment is not modeled here"; RFC 8632 §3.5.2 operator state "none, ack, shelved, closed" (`rfc8632.txt:583`); the ack-to-clear and raise-to-ack durations are the two other MTTR-family metrics |
| G19. Vendor alarm identity is lost on `other` resources | `string vendor_alarm_id` on `AlarmLocalRef` (optional) | Ruckus SCI `alarmUuid` (`sci-alarm.proto:50`); Huawei `hwAlarmActiveTable[targetAddrExtIndex, activeAlarmIndex]` (`09-ops.md:118`) |
| G20. Severity has no per-vendor original | `string severity_original` on `AlarmState` next to the normalised enum | Ruckus SCI `alarmSeverity` is a free string (`sci-alarm.proto:64`); OpenConfig `UNKNOWN` maps to `INDETERMINATE` with information loss (`docs/research/schema-building-blocks/04-platform-system.md:180-187`) |

### 7.4 Transitions and carriers

| Gap | Proposed addition | Evidence |
|---|---|---|
| G21. No `InterfaceEvent` (oper/admin status, speed, alias transitions) | `InterfaceEvent { InterfaceGlobalRef ref; InterfaceState before; InterfaceState after; }` or a narrow `{ attribute, old, new }` form, in `net/interface/v1` | No `Event` message under `spec/proto/flowseer/net/interface/v1/` (grep); `linkDown` is the most common trap (`IF-MIB:1106`); 02 §12.2 designed `interface_changes` for it |
| G22. Transition source and causal link | Common fields on every `<Entity>Event`: `TransitionSource source (POLL, TRAP, SYSLOG, INTEGRATION, REPLAYED)` and `string source_record_id` | E29: one producer, provenance back-link to the `snmp_notifications` or `syslog` row; the mapping result (G11) is the forward link |
| G23. Controller event streams have no carrier | `flowseer.event.integration.v1.IntegrationEventRecord { DeviceGlobalRef device (optional); Timestamp at; string vendor; string event_code; string event_type; string category; string severity_original; AlarmSeverity severity; string description; string reason; repeated KeyValue attributes; }` and an `IngestRecord` arm | Ruckus SCI `EventMessage` fields `eventCode, eventType, mainCategory, subCategory, severity, reason, description, disconnectReason, attributes` (`spec/proto/ruckus/sci/sci-event.proto:38-248`); `GOALS.md:59-60` names "an integration's change event" as a drift trigger; the device-inventory survey lists webhook pushes from Meraki, Mist, Aruba Central, UniFi Alarm Manager (`docs/research/device-inventory/README.md:138`) |
| G24. `IngestRecord` has no retention or site stamp | Not a proto change: the sink stamps `retention_class` from the tenant plan and `site_id` from inventory (F69); recorded here so nobody adds them to the record | E6, F36, F69; tenancy stays ambient (`2026-10-02-central-ingestion-pipeline-direction.md:53-55`) |

---

## 8. Unverified and open

1. Juniper and Aruba syslog message shapes: vendor pages were blocked (CDN "Access
   Denied") or 404; the shapes rest on the repository corpus and the Logstash Cisco
   pattern. Junos `junos@2636` SD-ID and `process[pid]: TAG:` layout are from memory.
2. Syslog volume per device: no baseline number; 0.02 msg/s is an assumption.
3. Bytes/row estimates (E12) are to be measured, not sourced.
4. Whether read-in-order early stop applies when `device_id IN (set)` precedes
   `ORDER BY ts DESC` (E16 assumes it does not).
5. Text index on an `ALIAS Array(String)` column: ClickStack's DDL does it (03), the
   ClickHouse page does not say so explicitly.
6. The `pdu` (trap vs inform) distinction is not on `snmp.Trap` today
   (`trap.go:22-41`); the field has to be added with the record type.
7. `SimpleAggregateFunction(max, Enum8)`: `max` is listed, Enum operands are not
   discussed; the DDL uses `UInt8` for severity in the intervals table to be safe.
8. The edge syslog admission limit under a storm is a default (256 frames), not a
   policy; the sink budget in E19 is a proposal.
9. `vendor_tag` regexp covers Cisco, Huawei, ASA shapes; Aruba AOS-S numeric event ids
   and AOS-CX pipe fields need the parser's fields, not a regexp.
10. Whether `minmax` on `cleared_at` with a sentinel open value is used by the planner
    for `cleared_at > T` (expected yes; verify with `EXPLAIN indexes = 1`).
