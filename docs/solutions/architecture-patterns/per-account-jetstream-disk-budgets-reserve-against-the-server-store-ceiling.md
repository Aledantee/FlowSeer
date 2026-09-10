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
  - "edgebus returns 'wait for the edge account JetStream' with ErrCodeHub for the second or later edge, while the first edge attached without complaint"
  - "The embedded server logs 'Error configuring jetstream for account [...]: insufficient storage resources' rather than anything about being out of disk"
  - "A single-edge test suite is green and the failure appears only once a second edge attaches to the same hub"
root_cause: "nats-server sums the JetStream DiskStorage limit of every enabled account and refuses to enable JetStream for an account whose limit would push that sum past JetStreamMaxStore. The budgets are reservations taken at account-enable time, not usage measured at write time, so a set of budgets that adds up to the store ceiling denies the next account outright."
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
(`src/modules/edgebus/keys.go:177-191`):

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
(`src/modules/edgebus/hub.go:189`), and `StartHub` already records why it is
not sized to cover every account (`hub.go:142-147`):

```go
// JetStreamMaxStore is a server-wide backstop; zero lets the server size
// it against the available disk. The per-account disk budgets below are
// the real guard: each account is limited to its own budget, so no
// number of edges can consume the store the journal writes into. A
// reserved MaxStoreBytes would have to exceed central plus every edge's
// budget at once, which an unbounded edge count cannot promise, so it
// stays a backstop rather than a reservation.
```

That comment is right about the intent and about why `MaxStoreBytes` is left at
zero in normal operation. What it does not say, and what this document exists to
record, is what happens to a deployment that does set it.

## Guidance

`DiskStorage` in an account JWT is a reservation the server takes when it
enables JetStream for that account, not a quota it enforces against bytes
actually written. `sufficientResources` sums `MaxStore` across every
JetStream-enabled account and refuses the new one when the sum would cross the
configured ceiling (nats-server v2.14.1, `server/jetstream.go:2594-2645`,
reached from `configJetStream` at `jetstream.go:797-820`):

```go
if storeReserved+totalMaxStore > js.config.MaxStore {
	return NewJSStorageResourcesExceededError()
}
```

The refusal does not fail the connection and does not surface as an
out-of-space error. `Server.updateAccountClaimsWithRefresh` logs the failure and
marks the account `incomplete` (`server/accounts.go:3805-3814`), so the account
exists, the credential authenticates, and every JetStream call inside it comes
back with `jetstream not enabled` (error code 10076). In edgebus that lands in
`waitForJetStream` (`src/modules/edgebus/hub.go:286-304`) and reaches the caller
as `wait for the edge account JetStream` with `ErrCodeHub` and the edge id
attached.

Three consequences follow, and only the first is obvious.

**Budgets are additive against the ceiling, so the arithmetic must leave room
for the next account.** The hub's defaults are `defaultCentralBudget` at 512 MiB
and `defaultEdgeBudget` at 128 MiB per edge (`hub.go:111-116`). A hub configured
with `MaxStoreBytes: 640 << 20` therefore has room for exactly one edge:
512 + 128 reaches the ceiling but does not cross it, and the second edge's
128 MiB does. Set a ceiling at all and it has to exceed central plus the
per-edge budget times the largest edge count the deployment will ever reach.

**The failure lands on the newest account, not the largest one.** Whichever
account is enabled last is the one refused, regardless of which account's budget
made the sum too large. An operator reading only the edgebus error sees the edge
that happened to attach at the wrong moment, so the first place to look is the
sum of every budget on the hub, not that edge.

**A single-edge test cannot find it.** One edge plus central is the arithmetic
that fits under almost any ceiling; the trap needs a second account. Anything
that changes a budget or the ceiling has to be exercised with at least two
edges attached to the same hub.

## Why This Matters

The failure mode is the wrong shape for its cause, which is what makes it cost
time. A budget overrun reads as "this account has no JetStream", so the search
starts at credentials, account resolution, and the JWT — the parts that in fact
worked. Nothing in the message names disk, the store, or a limit. The
`insufficient storage resources` line that does name it goes to the server's own
logger, which `quietLogger` forwards to `HubConfig.Logger` (`hub.go:611-625`); a
hub started without a logger loses the only sentence that points at the real
cause.

The default configuration is safe by construction. `MaxStoreBytes` at zero lets
the server size the ceiling from available disk, so the per-account budgets
stay well under it and the edge count is bounded by disk rather than by
arithmetic. The trap is reachable only by a deployment that pins the ceiling to
a specific number, which is exactly what a container with a fixed volume invites
someone to do.

## When to Apply

- Changing any budget constant or `HubConfig` budget field: add up central plus
  the per-edge budget times the target edge count and compare it against the
  ceiling the deployment sets, not against the disk.
- Diagnosing `jetstream not enabled` on a hub account: check
  `hub.AttachedEdges()` and the budget sum before looking at keys or the account
  resolver. The credential is not the problem.
- Adding a JetStream-enabled account to the hub for anything other than an
  edge: its budget joins the same sum, and it will push whichever account is
  enabled after it over the line.
- Writing a capacity note: a hub with a pinned ceiling holds
  `(ceiling - central budget) / edge budget` edges, and edge number N+1 fails to
  attach rather than filling the disk.
- Reading `HubConfig`'s own field comments on `MaxStoreBytes`,
  `EdgeBudgetBytes` and `CentralBudgetBytes`: the defaults they name are the
  fixed constants `defaultCentralBudget` and `defaultEdgeBudget`, not a share
  of `MaxStoreBytes`, which is passed to the server as given and left at zero
  in normal operation.

## Examples

The behavior was reproduced against the current tree with a hub whose ceiling
equals central's budget plus one edge's, run through `go test -overlay` so that
no file entered the package:

```go
hub, err := edgebus.StartHub(context.Background(), edgebus.HubConfig{
	StateDir:      t.TempDir(),
	FsyncPolicy:   service.BusFsyncPeriodic,
	MaxStoreBytes: 640 << 20,
})
if err != nil {
	t.Fatalf("start hub: %v", err)
}
defer hub.Close()
for _, id := range []string{"edge-a", "edge-b"} {
	t.Logf("attach %s: %v", id, hub.AttachEdge(context.Background(), id))
}
```

```
attach edge-a: <nil>
attach edge-b: wait for the edge account JetStream: nats: API error: code=503 err_code=10076 description=jetstream not enabled
```

The first edge takes the reservation to 640 MiB, which is the ceiling and not
past it. The second edge is refused before it ever writes a byte, and the
edgebus error names JetStream rather than storage.

## Related

- `src/modules/edgebus/README.md`: the account layout and what each account
  owns.
- `docs/solutions/architecture-patterns/local-bus-durability-is-a-runtime-setting-not-storage-identity.md`:
  the fsync rule the hub's embedded server shares with the local bus, and the
  same lesson that a JetStream setting's real home is the server option rather
  than the place it appears to belong.
