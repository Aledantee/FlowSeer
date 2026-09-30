---
title: A Per-Account JetStream Disk Budget Is a Reservation Against the Server's Store Ceiling
date: 2026-09-10
category: architecture-patterns
module: src/modules/edgebus
problem_type: architecture_pattern
component: messaging
severity: high
symptoms:
  - "Creating a stream or calling AccountInfo in a newly minted account fails with 'nats: API error: code=503 err_code=10076 description=jetstream not enabled'"
  - "edgebus returns 'storage limit exceeded' with ErrCodeStorage when the server cannot fit an edge account's budget under the store ceiling, while other wait failures retain ErrCodeHub"
  - "The embedded server logs 'Error configuring jetstream for account [...]: insufficient storage resources available' rather than anything about being out of disk"
  - "A single-edge test suite is green and the failure appears only once a second edge attaches to the same hub"
root_cause: "nats-server checks account JetStream limits in sufficientResources and refuses to enable an account whose budget would push the reservation past JetStreamMaxStore (server/jetstream.go:2629-2681). CONNECT finishes account setup before it returns, so a successful lookup whose JetStreamEnabled flag is false identifies that refusal (server/server.go:2094, server/jetstream.go:2075-2084). A failed stream setup can leave an enabled account behind even though edgebus has not added the edge to h.edges, so a later canceled attach must keep ErrCodeHub."
applies_when:
  - "Changing MaxStoreBytes, EdgeBudgetBytes, CentralBudgetBytes, defaultCentralBudget or defaultEdgeBudget in src/modules/edgebus/hub.go"
  - "Adding a third JetStream-enabled account to the hub, or giving an existing account a JetStream disk limit it did not have"
  - "Sizing an edge budget against a fixed disk, or deciding whether a deployment should set MaxStoreBytes at all"
  - "An edge attach fails with 'jetstream not enabled' while an earlier edge on the same hub attached fine"
  - "Writing a capacity or sizing note that says the hub can hold N edges on a given disk"
  - "Setting JetStreamLimits.DiskStorage on any account JWT signed by hubKeys.accountJWT"
related_components:
  - messaging
  - service_layer
  - testing_framework
tags: [jetstream, nats-account-limits, disk-budget, capacity-planning, multi-tenant, edgebus, scaling-failure]
---

# A Per-Account JetStream Disk Budget Is a Reservation Against the Server's Store Ceiling

## Context

The hub runs one embedded nats-server in operator mode with an account per
isolation boundary: `CENTRAL` for the journal buckets and the audit stream, and
`EDGE_<id>` for each edge's source stream. Each account's JWT carries a
JetStream disk limit, minted in `hubKeys.accountJWT`
(`src/modules/edgebus/keys.go:244-251`):

```go
if diskBytes > 0 {
	claims.Limits.JetStreamLimits = jwt.JetStreamLimits{
		MemoryStorage: jwt.NoLimit,
		DiskStorage:   diskBytes,
		Streams:       jwt.NoLimit,
		Consumer:      jwt.NoLimit,
	}
}
```

The server-wide ceiling is set once, from `HubConfig.MaxStoreBytes`
(`src/modules/edgebus/hub.go:46-52`), and `StartHub` records how account budgets
reserve against it (`src/modules/edgebus/hub.go:153-158`):

```go
// JetStreamMaxStore is the server store ceiling. Zero is finalized once
// at server start as 75% of free disk. Account budgets reserve against
// this ceiling when each account is enabled, so the edge count is
// capped by (ceiling - central budget) / edge budget. Each account is
// limited to its own budget so telemetry in an edge account cannot
// consume the store the journal writes into.
```

When `MaxStoreBytes` is left at zero, the server derives the ceiling dynamically
from available disk. When a deployment sets `MaxStoreBytes`, that number fixes
the ceiling directly.

## Guidance

`DiskStorage` in an account JWT is a reservation the server verifies when it
enables JetStream for that account. It is also an account write limit. In
nats-server v2.14.6 (`server/jetstream.go:2629`), `sufficientResources`
performs two distinct storage checks:

1. Stream reservations (`server/jetstream.go:2659-2661`): it checks
   `js.storeReserved + totalMaxStore > js.config.MaxStore`. Here `js.storeReserved`
   accumulates stream `MaxBytes` limits reserved across streams. This stream
   reservation is what `Server.JetStreamReservedResources()`
   (`server/jetstream.go:1146-1154`) exposes.
2. Account budget summation (`server/jetstream.go:2664-2681`): it iterates over
   `js.accounts`, summing each account's `MaxStore` limit into a local
   `storeReserved` variable. If `storeReserved + totalMaxStore > js.config.MaxStore`,
   it returns `NewJSStorageResourcesExceededError()` (error code 10047,
   "insufficient storage resources available").

Both checks are guarded by `recovering := js.config.maxStorePending`
(`server/jetstream.go:2658`). While `maxStorePending` is true, the server skips
the storage checks.

After an account is enabled, JetStream checks the account's current usage plus
the encoded message size against its `MaxStore` limit
(`server/jetstream.go:2444-2479`). The server therefore uses the same JWT field
for both the account-enable reservation and the write-time quota.

When account enablement fails, the server does not close the client connection.
Instead, `Server.updateAccountClaimsWithRefresh` logs the failure via
`s.Errorf("Error configuring jetstream for account [%s]: %v", ...)` and marks the
account `incomplete = true` (`server/accounts.go:3912-3917`). The account
exists and authenticates, but any JetStream request inside it returns
`jetstream not enabled` (error code 10076).

In edgebus, `ensureEdgeAccount` looks up the account immediately after
`connectAccount` and reads `Account.JetStreamEnabled`
(`src/modules/edgebus/hub.go:447-451`). CONNECT enables JetStream for the
authenticated account before it returns. A successful lookup with a false
flag therefore records a server refusal. When the wait then
fails, the hub returns `ErrCodeStorage` with the `edge`,
`central_budget_bytes`, and `edge_budget_bytes` attributes, plus
`ceiling_bytes` when `srv.JetStreamConfig()` is non-nil
(`src/modules/edgebus/hub.go:453-467`). A lookup failure, an enabled account,
or a connection failure keeps `ErrCodeHub`. The attach holds the hub's read
lock from `connectAccount` through the flag read, and `Close` takes the write
lock before shutdown. This prevents shutdown from clearing the flag during the
decision. `errs.From(err)` keeps the wait error in the returned chain.

```go
storage limit exceeded: edge budget <edge> B does not fit under store ceiling <ceiling> B
```

The hub does not pre-check the budget sum before attempting to attach. The
server owns the account-enable decision, so the hub checks the flag on the
account it just connected. A failed stream setup leaves that account enabled
even though `h.edges` does not contain it. A retry therefore reports the wait
failure as `ErrCodeHub`, not storage. The attach's read lock prevents
`Hub.Close` from shutting down the server between the connect and the flag
read. The wait remains after the flag read for the account API to become usable
before stream creation.
`StartHub` waits for server readiness before it connects the central account or
re-attaches persisted edges (`src/modules/edgebus/hub.go:238-256`).

### The frozen ceiling and restart caveat

When `MaxStoreBytes` is zero, nats-server initializes `jsc.MaxStore` using
`diskAvailable(jsc.StoreDir)` (`server/jetstream.go:2763`). The disk
availability helper (`server/disk_avail.go:23-37`) probes the filesystem via
`statfs` and sets the ceiling to 75% of free space, marking
`jsc.maxStorePending = true`.

Once existing file-based streams are recovered, `finalizeDynamicMaxStore`
(`server/jetstream.go:546-565`) clears `maxStorePending` and adds back 75% of the
recovered stream bytes (`atomic.LoadInt64(&js.storeUsed)`). This keeps the
ceiling stable across restarts when only JetStream uses the disk.

The caveat is that this dynamic ceiling is frozen at startup. It does not track
filesystem changes while running. If external files or logs consume disk space
while the hub is stopped, `diskAvailable` calculates 75% of the newly reduced
free space on the next start. The resulting ceiling will be lower. If previously
attached edge accounts and their budgets exceed this lower ceiling, restoring
those accounts on restart will fail with account-enable storage errors.

Three operational rules follow:

**Budgets are additive against the ceiling, so the arithmetic must leave room
for the next account.** The hub's defaults are `defaultCentralBudget` at 512 MiB
and `defaultEdgeBudget` at 128 MiB per edge (`src/modules/edgebus/hub.go:126-127`). A hub configured
with `MaxStoreBytes: 640 << 20` has room for exactly one edge: 512 + 128 reaches
the ceiling without crossing it, and another edge's 128 MiB does not fit under
the remaining capacity. Any configured ceiling must cover the central budget
plus the per-edge budget times the largest edge count the deployment expects.

**The refusal belongs to the account the server just checked.** The hub reads
that account's `JetStreamEnabled` flag instead of inferring refusal from
`h.edges` or an account count (`src/modules/edgebus/hub.go:447-467`). A failed
stream setup leaves the account enabled even though it is not inserted into
`h.edges`, so a retry is a hub wait failure rather than a storage refusal
(`src/modules/edgebus/storage_test.go:124-149`).

**A fresh canceled attach must not find it.** One edge plus central fits under
the 640 MiB ceiling, so a canceled attach on a fresh hub reports `ErrCodeHub`
and a retry succeeds. The refusal boundary and the failed-attach reservation
case are covered at `src/modules/edgebus/storage_test.go:32-100`. The fresh
hub and retry cases are covered at `src/modules/edgebus/storage_test.go:103-149`.
The close race is covered at `src/modules/edgebus/storage_test.go:152-196`.

## Why This Matters

Without inspecting the account's `JetStreamEnabled` flag after `CONNECT`, an
account budget refusal appears as a generic "jetstream not enabled" error. The
hub uses that flag together with a successful account lookup
to identify a server refusal. `srv.JetStreamConfig().MaxStore` supplies the
ceiling value in the resulting message. The underlying cause is an arithmetic
budget ceiling rather than credential or resolver issues.

The server's internal refusal log ("insufficient storage resources available")
is captured by `quietLogger` and forwarded to `HubConfig.Logger`
(`src/modules/edgebus/hub.go:651-664`). By checking the account's enablement
flag, `AttachEdge` reports the storage limit only when the server refused that
account. The attach and `Hub.Close` share the hub mutex, so shutdown cannot
clear the flag between `connectAccount` and the lookup.

## When to Apply

- Changing any budget constant or `HubConfig` budget field: add up central plus
  the per-edge budget times the target edge count and compare it against the
  ceiling the deployment sets.
- Diagnosing `jetstream not enabled` or storage limit errors on a hub account:
  check the account's enablement flag, recent attach failures, and the budget
  sum before looking at keys or the account resolver
(`src/modules/edgebus/hub.go:447-467`).
- Adding a JetStream-enabled account to the hub: its budget joins the same sum
  and can push subsequent accounts over the ceiling.
- Capacity planning: a hub with a store ceiling holds
  `(ceiling - central budget) / edge budget` edges, and edge number N+1 fails to
  attach rather than filling the disk.
- Accounting for disk drift across restarts: verify that external processes on
  the host volume cannot reduce free space to the point where the startup
  `diskAvailable` calculation drops below the total budget requirement.

## Examples

The behavior is verified in `src/modules/edgebus/storage_test.go:32-49` with a hub whose ceiling equals
central's budget plus one edge's budget:

```go
hub, err := edgebus.StartHub(context.Background(), edgebus.HubConfig{
	StateDir:           t.TempDir(),
	FsyncPolicy:        service.BusFsyncPeriodic,
	MaxStoreBytes:      640 << 20,
	CentralBudgetBytes: 512 << 20,
	EdgeBudgetBytes:    128 << 20,
})
if err != nil {
	t.Fatalf("start hub: %v", err)
}
defer hub.Close()

// First edge attaches successfully (512 MiB + 128 MiB = 640 MiB).
_ = hub.AttachEdge(context.Background(), "edge-a")

// Second edge is refused because its 128 MiB budget does not fit under the
// remaining capacity beneath the 640 MiB ceiling.
err = hub.AttachEdge(context.Background(), "edge-b")
```

The second attach fails with:

```
storage limit exceeded: edge budget 134217728 B does not fit under store ceiling 671088640 B: nats: API error: code=503 err_code=10076 description=jetstream not enabled
```

The error carries `edgebus/storage` and names the edge budget and the ceiling.

## Related

- `src/modules/edgebus/README.md`: the account layout and what each account
  owns.
- `src/modules/edgebus/storage_test.go`: tests asserting storage limit errors
  only for server-refused accounts.
- `docs/solutions/architecture-patterns/local-bus-durability-is-a-runtime-setting-not-storage-identity.md`:
  fsync rules and embedded JetStream server configuration.
