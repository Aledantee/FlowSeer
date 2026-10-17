---
title: Central High Availability - Direction
type: direction
date: 2026-10-03
topic: central-high-availability
status: proposed-direction
amends: docs/architecture/2026-08-20-device-service-and-inventory-direction.md
---

# Central High Availability - Direction

What it means for a central component to be ready for high availability, and
the three changes the device service needs to get there.

## Context

Two facts in the tree keep central at one process:

- **The hub is inside central.** The device service embeds a NATS server in
  operator mode with an in-memory account resolver
  (`src/modules/edgebus/hub.go`, `StartHub`). Its JetStream store, signing
  keys, and TLS pair live in `state_dir`. `src/modules/edgebus` has no cluster
  or route configuration, so a second central replica cannot join the first.
- **Background work assumes one process.** The drift poll, read sweeper,
  and capture artifact sweeper each run as a module of the one host
  (`src/services/device/README.md`).

The [device service record](2026-08-20-device-service-and-inventory-direction.md)
keeps a single-node hub "until" a deployment plan decides replicas. The
[ingestion record](2026-10-02-central-ingestion-pipeline-direction.md)
assembles its modules "into the process that runs the hub".

## Decision

### Every central component is ready for high availability

No central component depends on a single process staying up. Ready means
four things for a component:

- it runs correctly as two or more replicas, with no work done twice and no
  state on one replica that another needs,
- it reports readiness, so a replica that cannot serve takes no traffic,
- losing one replica loses no acknowledged write,
- a deployment can run it replicated and spread across nodes.

A deployment may still run one replica of anything. That is a choice of the
deployment and never a limit of the component.

### NATS leaves the device service

The hub becomes an upstream `nats-server` cluster, and central is a client of
it. Central is a plain client of the cluster, using centrally provisioned
credentials for CENTRAL. It neither mints per-edge accounts nor pushes
claims to the servers. Cluster operators own the server accounts and keys.

Central creates its streams and key-value buckets with a replica count its
configuration names. Every server sets `sync_interval: always`, the
hardening the device service record makes mandatory.

Edges deliver records and telemetry through Connect on central's edge
listener. The NATS cluster has no edge-facing WebSocket listener.

This amends the device service record's single-node hub and the ingestion
record's "process that runs the hub". The ingestion modules already take a
JetStream handle from their host, so they take the remote one.

### Central runs as several replicas

With the hub gone, central holds no store of its own. Its TLS pair and
NATS client credentials come from files every replica shares. Its journal
is in JetStream key-value buckets it writes with compare-and-set.

Two things in the tree make this plausible and neither proves it. What
central owes an edge is a pure function of the lane record, "so any replica
computes the same set after any restart" (`src/services/device/README.md`).
And the one-writer-per-device rule rests on compare-and-set. The background
modules are the open part: each runs in every replica unless one replica is
elected to run it.

Central gains a readiness endpoint in the same change.

### The edge-facing services get their own listener

`ServiceListeners` gains an `edge` address. The edge listener serves
`EdgeService`, `DispatchService`, `AuditService`, and `CaptureEdgeService`.
The API listener serves the operator services and nothing an edge calls. Both
serve the same certificate.

A deployment can then expose the edge listener and keep the operator API
private, with TLS passed through unchanged so an edge keeps pinning central's
key. TLS termination at an ingress would present a different key and is
incompatible with the pin.

Landed 2026-10-13: `ServiceListeners.edge` in
`spec/proto/flowseer/store/device/v1/service_config.proto`, required, with
`EdgeEnrollmentPolicy.central_url` naming it. One module in
`src/services/device/internal/host` binds both HTTP listeners before serving
either and shuts them down within one grace. Services added after this record follow the same rule:
`TenantService`, `TenantAdminService`, and `RawWindowService` are on the API
listener, and `RawWindowEdgeService` is on the edge listener.
Validation accepts a port of 0 for `edge`, which leaves edges
dialing a port nothing serves, so the fixed port is the operator's to
choose.

## Alternatives

- **A FlowSeer hub service that embeds `nats-server`.** Account minting would
  stay in-process. Rejected because FlowSeer would then own the cluster
  configuration and upgrades that upstream already handles.
- **Keeping the hub in central with one replica.** No code change. Rejected
  because central would remain coupled to one local store.
- **One central replica after the split.** It avoids the work on background
  modules. Rejected because central would stay a single point of failure for
  every edge RPC.
- **A path allowlist at an L7 ingress in place of a second listener.** It
  needs no code change. Rejected because device credentials would then
  depend on ingress configuration, and edges would pin an ingress key that
  has to survive every renewal.

## Consequences

- `src/modules/edgebus` changes from starting a server to connecting to a
  remote one. `StartHub`, the in-memory resolver, and the store ceiling
  arithmetic go away or move into server configuration. The CENTRAL disk
  budget solution needs a refresh.
- The lab run needs a `nats-server` process beside central.
- `DeviceServiceConfig` changes shape: an edge address, a NATS URL and
  credentials, a stream replica count, and no hub store. Nothing external
  consumes it.
- CENTRAL's disk budget is a reservation against the cluster's store.

## Open questions

- Whether an edge's dispatch stream, held by one replica, sees a mutation
  another replica admitted without shared memory. Unverified, and it decides
  how much new code several replicas need.
- Whether captured artifacts live on local disk. The capture artifact sweeper
  suggests they do, and replicas would then need shared storage.

## Sources

- Repository: `src/services/device/README.md`,
  `src/services/device/internal/host/serve.go`,
  `spec/proto/flowseer/store/device/v1/service_config.proto`,
  `spec/proto/flowseer/model/edge/v1/provisioning.proto`,
  `src/modules/edgebus/README.md`, `src/modules/edgebus/hub.go`.
- `nats-server` v2.15.0 at `go.mod`'s version: `server/opts.go`
  (`sync_interval`).
