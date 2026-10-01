---
name: golang.org/x/mod
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: keep
approved: 2026-10-01
---

## Why it is required

Tool packages `tools/deps/inventory` and `tools/deps/lookup` import `golang.org/x/mod` at `tools/deps/inventory/gomod.go:8`, `tools/deps/inventory/statements.go:10`, and `tools/deps/lookup/age.go:11`. The YANG generator imports it at `src/protocol/yang/cmd/yanggen/gomod.go:12`. The tool callers are `go run ./tools/deps` and `src/protocol/yang/cmd/yanggen`. The root `go.mod` pins version `v0.41.0`.

## Why it is safe

The publisher is the Go project. Version `v0.41.0` was published at `2026-08-24T20:56:42Z` and was 38 days old on 2026-10-01. Its 14-day wait ended at `2026-09-07T20:56:42Z`, and the Go proxy reported origin commit `d0a27b2d4a48460806692bf5c87fc157c3c65292`. The OSV lookup dated 2026-10-01 returned no advisory for this version. The root module graph contains 3 versions, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to parse the Go module grammar and reproduce module-path escaping for proxy requests. The inventory, statement gate, age lookup, and YANG generator all depend on those rules. The tree contains 3 versions, with 0 versions only reachable through this direct dependency.
