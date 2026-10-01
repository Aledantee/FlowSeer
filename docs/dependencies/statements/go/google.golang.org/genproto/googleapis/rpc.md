---
name: google.golang.org/genproto/googleapis/rpc
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

The importing package graph is rooted at `go.mod` and `src/protocol/yang/test/integration/testenv/testdata/gnmitarget/go.mod`. It requires `google.golang.org/genproto/googleapis/rpc` at `v0.0.0-20260928230214-8a89bd6388cc`.

## Why it is safe

The lockfile pins `v0.0.0-20260928230214-8a89bd6388cc`, and the inventory classifies this dependency as `deploy`. The `tools/deps age` and `tools/deps advisories` commands provide its publication and advisory evidence. Source not yet reviewed.

## Why not owned code

The repository does not own the external package `google.golang.org/genproto/googleapis/rpc`. Its pinned tree size is reported by `go run ./tools/deps tree`. Replacing it would move that package boundary and its maintenance into FlowSeer.
