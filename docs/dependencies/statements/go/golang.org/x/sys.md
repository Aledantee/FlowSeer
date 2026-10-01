---
name: golang.org/x/sys
ecosystem: go
required_by:
  - go.mod
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

Packages `common/service` and `services/device/internal/credential` import `golang.org/x/sys` at `src/common/service/storelock_posix.go:9` and `src/services/device/internal/credential/provider.go:13`. The root `go.mod` pins version `v0.48.0`.

## Why it is safe

The publisher is the Go project. Version `v0.48.0` was published at `2026-08-31T19:43:43Z` and was 31 days old on 2026-10-01. Its 14-day wait ended at `2026-09-14T19:43:43Z`, and the Go proxy reported origin commit `613e2570718ecde85c04e69ebd5585c3881c442c`. The OSV lookup dated 2026-10-01 returned no advisory for this version. The root module graph contains 2 versions, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to maintain Unix and Windows syscall wrappers for file locking, credential access, and packet handling. A local wrapper would repeat operating-system details already shared by those packages. The tree contains 2 versions, with 0 versions only reachable through this direct dependency.
