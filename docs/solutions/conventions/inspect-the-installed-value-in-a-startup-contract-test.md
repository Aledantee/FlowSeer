---
title: Inspect the installed value in a startup contract test
date: 2026-10-16
last_verified: 2026-10-16
category: conventions
module: src/modules/edgebus
problem_type: convention
component: testing_framework
severity: high
applies_when:
  - "Testing claims or configuration installed by a constructor or startup path when the test can also build a fresh value with the same helper"
related_components: [messaging]
tags: [testing, mutation-testing, false-test]
---

# Inspect the installed value in a startup contract test

A startup test can call the real constructor and still inspect an unrelated
value. `TestCentralAccountCarriesNoImportsOrExports` once started a hub, then
signed a fresh CENTRAL JWT through `accountJWT`. That checked the signing
helper while bypassing the claims `StartHub` installed. Commit `2214b8ff`
replaced that second signing call with a fetch from the running server.

The distinction matters when startup transforms a helper's output before
installing it. `StartHub` signs CENTRAL's claims and stores the encoded value
in its resolver (`src/modules/edgebus/hub.go:159-169`):

```go
centralJWT, err := keys.accountJWT(keys.central, "CENTRAL", centralBudget)
```

```go
if err := resolver.Store(pub, encoded); err != nil {
```

Fetch that installed value before asserting its contents. The current test
does this at `src/modules/edgebus/hub_internal_test.go:32-41`:

```go
encoded, err := h.server.AccountResolver().Fetch(central)
```

```go
if len(claims.Imports) != 0 || len(claims.Exports) != 0 {
```

The resolver is the one registered on the server, and its `Fetch` reads the
same map that `Store` writes. These are explicit in nats-server v2.15.0,
`server/accounts.go:3380-3385` and `:4217-4229` in the Go module cache.

Commit `2214b8ff` records the discriminating mutation: adding an import to
`centralJWT` inside `StartHub`, before `resolver.Store`, makes the corrected
test report:

```text
central account has 1 imports and 0 exports, want none
```

When checking another startup contract, mutate the value between construction
and installation. A test that rebuilds the helper output has no observation
of that mutation. Keep helper tests for helper behavior, and read the
installed value when the assertion concerns startup.

This checks stored claims. It does not prove runtime authorization for every
request or replace tests that exercise denied operations.
