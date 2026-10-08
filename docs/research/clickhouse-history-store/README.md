---
title: ClickHouse history store research
date: 2026-10-08
status: research; decisions live in the ingestion pipeline direction record and the history store plan
---

# ClickHouse history store research

Evidence for the ClickHouse history store that the
[central ingestion pipeline record](../../architecture/2026-10-02-central-ingestion-pipeline-direction.md)
names for append-only records and numeric time-series. Every claim in the
dossiers cites a URL fetched on 2026-10-08. Claims no fetched source confirmed
say "unverified", and each dossier collects them at its end. The legacy store
described in the
[production monitoring baseline](../2026-10-01-production-monitoring-baseline.md)
supplies lessons (scale, the insert stall, set-table volume) and no pattern.

| Dossier | Covers | Read before |
| --- | --- | --- |
| [00 Domain map](00-domain-map.md) | Every data domain present or planned, its nature, whether it belongs in the store, and the dossier that models it | Checking that a domain is covered |
| [01 Clients, durability, dedup](01-clients-durability-dedup.md) | Go clients and their versions, insert acknowledgement and quorum, insert dedup, row policies, query limits, migration tools, protobuf input | Choosing the client, the ack rule, or the migration tool |
| [02 Best practices](02-best-practices.md) | Findings F1 to F74: keys, partitions, types, codecs, counter rates, rollups, TTL, change and set storage, dedup, logs and the text index, tenancy, workload isolation, operations, enrichment, and draft DDL | Designing a table or a query path |
| [03 Prior art](03-prior-art.md) | 23 systems that keep network or observability history in ClickHouse, compared on table split, keys, tenancy, dedup, cardinality, rollups, retention, and migrations | Arguing for or against a pattern |
| [04 Interface and device metrics](04-interface-device-metrics.md) | Interface and Ethernet counters and status, CPU, storage, sensors, uptime, BGP, BFD, VRRP, and DHCP counters: wide samples per counter family, idempotent rollups, exact increase across discontinuities | Any counter or gauge table |
| [05 Wireless](05-wireless.md) | Radios, BSS, RF neighbor scans, wireless clients and sessions, the cross-device MAC lookup | Any wireless table |
| [06 Set domains](06-set-domains.md) | FDB, LLDP and CDP neighbors, ARP and ND: presence per member per day, walk markers, derived transitions, delta events on the bus | Any table whose device reports a full set per poll |
| [07 Events](07-events-logs-alarms.md) | Syslog, SNMP notifications, alarms and their intervals, per-domain transition tables, storm handling, the text index | Any append-only event table |
| [08 Routing and adjacencies](08-routing-adjacencies.md) | Routes, BGP paths, OSPF, IS-IS, STP, LACP, multicast, 802.1X, DHCP leases, NAT sessions, slow sets, one shared protocol transitions table | Any control-plane table |
| [09 Lifecycle, topology, audit](09-lifecycle-topology-audit.md) | Entity transitions, binding reachability, links, firmware and licences, drift, audit trails, device at site at time T | Inventory history, audit, or enrichment |
| [10 WAN, physical layer, planned](10-wan-phy-future.md) | PoE, optics, path quality and ICMP probes, cellular, flows, VPN, SD-WAN, discovery, and the five table patterns the store needs | Any new domain, to pick its pattern |
| [11 OLAP](11-olap.md) | The five patterns checked against fleet analytics: 24 analyst queries with cost models, stamped versus dictionary dimensions, the cross-domain join contract, daily rollups, the analyst workload | Any analytics query, dashboard, or dimension |
| [12 Benchmark](12-benchmark.md) | Measured on ClickHouse 26.8: query cost flat from 1x to 16x total data, row-policy forms, provider queries, rollup size, deduplication after redelivery. The suite is in [`benchmark/`](benchmark/README.md) | Checking a cost claim or rerunning the measurement |

Each of 04 to 10 ends with a schema gaps section: the fields, messages, and
record carriers the protobuf model lacks for its domains, with the proposed
addition and the evidence.

## What the evidence points to

These are recommendations for the plan to decide, not decisions.

- **Shared tables, tenant first in the sort key.** ClickHouse's multi-tenancy
  guide says a table or database per tenant "doesn't scale for 1000s of
  tenants" (02, F52), and Snuba, PostHog, Uptrace, and Cloudflare all lead
  their keys with the tenant (03, patterns). Cloudflare put the tenant in the
  partition key and reached 160k parts per replica (03, anti-patterns).
- **A typed table per domain.** Systems with a known schema (Akvorado, Snuba,
  Glaber) use typed tables per domain. Generic tables (ntopng time series,
  Telegraf, qryn) paid in Map promotion mutations, full scans, and an `ALTER`
  per new tag (03, implications). FlowSeer's records are typed protobuf, so the
  schema is known when the code builds.
- **Samples apart from changes.** Periodic numbers stay raw, ordered by
  entity then time, with rates computed at read time and rollups fed by
  incremental views (02, F25 to F33). State transitions are small append-only
  rows ordered by tenant, entity, and time (03, samples vs changes).
- **Presence intervals for set-valued domains.** No public project stores FDB,
  neighbor, or client sets in ClickHouse (03, unverified 30). The closest
  precedent is a presence row per member and time bucket holding
  `min(first_seen)` and `max(last_seen)` in an AggregatingMergeTree, which
  stays correct under redelivery because both aggregates are idempotent (03,
  set-valued domains). A change log with snapshot markers is the alternative
  (02, F43). It needs a reliable baseline and ordered delivery, which
  at-least-once delivery does not give.
- **No exactly-once per key.** Insert dedup hashes a whole block inside a
  window of 3,600 s or 10,000 blocks, so a late redelivery in another batch is
  stored again (01, section 3). ReplacingMergeTree removes duplicates only at
  merge time, and PostHog calls relying on it for events "a mistake" (03,
  anti-patterns). Duplicate-tolerant shapes (raw counters, idempotent
  aggregates) and read-time dedup where a count must be exact cover the rest
  (02, F45 to F48).
- **Bounded queries.** A read-only query user with pinned limits, a workload
  that keeps analytics off the ingestion CPU, and rollups that make long
  windows cheap answer the insert stall the baseline records (02, F56).
- **Migrations owned by FlowSeer.** golang-migrate links the deprecated v1
  client and goose fits replicated databases poorly (01, section 7). Akvorado,
  Uptrace, SigNoz, and Snuba each run their own Go or Python tool that issues
  replicated DDL from one job before writers start (03, migrations).
- **Version.** ClickHouse 26.8 LTS has the text index, unified insert dedup,
  and asynchronous inserts on by default. The sink therefore sets
  `async_insert` explicitly (02, F1, F2, F15).
