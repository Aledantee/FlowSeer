---
name: github.com/bufbuild/buf
ecosystem: go
required_by:
  - tools/buf/go.mod
criteria: run
verdict: keep
approved: ""
---

## Why it is required

The importing package graph is rooted at `tools/buf/go.mod`. It requires `github.com/bufbuild/buf` at `v1.73.0`.

## Why it is safe

The lockfile pins `v1.73.0`, and the inventory classifies this dependency as `run`. The `tools/deps age` and `tools/deps advisories` commands provide its publication and advisory evidence. Source not yet reviewed.

## Why not owned code

The repository does not own the external package `github.com/bufbuild/buf`. Its pinned tree size is reported by `go run ./tools/deps tree`. Replacing it would move that package boundary and its maintenance into FlowSeer.
