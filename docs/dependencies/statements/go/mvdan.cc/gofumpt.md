---
name: mvdan.cc/gofumpt
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: keep
approved: 2026-10-01
---

## Why it is required

The `mibgen` generator imports `mvdan.cc/gofumpt/format` at `src/protocol/snmp/cmd/mibgen/emit.go:16`. Its caller is `src/protocol/snmp/cmd/mibgen`, and the root `go.mod` pins version `v0.12.0`.

## Why it is safe

The publisher is the mvdan.cc Go formatting project. Version `v0.12.0` was published at `2026-09-07T22:34:18Z` and was 24 days old on 2026-10-01. Its 14-day wait ended at `2026-09-21T22:34:18Z`, and the Go proxy reported origin commit `3e07e7e70ac93761d8e79ca0083a19e3d59f753d`. The OSV lookup dated 2026-10-01 returned no advisory for this version. The root module graph contains 12 versions, with 4 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to format generated Go source and keep import grouping consistent in `mibgen`. The repository requires gofumpt's stricter formatting, which `go/format` does not provide. A local formatter would make one generator maintain its own Go formatting rules. The tree contains 12 versions, with 4 versions only reachable through this direct dependency.
