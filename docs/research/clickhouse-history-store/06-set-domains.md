---
title: Set-valued domains: FDB, LLDP and CDP neighbors, ARP and ND
date: 2026-10-08
status: research; sources fetched 2026-10-08, ClickHouse 26.8 LTS
---

# 06. Set-valued domains: FDB, LLDP/CDP neighbors, ARP/ND neighbor cache

Fetched 2026-10-08. Builds on 01 (section 3), 02 (F33, F38 to F48, 12.3) and 03
(set-valued domains, unverified 30). Findings are numbered S1 to S25, schema gaps G1 to G13. Repository
paths are relative to the worktree. Statements marked "inference" are reasoning
from the cited evidence, not something a source states for FlowSeer.

## 0. Result in one paragraph

Store each set as **presence per member per day bucket in an AggregatingMergeTree
whose sorting key carries the attributes a user asks history about** (for FDB:
VLAN, MAC, interface), with `min(first_seen)` and `max(last_seen)` as
`SimpleAggregateFunction` columns, plus a **poll marker table** (one row per
successful full walk) and a **transition table** the sink derives from the
current state it already keeps in JetStream KV (appear, vanish, move). That is
model (e), hybrid of (a) and (b). It is idempotent under redelivery and
out-of-order delivery by construction (S20), separates "device unreachable"
from "member gone" (S22), answers "set at T" by one primary-key range whose cost
is the member count of one device on one day (section 6, Q1), and answers "where was MAC X
in the tenant" through a second table ordered by member (5.2) at a cost of
days times devices that saw the MAC. On the bus, the edge ships **delta events plus one
small marker per walk** (section 9, S25), so ingestion volume follows change
as the device service record requires, and the central sink, which holds the
current set in KV, writes the presence rows itself. Full snapshots per poll
(c) are ruled out by volume at core scale (S14) and by that record. Pure intervals closed by the sink (d) are what
NAV does in Postgres and give an unbounded "set at T" scan (S8, S16). A change
log with snapshot markers (b) depends on the sink's state being exactly the
previous walk, so it is kept only as the derived transition table, never as
the system of record (S15).

## 1. Data semantics

### 1.1 What the messages carry

| Domain | Message (path:line) | Natural key per device | Attributes | Volatile parts |
| --- | --- | --- | --- | --- |
| FDB | `spec/proto/flowseer/net/switching/v1/fdb_entry.proto:22-47` | `network_instance` (44), `vlan_id` (30), `mac` EUI-48 (35) | `interface_name` (38), `kind` (40), `status` (42) | interface changes on a move; `kind`, `status` rarely |
| LLDP neighbor | `spec/proto/flowseer/net/protocol/lldp/v1/neighbor.proto:18-68` | `local_interface_name` (24), `chassis_id` (subtype+bytes, 30), `port_id` (subtype+bytes, 33) | `port_description`, `system_name`, `system_description`, `capabilities_*`, `management_addresses`, `time_to_live`, `ieee8023`, `med` | `time_to_live` every announcement; `system_description` on firmware upgrade |
| CDP neighbor | `spec/proto/flowseer/net/protocol/cdp/v1/neighbor.proto:19-103` | `local_interface_name` (22), `device_id` (29), `port_id` (36); the file comment names `(local_interface_name, device_id)` (14-15) | platform, software_version (up to 1,024 bytes, 43), capabilities, VLANs, duplex, addresses, power, MTU, sysname, OID, location | `time_to_live` (92) every announcement |
| ARP / IPv6 ND | `spec/proto/flowseer/net/ip/v1/neighbor_entry.proto:15-47` | `interface_name` (24), `ip` (30) | `mac` (37, absent while incomplete), `origin` (40), `is_router` (44), `reachability` (46) | `reachability` flips through the RFC 4861 NUD states `neighbor_reachability.proto:13-30` |
| RIB (non-goal) | `spec/proto/flowseer/net/routing/v1/route.proto` | `network_instance`, `destination_prefix` | protocol, preference, metric, next hops, active | metric and next hop on reconvergence |

The direction record keeps these tables device-scoped and keyed by interface
name (`docs/architecture/2026-09-25-schema-building-blocks-direction.md:193-216`,
`CONCEPTS.md:181-185`), and `NeighborEntry` inherits its network instance from
its interface (direction record line 193 to 195), so the ARP key needs no
instance column.

**S1. No ingest payload exists for any of these domains.**
`spec/proto/flowseer/integration/ingest/v1/ingest_record.proto:25-30` has one
`oneof payload` arm, `syslog`. `FdbEntry`, `NeighborEntry`, and both `Neighbor`
messages are referenced by no other `.proto` under `spec/proto/flowseer/`
(grep over the tree). The record types this report assumes are the delta and marker
records of section 9.2 (`appear`, `vanish`, `change` carrying one `FdbEntry`
or `Neighbor` or `NeighborEntry`, and a per-walk `walk` marker with
`complete`, `members`, `set_hash`), carried under `IngestRecord` with
`Provenance.observed_at` as the walk time
(`spec/proto/flowseer/model/inventory/v1/provenance.proto:12`). A full-walk
record (`repeated FdbEntry entries`, `complete bool`) exists only as the
benchmark and bootstrap fallback (9.5). The `complete` flag is load-bearing
(S22).

### 1.2 How the sets behave on real devices

**S2. FDB entries age out after 300 s of silence by default.** Cisco IOS
`mac address-table aging-time` "defaults to 300 seconds. Valid times are 0 or
from 10 to 1,000,000 seconds" (O'Reilly's Cisco IOS in a Nutshell reference,
`https://www.oreilly.com/library/view/cisco-ios-in/0596008694/re550.html`; the
Cisco command reference page itself returned 404 on 2026-10-08, so the primary
source is unverified; the Nexus claim that entries live "up to twice the
configured" timeout is from a search summary and unverified). With a 5-minute
poll (brief) and a 300 s age, a host idle for one poll vanishes from the walk
and returns on its next frame. Inference: the FDB is the churniest of the four
sets even when nothing moved, and the model must treat a one-poll gap as
noise unless the user asks for transitions.

**S3. FDB size: 32k to 64k on an access or distribution Catalyst, 256k raw on a
Nexus 9300.** Catalyst 9300 datasheet: MAC address table "32,000" for the
modular-uplink and 9300L models and "64,000" for the higher-scale models
(`https://www.cisco.com/c/en/us/products/collateral/switches/catalyst-9300-series-switches/nb-06-cat9300-ser-data-sheet-cte-en.html`).
Nexus 9300: "MAC address table" of "256,000 entries", "896,000 IPv4 host
entries", footnote "Raw capacity of tables"
(`https://www.cisco.com/c/en/us/products/collateral/switches/nexus-9000-series-switches/datasheet-c78-742282.html`).
The brief's planning figures (access 50 to 2,000, core 20k to 100k) are inside
these ceilings. Every MAC in a broadcast domain appears in the FDB of every
switch on its path, so fleet FDB rows are a multiple of the fleet's unique MACs.

**S4. ARP entries live 4 hours by default on Cisco.** "The default is 14400
seconds (4 hours)"; "We recommend that you set an ARP timeout value greater
than 60 seconds" (`https://www.cisco.com/c/en/us/td/docs/ios-xml/ios/ipaddr_arp/configuration/xe-16/arp-xe-16-book/arp-config-arp.html`).
Catalyst 9300 host capacity: "Total number of IPv4 routes (ARP plus learned
routes)" 32,000 to 112,000 with "24,000 direct" to "48,000 direct" (datasheet
above). Inference: ARP membership is far more stable than FDB membership
(hours, not minutes); the churn is in `reachability`, which cycles REACHABLE,
STALE, DELAY, PROBE as RFC 4861 describes, and that attribute must not become
part of the presence key or it would generate a row per flip.

**S5. LLDP neighbors are the most stable set.** Defaults on Cisco platforms:
holdtime 120 s, transmit timer 30 s, reinit 2 s (Cisco NX-API reference,
`https://developer.cisco.com/docs/cisco-nexus-3000-and-9000-series-nx-api-rest-sdk-user-guide-and-api-reference/latest/configuring-lldp-in-global-mode`:
holdtime range "[1, 255]" default "120"; the IOS figures come from a Cisco
Community thread and ipcisco.com and are unverified against the IEEE 802.1AB
text). Cardinality: one neighbor per uplink, phone, or AP port, so an access
switch holds a handful to about 50 and a distribution switch up to its port
count. The legacy store's lesson is that the volatile fields (TTL, descriptions)
used as labels created "about 25,000 new series every scrape, about 7 M per
day" (`docs/research/2026-10-01-production-monitoring-baseline.md:169-171`), so
`time_to_live` must never be in a key.

**S6. RIB as a set is a non-goal.** A core router's RIB runs to hundreds of
thousands of prefixes that reconverge in bursts; no query shape in the brief
asks "route set at T". Store per-instance, per-protocol route counts as gauges
in the samples domain (the counter rule, direction record), and leave route
rows to the current-state KV. Cost check: at 20k devices with even 1% routers
holding 1M routes, a presence table would carry 200M members, eight times the
whole FDB fleet (S13), for a query nobody listed.

## 2. Prior art on the data model

**S7. NAV (Network Administration Visualized) stores true intervals in
PostgreSQL and closes them with a miss counter.** Schema
(`https://raw.githubusercontent.com/Uninett/nav/master/python/nav/models/sql/baseline/manage.sql`):
`cam` has `netboxid`, `sysname`, `ifindex`, `module`, `port`, `mac MACADDR NOT
NULL`, `start_time TIMESTAMP NOT NULL`, `end_time TIMESTAMP NOT NULL DEFAULT
'infinity'`, `misscnt INT4 DEFAULT '0'`, and "Indexes: none beyond the primary
key". `arp` has `netboxid`, `prefixid`, `sysname`, `ip INET`, `mac MACADDR`,
`start_time`, `end_time DEFAULT 'infinity'`. Rules `netbox_close_cam`,
`netbox_close_arp` close open rows when a device is deleted, and
`netbox_status_close_arp` closes ARP rows "on netbox update when `NEW.up='n'`".
Closing logic (`https://raw.githubusercontent.com/Uninett/nav/master/python/nav/ipdevpoll/shadows/cam.py`):
a record is open when `end_time >= INFINITY or miss_count >= 0`; a missing
record gets `end_time = now()` and `miss_count + 1`; `MAX_MISS_COUNT = 3`, "closed
cam records have a grace period of MAX_MISS_COUNT collector runs"; a record
seen again inside the grace period is reclaimed (`update(end_time=INFINITY,
miss_count=0)`) rather than inserted anew. ARP
(`https://raw.githubusercontent.com/Uninett/nav/master/python/nav/ipdevpoll/plugins/arp.py`):
`expireable_mappings = set(open_mappings).difference(found_mappings)`; "If an
IP's MAC changes, the new pair opens and the old pair closes in the same run";
the key is `(ip, mac)` with ifindex pruned. The cam plugin only records access
ports: "Ports whose MACs include a NAV-monitored device that isn't this switch
are linkports ... only accessports get CAM records"
(`https://raw.githubusercontent.com/Uninett/nav/master/python/nav/ipdevpoll/plugins/cam.py`),
and it adds a sentinel "signifying that a full CAM collection has taken place
and that old CAM records can be safely expired". Topology candidates
(`adjacency_candidate`) carry `misscnt` but no time columns, and
`unrecognized_neighbor` carries `since`.

Lessons: (1) the interval row keyed by `(device, port, mac)` with a grace
period absorbs FDB aging noise (S2); (2) a MAC that changes port or an IP that
changes MAC is a new interval, so attributes that users ask history about
belong in the identity, not in mutable columns; (3) "set at T" in NAV is
`start_time <= T AND end_time >= T` over a table with no index, which scans the
device's whole history (S16); (4) a device going down closes ARP rows but not
CAM rows, which conflates "unreachable" with "gone" for ARP; (5) expiry is
gated on a sentinel that only a complete walk emits.

**S8. Netdisco keeps one row per `(mac, switch, port, vlan)` with
first/recent/last timestamps and an `active` flag, and never deletes.**
`node` columns (`https://raw.githubusercontent.com/netdisco/netdisco/master/lib/App/Netdisco/DB/Result/Node.pm`):
`mac macaddr`, `switch inet`, `port text`, `active boolean`, `oui`, `time_first`,
`time_recent`, `time_last` (all `timestamp`, default `LOCALTIMESTAMP`), `vlan
text default '0'`, primary key `(mac, switch, port, vlan)`. Store logic
(`https://raw.githubusercontent.com/netdisco/netdisco/master/lib/App/Netdisco/Worker/Plugin/Macsuck/Nodes.pm`):
rows for the same MAC on another switch or port are set inactive first ("Will
mark old entries for this data as no longer C<active>"); the upsert sets
`active => true, time_last => $now`, sets `time_recent` only "when the
deactivation step above changed at least one other row", and `time_first` only
on insert; "A MAC that is absent from the current forwarding table is not
touched"; stale rows are deactivated only by the optional `node_freshness`
window; "There is no archive table ... Old locations stay in the table as
inactive rows". `node_ip` (ARP) has `mac, ip, active, time_first, time_last,
dns`, primary key `(mac, ip, vrf)`
(`.../DB/Result/NodeIp.pm`); `store_arp` deactivates "every matching row for
that IP, whatever its MAC", refreshes `time_last`, sets `time_first` on insert
only, and keeps per-router windows `seen_on_router_first/last`
(`.../Util/Node.pm`).

Lessons: (1) Netdisco's row is the per-member `min(first)/max(last)` presence
row without buckets, so it answers "where is MAC X now and where was it ever"
but cannot answer "set at T" for a past T (one row per location, gaps lost);
(2) absence from a walk is deliberately not a signal, which is the safe default
when walks can be partial; (3) `time_recent` records the last move, which is
the transition the hybrid model stores explicitly.

**S9. LibreNMS and Observium keep current state only.** LibreNMS `ports_fdb`
rows carry `port_id`, `mac_address`, `vlan_id`, `device_id`, `created_at`,
`updated_at` (API output and insert error quoted in
`https://community.librenms.org/t/issues-fbd-call-via-api/27883` and
`https://community.librenms.org/t/trying-to-get-fdb-tables-to-work-on-arubaos-7/10395`;
the schema file `misc/db_schema.yaml` returned 404 and the "FDB history" pull
request named in `https://community.librenms.org/t/mac-address-history/3234`
is unverified). Observium's poller log shows "UPDATE `vlans_fdb` set
`fdb_last_change` ='1680085926',`deleted` ='1' WHERE `fdb_id` IN (...)"
(`https://jira.observium.org/browse/OBS-4572`), a soft delete with a last-change
time, no history. Lesson: created/updated and soft-delete stamps give first
seen and last seen of the current location only.

**S10. IP Fabric and Forward Networks store whole snapshots and diff two of
them.** IP Fabric: "3 loaded snapshots" by default, range 1 to 5 (1 to 100 in
7.5), "IP Fabric will automatically unload the oldest loaded snapshot from
memory and save it to the hard disk", retention "only supports the delete
action", runs "every day at 0:00 UTC"
(`https://docs.ipfabric.io/7.3/IP_Fabric_Settings/Discovery_and_Snapshots/snapshot_retention/`
and the 7.5 `snapshot_collection` page). Forward Networks: a snapshot is "a
collection of the network device's running configuration and state table files
at a specific point in time", and Diffs compare two snapshot ids
(`https://community.forwardnetworks.com/servicenow-cmdb-80/change-control-workflow-automation-with-the-api-551`).
Lesson: full snapshots work when a snapshot is a deliberate, infrequent event
(a change window) and few are loaded. They are the wrong unit for a 5-minute
poll at 20k devices (S14).

**S11. No NetBox or Nautobot plugin with MAC or ARP history was found** (two
searches; absence unverified). The nearest products are Infoblox NetMRI ("keeps
a record of every MAC it has ever observed ... IP and switch-port history") and
Cacti mactrack (search summaries, not fetched).

**S12. ClickHouse precedents for presence are the TimeSeries tags table and the
trace summary tables** (03, patterns table: "Interval-presence via
`min(first_seen)`/`max(last_seen)` SimpleAggregateFunction on an
AggregatingMergeTree keyed by entity"). None of them buckets by time; they keep
one row per entity for its whole life. Inference: that is fine for traces
(short-lived) and series (query "exists in window"), and wrong for a device
table where "set at T" must not scan the member's whole life (S16).

## 3. Volume model at 20,000 devices

Planning fleet, two scenarios. A = access-heavy: 19,000 access devices at 300
FDB members and 1,000 distribution or core devices at 20,000. B = core-heavy:
18,000 at 300 and 2,000 at 100,000. Poll interval 300 s (288 polls per day).

**S13. Fleet FDB member rows per walk.** A: 5.7 M + 20 M = 25.7 M. B: 5.4 M +
200 M = 205 M. ARP (gateways only, say 2,000 devices at 10,000): 20 M. LLDP:
20,000 devices at 10 = 0.2 M. CDP: a subset of LLDP's size.

**S14. Full snapshot per poll (model c) is out.** Rows per day = members x
288. A: 7.4 B rows/day, B: 59 B rows/day. Even at 2 bytes per row after sorting
and ZSTD (an optimistic figure, unverified), B writes 118 GB per day for the
FDB alone, and every "set at T" still reads one walk, which is cheap, while
"when did MAC X move" reads 288 rows per day of history per device. The
legacy 8.6 B-row set table 02 section 12.3 cites is the same shape at a
smaller fleet.

**S15. Change log with snapshot markers (model b, 02 F43 and 12.3).** Rows per
day = changes + members / snapshot interval in days. Daily snapshots make it
members x 1 per day plus transitions, the same post-merge order as the presence
table. Its weakness is not ordering: replay sorts by `observed_at`, so arrival
order does not matter, which corrects 03's "needs ... ordered delivery"
(03, set-valued domains). The weakness is the baseline: every add or remove is
a diff against the sink's copy of the previous walk, so a sink restart with
stale KV, a missed record, or a partial walk emits wrong deltas that persist
until the next snapshot (F43 says the same). A presence row needs no baseline.

**S16. True intervals closed by the sink (model d, NAV's shape).** Rows per
day = intervals opened per day, the smallest of all models for stable sets,
and the FDB aging noise (S2) multiplies it. Its query defect is structural:
"set at T" is `start <= T AND end >= T`, and an open or long interval that
started months ago is only found by scanning every interval of the device with
`start <= T`, which grows with the device's retained history. NAV has no index
for it (S7). Capping an interval at a day bound turns (d) into (a).

**S17. Presence per member per bucket (model a) and the hybrid (e).** Post-merge
rows per day = members x (1 + c), where c is the fraction of members that
change a key attribute or re-appear after a day boundary inside the bucket
(moves, VLAN changes). With daily buckets, A: about 26 M to 33 M FDB rows/day,
B: about 205 M to 270 M. Pre-merge insert volume depends on the write strategy
(S18).

**S18. Two write strategies the same table accepts.**

| Strategy | What the sink writes | Rows per member per day | State needed | A rows/s | B rows/s |
| --- | --- | --- | --- | --- | --- |
| W1 stateless | One row per member per walk | 288 | none | 86 k | 683 k |
| W2 on edge | One row at first sight in the bucket, one when the member vanishes or the bucket closes, one on a key-attribute change | about 2 + changes | KV: per member `first_seen_in_bucket`, `last_seen` | 0.6 k | 4.8 k |

Both land in the same AggregatingMergeTree, and a mix of the two (W1 while the
sink has no KV, W2 afterwards) is correct, because `min` and `max` fold either
stream to the same row. W1 at B is 683 k rows/s sustained plus the merge work to
fold 59 B rows into 270 M, which is the volume of model (c) passing through
merges every day. Inference: W1 is acceptable for the benchmark and a bootstrap,
W2 is required once cores are polled, and W2 is what the delta ingestion
shape of section 9 produces without any central diffing, because phase 5 keeps
current state in KV anyway
(`docs/plans/2026-10-02-2331-feat-central-ingestion-pipeline-phase5-plan.md:15-26`).
At restart W2 flushes one row per KV member so a crash loses no `last_seen`.
The rows/s columns are ClickHouse insert rates; what crosses the bus is the
smaller figure of 9.1.

**S19. Storage estimate (to be measured, section 8).** FDB presence row after
merge with the key `(tenant_id, device_id, bucket, vlan_id, mac,
interface_name)`: tenant and device are long runs (about 0.1 B each), `bucket`
is constant per partition day (about 0 B), `vlan_id` T64 (about 0.3 B), `mac`
sorted within a device and day so ZSTD finds shared prefixes (about 3 B),
`interface_name` LowCardinality (about 0.5 B), `first_seen` and `last_seen`
DateTime with Delta against the bucket (about 1.5 B each), two Enum8 (about
0.2 B). About 7 to 8 B per row. A: 0.25 GB/day, 90 GB/year. B: 2 GB/day,
0.75 TB/year. ARP rows are similar with a 16-byte IPv6 column (about 3 B
sorted). LLDP rows carry strings and run 40 to 80 B, at 0.2 M rows/day that is
under 20 MB/day. These figures use F24 and F25 (02) as the codec basis and are
unverified until the benchmark runs.

## 4. Correctness under delivery faults

**S20. Idempotent by construction.** `SimpleAggregateFunction` supports exactly
`any, anyLast, min, max, sum, ...` and is for functions where "the result of
applying a function `f` to a row set `S1 UNION ALL S2` can be obtained" by
applying it to parts and again to the results
(`https://clickhouse.com/docs/sql-reference/data-types/simpleaggregatefunction`).
`min(first_seen)` and `max(last_seen)` of a multiset are unchanged by
duplicating any row (redelivery) or permuting rows (out of order), so a walk
redelivered after the 10-minute JetStream window
(`docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md:257-262`)
changes nothing, with or without the ClickHouse block dedup of 01 section 3.
The engine folds rows "within a single data part" at merge time
(`https://clickhouse.com/docs/engines/table-engines/mergetree-family/aggregatingmergetree`),
so every reader aggregates with `GROUP BY` (F33, F41), and `optimize_on_insert`
(default 1, 01 section 3) folds duplicates inside one inserted block already.

**S21. Mutable attributes: `anyLast` is not safe out of order, so attributes
with history value go in the key.** "As a query can be executed in arbitrary
order, the result of this function is non-deterministic"
(`https://clickhouse.com/docs/sql-reference/aggregate-functions/reference/anylast`).
A late older walk could therefore overwrite a newer `interface_name`. The
design puts `interface_name` (FDB), `mac` (ARP), and the neighbor identity
(LLDP, CDP) in the sorting key, so a change is a new row with its own
interval, exactly NAV's and Netdisco's identity (S7, S8). What remains as
`anyLast` is cosmetic (`kind`, `status`, `origin`, `is_router`, LLDP
`system_name`, `port_description`) and a stale value for one bucket is
tolerable. Where a caller needs the newest value for certain, the column is
`AggregateFunction(argMax, T, DateTime)` read with `argMaxMerge`, at the cost
of a timestamp per row; ties are "not deterministic"
(`https://clickhouse.com/docs/sql-reference/aggregate-functions/reference/argmax`)
but ties here are identical redeliveries.

**S22. Device unreachable versus member gone.** A member is "gone" only when a
complete walk that should contain it does not. Three facts make that
decidable: the snapshot record carries `complete` (S1); the sink writes one
row per complete walk to `set_polls` (section 5.4); and the sink derives
`vanish` transitions only from complete walks (W2 writes `last_seen` on the
member's last complete walk). A reader then tells the cases apart:
`last_seen < max(observed_at) of set_polls for the device` means gone, equal
means present at the last walk, and no newer poll row means unknown. This is
NAV's sentinel (S7) and the fix for Netdisco's "absent is not touched" (S8)
without its unbounded `active` rows. The legacy baseline shows "About 2 to 5 %
of switch scrapes run into the scrape timeout on a normal day"
(`docs/research/2026-10-01-production-monitoring-baseline.md:172`); without the
`complete` flag those walks would emit a vanish storm for 2 to 5 % of devices
every poll and a matching appear storm five minutes later.

**S23. Loss of KV state.** With W2, a sink that loses its KV copy re-emits
every member as first seen now; `min(first_seen)` keeps the earlier value in the
same bucket, and the only artefact is a spurious `appear` transition per member
once, bounded and visible (the `set_polls` row records a `baseline_reset`
reason). With (b) as the system of record the same loss would corrupt replay
until the next snapshot (S15).

**S24. Transitions are events and carry `record_id`.** A transition row is
derived once per complete walk, keyed by `(tenant_id, mac, observed_at,
device_id, vlan_id, record_id)` in a ReplacingMergeTree, so a redelivered walk
reproduces byte-identical transition rows that collapse on merge, and a reader
that counts transitions uses `GROUP BY` on the natural key (F45, F48). An
out-of-order older walk is dropped by the sink's KV compare-and-set on
`observed_at` (phase 5 requirement 1: "an older record never overwrites a newer
one"), so it emits no transition.

## 5. Recommended tables (DDL)

Engines are `Replicated*` on the 1 x 2 layout (F64). Every table is shared and
tenant-first (F6). Partition by month on the bucket keeps 12 partitions per
year per table and keeps the partition key out of the per-tenant space
(03 anti-patterns: Cloudflare 160k parts). `ttl_only_drop_parts = 1` (F8).

### 5.1 FDB presence

```sql
CREATE TABLE flowseer.fdb_presence
(
    tenant_id        LowCardinality(String),
    device_id        UUID,
    bucket           Date,                                   -- toDate(observed_at), the presence day
    network_instance LowCardinality(String),                 -- fdb_entry.proto:44
    vlan_id          UInt16            CODEC(T64, ZSTD(1)),  -- fdb_entry.proto:30
    mac              UInt64            CODEC(ZSTD(1)),       -- fdb_entry.proto:35, EUI-48 in the low 48 bits
    interface_name   LowCardinality(String),                 -- fdb_entry.proto:38, '' when absent
    first_seen       SimpleAggregateFunction(min, DateTime) CODEC(Delta, ZSTD(1)),
    last_seen        SimpleAggregateFunction(max, DateTime) CODEC(Delta, ZSTD(1)),
    walks            SimpleAggregateFunction(sum, UInt32),   -- rows folded; W1 counts walks, W2 counts writes
    kind             SimpleAggregateFunction(anyLast, Enum8('unspecified' = 0, 'other' = 1, 'dynamic' = 2, 'static' = 3, 'self' = 4, 'remote' = 5)),
    status           SimpleAggregateFunction(anyLast, Enum8('unspecified' = 0, 'active' = 1, 'invalid' = 2)),
    INDEX idx_mac mac TYPE bloom_filter(0.01) GRANULARITY 1
)
ENGINE = ReplicatedAggregatingMergeTree
PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, device_id, bucket, network_instance, vlan_id, mac, interface_name)
TTL bucket + INTERVAL 400 DAY
SETTINGS ttl_only_drop_parts = 1, index_granularity = 8192;
```

Reasoning per clause.

- `ORDER BY (tenant_id, device_id, bucket, ...)`: "set at T", "what did device
  D hold on day X", and the per-device rollup are one contiguous index range of
  `members` rows (F3, F4). `bucket` before the member columns is the choice that
  bounds "set at T" at one day of one device. The alternative order
  `(tenant, device, mac, bucket)` would make "set at T" read every day the
  device retained (members x days), which grows with retention.
- Member columns after `bucket` in ascending cardinality (F5): instance, VLAN,
  MAC, interface. `interface_name` last in the key because it is the attribute
  whose change must create a new row (S21).
- `mac UInt64`: 02 section 12.3's choice; sorting 48-bit values within one
  device and day gives shared high bytes for ZSTD.
- `first_seen`, `last_seen` as `SimpleAggregateFunction`: "better performance
  than the `AggregateFunction`" (S20 source). `Delta` because within a sorted
  run first_seen values cluster around the day's polls.
- `walks` sum: lets a reader see how many writes folded into a row (a
  diagnostic for W1 versus W2 and for the benchmark), and under W1 it is the
  presence count of the day.
- `kind`, `status` as `anyLast`: cosmetic (S21).
- `idx_mac` bloom filter, `GRANULARITY 1`: serves the per-device "history of
  MAC X" without the member-ordered table. "Rare values that are critical for
  search" (F19); a MAC appears in at most a few granules per device-day. Skip
  indexes cost on ingest ("a meaningful cost both on data ingest and on
  queries", `https://clickhouse.com/docs/optimize/skipping-indexes`); the
  benchmark measures whether the by-member table (5.2) makes it redundant.
- `PARTITION BY toYYYYMM(bucket)`: coarse (F7), 12 partitions a year, and a
  late walk for a past day lands in that day's month, where the merge folds
  it. `insert_deduplication_token` "is tracked per partition" (01 section 3).
- `TTL bucket + INTERVAL 400 DAY`: retention class "long" (13 months, year
  over year plus a margin). Section 7 proposes the classes.
- No projection: on an AggregatingMergeTree a projection needs
  `deduplicate_merge_projection_mode` other than `throw`, the default, "An
  exception is thrown, preventing projection parts from going out of sync"
  (`https://clickhouse.com/docs/sql-reference/statements/alter/projection`),
  and "Lightweight updates and deletes aren't supported for tables with
  projections" (`https://clickhouse.com/docs/data-modeling/projections`),
  which would block per-tenant erasure (F57). A materialized view into a
  second AggregatingMergeTree (5.2) gives the second order without either
  constraint.

### 5.2 FDB presence by member (tenant-wide lookup)

```sql
CREATE TABLE flowseer.fdb_presence_by_mac
(
    tenant_id        LowCardinality(String),
    mac              UInt64            CODEC(ZSTD(1)),
    bucket           Date,
    device_id        UUID,
    network_instance LowCardinality(String),
    vlan_id          UInt16            CODEC(T64, ZSTD(1)),
    interface_name   LowCardinality(String),
    first_seen       SimpleAggregateFunction(min, DateTime) CODEC(Delta, ZSTD(1)),
    last_seen        SimpleAggregateFunction(max, DateTime) CODEC(Delta, ZSTD(1))
)
ENGINE = ReplicatedAggregatingMergeTree
PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, mac, bucket, device_id, network_instance, vlan_id, interface_name)
TTL bucket + INTERVAL 400 DAY
SETTINGS ttl_only_drop_parts = 1;

CREATE MATERIALIZED VIEW flowseer.fdb_presence_by_mac_mv TO flowseer.fdb_presence_by_mac AS
SELECT tenant_id, mac, bucket, device_id, network_instance, vlan_id, interface_name,
       min(first_seen) AS first_seen, max(last_seen) AS last_seen
FROM flowseer.fdb_presence
GROUP BY tenant_id, mac, bucket, device_id, network_instance, vlan_id, interface_name;
```

The view sees inserted blocks, not merged rows (F20), which is why the target
is again an AggregatingMergeTree: every block's partial min/max folds there.
Write cost doubles (two tables), storage adds about the same bytes with `mac`
now the long run and `device_id` the sorted column (about 2 B instead of 0.1 B
for the UUID, so slightly more). Insert dedup carries into the view
(F44). "Where was MAC X seen in the tenant between T1 and T2" is now the index
range `(tenant_id, mac, bucket in [T1, T2])`, whose size is days x devices that
saw the MAC, independent of the tenant's device count and of total data.

### 5.3 FDB transitions (derived by the sink)

```sql
CREATE TABLE flowseer.fdb_transitions
(
    tenant_id        LowCardinality(String),
    mac              UInt64            CODEC(ZSTD(1)),
    observed_at      DateTime          CODEC(Delta, ZSTD(1)),   -- the complete walk that proved the change
    device_id        UUID,
    network_instance LowCardinality(String),
    vlan_id          UInt16            CODEC(T64, ZSTD(1)),
    record_id        UUID,                                       -- IngestRecord.record_id of that walk
    op               Enum8('appear' = 1, 'vanish' = 2, 'move' = 3),
    interface_name   LowCardinality(String),                     -- after the change ('' on vanish)
    prev_interface   LowCardinality(String),                     -- before the change ('' on appear)
    prev_seen        DateTime          CODEC(Delta, ZSTD(1)),    -- last walk that held the previous row
    INDEX idx_device device_id TYPE bloom_filter(0.01) GRANULARITY 4,
    INDEX idx_ts observed_at TYPE minmax GRANULARITY 4
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY toYYYYMM(observed_at)
ORDER BY (tenant_id, mac, observed_at, device_id, network_instance, vlan_id, record_id)
TTL observed_at + INTERVAL 400 DAY
SETTINGS ttl_only_drop_parts = 1, min_age_to_force_merge_seconds = 604800,
         min_age_to_force_merge_on_partition_only = 1;
```

`mac` leads after the tenant because the transition questions are per MAC
("when did X appear, move, vanish, anywhere in the tenant"); the per-device
filter then narrows inside a MAC's small range. "What changed on device D in
the last 24 h" is the one query that does not sit on this key: it reads the
tenant's transitions in the window via `idx_ts`, filtered by `idx_device`, a
cost that grows with the tenant's transition rate in the window, not with
total data (section 6, Q6). A `vanish` is written only from a complete walk
(S22), with a grace of `G` consecutive complete walks before it counts, the
NAV `MAX_MISS_COUNT = 3` idea (S7), to absorb the 300 s aging noise (S2); `G`
is a sink setting, default 2, and the benchmark varies it.

### 5.4 Poll markers (shared by all set domains)

```sql
CREATE TABLE flowseer.set_polls
(
    tenant_id    LowCardinality(String),
    device_id    UUID,
    kind         Enum8('fdb' = 1, 'lldp' = 2, 'cdp' = 3, 'neighbor' = 4),
    observed_at  DateTime          CODEC(Delta, ZSTD(1)),
    record_id    UUID,
    complete     Bool,
    members      UInt32            CODEC(T64, ZSTD(1)),
    reason       Enum8('walk' = 1, 'baseline_reset' = 2)
)
ENGINE = ReplicatedReplacingMergeTree
PARTITION BY toYYYYMM(observed_at)
ORDER BY (tenant_id, device_id, kind, observed_at, record_id)
TTL observed_at + INTERVAL 90 DAY
SETTINGS ttl_only_drop_parts = 1;
```

One row per walk: 20,000 devices x 4 kinds x 288 = 23 M rows/day at under
10 B each (the row is nearly constant within a device run). It anchors "set at
T" (the last complete walk at or before T), tells unreachable from gone (S22),
and is the member-count gauge per device over time without touching the
presence table. Retention 90 days; the per-device member count beyond that
comes from the daily rollup (section 7).

### 5.5 LLDP presence

```sql
CREATE TABLE flowseer.lldp_presence
(
    tenant_id            LowCardinality(String),
    device_id            UUID,
    bucket               Date,
    local_interface_name LowCardinality(String),                 -- neighbor.proto:24
    chassis_id_subtype   Enum8('reserved' = 0, 'chassis_component' = 1, 'interface_alias' = 2, 'port_component' = 3, 'mac_address' = 4, 'network_address' = 5, 'interface_name' = 6, 'local' = 7),
    chassis_id           String            CODEC(ZSTD(1)),       -- chassis_id.proto:23, raw bytes, 1 to 255
    port_id_subtype      Enum8('reserved' = 0, 'interface_alias' = 1, 'port_component' = 2, 'mac_address' = 3, 'network_address' = 4, 'interface_name' = 5, 'agent_circuit_id' = 6, 'local' = 7),
    port_id              String            CODEC(ZSTD(1)),       -- port_id.proto:23
    remote_device_id     UUID,                                   -- resolved by the sink from inventory, zero UUID when unknown
    first_seen           SimpleAggregateFunction(min, DateTime) CODEC(Delta, ZSTD(1)),
    last_seen            SimpleAggregateFunction(max, DateTime) CODEC(Delta, ZSTD(1)),
    system_name          SimpleAggregateFunction(anyLast, String) CODEC(ZSTD(1)),   -- neighbor.proto:39
    port_description     SimpleAggregateFunction(anyLast, String) CODEC(ZSTD(1)),   -- neighbor.proto:37
    system_description   SimpleAggregateFunction(anyLast, String) CODEC(ZSTD(3)),   -- neighbor.proto:42
    capabilities_enabled SimpleAggregateFunction(groupBitOr, UInt16),                -- neighbor.proto:50 as a bit set
    ttl_seconds          SimpleAggregateFunction(max, UInt16),                        -- neighbor.proto:59, max of what was announced
    INDEX idx_remote remote_device_id TYPE bloom_filter(0.01) GRANULARITY 4
)
ENGINE = ReplicatedAggregatingMergeTree
PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, device_id, bucket, local_interface_name, chassis_id_subtype, chassis_id, port_id_subtype, port_id)
TTL bucket + INTERVAL 800 DAY
SETTINGS ttl_only_drop_parts = 1;
```

The identity is the announcement's mandatory TLVs (`neighbor.proto:28-33`),
and the local interface. `remote_device_id` is write-time enrichment (F69): the
sink resolves chassis id to a device from the inventory in KV, so "topology
graph at T" needs no join. `management_addresses`, `ieee8023`, and `med`
(`neighbor.proto:53, 65, 67`) stay out of history in the first version: they
are configuration of the neighbor, best read from its own device record, and
a nested type under `SimpleAggregateFunction(anyLast, JSON)` is unverified.
`ttl_seconds` as `max` records the announced hold time, never the remaining
time (S5). CDP follows the same shape with `(local_interface_name, device_id,
port_id)` as identity (`cdp/v1/neighbor.proto:22-36`) and `platform`,
`software_version`, `native_vlan_id`, `duplex`, `system_name` as `anyLast`
columns; a table `cdp_presence` with the same engine and key order. A
`lldp_transitions` table follows 5.3 with the neighbor identity in place of
`mac`; it is where "link came up or went down" lives for alerting.

### 5.6 ARP and IPv6 ND presence

```sql
CREATE TABLE flowseer.neighbor_presence
(
    tenant_id      LowCardinality(String),
    device_id      UUID,
    bucket         Date,
    interface_name LowCardinality(String),                         -- neighbor_entry.proto:24
    ip             IPv6,                                           -- neighbor_entry.proto:30, v4 as ::ffff:a.b.c.d
    mac            UInt64            CODEC(ZSTD(1)),               -- neighbor_entry.proto:37, 0 when absent (incomplete)
    mac_kind       Enum8('absent' = 0, 'eui48' = 1, 'eui64' = 2),  -- eui.proto MacAddress oneof
    first_seen     SimpleAggregateFunction(min, DateTime) CODEC(Delta, ZSTD(1)),
    last_seen      SimpleAggregateFunction(max, DateTime) CODEC(Delta, ZSTD(1)),
    origin         SimpleAggregateFunction(anyLast, Enum8('unspecified' = 0, 'other' = 1, 'dynamic' = 2, 'static' = 3, 'local' = 4)),
    is_router      SimpleAggregateFunction(max, UInt8),            -- neighbor_entry.proto:44, 1 if ever announced
    reachability_seen SimpleAggregateFunction(groupBitOr, UInt8),  -- bit per NUD state seen in the bucket
    INDEX idx_mac mac TYPE bloom_filter(0.01) GRANULARITY 1
)
ENGINE = ReplicatedAggregatingMergeTree
PARTITION BY toYYYYMM(bucket)
ORDER BY (tenant_id, device_id, bucket, interface_name, ip, mac)
TTL bucket + INTERVAL 400 DAY
SETTINGS ttl_only_drop_parts = 1;
```

`mac` in the key makes an IP that changes MAC a new row (NAV's `(ip, mac)`
interval, S7). `reachability` is folded to a bit set of states seen in the day
(`groupBitOr` is in the supported list, S20), which records "was ever FAILED
today" without a row per flip (S4). A by-member view `neighbor_presence_by_ip`
with key `(tenant_id, ip, bucket, device_id, interface_name, mac)` serves "where
was IP X" the way 5.2 serves MACs; MAC-to-IP lookups go through `idx_mac` or
through `fdb_presence_by_mac` first (device and day) and then this table's
primary key.

## 6. Queries and cost model

Notation: `D` devices in scope, `M` members per device (per domain), `W`
window in days, `P` polls per day (288 at 5 min), `c` churn fraction (rows per
member per day beyond 1), `g` = 8192 rows per granule, `k` parts not yet
merged in the bucket's partition (1 after merges settle, up to a few tens while
a day is hot, a value the benchmark reports). Every cost below is independent
of total tenants and total table size unless stated.

| Q | Question | SQL shape | Rows read (model) | Granules | Grows with |
| --- | --- | --- | --- | --- | --- |
| Q1 | FDB set of device d at T | 6.1 | `M (1 + c) k` | `ceil(M (1 + c) / g) + 1` per part | nothing beyond the device's own day |
| Q2 | When did MAC m appear or move on device d, window W | 6.2 via `fdb_transitions` | transitions of m in W (tens) | 1 to 2 | transitions of that MAC |
| Q2b | same via presence (`idx_mac`) | 6.2b | `W` granules x g at most | `W` to `2 W` | window length |
| Q3 | Where was MAC m seen in the tenant, window W | 6.3 on `fdb_presence_by_mac` | `W x devices that saw m` | 1 to a few | devices along the MAC's path |
| Q4 | Topology graph at T for a scope of D devices | 6.4 on `lldp_presence` | `D x M_lldp (1 + c) k` | `D` ranges, 1 granule each (M_lldp < g) | D |
| Q5 | Site view: members per device last 24 h, D devices | 6.5 | `D x M (1 + c) x 2 k` | `D x (ceil(M/g) + 1)` per part | D, M |
| Q6 | What changed on device d last 24 h | `fdb_transitions` with `idx_ts`, `idx_device` | tenant transitions in 24 h x bloom hit rate | granules of the tenant's day | tenant transition rate, bounded by LIMIT and the 24 h window |
| Q7 | Member count per device per day, weeks to months | rollup 7.1 | `D x W` | `ceil(D W / g)` | D, W |
| Q8 | Top-N devices by new MACs this week in scope | rollup 7.1 | `D x 7` | small | D |
| Q9 | Unreachable or gone for member m on d | 6.6 on `set_polls` | polls of d in window (288/day) | 1 to 2 | window |

Q6 is the one query whose cost scales with the tenant rather than the device.
It is bounded by the 24 h window, the minmax index, and `LIMIT`; if it becomes
hot, a second transition table ordered `(tenant_id, device_id, observed_at)`
fed by a materialized view is the remedy (same pattern as 5.2). Q2b exists to
show that the bloom filter alone bounds the per-device MAC history at `W`
granules of one device; `fdb_presence_by_mac` makes it a primary-key range and
also serves Q3, so the bloom filter on `fdb_presence` is a candidate to drop
after the benchmark.

### 6.1 Set at T

```sql
WITH (SELECT max(observed_at) FROM flowseer.set_polls
      WHERE tenant_id = {t} AND device_id = {d} AND kind = 'fdb'
        AND complete AND observed_at <= {T}) AS p
SELECT network_instance, vlan_id, mac, interface_name,
       min(first_seen) AS first_seen, max(last_seen) AS last_seen, anyLast(kind) AS kind
FROM flowseer.fdb_presence
WHERE tenant_id = {t} AND device_id = {d} AND bucket = toDate(p)
GROUP BY network_instance, vlan_id, mac, interface_name
HAVING first_seen <= p AND last_seen >= p
ORDER BY vlan_id, mac;
```

The `HAVING` must follow the `GROUP BY` because unmerged parts hold partial
states (S20). Reads one index range of one device's day. With daily buckets a
member that left and returned inside the day is reported present for the
whole span between `first_seen` and `last_seen`; the transition table (Q2)
carries the exact gap when it exceeded the grace `G`. The benchmark measures
the error rate of Q1 against the generator's truth at hourly versus daily
buckets (section 8).

### 6.2 When did MAC m appear, move, vanish on device d

```sql
SELECT observed_at, op, prev_interface, interface_name, vlan_id, prev_seen
FROM flowseer.fdb_transitions
WHERE tenant_id = {t} AND mac = {m} AND device_id = {d}
  AND observed_at BETWEEN {T1} AND {T2}
ORDER BY observed_at;
```

Exact counts need `GROUP BY` the natural key or `FINAL` (F41); for a listing,
duplicates before merge are rare and `min_age_to_force_merge_seconds` closes
them in old partitions (F44).

6.2b, the presence route:

```sql
SELECT bucket, vlan_id, interface_name, min(first_seen), max(last_seen), sum(walks)
FROM flowseer.fdb_presence
WHERE tenant_id = {t} AND device_id = {d} AND mac = {m}
  AND bucket BETWEEN toDate({T1}) AND toDate({T2})
GROUP BY bucket, vlan_id, interface_name ORDER BY bucket;
```

The primary index narrows to the device, the `bucket` range to `W` days, and
`idx_mac` to the granules holding `m`.

### 6.3 Where was MAC m seen in the tenant

```sql
SELECT device_id, bucket, interface_name, vlan_id, min(first_seen) AS first_seen, max(last_seen) AS last_seen
FROM flowseer.fdb_presence_by_mac
WHERE tenant_id = {t} AND mac = {m} AND bucket BETWEEN toDate({T1}) AND toDate({T2})
GROUP BY device_id, bucket, interface_name, vlan_id
ORDER BY bucket DESC, last_seen DESC;
```

Ranking the access port over uplinks (NAV records only access ports, S7) is a
join with `lldp_presence` on `(tenant_id, device_id, bucket,
local_interface_name = interface_name)`: a port with a switch neighbor that day
is an uplink. Both sides are index ranges of the same devices and days.

### 6.4 Topology graph at T

```sql
SELECT device_id, local_interface_name, remote_device_id, chassis_id_subtype, chassis_id, port_id_subtype, port_id,
       anyLast(system_name) AS system_name, min(first_seen) AS first_seen, max(last_seen) AS last_seen
FROM flowseer.lldp_presence
WHERE tenant_id = {t} AND device_id IN {scope} AND bucket = toDate({T})
GROUP BY device_id, local_interface_name, remote_device_id, chassis_id_subtype, chassis_id, port_id_subtype, port_id
HAVING first_seen <= {T} AND last_seen >= {T} - INTERVAL 20 MINUTE;
```

Two rows per link (one from each end). The API pairs them on
`(device_id, remote_device_id)` and reports a one-sided link as a
half-edge, which is the LLDP semantics (a neighbor announces, the device
receives). With a 10-minute neighbor poll the `20 MINUTE` guard is two polls;
the exact anchor is the `set_polls` row as in 6.1 when a single device is
asked. "Topology over time" is `lldp_transitions` filtered by window, and the
graph diff between T1 and T2 is 6.4 run twice minus the intersection.

### 6.5 Site view, last 24 h

```sql
SELECT device_id, countDistinct((network_instance, vlan_id, mac)) AS members,
       max(last_seen) AS newest
FROM flowseer.fdb_presence
WHERE tenant_id = {t} AND device_id IN {scope} AND bucket >= toDate(now() - INTERVAL 1 DAY)
GROUP BY device_id;
```

Reads up to two day buckets per device in scope. For a scope of 3,000 access
devices at 300 members that is about 2 M rows.

### 6.6 Unreachable or gone

```sql
SELECT max(observed_at) AS last_walk, argMax(complete, observed_at) AS last_complete
FROM flowseer.set_polls
WHERE tenant_id = {t} AND device_id = {d} AND kind = 'fdb' AND observed_at >= now() - INTERVAL 1 DAY;
```

Compared with a member's `last_seen`: equal to `last_walk` is present, older
with a later complete walk is gone, no walk since is unknown.

## 7. Rollups and retention

**7.1 Daily member counts per device** (feeds Q7, Q8, and the member-count
gauge after `set_polls` expires):

```sql
CREATE TABLE flowseer.fdb_device_daily
(
    tenant_id LowCardinality(String),
    device_id UUID,
    bucket    Date,
    members   AggregateFunction(uniqExact, Tuple(LowCardinality(String), UInt16, UInt64)),
    new_macs  AggregateFunction(uniqExact, UInt64)   -- MACs whose first_seen day is this bucket, computed by the sink flag
)
ENGINE = ReplicatedAggregatingMergeTree
PARTITION BY toYear(bucket)
ORDER BY (tenant_id, device_id, bucket)
TTL bucket + INTERVAL 3 YEAR;
```

`uniqExact` is a set, so duplicate inserts do not inflate it (the Contentsquare
lesson in 03: "all aggregations are not idempotent" is avoided by choosing
only set-valued aggregates). It is fed by a materialized view on
`fdb_presence` inserts. "New MACs" needs the sink to know first sight in the
tenant, which it has in KV under W2; under W1 the view can only count members.

**7.2 Retention classes.**

| Class | Tables | Keep | Why |
| --- | --- | --- | --- |
| polls | `set_polls` | 90 days | Diagnostic; the daily rollup carries the count onward |
| long | `fdb_presence`, `fdb_presence_by_mac`, `fdb_transitions`, `neighbor_presence*` | 400 days | "Where was this MAC last year" and audit questions; rows are small |
| topology | `lldp_presence`, `cdp_presence`, `lldp_transitions` | 800 days | Tiny volume, cabling history is asked about across years |
| rollup | `*_device_daily` | 3 years | Charts and capacity trends |

Per-tenant retention beyond that uses `TTL ... DELETE WHERE` on a retention
column (F36) or lightweight `DELETE` for a tenant forget (F57); the presence
tables have no projections, so both stay available.

## 8. Failure modes and how the design meets them

| Failure | Effect on (a)/(e) | Effect on (b) alone | Effect on (c) | Effect on (d) |
| --- | --- | --- | --- | --- |
| Redelivery after the 10 min window | none (S20) | duplicate change rows, harmless with argMax (F43) | duplicate snapshot rows, harmless | sink must ignore by revision, else re-closes intervals |
| Out-of-order walks | none for min/max; stale `anyLast` for one bucket (S21) | none if replay sorts by `observed_at` (S15) | none | sink must ignore older walks |
| Partial walk (SNMP timeout, 2 to 5 % of scrapes) | no `vanish`, no `last_seen` update; `set_polls` row says `complete = false` (S22) | emits removes for every missing member unless gated | a smaller snapshot looks like a mass departure | closes intervals unless gated (NAV sentinel) |
| Device unreachable for hours | `last_seen` stops; `set_polls` shows no walk; reader reports unknown | nothing emitted, correct | gap, correct | open intervals persist, correct but indistinguishable from present |
| Sink loses KV | one spurious `appear` per member, `baseline_reset` poll row (S23) | wrong deltas until next snapshot | none | all intervals close and reopen |
| Member leaves and returns inside a bucket for longer than G walks | transition rows carry the gap; presence row spans it | exact | exact | exact |
| FDB aging noise (one-walk gaps) | absorbed by G (S2) | a remove and add per gap unless G | visible | an interval per gap |
| Core at 100k members | W2 rows per day about 2 x 100k (S18) | changes only | 28.8 M rows/day/device | intervals only |
| Hot partition, many parts | `k` multiplies Q1 cost until merges settle; `GROUP BY` stays correct | same | same | same |

## 9. Ingestion shape: delta events on the bus, presence rows in the store

**S25. The device service record forbids snapshot-shaped ingestion.**
"Ingestion volume must stay proportional to *change*, not fleet size:
Adaptive Watch's indicator-gated fetches and delta events, not snapshots"
(`docs/architecture/2026-08-20-device-service-and-inventory-direction.md:380-382`).
A presence table needs a "still here" signal per member per bucket, which
looks like a snapshot. The two are reconciled by splitting what crosses the
bus from what the store holds: the edge ships deltas plus a per-walk marker,
and the central sink, which holds the current set in KV anyway (phase 5),
materialises the presence rows itself. Nothing proportional to fleet size
crosses the bus in steady state, and the ClickHouse tables in section 5 are
unchanged.

**9.1 Shapes compared.** `N` = fleet members, `V` = devices, `P` = walks per
day, `ch` = member changes per day (appear, vanish, key-attribute change).

| Shape | Bus records per day | Store rows per day | Baseline dependence | Fits S25 |
| --- | --- | --- | --- | --- |
| I1 full walk per poll (the S1 strawman) | `N x P` (A: 7.4 B, B: 59 B) | W1 or W2, sink diffs | none at the edge; sink needs KV for W2 | no |
| I2 edge deltas + per-walk marker, central presence | `ch + V x P` (A: about 3 M + 5.8 M per kind) | `N x (1 + c)` presence, `ch` transitions, `V x P` polls | edge KV baseline; marker carries a set hash so drift is detected per walk (9.3) | yes |
| I3 edge deltas + daily edge snapshot (F43's marker) | `ch + V x P + N` (A: adds 26 M, B: adds 205 M) | as I2 | snapshot repairs drift once a day, blind in between | partly: one term is `N`, at 1/288 of I1 |
| I4 edge deltas + per-member heartbeat each bucket | `ch + N / buckets` | as I2 | heartbeat is the snapshot in slices | same as I3 at daily buckets |

I2 is the recommendation. Its bus volume is `ch` (proportional to change)
plus one small marker per device per walk, the same order as a counter sample
record and independent of set size. I3 is the fallback when a set hash cannot
be computed consistently on both sides (9.3), and it still cuts bus volume
288-fold against I1.

**9.2 What the edge emits per complete walk.** For each set kind:

- Delta records: `appear(member, attributes)`, `vanish(member)`,
  `change(member, old attributes, new attributes)` under `IngestRecord`, each
  with `Provenance.observed_at` of the walk, from a diff against the edge's
  last complete walk for the device. "Attributes" are only the key attributes
  of section 5 (FDB interface and VLAN; ARP MAC; LLDP identity). LLDP
  `time_to_live` and ARP `reachability` never produce a delta (S4, S5); the
  edge folds reachability into a bit set it ships in the marker's per-kind
  summary at bucket close, or drops it.
- One marker record: `walk(kind, observed_at, complete, members, set_hash,
  baseline_reset)`. `set_hash` is an order-independent hash of the member
  identities (sum or xor of per-member 64-bit hashes, so both sides compute it
  without sorting). `baseline_reset = true` on the first walk after an edge
  restart or a device reassignment, followed by one `appear` per member: the
  bounded resync.
- A partial walk emits the marker with `complete = false` and no deltas
  (S22). A vanish honours the grace `G` at the edge (S7), so one-walk aging
  gaps (S2) never cross the bus.

Volume per day, scenario A: FDB `ch` about 5 % turnover + 0.5 % moves + 10 %
idle gaps longer than G at most, about 4 M deltas; markers 5.8 M per kind;
LLDP deltas in the thousands. Scenario B adds 2,000 cores at 100k members
with 2 % turnover: 4 M more deltas. Both are below one tenth of the
sample-record volume of 20k devices' interface counters at 5 minutes, which
the brief already plans for.

**9.3 What the sink does with them.** The sink keeps the current set per
device in KV (its own bucket or the state projector's, phase 5 open question
"Key layout in the bucket"), applies deltas with compare-and-set on
`observed_at` so an older redelivered delta is dropped (phase 5 requirement 1),
and writes:

- `set_polls`: one row per marker (5.4), the anchor for "set at T" and for
  unreachable versus gone.
- `fdb_transitions`, `lldp_transitions`: one row per delta (5.3), no central
  diffing needed; the delta's `record_id` is the row's `record_id`, so a
  redelivered delta collapses on merge (S24).
- `*_presence`: at each `appear` a row with `first_seen = observed_at`; at
  each `vanish` or `change` a row with `last_seen = prev_seen`; at bucket
  close, one row per member still present with `last_seen = observed_at of
  the device's last complete walk`. That is strategy W2 of S18 driven by
  deltas instead of by walks; the presence row count is `N x (1 + c)` per day
  (S17) and it is generated centrally, never ingested. A member that stays
  gets exactly one row per day, written at local midnight of the sink's
  schedule or at the next walk after the bucket changed.
- Drift check: on every marker the sink compares `set_hash` with the hash of
  its KV set. A mismatch means a delta was lost or applied out of order; the
  sink logs it, writes the marker with a `drift` flag, and asks the edge for a
  resync of that device over Connect (the same call shape as the raw window,
  ingestion direction record "Raw bytes stay at the edge"). Until the resync
  arrives the presence rows are those of the drifted set, and the transition
  table shows the resync as a `baseline_reset` marker followed by appear and
  vanish rows. Nothing else in the store is affected because presence rows are
  min/max folds (S20).

**9.4 Correctness per domain under I2.**

| Domain | Delta trigger | Heartbeat need | Residual risk |
| --- | --- | --- | --- |
| FDB | member `(instance, vlan, mac)` appears, vanishes after `G` complete walks, or moves interface | none on the bus; presence row at bucket close from KV | aging gaps shorter than `G` walks are invisible, by design (S2) |
| LLDP, CDP | identity `(local port, chassis, port)` appears or vanishes; `system_name` or `port_description` change ships as `change` | none | a flap shorter than the 10-minute poll is invisible on any model |
| ARP, ND | `(interface, ip, mac)` appears, vanishes, or MAC changes; `origin`, `is_router` as `change` | none; reachability bit set rides the marker at bucket close | NUD state history is a bit set per day, not a timeline (S4) |

**9.5 Fallback.** Until the edge diff exists the sink accepts full-walk
records (I1) and runs W1 (S18); that mode violates S25 and is for the
benchmark and a first integration only. The tables are the same in both modes
because `min` and `max` fold either stream (S18).

**9.6 KV sizing.** Key count equals fleet members (26 M to 205 M, S13) at both
the edge (per device, small) and the central sink (whole fleet). Whether one
JetStream KV bucket serves 200 M keys with watch and replay at that size is
unverified; a bucket per tenant or per set kind bounds it, and the phase 5
plan should measure before choosing.

## 10. Benchmark design

Target: a single-node ClickHouse 26.8 container; generator in Go (the sink's
language) writing with clickhouse-go v2 batches of 50k rows per table.

**Generator parameters (all seeded, seed default 20261008).**

| Parameter | Default | Note |
| --- | --- | --- |
| tenants | 1 x 10,000 devices, 20 x 500, 200 x 50 | brief: thousands small, a few at 10k; scale steps multiply the small ones |
| device classes | access 95 %: members lognormal median 300, range 50 to 2,000; core 5 %: uniform 20k to 100k | S3 |
| poll interval | 300 s FDB and ARP, 600 s LLDP | brief |
| days | 30 (1x) | |
| FDB member process | each member has a home port; daily turnover 5 % (access) 2 % (core); move 0.5 %/day; idle gaps (S2): 10 % of members per day gap of 5 to 60 min; VLAN change 0.1 %/day | |
| ARP process | 2,000 gateways at 10k; turnover 3 %/day; reachability flips 20/day per member; MAC change 0.2 %/day | S4 |
| LLDP process | 10 neighbors per device, 2 per core port; link flap 0.5 %/day lasting 1 to 30 min; system_description change 1 % of neighbors once in 30 days | S5 |
| faults | partial walk 3 % of walks (random 60 % of members); redelivery 1 % of walks after 10 to 60 min; out-of-order 0.5 % of walks delayed one interval; device unreachable 0.5 % of device-days | baseline 2 to 5 % timeouts |
| write strategy | W1 and W2 both generated from the same truth; W2 is produced by running the generator's edge diff (9.2) and the sink logic (9.3) | S18, section 9 |
| bus volume | records per day emitted by the edge diff, by kind: deltas, markers, resyncs | S25; expected `ch + V x P`, flat in `N` |
| bucket | day (default), hour (variant) | measures Q1 error rate |
| grace G | 2 (default), 1, 3 | S2, S7 |

The generator also emits the truth: per device the exact member set at every
poll, used to score Q1 and Q2.

**Scale steps.**

1. Fixed scope, growing total: 1x, 4x, 16x total devices (add small tenants and
   10 more days per step so both axes of total data grow). Expected: Q1 to Q5,
   Q7 to Q9 `read_rows` and p95 flat within 20 %; Q6 flat because the probed
   tenant does not grow.
2. Fixed total, growing scope: Q4 and Q5 with D = 10, 100, 1,000, 3,000;
   expected linear in D (within 2x of `D x M (1 + c) k`).
3. Growing M: one device at 300, 2,000, 20k, 100k members; Q1 expected linear
   in M; Q3 for a MAC seen on 1, 10, 100 devices expected linear in devices.
4. Write: W1 at step 1x and 4x, W2 at 1x, 4x, 16x; record rows/s, parts per
   insert (`system.part_log`, expected 1 part per insert per partition touched,
   normally 1), merge rows per day versus inserted rows (amplification).

**Metrics.** `system.query_log`: `read_rows`, `read_bytes`,
`query_duration_ms` p50 and p95 over 50 runs each with cache cleared
(`SYSTEM DROP MARK CACHE`, `query_cache` off), `memory_usage`. `EXPLAIN
indexes = 1` for granules selected per query. `system.parts` and
`system.columns`: `data_compressed_bytes / rows` per table and per column
after `OPTIMIZE TABLE ... FINAL` on closed partitions and again without it.
`system.part_log`: parts per insert, merged rows per day. Q1 and Q2
correctness: precision and recall against the truth at the generator's T
samples (1,000 random T per step).

**Pass criteria.**

| Check | Pass |
| --- | --- |
| Q1, Q3, Q4, Q5, Q7 read_rows | within 2x of the section 6 model at every step |
| Q1 to Q5 p95 across step 1 | within 20 % of the 1x value |
| Q6 p95 | within 20 % across step 1 (same tenant), and under 1 s at 16x |
| Q1 recall and precision versus truth, day bucket, G = 2 | recall at least 0.995, precision at least 0.99 (idle gaps are the expected loss) |
| Q2 transitions versus truth | every move and every gap longer than G walks found, no transition from a partial walk |
| fdb_presence bytes per row (merged) | at most 12 B (estimate 7 to 8 B, S19) |
| W1 insert | at least 100k rows/s sustained with parts per insert = 1 |
| W2 insert | rows per member per day at most 3 + changes |
| Redelivery and out-of-order | Q1 result identical with faults on and off |
| Partial walk | zero `vanish` rows attributed to a `complete = false` walk |
| Bus volume (step 3, M from 300 to 100k on one device) | delta records per day grow with `ch`, not with `M`; marker records exactly `P` per kind |
| Drift | with 0.1 % of deltas dropped by the generator, every affected device shows a `set_hash` mismatch on its next marker and is repaired by one resync |

## 11. Schema gaps

The tables above use what history needs, not only what the messages carry
today. Each gap names the missing carrier or field, the proposed addition,
and the evidence. Breaking changes are allowed (AGENTS.md, Agent behavior),
so none of these proposes a compatibility path.

| # | Gap | Proposed addition | Evidence |
| --- | --- | --- | --- |
| G1 | No `IngestRecord` arm carries a set table or a set change. Only `syslog` exists. | Four delta messages and one marker, as `oneof payload` arms: `FdbDelta { op Enum(APPEAR, VANISH, CHANGE); FdbEntry entry; FdbEntry previous }`, `LldpNeighborDelta`, `CdpNeighborDelta`, `NeighborCacheDelta` with the same shape over their row messages, and `TableWalk { kind Enum(FDB, LLDP, CDP, NEIGHBOR); bool complete; uint32 members; fixed64 set_hash; bool baseline_reset; Duration sys_uptime; Timestamp started_at }`. `Provenance.observed_at` is the walk's end. | `ingest_record.proto:25-30`; S25 (deltas, not snapshots); 9.2 and 9.3 for why `complete`, `set_hash`, `baseline_reset` are load-bearing |
| G2 | `TableWalk` needs the device's `sysUpTime` and the walk's start, or MIB `TimeStamp` values (G5, G6, G7) cannot be converted to wall time and a reboot between walks is invisible. | `sys_uptime`, `started_at` on `TableWalk` (G1). | `IP-MIB:2709-2717` ("The value of sysUpTime at the time this entry was last updated ... prior to the last re-initialization ... zero"); `LLDP-MIB:719-731` (TimeStamp); the ICX LLDP walk took "2.4k varbinds, ~22s" (`docs/research/device-inventory/lab/labsw06-ruckus-icx7150.md:162`), so rows at the start and end of one walk differ by 22 s; `docs/research/network-domain-atlas/entities/09-ops.md:218-222` ("every timestamp FlowSeer records ... FDB ages — is only meaningful if the device's clock is right") |
| G3 | `FdbEntry` has no per-entry age, so `first_seen` can never precede the first walk that saw the row, and a row re-learned between two walks looks continuous. | `google.protobuf.Duration age = 7` on `FdbEntry`, absent means unreported. History: `first_seen = min(observed_at - age)`, and an `age` smaller than the poll interval on a row that was present last walk is a re-learn the edge may report as `CHANGE`. | OpenConfig `mac-table ... state/age`: "The time in seconds since the MAC address has been in the table" (`spec/yang/openconfig/openconfig-network-instance-l2.yang:429-434`); Junos `detail` shows "Learned: 5w0d 19:18:08" (Juniper Community thread, search summary, unverified); Q-BRIDGE-MIB has no per-row age, so SNMP sources leave it absent |
| G4 | No device-level bridge facet carries the FDB aging time, the dynamic entry count, the capacity, or the learned-entry discards. The grace `G` (S2, 5.3) and the Adaptive Watch indicator for "walk the FDB only when it changed" need them, and discards are the capacity alarm. | `switching/v1 BridgeFacet` (device-scoped, per network instance): `Duration aging_time`, `uint32 dynamic_count`, `uint32 max_entries`, `uint64 learned_entry_discards` (counter). Stored in the samples domain, not in the set tables. Edge rule: `G = ceil(aging_time / poll_interval) + 1`. | `BRIDGE-MIB:770-778` `dot1dTpAgingTime` "802.1D-1998 recommends a default of 300 seconds"; `Q-BRIDGE-MIB:348-358` `dot1qFdbDynamicCount` "The current number of dynamic entries in this Filtering Database"; `BRIDGE-MIB:751-763` `dot1dTpLearnedEntryDiscards` "discarded due to a lack of storage space"; OpenConfig `mac-aging-time`, `maximum-entries` (`docs/research/schema-building-blocks/05-l2-and-instances.md:103`) |
| G5 | LLDP has no change indicator and no per-entry change time. Without them the edge walks the whole remote table every 10 minutes although the MIB offers a one-object test. | `lldp/v1 RemoteTablesStats` (device-scoped): `Timestamp last_change_at` (converted through G2), counters `inserts`, `deletes`, `drops`, `ageouts`. `Timestamp last_changed_at` on `Neighbor` from `lldpRemTimeMark`. | `LLDP-MIB:719-731` "An NMS can use this object to reduce polling of the lldpRemoteSystemsData objects"; `LLDP-MIB:792-802` ageouts "because the information timeliness interval has expired"; `LLDP-MIB:1368-1377` TimeFilter key; atlas trap "Walking it returns every historical row unless you filter" (`docs/research/network-domain-atlas/entities/03-switching.md:467-469`) |
| G6 | CDP has no change indicator and no per-entry change time. | `Timestamp last_changed_at` on `cdp/v1 Neighbor` from `cdpCacheLastChange`; `cdp/v1 GlobalState.last_change_at` from `cdpGlobalLastChange` as the indicator. | `cdpCacheLastChange` in the cdpCacheTable column list (`docs/research/schema-building-blocks/05-l2-and-instances.md:73`); `cdpGlobal*` list (same file, 75); the CISCO-CDP-MIB text itself was not fetched for this report (unverified) |
| G7 | `NeighborEntry` has no `last_updated`, so a refreshed mapping and an untouched one look alike and the edge cannot skip unchanged rows cheaply. | `Timestamp last_updated_at = 7` on `NeighborEntry`, absent when the device reports zero or nothing (converted through G2). History: an edge-side `CHANGE` only when key attributes change; `last_updated_at` itself never produces a delta. | `IP-MIB:2709-2717` `ipNetToPhysicalLastUpdated`; atlas row for `ipNetToPhysicalTable` naming `ipNetToPhysicalLastUpdated` and the NUD state column (`docs/research/network-domain-atlas/entities/04-ip.md:103`) |
| G8 | Neighbor announcements carry two TTLs on the wire, the announced hold time and the remaining time, and the message keeps only the announced one. The remaining time is how the edge tells a neighbor about to age out from a fresh one, which turns the next walk's `VANISH` from a surprise into an expected event. | `Duration time_remaining = 14` on `lldp/v1 Neighbor` and `= 20` on `cdp/v1 Neighbor`, absent when unreported; never a delta trigger and never stored in history (S5). | ICX output shows both: "Neighbor: 7801.5ab0.0501, TTL 113 seconds" and "+ Time to live: 120 seconds" (`docs/research/device-inventory/lab/_raw/labsw06/cli-session.txt:189-192`); the existing comment admits the gap: "a consumer then cannot tell a fresh announcement from one about to expire" (`lldp/v1/neighbor.proto:57-58`) |
| G9 | A forwarding row on some devices names a port list, not one interface, and `FdbEntry.interface_name` is a single string. | Adapter rule first: a dynamic row whose port list equals a LAG's member set maps to the LAG interface name (ICX already reports `lg1`). Fallback field `repeated string interface_names = 8` for static rows on several ports; the unicast CEL already drops the multicast rows that make up most of them. | LANCOM GS2326 `show mac-table` rows "Dynamic 18-fd-74-e2-6e-80 1 25,26" and "Static 33-33-00-00-00-01 1000 1-26,CPU" (`docs/research/device-inventory/lab/_raw/labsw04/ssh-session6.log:216-231`); ICX `show mac-address` rows on "lg1" (`.../labsw06/cli-session.txt:950-961`); `fdb_entry.proto:23-27` unicast rule |
| G10 | Topology enrichment (`remote_device_id`, 5.5) needs an index from an LLDP chassis id to a device, and the inventory holds only `base_mac`. Chassis id subtypes NETWORK_ADDRESS, INTERFACE_NAME, and LOCAL are not resolvable from it. | No new field: the state projector indexes every device's own `LocalSystem.chassis_id` into KV as `chassis/<subtype>/<hex> -> device_id`, and the sink resolves against it, falling back to `base_mac` for subtype MAC_ADDRESS. The plan must name this projection. | `model/inventory/v1/device.proto:142-144` (`base_mac`); `lldp/v1/local_system.proto:11` (`chassis_id`); subtypes `chassis_id_subtype.proto` (seven subtypes) |
| G11 | The FDB has no device-wide "something moved" indicator in the standard MIBs, so unlike LLDP (G5) the edge must walk it every interval. | None in the schema. `dynamic_count` (G4) is a necessary-but-not-sufficient pre-check (a move keeps the count). Vendor move counters exist (ExtremeXOS `show fdb mac-tracking statistics` with Add, Move, Delete counts; ALAXALA per-port move counts, search summaries, unverified) and can feed `BridgeFacet` as optional counters later. | Q-BRIDGE-MIB and BRIDGE-MIB grep: no `LastChange` object for the FDB; vendor evidence from search results only |
| G12 | Ruckus SmartZone's ICX feed carries no MAC table, ARP, or LLDP neighbor data, so for ICX these sets come from SNMP or CLI on the switch, not from the controller. | None in the schema; an integration fact for the plan: the ICX adapter needs the SNMP walk path (LLDP MIB at `1.0.8802.1.1.2`). | `spec/proto/ruckus/icx/switches.proto` grep: only `BgpNeighborEntry` (4724), `PortMacSecurity` (4792), `SecureMac` (4803); `docs/research/device-inventory/lab/labsw06-ruckus-icx7150.md:162` |
| G13 | Port security (sticky or secure MACs) is a kind the FDB enum does not name, and a secure MAC's history is an audit question. | `FDB_ENTRY_KIND_SECURE = 6` on `FdbEntryKind`, or a `bool secure` beside `kind`. Low priority. | `spec/proto/ruckus/icx/switches.proto:4792-4803` (`PortMacSecurity`, `SecureMac`); Nexus `show mac address-table` "Secure" column (page returned 404, unverified) |

Fields G3, G7, and G8 are observations the edge uses and the history sink
mostly ignores; they are listed because the delta model (section 9) is only
as cheap as the edge's ability to skip unchanged rows, and because `age`
lets `first_seen` be true before FlowSeer started watching a device.

## 12. Corrections to earlier dossiers

- 03 (set-valued domains) states a change log "needs a reliable baseline and
  ordered delivery". Ordered delivery is not needed when replay sorts by
  `observed_at` (S15); the baseline dependence stands and is the reason the
  change log is derived, not primary.
- 03's presence row keyed `(tenant, device, member key, bucket)` loses a port
  move inside a bucket. The attribute whose history is asked for must be part
  of the key (S21), which NAV and Netdisco both do (S7, S8).
- 02 F43 and 12.3 size snapshots "daily or weekly". With presence buckets no
  snapshot rows exist; the daily presence row is the snapshot, and the
  transition table replaces add/remove rows.

## 13. Unverified

1. Cisco IOS MAC aging default 300 s: only a secondary source fetched; the
   Nexus "up to twice the timeout" claim is a search summary.
2. LLDP timer 30 s and holdtime 120 s on IOS: Cisco Community and ipcisco.com;
   the IEEE 802.1AB text was not fetched. NX-API holdtime default 120 is
   fetched.
3. Absence of NetBox and Nautobot plugins with FDB or ARP history (two
   searches).
4. LibreNMS `ports_fdb` schema (from forum output) and the "FDB history" pull
   request; Observium `vlans_fdb` columns (from a poller log line).
5. NAV: whether `cleanup()` is gated on the sentinel outside `shadows/cam.py`.
6. Bytes per row estimates in S19 and the 2 B/row figure in S14.
7. `SimpleAggregateFunction(anyLast, JSON)` support for LLDP extensions.
8. A JetStream KV bucket at 200 M keys (section 9).
9. The `k` parts factor for hot partitions and merge amplification under W1 at
   core scale; the benchmark reports both.
10. Infoblox NetMRI and Cacti mactrack behaviour (search summaries only).
11. The order-independent `set_hash` (9.2) as a drift detector: collision
    behaviour of a sum or xor of 64-bit member hashes under adversarial
    inputs is not analysed; a 128-bit sum or a sorted hash is the fallback.
12. Edge-side state for the diff across edge restarts and the HA case of two
    edges serving one device: the `baseline_reset` resync bounds the damage,
    the cost at scale is unmeasured.
