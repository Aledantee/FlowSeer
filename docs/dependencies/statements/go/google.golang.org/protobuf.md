---
name: google.golang.org/protobuf
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

The importing package graph is rooted at `go.mod`. It requires `google.golang.org/protobuf` at `v1.36.12`.

## Why it is safe

The lockfile pins `v1.36.12`, and the inventory classifies this dependency as `deploy`. The `tools/deps age` and `tools/deps advisories` commands provide its publication and advisory evidence. Source not yet reviewed.

## Why not owned code

The repository does not own the external package `google.golang.org/protobuf`. Its pinned tree size is reported by `go run ./tools/deps tree`. Replacing it would move that package boundary and its maintenance into FlowSeer.
