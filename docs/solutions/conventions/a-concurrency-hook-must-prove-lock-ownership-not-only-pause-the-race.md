---
title: A Concurrency Hook Must Prove Lock Ownership, Not Only Pause the Race
date: 2026-09-30
last_verified: 2026-09-30
category: conventions
module: src/modules/edgebus
problem_type: convention
component: concurrency
severity: high
applies_when:
  - "Testing a close or shutdown race with an injected hook that is documented to run under a lock"
  - "Using TryLock in a test to prove a hook runs inside a lock"
  - "Reviewing a concurrency test that pauses an interleaving without checking lock ownership or protected state"
related_components: [service_layer, testing_framework]
tags: [concurrency, locks, trylock, race-test, test-hooks]
---

# A concurrency hook must prove lock ownership, not only pause the race

Pausing at an injected hook makes an interleaving reproducible. It does not
prove that the production lock still protects the hook. A test for a lock
contract needs an assertion that another writer cannot acquire the lock and an
assertion that the protected state has not crossed the boundary.

`Hub.mu` protects `closed` and serializes account enablement with shutdown
(`src/modules/edgebus/hub.go:99-106`). `ensureEdgeAccount` holds a read lock
from before CONNECT through the account flag read
(`hub.go:445-463`). `Close` takes the write lock before it sets `closed` and
shuts down the server (`hub.go:597-621`).

Each close-race hook calls `TryLock`. If it succeeds, the test records a
failure. At the flag-read stage it also reads `hub.closed` while the attach
read lock is held. After releasing the hook, the test checks that no lock
failure was recorded and then checks the expected storage or hub error
(`src/modules/edgebus/storage_test.go:164-229` and `:241-312`).

```go
if hub.mu.TryLock() {
	hub.mu.Unlock()
	lockFailures <- stage
}
if hub.closed {
	lockFailures <- stage
}
```

The same assertions run for a refused account and a fitting account. This
keeps the lock property separate from the returned error classification. A
mutation that releases the read lock before the flag read lets `TryLock`
succeed or lets `Close` mark the hub closed, so the test fails at the
interleaving where the contract was broken.

## Evidence

- `src/modules/edgebus/hub.go:99-104` documents that the attach hook runs under
  the hub read lock.
- `src/modules/edgebus/hub.go:445-463` places the two hook stages inside the
  read-lock interval.
- `src/modules/edgebus/hub.go:597-621` holds the write lock while shutdown
  clears the hub state.
- `src/modules/edgebus/storage_test.go:165-180` and `:242-257` test lock
  availability and the protected `closed` state.
- `src/modules/edgebus/storage_test.go:219-229` and `:296-312` drain the
  hook failures and assert the distinct error outcomes.

## What this does not cover

This pattern does not prove that every access to the protected state uses the
same lock. It proves the contract at the controlled interleaving. Pair it with
the race detector and ordinary tests for accesses outside the hook.
