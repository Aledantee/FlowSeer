---
name: go.opentelemetry.io/contrib/bridges/otelslog
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

The importing package graph is rooted at `go.mod`. It requires `go.opentelemetry.io/contrib/bridges/otelslog` at `v0.20.1`.

## Why it is safe

The lockfile pins `v0.20.1`, and the inventory classifies this dependency as `deploy`. The `tools/deps age` and `tools/deps advisories` commands provide its publication and advisory evidence. Source not yet reviewed.

## Why not owned code

The repository does not own the external package `go.opentelemetry.io/contrib/bridges/otelslog`. Its pinned tree size is reported by `go run ./tools/deps tree`. Replacing it would move that package boundary and its maintenance into FlowSeer.
