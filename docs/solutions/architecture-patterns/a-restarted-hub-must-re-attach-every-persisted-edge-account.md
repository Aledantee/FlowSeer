---
title: A Restarted Hub Must Re-Attach Every Persisted Edge Account, or Its Leaves Cannot Authenticate
date: 2026-09-10
category: architecture-patterns
module: src/modules/edgebus
problem_type: bug
component: messaging
severity: high
symptoms:
  - "An edge's leaf reconnects to a restarted hub and is rejected: the server logs 'Account fetch failed: account missing' followed by 'authentication error' and 'read error: authentication error', once per reconnect attempt, forever"
  - "Hub.LeafCount() stays at zero after a restart even though the leaf is dialling and the credential has not been rotated"
  - "Hub.AttachedEdges() returns an empty list on a hub whose keys directory holds edge-<id>.nk files"
  - "Everything works until the hub process is restarted; the edge recovers only if central re-issues an attach"
root_cause: "The hub resolves accounts through an in-memory server.MemAccResolver, so an account exists only for as long as some code path has stored its JWT into that resolver. The per-edge account key persists on disk and the credential minted from it stays cryptographically valid across a restart, but a restarted hub that does not re-store each edge's account JWT leaves the server unable to fetch the account the leaf's user JWT names, and authentication fails before any permission is consulted."
resolution_type: code_fix
applies_when:
  - "Changing StartHub's startup sequence in src/modules/edgebus/hub.go, especially anything between createStores and the return"
  - "Adding state to the hub that is minted once per edge and is expected to outlive the process"
  - "Changing hubKeys.persistedEdgeIDs, the edge-<id>.nk naming, or where per-edge keys are written"
  - "Replacing server.MemAccResolver with a directory or URL account resolver"
  - "A leaf that authenticated yesterday is rejected today with an authentication error and an unrotated credential"
  - "Deciding whether one edge's broken state may abort hub startup for every other edge"
  - "Reviewing a test suite for a component that mints long-lived credentials, and finding no test that restarts it"
related_components:
  - messaging
  - service_layer
  - testing_framework
tags: [nats-accounts, jwt, account-resolver, restart, credential-lifetime, leaf-node, edgebus, startup-sequence]
---

# A Restarted Hub Must Re-Attach Every Persisted Edge Account, or Its Leaves Cannot Authenticate

## Context

The hub mints one nats account per edge, and the credential it hands back is
long lived: `MintEdgeUser` returns a user JWT and seed the edge writes to a
`.creds` file and keeps using (`src/modules/edgebus/hub.go:494-500`). The
account key behind it is written to disk on first use and read back afterwards,
which `hubKeys` states as its own rule (`src/modules/edgebus/keys.go:79-81`):

```go
// edgeAccountKey returns the account key for one edge, creating and
// persisting it on first use so a restarted hub signs the same account and
// every credential minted under it stays valid.
```

That is true as far as it goes, and it is the half that misleads. The key
persists, the signature still verifies, and the credential is in every sense
still valid. What does not persist is the server's knowledge that the account
exists. The hub resolves accounts through `server.MemAccResolver`
(`hub.go:158`), an in-memory map: the system and central accounts are stored
into it during startup (`hub.go:172`), and an edge account is stored only by
`ensureEdgeAccount` (`hub.go:415`). A fresh process starts with an empty map.

## Guidance

Persisted key material and a live account are two different pieces of state, and
only one of them survives a restart. Anything the hub mints once per edge and
expects to keep working has to be rebuilt from disk during startup, before the
edge's next reconnect arrives. `StartHub` does exactly that
(`hub.go:239-252`):

```go
// Re-attach every edge whose account key persisted, so a restart
// restores the accounts and source streams the leaves reconnect into.
edgeIDs, err := keys.persistedEdgeIDs()
if err != nil {
	return nil, err
}
for _, edgeID := range edgeIDs {
	if _, err := hub.ensureEdgeAccount(ctx, edgeID); err != nil {
```

The disk is the enumeration source. `persistedEdgeIDs` lists the keys directory
and takes every `edge-<id>.nk` file as an edge to restore
(`keys.go:62-77`), so the set of edges the hub serves after a restart is the set
it ever minted a key for. No separate registry has to be kept in sync, and
central does not have to re-issue anything for an edge that was already
attached.

One edge's failure must not become every edge's failure. The loop warns and
continues rather than aborting startup, and the reason is written where the
decision is (`hub.go:247-249`):

```go
// One edge's stored key must not stop the hub. A file that
// cannot be turned into an account is a fact about that edge,
// and refusing to start leaves every other edge unserved for it.
```

The warn carries `flowseer.edge.bus.account_skipped` and the edge id, which is
the only signal that one edge is now permanently unable to connect while the
hub looks healthy.

Only a restart test finds any of this. Every assertion that matters here holds
on a hub that has never restarted: the credential is valid, the account resolves,
the leaf links, sourcing flows. The whole defect lives in the gap between two
processes, so a suite that starts one hub per test cannot reach it no matter how
many behaviors it covers.

## Why This Matters

The failure is silent on the side that can fix it and permanent on the side that
cannot. The hub starts cleanly, reports no error, and serves central normally.
The edge dials, is rejected, backs off, and dials again, and the credential it
holds will never work again no matter how long it retries — a rotation the edge
cannot initiate is the only way out. Removing the re-attach loop and running the
existing restart flow reproduces it:

```
attached edges after restart: []
...
Account fetch failed: account missing
127.0.0.1:53986 - lid_ws:19 - authentication error
127.0.0.1:53986 - lid_ws:19 - read error: authentication error
```

The three lines repeat once a second, one set per reconnect attempt. They reach
an operator only through `HubConfig.Logger`, since the embedded server runs with
`NoLog: true` and `quietLogger` forwards its diagnostics to the host logger
(`hub.go:611-625`). A hub started with a nil logger discards them, and the only
remaining evidence is a leaf count that never rises.

`Account fetch failed: account missing` is the wire's way of saying the resolver
had no entry, and it names neither the edge nor the account key. Read without
this context it points at the credential, which is the one thing that is
provably fine.

## When to Apply

- Adding startup work to `StartHub`: anything that reconstructs per-edge state
  belongs after `createStores` and before the return, alongside the re-attach
  loop, so it is in place before the first leaf reconnects.
- Introducing per-edge state that is created on first attach: ask what rebuilds
  it on the next process start. If the answer is "central will call attach
  again", that is a dependency on central being up and knowing to do so.
- Swapping the account resolver: a directory or URL resolver changes where the
  account JWT lives but not the fact that the account must be present before the
  leaf's first reconnect. The re-attach loop stays the thing that guarantees it.
- Renaming or relocating the per-edge key file: `persistedEdgeIDs` matches on
  the `edge-` prefix and the `.nk` suffix, and a rename that misses it silently
  empties the restore set while leaving every other test green.
- Reviewing a component that mints credentials meant to outlive the process:
  look for a test that stops it and starts it again on the same state
  directory. If there is none, the restart path is unverified.

## Examples

`TestRecordsPublishedWhileTheHubIsDownArriveAfterReconnect`
(`src/modules/edgebus/edgebus_test.go:273-309`) is the test that covers this.
Its subject is the buffered record, but the assertion that pins the re-attach is
the one before it: the second hub is started on the same state directory and the
same port, nothing calls `AttachEdge` on it, and the leaf has to come back on
its own.

```go
first.Close()
waitFor(t, "link down", 10*time.Second, func() bool { return !leaf.HubConnected() })
...
second := startHub(t, hubDir, port)
waitFor(t, "link up again", 20*time.Second, func() bool { return second.LeafCount() == 1 })
```

With the re-attach loop removed, `second.AttachedEdges()` is empty and
`LeafCount()` never leaves zero; the test fails at `link up again` rather than
at the record it is named for. That is worth knowing before diagnosing it as a
sourcing or forwarder problem.

`TestLaneBucketSurvivesRestart` (`edgebus_test.go:332-356`) covers the other
half of restart state, central's own key-value bucket, and passes with or
without the loop. The two together are the pattern: one test per kind of state
that has to cross a process boundary.

## Related

- `src/modules/edgebus/README.md`: the account boundary and what a leaf is
  permitted to address once it has authenticated.
- `docs/solutions/architecture-patterns/per-account-jetstream-disk-budgets-reserve-against-the-server-store-ceiling.md`:
  the other failure that only appears once the hub serves more than one edge,
  and the reason `ensureEdgeAccount` can fail during the re-attach loop.
