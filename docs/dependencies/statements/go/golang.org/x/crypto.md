---
name: golang.org/x/crypto
ecosystem: go
required_by:
  - go.mod
  - src/edge/netpen/go.mod
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

The importing package graph is rooted at `go.mod` and `src/edge/netpen/go.mod`. It requires `golang.org/x/crypto` at `v0.57.0`.

## Why it is safe

The lockfile pins `v0.57.0`, and the inventory classifies this dependency as `deploy`. The `tools/deps age` and `tools/deps advisories` commands provide its publication and advisory evidence. Source not yet reviewed.

## Why not owned code

The repository does not own the external package `golang.org/x/crypto`. Its pinned tree size is reported by `go run ./tools/deps tree`. Replacing it would move that package boundary and its maintenance into FlowSeer.
