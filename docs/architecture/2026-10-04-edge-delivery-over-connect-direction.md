---
title: Edge Delivery over Connect - Direction
type: direction
date: 2026-10-04
topic: edge-delivery-over-connect
status: proposed-direction
amends: docs/architecture/2026-08-20-device-service-and-inventory-direction.md, docs/architecture/2026-10-02-central-ingestion-pipeline-direction.md, docs/architecture/2026-10-03-central-high-availability-direction.md, docs/architecture/2026-10-03-deployment-direction.md, docs/architecture/2026-10-03-edge-high-availability-direction.md
---

# Edge Delivery over Connect - Direction

How what an edge publishes (ingest records and the agent's own telemetry)
reaches central, and what buffers it while central is unreachable.

## Context

The [device service record](2026-08-20-device-service-and-inventory-direction.md)
chose NATS as the fabric between edge and central for three jobs: announce,
execute, and events. Its 2026-09-06 amendment moved every decision to
Connect. The leaf link has carried only what the edge publishes since then,
which is one-way traffic from an edge buffer to central.

For that one job the link costs:

- A second public listener and a second credential system. `AttachBus`
  returns an account JWT, a user credential, and cluster URLs
  (`spec/proto/flowseer/edge/attach/v1/bus.proto`), and the hub keeps
  operator, account, and user keys (`src/modules/edgebus/keys.go`).
- One NATS account per edge. A shared edge account let one edge delete
  another's buffer through a flow-control reflection that no permission list
  closes (`src/modules/edgebus/README.md`, "Accounts are the security
  boundary").
- Three copies of each record: `EDGE_BUFFER` on the edge,
  `FLOWSEER_EDGE_<edge-id>` on the hub, and the typed CENTRAL stream.
- A cap on the number of edges from per-account disk budgets, and a full
  re-source of an edge buffer after a hub stream ages out (same README).
- A NATS server started inside every agent
  (`src/edge/agent/internal/busattach/busattach.go`).

The edge already delivers audit records over Connect, and central alone
writes them to JetStream (`spec/proto/flowseer/edge/audit/v1/audit_service.proto`).

## Decision

### The edge delivers over Connect and joins no bus

An edge sends what it publishes through authenticated Connect calls on the
listener that serves the other edge services. It runs no NATS server and
opens no second connection. Central takes the edge from the signed assertion
and the tenant from its edge store, as `DispatchService` does
(`src/services/device/internal/host/host.go`, `busResources.edgeTenant`).
Tenancy stays ambient: the request names neither.

### Two calls, one per kind of traffic

- An ingest call carries a batch of `IngestRecord`. Central validates each
  record and publishes it into the typed CENTRAL streams under the message
  id `<tenant>.<record_id>`, which is the deduplication the
  [ingestion record](2026-10-02-central-ingestion-pipeline-direction.md)
  already relies on.
- A telemetry call carries a batch of OTLP bodies, each with its signal.
  Central posts each body to the collector unchanged and keeps the rules for
  which answers retry and which drop.

Both answer with the number of leading items central settled. A settled item
is one central holds, or one it refused for a reason about the item. The
edge resends from the first unsettled item.

### The edge buffers in two segmented logs

The agent owns a small append-only log of checksummed records in segment
files. A sealed segment is the upload unit, and the agent deletes it once
central has settled every record in it. Each log is bounded by bytes and by
age and discards its oldest segment first, counting what it discards.

Device records and the agent's telemetry get separate logs with separate
bounds and separate drains. Telemetry volume then cannot evict a device
record, and a collector outage cannot hold device records back.

Fsync stays periodic on the edge. A record lost to a power cut there is a
gap in history, as the device service record already accepts.

### Central keeps JetStream

The typed ingest streams, the evidence stream, the audit streams, and the
key-value buckets are unchanged. This record removes NATS from the link, not
from central.

### What goes away

`AttachBus` and its messages, the embedded leaf node, per-edge NATS accounts
and minted users, the per-edge hub streams with their follower and
forwarder consumers, and the bus listener an edge dials.

## Alternatives

- **Keep the leaf link.** JetStream sourcing gives resume and flow control
  for free. Rejected because one-way upload does not need a broker on both
  ends, and the costs under Context are permanent.
- **Keep an embedded JetStream as a local-only buffer.** No new storage
  code. Rejected because every agent would still start a NATS server to
  hold an append-only file.
- **A third-party log library.** None fits. Versions are from the Go module
  proxy on 2026-10-04, and the first three were read from the module zip
  the proxy serves for that version.
  `github.com/tidwall/wal` v1.2.1 has no record checksum.
  `github.com/hashicorp/raft-wal` v0.5.0 is typed to `raft.Log`.
  `github.com/JohanLindvall/diskqueue` v0.0.13 matches the features but has
  one author and was created in June 2026. `github.com/nsqio/go-diskqueue`
  last released in 2021. Prometheus `tsdb/wlog` is importable only as part
  of the Prometheus module.
- **A queue on bbolt.** Rejected: a B+tree file does not shrink, and the
  job is sequential.
- **A layout other than segment files.** Grafana Alloy's stable metrics
  path buffers in the Prometheus write-ahead log, which is 128 MB segment
  files. JetStream's own file store writes message blocks of up to 8 MB
  (`nats-server` v2.15.0, `server/filestore.go`, `defaultLargeBlockSize`).
  The layout is kept and the broker around it is dropped.
- **An OTLP/HTTP relay on the edge listener in place of a typed telemetry
  call.** It needs no schema. Rejected because the retry and drop rules
  would move into agents in the field.
- **A central stream for telemetry with the forwarder kept.** Rejected: it
  buffers the agent's own telemetry twice.

## Consequences

- The device service record's "Edge attachment and enrollment" section, its
  transport diagram, and its "Exposing NATS" bullet describe the leaf link
  and change with the code that removes it.
- The ingestion record's stage diagram loses the leaf link and the per-edge
  stream. Intake becomes the handler of the ingest call.
- The [central high availability record](2026-10-03-central-high-availability-direction.md)
  no longer needs central to push per-edge account claims or to expose a
  NATS WebSocket listener. Central becomes a plain client of the cluster.
- The [deployment record](2026-10-03-deployment-direction.md) exposes one
  edge-facing address, not two.
- The [edge high availability record](2026-10-03-edge-high-availability-direction.md)
  loses its open question about the embedded leaf node on FreeBSD.
- `GOALS.md` states NATS as the fabric between edge and central. That line
  changes when this record is accepted.
- The agent still links `nats-server`, because `src/common/service` holds
  the process-local bus in the package that holds `service.Run`. Neither
  host enables that bus. Removing the import is a separate decision.
- Resume, backpressure, and the delete-on-settle rule are FlowSeer code now.

## Open questions

- Whether the ingest call sustains the load the device service record
  states, thousands of messages per second with tenfold bursts. Measured on
  an Apple M3 Pro, it does not under per-message sync: about 215 records/s
  at batch 64 and 1024, since nats-server v2.15.0 syncs each message of a
  stream in turn (`server/filestore.go:8777-8779`). Periodic sync at a
  100 ms interval measured about 42,000 records/s, and throughput stays flat
  from there up. The user decided on 2026-10-15 that the hub moves to
  periodic sync at 100 ms, which amends the device service record's
  power-loss rule. That change has not landed. Target-hardware rates are
  unverified, since Go syncs with `F_FULLFSYNC` on darwin.
- Whether the process-local bus stays. It has no consumer in either host.
- Whether an adapter host, the second kind of enrolled host `GOALS.md`
  names, delivers through the same ingest call. The tenant no longer
  comes from a stream, but central resolves it from the edge store, which
  holds Edges only.

## Sources

- Repository: `src/modules/edgebus/README.md`, `src/modules/edgebus/keys.go`,
  `src/modules/edgebus/forwarder.go`,
  `src/edge/agent/internal/busattach/busattach.go`,
  `src/services/device/internal/host/host.go`,
  `spec/proto/flowseer/edge/attach/v1/bus.proto`,
  `spec/proto/flowseer/edge/audit/v1/audit_service.proto`,
  `src/common/service/bus.go`.
- `nats-server` v2.15.0 at `go.mod`'s version: `server/filestore.go`.
- Go module proxy, `https://proxy.golang.org/<module>/@latest`, read
  2026-10-04.
- Grafana Alloy `prometheus.remote_write`:
  <https://grafana.com/docs/alloy/latest/reference/components/prometheus/prometheus.remote_write/>.

## Amendments

### 2026-10-15: central handlers landed

Landed 2026-10-15: `src/services/device/internal/intake/handler.go`,
`src/services/device/internal/otlpapi/handler.go`,
`src/modules/edgebus/collector.go`, and the two `Deliver` calls under
`spec/proto/flowseer/edge/`.

- Both calls mount on central's edge listener behind the assertion
  middleware. The telemetry call mounts whether or not a collector is
  configured, so an edge never mistakes it for an older central.
- No request carries an item count bound. The edge listener's 1 MiB body
  bound limits the work, and one 512 KiB segment's records always fit one
  call. Each item is validated by its handler, so one invalid item is
  refused alone instead of refusing the batch.
- A record stays at most 256 KiB. The loopback receiver accepts OTLP bodies
  up to 255 KiB, and a telemetry item's body is bounded at 261120 bytes, so
  a body with its envelope fits one record.
- The telemetry call settles a body the collector accepted or refused for
  its content (a 4xx other than 401, 403, 404, 408, and 429). Any other
  answer ends the count, and no delivery backstop applies, so a body waits
  in the edge's log until a collector accepts it or the log's bounds expire
  it. With no collector configured the call settles nothing.
- The forwarder and the telemetry call classify a collector's answer through
  one `edgebus.Collector`. Both refuse to start on an endpoint that is not an
  absolute `http` or `https` URL.

### 2026-10-16: agent switch landed

Landed 2026-10-16: `src/edge/agent/internal/drain`,
`src/edge/agent/internal/otlpreceiver`, `src/common/tlspin`,
`src/edge/agent/host/delivery.go`, and `AgentLogBounds` in
`spec/proto/flowseer/store/agent/v1/agent_config.proto`. The agent starts
no leaf node and no longer calls `AttachBus`.

- The agent's configuration splits its buffer bounds per log.
  `AgentBuffer` holds an `ingest` and a `telemetry` `AgentLogBounds`, each
  with `max_bytes` and `max_age`. Unset fields default to 192 MiB and 7 days
  for ingest and 64 MiB and 7 days for telemetry, which keeps the 256 MiB
  total the leaf's stream had. The floors are 1 MiB and 20 minutes, the
  smallest bounds `seglog.Open` accepts. A configuration with the old flat
  `buffer { max_bytes }` no longer loads.
- Each log has one drain. It sends one segment per call, resends a partly
  settled segment from its start after a restart, and backs off from 1 s to
  30 s without jitter. It drops a record only when the log's bounds expire
  it or the record cannot be decoded into the call's item type.
- Each drain runs under its own supervisor branch with an error budget no
  storage fault exhausts, so a segment that keeps failing to read holds back
  only its own log (`src/edge/agent/host/delivery.go`).
- The loopback OTLP receiver moved from `src/modules/edgebus` into the
  agent, and the TLS pin helpers moved to `src/common/tlspin`, since central
  and the agent both use them.
- An upgraded agent leaves its old `jetstream` directory and `hub.creds` on
  disk. Nothing removes them.

### 2026-10-16: central bus attachment removed

Landed 2026-10-16: `spec/proto/flowseer/edge/attach/v1/bus.proto` and
`EdgeService.AttachBus` are removed, along with `ServiceListeners.bus` and
`EdgeEnrollmentPolicy.cluster_urls` in
`spec/proto/flowseer/store/device/v1/service_config.proto`. Configurations
that still name either field fail to load. The hub binds no WebSocket or
leaf port.

The removed implementation paths are `src/modules/edgebus/leaf.go`,
`src/modules/edgebus/follower.go`, `src/modules/edgebus/forwarder.go`, and
`src/services/device/internal/intake/intake.go`. `Handler.Deliver` and
`prepareRecord` in `src/services/device/internal/intake` are the ingest
path. `src/services/device/internal/otlpapi/handler.go` uses
`edgebus.Collector` directly. The 2026-10-15 amendment's shared forwarder
classification now belongs to that telemetry handler alone.

`StartHub` in `src/modules/edgebus/hub.go` keeps operator mode and the
operator, system, and CENTRAL seeds, with CENTRAL as its only data account.
`hubKeys.mintUser` in `src/modules/edgebus/keys.go` still creates central's
user when it connects. No per-edge key files or accounts are created.

An upgraded central leaves `keys/edge-*.nk`, `keys/edge-*.tenant`, and
former edge accounts' JetStream directories on disk. Nothing reads the
stray keys. At startup nats-server v2.15.0 scans the store, fails to resolve
each former account, logs one `Account fetch failed` warning per directory,
and continues serving CENTRAL (`server/jetstream.go:933-947`,
`server/server.go:2183-2185`). The hub forwards the warning through
`quietLogger.Warnf`. `TestHubServesCentralWithFormerEdgeState` in
`src/modules/edgebus/hub_internal_test.go` checks this behavior and leaves
the files intact. Operators remove the stray files as described in the
[edgebus README](../../src/modules/edgebus/README.md#keys-and-upgrades).
There is no automatic removal because a mistaken directory deletion could
destroy CENTRAL's store.

The device service and ingestion records now have dated amendments naming
each replaced transport passage. The proposed central high availability
record makes central a plain NATS client, and the deployment record exposes
one edge-facing address. `GOALS.md` now states Connect delivery and no
longer says intake reads per-edge streams. These edits supersede the
Consequence that tied that goal edit to acceptance of this record. Its
status remains proposed pending acceptance.

The edge high availability record needed no edit: it already contains no
leaf attachment or FreeBSD leaf-node open question. This answers the
Consequence that called for removing that question.
