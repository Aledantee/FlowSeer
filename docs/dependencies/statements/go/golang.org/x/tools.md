---
name: golang.org/x/tools
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: keep
approved: 2026-10-01
---

## Why it is required

The `mibgen` generator imports `golang.org/x/tools/imports` at `src/protocol/snmp/cmd/mibgen/emit.go:15`. Its caller is `src/protocol/snmp/cmd/mibgen`, and the root `go.mod` pins version `v0.50.0`.

## Why it is safe

The publisher is the Go project. Version `v0.50.0` was published at `2026-09-08T19:59:56Z` and was 23 days old on 2026-10-01. Its 14-day wait ended at `2026-09-22T19:59:56Z`, and the Go proxy reported origin commit `265dd1a6ecf0ee85548c7a8d1787d25fc5675e06`. The OSV lookup dated 2026-10-01 returned no advisory for this version. The root module graph contains 14 versions, with 2 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to rewrite imports and format generated Go source in `mibgen`, including its package resolution rules. A local formatter would create a second implementation of Go import analysis for one generator. The tree contains 14 versions, with 2 versions only reachable through this direct dependency.
