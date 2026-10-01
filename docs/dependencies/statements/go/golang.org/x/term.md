---
name: golang.org/x/term
ecosystem: go
required_by:
  - src/edge/netpen/go.mod
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

Package `cmd/netpen` imports `golang.org/x/term` at `src/edge/netpen/cmd/netpen/main.go:18`. The netpen `go.mod` pins version `v0.46.0`.

## Why it is safe

The publisher is the Go project. Version `v0.46.0` was published at `2026-09-08T16:27:41Z` and was 23 days old on 2026-10-01. Its 14-day wait ended at `2026-09-22T16:27:41Z`, and the Go proxy reported origin commit `6226200ed12cba417a9d9e799c2a7179d3fc0e27`. The OSV lookup dated 2026-10-01 returned no advisory for this version. The netpen module graph contains 3 versions, with 0 versions only reachable through this direct dependency.

## Why not owned code

The netpen CLI would have to implement terminal mode changes and restoration for its interactive command path. That code is platform-specific and unrelated to the network experiment itself. The tree contains 3 versions, with 0 versions only reachable through this direct dependency.
