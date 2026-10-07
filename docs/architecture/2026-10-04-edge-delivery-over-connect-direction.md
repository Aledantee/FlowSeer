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
  states, thousands of messages per second with tenfold bursts. Unverified.
  The hub fsyncs every message, which bounds central's publish rate today
  as well.
- Whether the process-local bus stays. It has no consumer in either host.
- How the agent's configuration splits its buffer bounds between the two
  logs.
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
